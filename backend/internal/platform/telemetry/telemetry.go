// Package telemetry wires OpenTelemetry tracing and metrics into every DTHCMS binary.
//
// Two rules shape everything here.
//
// First, telemetry must never take the clinic down. A collector that is unreachable, a
// backend that is rate-limiting, an exporter that is failing — none of these may stop a
// process from starting or a request from being served. Every failure path in this file
// degrades to "no telemetry" and logs once, never to "no service".
//
// Second, telemetry must never carry patient identity. Logs already cannot (see
// internal/platform/logging), but a span attribute and a metric label are just as
// exportable, travel to the same third-party backend, and are much easier to add without
// thinking. The same key list governs all three, and spans are scrubbed on the way out —
// see redact.go.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Config describes where telemetry goes and how much of it.
type Config struct {
	// Enabled turns the whole subsystem off. When false, Start returns a Provider
	// backed by no-op implementations, so calling code needs no conditionals.
	Enabled bool
	// Endpoint is an OTLP/HTTP endpoint as host:port, without a scheme.
	Endpoint string
	// Insecure sends over plain HTTP. Local only; refused in production by config.
	Insecure bool
	// SampleRatio is the fraction of traces recorded, between 0 and 1.
	SampleRatio float64
	// MetricInterval is how often metrics are pushed.
	MetricInterval time.Duration
	// ExportTimeout bounds a single export attempt.
	ExportTimeout time.Duration

	Service     string
	Version     string
	Environment string
	Instance    string
}

// Provider owns the tracer and meter providers and the shutdown of both.
type Provider struct {
	tracers trace.TracerProvider
	meters  metric.MeterProvider

	shutdown []func(context.Context) error
	logger   *slog.Logger
	active   bool
}

// Start configures OpenTelemetry and returns a Provider.
//
// It does not return an error for an unreachable collector, and cannot: OTLP over HTTP
// connects lazily, and even if it did not, refusing to start a clinic's API because a
// metrics backend is down would be the wrong trade in every circumstance. Export
// failures surface through the error handler installed below.
func Start(ctx context.Context, cfg Config, logger *slog.Logger) (*Provider, error) {
	if logger == nil {
		return nil, errors.New("telemetry: logger is required")
	}

	if !cfg.Enabled {
		logger.Info("telemetry disabled",
			"note", "no traces or metrics will be exported; set DTHCMS_OTEL_ENABLED=true to enable")
		return &Provider{
			tracers: noop.NewTracerProvider(),
			meters:  metricnoop(),
			logger:  logger,
		}, nil
	}

	// Export failures are reported here rather than being printed to stderr by the SDK.
	// They are throttled: a collector that is down produces one failing export per
	// batch, and a log line for each would bury the application's own output.
	otel.SetErrorHandler(&throttledErrorHandler{logger: logger, every: time.Minute})

	res, err := buildResource(ctx, cfg)
	if err != nil {
		return nil, err
	}

	provider := &Provider{logger: logger, active: true}

	tracerProvider, err := startTracing(ctx, cfg, res, provider)
	if err != nil {
		return nil, err
	}
	provider.tracers = tracerProvider

	meterProvider, err := startMetrics(ctx, cfg, res, provider)
	if err != nil {
		// Tracing is already running; tear it down rather than leaking its goroutines.
		_ = provider.Shutdown(ctx)
		return nil, err
	}
	provider.meters = meterProvider

	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)

	// W3C trace context plus baggage: the same propagation the station apps, the web
	// client and any future service will use. Choosing the standard here means a trace
	// crosses a process boundary without anyone writing propagation code.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	logger.Info("telemetry started",
		"endpoint", cfg.Endpoint,
		"sample_ratio", cfg.SampleRatio,
		"metric_interval", cfg.MetricInterval.String(),
		"insecure", cfg.Insecure)

	return provider, nil
}

func startTracing(ctx context.Context, cfg Config, res *resource.Resource, p *Provider) (*sdktrace.TracerProvider, error) {
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(cfg.Endpoint),
		otlptracehttp.WithTimeout(cfg.ExportTimeout),
	}
	if cfg.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}

	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("creating the trace exporter: %w", err)
	}

	// Every span passes through Redacting on its way out. Wrapping the exporter rather
	// than filtering at the call site means an attribute added by a third-party
	// instrumentation library is scrubbed too — and most of the attributes on a span
	// are not ours.
	safe := Redacting(exporter)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(safe,
			sdktrace.WithBatchTimeout(5*time.Second),
			sdktrace.WithExportTimeout(cfg.ExportTimeout),
		),
		// ParentBased: once a station app decides a trace is being recorded, the server
		// records its half too. A server that sampled independently would produce
		// half-traces, which are worse than none — they look complete.
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
	)

	p.shutdown = append(p.shutdown, tp.Shutdown)
	return tp, nil
}

