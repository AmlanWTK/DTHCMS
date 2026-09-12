package medsafety

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Evaluating one rule against one clinical picture (CP77, and the seam CP78 builds on).
//
// # Why this is here at all, when CP78 owns the engine
//
// Because the sandbox cannot exist without it. "Author a rule, run it against a test patient,
// see the result before publishing" is acceptance criterion 4, and the only honest way to show a
// physician what a rule will do is to do it. Shipping a second, simpler evaluator for the
// sandbox and a real one for CP78 would mean the thing he tested and the thing that fires are
// different programs — which is the defect this checkpoint's whole verification bar is about.
//
// So the per-rule semantics are here, they are what the sandbox runs, and they are what CP78
// will call. What CP78 adds is above this line: assembling a [Context] from a real patient,
// running a whole [Ruleset], expanding allergies through the cross-reactivity map, detecting
// duplicates across items, reporting which drugs no rule covered, and recording the run.
//
// # Three-valued, and the third value is the point
//
// A predicate answers [Holds], [DoesNotHold] or [CannotTell]. The third is what an absent
// creatinine produces, and it is what makes §7.2's fail-closed behaviour possible: a renal rule
// that met no eGFR must say *"cannot verify"*, never nothing. Silence and "safe" are the same
// pixel on a screen, and only one of them is true.
//
// Over an AND the combination is simple, which is the whole reason the model has no OR:
//
//	any DoesNotHold          → the rule does not fire
//	otherwise any CannotTell → CannotVerify
//	otherwise                → Fires
//
// Note the order. One predicate that definitely does not hold settles the rule even if another
// is unknown — "metformin at eGFR 80" is not a rule that cannot be verified, it is a rule that
// does not apply. Getting that backwards produces a screen of "cannot verify" on every
// prescription, which is how fail-closed turns into noise and then into nobody reading it.

// Truth is a predicate's answer.
type Truth int

const (
	// DoesNotHold — the data say no.
	DoesNotHold Truth = iota
	// Holds — the data say yes.
	Holds
	// CannotTell — the datum this test needs is absent.
	CannotTell
)

// Outcome is a rule's answer.
type Outcome string

const (
	// OutcomeNotApplicable — none of the prescribed drugs is what this rule is about.
	OutcomeNotApplicable Outcome = "NOT_APPLICABLE"
	// OutcomeFires — the rule's conditions are met. This is a finding.
	OutcomeFires Outcome = "FIRES"
	// OutcomeDoesNotFire — the rule applies to a prescribed drug and its conditions are not met.
	OutcomeDoesNotFire Outcome = "DOES_NOT_FIRE"
	// OutcomeCannotVerify — the rule applies and the data needed to decide are missing. §7.2's
	// fail-closed state, and never to be rendered as though it were OutcomeDoesNotFire.
	OutcomeCannotVerify Outcome = "CANNOT_VERIFY"
)

// Datum names a piece of clinical information a rule needed.
//
// Returned on a CANNOT_VERIFY so that the screen can say *which* thing is missing, and so that
// CP81 can offer to go and get it. "Safety cannot be verified" with no noun in it is a message
// people learn to click past.
type Datum string

const (
	DatumEGFR        Datum = "EGFR"
	DatumAge         Datum = "AGE"
	DatumPregnancy   Datum = "PREGNANCY"
	DatumHepatic     Datum = "HEPATIC"
	DatumDiagnoses   Datum = "DIAGNOSES"
	DatumAllergies   Datum = "ALLERGIES"
	DatumDailyDose   Datum = "DAILY_DOSE"
	DatumCurrentMeds Datum = "CURRENT_MEDICATIONS"
)

// Needs says what datum a predicate reads. The table CP78's fail-closed logic is built on, and
// the table the authoring form uses to warn an author what his rule will not be able to answer.
func (p Predicate) Needs() Datum {
	switch p.Kind {
	case PredEGFR:
		return DatumEGFR
	case PredAge:
		return DatumAge
	case PredPregnancy:
		return DatumPregnancy
	case PredHepatic:
		return DatumHepatic
	case PredDiagnosis:
		return DatumDiagnoses
	case PredAllergy:
		return DatumAllergies
	case PredDailyDose:
		return DatumDailyDose
	case PredCoPrescribed:
		if p.CurrentMedications {
			return DatumCurrentMeds
		}
	}
	return ""
}

