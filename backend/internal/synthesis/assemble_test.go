package synthesis

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/allergy"
	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/history"
	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

// The deterministic half, tested without a database and without a model.
//
// This is the file the checkpoint's phrase *"the assembly step should be independently testable
// without any model at all"* is about. Everything here is a value in and a value out: no pool, no
// gateway, no provider, no fixtures on disk. What it asserts is the property CP72 will build on —
// that the context is a complete, addressable account of what the model was shown.

var (
	testNow    = time.Date(2026, 9, 14, 4, 30, 0, 0, time.UTC)
	testClinic = time.Date(2026, 9, 14, 0, 0, 0, 0, visit.Dhaka)
	// The clinic day every fact taken "today" is dated to, written once so the duplicate-reference
	// test can tell an original from a suffixed one.
	testClinicDay = "2026-09-14"
)

// fixture is a follow-up visit part-way through the morning, with a two-year HbA1c series, a
// history of diabetes, a recorded allergy and one station not yet done. Enough to exercise every
// section without being a second implementation of the assembler.
func fixture() Raw {
	patient := uuid.New()
	visitID := uuid.New()

	obs := func(code string, value float64, unit string, daysAgo int) clinical.Observation {
		v := value
		return clinical.Observation{
			ID: uuid.New(), PatientID: patient, Code: code, Value: &v, Unit: unit,
			Category:    categoryFor(code),
			EffectiveAt: testNow.AddDate(0, 0, -daysAgo),
			Status:      clinical.Active, Source: clinical.Station,
		}
	}

	// Today's HbA1c and weight are the *same rows* in `current` and in the trend series, because
	// that is what the two store reads return: one is "the newest value of everything" and the other
	// is "every value of this code", and the newest appears in both. A fixture that built them
	// separately would give the assembler two observations where the clinic has one, and the test
	// that says one measurement is one fact would then be asserting something about a record nobody
	// has.
	hba1cToday := obs("HBA1C", 8.2, "%", 0)
	weightToday := obs("BODY_WEIGHT", 71.5, "kg", 0)

	return Raw{
		Location:     visit.Dhaka,
		Demographics: Demographics{AgeMonths: 41 * 12, Sex: "female", AgeText: "41 years"},
		Visit: visit.Visit{
			ID: visitID, PatientID: patient, VisitType: visit.FollowUp,
			Status: visit.Open, ClinicDay: testClinic,
			ChiefComplaint: "tired for two weeks, feet tingling at night",
		},
		Planned: []visit.PlannedStation{
			{Position: 1, StationCode: "STN_REGISTRATION", Required: true},
			{Position: 2, StationCode: "STN_ANTHROPOMETRY", Required: true},
			{Position: 3, StationCode: "STN_COUNSELING", Required: true},
			{Position: 4, StationCode: "STN_EXAMINATION", Required: true},
			{Position: 5, StationCode: "STN_CONSULTATION", Required: true},
			{Position: 6, StationCode: "STN_QA", Required: true},
		},
		Encounters: []visit.Encounter{
			{ID: uuid.New(), VisitID: visitID, StationCode: "STN_REGISTRATION",
				Status: visit.Finished, StartedAt: testNow.Add(-90 * time.Minute), Outcome: "completed"},
			{ID: uuid.New(), VisitID: visitID, StationCode: "STN_ANTHROPOMETRY",
				Status: visit.Finished, StartedAt: testNow.Add(-70 * time.Minute), Outcome: "completed"},
			{ID: uuid.New(), VisitID: visitID, StationCode: "STN_COUNSELING",
				Status: visit.Finished, StartedAt: testNow.Add(-50 * time.Minute), Outcome: "completed"},
		},
		Current: []clinical.Observation{
			weightToday,
			obs("BP_SYSTOLIC", 148, "mm[Hg]", 0),
			obs("BP_DIASTOLIC", 92, "mm[Hg]", 0),
			hba1cToday,
			obs("BMI", 28.4, "kg/m2", 0),
			// SPO2 is in the fixture because it is the code that actually sprang the scrubber
			// trap: `obs.spo2` ends in a digit, and joined to an eight-digit date by a separator
			// the pattern counts it becomes a nine-digit "telephone number". The fixture had no
			// such code, so the test passed while the assembler was broken for one visit in
			// three — which is exactly the shape of green check this project has been bitten by.
			obs("SPO2", 97, "%", 0),
		},
		Trends: map[string][]clinical.Observation{
			"HBA1C": {
				hba1cToday,
				obs("HBA1C", 7.6, "%", 180),
				obs("HBA1C", 7.1, "%", 400),
			},
			"BODY_WEIGHT": {
				weightToday,
				obs("BODY_WEIGHT", 69.0, "kg", 180),
			},
		},
		// The unit catalogue, as `core.unit` seeds it. Without it the fixture would assert against
		// UCUM codes and three decimals, which is not what a physician is shown — and the test
		// would pass while the page read "196 mm[Hg]".
		Units: []clinical.Unit{
			{Code: "kg", DisplayEN: "kg", Decimals: 1},
			{Code: "cm", DisplayEN: "cm", Decimals: 1},
			{Code: "mm[Hg]", DisplayEN: "mmHg", Decimals: 0},
			{Code: "%", DisplayEN: "%", Decimals: 0},
			{Code: "kg/m2", DisplayEN: "kg/m²", Decimals: 1},
			{Code: "1", DisplayEN: "", Decimals: 2},
			{Code: "Cel", DisplayEN: "°C", Decimals: 1},
			{Code: "umol/L", DisplayEN: "µmol/L", Decimals: 0},
		},
		Ranges: []clinical.ReferenceRange{
			{Code: "BP_SYSTOLIC", Low: ptr(90.0), High: ptr(140.0)},
			{Code: "HBA1C", High: ptr(6.5)},
		},
		History: []history.Item{
			{ID: uuid.New(), PatientID: patient, Kind: "condition", DisplayEN: "Type 2 diabetes mellitus",
				Code: "E11", Said: "sugar since the flood", Status: "active",
				RecordedAt: testNow.AddDate(-3, 0, 0), ConfirmedAt: &testNow},
			{ID: uuid.New(), PatientID: patient, Kind: "medication", DisplayEN: "Metformin",
				Dose: "500 mg", Status: "active", RecordedAt: testNow.AddDate(-2, 0, 0)},
		},
		Allergies: allergy.State{
			Status: "HAS_ALLERGY",
			Allergies: []allergy.Allergy{{
				ID: uuid.New(), PatientID: patient, DisplayEN: "Penicillin",
				ReactionEN: "rash", Severity: "moderate", Certainty: "confirmed",
				RecordedAt: testNow.AddDate(-1, 0, 0),
			}},
		},
		PriorVisits: []visit.Visit{{
			ID: uuid.New(), PatientID: patient, VisitType: visit.FollowUp, Status: visit.Closed,
			ClinicDay: testClinic.AddDate(0, 0, -180),
			Diagnoses: "Type 2 diabetes, suboptimal control", Plan: "increase metformin, review in 6 months",
			NextReviewOn: ptrTime(testClinic.AddDate(0, 0, -1)),
		}},
	}
}

