package clinical_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
)

// The observation model and the units framework (CP42, §6, §11).
//
// Four acceptance criteria, and the first is the one the whole checkpoint exists for: a
// unit-bearing observation cannot be stored without a valid unit. It is asserted three ways
// here — through the API, through the database directly, and through the standing invariant
// — because "cannot be stored" has to mean cannot, not "the handler refuses it".

type api struct {
	*testsupport.DB
	store    *clinical.Store
	service  *clinical.Service
	server   *httptest.Server
	clock    *clock.Fixed
	facility uuid.UUID
	user     uuid.UUID
	device   uuid.UUID
	patient  uuid.UUID
	role     string
}

type staff struct {
	facility, user, device uuid.UUID
	permissions            []string
	role                   *string
}

func (s staff) Identify(context.Context, string) (httpx.Caller, error) {
	return httpx.Caller{
		UserID: s.user.String(), FacilityID: s.facility.String(),
		SessionID: uuid.NewSHA1(s.user, []byte("session")).String(),
		Code:      "A014", Permissions: s.permissions, Roles: []string{*s.role},
	}, nil
}

func (s staff) Authorize(ctx context.Context, caller httpx.Caller, anyOf []string) (context.Context, httpx.AuthzDecision) {
	for _, want := range anyOf {
		for _, held := range caller.Permissions {
			if want == held {
				return httpx.WithPrincipal(ctx, httpx.Principal{
					UserID: caller.UserID, FacilityID: caller.FacilityID,
					SessionID: caller.SessionID, Code: caller.Code,
					DeviceID: s.device.String(), Role: *s.role,
					Station: "STN_ANTHROPOMETRY",
				}), httpx.AuthzDecision{Allowed: true, Reason: "allowed"}
			}
		}
	}
	return ctx, httpx.AuthzDecision{Reason: "permission_not_held"}
}

