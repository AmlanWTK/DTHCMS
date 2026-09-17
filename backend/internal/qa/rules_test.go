package qa_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinicalterm"
	"github.com/AmlanWTK/DTHCMS/backend/internal/qa"
)

// Each rule of `docs/qa-rules.md` §3, fired and not fired (CP83).
//
// # Why these are pure and why that is not a compromise
//
// `qa.Evidence` is a struct of facts and every predicate is a function from it. So each rule gets
// its pair — the file that should bounce and the file that should not — without a database, and
// the pair is the test: a rule tested only in its firing case passes against an implementation
// that fires always, which is the most expensive bug this station can have. A rule that fires
// always teaches the officer to override.
//
// The database half is `qa_db_test.go`, and it tests different things: that the rule table is
// what drives these, that the gate holds, and that an empty table clears rather than skips.
//
// # Rules 12 and 14 are seeded here on purpose
//
// Neither can fire in this clinic today — no counselling template defines
// `AGRANULOCYTOSIS_WARNING`, and no molecule is flagged teratogenic. `TestRule12` and `TestRule14`
// seed the evidence so the predicate itself is covered, which is the whole point: the day
// somebody writes the reference data, the code they are switching on has been exercised.

func at(daysAgo int) time.Time {
	return now.AddDate(0, 0, -daysAgo)
}

var now = time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)

// base is a file with nothing wrong with it: a diabetic on metformin, seen today, with
// everything a rule could ask for present.
func base() qa.Evidence {
	asserted := true
	age := 58.0
	return qa.Evidence{
		Now:            now,
		PrescriptionID: uuid.New(), PatientID: uuid.New(), VisitID: uuid.New(),
		Lines: []qa.Line{{
			ItemID: uuid.New(), Label: "Comet 500", Generic: "Metformin hydrochloride",
			ClassCode: "BIGUANIDE", Dose: "1 tablet", DoseUnit: "mg",
		}},
		AgeYears: &age, Sex: "female", PregnancyThisVisit: true,
		AllergyAsserted: &asserted,
		DiagnosisCodes:  []string{"E11.9"},
		CodedThisVisit:  true,
		Latest: map[string]qa.ObservationFact{
			"HBA1C":              {Code: "HBA1C", EffectiveAt: at(30)},
			"BP_SYSTOLIC":        {Code: "BP_SYSTOLIC", EffectiveAt: now, InThisVisit: true},
			"BODY_WEIGHT":        {Code: "BODY_WEIGHT", EffectiveAt: now, InThisVisit: true},
			"MONOFILAMENT_LEFT":  {Code: "MONOFILAMENT_LEFT", EffectiveAt: at(60)},
			"RETINOPATHY_SCREEN": {Code: "RETINOPATHY_SCREEN", EffectiveAt: at(60)},
			"CHOL_LDL":           {Code: "CHOL_LDL", EffectiveAt: at(60)},
			"TSH":                {Code: "TSH", EffectiveAt: at(60)},
			"WBC":                {Code: "WBC", EffectiveAt: at(60)},
		},
		Orders:              map[string]qa.OrderFact{},
		CounselingCovered:   map[string]bool{},
		TeratogenicGenerics: map[string]bool{},
		EducationThisVisit:  true,
		RenalWindowDays:     180,
		StationNames: map[string][2]string{
			"STN_CONSULTATION": {"Physician Consultation", "চিকিৎসকের পরামর্শ"},
			"STN_HISTORY":      {"Medical History", "রোগের ইতিহাস"},
			"STN_EXAMINATION":  {"Clinical Examination & Vitals", "শারীরিক পরীক্ষা ও ভাইটাল"},
		},
		Stations: []qa.StationFact{
			{Code: "STN_ANTHROPOMETRY", NameEN: "Anthropometry", NameBN: "দেহমাপ",
				Mandatory: true, Recorded: true},
		},
		// The clinic's real display names, so these tests assert against what the officer
		// actually reads rather than against `clinicalterm`'s spelled fallback. A test written
		// around the fallback would pass on the day the catalogue lost a row.
		Terms: clinicalterm.New(
			clinicalterm.Term{Code: "HBA1C", EN: "HbA1c", BN: "এইচবিএ১সি"},
			clinicalterm.Term{Code: "BP_SYSTOLIC", EN: "Systolic blood pressure", BN: "সিস্টোলিক রক্তচাপ"},
			clinicalterm.Term{Code: "BP_DIASTOLIC", EN: "Diastolic blood pressure", BN: "ডায়াস্টোলিক রক্তচাপ"},
			clinicalterm.Term{Code: "BODY_WEIGHT", EN: "Weight", BN: "ওজন"},
			clinicalterm.Term{Code: "MONOFILAMENT_LEFT", EN: "Monofilament, left foot", BN: "মনোফিলামেন্ট, বাঁ পা"},
			clinicalterm.Term{Code: "MONOFILAMENT_RIGHT", EN: "Monofilament, right foot", BN: "মনোফিলামেন্ট, ডান পা"},
			clinicalterm.Term{Code: "RETINOPATHY_SCREEN", EN: "Retinopathy screening status", BN: "রেটিনোপ্যাথি স্ক্রিনিং অবস্থা"},
			clinicalterm.Term{Code: "CHOL_LDL", EN: "LDL cholesterol", BN: "এলডিএল"},
			clinicalterm.Term{Code: "CHOL_TOTAL", EN: "Total cholesterol", BN: "মোট কোলেস্টেরল"},
			clinicalterm.Term{Code: "TSH", EN: "TSH", BN: "টিএসএইচ"},
			clinicalterm.Term{Code: "WBC", EN: "White cell count", BN: "শ্বেত রক্তকণিকা"},
		),
	}
}

