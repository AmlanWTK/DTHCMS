package visit_test

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

// CP38's four acceptance criteria, and the two concurrency rules the plan names.

func TestIllegalStateTransitionsAreRejected(t *testing.T) {
	// Acceptance criterion 1, and the state machine as data rather than as a switch — so the
	// test enumerates every pair, including the ones nobody would think to try.
	states := []visit.Status{visit.Open, visit.Closed, visit.Abandoned}
	legal := map[string]bool{
		"open→closed": true, "open→abandoned": true,
		"closed→open": true, "abandoned→open": true,
	}
	for _, from := range states {
		for _, to := range states {
			want := legal[string(from)+"→"+string(to)]
			if got := visit.CanTransition(from, to); got != want {
				t.Errorf("%s → %s: CanTransition = %v, want %v", from, to, got, want)
			}
		}
	}

	// And the database refuses one too, because there will be a projector and a repair
	// script writing these rows and a rule enforced in one handler is a rule the others
	// do not have.
	h := newAPI(t)
	opened := h.openVisit(t, nil)
	id := opened["id"].(string)
	if resp, body := h.closeVisit(t, id, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("closing: %d %v", resp.StatusCode, body)
	}
	// closed → abandoned is not a legal move.
	_, err := h.SQL.Exec(
		`UPDATE core.visit SET status = 'abandoned', status_reason = 'x' WHERE id = $1`, id)
	if err == nil {
		t.Error("the database allowed closed → abandoned")
	}

	// And closing an already-closed visit is refused with a reason a person can act on.
	resp, body := h.closeVisit(t, id, nil)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("closing a closed visit returned %d: %v", resp.StatusCode, body)
	}
}

func TestEveryStationTouchProducesATimedAttributedEncounter(t *testing.T) {
	// Acceptance criterion 2.
	h := newAPI(t)
	opened := h.openVisit(t, nil)
	id := opened["id"].(string)

	journey := []string{"STN_REGISTRATION", "STN_ANTHROPOMETRY", "STN_EXAMINATION",
		"STN_CONSULTATION", "STN_RX_EDUCATION"}
	for _, station := range journey {
		resp, body := h.arrive(t, id, station)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("arriving at %s: %d %v", station, resp.StatusCode, body)
		}
		encounter := body["encounter"].(map[string]any)
		if encounter["started_role"] != "REGISTRATION" {
			t.Errorf("%s: started_role = %v", station, encounter["started_role"])
		}
		// Time passes at the station.
		h.clock.Advance(4 * time.Minute)
		if resp, body := h.depart(t, id, encounter["id"].(string), "completed"); resp.StatusCode != http.StatusOK {
			t.Fatalf("departing %s: %d %v", station, resp.StatusCode, body)
		}
	}

	resp, summary := h.call(t, http.MethodGet, "/v1/visits/"+id, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reading the visit: %d %v", resp.StatusCode, summary)
	}
	encounters := summary["encounters"].([]any)
	if len(encounters) != len(journey) {
		t.Fatalf("got %d encounters for a five-station journey", len(encounters))
	}
	for i, item := range encounters {
		encounter := item.(map[string]any)
		if encounter["station_code"] != journey[i] {
			t.Errorf("encounter %d is at %v, want %s", i, encounter["station_code"], journey[i])
		}
		if encounter["seconds"] == nil || encounter["seconds"].(float64) <= 0 {
			t.Errorf("%v has no measured duration", encounter["station_code"])
		}
		if encounter["ended_at"] == nil {
			t.Errorf("%v never ended", encounter["station_code"])
		}
	}
	// Twenty minutes at stations, and the visit is twenty minutes long, so nothing was spent
	// waiting. The arithmetic matters: §14.2's bottleneck analysis is this subtraction.
	if summary["total_seconds"].(float64) != 20*60 {
		t.Errorf("total_seconds = %v", summary["total_seconds"])
	}
	if summary["waiting_seconds"].(float64) != 0 {
		t.Errorf("waiting_seconds = %v with no gaps between stations", summary["waiting_seconds"])
	}

	// The invariant holds too.
	if _, err := h.SQL.Exec(`SELECT core.assert_encounters_are_timed()`); err != nil {
		t.Fatalf("the encounter invariant does not hold: %v", err)
	}
}

