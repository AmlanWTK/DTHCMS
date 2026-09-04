package clinical_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Impossible inputs, refused at the point of entry (CP46, §3 step 2).
//
// The four criteria, in order:
//
//  1. an absolute-limit violation cannot be saved by any client;
//  2. a soft-limit value takes an explicit confirmation, and the confirmation is recorded;
//  3. the message states the plausible range rather than saying "invalid";
//  4. the rules are data.
//
// The seeded patient is an adult male born 1985, which matters: every assertion below is
// against the rule that actually applies to him, not against a general one.

func (h *api) height(t *testing.T, value float64, extra ...map[string]any) (*http.Response, map[string]any) {
	t.Helper()
	body := map[string]any{"code": "BODY_HEIGHT", "value": value, "unit": "cm"}
	for _, more := range extra {
		for key, val := range more {
			body[key] = val
		}
	}
	return h.record(t, body)
}

func fieldText(t *testing.T, body map[string]any, field string) string {
	t.Helper()
	text, _ := fieldsOf(t, body)[field].(string)
	return text
}

// --- criterion 1 ---

func TestAnImpossibleValueCannotBeSavedByAnyClient(t *testing.T) {
	// 15 cm is not a short adult. It is a tape measure read off the wrong end, or a decimal
	// point in the wrong place, and no confirmation makes it a height.
	h := newAPI(t)

	resp, body := h.height(t, 15)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a height of 15 cm: %d %v", resp.StatusCode, body)
	}

	// And the same value with the confirmation flag set, which is what a client that had
	// been "fixed" to get past the warning would send.
	resp, body = h.height(t, 15, map[string]any{"confirmed": true, "confirmed_reason": "checked"})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a confirmed height of 15 cm was accepted: %d %v", resp.StatusCode, body)
	}
	// No confirmation is offered, because none would help.
	if fieldText(t, body, "confirmed") != "" {
		t.Error("an impossible value offered a confirmation, which would loop the operator")
	}
}

func TestAValueInsideTheBandIsSavedWithoutFuss(t *testing.T) {
	h := newAPI(t)
	resp, body := h.height(t, 172)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("an ordinary height: %d %v", resp.StatusCode, body)
	}
	if observationOf(t, body)["implausible_confirmed"] == true {
		t.Error("an ordinary value was marked as a confirmed override")
	}
}

// --- criterion 2 ---

func TestAnImplausibleButPossibleValueTakesAConfirmation(t *testing.T) {
	// 205 cm is a real person. A system that refused it would be a system that cannot record
	// the tallest patient in Faridpur — and staff who learn that work around the system,
	// which costs more than the typing errors the rule was written to catch.
	h := newAPI(t)

	resp, body := h.height(t, 205)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("205 cm without confirmation: %d %v", resp.StatusCode, body)
	}
	if fieldText(t, body, "confirmed") == "" {
		t.Fatal("the refusal did not tell the operator they could confirm it")
	}

	resp, body = h.height(t, 205, map[string]any{
		"confirmed": true, "confirmed_reason": "measured twice against the wall",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("205 cm confirmed: %d %v", resp.StatusCode, body)
	}
	row := observationOf(t, body)
	if row["implausible_confirmed"] != true {
		t.Error("the confirmation was not recorded on the observation")
	}
	if row["implausible_reason"] != "measured twice against the wall" {
		t.Errorf("the reason was recorded as %v", row["implausible_reason"])
	}
}

func TestEveryConfirmationIsInTheLedger(t *testing.T) {
	// "Recorded as an event so the pattern is auditable." A rule overridden twenty times a
	// week is a rule that is wrong, and the clinic should find that out from its own record
	// rather than from opinion.
	h := newAPI(t)
	h.height(t, 205, map[string]any{"confirmed": true, "confirmed_reason": "against the wall"})

	var confirmed bool
	var reason string
	err := h.SQL.QueryRow(`
		SELECT (payload->>'implausible_confirmed')::boolean, payload->>'implausible_reason'
		  FROM ledger.event
		 WHERE event_type = 'OBSERVATION_RECORDED'
		   AND payload->>'code' = 'BODY_HEIGHT'
		 ORDER BY global_seq DESC LIMIT 1`).Scan(&confirmed, &reason)
	if err != nil {
		t.Fatalf("reading the event back: %v", err)
	}
	if !confirmed || reason != "against the wall" {
		t.Errorf("the ledger says confirmed=%v reason=%q", confirmed, reason)
	}
}

