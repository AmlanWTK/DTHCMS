// Package ai is the single controlled path to any model (CP70, §10.3).
//
// # Why this exists before any agent does
//
// §7.2 names ten agents. PHI minimisation, cost metering, prompt versioning and auditability are
// either inherited by all ten or retrofitted into all ten, and the second of those does not happen:
// by the time there are nine agents in production, the tenth is written by copying the ninth, and
// whichever of the four the ninth forgot is now a property of the system. So the gateway is built
// first, and it is the only thing in this repository that may call [Provider.Generate].
//
// # Acceptance criterion 1b, which is the sharp one
//
// *"A real-patient payload cannot be sent on a free-tier credential."* The obvious implementation
// is a boolean on the request. It fails in the way that matters: an agent author who forgets the
// flag sends a real patient's clinical picture to a tier whose terms say Google trains on it and
// human reviewers read it (D-07), and nothing anywhere notices. A flag a caller sets is a claim
// nobody checks.
//
// **So there is no such flag.** [Request] has no field for it, and there is nowhere to add one
// without changing this package. Provenance is *resolved*, by [Store.ProvenanceOf], from a register
// of record ids somebody deliberately entered — and every branch of that function except "the
// register says this one is fabricated" returns something the tier guard refuses. A subject nobody
// registered is real. A payload with no subject is real. A lookup the database could not answer is
// real: a database that cannot answer must not become permission.
//
// That is the shape the brief asks for — the safe answer is the default, and the unsafe one has to
// be argued for. Three further layers sit behind it, none of them relying on the caller:
//
//   - the gateway refuses the free tier outside local, test and dev, whatever the register says,
//     because those are the only environments where the deployment as a whole is not allowed to
//     hold real patient data;
//   - `config.Load` already refuses `DTHCMS_AI_TIER=free` in production, so the credential cannot
//     be configured there at all;
//   - a check constraint on `core.ai_interaction` refuses to *record* a free-tier call that is not
//     synthetic, and invariant 99 re-checks the whole table — so even a bug in this file cannot
//     produce a row saying it happened.
//
// **The residual risk, stated plainly.** Whoever can write to `core.ai_synthetic_subject` can enter
// a real patient's id, and that patient's data would then be sendable on a free credential. That is
// a deliberate act with an author, a timestamp and a twenty-character reason on it, rather than a
// forgotten boolean — which is the entire improvement, and it is a real one. It is not a proof, and
// nothing here should be read as claiming otherwise. The register is written by `cmd/synthload` and
// by nothing else in this repository; a person with database access can bypass every word of this
// file, as they can bypass every other rule in the system.
//
// # What Invoke does, in order
//
// §10.3's numbered list, with two additions and one omission.
//
//	1  resolve the agent's prompt version, model, schema, timeout and budget from the registry
//	2  resolve provenance from the register — never from the request
//	3  minimise (D-08); a payload that still names a person is refused and recorded as refused
//	4  the tier guard; a refusal is recorded before it is returned
//	5  check the cache on the hash of the *minimised* payload
//	6  record the call, before contacting anybody
//	7  call the provider under the agent's timeout, with retry, backoff and a circuit breaker
//	8  validate the answer against the agent's schema; retry on a violation; fail cleanly at the end
//	9  ground the answer against the payload the model was shown (CP72); a violation is refused,
//	   recorded as a defect, and never retried
//	10 finish the record: tokens, cost, latency, validation result, which model actually answered
//	11 meter the spend and fire a budget alert if a threshold was crossed
//	12 restore the subject's identifiers and return, marked ai_generated
//
// Step 9 is §10.3's own step 7, added at CP72 and described in `grounding.go`. It runs in exactly
// one function — [Gateway.deliver] — through which every path that could hand a caller a model's
// answer passes, including the cache hit. Three exits would have been three places to forget it.
//
// The additions are steps 3-and-4-are-recorded and step 11. Recording a refusal matters because a
// call that was refused is exactly the one somebody asks about later, and a gateway that only
// recorded the calls that happened would have no answer.
//
// # What this package will not do
//
// It imports `platform` and nothing else — `architecture.json` allows it nothing else — so it does
// not know what a patient is, cannot read the clinical record, and cannot write to it. §10.6's
// first permanent invariant is *"AI never writes to the clinical record"*, and the cheapest way to
// hold that for ten agents is for the thing they all call to be structurally incapable of it. An
// agent assembles its own payload from its own module and hands it here.
package ai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/config"
)

