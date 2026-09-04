package clinical_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
)

// Critical values (CP50), the system's most direct patient-safety feature.
//
// Five acceptance criteria, and what is proven here is the four that are properties of the
// record rather than of a screen:
//
//	1. a named threshold raises an alert at the moment the value is entered — and one that is
//	   not named raises nothing, because an alarm that fires on ordinary values is an alarm
//	   staff learn to silence;
//	3. an unacknowledged alert escalates when its window passes, and never backwards;
//	4. an alert that reached nobody says so, and the operator is told to escalate in person;
//	5. every alert and every acknowledgement is in the ledger.
//
// Criterion 2 — "the consultant sees it within five seconds" — is a property of the socket
// and the phone, and it is on the manual verification list where it belongs.
//
// The thing this file leans on hardest is that the alert is raised **inside the transaction
// that stores the value**. Several tests below would still pass with a job that scanned for
// dangerous values a second later; the one that would not is the refusal test, and it is the
// one that matters, because the window that design leaves open is the window in which a
// patient is in a corridor and nothing is coming.

// alertPermissions is the consultant's set: they may read the board and answer it, and they
// may write a vital, which is how these tests raise one.
var alertPermissions = []string{
	"observation.read.values", "observation.write.vitals", "observation.write.anthro",
	"alert.read", "alert.acknowledge",
}

func (h *api) spo2(t *testing.T, value float64) (*http.Response, map[string]any) {
	t.Helper()
	h.role = "CLINICAL_ASSISTANT"
	return h.record(t, map[string]any{
		"code": "SPO2", "value": value, "unit": "%",
		"effective_at": "2026-09-14T09:00:00Z",
	})
}

func alertsOf(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	raw, _ := body["alerts"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		row, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("an alert that is not an object: %v", item)
		}
		out = append(out, row)
	}
	return out
}

// child replaces the seeded adult with a patient of the given age, so the paediatric bands
// can be exercised against the same fixtures.
func (h *api) child(t *testing.T, years int) uuid.UUID {
	t.Helper()
	id := uuid.New()
	born := h.clock.Now().UTC().AddDate(-years, 0, 0).Format("2006-01-02")
	if _, err := h.SQL.Exec(`
		INSERT INTO core.patient (id, facility_id, clinical_id, name_en, sex, birth_date,
		                          dob_precision, dob_verified_by, phone_primary, status,
		                          registered_by, registered_at)
		VALUES ($1, $2, $3, 'Ayesha Khatun', 'female', $4::date,
		        'day', 'birth_certificate', '+8801711111102', 'active', $5, now())`,
		id, h.facility, "DTHC-FRD-2026-0003"+born[8:], born, h.user); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestANamedThresholdRaisesAnAlertAsTheValueIsEntered(t *testing.T) {
	// Criterion 1, and the blueprint's own example: SpO2 below 92%.
	h := newAPI(t, alertPermissions...)

	resp, body := h.spo2(t, 88)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("a saturation of 88%%: %d %v", resp.StatusCode, body)
	}
	raised := alertsOf(t, body)
	if len(raised) != 1 {
		t.Fatalf("a saturation of 88%% raised %d alerts", len(raised))
	}
	alert := raised[0]
	if alert["breached"] != "low" {
		t.Errorf("breached is %v; 88 is below the floor", alert["breached"])
	}
	if alert["threshold"] != 92.0 {
		t.Errorf("threshold is %v; the blueprint names 92", alert["threshold"])
	}
	if alert["status"] != "OPEN" {
		t.Errorf("a freshly raised alert is %v", alert["status"])
	}
	// The action text travels with it. An alert that says only "SpO2 88" tells a clinical
	// assistant a number they already typed; what they need is the next thing to do.
	if alert["action_en"] == "" || alert["action_bn"] == "" {
		t.Error("the alert carries no instruction, in either language")
	}
	// Criterion 4, in the ordinary case for a test with no realtime gateway attached:
	// nobody was watching, so the operator is told to walk.
	if body["escalate_verbally"] != true {
		t.Errorf("with no screen listening, escalate_verbally is %v", body["escalate_verbally"])
	}
}

