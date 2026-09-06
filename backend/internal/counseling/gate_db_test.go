package counseling_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/AmlanWTK/DTHCMS/backend/internal/counseling"
)

// The counselling gate and its valve (CP57, §5.5, §5.4).
//
// Four acceptance criteria:
//
//	1. queueing to step 9 is impossible with unticked mandatory items, enforced server-side;
//	2. the blocked message names exactly which items are missing;
//	3. override requires elevated permission and a reason, and is audited;
//	4. the physician panel shows completion status with attribution and time per item.
//
// Criterion 1's "server-side" is read here the way CP54 read "cannot be bypassed by any client":
// the tests below refuse a queue entry through a plain `INSERT`, not through the API, because the
// path that matters is the one nobody remembers writing.
//
// Criterion 3 is tested from both ends — that an override without a reason is refused, and that
// one *with* a reason records what was skipped as it stood at that moment. The second half is
// the one that decays quietly: an override whose missing list is recomputed later says the valve
// was used for nothing, and the rate review then has nothing to look at.
//
// Criterion 4 is a screen; what is proven here is that everything it needs — who ticked what,
// when, and what is still outstanding — is readable from one place.

// diabetic gives the seeded patient a coded type 2 diabetes, which is what makes the diabetes
// checklist apply to their visit. Written straight into the read model: this file is about the
// gate, and recording a history is CP53's checkpoint.
func (h *floor) diabetic(t *testing.T) {
	t.Helper()
	if _, err := h.SQL.Exec(`
		INSERT INTO read.history_item (id, facility_id, patient_id, kind,
		                               code_system, code_version, code, status,
		                               recorded_at, recorded_by, event_id, global_seq)
		VALUES (gen_random_uuid(), $1, $2, 'COMORBIDITY', 'ICD10', '2019', 'E11.9', 'ACTIVE',
		        now(), $3, gen_random_uuid(), 900001)`,
		h.facility, h.patient, h.user); err != nil {
		t.Fatal(err)
	}
}

// queueTo puts the patient in a station's queue with a plain INSERT — no handler, no service.
func (h *floor) queueTo(t *testing.T, station string) error {
	t.Helper()
	_, err := h.SQL.Exec(`
		INSERT INTO core.queue_entry (facility_id, visit_id, patient_id, station_code,
		                              position, entered_at, clinic_day)
		VALUES ($1, $2, $3, $4, 9, now(), current_date)`,
		h.facility, h.visit, h.patient, station)
	return err
}

// asked satisfies CP54's allergy gate, which sits in front of this one. Without it every queue
// entry past the history station is refused for a different reason, and this file would be
// testing the wrong gate.
func (h *floor) asked(t *testing.T) {
	t.Helper()
	if _, err := h.SQL.Exec(`
		INSERT INTO read.allergy_assertion (id, facility_id, patient_id, kind, reason,
		                                    asserted_at, asserted_by, event_id, global_seq)
		VALUES (gen_random_uuid(), $1, $2, 'NO_KNOWN_ALLERGY', '', now(), $3,
		        gen_random_uuid(), 900002)`,
		h.facility, h.patient, h.user); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// Criterion 1: the gate holds, and it holds in the database
// ---------------------------------------------------------------------------

func TestAPatientWithUntickedMandatoryItemsCannotReachTheConsultation(t *testing.T) {
	h := newFloor(t)
	h.asked(t)
	h.diabetic(t)
	id := h.open(t)
	for _, code := range []string{"DISEASE_UNDERSTANDING", "COMPLICATIONS", "DIET",
		"EXERCISE", "SELF_CARE"} {
		h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/ticks",
			map[string]any{"item_code": code})
	}

	// Five of seven — the plan's own manual verification, done here as a test.
	err := h.queueTo(t, "STN_CONSULTATION")
	if err == nil {
		t.Fatal("a patient with two mandatory items uncovered reached the consultation")
	}
	if !strings.Contains(err.Error(), "counselling is not finished") {
		t.Fatalf("the refusal reads %q", err)
	}
	// Criterion 2, even at the database: the exception names the items rather than a count.
	for _, code := range []string{"GLUCOMETER", "INSULIN_TECHNIQUE"} {
		if !strings.Contains(err.Error(), code) {
			t.Fatalf("the refusal does not name %s: %q", code, err)
		}
	}
}

func TestTheCounsellingGateCannotBeBypassedByAnyClient(t *testing.T) {
	// Through the API a refusal is a sentence; by any other path it is a trigger. The path that
	// matters is the support script at eleven at night, and it does not call a handler.
	h := newFloor(t)
	h.asked(t)
	h.diabetic(t)
	if err := h.queueTo(t, "STN_CONSULTATION"); err == nil {
		t.Fatal("a plain INSERT put an uncounselled patient in the consultation queue")
	}
}

