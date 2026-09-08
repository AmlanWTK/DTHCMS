-- The job queue (CP69).
--
-- # The one query that is not here
--
-- There is no `InsertJob` that opens its own transaction. `EnqueueTx` takes the caller's `pgx.Tx`,
-- because acceptance criterion 1 — *"a job enqueued in a rolled-back transaction never runs"* — is
-- a property of where the insert happens, and a convenience helper that opened its own connection
-- would be the one call site that quietly broke it.

-- name: EnqueueJob :one
-- The enqueue. Everything the row needs that is not the caller's business — priority, attempts,
-- backoff, the SLA deadline — comes from the kind's own row, so a caller cannot enqueue a job with
-- a retry policy nobody agreed.
--
-- `ON CONFLICT DO NOTHING` against the live-dedupe index makes enqueue-once free: a caller that
-- retries its own transaction, or two stations that both notice the same visit is ready, produce
-- one job. The `:one` returns nothing when it was absorbed, which the Go side reads as "already
-- queued" rather than as an error.
INSERT INTO ops.job (id, kind, queue, priority, args, dedupe_key, max_attempts, run_at,
                     enqueued_at, sla_deadline)
SELECT sqlc.arg(id)::uuid, k.kind, k.queue, k.priority,
       sqlc.arg(args)::jsonb, sqlc.narg(dedupe_key)::text, k.max_attempts,
       sqlc.arg(run_at)::timestamptz,
       -- `enqueued_at` comes from the caller's clock rather than from the column's `now()`
       -- default, for a reason that only shows up on the dashboard: `oldest_available_seconds` is
       -- the number an alert fires on, and it is computed as `now - enqueued_at`. If one of those
       -- comes from the application and the other from the database, clock skew between the two
       -- becomes queue age, and an alert threshold measured in minutes is sensitive to a skew of
       -- minutes. One clock decides both.
       sqlc.arg(now)::timestamptz,
       CASE WHEN k.sla_seconds IS NULL THEN NULL
            ELSE sqlc.arg(now)::timestamptz + make_interval(secs => k.sla_seconds) END
  FROM ops.job_kind k
 WHERE k.kind = sqlc.arg(kind)::text
ON CONFLICT DO NOTHING
RETURNING id, kind, queue, priority, status, attempt, max_attempts, run_at, enqueued_at, sla_deadline;

-- name: ClaimJobs :many
-- Take up to `row_limit` jobs, best first, and hold them for the lease.
--
-- `FOR UPDATE ... SKIP LOCKED` is the whole concurrency story: two workers running this at the same
-- moment take disjoint sets rather than blocking on each other, and a third arriving mid-statement
-- takes whatever neither has locked.
--
-- A paused kind is skipped rather than claimed-and-requeued, so a pause takes effect on the next
-- poll rather than after one more run of everything already in flight.
WITH claimable AS (
  SELECT j.id
    FROM ops.job j
    JOIN ops.job_kind k ON k.kind = j.kind
   WHERE j.status = 'AVAILABLE'
     AND j.run_at <= sqlc.arg(now)::timestamptz
     AND j.queue = ANY(sqlc.arg(queues)::text[])
     AND k.paused_at IS NULL
   ORDER BY j.priority DESC, j.run_at, j.enqueued_at
   LIMIT sqlc.arg(row_limit)::integer
   FOR UPDATE OF j SKIP LOCKED
)
UPDATE ops.job j
   SET status = 'RUNNING',
       attempt = j.attempt + 1,
       leased_by = sqlc.arg(worker)::text,
       leased_until = sqlc.arg(now)::timestamptz + make_interval(secs => sqlc.arg(lease_seconds)::integer),
       started_at = coalesce(j.started_at, sqlc.arg(now)::timestamptz)
  FROM claimable c
 WHERE j.id = c.id
RETURNING j.id, j.kind, j.args, j.attempt, j.max_attempts, j.enqueued_at, j.sla_deadline;

-- name: HeartbeatJob :exec
-- Extend a lease while the work is still running.
--
-- Without this a job legitimately taking longer than one lease would be reaped out from under a
-- healthy worker and run twice — which is the difference between "at-least-once because a worker
-- died" and "at-least-once because we were impatient".
UPDATE ops.job
   SET leased_until = sqlc.arg(now)::timestamptz + make_interval(secs => sqlc.arg(lease_seconds)::integer)
 WHERE id = sqlc.arg(id)::uuid AND status = 'RUNNING' AND leased_by = sqlc.arg(worker)::text;

