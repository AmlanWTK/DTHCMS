package main

import (
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/audit"
	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/apispec"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
)

// The route half of the contract test.
//
// api/openapi.yaml is the contract of record, which is a claim worth exactly as much as
// the mechanism that enforces it. This is that mechanism: it walks the router this binary
// assembles and fails when the router and the document disagree in either direction — an
// undocumented route, or a documented route nobody implemented.
//
// Both directions matter, and the second is the one people forget. A path left in the
// document after the endpoint was renamed generates a client method that 404s, and the
// generated client compiles perfectly while doing it.
//
// It lives here rather than in httpx because the full surface only exists once the auth
// endpoints are mounted beside the operational ones, and httpx may not import a module.
// The composition root is the only place the whole surface is assembled, so it is the only
// place the whole surface can honestly be checked.

const specRelativePath = "../../../api/openapi.yaml"

func loadSpec(t *testing.T) apispec.Document {
	t.Helper()
	doc, err := apispec.Load(specRelativePath)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return doc
}

func specOperations(t *testing.T) map[string]bool {
	t.Helper()
	operations, err := loadSpec(t).Operations()
	if err != nil {
		t.Fatalf("%s: %v", specRelativePath, err)
	}
	return operations
}

// contractRouter assembles the surface the binary serves.
//
// The dependencies below the routing layer are absent on purpose: mounting a route does
// not touch a database or a session store, and requiring one here would make the contract
// check something that only runs when infrastructure is up. What is real is the route
// table — every handler is registered by the same code run() uses.
func contractRouter(t *testing.T) *chi.Mux {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	router, err := surface{
		Logger:         logger,
		IDs:            &ids.Sequential{Prefix: "req"},
		AllowedOrigins: []string{"http://localhost:3000"},
		MaxBodyBytes:   1024,
		RequestTimeout: 5 * time.Second,
		Health:         &httpx.Health{Service: "api", Version: "test", Logger: logger},
		Auth:           auth.NewHandlers(auth.HandlersConfig{Logger: logger}),
		Devices:        auth.NewDeviceHandlers(auth.DeviceHandlersConfig{Logger: logger}),
		Admin:          auth.NewAdminHandlers(auth.AdminHandlersConfig{Logger: logger}),
		Audit:          audit.NewHandlers(audit.HandlersConfig{Logger: logger}),
	}.router()
	if err != nil {
		t.Fatalf("the surface does not build: %v", err)
	}
	return router
}

