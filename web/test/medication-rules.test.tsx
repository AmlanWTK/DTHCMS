import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { FormularySearchResult } from '@/features/formulary/api/formulary';
import type { RuleVocabulary, SandboxResult } from '@/features/medication-rules/api/rules';

import { renderWithProviders } from './render';

/**
 * The two screens a physician meets (CP76 §10.1, CP77 §6.3).
 *
 * The manual verification for both is Dr. Nahid at a keyboard, and no test replaces it. What
 * can only be proven here is whether the person at that keyboard ends up believing the right
 * things — and each way that fails is quiet:
 *
 *  - **An alphabetical list presented as a personalised one.** The ranking's two per-physician
 *    terms have no source until CP80. A combobox that said nothing would have the physician
 *    concluding, reasonably, that the order reflects what he prescribes.
 *  - **A sandbox result read as something that happened.** He has just watched a rule block a
 *    prescription. If the screen does not say, in the same glance, that the rule is not live
 *    and the prescription does not exist, he has been shown a safety system that is not there.
 *  - **A provisional price drawn as a price.** Every seeded price is published MRP nobody at
 *    this clinic has checked, and this is the screen a cost is quoted to a patient from.
 *  - **An autocomplete that needs a mouse.** Keyboard-first is the requirement; a keyboard
 *    interface nobody can reach without pointing is a mouse interface.
 */

const searchFormulary = vi.hoisted(() => vi.fn());

vi.mock('@/features/formulary/api/formulary', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/formulary/api/formulary')>()),
  searchFormulary,
}));

const runSandbox = vi.hoisted(() => vi.fn());
const getVocabulary = vi.hoisted(() => vi.fn());
const previewRule = vi.hoisted(() => vi.fn());

vi.mock('@/features/medication-rules/api/rules', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/medication-rules/api/rules')>()),
  runSandbox,
  getVocabulary,
  previewRule,
}));

const { MedicineCombobox } = await import('@/features/formulary/components/MedicineCombobox');
const { RuleSandbox } = await import('@/features/medication-rules/components/RuleSandbox');

const THYROX: FormularySearchResult = {
  query: 'th',
  normalised_query: 'th',
  total: 2,
  ranking_complete: false,
  served_from: 'cache',
  cache_age_seconds: 3,
  entries: [
    {
      trade_name: 'Thyrox',
      generic_name: 'Levothyroxine sodium',
      manufacturer: 'Renata PLC',
      class_code: 'THYROID_HORMONE',
      class_name_en: 'Thyroid hormone',
      class_name_bn: 'থাইরয়েড হরমোন',
      form_code: 'TABLET',
      form_name_en: 'Tablet',
      form_name_bn: 'ট্যাবলেট',
      match: 'TRADE_PREFIX',
      times_prescribed: 0,
      days_since_last: null,
      strengths: [
        {
          product_id: 'p-25',
          strength: '25 mcg',
          dispense_unit: 'tablet',
          unit_name_en: 'tablet',
          unit_name_bn: 'ট্যাবলেট',
          price: {
            amount_poisha: 111,
            amount_bdt: '1.11',
            verification: 'PROVISIONAL',
            effective_from: '2026-09-08',
          },
        },
        {
          product_id: 'p-50',
          strength: '50 mcg',
          dispense_unit: 'tablet',
          unit_name_en: 'tablet',
          unit_name_bn: 'ট্যাবলেট',
          price: {
            amount_poisha: 220,
            amount_bdt: '2.20',
            verification: 'PROVISIONAL',
            effective_from: '2026-09-08',
          },
        },
      ],
    },
    {
      trade_name: 'Thyzol',
      generic_name: 'Methimazole',
      manufacturer: 'Nuvista Pharma Ltd.',
      class_code: 'ANTITHYROID',
      class_name_en: 'Antithyroid',
      class_name_bn: 'থাইরয়েড-প্রতিরোধী',
      form_code: 'TABLET',
      form_name_en: 'Tablet',
      form_name_bn: 'ট্যাবলেট',
      match: 'TRADE_PREFIX',
      times_prescribed: 0,
      days_since_last: null,
      strengths: [
        {
          product_id: 'p-5',
          strength: '5 mg',
          dispense_unit: 'tablet',
          unit_name_en: 'tablet',
          unit_name_bn: 'ট্যাবলেট',
          // No price at all — a real state, and one the screen must name rather than draw as zero.
          price: undefined,
        },
      ],
    },
  ],
};

beforeEach(() => {
  vi.clearAllMocks();
  searchFormulary.mockResolvedValue(THYROX);
});

