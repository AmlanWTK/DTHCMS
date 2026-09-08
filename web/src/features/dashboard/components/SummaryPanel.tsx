'use client';

import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';

import { Button, Icon } from '@dthcms/ui';

import { formatDateTime } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';
import { usePermission } from '@/lib/use-permission';

import { omissionFor, type PhysicianDashboard } from '../api/dashboard';
import { requestSummary } from '../api/summary';

import { AiMarked } from './AiMarked';
import { StatusWord } from './StatusWord';
import { WithheldNote } from './WithheldNote';

/**
 * §8's centre panel: the pre-consultation narrative (CP73, D-15).
 *
 * # This panel renders six states and invents none of them
 *
 * `GET /v1/visits/{id}/synthesis` never answers 404. `NOT_REQUESTED`, `PENDING`, `RUNNING`,
 * `READY`, `FAILED` and `UNCHANGED` are six different 200s with six different sentences, and
 * D-15 — *fail visible, never fail silent, never fail invented* — is why. This component's
 * whole job on the degraded states is to **say what the server said**, in the language the
 * reader chose, and get out of the way of the record in the column beside it.
 *
 * The temptation on a failed run is to draw a spinner or an empty card. Both are the failure
 * D-15 names: a physician who sees a blank centre column cannot tell whether the system tried,
 * whether it is still trying, or whether it has an answer it is deliberately withholding.
 *
 * # The withheld state is a normal state and is not an error
 *
 * CP72 blocks a summary whose claims cannot be traced to the record. That arrives here as
 * `FAILED` with `failure_kind: UNGROUNDED`, and it is drawn as what it is: *the system has an
 * answer and is not showing it, because a sentence in it could not be traced.* Not "the model
 * could not be reached", which would send somebody to check a network that is fine. The
 * grounding verdict and the findings count are on the provenance line, one interaction away
 * like every other attribution on this screen.
 *
 * # The red lines
 *
 * §6.4's red-lined abnormalities carry through as the model's `red_flags`, in two severities.
 * They are drawn **above** the narrative, because a physician with forty seconds reads the top
 * of this column and nothing else — and because a red line buried in the fourth paragraph of a
 * page of prose is a red line that has been drawn and not communicated.
 *
 * # Everything here is inside `AiMarked`
 *
 * Including the failure sentences, and that is deliberate rather than incidental: on a
 * degraded run what remains on screen is a mixture of the system's own message and, sometimes,
 * a previous generation's narrative. That is exactly the state in which an unmarked machine
 * sentence would be most dangerous.
 */

export interface SummaryPanelProps {
  view: PhysicianDashboard;
}

