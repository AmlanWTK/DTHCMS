// Package qa is station 10: the last point at which this clinic can notice that a file is
// incomplete **while the patient is still in the building** (CP83, §3 step 10, docs/qa-rules.md).
//
// # One sentence everything here follows from
//
// **Clearance is required; what clearance looks at is not.**
//
// Those two halves pull in opposite directions and both of them are load-bearing. The rules are
// rows — severity, bounce station, recency window, enabled, parameters — so that Dr Nahid
// retires rule 7 or widens rule 4's window without a release. The *requirement* is a trigger on
// `read.prescription` that never reads the rule table at all. So an empty `core.qa_rule` means
// every prescription clears after somebody looked at it, and it cannot be made to mean that
// nobody has to look. `docs/qa-rules.md` §5 says only one of those is safe, and it is right.
//
// The enforcement is in the database for CP57's reason, restated because it is the reason this
// package is shaped the way it is: the honest reading of "enforced server-side" is the path
// nobody remembers — the support script, the second client, the integration written after
// everybody who read the plan has left. This package produces the *findings*, because criterion 3
// asks for a bounce with a specific reason to a named station and a trigger's exception text is
// not a screen. The trigger is what holds for every path that does not come through here.
//
// # Why the rules are shapes rather than functions
//
// Eighteen rules; thirteen shapes. `OBSERVATION_RECENT` answers rules 4, 8, 9, 11 and 13 — "is
// there a recent enough value for one of these codes, for a patient who matches this trigger" —
// and the five differ only in their codes, their window and their trigger, which are columns.
//
// That is where criterion 5's line falls, and it is worth stating precisely because it is the
// criterion most easily faked:
//
//   - **A new rule of an existing shape is a row.** "No urine ACR in twelve months for a
//     diabetic, bouncing to consultation, WARN" is an INSERT. Nothing recompiles.
//   - **A genuinely new question is a release.** "Block if the patient has not paid" is a new
//     shape, needs a new predicate, and `core.qa_rule_kind` is read-only to the application so
//     that a row naming a shape nothing implements cannot exist. A rule that is present,
//     enabled and permanently silent is worse than no rule, because the screen shows it.
//
// # What this package consumes and does not reimplement
//
// Rules 1 and 3 are CP78's engine. Rule 6 is CP79's renal window, which is the facility's number
// and not a constant. Rules 12 and 16 are CP55–57's counselling ticks. Rule 10 is CP92's
// education records. None of them is re-derived here: this package asks those modules and turns
// their answers into findings. The one thing it had to create is the *order* — "no HbA1c
// recorded **or ordered**" — because no checkpoint in the plan owns lab ordering and rule 4
// without its second half blocks the consultant who did the right thing. That lives in
// `clinical`, which owns observations, and not here.
//
// # No PHI leaves this package
//
// Nothing here logs, traces or counts a drug, a diagnosis, a name or a number. A finding carries
// clinical detail because a finding is shown to the officer with the patient in front of them;
// it goes into the ledger and the read model, where a patient's record belongs, and into nothing
// else.
package qa

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Severity
// ---------------------------------------------------------------------------

// Severity is `docs/qa-rules.md` §2, and the difference between its two values is the patient's
// feet.
type Severity string

const (
	// SeverityBlock — the file is not safe to close. Cannot clear; bounces to a named station.
	//
	// A BLOCK sends an elderly patient who travelled from Boalmari back up the corridor for
	// another forty minutes. That cost is real, which is why the spec reserves BLOCK for
	// things where the alternative is worse — and why it is a column rather than a constant.
	SeverityBlock Severity = "BLOCK"
	// SeverityWarn — the file is incomplete and the consultant may still have a reason. Clears,
	// but the officer must acknowledge it and the acknowledgement is recorded.
	SeverityWarn Severity = "WARN"
)

// Blocks reports whether a finding at this severity stops a clearance.
func (s Severity) Blocks() bool { return s == SeverityBlock }

// ---------------------------------------------------------------------------
// Kinds
// ---------------------------------------------------------------------------

// Kind is one question shape, and every one of them is a predicate in rules.go.
//
// The catalogue is `core.qa_rule_kind`, which is read-only to the application. These constants
// and those rows are held together by `TestEveryKindInTheDatabaseHasAPredicate`: a kind in the
// database that Go does not implement is a rule that can never fire, and a kind in Go that the
// database does not know is a predicate no row can reach. Both are silent, and silence is the
// failure mode this whole station exists to prevent.
type Kind string

