package medsafety_test

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"unicode"
)

// The golden suite as a document Dr. Nahid can read, and the checks that keep it readable (CP78).
//
// The JSON is the test. This file renders it as a review sheet — the form the suite is actually
// argued about in, because nobody marks up a JSON file with a pen — and fails when the committed
// sheet has drifted from the JSON. Two artefacts, one truth, and a test rather than a convention
// keeping them the same.

const goldenSheetPath = "../../../docs/medication-safety-golden-suite.md"

func TestEveryScenarioSaysWhyItFiresInBothLanguagesAndCitesSomething(t *testing.T) {
	// A scenario with no `why` is a scenario a physician can only accept or delete, and one
	// with no citation is one nobody can check against the guidance it claims to follow. Both
	// are the quiet way a golden suite becomes a set of assertions nobody owns.
	suite := loadGoldenSuite(t)

	named := 0
	for _, s := range suite.Scenarios {
		if s.Named {
			named++
		}
		if strings.TrimSpace(s.TitleEN) == "" || strings.TrimSpace(s.TitleBN) == "" {
			t.Errorf("%s: the title is missing one of its two languages", s.ID)
		}
		if strings.TrimSpace(s.WhyEN) == "" || strings.TrimSpace(s.WhyBN) == "" {
			t.Errorf("%s: no reason it is expected to fire, in one or both languages. A "+
				"physician disagreeing has to know what to argue with.", s.ID)
		}
		if len(s.WhyEN) < 80 {
			t.Errorf("%s: the reason is %d characters. That is a label, not an argument.",
				s.ID, len(s.WhyEN))
		}
		if strings.TrimSpace(s.Citation) == "" {
			t.Errorf("%s: cites nothing", s.ID)
		}
		if !hasBengali(s.TitleBN) {
			t.Errorf("%s: title_bn is not in Bengali script: %q", s.ID, s.TitleBN)
		}
		if !hasBengali(s.WhyBN) {
			t.Errorf("%s: why_bn is not in Bengali script", s.ID)
		}
		if s.Blind.Remove == "" {
			t.Errorf("%s: says nothing about what to blind. Every scenario has to state how "+
				"the engine is made blind to the thing that should catch it, or it cannot "+
				"prove it is measuring anything.", s.ID)
		}
		if len(s.Expect.MustFire) == 0 && len(s.Expect.MustNotFire) == 0 &&
			s.Expect.Verdict == "" && len(s.Expect.VerdictMustNotBe) == 0 {
			t.Errorf("%s: expects nothing at all", s.ID)
		}
	}
	if named != 4 {
		t.Errorf("%d scenarios are marked as named in the plan; the plan names four and they "+
			"are non-negotiable", named)
	}
	if len(suite.Gaps) == 0 {
		t.Errorf("the suite lists no gaps. A suite that claims to cover everything is the " +
			"artefact §7.2's risk note is about.")
	}
}

func TestTheFourScenariosThePlanNamesArePresentAndAssertTheRightThing(t *testing.T) {
	// Written out by hand, because the other test only counts them. This is the reviewer's copy
	// of the list: if one is renamed away, this says so by name.
	suite := loadGoldenSuite(t)
	want := map[string]string{
		"PENICILLIN-AMOXICILLIN": "BLOCKED",
		"METFORMIN-EGFR-25":      "BLOCKED",
		"TWO-ACE-INHIBITORS":     "WARNINGS",
		"UNKNOWN-DRUG":           "CANNOT_VERIFY",
	}
	found := map[string]bool{}
	for _, s := range suite.Scenarios {
		expected, named := want[s.ID]
		if !named {
			continue
		}
		found[s.ID] = true
		if s.Expect.Verdict != expected {
			t.Errorf("%s expects verdict %s, and the plan says %s",
				s.ID, s.Expect.Verdict, expected)
		}
		if !s.Named {
			t.Errorf("%s is one of the four the plan names and is not marked as such", s.ID)
		}
	}
	for id := range want {
		if !found[id] {
			t.Errorf("%s is one of the four scenarios the plan names and it is not in the "+
				"suite", id)
		}
	}
	// The duplicate one has to be a duplicate finding rather than any warning at all.
	for _, s := range suite.Scenarios {
		if s.ID != "TWO-ACE-INHIBITORS" {
			continue
		}
		if len(s.Expect.MustFire) == 0 ||
			!strings.HasPrefix(s.Expect.MustFire[0].Rule, "DUP-") {
			t.Errorf("the two-ACE-inhibitor scenario does not assert a duplicate-therapy "+
				"finding: %+v", s.Expect.MustFire)
		}
	}
	// And the unknown-drug one has to assert coverage rather than a verdict alone.
	for _, s := range suite.Scenarios {
		if s.ID != "UNKNOWN-DRUG" {
			continue
		}
		if s.Expect.Coverage["1"] != "NOT_COVERED" {
			t.Errorf("the unknown-drug scenario does not assert NOT_COVERED: %v",
				s.Expect.Coverage)
		}
		if !contains(s.Expect.VerdictMustNotBe, "CLEAR_WITHIN_COVERAGE") {
			t.Errorf("the unknown-drug scenario does not forbid the clear verdict, which is " +
				"the whole point of it")
		}
	}
}

