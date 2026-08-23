import { describe, expect, it } from 'vitest';

import {
  layout,
  resolveTypeRole,
  scriptForLanguage,
  typeRoles,
  typeSteps,
  typography,
} from '../src/index.js';

/**
 * CP09 acceptance criterion 2 is "every primitive renders correctly in Bangla and
 * English". A component test can only check that at the component level; what can be
 * checked here is the layer beneath it — that the type system itself is bilingual rather
 * than Latin with a font swap, which is the usual way this is got wrong.
 */

describe('the type scale', () => {
  it('increases monotonically', () => {
    let previous = 0;
    for (const step of typeSteps) {
      const { size } = typography.scale[step] as { size: number };
      expect(size, `${step} is not larger than the step below it`).toBeGreaterThan(previous);
      previous = size;
    }
  });

  it('uses whole pixels', () => {
    // Fractional font sizes render inconsistently across Android WebView versions, and a
    // station app that is half a pixel different between devices is one whose layout
    // testing means nothing.
    for (const step of typeSteps) {
      const { size } = typography.scale[step] as { size: number };
      expect(Number.isInteger(size), `${step} is ${size}px`).toBe(true);
    }
  });

  it('never goes below 11px', () => {
    // Below about 11px, Bengali conjuncts stop resolving into distinct shapes on a
    // 1280x800 tablet - the matra merges with the character above it.
    for (const step of typeSteps) {
      const { size } = typography.scale[step] as { size: number };
      expect(size, `${step}`).toBeGreaterThanOrEqual(11);
    }
  });
});

describe('Bengali is set differently from Latin, at every size', () => {
  it('gives Bengali more leading at every step', () => {
    for (const step of typeSteps) {
      const spec = typography.scale[step] as { lineHeight: { latin: number; bengali: number } };
      expect(
        spec.lineHeight.bengali,
        `At ${step}, Bengali leading (${spec.lineHeight.bengali}) is not greater than Latin ` +
          `(${spec.lineHeight.latin}). Bengali vowel signs sit above the headstroke and ` +
          `conjuncts hang below the baseline; at Latin leading they collide with the line above.`,
      ).toBeGreaterThan(spec.lineHeight.latin);
    }
  });

  it('widens the gap at smaller sizes', () => {
    // Proportionally less room for matras and descenders as the size drops, so the
    // multiplier has to grow rather than stay constant.
    const smallest = typography.scale[typeSteps[0] as keyof typeof typography.scale] as {
      lineHeight: { latin: number; bengali: number };
    };
    const largest = typography.scale[
      typeSteps[typeSteps.length - 1] as keyof typeof typography.scale
    ] as { lineHeight: { latin: number; bengali: number } };

    const smallGap = smallest.lineHeight.bengali - smallest.lineHeight.latin;
    const largeGap = largest.lineHeight.bengali - largest.lineHeight.latin;

    expect(smallGap).toBeGreaterThanOrEqual(largeGap);
  });

  it('resolves a role to a different line height per script', () => {
    for (const role of typeRoles) {
      const latin = resolveTypeRole(role, 'latin');
      const bengali = resolveTypeRole(role, 'bengali');

      // Roles that pin a family - clinicalValue, identifier - keep Latin metrics in both,
      // which is the point of pinning them.
      const spec = typography.role[role] as { family: string };
      if (spec.family === 'auto') {
        expect(bengali.lineHeight, `${role}`).toBeGreaterThan(latin.lineHeight);
      } else {
        expect(bengali.lineHeight, `${role} is pinned to ${spec.family}`).toBe(latin.lineHeight);
      }
    }
  });
});

describe('clinical values are set for reading, not for style', () => {
  it('renders in the Latin family whatever the interface language', () => {
    // A glucose reading must look identical in a Bengali interface and an English one.
    // Digits that change shape with the interface language are digits somebody
    // transcribes wrongly onto a paper chart.
    const inBengaliUi = resolveTypeRole('clinicalValue', scriptForLanguage.bn);
    const inEnglishUi = resolveTypeRole('clinicalValue', scriptForLanguage.en);

    expect(inBengaliUi.fontFamily).toEqual(inEnglishUi.fontFamily);
    expect(inBengaliUi.fontFamily[0]).toBe('Inter');
    expect(inBengaliUi.fontSize).toBe(inEnglishUi.fontSize);
  });

  it('uses tabular figures', () => {
    // Proportional digits in a column of results do not align, and a misaligned column is
    // one that gets misread at a glance.
    expect(resolveTypeRole('clinicalValue', 'latin').tabular).toBe(true);
  });

  it('is never smaller than body text', () => {
    const value = resolveTypeRole('clinicalValue', 'latin');
    const body = resolveTypeRole('body', 'latin');
    expect(value.fontSize).toBeGreaterThanOrEqual(body.fontSize);
  });

  it('defaults to ASCII digits in both languages', () => {
    // The open decision recorded in typography.json. Defaulted, not decided: two numeral
    // systems in circulation around a lab printout is a transcription error waiting to
    // happen, so the default is the conservative one until Dr. Nahid rules.
    expect(typography.numerals.clinicalValues).toBe('ascii');
  });
});

describe('touch targets', () => {
  it('holds a 48px floor', () => {
    // WCAG 2.5.5 and the Android guideline. A station operator taps one-handed, sometimes
    // gloved, in a hurry.
    expect(layout.size.touchTarget).toBeGreaterThanOrEqual(48);
  });

  it('keeps the largest control at least as tall as the touch target', () => {
    expect(layout.size.control.lg).toBeGreaterThanOrEqual(layout.size.touchTarget);
  });
});
