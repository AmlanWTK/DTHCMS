import { ApiError, NetworkError } from '@dthcms/api-client';
import type { components } from '@dthcms/api-client';

import type { OutboxState, Row, Value } from '@/lib/local-store';

/**
 * The sync engine's decisions, as data (CP64/CP66, §13.4, §13.5, §13.6).
 *
 * Every rule in this file is the mirror image of one the server states in `docs/sync.md`, and each
 * is here — pure, with a test beside it — rather than inside the engine loop, for the reason every
 * station has followed since CP33: a decision buried in an async loop full of network calls is a
 * decision nobody checks. The loop in `engine.ts` reads as a sequence of these functions.
 *
 * The five rules that matter most, and what breaks if each is wrong:
 *
 *  1. **`event_id` is generated once, when the command is issued, and never again.** It is the
 *     ledger's key. A retry that regenerated it would write the same blood pressure twice, with
 *     two ids and two moments, and nothing downstream would ever notice — the worst failure this
 *     system has. `commands.ts` generates it; nothing in this file or the engine can change it.
 *  2. **`batch_id` is generated once per batch attempt and kept until the batch is resolved.** It
 *     is the receipt number for the case the endpoint exists for: fifty events processed and the
 *     response lost on the way back. A client that started a fresh batch would be asking a
 *     question the server can no longer answer.
 *  3. **`DUPLICATE` is success.** It is what a resent batch looks like. A client that treated it as
 *     an error would stop making progress after the first lost response and would keep a full
 *     queue for ever while reporting failures nobody can act on.
 *  4. **`REJECTED` is a person's problem, not the engine's.** It stops being resent and starts
 *     being visible (§13.6: surfaced, never auto-resolved). It is never deleted here.
 *  5. **`BLOCKED` is ours to send again**, and until then it is held back locally rather than
 *     resent into another `BLOCKED`, which would be a loop that looks like progress. What it is
 *     waiting for depends on the reason and the two are not interchangeable: an earlier event on
 *     the same record, resolved here, or room in the clinic's quarantine, resolved there.
 */

// --- the wire, as the contract describes it ---

export type SyncEvent = components['schemas']['SyncEvent'];
export type SyncResult = components['schemas']['SyncResult'];
export type SyncReceipt = components['schemas']['SyncReceipt'];
export type SyncPage = components['schemas']['SyncPage'];
export type PulledEvent = components['schemas']['PulledEvent'];
export type ReferenceVersion = components['schemas']['ReferenceVersion'];
export type DeviceSyncState = components['schemas']['DeviceSyncState'];

/** What happened to one event. The five are not interchangeable; see `actionFor`. */
export type Outcome = NonNullable<SyncResult['outcome']>;

/** The reason codes the server sends. A client branches on these, never on the prose. */
export const REASON_CODES = {
  deviceRevoked: 'DEVICE_REVOKED',
  deviceSuspended: 'DEVICE_SUSPENDED',
  clockImplausible: 'CLOCK_IMPLAUSIBLE',
  unknownType: 'UNKNOWN_EVENT_TYPE',
  invalidPayload: 'INVALID_PAYLOAD',
  sequenceConflict: 'SEQUENCE_CONFLICT',
  earlierFailed: 'EARLIER_EVENT_FAILED',
  /**
   * The clinic has nowhere left to hold this device's work: 2,000 events already await review.
   *
   * The second of the two things that produce a `BLOCKED`, and the opposite of the first in every
   * respect that matters to a client. `EARLIER_EVENT_FAILED` is about one record and is cleared
   * here; this is a ceiling on the whole device and is cleared by a person at the clinic reading
   * the quarantine. Sending again in the same tick — which is what the first one's handling would
   * do — is a loop that burns the battery, spends the rate-limit budget, and looks like activity.
   */
  quarantineFull: 'QUARANTINE_FULL',
  refused: 'REFUSED',
} as const;

/** The 429 that means the clinic's hold on this device is full. Never with a `Retry-After`. */
export const SYNC_QUARANTINE_FULL = 'SYNC_QUARANTINE_FULL';

// --- the numbers ---

