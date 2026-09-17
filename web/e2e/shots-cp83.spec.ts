import { expect, test } from './fixtures';

/**
 * Station 10, photographed (CP83).
 *
 * # Why the API is answered from literals
 *
 * These are pictures of *states*, and the states that matter cannot be produced on demand from a
 * live stack: a file with one blocking finding and two warnings, a file an override already
 * stands on, and — the one this checkpoint is really about — a clinic whose rule table is empty.
 * Driving a real backend into each would make the pictures a function of what a fixture happened
 * to seed that afternoon.
 *
 * The bodies below are what `internal/qa` actually serialises; the Go tests are what prove it,
 * and the clinical content is copied from migration 00071's seed rather than paraphrased, so that
 * a rule edited in the migration and not here shows up as a picture that disagrees with the
 * clinic.
 *
 * # The patient
 *
 * Mosammat Rahima Begum, 61, of Boalmari in Faridpur. Type 2 diabetes eleven years, on metformin
 * and premixed insulin, with an HbA1c nobody has taken since March and a lipid profile older than
 * that. She is the patient station 10 exists for: a file that looks finished and is not.
 *
 * # What each picture is for
 *
 * One per state a QA officer actually meets, in both languages, plus the two that are about *who*
 * is looking. The read-only one is not decoration: CP92 shipped a station that handed a consultant
 * the whole form, every control answering 403, and a screenshot is what found it.
 */

const OUT = 'shots';
const SHEET = '0190a8f2-0000-7000-8000-0000000000c1';
const PATIENT = '0190a8f2-0000-7000-8000-0000000000b1';
const VISIT = '0190a8f2-0000-7000-8000-0000000000a1';
const NAHID = '0190a8f2-0000-7000-8000-000000000010';

const json = (body: unknown, status = 200) => ({
  status,
  contentType: 'application/json',
  body: JSON.stringify(body),
});

const STATIONS = {
  STN_CONSULTATION: ['Physician Consultation', 'চিকিৎসকের পরামর্শ'],
  STN_EXAMINATION: ['Clinical Examination & Vitals', 'শারীরিক পরীক্ষা ও ভাইটাল'],
  STN_HISTORY: ['Medical History', 'রোগের ইতিহাস'],
  STN_RX_EDUCATION: ['Prescription Education', 'ওষুধ শিক্ষা'],
  STN_COUNSELING: ['Counseling & Lifestyle', 'পরামর্শ ও জীবনযাপন'],
  STN_ANTHROPOMETRY: ['Anthropometry & Screening', 'দেহমাপ ও স্ক্রিনিং'],
};

/** Migration 00071's own words for rule 4. */
const HBA1C = {
  rule_code: 'DIABETES_HBA1C',
  severity: 'BLOCK',
  title_en: 'Diabetic with no HbA1c recorded or ordered in six months',
  title_bn: 'ডায়াবেটিস রোগী, ছয় মাসে এইচবিএ১সি নেওয়া বা লেখা হয়নি',
  detail_en:
    "Record the result, or order the test. An ordered test counts: the consultant has done the " +
    "right thing and the lab's turnaround is not his to answer for.",
  detail_bn:
    'ফল লিখুন, অথবা পরীক্ষাটি দিন। পরীক্ষা দেওয়া হলেই যথেষ্ট: চিকিৎসক ঠিক কাজটিই করেছেন, ল্যাবের দেরির জন্য তিনি দায়ী নন।',
  // Exactly what the server sends: `internal/qa` renders the observation catalogue's own name
  // and says the window the way a person says it. A fixture that showed the fix while the
  // product did not have it would make the screenshot a lie (ADR-0038).
  subject_en: 'no HbA1c in the last six months, and none ordered',
  subject_bn: 'গত ৬ মাসে কোনো এইচবিএ১সি নেই, এবং কোনোটি দেওয়াও হয়নি',
  subject_codes: ['HBA1C'],
  bounce_station_code: 'STN_CONSULTATION',
  bounce_station_en: 'Physician Consultation',
  bounce_station_bn: 'চিকিৎসকের পরামর্শ',
};

