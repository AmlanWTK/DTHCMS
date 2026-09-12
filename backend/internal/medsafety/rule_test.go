package medsafety

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The rule model, its validation, its plain language and its evaluation (CP77).
//
// Everything here runs without a database. The tests that need the real forty seeded rules, the
// real approval workflow and the real audit trail are in medsafety_db_test.go.

func vocab() Vocabulary {
	return Vocabulary{
		Generics: map[string]string{
			"metformin hydrochloride": "Metformin hydrochloride",
			"pioglitazone":            "Pioglitazone",
			"glimepiride":             "Glimepiride",
			"levothyroxine sodium":    "Levothyroxine sodium",
		},
		Classes: map[string]bool{
			"BIGUANIDE": true, "ACE_INHIBITOR": true, "STATIN": true,
			"ANGIOTENSIN_II_RECEPTOR_BLOCKER": true, "THIAZOLIDINEDIONE": true,
		},
		AllergenGroups: map[string]bool{"PENICILLIN": true, "SULFONAMIDE_ANTIBIOTIC": true},
	}
}

func renalRule() Version {
	return Version{
		Severity: SeverityBlock,
		NameEN:   "Metformin below eGFR 30", NameBN: "eGFR 30-এর নিচে মেটফরমিন",
		MessageEN: "Metformin is contraindicated below an eGFR of 30.",
		MessageBN: "eGFR 30-এর নিচে মেটফরমিন দেওয়া যাবে না।",
		Source:    "ADA Standards of Care 2025 s11.",
		Condition: Condition{
			Subject: Target{Match: MatchGeneric, Generics: []string{"metformin hydrochloride"}},
			When: []Predicate{
				{Kind: PredEGFR, Operator: OpLessThan, Value: 30, Unit: "mL/min/1.73m2"},
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

func TestARuleNeedsBothLanguagesAndASeverityAndASource(t *testing.T) {
	// Acceptance criterion 2, at the model. Each case drops one thing a rule cannot be without,
	// and every one of them must be refused — an interface can be persuaded to skip a field;
	// this cannot.
	for _, tc := range []struct {
		name   string
		change func(*Version)
	}{
		{"no English message", func(v *Version) { v.MessageEN = "" }},
		{"no Bengali message", func(v *Version) { v.MessageBN = "" }},
		{"no English name", func(v *Version) { v.NameEN = "" }},
		{"no Bengali name", func(v *Version) { v.NameBN = "" }},
		{"advice in one language only", func(v *Version) { v.AdviceEN = "Stop it." }},
		{"no severity", func(v *Version) { v.Severity = "" }},
		{"an invented severity", func(v *Version) { v.Severity = "CRITICAL" }},
		{"no source", func(v *Version) { v.Source = "  " }},
	} {
		v := renalRule()
		tc.change(&v)
		if err := v.Validate(vocab(), TypeRenal); err == nil {
			t.Errorf("a rule with %s was accepted", tc.name)
		}
	}

	// And the whole rule, unchanged, is accepted — so the test above is about the changes and
	// not about the fixture being broken.
	if err := renalRule().Validate(vocab(), TypeRenal); err != nil {
		t.Fatalf("a complete rule was refused: %v", err)
	}
}

func TestARuleCannotNameAMoleculeThisClinicDoesNotHave(t *testing.T) {
	// The failure this prevents is silent: a rule naming "Metformin HCl" instead of "Metformin
	// hydrochloride" never fires, and nothing anywhere says so.
	v := renalRule()
	v.Condition.Subject.Generics = []string{"Metformin HCl"}
	err := v.Validate(vocab(), TypeRenal)
	if err == nil {
		t.Fatal("a rule naming a molecule the formulary does not have was accepted")
	}
	if !strings.Contains(err.Error(), "Metformin HCl") {
		t.Errorf("the error does not name the molecule it refused: %v", err)
	}
}

func TestARuleCannotTestSomethingItsKindCannotTest(t *testing.T) {
	// A renal rule with a pregnancy test in it is a rule somebody put on the wrong form. The
	// error names where the thing he wants does belong, because a validator that only says "no"
	// is one people argue with.
	v := renalRule()
	v.Condition.When = []Predicate{{Kind: PredPregnancy, States: []string{"PREGNANT"}}}
	err := v.Validate(vocab(), TypeRenal)
	if err == nil {
		t.Fatal("a renal rule testing pregnancy was accepted")
	}
	if !strings.Contains(err.Error(), string(TypePregnancy)) {
		t.Errorf("the error does not say where a pregnancy test belongs: %v", err)
	}
}

func TestARuleWithNoConditionsIsRefused(t *testing.T) {
	// It would fire on every prescription of the medicine, which is not a safety rule, it is a
	// pop-up.
	v := renalRule()
	v.Condition.When = nil
	if err := v.Validate(vocab(), TypeRenal); err == nil {
		t.Fatal("a rule with no conditions was accepted")
	}
}

func TestOnlyADuplicateTherapyRuleMayBeAboutAnyMedicine(t *testing.T) {
	// "Any medicine" is a rule somebody meant to narrow and did not, except for the one kind
	// that is genuinely about the prescription rather than about a drug.
	dup := Version{
		Severity: SeverityWarn, NameEN: "Twice", NameBN: "দুবার",
		MessageEN: "The same molecule twice.", MessageBN: "একই অণু দুবার।",
		Source: "Drafted here.",
		Condition: Condition{
			Subject: Target{Match: MatchAny},
			When:    []Predicate{{Kind: PredDuplicate, With: Target{Match: MatchGeneric}}},
		},
	}
	if err := dup.Validate(vocab(), TypeDuplicateTherapy); err != nil {
		t.Errorf("a duplicate-therapy rule about any medicine was refused: %v", err)
	}

	wide := renalRule()
	wide.Condition.Subject = Target{Match: MatchAny}
	if err := wide.Validate(vocab(), TypeRenal); err == nil {
		t.Error("a renal rule about every medicine in the formulary was accepted")
	}
}

func TestACanonicalConditionIsStableWhateverOrderItWasTypedIn(t *testing.T) {
	// Reproducibility rests on the exact bytes stored. A condition normalised at evaluation
	// time rather than at save time would be re-normalised by whatever the code does next year,
	// and the answer would quietly change.
	a := Condition{
		Subject: Target{Match: MatchGeneric, Generics: []string{"Pioglitazone", "metformin hydrochloride"}},
		When:    []Predicate{{Kind: PredDiagnosis, DiagnosisCodes: []string{"i50.9", "E11.9"}}},
	}
	b := Condition{
		Subject: Target{Match: MatchGeneric, Generics: []string{"METFORMIN HYDROCHLORIDE", " Pioglitazone "}},
		When:    []Predicate{{Kind: PredDiagnosis, DiagnosisCodes: []string{"E11.9", "I50.9", "E11.9"}}},
	}
	first, err := MarshalCondition(a)
	if err != nil {
		t.Fatal(err)
	}
	second, err := MarshalCondition(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("the same rule stored two ways produced two documents:\n  %s\n  %s", first, second)
	}
	// And canonicalising twice changes nothing, so a re-save does not produce a spurious diff.
	third, err := MarshalCondition(a.Canonical())
	if err != nil {
		t.Fatal(err)
	}
	if string(third) != string(first) {
		t.Error("canonicalising twice is not the same as canonicalising once")
	}
}

func TestAStoredConditionThisBuildCannotReadIsRefused(t *testing.T) {
	// A published rule whose unknown half is silently ignored is the quiet wrong answer this
	// whole module is arranged to avoid. The strictness is the point.
	raw := []byte(`{"subject":{"match":"GENERIC","generics":["metformin hydrochloride"]},
	                "when":[{"kind":"EGFR","operator":"LT","value":30,"unit":"x"}],
	                "unless":[{"kind":"SOMETHING_FROM_CP90"}]}`)
	if _, err := UnmarshalCondition(raw); err == nil {
		t.Fatal("a condition carrying a clause this build does not understand was read anyway")
	}
}

// ---------------------------------------------------------------------------
// Plain language
// ---------------------------------------------------------------------------

func TestTheRuleIsSaidBackInBothLanguagesWithItsConsequence(t *testing.T) {
	plain := renalRule().Explain()
	for _, want := range []string{"Metformin hydrochloride", "eGFR", "below 30", "stop the prescription"} {
		if !strings.Contains(plain.EN, want) {
			t.Errorf("the English preview does not mention %q: %s", want, plain.EN)
		}
	}
	if strings.TrimSpace(plain.BN) == "" {
		t.Fatal("there is no Bengali preview")
	}
	// The Bengali has to be Bengali, not the English with a full stop changed.
	if !strings.ContainsFunc(plain.BN, func(r rune) bool { return r >= 0x0980 && r <= 0x09FF }) {
		t.Errorf("the Bengali preview has no Bengali in it: %s", plain.BN)
	}
	// Drug names stay in Latin script in both.
	if !strings.Contains(plain.BN, "Metformin hydrochloride") {
		t.Errorf("the molecule was transliterated in the Bengali preview: %s", plain.BN)
	}
	// And the list around them is joined the way Bengali joins a list.
	twoDrugs := renalRule()
	twoDrugs.Condition.Subject.Generics = []string{"metformin hydrochloride", "pioglitazone"}
	if bn := twoDrugs.Explain().BN; strings.Contains(bn, " and ") {
		t.Errorf("two molecules are joined by the English conjunction in Bengali: %s", bn)
	}
	// The severity is stated as what will happen, not as its label. "BLOCK" is a word somebody
	// has to be taught; "stop the prescription" is what the author is choosing.
	if strings.Contains(plain.EN, "BLOCK") {
		t.Errorf("the preview says BLOCK rather than what BLOCK does: %s", plain.EN)
	}
	if len(plain.NeedsEN) != 1 || !strings.Contains(plain.NeedsEN[0], "eGFR") {
		t.Errorf("the preview does not warn that this rule needs an eGFR: %v", plain.NeedsEN)
	}
}

func TestEveryKindOfRuleCanBeSaidInBothLanguages(t *testing.T) {
	// A rule type whose preview came out empty in one language would be a rule a physician
	// could author and could not read back — and it would only be noticed by whoever wrote the
	// first one of that kind.
	cases := map[RuleType]Condition{
		TypeInteraction: {Subject: Target{Match: MatchClass, Classes: []string{"ACE_INHIBITOR"}},
			When: []Predicate{{Kind: PredCoPrescribed, CurrentMedications: true,
				With: Target{Match: MatchClass, Classes: []string{"ANGIOTENSIN_II_RECEPTOR_BLOCKER"}}}}},
		TypeContraindication: {Subject: Target{Match: MatchGeneric, Generics: []string{"pioglitazone"}},
			When: []Predicate{{Kind: PredDiagnosis, DiagnosisCodes: []string{"I50.9"}}}},
		TypeRenal:   renalRule().Condition,
		TypeHepatic: {Subject: Target{Match: MatchClass, Classes: []string{"STATIN"}}, When: []Predicate{{Kind: PredHepatic, States: []string{"SEVERE"}}}},
		TypePregnancy: {Subject: Target{Match: MatchClass, Classes: []string{"ACE_INHIBITOR"}},
			When: []Predicate{{Kind: PredPregnancy, States: []string{"PREGNANT"}}}},
		TypePaediatric: {Subject: Target{Match: MatchGeneric, Generics: []string{"pioglitazone"}},
			When: []Predicate{{Kind: PredAge, Operator: OpLessThan, Value: 18, Unit: "years"}}},
		TypeDuplicateTherapy: {Subject: Target{Match: MatchAny},
			When: []Predicate{{Kind: PredDuplicate, With: Target{Match: MatchClass}}}},
		TypeMaxDose: {Subject: Target{Match: MatchGeneric, Generics: []string{"glimepiride"}},
			When: []Predicate{{Kind: PredDailyDose, Operator: OpGreaterThan, Value: 8, Unit: "mg"}}},
	}
	if len(cases) != len(AllRuleTypes) {
		t.Fatalf("this test covers %d of the %d rule types", len(cases), len(AllRuleTypes))
	}
	for typ, condition := range cases {
		v := renalRule()
		v.Condition = condition
		if err := v.Validate(vocab(), typ); err != nil {
			t.Errorf("%s: the fixture is not a valid rule: %v", typ, err)
			continue
		}
		plain := v.Explain()
		if strings.TrimSpace(plain.EN) == "" || strings.TrimSpace(plain.BN) == "" {
			t.Errorf("%s has an empty preview: %q / %q", typ, plain.EN, plain.BN)
		}
		if !strings.ContainsFunc(plain.BN, func(r rune) bool { return r >= 0x0980 && r <= 0x09FF }) {
			t.Errorf("%s: the Bengali preview has no Bengali in it: %s", typ, plain.BN)
		}
		if strings.Contains(plain.EN, string(PredCoPrescribed)) ||
			strings.Contains(plain.EN, string(PredDailyDose)) {
			t.Errorf("%s: the preview fell through to a raw predicate name: %s", typ, plain.EN)
		}
		// The Bengali is a Bengali sentence, not the English one with some words swapped. An
		// English conjunction inside it is the specific way this goes wrong: a list joined by
		// the English " and " reads as a translation somebody abandoned halfway.
		for _, leak := range []string{" and ", " or ", " is prescribed", "the patient"} {
			if strings.Contains(plain.BN, leak) {
				t.Errorf("%s: the Bengali preview carries the English %q: %s", typ, leak, plain.BN)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Evaluation, and the third truth value
// ---------------------------------------------------------------------------

func metformin() Drug {
	return Drug{Label: "Comet 500 mg", Generic: "Metformin hydrochloride", Class: "BIGUANIDE"}
}

func egfr(v float64) *float64 { return &v }

func TestARuleFiresWhenItsConditionsAreMet(t *testing.T) {
	f := renalRule().Evaluate(Rule{Code: "MET-RENAL-30", Type: TypeRenal},
		Context{EGFR: egfr(25), Proposed: []Drug{metformin()}})
	if f.Outcome != OutcomeFires {
		t.Fatalf("metformin at eGFR 25 produced %s, want FIRES", f.Outcome)
	}
	if f.Severity != SeverityBlock {
		t.Errorf("the finding is %s", f.Severity)
	}
	if f.Subject != "Comet 500 mg" {
		t.Errorf("the finding names %q rather than the medicine the physician typed", f.Subject)
	}
	if len(f.Steps) != 1 || f.Steps[0].Truth != "HOLDS" {
		t.Errorf("the working is %+v", f.Steps)
	}
	if !strings.Contains(f.Steps[0].BecauseEN, "25") {
		t.Errorf("the working does not say what the eGFR actually was: %s", f.Steps[0].BecauseEN)
	}
}

func TestARuleDoesNotFireWhenTheDataSayNo(t *testing.T) {
	f := renalRule().Evaluate(Rule{Code: "MET-RENAL-30", Type: TypeRenal},
		Context{EGFR: egfr(72), Proposed: []Drug{metformin()}})
	if f.Outcome != OutcomeDoesNotFire {
		t.Fatalf("metformin at eGFR 72 produced %s, want DOES_NOT_FIRE", f.Outcome)
	}
	if len(f.Missing) != 0 {
		t.Errorf("a rule with all its data reported %v as missing", f.Missing)
	}
}

func TestAbsentDataProducesCannotVerifyAndNeverSilence(t *testing.T) {
	// **§7.2's fail-closed behaviour, at the model.** Silence and "safe" are the same pixel on
	// a screen and only one of them is true.
	f := renalRule().Evaluate(Rule{Code: "MET-RENAL-30", Type: TypeRenal},
		Context{Proposed: []Drug{metformin()}})
	if f.Outcome != OutcomeCannotVerify {
		t.Fatalf("metformin with no eGFR on file produced %s, want CANNOT_VERIFY", f.Outcome)
	}
	if len(f.Missing) != 1 || f.Missing[0] != DatumEGFR {
		t.Errorf("the finding does not name what is missing: %v", f.Missing)
	}
	if !strings.Contains(f.Steps[0].BecauseEN, "no eGFR") {
		t.Errorf("the working does not explain the gap: %s", f.Steps[0].BecauseEN)
	}
}

func TestADefiniteNoBeatsAnUnknown(t *testing.T) {
	// The ordering inside an AND, and the reason it is worth a test: getting it backwards
	// produces "cannot verify" on every prescription, which is how fail-closed turns into noise
	// and then into nobody reading it.
	//
	// Pioglitazone in a patient with no diagnosis list and an age of 40 — the paediatric test
	// definitely does not hold, so the rule does not apply, whatever else is unknown.
	v := renalRule()
	v.Condition = Condition{
		Subject: Target{Match: MatchGeneric, Generics: []string{"pioglitazone"}},
		When: []Predicate{
			{Kind: PredDiagnosis, DiagnosisCodes: []string{"I50.9"}}, // unknown: nil list
			{Kind: PredAge, Operator: OpLessThan, Value: 18, Unit: "years"},
		},
	}
	age := 40.0
	f := v.Evaluate(Rule{Code: "X", Type: TypeContraindication}, Context{
		AgeYears: &age,
		Proposed: []Drug{{Label: "Pidus", Generic: "Pioglitazone", Class: "THIAZOLIDINEDIONE"}},
	})
	if f.Outcome != OutcomeDoesNotFire {
		t.Fatalf("a rule with one test that definitely fails produced %s, want DOES_NOT_FIRE", f.Outcome)
	}
}

func TestAnEmptyListIsNotTheSameAsNoListAtAll(t *testing.T) {
	// "We asked, and there are none" and "nobody asked" are different clinical facts, and only
	// one of them is safe to treat as an answer.
	v := renalRule()
	v.Condition = Condition{
		Subject: Target{Match: MatchGeneric, Generics: []string{"pioglitazone"}},
		When:    []Predicate{{Kind: PredDiagnosis, DiagnosisCodes: []string{"I50.9"}}},
	}
	drug := Drug{Label: "Pidus", Generic: "Pioglitazone", Class: "THIAZOLIDINEDIONE"}

	unknown := v.Evaluate(Rule{Code: "X", Type: TypeContraindication},
		Context{Diagnoses: nil, Proposed: []Drug{drug}})
	if unknown.Outcome != OutcomeCannotVerify {
		t.Errorf("an unread diagnosis list produced %s, want CANNOT_VERIFY", unknown.Outcome)
	}

	asked := v.Evaluate(Rule{Code: "X", Type: TypeContraindication},
		Context{Diagnoses: []string{}, Proposed: []Drug{drug}})
	if asked.Outcome != OutcomeDoesNotFire {
		t.Errorf("a read-and-empty diagnosis list produced %s, want DOES_NOT_FIRE", asked.Outcome)
	}
}

func TestARuleAboutAMedicineNotBeingPrescribedIsNotApplicable(t *testing.T) {
	f := renalRule().Evaluate(Rule{Code: "MET-RENAL-30", Type: TypeRenal}, Context{
		EGFR:     egfr(20),
		Proposed: []Drug{{Label: "Thyrox 50", Generic: "Levothyroxine sodium", Class: "THYROID_HORMONE"}},
	})
	if f.Outcome != OutcomeNotApplicable {
		t.Errorf("a metformin rule met a levothyroxine prescription and said %s", f.Outcome)
	}
}

func TestAMaxDoseRuleWillNotConvertBetweenUnits(t *testing.T) {
	// A rule written in mg meeting a dose in mcg is a thousandfold error waiting to be made by
	// whoever writes the conversion. It answers "cannot verify" instead.
	v := renalRule()
	v.Condition = Condition{
		Subject: Target{Match: MatchGeneric, Generics: []string{"levothyroxine sodium"}},
		When:    []Predicate{{Kind: PredDailyDose, Operator: OpGreaterThan, Value: 300, Unit: "mcg"}},
	}
	dose := 0.5
	f := v.Evaluate(Rule{Code: "LEVO-MAXDOSE", Type: TypeMaxDose}, Context{
		Proposed: []Drug{{Label: "Thyrox", Generic: "Levothyroxine sodium",
			Class: "THYROID_HORMONE", DailyDose: &dose, DoseUnit: "mg"}},
	})
	if f.Outcome != OutcomeCannotVerify {
		t.Fatalf("a dose in mg against a rule in mcg produced %s, want CANNOT_VERIFY", f.Outcome)
	}
	if !strings.Contains(f.Steps[0].BecauseEN, "mg") || !strings.Contains(f.Steps[0].BecauseEN, "mcg") {
		t.Errorf("the working does not name both units: %s", f.Steps[0].BecauseEN)
	}
}

func TestDuplicateTherapyNeedsTwoOfTheThing(t *testing.T) {
	v := Version{
		Severity: SeverityWarn, NameEN: "Twice", NameBN: "দুবার",
		MessageEN: "Same molecule twice.", MessageBN: "একই অণু দুবার।",
		Source: "Drafted here.",
		Condition: Condition{
			Subject: Target{Match: MatchAny},
			When:    []Predicate{{Kind: PredDuplicate, With: Target{Match: MatchGeneric}}},
		},
	}
	rule := Rule{Code: "DUP-GENERIC", Type: TypeDuplicateTherapy}

	one := v.Evaluate(rule, Context{Proposed: []Drug{metformin()}})
	if one.Outcome != OutcomeDoesNotFire {
		t.Errorf("one metformin was reported as a duplicate: %s", one.Outcome)
	}
	two := v.Evaluate(rule, Context{Proposed: []Drug{metformin(),
		{Label: "Bigmet 850", Generic: "Metformin hydrochloride", Class: "BIGUANIDE"}}})
	if two.Outcome != OutcomeFires {
		t.Errorf("two metformins produced %s, want FIRES", two.Outcome)
	}
	if !strings.Contains(two.Steps[0].BecauseEN, "Comet 500 mg") ||
		!strings.Contains(two.Steps[0].BecauseEN, "Bigmet 850") {
		t.Errorf("the working does not name both: %s", two.Steps[0].BecauseEN)
	}
}

// ---------------------------------------------------------------------------
// The guarantee
// ---------------------------------------------------------------------------

func TestAnUnapprovedRuleCannotFire(t *testing.T) {
	// **The property this whole checkpoint is built around, at the Go layer.**
	//
	// The database makes it structurally impossible for an unapproved version to be in a
	// ruleset — it has no effective period, so `RulesetAt` cannot return it — and
	// medsafety_db_test.go proves that against the real forty seeded rules. This is the second
	// lock: a Ruleset assembled by hand with an unapproved version in it must still produce
	// nothing.
	//
	// **Delete the `!v.Approved()` guard in `Ruleset.Findings` and this test goes red.** That is
	// the check, and it is why the guard is not "redundant with the database".
	unapproved := renalRule()
	unapproved.Status = StatusDraft
	unapproved.Origin = OriginSeed

	rs := Ruleset{
		At:       time.Now(),
		Rules:    []Rule{{ID: uuid.New(), Code: "MET-RENAL-30", Type: TypeRenal}},
		Versions: []Version{unapproved},
	}
	// A picture in which this rule would certainly fire if it could.
	dying := Context{EGFR: egfr(18), Proposed: []Drug{metformin()}}

	if findings := rs.Findings(dying); len(findings) != 0 {
		t.Fatalf("an unapproved rule produced %d findings: %+v", len(findings), findings)
	}

	// And the same rule, approved, does fire — so the test above is about the approval and not
	// about the fixture being unable to fire at all.
	approved := unapproved
	at := time.Now().Add(-time.Hour)
	who := uuid.New()
	approved.Status = StatusPublished
	approved.ApprovedAt, approved.ApprovedBy = &at, &who
	approved.EffectiveFrom = &at
	rs.Versions = []Version{approved}

	findings := rs.Findings(dying)
	if len(findings) != 1 || findings[0].Outcome != OutcomeFires {
		t.Fatalf("the same rule, approved, produced %+v", findings)
	}
}

func TestFindingsAreOrderedWithTheBlocksFirst(t *testing.T) {
	// A physician reads the top of this list and stops.
	at := time.Now().Add(-time.Hour)
	who := uuid.New()
	live := func(v Version) Version {
		v.Status = StatusPublished
		v.ApprovedAt, v.ApprovedBy, v.EffectiveFrom = &at, &who, &at
		return v
	}
	block := live(renalRule())
	info := live(renalRule())
	info.Severity = SeverityInfo
	warn := live(renalRule())
	warn.Severity = SeverityWarn

	rs := Ruleset{
		At: time.Now(),
		Rules: []Rule{
			{Code: "C-INFO", Type: TypeRenal}, {Code: "B-BLOCK", Type: TypeRenal},
			{Code: "A-WARN", Type: TypeRenal},
		},
		Versions: []Version{info, block, warn},
	}
	got := rs.Findings(Context{EGFR: egfr(20), Proposed: []Drug{metformin()}})
	want := []Severity{SeverityBlock, SeverityWarn, SeverityInfo}
	if len(got) != 3 {
		t.Fatalf("got %d findings", len(got))
	}
	for i, f := range got {
		if f.Severity != want[i] {
			t.Errorf("position %d is %s, want %s", i, f.Severity, want[i])
		}
	}
}

func TestAFindingSerialisesEverythingTheScreenNeeds(t *testing.T) {
	// The sandbox reads this over the wire. A field renamed here is a blank panel there.
	f := renalRule().Evaluate(Rule{Code: "MET-RENAL-30", Type: TypeRenal},
		Context{Proposed: []Drug{metformin()}})
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		`"outcome":"CANNOT_VERIFY"`, `"severity":"BLOCK"`, `"message_bn"`,
		`"missing":["EGFR"]`, `"because_bn"`, `"source"`,
	} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("the serialised finding has no %s:\n%s", key, raw)
		}
	}
}
