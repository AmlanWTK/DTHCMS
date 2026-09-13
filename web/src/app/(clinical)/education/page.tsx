'use client';

import { useSearchParams } from 'next/navigation';
import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { EducationStation } from '@/features/education';

/**
 * Station 11 (CP88, CP92).
 *
 * # Why the patient and the visit are query parameters
 *
 * The same argument CP73's dashboard makes, and it applies more strongly here. The officer
 * reaches this screen from the traffic board with a patient in hand, and reaches it *without*
 * one at the start of a clinic — a path segment would make that landing a 404. What it shows
 * with no patient is the sentence saying what the station is for and where the list of people
 * waiting is.
 *
 * The **visit** is a parameter too rather than inferred from "the patient's open visit", and
 * that is not symmetry. Everything this station records is anchored to one journey: the score
 * is compared with the last visit, the checklists come from this visit's prescription, and the
 * assessment is filed against a visit id the server checks. A screen that guessed which visit it
 * was on would guess wrongly for the patient who came back the same afternoon.
 */
export default function Page() {
  const t = useTranslations('page.education');
  const params = useSearchParams();

  const patientId = params.get('patient');
  const visitId = params.get('visit');

  if (!patientId || !visitId) {
    return (
      <div className="app-stack">
        <PageHeader title={t('title')} description={t('description')} />
      </div>
    );
  }

  return (
    <div className="app-stack">
      <PageHeader title={t('title')} description={t('description')} />
      <EducationStation patientId={patientId} visitId={visitId} />
    </div>
  );
}