/**
 * How many events go in one batch.
 *
 * The server refuses more than 500 (`docs/sync.md`, "Limits"), and this is deliberately well
 * below it. The reason is not politeness: a batch is the unit of work that a dropped connection
 * costs, and the unit a receipt covers. A hundred events is a few seconds of a bad connection and
 * one small request to ask about afterwards; five hundred is a minute of one, on a tablet in a
 * corridor, and the whole morning riding on a single response arriving.
 *
 * A 200-event backlog is therefore two batches, and §13.10's scenario is a normal tick.
 */
export const BATCH_LIMIT = 100;

/** The server's own limit, mirrored so `BATCH_LIMIT` can be checked against it by a test. */
export const SERVER_MAX_BATCH = 500;

/** How many batches one sync attempt will send before yielding. */
export const MAX_BATCHES_PER_TICK = 8;

/** How many events one pull page asks for. The server's own default. */
export const PULL_PAGE = 200;

/**
 * §13.5's ladder: 2s, 5s, 15s, 60s, 5m, capped.
 *
 * The cap matters more than the shape. A tablet in a clinic with no signal for two hours must not
 * be trying every two seconds — that is the battery, and it is also the thing that makes the first
 * minute after the Wi-Fi returns useless because every device in the building is retrying at once.
 */
export const BACKOFF_LADDER_MS = [2_000, 5_000, 15_000, 60_000, 300_000] as const;

/**
 * How long to leave an event the clinic had no room to hold before offering it again.
 *
 * Off the ladder entirely, and that is the point. Every other wait in this file is a guess about
 * a network; this one is a guess about **a person reading a list**, because `QUARANTINE_FULL` is
 * cleared by a physician working through this device's quarantine and by nothing else. Waiting
 * does not clear it and neither does trying harder, so the ladder's first rung — two seconds —
 * would be a tablet asking a question two hundred times before anybody had opened the screen that
 * answers it.
 *
 * Half an hour, jittered to fifteen minutes either side. Long enough that the retries cost
 * nothing — two an hour against a budget of seven hundred — and short enough that a supervisor
 * who clears the list at eleven has the tablet delivering before half past, **with nobody touching
 * the tablet**. That last property is the requirement: the device that cannot deliver is by
 * definition one the clinic has refused, so anything that needed an operator to press something
 * would be asking the wrong person, in the wrong building, to fix it.
 */
export const TRIAGE_RETRY_MS = 30 * 60_000;

/**
 * The longest wait a `Retry-After` may buy.
 *
 * The server's number is obeyed (§6e) — it knows its own bucket and this client does not. The cap
 * is for the number that does not come from the server: a proxy or a WAF in front of the API
 * answering 429 with "come back in an hour" would otherwise park a morning of measurements for an
 * hour on the word of something that has never heard of this protocol. Fifteen minutes is far
 * beyond anything the real limiter asks for (its bucket refills one token every five seconds), so
 * in every case this cap fires the number was not the limiter's.
 */
export const MAX_RETRY_AFTER_MS = 15 * 60_000;

/**
 * How far a device's clock may be ahead before the clinic holds its entries (`ClockTolerance`).
 *
 * Mirrored here so the operator can be warned **before** a morning's work is quarantined rather
 * than after. The number is the server's; if the two ever disagree, the client's warning is the
 * one that is wrong, and it is one constant to change.
 */
export const CLOCK_TOLERANCE_MS = 5 * 60_000;

/** When to mention the clock at all. A minute is drift; five is a problem the clinic will act on. */
export const CLOCK_NOTICE_MS = 60_000;

/**
 * How many consecutive refusals of a *signed* request mean the device itself has been refused.
 *
 * A single 401 is an expired access token, which the refreshing fetch answers by itself. Three in
 * a row, across three attempts, with a session that keeps working, is not a token — it is a
 * revoked, suspended or lost device (`auth.Devices.Verify` refuses those before any handler runs),
 * and continuing to retry would be a loop rather than a recovery.
 */
export const MAX_AUTH_FAILURES = 3;

// --- the outbox row, typed ---

