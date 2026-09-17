import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { QAReviewPage } from '@/features/qa';

import { renderWithProviders } from './render';

/**
 * What station 10's screen tells the person in front of it (CP83).
 *
 * The engine is proven in Go and the gate is proven against a database. What can only be proven
 * here is **what a person ends up believing**, and the ways that fails are all quiet:
 *
 *  - **An unchecked file drawn as a clean one.** `rules_live: 0` means this clinic's checklist is
 *    empty and nothing was looked at. A green screen for that is the interface telling an officer
 *    somebody checked when nobody did, which is `docs/qa-rules.md` §1's "theatre".
 *  - **"Would clear" drawn as "has been cleared."** The gate reads the second. A screen that
 *    conflated them would say a prescription may be signed while a trigger refuses the signature.
 *  - **A control drawn for somebody who may not use it.** This is the defect CP92 shipped and a
 *    screenshot found: a consultant handed the whole education form, every button answering 403.
 *    This station has four readers with different rights and so the same mistake is four times as
 *    easy.
 *
 * Every test below goes through the real `usePermission` against the real session store, because
 * the defect being guarded against is a screen that never asked the question — and a test that
 * answered it on the screen's behalf would not have caught it either.
 */

const readQAReview = vi.hoisted(() => vi.fn());
const readQAQueue = vi.hoisted(() => vi.fn());
const clearPrescription = vi.hoisted(() => vi.fn());
const bouncePrescription = vi.hoisted(() => vi.fn());
const overrideQAGate = vi.hoisted(() => vi.fn());

vi.mock('@/features/qa/api/qa', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/qa/api/qa')>()),
  readQAReview,
  readQAQueue,
  clearPrescription,
  bouncePrescription,
  overrideQAGate,
}));

const { QAReviewScreen } = await import('@/features/qa/components/QAReviewScreen');
const { useSessionStore } = await import('@/stores/session');
const { StepUpProvider } = await import('@/features/auth');

/**
 * Renders the screen inside the provider the shell mounts.
 *
 * `useStepUp` throws outside it, deliberately — a screen that asked for a second factor with no
 * dialog to show would fail silently at the moment somebody needed it. So the tests mount the
 * real provider rather than stubbing the hook, which also means the override path below is
 * exercised through the same door the application uses.
 */
function renderScreen(locale: 'en' | 'bn' = 'en') {
  return renderWithProviders(
    <StepUpProvider>
      <QAReviewScreen prescriptionId={SHEET} />
    </StepUpProvider>,
    { locale },
  );
}

const initialSession = useSessionStore.getInitialState();
const SHIRIN = '0190d820-0000-7000-8000-000000009911';
const NAHID = '0190d820-0000-7000-8000-000000009912';
const SHEET = '0190a8f2-0000-7000-8000-0000000000c1';

function holding(role: string, permissions: string[]) {
  useSessionStore.setState({
    ...initialSession,
    status: 'authenticated',
    user: {
      id: role === 'PHYSICIAN' ? NAHID : SHIRIN,
      employeeCode: role === 'PHYSICIAN' ? 'E001' : 'E412',
      nameEN: role === 'PHYSICIAN' ? 'Dr Nahid' : 'Shirin Akhter',
      nameBN: role === 'PHYSICIAN' ? 'ডা. নাহিদ' : 'শিরীন আক্তার',
      facilityId: '11111111-1111-4111-8111-111111111111',
      roles: [role],
      grants: { [role]: permissions },
      permissions,
      secondFactor: { required: false, enrolled: true, pending: false, recoveryCodesLeft: 8 },
    },
    activeRole: role,
  });
}

/** The QA officer: reviews, clears, bounces — and may not override. */
const OFFICER = ['qa.review', 'qa.clear', 'qa.bounce', 'prescription.read'];
/** The consultant: may override, and deliberately may not read the override rate. */
const CONSULTANT = ['qa.override', 'qa.clear', 'prescription.read'];
/** Somebody who may look and do nothing. */
const ONLOOKER = ['qa.review', 'prescription.read'];

const STATIONS = {
  STN_CONSULTATION: ['Physician Consultation', 'চিকিৎসকের পরামর্শ'],
  STN_HISTORY: ['Medical History', 'রোগের ইতিহাস'],
};

