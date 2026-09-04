import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { DuplicateReview } from '@/features/patients';

/**
 * Deciding whether a record is somebody the clinic already knows (CP30).
 *
 * Never merges on its own: a wrong merge is worse than a duplicate, because a duplicate is
 * two incomplete histories and a wrong merge is one history containing another person's
 * clinical facts.
 */
export default async function Page({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <DuplicatesScreen id={id} />;
}

function DuplicatesScreen({ id }: { id: string }) {
  const t = useTranslations('patients.review');
  return (
    <div className="app-stack">
      <PageHeader title={t('title')} description={t('description')} />
      <DuplicateReview patientId={id} />
    </div>
  );
}