export interface OutboxRow {
  eventId: string;
  seq: number;
  aggregateType: string;
  aggregateId: string;
  patientId: string | null;
  visitId: string | null;
  eventType: string;
  eventVersion: number;
  occurredAt: string;
  payload: string;
  metadata: string | null;
  expectedSequence: number | null;
  state: OutboxState;
  attempts: number;
  nextAttemptAt: number;
  batchId: string | null;
  reasonCode: string | null;
  reason: string | null;
  createdAt: number;
}

function text(row: Row, column: string): string {
  const value = row[column];
  return value === null || value === undefined ? '' : String(value);
}

function optional(row: Row, column: string): string | null {
  const value = row[column];
  return value === null || value === undefined || value === '' ? null : String(value);
}

function number_(row: Row, column: string): number {
  const value = row[column];
  return typeof value === 'number' ? value : Number(value ?? 0);
}

function optionalNumber(row: Row, column: string): number | null {
  const value = row[column];
  if (value === null || value === undefined || value === '') return null;
  return typeof value === 'number' ? value : Number(value);
}

/** One stored row as the engine reads it. */
export function outboxRowOf(row: Row): OutboxRow {
  return {
    eventId: text(row, 'event_id'),
    seq: number_(row, 'seq'),
    aggregateType: text(row, 'aggregate_type'),
    aggregateId: text(row, 'aggregate_id'),
    patientId: optional(row, 'patient_id'),
    visitId: optional(row, 'visit_id'),
    eventType: text(row, 'event_type'),
    eventVersion: number_(row, 'event_version'),
    occurredAt: text(row, 'occurred_at'),
    payload: text(row, 'payload'),
    metadata: optional(row, 'metadata'),
    expectedSequence: optionalNumber(row, 'expected_sequence'),
    state: text(row, 'state') as OutboxState,
    attempts: number_(row, 'attempts'),
    nextAttemptAt: number_(row, 'next_attempt_at'),
    batchId: optional(row, 'batch_id'),
    reasonCode: optional(row, 'reason_code'),
    reason: optional(row, 'reason'),
    createdAt: number_(row, 'created_at'),
  };
}

/**
 * One row as the wire wants it.
 *
 * Note what is not sent, and could not be: no `recorded_at`, no actor, no source, no global
 * sequence. The server assigns all four, and a client that could supply them could forge
 * attribution — which is why the contract's `SyncEvent` does not have the fields at all.
 */
export function toWireEvent(row: OutboxRow): SyncEvent {
  const event: SyncEvent = {
    event_id: row.eventId,
    aggregate_type: row.aggregateType,
    aggregate_id: row.aggregateId,
    event_type: row.eventType,
    event_version: row.eventVersion,
    // Exactly as it was recorded. Never adjusted by the measured clock skew: the time a
    // measurement was taken is a clinical fact, and a client that quietly corrected it would be
    // forging the one field the server refuses to assign — and would defeat the hold that exists
    // so a person can say what time it actually was.
    occurred_at: row.occurredAt,
    payload: JSON.parse(row.payload) as Record<string, unknown>,
  };
  if (row.patientId) event.patient_id = row.patientId;
  if (row.visitId) event.visit_id = row.visitId;
  if (row.expectedSequence !== null) event.expected_sequence = row.expectedSequence;
  if (row.metadata) event.metadata = JSON.parse(row.metadata) as Record<string, unknown>;
  return event;
}

// --- backoff ---

/**
 * How long to wait before the next attempt, in milliseconds.
 *
 * The ladder, with jitter, and the jitter is not decoration. Every tablet in the clinic loses the
 * Wi-Fi at the same moment and regains it at the same moment; without jitter they all retry in the
 * same second, and the access point that was struggling with one of them fails all of them —
 * repeatedly, in lockstep, for as long as the ladder runs.
 *
 * Half the step is fixed and half is random, rather than "anything up to the step": full jitter
 * would sometimes retry after a hundred milliseconds, which is not a backoff, and a queue of
 * hundreds of events would spend its first minute hammering a server that has already said no.
 */
export function backoffDelay(attempts: number, random: number): number {
  const index = Math.min(Math.max(attempts, 1), BACKOFF_LADDER_MS.length) - 1;
  const step = BACKOFF_LADDER_MS[index] ?? BACKOFF_LADDER_MS[BACKOFF_LADDER_MS.length - 1] ?? 2_000;
  return jittered(step, random);
}

