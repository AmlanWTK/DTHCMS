import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { AllergyPanel } from '@/features/allergies';

/**
 * The hard stop's own screen (CP54, §3 step 4).
 *
 * Its own route as well as a strip in the header, because the two answer different
 * questions. The strip answers "what must I not miss about this patient" on whatever screen
 * somebody happens to be on. This answers "what is on file, who said it, and what will
 * clear the gate" — and it is the screen a history officer opens deliberately, in the five
 * seconds the checkpoint is meant to take.
 */
export default async function Page({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <AllergyScreen id={id} />;
}

function AllergyScreen({ id }: { id: string }) {
  const t = useTranslations('allergies');
  return (
    <div className="app-stack">
      <PageHeader title={t('pageTitle')} description={t('lede')} />
      <AllergyPanel patientId={id} />
    </div>
  );
}
