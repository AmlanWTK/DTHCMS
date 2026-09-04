import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type {
  Allergy,
  AllergyChange,
  AllergyReaction,
  AllergyState,
} from '@/features/allergies/api/allergies';
import type { Concept } from '@/features/terminology/api/terminology';
import { ApiError } from '@/lib/api';

import { messages, renderWithProviders } from './render';

/**
 * The allergy hard stop, on screen (CP54, §3 step 4, [R-01]).
 *
 * The gate itself is a trigger on the queue table, so nothing here is the enforcement.
 * What can only be proven here is whether the clinician reading a patient record ends up
 * believing the right thing — and every way that fails is quiet.
 *
 *  - **An empty list drawn as "no allergies".** `NONE_RECORDED` and `NO_KNOWN_ALLERGY`
 *    both arrive with nothing in the list and mean opposite things. This is acceptance
 *    criterion 3's failure mode and it has its own named test below: a header that drew
 *    them the same way would be lying in the direction where somebody writes the
 *    penicillin.
 *  - **An override growing on the screen.** The three answers are the whole surface; a
 *    fourth control would be the kindness that turns the gate into a habit. There is a
 *    named test whose entire job is to fail if one appears.
 *  - **"Unable to assess" read as reassurance.** Somebody looked at an unconscious patient
 *    and could not find out. It satisfies the gate and it is not an answer about the
 *    patient, so it is drawn as distinctly as an allergy and its reason is on screen.
 *  - **An emergency buried under a rash from 1998.** The server sorts worst first and
 *    nothing here re-sorts; the word "emergency" is on the row before any colour is.
 *  - **An uncoded allergy drawn as a coded one.** "The yellow tablet from the pharmacy near
 *    the bridge" is a real allergy and not a coding, and only one of those two facts is
 *    safe to drop.
 *  - **A withdrawal with no reason.** The disagreement is the interesting part.
 */

const getAllergyState = vi.hoisted(() => vi.fn());
const listAllergyReactions = vi.hoisted(() => vi.fn());
const listAllergyChanges = vi.hoisted(() => vi.fn());
const recordAllergy = vi.hoisted(() => vi.fn());
const assertAllergyStatus = vi.hoisted(() => vi.fn());
const withdrawAllergy = vi.hoisted(() => vi.fn());
const withdrawAllergyAssertion = vi.hoisted(() => vi.fn());

// Partial: the network calls are stubbed, the pure rules are not. `statusOf`,
// `isReassuring`, `isEmergency`, `allergyCoding`, `missingAllergyFields` and
// `recordRequestFrom` are what these screens actually do, and a test that stubbed them
// would prove the components call a function rather than that the right words reach the
// clinician.
vi.mock('@/features/allergies/api/allergies', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/allergies/api/allergies')>()),
  getAllergyState,
  listAllergyReactions,
  listAllergyChanges,
  recordAllergy,
  assertAllergyStatus,
  withdrawAllergy,
  withdrawAllergyAssertion,
}));

const getPatient = vi.hoisted(() => vi.fn());

vi.mock('@/features/patients/api/patients', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/patients/api/patients')>()),
  getPatient,
}));

const listFavourites = vi.hoisted(() => vi.fn());
const searchConcepts = vi.hoisted(() => vi.fn());

vi.mock('@/features/terminology/api/terminology', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/terminology/api/terminology')>()),
  listFavourites,
  searchConcepts,
}));

const { AllergyBanner } = await import('@/features/allergies/components/AllergyBanner');
const { AllergyPanel } = await import('@/features/allergies/components/AllergyPanel');
// The header the layout mounts on every patient screen — criterion 3 is only met if the
// strip is actually inside it.
const { PatientHeader } = await import('@/features/patients/components/PatientHeader');
// The barrel, imported as a caller would.
const surface = await import('@/features/allergies');
const { useSessionStore } = await import('@/stores/session');

const PATIENT = '0190a8f2-0000-7000-8000-0000000000a1';
const OFFICER = '0190a8f2-0000-7000-8000-0000000000c1';

function allergy(over: Partial<Allergy> = {}): Allergy {
  return {
    id: 'allergy-rash',
    patient_id: PATIENT,
    code_system: 'DTHC',
    code_version: '1.0',
    code: 'ALLERGEN_SULFA',
    display_en: 'Sulfa drugs',
    display_bn: 'সালফা ওষুধ',
    reaction: 'RASH',
    reaction_en: 'Rash',
    reaction_bn: 'ফুসকুড়ি',
    severity: 'mild',
    certainty: 'suspected',
    recorded_at: '2026-08-02T04:00:00Z',
    recorded_by: OFFICER,
    ...over,
  };
}

/** Worst first, as the server returns them: anaphylaxis above a rash from years ago. */
const ANAPHYLAXIS = allergy({
  id: 'allergy-penicillin',
  code: 'ALLERGEN_PENICILLIN',
  display_en: 'Penicillin',
  display_bn: 'পেনিসিলিন',
  reaction: 'ANAPHYLAXIS',
  reaction_en: 'Collapse or anaphylaxis',
  reaction_bn: 'অজ্ঞান হয়ে পড়া',
  is_emergency: true,
  severity: 'life_threatening',
  certainty: 'confirmed',
});

const RASH = allergy();

function state(over: Partial<AllergyState> = {}): AllergyState {
  return { status: 'NONE_RECORDED', satisfied: false, allergies: [], ...over };
}

const NOBODY_ASKED = state();

const ASKED_AND_NONE = state({
  status: 'NO_KNOWN_ALLERGY',
  satisfied: true,
  assertion: {
    id: 'assertion-1',
    patient_id: PATIENT,
    kind: 'NO_KNOWN_ALLERGY',
    asserted_at: '2026-09-01T05:00:00Z',
    asserted_by: OFFICER,
  },
});

const COULD_NOT_ASK = state({
  status: 'UNABLE_TO_ASSESS',
  satisfied: true,
  assertion: {
    id: 'assertion-2',
    patient_id: PATIENT,
    kind: 'UNABLE_TO_ASSESS',
    reason: 'Patient is drowsy and no attendant is present.',
    asserted_at: '2026-09-01T05:00:00Z',
    asserted_by: OFFICER,
  },
});

const RECORDED = state({
  status: 'ALLERGIES_RECORDED',
  satisfied: true,
  allergies: [ANAPHYLAXIS, RASH],
});

function reaction(over: Partial<AllergyReaction> = {}): AllergyReaction {
  return {
    reaction: 'RASH',
    display_en: 'Rash',
    display_bn: 'ফুসকুড়ি',
    is_emergency: false,
    ordering: 4,
    ...over,
  };
}