// --- criterion 3 ---

func TestTheMessageStatesTheRangeRatherThanSayingInvalid(t *testing.T) {
	// An operator told their entry is "out of range" and not told *what* range re-types the
	// same number. The plausible edge has to be in the sentence.
	h := newAPI(t)

	_, body := h.height(t, 205)
	message := fieldText(t, body, "value")
	if !strings.Contains(message, "200") {
		t.Errorf("the message does not name the limit: %q", message)
	}

	// And in Bangla, because half the staff work in it.
	envelope, _ := body["error"].(map[string]any)
	fieldsBN, _ := envelope["fields_bn"].(map[string]any)
	bangla, _ := fieldsBN["value"].(string)
	if bangla == "" || !strings.Contains(bangla, "200") {
		t.Errorf("the Bangla message does not name the limit: %q", bangla)
	}
}

func TestTheRuleExplainsItselfWhenItHasSomethingToSay(t *testing.T) {
	// The note is the difference between a rule an operator obeys and a rule they resent.
	h := newAPI(t)
	h.height(t, 172)

	// An adult does not grow 12 cm between visits.
	_, body := h.height(t, 184)
	message := fieldText(t, body, "value")
	if !strings.Contains(message, "adult") {
		t.Errorf("the delta refusal does not explain itself: %q", message)
	}
	if !strings.Contains(message, "172") {
		t.Errorf("the delta refusal does not say what the last value was: %q", message)
	}
}

// --- the delta rules ---

func TestAnAdultWhoGrowsTwelveCentimetresIsATypingError(t *testing.T) {
	h := newAPI(t)
	if resp, body := h.height(t, 172); resp.StatusCode != http.StatusCreated {
		t.Fatalf("the first height: %d %v", resp.StatusCode, body)
	}
	resp, body := h.height(t, 184)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("12 cm of growth in an adult: %d %v", resp.StatusCode, body)
	}
	// Confirmable, not impossible: a patient measured slouching last time is a real story.
	resp, _ = h.height(t, 184, map[string]any{"confirmed": true, "confirmed_reason": "was slouching"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("the confirmed re-measurement: %d", resp.StatusCode)
	}
}

func TestASmallChangeIsNotAChange(t *testing.T) {
	// The rules must not fire on ordinary measurement noise, or staff will confirm
	// everything reflexively and the confirmation will stop meaning anything.
	h := newAPI(t)
	h.height(t, 172)
	if resp, body := h.height(t, 173); resp.StatusCode != http.StatusCreated {
		t.Fatalf("1 cm between visits was refused: %d %v", resp.StatusCode, body)
	}
}

