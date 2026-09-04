package clinical_test

import (
	"math"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical/calc"
)

// Station 2, end to end (CP45).
//
// The arithmetic is proven in `internal/clinical/calc` against fixtures neither
// implementation produced. What is proven here is everything that is *not* arithmetic:
//
//   - a station's whole form lands in one transaction, or none of it lands (criterion 5's
//     attribution is meaningless on half a record);
//   - the server's derived values equal the ones the phone showed while the operator typed
//     (criterion 2), including when the inputs were only written a moment ago in the same
//     transaction — the failure mode being a BMI computed from last visit's height;
//   - a retry writes the same values rather than a second BMI.

func (h *api) batch(t *testing.T, body map[string]any) (*http.Response, map[string]any) {
	t.Helper()
	if _, ok := body["event_id"]; !ok {
		body["event_id"] = uuid.Must(uuid.NewV7()).String()
	}
	if _, ok := body["patient_id"]; !ok {
		body["patient_id"] = h.patient.String()
	}
	return h.call(t, http.MethodPost, "/v1/observations/batch", body)
}

func observationsOf(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	raw, ok := body["observations"].([]any)
	if !ok {
		t.Fatalf("no observations in %v", body)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		row, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("an observation that is not an object: %v", item)
		}
		out = append(out, row)
	}
	return out
}

// byCode indexes a batch's answer, because the assertion a test wants to make is about a
// weight, not about the fourth element.
func byCode(rows []map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, row := range rows {
		code, _ := row["code"].(string)
		if _, seen := out[code]; !seen {
			out[code] = row
		}
	}
	return out
}

