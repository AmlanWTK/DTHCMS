import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { JobsConsole } from '@/features/jobs';

/**
 * Administration → Background work (CP69, ADR-0031, §7.1).
 *
 * # Why it is filed under administration and not under QA
 *
 * `ops.jobs.read` reaches the physician, QA and the administrator; `ops.jobs.manage` reaches
 * the administrator alone. All three already hold `audit.read`, so this adds a link to a group
 * heading every one of them can already see rather than opening a new area for anybody — and
 * filing it under QA would have put the two controls behind a heading the one person who may
 * use them does not otherwise visit.
 *
 * It sits beside the audit trail and the device console for a plainer reason too: those are the
 * other two screens somebody opens when they are asking what the system did rather than what a
 * patient did, and an operator working an incident should not have to remember which area each
 * of the three lives in.
 */
export default function Page() {
  // The `page.` namespace rather than the feature's own, so the sidebar entry, the breadcrumb
  // and this heading are one string.
  const t = useTranslations('page.jobs');

  return (
    <div className="app-stack">
      <PageHeader title={t('title')} description={t('description')} />
      <JobsConsole />
    </div>
  );
}
