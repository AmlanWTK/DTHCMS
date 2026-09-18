import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ComponentType } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type {
  EducationChecklist,
  EducationCompetency,
  EducationReference,
  EducationSession,
  ImprovementScale,
} from '@/features/education';

import type { RecordingCapability } from '@/features/education/api/capability';

import { renderWithProviders } from './render';

const readEducationReference = vi.hoisted(() => vi.fn());
const readEducationSession = vi.hoisted(() => vi.fn());

vi.mock('@/features/education/api/education', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/education/api/education')>()),
  readEducationReference,
  readEducationSession,
}));

const { ComplianceQuestion } = await import('@/features/education/components/ComplianceQuestion');
const { ImprovementScoreSelector } =
  await import('@/features/education/components/ImprovementScoreSelector');
const { ReeducationNotice } = await import('@/features/education/components/ReeducationNotice');
const { TechniqueChecklist } = await import('@/features/education/components/TechniqueChecklist');
const { EducationStation } = await import('@/features/education/components/EducationStation');
const { useRecordingCapability } = await import('@/features/education/api/capability');
const { useSessionStore } = await import('@/stores/session');

/**
 * The education station's surfaces (CP88, CP92).
 *
 * The manual verification for CP88 is a real patient understanding the score selector without
 * explanation, and nothing here can stand in for that. What these tests hold is the set of ways
 * the screen could fail *quietly* while looking right in a screenshot:
 *
 *  - **A band drawn with no face or no words.** Colour alone is unavailable to one man in twelve
 *    and to anybody looking at a photocopy, and a scale whose bands differ only by tint is a
 *    scale the patient guesses at.
 *  - **Bengali that is really English.** An empty-string check passes for a label somebody
 *    forgot to translate; a script check does not.
 *  - **A numeral that has become the subject.** The faces and the words are what the patient
 *    reads; the numeral is the operator's. A test that only asserted the numbers exist would
 *    pass for a control that shows ten digits and nothing else.
 *  - **Last visit's answer pre-selected.** It is drawn as history, and a design that seeded it
 *    as a default would record what the patient did in March unless somebody disagreed.
 *  - **The compliance preamble trimmed.** It is the half that makes the true number sayable and
 *    the half a tidy-up removes first, because it reads as filler.
 *  - **A score offered on a first visit.** §2 says the question is not asked at all, and a
 *    number recorded there answers a different question and is averaged with the rest.
 */

const SCALE: ImprovementScale = {
  code: 'IMPROVEMENT_1_10',
  question_en: 'Compared with your last visit, how do you feel now?',
  question_bn: 'গতবারের তুলনায় এখন আপনার কেমন লাগছে?',
  min_value: 1,
  max_value: 10,
  neutral_value: 5,
  anchors: [
    {
      from_value: 1,
      to_value: 2,
      label_en: 'Much worse',
      label_bn: 'অনেক খারাপ',
      face_rank: 1,
      ordering: 10,
    },
    {
      from_value: 3,
      to_value: 4,
      label_en: 'A little worse',
      label_bn: 'একটু খারাপ',
      face_rank: 2,
      ordering: 20,
    },
    {
      from_value: 5,
      to_value: 5,
      label_en: 'About the same',
      label_bn: 'আগের মতোই',
      face_rank: 3,
      ordering: 30,
    },
    {
      from_value: 6,
      to_value: 7,
      label_en: 'A little better',
      label_bn: 'একটু ভালো',
      face_rank: 4,
      ordering: 40,
    },
    {
      from_value: 8,
      to_value: 10,
      label_en: 'Much better',
      label_bn: 'অনেক ভালো',
      face_rank: 5,
      ordering: 50,
    },
  ],
};

const NA_REASONS: EducationReference['not_applicable_reasons'] = [
  {
    code: 'first_visit',
    display_en: 'First visit - there is no last visit to compare with',
    display_bn: 'প্রথম আসা — তুলনা করার মতো আগের কোনো দিন নেই',
    ordering: 10,
  },
];