func rule(kind qa.Kind, severity qa.Severity, params qa.Params, window *int) qa.Rule {
	return qa.Rule{
		ID: uuid.New(), Code: "TEST_" + string(kind), Kind: kind, Sev: severity,
		BounceStation: "STN_CONSULTATION", WindowDays: window, Params: params, Enabled: true,
		TitleEN: "test rule", TitleBN: "পরীক্ষামূলক নিয়ম",
	}
}

func days(n int) *int { return &n }

// fires asserts a rule produces at least one finding, and that the finding says something in
// both languages — a finding with an empty Bengali sentence has not appeared for half the floor.
func fires(t *testing.T, r qa.Rule, e qa.Evidence, wantSubject string) {
	t.Helper()
	found := r.Evaluate(e)
	if len(found) == 0 {
		t.Fatalf("%s did not fire and should have", r.Code)
	}
	for _, f := range found {
		if strings.TrimSpace(f.TitleEN) == "" || strings.TrimSpace(f.TitleBN) == "" {
			t.Errorf("%s produced a finding missing one of its two languages: %+v", r.Code, f)
		}
		if strings.TrimSpace(f.ReasonBN()) == strings.TrimSpace(f.ReasonEN()) {
			t.Errorf("%s produced the same sentence in both languages: %q", r.Code, f.ReasonEN())
		}
	}
	if wantSubject != "" && !strings.Contains(found[0].SubjectEN, wantSubject) {
		t.Errorf("%s: subject %q does not name %q — a finding that does not say which line "+
			"leaves the physician deleting the wrong drug", r.Code, found[0].SubjectEN, wantSubject)
	}
}

func silent(t *testing.T, r qa.Rule, e qa.Evidence) {
	t.Helper()
	if found := r.Evaluate(e); len(found) > 0 {
		t.Fatalf("%s fired on a file that satisfies it: %+v", r.Code, found)
	}
}

// ---------------------------------------------------------------------------
// §3.1 Safety
// ---------------------------------------------------------------------------

func TestRule1UnresolvedInteraction(t *testing.T) {
	r := rule(qa.KindSafetyEngineFinding, qa.SeverityBlock,
		qa.Params{MinSeverity: "BLOCK", RuleTypes: []string{"INTERACTION"}}, nil)

	e := base()
	silent(t, r, e)

	e.Safety = []qa.SafetyFinding{{
		RuleCode: "INT_MET_CONTRAST", RuleType: "INTERACTION", Severity: "BLOCK",
		MessageEN: "Metformin with iodinated contrast", MessageBN: "মেটফরমিন ও আয়োডিনযুক্ত কনট্রাস্ট",
		Subject: "Comet 500",
	}}
	fires(t, r, e, "Comet 500")

	// A WARN from the engine does not satisfy a rule that asked for BLOCKs. The severity the
	// *engine* assigns and the severity this station assigns are different judgements, and
	// min_severity is what selects between them.
	e.Safety[0].Severity = "WARN"
	silent(t, r, e)

	// Nor does a finding of the wrong type: rule 1 is about interactions and duplicates, and
	// rule 3 is about allergies, and they are two rows that bounce to the same station for
	// different reasons.
	e.Safety[0].Severity = "BLOCK"
	e.Safety[0].RuleType = "ALLERGY"
	silent(t, r, e)
}

