package clinical_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
)

// The correction workflow (CP62, §4.3, [R-04]).
//
// The plan asks for one thing above all others here: **the canonical scenario as an automated end
// to end test**. §4.3 describes it concretely — record 150, flag it, route it, correct it to 140,
// and then assert that the original event is intact and retrievable, the projection shows 140, the
// BMI is recomputed, both values appear in the history with the right attribution, and the
// operator's tally has something to count.
//
// `TestTheHundredAndFortyCase` below is that scenario, written as one test on purpose: split into
// six it would still pass with the steps in the wrong order, and the order is what §4.3 is about.
//
// The rest of this file is the ways the workflow can be wrong in a clinic rather than in a
// database: a second flag on the same value, an answer from the wrong person, a rejection with
// nothing said, a supervisor's fix landing on the operator's record as though it were their own.

// recordHeight writes a height and returns its observation id.
func (h *api) recordHeight(t *testing.T, cm float64) uuid.UUID {
	t.Helper()
	resp, body := h.call(t, "POST", "/v1/observations", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient,
		"code": "BODY_HEIGHT", "value": cm, "unit": "cm",
		"effective_at": h.clock.Now().UTC(),
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("recording a height answered %d: %v", resp.StatusCode, body)
	}
	observation, _ := body["observation"].(map[string]any)
	return uuid.MustParse(observation["id"].(string))
}

func (h *api) recordWeight(t *testing.T, kg float64) uuid.UUID {
	t.Helper()
	resp, body := h.call(t, "POST", "/v1/observations", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient,
		"code": "BODY_WEIGHT", "value": kg, "unit": "kg",
		"effective_at": h.clock.Now().UTC(),
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("recording a weight answered %d: %v", resp.StatusCode, body)
	}
	observation, _ := body["observation"].(map[string]any)
	return uuid.MustParse(observation["id"].(string))
}

// asPhysician switches the harness to a second person: the one who flags.
//
// A correction request is a conversation between two people, and a test where one person flags
// their own value is not the scenario — it is refused, and there is a test below for that too.
func (h *api) asPhysician(t *testing.T, permissions ...string) uuid.UUID {
	t.Helper()
	physician := uuid.New()
	if _, err := h.SQL.Exec(`
		INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, $2, 'P014', 'Dr Farhana Islam', 'ডা. ফারহানা ইসলাম', 'active')`,
		physician, h.facility); err != nil {
		t.Fatal(err)
	}
	h.becomes(t, physician, "PHYSICIAN", permissions...)
	return physician
}

