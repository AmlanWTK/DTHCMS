package prescription

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/formulary"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// The write path (CP80). Every method here appends an event and lets the projection follow; none
// of them writes a prescription row, because the application role cannot.
//
// # Attribution
//
// Every method begins with `eventstore.ActorFrom(ctx)`, which is the only door production code
// has to an attribution envelope. A prescriber's name on a prescription comes from the session
// the authorisation engine verified and from nowhere else — there is no field in any request
// body that can put a colleague's name on a controlled drug.
//
// # No PHI leaves this file
//
// Nothing here logs, traces or counts. The drug names, doses and prices live in the ledger and
// the read model, where a patient's record belongs.

// Catalogue is the formulary this module reads a product and its price from.
//
// An interface rather than `*formulary.Store` so that the price lookup — the one call whose
// behaviour criterion 3 is entirely about — is substitutable in a test that has to prove a price
// change cannot reach a prescription already written.
type Catalogue interface {
	Product(ctx context.Context, facility, id uuid.UUID, on time.Time) (formulary.Product, error)
}

// Service writes prescriptions.
type Service struct {
	store   *Store
	events  *eventstore.Store
	machine *Machine
	cat     Catalogue
	clock   interface{ Now() time.Time }
}

// NewService builds one.
func NewService(store *Store, events *eventstore.Store, machine *Machine,
	catalogue Catalogue, clk interface{ Now() time.Time }) *Service {
	return &Service{store: store, events: events, machine: machine, cat: catalogue, clock: clk}
}

// Machine exposes the state machine, so a handler can render the matrix without a second copy.
func (s *Service) Machine() *Machine { return s.machine }

func (s *Service) now() time.Time {
	if s.clock == nil {
		return time.Now().UTC()
	}
	return s.clock.Now().UTC()
}

// ---------------------------------------------------------------------------
// Creating
// ---------------------------------------------------------------------------

// Creation is a new draft.
type Creation struct {
	EventID   uuid.UUID
	PatientID uuid.UUID
	VisitID   uuid.UUID

	// CarryForwardFrom is a previous prescription whose live items are copied onto this one.
	CarryForwardFrom *uuid.UUID
	// CarryForwardConfirmed is the plan's "explicit confirmation". **Carry-forward without it
	// is refused**, not silently skipped: a physician who meant to carry forward and got an
	// empty sheet would notice, and one who did not mean to and got last month's six drugs
	// might not.
	CarryForwardConfirmed bool

	LedgerSource eventstore.Source
}

// Create opens a draft.
//
// Carry-forward copies each **live** line of the source prescription, re-priced at today's
// price — because carrying a line forward is prescribing it again today, and what the patient
// will pay is today's price rather than last month's. The source line's id travels onto the copy
// so that "where did this come from" stays answerable.
func (s *Service) Create(ctx context.Context, in Creation) (Prescription, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Prescription{}, err
	}
	if in.CarryForwardFrom != nil && !in.CarryForwardConfirmed {
		return Prescription{}, ErrCarryForwardNotConfirmed
	}

	facility := actor.FacilityID()
	now := s.now()
	id := uuid.New()

	var source Prescription
	if in.CarryForwardFrom != nil {
		source, err = s.store.ByID(ctx, *in.CarryForwardFrom, facility)
		if err != nil {
			return Prescription{}, err
		}
		if source.PatientID != in.PatientID {
			// Carrying another patient's prescription forward is the single worst mistake
			// this feature could make, and it is one id typed wrong away.
			return Prescription{}, errors.New("that prescription belongs to a different patient")
		}
	}

	created := eventstore.PrescriptionCreated{
		PrescriptionID: id.String(),
		FacilityID:     facility.String(),
		PatientID:      in.PatientID.String(),
		VisitID:        in.VisitID.String(),
		CreatedAt:      now,
	}
	if in.CarryForwardFrom != nil {
		created.CarriedForwardFrom = in.CarryForwardFrom.String()
	}

	if err := s.appendOne(ctx, in.EventID, "PRESCRIPTION_CREATED", id, &in.PatientID, &in.VisitID,
		actor, in.LedgerSource, now, created); err != nil {
		return Prescription{}, err
	}

	for _, item := range source.Items {
		if !item.Live() {
			continue
		}
		from := item.ID
		if _, err := s.AddItem(ctx, Addition{
			PrescriptionID: id,
			ProductID:      item.ProductID,
			Label:          item.Label,
			Dose:           item.Dose, DailyDose: item.DailyDose, DoseUnit: item.DoseUnit,
			Frequency: item.Frequency, DurationDays: item.DurationDays,
			Route: item.Route, Quantity: item.Quantity,
			InstructionsEN: item.InstructionsEN, InstructionsBN: item.InstructionsBN,
			CarriedForwardFromItem: &from,
			LedgerSource:           in.LedgerSource,
		}); err != nil {
			return Prescription{}, err
		}
	}
	return s.store.ByID(ctx, id, facility)
}

