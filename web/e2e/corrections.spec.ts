import { expect, test } from './fixtures';

/**
 * The correction screen in a real browser (CP35).
 *
 * What only a browser can show: that the request carries the one field the operator
 * touched and not the eleven others the form rendered, that the button names how many
 * fields will change rather than only greying out, and that the history is on the same
 * screen as the form so the operator sees a correction already made before making it
 * again.
 */

const PATIENT = {
  id: 'p-1',
  clinical_id: 'DTHC-FRD-2026-000137',
  name_en: 'Md Rahim Uddin',
  name_bn: 'মোঃ রহিম উদ্দিন',
  sex: 'male',
  birth: { date: '1985-06-14', precision: 'day', source: 'national_id', age: 41 },
  phone_primary: '+8801711111101',
  phone_secondary: '',
  address: {
    division: 'Dhaka',
    district: 'Faridpur',
    upazila: 'Boalmari',
    address_line: '12 Mujib Road',
    postcode: '7800',
  },
  emergency_contact: { name: '', relation: '', phone: '' },
  socioeconomic: {},
  identifiers: [{ kind: 'national_id', masked: '**** **** 5678' }],
  status: 'active',
  registered_at: '2026-03-11T04:20:00Z',
};

const HISTORY = {
  corrections: [
    {
      field: 'birth_date',
      previous: '1958-06-14',
      current: '1985-06-14',
      reason: 'The NID card reads 1985; the desk transposed the digits.',
      high_impact: true,
      corrected_by_code: 'REG-04',
      corrected_at: '2026-08-20T09:12:00Z',
      event_id: '9f1d2a44-0000-4000-8000-000000000001',
    },
    {
      field: 'postcode',
      previous: '7801',
      current: '7800',
      reason: 'The postcard came back; Boalmari is 7800.',
      high_impact: false,
      corrected_by_code: 'REG-04',
      corrected_at: '2026-08-20T09:12:00Z',
      event_id: '9f1d2a44-0000-4000-8000-000000000002',
    },
  ],
};

const json = (body: unknown) => ({
  status: 200,
  contentType: 'application/json',
  body: JSON.stringify(body),
});

test.beforeEach(async ({ page }) => {
  await page.route('**/v1/patients/p-1', (route) => route.fulfill(json({ patient: PATIENT })));
  await page.route('**/v1/patients/p-1/history', (route) => route.fulfill(json(HISTORY)));
});

test.describe('CP35: correcting a record', () => {
  test('sends the one field that was touched', async ({ signedIn: page }) => {
    let body: Record<string, unknown> = {};
    await page.route('**/v1/patients/p-1', async (route) => {
      if (route.request().method() !== 'PATCH') {
        return route.fulfill(json({ patient: PATIENT }));
      }
      body = route.request().postDataJSON();
      return route.fulfill(
        json({
          patient: PATIENT,
          changes: [{ field: 'upazila', previous: 'Boalmari', current: 'Faridpur Sadar' }],
          high_impact: false,
          invalidated: [],
          event_id: '9f1d2a44-0000-4000-8000-000000000003',
        }),
      );
    });

    await page.goto('/patients/p-1/edit');
    await page.getByTestId('correct-upazila').fill('Faridpur Sadar');
    await page.getByTestId('correction-reason').fill('The patient has moved to Faridpur Sadar.');
    await page.getByTestId('correction-save').click();

    await expect(page.getByTestId('correction-done')).toBeVisible();
    // Twelve fields were rendered. Three keys are sent.
    expect(Object.keys(body).sort()).toEqual(['event_id', 'reason', 'upazila']);
  });

  test('says how many fields will change rather than only greying out', async ({
    signedIn: page,
  }) => {
    await page.goto('/patients/p-1/edit');
    const count = page.getByTestId('correction-count');
    await expect(count).toContainText(/nothing/i);
    await expect(page.getByTestId('correction-save')).toBeDisabled();

    await page.getByTestId('correct-postcode').fill('7801');
    await expect(count).toContainText(/1 field will change/i);

    await page.getByTestId('correct-district').fill('Rajbari');
    await expect(count).toContainText(/2 fields will change/i);
  });

  test('marks a changed field and prints what it was', async ({ signedIn: page }) => {
    await page.goto('/patients/p-1/edit');
    const field = page.locator('.app-correct__field').filter({ hasText: 'Postcode' });
    await expect(field).not.toHaveAttribute('data-changed', /.*/);

    await page.getByTestId('correct-postcode').fill('7801');
    await expect(field).toHaveAttribute('data-changed', 'true');
    await expect(page.getByTestId('correct-postcode-was')).toContainText('7800');
  });

  test('warns before a high-impact correction is submitted', async ({ signedIn: page }) => {
    await page.goto('/patients/p-1/edit');
    await expect(page.getByText(/values already computed/i)).toBeHidden();

    // Three fields, not a native picker: `06/14/1985` and `14/06/1985` are the same control
    // in two browser locales, and this is the field that must never be ambiguous [R-06].
    await page.getByTestId('dob-year').fill('1958');
    await expect(page.getByText(/values already computed/i)).toBeVisible();
    await expect(page.getByText(/authenticator code/i)).toBeVisible();
  });

  test('echoes the age as the year is retyped', async ({ signedIn: page }) => {
    await page.goto('/patients/p-1/edit');
    await page.getByTestId('dob-year').fill('1058');
    await expect(page.getByTestId('age-echo')).toHaveAttribute('data-tone', 'error');
    await page.getByTestId('dob-year').fill('1958');
    await expect(page.getByTestId('age-echo')).toHaveAttribute('data-tone', 'ok');
  });

  test('shows the history on the same screen as the form', async ({ signedIn: page }) => {
    await page.goto('/patients/p-1/edit');
    const history = page.getByTestId('correction-history');
    await expect(history).toBeVisible();
    await expect(history.getByRole('row', { name: /1958-06-14/ })).toBeVisible();
    // The high-impact row is marked, so an auditor can find the ones that mattered.
    await expect(history.locator('tr[data-high-impact]')).toHaveCount(1);
  });

  test('reads in Bangla', async ({ bangla: page }) => {
    await page.goto('/patients/p-1/edit');
    await expect(page.getByRole('heading', { name: 'এই রেকর্ড সংশোধন' })).toBeVisible();
    await expect(page.getByText('সংশোধনের ইতিহাস')).toBeVisible();
  });

  test('makes no request the browser refuses', async ({ signedIn: page }) => {
    const refused: string[] = [];
    page.on('console', (message) => {
      if (message.type() === 'error' && /Content Security Policy/i.test(message.text())) {
        refused.push(message.text());
      }
    });
    await page.goto('/patients/p-1/edit');
    await expect(page.getByTestId('correction-form')).toBeVisible();
    expect(refused).toEqual([]);
  });
});
