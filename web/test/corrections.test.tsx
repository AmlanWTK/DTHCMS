import type { components } from '@dthcms/api-client';
import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { Directory } from '@/features/attribution';

import { renderWithProviders } from './render';

/**
 * The correction and flagging workflow (CP62, §4.3, [R-04]).
 *
 * The manual verification is Dr. Nahid's own case: an operator records a height of 150 cm, the
 * physician is sure it is 140 and flags it, the request reaches the operator who typed it, they
 * correct it, everything derived from it recomputes, and both numbers stay in the record with
 * both names against them. What can be proven here is whether the interface makes each of those
 * steps possible and truthful — and every way it fails is quiet, and most of them read as
 * reassurance.
 *
 *  - **The original value hidden once it is corrected.** Criterion 1 says it is never altered
 *    and *remains visible*, and the tempting treatment — fold it away, grey it out, replace it
 *    with a note saying there was an earlier value — satisfies the first half and destroys the
 *    second. The number a physician disagreed with is the number a reviewer needs to read.
 *  - **`OVERRIDDEN` drawn as `APPLIED`.** A supervisor's fix read as the operator's own is a
 *    false statement about a colleague's work, and at CP63 it is a false statement in a quality
 *    tally. Two named tests.
 *  - **A rejection with no reason.** "No" with no reason is how a flagging culture dies: the
 *    person who flagged it learns nothing, cannot tell a disagreement from an oversight, and
 *    stops flagging.
 *  - **A flag one press away.** The control accuses a colleague of a mistake and its reason is
 *    counted. One mis-tap on a tablet must not send it.
 *  - **A flag control offered where the server refuses it.** On your own value, on a value
 *    already replaced, on one somebody has already flagged, or without the permission. Each is
 *    a refusal an operator would read as the software being unreliable.
 *  - **A derived value quietly left stale.** A corrected height with a BMI beside it that was
 *    computed from the old one is two numbers that disagree, both looking equally official.
 *  - **A uuid where a name should be.** The commonest failure and the one that looks like it is
 *    working: the field is populated, the screen renders it, and the answer is unreadable to
 *    everybody who needed it.
 *  - **A screen that reads as blame.** A correction is a fact about a number. There is a named
 *    test below that the operator's own queue carries no tally of their mistakes.
 */

type Observation = components['schemas']['Observation'];
type ObservationCode = components['schemas']['ObservationCode'];
type CorrectionReason = components['schemas']['CorrectionReason'];
type CorrectionRequest = components['schemas']['CorrectionRequest'];

const PATIENT = '0190a8f2-0000-7000-8000-0000000000a1';
/** The operator who typed 150. */
const KAMAL = '0190a8f2-0000-7000-8000-0000000000c1';
/** The physician who is sure it is 140. */
const NAHID = '0190a8f2-0000-7000-8000-0000000000c9';
/** A supervisor who answers for somebody who has gone home. */
const SHILPI = '0190a8f2-0000-7000-8000-0000000000c5';

const HEIGHT_150 = '0190a8f2-0000-7000-8000-0000000000d1';
const HEIGHT_140 = '0190a8f2-0000-7000-8000-0000000000d2';
const BMI_OLD = '0190a8f2-0000-7000-8000-0000000000e1';
const BMI_NEW = '0190a8f2-0000-7000-8000-0000000000e2';
const REQUEST = '0190a8f2-0000-7000-8000-0000000000f1';

/** A uuid anywhere on a screen. The thing no attribution surface may ever show. */
const UUID = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i;

const listObservationHistory = vi.hoisted(() => vi.fn());
const listPatientObservations = vi.hoisted(() => vi.fn());
const listObservationCodes = vi.hoisted(() => vi.fn());
const getObservation = vi.hoisted(() => vi.fn());

/*
 * Partial: the network calls are stubbed, the rules are not. `chainsOf`, `derivedFrom` and
 * `computedFromCurrent` are what these screens *are* — the chain, and whether the BMI moved —
 * and a test that stubbed them would prove the components call a function rather than that the
 * right sentence reaches a physician.
 */
vi.mock('@/features/observations/api/observations', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/observations/api/observations')>()),
  listObservationHistory,
  listPatientObservations,
  listObservationCodes,
  getObservation,
}));

const listCorrectionReasons = vi.hoisted(() => vi.fn());
const listCorrectionsForPatient = vi.hoisted(() => vi.fn());
const listMyCorrections = vi.hoisted(() => vi.fn());
const flagObservation = vi.hoisted(() => vi.fn());
const applyCorrection = vi.hoisted(() => vi.fn());
const rejectCorrection = vi.hoisted(() => vi.fn());

vi.mock('@/features/corrections/api/corrections', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/corrections/api/corrections')>()),
  listCorrectionReasons,
  listCorrectionsForPatient,
  listMyCorrections,
  flagObservation,
  applyCorrection,
  rejectCorrection,
}));

const { ValueChain } = await import('@/features/corrections/components/ValueChain');
const { ValueHistory } = await import('@/features/corrections/components/ValueHistory');
const { DerivedValues } = await import('@/features/corrections/components/DerivedValues');
const { CorrectionQueue } = await import('@/features/corrections/components/CorrectionQueue');
const { CorrectionTrail } = await import('@/features/corrections/components/CorrectionTrail');
// The barrel, imported as a caller would.
const surface = await import('@/features/corrections');
const { useSessionStore } = await import('@/stores/session');

/* ------------------------------------------------------------------------- */

function observation(over: Partial<Observation> = {}): Observation {
  return {
    id: HEIGHT_150,
    patient_id: PATIENT,
    code: 'BODY_HEIGHT',
    category: 'ANTHRO',
    value_type: 'numeric',
    value: 150,
    unit: 'cm',
    entered_value: 150,
    entered_unit: 'cm',
    effective_at: '2026-09-14T04:05:00Z',
    recorded_at: '2026-09-14T04:20:00Z',
    source: 'STATION',
    status: 'ACTIVE',
    recorded_by: KAMAL,
    recorded_role: 'ANTHROPOMETRY',
    station_code: 'STN_ANTHROPOMETRY',
    ...over,
  };
}

