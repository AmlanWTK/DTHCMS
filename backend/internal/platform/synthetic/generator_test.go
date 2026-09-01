package synthetic

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
	"unicode"
)

// profilePath is the clinician-authored case-mix these tests measure the generator against.
const profilePath = "../../testdata/profile.v1.json"

// cohortSize is large enough that a one-point error in any share is unambiguous, and small
// enough to generate in well under a second.
const cohortSize = 20000

// asOf anchors every date. Read from a constant rather than the clock so that a test which
// passes today passes in a year.
var asOf = time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)

func loadProfile(t *testing.T) *Profile {
	t.Helper()
	p, err := LoadProfile(profilePath)
	if err != nil {
		t.Fatalf("loading %s: %v", profilePath, err)
	}
	return p
}

func cohort(t *testing.T, seed int64) []Patient {
	t.Helper()
	return New(loadProfile(t), seed, asOf).Generate(cohortSize)
}

// --- the property the whole package exists for ---

// TestDeterminism is the load-bearing test.
//
// A generated dataset is only citable if it can be reproduced: a defect found in patient
// 4,183 has to be reachable by anyone with the seed, and a regression test has to be able to
// pin a population rather than describe one. Everything else here measures fidelity; this
// measures whether the fidelity means anything.
func TestDeterminism(t *testing.T) {
	p := loadProfile(t)

	first, _ := json.Marshal(New(p, 42, asOf).Generate(500))
	again, _ := json.Marshal(New(p, 42, asOf).Generate(500))
	if string(first) != string(again) {
		t.Fatal("same profile and seed produced a different population")
	}

	other, _ := json.Marshal(New(p, 43, asOf).Generate(500))
	if string(first) == string(other) {
		t.Fatal("a different seed produced an identical population — the seed is not reaching the sampler")
	}
}

// --- fidelity to the profile ---

// shares counts a key over the cohort and returns each as a fraction of the total.
func shares[T comparable](people []Patient, key func(Patient) (T, bool)) map[T]float64 {
	counts, total := map[T]int{}, 0
	for _, p := range people {
		if k, ok := key(p); ok {
			counts[k]++
			total++
		}
	}
	out := make(map[T]float64, len(counts))
	for k, c := range counts {
		out[k] = float64(c) / float64(total)
	}
	return out
}

// near fails with both numbers and the gap, because "expected true, got false" tells whoever
// broke this nothing about which direction to move.
func near(t *testing.T, label string, got, want, tolerance float64) {
	t.Helper()
	if diff := math.Abs(got - want); diff > tolerance {
		t.Errorf("%s: generated %.3f, profile states %.3f (off by %.3f, tolerance %.3f)",
			label, got, want, diff, tolerance)
	}
}

// tolerance is absolute, in shares. At n=20,000 the sampling noise on a 20% share is about
// 0.003, so 0.015 leaves room for the deliberate approximations documented in mix.go without
// letting a real drift through.
const tolerance = 0.015

func TestPresentingMixMatchesProfile(t *testing.T) {
	p := loadProfile(t)
	got := shares(cohort(t, 1), func(q Patient) (string, bool) { return q.ProblemKey, true })

	for key, want := range p.PresentingProblem {
		near(t, "presenting "+key, got[key], want, tolerance)
	}
}

func TestDemographicsMatchProfile(t *testing.T) {
	p := loadProfile(t)
	people := cohort(t, 2)

	var adults, adultFemale int
	for _, q := range people {
		if q.AgeYears >= 18 {
			adults++
			if q.Sex == Female {
				adultFemale++
			}
		}
	}

	near(t, "adult share", float64(adults)/float64(len(people)), p.Population.AdultShare, tolerance)
	near(t, "adult female share", float64(adultFemale)/float64(adults),
		p.Population.AdultFemaleShare, tolerance)
}

func TestDiabetesCaseloadMatchesProfile(t *testing.T) {
	p := loadProfile(t)
	people := cohort(t, 3)

	got := shares(people, func(q Patient) (DiabetesType, bool) {
		if q.Diabetes == nil {
			return "", false
		}
		return q.Diabetes.Type, true
	})

	near(t, "type 2", got[Type2], p.Diabetes.Type2, tolerance)
	near(t, "gestational", got[Gestational], p.Diabetes.Gestational, tolerance)
	near(t, "other/secondary", got[SecondaryDM], p.Diabetes.OtherSecondary, tolerance)

	// Type 1 gets its own, tighter tolerance. It is 2% of the caseload, so the 0.015 used
	// elsewhere would accept anything from zero to nearly double — which is exactly the bug
	// this package had: children with diabetes are type 1 by necessity, and drawing the
	// profile's 2% for adults on top of them put the caseload at 4.1%.
	near(t, "type 1", got[Type1], p.Diabetes.Type1, 0.007)

	var withDM, onInsulin int
	for _, q := range people {
		if q.Diabetes == nil {
			continue
		}
		withDM++
		if q.Diabetes.OnInsulin {
			onInsulin++
		}
	}
	near(t, "on insulin", float64(onInsulin)/float64(withDM), p.Prescribing.InsulinShare, tolerance)
}

