'use client';

import { useQuery } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useState, type ReactNode } from 'react';

import { AlertBanner, Button, Card, Input, Skeleton } from '@dthcms/ui';

import { formatDateTime } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';

import {
  cancelReasonReady,
  getJob,
  jobKey,
  type Job,
  type JobKind,
  type JobAttempt,
} from '../api/jobs';

import { catalogueOf, kindDescription, ranForLabel } from './jobsText';

/**
 * One job, and every attempt it made (CP69).
 *
 * # Why the attempts are the screen rather than a section of it
 *
 * Somebody opening a dead-lettered job is opening it to read the errors. The API sends them
 * with the job rather than behind a second request for exactly that reason, and this draws
 * them as the body of the page rather than as a collapsed panel: a screen that made an
 * operator click again to see the thing they came for would be built for the shape of the
 * endpoint rather than for the question.
 *
 * # How long an attempt ran is drawn beside what it said
 *
 * A job that fails in thirty milliseconds failed before it did anything — a bad argument, a
 * missing row, a refusal. One that fails after four minutes failed part-way through, and may
 * well have half-done something. They are different problems with the same error text on
 * them, and the only thing that distinguishes them is the duration, so it is never rounded
 * away below a second.
 *
 * The worker is on every attempt for the same family of reasons: five attempts that all
 * failed on `pod-3` is one wedged pod, and five spread across five workers is the job.
 *
 * # `args` is rendered, and it is safe to render
 *
 * `ops.job.args` may reference but must never embed PHI — a check constraint refuses it on
 * the way in and invariant 90 re-checks the whole table (ADR-0031 §1). So what is here is
 * ids, codes and counts, and rendering them is how an operator answers "which one failed"
 * without leaving the page. Nothing here looks any of them up: this screen's reader holds
 * `ops.jobs.read` and may hold nothing clinical, and a helpfully resolved patient name would
 * be this feature quietly acquiring a clinical surface.
 *
 * # The two controls, and why cancel asks for a sentence
 *
 * Retry is offered on a job that has been given up on; cancel on one that has not started.
 * Neither is drawn without `ops.jobs.manage`. The reason on a cancellation is required by
 * the server and required here before the button enables, because the refusal otherwise
 * costs a round trip and a re-read of a form on a clinic's connection — and because a
 * cancelled job that says nothing is a gap somebody has to explain later, and the person who
 * can explain it is the one cancelling it now.
 */

export interface JobDetailProps {
  id: string;
  catalogue: readonly JobKind[] | undefined;
  mayManage: boolean;
  busy: boolean;
  onBack: () => void;
  onRetry: (id: string) => void;
  onCancel: (id: string, reason: string) => void;
}