/** The 140/150 case as the ledger holds it: the original, corrected, and both still there. */
const CORRECTED_CHAIN: Observation[] = [
  observation({ id: HEIGHT_140, value: 140, entered_value: 140, status: 'ACTIVE' }),
  observation({ id: HEIGHT_150, status: 'CORRECTED', replaced_by: HEIGHT_140 }),
];

const BMI_RECOMPUTED = observation({
  id: BMI_NEW,
  code: 'BMI',
  category: 'DERIVED',
  value: 28.1,
  entered_value: undefined,
  entered_unit: undefined,
  unit: 'kg/m2',
  formula: 'bmi',
  formula_version: '1.0.0',
  inputs: { BODY_HEIGHT: 140, BODY_WEIGHT: 55 },
  effective_at: '2026-09-14T06:00:00Z',
  recorded_at: '2026-09-14T06:00:00Z',
});

const BMI_SUPERSEDED = observation({
  id: BMI_OLD,
  code: 'BMI',
  category: 'DERIVED',
  value: 24.4,
  entered_value: undefined,
  entered_unit: undefined,
  unit: 'kg/m2',
  formula: 'bmi',
  formula_version: '1.0.0',
  inputs: { BODY_HEIGHT: 150, BODY_WEIGHT: 55 },
  status: 'SUPERSEDED',
  replaced_by: BMI_NEW,
  effective_at: '2026-09-14T04:25:00Z',
  recorded_at: '2026-09-14T04:25:00Z',
});

const CODES: ObservationCode[] = [
  {
    code: 'BODY_HEIGHT',
    category: 'ANTHRO',
    value_type: 'numeric',
    display_en: 'Height',
    display_bn: 'উচ্চতা',
    write_permission: 'observation.write.anthro',
    canonical_unit: 'cm',
    dimension: 'length',
    units: [
      {
        code: 'cm',
        dimension: 'length',
        is_canonical: true,
        display_en: 'cm',
        display_bn: 'সেমি',
        decimals: 1,
      },
      {
        code: 'm',
        dimension: 'length',
        is_canonical: false,
        display_en: 'm',
        display_bn: 'মিটার',
        decimals: 2,
      },
    ],
  },
  {
    code: 'BMI',
    category: 'DERIVED',
    value_type: 'numeric',
    display_en: 'Body mass index',
    display_bn: 'বিএমআই',
    write_permission: 'observation.write.anthro',
    canonical_unit: 'kg/m2',
    units: [],
  },
];

const REASONS: CorrectionReason[] = [
  {
    code: 'TRANSCRIPTION',
    display_en: 'Typed a different number from the one read',
    display_bn: 'যা পড়া হয়েছিল তার চেয়ে অন্য সংখ্যা লেখা হয়েছে',
    transcription: true,
    ordering: 1,
  },
  {
    code: 'REMEASURED',
    display_en: 'Measured again and the first reading was wrong',
    display_bn: 'আবার মাপা হয়েছে, প্রথম পাঠটি ভুল ছিল',
    transcription: false,
    ordering: 5,
  },
];

function request(over: Partial<CorrectionRequest> = {}): CorrectionRequest {
  return {
    id: REQUEST,
    patient_id: PATIENT,
    observation_id: HEIGHT_150,
    code: 'BODY_HEIGHT',
    requested_at: '2026-09-14T05:00:00Z',
    requested_by: NAHID,
    requested_role: 'PHYSICIAN',
    requested_by_code: 'E010',
    requested_by_name_en: 'Dr Nahid Rahman',
    requested_by_name_bn: 'ডা. নাহিদ রহমান',
    reason_code: 'TRANSCRIPTION',
    note: 'The tape was against the wall, not the patient.',
    assigned_to: KAMAL,
    assigned_to_code: 'A014',
    assigned_to_name_en: 'Kamal Hossain',
    assigned_to_name_bn: 'কামাল হোসেন',
    status: 'OPEN',
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
      {
        id: NAHID,
        code: 'E010',
        name_en: 'Dr Nahid Rahman',
        name_bn: 'ডা. নাহিদ রহমান',
        status: 'active',
      },
      { id: SHILPI, code: 'E022', name_en: 'Shilpi Das', name_bn: 'শিল্পী দাস', status: 'active' },
    ],
    devices: [],
    stations: [
      { code: 'STN_ANTHROPOMETRY', name_en: 'Anthropometry', name_bn: 'দেহ পরিমাপ', sequence: 2 },
    ],
    as_of: '2026-09-14T00:00:00Z',
  };
}

const initialSession = useSessionStore.getInitialState();

function signedInAs(id: string, permissions: string[], role = 'PHYSICIAN') {
  useSessionStore.setState({
    ...initialSession,
    status: 'authenticated',
    user: {
      id,
      employeeCode: 'E010',
      nameEN: 'Dr Nahid Rahman',
      nameBN: 'ডা. নাহিদ রহমান',
      facilityId: '11111111-1111-4111-8111-111111111111',
      roles: [role],
      grants: { [role]: permissions },
      permissions,
      secondFactor: { required: false, enrolled: true, pending: false, recoveryCodesLeft: 8 },
    },
    activeRole: role,
  });
}

/** The physician in §4.3: may say a number looks wrong, may not change anybody's work. */
const FLAGGER = ['observation.read.values', 'observation.correct.request'];
/** The operator who typed it: reads values, needs no permission to fix their own mistake. */
const OPERATOR = ['observation.read.values'];
/** A supervisor: may answer a request routed to somebody else. */
const SUPERVISOR = ['observation.read.values', 'observation.correct.approve'];

beforeEach(() => {
  vi.clearAllMocks();
  signedInAs(NAHID, FLAGGER);
  listObservationHistory.mockImplementation((_patient: string, code: string) =>
    Promise.resolve(code === 'BMI' ? [BMI_RECOMPUTED, BMI_SUPERSEDED] : CORRECTED_CHAIN),
  );
  listPatientObservations.mockResolvedValue([CORRECTED_CHAIN[0], BMI_RECOMPUTED]);
  listObservationCodes.mockResolvedValue(CODES);
  getObservation.mockResolvedValue(observation({ status: 'CORRECTED', replaced_by: HEIGHT_140 }));
  listCorrectionReasons.mockResolvedValue(REASONS);
  listCorrectionsForPatient.mockResolvedValue([]);
  listMyCorrections.mockResolvedValue([]);
});