// ---------------------------------------------------------------------------
// Items
// ---------------------------------------------------------------------------

// Addition is one line going onto a draft.
type Addition struct {
	EventID        uuid.UUID
	PrescriptionID uuid.UUID

	ProductID *uuid.UUID
	// Label is what to call it when there is no product — a medicine this formulary does not
	// hold. Ignored when ProductID is given, because the formulary's own words are what the
	// pharmacy counter reads.
	Label string

	Dose         string
	DailyDose    *float64
	DoseUnit     string
	Frequency    string
	DurationDays *int
	Route        string
	Quantity     *float64

	InstructionsEN string
	InstructionsBN string

	CarriedForwardFromItem *uuid.UUID
	LedgerSource           eventstore.Source
}

// AddItem puts a line on a draft, capturing the price of the day.
//
// # The price, and why it is read here rather than at print time
//
// Criterion 3. The amount is read from CP75's `PriceAsOf` at the instant the line is written,
// copied into the event payload as a number, and never looked up again by anything downstream —
// not the projection, not a rebuild, not the printer. A price that changes next March therefore
// cannot change what this prescription says the medicine cost, and there is no path by which it
// could: the number is in an append-only ledger.
//
// A product with no price on the day produces **no captured price**, not a zero. Zero would tell
// a patient a medicine is free, and CP75 is explicit that a product's price history starts
// somewhere and the gap before it is a real state.
func (s *Service) AddItem(ctx context.Context, in Addition) (Item, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Item{}, err
	}
	facility := actor.FacilityID()
	now := s.now()

	sheet, err := s.store.ByID(ctx, in.PrescriptionID, facility)
	if err != nil {
		return Item{}, err
	}
	if !sheet.Status.Editable() {
		return Item{}, ErrNotEditable
	}

	line, err := s.store.nextLine(ctx, in.PrescriptionID)
	if err != nil {
		return Item{}, err
	}

	itemID := uuid.New()
	added := eventstore.PrescriptionItemAdded{
		PrescriptionID: in.PrescriptionID.String(),
		ItemID:         itemID.String(),
		FacilityID:     facility.String(),
		LineNo:         line,
		ProductLabel:   strings.TrimSpace(in.Label),
		Dose:           strings.TrimSpace(in.Dose),
		DailyDose:      in.DailyDose,
		DoseUnit:       strings.TrimSpace(in.DoseUnit),
		Frequency:      strings.TrimSpace(in.Frequency),
		DurationDays:   in.DurationDays,
		Route:          strings.TrimSpace(in.Route),
		Quantity:       in.Quantity,
		InstructionsEN: strings.TrimSpace(in.InstructionsEN),
		InstructionsBN: strings.TrimSpace(in.InstructionsBN),
		RecordedAt:     now,
	}
	if in.CarriedForwardFromItem != nil {
		added.CarriedForwardFromItem = in.CarriedForwardFromItem.String()
	}

	if in.ProductID != nil {
		product, err := s.cat.Product(ctx, facility, *in.ProductID, day(now))
		if err != nil {
			return Item{}, err
		}
		added.ProductID = product.ID.String()
		added.ProductLabel = product.TradeName
		added.GenericName = product.GenericName
		added.Strength = product.Strength
		added.FormCode = product.FormCode
		if product.Price != nil {
			amount := int64(product.Price.Amount)
			added.PricePoisha = &amount
			added.PriceID = product.Price.ID.String()
			added.PriceEffectiveFrom = product.Price.From
			added.PriceVerification = product.Price.Verification
			captured := now
			added.PriceCapturedAt = &captured
		}
	}
	if added.ProductLabel == "" {
		return Item{}, ErrInvalid
	}

	if err := s.appendOne(ctx, in.EventID, "PRESCRIPTION_ITEM_ADDED", in.PrescriptionID,
		&sheet.PatientID, &sheet.VisitID, actor, in.LedgerSource, now, added); err != nil {
		return Item{}, err
	}
	return s.item(ctx, itemID)
}

