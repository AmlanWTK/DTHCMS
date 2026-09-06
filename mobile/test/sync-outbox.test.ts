import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

/**
 * The local write path (CP64).
 *
 * Everything here runs against the in-memory driver, with the network stubbed to throw. That is
 * not a convenience: acceptance criterion 3 is *"the UI reads exclusively from local state, proven
 * by working fully in airplane mode"*, and the way to prove it is to make using the network a test
 * failure rather than a slow test.
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

const { LocalStoreFullError, MIGRATIONS, TABLES, createDisk, createMemoryStore } =
  await import('../src/lib/local-store');
// From the modules rather than the feature index: the index re-exports the screen, and these
// tests run in plain Node without a JSX transform. Same arrangement as every other station here.
const { issueCommand } = await import('../src/lib/sync/commands');
const {
  CACHE_TTL_DAYS,
  DESTROYS_UNDELIVERED_WORK,
  lifecycleFor,
  purgeExpired,
  wipeAfterSignOut,
  wipeEverything,
} = await import('../src/lib/sync/lifecycle');
const {
  applyProjections,
  eventsForPatient,
  readCounts,
  readMetrics,
  readOutbox,
  readProjection,
  readProjections,
} = await import('../src/lib/sync/outbox');
const { statusOf } = await import('../src/lib/sync/state');
const { projectionsFor } = await import('../src/lib/sync/projections');

const KEY = '00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff';
const PATIENT = 'patient-1';
const VISIT = 'visit-1';

type Store = Awaited<ReturnType<typeof openDevice>>['store'];

async function openDevice(disk = createDisk()) {
  const store = createMemoryStore({ disk });
  await store.open({ key: KEY });
  await store.migrate(MIGRATIONS);
  return { store, disk };
}

/** A weight, as station 2 records one. */
function weight(over: Partial<Parameters<typeof issueCommand>[1]> = {}) {
  return {
    aggregateType: 'patient',
    aggregateId: PATIENT,
    patientId: PATIENT,
    visitId: VISIT,
    eventType: 'WEIGHT_RECORDED',
    eventVersion: 1,
    occurredAt: '2026-09-05T08:40:00.000Z',
    payload: { code: 'BODY_WEIGHT', value: 71.5, unit: 'kg' },
    ...over,
  };
}

let ids = 0;
const nextId = () => `event-${(ids += 1)}`;
const at = () => 1_757_000_000_000;

const realFetch = globalThis.fetch;

beforeEach(() => {
  ids = 0;
  keystore.clear();
  // Airplane mode, enforced. Anything that reaches for the network fails the test it is in.
  globalThis.fetch = (() => {
    throw new Error('a station screen must not use the network to read or write');
  }) as typeof globalThis.fetch;
});

afterEach(() => {
  globalThis.fetch = realFetch;
});

