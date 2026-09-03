package httpx_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/telemetry"
)

// TestRequestProducesATraceWithADatabaseSpan is CP07's first acceptance criterion.
//
// The handler starts a child span the way instrumented database code does, so the test
// asserts the shape a real request produces: one server span, one database span nested
// inside it, sharing a trace.
func TestRequestProducesATraceWithADatabaseSpan(t *testing.T) {
	harness := newHarness(t)

	router := chi.NewRouter()
	router.Use(httpx.RequestID(ids.UUIDv7{}))
	router.Use(harness.instruments.Observe)
	router.Get("/v1/patients/{patientID}/visits", func(w http.ResponseWriter, r *http.Request) {
		// Stands in for otelpgx, which starts exactly this shape of span per query.
		_, dbSpan := harness.tracer.Start(r.Context(), "SELECT core.facility")
		dbSpan.SetAttributes(attribute.String("db.system", "postgresql"))
		dbSpan.End()

		w.WriteHeader(http.StatusOK)
	})

	request := httptest.NewRequest(http.MethodGet,
		"/v1/patients/0190a000-0000-7000-8000-000000000001/visits", nil)
	router.ServeHTTP(httptest.NewRecorder(), request)

	spans := harness.exporter.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("expected a server span and a database span, got %d: %v", len(spans), names(spans))
	}

	dbSpan, serverSpan := spans[0], spans[1]

	if serverSpan.Name != "GET /v1/patients/{patientID}/visits" {
		t.Errorf("server span is named %q; it must use the route template so that spans "+
			"group by endpoint rather than by patient", serverSpan.Name)
	}
	if serverSpan.SpanKind != trace.SpanKindServer {
		t.Errorf("server span kind = %v, want server", serverSpan.SpanKind)
	}

	if dbSpan.Parent.SpanID() != serverSpan.SpanContext.SpanID() {
		t.Error("the database span is not a child of the request span; without the parent " +
			"link a trace cannot show which request issued which query")
	}
	if dbSpan.SpanContext.TraceID() != serverSpan.SpanContext.TraceID() {
		t.Error("the database span is in a different trace from the request that caused it")
	}

	attrs := attrIndex(serverSpan.Attributes)
	if attrs["http.route"] != "/v1/patients/{patientID}/visits" {
		t.Errorf("http.route = %q, want the template", attrs["http.route"])
	}
	if attrs["http.response.status_code"] != "200" {
		t.Errorf("http.response.status_code = %q, want 200", attrs["http.response.status_code"])
	}
	if attrs["dthcms.correlation_id"] == "" {
		t.Error("the span carries no correlation id; the id an operator quotes on the phone " +
			"is then unconnected to the trace that would explain their problem")
	}
}

// TestAnIncomingTraceIsContinued matters for the station apps: a tablet that starts a
// trace and a server that starts its own produce two half-stories of one interaction.
func TestAnIncomingTraceIsContinued(t *testing.T) {
	harness := newHarness(t)

	router := chi.NewRouter()
	router.Use(harness.instruments.Observe)
	router.Get("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	// No call to telemetry.Start here, deliberately: propagation must work from the
	// Instrumentation's own configuration rather than from global state a test never set.
	//
	// A W3C traceparent, as a station app would send.
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set("traceparent", "00-"+traceID+"-00f067aa0ba902b7-01")

	router.ServeHTTP(httptest.NewRecorder(), request)

	spans := harness.exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if got := spans[0].SpanContext.TraceID().String(); got != traceID {
		t.Errorf("trace id = %s, want %s — the caller's trace was not continued", got, traceID)
	}
}

// TestMetricsUseTheRouteTemplateNotTheRawPath guards two failures at once: one time
// series per patient would break the metrics backend, and a search request's raw path
// would put what an operator typed into a label.
func TestMetricsUseTheRouteTemplateNotTheRawPath(t *testing.T) {
	harness := newHarness(t)

	router := chi.NewRouter()
	router.Use(harness.instruments.Observe)
	router.Get("/v1/patients/{patientID}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for _, id := range []string{"patient-one", "patient-two", "patient-three"} {
		router.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, "/v1/patients/"+id, nil))
	}

	histogram := collectHistogram(t, harness.reader, "http.server.request.duration")

	if len(histogram.DataPoints) != 1 {
		t.Fatalf("three requests to one endpoint produced %d time series; they must share "+
			"one, or the metrics backend grows a series per patient", len(histogram.DataPoints))
	}

	point := histogram.DataPoints[0]
	if point.Count != 3 {
		t.Errorf("recorded %d observations, want 3", point.Count)
	}

	labels := attrIndex(point.Attributes.ToSlice())
	if labels["http.route"] != "/v1/patients/{patientID}" {
		t.Errorf("http.route label = %q, want the template", labels["http.route"])
	}
	for key, value := range labels {
		if strings.Contains(value, "patient-one") {
			t.Errorf("label %q carries a raw path segment: %q", key, value)
		}
	}
}

