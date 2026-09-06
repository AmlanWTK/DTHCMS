import { ApiError, writing } from '@dthcms/api-client';
import type { components } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';

/**
 * The background queue, typed against the contract (CP69, ADR-0031, §7.1, §8.5).
 *
 * # What the screen behind this is for
 *
 * Somebody opens it for one of two reasons: they suspect background work has stopped, or a
 * physician has said the AI summary was not ready when the patient sat down. Everything in
 * this module exists to make one of those two questions answerable in a glance, and four of
 * the rules below exist because the obvious rendering answers them wrongly while looking
 * entirely healthy.
 *
 * # 1. Every registered kind is a row, including the empty ones
 *
 * `GET /v1/ops/jobs/health` returns one row per **registered** kind whether or not anything
 * is queued, and that is the whole point of the catalogue (ADR-0031's first consequence).
 * There is deliberately no filter in this module that drops a quiet row, and
 * `jobs.test.tsx` has a named test that renders a catalogue where most kinds have nothing
 * in flight and asserts every one of them is on screen. A dashboard assembled from what is
 * currently in the queue cannot report the failure it exists to catch — a job type that has
 * stopped being enqueued at all is, on such a dashboard, simply absent.
 *
 * # 2. A null rate is not a zero, and it is not healthy either
 *
 * `failure_rate` and `sla_attainment` are **null when nothing finished in the window**. "No
 * failures" and "nothing ran" are different states. There is no `rateOr(0)` here and there
 * must never be one: `rateIsAvailable` is the only predicate, and it forces a caller to
 * decide what to say rather than defaulting into the healthiest-looking number on the page.
 *
 * `seconds_since_last_finish: -1` means the kind has **never finished anything**, which is
 * the single most interesting thing a row can say — `NEVER_FINISHED` and `hasEverFinished`
 * name it so it cannot be rendered as "-1 seconds ago".
 *
 * # 3. Age is the alarm, not depth — and there are two ages
 *
 * A queue of two that has not moved in an hour is worse than a queue of four hundred that
 * is draining, so `severityOf` keys "stuck" off age and never off `available`. Which age is
 * the distinction the payload now draws and the screen has to keep:
 *
 *   `oldest_available_seconds`  total age, from `enqueued_at`. The honest number, and the
 *                               one a person reads — "waiting 38 minutes" is true and worth
 *                               knowing even when 30 of those were a deliberate backoff.
 *   `oldest_due_seconds`        from `run_at`, over jobs actually due. *Is there work nobody
 *                               is doing right now.* This is what severity is computed from.
 *
 * Driving the alarm off total age would light a row up for one failing SMS on a kind with a
 * half-hour backoff cap, while the retry policy behaved exactly as designed — and an alarm
 * that is usually wrong is one people stop reading. Showing only the due age would be the
 * opposite mistake: it hides that a job has existed for half an hour. So the row draws both,
 * and says which is which whenever they differ.
 *
 * The threshold is the kind's **own** SLA where it has one — a `clinical.synthesis` job due
 * six minutes ago has already broken §7.1's promise, and a general threshold would notice
 * minutes after the promise did.
 *
 * # 4. A paused kind reads as healthy if you only read the numbers
 *
 * Paused work is work that has stopped happening on purpose, and its row is a row with an
 * empty queue and no failures — which is what a perfectly healthy idle kind looks like. So
 * `paused` outranks everything in `severityOf`, including "stuck": an operator who has
 * paused `patient.sms` during an incident and forgotten needs the screen to say so before
 * it says anything else. The row also **names who paused it and when**, from `paused_at`
 * and `paused_by_name_*`: a screen that can say a kind is stopped and cannot say who
 * stopped it leaves the log as the only place that person is findable.
 *
 * # 5. A dead letter from last night is still a dead letter this morning
 *
 * `discarded` is windowed and `dead_letters` is not, and they answer different questions.
 * A kind can honestly read "0% given up on, 0 of 40 finished in the last hour" while forty
 * jobs sit waiting for somebody to press retry — so `dead_letters` is drawn on the row, and
 * a row carrying any is a row that needs a person whatever the window says.
 *
 * # There is deliberately no enqueue
 *
 * The API has no endpoint for it and this module has no function for it. Work is enqueued
 * by the code that decided it was needed, inside the transaction that made that decision
 * true (ADR-0031, criterion 1), and a console button that accepted a kind and a payload
 * would be the way around it that somebody eventually used because it was convenient.
 */

