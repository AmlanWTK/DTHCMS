// Package logging provides structured logging with two properties DTHCMS requires:
// every line carries a correlation ID, and no line can carry patient identity.
package logging

import (
	"context"
	"io"
	"log/slog"
	"strings"
)

// contextKey is unexported so no other package can collide with it.
type contextKey struct{ name string }

var correlationKey = &contextKey{"correlation_id"}

// WithCorrelationID returns a context carrying the correlation ID for this request.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationKey, id)
}

// CorrelationID returns the correlation ID from the context, or "" if absent.
func CorrelationID(ctx context.Context) string {
	if id, ok := ctx.Value(correlationKey).(string); ok {
		return id
	}
	return ""
}

// Options configures the logger.
type Options struct {
	// Level is one of debug, info, warn, error.
	Level string
	// Format is "json" or "text". Production is always json.
	Format string
	// Service names the process: api, worker, realtime, migrate.
	Service string
	// Version is the build version, so a log line can be tied to a build.
	Version string
}

// New builds the application logger.
func New(out io.Writer, opts Options) *slog.Logger {
	handlerOpts := &slog.HandlerOptions{Level: parseLevel(opts.Level)}

	var base slog.Handler
	if strings.EqualFold(opts.Format, "text") {
		base = slog.NewTextHandler(out, handlerOpts)
	} else {
		base = slog.NewJSONHandler(out, handlerOpts)
	}

	logger := slog.New(&redactingHandler{inner: base})

	if opts.Service != "" {
		logger = logger.With("service", opts.Service)
	}
	if opts.Version != "" {
		logger = logger.With("version", opts.Version)
	}
	return logger
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// redactingHandler enforces two rules on every record: the correlation ID from the
// context is attached, and any attribute logged under a PHI key is replaced.
//
// Redaction happens here rather than at call sites because a rule enforced in one place
// cannot be forgotten in another.
type redactingHandler struct {
	inner slog.Handler
}

func (h *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *redactingHandler) Handle(ctx context.Context, record slog.Record) error {
	out := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)

	if id := CorrelationID(ctx); id != "" {
		out.AddAttrs(slog.String("correlation_id", id))
	}

	record.Attrs(func(attr slog.Attr) bool {
		out.AddAttrs(redact(attr))
		return true
	})

	return h.inner.Handle(ctx, out)
}

func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		redacted = append(redacted, redact(attr))
	}
	return &redactingHandler{inner: h.inner.WithAttrs(redacted)}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{inner: h.inner.WithGroup(name)}
}

// redact replaces the value of a PHI-keyed attribute, recursing into groups so that
// nesting cannot be used — accidentally or otherwise — to smuggle a value through.
func redact(attr slog.Attr) slog.Attr {
	if _, banned := PHIKeys[strings.ToLower(attr.Key)]; banned {
		return slog.String(attr.Key, Redacted)
	}

	if attr.Value.Kind() == slog.KindGroup {
		group := attr.Value.Group()
		out := make([]slog.Attr, 0, len(group))
		for _, inner := range group {
			out = append(out, redact(inner))
		}
		return slog.Attr{Key: attr.Key, Value: slog.GroupValue(out...)}
	}

	return attr
}
