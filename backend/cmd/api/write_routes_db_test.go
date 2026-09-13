package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/auth/pwhash"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
)

// The write half of the envelope question (CP82, ADR-0021, D-71).
//
// # The test whose absence let 118 write routes stay broken
//
// `TestNoReadRouteDemandsAWriteEnvelope` (eventstore/reader.go's note) is the read half: it
// drives a real login with **no device** through every declared GET route and fails on any
// that refuses with DEVICE_REQUIRED, because a read is not evidence and needs no machine.
//
// This is the other half, and it is the one the product was blocked on. Every declared
// POST, PUT and PATCH under /v1 is driven with a real login **bound to a NAMED workstation**
// — a desktop enrolled through the real device service, its code minted by the real
// allocator, typed into the real sign-in endpoint — and none of them may answer
// DEVICE_REQUIRED. Before CP82 every single one of them did, from every browser in the
// clinic, and nothing in the suite said so: every domain test builds its identity with
// `ActorForTest`, which takes a device id as an argument and therefore always has one.
//
// # What is real here, and what is not — stated plainly, because it decides what this proves
//
// Real: the route table (`contractRouter` assembles exactly what `run()` does), each route's
// declared permission, the full middleware chain, the database, the device enrolment, the
// workstation allocator, the sign-in, the session row and its device_binding, the RBAC
// engine's resolution of the caller, and `eventstore.ActorFrom`.
//
// Not real: the domain handler bodies. Each declared write route is re-mounted here with the
// same requirement and a probe that asks for the write envelope and the read identity and
// reports what it got. Driving the real handlers would mean a legal body for a hundred and
// eighteen different schemas and a fixture for every one of them, and the property under
// test is not in those bodies — it is upstream of all of them, in the one line each handler
// shares: `eventstore.ActorFrom(ctx)`. Every DEVICE_REQUIRED in this codebase is that call
// failing, mapped to a status.
//
// # Why it cannot pass vacuously
//
// Two ways a test like this quietly proves nothing: the route is refused with a 403 before
// the probe runs, so "not DEVICE_REQUIRED" is true and meaningless; or so few routes are
// driven that the green says nothing. Both are guarded. Every route that *is* reached must
// produce a NAMED envelope naming the enrolled desk, the count of routes reached has a floor
// that a regression drops below, and the routes refused earlier are printed by name on every
// run rather than absorbed. `TestAnUnrecognisedWorkstationSignsInWithNoDeviceAndStillCannotWrite`
// is the negative: the same drive from a session that named no desk builds no envelope
// anywhere, which is what makes the green run mean something.

// probeResult is what a probe reports about the identity the chain handed it.
type probeResult struct {
	ActorOK   bool   `json:"actor_ok"`
	ActorErr  string `json:"actor_err"`
	DeviceID  string `json:"device_id"`
	Assurance string `json:"assurance"`
	ReaderOK  bool   `json:"reader_ok"`
	ReaderErr string `json:"reader_err"`
}

// writeStack is one test's private API: a real database, the real auth and device services,
// the real RBAC engine, and a router carrying the real route table's declarations.
type writeStack struct {
	*httptest.Server
	db       *testsupport.DB
	store    *auth.PostgresStore
	devices  *auth.Devices
	sessions *auth.Sessions
	facility uuid.UUID
	user     uuid.UUID
	roles    []string

	// writeRoutes is every declared POST/PUT/PATCH the real router serves, with its
	// requirement, in a stable order.
	writeRoutes []declaredRoute

	// reached records the routes whose probe actually ran, so that a route refused before
	// the probe cannot be mistaken for a route that passed.
	reached map[string]bool
}

type declaredRoute struct {
	method      string
	pattern     string
	requirement httpx.Requirement
}

const writeTestPassword = "correct horse battery staple"

