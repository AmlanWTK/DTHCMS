import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { MedicineLookup } from '@/features/formulary';

/**
 * Clinical → Find a medicine (CP76, §10.1).
 *
 * In the clinical area rather than beside the pharmacist's formulary console, because the two
 * screens answer different people's questions. The console is a price review; this is a physician
 * mid-clinic asking "what is that brand's generic, and what does it cost the patient".
 *
 * It is also where CP81's prescription editor gets its item entry from: `MedicineCombobox` is the
 * component, and this page is the place it can be used — and looked at — before the editor exists.
 */
export default function MedicineLookupPage() {
  const t = useTranslations('formulary');

  return (
    <div className="app-stack">
      <PageHeader title={t('search.title')} description={t('search.lede')} />
      <MedicineLookup />
    </div>
  );
}