export type JobKind = components['schemas']['JobKind'];
export type JobQueueHealth = components['schemas']['JobQueueHealth'];
export type Job = components['schemas']['Job'];
export type JobAttempt = components['schemas']['JobAttempt'];
export type JobStatus = Job['status'];

/**
 * The four lists this console offers, and why it is a view rather than a status.
 *
 * Three of them are statuses — what is waiting, what is running, what has been given up on.
 * The fourth is not, and that is the point: **`MISSED` crosses statuses.** A job that missed
 * §7.1's deadline succeeded, so it is not among the dead letters, and it is finished, so it
 * is not waiting; it is reachable only by asking the endpoint for `sla=missed` with no status
 * at all. Modelling the section as a status would have made the one number this checkpoint
 * exists to prove the only figure on the page with nothing behind it — which is exactly what
 * it was, until the filter existed.
 *
 * `SUCCEEDED` and `CANCELLED` are still not offered. This page is about what is wrong and
 * what to do about it, and an unfiltered list of everything that worked is a report rather
 * than a surface somebody works. Narrowed as a type rather than left open, so adding a fifth
 * is a decision with a name on it.
 */
export type JobView = 'AVAILABLE' | 'RUNNING' | 'DISCARDED' | 'MISSED';
export type JobClass = JobKind['job_class'];

/** What the health endpoint answers with. The window travels on the payload — see below. */
export interface QueueHealth {
  queues: JobQueueHealth[];
  /**
   * The window the **server** used, and the field every sentence on the screen is rendered
   * from.
   *
   * An out-of-range window is now refused with `422` against `window_seconds` rather than
   * quietly replaced with an hour, which is the right call and removes the sharp edge this
   * field was originally guarding. Reading the caption from the response rather than from
   * the request is kept anyway, and not out of habit: it costs nothing, it is the only
   * form that stays true if the server ever answers a different window than it was asked
   * for, and the failure it prevents — correct arithmetic under a false heading — is one
   * nobody would notice.
   */
  window_seconds: number;
  as_of: string;
}

/* ------------------------------------------------------------------------- */
/* Cache keys                                                                 */
/* ------------------------------------------------------------------------- */

/**
 * Everything under one `jobs` prefix.
 *
 * No realtime topic invalidates any of it: the queue publishes nothing to the gateway, and
 * a socket for it would be a channel whose only subscriber is a screen that already polls.
 * So this feature refreshes on a timer and on the acts an operator performs, and the
 * prefix is what a retry or a pause invalidates — a retried job leaves the dead-letter
 * list and changes its kind's depth in the same instant, and the two must not disagree.
 */
export const JOBS_KEY = ['jobs'] as const;

export function healthKey(windowSeconds: number) {
  // The window is part of the key. "The last hour" and "the last day" are different
  // answers to different questions, and a cache that treated them as one would show
  // yesterday's failure rate under this hour's heading.
  return ['jobs', 'health', windowSeconds] as const;
}

export const KINDS_KEY = ['jobs', 'kinds'] as const;

export function jobListKey(view: JobView, kind: string | null) {
  return ['jobs', 'list', view, kind ?? 'every-kind'] as const;
}

export function jobKey(id: string) {
  return ['jobs', 'job', id] as const;
}