func anthroForm() []map[string]any {
	return []map[string]any{
		{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BODY_HEIGHT", "value": 170, "unit": "cm"},
		{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BODY_WEIGHT", "value": 72.5, "unit": "kg"},
		{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "WAIST_CIRC", "value": 92, "unit": "cm"},
		{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "HIP_CIRC", "value": 98, "unit": "cm"},
		{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BODY_FAT_PCT", "value": 26, "unit": "%"},
		{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "MUSCLE_MASS", "value": 49.8, "unit": "kg"},
	}
}

func TestAWholeStationFormIsWrittenInOneRequest(t *testing.T) {
	// Criterion 3: the entry takes under thirty seconds. Ten round trips over clinic wifi
	// is not thirty seconds of measuring, it is thirty seconds of waiting.
	h := newAPI(t)

	resp, body := h.batch(t, map[string]any{
		"observations": anthroForm(),
		"derive":       []string{"BMI", "BMR", "IBW", "WHR"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("recording an anthropometry form: %d %v", resp.StatusCode, body)
	}
	rows := observationsOf(t, body)
	if len(rows) != 10 {
		t.Fatalf("wrote %d values, want six measured and four derived", len(rows))
	}

	indexed := byCode(rows)
	for _, code := range []string{
		"BODY_HEIGHT", "BODY_WEIGHT", "WAIST_CIRC", "HIP_CIRC", "BODY_FAT_PCT", "MUSCLE_MASS",
		"BMI", "BMR", "IBW", "WHR",
	} {
		if _, ok := indexed[code]; !ok {
			t.Errorf("%s is not in the answer", code)
		}
	}

	// Criterion 5: every value carries full attribution, derived ones included.
	for code, row := range indexed {
		if row["recorded_by"] == nil || row["recorded_by"] == "" {
			t.Errorf("%s has no recorded_by", code)
		}
		if row["recorded_role"] != "ANTHROPOMETRY" {
			t.Errorf("%s was attributed to %v", code, row["recorded_role"])
		}
	}
}

func TestTheServersDerivedValuesEqualTheOnesThePhoneShowed(t *testing.T) {
	// Criterion 2, and the reason the panel exists as a shared unit. The phone computes
	// while the operator types; the server computes when they save. If those two ever
	// disagree, the number the operator read aloud to the patient is not the number in the
	// record.
	h := newAPI(t)

	_, body := h.batch(t, map[string]any{
		"observations": anthroForm(),
		"derive":       []string{"BMI", "BMR", "IBW", "WHR"},
	})
	stored := byCode(observationsOf(t, body))

	// What the phone had on screen: the same numbers, through the same panel.
	weight, height, waist, hip := 72.5, 170.0, 92.0, 98.0
	// The seeded patient was born 1985-06-14; the fixed clock says 2026-09-14.
	ageYears := 41.2519999999999997
	onScreen := calc.AnthroPanel(calc.PanelInput{
		WeightKg: &weight, HeightCm: &height, WaistCm: &waist, HipCm: &hip,
		AgeYears: ageYears, Sex: calc.Male, Asian: true,
	})

	// BMI, WHR and ideal weight do not depend on age, so they must match exactly.
	for _, pair := range []struct {
		code   string
		screen *calc.Result
	}{
		{"BMI", onScreen.BMI},
		{"WHR", onScreen.WHR},
		{"IBW", onScreen.IBW},
	} {
		row, ok := stored[pair.code]
		if !ok {
			t.Fatalf("%s was not stored", pair.code)
		}
		if pair.screen == nil {
			t.Fatalf("the panel did not compute %s", pair.code)
		}
		got, _ := row["value"].(float64)
		if math.Abs(got-pair.screen.Value) > 1e-9 {
			t.Errorf("%s: server %.12f, phone %.12f", pair.code, got, pair.screen.Value)
		}
		if row["formula"] != pair.screen.Formula {
			t.Errorf("%s: server used %v, phone used %s", pair.code, row["formula"], pair.screen.Formula)
		}
		if row["formula_version"] != pair.screen.Version {
			t.Errorf("%s: server version %v, phone version %s",
				pair.code, row["formula_version"], pair.screen.Version)
		}
	}

	// BMR moves with age, and the server's age comes from the record rather than the
	// request — which is the point. Assert it is within a day's worth of drift of what the
	// phone would show, and that the formula and version are identical.
	row := stored["BMR"]
	got, _ := row["value"].(float64)
	if onScreen.BMR == nil {
		t.Fatal("the panel did not compute a BMR")
	}
	if math.Abs(got-onScreen.BMR.Value) > 5*0.0137 {
		t.Errorf("BMR: server %.4f, phone %.4f — more than a day of age apart", got, onScreen.BMR.Value)
	}
	if row["formula"] != onScreen.BMR.Formula || row["formula_version"] != onScreen.BMR.Version {
		t.Errorf("BMR: server %v@%v, phone %s@%s",
			row["formula"], row["formula_version"], onScreen.BMR.Formula, onScreen.BMR.Version)
	}
}

func TestADerivationSeesTheMeasurementsWrittenBesideIt(t *testing.T) {
	// The failure this rules out: the derivations run in the same transaction as the
	// measurements, so a BMI must be computed from the height in *this* request. Reading on
	// another connection would silently use the previous visit's height — a wrong BMI that
	// looks entirely plausible.
	h := newAPI(t)

	// A first visit, so there is an older height to get wrong.
	h.batch(t, map[string]any{
		"observations": []map[string]any{
			{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BODY_HEIGHT", "value": 150, "unit": "cm"},
			{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BODY_WEIGHT", "value": 60, "unit": "kg"},
		},
		"derive": []string{"BMI"},
	})

	// Confirmed, because 30 cm of height and 21 kg in one step is exactly what CP46's delta
	// rules refuse — correctly. Here it is the test being artificial rather than the patient
	// being unusual, and confirming is what a real operator would do if it were not.
	_, body := h.batch(t, map[string]any{
		"observations": []map[string]any{
			{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BODY_HEIGHT", "value": 180,
				"unit": "cm", "confirmed": true, "confirmed_reason": "re-measured standing"},
			{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BODY_WEIGHT", "value": 81,
				"unit": "kg", "confirmed": true, "confirmed_reason": "re-measured standing"},
		},
		"derive": []string{"BMI"},
	})
	row := byCode(observationsOf(t, body))["BMI"]
	got, _ := row["value"].(float64)
	// 81 kg at 180 cm is 25.0. At the old 150 cm it would be 36.0 — a different clinical
	// category, from the same request.
	if math.Abs(got-25.0) > 1e-9 {
		t.Errorf("BMI is %v; 81 kg at 180 cm is 25.0, and at last visit's height it would be 36", got)
	}
	inputs, _ := row["inputs"].(map[string]any)
	if height, _ := inputs["height_cm"].(float64); math.Abs(height-180) > 1e-9 {
		t.Errorf("the stored inputs say the height was %v", inputs["height_cm"])
	}
}

func TestABatchIsAllOrNothing(t *testing.T) {
	// A record holding three of six measurements is worse than one holding none: the
	// operator sees a failure and types the set again, and now the patient has two heights
	// and one weight from the same minute.
	h := newAPI(t)

	resp, body := h.batch(t, map[string]any{
		"observations": []map[string]any{
			{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BODY_HEIGHT", "value": 170, "unit": "cm"},
			{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BODY_WEIGHT", "value": 72.5, "unit": "kg"},
			// 900 cm is outside the plausibility band, and refuses the whole batch.
			{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "WAIST_CIRC", "value": 900, "unit": "cm"},
		},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("an implausible value in a batch: %d %v", resp.StatusCode, body)
	}
	// And it says which one, because a form of three numbers with one message is a form the
	// operator has to re-read entirely.
	if fields := fieldsOf(t, body); fields["observation"] == nil {
		t.Errorf("the refusal does not say which value: %v", fields)
	}

	resp, listed := h.call(t, http.MethodGet, "/v1/patients/"+h.patient.String()+"/observations", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reading the patient back: %d %v", resp.StatusCode, listed)
	}
	if rows, _ := listed["observations"].([]any); len(rows) != 0 {
		t.Errorf("the refused batch left %d values behind", len(rows))
	}
}

func TestSavingTwiceWritesOneSetOfValues(t *testing.T) {
	// A tablet that lost the reply and pressed save again. Every measurement carries its own
	// event id, so the ledger absorbs those; the derived values are the interesting half,
	// because the client cannot supply their ids — they are seeded from the batch's.
	h := newAPI(t)

	batchID := uuid.Must(uuid.NewV7()).String()
	form := anthroForm()
	first := map[string]any{
		"event_id": batchID, "observations": form, "derive": []string{"BMI", "IBW"},
	}
	resp, body := h.batch(t, first)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("the first save: %d %v", resp.StatusCode, body)
	}

	// The identical request, sent again.
	resp, body = h.batch(t, map[string]any{
		"event_id": batchID, "observations": form, "derive": []string{"BMI", "IBW"},
	})
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
		t.Fatalf("the retry: %d %v", resp.StatusCode, body)
	}

	_, listed := h.call(t, http.MethodGet,
		"/v1/patients/"+h.patient.String()+"/observations?code=BMI", nil)
	rows, _ := listed["observations"].([]any)
	count := 0
	for _, item := range rows {
		row, _ := item.(map[string]any)
		if row["code"] == "BMI" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("the retry left %d BMIs", count)
	}
}

func TestAHalfFilledFormRecordsWhatItHasAndDerivesWhatItCan(t *testing.T) {
	// A waist with no hip is an ordinary half-finished entry, not a failure. Refusing the
	// whole write because a ratio could not be computed would throw away measurements the
	// operator did take.
	h := newAPI(t)

	resp, body := h.batch(t, map[string]any{
		"observations": []map[string]any{
			{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BODY_HEIGHT", "value": 165, "unit": "cm"},
			{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "WAIST_CIRC", "value": 88, "unit": "cm"},
		},
		"derive": []string{"BMI", "WHR", "IBW"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("a half-filled form: %d %v", resp.StatusCode, body)
	}
	stored := byCode(observationsOf(t, body))
	if _, ok := stored["IBW"]; !ok {
		t.Error("ideal weight needs only a height, and should have been derived")
	}
	if _, ok := stored["BMI"]; ok {
		t.Error("a BMI was derived with no weight on record")
	}
	if _, ok := stored["WHR"]; ok {
		t.Error("a waist-hip ratio was derived with no hip on record")
	}
}

func TestABatchCannotSpanTwoPatients(t *testing.T) {
	// The mistake this rules out is one field set wrongly in one of six items, inside a
	// transaction that then makes it look deliberate.
	h := newAPI(t)
	other := uuid.New()

	resp, body := h.batch(t, map[string]any{
		"observations": []map[string]any{
			{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BODY_HEIGHT", "value": 170, "unit": "cm"},
			{"event_id": uuid.Must(uuid.NewV7()).String(), "patient_id": other.String(),
				"code": "BODY_WEIGHT", "value": 72.5, "unit": "kg"},
		},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a batch spanning two patients: %d %v", resp.StatusCode, body)
	}
}

func TestABatchIsCheckedAgainstTheActiveRoleValueByValue(t *testing.T) {
	// The batch must not be a way around CP41's rule. An operator wearing the anthropometry
	// hat cannot record a blood pressure, and putting it in a list with five heights does
	// not change that.
	h := newAPI(t, "observation.read.values", "observation.write.anthro")

	resp, body := h.batch(t, map[string]any{
		"observations": []map[string]any{
			{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BODY_HEIGHT", "value": 170, "unit": "cm"},
			{"event_id": uuid.Must(uuid.NewV7()).String(), "code": "BP_SYSTOLIC", "value": 128, "unit": "mm[Hg]"},
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a vital smuggled into an anthropometry batch: %d %v", resp.StatusCode, body)
	}
}

func TestABatchHasACeiling(t *testing.T) {
	// Not a bulk import. A batch is one station form, and an unbounded one would hold a
	// transaction open for as long as a client cared to make it.
	h := newAPI(t)
	var many []map[string]any
	for i := 0; i < 25; i++ {
		many = append(many, map[string]any{
			"event_id": uuid.Must(uuid.NewV7()).String(),
			"code":     "BODY_WEIGHT", "value": 70 + float64(i)/10, "unit": "kg",
		})
	}
	resp, _ := h.batch(t, map[string]any{"observations": many})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a batch of 25: %d", resp.StatusCode)
	}
}

func TestIdealBodyWeightIsDerivedAndNeverTyped(t *testing.T) {
	// Nobody measures an ideal weight. A record that let one be typed would be a record
	// where a clinical number could arrive with no formula behind it — which 00027's trigger
	// refuses, and this proves the refusal reaches the operator as a sentence.
	h := newAPI(t)
	resp, body := h.record(t, map[string]any{"code": "IBW", "value": 65.9, "unit": "kg"})
	if resp.StatusCode == http.StatusCreated {
		t.Fatalf("an ideal weight was typed in: %v", body)
	}
}
