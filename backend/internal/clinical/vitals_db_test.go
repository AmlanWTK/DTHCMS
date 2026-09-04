package clinical_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// Station 5's vitals (CP49).
//
// The screen is verified on a real phone. What is proven here is the record: that a repeat
// measurement is a second fact rather than a correction, that a blood pressure keeps the arm
// and position it was taken in, and that "what is normal" is a separate idea from "what is
// possible" — three concepts that clinical software routinely collapses into one, after
// which staff ignore all three.

func (h *api) vitals(t *testing.T, observations []map[string]any) (*http.Response, map[string]any) {
	t.Helper()
	h.role = "CLINICAL_ASSISTANT"
	return h.batch(t, map[string]any{"observations": observations})
}

func TestTwoBloodPressuresInOneSittingAreTwoFacts(t *testing.T) {
	// Criterion 2. A blood pressure measured once is a blood pressure measured badly, and
	// the second reading is not a correction of the first — a record that treated it as one
	// would lose the fact that they differed, which is often the finding.
	h := newAPI(t)

	resp, body := h.vitals(t, []map[string]any{
		{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BP_SYSTOLIC", "value": 148,
			"unit": "mm[Hg]", "effective_at": "2026-09-14T09:00:00Z"},
		{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BP_DIASTOLIC", "value": 94,
			"unit": "mm[Hg]", "effective_at": "2026-09-14T09:00:00Z"},
		{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BP_SYSTOLIC", "value": 138,
			"unit": "mm[Hg]", "effective_at": "2026-09-14T09:03:00Z"},
		{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BP_DIASTOLIC", "value": 88,
			"unit": "mm[Hg]", "effective_at": "2026-09-14T09:03:00Z"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("two readings: %d %v", resp.StatusCode, body)
	}

	_, listed := h.call(t, http.MethodGet,
		"/v1/patients/"+h.patient.String()+"/observations/BP_SYSTOLIC/history", nil)
	rows, _ := listed["observations"].([]any)
	if len(rows) != 2 {
		t.Fatalf("the record holds %d systolic readings", len(rows))
	}
	for _, item := range rows {
		row, _ := item.(map[string]any)
		if row["status"] != "ACTIVE" {
			t.Errorf("a repeat reading was marked %v; neither replaces the other", row["status"])
		}
	}
}

func TestABloodPressureKeepsTheArmAndPositionItWasTakenIn(t *testing.T) {
	// A series that silently mixes a sitting left-arm reading with a supine right-arm one is
	// a series nobody can read a trend from.
	h := newAPI(t)

	resp, body := h.vitals(t, []map[string]any{
		{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BP_SYSTOLIC", "value": 128,
			"unit": "mm[Hg]", "effective_at": "2026-09-14T09:00:00Z"},
		{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BP_DIASTOLIC", "value": 82,
			"unit": "mm[Hg]", "effective_at": "2026-09-14T09:00:00Z"},
		{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BP_ARM",
			"value_code": "left", "effective_at": "2026-09-14T09:00:00Z"},
		{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BP_POSITION",
			"value_code": "sitting", "effective_at": "2026-09-14T09:00:00Z"},
		{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BP_CUFF",
			"value_code": "adult", "effective_at": "2026-09-14T09:00:00Z"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("a blood pressure with its context: %d %v", resp.StatusCode, body)
	}
	stored := byCode(observationsOf(t, body))
	if stored["BP_ARM"]["value_code"] != "left" {
		t.Errorf("the arm was stored as %v", stored["BP_ARM"]["value_code"])
	}
	// The context shares the reading's effective time, which is what groups them.
	if stored["BP_ARM"]["effective_at"] != stored["BP_SYSTOLIC"]["effective_at"] {
		t.Error("the arm and the reading have different effective times, so nothing joins them")
	}
}

func TestAFullVitalsSetIsOneRequest(t *testing.T) {
	// Criterion 1 gives the whole set thirty seconds, most of which should be the operator
	// working the cuff and the oximeter rather than watching a spinner.
	h := newAPI(t)

	resp, body := h.vitals(t, []map[string]any{
		{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BP_SYSTOLIC", "value": 128, "unit": "mm[Hg]"},
		{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BP_DIASTOLIC", "value": 82, "unit": "mm[Hg]"},
		{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "HEART_RATE", "value": 78, "unit": "/min"},
		{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "SPO2", "value": 98, "unit": "%"},
		{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BODY_TEMP", "value": 98.6, "unit": "[degF]"},
		{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "RESP_RATE", "value": 16, "unit": "/min"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("a full vitals set: %d %v", resp.StatusCode, body)
	}
	rows := byCode(observationsOf(t, body))
	if len(rows) != 6 {
		t.Fatalf("wrote %d values", len(rows))
	}
	// The Fahrenheit reading is stored in Celsius and comes back in Fahrenheit.
	temp := rows["BODY_TEMP"]
	if value, _ := temp["value"].(float64); value < 36.9 || value > 37.1 {
		t.Errorf("98.6 °F was stored as %v °C", temp["value"])
	}
	if entered, _ := temp["entered_value"].(float64); entered != 98.6 {
		t.Errorf("the entered value came back as %v", temp["entered_value"])
	}
}

func TestWhatIsNormalIsNotWhatIsPossible(t *testing.T) {
	// Three different things a number can be outside, and clinical software routinely
	// collapses them into one — after which staff ignore all three.
	//
	// A systolic of 210 is *outside the normal range* and *entirely plausible*. It must save
	// without a fight, because it is exactly the reading the clinic exists to find.
	h := newAPI(t)
	h.role = "CLINICAL_ASSISTANT"

	resp, body := h.record(t, map[string]any{
		"code": "BP_SYSTOLIC", "value": 210, "unit": "mm[Hg]",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("a hypertensive reading was refused: %d %v", resp.StatusCode, body)
	}

	// And one that is not a blood pressure at all is refused.
	resp, _ = h.record(t, map[string]any{
		"code": "BP_SYSTOLIC", "value": 12, "unit": "mm[Hg]",
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a systolic of 12 was accepted: %d", resp.StatusCode)
	}
}

func TestTheStationAppIsToldWhatIsNormal(t *testing.T) {
	// Criterion 3 is a flag that appears as the number is typed. That only works if the
	// tablet holds the ranges — and holds the *same* ones the server would apply.
	h := newAPI(t)

	resp, body := h.call(t, http.MethodGet, "/v1/observations/reference-ranges", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reading the ranges: %d %v", resp.StatusCode, body)
	}
	ranges, _ := body["ranges"].([]any)
	if len(ranges) < 10 {
		t.Fatalf("the station app was given %d ranges", len(ranges))
	}

	byCodeCount := map[string]int{}
	for _, item := range ranges {
		row, _ := item.(map[string]any)
		code, _ := row["code"].(string)
		byCodeCount[code]++
		if row["approved"] == true {
			t.Error("a seeded range is marked approved; these need clinical confirmation")
		}
	}
	// A pulse needs a band per age group, or a two-year-old is flagged against an adult's.
	if byCodeCount["HEART_RATE"] < 4 {
		t.Errorf("heart rate has %d ranges; children need their own", byCodeCount["HEART_RATE"])
	}
}

func TestTheListedRangesResolveToTheSameOneTheServerPicks(t *testing.T) {
	// The same property CP46's rules have, for the same reason: a screen that flagged
	// against one band while the server used another would be a screen nobody could trust.
	h := newAPI(t)

	_, body := h.call(t, http.MethodGet, "/v1/observations/reference-ranges", nil)
	listed, _ := body["ranges"].([]any)

	matches := func(row map[string]any, sex string, age float64) bool {
		if got, ok := row["sex"].(string); ok && got != "" && got != sex {
			return false
		}
		if got, ok := row["min_age_years"].(float64); ok && age < got {
			return false
		}
		if got, ok := row["max_age_years"].(float64); ok && age >= got {
			return false
		}
		return true
	}

	checked := 0
	for _, code := range []string{"HEART_RATE", "RESP_RATE", "BP_SYSTOLIC", "SPO2", "BODY_TEMP"} {
		for _, sex := range []string{"male", "female"} {
			for _, age := range []float64{0.5, 1, 3, 6, 11, 12, 17, 18, 45} {
				var want map[string]any
				for _, item := range listed {
					row, _ := item.(map[string]any)
					if row["code"] == code && matches(row, sex, age) {
						want = row
						break
					}
				}
				var low, high *float64
				err := h.SQL.QueryRow(
					`SELECT low, high FROM core.reference_range_for($1::text, $2::text, $3::numeric)`,
					code, sex, age).Scan(&low, &high)
				if err != nil {
					if want != nil {
						t.Errorf("%s/%s/%v: the client would flag and the server would not", code, sex, age)
					}
					continue
				}
				if want == nil {
					t.Errorf("%s/%s/%v: the server would flag and the client would not", code, sex, age)
					continue
				}
				same := func(name string, listedValue any, serverValue *float64) {
					t.Helper()
					got, hasListed := listedValue.(float64)
					switch {
					case !hasListed && serverValue == nil:
					case hasListed && serverValue != nil && got == *serverValue:
					default:
						t.Errorf("%s/%s/%v: %s differs — client %v, server %v",
							code, sex, age, name, listedValue, serverValue)
					}
				}
				same("low", want["low"], low)
				same("high", want["high"], high)
				checked++
			}
		}
	}
	if checked < 50 {
		t.Fatalf("only %d combinations were compared", checked)
	}
}

func TestEveryVitalHasARangeOrTheFieldNeverTurnsAmber(t *testing.T) {
	h := newAPI(t)
	rows, err := h.SQL.Query(`
		SELECT c.code FROM core.observation_code c
		 WHERE c.category = 'VITAL' AND c.value_type = 'numeric' AND c.retired_at IS NULL
		   AND NOT EXISTS (SELECT 1 FROM core.reference_range r WHERE r.code = c.code)`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			t.Fatal(err)
		}
		t.Errorf("%s is a vital with no reference range, so its field never turns amber", code)
	}
}