/** Rule 7, a WARN: the foot examination the examination room can do today. */
const FOOT = {
  rule_code: 'DIABETES_FOOT',
  severity: 'WARN',
  title_en: 'Diabetic with no foot examination in twelve months',
  title_bn: 'ডায়াবেটিস রোগী, বারো মাসে পায়ের পরীক্ষা হয়নি',
  detail_en: 'The examination station can do it today.',
  detail_bn: 'পরীক্ষা কেন্দ্র আজই এটি করতে পারে।',
  // Four codes, one examination: the rule carries the phrase, so the screen does not list parts.
  subject_en: 'no foot sensation test in the last year',
  subject_bn: 'গত বছরে কোনো পায়ের অনুভূতি পরীক্ষা নেই',
  subject_codes: ['MONOFILAMENT_LEFT', 'MONOFILAMENT_RIGHT', 'FOOT_RISK_LEFT', 'FOOT_RISK_RIGHT'],
  bounce_station_code: 'STN_EXAMINATION',
  bounce_station_en: 'Clinical Examination & Vitals',
  bounce_station_bn: 'শারীরিক পরীক্ষা ও ভাইটাল',
};

/** Rule 9, the other WARN. */
const LIPIDS = {
  rule_code: 'DIABETES_LIPIDS',
  severity: 'WARN',
  title_en: 'Type 2 diabetic with no lipid profile in twelve months',
  title_bn: 'টাইপ ২ ডায়াবেটিস রোগী, বারো মাসে লিপিড প্রোফাইল হয়নি',
  detail_en: 'Order it, or record the result if it is on paper in front of you.',
  detail_bn: 'পরীক্ষাটি দিন, অথবা কাগজে ফল সামনে থাকলে সেটি লিখুন।',
  subject_en: 'no lipid profile in the last year, and none ordered',
  subject_bn: 'গত বছরে কোনো লিপিড প্রোফাইল নেই, এবং কোনোটি দেওয়াও হয়নি',
  subject_codes: ['CHOL_LDL', 'CHOL_TOTAL', 'CHOL_HDL', 'TRIGLYCERIDE'],
  bounce_station_code: 'STN_CONSULTATION',
  bounce_station_en: 'Physician Consultation',
  bounce_station_bn: 'চিকিৎসকের পরামর্শ',
};

function review(overrides: Record<string, unknown> = {}) {
  return {
    review: {
      prescription_id: SHEET,
      patient_id: PATIENT,
      visit_id: VISIT,
      at: '2026-09-14T09:30:00Z',
      rules_live: 18,
      stations: STATIONS,
      findings: [HBA1C, FOOT, LIPIDS],
      ...(overrides.review as Record<string, unknown>),
    },
    summary_en: 'One blocking finding and two warnings. This file cannot be cleared.',
    summary_bn: '১টি বাধা ও ২টি সতর্কবার্তা। এই ফাইলে ছাড়পত্র দেওয়া যাবে না।',
    can_clear: false,
    clearance_stands: false,
    ...overrides,
  };
}

/** Two warnings and nothing blocking: the file that clears with an acknowledgement. */
const WARNINGS_ONLY = review({
  review: {
    prescription_id: SHEET,
    patient_id: PATIENT,
    visit_id: VISIT,
    at: '2026-09-14T09:30:00Z',
    rules_live: 18,
    stations: STATIONS,
    findings: [FOOT, LIPIDS],
  },
  summary_en: 'Two warnings to acknowledge. Nothing blocks this file.',
  summary_bn: '২টি সতর্কবার্তা স্বীকার করতে হবে। কিছুই আটকাচ্ছে না।',
  can_clear: true,
  clearance_stands: false,
});

