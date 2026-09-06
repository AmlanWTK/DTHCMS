import { describe, expect, it, vi } from 'vitest';

/**
 * Station 5 with the Wi-Fi off (CP64 criterion 3, CP66).
 *
 * The worked example, tested the way the other eleven stations will have to be: the network is
 * stubbed to throw, so anything that reaches for it fails the test rather than slowing it down.
 * What is asserted is the whole round trip — the queue and the patient read from the local
 * record, a set of readings recorded with no connection, and then, when the connection comes
 * back, the clinic holding exactly what the tablet holds.
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

const { MIGRATIONS, createDisk, createMemoryStore } = await import('../src/lib/local-store');
const { syncOnce } = await import('../src/lib/sync/engine');
const { readOutbox, readProjection } = await import('../src/lib/sync/outbox');
const { emptyReading, VITAL_FIELDS } = await import('../src/features/vitals/form');
const { previousVitalsLocally, recordVitals, vitalsCommands } =
  await import('../src/features/vitals/offline');
const { ageInYears, inServicePatient, readLocalPatient, readStationQueue } =
  await import('../src/features/queue/local');

import { FakeClinic } from './fake-sync-server';
import { integrityProblems } from './sync-integrity';

import type { Reading } from '../src/features/vitals/form';

const KEY = '0f0e0d0c0b0a09080706050403020100f0e0d0c0b0a090807060504030201000';
const FACILITY = '11111111-1111-4111-8111-111111111111';
const PATIENT = '22222222-2222-4222-8222-222222222222';
const VISIT = '33333333-3333-4333-8333-333333333333';
const STATION = 'STN_VITALS';
const NOW = Date.parse('2026-09-05T09:00:00.000Z');

const realFetch = globalThis.fetch;

function reading(over: Partial<Record<string, string>> = {}): Reading {
  const base = emptyReading();
  for (const field of VITAL_FIELDS) {
    const text = over[field.key];
    if (text !== undefined) base.values[field.key] = { ...base.values[field.key], text };
  }
  return base;
}

let ids = 0;
const nextId = () => `00000000-0000-4000-8000-${(ids += 1).toString().padStart(12, '0')}`;

function idsFor() {
  const perValue = new Map<string, string>();
  return {
    perValue: (index: number, code: string) => {
      const key = `${index}:${code}`;
      const existing = perValue.get(key);
      if (existing !== undefined) return existing;
      const id = nextId();
      perValue.set(key, id);
      return id;
    },
    takenAt: (index: number) => new Date(NOW + index * 60_000).toISOString(),
    newObservationId: nextId,
  };
}

async function device() {
  ids = 0;
  const disk = createDisk();
  const store = createMemoryStore({ disk });
  await store.open({ key: KEY });
  await store.migrate(MIGRATIONS);
  const clinic = new FakeClinic({ now: () => NOW });
  return { store, disk, clinic };
}

/** The clinic's morning, as this device would pull it. */
function seedClinic(clinic: FakeClinic) {
  clinic.append({
    event_id: 'p-1',
    aggregate_type: 'PATIENT',
    aggregate_id: PATIENT,
    patient_id: PATIENT,
    event_type: 'PATIENT_REGISTERED',
    event_version: 1,
    occurred_at: '2026-09-01T08:00:00.000Z',
    payload: {
      facility_id: FACILITY,
      patient_id: PATIENT,
      name_en: 'Rahima Begum',
      name_bn: 'রহিমা বেগম',
      sex: 'female',
      birth_date: '1979-03-14',
    },
  });
  clinic.append({
    event_id: 'q-1',
    aggregate_type: 'VISIT',
    aggregate_id: VISIT,
    patient_id: PATIENT,
    visit_id: VISIT,
    event_type: 'QUEUE_ENTERED',
    event_version: 1,
    occurred_at: '2026-09-05T08:40:00.000Z',
    payload: {
      facility_id: FACILITY,
      patient_id: PATIENT,
      visit_id: VISIT,
      entry_id: 'entry-1',
      station_code: STATION,
      position: 3,
      priority: 0,
    },
  });
  clinic.append({
    event_id: 'q-2',
    aggregate_type: 'VISIT',
    aggregate_id: VISIT,
    patient_id: PATIENT,
    visit_id: VISIT,
    event_type: 'QUEUE_CALLED',
    event_version: 1,
    occurred_at: '2026-09-05T08:55:00.000Z',
    payload: {
      facility_id: FACILITY,
      patient_id: PATIENT,
      visit_id: VISIT,
      entry_id: 'entry-1',
      station_code: STATION,
      waited_seconds: 900,
    },
  });
}

