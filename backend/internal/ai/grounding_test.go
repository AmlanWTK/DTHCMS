package ai_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/AmlanWTK/DTHCMS/backend/internal/ai"
	"github.com/AmlanWTK/DTHCMS/backend/internal/ai/evalset"
)

// The grounding check, without a database and without a model (CP72).
//
// Everything here is a pure function of two values, which is the property the whole design was
// arranged for: `Assemble` produces the context, the context is stored, and the check compares two
// documents. A test of it needs neither a clinic nor a credential.
//
// The two acceptance criteria this file is answerable for are the first two, and they pull in
// opposite directions on purpose. Criterion 1 is "every injected hallucination is detected" and is
// satisfied by a check that refuses everything; criterion 2 is "the false-positive rate is low
// enough not to block legitimate summaries" and is satisfied by a check that refuses nothing. Only
// the pair is worth anything, and the pair is measured over the same frozen corpus.

// context is a payload in the shape `internal/synthesis` produces, small enough to read.
func groundingContext() map[string]any {
	return map[string]any{
		"assembler_version": "1.0.0",
		"demographics":      map[string]any{"age": "57 years", "sex": "female"},
		"visit": map[string]any{
			"clinic_day": "2026-09-08",
			"stations": []any{
				map[string]any{"code": "STN_VITALS", "status": "done"},
				map[string]any{"code": "STN_EXAMINATION", "status": "done"},
			},
		},
		"current_measurements": []any{
			map[string]any{"code": "HBA1C", "label": "HbA1c", "value": "8.2", "unit": "%",
				"ref": "obs.hba1c:2026-09-08"},
			map[string]any{"code": "BODY_WEIGHT", "label": "Weight", "value": "63.6", "unit": "kg",
				"ref": "obs.body_weight:2026-09-08"},
		},
		"facts": []any{
			map[string]any{"ref": "obs.hba1c:2026-09-08", "kind": "obs", "label": "HbA1c",
				"value": "8.2", "unit": "%", "on": "2026-09-08"},
			map[string]any{"ref": "obs.body_weight:2026-09-08", "kind": "obs", "label": "Weight",
				"value": "63.6", "unit": "kg", "on": "2026-09-08"},
		},
	}
}

func check(t *testing.T, output map[string]any, drugs ai.DrugCheck) ai.GroundingReport {
	t.Helper()
	return ai.GroundsFrom(groundingContext()).Check(output, drugs)
}

// TestAnAnswerThatQuotesTheContextPasses is the control.
//
// Without it every other test in this file would be satisfied by a check that refuses everything,
// which is the failure mode a detection-rate test cannot see.
func TestAnAnswerThatQuotesTheContextPasses(t *testing.T) {
	report := check(t, map[string]any{
		"narrative_en": "HbA1c is 8.2 % on 2026-09-08 [obs.hba1c:2026-09-08] and weight is 63.6 kg " +
			"[obs.body_weight:2026-09-08]. Both stations before the consultation are complete.",
		"citations":  []any{"obs.hba1c:2026-09-08", "obs.body_weight:2026-09-08"},
		"confidence": 0.8,
	}, nil)

	if !report.OK() {
		t.Fatalf("a correct answer was refused: %s", report.Summary())
	}
	// The denominators. A verdict of PASSED from a check that examined nothing is the failure this
	// whole mechanism is most likely to have, and it is indistinguishable from success unless the
	// report says how much it looked at.
	if report.Numbers < 3 || report.Dates < 1 || report.Citations < 3 {
		t.Errorf("the check examined %d numbers, %d dates and %d citations; it is not looking at the answer",
			report.Numbers, report.Dates, report.Citations)
	}
}

// TestAFabricatedLaboratoryValueIsCaught is §10.2's own example.
func TestAFabricatedLaboratoryValueIsCaught(t *testing.T) {
	report := check(t, map[string]any{
		"narrative_en": "HbA1c is 12.4 % [obs.hba1c:2026-09-08], which is poor control.",
	}, nil)

	if report.OK() {
		t.Fatal("a fabricated HbA1c was not caught")
	}
	assertFinding(t, report, ai.ArmNumber, "12.4")
	if report.Findings[0].Path != "narrative_en" {
		t.Errorf("the finding points at %q, not at the field the claim is in", report.Findings[0].Path)
	}
	if report.Findings[0].Excerpt == "" || !strings.Contains(report.Findings[0].Excerpt, "12.4") {
		t.Errorf("the finding's excerpt %q does not show the reviewer the sentence", report.Findings[0].Excerpt)
	}
}

