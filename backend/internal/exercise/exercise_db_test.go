package exercise_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/exercise"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
)

// Station 8 (CP60).
//
// The four acceptance criteria, and which tests carry them:
//
//  1. **contraindicated exercises are excluded, not warned** — the checkpoint's manual
//     verification, and the reason this suite checks the *bytes* of the response rather than only
//     the decoded list. A field a client could read is a field a client will eventually offer.
//  2. adherence targets structured and comparable across visits (required by §12.1);
//  3. the exercise sheet prints legibly in Bangla — the precondition being that every offered
//     exercise carries its Bangla instruction;
//  4. the library is editable without a code release.

type api struct {
	*testsupport.DB

	facility  uuid.UUID
	patient   uuid.UUID
	trainer   uuid.UUID
	physician uuid.UUID
	device    uuid.UUID

	user        uuid.UUID
	role        string
	permissions []string

	clock  *clock.Fixed
	store  *exercise.Store
	server *httptest.Server
}

type staff struct{ h *api }

func (s staff) Identify(context.Context, string) (httpx.Caller, error) {
	return httpx.Caller{
		UserID: s.h.user.String(), FacilityID: s.h.facility.String(),
		SessionID:   uuid.NewSHA1(s.h.user, []byte("session")).String(),
		Code:        "E001",
		Permissions: s.h.permissions, Roles: []string{s.h.role}, ActiveRole: s.h.role,
	}, nil
}