func newAPI(t *testing.T, permissions ...string) *api {
	t.Helper()
	if len(permissions) == 0 {
		permissions = []string{
			"observation.read.values", "observation.write.anthro",
			"observation.write.vitals", "observation.write.history",
			// The examination findings moved to their own permission at CP51: a foot
			// examination happens at station 5, and the history officer at station 4 does
			// not have the patient's shoes off.
			"observation.write.exam",
			"observation.correct.request",
		}
	}
	base := testsupport.Postgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, base.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	h := &api{DB: base, user: uuid.New(), device: uuid.New(), role: "ANTHROPOMETRY"}
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
	h.store = clinical.NewStore(pool)
	h.service = clinical.NewService(h.store, events, h.clock)
	h.seed(t)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handlers := clinical.NewHandlers(clinical.HandlersConfig{
		Service: h.service, Store: h.store, Clock: h.clock, Logger: logger,
	})
	who := staff{facility: h.facility, user: h.user, device: h.device,
		permissions: permissions, role: &h.role}
	router, err := httpx.NewRouter(httpx.RouterOptions{
		Logger: logger, IDs: &ids.Sequential{Prefix: "req"},
		MaxBodyBytes: 1 << 16, RequestTimeout: 10 * time.Second,
		Health:        &httpx.Health{Service: "api", Version: "test", Logger: logger},
		Authenticator: who, Authorizer: who,
		Routes: func(r chi.Router) {
			handlers.Mount(r)
			// The alert surface is mounted for every test in this package, not only the
			// CP50 ones: an alert is raised by an ordinary write, and a test that could not
			// see the alerts a write produced would be a test that quietly stopped noticing
			// them.
			handlers.MountAlerts(r)
			r.Route("/patients", func(p chi.Router) {
				handlers.MountPatient(p)
				handlers.MountPatientAlerts(p)
			})
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
		VALUES ($1, $2, 'A014', 'Shirin Akter', 'শিরীন আক্তার', 'active')`,
		h.user, h.facility); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO core.device (id, facility_id, name, kind, status, enrolled_at)
		VALUES ($1, $2, 'Tablet 1', 'tablet', 'active', now())`, h.device, h.facility); err != nil {
		t.Fatal(err)
	}
	h.patient = uuid.New()
	if _, err := h.SQL.Exec(`
		INSERT INTO core.patient (id, facility_id, clinical_id, name_en, sex, birth_date,
		                          dob_precision, dob_verified_by, phone_primary, status,
		                          registered_by, registered_at)
		VALUES ($1, $2, 'DTHC-FRD-2026-000201', 'Md Rahim Uddin', 'male', DATE '1985-06-14',
		        'day', 'national_id', '+8801711111101', 'active', $3, now())`,
		h.patient, h.facility, h.user); err != nil {
		t.Fatal(err)
	}
}

func (h *api) call(t *testing.T, method, path string, body any) (*http.Response, map[string]any) {
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
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test")
	req.Header.Set("X-Requested-With", "DTHCMS")
	req.Header.Set("X-Active-Role", h.role)
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp, out
}

func (h *api) record(t *testing.T, body map[string]any) (*http.Response, map[string]any) {
	t.Helper()
	if _, ok := body["event_id"]; !ok {
		body["event_id"] = uuid.Must(uuid.NewV7()).String()
	}
	if _, ok := body["patient_id"]; !ok {
		body["patient_id"] = h.patient.String()
	}
	return h.call(t, http.MethodPost, "/v1/observations", body)
}

// fieldsOf digs the per-field messages out of the error envelope. They live under `error`,
// which is easy to get wrong in a test and then assert nothing at all.
func fieldsOf(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	envelope, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("no error envelope in %v", body)
	}
	fields, _ := envelope["fields"].(map[string]any)
	return fields
}

func observationOf(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	row, ok := body["observation"].(map[string]any)
	if !ok {
		t.Fatalf("no observation in %v", body)
	}
	return row
}

// --- criterion 1 ---

func TestAUnitBearingValueCannotBeStoredWithoutItsUnit(t *testing.T) {
	// The criterion, and the whole reason this checkpoint exists. A weight of 154 with no
	// unit is 154 kg or 154 lb depending on who reads it, and a dose computed from it is
	// wrong by a factor of 2.2.
	h := newAPI(t)

	resp, body := h.record(t, map[string]any{"code": "BODY_WEIGHT", "value": 69.5})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a weight with no unit answered %d: %v", resp.StatusCode, body)
	}

	// And in the database, which is where "cannot" has to be true. The API is one path in;
	// a projection rebuild and a hand-written INSERT are others.
	_, err := h.SQL.Exec(`
		INSERT INTO read.observation
		  (id, facility_id, patient_id, code, category, value_type,
		   effective_at, recorded_at, source, recorded_by, recorded_role, event_id, global_seq)
		VALUES (gen_random_uuid(), $1, $2, 'BODY_WEIGHT', 'ANTHRO', 'numeric',
		        now(), now(), 'STATION', $3, 'ANTHROPOMETRY', gen_random_uuid(), 1)`,
		h.facility, h.patient, h.user)
	if err == nil {
		t.Error("the database accepted a weight with no unit")
	}
}

