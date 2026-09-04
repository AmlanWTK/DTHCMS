import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '@/lib/api';
import type { FamilyRelation, HistoryItem, HistoryKind } from '@/features/history/api/history';
import type { Concept } from '@/features/terminology/api/terminology';

import { renderWithProviders } from './render';

/**
 * Station 4's screen (CP53, §4.7).
 *
 * What the server proves is what a history *is*: which kinds exist, which fields each one
 * needs, that a coding is three fields or none, that a removal takes a reason. What can only
 * be proven here is whether the person taking the history ends up making the assertions they
 * think they are making — and every way that fails is quiet.
 *
 *  - **A carried-forward list that confirms itself.** This is acceptance criterion 3 and it
 *    has its own named test below. One click producing twenty assertions would put a signed
 *    claim in the record that the patient is still on a drug she stopped in March, with a
 *    clinician's name on it and no clinician behind it.
 *  - **An unconfirmed item that looks confirmed.** The distinction has to survive a
 *    photograph, a screen by a window, and the roughly one man in twelve who cannot use hue,
 *    so every assertion here is made against text.
 *  - **A coding recorded without its version.** `E11.9` is a different disease in ICD-11.
 *    The form has to put the system, the version and the code on the request together.
 *  - **An uncoded item drawn as a coded one.** "Sugar since the flood" is a real item and
 *    not a diagnosis, and a screen that hid the difference would let it be read as one.
 *  - **Resolve and remove collapsed into one button.** "She no longer has this" and "this
 *    was never true" are different clinical claims, and only one of them takes a reason.
 *  - **A refusal shown as a banner when the server named the field.** "Say how many days"
 *    beside the box beats "something went wrong" above the form.
 */

const listHistoryKinds = vi.hoisted(() => vi.fn());
const listMedicalHistory = vi.hoisted(() => vi.fn());
const recordHistoryItem = vi.hoisted(() => vi.fn());
const confirmHistoryItem = vi.hoisted(() => vi.fn());
const amendHistoryItem = vi.hoisted(() => vi.fn());
const removeHistoryItem = vi.hoisted(() => vi.fn());

// Partial: the network calls are stubbed, the pure rules are not. `groupByKind`,
// `missingFields`, `recordRequestFrom` and `itemCoding` are what the screen actually does,
// and a test that stubbed them would prove the components call a function rather than that
// the right three fields reach the server.
vi.mock('@/features/history/api/history', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/history/api/history')>()),
  listHistoryKinds,
  listMedicalHistory,
  recordHistoryItem,
  confirmHistoryItem,
  amendHistoryItem,
  removeHistoryItem,
}));

const listFavourites = vi.hoisted(() => vi.fn());
const searchConcepts = vi.hoisted(() => vi.fn());

vi.mock('@/features/terminology/api/terminology', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/terminology/api/terminology')>()),
  listFavourites,
  searchConcepts,
}));

const { MedicalHistory } = await import('@/features/history/components/MedicalHistory');
const { AddHistoryItem } = await import('@/features/history/components/AddHistoryItem');
// The barrel, imported as a caller would. An export left off the index is a boundary
// violation nobody sees until the next screen needs it.
const surface = await import('@/features/history');
const { useSessionStore } = await import('@/stores/session');

const PATIENT = '0190a8f2-0000-7000-8000-0000000000a1';

function kind(over: Partial<HistoryKind> = {}): HistoryKind {
  return {
    kind: 'COMPLAINT',
    display_en: 'Presenting complaint',
    display_bn: 'বর্তমান সমস্যা',
    code_system: 'DTHC',
    requires_relation: false,
    requires_duration: true,
    allows_severity: true,
    allows_onset: true,
    is_medication: false,
    ordering: 1,
    ...over,
  };
}

const COMPLAINT = kind();

const COMORBIDITY = kind({
  kind: 'COMORBIDITY',
  display_en: 'Other condition',
  display_bn: 'অন্যান্য রোগ',
  code_system: 'ICD10',
  requires_duration: false,
  allows_severity: false,
  ordering: 2,
});

const FAMILY = kind({
  kind: 'FAMILY_HISTORY',
  display_en: 'Family history',
  display_bn: 'পারিবারিক ইতিহাস',
  code_system: 'ICD10',
  requires_relation: true,
  requires_duration: false,
  allows_severity: false,
  allows_onset: false,
  ordering: 3,
});

const SURGICAL = kind({
  kind: 'SURGICAL_HISTORY',
  display_en: 'Operation',
  display_bn: 'অস্ত্রোপচার',
  requires_duration: false,
  allows_severity: false,
  ordering: 4,
});

const MEDICATION = kind({
  kind: 'MEDICATION',
  display_en: 'Current medicine',
  display_bn: 'চলতি ওষুধ',
  requires_duration: false,
  allows_severity: false,
  allows_onset: false,
  is_medication: true,
  ordering: 5,
});

const VACCINATION = kind({
  kind: 'VACCINATION',
  display_en: 'Vaccination',
  display_bn: 'টিকা',
  requires_duration: false,
  allows_severity: false,
  allows_onset: true,
  ordering: 6,
});

const KINDS = [MEDICATION, COMPLAINT, VACCINATION, COMORBIDITY, SURGICAL, FAMILY];

const RELATIONS: FamilyRelation[] = [
  { relation: 'MOTHER', display_en: 'Mother', display_bn: 'মা', degree: 1, ordering: 1 },
  { relation: 'FATHER', display_en: 'Father', display_bn: 'বাবা', degree: 1, ordering: 2 },
  {
    relation: 'SIBLING',
    display_en: 'Brother or sister',
    display_bn: 'ভাই বা বোন',
    degree: 1,
    ordering: 3,
  },
];

const REFERENCE = {
  kinds: KINDS,
  relations: RELATIONS,
  from_lifestyle_station: ['SMOKING_STATUS', 'ALCOHOL_USE'],
};

function item(over: Partial<HistoryItem> = {}): HistoryItem {
  return {
    id: 'item-1',
    patient_id: PATIENT,
    kind: 'COMPLAINT',
    status: 'ACTIVE',
    recorded_at: '2026-08-02T04:00:00Z',
    recorded_by: '0190a8f2-0000-7000-8000-0000000000c1',
    ...over,
  };
}

