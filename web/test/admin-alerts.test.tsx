import { act, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from './render';
import type { AdminAlert } from '@/features/audit/api/audit';
import type { SessionUser } from '@/stores/session';

/**
 * The alarm every administrator's console carries (CP22, criterion 3).
 *
 * Somebody has broken the glass on a patient record, or the audit chain no longer
 * verifies. Both are things a person has to be told about within a minute, wherever in
 * the console they happen to be standing — which is why this component polls rather than
 * waiting for the realtime gateway, and why the poll interval is half the criterion's
 * allowance rather than all of it.
 *
 * What is checked here is the alarm's failure modes, which are quiet ones:
 *
 *  - it must never appear for somebody whose role does not hold `audit.read`, and must
 *    not even ask — a poll the server would refuse, every thirty seconds, forever;
 *  - a break-glass and a broken chain are different emergencies and must not share a
 *    sentence;
 *  - the sentence is the server's, in the language the console is being read in. A
 *    Bangla-reading administrator handed an English alarm has to translate before acting;
 *  - acknowledging is a claim that a person has seen this. A failed acknowledgement that
 *    cleared the banner anyway would erase the alarm without anyone having seen it, and
 *    nothing on screen would ever mention it again;
 *  - a failed poll is not an alarm. A clinic's wifi drops; an alarm that fired on every
 *    dropped request is an alarm people learn to close without reading.
 */

const listAlerts = vi.hoisted(() => vi.fn());
const acknowledgeAlert = vi.hoisted(() => vi.fn());

vi.mock('@/features/audit/api/audit', () => ({ listAlerts, acknowledgeAlert }));

const { AdminAlerts, ALERT_POLL_MS } = await import('@/features/audit/components/AdminAlerts');
const { useSessionStore } = await import('@/stores/session');

const breakGlass: AdminAlert = {
  id: '0190a8f2-0000-7000-8000-0000000000a1',
  kind: 'break_glass',
  severity: 'high',
  message_en: 'Dr Karim opened emergency access to V-2026-0914-017.',
  message_bn: 'ডা. করিম V-2026-0914-017 রেকর্ডে জরুরি প্রবেশাধিকার নিয়েছেন।',
  reference: {},
  audit_seq: 4103,
  created_at: '2026-09-14T04:43:00Z',
};

const chainBroken: AdminAlert = {
  ...breakGlass,
  id: '0190a8f2-0000-7000-8000-0000000000a2',
  kind: 'chain_broken',
  message_en: 'The audit chain does not verify from entry 3990.',
  message_bn: '৩৯৯০ নম্বর এন্ট্রি থেকে অডিট চেইন মিলছে না।',
  audit_seq: 3990,
};

function person(grants: Record<string, string[]>): SessionUser {
  return {
    id: '0190a8f2-0000-7000-8000-00000000000a',
    employeeCode: 'A001',
    nameEN: 'Administrator',
    nameBN: 'প্রশাসক',
    facilityId: '11111111-1111-4111-8111-111111111111',
    roles: Object.keys(grants),
    grants,
    permissions: [...new Set(Object.values(grants).flat())],
    secondFactor: { required: true, enrolled: true, pending: false, recoveryCodesLeft: 10 },
  };
}

const initialSession = useSessionStore.getState();

/** An administrator wearing the hat that holds `audit.read`. */
function signInAsAdministrator() {
  useSessionStore.setState({
    status: 'authenticated',
    user: person({ ADMIN: ['audit.read', 'user.read'] }),
    activeRole: 'ADMIN',
  });
}

beforeEach(() => {
  useSessionStore.setState(initialSession, true);
  listAlerts.mockResolvedValue([]);
  acknowledgeAlert.mockResolvedValue(breakGlass);
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.clearAllMocks();
});

describe('who the alarm is for', () => {
  it('is silent, and does not poll, for a role that cannot read the trail', async () => {
    // Not merely hidden. A console polling an endpoint its role is refused on produces a
    // 403 every thirty seconds in the logs of a system whose logs are the evidence.
    useSessionStore.setState({
      status: 'authenticated',
      user: person({ NUTRITIONIST: ['observation.write.nutrition'] }),
      activeRole: 'NUTRITIONIST',
    });
    listAlerts.mockResolvedValue([breakGlass]);

    const { container } = renderWithProviders(<AdminAlerts />);

    await waitFor(() => expect(listAlerts).not.toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it('is silent for a signed-out console', async () => {
    renderWithProviders(<AdminAlerts />);

    await waitFor(() => expect(listAlerts).not.toHaveBeenCalled());
    expect(screen.queryByRole('region', { name: 'Administrator alerts' })).not.toBeInTheDocument();
  });

  it('takes up no room on an administrator’s screen when nothing is wrong', async () => {
    // A permanent empty container would push every console down by its own margin, which
    // is how a component that is meant to be exceptional becomes furniture.
    signInAsAdministrator();

    const { container } = renderWithProviders(<AdminAlerts />);

    await waitFor(() => expect(listAlerts).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });
});

describe('what the alarm says', () => {
  beforeEach(signInAsAdministrator);

  it('tells a break-glass and a broken chain apart', async () => {
    // One is a person doing something legitimate that must be reviewed; the other is the
    // record itself being untrustworthy. They call for different people.
    listAlerts.mockResolvedValue([breakGlass, chainBroken]);

    renderWithProviders(<AdminAlerts />);

    expect(await screen.findByText('Someone has broken the glass')).toBeInTheDocument();
    expect(screen.getByText('The audit chain failed verification')).toBeInTheDocument();
  });

  it('carries the server’s own sentence, so the administrator knows who and what', async () => {
    listAlerts.mockResolvedValue([breakGlass]);

    renderWithProviders(<AdminAlerts />);

    expect(
      await screen.findByText('Dr Karim opened emergency access to V-2026-0914-017.'),
    ).toBeInTheDocument();
  });

  it('interrupts, because a break-glass is not a notice to read later', async () => {
    listAlerts.mockResolvedValue([breakGlass]);

    renderWithProviders(<AdminAlerts />);

    // `alert`, not `status`: assertive is correct here and wrong for almost everything
    // else. And the whole group is a landmark, so it can be jumped to by name.
    const banner = await screen.findByRole('alert');
    expect(banner).toHaveTextContent('Someone has broken the glass');
    expect(screen.getByRole('region', { name: 'Administrator alerts' })).toContainElement(banner);
  });

  it('offers the way to the trail beside the way to dismiss it', async () => {
    // Acknowledging is not investigating. The link is what turns the alarm into an action.
    listAlerts.mockResolvedValue([breakGlass]);

    renderWithProviders(<AdminAlerts />);

    const link = await screen.findByRole('link', { name: 'Open the audit trail' });
    expect(link).toHaveAttribute('href', '/admin/audit');
  });

  it('reads in Bangla when the console is in Bangla', async () => {
    // Both halves: the label this application owns, and the sentence the server wrote.
    // An administrator who reads Bangla should not have to translate an alarm to act on it.
    listAlerts.mockResolvedValue([breakGlass]);

    renderWithProviders(<AdminAlerts />, { locale: 'bn' });

    expect(await screen.findByText('কেউ জরুরি প্রবেশাধিকার নিয়েছেন')).toBeInTheDocument();
    expect(
      screen.getByText('ডা. করিম V-2026-0914-017 রেকর্ডে জরুরি প্রবেশাধিকার নিয়েছেন।'),
    ).toBeInTheDocument();
    expect(screen.queryByText(breakGlass.message_en)).not.toBeInTheDocument();
  });
});

describe('acknowledging', () => {
  beforeEach(signInAsAdministrator);

  it('tells the server which alert was seen, and takes only that one down', async () => {
    const user = userEvent.setup();
    listAlerts.mockResolvedValue([breakGlass, chainBroken]);
    renderWithProviders(<AdminAlerts />);
    await screen.findByText('Someone has broken the glass');

    await user.click(screen.getAllByRole('button', { name: 'I have seen this' })[0]!);

    await waitFor(() =>
      expect(screen.queryByText('Someone has broken the glass')).not.toBeInTheDocument(),
    );
    expect(acknowledgeAlert).toHaveBeenCalledWith(breakGlass.id);
    // The other emergency is still an emergency.
    expect(screen.getByText('The audit chain failed verification')).toBeInTheDocument();
  });

  it('keeps the alarm up when the server refused the acknowledgement', async () => {
    // The dangerous version of this bug is silent: the banner disappears, nobody has
    // actually acknowledged anything, and the console never mentions it again.
    const user = userEvent.setup();
    listAlerts.mockResolvedValue([breakGlass]);
    acknowledgeAlert.mockRejectedValue(new Error('refused'));
    renderWithProviders(<AdminAlerts />);
    await screen.findByText('Someone has broken the glass');

    await user.click(screen.getByRole('button', { name: 'I have seen this' }));

    // Re-read from the server rather than trusted from local state: whatever the truth
    // is, it is the server's.
    await waitFor(() => expect(listAlerts).toHaveBeenCalledTimes(2));
    expect(screen.getByText('Someone has broken the glass')).toBeInTheDocument();
  });

  it('accepts one acknowledgement at a time, not one per impatient click', async () => {
    const user = userEvent.setup();
    listAlerts.mockResolvedValue([breakGlass, chainBroken]);
    let settle: (value: AdminAlert) => void = () => {};
    acknowledgeAlert.mockReturnValue(
      new Promise<AdminAlert>((resolve) => {
        settle = resolve;
      }),
    );
    renderWithProviders(<AdminAlerts />);
    await screen.findByText('Someone has broken the glass');

    const buttons = screen.getAllByRole('button', { name: 'I have seen this' });
    await user.click(buttons[0]!);

    // Every button, including the other alert's: while one write is in flight the group
    // is not accepting another.
    await waitFor(() => expect(buttons[0]!).toBeDisabled());
    expect(buttons[1]!).toBeDisabled();
    expect(buttons[0]!).toHaveAttribute('aria-busy', 'true');
    expect(buttons[1]!).not.toHaveAttribute('aria-busy');

    settle(breakGlass);
    await waitFor(() => expect(acknowledgeAlert).toHaveBeenCalledTimes(1));
  });
});

describe('the poll', () => {
  beforeEach(signInAsAdministrator);

  /**
   * Move the clock and let React settle.
   *
   * `waitFor` cannot be used under Vitest's fake timers — it looks for Jest's and, not
   * finding them, waits on timers that will never fire on their own.
   */
  async function tick(ms = 0) {
    await act(async () => {
      await vi.advanceTimersByTimeAsync(ms);
    });
  }

  it('is half the minute the criterion allows', () => {
    // Stated as a number rather than left implicit: the criterion is that an
    // administrator learns of a break-glass within a minute, and the margin is what
    // survives one dropped request.
    expect(ALERT_POLL_MS).toBe(30_000);
  });

  it('brings a break-glass onto a console nobody has touched', async () => {
    // The case the whole component exists for: the administrator is looking at some other
    // page and has clicked nothing.
    vi.useFakeTimers();
    listAlerts.mockResolvedValue([]);
    renderWithProviders(<AdminAlerts />);
    await tick();
    expect(listAlerts).toHaveBeenCalledTimes(1);

    listAlerts.mockResolvedValue([breakGlass]);
    await tick(ALERT_POLL_MS);

    expect(screen.getByText('Someone has broken the glass')).toBeInTheDocument();
  });

  it('stops asking once the console is gone', async () => {
    // An interval left running after unmount keeps a signed-out session's requests going
    // and holds the component's state alive to be written to.
    vi.useFakeTimers();
    const { unmount } = renderWithProviders(<AdminAlerts />);
    await tick();
    expect(listAlerts).toHaveBeenCalledTimes(1);

    unmount();
    await tick(ALERT_POLL_MS * 3);

    expect(listAlerts).toHaveBeenCalledTimes(1);
  });

  it('treats a failed poll as nothing to report, not as an alarm', async () => {
    // Clinic wifi drops. An alarm that fired on a dropped request would be an alarm the
    // administrator learns to close without reading, which is how the real one gets missed.
    listAlerts.mockRejectedValue(new Error('offline'));

    const { container } = renderWithProviders(<AdminAlerts />);

    await waitFor(() => expect(listAlerts).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it('keeps showing the last alarm it knew about when a later poll fails', async () => {
    // Losing the connection is not evidence that the emergency ended.
    vi.useFakeTimers();
    listAlerts.mockResolvedValue([breakGlass]);
    renderWithProviders(<AdminAlerts />);
    await tick();
    expect(screen.getByText('Someone has broken the glass')).toBeVisible();

    listAlerts.mockRejectedValue(new Error('offline'));
    await tick(ALERT_POLL_MS);

    expect(screen.getByText('Someone has broken the glass')).toBeInTheDocument();
  });
});
