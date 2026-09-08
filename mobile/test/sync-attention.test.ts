import { describe, expect, it, vi } from 'vitest';

/**
 * The failure ladder an operator can actually climb (CP67, §13.5, §13.9).
 *
 * CP66 proved that the counts are true. This file is about the four things CP67 adds on top of
 * them, and each `describe` is one of the checkpoint's own acceptance criteria:
 *
 *  1. the indicator is accurate at all times, and **never says everything is with the clinic over a
 *     non-empty queue** — including over a state nobody has written a case for;
 *  2. a refused entry is listed with a reason a person can act on, in their own language, and
 *     without the clinic's own words reaching somebody who could not have read them;
 *  3. the two acts work — correct and resubmit, or escalate — and neither loses a measurement;
 *  4. every state has words, in both languages, that a non-technical person can read.
 *
 * Everything here runs against the real memory store and the real `FakeClinic`, not against
 * fixtures: a rejection is produced by making the clinic refuse an event, and the correction is
 * pushed to it and accepted, because the interesting assertions are about what is in the ledger
 * afterwards and how many times.
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

const { MIGRATIONS, OUTBOX_STATES, createDisk, createMemoryStore } =
  await import('../src/lib/local-store');
const { issueCommand } = await import('../src/lib/sync/commands');
const { syncOnce } = await import('../src/lib/sync/engine');
const { readCounts, readMetrics, readOutbox, readProjection, readSyncLog } =
  await import('../src/lib/sync/outbox');
const { REASON_CODES, selectBatch, statusOf, toneOf } = await import('../src/lib/sync/state');
const { correctionFor, escalate, referenceOf, resubmit } =
  await import('../src/lib/sync/attention');
const { ACTS, OFFLINE_FACTS, actsFor, entryKey, itemsOf, pillFor, reasonFor } =
  await import('../src/features/sync/state');
const { VITAL_FIELDS } = await import('../src/features/vitals/form');
const { ANTHRO_FIELDS } = await import('../src/features/anthropometry/form');

import en from '../src/messages/en.json';
import bn from '../src/messages/bn.json';

import { FakeClinic } from './fake-sync-server';
import { integrityProblems } from './sync-integrity';

import type { OutboxRow, SyncMetrics } from '../src/lib/sync/state';

const KEY = 'ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554433221100';
const DEVICE = 'device-1';
const PATIENT = 'patient-1';
const START = Date.parse('2026-09-05T08:00:00.000Z');

let counter = 0;
const nextId = () => `id-${(counter += 1).toString().padStart(4, '0')}`;

async function harness(options: { rejectAll?: boolean } = {}) {
  counter = 0;
  const disk = createDisk();
  const store = createMemoryStore({ disk });
  await store.open({ key: KEY });
  await store.migrate(MIGRATIONS);
  let clock = START;
  const clinic = new FakeClinic({ now: () => clock });
  if (options.rejectAll === true) {
    clinic.rejectWhen = () => ({
      code: REASON_CODES.invalidPayload,
      reason: 'value 999 out of range for BODY_WEIGHT',
    });
  }
  const state = {
    get store() {
      return store;
    },
    clinic,
    now: () => clock,
    advance: (ms: number) => {
      clock += ms;
    },
    deps: {
      get store() {
        return store;
      },
      transport: clinic.transport(),
      deviceId: DEVICE,
      now: () => clock,
      random: () => 0.5,
      newId: nextId,
    },
    record: async (over: Record<string, unknown> = {}) => {
      const issued = await issueCommand(
        store,
        {
          aggregateType: 'patient',
          aggregateId: PATIENT,
          patientId: PATIENT,
          visitId: 'visit-1',
          eventType: 'OBSERVATION_RECORDED',
          eventVersion: 1,
          occurredAt: new Date(clock).toISOString(),
          payload: {
            observation_id: nextId(),
            facility_id: 'facility-1',
            patient_id: PATIENT,
            code: 'BODY_WEIGHT',
            effective_at: new Date(clock).toISOString(),
            source: 'STATION',
            value: 999,
            unit: 'kg',
          },
          ...over,
        },
        { now: () => clock, newId: nextId },
      );
      clock += 1_000;
      return issued.eventId;
    },
    sync: async () => syncOnce(state.deps),
    rows: async () => readOutbox(store),
    row: async (eventId: string) =>
      (await readOutbox(store)).find((one) => one.eventId === eventId) ?? null,
  };
  return state;
}

function metricsOf(over: Partial<SyncMetrics> = {}): SyncMetrics {
  const counts = {
    queued: 0,
    inFlight: 0,
    needsAttention: 0,
    escalated: 0,
    held: 0,
    blocked: 0,
    awaitingTriage: 0,
    total: 0,
  };
  const base: SyncMetrics = {
    ...counts,
    undelivered: 0,
    delivering: false,
    lastSuccessAt: null,
    lastAttemptAt: null,
    noRoomAt: null,
    cursor: 0,
    skew: { level: 'fine', ms: 0, minutes: 0, ahead: false, entriesWouldBeHeld: false },
    halted: '',
  };
  const merged = { ...base, ...over };
  // `undelivered` is the counted total in the real reader, so a fixture that let a caller set a
  // count without it would be testing a device that cannot exist.
  const named =
    merged.queued +
    merged.inFlight +
    merged.needsAttention +
    merged.escalated +
    merged.held +
    merged.blocked +
    merged.awaitingTriage;
  return { ...merged, total: over.total ?? named, undelivered: over.undelivered ?? named };
}

function rowOf(over: Partial<OutboxRow> = {}): OutboxRow {
  return {
    eventId: 'e-1',
    seq: 1,
    aggregateType: 'patient',
    aggregateId: PATIENT,
    patientId: PATIENT,
    visitId: null,
    eventType: 'OBSERVATION_RECORDED',
    eventVersion: 1,
    occurredAt: '2026-09-05T08:00:00.000Z',
    payload: JSON.stringify({ code: 'BODY_WEIGHT', value: 999, unit: 'kg' }),
    metadata: null,
    expectedSequence: null,
    state: 'NEEDS_ATTENTION',
    attempts: 1,
    nextAttemptAt: 0,
    batchId: null,
    reasonCode: REASON_CODES.invalidPayload,
    reason: 'value 999 out of range for BODY_WEIGHT',
    createdAt: START,
    ...over,
  };
}

type Tree = Record<string, unknown>;
function flatten(tree: Tree, prefix = ''): Map<string, string> {
  const out = new Map<string, string>();
  for (const [key, value] of Object.entries(tree)) {
    const path = prefix ? `${prefix}.${key}` : key;
    if (value !== null && typeof value === 'object') {
      for (const [k, v] of flatten(value as Tree, path)) out.set(k, v);
    } else {
      out.set(path, String(value));
    }
  }
  return out;
}
const english = flatten(en as Tree);
const bangla = flatten(bn as Tree);

// --- criterion 1 ---

describe('the indicator never says everything is with the clinic while anything is not', () => {
  it('reports every outbox state as undelivered, including one nobody has written a case for', () => {
    for (const state of OUTBOX_STATES) {
      // One row in each state in turn, counted the way `readCounts` counts: from the rows.
      const metrics = metricsOf({ total: 1, undelivered: 1 });
      expect(statusOf(metrics), `${state} must not read as synced`).not.toBe('synced');
    }
    // And the one that matters most: a state this build has never heard of, which is what a newer
    // engine writing a new state into an older screen's database looks like.
    expect(statusOf(metricsOf({ total: 1, undelivered: 1 }))).toBe('queued');
  });

  it('says everything is with the clinic only when the outbox is empty', () => {
    expect(statusOf(metricsOf())).toBe('synced');
    expect(pillFor(metricsOf(), { online: true }).key).toBe('pillSynced');
  });

  it('keeps an escalated entry counted as undelivered rather than resolved', () => {
    const metrics = metricsOf({ escalated: 3 });
    expect(metrics.undelivered).toBe(3);
    expect(statusOf(metrics)).toBe('escalated');
    expect(pillFor(metrics, { online: true }).key).toBe('pillEscalated');
  });

  it('lets a queue that is draining speak over entries somebody else already owns', () => {
    // 40 on their way and 1 escalated: the headline is the 40, because they are moving and the
    // escalation has been dealt with by the person holding the tablet. The total is still whole.
    const pill = pillFor(metricsOf({ queued: 40, escalated: 1 }), { online: true });
    expect(pill.key).toBe('pillQueued');
    expect(pill.count).toBe(40);
    expect(pill.undelivered).toBe(41);
  });

  it('says offline over a queue and still says the work is safe over an empty one', () => {
    expect(pillFor(metricsOf({ queued: 4 }), { online: false }).key).toBe('pillOffline');
    expect(pillFor(metricsOf(), { online: false }).key).toBe('pillOfflineClear');
  });

  it('does not let being offline quieten or reword a state that needs a person', () => {
    const attention = metricsOf({ needsAttention: 2 });
    expect(pillFor(attention, { online: false })).toEqual(pillFor(attention, { online: true }));
    const halted = metricsOf({ queued: 1, halted: 'DEVICE_REFUSED' });
    expect(pillFor(halted, { online: false })).toEqual(pillFor(halted, { online: true }));
  });

  it('is loud only for the two states a person standing here has to act on', () => {
    expect(toneOf('attention')).toBe('alert');
    expect(toneOf('halted')).toBe('alert');
    expect(toneOf('stalled')).toBe('notice');
    expect(toneOf('escalated')).toBe('notice');
    expect(toneOf('queued')).toBe('calm');
    expect(toneOf('syncing')).toBe('calm');
    expect(toneOf('synced')).toBe('calm');
  });
});

// --- criterion 2, and the security line ---

describe('a refused entry is explained in words the operator can act on', () => {
  it('names the measurement rather than the event type', () => {
    expect(entryKey(rowOf())).toBe('entryWeight');
    expect(entryKey(rowOf({ payload: JSON.stringify({ code: 'BP_SYSTOLIC', value: 120 }) }))).toBe(
      'entrySystolic',
    );
  });

  it('falls back to a vaguer noun for a code this build has never met, never to the code', () => {
    const row = rowOf({ payload: JSON.stringify({ code: 'SOMETHING_NEW', value: 1 }) });
    expect(entryKey(row)).toBe('entryMeasurement');
    expect(english.get(`sync.${entryKey(row)}`)).toBe('A measurement');
  });

  it('has a name for every measurement the two offline stations can actually send', () => {
    // The seam that keeps the hard-coded list honest. A station that adds a field would otherwise
    // leave this screen calling it "a measurement" with nothing failing.
    for (const field of [...VITAL_FIELDS, ...ANTHRO_FIELDS]) {
      const row = rowOf({ payload: JSON.stringify({ code: field.code, value: 1, unit: 'kg' }) });
      expect(entryKey(row), `${field.code} has a name of its own`).not.toBe('entryMeasurement');
    }
  });

  it('keeps the clinic’s own words away from an operator who could not have read them', () => {
    const row = rowOf();
    const assistant = reasonFor(row, { permissions: ['observation.write'] });
    expect(assistant.key).toBe('reasonInvalid');
    expect(assistant.prose).toBeNull();
    // The prose is a real one: it names a patient's weight. That is exactly what must not appear.
    expect(row.reason).toContain('999');
  });

  it('shows them to somebody the clinic would have shown the held event to', () => {
    const physician = reasonFor(rowOf(), { permissions: ['sync.quarantine.read'] });
    expect(physician.prose).toBe('value 999 out of range for BODY_WEIGHT');
  });

  it('treats no viewer at all as no permission', () => {
    expect(reasonFor(rowOf()).prose).toBeNull();
    expect(reasonFor(rowOf(), {}).prose).toBeNull();
  });

  it('never reaches for the prose to explain a code it does not recognise', () => {
    const row = rowOf({ reasonCode: 'SOMETHING_A_NEWER_SERVER_SAYS' });
    const view = reasonFor(row, { permissions: ['observation.write'] });
    expect(view.key).toBe('reasonRefused');
    expect(view.prose).toBeNull();
  });
});

describe('what a person is offered depends on what the refusal is', () => {
  it('offers a correction only where looking at the entry again could change the answer', () => {
    expect(actsFor(rowOf({ reasonCode: REASON_CODES.invalidPayload }))).toEqual([
      'correct',
      'escalate',
    ]);
    expect(actsFor(rowOf({ reasonCode: REASON_CODES.sequenceConflict }))).toEqual([
      'correct',
      'escalate',
    ]);
  });

  it('offers only escalation where no number the operator types can help', () => {
    expect(actsFor(rowOf({ reasonCode: REASON_CODES.unknownType }))).toEqual(['escalate']);
    expect(actsFor(rowOf({ reasonCode: REASON_CODES.deviceRevoked }))).toEqual(['escalate']);
    expect(actsFor(rowOf({ reasonCode: 'SOMETHING_A_NEWER_SERVER_SAYS' }))).toEqual(['escalate']);
  });

  it('offers no correction for an entry whose payload has no measurement to restate', () => {
    const row = rowOf({
      eventType: 'ALLERGY_RECORDED',
      payload: JSON.stringify({ allergy_id: 'a-1', substance: 'penicillin' }),
    });
    expect(actsFor(row)).toEqual(['escalate']);
    expect(entryKey(row)).toBe('entryAllergy');
  });

  it('stops offering to escalate an entry that has already been escalated', () => {
    const row = rowOf({ state: 'ESCALATED', reasonCode: REASON_CODES.unknownType });
    expect(actsFor(row)).toEqual([]);
    // Correcting one still is, so somebody who later works out what was wrong is not made to undo
    // the escalation first.
    expect(actsFor(rowOf({ state: 'ESCALATED', reasonCode: REASON_CODES.invalidPayload }))).toEqual(
      ['correct'],
    );
  });

  it('asks nothing of the operator about an entry the clinic is already holding', () => {
    expect(actsFor(rowOf({ state: 'HELD', reasonCode: REASON_CODES.clockImplausible }))).toEqual([
      'wait',
    ]);
    expect(ACTS).toContain('wait');
  });

  it('sorts the list by the order the operator worked in, oldest first', () => {
    const items = itemsOf([
      rowOf({ eventId: 'c', seq: 3 }),
      rowOf({ eventId: 'a', seq: 1 }),
      rowOf({ eventId: 'b', seq: 2 }),
    ]);
    expect(items.needsYou.map((one) => one.eventId)).toEqual(['a', 'b', 'c']);
  });

  it('separates the entries somebody here owns from the ones the clinic owns', () => {
    const items = itemsOf([
      rowOf({ eventId: 'a', seq: 1, state: 'NEEDS_ATTENTION' }),
      rowOf({ eventId: 'b', seq: 2, state: 'ESCALATED' }),
      rowOf({ eventId: 'c', seq: 3, state: 'HELD' }),
      rowOf({ eventId: 'd', seq: 4, state: 'PENDING', reasonCode: null, reason: null }),
    ]);
    expect(items.needsYou.map((one) => one.eventId)).toEqual(['a']);
    expect(items.escalated.map((one) => one.eventId)).toEqual(['b']);
    expect(items.atClinic.map((one) => one.eventId)).toEqual(['c']);
    expect(items.onItsWay.map((one) => one.eventId)).toEqual(['d']);
  });
});

// --- criterion 3: the two acts ---

describe('correcting a measurement the clinic refused', () => {
  it('sends a new event with a new id, and the clinic ends up with one weight, not two', async () => {
    const h = await harness({ rejectAll: true });
    const refusedId = await h.record();
    await h.sync();
    expect((await h.row(refusedId))?.state).toBe('NEEDS_ATTENTION');

    // A physician has looked at the chart: the weight was 79, not 999.
    h.clinic.rejectWhen = null;
    h.advance(60_000);
    const result = await resubmit(
      h.store,
      refusedId,
      { value: 79, unit: 'kg' },
      { newId: nextId, now: h.now },
    );
    expect(result.outcome).toBe('resubmitted');
    expect(result.replacementEventId).not.toBe(refusedId);

    await h.sync();
    const landed = [...h.clinic.ledger.values()];
    expect(landed).toHaveLength(1);
    expect(landed[0]?.event_id).toBe(result.replacementEventId);
    expect(landed[0]?.payload.value).toBe(79);
    // The moment the measurement was taken is untouched; only the envelope moved.
    expect(landed[0]?.payload.effective_at).toBe(new Date(START).toISOString());
    expect(landed[0]?.occurred_at).not.toBe(new Date(START).toISOString());

    expect(await integrityProblems(h.store, h.clinic, { quiescent: true })).toEqual([]);
  });

  it('empties the queue, so the operator is told the truth at the end of it', async () => {
    const h = await harness({ rejectAll: true });
    const refusedId = await h.record();
    await h.sync();
    expect(statusOf(await readMetrics(h.store))).toBe('attention');

    h.clinic.rejectWhen = null;
    h.advance(60_000);
    await resubmit(h.store, refusedId, { value: 79, unit: 'kg' }, { newId: nextId, now: h.now });
    await h.sync();

    const metrics = await readMetrics(h.store);
    expect(metrics.undelivered).toBe(0);
    expect(statusOf(metrics)).toBe('synced');
  });

  it('puts the corrected value on the screen rather than leaving the refused one there', async () => {
    const h = await harness({ rejectAll: true });
    const refusedId = await h.record();
    await h.sync();
    expect(
      (await readProjection(h.store, 'observation', `${PATIENT}/BODY_WEIGHT`))?.document.value,
    ).toBe(999);

    h.advance(60_000);
    await resubmit(h.store, refusedId, { value: 79, unit: 'kg' }, { newId: nextId, now: h.now });

    const projection = await readProjection(h.store, 'observation', `${PATIENT}/BODY_WEIGHT`);
    expect(projection?.document.value).toBe(79);
    // And it still says when the measurement was taken, not when it was corrected.
    expect(projection?.document.effective_at).toBe(new Date(START).toISOString());
  });

  it('carries the refused entry’s id with the correction so the record can join them up', () => {
    const command = correctionFor(
      rowOf(),
      { value: 79, unit: 'kg' },
      {
        newId: () => 'new-1',
        now: () => START + 60_000,
      },
    );
    expect(command.eventId).toBe('new-1');
    expect(command.metadata?.corrects_event_id).toBe('e-1');
    expect(command.metadata?.corrects_reason_code).toBe(REASON_CODES.invalidPayload);
    expect(command.payload.value).toBe(79);
    expect(command.occurredAt).toBe(new Date(START + 60_000).toISOString());
    // Never inherited: a correction that declared a dependency would come back BLOCKED behind the
    // very entry it replaces.
    expect(command.expectedSequence).toBeNull();
  });

  it('keeps the refused event on the device rather than erasing what was refused', async () => {
    const h = await harness({ rejectAll: true });
    const refusedId = await h.record();
    await h.sync();
    h.advance(60_000);
    await resubmit(h.store, refusedId, { value: 79, unit: 'kg' }, { newId: nextId, now: h.now });

    const events = await h.store.all({ table: 'local_events' });
    const refused = events.find((row) => String(row.event_id) === refusedId);
    expect(refused, 'the refused measurement is still on the device').toBeDefined();
    expect(refused?.global_seq, 'and is not claimed to be in the record').toBeNull();
    const log = await readSyncLog(h.store);
    expect(log.some((row) => row.phase === 'attention' && row.outcome === 'CORRECTED')).toBe(true);
  });

  it('refuses to correct an entry somebody has already dealt with on this tablet', async () => {
    const h = await harness({ rejectAll: true });
    const refusedId = await h.record();
    await h.sync();
    h.advance(60_000);
    await resubmit(h.store, refusedId, { value: 79, unit: 'kg' }, { newId: nextId, now: h.now });

    const again = await resubmit(
      h.store,
      refusedId,
      { value: 81, unit: 'kg' },
      { newId: nextId, now: h.now },
    );
    expect(again.outcome).toBe('gone');
    // The important half: the second press did not write a second measurement.
    expect((await h.rows()).filter((row) => row.state === 'PENDING')).toHaveLength(1);
  });

  it('will not let the sync screen correct an entry the clinic is holding', async () => {
    const h = await harness();
    const heldId = await h.record();
    h.clinic.deviceStatus = 'revoked';
    await h.sync();
    expect((await h.row(heldId))?.state).toBe('HELD');

    const result = await resubmit(
      h.store,
      heldId,
      { value: 79, unit: 'kg' },
      { newId: nextId, now: h.now },
    );
    expect(result.outcome).toBe('not-yours');
    expect((await h.row(heldId))?.state).toBe('HELD');
  });
});

describe('escalating a refusal nobody at this station can answer', () => {
  it('records it, stops asking the operator, and still counts the entry as undelivered', async () => {
    const h = await harness();
    h.clinic.rejectWhen = () => ({ code: REASON_CODES.unknownType, reason: 'unknown type' });
    const refusedId = await h.record();
    await h.sync();
    expect(statusOf(await readMetrics(h.store))).toBe('attention');

    expect(await escalate(h.store, refusedId)).toBe('escalated');

    const metrics = await readMetrics(h.store);
    expect(metrics.needsAttention).toBe(0);
    expect(metrics.escalated).toBe(1);
    expect(metrics.undelivered).toBe(1);
    expect(statusOf(metrics)).toBe('escalated');
    expect(toneOf(statusOf(metrics))).toBe('notice');
  });

  it('never sends it again, however many attempts follow', async () => {
    const h = await harness();
    h.clinic.rejectWhen = () => ({ code: REASON_CODES.unknownType, reason: 'unknown type' });
    const refusedId = await h.record();
    await h.sync();
    await escalate(h.store, refusedId);

    const pushesBefore = h.clinic.pushes.length;
    for (let i = 0; i < 5; i += 1) {
      h.advance(10 * 60_000);
      await h.sync();
    }
    expect(h.clinic.pushes.length).toBe(pushesBefore);
    expect((await h.row(refusedId))?.state).toBe('ESCALATED');
  });

  it('keeps the entry byte for byte what the clinic refused', async () => {
    const h = await harness();
    h.clinic.rejectWhen = () => ({ code: REASON_CODES.unknownType, reason: 'unknown type' });
    const refusedId = await h.record();
    await h.sync();
    const before = await h.row(refusedId);
    await escalate(h.store, refusedId);
    const after = await h.row(refusedId);
    expect({ ...after, state: before?.state }).toEqual(before);
  });

  it('gives a reference an operator can read out to a supervisor', () => {
    expect(referenceOf('9f8e7d6c-1234-4abc-9def-000000000000')).toBe('9F8E-7D6C');
    expect(referenceOf('short')).toBe('SHORT');
  });

  it('is not offered for an entry that is already at the clinic', async () => {
    const h = await harness();
    const heldId = await h.record();
    h.clinic.deviceStatus = 'revoked';
    await h.sync();
    expect(await escalate(h.store, heldId)).toBe('not-yours');
    expect((await h.row(heldId))?.state).toBe('HELD');
  });

  it('answers a second press without changing anything', async () => {
    const h = await harness();
    h.clinic.rejectWhen = () => ({ code: REASON_CODES.unknownType, reason: 'unknown type' });
    const refusedId = await h.record();
    await h.sync();
    await escalate(h.store, refusedId);
    expect(await escalate(h.store, refusedId)).toBe('already');
    expect(await escalate(h.store, 'no-such-event')).toBe('gone');
  });

  it('can still be corrected afterwards, if somebody works out what was wrong', async () => {
    const h = await harness({ rejectAll: true });
    const refusedId = await h.record();
    await h.sync();
    await escalate(h.store, refusedId);

    h.clinic.rejectWhen = null;
    h.advance(60_000);
    const result = await resubmit(
      h.store,
      refusedId,
      { value: 79, unit: 'kg' },
      { newId: nextId, now: h.now },
    );
    expect(result.outcome).toBe('resubmitted');
    await h.sync();
    expect((await readMetrics(h.store)).undelivered).toBe(0);
  });
});

describe('an escalated entry is held back from every batch the engine builds', () => {
  it('is not selected, and does not hold an independent later entry behind it', () => {
    const escalated = rowOf({ eventId: 'a', seq: 1, state: 'ESCALATED', nextAttemptAt: 0 });
    const later = rowOf({ eventId: 'b', seq: 2, state: 'PENDING', nextAttemptAt: 0 });
    const batch = selectBatch([escalated, later], { now: START + 60_000 });
    expect(batch.map((row) => row.eventId)).toEqual(['b']);
  });

  it('does hold back a later entry that declared it depends on the same record', () => {
    const escalated = rowOf({ eventId: 'a', seq: 1, state: 'ESCALATED', nextAttemptAt: 0 });
    const dependent = rowOf({
      eventId: 'b',
      seq: 2,
      state: 'PENDING',
      nextAttemptAt: 0,
      expectedSequence: 4,
    });
    expect(selectBatch([escalated, dependent], { now: START + 60_000 })).toEqual([]);
  });
});

// --- criterion 4: a non-technical person can read every state ---

describe('every state a person can be shown has words in both languages', () => {
  const pillKeys = new Set<string>();
  for (const online of [true, false]) {
    for (const over of [
      {},
      { queued: 3 },
      { inFlight: 2 },
      { needsAttention: 1 },
      { escalated: 1 },
      { held: 1 },
      { awaitingTriage: 1 },
      { queued: 1, halted: 'DEVICE_REFUSED' as const },
    ]) {
      pillKeys.add(pillFor(metricsOf(over), { online }).key);
    }
  }

  it('has a sentence for every pill the status logic can produce', () => {
    expect(pillKeys.size).toBeGreaterThan(6);
    for (const key of pillKeys) {
      expect(english.has(`sync.${key}`), `${key} in English`).toBe(true);
      expect(bangla.has(`sync.${key}`), `${key} in Bangla`).toBe(true);
    }
  });

  it('uses the same ICU arguments on both sides of every pill sentence', () => {
    const args = (text: string) => [...text.matchAll(/\{(\w+)\s*[},]/g)].map((m) => m[1]).sort();
    for (const key of pillKeys) {
      expect(args(bangla.get(`sync.${key}`) ?? ''), key).toEqual(
        args(english.get(`sync.${key}`) ?? ''),
      );
    }
  });

  it('has a line for every state an entry can be listed in', () => {
    for (const state of OUTBOX_STATES) {
      const item = itemsOf([rowOf({ state })]);
      const all = [...item.needsYou, ...item.escalated, ...item.atClinic, ...item.onItsWay];
      const key = all[0]?.statusKey ?? '';
      expect(english.has(`sync.${key}`), `${state} → ${key} in English`).toBe(true);
      expect(bangla.has(`sync.${key}`), `${state} → ${key} in Bangla`).toBe(true);
    }
  });

  it('has a name in both languages for every kind of entry the list can show', () => {
    const keys = new Set<string>(Object.values(entryNames()));
    for (const key of keys) {
      expect(english.has(`sync.${key}`), `${key} in English`).toBe(true);
      expect(bangla.has(`sync.${key}`), `${key} in Bangla`).toBe(true);
    }
  });

  it('says what still works offline, in both languages, on every line', () => {
    expect(OFFLINE_FACTS.length).toBeGreaterThan(3);
    for (const fact of OFFLINE_FACTS) {
      expect(english.has(`connection.${fact}`), `${fact} in English`).toBe(true);
      expect(bangla.has(`connection.${fact}`), `${fact} in Bangla`).toBe(true);
    }
  });

  it('never puts a technical token in front of an operator', () => {
    // The list's own vocabulary, checked against the thing it is a translation of: no sentence an
    // operator reads may contain an event type, an outbox state or a reason code.
    const tokens = [...OUTBOX_STATES, ...Object.values(REASON_CODES), 'OBSERVATION_RECORDED'];
    for (const [key, value] of english) {
      if (!key.startsWith('sync.')) continue;
      for (const token of tokens) {
        expect(value.includes(token), `${key} contains ${token}`).toBe(false);
      }
    }
  });
});

function entryNames(): Record<string, string> {
  const rows = [
    ...[...VITAL_FIELDS, ...ANTHRO_FIELDS].map((field) =>
      rowOf({ payload: JSON.stringify({ code: field.code, value: 1, unit: 'kg' }) }),
    ),
    rowOf({ payload: JSON.stringify({ code: 'SOMETHING_NEW' }) }),
    rowOf({ eventType: 'ALLERGY_RECORDED', payload: '{}' }),
    rowOf({ eventType: 'COUNSELING_ITEM_TICKED', payload: '{}' }),
    rowOf({ eventType: 'QUEUE_CALLED', payload: '{}' }),
    rowOf({ eventType: 'PATIENT_REGISTERED', payload: '{}' }),
    rowOf({ eventType: 'VISIT_OPENED', payload: '{}' }),
    rowOf({ eventType: 'SOMETHING_ELSE', payload: 'not json' }),
  ];
  const out: Record<string, string> = {};
  for (const row of rows) out[`${row.eventType}:${row.payload}`] = entryKey(row);
  return out;
}

// --- the counts themselves ---

describe('the counted total is what undelivered means', () => {
  it('counts a row in a state this build has no case for', async () => {
    const h = await harness();
    const id = await h.record();
    await h.store.run({
      kind: 'update',
      table: 'outbox',
      set: { state: 'SOMETHING_A_NEWER_BUILD_WRITES' },
      where: [{ column: 'event_id', op: '=', value: id }],
    });
    const counts = await readCounts(h.store);
    expect(counts.total).toBe(1);
    expect(
      counts.queued +
        counts.inFlight +
        counts.blocked +
        counts.awaitingTriage +
        counts.needsAttention +
        counts.escalated +
        counts.held,
      'the named states do not add up to it, which is the point',
    ).toBe(0);
    expect(statusOf(await readMetrics(h.store))).not.toBe('synced');
  });
});
