package clinical_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
)

// Station 5's structured examination (CP51).
//
// Four acceptance criteria. Two of them are properties of the record and are proven here:
//
//	2. every finding is coded and queryable for research — which, before this checkpoint, was
//	   a hope rather than a property, because nothing stopped a client sending four spellings
//	   of "absent" on four consecutive Tuesdays;
//	3. laterality is explicit on every relevant finding.
//
// Criterion 1 — a complete foot examination in under two minutes — is a stopwatch and a
// junior doctor, and it is on the manual verification list. Criterion 4's prompts are decided
// on the phone, from what is already in the record, and are tested there.
//
// The thing this file leans on hardest is that "coded" now *means* something. A finding whose
// value is not in its vocabulary cannot be stored — not "is rejected by the handler", cannot,
// by any path, because the rule is a trigger on the read model.

func examPermissions() []string {
	return []string{"observation.read.values", "observation.write.exam", "observation.write.vitals"}
}

func (h *api) exam(t *testing.T, body map[string]any) (*http.Response, map[string]any) {
	t.Helper()
	h.role = "CLINICAL_ASSISTANT"
	return h.record(t, body)
}

func monofilament(missed ...string) map[string]any {
	felt := map[string]bool{}
	for _, site := range clinical.MonofilamentSites {
		felt[site] = true
	}
	for _, site := range missed {
		felt[site] = false
	}
	return map[string]any{"felt": felt}
}

func TestACodedFindingMustBeOneOfItsAnswers(t *testing.T) {
	// Criterion 2, and the whole reason the vocabulary exists. Before it, `absent`, `Absent`
	// and `not felt` were three findings as far as any query was concerned — and a research
	// extract holding all three has no way to tell they were the same one.
	h := newAPI(t, examPermissions()...)

	resp, body := h.exam(t, map[string]any{
		"code": "DP_PULSE_LEFT", "value_code": "present",
		"effective_at": "2026-09-14T09:00:00Z",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("a pulse that is present: %d %v", resp.StatusCode, body)
	}

	for _, wrong := range []string{"Present", "not felt", "yes", "0"} {
		resp, _ := h.exam(t, map[string]any{
			"code": "DP_PULSE_LEFT", "value_code": wrong,
			"effective_at": "2026-09-14T09:01:00Z",
		})
		if resp.StatusCode == http.StatusCreated {
			t.Errorf("%q was accepted as a pulse finding", wrong)
		}
	}
}

func TestTheVocabularyCannotBeGoneRoundByAnyPath(t *testing.T) {
	// "Cannot be stored" has to mean cannot, not "the handler refuses it". A projection
	// rebuild and a hand-written INSERT go through the same trigger.
	h := newAPI(t, examPermissions()...)

	_, err := h.SQL.Exec(`
		INSERT INTO read.observation
		  (id, facility_id, patient_id, code, category, value_type, value_code,
		   effective_at, recorded_at, source, status, recorded_by, event_id, global_seq)
		VALUES (gen_random_uuid(), $1, $2, 'DP_PULSE_LEFT', 'EXAM', 'coded', 'squishy',
		        now(), now(), 'STATION', 'ACTIVE', $3, gen_random_uuid(), 999999)`,
		h.facility, h.patient, h.user)
	if err == nil {
		t.Fatal("a hand-written insert stored a value that is not in the vocabulary")
	}
}

func TestEveryLateralFindingIsRecordableOnBothSides(t *testing.T) {
	// Criterion 3. Laterality is in the code, so the failure this guards against is a finding
	// that exists for one foot and not the other — which is a screen that silently cannot
	// record the left one.
	h := newAPI(t, examPermissions()...)
	if _, err := h.SQL.Exec(`SELECT core.assert_lateral_findings_come_in_pairs()`); err != nil {
		t.Fatal(err)
	}

	for _, code := range []string{
		"VIBRATION_LEFT", "VIBRATION_RIGHT",
		"DP_PULSE_LEFT", "DP_PULSE_RIGHT",
		"PT_PULSE_LEFT", "PT_PULSE_RIGHT",
		"FOOT_DEFORMITY_LEFT", "FOOT_DEFORMITY_RIGHT",
		"FOOT_ULCER_LEFT", "FOOT_ULCER_RIGHT",
		"RETINOPATHY_LEFT", "RETINOPATHY_RIGHT",
	} {
		var n int
		if err := h.SQL.QueryRow(
			`SELECT count(*) FROM core.observation_answer WHERE code = $1`, code).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			t.Errorf("%s has no answers, so nothing can be recorded under it", code)
		}
	}
}