describe('one command, one transaction', () => {
  it('writes the event, the projection and the queue together', async () => {
    const { store } = await openDevice();
    const issued = await issueCommand(store, weight(), { now: at, newId: nextId });

    expect(issued).toEqual({ eventId: 'event-1', seq: 1, duplicate: false });

    const events = await store.all({ table: TABLES.localEvents });
    expect(events).toHaveLength(1);
    expect(events[0]?.origin).toBe('LOCAL');
    // Not yet the clinic's: no sequence, no arrival time. This is what a pending indicator means.
    expect(events[0]?.global_seq ?? null).toBeNull();

    const queued = await readOutbox(store);
    expect(queued).toHaveLength(1);
    expect(queued[0]?.state).toBe('PENDING');
    expect(queued[0]?.eventId).toBe('event-1');
    expect(queued[0]?.occurredAt).toBe('2026-09-05T08:40:00.000Z');

    const projection = await readProjection(store, 'observation', `${PATIENT}/BODY_WEIGHT`);
    expect(projection?.document.value).toBe(71.5);
    expect(projection?.confirmed).toBe(false);
  });

  it('writes nothing at all when the device has no room', async () => {
    // §13.10's storage-full case at the only place it can be survived: the operator is told
    // "not saved", and the record contains no trace of a measurement nobody has.
    const { store } = await openDevice();
    store.faults.full = true;

    await expect(issueCommand(store, weight(), { now: at, newId: nextId })).rejects.toBeInstanceOf(
      LocalStoreFullError,
    );

    store.faults.full = false;
    expect(await store.all({ table: TABLES.localEvents })).toEqual([]);
    expect(await readOutbox(store)).toEqual([]);
    expect(await store.all({ table: TABLES.projections })).toEqual([]);
    // And the sequence was not consumed, so the next entry is still the first.
    const issued = await issueCommand(store, weight(), { now: at, newId: nextId });
    expect(issued.seq).toBe(1);
  });

  it('is idempotent when a screen re-issues the same command', async () => {
    // The retry after "not saved". The same id must produce one event, one row, one measurement.
    const { store } = await openDevice();
    const first = await issueCommand(store, weight(), { now: at, newId: nextId });
    const again = await issueCommand(
      store,
      { ...weight(), eventId: first.eventId },
      { now: at, newId: nextId },
    );

    expect(again).toEqual({ eventId: 'event-1', seq: 1, duplicate: true });
    expect(await store.all({ table: TABLES.localEvents })).toHaveLength(1);
    expect(await readOutbox(store)).toHaveLength(1);
  });

  it('gives every fresh command its own id and keeps them in the order they were made', async () => {
    const { store } = await openDevice();
    await issueCommand(store, weight(), { now: at, newId: nextId });
    await issueCommand(
      store,
      weight({ payload: { code: 'BODY_HEIGHT', value: 160, unit: 'cm' } }),
      {
        now: at,
        newId: nextId,
      },
    );
    const queue = await readOutbox(store);
    expect(queue.map((row) => row.eventId)).toEqual(['event-1', 'event-2']);
    expect(queue.map((row) => row.seq)).toEqual([1, 2]);
  });

  it('notes a clock that is known to be wrong beside the entry, and does not correct it', async () => {
    // The measured skew is a note for whoever has to decide about a held event. The timestamp
    // itself is what the operator's device said, because rewriting it would be this client
    // asserting a clinical fact it does not know.
    const { store } = await openDevice();
    await store.run({
      kind: 'insert',
      table: TABLES.syncMeta,
      row: { key: 'clock_skew_ms', value: String(3 * 60 * 60 * 1000) },
    });
    await issueCommand(store, weight(), { now: at, newId: nextId });
    const row = (await readOutbox(store))[0];
    expect(row?.occurredAt).toBe('2026-09-05T08:40:00.000Z');
    expect(JSON.parse(row?.metadata ?? '{}')).toEqual({ device_clock_skew_ms: 10_800_000 });
  });
});

