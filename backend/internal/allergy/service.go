package allergy

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Service writes allergies and assertions.
//
// Recording and asserting are separate methods for the same reason confirming and amending a
// history item are: they are different claims. "She reacts to penicillin" and "she says she
// reacts to nothing" are not two shapes of one form, and a single endpoint that took either
// would make criterion 2 — an explicit, attributed assertion — a matter of which fields
// happened to be filled in.
type Service struct {
	store  *Store
	events *eventstore.Store
	clock  interface{ Now() time.Time }
}

func NewService(store *Store, events *eventstore.Store, clk interface{ Now() time.Time }) *Service {
	return &Service{store: store, events: events, clock: clk}
}

// Recording is one allergy as station 4 sends it.
type Recording struct {
	EventID   uuid.UUID
	PatientID uuid.UUID
	VisitID   *uuid.UUID

	CodeSystem  string
	CodeVersion string
	Code        string
	Said        string

	Reaction  string
	Severity  string
	Certainty string
	Note      string

	LedgerSource eventstore.Source
}

// Record writes one allergy.
//
// Recording an allergy does **not** withdraw an earlier "no known allergies", and that is
// deliberate. Both are true statements about their own moment: somebody asked in March and was
// told there were none, and somebody found one in June. The record keeps both, and
// `core.allergy_status` decides which is the current answer — a live allergy outranks any
// assertion. Withdrawing the March row would delete the fact that somebody asked.
func (s *Service) Record(ctx context.Context, in Recording) (State, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return State{}, err
	}
	trimmed(&in.CodeSystem, &in.CodeVersion, &in.Code, &in.Said,
		&in.Reaction, &in.Severity, &in.Certainty, &in.Note)

	coded := 0
	for _, part := range []string{in.CodeSystem, in.CodeVersion, in.Code} {
		if part != "" {
			coded++
		}
	}
	if coded != 0 && coded != 3 {
		return State{}, ErrPartialCoding
	}
	if coded == 0 && in.Said == "" {
		return State{}, ErrNothingNamed
	}
	// Checked against the vocabulary rather than trusted, because the alternative is a row
	// the header cannot render — and an allergy that shows as a blank line is worse than one
	// nobody recorded, since the blank line reads as "checked, nothing found".
	known, err := s.store.Reactions(ctx)
	if err != nil {
		return State{}, err
	}
	found := false
	for _, reaction := range known {
		if reaction.Reaction == in.Reaction {
			found = true
		}
	}
	if !found {
		return State{}, ErrUnknownReaction
	}

	now := s.clock.Now().UTC()
	payload := eventstore.AllergyRecorded{
		AllergyID:   uuid.New().String(),
		FacilityID:  actor.FacilityID().String(),
		PatientID:   in.PatientID.String(),
		CodeSystem:  in.CodeSystem,
		CodeVersion: in.CodeVersion,
		Code:        in.Code,
		Said:        in.Said,
		Reaction:    in.Reaction,
		Severity:    in.Severity,
		Certainty:   in.Certainty,
		Note:        in.Note,
		RecordedAt:  now,
	}
	if in.VisitID != nil {
		payload.VisitID = in.VisitID.String()
	}
	if err := s.append(ctx, in.EventID, "ALLERGY_RECORDED", in.PatientID, in.VisitID,
		actor, in.LedgerSource, now, payload); err != nil {
		return State{}, err
	}
	return s.store.For(ctx, in.PatientID)
}

// Assert records what the allergy answer is, in somebody's name.
//
// **This method is acceptance criterion 2.** There is no column anywhere that means "no
// allergies" by being empty, and this is the only way to say it — a positive act, by a named
// person, in the ledger.
func (s *Service) Assert(ctx context.Context, eventID, patient uuid.UUID, kind, reason string,
	visit *uuid.UUID, source eventstore.Source) (State, error) {

	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return State{}, err
	}
	trimmed(&kind, &reason)
	switch kind {
	case StatusNoKnown:
		if reason != "" {
			return State{}, ErrReasonNotWanted
		}
	case StatusUnable:
		if reason == "" {
			return State{}, ErrReasonRequired
		}
	default:
		return State{}, errors.New("allergy: an assertion is NO_KNOWN_ALLERGY or UNABLE_TO_ASSESS")
	}

	now := s.clock.Now().UTC()
	payload := eventstore.AllergyStatusAsserted{
		AssertionID: uuid.New().String(),
		FacilityID:  actor.FacilityID().String(),
		PatientID:   patient.String(),
		Kind:        kind,
		Reason:      reason,
		AssertedAt:  now,
	}
	if visit != nil {
		payload.VisitID = visit.String()
	}
	if err := s.append(ctx, eventID, "ALLERGY_STATUS_ASSERTED", patient, visit,
		actor, source, now, payload); err != nil {
		return State{}, err
	}
	return s.store.For(ctx, patient)
}

