import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, type RenderResult } from '@testing-library/react';
import { NextIntlClientProvider } from 'next-intl';
import type { ReactElement, ReactNode } from 'react';

import { LanguageProvider } from '@dthcms/ui';

import en from '../messages/en.json';
import bn from '../messages/bn.json';
import { DIRECTORY_KEY, type Directory } from '@/features/attribution';
import type { Locale } from '@/lib/i18n/config';

/**
 * Renders a component the way the application renders it.
 *
 * The providers are not boilerplate to be skipped: a component tested outside
 * NextIntlClientProvider silently falls back, and a component tested outside
 * LanguageProvider gets English from @dthcms/ui regardless of what next-intl is doing.
 * Either would make a bilingual test pass while the real screen was wrong.
 *
 * # Why the staff directory is seeded (CP61)
 *
 * Every clinical value now renders through `ValueWithAttribution`, which resolves the ids on
 * the value into names through one cached request per session. Left unseeded, every test
 * that renders any clinical value would issue a real `GET /v1/directory` at the API's
 * origin, resolve it after the assertions had run, and update React outside `act` — which is
 * a warning on dozens of unrelated files and, worse, a source of order-dependent flakiness.
 *
 * The default is an **empty** directory rather than a populated one on purpose. It is a real
 * state — the request answered, and this person is not in it — so a screen tested against it
 * exercises the honest fallback rather than a fixture's names. A test that cares about a
 * resolved name passes its own; a test that wants the failed-request path passes `null` and
 * stubs the call itself.
 */

export const messages = { en, bn } as const;

/** A directory that answered and contains nobody. See the note above. */
export function emptyDirectory(): Directory {
  return { staff: [], devices: [], stations: [], as_of: '2026-09-01T00:00:00Z' };
}

export function renderWithProviders(
  ui: ReactElement,
  {
    locale = 'en' as Locale,
    directory = emptyDirectory(),
  }: { locale?: Locale; directory?: Directory | null } = {},
): RenderResult {
  const queryClient = new QueryClient({
    defaultOptions: {
      // Retries turn a deliberate failure in a test into a several-second wait.
      queries: { retry: false },
      mutations: { retry: false },
    },
  });

  // Seeded before the first render, so the attribution component never has an in-flight
  // query to settle after a test has finished asserting. `null` leaves the cache empty for
  // the tests that are about what happens when the directory cannot be read.
  if (directory !== null) queryClient.setQueryData(DIRECTORY_KEY, directory);

  function Wrapper({ children }: { children: ReactNode }) {
    return (
      <NextIntlClientProvider locale={locale} messages={messages[locale]} timeZone="Asia/Dhaka">
        <QueryClientProvider client={queryClient}>
          <LanguageProvider language={locale} wrapper={false}>
            {children}
          </LanguageProvider>
        </QueryClientProvider>
      </NextIntlClientProvider>
    );
  }

  return render(ui, { wrapper: Wrapper });
}
