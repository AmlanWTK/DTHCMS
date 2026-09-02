package synthetic

import (
	"fmt"
	"math"
	"math/rand"
	"time"
)

// Generator produces a population from a profile.
//
// Deterministic by construction: the same profile and the same seed give the same people,
// on any machine. That is what makes a generated dataset citable — a defect found in
// patient 4,183 can be reproduced by anyone with the seed, and a regression test can pin a
// population rather than describing one.
type Generator struct {
	profile *Profile
	rng     *rand.Rand
	// asOf anchors every generated date. Passed in rather than read from the clock, for
	// the same reason as the seed.
	asOf time.Time
	next int

	// adultType1 is solved for at construction so the diabetes caseload as a whole lands on
	// the profile's type 1 share once the children — who are type 1 by necessity — are counted.
	adultType1 float64
}

// New builds a generator. Same seed, same population.
func New(profile *Profile, seed int64, asOf time.Time) *Generator {
	// The error is discarded deliberately: Validate already ran reconcile, which calls this
	// and refuses the profile if it cannot be solved. A profile that reached here is solvable.
	rate, _ := adultType1Rate(profile, computeMasses(profile))

	return &Generator{
		profile:    profile,
		rng:        rand.New(rand.NewSource(seed)), //nolint:gosec // fictional data, not cryptography
		asOf:       asOf,
		adultType1: rate,
	}
}

// Generate produces n patients.
func (g *Generator) Generate(n int) []Patient {
	people := make([]Patient, 0, n)
	for i := 0; i < n; i++ {
		people = append(people, g.patient(""))
	}
	return people
}

// GenerateCase produces one patient built to exercise a named scenario.
//
// The ten scenarios come from the clinician's own list. A generator that can only sample
// randomly will produce most of them eventually and none of them reliably, which is no use
// to a test that needs one.
func (g *Generator) GenerateCase(name string) (Patient, error) {
	if !knownCase(name) {
		return Patient{}, fmt.Errorf("unknown test case %q — see TestCases() for the list", name)
	}
	return g.patient(name), nil
}

// TestCases lists the scenarios GenerateCase understands.
func TestCases() []string {
	return []string{
		"severe_hyperglycaemia", "hypoglycaemia", "pregnancy",
		"paediatric_growth", "ckd_medication_limits", "thyroid_extremes",
		"insulin_initiation", "treatment_intolerance", "polypharmacy",
		"conflicting_or_missing_labs",
	}
}

func knownCase(name string) bool {
	for _, c := range TestCases() {
		if c == name {
			return true
		}
	}
	return false
}

// --- the patient ---

func (g *Generator) patient(forced string) Patient {
	g.next++
	p := Patient{
		ID:         fmt.Sprintf("synthetic-%06d", g.next),
		ForcedCase: forced,
	}

	// Problem first, then the person who has it. See mix.go for why this order.
	g.presenting(&p, forced)
	g.demographics(&p, forced)
	g.name(&p)
	g.conditions(&p, forced)
	g.comorbidities(&p)
	g.thyroidDosing(&p)
	g.medications(&p, forced)
	g.visits(&p, forced)

	return p
}

// presenting draws straight from the profile, so the presenting mix is exact by construction.
func (g *Generator) presenting(p *Patient, forced string) {
	key := choose(g.rng, g.profile.PresentingProblem, problemOrder)

	switch forced {
	case "severe_hyperglycaemia", "hypoglycaemia", "ckd_medication_limits",
		"insulin_initiation", "treatment_intolerance", "polypharmacy", "pregnancy":
		key = "diabetes"
	case "thyroid_extremes":
		key = "thyroid"
	case "paediatric_growth":
		key = "growthPuberty"
	}

	p.ProblemKey = key
	p.Presenting = problemNames[key]
}

// problemNames maps the profile's JSON keys to the typed values the rest of the system uses.
var problemNames = map[string]PresentingProblem{
	"diabetes": ProblemDiabetes, "thyroid": ProblemThyroid,
	"obesityMetabolic": ProblemObesity, "pcos": ProblemPCOS,
	"growthPuberty": ProblemGrowth, "boneCalciumVitaminD": ProblemBone,
	"adrenal": ProblemAdrenal, "pituitary": ProblemPituitary,
	"maleSexualReproductive": ProblemMaleReproductive,
}

