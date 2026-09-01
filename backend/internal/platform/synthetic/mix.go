package synthetic

import (
	"fmt"
	"math"
)

/*
 * Who presents with what.
 *
 * The clinician gave three marginals — the presenting-problem mix, the 20% paediatric share,
 * and the 70% female share among adults — but not the joint distribution between them. This
 * table supplies it.
 *
 * IMPORTANT: unlike profile.v1.json, these numbers are NOT the clinician's. They are an
 * interpolation, and they are kept here rather than in the profile so that nothing invented
 * is ever attributed to him. What makes them safe is that they are constrained: reconcile()
 * checks that the mixture they imply reproduces the three shares he did give, and a test
 * fails if an edit breaks that. So the table can be argued with clinically, but it cannot
 * silently drift away from the profile.
 *
 * Drawing this way round — problem first, person second — is what fixed the earlier version.
 * Drawing the person first and then restricting the problem to what was possible for them
 * diluted every sex-specific category: PCOS came out at 3.5% against a stated 7%, and male
 * sexual/reproductive at 1.7% against the same 7%. Conditioning in this direction makes each
 * presenting share exact by construction and pushes the approximation into the demographics,
 * where reconcile() can measure it.
 */

// stratum is the demographic shape of one presenting problem.
type stratum struct {
	// paediatric is the share of this problem's patients who are under 18.
	paediatric float64
	// adultFemale is the share of the *adults* with this problem who are female.
	adultFemale float64
	// childFemale is the same for the children. Forced to 1 or 0 where the problem is
	// sex-specific.
	childFemale float64
	// minChildAge refuses a two-year-old with PCOS. Only meaningful where paediatric > 0.
	minChildAge int
	// maxAdultAge caps the adult age where the problem is confined to part of life. Zero
	// means no cap. PCOS in a woman of fifty-five is not PCOS, it is a different conversation,
	// and generating one tells a reviewer the data was assembled field by field.
	maxAdultAge int
}

// caseMix maps each presenting problem to its demographic shape.
//
// The reasoning, problem by problem, is the part worth reviewing clinically:
//
//   - obesityMetabolic carries most of the paediatric load. It is the largest presentation
//     overall and childhood obesity is the fastest-growing paediatric endocrine referral in
//     urban Bangladesh, so 31% of it being under 18 is what makes the stated 20% paediatric
//     share reachable at all without inventing children elsewhere.
//   - growthPuberty is 90% paediatric; the adult tenth is short stature and hypogonadism
//     presenting late, which does happen and is worth having in the data.
//   - boneCalciumVitaminD is 55% paediatric: nutritional rickets and vitamin D deficiency.
//   - diabetes is only 3% paediatric. Type 1 is 2% of his diabetes caseload, and every
//     child with diabetes here is type 1, so a larger paediatric share would overproduce
//     type 1 no matter what the type sampler did. This is the constraint that was breaking
//     the earlier version.
//   - thyroid at 0.80 adult female comes from the profile itself (thyroid.femaleShare);
//     it is repeated here rather than read, so that reconcile() can check the whole table
//     against the whole profile in one place.
var caseMix = map[string]stratum{
	"diabetes":               {paediatric: 0.03, adultFemale: 0.57, childFemale: 0.50, minChildAge: 2},
	"thyroid":                {paediatric: 0.13, adultFemale: 0.80, childFemale: 0.60, minChildAge: 2},
	"obesityMetabolic":       {paediatric: 0.31, adultFemale: 0.83, childFemale: 0.50, minChildAge: 4},
	"pcos":                   {paediatric: 0.05, adultFemale: 1.00, childFemale: 1.00, minChildAge: 13, maxAdultAge: 45},
	"growthPuberty":          {paediatric: 0.90, adultFemale: 0.50, childFemale: 0.45, minChildAge: 2, maxAdultAge: 35},
	"boneCalciumVitaminD":    {paediatric: 0.55, adultFemale: 0.80, childFemale: 0.50, minChildAge: 2},
	"adrenal":                {paediatric: 0.30, adultFemale: 0.55, childFemale: 0.50, minChildAge: 2},
	"pituitary":              {paediatric: 0.25, adultFemale: 0.55, childFemale: 0.50, minChildAge: 2},
	"maleSexualReproductive": {paediatric: 0.06, adultFemale: 0.00, childFemale: 0.00, minChildAge: 13},
}

