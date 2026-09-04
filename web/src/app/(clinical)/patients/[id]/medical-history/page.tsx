import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { MedicalHistory } from '@/features/history';

/**
 * Station 4 — everything the patient brought with them (CP53).
 *
 * Its own screen rather than a card inside the visit, because the question it exists to ask
 * takes time: a returning patient's history arrives with nothing confirmed, and working
 * through it item by item is the station's whole job rather than a step in somebody else's.
 */
export default async function Page({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <MedicalHistoryScreen id={id} />;
}

function MedicalHistoryScreen({ id }: { id: string }) {
  const t = useTranslations('history');
  return (
    <div className="app-stack">
      <PageHeader title={t('pageTitle')} description={t('lede')} />
      <MedicalHistory patientId={id} />
    </div>
  );
}