func TestAnOrdinaryValueRaisesNothing(t *testing.T) {
	// The other half of criterion 1, and the half that decides whether the first half is
	// worth anything. An alarm that fires on ordinary values is an alarm staff learn to
	// silence, and then the one that matters is silenced with it.
	h := newAPI(t, alertPermissions...)

	_, body := h.spo2(t, 97)
	if raised := alertsOf(t, body); len(raised) != 0 {
		t.Fatalf("a saturation of 97%% raised %d alerts", len(raised))
	}
	if _, present := body["escalate_verbally"]; present {
		t.Error("a write that raised nothing told the operator to escalate")
	}
}

func TestThePaediatricBandsAreNotTheAdultOnes(t *testing.T) {
	// The plan asks for age bands by name, and this is why: a pulse of 150 is an emergency
	// in an adult and an ordinary afternoon in a three-year-old. One band wide enough for
	// both is a band that never fires for a child.
	h := newAPI(t, alertPermissions...)
	h.role = "CLINICAL_ASSISTANT"

	toddler := h.child(t, 3)
	_, body := h.record(t, map[string]any{
		"patient_id": toddler.String(), "code": "HEART_RATE", "value": 150,
		"unit": "/min", "effective_at": "2026-09-14T09:00:00Z",
	})
	if raised := alertsOf(t, body); len(raised) != 0 {
		t.Errorf("a pulse of 150 in a three-year-old raised %d alerts", len(raised))
	}

	_, adult := h.record(t, map[string]any{
		"code": "HEART_RATE", "value": 150, "unit": "/min",
		"effective_at": "2026-09-14T09:00:00Z",
	})
	if raised := alertsOf(t, adult); len(raised) != 1 {
		t.Errorf("a pulse of 150 in a forty-one-year-old raised %d alerts", len(raised))
	}
}

func TestARefusedValueLeavesNoAlertBehind(t *testing.T) {
	// The one test that a "scan for dangerous values afterwards" design would fail, and the
	// reason the alert is appended inside the same transaction. A saturation of 20% is below
	// the absolute plausibility floor, so nothing stores it — and nothing may alert on it
	// either, because an alert about a value that does not exist sends a consultant to a
	// patient whose reading was a probe falling off.
	h := newAPI(t, alertPermissions...)

	resp, _ := h.spo2(t, 20)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a saturation of 20%% was answered %d", resp.StatusCode)
	}
	var alerts int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM read.critical_alert`).Scan(&alerts); err != nil {
		t.Fatal(err)
	}
	if alerts != 0 {
		t.Errorf("a refused value left %d alerts behind", alerts)
	}
	var events int
	if err := h.SQL.QueryRow(
		`SELECT count(*) FROM ledger.event WHERE event_type = 'CRITICAL_VALUE_ALERTED'`,
	).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Errorf("a refused value wrote %d alert events", events)
	}
}

func TestASavedTwiceDoesNotRingTwice(t *testing.T) {
	// A tablet that lost the reply and pressed save again. The observation is absorbed by the
	// ledger's own idempotency; the alert has to be absorbed with it, or a corridor with bad
	// Wi-Fi becomes a consultant's phone going off once per retry until they stop looking.
	h := newAPI(t, alertPermissions...)
	h.role = "CLINICAL_ASSISTANT"

	eventID := uuid.Must(uuid.NewV7()).String()
	body := map[string]any{
		"event_id": eventID, "code": "SPO2", "value": 88, "unit": "%",
		"effective_at": "2026-09-14T09:00:00Z",
	}
	if resp, out := h.record(t, body); resp.StatusCode != http.StatusCreated {
		t.Fatalf("first save: %d %v", resp.StatusCode, out)
	}
	resp, out := h.record(t, map[string]any{
		"event_id": eventID, "code": "SPO2", "value": 88, "unit": "%",
		"effective_at": "2026-09-14T09:00:00Z",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("the retry: %d %v", resp.StatusCode, out)
	}
	if raised := alertsOf(t, out); len(raised) != 0 {
		t.Errorf("the retry raised %d alerts", len(raised))
	}
	var alerts int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM read.critical_alert`).Scan(&alerts); err != nil {
		t.Fatal(err)
	}
	if alerts != 1 {
		t.Errorf("two saves of one reading left %d alerts", alerts)
	}
}