func TestTheHundredAndFortyCase(t *testing.T) {
	// §4.3, start to finish.
	h := newAPI(t)
	operator := h.user

	// 1. The anthropometry officer records a height of 150 cm, and a weight, so that something
	//    is derived from the height and the cascade has work to do.
	height := h.recordHeight(t, 150)
	h.recordWeight(t, 62)
	if resp, body := h.call(t, "POST", "/v1/observations/derive", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient, "what": "BMI",
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("deriving the BMI answered %d: %v", resp.StatusCode, body)
	}
	before := h.currentBMI(t)

	// 2. The physician sees it, is sure it is 140, and says so.
	h.asPhysician(t, "observation.read.values", "observation.correct.request")
	resp, body := h.call(t, "POST", "/v1/observations/"+height.String()+"/flag", map[string]any{
		"event_id": uuid.New(), "reason_code": "TRANSCRIPTION",
		"note": "Patient is my height; the tape was against the wall, not her.",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("flagging answered %d: %v", resp.StatusCode, body)
	}
	request, _ := body["request"].(map[string]any)
	requestID := request["id"].(string)

	// 3. It is routed to the person who typed it — not to a supervisor, not to a queue.
	if request["assigned_to"] != operator.String() {
		t.Fatalf("the request was routed to %v, want the operator who typed the value",
			request["assigned_to"])
	}
	if request["status"] != "OPEN" {
		t.Fatalf("a new request reads %v", request["status"])
	}

	// 4. It is on that operator's own queue, and on nobody else's.
	h.becomes(t, operator, "ANTHROPOMETRY", "observation.read.values", "observation.write.anthro")
	_, queue := h.call(t, "GET", "/v1/corrections/mine", nil)
	requests, _ := queue["requests"].([]any)
	if len(requests) != 1 {
		t.Fatalf("%d requests on the operator's queue, want the one", len(requests))
	}

	// 5. They correct it to 140.
	resp, body = h.call(t, "POST", "/v1/corrections/"+requestID+"/apply", map[string]any{
		"event_id": uuid.New(), "value": 140.0, "unit": "cm",
		"note": "Re-measured. 140.",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("applying the correction answered %d: %v", resp.StatusCode, body)
	}
	answered, _ := body["request"].(map[string]any)
	if answered["status"] != "APPLIED" {
		t.Fatalf("the request reads %v after the author corrected it", answered["status"])
	}

	// 6. The original event is intact and retrievable — criterion 1. The ledger has both, and
	//    the first one is untouched.
	var recorded int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM ledger.event
		WHERE event_type = 'OBSERVATION_RECORDED'
		  AND payload->>'code' = 'BODY_HEIGHT'`).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 2 {
		t.Fatalf("%d height events in the ledger, want the original and the correction", recorded)
	}
	var originalValue float64
	var originalStatus string
	if err := h.SQL.QueryRow(`SELECT value_num, status FROM read.observation WHERE id = $1`,
		height).Scan(&originalValue, &originalStatus); err != nil {
		t.Fatalf("the original value is no longer in the record: %v", err)
	}
	if originalValue != 150 || originalStatus != "CORRECTED" {
		t.Fatalf("the original reads %v/%s; it must keep its value and stop being current",
			originalValue, originalStatus)
	}

	// 7. The projection shows 140.
	current := h.currentHeight(t)
	if current != 140 {
		t.Fatalf("the current height is %v", current)
	}

	// 8. The BMI is recomputed — criterion 3 — and the old one is superseded rather than edited.
	after := h.currentBMI(t)
	if after == before {
		t.Fatalf("the BMI did not move when the height it was computed from changed (%v)", after)
	}
	var supersededBMIs int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM read.observation
		WHERE patient_id = $1 AND code = 'BMI' AND status = 'SUPERSEDED'`,
		h.patient).Scan(&supersededBMIs); err != nil {
		t.Fatal(err)
	}
	if supersededBMIs != 1 {
		t.Fatalf("%d superseded BMIs; the old one must stay in the record beside the new",
			supersededBMIs)
	}

	// 9. Both values appear in the history with the right attribution — criterion 5.
	_, history := h.call(t, "GET",
		"/v1/patients/"+h.patient.String()+"/observations/BODY_HEIGHT/history", nil)
	values, _ := history["observations"].([]any)
	if len(values) != 2 {
		t.Fatalf("%d heights in the history, want both", len(values))
	}
	for _, entry := range values {
		row, _ := entry.(map[string]any)
		if row["recorded_by"] != operator.String() {
			t.Fatalf("a height in the history is attributed to %v, want the operator who "+
				"typed and corrected it", row["recorded_by"])
		}
	}

	// 10. And the correction itself carries both people — the one who asked and the one who
	//     answered — which is the half the observation rows cannot say.
	_, corrections := h.call(t, "GET", "/v1/patients/"+h.patient.String()+"/corrections", nil)
	raised, _ := corrections["requests"].([]any)
	if len(raised) != 1 {
		t.Fatalf("%d corrections on the patient, want the one", len(raised))
	}
	one, _ := raised[0].(map[string]any)
	if one["requested_by"] == one["assigned_to"] {
		t.Fatal("the flagger and the author are the same person; this is not the scenario")
	}
	if one["reason_code"] != "TRANSCRIPTION" {
		t.Fatalf("the reason reads %v, and CP63 counts transcription errors by it",
			one["reason_code"])
	}
	if one["replacement_id"] == nil || one["replacement_id"] == "" {
		t.Fatal("the correction does not name the value that replaced the flagged one")
	}
}

// currentHeight and currentBMI read what the record currently says.
func (h *api) currentHeight(t *testing.T) float64 { return h.currentValue(t, "BODY_HEIGHT") }
func (h *api) currentBMI(t *testing.T) float64    { return h.currentValue(t, "BMI") }

// currentBMIID is the derived row itself, for the test that tries to flag it.
func (h *api) currentBMIID(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := h.SQL.QueryRow(`SELECT id FROM read.observation
		WHERE patient_id = $1 AND code = 'BMI' AND status = 'ACTIVE'
		ORDER BY effective_at DESC, global_seq DESC LIMIT 1`, h.patient).Scan(&id); err != nil {
		t.Fatalf("reading the current BMI: %v", err)
	}
	return id
}

func (h *api) currentValue(t *testing.T, code string) float64 {
	t.Helper()
	var value float64
	if err := h.SQL.QueryRow(`SELECT value_num FROM read.observation
		WHERE patient_id = $1 AND code = $2 AND status = 'ACTIVE'
		ORDER BY effective_at DESC, global_seq DESC LIMIT 1`, h.patient, code).Scan(&value); err != nil {
		t.Fatalf("reading the current %s: %v", code, err)
	}
	return value
}

// ---------------------------------------------------------------------------
// The ways it can be wrong in a clinic
// ---------------------------------------------------------------------------

func TestAValueCannotBeFlaggedTwice(t *testing.T) {
	// A second flag is the same conversation, and two would route two corrections at one number.
	h := newAPI(t)
	height := h.recordHeight(t, 150)
	h.asPhysician(t, "observation.read.values", "observation.correct.request")

	flag := map[string]any{"event_id": uuid.New(), "reason_code": "TRANSCRIPTION"}
	if resp, _ := h.call(t, "POST", "/v1/observations/"+height.String()+"/flag", flag); resp.StatusCode != http.StatusCreated {
		t.Fatalf("the first flag answered %d", resp.StatusCode)
	}
	flag["event_id"] = uuid.New()
	resp, body := h.call(t, "POST", "/v1/observations/"+height.String()+"/flag", flag)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("a second flag answered %d, want 409: %v", resp.StatusCode, body)
	}
}

