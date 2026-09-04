import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { NextIntlClientProvider } from 'next-intl';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import bn from '../messages/bn.json';
import en from '../messages/en.json';

const correctPatient = vi.hoisted(() => vi.fn());
const requestStepUp = vi.hoisted(() => vi.fn());

vi.mock('@/features/patients/api/patients', async () => {
  const actual = await vi.importActual<typeof import('@/features/patients/api/patients')>(
    '@/features/patients/api/patients',
  );
  return { ...actual, correctPatient };
});

vi.mock('@/features/auth', async () => {
  const actual = await vi.importActual<typeof import('@/features/auth')>('@/features/auth');
  return { ...actual, useStepUp: () => requestStepUp };
});

const { CorrectionForm, changedFields } =
  await import('@/features/patients/components/CorrectionForm');
type Patient = import('@/features/patients').Patient;

/**
 * The correction screen (CP35).
 *
 * The assertions here are the three decisions that make this screen safe rather than the
 * fact that it renders: that a field nobody touched is not in the request, that a
 * high-impact field asks for a code *before* submitting, and that a telephone number
 * retyped in a different format is not treated as a change. All three are invisible in a
 * screenshot and silent in a regression.
 */

const patient: Patient = {
  id: 'p-1',
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

function withIntl(node: ReactNode, locale: 'en' | 'bn' = 'en') {
  return render(
    <NextIntlClientProvider locale={locale} messages={locale === 'bn' ? bn : en}>
      {node}
    </NextIntlClientProvider>,
  );
}

function applied(changes: { field: string; previous: string; current: string }[]) {
  return {
    patient,
    changes,
    high_impact: false,
    invalidated: [],
    event_id: 'e-1',
  };
}

beforeEach(() => {
  correctPatient.mockReset();
  requestStepUp.mockReset();
  requestStepUp.mockResolvedValue('step-up-token');
});

describe('the correction form', () => {
  it('sends only the field that was touched', async () => {
    const user = userEvent.setup();
    correctPatient.mockResolvedValue(
      applied([{ field: 'postcode', previous: '7800', current: '7801' }]),
    );
    withIntl(<CorrectionForm patient={patient} />);

    const postcode = screen.getByTestId('correct-postcode');
    await user.clear(postcode);
    await user.type(postcode, '7801');
    await user.type(
      screen.getByTestId('correction-reason'),
      'The postcard came back; the postcode is 7801.',
    );
    await user.click(screen.getByTestId('correction-save'));

    await waitFor(() => expect(correctPatient).toHaveBeenCalled());
    const body = correctPatient.mock.calls[0]![1] as Record<string, unknown>;
    expect(Object.keys(body).sort()).toEqual(['event_id', 'postcode', 'reason']);
    // The five other address fields were rendered and are not in the request.
    expect(body).not.toHaveProperty('district');
    expect(body).not.toHaveProperty('name_en');
  });

  it('will not submit without a reason', async () => {
    const user = userEvent.setup();
    withIntl(<CorrectionForm patient={patient} />);

    const postcode = screen.getByTestId('correct-postcode');
    await user.clear(postcode);
    await user.type(postcode, '7801');
    expect(screen.getByTestId('correction-save')).toBeDisabled();

    // Short of the minimum, so still refused before the request is made.
    await user.type(screen.getByTestId('correction-reason'), 'typo');
    expect(screen.getByTestId('correction-save')).toBeDisabled();
    expect(correctPatient).not.toHaveBeenCalled();
  });

  it('will not submit when nothing has changed', async () => {
    const user = userEvent.setup();
    withIntl(<CorrectionForm patient={patient} />);
    await user.type(
      screen.getByTestId('correction-reason'),
      'The card was checked and everything matches.',
    );
    expect(screen.getByTestId('correction-save')).toBeDisabled();
    expect(screen.getByTestId('correction-count')).toHaveTextContent(/nothing/i);
  });

  it('asks for a code before submitting a date of birth, not after', async () => {
    const user = userEvent.setup();
    correctPatient.mockResolvedValue(
      applied([{ field: 'birth_date', previous: '1985-03-14', current: '1958-03-14' }]),
    );
    withIntl(<CorrectionForm patient={patient} />);

    // CP32's three fields, not a native date input: `06/14/1985` and `14/06/1985` are the
    // same picker in two browser locales, and this is the field that must never be
    // ambiguous.
    await user.clear(screen.getByTestId('dob-year'));
    await user.type(screen.getByTestId('dob-year'), '1958');
    await user.type(
      screen.getByTestId('correction-reason'),
      'The NID card reads 1958; the desk typed 1985.',
    );
    await user.click(screen.getByTestId('correction-save'));

    await waitFor(() => expect(correctPatient).toHaveBeenCalled());
    expect(requestStepUp).toHaveBeenCalledWith('patient_correct_identity', expect.any(String));
    // The token reached the call, rather than the call being retried after a 403.
    expect(correctPatient.mock.calls[0]![2]).toBe('step-up-token');
    expect(correctPatient).toHaveBeenCalledTimes(1);
    expect(correctPatient.mock.calls[0]![1]).toMatchObject({ birth_date: '1958-03-14' });
  });

  it('shows the age in words as the year is retyped', async () => {
    const user = userEvent.setup();
    withIntl(<CorrectionForm patient={patient} today={new Date('2026-09-03T00:00:00Z')} />);

    // The reason this control is reused rather than a date input: a transposed year is
    // obvious as an age and invisible as four digits.
    await user.clear(screen.getByTestId('dob-year'));
    await user.type(screen.getByTestId('dob-year'), '1058');
    expect(screen.getByTestId('age-echo')).toHaveAttribute('data-tone', 'error');

    await user.clear(screen.getByTestId('dob-year'));
    await user.type(screen.getByTestId('dob-year'), '1958');
    expect(screen.getByTestId('age-echo')).toHaveAttribute('data-tone', 'ok');
    expect(screen.getByTestId('age-echo')).toHaveTextContent('68');
  });

  it('drops the day when the precision falls back to a year', async () => {
    const user = userEvent.setup();
    correctPatient.mockResolvedValue(applied([]));
    withIntl(<CorrectionForm patient={patient} />);

    // A patient who turns out to know only their birth year. The correction must carry the
    // precision as well as the date, or a percentile keeps treating an invented day as exact.
    await user.clear(screen.getByTestId('dob-day'));
    await user.clear(screen.getByTestId('dob-month'));
    await user.type(
      screen.getByTestId('correction-reason'),
      'The patient does not know the day; only the year is on the card.',
    );
    await user.click(screen.getByTestId('correction-save'));

    await waitFor(() => expect(correctPatient).toHaveBeenCalled());
    expect(correctPatient.mock.calls[0]![1]).toMatchObject({
      birth_date: '1985-01-01',
      dob_precision: 'year',
    });
  });

  it('does not ask for a code to correct an address', async () => {
    const user = userEvent.setup();
    correctPatient.mockResolvedValue(
      applied([{ field: 'upazila', previous: 'Faridpur Sadar', current: 'Boalmari' }]),
    );
    withIntl(<CorrectionForm patient={patient} />);

    await user.clear(screen.getByTestId('correct-upazila'));
    await user.type(screen.getByTestId('correct-upazila'), 'Boalmari');
    await user.type(screen.getByTestId('correction-reason'), 'The patient has moved to Boalmari.');
    await user.click(screen.getByTestId('correction-save'));

    await waitFor(() => expect(correctPatient).toHaveBeenCalled());
    expect(requestStepUp).not.toHaveBeenCalled();
    expect(correctPatient.mock.calls[0]![2]).toBeUndefined();
  });

  it('shows the previous value beside a field that has been changed', async () => {
    const user = userEvent.setup();
    withIntl(<CorrectionForm patient={patient} />);
    expect(screen.queryByTestId('correct-postcode-was')).toBeNull();
    await user.clear(screen.getByTestId('correct-postcode'));
    await user.type(screen.getByTestId('correct-postcode'), '7801');
    expect(screen.getByTestId('correct-postcode-was')).toHaveTextContent('7800');
  });

  it('names what was rebuilt rather than saying values were updated', async () => {
    const user = userEvent.setup();
    correctPatient.mockResolvedValue({
      ...applied([
        { field: 'name_en', previous: 'Md Rahim Uddin', current: 'Mohammad Rahim Uddin' },
      ]),
      high_impact: true,
      invalidated: [
        {
          derived_name: 'read.patient',
          depends_on: 'name_en',
          action: 'recompute' as const,
          description:
            'The search key is computed from the English name and must be rebuilt with it.',
        },
      ],
    });
    withIntl(<CorrectionForm patient={patient} />);

    await user.clear(screen.getByTestId('correct-name_en'));
    await user.type(screen.getByTestId('correct-name_en'), 'Mohammad Rahim Uddin');
    await user.type(
      screen.getByTestId('correction-reason'),
      'The NID card spells the name in full.',
    );
    await user.click(screen.getByTestId('correction-save'));

    const named = await screen.findByTestId('correction-invalidated');
    expect(named).toHaveTextContent('read.patient');
    expect(named).toHaveTextContent(/search key/i);
  });

  it('reads in Bangla', async () => {
    withIntl(<CorrectionForm patient={patient} />, 'bn');
    expect(screen.getByText('তথ্য সংশোধন')).toBeInTheDocument();
    expect(screen.getByText(/জন্ম তারিখ/)).toBeInTheDocument();
  });
});

describe('what counts as a change', () => {
  const before = {
    name_en: 'Md Rahim',
    name_bn: '',
    sex: 'male',
    birth_date: '1985-03-14',
    dob_precision: 'day',
    phone_primary: '+8801712345678',
    phone_secondary: '',
    division: '',
    district: '',
    upazila: '',
    address_line: '',
    postcode: '',
  };

  it('ignores a telephone number retyped in another format', () => {
    // The server refuses a correction that changes nothing, so a screen that called this a
    // change would offer a button that submits and then fails.
    expect(changedFields(before, { ...before, phone_primary: '01712-345678' })).toEqual([]);
    expect(changedFields(before, { ...before, phone_primary: '01712 345 678' })).toEqual([]);
  });

  it('ignores whitespace the operator did not mean to add', () => {
    expect(changedFields(before, { ...before, name_en: '  Md Rahim  ' })).toEqual([]);
  });

  it('reports a real change', () => {
    expect(changedFields(before, { ...before, name_en: 'Mohammad Rahim' })).toEqual(['name_en']);
    expect(changedFields(before, { ...before, phone_primary: '+8801812345678' })).toEqual([
      'phone_primary',
    ]);
  });
});