/** Half the step fixed, half of it random. The one rule every wait in this file obeys. */
function jittered(step: number, random: number): number {
  const spread = Math.min(Math.max(random, 0), 1);
  return Math.round(step * (0.5 + spread * 0.5));
}

/**
 * How long to leave an event the clinic had no room for.
 *
 * Jittered like everything else here, and for a sharper reason than usual: a ceiling is a
 * property of one *device*, so the tablets that hit it hit it together — a ward's worth of
 * stations revoked in one administrative action, all coming back at the same minute for the rest
 * of the day.
 */
export function triageDelay(random: number): number {
  return jittered(TRIAGE_RETRY_MS, random);
}

/**
 * How long to wait when the server said how long to wait.
 *
 * Never less than it asked — that is the whole point of the header, and a client that shaved it
 * would be spending a request to be told the same thing again. Jitter is added *on top* rather
 * than around, for the same reason it is added everywhere else: every tablet in the building
 * receives the same number in the same second.
 */
export function retryAfterDelay(seconds: number, random: number): number {
  // Floored at a second, because a limiter rounding down to zero is saying "now" and a client
  // that took it literally would spin against the bucket it has just emptied. Still never less
  // than the server asked for: it asked for none.
  const asked = Math.min(Math.max(seconds * 1_000, 1_000), MAX_RETRY_AFTER_MS);
  const spread = Math.min(Math.max(random, 0), 1);
  return Math.round(asked * (1 + spread * 0.5));
}

/** What the server asked for, when it asked. Null when it did not, or when it was not an API. */
export function retryAfterOf(error: unknown): number | null {
  return error instanceof ApiError ? error.retryAfterSeconds : null;
}

// --- what goes in the next batch ---

export interface BatchOptions {
  now: number;
  limit?: number;
}

/**
 * The next batch, in the order it must be sent.
 *
 * **Creation order, not `occurred_at`.** The plan says "ordered by `occurred_at`, preserving
 * per-aggregate order", and those two come apart on exactly the device §13.10 asks about: one
 * whose clock is three hours wrong, or is corrected mid-session. Sorting by a wrong clock can put
 * the second reading for a patient before the first, and the server applies a batch **in the order
 * the client sent it** — so the sort would silently invert two events on one aggregate, which is
 * the ordering guarantee failing in the one case it was written for. The local sequence is
 * monotonic whatever the clock does, and within an aggregate it is the order the operator worked
 * in, which is what "preserved" has to mean.
 *
 * Three rules hold the queue's shape, and each one is a failure that is otherwise invisible:
 *
 *   - an event is not sent while an earlier event on the same record is still waiting (backoff,
 *     or in another batch) — otherwise the pair arrives out of order;
 *   - an event that *declares* a dependency (`expected_sequence`) is not sent while an earlier
 *     event on that record is unresolved, which is the client half of `BLOCKED`: the server would
 *     refuse it anyway, and resending it every tick would be a loop that looks like activity;
 *   - an event that declares no dependency proceeds anyway, exactly as the server treats it — an
 *     observation is a fact about a moment, not a mutation of a state, and holding a morning's
 *     work behind one typo is the failure `docs/sync.md` argues against at length.
 *
 * An event the clinic had no room to hold (`AWAITING_TRIAGE`) gets the third rule and a wait of its
 * own. It must **not** get the treatment `BLOCKED_LOCAL` gets, where a row with no
 * `expectedSequence` goes straight back into the next batch: that ceiling is device-wide rather
 * than an aggregate dependency, so the event would come back blocked, every tick, for as long as
 * the tablet had power. And it must not get the first rule either, which would hold every later
 * entry for that patient behind it for half an hour — the clinic does not do that itself. It
 * accepts an independent later event while an earlier one is blocked, in the same batch, for the
 * reason `docs/sync.md` gives at length: an observation is a fact about a moment, and a morning's
 * work must not be held hostage to one entry somebody else has to deal with.
 */
