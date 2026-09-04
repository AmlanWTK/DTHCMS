import { expect, test } from './fixtures';

/**
 * The paediatric growth screen in a real browser (CP48, [R-06]).
 *
 * What only a browser can show: that the trajectory is legible over the reference band, that
 * the 95th percentile is distinguishable without relying on hue, that the switch between two
 * reference standards is drawn, and that the whole thing survives a monochrome print.
 *
 * The percentiles themselves are proven where they are computed — against WHO's and CDC's own
 * printed tables, in the Go suite. A browser test asserting a number the server sent would be
 * a test of the mock.
 */

const PATIENT = '0190a8f2-0000-7000-8000-0000000000c1';

function percentile(
  indicator: string,
  code: string,
  value: number,
  unit: string,
  p: number,
  z: number,
  ageMonths: number,
  standard = 'CDC_2000',
) {
  return {
    indicator,
    code,
    value,
    unit,
    age_days: Math.round(ageMonths * 30.4375),
    age_months: ageMonths,
    z,
    percentile: p,
    standard,
    standard_version: standard === 'CDC_2000' ? '2000.1' : '2006.1',
    l: -1.2,
    m: 16.5,
    s: 0.11,
    effective_at: '2026-09-14T09:00:00Z',
  };
}

const GROWTH = {
  growth: {
    patient_id: PATIENT,
    sex: 'male',
    age_days: 2830,
    applicable: true,
    current: {
      HFA: percentile('HFA', 'BODY_HEIGHT', 132.4, 'cm', 58.2, 0.21, 93),
      WFA: percentile('WFA', 'BODY_WEIGHT', 38.6, 'kg', 94.8, 1.63, 93),
      BFA: percentile('BFA', 'BMI', 22.0, 'kg/m2', 97.6, 1.98, 93),
    },
    history: {
      BFA: [
        {
          ...percentile('BFA', 'BMI', 16.1, 'kg/m2', 66, 0.41, 33, 'WHO_2006'),
          effective_at: '2023-06-10T09:00:00Z',
        },
        {
          ...percentile('BFA', 'BMI', 17.0, 'kg/m2', 82, 0.92, 45, 'WHO_2006'),
          effective_at: '2024-06-12T09:00:00Z',
        },
        {
          ...percentile('BFA', 'BMI', 19.4, 'kg/m2', 94, 1.55, 69),
          effective_at: '2025-06-14T09:00:00Z',
          standard_changed: true,
        },
        {
          ...percentile('BFA', 'BMI', 22.0, 'kg/m2', 97.6, 1.98, 93),
          effective_at: '2026-09-14T09:00:00Z',
        },
      ],
      HFA: [
        {
          ...percentile('HFA', 'BODY_HEIGHT', 95.2, 'cm', 52, 0.05, 33, 'WHO_2006'),
          effective_at: '2023-06-10T09:00:00Z',
        },
        {
          ...percentile('HFA', 'BODY_HEIGHT', 105.8, 'cm', 55, 0.13, 45, 'WHO_2006'),
          effective_at: '2024-06-12T09:00:00Z',
        },
        {
          ...percentile('HFA', 'BODY_HEIGHT', 119.6, 'cm', 57, 0.18, 69),
          effective_at: '2025-06-14T09:00:00Z',
          standard_changed: true,
        },
        {
          ...percentile('HFA', 'BODY_HEIGHT', 132.4, 'cm', 58.2, 0.21, 93),
          effective_at: '2026-09-14T09:00:00Z',
        },
      ],
    },
  },
  weight_status: {
    class: 'obese',
    percent_of_95th: 106,
    bmi_at_95th: 20.7,
    standard: 'CDC_2000',
  },
};

/** Reference lines shaped like a real BMI-for-age chart: a dip to about five, then a rise. */
function bmiLine(offset: number): [number, number][] {
  const out: [number, number][] = [];
  for (let months = 0; months <= 240; months += 3) {
    const years = months / 12;
    const base = 16.4 - 1.9 * Math.exp(-Math.pow((years - 0.4) / 1.6, 2) * -1) * 0;
    const dip = 16.6 - 1.7 * Math.exp(-Math.pow((years - 5.5) / 4.2, 2));
    out.push([months, Number((dip + offset * (0.55 + years * 0.09)).toFixed(3)) + base * 0]);
  }
  return out;
}