func TestOneFormCanRaiseTwoAlerts(t *testing.T) {
	// A blood pressure of 200/125 is two breaches, and the operator is shown both. Collapsing
	// them into "this entry has an alert" would let the second disappear behind whichever the
	// screen happened to draw.
	h := newAPI(t, alertPermissions...)

	resp, body := h.vitals(t, []map[string]any{
		{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BP_SYSTOLIC", "value": 200,
			"unit": "mm[Hg]", "effective_at": "2026-09-14T09:00:00Z"},
		{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BP_DIASTOLIC", "value": 125,
			"unit": "mm[Hg]", "effective_at": "2026-09-14T09:00:00Z"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("200/125: %d %v", resp.StatusCode, body)
	}
	raised := alertsOf(t, body)
	if len(raised) != 2 {
		t.Fatalf("200/125 raised %d alerts", len(raised))
	}
	for _, alert := range raised {
		if alert["breached"] != "high" {
			t.Errorf("%v was reported as breaching %v", alert["code"], alert["breached"])
		}
	}
}

func TestTheBoardShowsWhatIsUnansweredAndTheHistoryKeepsTheRest(t *testing.T) {
	h := newAPI(t, alertPermissions...)
	if _, body := h.spo2(t, 88); len(alertsOf(t, body)) != 1 {
		t.Fatal("the fixture did not raise an alert")
	}
	h.role = "PHYSICIAN"

	_, board := h.call(t, http.MethodGet, "/v1/alerts", nil)
	open, _ := board["alerts"].([]any)
	if len(open) != 1 {
		t.Fatalf("the board shows %d open alerts", len(open))
	}
	alert, _ := open[0].(map[string]any)
	id, _ := alert["id"].(string)

	resp, answered := h.call(t, http.MethodPost, "/v1/alerts/"+id+"/acknowledge",
		map[string]any{"note": "On oxygen, consultant at the bedside."})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("acknowledging: %d %v", resp.StatusCode, answered)
	}
	got, _ := answered["alert"].(map[string]any)
	if got["status"] != "ACKNOWLEDGED" {
		t.Errorf("after acknowledgement the alert is %v", got["status"])
	}
	if got["acknowledgement"] == "" {
		t.Error("the acknowledgement note was not kept")
	}

	_, board = h.call(t, http.MethodGet, "/v1/alerts", nil)
	if open, _ = board["alerts"].([]any); len(open) != 0 {
		t.Errorf("an answered alert is still on the board (%d)", len(open))
	}

	// It stays in the patient's history. An alert that vanished once somebody answered it
	// would make the record say the episode never happened.
	_, history := h.call(t, http.MethodGet,
		"/v1/patients/"+h.patient.String()+"/alerts", nil)
	past, _ := history["alerts"].([]any)
	if len(past) != 1 {
		t.Errorf("the patient's history holds %d alerts", len(past))
	}
}

func TestAnAcknowledgementSaysWhatIsBeingDone(t *testing.T) {
	// "Seen" is not an acknowledgement. The next person to open this record needs to know
	// what was already done, and the two minutes after a critical value are exactly when
	// nobody has time to write it down twice.
	h := newAPI(t, alertPermissions...)
	if _, body := h.spo2(t, 88); len(alertsOf(t, body)) != 1 {
		t.Fatal("the fixture did not raise an alert")
	}
	h.role = "PHYSICIAN"
	_, board := h.call(t, http.MethodGet, "/v1/alerts", nil)
	open, _ := board["alerts"].([]any)
	alert, _ := open[0].(map[string]any)
	id, _ := alert["id"].(string)

	for _, note := range []string{"", "  ", "ok"} {
		resp, _ := h.call(t, http.MethodPost, "/v1/alerts/"+id+"/acknowledge",
			map[string]any{"note": note})
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%q was accepted as an acknowledgement (%d)", note, resp.StatusCode)
		}
	}
}

func TestASecondAcknowledgementSaysSomebodyElseHasIt(t *testing.T) {
	// Two clinicians reaching for the same alert is the system working. 409 rather than 400:
	// nothing the second one sent was wrong, and the response carries the alert so their
	// screen can say who took it.
	h := newAPI(t, alertPermissions...)
	if _, body := h.spo2(t, 88); len(alertsOf(t, body)) != 1 {
		t.Fatal("the fixture did not raise an alert")
	}
	h.role = "PHYSICIAN"
	_, board := h.call(t, http.MethodGet, "/v1/alerts", nil)
	open, _ := board["alerts"].([]any)
	alert, _ := open[0].(map[string]any)
	id, _ := alert["id"].(string)

	if resp, _ := h.call(t, http.MethodPost, "/v1/alerts/"+id+"/acknowledge",
		map[string]any{"note": "Giving oral glucose."}); resp.StatusCode != http.StatusOK {
		t.Fatalf("the first acknowledgement was refused: %d", resp.StatusCode)
	}
	resp, body := h.call(t, http.MethodPost, "/v1/alerts/"+id+"/acknowledge",
		map[string]any{"note": "Also going to see them."})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("the second acknowledgement was answered %d", resp.StatusCode)
	}
	if _, carried := body["alert"]; !carried {
		t.Error("the conflict did not carry the alert, so the screen cannot say who has it")
	}
}

