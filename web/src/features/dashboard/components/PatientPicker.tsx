'use client';

import { useQuery } from '@tanstack/react-query';
import Link from 'next/link';
import { useLocale, useTranslations } from 'next-intl';

import { EmptyState, ErrorState, Skeleton } from '@dthcms/ui';

import type { Locale } from '@/lib/i18n/config';

import { listTodaysPatients, todaysPatientsKey } from '../api/dashboard';

/**
 * What the dashboard shows before a patient has been chosen (CP73).
 *
 * # Why this is not the traffic board
 *
 * The obvious list — *who is in the building right now* — is CP40's board, and it deliberately
 * carries **no patient id and no name**: it is a wall display, and a queue list with names on
 * it read out across a waiting room is the privacy failure that checkpoint was designed to
 * avoid. It cannot be a picker.
 *
 * So this is `GET /v1/patients/today`, which is *registered today*, and it is labelled as
 * exactly that rather than as "today's clinic". The difference matters in a follow-up-heavy
 * practice: a patient registered two years ago and seen this morning is not in this list. The
 * register is one link away and is where they are found.
 *
 * **This is a gap and it is worth naming rather than papering over.** The list a physician
 * actually wants is "patients with an open visit, in queue order" — the board's data with the
 * consultant's permissions applied — and no endpoint answers it. Adding one is a query and a
 * route; it was not added here because a picker is not what CP73 is for, and because guessing
 * the ordering a consultant wants would be answering a clinical question nobody asked. It is
 * recorded in `docs/progress.md`.
 *
 * # Why the dashboard is reached with a patient in the URL
 *
 * `?patient=<id>` rather than a route parameter, so that the screen can exist without one and
 * say something useful. A physician's real path here is from the register or from an alert,
 * both of which link with the patient already chosen.
 */

export function PatientPicker() {
  const t = useTranslations('dashboard.picker');
  const locale = useLocale() as Locale;

  const today = useQuery({
    queryKey: todaysPatientsKey(),
    queryFn: listTodaysPatients,
  });

  if (today.isPending) return <Skeleton height="12rem" />;

  if (today.isError) {
    return (
      <ErrorState title={t('failed')}>
        <p>{t('failedBody')}</p>
        <Link className="app-link" href="/patients">
          {t('openRegister')}
        </Link>
      </ErrorState>
    );
  }

  const patients = today.data ?? [];

  return (
    <section className="dash-picker" data-testid="patient-picker">
      <h2 className="dash-picker__title">{t('title')}</h2>
      {/* The label says what the list is, not what a reader might hope it is. "Registered
          today" and "in the clinic today" are different sets, and in a follow-up practice the
          second is much the larger. */}
      <p className="dash-picker__lede">{t('lede')}</p>

      {patients.length === 0 ? (
        <EmptyState icon="user" title={t('none')}>
          {t('noneBody')}
        </EmptyState>
      ) : (
        <ul className="dash-picker__list">
          {patients.map((patient) => (
            <li key={patient.patient_id}>
              <Link
                className="dash-picker__link"
                href={`/dashboard?patient=${patient.patient_id}`}
                data-testid={`pick-${patient.patient_id}`}
              >
                <strong>
                  {locale === 'bn' && patient.name_bn ? patient.name_bn : patient.name_en}
                </strong>
                <span className="dash-picker__id">{patient.clinical_id}</span>
              </Link>
            </li>
          ))}
        </ul>
      )}

      <Link className="app-link" href="/patients">
        {t('openRegister')}
      </Link>
    </section>
  );
}
