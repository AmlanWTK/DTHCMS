import { ApiError, NetworkError } from '@dthcms/api-client';
import { beforeEach, describe, expect, it, vi } from 'vitest';

/**
 * The sync engine, against a clinic (CP66).
 *
 * This file is §13.10's matrix — airplane mode mid-entry, an app kill with a full queue, a
 * 200-event sync, one rejection in fifty, a device clock three hours wrong, a token that expires
 * while offline, a device revoked while offline, a duplicate batch, a lossy network, and a full
 * disk — plus the rules each of those scenarios exists to protect: one `event_id` per measurement
 * for ever, one `batch_id` per batch until it is resolved, five outcomes that are not
 * interchangeable, per-aggregate order, backoff with jitter, and conflicts surfaced rather than
 * resolved.
 *
 * Every scenario finishes with the integrity check (`sync-integrity.ts`), which compares the
 * device and the clinic event for event. A test that only asserts what it expected to happen is a
 * test of the author's expectations; the integrity check is what would notice the thing nobody
 * expected.
 */

const keystore = new Map<string, string>();
vi.mock('expo-secure-store', () => ({
  setItemAsync: vi.fn(async (key: string, value: string) => {
    keystore.set(key, value);
  }),
  getItemAsync: vi.fn(async (key: string) => keystore.get(key) ?? null),
  deleteItemAsync: vi.fn(async (key: string) => {
    keystore.delete(key);
  }),
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'WHEN_UNLOCKED_THIS_DEVICE_ONLY',
}));

// Imported from the modules rather than from the feature index, which is the arrangement every
// station's tests use here: the index re-exports the screen, and these tests run in plain Node.
const { MIGRATIONS, META_KEYS, TABLES, createDisk, createMemoryStore } =
  await import('../src/lib/local-store');
const {
  CHECK_INTERVAL_MS,
  cacheCatalogue,
  checkWithClinic,
  createSyncEngine,
  halt,
  noteSessionLost,
  resume,
  resumeAfterSignIn,
  syncOnce,
  undeliveredCount,
} = await import('../src/lib/sync/engine');
const { issueCommand } = await import('../src/lib/sync/commands');
const { readMeta, readMetrics, readNumber, readOutbox, readProjection, readSyncLog } =
  await import('../src/lib/sync/outbox');
const {
  BACKOFF_LADDER_MS,
  BATCH_LIMIT,
  HALT_REASONS,
  MAX_AUTH_FAILURES,
  MAX_RETRY_AFTER_MS,
  REASON_CODES,
  SERVER_MAX_BATCH,
  actionFor,
  backoffDelay,
  classifyFailure,
  controlFor,
  planFromReceipt,
  retryAfterDelay,
  retryAfterOf,
  retryable,
  selectBatch,
  skewReading,
  statusOf,
  triageDelay,
} = await import('../src/lib/sync/state');

import { FakeClinic, lossy, seeded } from './fake-sync-server';
import { integrityProblems } from './sync-integrity';

import type { ClinicOptions } from './fake-sync-server';
import type { PushBody } from '../src/lib/sync/transport';
import type { OutboxRow } from '../src/lib/sync/state';

const KEY = 'ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554433221100';
const DEVICE = 'device-1';
const PATIENT = 'patient-1';
const OTHER_PATIENT = 'patient-2';
const START = Date.parse('2026-09-05T08:00:00.000Z');

let counter = 0;
const nextId = () => `id-${(counter += 1).toString().padStart(4, '0')}`;

interface Harness {
  store: ReturnType<typeof createMemoryStore>;
  disk: ReturnType<typeof createDisk>;
  clinic: FakeClinic;
  deps: Parameters<typeof syncOnce>[0];
  now(): number;
  advance(ms: number): void;
  record(over?: Record<string, unknown>): Promise<{ eventId: string }>;
  reopen(): Promise<void>;
}

async function harness(
  options: { clinic?: Omit<ClinicOptions, 'now'>; batchLimit?: number } = {},
): Promise<Harness> {
  const disk = createDisk();
  const store = createMemoryStore({ disk });
  await store.open({ key: KEY });
  await store.migrate(MIGRATIONS);
  let clock = START;
  // The clinic is built here rather than passed in, so that it and the device read the same
  // clock: a test clinic on the wall clock would report hours of skew to a device on `START`.
  const clinic = new FakeClinic({ ...options.clinic, now: () => clock });
  const state: Harness = {
    store,
    disk,
    clinic,
    now: () => clock,
    advance: (ms) => {
      clock += ms;
    },
    deps: {
      get store() {
        return state.store;
      },
      transport: clinic.transport(),
      deviceId: DEVICE,
      now: () => clock,
      // Half of each backoff step is fixed and half is random; a fixed 0.5 keeps the tests
      // deterministic while the jitter itself is asserted separately.
      random: () => 0.5,
      newId: nextId,
      ...(options.batchLimit === undefined ? {} : { batchLimit: options.batchLimit }),
    },
    record: async (over = {}) => {
      const issued = await issueCommand(
        state.store,
        {
          aggregateType: 'patient',
          aggregateId: PATIENT,
          patientId: PATIENT,
          visitId: 'visit-1',
          eventType: 'WEIGHT_RECORDED',
          eventVersion: 1,
          occurredAt: new Date(clock).toISOString(),
          payload: { code: 'BODY_WEIGHT', value: 70 },
          ...over,
        },
        { now: () => clock, newId: nextId },
      );
      // A second between entries, because there is one in a clinic: an operator cannot record two
      // measurements at the same millisecond, and two events with identical content at an
      // identical instant is what a duplicate looks like to the integrity check.
      clock += 1_000;
      return { eventId: issued.eventId };
    },
    reopen: async () => {
      await state.store.close();
      const next = createMemoryStore({ disk });
      await next.open({ key: KEY });
      await next.migrate(MIGRATIONS);
      state.store = next;
    },
  };
  return state;
}

function apiError(
  status: number,
  code = 'UNAUTHENTICATED',
  retryAfterSeconds: number | null = null,
): ApiError {
  return new ApiError({
    status,
    code,
    kind: 'auth',
    messageEN: 'no',
    messageBN: 'না',
    correlationID: 'req_test',
    retryAfterSeconds,
  });
}

beforeEach(() => {
  counter = 0;
  keystore.clear();
});

// --- the ordinary case ---

describe('a morning that goes well', () => {
  it('sends what was recorded, empties the queue and confirms the values on screen', async () => {
    const h = await harness();
    await h.record();
    await h.record({ payload: { code: 'BODY_HEIGHT', value: 160 } });

    const report = await syncOnce(h.deps);

    expect(report.accepted).toBe(2);
    expect(report.failure).toBeNull();
    expect(await readOutbox(h.store)).toEqual([]);
    expect(h.clinic.ledger.size).toBe(2);

    const projection = await readProjection(h.store, 'observation', `${PATIENT}/BODY_WEIGHT`);
    expect(projection?.confirmed).toBe(true);
    const metrics = await readMetrics(h.store);
    expect(metrics.undelivered).toBe(0);
    expect(statusOf(metrics)).toBe('synced');
    expect(metrics.lastSuccessAt).toBe(h.now());
    expect(await integrityProblems(h.store, h.clinic, { quiescent: true })).toEqual([]);
  });

  it('preserves the moment each measurement was taken and lets the clinic date its arrival', async () => {
    const h = await harness();
    await h.record({ occurredAt: '2026-09-05T08:40:00.000Z' });
    h.advance(2 * 60 * 60 * 1000);
    await syncOnce(h.deps);

    const stored = [...h.clinic.ledger.values()][0];
    expect(stored?.occurred_at).toBe('2026-09-05T08:40:00.000Z');
    expect(stored?.recorded_at).toBe(new Date(h.now()).toISOString());
    // Assigned by the clinic whatever the client says, which is how a reader tells a value entered
    // at the bedside from one that arrived hours later through a queue.
    expect(stored?.source).toBe('MOBILE_OFFLINE_SYNC');
  });

  it('records what it did in a local log that carries no clinical value', async () => {
    const h = await harness();
    await h.record();
    await syncOnce(h.deps);
    const log = await readSyncLog(h.store);
    expect(log.length).toBeGreaterThan(0);
    const text = JSON.stringify(log);
    expect(text).not.toContain('BODY_WEIGHT');
    expect(text).not.toContain(PATIENT);
    expect(text).toContain('SENT');
  });
});

// --- §13.10: airplane mode mid-entry ---

describe('airplane mode in the middle of an entry', () => {
  it('records everything and sends it when the signal comes back', async () => {
    const h = await harness();
    const offline = {
      ...h.clinic.transport(),
      push: async () => {
        throw new NetworkError(new Error('no signal'));
      },
      pull: async () => {
        throw new NetworkError(new Error('no signal'));
      },
      reference: async () => {
        throw new NetworkError(new Error('no signal'));
      },
    };

    const offlineDeps = { ...h.deps, transport: offline };
    for (let index = 0; index < 5; index += 1) {
      await h.record({ payload: { code: `CODE_${index}`, value: index } });
      const report = await syncOnce(offlineDeps);
      expect(report.failure).toBe('offline');
      h.advance(60_000);
    }

    // Nothing is lost and nothing claims to be synced.
    expect(await undeliveredCount(h.store)).toBe(5);
    const metrics = await readMetrics(h.store);
    expect(statusOf(metrics)).not.toBe('synced');
    expect(metrics.lastSuccessAt).toBeNull();

    h.advance(10 * 60_000);
    const back = await syncOnce(h.deps);
    expect(back.accepted).toBe(5);
    expect(await undeliveredCount(h.store)).toBe(0);
    expect(await integrityProblems(h.store, h.clinic, { quiescent: true })).toEqual([]);
  });
});

// --- §13.10: the app is killed with a full queue ---

