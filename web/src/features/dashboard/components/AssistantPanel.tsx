'use client';

import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useTranslations } from 'next-intl';
import { useState } from 'react';

import { ApiError } from '@/lib/api';
import { usePermission } from '@/lib/use-permission';

import {
  decideSuggestion,
  omissionFor,
  suggestionsInWorkingOrder,
  type PhysicianDashboard,
  type SuggestionDecisionKind,
} from '../api/dashboard';

import { AiMarked } from './AiMarked';
import { SuggestionCard } from './SuggestionCard';
import { WithheldNote } from './WithheldNote';

/**
 * §8's right panel: suggested diagnoses, missing-data alerts and drafted investigations and
 * doses, each with accept, edit and reject (CP73).
 *
 * # The panel is a mixture and is drawn as one
 *
 * §8 puts two very different things in this column. A *suggested ICD-coded diagnosis* is a
 * language model's proposal. A *missing-data alert* — "eye exam overdue 6 months" — is
 * arithmetic the deterministic assembler did over the record, with no model involved at all;
 * it is available even when the model failed, and it is one of the few things on this screen
 * that is simply true.
 *
 * Two consequences follow, and both are safety properties rather than presentation choices:
 *
 *   - **The AI enclosure goes around the model's items only.** Wrapping the whole panel would
 *     mark the assembler's arithmetic as machine-written, which trains a physician to discount
 *     the one group here they should not.
 *   - **Every item says which it is anyway**, in a word, because the two groups sit in one
 *     column and a boundary can be scrolled past or cropped out.
 *
 * # Why the deterministic half is drawn first
 *
 * It is the half that survives a failed run, and it is the half a physician can act on without
 * weighing anything. A panel that led with a model's differential and buried "no HbA1c in
 * twelve months" underneath would be putting the least certain item where the eye lands.
 *
 * # What a decision does
 *
 * It writes a row in the ledger. It does not write a prescription — see `SuggestionCard`, and
 * see §7.3, which makes that split permanent. The optimistic path is deliberately *not* taken:
 * the panel waits for the server and re-reads, because a decision that appeared to succeed and
 * did not would leave a physician believing the record says something it does not.
 */

export interface AssistantPanelProps {
  view: PhysicianDashboard;
}

export function AssistantPanel({ view }: AssistantPanelProps) {
  const t = useTranslations('dashboard.assistant');
  const client = useQueryClient();
  const mayDecide = usePermission('dashboard.suggestions.decide');
  const [failure, setFailure] = useState<string | null>(null);

  const withheld = omissionFor(view, 'summary');
  const assistant = view.assistant;

  const decide = useMutation({
    mutationFn: (input: {
      ref: string;
      decision: SuggestionDecisionKind;
      edited?: string;
      note?: string;
    }) =>
      decideSuggestion({
        patientId: view.patient.id,
        visitId: view.visit?.id ?? '',
        ...input,
      }),
    onSuccess: () => {
      setFailure(null);
      // The whole patient prefix. A decision changes the right panel and nothing else today,
      // and invalidating narrowly would be an optimisation whose correctness depends on that
      // staying true — which it will not the day a decision starts feeding a prescription.
      void client.invalidateQueries({ queryKey: ['patient', view.patient.id] });
    },
    onError: (error) => {
      // The one refusal with a specific remedy: the summary re-ran and this reference is not
      // in the current one. Saying "reload" is the difference between a physician pressing the
      // button again and a physician going to look for a colleague who changed nothing.
      setFailure(
        error instanceof ApiError && error.code === 'DASHBOARD_SUGGESTION_STALE'
          ? 'stale'
          : 'failed',
      );
    },
  });

  if (withheld) {
    return (
      <div className="dash-panel dash-panel--assistant" data-testid="assistant-panel">
        <h2 className="dash-panel__title">{t('title')}</h2>
        <WithheldNote omission={withheld} />
      </div>
    );
  }

  if (!assistant) {
    return (
      <div className="dash-panel dash-panel--assistant" data-testid="assistant-panel">
        <h2 className="dash-panel__title">{t('title')}</h2>
        <p className="dash-card__empty">{t('noVisit')}</p>
      </div>
    );
  }

  const ordered = suggestionsInWorkingOrder(assistant.suggestions);
  const fromRecord = ordered.filter((item) => item.origin === 'SYSTEM');
  const fromModel = ordered.filter((item) => item.origin === 'MODEL');

  return (
    <div className="dash-panel dash-panel--assistant" data-testid="assistant-panel">
      <h2 className="dash-panel__title" id="dash-assistant-heading">
        {t('title')}
      </h2>

      {failure && (
        <p className="dash-panel__error" role="alert" data-testid="assistant-error">
          {t(failure === 'stale' ? 'staleError' : 'decideFailed')}
        </p>
      )}

      {ordered.length === 0 && (
        // Empty, and empty is a fact: nothing has been suggested for this visit. Different
        // again from the withheld case above, which is why the two are separate branches.
        <p className="dash-card__empty" data-testid="assistant-empty">
          {t('nothingYet')}
        </p>
      )}

      {fromRecord.length > 0 && (
        <section className="dash-group" data-testid="assistant-system">
          <h3 className="dash-group__title">{t('fromRecord')}</h3>
          {/* Deliberately outside `AiMarked`. These are the assembler's findings, computed by
              code from stored facts; marking them as machine-written would be marking
              arithmetic as an opinion. */}
          <p className="dash-group__note">{t('fromRecordNote')}</p>
          <ul className="dash-suggestions">
            {fromRecord.map((suggestion) => (
              <SuggestionCard
                key={suggestion.ref}
                suggestion={suggestion}
                generation={assistant.generation}
                onDecide={(input) => decide.mutate(input)}
                pending={decide.isPending}
                mayDecide={mayDecide && (view.visit?.open ?? false)}
              />
            ))}
          </ul>
        </section>
      )}

      {fromModel.length > 0 && (
        <AiMarked label={t('fromModel')} testId="assistant-ai-region">
          <section className="dash-group" data-testid="assistant-model">
            <h3 className="dash-group__title">{t('fromModel')}</h3>
            <ul className="dash-suggestions">
              {fromModel.map((suggestion) => (
                <SuggestionCard
                  key={suggestion.ref}
                  suggestion={suggestion}
                  generation={assistant.generation}
                  onDecide={(input) => decide.mutate(input)}
                  pending={decide.isPending}
                  mayDecide={mayDecide && (view.visit?.open ?? false)}
                />
              ))}
            </ul>
          </section>
        </AiMarked>
      )}

      {!view.visit?.open && ordered.length > 0 && (
        // A closed visit's drafts are a record rather than a decision to make. The buttons
        // are gone and the sentence says why, because a physician who found three disabled
        // buttons and no explanation would assume a permission problem.
        <p className="dash-panel__note" data-testid="assistant-visit-closed">
          {t('visitClosed')}
        </p>
      )}
    </div>
  );
}
