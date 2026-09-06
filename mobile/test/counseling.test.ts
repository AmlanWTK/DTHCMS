import { readFileSync, readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { afterEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '@dthcms/api-client';

import en from '../src/messages/en.json';
import bn from '../src/messages/bn.json';
import {
  ACTS,
  ADVICE,
  ATTEMPTS,
  CLINIC_UTC_OFFSET_MINUTES,
  CODE_ALREADY_COVERED,
  ITEM_CODE,
  NOTE_MAX,
  PERM_TICK,
  PROBLEMS,
  REASON_MAX,
  TAPS_PER_ITEM,
  TAPS_TO_FINISH,
  TAPS_TO_START,
  TONES,
  adviceFor,
  alreadyCovered,
  attemptKey,
  attributionKey,
  attributionOf,
  checklistTitle,
  choicesOf,
  clockTime,
  completionOf,
  coveredBy,
  eventFor,
  finishRefused,
  historyOf,
  holdEvent,
  keepsItsEvent,
  mayTick,
  needsReason,
  noteOpenable,
  onTheList,
  oneItemCode,
  progressOf,
  readsBack,
  releaseEvent,
  resumeOf,
  roomsOf,
  rowsOf,
  sessionOpen,
  sessionTitle,
  startableOf,
  tapsToComplete,
  tickFor,
  tickIsLive,
  tickProblem,
  toCompletion,
  toStart,
  toTick,
  toUntick,
  toneFor,
  troubleKey,
  untickProblem,
  untickRefused,
  wordingOf,
  type Attempt,
  type CounselingChecklist,
  type CounselingItem,
  type CounselingSession,
  type CounselingTick,
  type Reader,
  type Trouble,
} from '../src/features/counseling/state';

/*
 * The station binding reaches the Keystore through lib/credentials, and the native module
 * cannot load under Node. Mocked exactly as allergies.test.ts and history.test.ts do; nothing
 * here exercises it.
 */
vi.mock('expo-secure-store', () => ({
  setItemAsync: vi.fn(async () => undefined),
  getItemAsync: vi.fn(async () => null),
  deleteItemAsync: vi.fn(async () => undefined),
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'WHEN_UNLOCKED_THIS_DEVICE_ONLY',
}));

const counselingApi = await import('../src/features/counseling/api');
const counselingState = await import('../src/features/counseling/state');
const {
  completeCounselingSession,
  getCounselingSession,
  listCounselingChecklistsForVisit,
  listCounselingSessionsForVisit,
  startCounselingSession,
  tickCounselingItem,
  troubleOf,
  untickCounselingItem,
} = counselingApi;

/**
 * Counselling ticking, at stations 3 and 7 (CP56).
 *
 * The screen is a React Native component and is judged in a clinic room by a counsellor with a
 * patient in front of them. What is checked here is every decision behind it, and six of these
 * tests matter more than the rest.
 *
 * **Nothing can tick a list.** There is no exported function with an array parameter, `toTick`
 * builds one body with one code, and `oneItemCode` refuses a string that is secretly a list.
 * Criterion 1 is only as strong as the smallest act a client can send.
 *
 * **Seven items is eight taps.** Counted, so that a future redesign adding a confirmation step
 * fails here rather than in a busy clinic with a stopwatch.
 *
 * **An un-tick without a reason never leaves the tablet.** Whitespace is not a reason.
 *
 * **Progress is two integers.** There is no percentage anywhere in this feature, and the
 * covered figure is the server's outstanding list subtracted from the mandatory count — never a
 * second count of the ticks, because the gate reads the first one.
 *
 * **Finishing with items outstanding is allowed, named and never drawn as a failure.** `Tone`
 * has no failure value at all, which is the structural version of the same promise.
 *
 * **A tick made by somebody else never reads as "you did this",** including when this tablet
 * does not yet know who is holding it.
 */

// --- the fixtures: the seeded seven-item diabetes checklist (migration 00037) ---

const item = (
  code: string,
  ordering: number,
  room: string,
  over: Partial<CounselingItem> = {},
): CounselingItem => ({
  item_code: code,
  ordering,
  text_en: `${code} in English`,
  text_bn: `${code} বাংলায়`,
  guidance_en: `Say this about ${code}.`,
  guidance_bn: `${code} নিয়ে এটা বলুন।`,
  mandatory: true,
  room,
  room_en: room === 'NUTRITION_ROOM' ? 'Nutrition room' : 'Counseling room',
  room_bn: room === 'NUTRITION_ROOM' ? 'পুষ্টি রুম' : 'কাউন্সেলিং রুম',
  room_step: room === 'NUTRITION_ROOM' ? 2 : 1,
  // The queue the room belongs to, on the item. The insulin corner is worked from the
  // counselling station rather than being a station of its own — which is exactly the fact
  // that used to cost a second fetch of the room catalogue.
  room_station: room === 'NUTRITION_ROOM' ? 'STN_NUTRITION' : 'STN_COUNSELING',
  ...over,
});

/**
 * The seven §5.1 items, in the seeded order — which interleaves the rooms.
 *
 * `DIET` is item three and belongs to the nutrition room, between two counselling-room items.
 * That is not a fixture convenience; it is what migration 00037 actually seeds, and it is the
 * case that would break a grouping written to assume rooms arrive contiguously.
 */
const ITEMS: CounselingItem[] = [
  item('DISEASE_UNDERSTANDING', 1, 'COUNSELING_ROOM'),
  item('COMPLICATIONS', 2, 'COUNSELING_ROOM'),
  item('DIET', 3, 'NUTRITION_ROOM'),
  item('EXERCISE', 4, 'COUNSELING_ROOM'),
  item('SELF_CARE', 5, 'COUNSELING_ROOM'),
  item('GLUCOMETER', 6, 'COUNSELING_ROOM'),
  item('INSULIN_TECHNIQUE', 7, 'INSULIN_CORNER', {
    room_en: 'Insulin corner',
    room_bn: 'ইনসুলিন কর্নার',
    room_step: 3,
  }),
];

/** What the server says this visit calls for: one rule matched, nothing opened yet. */
const checklist = (over: Partial<CounselingChecklist> = {}): CounselingChecklist => ({
  template_id: 'template-1',
  template_code: 'DIABETES',
  title_en: 'Diabetes counselling',
  title_bn: 'ডায়াবেটিস কাউন্সেলিং',
  version: 1,
  priority: 100,
  matched_system: 'ICD10',
  matched_version: '2019',
  matched_code: 'E11.9',
  // Always sent, and required by the contract. With it optional, "this checklist is not
  // finished" and "you were not told" were the same absence and the phone had to guess.
  complete: false,
  ...over,
});

const ME = 'counsellor-1';
const SOMEBODY_ELSE = 'counsellor-2';
const READER: Reader = { locale: 'en', me: ME };

const tick = (over: Partial<CounselingTick> & { item_code: string }): CounselingTick => ({
  ticked_at: '2026-09-04T05:02:00.000Z',
  ticked_by: ME,
  ticked_role: 'COUNSELOR',
  undo_count: 0,
  ...over,
});

function session(over: Partial<CounselingSession> = {}): CounselingSession {
  const items = over.items ?? ITEMS;
  const ticks = over.ticks ?? [];
  const covered = new Set(
    ticks.filter((one) => (one.undone_at ?? '') === '').map((one) => one.item_code),
  );
  return {
    id: 'session-1',
    patient_id: 'patient-1',
    visit_id: 'visit-1',
    template_id: 'template-1',
    template_version: 1,
    template_code: 'DIABETES',
    title_en: 'Diabetes counselling',
    title_bn: 'ডায়াবেটিস কাউন্সেলিং',
    started_at: '2026-09-04T04:55:00.000Z',
    started_by: ME,
    started_role: 'COUNSELOR',
    items,
    ticks,
    // The server's own list, standing in for `core.counseling_outstanding`.
    outstanding: items
      .filter((one) => one.mandatory && !covered.has(one.item_code))
      .map((one) => one.item_code),
    ...over,
  };
}

const ids = { event: 'event-1' };

// --- the network ---

function respond(body: unknown, init: { status?: number } = {}) {
  const status = init.status ?? 200;
  if (status === 204) return new Response(null, { status });
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

/** Stubs the network and hands back what the client actually sent. */
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
  'counseling',
);

function featureSource(file: string): string {
  return readFileSync(join(featureDir, file), 'utf8');
}

/**
 * The file with its comments stripped.
 *
 * These tests are about what the code does, not about the prose beside it. A comment saying
 * "there is no confirmation dialog here" would otherwise fail the test that checks there is no
 * confirmation dialog here, which would teach the next person to stop writing the comment.
 */
function featureCode(file: string): string {
  return featureSource(file)
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/.*$/gm, '$1');
}

// --- criterion 1: one tick is one act ---

describe('nothing in this feature can tick more than one item', () => {
  it('exports no function that takes a list of item codes', () => {
    // The named test criterion 1 asks for. `api.ts` is where a `tickAll` would be added, by
    // somebody reasonably trying to save round trips, and this is what stops it: no parameter
    // anywhere in that file is an array, and none of the exported names reads like a batch.
    const source = featureCode('api.ts');
    expect(/:\s*(readonly\s+)?string\[\]/.test(source), 'no string[] parameter in api.ts').toBe(
      false,
    );
    expect(/Array<\s*string\s*>/.test(source), 'no Array<string> in api.ts').toBe(false);
    expect(/item_codes|itemCodes|codes\s*:/.test(source), 'nothing plural in api.ts').toBe(false);

    const batchy = Object.keys(counselingApi).filter((name) =>
      /all|bulk|batch|many|rest|everything|remaining/i.test(name),
    );
    expect(batchy, 'no export whose name is a batch').toEqual([]);
  });

  it('has exactly these calls and no others', () => {
    // Every read and write, named. Another would have to be added here, in front of a
    // reviewer, next to the sentence in `api.ts` saying why there is no batch.
    expect(Object.keys(counselingApi).sort()).toEqual([
      'completeCounselingSession',
      'getCounselingGate',
      'getCounselingSession',
      'listCounselingChecklistsForVisit',
      'listCounselingSessionsForVisit',
      'startCounselingSession',
      'tickCounselingItem',
      'troubleOf',
      'untickCounselingItem',
    ]);
    // And the room catalogue is still not among them. `room_station` on a session's item carries
    // the only thing that fetch bought here, and CP57's gate carries its own `room_en`/`room_bn`
    // on every missing item — so neither screen asks a second question to name a room.
    expect(Object.keys(counselingApi).filter((name) => /room/i.test(name))).toEqual([]);
    // And the valve is not here. Overriding needs `counseling.gate.override`, which a station
    // operator does not hold — a control wired to it would answer 403 with a patient waiting —
    // so there is no call in this file for one to be wired to, and no path anywhere in the
    // feature that could build the request.
    expect(Object.keys(counselingApi).filter((name) => /override/i.test(name))).toEqual([]);
    for (const file of readdirSync(featureDir)) {
      expect(/gate\/override/.test(featureCode(file)), `${file}: no override call`).toBe(false);
    }
    // Each write takes a session id and one body. Two arguments, never a list.
    expect(tickCounselingItem.length).toBe(2);
    expect(untickCounselingItem.length).toBe(2);
    expect(completeCounselingSession.length).toBe(2);
    expect(startCounselingSession.length).toBe(1);
  });

  it('refuses an item code that is secretly a list', () => {
    // The guard in `state.ts`. `item_code` is typed as a string by the contract, which is
    // exactly the shape a batch would arrive in — seven acts wearing one press's attribution.
    expect(oneItemCode('DIET,EXERCISE')).toBeNull();
    expect(oneItemCode('DIET EXERCISE')).toBeNull();
    expect(oneItemCode('DIET\nEXERCISE')).toBeNull();
    expect(oneItemCode('DIET;EXERCISE')).toBeNull();
    expect(oneItemCode('DIET|EXERCISE')).toBeNull();
    expect(oneItemCode('["DIET","EXERCISE"]')).toBeNull();

    // And a single code still passes, trimmed and upper-cased the way the server does it.
    expect(oneItemCode('  diet  ')).toBe('DIET');
    expect(oneItemCode('INSULIN_TECHNIQUE')).toBe('INSULIN_TECHNIQUE');
    expect(ITEM_CODE.test('DIET')).toBe(true);
    expect(ITEM_CODE.test('DIET,EXERCISE')).toBe(false);
  });

  it('builds a body with exactly one item code on it', () => {
    const body = toTick(session(), 'DIET', '', ids)!;
    expect(body).not.toBeNull();
    expect(typeof body.item_code).toBe('string');
    expect(Object.keys(body).sort()).toEqual(['event_id', 'item_code']);
    expect(Array.isArray(body.item_code)).toBe(false);
  });

  it('will not build a tick for two items even when asked in one string', () => {
    expect(toTick(session(), 'DIET,EXERCISE', '', ids)).toBeNull();
    expect(tickProblem(session(), 'DIET,EXERCISE', '')).toBe('badItemCode');
  });

  it('sends one request per item, each with its own event id and idempotency key', async () => {
    const calls = stubFetch(respond({ session: session() }));
    await tickCounselingItem('session-1', {
      event_id: 'event-a',
      item_code: 'DIET',
    });
    await tickCounselingItem('session-1', {
      event_id: 'event-b',
      item_code: 'EXERCISE',
    });
    expect(calls).toHaveLength(2);
    // A retry over a stuttering link must be one tick in the ledger, so the key is the body's
    // own event id — two people claiming the same item at two moments is the failure.
    expect(calls[0]?.idempotencyKey).toBe('event-a');
    expect(calls[1]?.idempotencyKey).toBe('event-b');
    expect(calls[0]?.idempotencyKey).not.toBe(calls[1]?.idempotencyKey);
  });
});

// --- criterion 2: seven taps and a completion ---

describe('a full seven-item session is seven taps and a completion', () => {
  it('costs eight taps for the seeded seven-item checklist', () => {
    // The whole of criterion 2 as a number, because none of what keeps it at eight is visible
    // in a screenshot a year from now: no per-item dialog, no navigation between rooms, an
    // optional note that is behind a tap and never blocks.
    expect(ITEMS).toHaveLength(7);
    expect(tapsToComplete(ITEMS)).toBe(8);
    expect(TAPS_PER_ITEM).toBe(1);
    expect(TAPS_TO_FINISH).toBe(1);
    expect(tapsToComplete([])).toBe(1);
    expect(tapsToComplete(ITEMS.slice(0, 3))).toBe(4);
  });

  it('has no confirmation step anywhere in the screen', () => {
    // A dialog on the honest act doubles its cost and teaches people to dismiss dialogs, which
    // is a habit they carry to the dialog that matters.
    const screen = featureCode('CounselingStation.tsx');
    expect(/\bAlert\b/.test(screen), 'no Alert').toBe(false);
    expect(/<Modal\b/.test(screen), 'no Modal').toBe(false);
    expect(/confirm/i.test(screen), 'nothing named confirm').toBe(false);
  });

  it('has no navigation between rooms, because the rooms are headings on one list', () => {
    const screen = featureCode('CounselingStation.tsx');
    expect(/expo-router|useRouter|navigate\(/.test(screen), 'no navigation').toBe(false);
    // One scroll, so the whole checklist is always one gesture away.
    expect(screen.includes('<ScrollView')).toBe(true);
  });

  it('never lets the optional note block the tick', () => {
    // A required note is a note people fill with a full stop.
    const open = session();
    expect(toTick(open, 'DIET', '', ids)).not.toBeNull();
    expect(toTick(open, 'DIET', '   ', ids)).not.toBeNull();
    expect(tickProblem(open, 'DIET', '')).toBeNull();
    // An empty note is omitted rather than written as an empty string: "wrote nothing" and
    // "nobody wrote" are different facts about a physician's note.
    expect(toTick(open, 'DIET', '  ', ids)).not.toHaveProperty('note');
    expect(toTick(open, 'DIET', ' injects in one spot ', ids)?.note).toBe('injects in one spot');
    // A tick needs no reason; only an un-tick does.
    expect(needsReason('tick')).toBe(false);
    expect(needsReason('untick')).toBe(true);
    expect(ACTS).toEqual(['tick', 'untick']);
  });

  it('refuses a note longer than the record will hold, and says so before the request', () => {
    expect(NOTE_MAX).toBe(500);
    const tooMuch = 'x'.repeat(NOTE_MAX + 1);
    expect(tickProblem(session(), 'DIET', tooMuch)).toBe('noteTooLong');
    expect(toTick(session(), 'DIET', tooMuch, ids)).toBeNull();
    expect(toTick(session(), 'DIET', 'x'.repeat(NOTE_MAX), ids)).not.toBeNull();
  });

  it('offers the note before the tick and not after it', () => {
    // The projection lands a re-tick on the same row, overwriting `ticked_by` and `ticked_at`.
    // Attaching an afterthought would rewrite the answer to §5.4's question — who told this
    // patient about injection sites — so the note is written with the tick or not at all.
    const rows = rowsOf(session({ ticks: [tick({ item_code: 'DIET' })] }), READER);
    const covered = rows.find((row) => row.code === 'DIET')!;
    const uncovered = rows.find((row) => row.code === 'EXERCISE')!;
    expect(noteOpenable(uncovered)).toBe(true);
    expect(noteOpenable(covered)).toBe(false);
    // And there is no call that edits a note on its own.
    expect(Object.keys(counselingApi).filter((name) => /note/i.test(name))).toEqual([]);
  });
});

// --- criterion 3: un-ticking costs a reason ---

describe('un-ticking requires a reason, and whitespace is not one', () => {
  it('refuses an empty reason', () => {
    expect(untickRefused('')).toBe(true);
    expect(untickProblem('')).toBe('needsReason');
    expect(toUntick(session(), 'DIET', '', ids)).toBeNull();
  });

  it('refuses a reason that is only whitespace', () => {
    // Three spaces satisfy a "not empty" check and answer no question at all six months later,
    // when somebody is trying to tell a correction from a mis-tap.
    for (const blank of ['   ', '\t', '\n', ' \n\t ']) {
      expect(untickRefused(blank), JSON.stringify(blank)).toBe(true);
      expect(toUntick(session(), 'DIET', blank, ids)).toBeNull();
    }
  });

  it('accepts a real reason, trimmed, and sends it with the item', () => {
    const body = toUntick(session(), 'DIET', '  Ticked on the wrong patient.  ', ids)!;
    expect(body).toEqual({
      event_id: 'event-1',
      item_code: 'DIET',
      reason: 'Ticked on the wrong patient.',
    });
    expect(untickRefused('Ticked on the wrong patient.')).toBe(false);
  });

  it('refuses a reason longer than the record will hold', () => {
    expect(REASON_MAX).toBe(500);
    expect(untickProblem('x'.repeat(REASON_MAX + 1))).toBe('reasonTooLong');
    expect(untickProblem('x'.repeat(REASON_MAX))).toBeNull();
  });

  it('sends the reason to the un-tick endpoint, guarded and keyed', async () => {
    const calls = stubFetch(respond({ session: session() }));
    await untickCounselingItem('session-1', {
      event_id: 'event-1',
      item_code: 'DIET',
      reason: 'Wrong patient.',
    });
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/counseling/sessions/session-1/unticks');
    expect(calls[0]?.requestedWith).toBe('DTHCMS');
    expect(calls[0]?.idempotencyKey).toBe('event-1');
    expect(JSON.parse(calls[0]!.body).reason).toBe('Wrong patient.');
  });

  it('is the only act on this screen that has a second step', () => {
    // The tick is one press; the un-tick is a reason and then a press. That asymmetry is the
    // design: the rarer and more consequential act is the one that costs more.
    expect(needsReason('untick')).toBe(true);
    expect(needsReason('tick')).toBe(false);
  });
});

// --- criterion 4: progress is two numbers ---

describe('progress is two numbers and never a percentage', () => {
  it('counts mandatory items only, as integers', () => {
    const open = session({
      ticks: [
        tick({ item_code: 'DISEASE_UNDERSTANDING' }),
        tick({ item_code: 'COMPLICATIONS' }),
        tick({ item_code: 'DIET' }),
        tick({ item_code: 'EXERCISE' }),
        tick({ item_code: 'SELF_CARE' }),
      ],
    });
    const progress = progressOf(open);
    expect(progress.covered).toBe(5);
    expect(progress.mandatory).toBe(7);
    expect(progress.outstanding).toBe(2);
    expect(Number.isInteger(progress.covered)).toBe(true);
    expect(Number.isInteger(progress.mandatory)).toBe(true);
  });

  it('produces no percentage, and there is none in the feature at all', () => {
    // A percentage rounds away the difference between finished and nearly finished, and on a
    // list whose last item is insulin technique that is the difference that matters.
    const progress = progressOf(session());
    for (const value of Object.values(progress)) {
      expect(typeof value === 'boolean' || Number.isInteger(value)).toBe(true);
    }
    for (const file of readdirSync(featureDir)) {
      const source = featureCode(file);
      expect(/percent|\*\s*100|\/\s*(total|mandatory)\b|toFixed/i.test(source), file).toBe(false);
    }
    // Nor a bar, which is a percentage drawn sideways.
    expect(
      /ProgressBar|progressBar|width:\s*`?\$\{/.test(featureCode('CounselingStation.tsx')),
    ).toBe(false);
  });

  it('takes the covered figure from the server’s outstanding list, not from the ticks', () => {
    // `core.counseling_outstanding` is what CP57's gate reads. A second count here is how a
    // phone shows a green tick while a gate refuses the patient standing in front of it, so
    // when the two disagree the server wins — visibly.
    const disagreeing = session({
      ticks: [tick({ item_code: 'DIET' }), tick({ item_code: 'EXERCISE' })],
      outstanding: ['GLUCOMETER'],
    });
    expect(progressOf(disagreeing).covered).toBe(6);
    expect(progressOf(disagreeing).outstanding).toBe(1);
  });

  it('draws optional items and does not count them as outstanding', () => {
    const withOptional = session({
      items: [...ITEMS, item('LEAFLET', 8, 'COUNSELING_ROOM', { mandatory: false })],
      ticks: [tick({ item_code: 'LEAFLET' })],
    });
    const progress = progressOf(withOptional);
    expect(progress.mandatory).toBe(7);
    expect(progress.optional).toBe(1);
    expect(progress.optionalCovered).toBe(1);
    // The optional item is drawn…
    expect(rowsOf(withOptional, READER).map((row) => row.code)).toContain('LEAFLET');
    // …and it is not in the figure the gate reads.
    expect(progress.covered).toBe(0);
    expect(progress.outstanding).toBe(7);
    expect(completionOf(withOptional, rowsOf(withOptional, READER)).missing).toHaveLength(7);
  });

  it('says it knows nothing rather than “0 of 0” for an index row', () => {
    // The visit index answers without items, so it cannot say how many are mandatory. Drawing
    // "0 of 0" from that would read as a checklist somebody finished, on a visit nobody has
    // counselled.
    const index = session({
      items: undefined,
      ticks: undefined,
      outstanding: ['DIET', 'INSULIN_TECHNIQUE'],
    });
    const progress = progressOf(index);
    expect(progress.known).toBe(false);
    expect(progress.mandatory).toBe(0);
    expect(progressOf(session()).known).toBe(true);
  });

  it('still knows what is outstanding on an index row, because the server fills it there', () => {
    // The half that *is* knowable without the items. The server runs the gate's own function
    // per row on the index, so an empty list means "nothing is missing" everywhere rather than
    // sometimes meaning "this row was not asked" — and `known` is a statement about the
    // denominator alone.
    const index = session({
      items: undefined,
      ticks: undefined,
      outstanding: ['DIET', 'INSULIN_TECHNIQUE'],
    });
    expect(progressOf(index).outstanding).toBe(2);
    expect(progressOf(session({ items: undefined, ticks: undefined, outstanding: [] }))).toEqual(
      expect.objectContaining({ known: false, outstanding: 0 }),
    );
    // And nothing in the feature coalesces the field away, which is what made the two cases
    // indistinguishable before it was required.
    for (const file of readdirSync(featureDir)) {
      expect(/outstanding\s*\?\?/.test(featureCode(file)), `${file}: no default`).toBe(false);
    }
  });

  it('never reports a negative count', () => {
    const impossible = session({ outstanding: ITEMS.map((one) => one.item_code).concat('GHOST') });
    expect(progressOf(impossible).covered).toBe(0);
  });

  it('reports completion from the timestamp, never from the arithmetic', () => {
    // A session that closed itself the moment the last item was ticked would take the
    // counsellor's judgement out of the one place §5.4 relies on it.
    const everything = session({ ticks: ITEMS.map((one) => tick({ item_code: one.item_code })) });
    expect(progressOf(everything).covered).toBe(7);
    expect(progressOf(everything).complete).toBe(false);
    expect(sessionOpen(everything)).toBe(true);

    const closed = session({ completed_at: '2026-09-04T05:30:00.000Z' });
    expect(progressOf(closed).complete).toBe(true);
    expect(sessionOpen(closed)).toBe(false);
  });
});

// --- criterion 5: finishing with items outstanding ---

describe('finishing with items outstanding is a record, not a failure', () => {
  it('is allowed with every mandatory item uncovered', () => {
    // The patient left, the interpreter did not arrive, the insulin corner was closed. A
    // screen that refused the press would leave the counsellor with a cheaper, dishonest way
    // out: ticking items they did not cover.
    const nothing = session();
    expect(finishRefused(nothing)).toBe(false);
    expect(completionOf(nothing, rowsOf(nothing, READER)).open).toBe(true);
    expect(toCompletion(nothing, ids)).toEqual({ event_id: 'event-1' });
  });

  it('is refused for exactly one reason, and it is not outstanding items', () => {
    const closed = session({ completed_at: '2026-09-04T05:30:00.000Z' });
    expect(finishRefused(closed)).toBe(true);
    expect(toCompletion(closed, ids)).toBeNull();
    // Every other session finishes, however little was covered.
    for (const covered of [0, 1, 3, 7]) {
      const partial = session({
        ticks: ITEMS.slice(0, covered).map((one) => tick({ item_code: one.item_code })),
      });
      expect(finishRefused(partial), `${covered} covered`).toBe(false);
    }
  });

  it('changes the button’s words when items are outstanding', () => {
    const partial = session({ ticks: [tick({ item_code: 'DIET' })] });
    const some = completionOf(partial, rowsOf(partial, READER));
    expect(some.label).toBe('finishWithOutstanding');
    expect(some.hint).toBe('finishOutstandingHint');

    const everything = session({ ticks: ITEMS.map((one) => tick({ item_code: one.item_code })) });
    const all = completionOf(everything, rowsOf(everything, READER));
    expect(all.label).toBe('finish');
    expect(all.hint).toBe('finishHint');
    expect(all.missing).toEqual([]);
  });

  it('names the missing mandatory items, in list order, before the press', () => {
    // A count alone would send the counsellor back up the list to work out which ones, which
    // is the moment they stop reading it.
    const partial = session({
      ticks: [
        tick({ item_code: 'DISEASE_UNDERSTANDING' }),
        tick({ item_code: 'DIET' }),
        tick({ item_code: 'GLUCOMETER' }),
      ],
    });
    const completion = completionOf(partial, rowsOf(partial, READER));
    expect(completion.missing.map((row) => row.code)).toEqual([
      'COMPLICATIONS',
      'EXERCISE',
      'SELF_CARE',
      'INSULIN_TECHNIQUE',
    ]);
  });

  it('has no failure tone to draw it in', () => {
    // The structural version of "it must never look like a failure state": the type itself has
    // no critical value, so no row and no banner on this screen can be red without a change in
    // `state.ts`, next to the sentence saying why there is not one.
    expect(TONES).toEqual(['quiet', 'normal', 'borderline']);
    expect(TONES).not.toContain('critical');
    const untouched = rowsOf(session(), READER);
    for (const row of untouched) expect(row.tone).toBe('quiet');
  });

  it('completes without ticking anything', () => {
    // A completion that covered the outstanding items would be the batch criterion 1 forbids,
    // wearing a different name — and it would let a session be closed by somebody who
    // counselled nobody.
    const body = toCompletion(session(), ids)!;
    expect(Object.keys(body)).toEqual(['event_id']);
    expect(body).not.toHaveProperty('item_code');
    expect(body).not.toHaveProperty('items');
  });

  it('does not pre-empt CP57’s gate', () => {
    // The gate is now read here (CP57) and it is still not decided here, which is the half of
    // this test that was always the point. Whether an incomplete session reaches the physician
    // is the server's answer: `gateStateOf` reads two booleans it sets, and nothing in this
    // feature turns a session, a tick or a progress figure into one of its own.
    const exported = [...Object.keys(counselingState), ...Object.keys(counselingApi)];
    expect(
      exported.filter((name) => /allowVisit|blockVisit|escalat|mayGoOn|canAdvance/i.test(name)),
      'nothing here decides the gate',
    ).toEqual([]);

    // `finishRefused` still has exactly one reason and it is not an outstanding item, so the
    // finish control cannot quietly become the gate's second implementation.
    expect(finishRefused(session({ outstanding: ITEMS.map((one) => one.item_code) }))).toBe(false);
  });
});

// --- criterion 6: who made the tick ---

describe('a tick says who made it, and never says it was you when it was not', () => {
  it('reads your own tick as yours', () => {
    const mine = attributionOf(ME, 'COUNSELOR', '2026-09-04T05:02:00.000Z', ME);
    expect(mine.mine).toBe(true);
    expect(attributionKey(mine)).toBe('byYou');
  });

  it('reads somebody else’s tick as theirs, with their role', () => {
    // Two counsellors and an insulin corner are involved in one session; "you did this" against
    // a colleague's work is the sentence this test exists to prevent.
    const theirs = attributionOf(SOMEBODY_ELSE, 'RX_EDUCATOR', '2026-09-04T05:40:00.000Z', ME);
    expect(theirs.mine).toBe(false);
    expect(theirs.role).toBe('RX_EDUCATOR');
    expect(attributionKey(theirs)).toBe('bySomebodyElse');
  });

  it('never reads as yours when this tablet does not know who you are', () => {
    // The session store has not answered yet. An unknown reader defaulting to "you" would put
    // somebody's name on an act at exactly the moment nobody can check it.
    expect(attributionOf(ME, 'COUNSELOR', '2026-09-04T05:02:00.000Z', '').mine).toBe(false);
    expect(attributionOf('', 'COUNSELOR', '2026-09-04T05:02:00.000Z', '').mine).toBe(false);
    expect(attributionOf('', '', '', ME).mine).toBe(false);
    expect(attributionKey(attributionOf(ME, '', '', ''))).toBe('bySomebodyElse');
  });

  it('carries the time each tick was made', () => {
    const rows = rowsOf(
      session({
        ticks: [tick({ item_code: 'DIET', ticked_by: SOMEBODY_ELSE, ticked_role: 'NUTRITIONIST' })],
      }),
      READER,
    );
    const diet = rows.find((row) => row.code === 'DIET')!;
    expect(diet.history?.ticked.at).toBe('2026-09-04T05:02:00.000Z');
    expect(diet.history?.ticked.mine).toBe(false);
    expect(diet.history?.ticked.role).toBe('NUTRITIONIST');
  });

  it('shows a clock time in the clinic’s own hours', () => {
    // 05:02 UTC is 11:02 in Faridpur. An ISO string on a row is read by nobody and copied
    // wrongly by somebody.
    expect(clockTime('2026-09-04T05:02:00.000Z')).toBe('11:02');
    expect(clockTime('2026-09-04T05:04:00.000Z')).toBe('11:04');
    // Across midnight, and with no daylight saving to get wrong.
    expect(clockTime('2026-09-04T19:30:00.000Z')).toBe('01:30');
    expect(clockTime('2026-01-04T05:02:00.000Z')).toBe('11:02');
    // Never "Invalid Date" on a clinical row: that would be read as data.
    expect(clockTime('')).toBe('');
    expect(clockTime('not a time')).toBe('');
  });

  it('keeps its fixed offset agreeing with the zone the rest of the app formats in', () => {
    /*
     * The app states the clinic's clock twice: `CLINIC_TIME_ZONE` (`Asia/Dhaka`) goes to
     * `use-intl` so every date a component formats is the clinic's rather than the tablet's,
     * and this number is what a renderer-free file can use instead. Two statements of one fact
     * drift silently — a tick would read an hour out and nothing would look broken — so they
     * are checked against each other here rather than trusted to stay in step.
     *
     * If this ever fails, the answer is not to change the number: it means Bangladesh has an
     * offset that moves, and a fixed one cannot express it any more.
     */
    const zone = readFileSync(join(featureDir, '..', '..', 'lib', 'i18n.tsx'), 'utf8');
    const named = /CLINIC_TIME_ZONE = '([^']+)'/.exec(zone)?.[1];
    expect(named, 'the app names one clinic zone').toBe('Asia/Dhaka');

    for (const winter of ['2026-01-04T05:02:00.000Z', '2026-07-04T05:02:00.000Z']) {
      const parts = new Intl.DateTimeFormat('en-GB', {
        timeZone: named,
        hour: '2-digit',
        minute: '2-digit',
        hour12: false,
      }).format(new Date(winter));
      expect(clockTime(winter), `${winter} in ${named}`).toBe(parts);
    }
    expect(CLINIC_UTC_OFFSET_MINUTES).toBe(6 * 60);
  });
});

// --- criterion 7: a withdrawn tick is history ---

describe('a withdrawn tick is visible history, not an absence', () => {
  const takenBack = tick({
    item_code: 'DIET',
    ticked_at: '2026-09-04T05:02:00.000Z',
    undone_at: '2026-09-04T05:04:00.000Z',
    undone_by: SOMEBODY_ELSE,
    undone_reason: 'Covered by the nutritionist instead.',
    undo_count: 1,
  });

  it('keeps both moments, both people and the reason', () => {
    const history = historyOf(takenBack, ME);
    expect(clockTime(history.ticked.at)).toBe('11:02');
    expect(clockTime(history.withdrawn!.at)).toBe('11:04');
    expect(history.ticked.mine).toBe(true);
    expect(history.withdrawn?.mine).toBe(false);
    expect(history.withdrawn?.reason).toBe('Covered by the nutritionist instead.');
    expect(history.live).toBe(false);
  });

  it('does not draw a withdrawn tick as an item nobody touched', () => {
    // The difference between a correction and a thing that never happened.
    const withdrawn = session({ ticks: [takenBack] });
    const rows = rowsOf(withdrawn, READER);
    const diet = rows.find((row) => row.code === 'DIET')!;
    const untouched = rows.find((row) => row.code === 'EXERCISE')!;

    expect(diet.covered).toBe(false);
    expect(untouched.covered).toBe(false);
    // …and they are not the same row on screen.
    expect(diet.history).not.toBeNull();
    expect(untouched.history).toBeNull();
    expect(diet.tone).toBe('borderline');
    expect(untouched.tone).toBe('quiet');
    expect(toneFor(false, historyOf(takenBack, ME))).toBe('borderline');
    expect(toneFor(true, historyOf(tick({ item_code: 'DIET' }), ME))).toBe('normal');
    expect(toneFor(false, null)).toBe('quiet');
  });

  it('remembers how often an item has gone round, across re-ticks', () => {
    // "Ticked and un-ticked three times" is exactly what a quality review is looking for.
    const reticked = tick({ item_code: 'DIET', undo_count: 3 });
    const history = historyOf(reticked, ME);
    expect(history.undoCount).toBe(3);
    expect(history.live).toBe(true);
    expect(history.withdrawn).toBeNull();
  });

  it('shows the live tick when a row somehow carries two', () => {
    // Re-ticking lands on the same row, so in practice there is one. Written to prefer the
    // live one anyway: showing the withdrawn one would tell a counsellor an item is uncovered
    // when the server says it is covered.
    const both = [
      tick({ item_code: 'DIET', undone_at: '2026-09-04T05:04:00.000Z', undone_reason: 'no' }),
      tick({ item_code: 'DIET', ticked_at: '2026-09-04T05:10:00.000Z' }),
    ];
    expect(tickIsLive(tickFor(both, 'DIET'))).toBe(true);
    expect(tickFor(both, 'GLUCOMETER')).toBeNull();
    expect(tickIsLive(null)).toBe(false);
  });

  it('keeps the note off a row whose tick was taken back', () => {
    // The note belonged to a tick that no longer stands; showing it beside an uncovered item
    // would read as advice somebody currently gives.
    const withdrawnWithNote = session({
      ticks: [{ ...takenBack, note: 'Injects into one spot.' }],
    });
    const diet = rowsOf(withdrawnWithNote, READER).find((row) => row.code === 'DIET')!;
    expect(diet.note).toBe('');
    expect(diet.history?.withdrawn).not.toBeNull();
  });
});

// --- criterion 8: the list is frozen ---

describe('the list is frozen and nothing merges a fresher one in', () => {
  it('draws the items the session returned, in the order it returned them', () => {
    const rows = rowsOf(session(), READER);
    expect(rows.map((row) => row.code)).toEqual(ITEMS.map((one) => one.item_code));
  });

  it('follows the array the server sent, not the `ordering` field on the rows', () => {
    // Two orderings that could disagree, and only one of them is obeyed. Re-sorting by
    // `ordering` here would mean a screen and a server walking the same checklist in two
    // different orders the first time somebody edits a draft's numbering by hand.
    const scrambled = session({
      items: [
        item('GLUCOMETER', 99, 'COUNSELING_ROOM'),
        item('DIET', 1, 'NUTRITION_ROOM'),
        item('EXERCISE', 50, 'COUNSELING_ROOM'),
      ],
    });
    expect(rowsOf(scrambled, READER).map((row) => row.code)).toEqual([
      'GLUCOMETER',
      'DIET',
      'EXERCISE',
    ]);
    // And there is no comparator in the file at all, so nothing can quietly grow one.
    expect(/\.sort\(/.test(featureCode('state.ts')), 'no comparator in state.ts').toBe(false);
  });

  it('takes no template, no version and no catalogue', () => {
    // The absence is the criterion. There is no argument through which a freshly-fetched list
    // could arrive, so a checklist republished mid-session cannot change what this patient was
    // asked about.
    expect(rowsOf.length).toBe(2);
    for (const file of readdirSync(featureDir)) {
      expect(/counseling\/templates/.test(featureCode(file)), `${file} fetches no template`).toBe(
        false,
      );
    }
  });

  it('will not tick an item the frozen list does not contain', () => {
    const open = session();
    expect(onTheList(open, 'DIET')).toBe(true);
    expect(onTheList(open, 'FOOT_CARE')).toBe(false);
    expect(tickProblem(open, 'FOOT_CARE', '')).toBe('notOnThisChecklist');
    expect(toTick(open, 'FOOT_CARE', '', ids)).toBeNull();
    expect(toUntick(open, 'FOOT_CARE', 'wrong item', ids)).toBeNull();
  });

  it('upper-cases a code the way the server does before checking the frozen list', () => {
    // A check that disagreed with what actually goes on the wire would refuse a request the
    // server would have accepted, or — worse the other way round — pass one it will not.
    const open = session();
    expect(toTick(open, ' diet ', '', ids)?.item_code).toBe('DIET');
    expect(toUntick(open, 'diet', 'wrong patient', ids)?.item_code).toBe('DIET');
    expect(onTheList(open, 'DIET')).toBe(true);
  });

  it('answers a 422 on item_code with “reload the checklist”', () => {
    // The refusal criterion 8 predicts, arriving where the design said it would: this phone is
    // holding a list from before a republish, and the only thing that fixes it is reading the
    // session again.
    const stale: Trouble = {
      kind: 'refused',
      attempt: 'tick',
      status: 422,
      code: 'VALIDATION_FAILED',
      field: 'item_code',
      message: 'That item is not on this checklist. Reload it.',
    };
    expect(adviceFor(stale)).toBe('reload');
    // And the sentence says so, in both languages.
    expect(lookup('en', 'counseling.advice.reload')).toMatch(/again/i);
    expect(String(lookup('bn', 'counseling.advice.reload'))).toContain('আবার');
  });

  it('refuses every write against a session somebody closed', () => {
    const closed = session({ completed_at: '2026-09-04T05:30:00.000Z' });
    expect(tickProblem(closed, 'DIET', '')).toBe('sessionClosed');
    expect(toTick(closed, 'DIET', '', ids)).toBeNull();
    expect(toUntick(closed, 'DIET', 'wrong patient', ids)).toBeNull();
    expect(toCompletion(closed, ids)).toBeNull();
  });
});

// --- which checklists this visit calls for ---

describe('the server says which checklists this visit calls for, and this screen offers those', () => {
  it('resumes the open session rather than offering to start a second walk', () => {
    // The failure this prevents: two half-ticked copies of one list, each counsellor believing
    // the other covered the rest. The server names the open session for exactly this reason.
    const open = choicesOf([checklist({ session_id: 'session-1' })], [], 'en');
    expect(open[0]?.started).toBe(true);
    expect(open[0]?.startable).toBe(false);
    expect(startableOf(open)).toEqual([]);
    expect(resumeOf(open)).toBe('session-1');
  });

  it('offers starting a checklist that is called for and not started, and names the condition', () => {
    // "Why am I being asked to do this" gets an answer where the question is asked, rather
    // than in a policy document — and a checklist that arrived from a mis-coded condition is
    // caught by the person holding the phone or by nobody.
    const fresh = choicesOf([checklist()], [], 'en');
    expect(fresh[0]?.startable).toBe(true);
    expect(fresh[0]?.sessionId).toBe('');
    expect(fresh[0]?.matched).toBe('ICD10 E11.9');
    expect(startableOf(fresh).map((one) => one.templateId)).toEqual(['template-1']);
    // Nothing to resume: a session id is not invented for a session that does not exist.
    expect(resumeOf(fresh)).toBe('');
    // And the body that opens it is the server's own template id, never a guess.
    expect(toStart('patient-1', 'visit-1', fresh[0]!.templateId, ids)?.template_id).toBe(
      'template-1',
    );
  });

  it('says the coding whole, or says nothing', () => {
    // A bare `E11.9` is not a coding (CP52), and on a screen that will one day show the
    // clinic's own dictionary beside ICD-10 it is the sort of thing somebody reads as the
    // wrong disease.
    expect(choicesOf([checklist({ matched_system: '' })], [], 'en')[0]?.matched).toBe('E11.9');
    expect(choicesOf([checklist({ matched_code: '' })], [], 'en')[0]?.matched).toBe('');
    // A session the server could no longer match to a rule still appears — a rule retired at
    // lunchtime must not make a half-ticked session vanish — and claims no condition.
    const retired = choicesOf(
      [{ ...checklist(), matched_code: '', session_id: 'session-1' }],
      [],
      'en',
    );
    expect(retired[0]?.matched).toBe('');
    expect(retired[0]?.started).toBe(true);
  });

  it('keeps every checklist visible when the visit calls for more than one', () => {
    // A patient with type 2 diabetes and hypothyroidism gets one per matching rule, and both
    // are walked in the same three rooms by the same people. Hiding the second behind the one
    // that happens to be open is how it never gets walked.
    const both = choicesOf(
      [
        checklist({ session_id: 'session-1' }),
        checklist({
          template_id: 'template-2',
          template_code: 'HYPOTHYROID',
          title_en: 'Thyroid counselling',
          title_bn: 'থাইরয়েড কাউন্সেলিং',
          matched_code: 'E03.9',
          priority: 50,
        }),
      ],
      [],
      'en',
    );
    expect(both).toHaveLength(2);
    expect(both.map((one) => one.title)).toEqual(['Diabetes counselling', 'Thyroid counselling']);
    expect(startableOf(both).map((one) => one.title)).toEqual(['Thyroid counselling']);
    // The server's order — open first, then the clinic's own priority — and no comparator here.
    expect(resumeOf(both)).toBe('session-1');
  });

  it('opens on the one still being walked, not on the one finished this morning', () => {
    const morning = choicesOf(
      [
        checklist({ session_id: 'session-1', complete: true }),
        checklist({ template_id: 'template-2', session_id: 'session-2' }),
      ],
      [],
      'en',
    );
    expect(morning[0]?.finished).toBe(true);
    expect(resumeOf(morning)).toBe('session-2');
    // Everything finished: a reader lands on the last one started, and every other is one tap
    // away in the row above.
    const done = choicesOf(
      [
        checklist({ session_id: 'session-1', complete: true }),
        checklist({ template_id: 'template-2', session_id: 'session-2', complete: true }),
      ],
      [],
      'en',
    );
    expect(resumeOf(done)).toBe('session-2');
    expect(resumeOf([])).toBe('');
  });

  it('shows a hat that may only read what the visit should have been walked through', () => {
    // The checklist answer reads with `counseling.session.read` now, so a reviewer or a
    // physician's panel gets the same row a counsellor does — the checklist this visit called
    // for, the coded condition that called for it, and that nobody opened it. It is the half a
    // gate refusal turns on, and it used to be a 403 and an empty screen.
    const reading = choicesOf([checklist()], [], 'en');
    expect(reading).toHaveLength(1);
    expect(reading[0]?.title).toBe('Diabetes counselling');
    expect(reading[0]?.matched).toBe('ICD10 E11.9');
    expect(reading[0]?.started).toBe(false);
    // The row is in `startableOf` for everybody. Only the control is behind the permission —
    // a reviewer shown nothing here would read this as a visit that called for no counselling.
    expect(startableOf(reading).map((choice) => choice.templateId)).toEqual(['template-1']);
    expect(mayTick(['counseling.session.read'])).toBe(false);
    // And there is no session to open, so nothing is resumed and no id is invented for one.
    expect(resumeOf(reading)).toBe('');
  });

  it('adds a session the checklist answer did not name, and never a second copy of one', () => {
    // The checklist answer carries one row per checklist, so a list walked, finished and opened
    // again names only the later session. The index names every session there is, and the merge
    // is what puts the earlier walk back on the screen.
    const merged = choicesOf([checklist({ session_id: 'session-2' })], [session()], 'en');
    expect(merged.map((choice) => choice.sessionId)).toEqual(['session-2', 'session-1']);
    expect(merged[0]?.matched).toBe('ICD10 E11.9');
    // The index says nothing about which rule matched, and nothing is invented for it.
    expect(merged[1]?.matched).toBe('');

    // Where both answers name the same session it appears once. Two chips for one session would
    // be two ways into the same list, one of which somebody would treat as a second checklist.
    const same = choicesOf([checklist({ session_id: 'session-1' })], [session()], 'en');
    expect(same).toHaveLength(1);
    expect(same[0]?.matched).toBe('ICD10 E11.9');
  });

  it('reads “finished” from the field rather than from its presence', () => {
    // `complete` is required by the contract and always sent. While it was optional, a missing
    // value and a false one were the same absence and the phone had to guess which — so an
    // unfinished checklist could read as finished on a build that guessed the other way.
    expect(choicesOf([checklist({ session_id: 'session-1' })], [], 'en')[0]?.finished).toBe(false);
    expect(
      choicesOf([checklist({ session_id: 'session-1', complete: true })], [], 'en')[0]?.finished,
    ).toBe(true);
    for (const file of readdirSync(featureDir)) {
      expect(/complete\s*===/.test(featureCode(file)), `${file}: no presence check`).toBe(false);
    }
  });

  it('names a checklist the same way wherever it is drawn', () => {
    // One implementation behind both, so a chip row cannot say "DIABETES" where the heading
    // says "Diabetes counselling" and read as two different lists.
    expect(checklistTitle(checklist(), 'bn')).toBe('ডায়াবেটিস কাউন্সেলিং');
    expect(checklistTitle(checklist({ title_bn: '' }), 'bn')).toBe('Diabetes counselling');
    expect(checklistTitle(checklist({ title_en: '', title_bn: '' }), 'en')).toBe('DIABETES');
    expect(checklistTitle(checklist(), 'en')).toBe(sessionTitle(session(), 'en'));
  });

  it('draws a checklist nobody opened even to somebody who cannot open it', () => {
    const screen = featureCode('CounselingStation.tsx');
    // The list is drawn for everybody. Putting it behind the permission was right while the
    // checklist answer was a 403 for a reader; now it would be the screen hiding the one thing
    // that explains why a patient was held.
    expect(screen).toContain('startableOf(choices).map(');
    expect(/allowed\s*\?\s*startableOf/.test(screen), 'the list is not gated').toBe(false);
    // What the reader gets is a statement, not a greyed-out button: a control that looks
    // disabled invites the press that produces the 403.
    expect(screen).toContain('counseling-unwalked-');
    const unwalked = screen.slice(screen.indexOf('function UnwalkedChecklist'));
    expect(unwalked.slice(0, unwalked.indexOf('\n}\n'))).not.toContain('onPress');
    // And the sentence that says whose writing it is reaches that screen too, so a reviewer
    // does not read a row with no control as a screen that failed to load one.
    expect(screen.split('MayNotTick').length - 1, 'said on both branches').toBeGreaterThan(2);
  });

  it('has no picker, no search and no catalogue of every checklist in the clinic', () => {
    // Assignment is a rule keyed on a coding (§5.1). A counsellor choosing from a list would be
    // making that clinical decision by hand, which is the one thing the rules exist to stop.
    for (const file of readdirSync(featureDir)) {
      const source = featureCode(file);
      expect(/listCounselingTemplates|searchTemplates/.test(source), file).toBe(false);
      expect(/counseling\/templates/.test(source), file).toBe(false);
    }
    // The only start this feature can build is for one named template id.
    expect(toStart('patient-1', 'visit-1', '', ids)).toBeNull();
  });

  it('costs one press to open, once, and never one per item', () => {
    expect(TAPS_TO_START).toBe(1);
    // Still eight for the walk itself: opening is a separate promise from criterion 2's, and
    // it happens once per checklist for whichever room reaches the patient first.
    expect(tapsToComplete(ITEMS)).toBe(8);
  });
});

// --- the rooms are headings, not screens ---

describe('the rooms are headings on one list', () => {
  it('groups the seeded checklist into its three rooms without losing an item', () => {
    const groups = roomsOf(rowsOf(session(), READER), 'en');
    expect(groups.map((group) => group.room)).toEqual([
      'COUNSELING_ROOM',
      'NUTRITION_ROOM',
      'INSULIN_CORNER',
    ]);
    expect(
      groups
        .flatMap((group) => group.rows)
        .map((row) => row.code)
        .sort(),
    ).toEqual(ITEMS.map((one) => one.item_code).sort());
  });

  it('handles the seeded interleaving, where a nutrition item sits between two counselling ones', () => {
    // Migration 00037 puts DIET third. A grouping written to assume rooms arrive contiguously
    // would drop it or open a second nutrition heading.
    const groups = roomsOf(rowsOf(session(), READER), 'en');
    expect(groups).toHaveLength(3);
    expect(groups[0]?.rows.map((row) => row.code)).toEqual([
      'DISEASE_UNDERSTANDING',
      'COMPLICATIONS',
      'EXERCISE',
      'SELF_CARE',
      'GLUCOMETER',
    ]);
    expect(groups[1]?.rows.map((row) => row.code)).toEqual(['DIET']);
  });

  it('marks the operator’s own room from the item, with no second fetch', () => {
    // `room_station` arrives with the session. It used to mean a request for the room
    // catalogue, which bought this one boolean and left the marking missing for as long as a
    // second round trip took on a link that drops for seconds at a time.
    const rows = rowsOf(session(), READER);
    const nutrition = roomsOf(rows, 'en', 'STN_NUTRITION');
    expect(nutrition.find((group) => group.room === 'NUTRITION_ROOM')?.yours).toBe(true);
    expect(nutrition.find((group) => group.room === 'COUNSELING_ROOM')?.yours).toBe(false);
    // Everything is still drawn: the nutritionist seeing that diet was already covered is what
    // stops them covering it again.
    expect(nutrition.flatMap((group) => group.rows)).toHaveLength(7);

    // The insulin corner belongs to the counselling station rather than being one of its own.
    const counselling = roomsOf(rows, 'en', 'STN_COUNSELING');
    expect(counselling.find((group) => group.room === 'INSULIN_CORNER')?.yours).toBe(true);
    expect(counselling.find((group) => group.room === 'NUTRITION_ROOM')?.yours).toBe(false);
  });

  it('marks nothing and hides nothing when the item names no station', () => {
    const stationless = session({
      items: ITEMS.map((one) => ({ ...one, room_station: undefined })),
    });
    const groups = roomsOf(rowsOf(stationless, READER), 'en', 'STN_NUTRITION');
    expect(groups.every((group) => !group.yours)).toBe(true);
    expect(groups.flatMap((group) => group.rows)).toHaveLength(7);
    // The heading still reads, from the session's own copy of the room name.
    expect(groups[1]?.heading.text).toBe('Nutrition room');
    // And an operator standing at no station marks nothing either.
    expect(roomsOf(rowsOf(session(), READER), 'en').every((group) => !group.yours)).toBe(true);
    expect(roomsOf(rowsOf(session(), READER), 'en', '  ').every((group) => !group.yours)).toBe(
      true,
    );
  });

  it('reads the room heading in the reader’s language', () => {
    const bengaliRows = rowsOf(session(), { locale: 'bn', me: ME });
    const groups = roomsOf(bengaliRows, 'bn');
    expect(groups[0]?.heading.text).toBe('কাউন্সেলিং রুম');
    expect(groups[2]?.heading.text).toBe('ইনসুলিন কর্নার');
  });

  it('falls back to the bare room code rather than an empty heading', () => {
    // A blank heading would silently merge two rooms into one list.
    const bare = session({
      items: [item('DIET', 1, 'NUTRITION_ROOM', { room_en: '', room_bn: '' })],
    });
    const groups = roomsOf(rowsOf(bare, READER), 'en');
    expect(groups[0]?.heading.text).toBe('NUTRITION_ROOM');
  });
});

// --- the words on an item ---

describe('an item is read in the counsellor’s own language, and says when it is not', () => {
  it('shows the reader’s language when the item carries it', () => {
    expect(wordingOf('Food habits', 'খাদ্যাভ্যাস', 'bn')).toEqual({
      text: 'খাদ্যাভ্যাস',
      language: 'bn',
      ownLanguage: true,
    });
    expect(wordingOf('Food habits', 'খাদ্যাভ্যাস', 'en').text).toBe('Food habits');
  });

  it('falls back to the other language and says so rather than switching silently', () => {
    // Publishing already refuses a version missing either language, so this is an edge case —
    // but a Bangla reader handed English with no explanation is one who thinks the app switched
    // languages, and who may read an injection-technique instruction aloud rather than admit
    // they cannot read it.
    const fallen = wordingOf('Insulin injection sites', '', 'bn');
    expect(fallen.text).toBe('Insulin injection sites');
    expect(fallen.language).toBe('en');
    expect(fallen.ownLanguage).toBe(false);
  });

  it('says nothing at all rather than a blank line where the question belongs', () => {
    const nothing = wordingOf('  ', '', 'en');
    expect(nothing.text).toBe('');
    expect(nothing.language).toBeNull();
    // And the sentence for it tells the counsellor not to guess.
    expect(String(lookup('en', 'counseling.noText'))).toMatch(/guess/i);
  });

  it('treats absent guidance as ordinary and absent text as not', () => {
    const rows = rowsOf(
      session({ items: [item('DIET', 1, 'NUTRITION_ROOM', { guidance_en: '', guidance_bn: '' })] }),
      READER,
    );
    expect(rows[0]?.guidance.text).toBe('');
    expect(rows[0]?.text.text).toBe('DIET in English');
  });

  it('reads the checklist’s own title in the reader’s language, and its code before nothing', () => {
    expect(sessionTitle(session(), 'en')).toBe('Diabetes counselling');
    expect(sessionTitle(session(), 'bn')).toBe('ডায়াবেটিস কাউন্সেলিং');
    expect(sessionTitle(session({ title_en: '', title_bn: '' }), 'en')).toBe('DIABETES');
  });
});

// --- the calls ---

describe('the counselling calls', () => {
  it('reads one session with its frozen list, its ticks and what is outstanding', async () => {
    const calls = stubFetch(respond({ session: session() }));
    const read = await getCounselingSession('session-1');
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/counseling/sessions/session-1');
    expect(read.items).toHaveLength(7);
    expect(read.outstanding).toHaveLength(7);
  });

  it('reads what a visit has been walked through', async () => {
    const calls = stubFetch(respond({ sessions: [session()] }));
    expect(await listCounselingSessionsForVisit('visit-1')).toHaveLength(1);
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/counseling/visits/visit-1/sessions');
  });

  it('asks which checklists the visit calls for, and never which exist', async () => {
    // The one question that makes starting possible without a template picker. A picker would
    // be a counsellor making an assignment keyed on a coded condition (§5.1) by hand.
    const calls = stubFetch(respond({ checklists: [checklist()] }));
    const answer = await listCounselingChecklistsForVisit('visit-1');
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/counseling/visits/visit-1/checklists');
    expect(answer[0]?.matched_code).toBe('E11.9');
    // And nothing in this feature asks for the catalogue of every checklist in the clinic.
    for (const file of readdirSync(featureDir)) {
      expect(/counseling\/(assignments|templates)/.test(featureCode(file)), file).toBe(false);
    }
  });

  it('opens a session against the version published now', async () => {
    const calls = stubFetch(respond({ session: session() }, { status: 201 }));
    const body = toStart('patient-1', 'visit-1', 'template-1', ids)!;
    await startCounselingSession(body);
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/counseling/sessions');
    expect(calls[0]?.idempotencyKey).toBe('event-1');
    expect(JSON.parse(calls[0]!.body)).toEqual({
      event_id: 'event-1',
      patient_id: 'patient-1',
      visit_id: 'visit-1',
      template_id: 'template-1',
    });
    // Never assembled from blanks: a start missing any of the three is not a request.
    expect(toStart('', 'visit-1', 'template-1', ids)).toBeNull();
    expect(toStart('patient-1', '  ', 'template-1', ids)).toBeNull();
  });

  it('ticks one item, guarded and keyed by its own event', async () => {
    const calls = stubFetch(
      respond({ session: session({ ticks: [tick({ item_code: 'DIET' })] }) }),
    );
    const after = await tickCounselingItem('session-1', toTick(session(), 'DIET', '', ids)!);
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/counseling/sessions/session-1/ticks');
    expect(calls[0]?.method).toBe('POST');
    expect(calls[0]?.requestedWith).toBe('DTHCMS');
    // The whole session comes back, because a tick changes what the gate reads.
    expect(after.outstanding).toHaveLength(6);
  });

  it('finishes a session with nothing but an event id', async () => {
    const calls = stubFetch(
      respond({ session: session({ completed_at: '2026-09-04T05:30:00Z' }) }),
    );
    const after = await completeCounselingSession('session-1', toCompletion(session(), ids)!);
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/counseling/sessions/session-1/complete');
    expect(JSON.parse(calls[0]!.body)).toEqual({ event_id: 'event-1' });
    expect(sessionOpen(after)).toBe(false);
  });

  it('logs nothing at all', () => {
    // A counselling refusal names an item code and sometimes a counsellor's own words about a
    // patient. Neither belongs in a log line.
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

// --- what went wrong, and what to do about it ---

/** A refusal as the server sends one, with the parts a test varies spelled out. */
const refusal = (over: {
  status: number;
  /** The server's own code. It is the only part a client may branch on, so it is what varies. */
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

/** A trouble as the screen holds one, with only the parts a test cares about spelled out. */
const trouble = (over: Partial<Trouble> & { status: number }): Trouble => ({
  kind: 'refused',
  attempt: 'tick',
  code: '',
  field: '',
  message: '',
  ...over,
});

describe('what went wrong, in the three ways it can', () => {
  it('reports a request that never left the tablet as unreachable', async () => {
    // The clinic's link drops for seconds at a time (ADR-0004). Nothing was recorded, and
    // pressing again once the connection is back is the whole of the answer.
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Network request failed')));
    const error = await getCounselingSession('session-1').catch((e: unknown) => e);
    const lost = troubleOf(error, 'en', 'tick');
    expect(lost).toEqual({
      kind: 'unreachable',
      attempt: 'tick',
      status: 0,
      // Nothing answered, so there is no code to carry and none is invented.
      code: '',
      field: '',
      message: '',
    });
    expect(adviceFor(lost)).toBe('retry');
    expect(troubleKey(lost)).toBe('trouble.unreachable');
  });

  it('reports something that is not an error this app throws without inventing words', () => {
    const odd = troubleOf(new (class extends Error {})(), 'en', 'finish');
    expect(odd.kind).toBe('failed');
    expect(odd.attempt).toBe('finish');
    // No message is better than one this app made up about a server it did not hear from.
    expect(odd.message).toBe('');
  });

  it('reports the server’s own words on a refusal, in the reader’s language', () => {
    const error = refusal({
      status: 422,
      fields: { reason: 'Say why the tick is being taken back.' },
      fieldsBN: { reason: 'টিক কেন তুলে নেওয়া হচ্ছে তা লিখুন।' },
    });
    const english = troubleOf(error, 'en', 'untick');
    expect(english.kind).toBe('refused');
    expect(english.status).toBe(422);
    expect(english.field).toBe('reason');
    expect(english.message).toBe('Say why the tick is being taken back.');
    expect(troubleOf(error, 'bn', 'untick').message).toBe('টিক কেন তুলে নেওয়া হচ্ছে তা লিখুন।');
  });

  it('leads with item_code when a refusal names more than one field', () => {
    // It is the field that changes what the person does next: the phone is holding a list from
    // before a republish, and the answer is to reload rather than to retype.
    const error = refusal({
      status: 422,
      fields: { note: 'Too long.', item_code: 'That item is not on this checklist. Reload it.' },
    });
    expect(troubleOf(error, 'en', 'tick').field).toBe('item_code');
    expect(adviceFor(troubleOf(error, 'en', 'tick'))).toBe('reload');
  });

  it('shows a field this build has never heard of rather than swallowing it', () => {
    // A server ahead of this tablet names something `FIELD_ORDER` does not list. Reporting
    // nothing would leave a counsellor with a refusal and no clue; reporting it by name means
    // a support call can quote it. Sorted, so two operators do not read different sentences.
    const unknown = refusal({
      status: 422,
      fields: { zzz_future: 'Not yet known here.', aaa_future: 'Nor this.' },
    });
    expect(troubleOf(unknown, 'en', 'tick').field).toBe('aaa_future');
    expect(troubleOf(unknown, 'en', 'tick').message).toBe('Nor this.');

    // A refusal that names no field at all falls back to the server's own sentence.
    const wordless = refusal({ status: 422, messageEN: 'That request cannot be answered.' });
    expect(troubleOf(wordless, 'en', 'tick').field).toBe('');
    expect(troubleOf(wordless, 'en', 'tick').message).toBe('That request cannot be answered.');
  });

  it('gives exactly three answers and offers no button where none would help', () => {
    expect(ADVICE).toEqual(['reload', 'retry', 'none']);
    // A hat that does not hold `counseling.tick`. Pressing again cannot change that.
    expect(adviceFor(trouble({ status: 403 }))).toBe('none');
    // A colleague got there first, a session somebody closed, or an item that is not ticked.
    expect(adviceFor(trouble({ status: 409 }))).toBe('reload');
    expect(adviceFor(trouble({ status: 404 }))).toBe('reload');
    // A reason that is empty or too long: the server has already said which field.
    expect(adviceFor(trouble({ status: 422, attempt: 'untick', field: 'reason' }))).toBe('none');
    // Anything else is the server having a bad moment.
    expect(adviceFor(trouble({ kind: 'failed', status: 500 }))).toBe('retry');
  });

  it('classes 403, 404 and 409 as refusals rather than as the server falling over', () => {
    for (const status of [403, 404, 409]) {
      const error = refusal({ status });
      expect(troubleOf(error, 'en', 'tick').kind, String(status)).toBe('refused');
      expect(troubleOf(error, 'en', 'tick').status).toBe(status);
    }
    expect(troubleOf(refusal({ status: 500 }), 'en', 'tick').kind).toBe('failed');
  });

  it('carries the server’s code through untouched, on every shape of refusal', () => {
    // A code is the only part of an error a client may branch on. A message is written for a
    // person: it gets translated, shortened and improved, and a screen matching on its words
    // would change behaviour the day somebody rewrote a sentence.
    for (const status of [403, 404, 409, 422, 500]) {
      const error = refusal({ status, code: 'COUNSELING_SESSION_FINISHED' });
      expect(troubleOf(error, 'en', 'tick').code, String(status)).toBe(
        'COUNSELING_SESSION_FINISHED',
      );
    }
    // And nothing is invented where the server did not answer at all.
    expect(troubleOf(new (class extends Error {})(), 'en', 'tick').code).toBe('');
  });

  it('says before the first tap that a hat which cannot tick cannot tick', () => {
    // A courtesy, never a control: the server decides, and this only chooses a sentence.
    expect(PERM_TICK).toBe('counseling.tick');
    expect(mayTick(['counseling.session.read'])).toBe(false);
    expect(mayTick(['counseling.session.read', 'counseling.tick'])).toBe(true);
    expect(mayTick([])).toBe(false);
    // And nothing in this feature enforces it: no export decides who may write.
    expect(/throw new/.test(featureCode('state.ts')), 'state.ts throws nothing').toBe(false);
  });

  it('does not say “you may read but not tick” to anybody who counsels', () => {
    // Migration 00038 gives `counseling.tick` to the nutritionist, the exercise instructor and
    // the prescription educator as well as the counsellor — §5.2 walks three rooms, and only
    // the counsellor holding it would have meant every other room's items ticked by proxy.
    // The sentence survives for the hats that genuinely read without writing, so it must not
    // send anybody to a role they are already wearing.
    for (const room of ['NUTRITIONIST', 'EXERCISE', 'RX_EDUCATOR', 'COUNSELOR']) {
      expect(mayTick([PERM_TICK]), room).toBe(true);
    }
    for (const language of ['en', 'bn'] as const) {
      const sentence = String(lookup(language, 'counseling.mayNotTick'));
      expect(sentence, `${language} sends nobody to another role`).not.toMatch(
        /switch to|ভূমিকায় যান/i,
      );
      expect(sentence.length, `${language} says what it is instead`).toBeGreaterThan(40);
    }
  });
});

// --- a tick somebody else already made ---

describe('a tick refused because the item is covered is not the counsellor’s mistake', () => {
  it('reads the server’s own code, and never the act that met the conflict', () => {
    // The server refuses rather than re-attributing, because overwriting `ticked_by` on a
    // mis-tap is the one way this record loses the answer §5.4 exists to ask for. The person
    // holding the phone did nothing wrong, and the sentence has to say so.
    expect(CODE_ALREADY_COVERED).toBe('COUNSELING_ITEM_ALREADY_COVERED');
    const beaten = troubleOf(
      refusal({
        status: 409,
        code: CODE_ALREADY_COVERED,
        messageEN: 'Somebody has already covered that item. Open the session again to see who.',
      }),
      'en',
      'tick',
    );
    expect(alreadyCovered(beaten)).toBe(true);
    expect(troubleKey(beaten)).toBe('trouble.alreadyCovered');
    // Reload, because the phone's copy is what is behind — and reloading is what shows whose
    // tick it is.
    expect(adviceFor(beaten)).toBe('reload');
  });

  it('does not read the other conflicts that way, whichever act met them', () => {
    // Three other things arrive as 409, and until each carried its own code the only thing
    // separating them here was which request met one — which put "somebody has already covered
    // this item" in front of a counsellor whose session had simply been closed.
    for (const code of [
      'COUNSELING_SESSION_FINISHED',
      'COUNSELING_ITEM_NOT_TICKED',
      'IDEMPOTENCY_KEY_REUSED',
    ]) {
      for (const attempt of ATTEMPTS) {
        const other = troubleOf(refusal({ status: 409, code }), 'en', attempt);
        expect(alreadyCovered(other), `${code} on ${attempt}`).toBe(false);
        expect(troubleKey(other), `${code} on ${attempt}`).toBe('trouble.refused');
        // Still the same answer for all of them: this phone's copy is behind the record.
        expect(adviceFor(other), `${code} on ${attempt}`).toBe('reload');
      }
    }
    // A tick refused for any other reason is not it either, whatever the status.
    for (const status of [403, 404, 422, 500]) {
      const other = troubleOf(refusal({ status, code: 'FORBIDDEN' }), 'en', 'tick');
      expect(alreadyCovered(other), String(status)).toBe(false);
    }
  });

  it('goes back to the server for that one code, and for nothing else', () => {
    // The server's own sentence tells the counsellor to open the session again to see who. The
    // phone does it, in the seconds they spend reading the banner — because otherwise it keeps a
    // copy it has just been told is stale: the item still reads as uncovered, the tick control
    // is still live under a thumb, and the next press meets the same refusal.
    const beaten = troubleOf(refusal({ status: 409, code: CODE_ALREADY_COVERED }), 'en', 'tick');
    expect(readsBack(beaten)).toBe(true);

    // Everything else waits for the person to press reload. A screen that re-read itself after
    // every refusal is one that quietly retries in a room with no signal.
    for (const code of ['COUNSELING_SESSION_FINISHED', 'COUNSELING_ITEM_NOT_TICKED']) {
      expect(readsBack(troubleOf(refusal({ status: 409, code }), 'en', 'untick')), code).toBe(
        false,
      );
    }
    expect(readsBack(troubleOf(refusal({ status: 422 }), 'en', 'tick'))).toBe(false);
    expect(readsBack(trouble({ kind: 'unreachable', status: 0 }))).toBe(false);
  });

  it('names the colleague where this phone’s copy already knows, and guesses at nobody', () => {
    const covered = session({
      ticks: [tick({ item_code: 'DIET', ticked_by: SOMEBODY_ELSE, ticked_role: 'NUTRITIONIST' })],
    });
    const theirs = coveredBy(covered, 'DIET', ME);
    expect(theirs?.mine).toBe(false);
    expect(theirs?.role).toBe('NUTRITIONIST');
    expect(attributionKey(theirs!)).toBe('bySomebodyElse');
    expect(clockTime(theirs!.at)).toBe('11:02');

    // The ordinary case: a phone that knew would not have offered the tick, so there is
    // nothing to name and nothing is invented.
    expect(coveredBy(covered, 'EXERCISE', ME)).toBeNull();
    expect(coveredBy(null, 'DIET', ME)).toBeNull();
    expect(coveredBy(covered, 'DIET,EXERCISE', ME)).toBeNull();
    // A tick somebody took back is not a colleague covering it either.
    const withdrawn = session({
      ticks: [tick({ item_code: 'DIET', undone_at: '2026-09-04T05:04:00.000Z' })],
    });
    expect(coveredBy(withdrawn, 'DIET', ME)).toBeNull();
  });

  it('has its own sentence in both languages, and it blames nobody', () => {
    for (const language of ['en', 'bn'] as const) {
      const sentence = String(lookup(language, 'counseling.trouble.alreadyCovered'));
      expect(typeof lookup(language, 'counseling.trouble.alreadyCovered')).toBe('string');
      expect(sentence, language).not.toBe(String(lookup(language, 'counseling.trouble.refused')));
    }
    expect(String(lookup('en', 'counseling.trouble.alreadyCovered'))).not.toMatch(
      /error|invalid|you (did|must)/i,
    );
  });
});

// --- an event id belongs to an attempt, not to a press ---

describe('a retry re-sends the id its attempt was made with', () => {
  it('keeps one id per act, session and item, and never shares one between items', () => {
    // Ticking DIET and ticking EXERCISE are two acts. Sharing an id would make the second a
    // replay of the first, and the ledger would record one item covered where two were.
    expect(ATTEMPTS).toEqual(['start', 'tick', 'untick', 'finish']);
    const diet = attemptKey('tick', 'session-1', 'DIET');
    expect(diet).not.toBe(attemptKey('tick', 'session-1', 'EXERCISE'));
    expect(diet).not.toBe(attemptKey('untick', 'session-1', 'DIET'));
    expect(diet).not.toBe(attemptKey('tick', 'session-2', 'DIET'));
    // Whitespace around an id is not a different attempt.
    expect(attemptKey('tick', ' session-1 ', ' DIET ')).toBe(diet);
  });

  it('re-sends the first id rather than minting a second', () => {
    // **This is the whole point.** The server answers a repeat of a stored tick successfully
    // when the id matches, and refuses it with 409 when it does not — so a fresh id on the
    // retry turns this phone's own successful write into "somebody has already covered this"
    // against itself.
    const key = attemptKey('tick', 'session-1', 'DIET');
    const first = eventFor({}, key, 'event-a');
    expect(first).toBe('event-a');

    const held = holdEvent({}, key, first);
    expect(eventFor(held, key, 'event-b')).toBe('event-a');
    expect(eventFor(held, key, 'event-c')).toBe('event-a');
    // A different item is still a new act with a new id.
    expect(eventFor(held, attemptKey('tick', 'session-1', 'EXERCISE'), 'event-d')).toBe('event-d');
    // And a blank held value is not an id.
    expect(eventFor({ [key]: '   ' }, key, 'event-e')).toBe('event-e');
  });

  it('forgets the id once the attempt is settled, so the next press is a new act', () => {
    // Re-ticking an item after taking it back is a genuinely new tick, by whoever ticks it this
    // time. Reusing the old id would have the server hand back the answer to a request nobody
    // is making any more.
    const key = attemptKey('tick', 'session-1', 'DIET');
    const held = holdEvent({}, key, 'event-a');
    expect(eventFor(releaseEvent(held, key), key, 'event-b')).toBe('event-b');
    // Releasing one attempt leaves the others alone.
    const two = holdEvent(held, attemptKey('finish', 'session-1', ''), 'event-f');
    expect(Object.keys(releaseEvent(two, key))).toEqual([attemptKey('finish', 'session-1', '')]);
    // Nothing is mutated in place: the screen holds these in React state.
    expect(held[key]).toBe('event-a');
  });

  it('keeps the id only while nobody knows whether the write landed', () => {
    // A refusal wrote nothing, and the counsellor is being told why. A lost connection or a
    // server that fell over mid-request may have written, and the retry must be recognisable
    // as the same act rather than counted as a second one.
    expect(
      keepsItsEvent({
        kind: 'unreachable',
        attempt: 'tick',
        status: 0,
        code: '',
        field: '',
        message: '',
      }),
    ).toBe(true);
    expect(
      keepsItsEvent({
        kind: 'failed',
        attempt: 'tick',
        status: 500,
        code: '',
        field: '',
        message: '',
      }),
    ).toBe(true);
    for (const status of [403, 404, 409, 422]) {
      expect(
        keepsItsEvent({
          kind: 'refused',
          attempt: 'tick',
          status,
          code: '',
          field: '',
          message: '',
        }),
        String(status),
      ).toBe(false);
    }
  });

  it('mints a fresh id nowhere but through this rule', () => {
    // The screen is the only place a uuid is made, and it is made through `eventFor`. A
    // `crypto.randomUUID()` inside a mutation body would be a new id on every press.
    for (const file of readdirSync(featureDir)) {
      expect(/randomUUID|uuid\(/.test(featureCode(file)), `${file} mints no ids`).toBe(false);
    }
  });
});

// --- the words ---

const messages = { en: en as Record<string, unknown>, bn: bn as Record<string, unknown> }; // prettier-ignore

function lookup(language: 'en' | 'bn', path: string): unknown {
  return path
    .split('.')
    .reduce<unknown>(
      (node, key) =>
        node !== null && typeof node === 'object'
          ? (node as Record<string, unknown>)[key]
          : undefined,
      messages[language],
    );
}

describe('every label this station can produce exists in both languages', () => {
  const present = (path: string) => {
    for (const language of ['en', 'bn'] as const) {
      expect(typeof lookup(language, path), `${language}: ${path}`).toBe('string');
    }
  };

  it('has a sentence for every refusal this station can produce', () => {
    expect(PROBLEMS).toHaveLength(6);
    for (const problem of PROBLEMS) present(`counseling.problem.${problem}`);
  });

  it('has a sentence for every advice and every kind of trouble', () => {
    expect(ADVICE).toHaveLength(3);
    for (const advice of ADVICE) present(`counseling.advice.${advice}`);
    // The three shapes, plus the one refusal that gets its own words because the generic
    // sentence would read as the counsellor having done something wrong.
    for (const kind of ['refused', 'unreachable', 'failed', 'alreadyCovered']) {
      present(`counseling.trouble.${kind}`);
    }
  });

  it('has a sentence for every conflict the server can name, whichever act met it', () => {
    // The code chooses the sentence now. Every branch of that choice is spelled here, so a code
    // recognised without a sentence written for it is a test failure rather than a raw
    // identifier under somebody's finger.
    const conflict = (code: string, attempt: Attempt) =>
      troubleKey(troubleOf(refusal({ status: 409, code }), 'en', attempt));

    for (const code of [
      CODE_ALREADY_COVERED,
      'COUNSELING_SESSION_FINISHED',
      'COUNSELING_ITEM_NOT_TICKED',
      'IDEMPOTENCY_KEY_REUSED',
    ]) {
      for (const attempt of ATTEMPTS) present(`counseling.${conflict(code, attempt)}`);
    }

    // And the act changes none of them, which is the whole of the fix: a session somebody had
    // closed used to read as a colleague having got there first, purely because a tick met it.
    const finished = ATTEMPTS.map((attempt: Attempt) =>
      conflict('COUNSELING_SESSION_FINISHED', attempt),
    );
    expect(new Set(finished)).toEqual(new Set(['trouble.refused']));
  });

  it('has two attributions for a tick and two for a withdrawal, and they differ', () => {
    for (const part of ['ticked', 'withdrawn']) {
      present(`counseling.${part}.byYou`);
      present(`counseling.${part}.bySomebodyElse`);
      for (const language of ['en', 'bn'] as const) {
        // The whole of criterion 6 in the words: somebody else's act must never read as yours.
        expect(lookup(language, `counseling.${part}.byYou`), `${language} ${part}`).not.toBe(
          lookup(language, `counseling.${part}.bySomebodyElse`),
        );
      }
    }
  });

  it('says which language an item fell back to, in both languages', () => {
    for (const language of ['en', 'bn'] as const) present(`counseling.inLanguage.${language}`);
  });

  it('has both finishing sentences, and neither reads as a failure', () => {
    for (const key of ['finish', 'finishWithOutstanding', 'finishHint', 'finishOutstandingHint']) {
      present(`counseling.${key}`);
    }
    // The sentence a counsellor whose patient walked out reads. It has to say the record is
    // honest, because the alternative on offer is ticking items nobody covered.
    expect(String(lookup('en', 'counseling.finishOutstandingHint'))).toMatch(/honest/i);
    expect(String(lookup('en', 'counseling.finishOutstandingHint'))).not.toMatch(
      /error|failed|invalid|incomplete record/i,
    );
  });

  it('states progress as two numbers in both languages, and never as a percentage', () => {
    for (const language of ['en', 'bn'] as const) {
      const progress = String(lookup(language, 'counseling.progress'));
      expect(progress, `${language} progress`).toContain('{covered}');
      expect(progress, `${language} progress`).toContain('{mandatory}');
      expect(progress, `${language} progress`).not.toContain('%');
    }
    const counselingEn = JSON.stringify((en as Record<string, unknown>)['counseling']);
    const counselingBn = JSON.stringify((bn as Record<string, unknown>)['counseling']);
    expect(counselingEn).not.toContain('%');
    expect(counselingBn).not.toContain('%');
  });

  it('has the rest of the screen, in both languages', () => {
    for (const key of [
      'station',
      'loading',
      'noSession',
      'noSessionHint',
      'checklists',
      'startChecklist',
      'startingChecklist',
      'unwalked',
      'calledFor',
      'frozen',
      'progressHint',
      'optionalCovered',
      'taps',
      'finished',
      'finishedAt',
      'alreadyFinished',
      'mayNotTick',
      'yourRoom',
      'mandatory',
      'optional',
      'tick',
      'ticking',
      'covered',
      'noText',
      'withdrawnWhy',
      'unknownRole',
      'undoCount',
      'note',
      'noteAdd',
      'noteHint',
      'notePlaceholder',
      'untickOpen',
      'untickReasonLabel',
      'untickHint',
      'untickReasonPlaceholder',
      'untick',
      'untickCancel',
      'missingHeading',
      'nothingOutstanding',
      'reload',
      'retry',
    ]) {
      present(`counseling.${key}`);
    }
    // The two station titles: a nutritionist is at nutrition even though the list they are
    // walking says "counselling" at the top of it.
    present('screen.counseling');
    present('screen.nutrition');
  });

  it('sends a counsellor with nothing to walk to the station that can fix it', () => {
    // The empty screen is not a dead end. A checklist is called for by a *coded* condition, and
    // a complaint in the clinic's own words matches no rule — so the sentence names where the
    // coding is done rather than leaving the counsellor to work it out.
    expect(String(lookup('en', 'counseling.noSessionHint'))).toMatch(/history station/i);
    expect(String(lookup('bn', 'counseling.noSessionHint'))).toContain('ইতিহাস স্টেশন');
    // And it still says the choice is not this screen's to make.
    expect(String(lookup('en', 'counseling.noSessionHint'))).toMatch(/coded against them/i);
  });

  it('says why a checklist is being offered, in both languages', () => {
    for (const language of ['en', 'bn'] as const) {
      expect(String(lookup(language, 'counseling.calledFor')), language).toContain('{code}');
    }
  });

  it('tells a reader that a checklist was called for and nobody walked it', () => {
    // The sentence a reviewer or a physician's panel reads on a visit that was held. It has to
    // say the counselling did not happen — not that this screen could not show it — because the
    // two are read the same way by somebody deciding whether to send the patient back.
    for (const language of ['en', 'bn'] as const) {
      expect(typeof lookup(language, 'counseling.unwalked'), language).toBe('string');
      expect(String(lookup(language, 'counseling.unwalked')), language).not.toBe(
        String(lookup(language, 'counseling.alreadyFinished')),
      );
    }
    expect(String(lookup('en', 'counseling.unwalked'))).toMatch(/nobody has opened/i);
    expect(String(lookup('en', 'counseling.unwalked'))).not.toMatch(/permission|not allowed/i);
  });

  it('says out loud that an item must be covered, rather than only colouring it', () => {
    // Roughly one man in twelve who will work here cannot rely on the colour, and direct sun
    // through the clinic's windows flattens it for everybody else.
    for (const language of ['en', 'bn'] as const) {
      expect(lookup(language, 'counseling.mandatory')).not.toBe(
        lookup(language, 'counseling.optional'),
      );
      expect(lookup(language, 'counseling.covered')).not.toBe(lookup(language, 'counseling.tick'));
    }
  });
});

// --- the screen holds no decisions ---

describe('the screen is arrangement and the decisions are not in it', () => {
  it('reads every rule from state.ts rather than restating one', () => {
    const screen = featureCode('CounselingStation.tsx');
    // No second copy of "what is outstanding", "who ticked this" or "may this finish".
    expect(/\.filter\([^)]*mandatory/.test(screen), 'no second outstanding count').toBe(false);
    expect(/ticked_by\s*===/.test(screen), 'no second attribution check').toBe(false);
    expect(/completed_at\s*[=!]==/.test(screen), 'no second completion check').toBe(false);
  });

  it('sizes its tick target from the token rather than a number', () => {
    // CP09 sets `size.touchTarget` at 48 and calls it a safety requirement rather than a style
    // choice. A hardcoded 48 is a number that will not follow the token when it moves.
    const screen = featureCode('CounselingStation.tsx');
    expect(screen).toContain('theme.size.touchTarget');
    expect(/minHeight:\s*\d/.test(screen), 'no hardcoded minHeight').toBe(false);
    expect(/#[0-9a-fA-F]{3,8}\b/.test(screen), 'no colour literal').toBe(false);
  });

  it('uses no browser storage and no `any`', () => {
    for (const file of readdirSync(featureDir)) {
      expect(/:\s*any\b|<any>|as any\b/.test(featureCode(file)), `${file}: no any`).toBe(false);
    }
  });
});
