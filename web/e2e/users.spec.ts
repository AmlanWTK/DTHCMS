import { PHYSICIAN, expect, test } from './fixtures';

/**
 * CP21 in a browser: the administration console.
 *
 * Three journeys — invite a colleague and read out the first password, suspend an account
 * with a reason, grant a role with the permission preview — each through the confirm
 * dialog and then the step-up dialog, both real <dialog> elements.
 */

const json = (body: unknown, status = 200) => ({
  status,
  contentType: 'application/json',
  body: JSON.stringify(body),
});

const ROLES = [
  {
    code: 'NUTRITIONIST',
    name_en: 'Clinical nutritionist',
    name_bn: 'ক্লিনিক্যাল পুষ্টিবিদ',
    is_clinical: true,
    station: 'NUTRITION',
    permissions: [
      'patient.read.demographics',
      'patient.read.clinical',
      'observation.write.nutrition',
      'observation.read.values',
      'lab.read',
    ],
  },
  {
    code: 'REGISTRATION',
    name_en: 'Registration officer',
    name_bn: 'নিবন্ধন কর্মকর্তা',
    is_clinical: true,
    station: 'REGISTRATION',
    permissions: [
      'patient.read.demographics',
      'patient.write.demographics',
      'patient.consent.record',
      'patient.consent.revoke',
      'observation.correct.request',
    ],
  },
  {
    code: 'ADMIN',
    name_en: 'System administrator',
    name_bn: 'সিস্টেম প্রশাসক',
    is_clinical: false,
    station: '',
    permissions: ['user.read', 'user.invite', 'role.grant'],
  },
];

const SECOND_FACTOR = { required: false, enrolled: false, pending: false, recovery_codes_left: 0 };

interface UserRow {
  id: string;
  employee_code: string;
  name_en: string;
  name_bn: string;
  phone: string;
  email: string;
  status: string;
  status_reason: string;
  status_since: string;
  last_login_at: string | null;
  roles: string[];
  permissions: string[];
  second_factor: typeof SECOND_FACTOR;
  created_at: string;
}

const [NUTRITIONIST, REGISTRATION] = ROLES as [(typeof ROLES)[number], (typeof ROLES)[number]];

const RINA: UserRow = {
  id: '0190a8f2-0000-7000-8000-0000000000b1',
  employee_code: 'N002',
  name_en: 'Rina Akter',
  name_bn: 'রিনা আক্তার',
  phone: '01700000002',
  email: '',
  status: 'active',
  status_reason: '',
  status_since: '2026-09-01T09:00:00Z',
  last_login_at: '2026-09-03T08:15:00Z',
  roles: ['REGISTRATION'],
  permissions: REGISTRATION.permissions,
  second_factor: SECOND_FACTOR,
  created_at: '2026-09-01T09:00:00Z',
};

const account = (user: UserRow) => ({ ...user, sessions: [], grant_history: [] });

/**
 * The fixture's physician also holds ADMIN, but wears PHYSICIAN by default — and the
 * console shows its controls for the role being worn, not the union (CP20 [R-02]).
 */
async function actAsAdmin(page: import('@playwright/test').Page) {
  await page.getByLabel('Acting as').selectOption('ADMIN');
}

