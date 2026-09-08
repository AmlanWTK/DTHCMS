import { describe, expect, it, vi } from 'vitest';

/**
 * The nightly soak (CP68 acceptance criterion 3).
 *
 * # What a soak is for that the matrix is not
 *
 * The §13.10 matrix runs ten scenarios, each a few dozen events long, each arranged by hand to
 * reach one situation. That is what makes it a good regression net and a poor explorer: every run
 * visits the same states in the same order, so it can only ever find what somebody already thought
 * of. A clinic day is eight hours, four hundred measurements, a connection that comes and goes for
 * reasons nobody controls, and an app that is killed once because Android wanted the memory — and
 * the interesting failures in a system like this one are in the **combinations**: a batch left in
 * flight when the signal goes, resolved after a restart, against a clinic that refused one of its
 * events while the tablet was away.
 *
 * So this runs long, runs varied, and runs on a **different seed every night**. It is deliberately
 * not part of `pnpm test`: a suite that takes a minute on every save is a suite people stop
 * running, and its job here is to explore rather than to gate.
 *
 * # The seed, and how to see a failure again
 *
 * The seed is the day, so each night explores somewhere new — and it is in the name of the test,
 * so a failure names it. To repeat one exactly:
 *
 * ```bash
 * DTHCMS_SOAK_SEED=20260907 pnpm --filter @dthcms/mobile run test:soak
 * ```
 *
 * Everything downstream of it is deterministic: the weather, the clinic's decisions, the backoff
 * jitter, when the signal drops, which patients arrive when, and which entry the clinic refuses.
 * Nothing sleeps and nothing reads the wall clock, so the same seed is the same day, on any
 * machine, in any order.
 *
 * # What "zero data loss" is asserted as
 *
 * Not "the ledger has four hundred rows". Every measurement the operator took ends the day in
 * exactly one of four honest places — delivered, held at the clinic, still queued, or refused and
 * on the operator's screen — and the accounting has to add up to the number of measurements taken.
 * A count that only checked deliveries would call a tablet that dropped its refusals a success.
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

const { NetworkError } = await import('@dthcms/api-client');
const { FLAKY_NETWORK, createWorld, seeded } = await import('../offline/run');
const { envSeed } = await import('../offline/env');
const { REASON_CODES } = await import('../../src/lib/sync/state');
const { readCounts, readOutbox } = await import('../../src/lib/sync/outbox');
const { TABLES } = await import('../../src/lib/local-store/schema');

import type { SyncTransport } from '../../src/lib/sync/transport';

/** Eight hours, in five-minute steps. Ninety-six ticks, which is a station tablet's morning. */
const STEP_MS = 5 * 60_000;
const STEPS = 96;

/** How many patients an hour reach one station. Twelve is the clinic's own busy-day number. */
const ARRIVALS_PER_STEP = 1;

/** Measurements per patient at one station: height, weight, waist, BP twice, pulse. */
const PER_PATIENT = 6;

/**
 * Today, as a number.
 *
 * The one place in this suite that reads the wall clock, and it reads a *date* rather than a time:
 * every run on one day is the same run, so a failure reported in the morning is reproducible in
 * the afternoon without anybody having captured anything.
 */
function today(): number {
  const now = new Date();
  return now.getUTCFullYear() * 10_000 + (now.getUTCMonth() + 1) * 100 + now.getUTCDate();
}

const SEED = envSeed('DTHCMS_SOAK_SEED', today());

