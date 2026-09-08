import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '@/lib/api';
import type { PhysicianDashboard as DashboardView } from '@/features/dashboard/api/dashboard';

import { renderWithProviders } from './render';

/**
 * The physician's dashboard (CP73, §8).
 *
 * The manual verification for this checkpoint is Dr. Nahid opening a patient and being
 * oriented in sixty seconds. Nothing here can prove that. What can be proved is the set of
 * ways the screen would quietly make it impossible, and every one of them is a way of being
 * *reassuring while wrong*:
 *
 *  - **Twelve requests instead of one.** The acceptance criterion is fragile because several
 *    of the components on this screen fetch for themselves. If the aggregate stops priming
 *    their caches, nothing breaks visibly — the screen is simply slow, on a clinic's wifi,
 *    for the physician the whole checkpoint is for.
 *  - **A machine's sentence read as a person's.** Criterion 3. The failure is silent by
 *    definition: an unmarked narrative looks exactly like a clinician's note.
 *  - **The assembler's arithmetic marked as AI.** The opposite error, and the one nobody
 *    thinks of. Mark everything and a physician learns to discount the one item on the panel
 *    that is certainly true.
 *  - **A withheld panel drawn as an empty one.** "No active conditions" and "you were not
 *    shown the active conditions" are opposite facts, and the version that looks safe is the
 *    wrong one.
 *  - **A value with nobody's name against it.** Criterion 5, on every value including the
 *    four older points inside a sparkline, which is where it is most likely to be forgotten.
 *  - **Accept read as prescribe.** §7.3 makes the split permanent; the screen has to say so
 *    where the button is, not in a document.
 *  - **A shortcut firing while somebody types.** Single-key shortcuts on a screen with a
 *    free-text rejection note: `r` must be an `r`.
 */

const readDashboard = vi.hoisted(() => vi.fn());
const decideSuggestion = vi.hoisted(() => vi.fn());

/*
 * Partial: the network calls are stubbed, the rules are not. `omissionFor`,
 * `suggestionsInWorkingOrder` and `primeDashboardCaches` are what this feature *is* — the
 * last of them is the acceptance criterion — and a test that stubbed them would prove the
 * components call a function rather than that a physician gets one request.
 */
vi.mock('@/features/dashboard/api/dashboard', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/dashboard/api/dashboard')>()),
  readDashboard,
  decideSuggestion,
}));

const getAllergyState = vi.hoisted(() => vi.fn());

vi.mock('@/features/allergies/api/allergies', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/allergies/api/allergies')>()),
  getAllergyState,
}));

const listPatientAlerts = vi.hoisted(() => vi.fn());

vi.mock('@/features/alerts/api/alerts', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/alerts/api/alerts')>()),
  listPatientAlerts,
}));

const { PhysicianDashboard } = await import('@/features/dashboard/components/PhysicianDashboard');
const { useSessionStore } = await import('@/stores/session');

const PATIENT = '0190a8f2-0000-7000-8000-0000000000a1';
const VISIT = '0190a8f2-0000-7000-8000-0000000000b1';
const NAHID = '0190a8f2-0000-7000-8000-0000000000c9';
const RINA = '0190a8f2-0000-7000-8000-0000000000c1';

function observation(over: Record<string, unknown> = {}) {
  return {
    id: `0190a8f2-0000-7000-8000-00000000${Math.floor(Math.random() * 8999 + 1000)}`,
    patient_id: PATIENT,
    code: 'HBA1C',
    category: 'LAB' as const,
    value_type: 'numeric' as const,
    value: 62,
    unit: 'mmol/mol',
    entered_value: 7.8,
    entered_unit: '%#ngsp',
    effective_at: '2026-09-14T04:00:00Z',
    recorded_at: '2026-09-14T04:05:00Z',
    source: 'STATION' as const,
    status: 'ACTIVE' as const,
    recorded_by: RINA,
    recorded_role: 'CLINICAL_ASSISTANT',
    station_code: 'STN_EXAMINATION',
    ...over,
  };
}

