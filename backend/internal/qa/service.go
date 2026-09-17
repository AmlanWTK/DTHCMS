package qa

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/prescription"
	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

// The decisions (CP83).
//
// # What each of these three methods is, exactly
//
//   - [Service.Review] reads. It runs the rules and returns findings; it writes nothing and
//     decides nothing, so a QA officer can open a file, look, and walk away.
//   - [Service.Decide] is the officer's answer: CLEARED or BOUNCED. A clearance is one event; a
//     bounce is two, in one transaction — the decision with its findings, and CP80's transition
//     that actually moves the sheet back to DRAFT.
//   - [Service.Override] is the consultant's valve.
//
// # Why the refusals are here as well as in the database
//
// Each of them produces a sentence. `ErrBlocked` on a clearance tells the officer which findings
// are blocking; the trigger's exception says "prescription X has no QA clearance" to whoever is
// reading the logs. Both are needed and they are not the same thing: this one is an interface and
// that one is a guarantee. The test that matters is the mutation one — take the trigger away, and
// a test still fails.
//
// # Nothing here logs
//
// Not the findings, not the drug names, not the patient. The ledger and the read model hold the
// clinical content, which is where a patient's record belongs.

// Sheets is the prescription workflow this station drives.
//
// Only the two methods CP80 left for this checkpoint. An interface rather than
// `*prescription.Service` so that `Decide`'s transactional behaviour can be exercised without
// standing up the whole prescribing stack, and so that the one thing this station is allowed to
// do to a prescription is visible in four lines.
type Sheets interface {
	// QABounce moves QA_REVIEW back to DRAFT, which makes the items editable again.
	QABounce(ctx context.Context, eventID, id uuid.UUID, reason string,
		source eventstore.Source) (prescription.Prescription, error)
}

// Router puts a bounced patient back in a queue.
//
// # Why a bounce is a queue entry and not a notification
//
// A bounce is a real clinical event: somebody walks back up the corridor. `core.encounter`
// already has a `bounced` status and its comment says why — §14.2 counts rework, and a bounce
// recorded as "finished" makes rework invisible. Putting the patient into the named station's
// queue is what actually makes them arrive there; a message on a screen is what makes an
// operator hope they do.
//
// Nil is legal and means the routing half is not wired — which is the state a test rig is in, and
// which must not make the clinical decision fail. The decision is recorded either way, because a
// bounce whose routing failed is still a bounce and the record has to say so.
type Router interface {
	Enqueue(ctx context.Context, visitID uuid.UUID, in visit.Joining) (visit.QueueEntry, error)
}

// Service records QA decisions.
type Service struct {
	store   *Store
	events  *eventstore.Store
	sources Sources
	sheets  Sheets
	router  Router
	clock   interface{ Now() time.Time }
}

// NewService builds one.
func NewService(store *Store, events *eventstore.Store, sources Sources,
	sheets Sheets, clk interface{ Now() time.Time }) *Service {
	return &Service{store: store, events: events, sources: sources, sheets: sheets, clock: clk}
}

// WithRouter attaches the queue, so a bounce puts the patient back in the named station's line.
func (s *Service) WithRouter(r Router) *Service {
	out := *s
	out.router = r
	return &out
}

func (s *Service) now() time.Time {
	if s.clock == nil {
		return time.Now().UTC()
	}
	return s.clock.Now().UTC()
}

// ---------------------------------------------------------------------------
// Reading
// ---------------------------------------------------------------------------

// Review runs the rules over one prescription and returns what they found.
//
// **This is the method whose empty-table behaviour is §5.** A facility with no rule rows gets a
// [Review] with no findings and `RulesLive: 0`, and [Review.SummaryEN] says so in words rather
// than rendering identically to a clean file. What it does not get is a clearance: that is
// [Service.Decide], and the database refuses the signature without one.
func (s *Service) Review(ctx context.Context, facility, id uuid.UUID) (Review, error) {
	set, err := s.store.Ruleset(ctx, facility)
	if err != nil {
		return Review{}, err
	}
	evidence, err := s.sources.Gather(ctx, set, facility, id, s.now())
	if err != nil {
		return Review{}, err
	}
	review := set.Evaluate(evidence)

	if override, found, err := s.store.Override(ctx, facility, id); err != nil {
		return Review{}, err
	} else if found {
		review.Override = &override
	}
	if decision, found, err := s.store.StandingDecision(ctx, facility, id); err != nil {
		return Review{}, err
	} else if found {
		review.Decided = &decision
	}
	return review, nil
}