describe(`a full clinic day, seed ${SEED}`, () => {
  it('ends with every measurement accounted for and the two records agreeing', async () => {
    const weather = seeded(SEED ^ 0x51ed270b);
    let online = true;
    let offlineUntil = 0;

    const world = await createWorld({
      seed: SEED,
      profile: FLAKY_NETWORK,
      hooks: {
        transport: (base: SyncTransport) => {
          // The radio, over and above the packet loss. A clinic's dead spots are not random per
          // request — they are minutes at a time, in the corridor between two rooms, and a client
          // that only ever meets one dropped request in ten never meets the case where its whole
          // backoff ladder runs out.
          const guard = <T>(call: () => Promise<T>): Promise<T> =>
            online ? call() : Promise.reject(new NetworkError(new Error('no signal here')));
          return {
            push: (body) => guard(() => base.push(body)),
            receipt: (id) => guard(() => base.receipt(id)),
            pull: (since, limit) => guard(() => base.pull(since, limit)),
            reference: () => guard(() => base.reference()),
            state: () => guard(() => base.state()),
          };
        },
      },
    });

    // One entry in a hundred is refused, which is a busier morning than a real clinic has and the
    // right side to be wrong on: the refusals are what the day's accounting is really testing.
    //
    // **Decided once per event and remembered.** The first version of this drew fresh each time
    // the clinic saw an event, and the soak found it within a dozen seeds: a batch cut half way
    // through is re-sent under its own id and reprocessed, so an entry refused on the first pass
    // was accepted on the second, and the client — which had correctly put the refusal in front of
    // an operator and then correctly dropped the acceptance — was reported as having lost a
    // refusal it never had. The lesson is worth more than the bug: **weather may be random,
    // decisions may not.** Whether a request arrives is a property of the morning; whether the
    // clinic accepts a value is a property of the value, and a harness that confuses the two
    // manufactures failures that look exactly like client defects.
    const decided = new Map<string, { code: string; reason: string } | null>();
    world.clinic.rejectWhen = (event) => {
      const already = decided.get(event.event_id);
      if (already !== undefined) return already;
      const answer =
        weather() < 0.01
          ? { code: REASON_CODES.invalidPayload, reason: 'the clinic would not take that value' }
          : null;
      decided.set(event.event_id, answer);
      return answer;
    };

    let patients = 0;
    let killed = false;
    let clockFixed = false;

    for (let step = 0; step < STEPS; step += 1) {
      // Patients arrive. Six measurements each, a minute or so apart, as an operator works.
      for (let arrival = 0; arrival < ARRIVALS_PER_STEP; arrival += 1) {
        patients += 1;
        for (let index = 0; index < PER_PATIENT; index += 1) {
          await world.record({ patientId: `patient-${patients}` });
        }
      }

      // The signal comes and goes in stretches rather than per request.
      if (!online && step >= offlineUntil) online = true;
      if (online && weather() < 0.15) {
        online = false;
        offlineUntil = step + 1 + Math.floor(weather() * 6);
      }

      // Mid-morning, Android reclaims the memory. The app comes back to whatever had committed.
      if (!killed && step === 34) {
        killed = true;
        await world.restart();
      }

      // And somebody notices the tablet's clock is four minutes fast and fixes it, which inverts
      // two entries' `occurred_at` on whichever patient is being measured at the time.
      if (!clockFixed && step === 50) {
        clockFixed = true;
        world.clock.setAheadOfClinic(4 * 60_000);
        await world.record({ patientId: `patient-${patients}` });
        world.clock.correct();
        await world.record({ patientId: `patient-${patients}` });
      }

      await world.sync();
      world.clock.advance(STEP_MS);
    }

    // The end of the day: the tablet goes back to the desk, on the clinic's own Wi-Fi, and is
    // given as long as it needs.
    online = true;
    await world.drain({ ticks: 60 });

    const taken = world.entries.length;
    const counts = await readCounts(world.store);
    const outbox = await readOutbox(world.store);
    const inLedger = world.entries.filter((entry) => world.clinic.ledger.has(entry.eventId)).length;
    const inQuarantine = world.clinic.held.length;
    const stillHere = outbox.length;
    const where = `${taken} taken · ${inLedger} delivered · ${inQuarantine} held · ${stillHere} on the tablet (${counts.needsAttention} refused) · seed ${SEED} · ${world.chaos.replay()}`;

    // Zero data loss, stated as an accounting rather than as a delivery count. Every measurement
    // is in exactly one honest place, and the four add up to the number of measurements taken.
    expect(inLedger + inQuarantine + stillHere, where).toBe(taken);

    // A day's work is a day's work: the tablet must actually have delivered, not merely accounted.
    expect(inLedger, where).toBeGreaterThan(taken * 0.95);

    // And the two records agree, event for event, after eight simulated hours of weather.
    const problems = await world.problems({ quiescent: true });
    expect(
      problems.map((problem) => `${problem.claim}: ${problem.detail}`),
      where,
    ).toEqual([]);

    // Nothing was recorded twice under two ids, which is the failure a long run is most likely to
    // produce and the one no count above would notice on its own.
    const local = await world.store.all({ table: TABLES.localEvents });
    const ids = new Set(local.map((row) => String(row.event_id)));
    expect(ids.size, where).toBe(local.length);
  });
});