/** Three carried forward from last month, none of them confirmed by anybody since. */
const CARRIED: HistoryItem[] = [
  item({
    id: 'item-complaint',
    kind: 'COMPLAINT',
    code_system: 'DTHC',
    code_version: '1',
    code: 'DTHC-CHEST-BURN',
    display_en: 'Burning chest pain',
    display_bn: 'বুক জ্বালা',
    said: 'Burning after the evening meal',
    duration_days: 21,
    severity: 'moderate',
  }),
  item({
    id: 'item-comorbidity',
    kind: 'COMORBIDITY',
    code_system: 'ICD10',
    code_version: '2019',
    code: 'I10',
    display_en: 'Essential hypertension',
    display_bn: 'প্রাথমিক উচ্চ রক্তচাপ',
    onset_on: '2024-03-14',
    onset_precision: 'year',
  }),
  item({
    id: 'item-medication',
    kind: 'MEDICATION',
    code_system: 'DTHC',
    code_version: '1',
    code: 'DTHC-METFORMIN',
    display_en: 'Metformin',
    display_bn: 'মেটফরমিন',
    dose: '1 tablet',
    frequency: 'twice a day after food',
    reconciliation: 'NOT_STOCKED',
  }),
];

function concept(over: Partial<Concept> = {}): Concept {
  return {
    system: 'DTHC',
    version: '1',
    code: 'DTHC-CHEST-BURN',
    display_en: 'Burning chest pain',
    display_bn: 'বুক জ্বালা',
    ...over,
  };
}

function refusal(fields: Record<string, string>): ApiError {
  return new ApiError({
    status: 422,
    code: 'validation_failed',
    kind: 'validation',
    messageEN: 'The request could not be processed.',
    messageBN: 'অনুরোধটি প্রক্রিয়া করা যায়নি।',
    fields,
    fieldsBN: {},
    correlationID: 'req_history_1',
  });
}

const initialSession = useSessionStore.getInitialState();

