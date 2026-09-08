import { describe, expect, it, vi } from 'vitest';

/**
 * §13.10, run (CP68 acceptance criteria 1 and 2).
 *
 * # What this file is, and what it is not
 *
 * It is the checkpoint's first criterion made mechanical: every clause of §13.10 is a scenario in
 * `test/offline/scenarios.ts`, every scenario runs here, and the data-integrity checker runs after
 * each one. It is deliberately thin — the scenarios hold the argument, this holds the guarantee
 * that they all run and that none of them was quietly dropped.
 *
 * It is **not** the whole of the offline suite. `sync-engine.test.ts` is still where each rule is
 * argued with individually, one property per test, and it will go on being the file somebody reads
 * to understand the engine. This one exists because "all of §13.10, on every push, with the
 * integrity check clean" is a different claim from "seventy-five tests pass", and CP68 is the
 * checkpoint that has to make the first one.
 *
 * # The transcription check earns its place
 *
 * The first test compares each scenario's `blueprint` field against the clauses of §13.10 as
 * ratified. It looks like bookkeeping and it is the one check here that cannot be satisfied by
 * writing more code: this repository has been caught three times by a green check that confirmed
 * something *existed* rather than that it *worked*, and "all ten scenarios run" is exactly the
 * shape of claim that decays into "all nine remaining scenarios run" the first time somebody
 * deletes an awkward one.
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

const { ALL_CLAIMS, SCENARIOS } = await import('./offline/run');
const { actionFor } = await import('../src/lib/sync/state');

import type { EventAction, Outcome } from '../src/lib/sync/state';

/**
 * §13.10 of `docs/blueprint-v2.0.md`, as ratified, split on the middots the paragraph uses.
 *
 * Copied by hand from the blueprint and not read from it, deliberately. The blueprint is under
 * custody (`scripts/check_custody.py` fails the build if its bytes change), so a copy here that
 * matches is a copy that will keep matching until somebody ratifies a new one — and on the day
 * somebody does, this test is where the change is noticed and argued about rather than a place it
 * silently passes through.
 */
const THIRTEEN_TEN = [
  'Airplane mode mid-entry',
  'kill the app with a full queue and relaunch',
  'sync 200 queued events at once',
  'server rejects one event in a batch of fifty',
  'device clock set 3 hours wrong',
  'token expires while offline (must refresh cleanly on reconnect, not lose the queue)',
  'device revoked while offline',
  'duplicate submission of an entire batch',
  'flaky network (10% packet loss, 3s latency)',
  'storage full',
];

describe('the §13.10 matrix is all of §13.10', () => {
  it('has one scenario for each clause of the paragraph, in the paragraph’s own words', () => {
    expect(SCENARIOS.map((scenario) => scenario.blueprint)).toEqual(THIRTEEN_TEN);
  });

  it('gives every scenario an id nothing else uses, so the matrix can name one', () => {
    const ids = SCENARIOS.map((scenario) => scenario.id);
    expect(new Set(ids).size).toBe(ids.length);
  });

  it('says of every scenario whether a tablet is still owed something', () => {
    // The honest half of criterion 1. A scenario that runs here and needs hardware as well must
    // say so in one place that a person can read, or the green tick starts meaning more than it
    // is entitled to. `awaits` may be empty only where nothing is genuinely outstanding.
    for (const scenario of SCENARIOS) {
      if (scenario.reach === 'ci-and-device') {
        expect(scenario.awaits, `${scenario.id} claims to need a device and says nothing`).not.toBe(
          '',
        );
      }
    }
  });

  it('states, for every claim it can fail, what is lost when it does', () => {
    // A rule with no stated loss cannot be argued with, and a rule nobody can argue with is one
    // nobody will delete when it becomes wrong — they will weaken it instead, quietly.
    for (const claim of Object.values(ALL_CLAIMS)) {
      expect(claim.claim.length, `${claim.id} has no claim`).toBeGreaterThan(20);
      expect(claim.loss.length, `${claim.id} has no loss`).toBeGreaterThan(20);
    }
  });
});

describe('every outcome the contract defines has a decision behind it', () => {
  it('answers each of the five, and nothing falls through to a default', () => {
    // Exhaustive by construction: `Record<Outcome, …>` is a compile error the day the contract
    // gains a sixth outcome, which is the point. A client that met an unknown outcome and guessed
    // would be guessing about whether a measurement is safe.
    const expected: Record<Outcome, EventAction> = {
      ACCEPTED: 'drop',
      DUPLICATE: 'drop',
      REJECTED: 'attention',
      QUARANTINED: 'held',
      BLOCKED: 'retry',
    };
    for (const [outcome, action] of Object.entries(expected)) {
      expect(actionFor(outcome), outcome).toBe(action);
    }
    // And the one outcome that is not an outcome: a newer clinic saying something this build has
    // never heard of must never make a queued measurement disappear.
    expect(actionFor('SOMETHING_A_LATER_SERVER_SAYS')).toBe('attention');
  });
});

describe.each(SCENARIOS.map((scenario) => [scenario.id, scenario] as const))(
  '§13.10 · %s',
  (_id, scenario) => {
    it(scenario.asks, async () => {
      const outcome = await scenario.run();
      // Both halves in one assertion, because they are one question: the scenario's own checks and
      // the record-versus-record comparison that runs after every one of them. A scenario that
      // passed its own checks while the two records disagreed would be a scenario that tested the
      // author's expectations.
      expect(
        outcome.problems.map((problem) => `${problem.claim}: ${problem.detail}`),
        `${scenario.id} · ${outcome.notes.join(' · ')}`,
      ).toEqual([]);
    });
  },
);