func TestRule2AllergyStatusIsTriState(t *testing.T) {
	r := rule(qa.KindAllergyStatusAsserted, qa.SeverityBlock, qa.Params{}, nil)

	e := base()
	silent(t, r, e)

	// **The case this rule exists for.** Nil is nobody having asked; a pointer to false is
	// somebody having asked and found none. A bool would have collapsed them and turned a BLOCK
	// into a silent pass.
	e.AllergyAsserted = nil
	fires(t, r, e, "")

	none := false
	e.AllergyAsserted = &none
	silent(t, r, e)
}

func TestRule3AllergyConflict(t *testing.T) {
	r := rule(qa.KindSafetyEngineFinding, qa.SeverityBlock,
		qa.Params{MinSeverity: "BLOCK", RuleTypes: []string{"ALLERGY"}}, nil)

	e := base()
	silent(t, r, e)

	e.Safety = []qa.SafetyFinding{{
		RuleCode: "ALG_PENICILLIN", RuleType: "ALLERGY", Severity: "BLOCK",
		MessageEN: "The patient reports a penicillin allergy",
		MessageBN: "রোগী পেনিসিলিনে অ্যালার্জির কথা বলেছেন", Subject: "Amoxil 500",
	}}
	fires(t, r, e, "Amoxil 500")
}

// ---------------------------------------------------------------------------
// §3.2 Diabetes
// ---------------------------------------------------------------------------

func TestRule4HbA1cRecordedOrOrdered(t *testing.T) {
	r := rule(qa.KindObservationRecent, qa.SeverityBlock, qa.Params{
		Codes: []string{"HBA1C"}, AcceptOrdered: true,
		WhenDiagnosis: []string{"E10", "E11"},
	}, days(180))

	e := base()
	silent(t, r, e)

	// An HbA1c from eight months ago is not an HbA1c.
	e.Latest["HBA1C"] = qa.ObservationFact{Code: "HBA1C", EffectiveAt: at(240)}
	fires(t, r, e, "no HbA1c in the last six months")

	// **The plan's named manual check.** Ordering it satisfies the rule, because a consultant
	// who ordered the test has done the right thing and the lab's turnaround is not his.
	e.Orders["HBA1C"] = qa.OrderFact{Code: "HBA1C", OrderedAt: now}
	silent(t, r, e)

	// An order from eight months ago that nobody ever resulted is not an order either. This is
	// the judgement that makes "or ordered" mean what it says rather than being a way past the
	// rule forever.
	e.Orders["HBA1C"] = qa.OrderFact{Code: "HBA1C", OrderedAt: at(240)}
	fires(t, r, e, "no HbA1c in the last six months")

	// A patient who is not diabetic is not asked. The trigger is a column, which is what makes
	// "the same rule for hypertension" a row rather than a release.
	e.DiagnosisCodes = []string{"I10"}
	silent(t, r, e)

	// ICD-10 is a tree and the trigger matches by prefix: E11.22 is still type 2 diabetes.
	e.DiagnosisCodes = []string{"E11.22"}
	fires(t, r, e, "no HbA1c in the last six months")
}

func TestRule5BloodPressureThisVisit(t *testing.T) {
	r := rule(qa.KindObservationThisVisit, qa.SeverityBlock, qa.Params{
		Codes: []string{"BP_SYSTOLIC", "BP_DIASTOLIC"}, WhenDiagnosis: []string{"E11"},
	}, nil)
	// Two codes, one cuff. The phrase is on the rule because only the rule knows that a systolic
	// and a diastolic are one reading; the database refuses a multi-code rule without it.
	r.LooksForEN, r.LooksForBN = "blood pressure", "রক্তচাপ"

	e := base()
	silent(t, r, e)

	// **A blood pressure from last month does not satisfy "this visit".** That distinction is
	// the whole of rule 5: the number is thirty seconds away at a station the patient walked
	// past, and yesterday's reading belongs to yesterday's file.
	e.Latest["BP_SYSTOLIC"] = qa.ObservationFact{Code: "BP_SYSTOLIC", EffectiveAt: at(30)}
	fires(t, r, e, "no blood pressure recorded during this visit")

	// Either half of the pair satisfies it: a lab reports what it reports, and so does a cuff.
	e.Latest["BP_DIASTOLIC"] = qa.ObservationFact{
		Code: "BP_DIASTOLIC", EffectiveAt: now, InThisVisit: true}
	silent(t, r, e)
}

