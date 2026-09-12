package medsafety

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Renal dosing (CP79, §7.2, §6.4).
//
// CP43 derives eGFR from a creatinine. CP77 wrote eight banded renal rules. CP78 built the engine
// that runs them. This file is the three things between them that were nobody's, and each one is
// a way the previous arrangement could be silently wrong:
//
//  1. **How old an eGFR may be.** `MET-RENAL-30` would have blocked metformin on a creatinine
//     from 2023 with exactly the confidence it blocks one from this morning. The plan's own
//     sentence: *an eGFR from two years ago is not current renal function.*
//  2. **Which drugs cannot be prescribed at all without one.** CP78 fails closed when a *rule*
//     meets an absent eGFR. A drug with no renal rule written about it produced silence, and
//     §7.2's whole position is that silence is the one answer that must never happen.
//  3. **What to call the number.** An eGFR of 41 is CKD stage G3b, and a physician reading "41"
//     and a physician reading "G3b" are making the same decision with different amounts of help.
//
// # The window is six months, nobody has approved it, and it is applied anyway
//
// This is the one deliberate departure from CP77's rule that seeded clinical content is inert
// until a physician reads it, and the reason is that **there is no inert value for a staleness
// window**. A rule that does not fire does not fire. A window that does not apply means every
// eGFR is treated as current — the seed would have made the system less safe by being unapproved
// rather than more.
//
// So [Policy.Approved] travels with every renal status this package produces, the indicator says
// *provisional* in both languages until somebody's name is on it, and the number itself is a row
// in `core.facility_renal_policy` that Dr. Nahid can change without a release. Per facility,
// because the plan's D-61 scoping rule is that a clinic's clinical thresholds are that clinic's.
//
// # Months, not days
//
// The window is stored and applied in **calendar months**, and the expiry is `as_of + N months`
// rather than `as_of + 30N days`. The difference shows up exactly at the boundary, which is
// where this would have been wrong for two years before anybody noticed: six calendar months
// before 12 September is 12 March, 184 days, so a 180-day window calls an eGFR taken exactly six
// months ago stale while every physician saying "within six months" means it is not.
//
// **Exactly at the boundary is current.** Stale means the instant of the check is *after* the
// expiry, so an eGFR at exactly six months is the last day it counts, and six months plus a
// second is the first it does not. Either convention is defensible; the one that is not
// defensible is having no convention, and `TestTheStalenessBoundaryIsExact` pins this one.
//
// # Dialysis is out of scope, deliberately
//
// Nothing here models dialysis, and nothing here should be read as handling it. A patient on
// haemodialysis has an eGFR that is not a dosing input: the number describes residual function,
// the dosing is driven by the schedule and by whether the drug is dialysed out, and applying a
// band to it produces a confident wrong answer where the honest output is no answer. The plan
// lists dialysis scope as an open decision for Dr. Nahid. Until he answers it there is no
// dialysis field, no dialysis band, and no half-built path to mistake for one.
//
// # No PHI
//
// An eGFR is a clinical fact about a person. Nothing in this file logs, traces or counts, for
// the same reason nothing else in this module does.

// ---------------------------------------------------------------------------
// The policy
// ---------------------------------------------------------------------------

// Policy is one facility's eGFR recency window.
type Policy struct {
	// RecencyMonths is how old an eGFR may be and still count as current renal function.
	RecencyMonths int `json:"recency_months"`
	// Approved is false while the seeded six-month proposal stands. The window applies either
	// way; this is what makes the indicator say so.
	Approved bool   `json:"approved"`
	Source   string `json:"source_citation"`
	NoteEN   string `json:"note_en,omitempty"`
	NoteBN   string `json:"note_bn,omitempty"`
}

