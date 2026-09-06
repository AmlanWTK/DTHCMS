/**
 * `jobs` — the background queue's operator view, on the web (CP69, ADR-0031, §7.1, §8.5).
 *
 * ADR-0031 made the queue a table in this database rather than a framework, and its first
 * stated consequence is the reason this feature exists in the shape it does: `ops.job_kind` is
 * a **registered catalogue**, so the health page can list every kind including the ones with
 * nothing in flight — because a dashboard assembled from what is currently queued cannot show
 * that a job type has stopped being enqueued at all, which is the failure a dashboard exists
 * to catch.
 *
 * # What somebody is holding when they open this
 *
 * One of two things. Either background work is suspected to have stopped, or a physician has
 * said the AI summary was not ready when the patient sat down — §7.1's five minutes, which
 * ADR-0031 §2 calls the point of the whole checkpoint. Both questions have to be answerable
 * before the reader has finished scanning the page.
 *
 * # Four rules, each of which an ordinary dashboard breaks while looking healthy
 *
 * **Every registered kind is a row.** No filter drops a quiet one. The row that is missing is
 * the failure this screen was built for.
 *
 * **A null rate is not a zero and is not healthy.** `failure_rate` and `sla_attainment` are
 * null when nothing finished in the window; the cells say *nothing finished* in words, and the
 * state badge for such a row is grey rather than green. `seconds_since_last_finish: -1` reads
 * as **never**. There is no `rateOr(0)` in this feature and there must never be one.
 *
 * **Age outranks depth, and there are two ages.** Total age leads the row because it is the
 * honest number a person reads; `oldest_due_seconds` decides whether the row is called stuck,
 * because a job sitting out an exponential backoff is waiting on purpose and an alarm that
 * fires on it is one people stop reading. The row draws both and says which is which whenever
 * they differ. The threshold is the kind's own SLA where it has one, so a `clinical.synthesis`
 * job six minutes overdue is flagged by the same number §7.1 promises.
 *
 * **A paused kind is unmistakable, and says who stopped it.** It outranks every other state,
 * it is the only violet thing on the page, and the row carries the sentence *and* the name and
 * the hour from `paused_at` and `paused_by_name_*`. Paused work is work that has stopped
 * happening on purpose; by the numbers alone it is indistinguishable from a healthy idle
 * queue, and "who do I ask before turning this back on" is the question the reader is holding.
 *
 * **A dead letter from last night is still one this morning.** `dead_letters` is un-windowed
 * and sits on the row beside the windowed rate, so a kind reading "nothing finished in the
 * last hour" still shows the forty jobs waiting for a person — and the way through to them
 * does not depend on which window somebody happened to choose.
 *
 * **Every number on the row opens.** The depth, the running count, the dead letters and — the
 * one that matters most — the attainment figure. §7.1 is *measured* rather than asserted only
 * if a physician saying "the summary was not ready at 10:15" can be shown one of the jobs
 * behind the percentage; those jobs succeeded, so they are not among the dead letters, and
 * they finished, so they are not waiting, and `sla=missed` with no status is the only way to
 * reach them. A figure nobody can open is asserted to its reader however carefully it was
 * computed.
 *
 * # Two permissions, and the controls are absent rather than refused
 *
 * `ops.jobs.read` is the page. `ops.jobs.manage` is retry, cancel and pause — held by the
 * administrator and not by the physician or QA, who hold the read. Where it is not held the
 * controls are not drawn: a control that exists in order to be refused teaches people that the
 * software is unreliable.
 *
 * # And one thing that is deliberately absent
 *
 * **There is nothing here that enqueues a job**, because there is no endpoint for it. Work is
 * enqueued by the code that decided it was needed, inside the transaction that made that
 * decision true — which is how "a job enqueued in a rolled-back transaction never runs" is a
 * property of the system rather than a thing to remember. If a future screen wants one, that
 * is the design working.
 */
export { JobsConsole } from './components/JobsConsole';
export { QueueHealthTable, type QueueHealthTableProps } from './components/QueueHealthTable';
export { QueueStateBadge, type QueueStateBadgeProps } from './components/QueueStateBadge';
export { JobList, type JobListProps } from './components/JobList';
export { JobDetail, type JobDetailProps } from './components/JobDetail';
export { WindowPicker, type WindowPickerProps } from './components/WindowPicker';
export {
  ageLabel,
  catalogueOf,
  kindDescription,
  pausedAttribution,
  ranForLabel,
  type Translate,
} from './components/jobsText';
export {
  CONFLICT_CODES,
  DEFAULT_STALL_SECONDS,
  DEFAULT_WINDOW_SECONDS,
  JOBS_KEY,
  KINDS_KEY,
  MAX_WINDOW_SECONDS,
  MIN_WINDOW_SECONDS,
  NEVER_FINISHED,
  REFRESH_MS,
  WINDOW_CHOICES,
  ageParts,
  cancelJob,
  cancelReasonReady,
  conflictOf,
  getJob,
  getQueueHealth,
  hasDeadLetters,
  hasDeadline,
  hasEverFinished,
  healthKey,
  jobKey,
  jobListKey,
  listJobs,
  listKinds,
  missedDeadlines,
  needsAttention,
  pauseKind,
  ranForParts,
  rateIsAvailable,
  resumeKind,
  retryJob,
  rowsInOrder,
  severityOf,
  someWaitingIsDeliberate,
  stallThresholdFor,
  windowIsAcceptable,
  type AgeParts,
  type AgeUnit,
  type ConflictCode,
  type Job,
  type JobAttempt,
  type JobClass,
  type JobKind,
  type JobQueueHealth,
  type JobStatus,
  type JobView,
  type QueueHealth,
  type QueueState,
  type RanForParts,
  type RanForUnit,
} from './api/jobs';