func (s staff) Authorize(ctx context.Context, caller httpx.Caller, anyOf []string) (context.Context, httpx.AuthzDecision) {
	for _, want := range anyOf {
		for _, held := range caller.Permissions {
			if want == held {
				return httpx.WithPrincipal(ctx, httpx.Principal{
					UserID: caller.UserID, FacilityID: caller.FacilityID,
					SessionID: caller.SessionID, Code: caller.Code,
					DeviceID: s.h.device.String(), Role: caller.ActiveRole,
					Station: "STN_EXERCISE",
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

	h := &api{
		DB: base, trainer: uuid.New(), physician: uuid.New(), device: uuid.New(),
		role:        "EXERCISE",
		permissions: []string{"observation.read.values", "observation.write.exercise"},
	}
	h.user = h.trainer
	h.clock = clock.NewFixed(time.Date(2026, 9, 14, 4, 30, 0, 0, time.UTC))
	if err := base.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&h.facility); err != nil {
		t.Fatal(err)
	}

	events := eventstore.New(eventstore.Config{
		Pool: pool, Clock: h.clock, Synchronous: projection.NewSyncSet(projection.Default),
	})
	if err := projection.NewEngineWithEvents(pool, projection.Default, events).Register(ctx); err != nil {
		t.Fatal(err)
	}
	h.store = exercise.NewStore(pool)
	h.seed(t)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handlers := exercise.NewHandlers(exercise.HandlersConfig{
		Service: exercise.NewService(h.store, events, h.clock),
		Store:   h.store, Clock: h.clock, Logger: logger,
	})
	who := staff{h: h}
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
	for _, who := range []struct {
		id           uuid.UUID
		code, en, bn string
	}{
		{h.trainer, "E001", "Shirin Akter", "শিরিন আক্তার"},
		{h.physician, "P001", "Dr Nahid-Ul-Haque", "ডা. নাহিদ-উল-হক"},
	} {
		if _, err := h.SQL.Exec(`
			INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
			VALUES ($1, $2, $3, $4, $5, 'active')`,
			who.id, h.facility, who.code, who.en, who.bn); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO core.device (id, facility_id, name, kind, status, enrolled_at)
		VALUES ($1, $2, 'Tablet 8', 'tablet', 'active', now())`, h.device, h.facility); err != nil {
		t.Fatal(err)
	}
	h.patient = uuid.New()
	if _, err := h.SQL.Exec(`
		INSERT INTO core.patient (id, facility_id, clinical_id, name_en, sex, birth_date,
		                          dob_precision, dob_verified_by, phone_primary, status,
		                          registered_by, registered_at)
		VALUES ($1, $2, 'DTHC-FRD-2026-000601', 'Anwara Begum', 'female', DATE '1962-02-09',
		        'day', 'national_id', '+8801711111601', 'active', $3, now())`,
		h.patient, h.facility, h.trainer); err != nil {
		t.Fatal(err)
	}
}

func (h *api) call(t *testing.T, method, path string, body any) (*http.Response, map[string]any, string) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = strings.NewReader(string(raw))
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
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return resp, out, string(raw)
}

// liveConditions is the catalogue as it stands, which is what a complete assessment has to ask.
func (h *api) liveConditions(t *testing.T) []string {
	t.Helper()
	rows, err := h.SQL.Query(
		`SELECT code FROM core.contraindication WHERE retired_at IS NULL ORDER BY ordering, code`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var codes []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			t.Fatal(err)
		}
		codes = append(codes, code)
	}
	return codes
}

// assessed records a complete assessment — every live condition asked — and returns the response.
func (h *api) assessed(t *testing.T, conditions ...string) map[string]any {
	t.Helper()
	if conditions == nil {
		conditions = []string{}
	}
	return h.assessedAsking(t, h.liveConditions(t), conditions)
}

func (h *api) assessedAsking(t *testing.T, asked, conditions []string) map[string]any {
	t.Helper()
	resp, body, _ := h.call(t, "POST", "/v1/exercise/assessments", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient,
		"walks_unaided": true, "walk_minutes": 20,
		"asked": asked, "contraindications": conditions,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("recording an assessment answered %d: %v", resp.StatusCode, body)
	}
	return body
}

// offered is the codes this patient may be shown.
func (h *api) offered(t *testing.T) []string {
	t.Helper()
	resp, body, _ := h.call(t, "GET", "/v1/patients/"+h.patient.String()+"/exercise/options", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("options answered %d: %v", resp.StatusCode, body)
	}
	return codesIn(body)
}

func codesIn(body map[string]any) []string {
	list, _ := body["exercises"].([]any)
	out := make([]string, 0, len(list))
	for _, raw := range list {
		item, _ := raw.(map[string]any)
		out = append(out, item["code"].(string))
	}
	return out
}

func has(codes []string, want string) bool {
	for _, code := range codes {
		if code == want {
			return true
		}
	}
	return false
}

// --- criterion 1: excluded, not warned ---

// The checkpoint's own manual verification, written down as a test.
func TestSevereNeuropathyMakesTheHighImpactOptionsAbsentNotFlagged(t *testing.T) {
	h := newAPI(t)
	h.assessed(t, "SEVERE_NEUROPATHY")

	codes := h.offered(t)
	for _, forbidden := range []string{"JOG", "SKIPPING", "STANDING_BALANCE"} {
		if has(codes, forbidden) {
			t.Fatalf("%s was offered to a patient with severe neuropathy: %v", forbidden, codes)
		}
	}
	// And the control: the filter excluded something rather than everything, which is what
	// distinguishes a working rule from a broken query.
	if !has(codes, "WALK_FLAT") {
		t.Fatalf("walking on level ground should still be offered: %v", codes)
	}
}

// The stronger form of the same criterion, and the one that catches the tempting implementation.
//
// A server that sent the whole library with a `contraindicated: true` flag would pass the test
// above if the test only read `exercises`. This one reads the **bytes**: a code that appears
// anywhere in the response — in another array, in a "hidden" list, in a warning — is a code a
// client can offer, and criterion 1 says it must not be there at all.
func TestTheExcludedExercisesAppearNowhereInTheResponse(t *testing.T) {
	h := newAPI(t)
	h.assessed(t, "SEVERE_NEUROPATHY", "ACTIVE_FOOT_ULCER")

	_, _, raw := h.call(t, "GET", "/v1/patients/"+h.patient.String()+"/exercise/options", nil)
	for _, forbidden := range []string{"JOG", "SKIPPING", "STANDING_BALANCE", "WALK_FLAT", "STAIRS"} {
		if strings.Contains(raw, `"`+forbidden+`"`) {
			t.Fatalf("%s is in the response body for a patient it is contraindicated for:\n%s",
				forbidden, raw)
		}
	}
}

func TestAPlanNamingAContraindicatedExerciseIsRefused(t *testing.T) {
	h := newAPI(t)
	assessment := h.assessed(t, "SEVERE_NEUROPATHY")
	recorded, _ := assessment["assessment"].(map[string]any)

	resp, body, _ := h.call(t, "POST", "/v1/exercise/plans", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient,
		"assessment_id": recorded["id"],
		"targets": []map[string]any{
			{"exercise_code": "JOG", "times_per_week": 3, "minutes_per_session": 20},
		},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a contraindicated plan answered %d, want 422: %v", resp.StatusCode, body)
	}
	failure, _ := body["error"].(map[string]any)
	if failure["code"] != "EXERCISE_CONTRAINDICATED" {
		t.Fatalf("the refusal is %v, want EXERCISE_CONTRAINDICATED", failure["code"])
	}
	// Both languages, because the operator at station 8 may read either and the refusal is the
	// one sentence in this checkpoint that must not be misunderstood.
	message, _ := failure["message"].(string)
	messageBN, _ := failure["message_bn"].(string)
	if message == "" || messageBN == "" {
		t.Fatalf("the refusal does not read in both languages: %v", failure)
	}
	// And it names what was chosen, what forbids it, and why. "Not allowed" sends an operator to
	// the next high-impact option; a reason sends them to a conversation.
	if !strings.Contains(message, "Jogging") ||
		!strings.Contains(strings.ToLower(message), "neuropathy") {
		t.Fatalf("the refusal does not name the exercise and the condition: %q", message)
	}
	if !strings.Contains(messageBN, "জগিং") || !strings.Contains(messageBN, "নিউরোপ্যাথি") {
		t.Fatalf("the Bangla refusal does not name them: %q", messageBN)
	}
	// The reason from the mapping row, which is the sentence a clinician who disagrees argues
	// with. A constant here would have made the mapping unarguable where it is actually met.
	if !strings.Contains(message, "protective sensation") {
		t.Fatalf("the refusal does not say why: %q", message)
	}
	fields, _ := failure["fields"].(map[string]any)
	if fields["exercise_code"] != "JOG" || fields["contraindication_code"] != "SEVERE_NEUROPATHY" {
		t.Fatalf("the refusal does not carry the codes a client could act on: %v", fields)
	}
}

// The same rule, one layer down. The service could be bypassed — by a second application, by a
// projection rebuild, by a migration — and criterion 1 must still hold.
func TestTheDatabaseRefusesAContraindicatedPlanItem(t *testing.T) {
	h := newAPI(t)
	assessment := h.assessed(t, "CARDIAC_LIMITATION")
	recorded, _ := assessment["assessment"].(map[string]any)
	planned := h.issue(t, recorded["id"].(string), target("WALK_FLAT", 5, 30))

	// Straight into the read model, past every line of Go in this package.
	_, err := h.SQL.Exec(`
		INSERT INTO read.exercise_plan_item
		  (plan_id, exercise_code, times_per_week, minutes_per_session)
		VALUES ($1, 'HEAVY_LIFT', 2, 30)`, planned["id"])
	if err == nil {
		t.Fatal("the database accepted a contraindicated plan item")
	}
	if !strings.Contains(err.Error(), "CARDIAC_LIMITATION") {
		t.Fatalf("the refusal does not name the condition: %v", err)
	}
}

// The invariant, which is the check that notices a row that arrived some other way — a restored
// dump, a migration, a trigger somebody disabled.
func TestTheInvariantNoticesAContraindicatedPlanItem(t *testing.T) {
	h := newAPI(t)
	assessment := h.assessed(t, "PROLIFERATIVE_RETINOPATHY")
	recorded, _ := assessment["assessment"].(map[string]any)
	planned := h.issue(t, recorded["id"].(string), target("WALK_FLAT", 5, 30))

	if _, err := h.SQL.Exec(
		`ALTER TABLE read.exercise_plan_item DISABLE TRIGGER exercise_plan_item_is_permitted`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO read.exercise_plan_item
		  (plan_id, exercise_code, times_per_week, minutes_per_session)
		VALUES ($1, 'HEAVY_LIFT', 2, 30)`, planned["id"]); err != nil {
		t.Fatal(err)
	}

	var ignored string
	err := h.SQL.QueryRow(
		`SELECT core.assert_no_plan_offers_a_contraindicated_exercise()::text`).Scan(&ignored)
	if err == nil {
		t.Fatal("the invariant passed with a contraindicated plan item in the table")
	}
	if !strings.Contains(err.Error(), "contraindicated") {
		t.Fatalf("the invariant failed for the wrong reason: %v", err)
	}
}

func TestNoRouteReturnsTheLibraryWhole(t *testing.T) {
	h := newAPI(t)
	// The route that must not exist. If somebody adds a convenient `GET /v1/exercises` this
	// fails, which is the point: the convenience is the bug.
	for _, path := range []string{"/v1/exercises", "/v1/exercise/library", "/v1/exercise"} {
		resp, _, _ := h.call(t, "GET", path, nil)
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("%s returns 200 — the library is reachable unfiltered", path)
		}
	}
}

func TestTheExclusionIsCountedAndExplainedWithoutNamingWhatWasExcluded(t *testing.T) {
	h := newAPI(t)
	body := h.assessed(t, "SEVERE_NEUROPATHY")
	options, _ := body["options"].(map[string]any)

	library := options["library_size"].(float64)
	excluded := options["excluded"].(float64)
	offered := float64(len(codesIn(options)))
	if excluded != library-offered {
		t.Fatalf("excluded is %v, but the library holds %v and %v were offered",
			excluded, library, offered)
	}
	if excluded < 1 {
		t.Fatalf("severe neuropathy excluded nothing: %v", options)
	}

	reasons, _ := options["reasons"].([]any)
	if len(reasons) != 1 {
		t.Fatalf("%d reasons given for one condition: %v", len(reasons), reasons)
	}
	reason, _ := reasons[0].(map[string]any)
	if reason["code"] != "SEVERE_NEUROPATHY" {
		t.Fatalf("the reason is %v", reason["code"])
	}
	// Named in both languages, because the sentence explaining a gap on a Bangla screen has to be
	// in Bangla or the gap is just a gap.
	if reason["name_en"] == "" || reason["name_bn"] == "" {
		t.Fatalf("the reason does not read in both languages: %v", reason)
	}
	if reason["excluded"].(float64) < 1 {
		t.Fatalf("the reason accounts for nothing: %v", reason)
	}
	// A finding about this patient, not a question nobody put. The two are different sentences
	// on the screen and different next acts for the operator.
	if reason["status"] != "APPLIES" {
		t.Fatalf("the reason is reported as %v, want APPLIES", reason["status"])
	}
}

func TestAPatientWithNoContraindicationsIsOfferedEverything(t *testing.T) {
	h := newAPI(t)
	body := h.assessed(t)
	options, _ := body["options"].(map[string]any)

	if options["excluded"].(float64) != 0 {
		t.Fatalf("something was excluded from a patient with no conditions: %v", options)
	}
	if float64(len(codesIn(options))) != options["library_size"].(float64) {
		t.Fatalf("the whole library was not offered: %v", options)
	}
	reasons, _ := options["reasons"].([]any)
	if len(reasons) != 0 {
		t.Fatalf("reasons were given where nothing was excluded: %v", reasons)
	}
}

func TestTheFilterFollowsTheRecordWhenAConditionResolves(t *testing.T) {
	h := newAPI(t)
	h.assessed(t, "ACTIVE_FOOT_ULCER")
	if has(h.offered(t), "WALK_FLAT") {
		t.Fatal("walking was offered with an open foot ulcer")
	}

	// The ulcer healed. A new assessment, not an edit.
	h.assessed(t)
	if !has(h.offered(t), "WALK_FLAT") {
		t.Fatal("walking is still excluded after the ulcer was recorded as resolved")
	}
}

func TestOptionsAreRefusedBeforeAnybodyAnswersTheQuestions(t *testing.T) {
	h := newAPI(t)
	resp, body, _ := h.call(t, "GET", "/v1/patients/"+h.patient.String()+"/exercise/options", nil)
	// Not the whole library, and not an empty list either. "No contraindications recorded" and
	// "no contraindications" are different facts, and answering the first with a list is the
	// failure this checkpoint exists to prevent.
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("options before an assessment answered %d, want 409: %v", resp.StatusCode, body)
	}
	failure, _ := body["error"].(map[string]any)
	if failure["code"] != "EXERCISE_NO_ASSESSMENT" {
		t.Fatalf("the refusal is %v", failure["code"])
	}
}

// --- criterion 2: countable, comparable targets ---

func target(code string, times, minutes int) map[string]any {
	return map[string]any{
		"exercise_code": code, "times_per_week": times, "minutes_per_session": minutes,
	}
}

func (h *api) issue(t *testing.T, assessment string, targets ...map[string]any) map[string]any {
	t.Helper()
	resp, body, _ := h.call(t, "POST", "/v1/exercise/plans", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient,
		"assessment_id": assessment, "targets": targets,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("issuing a plan answered %d: %v", resp.StatusCode, body)
	}
	plan, _ := body["plan"].(map[string]any)
	return plan
}

func (h *api) assessAndIssue(t *testing.T, targets ...map[string]any) map[string]any {
	t.Helper()
	body := h.assessed(t)
	recorded, _ := body["assessment"].(map[string]any)
	return h.issue(t, recorded["id"].(string), targets...)
}

func TestEveryTargetIsTwoNumbersAndTheWeeklyTotalIsTheServers(t *testing.T) {
	h := newAPI(t)
	plan := h.assessAndIssue(t,
		target("WALK_FLAT", 5, 30),
		target("CHAIR_STAND", 3, 10))

	if plan["minutes_per_week"].(float64) != 180 {
		t.Fatalf("the weekly total is %v, want 180", plan["minutes_per_week"])
	}
	items, _ := plan["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("%d items on the plan", len(items))
	}
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if item["times_per_week"].(float64) < 1 || item["minutes_per_session"].(float64) < 1 {
			t.Fatalf("a target is not countable: %v", item)
		}
		want := item["times_per_week"].(float64) * item["minutes_per_session"].(float64)
		if item["minutes_per_week"].(float64) != want {
			t.Fatalf("the item's weekly minutes are %v, want %v", item["minutes_per_week"], want)
		}
	}
}

func TestATargetWithoutItsNumbersIsRefused(t *testing.T) {
	h := newAPI(t)
	body := h.assessed(t)
	recorded, _ := body["assessment"].(map[string]any)

	for _, bad := range []map[string]any{
		{"exercise_code": "WALK_FLAT", "times_per_week": 0, "minutes_per_session": 30},
		{"exercise_code": "WALK_FLAT", "times_per_week": 3, "minutes_per_session": 0},
		{"exercise_code": "WALK_FLAT", "times_per_week": 30, "minutes_per_session": 30},
	} {
		resp, out, _ := h.call(t, "POST", "/v1/exercise/plans", map[string]any{
			"event_id": uuid.New(), "patient_id": h.patient,
			"assessment_id": recorded["id"], "targets": []map[string]any{bad},
		})
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("%v answered %d, want 422: %v", bad, resp.StatusCode, out)
		}
	}
}