func TestNeverOpeningTheChecklistDoesNotOpenTheGate(t *testing.T) {
	// The loophole nobody has to find: skip the counselling room and there is no session, so
	// there is nothing "outstanding" in the naive reading. A checklist the record calls for and
	// nobody opened counts as all of its mandatory items missing.
	h := newFloor(t)
	h.asked(t)
	h.diabetic(t)

	status, err := h.store.Gate(context.Background(), h.visit)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Blocked {
		t.Fatal("a patient whose checklist nobody opened is not being held")
	}
	if len(status.Missing) != 7 {
		t.Fatalf("%d items missing, want all seven of a checklist nobody opened",
			len(status.Missing))
	}
	for _, item := range status.Missing {
		if item.SessionID != "" {
			t.Fatalf("item %s names a session that does not exist", item.ItemCode)
		}
	}
}

func TestAPatientWithNothingCodedIsNotHeld(t *testing.T) {
	// The gate is not a tax on every visit. A patient whose record calls for no checklist has
	// nothing outstanding, and holding them would be a checkpoint refusing people it has no
	// question for.
	h := newFloor(t)
	h.asked(t)
	if err := h.queueTo(t, "STN_CONSULTATION"); err != nil {
		t.Fatalf("a patient with no checklist was held: %v", err)
	}
}

func TestTheGateStandsOnlyInFrontOfTheConsultation(t *testing.T) {
	// Gating the counselling rooms themselves would be a checkpoint refusing the patient at the
	// door of the room where it would be satisfied.
	h := newFloor(t)
	h.asked(t)
	h.diabetic(t)
	if err := h.queueTo(t, "STN_COUNSELING"); err != nil {
		t.Fatalf("the counselling room itself was gated: %v", err)
	}
}

func TestCoveringEverythingOpensTheGate(t *testing.T) {
	h := newFloor(t)
	h.asked(t)
	h.diabetic(t)
	id := h.open(t)
	for _, item := range h.session(t, id).Items {
		h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/ticks",
			map[string]any{"item_code": item.ItemCode})
	}
	if err := h.queueTo(t, "STN_CONSULTATION"); err != nil {
		t.Fatalf("a fully counselled patient was held: %v", err)
	}
}

func TestTheGateInvariantNoticesTheTriggerGoingMissing(t *testing.T) {
	// A migration that dropped the trigger and kept the function would leave a clinic with no
	// gate **and no error** — the worst available outcome, because everybody would still
	// believe there was one.
	h := newFloor(t)
	if _, err := h.SQL.Exec(`SELECT core.assert_the_counseling_gate_is_wired()`); err != nil {
		t.Fatalf("the invariant fails on a healthy database: %v", err)
	}
	if _, err := h.SQL.Exec(`
		DROP TRIGGER counseling_gate_guards_the_consultation ON core.queue_entry`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`SELECT core.assert_the_counseling_gate_is_wired()`); err == nil {
		t.Fatal("the invariant passed with the gate unwired")
	}
}

// ---------------------------------------------------------------------------
// Criterion 2: the message names the items
// ---------------------------------------------------------------------------

func TestTheGateNamesEveryMissingItemInBothLanguages(t *testing.T) {
	h := newFloor(t)
	h.asked(t)
	h.diabetic(t)
	id := h.open(t)
	h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/ticks",
		map[string]any{"item_code": "DIET"})

	resp, body := h.call(t, "GET", "/v1/counseling/visits/"+h.visit.String()+"/gate", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reading the gate answered %d: %v", resp.StatusCode, body)
	}
	gate, _ := body["gate"].(map[string]any)
	if gate["blocked"] != true {
		t.Fatalf("the gate is not holding this patient: %v", gate)
	}
	missing, _ := gate["missing"].([]any)
	if len(missing) != 6 {
		t.Fatalf("%d items named, want the six nobody covered", len(missing))
	}
	first, _ := missing[0].(map[string]any)
	for _, field := range []string{"item_code", "room", "text_en", "text_bn", "template_code"} {
		if value, _ := first[field].(string); strings.TrimSpace(value) == "" {
			t.Fatalf("a missing item does not carry %s: %v", field, first)
		}
	}
	if value, _ := first["session_id"].(string); value == "" {
		t.Fatal("an item from an open session does not name it, so nothing can send the " +
			"counsellor back to the right place")
	}
}

// ---------------------------------------------------------------------------
// Criterion 3: the valve
// ---------------------------------------------------------------------------

func TestAnOverrideWithoutAReasonIsRefused(t *testing.T) {
	h := newFloor(t)
	h.asked(t)
	h.diabetic(t)
	h.held = append(h.held, counseling.PermOverride)

	resp, body := h.call(t, "POST", "/v1/counseling/visits/"+h.visit.String()+"/gate/override",
		map[string]any{"reason": "   "})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("an override with no reason answered %d, want 422: %v", resp.StatusCode, body)
	}
}