describe('the app is killed with a full queue', () => {
  it('comes back, asks what happened to the batch it was sending, and loses nothing', async () => {
    const h = await harness({ batchLimit: 10 });
    for (let index = 0; index < 10; index += 1) {
      await h.record({ payload: { code: `CODE_${index}`, value: index } });
    }

    // The clinic processes the batch and the answer never arrives.
    const dropped = {
      ...h.clinic.transport(),
      push: async (body: Parameters<typeof h.clinic.push>[0]) => {
        h.clinic.push(body);
        throw new NetworkError(new Error('the response was lost'));
      },
    };
    await syncOnce({ ...h.deps, transport: dropped });
    expect(h.clinic.ledger.size).toBe(10);
    // The rows are still here, still in flight, still carrying their receipt number.
    const inFlight = await readOutbox(h.store, ['IN_FLIGHT']);
    expect(inFlight).toHaveLength(10);
    const batchId = inFlight[0]?.batchId;
    expect(batchId).toBeTruthy();

    // The tablet is force-killed and relaunched.
    await h.reopen();
    h.advance(30_000);

    const report = await syncOnce(h.deps);
    // Answered from the receipt rather than re-sent: the clinic is not asked to process anything
    // a second time, and nothing is duplicated whether it is or not.
    expect(report.accepted + report.duplicated).toBe(10);
    expect(h.clinic.ledger.size).toBe(10);
    expect(await undeliveredCount(h.store)).toBe(0);
    expect(await integrityProblems(h.store, h.clinic, { quiescent: true })).toEqual([]);
  });

  it('sends the same event id on every attempt, however many there are', async () => {
    // Rule one, asserted directly rather than through its consequences: `event_id` is generated
    // when the command is issued and never again. Every indirect check of this — the ledger size,
    // the integrity report — only fires once a retry has actually happened, and a client that
    // regenerated the id on, say, the *third* attempt would pass all of them.
    const h = await harness({ batchLimit: 3 });
    for (let index = 0; index < 3; index += 1) await h.record();
    const issued = (await readOutbox(h.store)).map((row) => row.eventId);

    const sent: string[][] = [];
    const refusing = {
      ...h.clinic.transport(),
      push: async (body: Parameters<typeof h.clinic.push>[0]) => {
        sent.push(body.events.map((event) => event.event_id));
        throw new NetworkError(new Error('no'));
      },
      receipt: async () => null,
    };
    for (let attempt = 0; attempt < 4; attempt += 1) {
      await syncOnce({ ...h.deps, transport: refusing });
      h.advance(20 * 60_000);
    }

    expect(sent).toHaveLength(4);
    for (const attempt of sent) expect(attempt).toEqual(issued);
  });

  it('waits longer after each failed attempt, including while a batch is in flight', async () => {
    // A batch that stays in flight is retried from the receipt branch rather than from the push
    // loop, so the attempt has to be counted there too. Without it the ladder stays on its first
    // rung for ever — a tablet with no signal asking again every two seconds, which is the
    // battery and the thundering herd both.
    const h = await harness({ batchLimit: 2 });
    for (let index = 0; index < 2; index += 1) await h.record();
    const dead = {
      ...h.clinic.transport(),
      push: async () => {
        throw new NetworkError(new Error('no signal'));
      },
      receipt: async () => {
        throw new NetworkError(new Error('no signal'));
      },
    };

    const waits: number[] = [];
    for (let attempt = 0; attempt < 4; attempt += 1) {
      await syncOnce({ ...h.deps, transport: dead });
      const row = (await readOutbox(h.store))[0];
      waits.push((row?.nextAttemptAt ?? 0) - h.now());
      h.advance(60 * 60_000);
    }

    // Half of each ladder step is fixed and half is jittered; with the jitter pinned at a half,
    // 2s, 5s, 15s and 60s become these.
    expect(waits).toEqual([1_500, 3_750, 11_250, 45_000]);
  });

  it('keeps the same receipt number until the batch is resolved', async () => {
    const h = await harness({ batchLimit: 5 });
    for (let index = 0; index < 5; index += 1) await h.record();

    const seen: string[] = [];
    const flaky = {
      ...h.clinic.transport(),
      push: async (body: Parameters<typeof h.clinic.push>[0]) => {
        seen.push(body.batch_id);
        throw new NetworkError(new Error('nothing arrived'));
      },
      receipt: async () => null,
    };

    for (let attempt = 0; attempt < 3; attempt += 1) {
      await syncOnce({ ...h.deps, transport: flaky });
      h.advance(10 * 60_000);
    }

    // One receipt number across every attempt. A client that made a new one each time would be
    // asking a question the clinic can no longer answer.
    expect(new Set(seen).size).toBe(1);
    expect(seen).toHaveLength(3);
  });
});

// --- §13.10: two hundred events at once ---

describe('two hundred queued events', () => {
  it('goes in batches that respect the clinic limit, in the order they were recorded', async () => {
    const h = await harness();
    for (let index = 0; index < 200; index += 1) {
      await h.record({
        aggregateId: index % 2 === 0 ? PATIENT : OTHER_PATIENT,
        patientId: index % 2 === 0 ? PATIENT : OTHER_PATIENT,
        payload: { code: 'BODY_WEIGHT', value: index },
      });
      h.advance(1_000);
    }

    const report = await syncOnce(h.deps);
    expect(report.accepted).toBe(200);
    expect(report.batches).toBe(200 / BATCH_LIMIT);
    expect(h.clinic.pushes.every((push) => push.events.length <= SERVER_MAX_BATCH)).toBe(true);

    // Per-aggregate order, which is the guarantee: for each patient, the values arrive in the
    // order the operator recorded them.
    const forPatient = [...h.clinic.ledger.values()]
      .filter((row) => row.patient_id === PATIENT)
      .sort((a, b) => a.global_seq - b.global_seq)
      .map((row) => row.payload.value);
    expect(forPatient).toEqual([...Array(100).keys()].map((n) => n * 2));

    expect(await integrityProblems(h.store, h.clinic, { quiescent: true })).toEqual([]);
  });

  it('keeps the client batch well below the clinic refusal', () => {
    // Exceeding it is a refusal rather than a truncation, so this is not a soft limit.
    expect(BATCH_LIMIT).toBeLessThan(SERVER_MAX_BATCH);
    expect(BATCH_LIMIT * 2).toBeLessThanOrEqual(SERVER_MAX_BATCH);
  });
});

// --- §13.10: one rejection in fifty ---

describe('one bad event in a batch of fifty', () => {
  it('delivers the other forty-nine and puts the one in front of a person', async () => {
    const h = await harness();
    for (let index = 0; index < 50; index += 1) {
      await h.record({ payload: { code: 'BODY_WEIGHT', value: index } });
    }
    const queue = await readOutbox(h.store);
    const doomed = queue[12]?.eventId ?? '';
    h.clinic.rejectWhen = (event) =>
      event.event_id === doomed
        ? { code: 'INVALID_PAYLOAD', reason: 'value: a weight of 1200 kg is not plausible' }
        : null;

    const report = await syncOnce(h.deps);
    expect(report.accepted).toBe(49);
    expect(report.attention).toBe(1);

    const left = await readOutbox(h.store);
    expect(left).toHaveLength(1);
    expect(left[0]?.eventId).toBe(doomed);
    expect(left[0]?.state).toBe('NEEDS_ATTENTION');
    expect(left[0]?.reasonCode).toBe('INVALID_PAYLOAD');
    // The words the server sent are kept beside the code, for support rather than for the screen.
    expect(left[0]?.reason).toContain('1200');

    const metrics = await readMetrics(h.store);
    expect(metrics.needsAttention).toBe(1);
    expect(statusOf(metrics)).toBe('attention');

    // And it is not sent again on the next attempt: it is a person's problem now.
    h.advance(10 * 60_000);
    const pushesBefore = h.clinic.pushes.length;
    const second = await syncOnce(h.deps);
    expect(second.attention).toBe(0);
    expect(h.clinic.pushes).toHaveLength(pushesBefore);
    expect(await readOutbox(h.store)).toHaveLength(1);
    expect(await integrityProblems(h.store, h.clinic)).toEqual([]);
  });

  it('never rewrites or discards what the clinic refused', async () => {
    // §13.6: conflicts are surfaced, never auto-resolved. The row keeps its payload exactly.
    const h = await harness();
    await h.record({ payload: { code: 'BODY_WEIGHT', value: 1200 } });
    h.clinic.rejectWhen = () => ({ code: 'SEQUENCE_CONFLICT', reason: 'this record has moved on' });

    await syncOnce(h.deps);
    const row = (await readOutbox(h.store))[0];
    expect(JSON.parse(row?.payload ?? '{}')).toEqual({ code: 'BODY_WEIGHT', value: 1200 });
    expect(row?.state).toBe('NEEDS_ATTENTION');
    expect(h.clinic.ledger.size).toBe(0);
  });
});

// --- BLOCKED, which is the one outcome a client must keep ---

describe('an event that declared it depends on another', () => {
  it('is kept, held back while the first is unresolved, and sent once it is not', async () => {
    const h = await harness();
    const first = await h.record({ payload: { code: 'BODY_WEIGHT', value: 1 } });
    const second = await h.record({
      payload: { code: 'BODY_WEIGHT', value: 2 },
      expectedSequence: 41,
    });
    h.clinic.rejectWhen = (event) =>
      event.event_id === first.eventId ? { code: 'INVALID_PAYLOAD', reason: 'no' } : null;

    const report = await syncOnce(h.deps);
    expect(report.attention).toBe(1);
    expect(report.blocked).toBe(1);

    const queue = await readOutbox(h.store);
    expect(queue.map((row) => row.state)).toEqual(['NEEDS_ATTENTION', 'BLOCKED_LOCAL']);

    // It is not resent while the first is unresolved: sending it would produce another BLOCKED,
    // which is a loop that looks like progress.
    h.advance(10 * 60_000);
    const before = h.clinic.pushes.length;
    await syncOnce(h.deps);
    expect(h.clinic.pushes).toHaveLength(before);

    // A person deals with the first one — here, by removing it. Now the second may go.
    await h.store.run({
      kind: 'delete',
      table: TABLES.outbox,
      where: [{ column: 'event_id', op: '=', value: first.eventId }],
    });
    h.clinic.rejectWhen = null;
    h.advance(10 * 60_000);
    const after = await syncOnce(h.deps);
    expect(after.accepted).toBe(1);
    expect(h.clinic.ledger.has(second.eventId)).toBe(true);
  });
});

// --- BLOCKED for the other reason: the clinic has nowhere left to put it ---

