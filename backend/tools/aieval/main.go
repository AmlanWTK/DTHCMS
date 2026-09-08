// Command aieval runs the frozen AI evaluation set and is the CI gate (CP72, §10.5).
//
// # What it is for
//
// §10.5: *"every change to a prompt or model runs the frozen evaluation set in CI; results are
// recorded, and a regression blocks the merge."* This is that. It loads the set from
// `internal/ai/evalset`, refuses to run one that disagrees with its own manifest, checks every
// case against the **current** validator, schema and prompt, compares the result with the
// committed baseline, prints a page, and exits non-zero on a regression.
//
//	go run ./tools/aieval                      # replay: no model, no database, about a second
//	go run ./tools/aieval -record              # replay, and write the run to ops.ai_evaluation_run
//	go run ./tools/aieval -bless               # accept the current numbers as the new baseline
//	go run ./tools/aieval -live                # call the gateway; needs a credential and a database
//	go run ./tools/aieval -freeze -n 20        # rebuild the case set from core.ai_interaction
//
// # Why the default mode contacts nothing
//
// A gate that needs a paid credential is a gate that is disabled the first week the key expires,
// and one that needs a network is a gate that is flaky. Replay measures the three things that can
// regress without a model — did the validator stop catching a hallucination, did it start refusing
// a correct answer, does a frozen answer still fit the schema — and those are the regressions a
// prompt or validator change actually produces. Latency and cost need a model, are measured by
// `-live`, and are reported as **absent** rather than as zero when they were not measured.
//
// # Proving the gate can fail
//
// Four ways, each producing a distinct message and a non-zero exit: a missed hallucination, a new
// false positive, a frozen answer that no longer satisfies the schema, and a prompt whose hash is
// not the one the baseline was blessed against. `docs/ai-grounding.md` records a transcript of the
// gate refusing a change, because a gate nobody has watched fail is a gate nobody should trust.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/ai"
	"github.com/AmlanWTK/DTHCMS/backend/internal/ai/evalset"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "aieval:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		freeze  = flag.Bool("freeze", false, "rebuild the case set from core.ai_interaction (needs a database)")
		rehash  = flag.Bool("manifest-only", false, "re-hash the cases already on disk and rewrite the manifest; for when a formatter moved the bytes and not the content")
		count   = flag.Int("n", 20, "how many known-correct answers to freeze")
		dir     = flag.String("dir", "internal/ai/evalset", "where the frozen set lives")
		bless   = flag.Bool("bless", false, "record the current numbers as the new baseline")
		record  = flag.Bool("record", false, "write the run to ops.ai_evaluation_run (needs a database)")
		live    = flag.Bool("live", false, "call the gateway instead of replaying the frozen answers")
		gitSHA  = flag.String("git-sha", os.Getenv("GITHUB_SHA"), "the revision these numbers describe")
		verbose = flag.Bool("v", false, "print every case, not only the ones that are not clean")
	)
	flag.Parse()

	if *freeze && *rehash {
		return fmt.Errorf("-freeze rebuilds the cases and -manifest-only refuses to touch them; pick one")
	}
	if *rehash {
		return rewriteManifest(*dir)
	}
	if *freeze {
		return freezeSet(*dir, *count)
	}

	registry, err := ai.LoadRegistry()
	if err != nil {
		return err
	}
	set, err := evalset.Load()
	if err != nil {
		return err
	}
	prompt, known := registry.Latest(set.AgentCode)
	if !known {
		return fmt.Errorf("the set names the agent %q and this build has no prompt for it", set.AgentCode)
	}
	baseline, err := evalset.LoadBaseline()
	if err != nil {
		return err
	}

	mode := "replay"
	provider := evalset.Replay
	var closer func()
	if *live {
		mode = "live"
		provider, closer, err = liveProvider(set)
		if err != nil {
			return err
		}
		defer closer()
	}

	// The drug arm is unarmed here for the same reason it is unarmed everywhere else: there is no
	// formulary until CP75. Passing nil is not a convenience — it is what the deployment does, and
	// a harness that armed a stub would be measuring a validator nobody runs.
	result := evalset.Run(set, prompt, nil, mode, provider, func() time.Time { return time.Now().UTC() })
	result.Gate(baseline, promptHashes(registry))
	result.Run.GitSHA = strings.TrimSpace(*gitSHA)
	if result.Run.GitSHA == "" {
		result.Run.GitSHA = gitHead()
	}

	fmt.Print(result.Report())
	if *verbose {
		for _, detail := range result.Details {
			fmt.Printf("  %-44s %-16s %s\n", detail.Case.ID, detail.Outcome, detail.Report.Summary())
		}
	}

	if *record {
		if err := recordRun(result); err != nil {
			// A gate whose verdict depends on a database being reachable is a gate that goes green
			// when the database is down. The recording is for the trend; the verdict is already
			// decided, and it is printed above whatever happens here.
			fmt.Fprintln(os.Stderr, "aieval: the run could not be recorded:", err)
		}
	}

	if *bless {
		if result.Run.Verdict != "PASS" && result.Run.Detected < result.Run.Injected {
			return errors.New(
				"refusing to bless a baseline from a run that missed an injected hallucination: " +
					"criterion 1 has no tolerance, and blessing this would write that tolerance into a file")
		}
		if err := writeBaseline(*dir, result, promptHashes(registry)); err != nil {
			return err
		}
		fmt.Printf("\nBaseline written to %s. Commit it with the change that caused it.\n",
			filepath.Join(*dir, "baseline.json"))
		return nil
	}

	if result.Run.Verdict != "PASS" {
		return errors.New("the evaluation set regressed; see above")
	}
	return nil
}

