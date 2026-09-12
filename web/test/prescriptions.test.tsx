import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { SafetyPanel } from '@/features/prescriptions/components/SafetyPanel';
import { PrintPreview } from '@/features/prescriptions/components/PrintPreview';
import { resolveDefault } from '@/features/prescriptions/api/prescriptions';
import type { PrescribingDefault, PrintModel, SafetyResult } from '@/features/prescriptions';

import { renderWithProviders } from './render';

/**
 * What the prescription editor tells a physician (CP81).
 *
 * The engine's correctness is proven in Go and the routes are proven against a database. What can
 * only be proven here is **what a person standing in front of a patient ends up believing**, and
 * the ways that fails are all quiet:
 *
 *  - An unchecked prescription drawn as a clean one. This clinic has approved no rule, so this is
 *    the state of every check today and it is the single most important display decision in the
 *    checkpoint.
 *  - A dose a machine drafted drawn as a dose the physician set.
 *  - A preview that shows a draft as though it were a prescription.
 *  - A medicine with no price drawn as free.
 *
 * Each of the tests below fails on the *appearance* rather than on the data: they assert against
 * the words on the screen, because the data is already asserted in Go and the defect this
 * checkpoint is guarding against is a correct payload rendered reassuringly.
 */

/* ------------------------------------------------------------------------- */
/* Fixtures                                                                   */
/* ------------------------------------------------------------------------- */

/** What the engine actually answers at this clinic today: forty-eight rules, none approved. */
const NO_RULES: SafetyResult = {
  at: '2026-09-14T09:30:00Z',
  verdict: 'NO_RULES_APPROVED',
  summary_en:
    'No medication safety rule has been approved yet, so none of these 4 medicine(s) has been ' +
    'checked against anything. This is not a clean result — it is no result.',
  summary_bn:
    'এখনও কোনো ওষুধ-নিরাপত্তার নিয়ম অনুমোদিত হয়নি, তাই এই ৪টি ওষুধের কোনোটিই কিছুর সঙ্গে মিলিয়ে ' +
    'দেখা হয়নি। এটি নিরাপদ বলে ফলাফল নয় — এটি কোনো ফলাফলই নয়।',
  findings: [],
  coverage: [
    {
      ref: '1',
      label: 'Comet 500 mg',
      state: 'NOT_COVERED',
      rules_considered: 0,
      findings: 0,
      in_formulary: true,
      components_known: true,
      note_en: 'No live rule was about this medicine.',
      note_bn: 'এই ওষুধ নিয়ে চালু কোনো নিয়ম ছিল না।',
    },
  ],
  uncovered_count: 4,
  rules_live: 0,
  evaluated_versions: [],
  cross_reactivity: { reported: [] },
};

const DRAFT_MODEL: PrintModel = {
  version: '1',
  generated_at: '2026-09-14T09:31:00Z',
  content_hash: 'abc123',
  prescription_id: '0190a8f2-0000-7000-8000-00000000aa01',
  status: 'DRAFT',
  status_caveat_en:
    'DRAFT — not signed. This is not a prescription and must not be dispensed against.',
  status_caveat_bn:
    'খসড়া — স্বাক্ষরিত নয়। এটি ব্যবস্থাপত্র নয় এবং এর ভিত্তিতে ওষুধ দেওয়া যাবে না।',
  patient: {
    clinical_id: 'DTHC-FRD-2026-000137',
    name_en: 'Md Rahim Uddin',
    name_bn: 'মোঃ রহিম উদ্দিন',
    sex_en: 'Male',
    sex_bn: 'পুরুষ',
    age_years: 52,
    age_text_en: '52 years',
    age_text_bn: '52 বছর',
    written_on: '2026-09-14',
    visit_id: '0190a8f2-0000-7000-8000-00000000bb01',
    resolved: true,
  },
  lines: [
    {
      ordinal: 1,
      item_id: '0190a8f2-0000-7000-8000-00000000cc01',
      medicine: 'Comet 500 mg',
      generic: 'Metformin hydrochloride',
      directions_en: '1 tablet, twice daily, for 30 days',
      directions_bn: '1 tablet · দিনে দুইবার · 30 দিন — খাবেন',
      instruction_en: 'Take after food.',
      instruction_bn: 'খাবারের পরপরই খাবেন।',
      price_unverified: true,
      price_text: '5.00',
    },
    {
      ordinal: 2,
      item_id: '0190a8f2-0000-7000-8000-00000000cc02',
      medicine: 'Something unstocked',
      directions_en: '1 tablet, once daily',
      directions_bn: '1 tablet · দিনে একবার — খাবেন',
      price_unverified: false,
    },
  ],
  price: {
    lines_with_price: 1,
    lines_no_price: 1,
    lines_provisional: 1,
    caveat_en:
      '1 medicine(s) have no price on file and 1 carry a price nobody at this clinic has checked.',
    caveat_bn: '১টি ওষুধের দাম নথিতে নেই এবং ১টির দাম এই ক্লিনিকের কেউ যাচাই করেননি।',
  },
  signature: {
    signed: false,
    note_en: 'Not signed. Signing arrives with CP84; nothing stands in this space yet.',
    note_bn: 'স্বাক্ষরিত নয়। স্বাক্ষরের ব্যবস্থা CP84-এ আসবে; এই জায়গায় এখনও কিছু নেই।',
  },
  omitted: [
    {
      item_id: '0190a8f2-0000-7000-8000-00000000cc03',
      medicine: 'Glimepiride 2 mg',
      reason_en: 'Taken off this prescription before it was printed.',
      reason_bn: 'ছাপার আগেই এই ব্যবস্থাপত্র থেকে বাদ দেওয়া হয়েছে।',
    },
  ],
};

