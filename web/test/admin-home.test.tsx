import { screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from './render';
import { useSessionStore, type SessionUser } from '@/stores/session';

/**
 * The front door of the administration console (CP21).
 *
 * Rule 1 of docs/access-model.md: the interface hides what the operator cannot do.
 * A card here is an invitation — "invite colleagues, grant and revoke roles, suspend
 * accounts, reset credentials" — and offering it to somebody the server will refuse
 * teaches the clinic that the software is unreliable, which costs attention nobody has
 * spare on a clinic day.
 *
 * The part worth guarding is that the answer is scoped to the role being *worn*, not to
 * everything the person holds. A chief consultant who also administers the system sees
 * the clinical application while wearing the clinical hat; the administrative doors are
 * not standing open behind it, because every request that goes through them is decided by
 * the server for that same active role (CP20, [R-02]).
 */

vi.mock('next/navigation', () => ({
  usePathname: () => '/admin',
  useRouter: () => ({ push: vi.fn(), replace: vi.fn(), refresh: vi.fn() }),
  useSearchParams: () => new URLSearchParams(),
}));

const { AdminHome } = await import('@/features/users');

const GRANTS: Record<string, string[]> = {
  ADMIN: ['user.invite', 'role.grant', 'user.suspend', 'device.enroll', 'device.revoke'],
  HR: ['user.invite', 'user.suspend'],
  STOREKEEPER: ['device.enroll', 'device.revoke'],
  AUDITOR: ['audit.read'],
  PHYSICIAN: ['patient.read.demographics', 'prescription.sign'],
};

function person(roles: string[]): SessionUser {
  return {
    id: '0190a8f2-0000-7000-8000-00000000000a',
    employeeCode: 'E001',
    nameEN: 'Dr Test Administrator',
    nameBN: 'ডা. পরীক্ষা',
    facilityId: '11111111-1111-4111-8111-111111111111',
    roles,
    grants: Object.fromEntries(roles.map((role) => [role, GRANTS[role] ?? []])),
    permissions: [...new Set(roles.flatMap((role) => GRANTS[role] ?? []))],
    secondFactor: { required: true, enrolled: true, pending: false, recoveryCodesLeft: 10 },
  };
}

const initial = useSessionStore.getInitialState();

function wearing(role: string, alsoHolding: string[] = []) {
  useSessionStore.setState({
    ...initial,
    status: 'authenticated',
    user: person([role, ...alsoHolding]),
    activeRole: role,
  });
}

beforeEach(() => {
  useSessionStore.setState(initial, true);
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('the areas the console offers', () => {
  it('shows both doors to an administrator, each pointing at its screen', () => {
    wearing('ADMIN');
    renderWithProviders(<AdminHome />);

    expect(screen.getByRole('link', { name: /^Users/ })).toHaveAttribute('href', '/admin/users');
    expect(screen.getByRole('link', { name: /^Devices/ })).toHaveAttribute(
      'href',
      '/admin/devices',
    );
    expect(
      screen.getByText(
        'Invite colleagues, grant and revoke roles, suspend accounts, and reset credentials.',
      ),
    ).toBeInTheDocument();
  });

  it('shows a human resources officer people and not tablets', () => {
    wearing('HR');
    renderWithProviders(<AdminHome />);

    expect(screen.getByRole('link', { name: /^Users/ })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /^Devices/ })).not.toBeInTheDocument();
  });

  it('shows whoever looks after the tablets the devices, and no door into people', () => {
    wearing('STOREKEEPER');
    renderWithProviders(<AdminHome />);

    expect(screen.getByRole('link', { name: /^Devices/ })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /^Users/ })).not.toBeInTheDocument();
  });

  it('offers a clinician nothing, even though they signed in', () => {
    wearing('PHYSICIAN');
    renderWithProviders(<AdminHome />);

    expect(screen.queryAllByRole('link')).toHaveLength(0);
  });

  it('offers nothing before the session is known', () => {
    // `unknown` is not `anonymous`: the store starts here on every reload, and a card
    // rendered optimistically in that gap is a door drawn for somebody who may not have it.
    renderWithProviders(<AdminHome />);

    expect(screen.queryAllByRole('link')).toHaveLength(0);
  });

  it('answers for the hat being worn, not for every role the person holds', () => {
    // The same account, wearing the clinical role. The administrative permissions are
    // held — and the server would refuse them under this role, so they are not offered.
    wearing('PHYSICIAN', ['ADMIN']);
    renderWithProviders(<AdminHome />);

    expect(screen.queryAllByRole('link')).toHaveLength(0);
  });

  it('opens both doors once the same person puts the administrative hat on', () => {
    wearing('ADMIN', ['PHYSICIAN']);
    renderWithProviders(<AdminHome />);

    expect(screen.queryAllByRole('link')).toHaveLength(2);
  });

  it('offers the audit trail, and only that, to somebody who may only read it', () => {
    // The three cards mirror the three admin entries in the sidebar. An area that appears
    // in one and not the other is a page somebody can only reach by accident — which is
    // what this card was, until the console's landing page was drawing two doors out of
    // three.
    wearing('AUDITOR');
    renderWithProviders(<AdminHome />);

    expect(screen.getByText('Audit trail')).toBeInTheDocument();
    const links = screen.getAllByRole('link');
    expect(links).toHaveLength(1);
    expect(links[0]).toHaveAttribute('href', '/admin/audit');
  });
});
