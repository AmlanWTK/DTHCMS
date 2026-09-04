import { toCanonical } from '@dthcms/clinical-calc';

/**
 * Station 5's vitals, as data (CP49, [R-01], §15.2).
 *
 * # The order is the order a clinician measures in
 *
 * Not alphabetical, not grouped by instrument type: the automated cuff reports a blood
 * pressure and a pulse together, the oximeter clips on next, and the thermometer and the
 * respiratory count come after. An operator working down a screen in a different order from
 * their hands is an operator who skips one field every morning.
 *
 * **This order is proposed.** The plan's own risk note says to sit with the clinical
 * assistant and watch a morning before fixing it, and that has not happened yet.
 *
 * # A blood pressure is not two numbers
 *
 * It is two numbers, an arm, a position and a cuff. A series that silently mixes a sitting
 * left-arm reading with a supine right-arm one is a series nobody can read a trend from — so
 * the context travels with the reading rather than being a note somebody sometimes types.
 *
 * # Two readings are normal practice
 *
 * A blood pressure measured once is a blood pressure measured badly. Both readings are kept
 * as **distinct observations**, not averaged and not replaced: the second is not a correction
 * of the first, and a record that treated it as one would lose the fact that they differed.
 */

export type VitalKey = 'systolic' | 'diastolic' | 'pulse' | 'spo2' | 'temperature' | 'respiratory';

export interface VitalSpec {
  key: VitalKey;
  code: string;
  units: string[];
  /** Fields on the same row of the form. Systolic and diastolic are one measurement. */
  pairedWith?: VitalKey;
}

export const VITAL_FIELDS: readonly VitalSpec[] = Object.freeze([
  { key: 'systolic', code: 'BP_SYSTOLIC', units: ['mm[Hg]', 'kPa'], pairedWith: 'diastolic' },
  { key: 'diastolic', code: 'BP_DIASTOLIC', units: ['mm[Hg]', 'kPa'] },
  { key: 'pulse', code: 'HEART_RATE', units: ['/min'] },
  { key: 'spo2', code: 'SPO2', units: ['%'] },
  { key: 'temperature', code: 'BODY_TEMP', units: ['Cel', '[degF]'] },
  { key: 'respiratory', code: 'RESP_RATE', units: ['/min'] },
]);

/** What a blood pressure was taken on. Coded, because free text is a trend nobody can read. */
export const BP_ARMS = ['left', 'right'] as const;
export const BP_POSITIONS = ['sitting', 'standing', 'supine'] as const;
export const BP_CUFFS = ['adult', 'large_adult', 'paediatric', 'thigh'] as const;

export type BPArm = (typeof BP_ARMS)[number];
export type BPPosition = (typeof BP_POSITIONS)[number];
export type BPCuff = (typeof BP_CUFFS)[number];

export interface VitalField {
  text: string;
  unit: string;
}

/**
 * One set of vitals. A reading, not a form: the screen holds a list of these because two
 * blood pressures in a sitting is normal practice.
 */
export interface Reading {
  values: Record<VitalKey, VitalField>;
  arm: BPArm;
  position: BPPosition;
  cuff: BPCuff;
}

export function emptyReading(): Reading {
  const values = {} as Record<VitalKey, VitalField>;
  for (const field of VITAL_FIELDS) values[field.key] = { text: '', unit: field.units[0]! };
  return { values, arm: 'left', position: 'sitting', cuff: 'adult' };
}

export function parsedVital(field: VitalField): number | null {
  const text = field.text.trim();
  if (text === '') return null;
  const value = Number(text);
  // A temperature is the one vital where zero is not the smallest thing anybody types — but
  // 0 °C is not a body temperature either, and the plausibility band refuses it. Treating
  // every non-positive entry as "not measured" keeps one rule for all six fields.
  if (!Number.isFinite(value) || value <= 0) return null;
  return value;
}

export function canonicalVital(field: VitalField): number | null {
  const value = parsedVital(field);
  if (value === null) return null;
  return toCanonical(value, field.unit);
}

/** True when a reading holds nothing worth saving. */
export function isBlank(reading: Reading): boolean {
  return VITAL_FIELDS.every((field) => parsedVital(reading.values[field.key]) === null);
}

/** True when a blood pressure is half-typed: one number without the other. */
export function halfABloodPressure(reading: Reading): boolean {
  const systolic = parsedVital(reading.values.systolic) !== null;
  const diastolic = parsedVital(reading.values.diastolic) !== null;
  return systolic !== diastolic;
}

