import { META_KEYS, TABLES, isStorageFull, type LocalStore, type Row } from '@/lib/local-store';

import {
  appendSyncLog,
  confirmEvent,
  dropFromOutbox,
  nextSequence,
  readCounts,
  readMeta,
  readNumber,
  readOutbox,
  recordEvent,
  updateOutbox,
  writeMeta,
  applyProjections,
} from './outbox';
import {
  BATCH_LIMIT,
  HALT_REASONS,
  MAX_AUTH_FAILURES,
  MAX_BATCHES_PER_TICK,
  PULL_PAGE,
  REASON_CODES,
  backoffDelay,
  classifyFailure,
  planFromReceipt,
  retryAfterDelay,
  retryAfterOf,
  selectBatch,
  toWireEvent,
  triageDelay,
  type FailureKind,
  type HaltReason,
  type OutboxRow,
  type PulledEvent,
  type SyncReceipt,
} from './state';

import type { AppliedEvent } from './projections';
import type { SyncTransport } from './transport';

/**
 * The sync engine (CP66).
 *
 * # The shape of one attempt
 *
 *  1. **Resolve whatever is in flight.** If the last tick sent a batch and never heard, ask for its
 *     receipt — with the same id, which is what the receipt exists for. A batch the clinic has
 *     never seen comes back as nothing, and is simply sent again.
 *  2. **Push**, in batches, in the order things were recorded, one batch at a time.
 *  3. **Pull**, applying each event to the local record and advancing the cursor only past the ones
 *     that actually applied.
 *  4. **Ask whether the catalogues moved**, which is one small request and a fingerprint compare.
 *
 * # One batch in flight at a time
 *
 * Not for throughput — for the ability to say what happened. A receipt answers one batch; two in
 * flight means two unresolved questions and an ordering guarantee that depends on which answer
 * arrives first. Serialising them makes every failure recoverable by one rule: *there is at most
 * one unanswered batch, and its id is on the rows it carries.*
 *
 * # What this engine will not do
 *
 * It will not resolve a conflict (§13.6). A `REJECTED` event stops being sent and starts being
 * visible; nothing here rewrites a payload, drops an event, or decides that a measurement was
 * probably wrong. It will not correct a clock: a device three hours out records three-hours-out
 * timestamps and says so, because the alternative is a client silently asserting when a clinical
 * measurement happened. And it will not empty the queue to recover from anything — the only thing
 * that removes an event from the outbox is the clinic saying it has it.
 */

export interface EngineDeps {
  store: LocalStore;
  transport: SyncTransport;
  /** This device's enrolment id, or null. Without one the push is skipped and the pull is not. */
  deviceId: string | null;
  now?: () => number;
  random?: () => number;
  newId?: () => string;
  batchLimit?: number;
  maxBatches?: number;
  pullPage?: number;
  /** How many pages one attempt will pull before yielding. */
  maxPages?: number;
}

export interface SyncReport {
  batches: number;
  accepted: number;
  duplicated: number;
  attention: number;
  held: number;
  blocked: number;
  /** Events the clinic had no room to hold. Kept, and offered again after a triage-length wait. */
  ceilinged: number;
  /** Events that were sent and not mentioned in the receipt; requeued under a fresh batch. */
  unanswered: number;
  pulled: number;
  cursor: number;
  /** Catalogues whose fingerprint no longer matches what this device holds. */
  stale: string[];
  failure: FailureKind | null;
  halted: HaltReason;
  /** True when nothing was attempted because a batch is waiting out its backoff. */
  waiting: boolean;
  /**
   * True while this device is handing its backlog to the clinic's quarantine because it has been
   * revoked or suspended. Sync stops for good once there is nothing left to hand over.
   */
  delivering: boolean;
}

function emptyReport(): SyncReport {
  return {
    batches: 0,
    accepted: 0,
    duplicated: 0,
    attention: 0,
    held: 0,
    blocked: 0,
    ceilinged: 0,
    unanswered: 0,
    pulled: 0,
    cursor: 0,
    stale: [],
    failure: null,
    halted: '',
    waiting: false,
    delivering: false,
  };
}

interface Context {
  deps: EngineDeps;
  now: () => number;
  random: () => number;
  newId: () => string;
  report: SyncReport;
}

/** Stop syncing, with a reason a person can be shown. Nothing queued is touched. */
export async function halt(store: LocalStore, reason: HaltReason): Promise<void> {
  await writeMeta(store, META_KEYS.haltReason, reason);
}

/**
 * Start again.
 *
 * Deliberately explicit: a halt means somebody has to do something — sign in, or take the tablet to
 * an administrator — and an engine that cleared its own halt would retry forever against a device
 * the clinic has refused, which is the loop CP66 says to avoid.
 */
export async function resume(store: LocalStore): Promise<void> {
  await writeMeta(store, META_KEYS.haltReason, '');
  await writeMeta(store, META_KEYS.authFailures, 0);
  await writeMeta(store, META_KEYS.delivering, '');
}

