# ADR-0017 · A synchronous projection is a database function; asynchronous ones follow `global_seq` with no queue

- **Status:** Accepted
- **Date:** 2026-09-03
- **Checkpoint:** CP25
- **Supersedes:** —

## Context

§4.1 makes the event log the source of truth. That is only sustainable if everything derived
from it can be thrown away and rebuilt, and if somebody can prove the rebuilt version equals
the one built incrementally. CP25 asks for the framework that makes both true: a projection
interface, checkpoints, a lag metric, dead-letter handling, versioning, a one-command
rebuild, and the replay-equivalence test that runs in CI.

Two things in the plan's scope needed a decision rather than an implementation.

**Synchronous projections.** The plan asks for "synchronous in-transaction projections for
clinically critical reads". But `core.assert_read_models_derived()` — written at CP03, and
checked on every start — refuses to run a service where `dthcms_app` holds INSERT, UPDATE or
DELETE on anything in `read`. Its reasoning is unchanged and correct: a read model the
application can edit is one that will be corrected by hand one afternoon, after which it
agrees with nothing and no rebuild can be trusted. A synchronous projection runs _as_ the
application, inside the append transaction. Something had to give.

**The asynchronous runner.** The plan names River as the technology. River is a Postgres job
queue; a job queue is not available to this project (no module proxy reaches this sandbox),
and more to the point it is not obviously needed: the ledger is already a durable, ordered,
gapless log with a checkpoint per consumer.

## Decision

### 1. A synchronous projection is a SECURITY DEFINER function, not an INSERT from a handler

`read.apply_visit_vital(jsonb)` is owned by the schema owner, has a pinned
`search_path = read, pg_catalog`, and is `EXECUTE`-granted to `dthcms_app` and
`dthcms_projector` and to nobody else. The application calls it inside the append
transaction. It cannot delete a row, cannot write a value the event does not imply, and
cannot move a checkpoint backwards, because those are not things the function does.

The CP03 invariant is untouched: `has_table_privilege('dthcms_app', 'read.visit_vital',
'INSERT')` is still false, and the service still refuses to start if it ever becomes true.

Two consequences are worth stating rather than discovering:

- The derivation of a synchronous projection is written in SQL, not Go. That is a real
  constraint and the reason to keep synchronous projections few.
- The rebuild calls the _same function_. So "incrementally built" and "replayed" are not two
  implementations that a test hopes agree — they are one implementation driven twice. The
  equivalence test still exists, because the asynchronous projections are Go and because a
  claim this important deserves a check that would fail if it stopped being true.

The pinned `search_path` is not decoration. An unpinned SECURITY DEFINER function is the
classic PostgreSQL privilege escalation: a caller who can create a table in a schema earlier
on the path redirects the writes into their own.

_Considered and rejected:_ granting `dthcms_app` INSERT and UPDATE (but not DELETE) on the
synchronous read models. It weakens a checked invariant to a documented convention, and the
convention is exactly the one people break at the end of a long day.

### 2. Synchronous means "commits with its event", and a failure fails the append

The event row and the derived row are written in one transaction. There is no window in
which the ledger holds a fact the screen does not, which is the entire reason to pay for a
synchronous projection. The price is that a projection failure fails the write, and that is
the correct trade _only_ where staleness would be clinical — §4.1's junior doctor reading
the measurement the nurse just took. Everything else is asynchronous, where criterion 4
applies: a failing projection cannot affect an append at all, because it is a different
process.

Today exactly one projection is synchronous (`visit_vital`). The alert
`dthcms-synchronous-projection-lag` fires if a synchronous projection is ever _any_ events
behind, because that would mean an append happened through a path that did not carry it.

### 3. No job queue: the runner follows `global_seq`

Each asynchronous projection has a goroutine that reads `FromGlobal(checkpoint+1, batch)`,
applies, and advances its checkpoint — the batch and the checkpoint in one transaction. A
queue on top of the ledger would be a second ordered durable thing to keep in step with the
first, and the failure mode of "the queue and the log disagree" is worse than anything it
would buy.

