import type { components } from '@dthcms/api-client';

import { expect, test } from './fixtures';

/**
 * The session shape, taken from the contract rather than inferred from the literal below.
 *
 * Inferring it let the first version of this file build a fixture TypeScript narrowed to exactly
 * the fields it happened to contain, so adding `unclassified_devices` to one variant and not the
 * other was a type error about `never[]` rather than the thing it actually was. A fixture typed
 * against the contract fails when the contract moves, which is the only failure worth having.
 */
type Session = components['schemas']['EducationSession'];
type Competency = components['schemas']['EducationCompetency'];

/**
 * The education station and the improvement score, photographed (CP88, CP92).
 *
 * # Why the API is answered from literals
 *
 * These pictures are of *states*, and two of the states that matter cannot be produced on demand
 * from a live stack: a patient mid-assessment with one item unable and three corrected, and a
 * re-education flag standing from an earlier visit. Driving a real backend into each would make
 * the pictures a function of what a fixture happened to seed that afternoon.
 *
 * The literals below are what `internal/education` actually serialises — the Go tests are what
 * prove it does — and the clinical content is what migration 00070 seeds, copied rather than
 * paraphrased so that a checklist edited in the migration and not here shows up as a picture that
 * disagrees with the clinic.
 *
 * # The patient
 *
 * Mosammat Rahima Begum, 61, of Kanaipur in Faridpur: a follow-up type 2 diabetic eleven years in,
 * on premixed insulin by pen twice daily with metformin, testing at home. An HbA1c of 8.6% coming
 * down from 9.4%, a weight that has not moved, and a blood pressure a little above target. She is
 * the patient this station exists for — new enough to insulin that her technique is worth watching,
 * long enough on tablets that the compliance question has a real answer.
 */

const OUT = 'shots';
const PATIENT = '0190d820-0000-7000-8000-000000008801';
const VISIT = '0190d820-0000-7000-8000-000000008802';
const RINA = '0190d820-0000-7000-8000-000000008810';
const SHIRIN = '0190d820-0000-7000-8000-000000008811';

const json = (body: unknown) => ({
  status: 200,
  contentType: 'application/json',
  body: JSON.stringify(body),
});

// --- the reference data, as migration 00070 seeds it -------------------------

const STATES = [
  { state: 'demonstrated', display_en: 'Demonstrated', display_bn: 'নিজেই পেরেছেন', ordering: 10 },
  {
    state: 'corrected_today',
    display_en: 'Corrected today',
    display_bn: 'আজ শুধরে দেওয়া হয়েছে',
    ordering: 20,
  },
  {
    state: 'unable',
    display_en: 'Unable',
    display_bn: 'পারেননি',
    ordering: 30,
  },
];