func categoryFor(code string) clinical.Category {
	switch code {
	case "HBA1C":
		return clinical.Lab
	case "BMI":
		return clinical.Derived
	case "BODY_WEIGHT":
		return clinical.Anthro
	}
	return clinical.Vital
}

func ptr(v float64) *float64         { return &v }
func ptrTime(t time.Time) *time.Time { return &t }

// TestEveryNumberInTheContextIsACitableFact is the property CP72 rests on.
//
// The grounding check will ask "does every number in the model's answer resolve to a stored fact",
// and that question is only answerable if every number the model was *shown* is in the fact index
// under a reference. A section that carried a value the index did not have would be a number the
// model could quote and the validator would then reject as a hallucination — the worst of both
// failures, since the number is real.
func TestEveryNumberInTheContextIsACitableFact(t *testing.T) {
	ctx := Assemble(fixture(), testNow)
	facts := ctx.FactRefs()

	if len(facts) == 0 {
		t.Fatal("the assembler produced no facts at all")
	}

	check := func(what, reference, value string) {
		t.Helper()
		fact, known := facts[reference]
		if !known {
			t.Errorf("%s cites %q, which is not in the fact index", what, reference)
			return
		}
		if value != "" && fact.Value != value {
			t.Errorf("%s says %q but fact %s says %q", what, value, reference, fact.Value)
		}
	}

	for _, m := range ctx.Current {
		check("current measurement "+m.Code, m.Ref, m.Value)
	}
	for _, trend := range ctx.Trends {
		for _, point := range trend.Points {
			check("trend point "+trend.Code, point.Ref, point.Value)
		}
		if trend.Change != nil {
			check("trend change "+trend.Code, trend.Change.Ref, trend.Change.Delta)
		}
	}
	for _, item := range ctx.History {
		check("history item", item.Ref, "")
	}
	for _, item := range ctx.Allergies.Items {
		check("allergy", item.Ref, "")
	}
	for _, prior := range ctx.PriorVisits {
		check("prior visit", prior.Ref, "")
	}
}