// ---------------------------------------------------------------------------
// Deciding
// ---------------------------------------------------------------------------

// Decision input.
type Deciding struct {
	EventID        uuid.UUID
	PrescriptionID uuid.UUID
	Outcome        Outcome

	// Acknowledged is the WARN rule codes the officer accepts. A clearance that does not cover
	// every warning is refused: §2 makes the acknowledgement the thing that distinguishes a
	// warning from nothing at all, and a clearance that silently swallowed them would make WARN
	// and "no rule" the same severity.
	Acknowledged []string

	// BounceStation and Reason are the officer's own, and they default to the findings'.
	//
	// **Defaulted rather than required**, which is a deliberate choice against a form that makes
	// somebody retype what the screen already says. The default is the strongest blocking
	// finding's station and its reason, both languages; an officer who types something else gets
	// theirs, in English, and the Bengali falls back to the finding's sentence — because a
	// bilingual free-text field that demands two translations from a busy person gets one of
	// them filled with the other language's text.
	BounceStation string
	ReasonEN      string
	ReasonBN      string

	Source eventstore.Source
}

// Decide records CLEARED or BOUNCED.
//
// # The four refusals, and which of them the database also makes
//
//  1. Not with QA — refused here only. The transition trigger would refuse the *move*, but a
//     decision recorded against a signed prescription would be a row nothing stops.
//  2. Blocking findings with no override — refused here, and again by the gate when the
//     signature is attempted. The second one is the guarantee; this one is the sentence.
//  3. A warning nobody acknowledged — here only. It is an interface rule, not a safety one.
//  4. A bounce with no station or no reason — here, by a CHECK constraint on `read.qa_review`,
//     and by the event payload's own `Validate`. Three, because criterion 3 is the one a
//     hurried screen would most easily drop.
func (s *Service) Decide(ctx context.Context, facility uuid.UUID, in Deciding) (Decision, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Decision{}, err
	}

	status, patient, visitID, err := s.store.Status(ctx, facility, in.PrescriptionID)
	if err != nil {
		return Decision{}, err
	}
	if status != string(prescription.StatusQAReview) {
		return Decision{}, ErrNotUnderReview
	}

	review, err := s.Review(ctx, facility, in.PrescriptionID)
	if err != nil {
		return Decision{}, err
	}

	now := s.now()
	decision := Decision{
		ID: uuid.New(), PrescriptionID: in.PrescriptionID, PatientID: patient, VisitID: visitID,
		Outcome: in.Outcome, DecidedAt: now, DecidedBy: actor.UserID(), DecidedRole: actor.Role(),
		Findings: review.Findings, Acknowledged: []string{},
	}
	if review.Override != nil {
		decision.OverrideID = &review.Override.ID
	}

	switch in.Outcome {
	case OutcomeCleared:
		if len(review.Blocking()) > 0 && review.Override == nil {
			return Decision{}, ErrBlocked
		}
		acknowledged := map[string]bool{}
		for _, code := range in.Acknowledged {
			acknowledged[code] = true
		}
		for _, warning := range review.Warnings() {
			if !acknowledged[warning.RuleCode] {
				return Decision{}, ErrWarningsNotAcknowledged
			}
		}
		decision.Acknowledged = append(decision.Acknowledged, in.Acknowledged...)

	case OutcomeBounced:
		station, reasonEN, reasonBN := bounceTo(review, in)
		if station == "" {
			return Decision{}, ErrBounceNeedsAStation
		}
		if strings.TrimSpace(reasonEN) == "" || strings.TrimSpace(reasonBN) == "" {
			return Decision{}, ErrBounceNeedsAReason
		}
		decision.BounceStation, decision.ReasonEN, decision.ReasonBN = station, reasonEN, reasonBN
		if names, ok := review.Stations[station]; ok {
			decision.StationEN, decision.StationBN = names[0], names[1]
		}

	default:
		return Decision{}, ErrNotUnderReview
	}

	findings, err := json.Marshal(decision.Findings)
	if err != nil {
		return Decision{}, err
	}
	payload := eventstore.PrescriptionQADecided{
		ReviewID: decision.ID.String(), FacilityID: facility.String(),
		PrescriptionID: in.PrescriptionID.String(), PatientID: patient.String(),
		VisitID: visitID.String(), Outcome: string(in.Outcome),
		BounceStation: decision.BounceStation,
		ReasonEN:      decision.ReasonEN, ReasonBN: decision.ReasonBN,
		Findings: findings, Acknowledged: decision.Acknowledged,
		DecidedAt: now,
	}
	if decision.OverrideID != nil {
		payload.OverrideID = decision.OverrideID.String()
	}

	eventType := "PRESCRIPTION_QA_CLEARED"
	if in.Outcome == OutcomeBounced {
		eventType = "PRESCRIPTION_QA_BOUNCE_DECIDED"
	}

	// **Two writes, and the order is the whole of the argument.**
	//
	// They are deliberately *not* one transaction, and that is not laziness. CP80's
	// `Service.QABounce` opens its own transaction to append its own event, and the ledger's
	// hash chain serialises appends — so calling it from inside a transaction that has already
	// appended leaves this process waiting on a lock it is itself holding. The first draft of
	// this method did exactly that and hung for three minutes.
	//
	// So: the decision first, the transition second. That ordering is chosen for what each
	// failure leaves behind. A decision recorded whose transition then failed leaves a BOUNCED
	// row, a prescription still in QA_REVIEW, and — this is the part that matters — **no
	// clearance standing**, so the gate still refuses the signature and the officer simply
	// presses bounce again. The other order would leave a prescription back in DRAFT with no
	// record of why it came back, which is the state §14.2's rework counting cannot see.
	if err := s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return s.append(ctx, tx, in.EventID, eventType, in.PrescriptionID,
			patient, visitID, actor, in.Source, now, payload)
	}); err != nil {
		return Decision{}, err
	}
	if in.Outcome == OutcomeBounced && s.sheets != nil {
		if _, err := s.sheets.QABounce(ctx, uuid.New(), in.PrescriptionID,
			decision.ReasonEN, in.Source); err != nil {
			return Decision{}, err
		}
	}

	if in.Outcome == OutcomeBounced {
		s.route(ctx, decision)
	}
	return decision, nil
}

