import { readFileSync, readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { afterEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '@dthcms/api-client';

import en from '../src/messages/en.json';
import bn from '../src/messages/bn.json';
import { ANTHRO_FIELDS } from '../src/features/anthropometry/form';
import { VITAL_FIELDS } from '../src/features/vitals/form';
import {
  ADVICE,
  ANSWER_KINDS,
  CLINIC_UTC_OFFSET_MINUTES,
  CODE_ALREADY_ANSWERED,
  CORRECTION_REQUESTED,
  KNOWN_ROLE_CODES,
  MEASUREMENTS,
  NOTE_MAX,
  OUTCOMES,
  PROBLEMS,
  REASON_MAX,
  adviceFor,
  alreadyAnswered,
  answerKindOf,
  applyProblem,
  attemptKey,
  clinicDay,
  clockTime,
  draftFor,
  eventFor,
  flaggedBy,
  flaggedValueOf,
  holdEvent,
  keepsItsEvent,
  measurementFor,
  momentOf,
  myTopic,
  notThisRole,
  notYours,
  notifiesMe,
  outcomeOf,
  queueOf,
  reasonDisplay,
  recomputedOf,
  rejectProblem,
  releaseEvent,
  requestReadingOf,
  roleKeyOf,
  shapeOf,
  toApply,
  toReject,
  unitsFor,
  type CorrectionReason,
  type CorrectionRequest,
  type Draft,
  type FlaggedObservation,
  type Trouble,
} from '../src/features/corrections/state';

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

const correctionsApi = await import('../src/features/corrections/api');
const {
  applyCorrection,
  getFlaggedObservation,
  listCorrectionReasons,
  listMyCorrections,
  rejectCorrection,
  troubleOf,
} = correctionsApi;

/**
 * Answering a correction request, on the device of whoever typed the value (CP62, §4.3).
 *
 * The screen is a React Native component and is judged in a clinic corridor by somebody holding
 * a phone with a patient beside them. What is checked here is every decision behind it, and six
 * of these tests matter more than the rest.
 *
 * **The 140/150 case comes out the other end intact.** A height entered as 150 cm, flagged by a
 * physician who says 140, is shown as *150 cm* — the number and the unit the operator typed,
 * never the canonical round trip — and the body that goes back carries `140` and `cm` and
 * nothing else. That pair of assertions is the whole feature.
 *
 * **A reason is required to refuse, and nothing can send one without it.** `toReject` returns
 * null for an empty reason, for whitespace, and there is no other way in this feature to build
 * a rejection body.
 *
 * **The queue keeps the server's order.** Oldest first, unchanged. A queue answered
 * newest-first is one where the oldest request is never answered, and a sort applied here would
 * be a quiet second opinion about which colleague gets ignored.
 *
 * **Nothing here reads as blame.** No status tone anywhere in the feature, no permission gate in
 * front of the controls, and the `transcription` flag — the one CP63 counts — is never read,
 * because a metric that feels punitive makes staff hide errors instead of correcting them.
 *
 * **A closed request is never silently dropped.** The conflict code is recognised, the advice is
 * to read again rather than to press again, and the answered rows stay reachable.
 *
 * **A retry is a replay, not a second correction.** The event id is the idempotency key, it is
 * held across a failure nobody knows the outcome of, and it is forgotten after a refusal.
 */

// --- the fixtures: §4.3's height, and the physician who says it is wrong ---

const HEIGHT: CorrectionRequest = {
  id: 'request-1',
  patient_id: 'patient-1',
  visit_id: 'visit-1',
  observation_id: 'observation-1',
  code: 'BODY_HEIGHT',
  requested_at: '2026-03-04T05:02:00.000Z',
  requested_by: 'physician-1',
  requested_role: 'PHYSICIAN',
  requested_by_code: 'DR-014',
  requested_by_name_en: 'Dr Nahid Rahman',
  requested_by_name_bn: 'ডা. নাহিদ রহমান',
  reason_code: 'TRANSCRIPTION',
  note: 'The patient measures 140 on the stadiometer.',
  assigned_to: 'operator-1',
  assigned_to_code: 'OP-002',
  status: 'OPEN',
};

const request = (over: Partial<CorrectionRequest> = {}): CorrectionRequest => ({
  ...HEIGHT,
  ...over,
});

/** The observation the request points at: 150 cm, typed in centimetres at the station. */
const observation = (over: Partial<FlaggedObservation> = {}): FlaggedObservation => ({
  code: 'BODY_HEIGHT',
  category: 'ANTHRO',
  value_type: 'numeric',
  value: 150,
  unit: 'cm',
  entered_value: 150,
  entered_unit: 'cm',
  ...over,
});

const REASONS: CorrectionReason[] = [
  {
    code: 'TRANSCRIPTION',
    display_en: 'Typed differently from what was read',
    display_bn: 'যা পড়া হয়েছিল তার চেয়ে আলাদা লেখা হয়েছে',
    transcription: true,
    ordering: 1,
  },
  {
    code: 'WRONG_PATIENT',
    display_en: 'Recorded against the wrong patient',
    display_bn: 'ভুল রোগীর নামে লেখা হয়েছে',
    transcription: false,
    ordering: 2,
  },
];

const draft = (over: Partial<Draft> = {}): Draft => ({
  text: '150',
  unit: 'cm',
  bool: null,
  note: '',
  ...over,
});

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
  const mock = vi.fn(async (input: Request) => {
    calls.push({
      url: input.url,
      method: input.method,
      body: await input.clone().text(),
      requestedWith: input.headers.get('X-Requested-With'),
      idempotencyKey: input.headers.get('Idempotency-Key'),
    });
    const response = responses[Math.min(index, responses.length - 1)];
    index += 1;
    return response!.clone();
  });
  vi.stubGlobal('fetch', mock);
  return calls;
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
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
  'corrections',
);

function featureSource(file: string): string {
  return readFileSync(join(featureDir, file), 'utf8');
}

/**
 * The file with its comments stripped.
 *
 * These tests are about what the code does, not about the prose beside it. A comment saying
 * "there is no permission gate here" would otherwise fail the test that checks there is no
 * permission gate here, which would teach the next person to stop writing the comment.
 */
function featureCode(file: string): string {
  return featureSource(file)
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/.*$/gm, '$1');
}

