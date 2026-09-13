package prescription

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// AI prescribing suggestions and the physician's per-item decision (CP82, D-28,
// docs/ai-prescribing-suggestions.md).
//
// # Why this lives in `prescription` and not in a module of its own
//
// The checkpoint is judged on one sentence: *no AI-suggested item can reach a SIGNED prescription
// without an explicit accept or edit event.* A separate module would have had to ask this one to
// write the line, through an exported method taking a dose and a product — and an exported method
// that writes a prescription line from a suggestion is exactly the path the criterion says must
// not exist. Whoever held that module could call it; so could anybody else.
//
// Inside this package the path can be made unreachable instead. [Addition] — the only argument
// [Service.AddItem] takes — has no field naming a suggestion and there is nowhere to add one
// without editing this package. The unexported `Service.addItem` takes the origin as a second
// argument, and exactly one caller passes a non-nil value: [Service.Decide], which writes the
// decision row and the item event **in one transaction**. A deferred constraint trigger then
// refuses the commit if the two do not agree (migration 00069 §6).
//
// So the guarantee has a Go half and a database half, and neither is the whole of it:
//
//   - Go: there is no exported way to mark a line as AI-originated. A future caller who wants one
//     has to add a field to a struct in this file, which is a diff somebody reads.
//   - Database: a line marked as AI-originated cannot be committed without a decision naming it,
//     for every role including the projector, and invariant 130 re-asks the question of the whole
//     table afterwards.
//
// What neither half can do is stop a physician reading a suggestion off the screen and typing the
// same drug into the search box. That is a hand-typed line and the system records it as one, which
// §1 says is correct — an accepted suggestion gets no easier passage and a typed line no harder
// one. The honest statement of the guarantee is therefore about **recorded provenance**: every
// line whose origin is the AI says so, and every line that says so was decided on.
//
// # What the model is allowed to be
//
// Very little, and that is the design rather than a limitation. The agent is given a shortlist of
// products **this server built** — active, in this facility's formulary, not on the controlled
// register — and its answer names a `product_id` from that shortlist. It never writes a drug name.
// §2's first two refusals are therefore not prompt instructions that a model may ignore: a name
// outside the formulary is not expressible, and one that is expressible is re-checked here before
// anything is stored.
//
// The controlled half of that is keyed on the **molecule** and not on a row in this clinic's
// formulary. `core.controlled_molecule` holds testosterone whether or not the clinic stocks it, and
// `core.generic_is_controlled` — the one predicate both the shortlist subtraction and the answer
// check read — resolves it against the formulary at read time. So the rule holds from before a
// product exists rather than from the moment somebody adds one. Migration 00069 §2 argues it at
// length, because the first version of that table got it the other way round and the failure was
// silent.
//
// §2's third refusal — *"propose stopping or changing a medicine prescribed by another physician"*
// — is held the same way and is worth naming explicitly: there is no field in [Suggestion] that
// can say "stop" or "change". A suggestion is an addition or it is nothing.
//
// §2's fourth — *"propose anything for a patient with no recorded allergy status"* — is the one
// that cannot be expressed in a schema, because it is about the patient rather than the answer. It
// is a gate in front of the gateway: the allergy status is read first, and a patient without one
// produces a run in state REFUSED and **no model call at all**. Not a call whose answer is
// discarded — no call. CP54's stop is hard, and a machine that suggested around it would teach
// people the stop is soft.

// AgentCode is the prompt registry's name for the prescribing agent.
//
// A constant here and a row in `core.ai_agent`, the same shape `synthesis.AgentCode` has: the Go
// side names the prompt file and the database side carries the description, the grounding
// requirement and the budget.
const AgentCode = "clinical.prescribing"

// MaxSuggestions is the most the agent may offer for one draft.
//
// Six, matching CP71's `draft_medications` cap and chosen for the same reason: a panel a physician
// scrolls is a panel he stops reading. §3's whole argument is about the attention a suggestion
// costs, and the cheapest way to keep that cost honest is to keep the list short enough to read in
// the ninety seconds §7.1 budgets for the whole briefing.
const MaxSuggestions = 6

