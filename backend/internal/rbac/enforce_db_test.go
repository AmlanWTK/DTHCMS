package rbac_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/auth/pwhash"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/secretbox"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
)

// The engine at the door, end to end: the real router, the real handlers, the real
// database, and the RBAC engine deciding every declared route.
//
//	(3) a 403 does not reveal whether the resource exists;
//	(4) no endpoint bypasses the guard — every permission-guarded route refuses a role
//	    that holds none of its permissions;
//	    and the active-role header narrows a person to the hat they name.

const password = "correct horse battery"

type api struct {
	*httptest.Server
	st     *stack
	router chi.Routes
}

func newAPI(t *testing.T) *api {
	t.Helper()
	st := newStack(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	hasher := pwhash.New(pwhash.Params{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32})
	ring, _ := secretbox.NewRing(secretbox.Key{ID: "test-1", Material: bytes.Repeat([]byte{7}, secretbox.KeySize)})
	secondFactor := auth.NewSecondFactor(auth.SecondFactorConfig{Store: st.store, Users: st.store, Ring: ring, Clock: st.clock})
	sessions := auth.NewSessions(auth.SessionsConfig{Store: st.store, Hasher: hasher, Clock: st.clock, SecondFactor: secondFactor,
		Sleep: func(context.Context, time.Duration) {}})
	handlers := auth.NewHandlers(auth.HandlersConfig{
		Sessions: sessions, Store: st.store, SecondFactor: secondFactor, Logger: logger,
		FacilityID: st.facility, SecureCookies: true,
	})
	devices := auth.NewDevices(auth.DevicesConfig{Store: st.store, Clock: st.clock})
	deviceHandlers := auth.NewDeviceHandlers(auth.DeviceHandlersConfig{Devices: devices, Store: st.store, Logger: logger})

	router, err := httpx.NewRouter(httpx.RouterOptions{
		Logger: logger, IDs: &ids.Sequential{Prefix: "req"}, AllowedOrigins: []string{"http://localhost:3000"},
		MaxBodyBytes: 64 * 1024, RequestTimeout: 10 * time.Second,
		Authenticator:  &auth.Identifier{Sessions: sessions, Store: st.store},
		DeviceVerifier: &auth.DeviceVerifierAdapter{Devices: devices},
		Authorizer:     &rbac.HTTPAuthorizer{Resolver: st.resolver},
		AuthRoutes: func(r chi.Router) {
			handlers.Mount(r)
			deviceHandlers.MountAuth(r)
		},
		Routes: deviceHandlers.Mount,
	})
	if err != nil {
		t.Fatal(err)
	}
	a := &api{Server: httptest.NewServer(router), st: st, router: router}
	t.Cleanup(a.Close)
	return a
}

type reply struct {
	Status int
	Body   map[string]any
	Raw    string
}

func (a *api) call(t *testing.T, method, path string, body any, token, activeRole string) reply {
	t.Helper()
	var payload io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		payload = bytes.NewReader(encoded)
	}
	req, _ := http.NewRequest(method, a.URL+path, payload)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(httpx.RequestedWithHeader, httpx.RequestedWithValue)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if activeRole != "" {
		req.Header.Set(httpx.ActiveRoleHeader, activeRole)
	}
	res, err := a.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	out := reply{Status: res.StatusCode, Raw: string(raw)}
	_ = json.Unmarshal(raw, &out.Body)
	return out
}

func (a *api) signIn(t *testing.T, code string, roles ...auth.RoleCode) string {
	t.Helper()
	id := a.st.user(t, code, roles...)
	hash, _ := pwhash.New(pwhash.Params{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}).Hash(password)
	if _, err := a.st.db.SQL.Exec(`UPDATE core.app_user SET password_hash = $2, password_set_at = now() WHERE id = $1`, id, hash); err != nil {
		t.Fatal(err)
	}
	res := a.call(t, "POST", "/v1/auth/login", map[string]string{"employee_code": code, "password": password, "transport": "bearer"}, "", "")
	if res.Status != http.StatusOK {
		t.Fatalf("login %s: %d %s", code, res.Status, res.Raw)
	}
	return res.Body["access_token"].(string)
}

// correlation ids differ per request; everything else in a refusal must not.
var correlation = regexp.MustCompile(`"correlation_id":"[^"]*"`)