export function selectBatch(rows: OutboxRow[], options: BatchOptions): OutboxRow[] {
  const limit = options.limit ?? BATCH_LIMIT;
  const ordered = [...rows].sort((a, b) => a.seq - b.seq);
  const aggregates = new Map<string, 'waiting' | 'unresolved'>();
  const batch: OutboxRow[] = [];

  for (const row of ordered) {
    const key = `${row.aggregateType}/${row.aggregateId}`;
    const holding = aggregates.get(key);

    if (row.state === 'NEEDS_ATTENTION' || row.state === 'HELD') {
      // A person has to decide about this one. Later events on the same record that depend on its
      // state are now doomed until they do.
      aggregates.set(key, 'unresolved');
      continue;
    }
    if (row.state === 'IN_FLIGHT') {
      // Somebody else's batch is carrying it. Nothing after it on this record may overtake it.
      aggregates.set(key, 'waiting');
      continue;
    }
    if (row.state === 'AWAITING_TRIAGE' && row.nextAttemptAt > options.now) {
      // The clinic had nowhere to put this one. Still ours, and offered again when its wait is
      // up — but meanwhile it is treated as a refusal is: what declared a dependency on this
      // record waits, and what did not carries on. Once the wait is up it falls through and goes
      // in the next batch like any other row.
      aggregates.set(key, 'unresolved');
      continue;
    }
    if (row.nextAttemptAt > options.now) {
      aggregates.set(key, 'waiting');
      continue;
    }
    if (holding === 'waiting') continue;
    if (holding === 'unresolved' && row.expectedSequence !== null) continue;

    batch.push(row);
    if (batch.length >= limit) break;
  }

  return batch;
}

// --- what a receipt means ---

/** What to do with one event, given its outcome. */
export type EventAction = 'drop' | 'attention' | 'held' | 'retry' | 'ceiling';

/**
 * The five outcomes, and the five things a client does about them.
 *
 * `ACCEPTED` and `DUPLICATE` are the same action and different stories: the first is "it is in the
 * ledger now", the second is "it was already there, because you sent it before and never heard
 * back". Both mean the work is safe and the row leaves the queue.
 *
 * `BLOCKED` is the one outcome whose action depends on the reason as well, because the contract
 * gives it two and they are cleared by different people. `EARLIER_EVENT_FAILED` is resolved on
 * this tablet, so the event goes back in the queue and `selectBatch` holds it until the earlier
 * one is dealt with. `QUARANTINE_FULL` is resolved at the clinic, by a physician reading the
 * quarantine, and no amount of sending helps — so it is parked instead, and asking again is on a
 * clock measured in half-hours. Treating the second as the first is a hot loop; treating either as
 * `REJECTED` would throw away a real blood pressure because somebody is behind on their reading.
 */
export function actionFor(outcome: Outcome | string, reasonCode = ''): EventAction {
  switch (outcome) {
    case 'ACCEPTED':
    case 'DUPLICATE':
      return 'drop';
    case 'REJECTED':
      return 'attention';
    case 'QUARANTINED':
      return 'held';
    case 'BLOCKED':
      return reasonCode === REASON_CODES.quarantineFull ? 'ceiling' : 'retry';
    default:
      // An outcome this build does not know is not treated as success. A newer server saying
      // something new must never make a queued measurement disappear, so it stays and a person is
      // eventually told about it.
      return 'attention';
  }
}

export interface ResolvedEvent {
  eventId: string;
  action: EventAction;
  outcome: Outcome | string;
  reasonCode: string;
  reason: string;
  globalSeq: number | null;
}

export interface ReceiptPlan {
  resolved: ResolvedEvent[];
  /**
   * Events that were in the batch and are not in a **closed** receipt.
   *
   * The ordinary version of this case is handled a layer up and better: a batch the server opened
   * and never finished comes back `closed: false`, and resending it under the same id makes the
   * server reprocess it and answer per event. That is the receipt doing its job.
   *
   * What is left here is the anomaly — a receipt that says it is closed and does not mention an
   * event that was in the batch. It should not happen; if it does, the events go back to the queue
   * **without a batch id** and are sent in a fresh one, because asking again about a closed batch
   * would return the same silence for ever. Safe for one reason only: `event_id` is the ledger's
   * key, so anything that did land comes back `DUPLICATE`.
   */
  unanswered: string[];
  /** Results for events this device does not have. Should be empty; counted so a test can say so. */
  unknown: string[];
}

