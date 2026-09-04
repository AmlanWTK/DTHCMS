import { describe, expect, it } from 'vitest';

import { DISPLAY_PAIRS, dualUnit, hasSecondaryUnit } from '../src/display';

/**
 * Dual-unit display (CP44, [R-08]).
 *
 * The criteria are about what appears on a screen, so what is testable here is the pair the
 * screen is handed: that height carries feet and inches, that weight carries pounds, that
 * the rounding is the documented rounding, and that a value with no second unit anybody uses
 * says so rather than inventing one.
 */

describe('height', () => {
  it('shows centimetres with feet and inches beneath, not decimal feet', () => {
    // "Five point five feet" is a number a patient has to convert in their head, which is
    // the whole thing this requirement exists to prevent.
    const pair = dualUnit(168, 'cm', 'BODY_HEIGHT');
    expect(pair.primary.text).toBe('168.0');
    expect(pair.secondary?.text).toBe('5′6″');
  });

  it('carries twelve inches into a foot', () => {
    // 182.9 cm is 71.99 inches. Rounding to 72 and then failing to carry would render 5′12″.
    expect(dualUnit(182.9, 'cm', 'BODY_HEIGHT').secondary?.text).toBe('6′0″');
  });

  it('rounds to whole inches', () => {
    expect(dualUnit(167.7, 'cm', 'BODY_HEIGHT').secondary?.text).toBe('5′6″');
    expect(dualUnit(150, 'cm', 'BODY_HEIGHT').secondary?.text).toBe('4′11″');
  });
});

describe('a circumference is not a height', () => {
  it('shows a waist in plain inches', () => {
    // 94 cm is "37 inches" to everybody who has ever bought trousers. Rendering it as 3′1″
    // is arithmetically correct and clinically absurd — the kind of thing that gets noticed
    // in a waiting room rather than in a code review.
    const pair = dualUnit(94, 'cm', 'WAIST_CIRC');
    expect(pair.secondary?.text).toBe('37');
    expect(pair.secondary?.unit).toBe('in');
  });

  it('defaults to plain inches when the caller does not say which code', () => {
    // The safe direction to be wrong in: a new circumference added to the registry gets
    // inches, not feet.
    expect(dualUnit(94, 'cm').secondary?.text).toBe('37');
  });
});

describe('weight', () => {
  it('shows kilograms with pounds beneath', () => {
    const pair = dualUnit(69.5, 'kg');
    expect(pair.primary.text).toBe('69.5');
    expect(pair.secondary?.unit).toBe('[lb_av]');
    expect(pair.secondary?.text).toBe('153.2');
  });

  it('rounds both halves to one decimal', () => {
    expect(dualUnit(69.85322, 'kg').primary.text).toBe('69.9');
    expect(dualUnit(69.85322, 'kg').secondary?.text).toBe('154.0');
  });
});

describe('temperature', () => {
  it('converts with its offset', () => {
    // 37 °C is 98.6 °F. A factor-only conversion gives 66.6, which is not a temperature
    // anybody would notice was wrong on a busy morning.
    expect(dualUnit(37, 'Cel').secondary?.text).toBe('98.6');
  });
});

describe('the analyte concentrations', () => {
  it.each([
    ['mmol/L', 7.0, '126'],
    ['mmol/L#chol', 5.2, '201'],
    ['umol/L', 106.104, '1.20'],
    ['mmol/mol', 53, '7.0'],
  ])('%s %s shows as %s in the familiar unit', (unit, value, want) => {
    expect(dualUnit(value as number, unit as string).secondary?.text).toBe(want);
  });
});

describe('values with no second unit', () => {
  it.each(['mm[Hg]', '/min', '%', 'kg/m2', 'mL/min/{1.73_m2}'])('%s renders alone', (unit) => {
    expect(hasSecondaryUnit(unit)).toBe(false);
    expect(dualUnit(128, unit).secondary).toBeNull();
  });

  it('is a deliberate list, not an oversight', () => {
    // Showing "128 mmHg / 17.1 kPa" beneath a blood pressure would be noise on the one
    // reading nobody in this clinic reads in kilopascals.
    expect(Object.keys(DISPLAY_PAIRS)).not.toContain('mm[Hg]');
  });
});

describe('an unknown unit', () => {
  it('renders the value rather than nothing', () => {
    // A screen that showed a blank because a unit was added to the registry and not to this
    // table would be a screen hiding a clinical value.
    const pair = dualUnit(42, 'not-a-unit');
    expect(pair.primary.value).toBe(42);
    expect(pair.secondary).toBeNull();
  });
});
