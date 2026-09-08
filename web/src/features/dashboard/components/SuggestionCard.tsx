'use client';

import { useLocale, useTranslations } from 'next-intl';
import { useEffect, useRef, useState } from 'react';

import { Badge, Button, Icon, cx } from '@dthcms/ui';

import { useDirectory } from '@/features/attribution';
import { formatDateTime } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';

import type { DashboardSuggestion, SuggestionDecisionKind } from '../api/dashboard';

import { OriginMark } from './AiMarked';
import { StatusWord } from './StatusWord';
import { gapSentence } from './dashboardText';

/**
 * One item in §8's right panel, with accept, edit and reject against it (CP73).
 *
 * # The three buttons are a record, not a colour
 *
 * Pressing one writes an `AI_SUGGESTION_DECIDED` event into the ledger with the physician's
 * name, the time, the generation it was decided against and — for an edit — their own wording.
 * The panel then shows it, and keeps showing it across refreshes, because a decision the
 * screen forgets is a decision the physician makes again every time the summary re-runs.
 *
 * **Accepting records an intent. It does not write a prescription.** The card says so, in
 * words, under a drafted medication — not in a tooltip and not only in a document. §7.3 makes
 * the split permanent (generative models draft; deterministic databases and a human signature
 * prescribe), CP81 owns the prescription, and a physician who pressed *Accept* on a drafted
 * metformin and assumed it was on the prescription would have been misled by this screen.
 * That sentence is the whole reason the label reads *Accept as intent* rather than *Accept*.
 *
 * # Why rejecting takes no reason
 *
 * The note box is offered and never required. A physician made to justify nine rejections in a
 * morning stops rejecting, and starts leaving the panel alone — which destroys the one
 * measurement that says whether the panel is any good.
 *
 * # Why an edit is a different act from an acceptance
 *
 * "Agreed" and "agreed, with this changed to that" are different records, and the second is
 * the more useful one: it is the clinician correcting the model in their own words, which is
 * the training signal a prompt change should be argued from. So the edit box is a real field
 * with the model's text in it, and submitting it unchanged is still an edit — because saying
 * "I looked at this and rewrote it identically" is a claim the physician made.
 *
 * # Focus
 *
 * The card is a `<li>` with `tabIndex={-1}` so the keyboard shortcuts can move focus onto it
 * without putting every card into the tab order — a panel of eight cards each with four
 * controls is thirty-two tab stops, and a physician tabbing to the print button would pass
 * through all of them. Arrow keys move between cards; Tab moves through the controls of the
 * one that has focus. See `useDashboardShortcuts`.
 */

export interface SuggestionCardProps {
  suggestion: DashboardSuggestion;
  /** The run the panel is currently showing, for the stale-decision note. */
  generation: number;
  onDecide: (input: {
    ref: string;
    decision: SuggestionDecisionKind;
    edited?: string;
    note?: string;
  }) => void;
  pending: boolean;
  /** Whether the caller may answer a suggestion at all. Reading and answering are separate. */
  mayDecide: boolean;
}