const PEN_ITEMS = [
  [
    1,
    'EDU_PEN_01',
    false,
    'Checks the expiry date and that the insulin looks as it should',
    'মেয়াদ শেষের তারিখ দেখে নেন এবং ইনসুলিন দেখতে ঠিক আছে কি না মিলিয়ে নেন',
  ],
  [
    2,
    'EDU_PEN_02',
    true,
    'Rolls a cloudy insulin (NPH or premix) gently between the palms until evenly milky - does not shake it',
    'ঘোলা ইনসুলিন (এনপিএইচ বা প্রিমিক্স) দুই হাতের তালুর মাঝে আস্তে আস্তে গড়িয়ে সমানভাবে দুধের মতো করে নেন — ঝাঁকান না',
  ],
  [
    3,
    'EDU_PEN_03',
    false,
    'Attaches a new needle for this injection',
    'এই ইনজেকশনের জন্য নতুন সুচ লাগান',
  ],
  [
    4,
    'EDU_PEN_04',
    true,
    'Air-shot: dials 2 units, holds the pen upright, presses until a drop appears at the tip',
    'এয়ার-শট: ২ ইউনিট ঘুরিয়ে নিয়ে পেন সোজা উপরের দিকে ধরে চাপ দেন, যতক্ষণ না সুচের মাথায় এক ফোঁটা ওষুধ দেখা যায়',
  ],
  [
    5,
    'EDU_PEN_05',
    false,
    'Dials the prescribed dose and can state what that dose is',
    'নির্ধারিত ডোজ ঘুরিয়ে নেন এবং ডোজটি কত তা বলতে পারেন',
  ],
  [
    6,
    'EDU_PEN_06',
    false,
    'Chooses a site and can name at least two sites they rotate between',
    'ইনজেকশনের জায়গা বেছে নেন এবং অন্তত দুটি জায়গার নাম বলতে পারেন যেগুলো ঘুরিয়ে ফিরিয়ে ব্যবহার করেন',
  ],
  [
    7,
    'EDU_PEN_07',
    false,
    'Inserts at 90 degrees, skin pinched only if they are thin or using a longer needle',
    '৯০ ডিগ্রি কোণে সুচ ঢোকান; শুকনো গড়ন হলে বা লম্বা সুচ হলে তবেই চামড়া চিমটি দিয়ে তোলেন',
  ],
  [
    8,
    'EDU_PEN_08',
    true,
    'Holds the button down and counts to ten before withdrawing',
    'বোতাম চেপে ধরে রেখে দশ পর্যন্ত গোনেন, তারপর সুচ বের করেন',
  ],
  [
    9,
    'EDU_PEN_09',
    false,
    'Removes the needle and disposes of it safely - not loose in household waste',
    'সুচ খুলে নিরাপদে ফেলেন — ঘরের সাধারণ ময়লার সঙ্গে খোলা অবস্থায় নয়',
  ],
  [
    10,
    'EDU_PEN_10',
    false,
    'States how the pen in use and the spare are stored: in use at room temperature, spare in the fridge, never the freezer',
    'চলতি পেন ও বাড়তি পেন কীভাবে রাখতে হয় তা বলতে পারেন: চলতি পেন ঘরের তাপমাত্রায়, বাড়তি পেন ফ্রিজে — কখনোই ডিপ ফ্রিজে নয়',
  ],
] as const;

const PEN_CHECKLIST = {
  code: 'PEN_TECHNIQUE',
  device_type: 'INSULIN_PEN',
  title_en: 'Insulin pen technique',
  title_bn: 'ইনসুলিন পেন ব্যবহারের কৌশল',
  items: PEN_ITEMS.map(([ordinal, code, critical, en, bn]) => ({
    ordinal,
    code,
    text_en: en,
    text_bn: bn,
    is_critical: critical,
  })),
};

const SCALE = {
  code: 'IMPROVEMENT_1_10',
  question_en: 'Compared with your last visit, how do you feel now?',
  question_bn: 'গতবারের তুলনায় এখন আপনার কেমন লাগছে?',
  min_value: 1,
  max_value: 10,
  neutral_value: 5,
  anchors: [
    {
      from_value: 1,
      to_value: 2,
      label_en: 'Much worse',
      label_bn: 'অনেক খারাপ',
      face_rank: 1,
      ordering: 10,
    },
    {
      from_value: 3,
      to_value: 4,
      label_en: 'A little worse',
      label_bn: 'একটু খারাপ',
      face_rank: 2,
      ordering: 20,
    },
    {
      from_value: 5,
      to_value: 5,
      label_en: 'About the same',
      label_bn: 'আগের মতোই',
      face_rank: 3,
      ordering: 30,
    },
    {
      from_value: 6,
      to_value: 7,
      label_en: 'A little better',
      label_bn: 'একটু ভালো',
      face_rank: 4,
      ordering: 40,
    },
    {
      from_value: 8,
      to_value: 10,
      label_en: 'Much better',
      label_bn: 'অনেক ভালো',
      face_rank: 5,
      ordering: 50,
    },
  ],
};

