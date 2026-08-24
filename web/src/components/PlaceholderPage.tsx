import { useTranslations } from 'next-intl';

import { EmptyState } from '@dthcms/ui';

import { PageHeader } from '@/components/PageHeader';

/**
 * A screen that does not exist yet, said out loud.
 *
 * Every route group needs something to render so the shell can be navigated and
 * reviewed. The temptation is a page reading "Coming soon", which tells a reviewer
 * nothing about whether the checkpoint is late. Naming the checkpoint that fills it makes
 * the placeholder a status report.
 */
export function PlaceholderPage({
  titleKey,
  descriptionKey,
  areaKey,
  checkpoint,
}: {
  titleKey: string;
  descriptionKey: string;
  areaKey: string;
  /** The checkpoint this screen arrives at, e.g. "CP32". */
  checkpoint: string;
}) {
  const t = useTranslations();

  return (
    <>
      <PageHeader title={t(titleKey)} description={t(descriptionKey)} />
      <EmptyState icon="clock" title={t('placeholder.title', { area: t(areaKey) })}>
        {t('placeholder.body', { checkpoint })}
      </EmptyState>
    </>
  );
}
