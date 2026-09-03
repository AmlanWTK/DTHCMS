import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { AdminHome } from '@/features/users';

/**
 * Administration (CP21): the console's front door. Each area is a card; the ones the
 * person may not enter are not drawn.
 */
export default function AdminPage() {
  const t = useTranslations('page.admin');

  return (
    <div className="app-stack">
      <PageHeader title={t('title')} description={t('description')} />
      <AdminHome />
    </div>
  );
}