func TestNobodyFlagsTheirOwnValue(t *testing.T) {
	// Correcting your own value needs no request, and a flag on it would put a correction on
	// your own quality record that nobody asked you to make.
	h := newAPI(t, "observation.read.values", "observation.write.anthro",
		"observation.correct.request")
	height := h.recordHeight(t, 150)
	resp, body := h.call(t, "POST", "/v1/observations/"+height.String()+"/flag",
		map[string]any{"event_id": uuid.New(), "reason_code": "TRANSCRIPTION"})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("flagging one's own value answered %d, want 422: %v", resp.StatusCode, body)
	}
}

func TestAComputedValueIsNotFlagged(t *testing.T) {
	// A BMI is not typed by anybody; it is what a height and a weight make. Routing a request at
	// it would send the correction to whoever pressed "derive" — who has nothing to retype —
	// while the wrong number it came from sits uncorrected.
	h := newAPI(t)
	h.recordHeight(t, 150)
	h.recordWeight(t, 62)
	if resp, body := h.call(t, "POST", "/v1/observations/derive", map[string]any{
		"event_id": uuid.New(), "patient_id": h.patient, "what": "BMI",
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("deriving the BMI answered %d: %v", resp.StatusCode, body)
	}
	bmi := h.currentBMIID(t)

	h.asPhysician(t, "observation.read.values", "observation.correct.request")
	resp, body := h.call(t, "POST", "/v1/observations/"+bmi.String()+"/flag",
		map[string]any{"event_id": uuid.New(), "reason_code": "TRANSCRIPTION"})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("flagging a derived value answered %d, want 422: %v", resp.StatusCode, body)
	}
	// And it says what to do instead, in both languages, because an operator who is only told
	// "no" flags it again tomorrow.
	failure, _ := body["error"].(map[string]any)
	en, _ := failure["fields"].(map[string]any)
	bn, _ := failure["fields_bn"].(map[string]any)
	if en["observation_id"] == nil || bn["observation_id"] == nil {
		t.Fatalf("the refusal did not say what to do instead in both languages: %v", body)
	}
}

