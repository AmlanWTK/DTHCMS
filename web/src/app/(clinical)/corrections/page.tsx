'use client';

import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { CorrectionQueue } from '@/features/corrections';

/**
 * What I am being asked to fix (CP62, §4.3).
 *
 * Its own entry in the sidebar rather than a panel on the dashboard, because the request was
 * routed to a person and a person needs somewhere to find it. A queue that lived inside another
 * screen would be a queue somebody has to remember to look at, and the whole mechanism rests on
 * the operator who typed the value learning that somebody asked about it.
 */
export default function Page() {
  // The `page.` namespace rather than the feature's own, so the sidebar entry, the breadcrumb
  // and this heading are one string. Two names for one screen is how a breadcrumb and a tab
  // come to disagree about what the operator is looking at.
  const t = useTranslations('page.corrections');

  return (
    <div className="app-stack">
      <PageHeader title={t('title')} description={t('description')} />
      <CorrectionQueue />
    </div>
  );
}
