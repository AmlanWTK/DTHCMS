import { screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from './render';
import { useSessionStore, type SessionUser } from '@/stores/session';

/**
 * The dashboard's opening sentence, per reader (CP85).
 *
 * It said, to everybody, "the snapshot, the clinical summary and the AI assistant on one
 * screen". Six roles reach this page and only two of them — the consultant and the junior
 * doctor — hold `ai.synthesis.read`. The nutritionist, the exercise specialist, the history
 * officer and QA were promised two panels the server then withheld, and the withholding is
 * correct: §4.4 decides it and `WithheldNote` says so plainly where the panel would be.
 *
 * That combination is the failure worth a test. An honest refusal preceded by a promise reads
 * as a fault in the software rather than as a rule about the reader, and a clinician who
 * believes the screen is broken stops reading the parts of it that are working.
 *
 * The picker below is stubbed because this is a test about one sentence; what it lists is
 * `PatientPicker`'s business and has its own tests.
 */

vi.mock('next/navigation', () => ({
  useSearchParams: () => new URLSearchParams(),
  usePathname: () => '/dashboard',
  useRouter: () => ({ push: vi.fn(), replace: vi.fn(), refresh: vi.fn() }),
}));

vi.mock('@/features/dashboard', () => ({
  PatientPicker: () => <div data-testid="picker" />,
  PhysicianDashboard: () => <div data-testid="dashboard" />,
}));

const { default: DashboardPage } = await import('@/app/(clinical)/dashboard/page');

const base: SessionUser = {
  id: '0190a8f2-0000-7000-8000-00000000000a',
  employeeCode: 'E001',
  nameEN: 'Test',
  nameBN: 'পরীক্ষা',
  facilityId: '11111111-1111-4111-8111-111111111111',
  roles: [],
  grants: {},
  permissions: [],
  secondFactor: { required: false, enrolled: false, pending: false, recoveryCodesLeft: 0 },
};

function signedInAs(role: string, held: string[]) {
  useSessionStore.setState({
    status: 'authenticated',
    user: { ...base, roles: [role], grants: { [role]: held }, permissions: held },
    activeRole: role,
  });
}

beforeEach(() => {
  useSessionStore.setState({ status: 'anonymous', user: null, activeRole: null });
});

describe('the lede describes what this reader will actually get', () => {
  it('promises all three panels to a consultant, who is shown all three', () => {
    signedInAs('PHYSICIAN', ['patient.read.clinical', 'ai.synthesis.read']);
    renderWithProviders(<DashboardPage />);
    expect(
      screen.getByText(/the snapshot, the clinical summary and the AI assistant/i),
    ).toBeTruthy();
  });

  it('promises neither the summary nor the assistant to a nutritionist', () => {
    // The defect, as a sentence: `patient.read.clinical` without `ai.synthesis.read`.
    signedInAs('NUTRITIONIST', ['patient.read.clinical', 'observation.write.nutrition']);
    renderWithProviders(<DashboardPage />);

    expect(screen.queryByText(/the AI assistant on one screen/i)).toBeNull();
    const lede = screen.getByText(/are not shown to your role/i);
    expect(lede.textContent).toMatch(/snapshot/i);
    expect(lede.textContent).toMatch(/clinical summary and the AI assistant/i);
  });

  it('promises the same to an exercise specialist, who holds the same pair', () => {
    signedInAs('EXERCISE', ['patient.read.clinical', 'observation.write.exercise']);
    renderWithProviders(<DashboardPage />);
    expect(screen.queryByText(/the AI assistant on one screen/i)).toBeNull();
    expect(screen.getByText(/are not shown to your role/i)).toBeTruthy();
  });

  it('promises only what the station may read when the clinical read is not held', () => {
    signedInAs('REGISTRATION', ['patient.read.demographics']);
    renderWithProviders(<DashboardPage />);
    expect(screen.getByText(/what your station may read/i)).toBeTruthy();
  });
});