// problemOrder fixes the iteration order of the presenting-problem draw.
//
// Map iteration in Go is randomised, so sampling straight from the profile map would make
// the same seed give different people on different runs — which would quietly destroy the
// one property the generator exists to have.
var problemOrder = []string{
	"diabetes", "thyroid", "obesityMetabolic", "pcos", "growthPuberty",
	"boneCalciumVitaminD", "adrenal", "pituitary", "maleSexualReproductive",
}

// reconcile checks that caseMix, mixed over the profile's presenting shares, reproduces the
// paediatric and adult-female shares the clinician stated.
//
// Tolerance is absolute and deliberately tight. It is not a statistical tolerance — no
// sampling happens here, this is arithmetic on the table — so anything beyond a rounding
// residue means the table and the profile disagree about the clinic.
func reconcile(p *Profile, tolerance float64) error {
	var paediatricMass, adultMass, adultFemaleMass float64

	for _, key := range problemOrder {
		share, ok := p.PresentingProblem[key]
		if !ok {
			return fmt.Errorf("profile has no presenting share for %q", key)
		}
		s, ok := caseMix[key]
		if !ok {
			return fmt.Errorf("case mix has no stratum for %q", key)
		}
		paediatricMass += share * s.paediatric
		adultMass += share * (1 - s.paediatric)
		adultFemaleMass += share * (1 - s.paediatric) * s.adultFemale
	}

	if adultMass <= 0 {
		return fmt.Errorf("case mix leaves no adults")
	}

	if diff := math.Abs(paediatricMass - p.Population.PaediatricShare); diff > tolerance {
		return fmt.Errorf("case mix implies %.4f paediatric, profile states %.4f (off by %.4f)",
			paediatricMass, p.Population.PaediatricShare, diff)
	}

	adultFemale := adultFemaleMass / adultMass
	if diff := math.Abs(adultFemale - p.Population.AdultFemaleShare); diff > tolerance {
		return fmt.Errorf("case mix implies %.4f adult female, profile states %.4f (off by %.4f)",
			adultFemale, p.Population.AdultFemaleShare, diff)
	}

	m := computeMasses(p)

	if stated, ok := p.Comorbidity["diabetesAndThyroid"]; ok {
		if diff := math.Abs(m.overlap - stated.Value); diff > tolerance {
			return fmt.Errorf(
				"cross-over rates imply %.4f of the caseload has both diabetes and thyroid "+
					"disease, profile states %.4f (off by %.4f)", m.overlap, stated.Value, diff)
		}
	}

	if _, err := adultType1Rate(p, m); err != nil {
		return err
	}
	return nil
}

// conditionalShare answers "given that a share of the population is already known to have
// this, how often must it appear in the rest for the total to come out right".
//
// Used wherever the clinician gave a whole-caseload figure that a subgroup already satisfies:
// half his caseload is obese but a third present *for* obesity, so the remaining two-thirds
// cannot simply be sampled at one half again — that lands at 67%. Same shape for insulin,
// which every type 1 patient is already on.
func conditionalShare(total, alreadyCovered float64) float64 {
	if alreadyCovered >= 1 {
		return 0
	}
	return clamp((total-alreadyCovered)/(1-alreadyCovered), 0, 1)
}

/*
 * Co-occurrence.
 *
 * The clinician gave one number here — 20% of the *total* caseload has both diabetes and
 * thyroid disease — and it is a demanding one. Reaching it means roughly half of everyone
 * who arrives with either condition also has the other. That is high by international
 * standards, and it is his answer about his own clinic, so it is honoured rather than
 * softened. reconcile() checks these rates against it; if he revises the 20%, loading the
 * new profile fails until these move with it.
 *
 * Paediatric rates are separate and much smaller. Applying the adult rates to children gave
 * every ten-year-old presenting with obesity a 45% chance of acquiring diabetes, which the
 * type sampler then had to call type 1 — and type 1 came out at four times its stated share.
 */