func TestWaitingIsTheTimeNotAtAStation(t *testing.T) {
	// What a patient actually experiences. The number a clinic acts on is this one, not the
	// total — twenty minutes of care inside two hours in the building is the complaint.
	h := newAPI(t)
	opened := h.openVisit(t, nil)
	id := opened["id"].(string)

	h.clock.Advance(30 * time.Minute) // half an hour in the waiting room
	_, body := h.arrive(t, id, "STN_ANTHROPOMETRY")
	encounter := body["encounter"].(map[string]any)
	h.clock.Advance(5 * time.Minute)
	h.depart(t, id, encounter["id"].(string), "completed") //nolint:errcheck // asserted below

	_, summary := h.call(t, http.MethodGet, "/v1/visits/"+id, nil)
	if summary["total_seconds"].(float64) != 35*60 {
		t.Errorf("total_seconds = %v", summary["total_seconds"])
	}
	if summary["waiting_seconds"].(float64) != 30*60 {
		t.Errorf("waiting_seconds = %v, want thirty minutes", summary["waiting_seconds"])
	}
}

func TestAClosedVisitCarriesTheVisitMemory(t *testing.T) {
	// Acceptance criterion 3, §11.1.
	h := newAPI(t)
	opened := h.openVisit(t, nil)
	id := opened["id"].(string)

	// Each of the four missing in turn is refused.
	for field, body := range map[string]map[string]any{
		"diagnoses":        {"diagnoses": ""},
		"plan":             {"plan": ""},
		"next_review_days": {"next_review_days": 0},
	} {
		resp, decoded := h.closeVisit(t, id, func(b map[string]any) {
			for k, v := range body {
				b[k] = v
			}
		})
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("closing without %s returned %d: %v", field, resp.StatusCode, decoded)
		}
	}

	resp, decoded := h.closeVisit(t, id, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("closing: %d %v", resp.StatusCode, decoded)
	}
	closed := decoded["visit"].(map[string]any)
	if closed["chief_complaint"] != "Sugar problem for three months." {
		t.Errorf("the complaint was lost at close: %v", closed["chief_complaint"])
	}
	if closed["next_review_days"].(float64) != 90 {
		t.Errorf("next_review_days = %v", closed["next_review_days"])
	}
	// A date as well as an interval, so the outreach engine has something to compare to today.
	if closed["next_review_on"] == nil {
		t.Error("no next review date was computed")
	}

	// And the invariant refuses an incomplete closed visit however it was written.
	if _, err := h.SQL.Exec(`SELECT core.assert_closed_visits_are_complete()`); err != nil {
		t.Fatalf("the §11.1 invariant does not hold: %v", err)
	}
}

func TestAClosedVisitCannotBeSilentlyModified(t *testing.T) {
	// Acceptance criterion 4. §4.3's correction principle applies to a visit as much as to a
	// value: a closed record that changes without saying so is what it forbids.
	h := newAPI(t)
	opened := h.openVisit(t, nil)
	id := opened["id"].(string)
	if resp, _ := h.closeVisit(t, id, nil); resp.StatusCode != http.StatusOK {
		t.Fatal("closing failed")
	}

	for _, statement := range []string{
		`UPDATE core.visit SET diagnoses = 'Something else' WHERE id = $1`,
		`UPDATE core.visit SET plan = 'Something else' WHERE id = $1`,
		`UPDATE core.visit SET next_review_days = 7 WHERE id = $1`,
		`UPDATE core.visit SET chief_complaint = 'Something else' WHERE id = $1`,
	} {
		if _, err := h.SQL.Exec(statement, id); err == nil {
			t.Errorf("a closed visit was edited in place: %s", statement)
		}
	}
	// Nor deleted: an encounter is evidence of who saw a patient and when.
	if _, err := h.SQL.Exec(`DELETE FROM core.visit WHERE id = $1`, id); err == nil {
		t.Error("a visit was deleted")
	}

	// Reopening is the path, and it is recorded.
	resp, decoded := h.call(t, http.MethodPost, "/v1/visits/"+id+"/reopen", map[string]any{
		"event_id": uuid.Must(uuid.NewV7()).String(),
		"reason":   "The physician recorded the wrong review interval; correcting it.",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reopening: %d %v", resp.StatusCode, decoded)
	}
	if decoded["visit"].(map[string]any)["reopened_count"].(float64) != 1 {
		t.Errorf("the reopening was not counted: %v", decoded["visit"])
	}

	// And a reopening with no usable reason is refused.
	if resp, _ := h.closeVisit(t, id, nil); resp.StatusCode != http.StatusOK {
		t.Fatal("re-closing failed")
	}
	resp, _ = h.call(t, http.MethodPost, "/v1/visits/"+id+"/reopen", map[string]any{
		"event_id": uuid.Must(uuid.NewV7()).String(), "reason": "typo",
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("reopening with no reason returned %d", resp.StatusCode)
	}
}

// --- the concurrency rules ---

func TestOnlyOneVisitOpensWhenTwoDesksTryAtOnce(t *testing.T) {
	// A patient handed to two registration desks. Two open visits would mean two queues for
	// one person and two places for the physician's note to go — and the desk would not know
	// which.
	h := newAPI(t)
	const attempts = 12

	var wg sync.WaitGroup
	results := make([]int, attempts)
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, _ := h.call(t, http.MethodPost, "/v1/visits", map[string]any{
				"event_id": uuid.Must(uuid.NewV7()).String(), "patient_id": h.patient.String(),
				"visit_type": "new", "chief_complaint": "Sugar problem.",
			})
			results[i] = resp.StatusCode
		}()
	}
	wg.Wait()

	created := 0
	for _, status := range results {
		if status == http.StatusCreated {
			created++
		} else if status != http.StatusConflict {
			t.Errorf("an attempt returned %d, want 201 or 409", status)
		}
	}
	if created != 1 {
		t.Errorf("%d visits were opened for one patient", created)
	}

	var open int
	if err := h.SQL.QueryRow(
		`SELECT count(*) FROM core.visit WHERE patient_id = $1 AND status = 'open'`,
		h.patient).Scan(&open); err != nil {
		t.Fatal(err)
	}
	if open != 1 {
		t.Errorf("the database holds %d open visits for one patient", open)
	}
}