/**
 * How often this page re-reads, and it is one number on purpose.
 *
 * Fifteen seconds, the same as the traffic board and the alert board, and for the same
 * reason: this is a screen somebody leaves open on a second monitor during an incident and
 * does not reload. Short enough that a queue draining is visible as it happens; long enough
 * that nine `count(*)`s over the job table are not run four times a second by every console
 * in the building.
 *
 * **The same number for the health table and for the lists beneath it.** A row that says
 * "3 jobs are waiting for somebody" sits directly above the three jobs, and a page where
 * only one of the two refreshed would show a count of 2 over a list of 3 the moment a
 * colleague retried one — one screen disagreeing with itself about the thing it exists to
 * show. Two intervals would be two answers to "when was this true".
 */
export const REFRESH_MS = 15_000;

/* ------------------------------------------------------------------------- */
/* The window                                                                 */
/* ------------------------------------------------------------------------- */

/**
 * The windows the screen offers, and the one it opens on.
 *
 * An hour by default because that is the server's own default and its reason is good: long
 * enough that a quiet queue still has something in it, short enough that a failure rate
 * reflects what is happening now rather than what happened at the morning rush. Fifteen
 * minutes is for working an incident; six hours and a day are for "has this been happening
 * since last night", which is the question somebody asks the morning after.
 */
export const WINDOW_CHOICES = [900, 3600, 21600, 86400] as const;
export const DEFAULT_WINDOW_SECONDS = 3600;

/**
 * The range the server accepts, mirrored so a control cannot offer something outside it.
 *
 * Not a second opinion about the rule — the server still decides, and now decides by
 * refusing with `422` against `window_seconds` rather than clamping. The mirror is so that
 * this screen never builds a request it knows will be refused: a `422` reaching the page is
 * then a real disagreement with the server rather than a button doing what it was built to
 * do. Same arrangement, and the same reasoning, as the quality feature's day window.
 */
export const MIN_WINDOW_SECONDS = 60;
export const MAX_WINDOW_SECONDS = 86_400;

export function windowIsAcceptable(seconds: number): boolean {
  return (
    Number.isInteger(seconds) && seconds >= MIN_WINDOW_SECONDS && seconds <= MAX_WINDOW_SECONDS
  );
}

/* ------------------------------------------------------------------------- */
/* Reads                                                                      */
/* ------------------------------------------------------------------------- */

/** Depth, age, failures and SLA attainment, one row per registered kind. `ops.jobs.read`. */
export async function getQueueHealth(windowSeconds: number): Promise<QueueHealth> {
  return unwrap(
    api.GET('/v1/ops/jobs/health', {
      params: { query: { window_seconds: windowSeconds } },
    }),
  );
}

/**
 * The catalogue: §8.5's table as data.
 *
 * Read **beside** the health rows rather than instead of them, because the two payloads
 * carry different halves of one row. `JobQueueHealth` has the numbers; `JobKind` has the
 * bilingual description and the retry policy. A screen that showed only the health rows
 * would name each kind by its dotted identifier and nothing else, which is fine for whoever
 * wrote the code and useless to whoever is on the floor at nine on a Thursday.
 *
 * The pause is on **both** payloads now, so the health table names who stopped a kind
 * without waiting for this request to land. That redundancy is deliberate on the server's
 * side and this module does not try to reconcile it: the health row is what the table
 * draws, because a table whose pause notice appeared a beat after the rest of the row is a
 * table that flickers during the one incident it exists for.
 */
export async function listKinds(): Promise<JobKind[]> {
  const body = await unwrap(api.GET('/v1/ops/jobs/kinds'));
  return body.kinds;
}

/**
 * One of the four views, newest first. `ops.jobs.read`.
 *
 * `DISCARDED` is what this list is usually opened for, and note what that filter is *not*
 * scoped by: the dead-letter list is every job still given up on, while the `discarded`
 * count on a health row covers only the chosen window. The row's own `dead_letters` is the
 * un-windowed number the table draws beside the rate; this list is the same set with the
 * jobs themselves in it.
 *
 * `MISSED` sends **`sla=missed` and no status**, deliberately. A job that missed its
 * deadline may have succeeded or been given up on, and narrowing by either would drop half
 * of the answer to "which eight missed" — the question a physician's complaint turns into.
 */