// TestAValueMovedToADateTheRecordDoesNotHaveIsCaught, in both the shapes a date is written.
//
// The prose form is the one a numeric scan alone would miss: "12 March 2019" contains no token that
// looks like a date to anything that only knows `YYYY-MM-DD`.
func TestAValueMovedToADateTheRecordDoesNotHaveIsCaught(t *testing.T) {
	for _, one := range []struct{ name, sentence, token string }{
		{"iso", "The reading was taken on 2019-04-02.", "2019-04-02"},
		{"prose", "The reading was taken on 12 March 2019.", "12 March 2019"},
		{"month and year", "The reading was taken in March 2019.", "March 2019"},
	} {
		t.Run(one.name, func(t *testing.T) {
			report := check(t, map[string]any{"narrative_en": one.sentence}, nil)
			if report.OK() {
				t.Fatalf("a fabricated date written %q was not caught", one.sentence)
			}
			assertFinding(t, report, ai.ArmDate, one.token)
		})
	}
}

// TestACitationThatIsNotInTheFactIndexIsCaught, in both the places a model writes one.
func TestACitationThatIsNotInTheFactIndexIsCaught(t *testing.T) {
	t.Run("in the citations array", func(t *testing.T) {
		report := check(t, map[string]any{
			"narrative_en": "The thyroid function is abnormal.",
			"citations":    []any{"obs.tsh:2026-01-05"},
		}, nil)
		if report.OK() {
			t.Fatal("a citation naming a fact the model was never shown was not caught")
		}
		assertFinding(t, report, ai.ArmCitation, "obs.tsh:2026-01-05")
	})

	t.Run("in prose", func(t *testing.T) {
		report := check(t, map[string]any{
			"narrative_en": "Weight is 63.6 kg [obs.body_weight:2025-09-08].",
		}, nil)
		if report.OK() {
			t.Fatal("a bracketed citation whose date was altered was not caught")
		}
		assertFinding(t, report, ai.ArmCitation, "obs.body_weight:2025-09-08")
	})
}

// TestABracketThatIsNotACitationIsStillScannedForNumbers.
//
// The hole this is here to keep closed: bracketed citations are blanked out before the number arm
// runs, so that a reference's own date is not scanned twice. If the blanking applied to *every*
// bracket, a model could hide a fabricated value inside one — and the escape would be a square
// bracket wide.
func TestABracketThatIsNotACitationIsStillScannedForNumbers(t *testing.T) {
	report := check(t, map[string]any{
		"narrative_en": "The result was [HbA1c 12.9] at the last review.",
	}, nil)
	if report.OK() {
		t.Fatal("a fabricated value inside square brackets was not caught")
	}
	assertFinding(t, report, ai.ArmNumber, "12.9")
}

// TestCountingWhatTheModelWasShownIsNotAHallucination is criterion 2 in miniature.
//
// A model that says "both stations are complete" or "a further 7 measurements are on the record"
// has computed a number, and the number is not in the payload. It is also not an invention: it is a
// count of things the model was given. Flagging it would have blocked five of the twenty summaries
// in the frozen corpus — measured, not guessed — and a check that blocks a quarter of legitimate
// summaries is one the clinic turns off.
func TestCountingWhatTheModelWasShownIsNotAHallucination(t *testing.T) {
	for _, sentence := range []string{
		"Both stations before the consultation are complete; 2 stations were walked.",
		"A further 7 measurements are on the record and in the context beside this.",
		"1 of 2 pre-consultation stations is complete.",
	} {
		report := check(t, map[string]any{"narrative_en": sentence}, nil)
		if !report.OK() {
			t.Errorf("%q was refused: %s", sentence, report.Summary())
		}
	}
}

// TestACountIsOnlyACountWhenTheNounIsNextToIt.
//
// The other half of the rule above, and the one that decides whether it is safe. The window is four
// words and it stops at the first token carrying punctuation, so a measurement two clauses away
// from a collection noun is still checked. Without this the count rule would be an escape hatch:
// append "; 5 measurements" to any sentence and every number in it goes unexamined.
func TestACountIsOnlyACountWhenTheNounIsNextToIt(t *testing.T) {
	report := check(t, map[string]any{
		"narrative_en": "Pulse was 88 /min; 5 measurements are on the record.",
	}, nil)
	if report.OK() {
		t.Fatal("a fabricated pulse two clauses from a collection noun was treated as a count")
	}
	assertFinding(t, report, ai.ArmNumber, "88")
}

