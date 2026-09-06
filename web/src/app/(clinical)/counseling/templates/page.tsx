import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { TemplateList } from '@/features/counseling';

/**
 * Clinical → Counselling checklists (CP55, §5.1, [R-07]).
 *
 * In the clinical area rather than the administration one: acceptance criterion 1 names a
 * physician authoring and publishing a template, and every role that may read one holds
 * clinical permissions and none of administration's. See the note in `lib/navigation.ts`.
 */
export default function CounselingTemplatesPage() {
  const t = useTranslations('counseling');

  return (
    <div className="app-stack">
      <PageHeader title={t('pageTitle')} description={t('lede')} />
      <TemplateList />
    </div>
  );
}
