import { readFileSync, readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { afterEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '@dthcms/api-client';

import en from '../src/messages/en.json';
import bn from '../src/messages/bn.json';
import {
  CODE_ALREADY_COVERED,
  CODE_GATE_BLOCKED,
  GATE_STATES,
  REMEDIES,
  gateReadingOf,
  gateRefused,
  gateStateOf,
  gateToneFor,
  grantedBy,
  missingRoomsOf,
  missingRowOf,
  remedyFor,
  type CounselingGate,
  type CounselingGateOverride,
  type CounselingMissingItem,
  type GateState,
  type Locale,
} from '../src/features/counseling/state';

/*
 * The station binding reaches the Keystore through lib/credentials, and the native module cannot
 * load under Node. Mocked exactly as counseling.test.ts does; nothing here exercises it.
 */
vi.mock('expo-secure-store', () => ({
  setItemAsync: vi.fn(async () => undefined),
  getItemAsync: vi.fn(async () => null),
  deleteItemAsync: vi.fn(async () => undefined),
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'WHEN_UNLOCKED_THIS_DEVICE_ONLY',
}));

const counselingApi = await import('../src/features/counseling/api');
const counselingState = await import('../src/features/counseling/state');
const { getCounselingGate, troubleOf } = counselingApi;

/**
 * The counselling fail-closed gate, on the operator's phone (CP57, §5.5).
 *
 * The screen is a React Native component and is judged in a clinic corridor by somebody holding
 * a phone with a patient beside them. What is checked here is every decision behind it, and six
 * of these tests matter more than the rest.
 *
 * **Every missing item is named, and nothing is dropped.** Criterion 2 is that the blocked
 * message names exactly which items are missing, and the failure it exists to prevent is "this
 * patient cannot go on" with no list — a refusal that sends an operator to whoever is quickest to
 * ask rather than whoever is right. So the item count on the screen equals the server's, an item
 * with no text in either language is still a row, and a room whose words are missing is drawn
 * under its bare code rather than left out.
 *
 * **The two remediation paths are never collapsed.** A half-walked checklist is resumed; a
 * checklist nobody opened is started. The server draws the line by leaving `session_id` off, and
 * nothing here infers it from anything else — sending half the patients to the wrong room is the
 * failure, and it is the cheap mistake to make.
 *
 * **One read draws the whole panel.** The rooms' own words and the checklists' own titles travel
 * on the missing item, so there is no room-catalogue fetch and no merge with the visit's
 * checklist answer. A phone holding nothing but a refusal is the situation this panel exists for.
 *
 * **The phone does not decide the gate.** `blocked` and `overridden` are mirrored from the
 * server's answer for every combination, including the two that look wrong: blocked with an
 * empty list, and a non-empty list that is not blocking. A client-side "looks finished to me" is
 * exactly how a green tick appears on a phone while the queue refuses the patient in front of it.
 *
 * **There is no override anywhere in this feature.** Not a call, not a builder, not a control.
 *
 * **The refusal is recognised by the server's code and never by its words**, and it is not drawn
 * in the treatment this app gives a crash: nothing was lost and nothing went wrong.
 */

// --- the fixtures: the seeded three rooms and the seven-item diabetes checklist ---

/** §5.2's rooms as the server now sends them on the item: words and place in the walk. */
const ROOM_WORDS: Record<string, { en: string; bn: string; step: number }> = {
  COUNSELING_ROOM: { en: 'Counseling room', bn: 'কাউন্সেলিং রুম', step: 1 },
  NUTRITION_ROOM: { en: 'Nutrition room', bn: 'পুষ্টি রুম', step: 2 },
  INSULIN_CORNER: { en: 'Insulin corner', bn: 'ইনসুলিন কর্নার', step: 3 },
};

const missing = (
  code: string,
  room: string,
  over: Partial<CounselingMissingItem> = {},
): CounselingMissingItem => ({
  template_id: 'template-1',
  template_code: 'DIABETES',
  title_en: 'Diabetes counselling',
  title_bn: 'ডায়াবেটিস কাউন্সেলিং',
  session_id: 'session-1',
  item_code: code,
  room,
  room_en: ROOM_WORDS[room]?.en ?? '',
  room_bn: ROOM_WORDS[room]?.bn ?? '',
  // Zero for a room the vocabulary no longer lists — the server coalesces the left join rather
  // than dropping the row, and so does this fixture.
  room_step: ROOM_WORDS[room]?.step ?? 0,
  text_en: `${code} in English`,
  text_bn: `${code} বাংলায়`,
  ...over,
});

const unopened = (code: string, room: string): CounselingMissingItem =>
  missing(code, room, {
    template_id: 'template-2',
    template_code: 'HYPOTHYROID',
    title_en: 'Hypothyroidism counselling',
    title_bn: 'হাইপোথাইরয়েড কাউন্সেলিং',
    session_id: undefined,
  });

/**
 * A checklist somebody opened and left half-walked, in the server's own order.
 *
 * `core.counseling_gate_missing` sorts by template code, then the room's configured place in the
 * walk, then the item code — so the nutrition room (2) comes before the insulin corner (3), and
 * this file does no ordering of its own to make that true.
 */
const HALF_WALKED: CounselingMissingItem[] = [
  missing('DIET', 'NUTRITION_ROOM'),
  missing('INSULIN_TECHNIQUE', 'INSULIN_CORNER'),
];

/** A checklist nobody opened: every mandatory item outstanding, and no session to go back to. */
const UNOPENED: CounselingMissingItem[] = [
  unopened('DISEASE_UNDERSTANDING', 'COUNSELING_ROOM'),
  unopened('SELF_CARE', 'COUNSELING_ROOM'),
];

const override = (over: Partial<CounselingGateOverride> = {}): CounselingGateOverride => ({
  id: 'override-1',
  visit_id: 'visit-1',
  patient_id: 'patient-1',
  granted_at: '2026-09-04T05:02:00.000Z',
  granted_by: 'physician-1',
  granted_role: 'PHYSICIAN',
  granted_by_code: 'DR-014',
  granted_by_name_en: 'Dr Nahid Rahman',
  granted_by_name_bn: 'ডা. নাহিদ রহমান',
  reason: 'The interpreter did not come and the patient had to leave.',
  missing_at_grant: ['DIET', 'INSULIN_TECHNIQUE'],
  ...over,
});

/**
 * A gate as the server answers one.
 *
 * `blocked` defaults to whether anything is missing, which is what the server does — but every
 * test that cares sets it explicitly, because the point of several of them is that this side
 * never works it out.
 */
function gateOf(over: Partial<CounselingGate> = {}): CounselingGate {
  const list = over.missing ?? HALF_WALKED;
  return {
    visit_id: 'visit-1',
    blocked: list.length > 0,
    overridden: false,
    missing: list,
    ...over,
  };
}

/** Every row of the reading, flattened, in the order the screen draws them. */
function drawn(gate: CounselingGate, locale: Locale = 'en') {
  return missingRoomsOf(gate, locale).flatMap((group) =>
    group.lists.flatMap((list) => list.rows.map((row) => ({ group, list, row }))),
  );
}

// --- the network ---

function respond(body: unknown, init: { status?: number } = {}) {
  return new Response(JSON.stringify(body), {
    status: init.status ?? 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

interface Call {
  url: string;
  method: string;
  body: string;
}

function stubFetch(...responses: Response[]): Call[] {
  const calls: Call[] = [];
  let index = 0;
  const mock = vi.fn(async (request: Request) => {
    calls.push({ url: request.url, method: request.method, body: await request.clone().text() });
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
 * These tests are about what the code does, not the prose beside it. A comment saying "there is
 * no override control here" would otherwise fail the test that checks there is no override
 * control here, which would teach the next person to stop writing the comment.
 */
function featureCode(file: string): string {
  return featureSource(file)
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/.*$/gm, '$1');
}

// --- criterion 2: the blocked message names exactly which items are missing ---

describe('a blocked patient is a named list, never a count on its own', () => {
  it('draws every item the server named, and drops none of them', () => {
    // The whole of criterion 2. A refusal that said only "this patient cannot go on" is what
    // CP57 exists to prevent: it sends an operator back to a screen with no idea what to do
    // next, and what they do next is ask whoever is nearest rather than whoever is right.
    const gate = gateOf({ missing: [...HALF_WALKED, ...UNOPENED] });
    const rows = drawn(gate);
    expect(rows).toHaveLength(gate.missing.length);
    expect(rows.map(({ row }) => row.code)).toEqual([
      'DIET',
      'INSULIN_TECHNIQUE',
      'DISEASE_UNDERSTANDING',
      'SELF_CARE',
    ]);
  });

  it('reports the same count the server sent, whatever the grouping does', () => {
    // The number in the banner and the length of the list under it come from the same place. A
    // count that disagreed with its own list is the first thing an operator stops believing.
    const gate = gateOf({ missing: [...HALF_WALKED, ...UNOPENED] });
    const reading = gateReadingOf(gate, 'en');
    expect(reading.missing).toBe(gate.missing.length);
    expect(drawn(gate)).toHaveLength(reading.missing);
  });

  it('names each item in the reader’s language, and says when it could not', () => {
    const english = missingRowOf(missing('DIET', 'NUTRITION_ROOM'), 'en');
    expect(english.text.text).toBe('DIET in English');
    expect(english.text.ownLanguage).toBe(true);

    const bengali = missingRowOf(missing('DIET', 'NUTRITION_ROOM'), 'bn');
    expect(bengali.text.text).toBe('DIET বাংলায়');
    expect(bengali.text.ownLanguage).toBe(true);

    // Publishing refuses a version missing either language, so this is an edge case rather than
    // a shape to design around — and it still says which language it fell back to, because a
    // Bangla reader handed English with no explanation is one who thinks the app switched.
    const fallback = missingRowOf(missing('DIET', 'NUTRITION_ROOM', { text_bn: '' }), 'bn');
    expect(fallback.text.text).toBe('DIET in English');
    expect(fallback.text.ownLanguage).toBe(false);
    expect(fallback.text.language).toBe('en');
  });

  it('keeps an item with no text at all as a row rather than losing it', () => {
    // A row the screen cannot word is still an item the patient is being held for. Dropping it
    // would make the list shorter than the count above it, and the screen would be lying about
    // a patient standing in front of somebody.
    const gate = gateOf({
      missing: [missing('MYSTERY', 'NUTRITION_ROOM', { text_en: '', text_bn: '' })],
    });
    const rows = drawn(gate);
    expect(rows).toHaveLength(1);
    expect(rows[0]!.row.code).toBe('MYSTERY');
    expect(rows[0]!.row.text.text).toBe('');
    expect(rows[0]!.row.text.language).toBeNull();
  });
});

// --- one read draws the whole panel ---

describe('the gate’s own answer carries every word the panel needs', () => {
  it('names the checklist from the row, in the reader’s language', () => {
    // The refusal used to be merged with the visit's checklist answer to turn `DIABETES` into
    // "Diabetes counselling". The gate carries its own title now, which matters most in the one
    // situation this panel exists for: a phone holding nothing but a refusal.
    const both = missingRoomsOf(gateOf({ missing: [...HALF_WALKED, ...UNOPENED] }), 'en');
    const named = both.flatMap((group) => group.lists.map((list) => list.checklist));
    expect(named).toContain('Diabetes counselling');
    expect(named).toContain('Hypothyroidism counselling');

    const bengali = missingRoomsOf(gateOf(), 'bn');
    expect(bengali[0]!.lists[0]!.checklist).toBe('ডায়াবেটিস কাউন্সেলিং');
  });

  it('falls back to the checklist’s code rather than a blank name', () => {
    // Both title fields are optional on the wire. A nameless checklist on a blocked screen is a
    // route nobody can follow, so the stable code is what a reader gets instead.
    const nameless = gateOf({
      missing: [missing('DIET', 'NUTRITION_ROOM', { title_en: '', title_bn: '' })],
    });
    expect(missingRoomsOf(nameless, 'en')[0]!.lists[0]!.checklist).toBe('DIABETES');
    expect(missingRoomsOf(nameless, 'bn')[0]!.lists[0]!.checklist).toBe('DIABETES');
  });

  it('names the room from the row, in the reader’s language', () => {
    expect(missingRoomsOf(gateOf(), 'en')[0]!.heading.text).toBe('Nutrition room');
    expect(missingRoomsOf(gateOf(), 'bn')[0]!.heading.text).toBe('পুষ্টি রুম');
  });

  it('falls back to the bare room code rather than an empty heading', () => {
    // A room the vocabulary no longer lists: the server left-joins it and coalesces the words to
    // empty rather than dropping the item. A blank heading would silently merge two rooms into
    // one list, and on this screen that is two rooms' worth of patients sent to one of them.
    const forgotten = gateOf({
      missing: [
        missing('FOOT_CARE', 'PODIATRY_CORNER', { room_en: '', room_bn: '', room_step: 0 }),
        missing('DIET', 'NUTRITION_ROOM'),
      ],
    });
    const groups = missingRoomsOf(forgotten, 'en');
    expect(groups.map((group) => group.heading.text)).toEqual([
      'PODIATRY_CORNER',
      'Nutrition room',
    ]);
    // And nothing is dropped: a list shorter than its own count is a list nobody trusts.
    expect(drawn(forgotten)).toHaveLength(2);
  });

  it('fetches no room catalogue and no template list to draw itself', () => {
    // The gate read is the only request this panel makes. CP56 removed the room catalogue for
    // the checklist screen on the grounds that a second request on a link which drops for
    // seconds at a time buys too little; the same reasoning applies here now that the words are
    // on the item.
    for (const file of readdirSync(featureDir)) {
      const source = featureCode(file);
      expect(/counseling\/rooms/.test(source), `${file} fetches no room catalogue`).toBe(false);
      expect(/counseling\/templates/.test(source), `${file} fetches no template`).toBe(false);
    }
    expect(Object.keys(counselingApi).filter((name) => /room|template/i.test(name))).toEqual([]);
  });
});

// --- the two ways back ---

describe('there are two ways back and they are never drawn as one', () => {
  it('reads the remedy from the session the server named, and from nothing else', () => {
    // The whole difference, in one field. "Go back and finish it" told to somebody whose
    // colleague never started is an instruction that cannot be followed — and following it is a
    // patient walked to the wrong room.
    expect(remedyFor(missing('DIET', 'NUTRITION_ROOM'))).toBe('resume');
    expect(remedyFor(missing('DIET', 'NUTRITION_ROOM', { session_id: undefined }))).toBe('start');
    // Empty and whitespace are the same absence. A session id of three spaces would otherwise
    // route somebody into a session that does not exist.
    expect(remedyFor(missing('DIET', 'NUTRITION_ROOM', { session_id: '' }))).toBe('start');
    expect(remedyFor(missing('DIET', 'NUTRITION_ROOM', { session_id: '   ' }))).toBe('start');
    expect(REMEDIES).toEqual(['resume', 'start']);
  });

  it('carries the session to resume into, and nothing to start into', () => {
    for (const group of missingRoomsOf(gateOf({ missing: HALF_WALKED }), 'en')) {
      for (const list of group.lists) {
        expect(list.remedy).toBe('resume');
        expect(list.sessionId).toBe('session-1');
      }
    }

    for (const group of missingRoomsOf(gateOf({ missing: UNOPENED }), 'en')) {
      for (const list of group.lists) {
        expect(list.remedy).toBe('start');
        // Nothing to go back into, and no id invented for a session that does not exist.
        expect(list.sessionId).toBe('');
        expect(list.templateId).toBe('template-2');
      }
    }
  });

  it('offers one route per checklist rather than one per item', () => {
    // A checklist nobody opened has every mandatory item outstanding — seven for the seeded
    // diabetes list. A control per item would be seven identical buttons opening one session,
    // which is a screen whose real choice is invisible.
    const seven = ['A', 'B', 'C', 'D', 'E', 'F', 'G'].map((code) =>
      unopened(code, 'COUNSELING_ROOM'),
    );
    const groups = missingRoomsOf(gateOf({ missing: seven }), 'en');
    expect(groups).toHaveLength(1);
    expect(groups[0]!.lists).toHaveLength(1);
    expect(groups[0]!.lists[0]!.rows).toHaveLength(7);
  });

  it('keeps two checklists in one room as two routes', () => {
    // Both walked in the counselling room, and they are two different presses.
    const gate = gateOf({
      missing: [
        missing('DISEASE_UNDERSTANDING', 'COUNSELING_ROOM'),
        unopened('SELF_CARE', 'COUNSELING_ROOM'),
      ],
    });
    const groups = missingRoomsOf(gate, 'en');
    expect(groups).toHaveLength(1);
    expect(groups[0]!.lists.map((list) => list.remedy)).toEqual(['resume', 'start']);
  });

  it('never lets one checklist wear two remedies at once', () => {
    // The server's own query cannot produce a template that is both walked and unopened. If one
    // ever arrived, taking whichever remedy came first would route half of it wrongly — so the
    // grouping keys on the session as well and produces two routes instead of one guess.
    const gate = gateOf({
      missing: [
        missing('DIET', 'NUTRITION_ROOM'),
        missing('DIET_AGAIN', 'NUTRITION_ROOM', { session_id: undefined }),
      ],
    });
    const lists = missingRoomsOf(gate, 'en')[0]!.lists;
    expect(lists).toHaveLength(2);
    expect(lists.map((list) => list.remedy).sort()).toEqual(['resume', 'start']);
  });
});

// --- the rooms ---

describe('the missing items are grouped by room, in the order the server sent them', () => {
  it('reads in corridor order for one checklist, with no ordering of its own', () => {
    // `core.counseling_gate_missing` sorts by template code, then the room's own place in the
    // walk, then the item code. So the corridor order arrives already made, and this file holds
    // no opinion about it — the earlier version walked the room catalogue to impose the same
    // order, which was a second request and a second opinion.
    const gate = gateOf({
      missing: [
        missing('DISEASE_UNDERSTANDING', 'COUNSELING_ROOM'),
        missing('DIET', 'NUTRITION_ROOM'),
        missing('INSULIN_TECHNIQUE', 'INSULIN_CORNER'),
      ],
    });
    expect(missingRoomsOf(gate, 'en').map((group) => group.room)).toEqual([
      'COUNSELING_ROOM',
      'NUTRITION_ROOM',
      'INSULIN_CORNER',
    ]);
    // And there is still no comparator in the file, which is what keeps the promise that a
    // session's items are never re-sorted from rotting.
    expect(/\.sort\(/.test(featureCode('state.ts')), 'no comparator in state.ts').toBe(false);
  });

  it('follows the server across two checklists, and that is the whole cost', () => {
    // The server's outermost key is the template, so a room only the second checklist has
    // follows the first checklist's rooms rather than merging into the corridor. `room_step` is
    // on the wire and this file deliberately does not read it: reading it would mean sorting,
    // and a comparator here is how the item-order guarantee rots. Two checklists in different
    // rooms read slightly out of walking order; nothing is lost or hidden.
    const gate = gateOf({ missing: [...HALF_WALKED, ...UNOPENED] });
    expect(missingRoomsOf(gate, 'en').map((group) => group.room)).toEqual([
      'NUTRITION_ROOM',
      'INSULIN_CORNER',
      'COUNSELING_ROOM',
    ]);
    expect(/room_step/.test(featureCode('state.ts')), 'room_step is not read').toBe(false);
  });

  it('merges a room two checklists share into one heading', () => {
    const gate = gateOf({
      missing: [
        missing('DISEASE_UNDERSTANDING', 'COUNSELING_ROOM'),
        unopened('SELF_CARE', 'COUNSELING_ROOM'),
      ],
    });
    const groups = missingRoomsOf(gate, 'en');
    expect(groups).toHaveLength(1);
    expect(groups[0]!.heading.text).toBe('Counseling room');
  });

  it('has no “your room” marking, because the gate does not say which station works one', () => {
    // `RoomGroup.yours` on the checklist comes from `room_station` on a session's item; the
    // gate's items do not carry it. Fetching the room catalogue for that one boolean is exactly
    // the second request CP56 removed, so the marking is absent rather than bought. Every room
    // is still drawn to everybody, which was always the half that mattered.
    const groups = missingRoomsOf(gateOf(), 'en');
    for (const group of groups) expect(group).not.toHaveProperty('yours');
    expect(groups).toHaveLength(2);
  });
});

// --- the phone does not decide the gate ---

describe('the gate is the server’s answer and this side only reads it', () => {
  it('mirrors blocked and overridden for every combination the server can send', () => {
    const cases: { blocked: boolean; overridden: boolean; state: GateState }[] = [
      { blocked: true, overridden: false, state: 'blocked' },
      { blocked: false, overridden: true, state: 'overridden' },
      { blocked: false, overridden: false, state: 'clear' },
    ];
    for (const one of cases) {
      const gate = gateOf({ blocked: one.blocked, overridden: one.overridden });
      const reading = gateReadingOf(gate, 'en');
      expect(gateStateOf(gate)).toBe(one.state);
      expect(reading.state).toBe(one.state);
      expect(reading.blocked).toBe(one.blocked);
      expect(reading.overridden).toBe(one.overridden);
      // One reassuring state, and it is the one where nothing was outstanding and nobody had to
      // be let through.
      expect(reading.clear).toBe(one.state === 'clear');
    }
  });

  it('trusts the server when the answer looks wrong from here', () => {
    // Blocked with nothing in the list, and a full list that is not blocking. Both are answers
    // this side must not correct: `blocked` belongs to a trigger on the queue table, and a
    // client that recomputed it would be a second, staler account of a rule it does not own —
    // one that disagrees in whichever direction the bug happens to fall.
    const empty = gateReadingOf(gateOf({ blocked: true, missing: [] }), 'en');
    expect(empty.blocked).toBe(true);
    expect(empty.state).toBe('blocked');
    expect(empty.missing).toBe(0);

    const passing = gateReadingOf(
      gateOf({ blocked: false, overridden: false, missing: HALF_WALKED }),
      'en',
    );
    expect(passing.blocked).toBe(false);
    expect(passing.state).toBe('clear');
    // And the items are still drawn: they are still uncovered, and a counsellor can still cover
    // them. What this side does not do is call the patient held.
    expect(passing.rooms.flatMap((group) => group.lists)).not.toHaveLength(0);
  });

  it('never reads an override as counselling that was done', () => {
    // The contract keeps `blocked` and `overridden` apart for one stated reason: a screen that
    // drew them the same way would be telling a physician the counselling happened.
    const gate = gateOf({ blocked: false, overridden: true, override: override() });
    const reading = gateReadingOf(gate, 'en');
    expect(reading.state).toBe('overridden');
    expect(reading.clear).toBe(false);
    expect(reading.headline).not.toBe('gate.headline.clear');
    expect(reading.override?.reason).toBe(override().reason);
    // What was outstanding when it was granted, not what is outstanding now — items covered
    // afterwards would otherwise make the record say the override was for nothing.
    expect(reading.override?.missing_at_grant).toEqual(['DIET', 'INSULIN_TECHNIQUE']);
    // And the items still outstanding are still on screen, so the counselling can still be done.
    expect(reading.missing).toBe(2);
  });

  it('keeps the override’s own state when everything has since been covered', () => {
    // Granted, and then the items were covered anyway. Still not `clear`: somebody was let
    // through, and the record of that is the point of the valve.
    const gate = gateOf({ blocked: false, overridden: true, missing: [], override: override() });
    const reading = gateReadingOf(gate, 'en');
    expect(reading.state).toBe('overridden');
    expect(reading.missing).toBe(0);
  });

  it('names the person who granted it before the hat they were wearing', () => {
    // The override's whole worth is that a named person took it. "Let through by PHYSICIAN" is a
    // sentence about a permission; the question a reviewer asks six months later is which one.
    expect(grantedBy(override(), 'en')).toBe('Dr Nahid Rahman');
    expect(grantedBy(override(), 'bn')).toBe('ডা. নাহিদ রহমান');
    // The staff code before the role, because it identifies a person rather than a hat.
    expect(grantedBy(override({ granted_by_name_en: '', granted_by_name_bn: '' }), 'en')).toBe(
      'DR-014',
    );
    expect(
      grantedBy(
        override({ granted_by_name_en: '', granted_by_name_bn: '', granted_by_code: '' }),
        'en',
      ),
    ).toBe('PHYSICIAN');
    // And nothing invented when the record names nobody: an attribution this app made up would
    // be the only one in the system naming somebody who did not do the thing.
    expect(
      grantedBy(
        override({
          granted_by_name_en: '',
          granted_by_name_bn: '',
          granted_by_code: '',
          granted_role: '',
        }),
        'en',
      ),
    ).toBe('');
  });

  it('has no function anywhere that builds a gate or an override', () => {
    // The structural half of "the phone must not decide the gate". Every gate function takes the
    // server's answer; none of them constructs one, and none of them takes a session.
    const exported = [...Object.keys(counselingState), ...Object.keys(counselingApi)];
    expect(
      exported.filter((name) => /^(to|make|build|compute)Gate/i.test(name)),
      'nothing here builds a gate',
    ).toEqual([]);
    expect(exported.filter((name) => /override/i.test(name))).toEqual([]);

    // And the valve's endpoint appears nowhere in the feature. It needs
    // `counseling.gate.override`, which a station operator does not hold, so a control wired to
    // it would answer 403 with a patient waiting.
    for (const file of readdirSync(featureDir)) {
      expect(/gate\/override/.test(featureCode(file)), `${file}: no override call`).toBe(false);
    }
  });

  it('has three states and exactly three, each with a tone that is not a crash', () => {
    expect(GATE_STATES).toEqual(['blocked', 'overridden', 'clear']);
    expect(gateToneFor('blocked')).toBe('critical');
    expect(gateToneFor('overridden')).toBe('borderline');
    expect(gateToneFor('clear')).toBe('normal');
    // The same three status tokens CP54's allergy gate uses. A refusal here is not a failure —
    // nothing was lost and the checkpoint did its job — so it must not borrow the treatment this
    // app gives a crash or an unreachable server.
    for (const state of GATE_STATES) {
      expect(['normal', 'borderline', 'critical']).toContain(gateToneFor(state));
    }
  });
});

// --- the refusal at the queue ---

describe('the queue’s refusal is recognised by the server’s code and never by its words', () => {
  const refusal = (code: string, messageEN = 'This patient cannot go on yet.') =>
    new ApiError({
      status: 409,
      code,
      kind: 'conflict',
      messageEN,
      messageBN: 'এই রোগীকে এখনো পাঠানো যাবে না।',
      fields: {},
      fieldsBN: {},
      correlationID: 'req-1',
    });

  it('knows the gate’s own code', () => {
    expect(CODE_GATE_BLOCKED).toBe('VISIT_GATE_BLOCKED');
    expect(gateRefused(troubleOf(refusal(CODE_GATE_BLOCKED), 'en', 'finish'))).toBe(true);
  });

  it('does not mistake the counselling conflicts for it', () => {
    // `409` is five different facts across this feature and the queue, and the code is the only
    // part of a refusal a client may branch on. A screen that matched on the message would stop
    // recognising the gate the day somebody rewrote a sentence.
    for (const code of [
      CODE_ALREADY_COVERED,
      'COUNSELING_SESSION_FINISHED',
      'COUNSELING_ITEM_NOT_TICKED',
      'IDEMPOTENCY_KEY_REUSED',
    ]) {
      expect(gateRefused(troubleOf(refusal(code), 'en', 'tick')), code).toBe(false);
    }
    // Including one carrying the gate's own sentence under a different code.
    expect(
      gateRefused(troubleOf(refusal('CONFLICT', 'This patient cannot go on yet.'), 'en', 'tick')),
    ).toBe(false);
  });

  it('matches on nothing but the code, in either language', () => {
    // The server's message names the missing items as a sentence, in both languages. That
    // satisfies criterion 2 on its own — and it is deliberately not what this feature reads,
    // because a sentence cannot say which room, and the room is where the patient goes.
    for (const locale of ['en', 'bn'] as const) {
      const held = troubleOf(
        refusal(CODE_GATE_BLOCKED, 'counselling is not finished: DIET, INSULIN_TECHNIQUE'),
        locale,
        'finish',
      );
      expect(gateRefused(held)).toBe(true);
    }
    // And a refusal carrying the code with no words at all is still the gate. The message is
    // not read, so it cannot be the thing that breaks when somebody improves a sentence.
    expect(
      gateRefused({
        kind: 'refused',
        attempt: 'finish',
        status: 409,
        code: CODE_GATE_BLOCKED,
        field: '',
        message: '',
      }),
    ).toBe(true);
  });
});

// --- the call ---

describe('the gate call', () => {
  it('reads one visit’s gate, and writes nothing', async () => {
    const calls = stubFetch(respond({ gate: gateOf() }));
    const answer = await getCounselingGate('visit-1');
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/counseling/visits/visit-1/gate');
    expect(calls[0]!.method).toBe('GET');
    expect(calls[0]!.body).toBe('');
    expect(answer.missing).toHaveLength(2);
  });

  it('is the only request the blocked panel makes', async () => {
    // One read, and the whole panel draws from it: the rooms' words, the checklists' titles and
    // the items all travel on the answer.
    const calls = stubFetch(respond({ gate: gateOf({ missing: [...HALF_WALKED, ...UNOPENED] }) }));
    const answer = await getCounselingGate('visit-1');
    expect(calls).toHaveLength(1);
    const groups = missingRoomsOf(answer, 'en');
    expect(groups.map((group) => group.heading.text)).toEqual([
      'Nutrition room',
      'Insulin corner',
      'Counseling room',
    ]);
    expect(groups.flatMap((group) => group.lists.map((list) => list.checklist))).toEqual([
      'Diabetes counselling',
      'Diabetes counselling',
      'Hypothyroidism counselling',
    ]);
  });

  it('logs nothing at all', () => {
    // A gate answer names the item codes a patient was not counselled on. That is a clinical
    // detail about the person standing in front of the phone, and it does not belong in a log.
    for (const file of readdirSync(featureDir)) {
      expect(/console\.(log|warn|error|info|debug)/.test(featureCode(file)), file).toBe(false);
    }
  });
});

// --- the words ---

const messages: Record<'en' | 'bn', unknown> = { en, bn };

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

describe('every label the checkpoint can produce exists in both languages', () => {
  const present = (path: string) => {
    for (const language of ['en', 'bn'] as const) {
      expect(typeof lookup(language, path), `${language}: ${path}`).toBe('string');
    }
  };

  it('has a headline and a meaning for each of the three states', () => {
    expect(GATE_STATES).toHaveLength(3);
    for (const state of GATE_STATES) {
      present(`counseling.gate.headline.${state}`);
      present(`counseling.gate.meaning.${state}`);
    }
  });

  it('says the three states differently, in both languages', () => {
    // The failure this prevents is the one the contract names on the schema: a screen that drew
    // "held" and "let through" the same way would be telling a physician the counselling was
    // done.
    for (const language of ['en', 'bn'] as const) {
      const said = GATE_STATES.map((state) =>
        String(lookup(language, `counseling.gate.headline.${state}`)),
      );
      expect(new Set(said).size, language).toBe(GATE_STATES.length);
    }
  });

  it('has both remedies and both explanations, and they do not read alike', () => {
    for (const remedy of REMEDIES) {
      present(`counseling.gate.remedy.${remedy}`);
      present(`counseling.gate.remedyHint.${remedy}`);
    }
    for (const language of ['en', 'bn'] as const) {
      expect(lookup(language, 'counseling.gate.remedy.resume'), language).not.toBe(
        lookup(language, 'counseling.gate.remedy.start'),
      );
      expect(lookup(language, 'counseling.gate.remedyHint.resume'), language).not.toBe(
        lookup(language, 'counseling.gate.remedyHint.start'),
      );
    }
    // The one a phone must never say to somebody whose colleague never started: the hint for an
    // unopened checklist has to say nobody has opened it, in words.
    expect(String(lookup('en', 'counseling.gate.remedyHint.start'))).toMatch(/nobody has opened/i);
    expect(String(lookup('bn', 'counseling.gate.remedyHint.start'))).toContain('কেউ এই তালিকাটি');
  });

  it('says who may override, and never offers to', () => {
    present('counseling.gate.noOverrideHere');
    // A physician, a reason, and a count of them. Naming the person is the whole substitute for
    // the control that is deliberately absent — "you cannot do this" with no next step is what
    // sends an operator looking for a way round the checkpoint.
    expect(String(lookup('en', 'counseling.gate.noOverrideHere'))).toMatch(/physician/i);
    expect(String(lookup('en', 'counseling.gate.noOverrideHere'))).toMatch(/reason/i);
    expect(String(lookup('bn', 'counseling.gate.noOverrideHere'))).toContain('চিকিৎসক');
    expect(String(lookup('bn', 'counseling.gate.noOverrideHere'))).toContain('কারণ');
  });

  it('does not read the refusal as a failure', () => {
    // Criterion: the checkpoint did its job and nothing was lost. A sentence that called it an
    // error would teach an operator that the app breaks when a patient is held, and the next
    // thing they learn is how to get round it.
    const blocked = String(lookup('en', 'counseling.gate.meaning.blocked'));
    expect(blocked).toMatch(/nothing has gone wrong|nothing has been lost/i);
    expect(blocked).not.toMatch(/error|failed|invalid|crash/i);
  });

  it('has the rest of the panel, in both languages', () => {
    for (const key of [
      'gate.step',
      'gate.loading',
      'gate.unknown',
      'gate.stillMissing',
      'gate.onChecklist',
      'gate.overrideGranted',
      'gate.overrideGrantedAt',
      'gate.overrideReason',
      'gate.overrideCovered',
    ]) {
      present(`counseling.${key}`);
    }
    // The sentence for an answer that never arrived. It must not read as "nothing is holding
    // this patient", which is the one wrong answer a checkpoint may give by accident.
    expect(String(lookup('en', 'counseling.gate.unknown'))).toMatch(/could not read/i);
    expect(String(lookup('en', 'counseling.gate.unknown'))).toMatch(/still refuse/i);
  });

  it('keeps the same placeholders on both sides of every gate message', () => {
    const placeholders = (text: string) =>
      [...text.matchAll(/\{(\w+)\s*[},]/g)].map((match) => match[1]).sort();
    for (const [key, value] of Object.entries({
      'gate.stillMissing': ['n'],
      'gate.onChecklist': ['checklist'],
      'gate.overrideGranted': ['who'],
      'gate.overrideGrantedAt': ['when', 'who'],
      'gate.overrideReason': ['reason'],
      'gate.overrideCovered': ['items'],
    })) {
      for (const language of ['en', 'bn'] as const) {
        expect(
          placeholders(String(lookup(language, `counseling.${key}`))),
          `${language} ${key}`,
        ).toEqual(value);
      }
    }
  });
});

// --- the panel holds no decisions ---

describe('the blocked panel is arrangement and the decisions are not in it', () => {
  const panel = () => featureCode('CounselingGateStep.tsx');

  it('reads every rule from state.ts rather than restating one', () => {
    const screen = panel();
    // No second answer to "is this patient held", "which way back is this", "how many are
    // missing" or "who granted this". Each of those is a decision with a test beside it in
    // `state.ts`.
    expect(/session_id/.test(screen), 'no second remedy check').toBe(false);
    expect(/granted_by_name|granted_by_code/.test(screen), 'no second attribution').toBe(false);
    // The server's answer is touched in exactly one place — the call to `gateReadingOf` — and
    // every value drawn comes off the reading. A component that read `gate.blocked` for itself
    // would be the second implementation this whole checkpoint is arranged to avoid.
    expect(
      /gate\.blocked|gate\.overridden|gate\.missing/.test(screen),
      'reads only the reading',
    ).toBe(false);
    expect(/missing\.length/.test(screen), 'no second count').toBe(false);
    expect(/\.sort\(|\.filter\(/.test(screen), 'no second ordering or filter').toBe(false);
  });

  it('sizes its controls from the token rather than a number', () => {
    // CP09 sets `size.touchTarget` at 48 and calls it a safety requirement rather than a style
    // choice. This panel is tapped in a corridor by somebody holding a patient's file.
    const screen = panel();
    expect(screen).toContain('theme.size.touchTarget');
    expect(/minHeight:\s*\d/.test(screen), 'no hardcoded minHeight').toBe(false);
    expect(/#[0-9a-fA-F]{3,8}\b/.test(screen), 'no colour literal').toBe(false);
  });

  it('draws no control the operator’s hat cannot use', () => {
    // The permission is read to choose a sentence, exactly as the checklist reads it, and there
    // is no override control for it to gate — because there is no override call to wire one to.
    const screen = panel();
    expect(screen).toContain('mayTick');
    expect(/override\s*\(/.test(screen), 'no override control').toBe(false);
  });

  it('uses no browser storage and no `any` anywhere in the feature', () => {
    for (const file of readdirSync(featureDir)) {
      const source = featureCode(file);
      expect(/:\s*any\b|<any>|as any\b/.test(source), `${file}: no any`).toBe(false);
      expect(/localStorage|sessionStorage|AsyncStorage|SecureStore/.test(source), file).toBe(false);
    }
  });
});
