package exercise

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Recording and issuing, and the one refusal that is the whole checkpoint.
//
// # Where the filter is applied, and how many times
//
// Three times, deliberately, and each one catches something the others cannot:
//
//  1. `Options` computes the permitted set in the database, from the recorded assessment. This is
//     what the operator sees, and the excluded exercises never leave the process.
//  2. `Issue` re-computes it and refuses any target outside it. A client that kept an old list,
//     or that never asked for one, or that was written by somebody who did not read this file,
//     is refused here — because criterion 1 must not depend on a screen behaving.
//  3. `read.exercise_plan_item_is_permitted` refuses it again on the way into the read model,
//     which is the copy that holds under a projection rebuild.
//
// Three checks of the same rule reads like belt and braces. It is not: they guard three different
// failure modes — a bad screen, a bad client, and a bad replay — and the plan's manual
// verification is about the second.

// Service records assessments and issues plans.
type Service struct {
	store  *Store
	events *eventstore.Store
	clock  interface{ Now() time.Time }
}

// NewService builds one.
func NewService(store *Store, events *eventstore.Store, clk interface{ Now() time.Time }) *Service {
	return &Service{store: store, events: events, clock: clk}
}

// Recording is one exercise assessment being taken.
type Recording struct {
	EventID   uuid.UUID
	PatientID uuid.UUID
	VisitID   *uuid.UUID

	WalksUnaided *bool
	WalkMinutes  *int
	JointPain    string

	// Contraindications is what applies; Asked is what was put to the patient. Both come from
	// the station, and every live condition has to be in the second or the assessment is refused.
	Contraindications []string
	Asked             []string
	Note              string

	LedgerSource eventstore.Source
}

// Record writes the findings and returns them with the options they permit.
//
// The options come back on the same response on purpose. The alternative — record, then fetch —
// leaves a window in which the station holds findings and no list, and the natural thing for a
// client to do in that window is show the library it already has. Returning both makes the
// permitted list the only list the screen ever holds.
func (s *Service) Record(ctx context.Context, in Recording) (Assessment, Options, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Assessment{}, Options{}, err
	}

	live, err := s.store.LiveConditionCodes(ctx)
	if err != nil {
		return Assessment{}, Options{}, err
	}
	known := make(map[string]bool, len(live))
	for _, code := range live {
		known[code] = true
	}

	// Every live condition has to be answered. Without this the filter cannot tell a skipped
	// question from a negative answer, and a station that never showed the neuropathy question
	// would produce an assessment byte-identical to a careful one — after which
	// `core.exercises_permitted` computes the list as though the answer were no.
	//
	// The refusal names what is missing rather than saying "incomplete", because a client working
	// from a stale catalogue (an offline tablet, a rolling deploy) has to know which questions to
	// fetch and put before it can record.
	asked := map[string]bool{}
	for _, code := range in.Asked {
		if !known[code] {
			return Assessment{}, Options{}, fmt.Errorf("%w: %s", ErrUnknownContraindication, code)
		}
		asked[code] = true
	}
	missing := make([]string, 0, len(live))
	for _, code := range live {
		if !asked[code] {
			missing = append(missing, code)
		}
	}
	if len(missing) > 0 {
		return Assessment{}, Options{},
			fmt.Errorf("%w: %s", ErrIncompleteAssessment, strings.Join(missing, ", "))
	}

	// A code that is not in the catalogue is refused rather than stored. An assessment holding a
	// typo'd condition would filter nothing and look exactly like one that filtered correctly —
	// which is the failure mode this checkpoint is least able to afford.
	seen := map[string]bool{}
	conditions := make([]string, 0, len(in.Contraindications))
	for _, code := range in.Contraindications {
		if !known[code] {
			return Assessment{}, Options{}, fmt.Errorf("%w: %s", ErrUnknownContraindication, code)
		}
		if !asked[code] {
			// Invariant 89, at the door. A finding about a question nobody put is either a client
			// bug or a claim nobody made, and both are worse stored than refused.
			return Assessment{}, Options{},
				fmt.Errorf("%w: %s", ErrFindingWasNotAsked, code)
		}
		if seen[code] {
			continue
		}
		seen[code] = true
		conditions = append(conditions, code)
	}
	// **What the client said it asked**, deduplicated and put in catalogue order so that two
	// assessments of the same patient compare as strings without a client having to care.
	//
	// Built from `in.Asked` rather than from `live`, even though the two are provably the same
	// set today — unknown codes are refused above and missing ones are refused above. The
	// difference is what the ledger *claims*: `live` would record what the catalogue contained,
	// and `in.Asked` records what the station said it put to the patient. If the coverage rule is
	// ever relaxed — a partial assessment, an offline reconciliation — the first silently starts
	// writing a stronger claim than anybody made, which is the exact failure this field was added
	// to prevent, one level up.
	questions := make([]string, 0, len(live))
	for _, code := range live {
		if asked[code] {
			questions = append(questions, code)
		}
	}

	assessmentID := uuid.New()
	now := s.clock.Now().UTC()
	payload := eventstore.ExerciseAssessmentRecorded{
		AssessmentID: assessmentID.String(),
		FacilityID:   actor.FacilityID().String(),
		PatientID:    in.PatientID.String(),
		WalksUnaided: in.WalksUnaided,
		WalkMinutes:  in.WalkMinutes,
		JointPain:    in.JointPain,
		// Non-nil even when empty, so the payload carries `[]` rather than `null`: the projection
		// guards against a scalar, but an event that says "not asked" when it meant "none apply"
		// is a lie in the ledger, and the ledger is the copy that survives.
		Contraindications: conditions,
		Asked:             questions,
		Note:              in.Note,
		RecordedAt:        now,
	}
	if in.VisitID != nil {
		payload.VisitID = in.VisitID.String()
	}
	if err := s.append(ctx, in.EventID, in.PatientID, in.VisitID, actor, in.LedgerSource,
		now, "EXERCISE_ASSESSMENT_RECORDED", payload); err != nil {
		return Assessment{}, Options{}, err
	}

	assessment, err := s.store.AssessmentByID(ctx, assessmentID, actor.FacilityID())
	if err != nil {
		return Assessment{}, Options{}, err
	}
	options, err := s.store.Options(ctx, in.PatientID, actor.FacilityID())
	if err != nil {
		return Assessment{}, Options{}, err
	}
	return assessment, options, nil
}

