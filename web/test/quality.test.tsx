import { ApiError, type components } from '@dthcms/api-client';
import { screen, within } from '@testing-library/react';
import { createTranslator } from 'next-intl';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { Directory } from '@/features/attribution';

import en from '../messages/en.json';
import bn from '../messages/bn.json';

import { renderWithProviders } from './render';

/**
 * The operator quality record (CP63, §4.3, ADR-0029).
 *
 * The manual verification is that recurring patterns surface so somebody can act on them, and
 * the plan states the risk in the same breath: *"a metric that feels punitive damages data
 * honesty — staff hide errors instead of correcting them."* What can be proven from a screen
 * is whether the interface makes that outcome harder or easier, and every way it gets it wrong
 * is quiet, plausible, and looks like a working feature:
 *
 *  - **A count with no denominator.** "Three corrections" is not a fact about anybody. Three
 *    out of four hundred and twelve entries and three out of forty are different facts and
 *    only one is a question. There are named tests below for the rendering, for the type — a
 *    numerator without a denominator must not compile — and for the message files, where the
 *    same mistake can be reintroduced without touching a component.
 *  - **A null rate drawn as `0` or as a dash.** The server declines to compute one below its
 *    own floor of entries. Zero is a claim about somebody's month; a dash is a claim the
 *    answer went missing. Both are false, and both look tidier than the truth.
 *  - **`rejected` folded into a total.** An operator who defended a correct reading and was
 *    right is doing the job. Added into one number it becomes indistinguishable from a
 *    mistyped height, and every operator learns to accept every flag without looking.
 *  - **An unapproved threshold drawn as policy.** Every number this system ships with is a
 *    proposal no clinician has agreed to. A flag that travelled without saying so — quoted,
 *    printed, photographed and shown to the person it is about — would be a statement of
 *    clinic policy nobody made.
 *  - **A dismissal with nothing said.** It is the only act that takes a pattern off a
 *    colleague's record, and a record of unexplained dismissals is one nobody can review.
 *  - **A list that ranks people.** Position in a named list is a claim, whatever the headings
 *    say. The server sorts by correction count; this screen does not render that order unless
 *    a supervisor asks for it in so many words.
 *  - **A word that grades somebody.** The vocabulary is where a feature like this goes wrong
 *    first, well before the arithmetic. There is an audit of the module's exports and of every
 *    English string in its namespace, the way `corrections.test.tsx` audits its own.
 */

type QualityRecord = components['schemas']['QualityRecord'];
type QualityFlag = components['schemas']['QualityFlag'];
type QualityOperator = components['schemas']['QualityOperator'];
type QualityThreshold = components['schemas']['QualityThreshold'];
type ObservationCode = components['schemas']['ObservationCode'];

/** The operator whose month this is. */
const KAMAL = '0190a8f2-0000-7000-8000-0000000000c1';
/** A colleague, alphabetically before him, with more corrections against her name. */
const AMENA = '0190a8f2-0000-7000-8000-0000000000c2';
/** A colleague who recorded nine hundred values and had nothing come back on any of them. */
const QUIET = '0190a8f2-0000-7000-8000-0000000000c3';
/** The chief consultant, who supervises all three. */
const NAHID = '0190a8f2-0000-7000-8000-0000000000c9';

const FLAG = '0190a8f2-0000-7000-8000-0000000000f1';
const SECOND_FLAG = '0190a8f2-0000-7000-8000-0000000000f2';

/** A uuid anywhere on a screen. Nothing on this surface may render one. */
const UUID = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i;

const getMyRecord = vi.hoisted(() => vi.fn());
const getOperatorRecord = vi.hoisted(() => vi.fn());
const listOperators = vi.hoisted(() => vi.fn());
const listFlags = vi.hoisted(() => vi.fn());
const listThresholds = vi.hoisted(() => vi.fn());
const answerFlag = vi.hoisted(() => vi.fn());

/*
 * Partial: the network calls are stubbed, the rules are not. `operatorsInOrder`,
 * `flagAnswerReady` and `rateIsAvailable` are what these screens *are* — what order a
 * supervisor is handed, whether a dismissal may be sent, whether there is a rate to draw —
 * and a test that stubbed them would prove the components call a function rather than that
 * the right sentence reaches the person the numbers are about.
 */
vi.mock('@/features/quality/api/quality', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/quality/api/quality')>()),
  getMyRecord,
  getOperatorRecord,
  listOperators,
  listFlags,
  listThresholds,
  answerFlag,
}));

const listObservationCodes = vi.hoisted(() => vi.fn());
vi.mock('@/features/observations/api/observations', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/observations/api/observations')>()),
  listObservationCodes,
}));

const { CorrectionRate } = await import('@/features/quality/components/CorrectionRate');
const { QualityFlagCard } = await import('@/features/quality/components/QualityFlagCard');
const { MyQualityRecord } = await import('@/features/quality/components/MyQualityRecord');
const { SupervisorQuality } = await import('@/features/quality/components/SupervisorQuality');
const { ThresholdList } = await import('@/features/quality/components/ThresholdList');
// The barrel, imported as a caller would.
const surface = await import('@/features/quality');
const { useSessionStore } = await import('@/stores/session');

/* ------------------------------------------------------------------------- */

const CODES: ObservationCode[] = [
  {
    code: 'BODY_HEIGHT',
    category: 'ANTHRO',
    value_type: 'numeric',
    display_en: 'Height',
    display_bn: 'উচ্চতা',
    write_permission: 'observation.write.anthro',
    canonical_unit: 'cm',
    units: [],
  },
];

function record(over: Partial<QualityRecord> = {}): QualityRecord {
  return {
    operator_id: KAMAL,
    window: { from: '2026-08-05T00:00:00Z', to: '2026-09-04T00:00:00Z', days: 30 },
    operator_code: 'A014',
    operator_name_en: 'Kamal Hossain',
    operator_name_bn: 'কামাল হোসেন',
    operator_status: 'active',
    entries: 412,
    corrections: 3,
    upheld: 2,
    overridden: 0,
    rejected: 1,
    open: 0,
    rate: 0.5,
    rate_floor: 20,
    answered_flags_kept_days: 30,
    by_reason: [
      {
        reason_code: 'TRANSCRIPTION',
        display_en: 'Typed a different number from the one read',
        display_bn: 'যা পড়া হয়েছিল তার চেয়ে অন্য সংখ্যা লেখা হয়েছে',
        transcription: true,
        corrections: 2,
      },
      {
        reason_code: 'REMEASURED',
        display_en: 'Measured again and the first reading was different',
        display_bn: 'আবার মাপা হয়েছে, প্রথম পাঠটি আলাদা ছিল',
        transcription: false,
        corrections: 1,
      },
    ],
    by_code: [
      {
        code: 'BODY_HEIGHT',
        display_en: 'Height',
        display_bn: 'উচ্চতা',
        corrections: 3,
        entries: 118,
      },
    ],
    by_hour: [
      { hour: 9, corrections: 1, entries: 260 },
      { hour: 16, corrections: 2, entries: 47 },
    ],
    flags: [],
    ...over,
  };
}

function flag(over: Partial<QualityFlag> = {}): QualityFlag {
  return {
    id: FLAG,
    facility_id: '11111111-1111-4111-8111-111111111111',
    operator_id: KAMAL,
    operator_code: 'A014',
    operator_name_en: 'Kamal Hossain',
    operator_name_bn: 'কামাল হোসেন',
    threshold_code: 'TRANSCRIPTION_3_IN_30',
    threshold_en: 'Three or more transcription corrections in thirty days',
    threshold_bn: 'ত্রিশ দিনে তিন বা তার বেশি লেখার ভুল সংশোধন',
    action_en:
      'Sit with them at the station for one session and watch how the reading is transferred to the screen.',
    action_bn: 'একটি সেশনে তাঁর পাশে বসে দেখুন যন্ত্রের পাঠ কীভাবে স্ক্রিনে তোলা হচ্ছে।',
    threshold_approved: false,
    raised_at: '2026-09-04T05:00:00Z',
    window: { from: '2026-08-05T00:00:00Z', to: '2026-09-04T00:00:00Z', days: 30 },
    observed_count: 3,
    entries_count: 412,
    evidence: [
      {
        request_id: '0190a8f2-0000-7000-8000-0000000000e1',
        reason_code: 'TRANSCRIPTION',
        reason_en: 'Typed a different number from the one read',
        reason_bn: 'যা পড়া হয়েছিল তার চেয়ে অন্য সংখ্যা লেখা হয়েছে',
        code: 'BODY_HEIGHT',
        code_en: 'Height',
        code_bn: 'উচ্চতা',
        at: '2026-09-03T11:00:00Z',
        status: 'APPLIED',
        status_as_of: '2026-09-04T05:00:00Z',
        hour: 16,
      },
      {
        request_id: '0190a8f2-0000-7000-8000-0000000000e2',
        reason_code: 'TRANSCRIPTION',
        reason_en: 'Typed a different number from the one read',
        reason_bn: 'যা পড়া হয়েছিল তার চেয়ে অন্য সংখ্যা লেখা হয়েছে',
        code: 'BODY_HEIGHT',
        code_en: 'Height',
        code_bn: 'উচ্চতা',
        at: '2026-08-29T11:00:00Z',
        status: 'OVERRIDDEN',
        status_as_of: '2026-09-04T05:00:00Z',
        hour: 17,
      },
    ],
    status: 'OPEN',
    ...over,
  };
}

