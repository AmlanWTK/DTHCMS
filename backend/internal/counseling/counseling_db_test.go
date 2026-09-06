package counseling_test

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

	"github.com/AmlanWTK/DTHCMS/backend/internal/counseling"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
)

// Counselling templates (CP55, §5.1, [R-07]).
//
// Four acceptance criteria:
//
//	1. a new template can be authored and published by a physician without a code change;
//	2. completed sessions retain the template version used;
//	3. the diabetes template with all seven items is seeded;
//	4. items exist in both languages before publishing is allowed.
//
// Criterion 2 is the one worth reading closely, and it is the reason most of this file exists.
// It is easy to satisfy its words with a `version` column and destroy it with one `UPDATE`: an
// author fixes a typo in a live template, and every session that ever referenced it is now
// describing a checklist that never existed. Six months of counselling audit becomes unreadable
// and nothing looks wrong.
//
// So the tests below check the freeze from both sides — through the API, and with a direct
// statement against the table, because the edit that breaks this is a well-meaning one made by
// somebody who is not going through the API at all.

type api struct {
	*testsupport.DB
	store    *counseling.Store
	service  *counseling.Service
	server   *httptest.Server
	facility uuid.UUID
	user     uuid.UUID
	role     string
	held     []string
	audits   *recordedAudits
}

// recordedAudits stands in for the security trail. `counseling` may not import `audit`, so the
// handler takes an interface; this is the test's implementation of it.
type recordedAudits struct {
	published  []counseling.Publication
	overridden []counseling.GateOverride
}

func (r *recordedAudits) TemplatePublished(_ context.Context, p counseling.Publication) error {
	r.published = append(r.published, p)
	return nil
}

func (r *recordedAudits) GateOverridden(_ context.Context, o counseling.GateOverride) error {
	r.overridden = append(r.overridden, o)
	return nil
}

// alwaysStepped stands in for the second-factor check. The step-up is real in production and is
// asserted by the route table in the contract test; what this file is about is what publishing
// does once somebody is allowed to do it.
type alwaysStepped struct{}

func (alwaysStepped) ConsumeStepUp(context.Context, string, string, string) error { return nil }

type staff struct {
	facility, user uuid.UUID
	permissions    *[]string
	role           *string
}

func (s staff) Identify(context.Context, string) (httpx.Caller, error) {
	return httpx.Caller{
		UserID: s.user.String(), FacilityID: s.facility.String(),
		SessionID: uuid.NewSHA1(s.user, []byte("session")).String(),
		Code:      "P001", Permissions: *s.permissions, Roles: []string{*s.role},
	}, nil
}