// Issuing is a plan being given to a patient.
type Issuing struct {
	EventID   uuid.UUID
	PatientID uuid.UUID
	VisitID   *uuid.UUID

	// AssessmentID is the findings the operator was looking at when they chose. Required, and
	// checked against the current one: a plan built from a list that has since changed is a plan
	// built from the wrong list, and the operator has to see the new one before issuing.
	AssessmentID uuid.UUID

	Targets []Target
	Note    string

	LedgerSource eventstore.Source
}

// Issue writes the plan, refusing anything this patient's own assessment forbids.
//
// The refusal is the checkpoint. It is a 422 naming the exercise and the condition, not a warning
// and not a silent drop: an operator who chose stair climbing for somebody with a cardiac
// limitation has to be told which condition stopped it, or they will choose it again.
func (s *Service) Issue(ctx context.Context, in Issuing) (Plan, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Plan{}, err
	}
	if len(in.Targets) == 0 {
		return Plan{}, ErrEmptyPlan
	}

	assessment, err := s.store.LiveAssessment(ctx, in.PatientID, actor.FacilityID())
	if err != nil {
		return Plan{}, err
	}
	// The list the operator saw is the list they must still be on. Without this, a plan chosen
	// before a colleague recorded a foot ulcer would be issued against the pre-ulcer library, and
	// every check below would pass — because they would all be checking the wrong assessment.
	if assessment.ID != in.AssessmentID {
		return Plan{}, ErrStaleAssessment
	}

	options, err := s.store.Options(ctx, in.PatientID, actor.FacilityID())
	if err != nil {
		return Plan{}, err
	}
	// Keyed by code, and kept rather than reduced to a set of booleans, because every refusal
	// below names the exercise the way the operator saw it on the screen they chose it from.
	permitted := make(map[string]Exercise, len(options.Exercises))
	for _, e := range options.Exercises {
		permitted[e.Code] = e
	}

	items := make([]eventstore.ExerciseTarget, 0, len(in.Targets))
	chosen := map[string]bool{}
	for _, target := range in.Targets {
		if chosen[target.ExerciseCode] {
			// Refused rather than resolved by arrival order. Two entries for one exercise carry
			// two different weekly totals, and silently keeping the first would put a number in
			// §12.1's adherence data that nobody chose.
			en, bn := named(permitted, target.ExerciseCode)
			return Plan{}, &DuplicateTargetError{
				ExerciseCode: target.ExerciseCode, ExerciseEN: en, ExerciseBN: bn,
			}
		}
		chosen[target.ExerciseCode] = true

		if _, ok := permitted[target.ExerciseCode]; !ok {
			// Two different refusals wearing one shape would be a bug report nobody could act
			// on, so they are separated: an exercise that is in the library but excluded is
			// ErrContraindicated, and one that is not in the library at all is ErrUnknownExercise.
			row, err := s.store.knowsExercise(ctx, target.ExerciseCode)
			if err != nil {
				return Plan{}, err
			}
			if !row.Known {
				return Plan{}, &UnknownExerciseError{ExerciseCode: target.ExerciseCode}
			}
			if !row.Live {
				// Retired between the operator seeing the options and choosing from them. The
				// list moved; the request was not wrong when it was made.
				return Plan{}, &UnknownExerciseError{
					ExerciseCode: target.ExerciseCode,
					ExerciseEN:   row.NameEn, ExerciseBN: row.NameBn,
					Retired: true,
				}
			}
			refusal, err := s.store.whyExcluded(ctx, target.ExerciseCode, assessment)
			if err != nil {
				return Plan{}, err
			}
			return Plan{}, refusal
		}
		if target.TimesPerWeek < 1 || target.TimesPerWeek > 14 ||
			target.MinutesPerSession < 1 || target.MinutesPerSession > 240 {
			en, bn := named(permitted, target.ExerciseCode)
			return Plan{}, &NotCountableError{
				ExerciseCode: target.ExerciseCode, ExerciseEN: en, ExerciseBN: bn,
			}
		}
		ordering := target.Ordering
		if ordering == 0 {
			ordering = 100
		}
		items = append(items, eventstore.ExerciseTarget{
			ExerciseCode:      target.ExerciseCode,
			TimesPerWeek:      target.TimesPerWeek,
			MinutesPerSession: target.MinutesPerSession,
			Ordering:          ordering,
			Note:              target.Note,
		})
	}

	planID := uuid.New()
	now := s.clock.Now().UTC()
	payload := eventstore.ExercisePlanIssued{
		PlanID:       planID.String(),
		FacilityID:   actor.FacilityID().String(),
		PatientID:    in.PatientID.String(),
		AssessmentID: assessment.ID.String(),
		Items:        items,
		Note:         in.Note,
		IssuedAt:     now,
	}
	if in.VisitID != nil {
		payload.VisitID = in.VisitID.String()
	}
	if err := s.append(ctx, in.EventID, in.PatientID, in.VisitID, actor, in.LedgerSource,
		now, "EXERCISE_PLAN_ISSUED", payload); err != nil {
		return Plan{}, err
	}
	return s.store.LivePlan(ctx, in.PatientID, actor.FacilityID())
}