/** Cleared: the gate now says the prescription may be signed. */
const CLEARED = review({
  review: {
    prescription_id: SHEET,
    patient_id: PATIENT,
    visit_id: VISIT,
    at: '2026-09-14T09:35:00Z',
    rules_live: 18,
    stations: STATIONS,
    findings: [],
    decision: {
      id: '0190a8f2-0000-7000-8000-0000000000d1',
      prescription_id: SHEET,
      patient_id: PATIENT,
      visit_id: VISIT,
      outcome: 'CLEARED',
      decided_at: '2026-09-14T09:35:00Z',
      decided_by: '0190a8f2-0000-7000-8000-00000000000f',
      decided_by_name_en: 'Shirin Akhter',
      decided_by_name_bn: 'শিরীন আক্তার',
      findings: [],
      acknowledged: [],
    },
  },
  summary_en: '18 checks passed. This file is complete.',
  summary_bn: '১৮টি যাচাইয়ের সবগুলিই উত্তীর্ণ। ফাইলটি সম্পূর্ণ।',
  can_clear: true,
  clearance_stands: true,
});

/**
 * The state `docs/qa-rules.md` §5 is about.
 *
 * No rules configured, so nothing was checked — and the clearance is **still required**. This is
 * the picture that has to be different from the one above it, because the two are the same green
 * screen in every implementation that gets this wrong.
 */
const UNCHECKED = review({
  review: {
    prescription_id: SHEET,
    patient_id: PATIENT,
    visit_id: VISIT,
    at: '2026-09-14T09:30:00Z',
    rules_live: 0,
    stations: STATIONS,
    findings: [],
  },
  summary_en:
    'No QA rules are configured for this clinic, so nothing was checked. Clearance is still ' +
    'required before this prescription can be signed.',
  summary_bn:
    'এই ক্লিনিকের জন্য কোনো কিউএ নিয়ম নির্ধারণ করা নেই, তাই কিছুই যাচাই করা হয়নি। তবু স্বাক্ষরের আগে ছাড়পত্র দিতেই হবে।',
  can_clear: true,
  clearance_stands: false,
});

/** A file an override already stands on, as the consultant left it. */
const OVERRIDDEN = review({
  review: {
    prescription_id: SHEET,
    patient_id: PATIENT,
    visit_id: VISIT,
    at: '2026-09-14T09:42:00Z',
    rules_live: 18,
    stations: STATIONS,
    findings: [HBA1C, FOOT, LIPIDS],
    override: {
      id: '0190a8f2-0000-7000-8000-0000000000e1',
      prescription_id: SHEET,
      patient_id: PATIENT,
      visit_id: VISIT,
      granted_at: '2026-09-14T09:42:00Z',
      granted_by: NAHID,
      granted_by_code: 'E001',
      granted_by_name_en: 'Dr Nahid Hasan',
      granted_by_name_bn: 'ডা. নাহিদ হাসান',
      reason:
        'Lab closed for Eid until Sunday; the patient travelled from Boalmari and cannot return. ' +
        'HbA1c ordered for her next visit.',
      blocking_at_grant: ['DIABETES_HBA1C'],
    },
  },
});

