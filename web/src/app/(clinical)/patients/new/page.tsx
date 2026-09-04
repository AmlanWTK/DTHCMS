import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { RegistrationForm } from '@/features/patients';

/**
 * Step 1 of the journey (CP32). The one screen where a keyboard beats a tablet, so the web
 * is the primary surface for it and the station app (CP33) is the secondary one.
 */
export default function Page() {
  const t = useTranslations('patients.register');
  return (
    <div className="app-stack">
      <PageHeader title={t('title')} description={t('description')} />
      <RegistrationForm />
    </div>
  );
}