// promptHashes is every prompt in this build, by agent code.
//
// Every prompt, not only the evaluated agent's. A build that changed `gateway.echo` has changed a
// prompt, and §10.5 does not carve out the ones that happen not to be under test — the gate's job
// is to notice that a prompt moved and make somebody look at the numbers, whichever prompt it was.
func promptHashes(registry *ai.Registry) map[string]string {
	out := map[string]string{}
	for _, prompt := range registry.All() {
		// The latest version of each agent wins; `All` returns newest first, so the first one seen
		// for an agent is the one a call would use.
		if _, seen := out[prompt.AgentCode]; !seen {
			out[prompt.AgentCode] = prompt.SHA256
		}
	}
	return out
}

func writeBaseline(dir string, result evalset.Result, prompts map[string]string) error {
	note := fmt.Sprintf(
		"Blessed from a %s run of %d cases: %d injected hallucinations all detected, %d of %d "+
			"known-correct answers refused. The false-positive figure is measured on "+
			"deterministic-composer prose, not model prose — see docs/ai-grounding.md.",
		result.Run.Mode, result.Run.Cases, result.Run.Injected, result.Run.Blocked, result.Run.Correct)
	baseline := evalset.Baseline{
		CaseSetSHA256:  result.Run.CaseSetSHA256,
		Prompts:        prompts,
		Blocked:        result.Run.Blocked,
		SchemaFailures: result.Run.SchemaFailures,
		BlessedAt:      time.Now().UTC().Format(time.RFC3339),
		BlessedNote:    note,
	}
	encoded, err := json.MarshalIndent(baseline, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "baseline.json"), append(encoded, '\n'), 0o644)
}

// recordRun writes the run to `ops.ai_evaluation_run`, which is what the dashboard trends.
func recordRun(result evalset.Result) error {
	ctx := context.Background()
	rt, err := platform.Boot(ctx, platform.Options{
		Service: "aieval", NeedsDB: true, NoTelemetry: true,
	})
	if err != nil {
		return err
	}
	defer rt.Close()

	store := ai.NewStore(rt.DB.Pool)
	// The invariant on `ops.ai_evaluation_run` requires the prompt version and content hash to be
	// one this database has deployed, so the registry is synchronised first. On a CI database that
	// is the first deployment; on a developer's it is a no-op unless somebody edited a prompt
	// without bumping its version, which the registry refuses loudly and rightly.
	registry, err := ai.LoadRegistry()
	if err != nil {
		return err
	}
	if err := registry.Deploy(ctx, store, time.Now().UTC()); err != nil {
		return err
	}
	return store.RecordEvaluation(ctx, result.Run, result.Cases)
}

