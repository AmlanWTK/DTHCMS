import { expect, test } from './fixtures';

/**
 * Consent in a real browser (CP36, §15.1).
 *
 * What only a browser can show: that all five consents are on screen with the three nobody
 * asked about visibly different from the one that was withdrawn, that the wording is
 * rendered before the record button is reachable, and that the signature pad actually takes
 * a mark from a pointer.
 */

const TEMPLATES = ['care', 'communication', 'research', 'ai_processing', 'outreach'].map(
  (kind) => ({
    consent_type: kind,
    version: 1,
    language: 'en',
    title: `${kind} consent`,
    body: `Placeholder wording for ${kind}. Pending D-02.`,
    digest: 'a'.repeat(64),
    status: 'active',
  }),
);

const CONSENTS = [
  {
    consent_type: 'care',
    status: 'granted',
    template_version: 1,
    language: 'en',
    capture_method: 'signature',
    granted_at: '2026-09-14T04:42:00Z',
    granted_by_code: 'R001',
    has_evidence: true,
  },
  {
    consent_type: 'communication',
    status: 'revoked',
    template_version: 1,
    language: 'bn',
    capture_method: 'verbal_attested',
    granted_at: '2026-08-01T04:42:00Z',
    revoked_at: '2026-09-10T04:42:00Z',
    revoke_reason: 'The patient asked us to stop texting.',
    has_evidence: false,
  },
];

const json = (body: unknown, status = 200) => ({
  status,
  contentType: 'application/json',
  body: JSON.stringify(body),
});

test.beforeEach(async ({ page }) => {
  await page.route('**/v1/consent-templates**', (route) =>
    route.fulfill(json({ language: 'en', templates: TEMPLATES })),
  );
  await page.route('**/v1/patients/p-1/consents', (route) =>
    route.request().method() === 'GET'
      ? route.fulfill(json({ consents: CONSENTS }))
      : route.fulfill(
          json(
            { consent: { consent_type: 'research', status: 'granted', has_evidence: false } },
            201,
          ),
        ),
  );
});

test.describe('CP36: what a patient has agreed to', () => {
  test('shows all five, with never-asked drawn differently from withdrawn', async ({
    signedIn: page,
  }) => {
    await page.goto('/patients/p-1/consent');
    await expect(page.getByTestId('consent-panel')).toBeVisible();

    const rows = page.locator('.app-consent__row');
    await expect(rows).toHaveCount(5);
    await expect(page.locator('.app-consent__row[data-status="absent"]')).toHaveCount(3);
    await expect(page.getByTestId('consent-communication-status')).toContainText('Withdrawn');
    await expect(page.getByTestId('consent-research-status')).toContainText('Not asked');
  });

  test('puts the wording on screen before the consent can be recorded', async ({
    signedIn: page,
  }) => {
    await page.goto('/patients/p-1/consent');
    await page.getByTestId('consent-research-take').click();

    const wording = page.getByTestId('consent-wording');
    await expect(wording).toBeVisible();
    await expect(wording).toContainText('Placeholder wording for research');
    await expect(wording).toContainText('Version 1');
  });

  test('takes a signature from a pointer', async ({ signedIn: page }) => {
    await page.goto('/patients/p-1/consent');
    await page.getByTestId('consent-research-take').click();

    const pad = page.getByTestId('signature-pad');
    await expect(pad).toBeVisible();
    // Signature is the default method, so the button is refused until there is a mark.
    await expect(page.getByTestId('consent-record')).toBeDisabled();

    // Scrolled into view first: boundingBox is viewport-relative, and a pointer moved to a
    // coordinate below the fold lands on nothing at all — which looks exactly like a pad
    // that does not work.
    await pad.scrollIntoViewIfNeeded();
    const box = (await pad.boundingBox())!;
    await page.mouse.move(box.x + 40, box.y + box.height / 2);
    await page.mouse.down();
    for (let i = 1; i <= 20; i++) {
      await page.mouse.move(box.x + 40 + i * 12, box.y + box.height / 2 + Math.sin(i) * 20);
    }
    await page.mouse.up();

    await expect(page.getByTestId('consent-record')).toBeEnabled();
  });

  test('refuses a spoken consent with nobody watching', async ({ signedIn: page }) => {
    await page.goto('/patients/p-1/consent');
    await page.getByTestId('consent-research-take').click();
    await page.getByTestId('method-verbal_attested').check();

    await expect(page.getByTestId('consent-record')).toBeDisabled();
    await page.getByTestId('consent-witness').fill('Shirin Akter');
    await expect(page.getByTestId('consent-record')).toBeEnabled();
  });

  test('withdraws in one click with the reason optional', async ({ signedIn: page }) => {
    await page.route('**/v1/patients/p-1/consents/care/revoke', (route) =>
      route.fulfill(
        json({ consent: { consent_type: 'care', status: 'revoked', has_evidence: false } }),
      ),
    );
    await page.goto('/patients/p-1/consent');
    await page.getByTestId('consent-care-revoke').click();

    const confirm = page.getByTestId('consent-revoke-confirm');
    await expect(confirm).toBeEnabled();
    await expect(page.getByText(/does not have to explain/i)).toBeVisible();
  });

  test('says so when there is no approved wording', async ({ signedIn: page }) => {
    await page.route('**/v1/consent-templates**', (route) =>
      route.fulfill(json({ language: 'en', templates: [] })),
    );
    await page.goto('/patients/p-1/consent');
    await expect(page.getByText(/approved wording is not loaded/i)).toBeVisible();
    await expect(page.getByTestId('consent-research-take')).toBeDisabled();
  });

  test('reads in Bangla', async ({ bangla: page }) => {
    await page.goto('/patients/p-1/consent');
    await expect(page.getByRole('heading', { name: 'সম্মতি' }).first()).toBeVisible();
    await expect(page.getByText('কল ও এসএমএস')).toBeVisible();
  });

  test('makes no request the browser refuses', async ({ signedIn: page }) => {
    const refused: string[] = [];
    page.on('console', (message) => {
      if (message.type() === 'error' && /Content Security Policy/i.test(message.text())) {
        refused.push(message.text());
      }
    });
    await page.goto('/patients/p-1/consent');
    await expect(page.getByTestId('consent-panel')).toBeVisible();
    expect(refused).toEqual([]);
  });
});