// minimumWriteRoutesReached is how many declared write routes this test drove all the way to
// an attribution envelope (88 of 105).
//
// A floor rather than an exact count, so that adding a write route does not fail the suite —
// and a floor rather than nothing, so that a change which makes routes unreachable cannot
// make this test quietly cover less while still passing.
//
// It was 87 when this file was written, and the eighteen it did not reach were refused by
// the route guard asking a resource question with no resource — the defect CP83 names and
// fixes. Fixing the layering did not by itself open those routes: the guard now refuses a
// route whose permission reaches less than the facility and which has not declared that its
// handler judges the resource, which is the same refusal with an honest reason. They open
// one at a time, as each handler learns to judge its resource. `POST /v1/patients` is the
// first, which is why this is 88; the other seventeen are still listed below on every run.
const minimumWriteRoutesReached = 88

// cheapHasher is argon2id at a cost that makes a login take milliseconds. The parameters
// travel with the hash, so this changes nothing about what is under test.
func cheapHasher() *pwhash.Hasher {
	return pwhash.New(pwhash.Params{
		Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32,
	})
}

func newWriteStack(t *testing.T) *writeStack {
	t.Helper()

	db := testsupport.Postgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	pool, err := pgxpool.New(ctx, db.DSN)
	if err != nil {
		t.Fatalf("opening a pool on %s: %v", db.Name, err)
	}
	t.Cleanup(pool.Close)

	s := &writeStack{db: db, reached: map[string]bool{}}
	if err := db.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&s.facility); err != nil {
		t.Fatalf("reading the default facility: %v", err)
	}
	s.store = auth.NewPostgresStore(pool)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// The real device service. Nonces go to memory rather than Redis: nothing here presents
	// a device signature, and a nonce store is not what is under test.
	s.devices = auth.NewDevices(auth.DevicesConfig{
		Store: s.store, Nonces: forgetfulNonces{}, Clock: clock.Real{},
	})
	s.sessions = auth.NewSessions(auth.SessionsConfig{
		Store: s.store, Hasher: cheapHasher(), Clock: clock.Real{},
		// The wiring this checkpoint adds, in the order the composition root uses it.
		Workstations: s.devices,
	})

	authHandlers := auth.NewHandlers(auth.HandlersConfig{
		Sessions: s.sessions, Store: s.store, Logger: logger,
		FacilityID: s.facility, SecureCookies: false,
	})

	resolver := rbac.NewResolver(rbac.ResolverConfig{Grants: s.store, Clock: clock.Real{}})

	s.writeRoutes = declaredWriteRoutes(t)

	router, err := httpx.NewRouter(httpx.RouterOptions{
		Logger:         logger,
		IDs:            &ids.Sequential{Prefix: "req"},
		AllowedOrigins: []string{"http://localhost:3000"},
		MaxBodyBytes:   64 * 1024,
		RequestTimeout: 30 * time.Second,
		Authenticator:  &auth.Identifier{Sessions: s.sessions, Store: s.store},
		DeviceVerifier: &auth.DeviceVerifierAdapter{Devices: s.devices},
		Authorizer:     &rbac.HTTPAuthorizer{Resolver: resolver},
		AuthRoutes:     authHandlers.Mount,
		Routes:         s.mountProbes,
	})
	if err != nil {
		t.Fatalf("building the router: %v", err)
	}

	s.Server = httptest.NewServer(router)
	t.Cleanup(s.Close)
	return s
}

// mountProbes re-declares every write route the real router serves, with its real
// requirement and a probe in place of the handler.
func (s *writeStack) mountProbes(r chi.Router) {
	for _, route := range s.writeRoutes {
		route := route
		key := route.method + " " + route.pattern
		// The pattern arrives from chi.Walk with the /v1 prefix the real router mounts it
		// under; this router mounts under /v1 too, so it comes off again.
		pattern := strings.TrimPrefix(route.pattern, "/v1")
		r.Method(route.method, pattern, httpx.Declare(route.requirement,
			func(w http.ResponseWriter, req *http.Request) {
				s.reached[key] = true
				// The probe stands in for the handler, so it has to stand in for the
				// handler's resource check too (CP83). A route declared
				// httpx.PermissionScoped is entered by roles whose reach is narrower than
				// the facility, on the promise that the handler asks the engine again with
				// the resource in hand; the response is refused if nothing does. The probe
				// has no resource and is not testing scope — it is testing the device
				// envelope — so it settles the debt and says why. dthclint's scopecheck
				// holds the real handlers to the real thing.
				httpx.SettleScopeDebt(req.Context())
				result := probeResult{}
				actor, err := eventstore.ActorFrom(req.Context())
				if err != nil {
					result.ActorErr = err.Error()
				} else {
					result.ActorOK = true
					result.DeviceID = actor.DeviceID().String()
					result.Assurance = actor.Assurance()
				}
				reader, err := eventstore.ReaderFrom(req.Context())
				if err != nil {
					result.ReaderErr = err.Error()
				} else {
					result.ReaderOK = true
					_ = reader
				}
				httpx.WriteJSON(w, http.StatusOK, result)
			}))
	}
}

