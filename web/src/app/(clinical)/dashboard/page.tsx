import { useTranslations } from 'next-intl';

import { EmptyState } from '@dthcms/ui';

import { PageHeader } from '@/components/PageHeader';
import { SystemStatusCard } from '@/features/system-status';

/**
 * The dashboard.
 *
 * The physician dashboard proper lands at CP73. What is here now is the one thing CP10
 * is allowed to fetch — the backend's health — rendered through the feature convention so
 * that the convention is demonstrated against something real rather than described in a
 * README.
 */
export default function DashboardPage() {
  const t = useTranslations();

  return (
    <>
      <PageHeader title={t('page.dashboard.title')} description={t('page.dashboard.description')} />

      <SystemStatusCard />

      <EmptyState icon="clock" title={t('placeholder.title', { area: t('nav.dashboard') })}>
        {t('placeholder.body', { checkpoint: 'CP73' })}
      </EmptyState>
    </>
  );
}