/** Somebody who may read, write and confirm — a history officer at station 4. */
function holding(permissions: string[]) {
  useSessionStore.setState({
    ...initialSession,
    status: 'authenticated',
    user: {
      id: '0190a8f2-0000-7000-8000-0000000000c1',
      employeeCode: 'E004',
      nameEN: 'History officer',
      nameBN: 'ইতিহাস কর্মকর্তা',
      facilityId: '11111111-1111-4111-8111-111111111111',
      roles: ['HISTORY'],
      grants: { HISTORY: permissions },
      permissions,
      secondFactor: { required: false, enrolled: false, pending: false, recoveryCodesLeft: 0 },
    },
    activeRole: 'HISTORY',
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  holding(['history.read', 'history.write', 'history.confirm']);
  listHistoryKinds.mockResolvedValue(REFERENCE);
  listMedicalHistory.mockResolvedValue(CARRIED);
  confirmHistoryItem.mockResolvedValue({ ...CARRIED[0], confirmed_at: '2026-09-04T05:00:00Z' });
  amendHistoryItem.mockResolvedValue({ ...CARRIED[0], status: 'RESOLVED' });
  removeHistoryItem.mockResolvedValue(undefined);
  recordHistoryItem.mockResolvedValue(item({ id: 'new-item' }));
  listFavourites.mockResolvedValue({ system: 'DTHC', version: '1', concepts: [concept()] });
  searchConcepts.mockResolvedValue({ system: 'DTHC', version: '1', concepts: [concept()] });
});

afterEach(() => {
  useSessionStore.setState(initialSession, true);
  vi.restoreAllMocks();
});

async function openPanel(locale: 'en' | 'bn' = 'en') {
  renderWithProviders(<MedicalHistory patientId={PATIENT} />, { locale });
  await screen.findByTestId('medical-history');
}

describe('a carried-forward history is a list of questions', () => {
  it('says in words how many nobody has confirmed', async () => {
    await openPanel();

    // A count before any row is read. Not a tint on three cards nobody counted.
    expect(await screen.findByText('3 items nobody has confirmed')).toBeInTheDocument();
  });

  it('flags every unconfirmed item with a word, not only a colour', async () => {
    await openPanel();

    const rows = await screen.findAllByTestId(/^history-item-/);
    expect(rows).toHaveLength(3);

    for (const row of rows) {
      // The word, the sentence and the attribute — three signals for one fact, because the
      // one that reaches the reader is whichever survives their screen.
      expect(within(row).getByText('Not confirmed')).toBeInTheDocument();
      expect(
        within(row).getByText('Nobody has confirmed this since it was recorded.'),
      ).toBeInTheDocument();
      expect(row).toHaveAttribute('data-confirmed', 'false');
    }
  });

  it('says when a confirmed item was confirmed, and asks nothing of it', async () => {
    listMedicalHistory.mockResolvedValue([
      item({ id: 'item-done', said: 'chest burning', confirmed_at: '2026-09-01T06:30:00Z' }),
    ]);

    await openPanel();

    const row = await screen.findByTestId('history-item-item-done');
    expect(within(row).getByText(/^Confirmed /)).toBeInTheDocument();
    expect(within(row).queryByText('Is this still true?')).not.toBeInTheDocument();
  });

  it('confirming one item sends exactly one request, for that item', async () => {
    const user = userEvent.setup();
    await openPanel();

    const row = await screen.findByTestId('history-item-item-comorbidity');
    await user.click(
      within(row).getByRole('button', {
        name: /Confirm that Essential hypertension is still true/,
      }),
    );

    await waitFor(() => expect(confirmHistoryItem).toHaveBeenCalledTimes(1));
    expect(confirmHistoryItem).toHaveBeenCalledWith('item-comorbidity', undefined);
  });

  it('there is no way to confirm everything at once', async () => {
    /*
     * The named test acceptance criterion 3 rests on, on the screen rather than in the
     * client.
     *
     * "Prior history is presented for confirmation, never auto-accepted" is satisfied by a
     * flag, a column default or a confirm-all button in a way that makes the assertion on
     * somebody's behalf — and a confirm-all is the one that would arrive here, because it
     * is the obvious kindness to an officer with twenty rows in front of them. It would put
     * twenty claims in a signed clinical record with one person's name and nobody behind
     * them.
     *
     * So: one control per unconfirmed item, each named for its own item, and nothing on the
     * screen that speaks about the list.
     */
    const user = userEvent.setup();
    await openPanel();

    await screen.findAllByTestId(/^history-item-/);

    const confirms = screen.getAllByRole('button', { name: /is still true$/ });
    expect(confirms).toHaveLength(3);

    // Each names its own item, so no control is offered as speaking for more than one.
    const names = confirms.map((button) => button.getAttribute('aria-label'));
    expect(new Set(names).size).toBe(3);

    // Nothing on the screen offers to answer for the whole list.
    for (const button of screen.getAllByRole('button')) {
      expect(button.textContent ?? '').not.toMatch(/\ball\b/i);
      expect(button.getAttribute('aria-label') ?? '').not.toMatch(/\ball\b/i);
    }

    // And pressing one answers for one. Three unconfirmed items, one click, one assertion.
    await user.click(confirms[0]!);
    await waitFor(() => expect(confirmHistoryItem).toHaveBeenCalledTimes(1));

    // The feature exports nothing that could be wired to such a button later.
    expect(
      Object.keys(surface).filter((name) => /(all|batch|bulk|many|each|every)/i.test(name)),
    ).toEqual([]);
  });

  it('says so when a confirmation fails, and does not pretend the item is answered', async () => {
    const user = userEvent.setup();
    confirmHistoryItem.mockRejectedValue(new Error('nope'));
    await openPanel();

    const row = await screen.findByTestId('history-item-item-comorbidity');
    await user.click(within(row).getByRole('button', { name: /is still true$/ }));

    expect(await within(row).findByText('This item was not confirmed.')).toBeInTheDocument();
    expect(within(row).getByText('Not confirmed')).toBeInTheDocument();
  });

  it('offers no confirmation to somebody who may only read', async () => {
    holding(['history.read']);
    await openPanel();

    await screen.findAllByTestId(/^history-item-/);
    expect(screen.queryByRole('button', { name: /is still true$/ })).not.toBeInTheDocument();
    // Confirming is a separate permission from writing, so the write controls are gone too.
    expect(screen.queryByRole('button', { name: 'Add an item' })).not.toBeInTheDocument();
  });

  it('offers confirmation to somebody who may confirm but not amend', async () => {
    // The clinical assistant at a follow-up: the patient reached station 5 without seeing
    // the history officer, and an unconfirmed list is worse than a confirmed one.
    holding(['history.read', 'history.confirm']);
    await openPanel();

    await screen.findAllByTestId(/^history-item-/);
    expect(screen.getAllByRole('button', { name: /is still true$/ })).toHaveLength(3);
    expect(screen.queryByRole('button', { name: 'Correct a detail' })).not.toBeInTheDocument();
  });
});

describe('what the panel shows about an item', () => {
  it('shows the coding whole — code, system and version', async () => {
    await openPanel();

    const row = await screen.findByTestId('history-item-item-comorbidity');
    const chip = within(row).getByTestId('concept-chip');
    // A code with no version is a string. All three, always, or the record cannot be read
    // in five years.
    expect(within(chip).getByText('I10')).toBeInTheDocument();
    expect(within(chip).getByText('ICD10')).toBeInTheDocument();
    expect(within(chip).getByText('2019')).toBeInTheDocument();
  });

  it('marks an uncoded item as uncoded and shows what she said', async () => {
    listMedicalHistory.mockResolvedValue([
      item({ id: 'item-uncoded', said: 'sugar since the flood' }),
    ]);

    await openPanel();

    const row = await screen.findByTestId('history-item-item-uncoded');
    expect(row).toHaveAttribute('data-coded', 'false');
    expect(within(row).getByTestId('uncoded-flag')).toHaveTextContent('No code');
    expect(within(row).getByText(/sugar since the flood/)).toBeInTheDocument();
    // Nothing pretends there is a coding behind it.
    expect(within(row).queryByTestId('concept-chip')).not.toBeInTheDocument();
  });

  it('shows the detail each kind carries and nothing it does not', async () => {
    await openPanel();

    const complaint = await screen.findByTestId('history-item-item-complaint');
    expect(within(complaint).getByText('21 days')).toBeInTheDocument();
    expect(within(complaint).getByText('Moderate')).toBeInTheDocument();

    const medicine = screen.getByTestId('history-item-item-medication');
    expect(within(medicine).getByText('1 tablet')).toBeInTheDocument();
    expect(within(medicine).getByText('twice a day after food')).toBeInTheDocument();
    // A finding, not a failure: somebody looked, and this clinic does not carry it.
    expect(within(medicine).getByText('This clinic does not stock it')).toBeInTheDocument();
    // A medicine has no severity and no duration to show.
    expect(within(medicine).queryByText('Severity')).not.toBeInTheDocument();
  });

  it('renders an onset no more exactly than it was given', async () => {
    await openPanel();

    // `onset_precision: 'year'` on 2024-03-14. Printing the day would turn "about two years
    // ago" into a measurement nobody made.
    const row = await screen.findByTestId('history-item-item-comorbidity');
    const onset = within(row).getByText('2024');
    expect(onset).toBeInTheDocument();
    expect(within(row).queryByText(/March/)).not.toBeInTheDocument();
  });

  it('says who recorded it and when', async () => {
    await openPanel();

    const row = await screen.findByTestId('history-item-item-complaint');
    // Criterion 4: every item is individually attributed, and the screen shows it per item
    // rather than once for the list.
    expect(within(row).getByText(/^Recorded .* by 0190a8f2/)).toBeInTheDocument();
  });

  it('keeps a resolved item on the list, marked', async () => {
    listMedicalHistory.mockResolvedValue([
      item({ id: 'item-gone', said: 'ankle swelling', status: 'RESOLVED' }),
    ]);

    await openPanel();

    const row = await screen.findByTestId('history-item-item-gone');
    expect(within(row).getByText('Resolved')).toBeInTheDocument();
    // A list that hid it would make every follow-up look like a first visit.
    expect(within(row).getByText(/ankle swelling/)).toBeInTheDocument();
  });

  it('gives every kind a heading, including the ones with nothing under them', async () => {
    await openPanel();

    // "Not asked yet" and "nothing found" are the same blank space on a screen that hides
    // its empty groups.
    expect(await screen.findByRole('region', { name: 'Family history' })).toBeInTheDocument();
    expect(screen.getAllByText('Nothing recorded under this heading.')).toHaveLength(3);
  });

  it('says smoking and alcohol belong to another station', async () => {
    await openPanel();

    const note = await screen.findByTestId('lifestyle-note');
    expect(within(note).getByText('Smoking and alcohol are not asked here')).toBeInTheDocument();
    expect(within(note).getByText('SMOKING_STATUS, ALCOHOL_USE')).toBeInTheDocument();
  });

  it('says nothing has been recorded rather than showing a blank screen', async () => {
    listMedicalHistory.mockResolvedValue([]);
    await openPanel();

    expect(
      await screen.findByText('Nothing has been recorded for this patient'),
    ).toBeInTheDocument();
  });

  it('does not draw an unreadable history as an empty one', async () => {
    // The two look identical and mean opposite things, and one of them reads as "this
    // patient takes nothing".
    listMedicalHistory.mockRejectedValue(new Error('down'));

    renderWithProviders(<MedicalHistory patientId={PATIENT} />);

    expect(
      await screen.findByText('This patient’s history could not be read.'),
    ).toBeInTheDocument();
  });
});

describe('resolving and removing are different acts', () => {
  it('resolving keeps the item and never removes it', async () => {
    const user = userEvent.setup();
    await openPanel();

    const row = await screen.findByTestId('history-item-item-complaint');
    await user.click(
      within(row).getByRole('button', { name: /Record that Burning chest pain has resolved/ }),
    );

    // The wording asks the clinical question, not "are you sure".
    expect(within(row).getByText('Has Burning chest pain resolved?')).toBeInTheDocument();
    await user.click(within(row).getByRole('button', { name: 'Yes, it has resolved' }));

    await waitFor(() => expect(amendHistoryItem).toHaveBeenCalledTimes(1));
    expect(amendHistoryItem).toHaveBeenCalledWith('item-complaint', { status: 'RESOLVED' });
    expect(removeHistoryItem).not.toHaveBeenCalled();
  });

  it('removing takes a reason and is refused without one', async () => {
    const user = userEvent.setup();
    await openPanel();

    const row = await screen.findByTestId('history-item-item-complaint');
    await user.click(
      within(row).getByRole('button', { name: /Remove Burning chest pain as wrongly recorded/ }),
    );

    const submit = within(row).getByRole('button', { name: 'Remove this item' });
    // Disabled before the reason exists, rather than submitting and then reporting what the
    // form already knew.
    expect(submit).toBeDisabled();

    await user.type(
      within(row).getByLabelText(/Why should this not have been recorded/),
      'Recorded on the wrong patient.',
    );
    expect(submit).toBeEnabled();
    await user.click(submit);

    await waitFor(() => expect(removeHistoryItem).toHaveBeenCalledTimes(1));
    expect(removeHistoryItem).toHaveBeenCalledWith(
      'item-complaint',
      'Recorded on the wrong patient.',
    );
    expect(amendHistoryItem).not.toHaveBeenCalled();
  });

  it('offers the two as two controls, worded so neither can be mistaken for the other', async () => {
    await openPanel();

    const row = await screen.findByTestId('history-item-item-complaint');
    expect(within(row).getByText('She no longer has this')).toBeInTheDocument();
    expect(within(row).getByText('This should not have been recorded')).toBeInTheDocument();
  });

  it('does not offer to resolve something already resolved', async () => {
    listMedicalHistory.mockResolvedValue([
      item({ id: 'item-gone', said: 'ankle swelling', status: 'RESOLVED' }),
    ]);
    await openPanel();

    const row = await screen.findByTestId('history-item-item-gone');
    expect(within(row).queryByText('She no longer has this')).not.toBeInTheDocument();
    // Removing stays available: a resolved item can still have been the wrong patient's.
    expect(within(row).getByText('This should not have been recorded')).toBeInTheDocument();
  });

  it('says so when a removal fails', async () => {
    const user = userEvent.setup();
    removeHistoryItem.mockRejectedValue(new Error('nope'));
    await openPanel();

    const row = await screen.findByTestId('history-item-item-complaint');
    await user.click(within(row).getByRole('button', { name: /as wrongly recorded/ }));
    await user.type(within(row).getByLabelText(/Why should this not/), 'wrong file');
    await user.click(within(row).getByRole('button', { name: 'Remove this item' }));

    expect(await within(row).findByText('This item was not removed.')).toBeInTheDocument();
  });
});

describe('correcting a detail', () => {
  it('sends only what changed', async () => {
    const user = userEvent.setup();
    await openPanel();

    const row = await screen.findByTestId('history-item-item-medication');
    await user.click(within(row).getByRole('button', { name: 'Correct a detail' }));

    const dose = within(row).getByLabelText('Dose');
    await user.clear(dose);
    await user.type(dose, '2 tablets');
    await user.click(within(row).getByRole('button', { name: 'Save the correction' }));

    await waitFor(() => expect(amendHistoryItem).toHaveBeenCalledTimes(1));
    // The frequency was not touched, so it is absent — which is "unchanged" to the server,
    // and different from the empty string that clears it.
    expect(amendHistoryItem).toHaveBeenCalledWith('item-medication', { dose: '2 tablets' });
  });

  it('clears a field with an empty string rather than leaving it alone', async () => {
    const user = userEvent.setup();
    await openPanel();

    const row = await screen.findByTestId('history-item-item-medication');
    await user.click(within(row).getByRole('button', { name: 'Correct a detail' }));
    await user.clear(within(row).getByLabelText('How often?'));
    await user.click(within(row).getByRole('button', { name: 'Save the correction' }));

    await waitFor(() => expect(amendHistoryItem).toHaveBeenCalledTimes(1));
    // She has stopped taking it on a schedule. Absent would mean "unchanged", which is a
    // different request, and a form that could only make one of the two could not say this.
    expect(amendHistoryItem).toHaveBeenCalledWith('item-medication', { frequency: '' });
  });

  it('says that correcting also confirms', async () => {
    const user = userEvent.setup();
    await openPanel();

    const row = await screen.findByTestId('history-item-item-complaint');
    await user.click(within(row).getByRole('button', { name: 'Correct a detail' }));

    expect(within(row).getByText(/Correcting an item also confirms it/)).toBeInTheDocument();
  });

  it('renders a refusal against the field the server named', async () => {
    const user = userEvent.setup();
    amendHistoryItem.mockRejectedValue(refusal({ duration_days: 'A complaint needs a duration.' }));
    await openPanel();

    const row = await screen.findByTestId('history-item-item-complaint');
    await user.click(within(row).getByRole('button', { name: 'Correct a detail' }));

    const duration = within(row).getByLabelText(/How many days\?/);
    await user.clear(duration);
    await user.type(duration, '30');
    await user.click(within(row).getByRole('button', { name: 'Save the correction' }));

    await waitFor(() =>
      expect(duration).toHaveAccessibleDescription(/A complaint needs a duration/),
    );
    expect(duration).toHaveAttribute('aria-invalid', 'true');
  });
});

describe('the form asks what the kind asks for', () => {
  function renderForm(locale: 'en' | 'bn' = 'en') {
    renderWithProviders(
      <AddHistoryItem patientId={PATIENT} kinds={KINDS} relations={RELATIONS} />,
      { locale },
    );
  }

  it('opens on the kind the station asks first, in the server’s order', () => {
    renderForm();

    const kindField = screen.getByLabelText('What kind of item is this?') as HTMLSelectElement;
    expect(kindField.value).toBe('COMPLAINT');
    expect([...kindField.options].map((option) => option.value)).toEqual([
      'COMPLAINT',
      'COMORBIDITY',
      'FAMILY_HISTORY',
      'SURGICAL_HISTORY',
      'MEDICATION',
      'VACCINATION',
    ]);
  });

  it('asks a complaint for a duration', () => {
    renderForm();
    expect(screen.getByLabelText(/How many days\?/)).toBeInTheDocument();
    expect(screen.queryByLabelText(/Which relative\?/)).not.toBeInTheDocument();
  });

  it('asks a family history for a relation', async () => {
    const user = userEvent.setup();
    renderForm();

    await user.selectOptions(screen.getByLabelText('What kind of item is this?'), 'FAMILY_HISTORY');

    const relative = screen.getByLabelText(/Which relative\?/);
    expect(relative).toBeInTheDocument();
    expect([...(relative as HTMLSelectElement).options].map((option) => option.value)).toContain(
      'MOTHER',
    );
    // And nothing else: a relation is what this kind needs, a duration is not.
    expect(screen.queryByLabelText(/How many days\?/)).not.toBeInTheDocument();
  });

  it('asks a vaccination for neither a dose nor a severity', async () => {
    const user = userEvent.setup();
    renderForm();

    await user.selectOptions(screen.getByLabelText('What kind of item is this?'), 'VACCINATION');

    expect(screen.queryByLabelText('Dose')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('How bad is it?')).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/How many days\?/)).not.toBeInTheDocument();
    // It does allow an onset, and the precision travels with it.
    expect(screen.getByLabelText('When did it start?')).toBeInTheDocument();
    expect(screen.getByLabelText('How sure is that date?')).toBeInTheDocument();
  });

  it('asks a medicine for a dose and how often', async () => {
    const user = userEvent.setup();
    renderForm();

    await user.selectOptions(screen.getByLabelText('What kind of item is this?'), 'MEDICATION');

    expect(screen.getByLabelText('Dose')).toBeInTheDocument();
    expect(screen.getByLabelText('How often?')).toBeInTheDocument();
  });

  it('codes each kind from the catalogue the server named', async () => {
    const user = userEvent.setup();
    renderForm();

    // A complaint is coded from the clinic's own dictionary…
    await user.click(screen.getByLabelText(/Code for this Presenting complaint/));
    await waitFor(() =>
      expect(listFavourites).toHaveBeenCalledWith({ system: 'DTHC', version: undefined }),
    );

    // …and a comorbidity from ICD. A picker that chose for itself could make the record
    // assert that a patient presented with type 2 diabetes.
    await user.selectOptions(screen.getByLabelText('What kind of item is this?'), 'COMORBIDITY');
    await user.click(screen.getByLabelText(/Code for this Other condition/));
    await waitFor(() =>
      expect(listFavourites).toHaveBeenCalledWith({ system: 'ICD10', version: undefined }),
    );
  });

  it('forgets a concept picked under another kind', async () => {
    const user = userEvent.setup();
    renderForm();

    await user.click(screen.getByLabelText(/Code for this Presenting complaint/));
    await user.click(await screen.findByRole('option', { name: /Burning chest pain/ }));
    expect(screen.getByTestId('concept-chip')).toBeInTheDocument();

    // The DTHC coding is not a legal coding for an ICD-coded kind, and carrying it across
    // would send the server a request it is right to refuse.
    await user.selectOptions(screen.getByLabelText('What kind of item is this?'), 'COMORBIDITY');
    expect(screen.queryByTestId('concept-chip')).not.toBeInTheDocument();
  });
});

