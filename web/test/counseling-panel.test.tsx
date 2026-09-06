import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '@/lib/api';
import type { CounselingItem, CounselingRoom } from '@/features/counseling/api/counseling';
import type {
  CounselingGate,
  CounselingGateOverride,
  CounselingMissingItem,
  CounselingSession,
  CounselingTick,
} from '@/features/counseling/api/gate';
import type { Visit } from '@/features/patients/api/patients';

import { renderWithProviders } from './render';

/**
 * The physician's counselling panel (CP57, §5.5, §5.4).
 *
 * The manual verification for this checkpoint is a physician looking at a patient's record,
 * asking "what did they teach you about injection sites", and being able to say who taught
 * it. What can only be proven here is whether the panel makes that possible — and every way
 * it fails is quiet and reads as reassurance.
 *
 *  - **An overridden gate drawn as a finished one.** `blocked` is what the queue will do and
 *    `overridden` is why it will not, and a screen that reduced the pair to "is the patient
 *    held" would tell a physician the counselling was done. There is a named test below for
 *    exactly that sentence.
 *  - **Attribution by session rather than by item.** One session walks three rooms and the
 *    nutritionist ticks the nutrition items. "Counselled by Rina" is a true sentence and the
 *    answer to a question nobody asked.
 *  - **Attribution by role rather than by person.** The role tells a counsellor from a
 *    nutritionist and does not tell two counsellors apart, and "who taught you this" is a
 *    question about a person. The name leads; the role and the staff code stay beside it.
 *  - **A duration read as attention.** The number is the gap between two presses. A
 *    counsellor who talks through four items and ticks them at the end shows three fast items
 *    and one slow one, and a physician who took that at face value would be wrong about a
 *    colleague. The caveat has to be on the screen, not in a comment.
 *  - **A withdrawn tick shown as an absence.** "Ticked at 11:02 and taken back at 11:04, and
 *    here is why" is the record. A row that simply reads "not covered" has destroyed it.
 *  - **One missing-items list for two different problems.** An unfinished session has a room
 *    to send the patient back to; a checklist nobody opened has nobody to send them to.
 *  - **A tick control on a read surface.** A physician who could tick a counsellor's item
 *    could clear the gate holding their own patient, in a colleague's name. Two named tests.
 */

const testRoot = dirname(fileURLToPath(import.meta.url));
const webRoot = join(testRoot, '..');

const getCounselingGate = vi.hoisted(() => vi.fn());
const listCounselingSessionsForVisit = vi.hoisted(() => vi.fn());
const getCounselingSession = vi.hoisted(() => vi.fn());
const overrideCounselingGate = vi.hoisted(() => vi.fn());

/*
 * Partial: the network calls are stubbed, the rules are not. `itemProgress`, `gateState`,
 * `missingByRemediation`, `staffLabel` and `skippedAtGrant` are what this panel *is*, and
 * a test that stubbed them would prove the components call a function rather than that the
 * right sentence reaches a physician.
 */
vi.mock('@/features/counseling/api/gate', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/counseling/api/gate')>()),
  getCounselingGate,
  listCounselingSessionsForVisit,
  getCounselingSession,
  overrideCounselingGate,
}));

const listCounselingRooms = vi.hoisted(() => vi.fn());
const getCounselingVersion = vi.hoisted(() => vi.fn());

vi.mock('@/features/counseling/api/counseling', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/counseling/api/counseling')>()),
  listCounselingRooms,
  getCounselingVersion,
}));

const listPatientVisits = vi.hoisted(() => vi.fn());

vi.mock('@/features/patients/api/patients', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/patients/api/patients')>()),
  listPatientVisits,
}));

const { CounselingPanel } = await import('@/features/counseling/components/CounselingPanel');
const { GateState } = await import('@/features/counseling/components/GateState');
const { SessionItems } = await import('@/features/counseling/components/SessionItems');
const { SessionList } = await import('@/features/counseling/components/SessionList');
// The barrel, imported as a caller would.
const surface = await import('@/features/counseling');
const gateModule = await import('@/features/counseling/api/gate');
const { useSessionStore } = await import('@/stores/session');
/*
 * The unmocked module, for the handful of tests that care what goes on the wire.
 * `importActual` rather than the mock above: everything else in this file wants the network
 * stubbed out, and those want the opposite.
 */
const realGate = await vi.importActual<typeof import('@/features/counseling/api/gate')>(
  '@/features/counseling/api/gate',
);

const PATIENT = '0190a8f2-0000-7000-8000-0000000000a1';
const VISIT = '0190a8f2-0000-7000-8000-0000000000b1';
const OLD_VISIT = '0190a8f2-0000-7000-8000-0000000000b2';
const DIABETES_SESSION = '0190a8f2-0000-7000-8000-0000000000e1';
const THYROID_SESSION = '0190a8f2-0000-7000-8000-0000000000e2';
const DIABETES = '0190a8f2-0000-7000-8000-0000000000d1';
const THYROID = '0190a8f2-0000-7000-8000-0000000000d2';

/** Three people on one checklist, which is the situation this panel exists for. */
const RINA = '0190a8f2-0000-7000-8000-0000000000c1';
const SHILPI = '0190a8f2-0000-7000-8000-0000000000c2';
const NAHID = '0190a8f2-0000-7000-8000-0000000000c9';

const ROOMS: CounselingRoom[] = [
  {
    room: 'COUNSELING_ROOM',
    display_en: 'Counselling room',
    display_bn: 'কাউন্সেলিং কক্ষ',
    station_code: 'COUNSELING',
    ordering: 1,
  },
  {
    room: 'NUTRITION_ROOM',
    display_en: 'Nutrition room',
    display_bn: 'পুষ্টি কক্ষ',
    station_code: 'NUTRITION',
    ordering: 2,
  },
  {
    room: 'INSULIN_CORNER',
    display_en: 'Insulin corner',
    display_bn: 'ইনসুলিন কর্নার',
    station_code: 'COUNSELING',
    ordering: 3,
  },
];

function item(over: Partial<CounselingItem> = {}): CounselingItem {
  return {
    item_code: 'DIET',
    ordering: 1,
    text_en: 'Diet advice',
    text_bn: 'খাদ্য পরামর্শ',
    mandatory: true,
    room: 'COUNSELING_ROOM',
    room_en: 'Counselling room',
    room_bn: 'কাউন্সেলিং কক্ষ',
    ...over,
  };
}

const ITEMS: CounselingItem[] = [
  item({ item_code: 'DIET', ordering: 1 }),
  item({
    item_code: 'FOOT_CARE',
    ordering: 2,
    text_en: 'Foot care',
    text_bn: 'পায়ের যত্ন',
    mandatory: false,
  }),
  item({
    item_code: 'PORTION',
    ordering: 3,
    text_en: 'Portion sizes',
    text_bn: 'খাবারের পরিমাণ',
    room: 'NUTRITION_ROOM',
    room_en: 'Nutrition room',
    room_bn: 'পুষ্টি কক্ষ',
  }),
  item({
    item_code: 'INSULIN_TECHNIQUE',
    ordering: 4,
    text_en: 'Insulin injection technique',
    text_bn: 'ইনসুলিন দেওয়ার কৌশল',
    room: 'INSULIN_CORNER',
    room_en: 'Insulin corner',
    room_bn: 'ইনসুলিন কর্নার',
  }),
  item({
    item_code: 'HYPO',
    ordering: 5,
    text_en: 'Recognising a hypo',
    text_bn: 'হাইপো চেনা',
  }),
];

/** 10:00 in Dhaka. Every timestamp below is an offset from it. */
const STARTED = '2026-09-14T04:00:00Z';

const TICKS: CounselingTick[] = [
  {
    item_code: 'DIET',
    ticked_at: '2026-09-14T04:00:30Z',
    ticked_by: RINA,
    ticked_role: 'COUNSELOR',
    ticked_by_code: 'E041',
    ticked_by_name_en: 'Rina Akter',
    ticked_by_name_bn: 'রিনা আক্তার',
    note: 'Her daughter wrote the meal times down.',
    undo_count: 0,
  },
  {
    item_code: 'PORTION',
    ticked_at: '2026-09-14T04:03:30Z',
    ticked_by: SHILPI,
    ticked_role: 'NUTRITIONIST',
    ticked_by_code: 'E052',
    ticked_by_name_en: 'Shilpi Rahman',
    ticked_by_name_bn: 'শিল্পী রহমান',
    undo_count: 0,
  },
  {
    item_code: 'INSULIN_TECHNIQUE',
    ticked_at: '2026-09-14T04:04:00Z',
    ticked_by: RINA,
    ticked_role: 'COUNSELOR',
    ticked_by_code: 'E041',
    ticked_by_name_en: 'Rina Akter',
    ticked_by_name_bn: 'রিনা আক্তার',
    undone_at: '2026-09-14T04:06:00Z',
    undone_by: NAHID,
    undone_by_code: 'E010',
    undone_by_name_en: 'Dr Nahid Chowdhury',
    undone_by_name_bn: 'ডা. নাহিদ চৌধুরী',
    undone_reason: 'She had not brought her pen; nothing was demonstrated.',
    undo_count: 1,
  },
];

/** The second counsellor on the floor. Same role as Rina, and the reason a name is needed. */
const KHALEDA_TICK: CounselingTick = {
  item_code: 'HYPO',
  ticked_at: '2026-09-14T04:05:00Z',
  ticked_by: '0190a8f2-0000-7000-8000-0000000000c3',
  ticked_role: 'COUNSELOR',
  ticked_by_code: 'E047',
  ticked_by_name_en: 'Khaleda Begum',
  ticked_by_name_bn: 'খালেদা বেগম',
  undo_count: 0,
};

