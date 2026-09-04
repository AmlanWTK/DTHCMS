package patient_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// Merging (CP30). A merge is a redirect, never a delete, and never automatic.

func (h *api) mergeCall(t *testing.T, survivor, merged string, justification string) (*http.Response, map[string]any) {
	t.Helper()
	return h.call(t, http.MethodPost, "/v1/patients/"+survivor+"/merge", map[string]any{
		"event_id":      uuid.Must(uuid.NewV7()).String(),
		"merged_id":     merged,
		"score":         0.86,
		"decision":      "reviewed_match",
		"justification": justification,
	})
}

// twoRecordsOfOnePerson registers the same person twice, the way a clinic actually acquires
// a duplicate: a second registration at an outreach camp, with the identity number missing
// because the card was at home.
func (h *api) twoRecordsOfOnePerson(t *testing.T) (survivor, duplicate map[string]any) {
	t.Helper()
	survivor = h.registerAs(t, func(body map[string]any) {
		body["name_en"] = "Mohammad Rahim"
		body["birth_date"] = "1985-06-14"
		body["phone_primary"] = "01711111101"
	})
	duplicate = h.registerAs(t, func(body map[string]any) {
		body["name_en"] = "Muhammad Raheem"
		body["birth_date"] = "1985-01-01"
		body["dob_precision"] = "year"
		body["dob_source"] = "patient_stated"
		body["phone_primary"] = "01722222202"
		body["consent_reference"] = "consent_2026_0002"
	})
	return survivor, duplicate
}

func TestAMergeIsARedirectAndNotADelete(t *testing.T) {
	// Acceptance criterion 3. Every event from both records must still resolve, with its
	// original attribution: deleting anything would take a decade of somebody's clinical
	// history with it.
	h := newAPI(t)
	survivor, duplicate := h.twoRecordsOfOnePerson(t)

	resp, body := h.mergeCall(t, survivor["id"].(string), duplicate["id"].(string),
		"Same person: second registration at the outreach camp on 12 August, NID card was at home.")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %v", resp.StatusCode, body)
	}

	// The losing record still exists, still holds its own registration event, and points
	// at the survivor.
	var status string
	var redirect uuid.UUID
	if err := h.SQL.QueryRow(
		`SELECT status, merged_into_id FROM core.patient WHERE id = $1`,
		duplicate["id"]).Scan(&status, &redirect); err != nil {
		t.Fatalf("the merged record is gone: %v", err)
	}
	if status != "merged" || redirect.String() != survivor["id"] {
		t.Errorf("status = %q, redirect = %s", status, redirect)
	}

	// Both registration events are still there, each naming the record it was written
	// against.
	var events int
	if err := h.SQL.QueryRow(
		`SELECT count(*) FROM ledger.event WHERE event_type = 'PATIENT_REGISTERED'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 2 {
		t.Errorf("the ledger holds %d registrations after a merge; it must hold both", events)
	}

	// And following the chain from either id reaches the survivor. This is how every read
	// that starts from an old card or an old report finds the live record.
	for _, from := range []any{survivor["id"], duplicate["id"]} {
		var live uuid.UUID
		if err := h.SQL.QueryRow(`SELECT read.surviving_patient($1)`, from).Scan(&live); err != nil {
			t.Fatal(err)
		}
		if live.String() != survivor["id"] {
			t.Errorf("following from %v reached %s, want %s", from, live, survivor["id"])
		}
	}

	// The invariant that says the same thing, and runs at every start.
	if _, err := h.SQL.Exec(`SELECT core.assert_merges_are_redirects()`); err != nil {
		t.Errorf("assert_merges_are_redirects: %v", err)
	}
}

func TestAMergeRecordsWhoWhyAndOnWhatScore(t *testing.T) {
	h := newAPI(t)
	survivor, duplicate := h.twoRecordsOfOnePerson(t)
	const why = "Same person: second registration at the outreach camp on 12 August."

	if resp, body := h.mergeCall(t, survivor["id"].(string), duplicate["id"].(string), why); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %v", resp.StatusCode, body)
	}

	resp, body := h.call(t, http.MethodGet, "/v1/patients/"+survivor["id"].(string)+"/merges", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %v", resp.StatusCode, body)
	}
	merges := body["merges"].([]any)
	if len(merges) != 1 {
		t.Fatalf("merges = %v", merges)
	}
	record := merges[0].(map[string]any)
	if record["justification"] != why {
		t.Errorf("justification = %v", record["justification"])
	}
	if record["decision"] != "reviewed_match" {
		t.Errorf("decision = %v", record["decision"])
	}
	if score, _ := record["score"].(float64); score < 0.85 || score > 0.87 {
		t.Errorf("score = %v", record["score"])
	}
	if record["merged_by"] != h.user.String() {
		t.Errorf("merged_by = %v, want %s", record["merged_by"], h.user)
	}
	// And the event is in the ledger, on the losing aggregate — the record whose meaning
	// changed.
	var aggregate uuid.UUID
	if err := h.SQL.QueryRow(
		`SELECT aggregate_id FROM ledger.event WHERE event_type = 'PATIENT_MERGED'`).Scan(&aggregate); err != nil {
		t.Fatal(err)
	}
	if aggregate.String() != duplicate["id"] {
		t.Errorf("PATIENT_MERGED was written against %s, want the losing record %s", aggregate, duplicate["id"])
	}
}

func TestAMergeWithoutAJustificationIsRefused(t *testing.T) {
	// "Duplicate" is not a justification. Six months later the question is always "why did
	// we decide these were the same person".
	h := newAPI(t)
	survivor, duplicate := h.twoRecordsOfOnePerson(t)

	resp, body := h.mergeCall(t, survivor["id"].(string), duplicate["id"].(string), "dup")
	if resp.StatusCode < 400 {
		t.Fatalf("an unjustified merge was accepted: %d %v", resp.StatusCode, body)
	}
	var merges int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM core.patient_merge`).Scan(&merges); err != nil {
		t.Fatal(err)
	}
	if merges != 0 {
		t.Errorf("an unjustified merge was recorded")
	}
}

