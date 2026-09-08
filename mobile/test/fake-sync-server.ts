import { ApiError, NetworkError } from '@dthcms/api-client';

import type {
  PulledEvent,
  SyncEvent,
  SyncPage,
  SyncReceipt,
  SyncResult,
} from '../src/lib/sync/state';
import type { PushBody, SyncTransport } from '../src/lib/sync/transport';

/**
 * The clinic, as the sync engine sees it.
 *
 * A reimplementation of `internal/offline`'s decisions, close enough that a client which satisfies
 * this one satisfies that one, and explicit about every place it is a simplification. It exists
 * because the alternative — asserting on a hand-written receipt per test — tests what the author
 * expected the server to say rather than what the protocol says, which is precisely the class of
 * bug that ends in silent corruption.
 *
 * What it reproduces, one rule at a time:
 *
 *   - `event_id` is the ledger's key. The same event twice is one row and a `DUPLICATE`.
 *   - `batch_id` is a durable receipt. A **closed** batch is answered from its stored results
 *     without touching the ledger, and `replayed` says so.
 *   - **The batch row is opened before the first event and closed after the last**, exactly as
 *     `Service.Push` does — so `crashAfter` leaves a receipt with `closed: false`, and a push
 *     under that id is **reprocessed** rather than answered from it. The results of the first
 *     attempt stand: an event that already has an answer keeps the one it was given.
 *   - Events are grouped by aggregate and applied in the client's order; a failure blocks only
 *     later events on that aggregate that declare `expected_sequence`.
 *   - `occurred_at` is preserved; `recorded_at` is the server's; `source` is assigned.
 *   - Clock skew is measured from `client_clock` and reported on every receipt; an `occurred_at`
 *     more than the tolerance in the future is **held**, not rejected.
 *   - **A device that is not active may push and may do nothing else.** `POST /v1/sync/events` is
 *     the one route the status refusal does not apply to, so a revoked tablet can hand its
 *     backlog to the quarantine — every event held, none accepted — while the pull, the
 *     catalogues and its own state all answer 401.
 *   - **A device may leave only so many events awaiting review.** At the ceiling an event that
 *     would have been held comes back `BLOCKED`/`QUARANTINE_FULL` and is written nowhere, and a
 *     device the clinic has *refused* — every one of whose events would need a hold — is turned
 *     away whole with a 429 and **no** `Retry-After`, opening no batch row and no receipt. The
 *     allowance is cleared by triage, so this reimplementation clears it the same way: by
 *     something removing rows from `held`, never by the clock.
 *   - `device_id` in the body is **checked, not trusted**: the sending device comes from the
 *     signature, and a body naming a different one is a 422 on `device_id`.
 */

export interface LedgerRow {
  event_id: string;
  global_seq: number;
  aggregate_type: string;
  aggregate_id: string;
  patient_id?: string;
  visit_id?: string;
  event_type: string;
  event_version: number;
  occurred_at: string;
  recorded_at: string;
  payload: Record<string, unknown>;
  /**
   * The envelope's metadata, stored as the ledger stores it.
   *
   * Kept because one field in it is load-bearing off the device: a correction carries
   * `corrects_event_id`, which is how the clinic can tell a refused measurement that somebody
   * answered from one that was abandoned. `sync-integrity.ts` asks exactly that question, and it
   * has to be answerable after the correction has been delivered and the tablet's copy of the
   * outbox row is gone.
   */
  metadata?: Record<string, unknown>;
  source: string;
}

export interface HeldRow {
  event_id: string;
  reason_code: string;
  event: SyncEvent;
}

export interface ClinicOptions {
  now?: () => number;
  maxBatch?: number;
  clockToleranceMs?: number;
  /** Event types the clinic knows. Anything else is held, as an older server would hold it. */
  knownTypes?: string[];
  /** How many of this device's events may await review at once (`ops.quarantine_cap()`). */
  quarantineCap?: number;
  /** The device the signature verifies as. A body naming a different one is refused. */
  signedBy?: string;
}

export class FakeClinic {
  readonly ledger = new Map<string, LedgerRow>();
  readonly held: HeldRow[] = [];
  readonly batches = new Map<string, SyncReceipt>();
  /** Every push that arrived, in order, for tests that assert what was actually sent. */
  readonly pushes: PushBody[] = [];
  /**
   * Every event this clinic has refused outright, by id (CP67).
   *
   * The fourth place an event can legitimately be, and the integrity check needs it. Until CP67 a
   * rejected event stayed in the device's outbox for ever, so "in the ledger, in the quarantine or
   * in the outbox" covered every honest case; now a person can correct one, and the refused event
   * is then only in `local_events` — answered, not lost. Recording refusals here is what lets
   * `sync-integrity.ts` tell that apart from a client that quietly dropped queued work.
   */
  readonly refusals = new Map<string, { code: string; reason: string }>();