function session(over: Partial<CounselingSession> = {}): CounselingSession {
  return {
    id: DIABETES_SESSION,
    patient_id: PATIENT,
    visit_id: VISIT,
    template_id: DIABETES,
    template_version: 1,
    template_code: 'DIABETES',
    title_en: 'Diabetes counselling',
    title_bn: 'ডায়াবেটিস কাউন্সেলিং',
    started_at: STARTED,
    started_by: RINA,
    started_role: 'COUNSELOR',
    started_by_code: 'E041',
    started_by_name_en: 'Rina Akter',
    started_by_name_bn: 'রিনা আক্তার',
    approved_at: '2026-02-01T00:00:00Z',
    outstanding: ['INSULIN_TECHNIQUE', 'HYPO'],
    ...over,
  };
}

/**
 * The index row, as `GET /v1/counseling/visits/{id}/sessions` actually returns it.
 *
 * Deliberately stripped of the fields only the single-session read joins: `approved_at` and
 * the staff names. A card that assumed the index carried them would look right in a test and
 * be blank on the floor.
 */
function indexRow(over: Partial<CounselingSession> = {}): CounselingSession {
  const row = session(over);
  delete (row as { approved_at?: string }).approved_at;
  delete (row as { started_by_code?: string }).started_by_code;
  delete (row as { started_by_name_en?: string }).started_by_name_en;
  delete (row as { started_by_name_bn?: string }).started_by_name_bn;
  delete (row as { completed_by_code?: string }).completed_by_code;
  delete (row as { completed_by_name_en?: string }).completed_by_name_en;
  delete (row as { completed_by_name_bn?: string }).completed_by_name_bn;
  return row;
}

/** The full read: the frozen list, every tick including the withdrawn one, what is missing. */
const DETAIL: CounselingSession = session({ items: ITEMS, ticks: TICKS });

function missing(over: Partial<CounselingMissingItem> = {}): CounselingMissingItem {
  return {
    template_id: DIABETES,
    template_code: 'DIABETES',
    title_en: 'Diabetes counselling',
    title_bn: 'ডায়াবেটিস কাউন্সেলিং',
    session_id: DIABETES_SESSION,
    item_code: 'HYPO',
    room: 'COUNSELING_ROOM',
    room_en: 'Counselling room',
    room_bn: 'কাউন্সেলিং কক্ষ',
    room_step: 1,
    text_en: 'Recognising a hypo',
    text_bn: 'হাইপো চেনা',
    ...over,
  };
}

/** A checklist nobody opened: no `session_id`, and a different remediation path. */
const NOT_STARTED = missing({
  template_id: THYROID,
  template_code: 'THYROID',
  title_en: 'Thyroid counselling',
  title_bn: 'থাইরয়েড কাউন্সেলিং',
  item_code: 'TAKE_ON_EMPTY_STOMACH',
  text_en: 'Take levothyroxine on an empty stomach',
  text_bn: 'খালি পেটে লেভোথাইরক্সিন খাবেন',
});
delete (NOT_STARTED as { session_id?: string }).session_id;

const INSULIN_MISSING = missing({
  item_code: 'INSULIN_TECHNIQUE',
  room: 'INSULIN_CORNER',
  room_en: 'Insulin corner',
  room_bn: 'ইনসুলিন কর্নার',
  room_step: 3,
  text_en: 'Insulin injection technique',
  text_bn: 'ইনসুলিন দেওয়ার কৌশল',
});

const BLOCKED: CounselingGate = {
  visit_id: VISIT,
  blocked: true,
  overridden: false,
  // In the order `core.counseling_gate_missing` now returns them: by checklist, then by the
  // room's place in §5.2's walk, then by item code.
  missing: [missing(), INSULIN_MISSING, NOT_STARTED],
};

const CLEAR: CounselingGate = { visit_id: VISIT, blocked: false, overridden: false, missing: [] };

const OVERRIDE: CounselingGateOverride = {
  id: '0190a8f2-0000-7000-8000-0000000000f1',
  visit_id: VISIT,
  patient_id: PATIENT,
  granted_at: '2026-09-14T04:30:00Z',
  granted_by: NAHID,
  granted_role: 'PHYSICIAN',
  granted_by_code: 'E010',
  granted_by_name_en: 'Dr Nahid Chowdhury',
  granted_by_name_bn: 'ডা. নাহিদ চৌধুরী',
  reason: 'The insulin corner was closed and her daughter was waiting with the car.',
  // `GONE_SINCE` was outstanding then and is not now — somebody covered it afterwards.
  missing_at_grant: ['INSULIN_TECHNIQUE', 'HYPO', 'GONE_SINCE'],
};

const OVERRIDDEN: CounselingGate = {
  visit_id: VISIT,
  blocked: false,
  overridden: true,
  missing: [missing(), INSULIN_MISSING],
  override: OVERRIDE,
};

function visit(over: Partial<Visit> = {}): Visit {
  return {
    id: VISIT,
    patient_id: PATIENT,
    visit_code: 'V-2026-0914-017',
    visit_type: 'follow_up',
    status: 'open',
    clinic_day: '2026-09-14',
    opened_at: '2026-09-14T03:40:00Z',
    reopened_count: 0,
    ...over,
  };
}

/** Closed, and opened *later* than the open one — so "the newest" would pick the wrong visit. */
const CLOSED_AND_NEWER = visit({
  id: OLD_VISIT,
  visit_code: 'V-2026-0915-004',
  status: 'closed',
  opened_at: '2026-09-15T03:00:00Z',
  closed_at: '2026-09-15T06:00:00Z',
});

const initialSession = useSessionStore.getInitialState();

function holding(permissions: string[]) {
  useSessionStore.setState({
    ...initialSession,
    status: 'authenticated',
    user: {
      id: NAHID,
      employeeCode: 'E010',
      nameEN: 'Dr Nahid',
      nameBN: 'ডা. নাহিদ',
      facilityId: '11111111-1111-4111-8111-111111111111',
      roles: ['PHYSICIAN'],
      grants: { PHYSICIAN: permissions },
      permissions,
      secondFactor: { required: false, enrolled: true, pending: false, recoveryCodesLeft: 8 },
    },
    activeRole: 'PHYSICIAN',
  });
}

/** A chief consultant: reads sessions and checklists, and may open the valve. */
const PHYSICIAN_PERMISSIONS = [
  'counseling.session.read',
  'counseling.template.read',
  'counseling.gate.override',
];

/** A counsellor: reads and ticks, and may not send anybody past a checkpoint. */
const COUNSELLOR_PERMISSIONS = [
  'counseling.session.read',
  'counseling.template.read',
  'counseling.tick',
];

beforeEach(() => {
  vi.clearAllMocks();
  holding(PHYSICIAN_PERMISSIONS);
  listPatientVisits.mockResolvedValue([visit()]);
  listCounselingRooms.mockResolvedValue(ROOMS);
  getCounselingGate.mockResolvedValue(BLOCKED);
  // The index, without the joins only the single-session read makes.
  listCounselingSessionsForVisit.mockResolvedValue([indexRow()]);
  getCounselingSession.mockResolvedValue(DETAIL);
});

afterEach(() => {
  useSessionStore.setState(initialSession, true);
  vi.restoreAllMocks();
});

async function openPanel(locale: 'en' | 'bn' = 'en') {
  renderWithProviders(<CounselingPanel patientId={PATIENT} />, { locale });
  return screen.findByTestId('counseling-panel');
}

async function openGate(locale: 'en' | 'bn' = 'en', visitOpen = true) {
  renderWithProviders(<GateState visitId={VISIT} visitOpen={visitOpen} />, { locale });
  return screen.findByTestId('counseling-gate');
}

describe('the panel finds the visit it is about', () => {
  it('names the visit by the code a desk speaks, not by its id', async () => {
    await openPanel();
    expect(screen.getByTestId('counseling-visit')).toHaveTextContent('V-2026-0914-017');
  });

  it('takes the open visit rather than the most recent one', async () => {
    // A patient who came yesterday and is here again. Picking "the newest" would put
    // yesterday's counselling in front of a physician seeing today's patient.
    listPatientVisits.mockResolvedValue([CLOSED_AND_NEWER, visit()]);
    const panel = await openPanel();
    expect(panel).toHaveAttribute('data-visit', VISIT);
    expect(panel).toHaveAttribute('data-visit-open', 'true');
  });

  it('falls back to the last visit and says that visit is closed', async () => {
    listPatientVisits.mockResolvedValue([CLOSED_AND_NEWER]);
    const panel = await openPanel();
    expect(panel).toHaveAttribute('data-visit-open', 'false');
    expect(screen.getByText('This visit is closed')).toBeVisible();
    expect(
      screen.getByText(/not the patient sitting in front of you now/, { exact: false }),
    ).toBeVisible();
  });

  it('does not offer the override on a closed visit', async () => {
    // Sending somebody past a checkpoint is an act about a patient waiting in the building.
    // On a visit that ended last month it is a row in the rate view describing nothing.
    listPatientVisits.mockResolvedValue([CLOSED_AND_NEWER]);
    getCounselingGate.mockResolvedValue({ ...BLOCKED, visit_id: OLD_VISIT });
    await openPanel();
    await screen.findByTestId('counseling-gate');
    expect(screen.queryByTestId('open-override')).toBeNull();
  });

  it('says there is no visit rather than drawing an empty checkpoint', async () => {
    // A registered patient nobody has seen yet. Not an error, and emphatically not a
    // patient with nothing outstanding.
    listPatientVisits.mockResolvedValue([]);
    renderWithProviders(<CounselingPanel patientId={PATIENT} />);
    expect(await screen.findByText('This patient has no visit yet')).toBeVisible();
    expect(screen.queryByTestId('counseling-gate')).toBeNull();
  });

  it('says loudly when the visits cannot be read', async () => {
    listPatientVisits.mockRejectedValue(new Error('down'));
    renderWithProviders(<CounselingPanel patientId={PATIENT} />);
    expect(await screen.findByText('This patient’s visits could not be read.')).toBeVisible();
    expect(
      screen.getByText(/not read this as a patient nobody has ever counselled/, { exact: false }),
    ).toBeVisible();
  });

  it('shows nothing to somebody who may not read counselling sessions', async () => {
    holding(['patient.read.demographics']);
    renderWithProviders(<CounselingPanel patientId={PATIENT} />);
    expect(
      await screen.findByText('You do not have permission to read counselling sessions.'),
    ).toBeVisible();
    expect(listPatientVisits).not.toHaveBeenCalled();
  });
});

