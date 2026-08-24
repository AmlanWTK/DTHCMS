import { readFileSync, readdirSync, statSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import { androidElevation, theme } from '@dthcms/design-tokens/nativewind';

/**
 * The token pipeline, from the mobile side.
 *
 * CP09's build test asserts the three artefacts carry the same values; this asserts the
 * mobile app actually holds the discipline — semantic roles exist for both schemes, and
 * no screen smuggles in a colour of its own.
 */

const srcDir = join(dirname(fileURLToPath(import.meta.url)), '..', 'src');

describe('the generated theme carries what the shell needs', () => {
  it('has the semantic roles for both colour schemes', () => {
    for (const scheme of ['light', 'dark'] as const) {
      const roles = theme.colors[scheme];
      expect(roles.surface.sunken, `${scheme} surface`).toMatch(/^#/);
      expect(roles.text.primary, `${scheme} text`).toMatch(/^#/);
      expect(roles.border.control, `${scheme} border`).toMatch(/^#/);
      expect(roles.brand.solid, `${scheme} brand`).toMatch(/^#/);
    }
  });

  it('has every clinical status in both schemes', () => {
    for (const [name, perScheme] of Object.entries(theme.colors.status)) {
      expect(perScheme.light.solid, `${name} light`).toMatch(/^#/);
      expect(perScheme.dark.solid, `${name} dark`).toMatch(/^#/);
    }
  });

  it('carries the touch target and elevation the screens size themselves by', () => {
    expect(theme.size.touchTarget).toBe(48);
    expect(androidElevation.raised).toBeGreaterThan(androidElevation.flat);
  });
});

describe('no colour literal in mobile source', () => {
  it('contains no hex colour outside the token pipeline', () => {
    // The same law styles.test.ts enforces on the web stylesheets. A hex code in a
    // screen is a value that will not follow the theme and cannot be checked by the
    // contrast contract.
    const offenders: string[] = [];
    (function walk(dir: string) {
      for (const entry of readdirSync(dir)) {
        const path = join(dir, entry);
        if (statSync(path).isDirectory()) walk(path);
        else if (/\.(tsx?|css)$/.test(entry)) {
          const source = readFileSync(path, 'utf8');
          if (/#[0-9a-fA-F]{3,8}\b/.test(source)) offenders.push(path.slice(srcDir.length + 1));
        }
      }
    })(srcDir);

    expect(offenders, `Hex colours found in: ${offenders.join(', ')}`).toEqual([]);
  });
});
