import { PHYSICIAN, UNAUTHENTICATED, expect, mockSession, test } from './fixtures';

/**
 * CP17 in a browser: the second step of sign-in, and the security page from nothing to
 * enrolled, with a real <dialog> for the step-up.
 */

const CODES = [
  'K7QM-3XZP-A9BD-2NWE',
  'H4TR-8VLC-Q2MS-7YPA',
  'B9NX-5WKD-T3FR-6JQZ',
  'M2PL-7ACV-R8YH-4KTD',
  'X6WQ-2NJB-F5MR-9SLC',
  'D3KT-9RPZ-L7VA-2HNQ',
  'Q8FM-4XCW-J6TB-3RLD',
  'T5YA-6QHN-M9KP-8ZVX',
  'R2CB-8LTD-W4NJ-5MQF',
  'N7VP-3MKX-A6QR-9TBH',
];

const json = (body: unknown, status = 200) => ({
  status,
  contentType: 'application/json',
  body: JSON.stringify(body),
});

const notEnrolled = {
  ...PHYSICIAN,
  second_factor: { required: true, enrolled: false, pending: false, recovery_codes_left: 0 },
};

test.describe('CP17: the second step of sign-in', () => {
  test('asks for a code after the password, and signs in with it', async ({ signedOut: page }) => {
    await page.route('**/v1/auth/login', (route) =>
      route.fulfill(json({ challenge: 'ch-1', expires_at: '2099-01-01T00:00:00Z' }, 202)),
    );
    await page.route('**/v1/auth/login/second-factor', async (route) => {
      const body = route.request().postDataJSON() as { code?: string };
      if (body.code === '123456') {
        await page.route('**/v1/auth/me', (r) => r.fulfill(json(PHYSICIAN)));
        return route.fulfill(json({ access_token: 'e2e', expires_at: '', user: PHYSICIAN }));
      }
      return route.fulfill(json(UNAUTHENTICATED, 401));
    });

    await page.goto('/login');
    await page.getByLabel(/^Employee code/).fill('E001');
    await page.getByLabel(/^Password/).fill('correct horse battery');
    await page.getByRole('button', { name: 'Sign in' }).click();

    await expect(
      page.getByRole('heading', { name: 'Enter your authenticator code' }),
    ).toBeVisible();
    await expect(page).toHaveURL(/\/login$/);

    // Wrong first, then right.
    await page.getByLabel(/^Authenticator code/).fill('000000');
    await page.getByRole('button', { name: 'Continue' }).click();
    await expect(page.getByText('That code was not accepted.')).toBeVisible();

    await page.getByLabel(/^Authenticator code/).fill('123456');
    await page.getByRole('button', { name: 'Continue' }).click();
    await expect(page).toHaveURL(/\/dashboard$/);
    await expect(page.getByRole('navigation', { name: 'Primary' })).toBeAttached();
  });
});

test.describe('CP17: the security page', () => {
  test('a physician without an authenticator is nudged, and can enrol', async ({ page }) => {
    await mockSession(page, notEnrolled);
    await page.route('**/v1/auth/second-factor/enrol', (route) =>
      route.fulfill(
        json({
          secret: 'JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP',
          otpauth_uri:
            'otpauth://totp/DTHCMS:E001?secret=JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP&issuer=DTHCMS&algorithm=SHA1&digits=6&period=30',
        }),
      ),
    );
    await page.route('**/v1/auth/second-factor/confirm', async (route) => {
      await page.route('**/v1/auth/me', (r) => r.fulfill(json(PHYSICIAN)));
      return route.fulfill(json({ recovery_codes: CODES }));
    });

    // The nudge is on every shelled screen.
    await page.goto('/dashboard');
    await expect(page.getByText('Set up your authenticator app')).toBeVisible();
    await page.getByRole('link', { name: 'Set up now' }).click();
    await expect(page).toHaveURL(/\/account\/security$/);

    await page.getByRole('button', { name: 'Set up authenticator' }).click();
    await expect(
      page.getByRole('img', { name: 'QR code for your authenticator app' }),
    ).toBeVisible();

    await page.getByLabel(/^Authenticator code/).fill('123456');
    await page.getByRole('button', { name: 'Confirm and turn on' }).click();

    const list = page.getByRole('list', { name: 'Recovery codes' });
    await expect(list).toBeVisible();
    await expect(list.getByRole('listitem')).toHaveCount(10);
    await expect(page.getByText('These are shown once')).toBeVisible();

    await page.getByRole('button', { name: 'I have saved them' }).click();
    await expect(page.getByText('K7QM-3XZP-A9BD-2NWE')).toHaveCount(0);
    await expect(page.getByText('On', { exact: true })).toBeVisible();
    // The nudge is gone now.
    await expect(page.getByText('Set up your authenticator app')).toHaveCount(0);
  });

  test('turning the factor off asks for a step-up in a real dialog', async ({ signedIn: page }) => {
    await page.route('**/v1/auth/step-up', (route) =>
      route.fulfill(
        json({ step_up_token: 'su-1', purpose: 'second_factor.disable', expires_at: '' }),
      ),
    );
    await page.route('**/v1/auth/second-factor/disable', async (route) => {
      expect(route.request().headers()['x-step-up-token']).toBe('su-1');
      await page.route('**/v1/auth/me', (r) => r.fulfill(json(notEnrolled)));
      return route.fulfill({ status: 204 });
    });

    await page.goto('/account/security');
    await expect(page.getByText('On', { exact: true })).toBeVisible();
    await page.getByRole('button', { name: 'Turn off' }).click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText('Confirm it is you')).toBeVisible();
    // Focus is inside the dialog, which is what makes it modal for a keyboard user.
    await expect(dialog.getByLabel(/^Authenticator code/)).toBeFocused();

    await dialog.getByLabel(/^Authenticator code/).fill('654321');
    await dialog.getByRole('button', { name: 'Confirm' }).click();

    await expect(dialog).toBeHidden();
    await expect(
      page.getByText('The authenticator has been turned off for this account.'),
    ).toBeVisible();
    await expect(page.getByText('Off', { exact: true })).toBeVisible();
  });

  test('the security page is in Bangla when the interface is', async ({ bangla: page }) => {
    await page.goto('/account/security');
    await expect(page.getByRole('heading', { level: 1, name: 'নিরাপত্তা' })).toBeVisible();
    await expect(page.getByRole('heading', { level: 2, name: 'অথেন্টিকেটর অ্যাপ' })).toBeVisible();
  });
});