// knowsExercise says whether a code is in the library, and whether it is still live.
//
// Two answers rather than one, because "retired since you fetched the options" and "not an
// exercise" mean different things to a client: the first says the list moved, the second says the
// request is wrong. Not a "get the exercise" method, and the difference matters: it answers a
// yes-or-no question about a code the caller already named, and never hands back a row somebody
// could offer.
func (s *Store) knowsExercise(ctx context.Context, code string) (dbgen.KnowsExerciseRow, error) {
	return s.q.KnowsExercise(ctx, code)
}

// whyExcluded builds the refusal an operator actually reads.
//
// It names the exercise, names the condition and carries the sentence from the mapping table —
// in both languages — because "that is not allowed" sends an operator to the next high-impact
// option and a reason sends them to a conversation. That the reason is a row rather than a
// constant is the same decision that makes the mapping arguable: when somebody disagrees with an
// exclusion, this sentence is what they argue with.
func (s *Store) whyExcluded(ctx context.Context, code string, a Assessment) (error, error) {
	row, err := s.q.WhyExcluded(ctx, dbgen.WhyExcludedParams{
		ExerciseCode: code,
		Applying:     a.Contraindications,
		Asked:        a.Asked,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Excluded, but by nothing on this assessment: the exercise was retired between the
		// operator seeing the list and choosing from it. Rare, and worth its own answer rather
		// than a refusal that names a condition the patient does not have.
		return &UnknownExerciseError{ExerciseCode: code, Retired: true}, nil
	}
	if err != nil {
		return nil, err
	}
	return &ContraindicatedError{
		ExerciseCode:  code,
		ExerciseEN:    row.ExerciseEn,
		ExerciseBN:    row.ExerciseBn,
		ConditionCode: row.ConditionCode,
		ConditionEN:   row.ConditionEn,
		ConditionBN:   row.ConditionBn,
		ReasonEN:      row.ReasonEn,
		ReasonBN:      row.ReasonBn,
		Applies:       row.Applies,
	}, nil
}

func (s *Service) append(ctx context.Context, eventID, patient uuid.UUID, visit *uuid.UUID,
	actor eventstore.Actor, source eventstore.Source, now time.Time,
	eventType string, payload any) error {

	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if eventID == uuid.Nil {
		eventID = uuid.New()
	}
	if source == "" {
		source = eventstore.SourceWeb
	}
	envelope := eventstore.Envelope{
		EventID: eventID, AggregateType: "PATIENT", AggregateID: patient,
		PatientID: &patient, VisitID: visit,
		EventType: eventType, EventVersion: 1, OccurredAt: now,
		Actor: actor, Source: source, Payload: encoded,
	}
	return s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbgen.Queries) error {
		_, err := s.events.AppendInTx(ctx, tx, envelope)
		return err
	})
}
