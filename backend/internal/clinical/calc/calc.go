// Package calc is one correct implementation of every derived clinical value (CP43,
// blueprint §3, §6.4).
//
// # Why this package exists twice
//
// P-4 wants derived values on screen the instant an operator types the inputs, which means
// computing them on the client. The server has to compute them too, because the client is
// not authoritative about anything. **Two implementations of the CKD-EPI equation that
// disagree is a patient-safety bug**, so the two are held together by a shared fixture file
// — `packages/clinical-calc/fixtures/*.json` — that both test suites consume, and a CI job
// that fails if either disagrees with it.
//
// The fixtures are not "tests we wrote". Every one of them is a worked example from the
// paper the formula comes from, or a value computed by hand from the published equation and
// checked digit for digit. A fixture invented by reading this code would prove only that
// the code agrees with itself.
//
// # Every function is versioned
//
// A derived value stored in a record was computed by a *particular* version of a formula,
// and that version is stored beside it (migration 00027). Formulas change: CKD-EPI was
// revised in 2021 to remove the race coefficient, and a system that silently recomputed old
// values with the new equation would rewrite history. So `Version` is part of every result
// and the read model refuses a derived value that does not carry one.
//
// # Refusing rather than guessing
//
// Every function returns `(Result, error)`. Criterion 4: an invalid input returns an explicit
// "cannot compute" rather than a wrong number. A BMI from a height of zero is +Inf, which
// serialises to `null` in JSON and renders as an empty cell — a wrong answer that looks like
// a missing one.
package calc

import (
	"errors"
	"fmt"
	"math"
)

// Sex is what several of these equations need. Not a social category here: the coefficients
// in CKD-EPI and Mifflin-St Jeor are fitted to measured differences in creatinine generation
// and lean mass, and the papers report them this way.
type Sex string

const (
	Female Sex = "female"
	Male   Sex = "male"
	// Other is what `core.patient.sex` allows besides the two. None of these equations has a
	// coefficient for it, and inventing one would be inventing clinical evidence — so the
	// functions that need a sex refuse it, and the interface asks. See ErrSexUnsupported.
	Other Sex = "other"
)

// Result is a derived value with everything needed to store it: the number, its unit, the
// formula that produced it and that formula's version.
type Result struct {
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
	// Formula is the name stored on the derived value, e.g. "ckd_epi_2021".
	Formula string `json:"formula"`
	// Version is that formula's version in this library. Bumped when the arithmetic
	// changes, never when a comment does.
	Version string `json:"version"`
}

// The refusals. Each is a distinct sentence an interface can show, because "we need the
// patient's sex" and "that height cannot be right" send a person to two different fields.
var (
	// ErrNotPositive is a height, weight or creatinine of zero or less.
	ErrNotPositive = errors.New("calc: that measurement must be greater than zero")
	// ErrOutOfRange is an input outside the band the formula was fitted on. Not a
	// plausibility check — CP42 has already refused a typing error — but the edge beyond
	// which the equation's own paper does not claim to apply.
	ErrOutOfRange = errors.New("calc: that value is outside the range this formula covers")
	// ErrSexUnsupported is an equation whose published coefficients cover two sexes being
	// asked for a third. Refusing is the honest answer: choosing one would be inventing a
	// coefficient, and choosing the "average" would be inventing two.
	ErrSexUnsupported = errors.New("calc: this equation has no published coefficient for that sex")
	// ErrMissingInput is a formula asked to run without something it needs.
	ErrMissingInput = errors.New("calc: that value cannot be computed without all of its inputs")
)

// --- body mass index ---

// BMIVersion is bumped when the arithmetic changes.
const BMIVersion = "1.0.0"

