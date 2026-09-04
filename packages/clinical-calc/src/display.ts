/**
 * Dual-unit display (CP44, [R-08], blueprint §2).
 *
 * A named non-negotiable: **every clinical value shows the clinical unit with the
 * patient-familiar equivalent beneath it.** A height is 168 cm *and* 5′6″, because the
 * clinician thinks in one and the patient thinks in the other, and a screen that makes
 * either of them convert in their head is a screen where somebody converts wrongly.
 *
 * Implementing it as a shared pair-builder rather than per screen is what stops it being
 * forgotten: a screen that renders a raw number is a screen that fails a test, not a screen
 * somebody has to notice in review.
 *
 * # Why the factors are here and not fetched
 *
 * CP42's `/v1/observations/units` deliberately does not return conversion factors: the
 * conversion that decides what is *stored* happens once, in the database, so a client cannot
 * arrive at a different canonical value from the one the server will write.
 *
 * Display is a different problem. A tablet in a corridor with no signal still has to draw
 * "69.5 kg / 153.2 lb", so the display factors live here, in the bundle. They are the same
 * numbers, and `TestTheDisplayUnitsAgreeWithTheDatabase` in the Go suite reads this file and
 * fails if they ever drift — the same discipline as CP43's Go↔TS parity, for the same
 * reason.
 *
 * # Rounding
 *
 * Documented per unit below and applied consistently, because a value that renders as 69.5
 * on one screen and 69.46 on another is a value two people will disagree about out loud.
 * Feet and inches are the exception: whole inches, because nobody says "five foot six point
 * three".
 */

/** One half of a displayed value. */
export interface DisplayValue {
  /** Already rounded. Render it as-is. */
  value: number;
  /** The unit's code, e.g. `kg`. */
  unit: string;
  /** What to print: `69.5`, or `5′6″` for the feet-and-inches case. */
  text: string;
}

export interface DualUnit {
  /** The clinical unit — what the record holds and what a clinician reads. */
  primary: DisplayValue;
  /** The patient-familiar equivalent, or null where there is no second unit anybody uses. */
  secondary: DisplayValue | null;
}

/**
 * The display pairs: canonical unit → the unit shown beneath it.
 *
 * `factor` and `offset` convert **out of** the canonical unit: `secondary = (canonical −
 * offset) / factor`, matching the database's `core.from_canonical`. Written that way round
 * rather than inverted so the numbers here are literally the numbers in `core.unit`, which
 * is what makes the drift check a comparison rather than an arithmetic argument.
 *
 * A canonical unit absent from this table has no second unit anybody uses — mmHg, /min, %,
 * kg/m² — and its value renders alone. That is a deliberate list, not an oversight: showing
 * "128 mmHg / 17.1 kPa" beneath a blood pressure would be noise on the one reading nobody in
 * Bangladesh reads in kilopascals.
 */
export const DISPLAY_PAIRS: Readonly<
  Record<string, { unit: string; factor: number; offset: number; decimals: number }>
> = Object.freeze({
  // Height: handled specially below — feet and inches together, not decimal feet.
  cm: { unit: 'in', factor: 2.54, offset: 0, decimals: 0 },
  kg: { unit: '[lb_av]', factor: 0.45359237, offset: 0, decimals: 1 },
  Cel: { unit: '[degF]', factor: 0.5555555555555556, offset: -17.7777777777777778, decimals: 1 },
  'mmol/L': { unit: 'mg/dL', factor: 0.05551, offset: 0, decimals: 0 },
  'mmol/L#chol': { unit: 'mg/dL#chol', factor: 0.02586, offset: 0, decimals: 0 },
  'mmol/L#trig': { unit: 'mg/dL#trig', factor: 0.01129, offset: 0, decimals: 0 },
  'umol/L': { unit: 'mg/dL#cr', factor: 88.42, offset: 0, decimals: 2 },
  'mmol/mol': { unit: '%#ngsp', factor: 10.929, offset: -23.49735, decimals: 1 },
});

/**
 * How many decimals each canonical unit is written with.
 *
 * A property of the unit rather than of the screen: a weight in kg is 69.5 and the same
 * weight in grams is not 69500.0. Mirrors `core.unit.decimals`, and the drift check covers
 * these too.
 */