// Errors the gateway returns. Every one of them leaves a row in `core.ai_interaction` behind it,
// except [ErrUnknownAgent], which is not a call.
var (
	// ErrUnknownAgent is an agent code with no prompt in the registry. Nothing is recorded: there
	// is no prompt version to record it against, and a row naming an agent that does not exist
	// would be a row nobody can reproduce. It is a programming error and it fails immediately.
	ErrUnknownAgent = errors.New("ai: no prompt is registered for that agent")
	// ErrFreeTierRefused is criterion 1b happening. The call is not made, not downgraded and not
	// retried; the physician's screen degrades per D-15.
	ErrFreeTierRefused = errors.New("ai: a free-tier credential may only carry fabricated data")
	// ErrInvalidOutput is a model answer that failed its schema on every attempt. Criterion 4's
	// "ultimately fails cleanly rather than being passed through".
	ErrInvalidOutput = errors.New("ai: the model's answer did not match the agent's schema")
	// ErrCircuitOpen is a model this process has decided is down. Returned immediately, which is
	// the point of the breaker.
	ErrCircuitOpen = errors.New("ai: the circuit for that model is open")
	// ErrUnknownModel is a prompt pinning a model version that is not in the database catalogue.
	// Refused rather than defaulted, because the catalogue is where the price is, and a call whose
	// cost cannot be computed is a call that breaks the metering criterion 5 depends on.
	ErrUnknownModel = errors.New("ai: the prompt pins a model version that is not in the catalogue")
	// ErrUngrounded is CP72's block. The model answered, the answer satisfied its schema, and it
	// said something the model was not shown.
	//
	// **Not retried, and that is the difference from [ErrInvalidOutput].** A malformed answer is a
	// transport problem and the second attempt usually fixes it. An invented HbA1c is a quality
	// problem: retrying it spends money to destroy the evidence, the next answer probably passes,
	// and the rate of the failure this whole checkpoint exists to measure reads as zero. So the
	// answer is refused, the findings become rows in `core.ai_grounding_defect`, and the caller
	// degrades per D-15.
	ErrUngrounded = errors.New("ai: the model's answer makes a claim the context does not support")
)

// Clock is the small slice of time this package needs.
type Clock interface{ Now() time.Time }

// AlertRaiser puts a budget crossing in front of an administrator.
//
// An interface rather than an import, because `architecture.json` gives this module `platform` and
// nothing else — and it should stay that way. The composition root wires `audit`'s store in, which
// is the same shape as the job registry: the thing that knows how to raise an alert is not the
// thing that knows when to.
//
// Optional. A gateway with no raiser still records the crossing in `core.ai_budget_alert` and logs
// it at warn; what it loses is the administrator's console, which is a degradation rather than a
// failure and is not worth refusing to start over.
type AlertRaiser interface {
	RaiseBudgetAlert(ctx context.Context, alert BudgetAlert) error
}

// Gateway is the only path to a model.
type Gateway struct {
	store     *Store
	registry  *Registry
	provider  Provider
	minimiser *Minimiser
	breaker   *Breaker
	metrics   *Instruments

	tier     config.AITier
	env      config.Environment
	facility uuid.UUID

	alerts AlertRaiser
	clock  Clock
	logger *slog.Logger

	// drugs is CP75's formulary, when there is one. Nil today, and nil means the drug arm of the
	// grounding check refuses every drug name rather than passing it — see [DrugCheck].
	drugs DrugCheck

	// jitter is the randomness in the retry backoff. A field so a test can make the timing
	// deterministic; nothing in production sets it.
	jitter func() float64
}

// GatewayConfig is what a gateway needs.
type GatewayConfig struct {
	Store     *Store
	Registry  *Registry
	Provider  Provider
	Minimiser *Minimiser
	Breaker   *Breaker
	Metrics   *Instruments

	Tier     config.AITier
	Env      config.Environment
	Facility uuid.UUID

	Alerts AlertRaiser
	Clock  Clock
	Logger *slog.Logger

	// Drugs is the formulary the grounding check's drug arm consults. Supplied by the composition
	// root when CP75 exists; nil until then, which arms the check to refuse rather than to shrug.
	Drugs DrugCheck

	Jitter func() float64
}

// NewGateway builds one.
func NewGateway(cfg GatewayConfig) *Gateway {
	breaker := cfg.Breaker
	if breaker == nil {
		breaker = NewBreaker(3, 30*time.Second, func() time.Time { return cfg.Clock.Now() })
	}
	jitter := cfg.Jitter
	if jitter == nil {
		jitter = rand.Float64
	}
	return &Gateway{
		store: cfg.Store, registry: cfg.Registry, provider: cfg.Provider,
		minimiser: cfg.Minimiser, breaker: breaker, metrics: cfg.Metrics,
		tier: cfg.Tier, env: cfg.Env, facility: cfg.Facility,
		alerts: cfg.Alerts, clock: cfg.Clock, logger: cfg.Logger,
		drugs: cfg.Drugs, jitter: jitter,
	}
}

// Request is one invocation.
//
// There is no field here saying whether the subject is real or fabricated, and adding one would
// defeat criterion 1b. See the package comment.
type Request struct {
	// AgentCode names the prompt in the registry.
	AgentCode string
	// Subject is who this is about, and the identifiers to strike out and put back.
	Subject Subject
	// Payload is the structured clinical context, already assembled by the calling agent. Free text
	// is a string value in it like any other; the minimiser scrubs every string it finds, at any
	// depth, so there is no separate "and here is the prose" field for somebody to forget.
	Payload map[string]any
}

// Result is what an agent gets back.
type Result struct {
	InteractionID uuid.UUID `json:"interaction_id"`
	AgentCode     string    `json:"agent_code"`
	PromptVersion string    `json:"prompt_version"`
	ModelVersion  string    `json:"model_version"`

	// Output is the validated object, with the subject's identifiers restored into every string.
	Output map[string]any `json:"output"`

	// AIGenerated is always true and is never omitted. §10.3 step 9 calls it a mandatory marker and
	// §10.6's third permanent invariant requires every AI-derived element to be marked wherever it
	// appears. A field that is always true looks redundant right up until something serialises this
	// struct into a record and the marker is the only thing distinguishing a draft from a fact.
	AIGenerated bool `json:"ai_generated"`

	Cached       bool `json:"cached"`
	UsedFallback bool `json:"used_fallback"`

	InputTokens  int           `json:"input_tokens"`
	OutputTokens int           `json:"output_tokens"`
	CostMicroUSD int64         `json:"cost_micro_usd"`
	Latency      time.Duration `json:"-"`

	// Grounding is §10.2 step 4's verdict on this answer. A [Result] only ever exists with a
	// verdict of PASSED or NOT_REQUIRED — a failed one is [ErrUngrounded] and no output at all —
	// so this field is here for the caller to *record*, not to check. `core.ai_synthesis` has a
	// column for it and a check constraint that refuses a physician-readable row without it, which
	// is the second of the three independent enforcements described in migration 00054.
	Grounding GroundingReport `json:"grounding"`
}