/**
 * What happened when the operator asked the tablet to check with the clinic again.
 *
 * Three answers rather than a boolean, because the screen and the tests both need to tell "the
 * clinic was asked" from "there was nothing to ask about".
 */
export type CeilingCheck = 'asking' | 'too-soon' | 'nothing-to-check';

/**
 * The shortest interval between two operator-initiated checks.
 *
 * Measured from the clinic's last answer rather than from the last press, which is the property
 * that makes it both bounded and useful: a press that reached the clinic sets the clock, so a
 * bored operator gets one question every half minute however many times they press, while a press
 * that never left the building sets nothing and may be repeated at once — because nothing was
 * learned and nothing was spent.
 */
export const CHECK_INTERVAL_MS = 30_000;

/**
 * Offer the entries the clinic had no room for again, now, because somebody asked.
 *
 * The half-hour wait is right for a tablet nobody is standing over and wrong for the one case a
 * person is: the operator has telephoned the clinic, a physician has worked through the list, and
 * they would like to see the work go rather than leave the tablet on a desk for half an hour.
 *
 * Two things this deliberately does not do.
 *
 * It moves **only** rows in `AWAITING_TRIAGE`. Nothing else in the queue is any of this button's
 * business, and one row in particular must not be touched: a batch left in flight behind a
 * `Retry-After` is waiting because the clinic asked it to, and shortening that would be a client
 * spending the budget the server has just told it it does not have. Those rows are not in this
 * state, so the limiter's wait stands, and `resolveInFlight` will hold the whole attempt back
 * until it is up whatever this does.
 *
 * And it does not change any row's **state** — only when it is next due. A ceilinged event is
 * still ceilinged until the clinic says otherwise, and a client that promoted it to `PENDING` on
 * a press would be announcing an outcome nobody has been told yet.
 */
export async function checkWithClinic(
  store: LocalStore,
  options: { now?: () => number } = {},
): Promise<CeilingCheck> {
  const now = (options.now ?? Date.now)();
  const waiting = await readOutbox(store, ['AWAITING_TRIAGE']);
  if (waiting.length === 0) return 'nothing-to-check';
  if (now - (await readNumber(store, META_KEYS.noRoomAt, 0)) < CHECK_INTERVAL_MS) return 'too-soon';

  await store.transaction(async (tx) => {
    for (const row of waiting) await updateOutbox(tx, row.eventId, { next_attempt_at: 0 });
  });
  return 'asking';
}

/** The refresh token was refused. The queue is untouched; somebody signs in and it resumes. */
export async function noteSessionLost(store: LocalStore): Promise<void> {
  await halt(store, HALT_REASONS.sessionLost);
}

/**
 * Somebody has signed in. Clear the halt **only** if it was the session.
 *
 * A device the clinic has refused, or one with no room left, is not fixed by a different operator
 * signing in — and clearing those here would produce exactly the loop CP66 says to avoid: a halt
 * that lifts itself every time somebody touches the tablet. Those two are cleared by `resume`,
 * which is a person deciding to try again.
 */
export async function resumeAfterSignIn(store: LocalStore): Promise<boolean> {
  const halted = await readMeta(store, META_KEYS.haltReason);
  if (halted !== HALT_REASONS.sessionLost) return false;
  await resume(store);
  return true;
}

/** One synchronisation attempt. Never throws: everything it can survive, it records. */
export async function syncOnce(deps: EngineDeps): Promise<SyncReport> {
  const context: Context = {
    deps,
    now: deps.now ?? Date.now,
    random: deps.random ?? Math.random,
    newId: deps.newId ?? (() => globalThis.crypto.randomUUID()),
    report: emptyReport(),
  };
  const { store } = deps;
  const report = context.report;

  const halted = ((await readMeta(store, META_KEYS.haltReason)) ?? '') as HaltReason;
  if (halted !== '') {
    report.halted = halted;
    return report;
  }

  try {
    await writeMeta(store, META_KEYS.lastPushAt, context.now());
    // Set once the clinic has told us, event by event, that it is holding this device's work:
    // there is a backlog to hand over and nothing else this device may do.
    report.delivering = (await readMeta(store, META_KEYS.delivering)) === '1';

    const resolved = await resolveInFlight(context);
    if (resolved === 'waiting') {
      report.waiting = true;
      return report;
    }
    if (resolved === 'failed') return report;

    if (deps.deviceId) {
      await pushLoop(context, deps.deviceId);
      if (report.failure) return report;
    }

    if (report.delivering) {
      // Handing the backlog over to the quarantine. Nothing else on this device may talk to the
      // clinic, so the pull and the catalogues are skipped rather than attempted and counted as
      // failures — and once there is nothing left to hand over, sync stops for good.
      const counts = await readCounts(store);
      // `awaitingTriage` counts here, and the omission would be the whole bug: those events are
      // still to be handed over — the clinic simply has no room for them yet — and a device that
      // treated them as nothing left to do would halt itself with `DEVICE_REFUSED`, which only an
      // operator pressing something on the tablet can clear. The person who can actually fix this
      // is at the clinic, reading the quarantine, and they must be able to fix it from there.
      const left = counts.queued + counts.inFlight + counts.blocked + counts.awaitingTriage;
      if (left === 0) {
        await writeMeta(store, META_KEYS.delivering, '');
        await halt(store, HALT_REASONS.deviceRefused);
        report.halted = HALT_REASONS.deviceRefused;
      }
      return report;
    }

    await pullLoop(context);
    if (report.failure) return report;

    await referenceCheck(context);
    if (report.failure) return report;

    // A tick that got all the way here with nothing left to say. This is the number the operator
    // reads as "last synced", so it is written only on a clean pass — a partial success that
    // updated it would be the indicator saying "synced" over a non-empty queue.
    await writeMeta(store, META_KEYS.lastSuccessAt, context.now());
    await writeMeta(store, META_KEYS.authFailures, 0);
  } catch (error) {
    // The one failure that is not the network: no room to record what happened. Halting is the
    // honest response — continuing would mean asking the clinic questions whose answers this
    // device cannot write down, and an answer it cannot write down is one it will ask for again
    // for ever.
    if (isStorageFull(error)) {
      report.halted = HALT_REASONS.storageFull;
      report.failure = 'client';
      // Best-effort, both of them: on a device with no room left, writing down *why* it stopped
      // can fail too. The report says so either way, and a halt that could not be recorded means
      // the next attempt tries again — which is the harmless direction to be wrong in.
      try {
        await halt(store, HALT_REASONS.storageFull);
        await log(context, 'push', 'STORAGE_FULL', {});
      } catch {
        report.halted = '';
      }
      return report;
    }
    throw error;
  }

  report.cursor = await readNumber(store, META_KEYS.cursor, 0);
  return report;
}

