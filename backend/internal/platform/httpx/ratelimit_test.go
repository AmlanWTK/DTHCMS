package httpx

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
)

// CP65's security line: per-device rate limits.
//
// These tests are about the middleware's decisions — who is charged, what happens when the
// counter is unreachable, what a refusal says. The algorithm itself is tested against a real
// Redis in internal/platform/cache, because a limiter that is only ever tested against a fake is
// a limiter whose atomicity nobody has checked.

// countingLimiter answers from a script and records what it was asked.
type countingLimiter struct {
	allow []bool // consumed in order; exhausted means allow
	calls []string
	rules []Rule
	err   error
	n     int
}

func (c *countingLimiter) Take(_ context.Context, key string, rule Rule, _ time.Time) (Decision, error) {
	c.calls = append(c.calls, key)
	c.rules = append(c.rules, rule)
	if c.err != nil {
		return Decision{}, c.err
	}
	allowed := true
	if c.n < len(c.allow) {
		allowed = c.allow[c.n]
	}
	c.n++
	if allowed {
		return Decision{Allowed: true}, nil
	}
	return Decision{Allowed: false, RetryAfter: 2500 * time.Millisecond}, nil
}

func limitedHandler(cfg RateLimitConfig) (http.Handler, *atomic.Int64) {
	var served atomic.Int64
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	return RateLimit(cfg)(inner), &served
}

func limitedRequest(ctx context.Context, method, path string) *http.Request {
	return httptest.NewRequest(method, path, nil).WithContext(ctx)
}

func withDevice(id string) context.Context {
	return context.WithValue(context.Background(), deviceKey{}, DeviceIdentity{DeviceID: id})
}

func withUser(id string) context.Context {
	return context.WithValue(context.Background(), callerKey{}, Caller{UserID: id})
}

func TestARouteWithNoRuleIsNotCounted(t *testing.T) {
	limiter := &countingLimiter{}
	handler, served := limitedHandler(RateLimitConfig{
		Logger: testLogger(), Limiter: limiter,
		Rules: map[string]Rule{"POST /v1/sync/events": {Burst: 1, Every: time.Minute}},
	})

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, limitedRequest(withDevice("tablet-1"), "GET", "/v1/patients"))

	if len(limiter.calls) != 0 {
		t.Fatalf("an unlimited route consulted the limiter: %v", limiter.calls)
	}
	if served.Load() != 1 {
		t.Fatalf("the request did not reach the handler")
	}
}

func TestTheDeviceIsChargedRatherThanThePerson(t *testing.T) {
	limiter := &countingLimiter{}
	handler, _ := limitedHandler(RateLimitConfig{
		Logger: testLogger(), Limiter: limiter,
		Rules: map[string]Rule{"POST /v1/sync/events": {Burst: 3, Every: 5 * time.Second}},
	})

	// The same tablet, two different clinicians signed in on it.
	ctx := context.WithValue(withDevice("tablet-1"), callerKey{}, Caller{UserID: "nurse-a"})
	handler.ServeHTTP(httptest.NewRecorder(), limitedRequest(ctx, "POST", "/v1/sync/events"))
	ctx = context.WithValue(withDevice("tablet-1"), callerKey{}, Caller{UserID: "nurse-b"})
	handler.ServeHTTP(httptest.NewRecorder(), limitedRequest(ctx, "POST", "/v1/sync/events"))

	if len(limiter.calls) != 2 {
		t.Fatalf("expected two calls, got %v", limiter.calls)
	}
	if limiter.calls[0] != limiter.calls[1] {
		t.Fatalf("one tablet spent two budgets: %q then %q — a stolen device would get a fresh "+
			"allowance for every clinician who has ever signed in on it", limiter.calls[0], limiter.calls[1])
	}
	if !strings.Contains(limiter.calls[0], "device:tablet-1") {
		t.Fatalf("the key does not name the device: %q", limiter.calls[0])
	}
	if limiter.rules[0] != (Rule{Burst: 3, Every: 5 * time.Second}) {
		t.Fatalf("the route's rule was not passed through: %+v", limiter.rules[0])
	}
}

