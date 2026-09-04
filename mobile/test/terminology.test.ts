import { afterEach, describe, expect, it, vi } from 'vitest';

import { ApiError, NetworkError } from '@dthcms/api-client';

import en from '../src/messages/en.json';
import bn from '../src/messages/bn.json';
import {
  DEBOUNCE_MS,
  MAX_RESULTS,
  REASONS,
  apply,
  atCap,
  busy,
  catalogueAbsent,
  clearSelection,
  codingOf,
  conceptHeading,
  conceptLabel,
  conceptRows,
  due,
  isSelected,
  issue,
  openPicker,
  reasonFor,
  requestVersion,
  retry,
  sameCoding,
  select,
  selectable,
  tierReason,
  typed,
  visible,
  type Concept,
  type PickerState,
} from '../src/features/terminology/search';

/*
 * The station binding reaches the Keystore through lib/credentials, and the native module
 * cannot load under Node. Mocked exactly as api.test.ts does; nothing here exercises it.
 */
vi.mock('expo-secure-store', () => ({
  setItemAsync: vi.fn(async () => undefined),
  getItemAsync: vi.fn(async () => null),
  deleteItemAsync: vi.fn(async () => undefined),
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'WHEN_UNLOCKED_THIS_DEVICE_ONLY',
}));

const { getConcept, listFavourites, listSystems, runSearch, searchConcepts, troubleOf } =
  await import('../src/features/terminology/api');

/**
 * The coded terminology picker (CP52).
 *
 * The picker itself is a React Native component and is judged on the clinic's tablet, by
 * somebody wearing gloves. What is checked here is every decision behind it — which is all of
 * them, because they were deliberately kept out of the screen.
 *
 * Two of these tests matter more than the rest.
 *
 * **A selection carries system, version and code.** That is acceptance criterion 2, and it is
 * the difference between a diagnosis that can be read back in 2032 and a string. It is tested
 * by name, and tested again from the other direction: a concept arriving without a version
 * cannot become a selection at all.
 *
 * **An older answer cannot overwrite a newer one.** Not "usually does not" — there is one
 * function that can change what is on screen and its first line is the guard, so the test can
 * land the answers in the wrong order deliberately and assert arithmetic rather than luck.
 * On a link that drops for seconds at a time (ADR-0004), out-of-order answers are the normal
 * case, and the failure they cause is silent: a list that looks like an answer to what the
 * clinician typed.
 */

// --- the fixtures ---

const concept = (over: Partial<Concept> = {}): Concept => ({
  system: 'ICD10',
  version: '2019',
  code: 'E11.9',
  display_en: 'Type 2 diabetes mellitus without complications',
  display_bn: 'টাইপ ২ ডায়াবেটিস মেলিটাস, জটিলতাবিহীন',
  ...over,
});

/**
 * The clinic's list as the favourites endpoint returns it: ranked, and with no tier, because
 * it is not a search and there is nothing to rank against.
 */
const FAVOURITES: Concept[] = [
  concept({ code: 'E11.9', favourite_rank: 1 }),
  concept({
    code: 'E11.65',
    display_en: 'Type 2 diabetes mellitus with hyperglycaemia',
    display_bn: 'টাইপ ২ ডায়াবেটিস, রক্তে বেশি শর্করা',
    favourite_rank: 2,
  }),
  concept({
    code: 'E03.9',
    display_en: 'Hypothyroidism, unspecified',
    display_bn: 'হাইপোথাইরয়েডিজম, অনির্দিষ্ট',
    favourite_rank: 3,
  }),
];

const answerOf = (seq: number, concepts: Concept[], version = '2019') => ({
  seq,
  ok: true as const,
  system: 'ICD10',
  version,
  concepts,
});

const apiError = (over: {
  status: number;
  fields?: Record<string, string>;
  fieldsBN?: Record<string, string>;
  messageEN?: string;
  messageBN?: string;
}) =>
  new ApiError({
    status: over.status,
    code: 'VALIDATION_FAILED',
    kind: 'validation',
    messageEN: over.messageEN ?? 'That request cannot be answered.',
    messageBN: over.messageBN ?? 'ওই অনুরোধের উত্তর দেওয়া যাচ্ছে না।',
    fields: over.fields ?? {},
    fieldsBN: over.fieldsBN ?? {},
    correlationID: 'req-1',
  });

