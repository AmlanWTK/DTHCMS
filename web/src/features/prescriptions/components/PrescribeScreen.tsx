'use client';

import { useQuery } from '@tanstack/react-query';
import { useTranslations } from 'next-intl';

import { PrescriptionEditor, openVisitOf } from './PrescriptionEditor';

/**
 * The prescription editor, with the visit resolved (CP81).
 *
 * A prescription belongs to a visit (CP80), and the physician opening this screen has a patient in
 * front of him rather than a visit id in his hand. So the screen finds the patient's **open** visit
 * and says so when there is not one, rather than offering a blank editor whose first write would
 * fail with a validation error after four medicines had been typed.
 *
 * Deliberately not "the most recent visit". Prescribing against a visit that was closed yesterday
 * would put today's medicines on yesterday's encounter, and the record would be wrong in a way
 * nobody notices until somebody asks what happened at that appointment.
 */
export function PrescribeScreen({ patientId }: { patientId: string }) {
  const t = useTranslations('prescriptions');
  const visit = useQuery({
    queryKey: ['patients', patientId, 'open-visit'],
    queryFn: () => openVisitOf(patientId),
  });

  if (visit.isPending) return <p className="app-prescribe__note">{t('editor.loading')}</p>;

  return <PrescriptionEditor patientId={patientId} visitId={visit.data} />;
}