/** What a receipt means for the rows that were sent in it. */
export function planFromReceipt(rows: OutboxRow[], receipt: SyncReceipt): ReceiptPlan {
  const results = new Map<string, SyncResult>();
  for (const result of receipt.results ?? []) results.set(result.event_id, result);

  const plan: ReceiptPlan = { resolved: [], unanswered: [], unknown: [] };
  const sent = new Set(rows.map((row) => row.eventId));

  for (const row of rows) {
    const result = results.get(row.eventId);
    if (!result) {
      plan.unanswered.push(row.eventId);
      continue;
    }
    plan.resolved.push({
      eventId: row.eventId,
      action: actionFor(result.outcome, result.reason_code ?? ''),
      outcome: result.outcome,
      reasonCode: result.reason_code ?? '',
      reason: result.reason ?? '',
      globalSeq: typeof result.global_seq === 'number' ? result.global_seq : null,
    });
  }
  for (const id of results.keys()) if (!sent.has(id)) plan.unknown.push(id);
  return plan;
}

// --- failures ---

export type FailureKind =
  /** The request never arrived. A corridor, not a refusal. */
  | 'offline'
  /** 401. An expired token the client can refresh — or a device the clinic no longer accepts. */
  | 'unauthenticated'
  /** 403. This operator may not do this. Not retryable, and not the operator's fault to fix. */
  | 'forbidden'
  /** The batch was refused for its size. A client bug; the answer is smaller batches. */
  | 'too-large'
  /**
   * The batch named a device other than the one whose key signed it.
   *
   * Sent as a self-check — the clinic takes the sending device from the signature and only
   * *checks* the body — so a refusal here means this tablet's stored enrolment id and its key
   * have come apart. Nothing about sending again fixes that.
   */
  | 'device-mismatch'
  /**
   * The clinic is holding as many of this device's events as it will hold, and refused the whole
   * batch (`SYNC_QUARANTINE_FULL`, 429, no `Retry-After`).
   *
   * Its own kind rather than a `server` failure, because it is the one refusal about which the
   * client knows something certain: **nothing was written** — no batch row, no receipt, no
   * quarantine rows. Every other 429 leaves the honest answer "we do not know whether it landed",
   * and a client that does not know must keep the batch in flight and ask for a receipt. Here
   * asking would spend a request to be told about a batch the clinic never opened and would leave
   * the rows claiming to be in a batch that does not exist, which is a state no later code can
   * reason from.
   */
  | 'quarantine-full'
  /** 5xx, 408, 429. Retryable. */
  | 'server'
  /** Anything else the server said. Not retryable without a change. */
  | 'client';

export function classifyFailure(error: unknown): FailureKind {
  if (error instanceof NetworkError) return 'offline';
  if (error instanceof ApiError) {
    if (error.status === 401) return 'unauthenticated';
    if (error.status === 403) return 'forbidden';
    if (error.code === 'SYNC_BATCH_TOO_LARGE') return 'too-large';
    if (error.status === 422 && error.fields.device_id) return 'device-mismatch';
    // Read before `retryable`, which would see a 429 and call it a plain one. The code is the
    // only thing that separates the two, which is why §2 says a client branches on the code and
    // never on the status or the sentence.
    if (error.code === SYNC_QUARANTINE_FULL) return 'quarantine-full';
    if (error.retryable) return 'server';
    return 'client';
  }
  // Something that is not an API answer at all — a JSON body that would not parse, a bug here.
  // Treated as retryable, because the alternative is a queue that empties itself on a client bug.
  return 'server';
}

/** Whether a failure means "try again later" rather than "this will never work". */
export function retryable(kind: FailureKind): boolean {
  return (
    kind === 'offline' ||
    kind === 'server' ||
    kind === 'unauthenticated' ||
    // Later, and by a long way: what clears it is a person at the clinic. Still not "never",
    // because a tablet that gave up would need re-enrolling to deliver work it is still holding.
    kind === 'quarantine-full'
  );
}