const STATES: EducationReference['states'] = [
  { state: 'demonstrated', display_en: 'Demonstrated', display_bn: 'নিজেই পেরেছেন', ordering: 10 },
  {
    state: 'corrected_today',
    display_en: 'Corrected today',
    display_bn: 'আজ শুধরে দেওয়া হয়েছে',
    ordering: 20,
  },
  { state: 'unable', display_en: 'Unable', display_bn: 'পারেননি', ordering: 30 },
];

const PEN: EducationChecklist = {
  code: 'PEN_TECHNIQUE',
  device_type: 'INSULIN_PEN',
  title_en: 'Insulin pen technique',
  title_bn: 'ইনসুলিন পেন ব্যবহারের কৌশল',
  items: [
    {
      ordinal: 1,
      code: 'EDU_PEN_01',
      text_en: 'Checks the expiry date and that the insulin looks as it should',
      text_bn: 'মেয়াদ শেষের তারিখ দেখে নেন এবং ইনসুলিন দেখতে ঠিক আছে কি না মিলিয়ে নেন',
      is_critical: false,
    },
    {
      ordinal: 4,
      code: 'EDU_PEN_04',
      text_en:
        'Air-shot: dials 2 units, holds the pen upright, presses until a drop appears at the tip',
      text_bn:
        'এয়ার-শট: ২ ইউনিট ঘুরিয়ে নিয়ে পেন সোজা উপরের দিকে ধরে চাপ দেন, যতক্ষণ না সুচের মাথায় এক ফোঁটা ওষুধ দেখা যায়',
      is_critical: true,
    },
  ],
};

function reference(): EducationReference {
  return {
    device_types: [
      { code: 'INSULIN_PEN', name_en: 'Insulin pen', name_bn: 'ইনসুলিন পেন', ordering: 10 },
    ],
    checklists: [PEN],
    states: STATES,
    score_scale: SCALE,
    not_applicable_reasons: NA_REASONS,
    missed_dose_reasons: [
      {
        code: 'cost',
        display_en: 'The medicine cost too much',
        display_bn: 'ওষুধের দাম বেশি পড়ে যাচ্ছিল',
        ordering: 10,
      },
      { code: 'forgot', display_en: 'Forgot', display_bn: 'মনে ছিল না', ordering: 20 },
    ],
    reeducation_policy: { unable_raises_flag: true, corrected_today_threshold: 3 },
    compliance_question_en:
      'Most people miss a dose sometimes. In the last week, how many times did you miss?',
    compliance_question_bn:
      'প্রায় সবারই কোনো না কোনো দিন ওষুধ বাদ পড়ে। গত এক সপ্তাহে আপনার কতবার বাদ পড়েছে?',
  };
}

const initialSession = useSessionStore.getInitialState();
const SHIRIN = '0190d820-0000-7000-8000-000000008811';
const NAHID = '0190d820-0000-7000-8000-000000008812';

/**
 * Signs somebody in, holding exactly these permissions.
 *
 * The tests below go through the real `usePermission`, which reads the real session store, rather
 * than through a stub that returns what the test wants. The whole defect being fixed here was a
 * screen that never asked the question; a test that answers it for the screen would not have
 * caught it either.
 */