func TestAPlanWithNoExercisesIsRefused(t *testing.T) {
	h := newAPI(t)
	body := h.assessed(t)
	recorded, _ := body["assessment"].(map[string]any)

	resp, out, _ := h.call(t, "POST", "/v1/exercise/plans", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient,
		"assessment_id": recorded["id"], "targets": []map[string]any{},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("an empty plan answered %d: %v", resp.StatusCode, out)
	}
}

// §12.1 compares a patient against themselves across visits, which only works if the two visits'
// plans are the same shape.
func TestPlansAreComparableAcrossVisits(t *testing.T) {
	h := newAPI(t)
	h.assessAndIssue(t, target("WALK_FLAT", 3, 20))
	h.clock.Advance(30 * 24 * time.Hour)
	h.assessAndIssue(t, target("WALK_FLAT", 5, 30), target("CHAIR_STAND", 3, 10))

	resp, body, _ := h.call(t, "GET", "/v1/patients/"+h.patient.String()+"/exercise/history", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the history answered %d: %v", resp.StatusCode, body)
	}
	plans, _ := body["plans"].([]any)
	if len(plans) != 2 {
		t.Fatalf("%d plans in the history, want 2", len(plans))
	}
	newest, _ := plans[0].(map[string]any)
	oldest, _ := plans[1].(map[string]any)
	if newest["minutes_per_week"].(float64) != 180 || oldest["minutes_per_week"].(float64) != 60 {
		t.Fatalf("the weekly totals are %v then %v, want 180 then 60",
			newest["minutes_per_week"], oldest["minutes_per_week"])
	}
	if newest["status"] != "ACTIVE" || oldest["status"] != "SUPERSEDED" {
		t.Fatalf("the statuses are %v and %v", newest["status"], oldest["status"])
	}
}

