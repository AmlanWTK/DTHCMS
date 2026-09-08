// Package dashboard is the physician's three-panel screen, served as one read (CP73, §8).
//
// # The one sentence this package exists to make true
//
// *Opening a patient costs one request.*
//
// That is the checkpoint's own wording — "one request, not twelve" — and it is an acceptance
// criterion rather than a preference. Twelve round trips on a clinic's shared connection is
// how a dashboard becomes a second and a half of spinner, and the physician who waits through
// that twice learns to keep the screen open on the previous patient. What is being bought is
// not milliseconds; it is a screen a person trusts enough to look at.
//
// So this module composes. It owns no table, writes no projection, and adds no query: every
// number on the screen is read through the store of the module that owns it — `clinical` for
// values, `allergy` for allergies, `history` for conditions, `counseling` for the checklist,
// `synthesis` for the summary — and assembled into one payload. The alternative, a
// dashboard-shaped read model, would be a second copy of six modules' data with its own
// staleness and its own rebuild; the composition is a joinless fan-out that the database
// answers from indexes those modules already have.
//
// # Why the panels are read concurrently and what that costs
//
// Nine reads, issued together, on nine pooled connections. The wall time of the endpoint is
// therefore the slowest read rather than the sum, which for a patient with ten years of
// history is the observation history — and it is the one this module bounds explicitly (see
// [TrendPoints], and `clinical.Store.Current`, which returns one row per code rather than one
// per value).
//
// The cost is that a dashboard load takes as many connections as it has panels for the length
// of the slowest one. That is a real number to watch on a clinic with sixteen physicians,
// and it is why the fan-out is *fixed* — nine reads whatever the patient's history looks
// like, never one per year or one per code. A dashboard whose connection cost grew with the
// record would be a dashboard that fails first on exactly the patients it matters most for.
//
// # What is deliberately not here
//
// **The timeline** (CP74), **prescription editing** (CP81) and **the records panel** (CP110).
// Each is its own screen and its own read; folding them in would make one request cost what
// three screens cost and would put the checkpoint's own promise out of reach.
//
// **The synthesis's assembled context.** `synthesis.Run` carries the whole [synthesis.Context]
// — every fact the model was shown — and it is tempting to hand it over, because D-15's
// degraded state is built from it. It is not handed over, and the reason matters: the context
// is a *de-identified snapshot* with no operator names in it by construction (see the
// synthesis package comment), and the dashboard's left panel is the *live record with
// attribution on every value*. Drawing both would put the same numbers on one screen twice,
// from two moments, with provenance on one copy and not the other — and a physician who read
// the wrong copy would be reading a number nobody's name is against. The centre panel gets
// the state, the sentences and the model's own words; the record it is a summary *of* is the
// panel beside it.
//
// # Attribution is why several fields look redundant
//
// §4.3's promise — attribution one interaction away on every value — is a promise about
// *values*, so the payload carries observations as `clinical.Observation` rather than as
// formatted numbers. Every one of them arrives with `recorded_by`, `recorded_role`,
// `station_code`, `device_id`, `source` and `status`, which is exactly what CP61's
// `ValueWithAttribution` needs and exactly what a "vitals: {systolic: 140}" shape would have
// thrown away. The payload is larger for it. That is the trade the criterion asks for.
package dashboard

import (
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/allergy"
	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/counseling"
	"github.com/AmlanWTK/DTHCMS/backend/internal/history"
	"github.com/AmlanWTK/DTHCMS/backend/internal/synthesis"
)

// TrendPoints is how many values of one code the snapshot carries.
//
// §8 asks for "sparklines of the last 5 HbA1c values" and the number is taken literally for
// every trend rather than only for that one: five points is what a sparkline the width of a
// snapshot card can draw without turning into a smear, and it is the series a physician reads
// as "getting better or getting worse" rather than as a chart. The full series is CP74's
// timeline, one click away, and duplicating it here would be paying for a chart nobody can
// read at this size.
const TrendPoints = 5