// Invoke is the whole of §10.3.
func (g *Gateway) Invoke(ctx context.Context, req Request) (Result, error) {
	started := g.clock.Now()

	// 1. The agent's configuration. An unknown agent fails here and is not recorded: there is no
	// prompt version to record it against, and it is a programming error rather than a call.
	prompt, known := g.registry.Latest(req.AgentCode)
	if !known {
		return Result{}, fmt.Errorf("%w: %s", ErrUnknownAgent, req.AgentCode)
	}
	models, err := g.store.Models(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("ai: reading the model catalogue: %w", err)
	}
	model, pinned := models[prompt.ModelVersion]
	if !pinned {
		return Result{}, fmt.Errorf("%w: %s pins %s", ErrUnknownModel, prompt.AgentCode, prompt.ModelVersion)
	}

	// 2. Provenance, by lookup. Nothing from the request contributes to this.
	provenance, err := g.store.ProvenanceOf(ctx, req.Subject.PatientID)
	if err != nil {
		// The lookup already returned REAL_PATIENT; the error is logged rather than returned so
		// that a slow register degrades the tier available rather than the clinic's AI. On a paid
		// or mock credential the call proceeds normally; on a free one it is about to be refused,
		// which is the correct outcome of not knowing.
		g.logger.WarnContext(ctx, "the synthetic-subject register could not be read; treating the subject as a real patient",
			"agent_code", req.AgentCode, "error", err.Error())
	}

	rec := recording{
		ID: uuid.New(), FacilityID: g.facility, AgentCode: req.AgentCode,
		PromptVersion:         prompt.Version,
		RequestedModelVersion: prompt.ModelVersion,
		Tier:                  string(g.tier), Provenance: provenance,
		SubjectPatientID: req.Subject.PatientID,
		StartedAt:        started,
	}

	// 3. Minimisation. A payload that still names a person is refused, and the refusal is recorded
	// without the payload — storing it would move the leak from the provider to our own table.
	minimised, err := g.minimiser.Minimise(prompt.AgentCode, req.Subject, req.Payload)
	if err != nil {
		rec.Status = "REFUSED_PHI"
		rec.SubjectPseudonym = g.minimiser.Pseudonym(req.Subject.PatientID, prompt.AgentCode)
		rec.RefusalDetail = err.Error()
		// The hash of nothing, so the column's format constraint is satisfied without inventing a
		// hash of a payload that must not be stored. It cannot collide with a real input hash for
		// the obvious reason that no real payload is empty.
		rec.InputSHA256 = hashOf(nil)
		finished := g.clock.Now()
		rec.FinishedAt = &finished
		g.record(ctx, rec)
		g.metrics.Refused(ctx, req.AgentCode, "phi")
		return Result{}, err
	}
	rec.SubjectPseudonym = minimised.Pseudonym

	outbound, err := jsonOrNil(map[string]any{
		"payload": minimised.Payload,
		// What the scrubber took out, by kind and count, never the value. It is on the outbound
		// record rather than in a log because this is the artefact the plan calls the mitigation
		// for its own headline risk, and a reviewer needs to see the scrubber working.
		"removed":        minimised.Removed,
		"prompt_version": prompt.Version,
	})
	if err != nil {
		return Result{}, err
	}
	rec.Outbound = outbound
	rec.InputSHA256 = hashOf(outbound)

	// 4. The tier guard. Two conditions, and both must hold.
	if refusal := g.tierRefusal(provenance); refusal != "" {
		rec.Status = "REFUSED_TIER"
		rec.RefusalDetail = refusal
		// The payload is dropped from the record here as well. It is safe by construction — three
		// checks say so — but a refused call is one nobody reviewed, and keeping the payload of a
		// call we decided not to make is keeping something for no reader.
		rec.Outbound = nil
		finished := g.clock.Now()
		rec.FinishedAt = &finished
		g.record(ctx, rec)
		g.metrics.Refused(ctx, req.AgentCode, "tier")
		// At warn and named, because this is the one refusal in the system that means somebody
		// nearly sent a patient's data to a tier that trains on it.
		g.logger.WarnContext(ctx, "an AI call was refused by the tier guard",
			"agent_code", req.AgentCode, "tier", string(g.tier), "provenance", string(provenance))
		return Result{}, fmt.Errorf("%w: %s", ErrFreeTierRefused, refusal)
	}

	// 5. The cache, on the hash of the minimised payload.
	if prompt.CacheTTLSeconds > 0 {
		notBefore := started.Add(-time.Duration(prompt.CacheTTLSeconds) * time.Second)
		if cached, hit, err := g.store.Cache(ctx, rec.InputSHA256, notBefore); err != nil {
			// A cache that cannot be read is not a reason to refuse a clinical call. Log and pay
			// for the model.
			g.logger.WarnContext(ctx, "the AI response cache could not be read", "error", err.Error())
		} else if hit {
			return g.serveFromCache(ctx, rec, prompt, minimised, cached, started)
		}
	}

	// 6. The record, before anybody is contacted.
	rec.Status = "IN_FLIGHT"
	if _, _, err := g.store.Begin(ctx, rec); err != nil {
		// A call that cannot be recorded does not happen. That is criterion 2 taken seriously: the
		// alternative is a system that, when its audit table is unavailable, quietly carries on
		// sending patient data to a third party with no record of having done so.
		return Result{}, fmt.Errorf("ai: the call was not made because it could not be recorded: %w", err)
	}

	// 7-8. Call, retry, validate.
	outcome := g.callWithRetries(ctx, prompt, model, models, minimised)

	// 9. Finish the record, whatever happened.
	rec.Status = outcome.status
	rec.ModelVersion = outcome.modelVersion
	rec.InputTokens, rec.OutputTokens = outcome.inputTokens, outcome.outputTokens
	rec.CostMicroUSD = outcome.cost
	rec.Attempts = outcome.attempts
	rec.UsedFallback = outcome.usedFallback
	rec.OutputValid = outcome.outputValid
	rec.RefusalDetail = ""
	finished := g.clock.Now()
	rec.LatencyMS = int(finished.Sub(started) / time.Millisecond)
	rec.FinishedAt = &finished
	if outcome.response != nil {
		encoded, err := jsonOrNil(outcome.response)
		if err != nil {
			return Result{}, err
		}
		rec.Response = encoded
	}
	if outcome.err != nil {
		// The error text goes in refusal_detail, which is where an operator reading the outbound
		// log looks for "and why did that one fail". It is the provider's message, truncated by the
		// adapter, and never the payload.
		rec.RefusalDetail = outcome.err.Error()
	}
	if err := g.store.Finish(ctx, rec); err != nil {
		g.logger.ErrorContext(ctx, "an AI call completed but its record could not be finished",
			"agent_code", req.AgentCode, "interaction_id", rec.ID.String(), "error", err.Error())
	}
	g.metrics.Finished(ctx, req.AgentCode, rec.ModelVersion, outcome.status,
		finished.Sub(started), outcome.cost, outcome.inputTokens+outcome.outputTokens)

	// 10. Metering. After the record, so a spend figure can never count a call the record does not
	// have; and outside the error branch, because a failed call still consumed input tokens and
	// still cost money.
	g.meter(ctx, req.AgentCode, finished)

	if outcome.err != nil {
		return Result{}, outcome.err
	}

	// 9, 12. Ground, restore, and mark.
	return g.deliver(ctx, rec, minimised, outcome.response, Result{
		InteractionID: rec.ID, AgentCode: req.AgentCode,
		PromptVersion: prompt.Version, ModelVersion: outcome.modelVersion,
		AIGenerated:  true,
		UsedFallback: outcome.usedFallback,
		InputTokens:  outcome.inputTokens, OutputTokens: outcome.outputTokens,
		CostMicroUSD: outcome.cost, Latency: finished.Sub(started),
	}, finished)
}