describe('what a set of readings becomes', () => {
  it('carries everything the ledger requires and nothing it assigns', () => {
    const commands = vitalsCommands(
      [reading({ systolic: '128', diastolic: '82', pulse: '76' })],
      idsFor(),
      { facilityId: FACILITY, patientId: PATIENT, visitId: VISIT },
    );

    // Three numbers plus the arm, position and cuff that describe the blood pressure.
    expect(commands.map((command) => command.payload.code)).toEqual([
      'BP_SYSTOLIC',
      'BP_DIASTOLIC',
      'HEART_RATE',
      'BP_ARM',
      'BP_POSITION',
      'BP_CUFF',
    ]);

    const systolic = commands[0];
    expect(systolic?.eventType).toBe('OBSERVATION_RECORDED');
    expect(systolic?.aggregateType).toBe('PATIENT');
    expect(systolic?.aggregateId).toBe(PATIENT);
    expect(systolic?.payload).toMatchObject({
      facility_id: FACILITY,
      patient_id: PATIENT,
      visit_id: VISIT,
      code: 'BP_SYSTOLIC',
      value: 128,
      unit: 'mm[Hg]',
      source: 'STATION',
      effective_at: '2026-09-05T09:00:00.000Z',
    });
    // Its own identity, generated here because the clinic generates it on the online path and a
    // measurement taken in a corridor needs one before anything has accepted it.
    expect(String(systolic?.payload.observation_id)).toHaveLength(36);
    // Exactly one value shape, which is what the ledger validates.
    expect(systolic?.payload.value_code).toBeUndefined();

    const arm = commands[3];
    expect(arm?.payload).toMatchObject({ code: 'BP_ARM', value_code: 'left' });
    expect(arm?.payload.value).toBeUndefined();
    expect(arm?.payload.unit).toBeUndefined();

    // Nothing the server assigns: no actor, no recorded time, no source of arrival, no sequence.
    for (const command of commands) {
      expect(Object.keys(command.payload)).not.toContain('recorded_at');
      expect(Object.keys(command.payload)).not.toContain('actor_user_id');
      expect(command.expectedSequence).toBeUndefined();
    }
  });

  it('gives two readings in one sitting two different moments', () => {
    // A blood pressure measured once is measured badly, and the second is not a correction of
    // the first — so the two carry different effective times and both survive.
    const commands = vitalsCommands(
      [reading({ systolic: '150' }), reading({ systolic: '138' })],
      idsFor(),
      { facilityId: FACILITY, patientId: PATIENT },
    );
    const systolics = commands.filter((command) => command.payload.code === 'BP_SYSTOLIC');
    expect(systolics).toHaveLength(2);
    expect(systolics[0]?.payload.effective_at).not.toBe(systolics[1]?.payload.effective_at);
    expect(systolics[0]?.eventId).not.toBe(systolics[1]?.eventId);
  });
});

