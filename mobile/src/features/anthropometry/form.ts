import {
  anthroPanel,
  evaluatePlausibility,
  resolvePlausibilityRule,
  toCanonical,
  type Panel,
  type PlausibilityRule,
  type PlausibilitySubject,
  type Sex,
} from '@dthcms/clinical-calc';

/**
 * Station 2's form, as data (CP45, blueprint §3 step 2).
 *
 * Everything here is pure: text in, numbers and a panel out. The screen is then a rendering
 * of this and nothing else, which is what makes the interesting half testable without a
 * device — and the interesting half is not the layout, it is *what happens to a half-typed
 * number*.
 *
 * # Why the entered text is kept as text
 *
 * An operator typing "7" on the way to "72.5" has typed a plausible weight. Parsing to a
 * number on every keystroke and storing that would mean the field cannot hold "72." at all,
 * and the operator would watch their decimal point vanish. So the text is the state, the
 * number is derived, and a field that does not currently parse simply contributes nothing to
 * the panel.
 */

export type FieldKey = 'height' | 'weight' | 'waist' | 'hip' | 'bodyFat' | 'muscle';

export interface FieldSpec {
  key: FieldKey;
  /** The observation code this field writes. */
  code: string;
  /** The units the selector offers, canonical first. */
  units: string[];
}

/**
 * The fields, in the order the measurements are actually taken.
 *
 * Height then weight then the tape measurements then whatever the body-composition scale
 * reports. Not alphabetical and not grouped by instrument: an operator works down a screen
 * in the order their hands move, and a form that made them jump back up is a form that gets
 * one field skipped every morning.
 */
export const ANTHRO_FIELDS: readonly FieldSpec[] = Object.freeze([
  { key: 'height', code: 'BODY_HEIGHT', units: ['cm', 'in', '[ft_i]'] },
  { key: 'weight', code: 'BODY_WEIGHT', units: ['kg', '[lb_av]'] },
  { key: 'waist', code: 'WAIST_CIRC', units: ['cm', 'in'] },
  { key: 'hip', code: 'HIP_CIRC', units: ['cm', 'in'] },
  { key: 'bodyFat', code: 'BODY_FAT_PCT', units: ['%'] },
  { key: 'muscle', code: 'MUSCLE_MASS', units: ['kg', '[lb_av]'] },
]);

export interface FieldState {
  text: string;
  unit: string;
}

export type FormState = Record<FieldKey, FieldState>;

export function emptyForm(): FormState {
  const out = {} as FormState;
  for (const field of ANTHRO_FIELDS) out[field.key] = { text: '', unit: field.units[0]! };
  return out;
}

/**
 * The number a field currently holds, or null.
 *
 * Null for empty, for half-typed ("72."), for anything unparseable, and for zero or less —
 * which is a refusal rather than a measurement. A screen that treated 0 as a weight would
 * show a BMI of infinity, and infinity renders as a blank cell: a wrong answer wearing a
 * missing answer's clothes.
 */
export function parsedValue(field: FieldState): number | null {
  const text = field.text.trim();
  if (text === '') return null;
  const value = Number(text);
  if (!Number.isFinite(value) || value <= 0) return null;
  return value;
}

/** The same number in the unit the record stores, for the panel to compute from. */
export function canonicalValue(field: FieldState): number | null {
  const value = parsedValue(field);
  if (value === null) return null;
  return toCanonical(value, field.unit);
}

export interface PatientFacts {
  sex: Sex;
  ageYears: number;
}

/**
 * The panel, from whatever is currently on screen.
 *
 * Called on every keystroke, which is why it does nothing but arithmetic — no fetch, no
 * await, no state. Criterion (1) gives it 200ms and it uses a fraction of one.
 */
export function panelOf(form: FormState, patient: PatientFacts): Panel {
  return anthroPanel({
    weightKg: canonicalValue(form.weight),
    heightCm: canonicalValue(form.height),
    waistCm: canonicalValue(form.waist),
    hipCm: canonicalValue(form.hip),
    ageYears: patient.ageYears,
    sex: patient.sex,
    // This clinic. A BMI of 24 is "normal" internationally and "overweight" in a Bangladeshi
    // patient, and the whole screening pathway hangs on which side of that line somebody
    // falls (D-06, CP43).
    asian: true,
  });
}

/** The patient's last recorded value for each field, in canonical units. */
export type PreviousValues = Partial<Record<FieldKey, number>>;

export interface Delta {
  /** Signed, in the canonical unit. */
  change: number;
  direction: 'up' | 'down' | 'same';
}

/**
 * How far today's measurement is from the last one.
 *
 * Shown beside the field rather than after saving, because the moment it is useful is the
 * moment the operator can still re-measure: a weight 8 kg below the last visit is either a
 * clinical event or a typing error, and the person holding the scale is the only one who can
 * tell which.
 *
 * "Same" is a third answer rather than a zero, because a patient whose weight has not moved
 * in three months is a finding, and an interface that showed a blank there would be hiding
 * it.
 */
export function deltaOf(current: number | null, previous: number | undefined): Delta | null {
  if (current === null || previous === undefined) return null;
  const change = current - previous;
  if (Math.abs(change) < 1e-9) return { change: 0, direction: 'same' };
  return { change, direction: change > 0 ? 'up' : 'down' };
}

/** True when the form has nothing worth saving. */
export function isEmpty(form: FormState): boolean {
  return ANTHRO_FIELDS.every((field) => parsedValue(form[field.key]) === null);
}