export async function listJobs(
  view: JobView,
  options: { kind?: string | null; limit?: number } = {},
): Promise<Job[]> {
  const body = await unwrap(
    api.GET('/v1/ops/jobs', {
      params: {
        query: {
          ...(view === 'MISSED' ? { sla: 'missed' as const } : { status: view }),
          ...(options.kind ? { kind: options.kind } : {}),
          ...(options.limit === undefined ? {} : { limit: options.limit }),
        },
      },
    }),
  );
  return body.jobs;
}

/**
 * One job with every attempt it made. `ops.jobs.read`.
 *
 * The attempts come with it rather than behind a second request, because somebody opening
 * a dead-lettered job is opening it to read the errors.
 */
export async function getJob(id: string): Promise<Job> {
  const body = await unwrap(api.GET('/v1/ops/jobs/{id}', { params: { path: { id } } }));
  return body.job;
}

/* ------------------------------------------------------------------------- */
/* The two controls                                                           */
/* ------------------------------------------------------------------------- */

/**
 * Put a dead-lettered job back on the queue. `ops.jobs.manage`.
 *
 * The attempt count is reset server-side, so the retry policy applies again from the
 * start: somebody retrying a job that failed five times means "try again", not "try once
 * more".
 *
 * # Why the response body is still discarded
 *
 * It is no longer because the body is wrong. `POST /v1/ops/jobs/{id}/retry` used to answer
 * with a `Job` assembled from the `UPDATE ... RETURNING` clause, so most of the contract's
 * required fields serialised as Go zero values — `args: null`, `enqueued_at` in the year
 * one, attempt 0 of 0. The handler now re-reads the row, and the body is the whole job.
 *
 * What the body still cannot describe is everything **else** a retry moves: the
 * dead-letter list it just left, its kind's depth, and the count of kinds needing a look
 * above the table. Three of the four reads on this page change, and the response knows
 * about one of them. So the caller refetches the whole `jobs` prefix and this returns
 * nothing — one path produces what the screen shows, which is the same discipline the
 * realtime layer follows for the same reason.
 */
export async function retryJob(id: string): Promise<void> {
  await unwrap(api.POST('/v1/ops/jobs/{id}/retry', { params: { ...writing(), path: { id } } }));
}

/**
 * Stop a job that has not started, and say why. `ops.jobs.manage`.
 *
 * The reason is required — the server answers `422` against `reason` for an empty one, and
 * a cancelled job that says nothing is a gap somebody has to explain later. Checked before
 * the request rather than after it, because that refusal costs a round trip on a clinic's
 * connection while somebody is waiting.
 *
 * A **running** job is not cancellable and answers `409 JOB_NOT_CANCELLABLE`: the worker
 * holding it would carry on regardless, and a status saying otherwise would be a lie on a
 * screen.
 */
export async function cancelJob(id: string, reason: string): Promise<void> {
  await unwrap(
    api.POST('/v1/ops/jobs/{id}/cancel', {
      params: { ...writing(), path: { id } },
      body: { reason: reason.trim() },
    }),
  );
}

/** Whether the server will accept this cancellation. The reason is the whole check. */
export function cancelReasonReady(reason: string): boolean {
  return reason.trim() !== '';
}

/**
 * Stop a kind being claimed. `ops.jobs.manage`.
 *
 * Exists so that stopping a misbehaving job type during an incident is a button rather
 * than a deploy. It does **not** consume what is already queued: those jobs stay
 * `AVAILABLE` and are skipped rather than claimed-and-requeued, so resuming picks up
 * exactly what was waiting — which is why the screen keeps drawing the depth of a paused
 * kind rather than blanking it.
 */
export async function pauseKind(kind: string): Promise<JobKind> {
  const body = await unwrap(
    api.POST('/v1/ops/jobs/kinds/{kind}/pause', { params: { ...writing(), path: { kind } } }),
  );
  return body.kind;
}

export async function resumeKind(kind: string): Promise<JobKind> {
  const body = await unwrap(
    api.POST('/v1/ops/jobs/kinds/{kind}/resume', { params: { ...writing(), path: { kind } } }),
  );
  return body.kind;
}

