import { afterEach, describe, expect, it, vi } from 'vitest';

/**
 * The language preference store.
 *
 * Two behaviours matter and neither is obvious from reading the file. The default is
 * computed once, at module load, from the device locale — so a phone set up in Bangla
 * opens the app in Bangla without anybody choosing anything. And the store deliberately
 * does not persist: CP11 recorded that preferences wait for the encrypted database at
 * CP64, because secure storage is an allowlist of secrets and not a preferences drawer.
 *
 * Testing a module-load default means re-importing the module per case, which is why
 * these use `resetModules` rather than a shared import.
 */

async function loadWith(languageCode: string | undefined) {
  vi.resetModules();
  vi.doMock('expo-localization', () => ({
    getLocales: () => (languageCode === undefined ? [] : [{ languageCode }]),
  }));
  return import('../src/stores/preferences');
}

afterEach(() => {
  vi.doUnmock('expo-localization');
  vi.resetModules();
});

describe('the default language', () => {
  it('follows a Bangla device', async () => {
    const { usePreferences } = await loadWith('bn');
    expect(usePreferences.getState().language).toBe('bn');
  });

  it('follows an English device', async () => {
    const { usePreferences } = await loadWith('en');
    expect(usePreferences.getState().language).toBe('en');
  });

  it('falls back to English for a locale the app does not speak', async () => {
    // A device set to Hindi or Arabic gets English rather than an empty interface. The
    // clinic's two languages are the two the application has messages for.
    const { usePreferences } = await loadWith('hi');
    expect(usePreferences.getState().language).toBe('en');
  });

  it('falls back to English when the device reports no locale at all', async () => {
    // getLocales() returning empty is rare but real on a freshly wiped device.
    const { usePreferences } = await loadWith(undefined);
    expect(usePreferences.getState().language).toBe('en');
  });
});

describe('changing it', () => {
  it('takes effect immediately, for the session', async () => {
    const { usePreferences } = await loadWith('en');

    usePreferences.getState().setLanguage('bn');
    expect(usePreferences.getState().language).toBe('bn');

    usePreferences.getState().setLanguage('en');
    expect(usePreferences.getState().language).toBe('en');
  });
});