describe('recording an item', () => {
  function renderForm() {
    renderWithProviders(<AddHistoryItem patientId={PATIENT} kinds={KINDS} relations={RELATIONS} />);
  }

  it('puts the system, the version and the code on the request together', async () => {
    const user = userEvent.setup();
    renderForm();

    await user.click(screen.getByLabelText(/Code for this Presenting complaint/));
    await user.click(await screen.findByRole('option', { name: /Burning chest pain/ }));
    await user.type(screen.getByLabelText(/How many days\?/), '21');
    await user.click(screen.getByRole('button', { name: 'Record this item' }));

    await waitFor(() => expect(recordHistoryItem).toHaveBeenCalledTimes(1));
    // Acceptance criterion 2 of CP52, met by the screen that uses the picker: all three,
    // read off the concept the server returned rather than off anything configured here.
    expect(recordHistoryItem).toHaveBeenCalledWith(PATIENT, {
      kind: 'COMPLAINT',
      code_system: 'DTHC',
      code_version: '1',
      code: 'DTHC-CHEST-BURN',
      duration_days: 21,
    });
  });

  it('records an uncoded item when the catalogue has nothing, keeping her words', async () => {
    const user = userEvent.setup();
    renderForm();

    await user.type(screen.getByLabelText(/What she said/), 'sugar since the flood');
    await user.type(screen.getByLabelText(/How many days\?/), '30');
    await user.click(screen.getByRole('button', { name: 'Record this item' }));

    await waitFor(() => expect(recordHistoryItem).toHaveBeenCalledTimes(1));
    const [, request] = recordHistoryItem.mock.calls[0] as [string, Record<string, unknown>];
    expect(request.said).toBe('sugar since the flood');
    expect(request).not.toHaveProperty('code');
    expect(request).not.toHaveProperty('code_version');
  });

  it('asks for her words before sending an item with no code and no description', async () => {
    const user = userEvent.setup();
    renderForm();

    await user.type(screen.getByLabelText(/How many days\?/), '30');
    await user.click(screen.getByRole('button', { name: 'Record this item' }));

    expect(screen.getByLabelText(/What she said/)).toHaveAccessibleDescription(
      /Say what she described/,
    );
    expect(recordHistoryItem).not.toHaveBeenCalled();
  });

  it('asks for the relative before sending a family history without one', async () => {
    const user = userEvent.setup();
    renderForm();

    await user.selectOptions(screen.getByLabelText('What kind of item is this?'), 'FAMILY_HISTORY');
    await user.type(screen.getByLabelText(/What she said/), 'mother had sugar');
    await user.click(screen.getByRole('button', { name: 'Record this item' }));

    expect(screen.getByLabelText(/Which relative\?/)).toHaveAccessibleDescription(
      /Say which relative/,
    );
    expect(recordHistoryItem).not.toHaveBeenCalled();
  });

  it('renders a 422 against the field the server named', async () => {
    const user = userEvent.setup();
    recordHistoryItem.mockRejectedValue(
      refusal({ duration_days: 'A complaint needs a duration.' }),
    );
    renderForm();

    await user.type(screen.getByLabelText(/What she said/), 'chest burning');
    await user.type(screen.getByLabelText(/How many days\?/), '21');
    await user.click(screen.getByRole('button', { name: 'Record this item' }));

    const duration = screen.getByLabelText(/How many days\?/);
    await waitFor(() =>
      expect(duration).toHaveAccessibleDescription(/A complaint needs a duration/),
    );
    // Not a banner above the form: the message belongs beside the box it is about.
    expect(duration).toHaveAttribute('aria-invalid', 'true');
  });

  it('shows a refusal the form has no field for rather than swallowing it', async () => {
    const user = userEvent.setup();
    recordHistoryItem.mockRejectedValue(refusal({ patient_id: 'That patient is merged.' }));
    renderForm();

    await user.type(screen.getByLabelText(/What she said/), 'chest burning');
    await user.type(screen.getByLabelText(/How many days\?/), '21');
    await user.click(screen.getByRole('button', { name: 'Record this item' }));

    expect(await screen.findByText('That patient is merged.')).toBeInTheDocument();
  });

  it('says the item was not recorded when the server said nothing useful', async () => {
    const user = userEvent.setup();
    recordHistoryItem.mockRejectedValue(new Error('offline'));
    renderForm();

    await user.type(screen.getByLabelText(/What she said/), 'chest burning');
    await user.type(screen.getByLabelText(/How many days\?/), '21');
    await user.click(screen.getByRole('button', { name: 'Record this item' }));

    expect(
      await screen.findByText('The item was not recorded. Nothing has changed.'),
    ).toBeInTheDocument();
  });

  it('says so rather than offering a form that can record nothing', () => {
    renderWithProviders(<AddHistoryItem patientId={PATIENT} kinds={[]} relations={[]} />);

    expect(
      screen.getByText(
        'The kinds of history could not be read, so nothing can be recorded here yet.',
      ),
    ).toBeInTheDocument();
  });

  it('opens from the panel and closes when the item is recorded', async () => {
    const user = userEvent.setup();
    await openPanel();

    await user.click(await screen.findByRole('button', { name: 'Add an item' }));
    await user.type(screen.getByLabelText(/What she said/), 'chest burning');
    await user.type(screen.getByLabelText(/How many days\?/), '21');
    await user.click(screen.getByRole('button', { name: 'Record this item' }));

    await waitFor(() =>
      expect(screen.queryByRole('button', { name: 'Record this item' })).not.toBeInTheDocument(),
    );
    expect(screen.getByRole('button', { name: 'Add an item' })).toBeInTheDocument();
  });

  it('can be abandoned without recording anything', async () => {
    const user = userEvent.setup();
    await openPanel();

    await user.click(await screen.findByRole('button', { name: 'Add an item' }));
    await user.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(recordHistoryItem).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'Add an item' })).toBeInTheDocument();
  });
});