const REACTIONS: AllergyReaction[] = [
  reaction(),
  reaction({
    reaction: 'ANAPHYLAXIS',
    display_en: 'Collapse or anaphylaxis',
    display_bn: 'অজ্ঞান হয়ে পড়া',
    is_emergency: true,
    ordering: 1,
  }),
  reaction({ reaction: 'ITCHING', display_en: 'Itching', display_bn: 'চুলকানি', ordering: 5 }),
];

const CHANGES: AllergyChange[] = [
  {
    kind: 'ALLERGY',
    id: 'allergy-old',
    said: 'the red syrup',
    reaction: 'RASH',
    detail: 'suspected',
    at: '2026-07-01T04:00:00Z',
    by: OFFICER,
    undone_at: '2026-07-02T04:00:00Z',
    undone_by: OFFICER,
    undone_why: 'Recorded on the wrong patient.',
  },
  {
    kind: 'NO_KNOWN_ALLERGY',
    id: 'assertion-old',
    at: '2026-06-01T04:00:00Z',
    by: OFFICER,
  },
];

function concept(over: Partial<Concept> = {}): Concept {
  return {
    system: 'DTHC',
    version: '1.0',
    code: 'ALLERGEN_PENICILLIN',
    display_en: 'Penicillin',
    display_bn: 'পেনিসিলিন',
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
    correlationID: 'req_allergy_1',
  });
}

/** The Bangla messages themselves, so a locale test never holds a second copy of them. */
const BN = messages.bn.allergies;

