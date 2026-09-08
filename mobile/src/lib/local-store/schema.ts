import type { Migration } from './driver';

/**
 * The local schema (CP64, §13.3).
 *
 * Five tables the plan names, and one it does not:
 *
 *   - `outbox` — what has been recorded here and has not yet been answered for by the clinic;
 *   - `local_events` — every event this device knows about, whether it wrote it or pulled it;
 *   - `projections` — what the screens read;
 *   - `reference_cache` — the catalogues, with the fingerprint that says whether they moved;
 *   - `sync_meta` — the cursor, the clock skew, and the rest of the bookkeeping;
 *   - `sync_log` — one line per sync attempt, for the person answering "did that tablet sync".
 *
 * The sixth is CP66's *"sync attempts logged locally for support"*, and it arrives as migration 2
 * rather than being folded into migration 1 on purpose: a schema that has only ever been created
 * from scratch has never had its migration path run, and the first device to prove otherwise
 * should not be a tablet in a clinic.
 *
 * # What is deliberately not in any of these tables
 *
 * A row here is encrypted at rest by SQLCipher and is still the least protected copy of a clinical
 * value in the system — a tablet is lost far more easily than a server. So the reference cache
 * holds catalogues and never patients, the sync log holds counts and codes and never a payload or
 * a patient id, and nothing in this file has a column for an access token: the session's
 * credentials live in the Keystore (`lib/credentials.ts`) and are not durable state this layer
 * has any business holding.
 *
 * # Times
 *
 * Two kinds, and they are not interchangeable. `occurred_at` and `recorded_at` are ISO 8601 text,
 * because they are clinical facts that travel to the server and back and must survive the journey
 * byte for byte. Everything else — `created_at`, `next_attempt_at`, `updated_at` — is epoch
 * milliseconds, because it is local bookkeeping that is compared and never displayed as an
 * instant, and because a device whose clock is three hours wrong still needs its backoff to work.
 */

/** The names, so a typo is a compile error rather than a table that quietly does not exist. */
export const TABLES = {
  outbox: 'outbox',
  localEvents: 'local_events',
  projections: 'projections',
  referenceCache: 'reference_cache',
  syncMeta: 'sync_meta',
  syncLog: 'sync_log',
  migrations: 'schema_migrations',
} as const;

/**
 * What an outbox row is doing.
 *
 * `PENDING` and `IN_FLIGHT` are the queue; the other three are terminal *for the engine* and each
 * says something different to a person:
 *
 *   - `NEEDS_ATTENTION` — the clinic refused it. §13.5's failure ladder, and CP67's screen. It is
 *     never deleted by the engine, because a rejected clinical measurement that vanishes is the
 *     silent loss the whole design exists to prevent.
 *   - `ESCALATED` — a person has read the refusal, decided nothing on this tablet can answer it,
 *     and taken it to somebody who can (CP67). The row is unchanged in every other respect: same
 *     event id, same payload, still undelivered, still counted. What has changed is that the
 *     operator standing here has done everything they can do, and a screen that went on asking
 *     them to act would be training them to ignore it — which is CP67's own named risk, arriving
 *     from the direction that is hardest to see, because every individual alarm is true.
 *
 *     A state rather than a flag on `NEEDS_ATTENTION`, for the reason `AWAITING_TRIAGE` is a state
 *     rather than a reason code: the count on the operator's screen is the difference between
 *     "you have something to fix" and "you have already done what you can", and a screen reading
 *     the state and not the flag would say the first while the second was true. It is deliberately
 *     **not** delivered, not resent and not resolved — escalating tells a person, and there is no
 *     route on the wire that a rejected event can be sent down to reach one, so a state that
 *     implied otherwise would be the indicator lying about where the work is.
 *   - `HELD` — the clinic is holding it for a person to decide about (`QUARANTINED` on the wire).
 *     Not resent: it is not lost, and resending would only produce another hold.
 *   - `BLOCKED_LOCAL` — an earlier event on the same record has not been resolved and this one
 *     declared that it depends on that record's state. It is still ours to send, later.
 *   - `AWAITING_TRIAGE` — the clinic is already holding as many of this device's events as it will
 *     hold (`QUARANTINE_FULL`, `docs/sync.md`), so there was nowhere to put this one. Also still
 *     ours to send, and that is the only thing it has in common with `BLOCKED_LOCAL`: the two are
 *     cleared by different people in different buildings. A blocked event waits for whoever is
 *     holding this tablet to deal with an earlier entry; this one waits for a physician at the
 *     clinic to work through what the tablet has already sent, and nothing done here moves it.
 *     They are separate states rather than one state and a reason code because the count on the
 *     operator's screen is the difference between "you have something to fix" and "somebody at
 *     the clinic does", and a screen that read the state and not the reason would say the first
 *     while the second was true.
 *
 * There is no `SYNCED`. An event the clinic has accepted leaves the outbox altogether and lives in
 * `local_events` with its `global_seq` — which means "is anything unsent" is `SELECT COUNT(*)`
 * rather than a filter somebody has to remember to write.
 *
 * The column is plain text with no constraint, so a state added here needs no migration; what it
 * does need is a case in `readCounts`, in `selectBatch` and on the sync screen, which is why
 * `SyncCounts` also carries a total that is counted from the rows rather than summed from the
 * named states.
 */
