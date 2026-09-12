// Package medsafety is the clinic's medication safety rule library (CP77, §6.3, §7.2, D-22).
//
// # Whose rules these are
//
// D-22 settled it: **Dr. Nahid authors every clinical rule.** No licensed interaction database,
// no vendor's content, no developer inventing a threshold. That decision is only viable if the
// authoring tool is good enough that the rules actually get written, which is why this package's
// centre of gravity is not an engine — CP78 is the engine — but a model a physician can fill in
// on a form, read back in his own words, and try against a patient before he publishes it.
//
// Everything below follows from that:
//
//   - A [Condition] is a **subject and a list of tests joined by AND**. No OR, no NOT, no
//     nesting. "Or" is a second rule, which a physician can write; a boolean expression tree is
//     a thing only a programmer can read, and a rule nobody can read is a rule nobody checks.
//   - Every predicate names the **datum it needs**. That is what lets CP78 fail closed — an
//     absent creatinine on a renally-cleared drug must produce "cannot verify", never silence —
//     and it is what the authoring form uses to tell the author, before he saves, what this rule
//     will be unable to answer.
//   - Every rule carries a **bilingual message and a severity**, enforced by this package, by a
//     CHECK constraint, and by a test. A rule that fires in English at a counsellor who works in
//     Bangla has not fired.
//   - Every rule version is **frozen when it is published** and keeps its period, so a check run
//     last March can be re-run against the rules that were live last March.
//
// # What is seeded, and why it cannot fire
//
// Thirty rules and three cross-reactivity groups ship with the migration, drafted from published
// guidance (ADA Standards of Care, KDIGO, FDA labelling, ATA, the BNF) and **cited row by row**.
// Every one of them is stored `DRAFT` with `approved_at` null. An unapproved version has no
// effective period, so [RulesetAt] cannot return it and [Ruleset.Findings] cannot fire it, and
// the screens draw it with an "unapproved" badge and the source it came from.
//
// That is deliberate and it is the most important sentence in this package. A rule I wrote must
// not behave like a rule Dr. Nahid wrote. He has to read each one, agree with it, and put his
// name and a fresh second factor against it — and until he does, it is a suggestion sitting in a
// list, not a thing that stops a prescription.
//
// # What CP78 inherits
//
// This package ships the **per-rule** semantics: [Rule.Evaluate] takes a [Context] and returns a
// [Finding]. It has to, because the sandbox cannot exist without it — "see the result before
// publishing" is an acceptance criterion and it is the same evaluation the engine will do.
//
// CP78 owns everything above one rule:
//
//	building a Context from a real patient (eGFR with recency, allergies, current medications,
//	coded diagnoses) · running a whole Ruleset over a draft prescription · expanding an allergy
//	through the cross-reactivity groups · duplicate-therapy detection across items · coverage
//	reporting — which proposed drugs no rule mentioned — · ordering findings by severity ·
//	recording SAFETY_CHECK_RUN with the exact versions evaluated.
//
// The seam is [Ruleset] and [Context]: CP78 fills the Context and asks the Ruleset. Nothing in
// this package reads a patient, and nothing in it writes an event.
package medsafety

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// The eight kinds of rule
// ---------------------------------------------------------------------------

// RuleType is §6.3's list, and it is closed. A ninth kind is a schema change and a conversation,
// not a string somebody types into a form.
type RuleType string

const (
	// TypeInteraction — this drug with that drug.
	TypeInteraction RuleType = "INTERACTION"
	// TypeContraindication — this drug with that diagnosis or that allergy.
	TypeContraindication RuleType = "CONTRAINDICATION"
	// TypeRenal — this drug at this kidney function.
	TypeRenal RuleType = "RENAL"
	// TypeHepatic — this drug at this liver function.
	TypeHepatic RuleType = "HEPATIC"
	// TypePregnancy — this drug in pregnancy, breastfeeding, or when pregnancy is planned.
	TypePregnancy RuleType = "PREGNANCY"
	// TypePaediatric — this drug below this age.
	TypePaediatric RuleType = "PAEDIATRIC"
	// TypeDuplicateTherapy — two of the same molecule, or two of the same class.
	TypeDuplicateTherapy RuleType = "DUPLICATE_THERAPY"
	// TypeMaxDose — more of this drug per day than the label allows.
	TypeMaxDose RuleType = "MAX_DOSE"
)

