package httpx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
)

// Idempotency (CP24, blueprint §7.5 layer 2).
//
// A clinic's network drops. The tablet sent a measurement, saw no response, and retries.
// Without this the retry is a second measurement; with it the retry is handed the first
// response, byte for byte, and the record has one value with one attribution.
//
// The header is `Idempotency-Key` and the key is the client's to choose — a UUIDv7 per
// attempt, held across retries of that attempt. The middleware:
//
//   - claims the key, atomically, so two concurrent retries cannot both proceed;
//   - runs the handler once and stores what it wrote;
//   - answers every later retry from the store, with `Idempotency-Replayed: true`;
//   - refuses a key reused for a *different* request (409) rather than answering it with
//     the first request's response, which would be the worse failure.
//
// A handler that failed leaves no claim behind: a 5xx is a retryable outcome, and a client
// that retries it must not be met with "in progress" until the record expires.

// IdempotencyHeader is the request header carrying the key.
const IdempotencyHeader = "Idempotency-Key"

// IdempotencyReplayedHeader marks a response served from the store.
const IdempotencyReplayedHeader = "Idempotency-Replayed"

// DefaultIdempotencyTTL is how long a response is kept. Long enough for a tablet that spent
// the morning offline to retry, short enough that the table stays small.
const DefaultIdempotencyTTL = 24 * time.Hour

// MaxIdempotentResponse is the largest body kept. A bigger response is served normally and
// simply not cached; the event-level guarantee (§7.5 layer 1) still holds.
const MaxIdempotentResponse = 256 << 10

// IdempotencyRecord is a claim, and the response once there is one.
type IdempotencyRecord struct {
	Fingerprint []byte
	Complete    bool
	Status      int
	Headers     map[string]string
	Body        []byte
}

// IdempotencyStore is the persistence the middleware needs.
//
// An interface for the platform's usual reason: the middleware's job is the protocol, not
// the SQL. internal/platform/idempotency provides the Postgres implementation.
type IdempotencyStore interface {
	// Claim inserts a claim and reports whether this caller won it. When it did not, the
	// existing record is returned so the middleware can replay or refuse it.
	//
	// Both times are the caller's: a store that read its own clock would disagree with the
	// middleware's whenever the two differ, which in a test with a fixed clock is always,
	// and in production is whenever a machine's clock drifts.
	Claim(ctx context.Context, userID, facilityID, key string, fingerprint []byte, claimed, expires time.Time) (won bool, existing IdempotencyRecord, err error)
	// Complete stores the response against a claim this caller won.
	Complete(ctx context.Context, userID, key string, status int, headers map[string]string, body []byte, at time.Time) error
	// Release drops a claim whose handler did not produce a cacheable response.
	Release(ctx context.Context, userID, key string) error
}

// ErrIdempotencyKeyReused is what the middleware answers when a key is presented with a
// request that is not the one it was claimed for.
var errKeyReused = errs.New("IDEMPOTENCY_KEY_REUSED", errs.KindConflict, http.StatusConflict,
	"This request was sent with a key that was already used for a different request.",
	"এই অনুরোধটি এমন একটি কী দিয়ে পাঠানো হয়েছে যা আগে অন্য একটি অনুরোধে ব্যবহৃত হয়েছে।")

// errInProgress is what a caller gets when the first attempt is still running.
var errInProgress = errs.New("IDEMPOTENCY_IN_PROGRESS", errs.KindConflict, http.StatusConflict,
	"The same request is still being processed. Try again in a moment.",
	"একই অনুরোধ এখনো প্রক্রিয়া করা হচ্ছে। একটু পরে আবার চেষ্টা করুন।")

// IdempotencyConfig assembles the middleware.
type IdempotencyConfig struct {
	Store  IdempotencyStore
	Clock  clock.Clock
	Logger *slog.Logger
	TTL    time.Duration
	// Required makes a missing key on a mutating request a refusal rather than a
	// pass-through. The router sets it; it is off by default so that a chain assembled
	// without a store behaves the same as one assembled with it.
	Required bool
}