describe('the two-letter autocomplete (CP76)', () => {
  it('surfaces the generic, the form, the strengths and the price on two letters', async () => {
    const user = userEvent.setup();
    renderWithProviders(<MedicineCombobox />);

    await user.type(screen.getByRole('combobox'), 'th');

    await waitFor(() => expect(screen.getByText('Thyrox')).toBeInTheDocument());
    // The checkpoint's wording is "trade names **with generic, strength, form and price**".
    // A ranked list of brand names alone would satisfy it read carelessly and would send the
    // physician to another screen to find out what he is prescribing.
    expect(screen.getAllByText('Levothyroxine sodium').length).toBeGreaterThan(0);
    expect(screen.getAllByText('Tablet').length).toBeGreaterThan(0);
    expect(screen.getByText('25 mcg')).toBeInTheDocument();
    expect(screen.getByText('50 mcg')).toBeInTheDocument();
    expect(screen.getByText(/1\.11/)).toBeInTheDocument();
  });

  it('says the ranking is not personalised while CP80 does not exist', async () => {
    const user = userEvent.setup();
    renderWithProviders(<MedicineCombobox />);
    await user.type(screen.getByRole('combobox'), 'th');

    await waitFor(() =>
      expect(
        screen.getByText(/your own prescribing history is not in this ranking yet/i),
      ).toBeInTheDocument(),
    );
  });

  it('names a medicine nobody has priced rather than drawing a zero', async () => {
    const user = userEvent.setup();
    renderWithProviders(<MedicineCombobox />);
    await user.type(screen.getByRole('combobox'), 'th');

    await waitFor(() => expect(screen.getByText('Thyzol')).toBeInTheDocument());
    expect(screen.getAllByText(/no price/i).length).toBeGreaterThan(0);
  });

  it('is chosen from the keyboard alone', async () => {
    // ↓ ↑ between medicines, ← → between strengths, Enter to take it. A physician
    // mid-prescription has one hand on the keyboard.
    const user = userEvent.setup();
    const chosen = vi.fn();
    renderWithProviders(<MedicineCombobox onChoose={chosen} />);

    await user.type(screen.getByRole('combobox'), 'th');
    await waitFor(() => expect(screen.getByText('Thyrox')).toBeInTheDocument());

    await user.keyboard('{ArrowRight}'); // Thyrox 25 mcg → 50 mcg
    await user.keyboard('{Enter}');

    await waitFor(() => expect(chosen).toHaveBeenCalledTimes(1));
    expect(chosen.mock.calls[0]?.[0]).toMatchObject({
      productId: 'p-50',
      tradeName: 'Thyrox',
      strength: '50 mcg',
      genericName: 'Levothyroxine sodium',
    });
  });

  it('asks for nothing until something is typed', async () => {
    renderWithProviders(<MedicineCombobox />);
    await new Promise((resolve) => setTimeout(resolve, 250));
    expect(searchFormulary).not.toHaveBeenCalled();
  });
});

const VOCABULARY: RuleVocabulary = {
  types: [
    {
      type: 'RENAL',
      name_en: 'Kidney function',
      name_bn: 'কিডনির কার্যকারিতা',
      hint_en: 'This medicine when the kidneys are not clearing it.',
      hint_bn: 'কিডনি ওষুধটি বের করতে না পারলে।',
      predicates: [{ kind: 'EGFR', name_en: 'the eGFR is', name_bn: 'eGFR', needs: 'EGFR' }],
    },
  ],
  severities: ['BLOCK', 'WARN', 'INFO'],
  operators: ['LT', 'LTE', 'GT', 'GTE'],
  pregnancy_states: ['PREGNANT', 'BREASTFEEDING', 'PLANNING'],
  hepatic_grades: ['MILD', 'MODERATE', 'SEVERE'],
  generics: ['Metformin hydrochloride'],
  classes: ['BIGUANIDE'],
  allergen_groups: ['PENICILLIN'],
};

const DRAFT = {
  severity: 'BLOCK' as const,
  name_en: 'Metformin below eGFR 30',
  name_bn: 'eGFR 30-এর নিচে মেটফরমিন',
  message_en: 'Metformin is contraindicated below an eGFR of 30.',
  message_bn: 'eGFR 30-এর নিচে মেটফরমিন দেওয়া যাবে না।',
  source: 'ADA Standards of Care 2025 s11.',
  condition: {
    subject: { match: 'GENERIC' as const, generics: ['metformin hydrochloride'] },
    when: [{ kind: 'EGFR' as const, operator: 'LT' as const, value: 30, unit: 'mL/min/1.73m2' }],
  },
};