func TestAForbiddenAnswerDoesNotRevealExistence(t *testing.T) {
	a := newAPI(t)
	admin := a.signIn(t, "A001", auth.RoleAdmin)
	nurse := a.signIn(t, "N001", auth.RoleAnthropometry)

	issued := a.call(t, "POST", "/v1/devices", map[string]string{"name": "Ward tablet", "kind": "tablet"}, admin, "")
	if issued.Status != http.StatusCreated {
		t.Fatalf("issue: %d %s", issued.Status, issued.Raw)
	}
	real := issued.Body["device"].(map[string]any)["id"].(string)
	fake := uuid.New().String()

	// Criterion 3: the nurse gets the same 403 for a device that exists and one that does
	// not — and for a malformed id, and for the list.
	answers := map[string]reply{
		"real":      a.call(t, "GET", "/v1/devices/"+real, nil, nurse, ""),
		"fake":      a.call(t, "GET", "/v1/devices/"+fake, nil, nurse, ""),
		"malformed": a.call(t, "GET", "/v1/devices/not-a-uuid", nil, nurse, ""),
		"list":      a.call(t, "GET", "/v1/devices", nil, nurse, ""),
		"revoke":    a.call(t, "POST", "/v1/devices/"+real+"/revoke", map[string]string{"reason": "trying"}, nurse, ""),
	}
	var reference string
	for name, res := range answers {
		if res.Status != http.StatusForbidden {
			t.Fatalf("%s: %d %s", name, res.Status, res.Raw)
		}
		body := correlation.ReplaceAllString(res.Raw, "")
		if reference == "" {
			reference = body
		} else if body != reference {
			t.Fatalf("%s answers differently:\n%s\n%s", name, body, reference)
		}
	}
	// And the administrator can tell the two apart, as they should.
	if res := a.call(t, "GET", "/v1/devices/"+real, nil, admin, ""); res.Status != http.StatusOK {
		t.Fatalf("admin, real: %d", res.Status)
	}
	if res := a.call(t, "GET", "/v1/devices/"+fake, nil, admin, ""); res.Status != http.StatusNotFound {
		t.Fatalf("admin, fake: %d", res.Status)
	}
}

func TestEveryGuardedRouteRefusesARoleWithoutItsPermission(t *testing.T) {
	// Criterion 4: walk the declarations rather than a hand-kept list, so that a route
	// added tomorrow is in this test the day it exists. A researcher holds nothing any
	// device route asks for.
	a := newAPI(t)
	researcher := a.signIn(t, "R001", auth.RoleResearcher)
	decls, err := httpx.Declarations(a.router)
	if err != nil {
		t.Fatal(err)
	}
	guarded := 0
	for route, req := range decls {
		if req.IsPublic() || len(req.Permissions()) == 0 {
			continue
		}
		guarded++
		method, path := splitRoute(route)
		path = regexp.MustCompile(`\{id\}`).ReplaceAllString(path, uuid.New().String())
		var body any
		if method == "POST" {
			body = map[string]string{"name": "x", "kind": "tablet", "reason": "because"}
		}
		res := a.call(t, method, path, body, researcher, "")
		if res.Status != http.StatusForbidden {
			t.Errorf("%s: researcher got %d, want 403: %s", route, res.Status, res.Raw)
		}
	}
	if guarded < 9 {
		t.Fatalf("only %d guarded routes found; the walk is not seeing the device routes", guarded)
	}
}

func TestTheActiveRoleHeaderNarrowsToTheHat(t *testing.T) {
	a := newAPI(t)
	// An administrator who also covers a station [R-02].
	token := a.signIn(t, "A002", auth.RoleAdmin, auth.RoleAnthropometry)

	if res := a.call(t, "GET", "/v1/devices", nil, token, ""); res.Status != http.StatusOK {
		t.Fatalf("no hat, union applies: %d %s", res.Status, res.Raw)
	}
	if res := a.call(t, "GET", "/v1/devices", nil, token, "ADMIN"); res.Status != http.StatusOK {
		t.Fatalf("the admin hat: %d %s", res.Status, res.Raw)
	}
	if res := a.call(t, "GET", "/v1/devices", nil, token, "ANTHROPOMETRY"); res.Status != http.StatusForbidden {
		t.Fatalf("the anthropometry hat must not reach the device list: %d %s", res.Status, res.Raw)
	}
	if res := a.call(t, "GET", "/v1/devices", nil, token, "PHYSICIAN"); res.Status != http.StatusForbidden {
		t.Fatalf("a hat not held: %d %s", res.Status, res.Raw)
	}
}

func splitRoute(route string) (method, path string) {
	for i := 0; i < len(route); i++ {
		if route[i] == ' ' {
			return route[:i], route[i+1:]
		}
	}
	return route, ""
}
