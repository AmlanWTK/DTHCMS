import { bilingual } from '@/lib/bilingual';
import { CLINIC_TIME_ZONE } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';

/**
 * Turning what a quality record carries into words (CP63).
 *
 * # There is almost nothing left of the label half of this file, and that is the point
 *
 * The measurement names, the correction reasons and the sentence describing each threshold all
 * arrive from the server as bilingual pairs now — on `by_code`, on a flag's evidence, and on
 * the threshold rows. The first version of this file resolved the first two out of the
 * observation code registry and the correction vocabulary, and that was wrong in a way worth
 * recording: both registries sit behind `observation.read.values`, which the administrator
 * holding `quality.read.team` does not necessarily have and which the community field worker
 * definitely does not — so the two readers most likely to be shown a database identifier were
 * exactly the two the workaround could not help.
 *
 * What is left is the fallback rule and the clock.
 *
 * # Nothing here invents a name
 *
 * Every label falls back to the code itself, never to a guess and never to a blank. A blank
 * would remove a row from a table a supervisor is using to decide whether to talk to somebody;
 * a guessed name for a code this build has not heard of would be a confident answer about the
 * wrong measurement.
 *
 * # Why the dates on this feature carry Bengali numerals and the rest of the clinic's do not
 *
 * `formatters.ts` keeps dates in ASCII in both languages, and its reason is good: a date on a
 * clinical record may be copied onto a paper chart, read back over a phone or compared against
 * a lab report, and two numeral systems in circulation around one measurement is a
 * transcription error waiting to happen.
 *
 * **None of that is true of these screens.** There is no measurement here, no lab report and
 * nothing a person transcribes: the dates are prose about somebody's own month — *"covering 6
 * August to 5 September"*, *"noticed 4 September"*, *"09:00"*. In prose the rule inverts. A
 * Latin numeral inside a Bangla sentence is the single thing that makes an interface read as
 * translated rather than written, and it is worse here than anywhere else in the application,
 * because the numbers are exactly what the reader of this screen is looking for and this is the
 * screen that has to earn an operator's trust.
 *
 * So this is a deliberate, local departure rather than an oversight, and it is local: nothing
 * outside this feature imports these, and `formatters.ts` is untouched.
 */

/** The reader's language where the server sent it, the other where it did not, else the code. */
export function labelOr(
  en: string | undefined,
  bn: string | undefined,
  locale: Locale,
  fallback: string,
): string {
  return bilingual(en ?? '', bn ?? '', locale)?.text ?? fallback;
}

/** A day, in the reader's own numerals and always on the clinic's clock. */
export function qualityDate(value: Date | number, locale: Locale): string {
  return new Intl.DateTimeFormat(locale, {
    timeZone: CLINIC_TIME_ZONE,
    dateStyle: 'medium',
  }).format(value);
}

/** A day and a time — when a pattern was noticed, when somebody answered it. */
export function qualityMoment(value: Date | number, locale: Locale): string {
  return new Intl.DateTimeFormat(locale, {
    timeZone: CLINIC_TIME_ZONE,
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(value);
}

/**
 * An hour on the clinic's wall clock, as a time, in the reader's own numerals.
 *
 * A 24-hour clock, and padded so a column of them lines up: 09:00 must not sit to the left of
 * 16:00 in a list somebody is scanning for a shape. The padding is `minimumIntegerDigits`
 * rather than a string pad, because a string pad would prepend an ASCII zero to a Bengali
 * numeral and produce "0৯" — which is the exact defect this rule exists to prevent, arriving
 * through the fix for it.
 *
 * The hour is the hour the **value was recorded**, not the hour somebody questioned it. The
 * server takes that trouble deliberately: a physician reviewing yesterday's file at nine in the
 * morning would otherwise make every operator on the floor look like a morning problem.
 */
export function hourLabel(hour: number, locale: Locale): string {
  const twoDigits = new Intl.NumberFormat(locale, {
    minimumIntegerDigits: 2,
    useGrouping: false,
  });
  return `${twoDigits.format(hour)}:${twoDigits.format(0)}`;
}