describe('a station working with no connection at all', () => {
  it('reads the queue and the patient from the local record and records into it', async () => {
    const { store, clinic } = await device();
    seedClinic(clinic);

    // The morning's sync, while there was a connection.
    await syncOnce({
      store,
      transport: clinic.transport(),
      deviceId: 'device-1',
      now: () => NOW,
      newId: nextId,
    });

    // The connection goes. Anything that reaches for it from here fails the test.
    globalThis.fetch = (() => {
      throw new Error('a station must not need the network to work');
    }) as typeof globalThis.fetch;
    try {
      const queue = await readStationQueue(store, STATION, NOW);
      expect(queue).toHaveLength(1);
      expect(queue[0]?.status).toBe('in_service');
      expect(queue[0]?.waited_seconds).toBe(300);

      const entry = await inServicePatient(store, STATION, NOW);
      expect(entry?.patient_id).toBe(PATIENT);

      const person = await readLocalPatient(store, PATIENT, NOW);
      // The Bangla name, which is what the station screens show.
      expect(person?.name).toBe('রহিমা বেগম');
      expect(person?.sex).toBe('female');
      expect(person?.ageYears).toBe(47);

      const written = await recordVitals(
        store,
        [reading({ systolic: '128', diastolic: '82', temperature: '37.1' })],
        idsFor(),
        { facilityId: FACILITY, patientId: PATIENT, visitId: entry?.visit_id ?? null },
        { now: () => NOW },
      );
      expect(written).toHaveLength(6);

      // On screen at once, and honestly marked as not yet the clinic's.
      const systolic = await readProjection(store, 'observation', `${PATIENT}/BP_SYSTOLIC`);
      expect(systolic?.document.value).toBe(128);
      expect(systolic?.confirmed).toBe(false);
      expect(await readOutbox(store)).toHaveLength(6);

      const previous = await previousVitalsLocally(store, PATIENT);
      expect(previous.find((row) => row.code === 'BP_SYSTOLIC')?.value).toBe(128);
      // Nothing claims an author: the actor is the server's to assign, and a row that named
      // somebody locally would be asserting what it cannot know.
      expect(previous.find((row) => row.code === 'BP_SYSTOLIC')?.recorded_by).toBeUndefined();
    } finally {
      globalThis.fetch = realFetch;
    }

    // The connection comes back.
    const report = await syncOnce({
      store,
      transport: clinic.transport(),
      deviceId: 'device-1',
      now: () => NOW + 60_000,
      newId: nextId,
    });
    expect(report.accepted).toBe(6);
    expect(await readOutbox(store)).toEqual([]);

    // What the clinic holds is what the tablet recorded, moment for moment.
    const landed = [...clinic.ledger.values()].filter(
      (row) => row.event_type === 'OBSERVATION_RECORDED',
    );
    expect(landed).toHaveLength(6);
    expect(landed.map((row) => row.payload.code).sort()).toEqual([
      'BODY_TEMP',
      'BP_ARM',
      'BP_CUFF',
      'BP_DIASTOLIC',
      'BP_POSITION',
      'BP_SYSTOLIC',
    ]);
    expect(landed.every((row) => row.occurred_at === '2026-09-05T09:00:00.000Z')).toBe(true);
    expect(await integrityProblems(store, clinic, { quiescent: true })).toEqual([]);
  });

  it('does not lose a save the operator was told had failed', async () => {
    // The disk was full, the screen said "not saved", the operator freed some space and pressed
    // save again with the same ids. One measurement, not two.
    const { store, clinic } = await device();
    const context = { facilityId: FACILITY, patientId: PATIENT, visitId: VISIT };
    const stable = idsFor();

    store.faults.full = true;
    await expect(
      recordVitals(store, [reading({ pulse: '76' })], stable, context, { now: () => NOW }),
    ).rejects.toBeTruthy();
    expect(await readOutbox(store)).toEqual([]);

    store.faults.full = false;
    await recordVitals(store, [reading({ pulse: '76' })], stable, context, { now: () => NOW });
    await recordVitals(store, [reading({ pulse: '76' })], stable, context, { now: () => NOW });

    expect(await readOutbox(store)).toHaveLength(1);
    await syncOnce({
      store,
      transport: clinic.transport(),
      deviceId: 'device-1',
      now: () => NOW,
      newId: nextId,
    });
    expect(clinic.ledger.size).toBe(1);
    expect(await integrityProblems(store, clinic, { quiescent: true })).toEqual([]);
  });

  it('shows the clinic’s attribution once the value has been confirmed', async () => {
    const { store, clinic } = await device();
    clinic.append({
      event_id: 'obs-1',
      aggregate_type: 'PATIENT',
      aggregate_id: PATIENT,
      patient_id: PATIENT,
      event_type: 'OBSERVATION_RECORDED',
      event_version: 1,
      occurred_at: '2026-09-05T08:10:00.000Z',
      payload: { code: 'BP_SYSTOLIC', value: 118, unit: 'mm[Hg]' },
    });

    await syncOnce({
      store,
      transport: clinic.transport(),
      deviceId: 'device-1',
      now: () => NOW,
      newId: nextId,
    });

    const rows = await previousVitalsLocally(store, PATIENT);
    const systolic = rows.find((row) => row.code === 'BP_SYSTOLIC');
    expect(systolic?.value).toBe(118);
    expect(systolic?.source).toBe('WEB');
    const projection = await readProjection(store, 'observation', `${PATIENT}/BP_SYSTOLIC`);
    expect(projection?.confirmed).toBe(true);
  });

  it('reads the newest value per code, by when it was measured', async () => {
    // Not by when the clinic heard about it: a value recorded on this tablet ten minutes ago is
    // more recent than one pulled down this morning, whatever order they were stored in.
    const { store, clinic } = await device();
    clinic.append({
      event_id: 'obs-old',
      aggregate_type: 'PATIENT',
      aggregate_id: PATIENT,
      patient_id: PATIENT,
      event_type: 'OBSERVATION_RECORDED',
      event_version: 1,
      occurred_at: '2026-09-05T07:00:00.000Z',
      payload: { code: 'HEART_RATE', value: 64, unit: '/min' },
    });
    await syncOnce({
      store,
      transport: clinic.transport(),
      deviceId: 'device-1',
      now: () => NOW,
      newId: nextId,
    });

    await recordVitals(
      store,
      [reading({ pulse: '92' })],
      idsFor(),
      { facilityId: FACILITY, patientId: PATIENT },
      { now: () => NOW },
    );

    const rows = await previousVitalsLocally(store, PATIENT);
    expect(rows.filter((row) => row.code === 'HEART_RATE')[0]?.value).toBe(92);
  });
});

describe('an age nobody has to be told', () => {
  it('counts whole years and turns over on the birthday', () => {
    expect(ageInYears('1979-03-14', Date.parse('2026-03-13T00:00:00Z'))).toBe(46);
    expect(ageInYears('1979-03-14', Date.parse('2026-03-14T00:00:00Z'))).toBe(47);
    expect(ageInYears('', NOW)).toBe(0);
    expect(ageInYears('not a date', NOW)).toBe(0);
  });
});
