package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/patient"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/secretbox"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
)

// The test whose absence cost the clinic `POST /v1/patients` (CP83).
//
// # What it holds
//
// A registration clerk, holding REGISTRATION and nothing else, signing in for real at a
// real enrolled workstation, registering a real patient through the real router — and being
// answered 201.
//
// Every one of those words was true of the clinic and false of the test suite. The route
// guard asked `rbac.Can` about `Resource{Kind: "route"}`; `patient.write.demographics`
// reaches only their own station for REGISTRATION and only their own captures for
// FIELD_WORKER, and those are the only two roles that hold it; a resource standing at no
// station matches no station, so both were refused. `POST /v1/patients` answered 403 to
// every role in the catalogue, and nothing said so, because the module's own tests supply
// a fake authorizer that says yes and the route sweep in write_routes_db_test.go counts an
// authorisation refusal as "not a device problem" and moves on.
//
// So the assertion here is deliberately end-to-end and deliberately about one route. What
// makes it worth its runtime is the number of real things between the request and the row:
// the sign-in, the session, the workstation binding, the middleware chain, the RBAC
// engine's resolution of the person, `Reaches`, the scope debt, the handler's own call to
// `rbac.AuthorizeCreation`, the event store and the projection.

type registrationStack struct {
	*httptest.Server
	db       *testsupport.DB
	pool     *pgxpool.Pool
	store    *auth.PostgresStore
	devices  *auth.Devices
	facility uuid.UUID
	user     uuid.UUID
}

// mountExtra is a module's routes, added to the stack beside the patient ones.
//
// A parameter rather than a second copy of this constructor: the sign-in, the workstation,
// the middleware chain and the RBAC wiring are the expensive and interesting half, and a
// second stack that assembled them again would be a second place for them to drift out of
// step with what run() does. CP85's reference-route test is the only caller.
type mountExtra func(pool *pgxpool.Pool, logger *slog.Logger, r chi.Router)

// patientSub is a module's **per-patient** routes, mounted inside `/v1/patients` where the
// composition root puts them.
//
// A second parameter rather than a second entry in `extra`, because the two are mounted in
// different places and chi will not let a caller work that out for itself: `patient.Handlers`
// takes `/patients` with a `Mount`, and a route registered at `/patients/{id}/education` beside
// it panics at start-up with "attempting to Mount() a handler on an existing path". The
// composition root reaches these through `patient.HandlersConfig.Sub`, and so does this.
type patientSub func(pool *pgxpool.Pool, logger *slog.Logger) func(chi.Router)

func newRegistrationStack(t *testing.T, extra ...mountExtra) *registrationStack {
	return newStackWithPatientRoutes(t, nil, extra...)
}

