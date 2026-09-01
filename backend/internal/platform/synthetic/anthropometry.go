package synthetic

import (
	"math"
	"math/rand"
)

/*
 * Height, weight and growth.
 *
 * This file exists because of a defect the review page made obvious the first time it was
 * rendered: weight was computed at each visit from a height redrawn at each visit, so a
 * patient went 73.5 kg → 58.5 kg between two appointments three months apart. Nobody would
 * have caught that in a distribution check — the mean weight was fine.
 *
 * Height is therefore a property of the person, sampled once. Weight follows from height and
 * BMI, and moves over the visit series as a trajectory.
 *
 * CLINICAL REVIEW WANTED. The figures below are approximate population values for Bangladesh
 * assembled for plausibility, not measurements: adult mean stature near 1.63 m for men and
 * 1.51 m for women, and a paediatric curve interpolated between the knots in childHeightCm.
 * They are the numbers most worth a clinician's eye, because a growth clinic's entire purpose
 * is reading them.
 */

const (
	adultMaleHeightM   = 1.63
	adultFemaleHeightM = 1.51
	adultHeightSDM     = 0.065
)

// childHeightKnots is median stature in centimetres at each age in years.
//
// Interpolated between rather than modelled: a real growth reference is a set of centile
// curves that would have to be shipped and cited, and the generator needs plausible heights
// rather than a reference implementation. Boys and girls are separated only after eleven,
// where the curves genuinely diverge.
var childHeightKnots = []struct {
	age       float64
	boy, girl float64
}{
	{2, 86, 85},
	{4, 100, 99},
	{6, 112, 111},
	{8, 123, 122},
	{10, 132, 132},
	{12, 141, 145},
	{14, 155, 154},
	{16, 165, 157},
	{18, 168, 158},
}

// heightM gives this patient a stature, once, for life.
func heightM(rng *rand.Rand, age int, sex Sex, presenting PresentingProblem) float64 {
	var base float64

	if age >= 18 {
		base = adultMaleHeightM
		if sex == Female {
			base = adultFemaleHeightM
		}
		base += rng.NormFloat64() * adultHeightSDM
	} else {
		base = interpolateChildHeight(float64(age), sex)/100 + rng.NormFloat64()*0.045
	}

	// Short stature is why a growth patient is in the room. Generating them on the median
	// would make the whole paediatric caseload look like a well-child clinic.
	if presenting == ProblemGrowth {
		base *= 0.88 + rng.Float64()*0.06
	}

	return math.Round(clamp(base, 0.70, 1.95)*1000) / 1000
}

func interpolateChildHeight(age float64, sex Sex) float64 {
	value := func(i int) float64 {
		if sex == Female {
			return childHeightKnots[i].girl
		}
		return childHeightKnots[i].boy
	}

	if age <= childHeightKnots[0].age {
		return value(0) * age / childHeightKnots[0].age
	}
	for i := 1; i < len(childHeightKnots); i++ {
		if age <= childHeightKnots[i].age {
			lo, hi := childHeightKnots[i-1], childHeightKnots[i]
			t := (age - lo.age) / (hi.age - lo.age)
			return value(i-1) + t*(value(i)-value(i-1))
		}
	}
	return value(len(childHeightKnots) - 1)
}

// childBMIKnots is median BMI and the obesity threshold at each age in years.
//
// The shape matters more than the exact figures: BMI falls through early childhood, bottoms
// out near six at adiposity rebound, and climbs to the adult value by eighteen. A straight
// line through those ages — which is what this had first — puts a healthy toddler in the
// overweight band and lets a BMI of 25 pass as ordinary obesity at seven, which is nearer the
// 99.9th centile.
//
// CLINICAL REVIEW WANTED. These are shaped after the international age-and-sex cut-offs
// rather than copied from them; a paediatric eye on the obesity column would be worth more
// than anything else on this page.
var childBMIKnots = []struct{ age, median, obese float64 }{
	{2, 16.4, 20.1},
	{4, 15.7, 19.3},
	{6, 15.5, 19.8},
	{8, 16.1, 21.6},
	{10, 17.0, 24.0},
	{12, 18.2, 26.0},
	{14, 19.4, 27.6},
	{16, 20.5, 28.9},
	{18, 21.3, 30.0},
}

// childBMI is a plausible BMI for a child of this age.
//
// Not the adult cut-offs: a BMI of 17 is normal at six and underweight at sixteen, which is
// exactly the kind of thing a system that treats children as small adults gets wrong.
func childBMI(rng *rand.Rand, age int, obese bool) float64 {
	median, threshold := interpolateChildBMI(float64(age))

	if obese {
		return round1(threshold + rng.Float64()*3.5)
	}
	// Bounded below the obesity threshold rather than clamped to a multiple of the median, so
	// a child who was not drawn as obese cannot arrive at an obese BMI by noise.
	return round1(clamp(median+rng.NormFloat64()*1.5, 11.5, threshold-0.2))
}

func interpolateChildBMI(age float64) (median, obese float64) {
	knots := childBMIKnots
	if age <= knots[0].age {
		return knots[0].median, knots[0].obese
	}
	for i := 1; i < len(knots); i++ {
		if age <= knots[i].age {
			lo, hi := knots[i-1], knots[i]
			t := (age - lo.age) / (hi.age - lo.age)
			return lo.median + t*(hi.median-lo.median), lo.obese + t*(hi.obese-lo.obese)
		}
	}
	last := knots[len(knots)-1]
	return last.median, last.obese
}

// growthOverYear is how much a child of this age grows in twelve months, in centimetres.
//
// Roughly six centimetres a year through childhood, more through the pubertal spurt, tapering
// to nothing by eighteen. It is the number a growth clinic is actually watching, so a series
// of visits that does not move is a series that cannot be reviewed.
func growthOverYear(rng *rand.Rand, age int, presenting PresentingProblem) float64 {
	var cm float64
	switch {
	case age < 2:
		cm = 12
	case age < 10:
		cm = 6
	case age < 15:
		cm = 8 // the spurt
	case age < 18:
		cm = 3
	default:
		return 0
	}
	cm *= 0.8 + rng.Float64()*0.4

	// The referral reason: growing too slowly is what brought them in.
	if presenting == ProblemGrowth {
		cm *= 0.35
	}
	return cm
}
