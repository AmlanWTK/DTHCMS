import { describe, expect, it } from 'vitest';

import type { RealtimeMessage } from '../src/realtime';
import { gapInvalidations, queryKeys, realtimeInvalidations } from '../src/realtime-keys';

/**
 * The invalidate-don't-mutate rule (CP27), as tests.
 *
 * The plan puts that rule in bold and says it is enforced in review. Review is not a
 * mechanism, so this file is: every assertion below is about *keys*, and the module under
 * test exports no function that returns data. A future change that started writing message
 * contents into the cache would have nowhere to put them.
 */

const message = (over: Partial<RealtimeMessage> = {}): RealtimeMessage => ({
  seq: 1,
  topic: 'patient:p1',
  kind: 'measurement.recorded',
  at: '2026-09-03T04:42:00Z',
  ...over,
});

describe('realtimeInvalidations', () => {
  it('invalidates the patient the message is about', () => {
    const keys = realtimeInvalidations(message({ patient_id: 'p1' }));
    expect(keys).toContainEqual(queryKeys.patient('p1'));
  });

  it('invalidates the vitals strip for a measurement, and the visit it belongs to', () => {
    const keys = realtimeInvalidations(
      message({ kind: 'measurement.recorded', patient_id: 'p1', visit_id: 'v9' }),
    );
    expect(keys).toContainEqual(queryKeys.visit('v9'));
    expect(keys).toContainEqual(queryKeys.visitVitals('v9'));
    expect(keys).toContainEqual(queryKeys.patientTimeline('p1'));
  });

  it('leaves the timeline alone for a kind that does not add a row to it', () => {
    const keys = realtimeInvalidations(
      message({ topic: 'user:u1', kind: 'alert.raised', patient_id: undefined }),
    );
    expect(keys).not.toContainEqual(queryKeys.patientTimeline('p1'));
    expect(keys).toContainEqual(queryKeys.auditAlerts());
  });

  it('refreshes the board when a visit moves, wherever the message was published', () => {
    const keys = realtimeInvalidations(
      message({
        topic: 'station:s1',
        kind: 'visit.moved',
        visit_id: 'v9',
        summary: { facility_id: 'f1' },
      }),
    );
    expect(keys).toContainEqual(queryKeys.queue('f1'));
    expect(keys).toContainEqual(queryKeys.station('s1'));
  });

  it('maps the administrative kinds', () => {
    expect(
      realtimeInvalidations(message({ topic: 'user:u1', kind: 'device.revoked' })),
    ).toContainEqual(queryKeys.devices());
    expect(
      realtimeInvalidations(message({ topic: 'user:u1', kind: 'role.granted' })),
    ).toContainEqual(queryKeys.users());
    expect(
      realtimeInvalidations(message({ topic: 'user:u1', kind: 'break_glass.opened' })),
    ).toContainEqual(queryKeys.auditAlerts());
  });

  it('returns nothing surprising for a kind this build has never heard of', () => {
    // A newer gateway publishing a new kind during a rolling deploy. The screen stays
    // correct because it still fetches through the API; it is only not refreshed early.
    const keys = realtimeInvalidations(
      message({ topic: 'patient:p1', kind: 'holographic.projection.started' }),
    );
    expect(keys).toEqual([queryKeys.patient('p1')]);
  });

  it('never returns the same key twice', () => {
    const keys = realtimeInvalidations(message({ topic: 'patient:p1', patient_id: 'p1' }));
    const serialised = keys.map((key) => JSON.stringify(key));
    expect(new Set(serialised).size).toBe(serialised.length);
  });

  it('ignores a malformed topic rather than inventing a key from it', () => {
    expect(realtimeInvalidations(message({ topic: 'patient:', patient_id: undefined }))).toEqual(
      [],
    );
    expect(realtimeInvalidations(message({ topic: 'nonsense', patient_id: undefined }))).toEqual(
      [],
    );
  });
});

describe('a correction request reaching the operator who typed the value (CP62)', () => {
  it('lights up the operator\u2019s own queue, wherever the message was published', () => {
    const keys = realtimeInvalidations(
      message({ topic: 'user:u7', kind: 'correction.requested', patient_id: undefined }),
    );
    expect(keys).toContainEqual(queryKeys.corrections());
  });

  it('also refreshes the chain under the patient\u2019s value, for the physician who flagged it', () => {
    const keys = realtimeInvalidations(
      message({ topic: 'user:u7', kind: 'correction.applied', patient_id: 'p1' }),
    );
    expect(keys).toContainEqual(queryKeys.corrections());
    expect(keys).toContainEqual(queryKeys.patientCorrections('p1'));
    // And the value itself moved, so the patient's own reads are stale too.
    expect(keys).toContainEqual(queryKeys.patient('p1'));
  });

  it('carries no value \u2014 the key is a notification, and the screen re-reads through the API', () => {
    const keys = realtimeInvalidations(
      message({ topic: 'user:u7', kind: 'correction.requested', patient_id: 'p1' }),
    );
    for (const key of keys) {
      for (const part of key) {
        expect(typeof part === 'string' || typeof part === 'number').toBe(true);
      }
    }
  });
});