The poll interval is one second with an immediate re-read after a full batch. It is a poll
rather than `LISTEN`/`NOTIFY` because a poll cannot miss a wake-up: a notification dropped
while the runner reconnects leaves a projection quietly stalled, which is the failure mode
this system can least afford to have be silent.

Crash recovery re-applies at most one batch, so **every projection must be idempotent** and
tolerate out-of-order application. The reference projections show the two mechanisms:
`visit_vital` upserts under a `global_seq` guard, `station_activity` guards its counter with
`last_seq` and keeps distinct visits as rows rather than as a number.

### 4. A poison event is dead-lettered and skipped, not retried forever

Three attempts, then a row in `read.projection_dead_letter`, the checkpoint advanced past
it, and the projection marked `degraded`. The alternative — stop at the bad event — turns
"this model is missing one row" into "this model is frozen in the past", which is worse and
much harder to notice.

Resolving a dead letter does not re-apply the event. A projection that skipped one is
missing whatever it implied, and the honest repair is a rebuild.

### 5. A changed derivation refuses to advance until it is rebuilt

`Version()` is the version of the _computation_. When the stored version is not the code's,
the runner returns `ErrStaleVersion` naming the rebuild command rather than continuing.
Continuing would leave half the rows computed one way and half the other — a state no
further event corrects and no test detects.

### 6. The projector is its own binary and its own database role

`cmd/projector` connects with `DTHCMS_POSTGRES_PROJECTOR_URL` as `dthcms_projector`, the
only role that may write read models. Configuration refuses a production deployment where
that URL equals the application's, for the same reason it refuses one where the migration
URL does.

A rebuild is audited (`projection.rebuilt`), and `-reason` is required. A rebuild is the only
operation in DTHCMS that legitimately deletes derived clinical data; nothing is lost, because
the events remain — but "nothing was lost" is a claim somebody must be able to check later.

## Consequences

- Read models are provably rebuildable, and `TestAReplayProducesTheSameReadModels` runs in
  CI on every change without a `-short` escape.
- A new synchronous projection means a migration (the function, its grants) as well as Go.
  This is friction, and it is aimed at the right thing: synchronous should be rare.
- The projector is a new deployment unit with its own credentials.
- Lag is two metrics, not one: `dthcms_projection_lag_events` (how far behind) and
  `dthcms_projection_lag_seconds` (how stale). The alert fires on seconds, because a quiet
  clinic produces no events and a projection that has applied all of them is not behind.
- `read.projection_state` and `read.projection_dead_letter` are exempt from facility scoping
  (D-61's mechanism), because a projection belongs to the deployment rather than a clinic.
- **Open:** rebuild time at scale is unmeasured beyond the test's dataset.
  `ledger.aggregate_snapshot` exists (CP23) as the escape hatch if a rebuild ever becomes too
  slow to run in a maintenance window; nothing uses it yet, and nothing should until a
  measurement says so.

## Alternatives considered

- **River, or any job queue, per the plan's technology note.** Unavailable here, and it would
  duplicate the ordering the ledger already guarantees. If a later checkpoint needs
  retryable _side effects_ — sending an SMS, calling a model — that is a queue's job and this
  decision does not preclude one; projecting is not.
- **`LISTEN`/`NOTIFY` to wake the runner.** Lower latency, and a missed notification is a
  silently stalled projection. The polling loop's worst case is one second of lag, which no
  asynchronous read model cares about. It could be added as an _optimisation_ on top of the
  poll, never as a replacement for it.
- **One goroutine applying every projection per event.** Simpler, and it couples them: a
  projection that is slow, degraded or rebuilding would hold up every other. One loop each
  costs a connection and buys independence.
- **Rebuilding by truncating and re-running the incremental path.** What is implemented, in
  effect — but through `Reset` on the projection rather than a generic `TRUNCATE`, so a
  projection that owns several tables (like `station_activity`) empties all of them, and one
  that owns rows in a shared table can empty only its own.
