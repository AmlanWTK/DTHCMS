/**
 * Derived clinical values, computed on the client (CP43, blueprint §3, §6.4).
 *
 * # Why this exists twice
 *
 * P-4 wants a BMI on screen the instant an operator types a height and a weight — no round
 * trip, no spinner, and correct on a tablet with no signal. The server computes the same
 * values because the client is not authoritative about anything.
 *
 * **Two implementations of the CKD-EPI equation that disagree is a patient-safety bug.** So
 * this file and its Go twin (`backend/internal/clinical/calc`) are held together by
 * `fixtures/reference.json`, which both test suites consume, and a CI job that fails if
 * either disagrees with it. The fixture values were computed independently from the
 * published equations — a fixture read off either implementation would prove only that the
 * implementation agrees with itself.
 *
 * # Every function is versioned
 *
 * A derived value stored in a record was computed by a *particular* version of a formula,
 * and that version is stored beside it. Formulas change: CKD-EPI was revised in 2021 to
 * remove its race coefficient, and a system that silently recomputed old values with the new
 * equation would rewrite history.
 *
 * # Refusing rather than guessing
 *
 * Every function returns a result *or* a refusal, never a number it is not entitled to.
 * Criterion 4: an invalid input says "cannot compute". A BMI from a height of zero is
 * `Infinity`, which renders as an empty cell — a wrong answer that looks like a missing one.
 */

/** What several of these equations need. */
export type Sex = 'female' | 'male' | 'other';

/** A derived value with everything needed to store it. */
export interface CalcResult {
  value: number;
  unit: string;
  /** The formula name stored on the derived value, e.g. `ckd_epi_2021`. */
  formula: string;
  /** That formula's version in this library. Bumped when the arithmetic changes. */
  version: string;
}

/**
 * Why a value could not be computed.
 *
 * The four are distinct because "we need the patient's sex" and "that height cannot be
 * right" send a person to two different fields.
 */
export type CalcRefusalReason =
  'not_positive' | 'out_of_range' | 'sex_unsupported' | 'missing_input';

export interface CalcRefusal {
  ok: false;
  reason: CalcRefusalReason;
}

export interface CalcSuccess {
  ok: true;
  result: CalcResult;
}

export type Calculated = CalcSuccess | CalcRefusal;

const ok = (result: CalcResult): CalcSuccess => ({ ok: true, result });
const no = (reason: CalcRefusalReason): CalcRefusal => ({ ok: false, reason });

// --- versions ---

export const BMI_VERSION = '1.0.0';
export const CLASSIFY_VERSION = '1.0.0';
export const WHR_VERSION = '1.0.0';
export const MIFFLIN_VERSION = '1.0.0';
export const HARRIS_BENEDICT_VERSION = '1.0.0';
export const IBW_VERSION = '1.0.0';
export const DU_BOIS_VERSION = '1.0.0';
export const MOSTELLER_VERSION = '1.0.0';
export const CKD_EPI_VERSION = '2021.1';
export const SCHWARTZ_VERSION = '2009.1';
export const PACK_YEARS_VERSION = '1.0.0';

/** Every formula this library implements, with its current version. */
export const FORMULAS: Readonly<Record<string, string>> = Object.freeze({
  bmi: BMI_VERSION,
  obesity_class: CLASSIFY_VERSION,
  whr: WHR_VERSION,
  bmr_mifflin_st_jeor: MIFFLIN_VERSION,
  bmr_harris_benedict_revised: HARRIS_BENEDICT_VERSION,
  ibw_devine: IBW_VERSION,
  bsa_du_bois: DU_BOIS_VERSION,
  bsa_mosteller: MOSTELLER_VERSION,
  egfr_ckd_epi_2021: CKD_EPI_VERSION,
  egfr_bedside_schwartz: SCHWARTZ_VERSION,
  pack_years: PACK_YEARS_VERSION,
});

// --- body mass index ---

/**
 * Weight in kilograms over height in metres squared.
 *
 * Source: the definition itself (Quetelet index; WHO Technical Report Series 894, 2000).
 */