// BMI is weight in kilograms over height in metres squared.
//
// Source: the definition itself (Quetelet index; WHO Technical Report Series 894, 2000).
// There is no coefficient to get wrong, which makes it the useful case for proving the
// *machinery* — the parity harness, the versioning, the refusals — before the equations
// where an error would be invisible.
func BMI(weightKg, heightCm float64) (Result, error) {
	if weightKg <= 0 || heightCm <= 0 {
		return Result{}, ErrNotPositive
	}
	metres := heightCm / 100
	return Result{
		Value: weightKg / (metres * metres), Unit: "kg/m2",
		Formula: "bmi", Version: BMIVersion,
	}, nil
}

// ObesityClass is §3 step 2's classification.
type ObesityClass string

const (
	Underweight ObesityClass = "underweight"
	Normal      ObesityClass = "normal"
	Overweight  ObesityClass = "overweight"
	ObeseI      ObesityClass = "obese_i"
	ObeseII     ObesityClass = "obese_ii"
	ObeseIII    ObesityClass = "obese_iii"
)

// ClassifyVersion is bumped when a cut-off moves.
const ClassifyVersion = "1.0.0"

// Classify puts a BMI into a band, on either the international or the Asian cut-offs.
//
// **This clinic uses the Asian cut-offs**, and the difference is not cosmetic: a BMI of 24
// is "normal" internationally and "overweight" in a Bangladeshi patient, and the whole
// screening pathway hangs on which side of that line somebody falls.
//
// Sources:
//   - International: WHO Technical Report Series 894 (2000), Table 2.1.
//   - Asian: WHO expert consultation, "Appropriate body-mass index for Asian populations and
//     its implications for policy and intervention strategies", Lancet 2004;363:157–163.
//     Public-health action points at 23.0 and 27.5; this library uses the widely-applied
//     clinical banding derived from them (23–24.9 overweight, 25–29.9 obese I, ≥30 obese II),
//     which is what the Bangladesh Endocrine Society and the clinic's own protocol use.
//
// The Asian scale has no obese III band: the papers do not define one, and inventing a
// boundary at 40 to make the two scales symmetrical would be inventing a cut-off.
func Classify(bmi float64, asian bool) (ObesityClass, string, error) {
	if bmi <= 0 {
		return "", "", ErrNotPositive
	}
	if asian {
		switch {
		case bmi < 18.5:
			return Underweight, ClassifyVersion, nil
		case bmi < 23:
			return Normal, ClassifyVersion, nil
		case bmi < 25:
			return Overweight, ClassifyVersion, nil
		case bmi < 30:
			return ObeseI, ClassifyVersion, nil
		default:
			return ObeseII, ClassifyVersion, nil
		}
	}
	switch {
	case bmi < 18.5:
		return Underweight, ClassifyVersion, nil
	case bmi < 25:
		return Normal, ClassifyVersion, nil
	case bmi < 30:
		return Overweight, ClassifyVersion, nil
	case bmi < 35:
		return ObeseI, ClassifyVersion, nil
	case bmi < 40:
		return ObeseII, ClassifyVersion, nil
	default:
		return ObeseIII, ClassifyVersion, nil
	}
}

// --- waist-hip ratio ---

const WHRVersion = "1.0.0"

// WHR is waist circumference over hip circumference, both in the same unit.
//
// Source: WHO, "Waist Circumference and Waist–Hip Ratio: Report of a WHO Expert
// Consultation", Geneva, 8–11 December 2008. The ratio itself is a division; the risk
// cut-offs (≥0.90 in men, ≥0.85 in women) belong to CP58's risk scoring, not here — this
// library computes, it does not interpret.
func WHR(waistCm, hipCm float64) (Result, error) {
	if waistCm <= 0 || hipCm <= 0 {
		return Result{}, ErrNotPositive
	}
	return Result{
		Value: waistCm / hipCm, Unit: "1",
		Formula: "whr", Version: WHRVersion,
	}, nil
}

// --- basal metabolic rate ---

const (
	MifflinVersion        = "1.0.0"
	HarrisBenedictVersion = "1.0.0"
)

