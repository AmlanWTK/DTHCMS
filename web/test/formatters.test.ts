import { describe, expect, it } from 'vitest';

import {
  CLINIC_TIME_ZONE,
  NUMERALS,
  formatCount,
  formatDateTime,
  formatMeasurement,
} from '@/lib/formatters';

/**
 * Numerals and time, which are the two places a bilingual clinical interface quietly goes
 * wrong.
 *
 * The numeral policy is an OPEN DECISION for Dr. Nahid. What is tested here is not which
 * answer is right — it is that the answer lives in one table and that the table is
 * actually what the formatters obey, so changing it changes the application.
 */

describe('measurements stay in ASCII digits', () => {
  it('renders the same digits in both languages', () => {
    // A reading transcribed onto a paper chart, read back over a phone, or compared
    // against a lab printout is the place where two numeral systems in circulation
    // becomes a transcription error.
    expect(formatMeasurement(7.8, 'en')).toBe('7.8');
    expect(formatMeasurement(7.8, 'bn')).toBe('7.8');
  });

  it('does not group, at any magnitude', () => {
    expect(formatMeasurement(1234.5, 'en')).toBe('1234.5');
    expect(formatMeasurement(1234.5, 'bn')).toBe('1234.5');
  });

  it('keeps the requested precision rather than trimming it', () => {
    // 5.0 is not 5. In a clinical value the trailing zero says the measurement was taken
    // to that precision.
    expect(formatMeasurement(5, 'en')).toBe('5.0');
    expect(formatMeasurement(5, 'en', { decimals: 2 })).toBe('5.00');
  });
});

describe('counts in running text follow the language', () => {
  it('renders Bengali digits in Bangla', () => {
    expect(formatCount(3, 'bn')).toBe('৩');
    expect(formatCount(3, 'en')).toBe('3');
  });
});

describe('the policy table is the decision', () => {
  it('holds a choice for every kind of number', () => {
    expect(Object.keys(NUMERALS).sort()).toEqual(['count', 'date', 'identifier', 'measurement']);
  });

  it('keeps anything transcribable in ASCII', () => {
    expect(NUMERALS.measurement).toBe('latn');
    expect(NUMERALS.identifier).toBe('latn');
    expect(NUMERALS.date).toBe('latn');
  });
});

describe('time is clinic time', () => {
  it('is pinned to Dhaka, not to the browser', () => {
    // A clinic in Dhaka has one working day. A visit that renders at a different hour
    // because a laptop is set to UTC is one record two people read as two different times.
    expect(CLINIC_TIME_ZONE).toBe('Asia/Dhaka');
  });

  it('renders the Dhaka hour regardless of where it is rendered', () => {
    // 2026-08-23T04:30:00Z is 10:30 in Dhaka (UTC+6, no daylight saving).
    const instant = Date.UTC(2026, 7, 23, 4, 30);
    expect(formatDateTime(instant, 'en')).toContain('10:30');
  });

  it('uses ASCII digits for a date in Bangla', () => {
    const instant = Date.UTC(2026, 7, 23, 4, 30);
    expect(formatDateTime(instant, 'bn')).toMatch(/\d/);
    expect(formatDateTime(instant, 'bn')).not.toMatch(/[০-৯]/);
  });
});
