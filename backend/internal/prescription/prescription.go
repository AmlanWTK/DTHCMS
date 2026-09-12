// Package prescription is the clinic's primary output artefact and the most medico-legally
// significant object in the system (CP80, §3 step 9, §7, §9.3).
//
// # One sentence everything here follows from
//
// **Nobody edits a signed prescription.** Not the physician who wrote it, not an administrator,
// not a developer with a psql prompt and the application's password. A change to a signed
// prescription is a *correction*: a new prescription that says what it corrects and why, while
// the original stays in the record exactly as it was written.
//
// That is enforced in four places, and only one of them is Go:
//
//  1. `dthcms_app` holds **SELECT and nothing else** on `read.prescription` and
//     `read.prescription_item`. There is no statement this process can issue that writes one.
//  2. `core.prescription_is_frozen()` refuses any content change to any prescription, for every
//     role including the projector.
//  3. `core.prescription_item_is_frozen()` allows an item to be written only while its
//     prescription is DRAFT — not "not signed", DRAFT, because a prescription altered after QA
//     saw it is the same defect one step earlier.
//  4. [Machine] refuses an illegal transition before the event is appended, so the ledger never
//     carries one.
//
// The order matters: the database is the guarantee and this package is the ergonomics. A check
// in a handler alone is a check that one refactor removes.
//
// # The ledger is the truth
//
// Both tables are projections. Every fact in them arrived as an event, and `docs/write-path.md`
// is how. Drop the tables and a rebuild reconstructs them — including the price of every line,
// because the price travels in the event payload as a number rather than as a join.
//
// # What this checkpoint owns and what it does not
//
// CP80 owns the aggregate, the machine, the events and the API for drafting. It does **not** own
// signing (CP84, with step-up), QA clearance (CP83), printing (CP89) or dispensing (CP118) —
// and it does not pretend to. Those four transitions are edges in `core.prescription_transition`
// with a `owned_by` column naming their checkpoint, and this package exposes the service methods
// that drive them ([Service.Sign], [Service.Print], [Service.Dispense], [Service.QABounce])
// **without HTTP routes**, because a signing route without step-up 2FA would be a hole, not a
// head start. The machine is complete and testable; the workflows arrive with their screens.
package prescription

