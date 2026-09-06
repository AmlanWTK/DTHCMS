'use client';

import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { SupervisorQuality } from '@/features/quality';

/**
 * The patterns worth a conversation, and whose they are (CP63, §4.3, ADR-0029 §2).
 *
 * Under `/qa` and behind `quality.read.team` — the chief consultant, QA and the administrator
 * — and deliberately not behind `hr.performance.read`, which already exists and which HR
 * holds. The plan puts performance-linked pay and discipline out of scope, and a permission
 * that hands an operator's correction history to the department that sets pay puts it back in
 * whatever anybody intends by it.
 *
 * A sibling of `/qa` rather than a panel inside it, because a supervisor comes here to do one
 * thing and a surface reached inside another surface is a surface found a week late.
 */
export default function Page() {
  const t = useTranslations('page.qualityTeam');

  return (
    <div className="app-stack">
      <PageHeader title={t('title')} description={t('description')} />
      <SupervisorQuality />
    </div>
  );
}