func TestTheReviewSheetMatchesTheSuite(t *testing.T) {
	// The sheet is what Dr. Nahid reads; the JSON is what runs. A sheet that has drifted is a
	// physician reviewing a scenario that no longer exists, which is worse than no sheet.
	//
	// Regenerate with:
	//   DTHCMS_UPDATE_GOLDEN_SHEET=1 go test ./internal/medsafety/ -run TestTheReviewSheet
	suite := loadGoldenSuite(t)
	rendered := renderReviewSheet(suite)

	if wantsUpdate() {
		if err := os.WriteFile(goldenSheetPath, []byte(rendered), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", goldenSheetPath)
		return
	}
	committed, err := os.ReadFile(goldenSheetPath)
	if err != nil {
		t.Fatalf("the review sheet is missing; regenerate it with "+
			"DTHCMS_UPDATE_GOLDEN_SHEET=1: %v", err)
	}
	if string(committed) != rendered {
		t.Errorf("%s has drifted from the JSON. Regenerate it:\n"+
			"  DTHCMS_UPDATE_GOLDEN_SHEET=1 go test ./internal/medsafety/ -run TestTheReviewSheet",
			goldenSheetPath)
	}
}

// wantsUpdate reads an environment variable rather than a flag, because a test binary that
// registers its own flag breaks `go test ./...` for every other package in the tree.
func wantsUpdate() bool { return os.Getenv("DTHCMS_UPDATE_GOLDEN_SHEET") == "1" }

func renderReviewSheet(suite goldenSuite) string {
	var b strings.Builder
	b.WriteString("# The medication safety golden suite, for review\n\n")
	b.WriteString("_Generated from [`medication-safety-golden-suite.json`]" +
		"(medication-safety-golden-suite.json). Do not edit this file — edit the JSON and " +
		"regenerate: `DTHCMS_UPDATE_GOLDEN_SHEET=1 go test ./internal/medsafety/ " +
		"-run TestTheReviewSheet`._\n\n")
	b.WriteString("**What this is.** Every scenario below is a test that runs against the " +
		"real engine. Drafted by a developer from published guidance; **yours to correct**. " +
		"Each says what the patient looks like, what is being prescribed, what the engine must " +
		"say, and — most importantly — **why**. If you disagree with a why, that is the thing " +
		"to argue with; change the JSON and the test changes.\n\n")
	b.WriteString("**Every rule is approved inside the test and nowhere else.** Reading or " +
		"running this changes nothing in the clinic's own rule library.\n\n")
	b.WriteString(fmt.Sprintf("%d scenarios. %d of them are named in the implementation plan "+
		"and are marked ★.\n\n", len(suite.Scenarios), countNamed(suite)))

	b.WriteString("| # | Scenario | Expected | Rules |\n|---|---|---|---|\n")
	for i, s := range suite.Scenarios {
		star := ""
		if s.Named {
			star = "★ "
		}
		b.WriteString(fmt.Sprintf("| %d | %s[%s](#%s) | `%s` | %s |\n",
			i+1, star, s.TitleEN, strings.ToLower(s.ID), s.Expect.Verdict,
			joinCodes(s.RulesNeeded)))
	}
	b.WriteString("\n---\n\n")

	for i, s := range suite.Scenarios {
		star := ""
		if s.Named {
			star = " ★"
		}
		b.WriteString(fmt.Sprintf("## %d. %s%s\n\n", i+1, s.ID, star))
		b.WriteString("**" + s.TitleEN + "**  \n")
		b.WriteString(s.TitleBN + "\n\n")
		b.WriteString("**Why it must fire.** " + s.WhyEN + "\n\n")
		b.WriteString("**কেন।** " + s.WhyBN + "\n\n")
		b.WriteString("**Citation.** " + s.Citation + "\n\n")

		b.WriteString("**The patient.** " + describePatient(s.Patient) + "\n\n")
		b.WriteString("**Prescribed.** " + describeProposed(s.Proposed) + "\n\n")

		b.WriteString("**The engine must say.** ")
		if s.Expect.Verdict != "" {
			b.WriteString("verdict `" + s.Expect.Verdict + "`")
		} else {
			b.WriteString("(no verdict asserted)")
		}
		for _, fire := range s.Expect.MustFire {
			outcome := fire.Outcome
			if outcome == "" {
				outcome = "FIRES"
			}
			b.WriteString(fmt.Sprintf("; `%s` %s (%s) on line %q",
				fire.Rule, outcome, fire.Severity, fire.On))
		}
		for _, no := range s.Expect.MustNotFire {
			b.WriteString("; `" + no + "` must NOT fire")
		}
		if len(s.Expect.Coverage) > 0 {
			refs := make([]string, 0, len(s.Expect.Coverage))
			for ref := range s.Expect.Coverage {
				refs = append(refs, ref)
			}
			sort.Strings(refs)
			for _, ref := range refs {
				b.WriteString(fmt.Sprintf("; line %q is `%s`", ref, s.Expect.Coverage[ref]))
			}
		}
		b.WriteString(".\n\n")

		b.WriteString("**Proved by removal.** Take away " + blindName(s.Blind.Remove) +
			" and the answer must become `" + s.Blind.ExpectVerdict + "`")
		if len(s.Blind.CannotVerify) > 0 {
			b.WriteString(", with " + joinCodes(s.Blind.CannotVerify) +
				" saying it cannot verify")
		}
		b.WriteString(".")
		if strings.TrimSpace(s.Blind.WhyEN) != "" {
			b.WriteString(" " + s.Blind.WhyEN)
		}
		b.WriteString("\n\n")
	}

	b.WriteString("---\n\n## What this suite cannot test yet\n\n")
	b.WriteString("Scenarios that matter and cannot be written against the model as it " +
		"stands. Each names what it would need.\n\n")
	for _, gap := range suite.Gaps {
		b.WriteString("### " + str(gap["scenario"]) + "\n\n")
		b.WriteString(str(gap["why_en"]) + "\n\n")
		b.WriteString("_Needs:_ " + str(gap["needs"]) + "\n\n")
	}
	return b.String()
}

func countNamed(suite goldenSuite) int {
	n := 0
	for _, s := range suite.Scenarios {
		if s.Named {
			n++
		}
	}
	return n
}

func describePatient(p goldenPatient) string {
	var parts []string
	if p.AgeYears != nil {
		parts = append(parts, fmt.Sprintf("%g years old", *p.AgeYears))
	} else {
		parts = append(parts, "age not recorded")
	}
	if strings.TrimSpace(p.Pregnancy) == "" {
		parts = append(parts, "pregnancy status not recorded")
	} else {
		parts = append(parts, strings.ToLower(strings.ReplaceAll(p.Pregnancy, "_", " ")))
	}
	if p.EGFR != nil {
		parts = append(parts, fmt.Sprintf("eGFR %g", *p.EGFR))
	} else {
		parts = append(parts, "**no eGFR on file**")
	}
	switch {
	case p.Diagnoses == nil:
		parts = append(parts, "**diagnosis list not read**")
	case len(*p.Diagnoses) == 0:
		parts = append(parts, "no coded diagnoses")
	default:
		parts = append(parts, "diagnoses "+strings.Join(*p.Diagnoses, ", "))
	}
	switch {
	case p.Allergies == nil:
		parts = append(parts, "**allergy status not established**")
	case len(*p.Allergies) == 0:
		parts = append(parts, "asked about allergies, none known")
	default:
		parts = append(parts, "allergic to "+strings.Join(*p.Allergies, ", "))
	}
	switch {
	case p.Current == nil:
		parts = append(parts, "**current medicines not read**")
	case len(*p.Current) == 0:
		parts = append(parts, "on nothing already")
	default:
		var names []string
		for _, item := range *p.Current {
			names = append(names, labelOf(item))
		}
		parts = append(parts, "already taking "+strings.Join(names, ", "))
	}
	return strings.Join(parts, "; ") + "."
}

func describeProposed(items []goldenItem) string {
	if len(items) == 0 {
		return "_nothing_."
	}
	var parts []string
	for _, item := range items {
		entry := labelOf(item)
		if item.DailyDose != nil {
			entry += fmt.Sprintf(" (%g %s a day)", *item.DailyDose, item.DoseUnit)
		}
		parts = append(parts, entry)
	}
	return strings.Join(parts, "; ") + "."
}

func labelOf(item goldenItem) string {
	if strings.TrimSpace(item.Label) == "" {
		return item.Generic
	}
	if strings.TrimSpace(item.Generic) == "" {
		return item.Label
	}
	return item.Label + " — " + item.Generic
}

func blindName(remove string) string {
	switch remove {
	case "nothing":
		return "_nothing (the scenario is itself the blinding)_"
	case "current_medicines":
		return "the current medication list"
	case "daily_dose":
		return "the daily dose"
	case "components":
		return "the record of what these medicines are made of"
	case "egfr":
		return "the eGFR"
	case "diagnoses":
		return "the diagnosis list"
	case "allergies":
		return "the allergy list"
	default:
		return "the " + strings.ReplaceAll(remove, "_", " ")
	}
}

func joinCodes(codes []string) string {
	if len(codes) == 0 {
		return "_none — nothing is approved_"
	}
	out := make([]string, 0, len(codes))
	for _, code := range codes {
		out = append(out, "`"+code+"`")
	}
	return strings.Join(out, ", ")
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func hasBengali(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Bengali, r) {
			return true
		}
	}
	return false
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
