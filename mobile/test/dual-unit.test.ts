import { describe, expect, it } from 'vitest';

import { dualUnit, hasSecondaryUnit } from '@dthcms/clinical-calc';

/**
 * Dual-unit display on the station app (CP44, [R-08]).
 *
 * The component is React Native and these tests run in plain Node, so what is asserted here
 * is the pair the component is handed — the same arrangement as CP39's queue tests, and the
 * same reason: the logic worth testing is the conversion, and the rendering is verified on
 * the tablet.
 *
 * The web suite proves the same conversions through its own component, and both call the one
 * shared module — which is what stops a height reading 5′6″ on a tablet and 5.5 ft on a
 * desktop.
 */

describe('what the station app draws', () => {
  it('gives height in centimetres with feet and inches beneath', () => {
    const pair = dualUnit(168, 'cm', 'BODY_HEIGHT');
    expect(pair.primary.text).toBe('168.0');
    expect(pair.secondary?.text).toBe('5′6″');
  });

  it('gives weight in kilograms with pounds beneath', () => {
    expect(dualUnit(69.5, 'kg').secondary?.text).toBe('153.2');
  });

  it('gives a temperature with its offset applied', () => {
    expect(dualUnit(37, 'Cel').secondary?.text).toBe('98.6');
  });

  it('gives a waist in plain inches, not in feet', () => {
    // The bug this caught: every centimetre value was being spoken as feet and inches, so a
    // 94 cm waist rendered as 3′1″. The *code* decides, not the unit.
    expect(dualUnit(94, 'cm', 'WAIST_CIRC').secondary?.text).toBe('37');
  });

  it('gives a pulse alone', () => {
    expect(hasSecondaryUnit('/min')).toBe(false);
    expect(dualUnit(72, '/min').secondary).toBeNull();
  });

  it('rounds the two halves independently and consistently', () => {
    // The documented rounding: kg to one decimal, lb to one decimal, whole inches. A value
    // that rendered 69.5 on one screen and 69.46 on another is a value two people would
    // disagree about out loud.
    const pair = dualUnit(69.85322, 'kg');
    expect(pair.primary.text).toBe('69.9');
    expect(pair.secondary?.text).toBe('154.0');
  });
});