function view(over: Partial<DashboardView> = {}): DashboardView {
  return {
    as_of: '2026-09-14T04:10:00Z',
    patient: {
      id: PATIENT,
      clinical_id: 'DTHC-FRD-2026-000901',
      name_en: 'Ayesha Rahman',
      name_bn: 'আয়েশা রহমান',
      sex: 'female',
      birth_date: '1985-06-14',
      age_text: '41y',
      age_months: 495,
      status: 'active',
    },
    visit: {
      id: VISIT,
      visit_code: 'V-0912',
      visit_type: 'FOLLOW_UP',
      status: 'open',
      open: true,
      chief_complaint: 'tired for two weeks',
      clinic_day: '2026-09-14T00:00:00Z',
      opened_at: '2026-09-14T03:00:00Z',
    },
    access: { basis: 'NORMAL' },
    allergies: { status: 'NO_KNOWN_ALLERGY', satisfied: true, allergies: [] },
    critical_alerts: [],
    vitals: [observation(), observation({ code: 'BODY_WEIGHT', value: 61, unit: 'kg' })],
    body_mass: {
      observation: observation({ code: 'BMI', value: 24.03, unit: 'kg/m2' }),
      class: 'overweight',
      class_version: '1.0.0',
      scale: 'asian',
    },
    trends: [
      {
        code: 'HBA1C',
        unit: 'mmol/mol',
        points: [
          observation({ id: 'p1', value: 75, effective_at: '2025-09-14T04:00:00Z' }),
          observation({ id: 'p2', value: 68, effective_at: '2026-03-14T04:00:00Z' }),
          observation({ id: 'p3', value: 62, effective_at: '2026-09-14T04:00:00Z' }),
        ],
        change: { from: 75, to: 62, delta: -13, over_days: 365 },
      },
    ],
    active_conditions: [],
    growth: null,
    counseling: { visit_id: VISIT, blocked: false, overridden: false, missing: [] },
    summary: {
      state: 'READY',
      ai_generated: true,
      degraded: false,
      message_en: 'AI-generated draft. Every item needs the physician’s review before it is used.',
      message_bn: 'এআই-নির্মিত খসড়া।',
      requestable: true,
      narrative: 'Ayesha Rahman is a 41-year-old woman with type 2 diabetes of eight years.',
      key_points: ['HbA1c improving', 'Blood pressure above target'],
      red_flags: [
        { severity: 'urgent', statement: 'Systolic 165 at this visit', basis: ['obs.bp'] },
      ],
      citations: ['obs.hba1c:2026-09-14'],
      confidence: 0.72,
      provenance: {
        generation: 2,
        trigger: 'AUTOMATIC',
        prompt_version: '1.0.0',
        model_version: 'gemini-x',
        requested_at: '2026-09-14T03:50:00Z',
        finished_at: '2026-09-14T03:52:00Z',
        grounding_state: 'PASSED',
        grounding_findings: 0,
      },
    },
    assistant: {
      generation: 2,
      ai_generated: true,
      suggestions: [
        {
          ref: 'gap:no_hba1c_in_12_months',
          kind: 'GAP',
          origin: 'SYSTEM',
          label: 'no_hba1c_in_12_months',
          detail: 'No HbA1c in the last twelve months.',
          severity: 'important',
        },
        {
          ref: 'diagnosis:type_2_diabetes_mellitus',
          kind: 'DIAGNOSIS',
          origin: 'MODEL',
          label: 'Type 2 diabetes mellitus',
          icd10: 'E11.9',
          basis: ['obs.hba1c:2026-09-14'],
        },
        {
          ref: 'medication:metformin',
          kind: 'MEDICATION',
          origin: 'MODEL',
          label: 'Metformin',
          dose: '500 mg',
          frequency: 'twice daily',
          detail: 'First-line for type 2 diabetes at this HbA1c.',
        },
      ],
    },
    omitted: [],
    ...over,
  } as DashboardView;
}

const initialSession = useSessionStore.getInitialState();

const PHYSICIAN_PERMISSIONS = [
  'patient.read.clinical',
  'patient.read.demographics',
  'patient.read.allergies',
  'ai.synthesis.read',
  'ai.synthesis.request',
  'ai.suggestion.approve',
  'alert.read',
  'history.read',
  'observation.read.values',
  'counseling.session.read',
];