-- name: CompleteJob :exec
-- Done. `met_sla` is resolved here rather than computed on read, so that changing a kind's budget
-- does not retroactively rewrite whether last week was met.
UPDATE ops.job
   SET status = 'SUCCEEDED',
       finished_at = sqlc.arg(now)::timestamptz,
       leased_by = NULL, leased_until = NULL,
       met_sla = CASE WHEN sla_deadline IS NULL THEN NULL
                      ELSE sqlc.arg(now)::timestamptz <= sla_deadline END
 WHERE id = sqlc.arg(id)::uuid AND status = 'RUNNING' AND leased_by = sqlc.arg(worker)::text;

-- name: FailJob :one
-- A failure: back to the queue with a backoff, or dead-lettered if the attempts are spent.
--
-- The decision is made here, in one statement, rather than by the worker reading the row and
-- writing it back. Two workers cannot both decide, and a worker that dies between the read and the
-- write cannot leave a job in a state neither branch produced.
UPDATE ops.job j
   SET status = CASE WHEN j.attempt >= j.max_attempts THEN 'DISCARDED' ELSE 'AVAILABLE' END,
       run_at = CASE WHEN j.attempt >= j.max_attempts THEN j.run_at
                     ELSE sqlc.arg(now)::timestamptz + make_interval(secs => sqlc.arg(backoff_seconds)::integer) END,
       finished_at = CASE WHEN j.attempt >= j.max_attempts THEN sqlc.arg(now)::timestamptz ELSE NULL END,
       met_sla = CASE WHEN j.attempt >= j.max_attempts AND j.sla_deadline IS NOT NULL
                      THEN false ELSE j.met_sla END,
       leased_by = NULL, leased_until = NULL,
       last_error = sqlc.arg(error)::text
 WHERE j.id = sqlc.arg(id)::uuid AND j.status = 'RUNNING' AND j.leased_by = sqlc.arg(worker)::text
RETURNING j.id, j.kind, j.attempt, j.max_attempts, j.status, j.run_at;

-- name: RecordAttempt :exec
-- Every failure, kept with what it said. "It failed five times" and "here is what it said each
-- time" are different questions, and the second is the one asked at the point of fixing it.
INSERT INTO ops.job_attempt (job_id, attempt, worker, failed_at, ran_for_ms, error)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (job_id, attempt) DO NOTHING;

-- name: ReapExpiredLeases :many
-- Return abandoned work to the queue. This is what criterion 5 rests on, and what it buys is
-- **at-least-once** delivery: a worker that finished the work and died before marking the row runs
-- that job again when its lease expires. No queue that is not writing its completion inside the
-- work's own transaction can do better, which is why §8.5 requires every job to be idempotent.
--
-- A job whose attempts are spent is discarded rather than requeued, or one poisonous job would take
-- the queue down with it, over and over, forever.
UPDATE ops.job j
   SET status = CASE WHEN j.attempt >= j.max_attempts THEN 'DISCARDED' ELSE 'AVAILABLE' END,
       leased_by = NULL,
       leased_until = NULL,
       run_at = CASE WHEN j.attempt >= j.max_attempts THEN j.run_at ELSE sqlc.arg(now)::timestamptz END,
       finished_at = CASE WHEN j.attempt >= j.max_attempts THEN sqlc.arg(now)::timestamptz ELSE NULL END,
       met_sla = CASE
                   WHEN j.attempt >= j.max_attempts AND j.sla_deadline IS NOT NULL
                   THEN false ELSE j.met_sla
                 END,
       last_error = CASE
                      WHEN btrim(j.last_error) <> '' THEN j.last_error
                      ELSE 'the worker holding this job stopped answering'
                    END
 WHERE j.status = 'RUNNING' AND j.leased_until < sqlc.arg(now)::timestamptz
RETURNING j.id, j.kind, j.attempt, (j.attempt >= j.max_attempts)::boolean AS discarded;

