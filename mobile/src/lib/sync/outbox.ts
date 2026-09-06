import {
  META_KEYS,
  SYNC_LOG_LIMIT,
  TABLES,
  type LocalStore,
  type Row,
  type Value,
} from '@/lib/local-store';

import { projectionsFor, type AppliedEvent } from './projections';
import {
  outboxRowOf,
  replaces,
  skewReading,
  type HaltReason,
  type OutboxRow,
  type SyncCounts,
  type SyncMetrics,
} from './state';

/**
 * The local record, read and written (CP64).
 *
 * Everything that touches a table is here, so that `commands.ts` and `engine.ts` read as decisions
 * rather than as SQL-by-another-name, and so that the one rule this layer has to keep — **the
 * event, the projection and the outbox row commit together** — is visible in one place rather than
 * assembled at four call sites.
 */

/** Anything that can read and write: the store itself, or a transaction on it. */
export type Executor = Pick<LocalStore, 'all' | 'run'>;

// --- sync_meta ---

export async function readMeta(store: Executor, key: string): Promise<string | null> {
  const rows = await store.all({
    table: TABLES.syncMeta,
    where: [{ column: 'key', op: '=', value: key }],
    limit: 1,
  });
  const value = rows[0]?.value;
  return value === undefined || value === null ? null : String(value);
}

export async function readNumber(store: Executor, key: string, fallback = 0): Promise<number> {
  const raw = await readMeta(store, key);
  if (raw === null || raw === '') return fallback;
  const value = Number(raw);
  return Number.isFinite(value) ? value : fallback;
}

export async function writeMeta(store: Executor, key: string, value: Value): Promise<void> {
  await store.run({
    kind: 'insert',
    table: TABLES.syncMeta,
    row: { key, value: value === null ? null : String(value) },
    onConflict: 'replace',
  });
}

/**
 * The next local sequence number.
 *
 * This counter, not the clock, is what preserves the order events were recorded in — see
 * `selectBatch`. It is read and bumped inside the caller's transaction, so two commands issued in
 * the same tick cannot take the same number.
 */
export async function nextSequence(tx: Executor): Promise<number> {
  const current = await readNumber(tx, META_KEYS.sequence, 0);
  const next = current + 1;
  await writeMeta(tx, META_KEYS.sequence, next);
  return next;
}

// --- local_events ---

export async function hasEvent(store: Executor, eventId: string): Promise<boolean> {
  const rows = await store.all({
    table: TABLES.localEvents,
    columns: ['event_id'],
    where: [{ column: 'event_id', op: '=', value: eventId }],
    limit: 1,
  });
  return rows.length > 0;
}

/**
 * Record an event locally.
 *
 * `ignore` on conflict, and that is the whole of reconciliation's idempotency: an event this
 * device wrote and later pulls back down lands here twice and is stored once, with no branch
 * anywhere that has to remember to check first.
 */
export async function recordEvent(
  tx: Executor,
  event: AppliedEvent,
  options: { origin: 'LOCAL' | 'SERVER'; seq: number; at: number },
): Promise<void> {
  await tx.run({
    kind: 'insert',
    table: TABLES.localEvents,
    onConflict: 'ignore',
    row: {
      event_id: event.eventId,
      seq: options.seq,
      origin: options.origin,
      aggregate_type: event.aggregateType,
      aggregate_id: event.aggregateId,
      patient_id: event.patientId,
      visit_id: event.visitId,
      event_type: event.eventType,
      event_version: event.eventVersion,
      occurred_at: event.occurredAt,
      recorded_at: event.recordedAt,
      payload: JSON.stringify(event.payload),
      global_seq: event.globalSeq,
      applied_at: options.at,
    },
  });
}

/**
 * Mark an event as landed: where it is in the ledger, and when the clinic received it.
 *
 * Also flips the projections it set to confirmed, which is what turns a pending indicator beside a
 * value into a plain value.
 */
export async function confirmEvent(
  tx: Executor,
  eventId: string,
  globalSeq: number | null,
  recordedAt: string | null,
): Promise<void> {
  const set: Row = {};
  if (globalSeq !== null) set.global_seq = globalSeq;
  if (recordedAt !== null) set.recorded_at = recordedAt;
  if (Object.keys(set).length > 0) {
    await tx.run({
      kind: 'update',
      table: TABLES.localEvents,
      set,
      where: [{ column: 'event_id', op: '=', value: eventId }],
    });
  }
  await tx.run({
    kind: 'update',
    table: TABLES.projections,
    set: { confirmed: 1 },
    where: [{ column: 'event_id', op: '=', value: eventId }],
  });
}

/**
 * Every event known about a patient, oldest first.
 *
 * This is the "both values remain visible" half of §13.6, and it is a read of the log rather than
 * of the projections: the projection holds the current value, the log holds what was recorded and
 * by which event, and neither answers the other's question.
 */
