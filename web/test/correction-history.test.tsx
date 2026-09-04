import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from './render';

const getPatient = vi.hoisted(() => vi.fn());
const listCorrections = vi.hoisted(() => vi.fn());
const correctPatient = vi.hoisted(() => vi.fn());
const requestStepUp = vi.hoisted(() => vi.fn());

vi.mock('@/features/patients/api/patients', async () => {
  const actual = await vi.importActual<typeof import('@/features/patients/api/patients')>(
    '@/features/patients/api/patients',
  );
  return { ...actual, getPatient, listCorrections, correctPatient };
});

vi.mock('@/features/auth', async () => {
  const actual = await vi.importActual<typeof import('@/features/auth')>('@/features/auth');
  return { ...actual, useStepUp: () => requestStepUp };
});

const { CorrectionHistory } = await import('@/features/patients/components/CorrectionHistory');
const { PatientCorrection } = await import('@/features/patients/components/PatientCorrection');

type Patient = import('@/features/patients').Patient;
type Row = import('@/features/patients').PatientCorrectionRow;

/**
 * What a clinician reads when the letter in their hand disagrees with the screen (CP35).
 *
 * A correction in this system is an append-only fact, never an edit in place: the previous
 * value is kept, and the row that records the change carries who made it and why. That
 * property is worth nothing if the screen summarises it. Somebody standing at a desk with a
 * birth certificate needs to see that this date was 1985 until August, that it is 1958 now,
 * that the desk clerk changed it because the NID card says so — and that nothing in that
 * table can be quietly rewritten afterwards.
 *
 * So the assertions here are about what is legible and in what order: one row per field
 * rather than per gesture, the server's order kept, the reason as a column and not a
 * tooltip, an empty value printed as a dash rather than as a blank a reader would take for
 * "unchanged", and the previous value shown plainly — not struck through, because a value
 * that was replaced was very often not wrong, merely less precise.
 *
 * The screen around it earns its own tests for one reason: a correction the operator has
 * just made and cannot see in the history is a correction they will make a second time.
 */

const PATIENT_ID = '5f1d3e2a-0000-4000-8000-000000000001';

const patient = {
  id: PATIENT_ID,
  clinical_id: 'DTHC-FRD-2026-000137',
  name_en: 'Md Rahim Uddin',
  name_bn: 'মোঃ রহিম উদ্দিন',
  sex: 'male',
  birth: { date: '1985-03-14', precision: 'day', source: 'national_id' },
  phone_primary: '+8801712345678',
  phone_secondary: '',
  address: {
    division: 'Dhaka',
    district: 'Faridpur',
    upazila: 'Faridpur Sadar',
    address_line: '12 Mujib Road',
    postcode: '7800',
  },
  emergency_contact: {},
  socioeconomic: {},
  status: 'active',
  registered_at: '2026-08-12T05:05:00Z',
} as Patient;

function row(over: Partial<Row> = {}): Row {
  return {
    field: 'postcode',
    previous: '7800',
    current: '7801',
    reason: 'The postcard came back; the postcode is 7801.',
    high_impact: false,
    corrected_by_code: 'REG-014',
    corrected_at: '2026-09-01T04:30:00Z',
    event_id: 'e-1',
    ...over,
  };
}

const birthRow = row({
  field: 'birth_date',
  previous: '1985-03-14',
  current: '1958-03-14',
  reason: 'The NID card reads 1958; the desk typed 1985.',
  high_impact: true,
  corrected_by_code: 'DOC-002',
  corrected_at: '2026-08-20T09:15:00Z',
  event_id: 'e-2',
});