// Drug is one medicine in a clinical picture.
type Drug struct {
	// Ref is the caller's own handle for this line — a prescription item id, or an index the
	// editor generates. Carried so a finding and a coverage entry can point at the line the
	// physician is looking at rather than at a name two lines might share, and so that two
	// lines of the identical medicine are two items rather than one seen twice.
	//
	// Optional: the sandbox's hand-typed pictures have none, and items with no ref fall back
	// to label-and-generic.
	Ref string `json:"ref,omitempty"`
	// Label is what to call it in a message — the trade name the physician typed.
	Label string `json:"label"`
	// Generic is the molecule, spelled as `core.generic.name`. The thing rules match on.
	Generic string `json:"generic"`
	// Class is the therapeutic class code.
	Class string `json:"class"`

	// DailyDose and DoseUnit are optional. Absent means a max-dose rule about this drug
	// answers CannotTell rather than passing.
	DailyDose *float64 `json:"daily_dose,omitempty"`
	DoseUnit  string   `json:"dose_unit,omitempty"`

	// Components are the molecules this medicine contains (CP78, migration 00058).
	//
	// # The hole this closes
	//
	// `core.generic` holds a fixed-dose combination as one name — "Sitagliptin + Metformin
	// hydrochloride" is a single row — so a duplicate check comparing `Generic` with `Generic`
	// cannot see that Siglimet beside Comet is metformin twice. docs/medication-rules.md §10
	// named that the single largest hole in the seeded rule set, and this field is its closure:
	// a duplicate is a non-empty intersection of two molecule sets rather than an equality of
	// two names.
	//
	// A single-agent medicine has exactly one component. That is the degenerate case, not a
	// special case, and writing it out is what makes "has one molecule" a claim somebody made
	// rather than a consequence of the name having no plus sign in it.
	Components []string `json:"components,omitempty"`
	// ComponentsKnown is the fail-closed flag, and it is the field that must be branched on
	// rather than `len(Components) > 0`.
	//
	// A molecule set nobody has written intersects nothing, and "intersects nothing" and "is
	// not a duplicate" are the same value with two very different meanings. False makes a
	// duplicate question about this drug answer **cannot verify**; it must never make one
	// answer *no duplicate*. Nil-versus-empty would not carry this, because an empty set is
	// also what a caller who forgot the field produces.
	ComponentsKnown bool `json:"components_known,omitempty"`
}

// sameMolecule answers whether two drugs share a molecule, and says when it cannot tell.
//
// Three answers, for the reason every predicate in this file has three. Equal generic names
// settle it without needing components at all — which is what keeps a hand-typed sandbox picture
// working, and what keeps CP77's behaviour unchanged for the case it could already see. Beyond
// that the molecule sets decide, and if either set is unwritten the honest answer is that nobody
// knows.
func (d Drug) sameMolecule(other Drug) Truth {
	if strings.EqualFold(strings.TrimSpace(d.Generic), strings.TrimSpace(other.Generic)) {
		return Holds
	}
	if !d.ComponentsKnown || !other.ComponentsKnown {
		return CannotTell
	}
	for _, mine := range d.Components {
		for _, theirs := range other.Components {
			if strings.EqualFold(strings.TrimSpace(mine), strings.TrimSpace(theirs)) {
				return Holds
			}
		}
	}
	return DoesNotHold
}

// sharedMolecules names what two drugs have in common, for the sentence a finding carries.
// "Siglimet and Comet are the same molecule" is a claim a physician has to take on trust;
// "Siglimet and Comet both contain metformin" is one he can check against the box.
func (d Drug) sharedMolecules(other Drug) []string {
	var out []string
	for _, mine := range d.Components {
		for _, theirs := range other.Components {
			if strings.EqualFold(strings.TrimSpace(mine), strings.TrimSpace(theirs)) {
				out = append(out, mine)
			}
		}
	}
	return out
}

