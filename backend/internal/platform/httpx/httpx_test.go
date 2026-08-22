package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func testRouter(t *testing.T, health *Health) http.Handler {
	t.Helper()
	return NewRouter(RouterOptions{
		Logger:         testLogger(),
		IDs:            &ids.Sequential{Prefix: "req"},
		AllowedOrigins: []string{"http://localhost:3000"},
		MaxBodyBytes:   1024,
		RequestTimeout: 5 * time.Second,
		Health:         health,
	})
}

// --- health ---

func TestLivenessIgnoresDependencies(t *testing.T) {
	// A failing database must not make the process look dead: an orchestrator would
	// kill and restart a perfectly healthy service, repeatedly, during an outage.
	health := &Health{
		Service: "api",
		Version: "test",
		Logger:  testLogger(),
		Dependencies: []Dependency{
			{Name: "postgres", Critical: true, Check: func(context.Context) error {
				return errors.New("connection refused")
			}},
		},
	}

	rec := httptest.NewRecorder()
	testRouter(t, health).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("/healthz = %d, want 200 even with a dead dependency", rec.Code)
	}
}

func TestReadinessReflectsDependencyHealth(t *testing.T) {
	redisUp := true

	health := &Health{
		Service: "api",
		Version: "test",
		Logger:  testLogger(),
		Dependencies: []Dependency{
			{Name: "postgres", Critical: true, Check: func(context.Context) error { return nil }},
			{Name: "redis", Critical: true, Check: func(context.Context) error {
				if redisUp {
					return nil
				}
				return errors.New("dial tcp 127.0.0.1:6379: connect: connection refused")
			}},
		},
	}
	router := testRouter(t, health)

	get := func() (int, map[string]any) {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("readyz body is not JSON: %v", err)
		}
		return rec.Code, body
	}

	code, body := get()
	if code != http.StatusOK {
		t.Fatalf("with everything healthy /readyz = %d, want 200", code)
	}

	// Stop Redis.
	redisUp = false

	code, body = get()
	if code != http.StatusServiceUnavailable {
		t.Errorf("with Redis down /readyz = %d, want 503", code)
	}

	checks, _ := body["checks"].(map[string]any)
	if checks["redis"] != "unavailable" {
		t.Errorf("redis check = %v, want unavailable", checks["redis"])
	}
	if checks["postgres"] != "ok" {
		t.Errorf("postgres check = %v, want ok", checks["postgres"])
	}
}

func TestReadinessDoesNotLeakConnectionDetail(t *testing.T) {
	// A connection error can contain a host, a user name, sometimes a password.
	health := &Health{
		Service: "api",
		Logger:  testLogger(),
		Dependencies: []Dependency{
			{Name: "postgres", Critical: true, Check: func(context.Context) error {
				return errors.New("dial postgres://dthcms:sup3rs3cret@db.internal:5432 refused")
			}},
		},
	}

	rec := httptest.NewRecorder()
	testRouter(t, health).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if strings.Contains(rec.Body.String(), "sup3rs3cret") || strings.Contains(rec.Body.String(), "db.internal") {
		t.Errorf("readiness response leaked connection detail: %s", rec.Body.String())
	}
}

func TestNonCriticalDependencyDoesNotBlockReadiness(t *testing.T) {
	health := &Health{
		Service: "api",
		Logger:  testLogger(),
		Dependencies: []Dependency{
			{Name: "postgres", Critical: true, Check: func(context.Context) error { return nil }},
			{Name: "blobstore", Critical: false, Check: func(context.Context) error {
				return errors.New("not configured")
			}},
		},
	}

	rec := httptest.NewRecorder()
	testRouter(t, health).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("/readyz = %d; a non-critical dependency must not make the service unready", rec.Code)
	}
}

// --- middleware ---

func TestCorrelationIDIsGeneratedAndReturned(t *testing.T) {
	rec := httptest.NewRecorder()
	testRouter(t, &Health{Logger: testLogger()}).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Header().Get(RequestIDHeader) == "" {
		t.Error("every response must carry a request ID a clinic operator can quote")
	}
}

func TestCorrelationIDFromClientIsHonoured(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set(RequestIDHeader, "req_from_mobile_app")

	rec := httptest.NewRecorder()
	testRouter(t, &Health{Logger: testLogger()}).ServeHTTP(rec, req)

	if got := rec.Header().Get(RequestIDHeader); got != "req_from_mobile_app" {
		t.Errorf("request ID = %q; a client-supplied ID must be preserved so one "+
			"interaction can be traced across the phone and the server", got)
	}
}

func TestPanicIsRecoveredAndReturnsEnvelope(t *testing.T) {
	logger := testLogger()
	router := NewRouter(RouterOptions{
		Logger: logger, IDs: &ids.Sequential{}, MaxBodyBytes: 1024,
		RequestTimeout: time.Second, Health: &Health{Logger: logger},
	})
	router.Get("/boom", func(http.ResponseWriter, *http.Request) {
		panic("something went badly wrong with patient pat_123")
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "pat_123") {
		t.Errorf("the panic message reached the client: %s", rec.Body.String())
	}

	var body map[string]map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("panic response is not the standard envelope: %v", err)
	}
	if body["error"]["code"] != "INTERNAL" {
		t.Errorf("code = %v, want INTERNAL", body["error"]["code"])
	}
}

