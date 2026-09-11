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
  // The Fahrenheit offset is 160/9, written to the last digit a double actually keeps.
  // Adding more is not more precision: JavaScript rounds them away and the extra digits
  // then claim an accuracy the number does not have, which is why `no-loss-of-precision`
  // rejects them. The database column is `numeric` and holds the same decimal; the drift
  // test parses both and compares the values, not the text.
  cm: { unit: 'in', factor: 2.54, offset: 0, decimals: 0 },
  kg: { unit: '[lb_av]', factor: 0.45359237, offset: 0, decimals: 1 },
  Cel: { unit: '[degF]', factor: 0.5555555555555556, offset: -17.77777777777778, decimals: 1 },
  'mmol/L': { unit: 'mg/dL', factor: 0.05551, offset: 0, decimals: 0 },
  'mmol/L#chol': { unit: 'mg/dL#chol', factor: 0.02586, offset: 0, decimals: 0 },
  'mmol/L#trig': { unit: 'mg/dL#trig', factor: 0.01129, offset: 0, decimals: 0 },
  'umol/L': { unit: 'mg/dL#cr', factor: 88.42, offset: 0, decimals: 2 },
  'mmol/mol': { unit: '%#ngsp', factor: 10.929, offset: -23.49735, decimals: 1 },
  // Duration, added with the lifestyle assessment (CP58). Minutes are canonical because the
  // activity guideline is written in them and an integer minute is exact where a decimal hour
  // is not — but nobody says a patient slept four hundred and twenty minutes, so hours are the
  // familiar half of the pair for exactly the reason this table exists.
  min: { unit: 'h', factor: 60, offset: 0, decimals: 1 },
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
  // Whole minutes. A sleep duration written as 447.3 minutes claims a precision nobody
  // measured, and `core.unit` says the same (CP58).
  min: 0,
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

/**
 * Canonical units whose **clinical** reading is not the unit the record stores.
 *
 * CP44 was written on the assumption that the canonical unit and the unit a clinician reads
 * are the same, with the *patient-familiar* unit beneath. For almost everything they are: a
 * weight is stored and read in kilograms and spoken to a patient in pounds.
 *
 * HbA1c is the exception, and it is the one analyte this clinic exists for. The record stores
 * IFCC `mmol/mol`, which is right — it is the interoperable unit and the one the database
 * converts into. But this clinic **reads and prescribes in NGSP %**: a physician scanning a
 * chart sees 66 and has to convert 66 mmol/mol to 8.2 % in their head, on the one number the
 * consultation turns on. That is exactly the arithmetic [R-08] exists to abolish, and it was
 * being done in the wrong direction because "canonical" was standing in for "clinical".
 *
 * So this table names, per canonical unit, the unit a clinician reads it in. It changes the
 * **order** of the pair and nothing else: the value stored, the conversion factor and the
 * unit the API returns are all untouched, and the IFCC number is still on screen beneath the
 * NGSP one. A screen renders `8.2 % / 66 mmol/mol` rather than `66 mmol/mol / 8.2 %`.
 *
 * It is deliberately keyed on the **unit** and not on the observation code, so a second
 * HbA1c-like code needs no entry, and a future analyte whose clinical unit is not its storage
 * unit is one line rather than a special case in a chart.
 *
 * Which unit a clinic reads an analyte in is a clinical fact and not a technical one; this
 * entry is Dr. Nahid's, recorded here rather than in whichever screen noticed it.
 */
export const CLINICAL_READING: Readonly<Record<string, string>> = Object.freeze({
  'mmol/mol': '%#ngsp',
});

/** The unit a clinician reads this canonical unit in. The canonical unit itself, usually. */
export function clinicalReadingUnit(canonicalUnit: string): string {
  return CLINICAL_READING[canonicalUnit] ?? canonicalUnit;
}

/** How many decimals the clinical reading is written with. */
export function clinicalReadingDecimals(canonicalUnit: string): number {
  const reading = CLINICAL_READING[canonicalUnit];
  if (reading === undefined) return CANONICAL_DECIMALS[canonicalUnit] ?? 1;
  return DISPLAY_PAIRS[canonicalUnit]?.decimals ?? 1;
}

/**
 * A stored value in the unit a clinician reads it in.
 *
 * The identity where the two are the same, so a caller — a chart axis, say — can convert
 * unconditionally rather than branching on the analyte. Unrounded: a scale needs the number,
 * not the text, and rounding a domain bound would move a gridline.
 */
export function toClinicalReading(value: number, canonicalUnit: string): number {
  const pair =
    CLINICAL_READING[canonicalUnit] === undefined ? undefined : DISPLAY_PAIRS[canonicalUnit];
  if (pair === undefined) return value;
  return (value - pair.offset) / pair.factor;
}

/**
 * The inverse: a number a clinician read, back in the unit the record stores.
 *
 * An axis needs both directions — its ticks are chosen in the unit a person reads, and their
 * positions are computed in the unit the data is in. Doing that with two functions from one
 * table is what stops a chart inventing its own arithmetic beside the record's.
 */
