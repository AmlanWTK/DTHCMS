import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { PatientCorrection } from '@/features/patients';

/**
 * Correcting a record, and reading what has been corrected before (CP35).
 *
 * The form and the history are one screen rather than two, deliberately. The most common
 * reason a field looks wrong is that somebody already changed it and the operator is about
 * to change it back; putting the history where they can see it before typing is cheaper
 * than a second correction and a conversation.
 */
export default async function Page({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <CorrectionScreen id={id} />;
}

function CorrectionScreen({ id }: { id: string }) {
  const t = useTranslations('patients.correct');
  return (
    <div className="app-stack">
      <PageHeader title={t('pageTitle')} description={t('pageDescription')} />
      <PatientCorrection patientId={id} />
    </div>
  );
}
