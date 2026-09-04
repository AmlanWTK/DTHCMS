import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from './render';
import type { CriticalAlert } from '@/features/alerts/api/alerts';

/**
 * The two screens a critical value reaches (CP50, §4.4).
 *
 * The mechanism is proven on the server: what is raised, when it escalates, who may
 * acknowledge. What can only be proven here is whether the person holding the tablet can
 * *act on it*, and the ways that fails are quiet ones.
 *
 *  - **Colour carrying meaning alone.** Roughly one man in twelve cannot use the hue, a
 *    screen by a window flattens it for everybody, and the photograph a night registrar
 *    sends a consultant keeps the words and little else. So every fact these tests assert is
 *    asserted as text.
 *  - **An escalated alert drawn like a fresh one.** "Nobody answered" is not "this is
 *    dangerous", and only one of the two says the previous person has already been asked.
 *  - **`delivered: false` left in a log.** It is the difference between "somebody is on
 *    their way" and "nobody knows", and the second one is an instruction to walk.
 *  - **A 409 shouted at as an error.** Two clinicians reaching for the same alert is the
 *    system working; the screen has to say who has it and what they are doing.
 *  - **An unreadable patient strip that renders as silence.** An empty strip and a broken
 *    one look identical and mean opposite things, and one of them reads as an all-clear.
 */

const listOpenAlerts = vi.hoisted(() => vi.fn());
const listPatientAlerts = vi.hoisted(() => vi.fn());
const acknowledgeAlert = vi.hoisted(() => vi.fn());

// Partial: the network calls are stubbed, the pure ordering and validation rules are not.
// They are part of what the board does, and a test that stubbed them would prove that the
// component calls a function rather than that the right alert is at the top.
vi.mock('@/features/alerts/api/alerts', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/alerts/api/alerts')>()),
  listOpenAlerts,
  listPatientAlerts,
  acknowledgeAlert,
}));
vi.mock('@/features/realtime', () => ({ useRealtimeTopics: () => undefined }));

const { AlertBoard } = await import('@/features/alerts/components/AlertBoard');
const { AlertBanner } = await import('@/features/alerts/components/AlertBanner');

/** Six minutes ago, measured from the test's own clock rather than a fixed date. */
function minutesAgo(minutes: number): string {
  return new Date(Date.now() - minutes * 60_000).toISOString();
}

