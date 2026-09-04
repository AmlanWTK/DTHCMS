package patient_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/audit"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/patient"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/blobstore/blobtest"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
)

// Registration, end to end and against a real database (CP29).
//
// The whole of it is one transaction — patient, identifiers, research subject, link, event,
// projection — and almost every test here is about that being true rather than nearly true.

type api struct {
	*db
	service *patient.Service
	events  *eventstore.Store
	server  *httptest.Server
	blobs   *blobtest.Store
	clock   *clock.Fixed
	user    uuid.UUID
	device  uuid.UUID
}

// registrar is the session the tests make requests as: a registration officer, at the
// registration station, on an enrolled tablet.
type registrar struct {
	facility, user, device uuid.UUID
	permissions            []string
}

func (r registrar) Identify(context.Context, string) (httpx.Caller, error) {
	return httpx.Caller{
		UserID: r.user.String(), FacilityID: r.facility.String(),
		SessionID: uuid.NewSHA1(r.user, []byte("session")).String(),
		Code:      "R001", Permissions: r.permissions, Roles: []string{"REGISTRATION"},
	}, nil
}

// Authorize both decides and installs the principal, as the real engine does: the principal
// is the first moment the active role is known to be one the person holds, which is why a
// route with no permission guard carries none and cannot construct an envelope.
func (r registrar) Authorize(ctx context.Context, caller httpx.Caller, anyOf []string) (context.Context, httpx.AuthzDecision) {
	for _, want := range anyOf {
		for _, held := range caller.Permissions {
			if want == held {
				return httpx.WithPrincipal(ctx, httpx.Principal{
					UserID: caller.UserID, FacilityID: caller.FacilityID, SessionID: caller.SessionID,
					Code: caller.Code, DeviceID: r.device.String(),
					Role: "REGISTRATION", Station: "REGISTRATION",
				}), httpx.AuthzDecision{Allowed: true, Reason: "allowed"}
			}
		}
	}
	return ctx, httpx.AuthzDecision{Reason: "permission_not_held"}
}

// auditing carries the patient module's access entries into the real audit chain, exactly
// as cmd/api's bridge does. The real recorder rather than a spy, because half of what CP31
// asserts is what the *chain* ends up holding — and a spy would happily accept a search
// term that the real sentence registry would reject.
type auditing struct{ recorder *audit.Recorder }

func (a auditing) RecordPatientAccess(ctx context.Context, e patient.AccessEntry) error {
	details := map[string]any{"count": e.Count}
	if e.By != "" {
		details["by"] = e.By
	}
	entry := audit.Entry{
		Kind: e.Kind, FacilityID: e.FacilityID, ActorCode: e.ActorCode, ActorRole: e.ActorRole,
		TargetCode: e.Target, PatientID: e.PatientID, At: e.At, Details: details,
	}
	if e.ActorID != uuid.Nil {
		id := e.ActorID
		entry.ActorID = &id
	}
	_, err := a.recorder.Record(ctx, entry)
	return err
}

// alwaysStepUp stands in for the second factor. What a step-up *is* is tested where it
// lives; what matters here is that the merge route demands one at all, which the route
// declaration test asserts separately.
type alwaysStepUp struct{}

func (alwaysStepUp) ConsumeStepUp(context.Context, string, string, string) error { return nil }