// --- the in-flight batch ---

async function resolveInFlight(
  context: Context,
): Promise<'nothing' | 'done' | 'waiting' | 'failed'> {
  const { store, transport, deviceId } = context.deps;
  const rows = await readOutbox(store, ['IN_FLIGHT']);
  if (rows.length === 0) return 'nothing';

  const due = rows.every((row) => row.nextAttemptAt <= context.now());
  if (!due) return 'waiting';

  // Counted here as well as in `pushLoop`, because a batch that stays in flight is retried from
  // this branch and never from that one: without it a batch stuck behind a bad connection would
  // ask again at the first rung of the ladder for ever, which is §13.5's backoff not existing at
  // all in the one case it was written for. (Found by mutation: an engine that regenerated
  // `event_id` from the third attempt onwards passed every test, because no batch ever reached a
  // third attempt.)
  const attempted = rows.map((row) => ({ ...row, attempts: row.attempts + 1 }));
  await store.transaction(async (tx) => {
    for (const row of attempted) {
      await updateOutbox(tx, row.eventId, { attempts: row.attempts });
    }
  });

  const batchId = rows[0]?.batchId ?? null;
  if (batchId === null) {
    // A row in flight with no receipt number cannot be asked about. It has never been in this
    // state in practice, and if it ever is, the safe answer is to send it again: `event_id`
    // idempotency means a second delivery is a duplicate, not a second measurement.
    await requeue(context, attempted, 0);
    return 'done';
  }

  try {
    // The receipt first — except while handing a backlog over, where asking for it is the bug
    // this branch exists to prevent.
    //
    // `GET /v1/sync/batches/{id}` is **not** one of the routes a refused device may reach; the
    // exemption is `POST /v1/sync/events` and nothing else. So a revoked tablet whose push was
    // interrupted by the network asks about its own batch, is answered 401, counts that as a
    // refusal, and three ticks later has halted with `DEVICE_REFUSED` and a morning of blood
    // pressures still on it — the silent loss the quarantine exists to prevent, arriving down
    // the recovery path. Worse, it never gets out: `resolveInFlight` runs before the push loop,
    // so a device stuck here never pushes again, and no amount of resuming changes that.
    //
    // Re-pushing under the same id is not a workaround for the exemption; it is the better
    // question in its own right. `Push` checks the stored receipt **before** it reads the
    // device, so a batch that closed is answered from the record without touching the ledger —
    // the receipt lookup, arriving through the one route this device may use — and a batch left
    // open is reprocessed, which is safe because `event_id` is the ledger's key. What comes back
    // is the same answer the receipt route would have given, per event.
    //
    // Only while `delivering`. Outside a handover the receipt route works, it is the cheaper
    // question, and a client that guessed otherwise would resend fifty events to learn something
    // one small request answers.
    //
    // This is the lost-response case either way: the clinic may have processed all fifty events
    // and the answer may have died on the way back, and asking is the only way to tell that apart
    // from a push that never arrived.
    let receipt = context.report.delivering ? null : await transport.receipt(batchId);
    // Three ways to arrive here, all meaning "send it again, under the same id":
    //
    //   - **nothing** — the clinic has never seen this batch, so the push never arrived;
    //   - **`closed: false`** — it was opened and never finished, a server interrupted halfway.
    //     Such a receipt reports nothing, and a push under that id is reprocessed rather than
    //     answered from it, so resending gets the real per-event answers. That is the whole point
    //     of the receipt, and it is why this client does **not** start a fresh batch here.
    //   - **we did not ask**, because this is a handover and the receipt route is shut to us.
    if (receipt === null || receipt.closed === false) {
      if (!deviceId) {
        await requeue(context, attempted, 0);
        return 'done';
      }
      receipt = await transport.push({
        batch_id: batchId,
        device_id: deviceId,
        client_clock: new Date(context.now()).toISOString(),
        events: attempted.map(toWireEvent),
      });
    }
    await applyReceipt(context, attempted, receipt);
    await log(context, 'receipt', 'RESOLVED', { batchId, receipt });
    return 'done';
  } catch (error) {
    if (isStorageFull(error)) throw error;
    await failed(context, error, attempted, 'receipt');
    return 'failed';
  }
}

