package ai

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// The gateway's own numbers (D-14, criterion 5).
//
// # What is not a label
//
// No patient, no pseudonym, no interaction id, no prompt text. Two reasons, and the second is the
// one that bites. A metrics bill becomes a surprise through unbounded label cardinality — one time
// series per call — and a pseudonym is exactly that shape. And `dthclint`'s PHI check would not
// catch it: a pseudonym is not a banned word, and telemetry attributes leave the process exactly as
// log lines do. `agent_code` and `model_version` are bounded by catalogues that are tables, which
// is one of the reasons they are tables.
//
// # Why cost is a counter and spend is not a gauge here
//
// The authoritative spend figure is `sum(cost_micro_usd)` over the interaction table, because that
// is what the budget alert reads and what an invoice is reconciled against. A gauge here would be a
// second answer to the same question, computed differently, and the first thing anybody would do
// with two figures that disagree is stop trusting both. What is exported is the counter — how much
// this process has spent since it started — which is a different question and cannot be confused
// with the first.

// Instruments are the gateway's counters and histograms.
//
// A nil *Instruments is usable: every method tolerates it, so a test or a CLI without telemetry
// runs unchanged rather than needing a stub.
type Instruments struct {
	calls     metric.Int64Counter
	tokens    metric.Int64Counter
	cost      metric.Int64Counter
	duration  metric.Float64Histogram
	refusals  metric.Int64Counter
	budget    metric.Int64Counter
	validated metric.Int64Counter
}

// NewInstruments builds them. A nil meter yields usable no-op instruments.
func NewInstruments(meter metric.Meter) (*Instruments, error) {
	if meter == nil {
		return &Instruments{}, nil
	}
	calls, err := meter.Int64Counter("dthcms.ai.calls",
		metric.WithDescription("AI calls, by agent, model version and outcome. Includes the refused and the cached: a call that never reached a model is still a call somebody made."),
		metric.WithUnit("{call}"))
	if err != nil {
		return nil, fmt.Errorf("creating the AI call counter: %w", err)
	}
	tokens, err := meter.Int64Counter("dthcms.ai.tokens",
		metric.WithDescription("Tokens consumed, by agent and model version."),
		metric.WithUnit("{token}"))
	if err != nil {
		return nil, fmt.Errorf("creating the AI token counter: %w", err)
	}
	cost, err := meter.Int64Counter("dthcms.ai.cost",
		metric.WithDescription("Spend in micro-dollars since this process started. Not the authoritative figure — that is the sum over core.ai_interaction, which is what the budget alert reads and what an invoice is reconciled against."),
		metric.WithUnit("{microdollar}"))
	if err != nil {
		return nil, fmt.Errorf("creating the AI cost counter: %w", err)
	}
	duration, err := meter.Float64Histogram("dthcms.ai.duration",
		metric.WithDescription("How long a whole gateway call took, retries and backoff included. This is the number §7.1's five-minute SLA is actually spent on."),
		metric.WithUnit("s"))
	if err != nil {
		return nil, fmt.Errorf("creating the AI duration histogram: %w", err)
	}
	refusals, err := meter.Int64Counter("dthcms.ai.refused",
		metric.WithDescription("Calls the gateway refused before contacting anybody, by reason: 'tier' is acceptance criterion 1b happening, 'phi' is a payload that still named a person. Both should be zero in a healthy system, which is precisely why they need a signal rather than a log line."),
		metric.WithUnit("{call}"))
	if err != nil {
		return nil, fmt.Errorf("creating the AI refusal counter: %w", err)
	}
	budget, err := meter.Int64Counter("dthcms.ai.budget.crossed",
		metric.WithDescription("Budget thresholds crossed, by agent and percentage. Incremented once per threshold per day, which is the same guarantee the alert itself has."),
		metric.WithUnit("{crossing}"))
	if err != nil {
		return nil, fmt.Errorf("creating the AI budget counter: %w", err)
	}
	validated, err := meter.Int64Counter("dthcms.ai.output.validated",
		metric.WithDescription("Model answers checked against their agent's schema, by whether they passed. A rising invalid rate is a prompt or a model that has drifted, and it is visible here before anybody notices it on a screen."),
		metric.WithUnit("{answer}"))
	if err != nil {
		return nil, fmt.Errorf("creating the AI validation counter: %w", err)
	}
	return &Instruments{
		calls: calls, tokens: tokens, cost: cost, duration: duration,
		refusals: refusals, budget: budget, validated: validated,
	}, nil
}

// Finished records one completed call, whatever its outcome.
func (i *Instruments) Finished(ctx context.Context, agentCode, modelVersion, status string,
	took time.Duration, cost int64, tokens int) {

	if i == nil || i.calls == nil {
		return
	}
	labels := metric.WithAttributes(
		attribute.String("agent_code", agentCode),
		attribute.String("model_version", modelVersion),
		attribute.String("status", status),
	)
	i.calls.Add(ctx, 1, labels)
	i.duration.Record(ctx, took.Seconds(), metric.WithAttributes(
		attribute.String("agent_code", agentCode),
		attribute.String("status", status),
	))

	agentAndModel := metric.WithAttributes(
		attribute.String("agent_code", agentCode),
		attribute.String("model_version", modelVersion),
	)
	if tokens > 0 {
		i.tokens.Add(ctx, int64(tokens), agentAndModel)
	}
	if cost > 0 {
		i.cost.Add(ctx, cost, agentAndModel)
	}
	switch status {
	case "SUCCEEDED", "CACHED":
		i.validated.Add(ctx, 1, metric.WithAttributes(
			attribute.String("agent_code", agentCode), attribute.Bool("valid", true)))
	case "INVALID_OUTPUT":
		i.validated.Add(ctx, 1, metric.WithAttributes(
			attribute.String("agent_code", agentCode), attribute.Bool("valid", false)))
	}
}

// Refused records a call the gateway would not make.
func (i *Instruments) Refused(ctx context.Context, agentCode, reason string) {
	if i == nil || i.refusals == nil {
		return
	}
	i.refusals.Add(ctx, 1, metric.WithAttributes(
		attribute.String("agent_code", agentCode),
		attribute.String("reason", reason),
	))
}

// BudgetCrossed records a threshold crossing.
func (i *Instruments) BudgetCrossed(ctx context.Context, agentCode string, threshold int) {
	if i == nil || i.budget == nil {
		return
	}
	i.budget.Add(ctx, 1, metric.WithAttributes(
		attribute.String("agent_code", nameOrDeployment(agentCode)),
		attribute.Int("threshold_percent", threshold),
	))
}