-- name: JobKinds :many
-- The registered catalogue. Read at boot to check that every kind has a handler and every handler
-- has a kind, which is the same fail-at-startup treatment the route registry gets.
--
-- The pause is **attributed on the way out as well as on the way in**. `paused_by` was being
-- recorded and never shown, so the one screen an operator opens during an incident could say a
-- kind was stopped and not who stopped it — leaving the log as the only place that person was
-- findable, which is not what "findable afterwards" means.
SELECT k.kind, k.job_class, k.queue, k.priority, k.max_attempts, k.backoff_seconds,
       k.backoff_cap_seconds, k.sla_seconds, k.description_en, k.description_bn,
       k.paused_at, k.paused_by,
       coalesce(u.employee_code, '') AS paused_by_code,
       coalesce(u.name_en, '')       AS paused_by_name_en,
       coalesce(u.name_bn, '')       AS paused_by_name_bn
  FROM ops.job_kind k
  LEFT JOIN core.app_user u ON u.id = k.paused_by
 ORDER BY k.priority DESC, k.kind;

-- name: JobHealth :many
-- Criterion 3, as one query.
--
-- **Every registered kind is a row**, whether or not anything is queued — the left join is the
-- point. A dashboard assembled from what is in the queue cannot report the failure it exists to
-- catch: a job type that has stopped being enqueued at all.
--
-- `oldest_available_seconds` is the number to alert on. Depth says how much there is; age says how
-- long the oldest thing has been ignored, and a queue of two that has not moved in an hour is a
-- worse state than a queue of four hundred that is draining.
--
-- The rates are **null rather than zero when nothing finished in the window**. "No failures" and
-- "nothing ran" are different states, and a dashboard showing 0% for both hides a stopped queue
-- behind the healthiest-looking number on the page.
SELECT k.kind, k.job_class, k.queue, (k.paused_at IS NOT NULL)::boolean AS paused,
       k.paused_at, coalesce(u.name_en, '') AS paused_by_name_en,
       coalesce(u.name_bn, '') AS paused_by_name_bn,
       k.sla_seconds,
       count(*) FILTER (WHERE j.status = 'AVAILABLE')::bigint AS available,
       count(*) FILTER (WHERE j.status = 'RUNNING')::bigint AS running,
       -- Seconds, so an alert threshold is a number a person writes without converting.
       -- Coalesced to zero, and unambiguous because `available` says whether anything is
       -- waiting at all: zero with an empty queue means nothing waiting, zero with a queue means
       -- something arrived this instant.
       coalesce(max(CASE WHEN j.status = 'AVAILABLE'
                         THEN extract(epoch FROM (sqlc.arg(now)::timestamptz - j.enqueued_at))::integer
                    END), 0)::integer AS oldest_available_seconds,
       -- **The number to alert on**, and different from the one above in a way that matters.
       --
       -- `oldest_available_seconds` is total age from enqueue, which counts a job that is sitting
       -- out an exponential backoff as "waiting" — so one failing SMS on a kind with a half-hour
       -- cap lights the row up while the retry policy behaves exactly as designed. This one
       -- measures from `run_at`, over jobs that are actually **due**, so it answers the question an
       -- alert is really asking: is there work nobody is doing right now.
       coalesce(max(CASE WHEN j.status = 'AVAILABLE' AND j.run_at <= sqlc.arg(now)::timestamptz
                         THEN extract(epoch FROM (sqlc.arg(now)::timestamptz - j.run_at))::integer
                    END), 0)::integer AS oldest_due_seconds,
       count(*) FILTER (WHERE j.status = 'SUCCEEDED' AND j.finished_at >= sqlc.arg(now)::timestamptz - make_interval(secs => sqlc.arg(window_seconds)::integer))::bigint AS succeeded,
       count(*) FILTER (WHERE j.status = 'DISCARDED' AND j.finished_at >= sqlc.arg(now)::timestamptz - make_interval(secs => sqlc.arg(window_seconds)::integer))::bigint AS discarded,
       count(*) FILTER (WHERE j.status = 'CANCELLED' AND j.finished_at >= sqlc.arg(now)::timestamptz - make_interval(secs => sqlc.arg(window_seconds)::integer))::bigint AS cancelled,
       -- **Not windowed**, and deliberately beside the windowed counts rather than instead of
       -- them. A kind can read "0% given up on, 0 of 40 finished" while forty dead letters from
       -- last night are still sitting there waiting for somebody — both true, and a reader who
       -- assumed one set would conclude the screen was lying.
       count(*) FILTER (WHERE j.status = 'DISCARDED')::bigint AS dead_letters,
       CASE
         WHEN count(*) FILTER (WHERE j.status IN ('SUCCEEDED', 'DISCARDED')
                                 AND j.finished_at >= sqlc.arg(now)::timestamptz - make_interval(secs => sqlc.arg(window_seconds)::integer)) = 0
         THEN NULL
         ELSE round(100.0
              * count(*) FILTER (WHERE j.status = 'DISCARDED' AND j.finished_at >= sqlc.arg(now)::timestamptz - make_interval(secs => sqlc.arg(window_seconds)::integer))
              / count(*) FILTER (WHERE j.status IN ('SUCCEEDED', 'DISCARDED')
                                   AND j.finished_at >= sqlc.arg(now)::timestamptz - make_interval(secs => sqlc.arg(window_seconds)::integer)), 1)
       END::numeric AS failure_rate,
       count(*) FILTER (WHERE j.met_sla IS NOT NULL AND j.finished_at >= sqlc.arg(now)::timestamptz - make_interval(secs => sqlc.arg(window_seconds)::integer))::bigint AS sla_measured,
       count(*) FILTER (WHERE j.met_sla AND j.finished_at >= sqlc.arg(now)::timestamptz - make_interval(secs => sqlc.arg(window_seconds)::integer))::bigint AS sla_met,
       CASE
         WHEN count(*) FILTER (WHERE j.met_sla IS NOT NULL AND j.finished_at >= sqlc.arg(now)::timestamptz - make_interval(secs => sqlc.arg(window_seconds)::integer)) = 0
         THEN NULL
         ELSE round(100.0
              * count(*) FILTER (WHERE j.met_sla AND j.finished_at >= sqlc.arg(now)::timestamptz - make_interval(secs => sqlc.arg(window_seconds)::integer))
              / count(*) FILTER (WHERE j.met_sla IS NOT NULL AND j.finished_at >= sqlc.arg(now)::timestamptz - make_interval(secs => sqlc.arg(window_seconds)::integer)), 1)
       END::numeric AS sla_attainment,
       -- Seconds since this kind last finished anything, and **-1 for "never"** rather than a
       -- null timestamp. A sentinel is usually the wrong answer; here it is the right one twice
       -- over: the generator will not infer nullability through an aggregate, and the number a
       -- dashboard actually renders is an age rather than a wall-clock time — "nothing for four
       -- hours" is the sentence, and a kind that has never run is the single most interesting
       -- thing this row can say.
       coalesce(
         extract(epoch FROM (sqlc.arg(now)::timestamptz - max(j.finished_at)))::integer,
         -1)::integer AS seconds_since_last_finish
  FROM ops.job_kind k
  LEFT JOIN core.app_user u ON u.id = k.paused_by
  LEFT JOIN ops.job j ON j.kind = k.kind
 GROUP BY k.kind, k.job_class, k.queue, k.paused_at, u.name_en, u.name_bn, k.sla_seconds, k.priority
 ORDER BY k.priority DESC, k.kind;

