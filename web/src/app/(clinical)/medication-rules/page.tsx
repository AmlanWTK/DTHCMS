import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { RuleLibrary } from '@/features/medication-rules';

/**
 * Clinical → Medication safety rules (CP77, §6.3, D-22).
 *
 * In the clinical area rather than the administration one, and for a stronger reason than the
 * counselling templates have: D-22 makes the **physician** the author of every rule here, and the
 * permission to write or publish one is granted to his role alone. Filed under administration it
 * would be behind a group heading its only audience cannot see.
 */
export default function MedicationRulesPage() {
  const t = useTranslations('medicationRules');

  return (
    <div className="app-stack">
      <PageHeader title={t('pageTitle')} description={t('lede')} />
      <RuleLibrary />
    </div>
  );
}