func newAPI(t *testing.T, permissions ...string) *api {
	t.Helper()
	if len(permissions) == 0 {
		permissions = []string{"patient.write.demographics", "patient.read.demographics", "patient.merge"}
	}
	base := open(t)

	fixed := clock.NewFixed(time.Date(2026, 9, 3, 4, 42, 0, 0, time.UTC))
	events := eventstore.New(eventstore.Config{
		Pool: base.pool, Clock: fixed,
		Synchronous: projection.NewSyncSet(projection.Default),
	})
	// The projections must be registered before one may write, exactly as cmd/api does at
	// start-up: a synchronous projection with no row in read.projection_state silently
	// advances nothing.
	if err := projection.NewEngineWithEvents(base.pool, projection.Default, events).
		Register(context.Background()); err != nil {
		t.Fatal(err)
	}

	h := &api{
		db: base, events: events, clock: fixed,
		user: uuid.New(), device: uuid.New(),
	}
	h.service = patient.NewService(patient.ServiceConfig{
		Store: base.store, Events: events, Sealer: base.sealer, Clock: fixed,
	})

	// The registering officer and their tablet have to exist: the patient row references
	// the user, and an event references the device.
	if _, err := base.SQL.Exec(`
		INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, $2, 'R001', 'Registration Officer', 'নিবন্ধন কর্মকর্তা', 'active')`,
		h.user, base.facility); err != nil {
		t.Fatal(err)
	}
	if _, err := base.SQL.Exec(`
		INSERT INTO core.device (id, facility_id, name, kind, status, enrolled_at)
		VALUES ($1, $2, 'Tablet 1', 'tablet', 'active', now())`,
		h.device, base.facility); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	who := registrar{facility: base.facility, user: h.user, device: h.device, permissions: permissions}
	// A real object store in the test process (CP34). Mocking it away would leave the one
	// thing worth testing untested: that the server reads the object back rather than
	// taking the client's word for what was uploaded.
	// Real time for the object store, fixed time for everything else: the store's clock is
	// what signs the URL, and the test then makes a real HTTP request that the store checks
	// the expiry of. A fixed clock there mints a URL that is hours stale before it is used.
	blobs, adapter := blobtest.New(t, time.Now().UTC())
	h.blobs = blobs

	handlers := patient.NewHandlers(patient.HandlersConfig{
		Service: h.service, Store: base.store,
		Photos:  patient.NewPhotoService(base.store, events, adapter, fixed),
		Matcher: patient.NewMatcher(base.store, base.sealer),
		StepUp:  alwaysStepUp{},
		Audit:   auditing{recorder: audit.NewRecorder(audit.NewPostgresStore(base.pool), fixed, logger)},
		Clock:   fixed, Logger: logger,
	})
	router, err := httpx.NewRouter(httpx.RouterOptions{
		Logger: logger, IDs: &ids.Sequential{Prefix: "req"},
		MaxBodyBytes: 1 << 16, RequestTimeout: 10 * time.Second,
		Health:        &httpx.Health{Service: "api", Version: "test", Logger: logger},
		Authenticator: who, Authorizer: who,
		Routes: func(r chi.Router) { handlers.Mount(r) },
	})
	if err != nil {
		t.Fatal(err)
	}
	h.server = httptest.NewServer(router)
	t.Cleanup(h.server.Close)
	return h
}

// callWithout is `call` with no step-up token, for the routes that demand one.
func (h *api) callWithout(t *testing.T, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	return h.request(t, method, path, body, false)
}

func (h *api) call(t *testing.T, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	return h.request(t, method, path, body, true)
}

func (h *api) request(t *testing.T, method, path string, body any, stepUp bool) (*http.Response, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer registration")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		req.Header.Set("X-Requested-With", "DTHCMS")
		// The merge and high-impact-correction routes demand a step-up. What a step-up *is*
		// is tested where it lives; this harness only has to satisfy the door so the act
		// itself can be exercised — and `callWithout` leaves it off, so the demand can be
		// tested too.
		if stepUp {
			req.Header.Set(httpx.StepUpHeader, "step-up-ok")
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var decoded map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("%s %s returned %d and %q", method, path, resp.StatusCode, raw)
		}
	}
	return resp, decoded
}

