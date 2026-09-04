'use client';

import { useTranslations } from 'next-intl';
import { useSearchParams } from 'next/navigation';
import { Suspense } from 'react';

import { Skeleton } from '@dthcms/ui';

import { TrafficBoard } from '@/features/board';
import { PageHeader } from '@/components/PageHeader';

/**
 * The Clinic Traffic Control board (CP40).
 *
 * One route, two audiences. `?display=wall` is the screen in the waiting area — big type,
 * no controls, nothing to click by accident when somebody leans on the wall. Without it the
 * page is the floor supervisor's view: the same board, with the reroute controls.
 *
 * The wall variant drops the page header too. A screen that will hang for three years does
 * not need a breadcrumb; it needs every pixel spent on the thing people are reading from
 * five metres away.
 */
function Board() {
  const t = useTranslations('page.board');
  const params = useSearchParams();
  const wall = params.get('display') === 'wall';
  const day = params.get('day') ?? undefined;

  if (wall) {
    return <TrafficBoard density="wall" day={day} />;
  }
  return (
    <>
      <PageHeader title={t('title')} description={t('description')} />
      <TrafficBoard density="compact" day={day} />
    </>
  );
}

export default function Page() {
  return (
    <Suspense fallback={<Skeleton height="12rem" />}>
      <Board />
    </Suspense>
  );
}