func newStackWithPatientRoutes(t *testing.T, sub []patientSub, extra ...mountExtra) *registrationStack {
	t.Helper()

	db := testsupport.Postgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	pool, err := pgxpool.New(ctx, db.DSN)
	if err != nil {
		t.Fatalf("opening a pool on %s: %v", db.Name, err)
	}
	t.Cleanup(pool.Close)

	s := &registrationStack{db: db}
	s.pool = pool
	if err := db.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&s.facility); err != nil {
		t.Fatalf("reading the default facility: %v", err)
	}
	s.store = auth.NewPostgresStore(pool)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	events := eventstore.New(eventstore.Config{
		Pool: pool, Clock: clock.Real{},
		Synchronous: projection.NewSyncSet(projection.Default),
	})
	// As cmd/api does at start-up: a synchronous projection with no state row advances
	// nothing, and the registration would write an event and return no patient.
	if err := projection.NewEngineWithEvents(pool, projection.Default, events).Register(ctx); err != nil {
		t.Fatalf("registering the projections: %v", err)
	}

	ring, err := secretbox.NewRing(secretbox.Key{ID: "k1", Material: bytes.Repeat([]byte{7}, secretbox.KeySize)})
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := patient.NewIdentifierSealer(bytes.Repeat([]byte{11}, 32), ring)
	if err != nil {
		t.Fatal(err)
	}

	patientStore := patient.NewStore(pool)
	mounts := make([]func(chi.Router), 0, len(sub))
	for _, build := range sub {
		mounts = append(mounts, build(pool, logger))
	}
	handlers := patient.NewHandlers(patient.HandlersConfig{
		Service: patient.NewService(patient.ServiceConfig{
			Store: patientStore, Events: events, Sealer: sealer, Clock: clock.Real{},
		}),
		Store: patientStore, Sub: mounts, Clock: clock.Real{}, Logger: logger,
	})

	s.devices = auth.NewDevices(auth.DevicesConfig{
		Store: s.store, Nonces: forgetfulNonces{}, Clock: clock.Real{},
	})
	sessions := auth.NewSessions(auth.SessionsConfig{
		Store: s.store, Hasher: cheapHasher(), Clock: clock.Real{}, Workstations: s.devices,
	})
	authHandlers := auth.NewHandlers(auth.HandlersConfig{
		Sessions: sessions, Store: s.store, Logger: logger,
		FacilityID: s.facility, SecureCookies: false,
	})
	resolver := rbac.NewResolver(rbac.ResolverConfig{Grants: s.store, Clock: clock.Real{}})

	router, err := httpx.NewRouter(httpx.RouterOptions{
		Logger:         logger,
		IDs:            &ids.Sequential{Prefix: "req"},
		AllowedOrigins: []string{"http://localhost:3000"},
		MaxBodyBytes:   64 * 1024,
		RequestTimeout: 30 * time.Second,
		Authenticator:  &auth.Identifier{Sessions: sessions, Store: s.store},
		DeviceVerifier: &auth.DeviceVerifierAdapter{Devices: s.devices},
		// The reach store, as cmd/api wires it. Without one every station-scoped resource
		// check answers "no reach store on the context", which is a 403 that looks exactly
		// like a policy refusal — so the escalation test below would pass for the wrong
		// reason and prove nothing.
		Authorizer: &rbac.HTTPAuthorizer{Resolver: resolver, Reach: rbac.NewPostgresReach(pool)},
		AuthRoutes: authHandlers.Mount,
		Routes: func(r chi.Router) {
			handlers.Mount(r)
			for _, mount := range extra {
				mount(pool, logger, r)
			}
		},
	})
	if err != nil {
		t.Fatalf("building the router: %v", err)
	}

	s.Server = httptest.NewServer(router)
	t.Cleanup(s.Close)
	return s
}

// seedClerk creates an active user holding exactly one role.
//
// Exactly one, and REGISTRATION, because the whole defect lives in that role's reach: a
// user holding several roles could be authorised by a different hat and the green would
// mean nothing.
func (s *registrationStack) seedClerk(t *testing.T, code string, role auth.RoleCode) {
	t.Helper()
	hash, err := cheapHasher().Hash(writeTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	err = s.db.SQL.QueryRow(`
		INSERT INTO core.app_user (facility_id, employee_code, name_en, name_bn, password_hash, password_set_at, status)
		VALUES ($1, $2, 'Registration Clerk', 'নিবন্ধন কর্মকর্তা', $3, now(), 'active')
		RETURNING id`, s.facility, code, hash).Scan(&s.user)
	if err != nil {
		t.Fatalf("inserting %s: %v", code, err)
	}
	if _, err := s.db.SQL.Exec(`
		INSERT INTO core.user_role (user_id, role_id, facility_id)
		SELECT $1, id, $2 FROM core.role WHERE code = $3`, s.user, s.facility, string(role)); err != nil {
		t.Fatalf("granting %s: %v", role, err)
	}
}

func (s *registrationStack) signIn(t *testing.T, code, workstation string) string {
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
	defer func() { _ = res.Body.Close() }()
	payload, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("signing in answered %d: %s", res.StatusCode, payload)
	}
	var decoded map[string]any
	_ = json.Unmarshal(payload, &decoded)
	token, _ := decoded["access_token"].(string)
	if token == "" {
		t.Fatalf("signing in returned no access token: %s", payload)
	}
	return token
}

func (s *registrationStack) enrol(t *testing.T, name, station string) string {
	t.Helper()
	device, err := s.devices.EnrolWorkstation(context.Background(), auth.Actor{
		UserID: s.user, FacilityID: s.facility,
		Permissions: auth.NewPermissionSet(auth.PermDeviceEnroll),
	}, name, station)
	if err != nil {
		t.Fatalf("enrolling a workstation: %v", err)
	}
	return device.WorkstationCode
}

