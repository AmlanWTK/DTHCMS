package assessment

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical/calc"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Recording one questionnaire, and deriving the composite from what has been assessed (CP58).
//
// # Two writes, one act, and why they are not one transaction
//
// Answering a questionnaire appends `LIFESTYLE_ASSESSMENT_RECORDED`. Deriving the composite score
// appends an ordinary CP42 `OBSERVATION_RECORDED` for `LIFESTYLE_RISK`, through `clinical`, which
// is the one code path that knows how to write a derived value.
//
// They are separate events on purpose. The score is a *function of the record at a moment*, not a
// property of the questionnaire: the same answers produce a different score the day a weight moves
// or an activity number arrives from somewhere else, and a formula that is still a proposal (D-26)
// will be recomputed when it is agreed. Welding the two into one transaction would make the score
// look like part of the answer, and the day the formula changes there would be no honest way to
// re-derive without rewriting history.
//
// The consequence is stated plainly rather than hidden: a response can exist with no score, and
// `Record` reports whether one was derived and why not.

// Service records assessments.
type Service struct {
	store   *Store
	events  *eventstore.Store
	clock   interface{ Now() time.Time }
	derived Deriver

	// OnDeriveSkipped is told why a composite was not produced. Nil in production: a score that
	// could not be computed is not an error anybody should be woken for, and the API reports the
	// absence to the client that asked. A test sets it to see the reason.
	OnDeriveSkipped func(error)
}

// Deriver writes the composite as a clinical derived value.
//
// An interface rather than a direct call so that a test can watch what was derived without a
// clinical service behind it, and so that this package holds exactly one opinion about clinical
// writes: that it does not perform them itself.
type Deriver interface {
	RecordDerived(ctx context.Context, in clinical.Recording) (clinical.Observation, error)
}

// NewService builds one.
func NewService(store *Store, events *eventstore.Store, clk interface{ Now() time.Time }) *Service {
	return &Service{store: store, events: events, clock: clk}
}

// WithDeriver attaches the thing that can write a clinical derived value.
func (s *Service) WithDeriver(d Deriver) *Service {
	s.derived = d
	return s
}

// Recording is one questionnaire being answered.
type Recording struct {
	EventID   uuid.UUID
	PatientID uuid.UUID
	VisitID   *uuid.UUID

	InstrumentCode string
	Answers        []Answer

	LedgerSource eventstore.Source
}

// Record writes the answers and, if enough of the record exists, the composite score.
//
// The returned `derived` is nil when the score could not be computed, and `Scoring` then says
// which domains were assessed, which were not, and how many are needed. Silence would be worse:
// an operator who finished a questionnaire and saw no score would reasonably assume the save
// failed, and a client left to work out which domains are missing has to re-implement `derive`'s
// domain-to-source mapping — a second copy of a server decision, which will drift.
func (s *Service) Record(ctx context.Context, in Recording) (Response, *Scoring, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Response{}, nil, err
	}

	instruments, err := s.store.Instruments(ctx, false)
	if err != nil {
		return Response{}, nil, err
	}
	var instrument Instrument
	for _, candidate := range instruments {
		if candidate.Code == in.InstrumentCode {
			instrument = candidate
		}
	}
	if instrument.Code == "" {
		return Response{}, nil, fmt.Errorf("%w: %s", ErrUnknownInstrument, in.InstrumentCode)
	}
	// D-26, refused rather than silently skipped. An operator who was shown a form must be told
	// why it will not save, and a clinic that never sees this refusal never chases the licence.
	if !instrument.Usable {
		return Response{}, nil, fmt.Errorf("%w: %s", ErrInstrumentNotLicensed, instrument.Code)
	}

	version, _, items, err := s.store.published(ctx, instrument.Code)
	if err != nil {
		return Response{}, nil, err
	}

	scored, err := score(items, in.Answers)
	if err != nil {
		return Response{}, nil, err
	}

	responseID := uuid.New()
	now := s.clock.Now().UTC()
	payload := eventstore.LifestyleAssessmentRecorded{
		ResponseID:        responseID.String(),
		FacilityID:        actor.FacilityID().String(),
		PatientID:         in.PatientID.String(),
		InstrumentCode:    instrument.Code,
		InstrumentVersion: version,
		Answers:           scored,
		RecordedAt:        now,
	}
	if in.VisitID != nil {
		payload.VisitID = in.VisitID.String()
	}
	if err := s.append(ctx, in.EventID, in.PatientID, in.VisitID, actor, in.LedgerSource,
		now, payload); err != nil {
		return Response{}, nil, err
	}

	response, err := s.store.Live(ctx, in.PatientID, instrument.Code, actor.FacilityID())
	if err != nil {
		return Response{}, nil, err
	}

	scoring := s.Score(ctx, in.PatientID, in.VisitID, actor, in.LedgerSource)
	// A score that could not be computed is not a failed assessment. The answers are in the
	// ledger; the composite is a separate fact and a later one, and refusing the whole write
	// because four domains were not available would mean a station could never record the first
	// questionnaire of a patient's life.
	return response, scoring, nil
}

