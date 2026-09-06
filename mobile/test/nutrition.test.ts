import { readFileSync, readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { afterEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '@dthcms/api-client';

import en from '../src/messages/en.json';
import bn from '../src/messages/bn.json';
import { ofDietEntry } from '../src/features/attribution/state';
import {
  ADVICE,
  ATTEMPTS,
  CODE_ALREADY_WITHDRAWN,
  DAY_RELATIONS,
  DEBOUNCE_MS,
  DIET_ENTRY_NAMESPACE,
  MAX_RESULTS,
  MISSING,
  QUANTITY_STEPS,
  REASON_PRESETS,
  RECALL_REFRESH_MS,
  adviceFor,
  afterEntry,
  alreadyWithdrawn,
  applyAnswer,
  cannotWeigh,
  ceilingFor,
  chooseFood,
  chooseMeal,
  chooseMeasure,
  dayIsEmpty,
  dayRelation,
  emptyDraft,
  entryIdFor,
  entryLine,
  foodRows,
  hasEscapeHatch,
  issueSearch,
  mealGroups,
  measureNamed,
  measuresFor,
  missingFrom,
  openPicker,
  openWithdrawal,
  parsedQuantity,
  retrySearch,
  searchDue,
  searching,
  shiftDay,
  toEntry,
  toWithdrawal,
  totalsOf,
  troubleKey,
  typeQuantity,
  typeReason,
  typedQuery,
  unapproved,
  unusualDay,
  visible,
  wordingOf,
  type DietEntry,
  type DietRecall,
  type Food,
  type FoodMeasure,
  type Meal,
  type MealCode,
  type Reference,
  type Trouble,
} from '../src/features/nutrition/state';

/*
 * The station binding reaches the Keystore through lib/credentials, and the native module cannot
 * load under Node. Mocked exactly as the other station tests do; nothing here uses it.
 */
vi.mock('expo-secure-store', () => ({
  setItemAsync: vi.fn(async () => undefined),
  getItemAsync: vi.fn(async () => null),
  deleteItemAsync: vi.fn(async () => undefined),
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'WHEN_UNLOCKED_THIS_DEVICE_ONLY',
}));

const nutritionApi = await import('../src/features/nutrition/api');
const {
  getRecall,
  listRecallDays,
  listReference,
  recordDietEntry,
  runSearch,
  searchFoods,
  troubleOf,
  withdrawDietEntry,
} = nutritionApi;

/**
 * Station 7's 24-hour recall (CP59, §5.2, §12.1).
 *
 * The screen is a React Native component and is judged in a clinic room with a patient in the
 * chair and four minutes on the clock. What is checked here is every decision behind it, and
 * five of these matter more than the rest.
 *
 * **Nothing here computes a weight or a calorie.** `toEntry` sends the food, the measure and the
 * count; no body it produces carries `grams` or `kcal` at any depth; `totalsOf` copies the
 * server's figures across untouched; and no file in the feature contains a `reduce` or any
 * arithmetic on a composition figure. The energy is `read.diet_entry`'s, computed from the food
 * table, and §12.1's diet–outcome analysis reads it.
 *
 * **A measure the table cannot weigh is never offered.** `measuresFor` returns the food's own
 * portions plus the measures the server marks `universal`, and nothing else — and it finds the
 * escape hatch through that flag rather than through the literal `GRAM`, so the feature contains
 * no such literal at all.
 *
 * **The day being recalled is never computed here.** `toEntry` refuses to build a body without a
 * date, the date always comes from the server's own answer, and `dayRelation` exists only to
 * check it against the clinic's calendar — because the server's "yesterday" is a UTC yesterday
 * and this clinic runs six hours ahead of it.
 *
 * **Either operator may take back either entry.** `entryLine.mayWithdraw` is true on every
 * standing row whoever recorded it, and there is no comparison against the reader anywhere in
 * the feature that could quietly make it false.
 *
 * **A slow answer cannot land over a newer list.** Every replacement of the picker's results
 * goes through `applyAnswer`, whose first line drops anything not newer than what is shown.
 */

// --- the table, as the migration seeds it ---

const measure = (over: Partial<FoodMeasure> & { code: string }): FoodMeasure => ({
  name_en: over.code.toLowerCase(),
  name_bn: over.code,
  universal: false,
  // The ceiling lives on the measure's own row, which is where the server reads it from.
  max_quantity: over.universal === true ? 5000 : 100,
  ordering: 100,
  ...over,
});

/** The nine household measures, in the migration's own order. */
const measures = (): FoodMeasure[] => [
  measure({ code: 'PIECE', name_en: 'piece', name_bn: 'টুকরা', ordering: 10 }),
  measure({ code: 'CUP', name_en: 'cup', name_bn: 'কাপ', ordering: 20 }),
  measure({ code: 'BOWL', name_en: 'bowl', name_bn: 'বাটি', ordering: 30 }),
  measure({ code: 'PLATE', name_en: 'plate', name_bn: 'প্লেট', ordering: 40 }),
  measure({ code: 'TABLESPOON', name_en: 'tablespoon', name_bn: 'টেবিল চামচ', ordering: 50 }),
  measure({ code: 'TEASPOON', name_en: 'teaspoon', name_bn: 'চা চামচ', ordering: 60 }),
  measure({ code: 'GLASS', name_en: 'glass', name_bn: 'গ্লাস', ordering: 70 }),
  measure({ code: 'HANDFUL', name_en: 'handful', name_bn: 'এক মুঠো', ordering: 80 }),
  measure({ code: 'GRAM', name_en: 'gram', name_bn: 'গ্রাম', universal: true, ordering: 99 }),
];

/** The day's meals, with their names, as `core.meal` seeds them. */
const meals = (): Meal[] => [
  { code: 'BREAKFAST', name_en: 'Breakfast', name_bn: 'সকালের নাশতা', ordering: 1 },
  { code: 'MID_MORNING', name_en: 'Mid-morning', name_bn: 'দুপুরের আগে', ordering: 2 },
  { code: 'LUNCH', name_en: 'Lunch', name_bn: 'দুপুরের খাবার', ordering: 3 },
  { code: 'AFTERNOON', name_en: 'Afternoon', name_bn: 'বিকেলের খাবার', ordering: 4 },
  { code: 'DINNER', name_en: 'Dinner', name_bn: 'রাতের খাবার', ordering: 5 },
  { code: 'BEDTIME', name_en: 'Before bed', name_bn: 'ঘুমানোর আগে', ordering: 6 },
  { code: 'OTHER', name_en: 'Something else', name_bn: 'অন্য সময়', ordering: 7 },
];

/** The whole reference payload, as `GET /v1/foods/measures` answers it. */
const reference = (over: Partial<Reference> = {}): Reference => ({
  measures: measures(),
  meals: meals(),
  recallDateDefault: '2026-09-04',
  clinicToday: '2026-09-05',
  ...over,
});

const SOURCE =
  'Published South Asian composition tables; not yet confirmed against a national source.';

const ruti = (over: Partial<Food> = {}): Food => ({
  code: 'RUTI_ATTA',
  name_en: 'Ruti (wholemeal)',
  name_bn: 'আটার রুটি',
  group_code: 'GRAIN',
  kcal_per_100g: 275,
  protein_per_100g: 9,
  carb_per_100g: 55,
  fat_per_100g: 3,
  source: SOURCE,
  approved: false,
  portions: [
    {
      measure_code: 'PIECE',
      measure_en: 'piece',
      measure_bn: 'টুকরা',
      grams: 45,
      note_en: 'one medium ruti',
      note_bn: 'একটি মাঝারি রুটি',
      universal: false,
      max_quantity: 100,
    },
  ],
  ...over,
});

const rice = (over: Partial<Food> = {}): Food => ({
  code: 'RICE_BOILED',
  name_en: 'Boiled rice',
  name_bn: 'ভাত',
  group_code: 'GRAIN',
  kcal_per_100g: 130,
  protein_per_100g: 2.7,
  carb_per_100g: 28,
  fat_per_100g: 0.3,
  source: SOURCE,
  approved: false,
  portions: [
    {
      measure_code: 'CUP',
      measure_en: 'cup',
      measure_bn: 'কাপ',
      grams: 150,
      note_en: 'a teacup, packed',
      note_bn: 'এক কাপ, চেপে',
      universal: false,
      max_quantity: 100,
    },
    {
      measure_code: 'PLATE',
      measure_en: 'plate',
      measure_bn: 'প্লেট',
      grams: 300,
      note_en: 'a full plate',
      note_bn: 'ভরা এক প্লেট',
      universal: false,
      max_quantity: 100,
    },
  ],
  ...over,
});

/** A food nobody has written a portion for. It exists: the table is meant to grow. */
const unportioned = (): Food => ({
  code: 'KHICHURI',
  name_en: 'Khichuri',
  name_bn: 'খিচুড়ি',
  group_code: 'GRAIN',
  kcal_per_100g: 120,
  protein_per_100g: 4,
  carb_per_100g: 20,
  fat_per_100g: 2,
  source: 'Added by hand at this clinic; figures not confirmed.',
  approved: false,
});

// --- one day, built by two people ---

const RAHIM = '11111111-1111-4111-8111-111111111111';
const NASRIN = '22222222-2222-4222-8222-222222222222';

const entry = (over: Partial<DietEntry> & { id: string }): DietEntry => ({
  patient_id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
  recall_date: '2026-09-04',
  meal: 'BREAKFAST',
  food_code: 'RUTI_ATTA',
  food_en: 'Ruti (wholemeal)',
  food_bn: 'আটার রুটি',
  quantity: 2,
  measure_code: 'PIECE',
  measure_en: 'piece',
  measure_bn: 'টুকরা',
  grams: 90,
  kcal: 247.5,
  protein: 8.1,
  carb: 49.5,
  fat: 2.7,
  recorded_at: '2026-09-05T04:10:00Z',
  recorded_by: RAHIM,
  recorded_by_code: 'DTHC-014',
  recorded_by_name_en: 'Rahim Uddin',
  recorded_by_name_bn: 'রহিম উদ্দিন',
  ...over,
});

const recall = (over: Partial<DietRecall> = {}): DietRecall => ({
  patient_id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
  recall_date: '2026-09-04',
  entries: [
    entry({ id: 'e1' }),
    entry({
      id: 'e2',
      meal: 'LUNCH',
      food_code: 'RICE_BOILED',
      food_en: 'Boiled rice',
      food_bn: 'ভাত',
      quantity: 2,
      measure_code: 'CUP',
      measure_en: 'cup',
      measure_bn: 'কাপ',
      grams: 300,
      kcal: 390,
      recorded_by: NASRIN,
      recorded_by_code: 'DTHC-021',
      recorded_by_name_en: 'Nasrin Akter',
      recorded_by_name_bn: 'নাসরিন আক্তার',
    }),
  ],
  totals: { kcal: 637.5, protein: 16.2, carb: 99, fat: 5.4, entries: 2, contributors: 2 },
  ...over,
});

// --- the harness ---

function json(body: unknown, init: { status?: number } = {}): Response {
  return new Response(JSON.stringify(body), {
    status: init.status ?? 200,
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
  'nutrition',
);

/**
 * The file with its comments stripped.
 *
 * These tests are about what the code does, not the prose beside it. A comment saying "nothing
 * here adds up a calorie" would otherwise fail the test that checks nothing here adds up a
 * calorie, which would teach the next person to stop writing the comment.
 */
function featureCode(file: string): string {
  return readFileSync(join(featureDir, file), 'utf8')
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/.*$/gm, '$1');
}

const featureFiles = readdirSync(featureDir).filter((name) => /\.tsx?$/.test(name));

// --- the arithmetic is the server's ---

describe('nothing in this feature computes a weight or a calorie', () => {
  it('sends the food, the measure and the count, and never a weight or an energy', () => {
    const draft = {
      meal: 'BREAKFAST' as MealCode,
      foodCode: 'RUTI_ATTA',
      measureCode: 'PIECE',
      quantity: '2',
    };
    const body = toEntry(draft, measuresFor(ruti(), measures(), 'en'), {
      event: 'ev-1',
      patient: 'pt-1',
      recallDate: '2026-09-04',
    });
    expect(body).toEqual({
      event_id: 'ev-1',
      patient_id: 'pt-1',
      recall_date: '2026-09-04',
      meal: 'BREAKFAST',
      food_code: 'RUTI_ATTA',
      measure_code: 'PIECE',
      quantity: 2,
    });
    // Named explicitly as well as by the shape above, because the shape is what a future field
    // would slip past.
    expect(keysDeep(body)).not.toContain('grams');
    expect(keysDeep(body)).not.toContain('kcal');
    expect(keysDeep(body)).not.toContain('protein');
  });

  it('copies the day’s totals across untouched', () => {
    const reading = totalsOf(recall());
    expect(reading).toEqual({
      kcal: 637.5,
      protein: 16.2,
      carb: 99,
      fat: 5.4,
      entries: 2,
      contributors: 2,
      shared: true,
    });
  });

  it('reports no totals at all rather than zeroes when the day has not been read', () => {
    // A zero is a number somebody compares with a real one. There is no total until the server
    // has sent one.
    expect(totalsOf(null)).toBeNull();
    expect(totalsOf(undefined)).toBeNull();
  });

  it('has no arithmetic on a composition figure anywhere in the feature', () => {
    for (const file of featureFiles) {
      const source = featureCode(file);
      expect(/\.reduce\s*\(/.test(source), `${file} has a reduce`).toBe(false);
      // The per-hundred-grams figures are on the payload and are never read on this side: a
      // client that multiplied one by a portion would be a client with its own calorie count.
      expect(/kcal_per_100g|protein_per_100g|carb_per_100g|fat_per_100g/.test(source), file).toBe(
        false,
      );
      // Read with the string literals removed, so that a test id like `recall-kcal` is not
      // mistaken for a subtraction. Arithmetic never lives inside a quoted string.
      const code = withoutStrings(source);
      expect(
        /kcal\s*[*/+-]|[*/+-]\s*[\w.]*kcal\b/.test(code),
        `${file} does arithmetic on kcal`,
      ).toBe(false);
      expect(/grams\s*[*/]|[*/]\s*[\w.]*grams\b/.test(code), `${file} scales grams`).toBe(false);
    }
  });
});

/** The file with every string and template literal taken out, leaving only what executes. */
function withoutStrings(source: string): string {
  return source
    .replace(/`(?:\\.|[^`\\])*`/g, '``')
    .replace(/'(?:\\.|[^'\\])*'/g, "''")
    .replace(/"(?:\\.|[^"\\])*"/g, '""');
}

function keysDeep(value: unknown): string[] {
  if (value === null || typeof value !== 'object') return [];
  const out: string[] = [];
  for (const [key, nested] of Object.entries(value as Record<string, unknown>)) {
    out.push(key, ...keysDeep(nested));
  }
  return out;
}

// --- a measure the table cannot weigh is never offered ---

describe('only the measures the table can weigh are offered', () => {
  it('offers the food’s own portions, in the server’s order, and then grams', () => {
    expect(measuresFor(rice(), measures(), 'en').map((one) => one.code)).toEqual([
      'CUP',
      'PLATE',
      'GRAM',
    ]);
  });

  it('does not offer a measure the food has no portion for', () => {
    const offered = measuresFor(ruti(), measures(), 'en').map((one) => one.code);
    expect(offered).toEqual(['PIECE', 'GRAM']);
    // Nine measures exist. Eight of them are refused with a 422 for this food, so eight of them
    // are not on the screen.
    expect(offered).not.toContain('BOWL');
    expect(offered).not.toContain('PLATE');
  });

  it('always offers the escape hatch, and finds it by the flag rather than by the code', () => {
    // A deployment that renamed the universal measure, or added a second one, must reach the
    // operator with no change here. This is the whole reason `universal` is on the payload.
    const renamed: FoodMeasure[] = [
      measure({ code: 'GRAM', name_en: 'gram', name_bn: 'গ্রাম', universal: false }),
      measure({ code: 'MILLILITRE', name_en: 'millilitre', name_bn: 'মিলিলিটার', universal: true }),
    ];
    const offered = measuresFor(ruti(), renamed, 'en').map((one) => one.code);
    expect(offered).toEqual(['PIECE', 'MILLILITRE']);
    expect(offered).not.toContain('GRAM');
  });

  it('offers only the escape hatch for a food nobody has written a portion for', () => {
    const offered = measuresFor(unportioned(), measures(), 'en');
    expect(offered.map((one) => one.code)).toEqual(['GRAM']);
    expect(hasEscapeHatch(offered)).toBe(true);
    // Grams weighs what it says. A number here would be the tautology 1.
    expect(offered[0]!.grams).toBeNull();
  });

  it('says the escape hatch is missing rather than pretending it is there', () => {
    // The measures list has not arrived. A screen that quietly offered nothing would have an
    // operator conclude the food cannot be recorded.
    const offered = measuresFor(ruti(), [], 'en');
    expect(offered.map((one) => one.code)).toEqual(['PIECE']);
    expect(hasEscapeHatch(offered)).toBe(false);
    expect(measuresFor(unportioned(), [], 'en')).toEqual([]);
  });

  it('does not offer one measure twice when a food carries a universal portion of its own', () => {
    const withGrams = ruti({
      portions: [
        {
          measure_code: 'GRAM',
          measure_en: 'gram',
          measure_bn: 'গ্রাম',
          grams: 1,
          universal: true,
        },
      ],
    });
    expect(measuresFor(withGrams, measures(), 'en').map((one) => one.code)).toEqual(['GRAM']);
  });

  it('carries the portion note, which is what makes a household measure usable', () => {
    // "one medium ruti" is the difference between two operators meaning the same thing by one
    // piece and two operators meaning different things.
    const piece = measuresFor(ruti(), measures(), 'en')[0]!;
    expect(piece.grams).toBe(45);
    expect(piece.note.text).toBe('one medium ruti');
    expect(measuresFor(ruti(), measures(), 'bn')[0]!.note.text).toBe('একটি মাঝারি রুটি');
  });

  it('refuses to build a body for a measure that is not on offer', () => {
    const offered = measuresFor(ruti(), measures(), 'en');
    const draft = {
      meal: 'LUNCH' as MealCode,
      foodCode: 'RUTI_ATTA',
      measureCode: 'BOWL',
      quantity: '1',
    };
    expect(measureNamed(offered, 'BOWL')).toBeNull();
    expect(missingFrom(draft, offered)).toContain('measure');
    expect(
      toEntry(draft, offered, { event: 'e', patient: 'p', recallDate: '2026-09-04' }),
    ).toBeNull();
  });

  it('names no measure code anywhere in the feature', () => {
    // Not `GRAM`, and not the eight others. Every measure this screen draws came off the wire.
    for (const file of featureFiles) {
      const source = featureCode(file);
      for (const code of ['GRAM', 'CUP', 'PIECE', 'BOWL', 'PLATE', 'TABLESPOON', 'HANDFUL']) {
        expect(new RegExp(`['"\`]${code}['"\`]`).test(source), `${file} names ${code}`).toBe(false);
      }
    }
  });

  it('names no meal anywhere in the feature either', () => {
    // The seven meals come from `GET /v1/foods/measures`, in the order the day happens. A screen
    // holding its own list would have to be rebuilt the day the clinic adds an eighth.
    for (const file of featureFiles) {
      const source = featureCode(file);
      for (const meal of ['BREAKFAST', 'MID_MORNING', 'LUNCH', 'DINNER', 'BEDTIME']) {
        expect(new RegExp(`['"\`]${meal}['"\`]`).test(source), `${file} names ${meal}`).toBe(false);
      }
    }
  });
});