// form is a complete registration as a desk would submit it.
func form(eventID uuid.UUID) map[string]any {
	return map[string]any{
		"event_id":            eventID.String(),
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

// --- the happy path ---

func TestARegistrationIsOneEventAndOneProjectionRow(t *testing.T) {
	// Acceptance criteria 1 and 2.
	h := newAPI(t)
	resp, body := h.call(t, http.MethodPost, "/v1/patients", form(uuid.Must(uuid.NewV7())))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}

	created, _ := body["patient"].(map[string]any)
	patientID, err := uuid.Parse(created["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if created["clinical_id"] != h.code+"-2026-000001" {
		t.Errorf("clinical_id = %v", created["clinical_id"])
	}
	if body["duplicate"] != false {
		t.Errorf("a first registration reported duplicate = %v", body["duplicate"])
	}

	var events int
	if err := h.SQL.QueryRow(
		`SELECT count(*) FROM ledger.event WHERE aggregate_id = $1`, patientID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Errorf("the ledger holds %d events for one registration", events)
	}

	var rows int
	if err := h.SQL.QueryRow(
		`SELECT count(*) FROM read.patient WHERE patient_id = $1`, patientID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("read.patient holds %d rows for one registration", rows)
	}

	// Criterion 2: the research identity exists, assigned in the same transaction.
	var researchID string
	if err := h.SQL.QueryRow(
		`SELECT research_id FROM identity_link.research_subject WHERE patient_id = $1`,
		patientID).Scan(&researchID); err != nil {
		t.Fatalf("the patient has no research identity: %v", err)
	}
	if !patient.ValidResearchID(researchID) {
		t.Errorf("research id %q is not opaque", researchID)
	}
	var subjects int
	if err := h.SQL.QueryRow(
		`SELECT count(*) FROM research.research_subject WHERE research_id = $1`,
		researchID).Scan(&subjects); err != nil || subjects != 1 {
		t.Errorf("research subjects = %d (%v)", subjects, err)
	}
}

func TestTheProjectionMatchesTheEventPayloadExactly(t *testing.T) {
	// The test the plan asks for by name. A projection that quietly drops, renames or
	// defaults a field is a screen that disagrees with the ledger, and the ledger is the
	// one that will be read in three years.
	h := newAPI(t)
	resp, body := h.call(t, http.MethodPost, "/v1/patients", form(uuid.Must(uuid.NewV7())))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	patientID := uuid.MustParse(body["patient"].(map[string]any)["id"].(string))

	var raw []byte
	if err := h.SQL.QueryRow(
		`SELECT payload FROM ledger.event WHERE aggregate_id = $1`, patientID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}

	// Every scalar in the payload, read back out of the read model under the same name.
	// Column-by-column rather than a spot check, so a field added to the payload and
	// forgotten in the derivation fails here.
	for field, want := range payload {
		if field == "identifier_kinds" {
			continue // an array; checked below
		}
		var got any
		query := `SELECT ` + field + `::text FROM read.patient WHERE patient_id = $1`
		if err := h.SQL.QueryRow(query, patientID).Scan(&got); err != nil {
			t.Errorf("read.patient has no usable %s: %v", field, err)
			continue
		}
		if text(got) != text(want) {
			t.Errorf("%s: ledger says %q, read model says %q", field, text(want), text(got))
		}
	}

	// database/sql has no array type, so the comparison is made in the database: the
	// column, rendered as JSON, against the payload's own array.
	var kinds string
	if err := h.SQL.QueryRow(
		`SELECT to_jsonb(identifier_kinds)::text FROM read.patient WHERE patient_id = $1`,
		patientID).Scan(&kinds); err != nil {
		t.Fatal(err)
	}
	wanted, err := json.Marshal(payload["identifier_kinds"])
	if err != nil {
		t.Fatal(err)
	}
	if kinds != string(wanted) {
		t.Errorf("identifier_kinds: ledger says %s, read model says %s", wanted, kinds)
	}
}

func TestTheEventCarriesNoIdentityNumberAndNoResearchID(t *testing.T) {
	// ADR-0020 §5. The ledger is append-only: a number written here could never be
	// re-sealed under a rotated key nor removed for a patient who withdraws consent, and a
	// research id here would put the re-identification link in a table the application
	// reads.
	h := newAPI(t)
	resp, body := h.call(t, http.MethodPost, "/v1/patients", form(uuid.Must(uuid.NewV7())))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	patientID := uuid.MustParse(body["patient"].(map[string]any)["id"].(string))

	var raw string
	if err := h.SQL.QueryRow(
		`SELECT payload::text FROM ledger.event WHERE aggregate_id = $1`, patientID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"199012345678", "1990 1234 5678", "RS-", "research"} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("the event payload contains %q: %s", forbidden, raw)
		}
	}

	// And it does not come back in the response either.
	rendered, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(rendered, []byte("199012345678")) || bytes.Contains(rendered, []byte("1990 1234 5678")) {
		t.Errorf("the response contains the national ID: %s", rendered)
	}
	identifiers := body["patient"].(map[string]any)["identifiers"].([]any)
	if len(identifiers) != 1 {
		t.Fatalf("identifiers = %v", identifiers)
	}
	masked := identifiers[0].(map[string]any)["masked"].(string)
	if masked == "" || !bytes.Contains([]byte(masked), []byte("*")) {
		t.Errorf("masked = %q", masked)
	}
}