func TestRule6RenalWindowComesFromCP79(t *testing.T) {
	r := rule(qa.KindRenalWindow, qa.SeverityBlock, qa.Params{}, nil)

	e := base()
	silent(t, r, e)

	e.RenalBlocking = []string{"Comet 500"}
	fires(t, r, e, "Comet 500")

	// The window in the sentence is the facility's, not a constant: change the policy and the
	// finding says something different, which is what makes CP79's table the answer to "how old
	// is too old".
	e.RenalWindowDays = 365
	found := r.Evaluate(e)
	if !strings.Contains(found[0].SubjectEN, "the last year") {
		t.Fatalf("the finding quotes a window that is not the facility's: %q", found[0].SubjectEN)
	}

	// Two renally-dosed drugs on one sheet are two findings. One finding naming one of them
	// leaves the physician deleting the wrong drug.
	e.RenalBlocking = []string{"Comet 500", "Forxiga 10"}
	if found := r.Evaluate(e); len(found) != 2 {
		t.Fatalf("two blocked drugs produced %d findings", len(found))
	}
}

func TestRule7FootExaminationIsAWarning(t *testing.T) {
	r := rule(qa.KindObservationRecent, qa.SeverityWarn, qa.Params{
		Codes:         []string{"MONOFILAMENT_LEFT", "MONOFILAMENT_RIGHT"},
		WhenDiagnosis: []string{"E11"},
	}, days(365))
	r.BounceStation = "STN_EXAMINATION"
	r.LooksForEN, r.LooksForBN = "foot sensation test", "পায়ের অনুভূতি পরীক্ষা"

	e := base()
	silent(t, r, e)

	e.Latest["MONOFILAMENT_LEFT"] = qa.ObservationFact{
		Code: "MONOFILAMENT_LEFT", EffectiveAt: at(400)}
	fires(t, r, e, "no foot sensation test in the last year")

	if found := r.Evaluate(e); found[0].Sev.Blocks() {
		t.Fatal("rule 7 is a WARN: a foot examination the patient can have next month does not " +
			"send them back up the corridor today")
	}
}

func TestRule8And9ScreeningWarnings(t *testing.T) {
	eye := rule(qa.KindObservationRecent, qa.SeverityWarn, qa.Params{
		Codes: []string{"RETINOPATHY_SCREEN"}, AcceptOrdered: true,
		WhenDiagnosis: []string{"E11"},
	}, days(365))
	lipids := rule(qa.KindObservationRecent, qa.SeverityWarn, qa.Params{
		Codes: []string{"CHOL_LDL", "CHOL_TOTAL"}, AcceptOrdered: true,
		WhenDiagnosis: []string{"E11"},
	}, days(365))
	lipids.LooksForEN, lipids.LooksForBN = "lipid profile", "লিপিড প্রোফাইল"

	e := base()
	silent(t, eye, e)
	silent(t, lipids, e)

	e.Latest["RETINOPATHY_SCREEN"] = qa.ObservationFact{
		Code: "RETINOPATHY_SCREEN", EffectiveAt: at(400)}
	delete(e.Latest, "CHOL_LDL")
	// The eye rule names one code and lets the catalogue answer; the lipid rule names four parts
	// of one test and says what the test is. Both paths, in one assertion each.
	fires(t, eye, e, "no Retinopathy screening status in the last year")
	fires(t, lipids, e, "no lipid profile in the last year")

	// A total cholesterol satisfies the lipid rule even with no LDL. Requiring all four would
	// block on a panel that came back without HDL, which is what panels do.
	e.Latest["CHOL_TOTAL"] = qa.ObservationFact{Code: "CHOL_TOTAL", EffectiveAt: at(10)}
	silent(t, lipids, e)
}

func TestRule10InsulinNeedsTheEducationStation(t *testing.T) {
	r := rule(qa.KindEducationThisVisit, qa.SeverityBlock, qa.Params{
		WhenClasses: []string{"INSULIN_LONG_ACTING_ANALOGUE", "GLP_1_RECEPTOR_AGONIST"},
	}, nil)
	r.BounceStation = "STN_RX_EDUCATION"

	e := base()
	// Metformin is not a pen, so the rule does not apply however little education happened.
	e.EducationThisVisit = false
	silent(t, r, e)

	e.Lines = append(e.Lines, qa.Line{
		ItemID: uuid.New(), Label: "Lantus 100 IU/mL", Generic: "Insulin glargine",
		ClassCode: "INSULIN_LONG_ACTING_ANALOGUE",
	})
	fires(t, r, e, "Lantus")

	e.EducationThisVisit = true
	silent(t, r, e)
}