// declaredWriteRoutes is the real route table's mutating half.
//
// Read off the router `run()` assembles rather than from a list in this file, which is the
// whole point: a write route added next month is covered without anybody editing a test, and
// a route added without a declaration never reaches here because the router refuses to build.
func declaredWriteRoutes(t *testing.T) []declaredRoute {
	t.Helper()

	declarations, err := httpx.Declarations(contractRouter(t))
	if err != nil {
		t.Fatalf("reading the route declarations: %v", err)
	}

	var out []declaredRoute
	for key, requirement := range declarations {
		method, pattern, ok := strings.Cut(key, " ")
		if !ok {
			continue
		}
		switch method {
		case http.MethodPost, http.MethodPut, http.MethodPatch:
		default:
			continue
		}
		// Only the authenticated surface. The unauthenticated corner — sign in, refresh,
		// device enrolment — does not write clinical events and could not carry an
		// envelope: it is where one is obtained.
		if !strings.HasPrefix(pattern, "/v1/") || strings.HasPrefix(pattern, "/v1/auth/") {
			continue
		}
		if requirement.IsPublic() {
			continue
		}
		out = append(out, declaredRoute{method: method, pattern: pattern, requirement: requirement})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].pattern != out[j].pattern {
			return out[i].pattern < out[j].pattern
		}
		return out[i].method < out[j].method
	})
	if len(out) == 0 {
		t.Fatal("found no declared write routes; this test is reading the route table wrongly " +
			"and would pass whatever the code did")
	}
	return out
}

// forgetfulNonces accepts every nonce as fresh. Nothing in this file presents a device
// signature, so there is no replay to detect and no reason to require Redis.
type forgetfulNonces struct{}

func (forgetfulNonces) Remember(context.Context, string, time.Duration) (bool, error) {
	return true, nil
}

// --- fixtures ---

// seedUser creates an active user with a password and every role the clinic has.
//
// Every role, because the question is whether a *device* stops a write, and a 403 for a
// permission the person does not hold would mask that on route after route. The test still
// names the role it is acting as on each request, so nothing is authorised without a hat.
func (s *writeStack) seedUser(t *testing.T, code string) {
	t.Helper()

	hash, err := cheapHasher().Hash(writeTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	err = s.db.SQL.QueryRow(`
		INSERT INTO core.app_user (facility_id, employee_code, name_en, name_bn, password_hash, password_set_at, status)
		VALUES ($1, $2, 'Write Route Probe', 'পরীক্ষামূলক ব্যবহারকারী', $3, now(), 'active')
		RETURNING id`, s.facility, code, hash).Scan(&s.user)
	if err != nil {
		t.Fatalf("inserting %s: %v", code, err)
	}

	rows, err := s.db.SQL.Query(`SELECT code FROM core.role ORDER BY code`)
	if err != nil {
		t.Fatalf("reading the role catalogue: %v", err)
	}
	defer rows.Close()
	var all []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			t.Fatal(err)
		}
		all = append(all, role)
	}
	for _, role := range all {
		// One at a time, and a refusal is not fatal: §4.4 keeps some roles apart in the
		// database, and a separation of duties rule is not this test's to argue with.
		if _, err := s.db.SQL.Exec(`
			INSERT INTO core.user_role (user_id, role_id, facility_id)
			SELECT $1, id, $2 FROM core.role WHERE code = $3`, s.user, s.facility, role); err != nil {
			continue
		}
		s.roles = append(s.roles, role)
	}
	if len(s.roles) == 0 {
		t.Fatal("the probe user holds no roles; every route below would be a 403 and the test " +
			"would prove nothing")
	}
}