describe('the checkpoint says which of three things is true', () => {
  it('says the counselling is holding this patient when it is', async () => {
    const gate = await openGate();
    expect(gate).toHaveAttribute('data-state', 'blocked');
    expect(screen.getByText('Counselling is holding this patient')).toBeVisible();
  });

  it('says everything has been covered when nothing is outstanding', async () => {
    getCounselingGate.mockResolvedValue(CLEAR);
    const gate = await openGate();
    expect(gate).toHaveAttribute('data-state', 'clear');
    expect(screen.getByText('Every mandatory item has been covered')).toBeVisible();
  });

  it('never draws an overridden gate as a finished checklist', async () => {
    // The named test for the whole `overridden` field. The server has already set `blocked`
    // to false, so a screen that asked only that question would say "every mandatory item
    // has been covered" about a patient whose checklist nobody finished.
    getCounselingGate.mockResolvedValue(OVERRIDDEN);
    const gate = await openGate();

    expect(gate).toHaveAttribute('data-state', 'overridden');
    expect(gate).toHaveAttribute('data-blocked', 'false');
    expect(screen.queryByText('Every mandatory item has been covered')).toBeNull();
    expect(
      screen.getByText('Sent past the checkpoint with the counselling unfinished'),
    ).toBeVisible();
    expect(
      screen.getByText(/not the same as the checklist being done/, { exact: false }),
    ).toBeVisible();
  });

  it('keeps listing what is outstanding under an override', async () => {
    // An override covers nothing. A panel that stopped naming the missing items once
    // somebody had waved the patient through would have turned the valve into a fix.
    getCounselingGate.mockResolvedValue(OVERRIDDEN);
    await openGate();
    expect(screen.getByTestId('missing-item-HYPO')).toBeVisible();
    expect(screen.getByTestId('missing-item-INSULIN_TECHNIQUE')).toBeVisible();
  });

  it('says loudly when the checkpoint cannot be read', async () => {
    getCounselingGate.mockRejectedValue(new Error('down'));
    renderWithProviders(<GateState visitId={VISIT} visitOpen />);
    expect(await screen.findByText('The counselling checkpoint could not be read.')).toBeVisible();
    expect(
      screen.getByText(/not read this as a patient with nothing outstanding/, { exact: false }),
    ).toBeVisible();
  });
});

describe('what is missing is named, and the two remediation paths read differently', () => {
  it('names each outstanding item in words rather than by code', async () => {
    await openGate();
    expect(screen.getByTestId('missing-item-HYPO')).toHaveTextContent('Recognising a hypo');
  });

  it('says which room each outstanding item belongs to', async () => {
    await openGate();
    expect(screen.getByTestId('missing-item-INSULIN_TECHNIQUE')).toHaveTextContent(
      'in the Insulin corner',
    );
  });

  it('separates an unfinished session from a checklist nobody opened', async () => {
    await openGate();
    const unfinished = within(screen.getByTestId('missing-unfinished'));
    const notStarted = within(screen.getByTestId('missing-notStarted'));

    expect(unfinished.getByTestId('missing-item-HYPO')).toBeVisible();
    expect(notStarted.getByTestId('missing-item-TAKE_ON_EMPTY_STOMACH')).toBeVisible();
    expect(unfinished.queryByTestId('missing-item-TAKE_ON_EMPTY_STOMACH')).toBeNull();
  });

  it('tells the two paths apart in words, not only by grouping', async () => {
    await openGate();
    expect(screen.getByText('Started, and not finished')).toBeVisible();
    expect(screen.getByText('Nobody has opened these checklists')).toBeVisible();
    expect(
      screen.getByText(/there is nothing to return to, and somebody has to start it/, {
        exact: false,
      }),
    ).toBeVisible();
  });

  it('draws no second list when every missing item is on one path', async () => {
    // A heading with nothing under it reads as a failed load rather than as good news.
    getCounselingGate.mockResolvedValue({ ...BLOCKED, missing: [missing()] });
    await openGate();
    expect(screen.getByTestId('missing-unfinished')).toBeVisible();
    expect(screen.queryByTestId('missing-notStarted')).toBeNull();
  });

  it('names the checklist each outstanding item is on, in words', async () => {
    // The gate carries the template's own title on every missing item now, so a refusal
    // reads as "Thyroid counselling" rather than as THYROID.
    await openGate();
    expect(screen.getByTestId('missing-item-TAKE_ON_EMPTY_STOMACH')).toHaveTextContent(
      'on the Thyroid counselling checklist',
    );
  });

  it('renders the outstanding items in the order the server sent them', async () => {
    // `core.counseling_gate_missing` orders by checklist, then by the room's place in §5.2's
    // walk, then by item code. Nothing re-sorts here: a second opinion about the order a
    // refusal reads in would disagree quietly, and this list is an instruction about which
    // room to walk the patient to.
    await openGate();
    const codes = within(screen.getByTestId('missing-unfinished'))
      .getAllByRole('listitem')
      .map((row) => row.getAttribute('data-testid'));
    expect(codes).toEqual(['missing-item-HYPO', 'missing-item-INSULIN_TECHNIQUE']);
  });

  it('names the room without fetching the room catalogue', async () => {
    // The room's own names arrive on the missing item. A refusal that waited on a second
    // request would say INSULIN_CORNER for as long as that request took, on the one screen
    // whose job is telling somebody which room to walk the patient to.
    await openGate();
    expect(screen.getByTestId('missing-item-INSULIN_TECHNIQUE')).toHaveTextContent(
      'in the Insulin corner',
    );
    expect(listCounselingRooms).not.toHaveBeenCalled();
  });

  it('reads the outstanding items in Bangla when the interface is Bangla', async () => {
    await openGate('bn');
    const row = screen.getByTestId('missing-item-INSULIN_TECHNIQUE');
    expect(row).toHaveTextContent('ইনসুলিন দেওয়ার কৌশল');
    expect(row).toHaveTextContent('ইনসুলিন কর্নার');
    expect(row).toHaveTextContent('ডায়াবেটিস কাউন্সেলিং');
  });
});

describe('the override that stands', () => {
  beforeEach(() => {
    getCounselingGate.mockResolvedValue(OVERRIDDEN);
  });

  it('names the person who granted it, with the role beside them', async () => {
    // An override is the one act on this panel with somebody's judgement in it, and "the
    // chief consultant did this" describes a post rather than the colleague a reviewer will
    // go and ask about it.
    await openGate();
    expect(screen.getByTestId('override-by')).toHaveTextContent('Dr Nahid Chowdhury');
    expect(screen.getByTestId('override-by')).toHaveTextContent('Chief consultant');
    expect(screen.getByTestId('override-granted')).toHaveTextContent('Granted');
  });

  it('names them in Bangla when the interface is Bangla', async () => {
    await openGate('bn');
    expect(screen.getByTestId('override-by')).toHaveTextContent('ডা. নাহিদ চৌধুরী');
  });

  it('falls back to the role, in a sentence, when the staff record has gone', async () => {
    // The names are joined from the staff record rather than copied onto the row, which is
    // right — somebody who changes their name reads correctly on last year's work — and
    // means the join can come back empty. A blank where a name belongs reads as a rendering
    // fault rather than as a fact.
    const nameless = { ...OVERRIDE };
    delete (nameless as { granted_by_code?: string }).granted_by_code;
    delete (nameless as { granted_by_name_en?: string }).granted_by_name_en;
    delete (nameless as { granted_by_name_bn?: string }).granted_by_name_bn;
    getCounselingGate.mockResolvedValue({ ...OVERRIDDEN, override: nameless });

    await openGate();
    expect(screen.getByTestId('override-by')).toHaveTextContent('Chief consultant');
    // And the uuid, since there is no staff code left to quote.
    expect(screen.getByTestId('override-record')).toHaveTextContent(NAHID);
  });

  it('shows the reason, because that is what makes the valve reviewable', async () => {
    await openGate();
    expect(screen.getByTestId('override-reason')).toHaveTextContent(
      'The insulin corner was closed and her daughter was waiting with the car.',
    );
  });

  it('lists what was outstanding at the moment it was granted', async () => {
    await openGate();
    const skipped = within(screen.getByTestId('override-skipped'));
    expect(skipped.getByTestId('override-skipped-INSULIN_TECHNIQUE')).toHaveTextContent(
      'Insulin injection technique',
    );
  });

  it('keeps an item covered since then on the list, by its code', async () => {
    // `missing_at_grant` is a record of that moment. An item covered afterwards has no text
    // to look up, and dropping it would make the record say the override was for less than
    // it was.
    await openGate();
    expect(screen.getByTestId('override-skipped-GONE_SINCE')).toHaveTextContent('GONE_SINCE');
  });

  it('says the list is of that moment rather than of now', async () => {
    await openGate();
    expect(screen.getByText(/not what is uncovered now/, { exact: false })).toBeVisible();
  });

  it('carries the staff code as the identifier, and not the uuid as well', async () => {
    // One identifier on a row that already carries a name. The code is what does not move
    // when somebody's name does, and it is what a reviewer quotes.
    await openGate();
    expect(screen.getByTestId('override-record')).toHaveTextContent('E010');
    expect(screen.getByTestId('override-record')).not.toHaveTextContent(NAHID);
  });
});