func (g *Generator) demographics(p *Patient, forced string) {
	pop := g.profile.Population
	s := caseMix[p.ProblemKey]

	paediatric := pick(g.rng, s.paediatric)
	switch forced {
	case "paediatric_growth":
		paediatric = true
	case "pregnancy", "ckd_medication_limits", "polypharmacy":
		paediatric = false
	}

	if paediatric {
		p.AgeYears = int(math.Round(triangular(g.rng, pop.PaediatricAge)))
		if p.AgeYears < s.minChildAge {
			// Raised rather than redrawn: a redraw would bias the whole paediatric age
			// distribution toward the top of the range for every problem that sets a floor.
			p.AgeYears = s.minChildAge + g.rng.Intn(maxInt(1, 18-s.minChildAge))
		}
		p.Sex = sexFrom(g.rng, s.childFemale)
	} else {
		p.AgeYears = int(math.Round(triangular(g.rng, pop.AdultAge)))
		if s.maxAdultAge > 0 && p.AgeYears > s.maxAdultAge {
			// Redrawn within the window rather than clamped: clamping would pile every
			// out-of-range draw onto the cap itself, and a clinic where a quarter of the PCOS
			// patients are exactly forty-five reads as wrong as one where they are fifty-five.
			p.AgeYears = 18 + g.rng.Intn(s.maxAdultAge-17)
		}
		p.Sex = sexFrom(g.rng, s.adultFemale)
	}

	// Pregnancy only where it is possible. Generating it anywhere else produces the kind of
	// record that makes a clinician stop trusting the whole dataset.
	if p.Sex == Female && p.AgeYears >= 15 && p.AgeYears <= 45 {
		p.Pregnant = pick(g.rng, 0.06)
	}
	if forced == "pregnancy" {
		p.Sex = Female
		if p.AgeYears < 18 || p.AgeYears > 42 {
			p.AgeYears = 20 + g.rng.Intn(20)
		}
		p.Pregnant = true
	}

	p.HeightM = heightM(g.rng, p.AgeYears, p.Sex, p.Presenting)
	p.DateOfBirth = g.asOf.AddDate(-p.AgeYears, -g.rng.Intn(12), -g.rng.Intn(28))
	p.Urban = pick(g.rng, pop.UrbanShare)
	p.RecordInBangla = pick(g.rng, g.profile.NamesAndLanguage.BanglaForNarrative)
}

