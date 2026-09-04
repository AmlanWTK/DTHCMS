package terminology_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
	"github.com/AmlanWTK/DTHCMS/backend/internal/terminology"
)

// The coded catalogue (CP52, §8, §9.1, D-24).
//
// Four acceptance criteria, and three of them are properties of this code:
//
//	1. the 20 most common DTHC diagnoses are each findable within three keystrokes;
//	2. every coding stores its system and its version;
//	4. search p95 is under 150 ms.
//
// Criterion 3 — Bengali synonyms resolve — is proven here too, and it is the one that would
// have been quietly skipped: a search that works in English and returns nothing for থাইরয়েড
// is a picker every Bengali-speaking clinician stops using inside a week.
//
// The licensing rule is tested as hard as the search. D-24 is unanswered, SNOMED is marked
// unusable, and the guarantee is not that we remembered not to load it — it is that a search
// against it is refused and a standing invariant refuses the content.

type api struct {
	*testsupport.DB
	store  *terminology.Store
	server *httptest.Server
	held   []string
}

type staff struct {
	facility, user uuid.UUID
	permissions    *[]string
}

func (s staff) Identify(context.Context, string) (httpx.Caller, error) {
	return httpx.Caller{
		UserID: s.user.String(), FacilityID: s.facility.String(),
		SessionID: uuid.NewSHA1(s.user, []byte("session")).String(),
		Code:      "P004", Permissions: *s.permissions, Roles: []string{"PHYSICIAN"},
	}, nil
}

