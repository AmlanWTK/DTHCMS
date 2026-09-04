import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import {
  FORMULAS,
  bmi,
  bmrHarrisBenedict,
  bmrMifflin,
  bsaDuBois,
  bsaMosteller,
  classify,
  egfrBedsideSchwartz,
  egfrCkdEpi2021,
  idealBodyWeight,
  packYears,
  round,
  whr,
  type Calculated,
  type CalcRefusal,
  type Sex,
} from '../src/index';

/**
 * The parity harness, TypeScript half (CP43, criteria 1 and 2).
 *
 * The same fixture file the Go suite reads, run through the same case list. "Go and TS agree
 * on 100% of fixtures" is therefore not a claim somebody checks by hand — it is what green
 * means on both sides.
 *
 * The dispatch switch below is the mirror of `run()` in the Go harness. A formula added to
 * one language and not the other fails here, loudly, rather than quietly not being covered.
 */

const here = dirname(fileURLToPath(import.meta.url));
const fixtures = JSON.parse(
  readFileSync(join(here, '..', 'fixtures', 'reference.json'), 'utf8'),
) as {
  tolerance: number;
  cases: Record<
    string,
    Array<{ name: string; inputs: Record<string, unknown>; expected: unknown }>
  >;
  refusals: Array<{ formula: string; inputs: Record<string, unknown>; reason: string }>;
};

function n(inputs: Record<string, unknown>, key: string): number {
  const value = inputs[key];
  if (typeof value !== 'number') {
    throw new Error(`the fixture's ${key} is ${typeof value}, not a number`);
  }
  return value;
}

function s(inputs: Record<string, unknown>): Sex {
  return (inputs['sex'] ?? 'other') as Sex;
}

function run(formula: string, inputs: Record<string, unknown>): Calculated {
  switch (formula) {
    case 'bmi':
      return bmi(n(inputs, 'weight_kg'), n(inputs, 'height_cm'));
    case 'whr':
      return whr(n(inputs, 'waist_cm'), n(inputs, 'hip_cm'));
    case 'bmr_mifflin_st_jeor':
      return bmrMifflin(
        n(inputs, 'weight_kg'),
        n(inputs, 'height_cm'),
        n(inputs, 'age_years'),
        s(inputs),
      );
    case 'bmr_harris_benedict_revised':
      return bmrHarrisBenedict(
        n(inputs, 'weight_kg'),
        n(inputs, 'height_cm'),
        n(inputs, 'age_years'),
        s(inputs),
      );
    case 'ibw_devine':
      return idealBodyWeight(n(inputs, 'height_cm'), s(inputs));
    case 'bsa_du_bois':
      return bsaDuBois(n(inputs, 'weight_kg'), n(inputs, 'height_cm'));
    case 'bsa_mosteller':
      return bsaMosteller(n(inputs, 'weight_kg'), n(inputs, 'height_cm'));
    case 'egfr_ckd_epi_2021':
      return egfrCkdEpi2021(n(inputs, 'creatinine_mg_dl'), n(inputs, 'age_years'), s(inputs));
    case 'egfr_bedside_schwartz':
      return egfrBedsideSchwartz(
        n(inputs, 'creatinine_mg_dl'),
        n(inputs, 'height_cm'),
        n(inputs, 'age_years'),
      );
    case 'pack_years':
      return packYears(n(inputs, 'cigarettes_per_day'), n(inputs, 'years'));
    default:
      throw new Error(`the fixture names a formula this library does not implement: ${formula}`);
  }
}