// RenalPolicy reads one facility's window.
//
// No fallback. A facility with no row fails its own registered invariant
// (`assert_every_facility_has_a_renal_window`), and answering that with a default of six months
// would restore the constant the table exists to replace — and hide the broken invariant while
// doing it.
func (s *Store) RenalPolicy(ctx context.Context, facility uuid.UUID) (Policy, error) {
	row, err := s.q.FacilityRenalPolicy(ctx, facility)
	if err != nil {
		return Policy{}, err
	}
	return Policy{
		RecencyMonths: int(row.EgfrRecencyMonths),
		Approved:      row.ApprovedAt != nil,
		Source:        row.SourceCitation,
		NoteEN:        row.NotesEn,
		NoteBN:        row.NotesBn,
	}, nil
}

// Dependence is what is known about one molecule's relationship with kidney function.
type Dependence struct {
	// Required is true when this drug cannot be prescribed safely without a current eGFR.
	Required bool
	ReasonEN string
	ReasonBN string
	Source   string
	Approved bool
}

// RenalDependence reads the classification, keyed by lower-cased molecule name.
//
// **Molecules with no row are absent from the map**, which is the whole design: absence is
// "nobody has classified this", and it is reported as such rather than defaulted to either
// answer. Same three-state shape as CP78's coverage, for the same reason.
func (s *Store) RenalDependence(ctx context.Context) (map[string]Dependence, error) {
	rows, err := s.q.GenericRenalDependence(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Dependence, len(rows))
	for _, row := range rows {
		out[strings.ToLower(strings.TrimSpace(row.GenericName))] = Dependence{
			Required: row.Dependence == DependenceRequired,
			ReasonEN: row.ReasonEn, ReasonBN: row.ReasonBn,
			Source: row.SourceCitation, Approved: approvedFlag(row.IsApproved),
		}
	}
	return out, nil
}

// The two values `core.generic_renal_dependence.dependence` may hold.
const (
	// DependenceRequired — prescribing this molecule depends on knowing the kidney function,
	// whether because it is renally cleared, because its efficacy has an eGFR floor, or
	// because its risk profile changes with kidney function.
	DependenceRequired = "EGFR_REQUIRED"
	// DependenceNotRequired — prescribing it does not turn on the kidney function.
	DependenceNotRequired = "EGFR_NOT_REQUIRED"
)

// ---------------------------------------------------------------------------
// CKD staging
// ---------------------------------------------------------------------------

// Stage is a KDIGO GFR category.
type Stage string

const (
	StageG1      Stage = "G1"
	StageG2      Stage = "G2"
	StageG3a     Stage = "G3a"
	StageG3b     Stage = "G3b"
	StageG4      Stage = "G4"
	StageG5      Stage = "G5"
	StageUnknown Stage = ""
)

// StageOf is KDIGO 2024 table 1, and nothing else.
//
// **This is a GFR category, not a diagnosis of chronic kidney disease.** CKD needs the
// abnormality to have been present for more than three months and takes albuminuria into
// account; one eGFR of 52 is a G3a *number* and says nothing about whether this patient has CKD.
// [StageLabel] says so on the screen, because a staging display that reads "stage 3 kidney
// disease" off a single blood test is a diagnosis the software made up.
func StageOf(egfr float64) Stage {
	switch {
	case egfr >= 90:
		return StageG1
	case egfr >= 60:
		return StageG2
	case egfr >= 45:
		return StageG3a
	case egfr >= 30:
		return StageG3b
	case egfr >= 15:
		return StageG4
	default:
		return StageG5
	}
}

// StageLabel is what to draw beside the number, in both languages.
func StageLabel(s Stage) (string, string) {
	switch s {
	case StageG1:
		return "G1 — normal or high", "G1 — স্বাভাবিক বা বেশি"
	case StageG2:
		return "G2 — mildly reduced", "G2 — সামান্য কম"
	case StageG3a:
		return "G3a — mildly to moderately reduced", "G3a — সামান্য থেকে মাঝারি কম"
	case StageG3b:
		return "G3b — moderately to severely reduced", "G3b — মাঝারি থেকে বেশি কম"
	case StageG4:
		return "G4 — severely reduced", "G4 — অনেক কম"
	case StageG5:
		return "G5 — kidney failure", "G5 — কিডনি বিকল"
	default:
		return "", ""
	}
}