const featureFiles = readdirSync(featureDir).filter((name) => /\.tsx?$/.test(name));

// --- the queue is the server's, in the server's order ---

describe('the queue answers oldest first, and this file never says otherwise', () => {
  it('keeps the order the server sent', () => {
    // The whole reason `/mine` sorts: a queue answered newest-first is a queue where the
    // oldest request is never answered. A sort here would be a second, quieter opinion about
    // which colleague gets ignored, applied by the client that cannot see the whole queue.
    const rows = [
      request({ id: 'a', requested_at: '2026-03-01T05:00:00.000Z' }),
      request({ id: 'b', requested_at: '2026-03-02T05:00:00.000Z' }),
      request({ id: 'c', requested_at: '2026-03-03T05:00:00.000Z' }),
    ];
    expect(queueOf(rows).open.map((row) => row.id)).toEqual(['a', 'b', 'c']);
    expect(queueOf([...rows].reverse()).open.map((row) => row.id)).toEqual(['c', 'b', 'a']);
  });

  it('never sorts, anywhere in the feature', () => {
    for (const file of featureFiles) {
      expect(/\.sort\(/.test(featureCode(file)), `${file} sorts nothing`).toBe(file === 'api.ts');
    }
    // The one exception, and it is not the queue: `api.ts` orders the *field names* on a
    // refusal so two operators reading the same 422 read the same sentence.
    expect(featureCode('api.ts')).toMatch(/localeCompare/);
  });

  it('splits the open from the answered without dropping either', () => {
    const queue = queueOf([
      request({ id: 'a' }),
      request({ id: 'b', status: 'APPLIED' }),
      request({ id: 'c', status: 'REJECTED' }),
      request({ id: 'd', status: 'OVERRIDDEN' }),
    ]);
    expect(queue.open.map((row) => row.id)).toEqual(['a']);
    expect(queue.answered.map((row) => row.id)).toEqual(['b', 'c', 'd']);
  });

  it('opens a queue of exactly one, and leaves a longer one closed', () => {
    // One request is the ordinary case, and a tap to reach the only thing on a list teaches
    // nothing. Two would put two patients' values on screen with two keypads under them.
    expect(queueOf([request({ id: 'only' })]).focus).toBe('only');
    expect(queueOf([request({ id: 'a' }), request({ id: 'b' })]).focus).toBe('');
    expect(queueOf([request({ id: 'a', status: 'APPLIED' })]).focus).toBe('');
  });

  it('is an empty queue rather than a crash when nothing has arrived', () => {
    for (const nothing of [undefined, null, []]) {
      expect(queueOf(nothing)).toEqual({ open: [], answered: [], focus: '' });
    }
  });
});

// --- what the request says: the code, the note, and the person ---

describe('a request says what was flagged, and by whom, and why', () => {
  it('reads all three off one request', () => {
    const reading = requestReadingOf(HEIGHT, REASONS, 'en');
    expect(reading.measurement).toEqual({ code: 'BODY_HEIGHT', key: 'anthropometry.field.height' });
    expect(reading.who).toBe('Dr Nahid Rahman');
    expect(reading.role).toBe('PHYSICIAN');
    expect(reading.roleKey).toBe('role.codes.PHYSICIAN');
    expect(reading.reason).toBe('Typed differently from what was read');
    expect(reading.note).toBe('The patient measures 140 on the stadiometer.');
    expect(reading.when).toEqual({ date: '2026-03-04', time: '11:02', known: true });
    expect(reading.open).toBe(true);
    expect(reading.answeredAt).toBeNull();
  });

  it('reads it in Bangla when the reader is reading Bangla', () => {
    const reading = requestReadingOf(HEIGHT, REASONS, 'bn');
    expect(reading.who).toBe('ডা. নাহিদ রহমান');
    expect(reading.reason).toBe('যা পড়া হয়েছিল তার চেয়ে আলাদা লেখা হয়েছে');
  });

  it('names the person before the code, and the code before the hat', () => {
    // A name is what a colleague asks by. A staff code identifies a person; a role identifies
    // only a hat, and "a physician says this is wrong" is a less answerable sentence than
    // "Dr Rahman says this is wrong".
    expect(flaggedBy(HEIGHT, 'en')).toBe('Dr Nahid Rahman');
    expect(
      flaggedBy(
        request({ requested_by_name_en: undefined, requested_by_name_bn: undefined }),
        'en',
      ),
    ).toBe('DR-014');
    expect(
      flaggedBy(
        request({
          requested_by_name_en: undefined,
          requested_by_name_bn: undefined,
          requested_by_code: undefined,
        }),
        'en',
      ),
    ).toBe('PHYSICIAN');
  });

  it('falls back to the other spelling rather than leaving the name blank', () => {
    expect(flaggedBy(request({ requested_by_name_bn: '' }), 'bn')).toBe('Dr Nahid Rahman');
  });

  it('leaves the person empty rather than inventing one', () => {
    // The one attribution in this system that must never name somebody who did not do the
    // thing. Empty is left empty and the screen supplies the honest word for it.
    const anonymous = request({
      requested_by_name_en: undefined,
      requested_by_name_bn: undefined,
      requested_by_code: undefined,
      requested_role: undefined,
    });
    expect(flaggedBy(anonymous, 'en')).toBe('');
    expect(requestReadingOf(anonymous, REASONS, 'en').roleKey).toBeNull();
  });

  it('never renders a uuid at anybody', () => {
    const reading = requestReadingOf(HEIGHT, REASONS, 'en');
    for (const shown of [reading.who, reading.reason, reading.measurement.code, reading.note]) {
      expect(shown).not.toContain(HEIGHT.requested_by);
      expect(shown).not.toContain(HEIGHT.assigned_to);
    }
  });

  it('shows a reason code this build has never seen as itself', () => {
    // The taxonomy is rows rather than an enum because it is expected to change. A blank where
    // the reason belongs turns "somebody says this is a transcription error" into "somebody
    // says nothing".
    expect(reasonDisplay('SOMETHING_NEW', REASONS, 'en')).toBe('SOMETHING_NEW');
    expect(reasonDisplay('TRANSCRIPTION', undefined, 'en')).toBe('TRANSCRIPTION');
    expect(reasonDisplay('TRANSCRIPTION', [], 'bn')).toBe('TRANSCRIPTION');
  });

  it('shows a role this build has never heard of as its own code', () => {
    expect(roleKeyOf('PHYSICIAN')).toBe('role.codes.PHYSICIAN');
    expect(roleKeyOf('SOMETHING_NEW')).toBeNull();
    expect(roleKeyOf('')).toBeNull();
  });

  it('says when it was asked, with the date as well as the time', () => {
    // A flag raised last Tuesday showing "11:02" and nothing else is a flag an operator reads
    // as this morning's — and this queue is exactly where the old request is the one that has
    // been waiting.
    expect(momentOf('2026-03-04T05:02:00.000Z')).toEqual({
      date: '2026-03-04',
      time: '11:02',
      known: true,
    });
    expect(momentOf('not a time')).toEqual({ date: '', time: '', known: false });
    expect(clockTime('')).toBe('');
    expect(clinicDay('')).toBe('');
  });

  it('keeps its fixed offset agreeing with the zone the rest of the app formats in', () => {
    /*
     * The third statement of the clinic's clock in this application. Two would drift silently —
     * a flag would read an hour out and nothing would look broken — so all of them are checked
     * against the named zone rather than against each other.
     */
    const zone = readFileSync(join(featureDir, '..', '..', 'lib', 'i18n.tsx'), 'utf8');
    const named = /CLINIC_TIME_ZONE = '([^']+)'/.exec(zone)?.[1];
    expect(named, 'the app names one clinic zone').toBe('Asia/Dhaka');
    for (const moment of ['2026-01-04T05:02:00.000Z', '2026-07-04T05:02:00.000Z']) {
      const parts = new Intl.DateTimeFormat('en-GB', {
        timeZone: named,
        hour: '2-digit',
        minute: '2-digit',
        hour12: false,
      }).format(new Date(moment));
      expect(clockTime(moment), `${moment} in ${named}`).toBe(parts);
    }
    expect(CLINIC_UTC_OFFSET_MINUTES).toBe(6 * 60);
  });
});