/** The bounce, as the record holds it once it has been sent. */
const BOUNCED = review({
  review: {
    prescription_id: SHEET,
    patient_id: PATIENT,
    visit_id: VISIT,
    at: '2026-09-14T09:38:00Z',
    rules_live: 18,
    stations: STATIONS,
    findings: [HBA1C, FOOT, LIPIDS],
    decision: {
      id: '0190a8f2-0000-7000-8000-0000000000d2',
      prescription_id: SHEET,
      patient_id: PATIENT,
      visit_id: VISIT,
      outcome: 'BOUNCED',
      decided_at: '2026-09-14T09:38:00Z',
      decided_by: '0190a8f2-0000-7000-8000-00000000000f',
      decided_by_name_en: 'Shirin Akhter',
      decided_by_name_bn: 'শিরীন আক্তার',
      bounce_station_code: 'STN_CONSULTATION',
      bounce_station_en: 'Physician Consultation',
      bounce_station_bn: 'চিকিৎসকের পরামর্শ',
      reason_en:
        'Diabetic with no HbA1c recorded or ordered in six months — no HbA1c in the last six months, and none ordered',
      reason_bn:
        'ডায়াবেটিস রোগী, ছয় মাসে এইচবিএ১সি নেওয়া বা লেখা হয়নি — গত ৬ মাসে কোনো এইচবিএ১সি নেই, এবং কোনোটি দেওয়াও হয়নি',
      findings: [HBA1C],
      acknowledged: [],
    },
  },
  summary_en: 'One blocking finding and two warnings. This file cannot be cleared.',
  summary_bn: '১টি বাধা ও ২টি সতর্কবার্তা। এই ফাইলে ছাড়পত্র দেওয়া যাবে না।',
});

const QUEUE = {
  queue: [
    {
      prescription_id: SHEET,
      patient_id: PATIENT,
      visit_id: VISIT,
      submitted_at: '2026-09-14T09:24:00Z',
      patient_name_en: 'Mosammat Rahima Begum',
      patient_name_bn: 'মোসাম্মৎ রহিমা বেগম',
      clinical_id: 'DTHC-FRD-2026-000412',
      item_count: 4,
      bounce_count: 0,
    },
    {
      prescription_id: '0190a8f2-0000-7000-8000-0000000000c2',
      patient_id: '0190a8f2-0000-7000-8000-0000000000b2',
      visit_id: '0190a8f2-0000-7000-8000-0000000000a2',
      submitted_at: '2026-09-14T09:26:00Z',
      patient_name_en: 'Md Abdul Karim Sheikh',
      patient_name_bn: 'মোঃ আব্দুল করিম শেখ',
      clinical_id: 'DTHC-FRD-2026-000418',
      item_count: 3,
      bounce_count: 2,
    },
    {
      prescription_id: '0190a8f2-0000-7000-8000-0000000000c3',
      patient_id: '0190a8f2-0000-7000-8000-0000000000b3',
      visit_id: '0190a8f2-0000-7000-8000-0000000000a3',
      submitted_at: '2026-09-14T09:31:00Z',
      patient_name_en: 'Shefali Rani Das',
      patient_name_bn: 'শেফালী রাণী দাস',
      clinical_id: 'DTHC-FRD-2026-000423',
      item_count: 6,
      bounce_count: 0,
    },
  ],
};

type Page = import('@playwright/test').Page;

/** Answers the review endpoint with one body, and the queue with the morning's three patients. */
async function serve(page: Page, body: unknown) {
  await page.route('**/v1/qa/queue', (route) => route.fulfill(json(QUEUE)));
  await page.route('**/v1/prescriptions/*/qa', (route) => route.fulfill(json(body)));
  await page.route('**/v1/directory', (route) =>
    route.fulfill(json({ staff: [], devices: [], stations: [], as_of: '2026-09-01T00:00:00Z' })),
  );
}

async function openReview(page: Page, body: unknown) {
  await serve(page, body);
  await page.goto(`/qa?prescription=${SHEET}`);
  await expect(page.getByTestId('qa-review')).toBeVisible();
}

async function shoot(page: Page, name: string) {
  // A settled page: the fonts matter more here than anywhere else, because half these pictures
  // exist to show that Bengali conjuncts render rather than falling back to boxes.
  await page.evaluate(() => document.fonts.ready);
  await page.screenshot({ path: `${OUT}/${name}.png`, fullPage: true });
}

// --- the queue ---------------------------------------------------------------

test('the queue at station 10', async ({ qaOfficer: page }) => {
  await serve(page, review());
  await page.goto('/qa');
  await expect(page.getByText('Mosammat Rahima Begum')).toBeVisible();
  await shoot(page, 'cp83-queue-en');
});