func TestARecordIsMergedAwayOnlyOnce(t *testing.T) {
	// A second merge of the same loser would make "where did this patient's history go"
	// ambiguous.
	h := newAPI(t)
	survivor, duplicate := h.twoRecordsOfOnePerson(t)
	third := h.registerAs(t, func(body map[string]any) {
		body["name_en"] = "Mohammod Rahim"
		body["phone_primary"] = "01733333303"
		body["consent_reference"] = "consent_2026_0003"
	})

	const why = "Same person: second registration at the outreach camp on 12 August."
	if resp, _ := h.mergeCall(t, survivor["id"].(string), duplicate["id"].(string), why); resp.StatusCode != http.StatusOK {
		t.Fatal("the first merge failed")
	}
	resp, body := h.mergeCall(t, third["id"].(string), duplicate["id"].(string), why)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("a record was merged twice: %d %v", resp.StatusCode, body)
	}
}

func TestARecordCannotBeMergedIntoItselfOrIntoAMergedOne(t *testing.T) {
	h := newAPI(t)
	survivor, duplicate := h.twoRecordsOfOnePerson(t)
	const why = "Same person: second registration at the outreach camp on 12 August."

	if resp, _ := h.mergeCall(t, survivor["id"].(string), survivor["id"].(string), why); resp.StatusCode < 400 {
		t.Errorf("a record was merged into itself: %d", resp.StatusCode)
	}
	if resp, _ := h.mergeCall(t, survivor["id"].(string), duplicate["id"].(string), why); resp.StatusCode != http.StatusOK {
		t.Fatal("the first merge failed")
	}
	// Merging into a record that itself redirects would build a chain nobody asked for.
	third := h.registerAs(t, func(body map[string]any) {
		body["name_en"] = "Mohammod Rahim"
		body["phone_primary"] = "01733333304"
		body["consent_reference"] = "consent_2026_0004"
	})
	resp, body := h.mergeCall(t, duplicate["id"].(string), third["id"].(string), why)
	if resp.StatusCode < 400 {
		t.Errorf("a record was merged into a merged one: %d %v", resp.StatusCode, body)
	}
}

func TestARetriedMergeChangesNothing(t *testing.T) {
	h := newAPI(t)
	survivor, duplicate := h.twoRecordsOfOnePerson(t)
	request := map[string]any{
		"event_id":      uuid.Must(uuid.NewV7()).String(),
		"merged_id":     duplicate["id"],
		"score":         0.86,
		"decision":      "reviewed_match",
		"justification": "Same person: second registration at the outreach camp on 12 August.",
	}
	path := "/v1/patients/" + survivor["id"].(string) + "/merge"

	if resp, body := h.call(t, http.MethodPost, path, request); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %v", resp.StatusCode, body)
	}
	if resp, body := h.call(t, http.MethodPost, path, request); resp.StatusCode != http.StatusOK {
		t.Fatalf("the retry returned %d: %v", resp.StatusCode, body)
	}
	var merges, events int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM core.patient_merge`).Scan(&merges); err != nil {
		t.Fatal(err)
	}
	if err := h.SQL.QueryRow(
		`SELECT count(*) FROM ledger.event WHERE event_type = 'PATIENT_MERGED'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if merges != 1 || events != 1 {
		t.Errorf("a retried merge produced %d records and %d events", merges, events)
	}
}

func TestMergingNeedsThePermission(t *testing.T) {
	h := newAPI(t, "patient.write.demographics", "patient.read.demographics")
	survivor, duplicate := h.twoRecordsOfOnePerson(t)
	resp, _ := h.mergeCall(t, survivor["id"].(string), duplicate["id"].(string),
		"Same person: second registration at the outreach camp on 12 August.")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a registration officer merged two records: %d", resp.StatusCode)
	}
}

func TestAMergedRecordIsNoLongerACandidate(t *testing.T) {
	// Once two records are one person, the merged-away one must stop appearing in the
	// duplicate list — otherwise every subsequent registration of a similar name shows a
	// record that no longer means anything.
	h := newAPI(t)
	survivor, duplicate := h.twoRecordsOfOnePerson(t)
	if resp, _ := h.mergeCall(t, survivor["id"].(string), duplicate["id"].(string),
		"Same person: second registration at the outreach camp on 12 August."); resp.StatusCode != http.StatusOK {
		t.Fatal("the merge failed")
	}

	verdict := h.check(t, func(body map[string]any) {
		body["name_en"] = "Mohammad Rahim"
		body["birth_date"] = "1985-06-14"
		body["phone_primary"] = "01799999999"
	})
	for _, raw := range verdict["candidates"].([]any) {
		candidate := raw.(map[string]any)
		if candidate["patient_id"] == duplicate["id"] {
			t.Errorf("a merged-away record is still offered as a duplicate: %v", candidate)
		}
	}
}
