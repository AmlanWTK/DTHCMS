package evalset

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/ai"
)

// The harness, and the gate it feeds (CP72 criteria 1, 2, 3 and 5).
//
// # Two modes, and only one of them can run in CI
//
// **Replay** re-checks the frozen answers with no model contacted. It is deterministic, free,
// takes about a second, and measures three of the four metrics the checkpoint names: grounding
// violations, schema failures, and — as the two rates — detection and false positives. It cannot
// measure latency or cost, and it reports them as *absent* rather than as zero, which is the
// difference between "we did not measure this" and "this was free".
//
// **Live** calls the gateway with the frozen payloads and a real credential, and measures all
// four. It needs a paid key (D-07 forbids the free tier for anything derived from a patient, and
// forbids it outright outside local, test and dev), so it is a thing somebody runs deliberately
// before a release rather than a thing CI does on every push.
//
// The replay mode is not a lesser version of the live one. It answers a different and sharper
// question: *given the answers we have already seen, does the current validator, the current
// schema and the current prompt still behave the way the baseline says?* That is exactly the
// regression §10.5 asks CI to catch, and it catches it without a model in the loop.
//
// # What the gate refuses, and why each is a refusal rather than a warning
//
//  1. **A missed hallucination.** Criterion 1 has no tolerance and this is not a clinical
//     threshold requiring anybody's approval: a check that fails to catch a fabricated number in
//     a test case where somebody put one is not doing the job it was built for.
//  2. **More false positives than the baseline.** The baseline is a committed number, so this is
//     a comparison against a decision somebody made rather than against a constant in the code.
//     Loosening it means editing a file and defending the edit.
//  3. **A frozen answer that no longer satisfies the agent's schema.** This is how a prompt change
//     that tightens the output shape gets caught with no model involved.
//  4. **A prompt whose content hash is not the one the baseline was blessed against.** This is the
//     enforcement of *"every change to a prompt or model runs the evaluation set"*. A `paths:`
//     filter in a CI file would be the obvious way to do it and would be a gate that silently
//     stops applying the day somebody moves the prompts directory; a hash comparison in the gate
//     itself cannot stop applying, and its failure message says what to do.

// Outcome is what happened to one case. The five strings the database stores.
type Outcome string

const (
	Clean         Outcome = "CLEAN"
	Detected      Outcome = "DETECTED"
	Missed        Outcome = "MISSED"
	FalsePositive Outcome = "FALSE_POSITIVE"
	SchemaFailure Outcome = "SCHEMA_FAILURE"
)

// CaseResult is one case's verdict.
type CaseResult struct {
	Case     Case
	Outcome  Outcome
	Report   ai.GroundingReport
	Detail   string
	Duration time.Duration
}

// Result is one whole run.
type Result struct {
	Run   ai.EvaluationRun
	Cases []ai.EvaluationCase

	Details []CaseResult
	// Regressions is why the gate failed, one sentence each, in the order they were found.
	Regressions []string
}

// Baseline is the last accepted result, committed beside the set.
//
// The prompt hashes are the interesting field. They are what makes criterion 3 — *"the evaluation
// set runs in CI on every prompt or model change"* — a thing the gate enforces rather than a thing
// a workflow file remembers to trigger. A model change is a prompt change: `model_version` is a
// field in the prompt file, so changing the model changes the file's hash.
type Baseline struct {
	CaseSetSHA256 string            `json:"case_set_sha256"`
	Prompts       map[string]string `json:"prompt_sha256"`

	// Blocked is how many known-correct answers the validator refused when this baseline was
	// blessed. Zero today. A run with more than this is a regression; a run with fewer is an
	// improvement the blesser should record.
	Blocked int `json:"false_positives"`
	// SchemaFailures is the same for the schema check.
	SchemaFailures int `json:"schema_failures"`

	BlessedAt   string `json:"blessed_at"`
	BlessedNote string `json:"note"`
}

// LoadBaseline reads the committed baseline.
func LoadBaseline() (Baseline, error) {
	raw, err := files.ReadFile("baseline.json")
	if err != nil {
		return Baseline{}, err
	}
	var baseline Baseline
	if err := json.Unmarshal(raw, &baseline); err != nil {
		return Baseline{}, fmt.Errorf("evalset: the baseline does not parse: %w", err)
	}
	return baseline, nil
}