// --- pushing ---

async function pushLoop(context: Context, deviceId: string): Promise<void> {
  const { store, transport } = context.deps;
  const limit = context.deps.batchLimit ?? BATCH_LIMIT;
  const maxBatches = context.deps.maxBatches ?? MAX_BATCHES_PER_TICK;

  for (let attempt = 0; attempt < maxBatches; attempt += 1) {
    const queue = await readOutbox(store);
    const batch = selectBatch(queue, { now: context.now(), limit });
    if (batch.length === 0) return;

    // The receipt number, made once for this batch and written to the rows **before** the request
    // leaves. An app killed between here and the response comes back to rows that say which batch
    // they are in, which is the whole recovery.
    const batchId = context.newId();
    const at = context.now();
    await store.transaction(async (tx) => {
      for (const row of batch) {
        await updateOutbox(tx, row.eventId, {
          state: 'IN_FLIGHT',
          batch_id: batchId,
          attempts: row.attempts + 1,
          next_attempt_at: at,
        });
      }
    });
    const sent = batch.map((row) => ({ ...row, attempts: row.attempts + 1 }));

    try {
      const receipt = await transport.push({
        batch_id: batchId,
        device_id: deviceId,
        // The clinic measures the skew from this and reports it on every receipt, so a device
        // learns its clock is drifting before the drift is large enough to hold a morning's work.
        client_clock: new Date(at).toISOString(),
        events: batch.map(toWireEvent),
      });
      context.report.batches += 1;
      await applyReceipt(context, sent, receipt);
      await log(context, 'push', 'SENT', { batchId, receipt });
    } catch (error) {
      // A device with no room to write down the clinic's answer is not a network failure, and
      // must not be treated as one: the rows would go back to `PENDING` with their batch id
      // cleared, and the answer that has already been given would be unaskable. It goes up to
      // `syncOnce`, which halts and leaves the batch in flight for the next attempt to resolve.
      if (isStorageFull(error)) throw error;
      await failed(context, error, sent, 'push');
      return;
    }
  }
}

/**
 * What a receipt does to the queue.
 *
 * One transaction for the whole receipt: the clinic's answer is atomic, and applying half of it
 * would leave rows claiming to be in a batch that has already been resolved.
 */