function holding(role: string, permissions: string[]) {
  useSessionStore.setState({
    ...initialSession,
    status: 'authenticated',
    user: {
      id: role === 'PHYSICIAN' ? NAHID : SHIRIN,
      employeeCode: role === 'PHYSICIAN' ? 'E001' : 'E311',
      nameEN: role === 'PHYSICIAN' ? 'Dr Nahid' : 'Shirin Akter',
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

/** The prescription education officer: she records, which is the whole of CP88 §1. */
const OFFICER = ['education.read', 'education.record', 'reference.read', 'observation.read.values'];

/** The consultant at the next visit: he reads, and does not record. */
const PHYSICIAN = ['education.read', 'reference.read', 'observation.read.values'];

/**
 * Gives a writable control the capability token the real application would give it.
 *
 * The leaf components below take `RecordingCapability` as a **required** prop, and there is no
 * way to construct one: the type is branded and `useRecordingCapability` is the only producer.
 * That is deliberate — it is what makes "a control that can write is not renderable without the
 * permission" a compiler rule rather than a convention — and it means a test cannot hand one over
 * either. So these tests obtain theirs the way the station does, from the hook, against a session
 * store seeded with the officer's grants. A test that could fabricate a token would be testing a
 * different program.
 */
function capable<P extends { capability: RecordingCapability }>(Component: ComponentType<P>) {
  return function Capable(props: Omit<P, 'capability'>) {
    const capability = useRecordingCapability();
    if (capability === null) throw new Error('this test session does not hold education.record');
    return <Component {...({ ...props, capability } as P)} />;
  };
}

const Selector = capable(ImprovementScoreSelector);
const Checklist = capable(TechniqueChecklist);
const Compliance = capable(ComplianceQuestion);

beforeEach(() => {
  vi.clearAllMocks();
  holding('RX_EDUCATOR', OFFICER);
});

/** Whether a string contains Bengali script, as opposed to merely being non-empty. */
function hasBengali(value: string): boolean {
  return /[ঀ-৿]/.test(value);
}

describe('the improvement score selector', () => {
  it('draws a face and words for every band, not colour alone', () => {
    renderWithProviders(
      <Selector
        scale={SCALE}
        reasons={NA_REASONS}
        value={null}
        notApplicable={null}
        firstVisit={false}
        onPick={vi.fn()}
        onNotApplicable={vi.fn()}
      />,
    );

    // Five bands, five faces. A band with a tint and no face is a band a patient guesses at,
    // and a band with no words is one the officer cannot read back.
    const selector = screen.getByTestId('improvement-score');
    expect(selector.querySelectorAll('.edu-face')).toHaveLength(SCALE.anchors.length);
    for (const anchor of SCALE.anchors) {
      expect(screen.getByText(anchor.label_en)).toBeInTheDocument();
    }
  });

  it('offers one control per point of the scale, and one tap records it', async () => {
    const onPick = vi.fn();
    renderWithProviders(
      <Selector
        scale={SCALE}
        reasons={NA_REASONS}
        value={null}
        notApplicable={null}
        firstVisit={false}
        onPick={onPick}
        onNotApplicable={vi.fn()}
      />,
    );

    // Ten points, from the scale's own bounds. Criterion 1 is one interaction: a slider would
    // be a grab, a drag and a glance, on a tablet held at an angle in front of somebody else.
    expect(screen.getAllByRole('radio')).toHaveLength(10);
    await userEvent.click(screen.getByTestId('score-7'));
    expect(onPick).toHaveBeenCalledTimes(1);
    expect(onPick).toHaveBeenCalledWith(7);
  });

  it('reads the answer back in words as well as in digits', () => {
    renderWithProviders(
      <Selector
        scale={SCALE}
        reasons={NA_REASONS}
        value={7}
        notApplicable={null}
        firstVisit={false}
        onPick={vi.fn()}
        onNotApplicable={vi.fn()}
      />,
    );
    // "Recorded as 7" alone is a number to check against a spoken answer; the band is what the
    // officer actually confirms aloud.
    expect(screen.getByTestId('improvement-chosen')).toHaveTextContent('A little better');
  });

  it('does not offer a score on a first visit', () => {
    renderWithProviders(
      <Selector
        scale={SCALE}
        reasons={NA_REASONS}
        value={null}
        notApplicable={null}
        firstVisit
        onPick={vi.fn()}
        onNotApplicable={vi.fn()}
      />,
    );
    // §2: there is no last visit to compare with, so the question is not asked. A screen that
    // offered the scale anyway would collect a number answering a different question — and the
    // server refuses it, so the officer would meet a refusal in front of a patient.
    expect(screen.queryByTestId('score-7')).toBeNull();
    expect(screen.getByTestId('improvement-first-visit')).toBeInTheDocument();
    expect(screen.getByTestId('score-na-first_visit')).toBeInTheDocument();
  });

  it('renders the question and every band in Bangla', () => {
    renderWithProviders(
      <Selector
        scale={SCALE}
        reasons={NA_REASONS}
        value={null}
        notApplicable={null}
        firstVisit={false}
        onPick={vi.fn()}
        onNotApplicable={vi.fn()}
      />,
      { locale: 'bn' },
    );

    expect(screen.getByTestId('improvement-question')).toHaveTextContent(SCALE.question_bn);
    for (const anchor of SCALE.anchors) {
      const label = screen.getByText(anchor.label_bn);
      expect(label).toBeInTheDocument();
      // Non-empty is not the property that matters. An untranslated label passes an emptiness
      // check and fails the patient.
      expect(hasBengali(label.textContent ?? '')).toBe(true);
    }
  });
});

describe('the technique checklist', () => {
  it('offers exactly the three states and none of them by default', () => {
    renderWithProviders(
      <Checklist
        checklist={PEN}
        states={STATES}
        answers={{}}
        previous={new Map()}
        onAnswer={vi.fn()}
      />,
    );

    const item = screen.getByTestId('item-EDU_PEN_04');
    const buttons = within(item).getAllByRole('radio');
    expect(buttons).toHaveLength(3);
    // §5's states, and no "not assessed". An item nobody scored has no row; a fourth button
    // would turn an honest absence into a recorded judgement.
    for (const button of buttons) expect(button).toHaveAttribute('aria-checked', 'false');
  });

  it('marks the items that silently cost a patient their dose', () => {
    renderWithProviders(
      <Checklist
        checklist={PEN}
        states={STATES}
        answers={{}}
        previous={new Map()}
        onAnswer={vi.fn()}
      />,
    );
    expect(screen.getByTestId('item-EDU_PEN_04')).toHaveAttribute('data-critical', 'true');
    expect(screen.getByTestId('item-EDU_PEN_01')).not.toHaveAttribute('data-critical');
  });

  it('shows last visit as history and never as a default', () => {
    // Looked up by code rather than by position: an item inserted into the checklist would
    // silently move every index after it, and last visit's answer would end up against the
    // wrong line — on the one part of the screen whose job is to say what to check again.
    const airShot = PEN.items.find((item) => item.code === 'EDU_PEN_04');
    if (airShot === undefined) throw new Error('the fixture has lost the air-shot item');
    const previous = new Map<string, EducationCompetency>([
      [
        'EDU_PEN_04',
        {
          code: 'EDU_PEN_04',
          state: 'corrected_today',
          checklist_code: 'PEN_TECHNIQUE',
          ordinal: 4,
          text_en: airShot.text_en,
          text_bn: airShot.text_bn,
          is_critical: true,
          observed_at: '2026-06-14T05:00:00Z',
          recorded_by: SHIRIN,
          recorded_role: 'RX_EDUCATOR',
          recorded_at: '2026-06-14T05:00:00Z',
          station_code: 'STN_RX_EDUCATION',
          source: 'STATION',
        },
      ],
    ]);
    renderWithProviders(
      <Checklist
        checklist={PEN}
        states={STATES}
        answers={{}}
        previous={previous}
        onAnswer={vi.fn()}
      />,
    );

    // Visible — the thing to check next time is exactly what was corrected last time.
    expect(screen.getByTestId('last-EDU_PEN_04')).toHaveTextContent('Corrected today');
    // And not selected. A pre-selected default would record what the patient did in June
    // unless somebody actively disagreed, which is the failure this station exists to prevent.
    expect(screen.getByTestId('state-EDU_PEN_04-corrected_today')).toHaveAttribute(
      'aria-checked',
      'false',
    );
  });

  it('renders the items and the states in Bangla', () => {
    renderWithProviders(
      <Checklist
        checklist={PEN}
        states={STATES}
        answers={{}}
        previous={new Map()}
        onAnswer={vi.fn()}
      />,
      { locale: 'bn' },
    );
    expect(screen.getByText(PEN.title_bn)).toBeInTheDocument();
    for (const item of PEN.items) {
      const text = screen.getByText(item.text_bn);
      expect(hasBengali(text.textContent ?? '')).toBe(true);
    }
    for (const state of STATES) {
      expect(screen.getAllByText(state.display_bn).length).toBeGreaterThan(0);
    }
  });
});

describe('the compliance question', () => {
  it('asks it with the preamble that makes the true number sayable', () => {
    renderWithProviders(
      <Compliance
        reference={reference()}
        missed={null}
        reasons={[]}
        onMissed={vi.fn()}
        onToggleReason={vi.fn()}
      />,
    );
    // §7: the preamble is not politeness. Without it the question has one socially acceptable
    // answer, and the number that comes back is decoration.
    expect(screen.getByTestId('compliance-question')).toHaveTextContent(
      'Most people miss a dose sometimes.',
    );
  });

  it('asks for a reason only when something was missed', async () => {
    const onMissed = vi.fn();
    const { rerender } = renderWithProviders(
      <Compliance
        reference={reference()}
        missed={0}
        reasons={[]}
        onMissed={onMissed}
        onToggleReason={vi.fn()}
      />,
    );
    // Nought missed: no reasons. A coded reason attached to a zero is a row that means nothing
    // and will be counted by something.
    expect(screen.queryByTestId('compliance-reasons')).toBeNull();

    await userEvent.click(screen.getByTestId('missed-3'));
    expect(onMissed).toHaveBeenCalledTimes(1);
    expect(onMissed).toHaveBeenCalledWith(3);

    rerender(
      <Compliance
        reference={reference()}
        missed={3}
        reasons={[]}
        onMissed={onMissed}
        onToggleReason={vi.fn()}
      />,
    );
    expect(screen.getByTestId('reason-cost')).toBeInTheDocument();
  });

  it('asks it in Bangla with its preamble intact', () => {
    renderWithProviders(
      <Compliance
        reference={reference()}
        missed={2}
        reasons={['cost']}
        onMissed={vi.fn()}
        onToggleReason={vi.fn()}
      />,
      { locale: 'bn' },
    );
    const question = screen.getByTestId('compliance-question');
    expect(question).toHaveTextContent('প্রায় সবারই');
    expect(screen.getByTestId('reason-cost')).toHaveTextContent('ওষুধের দাম');
  });
});

describe('the GLP-1 checklists', () => {
  // The screen half of spec §6.4. The server decides which checklist arrives — that is asserted
  // against the real rule table in internal/education — and what has to be true here is that a
  // checklist carrying the daily timing item renders the daily question and nothing else. A
  // component that rendered a hard-coded "day of the week" label would pass every server test.
  const DAILY: EducationChecklist = {
    code: 'GLP1_DAILY_TECHNIQUE',
    device_type: 'GLP1_DAILY_PEN',
    title_en: 'Daily GLP-1 pen technique',
    title_bn: 'দৈনিক জিএলপি-১ পেন ব্যবহারের কৌশল',
    items: [
      {
        ordinal: 3,
        code: 'EDU_GLPD_03',
        text_en:
          'States the time of day they will take it, and that it is about the same time each day',
        text_bn:
          'দিনের কোন সময়ে নেবেন তা বলতে পারেন, এবং প্রতিদিন মোটামুটি সেই একই সময়েই নিতে হবে তা জানেন',
        is_critical: true,
      },
    ],
  };

  it('asks a daily patient the time of day and never the day of the week', () => {
    renderWithProviders(
      <Checklist
        checklist={DAILY}
        states={STATES}
        answers={{}}
        previous={new Map()}
        onAnswer={vi.fn()}
      />,
    );
    const list = screen.getByTestId('checklist-GLP1_DAILY_TECHNIQUE');
    expect(list).toHaveTextContent('time of day');
    expect(list).not.toHaveTextContent('day of the week');
  });

  it('renders the daily timing item in Bangla', () => {
    renderWithProviders(
      <Checklist
        checklist={DAILY}
        states={STATES}
        answers={{}}
        previous={new Map()}
        onAnswer={vi.fn()}
      />,
      { locale: 'bn' },
    );
    const wording = screen.getByText(DAILY.items[0]!.text_bn);
    expect(hasBengali(wording.textContent ?? '')).toBe(true);
  });
});

describe('the re-education flag', () => {
  it('says why it fired, not only that it did', () => {
    renderWithProviders(<ReeducationNotice raised unable={1} correctedToday={0} threshold={3} />);
    const flag = screen.getByTestId('reeducation-flag');
    expect(flag).toHaveAttribute('data-raised', 'true');
    // The working, so an officer can see how it was reached rather than argue with a verdict.
    expect(screen.getByTestId('reeducation-working')).toHaveTextContent('1');
    expect(screen.getByTestId('reeducation-working')).toHaveTextContent('3');
  });

  it('says so when it did not fire, rather than showing nothing', () => {
    renderWithProviders(
      <ReeducationNotice raised={false} unable={0} correctedToday={2} threshold={3} />,
    );
    // A notice that only appears in the bad case teaches an officer that its absence means
    // nothing was checked — which is exactly what the record refuses to say, by writing the
    // flag false rather than omitting it.
    expect(screen.getByTestId('reeducation-flag')).toHaveAttribute('data-raised', 'false');
  });

  it('draws a flag standing from an earlier visit differently', () => {
    renderWithProviders(
      <ReeducationNotice raised standing unable={0} correctedToday={0} threshold={3} />,
    );
    expect(screen.getByTestId('reeducation-flag')).toHaveAttribute('data-standing', 'true');
  });

  it('reads in Bangla', () => {
    renderWithProviders(<ReeducationNotice raised unable={1} correctedToday={0} threshold={3} />, {
      locale: 'bn',
    });
    expect(hasBengali(screen.getByTestId('reeducation-flag').textContent ?? '')).toBe(true);
  });
});

/**
 * Who the station draws itself for (CP88 §1, CP92 criterion 2).
 *
 * The nav entry is on `education.read`, which the physician holds deliberately — criterion 2 is
 * that he sees what the patient could do at the next consultation. The first version of this
 * feature took that as licence to hand him the whole station: ten live state buttons, the
 * missed-dose row, and the improvement score selector with a value highlighted. Every one of them
 * answers 403, so nothing false could reach the ledger — but the score is the part that matters,
 * and a screen letting the man whose treatment the number grades hover over an 8 is CP88's own
 * failure arriving through the interface instead of through the ledger.
 *
 * These two tests are written as the *absence* of controls rather than the presence of a flag.
 * A flag can be set and the buttons drawn anyway; that is precisely what happened.
 */
describe('the station and who is at it', () => {
  const PATIENT = '0190d820-0000-7000-8000-000000008801';
  const VISIT = '0190d820-0000-7000-8000-000000008802';

  function assessed(code: string, state: EducationCompetency['state']): EducationCompetency {
    const item = PEN.items.find((one) => one.code === code);
    if (item === undefined) throw new Error(`${code} is not on the fixture checklist`);
    return {
      code,
      state,
      checklist_code: PEN.code,
      ordinal: item.ordinal,
      text_en: item.text_en,
      text_bn: item.text_bn,
      is_critical: item.is_critical,
      observed_at: '2026-09-14T05:40:00Z',
      visit_id: VISIT,
      recorded_by: SHIRIN,
      recorded_role: 'RX_EDUCATOR',
      recorded_at: '2026-09-14T05:41:00Z',
      station_code: 'STN_RX_EDUCATION',
      source: 'STATION',
    };
  }

  function session(over: Partial<EducationSession> = {}): EducationSession {
    return {
      patient_id: PATIENT,
      visit_id: VISIT,
      checklists: [PEN],
      selected_devices: [
        {
          checklist_code: PEN.code,
          device_type: 'INSULIN_PEN',
          product_id: '0190d820-0000-7000-8000-000000008820',
          product_label: 'Mixtard 30',
          generic_name: 'Regular insulin human + Isophane insulin human (premix)',
        },
      ],
      unclassified_devices: [],
      improvement: { score: 7 },
      improvement_record: {
        answer: { score: 7 },
        recorded_by: SHIRIN,
        recorded_role: 'RX_EDUCATOR',
        recorded_at: '2026-09-14T05:42:00Z',
        effective_at: '2026-09-14T05:42:00Z',
        station_code: 'STN_RX_EDUCATION',
        source: 'PATIENT',
      },
      compliance_record: {
        missed_doses: 3,
        reasons: ['cost'],
        recorded_by: SHIRIN,
        recorded_role: 'RX_EDUCATOR',
        recorded_at: '2026-09-14T05:41:30Z',
        effective_at: '2026-09-14T05:41:30Z',
        station_code: 'STN_RX_EDUCATION',
        source: 'PATIENT',
      },
      first_visit: false,
      prior_competency: [
        assessed('EDU_PEN_01', 'demonstrated'),
        assessed('EDU_PEN_04', 'corrected_today'),
      ],
      reeducation_flagged: false,
      ...over,
    };
  }

  beforeEach(() => {
    readEducationReference.mockResolvedValue(reference());
    readEducationSession.mockResolvedValue(session());
  });

  async function open(locale: 'en' | 'bn' = 'en') {
    renderWithProviders(<EducationStation patientId={PATIENT} visitId={VISIT} />, { locale });
    await waitFor(() => expect(screen.getByTestId('education-station')).toBeInTheDocument());
    return screen.getByTestId('education-station');
  }

  it('shows the physician what was recorded and nothing he can operate', async () => {
    holding('PHYSICIAN', PHYSICIAN);
    const station = await open();

    // What he came for: the items, their three states, and who watched.
    expect(screen.getByTestId('recorded-assessment')).toBeInTheDocument();
    expect(screen.getByTestId('recorded-EDU_PEN_04')).toHaveAttribute(
      'data-state',
      'corrected_today',
    );
    expect(screen.getByTestId('recorded-EDU_PEN_04')).toHaveTextContent('Corrected today');
    expect(screen.getByTestId('recorded-compliance')).toHaveTextContent('3');

    // And the score as a settled fact: the number, its band, and no way to touch either.
    expect(screen.getByTestId('recorded-score-value')).toHaveTextContent('7');
    expect(screen.getByTestId('recorded-score-value')).toHaveTextContent('A little better');

    // The absence, asserted directly. Not "the mode is read" — the mode was never the problem.
    expect(within(station).queryAllByRole('radio')).toHaveLength(0);
    expect(within(station).queryAllByRole('textbox')).toHaveLength(0);
    // Every button on this screen is an attribution disclosure: the house control that answers
    // "who recorded this", on every clinical value in the application (CP61 §4.2). It reveals and
    // writes nothing, and it is the one control the physician genuinely needs here. Anything
    // else with a button role is a control that could change the record.
    for (const button of within(station).queryAllByRole('button')) {
      expect(button).toHaveAttribute('data-testid', 'attribution-trigger');
    }
    expect(screen.queryByTestId('improvement-score')).toBeNull();
    expect(screen.queryByTestId('score-7')).toBeNull();
    expect(screen.queryByTestId('state-EDU_PEN_04-corrected_today')).toBeNull();
    expect(screen.queryByTestId('missed-3')).toBeNull();
    expect(screen.queryByTestId('education-save')).toBeNull();
  });

  it('keeps "first visit, no comparison" readable rather than blank', async () => {
    holding('PHYSICIAN', PHYSICIAN);
    readEducationSession.mockResolvedValue(
      session({
        first_visit: true,
        improvement: null,
        improvement_record: undefined,
        prior_competency: [],
      }),
    );
    await open();

    // An empty score would read as a patient who declined to answer. The two observation codes
    // exist to keep those apart in the record; the screen must not merge them again.
    expect(screen.getByTestId('score-unasked')).toHaveTextContent(/first visit/i);
    expect(screen.queryByTestId('recorded-score-value')).toBeNull();
  });

  it('reads in Bangla for the physician too', async () => {
    holding('PHYSICIAN', PHYSICIAN);
    const station = await open('bn');
    expect(hasBengali(station.textContent ?? '')).toBe(true);
    expect(screen.getByTestId('recorded-EDU_PEN_04')).toHaveTextContent('আজ শুধরে দেওয়া হয়েছে');
    expect(within(station).queryAllByRole('radio')).toHaveLength(0);
    for (const button of within(station).queryAllByRole('button')) {
      expect(button).toHaveAttribute('data-testid', 'attribution-trigger');
    }
  });

  it('still gives the education officer the whole form', async () => {
    holding('RX_EDUCATOR', OFFICER);
    const station = await open();

    expect(station).toHaveAttribute('data-mode', 'record');
    expect(screen.getByTestId('state-EDU_PEN_04-corrected_today')).toBeInTheDocument();
    expect(screen.getByTestId('improvement-score')).toBeInTheDocument();
    expect(screen.getByTestId('score-7')).toBeInTheDocument();
    expect(screen.getByTestId('missed-3')).toBeInTheDocument();
    expect(screen.getByTestId('education-save')).toBeInTheDocument();
    // She is not shown the read-only transcript instead of her work.
    expect(screen.queryByTestId('recorded-assessment')).toBeNull();
  });
});
