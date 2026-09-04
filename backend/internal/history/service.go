package history

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Service writes history.
//
// Four methods, one per act, and the split is the design. `Record` is the only one that
// decides what an item *is*; `Confirm` says an item is still true; `Amend` changes what is
// known about it; `Remove` says it should not have been recorded. A single "save" that did
// all four would make criterion 3 unenforceable — a confirmation and an amendment would be
// the same request, and "somebody said this is still true" would be indistinguishable from
// "the software carried it forward".
type Service struct {
	store  *Store
	events *eventstore.Store
	clock  interface{ Now() time.Time }
}

func NewService(store *Store, events *eventstore.Store, clk interface{ Now() time.Time }) *Service {
	return &Service{store: store, events: events, clock: clk}
}

// Recording is one item as station 4 sends it.
type Recording struct {
	// EventID is the client's, so a tablet that lost the reply and saved again writes the
	// same item rather than a second one. A history taken over a bad connection is the exact
	// situation that produces duplicate complaints in a record.
	EventID uuid.UUID

	PatientID uuid.UUID
	VisitID   *uuid.UUID
	Kind      string

	CodeSystem  string
	CodeVersion string
	Code        string
	Said        string

	Relation       string
	DurationDays   *int
	Severity       string
	OnsetOn        string
	OnsetPrecision string

	Dose      string
	Frequency string

	LedgerSource eventstore.Source
}

// Record writes one item.
//
// # Why the kind's rules are checked here and again in the database
//
// The same reason CP42's unit rule is: this layer exists so the officer sees a sentence in
// their own language instead of a 500, and the trigger exists so the record is safe from
// every path that is not this one — a projection rebuild, a migration, a hand-written UPDATE
// at three in the morning. Neither is redundant with the other; they protect different things.
func (s *Service) Record(ctx context.Context, in Recording) (Item, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Item{}, err
	}
	trimmed(&in.Kind, &in.CodeSystem, &in.CodeVersion, &in.Code, &in.Said,
		&in.Relation, &in.Severity, &in.OnsetOn, &in.OnsetPrecision, &in.Dose, &in.Frequency)

	kind, err := s.store.kind(ctx, in.Kind)
	if err != nil {
		return Item{}, err
	}
	if err := check(kind, in); err != nil {
		return Item{}, err
	}

	itemID := uuid.New()
	now := s.clock.Now().UTC()

	// UNRECONCILED on every medication, and nothing at all on anything else. The trigger
	// refuses a reconciliation state on a kind that is not a drug, and setting it here rather
	// than defaulting it in the database keeps "which kinds are drugs" a single fact.
	reconciliation := ""
	if kind.IsMedication {
		reconciliation = "UNRECONCILED"
	}

	payload := eventstore.HistoryItemRecorded{
		ItemID:      itemID.String(),
		FacilityID:  actor.FacilityID().String(),
		PatientID:   in.PatientID.String(),
		Kind:        kind.Kind,
		CodeSystem:  in.CodeSystem,
		CodeVersion: in.CodeVersion,
		Code:        in.Code,
		Said:        in.Said,

		Relation:       in.Relation,
		DurationDays:   in.DurationDays,
		Severity:       in.Severity,
		OnsetOn:        in.OnsetOn,
		OnsetPrecision: in.OnsetPrecision,

		Dose:           in.Dose,
		Frequency:      in.Frequency,
		Reconciliation: reconciliation,

		RecordedAt: now,
	}
	if in.VisitID != nil {
		payload.VisitID = in.VisitID.String()
	}

	if err := s.append(ctx, in.EventID, "HISTORY_ITEM_RECORDED", in.PatientID, in.VisitID,
		actor, in.LedgerSource, now, payload); err != nil {
		return Item{}, err
	}
	return s.store.ByID(ctx, itemID)
}