func TestDiabetesThyroidOverlapMatchesProfile(t *testing.T) {
	p := loadProfile(t)
	people := cohort(t, 4)

	both := 0
	for _, q := range people {
		if q.Diabetes != nil && q.Thyroid != nil {
			both++
		}
	}
	near(t, "diabetes and thyroid together", float64(both)/float64(len(people)),
		p.Comorbidity["diabetesAndThyroid"].Value, tolerance)
}

func TestNameTraditionsMatchProfile(t *testing.T) {
	p := loadProfile(t)
	got := shares(cohort(t, 5), func(q Patient) (string, bool) { return q.Tradition, true })

	near(t, "muslim names", got["muslim"], p.NamesAndLanguage.MuslimShare, tolerance)
	near(t, "hindu names", got["hindu"], p.NamesAndLanguage.HinduShare, tolerance)
	near(t, "other names", got["other"], p.NamesAndLanguage.OtherShare, tolerance)
}

// --- coherence: the records a clinician would reject on sight ---

// TestNoImpossibleCombinations is the test that protects the dataset's credibility.
//
// A distribution that is a point or two off is a fidelity question. A pregnant man, or a
// nine-year-old with twenty years of type 2 diabetes, is the kind of record that makes a
// clinician stop trusting every other record in the file — and there is no partial credit
// for that. Each check below is one thing Dr. Nahid would notice immediately.
func TestNoImpossibleCombinations(t *testing.T) {
	for _, q := range cohort(t, 6) {
		if q.Pregnant && (q.Sex != Female || q.AgeYears < 15 || q.AgeYears > 45) {
			t.Errorf("%s: pregnant, sex %s, aged %d", q.ID, q.Sex, q.AgeYears)
		}
		if q.Presenting == ProblemPCOS && q.Sex != Female {
			t.Errorf("%s: PCOS in a %s patient", q.ID, q.Sex)
		}
		if q.Presenting == ProblemMaleReproductive && q.Sex != Male {
			t.Errorf("%s: male reproductive problem in a %s patient", q.ID, q.Sex)
		}

		if d := q.Diabetes; d != nil {
			if d.AgeAtDiagnosis > q.AgeYears {
				t.Errorf("%s: diagnosed at %d, aged %d", q.ID, d.AgeAtDiagnosis, q.AgeYears)
			}
			if d.DurationYears > float64(q.AgeYears) {
				t.Errorf("%s: %.1f years of diabetes, aged %d", q.ID, d.DurationYears, q.AgeYears)
			}
			// Type 2 below twelve is a data error. Between twelve and eighteen it is real and
			// rising in South Asia, and is generated on purpose.
			if q.AgeYears < 12 && d.Type == Type2 {
				t.Errorf("%s: type 2 diabetes aged %d", q.ID, q.AgeYears)
			}
			if d.Type == Gestational && (q.Sex != Female || !q.Pregnant) {
				t.Errorf("%s: gestational diabetes, sex %s, pregnant %v", q.ID, q.Sex, q.Pregnant)
			}
			if d.Type == Type1 && !d.OnInsulin {
				t.Errorf("%s: type 1 diabetes and not on insulin", q.ID)
			}
			// The contraindication the software must catch must not already be in the data,
			// or a test asserting the software catches it proves nothing.
			for _, m := range q.Medications {
				if m.Drug == "Metformin" && (d.EGFR < 30 || q.Pregnant) {
					t.Errorf("%s: metformin at eGFR %.0f, pregnant %v", q.ID, d.EGFR, q.Pregnant)
				}
			}
		}

		if th := q.Thyroid; th != nil && th.TSH == nil && !th.TSHUndetectable {
			t.Errorf("%s: thyroid disease with neither a TSH nor an undetectable flag", q.ID)
		}
	}
}

// TestBanglaNamesAreBangla guards a mistake already made once in this package: a Bangla
// string with Latin letters spliced into it. It renders as a name and is invisible in a
// distribution check.
func TestBanglaNamesAreBangla(t *testing.T) {
	for _, q := range cohort(t, 7) {
		for _, r := range q.Name.Bangla {
			if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' {
				t.Fatalf("%s: Latin letters in the Bangla name %q", q.ID, q.Name.Bangla)
			}
		}
		if strings.TrimSpace(q.Name.English) == "" || strings.TrimSpace(q.Name.Bangla) == "" {
			t.Fatalf("%s: empty name (%q / %q)", q.ID, q.Name.English, q.Name.Bangla)
		}
		for _, r := range q.Name.English {
			if unicode.Is(unicode.Bengali, r) {
				t.Fatalf("%s: Bengali letters in the English name %q", q.ID, q.Name.English)
			}
		}
	}
}