/** A message used as a regular expression needs its punctuation taken literally. */
function escapeForQuery(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

const initialSession = useSessionStore.getInitialState();

/** A history officer at station 4: may read the allergies and may write them. */
function holding(permissions: string[]) {
  useSessionStore.setState({
    ...initialSession,
    status: 'authenticated',
    user: {
      id: OFFICER,
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
  holding(['patient.read.allergies', 'allergy.write', 'patient.read.demographics']);
  getAllergyState.mockResolvedValue(NOBODY_ASKED);
  listAllergyReactions.mockResolvedValue(REACTIONS);
  listAllergyChanges.mockResolvedValue(CHANGES);
  recordAllergy.mockResolvedValue(RECORDED);
  assertAllergyStatus.mockResolvedValue(ASKED_AND_NONE);
  withdrawAllergy.mockResolvedValue(NOBODY_ASKED);
  withdrawAllergyAssertion.mockResolvedValue(NOBODY_ASKED);
  listFavourites.mockResolvedValue({ system: 'DTHC', version: '1.0', concepts: [concept()] });
  searchConcepts.mockResolvedValue({ system: 'DTHC', version: '1.0', concepts: [concept()] });
  getPatient.mockResolvedValue({
    id: PATIENT,
    clinical_id: 'DTHC-FRD-2026-000137',
    name_en: 'Rahima Begum',
    name_bn: 'রহিমা বেগম',
  });
});

afterEach(() => {
  useSessionStore.setState(initialSession, true);
  vi.restoreAllMocks();
});

async function openStrip(locale: 'en' | 'bn' = 'en') {
  renderWithProviders(<AllergyBanner patientId={PATIENT} />, { locale });
  return screen.findByTestId('allergy-strip');
}

async function openPanel(locale: 'en' | 'bn' = 'en') {
  renderWithProviders(<AllergyPanel patientId={PATIENT} />, { locale });
  await screen.findByTestId('allergy-panel');
}

describe('the four statuses are four different sentences', () => {
  it('draws an empty list with NONE_RECORDED and one with NO_KNOWN_ALLERGY differently', async () => {
    /*
     * The named test acceptance criterion 3 rests on.
     *
     * Both of these states carry an empty `allergies` array. One means nobody has asked and
     * the other means somebody asked and was told there are none, and they are the two ends
     * of the thing this checkpoint exists for. A strip that read the list rather than the
     * status would draw them identically — as a blank space, or worse as a quiet "no
     * allergies" — and the reading a prescriber takes from that is the safe-looking one.
     *
     * So: different words, a different `data-status`, a different answer to whether the
     * patient may go past the history station, and only one of them saying anything that
     * could be read as reassurance.
     */
    getAllergyState.mockResolvedValue(NOBODY_ASKED);
    const { unmount } = renderWithProviders(<AllergyBanner patientId={PATIENT} />);
    const unasked = await screen.findByTestId('allergy-strip');

    expect(unasked).toHaveAttribute('data-status', 'NONE_RECORDED');
    expect(unasked).toHaveAttribute('data-satisfied', 'false');
    expect(within(unasked).getByText('Allergy status not recorded')).toBeInTheDocument();
    expect(
      within(unasked).getByText(
        'Nobody has asked yet. This is not the same as a patient with no allergies.',
      ),
    ).toBeInTheDocument();
    // Nothing here says the patient has no allergies.
    expect(within(unasked).queryByText('No known allergies')).not.toBeInTheDocument();
    const unaskedWords = unasked.textContent ?? '';

    unmount();

    getAllergyState.mockResolvedValue(ASKED_AND_NONE);
    renderWithProviders(<AllergyBanner patientId={PATIENT} />);
    const answered = await screen.findByTestId('allergy-strip');

    expect(answered).toHaveAttribute('data-status', 'NO_KNOWN_ALLERGY');
    expect(answered).toHaveAttribute('data-satisfied', 'true');
    expect(within(answered).getByText('No known allergies')).toBeInTheDocument();
    expect(
      within(answered).getByText(
        'Somebody asked and was told there are none. A statement by a person, not an empty field.',
      ),
    ).toBeInTheDocument();
    // A person's name and a time, because the assertion is an event and not a property.
    expect(within(answered).getByTestId('allergy-assertion')).toHaveTextContent(/0190a8f2/);

    // And the two strips do not read the same, which is the whole assertion.
    expect(answered.textContent).not.toBe(unaskedWords);
  });

  it('says "unable to assess" as neither reassurance nor an allergy, with its reason', async () => {
    getAllergyState.mockResolvedValue(COULD_NOT_ASK);
    const strip = await openStrip();

    expect(strip).toHaveAttribute('data-status', 'UNABLE_TO_ASSESS');
    expect(within(strip).getByText('Allergies could not be assessed')).toBeInTheDocument();
    expect(
      within(strip).getByText(
        'Somebody asked and could not get an answer. This does not say the patient has no allergies.',
      ),
    ).toBeInTheDocument();

    // The reason is the thing that makes the third state reviewable rather than a silent
    // gap wearing a label, so it is on the strip and not behind a click.
    expect(
      within(strip).getByText(/Patient is drowsy and no attendant is present\./),
    ).toBeInTheDocument();

    // It satisfies the gate — that is why there is no override — so nothing says the
    // patient is blocked…
    expect(strip).toHaveAttribute('data-satisfied', 'true');
    expect(within(strip).queryByTestId('allergy-gate')).not.toBeInTheDocument();
    // …and it is not drawn as an allergy either: there is no allergy line at all.
    expect(within(strip).queryByTestId(/^allergy-line-/)).not.toBeInTheDocument();
    // Nor as the one status that may look reassuring.
    expect(within(strip).queryByText('No known allergies')).not.toBeInTheDocument();
  });

  it('says plainly that the patient cannot go past the history station, and what will clear it', async () => {
    const strip = await openStrip();

    const gate = within(strip).getByTestId('allergy-gate');
    expect(
      within(gate).getByText('This patient cannot be sent past the history station.'),
    ).toBeInTheDocument();
    expect(
      within(gate).getByText(/Record an allergy, or state that there are/),
    ).toBeInTheDocument();
    // The three answers are named, and the sentence closes the door on a fourth.
    expect(within(gate).getByText(/Nothing else opens the way/)).toBeInTheDocument();
  });

  it('says nothing about the gate once it is satisfied', async () => {
    getAllergyState.mockResolvedValue(RECORDED);
    const strip = await openStrip();

    expect(strip).toHaveAttribute('data-satisfied', 'true');
    expect(within(strip).queryByTestId('allergy-gate')).not.toBeInTheDocument();
  });

  it('does not draw an unreadable status as a patient with no allergies', async () => {
    // The two look identical on screen and mean opposite things, and one of them reads as
    // "nothing to worry about" to the person handing over the medicine.
    getAllergyState.mockRejectedValue(new Error('down'));

    renderWithProviders(<AllergyBanner patientId={PATIENT} />);

    expect(
      await screen.findByText('This patient’s allergy status could not be read.'),
    ).toBeInTheDocument();
    expect(screen.queryByTestId('allergy-strip')).not.toBeInTheDocument();
  });

  it('shows nothing at all to somebody who may not read allergies', async () => {
    holding(['patient.read.demographics']);
    renderWithProviders(<AllergyBanner patientId={PATIENT} />);

    await waitFor(() => expect(getAllergyState).not.toHaveBeenCalled());
    expect(screen.queryByTestId('allergy-strip')).not.toBeInTheDocument();
    // An empty strip would read as an answer; nothing at all does not.
    expect(screen.queryByTestId('allergy-unreadable')).not.toBeInTheDocument();
  });
});

describe('the patient header carries it on every screen', () => {
  it('renders the strip inside the header the patient layout mounts', async () => {
    // Criterion 3 is "allergies appear on the patient header on every screen", and the only
    // way that is true of screens nobody has written yet is for the header itself to carry
    // it. This is the assertion that the wiring exists rather than being claimed.
    renderWithProviders(<PatientHeader patientId={PATIENT} />);

    const header = await screen.findByTestId('patient-header');
    expect(await within(header).findByTestId('allergy-strip')).toBeInTheDocument();
    expect(within(header).getByText('Allergy status not recorded')).toBeInTheDocument();
  });

  it('still carries the strip when the patient’s own name cannot be read', async () => {
    // The name is a convenience. The allergy status is not, and it must not be taken down
    // by an unrelated failure.
    getPatient.mockRejectedValue(new Error('down'));
    renderWithProviders(<PatientHeader patientId={PATIENT} />);

    const header = await screen.findByTestId('patient-header');
    expect(await within(header).findByTestId('allergy-strip')).toBeInTheDocument();
    expect(within(header).queryByText('Rahima Begum')).not.toBeInTheDocument();
  });

  it('names the patient in Bangla when the interface is in Bangla', async () => {
    renderWithProviders(<PatientHeader patientId={PATIENT} />, { locale: 'bn' });

    const header = await screen.findByTestId('patient-header');
    expect(await within(header).findByText('রহিমা বেগম')).toBeInTheDocument();
    // The clinical id is an identifier and stays in ASCII in both languages: it is read
    // back at a desk and copied onto paper.
    expect(within(header).getByText('DTHC-FRD-2026-000137')).toBeInTheDocument();
  });

  it('names the patient and their clinical id when it can', async () => {
    renderWithProviders(<PatientHeader patientId={PATIENT} />);

    const header = await screen.findByTestId('patient-header');
    expect(await within(header).findByText('Rahima Begum')).toBeInTheDocument();
    // A clinical id is an identifier and stays in ASCII in both languages.
    expect(within(header).getByText('DTHC-FRD-2026-000137')).toBeInTheDocument();
  });
});

describe('what leads on the strip', () => {
  it('puts the emergency allergy first and says so in words', async () => {
    getAllergyState.mockResolvedValue(RECORDED);
    const strip = await openStrip();

    const lines = within(strip).getAllByTestId(/^allergy-line-/);
    // The server sorts worst first and nothing re-sorts: the one that stops a heart is at
    // the top rather than buried under a rash from 1998.
    expect(lines.map((line) => line.getAttribute('data-testid'))).toEqual([
      'allergy-line-allergy-penicillin',
      'allergy-line-allergy-rash',
    ]);

    const worst = lines[0]!;
    expect(worst).toHaveAttribute('data-emergency', 'true');
    // The word, not the hue. It has to survive a photograph, a window and a monochrome
    // clinic printer.
    expect(within(worst).getByText('Emergency reaction')).toBeInTheDocument();
    expect(within(worst).getByText('Life-threatening')).toBeInTheDocument();
    expect(within(worst).getByText('Penicillin')).toBeInTheDocument();
    expect(within(worst).getByText('Collapse or anaphylaxis')).toBeInTheDocument();

    expect(lines[1]).toHaveAttribute('data-emergency', 'false');
    expect(within(lines[1]!).queryByText('Emergency reaction')).not.toBeInTheDocument();

    // And the strip as a whole interrupts, which is right on the screen where somebody is
    // about to prescribe and wrong everywhere else.
    expect(strip).toHaveAttribute('role', 'alert');
    expect(strip).toHaveAttribute('data-emergency', 'true');
  });

  it('names an assertion that is no longer the headline, so it is not read as the allergy’s author', async () => {
    // Recording an allergy does not withdraw an earlier "no known allergies": both are true
    // statements about their own moment, and the live allergy outranks the assertion. An
    // unlabelled attribution line under the list would read as the name behind the allergy.
    getAllergyState.mockResolvedValue(
      state({
        status: 'ALLERGIES_RECORDED',
        satisfied: true,
        allergies: [RASH],
        assertion: ASKED_AND_NONE.assertion,
      }),
    );
    const strip = await openStrip();

    const line = within(strip).getByTestId('allergy-assertion');
    expect(line.textContent).toMatch(/^No known allergies — Stated /);
  });

  it('does not interrupt when there is nothing to interrupt for', async () => {
    getAllergyState.mockResolvedValue(ASKED_AND_NONE);
    const strip = await openStrip();
    expect(strip).toHaveAttribute('role', 'status');
  });

  it('marks an uncoded allergy and shows what she said', async () => {
    getAllergyState.mockResolvedValue(
      state({
        status: 'ALLERGIES_RECORDED',
        satisfied: true,
        allergies: [
          allergy({
            id: 'allergy-uncoded',
            code_system: undefined,
            code_version: undefined,
            code: undefined,
            display_en: undefined,
            display_bn: undefined,
            said: 'the yellow tablet from the pharmacy near the bridge',
          }),
        ],
      }),
    );

    const strip = await openStrip();
    const line = within(strip).getByTestId('allergy-line-allergy-uncoded');

    // The patient's own words name it, and the row says no code stands behind them.
    expect(
      within(line).getByText('the yellow tablet from the pharmacy near the bridge'),
    ).toBeInTheDocument();
    expect(within(line).getByText('No code')).toBeInTheDocument();
  });
});

describe('the station panel offers three answers and no fourth', () => {
  it('offers no control that clears the gate without one of the three answers', async () => {
    /*
     * The named test that keeps the override out.
     *
     * The unconscious patient and the child with no attendant are real, and the obvious
     * kindness — a skip, a "proceed anyway", a note that marks the question asked — is the
     * thing the plan warns about by name: a gate with a way past it is a gate people learn
     * the shape of. So the panel offers exactly three controls, each of them an answer with
     * a person behind it, and pressing the mildest of them still takes a second deliberate
     * confirmation rather than firing on the first click.
     */
    const user = userEvent.setup();
    await openPanel();

    const actions = await screen.findByTestId('allergy-actions');
    const offered = within(actions).getAllByRole('button');
    expect(offered.map((button) => button.textContent)).toEqual([
      'Record an allergy',
      'No known allergies',
      'Could not assess',
    ]);

    // Nothing anywhere on the screen offers a way round the question.
    const forbidden = /(skip|anyway|proceed|override|bypass|later|not now|continue without)/i;
    for (const button of screen.getAllByRole('button')) {
      expect(button.textContent ?? '', button.textContent ?? '').not.toMatch(forbidden);
      expect(button.getAttribute('aria-label') ?? '').not.toMatch(forbidden);
    }

    // And the safe-sounding one does not fire on the button that opens it: it states what
    // is about to be claimed, and waits.
    await user.click(within(actions).getByRole('button', { name: 'No known allergies' }));
    expect(assertAllergyStatus).not.toHaveBeenCalled();
    expect(screen.getByText(/It is not a default and not an empty field/)).toBeInTheDocument();

    // The feature exports nothing that could be wired to such a control later.
    expect(
      Object.keys(surface).filter((name) =>
        /(skip|override|bypass|proceed|waive|unblock)/i.test(name),
      ),
    ).toEqual([]);
  });

  it('says nobody has asked rather than showing an empty list as an answer', async () => {
    await openPanel();

    expect(await screen.findByText('Nobody has asked about allergies yet')).toBeInTheDocument();
    expect(screen.getByText(/This is not a record of no allergies/)).toBeInTheDocument();
  });

  it('states in your own name that there are no known allergies, carrying no reason', async () => {
    const user = userEvent.setup();
    await openPanel();

    await user.click(await screen.findByRole('button', { name: 'No known allergies' }));
    await user.click(screen.getByRole('button', { name: 'Yes, no known allergies' }));

    await waitFor(() => expect(assertAllergyStatus).toHaveBeenCalledTimes(1));
    expect(assertAllergyStatus).toHaveBeenCalledWith(PATIENT, { kind: 'NO_KNOWN_ALLERGY' });
  });

  it('refuses "could not assess" until there is a reason, and sends it', async () => {
    const user = userEvent.setup();
    await openPanel();

    await user.click(await screen.findByRole('button', { name: 'Could not assess' }));

    const confirm = screen.getByRole('button', { name: 'Record that it could not be assessed' });
    // Disabled before the reason exists, rather than submitting and then reporting what the
    // form already knew. The point of the third state is that it is reviewable.
    expect(confirm).toBeDisabled();

    await user.type(
      screen.getByLabelText(/Why could the answer not be got\?/),
      'Patient is drowsy and no attendant is present.',
    );
    expect(confirm).toBeEnabled();
    await user.click(confirm);

    await waitFor(() => expect(assertAllergyStatus).toHaveBeenCalledTimes(1));
    expect(assertAllergyStatus).toHaveBeenCalledWith(PATIENT, {
      kind: 'UNABLE_TO_ASSESS',
      reason: 'Patient is drowsy and no attendant is present.',
    });
  });

  it('offers no reason on "no known allergies", and lands a refusal about one on the reason field', async () => {
    /*
     * Two halves of the same rule. The server requires a reason on "unable to assess" and
     * refuses one on "no known allergies", so:
     *
     *  - the panel for the first has no reason box at all, which means the request the
     *    server would refuse cannot be made from this screen; and
     *  - when the server does name `reason`, the sentence lands beside the box it is about
     *    rather than in a banner above a form the operator has already scrolled past.
     */
    const user = userEvent.setup();
    await openPanel();

    await user.click(await screen.findByRole('button', { name: 'No known allergies' }));
    expect(screen.queryByLabelText(/Why could the answer not be got\?/)).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Yes, no known allergies' }));
    await waitFor(() => expect(assertAllergyStatus).toHaveBeenCalledTimes(1));
    const [, sent] = assertAllergyStatus.mock.calls[0] as [string, Record<string, unknown>];
    expect(sent).not.toHaveProperty('reason');

    // Now the other half, where the field exists and the server has something to say
    // about it.
    assertAllergyStatus.mockRejectedValue(refusal({ reason: 'Say why it could not be assessed.' }));
    await user.click(screen.getByRole('button', { name: 'Could not assess' }));
    const box = screen.getByLabelText(/Why could the answer not be got\?/);
    await user.type(box, '.');
    await user.click(screen.getByRole('button', { name: 'Record that it could not be assessed' }));

    await waitFor(() =>
      expect(box).toHaveAccessibleDescription(/Say why it could not be assessed/),
    );
    expect(box).toHaveAttribute('aria-invalid', 'true');
  });

  it('shows a refusal about a field the assertion panel has no box for', async () => {
    const user = userEvent.setup();
    assertAllergyStatus.mockRejectedValue(refusal({ patient_id: 'That patient is merged.' }));
    await openPanel();

    await user.click(await screen.findByRole('button', { name: 'No known allergies' }));
    await user.click(screen.getByRole('button', { name: 'Yes, no known allergies' }));

    // Swallowed, this would look like a statement that landed. It did not.
    expect(
      await screen.findByText('Nothing was recorded. The allergy status has not changed.'),
    ).toBeInTheDocument();
  });

  it('says so when an assertion fails and does not pretend the gate moved', async () => {
    const user = userEvent.setup();
    assertAllergyStatus.mockRejectedValue(new Error('offline'));
    await openPanel();

    await user.click(await screen.findByRole('button', { name: 'No known allergies' }));
    await user.click(screen.getByRole('button', { name: 'Yes, no known allergies' }));

    expect(
      await screen.findByText('Nothing was recorded. The allergy status has not changed.'),
    ).toBeInTheDocument();
  });

  it('offers nothing to write to somebody who may only read', async () => {
    holding(['patient.read.allergies']);
    await openPanel();

    await screen.findByTestId('allergy-panel');
    expect(screen.queryByTestId('allergy-actions')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'No known allergies' })).not.toBeInTheDocument();
  });

  it('refuses the whole panel to somebody who may not read allergies', async () => {
    holding(['patient.read.demographics']);
    renderWithProviders(<AllergyPanel patientId={PATIENT} />);

    expect(
      await screen.findByText('You may not read this patient’s allergies while wearing this role.'),
    ).toBeInTheDocument();
    expect(getAllergyState).not.toHaveBeenCalled();
  });

  it('does not draw an unreadable panel as an empty one', async () => {
    getAllergyState.mockRejectedValue(new Error('down'));
    renderWithProviders(<AllergyPanel patientId={PATIENT} />);

    expect(
      await screen.findByText('This patient’s allergy status could not be read.'),
    ).toBeInTheDocument();
  });
});

describe('recording one allergy', () => {
  it('puts the system, the version and the code on the request together', async () => {
    const user = userEvent.setup();
    await openPanel();

    await user.click(await screen.findByRole('button', { name: 'Record an allergy' }));
    await user.click(await screen.findByLabelText(/What is the allergy to\?/));
    await user.click(await screen.findByRole('option', { name: /Penicillin/ }));
    await user.selectOptions(screen.getByLabelText(/What did it do\?/), 'ANAPHYLAXIS');
    await user.selectOptions(screen.getByLabelText(/How bad was it\?/), 'life_threatening');
    await user.selectOptions(screen.getByLabelText(/How sure is this\?/), 'confirmed');
    await user.click(screen.getByRole('button', { name: 'Record this allergy' }));

    await waitFor(() => expect(recordAllergy).toHaveBeenCalledTimes(1));
    expect(recordAllergy).toHaveBeenCalledWith(PATIENT, {
      code_system: 'DTHC',
      code_version: '1.0',
      code: 'ALLERGEN_PENICILLIN',
      reaction: 'ANAPHYLAXIS',
      severity: 'life_threatening',
      certainty: 'confirmed',
    });
  });

  it('records an uncoded allergy when the dictionary has nothing, keeping her words', async () => {
    const user = userEvent.setup();
    await openPanel();

    await user.click(await screen.findByRole('button', { name: 'Record an allergy' }));
    await user.type(
      await screen.findByLabelText(/What she said/),
      'the yellow tablet from the pharmacy near the bridge',
    );
    await user.selectOptions(screen.getByLabelText(/What did it do\?/), 'RASH');
    await user.selectOptions(screen.getByLabelText(/How bad was it\?/), 'moderate');
    await user.selectOptions(screen.getByLabelText(/How sure is this\?/), 'suspected');
    await user.click(screen.getByRole('button', { name: 'Record this allergy' }));

    await waitFor(() => expect(recordAllergy).toHaveBeenCalledTimes(1));
    const [, request] = recordAllergy.mock.calls[0] as [string, Record<string, unknown>];
    expect(request.said).toBe('the yellow tablet from the pharmacy near the bridge');
    expect(request).not.toHaveProperty('code');
    expect(request).not.toHaveProperty('code_version');
  });

  it('asks for a severity and a certainty rather than choosing one', async () => {
    const user = userEvent.setup();
    await openPanel();

    await user.click(await screen.findByRole('button', { name: 'Record an allergy' }));
    // Nothing is pre-selected: a severity nobody stated is a claim nobody made, in the one
    // record a pharmacist reads before handing over a medicine.
    expect((await screen.findByLabelText(/How bad was it\?/)) as HTMLSelectElement).toHaveValue('');
    expect(screen.getByLabelText<HTMLSelectElement>(/How sure is this\?/)).toHaveValue('');

    await user.type(screen.getByLabelText(/What she said/), 'sulfa');
    await user.selectOptions(screen.getByLabelText(/What did it do\?/), 'RASH');
    await user.click(screen.getByRole('button', { name: 'Record this allergy' }));

    expect(screen.getByLabelText(/How bad was it\?/)).toHaveAccessibleDescription(
      /Say how bad it was/,
    );
    expect(recordAllergy).not.toHaveBeenCalled();
  });

  it('offers the emergency reactions first and says which they are', async () => {
    const user = userEvent.setup();
    await openPanel();

    await user.click(await screen.findByRole('button', { name: 'Record an allergy' }));
    const field = (await screen.findByLabelText(/What did it do\?/)) as HTMLSelectElement;

    // `is_emergency` is a property of the reaction, so the list says so rather than leaving
    // it to whatever severity is ticked beside it.
    expect([...field.options].map((option) => option.value)).toEqual([
      '',
      'ANAPHYLAXIS',
      'RASH',
      'ITCHING',
    ]);
    expect(field.options[1]!.textContent).toBe('Collapse or anaphylaxis — emergency');
  });

  it('renders a 422 against the field the server named', async () => {
    const user = userEvent.setup();
    recordAllergy.mockRejectedValue(refusal({ reaction: 'That reaction is not in the list.' }));
    await openPanel();

    await user.click(await screen.findByRole('button', { name: 'Record an allergy' }));
    await user.type(await screen.findByLabelText(/What she said/), 'sulfa');
    await user.selectOptions(screen.getByLabelText(/What did it do\?/), 'RASH');
    await user.selectOptions(screen.getByLabelText(/How bad was it\?/), 'mild');
    await user.selectOptions(screen.getByLabelText(/How sure is this\?/), 'suspected');
    await user.click(screen.getByRole('button', { name: 'Record this allergy' }));

    const field = screen.getByLabelText(/What did it do\?/);
    await waitFor(() =>
      expect(field).toHaveAccessibleDescription(/That reaction is not in the list/),
    );
  });

  it('says the allergy was not recorded when the server said nothing useful', async () => {
    const user = userEvent.setup();
    recordAllergy.mockRejectedValue(new Error('offline'));
    await openPanel();

    await user.click(await screen.findByRole('button', { name: 'Record an allergy' }));
    await user.type(await screen.findByLabelText(/What she said/), 'sulfa');
    await user.selectOptions(screen.getByLabelText(/What did it do\?/), 'RASH');
    await user.selectOptions(screen.getByLabelText(/How bad was it\?/), 'mild');
    await user.selectOptions(screen.getByLabelText(/How sure is this\?/), 'suspected');
    await user.click(screen.getByRole('button', { name: 'Record this allergy' }));

    expect(
      await screen.findByText('The allergy was not recorded. Nothing has changed.'),
    ).toBeInTheDocument();
  });

  it('can be abandoned without recording anything', async () => {
    const user = userEvent.setup();
    await openPanel();

    await user.click(await screen.findByRole('button', { name: 'Record an allergy' }));
    await user.click(await screen.findByRole('button', { name: 'Cancel' }));

    expect(recordAllergy).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'Record an allergy' })).toBeInTheDocument();
  });

  it('says so rather than offering a form that can record nothing', async () => {
    const user = userEvent.setup();
    listAllergyReactions.mockResolvedValue([]);
    await openPanel();

    await user.click(await screen.findByRole('button', { name: 'Record an allergy' }));
    expect(
      await screen.findByText(
        'The list of reactions could not be read, so no allergy can be recorded here yet.',
      ),
    ).toBeInTheDocument();
  });
});