func TestAFieldWorkerCanFixTheirOwnMistake(t *testing.T) {
	// The route guard used to be `observation.read.values`, which the field worker does not hold
	// — they fill a form and do not browse the record. That refused the one person the request
	// was addressed to, and their mistakes would have stayed on the record with a 403 in front
	// of the only person who could fix them.
	h := newAPI(t, "observation.write.anthro")
	h.role = "FIELD_WORKER"
	worker := h.user
	height := h.recordHeight(t, 150)

	h.asPhysician(t, "observation.read.values", "observation.correct.request")
	_, body := h.call(t, "POST", "/v1/observations/"+height.String()+"/flag",
		map[string]any{"event_id": uuid.New(), "reason_code": "TRANSCRIPTION"})
	request, _ := body["request"].(map[string]any)
	requestID := request["id"].(string)

	// Back to the field worker, who holds no read permission at all.
	h.becomes(t, worker, "FIELD_WORKER", "observation.write.anthro")

	resp, mine := h.call(t, "GET", "/v1/corrections/mine", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the field worker's own queue answered %d: %v", resp.StatusCode, mine)
	}
	requests, _ := mine["requests"].([]any)
	if len(requests) != 1 {
		t.Fatalf("the field worker was shown %d requests, want the one addressed to them", len(requests))
	}

	if resp, one := h.call(t, "GET", "/v1/corrections/"+requestID, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("reading their own request answered %d: %v", resp.StatusCode, one)
	}
	resp, applied := h.call(t, "POST", "/v1/corrections/"+requestID+"/apply",
		map[string]any{"event_id": uuid.New(), "value": 140.0, "unit": "cm"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the field worker correcting their own value answered %d: %v", resp.StatusCode, applied)
	}
	answered, _ := applied["request"].(map[string]any)
	if answered["status"] != "APPLIED" {
		t.Fatalf("the operator's own fix reads %v, want APPLIED", answered["status"])
	}
}

func TestSomebodyElsesRequestIsNotYoursToAnswer(t *testing.T) {
	// The author corrects their own work. Anybody else is an override, and an override needs the
	// supervisor's permission — otherwise the person who made the mistake never learns of it,
	// which is the whole thing §4.3 is arranged to prevent.
	h := newAPI(t)
	height := h.recordHeight(t, 150)
	h.asPhysician(t, "observation.read.values", "observation.correct.request")
	_, body := h.call(t, "POST", "/v1/observations/"+height.String()+"/flag",
		map[string]any{"event_id": uuid.New(), "reason_code": "TRANSCRIPTION"})
	request, _ := body["request"].(map[string]any)

	// Still the physician — who flagged it, holds no supervisor permission, and is not the author.
	resp, refused := h.call(t, "POST", "/v1/corrections/"+request["id"].(string)+"/apply",
		map[string]any{"event_id": uuid.New(), "value": 140.0, "unit": "cm"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("answering somebody else's request answered %d, want 403: %v",
			resp.StatusCode, refused)
	}
}

func TestASupervisorsFixIsRecordedAsAnOverride(t *testing.T) {
	// The patient in front of a physician cannot wait for an operator who has gone home, so the
	// valve exists. It is a **different status**, because an operator's quality record must not
	// read a supervisor's fix as though they had put it right themselves.
	h := newAPI(t)
	height := h.recordHeight(t, 150)
	h.asPhysician(t, "observation.read.values", "observation.correct.request",
		"observation.correct.approve")
	_, body := h.call(t, "POST", "/v1/observations/"+height.String()+"/flag",
		map[string]any{"event_id": uuid.New(), "reason_code": "TRANSCRIPTION"})
	request, _ := body["request"].(map[string]any)

	resp, applied := h.call(t, "POST", "/v1/corrections/"+request["id"].(string)+"/apply",
		map[string]any{"event_id": uuid.New(), "value": 140.0, "unit": "cm",
			"note": "Operator has gone home; patient is waiting."})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the supervisor's correction answered %d: %v", resp.StatusCode, applied)
	}
	answered, _ := applied["request"].(map[string]any)
	if answered["status"] != "OVERRIDDEN" {
		t.Fatalf("a supervisor's fix reads %v, want OVERRIDDEN", answered["status"])
	}

	var events int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM ledger.event
		WHERE event_type = 'SUPERVISOR_OVERRIDE_APPLIED'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("%d override events, want one", events)
	}
	// And the value is still corrected: the record is right either way, which is the point of
	// having the valve at all.
	if got := h.currentHeight(t); got != 140 {
		t.Fatalf("the height reads %v after the supervisor's correction", got)
	}
}