function respond(body: unknown, init: { status?: number } = {}) {
  return new Response(JSON.stringify(body), {
    status: init.status ?? 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

/** Stubs the network and hands back the URLs the client actually asked for. */
function stubFetch(...responses: Response[]) {
  const calls: string[] = [];
  let index = 0;
  const mock = vi.fn(async (request: Request) => {
    calls.push(request.url);
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

// --- what a coding is ---

describe('a selection carries system, version and code', () => {
  it('hands its caller all three, never a bare code', () => {
    // Acceptance criterion 2, by name. A coding stamped with a code alone is a string, and
    // the version is only missed years later by somebody trying to read the record back.
    const chosen = concept({ system: 'ICD10', version: '2019', code: 'E11.9' });
    const state = select(openPicker('ICD10'), chosen);

    expect(state.selected).toEqual({ system: 'ICD10', version: '2019', code: 'E11.9' });
    expect(codingOf(chosen)).toEqual({ system: 'ICD10', version: '2019', code: 'E11.9' });
    expect(Object.keys(state.selected!).sort()).toEqual(['code', 'system', 'version']);
  });

  it('takes the version off the concept, not off the picker', () => {
    // The picker resolved 2019; this row says 2016, because the caller asked for a concept
    // recorded years ago. The row wins — it is the pair the display was actually read under.
    const resolved = apply(openPicker('ICD10'), answerOf(1, [], '2019'));
    const older = concept({ version: '2016' });
    expect(select(resolved, older).selected).toEqual({
      system: 'ICD10',
      version: '2016',
      code: 'E11.9',
    });
  });

  it('refuses to store a concept that arrived without a version', () => {
    // The contract makes all three required, which is exactly why this is checked rather
    // than trusted: the check costs nothing and being wrong costs a record nobody can read.
    const state = openPicker('ICD10');
    expect(selectable(concept({ version: '' }))).toBe(false);
    expect(selectable(concept({ version: '   ' }))).toBe(false);
    expect(select(state, concept({ version: '' })).selected).toBeNull();
  });

  it('refuses a concept with no system and one with no code, for the same reason', () => {
    expect(selectable(concept({ system: '' }))).toBe(false);
    expect(selectable(concept({ code: '' }))).toBe(false);
    expect(selectable(concept())).toBe(true);
  });

  it('marks an unusable row as unselectable rather than hiding it', () => {
    // Hiding it would be a result the clinician saw in one build and not the next, with
    // nothing on screen to explain the difference.
    const rows = conceptRows([concept({ code: 'E10.9', version: '' })], 'en');
    expect(rows).toHaveLength(1);
    expect(rows[0]!.selectable).toBe(false);
  });

  it('compares codings on all three parts', () => {
    const coding = codingOf(concept());
    expect(sameCoding(coding, codingOf(concept()))).toBe(true);
    expect(sameCoding(coding, codingOf(concept({ version: '2016' })))).toBe(false);
    expect(sameCoding(coding, codingOf(concept({ system: 'DTHC' })))).toBe(false);
    expect(sameCoding(coding, codingOf(concept({ code: 'E10.9' })))).toBe(false);
    expect(sameCoding(null, coding)).toBe(false);
    expect(sameCoding(coding, null)).toBe(false);
  });

  it('knows which row on screen is the chosen one', () => {
    const state = select(openPicker('ICD10'), concept());
    expect(isSelected(state, concept())).toBe(true);
    expect(isSelected(state, concept({ code: 'E03.9' }))).toBe(false);
    expect(isSelected(clearSelection(state), concept())).toBe(false);
  });
});

// --- the words on a row ---

describe('what a concept is called', () => {
  it('is the Bangla when the interface is Bangla and there is Bangla', () => {
    expect(conceptLabel(concept(), 'bn')).toBe('টাইপ ২ ডায়াবেটিস মেলিটাস, জটিলতাবিহীন');
  });

  it('falls back to English when there is no Bangla, in both locales', () => {
    // An English row is not a good outcome; a blank one is a worse one. The clinician cannot
    // tell a missing translation from a missing result, and the row is tappable either way.
    const untranslated = concept({ display_bn: undefined });
    expect(conceptLabel(untranslated, 'bn')).toBe('Type 2 diabetes mellitus without complications');
    expect(conceptLabel(untranslated, 'en')).toBe('Type 2 diabetes mellitus without complications');
    expect(conceptLabel(concept({ display_bn: '   ' }), 'bn')).toBe(
      'Type 2 diabetes mellitus without complications',
    );
  });

  it('never shows the Bangla to an English interface', () => {
    expect(conceptLabel(concept(), 'en')).toBe('Type 2 diabetes mellitus without complications');
  });

  it('is never empty, even for a concept with no display at all', () => {
    const bare = concept({ display_en: '  ', display_bn: '' });
    expect(conceptLabel(bare, 'en')).toBe('E11.9');
    expect(conceptLabel(bare, 'bn')).toBe('E11.9');
  });
});

// --- why a row is where it is ---

describe('the reason behind a rank', () => {
  it('names each of the server’s four tiers', () => {
    expect(tierReason(1)).toBe('code');
    expect(tierReason(2)).toBe('favourite');
    expect(tierReason(3)).toBe('wordStart');
    expect(tierReason(4)).toBe('similar');
  });

  it('calls tier 4 a guess, because it is one', () => {
    // Tier 4 is the trigram tier: the catalogue matched a misspelling. The clinician has to
    // know that before they tap, which means the row has to say so rather than the ranking.
    const guess = concept({ tier: 4, score: 0.42 });
    expect(reasonFor(guess)).toBe('similar');
  });

  it('has no opinion about a tier the server has not got', () => {
    expect(tierReason(undefined)).toBeNull();
    expect(tierReason(0)).toBeNull();
    expect(tierReason(9)).toBeNull();
  });

  it('reads a favourites row by its rank, because that list is not a search', () => {
    expect(reasonFor(concept({ favourite_rank: 3 }))).toBe('clinicList');
    expect(reasonFor(concept())).toBeNull();
  });

  it('prefers the tier when a search returns a favourite', () => {
    // Tier 2 is "a favourite whose words start with the query", and it already means the
    // match; the rank alone would not say why this one and not another favourite.
    expect(reasonFor(concept({ tier: 2, favourite_rank: 1 }))).toBe('favourite');
  });
});

// --- the list, as it is drawn ---

describe('the results as they are drawn', () => {
  it('keeps the server’s order and never re-sorts it', () => {
    // ORDER BY tier, favourite_rank, score DESC happens in one SQL statement, and it is the
    // only ranking the clinic can tune. A client that disagreed would disagree invisibly.
    const ranked = [
      concept({ code: 'E11', tier: 1 }),
      concept({ code: 'E11.9', tier: 2, favourite_rank: 1 }),
      concept({ code: 'E10.9', tier: 3 }),
      concept({ code: 'E13.9', tier: 4, score: 0.31 }),
    ];
    expect(conceptRows(ranked, 'en').map((row) => row.concept.code)).toEqual([
      'E11',
      'E11.9',
      'E10.9',
      'E13.9',
    ]);
  });

  it('captions a chapter where it changes, and not again inside the run', () => {
    const rows = conceptRows(
      [
        concept({ code: 'E11.9', heading: 'Endocrine, nutritional and metabolic diseases' }),
        concept({ code: 'E03.9', heading: 'Endocrine, nutritional and metabolic diseases' }),
        concept({ code: 'I10', heading: 'Diseases of the circulatory system' }),
        concept({ code: 'R73.9' }),
      ],
      'en',
    );
    expect(rows.map((row) => row.heading)).toEqual([
      'Endocrine, nutritional and metabolic diseases',
      '',
      'Diseases of the circulatory system',
      '',
    ]);
  });

  it('captions the chapter in Bangla when the interface is in Bangla', () => {
    // The bug this was written for: a column of Bengali diagnoses filed under English
    // chapter names. Half-bilingual reads as an interface somebody translated the easy
    // parts of, and a database rule now refuses a heading with no Bangla form.
    const rows = conceptRows(
      [concept({ code: 'E11.9', heading: 'Diabetes', heading_bn: 'ডায়াবেটিস' })],
      'bn',
    );
    expect(rows[0]!.heading).toBe('ডায়াবেটিস');
    expect(conceptHeading(concept({ heading: 'Diabetes' }), 'bn')).toBe('Diabetes');
    expect(conceptHeading(concept({}), 'en')).toBe('');
  });

  it('re-captions a chapter that comes back after another one', () => {
    // Because the list is walked in order rather than grouped: an exact code match must not
    // sink below a favourite two rows down merely because they share a chapter.
    const rows = conceptRows(
      [
        concept({ code: 'E11.9', heading: 'Endocrine' }),
        concept({ code: 'I10', heading: 'Circulatory' }),
        concept({ code: 'E03.9', heading: 'Endocrine' }),
      ],
      'en',
    );
    expect(rows.map((row) => row.heading)).toEqual(['Endocrine', 'Circulatory', 'Endocrine']);
  });

  it('respects the twenty-five cap in the display logic', () => {
    // The server caps at 25 and this caps again on the way out. A picker that quietly became
    // scrollable to fifty rows is a picker where the twenty-first result is chosen by
    // whoever scrolls furthest.
    const many = Array.from({ length: 40 }, (_, i) => concept({ code: `X${i}` }));
    expect(MAX_RESULTS).toBe(25);
    expect(visible(many)).toHaveLength(25);
    expect(conceptRows(many, 'en')).toHaveLength(25);
    expect(conceptRows(many, 'en').at(-1)!.concept.code).toBe('X24');
  });

  it('leaves a short list alone', () => {
    expect(visible(FAVOURITES)).toHaveLength(3);
    expect(atCap(FAVOURITES)).toBe(false);
  });

  it('says so when the list came back full', () => {
    // The bottom of this list is not the bottom of the catalogue, and only a clinician who
    // is told so can write a better query.
    const full = Array.from({ length: MAX_RESULTS }, (_, i) => concept({ code: `X${i}` }));
    expect(atCap(full)).toBe(true);
  });
});

// --- opening the picker ---

describe('the picker as it opens', () => {
  it('shows the clinic’s favourites, in rank order, before anybody types', () => {
    // Criterion 1's cheap half: the diagnoses DTHC actually makes cost no keystrokes at all.
    let state = openPicker('ICD10');
    expect(state.query).toBe('');
    const { request } = issue(state, 0);
    expect(request.q).toBe('');

    state = apply(state, answerOf(request.seq, FAVOURITES));
    expect(conceptRows(state.concepts, 'en').map((row) => row.concept.code)).toEqual([
      'E11.9',
      'E11.65',
      'E03.9',
    ]);
    expect(state.concepts.map((c) => c.favourite_rank)).toEqual([1, 2, 3]);
    expect(conceptRows(state.concepts, 'en').map((row) => row.reason)).toEqual([
      'clinicList',
      'clinicList',
      'clinicList',
    ]);
  });

  it('fetches them at once rather than waiting out a debounce nobody typed into', () => {
    // 180 ms of blank list on open is 180 ms of an operator wondering whether the tap took.
    expect(due(openPicker('ICD10'), 0)).toBe(true);
  });

  it('names no version until the catalogue has resolved one', () => {
    const state = openPicker('ICD10');
    expect(state.version).toBe('');
    expect(issue(state, 0).request.version).toBe('');
  });

  it('carries an explicitly asked-for version straight through', () => {
    expect(issue(openPicker('ICD10', '2016'), 0).request.version).toBe('2016');
  });

  it('names the resolved version on every request after the first', () => {
    // A deployment that loaded a newer ICD-10 between two keystrokes would otherwise hand
    // one clinician two versions inside one search.
    let state = openPicker('ICD10');
    state = apply(issue(state, 0).state, answerOf(1, FAVOURITES, '2019'));
    expect(requestVersion(state)).toBe('2019');
    state = typed(state, 'dia', 1000);
    expect(issue(state, 1000).request.version).toBe('2019');
  });
});

// --- the keystroke machine ---

describe('the debounce', () => {
  it('waits for the box to be still before spending a request', () => {
    // A three-letter burst is one request rather than three, on a link that may be a shared
    // 3G connection for the whole clinic.
    let state = apply(issue(openPicker('ICD10'), 0).state, answerOf(1, FAVOURITES));
    state = typed(state, 'd', 1000);
    state = typed(state, 'di', 1080);
    state = typed(state, 'dia', 1160);
    expect(due(state, 1200)).toBe(false);
    expect(due(state, 1160 + DEBOUNCE_MS)).toBe(true);
  });

  it('does not re-send a query that has already gone out', () => {
    // Deleting a letter and typing it back costs nothing, which is what lets the debounce
    // stay short enough to be invisible.
    let state = openPicker('ICD10');
    state = typed(state, 'dia', 0);
    state = issue(state, DEBOUNCE_MS).state;
    expect(due(state, 10_000)).toBe(false);
    state = typed(state, 'diab', 10_000);
    state = typed(state, 'dia', 10_050);
    expect(due(state, 10_050 + DEBOUNCE_MS)).toBe(false);
  });

  it('treats an unchanged keystroke as no keystroke at all', () => {
    const state = typed(openPicker('ICD10'), '', 5000);
    expect(state.changedAt).toBe(0);
  });

  it('hands out sequence numbers that only ever go up', () => {
    let state = openPicker('ICD10');
    const seqs: number[] = [];
    for (const [text, at] of [
      ['d', 0],
      ['di', 1000],
      ['dia', 2000],
    ] as const) {
      state = typed(state, text, at);
      const issued = issue(state, at + DEBOUNCE_MS);
      state = issued.state;
      seqs.push(issued.request.seq);
    }
    expect(seqs).toEqual([1, 2, 3]);
  });

  it('is busy exactly while an answer is owed', () => {
    let state = openPicker('ICD10');
    expect(busy(state)).toBe(false);
    state = issue(state, 0).state;
    expect(busy(state)).toBe(true);
    state = apply(state, answerOf(1, FAVOURITES));
    expect(busy(state)).toBe(false);
  });
});

describe('an older answer can never overwrite a newer one', () => {
  /** Two searches in flight: "dia" first, "diabetic foot" second. */
  function twoInFlight(): { state: PickerState; first: number; second: number } {
    let state = openPicker('ICD10');
    state = typed(state, 'dia', 0);
    const first = issue(state, DEBOUNCE_MS);
    state = first.state;
    state = typed(state, 'diabetic foot', 1000);
    const second = issue(state, 1000 + DEBOUNCE_MS);
    state = second.state;
    return { state, first: first.request.seq, second: second.request.seq };
  }

  const stale = [concept({ code: 'E11.9', display_en: 'Type 2 diabetes' })];
  const fresh = [concept({ code: 'E11.621', display_en: 'Type 2 diabetes with foot ulcer' })];

  it('drops a slow answer that lands after a fast one', () => {
    // Structural, not a race the debounce usually wins: the guard is the first line of the
    // only function that can replace what is on screen.
    const { state, first, second } = twoInFlight();
    const after = apply(apply(state, answerOf(second, fresh)), answerOf(first, stale));
    expect(after.concepts.map((c) => c.code)).toEqual(['E11.621']);
    expect(after.applied).toBe(second);
  });

  it('shows the newer answer when they land in order', () => {
    const { state, first, second } = twoInFlight();
    const after = apply(apply(state, answerOf(first, stale)), answerOf(second, fresh));
    expect(after.concepts.map((c) => c.code)).toEqual(['E11.621']);
  });

  it('will not let a stale failure wipe a fresh list either', () => {
    // The half-undo: an old request timing out after a newer one has already landed. The
    // sequence number travels with the failure precisely so this cannot happen.
    const { state, first, second } = twoInFlight();
    let after = apply(state, answerOf(second, fresh));
    after = apply(after, { seq: first, ok: false, trouble: { kind: 'unreachable' } });
    expect(after.concepts.map((c) => c.code)).toEqual(['E11.621']);
    expect(after.trouble).toBeNull();
  });

  it('will not let a stale answer undo a fresh failure', () => {
    const { state, first, second } = twoInFlight();
    let after = apply(state, { seq: second, ok: false, trouble: { kind: 'unreachable' } });
    after = apply(after, answerOf(first, stale));
    expect(after.concepts).toEqual([]);
    expect(after.trouble).toEqual({ kind: 'unreachable' });
  });

  it('ignores an answer to a request that has already been answered', () => {
    const { state, second } = twoInFlight();
    const once = apply(state, answerOf(second, fresh));
    expect(apply(once, answerOf(second, stale))).toBe(once);
  });
});

// --- when it goes wrong ---

describe('a catalogue that refuses', () => {
  it('shows the server’s own sentence about the field it named', () => {
    // An unknown system, a version nobody loaded, SNOMED pending D-24: each is a sentence
    // the backend already writes in both languages. A client that paraphrased them would be
    // inventing a second, staler account of somebody else's licensing.
    const refused = troubleOf(
      apiError({
        status: 422,
        fields: { system: 'This clinic is not licensed to use that terminology.' },
        fieldsBN: { system: 'ওই টার্মিনোলজি ব্যবহারের লাইসেন্স এই ক্লিনিকের নেই।' },
      }),
      'en',
    );
    expect(refused).toEqual({
      kind: 'refused',
      field: 'system',
      message: 'This clinic is not licensed to use that terminology.',
    });
  });

  it('shows the Bangla sentence to a Bangla interface', () => {
    const refused = troubleOf(
      apiError({
        status: 422,
        fields: { version: 'That version of the terminology is not loaded here.' },
        fieldsBN: { version: 'ওই সংস্করণটি এখানে লোড করা নেই।' },
      }),
      'bn',
    );
    expect(refused).toEqual({
      kind: 'refused',
      field: 'version',
      message: 'ওই সংস্করণটি এখানে লোড করা নেই।',
    });
  });

  it('shows English rather than nothing when only English exists', () => {
    const refused = troubleOf(
      apiError({ status: 422, fields: { version: 'No version has been loaded yet.' } }),
      'bn',
    );
    expect(refused).toEqual({
      kind: 'refused',
      field: 'version',
      message: 'No version has been loaded yet.',
    });
  });

  it('reports the field the operator can act on first', () => {
    const refused = troubleOf(
      apiError({ status: 422, fields: { version: 'Name a version.', system: 'Name a system.' } }),
      'en',
    );
    expect(refused).toMatchObject({ field: 'system' });
  });

  it('still shows a field this build has never heard of', () => {
    // A newer server knowing about a parameter this tablet does not is not a reason to show
    // the operator nothing; the name is what a support call quotes.
    expect(
      troubleOf(apiError({ status: 422, fields: { zzz: 'Unexpected.', aaa: 'Also.' } }), 'en'),
    ).toEqual({ kind: 'refused', field: 'aaa', message: 'Also.' });
  });

  it('falls back to the top-level sentence when the server named no field', () => {
    expect(troubleOf(apiError({ status: 422 }), 'bn')).toEqual({
      kind: 'refused',
      field: '',
      message: 'ওই অনুরোধের উত্তর দেওয়া যাচ্ছে না।',
    });
  });

  it('is not the same thing as the catalogue being absent', () => {
    // A refusal is about the request; free text is not the answer to it, and offering it
    // would tell the operator to work around a rule the clinic actually has.
    expect(catalogueAbsent({ kind: 'refused', field: 'system', message: 'no' })).toBe(false);
    expect(catalogueAbsent(null)).toBe(false);
  });
});

describe('a catalogue that cannot be reached', () => {
  it('says the request never left the tablet', () => {
    expect(troubleOf(new NetworkError(new TypeError('Network request failed')), 'en')).toEqual({
      kind: 'unreachable',
    });
  });

  it('treats a server that answered badly as the same kind of absence', () => {
    // Different cause, same place for the operator to be: the catalogue is not answering,
    // and free text is the way on.
    const failed = troubleOf(
      apiError({ status: 503, messageEN: 'Service unavailable.', messageBN: 'সেবা মিলছে না।' }),
      'bn',
    );
    expect(failed).toEqual({ kind: 'failed', message: 'সেবা মিলছে না।' });
    expect(catalogueAbsent(failed)).toBe(true);
  });

  it('falls back to English when the server wrote no Bangla', () => {
    expect(troubleOf(apiError({ status: 500, messageBN: '  ' }), 'bn')).toEqual({
      kind: 'failed',
      message: 'That request cannot be answered.',
    });
  });

  it('says nothing it cannot stand behind about a failure it does not recognise', () => {
    // The screen supplies the sentence. There is nothing in a stray exception worth showing
    // a clinician standing in front of a patient.
    expect(troubleOf(new Error('boom'), 'en')).toEqual({ kind: 'failed', message: '' });
    expect(catalogueAbsent({ kind: 'failed', message: '' })).toBe(true);
  });

  it('clears the results with the failure, so no list outlives the box above it', () => {
    // A list left standing under a query that no longer matches it is the one way this
    // picker could put the wrong diagnosis under somebody's finger.
    let state = apply(issue(openPicker('ICD10'), 0).state, answerOf(1, FAVOURITES));
    state = typed(state, 'hypert', 1000);
    state = issue(state, 1000 + DEBOUNCE_MS).state;
    state = apply(state, { seq: 2, ok: false, trouble: { kind: 'unreachable' } });
    expect(state.concepts).toEqual([]);
    expect(state.trouble).toEqual({ kind: 'unreachable' });
  });

  it('lets the operator ask again for the very same query', () => {
    let state = typed(openPicker('ICD10'), 'dia', 0);
    state = issue(state, DEBOUNCE_MS).state;
    state = apply(state, { seq: 1, ok: false, trouble: { kind: 'unreachable' } });
    expect(due(state, 10_000)).toBe(false);

    state = retry(state, 10_000);
    expect(state.trouble).toBeNull();
    expect(due(state, 10_000)).toBe(true);
    expect(issue(state, 10_000).request.q).toBe('dia');
  });
});

// --- the four calls ---

describe('the four calls', () => {
  it('lists the terminologies this deployment holds', async () => {
    stubFetch(
      respond({
        systems: [
          {
            code: 'ICD10',
            title_en: 'ICD-10',
            title_bn: 'আইসিডি-১০',
            publisher: 'World Health Organization',
            usable: true,
            default_version: '2019',
          },
          {
            code: 'SNOMED',
            title_en: 'SNOMED CT',
            title_bn: 'স্নোমেড সিটি',
            publisher: 'SNOMED International',
            usable: false,
            licence_note: 'Pending D-24.',
          },
        ],
      }),
    );
    const systems = await listSystems();
    expect(systems.map((s) => s.code)).toEqual(['ICD10', 'SNOMED']);
    // The unusable system is returned, not hidden: "no results" and "we are not licensed
    // for that" send a clinician to two different places.
    expect(systems[1]!.usable).toBe(false);
  });

  it('asks the favourites endpoint for the favourites', async () => {
    const calls = stubFetch(respond({ system: 'ICD10', version: '2019', concepts: FAVOURITES }));
    const body = await listFavourites({ system: 'ICD10' });
    expect(calls[0]).toContain('/v1/terminology/favourites');
    expect(calls[0]).toContain('system=ICD10');
    // No version asked for means no parameter at all. An empty one is refused, and being
    // refused for not choosing is not the same as not choosing.
    expect(calls[0]).not.toContain('version=');
    expect(body.version).toBe('2019');
  });

  it('names a version when one was asked for', async () => {
    const calls = stubFetch(respond({ system: 'ICD10', version: '2016', concepts: [] }));
    await listFavourites({ system: 'ICD10', version: '2016' });
    expect(calls[0]).toContain('version=2016');
  });

  it('searches with the query and the limit it was given', async () => {
    const calls = stubFetch(respond({ system: 'ICD10', version: '2019', concepts: [] }));
    await searchConcepts({ system: 'ICD10', q: 'dia', limit: 10 });
    expect(calls[0]).toContain('/v1/terminology/search');
    expect(calls[0]).toContain('q=dia');
    expect(calls[0]).toContain('limit=10');
  });

  it('fetches one concept and its mappings', async () => {
    // What a screen needs to render a coding recorded years ago under a version nobody
    // searches any more — which is why the version is stored in the first place.
    const calls = stubFetch(
      respond({
        concept: concept({ version: '2016' }),
        mappings: [
          { to_system: 'SNOMED', to_version: '2024-01', to_code: '44054006', equivalence: 'wider' },
        ],
      }),
    );
    const body = await getConcept({ system: 'ICD10', version: '2016', code: 'E11.9' });
    expect(calls[0]).toContain('/v1/terminology/concept');
    expect(calls[0]).toContain('code=E11.9');
    expect(body.concept.version).toBe('2016');
    expect(body.mappings[0]!.equivalence).toBe('wider');
  });

  it('throws the shared error classes, so the trouble mapping has something to read', async () => {
    stubFetch(
      respond(
        {
          error: {
            code: 'VALIDATION_FAILED',
            kind: 'validation',
            message: 'That terminology is not one this clinic holds.',
            message_bn: 'এই ক্লিনিকে ওই টার্মিনোলজি নেই।',
            fields: { system: 'That terminology is not one this clinic holds.' },
          },
        },
        { status: 422 },
      ),
    );
    const error = await listFavourites({ system: 'NOPE' }).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect(troubleOf(error, 'en')).toMatchObject({ kind: 'refused', field: 'system' });
  });
});

describe('one request, answered', () => {
  it('goes to the favourites endpoint when nobody has typed', async () => {
    // The catalogue offers the favourites as their own endpoint precisely so a picker
    // opening on a list does not have to run a search to get one.
    const calls = stubFetch(respond({ system: 'ICD10', version: '2019', concepts: FAVOURITES }));
    const answer = await runSearch({ seq: 1, system: 'ICD10', version: '', q: '  ' }, 'en');
    expect(calls[0]).toContain('/v1/terminology/favourites');
    expect(answer.ok).toBe(true);
    expect(answer).toMatchObject({ seq: 1, system: 'ICD10', version: '2019' });
  });

  it('goes to the search endpoint once somebody has', async () => {
    const calls = stubFetch(respond({ system: 'ICD10', version: '2019', concepts: [] }));
    await runSearch({ seq: 2, system: 'ICD10', version: '2019', q: 'dia' }, 'en');
    expect(calls[0]).toContain('/v1/terminology/search');
    expect(calls[0]).toContain('q=dia');
  });

  it('brings a failure back with its sequence number rather than throwing it', async () => {
    // A failure with no sequence number cannot be aged, and an old request timing out would
    // then be free to wipe a newer list that had already landed.
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Network request failed')));
    const answer = await runSearch({ seq: 7, system: 'ICD10', version: '', q: 'dia' }, 'en');
    expect(answer).toEqual({ seq: 7, ok: false, trouble: { kind: 'unreachable' } });
  });

  it('feeds straight into the picker’s one door', async () => {
    stubFetch(respond({ system: 'ICD10', version: '2019', concepts: FAVOURITES }));
    const opened = issue(openPicker('ICD10'), 0);
    const state = apply(opened.state, await runSearch(opened.request, 'en'));
    expect(state.version).toBe('2019');
    expect(state.concepts).toHaveLength(3);
    expect(busy(state)).toBe(false);
  });
});

// --- the words ---

describe('every sentence this picker needs exists in both languages', () => {
  const messages = { en: en as Record<string, Record<string, unknown>>, bn: bn as Record<string, Record<string, unknown>> }; // prettier-ignore

  const lookup = (language: 'en' | 'bn', path: string): unknown =>
    path
      .split('.')
      .reduce<unknown>(
        (node, key) =>
          node !== null && typeof node === 'object'
            ? (node as Record<string, unknown>)[key]
            : undefined,
        messages[language],
      );

  const present = (path: string) => {
    for (const language of ['en', 'bn'] as const) {
      expect(typeof lookup(language, path), `${language}: ${path}`).toBe('string');
    }
  };

  it('has a sentence for every reason a row can carry', () => {
    // The screen looks the key up from the row, so a reason added without a sentence would
    // show a clinician a raw identifier where the explanation belongs.
    expect(REASONS).toHaveLength(5);
    for (const reason of REASONS) present(`terminology.reason.${reason}`);
  });

  it('has a reason for every tier the server can return', () => {
    for (const tier of [1, 2, 3, 4] as const) {
      present(`terminology.reason.${tierReason(tier)}`);
    }
  });

  it('tells the operator free text is still available, in both languages', () => {
    // The sentence that keeps an unreachable catalogue from stopping a consultation.
    present('terminology.freeText');
    present('terminology.retry');
    // One heading per trouble. "Cannot be reached" and "could not answer" leave the operator
    // in the same place but are not the same fact.
    present('terminology.refused');
    present('terminology.unreachable');
    present('terminology.failed');
  });

  it('has the chip that carries all three parts of a coding', () => {
    for (const language of ['en', 'bn'] as const) {
      const coding = lookup(language, 'terminology.coding') as string;
      for (const placeholder of ['{system}', '{code}', '{version}']) {
        expect(coding, `${language} coding`).toContain(placeholder);
      }
    }
  });
});