// WithdrawAllergy takes back an allergy that should not have been recorded.
//
// Never a deletion. An allergy somebody withdrew is one somebody disagreed with, and the next
// clinician reading the record needs to know that a colleague once believed it — which is why
// the row stays and the change history shows both halves.
func (s *Service) WithdrawAllergy(ctx context.Context, eventID, allergyID uuid.UUID,
	reason string, visit *uuid.UUID, source eventstore.Source) (State, error) {

	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return State{}, err
	}
	row, err := s.store.q.AllergyByID(ctx, allergyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return State{}, ErrNotFound
	}
	if err != nil {
		return State{}, err
	}
	if row.RemovedAt != nil {
		return State{}, ErrAlreadyWithdrawn
	}

	now := s.clock.Now().UTC()
	payload := eventstore.AllergyWithdrawn{
		AllergyID:   allergyID.String(),
		PatientID:   row.PatientID.String(),
		Reason:      reason,
		WithdrawnAt: now,
	}
	if visit != nil {
		payload.VisitID = visit.String()
	}
	if err := s.append(ctx, eventID, "ALLERGY_WITHDRAWN", row.PatientID, visit,
		actor, source, now, payload); err != nil {
		return State{}, err
	}
	// Read back rather than assuming. Withdrawing the last recorded allergy can drop a
	// patient's status back to whatever assertion stands behind it — or to nothing at all,
	// which re-closes the gate. The caller needs to be told that, not left to guess.
	return s.store.For(ctx, row.PatientID)
}

// WithdrawAssertion takes back a "no known allergies" or an "unable to assess".
//
// The one that matters is the first. An officer who tapped NKA on the wrong patient has put a
// claim in a clinical record that a prescriber will rely on, and the way back has to exist,
// be attributed, and leave a trace.
func (s *Service) WithdrawAssertion(ctx context.Context, eventID, assertionID uuid.UUID,
	reason string, visit *uuid.UUID, source eventstore.Source) (State, error) {

	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return State{}, err
	}
	row, err := s.store.q.AllergyAssertionByID(ctx, assertionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return State{}, ErrNotFound
	}
	if err != nil {
		return State{}, err
	}
	if row.WithdrawnAt != nil {
		return State{}, ErrAlreadyWithdrawn
	}

	now := s.clock.Now().UTC()
	payload := eventstore.AllergyWithdrawn{
		AssertionID: assertionID.String(),
		PatientID:   row.PatientID.String(),
		Reason:      reason,
		WithdrawnAt: now,
	}
	if visit != nil {
		payload.VisitID = visit.String()
	}
	if err := s.append(ctx, eventID, "ALLERGY_WITHDRAWN", row.PatientID, visit,
		actor, source, now, payload); err != nil {
		return State{}, err
	}
	return s.store.For(ctx, row.PatientID)
}

func (s *Service) append(ctx context.Context, eventID uuid.UUID, eventType string,
	patient uuid.UUID, visit *uuid.UUID, actor eventstore.Actor, source eventstore.Source,
	now time.Time, payload any) error {

	encoded, err := encode(payload)
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
		EventID: eventID,
		// The patient is the aggregate. An allergy belongs to a person for life and outlives
		// every visit, which is exactly what an aggregate keyed on the visit would lose.
		AggregateType: "PATIENT",
		AggregateID:   patient,
		PatientID:     &patient,
		VisitID:       visit,
		EventType:     eventType,
		EventVersion:  1,
		OccurredAt:    now,
		Actor:         actor,
		Source:        source,
		Payload:       encoded,
	}
	return s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbgen.Queries) error {
		_, err := s.events.AppendInTx(ctx, tx, envelope)
		return err
	})
}