export function bmi(weightKg: number, heightCm: number): Calculated {
  if (weightKg <= 0 || heightCm <= 0) return no('not_positive');
  const metres = heightCm / 100;
  return ok({
    value: weightKg / (metres * metres),
    unit: 'kg/m2',
    formula: 'bmi',
    version: BMI_VERSION,
  });
}

export type ObesityClass =
  'underweight' | 'normal' | 'overweight' | 'obese_i' | 'obese_ii' | 'obese_iii';

export interface Classification {
  ok: true;
  class: ObesityClass;
  version: string;
}

/**
 * §3 step 2's classification, on either the international or the Asian cut-offs.
 *
 * **This clinic uses the Asian cut-offs**, and the difference is not cosmetic: a BMI of 24
 * is "normal" internationally and "overweight" in a Bangladeshi patient, and the whole
 * screening pathway hangs on which side of that line somebody falls.
 *
 * Sources:
 *  - International: WHO Technical Report Series 894 (2000), Table 2.1.
 *  - Asian: WHO expert consultation, Lancet 2004;363:157–163, with the clinical banding
 *    derived from its action points (23–24.9 overweight, 25–29.9 obese I, ≥30 obese II).
 *
 * The Asian scale has no obese III band: the papers do not define one, and inventing a
 * boundary at 40 to make the two scales symmetrical would be inventing a cut-off.
 */
export function classify(value: number, asian: boolean): Classification | CalcRefusal {
  if (value <= 0) return no('not_positive');
  const version = CLASSIFY_VERSION;
  if (asian) {
    if (value < 18.5) return { ok: true, class: 'underweight', version };
    if (value < 23) return { ok: true, class: 'normal', version };
    if (value < 25) return { ok: true, class: 'overweight', version };
    if (value < 30) return { ok: true, class: 'obese_i', version };
    return { ok: true, class: 'obese_ii', version };
  }
  if (value < 18.5) return { ok: true, class: 'underweight', version };
  if (value < 25) return { ok: true, class: 'normal', version };
  if (value < 30) return { ok: true, class: 'overweight', version };
  if (value < 35) return { ok: true, class: 'obese_i', version };
  if (value < 40) return { ok: true, class: 'obese_ii', version };
  return { ok: true, class: 'obese_iii', version };
}

// --- waist-hip ratio ---

/**
 * Waist over hip, both in the same unit.
 *
 * Source: WHO, "Waist Circumference and Waist–Hip Ratio: Report of a WHO Expert
 * Consultation", Geneva, 2008. The risk cut-offs belong to CP58's scoring — this library
 * computes, it does not interpret.
 */
export function whr(waistCm: number, hipCm: number): Calculated {
  if (waistCm <= 0 || hipCm <= 0) return no('not_positive');
  return ok({ value: waistCm / hipCm, unit: '1', formula: 'whr', version: WHR_VERSION });
}

// --- basal metabolic rate ---

/**
 * Mifflin-St Jeor, and the clinic's default.
 *
 * Source: Mifflin MD, St Jeor ST, Hill LA, Scott BJ, Daugherty SA, Koh YO. Am J Clin Nutr
 * 1990;51(2):241–247.
 *
 *   men:   10·W + 6.25·H − 5·A + 5
 *   women: 10·W + 6.25·H − 5·A − 161
 *
 * Chosen over Harris-Benedict, which was fitted in 1919 on a cohort that does not resemble a
 * modern population and overestimates resting expenditure by roughly 5%.
 */
export function bmrMifflin(
  weightKg: number,
  heightCm: number,
  ageYears: number,
  sex: Sex,
): Calculated {
  if (weightKg <= 0 || heightCm <= 0) return no('not_positive');
  if (ageYears < 0 || ageYears > 130) return no('out_of_range');
  const base = 10 * weightKg + 6.25 * heightCm - 5 * ageYears;
  if (sex === 'male') {
    return ok({
      value: base + 5,
      unit: 'kcal/d',
      formula: 'bmr_mifflin_st_jeor',
      version: MIFFLIN_VERSION,
    });
  }
  if (sex === 'female') {
    return ok({
      value: base - 161,
      unit: 'kcal/d',
      formula: 'bmr_mifflin_st_jeor',
      version: MIFFLIN_VERSION,
    });
  }
  return no('sex_unsupported');
}