func (s staff) Authorize(ctx context.Context, caller httpx.Caller, anyOf []string) (context.Context, httpx.AuthzDecision) {
	for _, want := range anyOf {
		for _, held := range caller.Permissions {
			if want == held {
				return httpx.WithPrincipal(ctx, httpx.Principal{
					UserID: caller.UserID, FacilityID: caller.FacilityID,
					SessionID: caller.SessionID, Code: caller.Code, Role: "PHYSICIAN",
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

	h := &api{DB: base, held: []string{terminology.PermRead}}
	h.store = terminology.NewStore(pool)

	var facility uuid.UUID
	if err := base.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&facility); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handlers := terminology.NewHandlers(terminology.HandlersConfig{Store: h.store, Logger: logger})
	who := staff{facility: facility, user: uuid.New(), permissions: &h.held}
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

// get issues an authenticated read and decodes it.
func (h *api) get(t *testing.T, path string, query url.Values) (*http.Response, map[string]any) {
	t.Helper()
	target := h.server.URL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp, body
}

func (h *api) search(t *testing.T, q string) []map[string]any {
	t.Helper()
	resp, body := h.get(t, "/v1/terminology/search", url.Values{"system": {"ICD10"}, "q": {q}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("searching %q: %d %v", q, resp.StatusCode, body)
	}
	raw, _ := body["concepts"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		row, _ := item.(map[string]any)
		out = append(out, row)
	}
	return out
}

func codesOf(rows []map[string]any) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		code, _ := row["code"].(string)
		out = append(out, code)
	}
	return out
}

func positionOf(rows []map[string]any, code string) int {
	for i, row := range rows {
		if row["code"] == code {
			return i
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// Criterion 1: the twenty most common diagnoses, within three keystrokes
// ---------------------------------------------------------------------------

func TestEveryFavouriteIsFoundWithinThreeKeystrokes(t *testing.T) {
	// The criterion, read literally and tested literally: for each of the clinic's ranked
	// diagnoses there must be some three characters a clinician would plausibly type that
	// bring it back. "Plausibly" is not left to judgement — the three characters are taken
	// from the concept's own display or one of its own synonyms, which is exactly what
	// somebody reaching for it would start typing.
	h := newAPI(t)

	favourites, err := h.store.Favourites(context.Background(), "ICD10", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(favourites) < 20 {
		t.Fatalf("criterion 1 names twenty diagnoses; the clinic has ranked %d", len(favourites))
	}

	for _, concept := range favourites {
		starts, err := h.prefixes(t, concept.System, concept.Version, concept.Code)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		var tried []string
		for _, start := range starts {
			if len([]rune(start)) < 3 {
				continue
			}
			needle := string([]rune(start)[:3])
			tried = append(tried, needle)
			rows := h.search(t, needle)
			if positionOf(rows, concept.Code) >= 0 {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s (%s) is a ranked favourite but none of %v finds it in three "+
				"keystrokes. Criterion 1 is met by knowing which twenty and giving them the "+
				"words people actually type, not by a cleverer search — add a synonym.",
				concept.Code, concept.DisplayEN, tried)
		}
	}
}

// prefixes is every word a clinician might start typing for this concept: the words of its
// two displays and of every synonym it carries.
func (h *api) prefixes(t *testing.T, system, version, code string) ([]string, error) {
	t.Helper()
	rows, err := h.SQL.Query(`
		SELECT display_en FROM core.terminology_concept
		 WHERE system = $1 AND version = $2 AND code = $3
		UNION ALL
		SELECT display_bn FROM core.terminology_concept
		 WHERE system = $1 AND version = $2 AND code = $3
		UNION ALL
		SELECT term FROM core.terminology_synonym
		 WHERE system = $1 AND version = $2 AND code = $3`, system, version, code)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return nil, err
		}
		// Every word start, because that is what the ranking matches on.
		word := []rune{}
		for _, r := range text {
			if r == ' ' || r == ',' || r == '(' || r == ')' || r == '-' {
				if len(word) >= 3 {
					out = append(out, string(word))
				}
				word = nil
				continue
			}
			word = append(word, r)
		}
		if len(word) >= 3 {
			out = append(out, string(word))
		}
		if len([]rune(text)) >= 3 {
			out = append(out, text)
		}
	}
	return out, rows.Err()
}

func TestTheClinicsOwnDiagnosisComesFirst(t *testing.T) {
	// A favourite outranks the rest of the classification, and this is the case that proves
	// the tiers are doing work: "dia" matches dozens of ICD-10 titles, and a clinic that
	// diagnoses type 2 diabetes forty times a day should not have to read past "Diabetes
	// insipidus" to reach it.
	h := newAPI(t)

	rows := h.search(t, "dia")
	if len(rows) == 0 {
		t.Fatal(`"dia" found nothing`)
	}
	if rows[0]["code"] != "E11.9" {
		t.Errorf(`"dia" put %v first; the clinic's most common diagnosis E11.9 should lead. Got %v`,
			rows[0]["code"], codesOf(rows))
	}
	if tier, _ := rows[0]["tier"].(float64); tier != 2 {
		t.Errorf("E11.9 came back at tier %v; a favourite matched on a word start is tier 2", tier)
	}
}

func TestTypingACodeFindsThatCodeFirst(t *testing.T) {
	// Tier 1. Doctors who know the code type the code, and a search that answered them with
	// a fuzzy title match would be a search they route around by writing free text.
	h := newAPI(t)

	rows := h.search(t, "E11")
	if len(rows) == 0 {
		t.Fatal(`"E11" found nothing`)
	}
	for _, row := range rows {
		if tier, _ := row["tier"].(float64); tier == 1 {
			continue
		}
		break
	}
	if tier, _ := rows[0]["tier"].(float64); tier != 1 {
		t.Errorf("the first result for a typed code is tier %v, not 1: %v", tier, codesOf(rows))
	}
	code, _ := rows[0]["code"].(string)
	if len(code) < 3 || code[:3] != "E11" {
		t.Errorf(`"E11" returned %q first`, code)
	}
}

func TestAMisspellingStillFindsIt(t *testing.T) {
	// Tier 4. The clinic types on phone keyboards in a second language; a picker that only
	// answers correct spelling is a picker people give up on and write free text instead —
	// which is the whole thing this checkpoint exists to prevent.
	h := newAPI(t)

	rows := h.search(t, "diabetis")
	if positionOf(rows, "E11.9") < 0 {
		t.Errorf(`"diabetis" did not find E11.9: %v`, codesOf(rows))
	}
}

func TestASingleLetterDoesNotReturnTheCatalogue(t *testing.T) {
	// The floor under the trigram tier. Without it every keystroke returns everything, the
	// list flickers, and the ranking is noise.
	h := newAPI(t)

	rows := h.search(t, "e")
	if len(rows) > terminology.MaxResults {
		t.Fatalf("a single letter returned %d results; the cap is %d", len(rows), terminology.MaxResults)
	}
	for _, row := range rows {
		if tier, _ := row["tier"].(float64); tier == 4 {
			if score, _ := row["score"].(float64); score < 0.25 {
				t.Errorf("%v came back at tier 4 with score %v, under the 0.25 floor",
					row["code"], score)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Criterion 3: Bengali resolves
// ---------------------------------------------------------------------------

func TestBengaliFindsTheSameConcepts(t *testing.T) {
	// A search that works in English and returns nothing in Bengali is a search half this
	// clinic cannot use. Each of these is a word a Bengali-speaking clinician would type.
	h := newAPI(t)

	cases := []struct {
		typed string
		want  string
	}{
		{"ডায়াবে", "E11.9"},
		{"থাইর", "E03.9"},
		{"উচ্চ রক্তচাপ", "I10"},
	}
	for _, tc := range cases {
		rows := h.search(t, tc.typed)
		if positionOf(rows, tc.want) < 0 {
			t.Errorf("typing %q did not find %s: %v", tc.typed, tc.want, codesOf(rows))
		}
	}
}

func TestEveryFavouriteCarriesBothLanguages(t *testing.T) {
	// The application's copy of the standing invariant. A favourite is a button on a screen
	// that renders in two languages, and one with an empty Bengali display is a button that
	// is blank for half the staff.
	h := newAPI(t)

	favourites, err := h.store.Favourites(context.Background(), "ICD10", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, concept := range favourites {
		if concept.DisplayBN == "" {
			t.Errorf("%s (%s) is ranked %v but has no Bengali display",
				concept.Code, concept.DisplayEN, *concept.FavouriteRank)
		}
	}
}

// ---------------------------------------------------------------------------
// Criterion 2: every coding stores its system and its version
// ---------------------------------------------------------------------------

func TestEveryResultCarriesItsSystemAndVersion(t *testing.T) {
	// The half of criterion 2 this service owns. The recording code cannot stamp a version
	// it was never given, and a client that never asked for one must still receive it —
	// otherwise the first picker somebody writes stores a bare string.
	h := newAPI(t)

	resp, body := h.get(t, "/v1/terminology/search",
		url.Values{"system": {"ICD10"}, "q": {"dia"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, body)
	}
	version, _ := body["version"].(string)
	if version == "" {
		t.Fatal("the response named no version; a client that asked for none has nothing to stamp")
	}
	for _, row := range h.search(t, "dia") {
		if row["system"] != "ICD10" || row["version"] != version {
			t.Errorf("%v came back as %v/%v, not ICD10/%s",
				row["code"], row["system"], row["version"], version)
		}
	}
}

func TestAVersionThatIsNotLoadedIsRefusedRatherThanReplaced(t *testing.T) {
	// The failure mode criterion 2 exists to prevent, and it is silent: a caller asks for
	// ICD-10 2019, is quietly given 2016, and records a coding whose version is a lie that
	// nobody discovers until somebody tries to read it back in 2032.
	h := newAPI(t)

	resp, body := h.get(t, "/v1/terminology/search",
		url.Values{"system": {"ICD10"}, "version": {"1066"}, "q": {"dia"}})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a version nobody loaded answered %d, not 422: %v", resp.StatusCode, body)
	}
}

func TestAConceptCanBeReadBackByItsCoding(t *testing.T) {
	// What a coding is *for*: rendering a diagnosis recorded years ago, under a version
	// nobody types into a picker any more.
	h := newAPI(t)

	resp, body := h.get(t, "/v1/terminology/concept",
		url.Values{"system": {"ICD10"}, "code": {"E11.9"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, body)
	}
	concept, _ := body["concept"].(map[string]any)
	if concept["display_en"] == "" || concept["display_bn"] == "" {
		t.Errorf("E11.9 came back without both displays: %v", concept)
	}
	if _, ok := body["mappings"].([]any); !ok {
		t.Errorf("mappings must be present and empty rather than absent: %v", body["mappings"])
	}
}

func TestAnUnknownCodeIsNotFound(t *testing.T) {
	h := newAPI(t)

	resp, _ := h.get(t, "/v1/terminology/concept",
		url.Values{"system": {"ICD10"}, "code": {"Z99.999"}})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an unknown code answered %d, not 404", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// D-24: SNOMED is described, and refused
// ---------------------------------------------------------------------------

func TestSnomedIsDescribedButRefused(t *testing.T) {
	// The licensing control. Not "we remembered not to load it" — a search is refused, and
	// the refusal is 422 with a reason rather than an empty list, because "no results" sends
	// a clinician looking for a better spelling and this is not that.
	h := newAPI(t)

	resp, body := h.get(t, "/v1/terminology/systems", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, body)
	}
	systems, _ := body["systems"].([]any)
	var snomed map[string]any
	for _, item := range systems {
		row, _ := item.(map[string]any)
		if row["code"] == "SNOMED" {
			snomed = row
		}
	}
	if snomed == nil {
		t.Fatal("SNOMED is not registered; the mapping table cannot name it as a target")
	}
	if snomed["usable"] != false {
		t.Error("SNOMED is marked usable while D-24 is unanswered")
	}
	if snomed["licence_note"] == "" {
		t.Error("SNOMED is refused with no note; the next person to try needs the reason")
	}

	resp, body = h.get(t, "/v1/terminology/search",
		url.Values{"system": {"SNOMED"}, "q": {"diabetes"}})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("searching SNOMED answered %d, not 422: %v", resp.StatusCode, body)
	}
}

func TestTheStandingInvariantsHold(t *testing.T) {
	// The three CP52 invariants, run against the migrated database rather than trusted. They
	// also run after every migration; this is the copy that fails in a unit test run, where
	// somebody adding a code system or a favourite will see it.
	//
	// The first is the licensing control. It is the difference between "we remembered not to
	// load SNOMED" and "SNOMED content cannot be here" — and only the second survives the
	// person who arrives after D-24 is answered, reads that the mapping table names SNOMED,
	// and assumes that means the content is welcome.
	h := newAPI(t)
	for _, fn := range []string{
		"core.assert_no_unlicensed_terminology_is_embedded",
		"core.assert_favourites_are_bilingual",
		"core.assert_every_terminology_has_a_default_version",
	} {
		if _, err := h.SQL.Exec(`SELECT ` + fn + `()`); err != nil {
			t.Errorf("%s: %v", fn, err)
		}
	}
}

func TestUnlicensedContentCannotBeLoaded(t *testing.T) {
	// And the invariant is not decoration: load one SNOMED concept and it fails. Written as
	// a transaction that rolls back, so the assertion is about the rule rather than about
	// what this test left behind.
	h := newAPI(t)

	tx, err := h.SQL.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
		INSERT INTO core.code_system_version (system, version, is_default)
		VALUES ('SNOMED', 'INT-2026', false)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`
		INSERT INTO core.terminology_concept (system, version, code, display_en, display_bn)
		VALUES ('SNOMED', 'INT-2026', '44054006', 'Diabetes mellitus type 2', 'ডায়াবেটিস')`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`SELECT core.assert_no_unlicensed_terminology_is_embedded()`); err == nil {
		t.Error("SNOMED content was loaded and the invariant passed; the licensing control " +
			"is a comment rather than a rule")
	}
}

// ---------------------------------------------------------------------------
// Criterion 4: p95 under 150 ms
// ---------------------------------------------------------------------------

func TestSearchIsFastEnoughToTypeInto(t *testing.T) {
	// Criterion 4. The number is not a performance target for its own sake: an autocomplete
	// that answers slower than a person types is one they finish typing over, and the list
	// that lands is the answer to a query they have already moved past.
	//
	// Measured against the real database through the real handler, over the queries a
	// clinician actually produces — including the growing prefixes of one word, which is
	// what typing *is*.
	h := newAPI(t)

	queries := []string{
		"d", "di", "dia", "diab", "diabe", "diabet",
		"t", "th", "thy", "thyr", "thyro",
		"h", "hy", "hyp", "hyper", "hypert",
		"ডা", "ডায়া", "থাই", "থাইর",
		"E11", "E03", "I10", "diabetis", "tyroid", "obes", "pcos", "ckd",
	}

	// Warmed first: the first query of a session pays for the plan cache and the index
	// pages, and a p95 computed over a cold start measures the machine rather than the
	// query. The clinic's first search of the morning is one search.
	for _, q := range queries[:4] {
		h.search(t, q)
	}

	var samples []time.Duration
	for round := 0; round < 4; round++ {
		for _, q := range queries {
			start := time.Now()
			h.search(t, q)
			samples = append(samples, time.Since(start))
		}
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	p95 := samples[int(float64(len(samples))*0.95)]

	if p95 > 150*time.Millisecond {
		t.Errorf("search p95 is %v over %d samples; criterion 4 asks for under 150ms",
			p95.Round(time.Millisecond), len(samples))
	}
	t.Logf("search p95 %v, median %v, worst %v over %d samples",
		p95.Round(time.Millisecond),
		samples[len(samples)/2].Round(time.Millisecond),
		samples[len(samples)-1].Round(time.Millisecond), len(samples))
}

// ---------------------------------------------------------------------------
// The rest of the surface
// ---------------------------------------------------------------------------

func TestAnEmptyQueryIsTheFavourites(t *testing.T) {
	// What a picker shows before anybody has typed, which is most of the times it is opened.
	// Not everything, and not an error.
	h := newAPI(t)

	resp, body := h.get(t, "/v1/terminology/search", url.Values{"system": {"ICD10"}, "q": {""}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, body)
	}
	raw, _ := body["concepts"].([]any)
	if len(raw) == 0 {
		t.Fatal("an empty query returned nothing; a picker opens on the clinic's own list")
	}
	first, _ := raw[0].(map[string]any)
	if rank, ok := first["favourite_rank"].(float64); !ok || rank != 1 {
		t.Errorf("the empty query did not lead with rank 1: %v", first)
	}
}

func TestTheFavouritesComeBackInRankOrder(t *testing.T) {
	h := newAPI(t)

	resp, body := h.get(t, "/v1/terminology/favourites", url.Values{"system": {"ICD10"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, body)
	}
	raw, _ := body["concepts"].([]any)
	previous := 0.0
	for _, item := range raw {
		row, _ := item.(map[string]any)
		rank, ok := row["favourite_rank"].(float64)
		if !ok {
			t.Fatalf("a favourite came back with no rank: %v", row)
		}
		if rank <= previous {
			t.Fatalf("ranks are out of order at %v: %v after %v", row["code"], rank, previous)
		}
		previous = rank
	}
}

func TestTheClinicsOwnDictionaryIsSearchableToo(t *testing.T) {
	// Complaints ICD has no code for. Same tables, same ranking, same shape — so the picker
	// at station 3 is the picker at station 8 with a different `system`.
	h := newAPI(t)

	resp, body := h.get(t, "/v1/terminology/search", url.Values{"system": {"DTHC"}, "q": {"tir"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, body)
	}
	raw, _ := body["concepts"].([]any)
	if len(raw) == 0 {
		t.Fatal(`"tir" found no complaint in the clinic's own dictionary`)
	}
	for _, item := range raw {
		row, _ := item.(map[string]any)
		if row["system"] != "DTHC" {
			t.Errorf("a DTHC search returned a %v concept: %v", row["system"], row["code"])
		}
	}
}

func TestSearchIsCappedAtTwentyFive(t *testing.T) {
	h := newAPI(t)

	resp, body := h.get(t, "/v1/terminology/search",
		url.Values{"system": {"ICD10"}, "q": {"a"}, "limit": {"500"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, body)
	}
	raw, _ := body["concepts"].([]any)
	if len(raw) > terminology.MaxResults {
		t.Errorf("asking for 500 returned %d; the cap is %d", len(raw), terminology.MaxResults)
	}
}

func TestAnUnknownSystemIsAValidationFailure(t *testing.T) {
	// Not a 404: the caller asked a question about a parameter they supplied, and the field
	// that is wrong is the one the message should name.
	h := newAPI(t)

	resp, body := h.get(t, "/v1/terminology/search",
		url.Values{"system": {"NOTATHING"}, "q": {"dia"}})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("an unknown system answered %d, not 422: %v", resp.StatusCode, body)
	}
}

func TestASystemWithNoContentAsksForAVersion(t *testing.T) {
	// ICD-11 today: registered so the schema can carry it, with nothing loaded. The answer
	// is "name one", not an empty list that reads as "there is no such diagnosis".
	h := newAPI(t)

	resp, body := h.get(t, "/v1/terminology/search",
		url.Values{"system": {"ICD11"}, "q": {"dia"}})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a system with no default version answered %d, not 422: %v", resp.StatusCode, body)
	}
}

func TestWithoutThePermissionNothingIsReadable(t *testing.T) {
	h := newAPI(t)
	h.held = []string{"observation.read.values"}

	for _, path := range []string{
		"/v1/terminology/systems", "/v1/terminology/search",
		"/v1/terminology/favourites", "/v1/terminology/concept",
	} {
		resp, _ := h.get(t, path, url.Values{"system": {"ICD10"}, "code": {"E11.9"}})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s answered %d without terminology.read, not 403", path, resp.StatusCode)
		}
	}
}
