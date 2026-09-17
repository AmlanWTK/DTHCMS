'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useEffect, useRef, useState } from 'react';

import { Button, Icon } from '@dthcms/ui';

import type { Locale } from '@/lib/i18n/config';

import {
  REJECT_REASONS_KEY,
  aiSuggestionsKey,
  askForSuggestions,
  decideSuggestion,
  getSuggestions,
  isUnactioned,
  listRejectReasons,
  suggestionState,
  type AIPrescribingSuggestion,
} from '../api/aiSuggestions';

/**
 * What the AI proposed, and the three things a physician can do about each one (CP82).
 *
 * > *Automation bias is the named risk in the plan, and it is the real one. A physician who has
 * > accepted forty correct suggestions will accept the forty-first without reading it.*
 *
 * Every decision below follows from that sentence.
 *
 * # 1. It is a column, and it never looks like a prescription line
 *
 * The sheet is `<ol class="app-prescribe__lines">` — numbered, solid-bordered, in the main column.
 * A suggestion is a card in a separate aside with a dashed border, a tinted ground, no line number
 * and the word "proposed" in its heading. The two are never adjacent and never share a style.
 *
 * The moment a suggestion is accepted it **leaves this panel's active list and appears on the
 * sheet**, because from that moment it is an ordinary prescription line and drawing it as anything
 * else would be the lie in the other direction. What stays here is a one-line record of what was
 * decided, which is the trail §6 says CP82 collects and uses for nothing yet.
 *
 * # 2. The keyboard cannot arrive here by accident
 *
 * `Alt+L` focuses the prescription lines and `Tab` walks the row being typed. **Neither reaches
 * this panel.** The container is `inert` until the physician deliberately opens it with `Alt+J`,
 * which is a key bound to nothing else and sits nowhere near the keys a four-medicine prescription
 * is made of.
 *
 * `inert` rather than `tabIndex={-1}` on the children: a negative tab index removes an element from
 * the tab order and leaves it clickable and focusable by script, and the property wanted here is
 * that the *keyboard flow between prescription lines* cannot land on an accept button. `inert`
 * removes the whole subtree from focus, from the accessibility tree and from hit-testing, and
 * turning it off is an act.
 *
 * There is no key that accepts a suggestion. Accept, edit and reject are buttons, reached by Tab
 * **within the opened panel**, and each one is a click or an Enter on a focused control that says
 * what it does. A shortcut for "accept" would be exactly the next press of a key somebody was
 * already pressing, which is the thing §3 forbids.
 *
 * # 3. There is no "accept all", and there is nothing here to add one to
 *
 * No control takes more than one suggestion, and `decideSuggestion` takes one id. The panel header
 * shows a count and no action.
 *
 * # 4. Dismissing is one action; giving a reason is one more
 *
 * §5: *"a physician mid-clinic with a patient in front of him should be able to dismiss a
 * suggestion in one action."* So **Reject rejects**, immediately, with no reason — and the reason
 * list appears afterwards, on the decided card, as an optional second act.
 *
 * That ordering is deliberate and it is the one thing here I would want the consultant to press
 * on. A reason picker *before* the rejection collects more reasons and makes dismissing cost two
 * decisions; this way dismissing costs one and the reason is genuinely optional, which is what the
 * specification asks for and what will produce fewer reasons.
 */

export interface AISuggestionPanelProps {
  prescriptionId: string;
  /** Called after any decision, so the editor can refresh the sheet and re-run the safety check. */
  onDecided: () => void;
  /** True when the prescription is still a draft. Decisions are refused on anything else. */
  editable: boolean;
  /**
   * Whether the panel is open for the keyboard.
   *
   * Owned by the editor rather than by this component, because the key that opens it is bound
   * beside the editor's other shortcuts — one table, one help card, and no second place where a
   * key is claimed. While it is false the whole body is `inert`.
   */
  open: boolean;
  onToggle: () => void;
}