afterEach(() => {
  useSessionStore.setState(initialSession, true);
  vi.restoreAllMocks();
});

async function openChain(locale: 'en' | 'bn' = 'en') {
  renderWithProviders(<ValueChain patientId={PATIENT} code="BODY_HEIGHT" codeLabel="Height" />, {
    locale,
    directory: directory(),
  });
  return screen.findByTestId('value-chain');
}

/* ------------------------------------------------------------------------- */

describe('the chain keeps both numbers (criterion 1)', () => {
  it('shows the corrected value and the value it replaced', async () => {
    // The whole of criterion 1. A screen that showed only 140 would be a screen where the
    // number a physician disagreed with had vanished from the record as far as anybody
    // reading can tell.
    await openChain();

    expect(screen.getByTestId(`chain-row-${HEIGHT_150}`)).toHaveTextContent('150');
    expect(screen.getByTestId(`chain-row-${HEIGHT_140}`)).toHaveTextContent('140');
  });

  it('says which is current and which was replaced, in words', async () => {
    await openChain();

    expect(screen.getByTestId(`chain-state-${HEIGHT_140}`)).toHaveTextContent('Current value');
    expect(screen.getByTestId(`chain-state-${HEIGHT_150}`)).toHaveTextContent('Corrected');
  });

  it('draws the replaced value at the same weight as the current one', async () => {
    // Not a style preference. A row a screen has dimmed is a row a reviewer stops reading, and
    // the replaced row is the one this checkpoint exists to keep readable — so the rule that
    // distinguishes them is a rail and a word, never a drop in contrast or size.
    await openChain();

    const replaced = screen.getByTestId(`chain-row-${HEIGHT_150}`);
    expect(replaced).toHaveAttribute('data-status', 'CORRECTED');
    // No opacity, no display:none, no "hidden" attribute smuggling it off the screen.
    expect(replaced).toBeVisible();
    expect(replaced.getAttribute('style')).toBeNull();
  });

  it('keeps the two chains of one code apart', async () => {
    // A height at two visits is two measurements, and each may have been corrected. Flattened
    // into one list, last March's correction would sit between today's height and yesterday's.
    const march = observation({
      id: '0190a8f2-0000-7000-8000-0000000000d9',
      value: 149,
      entered_value: 149,
      effective_at: '2026-03-02T04:00:00Z',
      recorded_at: '2026-03-02T04:00:00Z',
    });
    listObservationHistory.mockResolvedValue([...CORRECTED_CHAIN, march]);

    await openChain();

    expect(screen.getByTestId(`chain-${HEIGHT_150}`)).toHaveAttribute('data-versions', '2');
    expect(screen.getByTestId(`chain-${march.id}`)).toHaveAttribute('data-versions', '1');
  });

  it('says out loud that both versions are kept', async () => {
    // A reader meeting this screen for the first time has to be told that both numbers are the
    // record, rather than that one of them is a draft somebody forgot to delete.
    await openChain();
    expect(screen.getByTestId('chain-has-versions')).toHaveTextContent('nothing was overwritten');
  });
});

describe('both attributions reach the screen (criterion 5)', () => {
  it('names who entered each value', async () => {
    await openChain();

    const original = screen.getByTestId(`chain-row-${HEIGHT_150}`);
    expect(within(original).getByTestId('attribution-person')).toHaveTextContent('Kamal Hossain');
  });

  it('names who asked about it, why, and who was asked', async () => {
    listCorrectionsForPatient.mockResolvedValue([request()]);
    await openChain();

    const trail = await screen.findByTestId(`correction-trail-${REQUEST}`);
    expect(within(trail).getByTestId('correction-requester')).toHaveTextContent('Dr Nahid Rahman');
    expect(within(trail).getByTestId('correction-assignee')).toHaveTextContent('Kamal Hossain');
    expect(within(trail).getByTestId('correction-reason')).toHaveTextContent(
      'Typed a different number from the one read',
    );
    expect(within(trail).getByTestId('correction-note')).toHaveTextContent(
      'The tape was against the wall',
    );
  });

  it('shows the free text as well as the code, because a code cannot say it', async () => {
    // A code alone cannot say "the tape was against the wall, not the patient", and a screen
    // that showed only the code would have thrown away the half somebody acts on.
    listCorrectionsForPatient.mockResolvedValue([request()]);
    await openChain();

    const trail = await screen.findByTestId(`correction-trail-${REQUEST}`);
    expect(within(trail).getByTestId('correction-note')).toBeInTheDocument();
    expect(within(trail).getByTestId('correction-reason')).toBeInTheDocument();
  });

  it('renders no uuid anywhere on the chain', async () => {
    // The commonest failure and the one that looks like it is working. A uuid answers a
    // different question from the one being asked and no colleague on the floor can use it.
    listCorrectionsForPatient.mockResolvedValue([
      request({ status: 'APPLIED', resolved_at: '2026-09-14T05:30:00Z', resolved_by: KAMAL }),
    ]);
    const chain = await openChain();
    await screen.findByTestId(`correction-trail-${REQUEST}`);

    expect(chain.textContent ?? '').not.toMatch(UUID);
  });

  it('names the reason by its code when the vocabulary no longer has a row for it', async () => {
    // A taxonomy that changed under a request already in the record. The code is a poor label
    // and a far better one than a blank, and it is something somebody can quote when they ask.
    listCorrectionReasons.mockResolvedValue([]);
    listCorrectionsForPatient.mockResolvedValue([request({ reason_code: 'RETIRED_CODE' })]);
    await openChain();

    const trail = await screen.findByTestId(`correction-trail-${REQUEST}`);
    expect(within(trail).getByTestId('correction-reason')).toHaveTextContent('RETIRED_CODE');
  });
});