// --- criterion: the value is shown in the unit it was typed in ---

describe('the value on screen is the one the operator typed', () => {
  it('shows 150 cm rather than the canonical round trip', () => {
    const flagged = flaggedValueOf(
      observation({ value: 69.85, unit: 'kg', entered_value: 154, entered_unit: '[lb_av]' }),
    );
    expect(flagged.value).toBe(154);
    expect(flagged.unit).toBe('[lb_av]');
  });

  it('falls back to the canonical figure rather than showing none', () => {
    // A value written on the web carries no entered pair. Showing the stored figure is worse
    // than showing what was typed and far better than showing a gap.
    const flagged = flaggedValueOf(
      observation({ entered_value: undefined, entered_unit: undefined }),
    );
    expect(flagged.value).toBe(150);
    expect(flagged.unit).toBe('cm');
  });

  it('reads text, boolean and coded values without inventing a number', () => {
    expect(
      flaggedValueOf(observation({ value_type: 'text', value: undefined, value_text: 'left' })),
    ).toMatchObject({ kind: 'words', words: 'left', value: null });
    expect(
      flaggedValueOf(observation({ value_type: 'boolean', value: undefined, value_bool: false })),
    ).toMatchObject({ kind: 'yesno', bool: false });
    expect(
      flaggedValueOf(observation({ value_type: 'coded', value: undefined, value_code: 'E11.9' })),
    ).toMatchObject({ kind: 'notHere', concept: 'E11.9' });
  });

  it('knows what answering this request consists of', () => {
    expect(answerKindOf(observation())).toBe('number');
    expect(answerKindOf(observation({ value_type: 'text' }))).toBe('words');
    expect(answerKindOf(observation({ value_type: 'boolean' }))).toBe('yesno');
    expect(answerKindOf(observation({ value_type: 'structured' }))).toBe('notHere');
    expect(answerKindOf(observation({ value_type: 'something-new' }))).toBe('unknown');
    expect(answerKindOf(null)).toBe('unknown');
    expect(answerKindOf(undefined)).toBe('unknown');
  });

  it('refuses to retype a value a formula produced', () => {
    // A BMI is not typed, it is computed. Retyping one would put a hand-entered number where a
    // formula's answer belongs and leave the inputs it now disagrees with untouched — and the
    // server recomputes derived values from a corrected input by itself, so correcting the
    // height is what changes the BMI. Both markers are honoured: the category and the formula.
    expect(answerKindOf(observation({ category: 'DERIVED' }))).toBe('derived');
    expect(answerKindOf(observation({ formula: 'bmi' }))).toBe('derived');
    expect(
      applyProblem(draft({ text: '22' }), flaggedValueOf(observation({ category: 'DERIVED' }))),
    ).toBe('cannotAnswerHere');
  });

  it('still shows a derived number rather than calling it “not recorded”', () => {
    // What can be done about a value and what the record holds are two different questions, and
    // collapsing them loses the number: an operator shown "not recorded" where a flagged BMI of
    // 22.4 should be has no idea what anybody is talking about.
    const bmi = flaggedValueOf(
      observation({
        code: 'BMI',
        category: 'DERIVED',
        value: 22.4,
        unit: 'kg/m2',
        entered_value: undefined,
        entered_unit: undefined,
      }),
    );
    expect(bmi.kind).toBe('derived');
    expect(bmi.shape).toBe('number');
    expect(bmi.value).toBe(22.4);
    expect(bmi.unit).toBe('kg/m2');
    expect(shapeOf(observation({ value_type: 'structured' }))).toBe('coded');
    expect(shapeOf(undefined)).toBe('unknown');
  });

  it('seeds the form with the value under discussion, and never with somebody else’s note', () => {
    // Seeded rather than blank: §4.3's case is a transposition, so the correction is nearly
    // always an edit of what is there, and an operator retyping a whole reading from memory in
    // front of a patient will sometimes retype it wrong. The note is the corrector's own
    // sentence and starts empty — carrying the original's forward would re-file an old remark
    // as new, in somebody else's name.
    expect(draftFor(observation())).toEqual({ text: '150', unit: 'cm', bool: null, note: '' });
    expect(draftFor(observation({ value_type: 'text', value_text: 'right arm' })).text).toBe(
      'right arm',
    );
    expect(draftFor(observation({ value_type: 'boolean', value_bool: true })).bool).toBe(true);
    expect(draftFor(undefined)).toEqual({ text: '', unit: '', bool: null, note: '' });
  });

  it('offers the station’s own units for a code it knows, and only the entered one otherwise', () => {
    // A height typed in feet has to be correctable in feet. On a code this build has no table
    // for, a guessed second unit would be an invitation to record a lab result in centimetres —
    // and one unit is what makes the field draw a label instead of a selector.
    expect(unitsFor('BODY_HEIGHT', 'cm')).toEqual(['cm', 'in', '[ft_i]']);
    expect(unitsFor('SERUM_TSH', 'mIU/L')).toEqual(['mIU/L']);
    expect(unitsFor('SERUM_TSH', '')).toEqual([]);
  });

  it('names a code this build has never heard of as itself', () => {
    expect(measurementFor('SERUM_TSH')).toEqual({ code: 'SERUM_TSH', key: null });
  });

  it('has a unit table that matches the stations it was copied from', () => {
    // The copy exists because importing either station's barrel would pull a React Native
    // screen into a module that has to load under Node. This is what stops it rotting: a field
    // added to a station, or a unit changed on one, fails here rather than quietly falling back
    // to a bare code on the one screen where a wrong unit becomes a wrong correction.
    for (const field of [...ANTHRO_FIELDS, ...VITAL_FIELDS]) {
      const known = MEASUREMENTS[field.code];
      expect(known, `${field.code} is on the correction screen's table`).toBeDefined();
      expect(known?.units, `${field.code} units`).toEqual(field.units);
    }
    expect(Object.keys(MEASUREMENTS).length).toBe(ANTHRO_FIELDS.length + VITAL_FIELDS.length);
  });
});

