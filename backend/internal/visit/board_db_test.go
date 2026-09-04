package visit_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The Clinic Traffic Control board (CP40, §5.2).
//
// Four acceptance criteria. Two of them — "updates within 2s" and "obvious at 5 metres" —
// are properties of a screen and are verified on the clinic's actual display; what is
// testable here is the mechanism each rests on: that a station event produces an
// announcement at all, and that the payload carries the heat level the screen colours from.
//
// The other two are testable outright, and criterion 2 is the one this file exists for.

func (h *api) board(t *testing.T) map[string]any {
	t.Helper()
	resp, body := h.call(t, http.MethodGet, "/v1/board", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reading the board: %d %v", resp.StatusCode, body)
	}
	return body
}

// waitingAt puts n patients into one station's queue, each entering `spread` earlier than
// the last so the board has a range of waiting times to work with.
func (h *api) waitingAt(t *testing.T, station string, n int, oldest time.Duration) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := range n {
		// The clinical id has to be unique across the whole test, not just this call: a
		// board test seeds several stations and each one is a separate call.
		patient := h.aPatient(t, fmt.Sprintf("DTHC-FRD-2026-9%03d%02d", stationSeed(station), i))
		visitID := h.visitFor(t, patient)
		resp, body := h.enqueue(t, visitID, station, nil)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("enqueueing at %s: %d %v", station, resp.StatusCode, body)
		}
		entryID := body["entry"].(map[string]any)["id"].(string)
		ids = append(ids, entryID)

		// Age the entry. The clock is fixed, so backdating `entered_at` is how a waiting
		// time exists at all in a test — and it is honest, because `entered_at` is exactly
		// what production measures from.
		age := oldest - time.Duration(i)*time.Minute
		if age < 0 {
			age = 0
		}
		if _, err := h.SQL.Exec(
			`UPDATE core.queue_entry SET entered_at = $2 WHERE id = $1`,
			entryID, h.clock.Now().Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

// stationSeed keeps clinical ids from colliding between the stations one test seeds.
func stationSeed(station string) int {
	sum := 0
	for _, r := range station {
		sum = (sum*31 + int(r)) % 997
	}
	return sum
}

func stationOf(t *testing.T, board map[string]any, code string) map[string]any {
	t.Helper()
	for _, raw := range board["stations"].([]any) {
		station := raw.(map[string]any)
		if station["station_code"] == code {
			return station
		}
	}
	t.Fatalf("the board has no %s column: %v", code, board["stations"])
	return nil
}

func TestTheBoardCarriesNoPatientIdentifier(t *testing.T) {
	// Acceptance criterion 2, and the whole reason this board is built the way it is.
	//
	// The criterion says "no clinical diagnosis appears on the board". This test asserts
	// something stronger, because the weaker version is untestable in the way that matters:
	// a payload containing no diagnosis *today* is one column away from containing one. So
	// the assertion is on the shape — the entire serialised board must contain no patient
	// id, no name, and none of the four clinical fields that sit one join away.
	h := newAPI(t)
	h.waitingAt(t, "STN_ANTHROPOMETRY", 3, 20*time.Minute)

	// Give the visit a diagnosis and the queue entry a priority reason: the two places a
	// clinical fact could leak from, both populated so their absence means something.
	if _, err := h.SQL.Exec(`UPDATE core.visit SET diagnoses = 'Type 2 diabetes mellitus',
	                                chief_complaint = 'polyuria for three weeks'`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`UPDATE core.queue_entry
	                            SET priority = 5, priority_reason = 'critical glucose 24.8'`); err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(h.board(t))
	if err != nil {
		t.Fatal(err)
	}
	payload := string(raw)

	forbidden := map[string]string{
		"Type 2 diabetes mellitus": "a diagnosis",
		"polyuria":                 "a chief complaint",
		"critical glucose":         "the reason somebody is being seen first",
		"Fatema Begum":             "a patient's name",
		h.patient.String():         "a patient id, which is a join key to everything else",
	}
	for needle, what := range forbidden {
		if strings.Contains(payload, needle) {
			t.Errorf("the wall board payload contains %s (%q)", what, needle)
		}
	}

	// And positively: it does say somebody is prioritised, because that is operationally
	// necessary and not sensitive. The board shows *that*, never *why*.
	station := stationOf(t, h.board(t), "STN_ANTHROPOMETRY")
	entries := station["entries"].([]any)
	if len(entries) == 0 {
		t.Fatal("the anthropometry column is empty")
	}
	if entries[0].(map[string]any)["flagged"] != true {
		t.Error("a prioritised patient is not flagged on the board")
	}
}

func TestTheBoardNamesPatientsByTheFacilityConvention(t *testing.T) {
	// The open decision, made safely: the default is the visit code alone, which means
	// nothing to anyone not holding the card it is printed on. Initials and the clinical id
	// are available and both are a deliberate act.
	h := newAPI(t)
	h.waitingAt(t, "STN_ANTHROPOMETRY", 1, time.Minute)

	label := func() string {
		station := stationOf(t, h.board(t), "STN_ANTHROPOMETRY")
		return station["entries"].([]any)[0].(map[string]any)["label"].(string)
	}

	first := label()
	if !strings.HasPrefix(first, "V-") {
		t.Fatalf("the default label is not a visit code: %q", first)
	}
	if strings.Contains(first, ".") || strings.Contains(first, "DTHC-") {
		t.Errorf("the default convention leaked an identifier: %q", first)
	}

	for _, convention := range []struct {
		setting string
		wants   string
	}{
		{"code_initials", "F.B."},
		{"code_clinical", "DTHC-FRD-2026-9"},
	} {
		if _, err := h.SQL.Exec(
			`UPDATE core.board_setting SET identify_by = $1`, convention.setting); err != nil {
			t.Fatal(err)
		}
		if got := label(); !strings.Contains(got, convention.wants) {
			t.Errorf("under %s the label is %q, wanted it to contain %q",
				convention.setting, got, convention.wants)
		}
	}
}

func TestTheBoardGoesAmberThenRed(t *testing.T) {
	// Acceptance criterion 3's mechanism. Whether it is obvious at five metres is a
	// question for the wall; what the server owes is a level that changes at the
	// thresholds, in both dimensions, in the right order.
	h := newAPI(t)

	// Calm: two people, three minutes.
	h.waitingAt(t, "STN_HISTORY", 2, 3*time.Minute)
	// Deep but recent: five people, one minute each. Depth alone should raise it.
	h.waitingAt(t, "STN_EXAMINATION", 5, time.Minute)
	// Shallow but old: one person, forty minutes. Wait alone should raise it to red.
	h.waitingAt(t, "STN_NUTRITION", 1, 40*time.Minute)

	board := h.board(t)
	for _, want := range []struct {
		station, heat string
	}{
		{"STN_HISTORY", "calm"},
		{"STN_EXAMINATION", "busy"},
		{"STN_NUTRITION", "bottleneck"},
	} {
		if got := stationOf(t, board, want.station)["heat"]; got != want.heat {
			t.Errorf("%s is %v, wanted %s", want.station, got, want.heat)
		}
	}

	// And the thresholds are data. Halving them moves the calm station.
	if _, err := h.SQL.Exec(`UPDATE core.board_setting
	                            SET busy_depth = 2, bottleneck_depth = 2,
	                                busy_wait_seconds = 60, bottleneck_wait_seconds = 120`); err != nil {
		t.Fatal(err)
	}
	if got := stationOf(t, h.board(t), "STN_HISTORY")["heat"]; got != "bottleneck" {
		t.Errorf("after lowering the thresholds STN_HISTORY is %v, wanted bottleneck", got)
	}
}

func TestARerouteTakesEffectImmediatelyAndIsAttributed(t *testing.T) {
	// Acceptance criterion 4. The patient must be gone from one column and present in the
	// other in the very next read, and the ledger must say who moved them and why.
	h := newAPI(t)
	entries := h.waitingAt(t, "STN_EXAMINATION", 1, 30*time.Minute)

	resp, body := h.call(t, http.MethodPost, "/v1/board/reroute/"+entries[0], map[string]any{
		"event_id": uuid.Must(uuid.NewV7()).String(),
		"to":       "STN_NUTRITION",
		"reason":   "examination is backed up; nutrition is free",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rerouting: %d %v", resp.StatusCode, body)
	}

	board := h.board(t)
	for _, raw := range board["stations"].([]any) {
		station := raw.(map[string]any)
		if station["station_code"] == "STN_EXAMINATION" {
			t.Error("the patient is still in the column they were rerouted out of")
		}
	}
	if got := len(stationOf(t, board, "STN_NUTRITION")["entries"].([]any)); got != 1 {
		t.Fatalf("the destination column holds %d entries, wanted 1", got)
	}

	// Attribution, in the ledger rather than in a log line.
	var actor uuid.UUID
	var reason string
	if err := h.SQL.QueryRow(`
		SELECT actor_user_id, payload->>'reason'
		  FROM ledger.event
		 WHERE event_type = 'QUEUE_LEFT' AND payload->>'outcome' = 'rerouted'
		 ORDER BY global_seq DESC LIMIT 1`).Scan(&actor, &reason); err != nil {
		t.Fatal(err)
	}
	if actor != h.user {
		t.Errorf("the reroute is attributed to %s, wanted %s", actor, h.user)
	}
	if !strings.Contains(reason, "backed up") {
		t.Errorf("the reroute's reason is %q", reason)
	}
}

func TestARerouteIsRefusedWithoutAReasonOrADestination(t *testing.T) {
	h := newAPI(t)
	entries := h.waitingAt(t, "STN_EXAMINATION", 1, time.Minute)

	for _, bad := range []map[string]any{
		{"to": "STN_NUTRITION", "reason": "busy"},         // under five characters
		{"to": "", "reason": "examination is backed up"},  // nowhere to go
		{"to": "STN_NOWHERE", "reason": "not a station"},  // somewhere that is not a station
		{"to": "STN_EXAMINATION", "reason": "same place"}, // where they already are
	} {
		bad["event_id"] = uuid.Must(uuid.NewV7()).String()
		resp, body := h.call(t, http.MethodPost, "/v1/board/reroute/"+entries[0], bad)
		if resp.StatusCode < 400 {
			t.Errorf("reroute %v was accepted: %d %v", bad, resp.StatusCode, body)
		}
	}
}

func TestARerouteRetryDoesNotQueueThePatientTwice(t *testing.T) {
	// The tablet that lost the reply and pressed again. A reroute is two ledger entries,
	// and the second one's id is derived from the first — so a retry writes the same two
	// ids and the ledger's primary key absorbs it.
	h := newAPI(t)
	entries := h.waitingAt(t, "STN_EXAMINATION", 1, time.Minute)
	body := map[string]any{
		"event_id": uuid.Must(uuid.NewV7()).String(),
		"to":       "STN_NUTRITION",
		"reason":   "examination is backed up; nutrition is free",
	}

	first, firstBody := h.call(t, http.MethodPost, "/v1/board/reroute/"+entries[0], body)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first reroute: %d %v", first.StatusCode, firstBody)
	}
	// The retry finds the entry no longer live and says so, rather than moving somebody a
	// second time.
	second, _ := h.call(t, http.MethodPost, "/v1/board/reroute/"+entries[0], body)
	if second.StatusCode != http.StatusConflict {
		t.Errorf("the retry answered %d, wanted 409", second.StatusCode)
	}

	var live int
	if err := h.SQL.QueryRow(`
		SELECT count(*) FROM core.queue_entry
		 WHERE status IN ('waiting', 'called', 'in_service')`).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 1 {
		t.Errorf("%d live queue entries after a retried reroute, wanted 1", live)
	}
}

func TestTheBoardSuggestsMovingTheLongestWaitOutOfABottleneck(t *testing.T) {
	h := newAPI(t)
	// A bottleneck by depth, with a clear range of waits so "longest first" is observable.
	h.waitingAt(t, "STN_EXAMINATION", 8, 25*time.Minute)
	// Somewhere later in the journey with nobody in it.
	h.waitingAt(t, "STN_QA", 1, time.Minute)

	board := h.board(t)
	suggestions := board["suggestions"].([]any)
	if len(suggestions) == 0 {
		t.Fatalf("a station with eight waiting produced no suggestion: %v", board["stations"])
	}
	first := suggestions[0].(map[string]any)
	if first["from"] != "STN_EXAMINATION" {
		t.Errorf("the suggestion moves somebody out of %v", first["from"])
	}
	// Never backwards along the journey: a suggestion to go back to registration is a
	// suggestion to repeat a station.
	if first["to"] == "STN_REGISTRATION" || first["to"] == "STN_ANTHROPOMETRY" {
		t.Errorf("the suggestion sends the patient backwards, to %v", first["to"])
	}
	if first["waited_seconds"].(float64) < 20*60 {
		t.Errorf("the suggestion picked a %v-second wait, not the longest",
			first["waited_seconds"])
	}
	// The facts rather than a sentence: the board composes the sentence in whichever
	// language it is being read in.
	if first["from_waiting"].(float64) < 7 {
		t.Errorf("the suggestion says the station it would empty holds %v", first["from_waiting"])
	}
}

func TestEveryStationEventTellsTheBoard(t *testing.T) {
	// Criterion 1's mechanism. Two seconds is a property of Redis and a websocket; what
	// this module owes is an announcement per transition, after the commit, naming the
	// station whose column changed.
	h := newAPI(t)
	entries := h.waitingAt(t, "STN_ANTHROPOMETRY", 1, time.Minute)
	h.notices.reset()

	if resp, body := h.callNext(t, "STN_ANTHROPOMETRY"); resp.StatusCode != http.StatusOK {
		t.Fatalf("call-next: %d %v", resp.StatusCode, body)
	}
	if resp, body := h.call(t, http.MethodPost, "/v1/stations/queue/"+entries[0]+"/leave",
		map[string]any{
			"event_id": uuid.Must(uuid.NewV7()).String(),
			"outcome":  "served", "reason": "measurements recorded",
		}); resp.StatusCode != http.StatusOK {
		t.Fatalf("leaving: %d %v", resp.StatusCode, body)
	}

	kinds := h.notices.kinds()
	for _, want := range []string{"queue.called", "queue.left"} {
		found := false
		for _, got := range kinds {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("no %s announcement; the board saw %v", want, kinds)
		}
	}

	// The announcement carries a depth, so a screen can redraw one column without a
	// refetch, and it carries no patient id — the same rule as the board payload, because
	// the wall display subscribes to this channel.
	for _, change := range h.notices.seen {
		if change.StationCode == "" {
			t.Error("an announcement does not say which station changed")
		}
		encoded, err := json.Marshal(change)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), h.patient.String()) {
			t.Error("a queue announcement carries a patient id")
		}
	}
}