describe('a supervisor’s fix never reads as the operator’s own', () => {
  it('says an override was somebody else, in its own sentence', async () => {
    listCorrectionsForPatient.mockResolvedValue([
      request({
        status: 'OVERRIDDEN',
        resolved_at: '2026-09-14T06:00:00Z',
        resolved_by: SHILPI,
        resolved_role: 'PHYSICIAN',
      }),
    ]);
    await openChain();

    const trail = await screen.findByTestId(`correction-trail-${REQUEST}`);
    expect(within(trail).getByTestId('correction-status')).toHaveTextContent(
      'Corrected by somebody else',
    );
    expect(within(trail).getByTestId('correction-overridden')).toHaveTextContent(
      'other than the person who recorded the value',
    );
  });

  it('does not use the applied sentence for an override', async () => {
    // The single failure this status was added to the contract to prevent: an operator's
    // record reading as though they had put it right themselves.
    listCorrectionsForPatient.mockResolvedValue([
      request({ status: 'OVERRIDDEN', resolved_at: '2026-09-14T06:00:00Z', resolved_by: SHILPI }),
    ]);
    await openChain();

    const trail = await screen.findByTestId(`correction-trail-${REQUEST}`);
    expect(trail).toHaveAttribute('data-status', 'OVERRIDDEN');
    expect(within(trail).getByTestId('correction-status')).not.toHaveTextContent(
      'Corrected by the person who recorded it',
    );
  });

  it('marks an applied correction as the author’s own', async () => {
    listCorrectionsForPatient.mockResolvedValue([
      request({ status: 'APPLIED', resolved_at: '2026-09-14T06:00:00Z', resolved_by: KAMAL }),
    ]);
    await openChain();

    const trail = await screen.findByTestId(`correction-trail-${REQUEST}`);
    expect(within(trail).getByTestId('correction-status')).toHaveTextContent(
      'Corrected by the person who recorded it',
    );
    expect(within(trail).queryByTestId('correction-overridden')).toBeNull();
  });

  it('shows a rejection as an answer and not as a problem', async () => {
    // "The value stands, and here is why" is an outcome. A screen that drew it as a failure
    // would teach physicians that flagging something and being told it was right is a thing to
    // avoid, which is how the signal disappears.
    listCorrectionsForPatient.mockResolvedValue([
      request({
        status: 'REJECTED',
        resolved_at: '2026-09-14T06:00:00Z',
        resolved_by: KAMAL,
        resolution_note: 'Measured twice on the stadiometer.',
      }),
    ]);
    await openChain();

    const trail = await screen.findByTestId(`correction-trail-${REQUEST}`);
    expect(within(trail).getByTestId('correction-status')).toHaveTextContent('The value stands');
    expect(within(trail).getByTestId('correction-resolution-note')).toHaveTextContent(
      'Measured twice on the stadiometer',
    );
  });
});

describe('the flag control', () => {
  it('takes two deliberate acts', async () => {
    // It accuses a colleague of a mistake and its reason is counted. One press on a tablet
    // being scrolled is not a decision.
    const user = userEvent.setup();
    await openChain();

    expect(screen.queryByTestId('flag-form')).toBeNull();
    await user.click(
      within(screen.getByTestId(`chain-row-${HEIGHT_140}`)).getByTestId('flag-open'),
    );
    expect(await screen.findByTestId('flag-form')).toBeInTheDocument();
  });

  it('will not send without a reason from the clinic’s own list', async () => {
    const user = userEvent.setup();
    await openChain();
    await user.click(
      within(screen.getByTestId(`chain-row-${HEIGHT_140}`)).getByTestId('flag-open'),
    );

    expect(await screen.findByTestId('flag-confirm')).toBeDisabled();
  });

  it('sends the reason code and the free text together', async () => {
    const user = userEvent.setup();
    flagObservation.mockResolvedValue(request());
    await openChain();

    await user.click(
      within(screen.getByTestId(`chain-row-${HEIGHT_140}`)).getByTestId('flag-open'),
    );
    await user.selectOptions(await screen.findByTestId('flag-reason'), 'TRANSCRIPTION');
    await user.type(screen.getByTestId('flag-note'), 'It reads 140 on the stadiometer.');
    await user.click(screen.getByTestId('flag-confirm'));

    expect(flagObservation).toHaveBeenCalledWith(HEIGHT_140, {
      reasonCode: 'TRANSCRIPTION',
      note: 'It reads 140 on the stadiometer.',
    });
  });

  it('says which reasons the clinic counts, at the moment the choice is made', async () => {
    // A vocabulary whose consequences are invisible is one people learn to answer
    // strategically. Said as a fact about how repeated problems are found, never as a threat.
    const user = userEvent.setup();
    await openChain();

    await user.click(
      within(screen.getByTestId(`chain-row-${HEIGHT_140}`)).getByTestId('flag-open'),
    );
    await user.selectOptions(await screen.findByTestId('flag-reason'), 'REMEASURED');
    expect(screen.queryByTestId('flag-counted')).toBeNull();

    await user.selectOptions(screen.getByTestId('flag-reason'), 'TRANSCRIPTION');
    expect(screen.getByTestId('flag-counted')).toHaveTextContent('counted');
  });

  it('is not offered to somebody without the permission to raise one', async () => {
    signedInAs(NAHID, OPERATOR);
    await openChain();
    expect(screen.queryByTestId('flag-open')).toBeNull();
  });

  it('is not offered on your own value, and says why', async () => {
    // The server refuses this with 422 — correct it rather than flagging it. A control that
    // exists in order to be refused teaches people the software is unreliable; an unexplained
    // absence on one row and not the next reads as a fault in the screen.
    signedInAs(KAMAL, FLAGGER);
    await openChain();

    expect(screen.queryByTestId('flag-open')).toBeNull();
    expect(screen.getByTestId(`chain-own-${HEIGHT_140}`)).toHaveTextContent('your own value');
  });

  it('is not offered on a value that has already been replaced', async () => {
    // The server answers 409. The row already says it was corrected, so nothing further is said.
    await openChain();
    const replaced = screen.getByTestId(`chain-row-${HEIGHT_150}`);
    expect(within(replaced).queryByTestId('flag-open')).toBeNull();
  });

  it('is not offered on a value somebody has already flagged', async () => {
    // A second flag is the same conversation, and two would route two corrections at one
    // number. The open request is drawn directly above, which is the explanation.
    listCorrectionsForPatient.mockResolvedValue([request({ observation_id: HEIGHT_140 })]);
    await openChain();
    await screen.findByTestId(`correction-trail-${REQUEST}`);

    expect(screen.queryByTestId('flag-open')).toBeNull();
  });

  it('says so rather than sending when somebody flagged it first', async () => {
    const user = userEvent.setup();
    const { ApiError } = await import('@/lib/api');
    flagObservation.mockRejectedValue(
      new ApiError({
        status: 409,
        code: 'CORRECTION_ALREADY_OPEN',
        kind: 'conflict',
        messageEN: 'Somebody has already asked for this value to be looked at.',
        messageBN: 'এই মানটি দেখার জন্য আগেই কেউ অনুরোধ করেছেন।',
        correlationID: 'req-1',
      }),
    );
    await openChain();

    await user.click(
      within(screen.getByTestId(`chain-row-${HEIGHT_140}`)).getByTestId('flag-open'),
    );
    await user.selectOptions(await screen.findByTestId('flag-reason'), 'TRANSCRIPTION');
    await user.click(screen.getByTestId('flag-confirm'));

    expect(await screen.findByText(/already asked about this value/i)).toBeInTheDocument();
  });
});