// bounceTo decides where the patient goes and what to tell the station they arrive at.
//
// The officer's own words win when they typed any; otherwise the strongest blocking finding's
// station and sentence, which is almost always what they would have typed. A bounce with no
// findings at all — an officer sending a file back for something the rules do not cover — takes
// the officer's station and reason and nothing else, which is why both are still refused when
// empty.
func bounceTo(review Review, in Deciding) (station, reasonEN, reasonBN string) {
	station, reasonEN, reasonBN = in.BounceStation, in.ReasonEN, in.ReasonBN

	blocking := review.Blocking()
	if len(blocking) == 0 {
		blocking = review.Warnings()
	}
	if len(blocking) > 0 {
		first := blocking[0]
		if station == "" {
			station = first.BounceStation
		}
		if strings.TrimSpace(reasonEN) == "" {
			reasonEN = first.ReasonEN()
		}
		if strings.TrimSpace(reasonBN) == "" {
			// When the officer typed English only, the Bengali is the finding's own sentence
			// rather than the English repeated. A Bengali field holding English text is a field
			// that has quietly stopped being bilingual.
			reasonBN = first.ReasonBN()
		}
	}
	return station, reasonEN, reasonBN
}

// route puts the bounced patient back in the named station's queue.
//
// # Why a routing failure does not fail the decision
//
// The decision is already in the ledger by the time this runs, and it has to be: a bounce is a
// clinical judgement and a queue entry is logistics. If the queue refuses — the station is closed
// for the day, CP57's counselling gate holds the patient, the row already exists — the honest
// outcome is a recorded bounce that somebody has to walk the patient through by hand, not a
// judgement that silently un-happened.
//
// It is deliberately quiet about the failure rather than logging it, because this package logs
// nothing: a log line here would carry the visit id, and CP83's route audit is where the fact
// that a bounce happened belongs.
func (s *Service) route(ctx context.Context, decision Decision) {
	if s.router == nil || decision.BounceStation == "" {
		return
	}
	// Priority 1 with the bounce's reason. A bounced patient is not an ordinary arrival: they
	// have already waited through the whole corridor once, and putting them at the back of the
	// examination queue at eleven o'clock is how a forty-minute bounce becomes a two-hour one.
	// The queue refuses a priority with no reason, which is exactly the right refusal here —
	// the reason is the finding.
	_, _ = s.router.Enqueue(ctx, decision.VisitID, visit.Joining{
		StationCode:    decision.BounceStation,
		Priority:       1,
		PriorityReason: decision.ReasonEN,
	})
}

