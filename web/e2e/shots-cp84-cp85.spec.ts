import { expect, test } from './fixtures';

/**
 * Signing a prescription, and the page a stranger checks it on, photographed (CP84, CP85).
 *
 * # Why the API is answered from literals
 *
 * These are pictures of *states*, and the states that matter cannot be produced on demand from a
 * live stack: a cleared prescription waiting for a signature, the same sheet signed, and — the one
 * CP85 exists for — a prescription that has been altered since it was signed. Driving a real
 * backend into the last of those means disabling a freeze trigger as the table's owner, which the
 * Go suite does and a browser cannot.
 *
 * The bodies below are what `internal/signing` actually serialises; `TestTheSigningSchemasDescribe
 * WhatTheServerSerialises` is what holds them to the contract, and the sentences are copied from
 * the Go source rather than paraphrased, so that a wording changed there and not here shows up as
 * a picture that disagrees with the product (ADR-0038).
 *
 * # The patient, and the clinical picture
 *
 * Mosammat Rahima Begum, 61, of Boalmari in Faridpur — station 10's patient from CP83, one visit
 * later. Type 2 diabetes eleven years with hypothyroidism on top of it, which is the commonest
 * combination this clinic sees and the reason it is called what it is. Four medicines: metformin
 * and empagliflozin for the diabetes, rosuvastatin because she is a 61-year-old diabetic, and
 * levothyroxine for the thyroid.
 *
 * **None of that appears on the public page**, which is the point of half these pictures.
 *
 * # Who each picture is taken as
 *
 * The signing ones as Dr K M Nahid Ul Haque, who holds `prescription.sign` and is the only person
 * in the clinic who does. The refusal one as Shirin Akhter at station 10, who reaches the same
 * sheet through `prescription.read` and may not sign it — that is the CP92 defect's shape here, and
 * a screenshot is what found it last time. The public ones as nobody at all: the `stranger` fixture
 * stubs no session, because the reader has never had one.
 */

const OUT = 'shots';

const SHEET = '0190a8f2-0000-7000-8000-0000000000c1';
const PATIENT = '0190a8f2-0000-7000-8000-0000000000b1';
const VISIT = '0190a8f2-0000-7000-8000-0000000000a1';
const FACILITY = '11111111-1111-4111-8111-111111111111';
const NAHID = '0190a8f2-0000-7000-8000-000000000010';
const TOKEN = 'MFRGGZDFMZTWQ2LKNNWG23TPOJZA4YTB';

const json = (body: unknown, status = 200) => ({
  status,
  contentType: 'application/json',
  body: JSON.stringify(body),
});

/** `TheImageIsNotTheSignature()`, word for word. */
const IMAGE_CAVEAT = {
  present_on_file: false,
  caveat_en:
    'This handwritten signature is a picture for readability. What proves this prescription has ' +
    'not been altered is the QR code, not the image.',
  caveat_bn:
    'হাতে লেখা এই স্বাক্ষরটি কেবল পড়ার সুবিধার জন্য একটি ছবি। এই ব্যবস্থাপত্র বদলানো হয়নি — তা ' +
    'প্রমাণ করে কিউআর কোড, ছবিটি নয়।',
};