describe('what was computed from the value (criterion 4)', () => {
  async function openDerived(locale: 'en' | 'bn' = 'en') {
    renderWithProviders(
      <DerivedValues patientId={PATIENT} code="BODY_HEIGHT" codeLabel="Height" />,
      { locale, directory: directory() },
    );
    return screen.findByTestId('derived-values');
  }

  it('says the BMI was computed from the height that stands now', async () => {
    await openDerived();
    const row = await screen.findByTestId('derived-BMI');
    expect(row).toHaveAttribute('data-current', 'true');
    expect(screen.getByTestId('derived-state-BMI')).toHaveTextContent('stands now');
  });

  it('shows the superseded BMI beside the one that replaced it', async () => {
    // Criterion 3 in the interface: derived values are versioned, not overwritten, and a
    // physician who cannot see the earlier answer has to take the recomputation on trust.
    await openDerived();
    const previous = await screen.findByTestId('derived-previous-BMI');
    expect(previous).toHaveTextContent('24.4');
    expect(screen.getByTestId('derived-value-BMI')).toHaveTextContent('28.1');
  });

  it('says plainly when a derived value was computed from a height that has since changed', async () => {
    // The cascade names a derivation it could not recompute rather than failing the
    // correction, and that failure reaches no field in the contract. This is how a screen can
    // still tell: the BMI says it saw 150 and the height now says 140.
    listPatientObservations.mockResolvedValue([
      CORRECTED_CHAIN[0],
      { ...BMI_RECOMPUTED, inputs: { BODY_HEIGHT: 150, BODY_WEIGHT: 55 } },
    ]);
    await openDerived();

    const row = await screen.findByTestId('derived-BMI');
    expect(row).toHaveAttribute('data-current', 'false');
    expect(screen.getByTestId('derived-state-BMI')).toHaveTextContent('not worked out again');
  });

  it('says the record does not say, rather than assuming it is fine', async () => {
    // Rounding "we cannot tell" up to "it is fine" is the one answer a physician must not be
    // given about a value they are about to act on.
    listPatientObservations.mockResolvedValue([
      CORRECTED_CHAIN[0],
      { ...BMI_RECOMPUTED, inputs: { BODY_HEIGHT: Number.NaN, BODY_WEIGHT: 55 } },
    ]);
    await openDerived();

    const row = await screen.findByTestId('derived-BMI');
    expect(row).toHaveAttribute('data-current', 'unknown');
    expect(screen.getByTestId('derived-state-BMI')).toHaveTextContent('does not say');
  });

  it('says nothing was computed from it rather than drawing an empty section', async () => {
    // A heading with nothing under it reads as a failed load.
    listPatientObservations.mockResolvedValue([CORRECTED_CHAIN[0]]);
    await openDerived();
    expect(await screen.findByTestId('derived-none')).toHaveTextContent('Height');
  });

  it('claims nothing about a derived value that records no inputs', async () => {
    // An older row, or a derivation that stored none. Guessing from the code would list a BMI
    // as unmoved on no evidence, which is worse than not listing it.
    listPatientObservations.mockResolvedValue([
      CORRECTED_CHAIN[0],
      { ...BMI_RECOMPUTED, inputs: undefined },
    ]);
    await openDerived();
    expect(await screen.findByTestId('derived-none')).toBeInTheDocument();
  });
});

