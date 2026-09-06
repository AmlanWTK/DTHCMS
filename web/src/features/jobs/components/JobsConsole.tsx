'use client';

import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslations } from 'next-intl';
import { useState } from 'react';

import { AlertBanner } from '@dthcms/ui';

import { usePermission } from '@/lib/use-permission';

import {
  DEFAULT_WINDOW_SECONDS,
  JOBS_KEY,
  KINDS_KEY,
  cancelJob,
  conflictOf,
  listKinds,
  retryJob,
  windowIsAcceptable,
  type ConflictCode,
  type JobView,
} from '../api/jobs';

import { JobDetail } from './JobDetail';
import { JobList } from './JobList';
import { QueueHealthTable } from './QueueHealthTable';
import { WindowPicker } from './WindowPicker';

/**
 * The background queue, for whoever is on the floor when it goes red (CP69, ADR-0031).
 *
 * # Three surfaces, one screen, in the order the questions are asked
 *
 * The health table answers *is anything wrong*, the list answers *what*, and the detail
 * answers *why*. They are one route rather than three because the second question is only
 * ever asked in the ten seconds after the first, and because the window somebody chose has to
 * travel with them: a failure rate read over an hour and a dead-letter list read over all
 * time are two screens quietly disagreeing unless the page says which is which, which it
 * does.
 *
 * The detail opens **in place** for the same reason the supervisor's quality record does —
 * coming back is one press rather than a browser gesture on a tablet, and nothing about the
 * page a person was reading has to be reconstructed from a URL.
 *
 * # Where the two write controls live and why the conflicts are handled twice
 *
 * Pausing a kind belongs beside the kind, in the table. Retrying and cancelling a job belong
 * beside the job, here. Each surface reports its own `409` next to the control that produced
 * it, because a notice about a pause floating above a list of dead letters is a notice
 * attached to the wrong thing.
 *
 * All four conflicts mean the same thing about the world — *somebody else changed this while
 * you were looking* — and none of them is treated as a failure. The screen refetches and says
 * what happened, because an operator told "could not retry" goes looking for a bug, and an
 * operator told "a colleague already retried this" is finished.
 *
 * # Reading is `ops.jobs.read`; touching is `ops.jobs.manage`
 *
 * The physician and QA hold the first and not the second, and for them the controls are
 * simply **not drawn** rather than drawn and refused. Retrying a dead-lettered job runs code
 * against a patient's record and pausing a kind stops the synthesis §7.1 promises will be
 * ready before the consultation; the floor supervisor who needs to know whether the queue is
 * healthy should not thereby be able to turn it off.
 *
 * # There is no way to enqueue anything from here
 *
 * There is no endpoint and there is no button, and that is the design working rather than a
 * gap. Work is enqueued by the code that decided it was needed, inside the transaction that
 * made that decision true.
 */
export function JobsConsole() {
  const t = useTranslations('jobs');
  const client = useQueryClient();

  const mayManage = usePermission('admin.jobs.manage');

  const [windowSeconds, setWindowSeconds] = useState(DEFAULT_WINDOW_SECONDS);

  /** Which list is showing under the table, and whether it is narrowed to one kind. */
  const [view, setView] = useState<JobView>('DISCARDED');
  const [kind, setKind] = useState<string | null>(null);
  /** The job whose detail is open, in place of the list. */
  const [openJob, setOpenJob] = useState<string | null>(null);

  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
  const [conflict, setConflict] = useState<ConflictCode | null>(null);
  const [done, setDone] = useState<'retried' | 'cancelled' | null>(null);

  /*
   * The catalogue, read once here and handed down. Three components need the bilingual
   * description of a kind and a single request answers all three; each fetching its own would
   * be three round trips for one table that changes when somebody deploys.
   */
  const kinds = useQuery({ queryKey: KINDS_KEY, queryFn: listKinds });

  function clearNotices() {
    setFailed(false);
    setConflict(null);
    setDone(null);
  }

  async function act(run: () => Promise<void>, outcome: 'retried' | 'cancelled') {
    setBusy(true);
    clearNotices();
    try {
      await run();
      setDone(outcome);
    } catch (error) {
      const clash = conflictOf(error);
      if (clash) setConflict(clash);
      else setFailed(true);
    } finally {
      /*
       * Refetched on every path, including the failures. A `409` means the screen is out of
       * date, and an ordinary failure leaves an operator who cannot tell whether the act
       * landed — in both cases the useful next thing is the current state, not the state
       * they were looking at when they pressed the button.
       *
       * The whole `jobs` prefix, because one retry moves four reads at once: the job, the
       * dead-letter list it was in, its kind's depth, and the attention count above the
       * table.
       */
      await client.invalidateQueries({ queryKey: JOBS_KEY });
      setBusy(false);
    }
  }

  return (
    <div className="app-stack app-jobs" data-testid="jobs-console">
      <WindowPicker
        windowSeconds={windowSeconds}
        onChange={(next) => {
          if (windowIsAcceptable(next)) setWindowSeconds(next);
        }}
      />

      <QueueHealthTable
        windowSeconds={windowSeconds}
        onOpenQueue={(openKind) => {
          clearNotices();
          setOpenJob(null);
          setView('AVAILABLE');
          setKind(openKind);
        }}
        onOpenRunning={(openKind) => {
          clearNotices();
          setOpenJob(null);
          setView('RUNNING');
          setKind(openKind);
        }}
        onOpenDeadLetters={(openKind) => {
          clearNotices();
          setOpenJob(null);
          setView('DISCARDED');
          setKind(openKind);
        }}
        onOpenMissed={(openKind) => {
          clearNotices();
          setOpenJob(null);
          setView('MISSED');
          setKind(openKind);
        }}
      />

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

      {done && (
        <AlertBanner tone="normal" title={t(`done.${done}`)} onDismiss={() => setDone(null)}>
          {t(`done.${done}Body`)}
        </AlertBanner>
      )}

      {openJob === null ? (
        <JobList
          view={view}
          kind={kind}
          catalogue={kinds.data}
          mayManage={mayManage}
          busy={busy}
          onOpen={(id) => {
            clearNotices();
            setOpenJob(id);
          }}
          onRetry={(id) => void act(() => retryJob(id), 'retried')}
          onClearKind={() => setKind(null)}
        />
      ) : (
        <JobDetail
          id={openJob}
          catalogue={kinds.data}
          mayManage={mayManage}
          busy={busy}
          onBack={() => {
            clearNotices();
            setOpenJob(null);
          }}
          onRetry={(id) => void act(() => retryJob(id), 'retried')}
          onCancel={(id, reason) => void act(() => cancelJob(id, reason), 'cancelled')}
        />
      )}
    </div>
  );
}