import (
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Statuses
// ---------------------------------------------------------------------------

// Status is one of the seven states of `core.prescription_status`.
type Status string

const (
	// StatusDraft — being written. The only status in which items may be touched.
	StatusDraft Status = "DRAFT"
	// StatusQAReview — submitted for CP83's clearance. Items are fixed.
	StatusQAReview Status = "QA_REVIEW"
	// StatusSigned — carries the physician's signature (CP84). Immutable by every path.
	StatusSigned Status = "SIGNED"
	// StatusPrinted — the paper is in the patient's hand (CP89).
	StatusPrinted Status = "PRINTED"
	// StatusDispensed — the medicine has left the counter (CP118).
	StatusDispensed Status = "DISPENSED"
	// StatusCancelled — withdrawn before the paper left the clinic. Terminal.
	StatusCancelled Status = "CANCELLED"
	// StatusCorrected — superseded by a correction. Terminal; the correction is a separate
	// prescription.
	StatusCorrected Status = "CORRECTED"
)

// AllStatuses is the catalogue, in lifecycle order.
var AllStatuses = []Status{
	StatusDraft, StatusQAReview, StatusSigned, StatusPrinted, StatusDispensed,
	StatusCancelled, StatusCorrected,
}

// Editable reports whether items may be added, changed or removed in this status.
//
// True for DRAFT and nothing else. The database says the same thing in
// `core.prescription_item_is_frozen()`; this is the copy a handler can consult before doing the
// work, and the two are held together by `TestGoAndTheDatabaseAgreeAboutWhatIsEditable`.
func (s Status) Editable() bool { return s == StatusDraft }

// Terminal reports whether this status has any outgoing edge at all.
func (s Status) Terminal() bool { return s == StatusCancelled || s == StatusCorrected }

// ---------------------------------------------------------------------------
// The state machine
// ---------------------------------------------------------------------------

// Edge is one legal transition and the event that carries it.
type Edge struct {
	From      Status
	To        Status
	EventType string
	NoteEN    string
	NoteBN    string
	// OwnedBy names the checkpoint whose workflow drives this edge. CP80 built the machine;
	// four of the twelve edges belong to screens that do not exist yet.
	OwnedBy string
}

// Machine answers whether one status may become another.
//
// **Built from `core.prescription_transition`, never from a literal in Go.** A hardcoded matrix
// here would be a second copy of the rule, and the failure mode of two copies is that they agree
// until somebody changes one — at which point the application permits a transition the trigger
// refuses, or worse, the other way round.
type Machine struct {
	edges map[Status]map[Status]Edge
	all   []Edge
}

// NewMachine builds one from the rows.
func NewMachine(edges []Edge) *Machine {
	m := &Machine{edges: map[Status]map[Status]Edge{}, all: append([]Edge{}, edges...)}
	for _, e := range edges {
		if m.edges[e.From] == nil {
			m.edges[e.From] = map[Status]Edge{}
		}
		m.edges[e.From][e.To] = e
	}
	sort.Slice(m.all, func(i, j int) bool {
		if m.all[i].From != m.all[j].From {
			return m.all[i].From < m.all[j].From
		}
		return m.all[i].To < m.all[j].To
	})
	return m
}

// Edges is the whole matrix, ordered.
func (m *Machine) Edges() []Edge { return append([]Edge{}, m.all...) }

// Allows reports whether `from` may become `to`, and the edge that says so.
func (m *Machine) Allows(from, to Status) (Edge, bool) {
	e, ok := m.edges[from][to]
	return e, ok
}

// Check refuses an illegal transition with a message that names both states and points at the
// only legal way out.
//
// Bilingual, because an error a physician meets on a screen is part of the interface.
func (m *Machine) Check(from, to Status) error {
	if from == to {
		return &TransitionError{From: from, To: to, Reason: reasonSame}
	}
	if _, ok := m.Allows(from, to); !ok {
		reason := reasonNoEdge
		if from.Terminal() {
			reason = reasonTerminal
		}
		return &TransitionError{From: from, To: to, Reason: reason}
	}
	return nil
}

// Reasons a transition was refused.
const (
	reasonSame     = "same"
	reasonNoEdge   = "no_edge"
	reasonTerminal = "terminal"
)

// TransitionError is an illegal transition, in both languages.
type TransitionError struct {
	From   Status
	To     Status
	Reason string
}

func (e *TransitionError) Error() string { return e.MessageEN() }

// MessageEN says what was refused and what to do instead.
func (e *TransitionError) MessageEN() string {
	switch e.Reason {
	case reasonSame:
		return "This prescription is already " + strings.ToLower(string(e.To)) + "."
	case reasonTerminal:
		return "This prescription is " + statusWordEN(e.From) + " and nothing further can " +
			"happen to it. Write a new prescription."
	default:
		if e.From == StatusSigned || e.From == StatusPrinted || e.From == StatusDispensed {
			return "This prescription is " + statusWordEN(e.From) + " and cannot be changed. " +
				"Correct it instead: a correction is a new prescription that supersedes this " +
				"one, and this one stays in the record exactly as it was written."
		}
		return "A prescription cannot go from " + statusWordEN(e.From) + " to " +
			statusWordEN(e.To) + "."
	}
}

// MessageBN is the same sentence in clinical Bengali.
func (e *TransitionError) MessageBN() string {
	switch e.Reason {
	case reasonSame:
		return "এই ব্যবস্থাপত্রটি ইতিমধ্যেই " + statusWordBN(e.To) + "।"
	case reasonTerminal:
		return "এই ব্যবস্থাপত্রটি " + statusWordBN(e.From) + ", এর সঙ্গে আর কিছু করা যাবে না। " +
			"নতুন ব্যবস্থাপত্র লিখুন।"
	default:
		if e.From == StatusSigned || e.From == StatusPrinted || e.From == StatusDispensed {
			return "এই ব্যবস্থাপত্রটি " + statusWordBN(e.From) + ", তাই এটি বদলানো যাবে না। " +
				"এর বদলে সংশোধনী দিন: সংশোধনী একটি নতুন ব্যবস্থাপত্র যা এটির জায়গা নেয়, আর " +
				"এটি যেমন লেখা হয়েছিল ঠিক তেমনই নথিতে থেকে যায়।"
		}
		return statusWordBN(e.From) + " অবস্থা থেকে " + statusWordBN(e.To) +
			" অবস্থায় যাওয়া যায় না।"
	}
}

func statusWordEN(s Status) string {
	switch s {
	case StatusDraft:
		return "a draft"
	case StatusQAReview:
		return "with QA"
	case StatusSigned:
		return "signed"
	case StatusPrinted:
		return "printed"
	case StatusDispensed:
		return "dispensed"
	case StatusCancelled:
		return "cancelled"
	case StatusCorrected:
		return "already corrected"
	}
	return string(s)
}

func statusWordBN(s Status) string {
	switch s {
	case StatusDraft:
		return "খসড়া"
	case StatusQAReview:
		return "কিউএ পর্যালোচনায়"
	case StatusSigned:
		return "স্বাক্ষরিত"
	case StatusPrinted:
		return "ছাপা হয়ে গেছে"
	case StatusDispensed:
		return "ওষুধ দেওয়া হয়ে গেছে"
	case StatusCancelled:
		return "বাতিল"
	case StatusCorrected:
		return "ইতিমধ্যেই সংশোধিত"
	}
	return string(s)
}

// ---------------------------------------------------------------------------
// The aggregate
// ---------------------------------------------------------------------------

// Prescription is one sheet.
type Prescription struct {
	ID         uuid.UUID `json:"id"`
	FacilityID uuid.UUID `json:"facility_id"`
	PatientID  uuid.UUID `json:"patient_id"`
	VisitID    uuid.UUID `json:"visit_id"`

	Status       Status `json:"status"`
	StatusNameEN string `json:"status_name_en"`
	StatusNameBN string `json:"status_name_bn"`

	CreatedAt   time.Time `json:"created_at"`
	CreatedBy   uuid.UUID `json:"created_by"`
	CreatedRole string    `json:"created_role,omitempty"`

	SubmittedAt *time.Time `json:"submitted_at,omitempty"`
	SubmittedBy *uuid.UUID `json:"submitted_by,omitempty"`
	BouncedAt   *time.Time `json:"bounced_at,omitempty"`
	SignedAt    *time.Time `json:"signed_at,omitempty"`
	SignedBy    *uuid.UUID `json:"signed_by,omitempty"`
	PrintedAt   *time.Time `json:"printed_at,omitempty"`
	DispensedAt *time.Time `json:"dispensed_at,omitempty"`

	CancelledAt     *time.Time `json:"cancelled_at,omitempty"`
	CancelledBy     *uuid.UUID `json:"cancelled_by,omitempty"`
	CancelledReason string     `json:"cancelled_reason,omitempty"`

	CorrectedAt *time.Time `json:"corrected_at,omitempty"`
	CorrectedBy *uuid.UUID `json:"corrected_by,omitempty"`

	// Corrects is the prescription this one supersedes. Nil on an ordinary prescription.
	Corrects         *uuid.UUID `json:"corrects_prescription_id,omitempty"`
	CorrectionReason string     `json:"correction_reason,omitempty"`
	// CorrectedBySuccessor is the correction that superseded this one.
	CorrectedBySuccessor *uuid.UUID `json:"corrected_by_prescription_id,omitempty"`

	// CorrectsDispensedOriginal is true when the prescription this one corrects had already
	// been dispensed. **Recorded on the correction itself** so that anybody reading it knows
	// medicine has already left the counter without going to look at the original — whose
	// status by then says CORRECTED and no longer says DISPENSED.
	CorrectsDispensedOriginal bool `json:"corrects_dispensed_original"`

	// CarriedForwardFrom is the previous prescription this one's items were copied from.
	CarriedForwardFrom *uuid.UUID `json:"carried_forward_from,omitempty"`

	UpdatedAt time.Time `json:"updated_at"`

	// Items is every line, removed ones included. A caller drawing the sheet filters on
	// `removed_at`; a caller auditing it does not.
	Items []Item `json:"items"`

	// Editable is `Status.Editable()`, returned so a screen does not have to hold its own copy
	// of which statuses are which.
	Editable bool `json:"editable"`
}

// Item is one line.
type Item struct {
	ID     uuid.UUID `json:"id"`
	LineNo int       `json:"line_no"`

	ProductID *uuid.UUID `json:"product_id,omitempty"`
	// Label, GenericName, Strength and FormCode are **copied at the moment of prescribing**,
	// not joined. A trade name corrected next year must not change what this sheet said, and a
	// product withdrawn from the formulary must not make a historical prescription unreadable.
	Label       string `json:"product_label"`
	GenericName string `json:"generic_name,omitempty"`
	Strength    string `json:"strength,omitempty"`
	FormCode    string `json:"form_code,omitempty"`

	Dose         string   `json:"dose"`
	DailyDose    *float64 `json:"daily_dose,omitempty"`
	DoseUnit     string   `json:"dose_unit,omitempty"`
	Frequency    string   `json:"frequency"`
	DurationDays *int     `json:"duration_days,omitempty"`
	Route        string   `json:"route,omitempty"`
	Quantity     *float64 `json:"quantity,omitempty"`

	InstructionsEN string `json:"instructions_en,omitempty"`
	InstructionsBN string `json:"instructions_bn,omitempty"`

	// Price is what this medicine cost on the day it was prescribed. Nil when the product had
	// no price on that day, which is real and is not zero.
	Price *CapturedPrice `json:"price,omitempty"`

	CarriedForwardFromItem *uuid.UUID `json:"carried_forward_from_item,omitempty"`

	RecordedAt time.Time  `json:"recorded_at"`
	RecordedBy uuid.UUID  `json:"recorded_by"`
	ModifiedAt *time.Time `json:"modified_at,omitempty"`
	ModifiedBy *uuid.UUID `json:"modified_by,omitempty"`

	RemovedAt     *time.Time `json:"removed_at,omitempty"`
	RemovedBy     *uuid.UUID `json:"removed_by,omitempty"`
	RemovedReason string     `json:"removed_reason,omitempty"`
}

// Live reports whether this line is still on the sheet.
func (i Item) Live() bool { return i.RemovedAt == nil }

// CapturedPrice is the price at the time of prescribing, **by value**.
//
// # Why the amount and not a reference
//
// CP75 never edits a price: a new price supersedes the old one and both stay, each with its own
// period. That makes "what did this cost on 4 March" answerable — by a query, today, against a
// table somebody could still ALTER.
//
// A prescription needs a stronger guarantee than that, because what it records is not a
// commercial fact but what a patient was told to pay. So the amount is copied onto the line, it
// travels in the ledger event as a number, and **the projection never looks a price up**: a
// rebuild of `read.prescription_item` from the ledger reproduces the amount of the day, not
// today's. `core.prescription_item_is_frozen()` then refuses any UPDATE that would move it.
//
// The price row's identity travels beside the amount — its id, the day it took effect, and
// whether anybody had verified it — so the line is traceable back to the price history without
// depending on that history's current shape. That is the brief's "by value, and the price row's
// identity", and both halves earn their place: the amount is what cannot be rewritten, the
// identity is what makes it auditable.
type CapturedPrice struct {
	// AmountPoisha is the unit price in poisha. Integer, because 12.34 taka in a float is
	// 12.339999999999999 and a prescription is a financial document.
	AmountPoisha int64 `json:"amount_poisha"`
	// AmountBDT is the same number as "12.34", so no client divides by 100 in floating point.
	AmountBDT string `json:"amount_bdt"`

	PriceID       uuid.UUID `json:"price_id"`
	EffectiveFrom string    `json:"effective_from"`
	// Verification is PROVISIONAL or VERIFIED **as it was when the prescription was written**.
	// A price somebody verified afterwards does not retroactively make this line verified.
	Verification string    `json:"verification"`
	CapturedAt   time.Time `json:"captured_at"`
}

// Errors this module returns. Each maps to one HTTP status in `http.go` and to one bilingual
// sentence on a screen.
var (
	// ErrNotFound — no such prescription in this facility.
	ErrNotFound = errors.New("prescription not found")
	// ErrItemNotFound — no such line on this prescription.
	ErrItemNotFound = errors.New("prescription item not found")
	// ErrNotEditable — the prescription is not a draft. The correction path is the answer.
	ErrNotEditable = errors.New("prescription is not a draft")
	// ErrItemRemoved — the line has already been taken off the sheet.
	ErrItemRemoved = errors.New("prescription item has been removed")
	// ErrNotCorrectable — a correction was asked for on a prescription that has not been
	// signed. A draft is edited, not corrected.
	ErrNotCorrectable = errors.New("prescription cannot be corrected")
	// ErrAlreadyCorrected — this prescription already has a correction. One each, enforced by
	// a unique index.
	ErrAlreadyCorrected = errors.New("prescription has already been corrected")
	// ErrCarryForwardNotConfirmed — carry-forward was asked for without the explicit
	// confirmation the plan requires.
	ErrCarryForwardNotConfirmed = errors.New("carry-forward must be confirmed explicitly")
	// ErrInvalid — the request does not describe a prescribable line.
	ErrInvalid = errors.New("invalid prescription item")
)
