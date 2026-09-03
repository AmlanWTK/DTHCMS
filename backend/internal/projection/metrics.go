package projection

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/telemetry"
)

// Lag, as a metric with an alert (criterion 2).
//
// Two numbers, because they answer different questions and each is misleading alone:
//
//	events   how many events the projection has not yet seen. Zero at a quiet clinic and
//	         zero at a busy one that is keeping up — it says "behind", not "slow".
//	seconds  how old the last event it applied is. This is what a person means by "the
//	         board is stale", and it is the one an alert should fire on.
//
// Both are observable gauges: they are properties of the database, read when the collector
// asks, rather than counters the runner has to remember to update. A counter the runner
// forgets to update during the failure being diagnosed is worse than no metric.
//
// The alert thresholds are in docs/projections.md; the rule they encode is that a
// synchronous projection is never behind (it commits with the event), so any lag on one is
// a bug, while an asynchronous projection lagging more than a minute means the runner is
// down or stuck.

// MetricsConfig assembles the observer.
type MetricsConfig struct {
	Engine   *Engine
	Provider *telemetry.Provider
	Logger   *slog.Logger
}

// RegisterMetrics publishes the lag gauges. The returned function unregisters them.
func RegisterMetrics(cfg MetricsConfig) (func() error, error) {
	if cfg.Engine == nil {
		return nil, fmt.Errorf("projection metrics need an engine")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	meter := cfg.Provider.Meter("dthcms/projection")

	events, err := meter.Int64ObservableGauge("dthcms.projection.lag.events",
		metric.WithDescription("Events appended to the ledger that this projection has not yet applied."),
		metric.WithUnit("{event}"))
	if err != nil {
		return nil, fmt.Errorf("creating the projection lag gauge: %w", err)
	}
	seconds, err := meter.Float64ObservableGauge("dthcms.projection.lag.seconds",
		metric.WithDescription("Age of the last event this projection applied."),
		metric.WithUnit("s"))
	if err != nil {
		return nil, fmt.Errorf("creating the projection lag age gauge: %w", err)
	}
	dead, err := meter.Int64ObservableGauge("dthcms.projection.dead_letters",
		metric.WithDescription("Events this projection could not apply and that nobody has resolved."),
		metric.WithUnit("{event}"))
	if err != nil {
		return nil, fmt.Errorf("creating the dead-letter gauge: %w", err)
	}
	degraded, err := meter.Int64ObservableGauge("dthcms.projection.degraded",
		metric.WithDescription("1 when a projection is degraded or rebuilding, 0 when healthy."))
	if err != nil {
		return nil, fmt.Errorf("creating the projection status gauge: %w", err)
	}

	registration, err := meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		// Its own timeout: a metrics callback that blocks on a slow database must not hold
		// the collector's whole cycle.
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		states, err := cfg.Engine.States(ctx)
		if err != nil {
			cfg.Logger.WarnContext(ctx, "projection lag not observed", "error", err.Error())
			return nil // a failed read is not a reason to break every other instrument
		}
		head, err := cfg.Engine.Head(ctx)
		if err != nil {
			cfg.Logger.WarnContext(ctx, "ledger head not observed", "error", err.Error())
			return nil
		}
		now := time.Now()
		for _, s := range states {
			labels := metric.WithAttributes(
				attribute.String("projection", s.Name),
				attribute.String("mode", string(s.Mode)),
			)
			behind := head - s.Checkpoint
			if behind < 0 {
				behind = 0
			}
			observer.ObserveInt64(events, behind, labels)
			observer.ObserveInt64(dead, s.OpenDeadLetters, labels)
			observer.ObserveInt64(degraded, boolAsInt(s.Status != Healthy), labels)
			// Age only when there is something to be behind: a projection that has applied
			// every event is not stale because the clinic closed for the night.
			if s.AppliedAt != nil && behind > 0 {
				observer.ObserveFloat64(seconds, now.Sub(*s.AppliedAt).Seconds(), labels)
			} else {
				observer.ObserveFloat64(seconds, 0, labels)
			}
		}
		return nil
	}, events, seconds, dead, degraded)
	if err != nil {
		return nil, fmt.Errorf("registering the projection lag callback: %w", err)
	}
	return registration.Unregister, nil
}

func boolAsInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// Lag is the same two numbers, read once, for a health endpoint or a command.
type Lag struct {
	State
	Behind int64
	Age    time.Duration
}

// Lags reports every projection's lag against the ledger's head.
func (e *Engine) Lags(ctx context.Context, now time.Time) ([]Lag, error) {
	states, err := e.States(ctx)
	if err != nil {
		return nil, err
	}
	head, err := e.Head(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Lag, 0, len(states))
	for _, s := range states {
		lag := Lag{State: s, Behind: head - s.Checkpoint}
		if lag.Behind < 0 {
			lag.Behind = 0
		}
		if s.AppliedAt != nil && lag.Behind > 0 {
			lag.Age = now.Sub(*s.AppliedAt)
		}
		out = append(out, lag)
	}
	return out, nil
}
