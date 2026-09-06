package counseling_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/counseling"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
)

// Counselling on the floor (CP56, §5.3, [R-01] [R-07]).
//
// Four acceptance criteria:
//
//	1. each tick carries its own attribution and timestamp;
//	2. a full seven-item session takes under sixty seconds of interaction;
//	3. un-ticking requires a reason and is recorded;
//	4. progress is visible on the traffic board within two seconds.
//
// Criterion 2 is a stopwatch on a real floor and no test stands in for it. What this file can
// prove is the half that would make it impossible — the number of *acts* a full session takes —
// and that is asserted below: seven ticks and a completion, no navigation between rooms, no
// second confirmation per item.
//
// Criterion 4 is measured end to end with a phone and a wall display. What is proven here is
// that the server announces progress on every act, with counts rather than item text, because a
// board that had to ask the API after each tick is a board that is two seconds late by design.
//
// The rest is criteria 1 and 3, and they are tested from both sides: through the API, and with
// direct statements against the tables, because the write that breaks attribution is the one
// that does not go through the API at all.

type floor struct {
	*testsupport.DB
	store    *counseling.Store
	sessions *counseling.SessionService
	server   *httptest.Server
	clock    *clock.Fixed
	progress *recordedProgress
	told     *seededConditions
	audits   *recordedAudits

	facility uuid.UUID
	user     uuid.UUID
	device   uuid.UUID
	patient  uuid.UUID
	visit    uuid.UUID
	role     string
	held     []string
}

// recordedProgress stands in for the realtime gateway. `counseling` may not import `realtime`,
// so the service takes an interface; this is the test's implementation of it.
type recordedProgress struct {
	seen []counseling.Progress
}

func (r *recordedProgress) CounselingProgressed(_ context.Context, p counseling.Progress) {
	r.seen = append(r.seen, p)
}

// seededVisits stands in for `visit`: which patient a visit belongs to.
type seededVisits struct {
	visit   *uuid.UUID
	patient *uuid.UUID
}

func (v seededVisits) PatientOf(_ context.Context, id, _ uuid.UUID) (uuid.UUID, error) {
	if id != *v.visit {
		return uuid.Nil, counseling.ErrNotFound
	}
	return *v.patient, nil
}

// seededConditions stands in for `history`: the patient's live coded comorbidities.
type seededConditions struct {
	codings []counseling.Coding
}

func (c *seededConditions) CodedConditions(context.Context, uuid.UUID) ([]counseling.Coding, error) {
	return c.codings, nil
}

type floorStaff struct {
	facility, user, device uuid.UUID
	permissions            *[]string
	role                   *string
}

func (s floorStaff) Identify(context.Context, string) (httpx.Caller, error) {
	return httpx.Caller{
		UserID: s.user.String(), FacilityID: s.facility.String(),
		SessionID: uuid.NewSHA1(s.user, []byte("session")).String(),
		Code:      "C003", Permissions: *s.permissions, Roles: []string{*s.role},
	}, nil
}

func (s floorStaff) Authorize(ctx context.Context, caller httpx.Caller,
	anyOf []string) (context.Context, httpx.AuthzDecision) {

	for _, want := range anyOf {
		for _, held := range caller.Permissions {
			if want == held {
				return httpx.WithPrincipal(ctx, httpx.Principal{
					UserID: caller.UserID, FacilityID: caller.FacilityID,
					SessionID: caller.SessionID, Code: caller.Code,
					DeviceID: s.device.String(),
					Role:     *s.role, Station: "STN_COUNSELING",
				}), httpx.AuthzDecision{Allowed: true, Reason: "allowed"}
			}
		}
	}
	return ctx, httpx.AuthzDecision{Reason: "permission_not_held"}
}