describe('the detail a family history and an amendment carry', () => {
  it('names the relative in words rather than showing the code', async () => {
    listMedicalHistory.mockResolvedValue([
      item({
        id: 'item-family',
        kind: 'FAMILY_HISTORY',
        code_system: 'ICD10',
        code_version: '2019',
        code: 'E11.9',
        display_en: 'Type 2 diabetes mellitus',
        display_bn: 'টাইপ ২ ডায়াবেটিস',
        relation: 'MOTHER',
      }),
    ]);

    await openPanel();

    const row = await screen.findByTestId('history-item-item-family');
    // "MOTHER" on a screen is a database value somebody forgot to translate.
    expect(within(row).getByText('Mother')).toBeInTheDocument();
    expect(within(row).queryByText('MOTHER')).not.toBeInTheDocument();
  });

  it('shows the relative’s own code when the server sent a relation it did not describe', async () => {
    listMedicalHistory.mockResolvedValue([
      item({
        id: 'item-family',
        kind: 'FAMILY_HISTORY',
        said: 'aunt had thyroid',
        relation: 'AUNT_UNCLE',
      }),
    ]);

    await openPanel();

    // A visible defect somebody will report, rather than an empty cell that reads as
    // "no relative" — and a family history with no relative is not one.
    const row = await screen.findByTestId('history-item-item-family');
    expect(within(row).getByText('AUNT_UNCLE')).toBeInTheDocument();
  });

  it('says when an item was last corrected', async () => {
    listMedicalHistory.mockResolvedValue([
      item({ id: 'item-fixed', said: 'chest burning', amended_at: '2026-09-02T07:00:00Z' }),
    ]);

    await openPanel();

    const row = await screen.findByTestId('history-item-item-fixed');
    expect(within(row).getByText(/^Last corrected /)).toBeInTheDocument();
  });

  it('sends a changed severity, onset and description together', async () => {
    const user = userEvent.setup();
    await openPanel();

    const row = await screen.findByTestId('history-item-item-complaint');
    await user.click(within(row).getByRole('button', { name: 'Correct a detail' }));

    const said = within(row).getByLabelText('What she said');
    await user.clear(said);
    await user.type(said, 'Burning at night now');
    await user.selectOptions(within(row).getByLabelText('How bad is it?'), 'severe');
    await user.type(within(row).getByLabelText('When did it start?'), '2026-08-01');
    await user.selectOptions(within(row).getByLabelText('How sure is that date?'), 'month');
    await user.click(within(row).getByRole('button', { name: 'Save the correction' }));

    await waitFor(() => expect(amendHistoryItem).toHaveBeenCalledTimes(1));
    expect(amendHistoryItem).toHaveBeenCalledWith('item-complaint', {
      said: 'Burning at night now',
      severity: 'severe',
      // The precision travels with the date and never without it.
      onset_on: '2026-08-01',
      onset_precision: 'month',
    });
  });

  it('says a correction failed when the server named nothing', async () => {
    const user = userEvent.setup();
    amendHistoryItem.mockRejectedValue(new Error('offline'));
    await openPanel();

    const row = await screen.findByTestId('history-item-item-complaint');
    await user.click(within(row).getByRole('button', { name: 'Correct a detail' }));
    const said = within(row).getByLabelText('What she said');
    await user.clear(said);
    await user.type(said, 'something else');
    await user.click(within(row).getByRole('button', { name: 'Save the correction' }));

    expect(await within(row).findByText('The correction was not saved.')).toBeInTheDocument();
  });

  it('lets each panel be abandoned without changing anything', async () => {
    const user = userEvent.setup();
    await openPanel();

    const row = await screen.findByTestId('history-item-item-complaint');

    await user.click(within(row).getByRole('button', { name: 'Correct a detail' }));
    await user.click(within(row).getByRole('button', { name: 'Cancel' }));

    await user.click(within(row).getByRole('button', { name: /has resolved$/ }));
    await user.click(within(row).getByRole('button', { name: 'Cancel' }));

    await user.click(within(row).getByRole('button', { name: /as wrongly recorded$/ }));
    await user.click(within(row).getByRole('button', { name: 'Cancel' }));

    expect(amendHistoryItem).not.toHaveBeenCalled();
    expect(removeHistoryItem).not.toHaveBeenCalled();
    expect(within(row).getByText('She no longer has this')).toBeInTheDocument();
  });

  it('says when resolving failed, and the notice can be dismissed', async () => {
    const user = userEvent.setup();
    amendHistoryItem.mockRejectedValue(new Error('offline'));
    await openPanel();

    const row = await screen.findByTestId('history-item-item-complaint');
    await user.click(within(row).getByRole('button', { name: /has resolved$/ }));
    await user.click(within(row).getByRole('button', { name: 'Yes, it has resolved' }));

    expect(
      await within(row).findByText('This item was not marked as resolved.'),
    ).toBeInTheDocument();
    await user.click(within(row).getByRole('button', { name: 'Dismiss' }));
    expect(
      within(row).queryByText('This item was not marked as resolved.'),
    ).not.toBeInTheDocument();
  });
});

