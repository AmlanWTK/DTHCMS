import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from './render';
import { ConfirmDialog, type ConfirmRequest } from '@/features/users/components/ConfirmDialog';
import { passwordAcceptable } from '@/features/users';

/**
 * "Are you sure, and why" — the one dialog every administrative write goes through (CP21).
 *
 * Two things here are load-bearing. The first is the reason: suspending a colleague,
 * revoking a role or ending somebody's sessions all leave a line in an append-only record,
 * and a line that says only "suspended" three months later is a line nobody can act on.
 * When the caller says a reason is required, the confirming button must stay dead until
 * one is written — the server's CHECK would refuse it anyway, and a refusal arriving after
 * the step-up prompt is a person who has typed an authenticator code for nothing.
 *
 * The second is the secret. Setting a password happens with the colleague standing at the
 * desk; the dialog is where a weak one would be typed under time pressure, so the policy
 * is applied before the button lights and a generated password is one click away.
 *
 * A refusal from the server has to stay on screen with the dialog open. An administrator
 * who sees the dialog vanish concludes the account was suspended.
 */

// jsdom has no <dialog>.showModal. Give it one that toggles `open`, which is all the
// component relies on.
beforeEach(() => {
  HTMLDialogElement.prototype.showModal = function showModal() {
    this.setAttribute('open', '');
  };
  HTMLDialogElement.prototype.close = function close() {
    this.removeAttribute('open');
  };
});

afterEach(() => {
  vi.restoreAllMocks();
});

function suspension(overrides: Partial<ConfirmRequest> = {}): ConfirmRequest {
  return {
    title: 'Suspend Rafiq Hasan?',
    body: 'Sign-in is refused and every open session ends.',
    confirmLabel: 'Suspend',
    destructive: true,
    reasonRequired: true,
    onConfirm: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  };
}

function newPassword(overrides: Partial<ConfirmRequest> = {}): ConfirmRequest {
  return {
    title: 'Set a new password for Rafiq Hasan',
    confirmLabel: 'Set the password',
    onConfirm: vi.fn().mockResolvedValue(undefined),
    secret: {
      label: 'New password',
      hint: 'At least 12 characters.',
      acceptable: passwordAcceptable,
      suggest: () => 'khpq-2rst-uvwx-yzab',
    },
    ...overrides,
  };
}

const confirmButton = () => screen.getByRole('button', { name: 'Suspend' });

describe('the dialog itself', () => {
  it('shows nothing at all until there is something to confirm', () => {
    renderWithProviders(<ConfirmDialog request={null} onCancel={vi.fn()} />);

    expect(screen.queryByRole('button')).not.toBeInTheDocument();
    expect(screen.queryByText('Reason')).not.toBeInTheDocument();
  });

  it("puts the caller's words on screen and draws a destructive act as dangerous", () => {
    renderWithProviders(<ConfirmDialog request={suspension()} onCancel={vi.fn()} />);

    expect(screen.getByRole('heading', { name: 'Suspend Rafiq Hasan?' })).toBeInTheDocument();
    expect(screen.getByText('Sign-in is refused and every open session ends.')).toBeInTheDocument();
    expect(confirmButton()).toHaveAttribute('data-variant', 'danger');
  });

  it('closes on cancel without doing anything', async () => {
    const onCancel = vi.fn();
    const request = suspension();
    const user = userEvent.setup();
    renderWithProviders(<ConfirmDialog request={request} onCancel={onCancel} />);

    await user.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(onCancel).toHaveBeenCalled();
    expect(request.onConfirm).not.toHaveBeenCalled();
  });

  it('forgets what was typed when the next confirmation opens', async () => {
    // Otherwise the reason given for suspending one colleague is prefilled as the reason
    // for deactivating the next.
    const user = userEvent.setup();
    const { rerender } = renderWithProviders(
      <ConfirmDialog request={suspension()} onCancel={vi.fn()} />,
    );
    await user.type(screen.getByLabelText(/^Reason/), 'left without notice');

    rerender(<ConfirmDialog request={null} onCancel={vi.fn()} />);
    rerender(<ConfirmDialog request={suspension()} onCancel={vi.fn()} />);

    expect(screen.getByLabelText(/^Reason/)).toHaveValue('');
  });
});