// enrolWorkstation registers a desktop through the real service and returns its printed code.
func (s *writeStack) enrolWorkstation(t *testing.T, name, station string) (auth.Device, string) {
	t.Helper()
	actor := auth.Actor{
		UserID: s.user, FacilityID: s.facility,
		Permissions: auth.NewPermissionSet(auth.PermDeviceEnroll),
	}
	device, err := s.devices.EnrolWorkstation(context.Background(), actor, name, station)
	if err != nil {
		t.Fatalf("enrolling a workstation: %v", err)
	}
	if device.WorkstationCode == "" {
		t.Fatal("the enrolment minted no workstation code")
	}
	if device.Status != auth.DeviceActive {
		t.Fatalf("a workstation was enrolled %s; a desk that is not active resolves to nothing "+
			"and every sign-in at it would be device-less", device.Status)
	}
	return device, device.WorkstationCode
}

// signIn performs a real sign-in through the real endpoint and returns the access token and
// what the server said about the workstation.
func (s *writeStack) signIn(t *testing.T, code, workstation string) (string, map[string]any) {
	t.Helper()
	body := map[string]any{"employee_code": code, "password": writeTestPassword, "transport": "bearer"}
	if workstation != "" {
		body["workstation"] = workstation
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, s.URL+"/v1/auth/login", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "DTHCMS")
	req.Header.Set("Idempotency-Key", uuid.NewString())

	res, err := s.Client().Do(req)
	if err != nil {
		t.Fatalf("signing in: %v", err)
	}
	defer res.Body.Close()
	var decoded map[string]any
	payload, _ := io.ReadAll(res.Body)
	_ = json.Unmarshal(payload, &decoded)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("signing in answered %d: %s", res.StatusCode, payload)
	}
	token, _ := decoded["access_token"].(string)
	if token == "" {
		t.Fatalf("signing in returned no access token: %s", payload)
	}
	return token, decoded
}

// drive sends one request to a route, filling path parameters with a syntactically valid
// uuid so that nothing is refused for the shape of its URL.
func (s *writeStack) drive(t *testing.T, route declaredRoute, token, role string) (int, string, probeResult) {
	t.Helper()

	path := route.pattern
	for {
		open := strings.Index(path, "{")
		if open < 0 {
			break
		}
		close := strings.Index(path[open:], "}")
		if close < 0 {
			break
		}
		path = path[:open] + uuid.NewString() + path[open+close+1:]
	}

	req, err := http.NewRequest(route.method, s.URL+path, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "DTHCMS")
	req.Header.Set("Idempotency-Key", uuid.NewString())
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Active-Role", role)

	res, err := s.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", route.method, path, err)
	}
	defer res.Body.Close()
	payload, _ := io.ReadAll(res.Body)

	var probe probeResult
	_ = json.Unmarshal(payload, &probe)
	return res.StatusCode, string(payload), probe
}

// errorCode reads the envelope's code, or "" when the body is not a refusal.
func errorCode(payload string) string {
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(payload), &envelope)
	return envelope.Error.Code
}

// --- the test ---

