package rbac_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
)

// The hole the loosening opens, and the thing that closes it (CP83).
//
// Letting a station-scoped role through the route guard is only safe if something further
// in judges the resource. These tests drive the real guard — httpx.Declared, the real
// middleware, the real rbac.HTTPAuthorizer and the real engine — and assert the three
// outcomes that matter: a resource at another station is refused; a resource at the
// caller's own station is allowed, so the refusal above is not vacuous; and a handler that
// judges nothing at all cannot answer 2xx, whatever it tried to write.
//
// The grants come from a stub reader rather than a database because what is under test is
// the decision chain, and a role table would only make these slower to run and no more
// real: the resolver's own database contract is held by resolver_db_test.go.

type stubGrants struct {
	userID uuid.UUID
	roles  []auth.RoleCode
}

func (s stubGrants) LiveGrants(context.Context, uuid.UUID) ([]auth.Grant, error) {
	out := make([]auth.Grant, 0, len(s.roles))
	for _, role := range s.roles {
		out = append(out, auth.Grant{UserID: s.userID, RoleCode: role, FacilityID: facility})
	}
	return out, nil
}

func (s stubGrants) GetUser(_ context.Context, id uuid.UUID) (auth.User, error) {
	return auth.User{ID: id, FacilityID: facility, Status: auth.StatusActive}, nil
}

// guardedServer mounts one declared route behind the real authorisation middleware.
func guardedServer(t *testing.T, roles []auth.RoleCode, req httpx.Requirement, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	user := me
	resolver := rbac.NewResolver(rbac.ResolverConfig{Grants: stubGrants{userID: user, roles: roles}})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	roleCodes := make([]string, 0, len(roles))
	for _, r := range roles {
		roleCodes = append(roleCodes, string(r))
	}
	caller := httpx.Caller{
		UserID: user.String(), FacilityID: facility.String(), SessionID: uuid.NewString(),
		Code: "OP01", Roles: roleCodes,
	}

	r := chi.NewRouter()
	r.Use(httpx.Authorize(logger, &rbac.HTTPAuthorizer{Resolver: resolver}))
	r.Use(func(next http.Handler) http.Handler { return httpx.CallerForTest(caller, next) })
	r.Method("POST", "/thing", httpx.Declare(req, h))

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func post(t *testing.T, srv *httptest.Server, role auth.RoleCode) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/thing", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(httpx.ActiveRoleHeader, string(role))
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(body)
}

// standingAt is the handler's half of the bargain: it knows where the person is working
// and what they are acting on, neither of which the route could know.
//
// The station is put on the subject here because nothing plumbs a station into a session
// yet — the guard resolves every caller with StationID nil. That is a real gap, recorded
// in the sweep this checkpoint leaves behind, and it is not what these tests are about:
// what they hold is that when the facts *are* present the engine still refuses the wrong
// station, so the loosening at the door did not become a widening.
func standingAt(subjectStation, resourceStation string, action rbac.Action) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		subject, ok := rbac.SubjectFrom(r.Context())
		if !ok {
			http.Error(w, "no subject", http.StatusInternalServerError)
			return
		}
		subject.StationCode = subjectStation
		ctx := rbac.WithSubject(r.Context(), subject)

		resource := rbac.Resource{
			Kind: "observation", FacilityID: facility,
			ID: uuid.New(), StationCode: resourceStation,
		}
		if err := rbac.Authorize(ctx, action, resource); err != nil {
			httpx.WriteError(w, r, slog.New(slog.NewTextHandler(io.Discard, nil)), err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"judged": true})
	}
}

// The most important test in this change: the endpoint gate no longer applies station
// scope, so the service layer must, and here it is asked to let somebody reach the next
// station's work.
func TestAStationRoleCannotReachAnotherStationsResource(t *testing.T) {
	srv := guardedServer(t, []auth.RoleCode{auth.RoleAnthropometry},
		httpx.PermissionScoped(auth.PermObservationWriteAnthro),
		standingAt(station, station2, auth.PermObservationWriteAnthro))

	status, body := post(t, srv, auth.RoleAnthropometry)
	if status != http.StatusForbidden {
		t.Fatalf("answered %d (%s); an anthropometry officer standing at one station reached an "+
			"observation at another, which is the escalation this change had to not create", status, body)
	}
	assertSaysNothingAboutTheResource(t, body)
}