describe('an entry the clinic has no room to hold', () => {
  // A device that is *not* refused, during a rolling deploy: it is sending an event type this
  // clinic has not learned yet, which needs a hold, and the clinic's hold on this device is
  // already as full as it may be. `docs/sync.md` is explicit that its valid measurements keep
  // landing and only that one comes back blocked.
  const rollingDeploy: Omit<ClinicOptions, 'now'> = {
    knownTypes: ['WEIGHT_RECORDED'],
    quarantineCap: 0,
  };

  it('keeps it out of every batch until the clinic has been triaged, rather than offering it every tick', async () => {
    const h = await harness({ clinic: { ...rollingDeploy } });
    await h.record();
    const ceilinged = await h.record({ eventType: 'BLOOD_PRESSURE_RECORDED' });

    const report = await syncOnce(h.deps);
    expect(report.accepted).toBe(1);
    expect(report.ceilinged).toBe(1);
    // Not the other kind of BLOCKED. Counting it as one would put it back in the next batch.
    expect(report.blocked).toBe(0);

    const queue = await readOutbox(h.store);
    expect(queue).toHaveLength(1);
    expect(queue[0]?.eventId).toBe(ceilinged.eventId);
    expect(queue[0]?.state).toBe('AWAITING_TRIAGE');
    expect(queue[0]?.reasonCode).toBe('QUARANTINE_FULL');

    // The loop this exists to prevent. Ten more attempts over the following ten minutes ask the
    // clinic nothing at all: waiting does not clear a ceiling that counts somebody's reading
    // list, so asking again before they have read it is battery, budget and a screen that looks
    // busy while nothing moves.
    const sent = h.clinic.pushes.length;
    for (let tick = 0; tick < 10; tick += 1) {
      h.advance(60_000);
      expect((await syncOnce(h.deps)).ceilinged).toBe(0);
    }
    expect(h.clinic.pushes).toHaveLength(sent);
    expect(await integrityProblems(h.store, h.clinic)).toEqual([]);
  });

  it('sends it again by itself once a physician has worked through the list, with nobody touching the tablet', async () => {
    const h = await harness({ clinic: { knownTypes: [], quarantineCap: 1 } });
    const first = await h.record();
    const second = await h.record();

    const report = await syncOnce(h.deps);
    expect(report.held).toBe(1);
    expect(report.ceilinged).toBe(1);
    expect(h.clinic.held.map((row) => row.event_id)).toEqual([first.eventId]);

    // Somebody at the clinic reads the quarantine and deals with what is in it. Nothing happens
    // on the tablet: it is in a drawer, and the person who can fix this is in another room.
    h.clinic.triage();
    h.advance(31 * 60_000);

    const after = await syncOnce(h.deps);
    expect(after.held).toBe(1);
    expect(h.clinic.held.map((row) => row.event_id)).toEqual([second.eventId]);
    expect(await readOutbox(h.store, ['AWAITING_TRIAGE'])).toEqual([]);
    expect(await integrityProblems(h.store, h.clinic)).toEqual([]);
  });

  it('never rewrites it, never drops it and never counts it as delivered', async () => {
    const h = await harness({ clinic: { ...rollingDeploy } });
    const stuck = await h.record({
      eventType: 'BLOOD_PRESSURE_RECORDED',
      payload: { systolic: 168, diastolic: 96 },
    });

    await syncOnce(h.deps);
    h.advance(60_000);
    await syncOnce(h.deps);

    const row = (await readOutbox(h.store))[0];
    expect(row?.eventId).toBe(stuck.eventId);
    expect(JSON.parse(row?.payload ?? '{}')).toEqual({ systolic: 168, diastolic: 96 });
    expect(row?.occurredAt).toBe(new Date(START).toISOString());
    // Nowhere at the clinic: not in the ledger, and not in the quarantine either. That is what
    // "there was nowhere to hold it" means, and it is why the device may not let go of it.
    expect(h.clinic.ledger.has(stuck.eventId)).toBe(false);
    expect(h.clinic.held).toEqual([]);
    expect(await undeliveredCount(h.store)).toBe(1);
  });

  it('goes on taking entries at the bedside, and does not hold the rest of that patient behind them', async () => {
    // An operator at the bedside must be able to record. And the entry they record next must
    // still go: the clinic accepts an independent later event while an earlier one is blocked,
    // so a client that queued the whole patient behind the stuck one would be stricter than the
    // clinic and would strand measurements nobody is holding.
    const h = await harness({ clinic: { ...rollingDeploy } });
    await h.record({ eventType: 'BLOOD_PRESSURE_RECORDED' });
    await syncOnce(h.deps);

    const later = await h.record();
    h.advance(60_000);
    const report = await syncOnce(h.deps);

    expect(report.accepted).toBe(1);
    expect(h.clinic.ledger.has(later.eventId)).toBe(true);
    const metrics = await readMetrics(h.store);
    expect(metrics.awaitingTriage).toBe(1);
    expect(metrics.undelivered).toBe(1);
  });

  it('says so on the screen, in a sentence about the clinic rather than about the queue', async () => {
    const h = await harness({ clinic: { ...rollingDeploy } });
    await h.record({ eventType: 'BLOOD_PRESSURE_RECORDED' });
    await syncOnce(h.deps);

    const metrics = await readMetrics(h.store);
    // Never `queued`, which reads as "give it a minute", and never `synced`.
    expect(statusOf(metrics)).toBe('stalled');
    expect(metrics.undelivered).toBe(1);
  });
});

describe('a refused tablet that is already at the clinic ceiling', () => {
  it('keeps the batch and does not ask for a receipt for one the clinic never opened', async () => {
    // The only refusal in the protocol a client may read as "nothing was written". Leaving the
    // rows in flight would send the next attempt looking for a receipt that does not exist, and
    // would leave them claiming to belong to a batch nobody has heard of.
    const h = await harness({ clinic: { quarantineCap: 0 } });
    for (let index = 0; index < 3; index += 1) await h.record();
    h.clinic.deviceStatus = 'revoked';

    let receipts = 0;
    const watching = {
      ...h.clinic.transport(),
      receipt: async (batchId: string) => {
        receipts += 1;
        return h.clinic.transport().receipt(batchId);
      },
    };

    const report = await syncOnce({ ...h.deps, transport: watching });
    expect(report.failure).toBe('quarantine-full');
    expect(report.ceilinged).toBe(3);
    expect(h.clinic.batches.size).toBe(0);

    const queue = await readOutbox(h.store);
    expect(queue).toHaveLength(3);
    expect(queue.every((row) => row.state === 'AWAITING_TRIAGE')).toBe(true);
    expect(queue.every((row) => row.batchId === null)).toBe(true);

    // The next attempt asks for nothing, because there is nothing to ask about and nothing due.
    const sent = h.clinic.pushes.length;
    h.advance(5 * 60_000);
    await syncOnce({ ...h.deps, transport: watching });
    expect(receipts).toBe(0);
    expect(h.clinic.pushes).toHaveLength(sent);
  });

  it('does not stop itself while it still has work nobody has made room for', async () => {
    // A refused tablet halts once its backlog is handed over, and that halt takes an operator
    // pressing something on the device to clear. Reaching it here would strand three real
    // measurements on the word of a queue that is full today and will not be tomorrow.
    const h = await harness({ clinic: { quarantineCap: 0 } });
    for (let index = 0; index < 3; index += 1) await h.record();
    h.clinic.deviceStatus = 'revoked';

    const report = await syncOnce(h.deps);
    expect(report.halted).toBe('');
    expect(report.delivering).toBe(true);
    expect((await readMetrics(h.store)).halted).toBe('');

    // Five more attempts with nothing new to send. A refused tablet is refused everywhere except
    // the push, so a tick that reached the pull would collect a 401, and three of those in a row
    // are how this engine recognises a device the clinic will not accept — a halt that takes
    // somebody standing over the tablet to clear. Here there is still a backlog to hand over, so
    // the tick must end before the pull and the tablet must stay ready.
    // Stepped so that five attempts stay inside the shortest wait the parked rows can have; what
    // happens when that wait is up is the next test's business.
    const step = Math.floor(triageDelay(0) / 6);
    for (let tick = 0; tick < 5; tick += 1) {
      h.advance(step);
      const again = await syncOnce(h.deps);
      expect(again.halted).toBe('');
      expect(again.failure).toBeNull();
    }
    expect(await readNumber(h.store, META_KEYS.authFailures, 0)).toBe(0);
    expect(await undeliveredCount(h.store)).toBe(3);
  });

  it('hands the morning over on its own once the clinic has made room', async () => {
    const h = await harness({ clinic: { quarantineCap: 2 } });
    for (let index = 0; index < 3; index += 1) await h.record();
    h.clinic.deviceStatus = 'revoked';

    // Two of the three are held; the third has nowhere to go and comes back blocked.
    const first = await syncOnce(h.deps);
    expect(first.held).toBe(2);
    expect(first.ceilinged).toBe(1);
    expect(first.halted).toBe('');

    // A second attempt now is turned away whole, because every event a refused device sends
    // would need a hold and there is room for none of them.
    h.advance(60_000);
    await h.record();
    const second = await syncOnce(h.deps);
    expect(second.failure).toBe('quarantine-full');
    expect(h.clinic.held).toHaveLength(2);

    h.clinic.triage();
    h.advance(31 * 60_000);
    const third = await syncOnce(h.deps);

    // The handover finishes by itself. All four measurements are at the clinic for a physician to
    // decide about, none is in the ledger — the revocation stands — and only now, with nothing
    // left to hand over, does the tablet stop.
    expect(third.held).toBe(2);
    expect(third.halted).toBe(HALT_REASONS.deviceRefused);
    expect(h.clinic.held).toHaveLength(2);
    expect(h.clinic.ledger.size).toBe(0);
    const queue = await readOutbox(h.store);
    expect(queue).toHaveLength(4);
    expect(queue.every((row) => row.state === 'HELD')).toBe(true);
    expect(await integrityProblems(h.store, h.clinic)).toEqual([]);
  });
});