describe('the words an item is read in', () => {
  it('falls back one way only, and never to an empty control name', async () => {
    const { itemName, kindLabel, onsetText, relationLabel } =
      await import('@/features/history/components/historyText');

    // Bangla when there is Bangla and the interface is in Bangla; English otherwise. A
    // reader of Bangla is better served by an English word than by a blank space.
    expect(kindLabel(COMPLAINT, 'bn')).toBe('বর্তমান সমস্যা');
    expect(kindLabel(COMPLAINT, 'en')).toBe('Presenting complaint');
    expect(kindLabel(kind({ display_bn: '' }), 'bn')).toBe('Presenting complaint');
    expect(kindLabel(kind({ display_en: '', display_bn: '' }), 'en')).toBe('COMPLAINT');

    expect(relationLabel(RELATIONS, 'MOTHER', 'bn')).toBe('মা');
    expect(relationLabel(RELATIONS, 'MOTHER', 'en')).toBe('Mother');
    expect(relationLabel([{ ...RELATIONS[0]!, display_bn: '' }], 'MOTHER', 'bn')).toBe('Mother');
    expect(relationLabel(RELATIONS, 'AUNT_UNCLE', 'en')).toBe('AUNT_UNCLE');

    // The name a button is announced by. A card with no coding is named by what she said,
    // because six controls all called "Confirm" is the same as none.
    expect(itemName(item({ display_en: 'Metformin', display_bn: 'মেটফরমিন' }), 'bn')).toBe(
      'মেটফরমিন',
    );
    expect(itemName(item({ display_bn: 'মেটফরমিন' }), 'en')).toBe('মেটফরমিন');
    expect(itemName(item({ said: 'sugar since the flood' }), 'en')).toBe('sugar since the flood');
    expect(itemName(item({ code: 'E11.9' }), 'en')).toBe('E11.9');
    expect(itemName(item(), 'en')).toBe('');

    // No onset is no row, rather than "Started —"; an unparseable one is the same.
    expect(onsetText(item(), 'en')).toBeNull();
    expect(onsetText(item({ onset_on: 'not a date' }), 'en')).toBeNull();
    // A bare date with no precision is read as a day, which is the least surprising
    // reading of one.
    expect(onsetText(item({ onset_on: '2024-03-14' }), 'en')).toMatch(/2024/);
  });
});