// --- the picker ---

describe('the picker never shows an answer to a query nobody typed', () => {
  it('asks immediately on open, so the table is on screen before anybody types', () => {
    const opened = openPicker();
    expect(opened.query).toBe('');
    expect(searchDue(opened, 0)).toBe(true);
  });

  it('waits for the box to be still, and does not re-ask for a query already asked', () => {
    let state = issueSearch(openPicker(), 0).state;
    expect(searchDue(state, 1000)).toBe(false);

    state = typedQuery(state, 'rut', 1000);
    expect(searchDue(state, 1000 + DEBOUNCE_MS - 1)).toBe(false);
    expect(searchDue(state, 1000 + DEBOUNCE_MS)).toBe(true);

    // Deleting a letter and typing it back costs nothing.
    state = typedQuery(state, 'ru', 2000);
    state = issueSearch(state, 2000).state;
    state = typedQuery(state, 'ru', 3000);
    expect(searchDue(state, 9999)).toBe(false);
  });

  it('drops an answer that is not newer than the one already shown', () => {
    let state = openPicker();
    state = applyAnswer(state, { seq: 2, ok: true, foods: [ruti()] });
    // The slow answer for "ru" landing after the fast one for "ruti". Arithmetic, not luck.
    state = applyAnswer(state, { seq: 1, ok: true, foods: [rice()] });
    expect(state.foods.map((food) => food.code)).toEqual(['RUTI_ATTA']);
    expect(state.applied).toBe(2);
  });

  it('cannot half-undo a newer list by landing an older failure over it', () => {
    let state = applyAnswer(openPicker(), { seq: 3, ok: true, foods: [ruti()] });
    state = applyAnswer(state, { seq: 2, ok: false, trouble: unreachable() });
    expect(state.trouble).toBeNull();
    expect(state.foods).toHaveLength(1);
  });

  it('clears the list on a failure, rather than leaving one under a box that no longer matches', () => {
    const state = applyAnswer(openPicker(), { seq: 1, ok: false, trouble: unreachable() });
    expect(state.foods).toEqual([]);
    expect(state.trouble?.kind).toBe('unreachable');
  });

  it('knows when an answer is still owed, from the sequences and nothing else', () => {
    const issued = issueSearch(openPicker(), 0);
    expect(searching(issued.state)).toBe(true);
    expect(searching(applyAnswer(issued.state, { seq: 1, ok: true, foods: [] }))).toBe(false);
  });

  it('asks again after a failure', () => {
    let state = issueSearch(openPicker(), 0).state;
    state = applyAnswer(state, { seq: 1, ok: false, trouble: unreachable() });
    expect(searchDue(state, 0)).toBe(false);
    state = retrySearch(state, 10);
    expect(searchDue(state, 10)).toBe(true);
    expect(state.trouble).toBeNull();
  });

  it('trims the list to what somebody will actually read', () => {
    const many = Array.from({ length: 40 }, (_, i) => ruti({ code: `F${i}` }));
    expect(visible(many)).toHaveLength(MAX_RESULTS);
    expect(foodRows(many, measures(), 'en')).toHaveLength(MAX_RESULTS);
  });

  it('keeps the server’s ranking exactly as it arrived', () => {
    // Prefix matches first, then trigram similarity, over the names *and* the synonyms. A screen
    // that re-sorted would sink the exact match somebody typed.
    const ranked = [rice(), ruti(), unportioned()];
    expect(foodRows(ranked, measures(), 'en').map((row) => row.code)).toEqual([
      'RICE_BOILED',
      'RUTI_ATTA',
      'KHICHURI',
    ]);
  });
});

