# The clinical event ledger

CP23. The single write path (§5.4) and the immutable ledger behind it (§7): how an event
is appended, what makes the sequence gapless, what the hash covers, how a day is
anchored, what a payload schema is, and where each acceptance criterion is proven.
The security audit log is the other trail (`docs/audit.md`); ADR-0015 records the
decisions.

## 1. The envelope

`eventstore.Envelope` is §7.2 as a Go struct. Every field of the attribution envelope is
required — `event_id` (client-generated UUIDv7), aggregate type and id, event type and
version, `occurred_at`, actor user, device, role and facility, source, payload — and
`Envelope.Validate` refuses an append missing any of them, naming the missing fields.
The table refuses the same row again with NOT NULL and CHECK constraints (criterion 5,
tested both ways). Station, patient, visit, `previous`, `correction` and `metadata` are
optional; a `correction`, when present, must be complete.

`occurred_at` is the client's clock and clinically meaningful; `recorded_at` is the
server's and authoritative. Both are kept (§7.2).

## 2. Append

`Store.Append(ctx, envelope)` does, in order:

1. validates the envelope (criterion 5);
2. decodes the payload against the registry for the type and version (§5, below) and
   checks the type belongs to the aggregate;
3. looks the `event_id` up — a replay returns the original row with `Duplicate: true`
   and writes nothing (§7.5);
4. takes `pg_advisory_xact_lock` on the aggregate, reads the head from
   `ledger.event_key`, assigns `sequence = head + 1`, and compares `ExpectedSequence`
   with the head when one was given (§7.9);
5. draws `global_seq`, sets `recorded_at`, computes the hash;
6. inserts the event and its key in one transaction.

Appends to different aggregates run in parallel; appends to one aggregate queue on the
lock. `TestSequencesAreGaplessUnderConcurrency` runs a hundred goroutines against one
aggregate and finds sequences 1–100, then eight concurrent retries of one event and finds
one row.

## 3. The tables

| Table                       | Purpose                                                                                                 |
| --------------------------- | ------------------------------------------------------------------------------------------------------- |
| `ledger.event`              | The ledger, §7.4 column for column, partitioned monthly by `recorded_at` with a default partition       |
| `ledger.event_key`          | One row per event: `event_id` unique, `(aggregate_type, aggregate_id, sequence)` unique, the chain head |
| `ledger.chain_anchor`       | One row per clinic day: the fold of the day's hashes, chained to the day before                         |
| `ledger.aggregate_snapshot` | Created at CP23, unused until a measured need (§7.9)                                                    |