func (s staff) Authorize(ctx context.Context, caller httpx.Caller, anyOf []string) (context.Context, httpx.AuthzDecision) {
	for _, want := range anyOf {
		for _, held := range caller.Permissions {
			if want == held {
				return httpx.WithPrincipal(ctx, httpx.Principal{
					UserID: caller.UserID, FacilityID: caller.FacilityID,
					SessionID: caller.SessionID, Code: caller.Code, Role: *s.role,
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

	h := &api{DB: base, user: uuid.New(), role: "PHYSICIAN", audits: &recordedAudits{}}
	h.held = []string{counseling.PermRead, counseling.PermWrite, counseling.PermPublish}
	if err := base.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&h.facility); err != nil {
		t.Fatal(err)
	}
	if _, err := base.SQL.Exec(`
		INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, $2, 'P001', 'Dr Test', 'ডা. পরীক্ষা', 'active')`,
		h.user, h.facility); err != nil {
		t.Fatal(err)
	}

	h.store = counseling.NewStore(pool)
	h.service = counseling.NewService(h.store)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handlers := counseling.NewHandlers(counseling.HandlersConfig{
		Store: h.store, Service: h.service, Audit: h.audits,
		StepUp: alwaysStepped{}, Logger: logger,
	})
	who := staff{facility: h.facility, user: h.user, permissions: &h.held, role: &h.role}
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
	req.Header.Set("X-Step-Up-Token", "test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	var decoded map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	return resp, decoded
}

func (h *api) diabetes(t *testing.T) counseling.Template {
	t.Helper()
	template, err := h.store.ByCode(context.Background(), "DIABETES")
	if err != nil {
		t.Fatal(err)
	}
	return template
}

// item is one draft item, complete enough to publish.
func item(code, room string, mandatory bool) map[string]any {
	return map[string]any{
		"item_code": code, "text_en": "Cover " + code, "text_bn": "বিষয়: " + code,
		"mandatory": mandatory, "room": room,
	}
}

// ---------------------------------------------------------------------------
// Criterion 3: the diabetes template is seeded
// ---------------------------------------------------------------------------

func TestTheDiabetesTemplateIsSeededWithSevenItems(t *testing.T) {
	// §5.1 names seven. Not "at least seven": the list is the launch minimum and a missing one
	// is a topic no counsellor is prompted to cover.
	h := newAPI(t)

	template := h.diabetes(t)
	version, err := h.store.Published(context.Background(), template.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(version.Items) != 7 {
		t.Fatalf("the diabetes template has %d items; section 5.1 names seven", len(version.Items))
	}

	want := []string{
		"DISEASE_UNDERSTANDING", "COMPLICATIONS", "DIET", "EXERCISE",
		"SELF_CARE", "GLUCOMETER", "INSULIN_TECHNIQUE",
	}
	for i, code := range want {
		if version.Items[i].ItemCode != code {
			t.Errorf("item %d is %s; section 5.1's order is %s", i+1, version.Items[i].ItemCode, code)
		}
	}
}

func TestTheSeededTemplateIsNotPresentedAsApproved(t *testing.T) {
	// D-53 is open. The seven items are transcribed from the blueprint and their Bengali is
	// mine; a version reporting itself approved would let an interface present a proposal as a
	// clinician's decision.
	h := newAPI(t)

	version, err := h.store.Published(context.Background(), h.diabetes(t).ID)
	if err != nil {
		t.Fatal(err)
	}
	if version.Approved() {
		t.Error("the seeded diabetes template reports itself approved; D-53 is open")
	}
	// And it says a migration published it rather than naming somebody who did not.
	if version.PublishedSource != "MIGRATION" {
		t.Errorf("the seeded version says it was published by %q", version.PublishedSource)
	}
	if version.PublishedBy != "" {
		t.Error("the seeded version names a person as its publisher; nobody published it")
	}
}

func TestEveryItemHasARoomAndTheRoomsAreInSequence(t *testing.T) {
	// §5.2's flow. The grouping is what a station screen follows, so an item in no room is an
	// item a counsellor meets in the wrong place.
	h := newAPI(t)

	resp, decoded := h.do(t, http.MethodGet, "/v1/counseling/rooms", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, decoded)
	}
	rooms, _ := decoded["rooms"].([]any)
	if len(rooms) != 3 {
		t.Fatalf("expected three rooms, got %d", len(rooms))
	}
	first, _ := rooms[0].(map[string]any)
	last, _ := rooms[2].(map[string]any)
	if first["room"] != "COUNSELING_ROOM" || last["room"] != "INSULIN_CORNER" {
		t.Errorf("the sequence is %v ... %v", first["room"], last["room"])
	}

	version, err := h.store.Published(context.Background(), h.diabetes(t).ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range version.Items {
		if item.Room == "" || item.RoomEN == "" {
			t.Errorf("%s is in no room", item.ItemCode)
		}
	}
}

// ---------------------------------------------------------------------------
// Criterion 2: a published version is frozen
// ---------------------------------------------------------------------------

func TestAPublishedVersionCannotBeEditedThroughTheApi(t *testing.T) {
	h := newAPI(t)
	template := h.diabetes(t)

	resp, decoded := h.do(t, http.MethodPut,
		"/v1/counseling/templates/"+template.ID.String()+"/versions/1",
		map[string]any{"items": []any{item("DIET", "NUTRITION_ROOM", true)}})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("editing a published version answered %d, not 409: %v", resp.StatusCode, decoded)
	}
}

func TestAPublishedVersionCannotBeEditedByAnyPath(t *testing.T) {
	// The edit that actually breaks criterion 2 is not an API call. It is somebody fixing a typo
	// with a statement, at three in the morning, in a database console — and afterwards every
	// session that referenced this version is describing a checklist that never existed.
	h := newAPI(t)

	if _, err := h.SQL.Exec(`
		UPDATE core.counseling_item SET text_en = 'a small correction'
		 WHERE item_code = 'GLUCOMETER'`); err == nil {
		t.Error("a published item was edited directly; criterion 2 is a convention, not a rule")
	}
	if _, err := h.SQL.Exec(`
		DELETE FROM core.counseling_item WHERE item_code = 'GLUCOMETER'`); err == nil {
		t.Error("a published item was deleted directly")
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO core.counseling_item
		  (template_id, version, item_code, ordering, text_en, text_bn, is_mandatory, room)
		SELECT id, 1, 'SNEAKED_IN', 99, 'x', 'x', true, 'COUNSELING_ROOM'
		  FROM core.counseling_template WHERE code = 'DIABETES'`); err == nil {
		t.Error("an item was added to a published version")
	}
}

func TestAVersionCannotBeUnpublished(t *testing.T) {
	// Un-publishing would leave sessions pointing at a version the floor can no longer see,
	// which is worse than retiring it: retired still reads.
	h := newAPI(t)

	if _, err := h.SQL.Exec(`
		UPDATE core.counseling_template_version SET status = 'DRAFT'
		 WHERE status = 'PUBLISHED'`); err == nil {
		t.Error("a published version was moved back to draft")
	}
}

func TestANewVersionCarriesTheOldItemsForward(t *testing.T) {
	// A new version exists to change one thing. Starting empty would mean retyping seven items
	// to fix a typo in one, and a system that makes the safe path expensive gets the unsafe one.
	h := newAPI(t)
	template := h.diabetes(t)

	resp, decoded := h.do(t, http.MethodPost,
		"/v1/counseling/templates/"+template.ID.String()+"/versions",
		map[string]any{"notes": "fixing the glucometer wording"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("drafting: %d %v", resp.StatusCode, decoded)
	}
	version, _ := decoded["version"].(map[string]any)
	if version["version"] != float64(2) {
		t.Fatalf("the new draft is version %v", version["version"])
	}
	items, _ := version["items"].([]any)
	if len(items) != 7 {
		t.Errorf("the draft copied %d of seven items", len(items))
	}
}

func TestPublishingRetiresTheVersionItReplaces(t *testing.T) {
	// One live checklist at a time. Two would make "which one does a new session get" a question
	// with two answers, and the wrong one would be whichever the query sorted first.
	h := newAPI(t)
	template := h.diabetes(t)
	base := "/v1/counseling/templates/" + template.ID.String()

	h.do(t, http.MethodPost, base+"/versions", map[string]any{})
	h.do(t, http.MethodPut, base+"/versions/2", map[string]any{
		"items": []any{
			item("DIET", "NUTRITION_ROOM", true),
			item("EXERCISE", "COUNSELING_ROOM", false),
		},
	})
	resp, decoded := h.do(t, http.MethodPost, base+"/versions/2/publish", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publishing: %d %v", resp.StatusCode, decoded)
	}

	live, err := h.store.Published(context.Background(), template.ID)
	if err != nil {
		t.Fatal(err)
	}
	if live.Version != 2 {
		t.Fatalf("the live version is %d", live.Version)
	}
	old, err := h.store.Version(context.Background(), template.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != "RETIRED" {
		t.Errorf("version 1 is %s after being replaced", old.Status)
	}
	// And it still reads, with its seven items, because a session references it.
	if len(old.Items) != 7 {
		t.Errorf("the retired version lost its items: %d", len(old.Items))
	}
}

// ---------------------------------------------------------------------------
// Criterion 4: both languages before publishing
// ---------------------------------------------------------------------------

func TestPublishingIsRefusedWhenAnItemIsInOneLanguage(t *testing.T) {
	// Half this clinic counsels in Bangla. An item in one language is an item half the
	// counsellors cannot read to a patient.
	h := newAPI(t)
	template := h.diabetes(t)
	base := "/v1/counseling/templates/" + template.ID.String()

	h.do(t, http.MethodPost, base+"/versions", map[string]any{})
	h.do(t, http.MethodPut, base+"/versions/2", map[string]any{
		"items": []any{
			map[string]any{"item_code": "DIET", "text_en": "Diet", "text_bn": "",
				"mandatory": true, "room": "NUTRITION_ROOM"},
		},
	})
	resp, decoded := h.do(t, http.MethodPost, base+"/versions/2/publish", nil)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("publishing a half-translated version answered %d: %v", resp.StatusCode, decoded)
	}
}

func TestAHalfWrittenDraftIsAllowed(t *testing.T) {
	// A rule that refused this would make the authoring screen fight the person using it: an
	// item with English and no Bengali is what writing looks like halfway through.
	h := newAPI(t)
	template := h.diabetes(t)
	base := "/v1/counseling/templates/" + template.ID.String()

	h.do(t, http.MethodPost, base+"/versions", map[string]any{})
	resp, decoded := h.do(t, http.MethodPut, base+"/versions/2", map[string]any{
		"items": []any{
			map[string]any{"item_code": "DIET", "text_en": "Diet", "text_bn": "",
				"mandatory": true, "room": "NUTRITION_ROOM"},
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("saving a half-written draft answered %d: %v", resp.StatusCode, decoded)
	}
}

func TestPublishingIsRefusedWhenThereAreNoItems(t *testing.T) {
	// An empty checklist on a phone is a checklist that reads as complete the moment it opens.
	h := newAPI(t)
	template := h.diabetes(t)
	base := "/v1/counseling/templates/" + template.ID.String()

	h.do(t, http.MethodPost, base+"/versions", map[string]any{})
	h.do(t, http.MethodPut, base+"/versions/2", map[string]any{"items": []any{}})
	resp, _ := h.do(t, http.MethodPost, base+"/versions/2/publish", nil)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("publishing an empty version answered %d", resp.StatusCode)
	}
}

func TestTheDatabaseRefusesAHalfTranslatedPublishToo(t *testing.T) {
	// The handler gives an author a sentence; the trigger holds for the migration and the
	// support script. Neither is redundant with the other.
	h := newAPI(t)

	var template uuid.UUID
	if err := h.SQL.QueryRow(
		`SELECT id FROM core.counseling_template WHERE code = 'DIABETES'`).Scan(&template); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO core.counseling_template_version (template_id, version, status)
		VALUES ($1, 9, 'DRAFT')`, template); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO core.counseling_item
		  (template_id, version, item_code, ordering, text_en, text_bn, is_mandatory, room)
		VALUES ($1, 9, 'ONLY_ENGLISH', 1, 'Only English', '', true, 'COUNSELING_ROOM')`,
		template); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		UPDATE core.counseling_template_version SET status = 'PUBLISHED', published_at = now(),
		       published_source = 'MIGRATION'
		 WHERE template_id = $1 AND version = 9`, template); err == nil {
		t.Error("a half-translated version was published directly")
	}
}

// ---------------------------------------------------------------------------
// Criterion 1: authored and published without a code change
// ---------------------------------------------------------------------------

func TestAPhysicianCanAuthorAndPublishAThyroidTemplate(t *testing.T) {
	// The whole criterion, and the manual verification the plan asks for, as far as a test can
	// stand in for it: create, write, publish, and see it become what a new session gets.
	h := newAPI(t)

	resp, decoded := h.do(t, http.MethodPost, "/v1/counseling/templates", map[string]any{
		"code": "THYROID", "title_en": "Thyroid counseling", "title_bn": "থাইরয়েড কাউন্সেলিং",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("creating: %d %v", resp.StatusCode, decoded)
	}
	template, _ := decoded["template"].(map[string]any)
	id, _ := template["id"].(string)
	base := "/v1/counseling/templates/" + id

	resp, decoded = h.do(t, http.MethodPut, base+"/versions/1", map[string]any{
		"items": []any{
			item("WHAT_THYROID_IS", "COUNSELING_ROOM", true),
			item("TAKING_THE_TABLET", "COUNSELING_ROOM", true),
			item("WHEN_TO_RETEST", "COUNSELING_ROOM", false),
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("saving: %d %v", resp.StatusCode, decoded)
	}

	resp, decoded = h.do(t, http.MethodPost, base+"/versions/1/publish", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publishing: %d %v", resp.StatusCode, decoded)
	}
	version, _ := decoded["version"].(map[string]any)
	if version["status"] != "PUBLISHED" || version["published_source"] != "USER" {
		t.Errorf("published as %v by %v", version["status"], version["published_source"])
	}
	if version["published_by"] != h.user.String() {
		t.Errorf("published by %v", version["published_by"])
	}

	// And the mandatory set §5.5's gate will read is what was authored.
	parsed, _ := uuid.Parse(id)
	live, err := h.store.Published(context.Background(), parsed)
	if err != nil {
		t.Fatal(err)
	}
	if len(live.Mandatory()) != 2 {
		t.Errorf("%d of three items are mandatory", len(live.Mandatory()))
	}
}

func TestPublishingIsAudited(t *testing.T) {
	// It is the act that changes what every counsellor asks every patient from that second
	// onwards. A configuration change with no name against it is one nobody can review.
	h := newAPI(t)
	template := h.diabetes(t)
	base := "/v1/counseling/templates/" + template.ID.String()

	h.do(t, http.MethodPost, base+"/versions", map[string]any{})
	h.do(t, http.MethodPut, base+"/versions/2", map[string]any{
		"items": []any{item("DIET", "NUTRITION_ROOM", true)},
	})
	h.do(t, http.MethodPost, base+"/versions/2/publish", nil)

	if len(h.audits.published) != 1 {
		t.Fatalf("%d publications reached the audit trail", len(h.audits.published))
	}
	entry := h.audits.published[0]
	if entry.Template != "DIABETES" || entry.Version != 2 || entry.Items != 1 {
		t.Errorf("the audit entry says %+v", entry)
	}
	if entry.ActorID != h.user {
		t.Errorf("audited against %v", entry.ActorID)
	}
}

func TestSavingADraftIsNotPublishing(t *testing.T) {
	// The two acts are separate endpoints on purpose. A physician who meant to fix a typo must
	// not be able to accidentally put a checklist on every phone on the floor.
	h := newAPI(t)
	template := h.diabetes(t)
	base := "/v1/counseling/templates/" + template.ID.String()

	h.do(t, http.MethodPost, base+"/versions", map[string]any{})
	h.do(t, http.MethodPut, base+"/versions/2", map[string]any{
		"items": []any{item("DIET", "NUTRITION_ROOM", true)},
	})

	live, err := h.store.Published(context.Background(), template.ID)
	if err != nil {
		t.Fatal(err)
	}
	if live.Version != 1 {
		t.Errorf("saving a draft changed the live version to %d", live.Version)
	}
	if len(h.audits.published) != 0 {
		t.Error("saving a draft produced an audit entry for a publication")
	}
}

// ---------------------------------------------------------------------------
// The assignment rules
// ---------------------------------------------------------------------------

func TestADiabetesCodingFindsTheDiabetesTemplate(t *testing.T) {
	// Keyed on a coding rather than a word: a rule saying "diabetes" would miss E11.65 and match
	// a complaint of diabetes insipidus.
	h := newAPI(t)

	for _, code := range []string{"E11.9", "E11.65", "E10.9", "O24.4"} {
		matches, err := h.store.For(context.Background(), "ICD10", "2019", code)
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 1 || matches[0].Code != "DIABETES" {
			t.Errorf("%s matched %v", code, matches)
		}
		if matches[0].Version != 1 {
			t.Errorf("%s matched version %d rather than the published one", code, matches[0].Version)
		}
	}

	// And a coding that is not diabetes matches nothing, rather than the nearest thing.
	matches, err := h.store.For(context.Background(), "ICD10", "2019", "E03.9")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("hypothyroidism matched %v", matches)
	}
}

func TestOnlyPublishedVersionsAreOffered(t *testing.T) {
	// A draft is not something to hand a counsellor: a session started against one would
	// reference a version that can still change under it.
	h := newAPI(t)

	resp, _ := h.do(t, http.MethodPost, "/v1/counseling/templates", map[string]any{
		"code": "PCOS", "title_en": "PCOS counseling", "title_bn": "পিসিওএস কাউন্সেলিং",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatal("creating the template")
	}
	var id uuid.UUID
	if err := h.SQL.QueryRow(
		`SELECT id FROM core.counseling_template WHERE code = 'PCOS'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO core.counseling_assignment (template_id, code_system, code_version, code_prefix)
		VALUES ($1, 'ICD10', '2019', 'E28')`, id); err != nil {
		t.Fatal(err)
	}

	matches, err := h.store.For(context.Background(), "ICD10", "2019", "E28.2")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("an unpublished template was offered: %v", matches)
	}
}

// ---------------------------------------------------------------------------
// Permissions and the standing rules
// ---------------------------------------------------------------------------

func TestPublishingNeedsMoreThanWriting(t *testing.T) {
	// Saving a draft is cheap and reversible; publishing is neither. A counsellor may read the
	// checklist and change nothing.
	h := newAPI(t)
	template := h.diabetes(t)
	base := "/v1/counseling/templates/" + template.ID.String()

	h.held = []string{counseling.PermRead, counseling.PermWrite}
	h.do(t, http.MethodPost, base+"/versions", map[string]any{})
	h.do(t, http.MethodPut, base+"/versions/2", map[string]any{
		"items": []any{item("DIET", "NUTRITION_ROOM", true)},
	})
	if resp, _ := h.do(t, http.MethodPost, base+"/versions/2/publish", nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("publishing without the publish permission answered %d", resp.StatusCode)
	}

	h.held = []string{counseling.PermRead}
	if resp, _ := h.do(t, http.MethodPost, "/v1/counseling/templates", map[string]any{
		"code": "X", "title_en": "x", "title_bn": "x",
	}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("creating a template with only read answered %d", resp.StatusCode)
	}
	if resp, _ := h.do(t, http.MethodGet, "/v1/counseling/templates", nil); resp.StatusCode != http.StatusOK {
		t.Errorf("reading with the read permission answered %d", resp.StatusCode)
	}
}

func TestAnItemCodeCannotAppearTwice(t *testing.T) {
	// Two items sharing a code make a tick ambiguous, and the ambiguity would surface only as a
	// gate that never closes.
	h := newAPI(t)
	template := h.diabetes(t)
	base := "/v1/counseling/templates/" + template.ID.String()

	h.do(t, http.MethodPost, base+"/versions", map[string]any{})
	resp, _ := h.do(t, http.MethodPut, base+"/versions/2", map[string]any{
		"items": []any{
			item("DIET", "NUTRITION_ROOM", true),
			item("DIET", "COUNSELING_ROOM", false),
		},
	})
	if resp.StatusCode == http.StatusOK {
		t.Error("a draft with a duplicate item code was accepted")
	}
}

func TestAnItemInAnUnknownRoomIsRefused(t *testing.T) {
	h := newAPI(t)
	template := h.diabetes(t)
	base := "/v1/counseling/templates/" + template.ID.String()

	h.do(t, http.MethodPost, base+"/versions", map[string]any{})
	resp, _ := h.do(t, http.MethodPut, base+"/versions/2", map[string]any{
		"items": []any{item("DIET", "THE_CAR_PARK", true)},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("an item in a room the clinic does not have answered %d", resp.StatusCode)
	}
}

func TestTheStandingInvariantsHold(t *testing.T) {
	h := newAPI(t)
	for _, fn := range []string{
		"core.assert_published_counseling_is_bilingual",
		"core.assert_every_published_template_has_items",
	} {
		if _, err := h.SQL.Exec(`SELECT ` + fn + `()`); err != nil {
			t.Errorf("%s: %v", fn, err)
		}
	}
}
