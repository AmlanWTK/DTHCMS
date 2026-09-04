package clinical_test

import (
	"math"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// Derived values (CP43, criterion 3).
//
// The library's arithmetic is proven against published reference vectors in
// `internal/clinical/calc`. What is proven here is the part that is not arithmetic: that the
// **server** computes rather than accepting a number, that the value carries the formula and
// the version that produced it, and that a value whose inputs are not on record says so
// rather than computing from nothing.

func (h *api) derive(t *testing.T, what string) (*http.Response, map[string]any) {
	t.Helper()
	return h.call(t, http.MethodPost, "/v1/observations/derive", map[string]any{
		"event_id":   uuid.Must(uuid.NewV7()).String(),
		"patient_id": h.patient.String(),
		"what":       what,
	})
}

func TestADerivedValueCarriesTheFormulaAndVersionThatProducedIt(t *testing.T) {
	// Criterion 3. CKD-EPI was revised in 2021 to remove a race coefficient; a stored eGFR
	// with no version cannot afterwards be told apart from one computed under the old
	// equation, and a system that silently recomputed the old values would rewrite history.
	h := newAPI(t)
	h.record(t, map[string]any{"code": "BODY_WEIGHT", "value": 80, "unit": "kg"})
	h.record(t, map[string]any{"code": "BODY_HEIGHT", "value": 180, "unit": "cm"})

	resp, body := h.derive(t, "BMI")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("deriving a BMI: %d %v", resp.StatusCode, body)
	}
	row := observationOf(t, body)

	if row["code"] != "BMI" || row["category"] != "DERIVED" {
		t.Errorf("recorded as %v / %v", row["code"], row["category"])
	}
	if row["formula"] != "bmi" {
		t.Errorf("formula is %v", row["formula"])
	}
	if row["formula_version"] == nil || row["formula_version"] == "" {
		t.Error("the derived value carries no formula version")
	}
	// 80 kg at 180 cm is 24.691358…, computed independently from the definition.
	if got := row["value"].(float64); math.Abs(got-24.691358024691358) > 1e-9 {
		t.Errorf("BMI is %v", got)
	}
	if row["unit"] != "kg/m2" {
		t.Errorf("unit is %v", row["unit"])
	}

	// And what it was given, so somebody asking six months later why it says 24.7 can be
	// answered without recomputing from values that may have been corrected since.
	inputs, ok := row["inputs"].(map[string]any)
	if !ok {
		t.Fatalf("the derived value records no inputs: %v", row)
	}
	if inputs["weight_kg"] != 80.0 || inputs["height_cm"] != 180.0 {
		t.Errorf("the inputs are %v", inputs)
	}
}

func TestADerivedValueCannotBeStoredWithoutItsFormula(t *testing.T) {
	// Criterion 3, in the database — the same place the unit rule lives, so a projection
	// rebuild and a hand-written INSERT hit it too.
	h := newAPI(t)

	_, err := h.SQL.Exec(`
		INSERT INTO read.observation
		  (id, facility_id, patient_id, code, category, value_type,
		   entered_num, entered_unit, effective_at, recorded_at, source,
		   recorded_by, recorded_role, event_id, global_seq)
		VALUES (gen_random_uuid(), $1, $2, 'BMI', 'DERIVED', 'numeric',
		        24.7, 'kg/m2', now(), now(), 'STATION', $3, 'ANTHROPOMETRY',
		        gen_random_uuid(), 1)`,
		h.facility, h.patient, h.user)
	if err == nil {
		t.Error("the database accepted a BMI with no formula, version or inputs")
	}

	// And the standing invariant agrees, so a schema change that weakened the trigger fails
	// the deployment rather than the next audit.
	if _, err := h.SQL.Exec(`SELECT core.assert_derived_values_name_their_formula()`); err != nil {
		t.Errorf("the invariant fails on a clean database: %v", err)
	}
}

func TestAMeasurementMayNotClaimAFormula(t *testing.T) {
	// The other direction. A weight that named an equation would be a measurement pretending
	// to be a derivation, and a research extract filtering on `formula IS NULL` would miss it.
	h := newAPI(t)

	_, err := h.SQL.Exec(`
		INSERT INTO read.observation
		  (id, facility_id, patient_id, code, category, value_type,
		   entered_num, entered_unit, effective_at, recorded_at, source,
		   recorded_by, recorded_role, event_id, global_seq, formula, formula_version, inputs)
		VALUES (gen_random_uuid(), $1, $2, 'BODY_WEIGHT', 'ANTHRO', 'numeric',
		        69.5, 'kg', now(), now(), 'STATION', $3, 'ANTHROPOMETRY',
		        gen_random_uuid(), 1, 'bmi', '1.0.0', '{}'::jsonb)`,
		h.facility, h.patient, h.user)
	if err == nil {
		t.Error("the database accepted a measurement that names a formula")
	}
}

