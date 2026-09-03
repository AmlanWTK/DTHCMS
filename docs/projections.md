# Projections: read models, checkpoints and rebuilds

CP25. How an event becomes something a screen can query, how far behind that is, what
happens when it fails, and how to throw it all away and derive it again. ADR-0017 records
the decisions; `docs/event-store.md` is the ledger the models are derived from.

## 1. The promise

Everything in the `read` schema is derived. It can be deleted and rebuilt from the ledger,
and the rebuilt version is byte-identical to the one built incrementally.
`TestAReplayProducesTheSameReadModels` is that sentence as a test, and it runs in CI on
every change with no `-short` escape.

What makes it true rather than hopeful is that nothing can edit a read model by hand:
`core.assert_read_models_derived()` refuses to start a service where `dthcms_app` holds any
write privilege in `read`, and `core.assert_read_models_rebuildable()` refuses one where
`dthcms_projector` cannot rewrite a model it would have to rebuild.

## 2. Two modes

|            | Synchronous                                    | Asynchronous                                    |
| ---------- | ---------------------------------------------- | ----------------------------------------------- |
| Runs       | Inside the append transaction, as `dthcms_app` | In `cmd/projector`, as `dthcms_projector`       |
| Written in | A `read.apply_…` SQL function                  | Go                                              |
| Staleness  | None: it commits with its event                | Up to about a second                            |
| A failure  | Fails the append                               | Dead-letters the event; the append is untouched |
| Today      | `visit_vital`                                  | `station_activity`                              |

The choice is clinical, not technical. Synchronous is for a read whose staleness would be
wrong on a screen someone is looking at now — §4.1's junior doctor seeing the measurement
the nurse entered a second ago. Everything else is asynchronous, and asynchronous is the
default: a synchronous projection puts its work and its locks inside the clinical write
path.

### Why a synchronous projection is a database function

The application may not write to `read` at all — that invariant predates this checkpoint and
is not relaxed by it. So a synchronous projection is a `SECURITY DEFINER` function with a
pinned `search_path`, `EXECUTE`-granted to the application and to nobody else. The
application can run the derivation and can do nothing else to the table: no delete, no value
the event does not imply, no checkpoint moved backwards.

A side effect worth having: the rebuild calls the same function, so the incremental and
replayed derivations are one implementation rather than two that agree.

## 3. Writing a projection

```go
type StationActivity struct{}

func (StationActivity) Name() string    { return "station_activity" }
func (StationActivity) Version() int    { return 1 }
func (StationActivity) Mode() Mode      { return Asynchronous }
func (StationActivity) Handles(t string) bool { return true }
func (StationActivity) Apply(ctx context.Context, tx pgx.Tx, e eventstore.Event) error
func (StationActivity) Reset(ctx context.Context, tx pgx.Tx) error
```

Two requirements, neither optional:

**Idempotent.** Applying an event twice must leave the model as applying it once did. A
runner that crashes between applying a batch and advancing its checkpoint re-applies the
batch; a rebuild re-applies everything.

**Out-of-order tolerant.** A rebuild that fell behind and caught up can present an older
event after a newer one.

Both are usually the same mechanism — key the write on the aggregate, guard it with
`global_seq`:

```sql
ON CONFLICT (visit_id, code) DO UPDATE SET … WHERE visit_vital.global_seq < excluded.global_seq
```

A **counter** is the shape that breaks. `station_activity` guards `events` with `last_seq`,
and keeps distinct visits as rows in a second table rather than as a number, because a
number cannot be made idempotent by looking at it.

Register it in `projection.Default`, in the checkpoint that adds it.

## 4. Versioning

`Version()` is the version of the _computation_. Change the logic, change the number. The
runner then refuses to advance — `ErrStaleVersion`, naming the rebuild command — rather than
leaving half the rows computed the old way and half the new, which is a state no further
event corrects and no test detects.

## 5. Failure

Three attempts, then the event goes to `read.projection_dead_letter`, the checkpoint
advances past it, and the projection is marked `degraded`. Stopping at the bad event would
turn "missing one row" into "frozen in the past", which is worse and harder to notice.

The model is now knowingly incomplete, and says so — in the register, in
`dthcms_projection_degraded`, and in `make project-status`. Resolving a dead letter does not
re-apply the event: the honest repair is a rebuild.

Criterion 4 — a failing projection does not block appends — is structural for asynchronous
projections: they are a different process and nothing in the append path waits on them.
`TestAFailingProjectionDoesNotBlockAppends` checks it anyway, because "structural" is a
claim.

## 6. Lag, and the alerts

| Metric                           | What it says                                         |
| -------------------------------- | ---------------------------------------------------- |
| `dthcms_projection_lag_events`   | Events appended that this projection has not applied |
| `dthcms_projection_lag_seconds`  | How old the last event it applied is                 |
| `dthcms_projection_dead_letters` | Unresolved failures                                  |
| `dthcms_projection_degraded`     | 1 when degraded or rebuilding                        |