describe('taking something back', () => {
  it('withdrawing an allergy takes a reason and is refused without one', async () => {
    const user = userEvent.setup();
    getAllergyState.mockResolvedValue(RECORDED);
    await openPanel();

    const row = await screen.findByTestId('allergy-allergy-penicillin');
    await user.click(within(row).getByRole('button', { name: /Withdraw Penicillin/ }));

    const confirm = within(row).getByRole('button', { name: 'Withdraw it' });
    expect(confirm).toBeDisabled();

    await user.type(
      within(row).getByLabelText(/Why should this not stand\?/),
      'Recorded on the wrong patient.',
    );
    expect(confirm).toBeEnabled();
    await user.click(confirm);

    await waitFor(() => expect(withdrawAllergy).toHaveBeenCalledTimes(1));
    expect(withdrawAllergy).toHaveBeenCalledWith(
      'allergy-penicillin',
      'Recorded on the wrong patient.',
    );
    // Nothing is deleted, and the screen says so before the reason is typed.
    expect(within(row).queryByRole('button', { name: /Delete/ })).not.toBeInTheDocument();
  });

  it('says withdrawing the last one closes the way past the station again', async () => {
    const user = userEvent.setup();
    getAllergyState.mockResolvedValue(RECORDED);
    await openPanel();

    const row = await screen.findByTestId('allergy-allergy-rash');
    await user.click(within(row).getByRole('button', { name: /Withdraw Sulfa drugs/ }));

    expect(
      within(row).getByText(/closes the way past the history station again/),
    ).toBeInTheDocument();
  });

  it('withdrawing the standing assertion takes a reason too', async () => {
    const user = userEvent.setup();
    getAllergyState.mockResolvedValue(ASKED_AND_NONE);
    await openPanel();

    const standing = await screen.findByTestId('standing-assertion');
    expect(standing).toHaveAttribute('data-kind', 'NO_KNOWN_ALLERGY');

    await user.click(within(standing).getByRole('button', { name: 'Withdraw this statement' }));
    const confirm = within(standing).getByRole('button', { name: 'Withdraw it' });
    expect(confirm).toBeDisabled();

    await user.type(
      within(standing).getByLabelText(/Why should this not stand\?/),
      'Tapped on the wrong patient.',
    );
    await user.click(confirm);

    await waitFor(() => expect(withdrawAllergyAssertion).toHaveBeenCalledTimes(1));
    expect(withdrawAllergyAssertion).toHaveBeenCalledWith(
      'assertion-1',
      'Tapped on the wrong patient.',
    );
  });

  it('shows the reason behind an "unable to assess" that stands', async () => {
    getAllergyState.mockResolvedValue(COULD_NOT_ASK);
    await openPanel();

    const standing = await screen.findByTestId('standing-assertion');
    expect(standing).toHaveAttribute('data-kind', 'UNABLE_TO_ASSESS');
    expect(
      within(standing).getByText(/Patient is drowsy and no attendant is present\./),
    ).toBeInTheDocument();
  });

  it('says so when a withdrawal fails, and the notice can be dismissed', async () => {
    const user = userEvent.setup();
    getAllergyState.mockResolvedValue(RECORDED);
    withdrawAllergy.mockRejectedValue(new Error('offline'));
    await openPanel();

    const row = await screen.findByTestId('allergy-allergy-rash');
    await user.click(within(row).getByRole('button', { name: /Withdraw Sulfa drugs/ }));
    await user.type(within(row).getByLabelText(/Why should this not stand\?/), 'wrong file');
    await user.click(within(row).getByRole('button', { name: 'Withdraw it' }));

    expect(await within(row).findByText('Nothing was withdrawn.')).toBeInTheDocument();
    await user.click(within(row).getByRole('button', { name: 'Dismiss' }));
    expect(within(row).queryByText('Nothing was withdrawn.')).not.toBeInTheDocument();
  });

  it('says so when withdrawing the standing statement fails', async () => {
    const user = userEvent.setup();
    getAllergyState.mockResolvedValue(ASKED_AND_NONE);
    withdrawAllergyAssertion.mockRejectedValue(new Error('offline'));
    await openPanel();

    const standing = await screen.findByTestId('standing-assertion');
    await user.click(within(standing).getByRole('button', { name: 'Withdraw this statement' }));
    await user.type(within(standing).getByLabelText(/Why should this not stand\?/), 'wrong desk');
    await user.click(within(standing).getByRole('button', { name: 'Withdraw it' }));

    expect(await within(standing).findByText('Nothing was withdrawn.')).toBeInTheDocument();
    // The claim is still standing, and the screen does not pretend otherwise.
    expect(within(standing).getByText('No known allergies')).toBeInTheDocument();
  });

  it('lets the standing statement be left alone', async () => {
    const user = userEvent.setup();
    getAllergyState.mockResolvedValue(ASKED_AND_NONE);
    await openPanel();

    const standing = await screen.findByTestId('standing-assertion');
    await user.click(within(standing).getByRole('button', { name: 'Withdraw this statement' }));
    await user.click(within(standing).getByRole('button', { name: 'Cancel' }));

    expect(withdrawAllergyAssertion).not.toHaveBeenCalled();
  });

  it('can be abandoned without withdrawing anything', async () => {
    const user = userEvent.setup();
    getAllergyState.mockResolvedValue(RECORDED);
    await openPanel();

    const row = await screen.findByTestId('allergy-allergy-rash');
    await user.click(within(row).getByRole('button', { name: /Withdraw Sulfa drugs/ }));
    await user.click(within(row).getByRole('button', { name: 'Cancel' }));

    expect(withdrawAllergy).not.toHaveBeenCalled();
  });
});