// ---------------------------------------------------------------------------
// §3.3 Thyroid
// ---------------------------------------------------------------------------

func TestRule11TSHOnAThyroidDrug(t *testing.T) {
	r := rule(qa.KindObservationRecent, qa.SeverityBlock, qa.Params{
		Codes: []string{"TSH"}, AcceptOrdered: true,
		WhenClasses: []string{"THYROID_HORMONE", "ANTITHYROID"},
	}, days(180))

	e := base()
	// No thyroid drug on the sheet: the rule does not apply, even with no TSH at all.
	delete(e.Latest, "TSH")
	silent(t, r, e)

	e.Lines = append(e.Lines, qa.Line{
		ItemID: uuid.New(), Label: "Thyrox 50", Generic: "Levothyroxine sodium",
		ClassCode: "THYROID_HORMONE",
	})
	fires(t, r, e, "no TSH in the last six months")

	e.Latest["TSH"] = qa.ObservationFact{Code: "TSH", EffectiveAt: at(20)}
	silent(t, r, e)
}

// TestRule12 covers the predicate for a rule that is inert in this clinic today.
//
// No counselling template defines `AGRANULOCYTOSIS_WARNING` — invariant 137 says so on every
// `migrate verify` — so this rule fires for nothing in production. The evidence is seeded here so
// that the code is exercised anyway: the day Dr Nahid adds the item, the predicate behind it has
// been tested rather than written in a hurry.
func TestRule12AgranulocytosisCounselling(t *testing.T) {
	r := rule(qa.KindCounselingItemCovered, qa.SeverityBlock, qa.Params{
		ItemCodes: []string{"AGRANULOCYTOSIS_WARNING"}, WhenClasses: []string{"ANTITHYROID"},
	}, nil)
	r.BounceStation = "STN_COUNSELING"
	// A counselling item has no short name anywhere — `core.counseling_item.text_en` is the
	// script the counsellor reads aloud — so the rule is the only place this phrase can live.
	r.LooksForEN, r.LooksForBN = "agranulocytosis warning", "অ্যাগ্রানুলোসাইটোসিসের সতর্কবার্তা"

	e := base()
	silent(t, r, e)

	e.Lines = append(e.Lines, qa.Line{
		ItemID: uuid.New(), Label: "Neo-Mercazole 5", Generic: "Carbimazole",
		ClassCode: "ANTITHYROID",
	})
	fires(t, r, e, "the agranulocytosis warning has not been covered")

	e.CounselingCovered["AGRANULOCYTOSIS_WARNING"] = true
	silent(t, r, e)
}

func TestRule13BaselineFullBloodCount(t *testing.T) {
	r := rule(qa.KindObservationRecent, qa.SeverityWarn, qa.Params{
		Codes: []string{"WBC"}, AcceptOrdered: true, WhenClasses: []string{"ANTITHYROID"},
	}, days(180))
	// One code, and a phrase anyway: the catalogue calls WBC a "White cell count", and the
	// officer sending a patient to the lab needs the name of the test that is ordered.
	r.LooksForEN, r.LooksForBN = "full blood count", "সম্পূর্ণ রক্ত পরীক্ষা (সিবিসি)"

	e := base()
	e.Lines = append(e.Lines, qa.Line{
		ItemID: uuid.New(), Label: "Neo-Mercazole 5", Generic: "Carbimazole",
		ClassCode: "ANTITHYROID",
	})
	silent(t, r, e)

	delete(e.Latest, "WBC")
	fires(t, r, e, "no full blood count in the last six months")

	e.Orders["WBC"] = qa.OrderFact{Code: "WBC", OrderedAt: now}
	silent(t, r, e)
}

// ---------------------------------------------------------------------------
// §3.4 The rule that fires for nothing
// ---------------------------------------------------------------------------