describe('what the screens read', () => {
  it('answers from the local record with no network at all', async () => {
    const { store } = await openDevice();
    await issueCommand(store, weight(), { now: at, newId: nextId });
    await issueCommand(
      store,
      weight({
        eventType: 'BP_RECORDED',
        payload: { code: 'BP_SYSTOLIC', value: 128, unit: 'mmHg' },
      }),
      { now: at, newId: nextId },
    );

    const forPatient = await readProjections(store, PATIENT);
    expect(forPatient.map((row) => row.key).sort()).toEqual([
      `${PATIENT}/BODY_WEIGHT`,
      `${PATIENT}/BP_SYSTOLIC`,
    ]);
    // The stub would have thrown; reaching here is the assertion.
    expect(forPatient.every((row) => row.confirmed === false)).toBe(true);
  });

  it('keeps every value in the log, not only the current one', async () => {
    // §13.6: both values remain visible. The projection holds the current one; the log holds what
    // was recorded, and neither answers the other's question.
    const { store } = await openDevice();
    await issueCommand(store, weight({ payload: { code: 'BODY_WEIGHT', value: 71.5 } }), {
      now: at,
      newId: nextId,
    });
    await issueCommand(
      store,
      weight({
        occurredAt: '2026-09-05T09:10:00.000Z',
        payload: { code: 'BODY_WEIGHT', value: 71.9 },
      }),
      { now: at, newId: nextId },
    );

    const current = await readProjection(store, 'observation', `${PATIENT}/BODY_WEIGHT`);
    expect(current?.document.value).toBe(71.9);
    const history = await eventsForPatient(store, PATIENT);
    expect(history.map((row) => JSON.parse(String(row.payload)).value)).toEqual([71.5, 71.9]);
  });

  it('does not let an earlier measurement arriving late overwrite a later one', async () => {
    // The other station's entry, synced afterwards. §13.6 is one comparison and this is it.
    const { store } = await openDevice();
    await issueCommand(
      store,
      weight({
        occurredAt: '2026-09-05T09:10:00.000Z',
        payload: { code: 'BODY_WEIGHT', value: 71.9 },
      }),
      { now: at, newId: nextId },
    );
    await issueCommand(
      store,
      weight({
        occurredAt: '2026-09-05T08:40:00.000Z',
        payload: { code: 'BODY_WEIGHT', value: 71.5 },
      }),
      { now: at, newId: nextId },
    );
    const current = await readProjection(store, 'observation', `${PATIENT}/BODY_WEIGHT`);
    expect(current?.document.value).toBe(71.9);
  });

  it('settles two devices on the same answer when the moment is identical', async () => {
    // Without a tie-break, two tablets applying the same pair in different orders would show
    // different current values for ever, with no error anywhere.
    const one = await openDevice();
    const other = await openDevice();
    const events = [
      { id: 'aaa', value: 70 },
      { id: 'bbb', value: 72 },
    ];
    for (const event of events) {
      await applyProjections(
        one.store,
        {
          eventId: event.id,
          aggregateType: 'patient',
          aggregateId: PATIENT,
          patientId: PATIENT,
          visitId: VISIT,
          eventType: 'WEIGHT_RECORDED',
          eventVersion: 1,
          occurredAt: '2026-09-05T08:40:00.000Z',
          recordedAt: null,
          payload: { code: 'BODY_WEIGHT', value: event.value },
          globalSeq: null,
        },
        { confirmed: true, at: at() },
      );
    }
    for (const event of [...events].reverse()) {
      await applyProjections(
        other.store,
        {
          eventId: event.id,
          aggregateType: 'patient',
          aggregateId: PATIENT,
          patientId: PATIENT,
          visitId: VISIT,
          eventType: 'WEIGHT_RECORDED',
          eventVersion: 1,
          occurredAt: '2026-09-05T08:40:00.000Z',
          recordedAt: null,
          payload: { code: 'BODY_WEIGHT', value: event.value },
          globalSeq: null,
        },
        { confirmed: true, at: at() },
      );
    }
    const first = await readProjection(one.store, 'observation', `${PATIENT}/BODY_WEIGHT`);
    const second = await readProjection(other.store, 'observation', `${PATIENT}/BODY_WEIGHT`);
    expect(first?.eventId).toBe(second?.eventId);
    expect(first?.document.value).toBe(second?.document.value);
  });

  it('keeps an event whose type this build cannot draw', async () => {
    // An older tablet talking to a newer clinic. The event is recorded so a later build can draw
    // it; what it must never be is discarded.
    const { store } = await openDevice();
    await issueCommand(store, weight({ eventType: 'SOMETHING_ADDED_NEXT_MONTH' }), {
      now: at,
      newId: nextId,
    });
    expect(await store.all({ table: TABLES.localEvents })).toHaveLength(1);
    expect(await store.all({ table: TABLES.projections })).toEqual([]);
  });
});