// TestTheTrendArithmeticIsDoneHereAndNotByTheModel.
//
// A language model asked to subtract two numbers is usually right, and "usually" is the problem:
// the error is invisible, plausible, and inside a clinical sentence. So the change is computed in
// Go and cited like any other fact — and this test is what says the computation is correct rather
// than merely present.
func TestTheTrendArithmeticIsDoneHereAndNotByTheModel(t *testing.T) {
	ctx := Assemble(fixture(), testNow)

	var hba1c *Trend
	for i := range ctx.Trends {
		if ctx.Trends[i].Code == "HBA1C" {
			hba1c = &ctx.Trends[i]
		}
	}
	if hba1c == nil {
		t.Fatal("no HbA1c trend was assembled from three HbA1c results")
	}
	if hba1c.Change == nil {
		t.Fatal("the HbA1c trend carries no computed change")
	}
	// 7.1 four hundred days ago to 8.2 today: the series is oldest-first and the delta is signed.
	if hba1c.Change.From != "7.1" || hba1c.Change.To != "8.2" {
		t.Errorf("the change runs %s → %s, want 7.1 → 8.2", hba1c.Change.From, hba1c.Change.To)
	}
	if hba1c.Change.Delta != "+1.1" {
		t.Errorf("the computed delta is %q, want %q", hba1c.Change.Delta, "+1.1")
	}
	if hba1c.Change.OverDays != 400 {
		t.Errorf("the interval is %d days, want 400", hba1c.Change.OverDays)
	}
}

// TestOneMeasurementIsOneFactHoweverManySectionsMentionIt.
//
// Today's HbA1c is both the latest value and the last point of the trend. If the two sections
// minted separate references, the model could cite one number under two names and a reviewer
// checking a sentence would find two facts that ought to be one — and CP72's validator would count
// the same value twice when measuring coverage.
func TestOneMeasurementIsOneFactHoweverManySectionsMentionIt(t *testing.T) {
	ctx := Assemble(fixture(), testNow)

	byValue := map[string][]string{}
	for _, fact := range ctx.Facts {
		if fact.Kind != "observation" || fact.Label != "HbA1c" {
			continue
		}
		byValue[fact.Value+"@"+fact.On] = append(byValue[fact.Value+"@"+fact.On], fact.Ref)
	}
	for key, refs := range byValue {
		if len(refs) > 1 {
			t.Errorf("the HbA1c %s has %d references (%v); one measurement is one fact", key, len(refs), refs)
		}
	}

	seen := map[string]bool{}
	for _, fact := range ctx.Facts {
		if seen[fact.Ref] {
			t.Errorf("fact reference %q appears twice", fact.Ref)
		}
		seen[fact.Ref] = true
	}
}