describe('what one allergy shows on the station screen', () => {
  it('shows the coding whole — code, system and version', async () => {
    getAllergyState.mockResolvedValue(RECORDED);
    await openPanel();

    const row = await screen.findByTestId('allergy-allergy-penicillin');
    const chip = within(row).getByTestId('concept-chip');
    expect(within(chip).getByText('ALLERGEN_PENICILLIN')).toBeInTheDocument();
    expect(within(chip).getByText('DTHC')).toBeInTheDocument();
    expect(within(chip).getByText('1.0')).toBeInTheDocument();
  });

  it('marks an uncoded allergy as uncoded and shows her words', async () => {
    getAllergyState.mockResolvedValue(
      state({
        status: 'ALLERGIES_RECORDED',
        satisfied: true,
        allergies: [
          allergy({
            id: 'allergy-uncoded',
            code_system: undefined,
            code_version: undefined,
            code: undefined,
            display_en: undefined,
            display_bn: undefined,
            said: 'the yellow tablet from the pharmacy near the bridge',
            note: 'Swelled up within the hour.',
          }),
        ],
      }),
    );
    await openPanel();

    const row = await screen.findByTestId('allergy-allergy-uncoded');
    expect(row).toHaveAttribute('data-coded', 'false');
    expect(within(row).getByTestId('uncoded-flag')).toHaveTextContent('No code');
    // Her words twice over, and deliberately: once naming the row, once as the quotation,
    // because on an uncoded allergy they are the only thing that identifies the substance.
    expect(
      within(row).getAllByText(/the yellow tablet from the pharmacy near the bridge/).length,
    ).toBe(2);
    expect(within(row).getByText('Swelled up within the hour.')).toBeInTheDocument();
    // Nothing pretends there is a coding behind it.
    expect(within(row).queryByTestId('concept-chip')).not.toBeInTheDocument();
  });

  it('shows a refusal about the coding beside the picker', async () => {
    const user = userEvent.setup();
    recordAllergy.mockRejectedValue(refusal({ code: 'That code is not an allergen.' }));
    await openPanel();

    await user.click(await screen.findByRole('button', { name: 'Record an allergy' }));
    await user.type(await screen.findByLabelText(/What she said/), 'sulfa');
    await user.selectOptions(screen.getByLabelText(/What did it do\?/), 'RASH');
    await user.selectOptions(screen.getByLabelText(/How bad was it\?/), 'mild');
    await user.selectOptions(screen.getByLabelText(/How sure is this\?/), 'suspected');
    await user.click(screen.getByRole('button', { name: 'Record this allergy' }));

    // The picker has no error slot of its own, so the sentence goes under it as an alert
    // rather than into a banner at the top of a form the operator has already scrolled past.
    expect(await screen.findByText('That code is not an allergen.')).toBeInTheDocument();
  });

  it('shows a refusal the form has no field for rather than swallowing it', async () => {
    const user = userEvent.setup();
    recordAllergy.mockRejectedValue(refusal({ patient_id: 'That patient is merged.' }));
    await openPanel();

    await user.click(await screen.findByRole('button', { name: 'Record an allergy' }));
    await user.type(await screen.findByLabelText(/What she said/), 'sulfa');
    await user.selectOptions(screen.getByLabelText(/What did it do\?/), 'RASH');
    await user.selectOptions(screen.getByLabelText(/How bad was it\?/), 'mild');
    await user.selectOptions(screen.getByLabelText(/How sure is this\?/), 'suspected');
    await user.click(screen.getByRole('button', { name: 'Record this allergy' }));

    expect(await screen.findByText('That patient is merged.')).toBeInTheDocument();
  });

  it('says what it did, how bad it was, how sure, and who recorded it', async () => {
    getAllergyState.mockResolvedValue(RECORDED);
    await openPanel();

    const row = await screen.findByTestId('allergy-allergy-penicillin');
    expect(within(row).getByText('Collapse or anaphylaxis')).toBeInTheDocument();
    expect(within(row).getByText('Life-threatening')).toBeInTheDocument();
    expect(within(row).getByText('Confirmed')).toBeInTheDocument();
    expect(within(row).getByText(/^Recorded .* by 0190a8f2/)).toBeInTheDocument();
  });
});