const REFERENCE = {
  device_types: [
    { code: 'INSULIN_PEN', name_en: 'Insulin pen', name_bn: 'ইনসুলিন পেন', ordering: 10 },
  ],
  checklists: [PEN_CHECKLIST],
  states: STATES,
  score_scale: SCALE,
  not_applicable_reasons: [
    {
      code: 'first_visit',
      display_en: 'First visit - there is no last visit to compare with',
      display_bn: 'প্রথম আসা — তুলনা করার মতো আগের কোনো দিন নেই',
      ordering: 10,
    },
  ],
  missed_dose_reasons: [
    {
      code: 'cost',
      display_en: 'The medicine cost too much',
      display_bn: 'ওষুধের দাম বেশি পড়ে যাচ্ছিল',
      ordering: 10,
    },
    { code: 'forgot', display_en: 'Forgot', display_bn: 'মনে ছিল না', ordering: 20 },
    {
      code: 'side_effects',
      display_en: 'Side effects',
      display_bn: 'ওষুধ খেলে শরীর খারাপ লাগছিল',
      ordering: 30,
    },
    {
      code: 'ran_out',
      display_en: 'Ran out of the medicine',
      display_bn: 'ওষুধ শেষ হয়ে গিয়েছিল',
      ordering: 40,
    },
    {
      code: 'felt_well_enough',
      display_en: 'Felt well enough to stop',
      display_bn: 'ভালো লাগছিল, তাই খাওয়া বন্ধ রেখেছিলাম',
      ordering: 50,
    },
    {
      code: 'could_not_come',
      display_en: 'Could not get to the clinic',
      display_bn: 'ক্লিনিকে আসতে পারিনি',
      ordering: 60,
    },
  ],
  reeducation_policy: { unable_raises_flag: true, corrected_today_threshold: 3 },
  compliance_question_en:
    'Most people miss a dose sometimes. In the last week, how many times did you miss?',
  compliance_question_bn:
    'প্রায় সবারই কোনো না কোনো দিন ওষুধ বাদ পড়ে। গত এক সপ্তাহে আপনার কতবার বাদ পড়েছে?',
};

/**
 * What the last officer saw, three months ago. One item she was corrected on, one she had right.
 *
 * The items are looked up by code rather than by position: the checklist is spec §6.1's, and an
 * item inserted into it would silently move every index after it — which would put last visit's
 * answer against the wrong line, on the one part of the screen whose whole job is to say what to
 * check again.
 */
function penItem(code: string) {
  const found = PEN_CHECKLIST.items.find((item) => item.code === code);
  if (!found) throw new Error(`${code} is not on the pen checklist`);
  return found;
}

const PRIOR: Competency[] = [
  {
    code: 'EDU_PEN_02',
    state: 'corrected_today',
    checklist_code: 'PEN_TECHNIQUE',
    ordinal: 2,
    text_en: penItem('EDU_PEN_02').text_en,
    text_bn: penItem('EDU_PEN_02').text_bn,
    is_critical: true,
    observed_at: '2026-06-16T05:20:00Z',
    visit_id: '0190d820-0000-7000-8000-000000008803',
    recorded_by: SHIRIN,
    recorded_role: 'RX_EDUCATOR',
    recorded_at: '2026-06-16T05:20:00Z',
    station_code: 'STN_RX_EDUCATION',
    source: 'STATION',
  },
  {
    code: 'EDU_PEN_09',
    state: 'demonstrated',
    checklist_code: 'PEN_TECHNIQUE',
    ordinal: 9,
    text_en: penItem('EDU_PEN_09').text_en,
    text_bn: penItem('EDU_PEN_09').text_bn,
    is_critical: false,
    observed_at: '2026-06-16T05:22:00Z',
    visit_id: '0190d820-0000-7000-8000-000000008803',
    recorded_by: SHIRIN,
    recorded_role: 'RX_EDUCATOR',
    recorded_at: '2026-06-16T05:22:00Z',
    station_code: 'STN_RX_EDUCATION',
    source: 'STATION',
  },
];