export function AISuggestionPanel({
  prescriptionId,
  onDecided,
  editable,
  open,
  onToggle,
}: AISuggestionPanelProps) {
  const t = useTranslations('prescriptions.ai');
  const locale = useLocale() as Locale;
  const queryClient = useQueryClient();

  const [editing, setEditing] = useState<string | null>(null);
  const [editDose, setEditDose] = useState('');
  const [editFrequency, setEditFrequency] = useState('');
  const [editDuration, setEditDuration] = useState('');
  const [reasoningFor, setReasoningFor] = useState<string | null>(null);
  const [problem, setProblem] = useState<string | null>(null);
  const panelRef = useRef<HTMLDivElement>(null);

  const run = useQuery({
    queryKey: aiSuggestionsKey(prescriptionId),
    queryFn: () => getSuggestions(prescriptionId),
  });

  const reasons = useQuery({
    queryKey: REJECT_REASONS_KEY,
    queryFn: listRejectReasons,
    staleTime: 5 * 60_000,
  });

  const ask = useMutation({
    mutationFn: () => askForSuggestions(prescriptionId),
    onSuccess: (fresh) => {
      queryClient.setQueryData(aiSuggestionsKey(prescriptionId), fresh);
      setProblem(null);
      if (!open) onToggle();
    },
    onError: (error: Error) => setProblem(error.message),
  });

  const decide = useMutation({
    mutationFn: (input: Parameters<typeof decideSuggestion>[0]) => decideSuggestion(input),
    onSuccess: async () => {
      setEditing(null);
      setProblem(null);
      await queryClient.invalidateQueries({ queryKey: aiSuggestionsKey(prescriptionId) });
      onDecided();
    },
    onError: (error: Error) => setProblem(error.message),
  });

  const suggestions = run.data?.suggestions ?? [];
  const pending = suggestions.filter(isUnactioned);
  const decided = suggestions.filter((one) => !isUnactioned(one));
  const state = run.data?.state ?? '';

  // Focus moves into the panel only when it is opened, and only then. A physician who pressed the
  // key meant to go there; a physician writing a line never does, because nothing else in this
  // feature calls `onToggle`.
  useEffect(() => {
    if (open) panelRef.current?.focus();
  }, [open]);

  function startEdit(suggestion: AIPrescribingSuggestion) {
    setEditing(suggestion.id);
    setEditDose(suggestion.dose);
    setEditFrequency(suggestion.frequency);
    setEditDuration(suggestion.duration_days ? String(suggestion.duration_days) : '');
  }

  return (
    <aside
      className="app-ai-panel"
      aria-labelledby="app-ai-panel-heading"
      data-testid="ai-panel"
      data-open={open ? 'true' : 'false'}
    >
      <header className="app-ai-panel__head">
        <h2 id="app-ai-panel-heading" className="app-ai-panel__heading">
          <Icon name="flask-conical" size={16} aria-hidden />
          {t('title')}
        </h2>
        {/* A count and no action. There is no control here that answers more than one
            suggestion — see the file header. */}
        <p className="app-ai-panel__count" data-testid="ai-pending-count">
          {t('pendingCount', { count: pending.length })}
        </p>
      </header>

      {/* One sentence, not two. The run carries its own, which is more specific — "the AI
          proposed nothing", "this patient has no recorded allergy status" — so the static lede
          is shown only before anything has been asked, where there is no run to speak for it. */}
      {run.data?.message_en ? null : <p className="app-ai-panel__lede">{t('notPrescribed')}</p>}

      <div className="app-ai-panel__actions">
        <Button
          variant="secondary"
          onClick={() => ask.mutate()}
          disabled={ask.isPending || !editable}
          data-testid="ai-ask"
        >
          {run.data && state ? t('askAgain') : t('ask')}
        </Button>
        <Button
          variant="secondary"
          onClick={onToggle}
          aria-expanded={open}
          aria-controls="app-ai-panel-body"
          data-testid="ai-toggle"
        >
          {open ? t('close') : t('openWithKey')}
        </Button>
      </div>

      {run.data?.message_en ? (
        <p className="app-ai-panel__state" data-testid="ai-state-message">
          {locale === 'bn' ? run.data.message_bn : run.data.message_en}
        </p>
      ) : null}

      {/* `inert` is the whole of §2. While it is set, nothing inside can be reached by Tab, by a
          screen reader's focus, or by a click — so the keyboard flow that moves between
          prescription lines cannot arrive on an accept button, and neither can a stray Enter. */}
      <div
        id="app-ai-panel-body"
        ref={panelRef}
        className="app-ai-panel__body"
        // React 19 forwards `inert` as a boolean attribute.
        inert={!open}
        tabIndex={open ? -1 : undefined}
        data-testid="ai-panel-body"
      >
        {pending.length === 0 && decided.length === 0 ? (
          <p className="app-ai-panel__empty" data-testid="ai-empty">
            {t('nothingProposed')}
          </p>
        ) : null}

        <ul className="app-ai-panel__list">
          {pending.map((suggestion) => (
            <li
              key={suggestion.id}
              className="app-ai-card"
              data-testid="ai-suggestion"
              data-state="UNACTIONED"
            >
              <p className="app-ai-card__tag">{t('proposed')}</p>
              <p className="app-ai-card__medicine">
                <strong>
                  {suggestion.product_label} {suggestion.strength}
                </strong>
                <span>{suggestion.generic_name}</span>
              </p>
              <p className="app-ai-card__directions">
                {[
                  suggestion.dose,
                  suggestion.frequency,
                  suggestion.duration_days ? t('days', { count: suggestion.duration_days }) : '',
                  suggestion.route,
                ]
                  .filter(Boolean)
                  .join(' · ')}
              </p>
              <p className="app-ai-card__why">
                {locale === 'bn' ? suggestion.rationale_bn : suggestion.rationale_en}
              </p>
              {/* The facts it rests on. §2 asks for a suggestion a physician can audit in five
                  seconds, and the references are what make that possible rather than a claim.

                  `basis_en`/`basis_bn` and not `basis`: the raw reference is a storage format —
                  `obs.hba1c:2026-09-01` — and this panel was showing it to a physician scanning
                  three cards. The server renders it (ADR-0038); the raw list stays as the
                  `title`, because it is what an engineer greps for. The fallback is the raw list
                  rather than an empty line, so a suggestion stored before this shipped still
                  says what it rests on. */}
              <p className="app-ai-card__basis" title={suggestion.basis.join(' · ')}>
                {t('basedOn')}{' '}
                {(
                  (locale === 'bn' ? suggestion.basis_bn : suggestion.basis_en) ?? suggestion.basis
                ).join(' · ')}
              </p>

              {editing === suggestion.id ? (
                <form
                  className="app-ai-card__edit"
                  onSubmit={(event) => {
                    event.preventDefault();
                    decide.mutate({
                      prescriptionId,
                      suggestionId: suggestion.id,
                      decision: 'EDITED',
                      edit: {
                        dose: editDose.trim(),
                        frequency: editFrequency.trim(),
                        durationDays: editDuration ? Number(editDuration) : undefined,
                      },
                    });
                  }}
                >
                  <p className="app-ai-card__edit-note">{t('editKeepsTheOffer')}</p>
                  <label className="app-prescribe__field">
                    <span>{t('dose')}</span>
                    <input
                      className="app-input"
                      value={editDose}
                      autoFocus
                      onChange={(event) => setEditDose(event.target.value)}
                      data-testid="ai-edit-dose"
                    />
                  </label>
                  <label className="app-prescribe__field">
                    <span>{t('frequency')}</span>
                    <input
                      className="app-input"
                      value={editFrequency}
                      onChange={(event) => setEditFrequency(event.target.value)}
                      data-testid="ai-edit-frequency"
                    />
                  </label>
                  <label className="app-prescribe__field app-prescribe__field--narrow">
                    <span>{t('duration')}</span>
                    <input
                      className="app-input"
                      inputMode="numeric"
                      value={editDuration}
                      onChange={(event) => setEditDuration(event.target.value)}
                      data-testid="ai-edit-duration"
                    />
                  </label>
                  <div className="app-ai-card__buttons">
                    <Button type="submit" disabled={decide.isPending} data-testid="ai-edit-save">
                      {t('editAndPut')}
                    </Button>
                    <Button type="button" variant="secondary" onClick={() => setEditing(null)}>
                      {t('cancel')}
                    </Button>
                  </div>
                </form>
              ) : (
                <div className="app-ai-card__buttons">
                  <Button
                    onClick={() =>
                      decide.mutate({
                        prescriptionId,
                        suggestionId: suggestion.id,
                        decision: 'ACCEPTED',
                      })
                    }
                    disabled={decide.isPending || !editable}
                    data-testid="ai-accept"
                  >
                    {t('accept')}
                  </Button>
                  <Button
                    variant="secondary"
                    onClick={() => startEdit(suggestion)}
                    disabled={decide.isPending || !editable}
                    data-testid="ai-edit"
                  >
                    {t('edit')}
                  </Button>
                  {/* One action. The reason list is offered afterwards, on the decided card:
                      §5 requires dismissing to cost one decision, not two. */}
                  <Button
                    variant="secondary"
                    onClick={() =>
                      decide.mutate({
                        prescriptionId,
                        suggestionId: suggestion.id,
                        decision: 'REJECTED',
                      })
                    }
                    disabled={decide.isPending || !editable}
                    data-testid="ai-reject"
                  >
                    {t('reject')}
                  </Button>
                </div>
              )}
            </li>
          ))}
        </ul>

        {decided.length > 0 ? (
          <section className="app-ai-panel__decided">
            <h3 className="app-ai-panel__subheading">{t('decidedTitle')}</h3>
            <ul className="app-ai-panel__list">
              {decided.map((suggestion) => {
                const state = suggestionState(suggestion);
                return (
                  <li
                    key={suggestion.id}
                    className="app-ai-card app-ai-card--decided"
                    data-testid="ai-decided"
                    data-state={state}
                  >
                    <p className="app-ai-card__tag">{t(`state.${state}`)}</p>
                    <p className="app-ai-card__medicine">
                      <strong>
                        {suggestion.product_label} {suggestion.strength}
                      </strong>
                    </p>
                    {/* What was offered, still readable after an edit changed the line. The
                        suggestion is stored as offered and is never rewritten, which is what
                        makes this a record rather than a copy of the line. */}
                    <p className="app-ai-card__offered" data-testid="ai-offered">
                      {t('asOffered', {
                        dose: suggestion.dose,
                        frequency: suggestion.frequency,
                      })}
                    </p>

                    {state === 'REJECTED' && !suggestion.decision?.reject_reason_code ? (
                      reasoningFor === suggestion.id ? (
                        <div className="app-ai-card__reasons" data-testid="ai-reason-list">
                          <p className="app-ai-card__edit-note">{t('reasonOptional')}</p>
                          <ul className="app-ai-card__reason-list">
                            {(reasons.data ?? []).map((reason) => (
                              <li key={reason.code}>
                                <button
                                  type="button"
                                  className="app-ai-card__reason"
                                  onClick={() =>
                                    decide.mutate({
                                      prescriptionId,
                                      suggestionId: suggestion.id,
                                      decision: 'REJECTED',
                                      rejectReasonCode: reason.code,
                                    })
                                  }
                                >
                                  {locale === 'bn' ? reason.label_bn : reason.label_en}
                                </button>
                              </li>
                            ))}
                          </ul>
                        </div>
                      ) : (
                        <button
                          type="button"
                          className="app-ai-card__link"
                          onClick={() => setReasoningFor(suggestion.id)}
                          data-testid="ai-add-reason"
                        >
                          {t('addReason')}
                        </button>
                      )
                    ) : null}

                    {suggestion.decision?.reject_reason_code ? (
                      <p className="app-ai-card__reason-given" data-testid="ai-reason-given">
                        {labelFor(reasons.data, suggestion.decision.reject_reason_code, locale)}
                      </p>
                    ) : null}
                  </li>
                );
              })}
            </ul>
          </section>
        ) : null}
      </div>

      {problem ? (
        <p className="app-prescribe__problem" role="alert" data-testid="ai-problem">
          {problem}
        </p>
      ) : null}
    </aside>
  );
}

/**
 * The label for a code, in the reader's language.
 *
 * Falls back to the code rather than to English. A rejection recorded against a reason that has
 * since been retired still has to render, and showing the code is honest where showing nothing
 * would look like a rejection with no reason — which is a different fact.
 */
function labelFor(
  reasons: { code: string; label_en: string; label_bn: string }[] | undefined,
  code: string,
  locale: Locale,
): string {
  const found = (reasons ?? []).find((reason) => reason.code === code);
  if (!found) return code;
  return locale === 'bn' ? found.label_bn : found.label_en;
}