// TrendCodes are the series the snapshot draws, in the order it draws them.
//
// A list rather than "every code this patient has", and the difference is the whole panel. A
// patient with ten years of history has thousands of values under dozens of codes; a snapshot
// that drew a sparkline for each would be a wall of charts, which is the information-density
// failure this checkpoint's own risk section names. These four are the ones a diabetes and
// thyroid clinic reads a trajectory in — the glycaemic control, the weight it moves with, and
// the two halves of the blood pressure that decide the cardiovascular conversation.
//
// It is a Go constant and not a table, and that is a shortcut with a name: which series a
// physician wants first is a clinical preference, and Dr. Nahid is the only person who can
// settle it. Moving it into `core.observation_code` is a column and a migration on the day he
// says. Until then it is four codes in one place rather than four codes decided by whichever
// screen was written last.
var TrendCodes = []string{"HBA1C", "BODY_WEIGHT", "BP_SYSTOLIC", "BP_DIASTOLIC"}

// View is the whole screen, in one object.
//
// # Why every panel is nullable and none is an empty list standing in for a refusal
//
// D-15 — *fail visible, never fail silent, never fail invented* — is the rule this shape is
// built to. A panel the caller may not read is **absent**, and its absence is named in
// [View.Omitted] with a sentence in both languages. A panel that is present and empty means
// something entirely different: it means this patient has none of that thing.
//
// Collapsing the two — drawing a refused panel as an empty one — is the failure mode worth
// naming, because it looks like reassurance. A pharmacist who may not read diagnoses would
// see "no active conditions" rather than "you may not see this", and would be wrong about the
// patient rather than wrong about their own permissions.
type View struct {
	// AsOf is when the server assembled this. On the payload because every panel below is a
	// read of a different moment within a few milliseconds of it, and a screen that says
	// "as of 10:42" is a screen whose staleness a physician can judge.
	AsOf time.Time `json:"as_of"`

	Patient Identity      `json:"patient"`
	Visit   *VisitContext `json:"visit"`

	// Access says on what basis this record is open. Never omitted, never inferred by the
	// client: reading a patient under break-glass is a fact about the reading, and the person
	// doing it must be told at the moment they do it rather than discover it in an audit.
	Access Access `json:"access"`

	// --- left panel: the snapshot ---

	Allergies *allergy.State    `json:"allergies"`
	Alerts    []clinical.Alert  `json:"critical_alerts"`
	Vitals    []clinical.Observation `json:"vitals"`
	BodyMass  *BodyMass         `json:"body_mass"`
	Trends    []Trend           `json:"trends"`
	// Conditions is §8's "active diagnoses", named for what the record actually holds.
	//
	// There is no diagnosis table in this system yet — coded diagnoses arrive with the
	// prescription engine at CP81 — so what a physician can be shown today is the coded
	// history: comorbidities and complaints an officer recorded against ICD-10 or the
	// clinic's own catalogue, with the ones somebody has since marked resolved left out.
	// Calling that "diagnoses" on the wire would be claiming a clinical act nobody performed.
	Conditions []history.Item `json:"active_conditions" visible:"history.read"`

	Growth       *clinical.Growth       `json:"growth"`
	WeightStatus *clinical.WeightStatus `json:"weight_status,omitempty"`

	Counseling *counseling.GateStatus `json:"counseling"`

	// --- centre panel: the clinical summary ---

	Summary *Summary `json:"summary" visible:"ai.synthesis.read"`

	// --- right panel: the AI assistant ---

	Assistant *Assistant `json:"assistant" visible:"ai.synthesis.read"`

	// Omitted names every panel this caller was not shown, and why. See the type comment.
	Omitted []Omission `json:"omitted"`
}

// Identity is who this is, and nothing that is not needed to know it.
//
// Deliberately narrower than `patient.Patient`: no address, no phone, no emergency contact,
// no socioeconomic record. Those are the registration desk's screen and the CRM's, and a
// dashboard payload carrying them would be shipping a patient's home address to a screen that
// draws a name and an age — which is the kind of over-fetch that is invisible until a browser
// cache or a screenshot in a support ticket makes it visible.
type Identity struct {
	ID uuid.UUID `json:"id"`
	// ClinicalID carries a visibility tag because the serialiser's default-restrictive rule
	// fires on the substring "clinical". That is a false positive of a rule worth keeping —
	// and the tag is the honest answer to it: the handle is what a desk reads aloud, and
	// anybody who may see the patient at all may see it.
	ClinicalID string `json:"clinical_id" visible:"patient.read.demographics"`

	NameEN string `json:"name_en"`
	NameBN string `json:"name_bn,omitempty"`
	Sex    string `json:"sex"`

	BirthDate string `json:"birth_date"`
	// AgeText is the age as a person says it — "43 years", "8 months". Composed here rather
	// than on the client because the paediatric case is where it matters: a screen that
	// rounded 8 months to 0 years would be a screen that lost the growth panel's whole point.
	AgeText   string `json:"age_text"`
	AgeMonths int    `json:"age_months"`

	Status string `json:"status"`
}