describe('answering a request', () => {
  async function openQueue(locale: 'en' | 'bn' = 'en') {
    renderWithProviders(<CorrectionQueue />, { locale, directory: directory() });
    return screen.findByTestId('correction-queue');
  }

  it('shows the flagged value while it is being corrected', async () => {
    // Correcting from memory is how 150 becomes 130. The person answering typed the number
    // hours ago and is being asked about it by somebody who was not there.
    signedInAs(KAMAL, OPERATOR, 'ANTHROPOMETRY');
    listMyCorrections.mockResolvedValue([request()]);
    await openQueue();

    expect(await screen.findByTestId('answer-original')).toHaveTextContent('150');
  });

  it('offers correcting and standing as two equal answers, neither pre-selected', async () => {
    signedInAs(KAMAL, OPERATOR, 'ANTHROPOMETRY');
    listMyCorrections.mockResolvedValue([request()]);
    await openQueue();

    expect(await screen.findByTestId('answer-correct')).toBeInTheDocument();
    expect(screen.getByTestId('answer-stands')).toBeInTheDocument();
    expect(screen.queryByTestId('answer-correct-form')).toBeNull();
    expect(screen.queryByTestId('answer-stands-form')).toBeNull();
  });

  it('sends the corrected value in the unit the original was entered in', async () => {
    const user = userEvent.setup();
    signedInAs(KAMAL, OPERATOR, 'ANTHROPOMETRY');
    listMyCorrections.mockResolvedValue([request()]);
    applyCorrection.mockResolvedValue(request({ status: 'APPLIED' }));
    await openQueue();

    await user.click(await screen.findByTestId('answer-correct'));
    const box = await screen.findByTestId('answer-value');
    await user.clear(box);
    await user.type(box, '140');
    await user.click(screen.getByTestId('answer-correct-confirm'));

    expect(applyCorrection).toHaveBeenCalledWith(REQUEST, {
      value: 140,
      unit: 'cm',
      note: '',
    });
  });

  it('opens the box on the value as it was typed, not on a conversion of it', async () => {
    // 154 lb is stored as 69.85 kg and read back as 154 lb. Pre-filling kilograms would ask an
    // operator to check a number they never wrote.
    signedInAs(KAMAL, OPERATOR, 'ANTHROPOMETRY');
    getObservation.mockResolvedValue(
      observation({
        code: 'BODY_WEIGHT',
        value: 69.85,
        unit: 'kg',
        entered_value: 154,
        entered_unit: '[lb_av]',
      }),
    );
    listMyCorrections.mockResolvedValue([request({ code: 'BODY_WEIGHT' })]);
    const user = userEvent.setup();
    await openQueue();

    await user.click(await screen.findByTestId('answer-correct'));
    expect(await screen.findByTestId('answer-value')).toHaveValue('154');
  });

  it('refuses to say the value stands with no reason', async () => {
    // "No" with no reason is how a flagging culture dies. The server refuses it, a database
    // constraint refuses it, and the form refuses it a third time — because the round trip
    // costs an operator's attention, and because the hint beside the box is the honest part.
    const user = userEvent.setup();
    signedInAs(KAMAL, OPERATOR, 'ANTHROPOMETRY');
    listMyCorrections.mockResolvedValue([request()]);
    await openQueue();

    await user.click(await screen.findByTestId('answer-stands'));
    expect(await screen.findByTestId('answer-stands-confirm')).toBeDisabled();

    await user.type(screen.getByTestId('answer-reason'), 'Measured twice on the stadiometer.');
    expect(screen.getByTestId('answer-stands-confirm')).toBeEnabled();
  });

  it('sends the reason with the refusal', async () => {
    const user = userEvent.setup();
    signedInAs(KAMAL, OPERATOR, 'ANTHROPOMETRY');
    listMyCorrections.mockResolvedValue([request()]);
    rejectCorrection.mockResolvedValue(request({ status: 'REJECTED' }));
    await openQueue();

    await user.click(await screen.findByTestId('answer-stands'));
    await user.type(await screen.findByTestId('answer-reason'), 'It reads 150 on the stadiometer.');
    await user.click(screen.getByTestId('answer-stands-confirm'));

    expect(rejectCorrection).toHaveBeenCalledWith(REQUEST, 'It reads 150 on the stadiometer.');
  });

  it('tells a supervisor their fix will be recorded as theirs, before they press', async () => {
    // A supervisor who did not know their fix would be recorded separately from the operator's
    // own has taken a decision about a colleague's record without being told they were taking
    // it. Said in a banner above the buttons, not reported afterwards.
    signedInAs(SHILPI, SUPERVISOR);
    listMyCorrections.mockResolvedValue([request()]);
    await openQueue();

    const form = await screen.findByTestId(`answer-${REQUEST}`);
    expect(form).toHaveAttribute('data-role', 'supervisor');
    expect(within(form).getByText(/not your own value/i)).toBeInTheDocument();
    expect(
      within(form).getByText(/recorded as your correction and not as theirs/i),
    ).toBeInTheDocument();
  });

  it('offers no answer form to somebody it is not theirs to answer', async () => {
    signedInAs(NAHID, FLAGGER);
    listMyCorrections.mockResolvedValue([request()]);
    await openQueue();

    await screen.findByTestId(`queue-row-${REQUEST}`);
    expect(screen.queryByTestId(`answer-${REQUEST}`)).toBeNull();
  });

  it('offers no answer form on a request somebody has already answered', async () => {
    signedInAs(KAMAL, OPERATOR, 'ANTHROPOMETRY');
    listMyCorrections.mockResolvedValue([
      request({ status: 'APPLIED', resolved_at: '2026-09-14T06:00:00Z', resolved_by: KAMAL }),
    ]);
    await openQueue();

    await screen.findByTestId(`queue-row-${REQUEST}`);
    expect(screen.queryByTestId(`answer-${REQUEST}`)).toBeNull();
  });
});

describe('the operator’s own queue', () => {
  async function openQueue(locale: 'en' | 'bn' = 'en') {
    renderWithProviders(<CorrectionQueue />, { locale, directory: directory() });
    return screen.findByTestId('correction-queue');
  }

  it('opens on what is waiting rather than on the history', async () => {
    signedInAs(KAMAL, OPERATOR, 'ANTHROPOMETRY');
    await openQueue();
    expect(listMyCorrections).toHaveBeenCalledWith(false);
  });

  it('asks for the answered ones only when told to', async () => {
    const user = userEvent.setup();
    signedInAs(KAMAL, OPERATOR, 'ANTHROPOMETRY');
    await openQueue();

    await user.click(screen.getByTestId('queue-all'));
    expect(listMyCorrections).toHaveBeenCalledWith(true);
  });

  it('keeps the server’s order, oldest first', async () => {
    // A queue answered newest-first is a queue where the oldest request is never answered, and
    // nothing on this screen re-sorts what the endpoint returned.
    signedInAs(KAMAL, OPERATOR, 'ANTHROPOMETRY');
    const older = request({
      id: '0190a8f2-0000-7000-8000-0000000000f0',
      requested_at: '2026-09-10T05:00:00Z',
    });
    listMyCorrections.mockResolvedValue([older, request()]);
    await openQueue();

    const rows = await screen.findAllByTestId(/^queue-row-/);
    expect(rows[0]).toHaveAttribute('data-testid', `queue-row-${older.id}`);
  });

  it('counts nothing and grades nobody', async () => {
    // The plan's own risk note: a metric that feels punitive makes staff hide errors instead of
    // correcting them. CP63 builds the quality tally and it is quality's screen; an inbox that
    // opened with a running count of your mistakes is the fastest way to stop somebody looking.
    signedInAs(KAMAL, OPERATOR, 'ANTHROPOMETRY');
    listMyCorrections.mockResolvedValue([
      request(),
      request({ id: '0190a8f2-0000-7000-8000-0000000000f2' }),
    ]);
    const queue = await openQueue();
    await screen.findAllByTestId(/^queue-row-/);

    const text = queue.textContent ?? '';
    expect(text).not.toMatch(/\b(error|errors|mistake|mistakes|score|rate|accuracy)\b/i);
  });

  it('says an unreadable queue is unreadable rather than showing an empty one', async () => {
    // An empty screen and a failed read look the same, and one of them means a colleague is
    // waiting for an answer nobody knows they asked for.
    signedInAs(KAMAL, OPERATOR, 'ANTHROPOMETRY');
    listMyCorrections.mockRejectedValue(new Error('offline'));
    await openQueue();

    expect(await screen.findByText(/Your list could not be read/i)).toBeInTheDocument();
  });
});

