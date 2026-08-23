package telemetry_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/logging"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/telemetry"
)

// TestSpansCannotCarryPatientIdentity is CP07's second acceptance criterion, applied to
// traces rather than logs.
//
// It records the attributes a careless or unlucky change would produce and asserts on
// what actually leaves the process — real spans through a real exporter, not a mock that
// was written to agree with the assertion.
func TestSpansCannotCarryPatientIdentity(t *testing.T) {
	exporter, tracer := recordingTracer(t)

	_, span := tracer.Start(context.Background(), "probe")
	span.SetAttributes(
		attribute.String("patient_name", "Rahima Begum"),        // phicheck:ignore fabricated value, proving this very attribute is redacted
		attribute.String("enduser.email", "rahima@example.com"), // phicheck:ignore fabricated value, proving this very attribute is redacted
		attribute.String("national_id", "1990123456789"),        // phicheck:ignore fabricated value, proving this very attribute is redacted
		attribute.String("password", "hunter2"),                 // phicheck:ignore fabricated value, proving this very attribute is redacted

		// Safe, and must survive: an opaque identifier is exactly what we ask people to
		// record instead.
		attribute.String("patient_id", "0190a000-0000-7000-8000-000000000001"),
		attribute.String("http.route", "/v1/patients/{patientID}"),
		attribute.Int("age_months", 42),
	)
	span.End()

	attrs := onlySpan(t, exporter).Attributes
	byKey := index(attrs)

	for _, key := range []string{"patient_name", "enduser.email", "national_id", "password"} {
		value, present := byKey[key]
		if !present {
			t.Errorf("attribute %q vanished; it should be present and redacted, so that the "+
				"trace still shows a value was set", key)
			continue
		}
		if value != logging.Redacted {
			t.Errorf("attribute %q exported as %q; want %q", key, value, logging.Redacted)
		}
	}

	if got := byKey["patient_id"]; got != "0190a000-0000-7000-8000-000000000001" {
		t.Errorf("patient_id was altered: %q. An opaque identifier is the safe thing to "+
			"record, and redacting it would make traces useless", got)
	}
	if got := byKey["http.route"]; got != "/v1/patients/{patientID}" {
		t.Errorf("http.route was altered: %q", got)
	}
}

// TestQueryStringsAreStripped covers the leak that looks harmless.
//
// A path carrying a patient id is fine. A query string is whatever an operator typed
// into a search box, which at a diabetes clinic is a patient's name.
func TestQueryStringsAreStripped(t *testing.T) {
	exporter, tracer := recordingTracer(t)

	_, span := tracer.Start(context.Background(), "probe")
	span.SetAttributes(
		attribute.String("url.full", "https://clinic.example/v1/patients?q=Rahima+Begum&limit=20"),
		attribute.String("url.path", "/v1/patients/0190a000-0000-7000-8000-000000000001/visits"),
	)
	span.End()

	byKey := index(onlySpan(t, exporter).Attributes)

	full := byKey["url.full"]
	if strings.Contains(full, "Rahima") {
		t.Errorf("url.full exported a patient name: %q", full)
	}
	if !strings.Contains(full, "/v1/patients") {
		t.Errorf("url.full lost the path as well as the query: %q. The path is what makes "+
			"the span useful", full)
	}

	if byKey["url.path"] != "/v1/patients/0190a000-0000-7000-8000-000000000001/visits" {
		t.Errorf("url.path was altered: %q. A path with no query carries only identifiers",
			byKey["url.path"])
	}
}