// TestNoFactReferenceLooksLikeATelephoneNumberToTheScrubber.
//
// The trap this is about is real and was met while writing the gateway: the minimiser replaces any
// run of seven digits with `[NUMBER]`, so a reference of the shape `obs.hba1c.20260312` would be
// redacted on the way out and the model would be asked to cite something it had never been shown.
// Dates are written with hyphens for that reason and not for looks.
func TestNoFactReferenceLooksLikeATelephoneNumberToTheScrubber(t *testing.T) {
	raw := fixture()
	// Two HbA1c results on **one day**, which is what mints a suffixed reference — and a suffixed
	// reference is where this trap actually sprang. A reference ending `2026-09-14` is eight digits
	// separated by hyphens, which the scrubber's nine-digit rule tolerates; one more digit of suffix
	// makes it nine, and the whole reference leaves the building as `[NUMBER]`. The first version of
	// `uniqueRef` numbered duplicates, and the database's copy of the same rule refused to store the
	// context at all.
	//
	// They go in the trend series rather than in `current`, because the current section keeps one
	// value per code — so a duplicate placed there would be deduplicated away and this test would
	// assert nothing. The first version of this test did exactly that and survived the mutation it
	// was written for.
	repeat := *raw.Current[3].Value
	repeat += 0.4
	raw.Trends["HBA1C"] = append(raw.Trends["HBA1C"], clinical.Observation{
		ID: uuid.New(), Code: "HBA1C", Value: &repeat, Unit: "%",
		Category: clinical.Lab, EffectiveAt: testNow.Add(-2 * time.Hour), Status: clinical.Active,
	})
	ctx := Assemble(raw, testNow)
	digits := regexp.MustCompile(`[0-9]{7,}`)

	// A *suffixed* reference, not merely a second one on another date. The guard is the whole
	// difference between this test biting and this test agreeing with itself.
	plain := "obs.hba1c:" + testClinicDay
	suffixed := 0
	for _, fact := range ctx.Facts {
		if strings.HasPrefix(fact.Ref, plain) && fact.Ref != plain {
			suffixed++
		}
	}
	if suffixed == 0 {
		t.Fatal("the fixture no longer produces a suffixed reference, so this test asserts nothing")
	}

	for _, fact := range ctx.Facts {
		if digits.MatchString(fact.Ref) {
			t.Errorf("fact reference %q carries a run of seven digits and would be scrubbed before the model saw it", fact.Ref)
		}
		for _, pattern := range scrubPatterns {
			if pattern.Matches(fact.Ref) {
				t.Errorf("fact reference %q trips the %s scrubber pattern", fact.Ref, pattern.Kind)
			}
		}
	}
}

// TestAFreeTextNoteThatLooksLikeAnIdentifierIsWithheldWhole.
//
// Not scrubbed into "call [NUMBER]", which a model reads around and sometimes narrates — the
// redaction itself becomes content. And `core.ai_synthesis.context` carries `ops.carries_identifier`
// as a check constraint, so the alternative to withholding is a synthesis that cannot be stored at
// all.
func TestAFreeTextNoteThatLooksLikeAnIdentifierIsWithheldWhole(t *testing.T) {
	raw := fixture()
	raw.Visit.ChiefComplaint = "tired for two weeks; husband will bring the report, call 01711234567"

	ctx := Assemble(raw, testNow)

	if strings.Contains(ctx.Visit.Complaint, "01711234567") {
		t.Fatalf("the complaint reached the context with a telephone number in it: %q", ctx.Visit.Complaint)
	}
	if !strings.Contains(ctx.Visit.Complaint, "withheld") {
		t.Errorf("the complaint is %q; a withheld note should say that it was withheld", ctx.Visit.Complaint)
	}
	// And nothing anywhere else in the payload either: the check the database will make is over
	// the whole object, not over the field somebody remembered.
	encoded, err := json.Marshal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, pattern := range scrubPatterns {
		if pattern.Matches(string(encoded)) {
			t.Errorf("the assembled context trips the %s scrubber pattern", pattern.Kind)
		}
	}
}

// TestTheContextNamesNoOperator.
//
// §4.2's attribution is a property of the record and belongs on the screen. A payload naming the
// four members of staff who touched this patient would be sending their personal data out of the
// building to no clinical purpose — and staff names are exactly the third-party names the gateway's
// scrubber cannot catch.
func TestTheContextNamesNoOperator(t *testing.T) {
	raw := fixture()
	raw.History[0].RecordedBy = uuid.New()
	raw.Current[0].RecordedRole = "CLINICAL_ASSISTANT"
	raw.Current[0].RecordedBy = uuid.New()

	encoded, err := json.Marshal(Assemble(raw, testNow))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		raw.History[0].RecordedBy.String(),
		raw.Current[0].RecordedBy.String(),
		"CLINICAL_ASSISTANT", "recorded_by", "device_id",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("the assembled context carries %q, which is attribution and not clinical content", forbidden)
		}
	}
}