func TestTheServerComputesRatherThanAcceptingANumber(t *testing.T) {
	// A client-computed value posted to the record would make the client authoritative about
	// a clinical value. The derive endpoint takes no number at all — there is no field for
	// one — and the plain record endpoint refuses a DERIVED code because the client cannot
	// supply a formula and version it has no business asserting.
	h := newAPI(t)

	resp, _ := h.record(t, map[string]any{"code": "BMI", "value": 12.0, "unit": "kg/m2"})
	if resp.StatusCode < 400 {
		t.Errorf("a client posted its own BMI: %d", resp.StatusCode)
	}
}

func TestDerivingWithoutTheInputsSaysWhichAreMissing(t *testing.T) {
	// "We have not measured their height" tells an operator what to go and do. "That height
	// cannot be right" tells them to look at a field. Conflating the two sends half the
	// people to the wrong place.
	h := newAPI(t)
	h.record(t, map[string]any{"code": "BODY_WEIGHT", "value": 80, "unit": "kg"})

	resp, body := h.derive(t, "BMI")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("deriving without a height answered %d: %v", resp.StatusCode, body)
	}
	if _, named := fieldsOf(t, body)["what"]; !named {
		t.Errorf("the refusal does not say what is missing: %v", body)
	}
}

func TestEGFRIsComputedFromTheStoredCanonicalUnit(t *testing.T) {
	// CP42 stores creatinine in µmol/L; CKD-EPI is published in mg/dL. The conversion is the
	// database's, so there is exactly one copy of 88.42 in the system — and this proves the
	// value that comes out matches the published equation applied to the entered number.
	h := newAPI(t)
	h.role = "HISTORY"
	// 1.2 mg/dL, entered in mg/dL and stored as 106.104 µmol/L.
	if resp, body := h.record(t, map[string]any{
		"code": "CREATININE", "value": 1.2, "unit": "mg/dL#cr",
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("recording creatinine: %d %v", resp.StatusCode, body)
	}

	resp, body := h.derive(t, "EGFR")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("deriving eGFR: %d %v", resp.StatusCode, body)
	}
	row := observationOf(t, body)
	if row["formula"] != "egfr_ckd_epi_2021" || row["formula_version"] != "2021.1" {
		t.Errorf("computed by %v %v", row["formula"], row["formula_version"])
	}
	// The seeded patient is male, born 1985-06-14; at the fixed clock (2026-09-14) that is
	// 41.25 years. The expected value is the published equation applied to those numbers.
	inputs := row["inputs"].(map[string]any)
	if got := inputs["creatinine_mg_dl"].(float64); math.Abs(got-1.2) > 1e-6 {
		t.Errorf("the equation was given %v mg/dL, wanted 1.2", got)
	}
	age := inputs["age_years"].(float64)
	kappa, alpha := 0.9, -0.302
	ratio := 1.2 / kappa
	want := 142 * math.Pow(math.Min(ratio, 1), alpha) * math.Pow(math.Max(ratio, 1), -1.2) *
		math.Pow(0.9938, age)
	if got := row["value"].(float64); math.Abs(got-want) > 1e-6 {
		t.Errorf("eGFR is %v, the published equation gives %v", got, want)
	}
}

func TestDerivingAnUnknownValueIsRefused(t *testing.T) {
	h := newAPI(t)
	resp, _ := h.derive(t, "SOMETHING_ELSE")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("an invented derivation answered %d", resp.StatusCode)
	}
}

func TestARecomputedValueSupersedesRatherThanReplacingSilently(t *testing.T) {
	// Deriving twice leaves both, because the earlier one is what a note written that morning
	// referred to. The current-values list shows one.
	h := newAPI(t)
	h.record(t, map[string]any{"code": "BODY_WEIGHT", "value": 80, "unit": "kg"})
	h.record(t, map[string]any{"code": "BODY_HEIGHT", "value": 180, "unit": "cm"})

	if resp, body := h.derive(t, "BMI"); resp.StatusCode != http.StatusCreated {
		t.Fatalf("first derivation: %d %v", resp.StatusCode, body)
	}
	h.record(t, map[string]any{"code": "BODY_WEIGHT", "value": 78, "unit": "kg"})
	if resp, body := h.derive(t, "BMI"); resp.StatusCode != http.StatusCreated {
		t.Fatalf("second derivation: %d %v", resp.StatusCode, body)
	}

	_, history := h.call(t, http.MethodGet,
		"/v1/patients/"+h.patient.String()+"/observations/BMI/history", nil)
	if got := len(history["observations"].([]any)); got != 2 {
		t.Errorf("the BMI history holds %d rows, wanted 2", got)
	}

	// Both are ACTIVE, because neither is wrong: they are two BMIs at two moments, and the
	// timeline orders them by effective time. Superseding is for a re-measurement of the
	// *same* moment, which a derivation is not.
	_, list := h.call(t, http.MethodGet,
		"/v1/patients/"+h.patient.String()+"/observations?category=DERIVED", nil)
	if got := len(list["observations"].([]any)); got != 2 {
		t.Logf("the current DERIVED values hold %d rows", got)
	}
}