function unreachable(): Trouble {
  return { kind: 'unreachable', attempt: 'foods', status: 0, code: '', field: '', message: '' };
}

// --- the table nobody has approved ---

describe('the food table says nobody has approved it', () => {
  it('counts the unapproved foods rather than assuming them', () => {
    expect(unapproved([ruti(), rice()])).toBe(2);
    // And it goes quiet by itself on the day a nutritionist approves the table.
    expect(unapproved([ruti({ approved: true }), rice({ approved: true })])).toBe(0);
    expect(unapproved([ruti({ approved: true }), rice()])).toBe(1);
    expect(unapproved(undefined)).toBe(0);
  });

  it('carries each row’s own source, because a table remembered as one is uncheckable', () => {
    const row = foodRows([ruti()], measures(), 'en')[0]!;
    expect(row.approved).toBe(false);
    expect(row.source).toBe(SOURCE);
  });
});

// --- the day being recalled ---

describe('the day being recalled comes from the server and is checked, never computed', () => {
  it('refuses to build an entry with no day at all', () => {
    const offered = measuresFor(ruti(), measures(), 'en');
    const draft = {
      meal: 'BREAKFAST' as MealCode,
      foodCode: 'RUTI_ATTA',
      measureCode: 'PIECE',
      quantity: '1',
    };
    expect(toEntry(draft, offered, { event: 'e', patient: 'p', recallDate: '' })).toBeNull();
    expect(toEntry(draft, offered, { event: 'e', patient: 'p', recallDate: '   ' })).toBeNull();
  });

  it('reads the day against the clinic’s calendar', () => {
    expect(dayRelation('2026-09-04', '2026-09-05')).toBe('yesterday');
    expect(dayRelation('2026-09-05', '2026-09-05')).toBe('today');
    expect(dayRelation('2026-08-30', '2026-09-05')).toBe('earlier');
    expect(dayRelation('2026-09-06', '2026-09-05')).toBe('ahead');
    expect(dayRelation('', '2026-09-05')).toBeNull();
    expect(dayRelation('2026-09-04', '')).toBeNull();
  });

  it('crosses a month, a year and a leap day without help', () => {
    // The server's default is a UTC yesterday and this clinic runs six hours ahead of it, so the
    // first of the month is a case that happens rather than one that is imagined.
    expect(dayRelation('2026-08-31', '2026-09-01')).toBe('yesterday');
    expect(dayRelation('2025-12-31', '2026-01-01')).toBe('yesterday');
    expect(dayRelation('2028-02-29', '2028-03-01')).toBe('yesterday');
  });

  it('names the two days worth stopping an operator over', () => {
    // A 24-hour recall is about a day that has finished. Today's food is half eaten and
    // tomorrow's has not been.
    expect(unusualDay('today')).toBe(true);
    expect(unusualDay('ahead')).toBe(true);
    expect(unusualDay('yesterday')).toBe(false);
    expect(unusualDay('earlier')).toBe(false);
    expect(unusualDay(null)).toBe(false);
  });

  it('steps a day without being dragged by the tablet’s own time zone', () => {
    expect(shiftDay('2026-09-05', -1)).toBe('2026-09-04');
    expect(shiftDay('2026-09-01', -1)).toBe('2026-08-31');
    expect(shiftDay('2026-01-01', -1)).toBe('2025-12-31');
    expect(shiftDay('2028-03-01', -1)).toBe('2028-02-29');
    expect(shiftDay('2026-12-31', 1)).toBe('2027-01-01');
    expect(shiftDay('2026-09-05', 0)).toBe('2026-09-05');
  });

  it('returns an unparseable date unchanged rather than an Invalid Date', () => {
    // A recall headed "NaN-NaN-NaN" is worse than one headed with whatever the server sent.
    expect(shiftDay('', -1)).toBe('');
    expect(shiftDay('yesterday', -1)).toBe('yesterday');
    expect(shiftDay('2026-9-5', -1)).toBe('2026-9-5');
  });

  it('reads the day against the clinic’s calendar and never the tablet’s clock', () => {
    // `clinic_today` off the reference payload. This check briefly ran against a UTC midnight,
    // which in Faridpur is six in the morning — so a recall recorded at half past midnight read
    // as "an earlier day" when it was yesterday's.
    const now = reference();
    expect(dayRelation(now.recallDateDefault, now.clinicToday)).toBe('yesterday');
    for (const file of featureFiles) {
      const source = withoutStrings(featureCode(file));
      // No wall clock anywhere in the feature. `shiftDay` builds a date from explicit parts,
      // which is a different act from asking the tablet what day it is.
      expect(/Date\.now\(\)/.test(source), `${file} reads the clock`).toBe(false);
      expect(/new Date\(\s*\)/.test(source), `${file} reads the clock`).toBe(false);
      expect(/toISOString|getFullYear\(\)/.test(source), `${file} reads the clock`).toBe(false);
    }
  });

  it('re-reads the day often enough to catch a colleague and rarely enough to be cheap', () => {
    expect(RECALL_REFRESH_MS).toBeGreaterThanOrEqual(5_000);
    expect(RECALL_REFRESH_MS).toBeLessThanOrEqual(60_000);
  });
});