/* ------------------------------------------------------------------------- */
/* The unapproved library                                                     */
/* ------------------------------------------------------------------------- */

describe('an unapproved rule library is rendered as no result, never as a clean one', () => {
  /**
   * The words a physician could read as reassurance.
   *
   * This list is the test. Not "does the panel render" — it does — but whether anything on it
   * could be glanced at and taken for "checked, nothing wrong". Every one of these has shipped in
   * somebody's clinical software as the empty state of a safety panel.
   */
  const REASSURING = [
    'no issues',
    'no problems',
    'no findings',
    'all clear',
    'looks good',
    'safe',
    'passed',
    'no warnings',
    'nothing found',
    'ok',
  ];

  it('says nothing was checked, in the verdict a reader sees first', () => {
    renderWithProviders(
      <SafetyPanel result={NO_RULES} checking={false} failed={false} itemCount={4} />,
    );

    expect(screen.getByTestId('safety-panel')).toHaveAttribute('data-verdict', 'NO_RULES_APPROVED');
    expect(screen.getByRole('heading', { level: 3 })).toHaveTextContent(
      /nothing has been checked/i,
    );
  });

  it('contains no word a physician could read as reassurance', () => {
    const { container } = renderWithProviders(
      <SafetyPanel result={NO_RULES} checking={false} failed={false} itemCount={4} />,
    );
    const text = (container.textContent ?? '').toLowerCase();
    // `ok` and `safe` occur inside other words, so the match is on word boundaries.
    const offenders = REASSURING.filter((word) =>
      new RegExp(`(^|[^a-z])${word}([^a-z]|$)`).test(text),
    );
    expect(
      offenders,
      `The panel says ${offenders.join(', ')} while no rule has been approved. ` +
        'Nothing on this prescription has been checked against anything.',
    ).toEqual([]);
  });

  it('says how many medicines went unchecked, as a number', () => {
    renderWithProviders(
      <SafetyPanel result={NO_RULES} checking={false} failed={false} itemCount={4} />,
    );
    expect(
      screen.getByText(/all 4 medicine\(s\) on this prescription are unchecked/i),
    ).toBeTruthy();
  });

  it('says what would make it different, and where', () => {
    renderWithProviders(
      <SafetyPanel result={NO_RULES} checking={false} failed={false} itemCount={4} />,
    );
    // A panel that says "nothing has been checked" and stops is a panel somebody learns to
    // ignore. This one names the act that changes it.
    expect(screen.getAllByText(/approve/i).length).toBeGreaterThan(0);
  });

  it('draws an uncovered medicine differently from a checked one', () => {
    const { container } = renderWithProviders(
      <SafetyPanel result={NO_RULES} checking={false} failed={false} itemCount={4} />,
    );
    const row = container.querySelector('[data-state="NOT_COVERED"]');
    expect(
      row,
      'the coverage row carries its state for the stylesheet and for this test',
    ).toBeTruthy();
    expect(row?.textContent).toMatch(/no rule covers this medicine/i);
  });

  it('is rendered in Bengali with the engine’s own Bengali sentence', () => {
    renderWithProviders(
      <SafetyPanel result={NO_RULES} checking={false} failed={false} itemCount={4} />,
      { locale: 'bn' },
    );
    expect(screen.getByText(/এটি কোনো ফলাফলই নয়/)).toBeTruthy();
  });

  it('reports a failed check as a failed check rather than showing the last good one', () => {
    const { container } = renderWithProviders(
      <SafetyPanel result={undefined} checking={false} failed itemCount={4} />,
    );
    expect(container.textContent).toMatch(/did not answer/i);
    expect(container.textContent).toMatch(/not been checked/i);
  });

  it('does not claim a check while there is nothing to check', () => {
    const { container } = renderWithProviders(
      <SafetyPanel result={undefined} checking={false} failed={false} itemCount={0} />,
    );
    expect(container.textContent).toMatch(/nothing has been checked/i);
  });
});

