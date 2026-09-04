import { PHYSICIAN, expect, test } from './fixtures';

/**
 * The duplicate review and merge screens in a real browser (CP30).
 *
 * What only a browser can show: that the comparison table marks the differences, that the
 * merge button stays disabled until a justification is typed, and that the whole screen is
 * reachable and operable from the keyboard — which matters because a registration desk
 * works with both hands on the keyboard and one eye on the patient.
 */

const LEFT = {
  id: 'p-1',
  clinical_id: 'DTHC-FRD-2026-000137',
  name_en: 'Mohammad Rahim',
  name_bn: 'মোহাম্মদ রহিম',
  sex: 'male',
  birth: { date: '1985-06-14', precision: 'day', source: 'national_id', age: 41 },
  phone_primary: '+8801711111101',
  phone_secondary: '',
  address: {
    division: 'Dhaka',
    district: 'Faridpur',
    upazila: 'Boalmari',
    address_line: '',
    postcode: '',
  },
  emergency_contact: { name: '', relation: '', phone: '' },
  socioeconomic: {},
  identifiers: [{ kind: 'national_id', masked: '**** **** 5678' }],
  status: 'active',
  registered_at: '2026-03-11T04:20:00Z',
};

const RIGHT = {
  ...LEFT,
  id: 'p-2',
  clinical_id: 'DTHC-FRD-2026-000482',
  name_en: 'Muhammad Raheem',
  birth: { date: '1985-01-01', precision: 'year', source: 'patient_stated', age: 41 },
  phone_primary: '+8801722222202',
  identifiers: [],
  registered_at: '2026-08-12T05:05:00Z',
};

const MATCH = {
  verdict: 'review',
  candidates: [
    {
      patient_id: 'p-2',
      clinical_id: 'DTHC-FRD-2026-000482',
      name_en: 'Muhammad Raheem',
      name_bn: 'মোহাম্মদ রহিম',
      sex: 'male',
      birth_date: '1985-01-01',
      phone_masked: '•••• 2202',
      district: 'Faridpur',
      registered_at: '2026-08-12T05:05:00Z',
      score: 0.86,
      deterministic: false,
      reasons: [
        {
          code: 'similar_name',
          message: 'The name sounds the same: Muhammad Raheem.',
          message_bn: 'নাম উচ্চারণ একই: মোহাম্মদ রহিম।',
        },
      ],
    },
  ],
};

const json = (body: unknown) => ({
  status: 200,
  contentType: 'application/json',
  body: JSON.stringify(body),
});

test.beforeEach(async ({ page }) => {
  await page.route('**/v1/patients/check-duplicates', (route) => route.fulfill(json(MATCH)));
  await page.route('**/v1/patients/p-1', (route) => route.fulfill(json({ patient: LEFT })));
  await page.route('**/v1/patients/p-2', (route) => route.fulfill(json({ patient: RIGHT })));
});

test.describe('CP30: deciding whether two records are one person', () => {
  test('marks the differences and nothing else', async ({ signedIn: page }) => {
    await page.goto('/patients/p-1/duplicates');
    await page.getByRole('button', { name: /compare side by side/i }).click();

    const table = page.locator('.app-merge__compare table');
    await expect(table).toBeVisible();

    // Rows that differ are marked; rows that agree are not. Highlighting the matches
    // instead would argue for merging, which is the decision that must never be careless.
    const differing = table.locator('tr[data-differs]');
    await expect(differing).toHaveCount(5); // clinical_id, name_en, birth_date, phone, registered
    await expect(table.getByRole('row', { name: /Boalmari/ })).not.toHaveAttribute(
      'data-differs',
      /.*/,
    );
  });

  test('will not merge without a justification', async ({ signedIn: page }) => {
    await page.goto('/patients/p-1/duplicates');
    await page.getByRole('button', { name: /compare side by side/i }).click();

    const merge = page.getByRole('button', { name: /merge these records/i });
    await expect(merge).toBeDisabled();

    await page.getByRole('textbox').fill('dup');
    await expect(merge).toBeDisabled();

    await page
      .getByRole('textbox')
      .fill('Same person: second registration at the outreach camp on 12 August.');
    await expect(merge).toBeEnabled();
  });

  test('lets the operator say they are different people without a reason', async ({
    signedIn: page,
  }) => {
    await page.goto('/patients/p-1/duplicates');
    await page.getByRole('button', { name: /compare side by side/i }).click();
    await page.getByRole('button', { name: /different people/i }).click();

    // Back to a list that no longer offers the record just dismissed.
    await expect(page.getByText(/no similar records/i)).toBeVisible();
  });

  test('is operable from the keyboard alone', async ({ signedIn: page }) => {
    await page.goto('/patients/p-1/duplicates');
    // Tab until the candidate button has focus, then activate it. A registration desk
    // works with both hands on the keyboard.
    for (let i = 0; i < 25; i++) {
      const focused = await page.evaluate(() => document.activeElement?.className ?? '');
      if (focused.includes('app-duplicates__choose')) break;
      await page.keyboard.press('Tab');
    }
    await page.keyboard.press('Enter');
    await expect(page.getByText(/are these the same person/i)).toBeVisible();
  });

  test('shows the whole screen in Bangla', async ({ bangla: page }) => {
    await page.goto('/patients/p-1/duplicates');
    await expect(page.getByRole('heading', { name: 'সম্ভাব্য একই রোগীর রেকর্ড' })).toBeVisible();
    // The reasons come from the server in both languages; the screen must pick.
    await expect(page.getByText('নাম উচ্চারণ একই: মোহাম্মদ রহিম।')).toBeVisible();
  });

  test('makes no request the browser refuses', async ({ signedIn: page }) => {
    const refused: string[] = [];
    page.on('console', (message) => {
      if (message.type() === 'error' && /Content Security Policy/i.test(message.text())) {
        refused.push(message.text());
      }
    });
    await page.goto('/patients/p-1/duplicates');
    await page.getByRole('button', { name: /compare side by side/i }).click();
    expect(refused).toEqual([]);
  });
});

void PHYSICIAN;