// BMRMifflin is the Mifflin-St Jeor equation, and the clinic's default.
//
// Source: Mifflin MD, St Jeor ST, Hill LA, Scott BJ, Daugherty SA, Koh YO. "A new predictive
// equation for resting energy expenditure in healthy individuals." Am J Clin Nutr
// 1990;51(2):241–247.
//
//	men:   10·W + 6.25·H − 5·A + 5
//	women: 10·W + 6.25·H − 5·A − 161
//
// **Chosen over Harris-Benedict**, which is the open decision this checkpoint carried. The
// reason: Harris-Benedict was fitted in 1919 on a cohort that does not resemble a modern
// population and overestimates resting expenditure by roughly 5% in most groups; Mifflin-St
// Jeor is the equation the Academy of Nutrition and Dietetics recommends for non-obese and
// obese adults alike, and it is what a nutritionist trained in the last thirty years will
// expect. Harris-Benedict stays available because a clinician may want to compare.
func BMRMifflin(weightKg, heightCm, ageYears float64, sex Sex) (Result, error) {
	if weightKg <= 0 || heightCm <= 0 {
		return Result{}, ErrNotPositive
	}
	if ageYears < 0 || ageYears > 130 {
		return Result{}, ErrOutOfRange
	}
	base := 10*weightKg + 6.25*heightCm - 5*ageYears
	switch sex {
	case Male:
		base += 5
	case Female:
		base -= 161
	default:
		return Result{}, ErrSexUnsupported
	}
	return Result{
		Value: base, Unit: "kcal/d",
		Formula: "bmr_mifflin_st_jeor", Version: MifflinVersion,
	}, nil
}

// BMRHarrisBenedict is the revised Harris-Benedict equation.
//
// Source: Roza AM, Shizgal HM. "The Harris Benedict equation reevaluated: resting energy
// requirements and the body cell mass." Am J Clin Nutr 1984;40(1):168–182. These are the
// 1984 revised coefficients, not the 1919 originals — a distinction worth keeping, because
// "Harris-Benedict" in a textbook may mean either.
//
//	men:   88.362 + 13.397·W + 4.799·H − 5.677·A
//	women: 447.593 + 9.247·W + 3.098·H − 4.330·A
func BMRHarrisBenedict(weightKg, heightCm, ageYears float64, sex Sex) (Result, error) {
	if weightKg <= 0 || heightCm <= 0 {
		return Result{}, ErrNotPositive
	}
	if ageYears < 0 || ageYears > 130 {
		return Result{}, ErrOutOfRange
	}
	var value float64
	switch sex {
	case Male:
		value = 88.362 + 13.397*weightKg + 4.799*heightCm - 5.677*ageYears
	case Female:
		value = 447.593 + 9.247*weightKg + 3.098*heightCm - 4.330*ageYears
	default:
		return Result{}, ErrSexUnsupported
	}
	return Result{
		Value: value, Unit: "kcal/d",
		Formula: "bmr_harris_benedict_revised", Version: HarrisBenedictVersion,
	}, nil
}

// --- ideal body weight ---

const IBWVersion = "1.0.0"

// IdealBodyWeight is the Devine formula.
//
// Source: Devine BJ. "Gentamicin therapy." Drug Intell Clin Pharm 1974;8:650–655. Widely
// used for drug dosing rather than for telling a patient what to weigh, which is worth
// remembering when it appears on a screen: it is a dosing weight, and a nutrition plan built
// from it would be a nutrition plan built from a pharmacokinetics convention.
//
//	men:   50.0 + 2.3 kg per inch over 5 feet
//	women: 45.5 + 2.3 kg per inch over 5 feet
//
// Below 152.4 cm the formula extrapolates downward, which Devine never intended. It is
// allowed here down to 120 cm and refused below that, because a negative ideal weight is
// worse than no answer.
func IdealBodyWeight(heightCm float64, sex Sex) (Result, error) {
	if heightCm <= 0 {
		return Result{}, ErrNotPositive
	}
	if heightCm < 120 {
		return Result{}, ErrOutOfRange
	}
	inchesOverFiveFeet := (heightCm - 152.4) / 2.54
	var base float64
	switch sex {
	case Male:
		base = 50.0
	case Female:
		base = 45.5
	default:
		return Result{}, ErrSexUnsupported
	}
	return Result{
		Value: base + 2.3*inchesOverFiveFeet, Unit: "kg",
		Formula: "ibw_devine", Version: IBWVersion,
	}, nil
}

