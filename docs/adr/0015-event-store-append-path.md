# ADR-0015 · One append path, an aggregate-locked gapless sequence, a per-aggregate hash chain with a daily anchor, and payload schemas in Go

- **Status:** Accepted
- **Date:** 2026-09-03
- **Checkpoint:** CP23
- **Supersedes:** —

## Context

Blueprint §7 makes the clinical event ledger the source of truth (§4.1), the audit trail
(§4.5) and the research substrate (§12), and names the properties it must have: a
client-generated `event_id` for idempotency, a server-assigned gapless `sequence` per
aggregate, a monotonic `global_seq` for replay, the full attribution envelope on every
row, a hash chain per aggregate with a daily global anchor, and append-only enforced by
the database. The plan calls this the highest-consequence checkpoint of Phase 1 and says
sequence assignment under concurrency is where it goes wrong.

Four things had to be chosen: how the sequence is assigned so that it is gapless under
load; how uniqueness is enforced on a partitioned table; what the hash covers and in what
form, so that a verifier years later computes the same bytes; and what a "payload schema"
is in a Go service without a JSON Schema dependency.

## Decision

1. **One `Append`, serialised per aggregate by an advisory lock.** `Store.Append` takes
   `pg_advisory_xact_lock` on a key derived from the aggregate, reads the head from
   `ledger.event_key`, assigns `head + 1`, draws `global_seq` from a sequence, hashes,
   inserts the event and its key, commits. Appends to different aggregates do not wait on
   each other; appends to the same aggregate queue. A hundred goroutines against one
   aggregate produce sequences 1–100 with no gap and no duplicate (tested). Optimistic
   concurrency is the same lock: `ExpectedSequence` is compared with the head inside it.

2. **Uniqueness in a companion table.** A unique constraint on a partitioned table must
   include the partition key, and neither `event_id` nor `(aggregate, sequence)` may
   include `recorded_at`. `ledger.event_key` — one row per event, not partitioned — holds
   both constraints and the aggregate head, and is written in the same transaction. Two
   retries of the same event racing each other: the second loses the primary key and is
   handed the first's row.

3. **The hash covers everything in a canonical form.** SHA-256 over `prev_hash` and every
   column in a fixed, length-prefixed order — sequence, global sequence and `recorded_at`
   included. JSON columns are canonicalised (sorted keys, no whitespace, numbers as Go
   formats a float64) after a decode, so the bytes JSONB gives back hash as the bytes the
   client sent. The reference hash of a fixed envelope is pinned in a test; changing the
   canonical form is thereby a deliberate act with a migration plan, never a refactor.
   A daily anchor folds the day's hashes in global order onto the previous day's anchor,
   so removing a whole aggregate breaks the day even though no aggregate chain does.

4. **Append-only three ways.** The application role has INSERT and SELECT only; rules on
   the parent turn UPDATE and DELETE into nothing (§7.4, verbatim); a row trigger cloned
   to every partition raises on either, which is what catches a statement aimed at a
   partition by name — the one route the rule does not see. The tamper tests do what a
   superuser would have to do (silence the trigger, edit the partition) and the verifier
   names the row every time.

5. **Payload schemas are Go types with a `Validate` method**, registered per type and
   version, decoded with unknown fields refused. The checks a clinical payload needs — a
   unit that is the canonical one, a value inside the plausible band — are easier to say
   and test in Go than in schema vocabulary, and the projections decode into the same
   types. Versioning is an upcaster per version, chained at read time, never deleted
   (§7.10); a version with no path forward is an error.

## Consequences

- The audit log (CP22) and the ledger now share one discipline — advisory-locked
  sequence, canonical hash, verifier, monthly partitions — and differ only in scope
  (global vs per-aggregate sequence, with the anchor bridging the two here).
- `ledger.event_key` doubles the write per event. At §9.4's volumes (30k events a day)
  the measured cost is invisible: p95 append latency is 2–3 ms on the development
  machine against a 50 ms budget; the 10,000-event run verifies in under half a second.
- The registry is deliberately small — the measurement, blood-pressure, patient and visit
  types the first clinical checkpoints need. Each later checkpoint registers its own
  types beside its handlers, and the catalogue test lists what exists.
- Projections (CP25) read `FromGlobal`; idempotency at the HTTP layer (CP24) sits in
  front of `Append` and relies on `event_id` being the client's.
- The nightly anchor and verification jobs call `AnchorDay` and `Verify`; the jobs
  framework that schedules them is a later checkpoint, and until then the console has no
  button for the ledger — there is nothing clinical in it yet to look at.
