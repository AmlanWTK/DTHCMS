'use client';

import { useQuery } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';

import { AlertBanner, EmptyState, Skeleton } from '@dthcms/ui';

import {
  latestVisit,
  listPatientVisits,
  openVisit,
  patientVisitsKey,
  type Visit,
} from '@/features/patients';
import { formatDateTime } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';
import { usePermission } from '@/lib/use-permission';

import { GateState } from './GateState';
import { SessionList } from './SessionList';

/**
 * The physician's counselling panel (CP57 criterion 4, §5.4, §5.5).
 *
 * One screen answering three questions a physician has about the patient in front of them:
 * is counselling holding this patient, what has actually been covered and by whom, and what
 * is still outstanding. It reads; it writes exactly one thing, which is the override, and
 * that lives behind its own permission in `GateState`.
 *
 * # Where the visit comes from, and why this component has to find it
 *
 * Counselling is recorded against a **visit**, and until this checkpoint no patient screen
 * had one. `visitId` has been an optional prop on the history and allergy panels since CP53
 * and is supplied by nothing: those two surfaces write against the *patient* and treat the
 * visit as an attribution detail they can do without. A gate cannot — `blocked` is a fact
 * about one visit, and there is no such thing as a patient-level answer to it.
 *
 * So the panel resolves it, from `GET /v1/patients/{id}/visits`, and takes the **open**
 * visit. Not the newest: a patient who came in March and again today has two, and the March
 * one is closed. If none is open the panel falls back to the most recent visit and *says
 * so*, because a physician reviewing yesterday's patient is a real reader and an empty screen
 * would tell them nobody counselled anybody. The visit is named on the panel either way —
 * `visit_code` is what is spoken at a desk — so there is never a question about which visit
 * the checkpoint below refers to.
 *
 * The override is hidden on a closed visit. See `GateState`.
 *
 * # Why this is a pull and not a subscription
 *
 * CP56 publishes a message on the patient's topic for every tick, under this panel's own
 * permission, and this checkpoint deliberately does not wire it up: the socket is a nicety
 * and the pull is the truth. A physician opening the panel reads the current state; a stale
 * panel left open on a desk is a smaller problem than a panel whose correctness depends on a
 * websocket having stayed connected across a clinic's morning.
 */

export interface CounselingPanelProps {
  patientId: string;
}

export function CounselingPanel({ patientId }: CounselingPanelProps) {
  const t = useTranslations('counseling');
  const locale = useLocale() as Locale;
  const mayRead = usePermission('counseling.sessions.view');

  const visits = useQuery({
    queryKey: patientVisitsKey(patientId),
    queryFn: () => listPatientVisits(patientId),
    enabled: mayRead,
  });

  if (!mayRead) {
    return <AlertBanner tone="unknown" title={t('panel.notPermitted')} />;
  }

  if (visits.isPending) return <Skeleton height="14rem" />;

  if (visits.isError || !visits.data) {
    return (
      <AlertBanner tone="critical" title={t('panel.visit.unavailable')}>
        {t('panel.visit.unavailableBody')}
      </AlertBanner>
    );
  }

  const open = openVisit(visits.data);
  const visit = open ?? latestVisit(visits.data);

  if (visit === undefined) {
    // A patient with no visit at all — registered and not yet seen. Not an error, and not a
    // patient with nothing outstanding: there is simply nothing for a checkpoint to be about.
    return (
      <EmptyState
        icon="inbox"
        title={t('panel.visit.none.title')}
        className="app-counseling-panel__no-visit"
      >
        {t('panel.visit.none.body')}
      </EmptyState>
    );
  }

  return (
    <section
      className="app-counseling-panel"
      aria-label={t('panel.pageTitle')}
      data-testid="counseling-panel"
      data-visit={visit.id}
      data-visit-open={open !== undefined}
    >
      <VisitLine visit={visit} open={open !== undefined} locale={locale} />
      <GateState visitId={visit.id} visitOpen={open !== undefined} />
      <SessionList visitId={visit.id} />
    </section>
  );
}

/**
 * Which visit this panel is about, said out loud.
 *
 * `visit_code` rather than the uuid: V-2026-0914-017 is what a desk says to a patient and
 * what a physician can quote to a counsellor. And when the visit is closed the panel says
 * that before anything else — otherwise a checkpoint reading "nothing outstanding" about a
 * visit that ended a month ago would be read as a statement about the patient in the chair.
 */
function VisitLine({ visit, open, locale }: { visit: Visit; open: boolean; locale: Locale }) {
  const t = useTranslations('counseling');

  return (
    <div className="app-counseling-panel__visit">
      <p className="app-counseling-panel__visit-code" data-testid="counseling-visit">
        {t('panel.visit.label', { code: visit.visit_code })}{' '}
        <span className="app-counseling-panel__visit-when">
          {t('panel.visit.opened', { when: formatDateTime(Date.parse(visit.opened_at), locale) })}
        </span>
      </p>

      {!open && (
        <AlertBanner tone="unknown" title={t('panel.visit.closedTitle')}>
          {t('panel.visit.closedBody')}
        </AlertBanner>
      )}
    </div>
  );
}