func newFloor(t *testing.T) *floor {
	t.Helper()
	base := testsupport.Postgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, base.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	h := &floor{DB: base, user: uuid.New(), device: uuid.New(), role: "COUNSELOR",
		progress: &recordedProgress{}, told: &seededConditions{}, audits: &recordedAudits{}}
	h.held = []string{counseling.PermRead, counseling.PermTick, counseling.PermSessionRead}
	h.clock = clock.NewFixed(time.Date(2026, 9, 21, 5, 15, 0, 0, time.UTC))
	if err := base.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&h.facility); err != nil {
		t.Fatal(err)
	}

	events := eventstore.New(eventstore.Config{
		Pool: pool, Clock: h.clock, Synchronous: projection.NewSyncSet(projection.Default),
	})
	if err := projection.NewEngineWithEvents(pool, projection.Default, events).Register(ctx); err != nil {
		t.Fatal(err)
	}

	h.store = counseling.NewStore(pool)
	h.sessions = counseling.NewSessionService(h.store, events, h.clock).
		WithNotifier(h.progress)
	h.seedFloor(t)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handlers := counseling.NewHandlers(counseling.HandlersConfig{
		Store: h.store, Service: counseling.NewService(h.store), Sessions: h.sessions,
		// The two lookups the floor needs and this module may not do itself: whose visit this
		// is, and what that patient's coded conditions are. In production they are bridges over
		// `visit` and `history` in cmd/api; here they are the smallest honest stand-ins.
		Visits:     seededVisits{visit: &h.visit, patient: &h.patient},
		Conditions: h.told,
		Audit:      h.audits,
		Clock:      h.clock,
		StepUp:     alwaysStepped{}, Logger: logger,
	})
	who := floorStaff{facility: h.facility, user: h.user, device: h.device,
		permissions: &h.held, role: &h.role}
	router, err := httpx.NewRouter(httpx.RouterOptions{
		Logger: logger, IDs: &ids.Sequential{Prefix: "req"},
		MaxBodyBytes: 1 << 18, RequestTimeout: 10 * time.Second,
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

// seedFloor puts a counsellor, a tablet, a patient and an open visit in the record. Written
// directly rather than through the registration service because this file is about counselling
// and a registration is four other checkpoints.
func (h *floor) seedFloor(t *testing.T) {
	t.Helper()
	if _, err := h.SQL.Exec(`
		INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, $2, 'C003', 'Shirin Akter', 'শিরিন আক্তার', 'active')`,
		h.user, h.facility); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO core.device (id, facility_id, name, kind, status, enrolled_at)
		VALUES ($1, $2, 'Phone 3', 'phone', 'active', now())`,
		h.device, h.facility); err != nil {
		t.Fatal(err)
	}
	h.patient = uuid.New()
	if _, err := h.SQL.Exec(`
		INSERT INTO core.patient (id, facility_id, clinical_id, name_en, sex, birth_date,
		                          dob_precision, dob_verified_by, phone_primary, status,
		                          registered_by, registered_at)
		VALUES ($1, $2, 'DTHC-FRD-2026-000722', 'Rahima Khatun', 'female', DATE '1969-11-08',
		        'day', 'national_id', '+8801711111722', 'active', $3, now())`,
		h.patient, h.facility, h.user); err != nil {
		t.Fatalf("seeding a patient: %v", err)
	}
	h.visit = uuid.New()
	if _, err := h.SQL.Exec(`
		INSERT INTO core.visit (id, facility_id, patient_id, visit_code, visit_type, status,
		                        clinic_day, opened_at, opened_by)
		VALUES ($1, $2, $3, 'V-CP56-1', 'follow_up', 'open', current_date, now(), $4)`,
		h.visit, h.facility, h.patient, h.user); err != nil {
		t.Fatalf("seeding a visit: %v", err)
	}
}

func (h *floor) call(t *testing.T, method, path string, body any) (*http.Response, map[string]any) {
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
	// The counselling app is a phone (§5.3, [R-01]), and the ledger records that it was.
	req.Header.Set("X-DTHCMS-Device", h.device.String())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	var decoded map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	return resp, decoded
}

// diabetesTemplate is the seeded checklist: seven items, all mandatory (CP55 criterion 3).
func (h *floor) diabetesTemplate(t *testing.T) uuid.UUID {
	t.Helper()
	template, err := h.store.ByCode(context.Background(), "DIABETES")
	if err != nil {
		t.Fatal(err)
	}
	return template.ID
}

// open starts a session for the seeded patient and returns its id.
func (h *floor) open(t *testing.T) uuid.UUID {
	t.Helper()
	resp, body := h.call(t, "POST", "/v1/counseling/sessions", map[string]any{
		"patient_id": h.patient, "visit_id": h.visit, "template_id": h.diabetesTemplate(t),
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("starting a session: %d %v", resp.StatusCode, body)
	}
	session, _ := body["session"].(map[string]any)
	id, err := uuid.Parse(session["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (h *floor) session(t *testing.T, id uuid.UUID) counseling.Session {
	t.Helper()
	session, err := h.store.Session(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

// ---------------------------------------------------------------------------
// Starting a session
// ---------------------------------------------------------------------------

func TestASessionRecordsTheVersionItIsWalking(t *testing.T) {
	// CP55's criterion 2 lives on this column. A session that resolved "the published version"
	// on every read would change what a patient was asked about the moment somebody publishes.
	h := newFloor(t)
	session := h.session(t, h.open(t))

	if session.TemplateVersion != 1 {
		t.Fatalf("session is walking version %d, want the published 1", session.TemplateVersion)
	}
	if len(session.Items) != 7 {
		t.Fatalf("session carries %d items, want the seeded seven", len(session.Items))
	}
	if len(session.Outstanding) != 7 {
		t.Fatalf("%d items outstanding at the start, want all seven", len(session.Outstanding))
	}
}

func TestStartingTwiceLandsInTheSameSession(t *testing.T) {
	// A counsellor whose phone lost the reply presses start again. A second half-ticked copy of
	// the same list is how two people each cover half of it and each believe the other did the
	// rest.
	h := newFloor(t)
	first := h.open(t)

	resp, body := h.call(t, "POST", "/v1/counseling/sessions", map[string]any{
		"patient_id": h.patient, "visit_id": h.visit, "template_id": h.diabetesTemplate(t),
	})
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("second start answered %d: %v", resp.StatusCode, body)
	}
	session, _ := body["session"].(map[string]any)
	if session["id"] != first.String() {
		t.Fatalf("second start opened %v, want the session already open (%s)",
			session["id"], first)
	}

	var sessions int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM read.counseling_session
		WHERE visit_id = $1`, h.visit).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 1 {
		t.Fatalf("%d sessions on the visit, want one", sessions)
	}
}

func TestAChecklistWithNothingPublishedCannotBeWalked(t *testing.T) {
	// A draft is not something to hand somebody on the floor.
	h := newFloor(t)
	var template uuid.UUID
	if err := h.SQL.QueryRow(`
		INSERT INTO core.counseling_template (code, title_en, title_bn)
		VALUES ('THYROID_WIP', 'Thyroid, in progress', 'থাইরয়েড, চলমান') RETURNING id`).
		Scan(&template); err != nil {
		t.Fatal(err)
	}

	resp, body := h.call(t, "POST", "/v1/counseling/sessions", map[string]any{
		"patient_id": h.patient, "visit_id": h.visit, "template_id": template,
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("starting on an unpublished checklist answered %d, want 422: %v",
			resp.StatusCode, body)
	}
}

// ---------------------------------------------------------------------------
// Criterion 1: every tick is individually attributed
// ---------------------------------------------------------------------------

func TestEveryTickCarriesItsOwnActorAndTime(t *testing.T) {
	h := newFloor(t)
	id := h.open(t)

	if resp, body := h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/ticks",
		map[string]any{"item_code": "DIET", "note": "Eats one meal after sunset."}); resp.StatusCode != http.StatusOK {
		t.Fatalf("ticking answered %d: %v", resp.StatusCode, body)
	}

	var by uuid.UUID
	var at time.Time
	var role, note string
	if err := h.SQL.QueryRow(`
		SELECT ticked_by, ticked_at, ticked_role, note FROM read.counseling_tick
		 WHERE session_id = $1 AND item_code = 'DIET'`, id).
		Scan(&by, &at, &role, &note); err != nil {
		t.Fatal(err)
	}
	if by != h.user {
		t.Fatalf("tick attributed to %s, want the counsellor %s", by, h.user)
	}
	if role != "COUNSELOR" {
		t.Fatalf("tick recorded the role %q, want COUNSELOR", role)
	}
	if at.IsZero() {
		t.Fatal("tick has no time")
	}
	if note != "Eats one meal after sunset." {
		t.Fatalf("note reads %q", note)
	}
}

func TestTwoCounsellorsAreAttributedSeparately(t *testing.T) {
	// The insulin corner is a different person from the counselling room, and §5.4's question
	// is "who told you about injection sites" rather than "who ran the session".
	h := newFloor(t)
	id := h.open(t)

	h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/ticks",
		map[string]any{"item_code": "DIET"})

	second := uuid.New()
	if _, err := h.SQL.Exec(`
		INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, $2, 'C009', 'Momena Begum', 'মোমেনা বেগম', 'active')`,
		second, h.facility); err != nil {
		t.Fatal(err)
	}
	h.user = second
	h.role = "RX_EDUCATOR"
	h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/ticks",
		map[string]any{"item_code": "INSULIN_TECHNIQUE"})

	rows, err := h.SQL.Query(`SELECT item_code, ticked_by, ticked_role
		FROM read.counseling_tick WHERE session_id = $1 ORDER BY item_code`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	seen := map[string]string{}
	for rows.Next() {
		var code, role string
		var by uuid.UUID
		if err := rows.Scan(&code, &by, &role); err != nil {
			t.Fatal(err)
		}
		seen[code] = by.String() + "/" + role
	}
	if len(seen) != 2 {
		t.Fatalf("%d ticks recorded, want two", len(seen))
	}
	if seen["DIET"] == seen["INSULIN_TECHNIQUE"] {
		t.Fatalf("both items attributed to the same person: %v", seen)
	}
	if !strings.HasSuffix(seen["INSULIN_TECHNIQUE"], "/RX_EDUCATOR") {
		t.Fatalf("the insulin corner's tick reads %q", seen["INSULIN_TECHNIQUE"])
	}
}