const ITEMS = [
  {
    id: '0190a8f2-0000-7000-8000-0000000001a1',
    line_no: 1,
    product_label: 'Comet',
    strength: '500 mg',
    generic_name: 'Metformin hydrochloride',
    dose: '1 tablet',
    frequency: 'twice daily',
    duration_days: 30,
    route: 'oral',
    instructions_en: 'After food.',
    instructions_bn: 'খাবারের পরে।',
  },
  {
    id: '0190a8f2-0000-7000-8000-0000000001a2',
    line_no: 2,
    product_label: 'Emjard',
    strength: '10 mg',
    generic_name: 'Empagliflozin',
    dose: '1 tablet',
    frequency: 'once daily',
    duration_days: 30,
    route: 'oral',
    instructions_en: 'In the morning.',
    instructions_bn: 'সকালে।',
  },
  {
    id: '0190a8f2-0000-7000-8000-0000000001a3',
    line_no: 3,
    product_label: 'Rocovas',
    strength: '10 mg',
    generic_name: 'Rosuvastatin',
    dose: '1 tablet',
    frequency: 'at night',
    duration_days: 30,
    route: 'oral',
    instructions_en: 'At bedtime.',
    instructions_bn: 'রাতে ঘুমানোর আগে।',
  },
  {
    id: '0190a8f2-0000-7000-8000-0000000001a4',
    line_no: 4,
    product_label: 'Thyrin',
    strength: '50 mcg',
    generic_name: 'Levothyroxine sodium',
    dose: '1 tablet',
    frequency: 'once daily',
    duration_days: 30,
    route: 'oral',
    instructions_en: 'Empty stomach, thirty minutes before breakfast.',
    instructions_bn: 'খালি পেটে, নাশতার ৩০ মিনিট আগে।',
  },
];

const PATIENT_RECORD = {
  patient: {
    id: PATIENT,
    clinical_id: 'DTHC-FRD-2026-000412',
    name_en: 'Mosammat Rahima Begum',
    name_bn: 'মোসাম্মৎ রহিমা বেগম',
    sex: 'female',
    date_of_birth: '1965-03-11',
    phone: '+8801711204412',
    address_line: 'Boalmari, Faridpur',
    status: 'active',
    facility_id: FACILITY,
    created_at: '2015-06-02T04:10:00Z',
    updated_at: '2026-09-14T03:55:00Z',
  },
};

/** The prescription, submitted and frozen. `editable: false` is what the screen turns on. */
function envelope(status: 'QA_REVIEW' | 'SIGNED') {
  return {
    prescription: {
      id: SHEET,
      patient_id: PATIENT,
      visit_id: VISIT,
      facility_id: FACILITY,
      status,
      status_name_en: status === 'SIGNED' ? 'Signed' : 'Waiting for quality review',
      status_name_bn: status === 'SIGNED' ? 'স্বাক্ষরিত' : 'মান যাচাইয়ের অপেক্ষায়',
      editable: false,
      created_at: '2026-09-14T09:12:00Z',
      updated_at: '2026-09-14T09:24:00Z',
      items: ITEMS,
    },
    available_transitions: [],
  };
}

function printModel(signed: boolean) {
  return {
    version: '1',
    generated_at: '2026-09-14T09:46:00Z',
    content_hash: '4b1d9a0c7e2f3a5b6c8d0e1f2a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d',
    prescription_id: SHEET,
    status: signed ? 'SIGNED' : 'QA_REVIEW',
    status_caveat_en: signed
      ? ''
      : 'This prescription has not been signed. It is not valid for dispensing.',
    status_caveat_bn: signed
      ? ''
      : 'এই ব্যবস্থাপত্রে স্বাক্ষর করা হয়নি। এটি দিয়ে ওষুধ দেওয়া যাবে না।',
    patient: {
      clinical_id: 'DTHC-FRD-2026-000412',
      name_en: 'Mosammat Rahima Begum',
      name_bn: 'মোসাম্মৎ রহিমা বেগম',
      sex_en: 'Female',
      sex_bn: 'মহিলা',
      age_years: 61,
      age_text_en: '61 years',
      age_text_bn: '৬১ বছর',
      written_on: '2026-09-14',
      visit_id: VISIT,
      resolved: true,
    },
    lines: ITEMS.map((item, index) => ({
      ordinal: index + 1,
      item_id: item.id,
      medicine: `${item.product_label} ${item.strength}`,
      generic: item.generic_name,
      directions_en: `${item.dose}, ${item.frequency}, ${item.duration_days} days, oral`,
      directions_bn: bengaliDirections(index),
      instruction_en: item.instructions_en,
      instruction_bn: item.instructions_bn,
      price_text: ['180.00', '760.00', '250.00', '95.00'][index],
      price_unverified: true,
    })),
    price: {
      total_text: '1,285.00',
      caveat_en: 'Prices are what this clinic last recorded and are not a quotation.',
      caveat_bn: 'দামগুলি এই ক্লিনিকে সর্বশেষ লিখে রাখা দাম, কোনো চূড়ান্ত মূল্যতালিকা নয়।',
    },
    signature: signed
      ? {
          signed: true,
          note_en:
            'Signed electronically. Scan the code to check this prescription is genuine and has ' +
            'not been altered.',
          note_bn:
            'ইলেকট্রনিকভাবে স্বাক্ষরিত। এই ব্যবস্থাপত্রটি আসল এবং অপরিবর্তিত কিনা দেখতে কোডটি স্ক্যান করুন।',
          signed_on: '2026-09-14',
          physician_name_en: 'Dr K M Nahid Ul Haque',
          physician_name_bn: 'ডা. কে এম নাহিদ উল হক',
          verification_path: `/verify/${TOKEN}`,
          image_caveat_en: IMAGE_CAVEAT.caveat_en,
          image_caveat_bn: IMAGE_CAVEAT.caveat_bn,
        }
      : {
          signed: false,
          note_en: 'Not signed. This space carries the physician’s signature once it is signed.',
          note_bn: 'স্বাক্ষর করা হয়নি। স্বাক্ষরের পর এই জায়গায় চিকিৎসকের স্বাক্ষর থাকবে।',
        },
    omitted: [],
  };
}