const (
	adultObesityToDiabetes = 0.45
	adultThyroidToDiabetes = 0.50
	adultDiabetesToThyroid = 0.46
	adultObesityToThyroid  = 0.14

	childObesityToDiabetes = 0.015
	childThyroidToDiabetes = 0.015
	childDiabetesToThyroid = 0.02
	childObesityToThyroid  = 0.03
)

// diabetesRate is the chance a patient presenting with problem j turns out to have diabetes.
func diabetesRate(problem string, child bool) float64 {
	switch problem {
	case "diabetes":
		return 1
	case "obesityMetabolic":
		if child {
			return childObesityToDiabetes
		}
		return adultObesityToDiabetes
	case "thyroid":
		if child {
			return childThyroidToDiabetes
		}
		return adultThyroidToDiabetes
	}
	return 0
}

// thyroidRate is the same for thyroid disease.
func thyroidRate(problem string, child bool) float64 {
	switch problem {
	case "thyroid":
		return 1
	case "diabetes":
		if child {
			return childDiabetesToThyroid
		}
		return adultDiabetesToThyroid
	case "obesityMetabolic":
		if child {
			return childObesityToThyroid
		}
		return adultObesityToThyroid
	}
	return 0
}

// masses is the analytic shape of the population the rates above imply.
//
// Computed rather than measured, and computed from the same constants the sampler uses, so
// it cannot drift away from what the generator actually does. It exists because two figures
// in the profile are properties of the *whole caseload* — the diabetes/thyroid overlap and
// the 2% type 1 share — and neither can be reached by sampling each patient in isolation.
type masses struct {
	diabetes      float64 // share of the whole caseload with diabetes
	childDiabetes float64 // of the whole caseload: children with diabetes
	adultDiabetes float64
	overlap       float64 // share with both diabetes and thyroid disease
}

func computeMasses(p *Profile) masses {
	var m masses
	for _, key := range problemOrder {
		w := p.PresentingProblem[key]
		s := caseMix[key]

		adult, child := w*(1-s.paediatric), w*s.paediatric

		m.adultDiabetes += adult * diabetesRate(key, false)
		m.childDiabetes += child * diabetesRate(key, true)

		m.overlap += adult * diabetesRate(key, false) * thyroidRate(key, false)
		m.overlap += child * diabetesRate(key, true) * thyroidRate(key, true)
	}
	m.diabetes = m.adultDiabetes + m.childDiabetes
	return m
}

// paediatricType1Share is how much of the paediatric diabetes caseload is type 1.
//
// Not 1.0: type 2 in an obese adolescent is real and rising in South Asia, and a system that
// has never seen one will not handle the first. The residue is small enough that using it as
// a constant here, rather than deriving it from the age distribution, costs less accuracy
// than the sampling noise of any population small enough to review by hand.
const paediatricType1Share = 0.93

// adultType1Rate solves for the rate at which adults must draw type 1 so that the whole
// diabetes caseload lands on the profile's figure.
//
// Every child with diabetes here is type 1 (mostly — see above), and children are a fixed
// share of the caseload. Drawing 2% for adults as well puts the caseload at twice its stated
// type 1 share, which is exactly what the earlier version did. Subtracting the paediatric
// mass first is the fix.
//
// A negative result means the profile is over-constrained — more children with diabetes than
// the stated type 1 share can accommodate — and is reported rather than clamped silently.
func adultType1Rate(p *Profile, m masses) (float64, error) {
	target := p.Diabetes.Type1 * m.diabetes
	fromChildren := paediatricType1Share * m.childDiabetes

	if m.adultDiabetes <= 0 {
		return 0, fmt.Errorf("case mix leaves no adults with diabetes")
	}
	rate := (target - fromChildren) / m.adultDiabetes
	if rate < 0 {
		return 0, fmt.Errorf(
			"profile states type 1 is %.1f%% of the diabetes caseload, but the case mix puts "+
				"%.2f%% of the whole caseload in children with diabetes, which alone exceeds it — "+
				"lower the paediatric diabetes shares in mix.go or raise the type 1 share",
			p.Diabetes.Type1*100, m.childDiabetes*100)
	}
	return rate, nil
}
