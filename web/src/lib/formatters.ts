import type { Locale } from '@/lib/i18n/config';

/**
 * Formatting, and the numeral question.
 *
 * Bangladesh reads Bengali digits — ৭.৮ — in prose, and ASCII digits on a lab printout.
 * Both are correct in their place, which makes "which numerals?" a decision that has to
 * be taken per kind of number rather than per language.
 *
 * The table below is the entire decision surface. It is an OPEN DECISION for Dr. Nahid,
 * and the default taken here is the conservative one: anything a person might transcribe
 * onto a paper chart, read back over a phone, or compare against a lab report stays in
 * ASCII, because two numeral systems in circulation around the same measurement is a
 * transcription error waiting to happen. Counts in running text follow the language.
 */

export const CLINIC_TIME_ZONE = 'Asia/Dhaka';

export type NumberKind = 'measurement' | 'identifier' | 'date' | 'count';

/** 'latn' is ASCII digits; 'beng' is Bengali; 'locale' means whatever the language uses. */
type NumeralChoice = 'latn' | 'beng' | 'locale';

export const NUMERALS: Record<NumberKind, NumeralChoice> = {
  measurement: 'latn',
  identifier: 'latn',
  date: 'latn',
  count: 'locale',
};

function localeTag(locale: Locale, kind: NumberKind): string {
  const choice = NUMERALS[kind];
  return choice === 'locale' ? locale : `${locale}-u-nu-${choice}`;
}

/**
 * A clinical measurement.
 *
 * Grouping is off. A glucose reading is never four digits in a context where a thousands
 * separator helps, and a separator that differs between locales is one more thing that
 * can be misread.
 */
export function formatMeasurement(
  value: number,
  locale: Locale,
  options: { decimals?: number } = {},
): string {
  const decimals = options.decimals ?? 1;
  return new Intl.NumberFormat(localeTag(locale, 'measurement'), {
    minimumFractionDigits: decimals,
    maximumFractionDigits: decimals,
    useGrouping: false,
  }).format(value);
}

/** A count in running text — "3 patients waiting". Follows the language. */
export function formatCount(value: number, locale: Locale): string {
  return new Intl.NumberFormat(localeTag(locale, 'count')).format(value);
}

/**
 * A date and time, always in clinic time.
 *
 * Not the browser's zone. A clinic in Dhaka has one working day, and a visit that renders
 * at a different hour because a laptop is set to UTC is one record two people read as two
 * different times.
 */
export function formatDateTime(value: Date | number, locale: Locale): string {
  return new Intl.DateTimeFormat(localeTag(locale, 'date'), {
    timeZone: CLINIC_TIME_ZONE,
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(value);
}

export function formatDate(value: Date | number, locale: Locale): string {
  return new Intl.DateTimeFormat(localeTag(locale, 'date'), {
    timeZone: CLINIC_TIME_ZONE,
    dateStyle: 'medium',
  }).format(value);
}

/** How exact a date somebody gave is. A history item's onset carries one (CP53). */
export type DatePrecision = 'day' | 'month' | 'year';

/**
 * A date rendered no more exactly than it was given.
 *
 * A patient who says "about two years ago" has answered the question, and the answer is
 * stored as a date with a precision beside it. Printing that as "14 March 2024" turns a
 * recollection into a measurement — and the next reader has no way of telling which they
 * are looking at, because on screen they are identical.
 *
 * So the precision decides the format: a year alone, a month and a year, or the whole date.
 * Numerals follow the `date` row of the table above and stay in ASCII in both languages,
 * for the same reason every other date does — it may be read back over a phone or copied
 * onto a paper chart.
 */
export function formatPartialDate(
  value: Date | number,
  locale: Locale,
  precision: DatePrecision,
): string {
  const options: Intl.DateTimeFormatOptions =
    precision === 'year'
      ? { year: 'numeric' }
      : precision === 'month'
        ? { year: 'numeric', month: 'long' }
        : { dateStyle: 'medium' };

  return new Intl.DateTimeFormat(localeTag(locale, 'date'), {
    timeZone: CLINIC_TIME_ZONE,
    ...options,
  }).format(value);
}