function bengaliDirections(index: number): string {
  return [
    '১টি ট্যাবলেট, দিনে দুইবার, ৩০ দিন, মুখে',
    '১টি ট্যাবলেট, দিনে একবার, ৩০ দিন, মুখে',
    '১টি ট্যাবলেট, রাতে, ৩০ দিন, মুখে',
    '১টি ট্যাবলেট, দিনে একবার, ৩০ দিন, মুখে',
  ][index] as string;
}

const SIGNATURE = {
  prescription_id: SHEET,
  facility_id: FACILITY,
  canonical_version: 1,
  canonical_sha256: '9f2c1f0e5b7a6d4c3e2f1a0b9c8d7e6f5a4b3c2d1e0f9a8b7c6d5e4f3a2b1c0d',
  algorithm: 'Ed25519',
  signer_kind: 'LOCAL',
  key_id: 'local-dev-1',
  public_key: '00'.repeat(32),
  signature: '01'.repeat(64),
  signed_at: '2026-09-14T09:45:00Z',
  signed_by: NAHID,
  signed_by_code: 'E001',
  signed_by_name_en: 'Dr K M Nahid Ul Haque',
  signed_by_name_bn: 'ডা. কে এম নাহিদ উল হক',
  device_assurance: 'NAMED',
  // The clearance by value, both halves: what verification recomputes from.
  qa_review_id: '0190a8f2-0000-7000-8000-0000000000d1',
  qa_cleared_at: '2026-09-14T09:35:00Z',
  non_exportable_key: false,
};

/** Cleared by station 10, waiting for the consultant. */
const READY = {
  signed: false,
  readiness: { may_sign: true, signed: false, cleared: true, status: 'QA_REVIEW' },
  signature_image: IMAGE_CAVEAT,
};

/** Submitted, and station 10 has not decided. The server's own sentences. */
const NOT_CLEARED = {
  signed: false,
  readiness: {
    may_sign: false,
    signed: false,
    cleared: false,
    status: 'QA_REVIEW',
    reason_en:
      'Station 10 has not cleared this prescription. It cannot be signed until the quality ' +
      'review clears it.',
    reason_bn:
      '১০ নম্বর কেন্দ্র এই ব্যবস্থাপত্রে ছাড়পত্র দেয়নি। মান যাচাইয়ের ছাড়পত্র না পাওয়া পর্যন্ত ' +
      'স্বাক্ষর করা যাবে না।',
  },
  signature_image: IMAGE_CAVEAT,
};