// TestTheDrugArmRefusesEveryNameUntilThereIsAFormulary.
//
// §10.4 A1: *"drug names validated against the formulary; an unrecognised drug name is dropped, not
// displayed."* There is no formulary — it is CP75 — so today every name is unrecognised. The arm is
// built and armed to refuse; what it is not is *stubbed*, because a stubbed drug list would certify
// as verified whatever somebody typed into it.
func TestTheDrugArmRefusesEveryNameUntilThereIsAFormulary(t *testing.T) {
	report := check(t, map[string]any{
		"draft_medications": []any{map[string]any{
			"drug": "Semaglutide", "rationale": "For weight and glycaemic control.",
		}},
	}, nil)

	if report.OK() {
		t.Fatal("a drafted medication was allowed through with no formulary to check it against")
	}
	assertFinding(t, report, ai.ArmDrug, "Semaglutide")
	if report.DrugArm != "UNARMED" {
		t.Errorf("the report says the drug arm is %q; it has nothing to check against", report.DrugArm)
	}
	if report.Drugs != 1 {
		t.Errorf("the check examined %d drug names, want 1", report.Drugs)
	}
}

// TestTheDrugArmPassesANameTheFormularyKnows is the test that will pass when CP75 lands.
//
// It is written now, against the seam rather than against a stub of the formulary's data, so that
// the day there is a `formulary` module the composition root wires it in and this test stops being
// a description of an intention. What it proves today is that the arm is *capable* of passing —
// that "everything is refused" is the absence of a formulary and not a hard-coded refusal.
func TestTheDrugArmPassesANameTheFormularyKnows(t *testing.T) {
	// Stands in for CP75's lookup. Deliberately not a list of real medicines: the point is the
	// seam, and a list here would be the beginning of the stub formulary this checkpoint refuses
	// to write.
	formulary := func(name string) bool { return name == "A_MEDICINE_THE_CLINIC_STOCKS" }

	known := check(t, map[string]any{
		"draft_medications": []any{map[string]any{"drug": "A_MEDICINE_THE_CLINIC_STOCKS"}},
	}, formulary)
	if !known.OK() {
		t.Fatalf("a formulary name was refused: %s", known.Summary())
	}
	if known.DrugArm != "ARMED" {
		t.Errorf("the report says the drug arm is %q with a formulary supplied", known.DrugArm)
	}

	unknown := check(t, map[string]any{
		"draft_medications": []any{map[string]any{"drug": "Notamedicine"}},
	}, formulary)
	if unknown.OK() {
		t.Fatal("a name the formulary does not have was allowed through")
	}
	assertFinding(t, unknown, ai.ArmDrug, "Notamedicine")
}

// TestAnExcerptNeverCarriesSomethingThePatternListWouldCatch.
//
// The defect table stores the sentence around a finding so that somebody can act on it a week
// later. That sentence comes from the model's answer, which is why it is checked here against the
// same pattern list the outbound payload is guarded by — and why the finding falls back to no
// excerpt at all rather than to a scrubbed one.
func TestAnExcerptNeverCarriesSomethingThePatternListWouldCatch(t *testing.T) {
	report := check(t, map[string]any{
		"narrative_en": "The attendant's number is 01711223344 and the value was 12.4 %.",
	}, nil)
	if report.OK() {
		t.Fatal("the fabricated value was not caught, so there is no excerpt to check")
	}
	patterns, err := ai.CompilePatterns(ai.DefaultPatterns)
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range report.Findings {
		for _, pattern := range patterns {
			if pattern.Matches(finding.Excerpt) {
				t.Errorf("the excerpt %q trips the %s pattern and would be refused by the column's own constraint",
					finding.Excerpt, pattern.Kind)
			}
		}
	}
}

