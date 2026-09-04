import { test as base, type Page } from '@playwright/test';

import { LOCALE_COOKIE } from '../../src/lib/i18n/config';

/**
 * Fixtures for the browser suite.
 *
 * Every spec that reaches a shelled screen needs a session, and from CP16 a spec opens
 * with "given a signed-in physician looking at a patient". The setup is four steps nobody
 * should retype, so it is here.
 *
 * Kept deliberately thin. A page-object layer that mirrors every component is a second
 * application to maintain, and it rots the moment somebody renames a heading. These are
 * the things that are genuinely awkward to do inline: switching language, and standing in
 * for a session that does not exist yet.
 */

export interface DthcmsFixtures {
  /** The application in Bangla, as half the clinic's staff will see it. */
  bangla: Page;
  /** A signed-in physician, as far as the browser can tell. */
  signedIn: Page;
  /** A visitor the server does not recognise. */
  signedOut: Page;
}

/** What `/v1/auth/me` says about the fixture's physician. */
export const PHYSICIAN = {
  id: '0190a8f2-0000-7000-8000-00000000000a',
  employee_code: 'E001',
  name_en: 'Dr Test Physician',
  name_bn: 'ডা. পরীক্ষা চিকিৎসক',
  status: 'active',
  facility_id: '11111111-1111-4111-8111-111111111111',
  roles: ['PHYSICIAN', 'ADMIN'],
  permissions: [
    'patient.read.demographics',
    'prescription.sign',
    'report.read.operational',
    'user.read',
    'user.invite',
    'user.suspend',
    'user.deactivate',
    'user.credential.reset',
    'role.grant',
    'role.revoke',
    'device.enroll',
    'device.revoke',
    'audit.read',
    'patient.read.clinical',
    'board.read',
    'visit.reroute',
    'observation.read.values',
  ],
  grants: [
    {
      role: 'PHYSICIAN',
      permissions: [
        'patient.read.demographics',
        'patient.read.clinical',
        'prescription.sign',
        'report.read.operational',
        'audit.read',
        'board.read',
        'visit.reroute',
        'observation.read.values',
      ],
    },
    {
      role: 'ADMIN',
      permissions: [
        'user.read',
        'user.invite',
        'user.suspend',
        'user.deactivate',
        'user.credential.reset',
        'role.grant',
        'role.revoke',
        'device.enroll',
        'device.revoke',
        'audit.read',
      ],
    },
  ],
  second_factor: { required: true, enrolled: true, pending: false, recovery_codes_left: 10 },
};

export const UNAUTHENTICATED = {
  error: {
    code: 'UNAUTHENTICATED',
    kind: 'auth',
    message: 'Please sign in again.',
    message_bn: 'অনুগ্রহ করে আবার সাইন ইন করুন।',
    correlation_id: 'req_e2e',
  },
};

const json = (body: unknown, status = 200) => ({
  status,
  contentType: 'application/json',
  body: JSON.stringify(body),
});

/**
 * Stands in for the API's session endpoints.
 *
 * The browser suite runs without a backend, on purpose: what it checks is navigation, the
 * stylesheet and the response headers, and those do not need a database. What it does need
 * is for the shell to believe somebody is signed in, and the shell believes whatever
 * `/v1/auth/me` tells it. So that is what is answered here — nothing more, so that a spec
 * which reaches for a clinical endpoint fails loudly rather than getting an empty 200.
 */
export async function mockSession(page: Page, current: typeof PHYSICIAN | null) {
  await page.route('**/v1/auth/me', (route) =>
    route.fulfill(current ? json(current) : json(UNAUTHENTICATED, 401)),
  );
  await page.route('**/v1/auth/refresh', (route) => route.fulfill(json(UNAUTHENTICATED, 401)));
  await page.route('**/v1/auth/login', async (route) => {
    const body = route.request().postDataJSON() as { employee_code?: string; password?: string };
    if (
      body.employee_code === PHYSICIAN.employee_code &&
      body.password === 'correct horse battery'
    ) {
      // A signed-in session from here on.
      await page.route('**/v1/auth/me', (r) => r.fulfill(json(PHYSICIAN)));
      return route.fulfill(
        json({ access_token: 'e2e', expires_at: '2099-01-01T00:00:00Z', user: PHYSICIAN }),
      );
    }
    return route.fulfill(json(UNAUTHENTICATED, 401));
  });
  await page.route('**/v1/auth/logout', (route) => route.fulfill({ status: 204 }));
  // The administrator alarm polls this from every shelled screen (CP22). Quiet unless a
  // spec says otherwise — a later page.route wins.
  await page.route('**/v1/audit/alerts', (route) => route.fulfill(json({ alerts: [] })));
}

export const test = base.extend<DthcmsFixtures>({
  bangla: async ({ page }, use) => {
    /*
     * The locale lives on the person, not in the URL (docs/web-shell.md §1), so there is
     * no `/bn` prefix to navigate to. It is a cookie, written by a server action.
     */
    // The cookie name is the application's, imported rather than retyped: this fixture
    // carried a stale name for a whole checkpoint before anything used it.
    await page
      .context()
      .addCookies([{ name: LOCALE_COOKIE, value: 'bn', url: 'http://127.0.0.1:3100' }]);
    await mockSession(page, PHYSICIAN);
    await use(page);
  },

  signedIn: async ({ page }, use) => {
    await mockSession(page, PHYSICIAN);
    await use(page);
  },

  signedOut: async ({ page }, use) => {
    await mockSession(page, null);
    await use(page);
  },
});

export { expect } from '@playwright/test';