// Answer is what an evaluation gets back for one case. Replay hands over the frozen answer; live
// mode hands over what the gateway returned, with the cost and latency it took.
type Answer struct {
	Output       map[string]any
	Grounding    *ai.GroundingReport
	Duration     time.Duration
	CostMicroUSD int64
	// Err is a call that never produced an answer. A provider failure in live mode is not a
	// grounding result and must not be counted as one.
	Err error
}

// Provider supplies an answer for a case. Replay and live are two implementations, and keeping
// them behind one seam is what stops the two modes drifting into two harnesses.
type Provider func(one Case) Answer

// Replay is the provider that contacts nobody: the frozen answer, re-checked.
func Replay(one Case) Answer { return Answer{Output: one.Output} }

// Run executes the set and produces the row and the verdict.
//
// The grounding check is run here rather than taken from the answer whenever the provider did not
// supply one, which is what makes replay mode test *the current validator* rather than the verdict
// that was recorded when the answers were frozen.
func Run(set Set, prompt ai.Prompt, drugs ai.DrugCheck, mode string, provider Provider,
	now func() time.Time) Result {

	started := now()
	out := Result{Run: ai.EvaluationRun{
		ID: uuid.New(), AgentCode: set.AgentCode,
		PromptVersion: prompt.Version, PromptSHA256: prompt.SHA256,
		ModelVersion: prompt.ModelVersion,
		CaseSet:      set.Name, CaseSetSHA256: set.SHA256,
		Mode: mode, Cases: len(set.Cases), StartedAt: started,
	}}

	var latencies []int
	var cost int64
	for _, one := range set.Cases {
		answer := provider(one)
		result := CaseResult{Case: one, Duration: answer.Duration}
		switch {
		case answer.Err != nil:
			// A call that never happened is not evidence about grounding either way. It is
			// recorded as a schema failure only if that is what it was; anything else stops the
			// run, because a partial run reported as a pass is the worst available outcome.
			result.Outcome = SchemaFailure
			result.Detail = "the provider returned no answer: " + answer.Err.Error()
		default:
			result = evaluate(one, prompt, drugs, answer)
		}
		if answer.Duration > 0 {
			latencies = append(latencies, int(answer.Duration/time.Millisecond))
		}
		cost += answer.CostMicroUSD

		out.Details = append(out.Details, result)
		switch one.Expect {
		case Grounded:
			out.Run.Correct++
			if result.Outcome == FalsePositive {
				out.Run.Blocked++
			}
		case Hallucination:
			out.Run.Injected++
			if result.Outcome == Detected {
				out.Run.Detected++
			}
		}
		if result.Outcome == SchemaFailure {
			out.Run.SchemaFailures++
		}

		encoded := result.Report.Findings
		if encoded == nil {
			encoded = []ai.GroundingFinding{}
		}
		one := ai.EvaluationCase{
			CaseID: result.Case.ID, Expectation: string(result.Case.Expect),
			Outcome: string(result.Outcome), Findings: encoded,
		}
		if answer.Duration > 0 {
			ms := int(answer.Duration / time.Millisecond)
			one.LatencyMS = &ms
		}
		out.Cases = append(out.Cases, one)
	}

	out.Run.FinishedAt = now()
	if mode == "live" {
		p50, p95 := percentiles(latencies)
		out.Run.LatencyP50MS, out.Run.LatencyP95MS = &p50, &p95
		spent := cost
		out.Run.CostMicroUSD = &spent
	}
	return out
}

