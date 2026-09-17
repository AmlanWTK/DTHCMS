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
  /** The prescription education officer, who is the only person station 11 is written for. */
  officer: Page;
  /** The same officer, in Bangla — which is the language she actually works in. */
  officerBangla: Page;
  /** The QA officer at station 10: reviews, clears, bounces — and may not override. */
  qaOfficer: Page;
  /** The same officer, in Bangla. */
  qaOfficerBangla: Page;
  /** The consultant: may override the QA gate, and may not read how often overrides happen. */
  consultant: Page;
  /** The same consultant, in Bangla. */
  consultantBangla: Page;
  /** Somebody who may read a QA review and act on nothing. */
  qaOnlooker: Page;
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
    'terminology.read',
    'history.read',
    'history.write',
    'history.confirm',
    'allergy.write',
    'patient.read.allergies',
    'counseling.template.read',
    'counseling.template.write',
    'counseling.template.publish',
    'counseling.session.read',
    'counseling.gate.override',
    // Station 11 (CP88, CP92). The read and not the write, which is the whole of the rule: the
    // consultant sees what the patient could and could not do at the last visit, and is not the
    // one who records it. `reference.read` is the dictionary every role holds (CP85).
    'education.read',
    'reference.read',
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
        'terminology.read',
        'history.read',
        'history.write',
        'history.confirm',
        'allergy.write',
        'patient.read.allergies',
        'counseling.template.read',
        'counseling.template.write',
        'counseling.template.publish',
        'counseling.session.read',
        'counseling.gate.override',
        'education.read',
        'reference.read',
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

/**
 * What `/v1/auth/me` says about the prescription education officer (CP88, CP92).
 *
 * She exists as a second fixture because station 11 has two audiences with opposite
 * relationships to it, and a picture taken as the wrong one misrepresents the clinic. The
 * physician above holds `education.read` and not `education.record`: he reads the assessment at
 * the next consultation and never records it. She holds both, and `observation.write.pro` — the
 * permission CP88 §1 moves onto her role precisely so the person whose treatment the score grades
 * is not the person asking for it.
 *
 * Her permission list is short on purpose. She is not a physician with an extra grant; she runs
 * one station, and a fixture that quietly handed her the consultant's screens would stop being
 * evidence of anything.
 */
export const EDUCATION_OFFICER = {
  id: '0190a8f2-0000-7000-8000-00000000000e',
  employee_code: 'E311',
  name_en: 'Shirin Akter',
  name_bn: 'শিরীন আক্তার',
  status: 'active',
  facility_id: '11111111-1111-4111-8111-111111111111',
  roles: ['RX_EDUCATOR'],
  permissions: [
    'patient.read.demographics',
    'patient.read.clinical',
    'observation.read.values',
    'education.read',
    'education.record',
    'observation.write.pro',
    'reference.read',
  ],
  grants: [
    {
      role: 'RX_EDUCATOR',
      permissions: [
        'patient.read.demographics',
        'patient.read.clinical',
        'observation.read.values',
        'education.read',
        'education.record',
        'observation.write.pro',
        'reference.read',
      ],
    },
  ],
  second_factor: { required: true, enrolled: true, pending: false, recovery_codes_left: 10 },
};

/**
 * The quality assurance officer at station 10 (CP83).
 *
 * She holds `qa.review`, `qa.clear` and `qa.bounce` — and **not** `qa.override`. That absence is
 * the checkpoint rather than an omission: `docs/qa-rules.md` §2 puts the override rate with
 * Quality and the grant with the prescriber, because the answer to a rising rate is a person
 * asking why and that person should not be the one granting them.
 */
export const QA_OFFICER = {
  id: '0190a8f2-0000-7000-8000-00000000000f',
  employee_code: 'E412',
  name_en: 'Shirin Akhter',
  name_bn: 'শিরীন আক্তার',
  status: 'active',
  facility_id: '11111111-1111-4111-8111-111111111111',
  roles: ['QA'],
  permissions: [
    'patient.read.demographics',
    'patient.read.clinical',
    'observation.read.values',
    'prescription.read',
    'diagnosis.read',
    'qa.review',
    'qa.clear',
    'qa.bounce',
    'reference.read',
  ],
  grants: [
    {
      role: 'QA',
      permissions: [
        'patient.read.demographics',
        'patient.read.clinical',
        'observation.read.values',
        'prescription.read',
        'diagnosis.read',
        'qa.review',
        'qa.clear',
        'qa.bounce',
        'reference.read',
      ],
    },
  ],
  second_factor: { required: true, enrolled: true, pending: false, recovery_codes_left: 10 },
};