const (
	// KindSafetyEngineFinding — rules 1 and 3. CP78's engine, asked again at the last station,
	// because a hard stop that was overridden upstream must not pass silently downstream.
	KindSafetyEngineFinding Kind = "SAFETY_ENGINE_FINDING"
	// KindAllergyStatusAsserted — rule 2. "No known allergy" is an answer; silence is not.
	KindAllergyStatusAsserted Kind = "ALLERGY_STATUS_ASSERTED"
	// KindObservationRecent — rules 4, 8, 9, 11, 13. The workhorse.
	KindObservationRecent Kind = "OBSERVATION_RECENT"
	// KindObservationThisVisit — rule 5. Not "recently": today, at a station the patient
	// already walked past.
	KindObservationThisVisit Kind = "OBSERVATION_THIS_VISIT"
	// KindRenalWindow — rule 6. CP79's window, which is the facility's number.
	KindRenalWindow Kind = "RENAL_WINDOW"
	// KindEducationThisVisit — rule 10. The link between stations 11 and 10.
	KindEducationThisVisit Kind = "EDUCATION_THIS_VISIT"
	// KindCounselingItemCovered — rule 12. One named item, not the whole checklist.
	KindCounselingItemCovered Kind = "COUNSELING_ITEM_COVERED"
	// KindCounselingComplete — rule 16. CP57's gate, asked once more.
	KindCounselingComplete Kind = "COUNSELING_COMPLETE"
	// KindTeratogenPregnancyStatus — rule 14. Inert until somebody flags a molecule.
	KindTeratogenPregnancyStatus Kind = "TERATOGEN_PREGNANCY_STATUS"
	// KindDiagnosisCodedThisVisit — rule 15. An uncoded visit is a visit no report can see.
	KindDiagnosisCodedThisVisit Kind = "DIAGNOSIS_CODED_THIS_VISIT"
	// KindMandatoryStationData — rule 17. The route's own mandatory stations.
	KindMandatoryStationData Kind = "MANDATORY_STATION_DATA"
	// KindWeightForWeightBasedDose — rule 18.
	KindWeightForWeightBasedDose Kind = "WEIGHT_FOR_WEIGHT_BASED_DOSE"
)

// AllKinds is every shape Go implements, in the order rules.go defines them.
var AllKinds = []Kind{
	KindSafetyEngineFinding, KindAllergyStatusAsserted, KindObservationRecent,
	KindObservationThisVisit, KindRenalWindow, KindEducationThisVisit,
	KindCounselingItemCovered, KindCounselingComplete, KindTeratogenPregnancyStatus,
	KindDiagnosisCodedThisVisit, KindMandatoryStationData, KindWeightForWeightBasedDose,
}

// ---------------------------------------------------------------------------
// A rule
// ---------------------------------------------------------------------------

// Params is everything a shape reads out of `core.qa_rule.params`.
//
// One struct for every shape rather than one per shape, because the alternative is a jsonb column
// decoded twelve different ways and a rule row whose meaning depends on which branch reads it.
// A key a shape does not use is inert, and [Rule.Validate] says so rather than letting it look
// configured — the same defect `uses_window` guards against in the database, one level up.
type Params struct {
	// Codes are observation codes. **Any one of them satisfies the rule**, not all: a lipid
	// profile is present if any of LDL, total, HDL or triglyceride is, because a lab reports
	// what it reports.
	Codes []string `json:"codes,omitempty"`
	// AcceptOrdered makes an order count as satisfying the rule. Rule 4's "or ordered": a
	// consultant who has ordered the test has done the right thing, and blocking him for the
	// lab's turnaround would be blocking the wrong person.
	AcceptOrdered bool `json:"accept_ordered,omitempty"`

	// WhenDiagnosis restricts the rule to patients carrying one of these diagnosis codes, by
	// prefix — "E11" matches E11.9 and E11.22. Empty means every patient.
	WhenDiagnosis []string `json:"when_diagnosis,omitempty"`
	// WhenGenerics restricts it to prescriptions carrying one of these molecules.
	WhenGenerics []string `json:"when_generics,omitempty"`
	// WhenClasses restricts it to prescriptions carrying a molecule in one of these classes.
	WhenClasses []string `json:"when_classes,omitempty"`

	// ItemCodes are counselling items, for KindCounselingItemCovered.
	ItemCodes []string `json:"item_codes,omitempty"`

	// MinSeverity and RuleTypes filter CP78's findings for KindSafetyEngineFinding.
	MinSeverity string   `json:"min_severity,omitempty"`
	RuleTypes   []string `json:"rule_types,omitempty"`

	// AgeMin and AgeMax are rule 14's band. Inclusive at both ends.
	AgeMin *int `json:"age_min,omitempty"`
	AgeMax *int `json:"age_max,omitempty"`
}