export async function eventsForPatient(store: Executor, patientId: string): Promise<Row[]> {
  return store.all({
    table: TABLES.localEvents,
    where: [{ column: 'patient_id', op: '=', value: patientId }],
    order: [
      { column: 'occurred_at', direction: 'asc' },
      { column: 'event_id', direction: 'asc' },
    ],
  });
}

// --- projections ---

/**
 * Apply an event to the screens' state.
 *
 * The comparison is §13.6's rule and nothing else: a value with a later `occurred_at` replaces an
 * earlier one, an earlier one arriving late does not overwrite what is already there, and a tie is
 * broken the same way on every device so two tablets cannot settle on different answers.
 */
export async function applyProjections(
  tx: Executor,
  event: AppliedEvent,
  options: { confirmed: boolean; at: number },
): Promise<number> {
  let written = 0;
  for (const projection of projectionsFor(event)) {
    const existing = await tx.all({
      table: TABLES.projections,
      where: [
        { column: 'kind', op: '=', value: projection.kind },
        { column: 'key', op: '=', value: projection.key },
      ],
      limit: 1,
    });
    const current = existing[0];
    const stored = current
      ? { occurredAt: String(current.occurred_at), eventId: String(current.event_id) }
      : null;
    if (!replaces({ occurredAt: event.occurredAt, eventId: event.eventId }, stored)) continue;
    await tx.run({
      kind: 'insert',
      table: TABLES.projections,
      onConflict: 'replace',
      row: {
        kind: projection.kind,
        key: projection.key,
        patient_id: projection.patientId,
        document: JSON.stringify(projection.document),
        occurred_at: event.occurredAt,
        event_id: event.eventId,
        confirmed: options.confirmed ? 1 : 0,
        updated_at: options.at,
      },
    });
    written += 1;
  }
  return written;
}

export interface ProjectionRow {
  kind: string;
  key: string;
  patientId: string | null;
  document: Record<string, unknown>;
  occurredAt: string;
  eventId: string;
  /** False while the event behind this value is still in the outbox. */
  confirmed: boolean;
}

function projectionOf(row: Row): ProjectionRow {
  return {
    kind: String(row.kind),
    key: String(row.key),
    patientId:
      row.patient_id === null || row.patient_id === undefined ? null : String(row.patient_id),
    document: JSON.parse(String(row.document)) as Record<string, unknown>,
    occurredAt: String(row.occurred_at),
    eventId: String(row.event_id),
    confirmed: Number(row.confirmed) === 1,
  };
}

/** One projection, or null. This — never the network — is what a screen reads. */
export async function readProjection(
  store: Executor,
  kind: string,
  key: string,
): Promise<ProjectionRow | null> {
  const rows = await store.all({
    table: TABLES.projections,
    where: [
      { column: 'kind', op: '=', value: kind },
      { column: 'key', op: '=', value: key },
    ],
    limit: 1,
  });
  const row = rows[0];
  return row ? projectionOf(row) : null;
}

/** Everything known about a patient, by kind. */
export async function readProjections(
  store: Executor,
  patientId: string,
  kind?: string,
): Promise<ProjectionRow[]> {
  const rows = await store.all({
    table: TABLES.projections,
    where: [
      { column: 'patient_id', op: '=', value: patientId },
      ...(kind ? [{ column: 'kind', op: '=' as const, value: kind }] : []),
    ],
    order: [{ column: 'key', direction: 'asc' }],
  });
  return rows.map(projectionOf);
}

// --- the outbox ---

export async function enqueue(tx: Executor, row: Row): Promise<void> {
  await tx.run({ kind: 'insert', table: TABLES.outbox, onConflict: 'ignore', row });
}

export async function readOutbox(store: Executor, states?: string[]): Promise<OutboxRow[]> {
  const rows = await store.all({
    table: TABLES.outbox,
    where: states ? [{ column: 'state', op: 'in', values: states }] : undefined,
    order: [{ column: 'seq', direction: 'asc' }],
  });
  return rows.map(outboxRowOf);
}

export async function readOutboxRow(store: Executor, eventId: string): Promise<OutboxRow | null> {
  const rows = await store.all({
    table: TABLES.outbox,
    where: [{ column: 'event_id', op: '=', value: eventId }],
    limit: 1,
  });
  const row = rows[0];
  return row ? outboxRowOf(row) : null;
}

export async function updateOutbox(tx: Executor, eventId: string, set: Row): Promise<void> {
  await tx.run({
    kind: 'update',
    table: TABLES.outbox,
    set,
    where: [{ column: 'event_id', op: '=', value: eventId }],
  });
}

export async function dropFromOutbox(tx: Executor, eventId: string): Promise<void> {
  await tx.run({
    kind: 'delete',
    table: TABLES.outbox,
    where: [{ column: 'event_id', op: '=', value: eventId }],
  });
}

/**
 * How much is where.
 *
 * Counted in memory from one read rather than by five aggregate queries: a station's queue is a
 * clinic day at most, and one read that cannot disagree with itself is worth more here than five
 * that can — this is the number the operator is told, and §13.9 says it must never be wrong.
 */