describe('asking the clinic again, because somebody at the clinic said it was clear', () => {
  /** A tablet with three entries the clinic had no room for, and one it will not send. */
  async function stalled() {
    const h = await harness({ clinic: { quarantineCap: 0 } });
    for (let index = 0; index < 3; index += 1) await h.record();
    h.clinic.deviceStatus = 'revoked';
    await syncOnce(h.deps);
    return h;
  }

  it('offers the stalled entries again at once, instead of waiting out the half hour', async () => {
    const h = await stalled();
    h.clinic.triage();
    h.clinic.deviceStatus = 'active';
    h.advance(60_000);

    expect(await checkWithClinic(h.store, { now: h.now })).toBe('asking');
    const report = await syncOnce(h.deps);

    // The operator telephoned, a physician cleared the list, and the work went — minutes after
    // the press rather than at whatever point the tablet would next have asked by itself.
    expect(report.accepted).toBe(3);
    expect(await undeliveredCount(h.store)).toBe(0);
    expect(await integrityProblems(h.store, h.clinic)).toEqual([]);
  });

  it('turns twenty presses into one question', async () => {
    const h = await stalled();
    const sent = h.clinic.pushes.length;
    // Past the floor, so that the first press is one the clinic is actually asked.
    h.advance(CHECK_INTERVAL_MS + 1_000);

    for (let press = 0; press < 20; press += 1) {
      await checkWithClinic(h.store, { now: h.now });
      await syncOnce(h.deps);
      h.advance(1_000);
    }
    // One question, not twenty. The floor is measured from the clinic's own last answer, so the
    // press that reaches it is the press that starts the clock — and the nineteen after it are
    // asking something that has already been asked and answered inside the same half minute.
    expect(h.clinic.pushes.length - sent).toBe(1);

    h.advance(CHECK_INTERVAL_MS);
    expect(await checkWithClinic(h.store, { now: h.now })).toBe('asking');
    await syncOnce(h.deps);
    expect(h.clinic.pushes.length - sent).toBe(2);
  });

  it('does not shorten a wait the clinic itself asked for', async () => {
    // The one thing this button must never do. A batch left in flight behind a `Retry-After` is
    // waiting because the server said so, and a client that spent the budget it has just been
    // told it does not have would be answered with another refusal and deserve it.
    const h = await stalled();
    await h.record();
    h.advance(31 * 60_000);

    const limited = {
      ...h.clinic.transport(),
      push: async () => {
        throw apiError(429, 'RATE_LIMITED', 600);
      },
    };
    await syncOnce({ ...h.deps, transport: limited });
    const inFlight = await readOutbox(h.store, ['IN_FLIGHT']);
    expect(inFlight.length).toBeGreaterThan(0);
    const due = inFlight.map((row) => row.nextAttemptAt);

    h.advance(60_000);
    await checkWithClinic(h.store, { now: h.now });

    expect((await readOutbox(h.store, ['IN_FLIGHT'])).map((row) => row.nextAttemptAt)).toEqual(due);
    // And nothing is sent: the attempt is held back by the wait the clinic named.
    const sent = h.clinic.pushes.length;
    expect((await syncOnce({ ...h.deps, transport: limited })).waiting).toBe(true);
    expect(h.clinic.pushes).toHaveLength(sent);
  });

  it('is the only button the screen offers while it applies', async () => {
    // The vaguer control must not be on the screen beside it. An operator pressing "try again
    // now" over a stalled queue would watch it leave the stalled entries exactly where they are,
    // which is how a person learns that the buttons on this screen are decorative.
    const h = await stalled();
    expect(controlFor(await readMetrics(h.store))).toBe('check-with-clinic');

    h.clinic.triage();
    h.clinic.deviceStatus = 'active';
    h.advance(CHECK_INTERVAL_MS + 1_000);
    await checkWithClinic(h.store, { now: h.now });
    await syncOnce(h.deps);
    // Nothing is waiting on the clinic any more, so the ordinary one comes back.
    expect(controlFor(await readMetrics(h.store))).toBe('retry');
  });

  it('leaves every other row exactly where it was', async () => {
    // An entry waiting on an earlier one for the same patient is a different problem with a
    // different answer, and this button is not it.
    const h = await harness({ clinic: { quarantineCap: 0 } });
    const first = await h.record({ payload: { code: 'BODY_WEIGHT', value: 1 } });
    await h.record({ payload: { code: 'BODY_WEIGHT', value: 2 }, expectedSequence: 41 });
    h.clinic.rejectWhen = (event) =>
      event.event_id === first.eventId ? { code: 'INVALID_PAYLOAD', reason: 'no' } : null;
    await syncOnce(h.deps);

    const before = await readOutbox(h.store);
    expect(before.map((row) => row.state)).toEqual(['NEEDS_ATTENTION', 'BLOCKED_LOCAL']);
    expect(await checkWithClinic(h.store, { now: h.now })).toBe('nothing-to-check');
    expect(await readOutbox(h.store)).toEqual(before);
  });

  it('only ever shows a time the clinic actually answered at', async () => {
    // The line says "last checked". It may only say that because the number behind it cannot
    // move unless a request reached the clinic and came back — so an operator who presses the
    // button in a corridor is told nothing new, which is exactly what happened to their work.
    const h = await harness({ clinic: { knownTypes: ['WEIGHT_RECORDED'], quarantineCap: 0 } });
    await h.record({ eventType: 'BLOOD_PRESSURE_RECORDED' });
    await syncOnce(h.deps);
    const answered = (await readMetrics(h.store)).noRoomAt;
    expect(answered).toBe(h.now());

    h.advance(31 * 60_000);
    const corridor = {
      ...h.clinic.transport(),
      push: async () => {
        throw new NetworkError(new Error('no signal'));
      },
    };
    expect(await checkWithClinic(h.store, { now: h.now })).toBe('asking');
    await syncOnce({ ...h.deps, transport: corridor });
    expect((await readMetrics(h.store)).noRoomAt).toBe(answered);

    // And it moves when the clinic answers, even though the answer has not changed: a newer time
    // beside the same sentence is the operator learning something true.
    h.advance(60_000);
    await syncOnce(h.deps);
    const metrics = await readMetrics(h.store);
    expect(metrics.noRoomAt).toBe(h.now());
    expect(metrics.awaitingTriage).toBe(1);
    expect(statusOf(metrics)).toBe('stalled');
  });
});

describe('the clinic asking for a pause', () => {
  it('waits as long as the server asked rather than the next rung of its own ladder', async () => {
    const h = await harness();
    await h.record();
    const limited = {
      ...h.clinic.transport(),
      push: async () => {
        throw apiError(429, 'RATE_LIMITED', 45);
      },
    };

    const report = await syncOnce({ ...h.deps, transport: limited });
    expect(report.failure).toBe('server');

    // The ladder's first rung is two seconds. The server said forty-five, and it is the one that
    // knows the state of its own bucket.
    const row = (await readOutbox(h.store))[0];
    expect(row?.state).toBe('IN_FLIGHT');
    expect((row?.nextAttemptAt ?? 0) - h.now()).toBeGreaterThanOrEqual(45_000);

    // And it is honoured: nothing is attempted before the time the clinic named.
    h.advance(30_000);
    expect((await syncOnce({ ...h.deps, transport: limited })).waiting).toBe(true);
  });

  it('never waits less than the number it was given, and not in lockstep with the next tablet', () => {
    for (const seconds of [1, 5, 45, 300]) {
      expect(retryAfterDelay(seconds, 0)).toBeGreaterThanOrEqual(seconds * 1_000);
      expect(retryAfterDelay(seconds, 1)).toBeGreaterThanOrEqual(seconds * 1_000);
      expect(retryAfterDelay(seconds, 0)).not.toBe(retryAfterDelay(seconds, 1));
    }
    // A server that says nothing, and a proxy that says something absurd.
    expect(retryAfterOf(apiError(429, 'RATE_LIMITED'))).toBeNull();
    expect(retryAfterDelay(86_400, 1)).toBeLessThanOrEqual(MAX_RETRY_AFTER_MS * 1.5);
    // Zero means "now", and a client that took it literally would spin against an empty bucket.
    expect(retryAfterDelay(0, 0)).toBeGreaterThanOrEqual(1_000);
  });

  it('tells a pause from a full quarantine, because only one of them means nothing was written', () => {
    expect(classifyFailure(apiError(429, 'RATE_LIMITED', 5))).toBe('server');
    expect(classifyFailure(apiError(429, 'SYNC_QUARANTINE_FULL'))).toBe('quarantine-full');
    // Both are still "try again later"; what differs is what the client may assume happened.
    expect(retryable(classifyFailure(apiError(429, 'SYNC_QUARANTINE_FULL')))).toBe(true);
  });

  it('reads the two BLOCKED reasons as two different instructions', () => {
    expect(actionFor('BLOCKED')).toBe('retry');
    expect(actionFor('BLOCKED', REASON_CODES.earlierFailed)).toBe('retry');
    expect(actionFor('BLOCKED', REASON_CODES.quarantineFull)).toBe('ceiling');
    // The wait is a person's reading list, not a network: minutes, not seconds.
    expect(triageDelay(0)).toBeGreaterThan(BACKOFF_LADDER_MS[BACKOFF_LADDER_MS.length - 1] ?? 0);
  });
});

// --- §13.10: a duplicate batch ---

describe('the same batch sent twice', () => {
  it('produces no duplicate events and empties the queue anyway', async () => {
    const h = await harness({ batchLimit: 4 });
    for (let index = 0; index < 4; index += 1) await h.record();

    // The clinic processes it; the answer is lost; the client asks again — and, because it kept
    // the receipt number, the second attempt sends the whole batch again under the same id.
    const dropped = {
      ...h.clinic.transport(),
      push: async (body: Parameters<typeof h.clinic.push>[0]) => {
        h.clinic.push(body);
        throw new NetworkError(new Error('lost'));
      },
      // The receipt request fails too, so the client falls back on re-sending the batch itself.
      receipt: async () => null,
    };
    await syncOnce({ ...h.deps, transport: dropped });
    h.advance(60_000);

    const resent = { ...h.clinic.transport(), receipt: async () => null };
    const report = await syncOnce({ ...h.deps, transport: resent });

    // The whole batch arrived twice and the clinic holds four events, not eight. The second
    // arrival is answered from the stored receipt without the ledger being touched again.
    expect(h.clinic.pushes).toHaveLength(2);
    expect(h.clinic.pushes[0]?.batch_id).toBe(h.clinic.pushes[1]?.batch_id);
    expect(h.clinic.ledger.size).toBe(4);
    expect(report.accepted + report.duplicated).toBe(4);
    expect(h.clinic.receipt(h.clinic.pushes[0]?.batch_id ?? '')?.replayed).toBe(true);
    expect(await undeliveredCount(h.store)).toBe(0);
    expect(await integrityProblems(h.store, h.clinic, { quiescent: true })).toEqual([]);
  });

  it('accepts the same events under a new batch number as duplicates, not as new measurements', async () => {
    // The other half of "duplicate submission": the same measurements arriving under a receipt
    // number the clinic has never seen. `event_id` is the ledger's key, so they land as
    // duplicates and the queue empties — which is what makes re-sending safe at all.
    const h = await harness({ batchLimit: 4 });
    for (let index = 0; index < 4; index += 1) await h.record();
    await syncOnce(h.deps);
    expect(h.clinic.ledger.size).toBe(4);

    const queue = await h.store.all({ table: TABLES.localEvents });
    const events = queue.map((row) => ({
      event_id: String(row.event_id),
      aggregate_type: String(row.aggregate_type),
      aggregate_id: String(row.aggregate_id),
      event_type: String(row.event_type),
      event_version: Number(row.event_version),
      occurred_at: String(row.occurred_at),
      payload: JSON.parse(String(row.payload)) as Record<string, unknown>,
    }));
    const receipt = h.clinic.push({ batch_id: 'a-brand-new-batch', device_id: DEVICE, events });
    expect(receipt.duplicated).toBe(4);
    expect(receipt.accepted).toBe(0);
    expect(h.clinic.ledger.size).toBe(4);
  });

  it('treats DUPLICATE as delivered and REJECTED as not, from the same receipt', () => {
    expect(actionFor('ACCEPTED')).toBe('drop');
    expect(actionFor('DUPLICATE')).toBe('drop');
    expect(actionFor('REJECTED')).toBe('attention');
    expect(actionFor('QUARANTINED')).toBe('held');
    expect(actionFor('BLOCKED')).toBe('retry');
    // An outcome a newer clinic invents must never make a queued measurement disappear.
    expect(actionFor('SOMETHING_NEW')).toBe('attention');
  });
});

