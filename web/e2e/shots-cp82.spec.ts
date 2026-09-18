import { expect, test } from './fixtures';

/**
 * The AI suggestion panel, photographed (CP82).
 *
 * # Why this suite mocks the API and `cp81-prescribe.spec.ts` does not
 *
 * That suite measures *numbers* — how long a four-item prescription takes, how long a safety
 * finding takes to appear — and a number measured against a mocked API is a measurement of React.
 * This one photographs *states*, and the states it has to photograph include two a live stack
 * cannot be made to produce on demand: a model that proposed nothing, and a run refused because
 * the patient has no recorded allergy status. Driving a real Gemini call into each of those would
 * make the pictures a function of what a model happened to say that afternoon.
 *
 * So the API is answered from a literal, and the literal is a real answer: the shapes below are
 * what `internal/prescription` actually serialises, and the Go tests are what prove it does.
 *
 * # The patient
 *
 * Shefali Khatun, 54, a follow-up diabetic at the Faridpur clinic — the picture this software was
 * built for, and one whose numbers are plausible rather than round. The suggestion is metformin at
 * a dose the clinic's own guidance carries, declined in one case for a reason a physician in
 * Faridpur actually gives.
 */

const OUT = 'shots';
const PATIENT = '0190d820-0000-7000-8000-0000000004a1';
const VISIT = '0190d820-0000-7000-8000-0000000004b1';
const PRESCRIPTION = '0190d820-0000-7000-8000-0000000004c1';
const SUGGESTION_MET = '0190d820-0000-7000-8000-0000000004d1';
const SUGGESTION_ATOR = '0190d820-0000-7000-8000-0000000004d2';
const ITEM = '0190d820-0000-7000-8000-0000000004e1';

const json = (body: unknown) => ({
  status: 200,
  contentType: 'application/json',
  body: JSON.stringify(body),
});

const REASONS = [
  {
    code: 'NOT_INDICATED',
    label_en: 'Not indicated for this patient',
    label_bn: 'এই রোগীর ক্ষেত্রে প্রযোজ্য নয়',
    ordering: 10,
  },
  {
    code: 'CONTRAINDICATED',
    label_en: 'Contraindicated — renal, hepatic or cardiac',
    label_bn: 'প্রতিনির্দেশিত — কিডনি, লিভার বা হৃদযন্ত্রজনিত',
    ordering: 20,
  },
  {
    code: 'ALLERGY',
    label_en: 'Allergy or previous adverse reaction',
    label_bn: 'অ্যালার্জি বা পূর্বে বিরূপ প্রতিক্রিয়া',
    ordering: 30,
  },
  {
    code: 'INTERACTION',
    label_en: 'Interacts with current therapy',
    label_bn: 'বর্তমান ওষুধের সঙ্গে বিক্রিয়া',
    ordering: 40,
  },
  {
    code: 'DUPLICATE',
    label_en: 'Duplicates therapy already prescribed',
    label_bn: 'ইতিমধ্যে দেওয়া ওষুধেরই পুনরাবৃত্তি',
    ordering: 50,
  },
  {
    code: 'WRONG_DOSE',
    label_en: 'Dose, frequency or duration wrong',
    label_bn: 'মাত্রা, সময় বা মেয়াদ ঠিক নেই',
    ordering: 60,
  },
  {
    code: 'PREFER_ALTERNATIVE',
    label_en: 'Prefer a different agent in this class',
    label_bn: 'এই শ্রেণিতে অন্য ওষুধ পছন্দ',
    ordering: 70,
  },
  {
    code: 'COST',
    label_en: 'Patient cannot afford it',
    label_bn: 'রোগীর সাধ্যের বাইরে',
    ordering: 80,
  },
  {
    code: 'AVAILABILITY',
    label_en: 'Not reliably available',
    label_bn: 'নিয়মিত পাওয়া যায় না',
    ordering: 90,
  },
  {
    code: 'ADHERENCE',
    label_en: 'Adherence or patient preference',
    label_bn: 'রোগীর পছন্দ বা নিয়ম মেনে চলার সমস্যা',
    ordering: 100,
  },
  {
    code: 'TOO_EARLY',
    label_en: 'Defer — reassess at the next visit',
    label_bn: 'এখন নয় — পরের বার পুনর্বিবেচনা',
    ordering: 110,
  },
  {
    code: 'INSUFFICIENT_DATA',
    label_en: 'Not enough information to decide',
    label_bn: 'সিদ্ধান্ত নেওয়ার মতো তথ্য নেই',
    ordering: 120,
  },
];