// Rule is one row of `core.qa_rule`.
type Rule struct {
	ID         uuid.UUID `json:"id"`
	FacilityID uuid.UUID `json:"-"`

	Code string   `json:"code"`
	Kind Kind     `json:"kind"`
	Sev  Severity `json:"severity"`

	// BounceStation is the room the patient walks back to. Always present on a BLOCK — the
	// database's `qa_rule_block_names_its_station` constraint — because "incomplete" without
	// "whose" is a rule that stalls.
	BounceStation string `json:"bounce_station_code,omitempty"`

	// WindowDays is how recent is recent enough, for the shapes that ask. Nil for the rest, and
	// the database refuses the two mismatches in either direction.
	WindowDays *int `json:"window_days,omitempty"`

	Params  Params `json:"params"`
	Enabled bool   `json:"enabled"`

	TitleEN  string `json:"title_en"`
	TitleBN  string `json:"title_bn"`
	DetailEN string `json:"detail_en,omitempty"`
	DetailBN string `json:"detail_bn,omitempty"`

	// LooksForEN and LooksForBN are the one clinical thing this rule is hunting for, as a bare
	// noun phrase with no article — "lipid profile", "foot sensation test". The predicate builds
	// the sentence around it, which is why the article is not here: three shapes build three
	// different sentences from the same noun, and a row carrying "a lipid profile" would render
	// "no a lipid profile".
	//
	// Empty is legal and common: a rule naming one observation code lets
	// `core.observation_code` answer, because a second copy of "HbA1c" on this row is a second
	// copy to drift. The database refuses it empty for a rule naming several codes or any
	// counselling item — see `qa_rule_says_what_it_looks_for` — because there the codes are
	// parts of one thing and only the rule knows what that thing is called.
	LooksForEN string `json:"looks_for_en,omitempty"`
	LooksForBN string `json:"looks_for_bn,omitempty"`

	Ordering  int        `json:"ordering"`
	RetiredAt *time.Time `json:"retired_at,omitempty"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// Live reports whether this rule is asked at all.
func (r Rule) Live() bool { return r.Enabled && r.RetiredAt == nil }

// ---------------------------------------------------------------------------
// A finding
// ---------------------------------------------------------------------------

// Finding is one thing this station noticed, in the words the officer reads.
//
// Bilingual throughout, and not by translating on the way out: the rule row carries both
// languages and so does every sentence this package composes, because a finding that appears in
// English for an officer who works in Bangla has not appeared.
type Finding struct {
	RuleCode string   `json:"rule_code"`
	Sev      Severity `json:"severity"`

	TitleEN string `json:"title_en"`
	TitleBN string `json:"title_bn"`
	// Detail is the rule's standing advice. What to do about it.
	DetailEN string `json:"detail_en,omitempty"`
	DetailBN string `json:"detail_bn,omitempty"`

	// Subject is what specifically triggered it on *this* sheet — the drug, the station, the
	// observation code. A finding that said only "a renally-dosed drug with no eGFR" would send
	// the physician looking through eight lines for the one that matters.
	SubjectEN string `json:"subject_en,omitempty"`
	SubjectBN string `json:"subject_bn,omitempty"`

	// SubjectCodes are the internal handles behind the sentence — `CHOL_LDL`, `AGRANULOCYTOSIS_
	// WARNING`. **Never the primary text**, which is the defect this field exists alongside: a
	// QA officer reads the sentence, and somebody debugging a rule at eight in the evening needs
	// the code. The client renders them as a detail — a title attribute — so that throwing the
	// code away does not trade one person's problem for another's.
	SubjectCodes []string `json:"subject_codes,omitempty"`

	BounceStation string `json:"bounce_station_code,omitempty"`
	StationEN     string `json:"bounce_station_en,omitempty"`
	StationBN     string `json:"bounce_station_bn,omitempty"`
}

// ReasonEN is the sentence a bounce carries: what is wrong, and about what.
func (f Finding) ReasonEN() string {
	if f.SubjectEN == "" {
		return f.TitleEN
	}
	return f.TitleEN + " — " + f.SubjectEN
}