// Context is everything a rule may look at. Nothing else is readable, by construction.
//
// # Why the pointers
//
// `*float64` rather than `float64` for eGFR, age and dose. A zero eGFR is not "unknown", it is a
// patient in anuric renal failure, and a model that cannot tell those apart is one where the
// most dangerous value in the range is indistinguishable from no value at all. The same argument
// made `NumericInput` take two ranges in the design system, and it is the same mistake.
//
// # No patient identifier, anywhere
//
// There is no patient id in this struct and there must not be. CP78 builds one of these from a
// record and throws it away; the sandbox builds one from a form. Neither is logged — see the
// note on [Ruleset.Findings] — and a Context with an id in it would be a patient's clinical
// picture travelling through a package that has no business holding one.
type Context struct {
	AgeYears *float64 `json:"age_years"`
	// Pregnancy is one of PREGNANT, BREASTFEEDING, PLANNING, NOT_PREGNANT, or empty for
	// unknown. Empty and NOT_PREGNANT are different: the first fails closed, the second does
	// not fire.
	Pregnancy string `json:"pregnancy"`

	EGFR     *float64   `json:"egfr"`
	EGFRAsOf *time.Time `json:"egfr_as_of,omitempty"`

	// Hepatic is NONE, MILD, MODERATE, SEVERE, or empty for unknown.
	Hepatic string `json:"hepatic"`

	// Diagnoses are codes. Nil means unknown and fails closed; an explicitly empty non-nil
	// slice means "asked, and there are none" and does not.
	Diagnoses []string `json:"diagnoses"`
	// Allergies are allergen group codes the patient reacts to, already expanded by CP78
	// through the cross-reactivity map where a rule asked for it. Same nil-versus-empty rule.
	Allergies []string `json:"allergies"`

	// Proposed is what is about to be prescribed.
	Proposed []Drug `json:"proposed"`
	// Current is what the patient is already taking.
	Current []Drug `json:"current"`
}

// Step is one predicate's answer, kept so the sandbox can show its working.
//
// The sandbox's whole value is that a physician can see *why* a rule did what it did. A verdict
// with no working is a verdict he has to take on trust, and the rules he does not trust are the
// ones he will not publish.
type Step struct {
	Kind  PredicateKind `json:"kind"`
	Truth string        `json:"truth"`
	// Because is the reason in plain language, in both languages.
	BecauseEN string `json:"because_en"`
	BecauseBN string `json:"because_bn"`
	// Missing is the datum that was absent, when the answer was CannotTell.
	Missing Datum `json:"missing,omitempty"`
}

// Finding is one rule's verdict.
type Finding struct {
	RuleID    uuid.UUID `json:"rule_id"`
	RuleCode  string    `json:"rule_code"`
	VersionID uuid.UUID `json:"version_id"`
	Version   int       `json:"version"`
	Type      RuleType  `json:"type"`
	Severity  Severity  `json:"severity"`

	Outcome Outcome `json:"outcome"`

	// Subject is the prescribed drug this verdict is about, as the physician labelled it.
	Subject string `json:"subject,omitempty"`
	// SubjectRef is the caller's handle for that line, so the editor can point at it. Empty
	// when the caller supplied no ref, and when the finding is about the prescription as a
	// whole rather than about one line.
	SubjectRef string `json:"subject_ref,omitempty"`

	MessageEN string `json:"message_en"`
	MessageBN string `json:"message_bn"`
	AdviceEN  string `json:"advice_en,omitempty"`
	AdviceBN  string `json:"advice_bn,omitempty"`
	Source    string `json:"source"`

	// Missing is every datum the rule needed and did not get. Populated only on CANNOT_VERIFY.
	Missing []Datum `json:"missing,omitempty"`
	// Steps is the working. The sandbox draws it; CP78 may choose not to.
	Steps []Step `json:"steps"`
}