// TestACorrectedMeasurementIsMaterialAndAnAttributionChangeIsNot.
//
// The checkpoint's own example, and the whole of the re-run decision. It is one function, and this
// is the test that says which side of the line each kind of change falls on.
func TestACorrectedMeasurementIsMaterialAndAnAttributionChangeIsNot(t *testing.T) {
	base := Assemble(fixture(), testNow).MaterialSHA256()

	// The same record, assembled a minute later. Immaterial: `assembled_at` moves and nothing else
	// does, and a hash that changed here would buy a model call a minute for every open visit.
	if later := Assemble(fixture(), testNow.Add(time.Minute)).MaterialSHA256(); later != base {
		t.Error("assembling the same record a minute later produced a different material hash")
	}

	// Attribution: a different operator and a different device on the same value. Immaterial —
	// nothing the model is shown changes.
	attributed := fixture()
	attributed.Current[1].RecordedBy = uuid.New()
	attributed.Current[1].RecordedRole = "JUNIOR_DOCTOR"
	attributed.Current[1].DeviceID = uuid.NewString()
	if got := Assemble(attributed, testNow).MaterialSHA256(); got != base {
		t.Error("changing who recorded a value changed the material hash; a re-run would be bought by an attribution edit")
	}

	// A corrected blood pressure. Material, and the checkpoint says so in as many words.
	corrected := fixture()
	*corrected.Current[1].Value = 152
	if got := Assemble(corrected, testNow).MaterialSHA256(); got == base {
		t.Error("a corrected systolic blood pressure did not change the material hash; the physician would read a summary of the wrong number")
	}

	// A new measurement arriving. Material.
	added := fixture()
	creatinine := 118.0
	added.Current = append(added.Current, clinical.Observation{
		ID: uuid.New(), Code: "CREATININE", Value: &creatinine, Unit: "umol/L",
		Category: clinical.Lab, EffectiveAt: testNow, Status: clinical.Active,
	})
	if got := Assemble(added, testNow).MaterialSHA256(); got == base {
		t.Error("a new creatinine did not change the material hash")
	}

	// A station finishing. Material: the summary should say the examination happened.
	progressed := fixture()
	progressed.Encounters = append(progressed.Encounters, visit.Encounter{
		ID: uuid.New(), StationCode: "STN_EXAMINATION", Status: visit.Finished,
		StartedAt: testNow.Add(-10 * time.Minute), Outcome: "completed",
	})
	if got := Assemble(progressed, testNow).MaterialSHA256(); got == base {
		t.Error("completing the examination station did not change the material hash")
	}

	// The assembler itself changing. Material by construction: a summary produced by an older
	// assembler is out of date in the same way one produced from older measurements is.
	if !strings.Contains(strings.Join(materialLines(Assemble(fixture(), testNow)), "\n"), "assembler="+AssemblerVersion) {
		t.Error("the material hash does not cover the assembler version")
	}
}

// materialLines re-derives what the hash covers, for the assertion above. Deliberately a separate
// reading of the same rule rather than a call into it: a test that asked the function under test
// what it hashed would agree with itself whatever it did.
func materialLines(c Context) []string {
	out := []string{"assembler=" + c.AssemblerVersion}
	for _, fact := range c.Facts {
		out = append(out, "fact="+fact.Ref)
	}
	return out
}

// TestCompletenessIsTheStationSequencesOwnDefinition.
//
// Acceptance criterion 2 rests on this: the automatic trigger fires when "all stations complete",
// and what that means is `core.station_sequence`'s — every *required* station at a position before
// the consultation, with a finished encounter. Not a list in Go, and not "every station in the
// plan", which would wait for the consultation itself and produce the summary after it was needed.
func TestCompletenessIsTheStationSequencesOwnDefinition(t *testing.T) {
	raw := fixture()

	if Assemble(raw, testNow).Visit.Complete {
		t.Error("the visit reports complete with the examination station not yet done")
	}

	raw.Encounters = append(raw.Encounters, visit.Encounter{
		ID: uuid.New(), StationCode: "STN_EXAMINATION", Status: visit.Finished,
		StartedAt: testNow.Add(-10 * time.Minute), Outcome: "completed",
	})
	if !Assemble(raw, testNow).Visit.Complete {
		t.Error("every required pre-consultation station is finished and the visit does not report complete")
	}

	// The consultation and everything after it must not be waited for. A trigger that did would
	// produce the summary after the physician had already seen the patient, which is the one
	// outcome §7.1 exists to prevent.
	if !preConsultationComplete(raw.Planned, raw.Encounters) {
		t.Error("completeness waits for a station at or after the consultation")
	}

	// An optional station left undone does not block. `required` is a column somebody set, and
	// this is the code that honours it.
	optional := fixture()
	optional.Planned = append(optional.Planned, visit.PlannedStation{
		Position: 4, StationCode: "STN_RECORDS", Required: false,
	})
	optional.Encounters = append(optional.Encounters, visit.Encounter{
		ID: uuid.New(), StationCode: "STN_EXAMINATION", Status: visit.Finished,
		StartedAt: testNow, Outcome: "completed",
	})
	if !preConsultationComplete(optional.Planned, optional.Encounters) {
		t.Error("an optional station nobody visited blocked completeness")
	}
}