// --- criterion 3: it prints in Bangla ---

func TestEveryOfferedExerciseCarriesItsBanglaInstruction(t *testing.T) {
	h := newAPI(t)
	body := h.assessed(t)
	options, _ := body["options"].(map[string]any)
	list, _ := options["exercises"].([]any)
	if len(list) == 0 {
		t.Fatal("nothing was offered")
	}
	for _, raw := range list {
		item, _ := raw.(map[string]any)
		for _, field := range []string{"name_en", "name_bn", "how_en", "how_bn"} {
			if strings.TrimSpace(item[field].(string)) == "" {
				t.Fatalf("%v has no %s, so a sheet cannot be printed from it", item["code"], field)
			}
		}
	}
}

func TestThePrintedPlanCarriesTheInstructionInBothLanguages(t *testing.T) {
	h := newAPI(t)
	plan := h.assessAndIssue(t, target("WALK_FLAT", 5, 30))
	items, _ := plan["items"].([]any)
	item, _ := items[0].(map[string]any)
	for _, field := range []string{"name_en", "name_bn", "how_en", "how_bn"} {
		if strings.TrimSpace(item[field].(string)) == "" {
			t.Fatalf("the plan item has no %s: %v", field, item)
		}
	}
}

func TestEveryExclusionReasonReadsInBothLanguages(t *testing.T) {
	h := newAPI(t)
	var offender string
	err := h.SQL.QueryRow(`
		SELECT exercise_code || '/' || contraindication_code
		  FROM core.exercise_contraindication
		 WHERE btrim(reason_en) = '' OR btrim(reason_bn) = ''
		 LIMIT 1`).Scan(&offender)
	if err == nil {
		t.Fatalf("%s says why in only one language", offender)
	}
	if !strings.Contains(err.Error(), "no rows") {
		t.Fatal(err)
	}
}

func TestTheQuestionsReadInBothLanguages(t *testing.T) {
	h := newAPI(t)
	resp, body, _ := h.call(t, "GET", "/v1/exercise/contraindications", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the catalogue answered %d: %v", resp.StatusCode, body)
	}
	list, _ := body["contraindications"].([]any)
	if len(list) < 3 {
		t.Fatalf("%d conditions, want at least the three §3 step 8 names", len(list))
	}
	for _, raw := range list {
		item, _ := raw.(map[string]any)
		for _, field := range []string{"name_en", "name_bn", "question_en", "question_bn"} {
			if strings.TrimSpace(item[field].(string)) == "" {
				t.Fatalf("%v has no %s", item["code"], field)
			}
		}
	}
}

// --- criterion 4: the library is editable without a code release ---

func TestANewExerciseIsOfferedWithoutACodeChange(t *testing.T) {
	h := newAPI(t)
	if _, err := h.SQL.Exec(`
		INSERT INTO core.exercise (code, name_en, name_bn, how_en, how_bn,
		                           kind, intensity, impact, ordering)
		VALUES ('TAI_CHI', 'Tai chi', 'তাই চি',
		        'Follow the slow movements, keeping your weight over your feet.',
		        'ধীর নড়াচড়াগুলি অনুসরণ করুন, ওজন পায়ের উপর রেখে।',
		        'BALANCE', 'LOW', 'NONE', 130)`); err != nil {
		t.Fatal(err)
	}
	h.assessed(t)
	if !has(h.offered(t), "TAI_CHI") {
		t.Fatal("a row inserted into the library is not offered")
	}
}