// TestNoWriteRouteDemandsAProvenDevice drives every declared write route with a session bound
// to a NAMED workstation, and fails on any that refuses for want of a device.
func TestNoWriteRouteDemandsAProvenDevice(t *testing.T) {
	s := newWriteStack(t)
	s.seedUser(t, "WR01")
	device, code := s.enrolWorkstation(t, "Registration desk 1", "REG")

	token, body := s.signIn(t, "WR01", code)
	if recognised, ok := body["workstation_recognised"].(bool); !ok || !recognised {
		t.Fatalf("the sign-in did not recognise %s: %v", code, body)
	}

	// The session row, before anything is driven. If this is wrong every assertion below is
	// measuring the wrong thing.
	var boundDevice uuid.UUID
	var binding string
	err := s.db.SQL.QueryRow(`
		SELECT device_id, device_binding FROM core.session
		 WHERE user_id = $1 AND revoked_at IS NULL
		 ORDER BY issued_at DESC LIMIT 1`, s.user).Scan(&boundDevice, &binding)
	if err != nil {
		t.Fatalf("reading the session row: %v", err)
	}
	if boundDevice != device.ID || binding != string(auth.BindingNamed) {
		t.Fatalf("session bound to device %s / %q, want %s / NAMED", boundDevice, binding, device.ID)
	}

	var deviceRequired, forbidden, noEnvelope []string
	reachedCount := 0
	for _, route := range s.writeRoutes {
		key := route.method + " " + route.pattern
		var payload string
		var probe probeResult

		// Each hat in turn until one is allowed. A person wears one role per request, and
		// this is the set of roles this clinic has.
		for _, role := range s.roles {
			_, payload, probe = s.drive(t, route, token, role)
			if s.reached[key] {
				break
			}
		}

		if code := errorCode(payload); code == "DEVICE_REQUIRED" {
			deviceRequired = append(deviceRequired, key)
			continue
		}
		if !s.reached[key] {
			// Refused before the probe, by authorisation rather than by a device. Recorded
			// and reported, never counted as a pass — see the note on stationScopedRoutes.
			forbidden = append(forbidden, fmt.Sprintf("%s (%s)", key, errorCode(payload)))
			continue
		}
		reachedCount++

		// A route declared httpx.Session() carries no principal by design: the principal is
		// minted by the authorisation engine, and a route with no permission requirement
		// never asks it. Such a route cannot write a clinical event and is not what this
		// test is about; what matters is that it did not refuse for want of a device.
		if len(route.requirement.Permissions()) == 0 {
			continue
		}
		if !probe.ActorOK {
			noEnvelope = append(noEnvelope, fmt.Sprintf("%s (%s)", key, probe.ActorErr))
			continue
		}
		if probe.DeviceID != device.ID.String() {
			t.Errorf("%s: envelope names device %s, want the workstation %s",
				key, probe.DeviceID, device.ID)
		}
		if probe.Assurance != httpx.AssuranceNamed {
			t.Errorf("%s: envelope assurance %q, want NAMED — a typed workstation code must "+
				"never be recorded as a proven device", key, probe.Assurance)
		}
	}

	if len(deviceRequired) > 0 {
		t.Errorf("%d of %d declared write routes refused a NAMED workstation with DEVICE_REQUIRED:\n  %s\n\n"+
			"ADR-0021 makes a browser session able to name the desk it was opened at, and the "+
			"whole point is that these routes then work. A route here is a screen the "+
			"registration desk cannot use.",
			len(deviceRequired), len(s.writeRoutes), strings.Join(deviceRequired, "\n  "))
	}
	if len(noEnvelope) > 0 {
		t.Errorf("%d declared write routes reached the handler and could not build an envelope:\n  %s",
			len(noEnvelope), strings.Join(noEnvelope, "\n  "))
	}

	// The floor. A route that stops being reached stops being covered, and the failure has
	// to be noisy rather than a number quietly going down — this whole file exists because a
	// hundred and eighteen routes were broken and nothing said so.
	if reachedCount < minimumWriteRoutesReached {
		t.Errorf("only %d write routes were driven all the way to an envelope, and %d were "+
			"driven before:\n  %s\n\n"+
			"Something now refuses earlier in the chain. Find out what, rather than lowering "+
			"this number.",
			reachedCount, minimumWriteRoutesReached, strings.Join(forbidden, "\n  "))
	}
	if len(forbidden) > 0 {
		// Reported at every run, and deliberately not a failure: these are refused by
		// AUTHORISATION, not by a device, and the refusal has nothing to do with CP82.
		//
		// Each of these declares a permission whose reach, for every role that holds it, is
		// narrower than the facility — a station's work, or a field worker's own captures —
		// and its handler does not yet judge the resource. CP83 made that refusal honest
		// rather than accidental: the guard no longer measures a station against a resource
		// that has none, it refuses because nothing downstream would measure it at all, and
		// says so in the log. They are refused rather than opened because a route reachable
		// with no scope check anywhere is worse than a route nobody can use.
		//
		// Opening one means giving its handler a resource and a call to rbac.Authorize, and
		// declaring the route httpx.PermissionScoped; dthclint's scopecheck then holds it.
		// Several of them will also need a station on the session, which nothing plumbs yet
		// — the RBAC resolver is handed a nil station on every request.
		t.Logf("%d write routes are refused by authorisation before any device question is "+
			"reached (a reach narrower than the facility, and no handler yet judging the "+
			"resource — CP83 leaves these failing closed and named):\n  %s",
			len(forbidden), strings.Join(forbidden, "\n  "))
	}

	t.Logf("drove %d declared write routes with a NAMED workstation (%s)", len(s.writeRoutes), code)
}

