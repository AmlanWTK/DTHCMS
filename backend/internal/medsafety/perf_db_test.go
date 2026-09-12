package medsafety_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/medsafety"
)

// Criterion 4: evaluation completes in under 500 ms (CP78).
//
// # What is being timed, and why it is the whole call
//
// Not the rule loop. `Engine.Check` end to end, including the three database reads it makes — the
// ruleset at an instant, the whole formulary's compositions, and the allergen groups with their
// cross-reactions. Timing only the in-memory part would be timing the half that was never going
// to be slow, and would pass for ever while somebody added an N+1 query to the other half.
//
// # The worst case
//
// Twelve proposed items — longer than any prescription this clinic writes — against **the entire
// approved rule library**, with a six-item current medication list, a four-diagnosis problem
// list, a recorded allergy and every numeric fact present so that no rule short-circuits on a
// missing datum. A picture with gaps in it is a *faster* picture, because a `DoesNotHold`
// settles a rule at its first predicate; filling everything in is what makes every rule walk
// every test.
func TestEvaluationOfAWholePrescriptionIsWellUnderHalfASecond(t *testing.T) {
	run := newGoldenRun(t)
	ctx := context.Background()
	at := time.Now().UTC()
	run.approve(t, []string{"ALL"}, at)
	run.approveAllergens(t,
		[]string{"PENICILLIN", "CEPHALOSPORIN", "CARBAPENEM",
			"SULFONAMIDE_ANTIBIOTIC", "SULFONAMIDE_NON_ANTIBIOTIC", "INSULIN_ANIMAL"},
		[]string{"PENICILLIN→CEPHALOSPORIN", "PENICILLIN→CARBAPENEM",
			"CEPHALOSPORIN→PENICILLIN",
			"SULFONAMIDE_ANTIBIOTIC→SULFONAMIDE_NON_ANTIBIOTIC"}, at)

	dose := func(v float64) *float64 { return &v }
	proposed := []medsafety.Item{
		{Ref: "1", Generic: "Metformin hydrochloride", Label: "Comet 500", DailyDose: dose(1000), DoseUnit: "mg"},
		{Ref: "2", Generic: "Sitagliptin + Metformin hydrochloride", Label: "Siglimet", DailyDose: dose(100), DoseUnit: "mg"},
		{Ref: "3", Generic: "Empagliflozin", Label: "Empa 10", DailyDose: dose(10), DoseUnit: "mg"},
		{Ref: "4", Generic: "Glimepiride", Label: "Glimepiride 2", DailyDose: dose(4), DoseUnit: "mg"},
		{Ref: "5", Generic: "Pioglitazone + Glimepiride", Label: "Pioglim", DailyDose: dose(30), DoseUnit: "mg"},
		{Ref: "6", Generic: "Ramipril", Label: "Ramipril 5", DailyDose: dose(5), DoseUnit: "mg"},
		{Ref: "7", Generic: "Telmisartan + Amlodipine besilate", Label: "Telma-AM", DailyDose: dose(40), DoseUnit: "mg"},
		{Ref: "8", Generic: "Atorvastatin calcium", Label: "Atorva 40", DailyDose: dose(40), DoseUnit: "mg"},
		{Ref: "9", Generic: "Fenofibrate", Label: "Fenofibrate 160", DailyDose: dose(160), DoseUnit: "mg"},
		{Ref: "10", Generic: "Levothyroxine sodium", Label: "Thyrox 100", DailyDose: dose(100), DoseUnit: "mcg"},
		{Ref: "11", Generic: "Pregabalin", Label: "Pregaba 75", DailyDose: dose(150), DoseUnit: "mg"},
		{Ref: "12", Generic: "Diclofenac sodium", Label: "Voltalin 50", DailyDose: dose(100), DoseUnit: "mg"},
	}
	picture := medsafety.Picture{
		AgeYears: f(68), Pregnancy: "NOT_PREGNANT", EGFR: f(38), Hepatic: "MILD",
		Diagnoses:      []string{"E11.9", "I10", "N18.3", "E78.5"},
		AllergenGroups: []string{"SULFONAMIDE_ANTIBIOTIC"},
		Current: []medsafety.Item{
			{Ref: "c1", Generic: "Insulin glargine", Label: "Lantus"},
			{Ref: "c2", Generic: "Furosemide", Label: "Lasix 40"},
			{Ref: "c3", Generic: "Aspirin (low dose)", Label: "Ecosprin 75"},
			{Ref: "c4", Generic: "Calcium lactate gluconate + Calcium carbonate + Vitamin D3", Label: "Calbo-D"},
			{Ref: "c5", Generic: "Losartan potassium + Hydrochlorothiazide", Label: "Losacar Plus"},
			{Ref: "c6", Generic: "Gliclazide", Label: "Gliclazide 80"},
		},
	}
	request := medsafety.Request{
		FacilityID: run.facility, At: at, Picture: picture, Proposed: proposed,
	}

	// One warm call, then twenty timed. The first includes whatever the connection pool does
	// on its way to being warm, which is not what a physician's second prescription of the day
	// experiences and is not what the criterion is about.
	if _, err := run.engine.Check(ctx, request); err != nil {
		t.Fatal(err)
	}

	const runs = 20
	timings := make([]time.Duration, 0, runs)
	var last medsafety.Result
	for i := 0; i < runs; i++ {
		started := time.Now()
		result, err := run.engine.Check(ctx, request)
		timings = append(timings, time.Since(started))
		if err != nil {
			t.Fatal(err)
		}
		last = result
	}
	sort.Slice(timings, func(i, j int) bool { return timings[i] < timings[j] })

	median := timings[len(timings)/2]
	worst := timings[len(timings)-1]
	t.Logf("12 items × %d live rules × %d current medicines: median %s, worst of %d runs %s "+
		"(%d findings, %d uncovered)",
		last.RulesLive, len(picture.Current), median, runs, worst,
		len(last.Findings), last.UncoveredCount)

	if worst > 500*time.Millisecond {
		t.Errorf("the worst of %d runs took %s; criterion 4 is under 500ms", runs, worst)
	}
	// A margin rather than the bare criterion. The number this measures on a developer's
	// machine is not the number a clinic sees, and a test that only just passes here is a test
	// that fails there without anything having changed.
	if median > 100*time.Millisecond {
		t.Errorf("the median took %s. That is inside the criterion and outside what this "+
			"workload should cost: three queries and about %d predicate evaluations.",
			median, 12*last.RulesLive*2)
	}
	// The timing means nothing if the call did no work.
	if last.RulesLive < 40 {
		t.Fatalf("only %d rules were live; this is not the worst case", last.RulesLive)
	}
	if len(last.Findings) < 5 {
		t.Fatalf("a twelve-item prescription in a CKD patient produced %d findings; the "+
			"picture is not exercising the library", len(last.Findings))
	}
}