// --- the receipt that mentions nothing ---

describe('a batch the clinic opened and never finished', () => {
  it('resends it under the same receipt number and is told, per event', async () => {
    // The server opens the batch row before the first event and closes it after the last, so an
    // interrupted batch leaves a receipt that exists and reports nothing. It comes back
    // `closed: false`, and a push under that id is reprocessed rather than answered from it —
    // which is the receipt doing exactly what it exists for. A client that started a fresh batch
    // here would be throwing that away.
    const h = await harness({ batchLimit: 6 });
    for (let index = 0; index < 6; index += 1) await h.record();

    h.clinic.crashAfter = 3;
    await syncOnce(h.deps);
    expect(h.clinic.ledger.size).toBe(3);
    const opened = h.clinic.pushes[0]?.batch_id;
    expect(h.clinic.receipt(opened ?? '')?.closed).toBe(false);

    h.advance(60_000);
    const report = await syncOnce(h.deps);

    // One receipt number throughout, and the three that had landed come back DUPLICATE rather
    // than as second measurements.
    expect(new Set(h.clinic.pushes.map((push) => push.batch_id)).size).toBe(1);
    expect(report.unanswered).toBe(0);
    expect(report.duplicated).toBe(3);
    expect(report.accepted).toBe(3);
    expect(h.clinic.ledger.size).toBe(6);
    expect(await undeliveredCount(h.store)).toBe(0);
    expect(await integrityProblems(h.store, h.clinic, { quiescent: true })).toEqual([]);
  });

  it('still recovers if a closed receipt somehow leaves an event unmentioned', async () => {
    // The anomaly that should not happen. Asking again about a closed batch returns the same
    // silence for ever, so the event goes back to the queue without a batch id — safe only
    // because `event_id` is the ledger's key.
    const h = await harness({ batchLimit: 2 });
    await h.record();
    await h.record();
    const queue = await readOutbox(h.store);
    const forgotten = queue[1]?.eventId ?? '';

    const forgetful = {
      ...h.clinic.transport(),
      push: async (body: Parameters<typeof h.clinic.push>[0]) => {
        const receipt = h.clinic.push(body);
        return { ...receipt, results: receipt.results.filter((r) => r.event_id !== forgotten) };
      },
      // The pull is stubbed out so the requeue is observable. Left real, it would resolve the row
      // by the other route — the event is in the ledger, so pulling it back down proves the
      // clinic has it and drops it from the queue, which is the belt to this braces.
      pull: async (since: number) => ({ events: [], cursor: since, latest: since, more: false }),
    };
    const report = await syncOnce({ ...h.deps, transport: forgetful });
    expect(report.unanswered).toBe(1);
    const left = await readOutbox(h.store);
    expect(left.map((row) => row.eventId)).toEqual([forgotten]);
    expect(left[0]?.batchId).toBeNull();
    expect(left[0]?.state).toBe('PENDING');
    // Not immediately: something is wrong on the other side, and re-sending as fast as batches
    // can be built would be a hot loop rather than a recovery.
    expect(left[0]?.nextAttemptAt).toBeGreaterThan(h.now());
  });
});

// --- §13.10: the device clock is three hours wrong ---

describe('a tablet whose clock is three hours out', () => {
  it('keeps the timestamps it recorded and says how far out it is', async () => {
    const h = await harness();
    // Three hours behind: every measurement is dated three hours ago. The clinic accepts it —
    // being in the past is unbounded and is the whole point of an offline queue — and the engine
    // reports the skew rather than quietly correcting a clinical timestamp.
    const behind = new Date(START - 3 * 60 * 60 * 1000).toISOString();
    await h.record({ occurredAt: behind });
    await syncOnce(h.deps);

    expect([...h.clinic.ledger.values()][0]?.occurred_at).toBe(behind);
    const metrics = await readMetrics(h.store);
    expect(metrics.skew.ms).toBe(0);

    // Now a device whose own clock is three hours fast: it sends a client clock three hours ahead
    // and the clinic says so on the receipt.
    const fast = { ...h.deps, now: () => h.now() + 3 * 60 * 60 * 1000 };
    await h.record();
    await syncOnce(fast);
    const after = await readMetrics(h.store);
    expect(after.skew.ms).toBe(3 * 60 * 60 * 1000);
    expect(after.skew.level).toBe('wrong');
    expect(after.skew.entriesWouldBeHeld).toBe(true);
    expect(after.skew.minutes).toBe(180);
  });

  it('has its future-dated entries held rather than accepted or refused', async () => {
    const h = await harness();
    const ahead = new Date(START + 3 * 60 * 60 * 1000).toISOString();
    await h.record({ occurredAt: ahead });

    const report = await syncOnce(h.deps);
    expect(report.held).toBe(1);
    const row = (await readOutbox(h.store))[0];
    expect(row?.state).toBe('HELD');
    expect(row?.reasonCode).toBe('CLOCK_IMPLAUSIBLE');
    // Held, not lost: the clinic has the whole event and a person decides what time it was.
    expect(h.clinic.held.map((held) => held.event_id)).toEqual([row?.eventId]);
    expect(h.clinic.ledger.size).toBe(0);
    // And it is not sent again — that would only produce another hold.
    h.advance(10 * 60_000);
    const before = h.clinic.pushes.length;
    await syncOnce(h.deps);
    expect(h.clinic.pushes).toHaveLength(before);
    expect(await integrityProblems(h.store, h.clinic)).toEqual([]);
  });

  it('reads a skew the way a person would have to explain it', () => {
    expect(skewReading(null).level).toBe('unknown');
    expect(skewReading(5_000).level).toBe('fine');
    expect(skewReading(90_000).level).toBe('drifting');
    expect(skewReading(6 * 60_000).level).toBe('wrong');
    expect(skewReading(6 * 60_000).entriesWouldBeHeld).toBe(true);
    // Behind is never a hold, however far — a device offline for a week is the point of all this.
    expect(skewReading(-7 * 24 * 60 * 60_000).entriesWouldBeHeld).toBe(false);
    expect(skewReading(-90_000).ahead).toBe(false);
  });
});

// --- §13.10: the token expires while offline ---

describe('a session that expires while the tablet is offline', () => {
  it('keeps the queue and delivers it once somebody signs in again', async () => {
    const h = await harness();
    for (let index = 0; index < 3; index += 1) await h.record();

    let signedIn = false;
    const gated = {
      ...h.clinic.transport(),
      push: async (body: Parameters<typeof h.clinic.push>[0]) => {
        if (!signedIn) throw apiError(401);
        return h.clinic.push(body);
      },
      receipt: async (id: string) => {
        if (!signedIn) throw apiError(401);
        return h.clinic.receipt(id);
      },
      pull: async (since: number, limit: number) => {
        if (!signedIn) throw apiError(401);
        return h.clinic.pull(since, limit);
      },
    };

    const report = await syncOnce({ ...h.deps, transport: gated });
    expect(report.failure).toBe('unauthenticated');
    // Nothing is lost, and nothing pretends to be delivered.
    expect(await undeliveredCount(h.store)).toBe(3);
    expect(await readMeta(h.store, META_KEYS.authFailures)).toBe('1');

    signedIn = true;
    h.advance(10 * 60_000);
    const after = await syncOnce({ ...h.deps, transport: gated });
    expect(after.accepted).toBe(3);
    expect(await undeliveredCount(h.store)).toBe(0);
    // The counter that would eventually halt sync is reset by a success.
    expect(await readMeta(h.store, META_KEYS.authFailures)).toBe('0');
    expect(await integrityProblems(h.store, h.clinic, { quiescent: true })).toEqual([]);
  });
});

// --- §13.10: the device is revoked while offline ---