/**
 * The revised Harris-Benedict equation.
 *
 * Source: Roza AM, Shizgal HM. Am J Clin Nutr 1984;40(1):168–182. The 1984 coefficients, not
 * the 1919 originals — a distinction worth keeping, because "Harris-Benedict" in a textbook
 * may mean either.
 */
export function bmrHarrisBenedict(
  weightKg: number,
  heightCm: number,
  ageYears: number,
  sex: Sex,
): Calculated {
  if (weightKg <= 0 || heightCm <= 0) return no('not_positive');
  if (ageYears < 0 || ageYears > 130) return no('out_of_range');
  let value: number;
  if (sex === 'male') {
    value = 88.362 + 13.397 * weightKg + 4.799 * heightCm - 5.677 * ageYears;
  } else if (sex === 'female') {
    value = 447.593 + 9.247 * weightKg + 3.098 * heightCm - 4.33 * ageYears;
  } else {
    return no('sex_unsupported');
  }
  return ok({
    value,
    unit: 'kcal/d',
    formula: 'bmr_harris_benedict_revised',
    version: HARRIS_BENEDICT_VERSION,
  });
}

// --- ideal body weight ---

/**
 * The Devine formula.
 *
 * Source: Devine BJ. Drug Intell Clin Pharm 1974;8:650–655. A **dosing** weight rather than a
 * target weight, which is worth remembering when it appears on a screen: a nutrition plan
 * built from it would be a plan built from a pharmacokinetics convention.
 *
 * Refused below 120 cm, where the downward extrapolation Devine never intended starts
 * producing numbers a clinician would not accept.
 */
export function idealBodyWeight(heightCm: number, sex: Sex): Calculated {
  if (heightCm <= 0) return no('not_positive');
  if (heightCm < 120) return no('out_of_range');
  const inchesOverFiveFeet = (heightCm - 152.4) / 2.54;
  let base: number;
  if (sex === 'male') base = 50.0;
  else if (sex === 'female') base = 45.5;
  else return no('sex_unsupported');
  return ok({
    value: base + 2.3 * inchesOverFiveFeet,
    unit: 'kg',
    formula: 'ibw_devine',
    version: IBW_VERSION,
  });
}

// --- body surface area ---

/** Source: Du Bois D, Du Bois EF. Arch Intern Med 1916;17:863–871. */
export function bsaDuBois(weightKg: number, heightCm: number): Calculated {
  if (weightKg <= 0 || heightCm <= 0) return no('not_positive');
  return ok({
    value: 0.007184 * Math.pow(heightCm, 0.725) * Math.pow(weightKg, 0.425),
    unit: 'm2',
    formula: 'bsa_du_bois',
    version: DU_BOIS_VERSION,
  });
}

/** Source: Mosteller RD. N Engl J Med 1987;317(17):1098. */
export function bsaMosteller(weightKg: number, heightCm: number): Calculated {
  if (weightKg <= 0 || heightCm <= 0) return no('not_positive');
  return ok({
    value: Math.sqrt((heightCm * weightKg) / 3600),
    unit: 'm2',
    formula: 'bsa_mosteller',
    version: MOSTELLER_VERSION,
  });
}

// --- kidney function ---

/**
 * The race-free CKD-EPI creatinine equation (§6.4).
 *
 * Source: Inker LA, Eneanya ND, Coresh J, et al. N Engl J Med 2021;385:1737–1749.
 *
 *   eGFR = 142 · min(Scr/κ, 1)^α · max(Scr/κ, 1)^−1.200 · 0.9938^age · 1.012 [if female]
 *   κ = 0.7 (female), 0.9 (male);  α = −0.241 (female), −0.302 (male)
 *
 * **Creatinine is in mg/dL**, the unit the equation is published in. CP42 stores creatinine
 * canonically in µmol/L, so the caller converts — using the server's conversion, not a second
 * copy of 88.42 in this file.
 *
 * The 2021 revision removed a race coefficient that raised the estimate for Black patients
 * with no physiological justification and delayed referrals as a result. The version string
 * says 2021 for exactly this reason.
 */