function threshold(over: Partial<QualityThreshold> = {}): QualityThreshold {
  return {
    code: 'TRANSCRIPTION_3_IN_30',
    pattern: 'TRANSCRIPTION',
    window_days: 30,
    min_count: 3,
    min_entries: 20,
    display_en: 'Three or more transcription corrections in thirty days',
    display_bn: 'ত্রিশ দিনে তিন বা তার বেশি লেখার ভুল সংশোধন',
    action_en: 'Sit with them at the station for one session.',
    action_bn: 'একটি সেশনে তাঁর পাশে বসুন।',
    approved: false,
    ordering: 10,
    ...over,
  };
}

function operator(over: Partial<QualityOperator> = {}): QualityOperator {
  return {
    operator_id: KAMAL,
    employee_code: 'A014',
    name_en: 'Kamal Hossain',
    name_bn: 'কামাল হোসেন',
    status: 'active',
    corrections: 3,
    upheld: 2,
    overridden: 0,
    rejected: 1,
    open: 0,
    entries: 412,
    rate: 0.5,
    rate_floor: 20,
    open_flags: 0,
    ...over,
  };
}

function directory(): Directory {
  return {
    staff: [
      {
        id: KAMAL,
        code: 'A014',
        name_en: 'Kamal Hossain',
        name_bn: 'কামাল হোসেন',
        status: 'active',
      },
      { id: AMENA, code: 'A002', name_en: 'Amena Begum', name_bn: 'আমেনা বেগম', status: 'active' },
      {
        id: QUIET,
        code: 'A031',
        name_en: 'Zubair Khan',
        name_bn: 'জুবায়ের খান',
        status: 'active',
      },
      {
        id: NAHID,
        code: 'E010',
        name_en: 'Dr Nahid Rahman',
        name_bn: 'ডা. নাহিদ রহমান',
        status: 'active',
      },
    ],
    devices: [],
    stations: [],
    as_of: '2026-09-04T00:00:00Z',
  };
}

const initialSession = useSessionStore.getInitialState();

/** Somebody who records values and holds nothing else. The field worker's shape. */
const OPERATOR = ['observation.write.anthro'];
/** A supervisor who may read the team's records and answer a flag. */
const SUPERVISOR = ['quality.read.team', 'quality.flag.resolve', 'observation.read.values'];
/** A supervisor who may read but not answer — the open question ADR-0029 leaves for Dr Nahid. */
const READ_ONLY_SUPERVISOR = ['quality.read.team'];