function suggestion(over: Record<string, unknown> = {}) {
  return {
    id: SUGGESTION_MET,
    run_id: '0190d820-0000-7000-8000-0000000004f1',
    ordinal: 1,
    product_id: '0190d820-0000-7000-8000-000000000501',
    product_label: 'Comet',
    generic_name: 'Metformin hydrochloride',
    strength: '500 mg',
    form_code: 'TABLET',
    dose: '500 mg',
    frequency: 'twice daily',
    duration_days: 30,
    route: 'oral',
    rationale_en:
      'HbA1c is 9.1% on gliclazide alone and the eGFR is 74, so metformin is not contraindicated. Adding it is the usual next step at this clinic before an injectable is considered.',
    rationale_bn:
      'কেবল গ্লিক্লাজাইডে HbA1c ৯.১%, eGFR ৭৪ — তাই মেটফরমিন প্রতিনির্দেশিত নয়। ইনজেকশনের কথা ভাবার আগে এই ক্লিনিকে সাধারণত এটিই পরবর্তী ধাপ।',
    // The raw references, kept because that is what CP72's grounding arm validated and what an
    // engineer greps for — and rendered by the server into the two fields the panel shows
    // (ADR-0038). A physician scanning three cards reads the second pair, not the first.
    basis: ['obs.hba1c:2026-09-01', 'obs.egfr:2026-09-01', 'dx.type_2_diabetes_mellitus'],
    basis_en: ['HbA1c, 1 Sep 2026', 'eGFR (CKD-EPI 2021), 1 Sep 2026', 'Type 2 diabetes mellitus'],
    basis_bn: ['এইচবিএ১সি, ১ সেপ্ট ২০২৬', 'ইজিএফআর, ১ সেপ্ট ২০২৬', 'Type 2 diabetes mellitus'],
    offered_at: '2026-09-13T09:41:00Z',
    ...over,
  };
}

function statin(over: Record<string, unknown> = {}) {
  return {
    id: SUGGESTION_ATOR,
    run_id: '0190d820-0000-7000-8000-0000000004f1',
    ordinal: 2,
    product_id: '0190d820-0000-7000-8000-000000000502',
    product_label: 'Atova',
    generic_name: 'Atorvastatin',
    strength: '20 mg',
    form_code: 'TABLET',
    dose: '20 mg',
    frequency: 'at night',
    duration_days: 30,
    route: 'oral',
    rationale_en:
      'LDL is 4.1 mmol/L with type 2 diabetes recorded, and nothing lipid-lowering is on the sheet.',
    rationale_bn:
      'টাইপ ২ ডায়াবেটিস নথিভুক্ত, LDL ৪.১ mmol/L, আর ব্যবস্থাপত্রে চর্বি কমানোর কোনো ওষুধ নেই।',
    basis: ['obs.chol_ldl:2026-09-01', 'dx.type_2_diabetes_mellitus'],
    basis_en: ['LDL cholesterol, 1 Sep 2026', 'Type 2 diabetes mellitus'],
    basis_bn: ['এলডিএল, ১ সেপ্ট ২০২৬', 'Type 2 diabetes mellitus'],
    offered_at: '2026-09-13T09:41:00Z',
    ...over,
  };
}

