package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
)

// Rule is one route's budget for one caller.
//
// Two numbers rather than "requests per minute", because the two answer different
// questions and a single rate answers neither well. Burst is how many requests may arrive
// at once — a tablet that comes back into signal and pushes four batches back to back is
// normal, and a limiter that spaced them out would make the reconnection slower than the
// outage. Every is how fast the budget refills, which is what bounds a client that never
// stops.
type Rule struct {
	// Burst is how many requests may arrive together before the refill rate starts to bite.
	Burst int
	// Every is how long one unit of budget takes to come back.
	Every time.Duration
}

// Decision is a limiter's answer.
type Decision struct {
	// Allowed is whether this request may proceed.
	Allowed bool
	// RetryAfter is how long until it could, and is only meaningful when Allowed is false.
	RetryAfter time.Duration
}

// Limiter counts requests against a rule.
//
// An interface for the reason Authenticator and DeviceVerifier are: platform may not
// import a module, and the middleware's job is to refuse a request that is over budget,
// not to know that the counter lives in Redis. It also lets the middleware's own tests run
// without one.
//
// Implementations must be atomic across processes. Two API instances sharing a Redis is
// the deployment this system has; a per-process counter would give a client as many
// budgets as there are instances, which is the failure mode that makes a limiter look
// present and do nothing.
type Limiter interface {
	// Take consumes one unit from the bucket named key. An error is not a refusal — see
	// RateLimit's fail-open note.
	Take(ctx context.Context, key string, rule Rule, now time.Time) (Decision, error)
}

// RateLimitConfig configures the middleware.
type RateLimitConfig struct {
	Logger  *slog.Logger
	Limiter Limiter
	Clock   clock.Clock

	// Rules are the routes that have a budget, keyed exactly as VerifyDevice keys its
	// quarantine exemptions: "METHOD /path", whole paths, no prefixes.
	//
	// A map rather than a per-route declaration because chi resolves the route pattern
	// *after* Use middleware runs, so there is nothing for a route's own declaration to be
	// read from at the point the decision has to be made. Whole paths so that no route
	// inherits a neighbour's budget by sharing a prefix with it.
	//
	// A route not in this map has no budget. That is deliberate for now: D-49 wants
	// per-user, per-device and per-endpoint-class limits across the whole API, which is a
	// hardening checkpoint of its own, and a limiter switched on everywhere at once with
	// numbers nobody has measured is how a clinic discovers rate limiting during a busy
	// morning. What is here is CP65's line — *per-device rate limits* on the sync
	// endpoints — on the two routes where the absence of one had a consequence.
	Rules map[string]Rule
}

// RateLimit refuses a request that is over its route's budget for the caller who sent it.
//
// # Why it keys on the device first
//
// A tablet is the unit of abuse here, not a person. The specific case this exists for is a
// device the clinic has revoked: since CP65 it may still reach the sync push, because the
// alternative was destroying the measurements it is holding, and what it writes there goes
// into a table `dthcms_app` may not DELETE from. One stolen tablet with a live key could
// otherwise append to that table until the disk filled, and every row is one a supervisor
// has to look at. Keying on the person instead would let the same tablet spend a different
// budget for every clinician who has ever signed in on it.
//
// A request with no device — a browser — is keyed on the user, which is the next narrowest
// thing that is actually verified. Never the socket address: a clinic behind one router is
// one address, and limiting on it would mean the busiest station rate-limits the others.
//
// # It fails open, deliberately
//
// A limiter that cannot reach its counter allows the request and logs it. The alternative
// is a clinic that stops taking blood pressures because Redis restarted, and this is a
// system where the cost of refusing real clinical work is higher than the cost of a
// window with no limit. What makes that acceptable rather than negligent is that the
// consequence the limiter exists to bound is bounded a second time in the database: the
// quarantine has a per-device cap enforced by a trigger, which is still there when Redis
// is not (migrations/00050_sync_limits.sql).
//
// The warning is coalesced to one a minute. An outage that logged per request would bury
// the clinical log in the exact minute somebody needs to read it.
func RateLimit(cfg RateLimitConfig) func(http.Handler) http.Handler {
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	var warned atomic.Int64

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rule, limited := cfg.Rules[r.Method+" "+r.URL.Path]
			if !limited || cfg.Limiter == nil {
				next.ServeHTTP(w, r)
				return
			}

			scope, id := rateLimitSubject(r.Context())
			if id == "" {
				// Unreachable on this chain: Authenticate runs before this and refuses a
				// request with no caller. Logged at error rather than passed over silently
				// because if it ever happens the chain has been rewired, and a limiter
				// keyed on nothing is a limiter that is not running.
				cfg.Logger.ErrorContext(r.Context(), "rate limit has nobody to charge",
					"route", r.Method+" "+r.URL.Path)
				next.ServeHTTP(w, r)
				return
			}

			key := r.Method + " " + r.URL.Path + "|" + scope + ":" + id
			decision, err := cfg.Limiter.Take(r.Context(), key, rule, cfg.Clock.Now())
			if err != nil {
				if shouldWarn(&warned, cfg.Clock.Now(), time.Minute) {
					cfg.Logger.WarnContext(r.Context(), "rate limiter unavailable; requests are not being counted",
						"route", r.Method+" "+r.URL.Path, "error", err.Error())
				}
				next.ServeHTTP(w, r)
				return
			}
			if decision.Allowed {
				next.ServeHTTP(w, r)
				return
			}

			// Seconds, rounded up, and never zero: Retry-After: 0 reads as "immediately",
			// which would send a well-behaved client straight back into the refusal.
			seconds := int(decision.RetryAfter / time.Second)
			if decision.RetryAfter%time.Second != 0 || seconds == 0 {
				seconds++
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))

			// At info, with the subject and the route. Not the request body, and not
			// anything from it: a rate-limit line is written most often when something is
			// wrong, which is when the log is most likely to be read by somebody who
			// should not be reading clinical values.
			cfg.Logger.InfoContext(r.Context(), "rate limited",
				"route", r.Method+" "+r.URL.Path, "scope", scope, "subject", id,
				"retry_after_seconds", seconds)

			WriteError(w, r, cfg.Logger, errs.ErrRateLimited)
		})
	}
}

// rateLimitSubject is who this request is charged to.
func rateLimitSubject(ctx context.Context) (scope, id string) {
	if device, ok := DeviceFrom(ctx); ok && device.DeviceID != "" {
		return "device", device.DeviceID
	}
	if caller, ok := CallerFrom(ctx); ok && caller.UserID != "" {
		return "user", caller.UserID
	}
	return "", ""
}

// shouldWarn reports whether enough time has passed to log again, and claims the slot.
//
// Compare-and-swap rather than a mutex because the contended case is a Redis outage, when
// every in-flight request arrives here at once and the one thing this must not do is
// serialise them behind a lock.
func shouldWarn(last *atomic.Int64, now time.Time, every time.Duration) bool {
	current := last.Load()
	if now.UnixNano()-current < int64(every) && current != 0 {
		return false
	}
	return last.CompareAndSwap(current, now.UnixNano())
}