// --- idempotence ---

func TestResubmittingTheSameRegistrationCreatesNothing(t *testing.T) {
	// Acceptance criterion 4, and the reason event_id is the client's: a tablet that
	// sends a registration, loses the reply and sends it again must create one patient.
	h := newAPI(t)
	eventID := uuid.Must(uuid.NewV7())

	first, firstBody := h.call(t, http.MethodPost, "/v1/patients", form(eventID))
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", first.StatusCode, firstBody)
	}
	second, secondBody := h.call(t, http.MethodPost, "/v1/patients", form(eventID))

	// 200 rather than 201: nothing was created this time, and a client reconciling an
	// offline queue by counting its 201s would otherwise double-count.
	if second.StatusCode != http.StatusOK {
		t.Fatalf("a re-submission returned %d: %v", second.StatusCode, secondBody)
	}
	if secondBody["duplicate"] != true {
		t.Errorf("the re-submission did not report itself as one: %v", secondBody["duplicate"])
	}

	firstPatient := firstBody["patient"].(map[string]any)
	secondPatient := secondBody["patient"].(map[string]any)
	if firstPatient["id"] != secondPatient["id"] || firstPatient["clinical_id"] != secondPatient["clinical_id"] {
		t.Errorf("the re-submission returned a different patient: %v vs %v", firstPatient, secondPatient)
	}

	for table, query := range map[string]string{
		"core.patient":                   `SELECT count(*) FROM core.patient`,
		"ledger.event":                   `SELECT count(*) FROM ledger.event`,
		"read.patient":                   `SELECT count(*) FROM read.patient`,
		"research.research_subject":      `SELECT count(*) FROM research.research_subject`,
		"identity_link.research_subject": `SELECT count(*) FROM identity_link.research_subject`,
		"core.patient_identifier":        `SELECT count(*) FROM core.patient_identifier`,
	} {
		var rows int
		if err := h.SQL.QueryRow(query).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != 1 {
			t.Errorf("%s holds %d rows after one registration submitted twice", table, rows)
		}
	}

	// And the clinical id series did not move: the second patient that was not created
	// did not consume a number.
	var next int
	if err := h.SQL.QueryRow(
		`SELECT next_value FROM core.clinical_id_counter WHERE facility_id = $1`, h.facility).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if next != 2 {
		t.Errorf("the counter is at %d; a re-submission consumed a clinical id", next)
	}
}

func TestAReusedEventIDForSomethingElseIsRefused(t *testing.T) {
	// A client bug rather than a retry. Answering it with a plausible-looking patient
	// would hide the bug for as long as the deployment lasts.
	h := newAPI(t)
	eventID := uuid.Must(uuid.NewV7())
	visit := uuid.New()
	actor := eventstore.ActorForTest(h.user, h.device, h.facility, "VITALS", "VITALS")
	if _, err := h.events.Append(context.Background(), eventstore.Envelope{
		EventID: eventID, AggregateType: "VISIT", AggregateID: visit, VisitID: &visit,
		EventType: "TEMP_RECORDED", EventVersion: 1,
		OccurredAt: h.clock.Now(), Actor: actor, Source: eventstore.SourceWeb,
		Payload: json.RawMessage(`{"code":"TEMP","value":37.1,"unit":"C"}`),
	}); err != nil {
		t.Fatal(err)
	}

	resp, body := h.call(t, http.MethodPost, "/v1/patients", form(eventID))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
}

// --- validation ---

