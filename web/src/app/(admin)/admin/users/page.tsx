import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { UserDirectory } from '@/features/users';

/**
 * Administration → Users (CP21): every account at the clinic, and the door to each one.
 */
export default function UsersPage() {
  const t = useTranslations('users');

  return (
    <div className="app-stack">
      <PageHeader title={t('title')} description={t('description')} />
      <UserDirectory />
    </div>
  );
}