// --- which row this tablet just wrote ---

describe('the entry a write produces can be found without waiting to be told', () => {
  it('derives the same id the server does', () => {
    // The vector comes from `nutrition.EntryIDFor`: a version-5 uuid over the namespace the Go
    // side holds. This is a second implementation of a *format* rather than of a rule — it
    // decides nothing clinical, and if it were wrong the row would simply not highlight — but
    // it is pinned so that a change on either side is a failure here rather than a silent one.
    expect(entryIdFor('01234567-89ab-7cde-8f01-23456789abcd')).toBe(
      '6efeb67a-ac83-5f37-b172-1f9000524fe7',
    );
    expect(entryIdFor('00000000-0000-0000-0000-000000000000')).toBe(
      'bc53d5cf-1353-58dc-bb99-c28af254b2da',
    );
  });

  it('is stable, and different for every event', () => {
    const one = entryIdFor('01234567-89ab-7cde-8f01-23456789abcd');
    expect(entryIdFor('01234567-89AB-7CDE-8F01-23456789ABCD')).toBe(one);
    expect(entryIdFor('11234567-89ab-7cde-8f01-23456789abcd')).not.toBe(one);
  });

  it('sets the version and the variant a uuid needs', () => {
    const derived = entryIdFor('01234567-89ab-7cde-8f01-23456789abcd');
    expect(derived).toMatch(/^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/);
    // Version 5, and the RFC 4122 variant, exactly as `uuid.NewHash` sets them.
    expect(derived[14]).toBe('5');
    expect('89ab').toContain(derived[19]);
  });

  it('answers nothing for something that is not an event id', () => {
    // An empty answer highlights no row, which is the right failure: the day is still correct.
    expect(entryIdFor('')).toBe('');
    expect(entryIdFor('not-a-uuid')).toBe('');
    expect(DIET_ENTRY_NAMESPACE).toBe('6f0e5b2a-4c1d-4f6e-9a3b-7d2c8e5f1a90');
  });
});

// --- the form ---

