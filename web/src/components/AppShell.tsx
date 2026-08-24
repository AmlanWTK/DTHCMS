import type { ReactNode } from 'react';
import { useTranslations } from 'next-intl';

import { Breadcrumbs } from '@/components/Breadcrumbs';
import { OfflineBanner } from '@/components/OfflineBanner';
import { Sidebar } from '@/components/Sidebar';
import { Topbar } from '@/components/Topbar';

/**
 * The frame every signed-in screen sits in.
 *
 * A server component: it holds no state of its own, and the pieces that do — the sidebar
 * drawer, the language switch, the connection watcher — are client components underneath
 * it. Keeping the frame on the server means a screen's own data can stream into it
 * without the whole shell being client-rendered first.
 */
export function AppShell({ children }: { children: ReactNode }) {
  const t = useTranslations();

  return (
    <div className="app-shell">
      <a className="skip-link" href="#main">
        {t('nav.skipToContent')}
      </a>

      <Topbar />
      <Sidebar />

      <main className="app-main" id="main">
        <Breadcrumbs />
        <OfflineBanner />
        {children}
      </main>
    </div>
  );
}
