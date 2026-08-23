package telemetry

import (
	"context"
	"net/url"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	metricnoopkg "go.opentelemetry.io/otel/metric/noop"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/logging"
)

// Redacting wraps a span exporter so that no span leaves the process carrying patient
// identity.
//
// The redaction happens at export rather than at the call site for one reason: most
// attributes on a span were not put there by us. otelhttp, otelpgx and redisotel each
// add their own, and any library added later will too. A rule applied where spans leave
// the process covers all of them, including the ones that do not exist yet.
//
// Three things are scrubbed:
//
//   - Attributes whose key is in logging.PHIKeys — the same list the log handler and
//     dthclint use, so the rule has one definition.
//   - Query strings on URL attributes. A path like /api/patients/0190…/visits is fine —
//     a patient id is an opaque identifier and is what we ask people to log. A query
//     string is not: `?q=Rahima+Begum` is a patient's name, typed into a search box, and
//     it would otherwise travel to the telemetry backend in full.
//   - Database statement attributes containing literal values, which otelpgx can be
//     configured to include and which would carry whatever was written.
//
// Event attributes are scrubbed the same way; an event is just attributes with a name.
func Redacting(inner sdktrace.SpanExporter) sdktrace.SpanExporter {
	return &redactingExporter{inner: inner}
}

type redactingExporter struct {
	inner sdktrace.SpanExporter
}

func (e *redactingExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	safe := make([]sdktrace.ReadOnlySpan, len(spans))
	for i, span := range spans {
		safe[i] = redactedSpan{ReadOnlySpan: span}
	}
	return e.inner.ExportSpans(ctx, safe)
}

func (e *redactingExporter) Shutdown(ctx context.Context) error {
	return e.inner.Shutdown(ctx)
}

// redactedSpan presents a span with its attributes scrubbed.
//
// Embedding the interface rather than reimplementing it matters: ReadOnlySpan has an
// unexported method precisely so that it can grow, and everything not overridden here
// keeps working when it does.
type redactedSpan struct {
	sdktrace.ReadOnlySpan
}

func (s redactedSpan) Attributes() []attribute.KeyValue {
	return RedactAttributes(s.ReadOnlySpan.Attributes())
}

func (s redactedSpan) Events() []sdktrace.Event {
	events := s.ReadOnlySpan.Events()
	if len(events) == 0 {
		return events
	}
	out := make([]sdktrace.Event, len(events))
	for i, event := range events {
		event.Attributes = RedactAttributes(event.Attributes)
		out[i] = event
	}
	return out
}

// RedactAttributes returns the attributes with every unsafe value replaced.
//
// Exported so that anything constructing attributes outside a span — a metric label
// set, a log-to-trace bridge — can apply the identical rule rather than a similar one.
func RedactAttributes(attrs []attribute.KeyValue) []attribute.KeyValue {
	if len(attrs) == 0 {
		return attrs
	}

	out := make([]attribute.KeyValue, len(attrs))
	for i, attr := range attrs {
		out[i] = RedactAttribute(attr)
	}
	return out
}

// RedactAttribute applies the redaction rules to one attribute.
func RedactAttribute(attr attribute.KeyValue) attribute.KeyValue {
	key := string(attr.Key)

	if isPHIKey(key) {
		return attribute.String(key, logging.Redacted)
	}
	if isURLKey(key) && attr.Value.Type() == attribute.STRING {
		return attribute.String(key, stripQuery(attr.Value.AsString()))
	}
	if isStatementKey(key) && attr.Value.Type() == attribute.STRING {
		return attribute.String(key, redactStatement(attr.Value.AsString()))
	}
	return attr
}

// isPHIKey matches the log rule, including on the last segment of a dotted attribute
// key. OpenTelemetry conventions namespace attributes (`enduser.name`, `user.email`),
// and a rule that only matched the whole key would miss every one of them.
func isPHIKey(key string) bool {
	lower := strings.ToLower(key)
	if _, banned := logging.PHIKeys[lower]; banned {
		return true
	}
	if idx := strings.LastIndexAny(lower, "._"); idx >= 0 && idx+1 < len(lower) {
		if _, banned := logging.PHIKeys[lower[idx+1:]]; banned {
			return true
		}
	}
	return false
}

func isURLKey(key string) bool {
	switch strings.ToLower(key) {
	case "url.full", "url.query", "http.url", "http.target", "http.request.header.referer":
		return true
	}
	return false
}

func isStatementKey(key string) bool {
	switch strings.ToLower(key) {
	case "db.statement", "db.query.text", "db.query.parameter":
		return true
	}
	return false
}

// stripQuery keeps the path and discards everything a person could have typed.
func stripQuery(raw string) string {
	if raw == "" {
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		// Unparseable: keep the part before the first '?' and drop the rest. Guessing
		// wrong in the safe direction costs a little context in a trace.
		if idx := strings.IndexByte(raw, '?'); idx >= 0 {
			return raw[:idx] + "?" + logging.Redacted
		}
		return raw
	}
	if parsed.RawQuery == "" && parsed.Fragment == "" {
		return raw
	}
	parsed.RawQuery = logging.Redacted
	parsed.Fragment = ""
	return parsed.String()
}

// redactStatement keeps the shape of a query and removes anything that could be a value.
//
// Parameterised SQL is safe — `$1` carries nothing. String and numeric literals are not:
// a query built by string concatenation, or a driver configured to interpolate, would
// otherwise put a patient's name into a span. Keeping the statement shape is what makes
// a slow-query trace useful, so the shape is what survives.
func redactStatement(statement string) string {
	if statement == "" {
		return statement
	}

	var b strings.Builder
	b.Grow(len(statement))

	inString := false
	for i := 0; i < len(statement); i++ {
		c := statement[i]

		if inString {
			if c == '\'' {
				// '' is an escaped quote inside a literal, not the end of it.
				if i+1 < len(statement) && statement[i+1] == '\'' {
					i++
					continue
				}
				inString = false
				b.WriteString("?'")
			}
			continue
		}

		if c == '\'' {
			inString = true
			b.WriteString("'")
			continue
		}
		b.WriteByte(c)
	}

	if inString {
		// An unterminated literal means the statement was truncated mid-value.
		b.WriteString("?")
	}
	return b.String()
}

// metricnoop returns a meter provider that records nothing.
func metricnoop() metricnoopkg.MeterProvider {
	return metricnoopkg.NewMeterProvider()
}