const SIGNED_AND_VERIFIED = {
  signed: true,
  readiness: {
    may_sign: false,
    signed: true,
    cleared: true,
    status: 'SIGNED',
    reason_en:
      'This prescription has already been signed. It cannot be signed again; a signed ' +
      'prescription that was wrong is corrected.',
    reason_bn:
      'এই ব্যবস্থাপত্রে ইতিমধ্যেই স্বাক্ষর করা হয়েছে। আবার স্বাক্ষর করা যায় না; ভুল থাকলে ' +
      'সংশোধনী ব্যবস্থাপত্র লিখতে হবে।',
  },
  verification: {
    prescription_id: SHEET,
    verdict: 'VERIFIED',
    signature: SIGNATURE,
    recomputed_canonical_sha256: SIGNATURE.canonical_sha256,
  },
  signature_image: IMAGE_CAVEAT,
};

/* ------------------------------------------------------------------------- */
/* Serving                                                                    */
/* ------------------------------------------------------------------------- */

type Page = import('@playwright/test').Page;

async function servePrescription(page: Page, signatureView: unknown, signed = false) {
  // Broadest first: a later `page.route` wins, so the specific patient record has to come after
  // the sub-collections it would otherwise swallow.
  await page.route('**/v1/directory', (route) =>
    route.fulfill(json({ staff: [], devices: [], stations: [], as_of: '2026-09-01T00:00:00Z' })),
  );
  await page.route('**/v1/audit/alerts', (route) => route.fulfill(json({ alerts: [] })));
  await page.route('**/v1/prescribing-defaults', (route) => route.fulfill(json({ defaults: [] })));
  await page.route('**/v1/instruction-templates', (route) =>
    route.fulfill(json({ templates: [] })),
  );
  await page.route('**/v1/patients/*/allergies*', (route) =>
    route.fulfill(
      json({
        // **`NO_KNOWN_ALLERGY`, not an empty list.** The two are opposite facts and the strip
        // draws them differently on purpose (CP54): an empty list under `NONE_RECORDED` means
        // nobody has asked, and a prescriber who read that as "none" would write the penicillin.
        // She was asked at station 4 and said no.
        status: 'NO_KNOWN_ALLERGY',
        satisfied: true,
        allergies: [],
        assertion: {
          id: '0190a8f2-0000-7000-8000-0000000000f1',
          kind: 'NO_KNOWN_ALLERGY',
          asserted_at: '2026-09-14T08:58:00Z',
          asserted_by: '0190a8f2-0000-7000-8000-00000000000c',
        },
      }),
    ),
  );
  await page.route('**/v1/patients/*/visits', (route) =>
    route.fulfill(json({ visits: [{ id: VISIT, status: 'open' }] })),
  );
  await page.route('**/v1/patients/*/prescriptions*', (route) =>
    route.fulfill(json({ prescriptions: [] })),
  );
  await page.route(`**/v1/patients/${PATIENT}`, (route) => route.fulfill(json(PATIENT_RECORD)));
  await page.route(`**/v1/prescriptions/${SHEET}/print-model`, (route) =>
    route.fulfill(json(printModel(signed))),
  );
  await page.route(`**/v1/prescriptions/${SHEET}/signature`, (route) =>
    route.fulfill(json(signatureView)),
  );
  await page.route(`**/v1/prescriptions/${SHEET}`, (route) =>
    route.fulfill(json(envelope(signed ? 'SIGNED' : 'QA_REVIEW'))),
  );
}

async function openSheet(page: Page) {
  await page.goto(`/patients/${PATIENT}/prescribe?prescription=${SHEET}`);
  await expect(page.getByTestId('frozen-sheet')).toBeVisible();
}

async function shoot(page: Page, name: string) {
  // A settled page: the fonts matter more here than anywhere else, because half these pictures
  // exist to show that Bengali conjuncts render rather than falling back to boxes.
  await page.evaluate(() => document.fonts.ready);
  await page.screenshot({ path: `${OUT}/${name}.png`, fullPage: true });
}