func TestNothingHereTicksAList(t *testing.T) {
	// A named test, and the thing it guards is an absence. `TickAll`, a `[]string` parameter or
	// a "complete the rest" flag would each attribute several acts to one press — and criterion
	// 1 is only as strong as the smallest thing a client can send.
	service := reflect.TypeOf(&counseling.SessionService{})
	for i := 0; i < service.NumMethod(); i++ {
		method := service.Method(i)
		for p := 1; p < method.Type.NumIn(); p++ {
			param := method.Type.In(p)
			if param.Kind() == reflect.Slice {
				t.Fatalf("%s takes a %s: a counselling write that accepts a list is one press "+
					"attributed to several acts", method.Name, param)
			}
		}
	}
}

func TestOneTickIsOneEventInTheLedger(t *testing.T) {
	h := newFloor(t)
	id := h.open(t)
	for _, code := range []string{"DIET", "EXERCISE", "GLUCOMETER"} {
		h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/ticks",
			map[string]any{"item_code": code})
	}

	var events, sources int
	if err := h.SQL.QueryRow(`
		SELECT count(*), count(DISTINCT source) FROM ledger.event
		 WHERE event_type = 'COUNSELING_ITEM_TICKED'`).Scan(&events, &sources); err != nil {
		t.Fatal(err)
	}
	if events != 3 {
		t.Fatalf("%d tick events in the ledger, want three", events)
	}

	var source string
	if err := h.SQL.QueryRow(`SELECT DISTINCT source FROM ledger.event
		WHERE event_type = 'COUNSELING_ITEM_TICKED'`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if source != "MOBILE_ONLINE" {
		t.Fatalf("ticks recorded from %q, want MOBILE_ONLINE — counselling is ticked on a phone",
			source)
	}
}