const SESSION: Session = {
  patient_id: PATIENT,
  visit_id: VISIT,
  checklists: [PEN_CHECKLIST],
  selected_devices: [
    {
      checklist_code: 'PEN_TECHNIQUE',
      device_type: 'INSULIN_PEN',
      product_id: '0190d820-0000-7000-8000-000000008820',
      // Exactly what the server sends: `product_label` is the formulary's trade name, copied
      // onto the prescription line when it was written (CP62/CP75). An earlier version of this
      // fixture glued the strength onto the end of it — "Mixtard 30 FlexPen 30% + 70% in
      // 100 IU/mL" — which is a string no part of the application produces, and which read as
      // data concatenation rather than as a clinician writing a medicine down.
      product_label: 'Mixtard 30',
      generic_name: 'Regular insulin human + Isophane insulin human (premix)',
    },
  ],
  improvement: null,
  first_visit: false,
  prior_competency: PRIOR,
  reeducation_flagged: false,
  // Nothing on this sheet is a device the station failed to recognise. The field is present
  // rather than omitted because an absent list and an empty one are the same fact here and the
  // contract requires it; the warning it drives has its own picture below.
  unclassified_devices: [],
};

/**
 * The same visit, half an hour later, as the consultant reads it (CP92 criterion 2).
 *
 * This is the state the physician's screen exists for and the one the officer's screen never
 * shows: the assessment is over, Shirin has gone back to the queue, and the ten rows are now
 * facts with her name on them. Rahima was corrected on the air-shot and could not state her
 * storage rule, she said she missed three doses in the week because the pen ran out and the next
 * one cost more than she had, and she said she feels a little better than last time.
 *
 * The score is deliberately a 7 and deliberately drawn in the read mode: a 7 that can be nudged
 * to an 8 by the man whose treatment it grades is the exact failure CP88 §1 exists to prevent,
 * and the picture is how anybody checks that it cannot be.
 */
const TODAY_STATES: Record<string, string> = {
  EDU_PEN_04: 'corrected_today',
  EDU_PEN_10: 'unable',
};

const ASSESSED: Competency[] = PEN_ITEMS.map(([ordinal, code, critical, en, bn]) => ({
  code,
  state: (TODAY_STATES[code] ?? 'demonstrated') as Competency['state'],
  checklist_code: 'PEN_TECHNIQUE',
  ordinal,
  text_en: en,
  text_bn: bn,
  is_critical: critical,
  observed_at: '2026-09-14T05:40:00Z',
  visit_id: VISIT,
  recorded_by: SHIRIN,
  recorded_role: 'RX_EDUCATOR',
  recorded_at: '2026-09-14T05:41:00Z',
  station_code: 'STN_RX_EDUCATION',
  source: 'STATION',
}));

const RECORDED_SESSION: Session = {
  ...SESSION,
  improvement: { score: 7 },
  improvement_record: {
    answer: { score: 7 },
    recorded_by: SHIRIN,
    recorded_role: 'RX_EDUCATOR',
    recorded_at: '2026-09-14T05:42:00Z',
    effective_at: '2026-09-14T05:42:00Z',
    station_code: 'STN_RX_EDUCATION',
    source: 'PATIENT',
  },
  compliance_record: {
    missed_doses: 3,
    reasons: ['ran_out', 'cost'],
    recorded_by: SHIRIN,
    recorded_role: 'RX_EDUCATOR',
    recorded_at: '2026-09-14T05:41:30Z',
    effective_at: '2026-09-14T05:41:30Z',
    station_code: 'STN_RX_EDUCATION',
    source: 'PATIENT',
  },
  prior_competency: ASSESSED,
  reeducation_flagged: true,
};

async function station(page: import('@playwright/test').Page, session: Session = SESSION) {
  await page.route('**/v1/education/reference', (route) => route.fulfill(json(REFERENCE)));
  await page.route(`**/v1/patients/${PATIENT}/education*`, (route) => {
    if (route.request().method() !== 'GET') {
      // The reply the server gives for the assessment below: nine technique items and the flag
      // it computed from them. The count matters in the picture — "0 values recorded" beside a
      // raised flag would be a screenshot of a bug that is not there.
      return route.fulfill(
        json({
          observations: Array.from({ length: 10 }, (_, index) => ({ id: `row-${index}` })),
          reeducation_flagged: true,
          unable: 1,
          corrected_today: 3,
        }),
      );
    }
    return route.fulfill(json(session));
  });
  await page.route('**/v1/directory', (route) =>
    route.fulfill(
      json({
        staff: [
          {
            id: RINA,
            name_en: 'Rina Parvin',
            name_bn: 'রিনা পারভীন',
            code: 'E204',
            status: 'active',
          },
          {
            id: SHIRIN,
            name_en: 'Shirin Akter',
            name_bn: 'শিরীন আক্তার',
            code: 'E311',
            status: 'active',
          },
        ],
        devices: [],
        stations: [],
        as_of: '2026-09-14T04:00:00Z',
      }),
    ),
  );
  await page.goto(`/education?patient=${PATIENT}&visit=${VISIT}`);
  await expect(page.getByTestId('education-station')).toBeVisible();
}

