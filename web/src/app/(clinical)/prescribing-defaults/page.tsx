import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { PrescribingDefaultsConsole } from '@/features/prescriptions';

/**
 * Clinical → Prescribing defaults (CP81).
 *
 * In the clinical group and not administration, for the same reason the medication rules are: the
 * permission to approve one is granted to the physician's role alone, so filed under
 * administration it would sit behind a heading its only audience cannot see.
 */
export default function PrescribingDefaultsPage() {
  const t = useTranslations('prescriptions');
  return (
    <div className="app-stack">
      <PageHeader title={t('defaults.pageTitle')} description={t('defaults.pageLede')} />
      <PrescribingDefaultsConsole />
    </div>
  );
}
