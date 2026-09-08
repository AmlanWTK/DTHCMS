package evalset_test

import (
	"bytes"
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/ai"
	"github.com/AmlanWTK/DTHCMS/backend/internal/ai/evalset"
)

// The gate, and whether it can fail (CP72 criteria 3 and 5).
//
// A gate nobody has watched fail is a gate nobody should trust, and this project has been bitten
// three times this week by checks that confirmed something existed rather than that it worked. So
// every one of the four refusals the harness can produce has a test that produces it, and the one
// that says the committed set passes is deliberately last: without the four failures beside it, a
// green run proves only that nothing was being checked.

func load(t *testing.T) (evalset.Set, ai.Prompt, evalset.Baseline) {
	t.Helper()
	set, err := evalset.Load()
	if err != nil {
		t.Fatalf("the frozen set does not load: %v", err)
	}
	registry, err := ai.LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	prompt, known := registry.Latest(set.AgentCode)
	if !known {
		t.Fatalf("the set names %q and this build has no prompt for it", set.AgentCode)
	}
	baseline, err := evalset.LoadBaseline()
	if err != nil {
		t.Fatal(err)
	}
	return set, prompt, baseline
}

func promptHashes(t *testing.T) map[string]string {
	t.Helper()
	registry, err := ai.LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, prompt := range registry.All() {
		if _, seen := out[prompt.AgentCode]; !seen {
			out[prompt.AgentCode] = prompt.SHA256
		}
	}
	return out
}

func run(t *testing.T, set evalset.Set, prompt ai.Prompt) evalset.Result {
	t.Helper()
	at := time.Date(2026, 9, 8, 6, 0, 0, 0, time.UTC)
	return evalset.Run(set, prompt, nil, "replay", evalset.Replay, func() time.Time { return at })
}

// TestTheFrozenSetLoadsAndIsBigEnoughToMeasureAnything.
//
// [evalset.Load] recomputes every case file's hash and the set hash and refuses a set that
// disagrees with its manifest, so this test is also the check that nobody has edited a case without
// regenerating the manifest — the two-file change that makes "frozen" mean something.
func TestTheFrozenSetLoadsAndIsBigEnoughToMeasureAnything(t *testing.T) {
	set, _, _ := load(t)
	correct, injected := set.Counts()
	if correct < 15 {
		t.Errorf("the set holds %d known-correct answers; a false-positive rate over fewer is noise", correct)
	}
	if injected < 5 {
		t.Errorf("the set holds %d injected hallucinations", injected)
	}
	if len(set.SHA256) != 64 {
		t.Errorf("the set hash is %q", set.SHA256)
	}
	// Every derived case really resolved a payload. A case with an empty payload would ground
	// against nothing and be "detected" for the emptiest possible reason.
	for _, one := range set.Cases {
		if len(one.Payload) == 0 {
			t.Errorf("%s has no payload", one.ID)
		}
		if len(one.Output) == 0 {
			t.Errorf("%s has no output", one.ID)
		}
		if one.Expect == evalset.Hallucination && len(one.Expected) == 0 {
			t.Errorf("%s is a hallucination case that does not say what should catch it, so it "+
				"would pass on any finding at all", one.ID)
		}
	}
}

// TestTheCommittedSetPassesTheCommittedBaseline is what CI asserts on every push.
func TestTheCommittedSetPassesTheCommittedBaseline(t *testing.T) {
	set, prompt, baseline := load(t)
	result := run(t, set, prompt)
	result.Gate(baseline, promptHashes(t))

	if result.Run.Verdict != "PASS" {
		t.Fatalf("the committed set does not pass its own baseline:\n%s", result.Report())
	}
	if result.Run.Detected != result.Run.Injected {
		t.Errorf("%d of %d injected hallucinations detected", result.Run.Detected, result.Run.Injected)
	}
	// The denominators again. A pass from a harness that examined nothing is the shape this whole
	// gate would fail in without anybody noticing.
	var numbers, citations int
	for _, detail := range result.Details {
		numbers += detail.Report.Numbers
		citations += detail.Report.Citations
	}
	if numbers < 500 || citations < 400 {
		t.Errorf("the run examined %d numbers and %d citations across %d cases; the gate is not "+
			"looking at the answers", numbers, citations, result.Run.Cases)
	}
	t.Logf("%s", result.Report())
}