// --- criterion: correcting is one act ---

describe('correcting is one act', () => {
  it('sends 140 cm and nothing else', () => {
    // The 140/150 case, as a body. The unit travels with the number exactly as it does from the
    // capture stations, so the server converts once and the record keeps both figures; nothing
    // is converted on this side.
    const body = toApply(draft({ text: '140' }), flaggedValueOf(observation()), 'event-1');
    expect(body).toEqual({ event_id: 'event-1', value: 140, unit: 'cm' });
  });

  it('carries the note when there is one and leaves it off when there is not', () => {
    const withNote = toApply(
      draft({ text: '140', note: '  The tape had slipped.  ' }),
      flaggedValueOf(observation()),
      'event-1',
    );
    expect(withNote).toEqual({
      event_id: 'event-1',
      value: 140,
      unit: 'cm',
      note: 'The tape had slipped.',
    });
    expect(toApply(draft({ text: '140' }), flaggedValueOf(observation()), 'event-1')).not.toEqual(
      expect.objectContaining({ note: expect.anything() }),
    );
  });

  it('never carries two kinds of value at once', () => {
    // A body with both `value` and `value_text` would leave the server to decide which the
    // operator meant, and whichever it picked would be written into a patient's record.
    const cases: [FlaggedObservation, Draft][] = [
      [observation(), draft({ text: '140' })],
      [observation({ value_type: 'text', value_text: 'left' }), draft({ text: 'right' })],
      [observation({ value_type: 'boolean', value_bool: false }), draft({ bool: true })],
    ];
    for (const [row, form] of cases) {
      const body = toApply(form, flaggedValueOf(row), 'event-1');
      const fields = Object.keys(body ?? {}).filter((key) =>
        ['value', 'value_text', 'value_bool'].includes(key),
      );
      expect(fields.length, JSON.stringify(body)).toBe(1);
    }
  });

  it('refuses a draft that is not yet an answer, and says which', () => {
    const flagged = flaggedValueOf(observation());
    expect(applyProblem(draft({ text: '' }), flagged)).toBe('empty');
    expect(applyProblem(draft({ text: '   ' }), flagged)).toBe('empty');
    expect(applyProblem(draft({ text: 'one forty' }), flagged)).toBe('notANumber');
    expect(applyProblem(draft({ text: '-4' }), flagged)).toBe('negative');
    expect(applyProblem(draft({ text: '140', note: 'x'.repeat(NOTE_MAX + 1) }), flagged)).toBe(
      'noteTooLong',
    );
    for (const bad of ['', 'one forty', '-4']) {
      expect(toApply(draft({ text: bad }), flagged, 'event-1')).toBeNull();
    }
  });

  it('refuses the value that is already there, and points at the other act', () => {
    // Applying an identical value would write a new observation the same as the old one and
    // mark the original CORRECTED — a correction that corrects nothing, on somebody's record,
    // for ever. If the number is right, the act is to say it stands and say why.
    const flagged = flaggedValueOf(observation());
    expect(applyProblem(draft({ text: '150' }), flagged)).toBe('unchanged');
    expect(applyProblem(draft({ text: '150.0' }), flagged)).toBe('unchanged');
    // The same number in a different unit is a different value, and a real correction: an
    // operator who realises the scale was reading pounds changes the unit, not the digits.
    expect(applyProblem(draft({ text: '150', unit: 'in' }), flagged)).toBeNull();
    expect(
      applyProblem(
        draft({ text: 'left' }),
        flaggedValueOf(observation({ value_type: 'text', value_text: 'left' })),
      ),
    ).toBe('unchanged');
    expect(
      applyProblem(
        draft({ bool: false }),
        flaggedValueOf(observation({ value_type: 'boolean', value_bool: false })),
      ),
    ).toBe('unchanged');
  });

  it('treats an unanswered yes/no as empty rather than as “no”', () => {
    // Null is "the operator has not chosen", which is not the same fact as "no" and must never
    // be written as one.
    const flagged = flaggedValueOf(observation({ value_type: 'boolean', value_bool: true }));
    expect(applyProblem(draft({ bool: null }), flagged)).toBe('empty');
    expect(toApply(draft({ bool: false }), flagged, 'event-1')).toEqual({
      event_id: 'event-1',
      value_bool: false,
    });
  });

  it('allows zero, which the capture form does not', () => {
    // A deliberate divergence from `anthropometry/form.ts`, which refuses zero because a weight
    // of zero gives a BMI of infinity. This screen answers a request about *any* code, and a
    // lab result of zero is a result. What stays refused is a negative measurement.
    expect(applyProblem(draft({ text: '0' }), flaggedValueOf(observation()))).toBeNull();
  });

  it('has no confirmation step and no second screen', () => {
    // "One clear action." A confirm dialog between an operator and the number they are fixing
    // is a second press for the same act, and this feature has nowhere to put one: there is no
    // function that takes a confirmation and no state that gates the write on one.
    for (const file of featureFiles) {
      expect(/confirm/i.test(featureCode(file)), `${file} confirms nothing`).toBe(false);
    }
  });
});