describe('the change history keeps both halves', () => {
  it('shows a withdrawn allergy, its reason and who took it back', async () => {
    await openPanel();

    const history = await screen.findByTestId('allergy-changes');
    const row = within(history).getByTestId('allergy-change-allergy-old');

    expect(row).toHaveAttribute('data-withdrawn', 'true');
    expect(within(row).getByText('the red syrup')).toBeInTheDocument();
    // Both halves: somebody believed it, somebody else disagreed, and the next clinician
    // reading the record needs to know.
    expect(
      within(row).getByText(/Withdrawn .* Recorded on the wrong patient\./),
    ).toBeInTheDocument();
  });

  it('names an assertion by its kind, since it has no substance to name', async () => {
    await openPanel();

    const row = await screen.findByTestId('allergy-change-assertion-old');
    expect(within(row).getAllByText('No known allergies').length).toBeGreaterThan(0);
    expect(row).toHaveAttribute('data-withdrawn', 'false');
  });

  it('shows a withdrawal the server described only in part', async () => {
    // `undone_by` and `undone_why` are optional on the contract, and a row that rendered
    // "undefined" beside a clinical fact is worse than one that says less.
    listAllergyChanges.mockResolvedValue([
      {
        kind: 'ALLERGY',
        id: 'allergy-thin',
        at: '2026-07-01T04:00:00Z',
        by: OFFICER,
        undone_at: '2026-07-02T04:00:00Z',
      },
    ]);
    await openPanel();

    const row = await screen.findByTestId('allergy-change-allergy-thin');
    expect(row).toHaveAttribute('data-withdrawn', 'true');
    expect(row.textContent).not.toMatch(/undefined/);
    // With no substance and no code, the line is named by what kind of statement it was.
    expect(within(row).getAllByText('Allergy').length).toBeGreaterThan(0);
  });

  it('says nothing has been said yet rather than showing a blank space', async () => {
    listAllergyChanges.mockResolvedValue([]);
    await openPanel();

    expect(
      await screen.findByText('Nothing has been said about this patient’s allergies yet.'),
    ).toBeInTheDocument();
  });

  it('says so when the change history cannot be read', async () => {
    listAllergyChanges.mockRejectedValue(new Error('down'));
    await openPanel();

    expect(await screen.findByText('The change history could not be read.')).toBeInTheDocument();
  });
});

