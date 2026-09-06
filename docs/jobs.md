# The background work queue

CP69. The job system behind §7.1's five-minute promise, with the promise measured rather than
asserted.

---

## Why this is a table and not River

The checkpoint asks for River. [ADR-0031](adr/0031-the-job-queue-is-a-table-in-this-database-not-river.md)
has the full argument; the short version is that §8.4's stated reason for River —

> no dual-write between the database and a broker (D-32)

— is a property of being **Postgres-backed**, and a table in this database is that property. What a
framework would additionally have brought is its own opaque `args` column, and that is the one thing
this checkpoint cannot afford: _"job payloads may reference but must not embed PHI"_ is a rule this
system enforces with a check constraint and an invariant, not with a convention, and neither
`dthclint` nor a database constraint can see inside a framework's serialised struct.

The ADR also states what this costs — at-least-once delivery, no cron expressions, one backoff
policy — because a decision whose costs are not written down is one that gets re-argued badly later.

## The four properties

### 1. A job enqueued in a rolled-back transaction never runs

It is a row. There is nothing to roll back separately.

More usefully: **there is no enqueue that opens its own transaction.** `Store.EnqueueTx` takes the
caller's `pgx.Tx` and that is the whole API. A convenience helper would be the one call site that
quietly broke the guarantee, and it would be reached for exactly when somebody was in a hurry.

There is no HTTP endpoint that enqueues, either, for the same reason.

### 2. A failed job retries per policy, then dead-letters visibly

The policy is the kind's, from §8.5's table: five attempts for clinical-critical, eight for
patient-facing, three for pipeline. Backoff is exponential from the kind's base, capped, **with
jitter** — without it, twenty jobs that failed together because one dependency was down retry
together, hit it at the same instant, and fail together again.

Every failure is kept in `ops.job_attempt` with what it said and how long it ran, because "it failed
five times" and "here is what it said each time" are different questions and the second is the one
asked at the point of fixing it. A discarded job with an empty `last_error` is refused by a check
constraint: a dead letter nobody can act on defeats the point of making dead-lettering visible.

A retry from the operator's screen **resets the attempt count**. Somebody retrying a job that failed
five times means "try again", not "try once more".

### 3. The SLA is measured

A kind's `sla_seconds` becomes a `sla_deadline` on the row at enqueue, and a `met_sla` boolean at
completion. Stamped rather than computed on read, so changing a kind's budget does not retroactively
rewrite whether last week was met. Invariant 92 refuses a finished job that carried a deadline
nobody resolved — without it, a kind could carry an SLA and report 100% attainment because nothing
ever recorded a miss.

`clinical.synthesis` is the only kind with a deadline today: §7.1's five minutes.

### 4. A worker restart causes no duplicate _effect_

The honest form of the claim. An expired lease returns the job to the queue, so a worker that
finished the work and died before marking the row runs that job again. **No queue that is not
writing its completion inside the work's own transaction can do better**, which is why §8.5 requires
every job to be idempotent and why the handler contract says so in as many words.

A lease rather than a lock, because a lock dies with the connection that took it and the case this
has to survive is a worker that stopped _answering_ rather than one that disconnected politely. A
heartbeat extends the lease while work runs, so a job legitimately taking longer than one lease is
not reaped out from under a healthy worker — the difference between at-least-once because a worker
died and at-least-once because we were impatient.

A job whose attempts are spent is discarded rather than requeued: otherwise one poisonous job would
take the queue down with it, over and over, forever.

## Depth is the second question. Age is the first.

A queue of four hundred that is draining is healthy. A queue of two that has not moved in an hour is
not. So the number the alert reads is **age**, and specifically `oldest_due_seconds`:

| Column                     | Measured from                    | Answers                                      |
| -------------------------- | -------------------------------- | -------------------------------------------- |
| `oldest_available_seconds` | `enqueued_at`                    | How long has the oldest waiting job existed? |
| `oldest_due_seconds`       | `run_at`, over jobs that are due | Is there work nobody is doing **right now**? |

The difference matters because a job sitting out an exponential backoff is waiting **on purpose**.
One failing SMS on a kind with a half-hour cap would otherwise light an alert while the retry policy
behaved exactly as designed.

## Three numbers that are deliberately not zero

- **`failure_rate` and `sla_attainment` are null when nothing finished in the window.** "No failures"
  and "nothing ran" are different states, and a dashboard showing 0% for both hides a stopped queue
  behind the healthiest-looking number on the page. The metric is _absent_ rather than zero for the
  same reason: a gauge reporting 0% attainment on a quiet night would fire the alert every night,
  and an alert that fires every night is one people turn off.
- **`seconds_since_last_finish` is -1 for "never".** A kind that has never finished anything is the
  single most interesting thing a row can say.
- **`dead_letters` is not windowed.** A dead letter from last night is still a dead letter this
  morning. A kind can honestly read "0% given up on, 0 of 40 finished in the last hour" while forty
  dead letters wait for somebody, and a reader who saw only one of those numbers would conclude the
  screen was lying.

## Every registered kind is a row