func sexFrom(rng *rand.Rand, femaleShare float64) Sex {
	if pick(rng, femaleShare) {
		return Female
	}
	return Male
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (g *Generator) name(p *Patient) {
	nl := g.profile.NamesAndLanguage
	tradition := choose(g.rng,
		map[string]float64{"muslim": nl.MuslimShare, "hindu": nl.HinduShare, "other": nl.OtherShare},
		[]string{"muslim", "hindu", "other"})
	p.Tradition = tradition

	pool := muslimNames
	switch tradition {
	case "hindu":
		pool = hinduNames
	case "other":
		pool = otherNames
	}

	given := pool.maleGiven
	if p.Sex == Female {
		given = pool.femaleGiven
	}
	g1 := given[g.rng.Intn(len(given))]
	fam := pool.family[g.rng.Intn(len(pool.family))]

	p.Name = Name{
		English: g1.English + " " + fam.English,
		Bangla:  g1.Bangla + " " + fam.Bangla,
	}
}

// --- conditions ---

func (g *Generator) conditions(p *Patient, forced string) {
	// Diabetes is present in more people than present *for* it, and thyroid disease likewise.
	// The rates live in mix.go, where reconcile() checks them against the clinician's stated
	// 20% overlap; here they are just applied.
	child := p.AgeYears < 18

	hasDiabetes := pick(g.rng, diabetesRate(p.ProblemKey, child))
	hasThyroid := pick(g.rng, thyroidRate(p.ProblemKey, child))

	if hasDiabetes {
		p.Diabetes = g.diabetes(p, forced)
	}
	if hasThyroid {
		p.Thyroid = g.thyroid(p, forced)
	}

	// BMI. Half the caseload is obese and a third present *for* it, so the ones who did not
	// present for it cannot simply be sampled at one half again — that lands the total at 67%.
	ob := g.profile.OtherConditions.Obesity
	obese := p.Presenting == ProblemObesity || pick(g.rng, conditionalShare(ob.Share, ob.AsMainProblem))

	if p.AgeYears >= 18 {
		if obese {
			p.BMI = round1(triangular(g.rng, ob.BMI.Cluster))
		} else {
			p.BMI = round1(19 + g.rng.Float64()*8)
		}
	} else {
		// Children are not small adults. A BMI of 17 is ordinary at six and underweight at
		// sixteen, and using the adult cut-offs here would put half the paediatric caseload in
		// the wrong category before the software ever saw it.
		// Childhood obesity is real and rising here, but it is not the adult prevalence. Using
		// the adult conditional rate put a quarter of the children above the 99th centile.
		p.BMI = childBMI(g.rng, p.AgeYears, obese && (p.Presenting == ProblemObesity || pick(g.rng, 0.4)))
	}
}

func (g *Generator) diabetes(p *Patient, forced string) *DiabetesProfile {
	dm := g.profile.Diabetes

	// The adult type 1 rate is derived, not taken from the profile: children with diabetes are
	// type 1 by clinical necessity and already account for part of the stated 2%. See
	// adultType1Rate in mix.go. The other three shares absorb the difference in proportion.
	type1 := g.adultType1
	scale := (1 - type1) / (1 - dm.Type1)
	typeKey := choose(g.rng, map[string]float64{
		"type2": dm.Type2 * scale, "type1": type1,
		"gestational": dm.Gestational * scale, "other": dm.OtherSecondary * scale,
	}, []string{"type2", "type1", "gestational", "other"})

	// Coherence beats the marginal distribution. A child with type 2 of twenty years'
	// standing is the single most obvious tell that data was generated field by field.
	//
	// Not quite always type 1, though: type 2 in an obese adolescent is real and rising in
	// South Asia, and a system that has never seen one will not handle the first.
	if p.AgeYears < 18 {
		if p.AgeYears >= 12 && p.Presenting == ProblemObesity && pick(g.rng, 0.30) {
			typeKey = "type2"
		} else {
			typeKey = "type1"
		}
	}
	/*
	 * Gestational diabetes decides the pregnancy, not the other way round.
	 *
	 * Pregnancy is drawn in demographics, before any diagnosis exists. Requiring it to have
	 * already been drawn capped gestational diabetes at ~1% against a stated 8%, because
	 * only ~2% of the cohort was pregnant at that point. An endocrine clinic that manages
	 * GDM referrals sees more pregnancy than a general one, and this is where that shows.
	 */
	if typeKey == "gestational" {
		p.Sex = Female
		if p.AgeYears < 18 || p.AgeYears > 42 {
			p.AgeYears = 20 + g.rng.Intn(20)
			p.HeightM = heightM(g.rng, p.AgeYears, p.Sex, p.Presenting)
			p.DateOfBirth = g.asOf.AddDate(-p.AgeYears, -g.rng.Intn(12), -g.rng.Intn(28))
		}
		p.Pregnant = true
	}
	if p.Pregnant && p.AgeYears >= 18 && pick(g.rng, 0.5) {
		typeKey = "gestational"
	}

	d := &DiabetesProfile{}
	switch typeKey {
	case "type1":
		d.Type = Type1
		d.AgeAtDiagnosis = int(math.Round(triangular(g.rng, dm.AgeAtDiagnosisType1)))
		d.OnInsulin = true // type 1 without insulin is not a patient, it is a mistake
	case "gestational":
		d.Type = Gestational
		d.AgeAtDiagnosis = p.AgeYears
	case "other":
		d.Type = SecondaryDM
		d.AgeAtDiagnosis = int(math.Round(triangular(g.rng, dm.AgeAtDiagnosisType2)))
	default:
		d.Type = Type2
		d.AgeAtDiagnosis = int(math.Round(triangular(g.rng, dm.AgeAtDiagnosisType2)))
	}
	if d.AgeAtDiagnosis > p.AgeYears {
		d.AgeAtDiagnosis = p.AgeYears
	}
	if d.AgeAtDiagnosis < 1 {
		d.AgeAtDiagnosis = 1
	}

	// Duration: how much of the illness happened before this clinic saw it.
	dur := dm.DurationAtFirstVisit
	switch choose(g.rng, map[string]float64{
		"here": dur.DiagnosedHere, "recent": dur.WithinOneYear, "established": dur.Established,
	}, []string{"here", "recent", "established"}) {
	case "here":
		d.DiagnosedHere = true
		d.DurationYears = 0
		d.AgeAtDiagnosis = p.AgeYears
	case "recent":
		d.DurationYears = round1(g.rng.Float64())
	default:
		d.DurationYears = round1(triangular(g.rng, dur.EstablishedYears))
	}
	if maxDuration := float64(p.AgeYears - d.AgeAtDiagnosis); d.DurationYears > maxDuration && maxDuration >= 0 {
		d.DurationYears = round1(maxDuration)
	}

	d.BaselineHbA1c = round1(triangular(g.rng, dm.HbA1cAtPresentation))
	if pick(g.rng, dm.AtTargetFirstVisit) {
		d.BaselineHbA1c = round1(5.9 + g.rng.Float64()*1.0)
	}

	// Renal function tracks duration, which is what makes the CKD dosing rules bite on the
	// patients they should bite on rather than at random.
	d.EGFR = round1(clamp(102-d.DurationYears*1.9-float64(p.AgeYears)*0.35+g.rng.NormFloat64()*8, 12, 120))

	// 30% of the diabetes caseload is on insulin — and every type 1 patient is already in
	// that 30%, so the rest must be sampled at the conditional rate or the total overshoots.
	d.OnInsulin = d.OnInsulin ||
		pick(g.rng, conditionalShare(g.profile.Prescribing.InsulinShare, dm.Type1))

	switch forced {
	case "severe_hyperglycaemia":
		d.Type = Type2
		d.BaselineHbA1c = round1(13.5 + g.rng.Float64()*2.5)
	case "ckd_medication_limits":
		d.EGFR = round1(18 + g.rng.Float64()*14) // where metformin and SGLT2 dosing change
	case "insulin_initiation":
		d.OnInsulin = true
		d.BaselineHbA1c = round1(11.0 + g.rng.Float64()*2.0)
	case "hypoglycaemia":
		d.OnInsulin = true
		d.BaselineHbA1c = round1(6.0 + g.rng.Float64()*1.0)
	}
	return d
}

func (g *Generator) thyroid(p *Patient, forced string) *ThyroidProfile {
	th := g.profile.Thyroid

	key := choose(g.rng, map[string]float64{
		"overt": th.OvertPrimaryHypothyroid, "subclinical": th.SubclinicalHypothyroid,
		"hyper": th.HyperthyroidGraves, "goitre": th.EuthyroidGoitre,
		"nodule": th.NoduleSurveillance, "ablative": th.PostSurgicalOrRAI,
		"cancer": th.CancerFollowUp,
	}, []string{"overt", "subclinical", "hyper", "goitre", "nodule", "ablative", "cancer"})

	t := &ThyroidProfile{}
	switch key {
	case "subclinical":
		t.Category = SubclinicalHypothyroid
		v := round2(4.5 + g.rng.Float64()*5.0)
		t.TSH = &v
	case "hyper":
		t.Category = Hyperthyroid
		// The profile says the low end is undetectable, not zero. A client that cannot
		// render "undetectable" prints 0.00, which reads as a plausible number.
		if pick(g.rng, 0.35) {
			t.TSHUndetectable = true
		} else {
			v := round2(0.005 + g.rng.Float64()*0.095)
			t.TSH = &v
		}
	case "goitre", "nodule":
		if key == "goitre" {
			t.Category = EuthyroidGoitre
		} else {
			t.Category = NoduleSurveillance
		}
		v := round2(0.8 + g.rng.Float64()*3.0)
		t.TSH = &v
	case "ablative", "cancer":
		if key == "ablative" {
			t.Category = PostAblative
		} else {
			t.Category = CancerFollowUp
		}
		v := round2(0.3 + g.rng.Float64()*2.0)
		t.TSH = &v
		t.LevothyroxineMcg = 75 + 25*g.rng.Intn(4)
	default:
		t.Category = OvertHypothyroid
		t.TSH = ptr(round2(hypothyroidTSH(g.rng, th.TSHHypothyroid)))
	}

	if forced == "thyroid_extremes" {
		if pick(g.rng, 0.5) {
			t.Category = OvertHypothyroid
			t.TSHUndetectable = false
			t.TSH = ptr(round2(120 + g.rng.Float64()*60)) // the tail above 100
			t.LevothyroxineMcg = th.LevothyroxineStart.CautiousStart
			t.CautiousStart = true
		} else {
			t.Category = Hyperthyroid
			t.TSH = nil
			t.TSHUndetectable = true
		}
	}
	return t
}

// thyroidDosing decides the starting dose of levothyroxine.
//
// It runs after comorbidities, and that ordering is the whole point. In the previous version
// this lived inside thyroid(), which runs before comorbidities are assigned — so the check for
// ischaemic heart disease read an empty slice and the cautious-start rule never fired once.
// It looked implemented, it passed review, and it was dead.
//
// The 25–50 mcg range is the clinician's own answer, not a textbook's: he starts low and
// titrates, which is not what a weight-based calculation would produce. All three of his
// stated triggers for a cautious start are honoured, including the one about long-standing
// severe hypothyroidism, which is read here as a TSH deep into the tail.
func (g *Generator) thyroidDosing(p *Patient) {
	t := p.Thyroid
	if t == nil || (t.Category != OvertHypothyroid && t.Category != SubclinicalHypothyroid) {
		return
	}

	ls := g.profile.Thyroid.LevothyroxineStart
	severe := t.TSH != nil && *t.TSH > 75

	if p.AgeYears > 60 || contains(p.Comorbidities, "ischaemic heart disease") || severe {
		t.LevothyroxineMcg = ls.CautiousStart
		t.CautiousStart = true
		return
	}

	steps := 1 + (ls.High-ls.Low)/25
	if steps < 1 {
		steps = 1
	}
	t.LevothyroxineMcg = ls.Low + 25*g.rng.Intn(steps)
}

// hypothyroidTSH honours the clinician's note that values above 100 occur.
//
// A triangular draw over 10–100 would never produce one, and the extreme presentation is
// precisely a case the software must handle.
func hypothyroidTSH(rng *rand.Rand, r Range) float64 {
	if pick(rng, 0.05) {
		return 100 + rng.Float64()*80
	}
	return triangular(rng, r)
}

// --- comorbidity, medication, visits ---

// comorbidities respects the denominators the clinician gave.
//
// He answered each row against a different population — "of adults with type 2 diabetes or
// metabolic disease", "of the diabetes caseload", "of the total clinic caseload". Flattening
// those into one denominator produces a cohort that is wrong in a way no reviewer catches by
// eye, so each is applied to the group it was stated for.
func (g *Generator) comorbidities(p *Patient) {
	c := g.profile.Comorbidity
	hasDM := p.Diabetes != nil
	type2OrMetabolic := (hasDM && p.Diabetes.Type == Type2) || p.Presenting == ProblemObesity

	add := func(name string, applies bool, share float64) {
		if applies && pick(g.rng, share) {
			p.Comorbidities = append(p.Comorbidities, name)
		}
	}

	add("hypertension", type2OrMetabolic && p.AgeYears >= 18, c["hypertension"].Value)
	add("dyslipidaemia", type2OrMetabolic && p.AgeYears >= 18, c["dyslipidaemia"].Value)
	add("fatty liver", type2OrMetabolic, c["fattyLiver"].Value)
	add("ischaemic heart disease", hasDM && p.Diabetes.Type == Type2, c["ischaemicHeartDisease"].Value)

	// Microvascular complications scale with duration rather than appearing at random —
	// which is what makes "50% arrive with an established complication" land on the people
	// who have had the disease longest.
	if hasDM {
		weight := clamp(p.Diabetes.DurationYears/12.0, 0.15, 1.6)
		add("diabetic retinopathy", true, c["retinopathy"].Value*weight)
		add("diabetic neuropathy", true, c["neuropathy"].Value*weight)
		add("diabetic nephropathy", p.Diabetes.EGFR < 75, c["nephropathyCKD"].Value*weight)
		add("diabetic foot", contains(p.Comorbidities, "diabetic neuropathy"), c["diabeticFoot"].Value*2)
	}
}

func (g *Generator) medications(p *Patient, forced string) {
	g.diabetesMedications(p, forced)

	if p.Thyroid != nil && p.Thyroid.LevothyroxineMcg > 0 {
		p.Medications = append(p.Medications, Medication{
			Drug: "Levothyroxine", Class: "thyroid hormone",
			Dose: fmt.Sprintf("%d mcg once daily, before breakfast", p.Thyroid.LevothyroxineMcg),
		})
	}
	if p.Thyroid != nil && p.Thyroid.Category == Hyperthyroid {
		p.Medications = append(p.Medications, Medication{
			Drug: "Carbimazole", Dose: "10 mg three times daily, tapered by response",
			Class: "antithyroid"})
	}

	g.otherMedications(p)

	if forced == "polypharmacy" {
		for len(p.Medications) < 7 {
			p.Medications = append(p.Medications, Medication{
				Drug: "Amlodipine", Dose: "5 mg once daily", Class: "calcium channel blocker"})
			p.Medications = append(p.Medications, Medication{
				Drug: "Losartan", Dose: "50 mg once daily", Class: "ARB"})
		}
	}
}

/*
 * otherMedications covers what the profile does not.
 *
 * Dr. Nahid's §prescribing answers are about diabetes, and the thyroid section gives a
 * levothyroxine range. That leaves four of the nine presenting problems — PCOS, bone and
 * vitamin D, male reproductive, adrenal and pituitary — arriving in clinic and leaving with
 * nothing written, which is both wrong and useless to review: a card that says "no medicines
 * prescribed yet" gives a clinician nothing to disagree with.
 *
 * CLINICAL REVIEW WANTED. Everything below is extrapolation, chosen for what is actually
 * available and affordable in Bangladesh rather than what a guideline would list first. These
 * are the lines most likely to be wrong, and the ones most worth an argument.
 */
func (g *Generator) otherMedications(p *Patient) {
	add := func(drug, dose, class string) {
		p.Medications = append(p.Medications, Medication{Drug: drug, Dose: dose, Class: class})
	}
	onMetformin := false
	for _, m := range p.Medications {
		if m.Drug == "Metformin" {
			onMetformin = true
		}
	}

	switch p.Presenting {
	case ProblemPCOS:
		if !onMetformin && !p.Pregnant && pick(g.rng, 0.7) {
			add("Metformin", "500 mg twice daily with food", "biguanide")
		}
		if !p.Pregnant && pick(g.rng, 0.45) {
			// Widely used here for the cycle and the hirsutism together.
			add("Ethinylestradiol + cyproterone", "one tablet daily, days 1–21", "combined oral contraceptive")
		}
		if !p.Pregnant && pick(g.rng, 0.2) {
			add("Letrozole", "2.5 mg daily, days 3–7 of the cycle", "ovulation induction")
		}

	case ProblemBone:
		add("Cholecalciferol", "40,000 IU weekly for 8 weeks, then monthly", "vitamin D")
		if pick(g.rng, 0.75) {
			add("Calcium carbonate", "500 mg elemental twice daily with meals", "calcium supplement")
		}
		if p.AgeYears > 55 && p.Sex == Female && pick(g.rng, 0.3) {
			add("Alendronate", "70 mg once weekly, fasting, upright", "bisphosphonate")
		}

	case ProblemMaleReproductive:
		if p.AgeYears >= 18 && pick(g.rng, 0.5) {
			add("Testosterone undecanoate", "1,000 mg intramuscular, 10–14 weekly", "androgen replacement")
		}
		if p.AgeYears >= 18 && pick(g.rng, 0.25) {
			add("Clomifene citrate", "25 mg on alternate days", "gonadotrophin stimulation")
		}

	case ProblemPituitary:
		if pick(g.rng, 0.45) {
			add("Cabergoline", "0.25 mg twice weekly, titrated to prolactin", "dopamine agonist")
		}
		if pick(g.rng, 0.2) {
			add("Hydrocortisone", "10 mg morning, 5 mg early afternoon", "glucocorticoid replacement")
		}

	case ProblemAdrenal:
		if pick(g.rng, 0.55) {
			add("Hydrocortisone", "10 mg morning, 5 mg early afternoon", "glucocorticoid replacement")
			if pick(g.rng, 0.6) {
				add("Fludrocortisone", "100 mcg once daily", "mineralocorticoid replacement")
			}
		}

	case ProblemObesity:
		// Only where there is no diabetes; the diabetes path already prescribes.
		if p.Diabetes == nil && p.AgeYears >= 18 {
			if !onMetformin && p.BMI >= 30 && pick(g.rng, 0.4) {
				add("Metformin", "500 mg twice daily with food", "biguanide")
			}
			if p.BMI >= 32 && !p.Pregnant && pick(g.rng, 0.2) {
				add("Semaglutide", "0.25 mg weekly, titrated", "GLP-1 receptor agonist")
			}
		}

	case ProblemGrowth:
		// Growth hormone is priced out of reach for most families here, so the honest answer
		// is usually investigation and review rather than a prescription. Generating it freely
		// would make the paediatric caseload look like a different country's.
		if p.AgeYears >= 12 && p.Sex == Male && pick(g.rng, 0.15) {
			add("Testosterone enanthate", "50 mg intramuscular monthly, 4–6 doses", "pubertal induction")
		}
	}
}

func (g *Generator) diabetesMedications(p *Patient, forced string) {
	if p.Diabetes == nil {
		return
	}

	pr := g.profile.Prescribing
	d := p.Diabetes

	// Metformin is first line, and eGFR is what stops it. Generating a patient on metformin
	// with an eGFR of 20 would be exactly the contraindication the software must catch, so
	// it is generated only when asked for.
	metforminSafe := d.EGFR >= 30 && !p.Pregnant
	if d.Type != Type1 && metforminSafe {
		dose := "500 mg twice daily with food"
		if d.EGFR >= 45 && pick(g.rng, 0.6) {
			dose = "1000 mg twice daily with food"
		} else if d.EGFR < 45 {
			dose = "500 mg once daily with food (reduced for eGFR)"
		}
		p.Medications = append(p.Medications, Medication{Drug: "Metformin", Dose: dose, Class: "biguanide"})
	}

	if d.Type == Type2 && !p.Pregnant {
		if d.EGFR >= 20 && pick(g.rng, pr.SGLT2Share) {
			p.Medications = append(p.Medications, Medication{
				Drug: "Empagliflozin", Dose: "10 mg once daily", Class: "SGLT2 inhibitor"})
		}
		if pick(g.rng, pr.DPP4Share) {
			// Linagliptin specifically: it needs no renal dose adjustment, which is why it
			// is the one the clinician named for this caseload.
			p.Medications = append(p.Medications, Medication{
				Drug: "Linagliptin", Dose: "5 mg once daily", Class: "DPP-4 inhibitor"})
		}
		if pick(g.rng, pr.GLP1Share) && p.BMI >= 27 {
			p.Medications = append(p.Medications, Medication{
				Drug: "Semaglutide", Dose: "0.25 mg weekly, titrated", Class: "GLP-1 receptor agonist"})
		}
		if pick(g.rng, pr.SulfonylureaShare) {
			p.Medications = append(p.Medications, Medication{
				Drug: "Gliclazide MR", Dose: "30 mg once daily", Class: "sulfonylurea"})
		}
	}

	if d.OnInsulin {
		regimen := Medication{Drug: "Insulin glargine", Dose: "10 units at bedtime, titrated", Class: "basal insulin"}
		if p.Pregnant {
			regimen = Medication{Drug: "Human insulin 30/70", Dose: "split-mixed, individualised", Class: "premixed insulin"}
		} else if pick(g.rng, 0.4) {
			regimen = Medication{Drug: "Human insulin 30/70", Dose: "twice daily before meals", Class: "premixed insulin"}
		}
		p.Medications = append(p.Medications, regimen)
	}

	if contains(p.Comorbidities, "dyslipidaemia") || contains(p.Comorbidities, "ischaemic heart disease") {
		p.Medications = append(p.Medications, Medication{
			Drug: "Rosuvastatin", Dose: "10 mg once daily", Class: "statin"})
	}
}

// visits builds a trajectory, not a set of samples.
//
// The clinician's numbers describe a response: median HbA1c 10.0 on arrival, a fall of 3–5
// points in the first six months, ~70% at target within a year *among those retained*. That
// last qualifier is why loss to follow-up is modelled first — the 70% is not a property of
// everyone who walks in.
func (g *Generator) visits(p *Patient, forced string) {
	fu := g.profile.FollowUp
	count := fu.VisitsInFirstYear
	if count <= 0 {
		count = 4
	}

	// A scenario patient is never lost to follow-up. They exist to exercise one specific path
	// — a hypoglycaemic reading at the last visit, an intolerance noted at the second — and
	// dropping them out of care silently produces a case that no longer contains the thing it
	// was asked for. That failure is invisible: the patient still generates, and every test
	// built on the case quietly asserts nothing.
	lost := forced == "" && pick(g.rng, fu.LostToFollowUpWithinYear)
	lostAfter := 1 + g.rng.Intn(count)

	// How steeply this patient responds. Sampled once, so a visit series is one person
	// improving rather than four unrelated draws.
	fall := triangular(g.rng, g.profile.Diabetes.HbA1cFallFirstSixMonths)
	if !pick(g.rng, g.profile.Diabetes.AtTargetAfterOneYear.Value) {
		fall *= 0.35 // the ones who do not get there
	}

	// Sampled once per patient, like height: a person either responds to treatment or does
	// not, and four independent draws would make the same person do both.
	grown := growthOverYear(g.rng, p.AgeYears, p.Presenting)
	trend := g.rng.Float64() * 0.06 // adults lose up to 6% of body weight over the year

	start := g.asOf.AddDate(-1, 0, 0)

	for i := 0; i < count; i++ {
		months := i * 3
		v := Visit{
			Date:     start.AddDate(0, months, g.rng.Intn(10)),
			Number:   i + 1,
			Attended: true,
		}

		if lost && i >= lostAfter {
			v.Attended = false
			v.Note = "did not attend"
			p.Visits = append(p.Visits, v)
			continue
		}

		if p.Diabetes != nil {
			// The fall is front-loaded: most of it happens in the first six months, which is
			// what the clinician described.
			// A function of elapsed months, not a running value carried between visits.
			// It was written as one — a `current` initialised outside the loop from the
			// baseline — and the initialisation was dead, because the first thing the loop
			// did was overwrite it. The linter caught the dead store; the misleading scope
			// was the part worth fixing.
			progress := 1 - math.Exp(-float64(months)/4.0)
			value := p.Diabetes.BaselineHbA1c - fall*progress + g.rng.NormFloat64()*0.25
			hba1c := round1(clamp(value, 4.8, 16.0))

			// Not every visit has every test. Missed, delayed or unaffordable — the profile
			// asks for this explicitly, and a screen that has never met a nil has never been
			// tested.
			if !pick(g.rng, 0.18) {
				v.HbA1c = ptr(hba1c)
			}
			if !pick(g.rng, 0.25) {
				v.FastingGlucose = ptr(round1(clamp(hba1c*1.6-2.2+g.rng.NormFloat64()*0.8, 3.5, 22.0)))
			}
			if forced == "hypoglycaemia" && i == count-1 {
				v.FastingGlucose = ptr(round1(2.6 + g.rng.Float64()*0.7))
				v.Note = "symptomatic hypoglycaemia reported"
			}
			if forced == "conflicting_or_missing_labs" {
				switch i {
				case 0:
					v.HbA1c, v.FastingGlucose = nil, nil
				case 1:
					v.HbA1c = ptr(5.6) // disagrees with the glucose beside it, on purpose
					v.FastingGlucose = ptr(16.4)
					v.Note = "HbA1c and glucose inconsistent — repeat requested"
				}
			}
		}

		// Weight moves along a trajectory from one height, not from a height redrawn each time.
		// Adults on treatment drift down slowly; children grow into theirs.
		if p.BMI > 0 && p.HeightM > 0 && !pick(g.rng, 0.2) {
			height := p.HeightM
			if p.AgeYears < 18 {
				height += grown * float64(months) / 12.0 / 100.0
			}
			drift := 1.0
			if p.AgeYears >= 18 {
				drift = 1 - trend*float64(months)/12.0
			}
			// Noise is proportional, not absolute: 400 grams is measurement scatter on a
			// ninety-kilogram adult and a fortnight's growth on a fifteen-kilogram toddler.
			weight := p.BMI * drift * height * height
			v.Weight = ptr(round1(clamp(weight*(1+g.rng.NormFloat64()*0.006), 3, 220)))
		}

		// Height at every paediatric visit: growth velocity is the reading.
		if p.AgeYears < 18 && p.HeightM > 0 {
			v.HeightCm = ptr(round1(p.HeightM*100 + grown*float64(months)/12.0))
		}

		p.Visits = append(p.Visits, v)
	}

	if forced == "treatment_intolerance" && len(p.Visits) > 1 {
		p.Visits[1].Note = "metformin stopped — gastrointestinal intolerance"
	}
}

// --- small helpers ---

func ptr[T any](v T) *T { return &v }

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }

func clamp(v, low, high float64) float64 {
	return math.Max(low, math.Min(high, v))
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