func TestABrowserIsChargedToItsUser(t *testing.T) {
	limiter := &countingLimiter{}
	handler, _ := limitedHandler(RateLimitConfig{
		Logger: testLogger(), Limiter: limiter,
		Rules: map[string]Rule{"GET /v1/sync/events": {Burst: 3, Every: time.Second}},
	})

	handler.ServeHTTP(httptest.NewRecorder(), limitedRequest(withUser("doctor-1"), "GET", "/v1/sync/events"))

	if len(limiter.calls) != 1 || !strings.Contains(limiter.calls[0], "user:doctor-1") {
		t.Fatalf("a request with no device was not charged to its user: %v", limiter.calls)
	}
}

func TestTwoDevicesDoNotShareABudget(t *testing.T) {
	limiter := &countingLimiter{}
	handler, _ := limitedHandler(RateLimitConfig{
		Logger: testLogger(), Limiter: limiter,
		Rules: map[string]Rule{"POST /v1/sync/events": {Burst: 1, Every: time.Minute}},
	})

	handler.ServeHTTP(httptest.NewRecorder(), limitedRequest(withDevice("tablet-1"), "POST", "/v1/sync/events"))
	handler.ServeHTTP(httptest.NewRecorder(), limitedRequest(withDevice("tablet-2"), "POST", "/v1/sync/events"))

	if limiter.calls[0] == limiter.calls[1] {
		t.Fatalf("two devices shared one bucket (%q): the busiest station would rate-limit the others",
			limiter.calls[0])
	}
}

func TestTheSameDeviceOnTwoRoutesDoesNotShareABudget(t *testing.T) {
	limiter := &countingLimiter{}
	handler, _ := limitedHandler(RateLimitConfig{
		Logger: testLogger(), Limiter: limiter,
		Rules: map[string]Rule{
			"POST /v1/sync/events": {Burst: 1, Every: time.Minute},
			"GET /v1/sync/events":  {Burst: 1, Every: time.Minute},
		},
	})

	handler.ServeHTTP(httptest.NewRecorder(), limitedRequest(withDevice("tablet-1"), "POST", "/v1/sync/events"))
	handler.ServeHTTP(httptest.NewRecorder(), limitedRequest(withDevice("tablet-1"), "GET", "/v1/sync/events"))

	if limiter.calls[0] == limiter.calls[1] {
		t.Fatalf("the push and the pull shared a bucket (%q): draining a backlog would stop a "+
			"tablet from catching up on what it missed", limiter.calls[0])
	}
}

func TestARefusalIs429WithARetryAfterAndNoHandler(t *testing.T) {
	limiter := &countingLimiter{allow: []bool{false}}
	handler, served := limitedHandler(RateLimitConfig{
		Logger: testLogger(), Limiter: limiter,
		Rules: map[string]Rule{"POST /v1/sync/events": {Burst: 1, Every: time.Minute}},
	})

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, limitedRequest(withDevice("tablet-1"), "POST", "/v1/sync/events"))

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got != "3" {
		t.Fatalf("Retry-After = %q, want \"3\" — 2.5 seconds rounded up", got)
	}
	if served.Load() != 0 {
		t.Fatal("the handler ran anyway, which is the whole thing a limiter must not do")
	}
	if !strings.Contains(w.Body.String(), "RATE_LIMITED") {
		t.Fatalf("the body does not carry the error code: %s", w.Body.String())
	}
}

func TestRetryAfterIsNeverZero(t *testing.T) {
	// A limiter that says "wait 200ms" must not produce Retry-After: 0, which reads as
	// "immediately" and sends a well-behaved client straight back into the refusal.
	limiter := &shortWaitLimiter{wait: 200 * time.Millisecond}
	handler, _ := limitedHandler(RateLimitConfig{
		Logger: testLogger(), Limiter: limiter,
		Rules: map[string]Rule{"POST /v1/sync/events": {Burst: 1, Every: time.Second}},
	})

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, limitedRequest(withDevice("tablet-1"), "POST", "/v1/sync/events"))

	if got := w.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want \"1\"", got)
	}
}

type shortWaitLimiter struct{ wait time.Duration }

