import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { afterEach, describe, expect, it, vi } from 'vitest';
import { createTranslator } from 'use-intl/core';

import {
  ApiError,
  NetworkError,
  gapInvalidations,
  queryKeys,
  realtimeInvalidations,
  type RealtimeMessage,
} from '@dthcms/api-client';

import bn from '../src/messages/bn.json';
import en from '../src/messages/en.json';
import {
  ADVICE,
  BASES,
  CLINIC_UTC_OFFSET_MINUTES,
  MAX_WINDOW_DAYS,
  MIN_WINDOW_DAYS,
  NOTE_STATES,
  NOTICES,
  OUTCOMES,
  PROBLEMS,
  QUALITY_FLAG_RAISED,
  QUALITY_FLAG_RESOLVED,
  QUALITY_KINDS,
  RECENT_DECISION_MS,
  SECTIONS,
  WINDOW_DAYS,
  adviceFor,
  answeredOf,
  byCodeOf,
  byHourOf,
  byReasonOf,
  clinicDay,
  clockHour,
  counted,
  flagReadingOf,
  hasOpenNote,
  myTopic,
  noteStateOf,
  notesOf,
  noticeOf,
  questionsOf,
  rateOf,
  retentionOf,
  thresholdReadingsOf,
  windowDaysFor,
  windowOf,
  workDone,
  type QualityFlag,
  type QualityRecord,
  type QualityRule,
  type Trouble,
} from '../src/features/quality/state';

/*
 * The station binding reaches the Keystore through lib/credentials, and the native module
 * cannot load under Node. Mocked exactly as corrections.test.ts does; nothing here exercises it.
 */
vi.mock('expo-secure-store', () => ({
  setItemAsync: vi.fn(async () => undefined),
  getItemAsync: vi.fn(async () => null),
  deleteItemAsync: vi.fn(async () => undefined),
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'WHEN_UNLOCKED_THIS_DEVICE_ONLY',
}));

const qualityApi = await import('../src/features/quality/api');
const {
  QUALITY_QUERY_PREFIX,
  getMyQualityRecord,
  listQualityThresholds,
  myQualityQueryKey,
  troubleOf,
} = qualityApi;

/**
 * The operator's own quality record (CP63, §4.3, ADR-0029).
 *
 * The plan states the risk this checkpoint carries in its own words — *a metric that feels
 * punitive damages data honesty; staff hide errors instead of correcting them* — and this file
 * is where the things that follow from it stop being intentions.
 *
 *   1. A count cannot reach a screen without its denominator — and every breakdown now has its
 *      own, so an hour is counted against that hour's entries rather than against a shared
 *      total. Without that, the end-of-shift pattern reads a rota as a person.
 *   2. A missing rate says how many more values it needs. Arithmetic, not a verdict.
 *   3. `rejected` sits beside `upheld`; `overridden` — a supervisor's fix — is drawn apart from
 *      both, which is the whole reason CP62 made it a different event.
 *   4. A note that has been answered stays on the record and says what was decided, by whom
 *      and why. The ambush this checkpoint exists to prevent has two ends.
 *   5. No name anywhere in the feature grades a person.
 *
 * Each is a test rather than a paragraph, because a paragraph is what gets skipped on the
 * afternoon somebody adds one more figure to the screen.
 */

const root = dirname(fileURLToPath(import.meta.url));
const featureDir = join(root, '..', 'src', 'features', 'quality');

function source(file: string): string {
  return readFileSync(join(featureDir, file), 'utf8');
}

/**
 * The same file with its comments taken out.
 *
 * Several of the rules below are about what the code does, and the comments beside that code
 * are where the rule is *explained* — naming the permissions this feature must not check, and
 * quoting the words it must not use. A grep over the raw file would fail on its own
 * documentation, and the fix somebody would reach for is deleting the explanation.
 */
function code(file: string): string {
  return source(file)
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/.*$/gm, '$1');
}

/** The `quality` namespace as this test reads it: a tree of sentences, at most one level deep. */
type Say = Record<string, Record<string, string>>;

function record(over: Partial<QualityRecord> = {}): QualityRecord {
  return {
    operator_id: 'e7f1c2d3-0000-4000-8000-000000000001',
    window: { from: '2026-08-06T04:00:00Z', to: '2026-09-05T04:00:00Z', days: 30 },
    entries: 412,
    corrections: 4,
    upheld: 1,
    overridden: 1,
    rejected: 1,
    open: 1,
    rate: 0.5,
    rate_floor: 20,
    answered_flags_kept_days: 30,
    by_reason: [],
    by_code: [],
    by_hour: [],
    flags: [],
    ...over,
  };
}

function flag(over: Partial<QualityFlag> = {}): QualityFlag {
  return {
    id: 'a1b2c3d4-0000-4000-8000-000000000001',
    facility_id: 'f0000000-0000-4000-8000-000000000001',
    operator_id: 'e7f1c2d3-0000-4000-8000-000000000001',
    threshold_code: 'TRANSCRIPTION_3_IN_30',
    threshold_en: 'Three or more transcription corrections in thirty days',
    threshold_bn: 'ত্রিশ দিনে তিন বা তার বেশি লেখার ভুল সংশোধন',
    action_en: 'Sit with them at the station for one session.',
    action_bn: 'একটি সেশনে তাঁর পাশে বসুন।',
    threshold_approved: false,
    raised_at: '2026-09-02T09:15:00Z',
    window: { from: '2026-08-03T09:15:00Z', to: '2026-09-02T09:15:00Z', days: 30 },
    observed_count: 3,
    entries_count: 412,
    evidence: [],
    status: 'OPEN',
    ...over,
  };
}

function rule(over: Partial<QualityRule['threshold']> = {}, sentences = {}): QualityRule {
  return {
    threshold: {
      code: 'TRANSCRIPTION_3_IN_30',
      pattern: 'TRANSCRIPTION',
      window_days: 30,
      min_count: 3,
      min_entries: 20,
      display_en: 'Three or more transcription corrections in thirty days',
      display_bn: 'ত্রিশ দিনে তিন বা তার বেশি লেখার ভুল সংশোধন',
      action_en: 'Sit with them at the station.',
      action_bn: 'তাঁর পাশে বসুন।',
      approved: false,
      ordering: 10,
      ...over,
    },
    looks_for_en: '3 in 30 days, transcription reasons only, at least 20 entries',
    looks_for_bn: '৩০ দিনে ৩টি, কেবল লেখার ভুলের কারণ, অন্তত ২০টি এন্ট্রি',
    ...sentences,
  };
}

// --- 1. a count cannot exist without its denominator, and each has its own ---