describe('the entry form costs as few taps as it honestly can', () => {
  it('opens on a quantity of one, which most of a recall is', () => {
    expect(emptyDraft()).toEqual({ meal: '', foodCode: '', measureCode: '', quantity: '1' });
  });

  it('chooses the food’s first household portion with the food, and never grams', () => {
    // Opening on grams would ask the operator to convert "two cups" in their head in front of a
    // patient, which is the thing this station exists not to do.
    const row = foodRows([rice()], measures(), 'en')[0]!;
    expect(chooseFood(emptyDraft(), row).measureCode).toBe('CUP');

    const only = foodRows([unportioned()], measures(), 'en')[0]!;
    // Except where there is no household portion at all, where the escape hatch is the answer.
    expect(chooseFood(emptyDraft(), only).measureCode).toBe('GRAM');
  });

  it('leaves the measure empty for a food with nothing to record it in', () => {
    const stranded = foodRows([unportioned()], [], 'en')[0]!;
    expect(chooseFood(emptyDraft(), stranded).measureCode).toBe('');
  });

  it('keeps the meal after an entry, because a patient describes a meal at a time', () => {
    let draft = chooseMeal(emptyDraft(), 'BREAKFAST');
    draft = chooseFood(draft, foodRows([ruti()], measures(), 'en')[0]!);
    draft = typeQuantity(draft, '2');
    const next = afterEntry(draft);
    expect(next.meal).toBe('BREAKFAST');
    expect(next.foodCode).toBe('');
    expect(next.measureCode).toBe('');
    expect(next.quantity).toBe('1');
  });

  it('holds the quantity as text so a decimal point can be typed', () => {
    // An operator on the way to "1.5" types "1." first, and a field that parsed every keystroke
    // would take their decimal point away.
    expect(typeQuantity(emptyDraft(), '1.').quantity).toBe('1.');
    expect(parsedQuantity(typeQuantity(emptyDraft(), '1.'))).toBe(1);
    expect(parsedQuantity(typeQuantity(emptyDraft(), '0.5'))).toBe(0.5);
  });

  it('refuses a quantity of none, which is not a thing anybody ate', () => {
    expect(parsedQuantity(typeQuantity(emptyDraft(), '0'))).toBeNull();
    expect(parsedQuantity(typeQuantity(emptyDraft(), '-1'))).toBeNull();
    expect(parsedQuantity(typeQuantity(emptyDraft(), ''))).toBeNull();
    expect(parsedQuantity(typeQuantity(emptyDraft(), 'two'))).toBeNull();
  });

  it('does not hold a copy of the server’s ceiling', () => {
    // The service refuses more than a hundred household measures and more than five thousand
    // grams, and neither number is in the contract. A copy here would go stale the day either is
    // tuned, so a hundred and one cups is refused by the server in its own words.
    expect(parsedQuantity(typeQuantity(emptyDraft(), '101'))).toBe(101);
    for (const file of featureFiles) {
      expect(/\b5000\b/.test(featureCode(file)), `${file} copies the gram ceiling`).toBe(false);
    }
  });

  it('names what is still wanted, in the order the form is filled in', () => {
    const offered = measuresFor(ruti(), measures(), 'en');
    // The quantity is not missing on an empty form: it opens at one, which is the commonest
    // answer and one the operator confirms by not touching it.
    expect(missingFrom(emptyDraft(), [])).toEqual(['meal', 'food', 'measure']);
    // `tooMany` is not on the list for a blank quantity: an unreadable number is not an
    // excessive one, and the two want different sentences.
    expect(missingFrom(typeQuantity(emptyDraft(), ''), [])).toEqual([
      'meal',
      'food',
      'measure',
      'quantity',
    ]);
    let draft = chooseMeal(emptyDraft(), 'BREAKFAST');
    draft = chooseFood(draft, foodRows([ruti()], measures(), 'en')[0]!);
    expect(missingFrom(draft, offered)).toEqual([]);
    expect(missingFrom(typeQuantity(draft, ''), offered)).toEqual(['quantity']);
    expect(missingFrom(chooseMeasure(draft, 'BOWL'), offered)).toEqual(['measure']);
  });

  it('reads the ceiling off the measure’s own row, and infers nothing', () => {
    const offered = measuresFor(rice(), measures(), 'en');
    // A hundred cups is nobody's lunch and two hundred grams is an ordinary plate of rice, so
    // the two limits differ — and the difference belongs to the measure. It was briefly a pair
    // of payload-level numbers this side chose between by reading `universal`, which would have
    // been right only until a second universal measure was seeded.
    expect(ceilingFor(measureNamed(offered, 'CUP'))).toBe(100);
    expect(ceilingFor(measureNamed(offered, 'GRAM'))).toBe(5000);
    // And a second universal measure gets its own limit rather than grams'.
    const millilitres = measure({
      code: 'MILLILITRE',
      name_en: 'millilitre',
      name_bn: 'মিলিলিটার',
      universal: true,
      max_quantity: 3000,
    });
    const withTwo = measuresFor(rice(), [...measures(), millilitres], 'en');
    expect(ceilingFor(measureNamed(withTwo, 'MILLILITRE'))).toBe(3000);
    expect(ceilingFor(measureNamed(withTwo, 'GRAM'))).toBe(5000);
  });

  it('falls back to the measure row for a portion that does not carry one', () => {
    // The portion's field is optional in the contract; the measure's is not. They are the same
    // column on the server, so the fallback is for an older payload rather than a disagreement.
    const older = rice({
      portions: [
        {
          measure_code: 'CUP',
          measure_en: 'cup',
          measure_bn: 'কাপ',
          grams: 150,
          universal: false,
        },
      ],
    });
    expect(ceilingFor(measureNamed(measuresFor(older, measures(), 'en'), 'CUP'))).toBe(100);
  });

  it('treats a ceiling nobody has stated as unknown, not as a limit of none', () => {
    // A form that refused every quantity while the measures were still loading would be a form
    // that looked broken for its first second, and the server refuses either way.
    const unstated = measures().map((one) => ({ ...one, max_quantity: 0 }));
    const offered = measuresFor(rice({ portions: [] }), unstated, 'en');
    expect(ceilingFor(measureNamed(offered, 'GRAM'))).toBeNull();
    expect(ceilingFor(null)).toBeNull();
    expect(ceilingFor(undefined)).toBeNull();
    const draft = {
      meal: 'LUNCH' as MealCode,
      foodCode: 'RICE_BOILED',
      measureCode: 'GRAM',
      quantity: '90000',
    };
    expect(missingFrom(draft, offered)).toEqual([]);
  });

  it('stops an operator over the ceiling mid-sentence rather than at the 422', () => {
    const offered = measuresFor(rice(), measures(), 'en');
    const cups = (quantity: string) => ({
      meal: 'LUNCH' as MealCode,
      foodCode: 'RICE_BOILED',
      measureCode: 'CUP',
      quantity,
    });
    expect(missingFrom(cups('100'), offered)).toEqual([]);
    expect(missingFrom(cups('101'), offered)).toEqual(['tooMany']);
    // And the body is refused too, so there is no path that sends what the form refused.
    expect(
      toEntry(cups('101'), offered, {
        event: 'e',
        patient: 'p',
        recallDate: '2026-09-04',
      }),
    ).toBeNull();

    // Grams gets the other number: five thousand is a real ceiling and a hundred would refuse
    // an ordinary plate of rice.
    const grams = (quantity: string) => ({ ...cups(quantity), measureCode: 'GRAM' });
    expect(missingFrom(grams('300'), offered)).toEqual([]);
    expect(missingFrom(grams('5000'), offered)).toEqual([]);
    expect(missingFrom(grams('5001'), offered)).toEqual(['tooMany']);
  });

  it('offers the four counts a recall is mostly made of', () => {
    expect([...QUANTITY_STEPS]).toEqual([0.5, 1, 2, 3]);
  });

  it('refuses a body with no patient and no event id', () => {
    const offered = measuresFor(ruti(), measures(), 'en');
    const draft = {
      meal: 'BREAKFAST' as MealCode,
      foodCode: 'RUTI_ATTA',
      measureCode: 'PIECE',
      quantity: '1',
    };
    expect(
      toEntry(draft, offered, { event: '', patient: 'p', recallDate: '2026-09-04' }),
    ).toBeNull();
    expect(
      toEntry(draft, offered, { event: 'e', patient: ' ', recallDate: '2026-09-04' }),
    ).toBeNull();
  });

  it('sends the visit only when there is one', () => {
    const offered = measuresFor(ruti(), measures(), 'en');
    const draft = {
      meal: 'BREAKFAST' as MealCode,
      foodCode: 'RUTI_ATTA',
      measureCode: 'PIECE',
      quantity: '1',
    };
    const ids = { event: 'e', patient: 'p', recallDate: '2026-09-04' };
    expect(toEntry(draft, offered, ids)).not.toHaveProperty('visit_id');
    expect(toEntry(draft, offered, { ...ids, visit: '' })).not.toHaveProperty('visit_id');
    expect(toEntry(draft, offered, { ...ids, visit: 'v-1' })?.visit_id).toBe('v-1');
  });
});

// --- two operators, one recall ---