describe('every field a kind offers reaches the request', () => {
  function renderForm() {
    renderWithProviders(<AddHistoryItem patientId={PATIENT} kinds={KINDS} relations={RELATIONS} />);
  }

  it('records a family history with the relative that makes it one', async () => {
    const user = userEvent.setup();
    renderForm();

    await user.selectOptions(screen.getByLabelText('What kind of item is this?'), 'FAMILY_HISTORY');
    await user.selectOptions(screen.getByLabelText(/Which relative\?/), 'MOTHER');
    await user.type(screen.getByLabelText(/What she said/), 'mother had sugar');
    await user.click(screen.getByRole('button', { name: 'Record this item' }));

    await waitFor(() => expect(recordHistoryItem).toHaveBeenCalledTimes(1));
    expect(recordHistoryItem).toHaveBeenCalledWith(PATIENT, {
      kind: 'FAMILY_HISTORY',
      said: 'mother had sugar',
      relation: 'MOTHER',
    });
  });

  it('records a complaint’s severity and an onset with its precision', async () => {
    const user = userEvent.setup();
    renderForm();

    await user.type(screen.getByLabelText(/What she said/), 'chest burning');
    await user.type(screen.getByLabelText(/How many days\?/), '21');
    await user.selectOptions(screen.getByLabelText('How bad is it?'), 'mild');
    await user.type(screen.getByLabelText('When did it start?'), '2024-03-14');
    await user.selectOptions(screen.getByLabelText('How sure is that date?'), 'year');
    await user.click(screen.getByRole('button', { name: 'Record this item' }));

    await waitFor(() => expect(recordHistoryItem).toHaveBeenCalledTimes(1));
    expect(recordHistoryItem).toHaveBeenCalledWith(PATIENT, {
      kind: 'COMPLAINT',
      said: 'chest burning',
      duration_days: 21,
      severity: 'mild',
      onset_on: '2024-03-14',
      onset_precision: 'year',
    });
  });

  it('records a medicine as she takes it', async () => {
    const user = userEvent.setup();
    renderForm();

    await user.selectOptions(screen.getByLabelText('What kind of item is this?'), 'MEDICATION');
    await user.type(screen.getByLabelText(/What she said/), 'the sugar tablet');
    await user.type(screen.getByLabelText('Dose'), '1 tablet');
    await user.type(screen.getByLabelText('How often?'), 'twice a day after food');
    await user.click(screen.getByRole('button', { name: 'Record this item' }));

    await waitFor(() => expect(recordHistoryItem).toHaveBeenCalledTimes(1));
    expect(recordHistoryItem).toHaveBeenCalledWith(PATIENT, {
      kind: 'MEDICATION',
      said: 'the sugar tablet',
      dose: '1 tablet',
      frequency: 'twice a day after food',
    });
  });

  it('lets a coding be taken off again, and then asks for her words instead', async () => {
    const user = userEvent.setup();
    renderForm();

    await user.click(screen.getByLabelText(/Code for this Presenting complaint/));
    await user.click(await screen.findByRole('option', { name: /Burning chest pain/ }));
    await user.click(screen.getByRole('button', { name: /Remove Burning chest pain/ }));

    expect(screen.queryByTestId('concept-chip')).not.toBeInTheDocument();
    // With no coding the description becomes the one the server enforces.
    expect(screen.getByLabelText(/What she said/)).toHaveAccessibleDescription(
      /Required, because nothing has been coded/,
    );
  });

  it('shows a refusal about the coding beside the picker', async () => {
    const user = userEvent.setup();
    recordHistoryItem.mockRejectedValue(
      refusal({ code: 'That code is not in the complaint dictionary.' }),
    );
    renderForm();

    await user.type(screen.getByLabelText(/What she said/), 'chest burning');
    await user.type(screen.getByLabelText(/How many days\?/), '21');
    await user.click(screen.getByRole('button', { name: 'Record this item' }));

    // A code the wrong catalogue would have filed as a presenting complaint. The picker has
    // no error slot of its own, so the sentence goes under it as an alert rather than into a
    // banner at the top of a form the operator has already scrolled past.
    expect(
      await screen.findByText('That code is not in the complaint dictionary.'),
    ).toBeInTheDocument();
  });
});