// Scoring is the composite, or the reason there is not one.
//
// The reason is on the payload rather than left to a client to work out, because working it out
// means re-implementing which observation feeds which domain — and the day somebody adds a fifth
// domain, every client that guessed is silently wrong about what is still needed.
type Scoring struct {
	// Score is the composite, absent when fewer than Minimum domains have been assessed.
	Score *clinical.Observation `json:"score"`
	// Assessed and Missing are the domain names, so a screen can say what is still wanted rather
	// than only that something is.
	Assessed []string `json:"assessed"`
	Missing  []string `json:"missing"`
	Minimum  int      `json:"minimum"`
}

// Score computes the composite from the record as it stands, and says what it was computed from.
//
// Exported and callable on its own, because the commonest sequence at station 3 is "type the four
// numbers, save" — which can take a patient from two assessed domains to four without any
// questionnaire being answered. A score that could only be produced by submitting a questionnaire
// would be stale exactly when the operator had just finished making it computable.
func (s *Service) Score(ctx context.Context, patient uuid.UUID, visit *uuid.UUID,
	actor eventstore.Actor, source eventstore.Source) *Scoring {

	inputs, assessed, missing := s.domains(ctx, patient, actor.FacilityID())
	out := &Scoring{Assessed: assessed, Missing: missing, Minimum: calc.MinimumLifestyleDomains}

	if len(assessed) < calc.MinimumLifestyleDomains || s.derived == nil {
		return out
	}
	observation, err := s.write(ctx, inputs, len(assessed), patient, visit, source)
	if err != nil {
		s.noteDeriveFailure(ctx, err)
		return out
	}
	out.Score = observation
	return out
}