async function applyReceipt(
  context: Context,
  rows: OutboxRow[],
  receipt: SyncReceipt,
): Promise<void> {
  const { store } = context.deps;
  const plan = planFromReceipt(rows, receipt);
  const at = context.now();
  const report = context.report;

  await store.transaction(async (tx) => {
    for (const resolved of plan.resolved) {
      switch (resolved.action) {
        case 'drop':
          // ACCEPTED and DUPLICATE. Both mean the work is in the ledger; the difference is only
          // whether this device had already been told.
          //
          // The batch's `received_at` stands in for the event's `recorded_at` until the event
          // comes back down on a pull carrying the clinic's own. `global_seq` is carried on a
          // replayed receipt as well as a computed one, so a batch resolved after a lost
          // response still records where its events landed.
          await confirmEvent(tx, resolved.eventId, resolved.globalSeq, receipt.received_at ?? null);
          await dropFromOutbox(tx, resolved.eventId);
          if (resolved.outcome === 'DUPLICATE') report.duplicated += 1;
          else report.accepted += 1;
          break;
        case 'attention':
          // §13.6, and §13.5's ladder: it stops being retried and starts being a person's
          // problem. Never deleted, never rewritten, never counted as delivered.
          await updateOutbox(tx, resolved.eventId, {
            state: 'NEEDS_ATTENTION',
            reason_code: resolved.reasonCode,
            reason: resolved.reason,
            batch_id: null,
          });
          report.attention += 1;
          break;
        case 'held':
          await updateOutbox(tx, resolved.eventId, {
            state: 'HELD',
            reason_code: resolved.reasonCode,
            reason: resolved.reason,
            batch_id: null,
          });
          report.held += 1;
          break;
        case 'retry':
          // BLOCKED: not attempted, because an earlier event on the same record failed. Ours to
          // send again once that one is resolved — and held back until then by `selectBatch`,
          // rather than resent into another BLOCKED every tick.
          await updateOutbox(tx, resolved.eventId, {
            state: 'BLOCKED_LOCAL',
            reason_code: resolved.reasonCode || REASON_CODES.earlierFailed,
            reason: resolved.reason,
            batch_id: null,
            next_attempt_at: 0,
          });
          report.blocked += 1;
          break;
        case 'ceiling':
          // BLOCKED, QUARANTINE_FULL: also not attempted, and everything else about it is
          // different. There is no earlier event to resolve and nothing on this tablet to fix —
          // the clinic is holding as many of this device's events as it will hold, and the
          // allowance is cleared by somebody reading the quarantine rather than by time passing.
          //
          // So it is parked, in a state of its own, with a wait measured against a person's
          // attention rather than against a network. Sending it again in this tick — which is
          // what the branch above would do, because a ceilinged event has no `expectedSequence`
          // for `selectBatch` to hold it back by — would produce the same answer, immediately,
          // for as long as the battery lasted: the loop that looks like activity.
          //
          // It is not lost, not rewritten and not delivered. Same event id, same payload, same
          // `occurred_at`, still in the outbox, still counted as undelivered.
          await updateOutbox(tx, resolved.eventId, {
            state: 'AWAITING_TRIAGE',
            reason_code: resolved.reasonCode || REASON_CODES.quarantineFull,
            reason: resolved.reason,
            batch_id: null,
            next_attempt_at: at + triageDelay(context.random()),
          });
          report.ceilinged += 1;
          break;
      }
    }

    if (plan.resolved.some((one) => one.action === 'ceiling')) {
      // The clinic has just said, event by event, that it has no room. This is one of the two
      // moments the sync screen's "last checked" line is allowed to move, and it is inside the
      // same transaction as the parking so that the time and the rows cannot disagree.
      await writeMeta(tx, META_KEYS.noRoomAt, at);
    }

    for (const eventId of plan.unanswered) {
      // See `ReceiptPlan.unanswered`: a **closed** receipt that does not mention an event should
      // not happen, and asking again about a closed batch would return the same silence for
      // ever — so it goes back to the queue without a batch id and is sent in a new one.
      //
      // With a backoff rather than at once. Something is wrong on the other side, and an event
      // that came back unmentioned twice would otherwise be re-sent as fast as this loop can
      // build batches.
      const row = rows.find((one) => one.eventId === eventId);
      await updateOutbox(tx, eventId, {
        state: 'PENDING',
        batch_id: null,
        next_attempt_at: at + backoffDelay(row?.attempts ?? 1, context.random()),
      });
    }
    report.unanswered += plan.unanswered.length;

    if (typeof receipt.clock_skew_ms === 'number') {
      await writeMeta(tx, META_KEYS.clockSkewMs, receipt.clock_skew_ms);
      await writeMeta(tx, META_KEYS.clockSkewAt, at);
    }
    await writeMeta(tx, META_KEYS.authFailures, 0);
  });

  // A device the clinic no longer trusts.
  //
  // **Not a reason to stop sending.** `POST /v1/sync/events` is the one route a correctly-signed
  // but no-longer-active device may reach, precisely so that a tablet revoked at nine — offline
  // since eight, holding a morning of real measurements on patients who have gone home — can put
  // them somewhere a person will see them. Every one of them is held at the clinic rather than
  // accepted, which is the point: accepting defeats the revocation, dropping loses the morning.
  //
  // So the engine keeps pushing until the queue has nothing left to send, and only then halts.
  // Everything else — the pull, the catalogues, the device's own state — will be refused with a
  // 401, so those are skipped meanwhile rather than counted as failures.
  const refused = plan.resolved.some(
    (resolved) =>
      resolved.reasonCode === REASON_CODES.deviceRevoked ||
      resolved.reasonCode === REASON_CODES.deviceSuspended,
  );
  if (refused) {
    await writeMeta(store, META_KEYS.delivering, '1');
    context.report.delivering = true;
  } else if (context.report.delivering && plan.resolved.some((one) => one.action === 'drop')) {
    // The clinic has started accepting this device's events again — an administrator reinstated
    // it part way through the handover. Without this the tablet would finish delivering and then
    // halt as though it were still refused, which is a working device stopping itself.
    await writeMeta(store, META_KEYS.delivering, '');
    context.report.delivering = false;
  }
}