// TestTheGateFailsWhenAHallucinationIsMissed.
//
// Criterion 1 has no tolerance. Simulated by removing the injection from a hallucination case's
// answer — which is exactly what a validator that stopped catching that class would look like from
// the gate's side.
func TestTheGateFailsWhenAHallucinationIsMissed(t *testing.T) {
	set, prompt, baseline := load(t)
	var patched int
	for i, one := range set.Cases {
		if one.Expect != evalset.Hallucination {
			continue
		}
		// The parent case's clean answer, put back under the corrupted case's id and expectation.
		for _, parent := range set.Cases {
			if parent.ID == one.DerivedFrom {
				set.Cases[i].Output = parent.Output
				patched++
			}
		}
		break
	}
	if patched != 1 {
		t.Fatalf("no hallucination case could be un-corrupted; this test is not testing what it says")
	}

	result := run(t, set, prompt)
	result.Gate(baseline, promptHashes(t))
	if result.Run.Verdict != "FAIL" {
		t.Fatal("the gate passed a run that missed an injected hallucination")
	}
	if !mentions(result.Regressions, "was not detected") {
		t.Errorf("the gate failed for some other reason: %v", result.Regressions)
	}
}

// TestACaseCaughtOnTheWrongArmIsAMiss.
//
// A hallucination detected for an unrelated reason is a case that stops catching anything the day
// the unrelated reason is fixed, so the harness counts it as a miss. This is not hypothetical: the
// digit-swap injection was written wrongly the first time and corrupted a date rather than a
// measurement, and this is the rule that caught it in one run rather than in six months.
func TestACaseCaughtOnTheWrongArmIsAMiss(t *testing.T) {
	set, prompt, baseline := load(t)
	var patched bool
	for i, one := range set.Cases {
		if one.Expect != evalset.Hallucination || len(one.Expected) == 0 {
			continue
		}
		// Still corrupted, still caught — but the case now claims it should be caught on an arm
		// that will not fire.
		set.Cases[i].Expected = []evalset.ExpectedFinding{{Arm: ai.ArmDrug, Token: "nothing"}}
		patched = true
		break
	}
	if !patched {
		t.Fatal("no hallucination case to re-label")
	}

	result := run(t, set, prompt)
	result.Gate(baseline, promptHashes(t))
	if result.Run.Verdict != "FAIL" {
		t.Fatal("the gate passed a case caught on an arm it does not claim to test")
	}
	if !mentions(result.Regressions, "caught, but not on") {
		t.Errorf("the gate failed for some other reason: %v", result.Regressions)
	}
}

// TestTheGateFailsWhenAKnownCorrectAnswerIsRefused is criterion 2's side of the gate.
//
// Simulated by putting a fabricated value into an answer the set says is correct, which is what a
// validator that had become too strict would look like from here.
func TestTheGateFailsWhenAKnownCorrectAnswerIsRefused(t *testing.T) {
	set, prompt, baseline := load(t)
	var patched bool
	for i, one := range set.Cases {
		if one.Expect != evalset.Grounded {
			continue
		}
		narrative, ok := one.Output["narrative_en"].(string)
		if !ok {
			continue
		}
		clone := map[string]any{}
		for key, value := range one.Output {
			clone[key] = value
		}
		clone["narrative_en"] = narrative + " Serum potassium was 9.87 mmol/L."
		set.Cases[i].Output = clone
		patched = true
		break
	}
	if !patched {
		t.Fatal("no known-correct case to corrupt")
	}

	result := run(t, set, prompt)
	result.Gate(baseline, promptHashes(t))
	if result.Run.Verdict != "FAIL" {
		t.Fatal("the gate passed a run that refused a known-correct answer")
	}
	if !mentions(result.Regressions, "known-correct answers were refused") {
		t.Errorf("the gate failed for some other reason: %v", result.Regressions)
	}
}