func TestSecurityHeadersArePresent(t *testing.T) {
	rec := httptest.NewRecorder()
	testRouter(t, &Health{Logger: testLogger()}).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/healthz", nil))

	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
		"Cache-Control":          "no-store",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestCORSAllowsOnlyKnownOrigins(t *testing.T) {
	router := testRouter(t, &Health{Logger: testLogger()})

	t.Run("known origin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.Header.Set("Origin", "http://localhost:3000")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
			t.Error("a configured origin should be allowed")
		}
	})

	t.Run("unknown origin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.Header.Set("Origin", "https://attacker.example")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Error("an unlisted origin must never be allowed")
		}
	})
}

func TestBodyLimitIsEnforced(t *testing.T) {
	logger := testLogger()
	router := NewRouter(RouterOptions{
		Logger: logger, IDs: &ids.Sequential{}, MaxBodyBytes: 64,
		RequestTimeout: time.Second, Health: &Health{Logger: logger},
	})
	router.Post("/echo", func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			WriteError(w, r, logger, errs.ErrPayloadTooLarge.WithDetail(err))
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	body := strings.NewReader(strings.Repeat("x", 5000))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/echo", body))

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413 — an unbounded body can exhaust memory", rec.Code)
	}
}

func TestUnknownRouteReturnsEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	testRouter(t, &Health{Logger: testLogger()}).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/v1/does-not-exist", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}

	var body map[string]map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("404 is not the standard envelope: %v", err)
	}
	if body["error"]["code"] != "NOT_FOUND" {
		t.Errorf("code = %v, want NOT_FOUND", body["error"]["code"])
	}
	if body["error"]["message_bn"] == "" {
		t.Error("every user-facing error needs Bangla wording; half the staff work in it")
	}
}

// --- error responses ---

func TestErrorResponseNeverLeaksInternalDetail(t *testing.T) {
	logger := testLogger()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/anything", nil)

	internal := fmt.Errorf("pq: relation \"patients\" does not exist (host db.internal, user dthcms)")
	WriteError(rec, req, logger, errs.ErrInternal.WithDetail(internal))

	body := rec.Body.String()
	for _, secret := range []string{"pq:", "patients", "db.internal", "dthcms"} {
		if strings.Contains(body, secret) {
			t.Errorf("internal detail %q reached the client: %s", secret, body)
		}
	}
}

func TestForbiddenAndNotFoundAreIndistinguishableInShape(t *testing.T) {
	// Whether a resource exists must not be inferable from an authorisation failure.
	logger := testLogger()

	shape := func(e *errs.Error) map[string]any {
		rec := httptest.NewRecorder()
		WriteError(rec, httptest.NewRequest(http.MethodGet, "/x", nil), logger, e)
		var body map[string]map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return body["error"]
	}

	forbidden := shape(errs.ErrForbidden)
	notFound := shape(errs.ErrNotFound)

	if len(forbidden) != len(notFound) {
		t.Errorf("the two responses have different shapes: %v vs %v", forbidden, notFound)
	}
}

func TestDecodeJSONRejectsUnknownFields(t *testing.T) {
	// Silently ignoring a field the client believed it sent is how a clinical value
	// goes missing without anyone noticing.
	var dst struct {
		Height float64 `json:"height_cm"`
	}

	req := httptest.NewRequest(http.MethodPost, "/x",
		strings.NewReader(`{"height_cm": 158, "heigth_cm": 185}`))
	rec := httptest.NewRecorder()

	if err := DecodeJSON(rec, req, &dst); err == nil {
		t.Error("a misspelled field must be rejected, not dropped")
	}
}

// --- lifecycle ---

func TestGracefulShutdownCompletesInFlightRequests(t *testing.T) {
	// A deployment landing mid-save must not lose a station operator's entry.
	var (
		started   = make(chan struct{})
		completed sync.WaitGroup
	)
	completed.Add(1)

	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		close(started)
		time.Sleep(300 * time.Millisecond)
		WriteJSON(w, http.StatusOK, map[string]string{"status": "finished"})
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)

	go func() {
		serveErr <- Serve(ctx, ServerOptions{
			Handler:         mux,
			Logger:          testLogger(),
			Listener:        listener,
			ShutdownTimeout: 5 * time.Second,
		})
	}()

	var (
		status int
		reqErr error
	)
	go func() {
		defer completed.Done()
		resp, err := http.Get("http://" + addr + "/slow")
		if err != nil {
			reqErr = err
			return
		}
		defer func() { _ = resp.Body.Close() }()
		status = resp.StatusCode
	}()

	<-started
	cancel() // shutdown while the request is in flight

	completed.Wait()

	if reqErr != nil {
		t.Fatalf("the in-flight request was cut off by shutdown: %v", reqErr)
	}
	if status != http.StatusOK {
		t.Errorf("in-flight request status = %d, want 200", status)
	}

	select {
	case err := <-serveErr:
		if err != nil {
			t.Errorf("Serve returned an error on clean shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("Serve did not return after shutdown")
	}
}

func TestShutdownRefusesNewRequests(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- Serve(ctx, ServerOptions{
			Handler:         http.NewServeMux(),
			Logger:          testLogger(),
			Listener:        listener,
			ShutdownTimeout: time.Second,
		})
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("Serve: %v", err)
	}

	client := &http.Client{Timeout: time.Second}
	if _, err := client.Get("http://" + addr + "/"); err == nil {
		t.Error("the server still accepts connections after shutdown")
	}
}
