import type { Locale } from '@/lib/i18n/config';

/**
 * Text that exists in two languages, one of which may be empty.
 *
 * Lifted out of the counselling panel's `panelText.ts` at CP61, unchanged, because the
 * attribution component needs exactly the same rule and a second implementation of it would
 * drift. Nothing here knows what it is naming — an item, a person, a room — which is why it
 * belongs beside `formatters.ts` rather than inside a feature.
 *
 * # Why falling back is a decision each caller makes, and why this one says it did
 *
 * `counselingText.ts` deliberately refuses to fall back: on an authoring screen the missing
 * half is the thing the author is there to find, and quietly showing the English would hide
 * it. On a reading screen the opposite is true — a physician standing in front of a patient
 * needs the item, and a blank where the Bangla is missing removes it from the record
 * altogether.
 *
 * So this falls back **and reports that it did**. `translated: false` means the reader is
 * looking at the other language and the caller may say so; `lang` is what the text is
 * actually in, for the `lang` attribute a screen reader needs to pronounce it. A helper that
 * fell back silently would make the two rules indistinguishable at the call site.
 */
export interface BilingualText {
  text: string;
  /** Which language the text is actually in, for the `lang` attribute a screen reader uses. */
  lang: Locale;
  /** False when the reader's own language was empty and the other one is being shown. */
  translated: boolean;
}

/** The reader's language where it has text, the other where it does not, `null` for neither. */
export function bilingual(en: string, bn: string, locale: Locale): BilingualText | null {
  const mine = (locale === 'bn' ? bn : en).trim();
  if (mine !== '') return { text: mine, lang: locale, translated: true };

  const other = (locale === 'bn' ? en : bn).trim();
  if (other === '') return null;
  return { text: other, lang: locale === 'bn' ? 'en' : 'bn', translated: false };
}