// TestAnUnrecognisedWorkstationSignsInWithNoDeviceAndStillCannotWrite is the negative that
// makes the test above mean something, and the rule ADR-0021 is most likely to be argued with.
//
// A code this clinic does not have **does not refuse the sign-in** — refusing would let an
// unauthenticated caller enumerate the clinic's desks, and would take a registration desk
// down over a typo. What it produces is a session with no device, from which clinical writes
// are refused exactly as they were before CP82.
//
// The failure mode being guarded against is the opposite one: an unknown code quietly
// becoming "no device" that then *works*, which would mean the whole mechanism could be
// bypassed by typing nonsense.
func TestAnUnrecognisedWorkstationSignsInWithNoDeviceAndStillCannotWrite(t *testing.T) {
	s := newWriteStack(t)
	s.seedUser(t, "WR02")
	s.enrolWorkstation(t, "Registration desk 1", "REG")

	token, body := s.signIn(t, "WR02", "FRD-NOPE-9")
	if recognised, ok := body["workstation_recognised"].(bool); !ok || recognised {
		t.Fatalf("an unknown code was reported as recognised: %v", body)
	}
	if body["workstation"] != nil && body["workstation"] != "" {
		t.Errorf("an unknown code came back as a bound workstation: %v", body["workstation"])
	}

	var deviceID *uuid.UUID
	var binding *string
	err := s.db.SQL.QueryRow(`
		SELECT device_id, device_binding FROM core.session
		 WHERE user_id = $1 AND revoked_at IS NULL
		 ORDER BY issued_at DESC LIMIT 1`, s.user).Scan(&deviceID, &binding)
	if err != nil {
		t.Fatalf("reading the session row: %v", err)
	}
	if deviceID != nil || binding != nil {
		t.Fatalf("an unrecognised code bound a device: device=%v binding=%v", deviceID, binding)
	}

	// And the writes are refused. Not a sample: every declared write route, because "it
	// silently became no device and then worked" would be a hole in exactly one of them.
	var wrote []string
	for _, route := range s.writeRoutes {
		key := route.method + " " + route.pattern
		for _, role := range s.roles {
			_, payload, probe := s.drive(t, route, token, role)
			if probe.ActorOK {
				wrote = append(wrote, key)
				break
			}
			if errorCode(payload) == "FORBIDDEN" {
				continue // a hat that may not, which says nothing about devices
			}
			break
		}
	}
	if len(wrote) > 0 {
		t.Fatalf("%d write routes built an attribution envelope from a session with no device:\n  %s\n\n"+
			"NAMED is a device; absent is still absent. A write attributed to no machine is "+
			"worse than a write that did not happen [R-03].", len(wrote), strings.Join(wrote, "\n  "))
	}
}