function firing(live: boolean): SandboxResult {
  return {
    live,
    patient: { proposed: [] },
    plain: {
      en: 'When Metformin hydrochloride is prescribed and the eGFR is below 30 mL/min/1.73m2, stop the prescription and require a written reason to go ahead.',
      bn: 'Metformin hydrochloride দেওয়া হলে এবং eGFR 30 mL/min/1.73m2-এর নিচে হলে, ব্যবস্থাপত্রটি আটকে দেওয়া হবে।',
      needs_en: ['a recent eGFR'],
      needs_bn: ['সাম্প্রতিক eGFR'],
    },
    finding: {
      rule_code: 'MET-RENAL-30',
      version: 1,
      type: 'RENAL',
      severity: 'BLOCK',
      outcome: 'FIRES',
      message_en: 'Metformin is contraindicated below an eGFR of 30.',
      message_bn: 'eGFR 30-এর নিচে মেটফরমিন দেওয়া যাবে না।',
      source: 'ADA Standards of Care 2025 s11.',
      steps: [
        {
          kind: 'EGFR',
          truth: 'HOLDS',
          because_en: 'eGFR is 18 mL/min/1.73m2, which is below 30 mL/min/1.73m2',
          because_bn: 'eGFR 18 mL/min/1.73m2, যা 30 mL/min/1.73m2-এর নিচে',
        },
      ],
    },
  };
}

describe('the rule-testing sandbox (CP77)', () => {
  beforeEach(() => {
    getVocabulary.mockResolvedValue(VOCABULARY);
    previewRule.mockResolvedValue({ plain: firing(false).plain, valid: true });
  });

  it('says out loud that an unpublished rule is doing nothing', async () => {
    // The failure this prevents: a physician watches a rule block a prescription, and takes
    // away that the safety checking is on. It is not, and will not be until he publishes.
    runSandbox.mockResolvedValue(firing(false));
    const user = userEvent.setup();
    renderWithProviders(<RuleSandbox type="RENAL" versionId="v-1" dirty={false} draft={DRAFT} />);

    await user.click(screen.getByRole('button', { name: /run it/i }));

    await waitFor(() => expect(screen.getByText(/this rule is not live/i)).toBeInTheDocument());
    expect(screen.getByText(/it fires/i)).toBeInTheDocument();
    // And the working, so the verdict is not something to take on trust.
    expect(screen.getByText(/eGFR is 18 mL\/min/)).toBeInTheDocument();
  });

  it('says the opposite when the rule is live', async () => {
    runSandbox.mockResolvedValue(firing(true));
    const user = userEvent.setup();
    renderWithProviders(<RuleSandbox type="RENAL" versionId="v-1" dirty={false} draft={DRAFT} />);

    await user.click(screen.getByRole('button', { name: /run it/i }));
    await waitFor(() => expect(screen.getByText(/this rule is live/i)).toBeInTheDocument());
  });

  it('tests what is on screen rather than what is saved, when the two differ', async () => {
    // A sandbox that silently ran the saved version while the author was looking at his edits
    // would be showing him the behaviour of a rule he is no longer writing.
    runSandbox.mockResolvedValue(firing(false));
    const user = userEvent.setup();
    renderWithProviders(<RuleSandbox type="RENAL" versionId="v-1" dirty draft={DRAFT} />);

    await user.click(screen.getByRole('button', { name: /run it/i }));

    await waitFor(() => expect(runSandbox).toHaveBeenCalled());
    const sent = runSandbox.mock.calls[0]?.[0];
    expect(sent?.versionId).toBeUndefined();
    expect(sent?.rule).toMatchObject({ name_en: 'Metformin below eGFR 30' });
  });

  it('sends an unanswered question as unknown rather than as an answer', async () => {
    // "We asked, and there are none" and "nobody asked" are different clinical facts, and only
    // one of them is safe to treat as an answer. An empty field is the second.
    runSandbox.mockResolvedValue(firing(false));
    const user = userEvent.setup();
    renderWithProviders(<RuleSandbox type="RENAL" versionId="v-1" dirty={false} draft={DRAFT} />);

    await user.click(screen.getByRole('button', { name: /run it/i }));
    await waitFor(() => expect(runSandbox).toHaveBeenCalled());

    const patient = runSandbox.mock.calls[0]?.[0]?.patient;
    expect(patient?.egfr).toBeNull();
    expect(patient?.age_years).toBeNull();
    expect(patient?.diagnoses).toBeNull();
    expect(patient?.allergies).toBeNull();
  });
});