// TestRule14 seeds the teratogenic flag, which no molecule carries in production.
//
// `docs/qa-rules.md` §3.4 is explicit that this rule fires for nothing until Dr Nahid writes the
// list. That state is reported by invariant 137 rather than left silent, and it is not a reason
// to leave the predicate untested.
func TestRule14TeratogenNeedsAPregnancyStatus(t *testing.T) {
	min, max := 15, 49
	r := rule(qa.KindTeratogenPregnancyStatus, qa.SeverityBlock,
		qa.Params{AgeMin: &min, AgeMax: &max}, nil)
	r.BounceStation = "STN_HISTORY"

	e := base()
	e.Lines = []qa.Line{{
		ItemID: uuid.New(), Label: "Neo-Mercazole 5", Generic: "Carbimazole",
		ClassCode: "ANTITHYROID",
	}}

	// A woman of thirty-four: inside the band, which is what makes the rest of this test about
	// the flag rather than about the age.
	inBand := 34.0
	e.AgeYears = &inBand

	// The production state: nothing is flagged, so nothing fires however incomplete the file is.
	e.PregnancyThisVisit = false
	e.TeratogenicGenerics = map[string]bool{}
	silent(t, r, e)

	// With the flag written, it fires.
	e.TeratogenicGenerics = map[string]bool{"carbimazole": true}
	fires(t, r, e, "Neo-Mercazole")

	// And the remediation exists, which it did not before migration 00071 added the observation
	// code: recording a pregnancy status at the history station satisfies it. Without somewhere
	// for the answer to go, this rule would have been a block with no way out but the override.
	e.PregnancyThisVisit = true
	silent(t, r, e)

	// Outside the band, and a man, and both are silent.
	e.PregnancyThisVisit = false
	age := 62.0
	e.AgeYears = &age
	silent(t, r, e)
	age = 30
	e.AgeYears = &age
	e.Sex = "male"
	silent(t, r, e)

	// **An unrecorded age fires.** A woman whose date of birth nobody wrote down might be
	// thirty, and the cost of asking is a question. Excluding on an absent fact is how a
	// fail-closed rule becomes fail-open.
	e.Sex = "female"
	e.AgeYears = nil
	fires(t, r, e, "Neo-Mercazole")
}

// ---------------------------------------------------------------------------
// §3.5 Completeness
// ---------------------------------------------------------------------------

func TestRule15TheVisitMustBeCoded(t *testing.T) {
	r := rule(qa.KindDiagnosisCodedThisVisit, qa.SeverityBlock, qa.Params{}, nil)

	e := base()
	silent(t, r, e)

	// **The surprising half.** A patient known to be diabetic for six years still fails this if
	// today's visit carries no code: `DiagnosisCodes` is what the patient carries and
	// `CodedThisVisit` is what today says, and §12's research reads the second.
	e.CodedThisVisit = false
	if len(e.DiagnosisCodes) == 0 {
		t.Fatal("this test is meaningless unless the patient carries a diagnosis already")
	}
	fires(t, r, e, "")
}

func TestRule16CounsellingChecklist(t *testing.T) {
	r := rule(qa.KindCounselingComplete, qa.SeverityBlock, qa.Params{}, nil)
	r.BounceStation = "STN_COUNSELING"

	e := base()
	silent(t, r, e)

	e.CounselingOutstanding = []qa.Term{
		{Code: "INSULIN_TECHNIQUE", EN: "Insulin injection sites and technique", BN: "ইনসুলিন দেওয়ার স্থান ও পদ্ধতি"},
		{Code: "DIET", EN: "Food habits and diet counseling", BN: "খাদ্যাভ্যাস ও ডায়েট কাউন্সেলিং"},
	}
	// The checklist's own words, not its item codes: the counsellor at station 3 and the officer
	// at station 10 have to be looking at the same sentence for the bounce to be actionable.
	fires(t, r, e, "Insulin injection sites and technique")
}

func TestRule17MandatoryStationBouncesToItself(t *testing.T) {
	r := rule(qa.KindMandatoryStationData, qa.SeverityWarn, qa.Params{}, nil)
	// The one rule with no station of its own: the finding names the station that recorded
	// nothing, which is why `core.qa_rule` lets a WARN carry none.
	r.BounceStation = ""

	e := base()
	silent(t, r, e)

	e.Stations = []qa.StationFact{
		{Code: "STN_ANTHROPOMETRY", NameEN: "Anthropometry", NameBN: "দেহমাপ",
			Mandatory: true, Recorded: false},
		{Code: "STN_NUTRITION", NameEN: "Nutrition", NameBN: "পুষ্টি",
			Mandatory: false, Recorded: false},
	}
	found := r.Evaluate(e)
	if len(found) != 1 {
		t.Fatalf("one mandatory station recorded nothing, and the rule produced %d findings", len(found))
	}
	if found[0].BounceStation != "STN_ANTHROPOMETRY" {
		t.Fatalf("rule 17 bounced to %q rather than to the station that recorded nothing",
			found[0].BounceStation)
	}
	if found[0].StationBN != "দেহমাপ" {
		t.Fatalf("the bounce does not carry the room's Bengali name: %q", found[0].StationBN)
	}
}

