import { screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { RenalStatus } from '@/features/prescriptions/api/renal';

import { renderWithProviders } from './render';

/**
 * The renal status indicator, on screen (CP79).
 *
 * The engine's correctness is proven in Go. What can only be proven here is whether the
 * prescriber reading the screen ends up believing the right thing — and every way that fails
 * is quiet:
 *
 *  - **An absent eGFR drawn quietly.** "There is no kidney function on file" is not a milder
 *    version of "the kidney function is old", and a blank or muted indicator reads as
 *    *probably fine*. It is the loudest state here, with a word and an `alert` role.
 *  - **A stale eGFR drawn as current.** The number is still shown — it is the best
 *    information there is, and hiding it would leave the physician with less than he had —
 *    but the age is on the screen and the state word says *out of date*.
 *  - **The six-month window drawn as settled.** Nobody has approved it. While that is true
 *    the indicator says so, in as many words.
 *  - **A GFR category read as a diagnosis.** One eGFR of 52 is a G3a number, not chronic
 *    kidney disease, and the screen says which it is.
 *  - **A failed read drawn as an empty box**, which reads as "checked, nothing found".
 */

const getRenalStatus = vi.hoisted(() => vi.fn());

vi.mock('@/features/prescriptions/api/renal', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/prescriptions/api/renal')>()),
  getRenalStatus,
}));

const { RenalIndicator } = await import('@/features/prescriptions/components/RenalIndicator');
const surface = await import('@/features/prescriptions');
const { useSessionStore } = await import('@/stores/session');
const initialSession = useSessionStore.getInitialState();

const PATIENT = '0190a8f2-0000-7000-8000-0000000000b7';

function status(over: Partial<RenalStatus> = {}): RenalStatus {
  return {
    known: true,
    egfr: 41.4,
    egfr_as_of: '2026-07-31T04:00:00Z',
    age_days: 43,
    stale: false,
    expires_at: '2027-01-31T04:00:00Z',
    stage: 'G3b',
    stage_label_en: 'G3b — moderately to severely reduced',
    stage_label_bn: 'G3b — মাঝারি থেকে বেশি কম',
    policy: {
      recency_months: 6,
      approved: false,
      source_citation: 'DTHCMS implementation plan CP79',
    },
    summary_en:
      'eGFR 41.4 mL/min/1.73m², G3b — moderately to severely reduced, taken on 31 July 2026 ' +
      '(43 days ago). Within the clinic’s 6-month window.',
    summary_bn:
      'eGFR 41.4 mL/min/1.73m², G3b — মাঝারি থেকে বেশি কম, নেওয়া হয়েছে 31 July 2026 তারিখে ' +
      '(43 দিন আগে)। ক্লিনিকের 6 মাসের সীমার মধ্যে।',
    ...over,
  } as RenalStatus;
}

function signIn(permissions: string[]) {
  useSessionStore.setState({
    ...initialSession,
    status: 'authenticated',
    user: {
      id: '0190a8f2-0000-7000-8000-0000000000d1',
      employeeCode: 'DOC01',
      nameEN: 'Nahid',
      nameBN: 'নাহিদ',
      facilityId: '11111111-1111-4111-8111-111111111111',
      roles: ['PHYSICIAN'],
      grants: { PHYSICIAN: permissions },
      permissions,
      secondFactor: { required: false, enrolled: false, pending: false, recoveryCodesLeft: 0 },
    },
    activeRole: 'PHYSICIAN',
  });
}

beforeEach(() => {
  getRenalStatus.mockReset();
  signIn(['medication.safety.check']);
});

afterEach(() => {
  useSessionStore.setState(initialSession, true);
});