func TestTheBoardRefusesAReaderWithoutItsOwnPermission(t *testing.T) {
	// The wall display holds `board.read` and nothing else. A caller without it is refused
	// even though they can read visits — the two are deliberately different permissions.
	h := newAPI(t, "visit.open", "visit.read", "visit.attend")
	resp, _ := h.call(t, http.MethodGet, "/v1/board", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("the board answered %d to a caller without board.read, wanted 403", resp.StatusCode)
	}
}

func TestARerouteNeedsMoreThanAStationOperatorsPermission(t *testing.T) {
	// Rerouting is deciding somebody else's queue is wrong. An anthropometry officer who
	// can push their own queue onto the next station is an officer having a bad morning.
	h := newAPI(t, "visit.open", "visit.read", "visit.attend", "board.read")
	entries := h.waitingAt(t, "STN_EXAMINATION", 1, time.Minute)
	resp, _ := h.call(t, http.MethodPost, "/v1/board/reroute/"+entries[0], map[string]any{
		"event_id": uuid.Must(uuid.NewV7()).String(),
		"to":       "STN_NUTRITION", "reason": "examination is backed up",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a station operator rerouted a patient: %d", resp.StatusCode)
	}
}

func TestTheBoardIsFastEnoughForAFullWaitingRoom(t *testing.T) {
	// "Board performance with 50 waiting patients" — the plan's testing note. The board is
	// polled by a wall display and refetched by every supervisor's phone on every change,
	// so it is the most-read endpoint in the building.
	h := newAPI(t)
	for _, station := range []string{
		"STN_ANTHROPOMETRY", "STN_HISTORY", "STN_EXAMINATION", "STN_NUTRITION", "STN_QA",
	} {
		h.waitingAt(t, station, 10, 20*time.Minute)
	}
	if _, err := h.SQL.Exec(`ANALYZE core.queue_entry, core.visit, core.patient`); err != nil {
		t.Fatal(err)
	}

	const runs = 40
	timings := make([]time.Duration, 0, runs)
	for range runs {
		start := time.Now()
		if _, err := h.store.BoardSnapshot(t.Context(), h.facility,
			h.clock.Now().Truncate(24*time.Hour), h.clock.Now()); err != nil {
			t.Fatal(err)
		}
		timings = append(timings, time.Since(start))
	}
	slowest := timings[0]
	for _, d := range timings {
		if d > slowest {
			slowest = d
		}
	}
	// Generous, because a sandbox is not a clinic's server. The number that matters is that
	// this is a two-query read of fifty rows and not an N+1 that grows with the room.
	if slowest > 300*time.Millisecond {
		t.Errorf("the slowest board read of 50 waiting patients took %s", slowest)
	}
	t.Logf("board with 50 waiting: slowest %s over %d reads", slowest, runs)
}