describe('opening the valve', () => {
  it('offers the override to somebody who holds the permission', async () => {
    await openGate();
    expect(screen.getByTestId('open-override')).toBeVisible();
  });

  it('does not offer it to a counsellor', async () => {
    // Its own server permission, marked sensitive, granted to two roles. An interface that
    // folded it into the read action would put the button on every phone on the floor.
    holding(COUNSELLOR_PERMISSIONS);
    await openGate();
    expect(screen.queryByTestId('open-override')).toBeNull();
  });

  it('does not offer it when nothing is outstanding', async () => {
    // The server refuses that with a 422, because an override on a visit the gate is not
    // holding is a row that makes the rate view lie. A control that exists to be refused
    // teaches an operator that the software is unreliable.
    getCounselingGate.mockResolvedValue(CLEAR);
    await openGate();
    expect(screen.queryByTestId('open-override')).toBeNull();
  });

  it('does not offer it when an override already stands', async () => {
    getCounselingGate.mockResolvedValue(OVERRIDDEN);
    await openGate();
    expect(screen.queryByTestId('open-override')).toBeNull();
  });

  it('will not send a blank reason', async () => {
    const user = userEvent.setup();
    await openGate();
    await user.click(screen.getByTestId('open-override'));
    expect(screen.getByTestId('override-confirm')).toBeDisabled();
    expect(overrideCounselingGate).not.toHaveBeenCalled();
  });

  it('says who reads the reason, so the box is not filled with a full stop', async () => {
    const user = userEvent.setup();
    await openGate();
    await user.click(screen.getByTestId('open-override'));
    expect(
      screen.getByText(/Read by whoever reviews how often this happens/, { exact: false }),
    ).toBeVisible();
  });

  it('states that the override covers nothing before it is pressed', async () => {
    const user = userEvent.setup();
    await openGate();
    await user.click(screen.getByTestId('open-override'));
    expect(
      screen.getByText(/every outstanding item stays outstanding/, { exact: false }),
    ).toBeVisible();
  });

  it('sends the reason and closes the form', async () => {
    const user = userEvent.setup();
    overrideCounselingGate.mockResolvedValue(OVERRIDDEN);
    await openGate();

    await user.click(screen.getByTestId('open-override'));
    await user.type(screen.getByTestId('override-reason'), 'The interpreter did not arrive.');
    await user.click(screen.getByTestId('override-confirm'));

    expect(overrideCounselingGate).toHaveBeenCalledWith(VISIT, 'The interpreter did not arrive.');
    expect(await screen.findByTestId('counseling-gate')).toBeVisible();
  });

  it('puts the server’s refusal against the reason box', async () => {
    // The server names `reason` for both of its refusals — an empty one, and an override on
    // a visit where nothing is outstanding — and its own sentence is more accurate than a
    // generic banner would be.
    const user = userEvent.setup();
    overrideCounselingGate.mockRejectedValue(
      new ApiError({
        status: 422,
        code: 'VALIDATION_FAILED',
        kind: 'validation',
        messageEN: 'Validation failed.',
        messageBN: 'যাচাই ব্যর্থ।',
        fields: { reason: 'Nothing is outstanding on this visit; there is nothing to override.' },
        correlationID: 'req_1',
      }),
    );
    await openGate();
    await user.click(screen.getByTestId('open-override'));
    await user.type(screen.getByTestId('override-reason'), 'x');
    await user.click(screen.getByTestId('override-confirm'));

    expect(
      await screen.findByText(
        'Nothing is outstanding on this visit; there is nothing to override.',
      ),
    ).toBeVisible();
  });

  it('says an override already stands when somebody got there first', async () => {
    const user = userEvent.setup();
    overrideCounselingGate.mockRejectedValue(
      new ApiError({
        status: 409,
        code: 'COUNSELING_GATE_ALREADY_OVERRIDDEN',
        kind: 'conflict',
        messageEN: 'An override already stands on this visit.',
        messageBN: 'এই ভিজিটে ইতিমধ্যে ছাড় দেওয়া আছে।',
        correlationID: 'req_2',
      }),
    );
    await openGate();
    await user.click(screen.getByTestId('open-override'));
    await user.type(screen.getByTestId('override-reason'), 'The interpreter did not arrive.');
    await user.click(screen.getByTestId('override-confirm'));

    expect(await screen.findByText('Somebody has already sent this patient past')).toBeVisible();
    expect(screen.getByText(/Only one override stands on a visit/, { exact: false })).toBeVisible();
  });

  it('does not claim an override stands for a different kind of conflict', async () => {
    // The counselling endpoints answer 409 for three different facts — an item somebody has
    // already covered, a session somebody has closed, and an idempotency key reused with a
    // different body. Branching on the number would put "somebody has already sent this
    // patient past" in front of a physician whose request failed for one of the others.
    const user = userEvent.setup();
    overrideCounselingGate.mockRejectedValue(
      new ApiError({
        status: 409,
        code: 'IDEMPOTENCY_KEY_REUSED',
        kind: 'conflict',
        messageEN: 'That event id was written with a different body.',
        messageBN: 'ওই ইভেন্ট আইডি অন্য তথ্য নিয়ে লেখা হয়েছে।',
        correlationID: 'req_3',
      }),
    );
    await openGate();
    await user.click(screen.getByTestId('open-override'));
    await user.type(screen.getByTestId('override-reason'), 'The interpreter did not arrive.');
    await user.click(screen.getByTestId('override-confirm'));

    expect(await screen.findByText('This patient was not sent past the checkpoint.')).toBeVisible();
    expect(screen.queryByText('Somebody has already sent this patient past')).toBeNull();
  });

  it('says nothing was recorded when the write fails for another reason', async () => {
    const user = userEvent.setup();
    overrideCounselingGate.mockRejectedValue(new Error('offline'));
    await openGate();
    await user.click(screen.getByTestId('open-override'));
    await user.type(screen.getByTestId('override-reason'), 'The interpreter did not arrive.');
    await user.click(screen.getByTestId('override-confirm'));

    expect(await screen.findByText('This patient was not sent past the checkpoint.')).toBeVisible();
    expect(screen.getByText('Nothing was recorded.')).toBeVisible();
  });

  it('lets the form be abandoned without writing anything', async () => {
    const user = userEvent.setup();
    await openGate();
    await user.click(screen.getByTestId('open-override'));
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByTestId('override-form')).toBeNull();
    expect(overrideCounselingGate).not.toHaveBeenCalled();
  });
});