// --- the clock ---

export type SkewLevel = 'unknown' | 'fine' | 'drifting' | 'wrong';

export interface SkewReading {
  level: SkewLevel;
  /** Milliseconds the device is ahead of the clinic. Negative when it is behind. */
  ms: number | null;
  /** Whole minutes, for the sentence a person reads. */
  minutes: number;
  ahead: boolean;
  /** True when entries recorded now would be held by the clinic rather than accepted. */
  entriesWouldBeHeld: boolean;
}

/**
 * What the measured skew means for the operator.
 *
 * The clinic holds an event dated more than five minutes in the *future* and accepts anything in
 * the past, however old — a device offline for a week is the whole point. So being behind is never
 * "wrong" in the protocol's sense, and is still worth saying, because a tablet three hours behind
 * writes a morning of measurements onto yesterday evening's timeline and nobody downstream can
 * tell. Neither case is fixed here: the fix is the device's clock, and the engine's job is to say
 * so rather than to quietly rewrite a clinical timestamp.
 */
export function skewReading(ms: number | null): SkewReading {
  if (ms === null) {
    return { level: 'unknown', ms: null, minutes: 0, ahead: false, entriesWouldBeHeld: false };
  }
  const size = Math.abs(ms);
  const minutes = Math.round(size / 60_000);
  const ahead = ms > 0;
  const held = ms > CLOCK_TOLERANCE_MS;
  let level: SkewLevel = 'fine';
  if (held || size >= CLOCK_TOLERANCE_MS) level = 'wrong';
  else if (size >= CLOCK_NOTICE_MS) level = 'drifting';
  return { level, ms, minutes, ahead, entriesWouldBeHeld: held };
}

// --- what the operator is shown ---

/** Why sync has stopped, when it has. Empty string means it has not. */
export const HALT_REASONS = {
  /** The refresh token was refused. Somebody has to sign in again; the queue is untouched. */
  sessionLost: 'SESSION_LOST',
  /** Signed requests keep being refused. The clinic no longer accepts this device. */
  deviceRefused: 'DEVICE_REFUSED',
  /** There is no room to record the answer to a sync, so syncing would lose the answer. */
  storageFull: 'STORAGE_FULL',
  /** This tablet's enrolment id and the key it signs with disagree. It has to be re-enrolled. */
  deviceMismatch: 'DEVICE_MISMATCH',
} as const;

export type HaltReason = (typeof HALT_REASONS)[keyof typeof HALT_REASONS] | '';

export interface SyncCounts {
  queued: number;
  inFlight: number;
  needsAttention: number;
  held: number;
  blocked: number;
  /** Events the clinic had no room to hold. Cleared by a person there, not by anything here. */
  awaitingTriage: number;
  /**
   * Every row in the outbox, counted rather than added up.
   *
   * The named states are what a screen labels; this is what "undelivered" means, and it is
   * deliberately not their sum. A state added to `OUTBOX_STATES` and forgotten in one of the four
   * places that used to add the five together would have produced a smaller number than the truth
   * — and the one number §13.9 says must never be wrong is this one, in the direction of saying
   * everything is delivered when it is not. Counting rows cannot make that mistake.
   */
  total: number;
}

export interface SyncMetrics extends SyncCounts {
  /** Everything that has not reached the clinic, whatever the reason. The honest headline. */
  undelivered: number;
  /**
   * True while this device is handing its backlog to the clinic's quarantine because it has been
   * revoked or suspended. The work is being delivered, and none of it is being accepted.
   */
  delivering: boolean;
  lastSuccessAt: number | null;
  lastAttemptAt: number | null;
  /**
   * When the clinic last answered that it has no room for this device's work.
   *
   * Null until it ever has. Shown beside the count it explains, and only while that count is
   * above zero — a time on its own, after the ceiling has cleared, would be a claim about a
   * state the tablet is no longer in.
   */
  noRoomAt: number | null;
  cursor: number;
  skew: SkewReading;
  halted: HaltReason;
}