export function SummaryPanel({ view }: SummaryPanelProps) {
  const t = useTranslations('dashboard.summary');
  const locale = useLocale() as Locale;
  const client = useQueryClient();
  const mayRequest = usePermission('summary.request');

  const withheld = omissionFor(view, 'summary');
  const summary = view.summary;

  const request = useMutation({
    mutationFn: () => requestSummary(view.visit?.id ?? ''),
    onSuccess: () => {
      // The whole dashboard, because a queued run changes the centre panel's state and the
      // right panel's generation together. Invalidating one of them would leave the two
      // halves of one fact disagreeing on screen.
      void client.invalidateQueries({ queryKey: ['patient', view.patient.id] });
    },
  });

  if (withheld) {
    return (
      <div className="dash-panel dash-panel--summary" data-testid="summary-panel">
        <h2 className="dash-panel__title">{t('title')}</h2>
        <WithheldNote omission={withheld} />
      </div>
    );
  }

  if (!summary) {
    // No visit at all — somebody registered this morning who has been nowhere. Not a fault,
    // and not a state to draw a spinner for.
    return (
      <div className="dash-panel dash-panel--summary" data-testid="summary-panel">
        <h2 className="dash-panel__title">{t('title')}</h2>
        <p className="dash-card__empty">{t('noVisit')}</p>
      </div>
    );
  }

  const message = locale === 'bn' ? summary.message_bn : summary.message_en;

  return (
    <div className="dash-panel dash-panel--summary" data-testid="summary-panel">
      <h2 className="dash-panel__title" id="dash-summary-heading">
        {t('title')}
      </h2>

      <AiMarked
        label={t('title')}
        degraded={summary.degraded}
        testId="summary-ai-region"
        className="dash-summary"
      >
        {/*
          The state, first and always, in the reader's language and in the server's own words.
          `aria-live="polite"` because this sentence changes under a physician who is reading
          the column — a run that finishes while the page is open moves it from "being
          prepared" to a narrative — and a change nobody is told about is a change that leaves
          a screen-reader user reading a sentence that is no longer on screen.
        */}
        <p
          className="dash-summary__state"
          data-state={summary.state}
          data-degraded={summary.degraded}
          role="status"
          aria-live="polite"
        >
          <StatusWord status={stateStatus(summary.state)} testId="summary-state">
            {t(`state.${summary.state}`)}
          </StatusWord>
          <span>{message}</span>
        </p>

        {summary.red_flags && summary.red_flags.length > 0 && (
          <ul className="dash-flags" data-testid="summary-red-flags">
            {summary.red_flags.map((flag, index) => (
              <li
                key={`${flag.severity}-${index}`}
                className="dash-flag"
                data-severity={flag.severity}
              >
                {/* The word before the tint. A photograph of this screen has no colour worth
                    relying on, and neither does the printed summary. */}
                <span className="dash-flag__severity">{t(`severity.${flag.severity}`)}</span>
                <span className="dash-flag__statement">{flag.statement}</span>
                {flag.basis && flag.basis.length > 0 && (
                  <span className="dash-flag__basis">
                    {t('basis', { refs: flag.basis.join(', ') })}
                  </span>
                )}
              </li>
            ))}
          </ul>
        )}

        {summary.key_points && summary.key_points.length > 0 && (
          <ul className="dash-summary__points" data-testid="summary-key-points">
            {summary.key_points.map((point) => (
              <li key={point}>{point}</li>
            ))}
          </ul>
        )}

        {summary.narrative ? (
          // The narrative is English (docs/synthesis.md, and Dr. Nahid's to confirm). It is
          // marked `lang` so that a Bengali interface does not hand a screen reader English
          // prose in a Bengali voice, which is unintelligible rather than merely wrong.
          <div className="dash-summary__narrative" lang="en" data-testid="summary-narrative">
            {summary.narrative.split(/\n{2,}/).map((paragraph, index) => (
              <p key={index}>{paragraph}</p>
            ))}
          </div>
        ) : (
          // No narrative, and the record beside this is what the physician reads. Said out
          // loud rather than left as white space: a blank column is the thing D-15 forbids.
          <p className="dash-summary__fallback" data-testid="summary-no-narrative">
            {t('readTheRecord')}
          </p>
        )}

        {summary.provenance && (
          // Attribution for a machine-written sentence. Nobody's name, because nobody wrote
          // it: the prompt version, the model, the generation, the grounding verdict. A
          // `<details>` rather than a row of chips because it is the fifth thing a physician
          // wants and the first thing a medico-legal review wants, and those two readers
          // want it at very different sizes.
          <details className="dash-provenance" data-testid="summary-provenance">
            <summary>{t('provenance.open')}</summary>
            <dl className="dash-provenance__list">
              <dt>{t('provenance.generation')}</dt>
              <dd>{summary.provenance.generation}</dd>
              <dt>{t('provenance.trigger')}</dt>
              <dd>{t(`trigger.${summary.provenance.trigger}`)}</dd>
              <dt>{t('provenance.model')}</dt>
              <dd>{summary.provenance.model_version || t('provenance.none')}</dd>
              <dt>{t('provenance.prompt')}</dt>
              <dd>{summary.provenance.prompt_version || t('provenance.none')}</dd>
              <dt>{t('provenance.grounding')}</dt>
              <dd>
                {t(`grounding.${summary.provenance.grounding_state}`)}
                {summary.provenance.grounding_findings > 0 &&
                  ` · ${t('provenance.findings', { count: summary.provenance.grounding_findings })}`}
              </dd>
              {summary.provenance.finished_at && (
                <>
                  <dt>{t('provenance.finished')}</dt>
                  <dd>{formatDateTime(Date.parse(summary.provenance.finished_at), locale)}</dd>
                </>
              )}
              {summary.confidence !== undefined && (
                <>
                  <dt>{t('provenance.confidence')}</dt>
                  {/* The model's opinion of itself. Labelled as that, because a bare
                      percentage beside a clinical narrative reads as a probability about the
                      patient. */}
                  <dd>
                    {t('provenance.confidenceValue', {
                      value: Math.round(summary.confidence * 100),
                    })}
                  </dd>
                </>
              )}
              {summary.citations && summary.citations.length > 0 && (
                <>
                  <dt>{t('provenance.citations')}</dt>
                  <dd className="dash-provenance__citations">{summary.citations.join(', ')}</dd>
                </>
              )}
            </dl>
          </details>
        )}
      </AiMarked>

      {summary.requestable && mayRequest && view.visit?.open && (
        // §7.1 promises the physician never *needs* to press this. It exists for the morning
        // the automatic trigger did not fire, which is exactly the morning somebody needs it.
        <div className="dash-summary__actions">
          <Button
            variant="secondary"
            onClick={() => request.mutate()}
            disabled={request.isPending}
            data-testid="summary-request"
          >
            <Icon name="refresh-cw" aria-hidden />
            {t(summary.state === 'NOT_REQUESTED' ? 'request' : 'rerun')}
          </Button>
          {request.isError && <p className="dash-summary__error">{t('requestFailed')}</p>}
        </div>
      )}
    </div>
  );
}

/**
 * The state as a status token.
 *
 * `UNCHANGED` is `normal` and not `stale`, and that is the one worth arguing: a run that
 * looked and found nothing new is a *completion*, not a staleness. The server's own sentence
 * says the record has not changed since the summary was prepared, which is reassurance rather
 * than a warning, and a token reading "stale" beside it would contradict the words next to it.
 */
function stateStatus(state: string) {
  switch (state) {
    case 'READY':
    case 'UNCHANGED':
      return 'normal' as const;
    case 'PENDING':
    case 'RUNNING':
      return 'stale' as const;
    case 'FAILED':
      return 'high' as const;
    default:
      return 'unknown' as const;
  }
}
