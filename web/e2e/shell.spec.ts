import { PHYSICIAN, expect, mockSession, test } from './fixtures';

import { ROUTE_GROUPS, UNSHELLED_ROUTES } from '../src/lib/navigation';

/**
 * The shell, in a browser.
 *
 * What this suite is for, and what it deliberately leaves to the unit tests: anything
 * that can be checked without a browser is checked without one, because those tests run
 * in two seconds on every save. What is here needs a real navigation, a real stylesheet,
 * or a real response header.
 */

const ALL_HREFS = ROUTE_GROUPS.flatMap((group) => group.items.map((item) => item.href));

test.describe('acceptance criterion 1: every route group renders with its layout', () => {
  for (const href of ALL_HREFS) {
    test(`${href} renders inside the shell`, async ({ signedIn: page }) => {
      await page.goto(href);

      await expect(page.getByRole('navigation', { name: 'Primary' })).toBeAttached();
      await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
      await expect(page.getByRole('link', { name: 'Skip to content' })).toBeAttached();
    });
  }

  for (const route of UNSHELLED_ROUTES) {
    test(`${route.href} renders without the shell`, async ({ signedOut: page }) => {
      // The signed-out and public pages must not show navigation. A sidebar full of areas
      // the reader cannot reach is an inventory of the application handed to whoever is
      // at the keyboard.
      await page.goto(route.href);
      await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
      await expect(page.getByRole('navigation', { name: 'Primary' })).toHaveCount(0);
    });
  }

  test('the root path lands somewhere real', async ({ signedIn: page }) => {
    await page.goto('/');
    await expect(page).toHaveURL(/\/dashboard$/);
  });

  test('an address that does not exist says so', async ({ signedIn: page }) => {
    await page.goto('/not-a-real-page');
    await expect(page.getByRole('heading', { name: 'This page does not exist' })).toBeVisible();
  });
});