func TestRule18WeightBasedDose(t *testing.T) {
	r := rule(qa.KindWeightForWeightBasedDose, qa.SeverityBlock,
		qa.Params{Codes: []string{"BODY_WEIGHT"}}, nil)
	r.BounceStation = "STN_ANTHROPOMETRY"

	e := base()
	silent(t, r, e)

	// A weight-based line with no weight this visit.
	e.Lines = []qa.Line{{
		ItemID: uuid.New(), Label: "Enoxaparin 40", Generic: "Enoxaparin sodium",
		Dose: "1 mg/kg", DoseUnit: "mg",
	}}
	delete(e.Latest, "BODY_WEIGHT")
	fires(t, r, e, "Enoxaparin 40")

	// Last year's weight does not satisfy it: a dose per kilogram against a number that has
	// moved is a dose against a number that has moved.
	e.Latest["BODY_WEIGHT"] = qa.ObservationFact{Code: "BODY_WEIGHT", EffectiveAt: at(300)}
	fires(t, r, e, "Enoxaparin 40")

	e.Latest["BODY_WEIGHT"] = qa.ObservationFact{
		Code: "BODY_WEIGHT", EffectiveAt: now, InThisVisit: true}
	silent(t, r, e)

	// A line that is not weight-based never fires it, whatever the weight situation.
	delete(e.Latest, "BODY_WEIGHT")
	e.Lines = []qa.Line{{ItemID: uuid.New(), Label: "Comet 500", Dose: "1 tablet"}}
	silent(t, r, e)
}

// ---------------------------------------------------------------------------
// The empty table, and the severities
// ---------------------------------------------------------------------------

// TestAnEmptyRuleTableClearsEverythingAndSaysSo is `docs/qa-rules.md` §5, at the engine level.
//
// The database half — that clearance is still *required* — is `qa_db_test.go`. This half is the
// other one: that an empty table produces a clean review whose summary says **nothing was
// checked**, rather than a clean review indistinguishable from a file that passed eighteen rules.
func TestAnEmptyRuleTableClearsEverythingAndSaysSo(t *testing.T) {
	set, err := qa.NewRuleset(nil)
	if err != nil {
		t.Fatal(err)
	}
	review := set.Evaluate(base())

	if len(review.Findings) != 0 || review.RulesLive != 0 {
		t.Fatalf("an empty rule table found something: %+v", review)
	}
	if !review.CanClear() {
		t.Fatal("an empty rule table must clear: an empty checklist is a clinic with nothing " +
			"to check, not a clinic that cannot clear anything")
	}
	for _, summary := range []string{review.SummaryEN(), review.SummaryBN()} {
		if summary == "" {
			t.Fatal("the empty case has no summary in one of the two languages")
		}
	}
	if !strings.Contains(review.SummaryEN(), "nothing was checked") {
		t.Fatalf("the empty summary does not say that nothing was checked: %q", review.SummaryEN())
	}
	if !strings.Contains(review.SummaryEN(), "Clearance is still required") {
		t.Fatalf("the empty summary does not say clearance is still required, which is the "+
			"whole of the §5 distinction: %q", review.SummaryEN())
	}

	// And a file that passed real rules says something different, so the two cannot be confused
	// on a screen.
	full, err := qa.NewRuleset([]qa.Rule{
		rule(qa.KindAllergyStatusAsserted, qa.SeverityBlock, qa.Params{}, nil)})
	if err != nil {
		t.Fatal(err)
	}
	passed := full.Evaluate(base())
	if passed.SummaryEN() == review.SummaryEN() {
		t.Fatal("a checked clean file and an unchecked file read identically")
	}
}

// TestAWarnClearsAndABlockDoesNot is §2's table, asserted as behaviour.
func TestAWarnClearsAndABlockDoesNot(t *testing.T) {
	warn := rule(qa.KindAllergyStatusAsserted, qa.SeverityWarn, qa.Params{}, nil)
	block := rule(qa.KindAllergyStatusAsserted, qa.SeverityBlock, qa.Params{}, nil)
	block.Code = "BLOCKING"

	e := base()
	e.AllergyAsserted = nil

	set, err := qa.NewRuleset([]qa.Rule{warn})
	if err != nil {
		t.Fatal(err)
	}
	review := set.Evaluate(e)
	if len(review.Warnings()) != 1 || len(review.Blocking()) != 0 {
		t.Fatalf("a WARN rule produced %+v", review.Findings)
	}
	if !review.CanClear() {
		t.Fatal("a WARN does not stop a clearance: §2 says it clears with an acknowledgement")
	}

	set, err = qa.NewRuleset([]qa.Rule{block})
	if err != nil {
		t.Fatal(err)
	}
	review = set.Evaluate(e)
	if review.CanClear() {
		t.Fatal("a BLOCK stopped nothing")
	}

	// **Severity is a row.** The same rule, the same evidence, a different `severity` column:
	// that is criterion 5 at its sharpest, because moving rule 10 from BLOCK to WARN is the
	// change `docs/qa-rules.md` §3.2 says might be needed and must not be a release.
	if warn.Kind != block.Kind {
		t.Fatal("this test only means something if the two rules differ in severity alone")
	}
}