// Confirm records that a carried-forward item is still true.
//
// **This method is acceptance criterion 3.** Every other design that satisfies the words —
// a `confirmed` flag the read sets, a column default, a batch endpoint that stamps a whole
// list — satisfies them by making the assertion on somebody's behalf. Here a confirmation is
// an event with an actor, and twenty items carried forward is twenty of them.
func (s *Service) Confirm(ctx context.Context, eventID, itemID uuid.UUID,
	visit *uuid.UUID, source eventstore.Source) (Item, error) {

	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Item{}, err
	}
	item, err := s.open(ctx, itemID)
	if err != nil {
		return Item{}, err
	}
	now := s.clock.Now().UTC()
	payload := eventstore.HistoryItemConfirmed{
		ItemID:      itemID.String(),
		PatientID:   item.PatientID.String(),
		ConfirmedAt: now,
	}
	if visit != nil {
		payload.VisitID = visit.String()
	}
	if err := s.append(ctx, eventID, "HISTORY_ITEM_CONFIRMED", item.PatientID, visit,
		actor, source, now, payload); err != nil {
		return Item{}, err
	}
	return s.store.ByID(ctx, itemID)
}

// Amendment is what may change about an item that is still the same item.
//
// Pointers because absent and empty are different requests: a body that omitted `severity`
// leaves it alone, and a screen clearing a field has to be able to say so. What is missing
// from this struct is deliberate — the kind and the coding are not amendable, because
// changing what an item is means removing one and adding another.
type Amendment struct {
	EventID uuid.UUID
	ItemID  uuid.UUID
	VisitID *uuid.UUID

	Said           *string
	Severity       *string
	DurationDays   *int
	OnsetOn        *string
	OnsetPrecision *string
	Dose           *string
	Frequency      *string
	Status         *string

	FormularyProductID *string
	Reconciliation     *string

	LedgerSource eventstore.Source
}

// Amend changes what is known about an item.
//
// An amendment **confirms as it changes**: somebody just made a fresh assertion about this
// item, and leaving `confirmed_at` behind would show an item edited a minute ago as one
// nobody has looked at since last month — which is precisely the list station 4 works from.
func (s *Service) Amend(ctx context.Context, in Amendment) (Item, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Item{}, err
	}
	item, err := s.open(ctx, in.ItemID)
	if err != nil {
		return Item{}, err
	}
	kind, err := s.store.kind(ctx, item.Kind)
	if err != nil {
		return Item{}, err
	}
	if err := checkAmendment(kind, in); err != nil {
		return Item{}, err
	}

	now := s.clock.Now().UTC()
	payload := eventstore.HistoryItemAmended{
		ItemID:       in.ItemID.String(),
		PatientID:    item.PatientID.String(),
		DurationDays: in.DurationDays,
		AmendedAt:    now,
	}
	if in.VisitID != nil {
		payload.VisitID = in.VisitID.String()
	}
	assign(&payload.Said, in.Said)
	assign(&payload.Severity, in.Severity)
	assign(&payload.OnsetOn, in.OnsetOn)
	assign(&payload.OnsetPrecision, in.OnsetPrecision)
	assign(&payload.Dose, in.Dose)
	assign(&payload.Frequency, in.Frequency)
	assign(&payload.Status, in.Status)
	assign(&payload.Reconciliation, in.Reconciliation)
	assign(&payload.FormularyProductID, in.FormularyProductID)

	if err := s.append(ctx, in.EventID, "HISTORY_ITEM_AMENDED", item.PatientID, in.VisitID,
		actor, in.LedgerSource, now, payload); err != nil {
		return Item{}, err
	}
	return s.store.ByID(ctx, in.ItemID)
}

// Remove marks an item as one that should not have been recorded.
//
// Distinct from resolving it, and the reason is worth stating: "she had this and no longer
// does" is a clinical fact worth keeping, and "this was never true" is a correction. A single
// delete collapses them, and the second needs a reason attached — an item somebody removed is
// an item somebody disagreed with, and what they disagreed with is the interesting part.
func (s *Service) Remove(ctx context.Context, eventID, itemID uuid.UUID, reason string,
	visit *uuid.UUID, source eventstore.Source) error {

	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return err
	}
	item, err := s.open(ctx, itemID)
	if err != nil {
		return err
	}
	now := s.clock.Now().UTC()
	payload := eventstore.HistoryItemRemoved{
		ItemID:    itemID.String(),
		PatientID: item.PatientID.String(),
		Reason:    strings.TrimSpace(reason),
		RemovedAt: now,
	}
	if visit != nil {
		payload.VisitID = visit.String()
	}
	return s.append(ctx, eventID, "HISTORY_ITEM_REMOVED", item.PatientID, visit,
		actor, source, now, payload)
}

