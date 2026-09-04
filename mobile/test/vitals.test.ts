import { describe, expect, it } from 'vitest';

import {
  VITAL_FIELDS,
  canonicalVital,
  emptyReading,
  flagsFor,
  halfABloodPressure,
  isBlank,
  outOfRange,
  parsedVital,
  resolveRange,
  toVitalsBatch,
  type Range,
  type Reading,
  type VitalKey,
} from '../src/features/vitals/form';

/**
 * Station 5's vitals (CP49).
 *
 * The layout is verified on a real phone; what is testable here is what decides whether the
 * record is right. Three things in particular: that a repeat reading is stored as a second
 * fact rather than a correction, that a blood pressure carries the arm and position it was
 * taken in, and that a value is flagged against what is normal *for this patient* rather than
 * against an adult band applied to a two-year-old.
 */

const ranges: Range[] = [
  { code: 'HEART_RATE', min_age_years: 18, low: 60, high: 100, approved: false },
  { code: 'HEART_RATE', min_age_years: 1, max_age_years: 6, low: 80, high: 140, approved: false },
  { code: 'SPO2', low: 95, approved: false },
  { code: 'BP_SYSTOLIC', min_age_years: 18, low: 90, high: 130, approved: false },
];

function reading(values: Partial<Record<VitalKey, [string, string]>>): Reading {
  const base = emptyReading();
  for (const [key, pair] of Object.entries(values)) {
    if (pair === undefined) continue;
    base.values[key as VitalKey] = { text: pair[0], unit: pair[1] };
  }
  return base;
}

describe('the form', () => {
  it('offers the fields in the order a clinician measures in', () => {
    // The automated cuff reports a blood pressure and a pulse together, the oximeter clips
    // on next, then the thermometer and the respiratory count. An operator working down a
    // screen in a different order from their hands skips one field every morning.
    expect(VITAL_FIELDS.map((f) => f.key)).toEqual([
      'systolic',
      'diastolic',
      'pulse',
      'spo2',
      'temperature',
      'respiratory',
    ]);
  });

  it('will not save half a blood pressure', () => {
    expect(halfABloodPressure(reading({ systolic: ['128', 'mm[Hg]'] }))).toBe(true);
    expect(
      halfABloodPressure(reading({ systolic: ['128', 'mm[Hg]'], diastolic: ['82', 'mm[Hg]'] })),
    ).toBe(false);
    // A reading with no blood pressure at all is not half of one.
    expect(halfABloodPressure(reading({ temperature: ['37.1', 'Cel'] }))).toBe(false);
  });

  it('treats an untouched reading as nothing to save', () => {
    expect(isBlank(emptyReading())).toBe(true);
    expect(isBlank(reading({ pulse: ['78', '/min'] }))).toBe(false);
  });

  it('converts Fahrenheit before anything is compared to a range', () => {
    expect(canonicalVital({ text: '98.6', unit: '[degF]' })).toBeCloseTo(37, 2);
    expect(parsedVital({ text: '98.6', unit: '[degF]' })).toBe(98.6);
  });
});

