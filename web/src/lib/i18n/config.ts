/**
 * Locale configuration.
 *
 * The locale is not in the URL — see the note in next.config.ts. It lives in a cookie,
 * which at CP16 becomes a mirror of the signed-in user's stored preference. Until then
 * the cookie is the whole story.
 */

export const LOCALES = ['en', 'bn'] as const;

export type Locale = (typeof LOCALES)[number];

/**
 * English by default.
 *
 * Not because it is the more important language here — the clinic works in Bangla — but
 * because a person who has expressed no preference is most often a new staff member on a
 * shared machine, and the interface has to be legible to whoever set the account up.
 * Once a preference exists it always wins.
 */
export const DEFAULT_LOCALE: Locale = 'en';

export const LOCALE_COOKIE = 'dthcms.locale';

/** The name each language calls itself, which is the only correct label for a switcher. */
export const LOCALE_NAMES: Record<Locale, string> = {
  en: 'English',
  bn: 'বাংলা',
};

export function isLocale(value: unknown): value is Locale {
  return typeof value === 'string' && (LOCALES as readonly string[]).includes(value);
}