// --- criterion: rejecting requires a reason, and says why ---

describe('saying the value stands requires a reason', () => {
  it('refuses an empty one before it can become a request', () => {
    expect(rejectProblem('')).toBe('needsReason');
    expect(rejectProblem('   ')).toBe('needsReason');
    expect(rejectProblem('x'.repeat(REASON_MAX + 1))).toBe('reasonTooLong');
    expect(rejectProblem('Measured twice; it is 150.')).toBeNull();
  });

  it('is the only way to build a rejection, and it cannot build one without a reason', () => {
    expect(toReject('', 'event-1')).toBeNull();
    expect(toReject('   ', 'event-1')).toBeNull();
    expect(toReject('  Measured twice on the stadiometer.  ', 'event-1')).toEqual({
      event_id: 'event-1',
      reason: 'Measured twice on the stadiometer.',
    });
  });

  it('says why the reason is required, above the field rather than under it', () => {
    // This sentence is the mechanism, not a validation message. A physician told only "no"
    // cannot tell a disagreement from an oversight, learns nothing either way, and stops
    // flagging — and a clinic where nobody questions a number has an audit trail and no
    // quality signal. It has to be readable before the operator writes, not after.
    const screen = featureSource('CorrectionInbox.tsx');
    const why = screen.indexOf("t('standsWhy')");
    const field = screen.indexOf('correction-reason-');
    expect(why, 'the sentence is drawn').toBeGreaterThan(-1);
    expect(field, 'the field is drawn').toBeGreaterThan(-1);
    expect(why, 'the sentence comes before the field').toBeLessThan(field);
    for (const messages of [en, bn]) {
      expect(
        (messages as unknown as Record<string, Record<string, string>>).corrections?.standsWhy
          ?.length,
      ).toBeGreaterThan(80);
    }
  });

  it('reports a missing reason under the reason field and not under the keypad', () => {
    // One slot per act. Shared, "say why the value stands" would be drawn under the number the
    // operator has just typed, where it reads as an objection to that number — and on a value
    // that cannot be retyped at all it would have nowhere to appear.
    const screen = featureSource('CorrectionInbox.tsx');
    expect(screen).toContain('correction-stands-problem-');
    expect(screen.indexOf('correction-stands-problem-')).toBeGreaterThan(
      screen.indexOf('correction-reason-'),
    );
    expect(screen.indexOf('correction-problem-')).toBeLessThan(
      screen.indexOf('correction-stands-'),
    );
  });
});

// --- criterion: nothing here reads as blame ---

describe('nothing on this screen reads as blame', () => {
  it('wears no status tone at all', () => {
    // The plan's own risk note: a quality mechanism that feels punitive makes staff hide errors
    // instead of correcting them. A red border would be this screen's first and loudest
    // statement, and what it would state is that somebody is in trouble.
    for (const file of featureFiles) {
      const source = featureCode(file);
      expect(/status\.(critical|borderline|high|low)/.test(source), `${file} tone`).toBe(false);
      expect(/useTokens\(\)[\s\S]{0,40}status/.test(source), `${file} reads no status tone`).toBe(
        false,
      );
    }
  });

  it('never reads the flag CP63 counts', () => {
    // `transcription` on a reason is a property of a taxonomy, not a fact about a colleague.
    // Printing "this counts as a transcription error" beside somebody's own mistake is exactly
    // the metric the risk note warns about, so this feature never reads the field.
    for (const file of featureFiles) {
      expect(/\btranscription\b/.test(featureCode(file)), `${file} reads it`).toBe(false);
    }
    expect(requestReadingOf(HEIGHT, REASONS, 'en')).not.toHaveProperty('transcription');
  });

  it('counts nothing about the operator', () => {
    // No "third flag this month". A rate is a management question and this is a screen for
    // somebody with a patient in front of them.
    for (const file of featureFiles) {
      const source = featureCode(file);
      expect(/\b(streak|score|rate|tally|offend)/i.test(source), `${file} counts nothing`).toBe(
        false,
      );
    }
  });

  it('says out loud that it is not a judgement, in both languages', () => {
    for (const messages of [en, bn]) {
      const words = (messages as unknown as Record<string, Record<string, string>>).corrections
        ?.notBlame;
      expect(words?.length ?? 0).toBeGreaterThan(40);
    }
    expect(featureSource('CorrectionInbox.tsx')).toContain("t('notBlame')");
  });
});