// deliver is the only way a model's answer leaves this package.
//
// # Why every exit funnels through one function
//
// There are three places a [Result] can be built — the ordinary path, a cache hit, and the retry
// after an undecodable cache entry — and a grounding check written at each of them is a grounding
// check that the fourth exit, added in two years by somebody reading the second, will not have.
// So the three build a Result with no `Output` and hand it here, and this is the only line in the
// package that sets `Output`. A future exit that forgets is a compile-time nothing and a runtime
// answer with no output at all, which fails loudly at its first caller rather than quietly at a
// physician's screen.
//
// # What is checked, and against what
//
// The **unrestored** answer — the object the model actually produced, still carrying the
// pseudonym — against the **minimised payload**, which is the object the model was actually shown.
// Both halves matter. Checking the restored answer would compare a narrative containing a
// patient's name against a payload that by construction contains none, and would put that name in
// every defect excerpt. Checking against the caller's pre-minimisation payload would ground the
// model on values the scrubber removed before it ever saw them.
//
// # The order of the writes
//
// The record is finished as SUCCEEDED before the verdict is written, and the verdict is then one
// statement that sets `grounding_state`, `grounding_findings` and `status` together. That is not
// arbitrary: migration 00054's constraints make `status = 'UNGROUNDED'` legal only alongside a
// failed verdict with findings, so a row cannot pass through a moment of claiming to be
// ungrounded without saying why. A process that dies between the two leaves a SUCCEEDED row whose
// `grounding_state` is NOT_CHECKED — visible, honest, and not an answer any caller received,
// because this function had not returned.
func (g *Gateway) deliver(ctx context.Context, rec recording, minimised Minimised,
	response map[string]any, result Result, at time.Time) (Result, error) {

	required, exemption, err := g.store.GroundingRequired(ctx, rec.AgentCode)
	if err != nil {
		// Fail closed, the same rule the provenance lookup follows. A register that cannot be read
		// grounds the answer; the cost is a summary withheld on a bad database day, and D-15's
		// degraded screen is built for exactly that.
		g.logger.WarnContext(ctx, "the agent register could not be read; grounding this answer anyway",
			"agent_code", rec.AgentCode, "error", err.Error())
		required = true
	}
	if !required {
		result.Grounding = GroundingReport{State: GroundingNotRequired, DrugArm: "UNARMED"}
		if g.drugs != nil {
			result.Grounding.DrugArm = "ARMED"
		}
		if err := g.store.RecordGrounding(ctx, rec.ID, GroundingNotRequired, 0, rec.Status); err != nil {
			g.logger.ErrorContext(ctx, "an AI answer's grounding exemption could not be recorded",
				"agent_code", rec.AgentCode, "interaction_id", rec.ID.String(), "error", err.Error())
		}
		g.logger.DebugContext(ctx, "an AI answer was not grounded because its agent is exempt",
			"agent_code", rec.AgentCode, "reason", exemption)
		result.Output, _ = minimised.RestoreInto(response).(map[string]any)
		return result, nil
	}

	report := GroundsFrom(minimised.Payload).Check(response, g.drugs)
	g.metrics.Grounded(ctx, rec.AgentCode, rec.ModelVersion, report)

	status := rec.Status
	if report.State == GroundingFailed {
		status = "UNGROUNDED"
	}
	if err := g.store.RecordGrounding(ctx, rec.ID, report.State, len(report.Findings), status); err != nil {
		g.logger.ErrorContext(ctx, "an AI answer's grounding verdict could not be recorded",
			"agent_code", rec.AgentCode, "interaction_id", rec.ID.String(),
			"grounding_state", string(report.State), "error", err.Error())
	}
	if report.State != GroundingFailed {
		result.Grounding = report
		result.Output, _ = minimised.RestoreInto(response).(map[string]any)
		return result, nil
	}

	// The defect, before the refusal. *"Grounding violations recorded as defects, not silently
	// retried."* A write that fails is logged at error and does not change the refusal — the
	// refusal is the safety property and must not depend on this table being writable — but it
	// leaves an UNGROUNDED interaction with no defect beside it, which is exactly what invariant
	// 107 exists to find.
	if err := g.store.RecordDefects(ctx, Recording{
		InteractionID: rec.ID, FacilityID: rec.FacilityID, AgentCode: rec.AgentCode,
		PromptVersion: rec.PromptVersion, ModelVersion: rec.ModelVersion,
		SubjectPatientID: rec.SubjectPatientID, SubjectPseudonym: rec.SubjectPseudonym,
	}, report.Findings, at); err != nil {
		g.logger.ErrorContext(ctx, "an ungrounded AI answer's defects could not be recorded",
			"agent_code", rec.AgentCode, "interaction_id", rec.ID.String(), "error", err.Error())
	}
	// At error and named. This is the failure §10.6's fifth permanent invariant exists to prevent,
	// and a system in which it happens regularly is one whose prompt or model needs work — which
	// is a thing somebody has to be told rather than a row somebody has to notice.
	g.logger.ErrorContext(ctx, "an AI answer was refused because it was not grounded in what the model was shown",
		"agent_code", rec.AgentCode, "interaction_id", rec.ID.String(),
		"prompt_version", rec.PromptVersion, "model_version", rec.ModelVersion,
		"findings", len(report.Findings), "detail", report.Summary())
	return Result{}, &UngroundedError{InteractionID: rec.ID, Report: report}
}

