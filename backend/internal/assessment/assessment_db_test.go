package assessment_test

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

	"github.com/AmlanWTK/DTHCMS/backend/internal/assessment"
	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
)

// The lifestyle assessment (CP58, §3 step 3, §12).
//
// The four acceptance criteria are the spine of this file:
//
//  1. raw item responses are stored, not just totals;
//  2. the score's formula version is stored with every score;
//  3. pack-years is computed correctly against reference examples;
//  4. the instrument version is recorded.
//
// Criterion 3 is proved twice over: the arithmetic is in `calc` against shared fixtures both
// languages consume, and the path from "an operator typed 20 a day for 15 years" to a stored
// PACK_YEARS is proved here.

type api struct {
	*testsupport.DB

	facility uuid.UUID
	patient  uuid.UUID
	user     uuid.UUID
	device   uuid.UUID
	role     string

	permissions []string
	clock       *clock.Fixed

	store    *assessment.Store
	clinical *clinical.Service
	server   *httptest.Server
}

type staff struct {
	facility, device uuid.UUID
	user             *uuid.UUID
	permissions      *[]string
	role             *string
}

func (s staff) Identify(context.Context, string) (httpx.Caller, error) {
	return httpx.Caller{
		UserID: s.user.String(), FacilityID: s.facility.String(),
		SessionID:   uuid.NewSHA1(*s.user, []byte("session")).String(),
		Code:        "A014",
		Permissions: *s.permissions, Roles: []string{*s.role}, ActiveRole: *s.role,
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
					Station: "STN_LIFESTYLE",
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
			"observation.read.values", "observation.write.lifestyle",
			// The station also records the plain numbers the composite reads.
			"observation.write.anthro", "observation.write.vitals",
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

	h := &api{DB: base, user: uuid.New(), device: uuid.New(), role: "COUNSELOR"}
	h.clock = clock.NewFixed(time.Date(2026, 9, 14, 4, 42, 0, 0, time.UTC))
	h.permissions = permissions
	if err := base.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&h.facility); err != nil {
		t.Fatal(err)
	}

	events := eventstore.New(eventstore.Config{
		Pool: pool, Clock: h.clock, Synchronous: projection.NewSyncSet(projection.Default),
	})
	if err := projection.NewEngineWithEvents(pool, projection.Default, events).Register(ctx); err != nil {
		t.Fatal(err)
	}
	clinicalStore := clinical.NewStore(pool)
	h.clinical = clinical.NewService(clinicalStore, events, h.clock)
	h.store = assessment.NewStore(pool)
	h.seed(t)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := assessment.NewService(h.store, events, h.clock).WithDeriver(deriver{h.clinical})
	handlers := assessment.NewHandlers(assessment.HandlersConfig{
		Service: service, Store: h.store, Clock: h.clock, Logger: logger,
	})
	clinicalHandlers := clinical.NewHandlers(clinical.HandlersConfig{
		Service: h.clinical, Store: clinicalStore, Clock: h.clock, Logger: logger,
	})
	who := staff{facility: h.facility, user: &h.user, device: h.device,
		permissions: &h.permissions, role: &h.role}
	router, err := httpx.NewRouter(httpx.RouterOptions{
		Logger: logger, IDs: &ids.Sequential{Prefix: "req"},
		MaxBodyBytes: 1 << 16, RequestTimeout: 10 * time.Second,
		Health:        &httpx.Health{Service: "api", Version: "test", Logger: logger},
		Authenticator: who, Authorizer: who,
		Routes: func(r chi.Router) {
			handlers.Mount(r)
			clinicalHandlers.Mount(r)
			r.Route("/patients", func(p chi.Router) {
				handlers.MountPatient(p)
				clinicalHandlers.MountPatient(p)
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

// deriver is the bridge cmd/api provides in production, in three lines.
type deriver struct{ service *clinical.Service }

func (d deriver) RecordDerived(ctx context.Context, in clinical.Recording) (clinical.Observation, error) {
	observation, _, err := d.service.Record(ctx, in)
	return observation, err
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
		VALUES ($1, $2, 'Tablet 3', 'tablet', 'active', now())`, h.device, h.facility); err != nil {
		t.Fatal(err)
	}
	h.patient = uuid.New()
	if _, err := h.SQL.Exec(`
		INSERT INTO core.patient (id, facility_id, clinical_id, name_en, sex, birth_date,
		                          dob_precision, dob_verified_by, phone_primary, status,
		                          registered_by, registered_at)
		VALUES ($1, $2, 'DTHC-FRD-2026-000401', 'Md Rahim Uddin', 'male', DATE '1985-06-14',
		        'day', 'national_id', '+8801711111401', 'active', $3, now())`,
		h.patient, h.facility, h.user); err != nil {
		t.Fatal(err)
	}
}

func (h *api) call(t *testing.T, method, path string, body any) (*http.Response, map[string]any) {
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
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

// value records one plain observation.
func (h *api) value(t *testing.T, code string, value float64, unit string) {
	t.Helper()
	body := map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient,
		"code": code, "value": value, "effective_at": h.clock.Now().UTC(),
	}
	if unit != "" {
		body["unit"] = unit
	}
	if resp, out := h.call(t, "POST", "/v1/observations", body); resp.StatusCode != http.StatusCreated {
		t.Fatalf("recording %s answered %d: %v", code, resp.StatusCode, out)
	}
}

// auditC answers the three questions, with the option codes worth the scores given.
func (h *api) auditC(t *testing.T, frequency, amount, heavy string) (*http.Response, map[string]any) {
	t.Helper()
	return h.call(t, "POST", "/v1/assessments", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient, "instrument_code": "AUDIT_C",
		"answers": []map[string]any{
			{"item_code": "FREQUENCY", "option_code": frequency},
			{"item_code": "TYPICAL_AMOUNT", "option_code": amount},
			{"item_code": "HEAVY_EPISODES", "option_code": heavy},
		},
	})
}

// --- criterion 1: raw item responses ---

func TestTheItemsAreStoredAndTheTotalIsComputedFromThem(t *testing.T) {
	// The criterion that decides the schema. §12's cohorting is done on behaviour, and a total
	// cannot be re-analysed or re-scored under a corrected formula.
	h := newAPI(t)
	resp, body := h.auditC(t, "TWO_TO_FOUR_MONTHLY", "THREE_OR_FOUR", "MONTHLY")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("answering the questionnaire answered %d: %v", resp.StatusCode, body)
	}
	response, _ := body["response"].(map[string]any)

	answers, _ := response["answers"].([]any)
	if len(answers) != 3 {
		t.Fatalf("%d items were stored, want the three that were answered", len(answers))
	}
	// 2 + 1 + 2 under the WHO's scoring.
	if got := response["total"].(float64); got != 5 {
		t.Fatalf("the total reads %v, want 5", got)
	}

	// And there is nowhere the total could have been stored, which is the other half of the
	// criterion: a column would be a second place for it to be wrong.
	var columns int
	if err := h.SQL.QueryRow(`
		SELECT count(*) FROM information_schema.columns
		 WHERE table_schema = 'read' AND table_name = 'instrument_response'
		   AND column_name IN ('total', 'score')`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if columns != 0 {
		t.Fatalf("the response table has %d stored-total column(s)", columns)
	}
}

func TestTheScoreOfAnAnswerComesFromTheServer(t *testing.T) {
	// A client that could send a score could send any total it liked, and the total is what §12
	// cohorts on. The request body has no score field at all; this proves the server ignores one
	// smuggled in anyway.
	h := newAPI(t)
	resp, body := h.call(t, "POST", "/v1/assessments", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient, "instrument_code": "AUDIT_C",
		"answers": []map[string]any{
			{"item_code": "FREQUENCY", "option_code": "NEVER", "score": 99},
			{"item_code": "TYPICAL_AMOUNT", "option_code": "ONE_OR_TWO", "score": 99},
			{"item_code": "HEAVY_EPISODES", "option_code": "NEVER", "score": 99},
		},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("answering answered %d: %v", resp.StatusCode, body)
	}
	response, _ := body["response"].(map[string]any)
	if got := response["total"].(float64); got != 0 {
		t.Fatalf("a client's own scores were believed: the total reads %v, want 0", got)
	}
}

// --- criterion 4: the version answered ---

func TestAResponseNamesTheWordingItAnswered(t *testing.T) {
	h := newAPI(t)
	_, body := h.auditC(t, "NEVER", "ONE_OR_TWO", "NEVER")
	response, _ := body["response"].(map[string]any)
	if got := response["instrument_version"].(float64); got != 1 {
		t.Fatalf("the response names version %v", got)
	}
}

func TestAnsweringAgainSupersedesRatherThanReplaces(t *testing.T) {
	// The same rule every clinical value follows: an operator who ran the questionnaire twice
	// because the patient corrected themselves has produced two facts, not one edit.
	h := newAPI(t)
	h.auditC(t, "NEVER", "ONE_OR_TWO", "NEVER")
	h.auditC(t, "FOUR_OR_MORE_WEEKLY", "TEN_OR_MORE", "DAILY")

	resp, body := h.call(t, "GET", "/v1/patients/"+h.patient.String()+"/assessments", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reading the responses answered %d: %v", resp.StatusCode, body)
	}
	responses, _ := body["responses"].([]any)
	if len(responses) != 2 {
		t.Fatalf("%d responses, want both", len(responses))
	}
	live, superseded := 0, 0
	for _, raw := range responses {
		switch raw.(map[string]any)["status"] {
		case "ACTIVE":
			live++
		case "SUPERSEDED":
			superseded++
		}
	}
	if live != 1 || superseded != 1 {
		t.Fatalf("%d live and %d superseded", live, superseded)
	}
}

// --- D-26 ---

func TestACopyrightedQuestionnaireCannotBeAnswered(t *testing.T) {
	// Refused rather than silently skipped. An operator who was shown a form must be told why it
	// will not save, and a clinic that never sees this refusal never chases the licence.
	h := newAPI(t)
	resp, body := h.call(t, "POST", "/v1/assessments", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient, "instrument_code": "PHQ_9",
		"answers": []map[string]any{{"item_code": "Q1", "option_code": "NOT_AT_ALL"}},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("answering an unlicensed questionnaire answered %d, want 422: %v",
			resp.StatusCode, body)
	}
	failure, _ := body["error"].(map[string]any)
	if failure["code"] != "INSTRUMENT_NOT_LICENSED" {
		t.Fatalf("the refusal reads %v", failure["code"])
	}
	if failure["message_bn"] == "" {
		t.Fatal("the refusal is English-only")
	}
}

func TestTheCatalogueSaysWhyAQuestionnaireIsAbsent(t *testing.T) {
	// Omitting the unusable ones would make a decision look like an oversight, and a clinician
	// who expected to find PHQ-9 would go looking for a bug.
	h := newAPI(t)
	resp, body := h.call(t, "GET", "/v1/assessments/instruments", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the catalogue answered %d: %v", resp.StatusCode, body)
	}
	instruments, _ := body["instruments"].([]any)

	usable, refused := 0, 0
	for _, raw := range instruments {
		row := raw.(map[string]any)
		if row["usable"].(bool) {
			usable++
			if len(row["items"].([]any)) == 0 {
				t.Fatalf("%v is usable but has no questions", row["code"])
			}
			continue
		}
		refused++
		if row["licence_note"] == "" {
			t.Fatalf("%v is refused without saying why", row["code"])
		}
		if row["items"] != nil && len(row["items"].([]any)) > 0 {
			t.Fatalf("%v is unlicensed and its wording is in the database", row["code"])
		}
	}
	if usable == 0 || refused == 0 {
		t.Fatalf("%d usable and %d refused; the catalogue should show both", usable, refused)
	}

	// And the database refuses the wording rather than trusting a reviewer.
	if _, err := h.SQL.Exec(`SELECT core.assert_no_unlicensed_instrument_holds_items()`); err != nil {
		t.Fatalf("the invariant refuses the seed this system ships: %v", err)
	}
}

func TestTheInvariantNoticesCopyrightedWordingSmuggledIn(t *testing.T) {
	// A canary. An invariant that cannot fail is worse than none, because it reads as protection.
	h := newAPI(t)
	if _, err := h.SQL.Exec(`
		INSERT INTO core.instrument_version (instrument_code, version, scoring, published_at)
		VALUES ('PHQ_9', 1, 'sum', now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO core.instrument_item
		  (instrument_code, version, item_code, ordering, prompt_en, prompt_bn, answer_type)
		VALUES ('PHQ_9', 1, 'Q1', 1, 'Little interest or pleasure in doing things',
		        'কাজে আগ্রহ বা আনন্দ কমে যাওয়া', 'coded')`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`SELECT core.assert_no_unlicensed_instrument_holds_items()`); err == nil {
		t.Fatal("copyrighted wording passed the invariant")
	}
}

// --- criteria 2 and 3: the score and its formula ---

func TestTheCompositeCarriesTheFormulaAndItsVersion(t *testing.T) {
	// Criterion 2. And the version says `-proposed`, because D-26 has not been answered and a
	// score computed today must stay identifiable as one computed before anybody agreed to the
	// arithmetic.
	h := newAPI(t)
	h.value(t, "CIGARETTES_PER_DAY", 20, "1")
	h.value(t, "SMOKING_YEARS", 15, "1")
	h.value(t, "SLEEP_MINUTES", 5, "h")
	h.value(t, "ACTIVE_MINUTES_WEEK", 0, "min")

	// Pack-years first, so the smoking domain exists.
	if resp, out := h.call(t, "POST", "/v1/observations/derive", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient, "what": "PACK_YEARS",
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("deriving pack-years answered %d: %v", resp.StatusCode, out)
	}

	resp, body := h.auditC(t, "TWO_TO_THREE_WEEKLY", "THREE_OR_FOUR", "MONTHLY")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("answering answered %d: %v", resp.StatusCode, body)
	}
	scoring, _ := body["scoring"].(map[string]any)
	score, _ := scoring["score"].(map[string]any)
	if score == nil {
		t.Fatalf("no score was derived from four domains: %v", body)
	}
	if score["formula"] != "lifestyle_risk" {
		t.Fatalf("the score names formula %v", score["formula"])
	}
	if version, _ := score["formula_version"].(string); !strings.HasSuffix(version, "-proposed") {
		t.Fatalf("the formula version is %q; it must say it is a proposal until D-26 is answered", version)
	}
	// And it says how many domains went into it, because a score from three is a different
	// number from a score from four.
	inputs, _ := score["inputs"].(map[string]any)
	if inputs["domains"] != 4.0 {
		t.Fatalf("the score reports %v domains", inputs["domains"])
	}
}

func TestPackYearsIsComputedFromWhatTheOperatorTyped(t *testing.T) {
	// Criterion 3, end to end. The arithmetic itself is proved in `calc` against fixtures both
	// languages consume; this is the path from a station keyboard to a stored value.
	h := newAPI(t)
	h.value(t, "CIGARETTES_PER_DAY", 20, "1")
	h.value(t, "SMOKING_YEARS", 15, "1")

	resp, body := h.call(t, "POST", "/v1/observations/derive", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient, "what": "PACK_YEARS",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("deriving pack-years answered %d: %v", resp.StatusCode, body)
	}
	observation, _ := body["observation"].(map[string]any)
	// (20 ÷ 20) × 15.
	if got := observation["value"].(float64); got != 15 {
		t.Fatalf("pack-years reads %v, want 15", got)
	}
	if observation["formula"] != "pack_years" {
		t.Fatalf("the value names formula %v", observation["formula"])
	}
}

func TestAnUnassessedDomainIsNotCountedAsGoodNews(t *testing.T) {
	// The failure mode that would make this score worse than useless: scoring four domains out
	// of four and treating an unanswered one as nothing means the patient who answered nothing
	// scores best, and the operator who ran the whole questionnaire has produced a
	// worse-looking record than the one who ran none of it.
	h := newAPI(t)
	// Two domains only: sleep and activity, both perfect.
	h.value(t, "SLEEP_MINUTES", 8, "h")
	h.value(t, "ACTIVE_MINUTES_WEEK", 200, "min")

	// AUDIT-C makes the third, at its worst.
	resp, body := h.auditC(t, "FOUR_OR_MORE_WEEKLY", "TEN_OR_MORE", "DAILY")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("answering answered %d: %v", resp.StatusCode, body)
	}
	scoring, _ := body["scoring"].(map[string]any)
	score, _ := scoring["score"].(map[string]any)
	if score == nil {
		t.Fatalf("three domains produced no score: %v", body)
	}
	inputs, _ := score["inputs"].(map[string]any)
	if inputs["domains"] != 3.0 {
		t.Fatalf("the score claims %v domains where three were assessed", inputs["domains"])
	}
	// Three domains, two of them zero and one of them one: 33.3, not 25 — which is what
	// counting the absent smoking domain as zero would have produced.
	if got := score["value"].(float64); got < 33 || got > 34 {
		t.Fatalf("the score reads %v; the missing domain was counted as good news", got)
	}
}

func TestTooFewDomainsProducesNoScoreRatherThanAWrongOne(t *testing.T) {
	// A "lifestyle risk" computed from one answer is a number whose name promises far more than
	// it holds. The response is still recorded — the answers are the record.
	h := newAPI(t)
	resp, body := h.auditC(t, "NEVER", "ONE_OR_TWO", "NEVER")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("answering answered %d: %v", resp.StatusCode, body)
	}
	scoring, _ := body["scoring"].(map[string]any)
	if scoring == nil {
		t.Fatalf("no scoring was reported at all: %v", body)
	}
	if scoring["score"] != nil {
		t.Fatalf("a composite was computed from one domain: %v", scoring["score"])
	}
	// And it says which domains are still needed, so the screen does not have to re-implement
	// which observation feeds which domain — a second copy of a server decision, which drifts.
	missing, _ := scoring["missing"].([]any)
	if len(missing) != 3 {
		t.Fatalf("the scoring names %d missing domains, want three", len(missing))
	}
	assessed, _ := scoring["assessed"].([]any)
	if len(assessed) != 1 || assessed[0] != "ALCOHOL" {
		t.Fatalf("the scoring says %v was assessed", assessed)
	}
	if scoring["minimum"] != 3.0 {
		t.Fatalf("the scoring reports a floor of %v", scoring["minimum"])
	}
	response, _ := body["response"].(map[string]any)
	if len(response["answers"].([]any)) != 3 {
		t.Fatal("the answers were lost because the score could not be computed")
	}
}

// --- the shapes an answer can be wrong ---

func TestTheCatalogueSaysWhereEachQuestionnaireCameFrom(t *testing.T) {
	// `READINESS_1` must never read as a validated instrument, and until this checkpoint's review
	// the only signal was a string comparison against seeded English prose — the most fragile
	// line in the client, holding up the rule that matters most.
	h := newAPI(t)
	_, body := h.call(t, "GET", "/v1/assessments/instruments", nil)
	found := map[string]string{}
	for _, raw := range body["instruments"].([]any) {
		row := raw.(map[string]any)
		found[row["code"].(string)] = row["provenance"].(string)
		// And the licence note reads in both languages. A refusal a counsellor cannot read is a
		// refusal they will work around.
		if row["licence_note"] == "" || row["licence_note_bn"] == "" {
			t.Fatalf("%v says why it may be used in one language only", row["code"])
		}
	}
	if found["READINESS_1"] != "THIS_CLINIC" {
		t.Fatalf("the clinic's own question reads as %v", found["READINESS_1"])
	}
	if found["AUDIT_C"] != "PUBLISHED" {
		t.Fatalf("the WHO's questionnaire reads as %v", found["AUDIT_C"])
	}
	// And the catalogue says when it last changed, so a tablet holding it for a morning can
	// re-check rather than meeting a republished version as a 422 after a patient has answered.
	if body["catalogue_version"] == nil {
		t.Fatal("the catalogue does not say when it last changed")
	}
}

func TestTheScoreCanBeRecomputedWithoutAnsweringAQuestionnaire(t *testing.T) {
	// The commonest sequence at this station is "type the four numbers, save", which can take a
	// patient from two assessed domains to four without any questionnaire being answered.
	h := newAPI(t)
	h.auditC(t, "TWO_TO_THREE_WEEKLY", "THREE_OR_FOUR", "MONTHLY")

	// One domain: no score.
	resp, body := h.call(t, "POST", "/v1/assessments/score",
		map[string]any{"patient_id": h.patient})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scoring answered %d: %v", resp.StatusCode, body)
	}
	scoring, _ := body["scoring"].(map[string]any)
	if scoring["score"] != nil {
		t.Fatalf("one domain produced a score: %v", scoring["score"])
	}

	// Now the numbers, and no questionnaire in sight.
	h.value(t, "SLEEP_MINUTES", 8, "h")
	h.value(t, "ACTIVE_MINUTES_WEEK", 200, "min")

	resp, body = h.call(t, "POST", "/v1/assessments/score",
		map[string]any{"patient_id": h.patient})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scoring answered %d: %v", resp.StatusCode, body)
	}
	scoring, _ = body["scoring"].(map[string]any)
	if scoring["score"] == nil {
		t.Fatalf("three domains still produced no score: %v", scoring)
	}
}

func TestAnAnswerMustBeTheShapeTheQuestionAsks(t *testing.T) {
	h := newAPI(t)
	resp, body := h.call(t, "POST", "/v1/assessments", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient, "instrument_code": "AUDIT_C",
		"answers": []map[string]any{
			{"item_code": "FREQUENCY", "value_num": 3},
			{"item_code": "TYPICAL_AMOUNT", "option_code": "ONE_OR_TWO"},
			{"item_code": "HEAVY_EPISODES", "option_code": "NEVER"},
		},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a number answering a coded question answered %d, want 422: %v", resp.StatusCode, body)
	}
}

func TestAnOptionThatIsNotOfferedIsRefused(t *testing.T) {
	h := newAPI(t)
	resp, _ := h.call(t, "POST", "/v1/assessments", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient, "instrument_code": "AUDIT_C",
		"answers": []map[string]any{
			{"item_code": "FREQUENCY", "option_code": "EVERY_OTHER_TUESDAY"},
			{"item_code": "TYPICAL_AMOUNT", "option_code": "ONE_OR_TWO"},
			{"item_code": "HEAVY_EPISODES", "option_code": "NEVER"},
		},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("an invented option answered %d, want 422", resp.StatusCode)
	}
}

func TestARequiredQuestionCannotBeLeftBlank(t *testing.T) {
	h := newAPI(t)
	resp, _ := h.call(t, "POST", "/v1/assessments", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient, "instrument_code": "AUDIT_C",
		"answers": []map[string]any{
			{"item_code": "FREQUENCY", "option_code": "NEVER"},
		},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a half-answered questionnaire answered %d, want 422", resp.StatusCode)
	}
}

func TestEveryQuestionAndEveryOptionReadsInBothLanguages(t *testing.T) {
	// Half this clinic's staff read the Bangla, and a questionnaire is read aloud to a patient
	// who reads neither — so the operator's own language is the one that matters here.
	h := newAPI(t)
	_, body := h.call(t, "GET", "/v1/assessments/instruments", nil)
	for _, raw := range body["instruments"].([]any) {
		row := raw.(map[string]any)
		if row["name_en"] == "" || row["name_bn"] == "" {
			t.Fatalf("%v is named in one language", row["code"])
		}
		if !row["usable"].(bool) {
			continue
		}
		for _, rawItem := range row["items"].([]any) {
			item := rawItem.(map[string]any)
			if item["prompt_en"] == "" || item["prompt_bn"] == "" {
				t.Fatalf("%v asks %v in one language", row["code"], item["item_code"])
			}
			options, _ := item["options"].([]any)
			for _, rawOption := range options {
				option := rawOption.(map[string]any)
				if option["label_en"] == "" || option["label_bn"] == "" {
					t.Fatalf("%v offers %v in one language", item["item_code"], option["option_code"])
				}
			}
		}
	}
}

func TestTheInvariantNoticesAResponseWithNoItems(t *testing.T) {
	h := newAPI(t)
	id := uuid.New()
	if _, err := h.SQL.Exec(`
		INSERT INTO read.instrument_response
		  (id, facility_id, patient_id, instrument_code, instrument_version,
		   recorded_at, recorded_by, event_id, global_seq)
		VALUES ($1, $2, $3, 'AUDIT_C', 1, now(), $4, $5, 999999)`,
		id, h.facility, h.patient, h.user, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`SELECT core.assert_every_response_kept_its_items()`); err == nil {
		t.Fatal("a questionnaire nobody filled in passed the invariant")
	}
}