-- name: JobsByStatus :many
-- The operator's list. Ordered newest-relevant-first: what is waiting, then what has failed.
SELECT j.id, j.kind, j.queue, j.priority, j.args, j.dedupe_key, j.status,
       j.attempt, j.max_attempts, j.run_at, j.enqueued_at, j.started_at, j.finished_at,
       j.leased_by, j.leased_until, j.sla_deadline, j.met_sla, j.last_error,
       coalesce(k.description_en, j.kind) AS description_en,
       coalesce(k.description_bn, j.kind) AS description_bn
  FROM ops.job j
  LEFT JOIN ops.job_kind k ON k.kind = j.kind
 WHERE (sqlc.narg(status)::text IS NULL OR j.status = sqlc.narg(status)::text)
   AND (sqlc.narg(kind)::text IS NULL OR j.kind = sqlc.narg(kind)::text)
   -- The jobs behind the attainment figure. Without this, a page could report "63.6% on time,
   -- 14 of 22 met it" and offer no way to reach the eight that missed: they succeeded, so they
   -- are not dead letters, and they are finished, so they are not waiting. A number nobody can
   -- drill into is asserted to the reader however carefully it was measured.
   AND (sqlc.narg(met_sla)::boolean IS NULL OR j.met_sla = sqlc.narg(met_sla)::boolean)
 ORDER BY j.enqueued_at DESC
 LIMIT sqlc.arg(row_limit);

