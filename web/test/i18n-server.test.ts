import { beforeEach, describe, expect, it, vi } from 'vitest';

import { CLINIC_TIME_ZONE } from '@/lib/formatters';
import { DEFAULT_LOCALE, LOCALE_COOKIE } from '@/lib/i18n/config';

/**
 * The two pieces of language handling that run on the server.
 *
 * Everything else about the bilingual interface is checked in `i18n.test.ts` against the
 * message files. This is the other half — the part that decides, before a byte of HTML is
 * sent, *which* of those files a request gets and how long that choice survives. It has no
 * screen of its own, so nothing here fails visibly:
 *
 *  - **a locale read from an unchecked cookie.** The cookie is client-writable by design
 *    (the sign-in page needs a language before anyone is signed in), so anything at all can
 *    arrive in it. An unvalidated value becomes `import('../../../messages/ar.json')`, which
 *    throws, and the whole page 500s for a visitor who did nothing but hold a stale cookie.
 *  - **a time zone taken from the browser.** The clinic has one working day. A visit
 *    timestamp rendered against a laptop set to UTC is a record two people read as two
 *    different times, six hours apart, with no indication that either is wrong.
 *  - **a preference that does not survive a reload**, which on a shared clinic machine means
 *    setting the language again in front of a waiting patient.
 *
 * `next/headers`, `next/cache` and `next-intl/server` are stood in for, because they only
 * exist inside a Next.js request. `getRequestConfig` is the identity function in the
 * react-server build — it returns the callback it is given — so the stand-in reproduces what
 * production does rather than replacing it; the logic under test is entirely ours.
 */

const jar = vi.hoisted(() => ({
  value: undefined as string | undefined,
  set: vi.fn(),
}));
const revalidatePath = vi.hoisted(() => vi.fn());

vi.mock('next/headers', () => ({
  cookies: () =>
    Promise.resolve({
      get: (name: string) => (jar.value === undefined ? undefined : { name, value: jar.value }),
      set: jar.set,
    }),
}));
vi.mock('next/cache', () => ({ revalidatePath }));
vi.mock('next-intl/server', () => ({
  getRequestConfig: (create: unknown) => create,
}));

const requestConfig = (await import('@/lib/i18n/request')).default;
const { setLocale } = await import('@/lib/i18n/actions');

/** What next-intl passes in. There is no `[locale]` segment here; the cookie is the source. */
const NO_SEGMENT = { requestLocale: Promise.resolve(undefined) };

function configure() {
  return requestConfig(NO_SEGMENT);
}

beforeEach(() => {
  jar.value = undefined;
  jar.set.mockClear();
  revalidatePath.mockClear();
});