export function egfrCkdEpi2021(creatinineMgDl: number, ageYears: number, sex: Sex): Calculated {
  if (creatinineMgDl <= 0) return no('not_positive');
  // Fitted on adults. Silently applying it to a child is how a normal result hides renal
  // impairment; the paediatric estimate is Schwartz's.
  if (ageYears < 18 || ageYears > 130) return no('out_of_range');

  let kappa: number;
  let alpha: number;
  let sexFactor: number;
  if (sex === 'female') {
    kappa = 0.7;
    alpha = -0.241;
    sexFactor = 1.012;
  } else if (sex === 'male') {
    kappa = 0.9;
    alpha = -0.302;
    sexFactor = 1.0;
  } else {
    return no('sex_unsupported');
  }

  const ratio = creatinineMgDl / kappa;
  const value =
    142 *
    Math.pow(Math.min(ratio, 1), alpha) *
    Math.pow(Math.max(ratio, 1), -1.2) *
    Math.pow(0.9938, ageYears) *
    sexFactor;

  return ok({
    value,
    unit: 'mL/min/{1.73_m2}',
    formula: 'egfr_ckd_epi_2021',
    version: CKD_EPI_VERSION,
  });
}

/**
 * The paediatric estimate: 0.413 · height(cm) / Scr(mg/dL).
 *
 * Source: Schwartz GJ, Muñoz A, Schneider MF, et al. J Am Soc Nephrol 2009;20(3):629–637.
 *
 * **D-23 is still open** and it is not about this equation — the arithmetic is settled. It is
 * about the age at which a patient stops being a child for this purpose: the paper's cohort
 * is 1–16, adult CKD-EPI is fitted from 18, and the two do not meet. This library refuses
 * above 18 and leaves the boundary to the caller, visibly, rather than picking one quietly.
 */
export function egfrBedsideSchwartz(
  creatinineMgDl: number,
  heightCm: number,
  ageYears: number,
): Calculated {
  if (creatinineMgDl <= 0 || heightCm <= 0) return no('not_positive');
  if (ageYears < 1 || ageYears >= 18) return no('out_of_range');
  return ok({
    value: (0.413 * heightCm) / creatinineMgDl,
    unit: 'mL/min/{1.73_m2}',
    formula: 'egfr_bedside_schwartz',
    version: SCHWARTZ_VERSION,
  });
}

// --- smoking ---

/**
 * (cigarettes per day ÷ 20) × years smoked.
 *
 * Twenty is the definition of a pack in this measure, not a property of any particular
 * packet: the unit is standardised so two clinicians counting the same patient agree.
 */
export function packYears(cigarettesPerDay: number, years: number): Calculated {
  if (cigarettesPerDay < 0 || years < 0) return no('not_positive');
  if (cigarettesPerDay > 200 || years > 100) return no('out_of_range');
  return ok({
    value: (cigarettesPerDay / 20) * years,
    unit: '1',
    formula: 'pack_years',
    version: PACK_YEARS_VERSION,
  });
}

/**
 * How a derived value is rounded, on both sides.
 *
 * `Math.round` is half-up and Go's `math.Round` is half-away-from-zero; they disagree on
 * −0.5. Every derived value in this system is positive, so today they agree — this shared
 * definition is what keeps them agreeing when somebody adds a formula that is not.
 */
export function round(value: number, decimals: number): number {
  if (!Number.isFinite(value)) return value;
  const scale = Math.pow(10, decimals);
  const scaled = value * scale;
  const rounded = scaled < 0 ? -Math.round(-scaled) : Math.round(scaled);
  return rounded / scale;
}

export {
  CANONICAL_DECIMALS,
  DISPLAY_PAIRS,
  ENTRY_UNITS,
  dualUnit,
  toCanonical,
  hasSecondaryUnit,
  usesFeetAndInches,
  type DisplayValue,
  type DualUnit,
} from './display';

export { anthroPanel, PANEL_VERSION, type Panel, type PanelInput } from './panel';

export {
  evaluate as evaluatePlausibility,
  resolveRule as resolvePlausibilityRule,
  type PlausibilityRule,
  type PlausibilitySubject,
  type PlausibilityVerdict,
  type PreviousMeasurement,
} from './plausibility';
