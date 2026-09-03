package httpx

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
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
// Authentication (CP16), device verification (CP18) and authorisation (CP20) are real;
// rate limiting is the placeholder that remains until CP49.

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

// Hijack passes the connection through to the server (CP26).
//
// A WebSocket handshake answers 101 and then takes the socket over; a wrapper that does not
// forward Hijack turns that into "the realtime gateway does not work behind the access log",
// which is a mystery to debug and a one-line fix to prevent. The status is recorded as 101
// so the log line is truthful about what happened.
func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := s.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("httpx: the underlying ResponseWriter cannot be hijacked")
	}
	if s.status == 0 || s.status == http.StatusOK {
		s.status = http.StatusSwitchingProtocols
	}
	return hijacker.Hijack()
}

// Flush passes a flush through, for the same reason.
func (s *statusRecorder) Flush() {
	if flusher, ok := s.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
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
					"Content-Type, Authorization, "+RequestIDHeader+", Idempotency-Key, "+RequestedWithHeader+
						", "+StepUpHeader+", "+ActiveRoleHeader+", "+deviceHeaders)
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

// Authenticator resolves an access token to the caller holding it.
//
// An interface rather than the auth package directly, because platform may not import a
// module (architecture.json), and because the middleware's job is to attach an identity to
// a request rather than to know how one is established.
type Authenticator interface {
	// Identify returns the caller, or an error if the token authenticates nobody. The error
	// is deliberately undifferentiated: unknown, expired, revoked and suspended all produce
	// the same refusal, and the middleware has no business telling them apart.
	Identify(ctx context.Context, token string) (Caller, error)
}

// Caller is who is making a request, once the token has been resolved.
type Caller struct {
	UserID      string
	FacilityID  string
	SessionID   string
	Code        string
	Permissions []string
	// DeviceID is the device the session was opened from, or empty for a session opened
	// without one (a browser). A session with a device must be used from that device;
	// VerifyDevice enforces it.
	DeviceID string
	// Roles are the codes of every live role. ActiveRole is the hat named by the request
	// (X-Active-Role), unverified here: the engine refuses one the caller does not hold.
	Roles      []string
	ActiveRole string
}

type callerKey struct{}

// CallerFrom returns the authenticated caller, if the request carried one.
func CallerFrom(ctx context.Context) (Caller, bool) {
	caller, ok := ctx.Value(callerKey{}).(Caller)
	return caller, ok
}

// CallerForTest wraps a handler with an authenticated caller already on the context,
// exactly as Authenticate would have left it.
//
// Caller is what every permission decision is made from, so a public constructor for one
// is a public door into the authenticated chain. dthclint refuses a call to this from
// anything but a _test.go file, which is what keeps that door shut; production code has
// one way to put a caller on a context, and it is Authenticate.
//
//dthclint:testonly
func CallerForTest(caller Caller, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, caller)))
	})
}

// Authenticate verifies the access token and puts the caller on the context.
//
// Every request, every time. There is no cache in front of this and no fast path around it:
// CP16's acceptance criterion 3 says a revoked session stops working within one request, and
// the only way to mean that is to ask on each one (ADR-0011).
//
// A nil authenticator leaves the chain a pass-through, which is what the tests that care
// about routing rather than identity want — and what the service does before CP16 is wired.
func Authenticate(logger *slog.Logger, authenticator Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if authenticator == nil {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := accessToken(r)
			if !ok {
				WriteError(w, r, logger, errs.ErrUnauthenticated)
				return
			}

			caller, err := authenticator.Identify(r.Context(), token)
			if err != nil {
				// Logged at info, not warn: an expired token on the first request after a
				// tea break is the system working, and a log that cries wolf about it is a
				// log nobody reads on the morning it matters.
				logger.InfoContext(r.Context(), "request not authenticated",
					"path", r.URL.Path, "method", r.Method)
				WriteError(w, r, logger, errs.ErrUnauthenticated)
				return
			}

			ctx := context.WithValue(r.Context(), callerKey{}, caller)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// SessionCookie carries the access token for browser clients.
//
// Two transports for one token, on purpose. The station app holds the token itself and sends
// it as a bearer header. The web application must not hold it at all — ADR-0010: a token
// JavaScript can read is a token a cross-site scripting hole can steal — so for the browser
// the token travels in an httpOnly cookie it cannot see. The middleware accepts either and
// the token is the same 32 random bytes whichever way it arrives.
//
// A cookie the browser attaches by itself is attached to requests the user did not intend.
// That is what RequireRequestedWith is for.
const SessionCookie = "dthcms.session"

// accessToken reads the token from the Authorization header, or failing that the session
// cookie. The header wins when both are present, so a client that has deliberately signed
// out on one surface is not silently re-authenticated by a stale cookie.
func accessToken(r *http.Request) (string, bool) {
	if token, ok := bearerToken(r); ok {
		return token, true
	}
	cookie, err := r.Cookie(SessionCookie)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return "", false
	}
	return cookie.Value, true
}

func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "

	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	return token, token != ""
}