// Evaluate runs one version against one clinical picture.
//
// Deterministic and allocation-light: no map iteration decides anything, no clock is read, and
// the only ordering is the author's own order of predicates. The same version and the same
// context give the same finding today and in three years, which is what criterion 3's
// reproducibility rests on.
func (v Version) Evaluate(rule Rule, c Context) Finding {
	f := Finding{
		RuleID: rule.ID, RuleCode: rule.Code, VersionID: v.ID, Version: v.Version,
		Type: rule.Type, Severity: v.Severity,
		MessageEN: v.MessageEN, MessageBN: v.MessageBN,
		AdviceEN: v.AdviceEN, AdviceBN: v.AdviceBN, Source: v.Source,
		Outcome: OutcomeNotApplicable,
	}

	// The subject decides applicability, one proposed drug at a time. The first drug that
	// matches and produces the strongest outcome is the one reported: a rule about metformin
	// met by two metformins is one finding, not two.
	best := -1
	for _, drug := range c.Proposed {
		if !v.Condition.Subject.covers(drug) {
			continue
		}
		outcome, steps, missing := v.assess(c, drug)
		if r := outcomeRank(outcome); r > best {
			best = r
			f.Outcome, f.Steps, f.Missing, f.Subject = outcome, steps, missing, drug.Label
			f.SubjectRef = drug.Ref
		}
	}
	return f
}

// Covers reports whether this version's subject is about a given drug.
//
// Exported for the engine's coverage report, which is the one question `Evaluate` cannot answer:
// a rule that applied to a drug and did not fire, and a rule that was never about that drug, are
// both "no finding" — and only one of them means the drug was checked. §7.2's honesty
// requirement is that those two never render the same, and this is what tells them apart.
func (v Version) Covers(d Drug) bool { return v.Condition.Subject.covers(d) }

// EvaluateFor runs one version against one clinical picture, about one named drug.
//
// # Why the engine needs this and `Evaluate` is not enough
//
// `Evaluate` reports the strongest thing a rule has to say about a whole prescription, in one
// finding, because that is what the sandbox shows: one rule, one verdict. A prescription is a
// different question. Metformin and Siglimet on one sheet at eGFR 25 are two contraindicated
// lines, and a check that reported one of them would leave the physician deleting the wrong
// drug. The same argument applies to coverage: "which lines did this rule not reach" cannot be
// answered by a result that names only the worst one.
//
// It is the same evaluation — the same `assess`, the same predicates, the same three-valued
// logic — with the subject fixed instead of chosen. A second implementation here would be the
// defect CP77's sandbox note is about, one layer up.
func (v Version) EvaluateFor(rule Rule, c Context, subject Drug) Finding {
	f := Finding{
		RuleID: rule.ID, RuleCode: rule.Code, VersionID: v.ID, Version: v.Version,
		Type: rule.Type, Severity: v.Severity,
		MessageEN: v.MessageEN, MessageBN: v.MessageBN,
		AdviceEN: v.AdviceEN, AdviceBN: v.AdviceBN, Source: v.Source,
		Outcome: OutcomeNotApplicable,
	}
	if !v.Condition.Subject.covers(subject) {
		return f
	}
	f.Subject, f.SubjectRef = subject.Label, subject.Ref
	f.Outcome, f.Steps, f.Missing = v.assess(c, subject)
	return f
}

// outcomeRank is which of two outcomes to report when a rule's subject matches twice. Firing
// beats cannot-verify beats not-firing, because the strongest thing this rule has to say about
// this prescription is the thing the physician needs to see.
func outcomeRank(o Outcome) int {
	switch o {
	case OutcomeFires:
		return 3
	case OutcomeCannotVerify:
		return 2
	case OutcomeDoesNotFire:
		return 1
	default:
		return 0
	}
}

func (v Version) assess(c Context, subject Drug) (Outcome, []Step, []Datum) {
	steps := make([]Step, 0, len(v.Condition.When))
	var missing []Datum
	anyUnknown := false

	for _, p := range v.Condition.When {
		truth, en, bn := p.test(c, subject)
		step := Step{Kind: p.Kind, Truth: truthName(truth), BecauseEN: en, BecauseBN: bn}
		if truth == CannotTell {
			step.Missing = p.Needs()
			missing = append(missing, p.Needs())
			anyUnknown = true
		}
		steps = append(steps, step)
		if truth == DoesNotHold {
			// Settled. See the file note on why a definite no beats an unknown.
			return OutcomeDoesNotFire, steps, nil
		}
	}
	if anyUnknown {
		return OutcomeCannotVerify, steps, missing
	}
	return OutcomeFires, steps, nil
}