// ---------------------------------------------------------------------------
// The status
// ---------------------------------------------------------------------------

// RenalStatus is what the indicator draws and what every renal finding cites.
//
// **Criterion 1 is this struct**: the eGFR that was used, and the date it was taken, together,
// always — there is no shape of this type that carries a value without its date.
type RenalStatus struct {
	// Known is false when there is no eGFR on file at all.
	Known bool `json:"known"`

	// EGFR is the value used, in mL/min/1.73m². Nil when Known is false. **A zero eGFR is
	// anuric renal failure and is not nil**, which is why this is a pointer and not a float
	// with a sentinel.
	EGFR *float64 `json:"egfr,omitempty"`
	// AsOf is when the result it was derived from was effective. Criterion 1.
	AsOf *time.Time `json:"egfr_as_of,omitempty"`
	// AgeDays is how old it was at the instant of the check, for a screen that would rather
	// say "taken 43 days ago" than render a date the reader has to subtract.
	AgeDays *int `json:"age_days,omitempty"`

	// Stale is true when AsOf is further back than the facility's window. Criterion 2.
	Stale bool `json:"stale"`
	// ExpiresAt is the instant this result stops counting as current. Present whenever AsOf
	// is, including when it is already in the past.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`

	Stage        Stage  `json:"stage,omitempty"`
	StageLabelEN string `json:"stage_label_en,omitempty"`
	StageLabelBN string `json:"stage_label_bn,omitempty"`

	Policy Policy `json:"policy"`

	// SummaryEN and SummaryBN are the indicator's own sentence. Always present, and the
	// absent case says what is missing rather than rendering a blank.
	SummaryEN string `json:"summary_en"`
	SummaryBN string `json:"summary_bn"`
}

// resolveRenal turns an eGFR, its date and a facility's window into a status.
//
// Pure and clock-free: `at` is the caller's instant, like everything else in this engine, so a
// reproduced check reproduces the staleness decision too. A check re-run in 2030 against a 2026
// instant must say the eGFR was current, because it was.
func resolveRenal(egfr *float64, asOf *time.Time, policy Policy, at time.Time) RenalStatus {
	status := RenalStatus{Policy: policy}
	if egfr == nil {
		status.SummaryEN, status.SummaryBN = renalAbsentSentence()
		return status
	}
	value := *egfr
	status.Known = true
	status.EGFR = &value
	status.Stage = StageOf(value)
	status.StageLabelEN, status.StageLabelBN = StageLabel(status.Stage)

	if asOf == nil {
		// An eGFR with no date. CP78's bridge cannot produce one — `Renal` returns the value
		// and the instant together or neither — but a caller assembling a Picture by hand can,
		// and a value whose age is unknown must not be quietly treated as fresh.
		status.Stale = true
		status.SummaryEN, status.SummaryBN = renalUndatedSentence(value, status)
		return status
	}
	when := asOf.UTC()
	status.AsOf = &when

	age := int(at.UTC().Sub(when).Hours() / 24)
	status.AgeDays = &age

	// Calendar months, and "after" rather than "at or after": exactly at the window is the
	// last instant the result counts. See the file header.
	expires := when.AddDate(0, policy.RecencyMonths, 0)
	status.ExpiresAt = &expires
	status.Stale = at.UTC().After(expires)

	status.SummaryEN, status.SummaryBN = renalSentence(status, value, age)
	return status
}

