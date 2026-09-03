import { expect, test } from './fixtures';

/**
 * Signing in, in a browser.
 *
 * The unit suite proves the form and the store against a scripted fetch. What needs a
 * browser is the part in between: a real redirect with a real `?next=`, real cookies the
 * page cannot see, and the shell appearing only once the server has said who is there.
 */

test.describe('CP16 acceptance: the sign-in flow', () => {
  test('an anonymous visitor is sent to sign in, and remembers where they were going', async ({
    signedOut: page,
  }) => {
    await page.goto('/patients');

    await expect(page).toHaveURL(/\/login\?next=%2Fpatients$/);
    await expect(page.getByRole('heading', { level: 1, name: 'Sign in' })).toBeVisible();
    // No inventory of the application for whoever is at the keyboard.
    await expect(page.getByRole('navigation', { name: 'Primary' })).toHaveCount(0);
  });

  test('a wrong password says one thing and stays put', async ({ signedOut: page }) => {
    await page.goto('/login');

    await page.getByLabel(/^Employee code/).fill('E001');
    await page.getByLabel(/^Password/).fill('not the password');
    await page.getByRole('button', { name: 'Sign in' }).click();

    await expect(page.getByText('Please sign in again.')).toBeVisible();
    await expect(page).toHaveURL(/\/login$/);
    await expect(page.getByLabel(/^Password/)).toHaveValue('');
  });

  test('the right password lands where the person was going, inside the shell', async ({
    signedOut: page,
  }) => {
    await page.goto('/patients');
    await expect(page).toHaveURL(/\/login\?next=%2Fpatients$/);

    await page.getByLabel(/^Employee code/).fill('E001');
    await page.getByLabel(/^Password/).fill('correct horse battery');
    await page.getByRole('button', { name: 'Sign in' }).click();

    await expect(page).toHaveURL(/\/patients$/);
    await expect(page.getByRole('navigation', { name: 'Primary' })).toBeAttached();
    await expect(page.getByText(/Dr Test Physician/)).toBeVisible();
  });

  test('signing out returns to the sign-in page and closes the door behind', async ({
    signedIn: page,
  }) => {
    await page.goto('/dashboard');
    await expect(page.getByRole('navigation', { name: 'Primary' })).toBeAttached();

    // From here the server no longer knows this person.
    await page.route('**/v1/auth/me', (route) =>
      route.fulfill({
        status: 401,
        contentType: 'application/json',
        body: '{"error":{"code":"UNAUTHENTICATED","kind":"auth","message":"Please sign in again.","message_bn":"অনুগ্রহ করে আবার সাইন ইন করুন।"}}',
      }),
    );
    await page.getByRole('button', { name: 'Sign out' }).click();

    await expect(page).toHaveURL(/\/login$/);
    await page.goto('/dashboard');
    await expect(page).toHaveURL(/\/login\?next=%2Fdashboard$/);
  });

  test('a signed-in person who opens the sign-in page is sent on', async ({ signedIn: page }) => {
    await page.goto('/login');
    await expect(page).toHaveURL(/\/dashboard$/);
  });

  test('the form is in Bangla when the interface is', async ({ bangla: page }) => {
    // The fixture signs the person in; the form must still render for a visitor. Route
    // the session away first.
    await page.route('**/v1/auth/me', (route) =>
      route.fulfill({
        status: 401,
        contentType: 'application/json',
        body: '{"error":{"code":"UNAUTHENTICATED","kind":"auth","message":"x","message_bn":"y"}}',
      }),
    );
    await page.goto('/login');
    await expect(page.getByRole('heading', { level: 1, name: 'সাইন ইন' })).toBeVisible();
    await expect(page.getByLabel(/^কর্মী কোড/)).toBeVisible();
  });

  test('signing in leaves nothing in web storage (ADR-0010)', async ({ signedOut: page }) => {
    await page.goto('/login');
    await page.getByLabel(/^Employee code/).fill('E001');
    await page.getByLabel(/^Password/).fill('correct horse battery');
    await page.getByRole('button', { name: 'Sign in' }).click();
    await expect(page).toHaveURL(/\/dashboard$/);

    const stored = await page.evaluate(() => ({
      local: Object.keys(localStorage),
      session: Object.keys(sessionStorage),
      cookie: document.cookie,
    }));
    expect(stored.local).toEqual([]);
    expect(stored.session).toEqual([]);
    // The locale cookie is the only one script may see; a session cookie is httpOnly.
    expect(stored.cookie).not.toMatch(/dthcms\.(session|refresh)/);
  });
});
