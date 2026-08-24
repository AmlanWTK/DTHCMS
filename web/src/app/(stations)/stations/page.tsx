import { useTranslations } from 'next-intl';

import { EmptyState } from '@dthcms/ui';

import { PageHeader } from '@/components/PageHeader';

/*
 * The desktop fallback for station work.
 *
 * §14.9 of the implementation plan lists this route group, and P-1 says web is not for
 * floor capture — which leaves it with no scheduled checkpoint. Rather than invent one,
 * the screen says so. See the CP10 review notes.
 */
export default function Page() {
  const t = useTranslations();

  return (
    <>
      <PageHeader title={t('page.stations.title')} description={t('page.stations.description')} />
      <EmptyState icon="clock" title={t('placeholder.title', { area: t('nav.stations') })}>
        {t('placeholder.unscheduled')}
      </EmptyState>
    </>
  );
}
