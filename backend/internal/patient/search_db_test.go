package patient_test

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/patient"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/textmatch"
)

// Patient search (CP31).
//
// The acceptance criteria are a latency number and a relevance claim, and both are only
// meaningful against a register of realistic size and realistic spellings — so the
// performance test builds fifty thousand rows and the relevance tests use names a
// Bangladeshi clinic actually holds.

// seedRegister writes rows straight into the read model.
//
// Deliberately not through the API: fifty thousand registrations would take an hour and
// would be testing the write path, which has its own tests. What is under test here is the
// query and its indexes, and those do not care how the rows arrived — but the *keys* do, so
// they are computed with the same function the projection uses.
func (h *api) seedRegister(t *testing.T, rows int) {
	t.Helper()
	surnames := []string{"Rahim", "Karim", "Begum", "Chowdhury", "Uddin", "Akter", "Hossain", "Khatun", "Islam", "Sarkar"}
	given := []string{"Mohammad", "Md", "Abdul", "Fatema", "Nasrin", "Salma", "Zakir", "Anwar", "Shirin", "Jamal"}
	districts := []string{"Faridpur", "Dhaka", "Rajbari", "Gopalganj", "Madaripur"}

	batch := make([]string, 0, rows)
	args := make([]any, 0, rows*10)
	for i := range rows {
		nameEN := fmt.Sprintf("%s %s", given[i%len(given)], surnames[(i/7)%len(surnames)])
		nameBN := "রোগী " + fmt.Sprint(i)
		born := time.Date(1950+i%70, time.Month(1+i%12), 1+i%28, 0, 0, 0, 0, patient.Dhaka)
		phone := fmt.Sprintf("+88017%08d", i%100000000)
		n := len(args)
		batch = append(batch, fmt.Sprintf(
			"($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			n+1, n+2, n+3, n+4, n+5, n+6, n+7, n+8, n+9, n+10, n+11, n+12, n+13, n+14, n+15, n+16, n+17))
		// Everything crosses as text and is cast in the statement: a VALUES list types its
		// parameters as text anyway, and handing pgx a time.Time for a text parameter is a
		// type it declines to encode.
		args = append(args,
			uuid.New().String(), h.facility.String(), fmt.Sprintf("%s-2026-%06d", h.code, i+1000),
			nameEN, nameBN, textmatch.Key(nameEN), "male",
			born.Format(time.DateOnly), "day", "national_id", phone,
			districts[i%len(districts)], "Boalmari",
			"consent_seed", registeredAt.Format(time.RFC3339), h.user.String(), "REGISTRATION")
		if len(batch) == 500 || i == rows-1 {
			// The VALUES list is cast column by column: PostgreSQL types an untyped
			// parameter in a VALUES row as text, and `SELECT v.*` then fails against the
			// uuid and date columns.
			statement := `INSERT INTO read.patient (
				patient_id, facility_id, clinical_id, name_en, name_bn, name_key_en, sex,
				birth_date, dob_precision, dob_source, phone_primary,
				district, upazila, consent_reference, registered_at, registered_by, registered_role,
				event_id, global_seq)
				SELECT v.patient_id::uuid, v.facility_id::uuid, v.clinical_id, v.name_en,
				       v.name_bn, v.name_key_en, v.sex, v.birth_date::date, v.dob_precision,
				       v.dob_source, v.phone_primary, v.district, v.upazila,
				       v.consent_reference, v.registered_at::timestamptz,
				       v.registered_by::uuid, v.registered_role,
				       gen_random_uuid(), 0
				  FROM (VALUES ` + joinAll(batch) + `) AS v(patient_id, facility_id, clinical_id,
				       name_en, name_bn, name_key_en, sex, birth_date, dob_precision, dob_source,
				       phone_primary, district, upazila, consent_reference, registered_at,
				       registered_by, registered_role)`
			if _, err := h.SQL.Exec(statement, args...); err != nil {
				t.Fatalf("seeding at %d: %v", i, err)
			}
			batch, args = batch[:0], args[:0]
		}
	}
	if _, err := h.SQL.Exec(`ANALYZE read.patient`); err != nil {
		t.Fatal(err)
	}
}

func joinAll(parts []string) string {
	out := ""
	for i, part := range parts {
		if i > 0 {
			out += ","
		}
		out += part
	}
	return out
}

// --- relevance ---

func TestSearchFindsAPatientByEveryPlausibleHandle(t *testing.T) {
	// Acceptance criterion 2, and the reason search exists at all: the operator has
	// whatever the person in front of them happened to bring.
	h := newAPI(t)
	created := h.registerAs(t, func(body map[string]any) {
		body["name_en"] = "Mohammad Rahim"
		body["name_bn"] = "মোহাম্মদ রহিম"
		body["phone_primary"] = "01712345678"
	})
	clinicalID := created["clinical_id"].(string)

	for name, term := range map[string]string{
		"the whole clinical id":     clinicalID,
		"the number off the card":   "000001",
		"the English name":          "Mohammad Rahim",
		"part of the English name":  "Rahim",
		"the Bangla name":           "মোহাম্মদ রহিম",
		"part of the Bangla name":   "রহিম",
		"the mobile as it is typed": "01712345678",
		"the mobile with +880":      "+8801712345678",
		// The one that matters most: a second operator romanising the same name their own
		// way. Without the phonetic key this returns nothing (CP30).
		"the name romanised differently": "Muhammad Raheem",
		"a misspelling":                  "Mohammed Rahim",
	} {
		results := h.searchFor(t, term)
		if len(results) == 0 {
			t.Errorf("searching by %s (%q) found nobody", name, term)
			continue
		}
		if results[0]["clinical_id"] != clinicalID {
			t.Errorf("searching by %s (%q) put %v first", name, term, results[0]["clinical_id"])
		}
	}
}

func (h *api) searchFor(t *testing.T, term string) []map[string]any {
	t.Helper()
	resp, body := h.call(t, http.MethodGet, "/v1/patients?q="+urlEscape(term), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search returned %d: %v", resp.StatusCode, body)
	}
	raw := body["patients"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		out = append(out, item.(map[string]any))
	}
	return out
}

func urlEscape(s string) string {
	out := ""
	for _, b := range []byte(s) {
		switch {
		case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9', b == '-', b == '_', b == '.':
			out += string(b)
		default:
			out += fmt.Sprintf("%%%02X", b)
		}
	}
	return out
}

func TestAnExactHandleOutranksAFuzzyName(t *testing.T) {
	// A clinical id is not a guess. If somebody types one, the record it names must be
	// first, whatever else happens to share a syllable with it.
	h := newAPI(t)
	first := h.registerAs(t, func(body map[string]any) { body["name_en"] = "Rahim Uddin" })
	h.registerAs(t, func(body map[string]any) {
		body["name_en"] = "Rahima Begum"
		body["phone_primary"] = "01812345678"
		body["consent_reference"] = "consent_2026_0002"
	})

	results := h.searchFor(t, first["clinical_id"].(string))
	if len(results) == 0 || results[0]["clinical_id"] != first["clinical_id"] {
		t.Fatalf("an exact clinical id did not come first: %v", results)
	}
	if rank, _ := results[0]["rank"].(float64); rank < 0.99 {
		t.Errorf("an exact match ranked %v", rank)
	}
}

func TestAMergedRecordIsOutOfTheWayButFindable(t *testing.T) {
	// A station looking for today's patient does not want yesterday's duplicate. But
	// "where did that record go" is a real question, so it is a parameter and not a
	// deletion.
	h := newAPI(t)
	survivor, duplicate := h.twoRecordsOfOnePerson(t)
	if resp, _ := h.mergeCall(t, survivor["id"].(string), duplicate["id"].(string),
		"Same person: second registration at the outreach camp on 12 August."); resp.StatusCode != http.StatusOK {
		t.Fatal("the merge failed")
	}

	for _, result := range h.searchFor(t, "Raheem") {
		if result["patient_id"] == duplicate["id"] {
			t.Errorf("a merged record appeared in an ordinary search: %v", result)
		}
	}
	resp, body := h.call(t, http.MethodGet, "/v1/patients?q=Raheem&include_merged=true", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatal(resp.StatusCode)
	}
	found := false
	for _, raw := range body["patients"].([]any) {
		if raw.(map[string]any)["patient_id"] == duplicate["id"] {
			found = true
		}
	}
	if !found {
		t.Error("include_merged did not bring the merged record back")
	}
}

func TestSearchNeverShowsAWholeTelephoneNumber(t *testing.T) {
	// A result list is the screen most often read over an operator's shoulder.
	h := newAPI(t)
	h.registerAs(t, func(map[string]any) {})
	results := h.searchFor(t, "Rahima")
	if len(results) == 0 {
		t.Fatal("no results")
	}
	masked, _ := results[0]["phone_masked"].(string)
	if masked != "•••• 5678" {
		t.Errorf("phone_masked = %q", masked)
	}
	if _, whole := results[0]["phone_primary"]; whole {
		t.Error("a search result carried the whole telephone number")
	}
}

func TestAnEmptySearchReturnsNothingRatherThanTheRegister(t *testing.T) {
	h := newAPI(t)
	h.registerAs(t, func(map[string]any) {})
	if results := h.searchFor(t, ""); len(results) != 0 {
		t.Errorf("an empty search returned %d rows", len(results))
	}
}

func TestTodaysPatientsUsesTheClinicsCalendar(t *testing.T) {
	// A patient registered at 00:30 in Dhaka belongs to that day. A list that disagrees
	// for six hours every morning is one nobody uses.
	h := newAPI(t)
	h.registerAs(t, func(map[string]any) {})

	resp, body := h.call(t, http.MethodGet, "/v1/patients/today", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %v", resp.StatusCode, body)
	}
	// The harness clock is 2026-09-03 04:42 UTC, which is 10:42 in Dhaka on the same day,
	// and the registration is at 10:00 Dhaka.
	if total, _ := body["total"].(float64); int(total) != 1 {
		t.Errorf("total = %v", body["total"])
	}
}

// --- the audit trail ---

func TestASearchIsAuditedWithoutTheTerm(t *testing.T) {
	// Acceptance criterion 4, with the part that is a decision rather than a checkbox: the
	// term is a patient's name, and a name in the audit trail is PHI in a table read by
	// administrators who may hold no clinical permission at all.
	h := newAPI(t)
	h.registerAs(t, func(map[string]any) {})
	h.searchFor(t, "Rahima Begum")

	var kind, details string
	if err := h.SQL.QueryRow(
		`SELECT kind, details::text FROM ledger.audit_event WHERE kind = 'patient.searched'`,
	).Scan(&kind, &details); err != nil {
		t.Fatalf("the search was not audited: %v", err)
	}
	if contains([]string{details}, "Rahima") || len(details) > 200 {
		t.Errorf("the audit entry carries the search term: %s", details)
	}
	for _, want := range []string{`"by"`, `"count"`, "name"} {
		if !containsSubstring(details, want) {
			t.Errorf("the audit entry does not record %s: %s", want, details)
		}
	}
}

func TestOpeningARecordIsAudited(t *testing.T) {
	h := newAPI(t)
	created := h.registerAs(t, func(map[string]any) {})
	if resp, _ := h.call(t, http.MethodGet, "/v1/patients/"+created["id"].(string)+"/summary", nil); resp.StatusCode != http.StatusOK {
		t.Fatal("the summary failed")
	}
	var target string
	if err := h.SQL.QueryRow(
		`SELECT target_code FROM ledger.audit_event WHERE kind = 'patient.viewed'`).Scan(&target); err != nil {
		t.Fatalf("opening a record was not audited: %v", err)
	}
	if target != created["clinical_id"] {
		t.Errorf("the entry names %q", target)
	}
}

func TestTheSummaryFollowsTheMergeChain(t *testing.T) {
	// An old card names a record that has since been merged away. Following the redirect
	// here means every screen lands on the live record without each of them remembering to.
	h := newAPI(t)
	survivor, duplicate := h.twoRecordsOfOnePerson(t)
	if resp, _ := h.mergeCall(t, survivor["id"].(string), duplicate["id"].(string),
		"Same person: second registration at the outreach camp on 12 August."); resp.StatusCode != http.StatusOK {
		t.Fatal("the merge failed")
	}

	resp, body := h.call(t, http.MethodGet, "/v1/patients/"+duplicate["id"].(string)+"/summary", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %v", resp.StatusCode, body)
	}
	if body["patient"].(map[string]any)["id"] != survivor["id"] {
		t.Errorf("the old id did not redirect: %v", body["patient"])
	}
}

// --- performance ---

// TestSearchIsFastEnoughOnAFullRegister is acceptance criterion 1: p95 under 300 ms at
// 50,000 patients.
//
// Skipped by default because it takes about a minute to build the register, and a suite a
// developer stops running is a suite that stops finding things. `DTHCMS_TEST_SCALE=1` runs
// it, and CI runs it nightly.
func TestSearchIsFastEnoughOnAFullRegister(t *testing.T) {
	if testing.Short() {
		t.Skip("scale test: run with -short=false")
	}
	h := newAPI(t)
	const register = 50000
	start := time.Now()
	h.seedRegister(t, register)
	t.Logf("seeded %d patients in %s", register, time.Since(start).Round(time.Millisecond))

	// The searches a station actually runs: a name, a partial name, a phone, a clinical id.
	terms := []string{
		"Mohammad Rahim", "Rahim", "Fatema Begum", "Chowdhury",
		"Muhammad Raheem", "+8801700001234", h.code + "-2026-001000", "001000",
	}
	var samples []time.Duration
	worst := map[string]time.Duration{}
	for round := 0; round < 12; round++ {
		for _, term := range terms {
			began := time.Now()
			if _, err := h.store.Search(context.Background(), h.facility,
				patient.SearchQuery{Term: term}, h.clock.Now()); err != nil {
				t.Fatal(err)
			}
			took := time.Since(began)
			samples = append(samples, took)
			if took > worst[term] {
				worst[term] = took
			}
		}
	}
	for term, took := range worst {
		t.Logf("  worst %-24q %s", term, took.Round(time.Millisecond))
	}

	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	p50 := samples[len(samples)/2]
	p95 := samples[len(samples)*95/100]
	t.Logf("register %d · %d searches · p50 %s · p95 %s",
		register, len(samples), p50.Round(time.Millisecond), p95.Round(time.Millisecond))

	if p95 > 300*time.Millisecond {
		t.Errorf("p95 is %s; slow search is the fastest way to lose staff goodwill", p95)
	}
}

func containsSubstring(haystack, needle string) bool {
	return len(needle) == 0 || len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