// The same route, the same role, the resource at the station they are actually working. A
// refusal here would mean the test above proves only that the route is broken.
func TestAStationRoleReachesItsOwnStationsResource(t *testing.T) {
	srv := guardedServer(t, []auth.RoleCode{auth.RoleAnthropometry},
		httpx.PermissionScoped(auth.PermObservationWriteAnthro),
		standingAt(station, station, auth.PermObservationWriteAnthro))

	status, body := post(t, srv, auth.RoleAnthropometry)
	if status != http.StatusOK {
		t.Fatalf("answered %d (%s); the officer was at the station the observation is at", status, body)
	}
}

// The guard's promise, kept by something other than a comment: a handler on a scoped route
// that never judges the resource cannot answer 2xx, even though it wrote one.
func TestAHandlerThatJudgesNothingCannotAnswerSuccess(t *testing.T) {
	srv := guardedServer(t, []auth.RoleCode{auth.RoleAnthropometry},
		httpx.PermissionScoped(auth.PermObservationWriteAnthro),
		func(w http.ResponseWriter, r *http.Request) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"wrote": "a body nobody authorised"})
		})

	status, body := post(t, srv, auth.RoleAnthropometry)
	if status != http.StatusForbidden {
		t.Fatalf("answered %d (%s); the scope debt was never settled and the response went out anyway",
			status, body)
	}
	if len(body) > 0 && bodyMentions(body, "nobody authorised") {
		t.Errorf("the handler's body escaped with the refusal: %s", body)
	}
	assertSaysNothingAboutTheResource(t, body)
}

// A handler that returns without writing at all would otherwise become an empty 200.
func TestAHandlerThatWritesNothingIsRefusedToo(t *testing.T) {
	srv := guardedServer(t, []auth.RoleCode{auth.RoleAnthropometry},
		httpx.PermissionScoped(auth.PermObservationWriteAnthro),
		func(http.ResponseWriter, *http.Request) {})

	if status, body := post(t, srv, auth.RoleAnthropometry); status != http.StatusForbidden {
		t.Fatalf("answered %d (%s); an unsettled debt must not leave as a 200", status, body)
	}
}

// A route that does not promise to judge the resource is refused for the roles whose reach
// is narrow — the same answer it gave before this change, which is what makes the change
// safe to make one route at a time.
func TestANarrowReachOnAnUnscopedRouteIsStillRefused(t *testing.T) {
	srv := guardedServer(t, []auth.RoleCode{auth.RoleAnthropometry},
		httpx.Permission(auth.PermObservationWriteAnthro),
		func(w http.ResponseWriter, r *http.Request) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"reached": true})
		})

	status, body := post(t, srv, auth.RoleAnthropometry)
	if status != http.StatusForbidden {
		t.Fatalf("answered %d (%s); the route declares no resource check and the role's reach is "+
			"narrower than the facility, so nothing would have judged the resource", status, body)
	}
}

// A facility-wide reach is untouched: it owes nothing, so it neither carries a debt nor
// needs a scoped declaration.
func TestAFacilityWideReachNeedsNoDeclarationAndNoDebt(t *testing.T) {
	srv := guardedServer(t, []auth.RoleCode{auth.RolePhysician},
		httpx.Permission(auth.PermPatientReadDemographics),
		func(w http.ResponseWriter, r *http.Request) {
			if _, owed := httpx.ScopeDebtFrom(r.Context()); owed {
				http.Error(w, "a facility-wide reach left a debt", http.StatusInternalServerError)
				return
			}
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"reached": true})
		})

	if status, body := post(t, srv, auth.RolePhysician); status != http.StatusOK {
		t.Fatalf("answered %d (%s)", status, body)
	}
}

// Every refusal in this file must be the same sentence. A 403 that varied with the cause
// would say whether the resource exists, which is the one thing a refusal may never say.
func assertSaysNothingAboutTheResource(t *testing.T, body string) {
	t.Helper()
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("the refusal is not the standard envelope: %s", body)
	}
	for _, leak := range []string{"station", "observation", station, station2} {
		if bodyMentions(envelope.Error.Message, leak) {
			t.Errorf("the refusal names %q, which tells the caller about a resource they may not see: %s",
				leak, envelope.Error.Message)
		}
	}
}

func bodyMentions(body, needle string) bool {
	return needle != "" && strings.Contains(body, needle)
}