// Idempotent returns the middleware. A nil store leaves the chain a pass-through, which is
// what the routing tests want and what the service is before the store is wired.
func Idempotent(cfg IdempotencyConfig) func(http.Handler) http.Handler {
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.TTL <= 0 {
		cfg.TTL = DefaultIdempotencyTTL
	}

	return func(next http.Handler) http.Handler {
		if cfg.Store == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := strings.TrimSpace(r.Header.Get(IdempotencyHeader))
			if key == "" {
				if cfg.Required && isMutating(r.Method) {
					WriteError(w, r, cfg.Logger, errs.ErrValidation.WithField(IdempotencyHeader,
						"required on every state-changing request: send a UUIDv7, and re-send the same one on a retry"))
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			if len(key) < 8 || len(key) > 200 {
				WriteError(w, r, cfg.Logger, errs.ErrValidation.WithField(IdempotencyHeader, "8 to 200 characters"))
				return
			}
			caller, ok := CallerFrom(r.Context())
			if !ok {
				// Unauthenticated: there is no one to scope the key to. The endpoints
				// outside the authenticated chain are login and refresh, which are not
				// idempotent in this sense.
				next.ServeHTTP(w, r)
				return
			}

			body, err := io.ReadAll(r.Body)
			if err != nil {
				WriteError(w, r, cfg.Logger, errs.ErrBadRequest.WithDetail(err))
				return
			}
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))

			fingerprint := requestFingerprint(r.Method, r.URL.Path, body)
			now := cfg.Clock.Now()

			won, existing, err := cfg.Store.Claim(r.Context(), caller.UserID, caller.FacilityID, key, fingerprint, now, now.Add(cfg.TTL))
			if err != nil {
				WriteError(w, r, cfg.Logger, errs.ErrInternal.WithDetail(err))
				return
			}
			if !won {
				if !bytes.Equal(existing.Fingerprint, fingerprint) {
					WriteError(w, r, cfg.Logger, errKeyReused)
					return
				}
				if !existing.Complete {
					WriteError(w, r, cfg.Logger, errInProgress)
					return
				}
				replay(w, existing)
				return
			}

			recorder := &recordingWriter{ResponseWriter: w, limit: MaxIdempotentResponse}
			next.ServeHTTP(recorder, r)
			recorder.flushStatus()

			// Only a settled answer is worth replaying. A 5xx is the server saying "try
			// again", and a stored 500 would turn a transient failure into a permanent one.
			// The chain's own refusals are excluded for the same reason in reverse: 401,
			// 403 and 429 describe the state of the session, the grant or the rate limiter
			// at one instant, and caching one would keep answering it for a day after the
			// token was refreshed or the role was granted.
			if !cacheable(recorder.status) || recorder.overflowed {
				if err := cfg.Store.Release(r.Context(), caller.UserID, key); err != nil {
					cfg.Logger.WarnContext(r.Context(), "idempotency claim not released", "error", err)
				}
				return
			}
			if err := cfg.Store.Complete(r.Context(), caller.UserID, key, recorder.status, recorder.captured(), recorder.body.Bytes(), cfg.Clock.Now()); err != nil {
				// The response has already gone to the client; a failure to remember it
				// means the next retry runs the handler again, which the event-level
				// guarantee still makes safe.
				cfg.Logger.ErrorContext(r.Context(), "idempotent response not stored", "error", err)
			}
		})
	}
}

// cacheable says whether a response is a settled answer to this request rather than a
// statement about the moment it arrived.
func cacheable(status int) bool {
	switch status {
	case 0, http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		return false
	}
	return status < 500
}

func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// requestFingerprint is what makes "the same request" mean something: the method, the path
// and the body. Headers are deliberately out — a retry may legitimately carry a new
// correlation id or a refreshed token.
func requestFingerprint(method, path string, body []byte) []byte {
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte{0})
	h.Write([]byte(path))
	h.Write([]byte{0})
	h.Write(body)
	sum := h.Sum(nil)
	return sum
}

func replay(w http.ResponseWriter, record IdempotencyRecord) {
	for name, value := range record.Headers {
		w.Header().Set(name, value)
	}
	w.Header().Set(IdempotencyReplayedHeader, "true")
	w.WriteHeader(record.Status)
	_, _ = w.Write(record.Body)
}

// cachedHeaders are the response headers worth replaying. The request id is not among
// them: the replay is a different request and its own id belongs in the log.
var cachedHeaders = []string{"Content-Type", "Location", "Content-Language"}

// recordingWriter keeps what the handler wrote so it can be stored and replayed.
type recordingWriter struct {
	http.ResponseWriter
	status     int
	body       bytes.Buffer
	limit      int
	overflowed bool
	wrote      bool
}

func (w *recordingWriter) WriteHeader(status int) {
	if w.wrote {
		return
	}
	w.status = status
	w.wrote = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *recordingWriter) Write(p []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	if w.body.Len()+len(p) > w.limit {
		w.overflowed = true
		w.body.Reset()
	}
	if !w.overflowed {
		w.body.Write(p)
	}
	return w.ResponseWriter.Write(p)
}

// flushStatus records the implicit 200 of a handler that wrote nothing at all.
func (w *recordingWriter) flushStatus() {
	if !w.wrote {
		w.status = http.StatusOK
	}
}

func (w *recordingWriter) captured() map[string]string {
	out := map[string]string{}
	for _, name := range cachedHeaders {
		if v := w.Header().Get(name); v != "" {
			out[name] = v
		}
	}
	return out
}

// EncodeHeaders and DecodeHeaders move the cached header map through the store's jsonb
// column. Here rather than in the store so the shape is the middleware's business.
func EncodeHeaders(headers map[string]string) ([]byte, error) { return json.Marshal(headers) }

func DecodeHeaders(raw []byte) map[string]string {
	out := map[string]string{}
	if len(raw) == 0 {
		return out
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]string{}
	}
	return out
}

// ErrNoClaim is returned by a store asked to complete a claim that is not there.
var ErrNoClaim = errors.New("httpx: no idempotency claim to complete")