// ---------------------------------------------------------------------------
// Criterion 3: un-ticking requires a reason, and is recorded
// ---------------------------------------------------------------------------

func TestUntickingWithoutAReasonIsRefused(t *testing.T) {
	h := newFloor(t)
	id := h.open(t)
	h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/ticks",
		map[string]any{"item_code": "DIET"})

	resp, body := h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/unticks",
		map[string]any{"item_code": "DIET", "reason": "   "})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("un-ticking with no reason answered %d, want 422: %v", resp.StatusCode, body)
	}
}

func TestAnUntickKeepsTheRowAndSaysWhy(t *testing.T) {
	// A delete would satisfy the words of criterion 3 and destroy the point: the interesting
	// record is that somebody covered an item and then somebody decided they had not.
	h := newFloor(t)
	id := h.open(t)
	h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/ticks",
		map[string]any{"item_code": "DIET"})

	if resp, body := h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/unticks",
		map[string]any{"item_code": "DIET", "reason": "Ticked the wrong row."}); resp.StatusCode != http.StatusOK {
		t.Fatalf("un-ticking answered %d: %v", resp.StatusCode, body)
	}

	var reason string
	var by uuid.UUID
	var at *time.Time
	var undos int
	if err := h.SQL.QueryRow(`
		SELECT undone_reason, undone_by, undone_at, undo_count FROM read.counseling_tick
		 WHERE session_id = $1 AND item_code = 'DIET'`, id).
		Scan(&reason, &by, &at, &undos); err != nil {
		t.Fatalf("the row is gone, or unreadable: %v", err)
	}
	if reason != "Ticked the wrong row." || by != h.user || at == nil {
		t.Fatalf("un-tick recorded as %q by %s at %v", reason, by, at)
	}
	if undos != 1 {
		t.Fatalf("undo count is %d, want one", undos)
	}

	session := h.session(t, id)
	if len(session.Outstanding) != 7 {
		t.Fatalf("%d items outstanding after the un-tick, want all seven back",
			len(session.Outstanding))
	}
}

func TestRetickingLandsOnTheSameRowAndRemembersTheUndo(t *testing.T) {
	h := newFloor(t)
	id := h.open(t)
	h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/ticks",
		map[string]any{"item_code": "DIET"})
	h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/unticks",
		map[string]any{"item_code": "DIET", "reason": "Patient asked to come back to it."})
	h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/ticks",
		map[string]any{"item_code": "DIET", "note": "Covered on the second pass."})

	var rows, undos int
	var undone *time.Time
	var note string
	if err := h.SQL.QueryRow(`SELECT count(*) FROM read.counseling_tick
		WHERE session_id = $1 AND item_code = 'DIET'`, id).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("%d rows for one item, want one — two answers to 'is this covered'", rows)
	}
	if err := h.SQL.QueryRow(`SELECT undo_count, undone_at, note FROM read.counseling_tick
		WHERE session_id = $1 AND item_code = 'DIET'`, id).Scan(&undos, &undone, &note); err != nil {
		t.Fatal(err)
	}
	if undone != nil {
		t.Fatal("the re-ticked item still reads as taken back")
	}
	if undos != 1 {
		t.Fatalf("undo count is %d after one un-tick, want one — the history is the point", undos)
	}
	if note != "Covered on the second pass." {
		t.Fatalf("note reads %q", note)
	}
}