/** A request that did not work. Nothing is dropped; the rows keep their batch and wait. */
async function failed(
  context: Context,
  error: unknown,
  rows: OutboxRow[],
  phase: 'push' | 'receipt' | 'pull' | 'reference',
): Promise<void> {
  const { store } = context.deps;
  const kind = classifyFailure(error);
  context.report.failure = kind;

  if (kind === 'device-mismatch') {
    // This tablet's stored enrolment id is not the device its key signs as. Nothing about
    // sending again will change that — the enrolment has to be repaired — so sync stops with a
    // reason rather than retrying a refusal every five minutes for ever. The queue is untouched.
    await halt(store, HALT_REASONS.deviceMismatch);
    context.report.halted = HALT_REASONS.deviceMismatch;
  }

  if (kind === 'unauthenticated') {
    // The refreshing fetch has already tried a refresh and a retry by the time this is seen, so a
    // 401 here is not an expired access token — it is a refused *session* or a refused *device*.
    // Three in a row is a device the clinic will not accept, and continuing would be a loop.
    const failures = (await readNumber(store, META_KEYS.authFailures, 0)) + 1;
    await writeMeta(store, META_KEYS.authFailures, failures);
    if (failures >= MAX_AUTH_FAILURES) {
      await halt(store, HALT_REASONS.deviceRefused);
      context.report.halted = HALT_REASONS.deviceRefused;
    }
  }

  if (kind === 'quarantine-full') {
    // The whole batch was turned away because the clinic has nowhere left to put this device's
    // work, and this is the only refusal in the protocol that tells the client something
    // certain: **nothing was written**. No batch row, no receipt, no quarantine rows (§6e, and
    // the 429 entry in the contract). So the batch id is worth nothing — asking for its receipt
    // would spend one of a limited number of requests to be told the clinic has never heard of
    // it — and the rows go back to being ours, parked exactly as a per-event `QUARANTINE_FULL`
    // is, because it is the same ceiling reached by a different door.
    //
    // Only a device the clinic has **refused** is answered this way. That makes the 429 an
    // answer to a second question as well: this tablet is no longer trusted. Recording that here
    // is not tidiness — without it the tick carries on to the pull, collects a 401, and three
    // ticks later the tablet has halted itself with `DEVICE_REFUSED`, a halt only somebody
    // standing over the device can clear. The one person who can actually fix this is at the
    // clinic with the quarantine open, and they must be able to fix it from there.
    await parkForTriage(context, rows);
    await writeMeta(store, META_KEYS.delivering, '1');
    context.report.delivering = true;
    context.report.ceilinged += rows.length;
  } else {
    const attempts = rows[0]?.attempts ?? 1;
    const asked = retryAfterOf(error);
    const wait =
      kind === 'too-large'
        ? 0
        : asked !== null
          ? // The server said how long. It knows the state of its own bucket and this client does
            // not, so its number wins over the ladder — a client that guessed shorter would spend
            // requests being refused, and one that guessed longer would leave a morning of
            // measurements on the tablet for no reason.
            retryAfterDelay(asked, context.random())
          : backoffDelay(attempts, context.random());

    if (kind === 'offline' || kind === 'server' || kind === 'unauthenticated') {
      // Left **in flight**, with the batch id. We do not know whether the clinic processed it —
      // that is precisely what a lost response looks like — so the next attempt asks for the
      // receipt rather than guessing. Waiting costs a small request; guessing costs either a
      // duplicate or a lost measurement.
      await store.transaction(async (tx) => {
        for (const row of rows) {
          await updateOutbox(tx, row.eventId, { next_attempt_at: context.now() + wait });
        }
      });
    } else {
      // The clinic answered and the answer was about the request rather than the events: too big,
      // malformed, or this operator may not send it. The events are fine, so they go back to the
      // queue with the reason recorded — never to "needs attention", which would put fifty
      // measurements in front of an operator because of one wrong header.
      await requeue(context, rows, context.now() + wait, kind);
    }
  }

  await log(context, phase, failureCode(kind), {
    batchId: rows[0]?.batchId ?? null,
    detail: error instanceof Error ? error.name : 'unknown',
  });
}

/**
 * Hold rows back until the clinic has room, keeping every one of them.
 *
 * The batch id is dropped, which is safe for one reason and it is the same reason the rest of
 * this file leans on: `event_id` is the ledger's key. If this were ever wrong about nothing
 * having landed, the events would be sent again in a fresh batch and come back `DUPLICATE`.
 */
async function parkForTriage(context: Context, rows: OutboxRow[]): Promise<void> {
  const answeredAt = context.now();
  const at = answeredAt + triageDelay(context.random());
  await context.deps.store.transaction(async (tx) => {
    // The other of the two moments "last checked" may move: the clinic answered, for the whole
    // batch at once, and the answer was still no room.
    await writeMeta(tx, META_KEYS.noRoomAt, answeredAt);
    for (const row of rows) {
      await updateOutbox(tx, row.eventId, {
        state: 'AWAITING_TRIAGE',
        reason_code: REASON_CODES.quarantineFull,
        reason: '',
        batch_id: null,
        next_attempt_at: at,
      });
    }
  });
}

function failureCode(kind: FailureKind): string {
  switch (kind) {
    case 'offline':
      return 'OFFLINE';
    case 'quarantine-full':
      return 'QUARANTINE_FULL';
    case 'unauthenticated':
      return 'REFUSED_AUTH';
    case 'forbidden':
      return 'REFUSED_PERMISSION';
    case 'too-large':
      return 'BATCH_TOO_LARGE';
    case 'device-mismatch':
      return 'DEVICE_MISMATCH';
    case 'server':
      return 'SERVER_ERROR';
    default:
      return 'REFUSED';
  }
}