// TestMessinessExists asserts the absences.
//
// Synthetic data is usually too clean, and a screen that has never met a missing HbA1c has
// never been tested. These are the gaps the clinician asked for explicitly — a lab not done
// because it was missed, delayed or unaffordable, and a visit not attended.
func TestMessinessExists(t *testing.T) {
	var visits, missingHbA1c, notAttended, undetectableTSH int

	for _, q := range cohort(t, 8) {
		if q.Thyroid != nil && q.Thyroid.TSHUndetectable {
			undetectableTSH++
		}
		for _, v := range q.Visits {
			visits++
			if !v.Attended {
				notAttended++
				continue
			}
			if q.Diabetes != nil && v.HbA1c == nil {
				missingHbA1c++
			}
		}
	}

	for _, c := range []struct {
		what string
		n    int
	}{
		{"visits not attended", notAttended},
		{"attended visits with no HbA1c", missingHbA1c},
		{"undetectable TSH", undetectableTSH},
	} {
		if c.n == 0 {
			t.Errorf("no %s in %d visits across %d patients — the data is too clean to test against",
				c.what, visits, cohortSize)
		}
	}
}

// --- the named scenarios ---

// TestNamedCasesDeliverWhatTheyPromise checks that each scenario is reliably what it says.
//
// Random sampling produces most of these eventually and none of them on demand, which is no
// use to a test that needs one. If GenerateCase("hypoglycaemia") returns a patient without a
// low glucose, every test built on it is quietly asserting nothing.
func TestNamedCasesDeliverWhatTheyPromise(t *testing.T) {
	g := New(loadProfile(t), 9, asOf)

	if _, err := g.GenerateCase("no such case"); err == nil {
		t.Error("an unknown case name was accepted")
	}

	// Each scenario is generated several times: one draw passing could be luck.
	const draws = 25

	for i := 0; i < draws; i++ {
		for _, name := range TestCases() {
			q, err := g.GenerateCase(name)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			checkCase(t, name, q)
		}
	}
}

func checkCase(t *testing.T, name string, q Patient) {
	t.Helper()

	needsDiabetes := map[string]bool{
		"severe_hyperglycaemia": true, "hypoglycaemia": true, "pregnancy": true,
		"ckd_medication_limits": true, "insulin_initiation": true,
		"treatment_intolerance": true, "polypharmacy": true,
	}
	if needsDiabetes[name] && q.Diabetes == nil {
		t.Fatalf("%s: no diabetes", name)
	}

	switch name {
	case "severe_hyperglycaemia":
		if q.Diabetes.BaselineHbA1c < 13 {
			t.Errorf("%s: HbA1c %.1f is not severe", name, q.Diabetes.BaselineHbA1c)
		}
	case "hypoglycaemia":
		low := false
		for _, v := range q.Visits {
			if v.FastingGlucose != nil && *v.FastingGlucose < 3.5 {
				low = true
			}
		}
		if !low {
			t.Errorf("%s: no visit with a low glucose", name)
		}
	case "pregnancy":
		if !q.Pregnant || q.Sex != Female {
			t.Errorf("%s: pregnant %v, sex %s", name, q.Pregnant, q.Sex)
		}
	case "paediatric_growth":
		if q.AgeYears >= 18 || q.Presenting != ProblemGrowth {
			t.Errorf("%s: aged %d presenting with %s", name, q.AgeYears, q.Presenting)
		}
	case "ckd_medication_limits":
		if q.Diabetes.EGFR >= 45 {
			t.Errorf("%s: eGFR %.0f is above the range where dosing changes", name, q.Diabetes.EGFR)
		}
	case "thyroid_extremes":
		if q.Thyroid == nil {
			t.Fatalf("%s: no thyroid profile", name)
		}
		extreme := q.Thyroid.TSHUndetectable || (q.Thyroid.TSH != nil && *q.Thyroid.TSH > 100)
		if !extreme {
			t.Errorf("%s: TSH is not at either extreme", name)
		}
	case "insulin_initiation":
		if !q.Diabetes.OnInsulin {
			t.Errorf("%s: not on insulin", name)
		}
	case "polypharmacy":
		if len(q.Medications) < 7 {
			t.Errorf("%s: only %d medications", name, len(q.Medications))
		}
	case "treatment_intolerance":
		found := false
		for _, v := range q.Visits {
			if strings.Contains(v.Note, "intolerance") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no intolerance recorded", name)
		}
	case "conflicting_or_missing_labs":
		if len(q.Visits) < 2 {
			t.Fatalf("%s: only %d visits", name, len(q.Visits))
		}
		if q.Visits[0].HbA1c != nil || q.Visits[0].FastingGlucose != nil {
			t.Errorf("%s: first visit was supposed to have no labs at all", name)
		}
	}
}