// TestTheFindingsAreOrderedTheSameWayEveryTime.
//
// Map iteration in Go is randomised, so an unsorted report would produce a different defect row
// order and a different CI message on every run. A harness whose output moves on its own is one
// people stop reading.
func TestTheFindingsAreOrderedTheSameWayEveryTime(t *testing.T) {
	output := map[string]any{
		// Two numbers in one string, in an order the sort reverses: 9.9 is written first and
		// "12.4" sorts before "9.9" as a string. Without a case like this the assertion below is
		// vacuous — walk order already happens to agree with sorted order for a single number per
		// field, which is how a mutation that removed the sort entirely survived the first version
		// of this test.
		"narrative_en": "HbA1c 9.9 %, creatinine 12.4 mg/dL, on 2019-04-02 [obs.tsh:2026-01-05].",
		"key_points":   []any{"Creatinine 3.9 mg/dL"},
	}
	first := check(t, output, nil)
	if len(first.Findings) < 3 {
		t.Fatalf("only %d findings; this test needs several to be able to see an ordering", len(first.Findings))
	}
	for i := 0; i < 20; i++ {
		again := check(t, output, nil)
		if fmt.Sprint(again.Findings) != fmt.Sprint(first.Findings) {
			t.Fatalf("run %d ordered the findings differently:\n%v\n%v", i, first.Findings, again.Findings)
		}
	}
	// And the order is the *stated* one — path, then arm, then token — rather than merely stable.
	//
	// Asserting only stability was not enough, and a mutation found it: walk order is already
	// deterministic because the object keys are sorted before they are visited, so removing the
	// final sort altogether left this test green. The order matters beyond determinism because the
	// defect rows are written in it and a reviewer reads them grouped by where in the answer the
	// claim was.
	for i := 1; i < len(first.Findings); i++ {
		previous, current := first.Findings[i-1], first.Findings[i]
		if previous.Path > current.Path ||
			(previous.Path == current.Path && previous.Arm > current.Arm) ||
			(previous.Path == current.Path && previous.Arm == current.Arm && previous.Token > current.Token) {
			t.Fatalf("the findings are stable but not sorted: %v then %v", previous, current)
		}
	}
}

// TestTheSynthesisAgentStillNamesItsDrugFieldSomethingTheDrugArmLooksFor.
//
// `ai.DrugKeys` is a key list, and a key list has one failure mode: the day somebody renames the
// field, the arm silently stops applying. This reads the agent's committed schema and fails if the
// name it uses is not one the check knows about — which is the only thing that would tell anybody.
func TestTheSynthesisAgentStillNamesItsDrugFieldSomethingTheDrugArmLooksFor(t *testing.T) {
	registry, err := ai.LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	prompt, known := registry.Latest("clinical.synthesis")
	if !known {
		t.Fatal("the synthesis agent has no prompt in this build")
	}
	medications := prompt.OutputSchema.Properties["draft_medications"]
	if medications == nil || medications.Items == nil {
		t.Fatal("the synthesis schema no longer has a draft_medications array; the drug arm has nothing to bite on")
	}
	var found []string
	for name := range medications.Items.Properties {
		if ai.DrugKeys[strings.ToLower(name)] {
			found = append(found, name)
		}
	}
	if len(found) == 0 {
		names := make([]string, 0, len(medications.Items.Properties))
		for name := range medications.Items.Properties {
			names = append(names, name)
		}
		t.Errorf("none of the drafted-medication fields %v is in ai.DrugKeys, so no drug name in a "+
			"synthesis answer would ever be checked", names)
	}
}

// TestEveryInjectedHallucinationInTheFrozenSetIsDetected is acceptance criterion 1.
//
// Over the committed set rather than over cases written inside this file, because the set is what
// CI runs and what the baseline is blessed against: a criterion measured on one corpus and gated on
// another is two claims wearing one number.
func TestEveryInjectedHallucinationInTheFrozenSetIsDetected(t *testing.T) {
	set, err := evalset.Load()
	if err != nil {
		t.Fatal(err)
	}
	_, injected := set.Counts()
	if injected < 5 {
		t.Fatalf("the frozen set holds %d injected hallucinations; too few to measure a detection rate", injected)
	}

	arms := map[ai.GroundingArm]bool{}
	for _, one := range set.Cases {
		if one.Expect != evalset.Hallucination {
			continue
		}
		report := ai.GroundsFrom(one.Payload).Check(one.Output, nil)
		if report.OK() {
			t.Errorf("%s was not detected at all: %s", one.ID, one.Injection)
			continue
		}
		// Caught, and caught on what the case says it should be caught on. A hallucination detected
		// for an unrelated reason is a case that stops testing anything the day the unrelated
		// reason is fixed — which is exactly what happened while this set was being built: the
		// digit-swap injection corrupted a date rather than a measurement and fired the wrong arm.
		for _, want := range one.Expected {
			if !hasFinding(report, want.Arm, want.Token) {
				t.Errorf("%s was caught, but not on %s %q; found %s",
					one.ID, want.Arm, want.Token, report.Summary())
			}
			arms[want.Arm] = true
		}
	}
	// All four arms are exercised by the set. Without this the suite could be green with three of
	// them never having run.
	for _, arm := range []ai.GroundingArm{ai.ArmCitation, ai.ArmNumber, ai.ArmDate, ai.ArmDrug} {
		if !arms[arm] {
			t.Errorf("no case in the frozen set exercises the %s arm", arm)
		}
	}
}

