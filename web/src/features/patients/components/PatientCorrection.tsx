'use client';

import { useTranslations } from 'next-intl';
import { useEffect, useState } from 'react';

import { AlertBanner, Skeleton } from '@dthcms/ui';

import { getPatient, type Patient } from '../api/patients';
import { CorrectionForm } from './CorrectionForm';
import { CorrectionHistory } from './CorrectionHistory';

/**
 * The correction screen: the record, the form, and what has already been corrected (CP35).
 *
 * The history is remounted after a correction — `key` on the version counter — because a
 * correction the operator just made and cannot see in the history is a correction they will
 * make again.
 */
export function PatientCorrection({ patientId }: { patientId: string }) {
  const t = useTranslations('patients.correct');
  const [patient, setPatient] = useState<Patient | null>(null);
  const [failed, setFailed] = useState(false);
  const [version, setVersion] = useState(0);

  useEffect(() => {
    let live = true;
    getPatient(patientId)
      .then((result) => {
        if (live) setPatient(result);
      })
      .catch(() => {
        if (live) setFailed(true);
      });
    return () => {
      live = false;
    };
  }, [patientId]);

  if (failed) return <AlertBanner tone="critical" title={t('loadFailed')} />;
  if (!patient) return <Skeleton lines={8} />;

  return (
    <div className="app-stack">
      <CorrectionForm
        patient={patient}
        onCorrected={(applied) => {
          setPatient(applied.patient);
          setVersion((n) => n + 1);
        }}
      />
      <section aria-label={t('historyTitle')}>
        <h2 className="app-correct__historyTitle">{t('historyTitle')}</h2>
        <CorrectionHistory key={version} patientId={patientId} />
      </section>
    </div>
  );
}