describe('the reason', () => {
  it('holds the button dead until a suspension has one worth keeping', async () => {
    const user = userEvent.setup();
    renderWithProviders(<ConfirmDialog request={suspension()} onCancel={vi.fn()} />);

    expect(
      screen.getByText('Kept with the record. At least three characters.'),
    ).toBeInTheDocument();
    expect(confirmButton()).toBeDisabled();

    await user.type(screen.getByLabelText(/^Reason/), 'ab');
    expect(confirmButton()).toBeDisabled();

    await user.type(screen.getByLabelText(/^Reason/), 'sent');
    expect(confirmButton()).toBeEnabled();
  });

  it('counts spaces as nothing, because "   " is not an explanation', async () => {
    const user = userEvent.setup();
    renderWithProviders(<ConfirmDialog request={suspension()} onCancel={vi.fn()} />);

    await user.type(screen.getByLabelText(/^Reason/), '    ');

    expect(confirmButton()).toBeDisabled();
  });

  it('lets an act that does not need one through with the field empty', () => {
    const request = suspension({ reasonRequired: false });
    renderWithProviders(<ConfirmDialog request={request} onCancel={vi.fn()} />);

    expect(screen.getByText('Optional, but kept with the record if given.')).toBeInTheDocument();
    expect(confirmButton()).toBeEnabled();
  });

  it('hands the caller the reason trimmed', async () => {
    const request = suspension();
    const user = userEvent.setup();
    renderWithProviders(<ConfirmDialog request={request} onCancel={vi.fn()} />);

    await user.type(screen.getByLabelText(/^Reason/), '  left without notice  ');
    await user.click(confirmButton());

    expect(request.onConfirm).toHaveBeenCalledWith({
      reason: 'left without notice',
      secret: '',
    });
  });
});

describe('the secret, when the act sets one', () => {
  it('refuses a password the server would refuse, before anybody types a code', async () => {
    const request = newPassword();
    const user = userEvent.setup();
    renderWithProviders(<ConfirmDialog request={request} onCancel={vi.fn()} />);

    const confirm = screen.getByRole('button', { name: 'Set the password' });
    expect(confirm).toBeDisabled();

    await user.type(screen.getByLabelText(/^New password/), 'short');
    expect(confirm).toBeDisabled();

    await user.clear(screen.getByLabelText(/^New password/));
    await user.type(screen.getByLabelText(/^New password/), 'twelve chars');
    expect(confirm).toBeEnabled();
  });

  it('offers a generated one, so nobody invents a weak password at the desk', async () => {
    const request = newPassword();
    const user = userEvent.setup();
    renderWithProviders(<ConfirmDialog request={request} onCancel={vi.fn()} />);

    await user.click(screen.getByRole('button', { name: 'Generate' }));

    expect(screen.getByLabelText(/^New password/)).toHaveValue('khpq-2rst-uvwx-yzab');
    await user.click(screen.getByRole('button', { name: 'Set the password' }));
    expect(request.onConfirm).toHaveBeenCalledWith({
      reason: '',
      secret: 'khpq-2rst-uvwx-yzab',
    });
  });

  it('offers no generator when the caller has nothing to suggest', () => {
    const request = newPassword({
      secret: {
        label: 'New password',
        hint: 'At least 12 characters.',
        acceptable: passwordAcceptable,
      },
    });
    renderWithProviders(<ConfirmDialog request={request} onCancel={vi.fn()} />);

    expect(screen.queryByRole('button', { name: 'Generate' })).not.toBeInTheDocument();
  });
});

describe('when the server refuses', () => {
  it("keeps the dialog open and shows the server's sentence", async () => {
    const request = suspension({
      onConfirm: vi.fn().mockRejectedValue(new Error('Confirm it is you and try again.')),
    });
    const user = userEvent.setup();
    renderWithProviders(<ConfirmDialog request={request} onCancel={vi.fn()} />);

    await user.type(screen.getByLabelText(/^Reason/), 'left without notice');
    await user.click(confirmButton());

    expect(await screen.findByText('Confirm it is you and try again.')).toBeInTheDocument();
    // Still open, still asking: the account was not suspended.
    expect(confirmButton()).toBeEnabled();
    expect(screen.getByLabelText(/^Reason/)).toHaveValue('left without notice');
  });

  it('says something of its own when the failure carries no sentence', async () => {
    const request = suspension({ onConfirm: vi.fn().mockRejectedValue('nothing readable') });
    const user = userEvent.setup();
    renderWithProviders(<ConfirmDialog request={request} onCancel={vi.fn()} />);

    await user.type(screen.getByLabelText(/^Reason/), 'left without notice');
    await user.click(confirmButton());

    expect(await screen.findByText('That did not complete.')).toBeInTheDocument();
  });

  it('locks the form while the write is in flight, so nothing is suspended twice', async () => {
    const request = suspension({ onConfirm: vi.fn().mockReturnValue(new Promise<void>(() => {})) });
    const user = userEvent.setup();
    renderWithProviders(<ConfirmDialog request={request} onCancel={vi.fn()} />);

    await user.type(screen.getByLabelText(/^Reason/), 'left without notice');
    await user.click(confirmButton());

    expect(confirmButton()).toHaveAttribute('aria-busy', 'true');
    expect(confirmButton()).toBeDisabled();
    expect(screen.getByLabelText(/^Reason/)).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled();
    expect(request.onConfirm).toHaveBeenCalledTimes(1);
  });
});
