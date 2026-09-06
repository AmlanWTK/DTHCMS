import { META_KEYS, type LocalStore } from '@/lib/local-store';

import {
  applyProjections,
  enqueue,
  hasEvent,
  nextSequence,
  readMeta,
  readOutboxRow,
  recordEvent,
} from './outbox';
import { CLOCK_NOTICE_MS } from './state';

import type { AppliedEvent } from './projections';

/**
 * The command handler: the only way anything is written on this device (CP64).
 *
 * # Why a screen never writes a projection
 *
 * A station issues a command; this function appends the event, updates what the screen reads, and
 * enqueues the outbox row — **in one local transaction**. That is the same shape the server keeps
 * one layer up, and it is bought for the same reason: if the three could be written separately,
 * they could disagree, and the disagreement that matters is the one where the screen shows a
 * weight the queue does not contain. The operator sees a recorded measurement, walks away, and
 * nothing about the device's behaviour is wrong afterwards — the value simply never reaches the
 * clinic, and the first person to notice is a physician wondering why a patient has no vitals.
 *
 * So there is exactly one write path, it is transactional, and a storage failure leaves **nothing**
 * behind rather than two thirds of an entry. `test/sync-outbox.test.ts` fills the device up in the
 * middle of a command and asserts all three tables are untouched.
 *
 * # `event_id` is generated here, once
 *
 * This is the only place in the offline system that makes one. It is the ledger's key and the
 * idempotency key: a retry that produced a new one would put the same blood pressure in the record
 * twice, as two facts about two moments, and nothing downstream could tell. Nothing in the engine
 * can change it — the column is written here and read everywhere else.
 *
 * A caller that already has an id (a screen retrying a save it was told had failed) passes it back,
 * and this function is idempotent: the same command issued twice is one event, one outbox row and
 * one projection.
 *
 * # `occurred_at` is the device's clock, unadjusted
 *
 * Even when the device's clock is known to be wrong. The measured skew rides along in the metadata
 * so that whoever reviews a held event can see it, but the timestamp itself is what the operator's
 * device said at the moment of the measurement, and quietly correcting it would be the client
 * asserting a clinical fact it does not know — and would defeat the clinic's hold, which exists
 * precisely so a person can say what time it actually was.
 */

export interface Command {
  /** Supplied only when re-issuing a command that already has an id. Never regenerated. */
  eventId?: string;
  aggregateType: string;
  aggregateId: string;
  patientId?: string | null;
  visitId?: string | null;
  eventType: string;
  eventVersion?: number;
  /** ISO 8601. Defaults to the device's clock now. */
  occurredAt?: string;
  payload: Record<string, unknown>;
  metadata?: Record<string, unknown>;
  /**
   * Set only by a command that genuinely depends on the aggregate's state (§7.9).
   *
   * Almost nothing a station does should set it: a measurement is a fact about a moment, and
   * declaring a dependency turns an ordinary event into one that can be `BLOCKED` behind another's
   * failure. It exists because state transitions need it, and it is opt-in for that reason.
   */
  expectedSequence?: number | null;
}

export interface CommandDeps {
  now?: () => number;
  newId?: () => string;
}

export interface Issued {
  eventId: string;
  seq: number;
  /** True when this command had already been issued — the same id, already queued or recorded. */
  duplicate: boolean;
}

function defaultId(): string {
  return globalThis.crypto.randomUUID();
}

/**
 * Issue one command.
 *
 * Resolves with the event id, which the caller keeps if it may ever need to re-issue. Rejects — and
 * writes nothing at all — when the device has no room, which is the one failure a station screen
 * must render rather than swallow.
 */
export async function issueCommand(
  store: LocalStore,
  command: Command,
  deps: CommandDeps = {},
): Promise<Issued> {
  const now = deps.now ?? Date.now;
  const at = now();
  const eventId = command.eventId ?? (deps.newId ?? defaultId)();
  const occurredAt = command.occurredAt ?? new Date(at).toISOString();

  return store.transaction(async (tx) => {
    // The same command twice is one event. A screen that was told "not saved" by a full disk and
    // retries after the operator frees some space passes the same id back, and so does a retry
    // after a crash; neither may produce a second measurement.
    //
    // Both tables are checked, not only the log: a sign-out wipes `local_events` and deliberately
    // keeps the outbox (see `lifecycle.ts`), so after one there are undelivered events that the
    // log no longer knows about — and the queue is the copy that matters.
    const queued = await readOutboxRow(tx, eventId);
    if (queued || (await hasEvent(tx, eventId))) {
      return { eventId, seq: queued?.seq ?? 0, duplicate: true };
    }

    const seq = await nextSequence(tx);
    const skewRaw = await readMeta(tx, META_KEYS.clockSkewMs);
    const skew = skewRaw === null ? null : Number(skewRaw);

    const metadata: Record<string, unknown> = { ...(command.metadata ?? {}) };
    if (skew !== null && Math.abs(skew) >= CLOCK_NOTICE_MS) {
      // Not a correction — a note. If this event is held for a clock the clinic does not trust,
      // the person deciding can see how far out the device was and by how much, rather than
      // inferring it from a timestamp that is the thing in doubt.
      metadata.device_clock_skew_ms = skew;
    }

    const event: AppliedEvent = {
      eventId,
      aggregateType: command.aggregateType,
      aggregateId: command.aggregateId,
      patientId: command.patientId ?? null,
      visitId: command.visitId ?? null,
      eventType: command.eventType,
      eventVersion: command.eventVersion ?? 1,
      occurredAt,
      recordedAt: null,
      payload: command.payload,
      globalSeq: null,
    };

    await recordEvent(tx, event, { origin: 'LOCAL', seq, at });
    await applyProjections(tx, event, { confirmed: false, at });
    await enqueue(tx, {
      event_id: eventId,
      seq,
      aggregate_type: event.aggregateType,
      aggregate_id: event.aggregateId,
      patient_id: event.patientId,
      visit_id: event.visitId,
      event_type: event.eventType,
      event_version: event.eventVersion,
      occurred_at: occurredAt,
      payload: JSON.stringify(command.payload),
      metadata: Object.keys(metadata).length > 0 ? JSON.stringify(metadata) : null,
      expected_sequence: command.expectedSequence ?? null,
      state: 'PENDING',
      attempts: 0,
      // Zero rather than `at`: the first attempt is due immediately. A queue that waited for its
      // own backoff before its first try would make every entry look slow on a good connection.
      next_attempt_at: 0,
      batch_id: null,
      reason_code: null,
      reason: null,
      created_at: at,
    });

    return { eventId, seq, duplicate: false };
  });
}