test.describe('acceptance criterion 2: language switching is instant and complete', () => {
  test('changes the whole shell, and the document language with it', async ({ signedIn: page }) => {
    await page.goto('/dashboard');
    await expect(page.locator('html')).toHaveAttribute('lang', 'en');
    await expect(page.getByRole('link', { name: 'Patients' })).toBeVisible();

    await page.getByRole('button', { name: /বাংলা/ }).click();

    await expect(page.locator('html')).toHaveAttribute('lang', 'bn');
    await expect(page.getByRole('link', { name: 'রোগী' })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Patients' })).toHaveCount(0);
  });

  test('survives a navigation and a reload', async ({ signedIn: page }) => {
    // The failure this catches: language held in client state only, so the first
    // server-rendered paint of the next page is in the wrong language.
    await page.goto('/dashboard');
    await page.getByRole('button', { name: /বাংলা/ }).click();
    await expect(page.locator('html')).toHaveAttribute('lang', 'bn');

    await page.goto('/patients');
    await expect(page.locator('html')).toHaveAttribute('lang', 'bn');

    await page.reload();
    await expect(page.locator('html')).toHaveAttribute('lang', 'bn');
  });

  test('leaves no English behind on a translated screen', async ({ signedIn: page }) => {
    await page.goto('/dashboard');
    await page.getByRole('button', { name: /বাংলা/ }).click();
    await expect(page.locator('html')).toHaveAttribute('lang', 'bn');

    // The sidebar is where an untranslated string would be most visible and least
    // noticed by whoever added it.
    const nav = page.getByRole('navigation', { name: 'প্রধান' });
    await expect(nav).toBeVisible();
    await expect(nav).not.toContainText('Dashboard');
    await expect(nav).not.toContainText('Patients');
  });
});

test.describe('acceptance criterion 3: an unhandled error is friendly, bilingual and traceable', () => {
  test('shows the boundary with a reference the operator can quote', async ({ signedIn: page }) => {
    await page.goto('/error-probe');

    await expect(page.getByText('Something went wrong')).toBeVisible();

    // The reference is the whole point: without it a support conversation starts with
    // "roughly when?". Located by the element that holds it rather than by the word
    // "Reference", which also appears in the sentence telling the operator to quote it.
    const reference = page.locator('.dthc-state__id');
    await expect(reference).toBeVisible();
    await expect(reference).not.toBeEmpty();
  });

  test('keeps the shell around it, so the operator is not stranded', async ({ signedIn: page }) => {
    // A route-group boundary, not the global one. The navigation should still work — the
    // clinical area failed, not the application.
    await page.goto('/error-probe');
    await expect(page.getByRole('navigation', { name: 'Primary' })).toBeAttached();
  });

  test('renders in Bangla when the interface is in Bangla', async ({ signedIn: page }) => {
    await page.goto('/dashboard');
    await page.getByRole('button', { name: /বাংলা/ }).click();
    // The click starts a server action; navigating before it lands races the cookie
    // write and intermittently gets the English boundary. Waiting on the attribute the
    // action changes is the deterministic signal that it finished.
    await expect(page.locator('html')).toHaveAttribute('lang', 'bn');
    await page.goto('/error-probe');
    await expect(page.getByText('কিছু একটা সমস্যা হয়েছে')).toBeVisible();
  });

  test('the probe is not something an operator can reach by clicking', async ({
    signedIn: page,
  }) => {
    /*
     * The route answers at all only because playwright.config.ts sets
     * DTHCMS_ENABLE_ERROR_PROBE for the server it starts; in a real deployment it is a
     * 404. What this asserts is the other half: it is in no menu, so nobody arrives there
     * by accident even where the flag is on.
     */
    await page.goto('/dashboard');
    const nav = page.getByRole('navigation', { name: 'Primary' });
    await expect(nav.getByRole('link', { name: /probe/i })).toHaveCount(0);
    await expect(nav.locator('a[href*="error-probe"]')).toHaveCount(0);
  });
});

test.describe('the shell works from the keyboard', () => {
  test('the first tab stop skips the navigation', async ({ signedIn: page }) => {
    await page.goto('/dashboard');
    // The shell mounts once the server has confirmed the session; before that there is a
    // skeleton with nothing to focus.
    await expect(page.getByRole('navigation', { name: 'Primary' })).toBeAttached();
    await page.keyboard.press('Tab');
    await expect(page.getByRole('link', { name: 'Skip to content' })).toBeFocused();
  });
});

test.describe('the policy does not block the application it protects', () => {
  test('makes no request the browser refuses', async ({ signedIn: page }) => {
    /*
     * The check that found a real defect. `connect-src 'self'` looked obviously correct
     * and blocked every call to the API in local development, where the Go service is on
     * its own port. Nothing in the unit suite runs a browser, so the feature failed on a
     * developer's machine and passed every test.
     */
    const refusals: string[] = [];
    page.on('console', (message) => {
      if (/Content Security Policy/i.test(message.text())) refusals.push(message.text());
    });

    await page.goto('/dashboard');
    await page.waitForLoadState('networkidle');

    expect(refusals, refusals.join('\n')).toEqual([]);
  });

  test('serves the tab icon it declares', async ({ signedIn: page }) => {
    const response = await page.request.get('/icon.svg');
    expect(response.status()).toBe(200);
  });
});

test.describe('native controls follow the theme', () => {
  test('the document and the role select compute color-scheme: dark under a dark OS', async ({
    browser,
  }) => {
    /*
     * The tokens only recolour what the page draws itself. The native select dropdown is
     * painted by the browser from the element's computed color-scheme — and when the
     * token build forgot to declare it, a dark interface got a white dropdown whose
     * options inherited near-white text. Found by Amlan reviewing the role switcher.
     *
     * Asserted on the computed style rather than on a screenshot, because the popup
     * itself is OS chrome no DOM assertion can reach; the computed value is the input
     * the browser paints it from.
     */
    const context = await browser.newContext({ colorScheme: 'dark' });
    const page = await context.newPage();
    await mockSession(page, PHYSICIAN);
    await page.goto('/dashboard');
    await expect(page.getByRole('navigation', { name: 'Primary' })).toBeAttached();

    const schemes = await page.evaluate(() => ({
      document: getComputedStyle(document.documentElement).colorScheme,
      select: getComputedStyle(document.querySelector('select')!).colorScheme,
    }));

    expect(schemes.document).toBe('dark');
    expect(schemes.select).toBe('dark');
    await context.close();
  });

  test('and light under a light OS', async ({ browser }) => {
    const context = await browser.newContext({ colorScheme: 'light' });
    const page = await context.newPage();
    await page.goto('/dashboard');
    expect(await page.evaluate(() => getComputedStyle(document.documentElement).colorScheme)).toBe(
      'light',
    );
    await context.close();
  });
});

test.describe('security headers', () => {
  test('sets a nonce-based policy with strict-dynamic, and no unsafe-eval', async ({
    signedIn: page,
  }) => {
    const response = await page.goto('/dashboard');
    const csp = response?.headers()['content-security-policy'] ?? '';

    expect(csp).toContain("'strict-dynamic'");
    expect(csp).toMatch(/'nonce-[A-Za-z0-9+/=]+'/);
    expect(csp).toContain("frame-ancestors 'none'");
    expect(csp).toContain("form-action 'self'");
    // The development relaxation must not survive into a production build.
    expect(csp).not.toContain('unsafe-eval');
  });

  test('gives every response a fresh nonce', async ({ signedIn: page }) => {
    const first = (await page.goto('/dashboard'))?.headers()['content-security-policy'] ?? '';
    const second = (await page.goto('/patients'))?.headers()['content-security-policy'] ?? '';
    expect(first).not.toBe(second);
  });

  test('sets the constant headers', async ({ signedIn: page }) => {
    const headers = (await page.goto('/dashboard'))?.headers() ?? {};
    expect(headers['x-content-type-options']).toBe('nosniff');
    expect(headers['referrer-policy']).toBe('strict-origin-when-cross-origin');
    expect(headers['x-frame-options']).toBe('DENY');
  });
});

test.describe('ADR-0010: nothing is kept in web storage', () => {
  test('leaves localStorage and sessionStorage untouched', async ({ signedIn: page }) => {
    // The ESLint rule stops it being written. This checks that nothing arrives there by
    // way of a dependency either.
    await page.goto('/dashboard');
    await page.getByRole('button', { name: /বাংলা/ }).click();
    await expect(page.locator('html')).toHaveAttribute('lang', 'bn');

    const stored = await page.evaluate(() => ({
      local: Object.keys(window.localStorage),
      session: Object.keys(window.sessionStorage),
    }));

    expect(stored.local).toEqual([]);
    expect(stored.session).toEqual([]);
  });
});