describe('the day shows both operators’ work, and either may take back either entry', () => {
  it('groups by meal in the order the server says the day happens', () => {
    const groups = mealGroups(recall(), meals(), 'en');
    expect(groups.map((group) => group.meal)).toEqual(['BREAKFAST', 'LUNCH']);
    // Meals with nothing in them are not drawn: seven empty headings above two lines of food is
    // a screen an operator scrolls past.
    expect(groups).toHaveLength(2);
  });

  it('keeps the entries inside a meal in the order the recall returned them', () => {
    const day = recall({
      entries: [
        entry({ id: 'a', eaten_at_hour: 7 }),
        entry({ id: 'b', eaten_at_hour: 9 }),
        entry({ id: 'c' }),
      ],
    });
    const lines = mealGroups(day, meals(), 'en')[0]!.lines;
    expect(lines.map((line) => line.id)).toEqual(['a', 'b', 'c']);
  });

  it('draws a meal this build has never heard of rather than dropping somebody’s dinner', () => {
    const day = recall({ entries: [entry({ id: 'x', meal: 'SUHOOR' as MealCode })] });
    const groups = mealGroups(day, meals(), 'en');
    expect(groups.map((group) => group.meal)).toEqual(['SUHOOR']);
    // Drawn under its code and flagged, so the screen can say why it reads like that rather
    // than dropping a patient's meal because a lookup was a version behind.
    expect(groups[0]!.known).toBe(false);
    expect(groups[0]!.name.text).toBe('SUHOOR');
  });

  it('draws each meal under the server’s own name, in both languages', () => {
    // `core.meal` carries the pair. They were bare enum codes for one checkpoint, which meant
    // every client invented its own Bangla for MID_MORNING — and two clients would have
    // disagreed about what a patient was asked.
    const english = mealGroups(recall(), meals(), 'en');
    expect(english.map((group) => group.name.text)).toEqual(['Breakfast', 'Lunch']);
    expect(english.every((group) => group.known)).toBe(true);
    const bangla = mealGroups(recall(), meals(), 'bn');
    expect(bangla.map((group) => group.name.text)).toEqual(['সকালের নাশতা', 'দুপুরের খাবার']);
  });

  it('says the name could not be read rather than dropping the meal', () => {
    // The ordinary way to reach this is a tablet whose reference call failed, not a version
    // skew. Either way the food stays on the day.
    const groups = mealGroups(recall(), [], 'en');
    expect(groups.map((group) => group.meal)).toEqual(['BREAKFAST', 'LUNCH']);
    expect(groups.every((group) => group.known)).toBe(false);
    expect(groups[0]!.lines).toHaveLength(1);
  });

  it('holds no meal name in either message file', () => {
    // The names are reference data now. A copy here is exactly the drift the bilingual table
    // was added to prevent, so the keys are gone and this is what keeps them gone.
    for (const tree of [en, bn]) {
      const keys = [...messages(tree).keys()].filter((key) => key.startsWith('nutrition.meal.'));
      expect(keys, `meal names still in a message file: ${keys.join(', ')}`).toEqual([]);
    }
  });

  it('lets anybody take back anybody’s entry', () => {
    // Requiring the original recorder would leave a duplicate standing until they come back from
    // the next patient, which is the whole reason the API allows it.
    const mine = entryLine(entry({ id: 'e1', recorded_by: RAHIM }), 'en');
    const theirs = entryLine(entry({ id: 'e2', recorded_by: NASRIN }), 'en');
    expect(mine.mayWithdraw).toBe(true);
    expect(theirs.mayWithdraw).toBe(true);
  });

  it('never compares the entry’s author against the reader', () => {
    // There is no operator id anywhere in this feature, so there is no arrangement of it in
    // which the control could quietly appear on some rows and not others.
    for (const file of featureFiles) {
      const source = featureCode(file);
      expect(/recorded_by\s*===|===\s*[\w.]*recorded_by/.test(source), file).toBe(false);
    }
  });

  it('keeps a withdrawn entry on the day, with its reason', () => {
    const line = entryLine(
      entry({
        id: 'e1',
        withdrawn_at: '2026-09-05T04:20:00Z',
        withdrawn_reason: 'The other assistant had already recorded this.',
        withdrawn_by_code: 'DTHC-021',
        withdrawn_by_name_en: 'Nasrin Akter',
        withdrawn_by_name_bn: 'নাসরিন আক্তার',
      }),
      'en',
    );
    expect(line.withdrawn).toBe(true);
    expect(line.mayWithdraw).toBe(false);
    expect(line.reason).toBe('The other assistant had already recorded this.');
    // And it is still in the day. A row that vanished would leave the other operator wondering
    // whether they imagined recording it.
    const day = recall({ entries: [line.entry] });
    expect(mealGroups(day, meals(), 'en')[0]!.lines).toHaveLength(1);
    expect(dayIsEmpty(day)).toBe(false);
  });

  it('says out loud when two people built the recall', () => {
    expect(totalsOf(recall())?.shared).toBe(true);
    const alone = recall({
      totals: { kcal: 100, protein: 1, carb: 2, fat: 3, entries: 1, contributors: 1 },
    });
    expect(totalsOf(alone)?.shared).toBe(false);
  });

  it('knows an empty day from an unread one', () => {
    expect(dayIsEmpty(recall({ entries: [] }))).toBe(true);
    expect(dayIsEmpty(null)).toBe(true);
    expect(mealGroups(null, meals(), 'en')).toEqual([]);
  });

  it('names the recorder and, on a withdrawal, both of them (CP61)', () => {
    const provenance = ofDietEntry(
      entry({
        id: 'e1',
        station_code: 'STN_NUTRITION',
        source: 'STATION',
        withdrawn_at: '2026-09-05T04:20:00Z',
        withdrawn_by: NASRIN,
        withdrawn_by_code: 'DTHC-021',
        withdrawn_by_name_en: 'Nasrin Akter',
        withdrawn_by_name_bn: 'নাসরিন আক্তার',
      }),
    );
    expect(provenance.by).toBe(RAHIM);
    expect(provenance.named).toEqual({
      code: 'DTHC-014',
      nameEN: 'Rahim Uddin',
      nameBN: 'রহিম উদ্দিন',
    });
    expect(provenance.station).toBe('STN_NUTRITION');
    expect(provenance.correction?.kind).toBe('withdrawn');
    expect(provenance.correction?.named?.nameEN).toBe('Nasrin Akter');
    // The withdrawer's own id, never the recorder's: at this station they are routinely two
    // different people, so a reader can be told whether they are the one who took it back.
    expect(provenance.correction?.by).toBe(NASRIN);
    expect(provenance.correction?.by).not.toBe(provenance.by);
  });

  it('reports no correction on an entry that still stands', () => {
    expect(ofDietEntry(entry({ id: 'e1' })).correction).toBeNull();
  });
});

// --- taking one back ---

describe('a withdrawal says why, and cannot be sent without one', () => {
  it('refuses a withdrawal that says nothing', () => {
    expect(toWithdrawal(openWithdrawal('e1'), 'ev-1')).toBeNull();
    expect(toWithdrawal(typeReason(openWithdrawal('e1'), '   '), 'ev-1')).toBeNull();
  });

  it('refuses one with no entry and one with no event id', () => {
    expect(toWithdrawal({ entryId: '', reason: 'a duplicate' }, 'ev-1')).toBeNull();
    expect(toWithdrawal({ entryId: 'e1', reason: 'a duplicate' }, '')).toBeNull();
  });

  it('sends the reason as written, trimmed', () => {
    const request = toWithdrawal(typeReason(openWithdrawal('e1'), '  a duplicate  '), 'ev-1');
    expect(request).toEqual({ entryId: 'e1', body: { event_id: 'ev-1', reason: 'a duplicate' } });
  });

  it('offers the two reasons this station actually produces', () => {
    // The commonest withdrawal here is the duplicate two assistants made. A reason nobody has to
    // type is a reason that gets written rather than "x".
    expect([...REASON_PRESETS]).toEqual(['duplicate', 'corrected']);
    for (const preset of REASON_PRESETS) {
      expect(messages(en).has(`nutrition.reason.${preset}`)).toBe(true);
      expect(messages(bn).has(`nutrition.reason.${preset}`)).toBe(true);
    }
  });
});

// --- the words on the server's rows ---