describe('a count cannot be produced without its denominator', () => {
  it('mints a figure when both halves are there', () => {
    const figure = counted(3, 412);
    expect(figure).not.toBeNull();
    expect(figure?.count).toBe(3);
    expect(figure?.outOf).toBe(412);
  });

  it('refuses a count with no denominator', () => {
    expect(counted(3, undefined)).toBeNull();
    expect(counted(3, null)).toBeNull();
  });

  it('refuses a denominator with no count', () => {
    expect(counted(undefined, 412)).toBeNull();
    expect(counted(null, 412)).toBeNull();
  });

  it('refuses a count larger than the total it came out of', () => {
    // Not pedantry: it is the shape a figure takes when two windows have been mixed up, and
    // "5 of 3" on a screen about somebody's work is worse than nothing at all.
    expect(counted(5, 3)).toBeNull();
  });

  it('refuses negatives and values that are not numbers', () => {
    expect(counted(-1, 10)).toBeNull();
    expect(counted(1, -10)).toBeNull();
    expect(counted(Number.NaN, 10)).toBeNull();
    expect(counted(1, Number.POSITIVE_INFINITY)).toBeNull();
  });

  it('allows nothing out of nothing, which is a true sentence on a first morning', () => {
    expect(counted(0, 0)).not.toBeNull();
  });

  it('never hands the screen a bare count for anything the server counted', () => {
    /*
     * The structural half of criterion 1. Every count about this operator's work reaches the
     * glass as a `Counted`, so the screen has no field to render on its own — and a future
     * edit that reaches for `record.corrections` to draw "4" fails here rather than in a
     * clinic. `entries` is the deliberate exception: it is the denominator itself.
     */
    const screen = source('MyQualityRecord.tsx');
    for (const field of [
      'corrections',
      'upheld',
      'overridden',
      'rejected',
      'observed_count',
      'entries_count',
      'by_reason',
      'by_code',
      'by_hour',
      'flags',
    ]) {
      expect(screen, `MyQualityRecord.tsx reads .${field} directly`).not.toMatch(
        new RegExp(`\\.${field}\\b`),
      );
    }
  });
});

describe('every breakdown is counted against its own denominator', () => {
  it('counts a reason against the questions asked, because a reason has no total of its own', () => {
    const rows = byReasonOf(
      record({
        corrections: 3,
        by_reason: [
          {
            reason_code: 'TRANSCRIPTION',
            display_en: 'Transcription',
            display_bn: 'লেখার ভুল',
            transcription: true,
            corrections: 2,
          },
        ],
      }),
      'en',
    );
    expect(rows[0]?.figure.outOf).toBe(3);
    expect(rows[0]?.basis).toBe('questions');
  });

  it('counts a measurement against how many of that measurement were taken', () => {
    /*
     * Three corrections on a weight is a different fact when somebody weighed four hundred
     * people and when they weighed nine. Against the correction total it was neither.
     */
    const rows = byCodeOf(
      record({
        corrections: 4,
        by_code: [
          {
            code: 'BODY_WEIGHT',
            display_en: 'Weight',
            display_bn: 'ওজন',
            corrections: 3,
            entries: 380,
          },
        ],
      }),
      'en',
    );
    expect(rows[0]?.figure.outOf).toBe(380);
    expect(rows[0]?.figure.outOf).not.toBe(4);
    expect(rows[0]?.basis).toBe('measurements');
  });

  it('counts an hour against that hour’s own entries, so a rota does not read as a person', () => {
    /*
     * The one that mattered most. An operator who works only the late shift will always
     * cluster late, and END_OF_SHIFT would flag them for the rota. "3 of 47 values you entered
     * at four in the afternoon" is a question somebody can answer; "3" is not.
     */
    const rows = byHourOf(
      record({ corrections: 4, by_hour: [{ hour: 16, corrections: 3, entries: 47 }] }),
    );
    expect(rows[0]?.label).toBe('16:00');
    expect(rows[0]?.figure.outOf).toBe(47);
    expect(rows[0]?.basis).toBe('hour');
  });

  it('says which denominator each row is against, and the screen has a sentence for each', () => {
    const english = en.quality as unknown as Record<string, string>;
    const bangla = bn.quality as unknown as Record<string, string>;
    for (const basis of BASES) {
      const key = {
        questions: 'patternReasonNote',
        measurements: 'patternCodeNote',
        hour: 'patternHourNote',
      }[basis];
      expect(english[key], `en ${key}`).toBeTypeOf('string');
      expect(bangla[key], `bn ${key}`).toBeTypeOf('string');
    }
  });

  it('names a measurement in the reader’s language now that the server sends a pair', () => {
    const rows = byCodeOf(
      record({
        corrections: 1,
        by_code: [
          {
            code: 'BODY_WEIGHT',
            display_en: 'Weight',
            display_bn: 'ওজন',
            corrections: 1,
            entries: 9,
          },
        ],
      }),
      'bn',
    );
    expect(rows[0]?.label).toBe('ওজন');
  });

  it('falls back to the raw code when the registry named nothing', () => {
    const rows = byCodeOf(
      record({
        corrections: 1,
        by_code: [
          { code: 'BODY_WEIGHT', display_en: '', display_bn: '', corrections: 1, entries: 9 },
        ],
      }),
      'bn',
    );
    expect(rows[0]?.label).toBe('BODY_WEIGHT');
  });

  it('drops a row whose count has no total to sit under', () => {
    const rows = byCodeOf(
      record({
        corrections: 1,
        by_code: [{ code: 'A', display_en: 'A', display_bn: 'এ', corrections: 4, entries: 1 }],
      }),
      'en',
    );
    expect(rows).toEqual([]);
  });

  it('drops an hour it cannot read rather than drawing a blank row', () => {
    const rows = byHourOf(
      record({ corrections: 2, by_hour: [{ hour: 99, corrections: 2, entries: 9 }] }),
    );
    expect(rows).toEqual([]);
  });

  it('keeps the server’s order and never sorts a person’s record by size', () => {
    // The payload comes back in stable code order now rather than count-descending, which is
    // the order this screen wants. Nothing here re-ranks it back.
    const rows = byCodeOf(
      record({
        corrections: 6,
        by_code: [
          { code: 'A', display_en: 'A', display_bn: 'এ', corrections: 1, entries: 10 },
          { code: 'B', display_en: 'B', display_bn: 'বি', corrections: 5, entries: 10 },
        ],
      }),
      'en',
    );
    expect(rows.map((row) => row.label)).toEqual(['A', 'B']);
  });
});

// --- 2. a missing rate is arithmetic ---