func TestAnOverrideNeedsItsOwnPermission(t *testing.T) {
	// Not something anybody who works at a station holds. The valve is acceptable because it is
	// answerable, and it is answerable because a named few can open it.
	h := newFloor(t)
	h.asked(t)
	h.diabetic(t)
	resp, _ := h.call(t, "POST", "/v1/counseling/visits/"+h.visit.String()+"/gate/override",
		map[string]any{"reason": "The patient's ride is leaving."})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a counsellor overrode the gate: %d", resp.StatusCode)
	}
}

func TestAnOverrideOpensTheGateAndSaysWhatWasSkipped(t *testing.T) {
	h := newFloor(t)
	h.asked(t)
	h.diabetic(t)
	h.held = append(h.held, counseling.PermOverride)

	resp, body := h.call(t, "POST", "/v1/counseling/visits/"+h.visit.String()+"/gate/override",
		map[string]any{"reason": "Patient's ride is leaving; booked for Thursday's group session."})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the override answered %d: %v", resp.StatusCode, body)
	}
	gate, _ := body["gate"].(map[string]any)
	if gate["blocked"] != false || gate["overridden"] != true {
		t.Fatalf("the gate reads %v after an override", gate)
	}

	// The missing list stays on the row as it stood. Items covered afterwards must not make the
	// record say the valve was used for nothing.
	var skipped, reason string
	if err := h.SQL.QueryRow(`
		SELECT array_to_string(missing_at_grant, ','), reason
		  FROM read.counseling_gate_override WHERE visit_id = $1`, h.visit).
		Scan(&skipped, &reason); err != nil {
		t.Fatal(err)
	}
	if len(strings.Split(skipped, ",")) != 7 {
		t.Fatalf("the override recorded %q, want the seven skipped items", skipped)
	}
	if !strings.HasPrefix(reason, "Patient's ride") {
		t.Fatalf("the reason reads %q", reason)
	}

	// And the queue lets the patient through — the trigger reads the same override.
	if err := h.queueTo(t, "STN_CONSULTATION"); err != nil {
		t.Fatalf("the gate still held after an override: %v", err)
	}
}

func TestAnOverrideOnAVisitNothingIsHoldingIsRefused(t *testing.T) {
	// A row recorded where the gate was not holding makes the rate view lie about how often
	// this happens — and the rate view is the only thing that keeps the valve honest.
	h := newFloor(t)
	h.asked(t)
	h.held = append(h.held, counseling.PermOverride)
	resp, body := h.call(t, "POST", "/v1/counseling/visits/"+h.visit.String()+"/gate/override",
		map[string]any{"reason": "Just in case."})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("an override with nothing outstanding answered %d, want 422: %v",
			resp.StatusCode, body)
	}
}

func TestASecondOverrideIsRefusedRatherThanStacked(t *testing.T) {
	h := newFloor(t)
	h.asked(t)
	h.diabetic(t)
	h.held = append(h.held, counseling.PermOverride)
	h.call(t, "POST", "/v1/counseling/visits/"+h.visit.String()+"/gate/override",
		map[string]any{"reason": "Interpreter did not come."})

	resp, _ := h.call(t, "POST", "/v1/counseling/visits/"+h.visit.String()+"/gate/override",
		map[string]any{"reason": "Interpreter did not come."})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("a second override answered %d, want 409", resp.StatusCode)
	}
}

func TestTheOverrideRateIsReadable(t *testing.T) {
	// The plan's mitigation for clinic-floor friction is the valve *plus* rate monitoring, and
	// monitoring nobody can read is a plan on paper.
	h := newFloor(t)
	h.asked(t)
	h.diabetic(t)
	h.held = append(h.held, counseling.PermOverride, counseling.PermQAReview)
	h.call(t, "POST", "/v1/counseling/visits/"+h.visit.String()+"/gate/override",
		map[string]any{"reason": "Insulin corner closed for the afternoon."})

	resp, body := h.call(t, "GET", "/v1/counseling/gate/overrides", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the rate view answered %d: %v", resp.StatusCode, body)
	}
	overrides, _ := body["overrides"].([]any)
	if len(overrides) != 1 {
		var stored, facility string
		_ = h.SQL.QueryRow(`SELECT granted_at::text, facility_id::text
			FROM read.counseling_gate_override LIMIT 1`).Scan(&stored, &facility)
		t.Fatalf("%d overrides listed, want the one granted today (window %v..%v; stored %s in %s, harness facility %s)",
			len(overrides), body["from"], body["to"], stored, facility, h.facility)
	}
	one, _ := overrides[0].(map[string]any)
	if one["reason"] != "Insulin corner closed for the afternoon." {
		t.Fatalf("the listed override reads %v", one["reason"])
	}
	if one["granted_by"] == nil {
		t.Fatal("the listed override names nobody")
	}
}