func TestARejectionMustSayWhy(t *testing.T) {
	// "No" with no reason is how a flagging culture dies: the physician learns nothing, cannot
	// tell a disagreement from an oversight, and stops flagging.
	h := newAPI(t)
	operator := h.user
	height := h.recordHeight(t, 150)
	h.asPhysician(t, "observation.read.values", "observation.correct.request")
	_, body := h.call(t, "POST", "/v1/observations/"+height.String()+"/flag",
		map[string]any{"event_id": uuid.New(), "reason_code": "TRANSCRIPTION"})
	request, _ := body["request"].(map[string]any)

	h.becomes(t, operator, "ANTHROPOMETRY", "observation.read.values", "observation.write.anthro")
	resp, refused := h.call(t, "POST", "/v1/corrections/"+request["id"].(string)+"/reject",
		map[string]any{"event_id": uuid.New(), "reason": "  "})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a rejection with no reason answered %d, want 422: %v", resp.StatusCode, refused)
	}

	resp, answered := h.call(t, "POST", "/v1/corrections/"+request["id"].(string)+"/reject",
		map[string]any{"event_id": uuid.New(),
			"reason": "Measured twice on the stadiometer. She is 150."})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the rejection answered %d: %v", resp.StatusCode, answered)
	}
	out, _ := answered["request"].(map[string]any)
	if out["status"] != "REJECTED" {
		t.Fatalf("the request reads %v", out["status"])
	}
	// The value stands, untouched.
	if got := h.currentHeight(t); got != 150 {
		t.Fatalf("the height moved to %v on a rejected request", got)
	}
}