function run(over: Record<string, unknown> = {}) {
  return {
    id: '0190d820-0000-7000-8000-0000000004f1',
    prescription_id: PRESCRIPTION,
    patient_id: PATIENT,
    visit_id: VISIT,
    state: 'READY',
    ai_interaction_id: '0190d820-0000-7000-8000-000000000601',
    prompt_version: '1.0.0',
    model_version: 'gemini-2.5-flash-001',
    offered_count: 2,
    dropped_count: 0,
    requested_at: '2026-09-13T09:41:00Z',
    requested_by: '0190a8f2-0000-7000-8000-00000000000a',
    suggestions: [suggestion(), statin()],
    message_en: 'AI-proposed, and not prescribed. Each one needs your accept, edit or reject.',
    message_bn:
      'এআই-এর প্রস্তাব, ব্যবস্থাপত্র নয়। প্রতিটির জন্য আপনার গ্রহণ, সংশোধন বা বাতিল প্রয়োজন।',
    ...over,
  };
}

function item(over: Record<string, unknown> = {}) {
  return {
    id: ITEM,
    line_no: 1,
    product_id: '0190d820-0000-7000-8000-000000000503',
    product_label: 'Comet',
    generic_name: 'Metformin hydrochloride',
    strength: '500 mg',
    form_code: 'TABLET',
    dose: '500 mg',
    frequency: 'twice daily',
    duration_days: 30,
    route: 'oral',
    instructions_en: 'Take after food.',
    instructions_bn: 'খাবারের পরপরই খাবেন।',
    recorded_at: '2026-09-13T09:42:00Z',
    recorded_by: '0190a8f2-0000-7000-8000-00000000000a',
    ...over,
  };
}

function prescription(items: unknown[]) {
  return {
    prescription: {
      id: PRESCRIPTION,
      facility_id: '11111111-1111-4111-8111-111111111111',
      patient_id: PATIENT,
      visit_id: VISIT,
      status: 'DRAFT',
      status_name_en: 'Draft',
      status_name_bn: 'খসড়া',
      created_at: '2026-09-13T09:40:00Z',
      created_by: '0190a8f2-0000-7000-8000-00000000000a',
      updated_at: '2026-09-13T09:42:00Z',
      items,
      editable: true,
    },
    available_transitions: [],
  };
}

const PRINT_MODEL = {
  version: '1',
  generated_at: '2026-09-13T09:42:00Z',
  content_hash: 'a1b2c3d4',
  prescription_id: PRESCRIPTION,
  status: 'DRAFT',
  status_caveat_en:
    'DRAFT — not signed. This is not a prescription and must not be dispensed against.',
  status_caveat_bn:
    'খসড়া — স্বাক্ষরিত নয়। এটি ব্যবস্থাপত্র নয় এবং এর ভিত্তিতে ওষুধ দেওয়া যাবে না।',
  patient: {
    clinical_id: 'DTHC-FRD-2026-001482',
    name_en: 'Shefali Khatun',
    name_bn: 'শেফালী খাতুন',
    sex_en: 'Female',
    sex_bn: 'মহিলা',
    age_years: 54,
    age_text_en: '54 years',
    age_text_bn: '৫৪ বছর',
    written_on: '2026-09-13',
    visit_id: VISIT,
    resolved: true,
  },
  lines: [],
  omitted: [],
  price: {
    lines_with_price: 0,
    lines_no_price: 0,
    lines_provisional: 0,
    caveat_en: '',
    caveat_bn: '',
  },
  signature: {
    signed: false,
    note_en: 'Not signed. Signing arrives with CP84; nothing stands in this space yet.',
    note_bn: 'স্বাক্ষরিত নয়। স্বাক্ষরের ব্যবস্থা CP84-এ আসবে; এই জায়গায় এখনও কিছু নেই।',
  },
};

interface Scene {
  run: unknown;
  items: unknown[];
}