async function shoot(page: import('@playwright/test').Page, name: string) {
  // A settled page: the fonts matter more here than anywhere else in this suite, because half
  // these pictures exist to show that Bengali conjuncts render.
  await page.evaluate(() => document.fonts.ready);
  await page.screenshot({ path: `${OUT}/${name}.png`, fullPage: true });
}

// --- CP88: the score selector ------------------------------------------------

test('the improvement score selector', async ({ officer: page }) => {
  await station(page);
  await expect(page.getByTestId('improvement-score')).toBeVisible();
  await page.getByTestId('score-7').click();
  await shoot(page, 'cp88-score-selector-en');
});

test('the improvement score selector, in Bangla', async ({ officerBangla: page }) => {
  await station(page);
  await expect(page.getByTestId('improvement-score')).toBeVisible();
  await page.getByTestId('score-7').click();
  await shoot(page, 'cp88-score-selector-bn');
});

// --- CP92: the checklist mid-assessment --------------------------------------

/**
 * A mix of all three states, which is what a real assessment looks like.
 *
 * Two corrected, one unable, the rest demonstrated: she resuspends the premix now but was
 * corrected on the air-shot, and she cannot state her storage rule. That is a patient the record
 * should be able to describe, and it is the case a boolean design cannot.
 */
async function midAssessment(page: import('@playwright/test').Page) {
  await station(page);
  const answers: Array<[string, string]> = [
    ['EDU_PEN_01', 'demonstrated'],
    ['EDU_PEN_02', 'demonstrated'],
    ['EDU_PEN_03', 'demonstrated'],
    ['EDU_PEN_04', 'corrected_today'],
    ['EDU_PEN_05', 'demonstrated'],
    ['EDU_PEN_06', 'corrected_today'],
    ['EDU_PEN_07', 'demonstrated'],
    ['EDU_PEN_08', 'corrected_today'],
    ['EDU_PEN_10', 'unable'],
  ];
  for (const [code, state] of answers) {
    await page.getByTestId(`state-${code}-${state}`).click();
  }
}

test('the pen checklist, mid-assessment', async ({ officer: page }) => {
  await midAssessment(page);
  await shoot(page, 'cp92-pen-checklist-en');
});

test('the pen checklist, mid-assessment, in Bangla', async ({ officerBangla: page }) => {
  await midAssessment(page);
  await shoot(page, 'cp92-pen-checklist-bn');
});

// --- CP92: the flag raised ---------------------------------------------------

test('the re-education flag raised', async ({ officer: page }) => {
  await midAssessment(page);
  await page.getByTestId('education-save').click();
  await expect(page.getByTestId('reeducation-flag')).toHaveAttribute('data-raised', 'true');
  await shoot(page, 'cp92-reeducation-flag-en');
});

test('the re-education flag raised, in Bangla', async ({ officerBangla: page }) => {
  await midAssessment(page);
  await page.getByTestId('education-save').click();
  await expect(page.getByTestId('reeducation-flag')).toHaveAttribute('data-raised', 'true');
  await shoot(page, 'cp92-reeducation-flag-bn');
});

// --- CP92: the compliance question -------------------------------------------

test('the compliance question', async ({ officer: page }) => {
  await station(page);
  await page.getByTestId('missed-3').click();
  await page.getByTestId('reason-cost').click();
  await page.getByTestId('reason-ran_out').click();
  await expect(page.getByTestId('compliance-reasons')).toBeVisible();
  await shoot(page, 'cp92-compliance-en');
});