test.describe('CP21: the administration console', () => {
  test('invites a colleague and hands over the first password once', async ({ signedIn: page }) => {
    let users: UserRow[] = [RINA];
    await page.route('**/v1/admin/roles', (route) => route.fulfill(json({ roles: ROLES })));
    await page.route('**/v1/auth/step-up', (route) => {
      const body = route.request().postDataJSON() as { purpose: string };
      expect(body.purpose).toBe('user.manage');
      return route.fulfill(
        json({ step_up_token: 'su-invite', purpose: body.purpose, expires_at: '' }),
      );
    });
    await page.route('**/v1/admin/users', async (route) => {
      if (route.request().method() === 'POST') {
        expect(route.request().headers()['x-step-up-token']).toBe('su-invite');
        const body = route.request().postDataJSON() as {
          employee_code: string;
          name_en: string;
          roles: string[];
          password: string;
        };
        expect(body.employee_code).toBe('N003');
        expect(body.roles).toEqual(['NUTRITIONIST']);
        expect(body.password.length).toBeGreaterThanOrEqual(12);
        const created: UserRow = {
          ...RINA,
          id: '0190a8f2-0000-7000-8000-0000000000b2',
          employee_code: body.employee_code,
          name_en: body.name_en,
          status: 'invited',
          roles: body.roles,
          permissions: NUTRITIONIST.permissions,
          last_login_at: null,
        };
        users = [...users, created];
        return route.fulfill(json(account(created), 201));
      }
      return route.fulfill(json({ users }));
    });

    await page.goto('/admin/users');
    await expect(page.getByRole('heading', { name: 'Users', level: 1 })).toBeVisible();
    await expect(page.getByRole('row', { name: /Rina Akter/ })).toContainText(
      'Registration officer',
    );
    // Wearing PHYSICIAN there is nothing to click; the console is read-only.
    await expect(page.getByRole('button', { name: 'Invite a colleague' })).toHaveCount(0);
    await actAsAdmin(page);

    await page.getByRole('button', { name: 'Invite a colleague' }).click();
    await page.getByLabel(/^Employee code/).fill('n003');
    await page.getByLabel(/^Name \(English\)/).fill('Sabbir Hossain');
    await page.getByLabel(/^Name \(Bengali\)/).fill('সাব্বির হোসেন');
    await page.getByRole('button', { name: 'Generate' }).click();
    // Nothing chosen yet: no permissions, and the form will not submit.
    await expect(page.getByText('No permissions yet')).toBeVisible();
    await expect(page.getByRole('button', { name: 'Create the account' })).toBeDisabled();

    await page.getByRole('checkbox', { name: /Clinical nutritionist/ }).check();
    // The preview is the effective-permission list of criterion 2.
    await expect(page.getByText('5 permissions in effect')).toBeVisible();
    await expect(page.getByText('observation.write.nutrition')).toBeVisible();

    await page.getByRole('button', { name: 'Create the account' }).click();

    const stepUp = page.getByRole('dialog', { name: /Confirm it is you/ });
    await expect(stepUp).toBeVisible();
    await expect(stepUp.getByText('Invite Sabbir Hossain')).toBeVisible();
    await stepUp.getByLabel(/^Authenticator code/).fill('123456');
    await stepUp.getByRole('button', { name: 'Confirm' }).click();

    await expect(
      page.getByRole('heading', { name: 'Sabbir Hossain has an account' }),
    ).toBeVisible();
    await expect(page.getByText('N003').first()).toBeVisible();
    await expect(page.getByText('Shown once')).toBeVisible();
    await expect(page.getByRole('row', { name: /Sabbir Hossain/ })).toContainText('Invited');

    await page.getByRole('button', { name: 'Done' }).click();
    await expect(page.getByText('Shown once')).toHaveCount(0);
  });

  test('suspends an account with a reason, after a step-up', async ({ signedIn: page }) => {
    let rina = RINA;
    await page.route('**/v1/admin/roles', (route) => route.fulfill(json({ roles: ROLES })));
    await page.route(`**/v1/admin/users/${RINA.id}`, (route) => route.fulfill(json(account(rina))));
    await page.route('**/v1/auth/step-up', (route) =>
      route.fulfill(json({ step_up_token: 'su-status', purpose: 'user.manage', expires_at: '' })),
    );
    await page.route(`**/v1/admin/users/${RINA.id}/status`, (route) => {
      expect(route.request().headers()['x-step-up-token']).toBe('su-status');
      const body = route.request().postDataJSON() as { status: string; reason: string };
      expect(body).toEqual({ status: 'suspended', reason: 'on leave until October' });
      rina = { ...rina, status: 'suspended', status_reason: body.reason };
      return route.fulfill(json(account(rina)));
    });

    await page.goto(`/admin/users/${RINA.id}`);
    await expect(page.getByRole('heading', { name: /Rina Akter/, level: 1 })).toBeVisible();
    await actAsAdmin(page);
    await page.getByRole('button', { name: 'Suspend' }).click();

    const confirm = page.getByRole('dialog', { name: 'Suspend Rina Akter?' });
    await expect(confirm).toBeVisible();
    // A suspension needs a reason; the button waits for one.
    await expect(confirm.getByRole('button', { name: 'Suspend' })).toBeDisabled();
    await confirm.getByLabel(/^Reason/).fill('on leave until October');
    await confirm.getByRole('button', { name: 'Suspend' }).click();

    const stepUp = page.getByRole('dialog', { name: /Confirm it is you/ });
    await expect(stepUp).toBeVisible();
    await stepUp.getByLabel(/^Authenticator code/).fill('123456');
    await stepUp.getByRole('button', { name: 'Confirm' }).click();

    await expect(page.getByText('Rina Akter is now Suspended.')).toBeVisible();
    await expect(page.getByRole('heading', { name: /Rina Akter/, level: 1 })).toContainText(
      'Suspended',
    );
    // From suspended the lifecycle offers activate and deactivate, not suspend.
    await expect(page.getByRole('button', { name: 'Activate', exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Suspend' })).toHaveCount(0);
  });

  test('grants a role with the permissions it adds shown first', async ({ signedIn: page }) => {
    let rina = RINA;
    await page.route('**/v1/admin/roles', (route) => route.fulfill(json({ roles: ROLES })));
    await page.route(`**/v1/admin/users/${RINA.id}`, (route) => route.fulfill(json(account(rina))));
    await page.route('**/v1/auth/step-up', (route) =>
      route.fulfill(json({ step_up_token: 'su-grant', purpose: 'user.manage', expires_at: '' })),
    );
    await page.route(`**/v1/admin/users/${RINA.id}/roles`, (route) => {
      expect(route.request().headers()['x-step-up-token']).toBe('su-grant');
      expect(route.request().postDataJSON()).toEqual({ role: 'NUTRITIONIST' });
      rina = {
        ...rina,
        roles: ['REGISTRATION', 'NUTRITIONIST'],
        permissions: [
          ...new Set([...REGISTRATION.permissions, ...NUTRITIONIST.permissions]),
        ].sort(),
      };
      return route.fulfill(json(account(rina)));
    });

    await page.goto(`/admin/users/${RINA.id}`);
    await expect(page.getByText('5 permissions in effect').first()).toBeVisible();
    await actAsAdmin(page);

    await page.getByText('Grant another role').click();
    const held = page.getByRole('checkbox', { name: /Registration officer/ });
    await expect(held).toBeChecked();
    await expect(held).toBeDisabled();
    await page.getByRole('checkbox', { name: /Clinical nutritionist/ }).check();
    // Two roles overlap on one permission: 5 + 5 − 1.
    await expect(page.getByText('9 permissions in effect')).toBeVisible();
    await expect(page.getByText('4 would be added')).toBeVisible();

    await page.getByRole('button', { name: 'Grant 1 role' }).click();
    const stepUp = page.getByRole('dialog', { name: /Confirm it is you/ });
    await stepUp.getByLabel(/^Authenticator code/).fill('123456');
    await stepUp.getByRole('button', { name: 'Confirm' }).click();

    await expect(page.getByText('Rina Akter now holds Clinical nutritionist.')).toBeVisible();
    await expect(page.getByText('9 permissions in effect').first()).toBeVisible();
    expect(PHYSICIAN.permissions).toContain('role.grant');
  });

  test('is in Bangla when the interface is', async ({ bangla: page }) => {
    await page.route('**/v1/admin/roles', (route) => route.fulfill(json({ roles: ROLES })));
    await page.route('**/v1/admin/users', (route) => route.fulfill(json({ users: [RINA] })));
    await page.goto('/admin/users');
    await expect(page.getByRole('heading', { level: 1, name: 'ব্যবহারকারী' })).toBeVisible();
    await expect(page.getByRole('row', { name: /রিনা আক্তার/ })).toContainText('সক্রিয়');
  });
});
