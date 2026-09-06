package visit_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

// The station queue (CP39, §5.2, §14.2).
//
// Four acceptance criteria, and the first of them is the whole checkpoint: no patient is ever
// assigned to two operators at the same station.

func (h *api) enqueue(t *testing.T, visitID, station string, edit func(map[string]any)) (*http.Response, map[string]any) {
	t.Helper()
	body := map[string]any{
		"event_id": uuid.Must(uuid.NewV7()).String(), "station_code": station,
	}
	if edit != nil {
		edit(body)
	}
	return h.call(t, http.MethodPost, "/v1/visits/"+visitID+"/queue", body)
}

func (h *api) callNext(t *testing.T, station string) (*http.Response, map[string]any) {
	t.Helper()
	return h.call(t, http.MethodPost, "/v1/stations/"+station+"/call-next",
		map[string]any{"event_id": uuid.Must(uuid.NewV7()).String()})
}

// aPatient registers a second, third … patient so a queue can hold more than one person.
func (h *api) aPatient(t *testing.T, clinicalID string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := h.SQL.Exec(`
		INSERT INTO core.patient (id, facility_id, clinical_id, name_en, sex, birth_date,
		                          dob_precision, dob_verified_by, phone_primary, status,
		                          registered_by, registered_at)
		VALUES ($1, $2, $3, 'Fatema Begum', 'female', DATE '1979-04-12',
		        'day', 'national_id', '+8801711111102', 'active', $4, now())`,
		id, h.facility, clinicalID, h.user); err != nil {
		t.Fatal(err)
	}
	// Every patient who reaches a queue has been asked about allergies (CP54's gate).
	h.asked(t, id)
	return id
}

func (h *api) visitFor(t *testing.T, patientID uuid.UUID) string {
	t.Helper()
	resp, body := h.call(t, http.MethodPost, "/v1/visits", map[string]any{
		"event_id": uuid.Must(uuid.NewV7()).String(), "patient_id": patientID.String(),
		"visit_type": "follow_up", "chief_complaint": "Routine review.",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("opening a visit: %d %v", resp.StatusCode, body)
	}
	return body["visit"].(map[string]any)["id"].(string)
}

func TestNoPatientIsCalledByTwoOperatorsAtOnce(t *testing.T) {
	// Acceptance criterion 1, and the reason the claim is in the database. Ten patients
	// waiting and twelve operators calling at once: every operator who gets somebody gets a
	// different somebody, and the two who miss out get a clear empty rather than a duplicate.
	h := newAPI(t)
	const waiting = 10

	for i := range waiting {
		patient := h.aPatient(t, "DTHC-FRD-2026-0002"+string(rune('0'+i)))
		visitID := h.visitFor(t, patient)
		if resp, body := h.enqueue(t, visitID, "STN_EXAMINATION", nil); resp.StatusCode != http.StatusCreated {
			t.Fatalf("enqueuing: %d %v", resp.StatusCode, body)
		}
	}

	const operators = 12
	var wg sync.WaitGroup
	claimed := make([]string, operators)
	statuses := make([]int, operators)
	for i := range operators {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, body := h.callNext(t, "STN_EXAMINATION")
			statuses[i] = resp.StatusCode
			if resp.StatusCode == http.StatusOK {
				claimed[i] = body["entry"].(map[string]any)["id"].(string)
			}
		}()
	}
	wg.Wait()

	seen := map[string]int{}
	got, empty := 0, 0
	for i, status := range statuses {
		switch status {
		case http.StatusOK:
			got++
			seen[claimed[i]]++
		case http.StatusNoContent:
			empty++
		default:
			t.Errorf("operator %d got %d, want 200 or 204", i, status)
		}
	}
	if got != waiting {
		t.Errorf("%d operators were given a patient, want %d", got, waiting)
	}
	if empty != operators-waiting {
		t.Errorf("%d operators found the queue empty, want %d", empty, operators-waiting)
	}
	for entry, count := range seen {
		if count != 1 {
			t.Errorf("queue entry %s was handed to %d operators", entry, count)
		}
	}

	// The invariant says the same thing as a standing check.
	if _, err := h.SQL.Exec(`SELECT core.assert_queue_claims_are_exclusive()`); err != nil {
		t.Fatalf("the exclusivity invariant does not hold: %v", err)
	}
}