describe('choosing the language for a request', () => {
  it('serves English to somebody who has never chosen', async () => {
    // Not because English matters more — the clinic works in Bangla — but because a person
    // with no preference is most often a new staff member on a shared machine, and the
    // interface has to be legible to whoever set the account up.
    const config = await configure();
    expect(config.locale).toBe(DEFAULT_LOCALE);
    expect(config.locale).toBe('en');
  });

  it('serves Bangla to somebody who has chosen it', async () => {
    jar.value = 'bn';
    const config = await configure();
    expect(config.locale).toBe('bn');
  });

  it('reads the preference from the cookie the switcher writes', async () => {
    // The two halves have to name the same cookie. If they ever drift, setting the language
    // appears to work — the page reloads — and comes back in English.
    jar.value = 'bn';
    await setLocale('bn');
    expect(jar.set.mock.calls[0]?.[0]).toBe(LOCALE_COOKIE);
    expect((await configure()).locale).toBe('bn');
  });

  it.each([
    { cookie: 'ar', why: 'a language we do not ship' },
    { cookie: 'EN', why: 'the right language in the wrong case' },
    { cookie: 'en-GB', why: 'a full BCP-47 tag rather than our short code' },
    { cookie: '', why: 'an empty cookie left behind by something else' },
    { cookie: '../../etc/passwd', why: 'a path, because the cookie is client-writable' },
    { cookie: '__proto__', why: 'a property name, for the same reason' },
  ])('falls back to English when the cookie names $why', async ({ cookie }) => {
    // The value goes into a dynamic import path. Anything unvalidated is either a 500 for a
    // visitor holding a stale cookie or a read of a file nobody meant to serve.
    jar.value = cookie;
    const config = await configure();
    expect(config.locale).toBe('en');
  });

  it('loads the messages themselves, not a promise of a module', async () => {
    // The default export of the JSON module, unwrapped. Handing next-intl the module object
    // renders every string on every screen as its own key — `shell.patientsWaiting` in a
    // clinic waiting room.
    const config = await configure();
    const messages = config.messages as Record<string, Record<string, string>>;
    expect(messages.growth?.pageTitle).toBe('Growth');
    expect(messages).not.toHaveProperty('default');
  });

  it('loads the Bangla file when Bangla is chosen', async () => {
    jar.value = 'bn';
    const config = await configure();
    const messages = config.messages as Record<string, Record<string, string>>;
    expect(messages.growth?.pageTitle).toBe('বৃদ্ধি');
  });

  it.each([undefined, 'en', 'bn', 'ar'])(
    'pins the clinic time zone whatever the cookie says (%s)',
    async (cookie) => {
      // Never the browser's. A clinic in Dhaka has one working day, and a timestamp that
      // renders differently because a laptop is set to UTC is a record two people read as
      // two different times.
      jar.value = cookie;
      const config = await configure();
      expect(config.timeZone).toBe(CLINIC_TIME_ZONE);
      expect(config.timeZone).toBe('Asia/Dhaka');
    },
  );
});

describe('setting the language', () => {
  it.each(['en', 'bn'] as const)('writes %s to the cookie', async (locale) => {
    await setLocale(locale);
    expect(jar.set).toHaveBeenCalledTimes(1);
    const [name, value] = jar.set.mock.calls[0]!;
    expect(name).toBe(LOCALE_COOKIE);
    expect(value).toBe(locale);
  });

  it('keeps the preference for a year, on the whole site', async () => {
    // A preference that expires is a preference somebody has to set again on a shared clinic
    // machine, in front of a waiting patient. Scoped to `/` because the language is not a
    // property of one page.
    await setLocale('bn');
    const options = jar.set.mock.calls[0]![2] as Record<string, unknown>;
    expect(options.path).toBe('/');
    expect(options.maxAge).toBe(60 * 60 * 24 * 365);
    expect(options.sameSite).toBe('lax');
  });

  it('leaves the cookie readable, because it is a preference and not a credential', async () => {
    // ADR-0010 forbids credentials JavaScript can read. This is not one: it is which of two
    // message files to load, and the client-side switcher wants to show the current choice.
    await setLocale('en');
    const options = jar.set.mock.calls[0]![2] as Record<string, unknown>;
    expect(options.httpOnly).toBe(false);
  });

  it('rebuilds the shell so the whole page comes back in the new language', async () => {
    // Without this the layout — the sidebar, the top bar, every label outside the changed
    // page — stays in the language it was rendered in, and half the screen is bilingual.
    await setLocale('bn');
    expect(revalidatePath).toHaveBeenCalledWith('/', 'layout');
  });

  it.each(['ar', 'EN', '', 'en-GB'])(
    'refuses %s rather than writing a language we cannot serve',
    async (value) => {
      // A cookie holding an unshippable locale would be read back by the request config on
      // every subsequent request. Refusing at the point of writing keeps the bad value out
      // of the jar entirely.
      await setLocale(value as never);
      expect(jar.set).not.toHaveBeenCalled();
      expect(revalidatePath).not.toHaveBeenCalled();
    },
  );

  it('changes nothing when the value is not a string at all', async () => {
    // A server action's argument arrives over the wire and is whatever the caller sent.
    for (const value of [null, undefined, 42, { locale: 'bn' }]) {
      await setLocale(value as never);
    }
    expect(jar.set).not.toHaveBeenCalled();
  });
});