export function JobDetail({
  id,
  catalogue,
  mayManage,
  busy,
  onBack,
  onRetry,
  onCancel,
}: JobDetailProps) {
  const t = useTranslations('jobs');
  const locale = useLocale() as Locale;

  const job = useQuery({
    queryKey: jobKey(id),
    queryFn: () => getJob(id),
    /*
     * No poll. Everything else on this page refreshes on a timer; this one does not, because
     * an operator may be half-way through typing a cancellation reason and a refetch that
     * re-rendered the form under them would lose it. The list above is on the timer and the
     * acts on this screen invalidate it, which is enough.
     */
  });

  const [reason, setReason] = useState('');
  const [cancelling, setCancelling] = useState(false);

  const kinds = catalogueOf(catalogue);

  return (
    <section className="app-stack" data-testid="job-detail">
      <Button variant="quiet" data-testid="back-to-list" onClick={onBack}>
        {t('backToList')}
      </Button>

      {job.isPending && <Skeleton height="14rem" />}

      {job.isError && (
        <AlertBanner tone="critical" title={t('detail.unavailable')}>
          {t('detail.unavailableBody')}
        </AlertBanner>
      )}

      {job.data !== undefined && (
        <Card elevation="raised" className="app-jobs__detail">
          <header className="app-jobs__detail-head">
            <h2>{kindDescription(job.data.kind, kinds, locale)}</h2>
            <p className="app-table__secondary">
              <code>{job.data.kind}</code> · {job.data.queue}
            </p>
            <p className="app-jobs__job-status" data-status={job.data.status}>
              {t(`status.${job.data.status}`)}
            </p>
          </header>

          <dl className="app-jobs__facts">
            <Fact label={t('fact.enqueued')}>
              {formatDateTime(new Date(job.data.enqueued_at), locale)}
            </Fact>
            <Fact label={t('fact.attempts')}>
              {t('attemptOf', { part: job.data.attempt, whole: job.data.max_attempts })}
            </Fact>
            <Fact label={t('fact.runAt')}>{formatDateTime(new Date(job.data.run_at), locale)}</Fact>
            {job.data.started_at && (
              <Fact label={t('fact.started')}>
                {formatDateTime(new Date(job.data.started_at), locale)}
              </Fact>
            )}
            {job.data.finished_at && (
              <Fact label={t('fact.finished')}>
                {formatDateTime(new Date(job.data.finished_at), locale)}
              </Fact>
            )}
            {job.data.leased_by && <Fact label={t('fact.worker')}>{job.data.leased_by}</Fact>}
            {job.data.sla_deadline && (
              <Fact label={t('fact.deadline')}>
                {formatDateTime(new Date(job.data.sla_deadline), locale)}
              </Fact>
            )}
            {/* `met_sla` is null while unfinished and for a kind with no deadline, and those
                are different from a miss. Only a decided answer is drawn, and a false is
                drawn loudly: a false here is the whole point of measuring. */}
            {job.data.met_sla !== null && (
              <Fact label={t('fact.metDeadline')}>
                <span
                  className={job.data.met_sla ? undefined : 'app-jobs__missed'}
                  data-testid="met-sla"
                >
                  {job.data.met_sla ? t('fact.metYes') : t('fact.metNo')}
                </span>
              </Fact>
            )}
            {job.data.dedupe_key && (
              <Fact label={t('fact.dedupe')}>
                <code>{job.data.dedupe_key}</code>
              </Fact>
            )}
            <Fact label={t('fact.id')}>
              {/* The handle an operator quotes into a log search, which is the next thing
                  they do: the full error text is in the log, joined to this by the id. */}
              <code>{job.data.id}</code>
            </Fact>
          </dl>

          <JobArgs args={job.data.args} />

          {job.data.last_error && (
            <p className="app-jobs__error">
              <span className="app-jobs__error-label">
                {job.data.status === 'CANCELLED' ? t('cancelledBecause') : t('lastError')}
              </span>
              <span className="app-jobs__error-text">{job.data.last_error}</span>
            </p>
          )}

          <Attempts attempts={job.data.attempts ?? []} locale={locale} />

          {mayManage && (
            <div className="app-jobs__detail-actions">
              {job.data.status === 'DISCARDED' && (
                <Button
                  variant="primary"
                  disabled={busy}
                  data-testid="detail-retry"
                  onClick={() => onRetry(job.data.id)}
                >
                  {t('retry')}
                </Button>
              )}

              {job.data.status === 'AVAILABLE' &&
                (cancelling ? (
                  <div className="app-jobs__cancel">
                    <Input
                      label={t('cancelReason')}
                      description={t('cancelReasonHint')}
                      value={reason}
                      required
                      data-testid="cancel-reason"
                      onChange={(event) => setReason(event.target.value)}
                    />
                    <div className="app-jobs__cancel-actions">
                      <Button
                        variant="quiet"
                        disabled={busy}
                        onClick={() => {
                          setCancelling(false);
                          setReason('');
                        }}
                      >
                        {t('back')}
                      </Button>
                      <Button
                        variant="danger"
                        // Checked here as well as on the server, so the refusal never costs
                        // a round trip and a re-read of a form already filled in.
                        disabled={busy || !cancelReasonReady(reason)}
                        data-testid="confirm-cancel"
                        onClick={() => onCancel(job.data.id, reason)}
                      >
                        {t('cancelJob')}
                      </Button>
                    </div>
                  </div>
                ) : (
                  <Button
                    variant="secondary"
                    disabled={busy}
                    data-testid="start-cancel"
                    onClick={() => setCancelling(true)}
                  >
                    {t('cancelJob')}
                  </Button>
                ))}
            </div>
          )}
        </Card>
      )}
    </section>
  );
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="app-jobs__fact">
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  );
}