// TestWeightIsATrajectoryNotFourDraws.
//
// This is a regression test for a defect that no distribution check could see and that the
// review page made obvious in seconds: height was recomputed at every visit, so a patient
// went 73.5 kg to 58.5 kg between two appointments three months apart. The mean weight across
// the cohort was perfectly correct throughout.
func TestWeightIsATrajectoryNotFourDraws(t *testing.T) {
	for _, q := range cohort(t, 10) {
		if q.HeightM <= 0 {
			t.Fatalf("%s: no height", q.ID)
		}

		var previous *Visit
		for i := range q.Visits {
			v := q.Visits[i]
			if v.Weight == nil {
				continue
			}
			if *v.Weight <= 0 {
				t.Errorf("%s: weight %.1f kg", q.ID, *v.Weight)
			}
			if previous != nil {
				// Rated per month rather than per visit, because a missed appointment puts
				// six months between two readings and a flat per-visit bound would then flag
				// an ordinary two quarters of growth.
				months := v.Date.Sub(previous.Date).Hours() / (24 * 30.44)
				change := math.Abs(*v.Weight-*previous.Weight) / *previous.Weight
				if months > 0 && change/months > 0.035 {
					t.Errorf("%s: aged %d, weight went %.1f → %.1f kg over %.1f months (%.1f%%/month)",
						q.ID, q.AgeYears, *previous.Weight, *v.Weight, months, 100*change/months)
				}
			}
			previous = &v
		}
	}
}

// TestChildrenAreMeasured.
//
// A paediatric endocrine clinic exists to read growth. A child whose record carries four
// visits and no height is a record with nothing in it to review — which is exactly what the
// first version generated for every non-diabetic child.
func TestChildrenAreMeasured(t *testing.T) {
	var children, withHeights int

	for _, q := range cohort(t, 11) {
		if q.AgeYears >= 18 {
			continue
		}
		children++

		var heights []float64
		for _, v := range q.Visits {
			if v.HeightCm != nil {
				heights = append(heights, *v.HeightCm)
			}
		}
		if len(heights) == 0 {
			t.Errorf("%s: aged %d with no height recorded at any visit", q.ID, q.AgeYears)
			continue
		}
		withHeights++

		for i := 1; i < len(heights); i++ {
			if heights[i] < heights[i-1]-0.05 {
				t.Errorf("%s: aged %d shrank from %.1f to %.1f cm",
					q.ID, q.AgeYears, heights[i-1], heights[i])
			}
		}
		if q.BMI <= 0 {
			t.Errorf("%s: aged %d with no BMI", q.ID, q.AgeYears)
		}
	}

	if children == 0 {
		t.Fatal("no children in the cohort at all")
	}
	if withHeights != children {
		t.Errorf("%d of %d children have no height", children-withHeights, children)
	}
}

// TestCautiousLevothyroxineStartActuallyFires.
//
// The rule was previously written inside thyroid(), which runs before comorbidities are
// assigned, so its check for ischaemic heart disease read an empty slice. It never fired once.
// The bug is invisible by inspection — the code is right, the ordering is wrong.
func TestCautiousLevothyroxineStartActuallyFires(t *testing.T) {
	p := loadProfile(t)
	cautious := p.Thyroid.LevothyroxineStart.CautiousStart

	var started, cautiousStarts, cautiousForHeartDisease int

	for _, q := range cohort(t, 12) {
		th := q.Thyroid
		if th == nil || th.LevothyroxineMcg == 0 {
			continue
		}
		if th.Category != OvertHypothyroid && th.Category != SubclinicalHypothyroid {
			continue // replacement after ablation, not initiation
		}
		started++

		if !th.CautiousStart {
			if q.AgeYears > 60 {
				t.Errorf("%s: aged %d started on %d mcg without a cautious start",
					q.ID, q.AgeYears, th.LevothyroxineMcg)
			}
			continue
		}

		cautiousStarts++
		if th.LevothyroxineMcg != cautious {
			t.Errorf("%s: cautious start at %d mcg, profile says %d",
				q.ID, th.LevothyroxineMcg, cautious)
		}
		if contains(q.Comorbidities, "ischaemic heart disease") {
			cautiousForHeartDisease++
		}
	}

	if started == 0 {
		t.Fatal("nobody was started on levothyroxine")
	}
	if cautiousStarts == 0 {
		t.Error("the cautious start rule never fired — check that dosing runs after comorbidities")
	}
	if cautiousForHeartDisease == 0 {
		t.Error("no cautious start was triggered by ischaemic heart disease, which is the " +
			"specific case that was dead code before")
	}
}
