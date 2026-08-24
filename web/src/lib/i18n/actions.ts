'use server';

import { cookies } from 'next/headers';
import { revalidatePath } from 'next/cache';

import { LOCALE_COOKIE, isLocale, type Locale } from '@/lib/i18n/config';

/**
 * Sets the interface language.
 *
 * A server action rather than a client-side state change, because the language has to
 * survive a reload and be known on the server before the first byte of HTML — otherwise
 * the shell renders in English and then flips, which on a slow tablet is a visible and
 * unpleasant flash.
 *
 * At CP16 this also writes the preference to the user's record. The cookie stays, because
 * the login page itself needs a language before anyone is signed in.
 */
export async function setLocale(locale: Locale): Promise<void> {
  if (!isLocale(locale)) return;

  const store = await cookies();
  store.set(LOCALE_COOKIE, locale, {
    path: '/',
    sameSite: 'lax',
    // One year. A preference that expires is a preference the person has to set again on
    // a shared clinic machine, in front of a waiting patient.
    maxAge: 60 * 60 * 24 * 365,
    httpOnly: false,
  });

  revalidatePath('/', 'layout');
}
