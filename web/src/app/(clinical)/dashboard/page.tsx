'use client';

import { useSearchParams } from 'next/navigation';
import { useTranslations } from 'next-intl';

import { PatientPicker, PhysicianDashboard } from '@/features/dashboard';

/**
 * The physician's dashboard (CP73, §8).
 *
 * # Why the patient is a query parameter and not a path segment
 *
 * `/dashboard?patient=<id>` rather than `/dashboard/<id>`, and it is not laziness. This route
 * has to exist *without* a patient: it is the first item in the clinical group's sidebar, it
 * is where a physician lands after signing in, and a path segment would make that landing a
 * 404 or a redirect. What it shows instead is the picker, which says plainly what list it is
 * offering and links to the register for everybody else.
 *
 * # Why this file is a client component and holds almost nothing
 *
 * `useSearchParams` needs one, and the whole screen is interactive — a socket subscription,
 * keyboard shortcuts, a remembered layout. The page's only job is to read the two parameters
 * and choose between the picker and the dashboard; everything else is in the feature, where
 * it can be tested without a router.
 */
export default function DashboardPage() {
  const t = useTranslations('dashboard');
  const params = useSearchParams();

  const patientId = params.get('patient');
  const visitId = params.get('visit');

  if (!patientId) {
    return (
      <div className="app-stack">
        <h1 className="app-page__title">{t('title')}</h1>
        <p className="app-page__description">{t('description')}</p>
        <PatientPicker />
      </div>
    );
  }

  return <PhysicianDashboard patientId={patientId} visitId={visitId ?? undefined} />;
}
