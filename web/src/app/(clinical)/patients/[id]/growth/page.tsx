'use client';

import { use } from 'react';
import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { GrowthScreen } from '@/features/growth';

/**
 * This child's growth (CP48, [R-06]).
 *
 * Its own screen as well as a card, because the card answers "where is this child now" and
 * the screen answers "where have they been" — and the second is the question a parent asks,
 * standing beside the desk, looking at a printed chart.
 */
export default function Page({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const t = useTranslations('growth');
  return (
    <div className="app-stack">
      <PageHeader title={t('pageTitle')} description={t('lede')} />
      <GrowthScreen patientId={id} />
    </div>
  );
}