func TestPriorityPatientsAppearFirst(t *testing.T) {
	// Acceptance criterion 2. §4.4's critical findings jump the queue.
	h := newAPI(t)

	// Three ordinary patients arrive first.
	var ordinary []string
	for i := range 3 {
		patient := h.aPatient(t, "DTHC-FRD-2026-0003"+string(rune('0'+i)))
		visitID := h.visitFor(t, patient)
		ordinary = append(ordinary, visitID)
		if resp, _ := h.enqueue(t, visitID, "STN_CONSULTATION", nil); resp.StatusCode != http.StatusCreated {
			t.Fatal("enqueuing failed")
		}
		h.clock.Advance(time.Minute)
	}

	// Then somebody with a critical finding, last through the door and first in the queue.
	urgent := h.aPatient(t, "DTHC-FRD-2026-00099")
	urgentVisit := h.visitFor(t, urgent)
	if resp, body := h.enqueue(t, urgentVisit, "STN_CONSULTATION", func(b map[string]any) {
		b["priority"] = 5
		b["priority_reason"] = "Random glucose 24.1 mmol/L with ketones."
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("enqueuing the urgent patient: %d %v", resp.StatusCode, body)
	}

	resp, board := h.call(t, http.MethodGet, "/v1/stations/STN_CONSULTATION/queue", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reading the queue: %d %v", resp.StatusCode, board)
	}
	entries := board["entries"].([]any)
	if len(entries) != 4 {
		t.Fatalf("the queue holds %d entries", len(entries))
	}
	if entries[0].(map[string]any)["visit_id"] != urgentVisit {
		t.Errorf("the queue's head is %v, not the urgent patient", entries[0])
	}
	// And call-next takes them, not the person who arrived first.
	_, called := h.callNext(t, "STN_CONSULTATION")
	if called["entry"].(map[string]any)["visit_id"] != urgentVisit {
		t.Errorf("call-next gave the ordinary patient ahead of the urgent one")
	}
	_ = ordinary
}

func TestJumpingTheQueueNeedsAReason(t *testing.T) {
	// Jumping a queue without a reason is the thing a queue exists to prevent.
	h := newAPI(t)
	visitID := h.openVisit(t, nil)["id"].(string)
	resp, body := h.enqueue(t, visitID, "STN_EXAMINATION", func(b map[string]any) {
		b["priority"] = 5
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("a reasonless priority returned %d: %v", resp.StatusCode, body)
	}

	// And the database refuses one however it was written.
	if _, err := h.SQL.Exec(`
		INSERT INTO core.queue_entry (facility_id, visit_id, patient_id, station_code,
		                              priority, clinic_day)
		VALUES ($1, $2, $3, 'STN_QA', 7, CURRENT_DATE)`,
		h.facility, visitID, h.patient); err == nil {
		t.Error("the database accepted a priority with no reason")
	}
}

func TestWaitingTimesAreAccurateToTheSecond(t *testing.T) {
	// Acceptance criterion 3, and the number the clinic actually acts on.
	h := newAPI(t)
	visitID := h.openVisit(t, nil)["id"].(string)
	if resp, _ := h.enqueue(t, visitID, "STN_ANTHROPOMETRY", nil); resp.StatusCode != http.StatusCreated {
		t.Fatal("enqueuing failed")
	}

	// Still waiting: the time grows.
	h.clock.Advance(7*time.Minute + 13*time.Second)
	_, queue := h.call(t, http.MethodGet, "/v1/stations/STN_ANTHROPOMETRY/queue", nil)
	entry := queue["entries"].([]any)[0].(map[string]any)
	if entry["waited_seconds"].(float64) != 7*60+13 {
		t.Errorf("waited_seconds = %v, want 433", entry["waited_seconds"])
	}

	// Called: the time stops at the call, not at now.
	_, called := h.callNext(t, "STN_ANTHROPOMETRY")
	if called["entry"].(map[string]any)["waited_seconds"].(float64) != 7*60+13 {
		t.Errorf("at the call, waited_seconds = %v", called["entry"].(map[string]any)["waited_seconds"])
	}
	h.clock.Advance(20 * time.Minute)
	_, visitQueue := h.call(t, http.MethodGet, "/v1/visits/"+visitID+"/queue", nil)
	after := visitQueue["entries"].([]any)[0].(map[string]any)
	if after["waited_seconds"].(float64) != 7*60+13 {
		t.Errorf("twenty minutes after the call, waited_seconds = %v; a waiting time must "+
			"stop when the waiting stops", after["waited_seconds"])
	}
}

func TestARerouteSaysWhereAndWhy(t *testing.T) {
	// Acceptance criterion 4.
	h := newAPI(t)
	visitID := h.openVisit(t, nil)["id"].(string)
	_, entered := h.enqueue(t, visitID, "STN_NUTRITION", nil)
	entryID := entered["entry"].(map[string]any)["id"].(string)

	// No destination.
	resp, _ := h.call(t, http.MethodPost, "/v1/stations/queue/"+entryID+"/leave", map[string]any{
		"event_id": uuid.Must(uuid.NewV7()).String(), "outcome": "rerouted",
		"reason": "The nutritionist is at lunch.",
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("a reroute with no destination returned %d", resp.StatusCode)
	}
	// No reason.
	resp, _ = h.call(t, http.MethodPost, "/v1/stations/queue/"+entryID+"/leave", map[string]any{
		"event_id": uuid.Must(uuid.NewV7()).String(), "outcome": "rerouted",
		"rerouted_to": "STN_EXERCISE",
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("a reroute with no reason returned %d", resp.StatusCode)
	}
	// A destination the clinic does not have.
	resp, _ = h.call(t, http.MethodPost, "/v1/stations/queue/"+entryID+"/leave", map[string]any{
		"event_id": uuid.Must(uuid.NewV7()).String(), "outcome": "rerouted",
		"rerouted_to": "STN_RADIOLOGY", "reason": "Needs an X-ray.",
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("a reroute to nowhere returned %d", resp.StatusCode)
	}

	// And a complete one, attributed.
	resp, body := h.call(t, http.MethodPost, "/v1/stations/queue/"+entryID+"/leave", map[string]any{
		"event_id": uuid.Must(uuid.NewV7()).String(), "outcome": "rerouted",
		"rerouted_to": "STN_EXERCISE", "reason": "The nutritionist is away; exercise first.",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rerouting: %d %v", resp.StatusCode, body)
	}
	entry := body["entry"].(map[string]any)
	if entry["status"] != "rerouted" || entry["rerouted_to"] != "STN_EXERCISE" {
		t.Errorf("entry = %v", entry)
	}

	// The event carries who did it.
	var actorRole string
	if err := h.SQL.QueryRow(
		`SELECT actor_role FROM ledger.event WHERE event_type = 'QUEUE_LEFT'`).Scan(&actorRole); err != nil {
		t.Fatal(err)
	}
	if actorRole != "REGISTRATION" {
		t.Errorf("the reroute event has actor_role %q", actorRole)
	}

	if _, err := h.SQL.Exec(`SELECT core.assert_queue_departures_are_explained()`); err != nil {
		t.Fatalf("the reroute invariant does not hold: %v", err)
	}
}

func TestAPatientCannotWaitTwiceAtOneStation(t *testing.T) {
	// A patient waiting twice is a patient who will be called twice, and the second call is
	// the one nobody can explain.
	h := newAPI(t)
	visitID := h.openVisit(t, nil)["id"].(string)
	if resp, _ := h.enqueue(t, visitID, "STN_QA", nil); resp.StatusCode != http.StatusCreated {
		t.Fatal("enqueuing failed")
	}
	resp, _ := h.enqueue(t, visitID, "STN_QA", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("a second entry at one station returned %d", resp.StatusCode)
	}

	// Once resolved, they may be queued again — which is what a QA bounce needs.
	_, queue := h.call(t, http.MethodGet, "/v1/visits/"+visitID+"/queue", nil)
	entryID := queue["entries"].([]any)[0].(map[string]any)["id"].(string)
	if resp, body := h.call(t, http.MethodPost, "/v1/stations/queue/"+entryID+"/leave",
		map[string]any{"event_id": uuid.Must(uuid.NewV7()).String(), "outcome": "served"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("resolving: %d %v", resp.StatusCode, body)
	}
	if resp, body := h.enqueue(t, visitID, "STN_QA", nil); resp.StatusCode != http.StatusCreated {
		t.Errorf("re-queuing after a bounce returned %d: %v", resp.StatusCode, body)
	}
}

func TestTheBoardShowsTheLongestWaitNotJustTheAverage(t *testing.T) {
	// An average hides the person who has been sitting there since nine.
	h := newAPI(t)

	first := h.openVisit(t, nil)["id"].(string)
	if resp, _ := h.enqueue(t, first, "STN_COUNSELING", nil); resp.StatusCode != http.StatusCreated {
		t.Fatal("enqueuing failed")
	}
	h.clock.Advance(50 * time.Minute)

	second := h.visitFor(t, h.aPatient(t, "DTHC-FRD-2026-00077"))
	if resp, _ := h.enqueue(t, second, "STN_COUNSELING", nil); resp.StatusCode != http.StatusCreated {
		t.Fatal("enqueuing failed")
	}
	h.clock.Advance(2 * time.Minute)

	resp, board := h.call(t, http.MethodGet, "/v1/stations/board", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the board returned %d: %v", resp.StatusCode, board)
	}
	var counselling map[string]any
	for _, row := range board["stations"].([]any) {
		if row.(map[string]any)["station_code"] == "STN_COUNSELING" {
			counselling = row.(map[string]any)
		}
	}
	if counselling == nil {
		t.Fatalf("counselling is not on the board: %v", board)
	}
	if counselling["waiting"].(float64) != 2 {
		t.Errorf("waiting = %v", counselling["waiting"])
	}
	if counselling["longest_wait_seconds"].(float64) != 52*60 {
		t.Errorf("longest_wait_seconds = %v, want 3120", counselling["longest_wait_seconds"])
	}
	// The average is 27 minutes, which is what would hide the person waiting 52.
	if counselling["average_wait_seconds"].(float64) != 27*60 {
		t.Errorf("average_wait_seconds = %v", counselling["average_wait_seconds"])
	}
}

func TestTheStationSequenceIsDataRatherThanCode(t *testing.T) {
	// The sequences are an operational decision still owed by the clinic, and one in Go is
	// one that needs a deployment to change.
	h := newAPI(t)
	var newCount, followCount int
	if err := h.SQL.QueryRow(
		`SELECT count(*) FROM core.station_sequence WHERE visit_type = 'new'`).Scan(&newCount); err != nil {
		t.Fatal(err)
	}
	if err := h.SQL.QueryRow(
		`SELECT count(*) FROM core.station_sequence WHERE visit_type = 'follow_up'`).Scan(&followCount); err != nil {
		t.Fatal(err)
	}
	if newCount != 12 {
		t.Errorf("a new visit plans %d stations, want §3's twelve", newCount)
	}
	if followCount >= newCount {
		t.Errorf("a follow-up plans %d stations and a new visit %d; a returning patient does "+
			"not repeat the history and records stations", followCount, newCount)
	}

	// And the position lands on the queue entry, so the board can order by the journey.
	visitID := h.openVisit(t, func(b map[string]any) { b["visit_type"] = "new" })["id"].(string)
	_, entered := h.enqueue(t, visitID, "STN_CONSULTATION", nil)
	if entered["entry"].(map[string]any)["position"].(float64) != 9 {
		t.Errorf("consultation is at position %v in a new visit's journey",
			entered["entry"].(map[string]any)["position"])
	}
}

// stubGate stands in for a checkpoint (CP57). The production one is the counselling gate, wired
// in cmd/api because `visit` may not import `counseling`; what this module can test is that it
// asks, refuses with what it was told, and records both answers.
type stubGate struct {
	holds   bool
	station string
	missing []string
}

func (g *stubGate) Check(_ context.Context, _ uuid.UUID, station string) (visit.GateDecision, error) {
	if g.station == "" || station != g.station {
		return visit.GateDecision{}, nil
	}
	return visit.GateDecision{
		Applies: true, Allowed: !g.holds, Name: "COUNSELING", Missing: g.missing,
		MissingEN: "Counselling is not finished: glucometer use; insulin injection sites.",
		MissingBN: "কাউন্সেলিং শেষ হয়নি: গ্লুকোমিটার ব্যবহার; ইনসুলিন দেওয়ার স্থান।",
	}, nil
}

func TestACheckpointHoldsThePatientAndSaysWhatIsMissing(t *testing.T) {
	// CP57 criterion 2, on the path an operator actually takes. The database refuses this
	// insert too — that trigger is the enforcement — but a trigger's exception is not a screen,
	// and "not allowed" would send an operator back with no idea what to do next.
	h := newAPI(t)
	h.gate.station = "STN_CONSULTATION"
	h.gate.holds = true
	h.gate.missing = []string{"GLUCOMETER", "INSULIN_TECHNIQUE"}

	patient := h.aPatient(t, "DTHC-FRD-2026-000301")
	visitID := h.visitFor(t, patient)

	resp, body := h.enqueue(t, visitID, "STN_CONSULTATION", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("a held patient was enqueued: %d %v", resp.StatusCode, body)
	}
	failure, _ := body["error"].(map[string]any)
	if failure["code"] != "VISIT_GATE_BLOCKED" {
		t.Fatalf("the refusal's code is %v, which no client can branch on", failure["code"])
	}
	message, _ := failure["message"].(string)
	if !strings.Contains(message, "glucometer use") {
		t.Fatalf("the refusal does not name the missing items: %q", message)
	}
	if bangla, _ := failure["message_bn"].(string); !strings.Contains(bangla, "গ্লুকোমিটার") {
		t.Fatalf("the refusal is not in both languages: %q", bangla)
	}

	// And it is recorded. "Held 14 times, passed 300" is a working checkpoint; "held 14, passed
	// 14" is one nobody can get through, and neither number exists unless both are written.
	var blocked int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM ledger.event
		WHERE event_type = 'VISIT_GATE_BLOCKED'`).Scan(&blocked); err != nil {
		t.Fatal(err)
	}
	if blocked != 1 {
		t.Fatalf("%d gate refusals recorded, want one", blocked)
	}
}

func TestACheckpointThatPassesIsRecordedToo(t *testing.T) {
	h := newAPI(t)
	h.gate.station = "STN_CONSULTATION"
	h.gate.holds = false

	patient := h.aPatient(t, "DTHC-FRD-2026-000302")
	visitID := h.visitFor(t, patient)
	if resp, body := h.enqueue(t, visitID, "STN_CONSULTATION", nil); resp.StatusCode != http.StatusCreated {
		t.Fatalf("a cleared patient was refused: %d %v", resp.StatusCode, body)
	}

	var satisfied int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM ledger.event
		WHERE event_type = 'VISIT_GATE_SATISFIED'`).Scan(&satisfied); err != nil {
		t.Fatal(err)
	}
	if satisfied != 1 {
		t.Fatalf("%d gate passes recorded, want one", satisfied)
	}
}

func TestACheckpointSaysNothingAboutStationsItDoesNotGuard(t *testing.T) {
	// Most queue entries meet no checkpoint at all, and writing an event for each of them would
	// be a log of the whole clinic rather than a record of the gates.
	h := newAPI(t)
	h.gate.station = "STN_CONSULTATION"
	h.gate.holds = true

	patient := h.aPatient(t, "DTHC-FRD-2026-000303")
	visitID := h.visitFor(t, patient)
	if resp, body := h.enqueue(t, visitID, "STN_EXAMINATION", nil); resp.StatusCode != http.StatusCreated {
		t.Fatalf("a station behind no checkpoint refused a patient: %d %v", resp.StatusCode, body)
	}

	var events int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM ledger.event
		WHERE event_type IN ('VISIT_GATE_BLOCKED', 'VISIT_GATE_SATISFIED')`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Fatalf("%d gate events for a station no gate guards", events)
	}
}