function routes(page: import('@playwright/test').Page, scene: Scene) {
  return page.route('**/v1/**', (route) => {
    const path = new URL(route.request().url()).pathname;
    if (route.request().method() !== 'GET') return route.fallback();

    if (path.endsWith('/ai-suggestion-reject-reasons'))
      return route.fulfill(json({ reasons: REASONS, total: REASONS.length }));
    if (path.endsWith('/ai-suggestions')) return route.fulfill(json(scene.run));
    if (path.endsWith(`/patients/${PATIENT}/visits`))
      return route.fulfill(
        json({ visits: [{ id: VISIT, status: 'open', visit_code: 'V-2026-1482' }] }),
      );
    if (path.endsWith(`/patients/${PATIENT}/prescriptions`))
      return route.fulfill(json({ prescriptions: [prescription(scene.items)] }));
    if (path.endsWith(`/prescriptions/${PRESCRIPTION}`))
      return route.fulfill(json(prescription(scene.items)));
    if (path.endsWith('/print-model')) return route.fulfill(json(PRINT_MODEL));
    if (path.endsWith('/prescribing-defaults'))
      return route.fulfill(json({ defaults: [], total: 0, approved: 0 }));
    if (path.endsWith('/instruction-templates'))
      return route.fulfill(json({ templates: [], total: 0, approved: 0 }));
    if (path.endsWith(`/patients/${PATIENT}`))
      return route.fulfill(
        json({
          patient: {
            id: PATIENT,
            clinical_id: 'DTHC-FRD-2026-001482',
            name_en: 'Shefali Khatun',
            name_bn: 'শেফালী খাতুন',
            sex: 'female',
            birth_date: '1972-02-11',
            dob_precision: 'day',
            status: 'active',
            phone_primary: '+8801712004821',
            registered_at: '2026-01-19T04:10:00Z',
          },
        }),
      );
    if (path.endsWith('/v1/directory'))
      return route.fulfill(
        json({ staff: [], devices: [], stations: [], as_of: '2026-09-13T00:00:00Z' }),
      );
    if (path.endsWith('/allergies/history')) return route.fulfill(json({ changes: [] }));
    if (path.endsWith('/allergies'))
      return route.fulfill(
        json({
          status: 'ALLERGIES_RECORDED',
          satisfied: true,
          allergies: [
            {
              id: '0190d820-0000-7000-8000-000000000801',
              patient_id: PATIENT,
              display_en: 'Sulphonamides',
              display_bn: 'সালফোনামাইড',
              said: 'sulpha tablets',
              reaction: 'rash',
              reaction_en: 'Rash',
              reaction_bn: 'ফুসকুড়ি',
              is_emergency: false,
              severity: 'moderate',
              certainty: 'confirmed',
              recorded_at: '2026-01-19T05:02:00Z',
              recorded_by: '0190a8f2-0000-7000-8000-00000000000a',
              recorded_role: 'HISTORY',
            },
          ],
        }),
      );
    if (path.endsWith('/renal-status'))
      return route.fulfill(
        json({
          known: true,
          egfr: 74.2,
          egfr_as_of: '2026-09-01T04:00:00Z',
          age_days: 12,
          stale: false,
          expires_at: '2027-03-01T04:00:00Z',
          stage: 'G2',
          stage_label_en: 'Mildly reduced',
          stage_label_bn: 'সামান্য কমেছে',
          window_days: 180,
          window_approved: false,
        }),
      );
    // Everything else — `/v1/auth/me`, the directory, the alert badge — is the signed-in
    // fixture's, registered before this handler and therefore reached by falling back to it.
    //
    // A GET neither answers is a screenshot of an error boundary rather than of the panel, so
    // when this suite is extended and a picture comes back wrong, the first thing to do is log
    // the path here: every state below was got working that way.
    return route.fallback();
  });
}

async function shoot(
  page: import('@playwright/test').Page,
  scene: Scene,
  name: string,
  { open = true }: { open?: boolean } = {},
) {
  await routes(page, scene);
  await page.setViewportSize({ width: 1320, height: 1500 });
  await page.goto(`/patients/${PATIENT}/prescribe`);
  await page.getByTestId('ai-panel').waitFor();
  if (open) {
    await page.getByTestId('ai-toggle').click();
    await page.getByTestId('ai-panel-body').waitFor();
  }
  await page.waitForTimeout(400);
  await page.screenshot({ path: `${OUT}/${name}.png`, fullPage: true });
}

/* ------------------------------------------------------------------------- */
/* 1. Suggestions present                                                     */
/* ------------------------------------------------------------------------- */

test('suggestions present', async ({ signedIn: page }) => {
  await shoot(page, { run: run(), items: [] }, 'cp82-suggestions-en');
  // The picture is only worth taking if the panel really says these are not prescribed.
  await expect(page.getByTestId('ai-pending-count')).toContainText('2');
});

