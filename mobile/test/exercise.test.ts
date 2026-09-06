import { readFileSync, readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { afterEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '@dthcms/api-client';

import en from '../src/messages/en.json';
import bn from '../src/messages/bn.json';
import { ofExerciseAssessment, ofExercisePlan } from '../src/features/attribution/state';
import {
  ADVICE,
  ATTEMPTS,
  CODE_CONTRAINDICATED,
  CODE_NO_ASSESSMENT,
  CODE_RETIRED,
  CODE_SUPERSEDED,
  EXCLUSION_STATUSES,
  MINUTES_PER_SESSION,
  REVIEWS,
  STEPS,
  TARGET_PROBLEMS,
  TIMES_PER_WEEK,
  WALKS_ANSWERS,
  WALK_MINUTES,
  adviceFor,
  answerCondition,
  answerWalks,
  askedOf,
  assessmentReading,
  chooseExercise,
  conditionsOf,
  contraindicated,
  duplicated,
  emptyAssessment,
  emptyStage,
  exclusionReading,
  incomplete,
  isChosen,
  keepOffered,
  namesExercise,
  namesOf,
  noAssessment,
  noTargets,
  offerRows,
  planReading,
  questionRows,
  questionsMissing,
  readsAsFailure,
  refusedRow,
  reviewFor,
  serverSpoke,
  stepFor,
  retired,
  superseded,
  targetFor,
  targetProblem,
  targetProblems,
  toAssessmentBody,
  toPlanBody,
  troubleKey,
  typeJointPain,
  typeMinutes,
  typeTimes,
  typeWalkMinutes,
  unanswered,
  unapproved,
  walkMinutesProblem,
  wordingOf,
  type Assessment,
  type AssessmentDraft,
  type Contraindication,
  type Exclusion,
  type ExclusionStatus,
  type Exercise,
  type Options,
  type Plan,
  type Trouble,
} from '../src/features/exercise/state';

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

const exerciseApi = await import('../src/features/exercise/api');
const {
  issuePlan,
  listContraindications,
  readExerciseRecord,
  readOptions,
  recordAssessment,
  troubleOf,
} = exerciseApi;

/**
 * Station 8's exercise assessment and plan (CP60, §3 step 8, §12.1).
 *
 * The screen is a React Native component and is judged in a clinic room with a patient in the
 * chair. What is checked here is every decision behind it, and one of them matters more than
 * everything else on this page.
 *
 * **The client never receives a contraindicated exercise, and nothing in the feature behaves as
 * though it might.** `offerRows` returns the payload's own array in the payload's own order; no
 * file in the feature contains a filter over `exercises`, an exercise code, or any sign of a
 * route that would return the library; and `toPlanBody` refuses to build a body for a code the
 * server did not offer. The excluded rows are not on the device to be shown, hidden, flagged or
 * revealed by a "show all" — which is exactly what makes the guarantee stronger than a careful
 * screen.
 *
 * **The exclusion is shown, and it names no exercise.** The count is the server's — copied, never
 * `library_size` minus what is offered and never the sum of the per-condition figures, which
 * overlap — and the reasons are conditions.
 *
 * **A target is two numbers or it is not a target.** `toPlanBody` is null while either is
 * missing, so the button cannot move; §12.1's adherence analysis reads those two integers.
 *
 * **An assessment says what it asked, not only what it found.** `asked` and `contraindications`
 * travel together, every question has to be answered before either can be sent, and no finding is
 * ever recorded about a question that was not put — invariant 89, kept on this side as well.
 *
 * **An unasked question is not a diagnosis.** A `NOT_ASKED` exclusion gets its own sentence in
 * both languages, and it never says the patient has the condition.
 *
 * **A 409 saying no assessment has been recorded is a step, not a failure.** It routes to the
 * questions, it is not drawn as a refusal, and the advice is not "try again" — which could never
 * work. So is a 422 carrying `missing_conditions`: that one means this tablet's catalogue is
 * older than the clinic's, and the way on is to read the questions again, with the new ones
 * marked.
 *
 * **A conflict means the list moved; a 422 means the request was wrong.** `EXERCISE_RETIRED`,
 * `EXERCISE_ASSESSMENT_SUPERSEDED` and `EXERCISE_NO_ASSESSMENT` are answered by reading the
 * options again. Every other target refusal names its exercise in `fields.exercise_code` and is
 * drawn on that row. Nothing here reads a status and guesses which it was.
 *
 * **Nothing here multiplies a target into a weekly total.** `planReading` copies the server's
 * `minutes_per_week` even when it disagrees with the two numbers beside it.
 */

// --- the catalogue and the library, as migration 00047 seeds them ---

const CONDITIONS: Contraindication[] = [
  {
    code: 'SEVERE_NEUROPATHY',
    name_en: 'Severe peripheral neuropathy',
    name_bn: 'তীব্র পেরিফেরাল নিউরোপ্যাথি',
    question_en:
      'Has the patient lost protective sensation — no feeling at one or more monofilament sites?',
    question_bn:
      'রোগী কি সুরক্ষামূলক অনুভূতি হারিয়েছেন — মনোফিলামেন্টের এক বা একাধিক জায়গায় অনুভূতি নেই?',
    ordering: 10,
  },
  {
    code: 'PROLIFERATIVE_RETINOPATHY',
    name_en: 'Proliferative retinopathy',
    name_bn: 'প্রলিফারেটিভ রেটিনোপ্যাথি',
    question_en:
      'Has an eye examination found new vessels, or has the patient had laser treatment?',
    question_bn: 'চোখের পরীক্ষায় কি নতুন রক্তনালি পাওয়া গেছে, বা লেজার চিকিৎসা হয়েছে?',
    ordering: 20,
  },
  {
    code: 'CARDIAC_LIMITATION',
    name_en: 'Cardiac limitation',
    name_bn: 'হৃদযন্ত্রের সীমাবদ্ধতা',
    question_en: 'Does the patient get chest pain or unusual breathlessness on exertion?',
    question_bn: 'পরিশ্রম করলে কি বুকে ব্যথা বা অস্বাভাবিক শ্বাসকষ্ট হয়?',
    ordering: 30,
  },
  {
    code: 'ACTIVE_FOOT_ULCER',
    name_en: 'Active foot ulcer',
    name_bn: 'পায়ে সক্রিয় ঘা',
    question_en: 'Is there an open ulcer or a wound on either foot today?',
    question_bn: 'আজ কোনও পায়ে কি খোলা ঘা বা ক্ষত আছে?',
    ordering: 40,
  },
  {
    code: 'UNCONTROLLED_HYPERTENSION',
    name_en: 'Uncontrolled blood pressure',
    name_bn: 'অনিয়ন্ত্রিত রক্তচাপ',
    question_en: 'Is the blood pressure above 180/110 today?',
    question_bn: 'আজ রক্তচাপ কি ১৮০/১১০-এর বেশি?',
    ordering: 50,
  },
];

/** Every code the seeded library holds. Named here so the feature can be checked for them. */
const LIBRARY_CODES = [
  'WALK_FLAT',
  'WALK_BRISK',
  'CYCLE_STATIONARY',
  'SWIM',
  'JOG',
  'SKIPPING',
  'STAIRS',
  'CHAIR_STAND',
  'BAND_ROW',
  'HEAVY_LIFT',
  'SEATED_STRETCH',
  'STANDING_BALANCE',
];

const exercise = (over: Partial<Exercise> & { code: string }): Exercise => ({
  name_en: over.code,
  name_bn: `${over.code} বাংলায়`,
  how_en: `How to do ${over.code}.`,
  how_bn: `${over.code} কীভাবে করবেন।`,
  kind: 'AEROBIC',
  intensity: 'MODERATE',
  impact: 'LOW',
  needs_equipment: false,
  can_do_at_home: true,
  // False on everything seeded: the library content is an open clinical decision.
  approved: false,
  ordering: 10,
  ...over,
});

const walkFlat = (): Exercise =>
  exercise({
    code: 'WALK_FLAT',
    name_en: 'Walking on level ground',
    name_bn: 'সমতল জায়গায় হাঁটা',
    how_en:
      'Walk at a pace where you can still talk but not sing. Wear closed shoes that fit, and check both feet afterwards.',
    how_bn:
      'এমন গতিতে হাঁটুন যাতে কথা বলা যায় কিন্তু গান গাওয়া যায় না। মাপমতো ঢাকা জুতা পরুন, আর হাঁটার পরে দুই পা দেখে নিন।',
    ordering: 10,
  });

const chairStand = (): Exercise =>
  exercise({
    code: 'CHAIR_STAND',
    name_en: 'Standing up from a chair',
    name_bn: 'চেয়ার থেকে ওঠা',
    how_en:
      'Sit on a firm chair and stand up without using your hands, ten times. Rest, and repeat.',
    how_bn: 'শক্ত চেয়ারে বসে হাত না লাগিয়ে দশবার উঠে দাঁড়ান। বিশ্রাম নিয়ে আবার করুন।',
    kind: 'RESISTANCE',
    ordering: 80,
  });

const seatedStretch = (): Exercise =>
  exercise({
    code: 'SEATED_STRETCH',
    name_en: 'Seated stretches',
    name_bn: 'বসে শরীর টানটান করা',
    how_en:
      'Sitting on a chair, reach slowly towards your toes and hold while you count to twenty. Do not bounce.',
    how_bn:
      'চেয়ারে বসে ধীরে পায়ের আঙুলের দিকে হাত বাড়ান, কুড়ি গোনা পর্যন্ত ধরে রাখুন। ঝাঁকাবেন না।',
    kind: 'FLEXIBILITY',
    intensity: 'LOW',
    impact: 'NONE',
    ordering: 110,
  });

/**
 * What the server sends for a patient with severe neuropathy.
 *
 * Nine of the twelve, and the three that jar an insensate foot — jogging, skipping, standing on
 * one leg — are **not in this payload at all**. That is not this fixture being tidy: it is the
 * shape of the real response, and every test below reads it as the whole world.
 */
const options = (over: Partial<Options> = {}): Options => ({
  assessment_id: 'assessment-1',
  exercises: [
    walkFlat(),
    exercise({ code: 'WALK_BRISK', intensity: 'VIGOROUS', ordering: 20 }),
    exercise({
      code: 'CYCLE_STATIONARY',
      needs_equipment: true,
      can_do_at_home: false,
      ordering: 30,
    }),
    exercise({ code: 'SWIM', needs_equipment: true, can_do_at_home: false, ordering: 40 }),
    exercise({ code: 'STAIRS', ordering: 70 }),
    chairStand(),
    exercise({ code: 'BAND_ROW', kind: 'RESISTANCE', needs_equipment: true, ordering: 90 }),
    exercise({ code: 'HEAVY_LIFT', kind: 'RESISTANCE', intensity: 'VIGOROUS', ordering: 100 }),
    seatedStretch(),
  ],
  library_size: 12,
  excluded: 3,
  reasons: [neuropathy(3)],
  ...over,
});

/**
 * One exclusion reason.
 *
 * `status` is required rather than defaulted, so a fixture cannot quietly claim a patient has a
 * condition nobody asked them about — which is the exact confusion the field exists to prevent.
 */
const reason = (
  code: string,
  nameEN: string,
  nameBN: string,
  status: ExclusionStatus,
  excluded: number,
): Exclusion => ({ code, name_en: nameEN, name_bn: nameBN, status, excluded });

const neuropathy = (excluded: number, status: ExclusionStatus = 'APPLIES'): Exclusion =>
  reason(
    'SEVERE_NEUROPATHY',
    'Severe peripheral neuropathy',
    'তীব্র পেরিফেরাল নিউরোপ্যাথি',
    status,
    excluded,
  );

const footUlcer = (excluded: number, status: ExclusionStatus = 'APPLIES'): Exclusion =>
  reason('ACTIVE_FOOT_ULCER', 'Active foot ulcer', 'পায়ে সক্রিয় ঘা', status, excluded);

const cardiac = (excluded: number, status: ExclusionStatus = 'APPLIES'): Exclusion =>
  reason('CARDIAC_LIMITATION', 'Cardiac limitation', 'হৃদযন্ত্রের সীমাবদ্ধতা', status, excluded);

const assessment = (over: Partial<Assessment> = {}): Assessment => ({
  id: 'assessment-1',
  patient_id: 'patient-1',
  contraindications: ['SEVERE_NEUROPATHY'],
  // Every live condition was put to the patient. Without this the row is indistinguishable from
  // one taken by a station that asked two questions and skipped three.
  asked: CONDITIONS.map((condition) => condition.code),
  status: 'ACTIVE',
  recorded_at: '2026-09-05T04:30:00Z',
  recorded_by: 'staff-1',
  recorded_role: 'EXERCISE_SPECIALIST',
  station_code: 'STN_EXERCISE',
  source: 'MOBILE_ONLINE',
  ...over,
});

const plan = (over: Partial<Plan> = {}): Plan => ({
  id: 'plan-1',
  patient_id: 'patient-1',
  assessment_id: 'assessment-1',
  status: 'ACTIVE',
  items: [
    {
      exercise_code: 'WALK_FLAT',
      times_per_week: 5,
      minutes_per_session: 30,
      ordering: 1,
      name_en: 'Walking on level ground',
      name_bn: 'সমতল জায়গায় হাঁটা',
      how_en: 'Walk at a pace where you can still talk but not sing.',
      how_bn: 'এমন গতিতে হাঁটুন যাতে কথা বলা যায় কিন্তু গান গাওয়া যায় না।',
      needs_equipment: false,
      can_do_at_home: true,
      approved: false,
      minutes_per_week: 150,
    },
    {
      exercise_code: 'CHAIR_STAND',
      times_per_week: 3,
      minutes_per_session: 10,
      ordering: 2,
      name_en: 'Standing up from a chair',
      name_bn: 'চেয়ার থেকে ওঠা',
      how_en: 'Sit on a firm chair and stand up without using your hands, ten times.',
      how_bn: 'শক্ত চেয়ারে বসে হাত না লাগিয়ে দশবার উঠে দাঁড়ান।',
      needs_equipment: false,
      can_do_at_home: true,
      approved: false,
      minutes_per_week: 30,
    },
  ],
  issued_at: '2026-09-05T04:45:00Z',
  issued_by: 'staff-2',
  issued_role: 'EXERCISE_SPECIALIST',
  station_code: 'STN_EXERCISE',
  source: 'MOBILE_ONLINE',
  minutes_per_week: 180,
  ...over,
});

const ids = { event: 'event-1', patient: 'patient-1', visit: 'visit-1' };

/** All five questions answered, none of them applying. */
function allAnsweredNo(): AssessmentDraft {
  return CONDITIONS.reduce(
    (draft, condition) => answerCondition(draft, condition.code, 'no'),
    emptyAssessment(),
  );
}

// --- the network ---

function respond(body: unknown, init: { status?: number } = {}) {
  return new Response(JSON.stringify(body), {
    status: init.status ?? 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

function refusal(status: number, code: string, messageEN: string, messageBN: string) {
  return respond(
    { error: { code, kind: 'conflict', message: messageEN, message_bn: messageBN } },
    {
      status,
    },
  );
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
  'exercise',
);

/**
 * The file with its comments stripped.
 *
 * These tests are about what the code does, not the prose beside it. A comment saying "there is
 * no filter over exercises here" would otherwise fail the test that checks there is no filter
 * over exercises, which would teach the next person to stop writing the comment.
 */
function featureCode(file: string): string {
  return readFileSync(join(featureDir, file), 'utf8')
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/.*$/gm, '$1');
}

const featureFiles = readdirSync(featureDir);

/** A message file as a flat map of dotted key to sentence. */
function flatten(tree: Record<string, unknown>, prefix = ''): Map<string, string> {
  const out = new Map<string, string>();
  for (const [key, value] of Object.entries(tree)) {
    const path = prefix ? `${prefix}.${key}` : key;
    if (value !== null && typeof value === 'object') {
      for (const [k, v] of flatten(value as Record<string, unknown>, path)) out.set(k, v);
    } else out.set(path, String(value));
  }
  return out;
}

// --- criterion 1: what is on the screen is what the server sent, and nothing else ---

describe('the permitted set is rendered exactly as it arrived', () => {
  it('returns the payload’s own rows, in the payload’s own order', () => {
    const payload = options();
    const rows = offerRows(payload, 'en');
    expect(rows.map((row) => row.code)).toEqual(payload.exercises.map((one) => one.code));
    expect(rows).toHaveLength(9);
  });

  it('has no sign of the three the server excluded', () => {
    // The point of the fixture: jogging, skipping and standing on one leg are not in the
    // response, so there is nothing for a screen to be careful about.
    const rows = offerRows(options(), 'en');
    const drawn = JSON.stringify(rows);
    for (const code of ['JOG', 'SKIPPING', 'STANDING_BALANCE']) {
      expect(
        rows.some((row) => row.code === code),
        code,
      ).toBe(false);
      expect(drawn.includes(code), code).toBe(false);
    }
  });

  it('answers an empty permitted set with an empty list rather than with a library', () => {
    // A patient for whom nothing is safe today is a real answer. The screen says so; it does
    // not fall back to anything, because there is nothing to fall back to.
    expect(offerRows(options({ exercises: [], excluded: 12 }), 'en')).toEqual([]);
    expect(offerRows(null, 'en')).toEqual([]);
    expect(offerRows(undefined, 'en')).toEqual([]);
  });

  it('holds no exercise of its own, anywhere in the feature', () => {
    // The failure this guards against is a screen with a copy of the library on it — the one
    // thing acceptance criterion 1 forbids, and the one a reviewer cannot see by reading a list.
    for (const file of featureFiles) {
      const source = featureCode(file);
      for (const code of LIBRARY_CODES) {
        expect(source.includes(code), `${file} names ${code}`).toBe(false);
      }
    }
  });

  it('has no filter, sort or slice over the exercises the server sent', () => {
    // Every way a list gets quietly shortened. `offerRows` maps and does nothing else, and
    // nothing else in the feature touches the array at all — `unapproved` counts with a loop
    // rather than a `filter().length`, so this assertion has no sanctioned exception to erode.
    for (const file of featureFiles) {
      const source = featureCode(file);
      expect(/exercises\s*\.\s*(filter|slice|splice)/.test(source), file).toBe(false);
      expect(/contraindicated\s*[:=]\s*(true|false)/.test(source), file).toBe(false);
      expect(/showAll|show_all|revealAll/i.test(source), file).toBe(false);
    }
  });

  it('knows no route that would return the library', () => {
    // There is no such endpoint in the contract, and this is what stops one being invented —
    // by a helper here, or by a hand-built path that bypasses the generated client.
    const source = featureFiles.map(featureCode).join('\n');
    expect(/\/v1\/exercises\b/.test(source)).toBe(false);
    expect(/\/v1\/exercise\/library/.test(source)).toBe(false);
    const paths = [...source.matchAll(/'(\/v1\/[^']+)'/g)].map((match) => match[1]).sort();
    expect(paths).toEqual([
      '/v1/exercise/assessments',
      '/v1/exercise/contraindications',
      '/v1/exercise/plans',
      '/v1/patients/{id}/exercise',
      '/v1/patients/{id}/exercise/options',
    ]);
  });

  it('asks for the options with no query at all', async () => {
    const calls = stubFetch(respond(options()));
    const held = await readOptions('patient-1');
    expect(held.exercises).toHaveLength(9);
    const url = new URL(calls[0]!.url);
    expect(url.pathname).toBe('/v1/patients/patient-1/exercise/options');
    // No `include_excluded`, no `all`, no filter of any kind. The contract has none, and this
    // is what stops one being added quietly.
    expect(url.search).toBe('');
  });

  it('counts the unapproved rows without dropping any of them', () => {
    expect(unapproved(options())).toBe(9);
    expect(unapproved(null)).toBe(0);
    const mixed = options({ exercises: [walkFlat(), { ...chairStand(), approved: true }] });
    expect(unapproved(mixed)).toBe(1);
    // Counted, never filtered: an unapproved starter list is still the list the clinic has.
    expect(offerRows(mixed, 'en')).toHaveLength(2);
  });
});

// --- what is sent instead of the excluded rows ---

describe('the exclusion is shown, by condition, and names no exercise', () => {
  it('reports the server’s count and the size of the library', () => {
    const reading = exclusionReading(options(), 'en')!;
    expect(reading.excluded).toBe(3);
    expect(reading.librarySize).toBe(12);
    expect(reading.shown).toBe(9);
  });

  it('copies `excluded` rather than subtracting anything', () => {
    // A payload where the two disagree — a row retired between the count and the query, say.
    // The server's figure is the one on the screen; a client that computed its own would be a
    // second answer to the same question, on the same screen, about the same patient.
    const odd = options({
      library_size: 12,
      excluded: 3,
      exercises: options().exercises.slice(0, 8),
    });
    const reading = exclusionReading(odd, 'en')!;
    expect(reading.excluded).toBe(3);
    expect(reading.shown).toBe(8);
    expect(reading.excluded).not.toBe(reading.librarySize - reading.shown);
  });

  it('does not add up the per-condition counts, which overlap', () => {
    // Jogging is excluded by neuropathy *and* by an open ulcer, so each reason is true on its
    // own and the counts do not sum to the total.
    const both = options({ excluded: 6, reasons: [neuropathy(3), footUlcer(6)] });
    const reading = exclusionReading(both, 'en')!;
    const sum = reading.reasons.reduce((total, reason) => total + reason.excluded, 0);
    expect(sum).toBe(9);
    expect(reading.excluded).toBe(6);
  });

  it('names conditions and never an exercise', () => {
    const reading = exclusionReading(options(), 'en')!;
    expect(reading.reasons.map((reason) => reason.code)).toEqual(['SEVERE_NEUROPATHY']);
    const drawn = JSON.stringify(reading);
    for (const code of LIBRARY_CODES) {
      expect(drawn.includes(code), code).toBe(false);
    }
    // And there is nothing on the reading that could hold one: a reason is a code, a name, a
    // count and which kind of reason it is.
    for (const reason of reading.reasons) {
      expect(Object.keys(reason).sort()).toEqual(['code', 'excluded', 'name', 'status']);
    }
  });

  it('says nothing at all when nothing was excluded', () => {
    // "0 of 12 not shown" is a banner people learn to read past, and the next one they read
    // past is the one that mattered.
    expect(exclusionReading(options({ excluded: 0, reasons: [] }), 'en')).toBeNull();
    expect(exclusionReading(null, 'en')).toBeNull();
  });

  it('still says how many when the server gave a count and no reason', () => {
    // A retired exercise leaves a count with nothing to name. The count is still true.
    const reading = exclusionReading(options({ excluded: 1, reasons: [] }), 'en')!;
    expect(reading.excluded).toBe(1);
    expect(reading.reasons).toEqual([]);
  });
});

// --- a finding about this patient, and a question nobody has put ---

describe('an unasked question is not a diagnosis', () => {
  /** A condition added to the catalogue after this patient's assessment was taken. */
  const grown = () =>
    options({
      excluded: 5,
      reasons: [neuropathy(3), cardiac(2, 'NOT_ASKED')],
    });

  it('carries the server’s status rather than working one out', () => {
    const reading = exclusionReading(grown(), 'en')!;
    expect(reading.reasons.map((one) => [one.code, one.status])).toEqual([
      ['SEVERE_NEUROPATHY', 'APPLIES'],
      ['CARDIAC_LIMITATION', 'NOT_ASKED'],
    ]);
    expect(EXCLUSION_STATUSES).toEqual(['APPLIES', 'NOT_ASKED']);
  });

  it('counts the questions nobody has put, so one line can answer all of them', () => {
    expect(exclusionReading(grown(), 'en')!.unasked).toBe(1);
    // A patient whose assessment covered the whole catalogue has none of them.
    expect(exclusionReading(options(), 'en')!.unasked).toBe(0);
    expect(
      exclusionReading(
        options({ excluded: 6, reasons: [neuropathy(3, 'NOT_ASKED'), cardiac(3, 'NOT_ASKED')] }),
        'en',
      )!.unasked,
    ).toBe(2);
  });

  it('keeps the server’s order rather than grouping the two kinds', () => {
    // The payload's order is the catalogue's, which is the order the questions were put in. A
    // screen that regrouped them would show its reasons in a different order from its questions.
    const mixed = exclusionReading(
      options({ excluded: 7, reasons: [cardiac(2, 'NOT_ASKED'), neuropathy(3), footUlcer(4)] }),
      'en',
    )!;
    expect(mixed.reasons.map((one) => one.code)).toEqual([
      'CARDIAC_LIMITATION',
      'SEVERE_NEUROPATHY',
      'ACTIVE_FOOT_ULCER',
    ]);
  });

  it('has a different sentence for each kind, in both languages', () => {
    // The whole reason the status exists. "Not shown because of X" says the patient has X;
    // "not shown until the question about X has been asked" says nobody has asked. Drawing the
    // second in the first's words would put a diagnosis on the screen that nobody made.
    const english = flatten(en as Record<string, unknown>);
    const bangla = flatten(bn as Record<string, unknown>);
    for (const key of ['exercise.exclusion.applies', 'exercise.exclusion.notAsked']) {
      expect(english.get(key), key).toBeDefined();
      expect(bangla.get(key), key).toBeDefined();
    }
    expect(english.get('exercise.exclusion.applies')).not.toBe(
      english.get('exercise.exclusion.notAsked'),
    );
    expect(bangla.get('exercise.exclusion.applies')).not.toBe(
      bangla.get('exercise.exclusion.notAsked'),
    );
    // The unasked sentence must not claim the condition applies: it says "until … has been
    // asked" in English and names জিজ্ঞাসা — asking — in Bangla.
    expect(english.get('exercise.exclusion.notAsked')).toMatch(/has been asked/);
    expect(bangla.get('exercise.exclusion.notAsked')).toContain('জিজ্ঞাসা');
    // Both still say how many and which condition, so the two sentences carry the same facts.
    for (const key of ['exercise.exclusion.applies', 'exercise.exclusion.notAsked']) {
      for (const text of [english.get(key)!, bangla.get(key)!]) {
        expect(text, key).toContain('{n, number}');
        expect(text, key).toContain('{condition}');
      }
    }
  });

  it('names conditions in a NOT_ASKED reason too, and still no exercise', () => {
    const reading = exclusionReading(grown(), 'bn')!;
    expect(reading.reasons[1]!.name.text).toBe('হৃদযন্ত্রের সীমাবদ্ধতা');
    const drawn = JSON.stringify(reading);
    for (const code of LIBRARY_CODES) {
      expect(drawn.includes(code), code).toBe(false);
    }
  });
});

// --- the questions ---

describe('the station asks the question, not the label', () => {
  it('renders each condition’s question as its own text, in the clinic’s order', () => {
    const rows = questionRows(CONDITIONS, emptyAssessment(), 'en');
    expect(rows.map((row) => row.code)).toEqual([
      'SEVERE_NEUROPATHY',
      'PROLIFERATIVE_RETINOPATHY',
      'CARDIAC_LIMITATION',
      'ACTIVE_FOOT_ULCER',
      'UNCONTROLLED_HYPERTENSION',
    ]);
    // The monofilament, not the word "neuropathy". A checkbox saying "neuropathy" gets ticked
    // for tingling toes; this question does not.
    expect(rows[0]!.question.text).toContain('monofilament');
    expect(rows[0]!.name.text).toBe('Severe peripheral neuropathy');
    for (const row of rows) {
      expect(row.question.text, row.code).not.toBe(row.name.text);
      expect(row.question.text.length, row.code).toBeGreaterThan(row.name.text.length);
    }
  });

  it('sorts by the server’s ordering rather than by arrival', () => {
    const shuffled = [...CONDITIONS].reverse();
    expect(questionRows(shuffled, emptyAssessment(), 'en').map((row) => row.code)[0]).toBe(
      'SEVERE_NEUROPATHY',
    );
  });

  it('carries each answer back onto its own row', () => {
    const draft = answerCondition(emptyAssessment(), 'CARDIAC_LIMITATION', 'yes');
    const rows = questionRows(CONDITIONS, draft, 'en');
    expect(rows.find((row) => row.code === 'CARDIAC_LIMITATION')!.answer).toBe('yes');
    // Unanswered is null rather than false: the difference is the whole point below.
    expect(rows.find((row) => row.code === 'ACTIVE_FOOT_ULCER')!.answer).toBeNull();
  });

  it('takes an answer back when the same one is pressed again', () => {
    let draft = answerCondition(emptyAssessment(), 'SWIM_NOT_A_CONDITION', 'yes');
    draft = answerCondition(draft, 'SWIM_NOT_A_CONDITION', 'yes');
    expect(draft.answers).toEqual({});
  });
});

describe('an assessment nobody finished is not an assessment', () => {
  it('lists the questions still open, in order', () => {
    const draft = answerCondition(emptyAssessment(), 'CARDIAC_LIMITATION', 'no');
    expect(unanswered(draft, CONDITIONS)).toEqual([
      'SEVERE_NEUROPATHY',
      'PROLIFERATIVE_RETINOPATHY',
      'ACTIVE_FOOT_ULCER',
      'UNCONTROLLED_HYPERTENSION',
    ]);
  });

  it('refuses to build a body while one is open', () => {
    // The failure this prevents: an operator skips the neuropathy question, the body goes out
    // with `[]`, and the record then asserts *no conditions apply* about a question nobody
    // asked — which is indistinguishable from a careful assessment and filters nothing.
    const draft = answerCondition(emptyAssessment(), 'CARDIAC_LIMITATION', 'no');
    expect(toAssessmentBody(draft, CONDITIONS, ids)).toBeNull();
    expect(toAssessmentBody(emptyAssessment(), CONDITIONS, ids)).toBeNull();
    // And with no catalogue in hand there is nothing to have asked.
    expect(toAssessmentBody(allAnsweredNo(), [], ids)).toBeNull();
  });

  it('sends an empty array rather than omitting the conditions', () => {
    const body = toAssessmentBody(allAnsweredNo(), CONDITIONS, ids)!;
    expect(body.contraindications).toEqual([]);
    expect('contraindications' in body).toBe(true);
    // "None apply" is the fact the whole filter turns on, and an absent list is
    // indistinguishable from a station that never asked.
    expect(JSON.parse(JSON.stringify(body)).contraindications).toEqual([]);
  });

  it('sends the codes that apply, in the catalogue’s order', () => {
    let draft = allAnsweredNo();
    draft = answerCondition(draft, 'ACTIVE_FOOT_ULCER', 'yes');
    draft = answerCondition(draft, 'SEVERE_NEUROPATHY', 'yes');
    expect(conditionsOf(draft, CONDITIONS)).toEqual(['SEVERE_NEUROPATHY', 'ACTIVE_FOOT_ULCER']);
    expect(toAssessmentBody(draft, CONDITIONS, ids)!.contraindications).toEqual([
      'SEVERE_NEUROPATHY',
      'ACTIVE_FOOT_ULCER',
    ]);
  });
});

// --- what was asked, beside what applies ---

describe('an assessment says what it asked, not only what it found', () => {
  it('sends every question that was put, in the catalogue’s order', () => {
    const body = toAssessmentBody(allAnsweredNo(), CONDITIONS, ids)!;
    expect(body.asked).toEqual([
      'SEVERE_NEUROPATHY',
      'PROLIFERATIVE_RETINOPATHY',
      'CARDIAC_LIMITATION',
      'ACTIVE_FOOT_ULCER',
      'UNCONTROLLED_HYPERTENSION',
    ]);
    // The pair is what makes either half mean anything: five asked and none applying is a
    // different row from two asked and none applying, and only the first is a complete
    // assessment. Sending `contraindications` alone made them byte-identical.
    expect(body.contraindications).toEqual([]);
    expect('asked' in body).toBe(true);
  });

  it('never records a finding about a question it did not put', () => {
    // Invariant 89, kept on this side too. It cannot be violated from this screen — the draft
    // has no way to hold "yes" for a question it never rendered — and the check is here so a
    // change that made it possible fails a test rather than a write.
    let draft = allAnsweredNo();
    draft = answerCondition(draft, 'ACTIVE_FOOT_ULCER', 'yes');
    const body = toAssessmentBody(draft, CONDITIONS, ids)!;
    for (const code of body.contraindications) {
      expect(body.asked.includes(code), code).toBe(true);
    }
  });

  it('says what was asked rather than what was downloaded', () => {
    // `askedOf` reads the answers, not the catalogue. In practice the two agree — the body is
    // refused until every question is answered — but a tablet whose catalogue grew mid-assessment
    // must not claim to have put a question it never showed.
    const partly = answerCondition(emptyAssessment(), 'CARDIAC_LIMITATION', 'no');
    expect(askedOf(partly, CONDITIONS)).toEqual(['CARDIAC_LIMITATION']);
    expect(askedOf(emptyAssessment(), CONDITIONS)).toEqual([]);
  });

  it('reads back how many questions an assessment put', () => {
    // The number that makes "none apply" mean something, and the one that explains a NOT_ASKED
    // exclusion: an assessment that asked five is complete-for-its-time after a sixth is added.
    expect(assessmentReading(assessment(), CONDITIONS, 'en')!.askedCount).toBe(5);
    expect(assessmentReading(assessment({ asked: [] }), CONDITIONS, 'en')!.askedCount).toBe(0);
  });

  it('sends `asked` on the wire, and the server sees it', async () => {
    const calls = stubFetch(
      respond({ assessment: assessment(), options: options() }, { status: 201 }),
    );
    await recordAssessment(toAssessmentBody(allAnsweredNo(), CONDITIONS, ids)!);
    const sent = JSON.parse(calls[0]!.body);
    expect(sent.asked).toHaveLength(5);
    expect(sent.contraindications).toEqual([]);
  });
});

describe('a catalogue this tablet has not caught up with', () => {
  const stale = (locale: 'en' | 'bn' = 'en') =>
    troubleOf(
      new ApiError({
        status: 422,
        code: 'VALIDATION_FAILED',
        kind: 'validation',
        messageEN: 'Some values need correcting.',
        messageBN: 'কিছু তথ্য সংশোধন করতে হবে।',
        fields: {
          asked:
            'These questions have not been asked, and the plan cannot be filtered without them: RENAL_IMPAIRMENT, PREGNANCY',
          missing_conditions: 'RENAL_IMPAIRMENT, PREGNANCY',
        },
        fieldsBN: {
          asked:
            'এই প্রশ্নগুলি করা হয়নি, আর এগুলি ছাড়া পরিকল্পনা যাচাই করা যাবে না: RENAL_IMPAIRMENT, PREGNANCY',
          missing_conditions: 'RENAL_IMPAIRMENT, PREGNANCY',
        },
        correlationID: 'req-9',
      }),
      locale,
      'assessment',
    );

  it('is recognised, and carries the codes out of the prose', () => {
    // `fields.missing_conditions` is what the contract says every client branches on to survive a
    // catalogue change. The same codes are inside `fields.asked` as a sentence — which gets
    // translated, shortened and improved — and a screen that parsed that sentence to find them
    // would break the day somebody rewrote it.
    expect(questionsMissing(stale())).toBe(true);
    expect(stale().missing).toEqual(['RENAL_IMPAIRMENT', 'PREGNANCY']);
    expect(stale('bn').missing).toEqual(['RENAL_IMPAIRMENT', 'PREGNANCY']);
    expect(stale().field).toBe('asked');
    expect(stale().message).toContain('RENAL_IMPAIRMENT');
    expect(stale('bn').message).toContain('এই প্রশ্নগুলি করা হয়নি');
    // The codes are never taken as the sentence, whichever key Go happened to serialise first.
    expect(stale().message).not.toBe('RENAL_IMPAIRMENT, PREGNANCY');
  });

  it('marks the new questions on the form rather than making the operator hunt', () => {
    // The point of having the codes at all: five questions, one of them new, and the operator can
    // see which without re-reading the other four.
    const grown = [
      ...CONDITIONS,
      {
        code: 'RENAL_IMPAIRMENT',
        name_en: 'Advanced kidney disease',
        name_bn: 'কিডনির উন্নত পর্যায়ের রোগ',
        question_en: 'Is the patient on dialysis, or has an eGFR below 30?',
        question_bn: 'রোগী কি ডায়ালাইসিসে আছেন, বা eGFR ৩০-এর নিচে?',
        ordering: 60,
      },
    ];
    const rows = questionRows(grown, allAnsweredNo(), 'en', ['RENAL_IMPAIRMENT']);
    expect(rows.filter((row) => row.newly).map((row) => row.code)).toEqual(['RENAL_IMPAIRMENT']);
    // Nothing is marked when nothing was refused, which is every ordinary assessment.
    expect(questionRows(grown, allAnsweredNo(), 'en').every((row) => !row.newly)).toBe(true);
  });

  it('carries no codes when the refusal is about something else', () => {
    expect(troubleOf(new Error('nothing'), 'en', 'assessment').missing).toEqual([]);
    const otherField = troubleOf(
      new ApiError({
        status: 422,
        code: 'VALIDATION_FAILED',
        kind: 'validation',
        messageEN: 'Some values need correcting.',
        messageBN: 'কিছু তথ্য সংশোধন করতে হবে।',
        fields: { contraindications: 'One of those conditions is not in the list.' },
        correlationID: 'req-13',
      }),
      'en',
      'assessment',
    );
    expect(questionsMissing(otherField)).toBe(false);
    expect(otherField.field).toBe('contraindications');
  });

  it('sends the operator back to the questions rather than blaming them', () => {
    // Unreachable by anything the operator did: `toAssessmentBody` will not build a body until
    // every question this tablet knows about is answered. So the way on is to read the catalogue
    // again, and the advice is the form rather than "try again".
    // Its own advice rather than the plain "answer the questions": the screen has already read
    // the catalogue again and marked what is new, and an operator told only to answer the
    // questions would re-read all six looking for the change.
    expect(adviceFor(stale())).toBe('reread');
    expect(serverSpoke(stale())).toBe(true);
    expect(stepFor({ options: options(), plan: null, asking: false }, stale())).toBe('choose');
    // It is a refusal — the findings were not recorded — so it is drawn as one.
    expect(readsAsFailure(stale())).toBe(true);
    // And it is not one of the "look at the list again" refusals: the list is not the problem.
    expect(reviewFor(stale())).toBeNull();
  });

  it('is answered by reading the questions again', async () => {
    // The sequence the screen performs: record, refused on `asked`, read the catalogue again.
    const grown = [
      ...CONDITIONS,
      {
        code: 'RENAL_IMPAIRMENT',
        name_en: 'Advanced kidney disease',
        name_bn: 'কিডনির উন্নত পর্যায়ের রোগ',
        question_en: 'Is the patient on dialysis, or has an eGFR below 30?',
        question_bn: 'রোগী কি ডায়ালাইসিসে আছেন, বা eGFR ৩০-এর নিচে?',
        ordering: 60,
      },
    ];
    const calls = stubFetch(
      respond(
        {
          error: {
            code: 'VALIDATION_FAILED',
            kind: 'validation',
            message: 'Some values need correcting.',
            message_bn: 'কিছু তথ্য সংশোধন করতে হবে।',
            fields: {
              asked: 'These questions have not been asked: RENAL_IMPAIRMENT',
              missing_conditions: 'RENAL_IMPAIRMENT',
            },
          },
        },
        { status: 422 },
      ),
      respond({ contraindications: grown }),
    );

    const error = await recordAssessment(toAssessmentBody(allAnsweredNo(), CONDITIONS, ids)!).catch(
      (e: unknown) => e,
    );
    const trouble = troubleOf(error, 'en', 'assessment');
    expect(questionsMissing(trouble)).toBe(true);
    // The codes are in hand before the refetch, which is what lets the form mark the new row the
    // moment it appears rather than after somebody compares two lists by eye.
    expect(trouble.missing).toEqual(['RENAL_IMPAIRMENT']);

    const fresh = await listContraindications();
    expect(new URL(calls[1]!.url).pathname).toBe('/v1/exercise/contraindications');
    // The new question is now on the form, marked, and the answers already given are still there.
    const answered = allAnsweredNo();
    expect(unanswered(answered, fresh)).toEqual(['RENAL_IMPAIRMENT']);
    expect(toAssessmentBody(answered, fresh, ids)).toBeNull();
    const rows = questionRows(fresh, answered, 'en', trouble.missing);
    expect(rows.find((row) => row.code === 'RENAL_IMPAIRMENT')!.newly).toBe(true);
    expect(rows.find((row) => row.code === 'SEVERE_NEUROPATHY')!.newly).toBe(false);
    const complete = answerCondition(answered, 'RENAL_IMPAIRMENT', 'no');
    expect(toAssessmentBody(complete, fresh, ids)!.asked).toHaveLength(6);
  });
});

describe('mobility has three states, and absent is not false', () => {
  it('omits `walks_unaided` entirely when nobody asked', () => {
    const body = toAssessmentBody(allAnsweredNo(), CONDITIONS, ids)!;
    expect('walks_unaided' in body).toBe(false);
    expect(WALKS_ANSWERS).toEqual(['yes', 'no', 'notAsked']);
  });

  it('sends false only when somebody answered no', () => {
    const cannot = answerWalks(allAnsweredNo(), 'no');
    expect(toAssessmentBody(cannot, CONDITIONS, ids)!.walks_unaided).toBe(false);
    const can = answerWalks(allAnsweredNo(), 'yes');
    expect(toAssessmentBody(can, CONDITIONS, ids)!.walks_unaided).toBe(true);
  });

  it('reads the same three states back off a recorded assessment', () => {
    expect(assessmentReading(assessment(), CONDITIONS, 'en')!.walks).toBe('notAsked');
    expect(assessmentReading(assessment({ walks_unaided: false }), CONDITIONS, 'en')!.walks).toBe(
      'no',
    );
    expect(assessmentReading(assessment({ walks_unaided: true }), CONDITIONS, 'en')!.walks).toBe(
      'yes',
    );
  });

  it('leaves the walking minutes out rather than guessing one', () => {
    const body = toAssessmentBody(allAnsweredNo(), CONDITIONS, ids)!;
    expect('walk_minutes' in body).toBe(false);
    expect(walkMinutesProblem(allAnsweredNo())).toBeNull();
  });

  it('refuses a figure the contract would refuse, before the operator presses', () => {
    const overRange = typeWalkMinutes(allAnsweredNo(), '900');
    expect(walkMinutesProblem(overRange)).toBe('outOfRange');
    expect(toAssessmentBody(overRange, CONDITIONS, ids)).toBeNull();
    expect(walkMinutesProblem(typeWalkMinutes(allAnsweredNo(), 'half an hour'))).toBe('notANumber');
    expect(walkMinutesProblem(typeWalkMinutes(allAnsweredNo(), '2.5'))).toBe('notANumber');
    expect(WALK_MINUTES).toEqual({ min: 0, max: 600 });
    // Zero is a real answer — a patient who does not walk at all — and is not a problem.
    expect(walkMinutesProblem(typeWalkMinutes(allAnsweredNo(), '0'))).toBeNull();
    expect(
      toAssessmentBody(typeWalkMinutes(allAnsweredNo(), '0'), CONDITIONS, ids)!.walk_minutes,
    ).toBe(0);
  });

  it('trims the joint pain and leaves it out when it is blank', () => {
    const blank = typeJointPain(allAnsweredNo(), '   ');
    expect('joint_pain' in toAssessmentBody(blank, CONDITIONS, ids)!).toBe(false);
    const said = typeJointPain(allAnsweredNo(), '  right knee, worse on stairs  ');
    expect(toAssessmentBody(said, CONDITIONS, ids)!.joint_pain).toBe('right knee, worse on stairs');
  });
});

// --- targets are two numbers ---

describe('a target missing either number cannot be submitted', () => {
  it('says which number is missing, and which is out of range', () => {
    expect(targetProblem({ code: 'WALK_FLAT', times: '', minutes: '' })).toBe('times');
    expect(targetProblem({ code: 'WALK_FLAT', times: '5', minutes: '' })).toBe('minutes');
    expect(targetProblem({ code: 'WALK_FLAT', times: '0', minutes: '30' })).toBe('timesRange');
    expect(targetProblem({ code: 'WALK_FLAT', times: '15', minutes: '30' })).toBe('timesRange');
    expect(targetProblem({ code: 'WALK_FLAT', times: '5', minutes: '0' })).toBe('minutesRange');
    expect(targetProblem({ code: 'WALK_FLAT', times: '5', minutes: '300' })).toBe('minutesRange');
    expect(targetProblem({ code: 'WALK_FLAT', times: 'five', minutes: '30' })).toBe('timesRange');
    expect(targetProblem({ code: 'WALK_FLAT', times: '5', minutes: '30' })).toBeNull();
    expect(TARGET_PROBLEMS).toEqual(['times', 'timesRange', 'minutes', 'minutesRange']);
    expect(TIMES_PER_WEEK).toEqual({ min: 1, max: 14 });
    expect(MINUTES_PER_SESSION).toEqual({ min: 1, max: 240 });
  });

  it('choosing an exercise fills in no number at all', () => {
    // No default of "three times a week for thirty minutes" anywhere. A target the operator did
    // not choose is a number §12.1's adherence analysis would read as a clinical decision.
    const chosen = chooseExercise(noTargets(), 'WALK_FLAT');
    expect(targetFor(chosen, 'WALK_FLAT')).toEqual({ code: 'WALK_FLAT', times: '', minutes: '' });
    expect(isChosen(chosen, 'WALK_FLAT')).toBe(true);
    expect(isChosen(chooseExercise(chosen, 'WALK_FLAT'), 'WALK_FLAT')).toBe(false);
  });

  it('builds no body while a chosen exercise is missing a number', () => {
    const payload = options();
    let chosen = chooseExercise(noTargets(), 'WALK_FLAT');
    chosen = chooseExercise(chosen, 'CHAIR_STAND');
    chosen = typeTimes(chosen, 'WALK_FLAT', '5');
    chosen = typeMinutes(chosen, 'WALK_FLAT', '30');
    // Chair stands chosen, neither number typed.
    expect(incomplete(chosen)).toEqual(['CHAIR_STAND']);
    expect(targetProblems(chosen)).toEqual({ CHAIR_STAND: 'times' });
    expect(toPlanBody(chosen, payload, ids)).toBeNull();

    chosen = typeTimes(chosen, 'CHAIR_STAND', '3');
    // Still half a target: the minutes are what §12.1 multiplies.
    expect(toPlanBody(chosen, payload, ids)).toBeNull();
    expect(incomplete(chosen)).toEqual(['CHAIR_STAND']);

    chosen = typeMinutes(chosen, 'CHAIR_STAND', '10');
    expect(incomplete(chosen)).toEqual([]);
    expect(toPlanBody(chosen, payload, ids)).not.toBeNull();
  });

  it('builds no body from an empty plan, or with no list in hand', () => {
    expect(toPlanBody(noTargets(), options(), ids)).toBeNull();
    const ready = typeMinutes(
      typeTimes(chooseExercise(noTargets(), 'WALK_FLAT'), 'WALK_FLAT', '5'),
      'WALK_FLAT',
      '30',
    );
    expect(toPlanBody(ready, null, ids)).toBeNull();
    expect(toPlanBody(ready, options(), { ...ids, patient: '  ' })).toBeNull();
  });

  it('refuses a code the server did not offer, whatever a caller holds', () => {
    // Unreachable from the screen, because the only codes it can choose came out of
    // `offerRows`. It is here so that the one place a target could be smuggled in has a test
    // on it — and so a stale selection cannot become a request.
    let chosen = chooseExercise(noTargets(), 'JOG');
    chosen = typeTimes(chosen, 'JOG', '3');
    chosen = typeMinutes(chosen, 'JOG', '30');
    expect(toPlanBody(chosen, options(), ids)).toBeNull();
  });

  it('freezes the plan against the assessment the list came from', () => {
    // The whole staleness mechanism. A client that sent anything else — the assessment it
    // recorded itself, one kept from a previous patient — would defeat the server's check.
    const payload = options({ assessment_id: 'assessment-77' });
    const chosen = typeMinutes(
      typeTimes(chooseExercise(noTargets(), 'WALK_FLAT'), 'WALK_FLAT', '5'),
      'WALK_FLAT',
      '30',
    );
    expect(toPlanBody(chosen, payload, ids)!.assessment_id).toBe('assessment-77');
  });

  it('sends the targets in the offered list’s order, not the tapping order', () => {
    let chosen = chooseExercise(noTargets(), 'SEATED_STRETCH');
    chosen = chooseExercise(chosen, 'WALK_FLAT');
    for (const code of ['SEATED_STRETCH', 'WALK_FLAT']) {
      chosen = typeMinutes(typeTimes(chosen, code, '3'), code, '20');
    }
    const body = toPlanBody(chosen, options(), ids)!;
    expect(body.targets.map((target) => target.exercise_code)).toEqual([
      'WALK_FLAT',
      'SEATED_STRETCH',
    ]);
    expect(body.targets.map((target) => target.ordering)).toEqual([1, 2]);
    // Two integers per target and nothing else that could carry "walk more".
    for (const target of body.targets) {
      expect(Object.keys(target).sort()).toEqual([
        'exercise_code',
        'minutes_per_session',
        'ordering',
        'times_per_week',
      ]);
      expect(Number.isInteger(target.times_per_week)).toBe(true);
      expect(Number.isInteger(target.minutes_per_session)).toBe(true);
    }
  });
});

// --- 409: no assessment ---

describe('no assessment means the questions, not an error', () => {
  const refused = () =>
    troubleOf(
      new ApiError({
        status: 409,
        code: CODE_NO_ASSESSMENT,
        kind: 'conflict',
        messageEN:
          'No exercise assessment has been recorded for this patient yet, so no options can be offered.',
        messageBN:
          'এই রোগীর ব্যায়াম-মূল্যায়ন এখনও নেওয়া হয়নি, তাই কোনও বিকল্প দেখানো যাচ্ছে না।',
        correlationID: 'req-1',
      }),
      'en',
      'options',
    );

  it('recognises it by the server’s code and not by the status', () => {
    expect(noAssessment(refused())).toBe(true);
    // 409 is also how a replayed event id arrives, and that is not an instruction to go and
    // ask five questions.
    const replay = troubleOf(
      new ApiError({
        status: 409,
        code: 'IDEMPOTENCY_KEY_REUSED',
        kind: 'conflict',
        messageEN: 'That key has been used for a different request.',
        messageBN: 'ওই কি-টি অন্য একটি অনুরোধে ব্যবহার হয়েছে।',
        correlationID: 'req-2',
      }),
      'en',
      'assessment',
    );
    expect(noAssessment(replay)).toBe(false);
  });

  it('routes to the assessment step', () => {
    expect(stepFor(emptyStage(), refused())).toBe('questions');
    // Even with a list and a plan already in hand — the refusal is about this patient now.
    expect(stepFor({ options: options(), plan: plan(), asking: false }, refused())).toBe(
      'questions',
    );
    expect(STEPS).toEqual(['questions', 'choose', 'sheet']);
  });

  it('is not drawn as a failure, and the advice is not "try again"', () => {
    const trouble = refused();
    expect(readsAsFailure(trouble)).toBe(false);
    expect(adviceFor(trouble)).toBe('ask');
    // Pressing retry on the same read fails identically for ever, which is how an operator
    // learns to press it at the failures where it cannot help.
    expect(adviceFor(trouble)).not.toBe('retry');
    // The banner draws the server's own sentence, not a copy of it written here.
    expect(serverSpoke(trouble)).toBe(true);
    expect(trouble.message).toContain('No exercise assessment has been recorded');
  });

  it('arrives from the endpoint with its code intact', async () => {
    stubFetch(
      refusal(
        409,
        CODE_NO_ASSESSMENT,
        'No exercise assessment has been recorded for this patient yet.',
        'এই রোগীর ব্যায়াম-মূল্যায়ন এখনও নেওয়া হয়নি।',
      ),
    );
    const error = await readOptions('patient-1').catch((e: unknown) => e);
    const trouble = troubleOf(error, 'en', 'options');
    expect(trouble.code).toBe(CODE_NO_ASSESSMENT);
    expect(stepFor(emptyStage(), trouble)).toBe('questions');
  });

  it('moves off the questions only once the server has answered with a list', () => {
    expect(stepFor(emptyStage(), null)).toBe('questions');
    expect(stepFor({ options: options(), plan: null, asking: false }, null)).toBe('choose');
    expect(stepFor({ options: options(), plan: plan(), asking: false }, null)).toBe('sheet');
    // And the operator can go back and ask again on a patient who already has findings.
    expect(stepFor({ options: options(), plan: plan(), asking: true }, null)).toBe('questions');
  });
});

// --- 409: superseded ---

describe('a newer assessment sends the operator back to a freshly read list', () => {
  const stale = () =>
    troubleOf(
      new ApiError({
        status: 409,
        code: CODE_SUPERSEDED,
        kind: 'conflict',
        messageEN:
          'A newer assessment has been recorded for this patient. Review the options again before issuing a plan.',
        messageBN:
          'এই রোগীর নতুন একটি মূল্যায়ন নেওয়া হয়েছে। পরিকল্পনা দেওয়ার আগে বিকল্পগুলি আবার দেখুন।',
        correlationID: 'req-3',
      }),
      'en',
      'plan',
    );

  it('is recognised, warned about, and answered by reading the list again', () => {
    const trouble = stale();
    expect(superseded(trouble)).toBe(true);
    expect(reviewFor(trouble)).toBe('superseded');
    expect(adviceFor(trouble)).toBe('review');
    expect(readsAsFailure(trouble)).toBe(true);
    expect(serverSpoke(trouble)).toBe(true);
    expect(REVIEWS).toEqual(['superseded', 'contraindicated', 'moved']);
  });

  it('refetches the options and carries the selection across the new list', async () => {
    // The sequence the screen performs: issue, refused, read the options again, keep what the
    // fresh list still offers.
    const narrower = options({
      assessment_id: 'assessment-2',
      exercises: options().exercises.filter((one) => one.code !== 'WALK_BRISK'),
      excluded: 4,
      reasons: [neuropathy(3), cardiac(4)],
    });
    const calls = stubFetch(
      refusal(
        409,
        CODE_SUPERSEDED,
        'A newer assessment has been recorded.',
        'নতুন মূল্যায়ন নেওয়া হয়েছে।',
      ),
      respond(narrower),
    );

    let chosen = chooseExercise(noTargets(), 'WALK_BRISK');
    chosen = chooseExercise(chosen, 'CHAIR_STAND');
    for (const code of ['WALK_BRISK', 'CHAIR_STAND']) {
      chosen = typeMinutes(typeTimes(chosen, code, '3'), code, '20');
    }

    const error = await issuePlan(toPlanBody(chosen, options(), ids)!).catch((e: unknown) => e);
    const trouble = troubleOf(error, 'en', 'plan');
    expect(reviewFor(trouble)).toBe('superseded');

    const fresh = await readOptions('patient-1');
    expect(new URL(calls[1]!.url).pathname).toBe('/v1/patients/patient-1/exercise/options');

    const kept = keepOffered(chosen, fresh);
    // The exercise the new findings exclude comes off the plan, and the operator is told.
    expect(kept.dropped).toEqual(['WALK_BRISK']);
    expect(kept.chosen.map((target) => target.code)).toEqual(['CHAIR_STAND']);
    // The numbers already typed for what survived are kept: retyping them would be a punishment
    // for a colleague's write.
    expect(targetFor(kept.chosen, 'CHAIR_STAND')).toEqual({
      code: 'CHAIR_STAND',
      times: '3',
      minutes: '20',
    });
    // And the plan now freezes against the *new* assessment.
    expect(toPlanBody(kept.chosen, fresh, ids)!.assessment_id).toBe('assessment-2');
  });

  it('names the dropped choices rather than counting them', () => {
    // A count leaves the operator comparing two lists by eye, having already typed two numbers
    // for the choice that went. The names come off the list they chose from — the new one no
    // longer has those rows, which is the whole reason they were dropped.
    const before = options();
    const after = options({
      exercises: before.exercises.filter((one) => one.code !== 'WALK_FLAT'),
    });
    let chosen = chooseExercise(noTargets(), 'WALK_FLAT');
    chosen = chooseExercise(chosen, 'CHAIR_STAND');
    const kept = keepOffered(chosen, after);
    expect(kept.dropped).toEqual(['WALK_FLAT']);
    expect(namesOf(kept.dropped, before, 'en')).toEqual(['Walking on level ground']);
    expect(namesOf(kept.dropped, before, 'bn')).toEqual(['সমতল জায়গায় হাঁটা']);
    // Against the new list there is nothing to resolve, which is why the old one is the one to
    // ask — and a code the screen cannot name is still shown rather than dropped silently.
    expect(namesOf(kept.dropped, after, 'en')).toEqual(['WALK_FLAT']);
    expect(namesOf(['NOT_A_CODE'], before, 'en')).toEqual(['NOT_A_CODE']);
    expect(namesOf([], before, 'en')).toEqual([]);
    expect(namesOf(['WALK_FLAT'], null, 'en')).toEqual(['WALK_FLAT']);
  });

  it('keeps nothing at all when there is no fresh list to keep it against', () => {
    const chosen = chooseExercise(noTargets(), 'WALK_FLAT');
    expect(keepOffered(chosen, null)).toEqual({ chosen: [], dropped: [] });
  });

  it('names only an exercise the operator was given and has now had taken back', () => {
    // The distinction criterion 1 turns on: this screen may name a row the server sent and has
    // since withdrawn, because the operator saw it and chose it. It can never name one it was
    // not sent, because those never reached the device.
    const chosen = chooseExercise(noTargets(), 'CHAIR_STAND');
    const { dropped } = keepOffered(chosen, options({ exercises: [walkFlat()] }));
    expect(dropped).toEqual(['CHAIR_STAND']);
    for (const target of chosen) {
      expect(dropped.every((code) => code === target.code)).toBe(true);
    }
  });
});

// --- 422: the refusal this checkpoint exists for ---

describe('a contraindicated target is refused, named, and put on its own row', () => {
  /** The refusal as the handler now writes it: a sentence, and the two codes beside it. */
  const forbidden = (locale: 'en' | 'bn') =>
    troubleOf(
      new ApiError({
        status: 422,
        code: CODE_CONTRAINDICATED,
        kind: 'validation',
        messageEN:
          'Jogging is not safe with severe peripheral neuropathy. A foot without protective sensation cannot feel an injury from repeated impact, and the damage is found days later.',
        messageBN:
          'তীব্র পেরিফেরাল নিউরোপ্যাথি থাকলে জগিং নিরাপদ নয়। সুরক্ষামূলক অনুভূতিহীন পা বারবার আঘাতের ক্ষতি টের পায় না, আর কয়েকদিন পরে তা ধরা পড়ে।',
        fields: {
          targets:
            'Jogging is not safe with severe peripheral neuropathy. A foot without protective sensation cannot feel an injury from repeated impact, and the damage is found days later.',
          exercise_code: 'JOG',
          contraindication_code: 'SEVERE_NEUROPATHY',
        },
        fieldsBN: {
          targets:
            'তীব্র পেরিফেরাল নিউরোপ্যাথি থাকলে জগিং নিরাপদ নয়। সুরক্ষামূলক অনুভূতিহীন পা বারবার আঘাতের ক্ষতি টের পায় না, আর কয়েকদিন পরে তা ধরা পড়ে।',
          exercise_code: 'JOG',
          contraindication_code: 'SEVERE_NEUROPATHY',
        },
        correlationID: 'req-4',
      }),
      locale,
      'plan',
    );

  it('names the exercise, the condition and the reason, in the reader’s language', () => {
    expect(contraindicated(forbidden('en'))).toBe(true);
    // The reason from the mapping table, which is the sentence a clinician who disagrees with an
    // exclusion argues with. "Not allowed" sends an operator to the next high-impact option.
    expect(forbidden('en').message).toContain('protective sensation');
    expect(forbidden('en').message).toContain('Jogging');
    expect(forbidden('bn').message).toContain('সুরক্ষামূলক অনুভূতিহীন পা');
    expect(adviceFor(forbidden('en'))).toBe('review');
    expect(reviewFor(forbidden('en'))).toBe('contraindicated');
  });

  it('takes the sentence from `targets` and never from a code field', () => {
    // Go marshals a map with its keys sorted, so `contraindication_code` arrives first. A client
    // that took "the first field the server named" would draw SEVERE_NEUROPATHY at an operator
    // as though that were the explanation.
    expect(forbidden('en').field).toBe('targets');
    expect(forbidden('en').message).not.toBe('SEVERE_NEUROPATHY');
    expect(forbidden('en').message).not.toBe('JOG');
    expect(forbidden('bn').field).toBe('targets');
  });

  it('carries the codes apart from the sentence, so the row can be found', () => {
    // This is what the codes in `fields` are for: the refusal goes on the row the operator chose
    // rather than at the top of a list of nine, where they would have to work out which one.
    expect(forbidden('en').exercise).toBe('JOG');
    expect(forbidden('en').condition).toBe('SEVERE_NEUROPATHY');
    expect(forbidden('bn').exercise).toBe('JOG');
    // Nothing else this station meets names an exercise, so nothing else lights up a row.
    expect(
      troubleOf(
        new ApiError({
          status: 409,
          code: CODE_SUPERSEDED,
          kind: 'conflict',
          messageEN: 'A newer assessment has been recorded.',
          messageBN: 'নতুন মূল্যায়ন নেওয়া হয়েছে।',
          correlationID: 'req-4b',
        }),
        'en',
        'plan',
      ).exercise,
    ).toBe('');
  });

  it('arrives from the endpoint with all three parts intact', async () => {
    stubFetch(
      respond(
        {
          error: {
            code: CODE_CONTRAINDICATED,
            kind: 'validation',
            message: 'Jogging is not safe with severe peripheral neuropathy.',
            message_bn: 'তীব্র পেরিফেরাল নিউরোপ্যাথি থাকলে জগিং নিরাপদ নয়।',
            fields: {
              targets: 'Jogging is not safe with severe peripheral neuropathy.',
              exercise_code: 'JOG',
              contraindication_code: 'SEVERE_NEUROPATHY',
            },
          },
        },
        { status: 422 },
      ),
    );
    const chosen = typeMinutes(
      typeTimes(chooseExercise(noTargets(), 'WALK_FLAT'), 'WALK_FLAT', '5'),
      'WALK_FLAT',
      '30',
    );
    const error = await issuePlan(toPlanBody(chosen, options(), ids)!).catch((e: unknown) => e);
    const trouble = troubleOf(error, 'en', 'plan');
    expect(trouble.exercise).toBe('JOG');
    expect(trouble.condition).toBe('SEVERE_NEUROPATHY');
    expect(trouble.message).toContain('Jogging');
  });
});

// --- a retirement is a conflict; everything else about the targets is the request ---

describe('an exercise retired mid-choice is a conflict, and says so with its own code', () => {
  const gone = (locale: 'en' | 'bn' = 'en') =>
    troubleOf(
      new ApiError({
        status: 409,
        code: CODE_RETIRED,
        kind: 'conflict',
        // The name in the sentence, the code in the field — as the handler now writes it.
        messageEN: 'Swimming is no longer in the library. Review the options and choose again.',
        messageBN: 'সাঁতার আর তালিকায় নেই। বিকল্পগুলি দেখে আবার বেছে নিন।',
        fields: { exercise_code: 'SWIM_CODE' },
        fieldsBN: { exercise_code: 'SWIM_CODE' },
        correlationID: 'req-10',
      }),
      locale,
      'plan',
    );

  it('is one of the answers that mean "fetch the options again"', () => {
    // A 409 rather than a 422, and that is the whole distinction a client needs: it says the list
    // moved, not that the request was wrong. It sits beside EXERCISE_NO_ASSESSMENT and
    // EXERCISE_ASSESSMENT_SUPERSEDED, and all three are answered by reading the options.
    expect(retired(gone())).toBe(true);
    expect(gone().status).toBe(409);
    expect(reviewFor(gone())).toBe('moved');
    expect(adviceFor(gone())).toBe('review');
    expect(readsAsFailure(gone())).toBe(true);
  });

  it('says the exercise’s name, and keeps the code out of the sentence', () => {
    // The operator chose a row that said "Swimming". Answering them with SWIM answers a question
    // they did not ask — and the code is still there, in `fields.exercise_code`, where a screen
    // can act on it. This is the regression that would slip back the next time somebody wrote
    // this message from the error value rather than from the row.
    expect(gone().message).toContain('Swimming');
    expect(gone().message).not.toContain('SWIM_CODE');
    expect(gone('bn').message).toContain('সাঁতার');
    expect(gone('bn').message).not.toContain('SWIM_CODE');
    // The code did not disappear; it moved to the field that is for codes.
    expect(gone().exercise).toBe('SWIM_CODE');
  });

  it('keeps its sentence in the banner, because the row it names is gone', () => {
    // The bug this prevents: `refusedExercise` was the code the server named, and the banner
    // stopped repeating the message on the strength of "the row has it". A retirement names an
    // exercise that the refetch has just removed — so the row does not exist, and the refusal
    // would have been drawn nowhere at all.
    // The exercise it names is not on the list any more — that is what a retirement means.
    expect(namesExercise(gone())).toBe(true);
    expect(refusedRow(gone(), options())).toBe('');
    // Against a list that still had it, the same refusal would name a row — which is why the
    // check is against the list being drawn rather than against whether the server named one.
    const stillThere = options({
      exercises: [...options().exercises, { ...walkFlat(), code: 'SWIM_CODE' }],
    });
    expect(refusedRow(gone(), stillThere)).toBe('SWIM_CODE');
  });

  it('names the exercise even though it carries no `targets` sentence', () => {
    // The codes are read whatever the status is. Reading them only on a 422 — which an earlier
    // version of this binding did — would have lost the one field this refusal carries.
    expect(gone().exercise).toBe('SWIM_CODE');
    expect(gone('bn').exercise).toBe('SWIM_CODE');
    expect(gone().field).toBe('');
    // And the sentence is the server's own, not the code.
    expect(gone().message).toContain('no longer in the library');
    expect(gone('bn').message).toContain('আর তালিকায় নেই');
  });

  it('refetches the options and carries the selection across', async () => {
    const narrower = options({
      assessment_id: 'assessment-1',
      exercises: options().exercises.filter((one) => one.code !== 'HEAVY_LIFT'),
      excluded: 4,
      reasons: [neuropathy(3), footUlcer(4)],
    });
    const calls = stubFetch(
      respond(
        {
          error: {
            code: CODE_RETIRED,
            kind: 'conflict',
            message: 'HEAVY_LIFT is no longer in the library.',
            message_bn: 'HEAVY_LIFT আর তালিকায় নেই।',
            fields: { exercise_code: 'HEAVY_LIFT' },
          },
        },
        { status: 409 },
      ),
      respond(narrower),
    );
    let chosen = chooseExercise(noTargets(), 'HEAVY_LIFT');
    chosen = chooseExercise(chosen, 'WALK_FLAT');
    for (const code of ['HEAVY_LIFT', 'WALK_FLAT']) {
      chosen = typeMinutes(typeTimes(chosen, code, '3'), code, '20');
    }

    const error = await issuePlan(toPlanBody(chosen, options(), ids)!).catch((e: unknown) => e);
    expect(reviewFor(troubleOf(error, 'en', 'plan'))).toBe('moved');

    const fresh = await readOptions('patient-1');
    expect(new URL(calls[1]!.url).pathname).toBe('/v1/patients/patient-1/exercise/options');
    const kept = keepOffered(chosen, fresh);
    expect(kept.dropped).toEqual(['HEAVY_LIFT']);
    expect(kept.chosen.map((target) => target.code)).toEqual(['WALK_FLAT']);
  });
});

describe('every other target refusal is about the request, and names its row', () => {
  const refusedTarget = (code: string, messageEN: string, messageBN: string) =>
    troubleOf(
      new ApiError({
        status: 422,
        code: 'VALIDATION_FAILED',
        kind: 'validation',
        messageEN: 'Some values need correcting.',
        messageBN: 'কিছু তথ্য সংশোধন করতে হবে।',
        fields: { targets: messageEN, exercise_code: code },
        fieldsBN: { targets: messageBN, exercise_code: code },
        correlationID: 'req-11',
      }),
      'en',
      'plan',
    );

  const unknown = () =>
    refusedTarget(
      'BURPEES',
      'BURPEES is not an exercise in the library.',
      'BURPEES এই তালিকার কোনও ব্যায়াম নয়।',
    );
  const uncountable = () =>
    refusedTarget(
      'WALK_FLAT',
      'WALK_FLAT needs how many times a week and how many minutes each time.',
      'WALK_FLAT-এর জন্য সপ্তাহে কতবার এবং প্রতিবার কত মিনিট, তা দিতে হবে।',
    );
  const twice = () =>
    refusedTarget(
      'WALK_FLAT',
      'WALK_FLAT is on the plan twice, with two different targets. Keep one.',
      'WALK_FLAT দুইবার আছে, দুই রকম লক্ষ্য নিয়ে। একটি রাখুন।',
    );

  it('does not send the operator back to the list', () => {
    // These say the request was wrong, not that the list moved. Refetching on one would be a
    // screen throwing away a half-built plan over a mistyped number — which is exactly what an
    // earlier version of this file did, on the strength of `toPlanBody` guaranteeing its own
    // body. That reasoning was true of this screen and of nothing else.
    for (const trouble of [unknown(), uncountable(), twice()]) {
      expect(reviewFor(trouble), trouble.message).toBeNull();
      expect(adviceFor(trouble), trouble.message).toBe('none');
      expect(retired(trouble), trouble.message).toBe(false);
    }
  });

  it('names the exercise on every one of them, so the sentence goes on the row', () => {
    expect(namesExercise(unknown())).toBe(true);
    expect(unknown().exercise).toBe('BURPEES');
    expect(uncountable().exercise).toBe('WALK_FLAT');
    expect(twice().exercise).toBe('WALK_FLAT');
    // Uniformly with the contraindication, which had it first.
    for (const trouble of [unknown(), uncountable(), twice()]) {
      expect(trouble.field, trouble.exercise).toBe('targets');
      expect(trouble.message, trouble.exercise).toContain(trouble.exercise);
    }
  });

  it('still never takes a code as the message', () => {
    // Go marshals a map with its keys sorted, so `exercise_code` arrives before `targets`. The
    // guard stays necessary now that every one of these carries both.
    for (const trouble of [unknown(), uncountable(), twice()]) {
      expect(trouble.message).not.toBe(trouble.exercise);
      expect(trouble.message.length).toBeGreaterThan(trouble.exercise.length);
    }
  });

  it('refuses to build a body that could produce any of them', () => {
    // None is reachable from this screen: every code came out of `offerRows`, both numbers are
    // checked against the contract's bands, and `chooseExercise` toggles rather than appends.
    const both = [
      { code: 'WALK_FLAT', times: '5', minutes: '30' },
      { code: 'WALK_FLAT', times: '3', minutes: '20' },
    ];
    expect(duplicated(both)).toEqual(['WALK_FLAT']);
    expect(toPlanBody(both, options(), ids)).toBeNull();
    const toggled = chooseExercise(chooseExercise(noTargets(), 'WALK_FLAT'), 'WALK_FLAT');
    expect(duplicated(toggled)).toEqual([]);
    expect(toggled).toHaveLength(0);
  });

  it('puts each of them on a row that is actually on the screen', () => {
    // The other half of the rule: these name exercises the list still offers, so the sentence
    // goes on the row and the banner stops repeating it.
    expect(refusedRow(uncountable(), options())).toBe('WALK_FLAT');
    expect(refusedRow(twice(), options())).toBe('WALK_FLAT');
    // An exercise that is not in the library at all has no row either, so its sentence stays up.
    expect(namesExercise(unknown())).toBe(true);
    expect(refusedRow(unknown(), options())).toBe('');
    expect(refusedRow(uncountable(), null)).toBe('');
    expect(refusedRow(null, options())).toBe('');
  });

  it('lights up no row when the refusal is about the whole plan', () => {
    // An empty plan names no exercise, because there is none to name.
    const empty = troubleOf(
      new ApiError({
        status: 422,
        code: 'VALIDATION_FAILED',
        kind: 'validation',
        messageEN: 'Some values need correcting.',
        messageBN: 'কিছু তথ্য সংশোধন করতে হবে।',
        fields: { targets: 'A plan needs at least one exercise.' },
        correlationID: 'req-12',
      }),
      'en',
      'plan',
    );
    expect(namesExercise(empty)).toBe(false);
    expect(empty.message).toContain('at least one');
  });
});

// --- what went wrong, in the three ways it can ---

describe('the screen never writes its own version of what the server refused', () => {
  it('has a sentence of its own only for the failures that had no server', () => {
    // Three shapes and no more. A key per refusal — `trouble.contraindicated` said "That exercise
    // is not safe for this patient's recorded condition" — is a second source of truth for the
    // one sentence this checkpoint exists to deliver, and it drifts the day a clinician improves
    // the server's wording. It was also drawn in semibold *above* the server's own sentence in
    // grey, which made the paraphrase the headline and the reason from the mapping table — the
    // sentence a clinician is meant to argue with — the footnote.
    const english = flatten(en as Record<string, unknown>);
    const keys = [...english.keys()].filter((key) => key.startsWith('exercise.trouble.'));
    expect(keys.sort()).toEqual([
      'exercise.trouble.failed',
      'exercise.trouble.onRow',
      'exercise.trouble.refused',
      'exercise.trouble.unreachable',
    ]);
  });

  it('names no refusal code and no refusal of its own in either message file', () => {
    // The specific regression: a sentence keyed to a server code. If one comes back it will be
    // named after the code it paraphrases, and it will be here.
    for (const tree of [en, bn]) {
      const keys = [...flatten(tree as Record<string, unknown>).keys()];
      for (const forbidden of [
        'exercise.trouble.contraindicated',
        'exercise.trouble.superseded',
        'exercise.trouble.retired',
        'exercise.trouble.noAssessment',
        'exercise.trouble.questionsMissing',
      ]) {
        expect(keys.includes(forbidden), forbidden).toBe(false);
      }
    }
  });

  it('falls back to its own words only when there was no server to write any', () => {
    // `troubleKey` is reached only through `serverSpoke` being false, and the two cases where
    // that is true are a request that never arrived and an answer this application does not
    // understand — neither of which has a server sentence to show.
    const unreachable: Trouble = {
      kind: 'unreachable',
      attempt: 'options',
      status: 0,
      code: '',
      field: '',
      message: '',
      exercise: '',
      condition: '',
      missing: [],
    };
    expect(serverSpoke(unreachable)).toBe(false);
    expect(troubleKey(unreachable)).toBe('trouble.unreachable');
    expect(troubleKey({ ...unreachable, kind: 'failed' })).toBe('trouble.failed');
    expect(troubleKey({ ...unreachable, kind: 'refused' })).toBe('trouble.refused');
    // Whitespace is not a sentence.
    expect(serverSpoke({ ...unreachable, message: '   ' })).toBe(false);
    expect(serverSpoke({ ...unreachable, message: 'The server said this.' })).toBe(true);
  });

  it('adds only what it did and what to do next', () => {
    // Every advice sentence is about the screen or the operator, never about the refusal. The
    // check is by eye on four strings, because there are four.
    const english = flatten(en as Record<string, unknown>);
    for (const advice of ADVICE) {
      if (advice === 'none') {
        expect(english.has(`exercise.advice.${advice}`)).toBe(false);
        continue;
      }
      const text = english.get(`exercise.advice.${advice}`)!;
      expect(text, advice).toBeDefined();
      // None of them restates a clinical fact: no advice line names an exercise, a condition,
      // or says anything is unsafe.
      expect(/not safe|unsafe|contraindicat/i.test(text), advice).toBe(false);
    }
  });
});

describe('what went wrong, and what can be done about it', () => {
  it('reports a request that never left the tablet as unreachable', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Network request failed')));
    const error = await readOptions('patient-1').catch((e: unknown) => e);
    expect(troubleOf(error, 'en', 'options')).toEqual({
      kind: 'unreachable',
      attempt: 'options',
      status: 0,
      code: '',
      field: '',
      message: '',
      exercise: '',
      condition: '',
      missing: [],
    });
    expect(adviceFor(troubleOf(error, 'en', 'options'))).toBe('retry');
  });

  it('offers nothing to press for a refusal pressing cannot fix', () => {
    const forbidden: Trouble = {
      kind: 'refused',
      attempt: 'assessment',
      status: 403,
      code: 'FORBIDDEN',
      field: '',
      message: 'That hat does not record exercise.',
      exercise: '',
      condition: '',
      missing: [],
    };
    expect(adviceFor(forbidden)).toBe('none');
    expect(adviceFor({ ...forbidden, status: 404 })).toBe('none');
    expect(adviceFor({ ...forbidden, status: 422 })).toBe('none');
    expect(adviceFor({ ...forbidden, kind: 'failed', status: 500 })).toBe('retry');
    expect(ADVICE).toEqual(['ask', 'reread', 'review', 'retry', 'none']);
    expect(ATTEMPTS).toEqual(['questions', 'options', 'assessment', 'plan', 'record']);
  });

  it('reads a field message off a 422 when the server named one', () => {
    const named = troubleOf(
      new ApiError({
        status: 422,
        code: 'VALIDATION_FAILED',
        kind: 'validation',
        messageEN: 'That request cannot be answered.',
        messageBN: 'ওই অনুরোধের উত্তর দেওয়া যাচ্ছে না।',
        fields: { targets: 'Every exercise needs how many times a week and how many minutes.' },
        fieldsBN: {
          targets: 'প্রতিটি ব্যায়ামের জন্য সপ্তাহে কতবার এবং প্রতিবার কত মিনিট, তা দিতে হবে।',
        },
        correlationID: 'req-5',
      }),
      'bn',
      'plan',
    );
    expect(named.field).toBe('targets');
    expect(named.message).toContain('প্রতিবার কত মিনিট');
  });

  it('says something even when the answer is not an error this app throws', () => {
    expect(troubleOf(new Error('something else'), 'en', 'plan')).toEqual({
      kind: 'failed',
      attempt: 'plan',
      status: 0,
      code: '',
      field: '',
      message: '',
      exercise: '',
      condition: '',
      missing: [],
    });
  });
});

// --- the writes ---

describe('the writes carry their own event id and take the list back with them', () => {
  it('records the findings and uses the options that came with them', async () => {
    const calls = stubFetch(
      respond({ assessment: assessment(), options: options() }, { status: 201 }),
    );
    const body = toAssessmentBody(allAnsweredNo(), CONDITIONS, ids)!;
    const written = await recordAssessment(body);

    // One call, not two. Between a write and a refetch there is a window in which the station
    // holds findings and no list, and the natural thing for a screen to do in that window is
    // show a library it already has.
    expect(calls).toHaveLength(1);
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/exercise/assessments');
    expect(calls[0]!.method).toBe('POST');
    expect(calls[0]!.idempotencyKey).toBe(body.event_id);
    expect(calls[0]!.requestedWith).toBe('DTHCMS');
    expect(JSON.parse(calls[0]!.body).contraindications).toEqual([]);
    expect(written.options.exercises).toHaveLength(9);
    expect(written.assessment.id).toBe('assessment-1');
  });

  it('issues the plan under the same rule', async () => {
    const calls = stubFetch(respond({ plan: plan() }, { status: 201 }));
    const chosen = typeMinutes(
      typeTimes(chooseExercise(noTargets(), 'WALK_FLAT'), 'WALK_FLAT', '5'),
      'WALK_FLAT',
      '30',
    );
    const body = toPlanBody(chosen, options(), ids)!;
    const issued = await issuePlan(body);
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/exercise/plans');
    expect(calls[0]!.idempotencyKey).toBe(body.event_id);
    expect(calls[0]!.requestedWith).toBe('DTHCMS');
    expect(JSON.parse(calls[0]!.body).assessment_id).toBe('assessment-1');
    expect(issued.minutes_per_week).toBe(180);
  });

  it('reads the standing record with both halves allowed to be null', async () => {
    stubFetch(respond({ assessment: null, plan: null }));
    // A patient who has not been to station 8 has no findings and no plan, and that is an
    // ordinary first visit rather than a broken route.
    expect(await readExerciseRecord('patient-1')).toEqual({ assessment: null, plan: null });
  });

  it('reads the catalogue with no query at all', async () => {
    const calls = stubFetch(respond({ contraindications: CONDITIONS }));
    expect(await listContraindications()).toHaveLength(5);
    const url = new URL(calls[0]!.url);
    expect(url.pathname).toBe('/v1/exercise/contraindications');
    expect(url.search).toBe('');
  });
});

// --- the sheet ---

describe('the sheet is the server’s figures, printed', () => {
  it('never multiplies the two numbers into a weekly total', () => {
    // The server's figure even when it disagrees with the arithmetic beside it — which is the
    // only way to tell a copy from a second implementation.
    const odd = plan({
      items: [
        { ...plan().items[0]!, times_per_week: 5, minutes_per_session: 30, minutes_per_week: 99 },
      ],
      minutes_per_week: 99,
    });
    const reading = planReading(odd, 'en')!;
    expect(reading.rows[0]!.minutesPerWeek).toBe(99);
    expect(reading.minutesPerWeek).toBe(99);
  });

  it('has no multiplication anywhere in the feature', () => {
    // `minutes_per_week` is derived once on the server so that a screen, a printed sheet and
    // §12.1's extract cannot each round it differently. A second implementation here is how one
    // figure appears on a tablet and another in the research record.
    for (const file of featureFiles) {
      const source = featureCode(file);
      expect(/[\w)\]]\s*\*\s*[\w(]/.test(source), `${file} multiplies`).toBe(false);
      expect(/times_per_week\s*\*|timesPerWeek\s*\*/.test(source), file).toBe(false);
    }
  });

  it('reads in the order the server stored, so a reprint matches the sheet handed over', () => {
    const shuffled = plan({ items: [...plan().items].reverse() });
    expect(planReading(shuffled, 'en')!.rows.map((row) => row.code)).toEqual([
      'WALK_FLAT',
      'CHAIR_STAND',
    ]);
  });

  it('takes the instruction as required, because the contract now guarantees it', () => {
    // `ExercisePlanItem.how_en`/`how_bn` were optional in the schema while `Exercise`'s were
    // required — the same joined column typed two ways, and it was the plan item that gets
    // printed and handed over. With that corrected there is nothing to defend against, and the
    // `?? ''` that used to sit here would now only hide a contract regression.
    const source = featureCode('state.ts');
    expect(/how_en\s*\?\?/.test(source)).toBe(false);
    expect(/how_bn\s*\?\?/.test(source)).toBe(false);
    // The invariant is the database's, not this file's: a row that reads in only one language is
    // still reported honestly rather than drawn as a blank line.
    const oneLanguage = plan({
      items: [{ ...plan().items[0]!, how_bn: '' }],
    });
    const row = planReading(oneLanguage, 'bn')!.rows[0]!;
    expect(row.how.ownLanguage).toBe(false);
    expect(row.how.language).toBe('en');
  });

  it('carries the instruction the patient is handed, and the frozen assessment', () => {
    const reading = planReading(plan(), 'en')!;
    expect(reading.rows[0]!.how.text).toContain('talk but not sing');
    expect(reading.assessmentId).toBe('assessment-1');
    expect(reading.superseded).toBe(false);
    expect(planReading(plan({ status: 'SUPERSEDED' }), 'en')!.superseded).toBe(true);
    expect(reading.unapproved).toBe(2);
    expect(planReading(null, 'en')).toBeNull();
  });
});

// --- both languages ---

describe('both languages render, everywhere the server writes words', () => {
  it('asks the question in Bangla', () => {
    const rows = questionRows(CONDITIONS, emptyAssessment(), 'bn');
    expect(rows[0]!.question.text).toContain('মনোফিলামেন্ট');
    expect(rows[0]!.question.ownLanguage).toBe(true);
    expect(rows[0]!.name.text).toBe('তীব্র পেরিফেরাল নিউরোপ্যাথি');
  });

  it('offers the exercise and its instruction in Bangla', () => {
    const rows = offerRows(options(), 'bn');
    expect(rows[0]!.name.text).toBe('সমতল জায়গায় হাঁটা');
    expect(rows[0]!.how.text).toContain('গান গাওয়া যায় না');
    expect(rows[0]!.how.ownLanguage).toBe(true);
  });

  it('names the exclusion’s condition in Bangla', () => {
    expect(exclusionReading(options(), 'bn')!.reasons[0]!.name.text).toBe(
      'তীব্র পেরিফেরাল নিউরোপ্যাথি',
    );
    expect(exclusionReading(options(), 'en')!.reasons[0]!.name.text).toBe(
      'Severe peripheral neuropathy',
    );
  });

  it('prints the sheet in Bangla', () => {
    // Criterion 3. What it requires of the data is that every exercise carries `how_bn` — the
    // instruction the patient reads at home with nobody to ask.
    const reading = planReading(plan(), 'bn')!;
    expect(reading.rows[0]!.name.text).toBe('সমতল জায়গায় হাঁটা');
    expect(reading.rows[0]!.how.text).toContain('গান গাওয়া যায় না');
    for (const row of reading.rows) {
      expect(/[ঀ-৿]/.test(row.how.text), row.code).toBe(true);
    }
  });

  it('says so plainly when a row reads in only one language', () => {
    // Invariant 88 refuses one, so this is an edge case rather than a shape to design around.
    // A Bangla-reading specialist handed English with no explanation is one who thinks the app
    // has switched languages on them.
    const half = wordingOf('Walking', '', 'bn');
    expect(half.text).toBe('Walking');
    expect(half.language).toBe('en');
    expect(half.ownLanguage).toBe(false);
    expect(wordingOf('', '', 'bn')).toEqual({ text: '', language: null, ownLanguage: false });
  });

  it('has every sentence this station can draw in both message files', () => {
    // The keys chosen by a value rather than written as a literal, which `i18n.test.ts` cannot
    // see. A missing one is a blank line beside a refusal.
    const english = flatten(en as Record<string, unknown>);
    const bangla = flatten(bn as Record<string, unknown>);

    const keys = [
      ...STEPS.map((step) => `exercise.step.${step}`),
      ...WALKS_ANSWERS.map((answer) => `exercise.mobility.walksAnswer.${answer}`),
      ...WALKS_ANSWERS.map((answer) => `exercise.findings.walks.${answer}`),
      ...TARGET_PROBLEMS.map((problem) => `exercise.target.problem.${problem}`),
      ...EXCLUSION_STATUSES.map((status) =>
        status === 'APPLIES' ? 'exercise.exclusion.applies' : 'exercise.exclusion.notAsked',
      ),
      ...REVIEWS.flatMap((review) => [
        `exercise.review.${review}.title`,
        `exercise.review.${review}.body`,
      ]),
      'exercise.mobility.problem.notANumber',
      'exercise.mobility.problem.outOfRange',
      'exercise.exclusion.unasked',
      'exercise.findings.asked',
      'exercise.offers.refused',
      'exercise.questions.newlyAdded',
      'exercise.questions.cancel',
      'exercise.trouble.refused',
      'exercise.trouble.unreachable',
      'exercise.trouble.failed',
      'exercise.trouble.onRow',
      'exercise.advice.ask',
      'exercise.advice.reread',
      'exercise.advice.review',
      'exercise.advice.retry',
      'exercise.kind.AEROBIC',
      'exercise.kind.RESISTANCE',
      'exercise.kind.FLEXIBILITY',
      'exercise.kind.BALANCE',
      'exercise.intensity.LOW',
      'exercise.intensity.MODERATE',
      'exercise.intensity.VIGOROUS',
      'screen.exercise',
    ];
    for (const key of keys) {
      expect(english.has(key), `${key} in English`).toBe(true);
      expect(bangla.has(key), `${key} in Bangla`).toBe(true);
    }
  });
});

// --- who did it ---

describe('the findings and the plan are separate acts by possibly separate people', () => {
  it('attributes the plan to whoever issued it, not to whoever took the assessment', () => {
    const provenance = ofExercisePlan(
      plan({
        issued_by: 'staff-2',
        issued_role: 'EXERCISE_SPECIALIST',
        issued_by_code: 'EX-02',
        issued_by_name_en: 'Rehana Akter',
        issued_by_name_bn: 'রেহানা আক্তার',
      }),
    );
    expect(provenance.by).toBe('staff-2');
    expect(provenance.at).toBe('2026-09-05T04:45:00Z');
    expect(provenance.station).toBe('STN_EXERCISE');
    expect(provenance.named).toEqual({
      code: 'EX-02',
      nameEN: 'Rehana Akter',
      nameBN: 'রেহানা আক্তার',
    });
    // The assessment names somebody else, and the two must not be confused: the specialist who
    // asked the questions is often not the one who chose the targets.
    expect(ofExerciseAssessment(assessment()).by).toBe('staff-1');
  });

  it('reads a superseded assessment as a correction with no corrector named', () => {
    // The person who asked again is the author of the assessment that replaced this one, and
    // this payload does not carry them. A name here would be the earlier author's.
    const provenance = ofExerciseAssessment(assessment({ status: 'SUPERSEDED' }));
    expect(provenance.correction?.kind).toBe('superseded');
    expect(provenance.correction?.by).toBe('');
    expect(provenance.correction?.named).toBeNull();
    expect(ofExerciseAssessment(assessment()).correction).toBeNull();
    expect(ofExercisePlan(null).by).toBe('');
    expect(ofExerciseAssessment(undefined).by).toBe('');
  });
});

// --- the findings, read back ---

describe('what was answered is drawn beside the list it filtered', () => {
  it('names the conditions from the catalogue, and by code when it cannot', () => {
    const reading = assessmentReading(
      assessment({ contraindications: ['SEVERE_NEUROPATHY', 'NOT_IN_CATALOGUE'] }),
      CONDITIONS,
      'bn',
    )!;
    expect(reading.conditions[0]!.name.text).toBe('তীব্র পেরিফেরাল নিউরোপ্যাথি');
    // A finding this tablet cannot name is still a finding that filtered the list, so it is
    // shown by its code rather than dropped.
    expect(reading.conditions[1]!.code).toBe('NOT_IN_CATALOGUE');
    expect(reading.conditions[1]!.name.text).toBe('NOT_IN_CATALOGUE');
  });

  it('says nothing was found when nothing was', () => {
    const reading = assessmentReading(assessment({ contraindications: [] }), CONDITIONS, 'en')!;
    expect(reading.conditions).toEqual([]);
    expect(assessmentReading(null, CONDITIONS, 'en')).toBeNull();
  });
});