// score checks each answer against the item it claims to answer and attaches what it was worth.
//
// **The score comes from the option table, never from the request.** A client that could send a
// score could send any total it liked, and the total is what §12 cohorts on.
func score(items []Item, answers []Answer) ([]eventstore.InstrumentAnswer, error) {
	given := map[string]bool{}
	out := make([]eventstore.InstrumentAnswer, 0, len(answers))

	for _, answer := range answers {
		item, ok := find(items, trimmed(answer.ItemCode))
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownItem, answer.ItemCode)
		}
		if given[item.ItemCode] {
			return nil, fmt.Errorf("%w: %s", ErrUnknownItem, answer.ItemCode)
		}
		given[item.ItemCode] = true

		encoded := eventstore.InstrumentAnswer{ItemCode: item.ItemCode}
		switch item.AnswerType {
		case "coded":
			if answer.OptionCode == "" || answer.ValueNum != nil || answer.ValueBool != nil {
				return nil, fmt.Errorf("%w: %s expects one of its options", ErrWrongAnswerShape, item.ItemCode)
			}
			points, ok := known(item, answer.OptionCode)
			if !ok {
				return nil, fmt.Errorf("%w: %s is not offered for %s",
					ErrUnknownOption, answer.OptionCode, item.ItemCode)
			}
			encoded.OptionCode = answer.OptionCode
			encoded.Score = points

		case "numeric":
			if answer.ValueNum == nil || answer.OptionCode != "" || answer.ValueBool != nil {
				return nil, fmt.Errorf("%w: %s expects a number", ErrWrongAnswerShape, item.ItemCode)
			}
			if item.MinValue != nil && *answer.ValueNum < *item.MinValue {
				return nil, fmt.Errorf("%w: %s", ErrOutOfRange, item.ItemCode)
			}
			if item.MaxValue != nil && *answer.ValueNum > *item.MaxValue {
				return nil, fmt.Errorf("%w: %s", ErrOutOfRange, item.ItemCode)
			}
			value := *answer.ValueNum
			encoded.ValueNum = &value
			// A numeric item carries no score of its own. Scoring a free number would need a
			// band table nobody has agreed, and inventing one here would be inventing clinical
			// evidence — the number is stored and the composite reads it directly.

		case "boolean":
			if answer.ValueBool == nil || answer.OptionCode != "" || answer.ValueNum != nil {
				return nil, fmt.Errorf("%w: %s expects yes or no", ErrWrongAnswerShape, item.ItemCode)
			}
			value := *answer.ValueBool
			encoded.ValueBool = &value
		}
		out = append(out, encoded)
	}

	for _, item := range items {
		if item.Required && !given[item.ItemCode] {
			return nil, fmt.Errorf("%w: %s", ErrItemMissing, item.ItemCode)
		}
	}
	if len(out) == 0 {
		return nil, ErrItemMissing
	}
	return out, nil
}

func (s *Service) append(ctx context.Context, eventID, patient uuid.UUID, visit *uuid.UUID,
	actor eventstore.Actor, source eventstore.Source, now time.Time, payload any) error {

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
		EventType: "LIFESTYLE_ASSESSMENT_RECORDED", EventVersion: 1, OccurredAt: now,
		Actor: actor, Source: source, Payload: encoded,
	}
	return s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbgen.Queries) error {
		_, err := s.events.AppendInTx(ctx, tx, envelope)
		return err
	})
}

