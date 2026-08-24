import { existsSync, readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import { MOBILE_ROUTES } from '../src/lib/navigation';

/**
 * The navigation definition and the disk have to agree — the same rule, and the same
 * two quiet failures, as the web shell's route test.
 */

const appDir = join(dirname(fileURLToPath(import.meta.url)), '..', 'src', 'app');

describe('every declared route has a screen behind it', () => {
  for (const route of MOBILE_ROUTES) {
    it(`${route.href} exists`, () => {
      expect(existsSync(join(appDir, route.file))).toBe(true);
    });
  }
});

describe('nothing on disk is undeclared', () => {
  it('has no screen file outside the definition', () => {
    const declared = new Set(MOBILE_ROUTES.map((route) => route.file));
    const structural = new Set(['_layout.tsx', 'index.tsx', '+not-found.tsx']);

    const found: string[] = [];
    (function walk(dir: string, prefix: string) {
      for (const entry of readdirSync(dir, { withFileTypes: true })) {
        if (entry.isDirectory()) walk(join(dir, entry.name), `${prefix}${entry.name}/`);
        else found.push(`${prefix}${entry.name}`);
      }
    })(appDir, '');

    const orphans = found.filter((file) => !declared.has(file) && !structural.has(file));
    expect(orphans, `Screens no navigation entry points at: ${orphans.join(', ')}`).toEqual([]);
  });

  it('declares the five groups from §14.10', () => {
    const groups = new Set(MOBILE_ROUTES.map((route) => route.file.split('/')[0]));
    expect([...groups].sort()).toEqual(['(auth)', '(patient)', '(queue)', '(station)', '(sync)']);
  });
});