describe('what each kind of event draws', () => {
  const event = (over: Record<string, unknown>) => ({
    eventId: 'e1',
    aggregateType: 'patient',
    aggregateId: PATIENT,
    patientId: PATIENT,
    visitId: VISIT,
    eventType: 'PATIENT_REGISTERED',
    eventVersion: 1,
    occurredAt: '2026-09-05T08:00:00.000Z',
    recordedAt: null,
    payload: {},
    globalSeq: null,
    ...over,
  });

  it('puts a name on the screen', () => {
    const [row] = projectionsFor(event({ payload: { name_en: 'Rahima Begum' } }));
    expect(row?.kind).toBe('patient');
    expect(row?.key).toBe(PATIENT);
    expect(row?.document.name_en).toBe('Rahima Begum');
  });

  it('says whether the visit is still open', () => {
    const [open] = projectionsFor(event({ eventType: 'VISIT_OPENED' }));
    expect(open?.kind).toBe('visit');
    expect(open?.document.status).toBe('open');
    const [closed] = projectionsFor(event({ eventType: 'VISIT_CLOSED' }));
    expect(closed?.document.status).toBe('closed');
    const [abandoned] = projectionsFor(event({ eventType: 'VISIT_ABANDONED' }));
    expect(abandoned?.document.status).toBe('abandoned');
  });

  it('keys an allergy by its own id, so a withdrawal lands on the row it withdraws', () => {
    const [recorded] = projectionsFor(
      event({
        eventType: 'ALLERGY_RECORDED',
        payload: { allergy_id: 'a1', substance: 'penicillin' },
      }),
    );
    expect(recorded?.key).toBe(`${PATIENT}/a1`);
    expect(recorded?.document.withdrawn).toBe(0);
    const [withdrawn] = projectionsFor(
      event({ eventType: 'ALLERGY_WITHDRAWN', payload: { allergy_id: 'a1' } }),
    );
    expect(withdrawn?.key).toBe(`${PATIENT}/a1`);
    expect(withdrawn?.document.withdrawn).toBe(1);
  });

  it('ticks and unticks one counselling item', () => {
    const [ticked] = projectionsFor(
      event({ eventType: 'COUNSELING_ITEM_TICKED', payload: { item_code: 'DIET_SALT' } }),
    );
    expect(ticked?.kind).toBe('counseling');
    expect(ticked?.document.ticked).toBe(1);
    const [unticked] = projectionsFor(
      event({ eventType: 'COUNSELING_ITEM_UNTICKED', payload: { item_code: 'DIET_SALT' } }),
    );
    expect(unticked?.document.ticked).toBe(0);
    // An item with no code is not a tick on anything, and must not become a row keyed on nothing.
    expect(projectionsFor(event({ eventType: 'COUNSELING_ITEM_TICKED', payload: {} }))).toEqual([]);
  });

  it('carries a critical value and whether somebody has acknowledged it', () => {
    const [raised] = projectionsFor(
      event({
        eventType: 'CRITICAL_VALUE_ALERTED',
        aggregateId: 'alert-1',
        payload: { code: 'GLUCOSE' },
      }),
    );
    expect(raised?.kind).toBe('alert');
    expect(raised?.document.acknowledged).toBe(0);
    const [acknowledged] = projectionsFor(
      event({ eventType: 'CRITICAL_VALUE_ACKNOWLEDGED', aggregateId: 'alert-1', payload: {} }),
    );
    expect(acknowledged?.document.acknowledged).toBe(1);
  });

  it('draws nothing for an observation with no patient on it', () => {
    // Keyed by patient and code; without a patient there is no key, and a row keyed on `null`
    // would collect every patient's weight in one place.
    expect(projectionsFor(event({ eventType: 'WEIGHT_RECORDED', patientId: null }))).toEqual([]);
    expect(projectionsFor(event({ eventType: 'ALLERGY_RECORDED', patientId: null }))).toEqual([]);
  });
});