describe('what is normal for this patient', () => {
  it('uses the age band, not an adult band applied to a child', () => {
    // A pulse of 130 is ordinary in a three-year-old and a tachycardia in an adult. One band
    // wide enough for both flags nobody.
    const child = { sex: 'male' as const, ageYears: 3 };
    const adult = { sex: 'male' as const, ageYears: 41 };
    expect(outOfRange(resolveRange(ranges, 'HEART_RATE', child), 130)).toBeNull();
    expect(outOfRange(resolveRange(ranges, 'HEART_RATE', adult), 130)).toEqual({
      direction: 'high',
      limit: 100,
    });
  });

  it('takes the first match, because the server ordered them', () => {
    expect(
      resolveRange(ranges, 'HEART_RATE', { sex: 'male', ageYears: 41 })?.min_age_years,
    ).toBe(18);
  });

  it('flags a floor with no ceiling without inventing one', () => {
    // 100% is the top of the SpO2 scale. A range that flagged it would flag every healthy
    // patient until staff stopped reading the flag at all.
    const range = resolveRange(ranges, 'SPO2', { sex: 'male', ageYears: 41 });
    expect(outOfRange(range, 100)).toBeNull();
    expect(outOfRange(range, 91)).toEqual({ direction: 'low', limit: 95 });
  });

  it('flags against the canonical value, so Fahrenheit is not read as Celsius', () => {
    const withTemp: Range[] = [...ranges, { code: 'BODY_TEMP', low: 36.1, high: 37.5, approved: false }];
    const flags = flagsFor(
      reading({ temperature: ['98.6', '[degF]'] }),
      withTemp,
      { sex: 'male', ageYears: 41 },
    );
    // 98.6 °F is 37 °C, which is normal. Read as Celsius it would be flagged wildly high.
    expect(flags.temperature).toBeUndefined();
  });

  it('says nothing about a field nobody has typed in', () => {
    expect(flagsFor(emptyReading(), ranges, { sex: 'male', ageYears: 41 })).toEqual({});
  });
});

describe('what gets sent', () => {
  const ids = {
    batch: 'b',
    patient: 'p',
    perValue: (index: number, code: string) => `e-${index}-${code}`,
    takenAt: (index: number) => `2026-09-14T09:0${index}:00Z`,
  };

  it('keeps two readings as two facts, not a correction', () => {
    // A blood pressure measured once is a blood pressure measured badly. The second reading
    // is not a correction of the first, and a record that treated it as one would lose the
    // fact that they differed — which is often the finding.
    const body = toVitalsBatch(
      [
        reading({ systolic: ['148', 'mm[Hg]'], diastolic: ['94', 'mm[Hg]'] }),
        reading({ systolic: ['138', 'mm[Hg]'], diastolic: ['88', 'mm[Hg]'] }),
      ],
      ids,
    );
    const systolics = body.observations.filter((o) => o.code === 'BP_SYSTOLIC');
    expect(systolics).toHaveLength(2);
    expect(systolics.map((o) => o.value)).toEqual([148, 138]);
    // Different times, so the timeline can order them. Nothing replaces anything.
    expect(new Set(systolics.map((o) => o.effective_at)).size).toBe(2);
    expect(JSON.stringify(body)).not.toContain('replaces');
  });

  it('records the arm, position and cuff with the blood pressure', () => {
    const body = toVitalsBatch(
      [reading({ systolic: ['128', 'mm[Hg]'], diastolic: ['82', 'mm[Hg]'] })],
      ids,
    );
    const codes = body.observations.map((o) => o.code);
    expect(codes).toContain('BP_ARM');
    expect(codes).toContain('BP_POSITION');
    expect(codes).toContain('BP_CUFF');
    expect(body.observations.find((o) => o.code === 'BP_ARM')?.value_code).toBe('left');
  });

  it('does not record a cuff size beside a lone temperature', () => {
    // Context for a measurement that is not there is noise in the record.
    const body = toVitalsBatch([reading({ temperature: ['37.1', 'Cel'] })], ids);
    expect(body.observations.map((o) => o.code)).toEqual(['BODY_TEMP']);
  });

  it('sends the number as typed with the unit as selected', () => {
    const body = toVitalsBatch([reading({ temperature: ['98.6', '[degF]'] })], ids);
    expect(body.observations[0]).toMatchObject({ value: 98.6, unit: '[degF]' });
  });

  it('skips a reading nobody filled in', () => {
    const body = toVitalsBatch([reading({ pulse: ['78', '/min'] }), emptyReading()], ids);
    expect(body.observations).toHaveLength(1);
  });

  it('gives every value a stable id, so a retry writes the same set', () => {
    const first = toVitalsBatch([reading({ pulse: ['78', '/min'] })], ids);
    const second = toVitalsBatch([reading({ pulse: ['78', '/min'] })], ids);
    expect(first.observations[0]!.event_id).toBe(second.observations[0]!.event_id);
  });
});