// MaxCandidates bounds the shortlist the model chooses from.
//
// The formulary is 250 products and the prompt would carry every one of them at about thirty
// tokens each. Bounded because an unbounded shortlist is an unbounded bill and because a model
// choosing from 250 near-identical brand names chooses worse than one choosing from a hundred —
// but bounded *after* the clinical filters rather than instead of them, so what is dropped is the
// least clinically relevant brand rather than an arbitrary prefix.
const MaxCandidates = 120

// MaxDecisionNote bounds the free text on a rejection.
//
// Generous rather than tight: a physician explaining why he is declining a drafted insulin should
// not be truncated mid-sentence. Bounded at all because this text goes into a ledger payload.
const MaxDecisionNote = 2000

// ---------------------------------------------------------------------------
// The suggestion, as offered
// ---------------------------------------------------------------------------

// Suggestion is one medicine the AI proposed, exactly as it proposed it.
//
// **Nothing in this struct is ever rewritten.** §4: *"the suggestion is stored as offered, by
// value, and is never mutated by the edit"*, because the difference between what the model said
// and what the physician wrote is the entire training signal Phase 3 will read. The database
// enforces it for every role; this struct having no setters is the ergonomics.
type Suggestion struct {
	ID      uuid.UUID `json:"id"`
	RunID   uuid.UUID `json:"run_id"`
	Ordinal int       `json:"ordinal"`

	// ProductID is a product in this facility's formulary. Not optional, unlike a prescription
	// line's: a physician may write a medicine this clinic does not stock, and the AI may not.
	ProductID   uuid.UUID `json:"product_id"`
	Label       string    `json:"product_label"`
	GenericName string    `json:"generic_name"`
	Strength    string    `json:"strength,omitempty"`
	FormCode    string    `json:"form_code,omitempty"`

	Dose         string   `json:"dose"`
	DailyDose    *float64 `json:"daily_dose,omitempty"`
	DoseUnit     string   `json:"dose_unit,omitempty"`
	Frequency    string   `json:"frequency"`
	DurationDays *int     `json:"duration_days,omitempty"`
	Route        string   `json:"route,omitempty"`

	// RationaleEN and RationaleBN are §2's *"the reasoning that produced it"*. Both, because the
	// physician may be reading either and a suggestion he cannot audit in five seconds is one he
	// will rubber-stamp or ignore.
	RationaleEN string `json:"rationale_en"`
	RationaleBN string `json:"rationale_bn"`
	// Basis is §2's *"the facts it rests on"*: CP71 fact references, already checked by the
	// gateway's grounding arm against the context the model was shown.
	Basis []string `json:"basis"`

	OfferedAt time.Time `json:"offered_at"`

	// Decision is what the physician did, or **nil**.
	//
	// A pointer and not a string with an "UNACTIONED" member, because §1 is explicit that
	// *"a suggestion that is never acted on is not a rejection"* and that conflating the two would
	// let silence be read as a decision. A nil pointer is not a value a comparison accidentally
	// matches; a fourth enum member is.
	Decision *Decision `json:"decision,omitempty"`
}

// State is the word a screen shows. Derived from [Suggestion.Decision] and never stored.
//
// The string "UNACTIONED" exists in this function and nowhere else in the system — not in the
// database, not in an event payload, not as a constant a query could compare against. A caller
// deciding whether a suggestion was rejected asks the pointer, not this.
func (s Suggestion) State() string {
	if s.Decision == nil {
		return "UNACTIONED"
	}
	return string(s.Decision.Kind)
}

// Actioned reports whether a physician has answered this suggestion.
func (s Suggestion) Actioned() bool { return s.Decision != nil }

// ---------------------------------------------------------------------------
// The decision
// ---------------------------------------------------------------------------

// DecisionKind is one of §4's three.
type DecisionKind string

const (
	// DecisionAccepted — a prescription line identical to the suggestion.
	DecisionAccepted DecisionKind = "ACCEPTED"
	// DecisionEdited — a prescription line that differs, with the difference recoverable because
	// the suggestion is still stored as offered.
	DecisionEdited DecisionKind = "EDITED"
	// DecisionRejected — nothing on the prescription, and optionally a reason from §5.
	DecisionRejected DecisionKind = "REJECTED"
)