// ReasonBN is the same sentence in clinical Bengali.
func (f Finding) ReasonBN() string {
	if f.SubjectBN == "" {
		return f.TitleBN
	}
	return f.TitleBN + " — " + f.SubjectBN
}

// ---------------------------------------------------------------------------
// A review
// ---------------------------------------------------------------------------

// Review is what the station says about one prescription, before anybody decides anything.
type Review struct {
	PrescriptionID uuid.UUID `json:"prescription_id"`
	PatientID      uuid.UUID `json:"patient_id"`
	VisitID        uuid.UUID `json:"visit_id"`

	// At is the instant the rules were read and the facts were taken. A decision recorded
	// against this review is a decision about the file as it stood then.
	At time.Time `json:"at"`

	// Findings is everything that fired, blocks first, then warnings, each group in the rule
	// table's own order so that the same file renders the same way twice.
	Findings []Finding `json:"findings"`

	// RulesLive is how many rules were asked. **Zero is a legitimate answer** and it is the one
	// §5 is about: it means this clinic's checklist is empty, not that clearance is optional.
	// A screen showing zero says so in those words.
	RulesLive int `json:"rules_live"`

	// Stations is the facility's station catalogue, code to its English and Bengali names.
	//
	// Carried on the review because a bounce may go somewhere no finding named: an officer who
	// decides the real problem is the history station sends the patient there, and the slip has
	// to print "রোগের ইতিহাস" rather than STN_HISTORY. A client that had only the findings'
	// stations would render the code for exactly the bounces a person chose themselves.
	Stations map[string][2]string `json:"stations,omitempty"`

	// Override is the consultant override standing on this prescription, if one does.
	Override *Override `json:"override,omitempty"`

	// Decided is the decision already recorded against this prescription since it was last
	// bounced, if there is one. A second reviewer opening the screen sees what the first did.
	Decided *Decision `json:"decision,omitempty"`
}

// Blocking is every finding that stops a clearance.
func (r Review) Blocking() []Finding {
	out := []Finding{}
	for _, f := range r.Findings {
		if f.Sev.Blocks() {
			out = append(out, f)
		}
	}
	return out
}

// Warnings is every finding that clears with an acknowledgement.
func (r Review) Warnings() []Finding {
	out := []Finding{}
	for _, f := range r.Findings {
		if !f.Sev.Blocks() {
			out = append(out, f)
		}
	}
	return out
}

// CanClear reports whether a clearance would be accepted right now.
//
// **False for a file with blocking findings and no override, and true for a file with none —
// including a file the empty rule table found nothing on.** That second case is the one worth
// naming: `CanClear` true is not "clearance is unnecessary", it is "clearance would be accepted",
// and the clearance still has to be recorded by somebody before the prescription can be signed.
func (r Review) CanClear() bool { return len(r.Blocking()) == 0 || r.Override != nil }

// ---------------------------------------------------------------------------
// The decisions
// ---------------------------------------------------------------------------

// Outcome is what the officer decided.
type Outcome string

const (
	// OutcomeCleared — the file may be signed and printed.
	OutcomeCleared Outcome = "CLEARED"
	// OutcomeBounced — the patient walks back to a named station.
	OutcomeBounced Outcome = "BOUNCED"
)

// Decision is one recorded QA outcome, as `read.qa_review` holds it.
type Decision struct {
	ID             uuid.UUID `json:"id"`
	PrescriptionID uuid.UUID `json:"prescription_id"`
	PatientID      uuid.UUID `json:"patient_id"`
	VisitID        uuid.UUID `json:"visit_id"`

	Outcome Outcome `json:"outcome"`

	DecidedAt   time.Time `json:"decided_at"`
	DecidedBy   uuid.UUID `json:"decided_by"`
	DecidedRole string    `json:"decided_role,omitempty"`
	// The person, named. "Who cleared this" is a question about a colleague, and a uuid answers
	// a different one.
	DecidedByCode   string `json:"decided_by_code,omitempty"`
	DecidedByNameEN string `json:"decided_by_name_en,omitempty"`
	DecidedByNameBN string `json:"decided_by_name_bn,omitempty"`

	BounceStation string `json:"bounce_station_code,omitempty"`
	StationEN     string `json:"bounce_station_en,omitempty"`
	StationBN     string `json:"bounce_station_bn,omitempty"`
	ReasonEN      string `json:"reason_en,omitempty"`
	ReasonBN      string `json:"reason_bn,omitempty"`

	// Findings as they stood at the decision, by value. Recomputing them later answers a
	// different question: an HbA1c ordered an hour afterwards would make a recomputed list say
	// the bounce was for nothing.
	Findings []Finding `json:"findings"`
	// Acknowledged is the WARN rule codes the officer accepted. §2's "the acknowledgement is
	// recorded", and it is recorded as codes rather than as a boolean, because "which ones did
	// they wave through" is the question somebody asks later.
	Acknowledged []string `json:"acknowledged"`

	OverrideID *uuid.UUID `json:"override_id,omitempty"`
}