func startMetrics(ctx context.Context, cfg Config, res *resource.Resource, p *Provider) (*sdkmetric.MeterProvider, error) {
	opts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpoint(cfg.Endpoint),
		otlpmetrichttp.WithTimeout(cfg.ExportTimeout),
	}
	if cfg.Insecure {
		opts = append(opts, otlpmetrichttp.WithInsecure())
	}

	exporter, err := otlpmetrichttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("creating the metric exporter: %w", err)
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter,
			sdkmetric.WithInterval(cfg.MetricInterval),
			sdkmetric.WithTimeout(cfg.ExportTimeout),
		)),
		// Explicit latency buckets in seconds. The defaults are tuned for machine-to-
		// machine RPC and put almost every DTHCMS request in one bucket. These are
		// chosen around what a person waiting at a station actually notices: a quarter
		// of a second is instant, one second is noticeable, three is annoying, ten is a
		// support call.
		sdkmetric.WithView(sdkmetric.NewView(
			sdkmetric.Instrument{Name: "http.server.request.duration"},
			sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
				Boundaries: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
			}},
		)),
	)

	p.shutdown = append(p.shutdown, mp.Shutdown)
	return mp, nil
}

func buildResource(ctx context.Context, cfg Config) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		semconv.ServiceName(cfg.Service),
		semconv.ServiceVersion(cfg.Version),
		semconv.DeploymentEnvironment(cfg.Environment),
	}
	if cfg.Instance != "" {
		attrs = append(attrs, semconv.ServiceInstanceID(cfg.Instance))
	}

	res, err := resource.New(ctx,
		resource.WithHost(),
		resource.WithProcessRuntimeDescription(),
		resource.WithAttributes(attrs...),
	)
	// A schema-version conflict between detectors is reported as an error alongside a
	// perfectly usable resource. Losing all telemetry over it would be absurd.
	if err != nil && res == nil {
		return nil, fmt.Errorf("building the telemetry resource: %w", err)
	}
	return res, nil
}

// Tracer returns a named tracer. Safe when telemetry is disabled.
func (p *Provider) Tracer(name string) trace.Tracer {
	if p == nil || p.tracers == nil {
		return noop.NewTracerProvider().Tracer(name)
	}
	return p.tracers.Tracer(name)
}

// Meter returns a named meter. Safe when telemetry is disabled.
func (p *Provider) Meter(name string) metric.Meter {
	if p == nil || p.meters == nil {
		return metricnoop().Meter(name)
	}
	return p.meters.Meter(name)
}

// Active reports whether telemetry is actually exporting.
func (p *Provider) Active() bool { return p != nil && p.active }

// Shutdown flushes pending spans and metrics.
//
// It is worth waiting for: the spans describing the failure that caused the shutdown are
// usually the ones still in the buffer.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}
	var errs []error
	for i := len(p.shutdown) - 1; i >= 0; i-- {
		if err := p.shutdown[i](ctx); err != nil {
			errs = append(errs, err)
		}
	}
	p.shutdown = nil
	return errors.Join(errs...)
}

// throttledErrorHandler reports export failures without flooding the log.
//
// A collector that is down fails every batch. Logging each one turns a degraded
// telemetry pipeline into an unreadable application log, which is a strictly worse
// outcome than the original problem.
type throttledErrorHandler struct {
	logger *slog.Logger
	every  time.Duration

	mu         sync.Mutex
	lastLogged time.Time
	suppressed int
}

func (h *throttledErrorHandler) Handle(err error) {
	if err == nil {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now()
	if !h.lastLogged.IsZero() && now.Sub(h.lastLogged) < h.every {
		h.suppressed++
		return
	}

	h.logger.Warn("telemetry export failed",
		"error", err.Error(),
		"suppressed_since_last", h.suppressed,
		"note", "the application is unaffected; traces and metrics are being dropped")
	h.lastLogged = now
	h.suppressed = 0
}

// NewWith builds a Provider around providers that were constructed elsewhere.
//
// Start is what production uses. This exists for tests, which need an in-memory exporter
// and a manual metric reader in order to assert on what was actually recorded — and
// asserting on real exported spans is worth far more than asserting that a mock was
// called.
func NewWith(tracers trace.TracerProvider, meters metric.MeterProvider) *Provider {
	return &Provider{tracers: tracers, meters: meters, active: true}
}