// AllRuleTypes is the catalogue, in the order §6.3 states it and the order the form lists it.
var AllRuleTypes = []RuleType{
	TypeInteraction, TypeContraindication, TypeRenal, TypeHepatic,
	TypePregnancy, TypePaediatric, TypeDuplicateTherapy, TypeMaxDose,
}

// Severity is what the rule does when it fires.
//
// Three levels and no fourth. BLOCK stops the prescription and needs an override with a recorded
// reason (CP82); WARN is shown and can be prescribed past; INFO is a note. A scale with five
// levels on it is a scale where the middle three mean whatever the author felt that day.
type Severity string

const (
	// SeverityBlock — do not prescribe this. Overridable only with a reason on the record.
	SeverityBlock Severity = "BLOCK"
	// SeverityWarn — prescribe if you mean to, having seen this.
	SeverityWarn Severity = "WARN"
	// SeverityInfo — worth knowing, nothing to decide.
	SeverityInfo Severity = "INFO"
)

// AllSeverities is the catalogue, most severe first.
var AllSeverities = []Severity{SeverityBlock, SeverityWarn, SeverityInfo}

// Rank orders findings. Lower is more severe.
func (s Severity) Rank() int {
	switch s {
	case SeverityBlock:
		return 0
	case SeverityWarn:
		return 1
	default:
		return 2
	}
}

// ---------------------------------------------------------------------------
// The condition
// ---------------------------------------------------------------------------

// TargetMatch says how a drug is named: by its molecule, by its therapeutic class, or not at all.
type TargetMatch string

const (
	// MatchGeneric names molecules. The precise way, and the one a label is written in.
	MatchGeneric TargetMatch = "GENERIC"
	// MatchClass names a therapeutic class from the formulary's own vocabulary — every ACE
	// inhibitor, every sulphonylurea. One row instead of six, and it covers a molecule added
	// to the formulary next year.
	MatchClass TargetMatch = "CLASS"
	// MatchAny is every drug. Only meaningful as a rule's subject, and only for the two kinds
	// of rule that are about the prescription rather than about a drug.
	MatchAny TargetMatch = "ANY"
)

// Target names a set of drugs.
type Target struct {
	Match TargetMatch `json:"match"`
	// Generics are molecule names, exactly as `core.generic.name` spells them. Latin script,
	// matched case-insensitively. Validated against the formulary when a rule is saved, so
	// that a typo is caught by the author rather than by a rule that silently never fires.
	Generics []string `json:"generics,omitempty"`
	// Classes are class codes from `core.medication_class`.
	Classes []string `json:"classes,omitempty"`
}

// PredicateKind is what a single test looks at. Closed, like RuleType.
type PredicateKind string

const (
	// PredCoPrescribed — another drug is on this prescription or in the patient's current
	// medications. The interaction rule's other half.
	PredCoPrescribed PredicateKind = "CO_PRESCRIBED"
	// PredDuplicate — the subject appears twice, at molecule or at class level.
	PredDuplicate PredicateKind = "DUPLICATE"
	// PredDiagnosis — the patient carries one of these coded diagnoses.
	PredDiagnosis PredicateKind = "DIAGNOSIS"
	// PredAllergy — the patient is allergic to a member of this allergen group.
	PredAllergy PredicateKind = "ALLERGY"
	// PredEGFR — estimated glomerular filtration rate, mL/min/1.73m².
	PredEGFR PredicateKind = "EGFR"
	// PredAge — age in years.
	PredAge PredicateKind = "AGE"
	// PredPregnancy — pregnancy state.
	PredPregnancy PredicateKind = "PREGNANCY"
	// PredHepatic — hepatic impairment grade.
	PredHepatic PredicateKind = "HEPATIC"
	// PredDailyDose — total daily dose of the subject drug, in the unit named.
	PredDailyDose PredicateKind = "DAILY_DOSE"
)