// TestUnmatchedRequestsShareOneLabel is the same protection against an adversary: a
// scanner probing a thousand paths must not create a thousand time series.
func TestUnmatchedRequestsShareOneLabel(t *testing.T) {
	harness := newHarness(t)

	router := chi.NewRouter()
	router.Use(harness.instruments.Observe)
	// chi dispatches straight to the NotFound handler, bypassing the middleware chain,
	// when a router has no routes at all. Production routers always have some; this one
	// needs at least one for the test to exercise the real path.
	router.Get("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	router.NotFound(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })

	for _, path := range []string{"/wp-admin", "/.env", "/phpmyadmin", "/admin/config"} {
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	histogram := collectHistogram(t, harness.reader, "http.server.request.duration")

	var unmatched int
	for _, point := range histogram.DataPoints {
		if attrIndex(point.Attributes.ToSlice())["http.route"] == "unmatched" {
			unmatched++
			if point.Count != 4 {
				t.Errorf("the unmatched series recorded %d observations, want 4", point.Count)
			}
		}
	}
	if unmatched != 1 {
		t.Fatalf("four unmatched paths produced %d time series; want 1, or a scanner "+
			"probing a thousand paths creates a thousand", unmatched)
	}
}

// TestServerErrorsMarkTheSpanFailed, and client errors do not. Colouring a correctly
// rejected malformed request red is how people learn to ignore red.
func TestServerErrorsMarkTheSpanFailed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		wantFailed bool
	}{
		{"server error", http.StatusInternalServerError, true},
		{"bad request", http.StatusBadRequest, false},
		{"not found", http.StatusNotFound, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			harness := newHarness(t)

			router := chi.NewRouter()
			router.Use(harness.instruments.Observe)
			router.Get("/probe", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			})

			router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/probe", nil))

			spans := harness.exporter.GetSpans()
			if len(spans) != 1 {
				t.Fatalf("expected 1 span, got %d", len(spans))
			}

			failed := spans[0].Status.Code.String() == "Error"
			if failed != tc.wantFailed {
				t.Errorf("status %d marked the span failed = %v, want %v",
					tc.status, failed, tc.wantFailed)
			}
		})
	}
}

// TestRouterWithoutInstrumentationStillServes: telemetry is optional wiring, and a nil
// provider must not be a way to break the API.
func TestRouterWithoutInstrumentationStillServes(t *testing.T) {
	router, _ := httpx.NewRouter(httpx.RouterOptions{
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		IDs:            ids.UUIDv7{},
		MaxBodyBytes:   1 << 20,
		RequestTimeout: 0,
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/nope", nil))

	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", recorder.Code)
	}
}

// --- harness ---

type harness struct {
	exporter    *tracetest.InMemoryExporter
	reader      *sdkmetric.ManualReader
	instruments *httpx.Instrumentation
	tracer      trace.Tracer
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	exporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(
		// The production redaction path, so these tests would also fail if a span
		// attribute started leaking.
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(telemetry.Redacting(exporter))),
	)

	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	t.Cleanup(func() {
		_ = tracerProvider.Shutdown(context.Background())
		_ = meterProvider.Shutdown(context.Background())
	})

	provider := telemetry.NewWith(tracerProvider, meterProvider)

	instruments, err := httpx.NewInstrumentation(provider)
	if err != nil {
		t.Fatalf("creating instrumentation: %v", err)
	}

	return &harness{
		exporter:    exporter,
		reader:      reader,
		instruments: instruments,
		tracer:      provider.Tracer("test"),
	}
}

func collectHistogram(t *testing.T, reader *sdkmetric.ManualReader, name string) metricdata.Histogram[float64] {
	t.Helper()

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("collecting metrics: %v", err)
	}

	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != name {
				continue
			}
			histogram, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("%s is a %T, not a float histogram", name, m.Data)
			}
			return histogram
		}
	}

	t.Fatalf("metric %q was never recorded", name)
	return metricdata.Histogram[float64]{}
}

func attrIndex(attrs []attribute.KeyValue) map[string]string {
	out := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		out[string(attr.Key)] = attr.Value.Emit()
	}
	return out
}

func names(spans tracetest.SpanStubs) []string {
	out := make([]string, len(spans))
	for i, span := range spans {
		out[i] = span.Name
	}
	return out
}
