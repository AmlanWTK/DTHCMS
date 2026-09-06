'use client';

import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { MyQualityRecord } from '@/features/quality';

/**
 * My own correction record (CP63, §4.3, ADR-0029 §3).
 *
 * Its own entry in the sidebar, reachable by every signed-in member of staff and gated on
 * nothing. That is the checkpoint's central decision rather than a convenience: the endpoint
 * behind it reads the caller's own id from the session, so there is no version of it that
 * returns somebody else's work — and an operator who has to be granted something before they
 * may see their own correction count is an operator who will assume the count is being kept
 * from them. A number somebody can see is a number they can argue with, and a number they can
 * argue with is one they will not hide from.
 *
 * It sits in the clinical group because that is where the corrections queue is, and the two
 * are the same conversation seen from two ends. The consequence is accepted knowingly: an
 * entry offered to everybody puts the clinical heading in the sidebar of every account,
 * including ones that record nothing. The alternative was to gate it on the permission that
 * marks somebody as an operator — and the community field worker records values all day and
 * holds none of them, which is precisely the person this screen exists for.
 */
export default function Page() {
  // The `page.` namespace rather than the feature's own, so the sidebar entry, the breadcrumb
  // and this heading are one string.
  const t = useTranslations('page.myQuality');

  return (
    <div className="app-stack">
      <PageHeader title={t('title')} description={t('description')} />
      <MyQualityRecord />
    </div>
  );
}
