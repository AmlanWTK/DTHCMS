import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { AlertBoard } from '@/features/alerts';

/**
 * Critical values nobody has answered yet (CP50, §4.4).
 *
 * Its own route rather than a panel on the dashboard, for the reason the escalation chain
 * exists at all: this is the screen a consultant is told to open, by name, when a value is
 * raised — and a screen somebody has to find inside another screen is a screen that gets
 * found a minute late.
 *
 * A thin shell, like every other route file here. Nothing on this page decides anything; the
 * board owns the reading, the ordering and the acknowledgement.
 */
export default function Page() {
  const t = useTranslations('page.alerts');

  return (
    <>
      <PageHeader title={t('title')} description={t('description')} />
      <AlertBoard />
    </>
  );
}