/* ------------------------------------------------------------------------- */
/* The preview                                                                */
/* ------------------------------------------------------------------------- */

describe('the preview shows the sheet as it will print', () => {
  it('carries the model version and content hash, so a print can be compared against it', () => {
    renderWithProviders(<PrintPreview model={DRAFT_MODEL} />);
    const root = screen.getByTestId('print-preview');
    expect(root).toHaveAttribute('data-print-model-version', '1');
    expect(root).toHaveAttribute('data-content-hash', 'abc123');
  });

  it('says a draft is not a prescription', () => {
    renderWithProviders(<PrintPreview model={DRAFT_MODEL} />);
    expect(screen.getByTestId('preview-caveat').textContent).toMatch(/not a prescription/i);
  });

  it('says it in Bengali to a Bengali reader', () => {
    renderWithProviders(<PrintPreview model={DRAFT_MODEL} />, { locale: 'bn' });
    expect(screen.getByTestId('preview-caveat').textContent).toMatch(/ব্যবস্থাপত্র নয়/);
  });

  it('prints both languages of the directions whichever language the reader uses', () => {
    // The paper is read by two people: the pharmacist reads the English directions, the patient
    // reads the Bangla. Switching the interface must not change what is on the sheet.
    for (const locale of ['en', 'bn'] as const) {
      const { container, unmount } = renderWithProviders(<PrintPreview model={DRAFT_MODEL} />, {
        locale,
      });
      expect(container.textContent).toContain('1 tablet, twice daily, for 30 days');
      expect(container.textContent).toContain('দিনে দুইবার');
      unmount();
    }
  });

  it('shows no price rather than a zero for a medicine with none', () => {
    const { container } = renderWithProviders(<PrintPreview model={DRAFT_MODEL} />);
    expect(container.textContent).toMatch(/no price on file/i);
    // A zero would tell a patient a medicine is free.
    expect(container.textContent).not.toMatch(/৳\s*0\.00/);
  });

  it('marks a price this clinic has not checked', () => {
    const { container } = renderWithProviders(<PrintPreview model={DRAFT_MODEL} />);
    expect(container.textContent).toMatch(/price not checked by this clinic/i);
  });

  it('shows what was taken off the sheet and why', () => {
    expect(renderWithProviders(<PrintPreview model={DRAFT_MODEL} />).container.textContent).toMatch(
      /Glimepiride 2 mg/,
    );
    expect(screen.getByTestId('preview-omitted').textContent).toMatch(/before it was printed/i);
  });

  it('says the signature space is empty rather than leaving it blank', () => {
    const { container } = renderWithProviders(<PrintPreview model={DRAFT_MODEL} />);
    expect(container.textContent).toMatch(/not signed/i);
  });

  it('says so when the patient could not be read, rather than showing blanks', () => {
    const unresolved: PrintModel = {
      ...DRAFT_MODEL,
      patient: {
        ...DRAFT_MODEL.patient,
        resolved: false,
        unresolved_note_en: "The patient's name and age could not be read for this preview.",
        unresolved_note_bn: 'এই প্রাকদর্শনের জন্য রোগীর নাম ও বয়স পড়া যায়নি।',
      },
    };
    const { container } = renderWithProviders(<PrintPreview model={unresolved} />);
    expect(container.textContent).toMatch(/could not be read/i);
    expect(container.textContent).not.toContain('Md Rahim Uddin');
  });
});

/* ------------------------------------------------------------------------- */
/* Resolving a suggestion                                                     */
/* ------------------------------------------------------------------------- */

describe('a dose suggestion resolves to the strength in the physician’s hand', () => {
  const rows = [
    { generic_name: 'Metformin hydrochloride', strength: '500 mg', dose: '1 tablet' },
    { generic_name: 'Metformin hydrochloride', strength: '1000 mg', dose: '1 tablet (1 g)' },
    { generic_name: 'Semaglutide', strength: '', dose: '1 pen dose' },
  ] as PrescribingDefault[];

  it('prefers the exact strength', () => {
    // The failure this guards: metformin's 500 mg suggestion offered for a 1000 mg tablet, which
    // is half the dose offered confidently.
    expect(resolveDefault(rows, 'Metformin hydrochloride', '1000 mg')?.dose).toBe('1 tablet (1 g)');
  });

  it('falls back to a molecule-wide row', () => {
    expect(resolveDefault(rows, 'Semaglutide', '1 mg/0.5 mL')?.dose).toBe('1 pen dose');
  });

  it('offers nothing when there is nothing for that strength', () => {
    expect(resolveDefault(rows, 'Metformin hydrochloride', '850 mg')).toBeUndefined();
  });

  it('offers nothing for a medicine nobody has written a suggestion for', () => {
    expect(resolveDefault(rows, 'Linagliptin', '5 mg')).toBeUndefined();
  });
});
