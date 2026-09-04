package history_test

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

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/history"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
)

// Medical history (CP53, §3 station 4, §11.1).
//
// Four acceptance criteria, and three of them are properties this file can prove:
//
//	1. complaints and comorbidities are coded, not free text;
//	3. prior history is presented for confirmation at the next visit, never auto-accepted;
//	4. every item is individually attributed.
//
// Criterion 2 — current medications link to formulary products where they exist — cannot be
// finished here: the formulary is a later checkpoint and there is nothing to link to. What is
// tested is the half that exists now, which is the half that would otherwise be missing when
// it arrives: every medication carries a reconciliation state from the moment it is recorded,
// and nothing else does.
//
// Criterion 3 is the one worth reading closely. It is easy to satisfy its words and destroy
// its meaning — a `confirmed` flag the read sets, a column default, a batch endpoint that
// stamps a list. Each of those makes an assertion on somebody's behalf, and the failure it
// produces is a signed document saying a patient is on a drug they stopped in March, with
// nobody's name against the claim. So the tests below check not only that confirming works
// but that carrying forward does *not*.

type api struct {
	*testsupport.DB
	store    *history.Store
	service  *history.Service
	server   *httptest.Server
	clock    *clock.Fixed
	facility uuid.UUID
	user     uuid.UUID
	device   uuid.UUID
	patient  uuid.UUID
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
		Code:      "H007", Permissions: *s.permissions, Roles: []string{*s.role},
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
	h.held = []string{history.PermRead, history.PermWrite, history.PermConfirm}
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
	h.store = history.NewStore(pool)
	h.service = history.NewService(h.store, events, h.clock)
	h.seed(t)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handlers := history.NewHandlers(history.HandlersConfig{
		Service: h.service, Store: h.store, Logger: logger,
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

// seed puts one patient in the record. Written directly rather than through the registration
// service because this file is about history and a registration is four other checkpoints.
func (h *api) seed(t *testing.T) {
	t.Helper()
	if _, err := h.SQL.Exec(`
		INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, $2, 'H007', 'Nasrin Sultana', 'নাসরিন সুলতানা', 'active')`,
		h.user, h.facility); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO core.device (id, facility_id, name, kind, status, enrolled_at)
		VALUES ($1, $2, 'Tablet 4', 'tablet', 'active', now())`,
		h.device, h.facility); err != nil {
		t.Fatal(err)
	}
	h.patient = uuid.New()
	if _, err := h.SQL.Exec(`
		INSERT INTO core.patient (id, facility_id, clinical_id, name_en, sex, birth_date,
		                          dob_precision, dob_verified_by, phone_primary, status,
		                          registered_by, registered_at)
		VALUES ($1, $2, 'DTHC-FRD-2026-000531', 'Rokeya Begum', 'female', DATE '1974-04-02',
		        'day', 'national_id', '+8801711111531', 'active', $3, now())`,
		h.patient, h.facility, h.user); err != nil {
		t.Fatalf("seeding a patient: %v", err)
	}
}

// --- the http helpers ---

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

func (h *api) record(t *testing.T, body map[string]any) (*http.Response, map[string]any) {
	t.Helper()
	return h.do(t, http.MethodPost, "/v1/patients/"+h.patient.String()+"/medical-history", body)
}

// mustRecord records an item and fails if it did not land. Returns the item.
func (h *api) mustRecord(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	resp, decoded := h.record(t, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("recording %v: %d %v", body["kind"], resp.StatusCode, decoded)
	}
	item, _ := decoded["item"].(map[string]any)
	return item
}

func (h *api) list(t *testing.T) []map[string]any {
	t.Helper()
	resp, decoded := h.do(t, http.MethodGet,
		"/v1/patients/"+h.patient.String()+"/medical-history", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("listing: %d %v", resp.StatusCode, decoded)
	}
	raw, _ := decoded["items"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		row, _ := item.(map[string]any)
		out = append(out, row)
	}
	return out
}

func complaint() map[string]any {
	return map[string]any{
		"kind": "COMPLAINT", "code_system": "DTHC", "code_version": "1.0",
		"code": "POLYURIA", "said": "passing urine all night since Eid",
		"duration_days": 21, "severity": "moderate",
	}
}

func comorbidity() map[string]any {
	return map[string]any{
		"kind": "COMORBIDITY", "code_system": "ICD10", "code_version": "2019", "code": "I10",
		"onset_on": "2019-06-01", "onset_precision": "month",
	}
}

func medication() map[string]any {
	return map[string]any{
		"kind": "MEDICATION", "code_system": "DTHC", "code_version": "1.0",
		"code": "DRUG_METFORMIN", "dose": "1 tablet", "frequency": "twice a day after food",
	}
}

// ---------------------------------------------------------------------------
// Criterion 1: coded, not free text
// ---------------------------------------------------------------------------

func TestAComplaintIsRecordedWithItsCoding(t *testing.T) {
	h := newAPI(t)

	item := h.mustRecord(t, complaint())
	if item["code"] != "POLYURIA" || item["code_system"] != "DTHC" || item["code_version"] != "1.0" {
		t.Errorf("the coding did not survive the round trip: %v", item)
	}
	// The words come from the catalogue rather than from the item, so a title corrected next
	// year reads correctly on everything coded with it.
	if item["display_en"] == "" || item["display_bn"] == "" {
		t.Errorf("the catalogue's words were not joined on: %v", item)
	}
	// And what the patient said is the item's own.
	if item["said"] != "passing urine all night since Eid" {
		t.Errorf("what the patient said was lost: %v", item["said"])
	}
}

func TestHalfACodingIsRefused(t *testing.T) {
	// The failure CP52 exists to prevent, met from the other side. A code with no version is
	// a string, and guessing the missing third is how a coding acquires a version nobody
	// searched.
	h := newAPI(t)

	for _, partial := range []map[string]any{
		{"code": "POLYURIA"},
		{"code": "POLYURIA", "code_system": "DTHC"},
		{"code_system": "DTHC", "code_version": "1.0"},
	} {
		body := complaint()
		delete(body, "code")
		delete(body, "code_system")
		delete(body, "code_version")
		for k, v := range partial {
			body[k] = v
		}
		resp, decoded := h.record(t, body)
		if resp.StatusCode == http.StatusCreated {
			t.Errorf("%v was accepted as a coding", partial)
		} else if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%v answered %d, not 422: %v", partial, resp.StatusCode, decoded)
		}
	}
}

func TestAnUncodedItemMustSayWhatWasMeant(t *testing.T) {
	// The escape hatch, and its floor. An item with neither a coding nor words asserts that
	// the patient has *something*.
	h := newAPI(t)

	body := complaint()
	delete(body, "code")
	delete(body, "code_system")
	delete(body, "code_version")
	body["said"] = ""
	resp, _ := h.record(t, body)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("an item with nothing in it answered %d, not 422", resp.StatusCode)
	}

	body["said"] = "a pain she cannot describe"
	item := h.mustRecord(t, body)
	if item["code"] != nil {
		t.Errorf("an uncoded item came back with a code: %v", item)
	}
}

func TestUncodedItemsAreCounted(t *testing.T) {
	// What keeps the escape hatch from becoming a loophole. If this number grows, the
	// catalogue is wrong rather than the officers — and somebody can see that it is growing.
	h := newAPI(t)

	body := complaint()
	delete(body, "code")
	delete(body, "code_system")
	delete(body, "code_version")
	body["said"] = "a pain she cannot describe"
	h.mustRecord(t, body)
	h.mustRecord(t, complaint())

	resp, decoded := h.do(t, http.MethodGet, "/v1/history/uncoded", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, decoded)
	}
	counts, _ := decoded["uncoded"].(map[string]any)
	if counts["COMPLAINT"] != float64(1) {
		t.Errorf("one uncoded complaint was recorded; the count says %v", counts["COMPLAINT"])
	}
}

func TestAConceptFromTheWrongCatalogueIsRefused(t *testing.T) {
	// The refusal a screen cannot make for itself, and the one whose absence would be
	// invisible: an ICD diagnosis filed as a presenting complaint makes the record assert
	// that a patient *presented with* type 2 diabetes, which is a claim nobody made.
	h := newAPI(t)

	body := complaint()
	body["code_system"] = "ICD10"
	body["code_version"] = "2019"
	body["code"] = "E11.9"
	resp, decoded := h.record(t, body)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("an ICD diagnosis was accepted as a complaint: %d %v", resp.StatusCode, decoded)
	}
}

func TestACodeThatIsNotInTheCatalogueCannotBeStored(t *testing.T) {
	// Not "the handler refuses it" — cannot, by any path. The item's coding is a foreign key
	// to the terminology, so a projection rebuild and a hand-written INSERT meet it too.
	h := newAPI(t)

	_, err := h.SQL.Exec(`
		INSERT INTO read.history_item (id, facility_id, patient_id, kind,
		                               code_system, code_version, code, said,
		                               duration_days, recorded_at, recorded_by,
		                               event_id, global_seq)
		VALUES ($1, $2, $3, 'COMPLAINT', 'DTHC', '1.0', 'NOT_A_CODE', '', 3,
		        now(), $4, $5, 900002)`,
		uuid.New(), h.facility, h.patient, h.user, uuid.New())
	if err == nil {
		t.Error("a code that is not in the catalogue was stored")
	}
}

// ---------------------------------------------------------------------------
// The per-kind rules
// ---------------------------------------------------------------------------

func TestAFamilyHistoryMustSayWhoseItIs(t *testing.T) {
	// A family history with no relative is not a family history. And the degree matters
	// clinically: a mother with diabetes is a risk factor with a number attached.
	h := newAPI(t)

	body := map[string]any{
		"kind": "FAMILY_HISTORY", "code_system": "ICD10", "code_version": "2019",
		"code": "E11.9",
	}
	resp, decoded := h.record(t, body)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a family history with no relation answered %d: %v", resp.StatusCode, decoded)
	}
	fields, _ := decoded["error"].(map[string]any)
	if fields != nil {
		named, _ := fields["fields"].(map[string]any)
		if _, ok := named["relation"]; !ok {
			t.Errorf("the refusal did not name the relation field: %v", named)
		}
	}

	body["relation"] = "MOTHER"
	item := h.mustRecord(t, body)
	if item["relation"] != "MOTHER" {
		t.Errorf("the relation was lost: %v", item)
	}
}

func TestAComplaintMustSayHowLong(t *testing.T) {
	// Duration is what separates a complaint from a diagnosis, and "three weeks" versus
	// "three years" is often the whole of the differential.
	h := newAPI(t)

	body := complaint()
	delete(body, "duration_days")
	resp, _ := h.record(t, body)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a complaint with no duration answered %d", resp.StatusCode)
	}
}

func TestADurationOfZeroDaysIsARealAnswer(t *testing.T) {
	// "Since this morning" is a duration. A shape that could not tell zero from absent would
	// turn the most urgent answer into a missing field.
	h := newAPI(t)

	body := complaint()
	body["duration_days"] = 0
	item := h.mustRecord(t, body)
	if item["duration_days"] != float64(0) {
		t.Errorf("zero days came back as %v", item["duration_days"])
	}
}

func TestAnOnsetDateTravelsWithItsPrecision(t *testing.T) {
	// The same rule the date of birth follows. A patient who says "about two years ago" has
	// given a real answer, and storing it as an exact date makes a guess look measured.
	h := newAPI(t)

	body := comorbidity()
	delete(body, "onset_precision")
	resp, _ := h.record(t, body)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("an onset with no precision answered %d", resp.StatusCode)
	}
}

func TestOnlyAMedicineCarriesADose(t *testing.T) {
	// A vaccination with a frequency is a data-entry accident nobody would notice on a
	// screen, and it would then be in the record forever.
	h := newAPI(t)

	body := map[string]any{
		"kind": "VACCINATION", "code_system": "DTHC", "code_version": "1.0",
		"code": "VAX_COVID19", "dose": "half a tablet",
	}
	resp, _ := h.record(t, body)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a vaccination with a dose answered %d", resp.StatusCode)
	}
}

func TestTheKindRulesAreEnforcedByTheDatabaseToo(t *testing.T) {
	// The handler exists so an officer sees a sentence; the trigger exists so the record is
	// safe from every path that is not the handler.
	h := newAPI(t)

	_, err := h.SQL.Exec(`
		INSERT INTO read.history_item (id, facility_id, patient_id, kind, said,
		                               recorded_at, recorded_by, event_id, global_seq)
		VALUES ($1, $2, $3, 'FAMILY_HISTORY', 'her mother had sugar',
		        now(), $4, $5, 900003)`,
		uuid.New(), h.facility, h.patient, h.user, uuid.New())
	if err == nil {
		t.Error("a family history with no relation was inserted directly")
	}
}

// ---------------------------------------------------------------------------
// Criterion 3: confirmed, never auto-accepted
// ---------------------------------------------------------------------------

func TestPriorHistoryComesBackUnconfirmed(t *testing.T) {
	// The heart of criterion 3. An item recorded last month arrives at this month's station 4
	// with nothing in `confirmed_at` — the software has made no claim about whether it is
	// still true, because nobody has.
	h := newAPI(t)

	h.mustRecord(t, medication())
	items := h.list(t)
	if len(items) != 1 {
		t.Fatalf("expected one item, got %d", len(items))
	}
	if items[0]["confirmed_at"] != nil {
		t.Errorf("a freshly recorded item came back already confirmed: %v", items[0])
	}
}

func TestConfirmingNamesTheClinicianWhoDidIt(t *testing.T) {
	// Criteria 3 and 4 together. A confirmation with no actor is the auto-acceptance the
	// criterion forbids, wearing a person's name.
	h := newAPI(t)

	item := h.mustRecord(t, medication())
	id, _ := item["id"].(string)

	resp, decoded := h.do(t, http.MethodPost, "/v1/history/items/"+id+"/confirm",
		map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirming: %d %v", resp.StatusCode, decoded)
	}
	confirmed, _ := decoded["item"].(map[string]any)
	if confirmed["confirmed_at"] == nil {
		t.Fatal("the item is still unconfirmed after a confirmation")
	}
	if confirmed["confirmed_by"] != h.user.String() {
		t.Errorf("confirmed by %v, not by the clinician who did it", confirmed["confirmed_by"])
	}
}

func TestNothingConfirmsAnItemButAPerson(t *testing.T) {
	// The failure this would actually take: not a bad row, but a column default added to make
	// a test pass, after which every item reads as confirmed and none was. Asserted as the
	// deployment asserts it, so the invariant fails in a unit run too.
	h := newAPI(t)

	h.mustRecord(t, complaint())
	if _, err := h.SQL.Exec(`SELECT core.assert_history_is_confirmed_by_people()`); err != nil {
		t.Errorf("the confirmation invariant fails on a clean record: %v", err)
	}

	var defaulted int
	if err := h.SQL.QueryRow(`
		SELECT count(*) FROM information_schema.columns
		 WHERE table_schema = 'read' AND table_name = 'history_item'
		   AND column_name IN ('confirmed_at', 'confirmed_by')
		   AND column_default IS NOT NULL`).Scan(&defaulted); err != nil {
		t.Fatal(err)
	}
	if defaulted != 0 {
		t.Error("a confirmation column has a default; criterion 3 is silently satisfied and destroyed")
	}
}

func TestThereIsNoWayToConfirmEverythingAtOnce(t *testing.T) {
	// Deliberately absent, and worth a test because it is the obvious convenience. A
	// "confirm all" button produces one action from a person and twenty assertions in the
	// record — which is exactly what criterion 3 forbids.
	h := newAPI(t)

	for _, path := range []string{
		"/v1/patients/" + h.patient.String() + "/medical-history/confirm",
		"/v1/history/confirm",
		"/v1/history/items/confirm",
	} {
		resp, _ := h.do(t, http.MethodPost, path, map[string]any{})
		// Any refusal will do — what must not happen is that one of them works. Which
		// refusal a router gives for a path it does not serve is the router's business.
		if resp.StatusCode < 400 {
			t.Errorf("%s answered %d; there must be no batch confirmation", path, resp.StatusCode)
		}
	}
}

func TestAnAmendmentConfirmsAsItChanges(t *testing.T) {
	// Somebody has just made a fresh assertion about this item. Leaving `confirmed_at` behind
	// would show an item edited a minute ago as one nobody has looked at since last month —
	// which is precisely the list station 4 works from.
	h := newAPI(t)

	item := h.mustRecord(t, medication())
	id, _ := item["id"].(string)

	resp, decoded := h.do(t, http.MethodPatch, "/v1/history/items/"+id,
		map[string]any{"dose": "2 tablets"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("amending: %d %v", resp.StatusCode, decoded)
	}
	amended, _ := decoded["item"].(map[string]any)
	if amended["dose"] != "2 tablets" {
		t.Errorf("the dose did not change: %v", amended)
	}
	if amended["confirmed_at"] == nil {
		t.Error("an amendment left the item unconfirmed")
	}
	if amended["amended_by"] != h.user.String() {
		t.Errorf("amended by %v", amended["amended_by"])
	}
}

// ---------------------------------------------------------------------------
// Criterion 4: individually attributed
// ---------------------------------------------------------------------------

func TestEveryItemNamesWhoRecordedIt(t *testing.T) {
	h := newAPI(t)

	item := h.mustRecord(t, complaint())
	if item["recorded_by"] != h.user.String() {
		t.Errorf("recorded by %v", item["recorded_by"])
	}
	if item["recorded_role"] != "HISTORY" {
		t.Errorf("recorded by role %v", item["recorded_role"])
	}
	if _, err := h.SQL.Exec(`SELECT core.assert_every_history_item_is_attributed()`); err != nil {
		t.Errorf("the attribution invariant fails: %v", err)
	}
}

func TestEachItemIsItsOwnEvent(t *testing.T) {
	// Criterion 4 is a property of the item, not of the list. A single HISTORY_TAKEN carrying
	// three items would make "who wrote that" unanswerable, and would make removing one
	// indistinguishable from rewriting the history.
	h := newAPI(t)

	h.mustRecord(t, complaint())
	h.mustRecord(t, comorbidity())
	h.mustRecord(t, medication())

	var events int
	if err := h.SQL.QueryRow(`
		SELECT count(*) FROM ledger.event WHERE event_type = 'HISTORY_ITEM_RECORDED'`).
		Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 3 {
		t.Errorf("three items produced %d events", events)
	}
}

func TestARetriedSaveWritesOneItem(t *testing.T) {
	// A history taken over a bad connection is the exact situation that produces duplicate
	// complaints in a record. The client's event id is the idempotency key.
	h := newAPI(t)

	body := complaint()
	body["event_id"] = uuid.New().String()
	h.mustRecord(t, body)
	h.record(t, body)

	if items := h.list(t); len(items) != 1 {
		t.Errorf("a retried save produced %d items", len(items))
	}
}

// ---------------------------------------------------------------------------
// Criterion 2: the half that exists before the formulary does
// ---------------------------------------------------------------------------

func TestEveryMedicationCarriesAReconciliationState(t *testing.T) {
	// The formulary is a later checkpoint and there is nothing to link to yet, which is the
	// honest reading of "where they exist". What matters now is that the *state* is recorded
	// from the moment a drug is written down — so the day the formulary lands, the work is
	// matching rows rather than migrating a record with nowhere to put the answer.
	h := newAPI(t)

	drug := h.mustRecord(t, medication())
	if drug["reconciliation"] != "UNRECONCILED" {
		t.Errorf("a medication was recorded as %v", drug["reconciliation"])
	}
	if drug["formulary_product_id"] != nil {
		t.Errorf("a product was linked before the formulary exists: %v", drug)
	}

	// And nothing else carries one. A vaccination with a reconciliation state would make
	// "which drugs has nobody checked" a question with the wrong answer.
	other := h.mustRecord(t, comorbidity())
	if other["reconciliation"] != nil && other["reconciliation"] != "" {
		t.Errorf("a comorbidity carries a reconciliation state: %v", other["reconciliation"])
	}
}

func TestOnlyAMatchNamesAProduct(t *testing.T) {
	// MATCHED means "it is this product", so it has to name one; NOT_STOCKED means somebody
	// looked and it is not here, which is a finding and names nothing.
	h := newAPI(t)

	item := h.mustRecord(t, medication())
	id, _ := item["id"].(string)

	resp, _ := h.do(t, http.MethodPatch, "/v1/history/items/"+id,
		map[string]any{"reconciliation": "MATCHED"})
	if resp.StatusCode == http.StatusOK {
		t.Error("a match that names no product was accepted")
	}

	resp, decoded := h.do(t, http.MethodPatch, "/v1/history/items/"+id,
		map[string]any{"reconciliation": "NOT_STOCKED"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("recording that a drug is not stocked: %d %v", resp.StatusCode, decoded)
	}
}

// ---------------------------------------------------------------------------
// Resolving, removing, and the difference
// ---------------------------------------------------------------------------

func TestResolvingKeepsTheItemAndRemovingSaysWhy(t *testing.T) {
	// "She had this and no longer does" is a clinical fact worth keeping. "This was never
	// true" is a correction, and what somebody disagreed with is the interesting part.
	h := newAPI(t)

	resolved := h.mustRecord(t, complaint())
	resolvedID, _ := resolved["id"].(string)
	if resp, decoded := h.do(t, http.MethodPatch, "/v1/history/items/"+resolvedID,
		map[string]any{"status": "RESOLVED"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("resolving: %d %v", resp.StatusCode, decoded)
	}

	removed := h.mustRecord(t, comorbidity())
	removedID, _ := removed["id"].(string)
	if resp, _ := h.do(t, http.MethodPost, "/v1/history/items/"+removedID+"/remove",
		map[string]any{}); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Error("an item was removed with no reason")
	}
	if resp, decoded := h.do(t, http.MethodPost, "/v1/history/items/"+removedID+"/remove",
		map[string]any{"reason": "recorded on the wrong patient"}); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("removing: %d %v", resp.StatusCode, decoded)
	}

	items := h.list(t)
	if len(items) != 1 {
		t.Fatalf("the list should hold the resolved item and not the removed one: %d", len(items))
	}
	if items[0]["status"] != "RESOLVED" {
		t.Errorf("the surviving item is %v", items[0]["status"])
	}

	// The removed one is still readable by id, with the reason, because a removal is
	// something somebody may need to look at.
	resp, decoded := h.do(t, http.MethodGet, "/v1/history/items/"+removedID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reading a removed item: %d %v", resp.StatusCode, decoded)
	}
}

func TestARemovedItemCannotBeConfirmed(t *testing.T) {
	h := newAPI(t)

	item := h.mustRecord(t, complaint())
	id, _ := item["id"].(string)
	h.do(t, http.MethodPost, "/v1/history/items/"+id+"/remove",
		map[string]any{"reason": "wrong patient"})

	resp, _ := h.do(t, http.MethodPost, "/v1/history/items/"+id+"/confirm", map[string]any{})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("confirming a removed item answered %d, not 409", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// The catalogue, the permissions and the standing rules
// ---------------------------------------------------------------------------

func TestTheKindsCarryTheirRules(t *testing.T) {
	// What makes a station app able to render the right fields per kind rather than
	// remembering which needs what.
	h := newAPI(t)

	resp, decoded := h.do(t, http.MethodGet, "/v1/history/kinds", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, decoded)
	}
	kinds, _ := decoded["kinds"].([]any)
	if len(kinds) != 6 {
		t.Fatalf("expected six kinds, got %d", len(kinds))
	}
	byKind := map[string]map[string]any{}
	for _, item := range kinds {
		row, _ := item.(map[string]any)
		byKind[row["kind"].(string)] = row
	}
	if byKind["FAMILY_HISTORY"]["requires_relation"] != true {
		t.Error("family history does not require a relation")
	}
	if byKind["COMPLAINT"]["requires_duration"] != true {
		t.Error("a complaint does not require a duration")
	}
	if byKind["MEDICATION"]["is_medication"] != true {
		t.Error("a medication is not marked as one")
	}
	if byKind["COMPLAINT"]["code_system"] != "DTHC" {
		t.Errorf("complaints draw on %v", byKind["COMPLAINT"]["code_system"])
	}
	if byKind["COMORBIDITY"]["code_system"] != "ICD10" {
		t.Errorf("comorbidities draw on %v", byKind["COMORBIDITY"]["code_system"])
	}

	relations, _ := decoded["relations"].([]any)
	if len(relations) == 0 {
		t.Error("no family relations")
	}
	// Smoking and alcohol belong to the lifestyle station and are shown from there. A second
	// copy would be two answers to one question with no way to tell which is current.
	lifestyle, _ := decoded["from_lifestyle_station"].([]any)
	if len(lifestyle) == 0 {
		t.Error("the kinds do not say which facts come from the lifestyle station")
	}
}

func TestWritingNeedsMoreThanReading(t *testing.T) {
	// §4.4's separation, and the reason it is three permissions: the physician who reads a
	// history at station 8 does not edit it there.
	h := newAPI(t)
	item := h.mustRecord(t, complaint())
	id, _ := item["id"].(string)

	h.held = []string{history.PermRead}
	if resp, _ := h.record(t, comorbidity()); resp.StatusCode != http.StatusForbidden {
		t.Errorf("recording with only a read permission answered %d", resp.StatusCode)
	}
	if resp, _ := h.do(t, http.MethodPost, "/v1/history/items/"+id+"/confirm",
		map[string]any{}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("confirming with only a read permission answered %d", resp.StatusCode)
	}

	// And confirming does not imply writing: a clinical assistant may say an item is still
	// true without being able to rewrite it.
	h.held = []string{history.PermRead, history.PermConfirm}
	if resp, _ := h.do(t, http.MethodPost, "/v1/history/items/"+id+"/confirm",
		map[string]any{}); resp.StatusCode != http.StatusOK {
		t.Errorf("confirming with the confirm permission answered %d", resp.StatusCode)
	}
	if resp, _ := h.do(t, http.MethodPatch, "/v1/history/items/"+id,
		map[string]any{"said": "changed"}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("amending with only confirm answered %d", resp.StatusCode)
	}
}

func TestTheStandingInvariantsHold(t *testing.T) {
	h := newAPI(t)
	h.mustRecord(t, complaint())
	h.mustRecord(t, medication())

	for _, fn := range []string{
		"core.assert_history_is_confirmed_by_people",
		"core.assert_every_history_item_is_attributed",
		"core.assert_every_history_kind_has_a_usable_catalogue",
	} {
		if _, err := h.SQL.Exec(`SELECT ` + fn + `()`); err != nil {
			t.Errorf("%s: %v", fn, err)
		}
	}
}

func TestTheHistoryComesBackInStationOrder(t *testing.T) {
	// Complaints first, then conditions, then family, then operations, medicines and
	// vaccinations — the order station 4 asks in, so the screen is the conversation.
	h := newAPI(t)

	h.mustRecord(t, medication())
	h.mustRecord(t, complaint())
	h.mustRecord(t, comorbidity())

	kinds := []string{}
	for _, item := range h.list(t) {
		kinds = append(kinds, item["kind"].(string))
	}
	want := []string{"COMPLAINT", "COMORBIDITY", "MEDICATION"}
	for i := range want {
		if i >= len(kinds) || kinds[i] != want[i] {
			t.Fatalf("the history came back as %v, not in station order %v", kinds, want)
		}
	}
}