export const OUTBOX_STATES = [
  'PENDING',
  'IN_FLIGHT',
  'BLOCKED_LOCAL',
  'AWAITING_TRIAGE',
  'NEEDS_ATTENTION',
  'ESCALATED',
  'HELD',
] as const;
export type OutboxState = (typeof OUTBOX_STATES)[number];

/** Where an event in `local_events` came from. */
export const EVENT_ORIGINS = ['LOCAL', 'SERVER'] as const;
export type EventOrigin = (typeof EVENT_ORIGINS)[number];

/** The keys `sync_meta` holds. Declared, for the same reason the Keystore's are. */
export const META_KEYS = {
  /** The pull cursor: the `global_seq` of the last event actually applied here. */
  cursor: 'pull_cursor',
  /** A local monotonic counter, which is what preserves per-aggregate order. */
  sequence: 'local_sequence',
  lastPullAt: 'last_pull_at',
  lastPushAt: 'last_push_at',
  /** The last time a sync completed with nothing left to say. What the operator is shown. */
  lastSuccessAt: 'last_success_at',
  /** How far ahead of the clinic this device's clock was, last time anybody measured. */
  clockSkewMs: 'clock_skew_ms',
  clockSkewAt: 'clock_skew_at',
  /** Why sync has stopped, when it has. Empty when it has not. */
  haltReason: 'halt_reason',
  /** Consecutive refusals of a signed request, which is how a revoked device is recognised. */
  authFailures: 'auth_failures',
  /**
   * Set while this device is handing its backlog to the clinic's quarantine.
   *
   * A revoked tablet may still reach `POST /v1/sync/events` and nothing else, so this says "push
   * only, and stop when the queue is empty" — the difference between a morning of measurements
   * reaching somebody who can decide about them and a morning of measurements sitting on a
   * tablet in a drawer.
   */
  delivering: 'delivering_to_quarantine',
  /**
   * When the clinic last answered that it has no room to hold anything more from this device.
   *
   * Written in exactly two places, both of them a reply from the clinic: the receipt that blocks
   * an event with `QUARANTINE_FULL`, and the 429 that turns a whole batch away. **Never on a
   * button press.** The sync screen renders it as "last checked", and it may only say that
   * because the value cannot move unless a request reached the clinic and came back — an
   * operator who presses "check with the clinic again" in a corridor gets no signal and no new
   * time, which is the truth. A timestamp written when the press happened would tell them their
   * work had been checked on at a moment when the tablet had not spoken to anybody.
   *
   * Never cleared, and it does not need to be: it is shown only while something is in
   * `AWAITING_TRIAGE`, and nothing enters that state without writing it first.
   */
  noRoomAt: 'clinic_no_room_at',
} as const;

export type MetaKey = (typeof META_KEYS)[keyof typeof META_KEYS];

