import { describe, expect, it } from 'vitest';

import {
  CLINIC_TIME_ZONE,
  NUMERALS,
  formatCount,
  formatDate,
  formatDateTime,
  formatMeasurement,
  formatPartialDate,
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

describe('a date is rendered no more exactly than it was given', () => {
  // A history item's onset carries a precision (CP53). "About two years ago" is a real
  // answer, and printing it as a day turns a recollection into a measurement — which the
  // next reader has no way of telling apart, because on screen they look identical.
  const onset = Date.parse('2024-03-14T00:00:00Z');

  it('shows the year alone when the year is all she said', () => {
    expect(formatPartialDate(onset, 'en', 'year')).toBe('2024');
    expect(formatPartialDate(onset, 'bn', 'year')).toBe('2024');
  });

  it('shows the month and the year when that is what she said', () => {
    expect(formatPartialDate(onset, 'en', 'month')).toMatch(/March 2024/);
    expect(formatPartialDate(onset, 'bn', 'month')).toContain('2024');
  });

  it('shows the whole date when the date is exact', () => {
    expect(formatPartialDate(onset, 'en', 'day')).toBe(formatDate(onset, 'en'));
    expect(formatPartialDate(onset, 'en', 'day')).toMatch(/14/);
  });

  it('keeps date numerals in ASCII in both languages', () => {
    // The `date` row of the table: a date may be read back over a phone or copied onto a
    // paper chart, and two numeral systems around one is a transcription error waiting.
    expect(formatPartialDate(onset, 'bn', 'month')).not.toMatch(/[০-৯]/);
    expect(formatDate(onset, 'bn')).not.toMatch(/[০-৯]/);
  });

  it('renders in clinic time, not the browser’s', () => {
    // Just before midnight in Dhaka is still the same day there and the previous day in
    // UTC. A date that shifted with the laptop's zone is one record two people read as two.
    const lateEvening = Date.parse('2024-03-14T18:30:00Z');
    expect(formatDate(lateEvening, 'en')).toMatch(/15/);
  });
});