async function requeue(
  context: Context,
  rows: OutboxRow[],
  nextAttemptAt: number,
  kind?: FailureKind,
): Promise<void> {
  await context.deps.store.transaction(async (tx) => {
    for (const row of rows) {
      await updateOutbox(tx, row.eventId, {
        state: 'PENDING',
        batch_id: null,
        next_attempt_at: nextAttemptAt,
        ...(kind ? { reason_code: failureCode(kind), reason: '' } : {}),
      });
    }
  });
}

// --- pulling and reconciling ---

function appliedEventOf(event: PulledEvent): AppliedEvent {
  return {
    eventId: event.event_id,
    aggregateType: event.aggregate_type,
    aggregateId: event.aggregate_id,
    patientId: event.patient_id ?? null,
    visitId: event.visit_id ?? null,
    eventType: event.event_type,
    eventVersion: event.event_version,
    occurredAt: event.occurred_at,
    recordedAt: event.recorded_at,
    payload: (event.payload ?? {}) as Record<string, unknown>,
    globalSeq: event.global_seq,
    actor: {
      userId: event.actor_user_id,
      role: event.actor_role,
      station: event.actor_station,
      source: event.source,
    },
  };
}

/**
 * Apply pulled events to the local record.
 *
 * Idempotent by construction: the event is inserted with `ignore` on conflict, so an event this
 * device already has — including one it wrote itself and is now seeing come back — changes
 * nothing except its confirmation. The projection is applied by `occurred_at`, so applying the
 * same event twice is a no-op and applying two events in either order gives the same answer.
 *
 * **The cursor advances only past events that were actually applied.** If the tenth of forty
 * throws, the cursor stops at the ninth and the next pull starts there. The alternative — advance
 * to the page's cursor and move on — loses the tenth for ever, silently, which is the exact
 * failure class this checkpoint exists to prevent.
 */
export async function applyPulled(
  store: LocalStore,
  events: PulledEvent[],
  at: number,
): Promise<{ applied: number; cursor: number | null; stopped: unknown | null }> {
  let applied = 0;
  let cursor: number | null = null;
  for (const event of events) {
    const local = appliedEventOf(event);
    try {
      await store.transaction(async (tx) => {
        const seq = await nextSequence(tx);
        await recordEvent(tx, local, { origin: 'SERVER', seq, at });
        // This is also the second, independent way an event leaves the outbox: seeing our own
        // event come back from the ledger proves the clinic has it, whatever happened to the
        // receipt. A lost response that somehow escaped the batch machinery is resolved here.
        await confirmEvent(tx, local.eventId, local.globalSeq, local.recordedAt);
        await dropFromOutbox(tx, local.eventId);
        await applyProjections(tx, local, { confirmed: true, at });
      });
    } catch (error) {
      return { applied, cursor, stopped: error };
    }
    applied += 1;
    cursor = event.global_seq;
  }
  return { applied, cursor, stopped: null };
}

async function pullLoop(context: Context): Promise<void> {
  const { store, transport } = context.deps;
  const pageSize = context.deps.pullPage ?? PULL_PAGE;
  const maxPages = context.deps.maxPages ?? 10;

  for (let page = 0; page < maxPages; page += 1) {
    const since = await readNumber(store, META_KEYS.cursor, 0);
    let answer;
    try {
      answer = await transport.pull(since, pageSize);
    } catch (error) {
      await failed(context, error, [], 'pull');
      return;
    }

    const result = await applyPulled(store, answer.events ?? [], context.now());
    context.report.pulled += result.applied;

    // Only past what was applied. An empty page still moves the cursor to the page's own — which
    // is `since` — so a client that had to infer it from the last event does not ask the same
    // question for ever.
    const advanced = result.stopped === null ? (answer.cursor ?? since) : (result.cursor ?? since);
    if (advanced !== since) {
      await writeMeta(store, META_KEYS.cursor, advanced);
    }
    await writeMeta(store, META_KEYS.lastPullAt, context.now());
    context.report.cursor = advanced;

    if (result.stopped !== null) {
      if (isStorageFull(result.stopped)) throw result.stopped;
      context.report.failure = 'client';
      await log(context, 'pull', 'APPLY_FAILED', { detail: 'projection' });
      return;
    }
    if (!answer.more) return;
  }
}

// --- reference data ---

/**
 * Ask whether the catalogues this device is holding have moved.
 *
 * One request and a fingerprint compare. The engine does **not** refetch: which catalogue matters
 * and what a stale one means is the feature's business — a station may carry on with a
 * fortnight-old terminology list and must not with a stale formulary — so this reports and the
 * features decide.
 */