  deviceStatus: 'active' | 'revoked' | 'suspended' = 'active';
  /** Reject a specific event, as a validation failure would. */
  rejectWhen: ((event: SyncEvent) => { code: string; reason: string } | null) | null = null;
  /** Process this many events of the next batch and then lose the connection. */
  crashAfter: number | null = null;

  private seq = 0;
  private readonly options: Required<Omit<ClinicOptions, 'knownTypes'>> & {
    knownTypes: string[] | null;
  };

  constructor(options: ClinicOptions = {}) {
    this.options = {
      now: options.now ?? Date.now,
      maxBatch: options.maxBatch ?? 500,
      clockToleranceMs: options.clockToleranceMs ?? 5 * 60_000,
      signedBy: options.signedBy ?? 'device-1',
      knownTypes: options.knownTypes ?? null,
      // The real cap is 2,000 and the real number is irrelevant to a client: what a test needs is
      // a clinic that is *at* its ceiling, which a small number reaches in one batch.
      quarantineCap: options.quarantineCap ?? Number.POSITIVE_INFINITY,
    };
  }

  /** Whether there is room to hold one more of this device's events. */
  private roomToHold(): boolean {
    return this.held.length < this.options.quarantineCap;
  }

  /** A physician works through the list. The only thing that makes room; time never does. */
  triage(count = this.held.length): number {
    return this.held.splice(0, count).length;
  }

  /** Everything except the push: refused outright for a device that is no longer active. */
  private guardStatus(): void {
    if (this.deviceStatus !== 'active') {
      throw this.refused('the device is not enrolled, or the request was not signed by it');
    }
  }

  private refused(message: string, status = 401, code = 'UNAUTHENTICATED'): ApiError {
    return new ApiError({
      status,
      code,
      // `technical` for the 429, which is the contract's kind for "the same request, later".
      kind: status === 401 ? 'auth' : status === 429 ? 'technical' : 'validation',
      messageEN: message,
      messageBN: message,
      correlationID: 'req_fake',
      // Deliberately absent for `SYNC_QUARANTINE_FULL`. Waiting does not clear a ceiling that
      // counts held rows, so a number of seconds would be a promise the clinic cannot keep.
    });
  }

  push(body: PushBody): SyncReceipt {
    this.pushes.push(body);
    const now = this.options.now();

    // Every event from a refused device would be held, so at the ceiling nothing in this batch
    // can land and there is no point opening a batch row and five hundred result rows to say so.
    // Checked before the replay branch, and before anything is written: this answer is the one
    // case in the protocol where a client may be certain that nothing at all happened.
    if (this.deviceStatus !== 'active' && !this.roomToHold()) {
      throw this.refused(
        'This tablet already has as many entries awaiting review as it may have.',
        429,
        'SYNC_QUARANTINE_FULL',
      );
    }

    // Already processed **and closed**: answered from the record, without touching the ledger.
    // An open row — a server interrupted halfway — is reprocessed instead, which is what
    // `closed: false` tells a client to expect.
    const stored = this.batches.get(body.batch_id);
    if (stored?.closed) {
      return { ...stored, replayed: true, server_time: new Date(now).toISOString() };
    }
    const reopened = stored !== undefined;

    if (body.events.length === 0) {
      throw this.refused('A batch needs at least one event.', 422, 'VALIDATION_FAILED');
    }
    if (body.events.length > this.options.maxBatch) {
      throw this.refused('That batch is too large.', 422, 'SYNC_BATCH_TOO_LARGE');
    }
    if (body.device_id !== undefined && body.device_id !== this.options.signedBy) {
      // Checked, not trusted. A body naming another device means the client is confused about
      // which device it is, and carrying on would record something neither side meant.
      throw this.mismatch();
    }
    // No status check: this is the one route a revoked device may reach, so that a morning of
    // measurements can be handed over and held rather than kept on a tablet nobody will read.

    const skew = body.client_clock ? Date.parse(body.client_clock) - now : undefined;
    const receipt: SyncReceipt = {
      batch_id: body.batch_id,
      received_at: new Date(now).toISOString(),
      server_time: new Date(now).toISOString(),
      events: 0,
      accepted: 0,
      duplicated: 0,
      rejected: 0,
      quarantined: 0,
      blocked: 0,
      results: [],
      closed: false,
      ...(skew === undefined ? {} : { clock_skew_ms: skew }),
    };
    // An interrupted batch keeps the answers its first attempt produced; `RecordResult` is
    // ON CONFLICT DO NOTHING on the server, and this is the same rule.
    const alreadyAnswered = new Map(
      (reopened ? (stored?.results ?? []) : []).map((result) => [result.event_id, result]),
    );
    // Opened before the first event, as the server does — so a crash half way leaves a receipt
    // that exists and reports nothing.
    this.batches.set(body.batch_id, { ...receipt });

    const groups = new Map<string, SyncEvent[]>();
    for (const event of body.events) {
      const key = `${event.aggregate_type}/${event.aggregate_id}`;
      groups.set(key, [...(groups.get(key) ?? []), event]);
    }

    const results = new Map<string, SyncResult>();
    let processed = 0;
    for (const group of groups.values()) {
      let failedEarlier = false;
      for (const event of group) {
        if (this.crashAfter !== null && processed >= this.crashAfter) {
          this.crashAfter = null;
          // The response never comes back. The batch stays open, which is the state the receipt
          // endpoint will report for ever.
          throw new NetworkError(new Error('the connection went away mid-batch'));
        }
        processed += 1;
        const result = alreadyAnswered.get(event.event_id) ?? this.one(event, failedEarlier, now);
        results.set(event.event_id, result);
        if (result.outcome === 'REJECTED' || result.outcome === 'QUARANTINED') failedEarlier = true;
      }
    }

    // Reported in the order the client sent, so a client can walk its outbox against the answer.
    for (const event of body.events) {
      const result = results.get(event.event_id);
      if (!result) continue;
      receipt.results.push(result);
      receipt.events += 1;
      if (result.outcome === 'ACCEPTED') receipt.accepted += 1;
      if (result.outcome === 'DUPLICATE') receipt.duplicated += 1;
      if (result.outcome === 'REJECTED') receipt.rejected += 1;
      if (result.outcome === 'QUARANTINED') receipt.quarantined += 1;
      if (result.outcome === 'BLOCKED') receipt.blocked += 1;
    }
    receipt.closed = true;
    this.batches.set(body.batch_id, { ...receipt });
    return receipt;
  }