func TestEveryCodedObservationHasSomethingToChooseFrom(t *testing.T) {
	h := newAPI(t, examPermissions()...)
	if _, err := h.SQL.Exec(`SELECT core.assert_every_coded_observation_has_answers()`); err != nil {
		t.Fatal(err)
	}
}

func TestTheAnswersAreServedInClinicalOrder(t *testing.T) {
	// "Present, diminished, absent" is a scale. A screen that sorted it alphabetically would
	// put absent first and make every examiner read the list twice — which, twenty findings
	// into a two-minute examination, is where mistakes come from.
	h := newAPI(t, examPermissions()...)
	h.role = "CLINICAL_ASSISTANT"

	_, body := h.call(t, http.MethodGet, "/v1/observations/answers", nil)
	raw, _ := body["answers"].([]any)
	if len(raw) < 40 {
		t.Fatalf("the endpoint published %d answers", len(raw))
	}

	byCodeOrder := map[string][]string{}
	for _, item := range raw {
		answer, _ := item.(map[string]any)
		code, _ := answer["code"].(string)
		value, _ := answer["value_code"].(string)
		byCodeOrder[code] = append(byCodeOrder[code], value)
	}
	if got := byCodeOrder["DP_PULSE_LEFT"]; len(got) != 3 ||
		got[0] != "present" || got[1] != "diminished" || got[2] != "absent" {
		t.Errorf("the pulse answers arrived as %v", got)
	}

	// Exactly one normal answer per set, or a screen cannot pre-select and a report cannot
	// count abnormal findings.
	normals := map[string]int{}
	for _, item := range raw {
		answer, _ := item.(map[string]any)
		code, _ := answer["code"].(string)
		if answer["is_normal"] == true {
			normals[code]++
		}
	}
	for _, code := range []string{"DP_PULSE_LEFT", "VIBRATION_RIGHT", "FOOT_SKIN_LEFT", "MURMUR"} {
		if normals[code] != 1 {
			t.Errorf("%s has %d normal answers", code, normals[code])
		}
	}
}

