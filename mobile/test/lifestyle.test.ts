import { readFileSync, readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { afterEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '@dthcms/api-client';

import en from '../src/messages/en.json';
import bn from '../src/messages/bn.json';
import {
  ADVICE,
  AVAILABILITY,
  CODE_NOT_LICENSED,
  LIFESTYLE_DERIVATIONS,
  LIFESTYLE_FIELDS,
  SCORE_SOURCES,
  adviceFor,
  answerFor,
  answerYesNo,
  answered,
  availabilityOf,
  canonicalNumber,
  catalogueRows,
  chooseOption,
  derivationsFor,
  emptyNumbers,
  emptyRun,
  hasBlockingNumber,
  instrumentNamed,
  isOwnWriting,
  isProposal,
  itemsInOrder,
  mayOpen,
  movedOn,
  numberProblem,
  numberProblems,
  numbersEmpty,
  optionsInOrder,
  outstandingOf,
  packYearsOnRecord,
  parsedAnswer,
  parsedNumber,
  progressOf,
  readScore,
  scoreOnRecord,
  scorePanel,
  stillNeeded,
  toAssessment,
  toNumbersBatch,
  troubleKey,
  typeNumber,
  warningsForNumbers,
  wordingOf,
  type Instrument,
  type Observation,
  type Scoring,
  type Trouble,
} from '../src/features/lifestyle/state';

/*
 * The station binding reaches the Keystore through lib/credentials, and the native module
 * cannot load under Node. Mocked exactly as the other station tests do; nothing here uses it.
 */
vi.mock('expo-secure-store', () => ({
  setItemAsync: vi.fn(async () => undefined),
  getItemAsync: vi.fn(async () => null),
  deleteItemAsync: vi.fn(async () => undefined),
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'WHEN_UNLOCKED_THIS_DEVICE_ONLY',
}));

const lifestyleApi = await import('../src/features/lifestyle/api');
const {
  getLifestyleScoring,
  listInstruments,
  listPatientAssessments,
  readCatalogueVersion,
  recordAssessment,
  recordLifestyleNumbers,
  scoreLifestyle,
  troubleOf,
} = lifestyleApi;

/**
 * Station 3's lifestyle assessment (CP58, ADR-0030).
 *
 * The screen is a React Native component and is judged in a clinic room with a patient in the
 * chair. What is checked here is every decision behind it, and five of these matter more than
 * the rest.
 *
 * **Nothing here adds up an answer.** `toAssessment` sends option codes; no body it produces
 * carries a `score` or a `total` at any depth; and no file in the feature contains a `reduce`
 * or any arithmetic on an option's points. The total is `core.instrument_total` and the
 * composite is a derived observation, and §12's cohorting reads both.
 *
 * **The unlicensed questionnaires are on the list.** `catalogueRows` returns all five seeded
 * instruments including PSS-10, PHQ-9 and IPAQ; `mayOpen` is false for exactly those three;
 * and the catalogue call sends no query at all, so there is no filter for a future change to
 * quietly switch on.
 *
 * **The clinic's own question says so.** `isOwnWriting` reads the server's `copyright_holder`,
 * because on a list beside a WHO instrument a single unlabelled question becomes, eventually,
 * a number somebody reports as validated.
 *
 * **A null score is never a zero.** `readScore(null)` is null, and the sentence that replaces
 * it is built from `domainsOnRecord` — which reads `PACK_YEARS` rather than the two counts it
 * comes from, because that is what the server reads.
 *
 * **The unit travels with the value.** Eight hours of sleep is sent as `8` with `h`, never as
 * the 480 this side worked out. The conversion here exists only so the plausibility band can
 * be checked before the write.
 */

// --- the catalogue, as the migration seeds it ---

const auditC = (over: Partial<Instrument> = {}): Instrument => ({
  code: 'AUDIT_C',
  name_en: 'Alcohol Use Disorders Identification Test — Consumption',
  name_bn: 'অ্যালকোহল ব্যবহার শনাক্তকরণ পরীক্ষা — সেবন',
  purpose_en: 'Three questions on how much and how often the patient drinks',
  purpose_bn: 'রোগী কতটা ও কত ঘন ঘন মদ্যপান করেন, তিনটি প্রশ্নে',
  copyright_holder: 'World Health Organization',
  licence_note:
    'Published by the WHO for free use and reproduction, including in clinical settings.',
  licence_note_bn:
    'বিশ্ব স্বাস্থ্য সংস্থা এটি বিনামূল্যে ব্যবহার ও পুনর্মুদ্রণের জন্য প্রকাশ করেছে, ক্লিনিকেও।',
  provenance: 'PUBLISHED',
  scoring: 'sum',
  usable: true,
  domain: 'ALCOHOL',
  ordering: 20,
  version: 1,
  version_published: true,
  items: [
    {
      item_code: 'FREQUENCY',
      ordering: 1,
      prompt_en: 'How often do you have a drink containing alcohol?',
      prompt_bn: 'কত ঘন ঘন আপনি অ্যালকোহলযুক্ত পানীয় পান করেন?',
      answer_type: 'coded',
      required: true,
      options: [
        { option_code: 'NEVER', ordering: 1, label_en: 'Never', label_bn: 'কখনও নয়', score: 0 },
        {
          option_code: 'MONTHLY_OR_LESS',
          ordering: 2,
          label_en: 'Monthly or less',
          label_bn: 'মাসে একবার বা তার কম',
          score: 1,
        },
      ],
    },
    {
      item_code: 'TYPICAL_AMOUNT',
      ordering: 2,
      prompt_en: 'How many standard drinks do you have on a typical day when you are drinking?',
      prompt_bn: 'যেদিন পান করেন, সাধারণত কয়টি স্ট্যান্ডার্ড পানীয় নেন?',
      answer_type: 'coded',
      required: true,
      options: [
        {
          option_code: 'ONE_OR_TWO',
          ordering: 1,
          label_en: '1 or 2',
          label_bn: '১ বা ২',
          score: 0,
        },
        {
          option_code: 'TEN_OR_MORE',
          ordering: 5,
          label_en: '10 or more',
          label_bn: '১০ বা বেশি',
          score: 4,
        },
      ],
    },
    {
      item_code: 'HEAVY_EPISODES',
      ordering: 3,
      prompt_en: 'How often do you have six or more drinks on one occasion?',
      prompt_bn: 'কত ঘন ঘন এক বসায় ছয় বা তার বেশি পানীয় নেন?',
      answer_type: 'coded',
      required: true,
      options: [
        { option_code: 'NEVER', ordering: 1, label_en: 'Never', label_bn: 'কখনও নয়', score: 0 },
        { option_code: 'DAILY', ordering: 5, label_en: 'Daily', label_bn: 'প্রতিদিন', score: 4 },
      ],
    },
  ],
  ...over,
});

const readiness = (over: Partial<Instrument> = {}): Instrument => ({
  code: 'READINESS_1',
  name_en: 'Readiness to change (single item)',
  name_bn: 'পরিবর্তনের প্রস্তুতি (একটি প্রশ্ন)',
  purpose_en: 'How ready the patient says they are to change one habit, in their own words',
  purpose_bn: 'রোগী নিজে বলছেন একটি অভ্যাস বদলাতে তিনি কতটা প্রস্তুত',
  copyright_holder: 'This clinic',
  licence_note: 'Written here. Not a validated instrument, and must not be reported as one.',
  licence_note_bn:
    'এখানেই লেখা। এটি যাচাই করা কোনো প্রশ্নপত্র নয়, এবং একে সেভাবে উপস্থাপন করা যাবে না।',
  provenance: 'THIS_CLINIC',
  // One item, and its own published note says the score means nothing on its own.
  scoring: 'none',
  usable: true,
  domain: 'READINESS',
  ordering: 60,
  version: 1,
  version_published: true,
  items: [
    {
      item_code: 'READY',
      ordering: 1,
      prompt_en: 'How ready do you feel to change this habit in the next month?',
      prompt_bn: 'আগামী এক মাসে এই অভ্যাসটি বদলাতে আপনি কতটা প্রস্তুত বোধ করছেন?',
      answer_type: 'coded',
      required: true,
      options: [
        {
          option_code: 'NOT_THINKING',
          ordering: 1,
          label_en: 'Not thinking about it',
          label_bn: 'ভাবছি না',
          score: 0,
        },
        {
          option_code: 'KEEPING_UP',
          ordering: 5,
          label_en: 'Keeping it up for a while now',
          label_bn: 'বেশ কিছুদিন ধরে চালিয়ে যাচ্ছি',
          score: 4,
        },
      ],
    },
  ],
  ...over,
});

/** One of D-26's three: registered by name, unusable, and with no wording at all. */
const unlicensed = (code: string, ordering: number, holder: string): Instrument => ({
  code,
  name_en: `${code} as published`,
  name_bn: `${code} প্রকাশিত রূপে`,
  purpose_en: 'Registered so that its absence reads as a decision',
  purpose_bn: 'অনুপস্থিতিটি যেন সিদ্ধান্ত হিসেবে পড়া যায়',
  copyright_holder: holder,
  licence_note: 'D-26: permission for clinical use in a fee-charging clinic must be confirmed.',
  licence_note_bn: 'D-26: ফি নেওয়া ক্লিনিকে ব্যবহারের অনুমতি আছে কিনা তা নিশ্চিত করতে হবে।',
  provenance: 'PUBLISHED',
  usable: false,
  domain: 'STRESS',
  ordering,
  version_published: false,
});

const catalogue = (): Instrument[] => [
  auditC(),
  unlicensed('PSS_10', 30, 'Sheldon Cohen / Mind Garden'),
  unlicensed('PHQ_9', 40, 'Pfizer Inc.'),
  unlicensed('IPAQ_SHORT', 50, 'IPAQ Group'),
  readiness(),
];

const ids = { event: 'event-1', patient: 'patient-1', visit: 'visit-1' };

// --- the network ---

function respond(body: unknown, init: { status?: number } = {}) {
  const status = init.status ?? 200;
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

interface Call {
  url: string;
  method: string;
  body: string;
  requestedWith: string | null;
  idempotencyKey: string | null;
}

function stubFetch(...responses: Response[]): Call[] {
  const calls: Call[] = [];
  let index = 0;
  const mock = vi.fn(async (request: Request) => {
    calls.push({
      url: request.url,
      method: request.method,
      body: await request.clone().text(),
      requestedWith: request.headers.get('X-Requested-With'),
      idempotencyKey: request.headers.get('Idempotency-Key'),
    });
    const response = responses[Math.min(index, responses.length - 1)];
    index += 1;
    return response!.clone();
  });
  vi.stubGlobal('fetch', mock);
  return calls;
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const featureDir = join(
  dirname(fileURLToPath(import.meta.url)),
  '..',
  'src',
  'features',
  'lifestyle',
);

/**
 * The file with its comments stripped.
 *
 * These tests are about what the code does, not the prose beside it. A comment saying "nothing
 * here adds up an answer" would otherwise fail the test that checks nothing here adds up an
 * answer, which would teach the next person to stop writing the comment.
 */
function featureCode(file: string): string {
  return readFileSync(join(featureDir, file), 'utf8')
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/.*$/gm, '$1');
}

// --- D-26: the questionnaires this clinic may not run are on the list ---

describe('an unusable instrument is shown, not hidden', () => {
  it('returns every seeded instrument, in the clinic’s own order', () => {
    const rows = catalogueRows(catalogue(), 'en');
    expect(rows.map((row) => row.code)).toEqual([
      'AUDIT_C',
      'PSS_10',
      'PHQ_9',
      'IPAQ_SHORT',
      'READINESS_1',
    ]);
  });

  it('marks the three copyrighted ones unavailable and refuses to open them', () => {
    const rows = catalogueRows(catalogue(), 'en');
    for (const code of ['PSS_10', 'PHQ_9', 'IPAQ_SHORT']) {
      const row = rows.find((one) => one.code === code)!;
      expect(row.availability, code).toBe('unlicensed');
      expect(row.open, code).toBe(false);
      // The sentence saying what has to be confirmed travels with the row. Without it the
      // greyed line would read as a bug rather than as a licence nobody has bought.
      expect(row.licenceNote.text, code).toContain('D-26');
      expect(row.copyrightHolder, code).not.toBe('');
    }
  });

  it('never opens one, whatever a caller passes', () => {
    const blocked = unlicensed('PHQ_9', 40, 'Pfizer Inc.');
    expect(mayOpen(blocked)).toBe(false);
    // Even if a version and its items somehow arrived — which the database's own invariant
    // refuses — the licence is checked first and the answer does not change.
    expect(mayOpen({ ...blocked, version_published: true, items: auditC().items })).toBe(false);
    expect(toAssessment(blocked, {}, ids)).toBeNull();
  });

  it('distinguishes a licence nobody bought from a version nobody published', () => {
    expect(availabilityOf(auditC())).toBe('ready');
    expect(availabilityOf(auditC({ version_published: false }))).toBe('unpublished');
    // A `names_only` fetch carries no items, and a questionnaire with no questions is not one
    // an operator should be able to open.
    expect(availabilityOf(auditC({ items: [] }))).toBe('unpublished');
    expect(AVAILABILITY).toEqual(['ready', 'unlicensed', 'unpublished']);
  });

  it('asks the server for the whole catalogue and never for a filtered one', async () => {
    const calls = stubFetch(
      respond({ instruments: catalogue(), catalogue_version: '2026-09-05T04:00:00Z' }),
    );
    const held = await listInstruments();
    expect(held.instruments).toHaveLength(5);
    // The version travels with the copy it describes, because it is only useful beside one.
    expect(held.version).toBe('2026-09-05T04:00:00Z');
    const url = new URL(calls[0]!.url);
    expect(url.pathname).toBe('/v1/assessments/instruments');
    // No usable-only filter — the contract has none and this is what stops one being added
    // quietly. And nothing in this feature ever asks for the catalogue without its questions:
    // `names_only` would be five instruments' worth of prose to learn one timestamp, which is
    // what `version_only` is for.
    expect(url.search).toBe('');
    for (const file of readdirSync(featureDir)) {
      expect(/names_only/.test(featureCode(file)), file).toBe(false);
    }
  });

  it('has no code anywhere that drops a row for being unusable', () => {
    // The failure this guards against is a one-line "tidy-up" of the list. Every filter in the
    // feature is checked by eye here because there are exactly two, and neither reads `usable`.
    for (const file of readdirSync(featureDir)) {
      const source = featureCode(file);
      expect(/filter\([^)]*usable/.test(source), file).toBe(false);
      expect(/usable\s*\)\s*\.map/.test(source), file).toBe(false);
    }
  });
});

describe('the clinic’s own question says whose it is', () => {
  it('reads a typed fact and not the English words in the copyright holder', () => {
    expect(isOwnWriting(readiness())).toBe(true);
    expect(isOwnWriting(auditC())).toBe(false);
    // The rule that matters most on this screen no longer rests on a seed nobody is stopping
    // from being tidied: the holder can say anything at all and the answer comes off
    // `provenance`.
    expect(isOwnWriting(readiness({ copyright_holder: 'Faridpur Diabetes Centre' }))).toBe(true);
    expect(isOwnWriting(auditC({ copyright_holder: 'This clinic' }))).toBe(false);
  });

  it('carries the fact onto the row the screen draws', () => {
    const rows = catalogueRows(catalogue(), 'en');
    expect(rows.find((row) => row.code === 'READINESS_1')?.ownWriting).toBe(true);
    expect(rows.find((row) => row.code === 'AUDIT_C')?.ownWriting).toBe(false);
  });

  it('has no comparison against seeded English prose left anywhere', () => {
    // The holder is still rendered — "Published by the World Health Organization" is worth
    // saying — but nothing branches on its words. A comparison is what this looks for.
    for (const file of readdirSync(featureDir)) {
      const source = featureCode(file);
      // A comparison against words, rather than the emptiness check that decides whether there
      // is a holder worth naming at all.
      const words =
        /copyright[_H]?older[^\n]*(?:[=!]==?\s*['"][^'"]*[A-Za-z]|includes|startsWith|toLowerCase)/;
      expect(words.test(source), file).toBe(false);
      expect(/['"]This clinic['"]/.test(source), file).toBe(false);
    }
  });
});

describe('the licence note is a bilingual pair like everything else on the row', () => {
  it('reads the note in the reader’s language', () => {
    // It was English only for one checkpoint, which meant a Bangla-reading counsellor met the
    // D-26 explanation — the one sentence on this screen that must not be misunderstood — in a
    // language they may not read.
    const bengali = catalogueRows(catalogue(), 'bn').find((row) => row.code === 'PHQ_9')!;
    expect(bengali.licenceNote.ownLanguage).toBe(true);
    expect(bengali.licenceNote.text).toContain('D-26');
    expect(bengali.licenceNote.text).not.toBe(unlicensed('PHQ_9', 40, 'Pfizer Inc.').licence_note);
  });

  it('says so rather than silently substituting when one language is missing', () => {
    const halfWritten = auditC({ licence_note_bn: '' });
    const row = catalogueRows([halfWritten], 'bn')[0]!;
    expect(row.licenceNote.ownLanguage).toBe(false);
    expect(row.licenceNote.language).toBe('en');
  });
});

describe('a total is shown only where a total means something', () => {
  it('reads the instrument’s own scoring scheme', () => {
    const rows = catalogueRows(catalogue(), 'en');
    expect(rows.find((row) => row.code === 'AUDIT_C')?.totalMeansSomething).toBe(true);
    // `READINESS_1` is one question and its published note says the score means nothing on its
    // own. "Total 3" under it would be a screen inventing a finding out of an answer.
    expect(rows.find((row) => row.code === 'READINESS_1')?.totalMeansSomething).toBe(false);
    // An instrument that names no scheme is summed, which is what every seeded one but the
    // readiness question does — the safe direction to be wrong in is showing a total that is a
    // sum of zeroes, not hiding a real one.
    expect(catalogueRows([auditC({ scoring: undefined })], 'en')[0]?.totalMeansSomething).toBe(
      true,
    );
  });
});

describe('an instrument is read in the operator’s own language, and says when it is not', () => {
  it('prefers the reader’s language and falls back saying so', () => {
    expect(wordingOf('Never', 'কখনও নয়', 'bn')).toEqual({
      text: 'কখনও নয়',
      language: 'bn',
      ownLanguage: true,
    });
    expect(wordingOf('Never', '', 'bn')).toEqual({
      text: 'Never',
      language: 'en',
      ownLanguage: false,
    });
    expect(wordingOf('', '', 'en')).toEqual({ text: '', language: null, ownLanguage: false });
  });

  it('draws the row in the language asked for', () => {
    const bengali = catalogueRows(catalogue(), 'bn');
    expect(bengali[0]?.name.text).toBe(auditC().name_bn);
    expect(bengali[0]?.name.ownLanguage).toBe(true);
  });
});

// --- running one questionnaire ---

describe('the questionnaire runner', () => {
  it('starts with every item blank', () => {
    const run = emptyRun(auditC());
    expect(Object.keys(run).sort()).toEqual(['FREQUENCY', 'HEAVY_EPISODES', 'TYPICAL_AMOUNT']);
    expect(run.FREQUENCY).toEqual({ option: '', text: '', yesNo: null });
    expect(answerFor(run, 'NOT_AN_ITEM')).toEqual({ option: '', text: '', yesNo: null });
  });

  it('keeps one answer per item and clears the other two shapes', () => {
    // The database's own trigger refuses a coded item answered with a number. A refusal that
    // arrives after the patient has left is a questionnaire that has to be run again.
    let run = emptyRun(auditC());
    run = chooseOption(run, 'FREQUENCY', 'NEVER');
    expect(run.FREQUENCY).toEqual({ option: 'NEVER', text: '', yesNo: null });
    run = typeNumber(run, 'FREQUENCY', '3');
    expect(run.FREQUENCY).toEqual({ option: '', text: '3', yesNo: null });
    run = answerYesNo(run, 'FREQUENCY', true);
    expect(run.FREQUENCY).toEqual({ option: '', text: '', yesNo: true });
  });

  it('counts two integers and never a percentage', () => {
    let run = emptyRun(auditC());
    expect(progressOf(auditC(), run)).toEqual({ answered: 0, total: 3 });
    run = chooseOption(run, 'FREQUENCY', 'NEVER');
    expect(progressOf(auditC(), run)).toEqual({ answered: 1, total: 3 });
    for (const file of readdirSync(featureDir)) {
      const source = featureCode(file);
      expect(/percent|\/\s*total|\*\s*100/i.test(source), file).toBe(false);
    }
  });

  it('names the required items still blank, in the order they are asked', () => {
    let run = emptyRun(auditC());
    expect(outstandingOf(auditC(), run)).toEqual(['FREQUENCY', 'TYPICAL_AMOUNT', 'HEAVY_EPISODES']);
    run = chooseOption(run, 'TYPICAL_AMOUNT', 'ONE_OR_TWO');
    expect(outstandingOf(auditC(), run)).toEqual(['FREQUENCY', 'HEAVY_EPISODES']);
  });

  it('treats an answer of zero as an answer', () => {
    // A questionnaire item whose true answer is none must be recordable. Station 2's rule —
    // where a weight of nought is a refusal wearing a measurement's clothes — is the opposite
    // one, and applying it here would record the patient who does nothing as unasked.
    const numeric = auditC({
      items: [
        {
          item_code: 'DAYS',
          ordering: 1,
          prompt_en: 'Days a week',
          prompt_bn: 'সপ্তাহে কত দিন',
          answer_type: 'numeric',
          min_value: 0,
          max_value: 7,
          required: true,
        },
      ],
    });
    const run = typeNumber(emptyRun(numeric), 'DAYS', '0');
    expect(parsedAnswer(run.DAYS!)).toBe(0);
    expect(answered(run, numeric.items![0]!)).toBe(true);
    expect(outstandingOf(numeric, run)).toEqual([]);
  });

  it('refuses a number the item’s own range does not take', () => {
    const numeric = auditC({
      items: [
        {
          item_code: 'DAYS',
          ordering: 1,
          prompt_en: 'Days a week',
          prompt_bn: 'সপ্তাহে কত দিন',
          answer_type: 'numeric',
          min_value: 0,
          max_value: 7,
          required: true,
        },
      ],
    });
    const item = numeric.items![0]!;
    expect(numberProblem(item, { option: '', text: '', yesNo: null })).toBeNull();
    expect(numberProblem(item, { option: '', text: 'four', yesNo: null })).toBe('not_a_number');
    expect(numberProblem(item, { option: '', text: '-1', yesNo: null })).toBe('below');
    expect(numberProblem(item, { option: '', text: '8', yesNo: null })).toBe('above');
    // An item with no bounds takes any number. A bound arriving as JSON null must not be
    // compared against as zero, which would make every legitimate answer read as too low.
    const open = { ...item, min_value: undefined, max_value: undefined };
    expect(numberProblem(open, { option: '', text: '-40', yesNo: null })).toBeNull();
    const nulled = { ...item, min_value: null, max_value: null } as unknown as typeof item;
    expect(numberProblem(nulled, { option: '', text: '3', yesNo: null })).toBeNull();
    expect(numberProblem(item, { option: '', text: '3', yesNo: null })).toBeNull();
    // A coded item has no number to be wrong about.
    expect(
      numberProblem(auditC().items![0]!, { option: 'NEVER', text: '', yesNo: null }),
    ).toBeNull();
    expect(numberProblems(numeric, typeNumber(emptyRun(numeric), 'DAYS', '9'))).toEqual({
      DAYS: 'above',
    });
  });

  it('sorts items and options by the instrument’s own ordering', () => {
    const scrambled = auditC({
      items: [...auditC().items!].reverse(),
    });
    expect(itemsInOrder(scrambled).map((item) => item.item_code)).toEqual([
      'FREQUENCY',
      'TYPICAL_AMOUNT',
      'HEAVY_EPISODES',
    ]);
    expect(optionsInOrder(auditC().items![1]!).map((one) => one.option_code)).toEqual([
      'ONE_OR_TWO',
      'TEN_OR_MORE',
    ]);
  });

  it('finds an instrument by code and nothing by a blank one', () => {
    expect(instrumentNamed(catalogue(), 'AUDIT_C')?.code).toBe('AUDIT_C');
    expect(instrumentNamed(catalogue(), '  ')).toBeNull();
    expect(instrumentNamed(catalogue(), 'NOPE')).toBeNull();
    expect(instrumentNamed(undefined, 'AUDIT_C')).toBeNull();
  });
});

// --- criterion: the client never computes a total ---

describe('nothing in this feature adds up an answer', () => {
  it('sends the option code and never its points', () => {
    let run = emptyRun(auditC());
    run = chooseOption(run, 'FREQUENCY', 'MONTHLY_OR_LESS');
    run = chooseOption(run, 'TYPICAL_AMOUNT', 'TEN_OR_MORE');
    run = chooseOption(run, 'HEAVY_EPISODES', 'DAILY');
    const body = toAssessment(auditC(), run, ids)!;
    expect(body).toEqual({
      event_id: 'event-1',
      patient_id: 'patient-1',
      visit_id: 'visit-1',
      instrument_code: 'AUDIT_C',
      answers: [
        { item_code: 'FREQUENCY', option_code: 'MONTHLY_OR_LESS' },
        { item_code: 'TYPICAL_AMOUNT', option_code: 'TEN_OR_MORE' },
        { item_code: 'HEAVY_EPISODES', option_code: 'DAILY' },
      ],
    });
    // The chosen options are worth 1, 4 and 4 in the catalogue this body was built from. If
    // any of those numbers appear in it, something in this feature has done arithmetic the
    // server owns.
    expect(JSON.stringify(body)).not.toContain('score');
    expect(JSON.stringify(body)).not.toContain('total');
  });

  it('carries no score or total at any depth of any body it can produce', () => {
    const walk = (value: unknown): string[] =>
      value === null || typeof value !== 'object'
        ? []
        : Object.entries(value as Record<string, unknown>).flatMap(([key, inner]) => [
            key,
            ...walk(inner),
          ]);
    let run = emptyRun(auditC());
    for (const item of auditC().items!) run = chooseOption(run, item.item_code, 'NEVER');
    const keys = walk(toAssessment(auditC(), run, ids));
    expect(keys).not.toContain('score');
    expect(keys).not.toContain('total');
  });

  it('has no summation anywhere in the feature', () => {
    for (const file of readdirSync(featureDir)) {
      const source = featureCode(file);
      expect(/\breduce\(/.test(source), `${file}: no reduce`).toBe(false);
      expect(/\.score\s*[+*]/.test(source), `${file}: no arithmetic on points`).toBe(false);
      expect(/[+*]\s*\w+\.score/.test(source), `${file}: no arithmetic on points`).toBe(false);
    }
  });

  it('is not a request until every required item is answered and every number fits', () => {
    const run = chooseOption(emptyRun(auditC()), 'FREQUENCY', 'NEVER');
    expect(toAssessment(auditC(), run, ids)).toBeNull();

    const numeric = auditC({
      items: [
        {
          item_code: 'DAYS',
          ordering: 1,
          prompt_en: 'Days a week',
          prompt_bn: 'সপ্তাহে কত দিন',
          answer_type: 'numeric',
          min_value: 0,
          max_value: 7,
          required: true,
        },
      ],
    });
    expect(toAssessment(numeric, typeNumber(emptyRun(numeric), 'DAYS', '9'), ids)).toBeNull();
    expect(toAssessment(numeric, typeNumber(emptyRun(numeric), 'DAYS', '2'), ids)?.answers).toEqual(
      [{ item_code: 'DAYS', value_num: 2 }],
    );
  });

  it('leaves an unanswered optional item out rather than sending a blank', () => {
    const withOptional = auditC({
      items: [
        {
          item_code: 'AGREED',
          ordering: 1,
          prompt_en: 'Did the patient agree?',
          prompt_bn: 'রোগী কি রাজি হয়েছেন?',
          answer_type: 'boolean',
          required: true,
        },
        {
          item_code: 'NOTE_DAYS',
          ordering: 2,
          prompt_en: 'Days a week',
          prompt_bn: 'সপ্তাহে কত দিন',
          answer_type: 'numeric',
          required: false,
        },
      ],
    });
    const run = answerYesNo(emptyRun(withOptional), 'AGREED', false);
    expect(toAssessment(withOptional, run, ids)?.answers).toEqual([
      { item_code: 'AGREED', value_bool: false },
    ]);
  });

  it('refuses to build a request with no patient, no event or no answers', () => {
    let run = emptyRun(auditC());
    for (const item of auditC().items!) run = chooseOption(run, item.item_code, 'NEVER');
    expect(toAssessment(auditC(), run, { ...ids, patient: '  ' })).toBeNull();
    expect(toAssessment(auditC(), run, { ...ids, event: '' })).toBeNull();
    // A visit is optional, and an empty one is left off rather than sent as a blank string.
    expect(toAssessment(auditC(), run, { ...ids, visit: '' })).not.toHaveProperty('visit_id');
    // An instrument with no required items and nothing answered is a response nobody filled
    // in, which `assert_every_response_kept_its_items` refuses at the other end.
    const optionalOnly = auditC({
      items: [
        {
          item_code: 'NOTE_DAYS',
          ordering: 1,
          prompt_en: 'Days a week',
          prompt_bn: 'সপ্তাহে কত দিন',
          answer_type: 'numeric',
          required: false,
        },
      ],
    });
    expect(toAssessment(optionalOnly, emptyRun(optionalOnly), ids)).toBeNull();
  });
});

// --- §3 step 3's four plain numbers ---

describe('the four plain numbers', () => {
  it('writes the codes CP58 declared, with the units each one takes', () => {
    expect(LIFESTYLE_FIELDS.map((field) => field.code)).toEqual([
      'CIGARETTES_PER_DAY',
      'SMOKING_YEARS',
      'SLEEP_MINUTES',
      'ACTIVE_MINUTES_WEEK',
    ]);
    // The counts are dimensionless: "twenty a day" is a pure number and the period is in the
    // code's name. The durations offer both, and open on the one the operator thinks in.
    expect(LIFESTYLE_FIELDS[0]?.units).toEqual(['1']);
    expect(LIFESTYLE_FIELDS[1]?.units).toEqual(['1']);
    expect(LIFESTYLE_FIELDS[2]?.units).toEqual(['h', 'min']);
    expect(LIFESTYLE_FIELDS[3]?.units).toEqual(['min', 'h']);
    const form = emptyNumbers();
    expect(form.sleep.unit).toBe('h');
    expect(form.activity.unit).toBe('min');
    expect(numbersEmpty(form)).toBe(true);
  });

  it('takes nought as a number and refuses what is not one', () => {
    // Nought cigarettes a day and nought minutes of activity a week are the two most
    // clinically interesting answers this station collects.
    expect(parsedNumber({ text: '0', unit: '1' })).toBe(0);
    expect(parsedNumber({ text: '', unit: '1' })).toBeNull();
    expect(parsedNumber({ text: '7.', unit: '1' })).toBe(7);
    expect(parsedNumber({ text: 'twenty', unit: '1' })).toBeNull();
    expect(parsedNumber({ text: '-1', unit: '1' })).toBeNull();
  });

  it('converts for the warning and never for the write', () => {
    expect(canonicalNumber({ text: '8', unit: 'h' })).toBe(480);
    expect(canonicalNumber({ text: '150', unit: 'min' })).toBe(150);
    expect(canonicalNumber({ text: '', unit: 'h' })).toBeNull();

    const form = { ...emptyNumbers(), sleep: { text: '8', unit: 'h' } };
    const body = toNumbersBatch(form, {
      batch: 'batch-1',
      patient: 'patient-1',
      visit: 'visit-1',
      perField: (key) => `event-${key}`,
    })!;
    // Eight, with hours — never the 480 this side worked out. `core.to_canonical` is what
    // decides what is stored (CP42), and posting a converted number would quietly make a phone
    // authoritative about a clinical value.
    expect(body.observations).toEqual([
      { event_id: 'event-sleep', code: 'SLEEP_MINUTES', value: 8, unit: 'h' },
    ]);
    expect(JSON.stringify(body)).not.toContain('480');
  });

  it('asks for pack-years when a smoking number is in the save, and never otherwise', () => {
    // Declared since CP43 and unreachable until this checkpoint gave it a history to read.
    expect(LIFESTYLE_DERIVATIONS).toEqual(['PACK_YEARS']);
    expect(derivationsFor(emptyNumbers())).toEqual([]);
    expect(derivationsFor({ ...emptyNumbers(), cigarettes: { text: '20', unit: '1' } })).toEqual([
      'PACK_YEARS',
    ]);
    // Either one on its own: the server derives from the record, so an operator adding the
    // years to a count taken at a previous visit is completing the derivation.
    expect(derivationsFor({ ...emptyNumbers(), smokingYears: { text: '15', unit: '1' } })).toEqual([
      'PACK_YEARS',
    ]);
    expect(derivationsFor({ ...emptyNumbers(), sleep: { text: '8', unit: 'h' } })).toEqual([]);
  });

  it('sends one event id per field and marks only what was confirmed', () => {
    const form = {
      ...emptyNumbers(),
      cigarettes: { text: '20', unit: '1' },
      smokingYears: { text: '15', unit: '1' },
    };
    const body = toNumbersBatch(form, {
      batch: 'batch-1',
      patient: 'patient-1',
      perField: (key) => `event-${key}`,
      confirmed: (key) => key === 'cigarettes',
    })!;
    expect(body.derive).toEqual(['PACK_YEARS']);
    expect(body.observations[0]).toEqual({
      event_id: 'event-cigarettes',
      code: 'CIGARETTES_PER_DAY',
      value: 20,
      unit: '1',
      confirmed: true,
    });
    expect(body.observations[1]).not.toHaveProperty('confirmed');
    expect(body).not.toHaveProperty('visit_id');
  });

  it('is not a request when there is nothing to write', () => {
    const perField = (key: string) => `event-${key}`;
    expect(toNumbersBatch(emptyNumbers(), { batch: 'b', patient: 'p', perField })).toBeNull();
    const form = { ...emptyNumbers(), sleep: { text: '8', unit: 'h' } };
    expect(toNumbersBatch(form, { batch: '', patient: 'p', perField })).toBeNull();
    expect(toNumbersBatch(form, { batch: 'b', patient: ' ', perField })).toBeNull();
  });

  it('warns while the patient can still be asked again', () => {
    // The seeded band: sleep is plausible between 180 and 720 canonical minutes, and the rule
    // is written in minutes because the value is. Two hours is 120.
    const rules = [
      {
        code: 'SLEEP_MINUTES',
        absolute_min: 0,
        absolute_max: 1440,
        plausible_min: 180,
        plausible_max: 720,
        note_en: 'Under three hours or over twelve is worth a second question.',
        note_bn: 'তিন ঘণ্টার কম বা বারো ঘণ্টার বেশি হলে আবার জিজ্ঞাসা করা দরকার।',
        approved: false,
      },
    ];
    const subject = { sex: 'female' as const, ageYears: 44 };
    const form = { ...emptyNumbers(), sleep: { text: '2', unit: 'h' } };
    const warnings = warningsForNumbers(form, rules, subject);
    expect(warnings.sleep).toMatchObject({ severity: 'warn', kind: 'low', limit: 180 });
    expect(hasBlockingNumber(warnings)).toBe(false);
    // Confirmed, it goes quiet — and the absolute band it cannot pass stays.
    expect(warningsForNumbers(form, rules, subject, { sleep: true }).sleep).toBeUndefined();
    const impossible = { ...emptyNumbers(), sleep: { text: '30', unit: 'h' } };
    expect(hasBlockingNumber(warningsForNumbers(impossible, rules, subject, { sleep: true }))).toBe(
      true,
    );
  });
});

// --- the score, shown honestly ---

const scoreValue = (over: Partial<Observation> = {}): Observation =>
  ({
    id: 'obs-1',
    patient_id: 'patient-1',
    code: 'LIFESTYLE_RISK',
    category: 'DERIVED',
    value_type: 'numeric',
    value: 41.7,
    unit: '1',
    effective_at: '2026-09-05T04:00:00Z',
    recorded_at: '2026-09-05T04:00:00Z',
    source: 'STATION',
    status: 'ACTIVE',
    recorded_by: 'user-1',
    recorded_role: 'COUNSELOR',
    formula: 'lifestyle_risk',
    formula_version: '0.1.0-proposed',
    inputs: { pack_years: 10, audit_c: 5, sleep_hours: 5, domains: 3 },
    ...over,
  }) as Observation;

const scoringOf = (over: Partial<Scoring> = {}): Scoring => ({
  score: scoreValue(),
  assessed: ['SMOKING', 'ALCOHOL', 'SLEEP'],
  missing: ['ACTIVITY'],
  minimum: 3,
  ...over,
});

describe('the score is the server’s, and its absence is an answer', () => {
  it('is null when the server sent none, and never a zero', () => {
    expect(readScore(null)).toBeNull();
    expect(readScore(undefined)).toBeNull();
    // A value the record holds with no number in it is the same absence.
    expect(readScore(scoreValue({ value: undefined }))).toBeNull();
  });

  it('reports how many domains went into it, from the value itself', () => {
    const reading = readScore(scoreValue())!;
    expect(reading.value).toBe(41.7);
    // The server's own count, off `inputs.domains`. A score from three domains is a different
    // number from one from four and a reader holding only the figure cannot tell.
    expect(reading.domains).toBe(3);
    expect(reading.formula).toBe('lifestyle_risk');
  });

  it('says nothing rather than guessing when the value carries no count', () => {
    const reading = readScore(
      scoreValue({ inputs: { pack_years: 1, audit_c: 2, sleep_hours: 8 } }),
    )!;
    expect(reading.domains).toBeNull();
  });

  it('recognises a formula version nobody has approved', () => {
    expect(isProposal('0.1.0-proposed')).toBe(true);
    // Any pre-release, not the literal word: a version renamed to `-draft` must not go quietly
    // unlabelled, which is exactly the failure the suffix exists to prevent.
    expect(isProposal('0.2.0-draft')).toBe(true);
    expect(isProposal('1.0.0')).toBe(false);
    expect(isProposal('1.0.0+build.5')).toBe(false);
    expect(readScore(scoreValue())?.proposed).toBe(true);
    expect(readScore(scoreValue({ formula_version: '1.0.0' }))?.proposed).toBe(false);
  });

  it('reads the composite and the pack-years off the patient’s current values', () => {
    const rows = [
      scoreValue(),
      scoreValue({ code: 'PACK_YEARS', value: 7.5, formula: 'pack_years' }),
    ];
    expect(scoreOnRecord(rows)?.code).toBe('LIFESTYLE_RISK');
    expect(packYearsOnRecord(rows)?.value).toBe(7.5);
    expect(scoreOnRecord([])).toBeNull();
    expect(packYearsOnRecord(undefined)).toBeNull();
  });
});

// --- what the card draws, and where each part of it came from ---

describe('nothing in this feature knows which observation feeds which domain', () => {
  it('has no mapping from an observation code to a domain left anywhere', () => {
    // The one place this feature re-implemented a server decision. It existed because the API
    // said only `null`; it says `assessed`, `missing` and `minimum` now, and a copy kept here
    // would be silently wrong the day a fifth domain arrives.
    for (const file of readdirSync(featureDir)) {
      const source = featureCode(file);
      expect(/PACK_YEARS['"]?\s*:/.test(source), `${file}: no domain mapping`).toBe(false);
      expect(/SLEEP_MINUTES['"]?\s*:/.test(source), `${file}: no domain mapping`).toBe(false);
      expect(/AUDIT_C/.test(source), `${file}: no instrument stands for a domain`).toBe(false);
      expect(/audit_c|sleep_hours|active_minutes_week/.test(source), file).toBe(false);
    }
  });

  it('takes the floor off the payload and never from a constant of its own', () => {
    expect(stillNeeded(scoringOf({ assessed: ['SMOKING'], minimum: 3 }))).toBe(2);
    expect(stillNeeded(scoringOf({ assessed: ['SMOKING', 'SLEEP'], minimum: 3 }))).toBe(1);
    // Met, and never negative: a card saying "another −1 areas" is a card nobody wrote.
    expect(
      stillNeeded(scoringOf({ assessed: ['SMOKING', 'SLEEP', 'ALCOHOL', 'ACTIVITY'], minimum: 3 })),
    ).toBe(0);
    // A clinician raising the floor changes the sentence with no change here.
    expect(stillNeeded(scoringOf({ assessed: ['SMOKING'], minimum: 4 }))).toBe(3);
    for (const file of readdirSync(featureDir)) {
      expect(/MINIMUM_LIFESTYLE_DOMAINS/.test(featureCode(file)), file).toBe(false);
    }
  });
});

describe('the card knows where its number came from', () => {
  const account = scoringOf({ score: null });

  it('prefers the write’s own answer, including when that answer is no score', () => {
    // A null a write has just sent is a fact about the assessment just recorded. Filling that
    // space from the chart would put an older score under a questionnaire that did not produce
    // it — which is why `undefined` and `null` are different arguments here.
    const panel = scorePanel(
      scoringOf({ score: null, assessed: ['SLEEP'], missing: ['SMOKING'] }),
      null,
      scoreValue(),
    );
    expect(panel.reading).toBeNull();
    expect(panel.from).toBe('write');
    expect(panel.scoring?.missing).toEqual(['SMOKING']);
  });

  it('shows the score a write produced as the operator’s own', () => {
    const panel = scorePanel(scoringOf(), scoreValue({ value: 52.5 }), scoreValue());
    expect(panel.reading?.value).toBe(52.5);
    expect(panel.from).toBe('write');
  });

  it('falls back to the score already on the chart, and says that is what it is', () => {
    const panel = scorePanel(account, undefined, scoreValue());
    expect(panel.reading?.value).toBe(41.7);
    expect(panel.from).toBe('record');
    // And it still names the domains, because the account is read on arrival and stores nothing.
    expect(panel.scoring?.missing).toEqual(['ACTIVITY']);
  });

  it('names what is missing for a patient with no composite at all', () => {
    // The case that used to be an honest silence: before the read-only endpoint the only way to
    // get this list was to write a derived observation.
    const panel = scorePanel(
      scoringOf({
        score: null,
        assessed: [],
        missing: ['SMOKING', 'ALCOHOL', 'SLEEP', 'ACTIVITY'],
      }),
      undefined,
      null,
    );
    expect(panel.reading).toBeNull();
    expect(panel.from).toBe('none');
    expect(panel.scoring?.missing).toHaveLength(4);
    expect(stillNeeded(panel.scoring!)).toBe(3);
    expect(SCORE_SOURCES).toEqual(['write', 'record', 'none']);
  });

  it('says nothing about the domains only when the account could not be read', () => {
    // Offline, or a read that failed. The one case left where the list cannot be named.
    const panel = scorePanel(null, undefined, scoreValue());
    expect(panel.reading?.value).toBe(41.7);
    expect(panel.scoring).toBeNull();
  });

  it('carries the server’s domain lists through untouched', () => {
    const panel = scorePanel(scoringOf(), undefined, scoreValue());
    expect(panel.scoring?.assessed).toEqual(['SMOKING', 'ALCOHOL', 'SLEEP']);
    expect(panel.scoring?.missing).toEqual(['ACTIVITY']);
    expect(panel.reading?.domains).toBe(3);
  });
});

// --- the catalogue a tablet is holding ---

describe('a republished catalogue is noticed before a patient has answered', () => {
  it('compares the version as a string and claims nothing from silence', () => {
    expect(movedOn('2026-09-05T04:00:00Z', '2026-09-05T09:30:00Z')).toBe(true);
    expect(movedOn('2026-09-05T04:00:00Z', '2026-09-05T04:00:00Z')).toBe(false);
    // A response missing the field, or a tablet that has not fetched yet. A banner raised
    // because one small request did not answer would train people to ignore it.
    expect(movedOn('2026-09-05T04:00:00Z', '')).toBe(false);
    expect(movedOn('', '2026-09-05T09:30:00Z')).toBe(false);
  });

  it('never decides which of two versions is newer', () => {
    // A client holding a stale copy cannot answer that, and a screen that tried would have to
    // parse the server's own marker into a date it then compared.
    for (const file of readdirSync(featureDir)) {
      const source = featureCode(file);
      expect(/new Date\([^)]*version/i.test(source), file).toBe(false);
      expect(/Date\.parse/.test(source), file).toBe(false);
    }
  });
});

// --- the calls ---

describe('the lifestyle calls', () => {
  it('has exactly these calls and no others', () => {
    // Another would have to be added here, in front of a reviewer, beside the sentence in
    // api.ts saying why nothing on this side assembles a score.
    expect(Object.keys(lifestyleApi).sort()).toEqual([
      'getLifestyleScoring',
      'listInstruments',
      'listPatientAssessments',
      'readCatalogueVersion',
      'recordAssessment',
      'recordLifestyleNumbers',
      'scoreLifestyle',
      'troubleOf',
    ]);
  });

  it('records one questionnaire, guarded and keyed by its own event', async () => {
    const calls = stubFetch(
      respond(
        { response: { id: 'r1', total: 6, answers: [] }, scoring: scoringOf() },
        { status: 201 },
      ),
    );
    let run = emptyRun(auditC());
    for (const item of auditC().items!) run = chooseOption(run, item.item_code, 'NEVER');
    const written = await recordAssessment(toAssessment(auditC(), run, ids)!);
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/assessments');
    expect(calls[0]?.method).toBe('POST');
    expect(calls[0]?.requestedWith).toBe('DTHCMS');
    expect(calls[0]?.idempotencyKey).toBe('event-1');
    expect(written.scoring.score?.code).toBe('LIFESTYLE_RISK');
  });

  it('carries a null score back as an answer, with the reason beside it', async () => {
    // Below `minimum` domains there is nothing honest to compute, and the response is still
    // recorded. A caller that treated this as an error would tell the operator the save failed;
    // one that got only `null` would have to work out what is missing for itself.
    stubFetch(
      respond(
        {
          response: { id: 'r1', total: 2, answers: [] },
          scoring: scoringOf({
            score: null,
            assessed: ['ALCOHOL'],
            missing: ['SMOKING', 'SLEEP', 'ACTIVITY'],
          }),
        },
        { status: 201 },
      ),
    );
    let run = emptyRun(auditC());
    for (const item of auditC().items!) run = chooseOption(run, item.item_code, 'NEVER');
    const written = await recordAssessment(toAssessment(auditC(), run, ids)!);
    expect(written.scoring.score).toBeNull();
    expect(written.scoring.missing).toEqual(['SMOKING', 'SLEEP', 'ACTIVITY']);
    expect(written.scoring.minimum).toBe(3);
    expect(written.response.id).toBe('r1');
  });

  it('asks the server to recompute after a save, with its own key', async () => {
    // "Type the four numbers, save" can take a patient from two assessed domains to four
    // without a questionnaire being answered at all.
    const calls = stubFetch(respond({ scoring: scoringOf() }));
    const scoring = await scoreLifestyle('patient-1', 'event-9', 'visit-1');
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/assessments/score');
    expect(calls[0]?.method).toBe('POST');
    expect(calls[0]?.idempotencyKey).toBe('event-9');
    expect(JSON.parse(calls[0]!.body)).toEqual({ patient_id: 'patient-1', visit_id: 'visit-1' });
    expect(scoring.assessed).toEqual(['SMOKING', 'ALCOHOL', 'SLEEP']);
  });

  it('leaves the visit off the recompute rather than sending a blank one', async () => {
    const calls = stubFetch(respond({ scoring: scoringOf() }));
    await scoreLifestyle('patient-1', 'event-9', '  ');
    expect(JSON.parse(calls[0]!.body)).toEqual({ patient_id: 'patient-1' });
  });

  it('re-checks the catalogue version and pays for nothing else', async () => {
    const calls = stubFetch(
      respond({ instruments: [], catalogue_version: '2026-09-05T09:30:00Z' }),
    );
    expect(await readCatalogueVersion()).toBe('2026-09-05T09:30:00Z');
    const url = new URL(calls[0]!.url);
    expect(url.pathname).toBe('/v1/assessments/instruments');
    // `version_only`, not `names_only`: the latter still carries five instruments' names,
    // purposes and licence notes, which is a lot to pay to learn one timestamp.
    expect(url.searchParams.get('version_only')).toBe('1');
    expect(url.searchParams.get('names_only')).toBeNull();
  });

  it('asks where a patient stands without writing anything', async () => {
    // A screen must be able to ask this when it opens. The writing endpoint cannot answer it:
    // a derived observation appended every time somebody opens a tab is ledger noise.
    const calls = stubFetch(respond({ scoring: scoringOf({ score: null }) }));
    const account = await getLifestyleScoring('patient-1');
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/patients/patient-1/lifestyle-scoring');
    expect(calls[0]?.method).toBe('GET');
    // No idempotency key, because nothing is written and there is no attempt to replay.
    expect(calls[0]?.idempotencyKey).toBeNull();
    expect(account.missing).toEqual(['ACTIVITY']);
    // Its score is null by construction; the figure is the patient's LIFESTYLE_RISK.
    expect(account.score).toBeNull();
  });

  it('writes the four numbers through the path every station value uses', async () => {
    const calls = stubFetch(respond({ observations: [], alerts: [] }, { status: 201 }));
    const body = toNumbersBatch(
      { ...emptyNumbers(), cigarettes: { text: '20', unit: '1' } },
      { batch: 'batch-1', patient: 'patient-1', perField: (key) => `event-${key}` },
    )!;
    await recordLifestyleNumbers(body);
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/observations/batch');
    expect(calls[0]?.idempotencyKey).toBe('batch-1');
    expect(JSON.parse(calls[0]!.body).derive).toEqual(['PACK_YEARS']);
  });

  it('reads every questionnaire a patient has answered, superseded ones included', async () => {
    const calls = stubFetch(respond({ responses: [{ id: 'r1', status: 'SUPERSEDED' }] }));
    expect(await listPatientAssessments('patient-1')).toHaveLength(1);
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/patients/patient-1/assessments');
  });

  it('logs nothing at all', () => {
    // A refusal here names an instrument and a patient's answers. Neither belongs in a log.
    for (const file of readdirSync(featureDir)) {
      expect(/console\.(log|warn|error|info|debug)/.test(featureCode(file)), file).toBe(false);
    }
  });

  it('touches no session token and no device storage', () => {
    for (const file of readdirSync(featureDir)) {
      const source = featureCode(file);
      expect(/localStorage|sessionStorage|AsyncStorage|SecureStore/.test(source), file).toBe(false);
      expect(/access_token|refresh_token|Authorization/.test(source), file).toBe(false);
    }
  });
});

// --- what went wrong ---

const refusal = (over: {
  status: number;
  code?: string;
  fields?: Record<string, string>;
  fieldsBN?: Record<string, string>;
  messageEN?: string;
  messageBN?: string;
}) =>
  new ApiError({
    status: over.status,
    code: over.code ?? 'VALIDATION_FAILED',
    kind: 'validation',
    messageEN: over.messageEN ?? 'That request cannot be answered.',
    messageBN: over.messageBN ?? 'ওই অনুরোধের উত্তর দেওয়া যাচ্ছে না।',
    fields: over.fields ?? {},
    fieldsBN: over.fieldsBN ?? {},
    correlationID: 'req-1',
  });

const trouble = (over: Partial<Trouble> & { status: number }): Trouble => ({
  kind: 'refused',
  attempt: 'assessment',
  code: '',
  field: '',
  message: '',
  ...over,
});

describe('what went wrong, in the three ways it can', () => {
  it('reports a request that never left the tablet as unreachable', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Network request failed')));
    const error = await listInstruments().catch((e: unknown) => e);
    expect(troubleOf(error, 'en', 'catalogue')).toEqual({
      kind: 'unreachable',
      attempt: 'catalogue',
      status: 0,
      code: '',
      field: '',
      message: '',
    });
    expect(adviceFor(trouble({ kind: 'unreachable', status: 0 }))).toBe('retry');
  });

  it('keeps the licence refusal apart from everything else a 422 can be', () => {
    // 422 rather than 403 because nothing is wrong with who is asking. An operator who read
    // this as a permissions problem would ask for a role change, be given one, and still not
    // be able to run PHQ-9.
    const notLicensed = troubleOf(
      refusal({ status: 422, code: CODE_NOT_LICENSED, messageEN: 'Not licensed here.' }),
      'en',
      'assessment',
    );
    expect(notLicensed.code).toBe(CODE_NOT_LICENSED);
    expect(troubleKey(notLicensed)).toBe('trouble.notLicensed');
    expect(adviceFor(notLicensed)).toBe('reload');

    const ordinary = troubleOf(
      refusal({ status: 422, fields: { answers: 'A required question was left blank.' } }),
      'en',
      'assessment',
    );
    expect(ordinary.field).toBe('answers');
    expect(troubleKey(ordinary)).toBe('trouble.refused');
    expect(adviceFor(ordinary)).toBe('none');
  });

  it('answers each refusal with the one thing that can help', () => {
    expect(ADVICE).toEqual(['reload', 'retry', 'none']);
    // A stale catalogue: read it again. Pressing save would send the same refused answers.
    expect(adviceFor(trouble({ status: 404 }))).toBe('reload');
    expect(adviceFor(trouble({ status: 409 }))).toBe('reload');
    // A hat without `observation.write.lifestyle`. Nothing on this screen fixes that.
    expect(adviceFor(trouble({ status: 403 }))).toBe('none');
    expect(adviceFor(trouble({ kind: 'failed', status: 500 }))).toBe('retry');
  });

  it('reads the refusal in the operator’s own language where the server sent one', () => {
    const bengali = troubleOf(
      refusal({ status: 403, messageBN: 'আপনার ভূমিকায় এটি লেখা যায় না।' }),
      'bn',
      'assessment',
    );
    expect(bengali.message).toBe('আপনার ভূমিকায় এটি লেখা যায় না।');
    expect(bengali.kind).toBe('refused');
    // Something that is not an error this app throws carries no code, and the screen supplies
    // the sentence.
    expect(troubleOf(new Error('boom'), 'en', 'numbers')).toEqual({
      kind: 'failed',
      attempt: 'numbers',
      status: 0,
      code: '',
      field: '',
      message: '',
    });
  });

  it('falls back to the server’s own sentence when it named no field', () => {
    const refused = troubleOf(
      refusal({ status: 422, messageEN: 'Nothing to record.' }),
      'en',
      'numbers',
    );
    expect(refused.field).toBe('');
    expect(refused.message).toBe('Nothing to record.');
  });
});

// --- every sentence this station can produce ---

describe('every label this station can produce exists in both languages', () => {
  type Tree = Record<string, unknown>;
  const flatten = (tree: Tree, prefix = ''): Set<string> => {
    const out = new Set<string>();
    for (const [key, value] of Object.entries(tree)) {
      const path = prefix ? `${prefix}.${key}` : key;
      if (value !== null && typeof value === 'object') {
        for (const inner of flatten(value as Tree, path)) out.add(inner);
      } else {
        out.add(path);
      }
    }
    return out;
  };
  const english = flatten((en as Tree).lifestyle as Tree);
  const bangla = flatten((bn as Tree).lifestyle as Tree);

  it('has a sentence for every domain, availability, advice and trouble', () => {
    const wanted = [
      // Every domain the contract can name, on the scoring payload and on an instrument's own
      // row. The screen lowercases the server's code to reach the key, so a domain added to the
      // enum without a sentence here is a blank line on a card.
      ...['SMOKING', 'ALCOHOL', 'SLEEP', 'ACTIVITY', 'STRESS', 'DIET', 'READINESS'].map(
        (domain) => `domain.${domain.toLowerCase()}`,
      ),
      'unavailable.unlicensed',
      'unavailable.unpublished',
      'trouble.refused',
      'trouble.unreachable',
      'trouble.failed',
      'trouble.notLicensed',
      'numberProblem.not_a_number',
      'numberProblem.below',
      'numberProblem.above',
      ...LIFESTYLE_FIELDS.map((field) => `field.${field.key}`),
      'score.none',
      'score.needMore',
      'score.missing',
      'score.have',
      'score.notRead',
      'score.onRecord',
      'score.domains',
      'score.proposed',
      'score.version',
      'ownWriting',
      'ownWritingWhy',
      'noTotal',
      'catalogueMoved',
    ];
    for (const key of wanted) {
      expect(english.has(key), `${key} in English`).toBe(true);
      expect(bangla.has(key), `${key} in Bangla`).toBe(true);
    }
  });

  it('has a key for every trouble this feature can name', () => {
    const kinds: Trouble['kind'][] = ['refused', 'unreachable', 'failed'];
    for (const kind of kinds) {
      expect(
        english.has(troubleKey(trouble({ kind, status: 0 })).replace('trouble.', 'trouble.')),
      ).toBe(true);
    }
    expect(english.has(troubleKey(trouble({ status: 422, code: CODE_NOT_LICENSED })))).toBe(true);
  });

  it('writes the numbers inside Bangla sentences as Bengali numerals', () => {
    // ICU `{x, number}` under the `bn` locale, which is what turns 3 into ৩. A figure left as
    // a bare Latin digit inside a Bangla sentence is one a reader has to change alphabets for
    // mid-line.
    const withNumbers = ['progress', 'score.domains', 'score.needMore', 'total', 'points'];
    const banglaTree = (bn as Tree).lifestyle as Tree;
    const read = (key: string): string =>
      key.split('.').reduce<unknown>((node, part) => (node as Tree)[part], banglaTree) as string;
    for (const key of withNumbers) {
      expect(read(key), key).toMatch(/\{\w+, number\}/);
    }
  });
});

// --- the screen is arrangement, and the decisions are not in it ---

describe('the screen holds no decision this file could hold instead', () => {
  it('does not decide availability, licensing or the score in the component', () => {
    const screen = featureCode('LifestyleStation.tsx');
    // Every one of these is a rule with a test above it. A component that re-derived one would
    // be a second implementation nobody runs outside a device.
    expect(/usable\s*===|\.usable\b/.test(screen), 'no licence test in the screen').toBe(false);
    expect(/version_published/.test(screen), 'no publication test in the screen').toBe(false);
    // `provenance` appears in this screen only as CP61's attribution prop. What must not be
    // here is the instrument's own field, read to decide whose question it is.
    expect(/(instrument|row)\.provenance/.test(screen), 'no provenance test in the screen').toBe(
      false,
    );
    expect(/THIS_CLINIC/.test(screen), 'no provenance literal in the screen').toBe(false);
    // The floor and the domain lists are the server's, read out of `scoring` and never
    // recomputed: the screen calls `stillNeeded` and prints `missing`. Asking whether a list is
    // empty is not counting it; subtracting one from the other is.
    expect(/minimum\s*[-+]/.test(screen), 'no arithmetic on the floor').toBe(false);
    expect(/assessed\.length\s*[-+]/.test(screen), 'no counting the domains').toBe(false);
    expect(/[-+]\s*[\w.]*assessed\.length/.test(screen), 'no counting the domains').toBe(false);
  });

  it('never renders a missing score as a zero or a dash', () => {
    const screen = featureCode('LifestyleStation.tsx');
    // The two obvious wrong renderings, both of which are marks a reader compares with a real
    // score. The card says which domains are missing instead.
    expect(/score[\w.?]*\s*\?\?\s*0\b/.test(screen), 'no zero standing in for a score').toBe(false);
    expect(/score[\w.?]*\s*\|\|\s*0\b/.test(screen), 'no zero standing in for a score').toBe(false);
    expect(/score[^\n]*['"]—['"]/.test(screen), 'no dash standing in for a score').toBe(false);
  });
});