func truthName(t Truth) string {
	switch t {
	case Holds:
		return "HOLDS"
	case CannotTell:
		return "CANNOT_TELL"
	default:
		return "DOES_NOT_HOLD"
	}
}

// covers reports whether a target names this drug.
func (t Target) covers(d Drug) bool {
	switch t.Match {
	case MatchAny:
		return true
	case MatchGeneric:
		for _, g := range t.Generics {
			if strings.EqualFold(g, d.Generic) {
				return true
			}
		}
	case MatchClass:
		for _, c := range t.Classes {
			if strings.EqualFold(c, d.Class) {
				return true
			}
		}
	}
	return false
}

// test answers one predicate, with a sentence in both languages saying why.
func (p Predicate) test(c Context, subject Drug) (Truth, string, string) {
	switch p.Kind {
	case PredEGFR:
		if c.EGFR == nil {
			return CannotTell, "no eGFR is on file", "কোনো eGFR নথিতে নেই"
		}
		return compare(*c.EGFR, p, "eGFR", "eGFR")

	case PredAge:
		if c.AgeYears == nil {
			return CannotTell, "the patient's age is not known", "রোগীর বয়স জানা নেই"
		}
		return compare(*c.AgeYears, p, "age", "বয়স")

	case PredDailyDose:
		if subject.DailyDose == nil {
			return CannotTell, "the daily dose has not been entered",
				"দৈনিক মাত্রা এখনও লেখা হয়নি"
		}
		if !strings.EqualFold(strings.TrimSpace(subject.DoseUnit), strings.TrimSpace(p.Unit)) {
			// Refused rather than converted. A rule written in mg meeting a dose in mcg is
			// a thousandfold error waiting to be made by whoever writes the conversion.
			return CannotTell,
				"the dose is in " + subject.DoseUnit + " and the rule is written in " + p.Unit,
				"মাত্রা " + subject.DoseUnit + " এককে, নিয়মটি " + p.Unit + " এককে লেখা"
		}
		return compare(*subject.DailyDose, p, "the daily dose", "দৈনিক মাত্রা")

	case PredPregnancy:
		if strings.TrimSpace(c.Pregnancy) == "" {
			return CannotTell, "pregnancy status has not been recorded",
				"গর্ভাবস্থার তথ্য নথিভুক্ত হয়নি"
		}
		for _, s := range p.States {
			if strings.EqualFold(s, c.Pregnancy) {
				return Holds, "the patient is " + pregnancyEN[c.Pregnancy],
					"রোগী " + pregnancyBN[c.Pregnancy]
			}
		}
		return DoesNotHold, "the patient is " + pregnancyEN[c.Pregnancy],
			"রোগী " + pregnancyBN[c.Pregnancy]

	case PredHepatic:
		if strings.TrimSpace(c.Hepatic) == "" {
			return CannotTell, "liver function has not been assessed",
				"যকৃতের কার্যকারিতা মূল্যায়ন করা হয়নি"
		}
		for _, s := range p.States {
			if strings.EqualFold(s, c.Hepatic) {
				return Holds, "hepatic impairment is " + strings.ToLower(c.Hepatic),
					"যকৃতের দুর্বলতা " + hepaticBN[c.Hepatic]
			}
		}
		return DoesNotHold, "hepatic impairment is " + strings.ToLower(c.Hepatic),
			"যকৃতের দুর্বলতা " + hepaticBN[c.Hepatic]

	case PredDiagnosis:
		if c.Diagnoses == nil {
			return CannotTell, "the diagnosis list has not been read",
				"রোগনির্ণয়ের তালিকা পড়া হয়নি"
		}
		for _, want := range p.DiagnosisCodes {
			for _, has := range c.Diagnoses {
				if strings.EqualFold(want, has) {
					return Holds, "the patient carries the diagnosis " + has,
						"রোগীর রোগনির্ণয়ে " + has + " রয়েছে"
				}
			}
		}
		return DoesNotHold, "none of those diagnoses is on the record",
			"সেসব রোগনির্ণয়ের কোনোটিই নথিতে নেই"

	case PredAllergy:
		if c.Allergies == nil {
			return CannotTell, "allergy status has not been established",
				"অ্যালার্জির তথ্য নেওয়া হয়নি"
		}
		for _, has := range c.Allergies {
			if strings.EqualFold(p.AllergenGroup, has) {
				return Holds, "the patient reacts to " + has,
					"রোগীর " + has + "-এ প্রতিক্রিয়া হয়"
			}
		}
		return DoesNotHold, "no reaction to " + p.AllergenGroup + " is recorded",
			p.AllergenGroup + "-এ প্রতিক্রিয়ার কোনো নথি নেই"

	case PredCoPrescribed:
		if p.CurrentMedications && c.Current == nil {
			return CannotTell, "the current medication list has not been read",
				"বর্তমান ওষুধের তালিকা পড়া হয়নি"
		}
		for _, d := range c.Proposed {
			if d.Label == subject.Label && d.Generic == subject.Generic {
				continue
			}
			if p.With.covers(d) {
				return Holds, d.Label + " is on the same prescription",
					d.Label + " একই ব্যবস্থাপত্রে রয়েছে"
			}
		}
		if p.CurrentMedications {
			for _, d := range c.Current {
				if p.With.covers(d) {
					return Holds, d.Label + " is already being taken",
						d.Label + " রোগী ইতিমধ্যে খাচ্ছেন"
				}
			}
		}
		return DoesNotHold, "nothing that interacts is being taken alongside",
			"সঙ্গে এমন কিছু নেওয়া হচ্ছে না যা প্রতিক্রিয়া করে"

	case PredDuplicate:
		return p.testDuplicate(c, subject)
	}
	return DoesNotHold, "", ""
}

