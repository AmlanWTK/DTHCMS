'use client';

import { QueryClientProvider } from '@tanstack/react-query';
import { useState, type ReactNode } from 'react';

import { LanguageProvider } from '@dthcms/ui';

import { RealtimeProvider } from '@/features/realtime';
import { createQueryClient } from '@/lib/query';
import type { Locale } from '@/lib/i18n/config';

/**
 * Client-side providers.
 *
 * Two language systems meet here, and they must not disagree. next-intl owns the
 * application's own strings; @dthcms/ui carries its own bilingual text for the handful of
 * things a primitive says on its own behalf — "Loading", a numeric field's out-of-range
 * warning. Both are fed the same locale from one place, because a shell in Bangla whose
 * input errors appear in English is worse than either language used consistently.
 *
 * `wrapper={false}` because the root layout already puts `lang` on `<html>`, and nested
 * lang attributes make the effective language harder to reason about.
 *
 * The QueryClient is created in state rather than at module scope. At module scope one
 * client would be shared by every request the server renders, which on a server is a
 * cache shared between different people's sessions.
 */
export function Providers({ locale, children }: { locale: Locale; children: ReactNode }) {
  const [queryClient] = useState(createQueryClient);

  return (
    <QueryClientProvider client={queryClient}>
      <LanguageProvider language={locale} wrapper={false}>
        {/* Inside the query client, because what arrives on the socket becomes an
            invalidation and nothing else (CP27). */}
        <RealtimeProvider>{children}</RealtimeProvider>
      </LanguageProvider>
    </QueryClientProvider>
  );
}