/**
 * What the job was asked to do.
 *
 * Ids, codes and counts, drawn as they arrived. There is no formatting here that could turn
 * a reference into a sentence about a patient, and nothing that follows one.
 */
function JobArgs({ args }: { args: Job['args'] }) {
  const t = useTranslations('jobs');
  const entries = Object.entries(args ?? {});

  return (
    <section className="app-jobs__args">
      <h3 className="app-jobs__section-sub">{t('argsTitle')}</h3>
      <p className="app-jobs__hint">{t('argsHint')}</p>
      {entries.length === 0 ? (
        <p className="app-jobs__nothing">{t('argsNone')}</p>
      ) : (
        <dl className="app-jobs__facts" data-testid="job-args">
          {entries.map(([key, value]) => (
            <Fact key={key} label={key}>
              <code>{typeof value === 'string' ? value : JSON.stringify(value)}</code>
            </Fact>
          ))}
        </dl>
      )}
    </section>
  );
}

/**
 * Every attempt, oldest first, with how long it ran and what it said.
 *
 * Oldest first rather than newest, because the useful reading is the *shape* of the
 * sequence: five identical timeouts is a dependency that is down, and a bad argument
 * followed by four timeouts is two problems where the second one is a distraction.
 */
function Attempts({ attempts, locale }: { attempts: readonly JobAttempt[]; locale: Locale }) {
  const t = useTranslations('jobs');

  return (
    <section className="app-jobs__attempts">
      <h3 className="app-jobs__section-sub">{t('attempts.title')}</h3>
      {attempts.length === 0 ? (
        /* Not an error and not an empty state with a tick: a job with no failed attempts
           either has not run or ran cleanly, and this section is simply not the story. */
        <p className="app-jobs__nothing" data-testid="no-attempts">
          {t('attempts.none')}
        </p>
      ) : (
        <ol className="app-jobs__attempt-list" data-testid="attempts">
          {[...attempts]
            .sort((a, b) => a.attempt - b.attempt)
            .map((attempt) => (
              <li key={attempt.attempt} className="app-jobs__attempt">
                <p className="app-jobs__attempt-head">
                  <span className="app-jobs__attempt-number">
                    {t('attempts.number', { count: attempt.attempt })}
                  </span>
                  <span className="app-jobs__attempt-ran" data-testid="ran-for">
                    {t('attempts.ranFor', { duration: ranForLabel(t, attempt.ran_for_ms) })}
                  </span>
                  <span className="app-table__secondary">
                    {formatDateTime(new Date(attempt.failed_at), locale)}
                  </span>
                </p>
                <p className="app-table__secondary">
                  {/* Which process held it. A lease held by "worker" tells nobody which of
                      six pods is wedged. */}
                  {t('attempts.on', { worker: attempt.worker })}
                </p>
                <p className="app-jobs__attempt-error">{attempt.error}</p>
              </li>
            ))}
        </ol>
      )}
      {attempts.length > 0 && (
        // The error text on each attempt is truncated for rendering; the whole of it is in
        // the log, joined to this by the job id above. Said once, here, rather than on every
        // attempt.
        <p className="app-jobs__hint">{t('attempts.truncated')}</p>
      )}
    </section>
  );
}