// Decision is what the physician did with one suggestion.
type Decision struct {
	ID           uuid.UUID    `json:"id"`
	SuggestionID uuid.UUID    `json:"suggestion_id"`
	Kind         DecisionKind `json:"decision"`

	DecidedBy uuid.UUID `json:"decided_by"`
	DecidedAt time.Time `json:"decided_at"`

	// ItemID is the prescription line this produced, for an acceptance or an edit. Nil for a
	// rejection, and the database refuses any other combination.
	ItemID *uuid.UUID `json:"prescription_item_id,omitempty"`

	// ReasonCode is a code from `core.ai_suggestion_reject_reason`. Optional, because §5 says a
	// physician mid-clinic must be able to dismiss a suggestion in one action.
	ReasonCode string `json:"reject_reason_code,omitempty"`
	Note       string `json:"reject_note,omitempty"`
}

// RejectReason is one row of §5's vocabulary.
//
// Reference data rather than a Go enum, so that Dr Nahid can change the wording or add a reason
// without a code release — the same rule as the counselling templates and the formulary. There is
// deliberately no `RejectReason` constant anywhere in this package: a constant would be a second
// copy of the list, and the copy in Go would be the one that stopped matching.
type RejectReason struct {
	Code     string `json:"code"`
	LabelEN  string `json:"label_en"`
	LabelBN  string `json:"label_bn"`
	Ordering int    `json:"ordering"`
}

// ---------------------------------------------------------------------------
// The run
// ---------------------------------------------------------------------------

// RunState is what became of one ask.
type RunState string

const (
	// RunReady — the model answered and whatever survived §2 is stored. **Including nothing.**
	// A READY run with no suggestions is "the AI proposed nothing", which is a different fact
	// from "nobody asked" and is shown differently.
	RunReady RunState = "READY"
	// RunRefused — a §2 gate stopped this before any model was contacted.
	RunRefused RunState = "REFUSED"
	// RunFailed — the gateway refused or the model could not answer. D-15: the clinic never stops
	// because AI stopped, so this is a sentence on a screen rather than an error on a form.
	RunFailed RunState = "FAILED"
)

// Refusal names which §2 rule stopped a run before it began.
type Refusal string

const (
	// RefusalNoAllergyStatus is §2's hard stop, and the only one that is about the patient rather
	// than about the answer. *"Not 'propose cautiously' — propose nothing."*
	RefusalNoAllergyStatus Refusal = "NO_ALLERGY_STATUS"
	// RefusalNotADraft is a prescription that has left DRAFT. Suggestions are offered against a
	// sheet somebody is writing; offering them against one in QA would be offering a change to a
	// document the physician cannot change.
	RefusalNotADraft Refusal = "NOT_A_DRAFT"
	// RefusalNoCandidates is a formulary with nothing left after §2's filters. Recorded rather
	// than treated as an empty answer, because "we asked and it said nothing" and "there was
	// nothing to ask about" are different states and only one of them is the model's fault.
	RefusalNoCandidates Refusal = "NO_CANDIDATES"
)

// Run is one ask of the agent against one draft.
type Run struct {
	ID             uuid.UUID `json:"id"`
	PrescriptionID uuid.UUID `json:"prescription_id"`
	PatientID      uuid.UUID `json:"patient_id"`
	VisitID        uuid.UUID `json:"visit_id"`

	State   RunState `json:"state"`
	Refusal Refusal  `json:"refusal,omitempty"`

	InteractionID *uuid.UUID `json:"ai_interaction_id,omitempty"`
	PromptVersion string     `json:"prompt_version,omitempty"`
	ModelVersion  string     `json:"model_version,omitempty"`

	OfferedCount int `json:"offered_count"`
	// DroppedCount and DroppedReasons are how many of the model's items this server threw away and
	// why. §2: *"a suggestion that fails validation is dropped, not repaired."* A count nobody can
	// see is a rule nobody can audit, so it is on the row and on the wire.
	DroppedCount   int            `json:"dropped_count"`
	DroppedReasons map[string]int `json:"dropped_reasons,omitempty"`

	FailureDetail string `json:"failure_detail,omitempty"`

	RequestedAt time.Time `json:"requested_at"`
	RequestedBy uuid.UUID `json:"requested_by"`

	// Suggestions is what survived, with each one's decision or nil.
	Suggestions []Suggestion `json:"suggestions"`

	// MessageEN and MessageBN say what state this is, in words. Filled by the service rather than
	// by a client, for D-15's reason: every state is a 200 with a sentence, because an empty panel
	// tells a physician nothing about whether the system tried.
	MessageEN string `json:"message_en"`
	MessageBN string `json:"message_bn"`
}