describe('a rate is the server’s, or it is how many more values it needs', () => {
  it('reports the rate the server sent', () => {
    expect(rateOf(record({ rate: 1.4 }))).toEqual({ known: true, perHundred: 1.4 });
  });

  it('reports a genuine zero as a number, not as an absence', () => {
    // The `!rate` this function is written to avoid. An operator with a clean month must not
    // be told their record could not be worked out.
    expect(rateOf(record({ entries: 400, corrections: 0, rate: 0 }))).toEqual({
      known: true,
      perHundred: 0,
    });
  });

  it('names the shortfall when the server withheld one', () => {
    /*
     * "Eight more values this month and a rate will appear" and "too few" are the same fact.
     * Only one of them reads as arithmetic rather than as an opinion about the reader, and it
     * is only sayable because `rate_floor` is on the payload.
     */
    expect(rateOf(record({ entries: 12, rate: null, rate_floor: 20 }))).toEqual({
      known: false,
      entries: 12,
      floor: 20,
      needed: 8,
    });
  });

  it('asks for no more values when the floor is already met', () => {
    // Should not happen — the server would have sent a rate — but a negative shortfall would
    // render as "minus three more values", which is worse than the generic sentence.
    expect(rateOf(record({ entries: 40, rate: null, rate_floor: 20 })).known).toBe(false);
    const missed = rateOf(record({ entries: 40, rate: null, rate_floor: 20 }));
    expect(missed.known === false && missed.needed).toBe(0);
  });

  it('computes no rate of its own, at any entry count', () => {
    // The floor is the server's and this phone does not hold a copy. A client that divided
    // would show a rate on the day the clinic raised the floor and the server stopped.
    expect(rateOf(record({ entries: 4000, corrections: 40, upheld: 40, rate: null })).known).toBe(
      false,
    );
    expect(code('state.ts'), 'state.ts divides').not.toMatch(/\* 100|\/ entries|100 \//);
  });

  it('has both sentences, and neither is a dash or a zero', () => {
    const english = en.quality as unknown as Record<string, string>;
    for (const key of ['rateSoon', 'rateNotYet']) {
      const sentence = english[key] ?? '';
      expect(sentence.length, `en ${key}`).toBeGreaterThan(15);
      expect(sentence, `en ${key}`).not.toMatch(/^[\s0—–-]*$/);
    }
    expect(english.rateSoon).toContain('{needed');
  });
});

// --- 3. the operator's own two answers, and the supervisor's kept apart ---

describe('an operator who defended a correct reading is doing the job', () => {
  it('draws rejected immediately after upheld', () => {
    const upheld = OUTCOMES.indexOf('upheld');
    const rejected = OUTCOMES.indexOf('rejected');
    expect(upheld).toBeGreaterThanOrEqual(0);
    expect(rejected).toBe(upheld + 1);
  });

  it('keeps a supervisor’s fix apart from the operator’s own', () => {
    /*
     * CP62 made `SUPERVISOR_OVERRIDE_APPLIED` a different event precisely so that an
     * operator's record would not read a supervisor's fix as though they had put it right
     * themselves. Counting it into `upheld` threw that away and told somebody they had put
     * right three values when they had put right one.
     */
    const rows = answeredOf(
      record({ corrections: 4, upheld: 1, rejected: 1, overridden: 1, open: 1 }),
    );
    const upheld = rows.find((row) => row.outcome === 'upheld');
    const overridden = rows.find((row) => row.outcome === 'overridden');
    expect(upheld?.figure.count).toBe(1);
    expect(overridden?.figure.count).toBe(1);
    expect(OUTCOMES).toContain('overridden');
  });

  it('gives all four the same shape, so none can be drawn as a total', () => {
    const rows = answeredOf(
      record({ corrections: 4, upheld: 1, rejected: 1, overridden: 1, open: 1 }),
    );
    expect(rows.map((row) => row.outcome)).toEqual(['upheld', 'rejected', 'overridden', 'open']);
    for (const row of rows) expect(row.figure.outOf).toBe(4);
  });

  it('shows each answer out of the questions asked, never out of the entries', () => {
    const rows = answeredOf(record({ entries: 412, corrections: 4 }));
    // Out of `entries` each of these would read as a rate, and four rates about one person on
    // one screen is a scorecard.
    for (const row of rows) expect(row.figure.outOf).not.toBe(412);
  });

  it('drops an answer the server could not put a denominator under', () => {
    const broken = record({ corrections: 1, upheld: 2, overridden: 0, rejected: 0, open: 0 });
    expect(answeredOf(broken).map((row) => row.outcome)).toEqual([
      'rejected',
      'overridden',
      'open',
    ]);
  });

  it('names all four in both languages, and says the two that are not the operator’s doing', () => {
    const enOutcome = (en.quality as unknown as Say).outcome ?? {};
    const bnOutcome = (bn.quality as unknown as Say).outcome ?? {};
    for (const outcome of OUTCOMES) {
      expect(enOutcome[outcome], `en outcome.${outcome}`).toBeTypeOf('string');
      expect(bnOutcome[outcome], `bn outcome.${outcome}`).toBeTypeOf('string');
    }
    // The wording has to make clear whose act each was, or "put right: 1" and "put right by
    // somebody else: 1" read as the same claim twice.
    expect(enOutcome.upheld).toMatch(/you/i);
    expect(enOutcome.overridden).toMatch(/supervisor/i);
  });

  it('says out loud that saying a value stands is part of the job', () => {
    const english = (en.quality as unknown as Record<string, string>).defending ?? '';
    const bangla = (bn.quality as unknown as Record<string, string>).defending ?? '';
    expect(english.length).toBeGreaterThan(40);
    expect(bangla.length).toBeGreaterThan(20);
  });
});

// --- 4. a note that has been answered stays, and says what was decided ---

describe('a note carries its denominator and its approval on its face', () => {
  it('renders the server’s own words in the reader’s language', () => {
    expect(flagReadingOf(flag(), 'en').threshold).toContain('transcription');
    expect(flagReadingOf(flag(), 'bn').threshold).toMatch(/[ঀ-৿]/);
    expect(flagReadingOf(flag(), 'bn').action).toMatch(/[ঀ-৿]/);
  });

  it('falls back to the other language rather than drawing a wordless note', () => {
    const oneSided = flag({ threshold_bn: '', action_bn: '' });
    expect(flagReadingOf(oneSided, 'bn').threshold).toContain('transcription');
  });

  it('carries the frozen count with the frozen denominator', () => {
    const reading = flagReadingOf(flag({ observed_count: 3, entries_count: 412 }), 'en');
    expect(reading.figure?.count).toBe(3);
    expect(reading.figure?.outOf).toBe(412);
  });

  it('carries the unapproved state rather than hiding it', () => {
    expect(flagReadingOf(flag(), 'en').approved).toBe(false);
    expect(flagReadingOf(flag({ threshold_approved: true }), 'en').approved).toBe(true);
  });

  it('treats a missing approval flag as unapproved', () => {
    const missing = flag() as { threshold_approved?: boolean };
    // Not present on the wire at all: the safe reading is the one that does not turn a
    // proposal into a policy.
    delete missing.threshold_approved;
    expect(flagReadingOf(missing as QualityFlag, 'en').approved).toBe(false);
  });

  it('labels its own frozen window, which is not the window the counts follow', () => {
    // Inherent to a record that freezes its evidence: the counts follow the window asked for,
    // a note's counts follow the threshold's. Both say which, rather than one being assumed.
    const reading = flagReadingOf(flag(), 'en');
    expect(reading.windowDays).toBe(30);
    expect((en.quality as unknown as Record<string, string>).noteOver).toContain('{days');
  });
});

describe('a note somebody answered stays on the record and says what was decided', () => {
  const answered = flag({
    id: 'answered',
    status: 'ACKNOWLEDGED',
    resolved_at: '2026-09-04T05:00:00Z',
    resolved_by_code: 'DR-014',
    resolved_by_name_en: 'Dr Nahid Rahman',
    resolved_by_name_bn: 'ডা. নাহিদ রহমান',
    resolution: 'We changed the tape and I sat with them on Tuesday.',
  });

  it('reads open, acknowledged and dismissed apart', () => {
    expect(noteStateOf(flag())).toBe('open');
    expect(noteStateOf(flag({ status: 'ACKNOWLEDGED' }))).toBe('acknowledged');
    expect(noteStateOf(flag({ status: 'DISMISSED' }))).toBe('dismissed');
  });

  it('treats a status it has never met as still open', () => {
    // The safe reading: a note this build cannot classify is one still waiting on somebody,
    // which keeps it visible rather than quietly filing it as settled.
    expect(noteStateOf({ status: 'ESCALATED' })).toBe('open');
    expect(noteStateOf({})).toBe('open');
  });

  it('names who decided, when, and why in their own words', () => {
    const reading = flagReadingOf(answered, 'en');
    expect(reading.state).toBe('acknowledged');
    expect(reading.decidedBy).toBe('Dr Nahid Rahman');
    expect(reading.decidedOn).toBe('2026-09-04');
    expect(reading.reason).toContain('sat with them');
  });

  it('names the supervisor in the reader’s language, falling back to their staff code', () => {
    expect(flagReadingOf(answered, 'bn').decidedBy).toMatch(/[ঀ-৿]/);
    const unnamed = flagReadingOf(
      flag({ ...answered, resolved_by_name_en: '', resolved_by_name_bn: '' }),
      'en',
    );
    expect(unnamed.decidedBy).toBe('DR-014');
  });

  it('puts the ones still waiting first, then the freshest decision', () => {
    /*
     * Two groups, because they are two different things to read. An open note is a
     * conversation that has not happened and the oldest has waited longest; an answered note
     * is news and the freshest decision is the one worth reading.
     */
    const notes = notesOf(
      record({
        flags: [
          flag({ id: 'answered-old', status: 'DISMISSED', resolved_at: '2026-08-20T09:00:00Z' }),
          flag({ id: 'open-new', raised_at: '2026-09-04T09:00:00Z' }),
          flag({ id: 'answered-new', status: 'DISMISSED', resolved_at: '2026-09-03T09:00:00Z' }),
          flag({ id: 'open-old', raised_at: '2026-08-10T09:00:00Z' }),
        ],
      }),
      'en',
    );
    expect(notes.map((note) => note.id)).toEqual([
      'open-old',
      'open-new',
      'answered-new',
      'answered-old',
    ]);
  });

  it('knows whether anything is still waiting on somebody', () => {
    expect(hasOpenNote(record())).toBe(false);
    expect(hasOpenNote(record({ flags: [flag()] }))).toBe(true);
    expect(hasOpenNote(record({ flags: [answered] }))).toBe(false);
    expect(hasOpenNote(undefined)).toBe(false);
  });

  it('has a sentence for each decision, in both languages, and neither reads as a verdict', () => {
    const enState = (en.quality as unknown as Say).noteState ?? {};
    const bnState = (bn.quality as unknown as Say).noteState ?? {};
    for (const state of NOTE_STATES) {
      if (state === 'open') continue;
      expect(enState[state], `en noteState.${state}`).toBeTypeOf('string');
      expect(bnState[state], `bn noteState.${state}`).toBeTypeOf('string');
    }
  });

  it('draws an answered note differently from an open one', () => {
    const screen = source('MyQualityRecord.tsx');
    expect(screen).toMatch(/note\.state === 'open'/);
    expect(screen, 'the decision is drawn').toMatch(/noteState\.\$\{note\.state\}/);
  });

  it('stops offering the supervisor’s suggested action once they have acted', () => {
    // Printing "what this suggests a supervisor does" underneath what they actually did would
    // read as a second opinion about their judgement.
    expect(source('MyQualityRecord.tsx')).toMatch(
      /note\.action === '' \|\| note\.state !== 'open'/,
    );
  });
});

describe('the evidence a note was written on', () => {
  const withEvidence = flag({
    evidence: [
      {
        request_id: 'r-1',
        reason_code: 'TRANSCRIPTION',
        reason_en: 'Transcription',
        reason_bn: 'লেখার ভুল',
        code: 'BODY_WEIGHT',
        code_en: 'Weight',
        code_bn: 'ওজন',
        at: '2026-08-28T10:30:00Z',
        status: 'OPEN',
        status_as_of: '2026-09-02T09:15:00Z',
        hour: 16,
      },
    ],
  });

  it('reads the reason and the measurement in the reader’s language', () => {
    const [item] = flagReadingOf(withEvidence, 'bn').evidence;
    expect(item?.reason).toBe('লেখার ভুল');
    expect(item?.measurement).toBe('ওজন');
  });

  it('falls back to the raw codes rather than showing a blank row', () => {
    const bare = flag({
      evidence: [
        {
          request_id: 'r-1',
          reason_code: 'TRANSCRIPTION',
          code: 'BODY_WEIGHT',
          at: '2026-08-28T10:30:00Z',
          status: 'OPEN',
          status_as_of: '2026-09-02T09:15:00Z',
          hour: 16,
        },
      ],
    });
    const [item] = flagReadingOf(bare, 'en').evidence;
    expect(item?.reason).toBe('TRANSCRIPTION');
    expect(item?.measurement).toBe('BODY_WEIGHT');
  });

  it('says when the frozen status was true, so it is not read as current', () => {
    const reading = flagReadingOf(withEvidence, 'en');
    expect(reading.frozenOn).toBe('2026-09-02');
    expect((en.quality as unknown as Record<string, string>).noteEvidenceAsOf).toContain('{date');
  });

  it('carries one frozen date for the whole note rather than one per row', () => {
    /*
     * `status_as_of` is the raise moment and is identical on every row of a flag — the
     * contract says so and says to render it once. A copy under each line implied it varied,
     * which is the reading that sends somebody looking for the fresher row. The rows keep
     * `wasStatus`, which genuinely does differ between them.
     */
    const reading = flagReadingOf(withEvidence, 'en');
    expect(reading.evidence[0]).not.toHaveProperty('wasStatusOn');
    // The frozen `status` is the one coded value on this payload with no display pair beside
    // it, so it is not carried out to a reading at all rather than drawn as a raw word.
    expect(reading.evidence[0]).not.toHaveProperty('wasStatus');
    const drawn = source('MyQualityRecord.tsx');
    const perRow = drawn.indexOf('note.evidence.map');
    const once = drawn.indexOf("t('noteEvidenceAsOf'");
    expect(once, 'the date is drawn beneath the rows, not inside the map').toBeGreaterThan(
      drawn.indexOf('))}', perRow),
    );
  });

  it('falls back to the raise day when a note carries no evidence to read it from', () => {
    // Which is what it is: `status_as_of` is the raise moment. A note with nothing behind it
    // should not lose the one date that says when its reading was true.
    expect(flagReadingOf(flag({ evidence: [] }), 'en').frozenOn).toBe('2026-09-02');
  });

  it('carries no patient and no measured value', () => {
    // A supervisor reading a patient's values through their staff's record would be reading
    // clinical data through a side door. A database invariant refuses a row that carries
    // either; this is the client end of the same rule.
    const rendered = JSON.stringify(flagReadingOf(withEvidence, 'en').evidence).toLowerCase();
    expect(rendered).not.toMatch(/patient|\bnid\b|"value"/);
  });
});

describe('an answered note says how long it will keep appearing', () => {
  it('reads the server’s number rather than promising one of its own', () => {
    /*
     * The same rule as `rate_floor`, for the same reason: a sentence in a message file about
     * how long something stays goes on being said after somebody changes the rule, and this
     * one is a promise made to the person the notes are about.
     */
    expect(retentionOf(record({ answered_flags_kept_days: 30 }))).toBe(30);
    expect(retentionOf(record({ answered_flags_kept_days: 14 }))).toBe(14);
  });

  it('says nothing at all rather than guessing when the server sent no number', () => {
    // A wrong promise about how long somebody's record keeps a decision is worse than none.
    const missing = record() as { answered_flags_kept_days?: number };
    delete missing.answered_flags_kept_days;
    expect(retentionOf(missing as QualityRecord)).toBe(0);
    expect(retentionOf(record({ answered_flags_kept_days: 0 }))).toBe(0);
    expect(retentionOf(undefined)).toBe(0);
    expect(source('MyQualityRecord.tsx')).toMatch(/keptDays === 0 \? null/);
  });

  it('has the sentence in both languages, with the number in the reader’s numerals', () => {
    const english = (en.quality as unknown as Record<string, string>).noteKept ?? '';
    const bangla = (bn.quality as unknown as Record<string, string>).noteKept ?? '';
    expect(english).toContain('{days, number}');
    expect(bangla).toContain('{days, number}');
  });

  it('holds no hard-coded retention of its own anywhere in the feature', () => {
    for (const file of ['state.ts', 'api.ts', 'MyQualityRecord.tsx', 'QualityNotice.tsx']) {
      expect(code(file), `${file} hardcodes a retention`).not.toMatch(/thirty days|30 days/);
    }
    for (const language of [en, bn]) {
      const sentence = (language.quality as unknown as Record<string, string>).noteKept ?? '';
      expect(sentence, 'the number is interpolated, not written out').not.toMatch(/30|৩০/);
    }
  });
});

describe('the window scopes what happened in it and hides nothing that is open', () => {
  it('keeps an open note raised long before the window began', () => {
    /*
     * The server now returns every open note whatever span was asked for, so a fortnight's
     * record still carries one raised two months ago that nobody has answered. Nothing here
     * filters or compensates: a note disappearing because somebody narrowed a window would be
     * the same silent vanishing the answered ones are kept to prevent.
     */
    const narrow = record({
      window: { from: '2026-08-29T04:00:00Z', to: '2026-09-05T04:00:00Z', days: 7 },
      flags: [flag({ id: 'old-and-open', raised_at: '2026-07-02T09:15:00Z' })],
    });
    expect(notesOf(narrow, 'en').map((note) => note.id)).toEqual(['old-and-open']);
    expect(hasOpenNote(narrow)).toBe(true);
    expect(noticeOf(narrow)).toBe('raised');
  });

  it('drops nothing the server sent, in either direction', () => {
    const spread = record({
      flags: [
        flag({ id: 'a', raised_at: '2025-01-01T00:00:00Z' }),
        flag({ id: 'b', raised_at: '2027-01-01T00:00:00Z' }),
      ],
    });
    expect(notesOf(spread, 'en')).toHaveLength(2);
    expect(code('state.ts'), 'state.ts filters notes by date').not.toMatch(
      /filter\([^)]*raised|window\.from/,
    );
  });
});

describe('the shell says something once, and stops', () => {
  it('speaks while a note is open', () => {
    expect(noticeOf(record({ flags: [flag()] }))).toBe('raised');
  });

  it('speaks for a day after somebody answers one', () => {
    const decided = record({
      window: { from: '2026-08-06T04:00:00Z', to: '2026-09-05T04:00:00Z', days: 30 },
      flags: [flag({ status: 'DISMISSED', resolved_at: '2026-09-04T20:00:00Z' })],
    });
    expect(noticeOf(decided)).toBe('answered');
  });

  it('stops once the decision is older than that', () => {
    const stale = record({
      flags: [flag({ status: 'DISMISSED', resolved_at: '2026-08-20T09:00:00Z' })],
    });
    expect(noticeOf(stale)).toBe('none');
    expect(RECENT_DECISION_MS).toBe(24 * 60 * 60_000);
  });

  it('prefers the open note when both are true', () => {
    const both = record({
      flags: [
        flag({ id: 'answered', status: 'DISMISSED', resolved_at: '2026-09-04T20:00:00Z' }),
        flag({ id: 'open' }),
      ],
    });
    expect(noticeOf(both)).toBe('raised');
  });

  it('says nothing at all when there is nothing to say', () => {
    expect(noticeOf(record())).toBe('none');
    expect(noticeOf(undefined)).toBe('none');
  });

  it('reads "now" off the record rather than off this tablet’s clock', () => {
    // A station tablet with a wrong clock would otherwise nag forever or never speak, and
    // neither failure would look like a clock.
    expect(code('state.ts')).not.toMatch(/Date\.now\(\)|new Date\(\)/);
    const noWindow = record({ flags: [flag({ status: 'DISMISSED', resolved_at: '' })] });
    noWindow.window = { from: '', to: '', days: 0 };
    expect(noticeOf(noWindow)).toBe('none');
  });

  it('has a sentence for each thing it can say, in both languages, and no count on either', () => {
    const enNotice = (en.quality as unknown as Say).notice ?? {};
    const bnNotice = (bn.quality as unknown as Say).notice ?? {};
    for (const notice of NOTICES) {
      if (notice === 'none') continue;
      expect(enNotice[notice], `en notice.${notice}`).toBeTypeOf('string');
      expect(bnNotice[notice], `bn notice.${notice}`).toBeTypeOf('string');
      // The one place a count belongs is beside its denominator, and a shell banner is not
      // the place to put both.
      expect(enNotice[notice], `en notice.${notice} carries a number`).not.toMatch(/\{|\d/);
      expect(bnNotice[notice], `bn notice.${notice} carries a number`).not.toMatch(/\{|\d/);
    }
  });
});

// --- 5. nothing here grades a person ---

/**
 * The words a quality record must not use about the person it is about.
 *
 * Checked against exported names, test identifiers and every message this feature ships, in
 * both languages. Comments are deliberately **not** checked: they are where the rule is
 * explained, and the explanation has to be able to quote the plan's own risk line and the
 * words it is refusing.
 */
const GRADING_WORDS = [
  'error',
  'mistake',
  'score',
  'accuracy',
  'accurate',
  'performance',
  'grade',
  'rank',
  'fault',
  'blame',
  'careless',
  'negligen',
  'punish',
  'discipline',
  'penalty',
  'offender',
  'failure',
  'worst',
];

/**
 * The one name that contains a forbidden word and is allowed to.
 *
 * `notBlame` is the sentence that denies the accusation, and it is the name three finished
 * features already give it. Banning the word here would ban the denial along with the thing
 * denied, and the screen would lose the line whose whole job is to say what it is not.
 */
const ALLOWED_BY_DESIGN: Record<string, string> = {
  notBlame: 'The sentence that denies it. The same name attribution and corrections both use.',
};

function grading(name: string): string[] {
  if (name in ALLOWED_BY_DESIGN) return [];
  const lowered = name.toLowerCase();
  return GRADING_WORDS.filter((word) => lowered.includes(word));
}

describe('no name in this feature grades anybody', () => {
  it('has no exported name that does', async () => {
    // Static paths, not a template: Metro and Vite both want the extension resolvable at
    // build time, and a warning nobody can act on is a warning people learn to scroll past.
    const modules: Record<string, Record<string, unknown>> = {
      'state.ts': await import('../src/features/quality/state'),
      'api.ts': qualityApi,
    };
    for (const [module, loaded] of Object.entries(modules)) {
      const names = Object.keys(loaded);
      expect(names.length, `${module} exports something`).toBeGreaterThan(3);
      for (const name of names) {
        expect(grading(name), `${module} exports ${name}`).toEqual([]);
      }
    }
  });

  it('has no exported name in the barrel that does, including the types', () => {
    /*
     * Read rather than imported. `index.ts` re-exports the two `.tsx` files, which pull in
     * React Native and cannot load in this container — and a type-only export has no runtime
     * name to walk anyway, which is exactly where a `QualityScore` would hide.
     */
    const names = [...code('index.ts').matchAll(/^\s{2}(?:type\s+)?([A-Za-z_$][\w$]*)/gm)].map(
      (match) => match[1] ?? '',
    );
    expect(names.length, 'the barrel exports something').toBeGreaterThan(10);
    for (const name of names) expect(grading(name), `index.ts exports ${name}`).toEqual([]);
  });

  it('has no test identifier that does', () => {
    // A `testID` is a label a person reads in a bug report and an accessibility tool speaks
    // out loud. It is as public as anything on the screen.
    for (const file of ['MyQualityRecord.tsx', 'QualityNotice.tsx']) {
      for (const match of source(file).matchAll(/testID={?["`]([^"`{}]+)/g)) {
        expect(grading(match[1] ?? ''), `${file} has testID ${match[1]}`).toEqual([]);
      }
    }
  });

  it('has no message key that does, in either language', () => {
    for (const [language, tree] of [
      ['en', en.quality as unknown],
      ['bn', bn.quality as unknown],
    ] as const) {
      for (const [key, value] of Object.entries(tree as Record<string, unknown>)) {
        expect(grading(key), `${language} key ${key}`).toEqual([]);
        if (typeof value !== 'object' || value === null) continue;
        for (const inner of Object.keys(value)) {
          expect(grading(inner), `${language} key ${key}.${inner}`).toEqual([]);
        }
      }
    }
  });

  it('has no English sentence that does', () => {
    for (const [key, value] of Object.entries(en.quality as unknown as Record<string, unknown>)) {
      const sentences = typeof value === 'string' ? [value] : Object.values(value as object);
      for (const sentence of sentences) {
        expect(grading(String(sentence)), `en quality.${key}`).toEqual([]);
      }
    }
  });
});

// --- what the record says, before any of it is drawn ---

describe('the work done is the first thing the screen has to draw', () => {
  it('puts the work at the top of the section order', () => {
    expect(SECTIONS[0]).toBe('work');
  });

  it('puts the questions received after it', () => {
    expect(SECTIONS.indexOf('questions')).toBeGreaterThan(SECTIONS.indexOf('work'));
    expect(SECTIONS.indexOf('answered')).toBeGreaterThan(SECTIONS.indexOf('questions'));
  });

  it('puts a note where it is neither the first thing read nor something to scroll for', () => {
    const notes = SECTIONS.indexOf('notes');
    expect(notes).toBeGreaterThan(0);
    expect(notes).toBeLessThan(SECTIONS.length - 1);
  });

  it('reads the entry count and refuses to invent one', () => {
    expect(workDone(record({ entries: 412 }))).toBe(412);
    expect(workDone(undefined)).toBe(0);
    expect(workDone(record({ entries: -3 }))).toBe(0);
  });

  it('shows the questions out of the entries', () => {
    const figure = questionsOf(record({ entries: 412, corrections: 4 }));
    expect(figure?.count).toBe(4);
    expect(figure?.outOf).toBe(412);
  });
});

describe('the window is a denominator too, and the screen cannot ask for a bad one', () => {
  it('reads the window the server sent, in clinic time', () => {
    const reading = windowOf(record());
    expect(reading.days).toBe(30);
    expect(reading.known).toBe(true);
    expect(reading.to).toBe('2026-09-05');
  });

  it('says it does not know rather than guessing', () => {
    const broken = record();
    broken.window = { from: '', to: '', days: 0 };
    expect(windowOf(broken).known).toBe(false);
  });

  it('asks for the window the plan verifies against, inside the contract’s bounds', () => {
    expect(WINDOW_DAYS).toBe(30);
    expect(WINDOW_DAYS).toBeGreaterThanOrEqual(MIN_WINDOW_DAYS);
    expect(WINDOW_DAYS).toBeLessThanOrEqual(MAX_WINDOW_DAYS);
  });

  it('cannot send a window the contract refuses', () => {
    /*
     * `?days` outside 1–365 is a 422 now rather than a silent thirty, which is right — a
     * client asking for a fortnight and being handed a month has two windows pretending to be
     * one. This is what makes it impossible for this application to be that client.
     */
    expect(windowDaysFor(0)).toBe(MIN_WINDOW_DAYS);
    expect(windowDaysFor(-5)).toBe(MIN_WINDOW_DAYS);
    expect(windowDaysFor(4000)).toBe(MAX_WINDOW_DAYS);
    expect(windowDaysFor(Number.NaN)).toBe(WINDOW_DAYS);
    expect(windowDaysFor(undefined)).toBe(WINDOW_DAYS);
    expect(windowDaysFor(14.7)).toBe(14);
  });

  it('keys the cache on the guarded window, so a key and its request cannot disagree', () => {
    expect(myQualityQueryKey(30)).not.toEqual(myQualityQueryKey(14));
    expect(myQualityQueryKey(0)).toEqual(myQualityQueryKey(MIN_WINDOW_DAYS));
  });
});

// --- the rules, readable before one is applied ---

describe('the rules can be read before one is ever applied', () => {
  it('renders the server’s own sentence rather than one built here', () => {
    /*
     * The server renders `looks_for_*` from the live row precisely so a threshold whose count
     * changes cannot leave a description of the old one behind. Rebuilding it from the row's
     * numbers — which is what this did while the field was English only — would put that
     * staleness straight back, in two languages instead of one.
     */
    const [english] = thresholdReadingsOf([rule()], 'en');
    const [bangla] = thresholdReadingsOf([rule()], 'bn');
    expect(english?.looksFor).toBe('3 in 30 days, transcription reasons only, at least 20 entries');
    expect(bangla?.looksFor).toMatch(/[০-৯]/);
    expect(bangla?.display).toMatch(/[ঀ-৿]/);
  });

  it('has deleted the sentence this feature used to compose', () => {
    for (const language of [en, bn]) {
      expect(Object.keys(language.quality as object)).not.toContain('ruleLooksFor');
    }
    expect(code('MyQualityRecord.tsx')).not.toMatch(/ruleLooksFor/);
  });

  it('carries the unapproved state of every rule out to the screen', () => {
    // False on everything this system ships with, and reported rather than inferred from a
    // null timestamp — the contract states it as a boolean so a client cannot forget the null.
    expect(thresholdReadingsOf([rule()], 'en')[0]?.approved).toBe(false);
    expect(thresholdReadingsOf([rule({ approved: true })], 'en')[0]?.approved).toBe(true);
  });

  it('survives a row the server sent half of', () => {
    expect(thresholdReadingsOf([{} as QualityRule], 'en')[0]?.code).toBe('');
    expect(thresholdReadingsOf(undefined, 'en')).toEqual([]);
  });
});

// --- the clinic's clock ---

describe('the clinic’s clock', () => {
  it('matches Asia/Dhaka', () => {
    const at = new Date('2026-09-05T00:00:00Z');
    const dhaka = new Intl.DateTimeFormat('en-GB', {
      timeZone: 'Asia/Dhaka',
      hour: '2-digit',
      hour12: false,
    }).format(at);
    expect(Number(dhaka)).toBe(CLINIC_UTC_OFFSET_MINUTES / 60);
  });

  it('reads a day in clinic time, and empties one it cannot read', () => {
    expect(clinicDay('2026-09-04T20:00:00Z')).toBe('2026-09-05');
    expect(clinicDay('not a date')).toBe('');
    expect(clinicDay(undefined)).toBe('');
  });

  it('reads an hour, and empties one outside the day', () => {
    expect(clockHour(9)).toBe('09:00');
    expect(clockHour(0)).toBe('00:00');
    expect(clockHour(24)).toBe('');
    expect(clockHour(null)).toBe('');
  });
});

// --- being told on the device of the person it is about ---

describe('a note reaches the operator it is about', () => {
  const me = 'e7f1c2d3-0000-4000-8000-000000000001';

  function message(kind: string, topic: string): RealtimeMessage {
    return { seq: 1, topic, kind, at: '2026-09-05T04:00:00Z', summary: {} };
  }

  it('subscribes to the operator’s own topic', () => {
    expect(myTopic(me)).toBe(`user:${me}`);
    expect(myTopic('')).toBe('');
  });

  it('names the two kinds the gateway publishes about a record', () => {
    expect(QUALITY_FLAG_RAISED).toBe('quality.flag_raised');
    expect(QUALITY_FLAG_RESOLVED).toBe('quality.flag_resolved');
    expect(QUALITY_KINDS).toHaveLength(2);
  });

  it('has both kinds reach this feature’s cache through the shared map', () => {
    /*
     * The map lives in another package now, and the failure it would produce if it stopped
     * covering these is a record sitting stale on the one device the note is about — which is
     * exactly the ambush this checkpoint exists to prevent. So the mobile side asserts it
     * rather than trusting it.
     */
    for (const kind of QUALITY_KINDS) {
      const keys = realtimeInvalidations(message(kind, `user:${me}`)).map((key) => key.join('/'));
      expect(keys, `${kind} reaches the record`).toContain(queryKeys.quality().join('/'));
    }
  });

  it('re-reads after a dropped connection, which is how somebody hears it from a person first', () => {
    const keys = gapInvalidations([`user:${me}`]).map((key) => key.join('/'));
    expect(keys).toContain(queryKeys.quality().join('/'));
  });

  it('keys this feature under exactly the prefix the shared map invalidates', () => {
    // Two spellings of one prefix is the failure the shared map exists to prevent, and it
    // looks like "the tablet updates and the dashboard does not".
    expect([...QUALITY_QUERY_PREFIX]).toEqual([...queryKeys.quality()]);
    expect(myQualityQueryKey(30).slice(0, 1)).toEqual([...queryKeys.quality()]);
  });

  it('carries no listener of its own any more', () => {
    for (const file of ['MyQualityRecord.tsx', 'QualityNotice.tsx']) {
      expect(code(file), `${file} keeps a private listener`).not.toMatch(/useRealtimeMessages/);
      expect(code(file), `${file} subscribes`).toMatch(/useRealtimeTopics/);
    }
  });
});

// --- what went wrong ---

describe('a read that does not come back', () => {
  function refusal(status: number): InstanceType<typeof ApiError> {
    return new ApiError({
      status,
      code: 'FORBIDDEN',
      kind: 'forbidden',
      messageEN: 'No.',
      messageBN: 'না।',
      correlationID: 'req-1',
    });
  }

  it('names an unreachable server rather than a refusal', () => {
    const trouble = troubleOf(new NetworkError('down'), 'en');
    expect(trouble.kind).toBe('unreachable');
    expect(adviceFor(trouble)).toBe('waitForLink');
  });

  it('reports a refusal in the server’s own words, in the reader’s language', () => {
    expect(troubleOf(refusal(403), 'bn').message).toBe('না।');
    expect(troubleOf(refusal(403), 'en').message).toBe('No.');
  });

  it('treats a 403 on your own record as something to report, not a hat to change', () => {
    /*
     * `/v1/quality/me` takes no permission at all, deliberately (ADR-0029 §3). There is no
     * role that would help, so there is no advice that tells an operator to switch one — and
     * a screen that said "you are not allowed to see your own record, try another role" would
     * be exactly the sentence the endpoint's design exists to make impossible.
     */
    const trouble = troubleOf(refusal(403), 'en');
    expect(trouble.kind).toBe('refused');
    expect(adviceFor(trouble)).toBe('tellSomebody');
    expect([...ADVICE] as string[]).not.toContain('switchRole');
  });

  it('treats a refused window as something for whoever looks after the system', () => {
    // A 422 here can only mean this build asked for a window the contract refuses, which
    // `windowDaysFor` exists to make impossible. It is not something to ask an operator at a
    // station to do differently.
    const trouble = troubleOf(refusal(422), 'en');
    expect(trouble.kind).toBe('refused');
    expect(adviceFor(trouble)).toBe('tellSomebody');
  });

  it('treats anything else as something to try again', () => {
    expect(adviceFor(troubleOf(refusal(500), 'en'))).toBe('tryAgain');
    expect(adviceFor(troubleOf(new Error('?'), 'en'))).toBe('tryAgain');
  });

  it('has a sentence for every kind and every piece of advice, in both languages', () => {
    const enTrouble = (en.quality as unknown as Say).trouble ?? {};
    const bnTrouble = (bn.quality as unknown as Say).trouble ?? {};
    const enAdvice = (en.quality as unknown as Say).advice ?? {};
    const bnAdvice = (bn.quality as unknown as Say).advice ?? {};
    for (const problem of PROBLEMS) {
      expect(enTrouble[problem], `en trouble.${problem}`).toBeTypeOf('string');
      expect(bnTrouble[problem], `bn trouble.${problem}`).toBeTypeOf('string');
    }
    for (const advice of ADVICE) {
      expect(enAdvice[advice], `en advice.${advice}`).toBeTypeOf('string');
      expect(bnAdvice[advice], `bn advice.${advice}`).toBeTypeOf('string');
    }
  });
});

// --- the two reads, as they go over the wire ---

/** Stubs the network and hands back what the client actually sent. */
function stubFetch(...responses: Response[]): { url: string; method: string }[] {
  const calls: { url: string; method: string }[] = [];
  let index = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: Request) => {
      calls.push({ url: input.url, method: input.method });
      const response = responses[Math.min(index, responses.length - 1)];
      index += 1;
      return response!.clone();
    }),
  );
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

describe('the two reads', () => {
  it('asks for the caller’s own record over the window, and names nobody', async () => {
    const calls = stubFetch(json({ record: record(), mine: true }));
    const answer = await getMyQualityRecord();
    expect(answer.entries).toBe(412);
    expect(calls[0]?.method).toBe('GET');
    expect(calls[0]?.url).toContain('/v1/quality/me');
    expect(calls[0]?.url).toContain('days=30');
    // No id anywhere on the request. The caller's own is read off the session on the server,
    // which is what makes "there is no version of this that returns somebody else's work" a
    // property of the endpoint rather than a promise made by this client.
    expect(calls[0]?.url).not.toContain('operator');
  });

  it('sends a window the contract accepts even when handed one it does not', async () => {
    const calls = stubFetch(json({ record: record(), mine: true }));
    await getMyQualityRecord(9999);
    expect(calls[0]?.url).toContain(`days=${MAX_WINDOW_DAYS}`);
    expect(calls[0]?.url).not.toContain('days=9999');
  });

  it('keeps the threshold and its sentence together', async () => {
    const calls = stubFetch(json({ thresholds: [rule()] }));
    const answer = await listQualityThresholds();
    expect(answer).toHaveLength(1);
    expect(answer[0]?.threshold.code).toBe('TRANSCRIPTION_3_IN_30');
    expect(answer[0]?.looks_for_bn).toMatch(/[০-৯]/);
    expect(calls[0]?.url).toContain('/v1/quality/thresholds');
  });
});

// --- the two languages, as they actually render ---

describe('both languages render a figure the operator can read', () => {
  /*
   * ICU's simple argument — `{count}` — is `String(value)`, and its typed one —
   * `{count, number}` — goes through `Intl.NumberFormat`. In Bangla those are two different
   * sentences: "৪১২টির মধ্যে 3টি" and "৪১২টির মধ্যে ৩টি". The first is what this screen shipped
   * with until it was rendered and read, and it is the kind of thing a message-file test that
   * only compares keys never catches.
   */
  type Render = (key: string, values: Record<string, number>) => string;

  // `createTranslator` types its keys off the message tree it is handed, and this test hands
  // it a tree chosen at runtime. The cast is what lets one function render both files.
  const translator = createTranslator as unknown as (config: {
    locale: string;
    messages: unknown;
    namespace: string;
  }) => Render;

  function render(locale: 'en' | 'bn', key: string, values: Record<string, number>): string {
    const messages = locale === 'bn' ? bn : en;
    return translator({ locale, messages, namespace: 'quality' })(key, values);
  }

  it('writes both halves of a figure in the reader’s numerals', () => {
    expect(render('en', 'figure', { count: 3, outOf: 412 })).toBe('3 of 412');
    const bangla = render('bn', 'figure', { count: 3, outOf: 412 });
    expect(bangla).toContain('৩');
    expect(bangla).toContain('৪১২');
    expect(bangla, 'a Latin digit left inside a Bangla sentence').not.toMatch(/[0-9]/);
  });

  it('writes the work done and the shortfall in the reader’s numerals too', () => {
    expect(render('bn', 'work', { days: 30 })).not.toMatch(/[0-9]/);
    expect(render('bn', 'rateSoon', { needed: 8, floor: 20 })).not.toMatch(/[0-9]/);
    expect(render('bn', 'rateKnown', { rate: 0.2 })).not.toMatch(/[0-9]/);
    expect(render('en', 'rateSoon', { needed: 8, floor: 20 })).toContain('8');
  });
});

// --- what this feature deliberately cannot do ---

describe('the supervisor’s half of CP63 is not on this phone', () => {
  it('binds no endpoint that needs a permission', () => {
    const api = code('api.ts');
    for (const path of [
      '/v1/quality/operators',
      '/v1/quality/flags',
      '/v1/quality/flags/{id}/resolve',
    ]) {
      expect(api, `api.ts binds ${path}`).not.toMatch(new RegExp(`GET\\('${path}`));
      expect(api, `api.ts binds ${path}`).not.toMatch(new RegExp(`POST\\('${path}`));
      expect(api, `api.ts names ${path} in code`).not.toMatch(new RegExp(`'${path}`));
    }
  });

  it('has no function anywhere in the feature that takes an operator id', () => {
    // The structural guarantee behind "an operator sees their own record and nothing else":
    // the caller's id comes off the session on the server, and there is no argument here that
    // could name anybody else.
    for (const file of ['api.ts', 'state.ts']) {
      expect(code(file), `${file} takes an operator id`).not.toMatch(/operatorId|operator_id:/);
    }
  });

  it('checks no permission before reading the caller’s own record', () => {
    /*
     * Asking an operator to hold a permission before they may see their own record is how a
     * count starts to feel like something being kept from them. A client-side gate would be a
     * locked control in front of the one person entitled to press it.
     */
    for (const file of ['api.ts', 'state.ts', 'MyQualityRecord.tsx', 'QualityNotice.tsx']) {
      expect(code(file), `${file} gates on a permission`).not.toMatch(
        /quality\.read\.team|quality\.flag\.resolve|hasPermission|usePermission|permissions/,
      );
    }
  });

  it('writes nothing at all', () => {
    for (const file of ['api.ts', 'MyQualityRecord.tsx', 'QualityNotice.tsx']) {
      expect(code(file), `${file} writes`).not.toMatch(/api\.(POST|PUT|PATCH|DELETE)\(/);
    }
  });
});

describe('the screen is sized for a hand and holds no colour of its own', () => {
  it('uses the 48dp touch target on every control it draws', () => {
    for (const file of ['MyQualityRecord.tsx', 'QualityNotice.tsx']) {
      const text = source(file);
      // `touchTarget` is 48 and `touchTargetCompact` is 44; the trailing comma is what keeps
      // the second from being counted as the first. Nothing on this screen wears the compact
      // one — it runs on cheap Android tablets, tapped one-handed and often in a hurry.
      const pressables = (text.match(/<Pressable/g) ?? []).length;
      const targets = (text.match(/theme\.size\.touchTarget,/g) ?? []).length;
      expect(pressables, `${file} draws controls`).toBeGreaterThan(0);
      expect(targets, `${file} sizes every control`).toBe(pressables);
      expect(text, `${file} uses the compact target`).not.toMatch(/touchTargetCompact/);
    }
  });

  it('reaches no web storage of any kind (ADR-0010)', () => {
    for (const file of ['api.ts', 'state.ts', 'MyQualityRecord.tsx', 'QualityNotice.tsx']) {
      expect(source(file)).not.toMatch(/localStorage|sessionStorage|AsyncStorage/);
    }
  });
});

// --- the shape of a Trouble, kept honest ---

describe('a trouble names its kind and nothing about a person', () => {
  it('carries only a kind, a status and the server’s sentence', () => {
    const trouble: Trouble = troubleOf(new NetworkError('down'), 'en');
    expect(Object.keys(trouble).sort()).toEqual(['kind', 'message', 'status']);
  });
});
