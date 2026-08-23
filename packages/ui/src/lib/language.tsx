import { createContext, useContext, useMemo, type ReactNode } from 'react';

import { scriptForLanguage, type Language, type Script } from '@dthcms/design-tokens';

/**
 * The interface language, and everything that follows from it.
 *
 * A language context rather than a prop on every component. The reason is not
 * convenience: `lang` has to reach the DOM as an attribute, because that is what tells
 * the browser which script to shape, what the token stylesheet keys its line height off,
 * and what a screen reader announces text in. A component that took language as a prop
 * and styled itself accordingly would look right and still be announced in the wrong
 * language.
 */

export interface LanguageValue {
  language: Language;
  script: Script;
  /** Picks the right side of a bilingual string. */
  t: (text: { en: string; bn: string }) => string;
}

const LanguageContext = createContext<LanguageValue | null>(null);

export interface LanguageProviderProps {
  language: Language;
  children: ReactNode;
  /**
   * Renders a wrapper element carrying `lang`. On by default.
   *
   * Turn it off only when the attribute is already set higher up — on `<html>` by the
   * application shell, for instance. Nested `lang` attributes are valid but make the
   * effective language harder to reason about.
   */
  wrapper?: boolean;
}

export function LanguageProvider({ language, children, wrapper = true }: LanguageProviderProps) {
  const value = useMemo<LanguageValue>(
    () => ({
      language,
      script: scriptForLanguage[language],
      t: (text) => text[language],
    }),
    [language],
  );

  return (
    <LanguageContext.Provider value={value}>
      {wrapper ? (
        <div lang={language} className="dthc-lang">
          {children}
        </div>
      ) : (
        children
      )}
    </LanguageContext.Provider>
  );
}

/**
 * Reads the interface language.
 *
 * Defaults to English rather than throwing when no provider is present. A primitive that
 * crashed outside a provider would make every test and every Storybook story carry
 * boilerplate, and the failure mode of the default — English text in an unwrapped tree —
 * is visible immediately, where a thrown error in a clinical screen is not.
 */
export function useLanguage(): LanguageValue {
  const value = useContext(LanguageContext);
  if (value) return value;

  return {
    language: 'en',
    script: 'latin',
    t: (text) => text.en,
  };
}