describe('a retraining flag raised on somebody’s own record (CP63)', () => {
  it('refreshes the quality record on the topic of the person it is about', () => {
    const keys = realtimeInvalidations(
      message({ topic: 'user:u7', kind: 'quality.flag_raised', patient_id: undefined }),
    );
    expect(keys).toContainEqual(queryKeys.quality());
  });

  it('carries no count and no evidence — a flag is read back through the API', () => {
    /*
     * The gateway publishes a flag id and a threshold code and nothing else, deliberately:
     * the screen re-reads through the endpoint, which puts the read back through the
     * permission that decides whether this reader may see a record at all. Nothing here may
     * turn that notification into a number on somebody's screen.
     */
    const keys = realtimeInvalidations(
      message({
        topic: 'user:u7',
        kind: 'quality.flag_raised',
        patient_id: undefined,
        summary: { flag_id: 'f1', threshold_code: 'TRANSCRIPTION_3_IN_30' },
      }),
    );
    for (const key of keys) {
      for (const part of key) {
        expect(typeof part === 'string' || typeof part === 'number').toBe(true);
      }
    }
    expect(keys).toContainEqual(queryKeys.quality());
  });

  it('refreshes the same record again when a supervisor answers it', () => {
    /*
     * The answer is published on the operator's own topic too, not only the raise. A note that
     * appeared on somebody's device and then sat there saying "waiting" after a supervisor had
     * already answered it would teach them that things are decided about them out of sight —
     * the same failure as never telling them, arriving a fortnight later.
     */
    const keys = realtimeInvalidations(
      message({
        topic: 'user:u7',
        kind: 'quality.flag_resolved',
        patient_id: undefined,
        summary: { flag_id: 'f1', threshold_code: 'TRANSCRIPTION_3_IN_30', status: 'DISMISSED' },
      }),
    );
    expect(keys).toContainEqual(queryKeys.quality());
  });

  it('names no patient, because a staff quality record has none in it', () => {
    // The database refuses a flag that carries a patient reference. The client half of that
    // decision is that nothing on this path reaches for one.
    const keys = realtimeInvalidations(
      message({ topic: 'user:u7', kind: 'quality.flag_raised', patient_id: undefined }),
    );
    expect(keys.some((key) => key[0] === 'patient')).toBe(false);
  });
});

describe('gapInvalidations', () => {
  it('refreshes exactly what the client is watching', () => {
    const keys = gapInvalidations(['patient:p1', 'queue:f1', 'station:s2']);
    expect(keys).toEqual([queryKeys.patient('p1'), queryKeys.queue('f1'), queryKeys.station('s2')]);
  });

  it('re-reads the correction queue after a gap, because a flag raised offline is silent', () => {
    expect(gapInvalidations(['user:u7'])).toEqual([
      queryKeys.auditAlerts(),
      queryKeys.corrections(),
      // And the quality record: a retraining flag is raised on the back of a correction, so
      // the message a gap swallows is exactly the one that would otherwise reach the operator
      // second-hand, from their supervisor.
      queryKeys.quality(),
    ]);
  });

  it('does not invalidate the whole cache — a clinic-wide refetch storm on every wifi blip', () => {
    expect(gapInvalidations([])).toEqual([]);
    expect(gapInvalidations(['nonsense', 'patient:'])).toEqual([]);
  });
});

describe('the discipline itself', () => {
  it('exposes no way to write a message into the cache', async () => {
    const module = (await import('../src/realtime-keys')) as Record<string, unknown>;
    const exported = Object.keys(module).sort();
    expect(exported).toEqual(['gapInvalidations', 'queryKeys', 'realtimeInvalidations']);
    // Every one of them returns keys or is a key factory. None takes a cache.
    for (const name of ['gapInvalidations', 'realtimeInvalidations']) {
      expect(typeof module[name]).toBe('function');
      expect((module[name] as (...args: never[]) => unknown).length).toBe(1);
    }
  });
});