// InTransaction runs fn against a transaction on this store's pool.
func (s *Store) InTransaction(ctx context.Context,
	fn func(context.Context, pgx.Tx, *dbgen.Queries) error) error {

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(ctx, tx, s.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// noteDeriveFailure records why no score was produced. Errors here are ordinary — three domains
// is a floor, not a bug — so this is a hook rather than a log call, and the tests set it.
func (s *Service) noteDeriveFailure(_ context.Context, err error) {
	if s.OnDeriveSkipped != nil {
		s.OnDeriveSkipped(err)
	}
}

// Standing is the same account **without writing anything**.
//
// A screen must be able to ask "where does this patient stand" on open, and the writing version
// cannot answer that: a derived observation appended every time somebody opens a tab is ledger
// noise, so a screen that used it would either pollute the record or — as the station screen did —
// show no missing-list at all until somebody saved something.
//
// It is also what the timeline and the physician dashboard will want, which read a stored
// `LIFESTYLE_RISK` alone and otherwise meet exactly the gap this closes: the value carries how
// many domains went into it and not which.
func (s *Service) Standing(ctx context.Context, patient, facility uuid.UUID) *Scoring {
	_, assessed, missing := s.domains(ctx, patient, facility)
	return &Scoring{Assessed: assessed, Missing: missing, Minimum: calc.MinimumLifestyleDomains}
}

// domains reads each part of the composite from wherever it already lives — pack-years is derived,
// AUDIT-C is a questionnaire total, sleep and activity are plain observations — because a composite
// that required its own copies of four numbers would be a fifth place they could disagree.
//
// It returns the names of what was and was not assessed, in the order a screen would list them.
// **An absent domain is left out, never counted as zero**: the obvious alternative means the
// patient who answered nothing scores best, and the operator who ran the whole questionnaire has
// produced a worse-looking record than the one who ran none of it.
func (s *Service) domains(ctx context.Context, patient, facility uuid.UUID) (
	calc.LifestyleInputs, []string, []string) {

	inputs := calc.LifestyleInputs{}
	var assessed, missing []string

	if value, ok := s.liveValue(ctx, patient, facility, "PACK_YEARS"); ok {
		inputs.PackYears = &value
		assessed = append(assessed, "SMOKING")
	} else {
		missing = append(missing, "SMOKING")
	}

	if response, err := s.store.Live(ctx, patient, "AUDIT_C", facility); err == nil {
		total := float64(response.Total)
		inputs.AuditC = &total
		assessed = append(assessed, "ALCOHOL")
	} else {
		missing = append(missing, "ALCOHOL")
	}

	if value, ok := s.liveValue(ctx, patient, facility, "SLEEP_MINUTES"); ok {
		// Stored in canonical minutes; the formula's bands are written in hours, because that is
		// how anybody argues about sleep. The conversion is here, at the edge.
		hours := value / 60
		inputs.SleepHours = &hours
		assessed = append(assessed, "SLEEP")
	} else {
		missing = append(missing, "SLEEP")
	}

	if value, ok := s.liveValue(ctx, patient, facility, "ACTIVE_MINUTES_WEEK"); ok {
		inputs.ActiveMinutesWeek = &value
		assessed = append(assessed, "ACTIVITY")
	} else {
		missing = append(missing, "ACTIVITY")
	}
	return inputs, assessed, missing
}

// write computes the composite and stores it as a clinical derived value (CP42's mechanism, so it
// inherits the correction cascade, the timeline, the research extract and the attribution).
func (s *Service) write(ctx context.Context, inputs calc.LifestyleInputs, assessed int,
	patient uuid.UUID, visit *uuid.UUID, source eventstore.Source) (*clinical.Observation, error) {

	result, domains, err := calc.LifestyleRisk(inputs)
	if err != nil {
		return nil, err
	}
	named := map[string]float64{
		// How many domains went into it, on the value itself. A score from three domains is a
		// different number from a score from four, and a reader with only the number cannot tell.
		"domains": float64(domains),
	}
	if inputs.PackYears != nil {
		named["pack_years"] = *inputs.PackYears
	}
	if inputs.AuditC != nil {
		named["audit_c"] = *inputs.AuditC
	}
	if inputs.SleepHours != nil {
		named["sleep_hours"] = *inputs.SleepHours
	}
	if inputs.ActiveMinutesWeek != nil {
		named["active_minutes_week"] = *inputs.ActiveMinutesWeek
	}
	_ = assessed

	value := result.Value
	observation, err := s.derived.RecordDerived(ctx, clinical.Recording{
		EventID: uuid.New(), PatientID: patient, VisitID: visit,
		Code: "LIFESTYLE_RISK", Value: &value, Unit: "1",
		EffectiveAt: s.clock.Now().UTC(),
		// `Station` because that is what CP43's own derivations use: the evidence class of a
		// derived value is the evidence class of the values it was computed from.
		Source:       clinical.Station,
		LedgerSource: source,
		Formula:      result.Formula, Version: result.Version, Inputs: named,
	})
	if err != nil {
		return nil, err
	}
	return &observation, nil
}

// liveValue is the patient's current value for one code, or nothing.
func (s *Service) liveValue(ctx context.Context, patient, facility uuid.UUID,
	code string) (float64, bool) {

	var value float64
	err := s.store.pool.QueryRow(ctx, `
		SELECT value_num FROM read.observation
		 WHERE patient_id = $1 AND facility_id = $2 AND code = $3 AND status = 'ACTIVE'
		 ORDER BY effective_at DESC, global_seq DESC
		 LIMIT 1`, patient, facility, code).Scan(&value)
	if err != nil {
		return 0, false
	}
	return value, true
}