-- name: JobByID :one
SELECT j.id, j.kind, j.queue, j.priority, j.args, j.dedupe_key, j.status,
       j.attempt, j.max_attempts, j.run_at, j.enqueued_at, j.started_at, j.finished_at,
       j.leased_by, j.leased_until, j.sla_deadline, j.met_sla, j.last_error,
       coalesce(k.description_en, j.kind) AS description_en,
       coalesce(k.description_bn, j.kind) AS description_bn
  FROM ops.job j
  LEFT JOIN ops.job_kind k ON k.kind = j.kind
 WHERE j.id = $1;

-- name: AttemptsFor :many
SELECT job_id, attempt, worker, failed_at, ran_for_ms, error
  FROM ops.job_attempt
 WHERE job_id = $1
 ORDER BY attempt;

-- name: RetryJob :one
-- Put a dead-lettered job back, with its attempt count reset so the policy applies again from the
-- start. An operator retrying a job that failed five times means "try again", not "try once more".
UPDATE ops.job
   SET status = 'AVAILABLE', attempt = 0, finished_at = NULL, met_sla = NULL,
       run_at = sqlc.arg(now)::timestamptz, last_error = ''
 WHERE id = sqlc.arg(id)::uuid AND status = 'DISCARDED'
RETURNING id;

-- name: CancelJob :one
-- Stop a job that has not started. A RUNNING job is not cancellable from here: the worker holding
-- it would carry on regardless, and a status saying otherwise would be a lie on a screen.
UPDATE ops.job
   SET status = 'CANCELLED', finished_at = sqlc.arg(now)::timestamptz,
       last_error = sqlc.arg(reason)::text
 WHERE id = sqlc.arg(id)::uuid AND status = 'AVAILABLE'
RETURNING id;

-- name: PauseKind :one
UPDATE ops.job_kind
   SET paused_at = sqlc.arg(now)::timestamptz, paused_by = sqlc.arg(paused_by)::uuid
 WHERE kind = sqlc.arg(kind)::text AND paused_at IS NULL
RETURNING kind, paused_at;

-- name: ResumeKind :one
UPDATE ops.job_kind
   SET paused_at = NULL, paused_by = NULL
 WHERE kind = sqlc.arg(kind)::text AND paused_at IS NOT NULL
RETURNING kind, paused_at;

-- name: DueSchedules :many
-- Periodic jobs whose time has come, claimed so that two workers produce one enqueue.
--
-- `FOR UPDATE SKIP LOCKED` is the leader election. It matters more here than for ordinary jobs: a
-- periodic job has no natural dedupe key, and "run the nightly audit twice" is a real cost rather
-- than an absorbed duplicate.
UPDATE ops.job_schedule s
   SET last_run_at = sqlc.arg(now)::timestamptz,
       next_run_at = sqlc.arg(now)::timestamptz + make_interval(secs => s.every_seconds)
 WHERE s.kind IN (
   SELECT d.kind FROM ops.job_schedule d
    WHERE d.paused_at IS NULL AND d.next_run_at <= sqlc.arg(now)::timestamptz
    FOR UPDATE SKIP LOCKED)
RETURNING s.kind, s.args, s.next_run_at;

-- name: PHIKeys :many
-- The database's copy of the list, so a Go test can compare it against logging.PHIKeys in both
-- directions. Two representations of one list is a thing that drifts, and the drift is silent.
--
-- `class` joined the row at CP70 and is compared by the same test. It is what lets the AI gateway
-- refuse the identifier keys while still sending the clinical ones — a distinction the logging
-- rule does not need and the gateway cannot work without. Splitting the list in two would have
-- drifted in exactly the direction that leaks: a key added here and forgotten there.
SELECT key, guidance, class FROM ops.phi_key ORDER BY key;