/**
 * What the server derives from an anthropometry form.
 *
 * A literal tuple rather than `string[]`, so that a name this server does not know is a
 * compile error here rather than a 422 in a corridor.
 */
export const ANTHRO_DERIVATIONS = ['BMI', 'BMR', 'IBW', 'WHR'] as const;

export interface BatchBody {
  event_id: string;
  patient_id: string;
  visit_id?: string;
  observations: {
    event_id: string;
    code: string;
    value: number;
    unit: string;
    confirmed?: boolean;
  }[];
  derive: (typeof ANTHRO_DERIVATIONS)[number][];
}

/**
 * The form as one request.
 *
 * The values go **as typed**, with the unit as selected — never the canonical numbers the
 * panel computed from. The server's conversion is the one that decides what is stored
 * (CP42), and posting a converted number would quietly make this file authoritative about
 * a clinical value.
 *
 * The derivations are named, not sent. The server computes them from what it has just
 * written; the panel above is for the operator's eyes while they type (CP43).
 */
export function toBatch(
  form: FormState,
  ids: {
    batch: string;
    patient: string;
    visit?: string;
    perField: (key: FieldKey) => string;
    /** Which fields the operator confirmed after a plausibility warning (CP46). */
    confirmed?: (key: FieldKey) => boolean;
  },
): BatchBody {
  const observations: BatchBody['observations'] = [];
  for (const field of ANTHRO_FIELDS) {
    const state = form[field.key];
    const value = parsedValue(state);
    if (value === null) continue;
    const entry: BatchBody['observations'][number] = {
      event_id: ids.perField(field.key),
      code: field.code,
      value,
      unit: state.unit,
    };
    if (ids.confirmed?.(field.key) === true) entry.confirmed = true;
    observations.push(entry);
  }
  const body: BatchBody = {
    event_id: ids.batch,
    patient_id: ids.patient,
    observations,
    derive: [...ANTHRO_DERIVATIONS],
  };
  if (ids.visit !== undefined) body.visit_id = ids.visit;
  return body;
}

/**
 * The patient's previous values, from the observation list the API returns.
 *
 * Reads `value` — the canonical number — rather than `entered_value`, because a delta
 * between 154 lb and 70 kg is not a delta.
 */
export function previousFrom(
  rows: { code: string; value?: number | null }[] | undefined,
): PreviousValues {
  const out: PreviousValues = {};
  if (rows === undefined) return out;
  const byCode = new Map<string, number>();
  for (const row of rows) {
    if (row.value === null || row.value === undefined) continue;
    // Newest first from the API, so the first occurrence of a code is the current one.
    if (!byCode.has(row.code)) byCode.set(row.code, row.value);
  }
  for (const field of ANTHRO_FIELDS) {
    const value = byCode.get(field.code);
    if (value !== undefined) out[field.key] = value;
  }
  return out;
}

/**
 * The plausibility warnings for a form as it stands (CP46).
 *
 * Computed on every keystroke beside the panel, for the same reason: the moment a warning is
 * useful is the moment the operator can still re-measure. The server checks the same rules
 * authoritatively — this is the half that arrives in time to be acted on.
 */
export type Confirmations = Partial<Record<FieldKey, boolean>>;

export interface FieldWarning {
  severity: 'stop' | 'warn';
  kind: 'low' | 'high' | 'rose' | 'fell';
  limit: number;
  previous?: number;
  note_en?: string;
  note_bn?: string;
}

/** The patient's previous value of each field, with when it was taken. */
export type PreviousMeasurements = Partial<Record<FieldKey, { value: number; at: Date }>>;

export function warningsFor(
  form: FormState,
  rules: readonly PlausibilityRule[],
  subject: PlausibilitySubject,
  previous: PreviousMeasurements,
  confirmed: Confirmations = {},
  now?: Date,
): Partial<Record<FieldKey, FieldWarning>> {
  const out: Partial<Record<FieldKey, FieldWarning>> = {};
  for (const field of ANTHRO_FIELDS) {
    const verdict = evaluatePlausibility(
      resolvePlausibilityRule(rules, field.code, subject),
      canonicalValue(form[field.key]),
      { previous: previous[field.key], now, confirmed: confirmed[field.key] === true },
    );
    if (verdict !== null) out[field.key] = verdict;
  }
  return out;
}

/**
 * The previous values with their times, for the delta rules.
 *
 * Separate from `previousFrom` because the delta check needs the *when* and the on-screen
 * comparison does not — and a function that returned both would make every caller carry a
 * date it has no use for.
 */
export function previousMeasurementsFrom(
  rows: { code: string; value?: number | null; effective_at?: string }[] | undefined,
): PreviousMeasurements {
  const out: PreviousMeasurements = {};
  if (rows === undefined) return out;
  const seen = new Map<string, { value: number; at: Date }>();
  for (const row of rows) {
    if (row.value === null || row.value === undefined) continue;
    if (seen.has(row.code)) continue;
    seen.set(row.code, {
      value: row.value,
      at: row.effective_at === undefined ? new Date(0) : new Date(row.effective_at),
    });
  }
  for (const field of ANTHRO_FIELDS) {
    const found = seen.get(field.code);
    if (found !== undefined) out[field.key] = found;
  }
  return out;
}

/** True when something on the form cannot be saved at all. */
export function hasBlocking(warnings: Partial<Record<FieldKey, FieldWarning>>): boolean {
  return Object.values(warnings).some((w) => w?.severity === 'stop');
}