func TestCorrectingAMistypedValueIsNotADeltaBreach(t *testing.T) {
	// The trap. An operator types 172, then 152 by mistake, then corrects it back to 172 —
	// and a delta rule comparing against the value being replaced would refuse exactly the
	// write that fixes the record.
	h := newAPI(t)
	_, first := h.height(t, 172)
	mistake := observationOf(t, first)["id"].(string)

	resp, body := h.record(t, map[string]any{
		"code": "BODY_HEIGHT", "value": 152, "unit": "cm",
		"confirmed": true, "confirmed_reason": "typo, correcting below",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("recording the mistake: %d %v", resp.StatusCode, body)
	}
	_ = mistake

	wrong := observationOf(t, body)["id"].(string)
	resp, body = h.record(t, map[string]any{
		"code": "BODY_HEIGHT", "value": 172, "unit": "cm",
		"replaces": wrong, "replaced_status": "CORRECTED",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("the correction was refused as a delta breach: %d %v", resp.StatusCode, body)
	}
}

// --- criterion 4 ---

func TestTheRulesAreDataAndCanBeRetunedWithoutARelease(t *testing.T) {
	h := newAPI(t)

	// 205 cm is refused today.
	if resp, _ := h.height(t, 205); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("205 cm was not refused to begin with: %d", resp.StatusCode)
	}

	// A clinic widens the band. No deployment, no code change.
	if _, err := h.SQL.Exec(`
		UPDATE core.plausibility_rule SET plausible_max = 210
		 WHERE code = 'BODY_HEIGHT' AND min_age_years = 18`); err != nil {
		t.Fatal(err)
	}

	if resp, body := h.height(t, 205); resp.StatusCode != http.StatusCreated {
		t.Fatalf("205 cm after widening the band: %d %v", resp.StatusCode, body)
	}
}

func TestTheRulesAreReadableByTheStationApp(t *testing.T) {
	// The client evaluates the same rules locally so the warning appears as the operator
	// types. It can only do that if it is given them — and it has to be given the *same*
	// ones, or the screen promises something the write then refuses.
	h := newAPI(t)

	resp, body := h.call(t, http.MethodGet, "/v1/observations/plausibility", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reading the rules: %d %v", resp.StatusCode, body)
	}
	rules, _ := body["rules"].([]any)
	if len(rules) < 10 {
		t.Fatalf("the station app was given %d rules", len(rules))
	}

	var forHeight int
	var approvedAnywhere bool
	for _, item := range rules {
		rule, _ := item.(map[string]any)
		if rule["code"] == "BODY_HEIGHT" {
			forHeight++
		}
		if rule["approved"] == true {
			approvedAnywhere = true
		}
	}
	if forHeight < 3 {
		t.Errorf("height has %d rules; there should be one per age band", forHeight)
	}
	// The seeded bands are proposals. An interface that showed them as settled would be
	// overstating what anybody has agreed to.
	if approvedAnywhere {
		t.Error("a seeded band is marked approved; these are proposals until Dr. Nahid says otherwise")
	}
}

// --- the rule that applies ---

func TestTheMostSpecificRuleWins(t *testing.T) {
	// An adult's plausible height band is not a two-year-old's, and one band wide enough for
	// both is a band that catches nothing.
	h := newAPI(t)

	for _, c := range []struct {
		age    string
		height float64
		want   int
	}{
		// 90 cm is ordinary at three and impossible-looking in an adult — but *possible*,
		// so the adult case is a confirmation rather than a refusal.
		{"2023-06-14", 90, http.StatusCreated},
		{"1985-06-14", 90, http.StatusUnprocessableEntity},
	} {
		t.Run(c.age, func(t *testing.T) {
			patient := uuid.New()
			if _, err := h.SQL.Exec(`
				INSERT INTO core.patient (id, facility_id, clinical_id, name_en, sex, birth_date,
				                          dob_precision, dob_verified_by, phone_primary, status,
				                          registered_by, registered_at)
				VALUES ($1, $2, $3, 'Test Patient', 'male', $4::date,
				        'day', 'national_id', '+8801711111102', 'active', $5, now())`,
				patient, h.facility, "DTHC-FRD-2026-"+fmt.Sprint(patient.ID()%1000000), c.age,
				h.user); err != nil {
				t.Fatal(err)
			}
			resp, body := h.record(t, map[string]any{
				"patient_id": patient.String(),
				"code":       "BODY_HEIGHT", "value": c.height, "unit": "cm",
			})
			if resp.StatusCode != c.want {
				t.Fatalf("a height of %g at %s: %d %v", c.height, c.age, resp.StatusCode, body)
			}
		})
	}
}

func TestALabValueIsCheckedByTheRegistryAndNothingNarrower(t *testing.T) {
	// Deliberate. A reference interval for a lab analyte is CP5x's problem, and a
	// plausibility rule pretending to be one would refuse exactly the abnormal results the
	// clinic exists to find.
	h := newAPI(t)
	h.role = "HISTORY"

	resp, body := h.record(t, map[string]any{
		"code": "GLUCOSE_FASTING", "value": 28, "unit": "mmol/L",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("a very high but real fasting glucose: %d %v", resp.StatusCode, body)
	}
}

func TestEveryMeasuredCodeHasARule(t *testing.T) {
	// The invariant, asserted from the outside too. A measured code with no rule accepts
	// anything an operator can type, silently, for as long as nobody notices.
	h := newAPI(t)

	rows, err := h.SQL.Query(`
		SELECT c.code FROM core.observation_code c
		 WHERE c.category IN ('ANTHRO', 'VITAL') AND c.value_type = 'numeric'
		   AND c.retired_at IS NULL
		   AND NOT EXISTS (SELECT 1 FROM core.plausibility_rule r WHERE r.code = c.code)`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			t.Fatal(err)
		}
		t.Errorf("%s is typed at a station and has no plausibility rule", code)
	}
}