// Operator is a numeric comparison. Four of them, all closed-form; there is no BETWEEN because
// two predicates joined by AND say the same thing and read better on a form.
type Operator string

const (
	OpLessThan    Operator = "LT"
	OpAtMost      Operator = "LTE"
	OpGreaterThan Operator = "GT"
	OpAtLeast     Operator = "GTE"
)

// Predicate is one test.
//
// A flat struct with a `kind` and the fields that kind uses, rather than an interface with nine
// implementations. It is what goes in the JSONB column, it is what the form posts, and it is
// what the plain-language renderer walks — three consumers who would otherwise each need their
// own encoding of the same nine shapes.
type Predicate struct {
	Kind PredicateKind `json:"kind"`

	// CO_PRESCRIBED and DUPLICATE.
	With Target `json:"with,omitempty"`
	// CurrentMedications widens CO_PRESCRIBED from "also on this prescription" to "also on
	// this prescription or already being taken". Default false, because the two are clinically
	// different and the author should say which he means.
	CurrentMedications bool `json:"current_medications,omitempty"`

	// DIAGNOSIS.
	DiagnosisCodes []string `json:"diagnosis_codes,omitempty"`

	// ALLERGY.
	AllergenGroup string `json:"allergen_group,omitempty"`
	// CrossReactive asks CP78 to expand the group through the approved cross-reactivity map.
	// Off by default: penicillin allergy raising a cephalosporin flag is a judgement, and the
	// author says whether this rule wants it.
	CrossReactive bool `json:"cross_reactive,omitempty"`

	// EGFR, AGE, DAILY_DOSE.
	Operator Operator `json:"operator,omitempty"`
	Value    float64  `json:"value,omitempty"`
	// Unit is carried and compared, never converted. A max-dose rule written in mg that met a
	// prescription in mcg must say it cannot verify, not divide by a thousand and hope.
	Unit string `json:"unit,omitempty"`

	// PREGNANCY and HEPATIC.
	States []string `json:"states,omitempty"`
}