function signedInAs(id: string, permissions: string[], role = 'ANTHROPOMETRY') {
  useSessionStore.setState({
    ...initialSession,
    status: 'authenticated',
    user: {
      id,
      employeeCode: 'A014',
      nameEN: 'Kamal Hossain',
      nameBN: 'কামাল হোসেন',
      facilityId: '11111111-1111-4111-8111-111111111111',
      roles: [role],
      grants: { [role]: permissions },
      permissions,
      secondFactor: { required: false, enrolled: true, pending: false, recoveryCodesLeft: 8 },
    },
    activeRole: role,
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  useSessionStore.setState(initialSession, true);
  signedInAs(KAMAL, OPERATOR);
  getMyRecord.mockResolvedValue(record());
  getOperatorRecord.mockResolvedValue({ record: record(), mine: false });
  listOperators.mockResolvedValue({
    operators: [operator()],
    window: { from: '2026-08-05T00:00:00Z', to: '2026-09-04T00:00:00Z', days: 30 },
  });
  listFlags.mockResolvedValue([]);
  listThresholds.mockResolvedValue([
    {
      threshold: threshold(),
      looks_for_en:
        '3 in 30 days, counting only the reasons that mean a number was mistyped, and only once there are at least 20 entries',
      looks_for_bn:
        '৩০ দিনে ৩ বার, শুধু যেসব কারণ বলে সংখ্যাটি ভুল লেখা হয়েছিল, এবং অন্তত ২০টি এন্ট্রি থাকলে তবেই',
    },
  ]);
  listObservationCodes.mockResolvedValue(CODES);
  answerFlag.mockResolvedValue(flag({ status: 'ACKNOWLEDGED' }));
});

afterEach(() => {
  vi.restoreAllMocks();
});

/* ------------------------------------------------------------------------- */

describe('a count is never drawn without what it came out of', () => {
  it('cannot be given a numerator without a denominator', () => {
    /*
     * The rule enforced by the compiler rather than by care. If `entries` were ever made
     * optional — the obvious "convenience" the day somebody has a count and not a
     * denominator to hand — this `@ts-expect-error` becomes unused and `tsc --noEmit`
     * fails, which is exactly the point: the shortcut cannot be taken quietly.
     */
    // @ts-expect-error - entries is required, and there is no rendering without it.
    const withoutADenominator = <CorrectionRate corrections={3} rate={null} />;
    expect(withoutADenominator).toBeTruthy();
  });

  it('draws both halves, whatever the numbers are', () => {
    for (const [corrections, entries] of [
      [0, 0],
      [3, 412],
      [1, 40],
      [11, 11],
    ] as const) {
      const view = renderWithProviders(
        <CorrectionRate corrections={corrections} entries={entries} rate={null} rateFloor={20} />,
      );
      const rate = screen.getByTestId('correction-rate');
      expect(rate).toHaveTextContent(String(corrections));
      expect(rate).toHaveTextContent(String(entries));
      view.unmount();
    }
  });

  it('has no message anywhere in the namespace that interpolates a count on its own', () => {
    /*
     * The component is not the only way this rule can be broken: a message file can
     * reintroduce a bare count without anybody touching a component, and the screen would
     * look right to whoever wrote it. So the pairs are checked as data, in both languages —
     * a numerator placeholder must always travel with its denominator.
     */
    const PAIRS: [string, string][] = [
      ['corrections', 'entries'],
      ['part', 'whole'],
    ];

    const offenders: string[] = [];
    for (const [language, tree] of [
      ['en', en.quality],
      ['bn', bn.quality],
    ] as const) {
      for (const [key, value] of flatten(tree as Record<string, unknown>)) {
        for (const [numerator, denominator] of PAIRS) {
          if (interpolates(value, numerator) && !interpolates(value, denominator)) {
            offenders.push(`${language}: quality.${key}`);
          }
        }
      }
    }

    expect(
      offenders,
      `These interpolate a count with nothing to read it against: ${offenders.join(', ')}`,
    ).toEqual([]);
  });

  it('has something to check', () => {
    // Guards the test above from passing vacuously if the namespace were ever emptied — or if
    // a placeholder grew an ICU type and the audit stopped recognising it, which is exactly
    // what happened when these counts became `{corrections, plural, …}`.
    const paired = [...flatten(en.quality as Record<string, unknown>)].filter(([, value]) =>
      interpolates(value, 'corrections'),
    );
    expect(paired.length).toBeGreaterThan(0);
  });

  it('puts the denominator on every line of the supervisor’s list', async () => {
    // A named list of counts with no denominators is a ranking of the worst regardless of
    // what the headings say. This is the assertion that the denominators are actually there.
    signedInAs(NAHID, SUPERVISOR, 'PHYSICIAN');
    renderWithProviders(<SupervisorQuality />, { directory: directory() });

    const row = await screen.findByTestId(`operator-rate-${KAMAL}`);
    expect(row).toHaveTextContent('3');
    expect(row).toHaveTextContent('412');
  });

  it('puts the denominator on a raised pattern, frozen with the count', async () => {
    renderWithProviders(<QualityFlagCard flag={flag()} />, { directory: directory() });

    const counts = await screen.findByTestId(`flag-counts-${FLAG}`);
    expect(counts).toHaveTextContent('3');
    expect(counts).toHaveTextContent('412');
  });
});

describe('a rate that could not be computed is words', () => {
  it('says how many more entries it would take, not a zero and not a dash', async () => {
    /*
     * The floor is on the payload now, so the sentence names a number the reader can count
     * towards. That is most of what makes this read as arithmetic rather than as a judgement:
     * "too few" is a verdict somebody cannot check, and "sixteen more values" is a rule.
     */
    getMyRecord.mockResolvedValue(
      record({ entries: 4, corrections: 1, upheld: 1, rate: null, rate_floor: 20 }),
    );
    renderWithProviders(<MyQualityRecord />, { directory: directory() });

    const absent = await screen.findByTestId('rate-too-few');
    expect(absent).toHaveTextContent('16');
    expect(absent.textContent ?? '').toMatch(/more entries/i);
    expect(screen.queryByTestId('rate-value')).toBeNull();
    // Not a dash, and not a zero standing in for an answer nobody has.
    expect(absent.textContent ?? '').not.toMatch(/^\s*[-–—]\s*$/);
    expect(absent.textContent ?? '').not.toMatch(/\b0(\.0)?\b/);
    // And the note beside it explains why there is no number rather than leaving a gap.
    expect(screen.getByTestId('rate-floor-note')).toBeInTheDocument();
  });

  it('falls back to words where there is no floor to count towards', async () => {
    // A reading with no floor — a flag has none, and neither would a shape the contract adds
    // next. The vaguer sentence is a fallback, never the thing a screen reaches for first.
    renderWithProviders(
      <CorrectionRate corrections={1} entries={4} rate={null} rateFloor={null} />,
    );
    expect(screen.getByTestId('rate-too-few').textContent ?? '').toMatch(/too few entries/i);
  });

  it('never says nought more entries are needed', async () => {
    // The server returns a rate the moment the floor is met, so this can only appear if the two
    // disagree — and "0 more entries and a rate will appear" beside no rate reads as nonsense.
    renderWithProviders(<CorrectionRate corrections={1} entries={40} rate={null} rateFloor={20} />);
    const absent = screen.getByTestId('rate-too-few');
    expect(absent.textContent ?? '').not.toMatch(/\b0\b/);
    expect(absent.textContent ?? '').toMatch(/too few entries/i);
  });

  it('does not claim there were too few entries on a pattern that never computed one', async () => {
    // A flag freezes a count and a denominator and never takes a ratio. Folding that into
    // the same absence would put "too few entries" under a flag raised on four hundred.
    renderWithProviders(<QualityFlagCard flag={flag()} />, { directory: directory() });

    await screen.findByTestId(`flag-counts-${FLAG}`);
    expect(screen.queryByTestId('rate-too-few')).toBeNull();
    expect(screen.queryByTestId('rate-value')).toBeNull();
  });

  it('draws the number when the server took one', async () => {
    renderWithProviders(<MyQualityRecord />, { directory: directory() });
    expect(await screen.findByTestId('rate-value')).toHaveTextContent('0.5');
  });

  it('needs no footnote about which corrections it counts', async () => {
    // The record's figure and the list's are the same arithmetic now — corrections that stood,
    // per hundred entries — so the two sentences that used to explain the difference are gone.
    // A number needing a footnote to be comparable with the number on the next screen is two
    // numbers wearing one word.
    renderWithProviders(<MyQualityRecord />, { directory: directory() });
    await screen.findByTestId('record-rate');
    expect(screen.queryByTestId('rate-counts-what')).toBeNull();
  });

  it('answers the question without a screen', async () => {
    expect(surface.rateIsAvailable({ rate: 0 })).toBe(true);
    expect(surface.rateIsAvailable({ rate: null })).toBe(false);
    expect(surface.rateIsAvailable({})).toBe(false);
  });
});

describe('a defended value sits beside a corrected one and is never folded into a total', () => {
  it('draws both, each against the same denominator', async () => {
    getMyRecord.mockResolvedValue(
      record({ corrections: 4, upheld: 2, overridden: 1, rejected: 1, open: 0 }),
    );
    renderWithProviders(<MyQualityRecord />, { directory: directory() });

    const upheld = await screen.findByTestId('outcome-upheld');
    const rejected = screen.getByTestId('outcome-rejected');

    expect(upheld).toHaveTextContent('2');
    expect(upheld).toHaveTextContent('4');
    expect(rejected).toHaveTextContent('1');
    expect(rejected).toHaveTextContent('4');
  });

  it('keeps a supervisor’s fix off the operator’s own line', async () => {
    /*
     * CP62 made a supervisor's fix a different event *precisely* so an operator's record would
     * not read it as though they had put it right themselves. A "put right" figure that quietly
     * included it would undo that at the last step, on the last screen.
     */
    getMyRecord.mockResolvedValue(
      record({ corrections: 4, upheld: 2, overridden: 1, rejected: 1, open: 0 }),
    );
    renderWithProviders(<MyQualityRecord />, { directory: directory() });

    const upheld = await screen.findByTestId('outcome-upheld');
    const overridden = screen.getByTestId('outcome-overridden');

    expect(upheld).toHaveTextContent(/the person who recorded them/i);
    expect(upheld).toHaveTextContent('2');
    expect(overridden).toHaveTextContent(/a supervisor/i);
    expect(overridden).toHaveTextContent('1');
    // Not three. The two are never added together anywhere on this screen.
    expect(upheld.textContent ?? '').not.toContain('3');
  });

  it('says that a value which stood is counted against nobody', async () => {
    renderWithProviders(<MyQualityRecord />, { directory: directory() });
    const note = await screen.findByTestId('stood-is-not-a-mark');
    expect(note.textContent ?? '').toMatch(/counted against nobody/i);
  });

  it('offers no export that adds the two together', async () => {
    /*
     * The tempting helper is `totalCorrections(record)` or `problemCount(record)`, answering
     * one number for both — and it is wrong in exactly the case this checkpoint exists for.
     * An operator who defended a correct reading did the job; added into the same figure as a
     * mistyped height it becomes indistinguishable from one, and the next screen somebody
     * builds in good faith would use it.
     */
    const forbidden = /^(total|sum|combined|allCorrections|problem|issue|defect)/i;
    const offenders = Object.keys(surface).filter((name) => forbidden.test(name));
    expect(
      offenders,
      `These would collapse a defended value into a corrected one: ${offenders.join(', ')}`,
    ).toEqual([]);
  });
});

describe('a threshold nobody has approved says so, on the face of every pattern', () => {
  it('carries the notice on a flag drawn on its own', async () => {
    renderWithProviders(<QualityFlagCard flag={flag()} />, { directory: directory() });

    const card = await screen.findByTestId(`quality-flag-${FLAG}`);
    expect(card).toHaveAttribute('data-approved', 'false');
    expect(within(card).getByText(/have not been approved/i)).toBeInTheDocument();
    expect(within(card).getByText(/proposal no clinician has agreed to/i)).toBeInTheDocument();
  });

  it('carries it on every card in a queue, not once at the top', async () => {
    // A flag is read alone as often as in a list — quoted in a message, printed, shown to the
    // person it is about. A notice that lived only at the top of the list would not travel.
    signedInAs(NAHID, SUPERVISOR, 'PHYSICIAN');
    listFlags.mockResolvedValue([flag(), flag({ id: SECOND_FLAG, operator_id: AMENA })]);
    renderWithProviders(<SupervisorQuality />, { directory: directory() });

    for (const id of [FLAG, SECOND_FLAG]) {
      const card = await screen.findByTestId(`quality-flag-${id}`);
      expect(card).toHaveAttribute('data-approved', 'false');
      expect(within(card).getByText(/have not been approved/i)).toBeInTheDocument();
    }
  });

  it('drops the notice only when a clinician has actually approved the numbers', async () => {
    renderWithProviders(<QualityFlagCard flag={flag({ threshold_approved: true })} />, {
      directory: directory(),
    });

    const card = await screen.findByTestId(`quality-flag-${FLAG}`);
    expect(card).toHaveAttribute('data-approved', 'true');
    expect(within(card).queryByText(/have not been approved/i)).toBeNull();
  });

  it('says the same thing on the rules an operator can read for themselves', async () => {
    renderWithProviders(<ThresholdList />, { directory: directory() });

    const row = await screen.findByTestId('threshold-TRANSCRIPTION_3_IN_30');
    expect(row).toHaveAttribute('data-approved', 'false');
    expect(within(row).getByTestId('threshold-proposal-TRANSCRIPTION_3_IN_30')).toHaveTextContent(
      /proposal/i,
    );
  });

  it('tells a supervisor before the first row of the queue', async () => {
    signedInAs(NAHID, SUPERVISOR, 'PHYSICIAN');
    listFlags.mockResolvedValue([flag()]);
    renderWithProviders(<SupervisorQuality />, { directory: directory() });

    expect(
      await screen.findByText(/none of these thresholds has been approved/i),
    ).toBeInTheDocument();
  });

  it('answers it without a screen', async () => {
    expect(surface.allThresholdsAreProposals([flag()])).toBe(true);
    expect(surface.allThresholdsAreProposals([flag({ threshold_approved: true })])).toBe(false);
    // An empty queue is not a queue of proposals; it is an empty queue.
    expect(surface.allThresholdsAreProposals([])).toBe(false);
  });
});

describe('answering a pattern', () => {
  beforeEach(() => {
    signedInAs(NAHID, SUPERVISOR, 'PHYSICIAN');
    listFlags.mockResolvedValue([flag()]);
  });

  it('will not send a dismissal with nothing said', async () => {
    const user = userEvent.setup();
    renderWithProviders(<SupervisorQuality />, { directory: directory() });

    await user.click(await screen.findByTestId(`dismiss-${FLAG}`));
    const confirm = screen.getByTestId(`dismiss-confirm-${FLAG}`);
    expect(confirm).toBeDisabled();

    // Whitespace is not a reason. The database refuses it and so does this form, before the
    // round trip rather than after it.
    await user.type(screen.getByTestId(`dismiss-reason-${FLAG}`), '   ');
    expect(screen.getByTestId(`dismiss-confirm-${FLAG}`)).toBeDisabled();
    expect(answerFlag).not.toHaveBeenCalled();
  });

  it('sends the dismissal once there is a reason, and sends the reason with it', async () => {
    const user = userEvent.setup();
    answerFlag.mockResolvedValue(
      flag({ status: 'DISMISSED', resolution: 'The scale was reading light.' }),
    );
    renderWithProviders(<SupervisorQuality />, { directory: directory() });

    await user.click(await screen.findByTestId(`dismiss-${FLAG}`));
    await user.type(screen.getByTestId(`dismiss-reason-${FLAG}`), 'The scale was reading light.');
    await user.click(screen.getByTestId(`dismiss-confirm-${FLAG}`));

    expect(answerFlag).toHaveBeenCalledWith(FLAG, {
      kind: 'dismissed',
      reason: 'The scale was reading light.',
    });
  });

  it('lets an acknowledgement go without a note, because saying yes should not need prose', async () => {
    const user = userEvent.setup();
    renderWithProviders(<SupervisorQuality />, { directory: directory() });

    await user.click(await screen.findByTestId(`acknowledge-${FLAG}`));
    const confirm = screen.getByTestId(`acknowledge-confirm-${FLAG}`);
    expect(confirm).toBeEnabled();

    await user.click(confirm);
    expect(answerFlag).toHaveBeenCalledWith(FLAG, { kind: 'acknowledged', note: '' });
  });

  it('pre-selects neither answer', async () => {
    // A form whose easier path is "acknowledge" produces acknowledgements from supervisors who
    // did not agree; one whose easier path is "dismiss" empties the list without anybody
    // reading it.
    renderWithProviders(<SupervisorQuality />, { directory: directory() });

    await screen.findByTestId(`acknowledge-${FLAG}`);
    expect(screen.queryByTestId(`acknowledge-form-${FLAG}`)).toBeNull();
    expect(screen.queryByTestId(`dismiss-form-${FLAG}`)).toBeNull();
  });

  it('says a colleague got there first rather than reporting a failure', async () => {
    const user = userEvent.setup();
    answerFlag.mockRejectedValue(
      new ApiError({
        status: 409,
        code: 'QUALITY_FLAG_ANSWERED',
        kind: 'conflict',
        messageEN: 'Somebody has already answered this.',
        messageBN: 'এটির উত্তর আগেই কেউ দিয়েছেন।',
        correlationID: 'test',
      }),
    );
    renderWithProviders(<SupervisorQuality />, { directory: directory() });

    await user.click(await screen.findByTestId(`acknowledge-${FLAG}`));
    await user.click(screen.getByTestId(`acknowledge-confirm-${FLAG}`));

    expect(await screen.findByText(/already answered this/i)).toBeInTheDocument();
  });

  it('does not draw the controls for a supervisor who may read but not answer', async () => {
    // A control that exists in order to be refused teaches people the software is unreliable.
    signedInAs(NAHID, READ_ONLY_SUPERVISOR, 'QA');
    renderWithProviders(<SupervisorQuality />, { directory: directory() });

    await screen.findByTestId(`quality-flag-${FLAG}`);
    expect(screen.queryByTestId(`acknowledge-${FLAG}`)).toBeNull();
    expect(screen.queryByTestId(`dismiss-${FLAG}`)).toBeNull();
  });

  it('shows what an answered pattern was answered with, and by whom', async () => {
    listFlags.mockResolvedValue([
      flag({
        status: 'DISMISSED',
        resolved_at: '2026-09-04T09:00:00Z',
        resolved_by: NAHID,
        resolved_by_code: 'E010',
        resolved_by_name_en: 'Dr Nahid Rahman',
        resolved_by_name_bn: 'ডা. নাহিদ রহমান',
        resolution: 'The scale at station two was reading light all fortnight.',
      }),
    ]);
    renderWithProviders(<SupervisorQuality />, { directory: directory() });

    const answered = await screen.findByTestId(`flag-answered-${FLAG}`);
    expect(answered).toHaveTextContent(/reading light all fortnight/);
    expect(screen.getByTestId(`flag-state-${FLAG}`)).toHaveTextContent(/not a problem/i);
    // A uuid is what the contract carries for the answerer; it must never be what is drawn.
    const answerer = screen.getByTestId(`flag-answerer-${FLAG}`);
    expect(answerer).toHaveTextContent('Dr Nahid Rahman');
    expect(answerer.textContent ?? '').not.toMatch(UUID);
  });

  it('refuses a reasonless dismissal without a screen', async () => {
    expect(surface.flagAnswerReady({ kind: 'dismissed', reason: '' })).toBe(false);
    expect(surface.flagAnswerReady({ kind: 'dismissed', reason: '   ' })).toBe(false);
    expect(surface.flagAnswerReady({ kind: 'dismissed', reason: 'The scale.' })).toBe(true);
    expect(surface.flagAnswerReady({ kind: 'acknowledged' })).toBe(true);
    expect(surface.flagAnswerReady({ kind: 'acknowledged', note: '' })).toBe(true);
  });
});

describe('the evidence a supervisor reads before speaking to anybody', () => {
  it('shows the reason, the measurement and the hour for each supporting correction', async () => {
    /*
     * All three come off the row now. The first version of this screen resolved the measurement
     * through the observation code registry and the reason through the record's own breakdown,
     * and that was wrong in a way worth remembering: both registries sit behind
     * `observation.read.values`, which the administrator holding `quality.read.team` does not
     * necessarily have — so the reader most likely to be shown a database identifier was the
     * one the workaround could not help.
     */
    renderWithProviders(<QualityFlagCard flag={flag()} />, { directory: directory() });

    const card = await screen.findByTestId(`quality-flag-${FLAG}`);
    expect(card).toHaveTextContent('Height');
    expect(card).toHaveTextContent(/Typed a different number/);
    // The hour on the clinic's clock, which is what makes an end-of-shift pattern legible.
    expect(card).toHaveTextContent('16:00');
    expect(card).toHaveTextContent('17:00');
    // And no permission was needed to read any of it.
    expect(listObservationCodes).not.toHaveBeenCalled();
  });

  it('reads the evidence in Bangla for a Bangla reader', async () => {
    renderWithProviders(<QualityFlagCard flag={flag()} />, {
      locale: 'bn',
      directory: directory(),
    });
    const card = await screen.findByTestId(`quality-flag-${FLAG}`);
    expect(card).toHaveTextContent('উচ্চতা');
    expect(card).toHaveTextContent('যা পড়া হয়েছিল');
  });

  it('names the measurement by its code where the server sent no display for it', async () => {
    // A code the registry has never heard of. The code is honest; a blank would remove the row
    // from a table somebody is using to decide whether to talk to a colleague.
    const bare = flag();
    renderWithProviders(
      <QualityFlagCard
        flag={{
          ...bare,
          evidence: bare.evidence.map((item) => ({
            ...item,
            code_en: undefined,
            code_bn: undefined,
            reason_en: undefined,
            reason_bn: undefined,
          })),
        }}
      />,
      { directory: directory() },
    );

    const card = await screen.findByTestId(`quality-flag-${FLAG}`);
    expect(card).toHaveTextContent('BODY_HEIGHT');
    expect(card).toHaveTextContent('TRANSCRIPTION');
  });

  it('says when the outcomes beside the evidence were true', async () => {
    // They are frozen with the rest of the row. A request that was waiting then and answered
    // since still reads as waiting, and a supervisor who took that for the live state would go
    // looking for a colleague who has already replied.
    renderWithProviders(<QualityFlagCard flag={flag()} />, { directory: directory() });
    expect(await screen.findByTestId(`flag-frozen-${FLAG}`)).toHaveTextContent(/as they stood on/i);
  });

  it('says so when a pattern cannot show what it was raised on', async () => {
    renderWithProviders(<QualityFlagCard flag={flag({ evidence: [] })} />, {
      directory: directory(),
    });
    expect(await screen.findByTestId(`flag-no-evidence-${FLAG}`)).toHaveTextContent(
      /do not act on it/i,
    );
  });

  it('carries the suggested first step from the server, in the reader’s language', async () => {
    const view = renderWithProviders(<QualityFlagCard flag={flag()} />, {
      directory: directory(),
    });
    expect(await screen.findByTestId(`flag-action-${FLAG}`)).toHaveTextContent(
      /watch how the reading is transferred/i,
    );
    view.unmount();

    renderWithProviders(<QualityFlagCard flag={flag()} />, {
      locale: 'bn',
      directory: directory(),
    });
    expect(await screen.findByTestId(`flag-action-${FLAG}`)).toHaveTextContent(
      'একটি সেশনে তাঁর পাশে বসে দেখুন',
    );
  });

  it('names whose record it is in the supervisor’s queue and not on somebody’s own screen', async () => {
    signedInAs(NAHID, SUPERVISOR, 'PHYSICIAN');
    listFlags.mockResolvedValue([flag()]);
    const view = renderWithProviders(<SupervisorQuality />, { directory: directory() });
    expect(await screen.findByTestId(`flag-operator-${FLAG}`)).toHaveTextContent('Kamal Hossain');
    view.unmount();

    signedInAs(KAMAL, OPERATOR);
    getMyRecord.mockResolvedValue(record({ flags: [flag()] }));
    renderWithProviders(<MyQualityRecord />, { directory: directory() });
    await screen.findByTestId(`quality-flag-${FLAG}`);
    // On your own screen the name would be your own, and the sentence would read as a file
    // being kept about you.
    expect(screen.queryByTestId(`flag-operator-${FLAG}`)).toBeNull();
  });
});

describe('the supervisor’s list is not a ranking', () => {
  beforeEach(() => {
    signedInAs(NAHID, SUPERVISOR, 'PHYSICIAN');
    listOperators.mockResolvedValue({
      operators: [
        // The server's own order: employee code, which asserts nothing.
        operator({
          operator_id: AMENA,
          employee_code: 'A002',
          name_en: 'Amena Begum',
          name_bn: 'আমেনা বেগম',
          corrections: 2,
          upheld: 1,
          overridden: 1,
          rejected: 0,
          open: 0,
          entries: 900,
          rate: 0.2,
        }),
        operator({
          operator_id: KAMAL,
          employee_code: 'A014',
          name_en: 'Kamal Hossain',
          corrections: 9,
          upheld: 6,
          overridden: 1,
          rejected: 2,
          open: 0,
          entries: 120,
          rate: 5.8,
        }),
        // The row that makes this a roster: recorded plenty, nothing came back.
        operator({
          operator_id: QUIET,
          employee_code: 'A031',
          name_en: 'Zubair Khan',
          name_bn: 'জুবায়ের খান',
          corrections: 0,
          upheld: 0,
          overridden: 0,
          rejected: 0,
          open: 0,
          entries: 900,
          rate: 0,
        }),
      ],
      window: { from: '2026-08-05T00:00:00Z', to: '2026-09-04T00:00:00Z', days: 30 },
    });
  });

  it('opens in name order, which is the order the reader can actually see', async () => {
    // The server orders by employee code, which asserts nothing and which nobody on this screen
    // is looking at. Name order is the same absence of a claim in the thing on the page.
    renderWithProviders(<SupervisorQuality />, { directory: directory() });

    const list = await screen.findByTestId('operator-list');
    const rows = within(list)
      .getAllByTestId(/^operator-0190/)
      .map((row) => row.getAttribute('data-testid'));
    expect(rows).toEqual([`operator-${AMENA}`, `operator-${KAMAL}`, `operator-${QUIET}`]);
  });

  it('gives the count order to a supervisor who asks for it in so many words', async () => {
    const user = userEvent.setup();
    renderWithProviders(<SupervisorQuality />, { directory: directory() });

    await user.click(await screen.findByTestId('order-by-corrections'));

    const list = screen.getByTestId('operator-list');
    const rows = within(list)
      .getAllByTestId(/^operator-0190/)
      .map((row) => row.getAttribute('data-testid'));
    expect(rows).toEqual([`operator-${KAMAL}`, `operator-${AMENA}`, `operator-${QUIET}`]);
  });

  it('is a roster, and keeps the people nothing came back on', async () => {
    /*
     * The rows with a denominator and no numerator are the ones that make this a list rather
     * than an accusation: a list every row of which carries a correction is an accusation
     * however it is titled and however carefully each line is annotated.
     */
    renderWithProviders(<SupervisorQuality />, { directory: directory() });

    const quiet = await screen.findByTestId(`operator-rate-${QUIET}`);
    expect(quiet).toHaveTextContent('0');
    expect(quiet).toHaveTextContent('900');
    // And no outcome split on a row where nothing came back — there is nothing to split.
    expect(screen.queryByTestId(`operator-outcomes-${QUIET}`)).toBeNull();
    expect(
      await screen.findByText(/including the people nothing came back on/i),
    ).toBeInTheDocument();
  });

  it('narrows to the people something came back on only when asked', async () => {
    const user = userEvent.setup();
    renderWithProviders(<SupervisorQuality />, { directory: directory() });

    await screen.findByTestId('operator-list');
    expect(listOperators).toHaveBeenCalledWith(30, { onlyCorrected: false });

    await user.click(screen.getByTestId('scope-corrected'));
    expect(listOperators).toHaveBeenCalledWith(30, { onlyCorrected: true });
  });

  it('puts the outcome split on every line that has one', async () => {
    // Only possible because the row carries it now. `rejected` beside `upheld` is the rule this
    // feature exists to keep, and it used to need a second request per operator to keep it.
    renderWithProviders(<SupervisorQuality />, { directory: directory() });

    const outcomes = await screen.findByTestId(`operator-outcomes-${KAMAL}`);
    expect(outcomes).toHaveTextContent(/the person who recorded them/i);
    expect(outcomes).toHaveTextContent(/a supervisor/i);
    expect(outcomes).toHaveTextContent(/the recorded value stands/i);
  });

  it('orders without a screen, and does not shuffle between reads', async () => {
    const rows = [
      operator({ operator_id: KAMAL, name_en: 'Kamal Hossain', corrections: 9 }),
      operator({
        operator_id: AMENA,
        employee_code: 'A002',
        name_en: 'Amena Begum',
        name_bn: 'আমেনা বেগম',
        corrections: 2,
      }),
    ];
    expect(surface.operatorsInOrder(rows, 'by-name', 'en').map((row) => row.name_en)).toEqual([
      'Amena Begum',
      'Kamal Hossain',
    ]);
    expect(surface.operatorsInOrder(rows, 'by-name', 'bn').map((row) => row.name_bn)).toEqual([
      'আমেনা বেগম',
      'কামাল হোসেন',
    ]);
    expect(
      surface.operatorsInOrder(rows, 'by-corrections', 'en').map((row) => row.name_en),
    ).toEqual(['Kamal Hossain', 'Amena Begum']);

    // A tie is broken by staff code rather than left to the order they arrived in.
    const tied = [
      operator({ operator_id: KAMAL, employee_code: 'A014', corrections: 3 }),
      operator({ operator_id: AMENA, employee_code: 'A002', corrections: 3 }),
    ];
    expect(
      surface.operatorsInOrder(tied, 'by-corrections', 'en').map((row) => row.employee_code),
    ).toEqual(['A002', 'A014']);
  });

  it('does not turn the list itself into an ordering nobody chose', async () => {
    // The default is written down so that a change to it is a decision rather than a tidy-up.
    renderWithProviders(<SupervisorQuality />, { directory: directory() });
    expect(await screen.findByTestId('operator-rows')).toHaveAttribute('data-order', 'by-name');
  });
});

describe('an operator’s own screen', () => {
  it('reads their own record and needs no permission to do it', async () => {
    // Signed in holding nothing but a write permission — the community field worker's shape.
    signedInAs(KAMAL, OPERATOR, 'FIELD_WORKER');
    renderWithProviders(<MyQualityRecord />, { directory: directory() });

    await screen.findByTestId('quality-record');
    expect(getMyRecord).toHaveBeenCalledWith(30);
  });

  it('says the record could not be read rather than showing an empty one', async () => {
    // An unreadable record and a clean one look identical, and on this screen of all screens
    // silence would be read as something being withheld.
    getMyRecord.mockRejectedValue(new Error('offline'));
    renderWithProviders(<MyQualityRecord />, { directory: directory() });

    expect(await screen.findByText(/nothing is being kept from you/i)).toBeInTheDocument();
  });

  it('changes the window when asked, and asks the server for it', async () => {
    const user = userEvent.setup();
    renderWithProviders(<MyQualityRecord />, { directory: directory() });

    await screen.findByTestId('quality-record');
    await user.click(screen.getByTestId('window-7'));

    expect(getMyRecord).toHaveBeenCalledWith(7);
  });

  it('reads a quiet month as a quiet month rather than as nothing to show', async () => {
    getMyRecord.mockResolvedValue(
      record({
        corrections: 0,
        upheld: 0,
        rejected: 0,
        open: 0,
        by_reason: [],
        by_code: [],
        by_hour: [],
      }),
    );
    renderWithProviders(<MyQualityRecord />, { directory: directory() });

    expect(await screen.findByText(/nobody asked about anything/i)).toBeInTheDocument();
    // And the pair is still on screen: no corrections, out of four hundred and twelve entries.
    // A quiet month is the one case where a screen is most tempted to print a bare zero.
    const rate = screen.getByTestId('record-rate');
    expect(rate).toHaveTextContent('0');
    expect(rate).toHaveTextContent('412');
  });

  it('groups the corrections three ways', async () => {
    renderWithProviders(<MyQualityRecord />, { directory: directory() });

    const byReason = await screen.findByTestId('by-reason');
    expect(byReason).toHaveTextContent(/Typed a different number/);
    // A reason is a slice of the corrections, so the corrections are its denominator.
    expect(byReason).toHaveTextContent('2 of 3');

    expect(screen.getByTestId('by-hour')).toHaveTextContent('16:00');
    // The measurement's own name, sent by the server rather than looked up behind a permission.
    expect(screen.getByTestId('by-code')).toHaveTextContent('Height');
  });

  it('reads every breakdown against its own denominator, not against the month’s total', async () => {
    /*
     * The hour breakdown is the one that decides whether the end-of-shift pattern is fair.
     * Somebody who works only the late shift clusters late by definition; two corrections out
     * of forty-seven values recorded in that hour is a fact, and "two of three corrections"
     * with no hint of how much work that hour held would put a rota in front of a supervisor
     * as though it were a person.
     */
    renderWithProviders(<MyQualityRecord />, { directory: directory() });

    const lateHour = await screen.findByTestId('by-hour-16');
    expect(lateHour).toHaveTextContent('2');
    expect(lateHour).toHaveTextContent('47');

    const morning = screen.getByTestId('by-hour-9');
    expect(morning).toHaveTextContent('1');
    expect(morning).toHaveTextContent('260');

    // And the measurement against how many of that measurement were taken.
    const height = screen.getByTestId('by-code-BODY_HEIGHT');
    expect(height).toHaveTextContent('3');
    expect(height).toHaveTextContent('118');
  });

  it('keeps the server’s stable order rather than sorting the breakdowns by size', async () => {
    // A size-ranked list of the ways one person's values were questioned reads as a
    // leaderboard, which is why the server stopped sending one. Nothing here re-sorts.
    getMyRecord.mockResolvedValue(
      record({
        by_hour: [
          { hour: 9, corrections: 1, entries: 260 },
          { hour: 16, corrections: 2, entries: 47 },
        ],
      }),
    );
    renderWithProviders(<MyQualityRecord />, { directory: directory() });

    const byHour = await screen.findByTestId('by-hour');
    const hours = within(byHour)
      .getAllByTestId(/^by-hour-\d+$/)
      .map((row) => row.getAttribute('data-testid'));
    expect(hours).toEqual(['by-hour-9', 'by-hour-16']);
  });

  it('says which reasons the clinic counts when it looks for repeated typing', async () => {
    // A vocabulary whose consequences are invisible is one people learn to answer
    // strategically.
    renderWithProviders(<MyQualityRecord />, { directory: directory() });
    const byReason = await screen.findByTestId('by-reason');
    expect(byReason).toHaveTextContent(/Counted when the clinic looks for repeated typing/i);
  });

  it('puts the rules on the same screen as the numbers', async () => {
    renderWithProviders(<MyQualityRecord />, { directory: directory() });
    expect(await screen.findByTestId('threshold-list')).toBeInTheDocument();
  });

  it('renders no uuid anywhere', async () => {
    getMyRecord.mockResolvedValue(record({ flags: [flag()] }));
    renderWithProviders(<MyQualityRecord />, { directory: directory() });

    const view = await screen.findByTestId('my-quality-record');
    expect(view.textContent ?? '').not.toMatch(UUID);
  });
});

describe('what an operator can read about the rules', () => {
  it('renders the server’s own sentence rather than rebuilding it', async () => {
    /*
     * It used to be rebuilt in the message files, because the endpoint answered English only.
     * Two sources for the sentence describing a rule would mean the screen and the database
     * eventually describe different rules — on the one screen an operator opens to find out
     * what is being measured about them.
     */
    renderWithProviders(<ThresholdList />, { directory: directory() });
    const sentence = await screen.findByTestId('threshold-looks-for-TRANSCRIPTION_3_IN_30');
    expect(sentence).toHaveTextContent(/only once there are at least 20 entries/i);
    expect(sentence).toHaveAttribute('lang', 'en');
  });

  it('reads that sentence in Bangla, with Bengali numerals', async () => {
    // A Latin numeral inside a Bangla sentence is what makes an interface read as translated
    // rather than written, and the numbers are the part a reader is actually looking for.
    renderWithProviders(<ThresholdList />, { locale: 'bn', directory: directory() });
    const sentence = await screen.findByTestId('threshold-looks-for-TRANSCRIPTION_3_IN_30');
    expect(sentence).toHaveTextContent('৩০');
    expect(sentence).toHaveTextContent('২০');
    expect(sentence).toHaveAttribute('lang', 'bn');
  });

  it('renders the threshold in Bangla for a Bangla reader', async () => {
    renderWithProviders(<ThresholdList />, { locale: 'bn', directory: directory() });
    expect(
      await screen.findByText('ত্রিশ দিনে তিন বা তার বেশি লেখার ভুল সংশোধন'),
    ).toBeInTheDocument();
  });

  it('says the rules could not be read rather than showing none', async () => {
    listThresholds.mockRejectedValue(new Error('offline'));
    renderWithProviders(<ThresholdList />, { directory: directory() });
    expect(await screen.findByText(/rules could not be read/i)).toBeInTheDocument();
  });

  it('keeps the clinic’s own order, which is the order a flag would be raised in', async () => {
    const rows = [
      { threshold: threshold({ code: 'B', ordering: 30 }), looks_for_en: '', looks_for_bn: '' },
      { threshold: threshold({ code: 'A', ordering: 10 }), looks_for_en: '', looks_for_bn: '' },
    ];
    expect(surface.thresholdsInOrder(rows).map((row) => row.threshold.code)).toEqual(['A', 'B']);
  });
});

describe('a pattern somebody has already answered', () => {
  const ANSWERED = flag({
    status: 'DISMISSED',
    resolved_at: '2026-09-04T09:00:00Z',
    resolved_by: NAHID,
    resolved_by_code: 'E010',
    resolved_by_name_en: 'Dr Nahid Rahman',
    resolved_by_name_bn: 'ডা. নাহিদ রহমান',
    resolution: 'The scale at station two was reading three kilos light all fortnight.',
  });

  it('stays on the operator’s own record with what was decided and by whom', async () => {
    /*
     * `/v1/quality/me` returns answered flags for a month as well as open ones. A note that
     * appeared on somebody's device and then silently vanished when a supervisor closed it
     * would teach them that things are decided about them out of sight — which is the same
     * failure as never telling them, arriving a fortnight later.
     */
    getMyRecord.mockResolvedValue(record({ flags: [ANSWERED] }));
    renderWithProviders(<MyQualityRecord />, { directory: directory() });

    const card = await screen.findByTestId(`quality-flag-${FLAG}`);
    expect(card).toHaveAttribute('data-status', 'DISMISSED');
    expect(within(card).getByTestId(`flag-resolution-${FLAG}`)).toHaveTextContent(
      /reading three kilos light/i,
    );
    expect(within(card).getByTestId(`flag-answerer-${FLAG}`)).toHaveTextContent('Dr Nahid Rahman');
    expect(await screen.findByTestId('answered-flags-stay')).toBeInTheDocument();
  });

  it('takes how long they stay from the server, not from a message file', async () => {
    /*
     * Same rule as `rate_floor`. A sentence in a message file about how long something stays is
     * a sentence that goes on being said after somebody changes the rule — and this one is a
     * promise made to the person the notes are about.
     */
    getMyRecord.mockResolvedValue(record({ flags: [ANSWERED], answered_flags_kept_days: 14 }));
    renderWithProviders(<MyQualityRecord />, { directory: directory() });

    const note = await screen.findByTestId('answered-flags-stay');
    expect(note).toHaveTextContent('14');
    expect(note.textContent ?? '').not.toContain('30');
  });

  it('reads differently from one still waiting, at the top of the card', async () => {
    // On the operator's own record an answered flag sits among open ones, so "somebody has
    // already dealt with this" is the first thing they need about it, not the last.
    getMyRecord.mockResolvedValue(record({ flags: [ANSWERED] }));
    const view = renderWithProviders(<MyQualityRecord />, { directory: directory() });

    expect(await screen.findByTestId(`flag-state-${FLAG}`)).toHaveTextContent(/not a problem/i);
    view.unmount();

    getMyRecord.mockResolvedValue(record({ flags: [flag()] }));
    renderWithProviders(<MyQualityRecord />, { directory: directory() });
    await screen.findByTestId(`quality-flag-${FLAG}`);
    expect(screen.queryByTestId(`flag-state-${FLAG}`)).toBeNull();
  });

  it('names the person who answered it without a directory lookup', async () => {
    // The names are joined onto the row now, the same way the operator's are, so the two people
    // on one row are named the same way — and a uuid never reaches the screen either way.
    renderWithProviders(<QualityFlagCard flag={ANSWERED} />, { directory: null });

    const answerer = await screen.findByTestId(`flag-answerer-${FLAG}`);
    expect(answerer).toHaveTextContent('Dr Nahid Rahman');
    expect(answerer.textContent ?? '').not.toMatch(UUID);
  });

  it('offers nobody a second answer to it', async () => {
    signedInAs(NAHID, SUPERVISOR, 'PHYSICIAN');
    renderWithProviders(<QualityFlagCard flag={ANSWERED} mayAnswer />, {
      directory: directory(),
    });

    await screen.findByTestId(`quality-flag-${FLAG}`);
    expect(screen.queryByTestId(`acknowledge-${FLAG}`)).toBeNull();
    expect(screen.queryByTestId(`dismiss-${FLAG}`)).toBeNull();
  });
});

describe('the window the server will accept', () => {
  it('offers nothing outside the range, so this control cannot produce a 422', async () => {
    for (const choice of surface.WINDOW_CHOICES) {
      expect(surface.windowIsAcceptable(choice), String(choice)).toBe(true);
    }
    expect(surface.windowIsAcceptable(0)).toBe(false);
    expect(surface.windowIsAcceptable(366)).toBe(false);
    expect(surface.windowIsAcceptable(30.5)).toBe(false);
    expect(surface.windowIsAcceptable(Number.NaN)).toBe(false);
  });

  it('asks only for windows it has offered', async () => {
    const user = userEvent.setup();
    renderWithProviders(<MyQualityRecord />, { directory: directory() });

    await screen.findByTestId('quality-record');
    for (const choice of surface.WINDOW_CHOICES) {
      await user.click(screen.getByTestId(`window-${choice}`));
    }
    for (const call of getMyRecord.mock.calls) {
      expect(surface.windowIsAcceptable(call[0] as number), String(call[0])).toBe(true);
    }
  });

  it('says what the server said when it refuses one anyway', async () => {
    /*
     * A window outside the range is refused with 422 rather than silently replaced with thirty,
     * which is right: a client asking for fourteen days and being handed thirty has two windows
     * pretending to be one, and every count on the screen is then a numerator over somebody
     * else's denominator. The picker cannot produce that refusal, so when one arrives it is a
     * real disagreement and is worth saying in the server's own words.
     */
    getMyRecord.mockRejectedValue(
      new ApiError({
        status: 422,
        code: 'VALIDATION_FAILED',
        kind: 'validation',
        messageEN: 'That request could not be accepted.',
        messageBN: 'অনুরোধটি গ্রহণ করা যায়নি।',
        fields: { days: 'Ask for between 1 and 365 days.' },
        fieldsBN: { days: '১ থেকে ৩৬৫ দিনের মধ্যে চান।' },
        correlationID: 'test',
      }),
    );
    renderWithProviders(<MyQualityRecord />, { directory: directory() });

    expect(await screen.findByText(/between 1 and 365 days/i)).toBeInTheDocument();
    // And not the generic sentence, which would hide a real disagreement behind "try again".
    expect(screen.queryByText(/nothing is being kept from you/i)).toBeNull();
  });

  it('gives every section of the supervisor’s screen the same window', async () => {
    // It used to scope two of three and leave the queue unbounded, which is three answers to
    // "which month am I looking at" on one screen.
    signedInAs(NAHID, SUPERVISOR, 'PHYSICIAN');
    const user = userEvent.setup();
    renderWithProviders(<SupervisorQuality />, { directory: directory() });

    await screen.findByTestId('operator-list');
    await user.click(screen.getByTestId('window-7'));

    expect(listFlags).toHaveBeenCalledWith(7, { includeAnswered: false });
    expect(listOperators).toHaveBeenCalledWith(7, { onlyCorrected: false });
  });

  it('counts the entries still needed without a screen', async () => {
    expect(surface.entriesUntilARate({ entries: 4, rate_floor: 20 })).toBe(16);
    expect(surface.entriesUntilARate({ entries: 20, rate_floor: 20 })).toBe(0);
    // Never negative: a caller with a rate in hand should be drawing it, not explaining it.
    expect(surface.entriesUntilARate({ entries: 400, rate_floor: 20 })).toBe(0);
  });
});

describe('the English reads as English at every count', () => {
  /*
   * "1 corrections out of 96 entries recorded" was on the hour rows, the measurement rows and
   * everywhere else a count of one reached the screen. It is the kind of defect that survives
   * every test of *what* a screen says, because the fixture happened to use three — and it is
   * the first thing a reader notices, on a screen whose whole job is to be trusted.
   *
   * So each count-bearing message is rendered at nought, one and two, and the singular is
   * checked against the plural nouns this namespace actually uses.
   */
  const PLURAL_NOUNS =
    /\b1 (corrections|entries|patterns|values|days|flags|records|measurements)\b/;

  const COUNTING: { key: string; values: (n: number) => Record<string, number> }[] = [
    { key: 'rate.outOf', values: (n) => ({ corrections: n, entries: n }) },
    { key: 'rate.moreNeeded', values: (n) => ({ needed: n }) },
    { key: 'rate.perHundred', values: (n) => ({ rate: n }) },
    { key: 'outcome.upheld', values: (n) => ({ part: n, whole: 9 }) },
    { key: 'outcome.overridden', values: (n) => ({ part: n, whole: 9 }) },
    { key: 'outcome.rejected', values: (n) => ({ part: n, whole: 9 }) },
    { key: 'outcome.open', values: (n) => ({ part: n, whole: 9 }) },
    { key: 'outcome.noneBody', values: (n) => ({ entries: n }) },
    { key: 'breakdown.share', values: (n) => ({ part: n, whole: 9 }) },
    { key: 'flags.answeredStay', values: (n) => ({ days: n }) },
    { key: 'operators.openFlags', values: (n) => ({ count: n }) },
  ];

  it('never writes a plural noun after a one', () => {
    const offenders: string[] = [];
    for (const { key, values } of COUNTING) {
      for (const count of [0, 1, 2]) {
        const rendered = render('en', key, values(count));
        if (PLURAL_NOUNS.test(rendered))
          offenders.push(`quality.${key} at ${count}: “${rendered}”`);
      }
    }
    expect(offenders, `These read as broken English: ${offenders.join(' | ')}`).toEqual([]);
  });

  it('still writes the plural at nought and at two', () => {
    // The other half, so the test above cannot be satisfied by deleting every noun.
    expect(render('en', 'rate.outOf', { corrections: 2, entries: 2 })).toBe(
      '2 corrections out of 2 entries recorded',
    );
    expect(render('en', 'rate.outOf', { corrections: 0, entries: 0 })).toBe(
      '0 corrections out of 0 entries recorded',
    );
    expect(render('en', 'rate.outOf', { corrections: 1, entries: 1 })).toBe(
      '1 correction out of 1 entry recorded',
    );
  });

  it('agrees with the verb, not only with the noun', () => {
    // "1 of 9 were corrected" is the same defect one word further along.
    expect(render('en', 'outcome.upheld', { part: 1, whole: 9 })).toMatch(/\bwas corrected\b/);
    expect(render('en', 'outcome.upheld', { part: 2, whole: 9 })).toMatch(/\bwere corrected\b/);
    expect(render('en', 'outcome.open', { part: 1, whole: 9 })).toMatch(/\bis still waiting\b/);
    expect(render('en', 'outcome.open', { part: 2, whole: 9 })).toMatch(/\bare still waiting\b/);
  });

  it('draws the count on screen in the singular where there is one', async () => {
    // Through the component rather than through the message, because passing a pre-formatted
    // string instead of a number would break the plural silently and look right in the file.
    getMyRecord.mockResolvedValue(
      record({
        by_hour: [{ hour: 9, corrections: 1, entries: 96 }],
        by_code: [
          {
            code: 'BODY_WEIGHT',
            display_en: 'Weight',
            display_bn: 'ওজন',
            corrections: 1,
            entries: 204,
          },
        ],
      }),
    );
    renderWithProviders(<MyQualityRecord />, { directory: directory() });

    expect(await screen.findByTestId('by-hour-9')).toHaveTextContent(
      '1 correction out of 96 entries recorded',
    );
    expect(screen.getByTestId('by-code-BODY_WEIGHT')).toHaveTextContent(
      '1 correction out of 204 entries recorded',
    );
  });
});

describe('the Bangla screen is written in Bengali numerals', () => {
  /*
   * A Latin numeral inside a Bangla sentence is the single thing that makes an interface read
   * as translated rather than written, and it is worse on this feature than anywhere else in
   * the application: the numbers are exactly what the reader of a screen about their own month
   * is looking for, and this is the screen that has to earn their trust.
   *
   * The counts were already right — they go through ICU's number formatting. The dates, the
   * times and the hour labels were not, because `formatters.ts` deliberately keeps dates in
   * ASCII for values somebody may copy onto a paper chart or read back over a phone. None of
   * that applies to prose about a staff record, so this feature formats its own; see the note
   * in `qualityText.ts`.
   */
  const LATIN_DIGIT = /[0-9]/;

  it('leaves no Latin digit anywhere on an operator’s own record', async () => {
    getMyRecord.mockResolvedValue(record({ flags: [flag()] }));
    renderWithProviders(<MyQualityRecord />, { locale: 'bn', directory: directory() });

    const view = await screen.findByTestId('my-quality-record');
    await screen.findByTestId(`quality-flag-${FLAG}`);
    const stray = (view.textContent ?? '').split(/\s+/).filter((word) => LATIN_DIGIT.test(word));
    expect(stray, `Latin digits in the Bangla screen: ${stray.join(', ')}`).toEqual([]);
  });

  it('writes the window, the moment it was noticed and the hours in Bengali', async () => {
    renderWithProviders(<QualityFlagCard flag={flag()} />, {
      locale: 'bn',
      directory: directory(),
    });

    const card = await screen.findByTestId(`quality-flag-${FLAG}`);
    // The window — "৬ আগ, ২০২৬ থেকে ৪ সেপ, ২০২৬ পর্যন্ত", not "6 আগ, 2026".
    expect(card).toHaveTextContent('২০২৬');
    expect(card.textContent ?? '').not.toMatch(/2026/);
    // The hour on the evidence rows, padded with a Bengali zero rather than an ASCII one.
    expect(card).toHaveTextContent('১৬:০০');
    expect(card.textContent ?? '').not.toMatch(/16:00/);
  });

  it('keeps the English screen in Latin digits', async () => {
    // The two are different sentences, not one sentence formatted twice. A change that made
    // Bengali numerals appear for an English reader would be the same defect mirrored.
    renderWithProviders(<QualityFlagCard flag={flag()} />, { directory: directory() });

    const card = await screen.findByTestId(`quality-flag-${FLAG}`);
    expect(card).toHaveTextContent('16:00');
    expect(card).toHaveTextContent('2026');
  });

  it('pads the hour without mixing the two numeral systems', async () => {
    // A string pad would prepend an ASCII zero to a Bengali numeral and produce "0৯" — the
    // exact defect this rule exists to prevent, arriving through the fix for it.
    expect(surface.hourLabel(9, 'bn')).toBe('০৯:০০');
    expect(surface.hourLabel(16, 'bn')).toBe('১৬:০০');
    expect(surface.hourLabel(9, 'en')).toBe('09:00');
  });
});

describe('nothing in this feature grades a person', () => {
  it('exports no name that does', async () => {
    /*
     * The same discipline `corrections.test.tsx` keeps over its own surface, extended here
     * because this is the feature where it matters most: these screens are *about* people.
     * The first place a quality record turns punitive is its vocabulary, long before its
     * arithmetic does — and a helper called `errorRate` would be used in good faith by
     * whoever builds the next screen.
     */
    const offenders = Object.keys(surface).filter((name) => wordsOf(name).some(grades));
    expect(
      offenders,
      `These name a verdict on a person rather than a fact about a number: ${offenders.join(', ')}`,
    ).toEqual([]);
  });

  it('has something to audit', async () => {
    // Guards the test above from passing vacuously if the barrel were ever emptied.
    expect(Object.keys(surface).length).toBeGreaterThan(20);
  });

  it('puts no such word on the screen either, in either language', () => {
    /*
     * Exports are not where a reader meets the vocabulary; labels are. "Going wrong" about a
     * *measurement* is allowed and is used — the same measurement going wrong again and again
     * is usually the instrument — because it grades an instrument rather than a person. What
     * is refused is any word that grades the person.
     */
    const forbidden =
      /\b(errors?|mistakes?|scores?|worst|offenders?|accuracy|inaccurate|blame|culprit|negligent|careless|sloppy|punish|penalty|demerit|incompetent|lazy|performance)\b/i;

    const offenders: string[] = [];
    for (const [key, value] of flatten(en.quality as Record<string, unknown>)) {
      if (forbidden.test(value)) offenders.push(`quality.${key}: “${value}”`);
    }
    for (const key of ['myQuality', 'qualityTeam'] as const) {
      for (const [suffix, value] of flatten(en.page[key] as Record<string, unknown>)) {
        if (forbidden.test(value)) offenders.push(`page.${key}.${suffix}: “${value}”`);
      }
    }

    expect(offenders, `These read as a verdict: ${offenders.join(' | ')}`).toEqual([]);
  });

  it('offers no export that deletes a raised pattern', async () => {
    // Both answers are recorded in place with a name against them. A flag that could be made
    // to disappear is a flag a supervisor can be persuaded to make disappear.
    const forbidden = /^(delete|remove|clear|drop|purge|hide)/i;
    const offenders = Object.keys(surface).filter((name) => forbidden.test(name));
    expect(offenders, `These would remove a flag: ${offenders.join(', ')}`).toEqual([]);
  });
});

describe('the message files carry what the screens ask for', () => {
  it('has a label for every window the feature offers, in both languages', () => {
    // Built at runtime from WINDOW_CHOICES, so the static scan in i18n.test.ts cannot see it.
    const english = flatten(en.quality as Record<string, unknown>);
    const bangla = flatten(bn.quality as Record<string, unknown>);
    for (const choice of surface.WINDOW_CHOICES) {
      expect([...english.keys()], `English ${choice}`).toContain(`window.days.${choice}`);
      expect([...bangla.keys()], `Bangla ${choice}`).toContain(`window.days.${choice}`);
    }
  });
});

/* ------------------------------------------------------------------------- */

/**
 * One message, rendered the way a screen renders it.
 *
 * ICU's simple argument — `{count}` — is `String(value)`; its typed one — `{count, number}` —
 * and the `#` inside a plural block both go through `Intl.NumberFormat`. In Bangla those are
 * two different sentences, and in English they are the difference between "1 corrections" and
 * "1 correction". A test that compared message *files* could not tell them apart.
 */
function render(locale: 'en' | 'bn', key: string, values: Record<string, number>): string {
  /*
   * The cast is on the factory rather than on `messages`. Casting the messages to `never`
   * poisons the contextual type of the object literal around it, and `namespace: 'quality'`
   * then fails to type-check against an inferred `undefined` — a confusing error a long way
   * from its cause.
   */
  const translator = createTranslator as unknown as (options: {
    locale: string;
    messages: Record<string, unknown>;
    namespace: string;
  }) => (key: string, values: Record<string, number>) => string;

  return translator({ locale, messages: locale === 'bn' ? bn : en, namespace: 'quality' })(
    key,
    values,
  );
}

/**
 * Whether a message interpolates a named argument, however it is typed.
 *
 * `{corrections}`, `{corrections, number}` and `{corrections, plural, …}` are all the same
 * argument on screen. An audit that recognised only the bare form would have gone quiet the day
 * these counts grew plural rules — which is the day it mattered most.
 */
function interpolates(message: string, name: string): boolean {
  return new RegExp(`\\{\\s*${name}\\s*[},]`).test(message);
}

/**
 * Words that grade a person, matched whole.
 *
 * Whole words rather than substrings, and it is not fussiness: `DEFAULT_WINDOW_DAYS` contains
 * "fault", and an audit that failed on it would be turned off within a week — which is how a
 * rule like this actually dies.
 */
const GRADING_WORDS =
  /^(errors?|mistakes?|scores?|worst|offend|offenders?|accuracy|inaccurate|blame|fault|faults|culprit|guilt|guilty|negligent|careless|sloppy|punish|penalty|demerit|rank|ranking|grade|grades|performance)$/i;

function grades(word: string): boolean {
  return GRADING_WORDS.test(word);
}

/** Splits an identifier into the words a reader would see in it. */
function wordsOf(name: string): string[] {
  return name
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .replace(/_/g, ' ')
    .split(/\s+/)
    .filter((word) => word !== '');
}

/** Flattens a message subtree into `a.b.c` keys, the way `i18n.test.ts` does. */
function flatten(tree: Record<string, unknown>, prefix = ''): Map<string, string> {
  const out = new Map<string, string>();
  for (const [key, value] of Object.entries(tree)) {
    const path = prefix ? `${prefix}.${key}` : key;
    if (value !== null && typeof value === 'object') {
      for (const [k, v] of flatten(value as Record<string, unknown>, path)) out.set(k, v);
    } else {
      out.set(path, String(value));
    }
  }
  return out;
}
