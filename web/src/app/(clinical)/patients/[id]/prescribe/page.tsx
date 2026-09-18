import { useTranslations } from 'next-intl';
import { Suspense } from 'react';

import { PageHeader } from '@/components/PageHeader';
import { PrescribeScreen } from '@/features/prescriptions';

/**
 * Clinical → the patient → Prescribe (CP81).
 *
 * A patient sub-route rather than a top-level screen, because a prescription is written for
 * somebody: the physician arrives here from the dashboard with a patient already in mind, and a
 * `/prescriptions` entry in the sidebar would ask him to pick one again.
 *
 * The editor sits inside a `Suspense` boundary because it reads `?prescription=` (CP84), and
 * `useSearchParams` in a prerendered tree client-renders everything up to the nearest boundary.
 * Without one that is the whole page, including the patient header — which is the band that
 * carries the allergy warning, and the last thing that should wait for JavaScript.
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
      <Suspense fallback={<p className="app-prescribe__note">{t('editor.loading')}</p>}>
        <PrescribeScreen patientId={id} />
      </Suspense>
    </div>
  );
}
