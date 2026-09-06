import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { CounselingPanel } from '@/features/counseling';

/**
 * §5.5's checkpoint and §5.4's spot-check, on the patient the physician has open (CP57).
 *
 * # Why it is a screen under the patient rather than a card on the dashboard
 *
 * It is mounted exactly where `medical-history` and `allergies` are, and for the same reason:
 * this is a question about *one patient*, asked deliberately, by somebody who has that
 * patient's record open. The patient layout gives it the header — and therefore the allergy
 * strip — for free.
 *
 * # Why it is not a strip on the header instead
 *
 * CP54's allergy status is on the header because it is dangerous to be unaware of on any
 * screen, whatever you came to do. This is not that. The counselling gate is read at one
 * moment by one person, and what it has to say — seven items, who covered which, how long
 * each took, what is outstanding and in which room — is a screenful. Compressed into a strip
 * it would become a colour and a count, which is the shape that answers "is it done" and not
 * the shape that answers "what were you told about injection sites".
 *
 * The panel finds the visit itself; see `CounselingPanel`. No patient screen carries one
 * today.
 */
export default async function Page({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <CounselingScreen id={id} />;
}

function CounselingScreen({ id }: { id: string }) {
  const t = useTranslations('counseling');
  return (
    <div className="app-stack">
      <PageHeader title={t('panel.pageTitle')} description={t('panel.lede')} />
      <CounselingPanel patientId={id} />
    </div>
  );
}