test('the compliance question, in Bangla', async ({ officerBangla: page }) => {
  await station(page);
  await page.getByTestId('missed-3').click();
  await page.getByTestId('reason-cost').click();
  await page.getByTestId('reason-ran_out').click();
  await expect(page.getByTestId('compliance-reasons')).toBeVisible();
  await shoot(page, 'cp92-compliance-bn');
});

// --- CP92 criterion 2: the station as the physician reads it -----------------

/**
 * The consultant's view, and the absence of every control in it.
 *
 * The assertions are as much a part of the picture as the picture is: a screenshot proves a
 * screen looked right on one afternoon, and `toHaveCount(0)` proves the thing that must stay
 * true. What is checked is the *absence* of the controls rather than the presence of a flag,
 * because a flag can be true while the buttons are drawn anyway.
 */
async function physicianView(page: import('@playwright/test').Page) {
  await station(page, RECORDED_SESSION);
  const screen = page.getByTestId('education-station');
  await expect(screen).toHaveAttribute('data-mode', 'read');
  await expect(page.getByTestId('recorded-assessment')).toBeVisible();
  await expect(page.getByTestId('improvement-score')).toHaveCount(0);
  await expect(page.getByTestId('education-save')).toHaveCount(0);
  // Scoped to the station: the shell has its own buttons — language, role, sign out — and an
  // unscoped count would be asserting something about the chrome instead of about this screen.
  await expect(screen.getByRole('radio')).toHaveCount(0);
  await expect(screen.getByRole('textbox')).toHaveCount(0);
  // The only buttons left are attribution disclosures — the house control that answers "who
  // recorded this" on every clinical value (CP61 §4.2). They reveal and write nothing, and they
  // are the one control the consultant genuinely needs here.
  const buttons = await screen.getByRole('button').all();
  for (const button of buttons) {
    await expect(button).toHaveAttribute('data-testid', 'attribution-trigger');
  }
}

test('the recorded assessment, as the physician reads it', async ({ signedIn: page }) => {
  await physicianView(page);
  await shoot(page, 'cp92-physician-view-en');
});

test('the recorded assessment, as the physician reads it, in Bangla', async ({ bangla: page }) => {
  await physicianView(page);
  await shoot(page, 'cp92-physician-view-bn');
});

// --- CP88 §4: the score beside the measurements ------------------------------

/**
 * The dashboard, with the score as the fifth trend.
 *
 * §4 is the reason this picture exists at all: *"A patient can feel much better with a rising
 * HbA1c — that is precisely the case worth seeing."* Rahima's HbA1c is coming down and her score
 * is rising, which is the reassuring case; what the layout has to make possible is the other one,
 * and it can only do that if the two series sit on the same card.
 *
 * They share no axis. Every sparkline is scaled between its own minimum and maximum — a 1–10
 * score plotted against millimoles per mole would be a flat line at the bottom of somebody else's
 * scale, which is the defect CP73 fixed and the one this checkpoint must not reintroduce.
 */
const observation = (over: Record<string, unknown>) => ({
  id: `0190d820-0000-7000-8000-0000000088${Math.floor(Math.random() * 89 + 10)}`,
  patient_id: PATIENT,
  code: 'HBA1C',
  category: 'LAB',
  value_type: 'numeric',
  value: 70,
  unit: 'mmol/mol',
  entered_value: 8.6,
  entered_unit: '%#ngsp',
  effective_at: '2026-09-14T04:00:00Z',
  recorded_at: '2026-09-14T04:05:00Z',
  source: 'STATION',
  status: 'ACTIVE',
  recorded_by: RINA,
  recorded_role: 'CLINICAL_ASSISTANT',
  station_code: 'STN_EXAMINATION',
  ...over,
});

const score = (value: number, at: string) =>
  observation({
    code: 'IMPROVEMENT_SCORE',
    category: 'PRO',
    value,
    unit: '1',
    entered_value: value,
    entered_unit: '1',
    effective_at: at,
    recorded_at: at,
    source: 'PATIENT',
    recorded_by: SHIRIN,
    recorded_role: 'RX_EDUCATOR',
    station_code: 'STN_RX_EDUCATION',
  });