func TestTheMonofilamentRecordsWhichSitesWereMissed(t *testing.T) {
	// A single "abnormal" would lose the finding. Early neuropathy at the hallux and a
	// forefoot that has lost protective sensation are different appointments.
	h := newAPI(t, examPermissions()...)

	resp, body := h.exam(t, map[string]any{
		"code": "MONOFILAMENT_LEFT", "value_json": monofilament("mth_1", "mth_3"),
		"effective_at": "2026-09-14T09:00:00Z",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("a ten-site test: %d %v", resp.StatusCode, body)
	}
	stored := observationOf(t, body)
	sites, _ := stored["value_json"].(map[string]any)
	felt, _ := sites["felt"].(map[string]any)
	if felt["mth_1"] != false || felt["hallux"] != true {
		t.Errorf("the sites came back as %v", felt)
	}
}

func TestAnIncompleteMonofilamentIsRefused(t *testing.T) {
	// Nine sites is an examiner who was interrupted. Recording it as "not felt" invents a
	// finding; recording it as "felt" hides one; refusing it sends them back to the foot.
	h := newAPI(t, examPermissions()...)

	nine := monofilament()
	delete(nine["felt"].(map[string]bool), "heel")

	resp, _ := h.exam(t, map[string]any{
		"code": "MONOFILAMENT_RIGHT", "value_json": nine,
		"effective_at": "2026-09-14T09:00:00Z",
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("nine of ten sites answered %d", resp.StatusCode)
	}
}

func TestAFootThatCouldNotBeExaminedIsARecordableFinding(t *testing.T) {
	// A dressing, an amputation, a patient who would not take their sock off. "We could not
	// look" is a finding; leaving the field blank is a record that cannot tell it apart from
	// an examination nobody got round to.
	h := newAPI(t, examPermissions()...)

	resp, body := h.exam(t, map[string]any{
		"code": "MONOFILAMENT_RIGHT", "value_json": map[string]any{"not_tested": true},
		"note":         "Dressing in place; district nurse changing it on Thursday.",
		"effective_at": "2026-09-14T09:00:00Z",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("a foot that could not be examined: %d %v", resp.StatusCode, body)
	}
}

func TestTheFootRiskCategoryFallsOutOfTheFindings(t *testing.T) {
	// The point of a structured examination: two examiners who record the same foot the same
	// way cannot disagree about its risk. An examiner who could type the category would be
	// back to an opinion with a dropdown in front of it.
	h := newAPI(t, examPermissions()...)

	for _, finding := range []map[string]any{
		{"code": "MONOFILAMENT_LEFT", "value_json": monofilament("mth_1", "mth_3", "hallux")},
		{"code": "DP_PULSE_LEFT", "value_code": "present"},
		{"code": "PT_PULSE_LEFT", "value_code": "present"},
		{"code": "FOOT_DEFORMITY_LEFT", "value_code": "none"},
		{"code": "FOOT_ULCER_LEFT", "value_code": "grade_0"},
	} {
		finding["effective_at"] = "2026-09-14T09:00:00Z"
		if resp, body := h.exam(t, finding); resp.StatusCode != http.StatusCreated {
			t.Fatalf("%v: %d %v", finding["code"], resp.StatusCode, body)
		}
	}

	resp, body := h.call(t, http.MethodPost, "/v1/observations/derive", map[string]any{
		"event_id":   uuid.Must(uuid.NewV7()).String(),
		"patient_id": h.patient.String(),
		"what":       "FOOT_RISK_LEFT",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("deriving the risk: %d %v", resp.StatusCode, body)
	}
	risk := observationOf(t, body)
	// Sensation lost, circulation intact, no deformity, no prior ulcer: category 1.
	if risk["value_code"] != "low" {
		t.Errorf("the category came back as %v", risk["value_code"])
	}
	// A derived value has to say which rule produced it and what it saw (CP43's invariant),
	// and for a categorisation "what it saw" is the four facts rather than two measurements.
	if risk["formula"] != "iwgdf_foot_risk" || risk["formula_version"] == "" {
		t.Errorf("the risk names %v version %v", risk["formula"], risk["formula_version"])
	}
	inputs, _ := risk["inputs"].(map[string]any)
	if inputs["lost_sensation"] != 1.0 || inputs["poor_circulation"] != 0.0 {
		t.Errorf("the inputs were recorded as %v", inputs)
	}
}

func TestAPriorUlcerOutranksAFootThatExaminesNormally(t *testing.T) {
	// The foot that ulcerated once is the foot that ulcerates again, and a well-healed foot
	// examines normally. A category computed from today's findings alone would send that
	// patient home in the lowest risk band.
	h := newAPI(t, examPermissions()...)

	for _, finding := range []map[string]any{
		{"code": "MONOFILAMENT_RIGHT", "value_json": monofilament()},
		{"code": "DP_PULSE_RIGHT", "value_code": "present"},
		{"code": "PT_PULSE_RIGHT", "value_code": "present"},
		{"code": "FOOT_DEFORMITY_RIGHT", "value_code": "none"},
		{"code": "FOOT_ULCER_RIGHT", "value_code": "grade_1"},
	} {
		finding["effective_at"] = "2026-09-14T09:00:00Z"
		if resp, body := h.exam(t, finding); resp.StatusCode != http.StatusCreated {
			t.Fatalf("%v: %d %v", finding["code"], resp.StatusCode, body)
		}
	}
	resp, body := h.call(t, http.MethodPost, "/v1/observations/derive", map[string]any{
		"event_id":   uuid.Must(uuid.NewV7()).String(),
		"patient_id": h.patient.String(),
		"what":       "FOOT_RISK_RIGHT",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("deriving the risk: %d %v", resp.StatusCode, body)
	}
	if got := observationOf(t, body)["value_code"]; got != "high" {
		t.Errorf("a foot with a previous ulcer came back as %v", got)
	}
}

func TestAFootNobodyHasExaminedHasNoRisk(t *testing.T) {
	// "We have not examined the left foot" tells an operator what to go and do. A category
	// invented from nothing tells them the opposite.
	h := newAPI(t, examPermissions()...)
	resp, _ := h.call(t, http.MethodPost, "/v1/observations/derive", map[string]any{
		"event_id":   uuid.Must(uuid.NewV7()).String(),
		"patient_id": h.patient.String(),
		"what":       "FOOT_RISK_LEFT",
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("an unexamined foot answered %d", resp.StatusCode)
	}
}

func TestTheHistoryOfficerNoLongerWritesAnExamination(t *testing.T) {
	// The change CP51 makes deliberately. CP42 parked the EXAM codes on the history
	// permission because there was no examination screen; a foot examination happens at
	// station 5, and the officer at station 4 does not have the patient's shoes off.
	h := newAPI(t, "observation.read.values", "observation.write.history")
	h.role = "HISTORY"
	resp, _ := h.record(t, map[string]any{
		"code": "DP_PULSE_LEFT", "value_code": "present",
		"effective_at": "2026-09-14T09:00:00Z",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("the history officer recorded a pedal pulse (%d)", resp.StatusCode)
	}
}

func TestCategorisationIsTheGuidelineWrittenOut(t *testing.T) {
	// The table, exhaustively, because a stratification with an off-by-one is a stratification
	// that puts somebody in the wrong follow-up interval for a year.
	for _, want := range []struct {
		findings clinical.FootFindings
		risk     clinical.FootRisk
	}{
		{clinical.FootFindings{}, clinical.FootRiskVeryLow},
		{clinical.FootFindings{LostSensation: true}, clinical.FootRiskLow},
		{clinical.FootFindings{PoorCirculation: true}, clinical.FootRiskLow},
		{clinical.FootFindings{Deformity: true}, clinical.FootRiskVeryLow},
		{clinical.FootFindings{LostSensation: true, PoorCirculation: true}, clinical.FootRiskModerate},
		{clinical.FootFindings{LostSensation: true, Deformity: true}, clinical.FootRiskModerate},
		{clinical.FootFindings{PoorCirculation: true, Deformity: true}, clinical.FootRiskModerate},
		{clinical.FootFindings{PriorUlcerOrAmputation: true}, clinical.FootRiskHigh},
		{clinical.FootFindings{LostSensation: true, PriorUlcerOrAmputation: true}, clinical.FootRiskHigh},
	} {
		if got := clinical.Categorise(want.findings); got != want.risk {
			t.Errorf("%+v categorised as %s, not %s", want.findings, got, want.risk)
		}
	}
}

func TestTheMonofilamentThresholdIsTwoSites(t *testing.T) {
	// One site missed is within the noise of a hurried examination; two is the threshold every
	// published protocol uses, and it is the line the risk category is drawn from.
	parse := func(missed ...string) clinical.Monofilament {
		raw, err := json.Marshal(monofilament(missed...))
		if err != nil {
			t.Fatal(err)
		}
		test, err := clinical.ParseMonofilament(raw)
		if err != nil {
			t.Fatal(err)
		}
		return test
	}
	if parse().LostSensation() {
		t.Error("a foot that felt every site was reported as having lost sensation")
	}
	if parse("hallux").LostSensation() {
		t.Error("one missed site was enough")
	}
	if !parse("hallux", "heel").LostSensation() {
		t.Error("two missed sites were not enough")
	}
	// A foot nobody could examine is not a foot with sensation. The honest answer is that we
	// do not know, and the caller distinguishes it by asking whether it was tested.
	untested, err := clinical.ParseMonofilament(json.RawMessage(`{"not_tested":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if untested.LostSensation() {
		t.Error("an untested foot was reported as having lost sensation")
	}
}