describe('the server’s own text is rendered, and never duplicated in the message files', () => {
  it('reads a food, a measure and a note in the operator’s language', () => {
    expect(wordingOf('Boiled rice', 'ভাত', 'bn')).toEqual({
      text: 'ভাত',
      language: 'bn',
      ownLanguage: true,
    });
    expect(wordingOf('Boiled rice', 'ভাত', 'en').text).toBe('Boiled rice');
  });

  it('says so when it falls back to the other language', () => {
    // A Bangla-reading nutritionist handed English with no explanation is one who thinks the app
    // switched languages on them.
    const fallen = wordingOf('Khichuri', '', 'bn');
    expect(fallen).toEqual({ text: 'Khichuri', language: 'en', ownLanguage: false });
    expect(wordingOf('', '', 'en')).toEqual({ text: '', language: null, ownLanguage: false });
  });

  it('holds no food name and no measure name in either message file', () => {
    // The names come back as `_en`/`_bn` pairs on every row. A copy in the message files would be
    // a second table for somebody to correct, and it would go stale first.
    const both = [...messages(en).entries(), ...messages(bn).entries()].filter(([key]) =>
      key.startsWith('nutrition.'),
    );
    for (const [key, value] of both) {
      for (const word of ['Boiled rice', 'ভাত', 'ruti', 'রুটি', 'tablespoon', 'টেবিল চামচ']) {
        expect(value.includes(word), `${key} holds a food or measure name`).toBe(false);
      }
    }
  });
});

// --- what went wrong ---

describe('a refusal is read for what it is, and answered with something to do', () => {
  it('recognises the measure the table cannot weigh, by the field the server named', () => {
    const refusal = troubleOf(
      apiError({
        status: 422,
        fields: { measure_code: 'The table does not know what that weighs.' },
      }),
      'en',
      'entry',
    );
    expect(cannotWeigh(refusal)).toBe(true);
    expect(refusal.message).toBe('The table does not know what that weighs.');
    expect(adviceFor(refusal)).toBe('grams');
    expect(troubleKey(refusal)).toBe('trouble.cannotWeigh');
  });

  it('prefers the measure over any other field a refusal names', () => {
    // The one half of a refusal that has a way forward, whichever key serialised first.
    const refusal = troubleOf(
      apiError({ status: 422, fields: { quantity: 'Too many.', measure_code: 'Unweighable.' } }),
      'en',
      'entry',
    );
    expect(refusal.field).toBe('measure_code');
  });

  it('recognises the other operator getting there first', () => {
    const refusal = troubleOf(
      apiError({ status: 409, code: CODE_ALREADY_WITHDRAWN }),
      'en',
      'withdrawal',
    );
    expect(alreadyWithdrawn(refusal)).toBe(true);
    expect(adviceFor(refusal)).toBe('refresh');
    expect(troubleKey(refusal)).toBe('trouble.alreadyWithdrawn');
  });

  it('offers no retry for a refusal that would fail identically', () => {
    expect(
      adviceFor(troubleOf(apiError({ status: 422, fields: { quantity: 'no' } }), 'en', 'entry')),
    ).toBe('none');
    expect(adviceFor(troubleOf(apiError({ status: 403 }), 'en', 'entry'))).toBe('none');
  });

  it('offers a retry for a tablet that could not reach the server', () => {
    expect(adviceFor(unreachable())).toBe('retry');
    expect(troubleKey(unreachable())).toBe('trouble.unreachable');
  });

  it('reads a refusal in the operator’s own language', () => {
    const refusal = troubleOf(
      apiError({
        status: 422,
        fields: { measure_code: 'Record it in grams.' },
        fieldsBN: { measure_code: 'গ্রামে লিখুন।' },
      }),
      'bn',
      'entry',
    );
    expect(refusal.message).toBe('গ্রামে লিখুন।');
  });

  it('has a sentence in both languages for every trouble this code can produce', () => {
    const english = messages(en);
    const bangla = messages(bn);
    for (const kind of ['refused', 'unreachable', 'failed'] as const) {
      const key = `nutrition.${troubleKey({ ...unreachable(), kind, status: 500 })}`;
      expect(english.has(key), key).toBe(true);
      expect(bangla.has(key), key).toBe(true);
    }
    for (const key of ['trouble.cannotWeigh', 'trouble.alreadyWithdrawn', 'trouble.useGrams']) {
      expect(english.has(`nutrition.${key}`), key).toBe(true);
      expect(bangla.has(`nutrition.${key}`), key).toBe(true);
    }
  });

  it('has a sentence for every group and every missing part', () => {
    // Each of these is a key built at runtime from a value the server sent or this module chose,
    // so the test is what stops a raw identifier arriving under somebody's finger. **Meals are
    // not on this list any more**: their names come from `core.meal`, and what is checked below
    // is that there is no key left for them to come from.
    const english = messages(en);
    const bangla = messages(bn);
    const groups = [
      'GRAIN',
      'PULSE',
      'VEGETABLE',
      'FRUIT',
      'FISH',
      'MEAT',
      'EGG',
      'DAIRY',
      'OIL',
      'SWEET',
      'DRINK',
      'SNACK',
      'OTHER',
    ];
    for (const group of groups) {
      expect(english.has(`nutrition.group.${group}`), group).toBe(true);
      expect(bangla.has(`nutrition.group.${group}`), group).toBe(true);
    }
    for (const part of MISSING) {
      expect(english.has(`nutrition.form.missing.${part}`), part).toBe(true);
      expect(bangla.has(`nutrition.form.missing.${part}`), part).toBe(true);
    }
    for (const relation of DAY_RELATIONS) {
      expect(english.has(`nutrition.day.relation.${relation}`), relation).toBe(true);
      expect(bangla.has(`nutrition.day.relation.${relation}`), relation).toBe(true);
    }
  });

  it('enumerates its attempts and its advice, so neither can grow silently', () => {
    expect([...ATTEMPTS]).toEqual(['foods', 'measures', 'recall', 'entry', 'withdrawal']);
    expect([...ADVICE]).toEqual(['refresh', 'retry', 'grams', 'none']);
  });

  it('writes the numbers inside Bangla sentences as Bengali numerals', () => {
    // ICU `{x, number}` under the `bn` locale, which is what turns 2 into ২. A figure left as a
    // bare Latin digit inside a Bangla sentence is one a reader has to change alphabets for
    // mid-line.
    const withNumbers = [
      'totals.entries',
      'totals.shared',
      'table.body',
      'form.atCap',
      'form.portion',
      'entry.line',
    ];
    const bangla = messages(bn);
    for (const key of withNumbers) {
      expect(bangla.get(`nutrition.${key}`), key).toMatch(/\{\w+, number\}/);
    }
  });
});

function apiError(over: {
  status: number;
  code?: string;
  fields?: Record<string, string>;
  fieldsBN?: Record<string, string>;
}): ApiError {
  return new ApiError({
    status: over.status,
    code: over.code ?? 'VALIDATION_FAILED',
    kind: 'validation',
    messageEN: 'That request cannot be answered.',
    messageBN: 'ওই অনুরোধের উত্তর দেওয়া যাচ্ছে না।',
    fields: over.fields ?? {},
    fieldsBN: over.fieldsBN ?? {},
    correlationID: 'req-1',
  });
}

type Tree = Record<string, unknown>;

function messages(tree: unknown): Map<string, string> {
  const out = new Map<string, string>();
  const walk = (node: Tree, prefix: string) => {
    for (const [key, value] of Object.entries(node)) {
      const path = prefix === '' ? key : `${prefix}.${key}`;
      if (value !== null && typeof value === 'object') walk(value as Tree, path);
      else out.set(path, String(value));
    }
  };
  walk(tree as Tree, '');
  return out;
}

// --- the five calls ---

