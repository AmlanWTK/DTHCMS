import { afterEach, describe, expect, it, vi } from 'vitest';

/**
 * The mutation matrix (CP68 acceptance criterion 4).
 *
 * # The criterion, and why it needs its own file
 *
 * *"A deliberately introduced sync bug is caught by the suite, verified by mutation."* Everything
 * else in this checkpoint is a check on the engine; this is the check on the checks. Twelve
 * deliberate bugs are injected one at a time into the real module graph, the §13.10 scenario each
 * one is aimed at is run against the broken engine, and the claim that was named in advance has to
 * be among the ones that fail.
 *
 * Two assertions per mutation, and the second is the one that makes the file worth having:
 *
 *  1. **something failed** — the mutation did not sail through;
 *  2. **the predicted claim failed** — the rule written to catch this loss is the rule that caught
 *     it. Without the second, a suite could pass this file by being uniformly noisy, and a matrix
 *     of "yes, something went red" would say nothing about which losses are actually covered.
 *
 * A third assertion runs once, over the whole set: **the unmutated engine passes the same scenario
 * clean**. A mutation "caught" by a scenario that fails anyway is a mutation that proves nothing,
 * and that is not a hypothetical — it is the failure mode of every mutation suite that has ever
 * been quietly disabled.
 *
 * # How a bug gets into the engine
 *
 * `vi.resetModules()`, then the mutation registers its mock, then `./offline/run` is imported
 * fresh. Every module below it — the engine, the state, the harness, the clinic, the integrity
 * checker — is rebuilt in that one graph, so the engine really calls the broken function and every
 * object in the run comes from the same copy of every module. `test/offline/mutations.ts` explains
 * why both halves of that sentence matter.
 *
 * The list is imported twice on purpose: once statically, to name the tests at collection time,
 * and once inside each test from the fresh graph, to get the seams. Only strings cross from the
 * first copy to the second.
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

import { MUTATIONS } from './offline/mutations';

/** Load a fresh graph with one deliberate bug in it, and run the scenario it is aimed at. */
async function runMutated(id: string): Promise<{ claims: string[]; scenario: string }> {
  vi.resetModules();
  // From the *static* copy, because `install` only registers a mock and touches nothing else. The
  // hooks are taken from the fresh copy below, where they belong to the graph they will run in.
  const declared = MUTATIONS.find((mutation) => mutation.id === id);
  declared?.install?.();

  const live = await import('./offline/run');
  const mutation = live.mutationById(id);
  const scenario = live.scenarioById(mutation.scenario);
  const outcome = await scenario.run(mutation.hooks ?? {});
  return { claims: outcome.problems.map((problem) => problem.claim), scenario: scenario.id };
}

afterEach(() => {
  // Both, and in this order. `resetModules` alone would leave the mock registered for the next
  // import; `unmock` alone would leave the mutated copy cached. A leaked mutation would make the
  // next mutation's result a statement about two bugs at once.
  vi.doUnmock('@/lib/sync/state');
  vi.resetModules();
});

describe('the suite catches a deliberately introduced sync bug', () => {
  describe.each(MUTATIONS.map((mutation) => [mutation.id, mutation] as const))(
    'mutation · %s',
    (id, mutation) => {
      it(`is caught, by ${mutation.caughtBy}`, async () => {
        const { claims, scenario } = await runMutated(id);

        expect(
          claims,
          `${id} survived the whole of §13.10's "${scenario}". ` +
            `The fix is a new check, never a quieter mutation: ${mutation.bug}`,
        ).not.toEqual([]);

        expect(
          claims,
          `${id} was caught, but not by the claim it was aimed at. ` +
            `Either the rule is not doing what it was written to do, or the prediction was wrong ` +
            `about the system — both are findings. Claims that failed: ${claims.join(', ')}`,
        ).toContain(mutation.caughtBy);
      });
    },
  );

  it('is not catching bugs that a clean run would report anyway', async () => {
    // The control. Every scenario a mutation is aimed at must pass unmutated, or "caught" means
    // "this scenario was already red", which is how a mutation suite ends up proving nothing while
    // looking thorough.
    vi.resetModules();
    const live = await import('./offline/run');
    const aimedAt = [...new Set(MUTATIONS.map((mutation) => mutation.scenario))];
    for (const id of aimedAt) {
      const outcome = await live.scenarioById(id).run();
      expect(
        outcome.problems.map((problem) => `${problem.claim}: ${problem.detail}`),
        `${id} does not pass without a mutation, so nothing it "catches" means anything`,
      ).toEqual([]);
    }
  });

  it('aims every mutation at a claim that exists and a scenario that exists', async () => {
    vi.resetModules();
    const live = await import('./offline/run');
    const claims = new Set(Object.values(live.ALL_CLAIMS).map((claim) => claim.id));
    const scenarios = new Set(live.SCENARIOS.map((scenario) => scenario.id));
    for (const mutation of MUTATIONS) {
      expect(claims, `${mutation.id} names a claim that does not exist`).toContain(
        mutation.caughtBy,
      );
      expect(scenarios, `${mutation.id} names a scenario that does not exist`).toContain(
        mutation.scenario,
      );
    }
  });

  it('covers every seam a bug can be introduced through', () => {
    // Not coverage for its own sake. Each seam is a different *kind* of defect — a wrong decision,
    // a lost write, a reused identity, a corrupted payload — and a matrix that only ever mutated
    // decisions would say nothing about whether the checker can see the other three.
    const seams = new Set(MUTATIONS.map((mutation) => mutation.seam));
    expect([...seams].sort()).toEqual(['ids', 'module', 'store', 'transport']);
  });
});
