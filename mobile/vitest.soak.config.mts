import { defineConfig } from 'vitest/config';

import base from './vitest.config.mts';

/**
 * The nightly soak, on its own configuration (CP68).
 *
 * # Why it is not simply another `*.test.ts`
 *
 * The soak runs a simulated eight-hour clinic day on a seed that changes daily. Both halves of
 * that sentence disqualify it from the suite that gates every push: it is slower than anything
 * else here, and it is deliberately not the same run twice, which is the opposite of what a gate
 * should be. A gate that occasionally explores somewhere new is a gate that occasionally goes red
 * for a reason unrelated to the change in front of it, and the third time that happens people stop
 * reading it.
 *
 * # Why not a skip flag instead
 *
 * The obvious alternative — one file with `it.skipIf(!process.env.SOAK)` — was rejected for the
 * reason `docs/testing.md` gives twice: a check that silently skips reports success. There would
 * then be a green tick on every push that meant "the soak did not run", which is exactly the class
 * of reassuring non-answer this checkpoint exists to stop producing. A separate configuration
 * makes the soak either run or not exist in a run; there is no third state that looks like a pass.
 *
 * The cost is that `pnpm --filter mobile test` does not compile this file, so a soak broken by a
 * refactor would not be noticed until the following night. That is covered from the other side:
 * `pnpm -r typecheck` runs `tsc` over the whole package including `test/soak`, on every push.
 */
export default defineConfig({
  ...base,
  test: {
    ...base.test,
    // Replaced rather than merged, and that is the whole reason this is written out by hand
    // instead of with `mergeConfig`: that helper concatenates arrays, so the soak configuration
    // would run the soak *and* all 1,403 ordinary tests, nightly, for no benefit and several
    // minutes. The alias configuration above it is inherited, which is the part worth inheriting.
    include: ['test/soak/**/*.soak.ts'],
    // Eight simulated hours pass in a few seconds because the clock is injected, but a soak that
    // one day grows to a hundred simulated days should fail on its assertions rather than on a
    // default timeout that has nothing to say about correctness.
    testTimeout: 300_000,
    coverage: { enabled: false },
  },
});