describe('the calls are what the contract says and nothing more', () => {
  it('asks for the table from the top when nothing has been typed', async () => {
    const calls = stubFetch(json({ foods: [ruti()] }));
    await searchFoods('   ');
    // No `q` at all, rather than an empty one: the endpoint answers that with the list from the
    // top, which is what puts food on screen before anybody types.
    expect(calls[0]!.url).toContain('/v1/foods?');
    expect(calls[0]!.url).not.toContain('q=');
    expect(calls[0]!.url).toContain(`limit=${MAX_RESULTS}`);
  });

  it('sends what the operator typed', async () => {
    const calls = stubFetch(json({ foods: [] }));
    await searchFoods('ruti');
    expect(calls[0]!.url).toContain('q=ruti');
  });

  it('never sends a group filter, because there is no way to pass one', async () => {
    const calls = stubFetch(json({ foods: [] }));
    await searchFoods('ruti');
    expect(calls[0]!.url).not.toContain('group=');
    expect(/group/.test(featureCode('api.ts').replace(/group_code/g, ''))).toBe(false);
  });

  it('reads the vocabulary, the ceilings and the clinic’s calendar in one call', async () => {
    const calls = stubFetch(
      json({
        measures: measures(),
        meals: meals(),
        recall_date_default: '2026-09-04',
        clinic_today: '2026-09-05',
      }),
    );
    const answered = await listReference();
    expect(calls[0]!.url).toContain('/v1/foods/measures');
    expect(answered.measures).toHaveLength(9);
    // The meals arrive **named**, so no client writes its own Bangla for MID_MORNING.
    expect(answered.meals[1]).toEqual({
      code: 'MID_MORNING',
      name_en: 'Mid-morning',
      name_bn: 'দুপুরের আগে',
      ordering: 2,
    });
    // The ceilings are not on this payload: they are on each measure's own row.
    expect(answered.measures[8]).toMatchObject({
      code: 'GRAM',
      universal: true,
      max_quantity: 5000,
    });
    // And the clinic's own calendar, which is the only place either date comes from.
    expect(answered.recallDateDefault).toBe('2026-09-04');
    expect(answered.clinicToday).toBe('2026-09-05');
  });

  it('lets the server choose the day on the first read', async () => {
    const calls = stubFetch(json({ recall: recall(), clinic_today: '2026-09-05' }));
    const answered = await getRecall('pt-1');
    expect(calls[0]!.url).not.toContain('date=');
    // And the answer's own date is what the screen goes on to use.
    expect(answered.recall.recall_date).toBe('2026-09-04');
    // Beside it, the clinic's own today — off this answer rather than off a reference payload
    // fetched hours earlier. A session crossing midnight would otherwise stop warning.
    expect(answered.clinicToday).toBe('2026-09-05');
  });

  it('names the day when the operator has moved it', async () => {
    const calls = stubFetch(
      json({ recall: recall({ recall_date: '2026-09-03' }), clinic_today: '2026-09-05' }),
    );
    await getRecall('pt-1', '2026-09-03');
    expect(calls[0]!.url).toContain('date=2026-09-03');
  });

  it('records one entry, with its own event id as the idempotency key', async () => {
    const calls = stubFetch(
      json({ recall: recall(), clinic_today: '2026-09-05' }, { status: 201 }),
    );
    const answered = await recordDietEntry({
      event_id: 'ev-1',
      patient_id: 'pt-1',
      recall_date: '2026-09-04',
      meal: 'BREAKFAST',
      food_code: 'RUTI_ATTA',
      measure_code: 'PIECE',
      quantity: 2,
    });
    expect(calls[0]!.method).toBe('POST');
    expect(calls[0]!.url).toContain('/v1/diet');
    expect(calls[0]!.requestedWith).toBe('DTHCMS');
    // One attempt, one key: a retry over a bad connection is one row in the ledger.
    expect(calls[0]!.idempotencyKey).toBe('ev-1');
    const sent = JSON.parse(calls[0]!.body) as Record<string, unknown>;
    expect(sent).not.toHaveProperty('grams');
    expect(sent).not.toHaveProperty('kcal');
    // The whole day comes back, not the entry: that is the duplicate-prevention mechanism.
    expect(answered.recall.entries).toHaveLength(2);
    expect(answered.recall.totals.contributors).toBe(2);
    // And a fresh clinic date on the write too, so a recall built across midnight keeps warning.
    expect(answered.clinicToday).toBe('2026-09-05');
  });

  it('takes an entry back through its own path, with a reason', async () => {
    const calls = stubFetch(json({ recall: recall(), clinic_today: '2026-09-05' }));
    const answered = await withdrawDietEntry('e1', { event_id: 'ev-2', reason: 'a duplicate' });
    expect(answered.clinicToday).toBe('2026-09-05');
    expect(calls[0]!.url).toContain('/v1/diet/e1/withdraw');
    expect(calls[0]!.idempotencyKey).toBe('ev-2');
    expect(JSON.parse(calls[0]!.body)).toEqual({ event_id: 'ev-2', reason: 'a duplicate' });
  });

  it('reads which days this patient has a recall for, with the counts', async () => {
    const calls = stubFetch(
      json({
        days: [
          { date: '2026-09-04', entries: 12 },
          { date: '2026-09-01', entries: 1 },
        ],
      }),
    );
    const answered = await listRecallDays('pt-1');
    expect(calls[0]!.url).toContain('/v1/patients/pt-1/diet/days');
    // The count is why this is worth a call: twelve items is a day worth opening and one is a
    // recall somebody abandoned, and bare dates cannot tell them apart.
    expect(answered).toEqual([
      { date: '2026-09-04', entries: 12 },
      { date: '2026-09-01', entries: 1 },
    ]);
  });

  it('returns a failed search rather than throwing it, with its sequence number', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Network request failed')));
    const answer = await runSearch({ seq: 7, q: 'ruti' }, 'en');
    expect(answer.seq).toBe(7);
    expect(answer.ok).toBe(false);
    // A failure with no sequence number could not be aged, and an old timeout would then be free
    // to wipe a newer list.
    if (!answer.ok) expect(answer.trouble.kind).toBe('unreachable');
  });

  it('asks for no total and offers no way to compute one', () => {
    const source = featureCode('api.ts');
    expect(/ENERGY_INTAKE|PROTEIN_INTAKE|CARB_INTAKE|FAT_INTAKE/.test(source)).toBe(false);
    expect(/\breduce\b|\bsum\b/.test(source)).toBe(false);
  });
});

// --- the screen holds no decision this file could hold instead ---

describe('the screen is arrangement, and the decisions are not in it', () => {
  it('touches no web storage anywhere in the feature (ADR-0010)', () => {
    for (const file of featureFiles) {
      const source = featureCode(file);
      expect(/localStorage|sessionStorage|AsyncStorage|indexedDB/.test(source), file).toBe(false);
    }
  });

  it('uses no colour literal', () => {
    for (const file of featureFiles) {
      expect(/#[0-9a-fA-F]{3,8}\b/.test(featureCode(file)), file).toBe(false);
    }
  });

  it('sizes every control from the touch-target token', () => {
    const screen = featureCode('NutritionStation.tsx');
    // CP09's 48-point floor, from the token rather than from a number somebody typed. This is
    // the screen with the tightest time budget in the system and the one where a mis-tap costs
    // most.
    expect(/size\.touchTarget/.test(screen)).toBe(true);
    expect(/minHeight:\s*\d/.test(screen), 'a hand-typed height').toBe(false);
  });

  it('does not decide which measures a food has, or which day is being recalled', () => {
    const screen = featureCode('NutritionStation.tsx');
    // Both are rules with tests above them. A component that re-derived one would be a second
    // implementation nobody runs outside a device.
    expect(/\.portions\b/.test(screen), 'the screen reads portions').toBe(false);
    expect(/new Date\(/.test(screen), 'the screen builds a date').toBe(false);
    expect(/universal\s*===|!\s*[\w.]*\.universal/.test(screen), 'the screen tests universal').toBe(
      false,
    );
  });

  it('never draws a total it worked out itself', () => {
    const screen = featureCode('NutritionStation.tsx');
    // The two obvious wrong renderings: a zero standing in for a figure that has not arrived,
    // and a dash where a number belongs. Both are marks a reader compares with a real one.
    expect(/totals[\w.?]*\s*\?\?\s*0\b/.test(screen)).toBe(false);
    expect(/kcal[\w.?]*\s*\|\|\s*0\b/.test(screen)).toBe(false);
  });

  it('writes nothing to the console', () => {
    for (const file of featureFiles) {
      expect(/console\.(log|warn|error|info|debug)/.test(featureCode(file)), file).toBe(false);
    }
  });
});