// Unactioned is how many suggestions nobody has answered.
//
// Counted from the pointer rather than from a state string, which is the same discipline [State]
// describes: there is one place in this package that decides what "unactioned" means.
func (r Run) Unactioned() int {
	n := 0
	for _, s := range r.Suggestions {
		if !s.Actioned() {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// Drop reasons
// ---------------------------------------------------------------------------

// Drop names why one item of a model's answer was thrown away.
//
// These are counted onto the run and are never shown to the physician as a suggestion — a dropped
// item does not appear anywhere a clinician reads, because §2's *"dropped, not repaired"* means
// the item is gone rather than shown with a caveat. They are on the row so that a deployment where
// the model keeps proposing controlled drugs is visible to Quality.
type Drop string

const (
	// DropNotInFormulary — a product id the shortlist did not contain, or one that is inactive or
	// belongs to another facility. §2's first refusal, enforced against the table rather than
	// against the prompt.
	DropNotInFormulary Drop = "not_in_formulary"
	// DropControlled — a product carrying a molecule on `core.controlled_molecule`. §2's second.
	//
	// The register is keyed on the molecule and not on a formulary row, so this holds for a
	// molecule the clinic does not stock yet as well as for one it does — see migration 00069 §2
	// for why that distinction is the whole of the rule rather than a detail of it.
	DropControlled Drop = "controlled"
	// DropIncomplete — a suggestion missing a dose or a frequency, or carrying a duration or a
	// daily dose outside what a prescription line may hold. The schema in the prompt asks for all
	// of it; this is the check that does not depend on the model having obeyed.
	DropIncomplete Drop = "incomplete"
	// DropNoRationale — a suggestion with no reasoning or no basis. §2 makes both mandatory and
	// argues why: a suggestion a physician cannot audit in five seconds is a failure whether he
	// rubber-stamps it or ignores it.
	DropNoRationale Drop = "no_rationale"
	// DropDuplicate — the same product twice in one answer, or a product already on the draft.
	// The second is the interesting one: proposing a medicine the physician has already written
	// is noise that costs exactly the attention §3 is trying to protect.
	DropDuplicate Drop = "duplicate"
	// DropOverCap — beyond [MaxSuggestions].
	DropOverCap Drop = "over_cap"
)

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

var (
	// ErrNoSuggestion — a decision naming a suggestion this prescription does not have. A 404,
	// and the same 404 a suggestion in another facility gives: a different answer for "exists but
	// is not yours" is an answer that tells a caller the row exists.
	ErrNoSuggestion = errors.New("no such AI prescribing suggestion")
	// ErrAlreadyDecided — a second decision on one suggestion. Refused rather than absorbed: a
	// physician who accepted and then changed his mind removes the line, which CP80 records with
	// its own reason, and overwriting the decision would erase the acceptance from the trail.
	ErrAlreadyDecided = errors.New("that suggestion has already been decided")
	// ErrUnknownDecision — not one of §4's three.
	ErrUnknownDecision = errors.New("a decision on a suggestion is accepted, edited or rejected")
	// ErrReasonBelongsToRejection — a reason code or note sent with an acceptance. Refused rather
	// than dropped: a client sending it has misunderstood which button was pressed, and absorbing
	// it would put a reason nobody meant into the signal §5 exists to collect.
	ErrReasonBelongsToRejection = errors.New("only a rejection carries a reason")
	// ErrUnknownReason — a reason code that is not in `core.ai_suggestion_reject_reason`.
	ErrUnknownReason = errors.New("that is not a rejection reason this clinic uses")
	// ErrSuggestionsUnavailable — the agent is not wired into this process. Loud rather than
	// silent, for the reason CP80 gives about the safety engine: a panel that showed "no
	// suggestions" because nothing was plugged in is the worst available failure.
	ErrSuggestionsUnavailable = errors.New("the AI prescribing agent is not wired into this process")
)
