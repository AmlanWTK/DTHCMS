package patient_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// Demographic corrections (CP35, §4.3).
//
// The correction principle applies to demographics as much as to clinical values, and the
// date of birth is why: a wrong one changes every pediatric percentile ever computed for
// that patient, and those numbers have already been read and acted on.

const why = "The NID card says 1985; the registration desk typed 1958."

func (h *api) correct(t *testing.T, patientID string, body map[string]any) (*http.Response, map[string]any) {
	t.Helper()
	if _, ok := body["event_id"]; !ok {
		body["event_id"] = uuid.Must(uuid.NewV7()).String()
	}
	if _, ok := body["reason"]; !ok {
		body["reason"] = why
	}
	return h.call(t, http.MethodPatch, "/v1/patients/"+patientID, body)
}

func TestACorrectionRecordsWhatItWasWhoChangedItAndWhy(t *testing.T) {
	// Acceptance criteria 1 and 2.
	h := newAPI(t)
	created := h.registerAs(t, func(map[string]any) {})
	id := created["id"].(string)

	resp, body := h.correct(t, id, map[string]any{"birth_date": "1985-06-14"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %v", resp.StatusCode, body)
	}
	if body["high_impact"] != true {
		t.Error("a date-of-birth correction was not marked high impact")
	}

	changes := body["changes"].([]any)
	if len(changes) != 1 {
		t.Fatalf("changes = %v", changes)
	}
	change := changes[0].(map[string]any)
	if change["field"] != "birth_date" || change["previous"] != "1979-04-12" || change["current"] != "1985-06-14" {
		t.Errorf("change = %v", change)
	}

	// The history, as a screen shows it.
	historyResp, history := h.call(t, http.MethodGet, "/v1/patients/"+id+"/history", nil)
	if historyResp.StatusCode != http.StatusOK {
		t.Fatalf("history returned %d", historyResp.StatusCode)
	}
	rows := history["corrections"].([]any)
	if len(rows) != 1 {
		t.Fatalf("corrections = %v", rows)
	}
	row := rows[0].(map[string]any)
	if row["reason"] != why || row["previous"] != "1979-04-12" || row["corrected_by_code"] != "R001" {
		t.Errorf("history row = %v", row)
	}

	// And the original is in the ledger, forever, whatever the read model later says.
	var payload string
	if err := h.SQL.QueryRow(
		`SELECT payload::text FROM ledger.event WHERE event_type = 'PATIENT_DEMOGRAPHICS_CORRECTED'`,
	).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if !containsSubstring(payload, "1979-04-12") {
		t.Errorf("the ledger does not hold the original value: %s", payload)
	}
}

func TestACorrectionRewritesTheRecordAndEverythingDerivedFromIt(t *testing.T) {
	// Acceptance criterion 3, in the form it can take today: the read model, the search key
	// and the anonymised cohort row all follow. What is *not* yet here — pediatric
	// percentiles — is enumerated in ops.derived_dependency rather than remembered.
	h := newAPI(t)
	created := h.registerAs(t, func(body map[string]any) { body["name_en"] = "Rahima Begum" })
	id := created["id"].(string)

	if resp, body := h.correct(t, id, map[string]any{
		"birth_date": "1985-06-14", "name_en": "Rahima Begom",
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %v", resp.StatusCode, body)
	}

	// The write side.
	var born, name string
	if err := h.SQL.QueryRow(
		`SELECT birth_date::text, name_en FROM core.patient WHERE id = $1`, id).Scan(&born, &name); err != nil {
		t.Fatal(err)
	}
	if born != "1985-06-14" || name != "Rahima Begom" {
		t.Errorf("core.patient = %s / %s", born, name)
	}

	// The read model, and its search key — which is computed from the name, so a corrected
	// name that left a stale key would make the patient unfindable by the new spelling.
	var readBorn, key string
	if err := h.SQL.QueryRow(
		`SELECT birth_date::text, name_key_en FROM read.patient WHERE patient_id = $1`, id).Scan(&readBorn, &key); err != nil {
		t.Fatal(err)
	}
	if readBorn != "1985-06-14" {
		t.Errorf("read.patient birth_date = %s", readBorn)
	}
	if key == "" {
		t.Error("the search key was not recomputed")
	}

	// The anonymised cohort row, or a research query runs against a birth year the
	// clinical record no longer holds.
	var birthYear int
	if err := h.SQL.QueryRow(`
		SELECT rs.birth_year FROM research.research_subject rs
		  JOIN identity_link.research_subject link ON link.research_id = rs.research_id
		 WHERE link.patient_id = $1`, id).Scan(&birthYear); err != nil {
		t.Fatal(err)
	}
	if birthYear != 1985 {
		t.Errorf("the cohort row still says %d", birthYear)
	}

	// And the correction reported what it invalidated, read from the register rather than
	// from a list somebody has to remember to update.
	_, body := h.correct(t, id, map[string]any{"birth_date": "1985-06-15"})
	invalidated := body["invalidated"].([]any)
	if len(invalidated) == 0 {
		t.Fatal("a date-of-birth correction reported nothing invalidated")
	}
	named := map[string]bool{}
	for _, raw := range invalidated {
		named[raw.(map[string]any)["derived_name"].(string)] = true
	}
	for _, want := range []string{"read.patient", "research.research_subject"} {
		if !named[want] {
			t.Errorf("%s was not named as depending on the date of birth", want)
		}
	}
}

func TestOnlyTheFieldsSentAreChanged(t *testing.T) {
	// A form that renders six fields must not rewrite five of them, or "what did the
	// operator actually alter" becomes unanswerable — and that is the question the history
	// exists to answer.
	h := newAPI(t)
	created := h.registerAs(t, func(map[string]any) {})
	id := created["id"].(string)

	if resp, _ := h.correct(t, id, map[string]any{"district": "Rajbari"}); resp.StatusCode != http.StatusOK {
		t.Fatal("the correction failed")
	}
	_, history := h.call(t, http.MethodGet, "/v1/patients/"+id+"/history", nil)
	rows := history["corrections"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["field"] != "district" {
		t.Errorf("corrections = %v", rows)
	}
	// The name is untouched.
	var name string
	if err := h.SQL.QueryRow(`SELECT name_en FROM core.patient WHERE id = $1`, id).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Rahima Begum" {
		t.Errorf("name_en = %q", name)
	}
}

func TestACorrectionThatChangesNothingIsRefused(t *testing.T) {
	// It would sit in the history looking like something happened.
	h := newAPI(t)
	created := h.registerAs(t, func(map[string]any) {})
	id := created["id"].(string)

	// The same values, and the same telephone number typed differently — which normalises
	// to what is already stored and is therefore not a change.
	for _, body := range []map[string]any{
		{"name_en": "Rahima Begum"},
		{"phone_primary": "+8801712345678"},
		{"phone_primary": "01712345678"},
	} {
		resp, decoded := h.correct(t, id, body)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%v returned %d: %v", body, resp.StatusCode, decoded)
		}
	}
	var rows int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM read.patient_correction`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("%d no-op corrections were recorded", rows)
	}
}

func TestACorrectionWithoutAReasonIsRefused(t *testing.T) {
	h := newAPI(t)
	created := h.registerAs(t, func(map[string]any) {})
	resp, _ := h.correct(t, created["id"].(string), map[string]any{
		"district": "Rajbari", "reason": "fix",
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestAHighImpactCorrectionNeedsAStepUp(t *testing.T) {
	// The risk is a session left open on a desk, not a person who should never have been
	// able to do this at all — so it is a step-up rather than a separate permission.
	h := newAPI(t)
	created := h.registerAs(t, func(map[string]any) {})
	id := created["id"].(string)

	// Without the header.
	resp, body := h.callWithout(t, http.MethodPatch, "/v1/patients/"+id, map[string]any{
		"event_id": uuid.Must(uuid.NewV7()).String(), "reason": why, "birth_date": "1985-06-14",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a date of birth was corrected without a step-up: %d %v", resp.StatusCode, body)
	}
	if body["error"].(map[string]any)["code"] != "STEP_UP_REQUIRED" {
		t.Errorf("error = %v", body["error"])
	}

	// An ordinary field does not need one.
	if resp, body := h.callWithout(t, http.MethodPatch, "/v1/patients/"+id, map[string]any{
		"event_id": uuid.Must(uuid.NewV7()).String(), "reason": why, "district": "Rajbari",
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("an ordinary correction demanded a step-up: %d %v", resp.StatusCode, body)
	}
}

func TestARetriedCorrectionChangesNothing(t *testing.T) {
	h := newAPI(t)
	created := h.registerAs(t, func(map[string]any) {})
	id := created["id"].(string)
	body := map[string]any{
		"event_id": uuid.Must(uuid.NewV7()).String(), "reason": why, "district": "Rajbari",
	}
	if resp, _ := h.correct(t, id, body); resp.StatusCode != http.StatusOK {
		t.Fatal("the first correction failed")
	}
	if resp, _ := h.correct(t, id, body); resp.StatusCode != http.StatusOK {
		t.Fatal("the retry failed")
	}
	var events, rows int
	if err := h.SQL.QueryRow(
		`SELECT count(*) FROM ledger.event WHERE event_type = 'PATIENT_DEMOGRAPHICS_CORRECTED'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := h.SQL.QueryRow(`SELECT count(*) FROM read.patient_correction`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if events != 1 || rows != 1 {
		t.Errorf("a retried correction produced %d events and %d history rows", events, rows)
	}
}

func TestEveryCorrectionIsExplained(t *testing.T) {
	h := newAPI(t)
	created := h.registerAs(t, func(map[string]any) {})
	if resp, _ := h.correct(t, created["id"].(string), map[string]any{"district": "Rajbari"}); resp.StatusCode != http.StatusOK {
		t.Fatal("the correction failed")
	}
	if _, err := h.SQL.Exec(`SELECT core.assert_corrections_are_explained()`); err != nil {
		t.Errorf("assert_corrections_are_explained: %v", err)
	}
}