// VisitContext is the journey this dashboard is about.
//
// Nil when the patient has never attended. That is a real state — somebody registered at the
// desk this morning and has not yet been anywhere — and it is not an error: the snapshot
// still has demographics, allergies and whatever history was taken, and a screen that refused
// to draw without a visit would refuse exactly at registration.
type VisitContext struct {
	ID        uuid.UUID `json:"id"`
	VisitCode string    `json:"visit_code"`
	VisitType string    `json:"visit_type"`
	Status    string    `json:"status"`
	// Open says whether this is the visit happening now, as against the most recent closed
	// one. The distinction decides what half the screen means: an open visit's counselling
	// checklist is something to finish, and a closed one's is a record.
	Open           bool      `json:"open"`
	ChiefComplaint string    `json:"chief_complaint,omitempty"`
	ClinicDay      time.Time `json:"clinic_day"`
	OpenedAt       time.Time `json:"opened_at"`
}

// AccessBasis is how this record came to be open to this caller.
type AccessBasis string

const (
	// BasisNormal is the ordinary path: the caller's role reaches this patient.
	BasisNormal AccessBasis = "NORMAL"
	// BasisBreakGlass is an emergency access that somebody opened with a typed
	// justification, and that every administrator was told about at the moment it opened.
	BasisBreakGlass AccessBasis = "BREAK_GLASS"
)

// Access is the basis, and the emergency door's state when one is open.
//
// # Why the physician is told rather than only the auditors
//
// CP22 records break-glass and alarms every administrator. What it does not do — and what a
// dashboard can — is tell *the person using it* that this is what they are doing, while they
// are doing it. An emergency access the clinician has forgotten is open is an emergency
// access that stops being an emergency and becomes a habit, which is the failure mode the
// whole mechanism exists to prevent.
type Access struct {
	Basis AccessBasis `json:"basis"`
	// BreakGlass is the open door, when there is one. Absent under BasisNormal.
	BreakGlass *BreakGlassNote `json:"break_glass,omitempty"`
}