/**
 * The status the operator sees, as a value (§13.9).
 *
 * `status` is deliberately not a colour and not a sentence: CP67 owns both. What is decided here
 * is the thing that must never be wrong — **`synced` requires an empty queue**. A pill that says
 * "synced" while anything is undelivered is the failure §13.9 says will be discovered exactly
 * once, after which nobody trusts the system again.
 */
export type SyncStatus = 'synced' | 'syncing' | 'queued' | 'attention' | 'stalled' | 'halted';

export function statusOf(metrics: SyncMetrics): SyncStatus {
  if (metrics.needsAttention > 0 || metrics.held > 0) return 'attention';
  if (metrics.halted !== '') return 'halted';
  // Above `syncing` and `queued`, and it is worth saying why a sixth status exists rather than
  // folding this into one of them. `queued` reads as "give it a minute", and these entries will
  // not move in any number of minutes: the clinic is holding as many of this tablet's events as
  // it will hold, and only a physician there can change that. Saying "waiting to be sent" would
  // be true of the row and false about the world, which is the failure mode a review of this app
  // has already caught once.
  if (metrics.awaitingTriage > 0) return 'stalled';
  if (metrics.inFlight > 0) return 'syncing';
  if (metrics.queued > 0 || metrics.blocked > 0) return 'queued';
  return 'synced';
}

/** Which control the sync screen offers. There is only ever one. */
export type SyncControl = 'check-with-clinic' | 'retry';

/**
 * Which one, decided here rather than in the component, for the reason `statusOf` is here.
 *
 * "Check with the clinic again" does everything "try again now" does and then offers the entries
 * the clinic had no room for as well, so where it applies it is strictly the better button and
 * the other one must not be on the screen beside it: two controls for one action, with the vaguer
 * of the two on top, ends with an operator pressing the vague one and watching the stalled
 * entries stay exactly where they were.
 */
export function controlFor(metrics: SyncMetrics): SyncControl {
  return metrics.awaitingTriage > 0 ? 'check-with-clinic' : 'retry';
}

/**
 * The message key for a reason code, in both languages.
 *
 * A code rather than the server's sentence, for the reason CP62 recorded: the prose is written for
 * whoever reads the log, and the operator needs a sentence in their own language that says what to
 * do. The server's `reason` is kept in the row beside it, for support.
 */
export function reasonKey(code: string): string {
  switch (code) {
    case REASON_CODES.deviceRevoked:
    case REASON_CODES.deviceSuspended:
      return 'reasonDeviceRefused';
    case REASON_CODES.clockImplausible:
      return 'reasonClock';
    case REASON_CODES.unknownType:
      return 'reasonUnknownType';
    case REASON_CODES.invalidPayload:
      return 'reasonInvalid';
    case REASON_CODES.sequenceConflict:
      return 'reasonConflict';
    case REASON_CODES.earlierFailed:
      return 'reasonEarlierFailed';
    case REASON_CODES.quarantineFull:
      return 'reasonQuarantineFull';
    default:
      return 'reasonRefused';
  }
}

// --- projections: §13.6's rule, as one comparison ---

export interface Stamp {
  occurredAt: string;
  eventId: string;
}

/**
 * Whether `incoming` should replace `stored` as the current value.
 *
 * §13.6, in one function: two devices recording a height for one patient is not a conflict, it is
 * two facts, and the current value is the later one by `occurred_at`. Both remain visible —
 * `local_events` keeps every one, and nothing here deletes anything.
 *
 * The tie-break on `event_id` is not a nicety. Without it, two events with the same `occurred_at`
 * applied in different orders on two devices leave two devices showing different current values,
 * for ever, with no error anywhere. Comparing the ids makes the result the same whatever order
 * they arrive in, which is the property reconciliation actually needs.
 */
export function replaces(incoming: Stamp, stored: Stamp | null): boolean {
  if (!stored) return true;
  if (incoming.occurredAt > stored.occurredAt) return true;
  if (incoming.occurredAt < stored.occurredAt) return false;
  return incoming.eventId > stored.eventId;
}

/** A value as it goes into a row: JSON for objects, and null stays null. */
export function jsonValue(value: unknown): Value {
  if (value === null || value === undefined) return null;
  return JSON.stringify(value);
}
