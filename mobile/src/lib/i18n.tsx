import { IntlProvider } from 'use-intl';
import type { ReactNode } from 'react';

import bn from '@/messages/bn.json';
import en from '@/messages/en.json';
import { usePreferences, type Language } from '@/stores/preferences';

/**
 * i18n for the station app.
 *
 * `use-intl` rather than the plan's `i18n-js` — a small, recorded deviation. It is the
 * framework-agnostic core of the `next-intl` the web shell already uses, so both
 * surfaces speak the same ICU message format, share the same key discipline, and are
 * checked by the same style of completeness test. Two ICU dialects across two surfaces
 * is exactly the kind of drift the token pipeline exists to prevent in colour; this
 * prevents it in language.
 *
 * The clinic's time zone is pinned for the same reason as on the web: a visit timestamp
 * that renders differently because a tablet is set to UTC is one record two people read
 * as two different times.
 */

export const MESSAGES: Record<Language, Record<string, unknown>> = { en, bn };

export const CLINIC_TIME_ZONE = 'Asia/Dhaka';

export function I18nProvider({ children }: { children: ReactNode }) {
  const language = usePreferences((state) => state.language);

  return (
    <IntlProvider
      locale={language}
      messages={MESSAGES[language] as never}
      timeZone={CLINIC_TIME_ZONE}
    >
      {children}
    </IntlProvider>
  );
}