// Override is the valve, once used.
//
// # Why it exists at all, and why it is the consultant's
//
// CP57's argument, and it transfers exactly. A gate people route around is worse than a gate with
// a recorded valve, because the routing-around is invisible. The difference here is who holds it:
// `docs/qa-rules.md` §2 puts the override at consultant level and the *rate* with Quality,
// because the answer to a rising override rate is a person asking why, and that person should not
// be the one granting them. So `qa.override` is held by PHYSICIAN and ADMIN and not by QA, and
// the rate route is behind `qa.review`, which QA holds and the physician does not.
type Override struct {
	ID             uuid.UUID `json:"id"`
	PrescriptionID uuid.UUID `json:"prescription_id"`
	PatientID      uuid.UUID `json:"patient_id"`
	VisitID        uuid.UUID `json:"visit_id"`

	GrantedAt       time.Time `json:"granted_at"`
	GrantedBy       uuid.UUID `json:"granted_by"`
	GrantedRole     string    `json:"granted_role,omitempty"`
	GrantedByCode   string    `json:"granted_by_code,omitempty"`
	GrantedByNameEN string    `json:"granted_by_name_en,omitempty"`
	GrantedByNameBN string    `json:"granted_by_name_bn,omitempty"`

	Reason string `json:"reason"`
	// BlockingAtGrant is what was blocking when it was granted, not what is blocking now.
	// Findings satisfied afterwards would make the record say the override was for nothing.
	BlockingAtGrant []string `json:"blocking_at_grant"`
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

var (
	// ErrNotFound — no such prescription in this facility. Indistinguishable, deliberately,
	// from one the caller may not see: a 403 that told the difference would be an existence
	// oracle.
	ErrNotFound = errors.New("qa: prescription not found")

	// ErrNotUnderReview — a decision on a prescription that is not with QA. A draft has not
	// been submitted and a signed one is past this station.
	ErrNotUnderReview = errors.New("qa: this prescription is not with QA")

	// ErrBlocked — a clearance asked for on a file with blocking findings and no override.
	// **This is the check the whole checkpoint is about**, and it is refused here so the officer
	// gets a sentence; the trigger refuses it again for every path that is not this one.
	ErrBlocked = errors.New("qa: this file has blocking findings and cannot be cleared")

	// ErrWarningsNotAcknowledged — a clearance that did not acknowledge every warning. §2 makes
	// the acknowledgement the thing that distinguishes a WARN from nothing at all.
	ErrWarningsNotAcknowledged = errors.New("qa: every warning has to be acknowledged")

	// ErrBounceNeedsAStation — a bounce with no room to bounce to.
	ErrBounceNeedsAStation = errors.New("qa: a bounce names the station the patient goes back to")

	// ErrBounceNeedsAReason — criterion 3's "with a specific reason".
	ErrBounceNeedsAReason = errors.New("qa: a bounce says what is wrong")

	// ErrOverrideReasonRequired — criterion 4, refused before it reaches the ledger.
	ErrOverrideReasonRequired = errors.New("qa: an override needs a reason")

	// ErrNothingToOverride — an override on a file nothing is blocking. Refused rather than
	// recorded: an override for no reason is a row that makes the rate view lie.
	ErrNothingToOverride = errors.New("qa: nothing is blocking this prescription")

	// ErrAlreadyOverridden — a second override on the same prescription. Not another act.
	ErrAlreadyOverridden = errors.New("qa: this prescription has already been overridden")

	// ErrUnknownKind — a rule row naming a shape this build does not implement. Unreachable
	// through the foreign key on `core.qa_rule_kind`, and refused here anyway, because the
	// alternative is a rule that is enabled and permanently silent.
	ErrUnknownKind = errors.New("qa: this rule names a question shape this build cannot ask")

	// ErrRuleInvalid — a rule row whose parameters do not describe something askable.
	ErrRuleInvalid = errors.New("qa: this rule does not describe something that can be checked")
)
