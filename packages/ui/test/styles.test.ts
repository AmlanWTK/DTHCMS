import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import { clinicalStatusNames } from '@dthcms/design-tokens';

/**
 * The stylesheet, checked as data.
 *
 * jsdom has no layout engine, so a test cannot ask how tall a rendered button is. What it
 * can do is read the stylesheet and assert the rules that produce the height — which
 * catches the thing that actually goes wrong. Nobody ships a button that is 30px tall on
 * purpose; they ship one whose min-height was written as a literal and then diverged from
 * the token when the token changed.
 */

const css = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), '..', 'src', 'styles.css'),
  'utf8',
);

/** The stylesheet with comments removed, so prose about colours is not mistaken for one. */
const rules = css.replace(/\/\*[\s\S]*?\*\//g, '');

describe('every value comes from a token', () => {
  it('contains no hex colours', () => {
    const found = rules.match(/#[0-9a-fA-F]{3,8}\b/g) ?? [];
    expect(
      found,
      `A hex colour in a component stylesheet is a value that will not follow the theme, ` +
        `will not invert for dark mode, and will not turn black for print. Found: ${found.join(', ')}`,
    ).toEqual([]);
  });

  it('contains no rgb, hsl or oklch literals', () => {
    const found = rules.match(/\b(rgb|rgba|hsl|hsla|oklch|oklab)\(/g) ?? [];
    expect(found, `Found: ${found.join(', ')}`).toEqual([]);
  });

  it('contains no raw pixel sizes beyond the two that cannot be tokens', () => {
    // 1px is the fallback in `var(--border-thin, 1px)` and -1px/1px belong to the
    // visually-hidden clip, which is a fixed technique rather than a design decision.
    const found = (rules.match(/(?<![\w-])-?\d+(\.\d+)?px/g) ?? []).filter(
      (value) => value !== '1px' && value !== '-1px',
    );
    expect(
      found,
      `A pixel literal is a value that does not scale with the OS text size and does not ` +
        `follow the spacing scale. Found: ${found.join(', ')}`,
    ).toEqual([]);
  });
});

describe('touch targets', () => {
  it('sizes the default button to the touch target token', () => {
    // WCAG 2.5.5 and the Android guideline. A station operator taps one-handed, sometimes
    // gloved, in a hurry.
    expect(rules).toMatch(/\.dthc-button--md\s*\{[^}]*min-height:\s*var\(--touch-target\)/);
  });

  it('sizes every text control to the touch target token', () => {
    expect(rules).toMatch(
      /\.dthc-input,\s*\.dthc-select\s*\{[^}]*min-height:\s*var\(--touch-target\)/,
    );
  });

  it('leaves the small button below the target, which is why it is documented', () => {
    // Asserted rather than ignored: `sm` is genuinely smaller, it is for pointer-driven
    // density, and a future change that quietly made it the default would be caught by
    // the Button tests rather than here.
    expect(rules).toMatch(/\.dthc-button--sm\s*\{[^}]*min-height:\s*var\(--space-8\)/);
  });
});

describe('clinical statuses are all styled', () => {
  for (const status of clinicalStatusNames) {
    it(`${status} has a subtle and a solid rule`, () => {
      // A status with a token but no rule renders as an unstyled pill: readable, wrong
      // colour, and indistinguishable from every other unstyled status.
      expect(rules, `${status} subtle`).toContain(`.dthc-pill--subtle[data-status='${status}']`);
      expect(rules, `${status} solid`).toContain(`.dthc-pill--solid[data-status='${status}']`);
      expect(rules, `${status} alert`).toContain(`.dthc-alert[data-status='${status}']`);
    });
  }
});

describe('motion', () => {
  it('stops looping animations under reduced motion', () => {
    // The token stylesheet collapses transition durations, which is not enough: a spinner
    // completing a revolution every millisecond is worse than one that does not spin.
    expect(rules).toMatch(/prefers-reduced-motion:\s*reduce/);
    expect(rules).toMatch(/prefers-reduced-motion[\s\S]*animation:\s*none/);
  });
});

describe('focus', () => {
  it('uses focus-visible rather than focus', () => {
    // :focus leaves a ring behind after a touch, where it reads as "still selected".
    expect(rules).toContain(':focus-visible');
  });

  it('draws the ring from the token', () => {
    expect(rules).toMatch(/outline:\s*var\(--focus-ring\)\s*solid\s*var\(--color-border-focus\)/);
  });
});