/* ------------------------------------------------------------------------- */
/* CP84 — the sign control                                                    */
/* ------------------------------------------------------------------------- */

test('the sign control on a cleared prescription', async ({ prescriber: page }) => {
  await servePrescription(page, READY);
  await openSheet(page);
  await expect(page.getByTestId('sign-prescription')).toBeVisible();
  await shoot(page, 'cp84-sign-control-en');
});

test('the sign control on a cleared prescription, in Bangla', async ({
  prescriberBangla: page,
}) => {
  await servePrescription(page, READY);
  await openSheet(page);
  await expect(page.getByTestId('sign-prescription')).toBeVisible();
  await shoot(page, 'cp84-sign-control-bn');
});

/**
 * The step-up, open.
 *
 * This is the picture the checkpoint's third criterion is about, and the reason it is a picture
 * rather than a test: what matters is that the physician *sees* he is being re-proved. A dialog
 * that appeared and resolved itself would satisfy the criterion and teach the clinic that the
 * second factor is a formality.
 */
test('the second factor, asked for before anything is signed', async ({ prescriber: page }) => {
  await servePrescription(page, READY);
  await openSheet(page);
  await page.getByTestId('sign-prescription').click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await shoot(page, 'cp84-step-up-en');
});

test('the second factor, in Bangla', async ({ prescriberBangla: page }) => {
  await servePrescription(page, READY);
  await openSheet(page);
  await page.getByTestId('sign-prescription').click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await shoot(page, 'cp84-step-up-bn');
});

/* ------------------------------------------------------------------------- */
/* CP84 — signed, and immutable                                               */
/* ------------------------------------------------------------------------- */

test('a signed prescription, saying in words that it cannot be changed', async ({
  prescriber: page,
}) => {
  await servePrescription(page, SIGNED_AND_VERIFIED, true);
  await openSheet(page);
  await expect(page.getByTestId('immutable-note')).toBeVisible();
  await expect(page.getByTestId('signature-verified')).toBeVisible();
  await shoot(page, 'cp84-signed-en');
});

test('a signed prescription, in Bangla', async ({ prescriberBangla: page }) => {
  await servePrescription(page, SIGNED_AND_VERIFIED, true);
  await openSheet(page);
  await expect(page.getByTestId('immutable-note')).toBeVisible();
  await shoot(page, 'cp84-signed-bn');
});

/* ------------------------------------------------------------------------- */
/* CP84 — the two ways there is no control                                    */
/* ------------------------------------------------------------------------- */

/** Station 10 has not decided, and the screen says which gate that is. */
test('an uncleared prescription offers no sign control', async ({ prescriber: page }) => {
  await servePrescription(page, NOT_CLEARED);
  await openSheet(page);
  await expect(page.getByTestId('signing-blocked')).toBeVisible();
  await expect(page.getByTestId('sign-prescription')).toHaveCount(0);
  await shoot(page, 'cp84-uncleared-en');
});

test('an uncleared prescription, in Bangla', async ({ prescriberBangla: page }) => {
  await servePrescription(page, NOT_CLEARED);
  await openSheet(page);
  await expect(page.getByTestId('signing-blocked')).toBeVisible();
  await shoot(page, 'cp84-uncleared-bn');
});

/**
 * The picture that exists because CP92 shipped the opposite.
 *
 * Shirin Akhter holds `prescription.read` and reaches this sheet. She does not hold
 * `prescription.sign` and never will. There is **no control** on her screen — not a disabled one,
 * which would be the interface telling her the act is part of her job and would cost her a second
 * factor to find out otherwise.
 */
test('a reader who may see the prescription and not sign it is handed no control', async ({
  qaOfficer: page,
}) => {
  await servePrescription(page, READY);
  await openSheet(page);
  await expect(page.getByTestId('signing-not-yours')).toBeVisible();
  await expect(page.getByTestId('sign-prescription')).toHaveCount(0);
  await shoot(page, 'cp84-no-control-en');
});