func TestAValueCannotBeRecordedInAUnitThatMeasuresSomethingElse(t *testing.T) {
	// Not a conversion problem. A weight in centimetres is a different measurement, and
	// "convert it" is the wrong instinct — there is nothing to convert.
	h := newAPI(t)

	resp, body := h.record(t, map[string]any{
		"code": "BODY_WEIGHT", "value": 69.5, "unit": "cm",
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a weight in centimetres answered %d: %v", resp.StatusCode, body)
	}
	if _, named := fieldsOf(t, body)["unit"]; !named {
		t.Errorf("the refusal does not name the unit field: %v", body)
	}
}

func TestAUnitlessValueIsRefusedAUnit(t *testing.T) {
	h := newAPI(t)
	// FOOT_ULCER_PRESENT is an examination finding, recorded at station 5 since CP51.
	h.role = "CLINICAL_ASSISTANT"

	resp, _ := h.record(t, map[string]any{
		"code": "FOOT_ULCER_PRESENT", "value_bool": true, "unit": "kg",
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("a boolean finding with a unit answered %d", resp.StatusCode)
	}
}

// --- criterion 2 ---

func TestAValueEnteredInPoundsComesBackInPoundsAndIsStoredInKilograms(t *testing.T) {
	// Criterion 2, and the design decision behind it: both values are stored. Reading the
	// value back in the unit it was entered in is a *read*, not a round trip — so 154 lb
	// comes back as exactly 154, not as 69.85 kg converted back to 153.99 lb.
	h := newAPI(t)

	resp, body := h.record(t, map[string]any{
		"code": "BODY_WEIGHT", "value": 154, "unit": "[lb_av]",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("recording a weight in pounds: %d %v", resp.StatusCode, body)
	}
	row := observationOf(t, body)

	if row["entered_value"] != 154.0 || row["entered_unit"] != "[lb_av]" {
		t.Errorf("the entered pair came back as %v %v", row["entered_value"], row["entered_unit"])
	}
	if row["unit"] != "kg" {
		t.Errorf("stored in %v, wanted kg", row["unit"])
	}
	canonical := row["value"].(float64)
	if math.Abs(canonical-69.85322) > 0.0001 {
		t.Errorf("154 lb stored as %v kg", canonical)
	}
}

func TestEveryUnitRoundTripsThroughTheDatabase(t *testing.T) {
	// Criterion 2 over the whole table rather than one example. The documented tolerance is
	// 1e-6; `numeric` makes it exact for every factor in the table today, and this is what
	// catches the day somebody adds a unit with a repeating factor.
	h := newAPI(t)

	rows, err := h.SQL.Query(`SELECT code FROM core.unit ORDER BY code`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()

	checked := 0
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			t.Fatal(err)
		}
		for _, value := range []float64{0, 1, 37.2, 154, 1234.5} {
			var back float64
			if err := h.SQL.QueryRow(
				`SELECT core.from_canonical(core.to_canonical($1::numeric, $2), $2)`,
				value, code).Scan(&back); err != nil {
				t.Fatalf("%s: %v", code, err)
			}
			if math.Abs(back-value) > 1e-6 {
				t.Errorf("%s: %v round-tripped to %v", code, value, back)
			}
		}
		checked++
	}
	if checked < 20 {
		t.Fatalf("only %d units were checked; the table should hold every unit the clinic uses", checked)
	}

	// And the standing invariant says the same thing, so a deployment fails rather than a
	// patient's weight three months later.
	if _, err := h.SQL.Exec(`SELECT core.assert_units_convert_both_ways()`); err != nil {
		t.Errorf("the invariant fails: %v", err)
	}
}

func TestFahrenheitConvertsWithItsOffset(t *testing.T) {
	// The one offset conversion in the table, and the reason the column exists. 98.6 °F is
	// 37 °C, and a factor-only conversion would make it 54.8.
	h := newAPI(t)
	h.role = "CLINICAL_ASSISTANT"

	resp, body := h.record(t, map[string]any{
		"code": "BODY_TEMP", "value": 98.6, "unit": "[degF]",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("recording a temperature in Fahrenheit: %d %v", resp.StatusCode, body)
	}
	row := observationOf(t, body)
	if got := row["value"].(float64); math.Abs(got-37.0) > 0.01 {
		t.Errorf("98.6 °F stored as %v °C", got)
	}
	if row["entered_value"] != 98.6 {
		t.Errorf("the entered value came back as %v", row["entered_value"])
	}
}

// --- criterion 3 ---

func TestAllSevenCategoriesAreSupported(t *testing.T) {
	h := newAPI(t)

	resp, body := h.call(t, http.MethodGet, "/v1/observations/codes", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reading the registry: %d %v", resp.StatusCode, body)
	}
	seen := map[string]int{}
	shapes := map[string]bool{}
	for _, raw := range body["codes"].([]any) {
		code := raw.(map[string]any)
		seen[code["category"].(string)]++
		shapes[code["value_type"].(string)] = true
	}
	for _, category := range []string{"ANTHRO", "VITAL", "EXAM", "LAB", "DERIVED", "SCREENING", "PRO"} {
		if seen[category] == 0 {
			t.Errorf("no codes in category %s", category)
		}
	}
	// And all five shapes, which is what makes the model general rather than a numbers
	// table with extra columns.
	for _, shape := range []string{"numeric", "text", "boolean", "coded", "structured"} {
		if !shapes[shape] {
			t.Errorf("no code of value type %s", shape)
		}
	}
}

func TestTheRegistryOffersEveryUnitAValueMayBeEnteredIn(t *testing.T) {
	// What makes offline validation possible on a tablet: one fetch, and the app knows
	// every unit each code accepts without another round trip.
	h := newAPI(t)

	_, body := h.call(t, http.MethodGet, "/v1/observations/codes", nil)
	for _, raw := range body["codes"].([]any) {
		code := raw.(map[string]any)
		if code["code"] != "BODY_WEIGHT" {
			continue
		}
		units := map[string]bool{}
		for _, u := range code["units"].([]any) {
			units[u.(map[string]any)["code"].(string)] = true
		}
		if !units["kg"] || !units["[lb_av]"] || !units["g"] {
			t.Errorf("BODY_WEIGHT offers %v", units)
		}
		if code["canonical_unit"] != "kg" {
			t.Errorf("BODY_WEIGHT is canonical in %v", code["canonical_unit"])
		}
		return
	}
	t.Fatal("BODY_WEIGHT is not in the registry")
}

// --- criterion 4 ---

func TestEveryObservationCarriesItsSourceAndItsAttribution(t *testing.T) {
	h := newAPI(t)

	resp, body := h.record(t, map[string]any{
		"code": "BODY_HEIGHT", "value": 168, "unit": "cm", "note": "measured without shoes",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("recording: %d %v", resp.StatusCode, body)
	}
	row := observationOf(t, body)

	if row["source"] != "STATION" {
		t.Errorf("source is %v", row["source"])
	}
	if row["recorded_by"] != h.user.String() {
		t.Errorf("attributed to %v", row["recorded_by"])
	}
	// The role at write time, from the envelope and never from the body. This is what makes
	// CP41's role switching answerable years later.
	if row["recorded_role"] != "ANTHROPOMETRY" {
		t.Errorf("recorded under role %v", row["recorded_role"])
	}
	if row["station_code"] != "STN_ANTHROPOMETRY" {
		t.Errorf("recorded at station %v", row["station_code"])
	}
	if row["note"] != "measured without shoes" {
		t.Errorf("the note is %v", row["note"])
	}
}

func TestAPatientReportedValueIsMarkedAsSuch(t *testing.T) {
	// Criterion 4's other half. A number a patient reported at home and a number an operator
	// measured on a calibrated scale are different evidence, and a physician deciding a dose
	// deserves to know which.
	h := newAPI(t)

	_, body := h.record(t, map[string]any{
		"code": "BODY_WEIGHT", "value": 70, "unit": "kg", "source": "PATIENT",
	})
	if got := observationOf(t, body)["source"]; got != "PATIENT" {
		t.Errorf("source is %v, wanted PATIENT", got)
	}
}

// --- the rest ---

func TestAnImplausibleValueIsRefused(t *testing.T) {
	// Plausibility, not clinical judgement: this is the band outside which a number is a
	// typing error. Critical values are CP50.
	h := newAPI(t)

	for _, bad := range []map[string]any{
		{"code": "BODY_WEIGHT", "value": 900, "unit": "kg"},
		{"code": "BODY_HEIGHT", "value": 3, "unit": "cm"},
		{"code": "BODY_WEIGHT", "value": 900000, "unit": "g"}, // implausible after conversion
	} {
		resp, body := h.record(t, bad)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%v answered %d: %v", bad, resp.StatusCode, body)
		}
	}

	// And the band is applied to the *canonical* value, so entering the same implausible
	// weight in pounds is refused too.
	resp, _ := h.record(t, map[string]any{"code": "BODY_WEIGHT", "value": 2000, "unit": "[lb_av]"})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("2000 lb answered %d", resp.StatusCode)
	}
}

func TestARoleMayOnlyRecordItsOwnCategory(t *testing.T) {
	// §4.4, and [R-02]. The permission checked is the **active role's**, not the union: an
	// operator who holds both hats must not record a blood pressure while wearing the
	// anthropometry one, because the event would be attributed to a role not allowed to
	// have taken it.
	h := newAPI(t)
	h.role = "ANTHROPOMETRY"

	resp, _ := h.record(t, map[string]any{
		"code": "BP_SYSTOLIC", "value": 128, "unit": "mm[Hg]",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("an anthropometry officer recorded a blood pressure: %d", resp.StatusCode)
	}

	// The same person, the same session, a different hat: allowed.
	h.role = "CLINICAL_ASSISTANT"
	resp, body := h.record(t, map[string]any{
		"code": "BP_SYSTOLIC", "value": 128, "unit": "mm[Hg]",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("a clinical assistant could not record a blood pressure: %d %v", resp.StatusCode, body)
	}
}

func TestTheWrongShapeOfValueIsRefused(t *testing.T) {
	h := newAPI(t)

	// The hat matters: the permission is checked before the shape is, which is the right
	// order — telling somebody their value is the wrong shape when they were never allowed
	// to record it at all sends them to the wrong place.
	for _, bad := range []struct {
		role string
		body map[string]any
	}{
		{"ANTHROPOMETRY", map[string]any{"code": "BODY_WEIGHT", "value_text": "about seventy kilos"}},
		{"CLINICAL_ASSISTANT", map[string]any{"code": "FOOT_ULCER_PRESENT", "value": 1}},
		{"ANTHROPOMETRY", map[string]any{"code": "BODY_WEIGHT", "value": 70, "unit": "kg", "value_text": "seventy"}},
		{"ANTHROPOMETRY", map[string]any{"code": "BODY_WEIGHT"}},
	} {
		h.role = bad.role
		resp, body := h.record(t, bad.body)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%v answered %d: %v", bad.body, resp.StatusCode, body)
		}
	}
}

func TestCorrectingAValueKeepsBothAndSaysWhichIsWhich(t *testing.T) {
	// CP35's rule, applied to values: nothing is deleted. The earlier row stops being the
	// value and says which row took its place.
	h := newAPI(t)

	_, first := h.record(t, map[string]any{"code": "BODY_WEIGHT", "value": 96.5, "unit": "kg"})
	original := observationOf(t, first)["id"].(string)

	resp, second := h.record(t, map[string]any{
		"code": "BODY_WEIGHT", "value": 69.5, "unit": "kg",
		"replaces": original, "replaced_status": "CORRECTED",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("correcting: %d %v", resp.StatusCode, second)
	}
	replacement := observationOf(t, second)["id"].(string)

	_, current := h.call(t, http.MethodGet, "/v1/observations/"+original, nil)
	earlier := observationOf(t, current)
	if earlier["status"] != "CORRECTED" {
		t.Errorf("the earlier row is %v", earlier["status"])
	}
	if earlier["replaced_by"] != replacement {
		t.Errorf("the earlier row points at %v", earlier["replaced_by"])
	}

	// The current value list shows one weight, not two.
	_, list := h.call(t, http.MethodGet,
		"/v1/patients/"+h.patient.String()+"/observations?category=ANTHRO", nil)
	weights := 0
	for _, raw := range list["observations"].([]any) {
		if raw.(map[string]any)["code"] == "BODY_WEIGHT" {
			weights++
		}
	}
	if weights != 1 {
		t.Errorf("the current values show %d weights", weights)
	}

	// And the history shows both, which is what a trend line and an investigator read.
	_, history := h.call(t, http.MethodGet,
		"/v1/patients/"+h.patient.String()+"/observations/BODY_WEIGHT/history", nil)
	if got := len(history["observations"].([]any)); got != 2 {
		t.Errorf("the history holds %d rows, wanted 2", got)
	}
}

func TestSupersedingIsNotCorrecting(t *testing.T) {
	// Two different facts. A report that conflated them would count a re-measurement as an
	// error rate, which is the kind of number that ends up in a quality review.
	h := newAPI(t)

	_, first := h.record(t, map[string]any{"code": "BODY_WEIGHT", "value": 69.5, "unit": "kg"})
	original := observationOf(t, first)["id"].(string)

	if resp, body := h.record(t, map[string]any{
		"code": "BODY_WEIGHT", "value": 69.8, "unit": "kg",
		"replaces": original, "replaced_status": "SUPERSEDED",
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("superseding: %d %v", resp.StatusCode, body)
	}

	_, current := h.call(t, http.MethodGet, "/v1/observations/"+original, nil)
	if got := observationOf(t, current)["status"]; got != "SUPERSEDED" {
		t.Errorf("the earlier row is %v, wanted SUPERSEDED", got)
	}
}

func TestAValueCannotBeCorrectedTwice(t *testing.T) {
	// A fork in the record: two rows each claiming to replace the same one, and nothing to
	// say which is current.
	h := newAPI(t)

	_, first := h.record(t, map[string]any{"code": "BODY_WEIGHT", "value": 96.5, "unit": "kg"})
	original := observationOf(t, first)["id"].(string)

	h.record(t, map[string]any{
		"code": "BODY_WEIGHT", "value": 69.5, "unit": "kg", "replaces": original,
	})
	resp, _ := h.record(t, map[string]any{
		"code": "BODY_WEIGHT", "value": 70.5, "unit": "kg", "replaces": original,
	})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("correcting a corrected value answered %d, wanted 409", resp.StatusCode)
	}
}

func TestEffectiveTimeAndRecordTimeAreDifferentFacts(t *testing.T) {
	// A blood pressure taken at 09:05 and entered at 09:20 has two times. A timeline that
	// used the second would order it wrongly beside a promptly-entered reading — and a lab
	// value transcribed from Tuesday's report is not a value from today.
	h := newAPI(t)
	taken := h.clock.Now().Add(-72 * time.Hour).UTC()

	_, body := h.record(t, map[string]any{
		"code": "BODY_WEIGHT", "value": 69.5, "unit": "kg",
		"effective_at": taken.Format(time.RFC3339),
	})
	row := observationOf(t, body)

	effective, err := time.Parse(time.RFC3339, row["effective_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := time.Parse(time.RFC3339, row["recorded_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if !effective.Equal(taken) {
		t.Errorf("effective_at is %s, wanted %s", effective, taken)
	}
	if !recorded.After(effective) {
		t.Errorf("recorded_at %s is not after effective_at %s", recorded, effective)
	}
}

func TestTheInvariantsHold(t *testing.T) {
	h := newAPI(t)
	h.record(t, map[string]any{"code": "BODY_WEIGHT", "value": 154, "unit": "[lb_av]"})
	h.record(t, map[string]any{"code": "BODY_HEIGHT", "value": 66, "unit": "in"})
	h.role = "CLINICAL_ASSISTANT"
	h.record(t, map[string]any{"code": "BODY_TEMP", "value": 98.6, "unit": "[degF]"})
	h.role = "HISTORY"
	h.record(t, map[string]any{"code": "FOOT_ULCER_PRESENT", "value_bool": false})

	for _, invariant := range []string{
		"core.assert_measurements_carry_their_units()",
		"core.assert_units_convert_both_ways()",
	} {
		if _, err := h.SQL.Exec("SELECT " + invariant); err != nil {
			t.Errorf("%s: %v", invariant, err)
		}
	}
}

func TestEveryCodeDeclaresAKnownPermission(t *testing.T) {
	// The route guard asks for the union of the write permissions; each code names one of
	// them. A code naming a permission outside that union would be unwritable through the
	// endpoint, and the failure would look like a 403 nobody could explain.
	h := newAPI(t)

	known := map[string]bool{
		"observation.write.anthro": true, "observation.write.vitals": true,
		"observation.write.lifestyle": true, "observation.write.history": true,
		"observation.write.nutrition": true, "observation.write.exercise": true,
		"observation.write.exam": true,
	}
	rows, err := h.SQL.Query(
		`SELECT code, write_permission FROM core.observation_code ORDER BY code`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var code, permission string
		if err := rows.Scan(&code, &permission); err != nil {
			t.Fatal(err)
		}
		if !known[permission] {
			t.Errorf("%s needs %q, which the write route does not ask for", code, permission)
		}
	}
}

func TestARetiredCodeStopsAcceptingValuesButKeepsItsHistory(t *testing.T) {
	h := newAPI(t)
	_, body := h.record(t, map[string]any{"code": "BODY_WEIGHT", "value": 69.5, "unit": "kg"})
	existing := observationOf(t, body)["id"].(string)

	if _, err := h.SQL.Exec(
		`UPDATE core.observation_code SET retired_at = now() WHERE code = 'BODY_WEIGHT'`); err != nil {
		t.Fatal(err)
	}

	resp, _ := h.record(t, map[string]any{"code": "BODY_WEIGHT", "value": 70, "unit": "kg"})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("a retired code still accepts values: %d", resp.StatusCode)
	}
	// A value recorded before the code was withdrawn is still a fact about a patient.
	if resp, _ := h.call(t, http.MethodGet, "/v1/observations/"+existing, nil); resp.StatusCode != http.StatusOK {
		t.Errorf("a value under a retired code became unreadable: %d", resp.StatusCode)
	}
}