// --- body surface area ---

const (
	DuBoisVersion    = "1.0.0"
	MostellerVersion = "1.0.0"
)

// BSADuBois is the Du Bois and Du Bois formula: 0.007184 · H^0.725 · W^0.425.
//
// Source: Du Bois D, Du Bois EF. "A formula to estimate the approximate surface area if
// height and weight be known." Arch Intern Med 1916;17:863–871.
func BSADuBois(weightKg, heightCm float64) (Result, error) {
	if weightKg <= 0 || heightCm <= 0 {
		return Result{}, ErrNotPositive
	}
	return Result{
		Value:   0.007184 * math.Pow(heightCm, 0.725) * math.Pow(weightKg, 0.425),
		Unit:    "m2",
		Formula: "bsa_du_bois", Version: DuBoisVersion,
	}, nil
}

// BSAMosteller is the square-root formula: √(H·W / 3600).
//
// Source: Mosteller RD. "Simplified calculation of body-surface area." N Engl J Med
// 1987;317(17):1098. Agrees with Du Bois to within about 2% over the ordinary range and is
// the one most oncology protocols specify, which is why both are here.
func BSAMosteller(weightKg, heightCm float64) (Result, error) {
	if weightKg <= 0 || heightCm <= 0 {
		return Result{}, ErrNotPositive
	}
	return Result{
		Value: math.Sqrt(heightCm * weightKg / 3600), Unit: "m2",
		Formula: "bsa_mosteller", Version: MostellerVersion,
	}, nil
}

// --- kidney function ---

const (
	CKDEPIVersion   = "2021.1"
	SchwartzVersion = "2009.1"
)

// EGFRCKDEPI2021 is the race-free CKD-EPI creatinine equation (§6.4).
//
// Source: Inker LA, Eneanya ND, Coresh J, et al. "New Creatinine- and Cystatin C-Based
// Equations to Estimate GFR without Race." N Engl J Med 2021;385:1737–1749.
//
//	eGFR = 142 · min(Scr/κ, 1)^α · max(Scr/κ, 1)^−1.200 · 0.9938^age · 1.012 [if female]
//	κ = 0.7 (female), 0.9 (male);  α = −0.241 (female), −0.302 (male)
//
// **Creatinine is in mg/dL** here, because that is the unit the equation is published in.
// CP42 stores creatinine canonically in µmol/L, so the caller converts — and the conversion
// is the database's, not a second constant in this file. Two copies of 88.42 is one copy
// that drifts.
//
// The 2021 revision exists because the earlier equation carried a race coefficient that
// raised the estimate for Black patients with no physiological justification, and delayed
// referrals as a result. The version string says 2021 for exactly this reason: a value
// computed under the old equation must stay identifiable as such.
func EGFRCKDEPI2021(creatinineMgDL, ageYears float64, sex Sex) (Result, error) {
	if creatinineMgDL <= 0 {
		return Result{}, ErrNotPositive
	}
	if ageYears < 18 {
		// The equation is fitted on adults. A paediatric estimate is Schwartz's, and
		// silently applying an adult equation to a child is how a normal result hides
		// renal impairment.
		return Result{}, ErrOutOfRange
	}
	if ageYears > 130 {
		return Result{}, ErrOutOfRange
	}

	var kappa, alpha, sexFactor float64
	switch sex {
	case Female:
		kappa, alpha, sexFactor = 0.7, -0.241, 1.012
	case Male:
		kappa, alpha, sexFactor = 0.9, -0.302, 1.0
	default:
		return Result{}, ErrSexUnsupported
	}

	ratio := creatinineMgDL / kappa
	value := 142 *
		math.Pow(math.Min(ratio, 1), alpha) *
		math.Pow(math.Max(ratio, 1), -1.200) *
		math.Pow(0.9938, ageYears) *
		sexFactor

	return Result{
		Value: value, Unit: "mL/min/{1.73_m2}",
		Formula: "egfr_ckd_epi_2021", Version: CKDEPIVersion,
	}, nil
}