// TestBlocksSortAboveWarnings: the officer reads top-down with somebody waiting.
func TestBlocksSortAboveWarnings(t *testing.T) {
	warn := rule(qa.KindCounselingComplete, qa.SeverityWarn, qa.Params{}, nil)
	warn.Code = "WARN_RULE"
	warn.Ordering = 1
	block := rule(qa.KindAllergyStatusAsserted, qa.SeverityBlock, qa.Params{}, nil)
	block.Code = "BLOCK_RULE"
	block.Ordering = 99

	set, err := qa.NewRuleset([]qa.Rule{warn, block})
	if err != nil {
		t.Fatal(err)
	}
	e := base()
	e.AllergyAsserted = nil
	e.CounselingOutstanding = []qa.Term{{Code: "DIET", EN: "Food habits and diet counseling", BN: "খাদ্যাভ্যাস ও ডায়েট কাউন্সেলিং"}}

	review := set.Evaluate(e)
	if len(review.Findings) != 2 {
		t.Fatalf("expected two findings, got %d", len(review.Findings))
	}
	if review.Findings[0].RuleCode != "BLOCK_RULE" {
		t.Fatalf("a warning sorted above a block, despite a lower `ordering`: %+v", review.Findings)
	}
}

// ---------------------------------------------------------------------------
// The rules that cannot ask their own question
// ---------------------------------------------------------------------------

// TestAnUnaskableRuleIsRefusedRatherThanSkipped is the failure `docs/qa-rules.md` §1 calls worse
// than having no station: a rule that is present, enabled and permanently silent.
func TestAnUnaskableRuleIsRefusedRatherThanSkipped(t *testing.T) {
	cases := map[string]qa.Rule{
		"a recency rule with no codes": rule(qa.KindObservationRecent, qa.SeverityBlock,
			qa.Params{}, days(180)),
		"a recency rule with no window": rule(qa.KindObservationRecent, qa.SeverityBlock,
			qa.Params{Codes: []string{"HBA1C"}}, nil),
		"a counselling rule naming no item": rule(qa.KindCounselingItemCovered,
			qa.SeverityBlock, qa.Params{}, nil),
		"a safety rule with a severity nobody defined": rule(qa.KindSafetyEngineFinding,
			qa.SeverityBlock, qa.Params{MinSeverity: "CRITICAL"}, nil),
		"an age band that runs backwards": func() qa.Rule {
			min, max := 49, 15
			return rule(qa.KindTeratogenPregnancyStatus, qa.SeverityBlock,
				qa.Params{AgeMin: &min, AgeMax: &max}, nil)
		}(),
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			if err := r.Validate(); err == nil {
				t.Fatal("accepted a rule that can never fire")
			}
			if _, err := qa.NewRuleset([]qa.Rule{r}); err == nil {
				t.Fatal("NewRuleset accepted it, which would leave it enabled and silent")
			}
		})
	}

	// A BLOCK with no station is refused too: "incomplete" without "whose" is a rule that
	// stalls, and it is a CHECK constraint as well.
	noStation := rule(qa.KindAllergyStatusAsserted, qa.SeverityBlock, qa.Params{}, nil)
	noStation.BounceStation = ""
	if err := noStation.Validate(); err == nil {
		t.Fatal("a BLOCK with nowhere to bounce to was accepted")
	}

	// A retired rule is skipped rather than refused. Retiring one is how Dr Nahid turns a rule
	// off, and a retired rule that failed validation would make the whole ruleset unloadable.
	retired := rule(qa.KindObservationRecent, qa.SeverityBlock, qa.Params{}, nil)
	stamp := now
	retired.RetiredAt = &stamp
	if _, err := qa.NewRuleset([]qa.Rule{retired}); err != nil {
		t.Fatalf("a retired rule made the ruleset unloadable: %v", err)
	}
}