const DASHBOARD = {
  as_of: '2026-09-14T04:10:00Z',
  patient: {
    id: PATIENT,
    clinical_id: 'DTHC-FRD-2026-000901',
    name_en: 'Mosammat Rahima Begum',
    name_bn: 'মোসাম্মৎ রহিমা বেগম',
    sex: 'female',
    birth_date: '1965-04-17',
    age_text: '61y',
    age_months: 736,
    status: 'active',
  },
  visit: {
    id: VISIT,
    visit_code: 'V-0914-017',
    visit_type: 'FOLLOW_UP',
    status: 'open',
    open: true,
    chief_complaint: 'feet tingling at night',
    clinic_day: '2026-09-14T00:00:00Z',
    opened_at: '2026-09-14T03:10:00Z',
  },
  access: { basis: 'NORMAL' },
  allergies: { status: 'NO_KNOWN_ALLERGY', satisfied: true, allergies: [] },
  critical_alerts: [],
  vitals: [
    observation({
      code: 'BP_SYSTOLIC',
      value: 146,
      unit: 'mm[Hg]',
      entered_value: 146,
      entered_unit: 'mm[Hg]',
    }),
    observation({
      code: 'BP_DIASTOLIC',
      value: 88,
      unit: 'mm[Hg]',
      entered_value: 88,
      entered_unit: 'mm[Hg]',
    }),
    observation({
      code: 'BODY_WEIGHT',
      value: 68.4,
      unit: 'kg',
      entered_value: 68.4,
      entered_unit: 'kg',
    }),
    observation({}),
  ],
  body_mass: {
    observation: observation({
      code: 'BMI',
      value: 27.6,
      unit: 'kg/m2',
      entered_value: 27.6,
      entered_unit: 'kg/m2',
    }),
    class: 'obese',
    class_version: '1.0.0',
    scale: 'asian',
  },
  trends: [
    {
      code: 'HBA1C',
      unit: 'mmol/mol',
      points: [
        observation({ value: 79, entered_value: 9.4, effective_at: '2025-12-08T04:00:00Z' }),
        observation({ value: 75, entered_value: 9.0, effective_at: '2026-03-16T04:00:00Z' }),
        observation({ value: 70, entered_value: 8.6, effective_at: '2026-09-14T04:00:00Z' }),
      ],
      change: { from: 79, to: 70, delta: -9, over_days: 280 },
    },
    {
      code: 'BODY_WEIGHT',
      unit: 'kg',
      points: [
        observation({
          code: 'BODY_WEIGHT',
          value: 68.9,
          unit: 'kg',
          entered_value: 68.9,
          entered_unit: 'kg',
          effective_at: '2025-12-08T04:00:00Z',
        }),
        observation({
          code: 'BODY_WEIGHT',
          value: 68.1,
          unit: 'kg',
          entered_value: 68.1,
          entered_unit: 'kg',
          effective_at: '2026-03-16T04:00:00Z',
        }),
        observation({
          code: 'BODY_WEIGHT',
          value: 68.4,
          unit: 'kg',
          entered_value: 68.4,
          entered_unit: 'kg',
          effective_at: '2026-09-14T04:00:00Z',
        }),
      ],
      change: { from: 68.9, to: 68.4, delta: -0.5, over_days: 280 },
    },
    {
      code: 'IMPROVEMENT_SCORE',
      unit: '1',
      points: [
        score(4, '2025-12-08T06:40:00Z'),
        score(6, '2026-03-16T06:20:00Z'),
        score(7, '2026-09-14T06:05:00Z'),
      ],
      change: { from: 4, to: 7, delta: 3, over_days: 280 },
    },
  ],
  active_conditions: [],
  growth: null,
  counseling: { visit_id: VISIT, blocked: false, overridden: false, missing: [] },
  summary: null,
  assistant: null,
  omitted: [],
};