// UngroundedError carries the verdict out with the refusal.
//
// A caller that only needed to know *that* the answer was refused could match [ErrUngrounded] and
// be done; the evaluation harness and the operator screens need to know *what* was wrong, and the
// alternative to a typed error is parsing the sentence back out of a string. `Unwrap` returns
// [ErrUngrounded], so `errors.Is` keeps working for everybody who does not care.
type UngroundedError struct {
	InteractionID uuid.UUID
	Report        GroundingReport
}

func (e *UngroundedError) Error() string {
	return fmt.Sprintf("%s: %s", ErrUngrounded.Error(), e.Report.Summary())
}

func (e *UngroundedError) Unwrap() error { return ErrUngrounded }

// tierRefusal returns why this call may not go out on this credential, or "".
//
// Two independent conditions, and the second is not redundant. The register decides whether *this
// payload* is fabricated; the environment decides whether *this deployment* is one where the
// question can be trusted at all. A staging system restored from a production backup would have
// real patients whose ids nobody has registered — which the first condition catches — but it would
// also be one bad `INSERT` away from having them registered, and the second condition is what makes
// that insufficient rather than fatal.
func (g *Gateway) tierRefusal(provenance Provenance) string {
	if g.tier != config.TierFree {
		return ""
	}
	switch g.env {
	case config.EnvLocal, config.EnvTest, config.EnvDev:
	default:
		return fmt.Sprintf(
			"a free-tier credential is not usable in %s: the Gemini free tier is trained on and "+
				"read by human reviewers, and only an environment that may not hold real patient "+
				"data at all can use it (ADR-0007, D-07)", g.env)
	}
	if !provenance.MayUseFreeTier() {
		return fmt.Sprintf(
			"the subject is %s and the credential is free tier: Google's terms say content sent to "+
				"the free tier is used for training and may be read by human reviewers, so nothing "+
				"derived from a real patient may go out on it (ADR-0007, D-07)", provenance)
	}
	return ""
}

