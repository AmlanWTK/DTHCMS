'use client';

import { useQuery } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';

import { AlertBanner, Button, EmptyState, Skeleton } from '@dthcms/ui';

import { formatDateTime } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';

import {
  REFRESH_MS,
  jobListKey,
  listJobs,
  type Job,
  type JobKind,
  type JobView,
} from '../api/jobs';

import { catalogueOf, kindDescription } from './jobsText';

/**
 * What was given up on, and what is waiting (CP69).
 *
 * # This is the list somebody actually opens the page to fix
 *
 * The health table answers *is anything wrong*. This answers *what*, and it is where an
 * operator spends the rest of their visit. `status=DISCARDED` is the dead letters — jobs
 * that exhausted their retry policy and were given up on — and it is the default view of
 * this section for that reason.
 *
 * # The dead letters are not the same set as a health row's `discarded` count
 *
 * That count is **windowed**: it is how many were given up on in the last hour, or day, or
 * whatever the picker says. This list is not windowed at all. So a kind can read "nothing
 * given up on" upstairs and have forty rows down here from last night, and both are true.
 * The heading says which is which in words, because a reader who assumed they were the same
 * set would conclude the screen was lying to them — and would be right to stop trusting it.
 *
 * # Newest first, and capped
 *
 * The server orders by `enqueued_at` descending and takes a limit. A page of dead letters is
 * a page of one outage; a scroll of four hundred is a report, and nobody works a report at
 * eight in the morning. The cap is stated when it is reached rather than left as a silent
 * truncation, because a list that quietly stops is a list somebody believes is complete.
 *
 * # No name, no number, nothing clinical
 *
 * By construction rather than by care: `ops.job.args` may reference but must never embed PHI
 * — a check constraint refuses it on the way in and invariant 90 re-checks the whole table.
 * So there is no redaction here to get wrong, and equally nothing here fetches a patient to
 * put a name beside a row. The reader of this screen holds `ops.jobs.read` and may hold
 * nothing clinical at all.
 */

/**
 * How many rows one page holds.
 *
 * Fifty is the server's own default and the size of one incident. The list says when it is
 * full rather than pretending to be complete.
 */
export const PAGE_SIZE = 50;

/**
 * Which of the four views this section names, and where its words live.
 *
 * Three are statuses and `MISSED` is not — it crosses them, because a job that missed its
 * deadline may have succeeded or been given up on. See `JobView`.
 */
const SECTION: Record<JobView, string> = {
  AVAILABLE: 'queued',
  RUNNING: 'running',
  DISCARDED: 'deadLetters',
  MISSED: 'missed',
};

export interface JobListProps {
  view: JobView;
  /** One kind, or every kind. Null is the whole floor, which is how it opens. */
  kind: string | null;
  catalogue: readonly JobKind[] | undefined;
  mayManage: boolean;
  busy: boolean;
  onOpen: (id: string) => void;
  onRetry: (id: string) => void;
  /** Drops the kind filter. Only offered when there is one. */
  onClearKind: () => void;
}

