'use client';

import { useQuery } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';

import { queryKeys } from '@dthcms/api-client';

import { AllergyBanner } from '@/features/allergies';

import { getPatient } from '../api/patients';

/**
 * The band every one of a patient's screens opens with (CP54, acceptance criterion 3).
 *
 * # Why this exists at all
 *
 * Until now each patient screen drew its own title and nothing was true of the patient
 * across all of them, so "allergies appear on the patient header on every screen" had no
 * header to appear on. This is that header: it is rendered by the layout at
 * `/patients/[id]`, which means every present and future screen under that path inherits it
 * without anybody remembering to add it. A banner mounted screen by screen is a banner that
 * is missing from the screen built next month, and the screen built next month is where
 * somebody writes a prescription.
 *
 * # What belongs here
 *
 * Facts that are true of the patient regardless of which screen was opened, and that are
 * dangerous to be unaware of. Today that is the allergy status. CP50's per-patient critical
 * value strip is the obvious next tenant and is deliberately **not** wired in here yet —
 * that is a decision for whoever owns that checkpoint, not a side effect of this one.
 *
 * # Why the identity is best-effort and the allergy strip is not
 *
 * A name is a convenience: it tells the reader whose record is on screen, and if it cannot
 * be read the screens below still say so themselves. The allergy strip is not a
 * convenience, so it has its own failure state and says out loud when it could not find
 * out — because "this patient has no allergies" and "this screen could not tell" are
 * different sentences and only one of them is safe to imply.
 */

export interface PatientHeaderProps {
  patientId: string;
}

export function PatientHeader({ patientId }: PatientHeaderProps) {
  const t = useTranslations('patients.header');
  const locale = useLocale();

  const patient = useQuery({
    // The shared patient key, so a screen that has already read this patient does not read
    // them again and a correction invalidates both at once.
    queryKey: queryKeys.patient(patientId),
    queryFn: () => getPatient(patientId),
  });

  const name =
    patient.data === undefined
      ? null
      : locale === 'bn' && patient.data.name_bn
        ? patient.data.name_bn
        : patient.data.name_en || patient.data.name_bn;

  return (
    <header className="app-patient-header" aria-label={t('label')} data-testid="patient-header">
      {name !== null && (
        <p className="app-patient-header__identity">
          <span className="app-patient-header__name">{name}</span>
          {/* A clinical id is an identifier: ASCII digits in both languages, because it is
              read back at a desk and copied onto paper. */}
          <span className="app-patient-header__id">{patient.data?.clinical_id}</span>
        </p>
      )}

      <AllergyBanner patientId={patientId} />
    </header>
  );
}
