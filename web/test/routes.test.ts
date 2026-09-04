import { existsSync, readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import {
  ROUTE_GROUPS,
  UNSHELLED_ROUTES,
  ALL_NAV_HREFS,
  PATIENT_SUBROUTES,
  patientSubroutePath,
} from '@/lib/navigation';

/**
 * CP10 acceptance criterion 1: every route group renders with the correct layout.
 *
 * A test that renders each page would prove less than it appears to. What actually goes
 * wrong is not that a page component is broken — it is that a link points at a path with
 * no page behind it, or a group gains a folder that nothing navigates to, and either can
 * survive a green test suite for months because nobody clicked that item.
 *
 * So this checks the two sides against each other: the navigation definition the sidebar
 * renders from, and the files on disk that Next routes by. Neither is allowed to contain
 * something the other does not.
 */

const appDir = join(dirname(fileURLToPath(import.meta.url)), '..', 'src', 'app');

/** Route group folders are the ones in parentheses. `verify` is a plain public path. */
function routeGroupDirectories(): string[] {
  return readdirSync(appDir, { withFileTypes: true })
    .filter((entry) => entry.isDirectory() && entry.name.startsWith('('))
    .map((entry) => entry.name);
}

describe('every navigable route has a page behind it', () => {
  for (const group of ROUTE_GROUPS) {
    describe(group.key, () => {
      it('has a layout, so the shell is applied', () => {
        expect(existsSync(join(appDir, group.directory, 'layout.tsx'))).toBe(true);
      });

      it('has an error boundary of its own', () => {
        // Per group rather than one global boundary: a failure in the research area
        // should not blank the clinical screen a physician is reading.
        expect(existsSync(join(appDir, group.directory, 'error.tsx'))).toBe(true);
      });

      for (const item of group.items) {
        it(`${item.href} exists`, () => {
          const segments = item.href.replace(/^\//, '').split('/');
          expect(existsSync(join(appDir, group.directory, ...segments, 'page.tsx'))).toBe(true);
        });
      }
    });
  }

  for (const route of UNSHELLED_ROUTES) {
    it(`${route.key} exists outside the shell`, () => {
      expect(existsSync(join(appDir, route.directory))).toBe(true);
    });
  }
});

describe('one patient’s screens', () => {
  const patientDir = join(appDir, '(clinical)', 'patients', '[id]');

  for (const route of PATIENT_SUBROUTES) {
    it(`${route.segment} exists`, () => {
      expect(existsSync(join(patientDir, route.segment, 'page.tsx'))).toBe(true);
    });
  }

  it('lists every sub-route on disk', () => {
    // The failure this catches is the one that already happened four times: a screen is
    // built, it works, and nothing anywhere names it — so it is reachable only by somebody
    // who knows the URL, and a reviewer reading the navigation cannot tell it exists.
    const onDisk = readdirSync(patientDir, { withFileTypes: true })
      .filter((entry) => entry.isDirectory())
      .map((entry) => entry.name);

    const listed = new Set(PATIENT_SUBROUTES.map((route) => route.segment));
    const orphans = onDisk.filter((name) => !listed.has(name));
    expect(orphans, `Patient screens nothing lists: ${orphans.join(', ')}`).toEqual([]);
  });

  it('builds a path with the patient in it', () => {
    expect(patientSubroutePath('p-1', 'medical-history')).toBe('/patients/p-1/medical-history');
  });
});

describe('nothing on disk is unreachable', () => {
  it('has no route group that navigation does not know about', () => {
    // The failure this catches: somebody adds a folder, ships the screens inside it, and
    // no sidebar entry ever appears. The application looks finished and one audience
    // cannot reach their area.
    const declared = new Set([
      ...ROUTE_GROUPS.map((group) => group.directory),
      ...UNSHELLED_ROUTES.map((route) => route.directory),
    ]);

    const orphans = routeGroupDirectories().filter((directory) => !declared.has(directory));
    expect(
      orphans,
      `Route groups on disk that nothing navigates to: ${orphans.join(', ')}`,
    ).toEqual([]);
  });

  it('declares the eight groups from the frontend architecture, plus the account', () => {
    // §14.9 names the first eight. If one is dropped, that is a decision, not a tidy-up.
    // `account` was added at CP17 for the person's own security settings — every role can
    // reach it, which is why it is not one of the audience areas.
    expect(ROUTE_GROUPS.map((group) => group.key).sort()).toEqual([
      'account',
      'admin',
      'clinical',
      'crm',
      'exec',
      'pharmacy',
      'qa',
      'research',
      'stations',
    ]);
  });
});

describe('paths are locale-free', () => {
  it('has no locale segment in any href', () => {
    // The decision recorded in next.config.ts. A locale in the path would mean a
    // physician sharing a link imposes their interface language on the recipient.
    for (const href of ALL_NAV_HREFS) {
      expect(href, href).not.toMatch(/^\/(en|bn)(\/|$)/);
    }
  });

  it('has no [locale] directory in the app router', () => {
    expect(existsSync(join(appDir, '[locale]'))).toBe(false);
  });
});
