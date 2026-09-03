package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The two halves of the browser transport, in isolation: how a token is read, and what
// the forgery guard refuses.

type stubAuthenticator struct{ accept string }

func (s stubAuthenticator) Identify(_ context.Context, token string) (Caller, error) {
	if token == s.accept {
		return Caller{UserID: "u1", SessionID: "s1", Code: "E001"}, nil
	}
	return Caller{}, errors.New("no")
}

func authed(t *testing.T) http.Handler {
	t.Helper()
	echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := CallerFrom(r.Context()); !ok {
			t.Error("handler ran without a caller on the context")
		}
		w.WriteHeader(http.StatusOK)
	})
	return Authenticate(testLogger(), stubAuthenticator{accept: "good"})(echo)
}

func TestAuthenticateAcceptsTheBearerHeader(t *testing.T) {
	req := httptest.NewRequest("GET", "/v1/x", nil)
	req.Header.Set("Authorization", "Bearer good")
	rec := httptest.NewRecorder()
	authed(t).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("bearer: %d", rec.Code)
	}
}

func TestAuthenticateAcceptsTheSessionCookie(t *testing.T) {
	// ADR-0010: the browser holds nothing; the token arrives in an httpOnly cookie.
	req := httptest.NewRequest("GET", "/v1/x", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: "good"})
	rec := httptest.NewRecorder()
	authed(t).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("cookie: %d", rec.Code)
	}
}

func TestAuthenticateRefusesWithNeitherOrWithABadOne(t *testing.T) {
	for name, set := range map[string]func(*http.Request){
		"nothing":           func(*http.Request) {},
		"bad header":        func(r *http.Request) { r.Header.Set("Authorization", "Bearer bad") },
		"bad cookie":        func(r *http.Request) { r.AddCookie(&http.Cookie{Name: SessionCookie, Value: "bad"}) },
		"empty cookie":      func(r *http.Request) { r.AddCookie(&http.Cookie{Name: SessionCookie, Value: ""}) },
		"basic, not bearer": func(r *http.Request) { r.Header.Set("Authorization", "Basic Z29vZA==") },
	} {
		req := httptest.NewRequest("GET", "/v1/x", nil)
		set(req)
		rec := httptest.NewRecorder()
		Authenticate(testLogger(), stubAuthenticator{accept: "good"})(
			http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Errorf("%s: handler ran", name) }),
		).ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d, want 401", name, rec.Code)
		}
	}
}

func TestTheHeaderWinsOverTheCookie(t *testing.T) {
	// A client that sends an explicit header has made a choice; a stale cookie beside it
	// must not quietly override that choice.
	req := httptest.NewRequest("GET", "/v1/x", nil)
	req.Header.Set("Authorization", "Bearer bad")
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: "good"})
	rec := httptest.NewRecorder()
	Authenticate(testLogger(), stubAuthenticator{accept: "good"})(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("handler ran") }),
	).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("bad header + good cookie: %d, want 401", rec.Code)
	}
}

func TestRequireRequestedWithGuardsOnlyStateChanges(t *testing.T) {
	ran := 0
	guard := RequireRequestedWith(testLogger())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		ran++
		w.WriteHeader(http.StatusOK)
	}))

	cases := []struct {
		method string
		header bool
		want   int
	}{
		{"GET", false, http.StatusOK},
		{"HEAD", false, http.StatusOK},
		{"OPTIONS", false, http.StatusOK},
		{"POST", false, http.StatusForbidden},
		{"PUT", false, http.StatusForbidden},
		{"PATCH", false, http.StatusForbidden},
		{"DELETE", false, http.StatusForbidden},
		{"POST", true, http.StatusOK},
		{"DELETE", true, http.StatusOK},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, "/v1/x", nil)
		if c.header {
			req.Header.Set(RequestedWithHeader, RequestedWithValue)
		}
		rec := httptest.NewRecorder()
		guard.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s header=%v: %d, want %d", c.method, c.header, rec.Code, c.want)
		}
	}

	// The wrong value is no value.
	req := httptest.NewRequest("POST", "/v1/x", nil)
	req.Header.Set(RequestedWithHeader, "XMLHttpRequest")
	rec := httptest.NewRecorder()
	guard.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("wrong guard value: %d, want 403", rec.Code)
	}
}

func TestCORSPreflightAdmitsTheGuardHeader(t *testing.T) {
	// The guard only works if a legitimate cross-origin client (the web app on its own
	// port in development) is allowed to send it. If preflight refused it, every login
	// from localhost:3000 would fail and the guard would look like the bug.
	req := httptest.NewRequest("OPTIONS", "/v1/auth/login", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()
	CORS([]string{"http://localhost:3000"})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Headers"); !containsHeader(got, RequestedWithHeader) {
		t.Errorf("Access-Control-Allow-Headers = %q, missing %s", got, RequestedWithHeader)
	}
}

func containsHeader(list, name string) bool {
	for _, h := range strings.Split(list, ",") {
		if http.CanonicalHeaderKey(strings.TrimSpace(h)) == http.CanonicalHeaderKey(name) {
			return true
		}
	}
	return false
}