test('suggestions present, in Bangla', async ({ bangla: page }) => {
  await shoot(page, { run: run(), items: [] }, 'cp82-suggestions-bn');
});

/* ------------------------------------------------------------------------- */
/* 2. One accepted                                                            */
/* ------------------------------------------------------------------------- */

const accepted = {
  id: '0190d820-0000-7000-8000-000000000701',
  suggestion_id: SUGGESTION_MET,
  decision: 'ACCEPTED',
  decided_by: '0190a8f2-0000-7000-8000-00000000000a',
  decided_at: '2026-09-13T09:42:00Z',
  prescription_item_id: ITEM,
};

test('one accepted, now on the sheet', async ({ signedIn: page }) => {
  await shoot(
    page,
    {
      run: run({ suggestions: [suggestion({ decision: accepted }), statin()] }),
      items: [item()],
    },
    'cp82-accepted-en',
  );
  // The accepted one has left the waiting list and is on the sheet; the panel keeps the record.
  await expect(page.getByTestId('ai-decided')).toContainText('Accepted');
  await expect(page.getByTestId('line')).toContainText('Comet');
});

test('one accepted, in Bangla', async ({ bangla: page }) => {
  await shoot(
    page,
    {
      run: run({ suggestions: [suggestion({ decision: accepted }), statin()] }),
      items: [item()],
    },
    'cp82-accepted-bn',
  );
});

/* ------------------------------------------------------------------------- */
/* 3. The rejection reason list                                               */
/* ------------------------------------------------------------------------- */

const rejected = {
  id: '0190d820-0000-7000-8000-000000000702',
  suggestion_id: SUGGESTION_ATOR,
  decision: 'REJECTED',
  decided_by: '0190a8f2-0000-7000-8000-00000000000a',
  decided_at: '2026-09-13T09:43:00Z',
};

async function shootReasons(page: import('@playwright/test').Page, name: string) {
  await routes(page, {
    run: run({ suggestions: [suggestion(), statin({ decision: rejected })] }),
    items: [],
  });
  await page.setViewportSize({ width: 1320, height: 1700 });
  await page.goto(`/patients/${PATIENT}/prescribe`);
  await page.getByTestId('ai-toggle').click();
  // The reason is offered *after* the rejection, which is what makes dismissing one action.
  await page.getByTestId('ai-add-reason').click();
  await page.getByTestId('ai-reason-list').waitFor();
  await page.waitForTimeout(400);
  await page.screenshot({ path: `${OUT}/${name}.png`, fullPage: true });
}

test('the reason list', async ({ signedIn: page }) => {
  await shootReasons(page, 'cp82-reasons-en');
  await expect(page.getByTestId('ai-reason-list')).toContainText('Patient cannot afford it');
});

test('the reason list, in Bangla', async ({ bangla: page }) => {
  await shootReasons(page, 'cp82-reasons-bn');
  await expect(page.getByTestId('ai-reason-list')).toContainText('রোগীর সাধ্যের বাইরে');
});

/* ------------------------------------------------------------------------- */
/* 4. The AI proposed nothing                                                 */
/* ------------------------------------------------------------------------- */

const NOTHING = run({
  offered_count: 0,
  suggestions: [],
  message_en: 'The AI proposed nothing for this patient. That is an answer, not a failure.',
  message_bn: 'এই রোগীর জন্য এআই কিছু প্রস্তাব করেনি। এটি একটি উত্তর, ব্যর্থতা নয়।',
});

test('the AI proposed nothing', async ({ signedIn: page }) => {
  await shoot(page, { run: NOTHING, items: [] }, 'cp82-nothing-en');
  // The empty state is a sentence, not a blank column. An empty panel would tell the physician
  // nothing about whether the system tried.
  await expect(page.getByTestId('ai-state-message')).toContainText('an answer, not a failure');
});

test('the AI proposed nothing, in Bangla', async ({ bangla: page }) => {
  await shoot(page, { run: NOTHING, items: [] }, 'cp82-nothing-bn');
});