describe('one session’s items, with attribution per item', () => {
  function renderItems(locale: 'en' | 'bn' = 'en', detail: CounselingSession = DETAIL) {
    renderWithProviders(<SessionItems session={detail} />, { locale });
  }

  it('lists the items in the order they are walked', () => {
    renderItems();
    const codes = screen
      .getAllByRole('listitem')
      .map((row) => row.getAttribute('data-item'))
      .filter((code): code is string => code !== null);
    expect(codes).toEqual(['DIET', 'FOOT_CARE', 'PORTION', 'INSULIN_TECHNIQUE', 'HYPO']);
  });

  it('names a different person for each item, not one person for the session', () => {
    // Criterion 4, and the reason this component exists. The counsellor covered the diet
    // and the nutritionist covered the portions, in one session, and "counselled by Rina"
    // would be a true sentence answering the wrong question.
    renderItems();
    expect(screen.getByTestId('panel-by-DIET')).toHaveTextContent('Rina Akter');
    expect(screen.getByTestId('panel-by-PORTION')).toHaveTextContent('Shilpi Rahman');
  });

  it('tells two people in the same role apart', () => {
    // The reason a name is needed at all. The role separates a counsellor from a
    // nutritionist and says nothing about which of two counsellors taught the hypo item —
    // and §5.4's question is about a person.
    renderItems('en', {
      ...DETAIL,
      ticks: [...TICKS, KHALEDA_TICK],
      outstanding: ['INSULIN_TECHNIQUE'],
    });
    expect(screen.getByTestId('panel-by-DIET')).toHaveTextContent('Rina Akter');
    expect(screen.getByTestId('panel-by-HYPO')).toHaveTextContent('Khaleda Begum');
    // Both are the clinical counselor, which is exactly why the name has to lead.
    expect(screen.getByTestId('panel-by-DIET')).toHaveTextContent('Clinical counselor');
    expect(screen.getByTestId('panel-by-HYPO')).toHaveTextContent('Clinical counselor');
  });

  it('keeps the role beside the name, because it says which room this was covered in', () => {
    renderItems();
    expect(screen.getByTestId('panel-by-PORTION')).toHaveTextContent('Clinical nutritionist');
  });

  it('carries the staff code of whoever covered each item, not the uuid', () => {
    // The code is the stable identifier: names are joined from the staff record and follow
    // somebody who changes theirs, and a physician quotes a code to a colleague.
    renderItems();
    expect(screen.getByTestId('panel-item-DIET')).toHaveTextContent('E041');
    expect(screen.getByTestId('panel-item-PORTION')).toHaveTextContent('E052');
    expect(screen.getByTestId('panel-item-DIET')).not.toHaveTextContent(RINA);
  });

  it('names the person in Bangla when the interface is Bangla', () => {
    renderItems('bn');
    expect(screen.getByTestId('panel-by-DIET')).toHaveTextContent('রিনা আক্তার');
  });

  it('falls back to the role in a sentence when a staff record has gone', () => {
    const nameless = { ...TICKS[0]! };
    delete (nameless as { ticked_by_code?: string }).ticked_by_code;
    delete (nameless as { ticked_by_name_en?: string }).ticked_by_name_en;
    delete (nameless as { ticked_by_name_bn?: string }).ticked_by_name_bn;
    renderItems('en', { ...DETAIL, ticks: [nameless, TICKS[1]!, TICKS[2]!] });

    expect(screen.getByTestId('panel-by-DIET')).toHaveTextContent('By Clinical counselor');
    // The uuid, since there is no code left to quote. Never a blank.
    expect(screen.getByTestId('panel-item-DIET')).toHaveTextContent(RINA);
  });

  it('measures the first item from the session opening', () => {
    // There is no earlier tick to measure from, and a duration with two different meanings
    // in one column means nothing.
    renderItems();
    expect(screen.getByTestId('panel-time-DIET')).toHaveTextContent('30 s');
    expect(screen.getByTestId('panel-time-DIET')).toHaveTextContent('after the session opened');
  });

  it('measures every other item from the tick before it', () => {
    renderItems();
    expect(screen.getByTestId('panel-time-PORTION')).toHaveTextContent('3 min 0 s');
    expect(screen.getByTestId('panel-time-PORTION')).toHaveTextContent('after the item before it');
  });

  it('says out loud that the times are gaps between ticks and not attention', () => {
    // The named test for rule 2. A physician reading "1 s" against insulin technique and
    // concluding a colleague spent a second on it would be wrong, and the interface would
    // have caused it.
    renderItems();
    expect(screen.getByTestId('time-caveat')).toHaveTextContent(
      /gap between one tick and the next/,
    );
    expect(screen.getByTestId('time-caveat')).toHaveTextContent(/it is not attention/);
  });

  it('shows no duration at all for an item nobody ticked', () => {
    renderItems();
    expect(screen.queryByTestId('panel-time-HYPO')).toBeNull();
  });

  it('reports no time rather than a negative one when a tick precedes the session', () => {
    // An offline phone carries its own clock. A negative duration on screen reads as a
    // fault in the record rather than in the clock.
    renderItems('en', {
      ...DETAIL,
      ticks: [{ ...TICKS[0]!, ticked_at: '2026-09-14T03:59:00Z' }],
      outstanding: ['INSULIN_TECHNIQUE', 'HYPO'],
    });
    expect(screen.getByTestId('panel-time-DIET')).toHaveTextContent(
      'No time can be read for this item.',
    );
  });

  it('shows the counsellor’s note beside the item it is about', () => {
    renderItems();
    expect(screen.getByTestId('panel-note-DIET')).toHaveTextContent(
      'Her daughter wrote the meal times down.',
    );
  });

  it('shows no note block on an item nobody wrote one for', () => {
    renderItems();
    expect(screen.queryByTestId('panel-note-PORTION')).toBeNull();
  });
});

describe('a withdrawn tick is history, not an absence', () => {
  it('says the item is not covered', () => {
    renderWithProviders(<SessionItems session={DETAIL} />);
    expect(screen.getByTestId('panel-state-INSULIN_TECHNIQUE')).toHaveTextContent('Not covered');
    expect(screen.getByTestId('panel-item-INSULIN_TECHNIQUE')).toHaveAttribute(
      'data-covered',
      'false',
    );
  });

  it('still says it was ticked, and when it was taken back', () => {
    renderWithProviders(<SessionItems session={DETAIL} />);
    const withdrawn = screen.getByTestId('panel-withdrawn-INSULIN_TECHNIQUE');
    expect(withdrawn).toHaveTextContent(/Ticked/);
    expect(withdrawn).toHaveTextContent(/taken back/);
  });

  it('names who took the tick back, which is not who made it', () => {
    // A counsellor correcting their own mis-tap and a physician striking out a colleague's
    // item are the same row in the ledger and very different events on the floor.
    renderWithProviders(<SessionItems session={DETAIL} />);
    expect(screen.getByTestId('panel-taken-back-by-INSULIN_TECHNIQUE')).toHaveTextContent(
      'Dr Nahid Chowdhury',
    );
    expect(screen.getByTestId('panel-by-DIET')).toHaveTextContent('Rina Akter');
  });

  it('says a record could not be read rather than leaving the un-ticker blank', () => {
    const nameless = { ...TICKS[2]! };
    delete (nameless as { undone_by_code?: string }).undone_by_code;
    delete (nameless as { undone_by_name_en?: string }).undone_by_name_en;
    delete (nameless as { undone_by_name_bn?: string }).undone_by_name_bn;
    renderWithProviders(
      <SessionItems session={{ ...DETAIL, ticks: [TICKS[0]!, TICKS[1]!, nameless] }} />,
    );
    expect(screen.getByTestId('panel-taken-back-by-INSULIN_TECHNIQUE')).toHaveTextContent(
      'whose record could not be read',
    );
  });

  it('shows the reason the tick was taken back', () => {
    // Required by the handler, by the event's validation and by a database constraint. An
    // un-tick with no reason is indistinguishable from a mis-tap, and telling those apart
    // is the entire value of recording it.
    renderWithProviders(<SessionItems session={DETAIL} />);
    expect(screen.getByTestId('panel-withdrawn-reason-INSULIN_TECHNIQUE')).toHaveTextContent(
      'She had not brought her pen; nothing was demonstrated.',
    );
  });

  it('says a reason was not recorded rather than leaving the line blank', () => {
    const stripped: CounselingTick = { ...TICKS[2]! };
    delete (stripped as { undone_reason?: string }).undone_reason;
    renderWithProviders(
      <SessionItems session={{ ...DETAIL, ticks: [TICKS[0]!, TICKS[1]!, stripped] }} />,
    );
    expect(screen.getByTestId('panel-withdrawn-reason-INSULIN_TECHNIQUE')).toHaveTextContent(
      'No reason was recorded for taking it back.',
    );
  });

  it('shows how often the item has been taken back', () => {
    renderWithProviders(<SessionItems session={DETAIL} />);
    expect(screen.getByTestId('panel-undo-INSULIN_TECHNIQUE')).toHaveTextContent(
      'Ticked and taken back once on this session',
    );
  });

  it('shows the undo count on an item that is covered now', () => {
    // `undo_count` is remembered across re-ticks, so the current state cannot show it —
    // and "ticked and taken back three times" is exactly what a quality review looks for.
    renderWithProviders(
      <SessionItems
        session={{
          ...DETAIL,
          ticks: [{ ...TICKS[0]!, undo_count: 3 }, TICKS[1]!, TICKS[2]!],
        }}
      />,
    );
    expect(screen.getByTestId('panel-undo-DIET')).toHaveTextContent(
      'Ticked and taken back 3 times on this session',
    );
    expect(screen.getByTestId('panel-state-DIET')).toHaveTextContent('Covered');
  });
});

describe('outstanding items read differently from optional ones', () => {
  it('says an outstanding mandatory item is holding the patient', () => {
    renderWithProviders(<SessionItems session={DETAIL} gate="blocked" />);
    expect(screen.getByTestId('panel-outstanding-HYPO')).toHaveTextContent(
      'Still outstanding. The checkpoint will hold this patient until it is covered.',
    );
  });

  it('stops claiming the patient is held once somebody has sent them past', () => {
    // The named test for the contradiction. The banner above says the patient was let
    // through; a row underneath saying the checkpoint will hold them until this is covered
    // is describing something that is no longer happening, and a screen that argues with
    // itself is one a physician stops reading.
    renderWithProviders(<SessionItems session={DETAIL} gate="overridden" />);
    const line = screen.getByTestId('panel-outstanding-HYPO');
    expect(line).toHaveTextContent('The patient was sent past the checkpoint without it.');
    expect(line).not.toHaveTextContent('will hold this patient');
  });

  it('marks an uncovered item exactly as strongly under an override', () => {
    // The override record exists so that what was skipped stays visible. Only the sentence
    // about the consequence changes; the fact, the marking and the rail do not.
    renderWithProviders(<SessionItems session={DETAIL} gate="overridden" />);
    expect(screen.getByTestId('panel-outstanding-HYPO')).toHaveTextContent('Still outstanding.');
    expect(screen.getByTestId('panel-item-HYPO')).toHaveAttribute('data-outstanding', 'true');
    expect(screen.getByTestId('panel-state-HYPO')).toHaveTextContent('Not covered');
  });

  it('claims no consequence at all when nobody has said what the gate is doing', () => {
    // A caller that renders this component without the gate state gets a row that is true
    // and less informative, rather than one that guesses. Silence asserts nothing.
    renderWithProviders(<SessionItems session={DETAIL} />);
    const line = screen.getByTestId('panel-outstanding-HYPO');
    expect(line).toHaveTextContent('Still outstanding.');
    expect(line).not.toHaveTextContent('will hold this patient');
    expect(line).not.toHaveTextContent('sent past the checkpoint');
  });

  it('claims no consequence when the gate and the session disagree', () => {
    // A clear gate with an outstanding item can only mean the two were read a moment apart.
    // Saying nothing about the consequence is the honest answer to a contradiction.
    renderWithProviders(<SessionItems session={DETAIL} gate="clear" />);
    const line = screen.getByTestId('panel-outstanding-HYPO');
    expect(line).toHaveTextContent('Still outstanding.');
    expect(line).not.toHaveTextContent('will hold this patient');
  });

  it('says an uncovered optional item does not hold the patient', () => {
    renderWithProviders(<SessionItems session={DETAIL} />);
    expect(screen.getByTestId('panel-optional-FOOT_CARE')).toHaveTextContent(
      'This item is optional, so it does not hold the patient.',
    );
  });

  it('takes what is outstanding from the server rather than recomputing it', () => {
    // `core.counseling_outstanding` is the one definition the gate also reads. A second
    // opinion here is how a panel shows a green tick while the gate refuses the patient
    // standing in front of it — so an item the server does not list is not marked, even
    // when it is mandatory and unticked.
    renderWithProviders(<SessionItems session={{ ...DETAIL, outstanding: [] }} />);
    expect(screen.queryByTestId('panel-outstanding-HYPO')).toBeNull();
    expect(screen.getByTestId('panel-item-HYPO')).toHaveAttribute('data-outstanding', 'false');
  });
});