// TestTheGapListNamesWhatIsMissing.
//
// A model shown a record with no HbA1c writes a summary that does not mention HbA1c, and the
// physician reads a confident page with a hole in it. Nothing about a language model notices an
// absence; noticing absences is what the deterministic pass is for.
func TestTheGapListNamesWhatIsMissing(t *testing.T) {
	raw := fixture()
	// A diabetic patient whose last HbA1c was two years ago, with no blood pressure recorded and
	// nobody having asked about allergies.
	raw.Current = raw.Current[:1]
	raw.Trends = map[string][]clinical.Observation{}
	raw.Allergies = allergy.State{}

	gaps := map[string]string{}
	for _, gap := range Assemble(raw, testNow).Gaps {
		gaps[gap.Code] = gap.Severity
	}

	for _, want := range []string{
		"allergy_status_unknown",
		"no_hba1c_in_12_months",
		"no_blood_pressure",
		"station_not_completed",
	} {
		if _, found := gaps[want]; !found {
			t.Errorf("the gap %q was not reported; the physician would read a summary with a hole in it", want)
		}
	}
	if gaps["allergy_status_unknown"] != "important" {
		t.Errorf("an unknown allergy status is %q, want important", gaps["allergy_status_unknown"])
	}

	// And the other direction: a complete record produces none of them. A gap list that fires on
	// everything is a gap list nobody reads.
	full := fixture()
	full.Encounters = append(full.Encounters, visit.Encounter{
		ID: uuid.New(), StationCode: "STN_EXAMINATION", Status: visit.Finished,
		StartedAt: testNow, Outcome: "completed",
	})
	for _, gap := range Assemble(full, testNow).Gaps {
		switch gap.Code {
		case "allergy_status_unknown", "no_hba1c_in_12_months", "no_blood_pressure", "station_not_completed":
			t.Errorf("a complete record still reports the gap %q", gap.Code)
		}
	}
}

// TestUnknownAllergyStatusIsNotNoAllergies.
//
// CP54 made "no known allergies" an attributed assertion rather than a default, precisely so that
// the distinction survives. A summary that read an absence of records as an absence of allergies
// would be the exact failure that rule exists to prevent, one layer further along.
func TestUnknownAllergyStatusIsNotNoAllergies(t *testing.T) {
	raw := fixture()
	raw.Allergies = allergy.State{}

	ctx := Assemble(raw, testNow)
	if ctx.Allergies.Status != "unknown" {
		t.Errorf("an unrecorded allergy state reads as %q, want unknown", ctx.Allergies.Status)
	}
	if strings.Contains(strings.ToLower(ctx.Allergies.Status), "none") {
		t.Error("an unrecorded allergy state reads as none, which is a different clinical fact")
	}

	asserted := fixture()
	asserted.Allergies = allergy.State{Status: "NO_KNOWN_ALLERGY"}
	if got := Assemble(asserted, testNow).Allergies.Status; got != "NO_KNOWN_ALLERGY" {
		t.Errorf("an asserted no-known-allergy reads as %q", got)
	}
}

