import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, type RenderResult } from '@testing-library/react';
import { NextIntlClientProvider } from 'next-intl';
import type { ReactElement, ReactNode } from 'react';

import { LanguageProvider } from '@dthcms/ui';

import en from '../messages/en.json';
import bn from '../messages/bn.json';
import type { Locale } from '@/lib/i18n/config';

/**
 * Renders a component the way the application renders it.
 *
 * The providers are not boilerplate to be skipped: a component tested outside
 * NextIntlClientProvider silently falls back, and a component tested outside
 * LanguageProvider gets English from @dthcms/ui regardless of what next-intl is doing.
 * Either would make a bilingual test pass while the real screen was wrong.
 */

export const messages = { en, bn } as const;

export function renderWithProviders(
  ui: ReactElement,
  { locale = 'en' as Locale }: { locale?: Locale } = {},
): RenderResult {
  const queryClient = new QueryClient({
    defaultOptions: {
      // Retries turn a deliberate failure in a test into a several-second wait.
      queries: { retry: false },
      mutations: { retry: false },
    },
  });

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
