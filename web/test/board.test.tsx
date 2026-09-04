import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from './render';

const readBoard = vi.hoisted(() => vi.fn());
const reroute = vi.hoisted(() => vi.fn());

vi.mock('@/features/board/api/board', () => ({ readBoard, reroute }));
vi.mock('@/features/realtime', () => ({ useRealtimeTopics: () => undefined }));

const { TrafficBoard } = await import('@/features/board/components/TrafficBoard');
const { useSessionStore } = await import('@/stores/session');

/**
 * The Clinic Traffic Control board (CP40, §5.2).
 *
 * The board's own privacy property is proven server-side, where it belongs: the payload has
 * no name and no patient id, enforced by a database view and a Go test. What this file
 * checks is the half that is genuinely the interface's: that the heat a supervisor acts on
 * is legible without relying on colour, that the reroute the board offers is a proposal and
 * not an action, and that a 409 reads as "the board moved on" rather than as a failure.
 */

const FACILITY = '11111111-1111-4111-8111-111111111111';

function station(over: Partial<Record<string, unknown>> = {}) {
  return {
    station_code: 'STN_EXAMINATION',
    position: 5,
    heat: 'bottleneck',
    waiting: 8,
    called: 1,
    in_service: 1,
    longest_wait_seconds: 1_800,
    entries: [
      {
        entry_id: 'e1',
        visit_id: 'v1',
        label: 'V-2026-0914-017',
        status: 'waiting',
        priority: 5,
        flagged: true,
        counseling_done: true,
        waited_seconds: 1_800,
      },
    ],
    ...over,
  };
}

function board(over: Partial<Record<string, unknown>> = {}) {
  return {
    day: '2026-09-14',
    generated_at: '2026-09-14T04:42:00Z',
    settings: {
      identify_by: 'code',
      busy_wait_seconds: 900,
      busy_depth: 4,
      bottleneck_wait_seconds: 1_800,
      bottleneck_depth: 7,
    },
    stations: [station()],
    suggestions: [
      {
        entry_id: 'e1',
        label: 'V-2026-0914-017',
        from: 'STN_EXAMINATION',
        to: 'STN_NUTRITION',
        waited_seconds: 1_800,
        from_waiting: 8,
      },
    ],
    waiting_total: 8,
    in_building_total: 10,
    ...over,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  useSessionStore.setState({
    status: 'authenticated',
    user: {
      id: 'u1',
      employeeCode: 'R001',
      nameEN: 'Registration Officer',
      nameBN: 'নিবন্ধন কর্মকর্তা',
      facilityId: FACILITY,
      roles: ['REGISTRATION'],
      grants: { REGISTRATION: ['board.read', 'visit.reroute'] },
      permissions: ['board.read', 'visit.reroute'],
      secondFactor: { required: false, enrolled: false, pending: false, recoveryCodesLeft: 0 },
    },
    activeRole: 'REGISTRATION',
  });
  readBoard.mockResolvedValue(board());
});

describe('the traffic board', () => {
  it('says a station is backed up in words, not only in colour', async () => {
    // Criterion 3's real requirement. Red alone fails for the roughly one in twelve men who
    // will work in this clinic, and it fails completely on a projector whose lamp has been
    // on for three years — which is the actual failure mode of a screen on a wall.
    renderWithProviders(<TrafficBoard />);

    expect(await screen.findByText('Backed up')).toBeInTheDocument();
    expect(screen.getByTestId('board-bottleneck-summary')).toHaveTextContent('Examination');
  });

  it('shows that a patient is prioritised and never why', async () => {
    renderWithProviders(<TrafficBoard />);

    expect(await screen.findByLabelText('Seen first')).toBeInTheDocument();
    // The reason is not in the payload at all; this asserts the screen does not invent one.
    expect(screen.queryByText(/glucose/i)).toBeNull();
  });

  it('offers a suggestion rather than applying one', async () => {
    // "Suggested reroutes with one-tap application" — the tap is load-bearing. A board that
    // moved patients on its own would be a board nobody could explain to a patient asking
    // why they were sent somewhere else.
    renderWithProviders(<TrafficBoard />);

    expect(
      await screen.findByText('Examination has 8 waiting; Nutrition is free'),
    ).toBeInTheDocument();
    expect(reroute).not.toHaveBeenCalled();
  });

  it('requires a destination and a reason before it will move anybody', async () => {
    const user = userEvent.setup();
    renderWithProviders(<TrafficBoard />);

    // The suggestion's button, not the column's: applying a suggestion arrives with the
    // destination and the reason already filled in, which is what makes it one tap.
    await user.click(await screen.findByRole('button', { name: 'Apply' }));
    expect(await screen.findByRole('button', { name: 'Move the patient' })).toBeEnabled();

    await user.clear(screen.getByLabelText(/Why/));
    await user.type(screen.getByLabelText(/Why/), 'busy');
    expect(screen.getByRole('button', { name: 'Move the patient' })).toBeDisabled();
  });

  it('applies a reroute and refreshes', async () => {
    const user = userEvent.setup();
    reroute.mockResolvedValue(undefined);
    renderWithProviders(<TrafficBoard />);

    await user.click(await screen.findByRole('button', { name: 'Apply' }));
    await user.click(await screen.findByRole('button', { name: 'Move the patient' }));

    await waitFor(() =>
      expect(reroute).toHaveBeenCalledWith(
        'e1',
        'STN_NUTRITION',
        'Examination has 8 waiting; Nutrition is free',
      ),
    );
  });

  it('reads a 409 as the board having moved on, not as a failure', async () => {
    // Two supervisors on the same board, one a second slower. Telling the second one "that
    // failed" would send them to look for a bug; telling them the patient has moved is what
    // happened.
    const user = userEvent.setup();
    const { ApiError } = await import('@dthcms/api-client');
    reroute.mockRejectedValue(
      new ApiError({
        status: 409,
        code: 'CONFLICT',
        kind: 'conflict',
        messageEN: 'That patient has already moved on.',
        messageBN: 'সেই রোগী ইতিমধ্যে চলে গেছেন।',
        correlationID: 'req_1',
      }),
    );
    renderWithProviders(<TrafficBoard />);

    await user.click(await screen.findByRole('button', { name: 'Apply' }));
    await user.click(await screen.findByRole('button', { name: 'Move the patient' }));

    expect(await screen.findByText(/already moved on/i)).toBeInTheDocument();
  });

  it('drops every control on the wall display', async () => {
    // The screen in the waiting area has nothing to click, because somebody will lean on it.
    renderWithProviders(<TrafficBoard density="wall" />);

    await screen.findByText('Backed up');
    expect(screen.queryByRole('button', { name: /^Move/ })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Apply' })).toBeNull();
    expect(screen.queryByText('Suggested moves')).toBeNull();
  });

  it('says nobody is waiting rather than drawing an empty grid', async () => {
    readBoard.mockResolvedValue(board({ stations: [], suggestions: [], waiting_total: 0 }));
    renderWithProviders(<TrafficBoard />);

    expect(await screen.findByText('Nobody is waiting')).toBeInTheDocument();
  });

  it('renders in Bangla', async () => {
    renderWithProviders(<TrafficBoard />, { locale: 'bn' });

    expect(await screen.findByText('জট')).toBeInTheDocument();
    expect(screen.getByText('শারীরিক পরীক্ষা')).toBeInTheDocument();
  });
});