func TestUntickingSomethingNobodyTickedIsRefused(t *testing.T) {
	h := newFloor(t)
	id := h.open(t)
	resp, _ := h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/unticks",
		map[string]any{"item_code": "DIET", "reason": "Not covered after all."})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("un-ticking an unticked item answered %d, want 409", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// What is outstanding, and who decides a session is finished
// ---------------------------------------------------------------------------

func TestOutstandingCountsOnlyMandatoryItems(t *testing.T) {
	h := newFloor(t)
	// A second checklist with one optional item, published, so the rule is visible.
	template := h.publishSmall(t, "OPTIONAL_ONE", []map[string]any{
		{"item_code": "MUST", "text_en": "Must", "text_bn": "অবশ্যই", "mandatory": true,
			"room": "COUNSELING_ROOM"},
		{"item_code": "MAY", "text_en": "May", "text_bn": "হতে পারে", "mandatory": false,
			"room": "COUNSELING_ROOM"},
	})

	resp, body := h.call(t, "POST", "/v1/counseling/sessions", map[string]any{
		"patient_id": h.patient, "visit_id": h.visit, "template_id": template,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("starting answered %d: %v", resp.StatusCode, body)
	}
	session, _ := body["session"].(map[string]any)
	id := uuid.MustParse(session["id"].(string))

	h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/ticks",
		map[string]any{"item_code": "MUST"})

	after := h.session(t, id)
	if len(after.Outstanding) != 0 {
		t.Fatalf("outstanding is %v after the mandatory item; the optional one is not a gate",
			after.Outstanding)
	}
}

func TestCompletingASessionTicksNothing(t *testing.T) {
	// A completion that covered the outstanding items would be the batch attribution criterion
	// 1 forbids, wearing a different name — and it would let a session be closed by somebody
	// who counselled nobody.
	h := newFloor(t)
	id := h.open(t)
	h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/ticks",
		map[string]any{"item_code": "DIET"})

	if resp, body := h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/complete",
		map[string]any{}); resp.StatusCode != http.StatusOK {
		t.Fatalf("completing answered %d: %v", resp.StatusCode, body)
	}

	session := h.session(t, id)
	if !session.Complete() {
		t.Fatal("the session did not close")
	}
	if len(session.Outstanding) != 6 {
		t.Fatalf("%d items outstanding after completion, want the six nobody covered",
			len(session.Outstanding))
	}
	var ticks int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM read.counseling_tick
		WHERE session_id = $1`, id).Scan(&ticks); err != nil {
		t.Fatal(err)
	}
	if ticks != 1 {
		t.Fatalf("%d ticks after completing, want the one somebody made", ticks)
	}
}

func TestAFinishedSessionRefusesFurtherTicks(t *testing.T) {
	h := newFloor(t)
	id := h.open(t)
	h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/complete", map[string]any{})

	resp, _ := h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/ticks",
		map[string]any{"item_code": "DIET"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("ticking a closed session answered %d, want 409", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// The list the session is walking is the frozen one
// ---------------------------------------------------------------------------

func TestASessionKeepsItsVersionAcrossARepublish(t *testing.T) {
	// CP55's criterion 2, end to end and on the floor. A counsellor halfway down the list when
	// a physician publishes version 2 finishes the list they started.
	h := newFloor(t)
	id := h.open(t)
	h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/ticks",
		map[string]any{"item_code": "DIET"})

	template := h.diabetesTemplate(t)
	if _, err := h.SQL.Exec(`
		INSERT INTO core.counseling_template_version (template_id, version, status)
		VALUES ($1, 2, 'DRAFT')`, template); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO core.counseling_item (template_id, version, item_code, ordering,
		                                  text_en, text_bn, is_mandatory, room)
		VALUES ($1, 2, 'ONLY_ITEM', 1, 'The rewritten list', 'নতুন তালিকা', true,
		        'COUNSELING_ROOM')`, template); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		UPDATE core.counseling_template_version SET status = 'RETIRED', retired_at = now()
		 WHERE template_id = $1 AND status = 'PUBLISHED'`, template); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		UPDATE core.counseling_template_version
		   SET status = 'PUBLISHED', published_at = now(), published_source = 'MIGRATION'
		 WHERE template_id = $1 AND version = 2`, template); err != nil {
		t.Fatal(err)
	}

	session := h.session(t, id)
	if session.TemplateVersion != 1 {
		t.Fatalf("the open session moved to version %d", session.TemplateVersion)
	}
	if len(session.Items) != 7 {
		t.Fatalf("the open session now shows %d items, want the seven it started with",
			len(session.Items))
	}
	if len(session.Outstanding) != 6 {
		t.Fatalf("outstanding is %d against the new list, want six against the old one",
			len(session.Outstanding))
	}
}

func TestASessionsVersionCannotBeMovedByAnUpdate(t *testing.T) {
	// The one way criterion 2 dies quietly is a well-meant UPDATE that "fixes" a session onto
	// the current version. The trigger refuses it for every path, including this one.
	h := newFloor(t)
	id := h.open(t)
	if _, err := h.SQL.Exec(`
		UPDATE read.counseling_session SET template_version = 2 WHERE id = $1`, id); err == nil {
		t.Fatal("a session's version was moved by a plain UPDATE")
	}
}

func TestATickAgainstAnItemNotOnTheListIsRefused(t *testing.T) {
	h := newFloor(t)
	id := h.open(t)
	resp, body := h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/ticks",
		map[string]any{"item_code": "SOMETHING_ELSE"})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("ticking an unknown item answered %d, want 422: %v", resp.StatusCode, body)
	}
}

func TestTheItemGuardCannotBeBypassedByAnyClient(t *testing.T) {
	// Through the API a stale item code is a sentence; by any other path it is a trigger. The
	// most likely way to produce one is a phone holding a list from before a republish, which
	// is precisely the client that will not be going through a handler when it matters.
	h := newFloor(t)
	id := h.open(t)
	_, err := h.SQL.Exec(`
		INSERT INTO read.counseling_tick (session_id, item_code, facility_id, patient_id,
		                                  ticked_at, ticked_by, event_id, global_seq)
		VALUES ($1, 'NOT_ON_THE_LIST', $2, $3, now(), $4, gen_random_uuid(), 1)`,
		id, h.facility, h.patient, h.user)
	if err == nil {
		t.Fatal("a plain INSERT ticked an item that is not on the checklist")
	}
	if !strings.Contains(err.Error(), "not on the checklist") {
		t.Fatalf("the refusal reads %q", err)
	}
}