// TestTheGateFailsWhenAPromptChangesAndTheBaselineWasNotReBlessed.
//
// **Criterion 3.** §10.5 requires the frozen set to run on every prompt or model change, and this
// is the enforcement of it. A `paths:` filter in a workflow file would be the obvious way and would
// be a gate that silently stops applying the day somebody moves the prompts directory; a hash
// comparison inside the gate cannot stop applying.
//
// A model change is a prompt change: `model_version` is a field in the prompt file, so swapping the
// model rewrites the file and moves its hash.
func TestTheGateFailsWhenAPromptChangesAndTheBaselineWasNotReBlessed(t *testing.T) {
	set, prompt, baseline := load(t)
	result := run(t, set, prompt)

	changed := map[string]string{}
	for name, hash := range promptHashes(t) {
		changed[name] = hash
	}
	changed[set.AgentCode] = strings.Repeat("f", 64)

	result.Gate(baseline, changed)
	if result.Run.Verdict != "FAIL" {
		t.Fatal("the gate passed a build whose prompt is not the one the baseline was blessed against")
	}
	if !mentions(result.Regressions, "has changed since the evaluation set last passed") {
		t.Errorf("the gate failed for some other reason: %v", result.Regressions)
	}
	if !mentions(result.Regressions, "-bless") {
		t.Error("the failure does not say what to do about it; a red gate that gives no remedy is " +
			"one somebody re-runs until it is green")
	}
}

// TestTheGateFailsWhenAPromptAppearsThatHasNeverBeenEvaluated.
//
// The other direction of the same rule: a new agent added to the build is a prompt the frozen set
// has never been run against, and a gate that only compared the prompts it already knew about
// would let one in silently.
func TestTheGateFailsWhenAPromptAppearsThatHasNeverBeenEvaluated(t *testing.T) {
	set, prompt, baseline := load(t)
	result := run(t, set, prompt)

	added := promptHashes(t)
	added["clinical.brandnew"] = strings.Repeat("a", 64)

	result.Gate(baseline, added)
	if result.Run.Verdict != "FAIL" {
		t.Fatal("the gate passed a build carrying a prompt nobody has evaluated")
	}
	if !mentions(result.Regressions, "has never been evaluated") {
		t.Errorf("the gate failed for some other reason: %v", result.Regressions)
	}
}

// TestTheGateFailsWhenTheCaseSetItselfChanges.
//
// The easiest way to make a regression disappear is to change the case that caught it, and nobody
// doing it would think of themselves as cheating. The set's own hash is on every recorded run, and
// a run against a set the baseline does not know is not comparable with the one before it.
func TestTheGateFailsWhenTheCaseSetItselfChanges(t *testing.T) {
	set, prompt, baseline := load(t)
	result := run(t, set, prompt)

	baseline.CaseSetSHA256 = strings.Repeat("b", 64)
	result.Gate(baseline, promptHashes(t))
	if result.Run.Verdict != "FAIL" {
		t.Fatal("the gate passed a run against a case set the baseline has never seen")
	}
	if !mentions(result.Regressions, "the case set has changed") {
		t.Errorf("the gate failed for some other reason: %v", result.Regressions)
	}
}