test('the queue at station 10, in Bangla', async ({ qaOfficerBangla: page }) => {
  await serve(page, review());
  await page.goto('/qa');
  await expect(page.getByText('মোসাম্মৎ রহিমা বেগম')).toBeVisible();
  await shoot(page, 'cp83-queue-bn');
});

// --- findings of both severities ---------------------------------------------

test('a review with both severities on it', async ({ qaOfficer: page }) => {
  await openReview(page, review());
  await expect(page.getByText(/Diabetic with no HbA1c/)).toBeVisible();
  await shoot(page, 'cp83-findings-en');
});

test('a review with both severities on it, in Bangla', async ({ qaOfficerBangla: page }) => {
  await openReview(page, review());
  await expect(page.getByText(/ডায়াবেটিস রোগী, ছয় মাসে এইচবিএ১সি/)).toBeVisible();
  await shoot(page, 'cp83-findings-bn');
});

// --- the bounce, with its reason ---------------------------------------------

test('a bounce being sent, with its station and its reason', async ({ qaOfficer: page }) => {
  await openReview(page, review());
  await page.getByLabel(/What is wrong \(English\)/).fill(
    'No HbA1c since March. Please order one before she leaves.',
  );
  await page
    .getByLabel(/What is wrong \(Bangla\)/)
    .fill('মার্চ থেকে এইচবিএ১সি হয়নি। তিনি যাওয়ার আগে একটি দিন।');
  await expect(page.getByTestId('qa-bounce')).toBeEnabled();
  await shoot(page, 'cp83-bounce-en');
});

test('a bounce being sent, in Bangla', async ({ qaOfficerBangla: page }) => {
  await openReview(page, review());
  await page
    .getByLabel(/কী সমস্যা \(বাংলা\)/)
    .fill('মার্চ থেকে এইচবিএ১সি হয়নি। তিনি যাওয়ার আগে একটি দিন।');
  await expect(page.getByTestId('qa-bounce')).toBeEnabled();
  await shoot(page, 'cp83-bounce-bn');
});

test('a file that has been sent back', async ({ qaOfficer: page }) => {
  await openReview(page, BOUNCED);
  await expect(page.getByText(/This file was sent back/)).toBeVisible();
  await shoot(page, 'cp83-bounced-en');
});

test('a file that has been sent back, in Bangla', async ({ qaOfficerBangla: page }) => {
  await openReview(page, BOUNCED);
  await expect(page.getByText(/এই ফাইলটি ফেরত পাঠানো হয়েছিল/)).toBeVisible();
  await shoot(page, 'cp83-bounced-bn');
});

// --- warnings, acknowledged, and the clearance -------------------------------

test('two warnings being acknowledged before a clearance', async ({ qaOfficer: page }) => {
  await openReview(page, WARNINGS_ONLY);
  for (const box of await page.getByRole('checkbox').all()) await box.check();
  await expect(page.getByTestId('qa-clear')).toBeEnabled();
  await shoot(page, 'cp83-warnings-acknowledged-en');
});

test('two warnings being acknowledged, in Bangla', async ({ qaOfficerBangla: page }) => {
  await openReview(page, WARNINGS_ONLY);
  for (const box of await page.getByRole('checkbox').all()) await box.check();
  await expect(page.getByTestId('qa-clear')).toBeEnabled();
  await shoot(page, 'cp83-warnings-acknowledged-bn');
});

test('a cleared prescription', async ({ qaOfficer: page }) => {
  await openReview(page, CLEARED);
  await expect(page.getByText(/may be signed/)).toBeVisible();
  await shoot(page, 'cp83-cleared-en');
});

test('a cleared prescription, in Bangla', async ({ qaOfficerBangla: page }) => {
  await openReview(page, CLEARED);
  await expect(page.getByText(/স্বাক্ষর করা যাবে/)).toBeVisible();
  await shoot(page, 'cp83-cleared-bn');
});

// --- the empty rule table ----------------------------------------------------