describe('both languages are usable on this panel', () => {
  it('reads the items in Bangla when the interface is Bangla', () => {
    renderWithProviders(<SessionItems session={DETAIL} />, { locale: 'bn' });
    expect(screen.getByTestId('panel-item-DIET')).toHaveTextContent('খাদ্য পরামর্শ');
    expect(screen.getByTestId('panel-state-DIET')).toHaveTextContent('বোঝানো হয়েছে');
  });

  it('names the room in Bangla too', () => {
    renderWithProviders(<SessionItems session={DETAIL} />, { locale: 'bn' });
    expect(screen.getByTestId('panel-item-PORTION')).toHaveTextContent('পুষ্টি কক্ষ');
  });

  it('says the seconds in Bengali numerals, as a count in running text does', () => {
    renderWithProviders(<SessionItems session={DETAIL} />, { locale: 'bn' });
    expect(screen.getByTestId('panel-time-DIET')).toHaveTextContent('৩০ সেকেন্ড');
  });

  it('falls back to the other language rather than dropping the item', () => {
    // The opposite rule from the authoring screen, on purpose. A physician with a minute
    // and a patient in the chair needs the item that *was* covered; a blank would remove it
    // from a panel whose whole job is to say what was covered.
    const halfWritten = { ...item({ item_code: 'DIET', text_bn: '' }), ordering: 1 };
    renderWithProviders(
      <SessionItems session={{ ...DETAIL, items: [halfWritten], outstanding: [] }} />,
      { locale: 'bn' },
    );
    expect(screen.getByTestId('panel-item-DIET')).toHaveTextContent('Diet advice');
  });

  it('says the reader’s own language is missing, rather than passing the other off as theirs', () => {
    const halfWritten = { ...item({ item_code: 'DIET', text_bn: '' }), ordering: 1 };
    renderWithProviders(
      <SessionItems session={{ ...DETAIL, items: [halfWritten], outstanding: [] }} />,
      { locale: 'bn' },
    );
    expect(screen.getByTestId('panel-fallback-DIET')).toHaveTextContent('বাংলা এখনো লেখা হয়নি');
  });

  it('keeps an item with no text in either language on the screen, by its code', () => {
    // An item that vanished from the panel is an item nobody covers and nobody misses.
    const blank = { ...item({ item_code: 'MYSTERY', text_en: '', text_bn: '' }), ordering: 1 };
    renderWithProviders(<SessionItems session={{ ...DETAIL, items: [blank], outstanding: [] }} />);
    expect(screen.getByTestId('panel-untitled-MYSTERY')).toBeVisible();
    expect(screen.getByTestId('panel-item-MYSTERY')).toHaveTextContent('MYSTERY');
  });
});

describe('the session header states facts about the session', () => {
  it('names the checklist and the version it walked', async () => {
    renderWithProviders(<SessionList visitId={VISIT} />);
    await screen.findByTestId('counseling-sessions');
    expect(screen.getByText('Diabetes counselling')).toBeVisible();
    expect(screen.getByText('Version 1')).toBeVisible();
  });

  it('says who opened the session as opening, not as counselling', async () => {
    // The whole point of the rows below is that opening a checklist and covering an item
    // are different acts by different people.
    renderWithProviders(<SessionList visitId={VISIT} />);
    expect(
      await screen.findByText(/by Rina Akter, Clinical counselor/, { exact: false }),
    ).toBeVisible();
  });

  it('names the opener from the detail, since the index does not join the staff record', async () => {
    // The index carries the role and not the name. A card that assumed otherwise would look
    // right in a test and read as a blank on the floor, so the header shows the role at once
    // and fills the name in when the detail lands.
    getCounselingSession.mockImplementation(() => new Promise(() => {}));
    renderWithProviders(<SessionList visitId={VISIT} />);
    await screen.findByTestId('counseling-sessions');
    expect(screen.getByTestId(`session-started-${DIABETES_SESSION}`)).toHaveTextContent(
      'by Clinical counselor',
    );
    expect(screen.getByTestId(`session-started-${DIABETES_SESSION}`)).not.toHaveTextContent(
      'Rina Akter',
    );
  });

  it('says a session is still open when no counsellor has closed it', async () => {
    renderWithProviders(<SessionList visitId={VISIT} />);
    await screen.findByTestId('counseling-sessions');
    expect(screen.getByTestId(`session-finished-${DIABETES_SESSION}`)).toHaveTextContent(
      'Still open — no counsellor has said they are done.',
    );
  });

  it('names who closed the session, which may be somebody who covered nothing', async () => {
    getCounselingSession.mockResolvedValue({
      ...DETAIL,
      completed_at: '2026-09-14T04:20:00Z',
      completed_by: RINA,
      completed_by_code: 'E041',
      completed_by_name_en: 'Rina Akter',
      completed_by_name_bn: 'রিনা আক্তার',
    });
    listCounselingSessionsForVisit.mockResolvedValue([
      indexRow({ completed_at: '2026-09-14T04:20:00Z', completed_by: RINA }),
    ]);
    renderWithProviders(<SessionList visitId={VISIT} />);
    expect(await screen.findByTestId(`session-closed-by-${DIABETES_SESSION}`)).toHaveTextContent(
      'Closed by Rina Akter.',
    );
  });

  it('says when a session was closed, without implying anything was covered', async () => {
    // A counsellor may finish with items outstanding: the patient left, the interpreter did
    // not arrive. Completing ticks nothing.
    listCounselingSessionsForVisit.mockResolvedValue([
      indexRow({ completed_at: '2026-09-14T04:20:00Z', completed_by: RINA }),
    ]);
    renderWithProviders(<SessionList visitId={VISIT} />);
    await screen.findByTestId('counseling-sessions');
    expect(screen.getByTestId(`session-finished-${DIABETES_SESSION}`)).toHaveTextContent('Closed');
    expect(screen.getByTestId(`session-outstanding-${DIABETES_SESSION}`)).toHaveTextContent(
      '2 items that must be covered',
    );
  });

  it('counts what is outstanding from the session’s own list', async () => {
    renderWithProviders(<SessionList visitId={VISIT} />);
    await screen.findByTestId('counseling-sessions');
    expect(screen.getByTestId(`session-outstanding-${DIABETES_SESSION}`)).toHaveTextContent(
      '2 items that must be covered',
    );
  });

  it('says nothing is outstanding rather than showing a bare zero', async () => {
    listCounselingSessionsForVisit.mockResolvedValue([indexRow({ outstanding: [] })]);
    renderWithProviders(<SessionList visitId={VISIT} />);
    await screen.findByTestId('counseling-sessions');
    expect(screen.getByTestId(`session-outstanding-${DIABETES_SESSION}`)).toHaveTextContent(
      'every item that must be covered has been',
    );
  });

  it('shows every checklist walked on the visit at once, without a tab to choose', async () => {
    // A physician asking "what did they teach you about injection sites" does not know
    // which checklist that item is on, and a tab strip makes them guess before they can
    // look.
    listCounselingSessionsForVisit.mockResolvedValue([
      indexRow(),
      indexRow({
        id: THYROID_SESSION,
        template_id: THYROID,
        template_code: 'THYROID',
        title_en: 'Thyroid counselling',
        title_bn: 'থাইরয়েড কাউন্সেলিং',
        outstanding: [],
      }),
    ]);
    renderWithProviders(<SessionList visitId={VISIT} />);
    await screen.findByTestId('counseling-sessions');
    expect(screen.getByTestId(`session-${DIABETES_SESSION}`)).toBeVisible();
    expect(screen.getByTestId(`session-${THYROID_SESSION}`)).toBeVisible();
  });

  it('marks a checklist nobody has approved (D-53)', async () => {
    // The seeded diabetes version is live and is a proposal: its items are transcribed from
    // §5.1 as a launch minimum and their Bangla and guidance are the engineer's. A
    // physician about to ask a patient what they were taught is entitled to know that.
    const unapproved = { ...DETAIL };
    delete (unapproved as { approved_at?: string }).approved_at;
    getCounselingSession.mockResolvedValue(unapproved);

    renderWithProviders(<SessionList visitId={VISIT} />);
    expect(await screen.findByText('Content not approved by a clinician')).toBeVisible();
  });

  it('reads the approval from the session rather than fetching the version', async () => {
    // `approved_at` is on the session now, so the panel answers D-53 without a second read
    // of the template — which also drops a dependency on the template read permission.
    renderWithProviders(<SessionList visitId={VISIT} />);
    await screen.findByTestId(`session-${DIABETES_SESSION}`);
    expect(getCounselingVersion).not.toHaveBeenCalled();
    expect(screen.queryByText('Content not approved by a clinician')).toBeNull();
  });

  it('claims nothing about approval before the session detail arrives', async () => {
    // Silence is not a claim in either direction. The panel never says a checklist *is*
    // approved, so an absent notice asserts nothing.
    getCounselingSession.mockRejectedValue(new Error('down'));
    renderWithProviders(<SessionList visitId={VISIT} />);
    await screen.findByTestId('counseling-sessions');
    expect(screen.queryByText('Content not approved by a clinician')).toBeNull();
  });

  it('carries the checkpoint’s state down to the items without a second request', async () => {
    // Read once in the list, on the key the gate banner already uses, so both are served
    // from one request — and so every row agrees with the banner above it.
    getCounselingGate.mockResolvedValue(OVERRIDDEN);
    renderWithProviders(<SessionList visitId={VISIT} />);
    await screen.findByTestId(`session-${DIABETES_SESSION}`);

    expect(await screen.findByTestId('panel-outstanding-HYPO')).toHaveTextContent(
      'The patient was sent past the checkpoint without it.',
    );
    expect(getCounselingGate).toHaveBeenCalledTimes(1);
  });

  it('says loudly when the sessions cannot be read', async () => {
    // "No checklist was walked" and "the checklists could not be read" look identical as an
    // empty list, and one of them tells a physician nobody counselled this patient.
    listCounselingSessionsForVisit.mockRejectedValue(new Error('down'));
    renderWithProviders(<SessionList visitId={VISIT} />);
    expect(await screen.findByText('The counselling sessions could not be read.')).toBeVisible();
  });

  it('says loudly when one session’s items cannot be read, keeping its header', async () => {
    getCounselingSession.mockRejectedValue(new Error('down'));
    renderWithProviders(<SessionList visitId={VISIT} />);
    await screen.findByTestId('counseling-sessions');
    expect(await screen.findByText('The items on this checklist could not be read.')).toBeVisible();
    // The header survives, because the detail response carries no title of its own.
    expect(screen.getByText('Diabetes counselling')).toBeVisible();
  });

  it('says no checklist was opened when the visit has none', async () => {
    listCounselingSessionsForVisit.mockResolvedValue([]);
    renderWithProviders(<SessionList visitId={VISIT} />);
    expect(await screen.findByText('No checklist has been opened on this visit')).toBeVisible();
  });

  it('says a session walked a version with no items rather than drawing it as finished', async () => {
    getCounselingSession.mockResolvedValue({ ...DETAIL, items: [], ticks: [] });
    renderWithProviders(<SessionList visitId={VISIT} />);
    expect(await screen.findByTestId('session-no-items')).toBeVisible();
  });
});

