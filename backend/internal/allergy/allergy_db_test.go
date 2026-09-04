package allergy_test

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

	"github.com/AmlanWTK/DTHCMS/backend/internal/allergy"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
)

// The allergy hard stop (CP54, §3 step 4).
//
// Four acceptance criteria, and the first and the fourth are the same sentence read twice:
//
//	1. a visit cannot advance past the history station without allergy status;
//	4. the gate cannot be bypassed by any client.
//
// Criterion 4 is what decides how these tests are written. It is not enough to show that the
// handler refuses; the interesting question is what happens when nobody goes through the
// handler at all. So the gate is asserted **against the database directly**, with a plain
// INSERT — because that is the shape of every path nobody thought about: a support script, a
// migration, next year's second client.
//
// Criterion 2 is the other one worth reading closely. "No Known Allergies" must never be a
// default or an empty field, and the failure that would actually take is not a bad row: it is
// somebody adding a column default to make something pass, after which every patient asserts.

type api struct {
	*testsupport.DB
	store    *allergy.Store
	service  *allergy.Service
	server   *httptest.Server
	clock    *clock.Fixed
	facility uuid.UUID
	user     uuid.UUID
	device   uuid.UUID
	patient  uuid.UUID
	visit    uuid.UUID
	role     string
	held     []string
}

type staff struct {
	facility, user, device uuid.UUID
	permissions            *[]string
	role                   *string
}

func (s staff) Identify(context.Context, string) (httpx.Caller, error) {
	return httpx.Caller{
		UserID: s.user.String(), FacilityID: s.facility.String(),
		SessionID: uuid.NewSHA1(s.user, []byte("session")).String(),
		Code:      "H009", Permissions: *s.permissions, Roles: []string{*s.role},
	}, nil
}

func (s staff) Authorize(ctx context.Context, caller httpx.Caller, anyOf []string) (context.Context, httpx.AuthzDecision) {
	for _, want := range anyOf {
		for _, held := range caller.Permissions {
			if want == held {
				return httpx.WithPrincipal(ctx, httpx.Principal{
					UserID: caller.UserID, FacilityID: caller.FacilityID,
					SessionID: caller.SessionID, Code: caller.Code,
					DeviceID: s.device.String(),
					Role:     *s.role, Station: "STN_HISTORY",
				}), httpx.AuthzDecision{Allowed: true, Reason: "allowed"}
			}
		}
	}
	return ctx, httpx.AuthzDecision{Reason: "permission_not_held"}
}