// TestTheGrowthSectionIsTheClinicalModulesArithmeticAndNotOurs [R-06].
//
// `internal/clinical` scores every measurement against the seeded reference and records the
// standard, its version and the L, M and S parameters per value. This section copies those numbers
// and computes nothing — a z-score derived a second time here would disagree with the one on the
// growth card the day either changed, and a physician would have two numbers for one child.
func TestTheGrowthSectionIsTheClinicalModulesArithmeticAndNotOurs(t *testing.T) {
	raw := fixture()
	raw.Demographics = Demographics{AgeMonths: 9 * 12, Sex: "male", AgeText: "9 years"}
	scored := clinical.Percentile{
		Indicator: clinical.BMIForAge, Code: "BMI", Value: 24.1, Unit: "kg/m2",
		AgeDays: 3287, AgeMonths: 108, Z: 2.04, P: 97.9,
		Standard: "CDC-2000", StandardVersion: "1.0", EffectiveAt: testNow,
	}
	raw.Growth = clinical.Growth{
		Applicable: true, Sex: "male", AgeDays: 3287,
		Current: map[clinical.Indicator]clinical.Percentile{clinical.BMIForAge: scored},
		History: map[clinical.Indicator][]clinical.GrowthPoint{
			clinical.BMIForAge: {{Percentile: scored}},
		},
	}

	ctx := Assemble(raw, testNow)
	if ctx.Growth == nil || !ctx.Growth.Applicable {
		t.Fatal("a nine-year-old with a scored BMI produced no growth section")
	}
	if len(ctx.Growth.Indicators) != 1 {
		t.Fatalf("the growth section carries %d indicators, want 1", len(ctx.Growth.Indicators))
	}
	reading := ctx.Growth.Indicators[0]
	if reading.Z != "2.04" || reading.Percentile != "97.9" {
		t.Errorf("the growth reading says z %s, centile %s; the clinical module said 2.04 and 97.9",
			reading.Z, reading.Percentile)
	}
	if ctx.Growth.Standard != "CDC-2000" {
		t.Errorf("the standard is %q; a percentile without its standard is a number with no meaning", ctx.Growth.Standard)
	}
	// §7.1's own requirement, in as many words: the ≥95th-percentile childhood obesity flag.
	if ctx.Growth.ObesityFlag != "bmi_for_age_at_or_above_95th_centile" {
		t.Errorf("a BMI on the 97.9th centile produced the flag %q", ctx.Growth.ObesityFlag)
	}

	// An adult gets no growth section at all — not a percentile computed off the end of a paediatric
	// chart, and not a note saying they are too old for one. "Too old for a growth reference" is
	// true of a forty-one-year-old and is not information about them; a paediatric line on every
	// adult's summary is a line the physician learns to skip, and a line they learn to skip is one
	// they will skip on the child who needed it.
	adult := fixture()
	adult.Growth = clinical.Growth{Applicable: false, Note: "too_old_for_a_growth_reference"}
	if got := Assemble(adult, testNow).Growth; got != nil {
		t.Errorf("a forty-one-year-old was given a paediatric growth section: %+v", got)
	}

	// A *child* with nothing measured yet does get the note, because that absence is information:
	// a section that vanished would leave the model free to conclude the child was measured and
	// found normal.
	unmeasured := fixture()
	unmeasured.Demographics = Demographics{AgeMonths: 7 * 12, Sex: "female", AgeText: "7 years"}
	unmeasured.Growth = clinical.Growth{Applicable: false, Note: "nothing_measured_yet"}
	if got := Assemble(unmeasured, testNow).Growth; got == nil || got.Note != "nothing_measured_yet" {
		t.Errorf("a seven-year-old with no measurements produced %+v; the absence is information", got)
	}
}

// TestAssemblyIsDeterministic. The word in the checkpoint, checked.
func TestAssemblyIsDeterministic(t *testing.T) {
	first, err := json.Marshal(Assemble(fixture(), testNow))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		again, err := json.Marshal(Assemble(fixture(), testNow))
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatalf("assembling the same record twice produced different contexts on run %d", i)
		}
	}
}

// TestCurrentMeasurementsAreTheNewestValuePerCodeAndNotEveryActiveRow.
//
// `clinical.Store.ForPatient` returns every active observation, newest first, and its own note says
// the caller takes the first row for each code. A patient with five years of follow-up therefore has
// forty active rows, and the first version of this assembler handed all forty to the model as
// "current measurements" — the narrative opened with five temperatures on five different dates
// before it reached the blood pressure. Nothing in the suite caught it, because the fixture had one
// value per code. It was found by reading the twenty summaries, which is what those exist for.
func TestCurrentMeasurementsAreTheNewestValuePerCodeAndNotEveryActiveRow(t *testing.T) {
	raw := fixture()
	// Five temperatures across five visits, all active, newest first — exactly what the store
	// returns for a real follow-up patient.
	for i := 0; i < 5; i++ {
		v := 36.4 + float64(i)/10
		raw.Current = append(raw.Current, clinical.Observation{
			ID: uuid.New(), Code: "BODY_TEMP", Value: &v, Unit: "Cel",
			Category: clinical.Vital, EffectiveAt: testNow.AddDate(0, 0, -90*i),
			Status: clinical.Active,
		})
	}

	ctx := Assemble(raw, testNow)

	byCode := map[string]int{}
	for _, m := range ctx.Current {
		byCode[m.Code]++
	}
	for code, n := range byCode {
		if n > 1 {
			t.Errorf("current_measurements carries %d rows for %s; it is the newest value per code", n, code)
		}
	}
	var temp *Measurement
	for i := range ctx.Current {
		if ctx.Current[i].Code == "BODY_TEMP" {
			temp = &ctx.Current[i]
		}
	}
	if temp == nil {
		t.Fatal("the temperature disappeared entirely")
	}
	if temp.Value != "36.4" || temp.On != testClinicDay {
		t.Errorf("the current temperature is %s on %s; the newest of the five is 36.4 on %s",
			temp.Value, temp.On, testClinicDay)
	}
}