// serveFromCache answers from a previous identical call.
//
// Recorded as its own interaction with `status = CACHED` and a cost of zero, rather than not
// recorded at all. Two reasons. The audit question is "what has this system said about this
// patient", and an answer that was served from the cache was still an answer given — a log that
// omitted it would under-report by however well the cache is working. And the zero cost is what
// makes "identical input is never re-billed" (§10.3 step 4) a thing somebody can *verify* from the
// record rather than a thing the code claims.
func (g *Gateway) serveFromCache(ctx context.Context, rec recording, prompt Prompt,
	minimised Minimised, cached Cached, started time.Time) (Result, error) {

	var response map[string]any
	if err := json.Unmarshal(cached.Response, &response); err != nil {
		// A stored answer that will not decode is a cache miss, not a failure. Fall through to the
		// provider rather than failing a clinical call over our own bookkeeping.
		g.logger.WarnContext(ctx, "a cached AI answer could not be decoded; calling the model instead",
			"agent_code", rec.AgentCode, "cached_interaction_id", cached.ID.String())
		return g.invokeUncached(ctx, rec, prompt, minimised, started)
	}

	rec.Status = "CACHED"
	rec.ModelVersion = cached.ModelVersion
	if rec.ModelVersion == "" {
		rec.ModelVersion = prompt.ModelVersion
	}
	rec.PromptVersion = prompt.Version
	// The tokens the cached call used, so a report of "tokens this agent has needed" is honest
	// about the work, while the cost stays zero because nobody was billed this time. The two
	// columns say different things and both are true.
	rec.InputTokens, rec.OutputTokens = cached.InputTokens, cached.OutputTokens
	rec.CostMicroUSD = 0
	rec.Attempts = 0
	valid := true
	rec.OutputValid = &valid
	rec.Response = cached.Response
	finished := g.clock.Now()
	rec.LatencyMS = int(finished.Sub(started) / time.Millisecond)
	rec.FinishedAt = &finished
	g.record(ctx, rec)
	g.metrics.Finished(ctx, rec.AgentCode, rec.ModelVersion, "CACHED",
		finished.Sub(started), 0, 0)

	// Grounded like any other answer, and not because the verdict could differ — the cache is keyed
	// on the hash of this same payload, so it cannot. Because a cache that skipped the check would
	// be a way to serve an ungrounded answer, and the way to be sure there is no such way is for
	// there to be no exit that does not run it.
	return g.deliver(ctx, rec, minimised, response, Result{
		InteractionID: rec.ID, AgentCode: rec.AgentCode,
		PromptVersion: rec.PromptVersion, ModelVersion: rec.ModelVersion,
		AIGenerated: true, Cached: true,
		InputTokens: cached.InputTokens, OutputTokens: cached.OutputTokens,
		Latency: finished.Sub(started),
	}, finished)
}

// invokeUncached is the tail of Invoke, reached only when a cache hit turned out to be unusable.
func (g *Gateway) invokeUncached(ctx context.Context, rec recording, prompt Prompt,
	minimised Minimised, started time.Time) (Result, error) {

	models, err := g.store.Models(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("ai: reading the model catalogue: %w", err)
	}
	rec.Status = "IN_FLIGHT"
	if _, _, err := g.store.Begin(ctx, rec); err != nil {
		return Result{}, fmt.Errorf("ai: the call was not made because it could not be recorded: %w", err)
	}
	outcome := g.callWithRetries(ctx, prompt, models[prompt.ModelVersion], models, minimised)

	rec.Status = outcome.status
	rec.ModelVersion = outcome.modelVersion
	rec.InputTokens, rec.OutputTokens = outcome.inputTokens, outcome.outputTokens
	rec.CostMicroUSD, rec.Attempts = outcome.cost, outcome.attempts
	rec.UsedFallback, rec.OutputValid = outcome.usedFallback, outcome.outputValid
	finished := g.clock.Now()
	rec.LatencyMS = int(finished.Sub(started) / time.Millisecond)
	rec.FinishedAt = &finished
	if outcome.response != nil {
		if encoded, err := jsonOrNil(outcome.response); err == nil {
			rec.Response = encoded
		}
	}
	if outcome.err != nil {
		rec.RefusalDetail = outcome.err.Error()
	}
	if err := g.store.Finish(ctx, rec); err != nil {
		g.logger.ErrorContext(ctx, "an AI call completed but its record could not be finished",
			"agent_code", rec.AgentCode, "interaction_id", rec.ID.String(), "error", err.Error())
	}
	g.meter(ctx, rec.AgentCode, finished)
	if outcome.err != nil {
		return Result{}, outcome.err
	}
	return g.deliver(ctx, rec, minimised, outcome.response, Result{
		InteractionID: rec.ID, AgentCode: rec.AgentCode,
		PromptVersion: prompt.Version, ModelVersion: outcome.modelVersion,
		AIGenerated: true, UsedFallback: outcome.usedFallback,
		InputTokens: outcome.inputTokens, OutputTokens: outcome.outputTokens,
		CostMicroUSD: outcome.cost, Latency: finished.Sub(started),
	}, finished)
}

// outcome is what the provider loop produced.
type outcome struct {
	status       string
	modelVersion string
	response     map[string]any
	inputTokens  int
	outputTokens int
	cost         int64
	attempts     int
	usedFallback bool
	outputValid  *bool
	err          error
}