/**
 * The consultant, holding the valve (CP83).
 *
 * `qa.override` and `qa.clear`, and **no `qa.review`** — so he can let a blocked prescription past
 * in his own name and cannot look at how often that has happened. The other half of the same
 * separation.
 */
export const QA_CONSULTANT = {
  id: '0190a8f2-0000-7000-8000-000000000010',
  employee_code: 'E001',
  name_en: 'Dr Nahid Hasan',
  name_bn: 'ডা. নাহিদ হাসান',
  status: 'active',
  facility_id: '11111111-1111-4111-8111-111111111111',
  roles: ['PHYSICIAN'],
  permissions: [
    'patient.read.demographics',
    'patient.read.clinical',
    'observation.read.values',
    'prescription.read',
    'prescription.draft',
    'diagnosis.read',
    'qa.clear',
    'qa.override',
    'reference.read',
  ],
  grants: [
    {
      role: 'PHYSICIAN',
      permissions: [
        'patient.read.demographics',
        'patient.read.clinical',
        'observation.read.values',
        'prescription.read',
        'prescription.draft',
        'diagnosis.read',
        'qa.clear',
        'qa.override',
        'reference.read',
      ],
    },
  ],
  second_factor: { required: true, enrolled: true, pending: false, recovery_codes_left: 10 },
};

/**
 * A reader who may look at a QA review and do nothing about it.
 *
 * The CP92 defect, in this station's shape: the screen must hand this person no control at all,
 * rather than a form whose every button answers 403.
 */
export const QA_ONLOOKER = {
  ...QA_OFFICER,
  id: '0190a8f2-0000-7000-8000-000000000011',
  employee_code: 'E455',
  name_en: 'Rina Parvin',
  name_bn: 'রিনা পারভীন',
  permissions: ['patient.read.demographics', 'prescription.read', 'qa.review', 'reference.read'],
  grants: [
    {
      role: 'QA',
      permissions: [
        'patient.read.demographics',
        'prescription.read',
        'qa.review',
        'reference.read',
      ],
    },
  ],
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
export async function mockSession(
  page: Page,
  // A structural type rather than a union of the literals: CP83 added three more sessions, and a
  // union that had to be extended for each is a union somebody widens by guessing.
  current: { id: string; employee_code: string; permissions: string[] } | null,
) {
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

  officer: async ({ page }, use) => {
    await mockSession(page, EDUCATION_OFFICER);
    await use(page);
  },

  officerBangla: async ({ page }, use) => {
    await page
      .context()
      .addCookies([{ name: LOCALE_COOKIE, value: 'bn', url: 'http://127.0.0.1:3100' }]);
    await mockSession(page, EDUCATION_OFFICER);
    await use(page);
  },

  qaOfficer: async ({ page }, use) => {
    await mockSession(page, QA_OFFICER);
    await use(page);
  },

  qaOfficerBangla: async ({ page }, use) => {
    await page
      .context()
      .addCookies([{ name: LOCALE_COOKIE, value: 'bn', url: 'http://127.0.0.1:3100' }]);
    await mockSession(page, QA_OFFICER);
    await use(page);
  },

  consultant: async ({ page }, use) => {
    await mockSession(page, QA_CONSULTANT);
    await use(page);
  },

  consultantBangla: async ({ page }, use) => {
    await page
      .context()
      .addCookies([{ name: LOCALE_COOKIE, value: 'bn', url: 'http://127.0.0.1:3100' }]);
    await mockSession(page, QA_CONSULTANT);
    await use(page);
  },

  qaOnlooker: async ({ page }, use) => {
    await mockSession(page, QA_ONLOOKER);
    await use(page);
  },
});

export { expect } from '@playwright/test';