// RequestedWithHeader and RequestedWithValue are the cross-site request forgery guard.
//
// Every request that can change something must carry `X-Requested-With: DTHCMS`. A form on
// another site cannot set that header. A script on another site cannot either without a
// CORS preflight, and the preflight is refused for any origin not on the allowlist. So a
// request carrying it was made by our own code, on our own origin — which is the whole
// question CSRF asks.
//
// It is required of every client, bearer or cookie, rather than only where a cookie was used.
// One rule with no branches is one rule that cannot be bypassed by arriving through the
// other door, and the station app adding a constant header costs nothing.
//
// Together with SameSite=Lax on the cookies and the origin allowlist, this is the "token on
// state-changing requests" ADR-0010 promised for CP16. A header rather than a rotating
// per-session token because the API holds no state per form, and a synchroniser token
// without state to synchronise against is a header with extra steps.
const (
	RequestedWithHeader = "X-Requested-With"
	RequestedWithValue  = "DTHCMS"
)

// RequireRequestedWith refuses state-changing requests that do not carry the guard header.
//
// GET, HEAD and OPTIONS pass: they change nothing, and a browser's own navigations and
// preflights cannot carry custom headers.
func RequireRequestedWith(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}
			if r.Header.Get(RequestedWithHeader) != RequestedWithValue {
				WriteError(w, r, logger, errs.ErrForbidden.WithDetail(
					errors.New("state-changing requests must carry "+RequestedWithHeader+": "+RequestedWithValue)))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// --- device verification (CP18) ---

// DeviceProof is what a request presented to prove which device it came from. Built by
// the middleware from the headers; verified by the auth module, which holds the keys.
type DeviceProof struct {
	DeviceID   string
	Timestamp  int64
	Nonce      string
	Signature  []byte
	Method     string
	Path       string
	BodyDigest [32]byte
	AppVersion string
}

// DeviceIdentity is what a verified proof establishes.
type DeviceIdentity struct {
	DeviceID   string
	FacilityID string
	Name       string
	KeyID      string
}

// DeviceVerifier checks a proof against the enrolled keys.
//
// An interface, like Authenticator, because platform may not import a module. The
// middleware's job is to refuse a request whose device does not check out and to attach
// the device to the one that does; whose key it is and what a nonce is are auth's business.
type DeviceVerifier interface {
	// VerifyDevice returns the device, or an error. The middleware distinguishes only
	// whether the proof was malformed (a client bug: 400) from every other refusal (401).
	VerifyDevice(ctx context.Context, proof DeviceProof) (DeviceIdentity, error)
}

// ErrDeviceProofMalformed is what a verifier returns for a proof that could not have been
// produced by a working client — a bad nonce, a short signature, an unparsable id.
var ErrDeviceProofMalformed = errors.New("device proof is malformed")

// Device header names. Duplicated from auth/devicesig rather than imported, because platform
// may not import auth; devicesig_test asserts the two agree.
const (
	DeviceIDHeader         = "X-Device-Id"
	DeviceTimestampHeader  = "X-Device-Timestamp"
	DeviceNonceHeader      = "X-Device-Nonce"
	DeviceSignatureHeader  = "X-Device-Signature"
	DeviceAppVersionHeader = "X-Device-App-Version"
)

// deviceHeaders is the CORS allowlist addition.
var deviceHeaders = DeviceIDHeader + ", " + DeviceTimestampHeader + ", " + DeviceNonceHeader +
	", " + DeviceSignatureHeader + ", " + DeviceAppVersionHeader

type deviceKey struct{}

// DeviceFrom returns the verified device, if the request came from one.
func DeviceFrom(ctx context.Context) (DeviceIdentity, bool) {
	device, ok := ctx.Value(deviceKey{}).(DeviceIdentity)
	return device, ok
}

// maxSignedBody bounds what the middleware will buffer to digest. Clinical writes are
// small; anything larger (a scanned record upload) is not device-signed over its body and
// gets its own scheme at the checkpoint that has uploads.
const maxSignedBody = 1 << 20

// VerifyDevice checks the device proof on a request that carries one, and enforces that a
// session opened from a device is only ever used from that device.
//
// Four outcomes:
//
//   - no device headers, session has no device: a browser. Passes; DeviceFrom reports
//     nothing; RequireDevice refuses it later if the route is a clinical write.
//   - no device headers, session has a device: a stolen access token being used from
//     somewhere that is not the tablet. Refused.
//   - device headers, proof fails: refused, and if the signature was the problem the auth
//     module has already recorded it against the device.
//   - device headers, proof verifies, and either the session has no device (the login
//     request itself, which binds it) or the same one: passes, device on the context.
//
// It runs after Authenticate in the /v1 chain, so the caller is known; in the /v1/auth
// corner there is no caller yet and only the proof is checked. A nil verifier leaves the
// chain a pass-through — for the routing tests — except that a session bound to a device
// is still refused without one, because that check needs no keys.
func VerifyDevice(logger *slog.Logger, verifier DeviceVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller, hasCaller := CallerFrom(r.Context())
			id := strings.TrimSpace(r.Header.Get(DeviceIDHeader))

			if id == "" {
				if hasCaller && caller.DeviceID != "" {
					logger.WarnContext(r.Context(), "device-bound session used without its device",
						"path", r.URL.Path, "method", r.Method)
					WriteError(w, r, logger, errs.ErrUnauthenticated.WithDetail(
						errors.New("this session was opened from a device and must be used from it")))
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			if verifier == nil {
				WriteError(w, r, logger, errs.ErrUnauthenticated.WithDetail(
					errors.New("device verification is not available")))
				return
			}

			proof, err := proofFromRequest(r)
			if err != nil {
				WriteError(w, r, logger, errs.ErrBadRequest.WithDetail(err))
				return
			}
			device, err := verifier.VerifyDevice(r.Context(), proof)
			if err != nil {
				if errors.Is(err, ErrDeviceProofMalformed) {
					WriteError(w, r, logger, errs.ErrBadRequest.WithDetail(err))
					return
				}
				logger.InfoContext(r.Context(), "device refused",
					"path", r.URL.Path, "method", r.Method, "reason", err.Error())
				WriteError(w, r, logger, errs.ErrUnauthenticated.WithDetail(
					errors.New("the device is not enrolled, or the request was not signed by it")))
				return
			}
			if hasCaller && caller.DeviceID != "" && caller.DeviceID != device.DeviceID {
				logger.WarnContext(r.Context(), "session presented from a different device",
					"path", r.URL.Path)
				WriteError(w, r, logger, errs.ErrUnauthenticated.WithDetail(
					errors.New("this session belongs to a different device")))
				return
			}

			ctx := context.WithValue(r.Context(), deviceKey{}, device)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// EnforceDeviceBinding refuses a device-bound session that is not being used from its
// device. The half of VerifyDevice that needs no keys, for a group where the proof was
// checked before the caller was known — the private half of /v1/auth.
func EnforceDeviceBinding(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller, ok := CallerFrom(r.Context())
			if ok && caller.DeviceID != "" {
				device, present := DeviceFrom(r.Context())
				if !present || device.DeviceID != caller.DeviceID {
					logger.WarnContext(r.Context(), "device-bound session used without its device",
						"path", r.URL.Path, "method", r.Method)
					WriteError(w, r, logger, errs.ErrUnauthenticated.WithDetail(
						errors.New("this session was opened from a device and must be used from it")))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// proofFromRequest reads the headers and digests the body, leaving the body readable for
// the handler. Only called when a device id was presented, so a browser's request is never
// buffered.
func proofFromRequest(r *http.Request) (DeviceProof, error) {
	ts, err := strconv.ParseInt(strings.TrimSpace(r.Header.Get(DeviceTimestampHeader)), 10, 64)
	if err != nil || ts <= 0 {
		return DeviceProof{}, errors.New(DeviceTimestampHeader + " must be seconds since the epoch")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(r.Header.Get(DeviceSignatureHeader)))
	if err != nil || len(sig) == 0 {
		return DeviceProof{}, errors.New(DeviceSignatureHeader + " must be base64")
	}
	nonce := strings.TrimSpace(r.Header.Get(DeviceNonceHeader))
	if nonce == "" {
		return DeviceProof{}, errors.New(DeviceNonceHeader + " is required")
	}

	var body []byte
	if r.Body != nil && r.Body != http.NoBody {
		body, err = io.ReadAll(io.LimitReader(r.Body, maxSignedBody+1))
		if err != nil {
			return DeviceProof{}, errors.New("reading the request body")
		}
		if len(body) > maxSignedBody {
			return DeviceProof{}, errors.New("a device-signed body may not exceed 1 MiB")
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
	}

	return DeviceProof{
		DeviceID:   strings.TrimSpace(r.Header.Get(DeviceIDHeader)),
		Timestamp:  ts,
		Nonce:      nonce,
		Signature:  sig,
		Method:     r.Method,
		Path:       r.URL.Path,
		BodyDigest: sha256.Sum256(body),
		AppVersion: strings.TrimSpace(r.Header.Get(DeviceAppVersionHeader)),
	}, nil
}

// RequireDevice refuses a request that did not come from a verified device.
//
// For the routes that write clinical events: D-46 says an unenrolled device may
// authenticate a person but may not write. The refusal names the rule, because the person
// seeing it is a clinician at a desktop wondering why the tablet works and the browser
// does not.
func RequireDevice(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := DeviceFrom(r.Context()); !ok {
				WriteError(w, r, logger, errs.ErrDeviceRequired)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimit will apply per-user, per-device and per-endpoint limits.
//
// Implemented at CP49 alongside the other API hardening.
func RateLimit(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

// --- step-up ---

// StepUpHeader carries a step-up token to a privileged endpoint.
const StepUpHeader = "X-Step-Up-Token"

// StepUpVerifier consumes a step-up token for one purpose on behalf of one session.
//
// An interface for the reason Authenticator is one: platform may not import a module, and
// the middleware's job is to refuse a request that has not stepped up, not to know what a
// second factor is.
type StepUpVerifier interface {
	// ConsumeStepUp returns nil when the token is valid for the session and purpose, and
	// spends it. Any error is a refusal; the middleware does not tell them apart.
	ConsumeStepUp(ctx context.Context, token string, sessionID string, purpose string) error
}

// RequireStepUp refuses a request that does not carry a valid step-up token for purpose.
//
// It sits after Authenticate, because a step-up is a property of a session. A request with
// no caller is refused as unauthenticated; a caller with no token, a spent token, a token
// minted for another purpose or another session, is refused as forbidden — the same
// answer for all of them, and the reason in the security event log.
//
// The token is consumed on success. A privileged action is authorised once; the next one
// asks again.
func RequireStepUp(logger *slog.Logger, verifier StepUpVerifier, purpose string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller, ok := CallerFrom(r.Context())
			if !ok {
				WriteError(w, r, logger, errs.ErrUnauthenticated)
				return
			}
			token := strings.TrimSpace(r.Header.Get(StepUpHeader))
			if token == "" || verifier == nil {
				WriteError(w, r, logger, errs.ErrStepUpRequired.WithDetail(
					errors.New("this action needs a fresh second factor: "+purpose)))
				return
			}
			if err := verifier.ConsumeStepUp(r.Context(), token, caller.SessionID, purpose); err != nil {
				logger.InfoContext(r.Context(), "step-up refused",
					"purpose", purpose, "path", r.URL.Path)
				WriteError(w, r, logger, errs.ErrStepUpRequired.WithDetail(err))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
