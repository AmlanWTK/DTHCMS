package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// The queue's own metrics (CP69 criterion 4).
//
// # Why the depth and age gauges are observed rather than counted
//
// A counter incremented on enqueue and decremented on claim is a number that drifts: every
// dropped decrement — a crash, a reaped lease, a row an operator cancelled by hand — leaves the
// gauge permanently wrong, and it goes wrong slowly enough that nobody notices until the alert it
// feeds is the thing that is broken. Reading the table is one small query per collection interval
// and cannot drift, because it is not a running total of anything.
//
// # Why the alert is on age and not on depth
//
// A queue of four hundred that is draining is healthy; a queue of two that has not moved in an
// hour is not. §7.1's promise is about *when work finishes*, so the number that predicts a broken
// promise is how long the oldest due job has been waiting.
//
// And it is `oldest_due_seconds` rather than total age, because a job sitting out an exponential
// backoff is waiting **on purpose**: one failing SMS on a kind with a half-hour cap would
// otherwise light up an alert while the retry policy behaved exactly as designed.
//
// # What is not a label
//
// There is no patient, no user, no job id. Job ids in particular are the classic way a metrics
// bill becomes a surprise — one time series per job — and dthclint's PHI check would not catch it
// because a uuid is not a banned word. `kind` and `queue` are bounded by a registered catalogue,
// which is the other reason that catalogue is a table.

// MetricsConfig is what the observer needs.
type MetricsConfig struct {
	Store *Store
	Meter metric.Meter
	Clock interface{ Now() time.Time }
	// Window is the period the rates are computed over. It only affects the throughput and
	// attainment gauges; depth and age are instantaneous.
	Window time.Duration
	Logger *slog.Logger
}

// RegisterMetrics publishes the queue's gauges. It returns an error rather than refusing to serve:
// a worker without metrics still does the work, and a clinic that cannot run because its dashboard
// is unavailable is a worse system than one that runs blind for an hour.
func RegisterMetrics(cfg MetricsConfig) error {
	window := cfg.Window
	if window <= 0 {
		window = time.Hour
	}

	depth, err := cfg.Meter.Int64ObservableGauge("dthcms.job.queue.depth",
		metric.WithDescription("Jobs waiting to be claimed, per kind."),
		metric.WithUnit("{job}"))
	if err != nil {
		return fmt.Errorf("creating the queue depth gauge: %w", err)
	}
	running, err := cfg.Meter.Int64ObservableGauge("dthcms.job.queue.running",
		metric.WithDescription("Jobs currently held by a worker, per kind."),
		metric.WithUnit("{job}"))
	if err != nil {
		return fmt.Errorf("creating the running gauge: %w", err)
	}
	dueAge, err := cfg.Meter.Int64ObservableGauge("dthcms.job.queue.oldest_due_seconds",
		metric.WithDescription(
			"How long the oldest job that is due has been waiting. The number to alert on: "+
				"a queue of two that has not moved in an hour is worse than a queue of four "+
				"hundred that is draining. Excludes jobs sitting out a retry backoff, which "+
				"are waiting on purpose."),
		metric.WithUnit("s"))
	if err != nil {
		return fmt.Errorf("creating the queue age gauge: %w", err)
	}
	deadLetters, err := cfg.Meter.Int64ObservableGauge("dthcms.job.dead_letters",
		metric.WithDescription(
			"Jobs given up on and still waiting for a person. Not windowed: a dead letter from "+
				"last night is still a dead letter this morning."),
		metric.WithUnit("{job}"))
	if err != nil {
		return fmt.Errorf("creating the dead-letter gauge: %w", err)
	}
	attainment, err := cfg.Meter.Float64ObservableGauge("dthcms.job.sla.attainment",
		metric.WithDescription(
			"Percentage of finished jobs that met their deadline, per kind, over the window. "+
				"§7.1's five minutes, measured rather than asserted. Not reported at all when "+
				"nothing was measured, because zero would read as total failure."),
		metric.WithUnit("%"))
	if err != nil {
		return fmt.Errorf("creating the SLA attainment gauge: %w", err)
	}
	paused, err := cfg.Meter.Int64ObservableGauge("dthcms.job.kind.paused",
		metric.WithDescription(
			"1 when a kind has been paused by an operator. Its own signal, because a paused "+
				"queue and a healthy idle queue produce identical depth and age numbers."),
		metric.WithUnit("{kind}"))
	if err != nil {
		return fmt.Errorf("creating the paused gauge: %w", err)
	}

	_, err = cfg.Meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		// Its own timeout. A collection that hung would hold the meter provider's callback
		// mutex, and every subsequent scrape would queue behind it — turning a slow database
		// into a metrics outage on top of it.
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		health, err := cfg.Store.Health(ctx, window, cfg.Clock.Now())
		if err != nil {
			return err
		}
		for _, row := range health {
			labels := metric.WithAttributes(
				attribute.String("kind", row.Kind),
				attribute.String("queue", row.Queue),
				attribute.String("job_class", row.Class),
			)
			observer.ObserveInt64(depth, int64(row.Available), labels)
			observer.ObserveInt64(running, int64(row.Running), labels)
			observer.ObserveInt64(dueAge, int64(row.OldestDueSeconds), labels)
			observer.ObserveInt64(deadLetters, int64(row.DeadLetters), labels)
			observer.ObserveInt64(paused, boolAsInt(row.Paused), labels)
			// Absent rather than zero when nothing was measured. A gauge reporting 0% for "no
			// jobs finished" would fire the attainment alert every quiet night, and an alert
			// that fires every quiet night is one people turn off.
			if row.SLAAttainment != nil {
				observer.ObserveFloat64(attainment, *row.SLAAttainment, labels)
			}
		}
		return nil
	}, depth, running, dueAge, deadLetters, attainment, paused)
	if err != nil {
		return fmt.Errorf("registering the queue metrics callback: %w", err)
	}
	return nil
}

func boolAsInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// Instruments are the per-job counters and the duration histogram, recorded as work happens
// rather than observed from the table.
//
// These are counters because that is what they are: a job that succeeded is an event, and the
// table cannot tell you how many succeeded last Tuesday without keeping every row forever.
type Instruments struct {
	completed metric.Int64Counter
	duration  metric.Float64Histogram
	slaMet    metric.Int64Counter
}

// NewInstruments builds them. A nil meter yields nil instruments, which every method tolerates —
// so a test or a worker without telemetry runs unchanged rather than needing a stub.
func NewInstruments(meter metric.Meter) (*Instruments, error) {
	if meter == nil {
		return &Instruments{}, nil
	}
	completed, err := meter.Int64Counter("dthcms.job.completed",
		metric.WithDescription("Jobs that finished, by outcome."),
		metric.WithUnit("{job}"))
	if err != nil {
		return nil, err
	}
	duration, err := meter.Float64Histogram("dthcms.job.duration",
		metric.WithDescription("How long a job's handler ran."),
		metric.WithUnit("s"))
	if err != nil {
		return nil, err
	}
	slaMet, err := meter.Int64Counter("dthcms.job.sla.resolved",
		metric.WithDescription("Finished jobs that carried a deadline, by whether they met it."),
		metric.WithUnit("{job}"))
	if err != nil {
		return nil, err
	}
	return &Instruments{completed: completed, duration: duration, slaMet: slaMet}, nil
}

// Finished records one job's outcome.
func (i *Instruments) Finished(ctx context.Context, kind, outcome string,
	took time.Duration, met *bool) {

	if i == nil || i.completed == nil {
		return
	}
	labels := metric.WithAttributes(
		attribute.String("kind", kind),
		attribute.String("outcome", outcome),
	)
	i.completed.Add(ctx, 1, labels)
	i.duration.Record(ctx, took.Seconds(), metric.WithAttributes(attribute.String("kind", kind)))
	if met != nil {
		i.slaMet.Add(ctx, 1, metric.WithAttributes(
			attribute.String("kind", kind),
			attribute.Bool("met", *met),
		))
	}
}