// TestANamedSessionRecordsTheDeviceAndTheAssuranceWhereBothCanBeReadBack proves the two
// things ADR-0021 rests on that are not visible from a handler: the device event that makes
// "which sessions claimed to be at FRD-REG-1" answerable, and the assurance surviving to the
// place that has to reason about it.
func TestANamedSessionRecordsTheDeviceAndTheAssuranceWhereBothCanBeReadBack(t *testing.T) {
	s := newWriteStack(t)
	s.seedUser(t, "WR03")
	device, code := s.enrolWorkstation(t, "Registration desk 1", "REG")

	token, _ := s.signIn(t, "WR03", code)

	// The device's own history.
	var kind, detail string
	err := s.db.SQL.QueryRow(`
		SELECT kind, detail::text FROM core.device_event
		 WHERE device_id = $1 AND kind = 'session_bound'
		 ORDER BY at DESC LIMIT 1`, device.ID).Scan(&kind, &detail)
	if err != nil {
		t.Fatalf("no session_bound event for %s: %v", code, err)
	}
	if !strings.Contains(detail, `"binding": "NAMED"`) && !strings.Contains(detail, `"binding":"NAMED"`) {
		t.Errorf("the binding event does not record the assurance: %s", detail)
	}
	if !strings.Contains(detail, code) {
		t.Errorf("the binding event does not name the workstation code: %s", detail)
	}
	// No PHI, ever. The detail carries identifiers and a printed label and nothing else.
	for _, forbidden := range []string{"Write Route Probe", "WR03", "পরীক্ষামূলক"} {
		if strings.Contains(detail, forbidden) {
			t.Errorf("the binding event carries %q, which names a person: %s", forbidden, detail)
		}
	}

	// And the assurance is readable where a caller that demands a proven device would ask.
	route := s.writeRoutes[0]
	var probe probeResult
	for _, role := range s.roles {
		_, _, probe = s.drive(t, route, token, role)
		if probe.ActorOK {
			break
		}
	}
	if !probe.ActorOK {
		t.Fatalf("could not reach any write route to read the assurance back")
	}
	if probe.Assurance != httpx.AssuranceNamed {
		t.Fatalf("assurance read back as %q, want NAMED", probe.Assurance)
	}
	if !probe.ReaderOK {
		t.Errorf("a NAMED session could not build a read identity: %s", probe.ReaderErr)
	}
}

// TestAWorkstationCodeFromAnotherFacilityNamesNothing.
//
// ADR-0021 lists "the clinic runs more than one site, where a code printed in Faridpur being
// typed in Dhaka is no longer a hypothetical" as a condition for revisiting the decision.
// That is a reason to keep the resolution facility-scoped from the first day, not a reason to
// wait: the code that would have to change later is the code being written now.
func TestAWorkstationCodeFromAnotherFacilityNamesNothing(t *testing.T) {
	s := newWriteStack(t)
	s.seedUser(t, "WR04")
	_, code := s.enrolWorkstation(t, "Registration desk 1", "REG")

	var other uuid.UUID
	err := s.db.SQL.QueryRow(`
		INSERT INTO core.facility (code, name_en, name_bn, facility_type)
		VALUES ('DTHC-DHA', 'DTHC Dhaka', 'ডিটিএইচসি ঢাকা', 'clinic')
		RETURNING id`).Scan(&other)
	if err != nil {
		t.Fatalf("creating a second facility: %v", err)
	}

	// The same code, resolved as the other clinic would resolve it.
	if _, err := s.devices.ResolveWorkstation(context.Background(), other, code); err == nil {
		t.Fatalf("%s resolved to a desk from another facility; a label printed in Faridpur "+
			"must not name a machine in Dhaka", code)
	} else if !strings.Contains(err.Error(), "no active workstation") {
		t.Fatalf("cross-facility resolution failed for the wrong reason: %v", err)
	}

	// And in this facility it still resolves, so the test above is not passing because the
	// code is simply unresolvable.
	if _, err := s.devices.ResolveWorkstation(context.Background(), s.facility, code); err != nil {
		t.Fatalf("%s does not resolve in its own facility either: %v", code, err)
	}
}

// TestASuspendedWorkstationNamesNothing: a desk is taken out of service by suspending it, and
// suspension has to reach the sign-in path or it is decoration. Nothing is ever deleted, so
// this is the only way a workstation stops being usable.
func TestASuspendedWorkstationNamesNothing(t *testing.T) {
	s := newWriteStack(t)
	s.seedUser(t, "WR05")
	device, code := s.enrolWorkstation(t, "Registration desk 1", "REG")

	if _, err := s.store.ChangeDeviceStatus(context.Background(), device.ID,
		auth.DeviceSuspended, &s.user, "the desk was moved to another room", time.Now()); err != nil {
		t.Fatalf("suspending the workstation: %v", err)
	}

	_, body := s.signIn(t, "WR05", code)
	if recognised, ok := body["workstation_recognised"].(bool); !ok || recognised {
		t.Fatalf("a suspended desk was still recognised: %v", body)
	}
}