describe('counting what has not reached the clinic', () => {
  it('counts a row in a state nobody has labelled yet as undelivered anyway', async () => {
    // The one number §13.9 says must never be wrong, and the direction that matters is a count
    // that is too small: a screen saying everything is with the clinic over a row that is not.
    // `undelivered` is therefore the number of rows, not the sum of the labelled states — an
    // outbox state added and forgotten in one of the four places that used to add them up would
    // have silently gone missing from all four.
    const { store } = await openDevice();
    await issueCommand(store, weight(), { now: at, newId: nextId });
    await issueCommand(store, weight({ eventId: 'stuck' }), { now: at, newId: nextId });
    await store.run({
      kind: 'update',
      table: TABLES.outbox,
      set: { state: 'AWAITING_TRIAGE', reason_code: 'QUARANTINE_FULL', next_attempt_at: 1 },
      where: [{ column: 'event_id', op: '=', value: 'stuck' }],
    });

    const counts = await readCounts(store);
    expect(counts.queued).toBe(1);
    expect(counts.awaitingTriage).toBe(1);
    expect(counts.total).toBe(2);

    const metrics = await readMetrics(store);
    expect(metrics.undelivered).toBe(2);
    expect(statusOf(metrics)).toBe('stalled');
  });

  it('tells whoever is about to wipe the tablet about them too', async () => {
    // `wipeEverything` is the only function here that can lose a measurement, and the count it
    // returns is what the person pressing it is shown. An entry the clinic had no room for is
    // exactly as lost as any other.
    const { store } = await openDevice();
    await issueCommand(store, weight({ eventId: 'stuck' }), { now: at, newId: nextId });
    await store.run({
      kind: 'update',
      table: TABLES.outbox,
      set: { state: 'AWAITING_TRIAGE' },
      where: [{ column: 'event_id', op: '=', value: 'stuck' }],
    });
    expect((await wipeEverything(store, DESTROYS_UNDELIVERED_WORK)).undelivered).toBe(1);
  });
});

describe('the queue survives the app being killed', () => {
  it('still holds everything after a relaunch', async () => {
    // §13.10: kill the app with a full queue and relaunch. The manual verification for this
    // checkpoint is the same test done by hand on a tablet.
    const { store, disk } = await openDevice();
    for (let index = 0; index < 20; index += 1) {
      await issueCommand(store, weight({ payload: { code: `CODE_${index}`, value: index } }), {
        now: at,
        newId: nextId,
      });
    }
    await store.close();

    const relaunched = createMemoryStore({ disk });
    await relaunched.open({ key: KEY });
    expect(await relaunched.migrate(MIGRATIONS)).toBe(0);
    const queue = await readOutbox(relaunched);
    expect(queue).toHaveLength(20);
    expect(queue.map((row) => row.seq)).toEqual([...Array(20).keys()].map((n) => n + 1));
    const metrics = await readMetrics(relaunched);
    expect(metrics.queued).toBe(20);
    expect(statusOf(metrics)).toBe('queued');
  });
});

