import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { PrescribeScreen } from '@/features/prescriptions';

/**
 * Clinical → the patient → Prescribe (CP81).
 *
 * A patient sub-route rather than a top-level screen, because a prescription is written for
 * somebody: the physician arrives here from the dashboard with a patient already in mind, and a
 * `/prescriptions` entry in the sidebar would ask him to pick one again.
 */
export default async function Page({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <Prescribe id={id} />;
}

function Prescribe({ id }: { id: string }) {
  const t = useTranslations('prescriptions');
  return (
    <div className="app-stack">
      <PageHeader title={t('editor.pageTitle')} description={t('editor.lede')} />
      <PrescribeScreen patientId={id} />
    </div>
  );
}
