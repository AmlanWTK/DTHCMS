package ai

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// The quality dashboard's gauges (CP72 criterion 4).
//
// # Why these are read from the table rather than counted in the process
//
// The same argument `jobs.RegisterMetrics` makes, with one addition. A counter maintained in a
// process is a number that resets when the process does and drifts when a write is dropped; a
// gauge that reads the table cannot drift because it is not a running total of anything.
//
// The addition is that the two numbers criterion 2 is judged on — the false-positive rate and the
// review backlog — are properties of *human decisions recorded later*, sometimes days after the
// call. There is no moment in a process's life at which they could be counted. They exist only in
// the table.
//
// # Why a nil rate is not zero
//
// `FalsePositiveRate` returns nil when nothing has been reviewed, and this observer then reports
// nothing rather than reporting zero. A validator nobody has checked has an *unknown*
// false-positive rate. Publishing 0% for it would put a green number on a dashboard that means
// "we have never looked", which is the single most misleading thing this checkpoint could ship —
// the brief names it as the criterion most likely to be faked, and a metric is the easiest place
// to fake it without meaning to.

// QualityMetricsConfig is what the observer needs.
type QualityMetricsConfig struct {
	Store *Store
	Meter metric.Meter
	Clock Clock
	// Facility is whose numbers these are. One today; D-61 will decide whether that stays true,
	// and this is a field rather than a lookup so that the day it changes is a change here.
	Facility uuid.UUID
	// Agents whose evaluation runs are published. Named rather than discovered, because a gauge
	// that appeared when a run happened would be a gauge that silently disappears when the
	// harness stops running — which is the failure it exists to show.
	Agents []string
	// Window is how far back the grounding rates look. An hour by default: long enough for a
	// clinic morning to show, short enough that yesterday's incident is not still colouring today.
	Window time.Duration
	Logger *slog.Logger
}

// RegisterQualityMetrics publishes the grounding and evaluation gauges.
//
// Returns an error rather than refusing to serve, for the same reason the queue's does: a clinic
// that cannot run because its dashboard is unavailable is worse than one that runs blind.
func RegisterQualityMetrics(cfg QualityMetricsConfig) error {
	window := cfg.Window
	if window <= 0 {
		window = time.Hour
	}

	answers, err := cfg.Meter.Int64ObservableGauge("dthcms.ai.grounding.answers",
		metric.WithDescription(
			"Model answers in the window, per agent and per verdict. PASSED, FAILED and "+
				"NOT_REQUIRED are separate series on purpose: an agent quietly moving to "+
				"NOT_REQUIRED is an exemption somebody granted, and it should be as visible as a "+
				"rise in failures."),
		metric.WithUnit("{answer}"))
	if err != nil {
		return err
	}
	open, err := cfg.Meter.Int64ObservableGauge("dthcms.ai.grounding.defects_open",
		metric.WithDescription(
			"Grounding defects nobody has classified yet. Not windowed: a defect from last week "+
				"is still unreviewed this week, and the backlog is what makes the false-positive "+
				"rate below unmeasurable."),
		metric.WithUnit("{defect}"))
	if err != nil {
		return err
	}
	falsePositive, err := cfg.Meter.Float64ObservableGauge("dthcms.ai.grounding.false_positive_rate",
		metric.WithDescription(
			"Of the grounding defects a person has reviewed, the fraction they judged the check "+
				"wrong about. **Absent, never zero, when nothing has been reviewed** — an "+
				"unmeasured rate must not read as a perfect one."),
		metric.WithUnit("1"))
	if err != nil {
		return err
	}
	detection, err := cfg.Meter.Float64ObservableGauge("dthcms.ai.evaluation.detection_rate",
		metric.WithDescription(
			"The last evaluation run's detection rate on injected hallucinations: acceptance "+
				"criterion 1, which is 1.0 or the build does not ship."),
		metric.WithUnit("1"))
	if err != nil {
		return err
	}
	evalFalsePositive, err := cfg.Meter.Float64ObservableGauge("dthcms.ai.evaluation.false_positive_rate",
		metric.WithDescription(
			"The last evaluation run's false-positive rate on the frozen corpus of correct "+
				"answers: acceptance criterion 2, measured on a set whose limitations are stated "+
				"in docs/ai-grounding.md."),
		metric.WithUnit("1"))
	if err != nil {
		return err
	}
	evalAge, err := cfg.Meter.Float64ObservableGauge("dthcms.ai.evaluation.age_seconds",
		metric.WithDescription(
			"How long since the frozen evaluation set last ran for this agent. The gauge that "+
				"catches the harness having quietly stopped, which is the way a CI gate stops "+
				"being a gate without anybody deciding to remove it."),
		metric.WithUnit("s"))
	if err != nil {
		return err
	}
	evalVerdict, err := cfg.Meter.Int64ObservableGauge("dthcms.ai.evaluation.passing",
		metric.WithDescription("1 when the last evaluation run passed, 0 when it failed."),
		metric.WithUnit("{run}"))
	if err != nil {
		return err
	}

	_, err = cfg.Meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		// Its own timeout, for the reason the queue's collection has one: a hung collection holds
		// the meter provider's callback mutex and turns a slow database into a metrics outage on
		// top of it.
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		now := cfg.Clock.Now()
		health, err := cfg.Store.Health(ctx, cfg.Facility, now.Add(-window))
		if err != nil {
			return err
		}
		for _, row := range health {
			agent := metric.WithAttributes(attribute.String("agent_code", row.AgentCode))
			for verdict, count := range map[string]int64{
				"PASSED": row.Passed, "FAILED": row.Failed, "NOT_REQUIRED": row.NotRequired,
			} {
				observer.ObserveInt64(answers, count, metric.WithAttributes(
					attribute.String("agent_code", row.AgentCode),
					attribute.String("verdict", verdict)))
			}
			observer.ObserveInt64(open, row.DefectsOpen, agent)
			if rate := row.FalsePositiveRate(); rate != nil {
				observer.ObserveFloat64(falsePositive, *rate, agent)
			}
		}

		for _, code := range cfg.Agents {
			run, found, err := cfg.Store.LatestEvaluation(ctx, code)
			if err != nil {
				return err
			}
			if !found {
				// Nothing observed, which shows on the dashboard as a gap. A zero here would say
				// "the evaluation ran and detected nothing", which is the opposite of the truth.
				continue
			}
			agent := metric.WithAttributes(attribute.String("agent_code", code))
			if rate := run.DetectionRate(); rate != nil {
				observer.ObserveFloat64(detection, *rate, agent)
			}
			if rate := run.FalsePositiveRate(); rate != nil {
				observer.ObserveFloat64(evalFalsePositive, *rate, agent)
			}
			observer.ObserveFloat64(evalAge, now.Sub(run.FinishedAt).Seconds(), agent)
			passing := int64(0)
			if run.Verdict == "PASS" {
				passing = 1
			}
			observer.ObserveInt64(evalVerdict, passing, agent)
		}
		return nil
	}, answers, open, falsePositive, detection, evalFalsePositive, evalAge, evalVerdict)
	return err
}