Alerts (`deploy/local/grafana/alerting/rules.json`):

- **A read model is more than a minute behind** — `lag_seconds > 60` for 5 minutes, on
  asynchronous projections. Seconds rather than events, because a quiet clinic produces no
  events and a projection that has applied all of them is not behind.
- **A read model is degraded or rebuilding** — for 10 minutes.
- **A synchronous read model is behind** — `lag_events > 0` for 5 minutes, critical. This
  should be impossible: a synchronous projection commits with its event, so any lag means an
  append happened through a path that did not carry it.

## 7. Running and rebuilding

```bash
make project              # follow the ledger (cmd/projector run)
make project-status       # checkpoints, lag, health, dead letters
make project-rebuild REASON='payload fix for HEIGHT v2' NAME=visit_vital
make project-rebuild REASON='schema change'             # every projection
```

A rebuild marks the projection `rebuilding` and its checkpoint zero — so a runner in another
process stands off and a reader can see the rows are not to be trusted — empties it, replays
every event through the same `Apply` the incremental path uses, and marks it healthy at the
ledger's head. A rebuild that dies halfway leaves the projection visibly `rebuilding` rather
than silently half-derived.

`-reason` is required, and the rebuild is audited (`projection.rebuilt`). It is the only
operation in DTHCMS that legitimately deletes derived clinical data. Nothing is lost — the
events remain — but somebody must be able to check that afterwards.

The projector connects as `dthcms_projector` via `DTHCMS_POSTGRES_PROJECTOR_URL`. Locally
that role is created by `make migrate` (`migrate dev-roles`). Configuration refuses a
production deployment where it equals the application's URL.

## 8. The read models today

**`read.visit_vital`** (synchronous) — the current value of each measurement on a visit,
with who took it, when, and on which device's behalf [R-03]. One row per (visit, code); a
correction overwrites the value it corrects, and the ledger keeps both (§7.7). This is the
vitals strip the clinical screens read, and CP61's "Entered by" line.

**`read.station_activity`** (asynchronous) — events and distinct visits per station per
clinic day (Asia/Dhaka), for the traffic board at CP40. Events from a role with no station —
a physician at a desk — are not counted; the board has no column for them.

Each clinical checkpoint adds its own projections beside its feature. These two are the
framework's references and real read models in their own right.

## 9. Acceptance criteria, and where each is proven

| Criterion                                                 | Test                                                                 |
| --------------------------------------------------------- | -------------------------------------------------------------------- |
| (1) Replay equivalence, in CI on every change             | `TestAReplayProducesTheSameReadModels`, `TestARebuildIsIdempotent`   |
| (2) Lag is a metric with an alert                         | `TestLagIsTheDistanceFromTheLedgerHead`; three rules in `rules.json` |
| (3) A rebuild is one documented command                   | `make project-rebuild`; `TestARebuildClearsTheDeadLetters`           |
| (4) A failing projection does not block appends           | `TestAFailingProjectionDoesNotBlockAppends`                          |
| Applying an event twice, or out of order, changes nothing | `TestApplyingAnEventTwiceChangesNothing`                             |
| A changed derivation refuses to advance                   | `TestAChangedDerivationRefusesToAdvanceUntilItIsRebuilt`             |
| Dead letters, and the return to healthy                   | `TestResolvingTheLastDeadLetterRestoresHealth`                       |
| Synchronous commits with its event                        | `TestASynchronousProjectionCommitsWithItsEvent`                      |
| The application cannot edit a read model                  | `TestTheApplicationRoleCannotWriteAReadModel`                        |
| The runner follows and stops cleanly                      | `TestTheRunnerFollowsTheLedgerAndStopsCleanly`                       |
| The API's write path carries its synchronous projections  | `TestTheClinicalStoreCarriesItsSynchronousProjections`               |

## 10. Manual verification

The plan's check: rebuild on the synthetic dataset and compare a checksum of every read
table before and after.

```bash
make project-status
psql -c "SELECT md5(string_agg(t::text, '|' ORDER BY t::text)) FROM read.visit_vital t"
make project-rebuild REASON='manual verification'
psql -c "SELECT md5(string_agg(t::text, '|' ORDER BY t::text)) FROM read.visit_vital t"   # identical
```

## 11. Open

- **Rebuild time at scale** is unmeasured beyond the test's dataset.
  `ledger.aggregate_snapshot` (CP23) is the escape hatch if a rebuild ever outgrows a
  maintenance window; nothing uses it, and nothing should until a measurement says so.
- **`LISTEN`/`NOTIFY`** could cut the one-second poll to nothing. It would be an optimisation
  on top of the poll, never a replacement: a dropped notification is a silently stalled
  projection.
- **A projection's own HTTP surface** arrives with the features that read them. There is no
  endpoint at this checkpoint, as planned.