/** A blocked file: a diabetic with no HbA1c. Rahima Begum's, from the Go fixtures. */
function blocked(): QAReviewPage {
  return {
    review: {
      prescription_id: SHEET,
      patient_id: '0190a8f2-0000-7000-8000-0000000000p1',
      visit_id: '0190a8f2-0000-7000-8000-0000000000v1',
      at: '2026-09-14T09:30:00Z',
      rules_live: 18,
      stations: STATIONS,
      findings: [
        {
          rule_code: 'DIABETES_HBA1C',
          severity: 'BLOCK',
          title_en: 'Diabetic with no HbA1c recorded or ordered in six months',
          title_bn: 'ডায়াবেটিস রোগী, ছয় মাসে এইচবিএ১সি নেওয়া বা লেখা হয়নি',
          subject_en: 'no HbA1c in the last six months, and none ordered',
          subject_codes: ['HBA1C'],
          subject_bn: 'গত ৬ মাসে কোনো এইচবিএ১সি নেই, এবং কোনোটি দেওয়াও হয়নি',
          bounce_station_code: 'STN_CONSULTATION',
          bounce_station_en: 'Physician Consultation',
          bounce_station_bn: 'চিকিৎসকের পরামর্শ',
        },
        {
          rule_code: 'DIABETES_LIPIDS',
          severity: 'WARN',
          title_en: 'Type 2 diabetic with no lipid profile in twelve months',
          title_bn: 'টাইপ ২ ডায়াবেটিস রোগী, বারো মাসে লিপিড প্রোফাইল হয়নি',
          subject_en: 'no lipid profile in the last year, and none ordered',
          subject_codes: ['CHOL_LDL', 'CHOL_TOTAL'],
          subject_bn: 'গত বছরে কোনো লিপিড প্রোফাইল নেই, এবং কোনোটি দেওয়াও হয়নি',
          bounce_station_code: 'STN_CONSULTATION',
          bounce_station_en: 'Physician Consultation',
          bounce_station_bn: 'চিকিৎসকের পরামর্শ',
        },
      ],
    },
    summary_en: 'One blocking finding and one warning. This file cannot be cleared.',
    summary_bn: '১টি বাধা ও ১টি সতর্কবার্তা। এই ফাইলে ছাড়পত্র দেওয়া যাবে না।',
    can_clear: false,
    clearance_stands: false,
  };
}

/** The state `docs/qa-rules.md` §5 is about: no rules, nothing checked, still not cleared. */
function unchecked(): QAReviewPage {
  return {
    review: {
      prescription_id: SHEET,
      patient_id: '0190a8f2-0000-7000-8000-0000000000p1',
      visit_id: '0190a8f2-0000-7000-8000-0000000000v1',
      at: '2026-09-14T09:30:00Z',
      rules_live: 0,
      stations: STATIONS,
      findings: [],
    },
    summary_en:
      'No QA rules are configured for this clinic, so nothing was checked. Clearance is still ' +
      'required before this prescription can be signed.',
    summary_bn:
      'এই ক্লিনিকের জন্য কোনো কিউএ নিয়ম নির্ধারণ করা নেই, তাই কিছুই যাচাই করা হয়নি। তবু স্বাক্ষরের আগে ছাড়পত্র দিতেই হবে।',
    can_clear: true,
    clearance_stands: false,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  readQAReview.mockResolvedValue(blocked());
  readQAQueue.mockResolvedValue({ queue: [] });
});

describe('the two states that must not look alike', () => {
  it('says in words that an empty rule table checked nothing, and that clearance is still owed', async () => {
    holding('QA', OFFICER);
    readQAReview.mockResolvedValue(unchecked());

    renderScreen();

    // The sentence, from the server, in the officer's own language.
    expect(await screen.findByText(/nothing was checked/i)).toBeInTheDocument();
    expect(screen.getByText(/Clearance is still required/i)).toBeInTheDocument();
    // And the count, said plainly rather than rendered as a reassuring zero.
    expect(screen.getByText(/No rules configured/i)).toBeInTheDocument();
  });

  it('does not say a file is cleared merely because it could be', async () => {
    holding('QA', OFFICER);
    readQAReview.mockResolvedValue(unchecked());

    renderScreen();

    // `can_clear` is true and `clearance_stands` is false: nobody has decided yet, and the gate
    // on the prescription reads the second.
    expect(await screen.findByText(/cannot be signed yet/i)).toBeInTheDocument();
    expect(screen.queryByText(/may be signed/i)).not.toBeInTheDocument();
  });
});

