'use client';

import { useQuery } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';

import { AlertBanner, Card, EmptyState, Skeleton } from '@dthcms/ui';

import { formatDateTime } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';
import { isKnownRole } from '@/lib/permissions';

import { contentApproved } from '../api/counseling';
import {
  counselingGateKey,
  counselingSessionKey,
  counselingVisitSessionsKey,
  gateState,
  getCounselingGate,
  getCounselingSession,
  isFinished,
  listCounselingSessionsForVisit,
  type CounselingSession,
  type GateState,
} from '../api/gate';

import { SessionItems } from './SessionItems';
import { sessionTitle, staffLabel } from './panelText';

/**
 * Every checklist walked on one visit, with its items opened (CP57 criterion 4).
 *
 * # Why all of them are open, and none of them is a tab
 *
 * A patient with type 2 diabetes and hypothyroidism gets one session per matching assignment
 * rule. The physician has about a minute with the record open and a patient in the chair, and
 * §5.4's question — *what did they teach you about injection sites* — does not come with the
 * name of the checklist that item is on. A tab strip makes the physician guess which one to
 * open before they can look, which is a guess about the thing they came to find out.
 *
 * # Why each card reads its own session
 *
 * The index endpoint deliberately carries no items and no ticks — it is an index. So the
 * header of each card is drawn from the index row, which has the title, the version and what
 * is outstanding, and the items arrive underneath when the detail does.
 *
 * That is now a choice about latency and failure rather than a workaround: the detail response
 * carries the checklist's code and titles too, so a card built only from it *could* name
 * itself — but it would show nothing at all until the second read landed, and nothing at all
 * if that read failed. What only the detail has is `approved_at` and the staff names, which
 * are joins the index does not make, so those two facts appear a moment later.
 *
 * A visit has one or two sessions. Two small reads, and a physician who never sees a spinner
 * where a checklist name should be.
 *
 * # Why this reads the gate as well
 *
 * Not to draw it — `GateState` does that — but because an uncovered item's *consequence*
 * depends on it: "the checkpoint will hold this patient" stops being true the moment somebody
 * sends them past, and a banner saying they were let through above rows saying they are held
 * is a screen a physician stops believing. It is read here rather than in each card or in
 * `SessionItems` so that the whole list asks once, on the key `GateState` already uses — so
 * TanStack serves both from one request rather than two.
 */

export interface SessionListProps {
  visitId: string;
}