func (s *registrationStack) post(t *testing.T, token, role, path string, body any) (int, map[string]any, string) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, s.URL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "DTHCMS")
	req.Header.Set("Idempotency-Key", uuid.NewString())
	req.Header.Set("Authorization", "Bearer "+token)
	if role != "" {
		req.Header.Set(httpx.ActiveRoleHeader, role)
	}
	res, err := s.Client().Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = res.Body.Close() }()
	payload, _ := io.ReadAll(res.Body)
	var decoded map[string]any
	_ = json.Unmarshal(payload, &decoded)
	return res.StatusCode, decoded, string(payload)
}

// registrationForm is a complete, valid registration as the desk submits it.
func registrationForm() map[string]any {
	return map[string]any{
		"event_id":            uuid.Must(uuid.NewV7()).String(),
		"name_en":             "Rahima Begum",
		"name_bn":             "রহিমা বেগম",
		"sex":                 "female",
		"birth_date":          "1979-04-12",
		"dob_precision":       "day",
		"dob_source":          "national_id",
		"phone_primary":       "01712345678",
		"phone_secondary":     "02-8812345",
		"division":            "Dhaka",
		"district":            "Faridpur",
		"upazila":             "Boalmari",
		"address_line":        "Village Rupapat",
		"postcode":            "7860",
		"emergency_name":      "Abdul Karim",
		"emergency_relation":  "son",
		"emergency_phone":     "01812345678",
		"education_level":     "secondary",
		"occupation_category": "homemaker",
		"income_band":         "10k_25k",
		"household_size":      5,
		"residence_type":      "rural",
		"medicine_payer":      "family",
		"identifiers":         map[string]string{"national_id": "1990 1234 5678"},
		"consent_reference":   "consent_2026_0001",
	}
}

// The regression. Before CP83 this answered 403, with "out_of_scope" in the log and an
// identical message on the wire, for the only role in the clinic whose job it is.
func TestARegistrationClerkCanRegisterAPatient(t *testing.T) {
	s := newRegistrationStack(t)
	s.seedClerk(t, "REG-CP83", auth.RoleRegistration)
	workstation := s.enrol(t, "Registration desk 1", "STN_REGISTRATION")
	token := s.signIn(t, "REG-CP83", workstation)

	status, body, raw := s.post(t, token, string(auth.RoleRegistration), "/v1/patients", registrationForm())
	if status != http.StatusCreated {
		t.Fatalf("POST /v1/patients answered %d: %s\n\n"+
			"A registration clerk holds patient.write.demographics and nothing else does the job. "+
			"If this is a 403, the route guard is asking a resource question again.", status, raw)
	}
	registered, _ := body["patient"].(map[string]any)
	if registered == nil || registered["id"] == nil {
		t.Fatalf("a 201 with no patient in it: %s", raw)
	}

	// And the row is really there, so that a handler which learned to answer 201 without
	// writing anything would not pass.
	var count int
	if err := s.db.SQL.QueryRow(`SELECT count(*) FROM read.patient WHERE patient_id = $1`,
		registered["id"]).Scan(&count); err != nil {
		t.Fatalf("reading the projection: %v", err)
	}
	if count != 1 {
		t.Fatalf("the projection holds %d rows for the patient that was just registered", count)
	}
}

// The other half: loosening the door did not open it to somebody who was never meant
// through it. A counsellor holds no `patient.write.demographics` at all.
func TestACounsellorStillCannotRegisterAPatient(t *testing.T) {
	s := newRegistrationStack(t)
	s.seedClerk(t, "CNS-CP83", auth.RoleCounselor)
	workstation := s.enrol(t, "Counselling desk 1", "STN_COUNSELING")
	token := s.signIn(t, "CNS-CP83", workstation)

	status, _, raw := s.post(t, token, string(auth.RoleCounselor), "/v1/patients", registrationForm())
	if status != http.StatusForbidden {
		t.Fatalf("POST /v1/patients answered %d for a counsellor: %s", status, raw)
	}
}