func TestAnInvalidRegistrationIsRefusedFieldByFieldInBothLanguages(t *testing.T) {
	// Acceptance criterion 3. "Some values need correcting" is not something a
	// registration officer can act on; "the mobile number must be a Bangladeshi mobile,
	// like 01712345678" is.
	h := newAPI(t)
	body := form(uuid.Must(uuid.NewV7()))
	body["birth_date"] = "2030-01-01" // in the future
	body["phone_primary"] = "12345"   // not a mobile
	body["sex"] = "F"                 // not one of the three
	body["income_band"] = "12000"     // not a band
	delete(body, "consent_reference")

	resp, decoded := h.call(t, http.MethodPost, "/v1/patients", body)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, decoded)
	}
	envelope := decoded["error"].(map[string]any)
	fields, _ := envelope["fields"].(map[string]any)
	fieldsBN, _ := envelope["fields_bn"].(map[string]any)

	// Every problem at once, not the first: a desk that fixes one field, resubmits and is
	// told about the next holds a queue four times over.
	for _, want := range []string{"birth_date", "phone_primary", "sex", "socioeconomic.income_band", "consent_reference"} {
		if _, ok := fields[want]; !ok {
			t.Errorf("%s was not reported; got %v", want, fields)
		}
		if _, ok := fieldsBN[want]; !ok {
			t.Errorf("%s has no Bangla message; got %v", want, fieldsBN)
		}
	}
	if len(fields) != len(fieldsBN) {
		t.Errorf("%d English messages and %d Bangla ones", len(fields), len(fieldsBN))
	}

	// And nothing was written.
	var patients int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM core.patient`).Scan(&patients); err != nil {
		t.Fatal(err)
	}
	if patients != 0 {
		t.Errorf("a refused registration wrote %d patients", patients)
	}
}

func TestAMalformedBirthDateIsAFieldError(t *testing.T) {
	h := newAPI(t)
	body := form(uuid.Must(uuid.NewV7()))
	body["birth_date"] = "12 April 1979"

	resp, decoded := h.call(t, http.MethodPost, "/v1/patients", body)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, decoded)
	}
	fields := decoded["error"].(map[string]any)["fields"].(map[string]any)
	if _, ok := fields["birth_date"]; !ok {
		t.Errorf("the date was refused without naming the field: %v", fields)
	}
}

func TestARegistrationWithoutAnEventIDIsRefused(t *testing.T) {
	h := newAPI(t)
	body := form(uuid.Must(uuid.NewV7()))
	delete(body, "event_id")

	resp, decoded := h.call(t, http.MethodPost, "/v1/patients", body)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, decoded)
	}
}

func TestAFieldTheServerDoesNotKnowIsRefused(t *testing.T) {
	// Silently dropping a field a client believed it sent is how a clinical value goes
	// missing without anyone noticing.
	h := newAPI(t)
	body := form(uuid.Must(uuid.NewV7()))
	body["blood_group"] = "O+"

	resp, decoded := h.call(t, http.MethodPost, "/v1/patients", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, decoded)
	}
}

func TestTheSameNationalIDIsRefusedForASecondPatient(t *testing.T) {
	h := newAPI(t)
	if resp, body := h.call(t, http.MethodPost, "/v1/patients", form(uuid.Must(uuid.NewV7()))); resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}

	second := form(uuid.Must(uuid.NewV7()))
	second["name_en"] = "Rahima Begum (again)"
	// The same number, written the way a different operator would write it.
	second["identifiers"] = map[string]string{"national_id": "1990-1234-5678"}

	resp, decoded := h.call(t, http.MethodPost, "/v1/patients", second)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, decoded)
	}
	fields := decoded["error"].(map[string]any)["fields"].(map[string]any)
	if _, ok := fields["identifiers"]; !ok {
		t.Errorf("the conflict did not name the field: %v", fields)
	}

	// The refused registration left nothing, including its clinical id.
	var patients, next int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM core.patient`).Scan(&patients); err != nil {
		t.Fatal(err)
	}
	if err := h.SQL.QueryRow(
		`SELECT next_value FROM core.clinical_id_counter WHERE facility_id = $1`, h.facility).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if patients != 1 || next != 2 {
		t.Errorf("after a refused duplicate: %d patients, counter at %d", patients, next)
	}
}

