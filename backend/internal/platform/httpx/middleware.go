package httpx

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/logging"
)

// The middleware order is deliberate and documented in the implementation plan (8.3):
//
//	recover → request ID → trace and measure → access log → security headers → CORS
//	  → body limit → timeout → authenticate → verify device → authorise → rate limit
//	  → handler
//
// Tracing sits third: it must be inside the request ID so that a span can carry the
// correlation ID, and outside the access log so that every log line for the request
// already has a trace to belong to.
//
// Two properties depend on that order. Cheap checks fail first, so an unauthenticated
// request cannot consume a rate-limit slot belonging to a real user. And authorisation
// runs before any handler touches a resource, so a 403 never reveals whether the
// resource exists.
//
// Authentication, device verification, authorisation and rate limiting are placeholders
// at CP05. They exist now so that the chain has its final shape, and so that the
// checkpoint that implements each one changes a single function rather than the
// architecture.

// RequestIDHeader is echoed back so a clinic operator reporting a problem can quote it.
const RequestIDHeader = "X-Request-ID"

// Recover turns a panic into a 500 without taking the process down.
//
// A panic in one request must never end a clinic session for everyone else.
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.ErrorContext(r.Context(), "panic recovered",
						"panic", fmt.Sprint(recovered),
						"stack", string(debug.Stack()),
						"path", r.URL.Path,
						"method", r.Method)

					WriteError(w, r, logger, errs.ErrInternal.WithDetail(
						fmt.Errorf("panic: %v", recovered)))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// RequestID assigns a correlation ID to every request and puts it in the context, so
// that every log line, job and downstream call can be tied back to one interaction.
func RequestID(gen ids.Generator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(RequestIDHeader)
			if id == "" || len(id) > 64 {
				id = gen.New()
			}

			w.Header().Set(RequestIDHeader, id)
			ctx := logging.WithCorrelationID(r.Context(), id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// statusRecorder captures the status code for the access log.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// AccessLog records one line per request.
//
// It logs the method, path, status, duration and size — and nothing from the body or
// the query string, either of which may carry a patient identifier.
func AccessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rec, r)

			logger.InfoContext(r.Context(), "request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"bytes", rec.bytes,
				"duration_ms", time.Since(start).Milliseconds())
		})
	}
}

// SecurityHeaders applies the headers that cost nothing and prevent whole classes of
// attack. The API serves JSON only, so the content security policy can be maximally
// restrictive.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// CORS allows exactly the listed origins and nothing else.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[strings.TrimSpace(origin)] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			if origin != "" && allowed[origin] {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Credentials", "true")
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers",
					"Content-Type, Authorization, "+RequestIDHeader+", Idempotency-Key")
				h.Set("Access-Control-Max-Age", "600")
				h.Add("Vary", "Origin")
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// BodyLimit caps request bodies. Without it, a single client can exhaust memory.
func BodyLimit(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}

// Timeout bounds how long a request may run.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// --- Placeholders. Each is replaced by the checkpoint named in its comment. ---

// Authenticate will verify the access token and put the session on the context.
//
// Implemented at CP16. Until then it is a pass-through: there are no protected routes,
// because there are no routes that touch data.
func Authenticate(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

// VerifyDevice will check that the request comes from an enrolled, non-revoked device
// and bind the device identity to the request, so that every clinical event can carry a
// device_id that is evidence rather than a claim (R-03).
//
// Implemented at CP18.
func VerifyDevice(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

// Authorize will enforce the role's permission for the route, denying by default.
//
// Implemented at CP20, at which point a route with no declared permission will prevent
// the service from starting at all.
func Authorize(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

// RateLimit will apply per-user, per-device and per-endpoint limits.
//
// Implemented at CP49 alongside the other API hardening.
func RateLimit(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}