func TestANewMappingExcludesWithoutACodeChange(t *testing.T) {
	h := newAPI(t)
	h.assessed(t, "UNCONTROLLED_HYPERTENSION")
	if !has(h.offered(t), "STAIRS") {
		t.Fatal("stairs should be offered before the mapping is added")
	}

	if _, err := h.SQL.Exec(`
		INSERT INTO core.exercise_contraindication
		  (exercise_code, contraindication_code, reason_en, reason_bn)
		VALUES ('STAIRS', 'UNCONTROLLED_HYPERTENSION',
		        'Climbing raises the pressure further.',
		        'সিঁড়ি ওঠা রক্তচাপ আরও বাড়ায়।')`); err != nil {
		t.Fatal(err)
	}
	if has(h.offered(t), "STAIRS") {
		t.Fatal("a mapping added to the table did not exclude anything")
	}
}

func TestARetiredExerciseIsNoLongerOffered(t *testing.T) {
	h := newAPI(t)
	h.assessed(t)
	if !has(h.offered(t), "SWIM") {
		t.Fatal("swimming should be offered")
	}
	if _, err := h.SQL.Exec(
		`UPDATE core.exercise SET retired_at = now() WHERE code = 'SWIM'`); err != nil {
		t.Fatal(err)
	}
	if has(h.offered(t), "SWIM") {
		t.Fatal("a retired exercise is still offered")
	}
}

func TestTheLibraryReportsThatNobodyHasApprovedIt(t *testing.T) {
	h := newAPI(t)
	body := h.assessed(t)
	options, _ := body["options"].(map[string]any)
	for _, raw := range options["exercises"].([]any) {
		item, _ := raw.(map[string]any)
		if item["approved"].(bool) {
			t.Fatalf("%v claims to be approved, but the library is a starter list",
				item["code"])
		}
	}
}

// --- the frozen assessment ---

func TestAPlanFreezesTheAssessmentItWasFilteredAgainst(t *testing.T) {
	h := newAPI(t)
	first := h.assessed(t, "CARDIAC_LIMITATION")
	recorded, _ := first["assessment"].(map[string]any)
	plan := h.issue(t, recorded["id"].(string), target("WALK_FLAT", 5, 30))

	// The condition resolves and a new assessment is taken. The old plan must still point at the
	// findings it was built from, or "why was she given this" has no answer.
	h.clock.Advance(24 * time.Hour)
	h.assessed(t)

	resp, body, _ := h.call(t, "GET", "/v1/patients/"+h.patient.String()+"/exercise/history", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the history answered %d: %v", resp.StatusCode, body)
	}
	plans, _ := body["plans"].([]any)
	kept, _ := plans[0].(map[string]any)
	if kept["assessment_id"] != plan["assessment_id"] {
		t.Fatalf("the plan now points at %v, not the assessment it was built from (%v)",
			kept["assessment_id"], plan["assessment_id"])
	}
	if kept["assessment_id"] != recorded["id"] {
		t.Fatalf("the frozen assessment is %v, want %v", kept["assessment_id"], recorded["id"])
	}
}

func TestIssuingAgainstASupersededAssessmentIsRefused(t *testing.T) {
	h := newAPI(t)
	first := h.assessed(t)
	stale, _ := first["assessment"].(map[string]any)

	// A colleague records a foot ulcer while this operator is still choosing.
	h.assessed(t, "ACTIVE_FOOT_ULCER")

	resp, body, _ := h.call(t, "POST", "/v1/exercise/plans", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient,
		"assessment_id": stale["id"],
		"targets":       []map[string]any{target("WALK_FLAT", 5, 30)},
	})
	// Without this the plan would be issued against the pre-ulcer list, and every filter check
	// would pass — because they would all be checking the wrong assessment.
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("a plan built on a superseded assessment answered %d: %v", resp.StatusCode, body)
	}
	failure, _ := body["error"].(map[string]any)
	if failure["code"] != "EXERCISE_ASSESSMENT_SUPERSEDED" {
		t.Fatalf("the refusal is %v", failure["code"])
	}
}

func TestASecondAssessmentSupersedesTheFirst(t *testing.T) {
	h := newAPI(t)
	h.assessed(t, "ACTIVE_FOOT_ULCER")
	h.clock.Advance(24 * time.Hour)
	h.assessed(t)

	resp, body, _ := h.call(t, "GET", "/v1/patients/"+h.patient.String()+"/exercise/history", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the history answered %d: %v", resp.StatusCode, body)
	}
	assessments, _ := body["assessments"].([]any)
	if len(assessments) != 2 {
		t.Fatalf("%d assessments, want 2 — the first must not be edited away", len(assessments))
	}
	newest, _ := assessments[0].(map[string]any)
	oldest, _ := assessments[1].(map[string]any)
	if newest["status"] != "ACTIVE" || oldest["status"] != "SUPERSEDED" {
		t.Fatalf("statuses are %v and %v", newest["status"], oldest["status"])
	}
	// The old findings are still readable, which is what makes the frozen plan meaningful.
	conditions, _ := oldest["contraindications"].([]any)
	if len(conditions) != 1 || conditions[0] != "ACTIVE_FOOT_ULCER" {
		t.Fatalf("the superseded assessment lost its findings: %v", oldest)
	}
}

// --- what is recorded, and by whom ---

func TestTheAssessmentAndThePlanBothSayWhoAndWhere(t *testing.T) {
	h := newAPI(t)
	body := h.assessed(t)
	recorded, _ := body["assessment"].(map[string]any)
	if recorded["recorded_by"] != h.trainer.String() {
		t.Fatalf("the assessment is attributed to %v", recorded["recorded_by"])
	}
	// [R-03]: the name, not only the id, so a reviewer sees who without a second request.
	if recorded["recorded_by_name_en"] != "Shirin Akter" ||
		recorded["recorded_by_name_bn"] != "শিরিন আক্তার" {
		t.Fatalf("the assessment does not carry the recorder's name: %v", recorded)
	}
	if recorded["station_code"] != "STN_EXERCISE" || recorded["device_id"] != h.device.String() {
		t.Fatalf("the assessment does not say where it was taken: %v", recorded)
	}

	plan := h.issue(t, recorded["id"].(string), target("WALK_FLAT", 5, 30))
	if plan["issued_by"] != h.trainer.String() || plan["issued_by_name_bn"] != "শিরিন আক্তার" {
		t.Fatalf("the plan does not say who issued it: %v", plan)
	}
}