export const CANONICAL_DECIMALS: Readonly<Record<string, number>> = Object.freeze({
  cm: 1,
  m: 2,
  kg: 1,
  g: 0,
  'mm[Hg]': 0,
  Cel: 1,
  '/min': 0,
  '%': 0,
  'kcal/d': 0,
  'kg/m2': 1,
  m2: 2,
  '1': 2,
  'mL/min/{1.73_m2}': 0,
  'mmol/L': 1,
  'mmol/L#chol': 2,
  'mmol/L#trig': 2,
  'umol/L': 0,
  'mmol/mol': 0,
});

/** Rounding, shared with the calculation library so the two never disagree at a half. */
function roundTo(value: number, decimals: number): number {
  if (!Number.isFinite(value)) return value;
  const scale = Math.pow(10, decimals);
  const scaled = value * scale;
  const rounded = scaled < 0 ? -Math.round(-scaled) : Math.round(scaled);
  return rounded / scale;
}

function format(value: number, decimals: number): string {
  return value.toFixed(decimals);
}

/**
 * Codes measured in centimetres that a patient states in **feet and inches**.
 *
 * Height, and only height. A waist of 94 cm is "37 inches" to everybody who has ever bought
 * trousers; rendering it as 3′1″ is arithmetically correct and clinically absurd, and it is
 * the kind of thing that gets noticed in a waiting room rather than in a code review.
 *
 * So the *code* decides, not the unit. A new circumference added to the registry gets plain
 * inches by default, which is the safe direction to be wrong in.
 */
const FEET_AND_INCHES_CODES = new Set(['BODY_HEIGHT']);

/**
 * Height, in feet and inches together.
 *
 * `5′6″`, not `5.5 ft`. Nobody says "five point five feet", and a decimal foot is a number a
 * patient has to convert in their head — which is the whole thing [R-08] exists to prevent.
 *
 * Rounded to whole inches, and 12 inches carries into a foot: 167.7 cm is 66.02 inches,
 * which is 5′6″ and not 5′6.02″ or 4′18″.
 */
function feetAndInches(centimetres: number): DisplayValue {
  const totalInches = Math.round(centimetres / 2.54);
  const feet = Math.floor(totalInches / 12);
  const inches = totalInches - feet * 12;
  return { value: totalInches, unit: 'in', text: `${feet}′${inches}″` };
}

/**
 * Build the pair a screen renders.
 *
 * `canonicalUnit` is the unit the value is stored in — `observation.unit` from the API. The
 * value must already be in that unit, which it is: the server stores canonically and returns
 * both the canonical value and the entered one.
 *
 * `code` is the observation code, and it matters for exactly one thing: whether a
 * centimetre value is spoken in feet and inches (a height) or in plain inches (a waist).
 * Optional, so a caller with only a unit still gets a correct pair — just the safe default.
 */
export function dualUnit(value: number, canonicalUnit: string, code?: string): DualUnit {
  const decimals = CANONICAL_DECIMALS[canonicalUnit] ?? 1;
  const primary: DisplayValue = {
    value: roundTo(value, decimals),
    unit: canonicalUnit,
    text: format(roundTo(value, decimals), decimals),
  };

  // Height is the one value with a compound second unit.
  if (canonicalUnit === 'cm' && code !== undefined && FEET_AND_INCHES_CODES.has(code)) {
    return { primary, secondary: feetAndInches(value) };
  }

  const pair = DISPLAY_PAIRS[canonicalUnit];
  if (!pair) return { primary, secondary: null };

  const converted = roundTo((value - pair.offset) / pair.factor, pair.decimals);
  return {
    primary,
    secondary: { value: converted, unit: pair.unit, text: format(converted, pair.decimals) },
  };
}

/** Whether a canonical unit has a second unit worth showing. */
export function hasSecondaryUnit(canonicalUnit: string): boolean {
  return canonicalUnit in DISPLAY_PAIRS;
}

/** Whether a code is spoken in feet and inches rather than plain inches. Height, and only it. */
export function usesFeetAndInches(code: string): boolean {
  return FEET_AND_INCHES_CODES.has(code);
}