/* ------------------------------------------------------------------------- */
/* The four conflicts                                                         */
/* ------------------------------------------------------------------------- */

/**
 * The `409`s, which all mean the same thing about the world.
 *
 * Every one of them is "somebody else changed this while you were looking" — a colleague
 * retried the job thirty seconds ago, the worker picked it up, another operator paused the
 * kind from the next desk. None is a failure to report and none is worth a retry: the
 * screen is out of date, and the honest response is to refetch and say what happened.
 *
 * Branched on by **code** rather than by status, because `409` also carries
 * `IDEMPOTENCY_KEY_REUSED` and `IDEMPOTENCY_IN_PROGRESS` from the layer above, and those
 * are client bugs rather than a colleague working the same queue.
 */
export const CONFLICT_CODES = [
  'JOB_NOT_RETRYABLE',
  'JOB_NOT_CANCELLABLE',
  'JOB_KIND_ALREADY_PAUSED',
  'JOB_KIND_NOT_PAUSED',
] as const;

export type ConflictCode = (typeof CONFLICT_CODES)[number];

/** Which of the four this refusal is, or null if it is an ordinary failure. */
export function conflictOf(error: unknown): ConflictCode | null {
  if (!(error instanceof ApiError)) return null;
  return (CONFLICT_CODES as readonly string[]).includes(error.code)
    ? (error.code as ConflictCode)
    : null;
}

/* ------------------------------------------------------------------------- */
/* Reading a row                                                              */
/* ------------------------------------------------------------------------- */

/** `seconds_since_last_finish` when the kind has never finished anything. */
export const NEVER_FINISHED = -1;

/** Whether this kind has ever finished a job. False is the loudest thing a row can say. */
export function hasEverFinished(row: JobQueueHealth): boolean {
  return row.seconds_since_last_finish !== NEVER_FINISHED;
}

/**
 * Whether the server could put a number on this window at all.
 *
 * The only predicate about a rate, and there is deliberately no companion that substitutes
 * a zero. Both rates are null when nothing finished, and a screen that reads `false` here
 * says "nothing ran" in words rather than drawing 0%.
 */
export function rateIsAvailable(rate: number | null | undefined): boolean {
  return rate !== null && rate !== undefined;
}

/** Whether this kind carries a deadline at all. Most do not; §7.1's synthesis does. */
export function hasDeadline(row: Pick<JobQueueHealth, 'sla_seconds'>): boolean {
  return row.sla_seconds !== null && row.sla_seconds !== undefined;
}

/**
 * How many finished jobs missed the deadline their kind carries, over the window.
 *
 * The complement of `sla_met` rather than a field of its own, and it is the number the
 * attainment cell hangs its way-through off: a percentage with nothing behind it is
 * asserted to its reader however carefully it was measured.
 */
export function missedDeadlines(row: JobQueueHealth): number {
  return Math.max(0, row.sla_measured - row.sla_met);
}

/** Jobs given up on and still waiting for a person. Not windowed — see the header. */
export function hasDeadLetters(row: Pick<JobQueueHealth, 'dead_letters'>): boolean {
  return row.dead_letters > 0;
}

/**
 * Whether some of what is waiting is waiting *on purpose*.
 *
 * True when the oldest job has existed longer than anything is actually due for — which is
 * what a retry backoff looks like from outside. The row says so in words when this holds,
 * because "waiting 38 minutes" and "nothing due yet" are both true at once and a reader
 * given only the first would go looking for a wedged worker that does not exist.
 */
export function someWaitingIsDeliberate(row: JobQueueHealth): boolean {
  return row.available > 0 && row.oldest_due_seconds < row.oldest_available_seconds;
}