describe('the screen reads in both languages', () => {
  it('says the unconfirmed state in Bangla', async () => {
    await openPanel('bn');

    expect(await screen.findByText('৩ টি বিষয় এখনও কেউ নিশ্চিত করেননি')).toBeInTheDocument();
    const row = screen.getByTestId('history-item-item-complaint');
    expect(within(row).getByText('নিশ্চিত করা হয়নি')).toBeInTheDocument();
    expect(within(row).getByText('হ্যাঁ, এখনও সত্য')).toBeInTheDocument();
  });

  it('names the kinds, the details and the two acts in Bangla', async () => {
    await openPanel('bn');

    expect(await screen.findByRole('region', { name: 'পারিবারিক ইতিহাস' })).toBeInTheDocument();

    const complaint = screen.getByTestId('history-item-item-complaint');
    // A count in running text follows the language: ২১, not 21.
    expect(within(complaint).getByText('২১ দিন')).toBeInTheDocument();
    expect(within(complaint).getByText('মাঝারি')).toBeInTheDocument();
    expect(within(complaint).getByText('এটি আর নেই')).toBeInTheDocument();
    expect(within(complaint).getByText('এটি লেখাই উচিত ছিল না')).toBeInTheDocument();

    // A code is an identifier and stays in ASCII in both languages.
    const chip = within(screen.getByTestId('history-item-item-comorbidity')).getByTestId(
      'concept-chip',
    );
    expect(within(chip).getByText('I10')).toBeInTheDocument();
    expect(within(chip).getByText('2019')).toBeInTheDocument();
  });

  it('asks the form’s questions in Bangla, from the same rules', async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <AddHistoryItem patientId={PATIENT} kinds={KINDS} relations={RELATIONS} />,
      { locale: 'bn' },
    );

    expect(screen.getByLabelText(/কত দিন ধরে\?/)).toBeInTheDocument();

    await user.selectOptions(screen.getByLabelText('এটি কোন ধরনের বিষয়?'), 'FAMILY_HISTORY');
    const relative = screen.getByLabelText(/কোন আত্মীয়\?/);
    expect(relative).toBeInTheDocument();
    expect(
      [...(relative as HTMLSelectElement).options].map((option) => option.textContent),
    ).toContain('মা');
  });
});