describe('the screens read in both languages', () => {
  it('says the four statuses and the refusal in Bangla', async () => {
    getAllergyState.mockResolvedValue(NOBODY_ASKED);
    const strip = await openStrip('bn');

    // Asserted against the message file rather than a second copy typed here: two Bengali
    // strings can differ in normalisation while looking identical on the page, and what is
    // worth proving is that the Bangla message is the one that reaches the screen.
    expect(within(strip).getByText(BN.status.NONE_RECORDED)).toBeInTheDocument();
    expect(within(strip).getByText(BN.gate.blocked)).toBeInTheDocument();
  });

  it('says the emergency and the severity in Bangla, and keeps the code in ASCII', async () => {
    getAllergyState.mockResolvedValue(RECORDED);
    const strip = await openStrip('bn');

    const worst = within(strip).getByTestId('allergy-line-allergy-penicillin');
    expect(within(worst).getByText(BN.flag.emergency)).toBeInTheDocument();
    expect(within(worst).getByText(BN.severity.life_threatening)).toBeInTheDocument();
    // The substance and the reaction in Bangla, from the server's own words rather than
    // from a message file — the catalogue is what names a drug.
    expect(within(worst).getByText(ANAPHYLAXIS.display_bn as string)).toBeInTheDocument();
    expect(within(worst).getByText(ANAPHYLAXIS.reaction_bn as string)).toBeInTheDocument();
  });

  it('asks the panel’s three questions in Bangla', async () => {
    const user = userEvent.setup();
    await openPanel('bn');

    const actions = await screen.findByTestId('allergy-actions');
    expect(
      within(actions)
        .getAllByRole('button')
        .map((one) => one.textContent),
    ).toEqual([BN.actions.record, BN.actions.noKnown, BN.actions.unable]);

    await user.click(within(actions).getByRole('button', { name: BN.actions.unable }));
    expect(screen.getByLabelText(new RegExp(escapeForQuery(BN.assert.reason)))).toBeInTheDocument();
  });

  it('keeps a coded allergy’s code and version in ASCII on the station screen', async () => {
    getAllergyState.mockResolvedValue(RECORDED);
    await openPanel('bn');

    const chip = within(await screen.findByTestId('allergy-allergy-penicillin')).getByTestId(
      'concept-chip',
    );
    expect(within(chip).getByText('ALLERGEN_PENICILLIN')).toBeInTheDocument();
    expect(within(chip).getByText('1.0')).toBeInTheDocument();
  });
});

