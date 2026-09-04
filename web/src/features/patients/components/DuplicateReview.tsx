'use client';

import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useState } from 'react';

import { queryKeys } from '@dthcms/api-client';
import { AlertBanner, EmptyState, Skeleton } from '@dthcms/ui';

import {
  checkDuplicates,
  getPatient,
  type DuplicateCandidate,
  type Patient,
} from '../api/patients';
import { MergeReview } from './MergeReview';
import { reasonText } from './reasonText';

/**
 * The duplicate review screen (CP30): one patient, everybody who might be them, and the
 * side-by-side comparison that decides it.
 *
 * Reached from the registration desk's inline warning and from a patient's own record. It
 * re-runs the match rather than trusting a list that was computed when the warning was
 * shown, because a record may have been merged in between and offering a merged-away
 * record as a candidate is how a chain gets built by accident.
 */
export function DuplicateReview({ patientId }: { patientId: string }) {
  const t = useTranslations('patients.review');
  const locale = useLocale();
  const queryClient = useQueryClient();
  const [chosen, setChosen] = useState<DuplicateCandidate | null>(null);
  const [dismissed, setDismissed] = useState<string[]>([]);
  const [merged, setMerged] = useState<Patient | null>(null);

  const patient = useQuery({
    queryKey: queryKeys.patient(patientId),
    queryFn: () => getPatient(patientId),
  });

  const match = useQuery({
    queryKey: [...queryKeys.patient(patientId), 'duplicates'],
    enabled: patient.isSuccess,
    queryFn: () =>
      checkDuplicates({
        name_en: patient.data!.name_en,
        name_bn: patient.data!.name_bn,
        sex: patient.data!.sex,
        birth_date: patient.data!.birth.date,
        dob_precision: patient.data!.birth.precision,
        dob_source: patient.data!.birth.source,
        phone_primary: patient.data!.phone_primary,
        district: patient.data!.address.district,
        upazila: patient.data!.address.upazila,
        consent_reference: 'review',
      }),
  });

  const candidate = useQuery({
    queryKey: chosen ? queryKeys.patient(chosen.patient_id) : ['nobody'],
    enabled: chosen !== null,
    queryFn: () => getPatient(chosen!.patient_id),
  });

  if (patient.isPending || match.isPending) {
    return <Skeleton lines={6} />;
  }
  if (patient.isError) {
    return <AlertBanner tone="critical" title={t('loadFailed')} />;
  }

  if (merged) {
    return (
      <AlertBanner tone="normal" title={t('mergedTitle')}>
        {t('mergedBody', { clinicalId: merged.clinical_id })}
      </AlertBanner>
    );
  }

  // Candidates the officer has already said are different people stay off the screen for
  // the rest of the session. Re-showing a decision somebody has just made is how a warning
  // becomes noise.
  const candidates = (match.data?.candidates ?? []).filter(
    (item) => item.patient_id !== patientId && !dismissed.includes(item.patient_id),
  );

  if (candidates.length === 0) {
    return <EmptyState title={t('noneTitle')}>{t('noneBody')}</EmptyState>;
  }

  if (chosen && candidate.isSuccess) {
    return (
      <MergeReview
        left={patient.data}
        right={candidate.data}
        candidate={chosen}
        onMerged={(survivor) => {
          setMerged(survivor);
          void queryClient.invalidateQueries({ queryKey: queryKeys.patient(patientId) });
        }}
        onDifferent={() => {
          setDismissed((current) => [...current, chosen.patient_id]);
          setChosen(null);
        }}
      />
    );
  }

  return (
    <div className="app-stack">
      <p className="app-duplicates__hint">{t('lede', { clinicalId: patient.data.clinical_id })}</p>
      <ul className="app-duplicates__list">
        {candidates.map((item) => (
          <li key={item.patient_id}>
            <button
              type="button"
              className="app-duplicates__choose"
              onClick={() => setChosen(item)}
            >
              <span className="app-duplicates__name">{item.name_en}</span>
              <span className="app-duplicates__name-bn">{item.name_bn}</span>
              <span>{item.clinical_id}</span>
              <span>{item.birth_date}</span>
              <ul className="app-duplicates__reasons">
                {item.reasons.map((reason) => (
                  <li key={reason.code}>{reasonText(reason, locale)}</li>
                ))}
              </ul>
              <span className="app-duplicates__compare-cta">{t('compare')}</span>
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}