// Renal resolves one patient's renal status without running a whole safety check.
//
// What the indicator on the prescription editor reads. Same resolution the engine uses — one
// function, so the number the screen shows and the number the rules ran against cannot disagree,
// which is the failure this would otherwise have: two implementations of "the latest eGFR", one
// of which forgets the window.
func (e *Engine) Renal(ctx context.Context, facts PatientFacts,
	facility, patient uuid.UUID, at time.Time) (RenalStatus, error) {

	policy, err := e.rules.RenalPolicy(ctx, facility)
	if err != nil {
		return RenalStatus{}, err
	}
	egfr, asOf, err := facts.Renal(ctx, facility, patient)
	if err != nil {
		return RenalStatus{}, err
	}
	return resolveRenal(egfr, asOf, policy, at), nil
}

// ---------------------------------------------------------------------------
// The findings
// ---------------------------------------------------------------------------

// The rule codes this file's findings cite. They are the engine's own, like
// COVERAGE-UNKNOWN-DRUG: no physician authored them and none can be withdrawn, because each is a
// statement about what the check could not do rather than a claim about a drug.
const (
	// CodeRenalStale — the eGFR used is older than the facility's window.
	CodeRenalStale = "RENAL-EGFR-STALE"
	// CodeRenalAbsent — a drug that needs a kidney function was proposed and there is none.
	// **Criterion 3.**
	CodeRenalAbsent = "RENAL-EGFR-ABSENT"
	// CodeRenalUnclassified — nobody has said whether this molecule needs a kidney function.
	CodeRenalUnclassified = "RENAL-NOT-CLASSIFIED"
)

// renalFindings is everything CP79 adds to a check, given the resolved status and the drugs.
//
// Ordered by how much it matters: the absent eGFR on a drug that needs one, then the stale one,
// then the unclassified molecules. Each is per drug where the drug is what makes it true, and
// once where it is not: a stale eGFR is a fact about the patient, and twelve copies of it down
// the finding list would bury the eleven other things the physician has to read.
func renalFindings(status RenalStatus, proposed []Drug, byGeneric map[string]Dependence) []Finding {
	var out []Finding

	needs := func(d Drug) (Dependence, bool) {
		dep, ok := byGeneric[strings.ToLower(strings.TrimSpace(d.Generic))]
		return dep, ok
	}

	// ---- 1. No eGFR at all, for a drug that needs one. Criterion 3. ------
	//
	// CANNOT_VERIFY at BLOCK severity. The severity is the judgement in this function and it
	// is the plan's: *fail closed when creatinine is absent for a renally-cleared drug*. Fail
	// closed means the prescription does not proceed as though it had been checked, and a WARN
	// among warnings is not that. It is CANNOT_VERIFY rather than FIRES because nothing is
	// known to be wrong — what is known is that nobody can tell.
	if !status.Known {
		for _, drug := range proposed {
			dep, classified := needs(drug)
			if !classified || !dep.Required {
				continue
			}
			out = append(out, Finding{
				RuleCode: CodeRenalAbsent, Type: TypeRenal, Severity: SeverityBlock,
				Outcome: OutcomeCannotVerify, SubjectRef: drug.Ref, Subject: drug.Label,
				MessageEN: "There is no eGFR on file for this patient, and " + drug.Label +
					" cannot be dosed safely without one. " + dep.ReasonEN,
				MessageBN: "এই রোগীর কোনো eGFR নথিতে নেই, আর " + drug.Label +
					" কিডনির কার্যকারিতা না জেনে নিরাপদে দেওয়া যায় না। " + dep.ReasonBN,
				AdviceEN: "Order a serum creatinine. The eGFR is derived from it automatically.",
				AdviceBN: "সিরাম ক্রিয়েটিনিন দিন। তা থেকে eGFR আপনাআপনি বেরিয়ে আসবে।",
				Source:   dep.Source,
				Missing:  []Datum{DatumEGFR},
			})
		}
	}

	// ---- 2. An eGFR that is too old to be current. Criterion 2. ----------
	//
	// FIRES rather than CANNOT_VERIFY, and WARN rather than BLOCK. A stale eGFR is a known
	// fact, not an absence: the rules did run, against a number that was true in March. What
	// the physician has to decide is whether it is still true, and that is a warning.
	//
	// Raised once, whatever is on the prescription, including when nothing needs it — a
	// physician looking at a two-year-old eGFR should be told so before deciding what to
	// prescribe, not only after proposing something renally dosed.
	if status.Known && status.Stale {
		out = append(out, renalStaleFinding(status))
	}

	// ---- 3. Nobody has classified this molecule -------------------------
	//
	// Only when there is no eGFR. With one on file the banded rules run and the coverage model
	// already reports what was and was not checked; adding a second uncovered-style finding
	// there would be noise on every ordinary prescription. Without one, the honest statement is
	// that this drug might need a kidney function and nobody has said.
	if !status.Known {
		for _, drug := range proposed {
			if _, classified := needs(drug); classified {
				continue
			}
			if strings.TrimSpace(drug.Generic) == "" {
				// An unidentifiable drug is already CP78's COVERAGE-UNKNOWN-DRUG finding.
				// Saying it again here in different words would make one problem look like
				// two.
				continue
			}
			out = append(out, Finding{
				RuleCode: CodeRenalUnclassified, Type: TypeRenal, Severity: SeverityWarn,
				Outcome: OutcomeCannotVerify, SubjectRef: drug.Ref, Subject: drug.Label,
				MessageEN: "Nobody has recorded whether " + drug.Label +
					" needs a kidney function before it is prescribed, and there is no eGFR " +
					"on file. This has not been checked against renal dosing at all.",
				MessageBN: "ব্যবস্থাপত্র দেওয়ার আগে " + drug.Label +
					"-এর জন্য কিডনির কার্যকারিতা জানা দরকার কি না, তা কেউ লিখে রাখেনি, আর নথিতে " +
					"কোনো eGFR-ও নেই। কিডনি অনুযায়ী মাত্রার দিক থেকে এটি একেবারেই যাচাই হয়নি।",
				AdviceEN: "Check the label by hand, and classify this molecule so the next " +
					"prescription does not need the same check.",
				AdviceBN: "ওষুধের নির্দেশিকা নিজে দেখুন, এবং এই ওষুধটির শ্রেণিবিন্যাস করে রাখুন " +
					"যাতে পরেরবার একই কাজ আবার করতে না হয়।",
				Source:  "CP79: core.generic_renal_dependence holds no row for this molecule.",
				Missing: []Datum{DatumEGFR},
			})
		}
	}
	return out
}