func TestAnEmptyContraindicationListIsRecordedAsEmptyRatherThanMissing(t *testing.T) {
	h := newAPI(t)
	body := h.assessed(t)
	recorded, _ := body["assessment"].(map[string]any)
	conditions, ok := recorded["contraindications"].([]any)
	// "None apply" is the fact the whole filter turns on. A missing key would read as "nobody
	// asked", and the two must never be confused.
	if !ok || conditions == nil {
		t.Fatalf("the contraindication list is missing rather than empty: %v", recorded)
	}
	if len(conditions) != 0 {
		t.Fatalf("conditions appeared from nowhere: %v", conditions)
	}
}

func TestTheLedgerCarriesBothEventsWithTheirPatient(t *testing.T) {
	h := newAPI(t)
	h.assessAndIssue(t, target("WALK_FLAT", 5, 30))

	rows, err := h.SQL.Query(`
		SELECT event_type, patient_id IS NOT NULL
		  FROM ledger.event
		 WHERE event_type LIKE 'EXERCISE%'
		 ORDER BY global_seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var kinds []string
	for rows.Next() {
		var kind string
		var identified bool
		if err := rows.Scan(&kind, &identified); err != nil {
			t.Fatal(err)
		}
		if !identified {
			t.Fatalf("%s was appended without a patient", kind)
		}
		kinds = append(kinds, kind)
	}
	want := []string{"EXERCISE_ASSESSMENT_RECORDED", "EXERCISE_PLAN_ISSUED"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("the ledger holds %v, want %v", kinds, want)
	}
}

// --- refusals and scope ---

func TestAnUnknownConditionIsRefusedRatherThanStored(t *testing.T) {
	h := newAPI(t)
	resp, body, _ := h.call(t, "POST", "/v1/exercise/assessments", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient,
		"asked":             h.liveConditions(t),
		"contraindications": []string{"SEVERE_NEUROPATHYY"},
	})
	// A typo'd condition would filter nothing and look exactly like one that filtered correctly.
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("an unknown condition answered %d: %v", resp.StatusCode, body)
	}
}

func TestAnUnknownExerciseIsRefusedSeparatelyFromAContraindicatedOne(t *testing.T) {
	h := newAPI(t)
	body := h.assessed(t)
	recorded, _ := body["assessment"].(map[string]any)

	resp, out, _ := h.call(t, "POST", "/v1/exercise/plans", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient,
		"assessment_id": recorded["id"],
		"targets":       []map[string]any{target("MOON_WALK", 3, 20)},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("an unknown exercise answered %d: %v", resp.StatusCode, out)
	}
	failure, _ := out["error"].(map[string]any)
	// Two different problems must not wear one message: "not in the library" and "not safe for
	// this patient" send an operator to different places.
	if failure["code"] == "EXERCISE_CONTRAINDICATED" {
		t.Fatalf("an unknown exercise was reported as contraindicated: %v", failure)
	}
	fields, _ := failure["fields"].(map[string]any)
	targets, _ := fields["targets"].(string)
	// Which one. A plan carrying six targets and a refusal naming none is a refusal an operator
	// cannot act on.
	if !strings.Contains(targets, "MOON_WALK") {
		t.Fatalf("the refusal does not say which target: %v", fields)
	}
}

func TestAnotherFacilitysPatientIsNotFound(t *testing.T) {
	h := newAPI(t)
	stranger := uuid.New()
	for _, path := range []string{
		"/v1/patients/" + stranger.String() + "/exercise",
		"/v1/patients/" + stranger.String() + "/exercise/options",
		"/v1/patients/" + stranger.String() + "/exercise/history",
	} {
		resp, _, _ := h.call(t, "GET", path, nil)
		// 404 rather than 403: whether this facility has that patient is itself something a 403
		// would disclose.
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s answered %d for a patient this facility does not have", path, resp.StatusCode)
		}
	}
}

func TestAPatientWithNoExerciseRecordReadsAsEmptyRatherThanMissing(t *testing.T) {
	h := newAPI(t)
	resp, body, _ := h.call(t, "GET", "/v1/patients/"+h.patient.String()+"/exercise", nil)
	// A first visit is not a broken route.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a patient with no record answered %d: %v", resp.StatusCode, body)
	}
	if body["assessment"] != nil || body["plan"] != nil {
		t.Fatalf("something was found where nothing was recorded: %v", body)
	}
}

func TestWithoutTheStationPermissionNothingIsWritable(t *testing.T) {
	h := newAPI(t)
	h.permissions = []string{"observation.read.values"}

	resp, _, _ := h.call(t, "POST", "/v1/exercise/assessments", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient,
		"asked": h.liveConditions(t), "contraindications": []string{},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a reader could record an assessment: %d", resp.StatusCode)
	}
}

// --- what the assessment asked, as opposed to what it found ---

// The gap that made "we asked all five and none apply" and "we asked two and skipped the
// neuropathy question" byte-identical rows — after which the filter computed the permitted list
// as though the unasked question had been answered no.
func TestAnAssessmentThatSkipsAQuestionIsRefused(t *testing.T) {
	h := newAPI(t)
	all := h.liveConditions(t)

	resp, body, _ := h.call(t, "POST", "/v1/exercise/assessments", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient,
		"asked": all[:2], "contraindications": []string{},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("an incomplete assessment answered %d: %v", resp.StatusCode, body)
	}
	failure, _ := body["error"].(map[string]any)
	fields, _ := failure["fields"].(map[string]any)
	missing, _ := fields["asked"].(string)
	// Named, not "incomplete": a client working from a stale catalogue has to know which
	// questions to fetch and put before it can record.
	for _, code := range all[2:] {
		if !strings.Contains(missing, code) {
			t.Fatalf("the refusal does not name %s: %q", code, missing)
		}
	}
}

func TestAFindingAboutAQuestionNobodyAskedIsRefused(t *testing.T) {
	h := newAPI(t)
	all := h.liveConditions(t)

	resp, body, _ := h.call(t, "POST", "/v1/exercise/assessments", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient,
		"asked": all, "contraindications": []string{"SEVERE_NEUROPATHY"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("a complete assessment answered %d: %v", resp.StatusCode, body)
	}
	// And now the same finding without the question. Invariant 89, at the door.
	resp, body, _ = h.call(t, "POST", "/v1/exercise/assessments", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient,
		"asked":             remove(all, "SEVERE_NEUROPATHY"),
		"contraindications": []string{"SEVERE_NEUROPATHY"},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a finding about an unasked question answered %d: %v", resp.StatusCode, body)
	}
}

func remove(codes []string, drop string) []string {
	out := make([]string, 0, len(codes))
	for _, code := range codes {
		if code != drop {
			out = append(out, code)
		}
	}
	return out
}

func TestTheAssessmentRecordsWhatItAsked(t *testing.T) {
	h := newAPI(t)
	body := h.assessed(t)
	recorded, _ := body["assessment"].(map[string]any)
	asked, ok := recorded["asked"].([]any)
	if !ok || len(asked) != len(h.liveConditions(t)) {
		t.Fatalf("the assessment does not say what it asked: %v", recorded)
	}
}

// A condition added to the catalogue after an assessment was taken narrows that patient's list
// until somebody asks them the new question. Absence of evidence is not evidence of absence, and
// the alternative — offering it — is the same "warned, not excluded" failure one step removed.
func TestAConditionAddedLaterExcludesUntilItIsAsked(t *testing.T) {
	h := newAPI(t)
	h.assessed(t)
	if !has(h.offered(t), "SWIM") {
		t.Fatal("swimming should be offered before the new condition exists")
	}

	if _, err := h.SQL.Exec(`
		INSERT INTO core.contraindication
		  (code, name_en, name_bn, question_en, question_bn, ordering)
		VALUES ('OPEN_WOUND_ANY', 'Any open wound', 'যেকোনো খোলা ক্ষত',
		        'Is there an open wound anywhere on the body today?',
		        'আজ শরীরের কোথাও কি খোলা ক্ষত আছে?', 60)`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO core.exercise_contraindication
		  (exercise_code, contraindication_code, reason_en, reason_bn)
		VALUES ('SWIM', 'OPEN_WOUND_ANY', 'An open wound must not go into pool water.',
		        'খোলা ক্ষত নিয়ে পুলের পানিতে নামা যাবে না।')`); err != nil {
		t.Fatal(err)
	}

	resp, body, _ := h.call(t, "GET", "/v1/patients/"+h.patient.String()+"/exercise/options", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("options answered %d: %v", resp.StatusCode, body)
	}
	if has(codesIn(body), "SWIM") {
		t.Fatal("swimming is still offered against a question nobody has asked")
	}
	// And it is reported as a question rather than as a finding, because saying the patient has
	// an open wound would be a claim nobody made.
	reasons, _ := body["reasons"].([]any)
	var found map[string]any
	for _, raw := range reasons {
		reason, _ := raw.(map[string]any)
		if reason["code"] == "OPEN_WOUND_ANY" {
			found = reason
		}
	}
	if found == nil {
		t.Fatalf("the new question is not given as a reason: %v", reasons)
	}
	if found["status"] != "NOT_ASKED" {
		t.Fatalf("the new question is reported as %v, want NOT_ASKED", found["status"])
	}

	// Asking it re-opens the option.
	h.assessed(t)
	if !has(h.offered(t), "SWIM") {
		t.Fatal("swimming is still excluded after the new question was asked and answered no")
	}
}