/* ------------------------------------------------------------------------- */
/* CP85 — the public page                                                     */
/* ------------------------------------------------------------------------- */

const GENUINE = {
  verdict: 'VERIFIED',
  prescription: {
    id: SHEET,
    // `clinicalterm.DateEN` / `DateBN`, word for word. A date in words because the reader is
    // holding paper at a counter, and in both scripts because they told us no language.
    issued_on_en: '14 Sep 2026',
    issued_on_bn: '১৪ সেপ্ট ২০২৬',
    physician_name_en: 'Dr K M Nahid Ul Haque',
    physician_name_bn: 'ডা. কে এম নাহিদ উল হক',
    facility_name_en: 'Diabetes, Thyroid & Hormone Clinic, Faridpur',
    facility_name_bn: 'ডায়াবেটিস, থাইরয়েড ও হরমোন ক্লিনিক, ফরিদপুর',
    item_count: 4,
  },
  message_en:
    'This prescription was issued by this clinic and has not been altered since it was signed.',
  message_bn:
    'এই ব্যবস্থাপত্রটি এই ক্লিনিক থেকে দেওয়া হয়েছে এবং স্বাক্ষরের পর এতে কোনো পরিবর্তন করা হয়নি।',
};

/** `notVerified()`, word for word. One response for every failure. */
const NOT_VERIFIED = {
  verdict: 'NOT_VERIFIED',
  message_en:
    'This code could not be verified. It may have been mistyped or damaged, or the prescription ' +
    'may not be genuine. Ask the clinic before acting on it.',
  message_bn:
    'এই কোডটি যাচাই করা যায়নি। এটি ভুল উঠে থাকতে পারে বা নষ্ট হয়ে থাকতে পারে, অথবা ব্যবস্থাপত্রটি ' +
    'আসল না-ও হতে পারে। এটি অনুযায়ী কিছু করার আগে ক্লিনিকে জিজ্ঞাসা করুন।',
};

async function openVerification(page: Page, answer: unknown) {
  await page.route('**/v1/verify/*', (route) => route.fulfill(json(answer)));
  // A phone, held at a counter. These pictures are the only ones in the set taken at that size,
  // because that is the only device this page is ever opened on.
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`/verify/${TOKEN}`);
}

test('the public page for a genuine prescription', async ({ stranger: page }) => {
  await openVerification(page, GENUINE);
  await expect(page.getByTestId('verify-genuine')).toBeVisible();
  await shoot(page, 'cp85-verify-genuine-en');
});

test('the public page for a genuine prescription, in Bangla', async ({ strangerBangla: page }) => {
  await openVerification(page, GENUINE);
  await expect(page.getByTestId('verify-genuine')).toBeVisible();
  await shoot(page, 'cp85-verify-genuine-bn');
});

/**
 * A tampered prescription, and the picture that has to be different from the one above it.
 *
 * No block, no physician's name, no clinic, no date, no count. Printing a named colleague beside
 * "this has been altered" would publish an accusation about them to anybody holding a forged sheet
 * — and telling the holder that the token resolved but the content did not would tell a forger
 * they had the token right and only the content wrong.
 */
test('the public page for a tampered prescription', async ({ stranger: page }) => {
  await openVerification(page, NOT_VERIFIED);
  await expect(page.getByTestId('verify-not-verified')).toBeVisible();
  await expect(page.getByTestId('verify-facts')).toHaveCount(0);
  await shoot(page, 'cp85-verify-tampered-en');
});

test('the public page for a tampered prescription, in Bangla', async ({ strangerBangla: page }) => {
  await openVerification(page, NOT_VERIFIED);
  await expect(page.getByTestId('verify-not-verified')).toBeVisible();
  await shoot(page, 'cp85-verify-tampered-bn');
});