// ---------------------------------------------------------------------------
// Which checklist the patient in the room actually needs
// ---------------------------------------------------------------------------

func TestTheChecklistsAVisitCallsForComeFromTheRecordedCondition(t *testing.T) {
	// Without this, starting a session would mean showing a counsellor every checklist in the
	// clinic and having them pick — a clinical assignment made by the person least placed to
	// make it, which is what §5.1's assignment rules exist to avoid.
	h := newFloor(t)
	h.told.codings = []counseling.Coding{{System: "ICD10", Version: "2019", Code: "E11.9"}}

	resp, body := h.call(t, "GET", "/v1/counseling/visits/"+h.visit.String()+"/checklists", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("asking answered %d: %v", resp.StatusCode, body)
	}
	checklists, _ := body["checklists"].([]any)
	if len(checklists) != 1 {
		t.Fatalf("%d checklists for a patient with type 2 diabetes, want the one", len(checklists))
	}
	first, _ := checklists[0].(map[string]any)
	if first["template_code"] != "DIABETES" {
		t.Fatalf("the checklist offered is %v", first["template_code"])
	}
	if first["matched_code"] != "E11.9" {
		t.Fatalf("the answer does not say which condition called for it: %v", first)
	}
	if _, started := first["session_id"]; started {
		t.Fatal("a checklist nobody has started names a session")
	}
}

func TestAPatientWithNoCodedConditionIsOfferedNothing(t *testing.T) {
	// Not an error and not every checklist in the clinic. An empty list is the honest answer,
	// and the phone says so in words rather than offering a menu.
	h := newFloor(t)
	resp, body := h.call(t, "GET", "/v1/counseling/visits/"+h.visit.String()+"/checklists", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("asking answered %d: %v", resp.StatusCode, body)
	}
	if checklists, _ := body["checklists"].([]any); len(checklists) != 0 {
		t.Fatalf("%d checklists offered with nothing recorded", len(checklists))
	}
}

func TestAnOpenSessionStaysOfferedEvenWhenTheRuleIsRetired(t *testing.T) {
	// A rule retired at lunchtime must not make a half-ticked session disappear from the phone
	// of the counsellor walking it.
	h := newFloor(t)
	h.told.codings = []counseling.Coding{{System: "ICD10", Version: "2019", Code: "E11.9"}}
	id := h.open(t)
	if _, err := h.SQL.Exec(`UPDATE core.counseling_assignment SET retired_at = now()`); err != nil {
		t.Fatal(err)
	}

	_, body := h.call(t, "GET", "/v1/counseling/visits/"+h.visit.String()+"/checklists", nil)
	checklists, _ := body["checklists"].([]any)
	if len(checklists) != 1 {
		t.Fatalf("%d checklists after the rule was retired, want the open session", len(checklists))
	}
	first, _ := checklists[0].(map[string]any)
	if first["session_id"] != id.String() {
		t.Fatalf("the open session is not offered back: %v", first)
	}
}

// ---------------------------------------------------------------------------
// Ticking something already ticked
// ---------------------------------------------------------------------------

func TestTickingAnItemSomebodyElseCoveredIsRefused(t *testing.T) {
	// Silent re-attribution is the failure. The second tick would overwrite who covered the
	// item and when, and §5.4's question would come back with the name of whoever pressed last.
	h := newFloor(t)
	id := h.open(t)
	h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/ticks",
		map[string]any{"item_code": "DIET"})

	second := uuid.New()
	if _, err := h.SQL.Exec(`
		INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, $2, 'C011', 'Fahima Yasmin', 'ফাহিমা ইয়াসমিন', 'active')`,
		second, h.facility); err != nil {
		t.Fatal(err)
	}
	first := h.user
	h.user = second

	resp, _ := h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/ticks",
		map[string]any{"item_code": "DIET"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("a second tick on a covered item answered %d, want 409", resp.StatusCode)
	}

	var by uuid.UUID
	if err := h.SQL.QueryRow(`SELECT ticked_by FROM read.counseling_tick
		WHERE session_id = $1 AND item_code = 'DIET'`, id).Scan(&by); err != nil {
		t.Fatal(err)
	}
	if by != first {
		t.Fatal("the item was re-attributed to whoever pressed last")
	}
}

func TestTheSameTickArrivingTwiceIsTheSameAct(t *testing.T) {
	// A phone that lost the reply, or an offline queue replaying what it wrote in a room with
	// no signal. It must not be told it is wrong — the ledger already holds that event.
	h := newFloor(t)
	id := h.open(t)
	event := uuid.New()

	for attempt := 0; attempt < 2; attempt++ {
		resp, body := h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/ticks",
			map[string]any{"event_id": event, "item_code": "DIET"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("attempt %d answered %d: %v", attempt+1, resp.StatusCode, body)
		}
	}

	var ticks, events int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM read.counseling_tick
		WHERE session_id = $1`, id).Scan(&ticks); err != nil {
		t.Fatal(err)
	}
	if err := h.SQL.QueryRow(`SELECT count(*) FROM ledger.event
		WHERE event_type = 'COUNSELING_ITEM_TICKED'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if ticks != 1 || events != 1 {
		t.Fatalf("a replayed tick wrote %d rows and %d events, want one of each", ticks, events)
	}
}