// TestStatementLiteralsAreStripped protects against the day someone builds SQL by
// concatenation, or turns on a driver option that interpolates parameters.
func TestStatementLiteralsAreStripped(t *testing.T) {
	exporter, tracer := recordingTracer(t)

	_, span := tracer.Start(context.Background(), "probe")
	span.SetAttributes(
		attribute.String("db.statement",
			"SELECT id FROM core.patient WHERE name_en = 'Rahima Begum' AND nid = '1990123456789'"),
	)
	span.End()

	statement := index(onlySpan(t, exporter).Attributes)["db.statement"]

	if strings.Contains(statement, "Rahima") || strings.Contains(statement, "1990123456789") {
		t.Fatalf("db.statement exported literal values: %q", statement)
	}
	// The shape has to survive, or a slow-query trace tells you nothing about which
	// query was slow.
	for _, keep := range []string{"SELECT", "core.patient", "name_en", "nid"} {
		if !strings.Contains(statement, keep) {
			t.Errorf("db.statement lost %q: %q", keep, statement)
		}
	}
}

// TestEventAttributesAreScrubbed closes the obvious way around attribute redaction.
func TestEventAttributesAreScrubbed(t *testing.T) {
	exporter, tracer := recordingTracer(t)

	_, span := tracer.Start(context.Background(), "probe")
	span.AddEvent("patient matched", trace.WithAttributes(
		attribute.String("phone", "+8801700000000"), // phicheck:ignore fabricated value, proving this very attribute is redacted
		attribute.String("match_score", "0.98"),
	))
	span.End()

	events := onlySpan(t, exporter).Events
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	byKey := index(events[0].Attributes)
	if byKey["phone"] != logging.Redacted {
		t.Errorf("event attribute phone exported as %q; an event is attributes with a name, "+
			"and the same rule has to apply", byKey["phone"])
	}
	if byKey["match_score"] != "0.98" {
		t.Errorf("safe event attribute was altered: %q", byKey["match_score"])
	}
}

// TestDisabledTelemetryIsUsable makes sure the off switch cannot crash a binary. The
// worker and the CLI tools run with telemetry off; a nil dereference there would be a
// self-inflicted outage caused by the observability layer.
func TestDisabledTelemetryIsUsable(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	provider, err := telemetry.Start(context.Background(), telemetry.Config{Enabled: false}, logger)
	if err != nil {
		t.Fatalf("Start with telemetry disabled: %v", err)
	}
	if provider.Active() {
		t.Error("a disabled provider reports itself active")
	}

	_, span := provider.Tracer("probe").Start(context.Background(), "probe")
	span.SetAttributes(attribute.String("patient_id", "x"))
	span.End()

	if _, err := provider.Meter("probe").Int64Counter("probe.count"); err != nil {
		t.Errorf("creating an instrument on a disabled provider: %v", err)
	}
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Errorf("shutting down a disabled provider: %v", err)
	}

	// And a nil provider, which is what a caller that skipped setup will hold.
	var absent *telemetry.Provider
	_, nilSpan := absent.Tracer("probe").Start(context.Background(), "probe")
	nilSpan.End()
	if err := absent.Shutdown(context.Background()); err != nil {
		t.Errorf("shutting down a nil provider: %v", err)
	}
}

func TestStartRejectsAMissingLogger(t *testing.T) {
	if _, err := telemetry.Start(context.Background(), telemetry.Config{Enabled: true}, nil); err == nil {
		t.Error("Start accepted a nil logger; export failures would then be silent")
	}
}

// --- helpers ---

// recordingTracer returns a tracer whose spans go through the production redaction path
// into memory. SimpleSpanProcessor rather than a batcher: the test wants the span now.
func recordingTracer(t *testing.T) (*tracetest.InMemoryExporter, trace.Tracer) {
	t.Helper()

	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(telemetry.Redacting(exporter))),
	)
	t.Cleanup(func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Logf("shutting down the test tracer provider: %v", err)
		}
	})

	return exporter, tp.Tracer("test")
}

func onlySpan(t *testing.T, exporter *tracetest.InMemoryExporter) tracetest.SpanStub {
	t.Helper()
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected exactly 1 exported span, got %d", len(spans))
	}
	return spans[0]
}

func index(attrs []attribute.KeyValue) map[string]string {
	out := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		out[string(attr.Key)] = attr.Value.Emit()
	}
	return out
}