describe('a device revoked while it was offline', () => {
  it('hands the morning over to the quarantine and then stops', async () => {
    // The tablet revoked at nine, offline since eight, holding forty real blood pressures on
    // patients who have gone home. `POST /v1/sync/events` is the one route it may still reach,
    // so the right behaviour is to push the backlog — every event held rather than accepted,
    // which is the point — and only then stop.
    const h = await harness();
    for (let index = 0; index < 40; index += 1) await h.record();
    h.clinic.deviceStatus = 'revoked';

    const report = await syncOnce(h.deps);

    expect(report.held).toBe(40);
    expect(report.delivering).toBe(true);
    // Nothing accepted into the ledger: the revocation stands.
    expect(h.clinic.ledger.size).toBe(0);
    // And nothing lost: all forty are at the clinic, whole, for a physician to decide about.
    expect(h.clinic.held).toHaveLength(40);
    const queue = await readOutbox(h.store);
    expect(queue.every((row) => row.state === 'HELD')).toBe(true);
    expect(queue.every((row) => row.reasonCode === 'DEVICE_REVOKED')).toBe(true);
    // Nothing left to hand over, so sync stops for good rather than looping.
    expect(report.halted).toBe(HALT_REASONS.deviceRefused);
    expect(await integrityProblems(h.store, h.clinic)).toEqual([]);

    const before = h.clinic.pushes.length;
    h.advance(60 * 60_000);
    const after = await syncOnce(h.deps);
    expect(after.halted).toBe(HALT_REASONS.deviceRefused);
    expect(h.clinic.pushes).toHaveLength(before);
  });

  it('does not treat the refusals on every other route as a reason to stop pushing', async () => {
    // A revoked device gets 401 on the pull, on the catalogues and on its own state. Counting
    // those as failures would halt sync before the backlog had been handed over — the morning
    // stranded on the tablet, which is the outcome the quarantine exists to prevent.
    const h = await harness({ batchLimit: 5 });
    for (let index = 0; index < 12; index += 1) await h.record();
    h.clinic.deviceStatus = 'revoked';

    let pulls = 0;
    const counting = {
      ...h.clinic.transport(),
      pull: async (since: number, limit: number) => {
        pulls += 1;
        return h.clinic.transport().pull(since, limit);
      },
    };

    const report = await syncOnce({ ...h.deps, transport: counting });
    expect(report.held).toBe(12);
    expect(report.failure).toBeNull();
    // The pull was skipped rather than attempted and counted as a failure.
    expect(pulls).toBe(0);
    expect(await readMeta(h.store, META_KEYS.authFailures)).not.toBe('3');
    expect(h.clinic.held).toHaveLength(12);
  });

  it('stops handing over the moment the clinic starts accepting again', async () => {
    // Reinstated part way through the handover. A tablet that finished delivering and then halted
    // as though it were still refused would be a working device stopping itself.
    const h = await harness({ batchLimit: 4 });
    for (let index = 0; index < 12; index += 1) await h.record();
    h.clinic.deviceStatus = 'revoked';

    const first = await syncOnce({ ...h.deps, maxBatches: 1 });
    expect(first.held).toBe(4);
    expect(first.delivering).toBe(true);
    expect(first.halted).toBe('');

    h.clinic.deviceStatus = 'active';
    h.advance(60_000);
    const second = await syncOnce(h.deps);
    expect(second.accepted).toBe(8);
    expect(second.delivering).toBe(false);
    expect(second.halted).toBe('');
    expect(await undeliveredCount(h.store)).toBe(4);
    expect(await integrityProblems(h.store, h.clinic)).toEqual([]);
  });

  it('survives a dropped connection mid-handover instead of stopping with the morning still on it', async () => {
    // The whole handover rests on one route, and the recovery path used to leave it. A revoked
    // tablet whose push is interrupted has rows in flight with a batch id; asking
    // `GET /v1/sync/batches/{id}` about them is a 401, because that route is not exempt — and
    // three of those in a row are how this engine recognises a refused device. The tablet would
    // halt with the forty measurements it was in the middle of handing over, and never push
    // again, because resolving an in-flight batch happens before the push loop.
    const h = await harness({ batchLimit: 5 });
    for (let index = 0; index < 12; index += 1) await h.record();
    h.clinic.deviceStatus = 'revoked';

    const first = await syncOnce({ ...h.deps, maxBatches: 1 });
    expect(first.held).toBe(5);
    expect(first.delivering).toBe(true);

    // The clinic does the work on the second batch and the answer dies on the way back — the one
    // case the receipt number exists for, now happening to the one device that cannot ask.
    let lost = false;
    let receipts = 0;
    const answerLost = {
      ...h.clinic.transport(),
      push: async (body: PushBody) => {
        const receipt = h.clinic.push(body);
        if (lost) return receipt;
        lost = true;
        throw new NetworkError(new Error('the response was lost'));
      },
      receipt: async (batchId: string) => {
        receipts += 1;
        return h.clinic.transport().receipt(batchId);
      },
    };

    h.advance(60_000);
    const second = await syncOnce({ ...h.deps, transport: answerLost, maxBatches: 1 });
    expect(second.failure).toBe('offline');
    const inFlight = await readOutbox(h.store, ['IN_FLIGHT']);
    expect(inFlight).toHaveLength(5);
    expect(inFlight.every((row) => row.batchId !== null)).toBe(true);
    expect(h.clinic.held).toHaveLength(10);

    // It recovers by itself, with nobody touching it: the same batch id, sent again, on the one
    // route it may use. The clinic answers that batch from its stored receipt without touching
    // the ledger, so the five come back held rather than held a second time.
    h.advance(60_000);
    const third = await syncOnce({ ...h.deps, transport: answerLost });

    expect(receipts).toBe(0);
    expect(await readNumber(h.store, META_KEYS.authFailures, 0)).toBe(0);
    expect(h.clinic.held).toHaveLength(12);
    expect(h.clinic.ledger.size).toBe(0);
    const queue = await readOutbox(h.store);
    expect(queue).toHaveLength(12);
    expect(queue.every((row) => row.state === 'HELD')).toBe(true);
    // And only now, with the whole morning handed over, does it stop.
    expect(third.halted).toBe(HALT_REASONS.deviceRefused);
    expect(await integrityProblems(h.store, h.clinic)).toEqual([]);
  });

  it('still respects the ceiling when it sends an interrupted batch again', async () => {
    // The re-push goes through the machinery, not around it. A handover that could ignore the
    // ceiling would be the flood the ceiling exists to stop, arriving from the recovery path.
    const h = await harness({ batchLimit: 5, clinic: { quarantineCap: 5 } });
    for (let index = 0; index < 12; index += 1) await h.record();
    h.clinic.deviceStatus = 'revoked';

    await syncOnce({ ...h.deps, maxBatches: 1 });
    expect(h.clinic.held).toHaveLength(5);

    const corridor = {
      ...h.clinic.transport(),
      push: async () => {
        throw new NetworkError(new Error('no signal'));
      },
    };
    h.advance(60_000);
    await syncOnce({ ...h.deps, transport: corridor, maxBatches: 1 });
    expect(await readOutbox(h.store, ['IN_FLIGHT'])).toHaveLength(5);

    h.advance(60_000);
    const report = await syncOnce(h.deps);
    expect(report.failure).toBe('quarantine-full');
    expect(report.halted).toBe('');
    const parked = await readOutbox(h.store, ['AWAITING_TRIAGE']);
    expect(parked).toHaveLength(5);
    expect(parked.every((row) => row.batchId === null)).toBe(true);
  });

  it('still waits as long as the clinic asked when it sends an interrupted batch again', async () => {
    const h = await harness({ batchLimit: 5 });
    for (let index = 0; index < 12; index += 1) await h.record();
    h.clinic.deviceStatus = 'revoked';
    await syncOnce({ ...h.deps, maxBatches: 1 });

    const corridor = {
      ...h.clinic.transport(),
      push: async () => {
        throw new NetworkError(new Error('no signal'));
      },
    };
    h.advance(60_000);
    await syncOnce({ ...h.deps, transport: corridor, maxBatches: 1 });

    const limited = {
      ...h.clinic.transport(),
      push: async () => {
        throw apiError(429, 'RATE_LIMITED', 600);
      },
    };
    h.advance(60_000);
    const report = await syncOnce({ ...h.deps, transport: limited });

    expect(report.failure).toBe('server');
    const inFlight = await readOutbox(h.store, ['IN_FLIGHT']);
    expect(inFlight).toHaveLength(5);
    // Kept, with the batch id, waiting out the number the clinic named rather than the ladder's.
    expect(inFlight.every((row) => row.nextAttemptAt - h.now() >= 600_000)).toBe(true);
    expect(await undeliveredCount(h.store)).toBe(12);
  });

  it('starts again if an administrator reinstates the tablet', async () => {
    const h = await harness();
    for (let index = 0; index < 3; index += 1) await h.record();
    h.clinic.deviceStatus = 'suspended';
    await syncOnce(h.deps);
    expect((await readMetrics(h.store)).halted).toBe(HALT_REASONS.deviceRefused);

    // The held events are the clinic's to release now; what resumes is the device, not the queue.
    h.clinic.deviceStatus = 'active';
    await resume(h.store);
    await h.record();
    h.advance(60_000);
    const report = await syncOnce(h.deps);
    expect(report.accepted).toBe(1);
    expect(report.delivering).toBe(false);
    expect(await integrityProblems(h.store, h.clinic)).toEqual([]);
  });
});

describe('a session the clinic refuses everywhere', () => {
  it('stops after three refusals rather than looping, and keeps every entry', async () => {
    // Not a revoked device — that one may still push. This is a session the clinic will not
    // accept at all: the refresh worked and the request was still refused.
    const h = await harness();
    for (let index = 0; index < 6; index += 1) await h.record();
    const refusing = {
      ...h.clinic.transport(),
      push: async () => {
        throw apiError(401);
      },
      receipt: async () => {
        throw apiError(401);
      },
      pull: async () => {
        throw apiError(401);
      },
    };

    for (let attempt = 0; attempt < MAX_AUTH_FAILURES; attempt += 1) {
      await syncOnce({ ...h.deps, transport: refusing });
      h.advance(60 * 60_000);
    }

    const metrics = await readMetrics(h.store);
    expect(metrics.halted).toBe(HALT_REASONS.deviceRefused);
    expect(metrics.undelivered).toBe(6);

    const report = await syncOnce({ ...h.deps, transport: refusing });
    expect(report.halted).toBe(HALT_REASONS.deviceRefused);
    expect(await undeliveredCount(h.store)).toBe(6);
  });
});

describe('a tablet whose enrolment and key have come apart', () => {
  it('stops and says so rather than sending a refusal every five minutes', async () => {
    // `device_id` is sent as a self-check: the clinic takes the sending device from the
    // signature and only *checks* the body. A refusal means this tablet's stored enrolment id is
    // not the device its key signs as, and no amount of resending repairs that.
    const h = await harness();
    await h.record();
    const wrong = { ...h.deps, deviceId: 'a-different-device' };

    const report = await syncOnce(wrong);
    expect(report.failure).toBe('device-mismatch');
    expect(report.halted).toBe(HALT_REASONS.deviceMismatch);
    // The queue is untouched, and nothing was accepted under the wrong name.
    expect(await undeliveredCount(h.store)).toBe(1);
    expect(h.clinic.ledger.size).toBe(0);
  });
});

// --- §13.10: a lossy network ---