/**
 * The §5 picture, and the most important one in this set.
 *
 * A clinic with no rules checked nothing. The screen says so in words and still says the
 * prescription cannot be signed — which is what makes this picture different from
 * `cp83-cleared-en`, and what the whole checkpoint turns on.
 */
test('an empty rule table: nothing checked, and clearance still owed', async ({
  qaOfficer: page,
}) => {
  await openReview(page, UNCHECKED);
  await expect(page.getByText(/nothing was checked/)).toBeVisible();
  await shoot(page, 'cp83-no-rules-en');
});

test('an empty rule table, in Bangla', async ({ qaOfficerBangla: page }) => {
  await openReview(page, UNCHECKED);
  await expect(page.getByText(/কিছুই যাচাই করা হয়নি/)).toBeVisible();
  await shoot(page, 'cp83-no-rules-bn');
});

// --- the override ------------------------------------------------------------

test('the consultant override, with its reason', async ({ consultant: page }) => {
  await openReview(page, review());
  await expect(page.getByTestId('qa-override')).toBeVisible();
  await page
    .getByLabel(/Why this file is going through blocked/)
    .fill('Lab closed for Eid until Sunday; she travelled from Boalmari and cannot return.');
  await expect(page.getByTestId('qa-override')).toBeEnabled();
  await shoot(page, 'cp83-override-en');
});

test('the consultant override, in Bangla', async ({ consultantBangla: page }) => {
  await openReview(page, review());
  await expect(page.getByTestId('qa-override')).toBeVisible();
  await page
    .getByLabel(/আটকানো অবস্থাতেও কেন ছেড়ে দেওয়া হচ্ছে/)
    .fill('ঈদের ছুটিতে ল্যাব বন্ধ; তিনি বোয়ালমারী থেকে এসেছেন, ফিরে আসতে পারবেন না।');
  await expect(page.getByTestId('qa-override')).toBeEnabled();
  await shoot(page, 'cp83-override-bn');
});

test('a file an override already stands on', async ({ qaOfficer: page }) => {
  await openReview(page, OVERRIDDEN);
  await expect(page.getByText(/A consultant override stands/)).toBeVisible();
  await shoot(page, 'cp83-override-standing-en');
});

test('a file an override already stands on, in Bangla', async ({ qaOfficerBangla: page }) => {
  await openReview(page, OVERRIDDEN);
  await expect(page.getByText(/কনসালট্যান্টের ওভাররাইড দেওয়া আছে/)).toBeVisible();
  await shoot(page, 'cp83-override-standing-bn');
});

// --- who is handed which controls -------------------------------------------

/**
 * The picture that exists because CP92 shipped the opposite.
 *
 * A reader holding `qa.review` alone sees the findings and **no control at all**. Not a disabled
 * button: an absent one. A disabled button is still the interface telling this person the act is
 * part of their job.
 */
test('a reader who may look and not act is handed no controls', async ({ qaOnlooker: page }) => {
  await openReview(page, review());
  await expect(page.getByText(/may read this review and not act on it/)).toBeVisible();
  await expect(page.getByTestId('qa-clear')).toHaveCount(0);
  await expect(page.getByTestId('qa-bounce')).toHaveCount(0);
  await expect(page.getByTestId('qa-override')).toHaveCount(0);
  await shoot(page, 'cp83-read-only-en');
});

/**
 * And the separation §2 is about, photographed from the officer's side: she clears and bounces,
 * and there is no override on her screen, because the person watching the rate must not be the
 * person granting them.
 */
test('the officer has no override on her screen', async ({ qaOfficer: page }) => {
  await openReview(page, review());
  await expect(page.getByTestId('qa-clear')).toBeVisible();
  await expect(page.getByTestId('qa-bounce')).toBeVisible();
  await expect(page.getByTestId('qa-override')).toHaveCount(0);
  await shoot(page, 'cp83-officer-no-override-en');
});