func renalStaleFinding(status RenalStatus) Finding {
	age := 0
	if status.AgeDays != nil {
		age = *status.AgeDays
	}
	months := itoa(status.Policy.RecencyMonths)
	on := ""
	if status.AsOf != nil {
		on = status.AsOf.Format("2 January 2006")
	}
	provisional := ""
	provisionalBN := ""
	if !status.Policy.Approved {
		provisional = " (the " + months + "-month window is a proposal no physician has approved.)"
		provisionalBN = " (" + months + " মাসের এই সীমাটি একটি প্রস্তাব, কোনো চিকিৎসক এখনও অনুমোদন করেননি।)"
	}
	return Finding{
		RuleCode: CodeRenalStale, Type: TypeRenal, Severity: SeverityWarn,
		Outcome: OutcomeFires,
		MessageEN: "The eGFR used here was taken on " + on + ", " + itoa(age) +
			" days ago, which is older than this clinic's " + months +
			"-month window. Every renal rule below ran against a kidney function that may " +
			"no longer be the patient's." + provisional,
		MessageBN: "এখানে ব্যবহৃত eGFR নেওয়া হয়েছিল " + on + " তারিখে, " + itoa(age) +
			" দিন আগে — যা এই ক্লিনিকের " + months + " মাসের সীমার চেয়ে পুরনো। নিচের প্রতিটি " +
			"কিডনি-নিয়ম এমন একটি কার্যকারিতার বিপরীতে চলেছে যা এখন রোগীর নাও হতে পারে।" + provisionalBN,
		AdviceEN: "Order a serum creatinine before relying on the renal findings below.",
		AdviceBN: "নিচের কিডনি-সংক্রান্ত ফলাফলের উপর ভরসা করার আগে সিরাম ক্রিয়েটিনিন দিন।",
		Source:   status.Policy.Source,
	}
}