func TestTheOfficerWhoEnteredTheValueCannotCloseTheirOwnAlert(t *testing.T) {
	// A clinic where the person who typed the number can close the alert about it is a clinic
	// that can clear its board without a clinician ever seeing one.
	h := newAPI(t, "observation.read.values", "observation.write.vitals", "alert.read")
	if _, body := h.spo2(t, 88); len(alertsOf(t, body)) != 1 {
		t.Fatal("the fixture did not raise an alert")
	}
	_, board := h.call(t, http.MethodGet, "/v1/alerts", nil)
	open, _ := board["alerts"].([]any)
	alert, _ := open[0].(map[string]any)
	id, _ := alert["id"].(string)

	resp, _ := h.call(t, http.MethodPost, "/v1/alerts/"+id+"/acknowledge",
		map[string]any{"note": "I saw it, it is fine."})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("the entering officer closed their own alert (%d)", resp.StatusCode)
	}
}

func TestAnUnansweredAlertWalksDownTheChain(t *testing.T) {
	// Criterion 3. The sweep's arithmetic is the database's, so this drives it the way the
	// worker does: ask what is due at a given moment, advance it, and ask again.
	h := newAPI(t, alertPermissions...)
	if _, body := h.spo2(t, 88); len(alertsOf(t, body)) != 1 {
		t.Fatal("the fixture did not raise an alert")
	}
	ctx := context.Background()
	raisedAt := h.clock.Now().UTC()

	// Nothing is due in the first second: step 1 is the consultant, and it already happened.
	due, err := h.store.DueForEscalation(ctx, raisedAt.Add(time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("%d alerts were due one second after being raised", len(due))
	}

	// Two minutes later, step 2.
	due, err = h.store.DueForEscalation(ctx, raisedAt.Add(3*time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("%d alerts were due three minutes in", len(due))
	}
	if due[0].NextStep != 2 || due[0].NotifyRole != "JUNIOR_DOCTOR" {
		t.Fatalf("step %d notifies %q", due[0].NextStep, due[0].NotifyRole)
	}

	actor := eventstore.ActorForTest(h.user, h.device, h.facility, "PHYSICIAN", "STN_CONSULTATION")
	if err := h.service.Escalate(ctx, actor, due[0].Alert, due[0].NextStep, due[0].NotifyRole); err != nil {
		t.Fatal(err)
	}
	after, err := h.store.AlertByID(ctx, due[0].Alert.ID, h.facility)
	if err != nil {
		t.Fatal(err)
	}
	if after.EscalationStep != 2 || !after.Escalated() {
		t.Fatalf("after escalating, the alert is at step %d", after.EscalationStep)
	}

	// Six minutes in, the last step — which notifies no role at all, because a chain whose
	// final link is another notification has no end.
	due, err = h.store.DueForEscalation(ctx, raisedAt.Add(6*time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].NextStep != 3 || due[0].NotifyRole != "" {
		t.Fatalf("the last step is %+v", due)
	}

	// And an acknowledgement takes it off the sweep entirely.
	h.role = "PHYSICIAN"
	_, _ = h.call(t, http.MethodPost, "/v1/alerts/"+after.ID.String()+"/acknowledge",
		map[string]any{"note": "With the patient now."})
	due, err = h.store.DueForEscalation(ctx, raisedAt.Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Errorf("an acknowledged alert is still escalating (%d)", len(due))
	}
}

func TestAnEscalationNeverGoesBackwards(t *testing.T) {
	// A replay that arrived out of order — a projection rebuild, two workers, a retried
	// event — must not un-escalate an alert. The guard is in the projection function, so it
	// holds for a rebuild as well as for a live write.
	h := newAPI(t, alertPermissions...)
	if _, body := h.spo2(t, 88); len(alertsOf(t, body)) != 1 {
		t.Fatal("the fixture did not raise an alert")
	}
	ctx := context.Background()
	alerts, err := h.store.OpenAlerts(ctx, h.facility, 10)
	if err != nil || len(alerts) != 1 {
		t.Fatalf("reading the board: %v %d", err, len(alerts))
	}
	actor := eventstore.ActorForTest(h.user, h.device, h.facility, "PHYSICIAN", "STN_CONSULTATION")

	if err := h.service.Escalate(ctx, actor, alerts[0], 3, ""); err != nil {
		t.Fatal(err)
	}
	if err := h.service.Escalate(ctx, actor, alerts[0], 2, "JUNIOR_DOCTOR"); err != nil {
		t.Fatal(err)
	}
	after, err := h.store.AlertByID(ctx, alerts[0].ID, h.facility)
	if err != nil {
		t.Fatal(err)
	}
	if after.EscalationStep != 3 {
		t.Errorf("a late step-2 event moved the alert back to step %d", after.EscalationStep)
	}
}

func TestEveryAlertAndAcknowledgementIsInTheLedger(t *testing.T) {
	// Criterion 5, asserted against the ledger rather than the read model: the read model can
	// be rebuilt, and what makes that safe is that the facts are somewhere else.
	h := newAPI(t, alertPermissions...)
	if _, body := h.spo2(t, 88); len(alertsOf(t, body)) != 1 {
		t.Fatal("the fixture did not raise an alert")
	}
	h.role = "PHYSICIAN"
	_, board := h.call(t, http.MethodGet, "/v1/alerts", nil)
	open, _ := board["alerts"].([]any)
	alert, _ := open[0].(map[string]any)
	id, _ := alert["id"].(string)
	if resp, _ := h.call(t, http.MethodPost, "/v1/alerts/"+id+"/acknowledge",
		map[string]any{"note": "Oxygen started."}); resp.StatusCode != http.StatusOK {
		t.Fatal("the acknowledgement was refused")
	}

	for _, kind := range []string{
		"CRITICAL_VALUE_ALERTED",
		"CRITICAL_VALUE_DELIVERY_ATTEMPTED",
		"CRITICAL_VALUE_ACKNOWLEDGED",
	} {
		var n int
		if err := h.SQL.QueryRow(
			`SELECT count(*) FROM ledger.event WHERE event_type = $1`, kind).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("%s appears %d times in the ledger", kind, n)
		}
	}

	// And the attribution is the envelope's, not the payload's.
	var role string
	if err := h.SQL.QueryRow(
		`SELECT actor_role FROM ledger.event WHERE event_type = 'CRITICAL_VALUE_ACKNOWLEDGED'`,
	).Scan(&role); err != nil {
		t.Fatal(err)
	}
	if role != "PHYSICIAN" {
		t.Errorf("the acknowledgement was attributed to %q", role)
	}
}

func TestTheDeliveryRecordSaysWhetherAnybodyWasTold(t *testing.T) {
	// Criterion 4's evidence. No gateway is attached in this harness, so nobody was watching
	// — which is exactly the case the record has to be able to state, because "how often did
	// an alert reach nobody" is a question the clinic should answer from its own data.
	h := newAPI(t, alertPermissions...)
	if _, body := h.spo2(t, 88); len(alertsOf(t, body)) != 1 {
		t.Fatal("the fixture did not raise an alert")
	}
	var recipients int
	var notified *time.Time
	if err := h.SQL.QueryRow(
		`SELECT recipients, notified_at FROM read.critical_alert`).Scan(&recipients, &notified); err != nil {
		t.Fatal(err)
	}
	if recipients != 0 {
		t.Errorf("the alert reported %d recipients with no gateway attached", recipients)
	}
	if notified == nil {
		t.Error("no delivery attempt was recorded at all, which reads as 'we never tried'")
	}
}

func TestTheListedRulesResolveToTheSameRuleTheServerPicks(t *testing.T) {
	// The station app takes the first matching entry and never ranks anything itself. That is
	// only safe if the server's own resolution agrees, so this walks every rule the endpoint
	// publishes and asks the database which one it would have chosen.
	h := newAPI(t, alertPermissions...)
	h.role = "PHYSICIAN"

	_, body := h.call(t, http.MethodGet, "/v1/alerts/rules", nil)
	rules, _ := body["rules"].([]any)
	if len(rules) < 10 {
		t.Fatalf("the endpoint published %d rules", len(rules))
	}

	type probe struct {
		code string
		sex  string
		age  float64
	}
	for _, p := range []probe{
		{"SPO2", "male", 41}, {"SPO2", "female", 3},
		{"HEART_RATE", "male", 41}, {"HEART_RATE", "female", 3},
		{"HEART_RATE", "male", 0.5}, {"HEART_RATE", "female", 15},
		{"BP_SYSTOLIC", "male", 41}, {"BP_SYSTOLIC", "male", 8},
		{"BODY_TEMP", "female", 0.2}, {"BODY_TEMP", "female", 30},
		{"GLUCOSE_RANDOM", "male", 41}, {"GLUCOSE_RANDOM", "male", 9},
	} {
		var wantID string
		if err := h.SQL.QueryRow(
			`SELECT id::text FROM core.critical_value_for($1, $2, $3)`,
			p.code, p.sex, p.age).Scan(&wantID); err != nil {
			t.Fatalf("%s/%s/%v: %v", p.code, p.sex, p.age, err)
		}
		gotID := firstMatchingRule(rules, p.code, p.sex, p.age)
		if gotID != wantID {
			t.Errorf("%s for a %v-year-old %s: the list gives %s, the server picks %s",
				p.code, p.age, p.sex, gotID, wantID)
		}
	}
}

// firstMatchingRule is the client's rule, written out: take the first entry whose predicate
// matches, in the order the server sent them.
func firstMatchingRule(rules []any, code, sex string, age float64) string {
	for _, item := range rules {
		rule, _ := item.(map[string]any)
		if rule["code"] != code {
			continue
		}
		if ruleSex, ok := rule["sex"].(string); ok && ruleSex != "" && ruleSex != sex {
			continue
		}
		if low, ok := rule["min_age_years"].(float64); ok && age < low {
			continue
		}
		if high, ok := rule["max_age_years"].(float64); ok && age >= high {
			continue
		}
		id, _ := rule["id"].(string)
		return id
	}
	return ""
}

func TestTheEscalationChainIsPublishedInOrder(t *testing.T) {
	h := newAPI(t, alertPermissions...)
	h.role = "PHYSICIAN"

	_, body := h.call(t, http.MethodGet, "/v1/alerts/escalation", nil)
	steps, _ := body["steps"].([]any)
	if len(steps) < 2 {
		t.Fatalf("the chain has %d steps", len(steps))
	}
	first, _ := steps[0].(map[string]any)
	if first["after_seconds"] != 0.0 || first["notify_role"] == "" {
		t.Errorf("the first step is %v", first)
	}
	last, _ := steps[len(steps)-1].(map[string]any)
	if _, named := last["notify_role"]; named {
		t.Errorf("the last step notifies %v, which is not an end", last["notify_role"])
	}
	previous := -1.0
	for _, item := range steps {
		step, _ := item.(map[string]any)
		after, _ := step["after_seconds"].(float64)
		if after <= previous {
			t.Errorf("step %v does not wait longer than the one before it", step["step"])
		}
		previous = after
	}
}

func TestEveryThresholdIsProposedUntilTheClinicalLeadApprovesIt(t *testing.T) {
	// D-27 is open and blocking. A screen that presented these as settled would overstate
	// what anybody has agreed to, so the API says so on every row and this keeps it honest.
	h := newAPI(t, alertPermissions...)
	h.role = "PHYSICIAN"

	_, body := h.call(t, http.MethodGet, "/v1/alerts/rules", nil)
	rules, _ := body["rules"].([]any)
	for _, item := range rules {
		rule, _ := item.(map[string]any)
		if rule["approved"] != false {
			t.Errorf("%v is reported as approved before D-27", rule["code"])
		}
	}
}

func TestTheStandingInvariantsHoldForTheSeededThresholds(t *testing.T) {
	// The four CP50 invariants, run against the migrated database rather than trusted. They
	// also run after every migration; this is the copy that fails in a unit test run, where
	// somebody editing a threshold will see it.
	h := newAPI(t, alertPermissions...)
	for _, fn := range []string{
		"core.assert_critical_thresholds_can_fire",
		"core.assert_critical_sits_outside_normal",
		"core.assert_the_escalation_chain_is_walkable",
		"core.assert_critical_rules_name_live_codes",
	} {
		if _, err := h.SQL.Exec(`SELECT ` + fn + `()`); err != nil {
			t.Errorf("%s: %v", fn, err)
		}
	}
}

var _ = clinical.MinAcknowledgement