describe('the words an allergy is read in', () => {
  it('falls back one way only, and never to an empty row', async () => {
    const { changeSubject, reactionLabel, reactionName, substanceName } =
      await import('@/features/allergies/components/allergyText');

    // Bangla when there is Bangla and the interface is in Bangla; English otherwise. A
    // reader of Bangla is better served by an English word than by a blank space where the
    // allergen should be — and here a blank space reads as "checked, nothing found".
    expect(substanceName(ANAPHYLAXIS, 'bn')).toBe(ANAPHYLAXIS.display_bn);
    expect(substanceName(ANAPHYLAXIS, 'en')).toBe('Penicillin');
    expect(substanceName(allergy({ display_en: undefined }), 'en')).toBe(
      allergy().display_bn as string,
    );
    // The uncoded escape hatch: her own words name the row.
    expect(
      substanceName(
        allergy({ display_en: undefined, display_bn: undefined, said: '  the yellow tablet  ' }),
        'en',
      ),
    ).toBe('the yellow tablet');
    // And the code as a last resort, which is ugly and is at least something to look up.
    expect(
      substanceName(allergy({ display_en: undefined, display_bn: undefined, said: '   ' }), 'en'),
    ).toBe('ALLERGEN_SULFA');
    expect(
      substanceName(
        allergy({ display_en: undefined, display_bn: undefined, code: undefined }),
        'en',
      ),
    ).toBe('');

    expect(reactionName(ANAPHYLAXIS, 'bn')).toBe(ANAPHYLAXIS.reaction_bn);
    expect(reactionName(ANAPHYLAXIS, 'en')).toBe('Collapse or anaphylaxis');
    expect(reactionName(allergy({ reaction_en: undefined }), 'en')).toBe(
      allergy().reaction_bn as string,
    );
    // A vocabulary entry this build has never heard of shows its code, which is a visible
    // defect somebody will report — an empty cell reads as a reaction nobody recorded.
    expect(reactionName(allergy({ reaction_en: undefined, reaction_bn: undefined }), 'en')).toBe(
      'RASH',
    );
    expect(reactionName(allergy({ reaction_bn: undefined }), 'bn')).toBe('Rash');

    expect(reactionLabel(REACTIONS[0]!, 'bn')).toBe(REACTIONS[0]!.display_bn);
    expect(reactionLabel(REACTIONS[0]!, 'en')).toBe('Rash');
    expect(reactionLabel(reaction({ display_bn: '' }), 'bn')).toBe('Rash');
    expect(reactionLabel(reaction({ display_en: '', display_bn: '' }), 'en')).toBe('RASH');

    // A change line is named by what it was about, and by its kind when it was about
    // nothing nameable — an assertion has no substance.
    expect(changeSubject(CHANGES[0]!, 'Allergy')).toBe('the red syrup');
    expect(changeSubject({ ...CHANGES[0]!, said: '  ', code: 'ALLERGEN_X' }, 'Allergy')).toBe(
      'ALLERGEN_X',
    );
    expect(changeSubject(CHANGES[1]!, 'No known allergies')).toBe('No known allergies');
  });
});