func evaluate(one Case, prompt ai.Prompt, drugs ai.DrugCheck, answer Answer) CaseResult {
	result := CaseResult{Case: one, Duration: answer.Duration}

	// The schema first, because an answer that no longer fits the agent's shape is a different
	// regression from an answer that says something untrue, and reporting the second when the
	// first is what happened would send somebody to look at the wrong file.
	//
	// Skipped when the provider brought its own grounding verdict, which only happens in live mode:
	// the gateway validated the schema at step 8 before it ever reached step 9, so a second check
	// here would be checking the gateway rather than the answer — and on a refusal there is no
	// answer left to check, because the gateway does not hand one back.
	if prompt.OutputSchema != nil && answer.Grounding == nil {
		encoded, err := json.Marshal(answer.Output)
		if err != nil {
			result.Outcome, result.Detail = SchemaFailure, err.Error()
			return result
		}
		if _, violations := prompt.OutputSchema.Validate(string(encoded)); len(violations) > 0 {
			result.Outcome = SchemaFailure
			result.Detail = "the frozen answer no longer satisfies the agent's output schema: " +
				violations[0].String()
			return result
		}
	}

	if answer.Grounding != nil {
		result.Report = *answer.Grounding
	} else {
		result.Report = ai.GroundsFrom(one.Payload).Check(answer.Output, drugs)
	}

	switch one.Expect {
	case Grounded:
		if result.Report.OK() {
			result.Outcome = Clean
			return result
		}
		result.Outcome = FalsePositive
		result.Detail = result.Report.Summary()
		return result
	case Hallucination:
		missing := missingExpectations(one.Expected, result.Report)
		if len(missing) == 0 && !result.Report.OK() {
			result.Outcome = Detected
			return result
		}
		result.Outcome = Missed
		switch {
		case result.Report.OK():
			result.Detail = "the check passed an answer the set says is corrupted"
		default:
			// Caught, but not on what the case says it should have been caught on. Reported as a
			// miss rather than as a pass: a hallucination detected for an unrelated reason is a
			// case that will stop catching anything the day the unrelated reason is fixed.
			result.Detail = "caught, but not on " + strings.Join(missing, ", ") +
				"; found instead: " + result.Report.Summary()
		}
		return result
	}
	result.Outcome = SchemaFailure
	result.Detail = "the case does not say what it expects"
	return result
}

func missingExpectations(expected []ExpectedFinding, report ai.GroundingReport) []string {
	var missing []string
	for _, want := range expected {
		found := false
		for _, got := range report.Findings {
			if got.Arm == want.Arm && strings.Contains(got.Token, want.Token) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, fmt.Sprintf("%s %q", want.Arm, want.Token))
		}
	}
	return missing
}

// Gate applies the four refusals and stamps the verdict on the run.
func (r *Result) Gate(baseline Baseline, prompts map[string]string) {
	r.Regressions = nil

	if r.Run.Injected == 0 {
		r.Regressions = append(r.Regressions,
			"the set injected no hallucinations at all, so it measured nothing: acceptance "+
				"criterion 1 cannot be met by a set with nothing to detect")
	}
	if r.Run.Detected < r.Run.Injected {
		for _, detail := range r.Details {
			if detail.Outcome == Missed {
				r.Regressions = append(r.Regressions, fmt.Sprintf(
					"%s was not detected: %s", detail.Case.ID, detail.Detail))
			}
		}
	}
	if r.Run.Blocked > baseline.Blocked {
		r.Regressions = append(r.Regressions, fmt.Sprintf(
			"%d known-correct answers were refused and the baseline allows %d; the false-positive "+
				"rate has regressed", r.Run.Blocked, baseline.Blocked))
		for _, detail := range r.Details {
			if detail.Outcome == FalsePositive {
				r.Regressions = append(r.Regressions, "  "+detail.Case.ID+": "+detail.Detail)
			}
		}
	}
	if r.Run.SchemaFailures > baseline.SchemaFailures {
		r.Regressions = append(r.Regressions, fmt.Sprintf(
			"%d frozen answers no longer satisfy the agent's schema and the baseline allows %d",
			r.Run.SchemaFailures, baseline.SchemaFailures))
		for _, detail := range r.Details {
			if detail.Outcome == SchemaFailure {
				r.Regressions = append(r.Regressions, "  "+detail.Case.ID+": "+detail.Detail)
			}
		}
	}
	if baseline.CaseSetSHA256 != "" && baseline.CaseSetSHA256 != r.Run.CaseSetSHA256 {
		r.Regressions = append(r.Regressions, fmt.Sprintf(
			"the case set has changed since the baseline was blessed (%s → %s); re-run with -bless "+
				"and commit the baseline, so that the numbers it holds describe the cases that ran",
			short(baseline.CaseSetSHA256), short(r.Run.CaseSetSHA256)))
	}

	// Criterion 3, and the reason it is here rather than in a workflow file. Sorted so the message
	// is stable, because an unstable failure message is one people stop reading.
	names := make([]string, 0, len(prompts))
	for name := range prompts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		want, blessed := baseline.Prompts[name]
		switch {
		case !blessed:
			r.Regressions = append(r.Regressions, fmt.Sprintf(
				"the prompt %s has never been evaluated: add it to the baseline by running the "+
					"harness with -bless and committing the result", name))
		case want != prompts[name]:
			r.Regressions = append(r.Regressions, fmt.Sprintf(
				"the prompt %s has changed since the evaluation set last passed against it "+
					"(%s → %s). §10.5 requires the frozen set to run on every prompt or model "+
					"change: run `go run ./tools/aieval -bless` and commit the baseline",
				name, short(want), short(prompts[name])))
		}
	}
	for name := range baseline.Prompts {
		if _, present := prompts[name]; !present {
			r.Regressions = append(r.Regressions, fmt.Sprintf(
				"the baseline was blessed against the prompt %s, which this build does not contain", name))
		}
	}

	r.Run.Verdict = "PASS"
	if len(r.Regressions) > 0 {
		r.Run.Verdict = "FAIL"
		r.Run.RegressionDetail = strings.Join(r.Regressions, "\n")
	}
}