describe('who is handed which controls', () => {
  it('gives the officer the clearance and the bounce, and no override', async () => {
    holding('QA', OFFICER);

    renderScreen();

    expect(await screen.findByTestId('qa-clear')).toBeInTheDocument();
    expect(screen.getByTestId('qa-bounce')).toBeInTheDocument();
    // **The separation this checkpoint is about.** The person watching the override rate must
    // not be the person granting them, so the officer's screen has no override on it at all —
    // absent, not disabled.
    expect(screen.queryByTestId('qa-override')).not.toBeInTheDocument();
  });

  it('gives the consultant the override, and no bounce they do not hold', async () => {
    holding('PHYSICIAN', CONSULTANT);

    renderScreen();

    expect(await screen.findByTestId('qa-override')).toBeInTheDocument();
    expect(screen.queryByTestId('qa-bounce')).not.toBeInTheDocument();
  });

  it('hands an onlooker no control at all, and says why', async () => {
    // The defect CP92 shipped, in this station's shape: a reader who may look being given a
    // form. Every control here would answer 403, and one of them would also be refused by a
    // database trigger — but a screen that draws it has already told this person the act is
    // part of their job.
    holding('QA', ONLOOKER);

    renderScreen();

    expect((await screen.findAllByText(/1 blocking finding/i)).length).toBeGreaterThan(0);
    expect(screen.queryByTestId('qa-clear')).not.toBeInTheDocument();
    expect(screen.queryByTestId('qa-bounce')).not.toBeInTheDocument();
    expect(screen.queryByTestId('qa-override')).not.toBeInTheDocument();
    expect(screen.getByText(/may read this review and not act on it/i)).toBeInTheDocument();
  });
});

describe('what a finding says', () => {
  it('names the drug or the measurement, not only the rule', async () => {
    holding('QA', OFFICER);

    renderScreen();

    // A finding that said only "no HbA1c" would leave the consultation room guessing what to do.
    expect(
      await screen.findByText(/no HbA1c in the last six months, and none ordered/),
    ).toBeInTheDocument();
    // And the room it goes back to, by its own name rather than by its code.
    expect(screen.getAllByText(/Physician Consultation/).length).toBeGreaterThan(0);
    expect(screen.queryByText('STN_CONSULTATION')).not.toBeInTheDocument();
  });

  it('renders the whole screen in Bangla when the officer works in Bangla', async () => {
    holding('QA', OFFICER);

    renderScreen('bn');

    expect(
      await screen.findByText('ডায়াবেটিস রোগী, ছয় মাসে এইচবিএ১সি নেওয়া বা লেখা হয়নি'),
    ).toBeInTheDocument();
    expect(screen.getByText('১টি বাধা ও ১টি সতর্কবার্তা। এই ফাইলে ছাড়পত্র দেওয়া যাবে না।')).toBeInTheDocument();
    // The bounce station, in Bengali. A room name in English on a Bengali screen is a room name
    // half the floor cannot read.
    expect(screen.getAllByText(/চিকিৎসকের পরামর্শ/).length).toBeGreaterThan(0);
  });
});

describe('the warning and its acknowledgement', () => {
  it('will not clear until every warning has been ticked', async () => {
    holding('QA', OFFICER);
    readQAReview.mockResolvedValue({
      ...blocked(),
      review: {
        ...blocked().review,
        findings: blocked().review.findings!.filter((f) => f.severity === 'WARN'),
      },
      summary_en: 'One warning to acknowledge. Nothing blocks this file.',
      can_clear: true,
    });

    renderScreen();

    const clear = await screen.findByTestId('qa-clear');
    // §2: a WARN clears — *with an acknowledgement*. Without the tick it is not a clearance,
    // and a screen that let it through would make WARN and "no rule" the same severity.
    expect(clear).toBeDisabled();

    await userEvent.click(screen.getByRole('checkbox'));
    await waitFor(() => expect(clear).toBeEnabled());

    await userEvent.click(clear);
    await waitFor(() =>
      expect(clearPrescription).toHaveBeenCalledWith(
        SHEET,
        expect.objectContaining({ acknowledged: ['DIABETES_LIPIDS'] }),
      ),
    );
  });

  it('offers no clearance at all on a file with a blocking finding', async () => {
    holding('QA', OFFICER);

    renderScreen();

    expect(await screen.findByTestId('qa-clear')).toBeDisabled();
  });
});