  private mismatch(): ApiError {
    return new ApiError({
      status: 422,
      code: 'VALIDATION_FAILED',
      kind: 'validation',
      messageEN: 'This batch names a device other than the one that sent it.',
      messageBN: 'এই ব্যাচে যে যন্ত্রের কথা বলা হয়েছে, সেটি পাঠানো যন্ত্র নয়।',
      fields: { device_id: 'This batch names a device other than the one that sent it.' },
      correlationID: 'req_fake',
    });
  }

  private one(event: SyncEvent, failedEarlier: boolean, now: number): SyncResult {
    if (failedEarlier && typeof event.expected_sequence === 'number') {
      return {
        event_id: event.event_id,
        outcome: 'BLOCKED',
        reason_code: 'EARLIER_EVENT_FAILED',
        reason: 'an earlier event for the same record did not land',
      };
    }
    if (this.deviceStatus !== 'active') {
      return this.hold(
        event,
        this.deviceStatus === 'revoked' ? 'DEVICE_REVOKED' : 'DEVICE_SUSPENDED',
      );
    }
    if (Date.parse(event.occurred_at) > now + this.options.clockToleranceMs) {
      return this.hold(event, 'CLOCK_IMPLAUSIBLE');
    }
    if (this.options.knownTypes && !this.options.knownTypes.includes(event.event_type)) {
      return this.hold(event, 'UNKNOWN_EVENT_TYPE');
    }
    const refusal = this.rejectWhen?.(event) ?? null;
    if (refusal) {
      this.refusals.set(event.event_id, refusal);
      return {
        event_id: event.event_id,
        outcome: 'REJECTED',
        reason_code: refusal.code,
        reason: refusal.reason,
      };
    }

    const existing = this.ledger.get(event.event_id);
    if (existing) {
      // The same event twice is one row. This is the whole of "duplicate submission produces no
      // duplicate events", and it is why a client may resend a whole batch without thinking.
      return { event_id: event.event_id, outcome: 'DUPLICATE', global_seq: existing.global_seq };
    }
    this.seq += 1;
    this.ledger.set(event.event_id, {
      event_id: event.event_id,
      global_seq: this.seq,
      aggregate_type: event.aggregate_type,
      aggregate_id: event.aggregate_id,
      ...(event.patient_id ? { patient_id: event.patient_id } : {}),
      ...(event.visit_id ? { visit_id: event.visit_id } : {}),
      event_type: event.event_type,
      event_version: event.event_version,
      // Preserved exactly. The server assigns the other one.
      occurred_at: event.occurred_at,
      recorded_at: new Date(now).toISOString(),
      payload: event.payload as Record<string, unknown>,
      ...(event.metadata ? { metadata: event.metadata as Record<string, unknown> } : {}),
      source: 'MOBILE_OFFLINE_SYNC',
    });
    return { event_id: event.event_id, outcome: 'ACCEPTED', global_seq: this.seq };
  }