// ---------------------------------------------------------------------------
// The sentences
// ---------------------------------------------------------------------------

func renalAbsentSentence() (string, string) {
	return "No eGFR is on file for this patient. Nothing on this prescription has been checked " +
			"against kidney function.",
		"এই রোগীর কোনো eGFR নথিতে নেই। এই ব্যবস্থাপত্রের কিছুই কিডনির কার্যকারিতার সঙ্গে মিলিয়ে দেখা হয়নি।"
}

func renalUndatedSentence(value float64, status RenalStatus) (string, string) {
	return "An eGFR of " + number(value) + " (" + status.StageLabelEN +
			") is on file with no date against it, so it cannot be treated as current.",
		"নথিতে " + number(value) + " eGFR (" + status.StageLabelBN +
			") আছে, কিন্তু তার কোনো তারিখ নেই, তাই একে বর্তমান ধরা যাচ্ছে না।"
}

func renalSentence(status RenalStatus, value float64, age int) (string, string) {
	on := status.AsOf.Format("2 January 2006")
	head := "eGFR " + number(value) + " mL/min/1.73m², " + status.StageLabelEN +
		", taken on " + on + " (" + itoa(age) + " days ago)."
	headBN := "eGFR " + number(value) + " mL/min/1.73m², " + status.StageLabelBN +
		", নেওয়া হয়েছে " + on + " তারিখে (" + itoa(age) + " দিন আগে)।"

	if status.Stale {
		return head + " This is older than the clinic's " + itoa(status.Policy.RecencyMonths) +
				"-month window and is not current renal function.",
			headBN + " এটি ক্লিনিকের " + itoa(status.Policy.RecencyMonths) +
				" মাসের সীমার চেয়ে পুরনো, তাই একে বর্তমান কিডনি-কার্যকারিতা ধরা যায় না।"
	}
	return head + " Within the clinic's " + itoa(status.Policy.RecencyMonths) + "-month window.",
		headBN + " ক্লিনিকের " + itoa(status.Policy.RecencyMonths) + " মাসের সীমার মধ্যে।"
}

// number formats an eGFR the way a laboratory report does: whole numbers whole, one decimal
// otherwise. A derived value of 41.379310344827587 rendered in full is a number nobody reads.
//
// The tenth is taken from the scaled integer rather than from `(rounded - whole) * 10`, which is
// the obvious version and is wrong: 41.4 is not representable, the subtraction yields
// 0.39999999999999858, and truncating that prints "41.3". The first draft of this function did
// exactly that and `TestTheRenalStatusAlwaysCarriesTheDateAndASentence` caught it.
func number(v float64) string {
	negative := v < 0
	if negative {
		v = -v
	}
	scaled := int64(v*10 + 0.5)
	whole, tenth := scaled/10, scaled%10
	out := itoa(int(whole))
	if tenth != 0 {
		out += "." + itoa(int(tenth))
	}
	if negative {
		return "-" + out
	}
	return out
}

// approvedFlag reads sqlc's `interface{}` for a computed boolean column.
//
// `d.approved_at IS NOT NULL AS is_approved` is a boolean to PostgreSQL and an `any` to the
// generator, which cannot infer a type for an expression. Asserted here rather than restructured
// into a nullable timestamp the caller re-tests, because the question the map answers is a
// boolean and a caller that had to derive it is a caller that will derive it differently.
func approvedFlag(v any) bool {
	b, ok := v.(bool)
	return ok && b
}