describe('signing out', () => {
  it('leaves nothing readable and loses nothing undelivered', async () => {
    // §13.8 says wipe on logout; §15.2 says never lose a station entry. Both, or the security
    // control becomes the thing that destroys a morning's work.
    const { store } = await openDevice();
    await issueCommand(store, weight(), { now: at, newId: nextId });
    await store.run({
      kind: 'insert',
      table: TABLES.referenceCache,
      row: { catalogue: 'terminology', fingerprint: 'fp1', rows: 3, document: '[]', fetched_at: 1 },
    });

    const report = await wipeAfterSignOut(store);
    expect(report).toEqual({ undelivered: 1, keptQueue: true, keyForgotten: false });

    expect(await store.all({ table: TABLES.projections })).toEqual([]);
    expect(await store.all({ table: TABLES.localEvents })).toEqual([]);
    expect(await store.all({ table: TABLES.referenceCache })).toEqual([]);
    // The undelivered measurement is still there, and still complete enough to send.
    const queue = await readOutbox(store);
    expect(queue).toHaveLength(1);
    expect(JSON.parse(queue[0]?.payload ?? '{}')).toEqual({
      code: 'BODY_WEIGHT',
      value: 71.5,
      unit: 'kg',
    });
    // The cursor is reset, because the events it described have been deleted.
    const metrics = await readMetrics(store);
    expect(metrics.cursor).toBe(0);
  });

  it('still recognises a re-issued command after the readable record has been wiped', async () => {
    // The queue outlives the sign-out, so the duplicate check has to look at it: an event whose
    // log entry was wiped must not be enqueued a second time by a screen retrying a save.
    const { store } = await openDevice();
    const first = await issueCommand(store, weight(), { now: at, newId: nextId });
    await wipeAfterSignOut(store);
    const again = await issueCommand(
      store,
      { ...weight(), eventId: first.eventId },
      { now: at, newId: nextId },
    );
    expect(again.duplicate).toBe(true);
    expect(await readOutbox(store)).toHaveLength(1);
  });

  it('empties the tablet completely only when somebody asks, and says what it cost', async () => {
    const { store } = await openDevice();
    await issueCommand(store, weight(), { now: at, newId: nextId });
    await store.run({
      kind: 'insert',
      table: TABLES.syncMeta,
      row: { key: 'clock_skew_ms', value: '10' },
    });

    // The confirmation is a required argument spelled as a sentence, so that nothing reaches this
    // function by autocomplete or by copying the line above it.
    await expect(
      // @ts-expect-error — the point of the argument is that this does not compile.
      wipeEverything(store),
    ).rejects.toThrow(/must be confirmed/);

    const report = await wipeEverything(store, DESTROYS_UNDELIVERED_WORK);
    expect(report.undelivered).toBe(1);
    expect(report.keptQueue).toBe(false);
    expect(report.keyForgotten).toBe(true);
    expect(await readOutbox(store)).toEqual([]);
    expect(keystore.size).toBe(0);
  });

  it('releases the key on the way in and drops it on the way out', () => {
    expect(lifecycleFor('unknown', 'authenticated')).toBe('unlock');
    expect(lifecycleFor('anonymous', 'authenticated')).toBe('unlock');
    expect(lifecycleFor('authenticated', 'authenticated')).toBe('nothing');
    expect(lifecycleFor('authenticated', 'anonymous')).toBe('lock');
    // A cold start that has not decided yet must not wipe anything.
    expect(lifecycleFor('unknown', 'anonymous')).toBe('nothing');
    expect(lifecycleFor('anonymous', 'unknown')).toBe('nothing');
  });
});

describe('the cache TTL', () => {
  it('purges old cached events and never anything undelivered', async () => {
    const { store }: { store: Store } = await openDevice();
    const now = Date.parse('2026-09-30T08:00:00.000Z');

    // An old event this device pulled, and an old one it recorded and has not sent.
    await store.run({
      kind: 'insert',
      table: TABLES.localEvents,
      row: {
        event_id: 'old-server',
        seq: 1,
        origin: 'SERVER',
        aggregate_type: 'patient',
        aggregate_id: PATIENT,
        patient_id: PATIENT,
        visit_id: null,
        event_type: 'WEIGHT_RECORDED',
        event_version: 1,
        occurred_at: '2026-09-01T08:00:00.000Z',
        recorded_at: '2026-09-01T08:00:00.000Z',
        payload: '{}',
        global_seq: 5,
        applied_at: 1,
      },
    });
    await issueCommand(
      store,
      weight({ eventId: 'old-mine', occurredAt: '2026-09-01T08:00:00.000Z' }),
      { now: at, newId: nextId },
    );

    const purged = await purgeExpired(store, { now, ttlDays: CACHE_TTL_DAYS });
    expect(purged).toBe(1);
    const kept = await store.all({ table: TABLES.localEvents });
    expect(kept.map((row) => row.event_id)).toEqual(['old-mine']);
    expect(await readCounts(store)).toMatchObject({ queued: 1 });
  });
});