/**
 * How long work may be *due* before the row is called stuck.
 *
 * The kind's own SLA where it has one: a `clinical.synthesis` job due five minutes ago has
 * already broken §7.1's promise, and a fixed threshold would notice minutes after the
 * promise did. For the kinds with no deadline — most of them — fifteen minutes, which is
 * short enough that a wedged worker is caught inside a clinic session.
 *
 * Measured against `oldest_due_seconds`, never against total age. A job sitting out an
 * exponential backoff is waiting on purpose, and one failing SMS on a kind with a half-hour
 * cap would otherwise light this up while the retry policy behaved exactly as designed —
 * the arithmetic would be right and the alarm would be wrong, which is the shape of an
 * alarm people learn to ignore.
 */
export const DEFAULT_STALL_SECONDS = 900;

export function stallThresholdFor(row: JobQueueHealth): number {
  return row.sla_seconds ?? DEFAULT_STALL_SECONDS;
}

/**
 * The one word a row gets, and the order is the argument.
 *
 * A row can be several of these at once — a paused kind with a stuck queue and last hour's
 * failures is all three — and a screen that drew every fact as its own badge would be a
 * screen nobody scans. So there is one state per row, chosen by which fact an operator has
 * to act on first, and the rest of the row still carries the numbers.
 *
 *  - `paused`     first, always. It is the one state that looks identical to a healthy idle
 *                 queue if you read only the numbers, and it is the one somebody did on
 *                 purpose and may have forgotten.
 *  - `stuck`      **due** age — not depth, and not total age. Work is due and nothing is
 *                 taking it.
 *  - `failing`    jobs finished in the window and some were given up on.
 *  - `abandoned`  jobs given up on are still sitting there, whatever the window says.
 *                 Deliberately not folded into `failing`: the two ask for different acts.
 *                 `failing` is *find out what is going wrong now*; this is *somebody press
 *                 retry*, and it is the state a kind is left in for days after the incident
 *                 that caused it has been fixed and forgotten.
 *  - `late`       jobs met their deadline less than always. §7.1 as a number.
 *  - `never`      registered, and has never finished anything. On a clinic that has been
 *                 running for months this means a job type that is not wired up.
 *  - `quiet`      nothing queued, nothing running, nothing finished in the window. **Not
 *                 healthy** — this is the shape of a stopped queue as well as of a quiet
 *                 one, and the row says so rather than choosing for the reader.
 *  - `working`    something is in flight and nothing above is true.
 *  - `healthy`    finished work in the window, all of it, on time.
 */
export type QueueState =
  'paused' | 'stuck' | 'failing' | 'abandoned' | 'late' | 'never' | 'quiet' | 'working' | 'healthy';

export function severityOf(row: JobQueueHealth): QueueState {
  if (row.paused) return 'paused';
  // Due age, never total age — see stallThresholdFor. `available > 0` is belt and braces:
  // the server reports zero due seconds when nothing is due, and a row with an empty queue
  // that somehow reported otherwise should not be called stuck.
  if (row.available > 0 && row.oldest_due_seconds >= stallThresholdFor(row)) return 'stuck';
  if (rateIsAvailable(row.failure_rate) && (row.failure_rate ?? 0) > 0) return 'failing';
  if (hasDeadLetters(row)) return 'abandoned';
  if (rateIsAvailable(row.sla_attainment) && (row.sla_attainment ?? 100) < 100) return 'late';
  if (!hasEverFinished(row)) return 'never';
  if (row.available === 0 && row.running === 0 && !rateIsAvailable(row.failure_rate))
    return 'quiet';
  if (row.available > 0 || row.running > 0) return 'working';
  return 'healthy';
}

/**
 * The states that count towards the sentence above the table.
 *
 * `never` is in it, and that is the disagreement this screen has with an ordinary dashboard:
 * a job type that has never finished anything, on a clinic that has been running for months,
 * is a job type nobody wired up — and it is invisible on any dashboard built from what is
 * currently queued.
 *
 * `abandoned` is in it for a plainer reason: those jobs will not run again until a person
 * presses retry, and nothing else on this page or in the metrics will raise its hand for
 * them once the incident that caused them is over.
 *
 * **`quiet` is deliberately not in it**, and that is a judgement rather than an oversight.
 * `maintenance.ledger_verify` runs on a six-hourly schedule and is therefore quiet for most
 * of every hour-long window; an alarm that fired on it would fire five times out of six, and
 * an alarm that is usually wrong is an alarm people learn to dismiss without reading — taking
 * the real ones with it. So a quiet row is not counted here and is still not drawn as
 * healthy: it is grey, it says *nothing finished* in words, and a reader deciding whether
 * that is normal for this kind has both the schedule and the last-finish age in front of
 * them.
 */