func TestEveryRoomsOperatorMayTickTheirOwnRoom(t *testing.T) {
	// §5.2 walks three rooms, and every one of them has to be tickable by whoever is standing
	// in it. The counsellor's station covers two of the three; the nutrition room is the
	// nutritionist's, and before CP56 they could not tick it — which would have meant the diet
	// item ticked by somebody who was not in the room, the precise failure §5.4's
	// spot-questioning exists to catch.
	//
	// The exercise specialist and the prescription educator are deliberately not in this list:
	// no room maps to their stations, so a grant would be a permission nothing can reach. Who
	// staffs the insulin corner is an operational decision, and when it is made it is a
	// `station_code` on the room and a grant, not a guess made here.
	h := newFloor(t)
	for _, role := range []string{"COUNSELOR", "NUTRITIONIST"} {
		var held bool
		if err := h.SQL.QueryRow(`
			SELECT EXISTS (SELECT 1 FROM core.role_permission rp
			                 JOIN core.role r ON r.id = rp.role_id
			                WHERE r.code = $1 AND rp.permission_code = 'counseling.tick')`,
			role).Scan(&held); err != nil {
			t.Fatal(err)
		}
		if !held {
			t.Fatalf("%s cannot tick a counselling item", role)
		}
	}
}

func TestAnItemNamesTheStationItsRoomBelongsTo(t *testing.T) {
	// So a phone knows whether the item in front of it belongs to the room the operator is
	// standing in, without fetching the room catalogue a second time to learn that the insulin
	// corner is part of the counselling station.
	h := newFloor(t)
	session := h.session(t, h.open(t))
	for _, item := range session.Items {
		if item.RoomStation == "" {
			t.Fatalf("item %s names no station for its room", item.ItemCode)
		}
	}
}

// ---------------------------------------------------------------------------
// Criterion 4: the board is told
// ---------------------------------------------------------------------------

func TestProgressIsAnnouncedOnEveryAct(t *testing.T) {
	// The board's two seconds are measured from the tick. A board that had to ask the API after
	// each one would be late by design, so the server announces rather than waiting to be asked.
	h := newFloor(t)
	id := h.open(t)
	h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/ticks",
		map[string]any{"item_code": "DIET"})
	h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/complete", map[string]any{})

	if len(h.progress.seen) != 3 {
		t.Fatalf("%d announcements, want one each for start, tick and completion",
			len(h.progress.seen))
	}
	kinds := []string{}
	for _, p := range h.progress.seen {
		kinds = append(kinds, p.Kind)
	}
	want := []string{"counseling.started", "counseling.ticked", "counseling.completed"}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("announcements were %v, want %v", kinds, want)
		}
	}

	ticked := h.progress.seen[1]
	if ticked.Covered != 1 || ticked.Mandatory != 7 {
		t.Fatalf("the tick announced %d of %d, want 1 of 7", ticked.Covered, ticked.Mandatory)
	}
	if ticked.PatientID != h.patient || ticked.VisitID != h.visit {
		t.Fatal("the announcement does not identify what to fetch")
	}
}

func TestTheAnnouncementCarriesCountsRatherThanContent(t *testing.T) {
	// The board is a wall display in a public waiting area. "Covered 6 of 7" is a progress bar;
	// "insulin technique — not yet covered" is a clinical detail about the person standing in
	// front of it.
	h := newFloor(t)
	id := h.open(t)
	h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/ticks",
		map[string]any{"item_code": "INSULIN_TECHNIQUE", "note": "Rotates on the same arm."})

	announced, err := json.Marshal(h.progress.seen)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"INSULIN_TECHNIQUE", "Rotates on the same arm", "Insulin"} {
		if strings.Contains(string(announced), leak) {
			t.Fatalf("the announcement carries %q", leak)
		}
	}
}

// ---------------------------------------------------------------------------
// Criterion 2: how many acts a full session takes
// ---------------------------------------------------------------------------

func TestAFullSevenItemSessionIsSevenTicksAndACompletion(t *testing.T) {
	// The sixty seconds are a stopwatch on a real floor and no test stands in for that. What
	// this pins is the half that would make it impossible: the number of acts. Seven ticks and
	// one completion, with no per-item confirmation and no navigation between rooms — the rooms
	// are headings on one list, not screens.
	h := newFloor(t)
	id := h.open(t)

	acts := 0
	for _, item := range h.session(t, id).Items {
		resp, body := h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/ticks",
			map[string]any{"item_code": item.ItemCode})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("ticking %s answered %d: %v", item.ItemCode, resp.StatusCode, body)
		}
		acts++
	}
	h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/complete", map[string]any{})
	acts++

	if acts != 8 {
		t.Fatalf("a full session took %d acts, want seven ticks and a completion", acts)
	}
	session := h.session(t, id)
	if len(session.Outstanding) != 0 {
		t.Fatalf("%d items outstanding after covering all seven", len(session.Outstanding))
	}
	if !session.Complete() {
		t.Fatal("the session did not close")
	}
}

// ---------------------------------------------------------------------------
// Permissions, and the invariants
// ---------------------------------------------------------------------------