describe('nothing on this panel ticks anything', () => {
  it('exposes no tick, un-tick, complete or start call from the gate module', () => {
    // The named test for the rule. A golden list of the HTTP calls this module makes: a new
    // endpoint added here fails this test and forces somebody to decide, on purpose,
    // whether the physician's panel should be able to write on a counsellor's checklist.
    const source = readFileSync(
      join(webRoot, 'src', 'features', 'counseling', 'api', 'gate.ts'),
      'utf8',
    );
    const calls = [...source.matchAll(/api\.(GET|POST|PUT|PATCH|DELETE)\('([^']+)'/g)].map(
      (match) => `${match[1]} ${match[2]}`,
    );

    expect(calls.sort()).toEqual([
      'GET /v1/counseling/gate/overrides',
      'GET /v1/counseling/sessions/{sessionId}',
      'GET /v1/counseling/visits/{visitId}/gate',
      'GET /v1/counseling/visits/{visitId}/sessions',
      'POST /v1/counseling/visits/{visitId}/gate/override',
    ]);

    for (const banned of [
      'tickCounselingItem',
      'untickCounselingItem',
      'completeCounselingSession',
      'startCounselingSession',
    ]) {
      expect(Object.keys(gateModule)).not.toContain(banned);
    }
  });

  it('renders no control at all inside a session’s items', () => {
    // The other half, on the screen rather than in the module. A checkbox here is a
    // physician covering an item on a counsellor's behalf, from a chair, having said
    // nothing to the patient.
    renderWithProviders(<SessionItems session={DETAIL} />);
    expect(screen.queryAllByRole('checkbox')).toEqual([]);
    expect(screen.queryAllByRole('button')).toEqual([]);
    expect(screen.queryAllByRole('textbox')).toEqual([]);
  });

  it('offers no tick control to somebody who holds the tick permission', async () => {
    // A counsellor reading this panel holds `counseling.tick`. They tick on their phone,
    // against a session they opened. This surface is not that surface.
    holding(COUNSELLOR_PERMISSIONS);
    renderWithProviders(<SessionList visitId={VISIT} />);
    await screen.findByTestId(`session-${DIABETES_SESSION}`);
    expect(screen.queryAllByRole('checkbox')).toEqual([]);
    expect(screen.queryAllByRole('button')).toEqual([]);
  });

  it('has exactly one control on the whole panel, and it is the override', async () => {
    await openPanel();
    await screen.findByTestId(`session-${DIABETES_SESSION}`);
    const buttons = screen.getAllByRole('button');
    expect(buttons).toHaveLength(1);
    expect(buttons[0]).toHaveAttribute('data-testid', 'open-override');
  });
});

describe('the rules the panel is built on', () => {
  const { gateState, missingByRemediation, skippedAtGrant, staffLabel } = surface;
  const { itemProgress, elapsedParts, ticksInOrder, coveredCount, isFinished } = surface;

  it('answers overridden rather than clear when an override stands', () => {
    // The server sets `blocked` to false once an override exists, so a two-state answer
    // would round the override up to "nothing to do".
    expect(gateState(OVERRIDDEN)).toBe('overridden');
    expect(gateState(BLOCKED)).toBe('blocked');
    expect(gateState(CLEAR)).toBe('clear');
  });

  it('splits missing items on whether a checklist was ever opened', () => {
    const split = missingByRemediation(BLOCKED);
    expect(split.unfinished.map((row) => row.item_code)).toEqual(['HYPO', 'INSULIN_TECHNIQUE']);
    expect(split.notStarted.map((row) => row.item_code)).toEqual(['TAKE_ON_EMPTY_STOMACH']);
  });

  it('holds no opinion of its own about the order the missing items read in', () => {
    // `core.counseling_gate_missing` carries the ORDER BY now — checklist, then the room's
    // place in the walk, then item code. The client-side sort that used to live here would
    // be a second opinion about the order of a refusal, held where nobody would look when
    // the two disagreed.
    expect(surface).not.toHaveProperty('missingInOrder');
  });

  it('names a person, keeps the role beside them, and prefers the staff code', () => {
    const who = staffLabel(
      {
        code: 'E041',
        name_en: 'Rina Akter',
        name_bn: 'রিনা আক্তার',
        role: 'COUNSELOR',
        id: RINA,
      },
      'en',
    );
    expect(who).toEqual({ name: 'Rina Akter', code: 'E041', role: 'COUNSELOR', id: null });
  });

  it('gives the name in the reader’s language, and the other where theirs is missing', () => {
    expect(staffLabel({ name_en: 'Rina Akter', name_bn: 'রিনা আক্তার' }, 'bn').name).toBe(
      'রিনা আক্তার',
    );
    // An English name is more use to a reader of Bangla than a blank.
    expect(staffLabel({ name_en: 'Rina Akter' }, 'bn').name).toBe('Rina Akter');
  });

  it('answers with no name at all when the staff record has gone', () => {
    // Rather than an empty string the caller would render as a blank. The names are joined
    // from the staff record, so this is a state that happens.
    const who = staffLabel({ role: 'COUNSELOR', id: RINA }, 'en');
    expect(who.name).toBeNull();
    expect(who.code).toBeNull();
    expect(who.id).toBe(RINA);
  });

  it('offers the uuid only when there is no staff code, never both', () => {
    // One identifier on a row that already carries a name.
    expect(staffLabel({ code: 'E041', id: RINA }, 'en').id).toBeNull();
    expect(staffLabel({ id: RINA }, 'en').id).toBe(RINA);
  });

  it('keeps a skipped item with no current match, carrying its code', () => {
    const skipped = skippedAtGrant(OVERRIDE, OVERRIDDEN.missing);
    expect(skipped.map((row) => row.itemCode)).toEqual(['INSULIN_TECHNIQUE', 'HYPO', 'GONE_SINCE']);
    expect(skipped[2]?.item).toBeUndefined();
    expect(skipped[0]?.item?.text_en).toBe('Insulin injection technique');
  });

  it('treats a withdrawn tick as not covered, and still returns it', () => {
    const rows = itemProgress(DETAIL);
    const insulin = rows.find((row) => row.item.item_code === 'INSULIN_TECHNIQUE');
    expect(insulin?.covered).toBe(false);
    expect(insulin?.withdrawn).toBe(true);
    expect(insulin?.tick?.undone_reason).toContain('nothing was demonstrated');
  });

  it('measures the first tick from the session start and the rest from each other', () => {
    const rows = itemProgress(DETAIL);
    const byCode = new Map(rows.map((row) => [row.item.item_code, row]));
    expect(byCode.get('DIET')?.elapsedMs).toBe(30_000);
    expect(byCode.get('DIET')?.fromSessionStart).toBe(true);
    expect(byCode.get('PORTION')?.elapsedMs).toBe(180_000);
    expect(byCode.get('PORTION')?.fromSessionStart).toBe(false);
  });

  it('counts a withdrawn tick’s own clock, so the next item is not credited with it', () => {
    // A withdrawn tick happened and took time on the floor. Dropping it from the sequence
    // would silently add its share of the clock to whatever was ticked next.
    const chronological = ticksInOrder(DETAIL).map((tick) => tick.item_code);
    expect(chronological).toEqual(['DIET', 'PORTION', 'INSULIN_TECHNIQUE']);
  });

  it('answers no elapsed time for an item that was never ticked', () => {
    const rows = itemProgress(DETAIL);
    expect(rows.find((row) => row.item.item_code === 'HYPO')?.elapsedMs).toBeNull();
  });

  it('does not count a withdrawn tick as coverage', () => {
    expect(coveredCount(DETAIL)).toBe(2);
  });

  it('reads whether a session is finished from the timestamp a counsellor set', () => {
    // Never from what is outstanding. A session may close with items missing, and a session
    // with nothing missing may still be open.
    expect(isFinished(DETAIL)).toBe(false);
    expect(isFinished(session({ completed_at: '2026-09-14T04:20:00Z' }))).toBe(true);
    expect(isFinished(session({ outstanding: [] }))).toBe(false);
  });

  it('splits a duration into minutes and seconds for a message file to word', () => {
    expect(elapsedParts(30_000)).toEqual({ minutes: 0, seconds: 30 });
    expect(elapsedParts(180_000)).toEqual({ minutes: 3, seconds: 0 });
    expect(elapsedParts(95_400)).toEqual({ minutes: 1, seconds: 35 });
  });

  it('never returns a negative duration', () => {
    expect(elapsedParts(-5_000)).toEqual({ minutes: 0, seconds: 0 });
  });

  it('refuses a blank override reason before the server has to', () => {
    expect(surface.overrideReasonAcceptable('')).toBe(false);
    expect(surface.overrideReasonAcceptable('   ')).toBe(false);
    expect(surface.overrideReasonAcceptable('The interpreter did not arrive.')).toBe(true);
  });

  it('spells each cache key once, so two screens cannot disagree', () => {
    expect(surface.counselingGateKey(VISIT)).toEqual(['counseling', 'gate', VISIT]);
    expect(surface.counselingVisitSessionsKey(VISIT)).toEqual([
      'counseling',
      'sessions',
      'visit',
      VISIT,
    ]);
    expect(surface.counselingSessionKey(DIABETES_SESSION)).toEqual([
      'counseling',
      'session',
      DIABETES_SESSION,
    ]);
    expect(surface.counselingOverridesKey({ from: '2026-09-01' })).toEqual([
      'counseling',
      'overrides',
      '2026-09-01',
      null,
    ]);
  });
});