async function dashboard(page: import('@playwright/test').Page) {
  // A floor under every *patient* read this screen makes, registered first so the specific
  // routes below win.
  //
  // It is here because the suite was flaky without it, and the flakiness was informative: a
  // request nobody routed leaves the browser, finds no backend, and arrives at the screen as a
  // NetworkError — which the shell correctly draws as "this request did not reach the clinic
  // server". The dashboard is the widest read in the application and calls several modules; a
  // picture of it that depends on having remembered all of them is a picture that fails on the
  // morning somebody adds a panel.
  //
  // Scoped to `/v1/patients/` and not to `/v1/`, and the difference is not tidiness. Playwright
  // matches the most recently registered route first, so a floor across the whole API would sit
  // in front of the session fixture's `/v1/auth/me` and answer it with an empty object — which
  // the shell reads as a session that has gone, and the picture becomes the sign-in screen.
  // That is exactly what happened on the first attempt.
  await page.route('**/v1/patients/**', (route) => route.fulfill(json({})));
  await page.route('**/v1/education/reference', (route) => route.fulfill(json(REFERENCE)));
  await page.route(`**/v1/patients/${PATIENT}/dashboard*`, (route) =>
    route.fulfill(json(DASHBOARD)),
  );
  await page.route(`**/v1/patients/${PATIENT}/alerts*`, (route) =>
    route.fulfill(json({ alerts: [] })),
  );
  await page.route('**/v1/directory', (route) =>
    route.fulfill(
      json({
        staff: [
          {
            id: RINA,
            name_en: 'Rina Parvin',
            name_bn: 'রিনা পারভীন',
            code: 'E204',
            status: 'active',
          },
          {
            id: SHIRIN,
            name_en: 'Shirin Akter',
            name_bn: 'শিরীন আক্তার',
            code: 'E311',
            status: 'active',
          },
        ],
        devices: [],
        stations: [],
        as_of: '2026-09-14T04:00:00Z',
      }),
    ),
  );
  await page.goto(`/dashboard?patient=${PATIENT}&visit=${VISIT}`);
  await expect(page.getByTestId('snapshot-trends')).toBeVisible();
}

test('the score beside the measurements', async ({ signedIn: page }) => {
  await dashboard(page);
  await expect(page.getByTestId('sparkline-IMPROVEMENT_SCORE')).toBeVisible();
  await shoot(page, 'cp88-score-trend-en');
});

test('the score beside the measurements, in Bangla', async ({ bangla: page }) => {
  await dashboard(page);
  await expect(page.getByTestId('sparkline-IMPROVEMENT_SCORE')).toBeVisible();
  await shoot(page, 'cp88-score-trend-bn');
});

// --- CP92 §6.4: a GLP-1 the station does not recognise -----------------------

/**
 * The picture the fallback decision exists for.
 *
 * §6.4 gives the GLP-1 rules no class-level rule behind them, so a molecule the formulary gains
 * next year selects no checklist. That is the safe answer — inheriting semaglutide's weekly
 * checklist would tell a liraglutide-style daily patient to inject on Fridays — but it is only
 * safe if somebody is told, because an empty checklist list is also what a patient on tablets
 * alone looks like.
 *
 * This is what "told" looks like on the officer's screen, with the patient still in front of
 * them.
 */
const UNRECOGNISED: Session = {
  ...SESSION,
  checklists: [],
  selected_devices: [],
  unclassified_devices: [
    {
      product_id: '0190d820-0000-7000-8000-000000008830',
      generic_name: 'Tirzepatide',
      class_code: 'GLP_1_RECEPTOR_AGONIST',
      class_name_en: 'GLP-1 receptor agonist',
      class_name_bn: 'জিএলপি-১ রিসেপ্টর অ্যাগোনিস্ট',
    },
  ],
};

test('a device the station does not recognise', async ({ officer: page }) => {
  await station(page, UNRECOGNISED);
  await expect(page.getByTestId('unclassified-devices')).toBeVisible();
  await shoot(page, 'cp92-unrecognised-device-en');
});

test('a device the station does not recognise, in Bangla', async ({ officerBangla: page }) => {
  await station(page, UNRECOGNISED);
  await expect(page.getByTestId('unclassified-devices')).toBeVisible();
  await shoot(page, 'cp92-unrecognised-device-bn');
});