func TestAPatientCannotBeAtOneStationTwiceAtOnce(t *testing.T) {
	// Two tablets pressing "start" in the same second. The plan names concurrent station
	// entry as a test, and it is an index rather than a check in a handler because no amount
	// of care in a handler wins this race.
	h := newAPI(t)
	opened := h.openVisit(t, nil)
	id := opened["id"].(string)

	const attempts = 12
	var wg sync.WaitGroup
	results := make([]int, attempts)
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, _ := h.arrive(t, id, "STN_EXAMINATION")
			results[i] = resp.StatusCode
		}()
	}
	wg.Wait()

	started := 0
	for _, status := range results {
		if status == http.StatusCreated {
			started++
		} else if status != http.StatusConflict {
			t.Errorf("an attempt returned %d, want 201 or 409", status)
		}
	}
	if started != 1 {
		t.Errorf("%d encounters were started at one station", started)
	}
}

func TestAPatientReturningToAStationIsANewEncounter(t *testing.T) {
	// Which is what makes rework countable (§14.2). A bounce recorded as "completed" makes
	// rework invisible, and rework is the one number a quality gate exists to produce.
	h := newAPI(t)
	opened := h.openVisit(t, nil)
	id := opened["id"].(string)

	_, first := h.arrive(t, id, "STN_QA")
	firstID := first["encounter"].(map[string]any)["id"].(string)
	h.clock.Advance(2 * time.Minute)
	if resp, body := h.depart(t, id, firstID, "bounced"); resp.StatusCode != http.StatusOK {
		t.Fatalf("bouncing: %d %v", resp.StatusCode, body)
	}

	// The same station again, which the partial index now allows because the first is closed.
	resp, second := h.arrive(t, id, "STN_QA")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("returning to the station: %d %v", resp.StatusCode, second)
	}

	var bounced, total int
	if err := h.SQL.QueryRow(
		`SELECT count(*) FILTER (WHERE status = 'bounced'), count(*)
		   FROM core.encounter WHERE visit_id = $1 AND station_code = 'STN_QA'`,
		id).Scan(&bounced, &total); err != nil {
		t.Fatal(err)
	}
	if bounced != 1 || total != 2 {
		t.Errorf("QA has %d encounters of which %d bounced; want 2 and 1", total, bounced)
	}

	// And an encounter ends once.
	if resp, _ := h.depart(t, id, firstID, "completed"); resp.StatusCode != http.StatusConflict {
		t.Errorf("finishing an ended encounter returned %d", resp.StatusCode)
	}
}