// callWithRetries is §10.3 steps 5 and 6: timeout, breaker, retry, schema validation.
//
// # Two kinds of retry, one budget
//
// A provider failure and an invalid answer are different problems with the same remedy — try again
// — and sharing one attempt budget between them is deliberate. Separate budgets multiply: three
// provider attempts times three validation attempts is nine calls to a model for one synthesis, at
// nine times the cost, inside a five-minute SLA. One budget means an agent's `max_attempts` is what
// it says it is.
//
// # The repair instruction
//
// A retry after a schema violation appends what was wrong to the prompt. This is worth doing and
// worth being sceptical about: it materially raises the chance the second answer parses, and it
// also means the second answer was produced from a different prompt than the recorded one. The
// record stores the prompt *version*, and the repair text is derived deterministically from the
// schema and the violations, so the reconstruction is exact — but it is a caveat and it belongs
// here rather than in a footnote.
func (g *Gateway) callWithRetries(ctx context.Context, prompt Prompt, model Model,
	models map[string]Model, minimised Minimised) outcome {

	user, err := prompt.Render(minimised.Payload)
	if err != nil {
		return outcome{status: "PROVIDER_ERROR", err: err, modelVersion: prompt.ModelVersion}
	}

	// The agent's whole budget, across every attempt. §7.1's five minutes is a promise about a
	// pipeline; one model call inside it gets far less, and a per-attempt timeout would let three
	// attempts quietly consume three times what the agent asked for.
	ctx, cancel := context.WithTimeout(ctx, time.Duration(prompt.TimeoutSeconds)*time.Second)
	defer cancel()

	result := outcome{modelVersion: prompt.ModelVersion}
	active := model
	repair := ""

	for attempt := 1; attempt <= prompt.MaxAttempts; attempt++ {
		result.attempts = attempt

		if !g.breaker.Allow(active.ModelVersion) {
			// The primary is down and this process knows it. The fallback is the point of §10.4's
			// two-tier model choice: a Flash answer clearly recorded as a fallback beats no answer,
			// and beats a fabricated one by more (D-15).
			if fallback, ok := g.fallback(prompt, models, active); ok {
				active = fallback
				result.usedFallback = true
				result.modelVersion = active.ModelVersion
				continue
			}
			result.status = "CIRCUIT_OPEN"
			result.err = fmt.Errorf("%w: %s", ErrCircuitOpen, active.ModelVersion)
			return result
		}

		response, err := g.provider.Generate(ctx, ProviderRequest{
			ModelVersion:    active.ModelVersion,
			System:          prompt.System,
			User:            user + repair,
			Temperature:     prompt.Temperature,
			MaxOutputTokens: prompt.MaxOutputTokens,
			OutputSchema:    prompt.OutputSchema,
		})
		if err != nil {
			g.breaker.Failed(active.ModelVersion)
			result.err = err
			result.status = statusFor(ctx, err)

			if !Retryable(err) {
				// A refusal or a rejected request. Retrying is the one thing guaranteed not to
				// help, and three attempts at a safety refusal is three times the cost for the
				// same silence.
				if fallback, ok := g.fallback(prompt, models, active); ok && errors.Is(err, ErrProviderRejected) {
					// Except when the provider says the *request* is wrong, which on a retired
					// model version is exactly what a fallback is for.
					active = fallback
					result.usedFallback = true
					result.modelVersion = active.ModelVersion
					continue
				}
				return result
			}
			if attempt == prompt.MaxAttempts {
				return result
			}
			if !g.wait(ctx, backoffFor(attempt, err, g.jitter)) {
				result.status = "TIMEOUT"
				result.err = fmt.Errorf("%w: the agent's budget ran out during backoff", ErrProviderUnavailable)
				return result
			}
			continue
		}

		g.breaker.Succeeded(active.ModelVersion)
		result.modelVersion = response.ModelVersion
		if result.modelVersion == "" {
			result.modelVersion = active.ModelVersion
		}
		result.inputTokens += response.InputTokens
		result.outputTokens += response.OutputTokens
		// Priced against the model that was *asked*, not the one that answered: the mock reports
		// itself as `mock-000`, which is registered at zero, and pricing a real Gemini call against
		// a version the catalogue does not know would silently make it free.
		result.cost += active.Cost(response.InputTokens, response.OutputTokens)

		object, violations := prompt.OutputSchema.Validate(response.Text)
		if len(violations) == 0 {
			valid := true
			result.outputValid = &valid
			result.status = "SUCCEEDED"
			result.response = object
			result.err = nil
			return result
		}

		invalid := false
		result.outputValid = &invalid
		result.status = "INVALID_OUTPUT"
		result.err = fmt.Errorf("%w: %s", ErrInvalidOutput, describe(violations))
		if attempt == prompt.MaxAttempts {
			// Criterion 4's tail: rejected, retried, and then failed cleanly rather than passed
			// through. The caller gets an error and no output; nothing half-validated escapes.
			result.response = nil
			return result
		}
		repair = repairInstruction(violations)
	}
	return result
}

// fallback returns the registered fallback model, if there is one and it is not already in use.
func (g *Gateway) fallback(prompt Prompt, models map[string]Model, active Model) (Model, bool) {
	if prompt.FallbackModelVersion == "" || active.ModelVersion == prompt.FallbackModelVersion {
		return Model{}, false
	}
	model, known := models[prompt.FallbackModelVersion]
	return model, known
}

