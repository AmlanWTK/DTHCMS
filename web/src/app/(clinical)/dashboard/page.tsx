'use client';

import { useSearchParams } from 'next/navigation';
import { useTranslations } from 'next-intl';

import { PatientPicker, PhysicianDashboard } from '@/features/dashboard';
import { usePermission } from '@/lib/use-permission';

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
 *
 * # Why the lede is chosen and not written once
 *
 * It used to say, to everybody, "the snapshot, the clinical summary and the AI assistant on
 * one screen". Three of the six roles that reach this page hold `patient.read.clinical` and
 * not `ai.synthesis.read` — the nutritionist, the exercise specialist and the history officer
 * — so for half the readers the sentence named two things they would never see and then the
 * screen withheld them. That is worse than saying nothing: the panels themselves say plainly
 * that something is withheld (`WithheldNote`), and a lede that promised those panels a moment
 * earlier turns an honest refusal into an apparent fault.
 *
 * So the sentence is chosen by what this reader will actually be handed. The three variants
 * are the three shapes this screen takes, and they are chosen by the same two client actions
 * the panels themselves are gated on rather than by role, because a grant moves and a role
 * list here would not move with it.
 */
export default function DashboardPage() {
  const t = useTranslations('dashboard');
  const params = useSearchParams();
  const mayReadClinical = usePermission('dashboard.view');
  const mayReadSummary = usePermission('summary.view');

  const patientId = params.get('patient');
  const visitId = params.get('visit');

  if (!patientId) {
    const lede = !mayReadClinical
      ? 'descriptionStationOnly'
      : mayReadSummary
        ? 'description'
        : 'descriptionRecordOnly';
    return (
      <div className="app-stack">
        <h1 className="app-page__title">{t('title')}</h1>
        <p className="app-page__description">{t(lede)}</p>
        <PatientPicker />
      </div>
    );
  }

  return <PhysicianDashboard patientId={patientId} visitId={visitId ?? undefined} />;
}
