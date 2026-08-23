package httpx

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/logging"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/telemetry"
)

// unmatchedRoute labels requests that matched no route.
//
// The raw path is never used as a label or a span name. Two reasons, and both matter:
// a URL scanner probing a thousand paths would otherwise create a thousand metric time
// series, and a search request carries whatever the operator typed — which at a clinic
// is usually a patient's name.
const unmatchedRoute = "unmatched"

// Instrumentation holds the RED instruments, created once at start-up.
//
// Rate, Errors and Duration come from two instruments rather than three: the status code
// is a label on the duration histogram, so the error rate is a query over it. One
// instrument that answers three questions is cheaper to record and impossible to have
// disagree with itself.
type Instrumentation struct {
	tracer   trace.Tracer
	duration metric.Float64Histogram
	inFlight metric.Int64UpDownCounter

	// propagator extracts an incoming trace context.
	//
	// It is held here rather than read from otel.GetTextMapPropagator() on each request.
	// The global is set by telemetry.Start, which means anything constructing an
	// Instrumentation without going through Start — a test, a tool, an embedding — gets
	// the SDK's default no-op propagator and silently starts a fresh trace for every
	// request. Distributed tracing that quietly stops being distributed is the kind of
	// defect that is only noticed during the incident it was meant to help with.
	propagator propagation.TextMapPropagator
}

// NewInstrumentation builds the HTTP instruments.
func NewInstrumentation(provider *telemetry.Provider) (*Instrumentation, error) {
	meter := provider.Meter("dthcms/httpx")

	duration, err := meter.Float64Histogram("http.server.request.duration",
		metric.WithDescription("Time to serve an HTTP request."),
		metric.WithUnit("s"))
	if err != nil {
		return nil, fmt.Errorf("creating the request duration histogram: %w", err)
	}

	inFlight, err := meter.Int64UpDownCounter("http.server.active_requests",
		metric.WithDescription("Requests currently being served."),
		metric.WithUnit("{request}"))
	if err != nil {
		return nil, fmt.Errorf("creating the active requests counter: %w", err)
	}

	return &Instrumentation{
		tracer:   provider.Tracer("dthcms/httpx"),
		duration: duration,
		inFlight: inFlight,
		propagator: propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	}, nil
}

// Observe traces and measures every request.
//
// It is written here rather than taken from otelhttp for one reason: the route template.
// chi resolves which route matched only after the request has been dispatched, so the
// span name and the metric label can only be correct if they are set on the way out.
// Instrumentation that labels by raw path instead is the usual source of both a
// cardinality explosion and a PHI leak, and it is not worth inheriting to save fifty
// lines.
func (i *Instrumentation) Observe(next http.Handler) http.Handler {
	if i == nil {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Continue the caller's trace when there is one. A station app that starts a
		// trace on a tablet and a server that starts its own produce two half-stories.
		ctx := i.propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

		// The span is named for the method now and renamed to include the route
		// template once one is known. A span named "GET" is useless; a span named for
		// an unmatched raw path is worse than useless.
		ctx, span := i.tracer.Start(ctx, r.Method,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.request.method", r.Method),
				attribute.String("url.path", r.URL.Path),
				attribute.String("url.scheme", scheme(r)),
				attribute.String("network.protocol.version", r.Proto),
				attribute.String("user_agent.original", r.UserAgent()),
			))
		defer span.End()

		// The correlation ID and the trace ID name the same interaction in two
		// vocabularies: one an operator can read over the phone, the other a trace
		// viewer can find. Recording both on the span joins them.
		if id := logging.CorrelationID(ctx); id != "" {
			span.SetAttributes(attribute.String("dthcms.correlation_id", id))
		}

		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		methodAttr := attribute.String("http.request.method", r.Method)
		i.inFlight.Add(ctx, 1, metric.WithAttributes(methodAttr))

		next.ServeHTTP(rec, r.WithContext(ctx))

		i.inFlight.Add(ctx, -1, metric.WithAttributes(methodAttr))

		route := routeTemplate(r)
		span.SetName(r.Method + " " + route)
		span.SetAttributes(
			attribute.String("http.route", route),
			attribute.Int("http.response.status_code", rec.status),
		)

		// 5xx marks the span as failed; 4xx does not. A rejected malformed request is
		// the API working correctly, and colouring it red trains people to ignore red.
		if rec.status >= http.StatusInternalServerError {
			span.SetStatus(codes.Error, http.StatusText(rec.status))
		}

		i.duration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(
			methodAttr,
			attribute.String("http.route", route),
			attribute.Int("http.response.status_code", rec.status),
		))
	})
}

// routeTemplate returns the matched chi pattern, for example /v1/patients/{patientID}.
func routeTemplate(r *http.Request) string {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return unmatchedRoute
	}
	if pattern := rctx.RoutePattern(); pattern != "" {
		return pattern
	}
	return unmatchedRoute
}

func scheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	// Behind a load balancer the original scheme arrives in a header. Trusting it is
	// safe for a telemetry attribute and would not be for an access decision.
	if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded == "https" {
		return "https"
	}
	return "http"
}
