import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import { anthroPanel, type Panel, type PanelInput } from '../src/panel';

/**
 * The panel's half of the parity harness (CP45 criterion 2, ADR-0025).
 *
 * `backend/internal/clinical/calc/panel_test.go` reads the same file and asserts the same
 * things. Neither implementation generated it: every expected number was computed from the
 * published equations directly, so a shared mistake in the two libraries fails here rather
 * than agreeing with itself.
 */

interface FixtureCase {
  name: string;
  input: {
    weight_kg: number | null;
    height_cm: number | null;
    waist_cm: number | null;
    hip_cm: number | null;
    age_years: number;
    sex: PanelInput['sex'];
    asian: boolean;
  };
  expect: Panel;
}

const here = dirname(fileURLToPath(import.meta.url));
const fixture = JSON.parse(readFileSync(join(here, '..', 'fixtures', 'panel.json'), 'utf8')) as {
  tolerance: number;
  cases: FixtureCase[];
};

describe('the anthropometry panel matches the reference fixture', () => {
  it('has cases, so it cannot pass by doing nothing', () => {
    expect(fixture.cases.length).toBeGreaterThan(10);
  });

  for (const testCase of fixture.cases) {
    it(testCase.name, () => {
      const got = anthroPanel({
        weightKg: testCase.input.weight_kg,
        heightCm: testCase.input.height_cm,
        waistCm: testCase.input.waist_cm,
        hipCm: testCase.input.hip_cm,
        ageYears: testCase.input.age_years,
        sex: testCase.input.sex,
        asian: testCase.input.asian,
      });
      const want = testCase.expect;

      for (const key of ['bmi', 'bmr', 'ideal_body_weight', 'whr'] as const) {
        const expected = want[key];
        const actual = got[key];
        if (expected === undefined) {
          expect(actual, `${key} should not have been computed`).toBeUndefined();
          continue;
        }
        expect(actual, `${key} was not computed`).toBeDefined();
        expect(Math.abs(actual!.value - expected.value)).toBeLessThanOrEqual(fixture.tolerance);
        expect(actual!.unit).toBe(expected.unit);
        expect(actual!.formula).toBe(expected.formula);
        expect(actual!.version).toBe(expected.version);
      }

      expect(got.obesity_class).toBe(want.obesity_class);
      expect(got.obesity_class_version).toBe(want.obesity_class_version);
      expect(got.needs ?? {}).toEqual(want.needs ?? {});
      expect(got.refused ?? {}).toEqual(want.refused ?? {});
    });
  }
});

describe('the panel is instant', () => {
  // Criterion (1): derived values appear within 200ms of the last keystroke, computed
  // locally. This asserts the computation is far enough below that budget that the whole of
  // it belongs to rendering — measured on the slowest thing in the room, which is not a
  // phone but a CI container under load.
  it('computes a thousand panels in well under the keystroke budget', () => {
    const input: PanelInput = {
      weightKg: 72.5,
      heightCm: 170,
      waistCm: 92,
      hipCm: 98,
      ageYears: 45,
      sex: 'male',
      asian: true,
    };
    const started = performance.now();
    for (let i = 0; i < 1000; i += 1) anthroPanel(input);
    expect(performance.now() - started).toBeLessThan(200);
  });
});