const NEEDS_ATTENTION: readonly QueueState[] = [
  'paused',
  'stuck',
  'failing',
  'abandoned',
  'late',
  'never',
];

export function needsAttention(row: JobQueueHealth): boolean {
  return NEEDS_ATTENTION.includes(severityOf(row));
}

/** How loud each state is, for ordering. Higher is louder. */
const LOUDNESS: Record<QueueState, number> = {
  stuck: 8,
  failing: 7,
  abandoned: 6,
  late: 5,
  paused: 4,
  never: 3,
  quiet: 2,
  working: 1,
  healthy: 0,
};

/**
 * The rows in the order an operator should read them.
 *
 * Loudest first, ties broken by the age of the oldest waiting job and then by name so the
 * table does not shuffle between two-second refreshes. `paused` sorts below the three
 * states that mean something is going wrong now — it is the first thing to *say* about a
 * row and not the first row to read, because a kind somebody paused deliberately is a
 * known state and a stuck queue is not.
 */
export function rowsInOrder(rows: readonly JobQueueHealth[]): JobQueueHealth[] {
  return [...rows].sort(
    (a, b) =>
      LOUDNESS[severityOf(b)] - LOUDNESS[severityOf(a)] ||
      // Due age breaks the tie, for the same reason it decides the state: between two stuck
      // rows, the one with work overdue longest is the one nobody is doing anything about.
      b.oldest_due_seconds - a.oldest_due_seconds ||
      b.oldest_available_seconds - a.oldest_available_seconds ||
      a.kind.localeCompare(b.kind),
  );
}

/* ------------------------------------------------------------------------- */
/* Durations, as parts a message file turns into a sentence                   */
/* ------------------------------------------------------------------------- */

export type AgeUnit = 'seconds' | 'minutes' | 'hours' | 'days';

export interface AgeParts {
  unit: AgeUnit;
  value: number;
}

/**
 * A number of seconds as the largest unit that still says something useful.
 *
 * Parts rather than a formatted string, because the sentence is the message file's job in
 * both languages and a number joined to a word here would be an English word order baked
 * into a helper. Rounded down: "waiting 2 hours" while it is 2 hours 50 minutes understates
 * by less than rounding up overstates, and an age on this screen is read as "at least".
 */
export function ageParts(seconds: number): AgeParts {
  const safe = Math.max(0, Math.floor(seconds));
  if (safe < 60) return { unit: 'seconds', value: safe };
  if (safe < 3600) return { unit: 'minutes', value: Math.floor(safe / 60) };
  if (safe < 86_400) return { unit: 'hours', value: Math.floor(safe / 3600) };
  return { unit: 'days', value: Math.floor(safe / 86_400) };
}

export type RanForUnit = 'ms' | 'seconds' | 'minutes';

export interface RanForParts {
  unit: RanForUnit;
  value: number;
}

/**
 * How long an attempt ran before it failed.
 *
 * Kept separate from `ageParts` because the interesting range is different and the
 * distinction is diagnostic: a job that fails in 30 ms failed before it did anything, and
 * one that fails after four minutes failed part-way through something. Milliseconds are
 * therefore not rounded away below a second.
 */
export function ranForParts(ms: number): RanForParts {
  const safe = Math.max(0, Math.floor(ms));
  if (safe < 1000) return { unit: 'ms', value: safe };
  if (safe < 60_000) return { unit: 'seconds', value: Math.round(safe / 100) / 10 };
  return { unit: 'minutes', value: Math.round(safe / 6000) / 10 };
}