export function JobList({
  view,
  kind,
  catalogue,
  mayManage,
  busy,
  onOpen,
  onRetry,
  onClearKind,
}: JobListProps) {
  const t = useTranslations('jobs');
  const locale = useLocale() as Locale;

  const jobs = useQuery({
    queryKey: jobListKey(view, kind),
    queryFn: () => listJobs(view, { kind, limit: PAGE_SIZE }),
    /*
     * The same interval as the table above it, and that is not a preference.
     *
     * The health row says "3 jobs are waiting for somebody" and this list is the three jobs.
     * On a page where only the table refreshed, a colleague retrying one from the next desk
     * moved the count to 2 within fifteen seconds and left three rows underneath it — one
     * screen quietly disagreeing with itself about the thing it exists to show, which is
     * precisely the failure this feature spends its comments arguing against elsewhere.
     *
     * The two still come from two requests at two instants, so they can differ for a moment.
     * That is inherent — a count and a page of rows are different endpoints — but a moment is
     * a very different thing from until-you-reload.
     */
    refetchInterval: REFRESH_MS,
  });

  const kinds = catalogueOf(catalogue);

  return (
    <section className="app-stack" data-testid={`job-list-${view}`}>
      <header className="app-jobs__list-head">
        <h2 className="app-jobs__section">{t(`${SECTION[view]}.title`)}</h2>
        <p className="app-jobs__hint">{t(`${SECTION[view]}.lead`)}</p>
        {kind !== null && (
          <p className="app-jobs__filter">
            <span>{t('filteredTo', { kind: kindDescription(kind, kinds, locale) })}</span>
            <Button variant="quiet" size="sm" data-testid="clear-kind" onClick={onClearKind}>
              {t('showEveryKind')}
            </Button>
          </p>
        )}
      </header>

      {jobs.isPending && <Skeleton height="10rem" />}

      {jobs.isError && (
        <AlertBanner tone="critical" title={t('list.unavailable')}>
          {t('list.unavailableBody')}
        </AlertBanner>
      )}

      {jobs.data !== undefined && jobs.data.length === 0 && (
        <EmptyState icon="check" title={t(`${SECTION[view]}.emptyTitle`)}>
          {t(`${SECTION[view]}.emptyBody`)}
        </EmptyState>
      )}

      {jobs.data !== undefined && jobs.data.length > 0 && (
        <>
          <ul className="app-jobs__jobs">
            {jobs.data.map((job) => (
              <li key={job.id}>
                <JobRow
                  job={job}
                  description={kindDescription(job.kind, kinds, locale)}
                  locale={locale}
                  mayManage={mayManage}
                  busy={busy}
                  onOpen={() => onOpen(job.id)}
                  onRetry={() => onRetry(job.id)}
                />
              </li>
            ))}
          </ul>
          {jobs.data.length === PAGE_SIZE && (
            // Stated rather than silent. A truncated list nobody was told about is a list
            // somebody counts and reports as a total.
            <p className="app-jobs__hint" data-testid="list-capped">
              {t('list.capped', { count: PAGE_SIZE })}
            </p>
          )}
        </>
      )}
    </section>
  );
}

function JobRow({
  job,
  description,
  locale,
  mayManage,
  busy,
  onOpen,
  onRetry,
}: {
  job: Job;
  description: string;
  locale: Locale;
  mayManage: boolean;
  busy: boolean;
  onOpen: () => void;
  onRetry: () => void;
}) {
  const t = useTranslations('jobs');

  return (
    <article
      className="app-jobs__job"
      data-status={job.status}
      data-missed={job.met_sla === false}
      data-testid={`job-${job.id}`}
    >
      <header className="app-jobs__job-head">
        <h3>{description}</h3>
        <span className="app-jobs__job-when">
          {formatDateTime(new Date(job.finished_at ?? job.enqueued_at), locale)}
        </span>
      </header>

      <p className="app-table__secondary">
        <code>{job.kind}</code> · {t('attemptOf', { part: job.attempt, whole: job.max_attempts })}
      </p>

      {/* A finished job that missed its deadline carries no error and would otherwise look
          identical to one that was on time — which is the whole reason this list is worth
          opening. `met_sla` is null while unfinished and for a kind with no deadline, so
          only a decided `false` is drawn. */}
      {job.met_sla === false && (
        <p className="app-jobs__missed-flag" data-testid={`missed-${job.id}`}>
          {t('missedItsDeadline')}
        </p>
      )}

      {job.last_error && (
        /* A cancelled job's `last_error` is the reason somebody typed when they cancelled
           it, not a failure — the server writes the reason into that column. Labelling both
           the same way would show an operator their own sentence under the word "error". */
        <p className="app-jobs__error" data-testid={`error-${job.id}`}>
          <span className="app-jobs__error-label">
            {job.status === 'CANCELLED' ? t('cancelledBecause') : t('lastError')}
          </span>
          <span className="app-jobs__error-text">{job.last_error}</span>
        </p>
      )}

      <div className="app-jobs__job-actions">
        <Button variant="secondary" size="sm" data-testid={`open-${job.id}`} onClick={onOpen}>
          {t('openJob')}
        </Button>
        {mayManage && job.status === 'DISCARDED' && (
          <Button
            variant="primary"
            size="sm"
            disabled={busy}
            aria-label={t('retryNamed', { kind: job.kind })}
            data-testid={`retry-${job.id}`}
            onClick={onRetry}
          >
            {t('retry')}
          </Button>
        )}
      </div>
    </article>
  );
}