// wait sleeps unless the agent's budget runs out first.
func (g *Gateway) wait(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// backoffFor is how long to wait before the next attempt.
//
// The provider's own advice wins when it gave any: a 429 answered with our exponential curve
// instead of its Retry-After is a second 429, and then a third. Otherwise exponential from a second
// with jitter — and the jitter is not decoration. Without it, forty synthesis jobs that failed
// together because Gemini was down retry together, hit it at the same instant, and fail together
// again, which turns a brief outage into a repeating one.
func backoffFor(attempt int, err error, jitter func() float64) time.Duration {
	if advice, ok := RetryAfter(err); ok {
		return advice
	}
	base := time.Second
	for i := 1; i < attempt && base < 30*time.Second; i++ {
		base *= 2
	}
	if base > 30*time.Second {
		base = 30 * time.Second
	}
	spread := float64(base) * 0.5 * (jitter() - 0.5)
	return time.Duration(float64(base) + spread)
}

// statusFor turns a provider failure into the status recorded on the row.
//
// The deadline is asked about twice, and not redundantly: the agent's budget may have expired while
// the call was in flight (the context), or the transport may have reported its own deadline while
// the budget still had room (the error). Both are timeouts on the operator's screen and neither is
// a provider fault, which is the distinction the outbound log is read for.
func statusFor(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "TIMEOUT"
	}
	return "PROVIDER_ERROR"
}

// record writes a terminal row and complains rather than failing when it cannot.
//
// The asymmetry with [Store.Begin] is deliberate. A record that cannot be written *before* a call
// stops the call, because the alternative is sending patient data to a third party with no record
// of it. A record that cannot be written *about a call already refused* has nothing left to
// prevent; failing here would replace a clear refusal with a database error and lose the reason.
func (g *Gateway) record(ctx context.Context, rec recording) {
	if _, _, err := g.store.Begin(ctx, rec); err != nil {
		g.logger.ErrorContext(ctx, "an AI interaction could not be recorded",
			"agent_code", rec.AgentCode, "status", rec.Status, "error", err.Error())
	}
}

// meter adds the day up and fires any threshold that has just been crossed (criterion 5, D-14).
//
// Every crossed threshold is raised, not only the highest, because they mean different things: 60%
// at eleven in the morning is a heads-up and 100% is a stop, and a system that only told you about
// the highest one it noticed would skip the warning entirely on a day when a single expensive job
// crossed both at once.
func (g *Gateway) meter(ctx context.Context, agentCode string, now time.Time) {
	dayStart := now.UTC().Truncate(24 * time.Hour)
	spend, err := g.store.DailySpend(ctx, g.facility, dayStart, dayStart.Add(24*time.Hour))
	if err != nil {
		g.logger.WarnContext(ctx, "the AI spend could not be metered", "error", err.Error())
		return
	}
	budgets, err := g.store.Budgets(ctx, g.facility)
	if err != nil {
		g.logger.WarnContext(ctx, "the AI budgets could not be read", "error", err.Error())
		return
	}
	for _, line := range spend {
		budget, configured := budgets[line.AgentCode]
		if !configured || budget.DailyMicroUSD <= 0 {
			continue
		}
		percent := line.SpendMicroUSD * 100 / budget.DailyMicroUSD
		for _, threshold := range budget.Thresholds {
			if percent < int64(threshold) {
				continue
			}
			alert, fresh, err := g.store.RaiseBudgetAlert(ctx, g.facility, line.AgentCode,
				dayStart, threshold, line.SpendMicroUSD, budget.DailyMicroUSD, now)
			if err != nil {
				g.logger.WarnContext(ctx, "an AI budget alert could not be recorded", "error", err.Error())
				continue
			}
			if !fresh {
				continue
			}
			g.metrics.BudgetCrossed(ctx, line.AgentCode, threshold)
			// At warn on the way past 60 and 80, at error on the way past 100: the first two are
			// somebody's afternoon and the third is money leaving the clinic.
			level := slog.LevelWarn
			if threshold >= 100 {
				level = slog.LevelError
			}
			g.logger.Log(ctx, level, "an AI budget threshold was crossed",
				"agent_code", nameOrDeployment(line.AgentCode),
				"threshold_percent", threshold,
				"spend_micro_usd", line.SpendMicroUSD,
				"budget_micro_usd", budget.DailyMicroUSD)
			if g.alerts != nil {
				if err := g.alerts.RaiseBudgetAlert(ctx, alert); err != nil {
					g.logger.WarnContext(ctx, "an AI budget alert could not be put in front of an administrator",
						"error", err.Error())
				}
			}
		}
	}
}

func nameOrDeployment(agentCode string) string {
	if agentCode == "" {
		return "(the deployment as a whole)"
	}
	return agentCode
}

// hashOf is the cache key: the minimised payload, which already carries the prompt version.
//
// The *minimised* payload and never the caller's, which matters more than it looks. Hashing the
// input would key a cache on a patient's name, so the key itself would be an identifier — derived,
// unreadable, and still a value that is one rainbow table away from linking two records.
func hashOf(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func describe(violations []Violation) string {
	parts := make([]string, 0, len(violations))
	for _, v := range violations {
		parts = append(parts, v.String())
	}
	return strings.Join(parts, "; ")
}

// repairInstruction is what a retry adds to the prompt after a schema violation.
//
// Deterministic — derived from the violations and nothing else — so that a recorded interaction can
// be reproduced exactly from its prompt version and its input hash, which is what §10.6's fourth
// permanent invariant asks for.
func repairInstruction(violations []Violation) string {
	var b strings.Builder
	b.WriteString("\n\nYour previous answer was rejected because it did not match the required schema:\n")
	for _, v := range violations {
		b.WriteString("  - ")
		b.WriteString(v.String())
		b.WriteString("\n")
	}
	b.WriteString("Answer again with only the JSON object. Do not explain the correction.")
	return b.String()
}