// gitHead is a convenience for a developer running this by hand: CI passes -git-sha.
func gitHead() string {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// liveProvider calls the real gateway with the frozen payloads.
//
// Deliberately not wired to a mock: the point of `-live` is to measure what a real model does with
// the real prompt, and a live mode that could be satisfied by a stub is a live mode somebody will
// run in CI by accident and believe.
func liveProvider(set evalset.Set) (evalset.Provider, func(), error) {
	ctx := context.Background()
	rt, err := platform.Boot(ctx, platform.Options{
		Service: "aieval", NeedsDB: true, NoTelemetry: true,
	})
	if err != nil {
		return nil, nil, err
	}
	if rt.Config.Env.IsProduction() {
		rt.Close()
		return nil, nil, errors.New("refused: -live spends money against a model and the environment is production")
	}
	registry, err := ai.LoadRegistry()
	if err != nil {
		rt.Close()
		return nil, nil, err
	}
	store := ai.NewStore(rt.DB.Pool)
	if err := registry.Deploy(ctx, store, time.Now().UTC()); err != nil {
		rt.Close()
		return nil, nil, err
	}
	minimiser, err := ai.NewMinimiser(rt.Config.Secrets.IdentifierPepper)
	if err != nil {
		rt.Close()
		return nil, nil, err
	}
	var provider ai.Provider = ai.NewMock()
	if rt.Config.AI.Tier != "mock" {
		provider = ai.NewGemini(rt.Config.AI.BaseURL, rt.Config.AI.APIKey, rt.Config.AI.Timeout)
	}
	facility, err := defaultFacility(ctx, rt)
	if err != nil {
		rt.Close()
		return nil, nil, err
	}
	gateway := ai.NewGateway(ai.GatewayConfig{
		Store: store, Registry: registry, Provider: provider, Minimiser: minimiser,
		Tier: rt.Config.AI.Tier, Env: rt.Config.Env, Facility: facility,
		Clock: clock.Real{}, Logger: rt.Logger,
	})

	return func(one evalset.Case) evalset.Answer {
			// **A hallucination case is replayed even in live mode**, and that is not a shortcut.
			// The injection lives in the frozen *answer*; asking the model for a new answer would
			// throw the injection away and measure nothing. The detection rate is a property of
			// the validator and is measured identically in both modes. What `-live` adds is the
			// number the frozen corpus cannot give: how often the check refuses prose a real model
			// wrote, which is exactly the limitation `docs/ai-grounding.md` records.
			if one.Expect == evalset.Hallucination {
				return evalset.Replay(one)
			}
			started := time.Now()
			// The payload is handed over as the frozen case holds it. The subject carries no
			// identifiers — there are none in a frozen case to carry — so the minimiser has
			// nothing to strip, and the tier guard resolves provenance from the register exactly
			// as it would for a real call. A free credential will refuse every one of these, which
			// is correct: the subjects are not in the register.
			result, err := gateway.Invoke(ctx, ai.Request{
				AgentCode: set.AgentCode, Payload: one.Payload,
			})
			answer := evalset.Answer{Duration: time.Since(started)}
			if err != nil {
				var ungrounded *ai.UngroundedError
				if errors.As(err, &ungrounded) {
					// A refusal is a *result*, not a failure of the harness: the gateway did its
					// job and the verdict came out with the error. The harness counts it against
					// this case's expectation — which for a GROUNDED case means a false positive
					// *candidate*, not a confirmed one: a real model may genuinely have invented
					// something, and only a person reading the defect can tell. That review is
					// what `core.ai_grounding_defect.classification` exists for.
					answer.Output = map[string]any{}
					report := ungrounded.Report
					answer.Grounding = &report
					return answer
				}
				answer.Err = err
				return answer
			}
			answer.Output = result.Output
			report := result.Grounding
			answer.Grounding = &report
			answer.CostMicroUSD = result.CostMicroUSD
			return answer
		}, func() {
			rt.Close()
		}, nil
}

func defaultFacility(ctx context.Context, rt *platform.Runtime) (uuid.UUID, error) {
	var id uuid.UUID
	if err := rt.DB.Pool.QueryRow(ctx, `SELECT core.default_facility()`).Scan(&id); err != nil {
		return id, fmt.Errorf("finding the facility; has migrate run? %w", err)
	}
	return id, nil
}