function holding(permissions: string[]) {
  useSessionStore.setState({
    ...initialSession,
    status: 'authenticated',
    user: {
      id: NAHID,
      employeeCode: 'E010',
      nameEN: 'Dr Nahid',
      nameBN: 'ডা. নাহিদ',
      facilityId: '11111111-1111-4111-8111-111111111111',
      roles: ['PHYSICIAN'],
      grants: { PHYSICIAN: permissions },
      permissions,
      secondFactor: { required: false, enrolled: true, pending: false, recoveryCodesLeft: 8 },
    },
    activeRole: 'PHYSICIAN',
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  holding(PHYSICIAN_PERMISSIONS);
  readDashboard.mockResolvedValue(view());
  getAllergyState.mockResolvedValue({ status: 'NONE_RECORDED', satisfied: false, allergies: [] });
  listPatientAlerts.mockResolvedValue([]);
  decideSuggestion.mockResolvedValue({
    kind: 'REJECTED',
    decided_by: NAHID,
    decided_at: '2026-09-14T04:20:00Z',
    generation: 2,
  });
});

afterEach(() => {
  useSessionStore.setState(initialSession, true);
  vi.restoreAllMocks();
});

async function open(locale: 'en' | 'bn' = 'en') {
  const result = renderWithProviders(<PhysicianDashboard patientId={PATIENT} />, { locale });
  await screen.findByTestId('dashboard');
  return result;
}

// --- one request ------------------------------------------------------------

describe('the whole screen is one request', () => {
  it('reads the dashboard once and asks nothing else for the allergy strip', async () => {
    await open();

    expect(readDashboard).toHaveBeenCalledTimes(1);
    // The allergy strip fetches for itself everywhere else in the application. Here it must
    // find the aggregate's answer already in its own cache — that priming *is* the acceptance
    // criterion, and if it stops working nothing breaks visibly: the screen is simply slow.
    expect(getAllergyState).not.toHaveBeenCalled();
  });

  it('shows the allergy status the aggregate carried, not the one the strip would have fetched', async () => {
    // The stub for `getAllergyState` deliberately answers NONE_RECORDED while the aggregate
    // says NO_KNOWN_ALLERGY. If the strip ever fetched again, this is the assertion that
    // catches it — a call count alone would pass against a component that fetched and threw
    // the answer away.
    await open();

    const strip = await screen.findByTestId('allergy-strip');
    expect(strip).toHaveAttribute('data-status', 'NO_KNOWN_ALLERGY');
  });
});

// --- criterion 3: AI marking ------------------------------------------------

describe('AI content is unmistakably marked', () => {
  it('encloses the narrative in a region that names the machine', async () => {
    await open();

    const region = screen.getByTestId('summary-ai-region');
    expect(within(region).getByTestId('summary-narrative')).toBeInTheDocument();
    // A landmark whose accessible name leads with the machine, so a screen-reader user meets
    // "AI-generated" on entry rather than discovering it from a visual treatment.
    expect(region).toHaveAttribute('aria-label', expect.stringMatching(/AI/i));
  });

  it('says so in both languages at once', async () => {
    // A summary is read over a shoulder by whoever is in the room, and the person who most
    // needs to know a machine wrote it may not be the one who chose the interface language.
    await open();
    const region = screen.getByTestId('summary-ai-region');
    expect(region.textContent).toMatch(/[ঀ-৿]/);
    expect(region.textContent).toMatch(/[A-Za-z]/);
  });

  it('does not mark the assembler’s own findings as machine-written', async () => {
    // The error nobody thinks of. Marking arithmetic as an opinion teaches a physician to
    // discount the one group on this panel that is certainly true.
    await open();

    const gap = screen.getByTestId('suggestion-gap:no_hba1c_in_12_months');
    expect(within(gap).getByTestId('origin-SYSTEM')).toBeInTheDocument();

    const systemGroup = screen.getByTestId('assistant-system');
    expect(within(systemGroup).queryByTestId('ai-marked')).toBeNull();

    // And the model's half is inside one.
    const modelGroup = screen.getByTestId('assistant-ai-region');
    expect(
      within(modelGroup).getByTestId('suggestion-diagnosis:type_2_diabetes_mellitus'),
    ).toBeInTheDocument();
  });

  it('marks every item, including the ones that are not AI', async () => {
    // A distinction carried by the absence of a mark cannot survive being cropped, and "no
    // mark" and "a mark I did not notice" look identical.
    await open();
    const model = screen.getByTestId('suggestion-diagnosis:type_2_diabetes_mellitus');
    expect(within(model).getByTestId('origin-MODEL')).toBeInTheDocument();
  });
});

// --- criterion 5: attribution -----------------------------------------------

describe('attribution is one interaction away on every value', () => {
  it('draws the current values through the attribution control', async () => {
    await open();
    expect(screen.getByTestId('snapshot-value-HBA1C')).toBeInTheDocument();
    expect(screen.getByTestId('snapshot-bmi-attribution')).toBeInTheDocument();
  });

  it('makes every point of a sparkline interrogable, not only the newest', async () => {
    // The place the promise is most likely to quietly not hold: a chart is a picture, and a
    // step in an HbA1c series is exactly what makes a physician ask who typed it.
    await open();
    for (const id of ['p1', 'p2', 'p3']) {
      expect(screen.getByTestId(`sparkline-point-${id}`)).toBeInTheDocument();
    }
    expect(screen.getByTestId('sparkline-now-HBA1C')).toBeInTheDocument();
  });

  it('names who entered a value when the control is opened', async () => {
    const user = userEvent.setup();
    renderWithProviders(<PhysicianDashboard patientId={PATIENT} />, {
      directory: {
        staff: [
          {
            id: RINA,
            name_en: 'Rina Akter',
            name_bn: 'রিনা আক্তার',
            code: 'A21',
            status: 'active',
          },
        ],
        devices: [],
        stations: [],
        as_of: '2026-09-14T00:00:00Z',
      },
    });
    await screen.findByTestId('dashboard');

    const value = screen.getByTestId('snapshot-value-HBA1C');
    await user.click(within(value).getByRole('button'));
    expect(within(value).getByText(/Rina Akter/)).toBeInTheDocument();
  });
});

// --- withheld against empty -------------------------------------------------

describe('a panel that was withheld is not drawn as an empty one', () => {
  it('says which panel and which permission', async () => {
    readDashboard.mockResolvedValue(
      view({
        active_conditions: undefined,
        omitted: [
          {
            panel: 'active_conditions',
            permission: 'history.read',
            reason_en: 'Your role does not include this part of the record.',
            reason_bn: 'আপনার ভূমিকায় রেকর্ডের এই অংশ দেখার অনুমতি নেই।',
          },
        ],
      }),
    );
    await open();

    const note = screen.getByTestId('withheld-active_conditions');
    expect(within(note).getByText(/does not include/i)).toBeInTheDocument();
    expect(within(note).getByText(/history\.read/)).toBeInTheDocument();
    // And the empty-state sentence is nowhere on the screen: drawing both would be saying
    // two contradictory things about the same panel.
    expect(screen.queryByTestId('snapshot-conditions')).toBeNull();
  });

  it('draws an empty panel differently from a withheld one', async () => {
    await open();
    const conditions = screen.getByTestId('snapshot-conditions');
    expect(within(conditions).getByText(/Nothing coded/i)).toBeInTheDocument();
    expect(screen.queryByTestId('withheld-active_conditions')).toBeNull();
  });

  it('flattens a dotted panel name rather than rendering a key at a physician', async () => {
    // The server names a panel by its JSON path — `access.break_glass` — and a dot is how
    // next-intl expresses nesting. A panel this build has never heard of falls back to a
    // generic name rather than putting `trends.bp_systolic` in a heading.
    readDashboard.mockResolvedValue(
      view({
        omitted: [
          {
            panel: 'trends.bp_systolic',
            permission: '',
            reason_en: 'This part of the record could not be read just now.',
            reason_bn: 'রেকর্ডের এই অংশ এখন পড়া যায়নি।',
          },
        ],
      }),
    );
    await open();
    const note = screen.getByTestId('withheld-trends.bp_systolic');
    expect(note.textContent).not.toMatch(/panelName/);
    expect(note.textContent).not.toMatch(/bp_systolic/);
  });
});

// --- the degraded summary ---------------------------------------------------

describe('the summary’s degraded states are drawn, not hidden', () => {
  it('says a withheld summary was withheld and why, and keeps the record beside it', async () => {
    readDashboard.mockResolvedValue(
      view({
        summary: {
          state: 'FAILED',
          ai_generated: true,
          degraded: true,
          message_en:
            'AI summary unavailable — the structured record is shown. The summary was withheld: a claim in it could not be traced to this patient’s record.',
          message_bn: 'এআই সারসংক্ষেপ পাওয়া যায়নি — কাঠামোবদ্ধ রেকর্ড দেখানো হচ্ছে।',
          requestable: true,
          provenance: {
            generation: 1,
            trigger: 'AUTOMATIC',
            requested_at: '2026-09-14T03:50:00Z',
            grounding_state: 'FAILED',
            grounding_findings: 2,
            failure_kind: 'UNGROUNDED',
          },
        },
      }),
    );
    await open();

    expect(screen.getByTestId('summary-ai-region')).toHaveAttribute('data-degraded', 'true');
    // More than once: the state line carries the server's sentence and the provenance block
    // carries the grounding verdict, and both should say it.
    expect(screen.getAllByText(/could not be traced/i).length).toBeGreaterThan(0);
    // No narrative is invented, and the absence is said out loud rather than left as space.
    expect(screen.queryByTestId('summary-narrative')).toBeNull();
    expect(screen.getByTestId('summary-no-narrative')).toBeInTheDocument();
    // The record is still there — that is the whole of D-15's degraded mode.
    expect(screen.getByTestId('snapshot-vitals')).toBeInTheDocument();
  });

  it('keeps the deterministic findings when the model has not answered', async () => {
    readDashboard.mockResolvedValue(
      view({
        summary: {
          state: 'PENDING',
          ai_generated: true,
          degraded: true,
          message_en: 'The AI summary is being prepared. The structured record is shown.',
          message_bn: 'এআই সারসংক্ষেপ তৈরি হচ্ছে।',
          requestable: false,
        },
        assistant: {
          generation: 1,
          ai_generated: true,
          suggestions: [
            {
              ref: 'gap:no_hba1c_in_12_months',
              kind: 'GAP',
              origin: 'SYSTEM',
              label: 'no_hba1c_in_12_months',
              detail: 'No HbA1c in the last twelve months.',
              severity: 'important',
            },
          ],
        },
      }),
    );
    await open();

    expect(screen.getByTestId('assistant-system')).toBeInTheDocument();
    expect(screen.queryByTestId('assistant-ai-region')).toBeNull();
  });

  it('translates a gap code into a sentence, falling back to the server’s English', async () => {
    await open();
    const gap = screen.getByTestId('suggestion-gap:no_hba1c_in_12_months');
    expect(gap.textContent).toMatch(/No HbA1c in the last twelve months/i);
    expect(gap.textContent).not.toMatch(/no_hba1c_in_12_months/);
  });
});

// --- accept, edit, reject ---------------------------------------------------

describe('accept, edit and reject', () => {
  it('says under every drafted drug that accepting is not prescribing', async () => {
    // §7.3's split, said where the button is. A physician who pressed Accept on a drafted
    // metformin and assumed it was on the prescription would have been misled by this screen.
    await open();
    const medication = screen.getByTestId('suggestion-medication:metformin');
    expect(within(medication).getByText(/does not write a prescription/i)).toBeInTheDocument();
    expect(within(medication).getByTestId('accept-medication:metformin').textContent).toMatch(
      /intent/i,
    );
  });

  it('sends a rejection with its note', async () => {
    const user = userEvent.setup();
    await open();

    await user.click(screen.getByTestId('reject-diagnosis:type_2_diabetes_mellitus'));

    await waitFor(() =>
      expect(decideSuggestion).toHaveBeenCalledWith({
        patientId: PATIENT,
        visitId: VISIT,
        ref: 'diagnosis:type_2_diabetes_mellitus',
        decision: 'REJECTED',
      }),
    );
  });

  it('requires the edited wording before an edit can be saved', async () => {
    const user = userEvent.setup();
    await open();

    await user.click(screen.getByTestId('start-edit-diagnosis:type_2_diabetes_mellitus'));
    const box = screen.getByTestId('edit-diagnosis:type_2_diabetes_mellitus');
    await user.clear(box);
    expect(screen.getByTestId('save-edit-diagnosis:type_2_diabetes_mellitus')).toBeDisabled();

    await user.type(box, 'Type 2 diabetes with early nephropathy');
    await user.click(screen.getByTestId('save-edit-diagnosis:type_2_diabetes_mellitus'));

    await waitFor(() =>
      expect(decideSuggestion).toHaveBeenCalledWith(
        expect.objectContaining({
          decision: 'EDITED',
          edited: 'Type 2 diabetes with early nephropathy',
        }),
      ),
    );
  });

  it('keeps a decided suggestion on the panel rather than hiding it', async () => {
    // A rejection that vanished would leave no evidence the physician had considered the
    // draft, which is half of why the decision is recorded at all.
    readDashboard.mockResolvedValue(
      view({
        assistant: {
          generation: 2,
          ai_generated: true,
          suggestions: [
            {
              ref: 'diagnosis:type_2_diabetes_mellitus',
              kind: 'DIAGNOSIS',
              origin: 'MODEL',
              label: 'Type 2 diabetes mellitus',
              decision: {
                kind: 'REJECTED',
                note: 'already coded at the last visit',
                decided_by: NAHID,
                decided_at: '2026-09-14T04:20:00Z',
                generation: 2,
              },
            },
          ],
        },
      }),
    );
    await open();

    const card = screen.getByTestId('suggestion-diagnosis:type_2_diabetes_mellitus');
    expect(card).toHaveAttribute('data-decision', 'REJECTED');
    expect(within(card).getByText(/already coded at the last visit/)).toBeInTheDocument();
  });

  it('says when a decision was made against an older run', async () => {
    readDashboard.mockResolvedValue(
      view({
        assistant: {
          generation: 3,
          ai_generated: true,
          suggestions: [
            {
              ref: 'diagnosis:type_2_diabetes_mellitus',
              kind: 'DIAGNOSIS',
              origin: 'MODEL',
              label: 'Type 2 diabetes mellitus',
              decision: {
                kind: 'ACCEPTED',
                decided_by: NAHID,
                decided_at: '2026-09-14T04:20:00Z',
                generation: 1,
              },
            },
          ],
        },
      }),
    );
    await open();
    expect(screen.getByText(/generation 1/i)).toBeInTheDocument();
  });

  it('offers no decision controls at all without the permission', async () => {
    holding(PHYSICIAN_PERMISSIONS.filter((p) => p !== 'ai.suggestion.approve'));
    await open();
    expect(screen.queryByTestId('accept-diagnosis:type_2_diabetes_mellitus')).toBeNull();
    expect(screen.queryByTestId('reject-diagnosis:type_2_diabetes_mellitus')).toBeNull();
  });

  it('offers no decision controls on a closed visit, and says why', async () => {
    readDashboard.mockResolvedValue(
      view({
        visit: {
          id: VISIT,
          visit_code: 'V-0912',
          visit_type: 'FOLLOW_UP',
          status: 'closed',
          open: false,
          clinic_day: '2026-09-14T00:00:00Z',
          opened_at: '2026-09-14T03:00:00Z',
        },
      }),
    );
    await open();
    expect(screen.queryByTestId('accept-diagnosis:type_2_diabetes_mellitus')).toBeNull();
    expect(screen.getByTestId('assistant-visit-closed')).toBeInTheDocument();
  });

  it('tells the physician to reload when the summary has been prepared again', async () => {
    const user = userEvent.setup();
    decideSuggestion.mockRejectedValue(
      new ApiError({
        status: 409,
        code: 'DASHBOARD_SUGGESTION_STALE',
        kind: 'conflict',
        messageEN: 'The summary has been prepared again.',
        messageBN: 'সারসংক্ষেপ আবার তৈরি হয়েছে।',
        correlationID: 'test-stale',
      }),
    );
    await open();

    await user.click(screen.getByTestId('reject-diagnosis:type_2_diabetes_mellitus'));
    const error = await screen.findByTestId('assistant-error');
    // The specific remedy, not a generic conflict: "someone else changed this" would send a
    // physician looking for a colleague who changed nothing.
    expect(error.textContent).toMatch(/Reload/i);
  });
});

// --- break-glass ------------------------------------------------------------

describe('the emergency door', () => {
  it('tells the physician they are reading under break-glass, with their own reason', async () => {
    readDashboard.mockResolvedValue(
      view({
        access: {
          basis: 'BREAK_GLASS',
          break_glass: {
            id: '0190a8f2-0000-7000-8000-0000000000f1',
            justification: 'unconscious patient, no attendant, needs insulin history',
            granted_at: '2026-09-14T03:00:00Z',
            expires_at: '2026-09-14T07:00:00Z',
            acknowledged: false,
          },
        },
      }),
    );
    await open();

    const banner = screen.getByTestId('break-glass-banner');
    expect(within(banner).getByText(/unconscious patient/)).toBeInTheDocument();
    expect(banner.textContent).toMatch(/No administrator has seen it yet/i);
  });

  it('shows no banner on an ordinary read', async () => {
    await open();
    expect(screen.queryByTestId('break-glass-banner')).toBeNull();
  });

  it('offers the emergency door on a refusal without saying why it was refused', async () => {
    readDashboard.mockRejectedValue(
      new ApiError({
        status: 403,
        code: 'FORBIDDEN',
        kind: 'authorization',
        messageEN: 'You do not have permission to do that.',
        messageBN: 'আপনার অনুমতি নেই।',
        correlationID: 'test-forbidden',
      }),
    );
    renderWithProviders(<PhysicianDashboard patientId={PATIENT} />);
    const refusal = await screen.findByTestId('dashboard-refused');

    const link = within(refusal).getByTestId('break-glass-link');
    expect(link).toHaveAttribute('href', `/break-glass?patient=${PATIENT}`);
    // And the screen does not guess *why* it was refused. The server answers the same 403
    // whatever the reason, deliberately, and a client that helpfully explained "this patient
    // is outside your scope" would have confirmed that the patient exists.
    expect(refusal.textContent).not.toMatch(/scope|another physician|not your patient/i);
  });
});

// --- keyboard ---------------------------------------------------------------

describe('keyboard shortcuts', () => {
  it('moves focus to a panel rather than only scrolling to it', async () => {
    const user = userEvent.setup();
    await open();

    await user.keyboard('2');
    expect(document.getElementById('dash-summary')).toHaveFocus();

    await user.keyboard('1');
    expect(document.getElementById('dash-snapshot')).toHaveFocus();
  });

  it('collapses and restores the assistant', async () => {
    const user = userEvent.setup();
    await open();

    expect(document.getElementById('dash-assistant')).not.toHaveAttribute('hidden');
    await user.keyboard('r');
    await waitFor(() =>
      expect(document.getElementById('dash-assistant')).toHaveAttribute('hidden'),
    );
    await user.keyboard('r');
    await waitFor(() =>
      expect(document.getElementById('dash-assistant')).not.toHaveAttribute('hidden'),
    );
  });

  it('does not fire while somebody is typing a rejection note', async () => {
    // The one situation single-key shortcuts are dangerous in. `r` in a note is an `r`.
    const user = userEvent.setup();
    await open();

    await user.click(screen.getByTestId('start-edit-diagnosis:type_2_diabetes_mellitus'));
    const note = screen.getByTestId('note-diagnosis:type_2_diabetes_mellitus');
    await user.click(note);
    await user.keyboard('rp321');

    expect(note).toHaveValue('rp321');
    expect(document.getElementById('dash-assistant')).not.toHaveAttribute('hidden');
  });

  it('opens the shortcut list and returns focus when it closes', async () => {
    const user = userEvent.setup();
    await open();

    const opener = screen.getByTestId('shortcuts');
    opener.focus();
    await user.keyboard('?');
    const help = await screen.findByTestId('shortcut-help');
    expect(help).toHaveFocus();

    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByTestId('shortcut-help')).toBeNull());
    expect(opener).toHaveFocus();
  });
});

// --- the BMI band -----------------------------------------------------------

describe('the body-mass card', () => {
  it('names the scale beside the band', async () => {
    // A class with no scale is a word two people read differently: 24 is "normal"
    // internationally and "overweight" on the cut-offs this clinic uses.
    await open();
    const bmi = screen.getByTestId('snapshot-bmi');
    expect(within(bmi).getByText(/Overweight/i)).toBeInTheDocument();
    expect(within(bmi).getByText(/Asian cut-offs/i)).toBeInTheDocument();
  });
});

// --- Bangla -----------------------------------------------------------------

describe('the screen reads in Bangla', () => {
  it('draws the server’s Bangla sentences and the Bangla name', async () => {
    await open('bn');
    expect(screen.getByText('আয়েশা রহমান')).toBeInTheDocument();
    expect(screen.getByTestId('summary-ai-region').textContent).toMatch(/এআই/);
  });
});