export function fromClinicalReading(value: number, canonicalUnit: string): number {
  const pair =
    CLINICAL_READING[canonicalUnit] === undefined ? undefined : DISPLAY_PAIRS[canonicalUnit];
  if (pair === undefined) return value;
  return value * pair.factor + pair.offset;
}

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
  const stored: DisplayValue = {
    value: roundTo(value, decimals),
    unit: canonicalUnit,
    text: format(roundTo(value, decimals), decimals),
  };

  // Height is the one value with a compound second unit.
  if (canonicalUnit === 'cm' && code !== undefined && FEET_AND_INCHES_CODES.has(code)) {
    return { primary: stored, secondary: feetAndInches(value) };
  }

  const pair = DISPLAY_PAIRS[canonicalUnit];
  if (!pair) return { primary: stored, secondary: null };

  const converted = roundTo((value - pair.offset) / pair.factor, pair.decimals);
  const other: DisplayValue = {
    value: converted,
    unit: pair.unit,
    text: format(converted, pair.decimals),
  };

  // Which of the two leads. For almost everything the stored unit is also the clinical one
  // and the converted half is the patient-familiar one; for HbA1c the clinic reads the
  // converted half, and putting the stored number first makes a physician convert in their
  // head on the one value the consultation turns on. See CLINICAL_READING.
  return CLINICAL_READING[canonicalUnit] === undefined
    ? { primary: stored, secondary: other }
    : { primary: other, secondary: stored };
}

/** Whether a canonical unit has a second unit worth showing. */
export function hasSecondaryUnit(canonicalUnit: string): boolean {
  return canonicalUnit in DISPLAY_PAIRS;
}

/** Whether a code is spoken in feet and inches rather than plain inches. Height, and only it. */
export function usesFeetAndInches(code: string): boolean {
  return FEET_AND_INCHES_CODES.has(code);
}

/**
 * The units a station screen lets an operator *enter* in, and how each converts **into** the
 * canonical unit: `canonical = entered × factor + offset`.
 *
 * # Why this exists when the server converts
 *
 * It does not decide what is stored. A write still posts the number and the unit exactly as
 * typed, and `core.to_canonical` does the conversion that lands in the record — the rule from
 * CP42 that stops a client and a server ever disagreeing about a stored value.
 *
 * What this is for is the twenty seconds before that write. P-4 wants a BMI on screen as the
 * operator types, and an operator whose scale reads in pounds is typing pounds. Without these
 * factors the panel would either show nothing until the save came back, or — far worse —
 * compute a BMI from 154 as though it were kilograms.
 *
 * The numbers are the ones in `core.unit`, and `TestTheEntryUnitsAgreeWithTheDatabase` in the
 * Go suite reads this file and fails if they ever drift.
 */
export const ENTRY_UNITS: Readonly<
  Record<string, { canonical: string; factor: number; offset: number }>
> = Object.freeze({
  cm: { canonical: 'cm', factor: 1, offset: 0 },
  m: { canonical: 'cm', factor: 100, offset: 0 },
  in: { canonical: 'cm', factor: 2.54, offset: 0 },
  '[ft_i]': { canonical: 'cm', factor: 30.48, offset: 0 },
  kg: { canonical: 'kg', factor: 1, offset: 0 },
  g: { canonical: 'kg', factor: 0.001, offset: 0 },
  '[lb_av]': { canonical: 'kg', factor: 0.45359237, offset: 0 },
  'mm[Hg]': { canonical: 'mm[Hg]', factor: 1, offset: 0 },
  kPa: { canonical: 'mm[Hg]', factor: 7.50062, offset: 0 },
  Cel: { canonical: 'Cel', factor: 1, offset: 0 },
  '[degF]': { canonical: 'Cel', factor: 0.5555555555555556, offset: -17.77777777777778 },
  '/min': { canonical: '/min', factor: 1, offset: 0 },
  '%': { canonical: '%', factor: 1, offset: 0 },
  // The ratio dimension's canonical unit, which is how a dimensionless count is entered: pack
  // years, cigarettes a day, years smoked. Present so that a station form checking a
  // plausibility band on one of those gets a number rather than null — a warning that silently
  // stopped appearing is worse than one that never did (CP58).
  '1': { canonical: '1', factor: 1, offset: 0 },
  // Duration (CP58). Sleep is typed in hours and activity in minutes, and both are stored in
  // canonical minutes: without the factor here, a plausibility band written in minutes would be
  // compared against a number of hours, which is the unit bug CP42's framework exists to stop.
  min: { canonical: 'min', factor: 1, offset: 0 },
  h: { canonical: 'min', factor: 60, offset: 0 },
});

/**
 * A typed value in the unit it was typed in, as the canonical number the panel computes from.
 *
 * Returns null for a unit this table does not know, which is the honest answer: a panel that
 * guessed would show a BMI computed from the wrong scale, and a wrong number nobody can see
 * is wrong is the worst thing a station screen can put on a phone.
 */
export function toCanonical(value: number, unit: string): number | null {
  const entry = ENTRY_UNITS[unit];
  if (entry === undefined || !Number.isFinite(value)) return null;
  return value * entry.factor + entry.offset;
}