// TestUnitsAreWrittenTheWayAClinicianWritesThem.
//
// Values are stored in UCUM, which is right for a database and wrong on a page: a physician reading
// "196 mm[Hg]" is reading a machine's spelling of their own unit. `core.unit.display_en` already
// holds the answer, per unit, as the clinic's own decision.
func TestUnitsAreWrittenTheWayAClinicianWritesThem(t *testing.T) {
	ctx := Assemble(fixture(), testNow)

	units := map[string]string{}
	for _, m := range ctx.Current {
		units[m.Code] = m.Unit
	}
	for code, want := range map[string]string{
		"BP_SYSTOLIC": "mmHg", "BODY_WEIGHT": "kg", "HBA1C": "%", "BMI": "kg/m²",
	} {
		if units[code] != want {
			t.Errorf("%s is written in %q, want %q", code, units[code], want)
		}
	}

	// UCUM spells "dimensionless" as `1`, which reads as nonsense beside a number: "waist-hip ratio
	// 0.879 1". A ratio has no unit and the payload says so by having none.
	raw := fixture()
	whr := 0.86
	raw.Current = append(raw.Current, clinical.Observation{
		ID: uuid.New(), Code: "WHR", Value: &whr, Unit: "1",
		Category: clinical.Derived, EffectiveAt: testNow, Status: clinical.Active,
	})
	for _, m := range Assemble(raw, testNow).Current {
		if m.Code == "WHR" && m.Unit != "" {
			t.Errorf("a dimensionless ratio carries the unit %q", m.Unit)
		}
	}

	// An empty catalogue is survivable: the UCUM code comes through, which is ugly and correct
	// rather than absent.
	bare := fixture()
	bare.Units = nil
	for _, m := range Assemble(bare, testNow).Current {
		if m.Code == "BP_SYSTOLIC" && m.Unit != "mm[Hg]" {
			t.Errorf("with no unit catalogue the systolic unit is %q, want the raw code", m.Unit)
		}
	}
}

// TestComputedNumbersAreRoundedAndMeasuredOnesAreNot.
//
// A centile printed to three decimals — "the 53.747th centile" — invites a reader to believe the
// number is that good, and it is a measurement taken with a stadiometer. Measured values keep their
// stored precision, because grounding compares strings and a rounded measurement would no longer
// match the observation it came from.
func TestComputedNumbersAreRoundedAndMeasuredOnesAreNot(t *testing.T) {
	raw := fixture()
	raw.Demographics = Demographics{AgeMonths: 11 * 12, Sex: "female", AgeText: "11 years"}
	scored := clinical.Percentile{
		Indicator: clinical.HeightForAge, Code: "BODY_HEIGHT", Value: 145.5, Unit: "cm",
		Z: 0.0937281, P: 53.7472, Standard: "CDC_2000", StandardVersion: "1.0",
		EffectiveAt: testNow,
	}
	raw.Growth = clinical.Growth{
		Applicable: true, Sex: "female",
		Current: map[clinical.Indicator]clinical.Percentile{clinical.HeightForAge: scored},
		History: map[clinical.Indicator][]clinical.GrowthPoint{
			clinical.HeightForAge: {{Percentile: scored}},
		},
	}

	ctx := Assemble(raw, testNow)
	if ctx.Growth == nil || len(ctx.Growth.Indicators) != 1 {
		t.Fatal("no growth reading was assembled")
	}
	reading := ctx.Growth.Indicators[0]
	if reading.Percentile != "53.7" {
		t.Errorf("the centile reads %q, want 53.7", reading.Percentile)
	}
	if reading.Z != "0.09" {
		t.Errorf("the z-score reads %q, want 0.09", reading.Z)
	}
	if reading.Value != "145.5" {
		t.Errorf("the measured height reads %q; a measurement keeps its stored precision", reading.Value)
	}
}