export function SessionList({ visitId }: SessionListProps) {
  const t = useTranslations('counseling');

  const sessions = useQuery({
    queryKey: counselingVisitSessionsKey(visitId),
    queryFn: () => listCounselingSessionsForVisit(visitId),
  });

  // The same key `GateState` reads, so this costs no second request. Undefined until it
  // arrives, and undefined if it never does — the rows then claim no consequence rather than
  // asserting one the checkpoint is not carrying out.
  const gate = useQuery({
    queryKey: counselingGateKey(visitId),
    queryFn: () => getCounselingGate(visitId),
  });

  const state = gate.data === undefined ? undefined : gateState(gate.data);

  if (sessions.isPending) return <Skeleton height="12rem" />;

  if (sessions.isError || !sessions.data) {
    // Loud, not quiet. "No checklist was walked on this visit" and "the checklists could not
    // be read" look identical as an empty list and mean opposite things — and one of them
    // tells a physician that nobody counselled this patient.
    return (
      <AlertBanner tone="critical" title={t('panel.sessions.unavailable')}>
        {t('panel.sessions.unavailableBody')}
      </AlertBanner>
    );
  }

  return (
    <section
      className="app-counseling-panel__sessions"
      aria-label={t('panel.sessions.title')}
      data-testid="counseling-sessions"
    >
      <h2 className="app-counseling-panel__heading">{t('panel.sessions.title')}</h2>

      {sessions.data.length === 0 ? (
        <EmptyState icon="inbox" title={t('panel.sessions.empty.title')}>
          {t('panel.sessions.empty.body')}
        </EmptyState>
      ) : (
        <ol className="app-counseling-panel__session-list">
          {sessions.data.map((session) => (
            <li key={session.id}>
              <SessionCard summary={session} gate={state} />
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}

/**
 * One checklist: what it is, who opened it, whether it was finished, and every item.
 *
 * The header states the session's own facts as facts about the session. Who *opened* it is
 * not who covered anything — the whole point of the rows below is that those are different
 * people — so it is worded as opening rather than as counselling.
 */
function SessionCard({
  summary,
  gate,
}: {
  summary: CounselingSession;
  gate: GateState | undefined;
}) {
  const t = useTranslations('counseling');
  const tRole = useTranslations('role');
  const locale = useLocale() as Locale;

  const detail = useQuery({
    queryKey: counselingSessionKey(summary.id),
    queryFn: () => getCounselingSession(summary.id),
  });

  /*
   * The index row until the detail arrives, then the detail.
   *
   * They are not the same shape, and the difference decides what this header can say. The
   * index carries the checklist's code and titles and what is outstanding; **only the single
   * session read carries `approved_at` and the staff names**, because those come from joins
   * the index does not make. So the card draws immediately from the index — a physician never
   * sees a spinner where a checklist name should be, and the name survives a failed detail
   * read — and fills in the person and the approval state a moment later.
   */
  const row = detail.data ?? summary;

  // Who opened the checklist, which is not who covered anything on it. Named as opening.
  const openedBy = staffLabel(
    {
      code: row.started_by_code,
      name_en: row.started_by_name_en,
      name_bn: row.started_by_name_bn,
      role: row.started_role,
      id: row.started_by,
    },
    locale,
  );
  const openedRole =
    openedBy.role === null
      ? undefined
      : isKnownRole(openedBy.role)
        ? tRole(openedBy.role)
        : openedBy.role;

  const closedBy = staffLabel(
    {
      code: row.completed_by_code,
      name_en: row.completed_by_name_en,
      name_bn: row.completed_by_name_bn,
    },
    locale,
  );

  const outstanding = summary.outstanding.length;

  return (
    <Card elevation="flat" className="app-counseling-panel__session">
      <article data-testid={`session-${summary.id}`} data-finished={isFinished(summary)}>
        <header className="app-counseling-panel__session-head">
          <h3 className="app-counseling-panel__session-title">{sessionTitle(summary, locale)}</h3>
          <p className="app-counseling-panel__session-version">
            {/* The version is on the session rather than looked up, which is where CP55's
                criterion 2 lives: a checklist republished mid-session did not change what
                this patient was asked about. A physician comparing two patients' records
                needs to see that they were asked different questions. */}
            {t('version.heading', { version: summary.template_version })}
          </p>
        </header>

        {detail.data && !contentApproved(detail.data) && (
          // D-53 is open, and this is where it reaches a clinician. The seeded diabetes
          // version is live, was published by a migration, and its seven items are
          // transcribed from §5.1 with an engineer's Bangla and guidance. A physician about
          // to ask a patient what they were taught is entitled to know that the list they
          // were taught from is a proposal — and the alternative, saying nothing, would
          // present it as a signed-off clinical document because it is on a clinical screen.
          //
          // Read from the session itself, which now carries `approved_at` for exactly this
          // purpose. Nothing is drawn until the detail arrives, and nothing is drawn if it
          // never does: this panel never claims a checklist *is* approved, so an absent
          // notice asserts nothing either way.
          <AlertBanner
            tone="borderline"
            title={t('approval.notApproved')}
            className="app-counseling-panel__unapproved"
          >
            {t('approval.notApprovedBody')}
          </AlertBanner>
        )}

        <dl className="app-counseling-panel__session-facts">
          <dt>{t('panel.sessions.started')}</dt>
          <dd data-testid={`session-started-${summary.id}`}>
            {/* The person where the detail has arrived and the record exists; the role alone
                until then, which is what the index can say. Never a blank. */}
            {openedBy.name !== null
              ? t('panel.sessions.startedByPerson', {
                  when: formatDateTime(Date.parse(summary.started_at), locale),
                  name: openedBy.name,
                  role: openedRole ?? t('panel.sessions.unknownRole'),
                })
              : t('panel.sessions.startedValue', {
                  when: formatDateTime(Date.parse(summary.started_at), locale),
                  role: openedRole ?? t('panel.sessions.unknownRole'),
                })}
          </dd>

          <dt>{t('panel.sessions.finishedLabel')}</dt>
          <dd data-testid={`session-finished-${summary.id}`}>
            {/* A session is closed by a counsellor saying so, never by arithmetic — and it
                may be closed with items outstanding, because the patient left or the
                interpreter did not arrive. "Still open" and "finished" are both real states
                and neither implies anything about what was covered. */}
            {summary.completed_at ? (
              <>
                {t('panel.sessions.finishedAt', {
                  when: formatDateTime(Date.parse(summary.completed_at), locale),
                })}{' '}
                {/* Who closed it, where the record says. A session may be closed with items
                    outstanding, and which counsellor decided that is a fact about the
                    decision rather than about the checklist. */}
                {closedBy.name !== null && (
                  <span data-testid={`session-closed-by-${summary.id}`}>
                    {t('panel.sessions.closedBy', { name: closedBy.name })}
                  </span>
                )}
              </>
            ) : (
              t('panel.sessions.stillOpen')
            )}
          </dd>

          <dt>{t('panel.sessions.outstandingLabel')}</dt>
          <dd data-testid={`session-outstanding-${summary.id}`}>
            {/* From the session's own `outstanding`, which the contract guarantees is always
                present — so an empty list means "nothing is missing" rather than "this row
                was not asked". It comes from the same database function the gate reads. */}
            {outstanding === 0
              ? t('panel.sessions.allCovered')
              : t('panel.sessions.outstandingCount', { count: outstanding })}
          </dd>
        </dl>

        {detail.isPending && <Skeleton height="8rem" />}

        {detail.isError && (
          <AlertBanner tone="critical" title={t('panel.sessions.detailUnavailable')}>
            {t('panel.sessions.detailUnavailableBody')}
          </AlertBanner>
        )}

        {detail.data && <SessionItems session={detail.data} gate={gate} />}
      </article>
    </Card>
  );
}