// testDuplicate answers "is this medicine being given twice".
//
// Counted over the proposed list plus, when the author asked for it, the current medications.
// The subject itself is one of the matches, so a duplicate needs two.
//
// # Three answers, because two would be a lie
//
// At CLASS level the question is settled by two class codes, which are always either equal or
// not. At GENERIC level it is a question about molecules (see [Drug.Components]), and a molecule
// set nobody has written cannot answer it. So a drug whose components are unknown, beside a drug
// with a different generic name, produces **CannotTell** — "these might be the same medicine and
// I cannot tell" — and never DoesNotHold.
//
// That asymmetry is deliberate and it is the whole point of the change. Before migration 00058
// this function answered "no duplicate" for Siglimet beside Comet, which is metformin twice and
// one of the commonest real prescribing errors in a diabetes clinic. The cost of the new answer
// is a "cannot verify" on any medicine nobody has decomposed; the cost of the old one was
// silence on a duplicate that was there.
func (p Predicate) testDuplicate(c Context, subject Drug) (Truth, string, string) {
	if p.CurrentMedications && c.Current == nil {
		return CannotTell, "the current medication list has not been read",
			"বর্তমান ওষুধের তালিকা পড়া হয়নি"
	}

	// The subject is one of the matches, so the count starts at one and a duplicate needs two.
	names := []string{subject.Label}
	var shared []string
	var unverifiable []string

	consider := func(d Drug, self bool) {
		if self {
			return
		}
		if p.With.Match == MatchClass {
			if d.Class != "" && subject.Class != "" && strings.EqualFold(d.Class, subject.Class) {
				names = append(names, d.Label)
			}
			return
		}
		switch subject.sameMolecule(d) {
		case Holds:
			names = append(names, d.Label)
			shared = append(shared, subject.sharedMolecules(d)...)
		case CannotTell:
			unverifiable = append(unverifiable, d.Label)
		}
	}

	// Positional, not by value: two lines of the identical medicine are two different items
	// and the second one is exactly the duplicate being looked for. Comparing by label would
	// make "Comet 500" twice invisible.
	for i := range c.Proposed {
		consider(c.Proposed[i], sameItem(c.Proposed[i], subject))
	}
	if p.CurrentMedications {
		for i := range c.Current {
			consider(c.Current[i], false)
		}
	}

	if len(names) >= 2 {
		joined := strings.Join(names, " + ")
		if p.With.Match == MatchClass {
			return Holds, joined + " are the same therapeutic class",
				joined + " একই চিকিৎসা-শ্রেণির"
		}
		if molecules := tidy(shared, strings.ToLower); len(molecules) > 0 {
			// Naming the molecule is what makes a combination's duplicate checkable by eye:
			// "Siglimet + Comet both contain metformin" is an argument a physician can have.
			return Holds, joined + " both contain " + joinOrEN2(molecules),
				joined + " দুটিতেই " + strings.Join(molecules, ", ") + " আছে"
		}
		return Holds, joined + " are the same molecule", joined + " একই অণুর"
	}

	// Nothing matched — but something might have, and nobody knows.
	if len(unverifiable) > 0 {
		listed := strings.Join(unverifiable, ", ")
		return CannotTell,
			"it is not recorded what " + listed + " is made of, so it cannot be told whether " +
				subject.Label + " repeats it",
			listed + " কী দিয়ে তৈরি তা নথিভুক্ত নেই, তাই " + subject.Label +
				" তার পুনরাবৃত্তি কি না বলা যাচ্ছে না"
	}

	what := "molecule"
	if p.With.Match == MatchClass {
		what = "class"
	}
	return DoesNotHold, "nothing else of the same " + what + " is present",
		"একই শ্রেণির বা অণুর আর কিছু নেই"
}