func newAPI(t *testing.T) *api {
	t.Helper()
	base := testsupport.Postgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, base.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	h := &api{DB: base, user: uuid.New(), device: uuid.New(), role: "HISTORY"}
	h.held = []string{allergy.PermRead, allergy.PermWrite, "qa.review"}
	h.clock = clock.NewFixed(time.Date(2026, 9, 14, 4, 42, 0, 0, time.UTC))
	if err := base.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&h.facility); err != nil {
		t.Fatal(err)
	}

	events := eventstore.New(eventstore.Config{
		Pool: pool, Clock: h.clock, Synchronous: projection.NewSyncSet(projection.Default),
	})
	if err := projection.NewEngineWithEvents(pool, projection.Default, events).Register(ctx); err != nil {
		t.Fatal(err)
	}
	h.store = allergy.NewStore(pool)
	h.service = allergy.NewService(h.store, events, h.clock)
	h.seed(t)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handlers := allergy.NewHandlers(allergy.HandlersConfig{
		Service: h.service, Store: h.store, Clock: h.clock, Logger: logger,
	})
	who := staff{facility: h.facility, user: h.user, device: h.device,
		permissions: &h.held, role: &h.role}
	router, err := httpx.NewRouter(httpx.RouterOptions{
		Logger: logger, IDs: &ids.Sequential{Prefix: "req"},
		MaxBodyBytes: 1 << 16, RequestTimeout: 10 * time.Second,
		Health:        &httpx.Health{Service: "api", Version: "test", Logger: logger},
		Authenticator: who, Authorizer: who,
		Routes: func(r chi.Router) {
			handlers.Mount(r)
			r.Route("/patients", func(p chi.Router) { handlers.MountPatient(p) })
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.server = httptest.NewServer(router)
	t.Cleanup(h.server.Close)
	return h
}

func (h *api) seed(t *testing.T) {
	t.Helper()
	if _, err := h.SQL.Exec(`
		INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, $2, 'H009', 'Shahnaz Parvin', 'শাহনাজ পারভীন', 'active')`,
		h.user, h.facility); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO core.device (id, facility_id, name, kind, status, enrolled_at)
		VALUES ($1, $2, 'Tablet 5', 'tablet', 'active', now())`,
		h.device, h.facility); err != nil {
		t.Fatal(err)
	}
	h.patient = uuid.New()
	if _, err := h.SQL.Exec(`
		INSERT INTO core.patient (id, facility_id, clinical_id, name_en, sex, birth_date,
		                          dob_precision, dob_verified_by, phone_primary, status,
		                          registered_by, registered_at)
		VALUES ($1, $2, 'DTHC-FRD-2026-000777', 'Anwara Khatun', 'female', DATE '1968-11-03',
		        'day', 'national_id', '+8801711111777', 'active', $3, now())`,
		h.patient, h.facility, h.user); err != nil {
		t.Fatal(err)
	}
	h.visit = uuid.New()
	if _, err := h.SQL.Exec(`
		INSERT INTO core.visit (id, facility_id, patient_id, visit_code, visit_type, status,
		                        clinic_day, opened_at, opened_by)
		VALUES ($1, $2, $3, 'V-CP54-1', 'follow_up', 'open', current_date, now(), $4)`,
		h.visit, h.facility, h.patient, h.user); err != nil {
		t.Fatalf("seeding a visit: %v", err)
	}
}

// queue attempts to put the patient in the queue for a station, straight into the table.
//
// **Deliberately not through the API.** Criterion 4 is that no client can bypass the gate, and
// a test that only exercised the handler would be a test of the client that exists rather than
// of the ones that do not. This is what a support script looks like.
func (h *api) queue(t *testing.T, station string) error {
	t.Helper()
	_, err := h.SQL.Exec(`
		INSERT INTO core.queue_entry (id, facility_id, visit_id, patient_id, station_code,
		                              position, entered_at, clinic_day)
		VALUES ($1, $2, $3, $4, $5, 1, now(), current_date)`,
		uuid.New(), h.facility, h.visit, h.patient, station)
	return err
}

func (h *api) do(t *testing.T, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "DTHCMS")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	var decoded map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	return resp, decoded
}

func (h *api) state(t *testing.T) map[string]any {
	t.Helper()
	resp, decoded := h.do(t, http.MethodGet,
		"/v1/patients/"+h.patient.String()+"/allergies", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reading allergy state: %d %v", resp.StatusCode, decoded)
	}
	return decoded
}

func penicillin() map[string]any {
	return map[string]any{
		"code_system": "DTHC", "code_version": "1.0", "code": "ALLERGEN_PENICILLIN",
		"said": "the injection at the village pharmacy", "reaction": "SWELLING_FACE",
		"severity": "severe", "certainty": "confirmed",
	}
}

// ---------------------------------------------------------------------------
// Criteria 1 and 4: the gate
// ---------------------------------------------------------------------------

func TestAPatientWithNoAllergyStatusCannotPassTheHistoryStation(t *testing.T) {
	// The whole checkpoint in one test. Every station up to and including the history station
	// is reachable — that is where the status gets recorded — and nothing after it is.
	h := newAPI(t)

	for _, station := range []string{
		"STN_REGISTRATION", "STN_ANTHROPOMETRY", "STN_COUNSELING", "STN_HISTORY",
	} {
		if err := h.queue(t, station); err != nil {
			t.Errorf("%s should be reachable before the allergy question is asked: %v",
				station, err)
		}
	}
	for _, station := range []string{
		"STN_EXAMINATION", "STN_NUTRITION", "STN_CONSULTATION", "STN_RX_EDUCATION",
	} {
		if err := h.queue(t, station); err == nil {
			t.Errorf("%s was reachable with no allergy status; the hard stop is not hard",
				station)
		}
	}
}

func TestTheGateCannotBeBypassedByAnyClient(t *testing.T) {
	// Criterion 4, tested the only way it can honestly be tested: not through the API at all.
	// A `INSERT` straight into the queue is what a support script, a migration, or next year's
	// second client looks like, and the refusal has to reach them too.
	h := newAPI(t)

	err := h.queue(t, "STN_CONSULTATION")
	if err == nil {
		t.Fatal("a plain INSERT put a patient in front of the consultant with no allergy status")
	}
	// And the refusal says what to do, because whoever meets it is looking at a stack trace
	// rather than at a screen.
	if !contains(err.Error(), "allergy status is required") {
		t.Errorf("the refusal reads %q; it should name what is missing", err.Error())
	}
}

func TestAssertingNoKnownAllergiesOpensTheGate(t *testing.T) {
	// Five seconds of work, and the patient moves. The gate is absolute, not obstructive.
	h := newAPI(t)

	resp, decoded := h.do(t, http.MethodPost,
		"/v1/patients/"+h.patient.String()+"/allergies/assert",
		map[string]any{"kind": "NO_KNOWN_ALLERGY"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("asserting NKA: %d %v", resp.StatusCode, decoded)
	}
	if decoded["status"] != "NO_KNOWN_ALLERGY" || decoded["satisfied"] != true {
		t.Fatalf("the status after an assertion is %v", decoded["status"])
	}
	if err := h.queue(t, "STN_CONSULTATION"); err != nil {
		t.Errorf("the gate is still closed after an assertion: %v", err)
	}
}

func TestRecordingAnAllergyOpensTheGate(t *testing.T) {
	h := newAPI(t)

	resp, decoded := h.do(t, http.MethodPost,
		"/v1/patients/"+h.patient.String()+"/allergies", penicillin())
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("recording: %d %v", resp.StatusCode, decoded)
	}
	if decoded["status"] != "ALLERGIES_RECORDED" {
		t.Fatalf("the status is %v", decoded["status"])
	}
	if err := h.queue(t, "STN_EXAMINATION"); err != nil {
		t.Errorf("the gate is still closed after an allergy was recorded: %v", err)
	}
}

func TestUnableToAssessIsAllergyStatusAndNotAnOverride(t *testing.T) {
	// The third state, and why there is no fourth. It opens the gate — the unconscious patient
	// reaches the consultant — and it does *not* say the patient has no allergies, which is the
	// distinction the safety engine will depend on.
	h := newAPI(t)

	// It refuses to be a silent gap: the reason is the whole point of the state.
	resp, _ := h.do(t, http.MethodPost,
		"/v1/patients/"+h.patient.String()+"/allergies/assert",
		map[string]any{"kind": "UNABLE_TO_ASSESS"})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unable-to-assess with no reason answered %d", resp.StatusCode)
	}

	resp, decoded := h.do(t, http.MethodPost,
		"/v1/patients/"+h.patient.String()+"/allergies/assert",
		map[string]any{"kind": "UNABLE_TO_ASSESS", "reason": "drowsy, no attendant present"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("unable-to-assess: %d %v", resp.StatusCode, decoded)
	}
	if decoded["status"] != "UNABLE_TO_ASSESS" {
		t.Fatalf("the status is %v", decoded["status"])
	}
	if decoded["status"] == "NO_KNOWN_ALLERGY" {
		t.Error("unable-to-assess was recorded as a claim that there are no allergies")
	}
	if err := h.queue(t, "STN_CONSULTATION"); err != nil {
		t.Errorf("a patient who cannot be asked is stuck at station 4: %v", err)
	}
}

func TestThereIsNoEndpointThatBypassesTheGate(t *testing.T) {
	// The shape this would arrive in: somebody adds an override "for emergencies". The plan's
	// own risk note is that operators learn the shape of whatever clears the gate fastest, and
	// an override is the fastest thing there is.
	h := newAPI(t)

	for _, path := range []string{
		"/v1/patients/" + h.patient.String() + "/allergies/override",
		"/v1/patients/" + h.patient.String() + "/allergies/skip",
		"/v1/allergies/override",
	} {
		resp, _ := h.do(t, http.MethodPost, path, map[string]any{"reason": "emergency"})
		if resp.StatusCode < 400 {
			t.Errorf("%s answered %d; there must be no way past the gate", path, resp.StatusCode)
		}
	}
}

func TestWithdrawingTheLastStatusClosesTheGateAgain(t *testing.T) {
	// The case that would otherwise be silent: an officer takes back an NKA they tapped on the
	// wrong patient, and the record is back where it started. The response says so rather than
	// leaving the caller to assume the gate is still open.
	h := newAPI(t)

	_, decoded := h.do(t, http.MethodPost,
		"/v1/patients/"+h.patient.String()+"/allergies/assert",
		map[string]any{"kind": "NO_KNOWN_ALLERGY"})
	assertion, _ := decoded["assertion"].(map[string]any)
	id, _ := assertion["id"].(string)

	resp, after := h.do(t, http.MethodPost,
		"/v1/allergies/assertions/"+id+"/withdraw",
		map[string]any{"reason": "asserted on the wrong patient"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("withdrawing: %d %v", resp.StatusCode, after)
	}
	if after["status"] != "NONE_RECORDED" || after["satisfied"] != false {
		t.Fatalf("after withdrawing the only assertion the status is %v", after["status"])
	}
	if err := h.queue(t, "STN_CONSULTATION"); err == nil {
		t.Error("the gate stayed open after the only allergy status was withdrawn")
	}
}

// ---------------------------------------------------------------------------
// Criterion 2: NKA is an explicit, attributed assertion
// ---------------------------------------------------------------------------

func TestNoKnownAllergiesNamesWhoSaidIt(t *testing.T) {
	h := newAPI(t)

	_, decoded := h.do(t, http.MethodPost,
		"/v1/patients/"+h.patient.String()+"/allergies/assert",
		map[string]any{"kind": "NO_KNOWN_ALLERGY"})
	assertion, _ := decoded["assertion"].(map[string]any)
	if assertion["asserted_by"] != h.user.String() {
		t.Errorf("asserted by %v", assertion["asserted_by"])
	}
	if assertion["asserted_role"] != "HISTORY" {
		t.Errorf("asserted by role %v", assertion["asserted_role"])
	}
}

func TestNothingAssertsOnAClinicianBehalf(t *testing.T) {
	// Criterion 2's real failure mode: not a bad row, but a column default added to make
	// something pass, after which every patient asserts and none of them was asked.
	h := newAPI(t)

	if _, err := h.SQL.Exec(
		`SELECT core.assert_no_known_allergies_is_always_asserted()`); err != nil {
		t.Errorf("the assertion invariant fails on a clean record: %v", err)
	}

	var defaulted int
	if err := h.SQL.QueryRow(`
		SELECT count(*) FROM information_schema.columns
		 WHERE table_schema = 'read' AND table_name = 'allergy_assertion'
		   AND column_name IN ('kind', 'asserted_by')
		   AND column_default IS NOT NULL`).Scan(&defaulted); err != nil {
		t.Fatal(err)
	}
	if defaulted != 0 {
		t.Error("an assertion column has a default; criterion 2 is silently satisfied and destroyed")
	}
}

func TestNoKnownAllergiesRefusesAReason(t *testing.T) {
	// Text nobody will ever read, answering a question nobody asked. Refused rather than
	// stored, because a field that accepts anything is a field somebody will use for the wrong
	// thing — and "patient seemed unsure" belongs in unable-to-assess.
	h := newAPI(t)

	resp, _ := h.do(t, http.MethodPost,
		"/v1/patients/"+h.patient.String()+"/allergies/assert",
		map[string]any{"kind": "NO_KNOWN_ALLERGY", "reason": "she seemed unsure"})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("a reason on NKA answered %d", resp.StatusCode)
	}
}

func TestANewAssertionSupersedesTheOldOne(t *testing.T) {
	// Both cannot be the current answer, and a status function picking between them by
	// timestamp is a coin toss waiting to happen.
	h := newAPI(t)

	h.do(t, http.MethodPost, "/v1/patients/"+h.patient.String()+"/allergies/assert",
		map[string]any{"kind": "NO_KNOWN_ALLERGY"})
	h.clock.Advance(time.Minute)
	_, decoded := h.do(t, http.MethodPost,
		"/v1/patients/"+h.patient.String()+"/allergies/assert",
		map[string]any{"kind": "UNABLE_TO_ASSESS", "reason": "she has gone home"})
	if decoded["status"] != "UNABLE_TO_ASSESS" {
		t.Fatalf("the status is %v", decoded["status"])
	}

	var live int
	if err := h.SQL.QueryRow(`
		SELECT count(*) FROM read.allergy_assertion
		 WHERE patient_id = $1 AND withdrawn_at IS NULL`, h.patient).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 1 {
		t.Errorf("%d assertions are live at once", live)
	}
}

func TestRecordingAnAllergyDoesNotEraseAnEarlierAssertion(t *testing.T) {
	// Both are true statements about their own moment: somebody asked in March and was told
	// there were none, and somebody found one in June. Withdrawing the March row would delete
	// the fact that anybody asked.
	h := newAPI(t)

	h.do(t, http.MethodPost, "/v1/patients/"+h.patient.String()+"/allergies/assert",
		map[string]any{"kind": "NO_KNOWN_ALLERGY"})
	h.clock.Advance(90 * 24 * time.Hour)
	h.do(t, http.MethodPost, "/v1/patients/"+h.patient.String()+"/allergies", penicillin())

	state := h.state(t)
	if state["status"] != "ALLERGIES_RECORDED" {
		t.Errorf("a recorded allergy did not outrank the assertion: %v", state["status"])
	}
	var kept int
	if err := h.SQL.QueryRow(`
		SELECT count(*) FROM read.allergy_assertion WHERE patient_id = $1`,
		h.patient).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if kept != 1 {
		t.Error("the earlier assertion was erased rather than outranked")
	}
}

// ---------------------------------------------------------------------------
// Criterion 3: legible, everywhere
// ---------------------------------------------------------------------------

func TestTheWorstAllergyComesFirst(t *testing.T) {
	// A header showing three allergies shows the one that stops a heart at the top, rather
	// than burying it under a rash from 1998.
	h := newAPI(t)

	h.do(t, http.MethodPost, "/v1/patients/"+h.patient.String()+"/allergies",
		map[string]any{"said": "dust", "reaction": "ITCHING",
			"severity": "mild", "certainty": "suspected"})
	h.clock.Advance(time.Minute)
	h.do(t, http.MethodPost, "/v1/patients/"+h.patient.String()+"/allergies",
		map[string]any{"code_system": "DTHC", "code_version": "1.0",
			"code": "ALLERGEN_PENICILLIN", "reaction": "ANAPHYLAXIS",
			"severity": "life_threatening", "certainty": "confirmed"})

	state := h.state(t)
	list, _ := state["allergies"].([]any)
	if len(list) != 2 {
		t.Fatalf("expected two allergies, got %d", len(list))
	}
	first, _ := list[0].(map[string]any)
	if first["reaction"] != "ANAPHYLAXIS" {
		t.Errorf("the list leads with %v", first["reaction"])
	}
	if first["is_emergency"] != true {
		t.Error("an anaphylaxis is not marked as an emergency")
	}
}

func TestAnEmptyListAndNobodyHavingAskedAreDifferentAnswers(t *testing.T) {
	// The reason the status is returned as well as the list. A header that drew both as blank
	// would be lying about one of them, and it would be lying in the safe-looking direction.
	h := newAPI(t)

	before := h.state(t)
	if before["status"] != "NONE_RECORDED" || before["satisfied"] != false {
		t.Fatalf("an unasked patient reads as %v", before["status"])
	}
	list, _ := before["allergies"].([]any)
	if len(list) != 0 {
		t.Fatal("an unasked patient has allergies")
	}

	h.do(t, http.MethodPost, "/v1/patients/"+h.patient.String()+"/allergies/assert",
		map[string]any{"kind": "NO_KNOWN_ALLERGY"})
	after := h.state(t)
	list, _ = after["allergies"].([]any)
	if len(list) != 0 {
		t.Fatal("an NKA patient has allergies")
	}
	// Same empty list, opposite meaning — and the two are told apart by the status alone.
	if after["status"] == before["status"] {
		t.Error("no-allergies and nobody-asked read the same")
	}
}

func TestAnUncodedAllergyIsStillRecordedAndStillLegible(t *testing.T) {
	// The escape hatch matters more here than anywhere else in the system: an allergy nobody
	// could code is far more dangerous in a note field than it is on the header, marked.
	h := newAPI(t)

	resp, decoded := h.do(t, http.MethodPost, "/v1/patients/"+h.patient.String()+"/allergies",
		map[string]any{"said": "a yellow tablet from the pharmacy near the bridge",
			"reaction": "RASH", "severity": "moderate", "certainty": "suspected"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("%d %v", resp.StatusCode, decoded)
	}
	list, _ := decoded["allergies"].([]any)
	first, _ := list[0].(map[string]any)
	if first["code"] != nil {
		t.Errorf("an uncoded allergy came back with a code: %v", first)
	}
	if first["said"] == "" || first["reaction_en"] == "" {
		t.Errorf("an uncoded allergy is not legible: %v", first)
	}
}

func TestAnAllergyThatNamesNothingIsRefused(t *testing.T) {
	// It would produce a warning nobody can act on, and warnings nobody can act on are how a
	// clinic learns to click past the ones that matter.
	h := newAPI(t)

	resp, _ := h.do(t, http.MethodPost, "/v1/patients/"+h.patient.String()+"/allergies",
		map[string]any{"reaction": "RASH", "severity": "mild", "certainty": "suspected"})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("an allergy naming no substance answered %d", resp.StatusCode)
	}
}

func TestAReactionOutsideTheVocabularyIsRefused(t *testing.T) {
	h := newAPI(t)

	resp, _ := h.do(t, http.MethodPost, "/v1/patients/"+h.patient.String()+"/allergies",
		map[string]any{"said": "prawn", "reaction": "FELT_FUNNY",
			"severity": "mild", "certainty": "suspected"})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("an unknown reaction answered %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// The change history, and the QA view
// ---------------------------------------------------------------------------

func TestAWithdrawnAllergyStaysInTheChangeHistory(t *testing.T) {
	// Somebody believed it and somebody else disagreed. Both halves are worth reading before
	// writing a prescription, which is why nothing here is deleted.
	h := newAPI(t)

	_, decoded := h.do(t, http.MethodPost, "/v1/patients/"+h.patient.String()+"/allergies",
		penicillin())
	list, _ := decoded["allergies"].([]any)
	first, _ := list[0].(map[string]any)
	id, _ := first["id"].(string)

	h.clock.Advance(time.Hour)
	h.do(t, http.MethodPost, "/v1/allergies/"+id+"/withdraw",
		map[string]any{"reason": "the patient was describing her sister"})

	state := h.state(t)
	live, _ := state["allergies"].([]any)
	if len(live) != 0 {
		t.Error("a withdrawn allergy is still on the header")
	}

	resp, history := h.do(t, http.MethodGet,
		"/v1/patients/"+h.patient.String()+"/allergies/history", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, history)
	}
	changes, _ := history["changes"].([]any)
	if len(changes) != 1 {
		t.Fatalf("the change history holds %d entries", len(changes))
	}
	change, _ := changes[0].(map[string]any)
	if change["undone_at"] == nil || change["undone_why"] == "" {
		t.Errorf("the withdrawal left no trace: %v", change)
	}
}

func TestTheAssertionRateIsVisibleToQa(t *testing.T) {
	// The plan's own mitigation for the risk it names. A view, not a rule.
	h := newAPI(t)

	h.do(t, http.MethodPost, "/v1/patients/"+h.patient.String()+"/allergies/assert",
		map[string]any{"kind": "NO_KNOWN_ALLERGY"})

	resp, decoded := h.do(t, http.MethodGet, "/v1/allergies/assertion-rates", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, decoded)
	}
	operators, _ := decoded["operators"].([]any)
	if len(operators) != 1 {
		t.Fatalf("expected one operator, got %d", len(operators))
	}
	row, _ := operators[0].(map[string]any)
	if row["asserted_by"] != h.user.String() || row["no_known"] != float64(1) {
		t.Errorf("the rate row is %v", row)
	}
}

func TestWritingNeedsMoreThanReading(t *testing.T) {
	h := newAPI(t)
	h.held = []string{allergy.PermRead}

	if resp, _ := h.do(t, http.MethodPost,
		"/v1/patients/"+h.patient.String()+"/allergies/assert",
		map[string]any{"kind": "NO_KNOWN_ALLERGY"}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("asserting with only a read permission answered %d", resp.StatusCode)
	}
	// And reading it is not blinded: the pharmacist holds exactly this permission and nothing
	// else clinical, because an allergy has to reach whoever hands over the medicine.
	if resp, _ := h.do(t, http.MethodGet,
		"/v1/patients/"+h.patient.String()+"/allergies", nil); resp.StatusCode != http.StatusOK {
		t.Errorf("reading with the read permission answered %d", resp.StatusCode)
	}
}

func TestTheStandingInvariantsHold(t *testing.T) {
	h := newAPI(t)
	h.do(t, http.MethodPost, "/v1/patients/"+h.patient.String()+"/allergies", penicillin())

	for _, fn := range []string{
		"core.assert_no_known_allergies_is_always_asserted",
		"core.assert_the_allergy_gate_is_wired",
		"core.assert_every_allergy_is_legible",
	} {
		if _, err := h.SQL.Exec(`SELECT ` + fn + `()`); err != nil {
			t.Errorf("%s: %v", fn, err)
		}
	}
}

func TestTheGateInvariantNoticesTheTriggerGoingMissing(t *testing.T) {
	// The invariant that guards the gate, tested by taking the gate away. A migration that
	// dropped the trigger and kept the function would otherwise leave a clinic with no hard
	// stop and no error — which is the worst of the available outcomes, because everybody
	// would still believe there was one.
	h := newAPI(t)

	tx, err := h.SQL.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(
		`DROP TRIGGER allergy_status_gates_the_queue ON core.queue_entry`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`SELECT core.assert_the_allergy_gate_is_wired()`); err == nil {
		t.Error("the gate was removed and the invariant said nothing")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) &&
		(haystack == needle || len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