export function SuggestionCard({
  suggestion,
  generation,
  onDecide,
  pending,
  mayDecide,
}: SuggestionCardProps) {
  const t = useTranslations('dashboard.assistant');
  const tGap = useTranslations('dashboard');
  const locale = useLocale() as Locale;
  const lookup = useDirectory();

  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(suggestion.label);
  const [note, setNote] = useState('');
  const editRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    if (editing) editRef.current?.focus();
  }, [editing]);

  const decided = suggestion.decision;
  const stale = decided !== undefined && decided.generation < generation;

  const label =
    suggestion.kind === 'GAP'
      ? gapSentence(suggestion.label, suggestion.detail ?? suggestion.label, tGap)
      : suggestion.label;

  return (
    <li
      className={cx('dash-suggestion', decided && 'dash-suggestion--decided')}
      data-testid={`suggestion-${suggestion.ref}`}
      data-kind={suggestion.kind}
      data-origin={suggestion.origin}
      data-decision={decided?.kind ?? 'none'}
      tabIndex={-1}
      aria-label={`${t(`kind.${suggestion.kind}`)}: ${label}`}
    >
      <header className="dash-suggestion__head">
        <Badge tone="neutral">{t(`kind.${suggestion.kind}`)}</Badge>
        {/* The origin mark, per item, because this panel is a mixture: a model's proposal and
            the assembler's own arithmetic look identical on a screen and are not the same kind
            of claim. Marking the whole panel as AI would teach a physician to discount the one
            item on it that is certainly true. */}
        <OriginMark origin={suggestion.origin} />
        {suggestion.severity && (
          // A gap's two levels, drawn as a status rather than as a badge: `StatusPill` is
          // the only clinical marker in the design system that carries an icon and a word
          // as well as a tint, and "important" beside a missing eye examination is a
          // clinical statement rather than a category.
          <StatusWord status={suggestion.severity === 'important' ? 'borderline' : 'unknown'}>
            {t(`severity.${suggestion.severity}`)}
          </StatusWord>
        )}
      </header>

      <p className="dash-suggestion__label">
        <strong>{label}</strong>
        {suggestion.icd10 && <Badge tone="neutral">{suggestion.icd10}</Badge>}
      </p>

      {(suggestion.dose || suggestion.frequency || suggestion.route) && (
        <p className="dash-suggestion__dose">
          {[suggestion.dose, suggestion.frequency, suggestion.route].filter(Boolean).join(' · ')}
        </p>
      )}

      {suggestion.detail && suggestion.kind !== 'GAP' && (
        <p className="dash-suggestion__detail">{suggestion.detail}</p>
      )}

      {suggestion.basis && suggestion.basis.length > 0 && (
        // Criterion 5 for a machine-written line. A suggestion has no author to name, so its
        // attribution is the evidence it was drawn from — and it is one interaction away, in
        // a `<details>`, like every other attribution on this screen.
        <details className="dash-suggestion__basis" data-testid={`basis-${suggestion.ref}`}>
          <summary>{t('basis.open', { count: suggestion.basis.length })}</summary>
          <ul>
            {suggestion.basis.map((ref) => (
              <li key={ref}>
                <code>{ref}</code>
              </li>
            ))}
          </ul>
        </details>
      )}

      {suggestion.kind === 'MEDICATION' && (
        // Said under every drafted drug, every time, and not once at the top of the panel. A
        // physician scrolling to the fourth medication has left the panel header behind, and
        // this is the sentence that stops *Accept* being read as *Prescribe*.
        <p className="dash-suggestion__notprescription">{t('notAPrescription')}</p>
      )}

      {decided ? (
        <div className="dash-suggestion__decision" data-testid={`decision-${suggestion.ref}`}>
          <p className="dash-suggestion__decision-line">
            <Icon name={decided.kind === 'REJECTED' ? 'x' : 'check'} aria-hidden />
            <strong>{t(`decision.${decided.kind}`)}</strong>
            <span>
              {t('decidedBy', {
                who: lookup.person(decided.decided_by)?.name ?? t('someone'),
                at: formatDateTime(Date.parse(decided.decided_at), locale),
              })}
            </span>
          </p>
          {decided.edited && <p className="dash-suggestion__edited">{decided.edited}</p>}
          {decided.note && <p className="dash-suggestion__note">{decided.note}</p>}
          {stale && (
            // Shown rather than hidden, and shown rather than silently treated as current.
            // The decision was about a sentence drawn from different data; whether it still
            // stands is a clinical judgement, not something a fold should make.
            <p className="dash-suggestion__stale">
              {t('decidedAgainstOlderRun', { generation: decided.generation })}
            </p>
          )}
          {mayDecide && (
            <Button
              variant="quiet"
              size="sm"
              onClick={() => setEditing(true)}
              data-testid={`redecide-${suggestion.ref}`}
            >
              {t('changeYourMind')}
            </Button>
          )}
        </div>
      ) : null}

      {mayDecide && (editing || !decided) && (
        <div className="dash-suggestion__actions">
          {editing ? (
            <div className="dash-suggestion__editor">
              <label className="dash-suggestion__field">
                <span>{t('editLabel')}</span>
                <textarea
                  ref={editRef}
                  className="app-input"
                  rows={2}
                  value={draft}
                  onChange={(event) => setDraft(event.target.value)}
                  data-testid={`edit-${suggestion.ref}`}
                />
              </label>
              <label className="dash-suggestion__field">
                <span>{t('noteLabel')}</span>
                <textarea
                  className="app-input"
                  rows={2}
                  value={note}
                  onChange={(event) => setNote(event.target.value)}
                  data-testid={`note-${suggestion.ref}`}
                />
              </label>
              <div className="dash-suggestion__buttons">
                <Button
                  size="sm"
                  disabled={pending || draft.trim() === ''}
                  onClick={() => {
                    onDecide({
                      ref: suggestion.ref,
                      decision: 'EDITED',
                      edited: draft.trim(),
                      note: note.trim() === '' ? undefined : note.trim(),
                    });
                    setEditing(false);
                  }}
                  data-testid={`save-edit-${suggestion.ref}`}
                >
                  {t('saveEdit')}
                </Button>
                <Button variant="quiet" size="sm" onClick={() => setEditing(false)}>
                  {t('cancel')}
                </Button>
              </div>
            </div>
          ) : (
            <div className="dash-suggestion__buttons">
              <Button
                size="sm"
                disabled={pending}
                onClick={() => onDecide({ ref: suggestion.ref, decision: 'ACCEPTED' })}
                data-testid={`accept-${suggestion.ref}`}
              >
                <Icon name="check" aria-hidden />
                {/* "Accept as intent", not "Accept". The extra two words are the whole
                    difference between a record of agreement and a prescription, and CP81 owns
                    the second one. */}
                {t('accept')}
              </Button>
              <Button
                variant="secondary"
                size="sm"
                disabled={pending}
                onClick={() => setEditing(true)}
                data-testid={`start-edit-${suggestion.ref}`}
              >
                {t('edit')}
              </Button>
              <Button
                variant="quiet"
                size="sm"
                disabled={pending}
                onClick={() => onDecide({ ref: suggestion.ref, decision: 'REJECTED' })}
                data-testid={`reject-${suggestion.ref}`}
              >
                <Icon name="x" aria-hidden />
                {t('reject')}
              </Button>
            </div>
          )}
        </div>
      )}
    </li>
  );
}
