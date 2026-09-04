import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { ConsentPanel } from '@/features/consent';

/**
 * What this patient has agreed to (CP36, §15.1).
 *
 * Its own screen as well as a panel, because "has this patient consented to being called"
 * is a question asked away from the record — by whoever is about to make the call.
 */
export default async function Page({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <ConsentScreen id={id} />;
}

function ConsentScreen({ id }: { id: string }) {
  const t = useTranslations('patients.consent');
  return (
    <div className="app-stack">
      <PageHeader title={t('pageTitle')} description={t('lede')} />
      <ConsentPanel patientId={id} heading={false} />
    </div>
  );
}