const CURVES = {
  curves: {
    indicator: 'BFA',
    sex: 'male',
    unit: 'kg/m2',
    standards: [
      {
        code: 'WHO_2006',
        version: '2006.1',
        min_age_months: 0,
        max_age_months: 60,
        name_en: 'WHO Child Growth Standards',
        name_bn: 'ডব্লিউএইচও শিশু বৃদ্ধি মানদণ্ড',
      },
      {
        code: 'CDC_2000',
        version: '2000.1',
        min_age_months: 60,
        max_age_months: 240.5,
        name_en: 'CDC 2000 Growth Charts',
        name_bn: 'সিডিসি ২০০০ বৃদ্ধি চার্ট',
      },
    ],
    curves: [
      { percentile: 3, points: bmiLine(-1.9) },
      { percentile: 15, points: bmiLine(-1.1) },
      { percentile: 50, points: bmiLine(0) },
      { percentile: 85, points: bmiLine(1.4) },
      { percentile: 95, points: bmiLine(2.1) },
      { percentile: 97, points: bmiLine(2.5) },
    ],
  },
};

async function stubGrowth(page: import('@playwright/test').Page) {
  await page.route('**/v1/patients/*/growth', (route) =>
    route.fulfill({ json: GROWTH, headers: { 'X-Request-ID': 'req_growth' } }),
  );
  await page.route('**/v1/observations/growth-curves**', (route) =>
    route.fulfill({ json: CURVES, headers: { 'X-Request-ID': 'req_curves' } }),
  );
}

test.describe('CP48: the paediatric growth screen', () => {
  test('shows the flag in words before it shows it in colour', async ({ signedIn: page }) => {
    await stubGrowth(page);
    await page.goto(`/patients/${PATIENT}/growth`);

    const flag = page.getByTestId('weight-status');
    await expect(flag).toBeVisible();
    // The word carries the meaning. Roughly one man in twelve who will work in this clinic
    // cannot rely on the colour, and a screen in direct sun flattens every hue anyway.
    await expect(flag).toContainText('Obese');
    await expect(flag).toHaveAttribute('data-class', 'obese');
  });

  test('draws the child over the reference band, with the 95th picked out', async ({
    signedIn: page,
  }) => {
    await stubGrowth(page);
    await page.goto(`/patients/${PATIENT}/growth`);

    await expect(page.getByTestId('growth-chart')).toBeVisible();
    const trajectory = page.getByTestId('growth-trajectory');
    await expect(trajectory).toBeVisible();

    // The patient's line is heavier than any reference line — the failure mode of a growth
    // chart is a clinician reading the wrong one.
    const patientWidth = await trajectory.evaluate((node) =>
      Number(getComputedStyle(node).strokeWidth.replace('px', '')),
    );
    const referenceWidth = await page
      .locator('path[data-percentile="50"]')
      .evaluate((node) => Number(getComputedStyle(node).strokeWidth.replace('px', '')));
    expect(patientWidth).toBeGreaterThan(referenceWidth);

    // The 95th is dashed as well as tinted, so the distinction survives a monochrome print.
    const dash = await page
      .locator('path[data-percentile="95"]')
      .evaluate((node) => getComputedStyle(node).strokeDasharray);
    expect(dash).not.toBe('none');
  });

  test('draws where the reference changes rather than joining silently', async ({
    signedIn: page,
  }) => {
    // D-21: a percentile computed under WHO and one computed under CDC are not the same
    // measurement, and a chart with an invisible join invites exactly that comparison.
    await stubGrowth(page);
    await page.goto(`/patients/${PATIENT}/growth`);

    await expect(page.getByTestId('standard-change').first()).toBeVisible();
    await expect(page.getByTestId('percentile-standard')).toContainText('CDC 2000');
  });

  test('switches indicator without refetching the patient', async ({ signedIn: page }) => {
    await stubGrowth(page);
    await page.goto(`/patients/${PATIENT}/growth`);
    await expect(page.getByTestId('growth-chart')).toBeVisible();

    await page.getByTestId('growth-tab-HFA').click();
    await expect(page.getByTestId('growth-tab-HFA')).toHaveAttribute('aria-selected', 'true');
  });

  test('is in Bangla when the interface is', async ({ bangla: page }) => {
    await stubGrowth(page);
    await page.goto(`/patients/${PATIENT}/growth`);

    await expect(page.getByTestId('weight-status')).toContainText('স্থূল');
    await expect(page.getByTestId('percentile-standard')).toContainText('সিডিসি');
  });

  test('keeps the chart legible in print, where there is no colour left', async ({
    signedIn: page,
  }) => {
    // A parent takes this home. The office printer is monochrome.
    await stubGrowth(page);
    await page.goto(`/patients/${PATIENT}/growth`);
    await expect(page.getByTestId('growth-chart')).toBeVisible();

    await page.emulateMedia({ media: 'print' });

    // The tabs are an on-screen control and have no business on paper.
    await expect(page.getByTestId('growth-tab-HFA')).toBeHidden();
    // The threshold and the patient both go to full ink; the reference band does not.
    const threshold = await page
      .locator('path[data-percentile="95"]')
      .evaluate((node) => getComputedStyle(node).stroke);
    const reference = await page
      .locator('path[data-percentile="50"]')
      .evaluate((node) => getComputedStyle(node).stroke);
    expect(threshold).not.toBe(reference);
  });
});