// --- the duplicate hook ---

func TestTheDuplicateHookCanRefuseARegistration(t *testing.T) {
	// CP30 plugs in here. The point of adding the seam now is that CP30 is a matching
	// algorithm rather than a retrofit into a handler that grew around its absence.
	h := newAPI(t)
	var sawIdentifiers int
	h.service.Duplicates = func(_ context.Context, facility uuid.UUID, in patient.Registration, ids []patient.Identifier) error {
		if facility != h.facility {
			t.Errorf("the hook was given facility %s", facility)
		}
		if in.PhonePrimary != "+8801712345678" {
			t.Errorf("the hook was given an unnormalised phone %q", in.PhonePrimary)
		}
		sawIdentifiers = len(ids)
		return errors.New("this is Rahima Begum, DTHC-FRD-2025-000042")
	}

	resp, _ := h.call(t, http.MethodPost, "/v1/patients", form(uuid.Must(uuid.NewV7())))
	if resp.StatusCode < 400 {
		t.Fatalf("a refused duplicate returned %d", resp.StatusCode)
	}
	if sawIdentifiers != 1 {
		t.Errorf("the hook saw %d sealed identifiers", sawIdentifiers)
	}
	var patients int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM core.patient`).Scan(&patients); err != nil {
		t.Fatal(err)
	}
	if patients != 0 {
		t.Errorf("a registration the hook refused wrote %d patients", patients)
	}
}

// --- reading one back ---

func TestAPatientCanBeReadBack(t *testing.T) {
	h := newAPI(t)
	_, created := h.call(t, http.MethodPost, "/v1/patients", form(uuid.Must(uuid.NewV7())))
	id := created["patient"].(map[string]any)["id"].(string)

	resp, body := h.call(t, http.MethodGet, "/v1/patients/"+id, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	found := body["patient"].(map[string]any)
	if found["clinical_id"] != h.code+"-2026-000001" {
		t.Errorf("clinical_id = %v", found["clinical_id"])
	}
	// The age is rendered by the server, on the clinic's calendar, beside the precision
	// that says how much it is worth.
	birth := found["birth"].(map[string]any)
	if birth["precision"] != "day" || birth["date"] != "1979-04-12" {
		t.Errorf("birth = %v", birth)
	}
	if age, ok := birth["age"].(float64); !ok || int(age) != 47 {
		t.Errorf("age = %v, want 47 on 2026-09-03", birth["age"])
	}
	// The phone came back normalised, however it was typed.
	if found["phone_primary"] != "+8801712345678" || found["phone_secondary"] != "+88028812345" {
		t.Errorf("phones = %v / %v", found["phone_primary"], found["phone_secondary"])
	}
}

func TestAnUnknownOrForbiddenPatientIsTheSameAnswer(t *testing.T) {
	// A 404 that distinguishes "no such patient" from "not yours" from "that is not a
	// UUID" is a way to learn which patients exist.
	h := newAPI(t)
	for _, path := range []string{
		"/v1/patients/" + uuid.New().String(),
		"/v1/patients/not-a-uuid",
	} {
		resp, body := h.call(t, http.MethodGet, path, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s returned %d: %v", path, resp.StatusCode, body)
			continue
		}
		if body["error"].(map[string]any)["code"] != "NOT_FOUND" {
			t.Errorf("GET %s returned %v", path, body["error"])
		}
	}
}

func TestRegisteringNeedsThePermission(t *testing.T) {
	h := newAPI(t, "patient.read.demographics")
	resp, _ := h.call(t, http.MethodPost, "/v1/patients", form(uuid.Must(uuid.NewV7())))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a reader registered a patient: %d", resp.StatusCode)
	}
}

// --- helpers ---

// text renders a payload value the way ::text renders a column, so the two can be
// compared without a type switch per field.
func text(v any) string {
	switch value := v.(type) {
	case nil:
		return ""
	case string:
		return value
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(value)
	default:
		raw, _ := json.Marshal(value)
		return string(raw)
	}
}