// --- who may answer: nobody is asked to hold a permission to fix their own mistake ---

describe('answering your own request is gated on nothing', () => {
  it('has no permission check anywhere in the feature', () => {
    // The request was routed to the person who typed the value. A client-side gate would be a
    // locked control in front of the one person entitled to press it — and a control that
    // cannot work teaches an operator that the screen lies.
    for (const file of featureFiles) {
      const source = featureCode(file);
      expect(/permission/i.test(source), `${file} checks no permission`).toBe(false);
      expect(/observation\.correct\./.test(source), `${file} names no permission`).toBe(false);
    }
  });

  it('has no way to flag a value, which is a different permission and a different screen', () => {
    // Saying a value looks wrong is a clinical judgement needing `observation.correct.request`,
    // which a station operator does not hold. A call here is what a control would be wired to,
    // so its absence is structural rather than an oversight.
    for (const file of featureFiles) {
      expect(/observations\/\{id\}\/flag|\/flag'/.test(featureCode(file)), `${file}`).toBe(false);
    }
  });

  it('reads only the caller’s own queue', () => {
    for (const file of featureFiles) {
      const source = featureCode(file);
      if (!source.includes('/v1/corrections')) continue;
      expect(source).toContain('/v1/corrections/mine');
      expect(/patients\/\{id\}\/corrections/.test(source), `${file}`).toBe(false);
    }
  });
});

// --- criterion: a closed request is not silently dropped ---

describe('a request answered elsewhere is explained, not dropped', () => {
  it('recognises the conflict by the server’s code and not by its words', () => {
    expect(alreadyAnswered(trouble({ status: 409, code: CODE_ALREADY_ANSWERED }))).toBe(true);
    expect(alreadyAnswered(trouble({ status: 409, code: 'IDEMPOTENCY_KEY_REUSED' }))).toBe(false);
    expect(CODE_ALREADY_ANSWERED).toBe('CORRECTION_ALREADY_ANSWERED');
  });

  it('answers a conflict by reading again rather than by pressing again', () => {
    // Pressing again would send the same refused request for the same stale reason, and would
    // keep sending it, because this phone's reason for offering the control has not changed.
    expect(adviceFor(trouble({ status: 409, code: CODE_ALREADY_ANSWERED }))).toBe('reload');
    expect(adviceFor(trouble({ status: 404 }))).toBe('reload');
    expect(adviceFor(trouble({ kind: 'unreachable', status: 0 }))).toBe('retry');
    expect(adviceFor(trouble({ kind: 'failed', status: 503 }))).toBe('retry');
    expect(adviceFor(trouble({ status: 403 }))).toBe('none');
    expect(adviceFor(trouble({ status: 422 }))).toBe('none');
  });

  it('keeps the answered request on screen and says how it was answered', () => {
    const answered = requestReadingOf(
      request({
        status: 'APPLIED',
        resolved_at: '2026-03-04T06:20:00.000Z',
        resolution_note: 'The tape had slipped.',
        recomputed: ['BMI', 'BMR:not-recomputed'],
      }),
      REASONS,
      'en',
    );
    expect(answered.open).toBe(false);
    expect(answered.outcome).toBe('applied');
    expect(answered.answeredAt).toEqual({ date: '2026-03-04', time: '12:20', known: true });
    expect(answered.answerNote).toBe('The tape had slipped.');
    const screen = featureSource('CorrectionInbox.tsx');
    expect(screen).toContain('queue.answered');
    expect(screen).toContain("t('answeredElsewhere')");
  });

  it('keeps a supervisor’s fix as its own word', () => {
    // `OVERRIDDEN` means somebody else corrected the value. Telling an operator they put
    // something right when a supervisor did would be untrue about their own record, in the one
    // place that record is visible to them.
    expect(outcomeOf(request({ status: 'OVERRIDDEN' }))).toBe('overridden');
    expect(outcomeOf(request({ status: 'APPLIED' }))).toBe('applied');
    expect(outcomeOf(request({ status: 'REJECTED' }))).toBe('rejected');
    expect(outcomeOf(HEIGHT)).toBe('open');
    expect(new Set(OUTCOMES)).toEqual(new Set(['open', 'applied', 'rejected', 'overridden']));
  });

  it('names what could not be recomputed rather than filtering it out', () => {
    // Criterion 3, arriving on the operator's screen. A physician reading "height corrected"
    // while a stale BMI sits beside it is the disagreement the whole transaction exists to
    // prevent, and this is the one case the server cannot prevent it in.
    expect(recomputedOf(request({ recomputed: ['BMI', 'BMR:not-recomputed', '  '] }))).toEqual([
      { code: 'BMI', done: true },
      { code: 'BMR', done: false },
    ]);
    expect(recomputedOf(HEIGHT)).toEqual([]);
  });

  it('tells a refusal about the hat from a refusal about the request', () => {
    // `/mine` is gated on a *role's* permission while the queue belongs to a *person*, so an
    // operator wearing the wrong hat gets a 403 for requests that are genuinely theirs. That
    // sentence and "this one is somebody else's" send them to two different places.
    expect(notThisRole(trouble({ act: 'read', status: 403 }))).toBe(true);
    expect(notThisRole(trouble({ act: 'apply', status: 403 }))).toBe(false);
    expect(notYours(trouble({ act: 'apply', status: 403 }))).toBe(true);
    expect(notYours(trouble({ act: 'read', status: 403 }))).toBe(false);
  });
});

// --- one attempt, over a link that drops ---

describe('a retry is a replay rather than a second correction', () => {
  it('gives each act on each request its own id', () => {
    // Sharing one would make a rejection a replay of a correction, and the server would answer
    // the second press with the answer to the first.
    expect(attemptKey('apply', 'request-1')).not.toBe(attemptKey('reject', 'request-1'));
    expect(attemptKey('apply', 'request-1')).not.toBe(attemptKey('apply', 'request-2'));
  });

  it('re-sends the id an attempt was first made with', () => {
    const key = attemptKey('apply', 'request-1');
    expect(eventFor({}, key, 'minted')).toBe('minted');
    expect(eventFor(holdEvent({}, key, 'first'), key, 'minted')).toBe('first');
    expect(eventFor(releaseEvent(holdEvent({}, key, 'first'), key), key, 'minted')).toBe('minted');
  });

  it('keeps the id only while nobody knows what happened', () => {
    // A refusal forgets it: nothing was written, and whatever the operator does next is a new
    // act. Reusing the old id would have the server hand back the answer to a request nobody
    // is making any more.
    expect(keepsItsEvent(trouble({ kind: 'unreachable', status: 0 }))).toBe(true);
    expect(keepsItsEvent(trouble({ kind: 'failed', status: 500 }))).toBe(true);
    expect(keepsItsEvent(trouble({ kind: 'refused', status: 409 }))).toBe(false);
  });

  it('mints a uuid in exactly one place', () => {
    // Anywhere else and two attempts at one act would go out with two ids, which is two
    // corrections in an append-only ledger.
    const minting = featureFiles.filter((file) => /randomUUID/.test(featureCode(file)));
    expect(minting).toEqual(['CorrectionInbox.tsx']);
    expect(featureCode('CorrectionInbox.tsx').match(/randomUUID/g)?.length).toBe(1);
  });
});

// --- being told, on the device that typed the value ---

describe('the author is told on their own device', () => {
  it('listens on the operator’s own channel and nobody else’s', () => {
    expect(myTopic('operator-1')).toBe('user:operator-1');
    expect(myTopic('  ')).toBe('');
  });

  it('reacts to a flag for this reader and to nothing else', () => {
    const flag = { kind: CORRECTION_REQUESTED, topic: 'user:operator-1' };
    expect(notifiesMe(flag, 'operator-1')).toBe(true);
    // The kind alone would react to somebody else's flag if a future gateway ever fanned one
    // out more widely; the topic alone would react to this person's critical-value alerts.
    expect(notifiesMe(flag, 'operator-2')).toBe(false);
    expect(notifiesMe({ kind: 'alert.raised', topic: 'user:operator-1' }, 'operator-1')).toBe(
      false,
    );
    expect(notifiesMe(flag, '')).toBe(false);
    expect(notifiesMe({}, 'operator-1')).toBe(false);
  });

  it('turns a message into a re-read and never into a value on screen', () => {
    // A value written into the cache from a socket is a value no endpoint returned. The
    // gateway's summary deliberately carries no number, and this is what keeps it that way:
    // the only thing the listener does with a message is invalidate.
    const notice = featureCode('CorrectionNotice.tsx');
    expect(notice).toContain('invalidateQueries');
    expect(/setQueryData|summary\s*[.[]/.test(notice)).toBe(false);
  });

  it('is a count and a way to look, never a clinical value', () => {
    // The notice sits in the application shell, so it is on screen wherever the operator is —
    // which is also why it must not name a patient or a number.
    const notice = featureCode('CorrectionNotice.tsx');
    expect(/EnteredBy|MeasurementField|DualUnitValue|value_text|entered_value/.test(notice)).toBe(
      false,
    );
    expect(notice).toContain("t('notice'");
  });

  it('is reachable from wherever the operator happens to be', () => {
    const shell = readFileSync(
      join(featureDir, '..', '..', 'components', 'ScreenShell.tsx'),
      'utf8',
    );
    expect(shell, 'the shell draws the notice').toContain('<CorrectionNotice />');
  });
});

// --- the calls themselves ---

describe('the calls are the contract’s, and carry what a write must carry', () => {
  it('reads the caller’s own queue, open by default', async () => {
    const calls = stubFetch(json({ requests: [HEIGHT] }));
    await listMyCorrections(false);
    expect(calls[0]?.url).toContain('/v1/corrections/mine');
    expect(calls[0]?.url).not.toContain('all=');
    await listMyCorrections(true);
    expect(calls[1]?.url).toContain('all=');
  });

  it('reads the vocabulary and the flagged value', async () => {
    const calls = stubFetch(json({ reasons: REASONS }), json({ observation: observation() }));
    await listCorrectionReasons();
    expect(calls[0]?.url).toContain('/v1/corrections/reasons');
    await getFlaggedObservation('observation-1');
    expect(calls[1]?.url).toContain('/v1/observations/observation-1');
  });

  it('sends the correction with the forgery guard and its own id as the idempotency key', async () => {
    const calls = stubFetch(json({ request: request({ status: 'APPLIED' }) }));
    await applyCorrection('request-1', { event_id: 'event-1', value: 140, unit: 'cm' });
    expect(calls[0]?.method).toBe('POST');
    expect(calls[0]?.url).toContain('/v1/corrections/request-1/apply');
    expect(calls[0]?.requestedWith).toBe('DTHCMS');
    // The same value in the body and on the header: that is what makes a retry a replay.
    expect(calls[0]?.idempotencyKey).toBe('event-1');
    expect(JSON.parse(calls[0]?.body ?? '{}')).toEqual({
      event_id: 'event-1',
      value: 140,
      unit: 'cm',
    });
  });

  it('sends the refusal with its reason', async () => {
    const calls = stubFetch(json({ request: request({ status: 'REJECTED' }) }));
    await rejectCorrection('request-1', { event_id: 'event-2', reason: 'Measured twice.' });
    expect(calls[0]?.url).toContain('/v1/corrections/request-1/reject');
    expect(calls[0]?.idempotencyKey).toBe('event-2');
    expect(JSON.parse(calls[0]?.body ?? '{}')).toEqual({
      event_id: 'event-2',
      reason: 'Measured twice.',
    });
  });

  it('logs nothing, and holds no credential', async () => {
    for (const file of featureFiles) {
      const source = featureCode(file);
      expect(/console\./.test(source), `${file} logs nothing`).toBe(false);
      expect(/localStorage|sessionStorage|AsyncStorage|SecureStore/.test(source), file).toBe(false);
      expect(/access_token|refresh_token|Authorization/.test(source), file).toBe(false);
    }
  });
});

// --- what went wrong ---

describe('a refusal is shown in the server’s own words', () => {
  it('reports a request that never left the tablet as unreachable', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Network request failed')));
    const error = await listMyCorrections(false).catch((thrown: unknown) => thrown);
    expect(troubleOf(error, 'en', 'read')).toEqual({
      kind: 'unreachable',
      act: 'read',
      status: 0,
      code: '',
      field: '',
      message: '',
    });
  });

  it('names the field the server named, in the reader’s language', () => {
    const refused = troubleOf(
      refusal({
        status: 422,
        fields: { value: 'That height is not plausible.' },
        fieldsBN: { value: 'ওই উচ্চতা যুক্তিসঙ্গত নয়।' },
      }),
      'bn',
      'apply',
    );
    expect(refused.kind).toBe('refused');
    expect(refused.field).toBe('value');
    expect(refused.message).toBe('ওই উচ্চতা যুক্তিসঙ্গত নয়।');
  });

  it('carries the server’s code through untouched', () => {
    // The only part of an error a client may branch on. A message gets translated, shortened
    // and improved, and a screen that matched on its words would change behaviour the day
    // somebody rewrote a sentence.
    const conflict = troubleOf(
      refusal({ status: 409, code: CODE_ALREADY_ANSWERED }),
      'en',
      'apply',
    );
    expect(conflict).toMatchObject({ kind: 'refused', code: CODE_ALREADY_ANSWERED, status: 409 });
    expect(alreadyAnswered(conflict)).toBe(true);
  });

  it('separates a refusal from a failure', () => {
    for (const status of [403, 404, 409]) {
      expect(troubleOf(refusal({ status }), 'en', 'apply').kind).toBe('refused');
    }
    expect(troubleOf(refusal({ status: 500 }), 'en', 'apply').kind).toBe('failed');
    expect(troubleOf(new Error('something else'), 'en', 'apply')).toEqual({
      kind: 'failed',
      act: 'apply',
      status: 0,
      code: '',
      field: '',
      message: '',
    });
  });
});

// --- every sentence this feature can produce exists in both languages ---

describe('every message key this feature can produce exists in both files', () => {
  type Tree = Record<string, unknown>;

  function has(tree: Tree, key: string): boolean {
    let node: unknown = tree;
    for (const part of key.split('.')) {
      if (node === null || typeof node !== 'object') return false;
      node = (node as Tree)[part];
    }
    return typeof node === 'string' && node.length > 0;
  }

  const dynamic = [
    ...PROBLEMS.map((problem) => `corrections.problem.${problem}`),
    ...ADVICE.map((advice) => `corrections.advice.${advice}`),
    ...OUTCOMES.map((outcome) => `corrections.outcome.${outcome}`),
    ...['refused', 'unreachable', 'failed'].map((kind) => `corrections.trouble.${kind}`),
    ...ANSWER_KINDS.filter((kind) => !['number', 'words', 'yesno'].includes(kind)).map(
      (kind) => `corrections.cannot.${kind}`,
    ),
    ...KNOWN_ROLE_CODES.map((code) => `role.codes.${code}`),
    ...Object.values(MEASUREMENTS).flatMap((measurement) =>
      measurement.key === null ? [] : [measurement.key],
    ),
  ];

  it('has a sentence for every key built from a value rather than written out', () => {
    // The keys `i18n.test.ts` cannot see: they are assembled from a constant at runtime, so a
    // missing one is a raw identifier drawn at an operator rather than a build failure.
    for (const key of dynamic) {
      expect(has(en as Tree, key), `${key} in English`).toBe(true);
      expect(has(bn as Tree, key), `${key} in Bangla`).toBe(true);
    }
  });

  it('names exactly the roles the message files name', () => {
    // The list is a copy of the message file's own keys, and this is what stops it rotting: a
    // role added to the catalogue without a word here would be drawn as `RX_EDUCATOR` on the
    // one screen where a colleague's identity is the point.
    const roles = Object.keys((en as Tree).role as Tree).includes('codes')
      ? Object.keys(((en as Tree).role as Tree).codes as Tree).filter((code) => code !== 'null')
      : [];
    expect([...KNOWN_ROLE_CODES].sort()).toEqual([...roles].sort());
    const bangla = Object.keys(((bn as Tree).role as Tree).codes as Tree).filter(
      (code) => code !== 'null',
    );
    expect([...KNOWN_ROLE_CODES].sort()).toEqual([...bangla].sort());
  });

  it('has a screen title for the queue’s own route', () => {
    expect(has(en as Tree, 'screen.corrections')).toBe(true);
    expect(has(bn as Tree, 'screen.corrections')).toBe(true);
  });
});

// --- the shapes the tests above lean on ---

/** A refusal as the server sends one, with the parts a test varies spelled out. */
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

/** A trouble as the screen holds one, with only the parts a test cares about spelled out. */
function trouble(over: Partial<Trouble> & { status: number }): Trouble {
  return {
    kind: 'refused',
    act: 'apply',
    code: '',
    field: '',
    message: '',
    ...over,
  };
}
