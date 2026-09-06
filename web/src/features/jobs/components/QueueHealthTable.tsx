'use client';

import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useState } from 'react';

import { AlertBanner, Button, EmptyState, Skeleton } from '@dthcms/ui';

import { formatCount, formatDateTime } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';
import { usePermission } from '@/lib/use-permission';

import {
  JOBS_KEY,
  KINDS_KEY,
  REFRESH_MS,
  conflictOf,
  getQueueHealth,
  hasDeadLetters,
  hasDeadline,
  hasEverFinished,
  healthKey,
  missedDeadlines,
  listKinds,
  needsAttention,
  pauseKind,
  rateIsAvailable,
  resumeKind,
  rowsInOrder,
  severityOf,
  someWaitingIsDeliberate,
  type ConflictCode,
  type JobQueueHealth,
} from '../api/jobs';

import { QueueStateBadge } from './QueueStateBadge';
import { ageLabel, catalogueOf, kindDescription, pausedAttribution } from './jobsText';

/**
 * Is anything stuck, is anything failing, is the five-minute promise being kept (CP69, §7.1).
 *
 * # Who opens this and what they are holding
 *
 * An operator who suspects background work has stopped, or who has just been told by a
 * physician that the AI summary was not ready when the patient sat down. Both arrive with a
 * question that has to be answerable without reading, and both are standing up.
 *
 * # Every registered kind is a row, including the silent ones
 *
 * There is no filter on this table and there must never be one. `GET /v1/ops/jobs/health`
 * returns the **catalogue** joined to the queue, so a kind with nothing queued, nothing
 * running and nothing finished still has a row — and that row is the only way this screen can
 * report the failure it exists to catch, which is a job type that has stopped being enqueued
 * at all. A "hide the empty ones" control would be the most requested feature on this page
 * and the one that breaks it.
 *
 * # Age is the number, and it is drawn larger than depth
 *
 * `oldest_available_seconds` is set in the row's biggest type with the count beneath it in
 * small, because a queue of two that has not moved in an hour is a worse state than a queue of
 * four hundred that is draining, and a table whose largest number was the depth would put an
 * operator's eye on the wrong one of those every time.
 *
 * # A null rate is never a zero
 *
 * `failure_rate` and `sla_attainment` are null when nothing finished in the window, and the
 * cells say *nothing finished* in words. Rendering 0% for both would hide a stopped queue
 * behind the healthiest-looking number on the page — the exact failure the payload's nullity
 * exists to prevent. `seconds_since_last_finish: -1` is drawn as **never**, in the loud
 * column, because on a clinic that has been running for months it means a job type nobody
 * wired up.
 *
 * # The window is the server's, in words, on the screen
 *
 * Every rate here is over a window, and the sentence above the table says which — from
 * `window_seconds` on the payload rather than from the number this screen asked for, because
 * the endpoint clamps an unacceptable window back to an hour rather than refusing it, and a
 * screen that printed its own request would be captioning an hour's arithmetic with a period
 * nobody computed.
 *
 * # Pausing is behind its own permission and its own confirmation
 *
 * `ops.jobs.manage`, which the physician and QA do not hold — they hold `ops.jobs.read`, and
 * for them these controls are simply not drawn. A control that exists in order to be refused
 * teaches people the software is unreliable. Pausing also stops work a clinic is relying on,
 * so it asks once before doing it.
 */

export interface QueueHealthTableProps {
  windowSeconds: number;
  /** Opens what is waiting for one kind — the queue behind a depth. */
  onOpenQueue: (kind: string) => void;
  /** Opens what is running for one kind. */
  onOpenRunning: (kind: string) => void;
  /** Opens the dead letters, narrowed to one kind. */
  onOpenDeadLetters: (kind: string) => void;
  /** Opens the jobs behind the attainment figure — the ones that missed. */
  onOpenMissed: (kind: string) => void;
}