describe('a network that drops one request in ten', () => {
  it('gets everything there exactly once, whatever it drops', async () => {
    const h = await harness({ batchLimit: 7 });
    const random = seeded(20260905);
    const drops: string[] = [];
    const transport = lossy(h.clinic.transport(), {
      rate: 0.1,
      random,
      onDrop: (phase) => drops.push(phase),
    });
    const deps = { ...h.deps, transport, random };

    for (let round = 0; round < 12; round += 1) {
      for (let index = 0; index < 5; index += 1) {
        await h.record({ payload: { code: 'BODY_WEIGHT', value: round * 5 + index } });
        h.advance(1_000);
      }
      await syncOnce(deps);
      h.advance(10 * 60_000);
    }
    // Drain, on a connection that has come good.
    for (let round = 0; round < 6; round += 1) {
      await syncOnce(h.deps);
      h.advance(10 * 60_000);
    }

    expect(drops.length).toBeGreaterThan(0);
    expect(await undeliveredCount(h.store)).toBe(0);
    expect(h.clinic.ledger.size).toBe(60);
    expect(await integrityProblems(h.store, h.clinic, { quiescent: true })).toEqual([]);
  });

  it('survives losing the answer rather than the request', async () => {
    // The harder half: the clinic did the work and the response died. Everything the receipt
    // exists for is in this case.
    const h = await harness({ batchLimit: 5 });
    const random = seeded(7);
    const transport = lossy(h.clinic.transport(), { rate: 0.35, random, mode: 'after' });
    for (let round = 0; round < 8; round += 1) {
      for (let index = 0; index < 3; index += 1) await h.record();
      await syncOnce({ ...h.deps, transport, random });
      h.advance(10 * 60_000);
    }
    for (let round = 0; round < 8; round += 1) {
      await syncOnce(h.deps);
      h.advance(10 * 60_000);
    }
    expect(await undeliveredCount(h.store)).toBe(0);
    expect(h.clinic.ledger.size).toBe(24);
    expect(await integrityProblems(h.store, h.clinic, { quiescent: true })).toEqual([]);
  });
});

// --- §13.10: the device runs out of room ---

describe('a device that runs out of room mid-sync', () => {
  it('keeps the batch, halts, and resolves it from the receipt once there is space', async () => {
    const h = await harness({ batchLimit: 3 });
    for (let index = 0; index < 3; index += 1) await h.record();

    // The clinic answers; there is no room to write down what it said. The first commits of the
    // tick — the bookkeeping and the batch being marked in flight — go through, and the one that
    // records the receipt does not.
    h.store.faults.afterCommits = h.store.commits + 2;
    h.store.faults.failCommits = 1;
    const report = await syncOnce(h.deps);
    expect(report.halted).toBe(HALT_REASONS.storageFull);
    expect(h.clinic.ledger.size).toBe(3);
    // The rows are still in flight, still carrying the receipt number, so the answer can be asked
    // for again rather than guessed at.
    expect(await readOutbox(h.store, ['IN_FLIGHT'])).toHaveLength(3);

    h.store.faults.failCommits = 0;
    h.store.faults.afterCommits = 0;
    await resume(h.store);
    h.advance(60_000);
    const after = await syncOnce(h.deps);
    expect(after.duplicated + after.accepted).toBe(3);
    expect(await undeliveredCount(h.store)).toBe(0);
    expect(h.clinic.ledger.size).toBe(3);
    expect(await integrityProblems(h.store, h.clinic, { quiescent: true })).toEqual([]);
  });
});

// --- pulling and reconciling ---

describe('what the clinic sends down', () => {
  it('applies each event once, however often it is pulled', async () => {
    const h = await harness();
    h.clinic.append({
      event_id: 'server-1',
      aggregate_type: 'patient',
      aggregate_id: OTHER_PATIENT,
      patient_id: OTHER_PATIENT,
      event_type: 'WEIGHT_RECORDED',
      event_version: 1,
      occurred_at: '2026-09-05T07:50:00.000Z',
      payload: { code: 'BODY_WEIGHT', value: 64.2 },
    });

    const first = await syncOnce(h.deps);
    expect(first.pulled).toBe(1);
    expect(first.cursor).toBe(1);
    const projection = await readProjection(h.store, 'observation', `${OTHER_PATIENT}/BODY_WEIGHT`);
    expect(projection?.document.value).toBe(64.2);
    expect(projection?.confirmed).toBe(true);

    // Pulling from the beginning again changes nothing.
    await h.store.run({
      kind: 'insert',
      table: TABLES.syncMeta,
      row: { key: META_KEYS.cursor, value: '0' },
      onConflict: 'replace',
    });
    h.advance(60_000);
    const second = await syncOnce(h.deps);
    expect(second.pulled).toBe(1);
    expect(await h.store.all({ table: TABLES.localEvents })).toHaveLength(1);
  });

  it('advances the cursor only past what it managed to apply', async () => {
    const h = await harness();
    h.clinic.append({
      event_id: 'server-first',
      aggregate_type: 'patient',
      aggregate_id: OTHER_PATIENT,
      patient_id: OTHER_PATIENT,
      event_type: 'WEIGHT_RECORDED',
      event_version: 1,
      occurred_at: '2026-09-05T07:00:00.000Z',
      payload: { code: 'BODY_WEIGHT', value: 1 },
    });
    await syncOnce(h.deps);
    expect(await readMeta(h.store, META_KEYS.cursor)).toBe('1');

    for (let index = 0; index < 4; index += 1) {
      h.clinic.append({
        event_id: `server-${index}`,
        aggregate_type: 'patient',
        aggregate_id: OTHER_PATIENT,
        patient_id: OTHER_PATIENT,
        event_type: 'WEIGHT_RECORDED',
        event_version: 1,
        occurred_at: `2026-09-05T07:5${index}:00.000Z`,
        // A different code each time, so each one is its own projection row rather than a replace.
        payload: { code: `CODE_${index}`, value: index },
      });
    }

    // Room for some of them and not all. The limit is on every row this device holds, which is
    // what running out of space actually means.
    const total = Object.values(h.store.disk.tables).reduce((sum, rows) => sum + rows.length, 0);
    h.store.faults.rowLimit = total + 4;
    h.advance(60_000);
    await syncOnce(h.deps);

    const applied = await h.store.all({ table: TABLES.localEvents });
    expect(applied.length).toBeGreaterThan(1);
    expect(applied.length).toBeLessThan(5);
    // The cursor is where the last applied event is, and not one place further: the next attempt
    // asks for the one that did not apply rather than skipping it for ever.
    const highest = Math.max(...applied.map((row) => Number(row.global_seq ?? 0)));
    expect(Number(await readMeta(h.store, META_KEYS.cursor))).toBe(highest);

    // With room again, the rest arrive, once each.
    h.store.faults.rowLimit = undefined;
    await resume(h.store);
    h.advance(60_000);
    await syncOnce(h.deps);
    expect(await h.store.all({ table: TABLES.localEvents })).toHaveLength(5);
    expect(await integrityProblems(h.store, h.clinic, { quiescent: true })).toEqual([]);
  });

  it('drops an event from the queue when it comes back down from the ledger', async () => {
    // The second, independent confirmation path: whatever happened to the receipt, seeing our own
    // event in the ledger proves the clinic has it.
    const h = await harness({ batchLimit: 2 });
    const mine = await h.record();
    const dropped = {
      ...h.clinic.transport(),
      push: async (body: Parameters<typeof h.clinic.push>[0]) => {
        h.clinic.push(body);
        throw new NetworkError(new Error('lost'));
      },
      receipt: async () => null,
      pull: async (since: number, limit: number) => h.clinic.pull(since, limit),
    };
    await syncOnce({ ...h.deps, transport: dropped });
    expect(await readOutbox(h.store, ['IN_FLIGHT'])).toHaveLength(1);

    // A pull that includes it, with the push still failing.
    h.advance(60_000);
    await syncOnce({
      ...h.deps,
      transport: {
        ...dropped,
        push: async () => {
          throw new NetworkError(new Error('still no'));
        },
        receipt: async (id: string) => h.clinic.receipt(id),
      },
    });
    const remaining = await readOutbox(h.store);
    expect(remaining.map((row) => row.eventId)).not.toContain(mine.eventId);
    expect(await integrityProblems(h.store, h.clinic)).toEqual([]);
  });

  it('leaves a later value alone when an earlier one arrives afterwards', async () => {
    const h = await harness();
    await h.record({
      occurredAt: '2026-09-05T09:00:00.000Z',
      payload: { code: 'BODY_WEIGHT', value: 72 },
    });
    h.clinic.append({
      event_id: 'server-early',
      aggregate_type: 'patient',
      aggregate_id: PATIENT,
      patient_id: PATIENT,
      event_type: 'WEIGHT_RECORDED',
      event_version: 1,
      occurred_at: '2026-09-05T08:00:00.000Z',
      payload: { code: 'BODY_WEIGHT', value: 71 },
    });

    await syncOnce(h.deps);
    const projection = await readProjection(h.store, 'observation', `${PATIENT}/BODY_WEIGHT`);
    expect(projection?.document.value).toBe(72);
    // Both are on the device, which is what §4.3 asks of the history.
    expect(await h.store.all({ table: TABLES.localEvents })).toHaveLength(2);
  });
});

// --- reference data ---

describe('the catalogues', () => {
  it('reports the ones whose fingerprint no longer matches, and nothing else', async () => {
    const h = await harness();
    const first = await syncOnce(h.deps);
    expect(first.stale.sort()).toEqual(['exercises', 'terminology']);

    await cacheCatalogue(h.store, 'terminology', 'fp-terminology-1', [{ code: 'x' }], h.now());
    await cacheCatalogue(h.store, 'exercises', 'fp-exercises-1', [], h.now());
    h.advance(60_000);
    const second = await syncOnce(h.deps);
    expect(second.stale).toEqual([]);
  });
});

// --- the rules, on their own ---