`ops.job_kind` is a catalogue, not free strings, and the queue-health query is
`job_kind LEFT JOIN job`. The left join is the point: a dashboard assembled from what is currently in
the queue cannot report the failure it exists to catch — a job type that has **stopped being
enqueued at all**. Synthesis silently not running is exactly what §7.1 is worried about, and a page
that rendered an empty list would report it as health.

The catalogue is also why `Registry.Check` can fail at startup rather than at three in the morning:
a handler for a kind the database does not know is a typo whose job would never be claimed, and a
kind whose queue this worker serves with no handler is work that is enqueued, waits, and is never
done. Serving a _subset_ of kinds is fine — that is what queues are for — so the check is against
the queues this worker actually claims.

## Pausing

An operator can stop a kind without a deploy, because the alternative during an incident is a
deploy. Pausing does not consume what is queued: those jobs stay `AVAILABLE` and are skipped rather
than claimed-and-requeued, so resuming picks up exactly what was waiting.

It is attributed, and the attribution is **shown** as well as recorded — a screen that can say a
kind is stopped and cannot say who stopped it leaves the log as the only place that person is
findable. `dthcms_job_kind_paused` is also its own metric and its own alert, because a paused queue
and a healthy idle queue produce identical depth and age numbers: pausing is the one way to make
work stop happening without anything looking wrong.

## PHI

`ops.job.args` may reference but must not embed. Enforced three times, which is one more than
elsewhere in this system and deliberately so — a job argument is written by one module, stored for
hours, and read by another process:

1. **In Go**, so the refusal names the key. A constraint violation says `job_args_carry_no_phi`,
   which tells a developer they broke a rule and not which word broke it.
2. **By a check constraint**, so a second application cannot skip the Go check.
3. **By invariant 90**, which re-checks the whole table — a constraint added later does not validate
   what is already there.

All three read one list. `logging.PHIKeys` had been the single source of truth, enforced statically
by `dthclint` and dynamically by the log handler; a constraint cannot read a Go map, so the list is
now `ops.phi_key` as well, with a test comparing the two in both directions. Same treatment as the
permission catalogue and the event registry, for the same reason: two representations of one list
drift, and the drift is silent.

The check is recursive. `{"patient": {"name": "..."}}` hides it one level down, and a check that only
looked at the top level would pass exactly the payload somebody was most likely to write.

## The metrics, and what alerts on them

| Metric                                   | Alerts when                                            |
| ---------------------------------------- | ------------------------------------------------------ |
| `dthcms_job_queue_oldest_due_seconds`    | above 300s for 5m — work is due and nobody is doing it |
| `dthcms_job_sla_attainment`              | below 95% for 10m — §7.1's promise is being missed     |
| `dthcms_job_dead_letters`                | above 0 for 15m — jobs are waiting for a person        |
| `dthcms_job_completed{outcome="failed"}` | failure rate above 25% for 10m                         |
| `dthcms_job_kind_paused`                 | 1 for 30m — a pause has been forgotten                 |

The depth and age gauges are **observed from the table** rather than counted. A counter incremented
on enqueue and decremented on claim drifts: every dropped decrement — a crash, a reaped lease, a row
cancelled by hand — leaves it permanently wrong, slowly enough that nobody notices until the alert
it feeds is the broken thing.

No metric carries a patient, a user, or a job id. A job id in particular is how a metrics bill
becomes a surprise — one time series per job — and `dthclint`'s PHI check would not catch it, because
a uuid is not a banned word. `kind` and `queue` are bounded by the catalogue, which is the other
reason that catalogue is a table.

## The query, for an operator with psql

The queue-health SQL lives once, in `backend/internal/jobs/queries/jobs.sql`, generated and
type-checked against the schema. At three in the morning, this is the short version:

```sql
SELECT k.kind,
       count(*) FILTER (WHERE j.status = 'AVAILABLE')  AS waiting,
       count(*) FILTER (WHERE j.status = 'RUNNING')    AS running,
       count(*) FILTER (WHERE j.status = 'DISCARDED')  AS dead_letters,
       coalesce(max(CASE WHEN j.status = 'AVAILABLE' AND j.run_at <= now()
                         THEN extract(epoch FROM (now() - j.run_at))::int END), 0) AS oldest_due_s,
       k.paused_at
  FROM ops.job_kind k
  LEFT JOIN ops.job j ON j.kind = k.kind
 GROUP BY k.kind, k.paused_at, k.priority
 ORDER BY k.priority DESC, k.kind;
```

And the errors behind one dead letter:

```sql
SELECT attempt, failed_at, ran_for_ms, error
  FROM ops.job_attempt WHERE job_id = '…' ORDER BY attempt;
```

## Open decisions

- **The five alert thresholds.** Every one is a proposal. 300 seconds of due-age and 95% attainment
  are the two that will be argued about first, and both should be set from a fortnight of real
  traffic rather than from this document.
- **Which queues get their own workers.** Today one process serves all six. The separation exists so
  that a burst of document processing cannot slow down a clinician entering a blood pressure, and it
  becomes a deployment decision the day OCR arrives.
- **What `clinical.synthesis` does when it is going to miss.** The deadline is on the handler's
  `Running` struct precisely so that work which can degrade gracefully — a shorter summary — can see
  how much time is left. Whether it should is CP71's question and a clinical one.
