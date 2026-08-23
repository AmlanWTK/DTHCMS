package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func capture(t *testing.T, fn func(ctx context.Context, log *slog.Logger)) map[string]any {
	t.Helper()

	var buf bytes.Buffer
	logger := New(&buf, Options{Level: "debug", Format: "json", Service: "test", Version: "0.0.0"})
	fn(context.Background(), logger)

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("nothing was logged")
	}
	// Take the last record if several were written.
	if idx := strings.LastIndex(line, "\n"); idx >= 0 {
		line = line[idx+1:]
	}

	var record map[string]any
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		t.Fatalf("log line is not valid JSON: %v\n%s", err, line)
	}
	return record
}

func TestPatientIdentifiersAreRedacted(t *testing.T) {
	// Each of these has ended up in a production log in some clinical system somewhere.
	for key, sensitive := range map[string]string{
		"name":        "Ayesha Rahman",
		"national_id": "1990123456789",
		"phone":       "+8801712345678",
		"address":     "House 12, Road 3, Faridpur",
		"dob":         "1984-03-11",
		"password":    "hunter2",
		"token":       "eyJhbGciOi",
	} {
		t.Run(key, func(t *testing.T) {
			record := capture(t, func(ctx context.Context, log *slog.Logger) {
				log.InfoContext(ctx, "test", key, sensitive)
			})

			got, _ := record[key].(string)
			if got != Redacted {
				t.Errorf("%s = %q, want %q", key, got, Redacted)
			}
			if strings.Contains(mustJSON(t, record), sensitive) {
				t.Errorf("the sensitive value survived somewhere in the record: %s", mustJSON(t, record))
			}
		})
	}
}

func TestRedactionRecursesIntoGroups(t *testing.T) {
	// Nesting must not be a way around the rule, deliberate or otherwise.
	record := capture(t, func(ctx context.Context, log *slog.Logger) {
		log.InfoContext(ctx, "test",
			slog.Group("patient",
				slog.String("patient_id", "pat_123"),
				slog.String("name", "Ayesha Rahman"), // phicheck:ignore fabricated name, proving this very value is redacted
			))
	})

	if strings.Contains(mustJSON(t, record), "Ayesha") {
		t.Errorf("a nested patient name was logged: %s", mustJSON(t, record))
	}
	if !strings.Contains(mustJSON(t, record), "pat_123") {
		t.Error("the patient ID should survive; it is what makes the log useful")
	}
}

func TestSafeKeysAreUntouched(t *testing.T) {
	record := capture(t, func(ctx context.Context, log *slog.Logger) {
		log.InfoContext(ctx, "test",
			"patient_id", "pat_123",
			"age_months", 504,
			"station", "ANTHROPOMETRY")
	})

	if record["patient_id"] != "pat_123" {
		t.Errorf("patient_id was altered: %v", record["patient_id"])
	}
	if record["station"] != "ANTHROPOMETRY" {
		t.Errorf("station was altered: %v", record["station"])
	}
}

func TestCorrelationIDIsAttachedFromContext(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, Options{Level: "info", Format: "json"})

	ctx := WithCorrelationID(context.Background(), "req_abc123")
	logger.InfoContext(ctx, "test")

	if !strings.Contains(buf.String(), "req_abc123") {
		t.Errorf("the correlation ID must appear on every line: %s", buf.String())
	}
}

func TestWithAttrsIsAlsoRedacted(t *testing.T) {
	// A logger carrying a sensitive attribute would leak it on every subsequent line,
	// which is worse than a single bad call.
	var buf bytes.Buffer
	logger := New(&buf, Options{Level: "info", Format: "json"}).
		With("name", "Ayesha Rahman") // phicheck:ignore fabricated name, proving With() is redacted too
	logger.Info("test")

	if strings.Contains(buf.String(), "Ayesha") {
		t.Errorf("a name attached with With() leaked: %s", buf.String())
	}
}

func TestServiceAndVersionAreAlwaysPresent(t *testing.T) {
	record := capture(t, func(ctx context.Context, log *slog.Logger) {
		log.InfoContext(ctx, "test")
	})

	if record["service"] != "test" || record["version"] != "0.0.0" {
		t.Errorf("every line must identify the service and build: %v", record)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// TestLogLinesCarryTheTraceID is what makes a log line and a trace two views of one
// event rather than two unrelated records.
//
// Without it, an operator reports a problem, the log line is found, and answering "what
// was the system doing" means reading timestamps in a second tool and hoping.
func TestLogLinesCarryTheTraceID(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, Options{Level: "info", Format: "json"})

	provider := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	ctx, span := provider.Tracer("test").Start(context.Background(), "probe")
	defer span.End()

	log.InfoContext(ctx, "queue entry created", "station", "vitals")

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log output is not JSON: %v\n%s", err, buf.String())
	}

	wantTrace := span.SpanContext().TraceID().String()
	if line["trace_id"] != wantTrace {
		t.Errorf("trace_id = %v, want %s", line["trace_id"], wantTrace)
	}
	if line["span_id"] != span.SpanContext().SpanID().String() {
		t.Errorf("span_id = %v, want %s", line["span_id"], span.SpanContext().SpanID())
	}
}

func TestLogLinesWithoutASpanAreUnchanged(t *testing.T) {
	// The worker, the CLI tools and start-up logging all run outside a span. Emitting an
	// empty or zero trace id there would make every log search for a real trace match
	// them too.
	var buf bytes.Buffer
	log := New(&buf, Options{Level: "info", Format: "json"})

	log.InfoContext(context.Background(), "starting")

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log output is not JSON: %v", err)
	}
	if _, present := line["trace_id"]; present {
		t.Errorf("a line logged outside a span carries trace_id = %v", line["trace_id"])
	}
}
