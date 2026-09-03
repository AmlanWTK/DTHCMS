import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { BreakGlassConsole } from '@/features/audit';

/**
 * Emergency access (CP22): the door a clinician opens with a typed justification, with
 * every administrator told at once.
 */
export default function BreakGlassPage() {
  const t = useTranslations('breakGlass');

  return (
    <div className="app-stack">
      <PageHeader title={t('title')} description={t('description')} />
      <BreakGlassConsole />
    </div>
  );
}
