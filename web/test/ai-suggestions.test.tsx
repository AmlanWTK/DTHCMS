import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from './render';

/**
 * What the AI suggestion panel tells a physician (CP82).
 *
 * The boundary itself is proven in Go and in the database: a suggestion cannot become a
 * prescription line without a decision, and a deferred constraint refuses the commit. What can
 * only be proven here is **what a person standing in front of a patient ends up believing and
 * ends up able to do by accident**, and every failure of that is quiet:
 *
 *  - A suggestion drawn like a prescription line. The specification's named risk is automation
 *    bias, and the mitigation is entirely visual: if the two look alike, the fortieth suggestion
 *    is accepted without being read.
 *  - **A suggestion reachable by the keyboard flow that writes prescription lines.** This is the
 *    one that would actually produce a wrongly accepted medicine: a physician tabbing out of a
 *    dose field onto an "Accept" button and pressing Enter, which is the keystroke he has pressed
 *    four times already.
 *  - An "accept all" control, of any kind.
 *  - A suggestion nobody answered drawn as one that was declined.
 *  - A declined suggestion that cost two decisions to decline.
 *
 * Each test below fails on the *screen* rather than on the data.
 */

const getSuggestions = vi.hoisted(() => vi.fn());
const listRejectReasons = vi.hoisted(() => vi.fn());
const askForSuggestions = vi.hoisted(() => vi.fn());
const decideSuggestion = vi.hoisted(() => vi.fn());

vi.mock('@/features/prescriptions/api/aiSuggestions', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/prescriptions/api/aiSuggestions')>()),
  getSuggestions,
  listRejectReasons,
  askForSuggestions,
  decideSuggestion,
}));

const { AISuggestionPanel } = await import(
  '@/features/prescriptions/components/AISuggestionPanel'
);
const { PRESCRIPTION_SHORTCUTS } = await import(
  '@/features/prescriptions/components/usePrescriptionShortcuts'
);

const PRESCRIPTION = '0190a8f2-0000-7000-8000-00000000aa01';

/** A suggestion the way the server sends one: no `decision` key at all when nobody has answered. */
function suggestion(over: Record<string, unknown> = {}) {
  return {
    id: '0190a8f2-0000-7000-8000-00000000dd01',
    run_id: '0190a8f2-0000-7000-8000-00000000ee01',
    ordinal: 1,
    product_id: '0190a8f2-0000-7000-8000-00000000cc01',
    product_label: 'Comet',
    generic_name: 'Metformin hydrochloride',
    strength: '500 mg',
    dose: '500 mg',
    frequency: 'twice daily',
    duration_days: 30,
    route: 'oral',
    rationale_en:
      'Glycaemic control is above target on the current regimen and metformin is the usual first step here.',
    rationale_bn:
      'বর্তমান ব্যবস্থাপত্রে রক্তে শর্করার নিয়ন্ত্রণ লক্ষ্যমাত্রার বাইরে; এখানে মেটফরমিনই সাধারণত প্রথম পছন্দ।',
    basis: ['obs.hba1c:2026-09-01'],
    offered_at: '2026-09-14T09:30:00Z',
    ...over,
  };
}

function run(over: Record<string, unknown> = {}) {
  return {
    id: '0190a8f2-0000-7000-8000-00000000ee01',
    prescription_id: PRESCRIPTION,
    state: 'READY',
    offered_count: 1,
    dropped_count: 0,
    suggestions: [suggestion()],
    message_en: 'AI-proposed, and not prescribed. Each one needs your accept, edit or reject.',
    message_bn: 'এআই-এর প্রস্তাব, ব্যবস্থাপত্র নয়। প্রতিটির জন্য আপনার গ্রহণ, সংশোধন বা বাতিল প্রয়োজন।',
    ...over,
  };
}

const REASONS = [
  {
    code: 'PREFER_ALTERNATIVE',
    label_en: 'Prefer a different agent in this class',
    label_bn: 'এই শ্রেণিতে অন্য ওষুধ পছন্দ',
    ordering: 70,
  },
  {
    code: 'COST',
    label_en: 'Patient cannot afford it',
    label_bn: 'রোগীর সাধ্যের বাইরে',
    ordering: 80,
  },
];

function panel(open = true, over: Record<string, unknown> = {}, locale: 'en' | 'bn' = 'en') {
  getSuggestions.mockResolvedValue(run(over));
  listRejectReasons.mockResolvedValue(REASONS);
  return renderWithProviders(
    <AISuggestionPanel
      prescriptionId={PRESCRIPTION}
      onDecided={() => {}}
      editable
      open={open}
      onToggle={() => {}}
    />,
    { locale },
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  decideSuggestion.mockResolvedValue({ decision: { decision: 'ACCEPTED' } });
});

