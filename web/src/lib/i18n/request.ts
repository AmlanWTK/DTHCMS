import { cookies } from 'next/headers';
import { getRequestConfig } from 'next-intl/server';

import { CLINIC_TIME_ZONE } from '@/lib/formatters';
import { DEFAULT_LOCALE, LOCALE_COOKIE, isLocale } from '@/lib/i18n/config';

/**
 * Resolves the locale and messages for a request.
 *
 * The time zone is pinned rather than taken from the browser. A clinic in Dhaka has one
 * working day, and a visit timestamp that renders differently because a laptop is set to
 * UTC is a record two people read as two different times.
 */
export default getRequestConfig(async () => {
  const store = await cookies();
  const cookieValue = store.get(LOCALE_COOKIE)?.value;
  const locale = isLocale(cookieValue) ? cookieValue : DEFAULT_LOCALE;

  const messages = (await import(`../../../messages/${locale}.json`)) as {
    default: Record<string, unknown>;
  };

  return {
    locale,
    messages: messages.default,
    timeZone: CLINIC_TIME_ZONE,
  };
});
