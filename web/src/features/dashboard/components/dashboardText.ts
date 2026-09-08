import type { Locale } from '@/lib/i18n/config';

/**
 * The words this screen needs that are not in the message files (CP73).
 *
 * # Why these are functions and not translation keys
 *
 * Everything with a fixed English and Bengali sentence lives in `messages/`. What lives here
 * is the handful of labels that come from **server data** — an observation code, a gap code, a
 * BMI band — where the message file has an entry per value and the lookup has to be safe
 * against a value nobody has written a sentence for yet.
 *
 * That last property is the whole reason this file exists. A server that starts sending a new
 * observation code tomorrow must produce a screen that shows the code rather than a screen
 * that shows `dashboard.trend.code.NEW_THING` or, worse, nothing. A missing sentence is a
 * content gap; a blank where a clinical value's name should be is a screen a physician
 * mistrusts.
 */

/**
 * What these helpers need from `next-intl`'s translator: the call, and `has`.
 *
 * `has` is the important half. Asking for a message that is not there *logs a console error*
 * and returns the key, so a screen drawing forty observation codes with a handful of unnamed
 * ones fills a physician's console with errors about a fallback that is working perfectly.
 * Worse, it makes a genuinely broken key indistinguishable from a deliberate fallback in the
 * one place somebody would look for it.
 */
type Translate = ((key: string, values?: Record<string, string | number>) => string) & {
  has: (key: string) => boolean;
};

/**
 * What a code is called, in the reader's language.
 *
 * The registry has bilingual display names and the dashboard payload does not carry them —
 * that is a deliberate omission rather than an oversight: `GET /v1/observations/codes` is
 * reference data with no patient in it, fetched once per session and cached, and copying
 * forty display names onto every dashboard response would be sending the same dictionary with
 * every patient. Until a screen needs the whole registry, the four codes §8's snapshot draws
 * have sentences in the message file.
 */
export function codeLabel(code: string, _locale: Locale, t: Translate): string {
  const key = `code.${code}`;
  // Showing the raw code is the honest fallback: `HBA1C` is readable by the person this
  // screen is for, and `dashboard.trend.code.HBA1C` is not. A patient's record legitimately
  // contains codes nobody has written a name for — a waist circumference, a hip — and drawing
  // those as the code is right until somebody adds the sentence.
  return t.has(key) ? t(key) : code;
}

/**
 * A missing-data alert's sentence.
 *
 * The server sends both a stable `code` and an English `detail`. The code is what a message
 * file can translate; the detail is the assembler's own English sentence and is the fallback.
 * Preferring the translation means a Bengali reader gets Bengali where somebody has written
 * it, and the assembler's English where nobody has — which is better than the alternative in
 * both directions, and says out loud which one is being shown by carrying the code as well.
 */
export function gapSentence(code: string, detail: string, t: Translate): string {
  const key = `gap.${code}`;
  return t.has(key) ? t(key) : detail;
}

/**
 * A BMI band's name, or `null` when the server sent no band.
 *
 * `null` rather than a "no band" sentence, and that is not a style preference: the i18n
 * scanner attributes a literal translation call to the namespace of the `useTranslations` call
 * in the same file, and this file has none. A sentence composed here would be a key the check could
 * not see, in the one file where it is most likely to be wrong. The component owns the
 * sentence; this owns the lookup.
 */
export function bmiClassLabel(className: string | undefined, t: Translate): string | null {
  if (!className) return null;
  const key = `bmi.${className}`;
  return t.has(key) ? t(key) : className;
}

/**
 * How urgently a gap should be read: the server's two levels, not five.
 *
 * Mapped to the design tokens' status vocabulary rather than to a colour, so that the panel
 * uses the same visual language as every other status in the application. `important` is
 * `borderline` and not `critical`: a missing eye examination is a thing to do and not an
 * emergency, and a panel where everything is red is a panel nobody reads.
 */
export function gapTone(severity: string | undefined): 'borderline' | 'unknown' {
  return severity === 'important' ? 'borderline' : 'unknown';
}