// Modification changes how a line is taken. There is no price field, by construction.
type Modification struct {
	EventID        uuid.UUID
	PrescriptionID uuid.UUID
	ItemID         uuid.UUID

	// LineNo reorders. Zero means leave it where it is.
	LineNo       int
	Dose         string
	DailyDose    *float64
	DoseUnit     string
	Frequency    string
	DurationDays *int
	Route        string
	Quantity     *float64

	InstructionsEN string
	InstructionsBN string

	LedgerSource eventstore.Source
}

// ModifyItem changes a line on a draft.
//
// **It cannot change the medicine or its price.** Changing which drug a line is is removing one
// item and adding another — two events, two rows, both visible — rather than a silent
// substitution on a line that keeps its identity and its history.
func (s *Service) ModifyItem(ctx context.Context, in Modification) (Item, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Item{}, err
	}
	facility := actor.FacilityID()
	now := s.now()

	sheet, err := s.store.ByID(ctx, in.PrescriptionID, facility)
	if err != nil {
		return Item{}, err
	}
	if !sheet.Status.Editable() {
		return Item{}, ErrNotEditable
	}
	current, err := s.item(ctx, in.ItemID)
	if err != nil {
		return Item{}, err
	}
	if !current.Live() {
		return Item{}, ErrItemRemoved
	}

	line := in.LineNo
	if line <= 0 {
		line = current.LineNo
	}
	modified := eventstore.PrescriptionItemModified{
		PrescriptionID: in.PrescriptionID.String(),
		ItemID:         in.ItemID.String(),
		LineNo:         line,
		Dose:           strings.TrimSpace(in.Dose),
		DailyDose:      in.DailyDose,
		DoseUnit:       strings.TrimSpace(in.DoseUnit),
		Frequency:      strings.TrimSpace(in.Frequency),
		DurationDays:   in.DurationDays,
		Route:          strings.TrimSpace(in.Route),
		Quantity:       in.Quantity,
		InstructionsEN: strings.TrimSpace(in.InstructionsEN),
		InstructionsBN: strings.TrimSpace(in.InstructionsBN),
		ModifiedAt:     now,
	}
	if err := s.appendOne(ctx, in.EventID, "PRESCRIPTION_ITEM_MODIFIED", in.PrescriptionID,
		&sheet.PatientID, &sheet.VisitID, actor, in.LedgerSource, now, modified); err != nil {
		return Item{}, err
	}
	return s.item(ctx, in.ItemID)
}

// RemoveItem takes a line off a draft. The row stays, with who removed it and why.
func (s *Service) RemoveItem(ctx context.Context, eventID, prescription, item uuid.UUID,
	reason string, source eventstore.Source) error {

	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return err
	}
	facility := actor.FacilityID()
	now := s.now()

	sheet, err := s.store.ByID(ctx, prescription, facility)
	if err != nil {
		return err
	}
	if !sheet.Status.Editable() {
		return ErrNotEditable
	}
	current, err := s.item(ctx, item)
	if err != nil {
		return err
	}
	if !current.Live() {
		return ErrItemRemoved
	}

	return s.appendOne(ctx, eventID, "PRESCRIPTION_ITEM_REMOVED", prescription,
		&sheet.PatientID, &sheet.VisitID, actor, source, now,
		eventstore.PrescriptionItemRemoved{
			PrescriptionID: prescription.String(), ItemID: item.String(),
			Reason: strings.TrimSpace(reason), RemovedAt: now,
		})
}

// ---------------------------------------------------------------------------
// Transitions
// ---------------------------------------------------------------------------

// Submit sends a draft for QA clearance.
func (s *Service) Submit(ctx context.Context, eventID, id uuid.UUID, source eventstore.Source) (Prescription, error) {
	return s.transition(ctx, eventID, id, StatusQAReview, "", nil, source)
}

// Cancel withdraws a prescription before the paper has left the clinic.
//
// Legal from DRAFT, QA_REVIEW and SIGNED, and from nowhere else. Once the sheet is PRINTED the
// paper is in the patient's hand and the pharmacy may act on it, so the record has to show a
// replacement rather than an absence — which is a correction.
func (s *Service) Cancel(ctx context.Context, eventID, id uuid.UUID, reason string, source eventstore.Source) (Prescription, error) {
	if strings.TrimSpace(reason) == "" {
		return Prescription{}, errors.New("a cancellation says why")
	}
	return s.transition(ctx, eventID, id, StatusCancelled, reason, nil, source)
}