func TestAnAbandonedVisitIsNotACompletedOne(t *testing.T) {
	// §14.2 counts throughput, and a visit nobody completed must not be counted as a
	// completed journey — the number that results is the one somebody puts in a report.
	h := newAPI(t)
	opened := h.openVisit(t, nil)
	id := opened["id"].(string)

	resp, decoded := h.call(t, http.MethodPost, "/v1/visits/"+id+"/abandon", map[string]any{
		"event_id": uuid.Must(uuid.NewV7()).String(), "reason": "patient_left",
		"note": "Left after forty minutes without being seen.",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("abandoning: %d %v", resp.StatusCode, decoded)
	}
	if decoded["visit"].(map[string]any)["status"] != "abandoned" {
		t.Errorf("status = %v", decoded["visit"])
	}

	// The patient can be given a new visit at once — they came back.
	if resp, _ := h.call(t, http.MethodPost, "/v1/visits", map[string]any{
		"event_id": uuid.Must(uuid.NewV7()).String(), "patient_id": h.patient.String(),
		"visit_type": "new",
	}); resp.StatusCode != http.StatusCreated {
		t.Errorf("opening a visit after an abandonment returned %d", resp.StatusCode)
	}
}

func TestARetriedOpenFindsTheSameVisit(t *testing.T) {
	// A tablet that opened a visit, lost the reply and pressed again must not open a second
	// one an hour later.
	h := newAPI(t)
	eventID := uuid.Must(uuid.NewV7()).String()
	same := func(b map[string]any) { b["event_id"] = eventID }

	first := h.openVisit(t, same)
	h.clock.Advance(time.Hour)
	resp, decoded := h.call(t, http.MethodPost, "/v1/visits", map[string]any{
		"event_id": eventID, "patient_id": h.patient.String(), "visit_type": "new",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("the retry returned %d: %v", resp.StatusCode, decoded)
	}
	if decoded["visit"].(map[string]any)["id"] != first["id"] {
		t.Errorf("the retry opened a different visit: %v vs %v",
			decoded["visit"].(map[string]any)["id"], first["id"])
	}

	var visits int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM core.visit`).Scan(&visits); err != nil {
		t.Fatal(err)
	}
	if visits != 1 {
		t.Errorf("%d visits exist after one open and one retry", visits)
	}
}

func TestTheVisitCodeIsGaplessPerClinicDay(t *testing.T) {
	// Spoken at a desk, so a gap reads as a lost patient.
	h := newAPI(t)
	first := h.openVisit(t, nil)
	if resp, _ := h.call(t, http.MethodPost, "/v1/visits/"+first["id"].(string)+"/abandon",
		map[string]any{"event_id": uuid.Must(uuid.NewV7()).String(), "reason": "patient_left"}); resp.StatusCode != http.StatusOK {
		t.Fatal("abandoning failed")
	}
	second := h.openVisit(t, nil)

	if first["visit_code"] != "V-2026-0914-001" {
		t.Errorf("first visit code = %v", first["visit_code"])
	}
	if second["visit_code"] != "V-2026-0914-002" {
		t.Errorf("second visit code = %v", second["visit_code"])
	}
}

func TestAVisitCannotStartAtAStationTheClinicDoesNotHave(t *testing.T) {
	h := newAPI(t)
	opened := h.openVisit(t, nil)
	resp, body := h.arrive(t, opened["id"].(string), "STN_RADIOLOGY")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("an unknown station returned %d: %v", resp.StatusCode, body)
	}
}

func TestTheDayBoardIsTheClinicsDayNotUTC(t *testing.T) {
	// A visit opened at 23:50 Dhaka belongs to that day all night. Asking a Dhaka clinic for
	// "today" in UTC gives it the wrong six hours of its own morning.
	h := newAPI(t)
	h.openVisit(t, nil)

	resp, body := h.call(t, http.MethodGet, "/v1/visits/today", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the day board returned %d: %v", resp.StatusCode, body)
	}
	// 2026-09-14 04:42 UTC is 10:42 on the 14th in Dhaka.
	if body["day"] != "2026-09-14" {
		t.Errorf("day = %v", body["day"])
	}
	if len(body["visits"].([]any)) != 1 {
		t.Errorf("the board shows %d visits", len(body["visits"].([]any)))
	}
}

func TestTheEventsAreOnTheVisitAggregate(t *testing.T) {
	// Every transition is an event, in the same transaction as the row it describes: a visit
	// row with no event is a fact with no history.
	h := newAPI(t)
	opened := h.openVisit(t, nil)
	id := opened["id"].(string)
	_, arrived := h.arrive(t, id, "STN_EXAMINATION")
	h.depart(t, id, arrived["encounter"].(map[string]any)["id"].(string), "completed") //nolint:errcheck // asserted below
	h.closeVisit(t, id, nil)                                                           //nolint:errcheck // asserted below

	rows, err := h.SQL.Query(
		`SELECT event_type FROM ledger.event WHERE aggregate_type = 'VISIT'
		   AND aggregate_id = $1 ORDER BY global_seq`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			t.Fatal(err)
		}
		got = append(got, kind)
	}
	want := []string{"VISIT_OPENED", "ENCOUNTER_STARTED", "ENCOUNTER_FINISHED", "VISIT_CLOSED"}
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d is %s, want %s", i, got[i], want[i])
		}
	}
}