describe('every reference vector matches', () => {
  // Criterion 1. Every expected value was computed independently from the published
  // equation, not read off either implementation.
  const numeric = Object.entries(fixtures.cases).filter(([formula]) => formula !== 'obesity_class');

  it('exercises the whole library', () => {
    expect(numeric.length).toBeGreaterThanOrEqual(9);
    expect(numeric.reduce((total, [, cases]) => total + cases.length, 0)).toBeGreaterThanOrEqual(
      20,
    );
  });

  for (const [formula, cases] of numeric) {
    for (const testCase of cases) {
      it(`${formula}: ${testCase.name}`, () => {
        const got = run(formula, testCase.inputs);
        expect(got.ok, `refused a valid case: ${JSON.stringify(got)}`).toBe(true);
        if (!got.ok) return;
        expect(Math.abs(got.result.value - (testCase.expected as number))).toBeLessThanOrEqual(
          fixtures.tolerance,
        );
        expect(got.result.formula).toBe(formula);
        // A stored value without its version cannot be identified later, and CKD-EPI has
        // already changed once.
        expect(got.result.version).not.toBe('');
      });
    }
  }
});

describe('the two obesity scales', () => {
  // Getting this wrong changes a patient's pathway rather than a number on a screen: a BMI
  // of 24 is "normal" internationally and "overweight" in a Bangladeshi patient.
  for (const testCase of fixtures.cases['obesity_class'] ?? []) {
    it(testCase.name, () => {
      const value = n(testCase.inputs, 'bmi');
      for (const [scale, want] of Object.entries(testCase.expected as Record<string, string>)) {
        const got = classify(value, scale === 'asian');
        expect(got.ok).toBe(true);
        if (!got.ok) return;
        expect(got.class, `${scale} scale at BMI ${value}`).toBe(want);
        expect(got.version).not.toBe('');
      }
    });
  }
});

describe('an invalid input says so', () => {
  // Criterion 4. A BMI computed from a height of zero is Infinity, which renders as an empty
  // cell — a wrong answer that looks like a missing one.
  for (const refusal of fixtures.refusals) {
    it(`${refusal.formula}: ${refusal.reason}`, () => {
      const got = run(refusal.formula, refusal.inputs);
      expect(got.ok, `returned a value instead of refusing`).toBe(false);
      expect((got as CalcRefusal).reason).toBe(refusal.reason);
    });
  }
});

describe('versions', () => {
  it('names a version for every formula', () => {
    expect(Object.keys(FORMULAS).length).toBeGreaterThanOrEqual(10);
    for (const [name, version] of Object.entries(FORMULAS)) {
      expect(version, `${name} has no version`).not.toBe('');
    }
  });

  it('covers every formula the fixtures exercise', () => {
    // So a formula can neither be implemented without a version nor versioned without being
    // reachable from the fixtures.
    for (const formula of Object.keys(fixtures.cases)) {
      expect(FORMULAS[formula], `${formula} is exercised but not listed`).toBeDefined();
    }
  });

  it('agrees with the Go library on every version string', () => {
    // The versions are the one part of the contract the fixtures cannot check, because a
    // fixture holds values and not versions. They are written out here so a bump on one side
    // and not the other is a failing test rather than two records that claim to have been
    // computed the same way and were not.
    expect(FORMULAS).toEqual({
      bmi: '1.0.0',
      obesity_class: '1.0.0',
      whr: '1.0.0',
      bmr_mifflin_st_jeor: '1.0.0',
      bmr_harris_benedict_revised: '1.0.0',
      ibw_devine: '1.0.0',
      bsa_du_bois: '1.0.0',
      bsa_mosteller: '1.0.0',
      egfr_ckd_epi_2021: '2021.1',
      egfr_bedside_schwartz: '2009.1',
      pack_years: '1.0.0',
    });
  });
});

describe('rounding', () => {
  // "Round to one decimal" is ambiguous at a half and the two runtimes resolve it
  // differently. The shared definition is what keeps them agreeing.
  it.each([
    [24.691358, 1, 24.7],
    [24.65, 1, 24.7],
    [24.64999, 1, 24.6],
    [77.88343795839066, 0, 78],
    [1.996421022275045, 2, 2.0],
    [0.5, 0, 1],
  ])('round(%s, %s) is %s', (value, decimals, want) => {
    expect(round(value, decimals)).toBe(want);
  });
});