// Condition is a rule's whole logic: which drug it is about, and what has to be true.
//
// # Why AND and nothing else
//
// Three reasons, in order of how much they matter.
//
//  1. **A physician can read it.** "When metformin is prescribed, and the eGFR is below 30" is a
//     sentence. A parse tree with an OR inside a NOT is not, and the thing that goes wrong with
//     an unreadable rule is not that it is wrong — it is that nobody notices it is wrong.
//  2. **CP78 can evaluate it in a loop.** No recursion, no precedence, no short-circuit
//     surprises. The evaluation of a whole ruleset is O(rules × predicates) with a small
//     constant, which is what "sub-second" is made of.
//  3. **Three-valued logic stays simple.** With absent data in play, every predicate answers
//     holds / does not hold / **cannot tell**, and the rule's answer over an AND is obvious. The
//     same three values through an OR and a NOT are where fail-closed becomes fail-quietly.
//
// The cost is real and is stated: "avoid in pregnancy *or* breastfeeding" needs one predicate
// with two states (which PREGNANCY has), and "avoid in heart failure *or* liver disease" needs
// two rules. The second is a genuine duplication, and the answer is that two rules a physician
// can each read beats one he cannot.
type Condition struct {
	// Subject is the drug being prescribed that this rule is about.
	Subject Target `json:"subject"`
	// When is every test that must hold. All of them, joined by AND. Empty is legal only for a
	// rule whose subject alone is the whole statement — which is none of the eight types, so
	// [Condition.Validate] refuses it.
	When []Predicate `json:"when"`
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

// ErrInvalidRule is a rule that does not say something evaluable.
var ErrInvalidRule = errors.New("medsafety: this rule does not say something that can be checked")

// ErrNotBilingual is a rule missing one of its two languages.
var ErrNotBilingual = errors.New("medsafety: a rule needs its message in both languages")

// Vocabulary is what a condition is validated against: the molecules and classes this clinic
// actually holds, and the allergen groups that exist.
//
// Passed in rather than looked up, because validation happens in three places — the authoring
// save, the import, and the migration's own seed check — and a validator that fetched its own
// vocabulary would make the third impossible.
type Vocabulary struct {
	// Generics is every `core.generic.name`, lowercased.
	Generics map[string]string
	// Classes is every `core.medication_class.code`.
	Classes map[string]bool
	// AllergenGroups is every `core.allergen_group.code`.
	AllergenGroups map[string]bool
}

// Validate checks that a condition says something a rule of this type can evaluate.
//
// It is strict on purpose. Every check here is one the authoring form can also make, so an
// author is told at the moment he types; this is the wall behind it, because the import endpoint
// and a future API client do not go through the form.
func (c Condition) Validate(t RuleType, v Vocabulary) error {
	// A duplicate-therapy rule is the one kind that is about the prescription rather than about
	// a medicine — "the same molecule twice, whatever it is" — so it is the one kind whose
	// subject may be ANY. Everywhere else an ANY subject is a rule somebody meant to narrow and
	// did not, and its consequence is a finding on every line of every prescription.
	if err := c.Subject.validate(v, t != TypeDuplicateTherapy); err != nil {
		return fmt.Errorf("%w: the medicine this rule is about — %s", ErrInvalidRule, err)
	}
	if len(c.When) == 0 {
		return fmt.Errorf("%w: a rule needs at least one thing that has to be true, or it "+
			"would fire on every prescription of this medicine", ErrInvalidRule)
	}
	allowed := allowedPredicates[t]
	seen := map[PredicateKind]bool{}
	for i, p := range c.When {
		if !allowed[p.Kind] {
			return fmt.Errorf("%w: a %s rule cannot test %s — that belongs in a %s rule",
				ErrInvalidRule, t, p.Kind, typeFor(p.Kind))
		}
		if seen[p.Kind] && p.Kind != PredDiagnosis && p.Kind != PredCoPrescribed {
			return fmt.Errorf("%w: condition %d tests %s twice; two bounds on one number are "+
				"two rules", ErrInvalidRule, i+1, p.Kind)
		}
		seen[p.Kind] = true
		if err := p.validate(v); err != nil {
			return fmt.Errorf("%w: condition %d — %s", ErrInvalidRule, i+1, err)
		}
	}
	return nil
}

// allowedPredicates is which tests each kind of rule may use.
//
// A table rather than a switch, because it is the part of the model a reviewer most wants to
// read at a glance, and because the authoring form builds its own field list from it — so the
// form and the validator cannot disagree about what a renal rule may contain.
var allowedPredicates = map[RuleType]map[PredicateKind]bool{
	TypeInteraction:      {PredCoPrescribed: true},
	TypeContraindication: {PredDiagnosis: true, PredAllergy: true},
	TypeRenal:            {PredEGFR: true},
	TypeHepatic:          {PredHepatic: true},
	TypePregnancy:        {PredPregnancy: true},
	TypePaediatric:       {PredAge: true},
	TypeDuplicateTherapy: {PredDuplicate: true},
	TypeMaxDose:          {PredDailyDose: true},
}

// typeFor names the rule type a predicate belongs to, for the error message. A validator that
// says "that is not allowed" and stops is one the author argues with; one that says where the
// thing he wants does belong is one he follows.
func typeFor(k PredicateKind) RuleType {
	for t, kinds := range allowedPredicates {
		if kinds[k] {
			return t
		}
	}
	return "?"
}

func (t Target) validate(v Vocabulary, subject bool) error {
	switch t.Match {
	case MatchGeneric:
		if len(t.Generics) == 0 {
			return errors.New("no medicine is named")
		}
		for _, g := range t.Generics {
			if _, ok := v.Generics[strings.ToLower(strings.TrimSpace(g))]; !ok {
				return fmt.Errorf("this clinic's formulary has no molecule called %q", g)
			}
		}
	case MatchClass:
		if len(t.Classes) == 0 {
			return errors.New("no class is named")
		}
		for _, c := range t.Classes {
			if !v.Classes[strings.TrimSpace(c)] {
				return fmt.Errorf("this clinic's formulary has no class called %q", c)
			}
		}
	case MatchAny:
		if subject {
			// A rule about every drug is almost always a rule somebody meant to narrow and
			// did not, and the consequence is a finding on every line of every prescription.
			return errors.New("say which medicine or which class this rule is about")
		}
	default:
		return fmt.Errorf("%q is not a way of naming a medicine", t.Match)
	}
	return nil
}

func (p Predicate) validate(v Vocabulary) error {
	switch p.Kind {
	case PredCoPrescribed:
		return p.With.validate(v, false)
	case PredDuplicate:
		if p.With.Match != MatchGeneric && p.With.Match != MatchClass {
			return errors.New("say whether a duplicate means the same molecule or the same class")
		}
	case PredDiagnosis:
		if len(p.DiagnosisCodes) == 0 {
			return errors.New("no diagnosis is named")
		}
	case PredAllergy:
		if p.AllergenGroup == "" {
			return errors.New("no allergen is named")
		}
		if !v.AllergenGroups[p.AllergenGroup] {
			return fmt.Errorf("there is no allergen group called %q", p.AllergenGroup)
		}
	case PredEGFR, PredAge, PredDailyDose:
		switch p.Operator {
		case OpLessThan, OpAtMost, OpGreaterThan, OpAtLeast:
		default:
			return fmt.Errorf("%q is not a comparison", p.Operator)
		}
		if p.Value <= 0 {
			return errors.New("the number has to be above zero")
		}
		if p.Unit == "" {
			return errors.New("say what unit the number is in")
		}
	case PredPregnancy:
		if len(p.States) == 0 {
			return errors.New("say which states this applies to")
		}
		for _, s := range p.States {
			if !validPregnancy[s] {
				return fmt.Errorf("%q is not a pregnancy state", s)
			}
		}
	case PredHepatic:
		if len(p.States) == 0 {
			return errors.New("say which grades of impairment this applies to")
		}
		for _, s := range p.States {
			if !validHepatic[s] {
				return fmt.Errorf("%q is not a grade of hepatic impairment", s)
			}
		}
	default:
		return fmt.Errorf("%q is not something a rule can test", p.Kind)
	}
	return nil
}

// The two categorical vocabularies. Small, closed, and here rather than in a table because they
// are properties of the clinical model rather than of this clinic — no facility gets to invent a
// fourth pregnancy state.
var (
	validPregnancy = map[string]bool{
		"PREGNANT": true, "BREASTFEEDING": true, "PLANNING": true,
	}
	validHepatic = map[string]bool{
		"MILD": true, "MODERATE": true, "SEVERE": true,
	}
)

// PregnancyStates and HepaticGrades are the catalogues the authoring form draws.
var (
	PregnancyStates = []string{"PREGNANT", "BREASTFEEDING", "PLANNING"}
	HepaticGrades   = []string{"MILD", "MODERATE", "SEVERE"}
)

// ---------------------------------------------------------------------------
// Canonical form
// ---------------------------------------------------------------------------

// Canonical returns the condition with its lists trimmed, lowercased where they are matched
// case-insensitively, and sorted.
//
// Two reasons, and the second is the one that matters. First, a rule saved twice with the same
// meaning should produce the same JSON, so a diff between two versions shows what changed rather
// than what was reordered. Second, **reproducibility**: criterion 3 says a historical check must
// be re-runnable, and that is a claim about the exact bytes stored — a condition normalised at
// evaluation time rather than at save time would be re-normalised by whatever the code does next
// year, and the answer would quietly change.
func (c Condition) Canonical() Condition {
	out := Condition{Subject: c.Subject.canonical()}
	for _, p := range c.When {
		p.With = p.With.canonical()
		p.DiagnosisCodes = tidy(p.DiagnosisCodes, strings.ToUpper)
		p.States = tidy(p.States, strings.ToUpper)
		p.AllergenGroup = strings.ToUpper(strings.TrimSpace(p.AllergenGroup))
		p.Unit = strings.TrimSpace(p.Unit)
		out.When = append(out.When, p)
	}
	// The predicates themselves keep the author's order. It is the order they read in, the
	// order the plain-language sentence uses, and sorting it would rewrite his sentence.
	return out
}

func (t Target) canonical() Target {
	return Target{
		Match:    t.Match,
		Generics: tidy(t.Generics, strings.ToLower),
		Classes:  tidy(t.Classes, strings.ToUpper),
	}
}

func tidy(in []string, norm func(string) string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = norm(strings.TrimSpace(s))
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// MarshalCondition renders a condition as the bytes that go in the column.
func MarshalCondition(c Condition) ([]byte, error) { return json.Marshal(c.Canonical()) }

// UnmarshalCondition reads them back. Deliberately strict: an unknown field in a stored
// condition means the model changed under a rule that was published against the old one, and
// evaluating it as though the missing part did not exist is exactly the silent wrong answer this
// whole module is arranged to avoid.
func UnmarshalCondition(raw []byte) (Condition, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var c Condition
	if err := dec.Decode(&c); err != nil {
		return Condition{}, fmt.Errorf("%w: %s", ErrInvalidRule, err)
	}
	return c, nil
}

// ---------------------------------------------------------------------------
// The rule and its versions
// ---------------------------------------------------------------------------

// Origin says where a version's clinical content came from.
type Origin string

const (
	// OriginSeed — drafted from published guidance and shipped with the migration. Never
	// approved by anybody at this clinic until somebody approves it, and inert until then.
	OriginSeed Origin = "SEED"
	// OriginAuthored — written here, on the form.
	OriginAuthored Origin = "AUTHORED"
	// OriginImported — arrived through the import endpoint, as a draft.
	OriginImported Origin = "IMPORTED"
)

// Status is where a version is in its life.
type Status string

const (
	// StatusDraft — being written, or seeded and not yet agreed. **Never evaluated.**
	StatusDraft Status = "DRAFT"
	// StatusPublished — approved, live, and the one a check uses now.
	StatusPublished Status = "PUBLISHED"
	// StatusSuperseded — was live, then a later version replaced it. Kept, and still used to
	// reproduce a check from the period it covered.
	StatusSuperseded Status = "SUPERSEDED"
	// StatusWithdrawn — was live, then withdrawn without a replacement. Same: kept, still
	// reproduces its own period.
	StatusWithdrawn Status = "WITHDRAWN"
)

// Rule is the identity that outlives its versions.
type Rule struct {
	ID         uuid.UUID `json:"id"`
	FacilityID uuid.UUID `json:"-"`
	// Code is the stable handle a finding cites and a person quotes. Uppercase, unique in a
	// facility, and never reused.
	Code string   `json:"code"`
	Type RuleType `json:"type"`

	IsActive        bool       `json:"is_active"`
	WithdrawnAt     *time.Time `json:"withdrawn_at,omitempty"`
	WithdrawnReason string     `json:"withdrawn_reason,omitempty"`

	// LatestVersion and PublishedVersion are what the list screen needs to say, in one row,
	// whether this rule is live and whether there is unpublished work on it.
	LatestVersion    int  `json:"latest_version"`
	PublishedVersion *int `json:"published_version"`

	CreatedAt time.Time `json:"created_at"`
}

// Version is one frozen statement of a rule.
type Version struct {
	ID      uuid.UUID `json:"id"`
	RuleID  uuid.UUID `json:"rule_id"`
	Version int       `json:"version"`

	Severity Severity `json:"severity"`

	// NameEN and NameBN are the short label — what the list shows and what a finding is filed
	// under. MessageEN and MessageBN are what the physician reads when it fires.
	NameEN    string `json:"name_en"`
	NameBN    string `json:"name_bn"`
	MessageEN string `json:"message_en"`
	MessageBN string `json:"message_bn"`
	// AdviceEN and AdviceBN are optional and are what to do instead. Optional because "avoid"
	// is sometimes the whole of it, and a form that demanded advice would get "n/a".
	AdviceEN string `json:"advice_en,omitempty"`
	AdviceBN string `json:"advice_bn,omitempty"`

	Condition Condition `json:"condition"`

	// Source is where the clinical content came from. **Required.** A rule that blocks a
	// prescription with nothing behind it is a rule nobody can argue with or update when the
	// guidance changes, and every one of the seeded thirty carries its citation.
	Source string `json:"source"`
	Origin Origin `json:"origin"`
	Status Status `json:"status"`

	AuthoredBy   *uuid.UUID `json:"authored_by,omitempty"`
	AuthoredCode string     `json:"authored_by_code,omitempty"`
	AuthoredAt   time.Time  `json:"authored_at"`

	// ApprovedAt is the whole of "this is Dr. Nahid's rule rather than a suggestion". Null on
	// every seeded row, and the database will not let a version be PUBLISHED without it.
	ApprovedBy   *uuid.UUID `json:"approved_by,omitempty"`
	ApprovedCode string     `json:"approved_by_code,omitempty"`
	ApprovedAt   *time.Time `json:"approved_at"`

	// EffectiveFrom and EffectiveTo are the period this version was the live one. Half-open,
	// exactly like a price: a check run at an instant inside the period must reproduce against
	// this version forever. Null on a draft, because a draft was never live.
	EffectiveFrom *time.Time `json:"effective_from"`
	EffectiveTo   *time.Time `json:"effective_to,omitempty"`

	Notes string `json:"notes,omitempty"`
}

// Approved reports whether a physician has put their name to this version.
//
// The one predicate the rest of the system asks. A version that is not approved has no effective
// period, so it cannot be in a [Ruleset] — this is the belt to that braces, and the test that
// proves a seeded rule cannot fire deletes this check to make sure it is load-bearing.
func (v Version) Approved() bool { return v.ApprovedAt != nil && v.ApprovedBy != nil }

// Live reports whether this version was the live one at an instant.
func (v Version) Live(at time.Time) bool {
	if !v.Approved() || v.EffectiveFrom == nil {
		return false
	}
	if at.Before(*v.EffectiveFrom) {
		return false
	}
	return v.EffectiveTo == nil || at.Before(*v.EffectiveTo)
}

// Validate checks a version is sayable before it is stored.
func (v Version) Validate(vocab Vocabulary, t RuleType) error {
	if strings.TrimSpace(v.NameEN) == "" || strings.TrimSpace(v.NameBN) == "" {
		return fmt.Errorf("%w: the rule needs a short name in English and in Bangla", ErrNotBilingual)
	}
	if strings.TrimSpace(v.MessageEN) == "" || strings.TrimSpace(v.MessageBN) == "" {
		return fmt.Errorf("%w: the message the physician will read has to be in both languages",
			ErrNotBilingual)
	}
	if (strings.TrimSpace(v.AdviceEN) == "") != (strings.TrimSpace(v.AdviceBN) == "") {
		return fmt.Errorf("%w: the advice is written in one language only", ErrNotBilingual)
	}
	switch v.Severity {
	case SeverityBlock, SeverityWarn, SeverityInfo:
	default:
		return fmt.Errorf("%w: %q is not a severity", ErrInvalidRule, v.Severity)
	}
	if strings.TrimSpace(v.Source) == "" {
		return fmt.Errorf("%w: say where this rule comes from — a guideline, a label, a "+
			"textbook. A rule with nothing behind it cannot be argued with or updated",
			ErrInvalidRule)
	}
	return v.Condition.Validate(t, vocab)
}
