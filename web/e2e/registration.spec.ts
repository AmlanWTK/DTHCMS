import { expect, test } from './fixtures';

/**
 * The registration desk in a real browser (CP32).
 *
 * The acceptance criteria are about behaviour a unit test cannot see: that a wrong year is
 * visually obvious before submission, that duplicate warnings arrive before creation rather
 * than after, and that the whole form can be completed without touching the mouse — which
 * is what a ninety-second registration actually depends on.
 */

const CLEAR = { verdict: 'clear', candidates: [] };
const REVIEW = {
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

const REGISTERED = {
  patient: {
    id: 'p-9',
    clinical_id: 'DTHC-FRD-2026-000138',
    name_en: 'Rahima Begum',
    name_bn: 'রহিমা বেগম',
    sex: 'female',
    birth: { date: '1985-06-14', precision: 'day', source: 'national_id', age: 41 },
    phone_primary: '+8801712345678',
    phone_secondary: '',
    address: { division: '', district: '', upazila: '', address_line: '', postcode: '' },
    emergency_contact: { name: '', relation: '', phone: '' },
    socioeconomic: {},
    identifiers: [],
    status: 'active',
    registered_at: '2026-09-03T10:00:00Z',
  },
  event_id: '0190a8f2-0000-7000-8000-00000000000f',
  duplicate: false,
};

const json = (body: unknown, status = 200) => ({
  status,
  contentType: 'application/json',
  body: JSON.stringify(body),
});

async function routes(page: import('@playwright/test').Page, match: unknown = CLEAR) {
  await page.route('**/v1/patients/check-duplicates', (route) => route.fulfill(json(match)));
  await page.route('**/v1/patients', (route) =>
    route.request().method() === 'POST'
      ? route.fulfill(json(REGISTERED, 201))
      : route.fulfill(json({ patients: [], page: 1 })),
  );
}

test.describe('CP32: the registration desk', () => {
  test('makes a mistyped year obvious before submission', async ({ signedIn: page }) => {
    // Criterion 2, and the reason the field is three boxes with an echo rather than one
    // text input: 1085 is not visibly wrong, "over 130" is.
    await routes(page);
    await page.goto('/patients/new');
    await page.getByTestId('dob-day').fill('14');
    await page.getByTestId('dob-month').fill('06');
    await page.getByTestId('dob-year').fill('1085');

    const echo = page.getByTestId('age-echo');
    await expect(echo).toHaveText(/over 130/i);
    await expect(echo).toHaveAttribute('data-tone', 'error');

    await page.getByTestId('dob-year').fill('1985');
    await expect(echo).toHaveText(/41 years, 2 months/);
    await expect(echo).toHaveAttribute('data-tone', 'ok');
  });

  test('accepts a year on its own and says the age is approximate', async ({ signedIn: page }) => {
    await routes(page);
    await page.goto('/patients/new');
    await page.getByTestId('dob-year').fill('1958');
    await expect(page.getByTestId('age-echo')).toHaveText(/About 68 years old/);
  });

  test('warns about a duplicate before the record is created', async ({ signedIn: page }) => {
    // Criterion 3. Warning after creation is warning too late.
    await routes(page, REVIEW);
    await page.goto('/patients/new');
    await page.getByTestId('name-en').fill('Mohammad Rahim');

    await expect(page.getByText('Somebody similar is already registered')).toBeVisible();
    await expect(page.getByText('The name sounds the same: Muhammad Raheem.')).toBeVisible();

    // And saying they are different people is one click with no reason asked.
    await page.getByRole('button', { name: 'These are different people' }).click();
    await expect(page.getByRole('button', { name: /Marked as different people/ })).toBeDisabled();
  });

  test('can be completed without touching the mouse', async ({ signedIn: page }) => {
    // Criterion 5, and what a ninety-second registration actually depends on.
    await routes(page);
    await page.goto('/patients/new');

    await page.getByTestId('name-en').focus();
    await page.keyboard.type('Rahima Begum');
    await page.keyboard.press('Tab');
    await page.keyboard.type('রহিমা বেগম');
    await page.keyboard.press('Tab');
    await page.keyboard.press('f'); // the native select picks "Female"
    await page.keyboard.press('Tab');
    await page.keyboard.type('14');
    await page.keyboard.press('Tab');
    await page.keyboard.type('06');
    await page.keyboard.press('Tab');
    await page.keyboard.type('1985');

    await expect(page.getByTestId('age-echo')).toHaveText(/41 years/);
    await expect(page.getByTestId('sex')).toHaveValue('female');
  });

  test('names what is still missing rather than only greying the button', async ({
    signedIn: page,
  }) => {
    await routes(page);
    await page.goto('/patients/new');
    const save = page.getByRole('button', { name: 'Register' });
    await expect(save).toBeDisabled();
    await expect(page.getByText(/Still needed/)).toContainText('the English name');

    await page.getByTestId('name-en').fill('Rahima Begum');
    await page.getByTestId('sex').selectOption('female');
    await page.getByTestId('dob-day').fill('14');
    await page.getByTestId('dob-month').fill('06');
    await page.getByTestId('dob-year').fill('1985');
    await page.getByTestId('dob-source').selectOption('national_id');
    await page.getByTestId('phone').fill('01712345678');
    await page.getByTestId('consent').fill('consent_2026_0001');

    await expect(save).toBeEnabled();
    await expect(page.getByText(/Still needed/)).toHaveCount(0);
  });

  test('hands over the clinical id to read aloud and print', async ({ signedIn: page }) => {
    await routes(page);
    await page.goto('/patients/new');
    await page.getByTestId('name-en').fill('Rahima Begum');
    await page.getByTestId('sex').selectOption('female');
    await page.getByTestId('dob-day').fill('14');
    await page.getByTestId('dob-month').fill('06');
    await page.getByTestId('dob-year').fill('1985');
    await page.getByTestId('dob-source').selectOption('national_id');
    await page.getByTestId('phone').fill('01712345678');
    await page.getByTestId('consent').fill('consent_2026_0001');
    await page.getByRole('button', { name: 'Register' }).click();

    await expect(page.getByText('DTHC-FRD-2026-000138')).toBeVisible();
    await expect(page.getByText(/Send the patient to Anthropometry/)).toBeVisible();
  });

  test('is entirely in Bangla when the interface is', async ({ bangla: page }) => {
    await routes(page);
    await page.goto('/patients/new');
    await expect(page.getByRole('heading', { name: 'রোগী নিবন্ধন' })).toBeVisible();
    await expect(page.getByText('কে এই ব্যক্তি')).toBeVisible();
    await page.getByTestId('dob-year').fill('1985');
    await expect(page.getByTestId('age-echo')).toContainText('বছর');
  });

  test('makes no request the browser refuses', async ({ signedIn: page }) => {
    const refused: string[] = [];
    page.on('console', (message) => {
      if (message.type() === 'error' && /Content Security Policy/i.test(message.text())) {
        refused.push(message.text());
      }
    });
    await routes(page);
    await page.goto('/patients/new');
    await page.getByTestId('name-en').fill('Rahima Begum');
    expect(refused).toEqual([]);
  });
});