// Retiring a condition is a clinical decision that it is no longer a contraindication. Without
// this, a retired condition would exclude forever: it is absent from every new assessment's asked
// set and so reads as "never asked".
func TestARetiredConditionStopsExcluding(t *testing.T) {
	h := newAPI(t)
	h.assessed(t, "ACTIVE_FOOT_ULCER")
	if has(h.offered(t), "WALK_FLAT") {
		t.Fatal("walking was offered with an open foot ulcer")
	}
	if _, err := h.SQL.Exec(
		`UPDATE core.contraindication SET retired_at = now() WHERE code = 'ACTIVE_FOOT_ULCER'`); err != nil {
		t.Fatal(err)
	}
	if !has(h.offered(t), "WALK_FLAT") {
		t.Fatal("a retired condition is still excluding")
	}
}

func TestTheInvariantNoticesAFindingNobodyAskedAbout(t *testing.T) {
	h := newAPI(t)
	h.assessed(t, "CARDIAC_LIMITATION")

	// Past the service, the way a restored dump or a migration would arrive.
	if _, err := h.SQL.Exec(`
		UPDATE read.exercise_assessment
		   SET asked = ARRAY['SEVERE_NEUROPATHY']::text[]
		 WHERE status = 'ACTIVE'`); err != nil {
		t.Fatal(err)
	}
	var ignored string
	err := h.SQL.QueryRow(
		`SELECT core.assert_every_finding_was_asked_about()::text`).Scan(&ignored)
	if err == nil {
		t.Fatal("the invariant passed with a finding nobody asked about")
	}
	if !strings.Contains(err.Error(), "did not ask about") {
		t.Fatalf("the invariant failed for the wrong reason: %v", err)
	}
}

func TestOneExerciseTargetedTwiceIsRefused(t *testing.T) {
	h := newAPI(t)
	body := h.assessed(t)
	recorded, _ := body["assessment"].(map[string]any)

	// Two different weekly totals for one exercise. Keeping the first by arrival order would put
	// a number in §12.1's adherence data that nobody chose.
	resp, out, _ := h.call(t, "POST", "/v1/exercise/plans", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient,
		"assessment_id": recorded["id"],
		"targets": []map[string]any{
			target("WALK_FLAT", 3, 20),
			target("WALK_FLAT", 5, 45),
		},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a duplicated target answered %d: %v", resp.StatusCode, out)
	}
}

// --- what a client can act on ---

