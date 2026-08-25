import type { ViteUserConfig } from 'vitest/config';

type CoverageOptions = NonNullable<NonNullable<ViteUserConfig['test']>['coverage']>;

/**
 * One coverage configuration for every workspace.
 *
 * A percentage is only meaningful if the denominator is. `packages/ui` measured 1.18%
 * with 183 tests passing, because a built Storybook bundle sat in the working directory
 * and every minified asset in it counted as uncovered source. A number that moves that
 * much depending on whether somebody happened to run a build is not a quality signal —
 * it is noise with a decimal point.
 *
 * So the exclusions live here, in one reviewable list, rather than being re-invented per
 * package. The rule for adding to it: **an exclusion must name the layer that covers the
 * code instead.** Anything excluded because it is genuinely hard to test is not an
 * exclusion, it is an untested file, and it belongs in the denominator where it can
 * embarrass someone.
 */
const EXCLUDE = [
  // Build output. Present or absent depending on what was last run, which is exactly
  // what a coverage denominator must never depend on.
  '**/node_modules/**',
  '**/dist/**',
  '**/.next/**',
  '**/storybook-static/**',
  '**/.expo-export/**',
  '**/coverage/**',
  '**/build/**',

  // Generated, never hand-written. `api-client/src/schema.ts` comes from the OpenAPI
  // document and is verified by the contract tests and the regeneration diff in CI.
  '**/src/schema.ts',

  // The tests themselves, and the tooling around them.
  '**/test/**',
  '**/e2e/**',
  '**/*.config.*', // .ts .mts .cts .js .mjs .cjs — mobile needs .mts, and a
  //                       brace list that misses one counts a config file as source
  '**/*.d.ts',

  // Storybook stories are examples for people, checked by the accessibility suite and by
  // eye in Storybook. Counting them as code under test measures documentation.
  '**/*.stories.{ts,tsx}',
];

/**
 * The floors, confirmed by Dr. Nahid at CP13.
 *
 * 90% where a wrong number reaches a patient — clinical calculation and safety rules.
 * 70% everywhere else: high enough to notice a regression, low enough that nobody is
 * tempted to write assertion-free tests to clear it.
 */
export const CLINICAL_FLOOR = 90;
export const DEFAULT_FLOOR = 70;

/**
 * Branch coverage is deliberately allowed to sit below the statement floor.
 *
 * Every primitive here carries branches for props no screen sets yet — a `Card` with
 * five variants used at two. Holding branches to the statement floor would mean writing
 * tests for combinations nobody renders, which is work that produces a number rather
 * than confidence. Statements and lines are the floors that mean something today;
 * branches rise as the application actually uses what the design system offers.
 */
export function coverage(
  floor: number,
  options: { exclude?: string[]; branchFloor?: number } = {},
): CoverageOptions {
  return {
    provider: 'v8',
    reporter: ['text', 'json-summary'],
    exclude: [...EXCLUDE, ...(options.exclude ?? [])],
    thresholds: {
      statements: floor,
      lines: floor,
      functions: floor,
      branches: options.branchFloor ?? floor - 15,
    },
  };
}