function alert(over: Partial<CriticalAlert> = {}): CriticalAlert {
  return {
    id: 'alert-spo2',
    patient_id: 'patient-1',
    observation_id: 'obs-1',
    code: 'SPO2',
    display_en: 'Oxygen saturation',
    display_bn: 'অক্সিজেন সম্পৃক্তি',
    value: 88,
    unit: '%',
    breached: 'low',
    threshold: 92,
    action_en: 'Give oxygen and call the consultant now.',
    action_bn: 'অক্সিজেন দিন এবং এখনই পরামর্শকে ডাকুন।',
    raised_at: minutesAgo(6),
    raised_by: 'user-1',
    station_code: 'STN_EXAMINATION',
    status: 'OPEN',
    escalation_step: 1,
    delivered: true,
    recipients: 3,
    ...over,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  listOpenAlerts.mockResolvedValue([alert()]);
  listPatientAlerts.mockResolvedValue([alert()]);
  acknowledgeAlert.mockResolvedValue({ outcome: 'acknowledged', alert: alert() });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('the board says what is wrong in words', () => {
  it('states the severity as a word, not only as a colour', async () => {
    renderWithProviders(<AlertBoard />);

    expect(await screen.findByText('Critical')).toBeInTheDocument();
    // And which end was crossed, which is the other half: 3.0 mmol/L is as urgent as 25.0
    // and the two mean opposite things.
    expect(screen.getByText('Low')).toBeInTheDocument();
  });

  it('names the code rather than making somebody look it up', async () => {
    // "SPO2 88" makes whoever reads it look the code up, and the moment it matters is not a
    // moment for lookups.
    renderWithProviders(<AlertBoard />);

    expect(await screen.findByRole('heading', { name: 'Oxygen saturation' })).toBeInTheDocument();
  });

  it('gives the value, its unit and the limit it crossed', async () => {
    renderWithProviders(<AlertBoard />);

    const row = await screen.findByTestId('alert-alert-spo2');
    expect(within(row).getByText('88')).toBeInTheDocument();
    expect(within(row).getByText('%')).toBeInTheDocument();
    expect(within(row).getByText(/below the critical limit of 92/)).toBeInTheDocument();
  });

  it('carries the action, which is the useful half of an alert', async () => {
    renderWithProviders(<AlertBoard />);

    expect(await screen.findByText('Give oxygen and call the consultant now.')).toBeInTheDocument();
  });

  it('says where it came from and how long it has been waiting', async () => {
    renderWithProviders(<AlertBoard />);

    const row = await screen.findByTestId('alert-alert-spo2');
    expect(within(row).getByText('Examination')).toBeInTheDocument();
    expect(within(row).getByText('raised 6 min ago')).toBeInTheDocument();
  });

  it('reads the alert in Bangla when the screen is in Bangla', async () => {
    // A Bangla-reading clinician handed an English instruction has to translate it before
    // acting, at the one moment there is no time for that.
    renderWithProviders(<AlertBoard />, { locale: 'bn' });

    expect(await screen.findByText('অক্সিজেন সম্পৃক্তি')).toBeInTheDocument();
    expect(screen.getByText('অক্সিজেন দিন এবং এখনই পরামর্শকে ডাকুন।')).toBeInTheDocument();
  });
});

describe('the board distinguishes an unanswered alert from a fresh one', () => {
  it('says nobody answered, and which step it has reached', async () => {
    listOpenAlerts.mockResolvedValue([alert({ escalation_step: 2 })]);

    renderWithProviders(<AlertBoard />);

    expect(await screen.findByText('Not answered')).toBeInTheDocument();
    expect(screen.getByText(/escalation step 2/)).toBeInTheDocument();
    // Not merely a different tint: the row is marked, so the print stylesheet and anybody
    // reading the DOM see the same distinction the eye does.
    expect(screen.getByTestId('alert-alert-spo2')).toHaveAttribute('data-escalated', 'true');
  });

  it('puts the unanswered one above the one raised more recently', async () => {
    listOpenAlerts.mockResolvedValue([
      alert({
        id: 'fresh',
        code: 'GLUCOSE',
        display_en: 'Blood glucose',
        raised_at: minutesAgo(1),
      }),
      alert({ id: 'stale', escalation_step: 3, raised_at: minutesAgo(9) }),
    ]);

    renderWithProviders(<AlertBoard />);

    await screen.findByTestId('alert-fresh');
    const rows = screen.getAllByRole('listitem');
    expect(rows[0]).toHaveAttribute('data-testid', 'alert-stale');
  });
});

describe('the board says when nobody was told', () => {
  it('says plainly that the alert reached no screen', async () => {
    // The difference between "somebody is on their way" and "nobody knows". The alert is in
    // the ledger either way; only one of these is an instruction to walk down the corridor.
    listOpenAlerts.mockResolvedValue([alert({ delivered: false, recipients: 0 })]);

    renderWithProviders(<AlertBoard />);

    expect(await screen.findByText(/reached no screen/)).toBeInTheDocument();
    expect(screen.getByText(/go and find somebody/)).toBeInTheDocument();
  });

  it('says nothing of the kind when it did reach somebody', async () => {
    renderWithProviders(<AlertBoard />);

    await screen.findByTestId('alert-alert-spo2');
    expect(screen.queryByText(/reached no screen/)).toBeNull();
  });
});

describe('acknowledging is a sentence, not a click', () => {
  it('will not take an alert until something has been written', async () => {
    const user = userEvent.setup();
    renderWithProviders(<AlertBoard />);

    await user.click(
      await screen.findByRole('button', { name: 'Acknowledge the Oxygen saturation alert' }),
    );

    const take = screen.getByRole('button', { name: 'Take this alert' });
    expect(take).toBeDisabled();
    // Two characters is still nothing anybody can act on, and the server refuses it.
    await user.type(screen.getByRole('textbox'), 'ok');
    expect(take).toBeDisabled();
  });

  it('tells the writer who is going to read it', async () => {
    // The note is not paperwork. The next person to open this record reads it instead of
    // asking, and the field has to say so rather than leaving them to guess.
    const user = userEvent.setup();
    renderWithProviders(<AlertBoard />);

    await user.click(
      await screen.findByRole('button', { name: 'Acknowledge the Oxygen saturation alert' }),
    );

    expect(screen.getByText(/next person to open this record/)).toBeInTheDocument();
  });

  it('sends what is being done about it', async () => {
    const user = userEvent.setup();
    renderWithProviders(<AlertBoard />);

    await user.click(
      await screen.findByRole('button', { name: 'Acknowledge the Oxygen saturation alert' }),
    );
    await user.type(screen.getByRole('textbox'), '  Giving oxygen, reviewing in 5.  ');
    await user.click(screen.getByRole('button', { name: 'Take this alert' }));

    await waitFor(() =>
      expect(acknowledgeAlert).toHaveBeenCalledWith('alert-spo2', 'Giving oxygen, reviewing in 5.'),
    );
  });
});

describe('two clinicians reaching for the same alert', () => {
  it('says somebody got there first, and what they said they were doing', async () => {
    // Not an error banner. A 409 here is the system working, and the useful half of the
    // notice is the other clinician's note — which is exactly why the note is mandatory.
    acknowledgeAlert.mockResolvedValue({
      outcome: 'taken',
      alert: alert({
        status: 'ACKNOWLEDGED',
        acknowledged_by: 'user-2',
        acknowledged_at: minutesAgo(1),
        acknowledgement: 'Oxygen started, consultant called.',
      }),
    });
    const user = userEvent.setup();
    renderWithProviders(<AlertBoard />);

    await user.click(
      await screen.findByRole('button', { name: 'Acknowledge the Oxygen saturation alert' }),
    );
    await user.type(screen.getByRole('textbox'), 'Giving oxygen.');
    await user.click(screen.getByRole('button', { name: 'Take this alert' }));

    expect(await screen.findByText('Somebody got there first.')).toBeInTheDocument();
    expect(screen.getByText(/Oxygen started, consultant called\./)).toBeInTheDocument();
    expect(screen.queryByText('This alert was not taken.')).toBeNull();
  });

  it('still says the alert is taken when the server named nobody', async () => {
    acknowledgeAlert.mockResolvedValue({ outcome: 'taken', alert: null });
    const user = userEvent.setup();
    renderWithProviders(<AlertBoard />);

    await user.click(
      await screen.findByRole('button', { name: 'Acknowledge the Oxygen saturation alert' }),
    );
    await user.type(screen.getByRole('textbox'), 'Giving oxygen.');
    await user.click(screen.getByRole('button', { name: 'Take this alert' }));

    expect(
      await screen.findByText('Another clinician acknowledged this one while you were writing.'),
    ).toBeInTheDocument();
  });
});

describe('when the board itself cannot be trusted', () => {
  it('says the acknowledgement did not happen, so the escalation is still somebody’s problem', async () => {
    // A failed write that cleared the row would remove the alarm without anybody having
    // answered it, and nothing on screen would mention it again.
    acknowledgeAlert.mockRejectedValue(new Error('refused'));
    const user = userEvent.setup();
    renderWithProviders(<AlertBoard />);

    await user.click(
      await screen.findByRole('button', { name: 'Acknowledge the Oxygen saturation alert' }),
    );
    await user.type(screen.getByRole('textbox'), 'Giving oxygen.');
    await user.click(screen.getByRole('button', { name: 'Take this alert' }));

    expect(await screen.findByText('This alert was not taken.')).toBeInTheDocument();
    expect(screen.getByText(/escalation is still running/)).toBeInTheDocument();
  });

  it('says it cannot read the alerts rather than showing an empty board', async () => {
    // An empty board and an unreadable one look identical and mean opposite things. Only
    // one of them is safe to read as "there is nothing wrong in this clinic".
    listOpenAlerts.mockRejectedValue(new Error('offline'));

    renderWithProviders(<AlertBoard />);

    expect(
      await screen.findByText('Critical values cannot be read right now.'),
    ).toBeInTheDocument();
    expect(screen.queryByText('Nothing is waiting')).toBeNull();
  });

  it('answers an empty board with a sentence rather than a blank', async () => {
    listOpenAlerts.mockResolvedValue([]);

    renderWithProviders(<AlertBoard />);

    expect(await screen.findByText('Nothing is waiting')).toBeInTheDocument();
    expect(screen.getByText(/has been acknowledged/)).toBeInTheDocument();
  });
});

describe('the strip on a patient screen', () => {
  it('carries the same words the board uses', async () => {
    // Two names for one finding is a physician working out whether they are looking at one
    // problem or two, at the moment that is the worst possible use of their attention.
    listPatientAlerts.mockResolvedValue([alert({ escalation_step: 2, delivered: false })]);

    renderWithProviders(<AlertBanner patientId="patient-1" />);

    expect(await screen.findByText('Not answered')).toBeInTheDocument();
    expect(screen.getByText('Oxygen saturation')).toBeInTheDocument();
    expect(screen.getByText(/below the critical limit of 92/)).toBeInTheDocument();
    expect(screen.getByText(/reached no screen/)).toBeInTheDocument();
  });

  it('counts what nobody has answered', async () => {
    listPatientAlerts.mockResolvedValue([alert(), alert({ id: 'second', code: 'GLUCOSE' })]);

    renderWithProviders(<AlertBanner patientId="patient-1" />);

    expect(await screen.findByText('2 critical values nobody has answered')).toBeInTheDocument();
  });

  it('leaves the answered ones to the history', async () => {
    // They stay in the record — an alert that vanished would make the record say the episode
    // never happened — but a strip is for what is still open.
    listPatientAlerts.mockResolvedValue([
      alert({ id: 'done', status: 'ACKNOWLEDGED', acknowledgement: 'oxygen given' }),
      alert({ id: 'open' }),
    ]);

    renderWithProviders(<AlertBanner patientId="patient-1" />);

    expect(await screen.findByTestId('alert-strip-open')).toBeInTheDocument();
    expect(screen.queryByTestId('alert-strip-done')).toBeNull();
  });

  it('shows nothing at all when this patient has nothing open', async () => {
    // A permanent "no critical values" band on every patient screen is furniture, and
    // furniture is what the eye stops seeing.
    listPatientAlerts.mockResolvedValue([]);

    const { container } = renderWithProviders(<AlertBanner patientId="patient-1" />);

    await waitFor(() => expect(listPatientAlerts).toHaveBeenCalled());
    await waitFor(() => expect(container).toBeEmptyDOMElement());
  });

  it('never reads as an all-clear when it could not be read at all', async () => {
    listPatientAlerts.mockRejectedValue(new Error('offline'));

    renderWithProviders(<AlertBanner patientId="patient-1" />);

    expect(await screen.findByText(/could not be read/)).toBeInTheDocument();
    expect(screen.getByText(/all-clear/)).toBeInTheDocument();
  });
});