func (s *shortWaitLimiter) Take(context.Context, string, Rule, time.Time) (Decision, error) {
	return Decision{Allowed: false, RetryAfter: s.wait}, nil
}

func TestAnUnreachableLimiterLetsTheClinicWork(t *testing.T) {
	limiter := &countingLimiter{err: errors.New("redis: connection refused")}
	handler, served := limitedHandler(RateLimitConfig{
		Logger: testLogger(), Limiter: limiter,
		Rules: map[string]Rule{"POST /v1/sync/events": {Burst: 1, Every: time.Minute}},
	})

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, limitedRequest(withDevice("tablet-1"), "POST", "/v1/sync/events"))

	if w.Code != http.StatusOK || served.Load() != 1 {
		t.Fatalf("status = %d, served = %d: a clinic must not stop taking blood pressures "+
			"because Redis restarted. What makes that acceptable is the database cap, not a "+
			"second guess here", w.Code, served.Load())
	}
}

func TestANilLimiterLeavesEveryRouteUnlimited(t *testing.T) {
	handler, served := limitedHandler(RateLimitConfig{
		Logger: testLogger(),
		Rules:  map[string]Rule{"POST /v1/sync/events": {Burst: 1, Every: time.Minute}},
	})

	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, limitedRequest(withDevice("tablet-1"), "POST", "/v1/sync/events"))
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i, w.Code)
		}
	}
	if served.Load() != 3 {
		t.Fatalf("served = %d, want 3", served.Load())
	}
}

func TestTheOutageWarningIsCoalesced(t *testing.T) {
	// A Redis outage logs once a minute, not once a request. The minute somebody most needs to
	// read the clinical log is the minute this would otherwise bury it.
	var lines int
	logger := slog.New(countingHandler{count: &lines})
	clk := clock.NewFixed(time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC))

	handler, _ := limitedHandler(RateLimitConfig{
		Logger: logger, Limiter: &countingLimiter{err: errors.New("down")}, Clock: clk,
		Rules: map[string]Rule{"POST /v1/sync/events": {Burst: 1, Every: time.Minute}},
	})

	for i := 0; i < 50; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), limitedRequest(withDevice("tablet-1"), "POST", "/v1/sync/events"))
	}
	if lines != 1 {
		t.Fatalf("fifty failed requests wrote %d warnings, want 1", lines)
	}

	clk.Advance(61 * time.Second)
	handler.ServeHTTP(httptest.NewRecorder(), limitedRequest(withDevice("tablet-1"), "POST", "/v1/sync/events"))
	if lines != 2 {
		t.Fatalf("the warning did not come back after a minute: %d lines", lines)
	}
}

type countingHandler struct{ count *int }

func (c countingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (c countingHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Level == slog.LevelWarn {
		*c.count++
	}
	return nil
}
func (c countingHandler) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c countingHandler) WithGroup(string) slog.Handler      { return c }

func TestARequestWithNobodyToChargeIsNotSilentlyUnlimited(t *testing.T) {
	// Unreachable on the authenticated chain. If the chain is ever rewired so that it happens,
	// the limiter is not running, and that must be loud rather than invisible.
	var errorLines int
	logger := slog.New(errorCountingHandler{count: &errorLines})
	handler, served := limitedHandler(RateLimitConfig{
		Logger: logger, Limiter: &countingLimiter{},
		Rules: map[string]Rule{"POST /v1/sync/events": {Burst: 1, Every: time.Minute}},
	})

	handler.ServeHTTP(httptest.NewRecorder(),
		limitedRequest(context.Background(), "POST", "/v1/sync/events"))

	if errorLines != 1 {
		t.Fatalf("a request with no identity wrote %d error lines, want 1", errorLines)
	}
	if served.Load() != 1 {
		t.Fatal("it should still be served — the route is already authenticated by this point")
	}
}

type errorCountingHandler struct{ count *int }

func (c errorCountingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (c errorCountingHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Level == slog.LevelError {
		*c.count++
	}
	return nil
}
func (c errorCountingHandler) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c errorCountingHandler) WithGroup(string) slog.Handler      { return c }