// TestTheFalsePositiveRateOnKnownCorrectAnswersIsMeasured is acceptance criterion 2.
//
// The number, and the denominators that make it mean something. What this measures is stated
// plainly in `docs/ai-grounding.md` and repeated here because it is the criterion most easily
// faked: the twenty known-correct answers were written by CP71's **deterministic composer**, which
// only ever writes values it read out of the payload. That makes them a real corpus for the
// citation, date and drug arms and a weak one for the number arm's hardest case — a model
// paraphrasing a label rather than quoting it.
func TestTheFalsePositiveRateOnKnownCorrectAnswersIsMeasured(t *testing.T) {
	set, err := evalset.Load()
	if err != nil {
		t.Fatal(err)
	}
	correct, _ := set.Counts()
	if correct < 15 {
		t.Fatalf("the frozen set holds %d known-correct answers; too few to measure a rate on", correct)
	}

	var blocked int
	var numbers, dates, citations int
	for _, one := range set.Cases {
		if one.Expect != evalset.Grounded {
			continue
		}
		report := ai.GroundsFrom(one.Payload).Check(one.Output, nil)
		numbers += report.Numbers
		dates += report.Dates
		citations += report.Citations
		if !report.OK() {
			blocked++
			t.Errorf("%s is a known-correct answer and was refused: %s", one.ID, report.Summary())
		}
	}

	// The denominator, asserted rather than reported. A false-positive rate of 0% from a check that
	// examined nothing is the shape this criterion is most likely to be faked in, deliberately or
	// otherwise: a payload whose fact index stopped decoding would produce exactly that number.
	if numbers < 500 || citations < 400 || dates < 40 {
		t.Fatalf("the check examined %d numbers, %d citations and %d dates across %d answers; "+
			"a zero false-positive rate from that is not a measurement",
			numbers, citations, dates, correct)
	}
	t.Logf("false positives: %d of %d known-correct answers (%.1f%%), from %d numbers, "+
		"%d citations and %d dates examined",
		blocked, correct, float64(blocked)/float64(correct)*100, numbers, citations, dates)
}

// TestAnAgentWithNoFactIndexStillHasItsNumbersChecked.
//
// The citation arm needs a `facts` array; the number and date arms do not. An agent that arrives in
// two years without a fact index should lose the citation arm and keep the rest, rather than
// silently passing everything — which is what a check that keyed on the fact index would do.
func TestAnAgentWithNoFactIndexStillHasItsNumbersChecked(t *testing.T) {
	grounds := ai.GroundsFrom(map[string]any{"note": "the value was 8.2"})
	if grounds.Facts() != 0 {
		t.Fatalf("this payload has no fact index and the grounds report %d", grounds.Facts())
	}
	if report := grounds.Check(map[string]any{"summary": "the value was 8.2"}, nil); !report.OK() {
		t.Errorf("a quoted value was refused: %s", report.Summary())
	}
	report := grounds.Check(map[string]any{"summary": "the value was 9.7"}, nil)
	if report.OK() {
		t.Error("an invented value was allowed through for want of a fact index")
	}
	assertFinding(t, report, ai.ArmNumber, "9.7")
}

// TestTheGroundingReportSurvivesAJSONRoundTrip. The findings are stored as jsonb on
// `ops.ai_evaluation_case` and returned over the API; a field that did not serialise would be a
// defect queue with no reasons in it.
func TestTheGroundingReportSurvivesAJSONRoundTrip(t *testing.T) {
	report := check(t, map[string]any{"narrative_en": "HbA1c 12.4 %."}, nil)
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var back ai.GroundingReport
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Findings) != len(report.Findings) || back.State != report.State {
		t.Fatalf("the report did not survive: %s", encoded)
	}
	if back.Findings[0].Reason == "" || back.Findings[0].Token == "" {
		t.Errorf("a stored finding has no reason or no token: %s", encoded)
	}
}

func assertFinding(t *testing.T, report ai.GroundingReport, arm ai.GroundingArm, token string) {
	t.Helper()
	if !hasFinding(report, arm, token) {
		t.Errorf("no %s finding for %q; the report says %s", arm, token, report.Summary())
	}
}

func hasFinding(report ai.GroundingReport, arm ai.GroundingArm, token string) bool {
	for _, finding := range report.Findings {
		if finding.Arm == arm && strings.Contains(finding.Token, token) {
			return true
		}
	}
	return false
}