// sameItem decides whether a drug in the list *is* the subject rather than a second line that
// happens to look like it.
//
// Compared by the item reference the engine stamps on every proposed drug, and by label and
// generic together when there is none — which is what the sandbox's hand-typed pictures have.
// Getting this wrong in either direction is a real defect: too loose and a genuine duplicate of
// the identical medicine is invisible, too tight and every single drug reports itself twice.
func sameItem(d, subject Drug) bool {
	if d.Ref != "" || subject.Ref != "" {
		return d.Ref == subject.Ref
	}
	return d.Label == subject.Label && d.Generic == subject.Generic
}

// joinOrEN2 writes a short list as English prose. "metformin" / "metformin and sitagliptin".
func joinOrEN2(in []string) string {
	switch len(in) {
	case 0:
		return ""
	case 1:
		return in[0]
	case 2:
		return in[0] + " and " + in[1]
	default:
		return strings.Join(in[:len(in)-1], ", ") + " and " + in[len(in)-1]
	}
}

// compare runs a numeric predicate and writes the sentence.
func compare(got float64, p Predicate, labelEN, labelBN string) (Truth, string, string) {
	var ok bool
	var relEN, relBN string
	switch p.Operator {
	case OpLessThan:
		ok, relEN, relBN = got < p.Value, "below", "-এর নিচে"
	case OpAtMost:
		ok, relEN, relBN = got <= p.Value, "at or below", "-এর সমান বা নিচে"
	case OpGreaterThan:
		ok, relEN, relBN = got > p.Value, "above", "-এর উপরে"
	case OpAtLeast:
		ok, relEN, relBN = got >= p.Value, "at or above", "-এর সমান বা উপরে"
	}
	en := labelEN + " is " + trimFloat(got) + " " + p.Unit
	bn := labelBN + " " + trimFloat(got) + " " + p.Unit
	if ok {
		return Holds, en + ", which is " + relEN + " " + trimFloat(p.Value) + " " + p.Unit,
			bn + ", যা " + trimFloat(p.Value) + " " + p.Unit + relBN
	}
	return DoesNotHold, en + ", which is not " + relEN + " " + trimFloat(p.Value) + " " + p.Unit,
		bn + ", যা " + trimFloat(p.Value) + " " + p.Unit + relBN + " নয়"
}