// routerOperations returns the "METHOD /path" set the router actually serves.
func routerOperations(t *testing.T, router chi.Routes) map[string]bool {
	t.Helper()

	operations := map[string]bool{}
	err := chi.Walk(router, func(method, route string, _ http.Handler,
		_ ...func(http.Handler) http.Handler) error {

		// A mounted subrouter with nothing in it leaves a wildcard behind. It is a
		// mount point rather than an endpoint, and documenting it would be a lie.
		route = strings.TrimSuffix(route, "/*")
		if route != "/" {
			route = strings.TrimSuffix(route, "/")
		}
		if route == "" || route == "/" || strings.Contains(route, "*") {
			return nil
		}

		operations[strings.ToUpper(method)+" "+route] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walking the router: %v", err)
	}
	return operations
}

func sorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for item := range set {
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func TestEveryImplementedRouteIsDocumented(t *testing.T) {
	// Acceptance criterion 2. Add an endpoint without documenting it and this fails,
	// which is the entire reason the check exists — a contract nobody enforces is a
	// comment.
	spec := specOperations(t)
	router := routerOperations(t, contractRouter(t))

	var undocumented []string
	for operation := range router {
		if !spec[operation] {
			undocumented = append(undocumented, operation)
		}
	}
	sort.Strings(undocumented)

	if len(undocumented) > 0 {
		t.Errorf("these routes are served but absent from api/openapi.yaml:\n  %s\n\n"+
			"Add them to the contract. Three surfaces consume this API; an endpoint that "+
			"exists only in Go is an endpoint no client can be generated for.",
			strings.Join(undocumented, "\n  "))
	}
}

func TestEveryDocumentedRouteIsImplemented(t *testing.T) {
	// The direction people forget. A path left in the document after the endpoint was
	// renamed generates a client method that 404s — and the generated client compiles
	// perfectly while doing it.
	spec := specOperations(t)
	router := routerOperations(t, contractRouter(t))

	var unimplemented []string
	for operation := range spec {
		if !router[operation] {
			unimplemented = append(unimplemented, operation)
		}
	}
	sort.Strings(unimplemented)

	if len(unimplemented) > 0 {
		t.Errorf("these routes are documented but not served:\n  %s\n\n"+
			"Either implement them or remove them from api/openapi.yaml. Documented routes "+
			"that do not exist are generated into every client.",
			strings.Join(unimplemented, "\n  "))
	}
}

func TestTheServedRoutesAreTheOnesWeExpect(t *testing.T) {
	// A guard on the two tests above rather than a duplicate of them: if the walk or the
	// scanner silently returned nothing, both would pass by agreeing about an empty set.
	//
	// It is written out by hand deliberately. Adding an endpoint should require saying so
	// here, in a list a reviewer can read in one glance and ask "should that exist?" — the
	// question the other two tests cannot ask, because they only check that two machines
	// agree with each other.
	want := []string{
		"GET /healthz",
		"GET /readyz",
		"GET /v1/admin/roles",
		"GET /v1/admin/users",
		"GET /v1/admin/users/{id}",
		"GET /v1/audit/alerts",
		"GET /v1/audit/break-glass",
		"GET /v1/audit/break-glass/mine",
		"GET /v1/audit/chain",
		"GET /v1/audit/events",
		"GET /v1/audit/export",
		"GET /v1/audit/kinds",
		"GET /v1/audit/signing-key",
		"GET /v1/auth/me",
		"GET /v1/auth/second-factor",
		"GET /v1/auth/sessions",
		"GET /v1/devices",
		"GET /v1/devices/self",
		"GET /v1/devices/{id}",
		"GET /v1/devices/{id}/events",
		"GET /version",
		"POST /v1/admin/users",
		"POST /v1/admin/users/{id}/password",
		"POST /v1/admin/users/{id}/roles",
		"POST /v1/admin/users/{id}/roles/{role}/revoke",
		"POST /v1/admin/users/{id}/second-factor/reset",
		"POST /v1/admin/users/{id}/sessions/end",
		"POST /v1/admin/users/{id}/status",
		"POST /v1/audit/alerts/{id}/acknowledge",
		"POST /v1/audit/break-glass",
		"POST /v1/audit/break-glass/{id}/acknowledge",
		"POST /v1/audit/break-glass/{id}/end",
		"POST /v1/auth/device/enrol",
		"POST /v1/auth/login",
		"POST /v1/auth/login/second-factor",
		"POST /v1/auth/logout",
		"POST /v1/auth/logout-all",
		"POST /v1/auth/refresh",
		"POST /v1/auth/second-factor/confirm",
		"POST /v1/auth/second-factor/disable",
		"POST /v1/auth/second-factor/enrol",
		"POST /v1/auth/second-factor/recovery-codes",
		"POST /v1/auth/step-up",
		"POST /v1/devices",
		"POST /v1/devices/self/rotate-key",
		"POST /v1/devices/{id}/enrolments",
		"POST /v1/devices/{id}/lost",
		"POST /v1/devices/{id}/reinstate",
		"POST /v1/devices/{id}/revoke",
		"POST /v1/devices/{id}/suspend",
	}

	if got := sorted(routerOperations(t, contractRouter(t))); !reflect.DeepEqual(got, want) {
		t.Errorf("router serves %v, want %v", got, want)
	}
	if got := sorted(specOperations(t)); !reflect.DeepEqual(got, want) {
		t.Errorf("contract declares %v, want %v", got, want)
	}
}

// TestEveryRouteDeclaresItsRequirement is the route-registry audit (CP20, criterion 4).
//
// NewRouter already refuses to build a surface with an undeclared route; this test is the
// reviewer's copy of the table — which routes are public, which need only a session, and
// which permission guards each of the rest — written out by hand so that adding a route
// means saying here what it takes to reach it, in a diff somebody reads.
func TestEveryRouteDeclaresItsRequirement(t *testing.T) {
	decls, err := httpx.Declarations(contractRouter(t))
	if err != nil {
		t.Fatal(err)
	}

	public := "public"
	session := "session"
	want := map[string]string{
		"GET /healthz": public,
		"GET /readyz":  public,
		"GET /version": public,

		"POST /v1/auth/login":               public,
		"POST /v1/auth/login/second-factor": public,
		"POST /v1/auth/refresh":             public,
		"POST /v1/auth/device/enrol":        public,

		"GET /v1/auth/me":                            session,
		"GET /v1/auth/sessions":                      session,
		"POST /v1/auth/logout":                       session,
		"POST /v1/auth/logout-all":                   session,
		"GET /v1/auth/second-factor":                 session,
		"POST /v1/auth/second-factor/enrol":          session,
		"POST /v1/auth/second-factor/confirm":        session,
		"POST /v1/auth/step-up":                      session,
		"POST /v1/auth/second-factor/disable":        session, // plus a step-up
		"POST /v1/auth/second-factor/recovery-codes": session, // plus a step-up
		"GET /v1/devices/self":                       session, // plus a verified device
		"POST /v1/devices/self/rotate-key":           session, // plus a verified device

		"GET /v1/admin/roles":                           "user.read",
		"GET /v1/admin/users":                           "user.read",
		"GET /v1/admin/users/{id}":                      "user.read",
		"POST /v1/admin/users":                          "user.invite", // plus a step-up
		"POST /v1/admin/users/{id}/status":              "user.invite|user.suspend|user.deactivate",
		"POST /v1/admin/users/{id}/roles":               "role.grant",  // plus a step-up
		"POST /v1/admin/users/{id}/roles/{role}/revoke": "role.revoke", // plus a step-up
		"POST /v1/admin/users/{id}/sessions/end":        "user.credential.reset",
		"POST /v1/admin/users/{id}/password":            "user.credential.reset",
		"POST /v1/admin/users/{id}/second-factor/reset": "user.credential.reset",

		"GET /v1/audit/events":                        "audit.read",
		"GET /v1/audit/kinds":                         session,
		"GET /v1/audit/chain":                         "audit.read",
		"GET /v1/audit/export":                        "audit.read",
		"GET /v1/audit/signing-key":                   session,
		"GET /v1/audit/alerts":                        "audit.read",
		"POST /v1/audit/alerts/{id}/acknowledge":      "audit.read",
		"POST /v1/audit/break-glass":                  "patient.read.clinical|patient.read.demographics", // plus a step-up
		"GET /v1/audit/break-glass":                   "audit.read",
		"GET /v1/audit/break-glass/mine":              session,
		"POST /v1/audit/break-glass/{id}/end":         session, // one's own, or audit.read
		"POST /v1/audit/break-glass/{id}/acknowledge": "audit.read",

		"GET /v1/devices":                  "device.enroll|device.revoke|audit.read",
		"GET /v1/devices/{id}":             "device.enroll|device.revoke|audit.read",
		"GET /v1/devices/{id}/events":      "device.enroll|device.revoke|audit.read",
		"POST /v1/devices":                 "device.enroll",
		"POST /v1/devices/{id}/enrolments": "device.enroll",
		"POST /v1/devices/{id}/suspend":    "device.revoke",
		"POST /v1/devices/{id}/reinstate":  "device.revoke",
		"POST /v1/devices/{id}/revoke":     "device.revoke",
		"POST /v1/devices/{id}/lost":       "device.revoke",
	}

	got := map[string]string{}
	for route, req := range decls {
		switch {
		case req.IsPublic():
			got[route] = public
		case len(req.Permissions()) == 0:
			got[route] = session
		default:
			got[route] = strings.Join(req.Permissions(), "|")
		}
	}
	if !reflect.DeepEqual(got, want) {
		for route, g := range got {
			if w, ok := want[route]; !ok || w != g {
				t.Errorf("%s: declared %q, table says %q", route, g, w)
			}
		}
		for route := range want {
			if _, ok := got[route]; !ok {
				t.Errorf("%s: in the table but not served", route)
			}
		}
	}
}