describe('the batch, before it is sent', () => {
  const row = (over: Partial<OutboxRow> = {}): OutboxRow => ({ ...baseRow(), ...over });
  function baseRow(): OutboxRow {
    return {
      eventId: 'e1',
      seq: 1,
      aggregateType: 'patient',
      aggregateId: PATIENT,
      patientId: PATIENT,
      visitId: null,
      eventType: 'WEIGHT_RECORDED',
      eventVersion: 1,
      occurredAt: '2026-09-05T08:00:00.000Z',
      payload: '{}',
      metadata: null,
      expectedSequence: null,
      state: 'PENDING',
      attempts: 0,
      nextAttemptAt: 0,
      batchId: null,
      reasonCode: null,
      reason: null,
      createdAt: 0,
    };
  }

  it('goes in the order things were recorded, not in the order of a clock that may be wrong', () => {
    // A tablet whose clock is corrected mid-session records the second reading with an earlier
    // timestamp than the first. Sorting by `occurred_at` would invert them on the wire, and the
    // clinic applies a batch in the order it was sent.
    const batch = selectBatch(
      [
        row({ eventId: 'first', seq: 1, occurredAt: '2026-09-05T11:00:00.000Z' }),
        row({ eventId: 'second', seq: 2, occurredAt: '2026-09-05T08:00:00.000Z' }),
      ],
      { now: 1_000 },
    );
    expect(batch.map((one) => one.eventId)).toEqual(['first', 'second']);
  });

  it('holds back everything after an event on the same record that is not ready', () => {
    const batch = selectBatch(
      [
        row({ eventId: 'waiting', seq: 1, nextAttemptAt: 5_000 }),
        row({ eventId: 'after-it', seq: 2 }),
        row({ eventId: 'other-record', seq: 3, aggregateId: OTHER_PATIENT }),
      ],
      { now: 1_000 },
    );
    expect(batch.map((one) => one.eventId)).toEqual(['other-record']);
  });

  it('lets an ordinary measurement past a refusal and holds back one that declared a dependency', () => {
    // Exactly what the clinic does: an observation is a fact about a moment, and holding a
    // morning's work behind one typo would be worse than the typo.
    const batch = selectBatch(
      [
        row({ eventId: 'refused', seq: 1, state: 'NEEDS_ATTENTION' }),
        row({ eventId: 'independent', seq: 2 }),
        row({ eventId: 'dependent', seq: 3, expectedSequence: 12 }),
      ],
      { now: 1_000 },
    );
    expect(batch.map((one) => one.eventId)).toEqual(['independent']);
  });

  it('waits out an entry the clinic had no room for, and lets the rest of that patient past it', () => {
    // The clinic itself accepts an independent later event while an earlier one is blocked — in
    // the same batch. A client that queued the whole record behind the stuck one would be
    // stricter than the clinic and would strand measurements nobody is holding.
    const batch = selectBatch(
      [
        row({ eventId: 'no-room', seq: 1, state: 'AWAITING_TRIAGE', nextAttemptAt: 900_000 }),
        row({ eventId: 'independent', seq: 2 }),
        row({ eventId: 'dependent', seq: 3, expectedSequence: 12 }),
        row({ eventId: 'other-record', seq: 4, aggregateId: OTHER_PATIENT }),
      ],
      { now: 1_000 },
    );
    expect(batch.map((one) => one.eventId)).toEqual(['independent', 'other-record']);
  });

  it('offers it again once its wait is up, without anybody clearing anything', () => {
    // The recovery path. A tablet whose quarantine has been triaged has no way of being told, so
    // the only thing that can put the entry back on the wire is this.
    const batch = selectBatch(
      [row({ eventId: 'no-room', state: 'AWAITING_TRIAGE', nextAttemptAt: 900_000 })],
      { now: 900_001 },
    );
    expect(batch.map((one) => one.eventId)).toEqual(['no-room']);
  });

  it('never sends more than one batch worth', () => {
    const rows = [...Array(250).keys()].map((index) => row({ eventId: `e${index}`, seq: index }));
    expect(selectBatch(rows, { now: 1_000 })).toHaveLength(BATCH_LIMIT);
    expect(selectBatch(rows, { now: 1_000, limit: 10 })).toHaveLength(10);
  });

  it('skips what another batch is already carrying', () => {
    const batch = selectBatch(
      [
        row({ eventId: 'sent', seq: 1, state: 'IN_FLIGHT', batchId: 'b1' }),
        row({ eventId: 'next', seq: 2 }),
      ],
      { now: 1_000 },
    );
    expect(batch).toEqual([]);
  });
});

describe('backing off', () => {
  it('follows the ladder and never retries faster than half a step', () => {
    for (let attempt = 1; attempt <= BACKOFF_LADDER_MS.length + 2; attempt += 1) {
      const step = BACKOFF_LADDER_MS[Math.min(attempt, BACKOFF_LADDER_MS.length) - 1] ?? 0;
      expect(backoffDelay(attempt, 0)).toBe(step / 2);
      expect(backoffDelay(attempt, 1)).toBe(step);
      expect(backoffDelay(attempt, 0.5)).toBeGreaterThanOrEqual(step / 2);
      expect(backoffDelay(attempt, 0.5)).toBeLessThanOrEqual(step);
    }
  });

  it('gives two tablets that lost the signal together two different times to come back', () => {
    // Without jitter every device in the building retries in the same second, and the access
    // point that was struggling with one of them fails all of them, repeatedly, in lockstep.
    const one = backoffDelay(3, 0.11);
    const other = backoffDelay(3, 0.87);
    expect(one).not.toBe(other);
    expect(Math.abs(one - other)).toBeGreaterThan(1_000);
  });

  it('caps rather than growing for ever', () => {
    const last = BACKOFF_LADDER_MS[BACKOFF_LADDER_MS.length - 1] ?? 0;
    expect(backoffDelay(50, 1)).toBe(last);
  });
});

describe('reading a failure', () => {
  it('tells a corridor from a refusal', () => {
    expect(classifyFailure(new NetworkError(new Error('no signal')))).toBe('offline');
    expect(classifyFailure(apiError(401))).toBe('unauthenticated');
    expect(classifyFailure(apiError(403))).toBe('forbidden');
    expect(classifyFailure(apiError(422, 'SYNC_BATCH_TOO_LARGE'))).toBe('too-large');
    expect(classifyFailure(apiError(500))).toBe('server');
    expect(classifyFailure(apiError(422, 'VALIDATION_FAILED'))).toBe('client');
    // A bug in this code is retryable rather than a reason to empty the queue.
    expect(classifyFailure(new TypeError('undefined is not a function'))).toBe('server');
  });
});

describe('a receipt that does not mention everything it was asked about', () => {
  it('requeues the unanswered rather than waiting for an answer that will not come', () => {
    const rows: OutboxRow[] = [{ eventId: 'a' }, { eventId: 'b' }].map((partial) => ({
      ...partial,
      seq: 1,
      aggregateType: 'patient',
      aggregateId: PATIENT,
      patientId: null,
      visitId: null,
      eventType: 'WEIGHT_RECORDED',
      eventVersion: 1,
      occurredAt: '2026-09-05T08:00:00.000Z',
      payload: '{}',
      metadata: null,
      expectedSequence: null,
      state: 'IN_FLIGHT',
      attempts: 1,
      nextAttemptAt: 0,
      batchId: 'batch-1',
      reasonCode: null,
      reason: null,
      createdAt: 0,
    }));
    const plan = planFromReceipt(rows, {
      batch_id: 'batch-1',
      received_at: '2026-09-05T08:00:00.000Z',
      server_time: '2026-09-05T08:00:00.000Z',
      events: 1,
      accepted: 1,
      duplicated: 0,
      rejected: 0,
      quarantined: 0,
      blocked: 0,
      closed: true,
      results: [{ event_id: 'a', outcome: 'ACCEPTED', global_seq: 4 }],
      replayed: true,
    });
    expect(plan.resolved.map((one) => one.eventId)).toEqual(['a']);
    expect(plan.unanswered).toEqual(['b']);
    expect(plan.unknown).toEqual([]);
  });
});

// --- the scheduler ---

describe('the loop around it', () => {
  it('runs one attempt at a time, however many callers ask', async () => {
    const h = await harness();
    await h.record();
    let running = 0;
    let overlaps = 0;
    const slow = {
      ...h.clinic.transport(),
      push: async (body: Parameters<typeof h.clinic.push>[0]) => {
        running += 1;
        if (running > 1) overlaps += 1;
        await new Promise((resolve) => setTimeout(resolve, 5));
        running -= 1;
        return h.clinic.push(body);
      },
    };
    const engine = createSyncEngine({ ...h.deps, transport: slow });
    const [a, b, c] = await Promise.all([
      engine.sync('manual'),
      engine.sync('connectivity'),
      engine.sync('foreground'),
    ]);
    expect(overlaps).toBe(0);
    // Everybody gets the answer of the attempt that ran.
    expect(a).toBe(b);
    expect(b).toBe(c);
    expect(h.clinic.ledger.size).toBe(1);
  });

  it('syncs on an interval until it is stopped', async () => {
    const h = await harness();
    const timers: (() => void)[] = [];
    const reports: unknown[] = [];
    const engine = createSyncEngine({
      ...h.deps,
      intervalMs: 1_000,
      setTimer: (fn) => {
        timers.push(fn);
        return timers.length;
      },
      clearTimer: () => undefined,
      onReport: (report) => reports.push(report),
    });
    engine.start();
    expect(engine.running).toBe(true);
    expect(timers).toHaveLength(1);

    await h.record();
    timers[0]?.();
    await vi.waitFor(() => expect(reports.length).toBe(1));
    expect(h.clinic.ledger.size).toBe(1);

    engine.stop();
    expect(engine.running).toBe(false);
  });
});

describe('starting again after a halt', () => {
  it('clears a halt that a sign-in fixes and leaves the others alone', async () => {
    const h = await harness();
    await noteSessionLost(h.store);
    expect(await resumeAfterSignIn(h.store)).toBe(true);
    expect((await readMetrics(h.store)).halted).toBe('');

    // A device the clinic has refused is not fixed by a different operator signing in, and a
    // halt that lifted itself every time somebody touched the tablet would be the loop CP66
    // says to avoid.
    await halt(h.store, HALT_REASONS.deviceRefused);
    expect(await resumeAfterSignIn(h.store)).toBe(false);
    expect((await readMetrics(h.store)).halted).toBe(HALT_REASONS.deviceRefused);
    await resume(h.store);
    expect((await readMetrics(h.store)).halted).toBe('');
  });
});

// --- the integrity check itself ---

describe('the integrity check', () => {
  it('notices the same measurement recorded under two ids', async () => {
    // A canary. A check that cannot fail is worse than no check, because it reads as oversight.
    const h = await harness();
    await h.record({ payload: { code: 'BODY_WEIGHT', value: 70 } });
    await syncOnce(h.deps);
    expect(await integrityProblems(h.store, h.clinic, { quiescent: true })).toEqual([]);

    const original = [...h.clinic.ledger.values()][0];
    if (!original) throw new Error('nothing landed');
    h.clinic.ledger.set('a-second-id', { ...original, event_id: 'a-second-id', global_seq: 99 });

    const problems = await integrityProblems(h.store, h.clinic, { quiescent: true });
    expect(problems.join('\n')).toContain('the same measurement');
  });

  it('notices a device that believes it synced something the clinic has never seen', async () => {
    const h = await harness();
    await h.record();
    await syncOnce(h.deps);
    h.clinic.ledger.clear();
    const problems = await integrityProblems(h.store, h.clinic);
    expect(problems.join('\n')).toContain('the clinic has never seen it');
  });

  it('notices a measurement that exists nowhere but on the device', async () => {
    const h = await harness();
    await h.record();
    await syncOnce(h.deps);
    // Something has emptied the outbox without the clinic having the event.
    h.clinic.ledger.clear();
    await h.store.run({ kind: 'update', table: TABLES.localEvents, set: { global_seq: null } });
    const problems = await integrityProblems(h.store, h.clinic);
    expect(problems.join('\n')).toContain('nowhere else');
  });
});