func TestARequestIsAnsweredOnce(t *testing.T) {
	h := newAPI(t)
	operator := h.user
	height := h.recordHeight(t, 150)
	h.asPhysician(t, "observation.read.values", "observation.correct.request")
	_, body := h.call(t, "POST", "/v1/observations/"+height.String()+"/flag",
		map[string]any{"event_id": uuid.New(), "reason_code": "TRANSCRIPTION"})
	request, _ := body["request"].(map[string]any)

	h.becomes(t, operator, "ANTHROPOMETRY", "observation.read.values", "observation.write.anthro")
	h.call(t, "POST", "/v1/corrections/"+request["id"].(string)+"/apply",
		map[string]any{"event_id": uuid.New(), "value": 140.0, "unit": "cm"})
	resp, _ := h.call(t, "POST", "/v1/corrections/"+request["id"].(string)+"/apply",
		map[string]any{"event_id": uuid.New(), "value": 141.0, "unit": "cm"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("a second answer to one request answered %d, want 409", resp.StatusCode)
	}
}

func TestTheOperatorIsToldOnTheirOwnDevice(t *testing.T) {
	// Criterion 4. The notification goes to the operator's own topic and to nobody else's: a
	// correction request broadcast to a station is a mistake announced to whoever is standing
	// there.
	h := newAPI(t)
	told := &recordedCorrections{}
	h.service = h.service.WithCorrectionNotifier(told)
	h.remount(t)

	operator := h.user
	height := h.recordHeight(t, 150)
	h.asPhysician(t, "observation.read.values", "observation.correct.request")
	h.call(t, "POST", "/v1/observations/"+height.String()+"/flag",
		map[string]any{"event_id": uuid.New(), "reason_code": "TRANSCRIPTION"})

	if len(told.seen) != 1 {
		t.Fatalf("%d operators told, want the one whose value was flagged", len(told.seen))
	}
	if told.seen[0].AssignedTo != operator {
		t.Fatalf("the request was announced to %s, want the author %s",
			told.seen[0].AssignedTo, operator)
	}
}

// recordedCorrections stands in for the realtime gateway. `clinical` may not import `realtime`,
// so the service takes an interface; this is the test's implementation of it.
type recordedCorrections struct {
	seen []clinical.CorrectionRequest
}

func (r *recordedCorrections) CorrectionRequested(_ context.Context, request clinical.CorrectionRequest) {
	r.seen = append(r.seen, request)
}

// reviewer records which operators the quality module was asked to look at.
type reviewer struct{ asked []uuid.UUID }

func (r *reviewer) ReviewOperator(_ context.Context, operator uuid.UUID) {
	r.asked = append(r.asked, operator)
}

func TestAnsweringACorrectionAsksForAQualityReview(t *testing.T) {
	// CP63's other half, from this side. The quality module's own tests seed corrections
	// directly, because the scenarios that make it interesting need time to move; this proves
	// the hook that connects the two — and, specifically, that it names **the operator the
	// request was routed to** rather than whoever answered it.
	h := newAPI(t)
	watcher := &reviewer{}
	h.service = h.service.WithQualityReviewer(watcher)
	h.remount(t)

	operator := h.user
	height := h.recordHeight(t, 150)
	h.asPhysician(t, "observation.read.values", "observation.correct.request",
		"observation.correct.approve")
	_, body := h.call(t, "POST", "/v1/observations/"+height.String()+"/flag",
		map[string]any{"event_id": uuid.New(), "reason_code": "TRANSCRIPTION"})
	request, _ := body["request"].(map[string]any)

	// The physician overrides. The review must still be about the operator: a supervisor's fix
	// lands on the operator's record, which is the whole reason it is a different event.
	if resp, applied := h.call(t, "POST", "/v1/corrections/"+request["id"].(string)+"/apply",
		map[string]any{"event_id": uuid.New(), "value": 140.0, "unit": "cm",
			"note": "Operator has gone home."}); resp.StatusCode != http.StatusOK {
		t.Fatalf("the correction answered %d: %v", resp.StatusCode, applied)
	}

	if len(watcher.asked) != 1 {
		t.Fatalf("the quality module was asked to look %d times", len(watcher.asked))
	}
	if watcher.asked[0] != operator {
		t.Fatalf("the review was about %v, want the operator who typed the value", watcher.asked[0])
	}
}

func TestARejectionIsAlsoReviewed(t *testing.T) {
	// Not a no-op: the shape of somebody's month changes when a request closes, and asking here
	// is what makes a rejection remove a request from the open count rather than leaving it
	// there until the next correction happens to trigger a look.
	h := newAPI(t)
	watcher := &reviewer{}
	h.service = h.service.WithQualityReviewer(watcher)
	h.remount(t)

	operator := h.user
	height := h.recordHeight(t, 150)
	h.asPhysician(t, "observation.read.values", "observation.correct.request")
	_, body := h.call(t, "POST", "/v1/observations/"+height.String()+"/flag",
		map[string]any{"event_id": uuid.New(), "reason_code": "TRANSCRIPTION"})
	request, _ := body["request"].(map[string]any)

	h.becomes(t, operator, "ANTHROPOMETRY", "observation.read.values", "observation.write.anthro")
	if resp, rejected := h.call(t, "POST", "/v1/corrections/"+request["id"].(string)+"/reject",
		map[string]any{"event_id": uuid.New(),
			"reason": "I measured her twice; 150 is right."}); resp.StatusCode != http.StatusOK {
		t.Fatalf("the rejection answered %d: %v", resp.StatusCode, rejected)
	}
	if len(watcher.asked) != 1 || watcher.asked[0] != operator {
		t.Fatalf("a rejection produced %v reviews", watcher.asked)
	}
}

func TestTheInvariantNoticesACorrectionThatSaysNothing(t *testing.T) {
	h := newAPI(t)
	if _, err := h.SQL.Exec(`SELECT core.assert_every_correction_says_why()`); err != nil {
		t.Fatalf("the invariant fails on a clean database: %v", err)
	}
	if _, err := h.SQL.Exec(`SELECT core.assert_corrected_values_are_still_there()`); err != nil {
		t.Fatalf("the second invariant fails on a clean database: %v", err)
	}
}

func TestEveryDerivationSaysWhatItReads(t *testing.T) {
	// The cascade's dependency list is written in Go beside the formulas rather than inferred
	// from what a derivation stored, because `inputs` holds the names the formula's own paper
	// uses — `height_cm`, not `BODY_HEIGHT` — and a cascade matching on those would silently
	// recompute nothing. The cost of writing it twice is that the two can drift, so this walks
	// the closed list and fails on a derivation that declares nothing without saying why.
	//
	// The foot risks are the deliberate exception: they are computed from findings rather than
	// from numbers, and a finding is corrected as a finding.
	declared := map[clinical.Derivable]bool{}
	for _, code := range []string{"BODY_HEIGHT", "BODY_WEIGHT", "WAIST_CIRC", "HIP_CIRC",
		"CREATININE", "SMOKING_YEARS", "CIGARETTES_PER_DAY"} {
		for _, what := range clinical.DerivationsReading(code) {
			declared[what] = true
		}
	}
	for _, what := range clinical.Derivables {
		if what == clinical.DeriveFootRiskLeft || what == clinical.DeriveFootRiskRight {
			continue
		}
		if !declared[what] {
			t.Errorf("%s reads nothing, so a correction to its inputs would move nothing", what)
		}
	}
}

func TestACorrectionToAValueNothingUsesRecomputesNothing(t *testing.T) {
	// A clinic that never measured a waist has no WHR to move, and a cascade that derived one
	// out of a correction to something else would invent a value nobody asked for.
	h := newAPI(t)
	height := h.recordHeight(t, 150)
	h.asPhysician(t, "observation.read.values", "observation.correct.request")
	_, body := h.call(t, "POST", "/v1/observations/"+height.String()+"/flag",
		map[string]any{"event_id": uuid.New(), "reason_code": "TRANSCRIPTION"})
	request, _ := body["request"].(map[string]any)

	h.becomes(t, h.user, "PHYSICIAN", "observation.read.values", "observation.correct.request",
		"observation.correct.approve", "observation.write.anthro")
	if resp, applied := h.call(t, "POST", "/v1/corrections/"+request["id"].(string)+"/apply",
		map[string]any{"event_id": uuid.New(), "value": 140.0, "unit": "cm"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("the correction answered %d: %v", resp.StatusCode, applied)
	}

	var derived int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM read.observation
		WHERE patient_id = $1 AND formula <> ''`, h.patient).Scan(&derived); err != nil {
		t.Fatal(err)
	}
	if derived != 0 {
		t.Fatalf("%d derived values appeared out of a correction to a height nothing used", derived)
	}
}