// open reads an item and refuses if somebody has already removed it. Every act but recording
// goes through here, so "you cannot confirm something that was withdrawn" is one rule rather
// than three.
func (s *Service) open(ctx context.Context, id uuid.UUID) (Item, error) {
	row, err := s.store.q.HistoryItem(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, err
	}
	if row.RemovedAt != nil {
		return Item{}, ErrRemoved
	}
	return Item{ID: row.ID, PatientID: row.PatientID, Kind: row.Kind, Status: row.Status}, nil
}

// append writes one event, with its synchronous projection, in one transaction.
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
		// The patient is the aggregate. A history belongs to a person and outlives every
		// visit they make, which is exactly what an aggregate keyed on the visit would lose.
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

// check is the per-kind rules, in the order an officer would meet them.
func check(kind Kind, in Recording) error {
	coded := 0
	for _, part := range []string{in.CodeSystem, in.CodeVersion, in.Code} {
		if part != "" {
			coded++
		}
	}
	if coded != 0 && coded != 3 {
		return ErrPartialCoding
	}
	if coded == 0 && in.Said == "" {
		return ErrNothingSaid
	}
	// A concept from the wrong catalogue is the one refusal here that a screen cannot make
	// itself: it needs to know which system this kind draws on, and that is a row.
	if coded == 3 && !strings.EqualFold(in.CodeSystem, kind.CodeSystem) {
		return fmt.Errorf("%w: %s is coded in %s", ErrWrongCatalogue, kind.Kind, kind.CodeSystem)
	}

	if kind.RequiresRelation && in.Relation == "" {
		return ErrNeedsRelation
	}
	if !kind.RequiresRelation && in.Relation != "" {
		return fmt.Errorf("%w: a %s is about the patient, not a relative",
			ErrNeedsRelation, kind.Kind)
	}
	if kind.RequiresDuration && in.DurationDays == nil {
		return ErrNeedsDuration
	}
	if !kind.AllowsSeverity && in.Severity != "" {
		return ErrNoSeverity
	}
	if !kind.AllowsOnset && in.OnsetOn != "" {
		return ErrNoOnset
	}
	if (in.OnsetOn == "") != (in.OnsetPrecision == "") {
		return ErrOnsetPartial
	}
	if !kind.IsMedication && (in.Dose != "" || in.Frequency != "") {
		return ErrNoDose
	}
	return nil
}

// checkAmendment is the subset of those rules an amendment can still break.
func checkAmendment(kind Kind, in Amendment) error {
	if in.Severity != nil && *in.Severity != "" && !kind.AllowsSeverity {
		return ErrNoSeverity
	}
	if in.OnsetOn != nil && *in.OnsetOn != "" && !kind.AllowsOnset {
		return ErrNoOnset
	}
	if !kind.IsMedication {
		if (in.Dose != nil && *in.Dose != "") || (in.Frequency != nil && *in.Frequency != "") {
			return ErrNoDose
		}
		if in.Reconciliation != nil && *in.Reconciliation != "" {
			return fmt.Errorf("%w: a %s is not reconciled against the formulary",
				ErrNoDose, kind.Kind)
		}
	}
	if in.Status != nil && *in.Status != "" && *in.Status != "ACTIVE" && *in.Status != "RESOLVED" {
		return fmt.Errorf("history: status is %q; it is ACTIVE or RESOLVED", *in.Status)
	}
	return nil
}

// assign copies an optional field onto the payload, trimmed. Absent means unchanged, which is
// why the payload's field is left alone rather than set to "".
func assign(dst *string, src *string) {
	if src == nil {
		return
	}
	*dst = strings.TrimSpace(*src)
}
