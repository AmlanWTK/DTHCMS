'use client';

import { useSearchParams } from 'next/navigation';
import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { QAQueue, QAReviewScreen } from '@/features/qa';

/**
 * Station 10 (CP83).
 *
 * # Why the prescription is a query parameter
 *
 * The officer reaches this screen from the queue with a sheet in hand, and reaches it *without*
 * one at the start of a clinic — a path segment would make that landing a 404. With no
 * prescription named it draws the queue, which is what the station actually looks like for most
 * of the morning.
 */
export default function Page() {
  const t = useTranslations('page.qa');
  const params = useSearchParams();
  const prescriptionId = params.get('prescription');

  return (
    <div className="app-stack">
      <PageHeader title={t('title')} description={t('description')} />
      {prescriptionId ? <QAReviewScreen prescriptionId={prescriptionId} /> : <QAQueue />}
    </div>
  );
}