describe('the bounce', () => {
  it('sends the station the officer saw on the screen, not whatever the server would have guessed', async () => {
    holding('QA', OFFICER);

    renderScreen();

    await userEvent.click(await screen.findByTestId('qa-bounce'));
    await waitFor(() =>
      expect(bouncePrescription).toHaveBeenCalledWith(
        SHEET,
        expect.objectContaining({ bounce_station_code: 'STN_CONSULTATION' }),
      ),
    );
  });

  it('lets the officer send it somewhere else, in both languages', async () => {
    holding('QA', OFFICER);

    renderScreen();

    await userEvent.selectOptions(
      await screen.findByLabelText(/Which station/i),
      'STN_HISTORY',
    );
    await userEvent.type(
      screen.getByLabelText(/What is wrong \(English\)/i),
      'Confirm the insulin she is already on.',
    );
    await userEvent.type(
      screen.getByLabelText(/What is wrong \(Bangla\)/i),
      'তিনি যে ইনসুলিন নিচ্ছেন তা নিশ্চিত করুন।',
    );
    await userEvent.click(screen.getByTestId('qa-bounce'));

    await waitFor(() =>
      expect(bouncePrescription).toHaveBeenCalledWith(
        SHEET,
        expect.objectContaining({
          bounce_station_code: 'STN_HISTORY',
          reason_en: 'Confirm the insulin she is already on.',
          reason_bn: 'তিনি যে ইনসুলিন নিচ্ছেন তা নিশ্চিত করুন।',
        }),
      ),
    );
  });
});

describe('the override', () => {
  it('will not send one with no reason', async () => {
    holding('PHYSICIAN', CONSULTANT);

    renderScreen();

    expect(await screen.findByTestId('qa-override')).toBeDisabled();
    await userEvent.type(screen.getByLabelText(/Why this file is going through blocked/i), 'x');
    await waitFor(() => expect(screen.getByTestId('qa-override')).toBeEnabled());
  });

  it('is not offered on a file nothing is blocking', async () => {
    // An override with nothing behind it is a row that makes the rate view lie, and the server
    // refuses it. The screen does not offer it either — a button that exists to be refused is a
    // button that teaches somebody to press it.
    holding('PHYSICIAN', CONSULTANT);
    readQAReview.mockResolvedValue(unchecked());

    renderScreen();

    await screen.findByText(/nothing was checked/i);
    expect(screen.queryByTestId('qa-override')).not.toBeInTheDocument();
  });

  it('says an override already stands rather than offering a second', async () => {
    holding('PHYSICIAN', CONSULTANT);
    const page = blocked();
    readQAReview.mockResolvedValue({
      ...page,
      review: {
        ...page.review,
        override: {
          id: '0190a8f2-0000-7000-8000-0000000000o1',
          prescription_id: SHEET,
          patient_id: page.review.patient_id,
          visit_id: page.review.visit_id,
          granted_at: '2026-09-14T09:40:00Z',
          granted_by: NAHID,
          granted_by_name_en: 'Dr Nahid',
          granted_by_name_bn: 'ডা. নাহিদ',
          reason: 'Lab closed for Eid; the patient travelled from Boalmari.',
          blocking_at_grant: ['DIABETES_HBA1C'],
        },
      },
    });

    renderScreen();

    expect(await screen.findByText(/A consultant override stands/i)).toBeInTheDocument();
    expect(screen.getByText(/Lab closed for Eid/)).toBeInTheDocument();
    expect(screen.queryByTestId('qa-override')).not.toBeInTheDocument();
  });

});

// ---------------------------------------------------------------------------
// The screen is in a language, not in column names
// ---------------------------------------------------------------------------

describe('the words a finding is written in', () => {
  it('shows no internal code as the text of a finding, and keeps it as the detail', async () => {
    holding('QA', OFFICER);
    renderScreen();

    // The regression, asserted against the rendered DOM rather than against a fixture: every
    // visible string, and nothing that looks like `CHOL_LDL` in any of them.
    const findings = await screen.findAllByRole('listitem');
    expect(findings.length).toBeGreaterThan(0);
    for (const item of findings) {
      const visible = item.textContent ?? '';
      // The rule code is deliberately shown in a <code> element beside the title, so it is
      // subtracted before the check: what must not appear is a code inside a *sentence*.
      const sentences = visible.replace(/DIABETES_\w+/g, '');
      expect(sentences).not.toMatch(/[A-Z][A-Z0-9]*_[A-Z0-9_]+/);
    }

    // And the codes are still reachable for whoever is debugging the rule.
    expect(screen.getByTitle('CHOL_LDL, CHOL_TOTAL')).toBeInTheDocument();
  });

  it('renders the lipid rule as one clinical thing rather than as its parts', async () => {
    holding('QA', OFFICER);
    renderScreen();

    expect(
      await screen.findByText(/no lipid profile in the last year, and none ordered/),
    ).toBeInTheDocument();
    expect(screen.queryByText(/CHOL_LDL or CHOL_TOTAL/)).not.toBeInTheDocument();
    // "in the last 1 year" is a machine counting, and it was on this screen.
    expect(screen.queryByText(/in the last 1 year/)).not.toBeInTheDocument();
  });
});