// A retired exercise means the list moved; a typo means the request is wrong. A client that could
// not tell them apart would refetch on a typo or fail to refetch on a retirement.
func TestARetirementIsAConflictAndATypoIsAValidationFailure(t *testing.T) {
	h := newAPI(t)
	body := h.assessed(t)
	recorded, _ := body["assessment"].(map[string]any)

	if _, err := h.SQL.Exec(
		`UPDATE core.exercise SET retired_at = now() WHERE code = 'SWIM'`); err != nil {
		t.Fatal(err)
	}
	resp, out, _ := h.call(t, "POST", "/v1/exercise/plans", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient,
		"assessment_id": recorded["id"], "targets": []map[string]any{target("SWIM", 3, 30)},
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("a retired exercise answered %d, want 409: %v", resp.StatusCode, out)
	}
	failure, _ := out["error"].(map[string]any)
	if failure["code"] != "EXERCISE_RETIRED" {
		t.Fatalf("the refusal is %v, want EXERCISE_RETIRED", failure["code"])
	}
	fields, _ := failure["fields"].(map[string]any)
	if fields["exercise_code"] != "SWIM" {
		t.Fatalf("the refusal does not carry the code a screen acts on: %v", fields)
	}
	// And the sentence names the exercise the way the operator saw it. Every other refusal here
	// does; a database identifier in the middle of a Bengali sentence would be the only one that
	// answered a question nobody asked.
	message, _ := failure["message"].(string)
	messageBN, _ := failure["message_bn"].(string)
	if !strings.Contains(message, "Swimming") || strings.Contains(message, "SWIM ") {
		t.Fatalf("the refusal does not name the exercise: %q", message)
	}
	if !strings.Contains(messageBN, "সাঁতার") {
		t.Fatalf("the Bangla refusal does not name the exercise: %q", messageBN)
	}

	resp, out, _ = h.call(t, "POST", "/v1/exercise/plans", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient,
		"assessment_id": recorded["id"], "targets": []map[string]any{target("MOON_WALK", 3, 30)},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("an unknown exercise answered %d, want 422: %v", resp.StatusCode, out)
	}
}

// The codes a screen highlights a row with, out of the prose. A sentence gets translated,
// shortened and improved; a code does not.
func TestEveryTargetRefusalCarriesTheCodeAsWellAsTheSentence(t *testing.T) {
	h := newAPI(t)
	body := h.assessed(t)
	recorded, _ := body["assessment"].(map[string]any)

	for _, bad := range [][]map[string]any{
		{target("MOON_WALK", 3, 20)},
		{target("WALK_FLAT", 0, 20)},
		{target("WALK_FLAT", 3, 20), target("WALK_FLAT", 5, 40)},
	} {
		resp, out, _ := h.call(t, "POST", "/v1/exercise/plans", map[string]any{
			"event_id": uuid.New(), "patient_id": h.patient,
			"assessment_id": recorded["id"], "targets": bad,
		})
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("%v answered %d: %v", bad, resp.StatusCode, out)
		}
		failure, _ := out["error"].(map[string]any)
		fields, _ := failure["fields"].(map[string]any)
		if fields["exercise_code"] == nil || fields["exercise_code"] == "" {
			t.Fatalf("%v was refused without naming a code a screen could highlight: %v",
				bad, fields)
		}
		// And the sentence names the exercise the way the operator saw it, wherever the exercise
		// is one they were offered. A database identifier in a Bengali sentence answers a
		// question nobody asked; the code belongs in the field, not in the prose.
		sentence, _ := fields["targets"].(string)
		fieldsBN, _ := failure["fields_bn"].(map[string]any)
		sentenceBN, _ := fieldsBN["targets"].(string)
		if bad[0]["exercise_code"] == "WALK_FLAT" {
			if !strings.Contains(sentence, "Walking on level ground") ||
				strings.Contains(sentence, "WALK_FLAT") {
				t.Fatalf("the refusal uses the code rather than the name: %q", sentence)
			}
			if !strings.Contains(sentenceBN, "সমতল জায়গায় হাঁটা") {
				t.Fatalf("the Bangla refusal does not name the exercise: %q", sentenceBN)
			}
		}
	}
}

func TestTheIncompleteRefusalCarriesTheMissingCodesOutOfTheProse(t *testing.T) {
	h := newAPI(t)
	all := h.liveConditions(t)
	resp, body, _ := h.call(t, "POST", "/v1/exercise/assessments", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient,
		"asked": all[:1], "contraindications": []string{},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("an incomplete assessment answered %d: %v", resp.StatusCode, body)
	}
	failure, _ := body["error"].(map[string]any)
	fields, _ := failure["fields"].(map[string]any)
	codes, _ := fields["missing_conditions"].(string)
	for _, code := range all[1:] {
		if !strings.Contains(codes, code) {
			t.Fatalf("missing_conditions does not carry %s: %q", code, codes)
		}
	}
}

// The ledger records what the station said it put to the patient, not what the catalogue happened
// to contain. The two are the same set today because the coverage rule holds; the point is that
// the claim in the ledger is the station's own.
func TestTheLedgerRecordsWhatTheStationSaidItAsked(t *testing.T) {
	h := newAPI(t)
	h.assessed(t)

	var payload string
	if err := h.SQL.QueryRow(`
		SELECT payload::text FROM ledger.event
		 WHERE event_type = 'EXERCISE_ASSESSMENT_RECORDED'
		 ORDER BY global_seq DESC LIMIT 1`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	for _, code := range h.liveConditions(t) {
		if !strings.Contains(payload, code) {
			t.Fatalf("the event does not record that %s was asked: %s", code, payload)
		}
	}
	if !strings.Contains(payload, `"asked"`) {
		t.Fatalf("the event carries no asked set: %s", payload)
	}
}

// The plan item's wording is never omitted, because the contract marks it required and a
// generated client trusts that.
func TestThePlanItemAlwaysCarriesItsWordingKeys(t *testing.T) {
	h := newAPI(t)
	body := h.assessed(t)
	recorded, _ := body["assessment"].(map[string]any)
	resp, out, raw := h.call(t, "POST", "/v1/exercise/plans", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient,
		"assessment_id": recorded["id"], "targets": []map[string]any{target("WALK_FLAT", 5, 30)},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("issuing answered %d: %v", resp.StatusCode, out)
	}
	for _, key := range []string{`"how_en"`, `"how_bn"`, `"name_en"`, `"name_bn"`} {
		if !strings.Contains(raw, key) {
			t.Fatalf("the plan item omits %s, which the schema marks required: %s", key, raw)
		}
	}
}