func TestTheInvariantNoticesAnOverrideWithNoReason(t *testing.T) {
	h := newFloor(t)
	h.asked(t)
	h.diabetic(t)
	h.held = append(h.held, counseling.PermOverride)
	h.call(t, "POST", "/v1/counseling/visits/"+h.visit.String()+"/gate/override",
		map[string]any{"reason": "The consultant is leaving for the day."})

	if _, err := h.SQL.Exec(`SELECT core.assert_every_override_says_why()`); err != nil {
		t.Fatalf("the invariant fails on an honest override: %v", err)
	}
	if _, err := h.SQL.Exec(`
		ALTER TABLE read.counseling_gate_override
		 DROP CONSTRAINT counseling_gate_override_reason_check`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`UPDATE read.counseling_gate_override SET reason = ''`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`SELECT core.assert_every_override_says_why()`); err == nil {
		t.Fatal("the invariant passed with an override that gives no reason")
	}
}

// ---------------------------------------------------------------------------
// Criterion 4: what the physician's panel reads
// ---------------------------------------------------------------------------

func TestThePanelCanSeeWhoCoveredWhatAndWhen(t *testing.T) {
	// §5.4's method is spot-questioning: the physician asks the patient what they were told
	// about injection sites and then looks at who told them. Everything that answer needs comes
	// from one read.
	h := newFloor(t)
	h.asked(t)
	h.diabetic(t)
	id := h.open(t)
	h.call(t, "POST", "/v1/counseling/sessions/"+id.String()+"/ticks",
		map[string]any{"item_code": "INSULIN_TECHNIQUE", "note": "Rotates on the same arm."})

	resp, body := h.call(t, "GET", "/v1/counseling/sessions/"+id.String(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reading the session answered %d: %v", resp.StatusCode, body)
	}
	session, _ := body["session"].(map[string]any)
	ticks, _ := session["ticks"].([]any)
	if len(ticks) != 1 {
		t.Fatalf("%d ticks on the panel's read", len(ticks))
	}
	tick, _ := ticks[0].(map[string]any)
	for _, field := range []string{"ticked_by", "ticked_at", "ticked_role", "note"} {
		if value, present := tick[field]; !present || value == "" {
			t.Fatalf("the panel cannot see %s: %v", field, tick)
		}
	}
	outstanding, _ := session["outstanding"].([]any)
	if len(outstanding) != 6 {
		t.Fatalf("%d items outstanding on the panel's read, want six", len(outstanding))
	}
}

func TestThePanelSeesTheOverrideRatherThanAnEmptyGate(t *testing.T) {
	// A screen that drew "overridden" and "nothing was missing" the same way would be telling a
	// physician the counselling was done.
	h := newFloor(t)
	h.asked(t)
	h.diabetic(t)
	h.held = append(h.held, counseling.PermOverride)
	h.call(t, "POST", "/v1/counseling/visits/"+h.visit.String()+"/gate/override",
		map[string]any{"reason": "Patient unwell; rebooked."})

	_, body := h.call(t, "GET", "/v1/counseling/visits/"+h.visit.String()+"/gate", nil)
	gate, _ := body["gate"].(map[string]any)
	if gate["overridden"] != true {
		t.Fatal("the panel cannot tell an override from a finished checklist")
	}
	override, _ := gate["override"].(map[string]any)
	if override == nil || override["reason"] == "" {
		t.Fatalf("the override carries no reason to the panel: %v", gate)
	}
	missing, _ := gate["missing"].([]any)
	if len(missing) == 0 {
		t.Fatal("the panel cannot see what was skipped")
	}
}

func TestAnOverrideIsAudited(t *testing.T) {
	// Criterion 3's last clause. In the security trail beside role grants and break-glass —
	// the log somebody reads when asking "who decided this" — and the entry carries the reason
	// and a count rather than the clinical detail, which belongs on the physician's panel.
	h := newFloor(t)
	h.asked(t)
	h.diabetic(t)
	h.held = append(h.held, counseling.PermOverride)
	h.call(t, "POST", "/v1/counseling/visits/"+h.visit.String()+"/gate/override",
		map[string]any{"reason": "Ambulance transfer; counselling booked for the follow-up."})

	if len(h.audits.overridden) != 1 {
		t.Fatalf("%d audit entries for an override, want one", len(h.audits.overridden))
	}
	entry := h.audits.overridden[0]
	if entry.Missing != 7 || entry.VisitID != h.visit || entry.ActorID != h.user {
		t.Fatalf("the audit entry reads %+v", entry)
	}
	if !strings.HasPrefix(entry.Reason, "Ambulance transfer") {
		t.Fatalf("the audited reason is %q", entry.Reason)
	}
}