export function QueueHealthTable({
  windowSeconds,
  onOpenQueue,
  onOpenRunning,
  onOpenDeadLetters,
  onOpenMissed,
}: QueueHealthTableProps) {
  const t = useTranslations('jobs');
  const locale = useLocale() as Locale;
  const client = useQueryClient();

  const mayManage = usePermission('admin.jobs.manage');

  const health = useQuery({
    queryKey: healthKey(windowSeconds),
    queryFn: () => getQueueHealth(windowSeconds),
    refetchInterval: REFRESH_MS,
  });

  /*
   * The catalogue, read once and kept. It changes only when somebody deploys a migration or
   * pauses a kind, so it is not on the fifteen-second timer — but it is invalidated by the
   * pause control below, because `paused_at` lives on it and a kind that says "paused" in one
   * half of a row and not the other is worse than either.
   */
  const kinds = useQuery({ queryKey: KINDS_KEY, queryFn: listKinds });

  /** The kind whose pause is being confirmed. One at a time; this is not a queue to work. */
  const [pausing, setPausing] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
  /**
   * A colleague changed this while it was on screen.
   *
   * Held as the code rather than as a boolean, because the four conflicts say four different
   * things about what happened and an operator who is told "somebody paused this already"
   * stops looking for a bug.
   */
  const [conflict, setConflict] = useState<ConflictCode | null>(null);

  async function setPaused(kind: string, paused: boolean) {
    setBusy(true);
    setFailed(false);
    setConflict(null);
    try {
      await (paused ? pauseKind(kind) : resumeKind(kind));
      setPausing(null);
      await client.invalidateQueries({ queryKey: JOBS_KEY });
    } catch (error) {
      const clash = conflictOf(error);
      if (clash) {
        // Not a failure. Somebody else got here first, so the screen is out of date and the
        // honest response is to make it current and say what happened — refetching before
        // the notice renders, so the row under the sentence already agrees with it.
        setConflict(clash);
        setPausing(null);
        await client.invalidateQueries({ queryKey: JOBS_KEY });
      } else {
        setFailed(true);
      }
    } finally {
      setBusy(false);
    }
  }

  if (health.isPending) return <Skeleton height="16rem" />;

  if (health.isError || !health.data) {
    return (
      // Critical and worded so it cannot be read as reassurance. An unreadable queue and an
      // empty one look identical on a dashboard, and one of them means nothing has run since
      // last night.
      <AlertBanner tone="critical" title={t('health.unavailable')}>
        {t('health.unavailableBody')}
      </AlertBanner>
    );
  }

  const catalogue = catalogueOf(kinds.data);
  const rows = rowsInOrder(health.data.queues);
  const attention = rows.filter(needsAttention);

  return (
    <section className="app-stack" data-testid="queue-health" aria-label={t('health.title')}>
      <header className="app-jobs__summary">
        <p className="app-jobs__window-sentence" data-testid="window-sentence">
          {/* From the payload, not from the request. See the note above. */}
          {t('health.overWindow', { window: ageLabel(t, health.data.window_seconds) })}
        </p>
        <p className="app-jobs__as-of">
          {t('health.asOf', { at: formatDateTime(new Date(health.data.as_of), locale) })}
        </p>
      </header>

      {attention.length > 0 ? (
        <p className="app-jobs__alarm" data-testid="attention-summary">
          {t('health.needAttention', { part: attention.length, whole: rows.length })}
        </p>
      ) : (
        <p className="app-jobs__calm" data-testid="attention-summary">
          {t('health.allWell', { whole: rows.length })}
        </p>
      )}

      {conflict && (
        <AlertBanner
          tone="borderline"
          title={t(`conflict.${conflict}`)}
          onDismiss={() => setConflict(null)}
        >
          {t('conflict.body')}
        </AlertBanner>
      )}

      {failed && (
        <AlertBanner tone="critical" title={t('controlFailed')} onDismiss={() => setFailed(false)}>
          {t('controlFailedBody')}
        </AlertBanner>
      )}

      {rows.length === 0 ? (
        // Not "the queue is quiet". An empty catalogue means no job type is registered at
        // all, which is a broken deployment rather than a calm morning.
        <EmptyState icon="octagon-alert" title={t('health.noKindsTitle')}>
          {t('health.noKindsBody')}
        </EmptyState>
      ) : (
        <div className="app-table-wrap">
          <table className="app-table app-jobs__table">
            <thead>
              <tr>
                <th scope="col">{t('column.kind')}</th>
                <th scope="col">{t('column.state')}</th>
                <th scope="col">{t('column.waiting')}</th>
                <th scope="col">{t('column.running')}</th>
                <th scope="col">{t('column.failures')}</th>
                <th scope="col">{t('column.deadline')}</th>
                <th scope="col">{t('column.lastFinish')}</th>
                {mayManage && <th scope="col">{t('column.controls')}</th>}
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <QueueRow
                  key={row.kind}
                  row={row}
                  description={kindDescription(row.kind, catalogue, locale)}
                  locale={locale}
                  mayManage={mayManage}
                  busy={busy}
                  confirming={pausing === row.kind}
                  onAskPause={() => {
                    setConflict(null);
                    setFailed(false);
                    setPausing(row.kind);
                  }}
                  onCancelPause={() => setPausing(null)}
                  onSetPaused={(paused) => void setPaused(row.kind, paused)}
                  onOpenQueue={() => onOpenQueue(row.kind)}
                  onOpenRunning={() => onOpenRunning(row.kind)}
                  onOpenDeadLetters={() => onOpenDeadLetters(row.kind)}
                  onOpenMissed={() => onOpenMissed(row.kind)}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

function QueueRow({
  row,
  description,
  locale,
  mayManage,
  busy,
  confirming,
  onAskPause,
  onCancelPause,
  onSetPaused,
  onOpenQueue,
  onOpenRunning,
  onOpenDeadLetters,
  onOpenMissed,
}: {
  row: JobQueueHealth;
  description: string;
  locale: Locale;
  mayManage: boolean;
  busy: boolean;
  confirming: boolean;
  onAskPause: () => void;
  onCancelPause: () => void;
  onSetPaused: (paused: boolean) => void;
  onOpenQueue: () => void;
  onOpenRunning: () => void;
  onOpenDeadLetters: () => void;
  onOpenMissed: () => void;
}) {
  const t = useTranslations('jobs');
  const state = severityOf(row);
  const pausedBy = pausedAttribution(row, locale);

  return (
    <tr className="app-jobs__row" data-state={state} data-testid={`kind-${row.kind}`}>
      <th scope="row">
        <div className="app-table__primary">{description}</div>
        {/* The dotted identifier stays on the row even when there is a sentence for it: it is
            what somebody types into a log search, which is the next thing they do. */}
        <div className="app-table__secondary">
          <code>{row.kind}</code>
        </div>
        {/* The class decides the retry defaults (§8.5) and the queue decides which pool of
            workers claims it — which is why a burst of document processing cannot slow down
            a clinician entering a blood pressure, and why "the pipeline queue is backed up"
            is a sentence somebody says on this page. */}
        <div className="app-table__secondary">
          {t(`class.${row.job_class}`)} · {row.queue}
        </div>
      </th>

      <td className="app-jobs__state-cell">
        <QueueStateBadge state={state} />
        {row.paused && (
          // Said again in a sentence, because a paused kind is the one state that looks
          // exactly like a healthy idle one if the reader only takes in the numbers.
          //
          // And it names the person and the hour, which is the half that was missing: the
          // operator reading this at nine on Thursday is not the operator who paused it
          // during Tuesday's incident, and "who do I ask before I turn this back on" is the
          // question they are actually holding. A name with no time would be as bad — a
          // pause from an hour ago and one from last week call for different conversations.
          <div className="app-jobs__paused-note" data-testid={`paused-${row.kind}`}>
            <p>{t('pausedNote')}</p>
            {/* And who, and when. A name with no time would be as unhelpful as a time with
                no name: a pause from an hour ago and one from last Tuesday call for
                different conversations with the same colleague. */}
            {pausedBy !== null && (
              <p className="app-jobs__paused-by">{t(pausedBy.key, pausedBy.values)}</p>
            )}
          </div>
        )}
      </td>

      <td className="app-jobs__age-cell">
        {row.available === 0 ? (
          <span className="app-jobs__nothing">{t('nothingWaiting')}</span>
        ) : (
          <>
            {/* Total age, in the row's largest type: it is the honest number and the one a
                person reads. "Waiting 38 minutes" is true and worth knowing even when 30 of
                those minutes were a deliberate backoff. */}
            <span className="app-jobs__age" data-testid={`age-${row.kind}`}>
              {ageLabel(t, row.oldest_available_seconds)}
            </span>
            {/* And the due age underneath, but only when it says something different.
                Printing the same number twice teaches a reader to stop reading the second
                line, which is where the interesting case lives: "38 minutes waiting, none
                of it due" is a retry policy working, and drawn as 38 minutes alone it is
                indistinguishable from a wedged worker. */}
            {someWaitingIsDeliberate(row) && (
              <span className="app-jobs__due" data-testid={`due-${row.kind}`}>
                {row.oldest_due_seconds === 0
                  ? t('noneDue')
                  : t('dueFor', { age: ageLabel(t, row.oldest_due_seconds) })}
              </span>
            )}
            <span className="app-jobs__depth">
              <Button
                variant="quiet"
                size="sm"
                data-testid={`open-queue-${row.kind}`}
                onClick={onOpenQueue}
              >
                {t('waitingCount', { count: row.available })}
              </Button>
            </span>
          </>
        )}
      </td>

      <td className="app-jobs__count-cell">
        {row.running === 0 ? (
          formatCount(0, locale)
        ) : (
          // A way through, for the same reason the depth has one. "Two running" is a number
          // an operator can do nothing with; "two running, and one of them has been running
          // for four minutes" is a wedged worker. The depth had a way through from the first
          // version of this page and this did not, which made the busiest column on a
          // failing row the only dead end on it.
          <Button
            variant="quiet"
            size="sm"
            aria-label={t('seeRunningNamed', { kind: row.kind })}
            data-testid={`open-running-${row.kind}`}
            onClick={onOpenRunning}
          >
            {formatCount(row.running, locale)}
          </Button>
        )}
      </td>

      <td className="app-jobs__rate-cell">
        {rateIsAvailable(row.failure_rate) ? (
          <>
            {/* The percentage says what it is a percentage *of* — a bare "2.4%" in a table
                cell is a number two readers will take to mean two different things. */}
            <span className="app-jobs__rate" data-testid={`failures-${row.kind}`}>
              {t('failureRate', { value: row.failure_rate ?? 0 })}
            </span>
            <div className="app-table__secondary">
              {t('outOfFinished', {
                part: row.discarded,
                whole: row.succeeded + row.discarded,
              })}
            </div>
          </>
        ) : (
          /* Not 0%. Nothing finished in the window, which is a different fact and is
             sometimes the only thing wrong with this row. */
          <span className="app-jobs__nothing" data-testid={`failures-${row.kind}`}>
            {t('nothingFinished')}
          </span>
        )}

        {/* Outside the branch above, deliberately. `dead_letters` is not windowed and the
            rate is, so a kind with forty jobs waiting for somebody can read "nothing
            finished" in the last hour — and on the old rendering, which hung the way
            through to them off the windowed count, those forty were reachable only by
            widening the window until they appeared. The number an operator can act on does
            not get to depend on which window they happened to choose. */}
        {hasDeadLetters(row) && (
          <p className="app-jobs__dead" data-testid={`dead-${row.kind}`}>
            <span className="app-jobs__dead-count">
              {t('deadLetterCount', { count: row.dead_letters })}
            </span>
            <Button
              variant="quiet"
              size="sm"
              aria-label={t('seeGivenUpNamed', { kind: row.kind })}
              data-testid={`open-dead-${row.kind}`}
              onClick={onOpenDeadLetters}
            >
              {t('seeGivenUp')}
            </Button>
          </p>
        )}
      </td>

      <td className="app-jobs__rate-cell">
        {!hasDeadline(row) ? (
          <span className="app-jobs__nothing">{t('noDeadline')}</span>
        ) : rateIsAvailable(row.sla_attainment) ? (
          <>
            <span className="app-jobs__rate" data-testid={`sla-${row.kind}`}>
              {t('slaRate', { value: row.sla_attainment ?? 0 })}
            </span>
            <div className="app-table__secondary">
              {t('metDeadline', { part: row.sla_met, whole: row.sla_measured })}
            </div>
            <div className="app-table__secondary">
              {t('deadlineIs', { window: ageLabel(t, row.sla_seconds ?? 0) })}
            </div>
            {/* The way through to the ones that missed, and it is the one that matters most
                on this row. "63.6% on time, 14 of 22 met it" is the number §7.1 is measured
                by; without a way to open the eight it is a figure asserted to its reader
                however carefully it was computed — and they are unreachable any other way,
                having succeeded (so not dead letters) and finished (so not waiting). */}
            {missedDeadlines(row) > 0 && (
              <Button
                variant="quiet"
                size="sm"
                aria-label={t('seeMissedNamed', { kind: row.kind })}
                data-testid={`open-missed-${row.kind}`}
                onClick={onOpenMissed}
              >
                {t('seeMissed', { count: missedDeadlines(row) })}
              </Button>
            )}
          </>
        ) : (
          <span className="app-jobs__nothing" data-testid={`sla-${row.kind}`}>
            {t('nothingMeasured')}
          </span>
        )}
      </td>

      <td>
        {hasEverFinished(row) ? (
          <span data-testid={`last-finish-${row.kind}`}>
            {t('agoLabel', { age: ageLabel(t, row.seconds_since_last_finish) })}
          </span>
        ) : (
          /* The single most interesting thing this row can say, so it is a word rather than
             a dash and it is not quiet. */
          <span className="app-jobs__never" data-testid={`last-finish-${row.kind}`}>
            {t('neverFinished')}
          </span>
        )}
      </td>

      {mayManage && (
        <td className="app-table__actions">
          {confirming ? (
            <div className="app-jobs__confirm">
              <p>{row.paused ? t('confirmResume') : t('confirmPause')}</p>
              <Button variant="quiet" size="sm" disabled={busy} onClick={onCancelPause}>
                {t('back')}
              </Button>
              <Button
                variant="primary"
                size="sm"
                disabled={busy}
                data-testid={`confirm-pause-${row.kind}`}
                onClick={() => onSetPaused(!row.paused)}
              >
                {row.paused ? t('resume') : t('pause')}
              </Button>
            </div>
          ) : (
            <Button
              variant="secondary"
              size="sm"
              disabled={busy}
              // Named, because a table of nine rows has nine buttons reading "Pause" and a
              // screen reader hears them as nine identical controls.
              aria-label={
                row.paused
                  ? t('resumeNamed', { kind: row.kind })
                  : t('pauseNamed', { kind: row.kind })
              }
              data-testid={`pause-${row.kind}`}
              onClick={onAskPause}
            >
              {row.paused ? t('resume') : t('pause')}
            </Button>
          )}
        </td>
      )}
    </tr>
  );
}