// TestTheGateFailsWhenAFrozenAnswerNoLongerFitsTheAgentsSchema.
//
// The regression a prompt change produces without a model being involved: tighten the output shape,
// and answers that were valid stop being valid. Caught in replay, in a second, with no credential.
func TestTheGateFailsWhenAFrozenAnswerNoLongerFitsTheAgentsSchema(t *testing.T) {
	set, prompt, baseline := load(t)

	// A narrative of at least ten thousand characters: nothing in the frozen set is that long, so
	// this is a schema every committed answer now fails.
	tightened := *prompt.OutputSchema
	properties := map[string]*ai.Schema{}
	for name, schema := range tightened.Properties {
		properties[name] = schema
	}
	narrative := *properties["narrative_en"]
	floor := 10000
	narrative.MinLength = &floor
	properties["narrative_en"] = &narrative
	tightened.Properties = properties
	prompt.OutputSchema = &tightened

	result := run(t, set, prompt)
	result.Gate(baseline, promptHashes(t))
	if result.Run.Verdict != "FAIL" {
		t.Fatal("the gate passed a build whose schema no longer accepts a single frozen answer")
	}
	if result.Run.SchemaFailures == 0 {
		t.Error("no schema failures were counted")
	}
	if !mentions(result.Regressions, "no longer satisfy the agent's schema") {
		t.Errorf("the gate failed for some other reason: %v", result.Regressions)
	}
}

// TestAReplayRunReportsLatencyAndCostAsAbsentRatherThanZero.
//
// The difference between "we did not measure this" and "this was free" is the whole reason the
// columns are nullable, and a dashboard reading a replay's zero cost as a saving is the mistake
// this prevents.
func TestAReplayRunReportsLatencyAndCostAsAbsentRatherThanZero(t *testing.T) {
	set, prompt, _ := load(t)
	result := run(t, set, prompt)
	if result.Run.LatencyP50MS != nil || result.Run.LatencyP95MS != nil || result.Run.CostMicroUSD != nil {
		t.Errorf("a replay reported latency %v/%v and cost %v",
			result.Run.LatencyP50MS, result.Run.LatencyP95MS, result.Run.CostMicroUSD)
	}
	if !strings.Contains(result.Report(), "not measured") {
		t.Error("the report does not say the numbers it does not have were not measured")
	}
}

// TestATamperedCaseIsRefused is what makes "frozen" a property rather than a description.
//
// The easiest way to make a regression disappear is to change the case that caught it, and nobody
// doing it would think of themselves as cheating. [evalset.Load] recomputes every case file's hash
// and refuses a set that disagrees with its manifest — and until this test existed, that refusal
// had never fired: a mutation that removed the comparison entirely left the suite green, because
// the committed manifest agrees with the committed cases.
func TestATamperedCaseIsRefused(t *testing.T) {
	honest, err := fs.Sub(realFiles(t), ".")
	if err != nil {
		t.Fatal(err)
	}
	tampered := fstest.MapFS{}
	names, err := fs.Glob(honest, "cases/*.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range append(names, "manifest.json", "baseline.json") {
		body, err := fs.ReadFile(honest, name)
		if err != nil {
			t.Fatal(err)
		}
		tampered[name] = &fstest.MapFile{Data: body}
	}
	// The control: the copy loads exactly as the embedded original does.
	if _, err := evalset.LoadFS(tampered); err != nil {
		t.Fatalf("an untouched copy of the set does not load: %v", err)
	}

	// One character. A description edited in a way that changes nothing a run would do — which is
	// precisely the edit somebody would make while quietly widening a case.
	victim := names[0]
	tampered[victim] = &fstest.MapFile{
		Data: bytes.Replace(tampered[victim].Data, []byte(`"description": "`),
			[]byte(`"description": " `), 1),
	}
	_, err = evalset.LoadFS(tampered)
	if err == nil {
		t.Fatal("a case edited without the manifest being regenerated was accepted")
	}
	if !strings.Contains(err.Error(), "hash") && !strings.Contains(err.Error(), "manifest") {
		t.Errorf("refused with %v, which does not tell the reader what happened", err)
	}
}

// realFiles reads the committed set off disk, because the embedded copy is not reachable as an
// fs.FS from outside the package.
func realFiles(t *testing.T) fs.FS {
	t.Helper()
	return os.DirFS(".")
}

func mentions(lines []string, want string) bool {
	for _, line := range lines {
		if strings.Contains(line, want) {
			return true
		}
	}
	return false
}