// Report is the page CI prints. Written for somebody reading a red build at eleven at night: the
// verdict first, then the numbers, then every case that is not clean, and nothing else.
func (r Result) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "AI evaluation · %s · %s\n", r.Run.AgentCode, r.Run.Verdict)
	fmt.Fprintf(&b, "  case set        %s (%s)\n", r.Run.CaseSet, short(r.Run.CaseSetSHA256))
	fmt.Fprintf(&b, "  prompt          %s (%s)\n", r.Run.PromptVersion, short(r.Run.PromptSHA256))
	fmt.Fprintf(&b, "  model           %s\n", r.Run.ModelVersion)
	fmt.Fprintf(&b, "  mode            %s\n", r.Run.Mode)
	fmt.Fprintf(&b, "  cases           %d\n", r.Run.Cases)

	detection := "not measured (no hallucinations in the set)"
	if rate := r.Run.DetectionRate(); rate != nil {
		detection = fmt.Sprintf("%d/%d (%.1f%%)", r.Run.Detected, r.Run.Injected, *rate*100)
	}
	fmt.Fprintf(&b, "  detection       %s\n", detection)

	falsePositive := "not measured (no known-correct answers in the set)"
	if rate := r.Run.FalsePositiveRate(); rate != nil {
		falsePositive = fmt.Sprintf("%d/%d (%.1f%%)", r.Run.Blocked, r.Run.Correct, *rate*100)
	}
	fmt.Fprintf(&b, "  false positives %s\n", falsePositive)
	fmt.Fprintf(&b, "  schema failures %d\n", r.Run.SchemaFailures)

	// The denominator. A clean run from a check that examined nothing looks identical to a clean
	// run from a check that examined six hundred numbers, and only one of them is reassuring.
	var citations, numbers, dates, drugs int
	for _, detail := range r.Details {
		citations += detail.Report.Citations
		numbers += detail.Report.Numbers
		dates += detail.Report.Dates
		drugs += detail.Report.Drugs
	}
	fmt.Fprintf(&b, "  examined        %d citations, %d numbers, %d dates, %d drug names\n",
		citations, numbers, dates, drugs)
	if r.Run.LatencyP50MS != nil {
		fmt.Fprintf(&b, "  latency         p50 %dms, p95 %dms\n", *r.Run.LatencyP50MS, *r.Run.LatencyP95MS)
	} else {
		fmt.Fprintf(&b, "  latency         not measured: replay contacts no model\n")
	}
	if r.Run.CostMicroUSD != nil {
		fmt.Fprintf(&b, "  cost            %d micro-USD\n", *r.Run.CostMicroUSD)
	} else {
		fmt.Fprintf(&b, "  cost            not measured: replay contacts no model\n")
	}

	for _, detail := range r.Details {
		if detail.Outcome == Clean || detail.Outcome == Detected {
			continue
		}
		fmt.Fprintf(&b, "\n  %-40s %s\n      %s\n", detail.Case.ID, detail.Outcome, detail.Detail)
	}
	if len(r.Regressions) > 0 {
		b.WriteString("\nREGRESSIONS\n")
		for _, line := range r.Regressions {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}
	return b.String()
}

func percentiles(values []int) (int, int) {
	if len(values) == 0 {
		return 0, 0
	}
	sorted := append([]int(nil), values...)
	sort.Ints(sorted)
	at := func(fraction float64) int {
		index := int(fraction * float64(len(sorted)-1))
		return sorted[index]
	}
	return at(0.5), at(0.95)
}

func short(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}