**Append-only, three ways** (criterion 2): the application role holds INSERT and SELECT
and nothing else (the schema's default privileges; `core.assert_ledger_append_only()`);
rules on the parent turn UPDATE and DELETE into nothing (§7.4, verbatim); a row trigger
cloned to every partition raises on either, which is what refuses a statement aimed at a
partition by name. `core.assert_event_store_immutable()` checks all three on every start.
`TestTheLedgerCannotBeRewritten` tries each route.

**Partitions.** `ledger.ensure_event_partitions(n)` creates the next _n_ months; the
migration creates fifteen. The application cannot create tables in `ledger`, so this is a
migrator's job. A row for an uncovered month lands in `event_default` rather than being
refused, and `Verify` reports the count as `Strays`. `TestPartitionsRotateAndCatchStrays`
rotates and catches one.

## 4. The chain and the anchor

Every event's `hash` is SHA-256 over `prev_hash` and every column of the row in a fixed,
length-prefixed order — sequence, global sequence and `recorded_at` included. JSON columns
are canonicalised: decoded, re-encoded with sorted keys and no whitespace, numbers as Go
formats a float64 — so `{"value":150.0}` from the client and `{"value": 150}` from JSONB
hash the same. `TestTheHashIsCanonical` pins the reference hash of a fixed envelope: a
change to the canonical form fails the test and is therefore a deliberate act with a
migration plan for existing chains.

`Store.Verify` walks every aggregate from sequence 1 and recomputes every hash, then
re-folds every daily anchor. It names the first aggregate and sequence that does not
agree — a changed field, a broken link, a missing sequence, a key table that disagrees
with the ledger — or the first day whose anchor no longer matches its events.
`TestTheVerifierDetectsTampering` does what a superuser would have to do (silences the
trigger, edits the partition) four ways, and the verifier names the row each time
(criterion 3).

`Store.AnchorDay(facility, day)` folds a clinic day (Asia/Dhaka) in global order onto the
previous anchor and writes `ledger.chain_anchor`; a day is anchored once, an empty day
still anchors, and an event that appears in an anchored day after the fact breaks that
day (`TestDailyAnchorsChainTheDays`). The nightly job anchors yesterday and verifies;
the monthly job re-verifies a sample (§7.10). The jobs framework that schedules them is a
later checkpoint; the functions are here.

## 5. The registry

`eventstore.Default` holds the event types (§7.3): name, version, aggregate, a payload
type with a `Validate` method, and an optional upcaster to the next version. Decoding
refuses unknown fields and wrong types; `Validate` refuses the implausible — a height
outside 30–250 cm, a weight in pounds, a diastolic above a systolic. An unregistered type
or version, a payload that does not fit, or a type on the wrong aggregate is refused before
the lock is taken.

The initial catalogue: `PATIENT_REGISTERED` (PATIENT); `VISIT_OPENED`, `HEIGHT_RECORDED`,
`HEIGHT_CORRECTED`, `WEIGHT_RECORDED`, `WEIGHT_CORRECTED`, `WAIST_RECORDED`,
`HIP_RECORDED`, `PULSE_RECORDED`, `SPO2_RECORDED`, `TEMP_RECORDED` (Measurement),
`BP_RECORDED`, `BP_CORRECTED` (BloodPressure) — all VISIT. Each clinical checkpoint
registers its own beside its handlers; `TestTheInitialCatalogueIsWhatTheDocumentationSays`
keeps this list honest.

**Versioning** (§7.10): a new shape is a new version with an upcaster from the old one,
chained at read time by `Store.Decode`; upcasters are never deleted, and a version with
no path forward is an error, not a silent pass (`TestAnOldVersionIsUpcastToTheCurrentOne`).

## 6. Reads

`Stream(aggregate, fromSeq)` is the replay read, in the aggregate's own order.
`FromGlobal(fromGlobal)` is the projection read (§7.8), in global order. `ByID` reads one.
There is no HTTP surface at this checkpoint — internal only, as the plan says; the first
clinical write arrives with its station.

## 7. Performance

`TestAppendLatencyStaysUnderBudget` appends a thousand events across twenty aggregates and
fails above a p95 of 50 ms (criterion 4); on the development machine p95 is 2–3 ms.
`TestTenThousandEventsVerify` appends ten thousand across a hundred aggregates with eight
writers and verifies the whole ledger afterwards (≈1,100 events/s and a 0.35 s
verification here). `BenchmarkAppend` is the serialised worst case. All three skip under
`-short`.

## 8. Acceptance criteria, and where each is proven

| Criterion                                                              | Test                                                                            |
| ---------------------------------------------------------------------- | ------------------------------------------------------------------------------- |
| (1) Sequences gapless per aggregate under concurrent load              | `TestSequencesAreGaplessUnderConcurrency`                                       |
| (2) The application role cannot mutate or delete events                | `TestTheLedgerCannotBeRewritten`; `assert_event_store_immutable` on every start |
| (3) The verifier detects any tampering                                 | `TestTheVerifierDetectsTampering`, `TestDailyAnchorsChainTheDays`               |
| (4) Append latency p95 ≤ 50 ms                                         | `TestAppendLatencyStaysUnderBudget`, `BenchmarkAppend`                          |
| (5) Every event carries the full envelope; a missing field is rejected | `TestAnIncompleteEnvelopeIsRejected` (module and table)                         |
| Partition rotation                                                     | `TestPartitionsRotateAndCatchStrays`                                            |
| 10,000-event insert                                                    | `TestTenThousandEventsVerify`                                                   |
| The 140/150 case (§7.7)                                                | `TestAppendAssignsSequencesAndChains`                                           |

## 9. Manual verification

Append events (any test does), then as the owner:

```sql
UPDATE ledger.event SET payload = '{}' WHERE global_seq = 1;          -- UPDATE 0: the rule
UPDATE ledger.event_2026_09 SET payload = '{}' WHERE global_seq = 1;  -- ERROR: the trigger
SET session_replication_role = replica;                               -- superuser only
UPDATE ledger.event_2026_09 SET payload = '{}' WHERE global_seq = 1;  -- UPDATE 1: tampered
```

then `Store.Verify` names sequence 1 of that aggregate. As `dthcms_app`, the first
statement is also `UPDATE 0` and `TRUNCATE ledger.event` is `permission denied`.

## 10. Open

- **D-05 retention.** Partitioned from the first day so the answer is a per-partition
  operation; the plan's 24-month cold storage move is one `ALTER TABLE … DETACH`.
- **The nightly anchor and verification jobs** wait on the jobs framework.
- **A chain break is a P1 security incident** (§7.10); the runbook entry arrives with the
  operations checkpoint, and the console alert already exists for the audit chain.