describe('the renal indicator', () => {
  it('shows the eGFR with its date, which is CP79 criterion 1', () => {
    renderWithProviders(<RenalIndicator patientId={PATIENT} status={status()} />);

    const card = screen.getByTestId('renal-indicator');
    expect(card.dataset.tone).toBe('current');
    expect(card.dataset.stage).toBe('G3b');
    expect(screen.getByTestId('renal-egfr')).toHaveTextContent('41.4');
    // The date is in the sentence the server wrote, and the sentence is on the screen.
    expect(screen.getByTestId('renal-summary')).toHaveTextContent('31 July 2026');
    expect(screen.getByTestId('renal-summary')).toHaveTextContent('43 days ago');
    // Given a status, the component performs no I/O at all.
    expect(getRenalStatus).not.toHaveBeenCalled();
  });

  it('draws a patient with no eGFR as the loudest state, not as a blank', () => {
    // The failure this checkpoint exists to prevent: a prescriber reading an empty
    // indicator as "probably fine".
    renderWithProviders(
      <RenalIndicator
        patientId={PATIENT}
        status={status({
          known: false,
          egfr: undefined,
          egfr_as_of: undefined,
          age_days: undefined,
          expires_at: undefined,
          stage: undefined,
          stage_label_en: undefined,
          stage_label_bn: undefined,
          summary_en:
            'No eGFR is on file for this patient. Nothing on this prescription has been ' +
            'checked against kidney function.',
          summary_bn:
            'এই রোগীর কোনো eGFR নথিতে নেই। এই ব্যবস্থাপত্রের কিছুই কিডনির কার্যকারিতার সঙ্গে মিলিয়ে দেখা হয়নি।',
        })}
      />,
    );

    const card = screen.getByTestId('renal-indicator');
    expect(card.dataset.tone).toBe('unknown');
    // A word, before any colour.
    expect(card).toHaveTextContent('No kidney function on file');
    // Interrupting is right here and nowhere else.
    expect(card).toHaveAttribute('role', 'alert');
    expect(screen.queryByTestId('renal-egfr')).not.toBeInTheDocument();
    expect(screen.getByTestId('renal-summary')).toHaveTextContent('has been checked');
  });

  it('still shows a stale eGFR, and says it is out of date', () => {
    // Hiding the number would leave the physician with less than he had. The renal rules
    // run against it; what changes is that the age is on the screen.
    renderWithProviders(
      <RenalIndicator
        patientId={PATIENT}
        status={status({
          stale: true,
          age_days: 425,
          summary_en:
            'eGFR 41.4 mL/min/1.73m², G3b, taken on 15 July 2025 (425 days ago). This is ' +
            'older than the clinic’s 6-month window and is not current renal function.',
        })}
      />,
    );

    const card = screen.getByTestId('renal-indicator');
    expect(card.dataset.tone).toBe('stale');
    expect(card).toHaveTextContent('Out of date');
    expect(screen.getByTestId('renal-egfr')).toHaveTextContent('41.4');
    expect(screen.getByTestId('renal-summary')).toHaveTextContent('425 days ago');
    expect(screen.getByTestId('renal-summary')).toHaveTextContent('not current renal function');
  });

  it('says the six-month window is nobody’s decision yet', () => {
    renderWithProviders(<RenalIndicator patientId={PATIENT} status={status()} />);
    expect(screen.getByTestId('renal-provisional')).toHaveTextContent(
      'No physician has approved it yet',
    );
  });

  it('stops saying provisional once a physician has approved the window', () => {
    // The other half. Without this, the banner could be hardcoded and the test above would
    // still pass.
    renderWithProviders(
      <RenalIndicator
        patientId={PATIENT}
        status={status({ policy: { recency_months: 6, approved: true, source_citation: 'KDIGO' } })}
      />,
    );
    expect(screen.queryByTestId('renal-provisional')).not.toBeInTheDocument();
  });

  it('says a GFR category is not a diagnosis', () => {
    renderWithProviders(<RenalIndicator patientId={PATIENT} status={status()} />);
    expect(screen.getByTestId('renal-indicator')).toHaveTextContent(
      'not a diagnosis of chronic kidney disease',
    );
  });

  it('renders the server’s Bengali sentence when the locale is Bangla', () => {
    // Both languages come from the engine, so the sentence the findings cite and the
    // sentence on the screen cannot drift.
    renderWithProviders(<RenalIndicator patientId={PATIENT} status={status()} />, {
      locale: 'bn',
    });
    expect(screen.getByTestId('renal-summary')).toHaveTextContent('মাসের সীমার মধ্যে');
    expect(screen.getByTestId('renal-indicator')).toHaveTextContent('বর্তমান');
  });

  it('says so when the status cannot be read, rather than drawing an empty box', async () => {
    getRenalStatus.mockRejectedValue(new Error('down'));
    renderWithProviders(<RenalIndicator patientId={PATIENT} />);
    expect(await screen.findByTestId('renal-unreadable')).toHaveTextContent(
      'Do not read that as normal kidney function',
    );
  });

  it('shows nothing at all to somebody who may not read it', () => {
    signIn([]);
    const { container } = renderWithProviders(<RenalIndicator patientId={PATIENT} />);
    expect(container).toBeEmptyDOMElement();
    expect(getRenalStatus).not.toHaveBeenCalled();
  });

  it('exports one answer to “may this be drawn as reassurance”', () => {
    // A second derivation goes wrong by treating !stale as sufficient, which is true for a
    // patient with no eGFR at all.
    expect(surface.isCurrentRenalFunction(status())).toBe(true);
    expect(surface.isCurrentRenalFunction(status({ stale: true }))).toBe(false);
    expect(surface.isCurrentRenalFunction(status({ known: false, stale: false }))).toBe(false);
    expect(surface.renalTone(status({ known: false, stale: false }))).toBe('unknown');
  });
});
