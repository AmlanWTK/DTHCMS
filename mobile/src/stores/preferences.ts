import { getLocales } from 'expo-localization';
import { create } from 'zustand';

/**
 * The interface language.
 *
 * The default comes from the device: a phone set up in Bangla opens the app in Bangla.
 * The toggle changes it instantly for the session.
 *
 * Deliberately not persisted yet. The persistence layer for preferences is the local
 * database at CP64 — the one store that survives offline — and secure storage is not a
 * preferences drawer: the wrapper's whole point is that only declared secrets live
 * there. Until CP64, a restart falls back to the device locale, which is almost always
 * the same answer.
 */

export type Language = 'en' | 'bn';

function deviceLanguage(): Language {
  const [first] = getLocales();
  return first?.languageCode === 'bn' ? 'bn' : 'en';
}

interface PreferencesState {
  language: Language;
  setLanguage: (language: Language) => void;
}

export const usePreferences = create<PreferencesState>((set) => ({
  language: deviceLanguage(),
  setLanguage: (language) => set({ language }),
}));
