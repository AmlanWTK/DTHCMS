package main

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
)

// The inventory CP83 refuses to round down.
//
// A route whose declared permission reaches, for every role that holds it, less than the
// whole facility cannot be decided at the door: the station or the owner is a fact about a
// resource nothing has looked up. Such a route has exactly two honest states.
//
//   - It declares httpx.PermissionScoped, its handler judges the resource, and dthclint's
//     scopecheck holds it to that.
//   - It declares plain httpx.Permission, and the guard refuses it for exactly the roles
//     whose reach is narrow. Nobody can use it, and nobody can use it unchecked.
//
// There is no third state, and this test is what says so out loud: it prints every route in
// the second state by name on every run, so that "seventy-seven routes are refused" is a
// number somebody reads rather than a number somebody discovers.

// scopeNarrowingRoute is one route with the reaches its permissions grant.
type scopeNarrowingRoute struct {
	key     string
	scoped  bool
	reaches []string
}

func scopeNarrowingRoutes(t *testing.T) []scopeNarrowingRoute {
	t.Helper()
	declarations, err := httpx.Declarations(contractRouter(t))
	if err != nil {
		t.Fatalf("reading the route declarations: %v", err)
	}
	if len(declarations) == 0 {
		t.Fatal("no declared routes; this test is reading the route table wrongly and would " +
			"pass whatever the code did")
	}

	var out []scopeNarrowingRoute
	for key, requirement := range declarations {
		var reaches []string
		for _, permission := range requirement.Permissions() {
			for _, role := range auth.AllRoles {
				reach := rbac.ReachOf(role, permission)
				if reach == "" || reach == rbac.ScopeAny {
					continue
				}
				reaches = append(reaches, fmt.Sprintf("%s/%s=%s", permission, role, reach))
			}
		}
		if len(reaches) == 0 {
			continue
		}
		sort.Strings(reaches)
		out = append(out, scopeNarrowingRoute{
			key: key, scoped: requirement.EnforcesResourceScope(), reaches: reaches,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}

// The list, printed every run. Not an assertion: the routes below are refused, which is
// safe, and a checkpoint that opens one should make this list shorter rather than make this
// test louder.
func TestTheRoutesStillRefusedForWantOfAResourceCheckAreNamed(t *testing.T) {
	narrowing := scopeNarrowingRoutes(t)

	var refused, scoped []string
	for _, r := range narrowing {
		if r.scoped {
			scoped = append(scoped, r.key)
			continue
		}
		refused = append(refused, fmt.Sprintf("%-58s %s", r.key, strings.Join(r.reaches, " ")))
	}

	t.Logf("%d declared routes grant some role a reach narrower than the facility.", len(narrowing))
	t.Logf("%d of them declare httpx.PermissionScoped and judge the resource in the handler:\n  %s",
		len(scoped), strings.Join(scoped, "\n  "))
	t.Logf("%d are refused by the route guard for exactly those roles, because no layer would "+
		"judge the resource. Each needs a handler that loads its resource and calls "+
		"rbac.Authorize before it can be opened:\n  %s",
		len(refused), strings.Join(refused, "\n  "))

	if len(narrowing) == 0 {
		t.Fatal("no route grants a narrow reach, which cannot be true of this catalogue: " +
			"the sweep is reading the engine wrongly and would pass whatever the code did")
	}
}

// A declaration that promises something nothing needs is dead weight, and dead weight in a
// guard is worse than elsewhere: the next person reads it as evidence that the route was
// thought about.
func TestNoRouteDeclaresAResourceCheckItDoesNotNeed(t *testing.T) {
	declarations, err := httpx.Declarations(contractRouter(t))
	if err != nil {
		t.Fatalf("reading the route declarations: %v", err)
	}
	narrow := map[string]bool{}
	for _, r := range scopeNarrowingRoutes(t) {
		narrow[r.key] = true
	}
	for key, requirement := range declarations {
		if requirement.EnforcesResourceScope() && !narrow[key] {
			t.Errorf("%s declares httpx.PermissionScoped, but every role that holds its "+
				"permissions reaches the whole facility; the route guard's decision is already "+
				"the whole decision and the declaration only invites a check nobody owes", key)
		}
	}
}

// The route the defect was measured on, held by name: it must be scoped, because both roles
// that hold its permission are scoped narrower than the facility and it would otherwise be
// refused to the only people whose job it is.
func TestTheRegistrationRouteDeclaresItsResourceCheck(t *testing.T) {
	declarations, err := httpx.Declarations(contractRouter(t))
	if err != nil {
		t.Fatalf("reading the route declarations: %v", err)
	}
	const key = http.MethodPost + " /v1/patients"
	requirement, ok := declarations[key]
	if !ok {
		t.Fatalf("%s is not declared; the route table moved and this test stopped meaning anything", key)
	}
	if !requirement.EnforcesResourceScope() {
		t.Fatalf("%s does not declare httpx.PermissionScoped. Its permission is held only by "+
			"REGISTRATION and FIELD_WORKER, both scoped narrower than the facility, so the guard "+
			"refuses it — which is the defect, exactly as it was.", key)
	}
}
