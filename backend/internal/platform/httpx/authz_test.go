package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
)

// The route guard (CP20): deny by default, enforced at boot and at the door.

type fakeAuthenticator struct{ caller Caller }

func (f fakeAuthenticator) Identify(context.Context, string) (Caller, error) {
	return f.caller, nil
}

type fakeAuthorizer struct {
	allow    bool
	askedFor []string
}

func (f *fakeAuthorizer) Authorize(ctx context.Context, _ Caller, anyOf []string) (context.Context, AuthzDecision) {
	f.askedFor = anyOf
	if f.allow {
		return context.WithValue(ctx, "subject", "resolved"), AuthzDecision{Allowed: true, Reason: "allowed"} //nolint:staticcheck // a test key
	}
	return ctx, AuthzDecision{Reason: "permission_not_held", Rule: "", Detail: "not held"}
}

func ok(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "subject": r.Context().Value("subject")}) //nolint:staticcheck // a test key
}

func buildRouter(t *testing.T, authorizer Authorizer, caller *Caller, routes func(chi.Router)) http.Handler {
	t.Helper()
	opts := RouterOptions{
		Logger: testLogger(), IDs: &ids.Sequential{Prefix: "req"}, MaxBodyBytes: 1024,
		RequestTimeout: time.Second, Health: &Health{Logger: testLogger()},
		Routes: routes, Authorizer: authorizer,
	}
	if caller != nil {
		opts.Authenticator = fakeAuthenticator{caller: *caller}
	}
	router, err := NewRouter(opts)
	if err != nil {
		t.Fatalf("building the router: %v", err)
	}
	return router
}

func get(router http.Handler, path string, authenticated bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if authenticated {
		req.Header.Set("Authorization", "Bearer t")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestAnUndeclaredRouteRefusesToBoot(t *testing.T) {
	// Acceptance criterion 1: a route with no declared requirement is a service that does
	// not start.
	_, err := NewRouter(RouterOptions{
		Logger: testLogger(), IDs: &ids.Sequential{}, MaxBodyBytes: 1024, RequestTimeout: time.Second,
		Routes: func(r chi.Router) {
			r.Get("/patients", ok)                                   // forgot to declare
			r.Method("GET", "/devices", Declare(Session(), ok))      // fine
			r.Route("/labs", func(l chi.Router) { l.Post("/", ok) }) // forgot, nested
		},
	})
	if !errors.Is(err, ErrUndeclaredRoutes) {
		t.Fatalf("want ErrUndeclaredRoutes, got %v", err)
	}
	for _, want := range []string{"GET /v1/patients", "POST /v1/labs"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error must name %s:\n%s", want, err)
		}
	}
	if strings.Contains(err.Error(), "/v1/devices") {
		t.Errorf("a declared route must not be reported:\n%s", err)
	}
}

func TestDeclaredRoutesBoot(t *testing.T) {
	router := buildRouter(t, nil, nil, func(r chi.Router) {
		r.Method("GET", "/open", Declare(Public(), ok))
	})
	if rec := get(router, "/healthz", false); rec.Code != http.StatusOK {
		t.Fatalf("health must stay public: %d", rec.Code)
	}
}

func TestSessionRequirementNeedsACaller(t *testing.T) {
	caller := Caller{UserID: "u1", FacilityID: "f1", SessionID: "s1"}
	router := buildRouter(t, &fakeAuthorizer{allow: true}, &caller, func(r chi.Router) {
		r.Method("GET", "/me", Declare(Session(), ok))
	})
	if rec := get(router, "/v1/me", false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d %s", rec.Code, rec.Body)
	}
	if rec := get(router, "/v1/me", true); rec.Code != http.StatusOK {
		t.Fatalf("with a session: %d %s", rec.Code, rec.Body)
	}
}

func TestPermissionRequirementAsksTheEngine(t *testing.T) {
	caller := Caller{UserID: "u1", FacilityID: "f1", SessionID: "s1"}
	engine := &fakeAuthorizer{allow: true}
	router := buildRouter(t, engine, &caller, func(r chi.Router) {
		r.Method("GET", "/devices", Declare(Permission("device.enroll", "audit.read"), ok))
	})

	rec := get(router, "/v1/devices", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("allowed: %d %s", rec.Code, rec.Body)
	}
	if len(engine.askedFor) != 2 || engine.askedFor[0] != "device.enroll" {
		t.Fatalf("the engine was asked for %v", engine.askedFor)
	}
	// What the engine put on the context reached the handler.
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["subject"] != "resolved" {
		t.Fatalf("the engine's context did not reach the handler: %s", rec.Body)
	}

	engine.allow = false
	rec = get(router, "/v1/devices", true)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("denied: %d %s", rec.Code, rec.Body)
	}
	// Acceptance criterion 3: the refusal says nothing about the resource, the rule or
	// the permission — the same words for every 403.
	if strings.Contains(rec.Body.String(), "device") || strings.Contains(rec.Body.String(), "not held") {
		t.Fatalf("the 403 leaks: %s", rec.Body)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"].(map[string]any)["code"] != "FORBIDDEN" {
		t.Fatalf("code: %s", rec.Body)
	}
}

func TestNoEngineFailsClosed(t *testing.T) {
	caller := Caller{UserID: "u1", FacilityID: "f1", SessionID: "s1", Permissions: []string{"device.enroll"}}
	router := buildRouter(t, nil, &caller, func(r chi.Router) {
		r.Method("GET", "/devices", Declare(Permission("device.enroll"), ok))
		r.Method("GET", "/me", Declare(Session(), ok))
	})
	if rec := get(router, "/v1/devices", true); rec.Code != http.StatusForbidden {
		t.Fatalf("a permission route with no engine must refuse: %d %s", rec.Code, rec.Body)
	}
	if rec := get(router, "/v1/me", true); rec.Code != http.StatusOK {
		t.Fatalf("a session route needs no engine: %d", rec.Code)
	}
}

func TestPublicRoutesNeedNothing(t *testing.T) {
	router := buildRouter(t, nil, nil, func(r chi.Router) {
		r.Method("GET", "/open", Declare(Public(), ok))
	})
	if rec := get(router, "/v1/open", false); rec.Code != http.StatusOK {
		t.Fatalf("public: %d %s", rec.Code, rec.Body)
	}
}

func TestDeclarationsAreReadable(t *testing.T) {
	router := buildRouter(t, nil, nil, func(r chi.Router) {
		r.Method("GET", "/a", Declare(Public(), ok))
		r.Method("POST", "/b", Declare(Permission("x.y"), ok))
	})
	decls, err := Declarations(router.(chi.Routes))
	if err != nil {
		t.Fatal(err)
	}
	if !decls["GET /v1/a"].IsPublic() || decls["POST /v1/b"].Permissions()[0] != "x.y" || !decls["GET /healthz"].IsPublic() {
		t.Fatalf("declarations = %+v", decls)
	}
}