describe('the value history screen', () => {
  it('names the measurement in words rather than by its code', async () => {
    // `BODY_HEIGHT` on a physician's screen is a database identifier being shown to a clinician.
    renderWithProviders(<ValueHistory patientId={PATIENT} />, { directory: directory() });
    const picker = await screen.findByTestId('history-code');
    expect(within(picker).getByRole('option', { name: 'Height' })).toBeInTheDocument();
  });

  it('offers only the measurements this patient has a value for', async () => {
    renderWithProviders(<ValueHistory patientId={PATIENT} />, { directory: directory() });
    const picker = await screen.findByTestId('history-code');
    // The registry lists both; this patient has a height and a BMI, and nothing else.
    expect(within(picker).getAllByRole('option')).toHaveLength(2);
  });

  it('says the record is empty rather than drawing a chooser with nothing in it', async () => {
    listPatientObservations.mockResolvedValue([]);
    renderWithProviders(<ValueHistory patientId={PATIENT} />, { directory: directory() });
    expect(await screen.findByText(/No values recorded for this patient/i)).toBeInTheDocument();
  });
});

describe('the screen speaks Bangla', () => {
  it('writes the chain, the states and the reason in Bangla', async () => {
    listCorrectionsForPatient.mockResolvedValue([request()]);
    await openChain('bn');

    expect(screen.getByTestId(`chain-state-${HEIGHT_140}`)).toHaveTextContent('বর্তমান মান');
    const trail = await screen.findByTestId(`correction-trail-${REQUEST}`);
    expect(within(trail).getByTestId('correction-reason')).toHaveTextContent(
      'যা পড়া হয়েছিল তার চেয়ে অন্য সংখ্যা লেখা হয়েছে',
    );
    expect(within(trail).getByTestId('correction-requester')).toHaveTextContent('ডা. নাহিদ রহমান');
  });

  it('writes the override sentence in Bangla', async () => {
    listCorrectionsForPatient.mockResolvedValue([
      request({ status: 'OVERRIDDEN', resolved_at: '2026-09-14T06:00:00Z', resolved_by: SHILPI }),
    ]);
    await openChain('bn');

    const trail = await screen.findByTestId(`correction-trail-${REQUEST}`);
    expect(within(trail).getByTestId('correction-overridden')).toHaveTextContent('অন্য কেউ');
  });
});

describe('the record survives what the screen cannot read', () => {
  it('draws the values even when the correction list could not be read', async () => {
    // The values are the record; the flags are a conversation about them. A failure to read
    // the second must not blank the first — and must not be silent either, because a chain
    // with no trail on it looks exactly like a chain nobody ever questioned.
    listCorrectionsForPatient.mockRejectedValue(new Error('offline'));
    await openChain();

    expect(screen.getByTestId(`chain-row-${HEIGHT_150}`)).toHaveTextContent('150');
    expect(await screen.findByText(/correction requests could not be read/i)).toBeInTheDocument();
  });

  it('says the history could not be read rather than showing an empty chain', async () => {
    listObservationHistory.mockRejectedValue(new Error('offline'));
    renderWithProviders(<ValueChain patientId={PATIENT} code="BODY_HEIGHT" codeLabel="Height" />, {
      directory: directory(),
    });
    expect(await screen.findByText(/value history could not be read/i)).toBeInTheDocument();
  });

  it('falls back to the role when the staff directory could not be read', async () => {
    // "Nobody answered this" and "we could not read the names" are different sentences, and
    // only one of them is safe to imply.
    listCorrectionsForPatient.mockResolvedValue([
      request({
        status: 'APPLIED',
        resolved_at: '2026-09-14T06:00:00Z',
        resolved_by: SHILPI,
        resolved_role: 'PHYSICIAN',
      }),
    ]);
    renderWithProviders(<ValueChain patientId={PATIENT} code="BODY_HEIGHT" codeLabel="Height" />, {
      directory: null,
    });

    const trail = await screen.findByTestId(`correction-trail-${REQUEST}`);
    const resolver = within(trail).getByTestId('correction-resolver');
    expect(resolver).toHaveTextContent('Chief consultant');
    expect(resolver.textContent ?? '').not.toMatch(UUID);
  });
});

describe('one trail on its own', () => {
  it('draws no answer form where the screen around it already has one', async () => {
    // The queue draws its own form beneath the trail. Two forms for one request on one screen
    // is two buttons that do the same thing and disagree about which one is busy.
    signedInAs(KAMAL, OPERATOR, 'ANTHROPOMETRY');
    renderWithProviders(<CorrectionTrail request={request()} allowAnswer={false} />, {
      directory: directory(),
    });

    await screen.findByTestId(`correction-trail-${REQUEST}`);
    expect(screen.queryByTestId(`answer-${REQUEST}`)).toBeNull();
  });

  it('offers the form to the person it was routed to when nothing else has', async () => {
    signedInAs(KAMAL, OPERATOR, 'ANTHROPOMETRY');
    renderWithProviders(<CorrectionTrail request={request()} />, { directory: directory() });

    const form = await screen.findByTestId(`answer-${REQUEST}`);
    expect(form).toHaveAttribute('data-role', 'author');
    // The person it was routed to needs no permission for their own work, and is not told
    // their fix is an override — because it is not one.
    expect(within(form).queryByText(/not your own value/i)).toBeNull();
  });

  it('says a request is waiting rather than leaving the outcome blank', async () => {
    signedInAs(NAHID, FLAGGER);
    renderWithProviders(<CorrectionTrail request={request()} allowAnswer={false} />, {
      directory: directory(),
    });

    const trail = await screen.findByTestId(`correction-trail-${REQUEST}`);
    expect(within(trail).getByTestId('correction-waiting')).toHaveTextContent(
      'value on the record has not changed',
    );
  });
});