describe('the AI suggestion panel', () => {
  it('is unreachable by the keyboard until it is deliberately opened', async () => {
    panel(false);
    await screen.findByTestId('ai-panel');

    const body = screen.getByTestId('ai-panel-body');
    // `inert` is the property. It removes the whole subtree from the tab order, from the
    // accessibility tree and from hit-testing — so the keyboard flow that moves between
    // prescription lines cannot arrive on an Accept button, and a stray Enter cannot press one.
    //
    // Asserted on the attribute rather than by tabbing, because a tab-order test passes for the
    // wrong reason the moment jsdom's focus model differs from a browser's, and this is the one
    // guarantee on this screen that would produce a wrongly prescribed medicine.
    expect(body.hasAttribute('inert')).toBe(true);
  });

  it('is reachable once it is open', async () => {
    panel(true);
    await screen.findByTestId('ai-suggestion');
    expect(screen.getByTestId('ai-panel-body').hasAttribute('inert')).toBe(false);
    expect(screen.getByTestId('ai-accept')).toBeInTheDocument();
  });

  it('has no shortcut that accepts, edits or rejects anything', () => {
    // The whole table, asserted by name. §3: accepting a suggestion must be a deliberate act with
    // its own target rather than the next press of the key you were already pressing — so the
    // panel gets one key and it opens the panel.
    const actions = PRESCRIPTION_SHORTCUTS.map((one) => one.action);
    for (const forbidden of ['accept', 'edit', 'reject', 'acceptAll']) {
      expect(actions).not.toContain(forbidden);
    }
    expect(actions).toContain('suggestions');
  });

  it('offers no control that answers more than one suggestion', async () => {
    getSuggestions.mockResolvedValue(
      run({
        suggestions: [
          suggestion(),
          suggestion({ id: '0190a8f2-0000-7000-8000-00000000dd02', ordinal: 2 }),
          suggestion({ id: '0190a8f2-0000-7000-8000-00000000dd03', ordinal: 3 }),
        ],
      }),
    );
    listRejectReasons.mockResolvedValue(REASONS);
    renderWithProviders(
      <AISuggestionPanel
        prescriptionId={PRESCRIPTION}
        onDecided={() => {}}
        editable
        open
        onToggle={() => {}}
      />,
    );
    await waitFor(() => expect(screen.getAllByTestId('ai-suggestion')).toHaveLength(3));

    // One Accept per suggestion and no more. A panel with four accept buttons for three
    // suggestions has an "accept all" in it under some other name.
    expect(screen.getAllByTestId('ai-accept')).toHaveLength(3);
    for (const button of screen.getAllByRole('button')) {
      expect(button.textContent ?? '').not.toMatch(/all/i);
    }
  });

  it('never draws a suggestion as a prescription line', async () => {
    panel(true);
    const card = await screen.findByTestId('ai-suggestion');

    // Not a numbered list item on the sheet: the sheet is `app-prescribe__line` inside an `<ol>`,
    // and nothing here shares that class. A physician reading at speed reads the shape first.
    expect(card.className).not.toContain('app-prescribe__line');
    expect(card.closest('ol')).toBeNull();
    // And it says what it is, in words, above the medicine.
    expect(card.textContent).toContain('Proposed — not on the prescription');
  });

  it('accepts one suggestion by its own button, carrying no content', async () => {
    const user = userEvent.setup();
    panel(true);
    await screen.findByTestId('ai-accept');
    await user.click(screen.getByTestId('ai-accept'));

    await waitFor(() => expect(decideSuggestion).toHaveBeenCalledTimes(1));
    const call = decideSuggestion.mock.calls[0]?.[0] as Record<string, unknown>;
    expect(call['decision']).toBe('ACCEPTED');
    expect(call['suggestionId']).toBe('0190a8f2-0000-7000-8000-00000000dd01');
    // The request names the suggestion and nothing about the medicine. A client that could send a
    // product could claim a provenance it did not earn.
    expect(call).not.toHaveProperty('productId');
    expect(call).not.toHaveProperty('label');
  });

  it('shows what was offered beside an edit, so the model’s dose is still on the screen', async () => {
    getSuggestions.mockResolvedValue(
      run({
        suggestions: [
          suggestion({
            decision: {
              id: '0190a8f2-0000-7000-8000-00000000ff01',
              suggestion_id: '0190a8f2-0000-7000-8000-00000000dd01',
              decision: 'EDITED',
              decided_by: '0190a8f2-0000-7000-8000-000000000001',
              decided_at: '2026-09-14T09:31:00Z',
              prescription_item_id: '0190a8f2-0000-7000-8000-00000000cc09',
            },
          }),
        ],
      }),
    );
    listRejectReasons.mockResolvedValue(REASONS);
    renderWithProviders(
      <AISuggestionPanel
        prescriptionId={PRESCRIPTION}
        onDecided={() => {}}
        editable
        open
        onToggle={() => {}}
      />,
    );

    const offered = await screen.findByTestId('ai-offered');
    expect(offered.textContent).toContain('500 mg');
    expect(screen.getByTestId('ai-decided').textContent).toContain(
      'Edited — your version is on the prescription',
    );
  });

  it('declines in one action, and offers the reason afterwards', async () => {
    const user = userEvent.setup();
    panel(true);
    await user.click(await screen.findByTestId('ai-reject'));

    // One call, with no reason in it. §5: a physician mid-clinic must be able to dismiss a
    // suggestion in one action, so a reason picker *before* the rejection would be a second
    // decision imposed on the moment he has least of them to spare.
    await waitFor(() => expect(decideSuggestion).toHaveBeenCalledTimes(1));
    expect(decideSuggestion.mock.calls[0]?.[0]).toMatchObject({ decision: 'REJECTED' });
    expect(decideSuggestion.mock.calls[0]?.[0]?.rejectReasonCode).toBeUndefined();
  });

  it('offers the reason list on a declined suggestion, bilingually', async () => {
    const user = userEvent.setup();
    getSuggestions.mockResolvedValue(
      run({
        suggestions: [
          suggestion({
            decision: {
              id: '0190a8f2-0000-7000-8000-00000000ff02',
              suggestion_id: '0190a8f2-0000-7000-8000-00000000dd01',
              decision: 'REJECTED',
              decided_by: '0190a8f2-0000-7000-8000-000000000001',
              decided_at: '2026-09-14T09:31:00Z',
            },
          }),
        ],
      }),
    );
    listRejectReasons.mockResolvedValue(REASONS);
    renderWithProviders(
      <AISuggestionPanel
        prescriptionId={PRESCRIPTION}
        onDecided={() => {}}
        editable
        open
        onToggle={() => {}}
      />,
      { locale: 'bn' },
    );

    await user.click(await screen.findByTestId('ai-add-reason'));
    const list = await screen.findByTestId('ai-reason-list');
    // The Bengali labels, not the English ones. A reason list that fell back to English is a list
    // a Bengali-reading physician stops using, and the signal §5 exists to collect goes with it.
    expect(list.textContent).toContain('এই শ্রেণিতে অন্য ওষুধ পছন্দ');
    expect(list.textContent).not.toContain('Prefer a different agent');
  });

  it('does not draw an unanswered suggestion as a declined one', async () => {
    panel(true);
    await screen.findByTestId('ai-suggestion');

    // It is in the waiting list, it has all three buttons, and the word "declined" is nowhere on
    // the card. Conflating "he said no" with "he did not look" is the failure §1 names.
    const card = screen.getByTestId('ai-suggestion');
    expect(card.dataset.state).toBe('UNACTIONED');
    expect(card.textContent).not.toMatch(/declined/i);
    expect(screen.queryByTestId('ai-decided')).toBeNull();
    expect(screen.getByTestId('ai-pending-count').textContent).toContain('1');
  });

  it('says the AI proposed nothing, which is not the same as nobody having asked', async () => {
    getSuggestions.mockResolvedValue(
      run({
        suggestions: [],
        offered_count: 0,
        message_en: 'The AI proposed nothing for this patient. That is an answer, not a failure.',
        message_bn: 'এই রোগীর জন্য এআই কিছু প্রস্তাব করেনি। এটি একটি উত্তর, ব্যর্থতা নয়।',
      }),
    );
    listRejectReasons.mockResolvedValue(REASONS);
    renderWithProviders(
      <AISuggestionPanel
        prescriptionId={PRESCRIPTION}
        onDecided={() => {}}
        editable
        open
        onToggle={() => {}}
      />,
    );

    const message = await screen.findByTestId('ai-state-message');
    expect(message.textContent).toContain('proposed nothing');
    expect(message.textContent).toContain('an answer, not a failure');
  });

  it('says why nothing was asked when the patient has no allergy status', async () => {
    getSuggestions.mockResolvedValue(
      run({
        state: 'REFUSED',
        refusal: 'NO_ALLERGY_STATUS',
        suggestions: [],
        message_en:
          'No suggestion was asked for: this patient has no recorded allergy status. Record it at the history station first — nothing is proposed around that stop.',
        message_bn:
          'কোনো প্রস্তাব চাওয়া হয়নি: এই রোগীর অ্যালার্জির অবস্থা নথিতে নেই। আগে ইতিহাস কেন্দ্রে সেটি লিখুন — ওই বাধা এড়িয়ে কিছু প্রস্তাব করা হয় না।',
      }),
    );
    listRejectReasons.mockResolvedValue(REASONS);
    renderWithProviders(
      <AISuggestionPanel
        prescriptionId={PRESCRIPTION}
        onDecided={() => {}}
        editable
        open
        onToggle={() => {}}
      />,
    );

    // The physician can clear this in thirty seconds, but only if the screen says which stop it
    // was. "No suggestions" would leave him thinking the model had nothing to add.
    const message = await screen.findByTestId('ai-state-message');
    expect(message.textContent).toContain('allergy status');
    expect(message.textContent).toContain('history station');
  });

  it('reads in Bengali throughout', async () => {
    panel(true, {}, 'bn');
    const card = await screen.findByTestId('ai-suggestion');
    expect(card.textContent).toContain('প্রস্তাবিত — ব্যবস্থাপত্রে নেই');
    expect(card.textContent).toContain('এখানে মেটফরমিনই সাধারণত প্রথম পছন্দ');
    expect(screen.getByTestId('ai-accept').textContent).toContain('গ্রহণ করুন');
    // The English rationale is not on the Bengali screen at all.
    expect(card.textContent).not.toContain('Glycaemic control');
  });
});