async function referenceCheck(context: Context): Promise<void> {
  const { store, transport } = context.deps;
  let answer;
  try {
    answer = await transport.reference();
  } catch (error) {
    await failed(context, error, [], 'reference');
    return;
  }

  const at = context.now();
  const held = new Map<string, Row>();
  for (const row of await store.all({ table: TABLES.referenceCache })) {
    held.set(String(row.catalogue), row);
  }
  const stale: string[] = [];
  for (const catalogue of answer.catalogues) {
    const current = held.get(catalogue.catalogue);
    if (!current || String(current.fingerprint) !== catalogue.fingerprint) {
      stale.push(catalogue.catalogue);
    }
  }
  context.report.stale = stale;

  // The clock, measured a second way. A tick that pushed nothing still learns the skew, which
  // matters for the device that has been recording all morning and has not managed to send yet —
  // the one whose entries are about to be held.
  const serverTime = Date.parse(answer.server_time);
  if (Number.isFinite(serverTime)) {
    const measured = at - serverTime;
    const last = await readNumber(store, META_KEYS.clockSkewAt, 0);
    if (last < at) {
      await writeMeta(store, META_KEYS.clockSkewMs, measured);
      await writeMeta(store, META_KEYS.clockSkewAt, at);
    }
  }
  if (stale.length > 0) {
    await log(context, 'reference', 'STALE', { detail: String(stale.length) });
  }
}

/** What this device holds of a catalogue, after a feature has refetched it. */
export async function cacheCatalogue(
  store: LocalStore,
  catalogue: string,
  fingerprint: string,
  document: unknown,
  at: number,
): Promise<void> {
  await store.run({
    kind: 'insert',
    table: TABLES.referenceCache,
    onConflict: 'replace',
    row: {
      catalogue,
      fingerprint,
      rows: Array.isArray(document) ? document.length : 0,
      document: document === undefined ? null : JSON.stringify(document),
      fetched_at: at,
    },
  });
}

// --- the local log ---

async function log(
  context: Context,
  phase: 'push' | 'pull' | 'receipt' | 'reference',
  outcome: string,
  extra: { batchId?: string | null; receipt?: SyncReceipt; detail?: string },
): Promise<void> {
  const receipt = extra.receipt;
  await appendSyncLog(context.deps.store, {
    id: context.newId(),
    at: context.now(),
    phase,
    outcome,
    batchId: extra.batchId ?? receipt?.batch_id ?? null,
    events: receipt?.events ?? 0,
    accepted: receipt?.accepted ?? 0,
    duplicated: receipt?.duplicated ?? 0,
    rejected: receipt?.rejected ?? 0,
    quarantined: receipt?.quarantined ?? 0,
    blocked: receipt?.blocked ?? 0,
    detail: extra.detail ?? null,
  });
}

// --- the scheduler ---

export interface SchedulerDeps extends EngineDeps {
  /** How often to try when there is nothing else prompting it. */
  intervalMs?: number;
  setTimer?: (fn: () => void, ms: number) => unknown;
  clearTimer?: (handle: unknown) => void;
  /** Told about every attempt, for the status pill. */
  onReport?: (report: SyncReport) => void;
}

export interface SyncEngine {
  start(): void;
  stop(): void;
  /** Sync now. Several callers get one attempt: never two at once against one database. */
  sync(reason: SyncReason): Promise<SyncReport>;
  readonly running: boolean;
}

export type SyncReason = 'interval' | 'connectivity' | 'foreground' | 'manual' | 'command';

/** Five minutes: the top of §13.5's ladder, and the interval when nothing else prompts a sync. */
export const DEFAULT_INTERVAL_MS = 5 * 60_000;

/**
 * The loop around `syncOnce`.
 *
 * Three triggers, which are CP66's scope in one line: connectivity regained, app foregrounded, and
 * an interval. The wiring to NetInfo and AppState is in `SyncProvider.tsx`, where it cannot be
 * tested and where there is consequently nothing to decide.
 *
 * Single-flight, and that is the only interesting thing here: two overlapping attempts would both
 * read the same queue, both mark the same rows in flight, and produce two batches carrying the
 * same events under different receipt numbers. Idempotency means the ledger would survive it; the
 * outbox would not.
 */
export function createSyncEngine(deps: SchedulerDeps): SyncEngine {
  const interval = deps.intervalMs ?? DEFAULT_INTERVAL_MS;
  const setTimer = deps.setTimer ?? ((fn, ms) => setTimeout(fn, ms));
  const clearTimer = deps.clearTimer ?? ((handle) => clearTimeout(handle as never));
  let timer: unknown = null;
  let inFlight: Promise<SyncReport> | null = null;
  let started = false;

  async function run(): Promise<SyncReport> {
    const report = await syncOnce(deps);
    deps.onReport?.(report);
    return report;
  }

  const engine: SyncEngine = {
    get running() {
      return started;
    },

    start() {
      if (started) return;
      started = true;
      const tick = () => {
        void engine.sync('interval').finally(() => {
          if (started) timer = setTimer(tick, interval);
        });
      };
      timer = setTimer(tick, interval);
    },

    stop() {
      started = false;
      if (timer !== null) clearTimer(timer);
      timer = null;
    },

    async sync() {
      // The reason is for the caller's own logging; what matters here is that there is one
      // attempt at a time. A returned promise rather than a refusal, so a screen that asked for a
      // sync gets the answer of the attempt that is already running.
      inFlight ??= run().finally(() => {
        inFlight = null;
      });
      return inFlight;
    },
  };

  return engine;
}

/** How much is undelivered right now. The one number §13.9 says must never be wrong. */
export async function undeliveredCount(store: LocalStore): Promise<number> {
  return (await readCounts(store)).total;
}