// trimFloat renders a number the way a clinician writes it: 30 rather than 30.0, 2.5 rather
// than 2.500000. ASCII digits in both languages, per the design system.
func trimFloat(f float64) string {
	s := strings.TrimRight(strings.TrimRight(formatFloat(f), "0"), ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}

func formatFloat(f float64) string {
	return strings.TrimSpace(strings.Replace(sprintfFloat(f), "+", "", 1))
}

var pregnancyEN = map[string]string{
	"PREGNANT": "pregnant", "BREASTFEEDING": "breastfeeding",
	"PLANNING": "planning a pregnancy", "NOT_PREGNANT": "not pregnant",
}

var pregnancyBN = map[string]string{
	"PREGNANT": "গর্ভবতী", "BREASTFEEDING": "স্তন্যদানকারী",
	"PLANNING": "গর্ভধারণের পরিকল্পনা করছেন", "NOT_PREGNANT": "গর্ভবতী নন",
}

var hepaticBN = map[string]string{
	"NONE": "নেই", "MILD": "মৃদু", "MODERATE": "মাঝারি", "SEVERE": "তীব্র",
}

// ---------------------------------------------------------------------------
// The ruleset — what CP78 asks
// ---------------------------------------------------------------------------

// Ruleset is the rules that were live at one instant, frozen.
//
// **This is the interface CP78 builds on.** It is loaded by [Store.RulesetAt], which answers
// "which version of each rule was the live one at time T" from the version periods — the same
// half-open-interval idiom CP75 uses for prices, and the same reason: a check run in March must
// be reproducible in December against March's rules, and a query for "the current version"
// cannot answer that however carefully it is written.
//
// Nothing unapproved can be in one. An unapproved version has no effective period, so the query
// cannot return it; [Ruleset.Findings] checks [Version.Approved] again anyway, because the thing
// being guarded — a rule I drafted behaving like a rule Dr. Nahid wrote — is worth two locks.
type Ruleset struct {
	// At is the instant this set was the live one.
	At time.Time
	// Rules and Versions are parallel: Versions[i] is the version of Rules[i] that was live.
	Rules    []Rule
	Versions []Version
}

// Findings evaluates every rule in the set against one clinical picture.
//
// # What this deliberately does not do
//
// It does not report coverage — which proposed drugs no rule mentioned — because that is CP78's
// and it needs the formulary to say what a drug *is* before it can say nothing covered it. It
// does not expand allergies through the cross-reactivity map; CP78 does that while building the
// Context, which is the right place because the expansion is a property of the patient's allergy
// rather than of any one rule. And it does not persist anything.
//
// # It logs nothing
//
// Not the context, not the findings, not a count. The argument is short: a Context is a
// patient's age, pregnancy state, kidney function, diagnoses, allergies and medication list. A
// log line with any of it in it is a patient record in a file with different access controls,
// and the sandbox — which runs this on a picture a physician typed — is where somebody would
// most reasonably have added one.
func (rs Ruleset) Findings(c Context) []Finding {
	out := make([]Finding, 0, len(rs.Rules))
	for i, rule := range rs.Rules {
		v := rs.Versions[i]
		if !v.Approved() {
			// Unreachable through RulesetAt, and kept because it is the second lock on the
			// one property this package exists to guarantee.
			continue
		}
		f := v.Evaluate(rule, c)
		if f.Outcome == OutcomeNotApplicable || f.Outcome == OutcomeDoesNotFire {
			continue
		}
		out = append(out, f)
	}
	sort.SliceStable(out, func(i, j int) bool {
		// Blocks first, then cannot-verifies within a severity, then by code so the order is
		// total. A physician reads the top of this list and stops.
		if out[i].Severity.Rank() != out[j].Severity.Rank() {
			return out[i].Severity.Rank() < out[j].Severity.Rank()
		}
		if (out[i].Outcome == OutcomeFires) != (out[j].Outcome == OutcomeFires) {
			return out[i].Outcome == OutcomeFires
		}
		return out[i].RuleCode < out[j].RuleCode
	})
	return out
}

// sprintfFloat is strconv in one place, so [trimFloat] reads as one idea.
func sprintfFloat(f float64) string { return strconv.FormatFloat(f, 'f', 6, 64) }