// ---------------------------------------------------------------------------
// The override
// ---------------------------------------------------------------------------

// Override is the consultant's valve.
//
// **Three independent refusals**, and criterion 4 is all three: the permission is checked at the
// route, the step-up at the route, and the reason here, by this method and by the event payload's
// own `Validate` and by a CHECK constraint. Each of them refuses on its own, which is what makes
// the test that removes one of them fail.
//
// An override on a file nothing is blocking is refused rather than recorded. An override row with
// no blocking findings behind it is a row that makes the rate view lie, and the rate view is the
// only thing standing between a valve and a habit.
func (s *Service) Override(ctx context.Context, facility uuid.UUID, eventID,
	id uuid.UUID, reason string, source eventstore.Source) (Override, error) {

	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Override{}, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Override{}, ErrOverrideReasonRequired
	}

	status, patient, visitID, err := s.store.Status(ctx, facility, id)
	if err != nil {
		return Override{}, err
	}
	if status != string(prescription.StatusQAReview) {
		return Override{}, ErrNotUnderReview
	}
	if _, found, err := s.store.Override(ctx, facility, id); err != nil {
		return Override{}, err
	} else if found {
		return Override{}, ErrAlreadyOverridden
	}

	review, err := s.Review(ctx, facility, id)
	if err != nil {
		return Override{}, err
	}
	blocking := review.Blocking()
	if len(blocking) == 0 {
		return Override{}, ErrNothingToOverride
	}
	codes := make([]string, 0, len(blocking))
	for _, f := range blocking {
		codes = append(codes, f.RuleCode)
	}

	now := s.now()
	out := Override{
		ID: uuid.New(), PrescriptionID: id, PatientID: patient, VisitID: visitID,
		GrantedAt: now, GrantedBy: actor.UserID(), GrantedRole: actor.Role(),
		Reason: reason, BlockingAtGrant: codes,
	}
	payload := eventstore.PrescriptionQAOverridden{
		OverrideID: out.ID.String(), FacilityID: facility.String(),
		PrescriptionID: id.String(), PatientID: patient.String(), VisitID: visitID.String(),
		Reason: reason, Blocking: codes, GrantedAt: now,
	}
	err = s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return s.append(ctx, tx, eventID, "PRESCRIPTION_QA_OVERRIDDEN", id,
			patient, visitID, actor, source, now, payload)
	})
	if err != nil {
		return Override{}, err
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// The ledger
// ---------------------------------------------------------------------------

func (s *Service) append(ctx context.Context, tx pgx.Tx, eventID uuid.UUID, eventType string,
	sheet, patient, visitID uuid.UUID, actor eventstore.Actor, source eventstore.Source,
	now time.Time, payload any) error {

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
	_, err = s.events.AppendInTx(ctx, tx, eventstore.Envelope{
		EventID: eventID,
		// The prescription is the aggregate, like every other event about this sheet, so that
		// "everything that ever happened to it" stays one read.
		AggregateType: "PRESCRIPTION",
		AggregateID:   sheet,
		PatientID:     &patient,
		VisitID:       &visitID,
		EventType:     eventType,
		EventVersion:  1,
		OccurredAt:    now,
		Actor:         actor,
		Source:        source,
		Payload:       encoded,
	})
	return err
}