// EGFRBedsideSchwartz is the paediatric estimate: 0.413 · height(cm) / Scr(mg/dL).
//
// Source: Schwartz GJ, Muñoz A, Schneider MF, et al. "New equations to estimate GFR in
// children with CKD." J Am Soc Nephrol 2009;20(3):629–637.
//
// **D-23 is still open** and it is not about this equation — the arithmetic is settled. It
// is about the *age at which a patient stops being a child for this purpose*: the paper's
// cohort is 1–16, adult CKD-EPI is fitted from 18, and the two do not meet. This library
// therefore refuses above 18 and leaves the boundary decision to the caller, visibly, rather
// than picking one quietly.
func EGFRBedsideSchwartz(creatinineMgDL, heightCm, ageYears float64) (Result, error) {
	if creatinineMgDL <= 0 || heightCm <= 0 {
		return Result{}, ErrNotPositive
	}
	if ageYears < 1 || ageYears >= 18 {
		return Result{}, ErrOutOfRange
	}
	return Result{
		Value: 0.413 * heightCm / creatinineMgDL, Unit: "mL/min/{1.73_m2}",
		Formula: "egfr_bedside_schwartz", Version: SchwartzVersion,
	}, nil
}

// --- smoking ---

const PackYearsVersion = "1.0.0"

// PackYears is (cigarettes per day ÷ 20) × years smoked.
//
// Twenty is the definition of a pack in this measure, not a property of any particular
// packet: the unit is standardised so that two clinicians counting the same patient get the
// same number.
func PackYears(cigarettesPerDay, years float64) (Result, error) {
	if cigarettesPerDay < 0 || years < 0 {
		return Result{}, ErrNotPositive
	}
	if cigarettesPerDay > 200 || years > 100 {
		return Result{}, ErrOutOfRange
	}
	return Result{
		Value: (cigarettesPerDay / 20) * years, Unit: "1",
		Formula: "pack_years", Version: PackYearsVersion,
	}, nil
}

// Formulas is every formula this library implements, with its current version. The read
// model refuses a derived value naming a formula that is not here.
func Formulas() map[string]string {
	return map[string]string{
		"bmi":                         BMIVersion,
		"obesity_class":               ClassifyVersion,
		"whr":                         WHRVersion,
		"bmr_mifflin_st_jeor":         MifflinVersion,
		"bmr_harris_benedict_revised": HarrisBenedictVersion,
		"ibw_devine":                  IBWVersion,
		"bsa_du_bois":                 DuBoisVersion,
		"bsa_mosteller":               MostellerVersion,
		"egfr_ckd_epi_2021":           CKDEPIVersion,
		"egfr_bedside_schwartz":       SchwartzVersion,
		"pack_years":                  PackYearsVersion,
	}
}

// Round is how a derived value is rounded for display and for the parity fixtures.
//
// One function, used by both languages, because "round to one decimal" is ambiguous at a
// half and the two runtimes resolve it differently — Go's math.Round is half-away-from-zero
// and JavaScript's Math.round is half-up, which disagree on −0.5. Every derived value in
// this system is positive, so today they agree; the shared implementation is what keeps
// them agreeing when somebody adds a formula that is not.
func Round(v float64, decimals int) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return v
	}
	scale := math.Pow(10, float64(decimals))
	return math.Round(v*scale) / scale
}

func init() {
	// A formula in the map with no version is a formula whose derived values could not be
	// identified later. Cheap to check, and it runs once.
	for name, version := range Formulas() {
		if version == "" {
			panic(fmt.Sprintf("calc: formula %q has no version", name))
		}
	}
}