  private hold(event: SyncEvent, code: string): SyncResult {
    if (!this.roomToHold()) {
      // Blocked, never rejected: a conforming client drops a rejected event, and dropping a real
      // measurement because a supervisor is behind on their reading is the silent loss the
      // quarantine exists to prevent. Written nowhere — the client keeps it and sends it again.
      return {
        event_id: event.event_id,
        outcome: 'BLOCKED',
        reason_code: 'QUARANTINE_FULL',
        reason: 'this device has as many entries awaiting review as it may have',
      };
    }
    this.held.push({ event_id: event.event_id, reason_code: code, event });
    return {
      event_id: event.event_id,
      outcome: 'QUARANTINED',
      reason_code: code,
      reason: 'held for a person to decide about',
    };
  }

  receipt(batchId: string): SyncReceipt | null {
    const stored = this.batches.get(batchId);
    if (!stored) return null;
    return { ...stored, replayed: true, server_time: new Date(this.options.now()).toISOString() };
  }

  /** Something the clinic recorded that this device did not: another station's work. */
  append(
    row: Omit<LedgerRow, 'global_seq' | 'recorded_at' | 'source'> & { source?: string },
  ): LedgerRow {
    this.seq += 1;
    const stored: LedgerRow = {
      ...row,
      global_seq: this.seq,
      recorded_at: new Date(this.options.now()).toISOString(),
      source: row.source ?? 'WEB',
    };
    this.ledger.set(row.event_id, stored);
    return stored;
  }

  pull(since: number, limit: number): SyncPage {
    const all = [...this.ledger.values()]
      .filter((row) => row.global_seq > since)
      .sort((a, b) => a.global_seq - b.global_seq);
    const page = all.slice(0, limit);
    const latest = this.seq;
    // The cursor is on every page, including an empty one.
    const cursor = page.length > 0 ? (page[page.length - 1]?.global_seq ?? since) : since;
    return {
      events: page.map((row) => ({ ...row }) as PulledEvent),
      cursor,
      latest,
      more: cursor < latest,
    };
  }

  transport(): SyncTransport {
    return {
      // The one route a revoked device may still reach.
      push: async (body) => this.push(body),
      receipt: async (batchId) => {
        this.guardStatus();
        return this.receipt(batchId);
      },
      pull: async (since, limit) => {
        this.guardStatus();
        return this.pull(since, limit);
      },
      reference: async () => {
        this.guardStatus();
        return {
          catalogues: [
            { catalogue: 'terminology', rows: 120, fingerprint: 'fp-terminology-1' },
            { catalogue: 'exercises', rows: 12, fingerprint: 'fp-exercises-1' },
          ],
          server_time: new Date(this.options.now()).toISOString(),
        };
      },
      state: async () => {
        this.guardStatus();
        return {
          state: {
            device_id: this.options.signedBy,
            last_pulled_seq: 0,
            pushed_total: this.ledger.size,
            quarantined_total: this.held.length,
          },
          server_time: new Date(this.options.now()).toISOString(),
        };
      },
    };
  }
}

// --- a network that is not reliable ---

/** A small deterministic PRNG, so a lossy run can be replayed exactly. */
export function seeded(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export interface LossyOptions {
  /** The fraction of requests that fail. §13.10 asks for ten per cent. */
  rate: number;
  random: () => number;
  /**
   * How they fail. `after` is the interesting one — the request arrives, the clinic does the
   * work, and the answer dies on the way back. That is the case the receipt exists for, and a
   * client that only survives `before` has not been tested.
   */
  mode?: 'before' | 'after' | 'both';
  onDrop?: (phase: string) => void;
}

export function lossy(transport: SyncTransport, options: LossyOptions): SyncTransport {
  const mode = options.mode ?? 'both';
  const fails = () => options.random() < options.rate;
  const dropsAfter = () =>
    mode === 'after' ? true : mode === 'before' ? false : options.random() < 0.5;

  async function guard<T>(phase: string, call: () => Promise<T>): Promise<T> {
    if (fails()) {
      if (dropsAfter()) {
        // Do the work, then lose the answer.
        await call();
        options.onDrop?.(`${phase}:after`);
        throw new NetworkError(new Error('the response was lost'));
      }
      options.onDrop?.(`${phase}:before`);
      throw new NetworkError(new Error('the request never arrived'));
    }
    return call();
  }

  return {
    push: (body) => guard('push', () => transport.push(body)),
    receipt: (batchId) => guard('receipt', () => transport.receipt(batchId)),
    pull: (since, limit) => guard('pull', () => transport.pull(since, limit)),
    reference: () => guard('reference', () => transport.reference()),
    state: () => guard('state', () => transport.state()),
  };
}
