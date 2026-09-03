import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { SecuritySettings } from '@/features/auth';

/**
 * Account → Security: the authenticator app (CP17). Password change arrives with the
 * administrator console at CP21, where the reset path it needs also lives.
 */
export default function SecurityPage() {
  const t = useTranslations('security');

  return (
    <div className="app-stack">
      <PageHeader title={t('title')} description={t('description')} />
      <SecuritySettings />
    </div>
  );
}