// --- what is normal ---

export interface Range {
  code: string;
  sex?: 'male' | 'female';
  min_age_years?: number;
  max_age_years?: number;
  low?: number;
  high?: number;
  note_en?: string;
  note_bn?: string;
  approved: boolean;
}

export interface Subject {
  sex: 'male' | 'female' | 'other';
  ageYears: number;
}

/** The range that applies: the first match in the server's own order. */
export function resolveRange(
  ranges: readonly Range[],
  code: string,
  subject: Subject,
): Range | null {
  for (const range of ranges) {
    if (range.code !== code) continue;
    if (range.sex !== undefined && range.sex !== subject.sex) continue;
    if (range.min_age_years !== undefined && subject.ageYears < range.min_age_years) continue;
    if (range.max_age_years !== undefined && subject.ageYears >= range.max_age_years) continue;
    return range;
  }
  return null;
}

export type OutOfRange = { direction: 'low' | 'high'; limit: number } | null;

/**
 * Whether a value is outside what is normal for this patient.
 *
 * Criterion 3, and nothing more. This is not an alert: a value outside the range turns the
 * field amber so the operator looks again. Critical values, the audible warning and the
 * escalation chain are CP50, and a screen that shouted at every second patient is a screen
 * nobody hears when it matters.
 */
export function outOfRange(range: Range | null, canonical: number | null): OutOfRange {
  if (range === null || canonical === null) return null;
  if (range.low !== undefined && canonical < range.low) {
    return { direction: 'low', limit: range.low };
  }
  if (range.high !== undefined && canonical > range.high) {
    return { direction: 'high', limit: range.high };
  }
  return null;
}

export function flagsFor(
  reading: Reading,
  ranges: readonly Range[],
  subject: Subject,
): Partial<Record<VitalKey, NonNullable<OutOfRange>>> {
  const out: Partial<Record<VitalKey, NonNullable<OutOfRange>>> = {};
  for (const field of VITAL_FIELDS) {
    const verdict = outOfRange(
      resolveRange(ranges, field.code, subject),
      canonicalVital(reading.values[field.key]),
    );
    if (verdict !== null) out[field.key] = verdict;
  }
  return out;
}

// --- what gets sent ---

export interface VitalsBatch {
  event_id: string;
  patient_id: string;
  observations: {
    event_id: string;
    code: string;
    value?: number;
    unit?: string;
    value_code?: string;
    effective_at: string;
  }[];
}

/**
 * The readings as one request.
 *
 * Every reading's observations share an `effective_at`, and two readings have different ones.
 * That is what keeps them distinct facts rather than a correction: the timeline groups by
 * time, and two blood pressures a minute apart are two blood pressures.
 *
 * The arm, position and cuff go with each reading, not once for the set — an operator who
 * takes the second reading on the other arm is doing something clinically meaningful.
 */
export function toVitalsBatch(
  readings: readonly Reading[],
  ids: {
    batch: string;
    patient: string;
    /** A stable id per (reading, code), so a retry writes the same values. */
    perValue: (readingIndex: number, code: string) => string;
    /** When each reading was taken. */
    takenAt: (readingIndex: number) => string;
  },
): VitalsBatch {
  const observations: VitalsBatch['observations'] = [];

  readings.forEach((reading, index) => {
    if (isBlank(reading)) return;
    const at = ids.takenAt(index);

    for (const field of VITAL_FIELDS) {
      const value = parsedVital(reading.values[field.key]);
      if (value === null) continue;
      observations.push({
        event_id: ids.perValue(index, field.code),
        code: field.code,
        value,
        unit: reading.values[field.key].unit,
        effective_at: at,
      });
    }

    // The context, only when there is a blood pressure for it to be context *for*. Recording
    // "left arm, sitting" beside a lone temperature would be noise in the record.
    const hasBP =
      parsedVital(reading.values.systolic) !== null ||
      parsedVital(reading.values.diastolic) !== null;
    if (!hasBP) return;
    for (const [code, valueCode] of [
      ['BP_ARM', reading.arm],
      ['BP_POSITION', reading.position],
      ['BP_CUFF', reading.cuff],
    ] as const) {
      observations.push({
        event_id: ids.perValue(index, code),
        code,
        value_code: valueCode,
        effective_at: at,
      });
    }
  });

  return { event_id: ids.batch, patient_id: ids.patient, observations };
}
