'use client';

import { use } from 'react';
import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { PatientTimeline } from '@/features/timeline';

/**
 * This patient's whole record on one time axis (CP74, §8).
 *
 * # Why it is a screen of its own and not a fourth dashboard panel
 *
 * CP73's dashboard answers *what is true about this patient now*, in three columns, in
 * sixty seconds. This answers *what has happened to them*, over a decade, and needs the
 * full width of the window to do it — a decade in a 20rem column is a decade nobody can
 * read. Folding it into the dashboard would also make one request cost what two screens
 * cost, which is the criterion CP73 is judged on.
 *
 * `docs/dashboard.md` says so in as many words: the timeline is CP74, and it is not there.
 */
export default function Page({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const t = useTranslations('timeline');
  return (
    <div className="app-stack">
      <PageHeader title={t('pageTitle')} description={t('lede')} />
      <PatientTimeline patientId={id} />
    </div>
  );
}