beforeEach(() => {
  getPatient.mockReset();
  listCorrections.mockReset();
  correctPatient.mockReset();
  requestStepUp.mockReset();
  getPatient.mockResolvedValue(patient);
  listCorrections.mockResolvedValue([]);
  requestStepUp.mockResolvedValue('step-up-token');
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('the correction history', () => {
  it('gives every corrected field its own row, in the order the server sent them', async () => {
    // One row per field, not per gesture. Somebody auditing a date of birth should not have
    // to open a correction about a postcode to find it.
    listCorrections.mockResolvedValue([
      birthRow,
      row({ field: 'name_en', previous: 'Md Rahim', current: 'Md Rahim Uddin', event_id: 'e-3' }),
      row(),
    ]);
    renderWithProviders(<CorrectionHistory patientId={PATIENT_ID} />);

    const table = await screen.findByRole('table');
    const cells = within(table)
      .getAllByRole('row')
      .slice(1)
      .map((line) => within(line).getAllByRole('cell')[1]?.textContent);
    expect(cells).toEqual([expect.stringContaining('Date of birth'), 'Name (English)', 'Postcode']);
  });

  it('puts what it was, what it is, why, and who in the same row', async () => {
    // The reason is a column rather than a tooltip: it is the only part of this table that
    // explains anything, and a reason nobody reads is a reason nobody writes carefully.
    listCorrections.mockResolvedValue([birthRow]);
    renderWithProviders(<CorrectionHistory patientId={PATIENT_ID} />);

    const line = (await screen.findAllByRole('row'))[1]!;
    expect(within(line).getByText('1985-03-14')).toBeInTheDocument();
    expect(within(line).getByText('1958-03-14')).toBeInTheDocument();
    expect(
      within(line).getByText('The NID card reads 1958; the desk typed 1985.'),
    ).toBeInTheDocument();
    expect(within(line).getByText('DOC-002')).toBeInTheDocument();
  });

  it('marks the changes an auditor cares about most', async () => {
    listCorrections.mockResolvedValue([birthRow, row()]);
    renderWithProviders(<CorrectionHistory patientId={PATIENT_ID} />);

    const lines = (await screen.findAllByRole('row')).slice(1);
    expect(lines[0]).toHaveAttribute('data-high-impact', 'true');
    expect(within(lines[0]!).getByText('High impact')).toBeInTheDocument();
    // A postcode is not marked, so the mark keeps meaning something.
    expect(lines[1]).not.toHaveAttribute('data-high-impact');
    expect(within(lines[1]!).queryByText('High impact')).toBeNull();
  });

  it('prints a dash where a value was empty, and leaves the old value legible', async () => {
    // A blank cell reads as "unchanged". And the previous value is not struck through:
    // often it was not wrong, merely less precise — a year-precision date replaced by a
    // certificate.
    listCorrections.mockResolvedValue([
      row({ field: 'phone_secondary', previous: '', current: '+8801812345678' }),
      row({ field: 'address_line', previous: '12 Mujib Road', current: '' }),
    ]);
    renderWithProviders(<CorrectionHistory patientId={PATIENT_ID} />);

    const lines = (await screen.findAllByRole('row')).slice(1);
    const first = within(lines[0]!).getAllByRole('cell');
    expect(first[2]).toHaveTextContent('—');
    expect(first[3]).toHaveTextContent('+8801812345678');

    const second = within(lines[1]!).getAllByRole('cell');
    expect(second[2]).toHaveTextContent('12 Mujib Road');
    expect(second[2]?.querySelector('del, s')).toBeNull();
    expect(second[3]).toHaveTextContent('—');
  });

  it.each([
    [1, '1 recorded change, newest first.'],
    [3, '3 recorded changes, newest first.'],
  ])('counts %i change(s) in the caption', async (count, caption) => {
    listCorrections.mockResolvedValue(
      Array.from({ length: count }, (_, index) => row({ event_id: `e-${index}` })),
    );
    renderWithProviders(<CorrectionHistory patientId={PATIENT_ID} />);

    expect(await screen.findByText(caption)).toBeInTheDocument();
  });

  it('states that nothing in the table can be edited or removed', async () => {
    // The append-only property, said out loud to the person reading. It is the reason the
    // table is trustworthy at all.
    listCorrections.mockResolvedValue([row()]);
    renderWithProviders(<CorrectionHistory patientId={PATIENT_ID} />);

    expect(
      await screen.findByText(/Previous values are kept permanently\. Nothing in this table/),
    ).toBeInTheDocument();
  });

  it('says a record has never been corrected rather than showing an empty table', async () => {
    renderWithProviders(<CorrectionHistory patientId={PATIENT_ID} />);

    expect(await screen.findByTestId('history-empty')).toHaveTextContent(
      'This record has never been corrected.',
    );
    expect(screen.queryByRole('table')).toBeNull();
  });

  it('says the history could not be loaded rather than that there is none', async () => {
    // The two are opposite instructions: one means "carry on", the other means "do not
    // rely on what you can see here".
    listCorrections.mockRejectedValue(new Error('gateway'));
    renderWithProviders(<CorrectionHistory patientId={PATIENT_ID} />);

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'The correction history could not be loaded.',
    );
    expect(screen.queryByTestId('history-empty')).toBeNull();
  });

  it('waits visibly rather than showing "never corrected" while it loads', async () => {
    listCorrections.mockReturnValue(new Promise(() => {}));
    renderWithProviders(<CorrectionHistory patientId={PATIENT_ID} />);

    expect(screen.getByRole('status')).toBeInTheDocument();
    expect(screen.queryByTestId('history-empty')).toBeNull();
  });

  it('reads in Bangla, including the name of the field that changed', async () => {
    listCorrections.mockResolvedValue([birthRow]);
    renderWithProviders(<CorrectionHistory patientId={PATIENT_ID} />, { locale: 'bn' });

    expect(await screen.findByText('জন্ম তারিখ')).toBeInTheDocument();
    expect(screen.getByText('কেন')).toBeInTheDocument();
    expect(screen.getByText('গুরুত্বপূর্ণ')).toBeInTheDocument();
  });

  it('writes the time in the reader’s locale rather than one fixed format', async () => {
    listCorrections.mockResolvedValue([birthRow]);

    const english = renderWithProviders(<CorrectionHistory patientId={PATIENT_ID} />);
    const inEnglish = (await screen.findAllByRole('row'))[1]!.textContent ?? '';
    english.unmount();

    renderWithProviders(<CorrectionHistory patientId={PATIENT_ID} />, { locale: 'bn' });
    const inBangla = (await screen.findAllByRole('row'))[1]!.textContent ?? '';

    // Same instant, two readers. A clinic where half the staff read Bangla digits should
    // not be shown one hard-coded rendering of when a record changed.
    expect(inEnglish).not.toBe(inBangla);
  });
});

describe('the correction screen around it', () => {
  it('waits rather than showing an empty record to type over', () => {
    getPatient.mockReturnValue(new Promise(() => {}));
    renderWithProviders(<PatientCorrection patientId={PATIENT_ID} />);

    expect(screen.getByRole('status')).toBeInTheDocument();
    expect(screen.queryByTestId('correction-form')).toBeNull();
  });

  it('refuses to show a form when the record could not be read', async () => {
    // A form pre-filled with nothing would submit blanks over a real record.
    getPatient.mockRejectedValue(new Error('gateway'));
    renderWithProviders(<PatientCorrection patientId={PATIENT_ID} />);

    expect(await screen.findByRole('alert')).toHaveTextContent('That record could not be loaded.');
    expect(screen.queryByTestId('correction-form')).toBeNull();
  });

  it('shows the record’s form and its history on one screen', async () => {
    listCorrections.mockResolvedValue([row()]);
    renderWithProviders(<PatientCorrection patientId={PATIENT_ID} />);

    expect(await screen.findByTestId('correction-form')).toBeInTheDocument();
    expect(await screen.findByRole('region', { name: 'Correction history' })).toBeInTheDocument();
    expect(await screen.findByTestId('correction-history')).toBeInTheDocument();
    expect(getPatient).toHaveBeenCalledWith(PATIENT_ID);
  });

  it('re-reads the history after a correction, so the operator can see what they just did', async () => {
    // Without this the table still says "never corrected" a second after the change was
    // accepted, and the operator makes it again.
    const user = userEvent.setup();
    listCorrections.mockResolvedValueOnce([]).mockResolvedValueOnce([row()]);
    correctPatient.mockResolvedValue({
      patient,
      changes: [{ field: 'postcode', previous: '7800', current: '7801' }],
      high_impact: false,
      invalidated: [],
      event_id: 'e-1',
    });
    renderWithProviders(<PatientCorrection patientId={PATIENT_ID} />);

    await screen.findByTestId('history-empty');

    const postcode = await screen.findByTestId('correct-postcode');
    await user.clear(postcode);
    await user.type(postcode, '7801');
    await user.type(
      screen.getByTestId('correction-reason'),
      'The postcard came back; the postcode is 7801.',
    );
    await user.click(screen.getByTestId('correction-save'));

    await waitFor(() => expect(listCorrections).toHaveBeenCalledTimes(2));
    expect(await screen.findByTestId('correction-history')).toHaveTextContent('7801');
    // An ordinary field, so nobody was asked for an authenticator code.
    expect(requestStepUp).not.toHaveBeenCalled();
  });
});
