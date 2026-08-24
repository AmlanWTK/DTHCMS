import { describe, expect, it } from 'vitest';
import { ramps, themes, clinicalStatuses, resolveTypeRole } from '../src/index';

describe('token resolution', () => {
  it('builds every ramp', () => {
    expect(Object.keys(ramps).sort()).toEqual(
      [
        'borderline',
        'brand',
        'critical',
        'high',
        'low',
        'neutral',
        'normal',
        'stale',
        'unknown',
      ].sort(),
    );
    expect(ramps.brand['500']).toMatch(/^#[0-9a-f]{6}$/);
  });

  it('resolves both themes', () => {
    expect(themes.light.surface.base).toMatch(/^#[0-9a-f]{6}$/);
    expect(themes.dark.surface.base).toMatch(/^#[0-9a-f]{6}$/);
  });

  it('gives every clinical status an icon and both labels', () => {
    for (const status of Object.values(clinicalStatuses)) {
      expect(status.icon).not.toBe('');
      expect(status.label.en).not.toBe('');
      expect(status.label.bn).not.toBe('');
    }
  });

  it('resolves a type role per script', () => {
    const bodyBn = resolveTypeRole('body', 'bengali');
    const bodyLatin = resolveTypeRole('body', 'latin');
    expect(bodyBn.lineHeight).toBeGreaterThan(bodyLatin.lineHeight);
  });
});