// BreakGlassNote is the open door, said plainly.
//
// The justification is on it. It was typed by this same person, so showing it back is not a
// disclosure — and reading one's own sentence from an hour ago is the most effective reminder
// available that the door is still open.
type BreakGlassNote struct {
	ID            uuid.UUID `json:"id"`
	Justification string    `json:"justification"`
	GrantedAt     time.Time `json:"granted_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	// Acknowledged says an administrator has seen it. False is not a fault; it means nobody
	// has looked yet, which is a fact worth showing rather than hiding behind a boolean
	// nobody renders.
	Acknowledged bool `json:"acknowledged"`
}

// Omission is a panel this caller did not get, and why.
type Omission struct {
	// Panel is the JSON field that is absent: "summary", "active_conditions".
	Panel string `json:"panel"`
	// Permission is what would have been needed. Named rather than hidden, because the
	// remedy is a grant and the person who can make it needs to be told which one.
	Permission string `json:"permission"`
	ReasonEN   string `json:"reason_en"`
	ReasonBN   string `json:"reason_bn"`
}

// BodyMass is the BMI with its class, which §8 asks for as one thing.
//
// # Why the class is computed here and the value is not
//
// The BMI is a stored derived observation with its formula, its version and the two values it
// was computed from (CP43) — so it is read, never recomputed, and it arrives with the
// attribution of the derivation that produced it. The *class* is not stored anywhere: it is a
// banding of the value, and `clinical/calc.Classify` is the one implementation of it. Calling
// that function here is not a second opinion; writing the cut-offs into a screen would be.
//
// **The Asian scale, always.** A BMI of 24 is "normal" internationally and "overweight" in a
// Bangladeshi patient, and the entire screening pathway hangs on which side of that line
// somebody falls. The scale is on the payload rather than assumed by the client, because a
// class with no scale beside it is a word two people can read differently.
type BodyMass struct {
	// Observation is the stored BMI, with everything CP61 needs to say who derived it.
	Observation clinical.Observation `json:"observation"`
	Class       string               `json:"class"`
	// ClassVersion is bumped when a cut-off moves. On the wire so that a screenshot from
	// last year can be read against the bands that were in force when it was taken.
	ClassVersion string `json:"class_version"`
	Scale        string `json:"scale"`
}

// Trend is one code's last few values, oldest first.
//
// The points are whole observations rather than pairs of numbers, for the criterion-5 reason:
// a sparkline whose points cannot be interrogated is a picture, and §4.3 asks for attribution
// on every value. A physician who sees a step in the HbA1c must be able to ask who typed the
// value at the step without leaving the screen.
type Trend struct {
	Code  string `json:"code"`
	Unit  string `json:"unit,omitempty"`
	// Points are oldest first. The direction is fixed here rather than left to the client
	// because two screens reading it differently would draw the same patient improving and
	// deteriorating.
	Points []clinical.Observation `json:"points"`
	// Change is the arithmetic, done once on the server.
	//
	// It is here for the same reason [synthesis.TrendChange] exists: a difference computed in
	// two places is a difference that will one day be computed two ways. The client draws
	// what this says.
	Change *TrendChange `json:"change,omitempty"`
}

// TrendChange is the first-to-last difference of a trend, with the interval it happened over.
type TrendChange struct {
	From     float64 `json:"from"`
	To       float64 `json:"to"`
	Delta    float64 `json:"delta"`
	OverDays int     `json:"over_days"`
}

// --- the centre panel ---

// Summary is the pre-consultation narrative as the centre panel needs it.
//
// A projection of [synthesis.View] rather than the thing itself. What is dropped is the
// assembled context — see the package comment — and what is added is the model's own answer
// unpacked from the raw JSON it is stored as, so that the client is not parsing an untyped
// blob to find the sentence it must mark as machine-written.
//
// **`AIGenerated` is always true and is never omitted from the wire.** It looks redundant on
// a struct whose whole purpose is to carry an AI answer, right up until something serialises
// this into a print, an export or a record — and then the marker is the only thing separating
// a draft from a fact. §10.6's third permanent invariant asks for exactly that, everywhere it
// appears, and `synthesis.Run` carries the same field for the same reason.
type Summary struct {
	State       synthesis.State `json:"state"`
	AIGenerated bool            `json:"ai_generated"`
	// Degraded says the screen should read the record beside this rather than this. True for
	// everything except a summary that is ready and current.
	Degraded  bool   `json:"degraded"`
	MessageEN string `json:"message_en"`
	MessageBN string `json:"message_bn"`
	// Requestable says the "prepare a summary" button should be live.
	Requestable bool `json:"requestable"`

	// Narrative is §8's one-page flowing account, in the model's words. English; the decision
	// and its reasoning are in the synthesis package comment and are Dr. Nahid's to confirm.
	Narrative string `json:"narrative,omitempty"`
	// KeyPoints is the model's own summary of its summary. Kept separate from the narrative
	// because a physician with forty seconds reads these and nothing else.
	KeyPoints []string `json:"key_points,omitempty"`
	// RedFlags is §6.4's red-line carry-through as the model expressed it. Two severities,
	// because a list where everything is urgent is a list nobody reads.
	RedFlags []RedFlag `json:"red_flags,omitempty"`
	// Citations is every fact reference the model cited, so that a reader can see the answer
	// rests on something. CP72's validator has already checked that each one exists.
	Citations []string `json:"citations,omitempty"`
	// Confidence is the model's own number, 0 to 1. On the wire and drawn as what it is: a
	// model's opinion of itself, which is evidence about the model and not about the patient.
	Confidence *float64 `json:"confidence,omitempty"`

	Provenance *Provenance `json:"provenance,omitempty"`
}

// RedFlag is one thing the model says needs attention now.
type RedFlag struct {
	// Severity is "urgent" or "attention".
	Severity  string   `json:"severity"`
	Statement string   `json:"statement"`
	Basis     []string `json:"basis,omitempty"`
}

// Provenance is what produced this summary, for criterion 5's sake.
//
// Attribution on an AI sentence is not a person's name — no person wrote it — so it is the
// prompt version, the model version, the generation, when it ran and what the grounding check
// said. A physician who is accountable for acting on a draft must be able to reach all five
// without leaving the page; CP70's outbound log is where the payload itself lives, and
// `InteractionID` is the handle that reaches it.
type Provenance struct {
	Generation    int                       `json:"generation"`
	Trigger       synthesis.Trigger         `json:"trigger"`
	PromptVersion string                    `json:"prompt_version,omitempty"`
	ModelVersion  string                    `json:"model_version,omitempty"`
	InteractionID *uuid.UUID                `json:"ai_interaction_id,omitempty"`
	RequestedAt   time.Time                 `json:"requested_at"`
	FinishedAt    *time.Time                `json:"finished_at,omitempty"`
	Grounding     synthesis.GroundingState  `json:"grounding_state"`
	// GroundingFindings is how many claims failed the check. Zero on a passed run; the number
	// a reviewer is about to open on a failed one. Never `omitempty`: a zero is a
	// measurement, and an absent field would be read as one.
	GroundingFindings int         `json:"grounding_findings"`
	FailureKind       synthesis.FailureKind `json:"failure_kind,omitempty"`
	FailureDetail     string      `json:"failure_detail,omitempty"`
}

// --- the right panel ---

// SuggestionOrigin says what wrote a line in the right panel, and it is a safety control.
//
// §8's right panel mixes two things that look identical on a screen and are not: a diagnosis a
// language model proposed, and a gap a deterministic assembler found because it looked for an
// HbA1c and there was not one. The second is arithmetic over the record; the first is a
// machine's opinion. A panel that drew them the same way would either mark the arithmetic as
// AI — which trains a physician to discount the one item on the panel that is certainly
// true — or fail to mark the opinion, which is criterion 3 failed outright.
type SuggestionOrigin string

const (
	// OriginModel is a language model's proposal. Marked, always, unmistakably.
	OriginModel SuggestionOrigin = "MODEL"
	// OriginSystem is a deterministic finding of the assembler: a missing measurement, a
	// station nobody reached, an overdue review. No model was involved and none is credited.
	OriginSystem SuggestionOrigin = "SYSTEM"
)

// SuggestionKind groups the right panel's items.
type SuggestionKind string

const (
	// KindDiagnosis is §8's "suggested ICD-coded diagnoses".
	KindDiagnosis SuggestionKind = "DIAGNOSIS"
	// KindInvestigation is a drafted investigation.
	KindInvestigation SuggestionKind = "INVESTIGATION"
	// KindMedication is a drafted drug with its dose. **Never a prescription** — see
	// [Decision].
	KindMedication SuggestionKind = "MEDICATION"
	// KindGap is a missing-data alert: deterministic, and §8 names it in the same panel.
	KindGap SuggestionKind = "GAP"
)

// Assistant is the right panel.
type Assistant struct {
	// Generation is the synthesis run these suggestions came from. On the panel because a
	// decision is recorded against it: accepting a diagnosis drafted from this morning's
	// eight o'clock data is a different act from accepting one drafted after the labs came
	// back, and a decision with no generation on it cannot tell the two apart.
	Generation  int          `json:"generation"`
	AIGenerated bool         `json:"ai_generated"`
	Suggestions []Suggestion `json:"suggestions"`
}

// Suggestion is one item with accept, edit and reject against it.
type Suggestion struct {
	// Ref is stable across re-runs of the same generation and is what a decision names.
	//
	// Derived from the kind and the item's own text rather than from its position in the
	// list, because a model that reorders its answer between two reads of the same run would
	// otherwise move every decision onto the wrong line. See [suggestionRef].
	Ref    string           `json:"ref"`
	Kind   SuggestionKind   `json:"kind"`
	Origin SuggestionOrigin `json:"origin"`

	// Label is the thing itself: the diagnosis, the investigation, the drug.
	Label string `json:"label"`
	// Detail is why. The model's rationale, or the assembler's sentence for a gap.
	Detail string `json:"detail,omitempty"`
	// Code is the ICD-10 code where the model offered one. Absent is honest and common: a
	// model that cannot code a condition should say the condition, and a screen that invented
	// a code to fill the column would be putting a billing artefact into a clinical record.
	//
	// The visibility tag is `diagnosis.read` and it is not a formality the serialiser forced:
	// it did force it — the default-restrictive rule fired on "icd" and refused the whole type
	// until this line existed — and the answer it forced is the correct one. A coded diagnosis
	// is what §4.4 blinds the pharmacist and the registration desk from, and a *suggested* one
	// is no less a clinical interpretation for having been suggested.
	Code string `json:"icd10,omitempty" visible:"diagnosis.read"`

	// Dose, Frequency and Route are the drafted prescription's shape, for a MEDICATION.
	Dose      string `json:"dose,omitempty"`
	Frequency string `json:"frequency,omitempty"`
	Route     string `json:"route,omitempty"`

	// Severity is a GAP's — "note" or "important" — and empty for everything else.
	Severity string `json:"severity,omitempty"`

	// Basis is the fact references this rests on. Criterion 5 for a machine-written line:
	// the attribution of a suggestion is the evidence it was drawn from, and it is one
	// interaction away like every other value on the screen.
	Basis []string `json:"basis,omitempty"`

	// Decision is what the physician did with it, if anything yet.
	Decision *Decision `json:"decision,omitempty"`
}

// DecisionKind is what a physician did with a suggestion.
type DecisionKind string

const (
	// Accepted — the physician agrees. See [Decision] for what that does and does not do.
	Accepted DecisionKind = "ACCEPTED"
	// Edited — the physician agrees with a changed version, which is on the record.
	Edited DecisionKind = "EDITED"
	// Rejected — the physician disagrees.
	Rejected DecisionKind = "REJECTED"
)

// Decision is a physician's answer to one suggestion, and it is a record rather than a
// control's colour.
//
// # What accepting does, and what it deliberately does not do
//
// **It records an intent. It does not write a prescription.** Prescription editing is CP81 and
// is out of scope here, and the honest consequence is worth stating rather than working
// around: accepting a drafted metformin 500mg puts a row in the ledger saying that this
// physician, at this time, agreed with this draft — and puts nothing on any prescription.
// CP81 will read these when it builds the prescription screen, and until then the value of
// the record is that it exists at all.
//
// Building the other thing — acceptance that writes a drug into a live prescription — would
// be building CP81's write path inside CP73 with none of CP81's safety machinery: no
// interaction check, no dose validation against renal function, no formulary, no signature.
// §7.3 makes that split permanent: *generative models draft, deterministic databases and a
// human signature prescribe.*
//
// # Why a rejection is stored and not simply hidden
//
// A rejected suggestion that vanished would leave no evidence the physician had considered
// it, which is the wrong record in both directions. Medico-legally, "the system suggested a
// thyroid function test and the physician declined it" is a defensible sentence and an absent
// one is not. Operationally, the rejection rate per suggestion kind is the only measurement
// that says whether §8's right panel is any good — a panel whose diagnoses are rejected nine
// times in ten is a panel that is costing attention and buying nothing, and nobody would ever
// find that out from a screen that quietly forgot.
//
// # Where it lives
//
// In the ledger, as an `AI_SUGGESTION_DECIDED` event on the visit, and **nowhere else**.
// There is no read model and no table: the decisions for one visit are folded from that
// visit's own event stream, which is bounded by one journey through the clinic and is a
// handful of rows. §4.1 says the event log and not the current-state table is the source of
// truth, and this is one of the few places where that is literally how it is read.
//
// The cost is real and is worth naming: *"how often does the physician reject a drafted
// diagnosis, across the clinic, this quarter"* cannot be answered by a query today. It needs
// a projection, and a projection is what CP81 will want anyway when these decisions start
// meaning something to a prescription. Building the read model now would be building it
// twice.
type Decision struct {
	Kind DecisionKind `json:"kind"`
	// Edited is the physician's own wording, for [Edited]. Empty otherwise.
	Edited string `json:"edited,omitempty"`
	// Note is why, where they said. Optional for accept and edit; the screen asks for it on a
	// rejection, and the server does not require it — a physician who rejects nine drafts in
	// a morning and is made to justify each one will stop rejecting them.
	Note string `json:"note,omitempty"`

	DecidedBy   uuid.UUID `json:"decided_by"`
	DecidedRole string    `json:"decided_role,omitempty"`
	DecidedAt   time.Time `json:"decided_at"`
	// Generation is the synthesis run the decision was made against. A decision made on
	// generation 2 and shown beside generation 3's suggestion is a decision about a different
	// sentence, and the screen says so rather than presenting it as current.
	Generation int `json:"generation"`
}