export async function readCounts(store: Executor): Promise<SyncCounts> {
  const rows = await readOutbox(store);
  const counts: SyncCounts = {
    queued: 0,
    inFlight: 0,
    needsAttention: 0,
    held: 0,
    blocked: 0,
    awaitingTriage: 0,
    // Counted, not summed. A row in a state nobody has written a case for is still a measurement
    // that has not reached the clinic, and this is the number that says so.
    total: rows.length,
  };
  for (const row of rows) {
    switch (row.state) {
      case 'PENDING':
        counts.queued += 1;
        break;
      case 'IN_FLIGHT':
        counts.inFlight += 1;
        break;
      case 'BLOCKED_LOCAL':
        counts.blocked += 1;
        break;
      case 'AWAITING_TRIAGE':
        counts.awaitingTriage += 1;
        break;
      case 'NEEDS_ATTENTION':
        counts.needsAttention += 1;
        break;
      case 'HELD':
        counts.held += 1;
        break;
    }
  }
  return counts;
}

/** Everything the sync screen shows, in one read. */
export async function readMetrics(store: Executor): Promise<SyncMetrics> {
  const counts = await readCounts(store);
  const skewRaw = await readMeta(store, META_KEYS.clockSkewMs);
  const lastSuccess = await readMeta(store, META_KEYS.lastSuccessAt);
  const lastAttempt = await readMeta(store, META_KEYS.lastPushAt);
  const noRoom = await readMeta(store, META_KEYS.noRoomAt);
  return {
    ...counts,
    undelivered: counts.total,
    delivering: (await readMeta(store, META_KEYS.delivering)) === '1',
    lastSuccessAt: lastSuccess === null ? null : Number(lastSuccess),
    lastAttemptAt: lastAttempt === null ? null : Number(lastAttempt),
    noRoomAt: noRoom === null ? null : Number(noRoom),
    cursor: await readNumber(store, META_KEYS.cursor, 0),
    skew: skewReading(skewRaw === null ? null : Number(skewRaw)),
    halted: ((await readMeta(store, META_KEYS.haltReason)) ?? '') as HaltReason,
  };
}

// --- the local sync log ---

export interface SyncLogEntry {
  id: string;
  at: number;
  phase: 'push' | 'pull' | 'receipt' | 'reference';
  outcome: string;
  batchId?: string | null;
  events?: number;
  accepted?: number;
  duplicated?: number;
  rejected?: number;
  quarantined?: number;
  blocked?: number;
  /** A code or a short technical note. **Never** a payload, a patient, or a value. */
  detail?: string | null;
}

/**
 * One line about one attempt, for the person answering "did that tablet sync this morning".
 *
 * Counts and codes only. A log line that carried a payload would put clinical values in the one
 * place on the device nobody thinks of as a record — and support logs get copied, pasted and
 * emailed, which is exactly how PHI leaves a building.
 */
export async function appendSyncLog(store: LocalStore, entry: SyncLogEntry): Promise<void> {
  await store.run({
    kind: 'insert',
    table: TABLES.syncLog,
    onConflict: 'replace',
    row: {
      id: entry.id,
      at: entry.at,
      phase: entry.phase,
      outcome: entry.outcome,
      batch_id: entry.batchId ?? null,
      events: entry.events ?? 0,
      accepted: entry.accepted ?? 0,
      duplicated: entry.duplicated ?? 0,
      rejected: entry.rejected ?? 0,
      quarantined: entry.quarantined ?? 0,
      blocked: entry.blocked ?? 0,
      detail: entry.detail ?? null,
    },
  });
  const kept = await store.all({
    table: TABLES.syncLog,
    columns: ['at'],
    order: [{ column: 'at', direction: 'desc' }],
    limit: SYNC_LOG_LIMIT,
  });
  if (kept.length < SYNC_LOG_LIMIT) return;
  const oldest = kept[kept.length - 1]?.at;
  if (oldest === undefined || oldest === null) return;
  await store.run({
    kind: 'delete',
    table: TABLES.syncLog,
    where: [{ column: 'at', op: '<', value: Number(oldest) }],
  });
}

/**
 * A catalogue this device is holding, or null.
 *
 * The read half of `cacheCatalogue`. A station asks for it **before** the network, not after: a
 * reference range that arrives only when the clinic is reachable is a safety net that is absent
 * in exactly the session that needs one.
 */
export async function readCatalogue(store: Executor, catalogue: string): Promise<unknown | null> {
  const rows = await store.all({
    table: TABLES.referenceCache,
    where: [{ column: 'catalogue', op: '=', value: catalogue }],
    limit: 1,
  });
  const document = rows[0]?.document;
  if (document === undefined || document === null) return null;
  return JSON.parse(String(document)) as unknown;
}

export async function readSyncLog(store: Executor, limit = 20): Promise<Row[]> {
  return store.all({
    table: TABLES.syncLog,
    order: [{ column: 'at', direction: 'desc' }],
    limit,
  });
}