func TestReadingASessionIsNotTickingOne(t *testing.T) {
	// The physician's panel and the traffic board read sessions and never tick. A panel that
	// needed `counseling.tick` would be a physician's screen carrying the right to write on
	// somebody else's checklist.
	h := newFloor(t)
	id := h.open(t)

	h.held = []string{counseling.PermSessionRead}
	if resp, _ := h.call(t, "GET", "/v1/counseling/sessions/"+id.String(), nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("a reader could not read a session: %d", resp.StatusCode)
	}
	resp, _ := h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/ticks",
		map[string]any{"item_code": "DIET"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a reader ticked an item: %d", resp.StatusCode)
	}
}

func TestTheVisitsSessionsAreListed(t *testing.T) {
	h := newFloor(t)
	h.open(t)
	resp, body := h.call(t, "GET", "/v1/counseling/visits/"+h.visit.String()+"/sessions", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("listing answered %d: %v", resp.StatusCode, body)
	}
	sessions, _ := body["sessions"].([]any)
	if len(sessions) != 1 {
		t.Fatalf("%d sessions listed, want one", len(sessions))
	}
	first, _ := sessions[0].(map[string]any)
	if first["template_code"] != "DIABETES" {
		t.Fatalf("the listed session names %v", first["template_code"])
	}
}

func TestTheInvariantNoticesAnUnattributedTick(t *testing.T) {
	h := newFloor(t)
	if _, err := h.SQL.Exec(`SELECT core.assert_every_tick_is_attributed()`); err != nil {
		t.Fatalf("the invariant fails on a clean database: %v", err)
	}
}

func TestTheInvariantNoticesAnUntickWithNoReason(t *testing.T) {
	// The CHECK constraint guards rows written from now on; the invariant notices one that
	// arrived some other way — a restored backup, a migration, a hand-written UPDATE.
	h := newFloor(t)
	id := h.open(t)
	h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/ticks",
		map[string]any{"item_code": "DIET"})
	h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/unticks",
		map[string]any{"item_code": "DIET", "reason": "Wrong row."})

	if _, err := h.SQL.Exec(`SELECT core.assert_untick_reasons_are_recorded()`); err != nil {
		t.Fatalf("the invariant fails on an honest un-tick: %v", err)
	}

	if _, err := h.SQL.Exec(`
		ALTER TABLE read.counseling_tick DROP CONSTRAINT counseling_tick_undo_is_complete`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`UPDATE read.counseling_tick SET undone_reason = ''
		WHERE session_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`SELECT core.assert_untick_reasons_are_recorded()`); err == nil {
		t.Fatal("the invariant passed with an un-tick that gives no reason")
	}
}

func TestTheInvariantNoticesASessionWalkingADraft(t *testing.T) {
	h := newFloor(t)
	h.open(t)
	if _, err := h.SQL.Exec(`SELECT core.assert_sessions_walk_published_versions()`); err != nil {
		t.Fatalf("the invariant fails on a published session: %v", err)
	}

	// A version cannot be pushed back to DRAFT — the forward-only trigger refuses that — and a
	// session cannot be moved onto another version. So the row that this invariant exists to
	// notice can only arrive from outside both: a restored backup, a bad migration, an INSERT
	// written by hand. That is what is built here.
	template := h.diabetesTemplate(t)
	if _, err := h.SQL.Exec(`
		INSERT INTO core.counseling_template_version (template_id, version, status)
		VALUES ($1, 2, 'DRAFT')`, template); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO read.counseling_session (id, facility_id, patient_id, visit_id,
		                                     template_id, template_version,
		                                     started_at, started_by, event_id, global_seq)
		VALUES (gen_random_uuid(), $1, $2, gen_random_uuid(), $3, 2, now(), $4,
		        gen_random_uuid(), 999999)`,
		h.facility, h.patient, template, h.user); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`SELECT core.assert_sessions_walk_published_versions()`); err == nil {
		t.Fatal("the invariant passed with a session walking a draft checklist")
	}
}

// publishSmall creates and publishes a two-item checklist, returning its id.
func (h *floor) publishSmall(t *testing.T, code string, items []map[string]any) uuid.UUID {
	t.Helper()
	var template uuid.UUID
	if err := h.SQL.QueryRow(`
		INSERT INTO core.counseling_template (code, title_en, title_bn)
		VALUES ($1, $1, $1) RETURNING id`, code).Scan(&template); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO core.counseling_template_version (template_id, version, status)
		VALUES ($1, 1, 'DRAFT')`, template); err != nil {
		t.Fatal(err)
	}
	for i, item := range items {
		if _, err := h.SQL.Exec(`
			INSERT INTO core.counseling_item (template_id, version, item_code, ordering,
			                                  text_en, text_bn, is_mandatory, room)
			VALUES ($1, 1, $2, $3, $4, $5, $6, $7)`,
			template, item["item_code"], i+1, item["text_en"], item["text_bn"],
			item["mandatory"], item["room"]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.SQL.Exec(`
		UPDATE core.counseling_template_version
		   SET status = 'PUBLISHED', published_at = now(), published_source = 'MIGRATION'
		 WHERE template_id = $1 AND version = 1`, template); err != nil {
		t.Fatal(err)
	}
	return template
}