export const MIGRATIONS: readonly Migration[] = [
  {
    version: 1,
    name: 'the local record',
    changes: [
      {
        kind: 'create table',
        table: TABLES.outbox,
        primaryKey: ['event_id'],
        columns: [
          // The idempotency key, generated once when the command is issued and never again.
          // Everything about not duplicating a clinical measurement rests on this column.
          { name: 'event_id', type: 'text', notNull: true },
          // Creation order on this device. The batch is built in this order, which is what makes
          // per-aggregate ordering true even when the tablet's clock is wrong — see
          // `features/sync/state.ts`.
          { name: 'seq', type: 'integer', notNull: true },
          { name: 'aggregate_type', type: 'text', notNull: true },
          { name: 'aggregate_id', type: 'text', notNull: true },
          { name: 'patient_id', type: 'text' },
          { name: 'visit_id', type: 'text' },
          { name: 'event_type', type: 'text', notNull: true },
          { name: 'event_version', type: 'integer', notNull: true },
          // ISO 8601, by this device's clock, preserved exactly. Never adjusted for measured
          // skew: the time a measurement was taken is a clinical fact, and a client that quietly
          // rewrote it would be forging the one field the server refuses to assign.
          { name: 'occurred_at', type: 'text', notNull: true },
          { name: 'payload', type: 'text', notNull: true },
          { name: 'metadata', type: 'text' },
          // Null unless this event declares that it depends on the aggregate's state (§7.9).
          // Only such an event can come back BLOCKED, and only such an event is held back here.
          { name: 'expected_sequence', type: 'integer' },
          { name: 'state', type: 'text', notNull: true, default: 'PENDING' },
          { name: 'attempts', type: 'integer', notNull: true, default: 0 },
          { name: 'next_attempt_at', type: 'integer', notNull: true, default: 0 },
          // The receipt number of the batch this row was last sent in. Kept until the batch is
          // resolved, so a lost response is answerable rather than guessed at.
          { name: 'batch_id', type: 'text' },
          { name: 'reason_code', type: 'text' },
          { name: 'reason', type: 'text' },
          { name: 'created_at', type: 'integer', notNull: true },
        ],
      },
      { kind: 'create index', name: 'outbox_by_seq', table: TABLES.outbox, columns: ['seq'] },
      {
        kind: 'create index',
        name: 'outbox_by_state',
        table: TABLES.outbox,
        columns: ['state', 'seq'],
      },
      {
        kind: 'create table',
        table: TABLES.localEvents,
        primaryKey: ['event_id'],
        columns: [
          { name: 'event_id', type: 'text', notNull: true },
          { name: 'seq', type: 'integer', notNull: true },
          { name: 'origin', type: 'text', notNull: true },
          { name: 'aggregate_type', type: 'text', notNull: true },
          { name: 'aggregate_id', type: 'text', notNull: true },
          { name: 'patient_id', type: 'text' },
          { name: 'visit_id', type: 'text' },
          { name: 'event_type', type: 'text', notNull: true },
          { name: 'event_version', type: 'integer', notNull: true },
          { name: 'occurred_at', type: 'text', notNull: true },
          // The server's arrival time, null for an event this device has not yet delivered.
          { name: 'recorded_at', type: 'text' },
          { name: 'payload', type: 'text', notNull: true },
          // Where it landed in the ledger. Null means "the clinic has not confirmed this", which
          // is what a pending indicator beside a value actually means.
          { name: 'global_seq', type: 'integer' },
          { name: 'applied_at', type: 'integer', notNull: true },
        ],
      },
      {
        kind: 'create index',
        name: 'local_events_by_patient',
        table: TABLES.localEvents,
        columns: ['patient_id', 'occurred_at'],
      },
      {
        kind: 'create table',
        table: TABLES.projections,
        primaryKey: ['kind', 'key'],
        columns: [
          { name: 'kind', type: 'text', notNull: true },
          { name: 'key', type: 'text', notNull: true },
          { name: 'patient_id', type: 'text' },
          { name: 'document', type: 'text', notNull: true },
          // The `occurred_at` of the event that set this row. §13.6's rule — the later
          // measurement is the current value — is this column and a comparison, and nothing else.
          { name: 'occurred_at', type: 'text', notNull: true },
          { name: 'event_id', type: 'text', notNull: true },
          // 0 while the event behind this value is still in the outbox.
          { name: 'confirmed', type: 'integer', notNull: true, default: 0 },
          { name: 'updated_at', type: 'integer', notNull: true },
        ],
      },
      {
        kind: 'create index',
        name: 'projections_by_patient',
        table: TABLES.projections,
        columns: ['patient_id', 'kind'],
      },
      {
        kind: 'create table',
        table: TABLES.referenceCache,
        primaryKey: ['catalogue'],
        columns: [
          { name: 'catalogue', type: 'text', notNull: true },
          { name: 'fingerprint', type: 'text', notNull: true },
          { name: 'rows', type: 'integer', notNull: true, default: 0 },
          { name: 'document', type: 'text' },
          { name: 'fetched_at', type: 'integer', notNull: true },
        ],
      },
      {
        kind: 'create table',
        table: TABLES.syncMeta,
        primaryKey: ['key'],
        columns: [
          { name: 'key', type: 'text', notNull: true },
          { name: 'value', type: 'text' },
        ],
      },
    ],
  },
  {
    version: 2,
    name: 'the local sync log',
    changes: [
      {
        kind: 'create table',
        table: TABLES.syncLog,
        primaryKey: ['id'],
        columns: [
          { name: 'id', type: 'text', notNull: true },
          { name: 'at', type: 'integer', notNull: true },
          // What the attempt was and how it ended. Codes, never prose about a patient.
          { name: 'phase', type: 'text', notNull: true },
          { name: 'outcome', type: 'text', notNull: true },
          { name: 'batch_id', type: 'text' },
          { name: 'events', type: 'integer', notNull: true, default: 0 },
          { name: 'accepted', type: 'integer', notNull: true, default: 0 },
          { name: 'duplicated', type: 'integer', notNull: true, default: 0 },
          { name: 'rejected', type: 'integer', notNull: true, default: 0 },
          { name: 'quarantined', type: 'integer', notNull: true, default: 0 },
          { name: 'blocked', type: 'integer', notNull: true, default: 0 },
          { name: 'detail', type: 'text' },
        ],
      },
      { kind: 'create index', name: 'sync_log_by_at', table: TABLES.syncLog, columns: ['at'] },
    ],
  },
];

/** How many lines of the sync log are kept. Enough for a morning, not enough to be a database. */
export const SYNC_LOG_LIMIT = 100;