// QABounce returns a prescription to the prescriber. **CP83 owns the workflow**; this is the
// transition it will drive, complete and testable, with no route in front of it.
func (s *Service) QABounce(ctx context.Context, eventID, id uuid.UUID, reason string, source eventstore.Source) (Prescription, error) {
	return s.transition(ctx, eventID, id, StatusDraft, reason, nil, source)
}

// Sign moves a cleared prescription to SIGNED.
//
// **CP84 owns signing**, which is the canonical serialisation, the KMS key, the step-up 2FA and
// the signature in the ledger. This method is the transition underneath all of that, and it has
// **no HTTP route**, deliberately: a signing endpoint without step-up would be a hole rather
// than a head start. `PRESCRIPTION_SIGNED` version 2 carries the signature.
func (s *Service) Sign(ctx context.Context, eventID, id uuid.UUID, source eventstore.Source) (Prescription, error) {
	return s.transition(ctx, eventID, id, StatusSigned, "", nil, source)
}

// Print records that the paper was produced. CP89 owns the pipeline; no route here.
func (s *Service) Print(ctx context.Context, eventID, id uuid.UUID, source eventstore.Source) (Prescription, error) {
	return s.transition(ctx, eventID, id, StatusPrinted, "", nil, source)
}

// Dispense records that the medicine left the counter. CP118 owns the pharmacy console; no route
// here.
func (s *Service) Dispense(ctx context.Context, eventID, id uuid.UUID, source eventstore.Source) (Prescription, error) {
	return s.transition(ctx, eventID, id, StatusDispensed, "", nil, source)
}

// transition is every status change, checked against the machine before the append.
//
// The machine is checked here and the trigger checks it again in the database. Two locks on one
// door, and the reason is what is behind it: an event describing an illegal transition would be
// permanent, and the projection would then refuse to apply it — leaving a ledger and a read
// model that disagree about what happened to a prescription.
func (s *Service) transition(ctx context.Context, eventID, id uuid.UUID, to Status,
	reason string, correction *uuid.UUID, source eventstore.Source) (Prescription, error) {

	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Prescription{}, err
	}
	facility := actor.FacilityID()
	now := s.now()

	sheet, err := s.store.ByID(ctx, id, facility)
	if err != nil {
		return Prescription{}, err
	}
	if err := s.machine.Check(sheet.Status, to); err != nil {
		return Prescription{}, err
	}
	edge, _ := s.machine.Allows(sheet.Status, to)

	payload := eventstore.PrescriptionTransitioned{
		PrescriptionID: id.String(),
		FromStatus:     string(sheet.Status),
		ToStatus:       string(to),
		Reason:         strings.TrimSpace(reason),
		At:             now,
	}
	if correction != nil {
		payload.CorrectionID = correction.String()
	}
	if err := s.appendOne(ctx, eventID, edge.EventType, id,
		&sheet.PatientID, &sheet.VisitID, actor, source, now, payload); err != nil {
		return Prescription{}, err
	}
	return s.store.ByID(ctx, id, facility)
}

// ---------------------------------------------------------------------------
// Correction
// ---------------------------------------------------------------------------

// CorrectionRequest supersedes a signed prescription.
type CorrectionRequest struct {
	EventID uuid.UUID
	// Corrects is the prescription being superseded.
	Corrects uuid.UUID
	// Reason is what was wrong with it. Required.
	Reason string
	// VisitID is the visit the correction belongs to. Defaults to the original's visit, which
	// is what a correction written the same day means; a correction written at a later visit
	// says so.
	VisitID *uuid.UUID
	// CopyItems carries the original's live lines onto the correction, re-priced at today's
	// price. The usual case: a correction almost always keeps most of the sheet.
	CopyItems bool

	LedgerSource eventstore.Source
}