describe('the interface actions match the server’s permissions', () => {
  it('asks for the read permission the server grants, not the tick one', async () => {
    const { requirementsOf } = await import('@/lib/permissions');
    expect(requirementsOf('counseling.sessions.view')).toEqual(['counseling.session.read']);
    expect(requirementsOf('counseling.gate.override')).toEqual(['counseling.gate.override']);
  });

  it('puts the override rate behind quality’s permission and not the physician’s', async () => {
    // The answer to a rising override rate is a person asking why, and that person is not
    // the one granting them.
    const { requirementsOf } = await import('@/lib/permissions');
    expect(requirementsOf('counseling.overrides.review')).toEqual(['qa.review']);
  });

  it('offers the panel as a screen of the patient’s, under its own permission', async () => {
    const { PATIENT_SUBROUTES } = await import('@/lib/navigation');
    const route = PATIENT_SUBROUTES.find((entry) => entry.segment === 'counseling');
    expect(route?.permission).toBe('counseling.sessions.view');
    expect(route?.labelKey).toBe('counseling.panel.pageTitle');
  });
});

/**
 * The calls themselves, over a stubbed fetch.
 *
 * `vi.importActual` rather than the mocked module above: everything else in this file wants
 * the network stubbed out, and these five want the opposite — what actually goes on the wire
 * is the thing under test.
 */
describe('what the calls put on the wire', () => {
  function server(routes: Record<string, (request: Request) => Response>): Request[] {
    const seen: Request[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const request = new Request(input, init);
        seen.push(request);
        const key = `${request.method} ${new URL(request.url).pathname}`;
        const handler = routes[key];
        if (!handler) throw new Error(`no route for ${key}`);
        return handler(request);
      }),
    );
    return seen;
  }

  function respond(body: unknown, status = 200) {
    return new Response(JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'req_gate_1' },
    });
  }

  const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('unwraps the gate rather than answering with a boolean', async () => {
    server({ [`GET /v1/counseling/visits/${VISIT}/gate`]: () => respond({ gate: BLOCKED }) });
    const gate = await realGate.getCounselingGate(VISIT);
    expect(gate.missing).toHaveLength(3);
    expect(gate.blocked).toBe(true);
  });

  it('sends the override with a trimmed reason and an idempotency key', async () => {
    // A retry after a lost reply must write the same override rather than a second one, and
    // a reason padded with spaces is the same reason.
    const seen = server({
      [`POST /v1/counseling/visits/${VISIT}/gate/override`]: () => respond({ gate: OVERRIDDEN }),
    });
    await realGate.overrideCounselingGate(VISIT, '  The interpreter did not arrive.  ');

    const body = (await seen[0]!.json()) as { event_id: string; reason: string };
    expect(body.reason).toBe('The interpreter did not arrive.');
    expect(body.event_id).toMatch(UUID);
    expect(seen[0]!.headers.get('Idempotency-Key')).toMatch(UUID);
    expect(seen[0]!.headers.get('X-Requested-With')).toBe('DTHCMS');
  });

  it('answers the override refusal as an error rather than as a gate', async () => {
    // Swallowing this would leave a screen drawing a checkpoint the server never granted.
    server({
      [`POST /v1/counseling/visits/${VISIT}/gate/override`]: () =>
        respond(
          {
            error: {
              code: 'VALIDATION_FAILED',
              kind: 'validation',
              message: 'Validation failed.',
              message_bn: 'যাচাই ব্যর্থ।',
              fields: { reason: 'Nothing is outstanding on this visit.' },
              correlation_id: 'req_gate_1',
            },
          },
          422,
        ),
    });
    await expect(realGate.overrideCounselingGate(VISIT, 'x')).rejects.toBeInstanceOf(ApiError);
  });

  it('asks for the override rates over whole days, dropping an end it was not given', async () => {
    // The window is the reviewer's question — "how often did this happen last week" — and a
    // half-open day range is what the server takes.
    const seen = server({
      'GET /v1/counseling/gate/overrides': () =>
        respond({ from: '2026-09-07', to: '2026-09-14', overrides: [OVERRIDE] }),
    });
    const result = await realGate.listCounselingGateOverrides({ from: '2026-09-07' });

    const url = new URL(seen[0]!.url);
    expect(url.searchParams.get('from')).toBe('2026-09-07');
    expect(url.searchParams.has('to')).toBe(false);
    expect(result.overrides[0]?.reason).toContain('insulin corner');
  });

  it('names the conflict that means somebody already let this patient through', async () => {
    // Named rather than inferred from the 409, which the counselling endpoints answer for
    // three different facts.
    server({
      [`POST /v1/counseling/visits/${VISIT}/gate/override`]: () =>
        respond(
          {
            error: {
              code: 'COUNSELING_GATE_ALREADY_OVERRIDDEN',
              kind: 'conflict',
              message: 'An override already stands on this visit.',
              message_bn: 'এই ভিজিটে ইতিমধ্যে ছাড় দেওয়া আছে।',
              correlation_id: 'req_gate_2',
            },
          },
          409,
        ),
    });
    const failure = await realGate
      .overrideCounselingGate(VISIT, 'The interpreter did not arrive.')
      .catch((error: unknown) => error);

    expect(surface.overrideAlreadyStands(failure)).toBe(true);
    expect(surface.OVERRIDE_CONFLICT_CODE).toBe('COUNSELING_GATE_ALREADY_OVERRIDDEN');
  });

  it('does not mistake another conflict for one', () => {
    expect(
      surface.overrideAlreadyStands(
        new ApiError({
          status: 409,
          code: 'IDEMPOTENCY_KEY_REUSED',
          kind: 'conflict',
          messageEN: 'Reused.',
          messageBN: 'পুনর্ব্যবহৃত।',
          correlationID: 'req_gate_3',
        }),
      ),
    ).toBe(false);
    expect(surface.overrideAlreadyStands(new Error('offline'))).toBe(false);
  });

  it('reads one session by its id, with its items and its ticks', async () => {
    server({
      [`GET /v1/counseling/sessions/${DIABETES_SESSION}`]: () => respond({ session: DETAIL }),
    });
    const one = await realGate.getCounselingSession(DIABETES_SESSION);
    expect(one.items).toHaveLength(5);
    expect(one.ticks).toHaveLength(3);
  });

  it('reads every session on a visit, oldest first as the server ordered them', async () => {
    server({
      [`GET /v1/counseling/visits/${VISIT}/sessions`]: () =>
        respond({ sessions: [indexRow(), indexRow({ id: THYROID_SESSION })] }),
    });
    const rows = await realGate.listCounselingSessionsForVisit(VISIT);
    expect(rows.map((row) => row.id)).toEqual([DIABETES_SESSION, THYROID_SESSION]);
  });
});