describe('the module offers nothing that would blur the two outcomes', () => {
  it('has no export collapsing an applied correction and a supervisor’s override', async () => {
    /*
     * The tempting helper is `wasCorrected(request)`, answering true for both — and it is
     * wrong in exactly the case this checkpoint exists for. A supervisor's fix corrected the
     * value just as thoroughly; the distinction is whose record it goes on, and CP63 counts
     * these statuses. A predicate that erased it would be used by the next screen somebody
     * builds, in good faith, and the error would surface as an operator's tally.
     */
    const forbidden = /^(was|is|has)?[Cc]orrected$|^resolvedOk$|^succeeded$|^wasFixed$/;
    const offenders = Object.keys(surface).filter((name) => forbidden.test(name));
    expect(
      offenders,
      `These would answer the same for APPLIED and OVERRIDDEN: ${offenders.join(', ')}`,
    ).toEqual([]);
  });

  it('has no export that edits a value in place', async () => {
    // Correcting is writing a *new* value that replaces the old one. An update would destroy
    // criterion 1 in the one place a reviewer would never think to look.
    const forbidden = /^(update|edit|patch|amend|overwrite|delete|remove)/;
    const offenders = Object.keys(surface).filter((name) => forbidden.test(name));
    expect(offenders, `These would alter a recorded value: ${offenders.join(', ')}`).toEqual([]);
  });

  it('still exports the four statuses as four', async () => {
    // Guards the two tests above from passing vacuously if the barrel were ever emptied.
    expect(Object.keys(surface).length).toBeGreaterThan(20);
    expect(surface.wasOverridden(request({ status: 'OVERRIDDEN' }))).toBe(true);
    expect(surface.wasOverridden(request({ status: 'APPLIED' }))).toBe(false);
  });
});

describe('the rules, without a screen', () => {
  it('offers a flag only where the server would accept one', async () => {
    const open = request({ observation_id: HEIGHT_140 });
    const value = observation({ id: HEIGHT_140 });

    expect(
      surface.whyNotFlaggable(value, {
        viewerId: NAHID,
        mayRequest: false,
        openRequest: undefined,
      }),
    ).toBe('not-permitted');
    expect(
      surface.whyNotFlaggable(value, {
        viewerId: undefined,
        mayRequest: true,
        openRequest: undefined,
      }),
    ).toBe('viewer-unknown');
    expect(
      surface.whyNotFlaggable(value, { viewerId: KAMAL, mayRequest: true, openRequest: undefined }),
    ).toBe('your-own');
    expect(
      surface.whyNotFlaggable(observation({ status: 'CORRECTED' }), {
        viewerId: NAHID,
        mayRequest: true,
        openRequest: undefined,
      }),
    ).toBe('already-replaced');
    expect(
      surface.whyNotFlaggable(value, { viewerId: NAHID, mayRequest: true, openRequest: open }),
    ).toBe('already-flagged');
    expect(
      surface.whyNotFlaggable(value, { viewerId: NAHID, mayRequest: true, openRequest: undefined }),
    ).toBeNull();
  });

  it('says a replaced value is replaced, even when it is also your own', async () => {
    // "Correct it rather than flagging it" is true of your own value and useless once a later
    // value has replaced it: there is nothing left to correct. The fact worth saying is the
    // replacement, so the row says that and the sentence about whose value it is stays off.
    expect(
      surface.whyNotFlaggable(observation({ status: 'CORRECTED', replaced_by: HEIGHT_140 }), {
        viewerId: KAMAL,
        mayRequest: true,
        openRequest: undefined,
      }),
    ).toBe('already-replaced');
  });

  it('tells the author’s answer from a supervisor’s before either is sent', async () => {
    const open = request();
    expect(surface.answerRole(open, { viewerId: KAMAL, mayApprove: false })).toBe('author');
    expect(surface.answerRole(open, { viewerId: SHILPI, mayApprove: true })).toBe('supervisor');
    expect(surface.answerRole(open, { viewerId: NAHID, mayApprove: false })).toBe('nobody');
    // An unresolved session is not the author. Assuming it were would offer a form the server
    // refuses, to somebody who has no idea why.
    expect(surface.answerRole(open, { viewerId: undefined, mayApprove: false })).toBe('nobody');
  });

  it('keeps every request raised on one value, including the ones already answered', async () => {
    // Flagged, rejected, flagged again a week later is an ordinary sequence and the whole of it
    // is the record. Showing only the latest would hide a rejection, which is the one outcome a
    // physician most needs before flagging the same number again.
    const first = request({ id: 'r-1', status: 'REJECTED', requested_at: '2026-09-01T00:00:00Z' });
    const second = request({ id: 'r-2', requested_at: '2026-09-14T00:00:00Z' });
    const all = surface.requestsOn([first, second], HEIGHT_150);

    expect(all.map((row) => row.id)).toEqual(['r-2', 'r-1']);
    expect(surface.openRequestOn([first, second], HEIGHT_150)?.id).toBe('r-2');
  });

  it('refuses a rejection with nothing but whitespace in it', async () => {
    expect(surface.rejectReasonAcceptable('   ')).toBe(false);
    expect(surface.rejectReasonAcceptable('It reads 150.')).toBe(true);
  });

  it('refuses a flag with no reason chosen', async () => {
    expect(surface.flagReady('')).toBe(false);
    expect(surface.flagReady('TRANSCRIPTION')).toBe(true);
  });
});
