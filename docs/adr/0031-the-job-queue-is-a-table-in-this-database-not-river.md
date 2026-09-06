# ADR-0031 · The job queue is a table in this database, not River

**Status:** Accepted · **Date:** 5 Sep 2026 · **Checkpoints:** CP69 · **Amends:** §4's technology list and §8.4's parenthetical

## Context

CP69 asks for "River integration with **transactional enqueue**", and §8.4 gives the reason in one
sentence:

> Event append, projection update (synchronous ones), and job enqueue commit together. This is why
> River (Postgres-backed) was chosen: no dual-write between the database and a broker (D-32).

Read that sentence again with the emphasis where the argument actually sits. The property being
bought is **Postgres-backed**. The alternative being rejected is **a broker** — Redis Streams,
Pub/Sub, SQS — where enqueueing is a second write that can succeed while the transaction rolls
back, or fail while it commits. That is D-32's dual-write problem, and it is a real one: a job
that runs against a patient record that was never written is the worst kind of bug, because it
looks like data corruption rather than like a queue error.

River is one way to get the property. It is not the property.

## The forcing constraint, stated first

This environment's egress allowlist does not include the Go module proxy, so no new Go dependency
can be added here at all. That is a real constraint and it is not a justification: a decision made
because a tool could not be downloaded is a decision that should be revisited the moment it can
be. So the question was asked properly — _if River were installable, would we use it?_ — and the
answer below is no, for reasons that would hold on a machine with an open network.

## Decision

**`ops.job` is a table in this database, claimed with `FOR UPDATE SKIP LOCKED`, in the same shape
as every other durable thing here: a migration, a `queries/` file, a Go module with an entry in
`architecture.json`, and invariants that are checked by `migrate verify`.**

Four arguments, in the order they matter.

### 1. PHI

The checkpoint says _"Job payloads may reference but must not embed PHI"_, and this system does not
express rules like that as intentions. `logging.PHIKeys` is enforced twice — statically by
`dthclint` at build time, dynamically by the log handler at run time — because either alone is
insufficient, and the same reasoning applies here with more force: a job argument is written by one
module, stored for hours, and read by another process. A `river.JobArgs` struct marshalled to the
framework's own `args` column is invisible to both layers. `dthclint` cannot see into it, and there
is no place to hang a check constraint.

`ops.job.args` has one, and invariant 90 re-checks the whole table. The PHI key list moved into the
database (`ops.phi_key`) to make that possible, and a Go test compares it against
`logging.PHIKeys` in both directions — the same treatment the permission catalogue and the event
type registry already get, for the same reason.

### 2. The SLA is the point of the checkpoint

_"The §7.1 five-minute SLA is measured as a real metric with an alert, not asserted."_ River can
report depth, age, throughput and failure rate from its own tables. It has no opinion about a
deadline, because a deadline is our concept: it comes from `ops.job_kind.sla_seconds`, is stamped
onto the row at enqueue as `sla_deadline`, and is resolved to `met_sla` at completion. Those three
columns are what make attainment a number rather than a claim, and they would have had to be our
own columns in our own table beside River's either way — at which point there are two tables
describing one job, and the interesting one is ours.

### 3. The architecture checker cannot see a framework

`architecture.json` is the enforcement of §8.2, and it works by knowing which internal package
imports which. A job framework that every module enqueues through is a new edge from everywhere to
one place; expressed as a third-party import it is invisible to `dthclint`, and the module boundary
this codebase spends effort maintaining quietly stops meaning anything for the one cross-cutting
concern most likely to grow tendrils. `internal/jobs` is a module with an allowlist entry like any
other, and the checker fails the build when somebody reaches through it.

### 4. It reads like the rest of the system

Every durable mechanism here — the ledger, the projections, the idempotency store, the invariant
registry, the audit chain — is a migration plus a `queries/` file plus a Go module. A reader who has
understood one has understood the shape of all of them. A job queue in that shape costs a reader
nothing; a framework with its own migration tool, its own table naming, its own worker lifecycle and
its own periodic-job DSL is a second system to learn inside the first.

## What this costs, stated plainly

Retries, backoff, lease expiry and crash recovery are exactly the things that are easy to get
subtly wrong, and River has had them exercised by other people's production traffic. We have not.
Three specific costs:

- **At-least-once, not exactly-once.** A worker that dies between finishing the work and marking the
  row runs that job again after its lease expires. This is unavoidable in any queue that is not
  writing its completion in the same transaction as the work, and it is why every job here must be
  idempotent. That is not a new burden — §8.5 already required it, and the codebase already has the
  mechanism: event ids derived deterministically from their inputs, so the ledger absorbs a repeat.
  The test for criterion 5 kills a worker mid-job and asserts the ledger holds one event, not two.
- **No cron expressions.** Periodic jobs are `ops.job_schedule.every_seconds` and a `next_run_at`,
  which covers "every five minutes" and does not cover "at 02:00 on the first of the month". The
  three periodic jobs this system has today are all intervals. When one is not, that is a migration.
- **Our backoff is exponential with jitter and nothing more.** No per-error-class policies, no
  snoozing, no priority ageing.

If any of those becomes the constraint, this ADR should be superseded rather than worked around.
The table's shape is close enough to River's that a migration would be mechanical.

## Consequences

- `ops.job_kind` is a **registered catalogue**, not free strings, so the queue-health page can list
  every kind including the ones with nothing in flight — a dashboard that only shows what is
  currently queued cannot show that a job type has stopped being enqueued at all, which is the
  failure mode a dashboard exists to catch.
- Enqueue happens through `jobs.Store.EnqueueTx`, which takes the caller's `pgx.Tx`. There is no
  non-transactional enqueue in the API surface; a caller that has no transaction opens one. That is
  criterion 1, made structural rather than remembered.
- A kind can be **paused** by an operator without a deploy, because the alternative during an
  incident is a deploy.
- §4's technology list should drop River, and §8.4's parenthetical should read _"this is why the
  queue is a table in this database"_ — the argument it makes is unchanged and now stands on its
  own.
