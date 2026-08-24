import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

/**
 * The shell stylesheet, checked as data.
 *
 * Same discipline as @dthcms/ui, for the same reason: jsdom has no layout engine, so a
 * test cannot ask how wide the sidebar rendered. What it can do is read the rules that
 * produce it, and catch the thing that actually goes wrong — a value written as a literal,
 * which then does not follow the theme, does not invert for dark mode and does not turn
 * black for print.
 *
 * The second test is the one worth having. Every `var(--x)` the shell uses must be a
 * variable the token build actually emits. `var(--colour-text-primary)` is a spelling
 * mistake CSS does not report: the declaration is simply dropped, and the text renders in
 * whatever it inherits.
 */

const webRoot = join(dirname(fileURLToPath(import.meta.url)), '..');
const css = readFileSync(join(webRoot, 'src', 'styles', 'globals.css'), 'utf8');
const rules = css.replace(/\/\*[\s\S]*?\*\//g, '');

const tokensCss = readFileSync(
  join(webRoot, '..', 'packages', 'design-tokens', 'dist', 'tokens.css'),
  'utf8',
);

/**
 * Variables the shell defines for itself.
 *
 * A sidebar width is a property of this application's layout, not of the design language
 * shared with the mobile app and the print pipeline, so it does not belong in the token
 * package. `--border-thin` is referenced with a fallback, the same idiom @dthcms/ui uses.
 */
const SHELL_OWNED = ['--shell-sidebar-width', '--shell-content-max', '--shell-panel-max'];
const WITH_FALLBACK = ['--border-thin'];

describe('every value comes from a token', () => {
  it('contains no hex colours', () => {
    const found = rules.match(/#[0-9a-fA-F]{3,8}\b/g) ?? [];
    expect(found, `Found: ${found.join(', ')}`).toEqual([]);
  });

  it('contains no rgb, hsl or oklch literals', () => {
    const found = rules.match(/\b(rgb|rgba|hsl|hsla|oklch|oklab)\(/g) ?? [];
    expect(found, `Found: ${found.join(', ')}`).toEqual([]);
  });

  it('contains no raw pixel sizes', () => {
    // rem everywhere, so the shell scales with the operating system's text size. A
    // physician who has set their tablet to large text has done so for a reason.
    const found = (rules.match(/(?<![\w-])-?\d+(\.\d+)?px/g) ?? []).filter(
      (value) => value !== '1px' && value !== '-1px',
    );
    expect(found, `Found: ${found.join(', ')}`).toEqual([]);
  });
});

describe('every variable the shell reads actually exists', () => {
  const referenced = [...new Set((rules.match(/var\(--[\w-]+/g) ?? []).map((v) => v.slice(4)))];

  it('references some', () => {
    expect(referenced.length).toBeGreaterThan(20);
  });

  for (const name of referenced) {
    it(`${name} is defined`, () => {
      if (SHELL_OWNED.includes(name) || WITH_FALLBACK.includes(name)) {
        expect(rules.includes(`${name}:`) || WITH_FALLBACK.includes(name)).toBe(true);
        return;
      }

      expect(
        tokensCss.includes(`${name}:`),
        `${name} is not emitted by the token build. A misspelt custom property is not an ` +
          `error in CSS — the declaration is dropped and the element renders with whatever ` +
          `it inherits, which usually looks almost right.`,
      ).toBe(true);
    });
  }
});

describe('the shell knows about both scripts', () => {
  it('adjusts type for Bangla rather than only swapping the font', () => {
    // Setting the family without the leading is the mistake the token pairing exists to
    // prevent; a rule here that sets one alone would reintroduce it locally.
    expect(rules).toContain("[lang='bn']");
    expect(rules).toMatch(/\[lang='bn'\][\s\S]*--leading-2xl-bengali/);
  });

  it('drops uppercasing and letter-spacing for Bangla', () => {
    // Bengali has no letter case, so text-transform does nothing, and extra tracking only
    // pushes conjuncts apart.
    expect(rules).toMatch(/\[lang='bn'\][\s\S]*text-transform:\s*none/);
  });
});

describe('print', () => {
  it('removes the shell furniture', () => {
    // On a monochrome laser in a clinic, every millimetre of ink spent on a sidebar is a
    // millimetre not spent on the record.
    expect(rules).toMatch(/@media print[\s\S]*\.app-sidebar/);
  });
});

describe('touch targets', () => {
  it('sizes navigation links to the touch target token', () => {
    expect(rules).toMatch(/\.app-nav-link\s*\{[^}]*min-height:\s*var\(--touch-target\)/);
  });
});
