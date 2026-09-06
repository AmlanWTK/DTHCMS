'use client';

import { useSearchParams } from 'next/navigation';
import { useTranslations } from 'next-intl';
import { Suspense, use } from 'react';

import { PageHeader } from '@/components/PageHeader';
import { ValueHistory } from '@/features/corrections';

/**
 * What this value said before, and who said it (CP62, §4.3, criterion 5).
 *
 * Its own screen rather than a panel inside another, because it answers a question a physician
 * asks *about a number they are looking at*: what did this say before, who entered it, who
 * disagreed, and what happened. That question arrives from several places — a critical value on
 * the alert board, a correction request in somebody's queue, a chart that moved — and a screen
 * those can all link to is one URL rather than three panels that drift apart.
 *
 * `?code=` is how they arrive on the right measurement. A consultant following a link from a
 * saturation alert should land on the saturation and not on whatever happens to sort first;
 * a code the patient has no value for falls back to the first one they do have, rather than
 * rendering an empty chain that reads as a failure.
 */
export default function Page({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const t = useTranslations('corrections');

  return (
    <div className="app-stack">
      <PageHeader title={t('history.pageTitle')} description={t('history.lede')} />
      {/* `useSearchParams` opts its subtree out of static rendering, so the boundary is here
          rather than around the whole page: the heading is the same for every reader and
          should not wait on the browser to say which measurement they asked for. */}
      <Suspense fallback={null}>
        <OpenedOnTheLinkedCode patientId={id} />
      </Suspense>
    </div>
  );
}

function OpenedOnTheLinkedCode({ patientId }: { patientId: string }) {
  const query = useSearchParams();
  const code = query.get('code');
  return <ValueHistory patientId={patientId} initialCode={code ?? undefined} />;
}