// Correct writes a new prescription that supersedes a signed one.
//
// # The policy, decided rather than left open
//
// The plan listed "correction policy after dispensing" as an open decision. This is the answer,
// and it is flagged for Dr. Nahid:
//
//   - A correction is **always permitted** from SIGNED, PRINTED or DISPENSED.
//   - It is **a new prescription**, in DRAFT, that names what it corrects and why. The original
//     moves to CORRECTED, is linked forward to the correction, and **is never edited and never
//     removed from the record**.
//   - If the original was already DISPENSED, the correction records that fact **on itself**, so
//     that anybody reading the correction knows medicine has already left the counter. They
//     would otherwise have to go and look at the original, whose status by then says CORRECTED
//     and no longer says DISPENSED.
//
// **What the pharmacy does next is a clinical and operational question, and it is his.** This
// records the fact and makes it visible; it does not decide whether the patient is called back.
//
// # One correction per prescription
//
// Enforced by a unique index, not by this check. A prescription corrected twice would leave two
// sheets each claiming to supersede the same one, and no reader could say which is current. A
// correction that itself needs correcting is corrected in turn — a chain, not a fan.
func (s *Service) Correct(ctx context.Context, in CorrectionRequest) (Prescription, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Prescription{}, err
	}
	if strings.TrimSpace(in.Reason) == "" {
		return Prescription{}, errors.New("a correction says what was wrong with the prescription it supersedes")
	}
	facility := actor.FacilityID()
	now := s.now()

	original, err := s.store.ByID(ctx, in.Corrects, facility)
	if err != nil {
		return Prescription{}, err
	}
	// A draft is edited, not corrected. Offering both would make "correct this" mean two
	// different things depending on a status the physician cannot see from the button.
	switch original.Status {
	case StatusSigned, StatusPrinted, StatusDispensed:
	case StatusCorrected:
		return Prescription{}, ErrAlreadyCorrected
	default:
		return Prescription{}, ErrNotCorrectable
	}

	correctionID := uuid.New()
	visit := original.VisitID
	if in.VisitID != nil {
		visit = *in.VisitID
	}

	created := eventstore.PrescriptionCreated{
		PrescriptionID:            correctionID.String(),
		FacilityID:                facility.String(),
		PatientID:                 original.PatientID.String(),
		VisitID:                   visit.String(),
		CorrectsPrescriptionID:    in.Corrects.String(),
		CorrectionReason:          strings.TrimSpace(in.Reason),
		CorrectsDispensedOriginal: original.Status == StatusDispensed,
		CreatedAt:                 now,
	}
	if err := s.appendOne(ctx, in.EventID, "PRESCRIPTION_CREATED", correctionID,
		&original.PatientID, &visit, actor, in.LedgerSource, now, created); err != nil {
		return Prescription{}, err
	}

	// The original moves to CORRECTED and is linked forward, in its own stream. A separate
	// event on a separate aggregate, because the original's history must contain only events
	// about the original.
	if _, err := s.transition(ctx, uuid.New(), in.Corrects, StatusCorrected,
		strings.TrimSpace(in.Reason), &correctionID, in.LedgerSource); err != nil {
		return Prescription{}, err
	}

	if in.CopyItems {
		for _, item := range original.Items {
			if !item.Live() {
				continue
			}
			from := item.ID
			if _, err := s.AddItem(ctx, Addition{
				PrescriptionID: correctionID,
				ProductID:      item.ProductID, Label: item.Label,
				Dose: item.Dose, DailyDose: item.DailyDose, DoseUnit: item.DoseUnit,
				Frequency: item.Frequency, DurationDays: item.DurationDays,
				Route: item.Route, Quantity: item.Quantity,
				InstructionsEN: item.InstructionsEN, InstructionsBN: item.InstructionsBN,
				CarriedForwardFromItem: &from,
				LedgerSource:           in.LedgerSource,
			}); err != nil {
				return Prescription{}, err
			}
		}
	}
	return s.store.ByID(ctx, correctionID, facility)
}

// ---------------------------------------------------------------------------
// Plumbing
// ---------------------------------------------------------------------------

func (s *Service) item(ctx context.Context, id uuid.UUID) (Item, error) {
	row, err := s.store.q.PrescriptionItem(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Item{}, ErrItemNotFound
	}
	if err != nil {
		return Item{}, err
	}
	// Re-read through the full row shape so that one function decides what an Item looks like.
	items, err := s.store.q.PrescriptionItems(ctx, row.PrescriptionID)
	if err != nil {
		return Item{}, err
	}
	for _, candidate := range items {
		if candidate.ID == id {
			return itemOf(candidate), nil
		}
	}
	return Item{}, ErrItemNotFound
}

// appendOne writes one event, with its synchronous projection, in one transaction.
func (s *Service) appendOne(ctx context.Context, eventID uuid.UUID, eventType string,
	prescription uuid.UUID, patient, visit *uuid.UUID, actor eventstore.Actor,
	source eventstore.Source, now time.Time, payload any) error {

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
		EventID: eventID,
		// **The prescription is the aggregate.** Its stream is exactly this sheet, so its
		// whole history is one read rather than a filter over a patient's clinical life.
		AggregateType: "PRESCRIPTION",
		AggregateID:   prescription,
		PatientID:     patient,
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
