'use client';

import { useQuery } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';

import { AlertBanner, EmptyState, Skeleton } from '@dthcms/ui';

import {
  ObservationValue,
  chainsOf,
  listObservationHistory,
  observationHistoryKey,
} from '@/features/observations';
import { formatDateTime } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';
import { usePermission } from '@/lib/use-permission';
import { useSessionStore } from '@/stores/session';

import {
  listCorrectionsForPatient,
  openRequestOn,
  patientCorrectionsKey,
  requestsOn,
  whyNotFlaggable,
  type CorrectionRequest,
} from '../api/corrections';

import { CorrectionTrail } from './CorrectionTrail';
import { FlagValue } from './FlagValue';

/**
 * Every value ever recorded for one code, with both attributions on it (CP62, criterion 5).
 *
 * This is the screen §4.3's 140/150 case ends on. A physician looks at a height and asks what
 * it said before; the answer is 150, entered by the operator who typed it, flagged by the
 * physician who disagreed, corrected by the operator themselves, and both numbers are still
 * here with both names against them.
 *
 * # Why the replaced values are rows and not a footnote
 *
 * Criterion 1 is that the original value is never altered and **remains visible**. A screen
 * showing today's height with a small note saying it had been corrected would satisfy the
 * first half and quietly break the second: the number a physician disagreed with is the number
 * a reviewer needs to see, and "there was an earlier value" is not it. So every row in the
 * chain is drawn the same way, through the same attributed component, at the same size.
 *
 * # Why the chain is a chain rather than a list
 *
 * One code accumulates several independent measurements — a height at three visits — and each
 * of those may have been corrected. Flattening them into one date-ordered list would put last
 * March's correction between today's height and yesterday's, and a reader asking "what did
 * *this* value say before" would have to reconstruct the links by eye. `chainsOf` follows
 * `replaced_by`, which is the link the server already records.
 *
 * Within a chain, oldest first: it is the order the events happened in and the order the
 * sentence reads in. Chains themselves are newest first, because a physician opens this screen
 * about today.
 *
 * # Why the flag control is on every row a physician may flag, and nowhere else
 *
 * A control that exists in order to be refused teaches people that the software is unreliable.
 * So it is not drawn for somebody without `observation.correct.request`, on a value that is
 * already replaced, or on one somebody has already flagged — the last two are already visible
 * on the row itself, as the status word and as the open request drawn directly above it.
 *
 * A physician's **own** value is the one case that gets a sentence rather than silence, because
 * the absence would otherwise read as a fault in the screen: the server refuses it with "correct
 * it rather than flagging it", and that is worth saying where the control would have been.
 *
 * # Nothing on this screen changes a value
 *
 * The only writes reachable from here are raising a flag and — for the person a request was
 * routed to, or a supervisor — answering one. There is no edit control on any row and there
 * must never be: correcting is writing a new value that replaces the old one, and an edit
 * would destroy the chain this screen exists to show.
 */
/** The three states an observation row can be in that this build has a sentence for. */
const KNOWN_STATES = ['ACTIVE', 'CORRECTED', 'SUPERSEDED'] as const;

function stateKnown(status: string): status is (typeof KNOWN_STATES)[number] {
  return (KNOWN_STATES as readonly string[]).includes(status);
}

export interface ValueChainProps {
  patientId: string;
  code: string;
  /** What this code is, in words — "Height". Falls back to the code at the call site. */
  codeLabel: string;
}

export function ValueChain({ patientId, code, codeLabel }: ValueChainProps) {
  const t = useTranslations('corrections');
  const locale = useLocale() as Locale;

  const viewerId = useSessionStore((state) => state.user?.id);
  const mayRequest = usePermission('corrections.request');

  const history = useQuery({
    queryKey: observationHistoryKey(patientId, code),
    queryFn: () => listObservationHistory(patientId, code),
  });

  /*
   * The corrections are read per patient rather than per code, which is what the contract
   * offers — and it is also the cheaper read: a chain view that switched codes would otherwise
   * issue a second request for a list it already had.
   */
  const corrections = useQuery({
    queryKey: patientCorrectionsKey(patientId),
    queryFn: () => listCorrectionsForPatient(patientId),
  });

  if (history.isPending) return <Skeleton height="12rem" />;

  if (history.isError || history.data === undefined) {
    return (
      <AlertBanner tone="critical" title={t('chain.unavailable')}>
        {t('chain.unavailableBody')}
      </AlertBanner>
    );
  }

  const chains = chainsOf(history.data);

  if (chains.length === 0) {
    return (
      <EmptyState title={t('chain.none', { what: codeLabel })}>{t('chain.noneBody')}</EmptyState>
    );
  }

  return (
    <section
      className="app-corrections__chains"
      aria-label={t('chain.title', { what: codeLabel })}
      data-testid="value-chain"
    >
      {corrections.isError && (
        // The values are the record; the flags are a conversation about them. A failure to read
        // the second must not blank the first, and it must not be silent either — a chain with
        // no trail on it looks exactly like a chain nobody ever questioned.
        <AlertBanner tone="borderline" title={t('chain.correctionsUnavailable')}>
          {t('chain.correctionsUnavailableBody')}
        </AlertBanner>
      )}

      <ol className="app-corrections__chain-list">
        {chains.map((chain) => {
          const head = chain[0];
          if (head === undefined) return null;
          const measured = Date.parse(head.effective_at);

          return (
            <li
              key={head.id}
              className="app-corrections__chain"
              data-testid={`chain-${head.id}`}
              data-versions={chain.length}
            >
              <h3 className="app-corrections__chain-heading">
                {t('chain.measured', {
                  when: Number.isFinite(measured)
                    ? formatDateTime(measured, locale)
                    : t('trail.timeUnreadable'),
                })}
              </h3>

              {chain.length > 1 && (
                // Said once per chain rather than implied by the rows. A reader who has not met
                // this screen before needs to be told that both numbers are the record, not
                // that one of them is a draft.
                <p className="app-corrections__chain-note" data-testid="chain-has-versions">
                  {t('chain.bothKept', { count: chain.length })}
                </p>
              )}

              <ol className="app-corrections__versions">
                {chain.map((observation, index) => {
                  const raised = requestsOn(corrections.data ?? [], observation.id);
                  const open = openRequestOn(corrections.data ?? [], observation.id);
                  const blocker = whyNotFlaggable(observation, {
                    viewerId,
                    mayRequest,
                    openRequest: open,
                  });

                  return (
                    <li
                      key={observation.id}
                      className="app-corrections__version"
                      data-testid={`chain-row-${observation.id}`}
                      data-status={observation.status}
                      data-position={index === 0 ? 'original' : 'later'}
                    >
                      <div className="app-corrections__version-value">
                        <ObservationValue
                          observation={observation}
                          label={codeLabel}
                          testId={`chain-value-${observation.id}`}
                        />
                        <span
                          className="app-corrections__version-state"
                          data-testid={`chain-state-${observation.id}`}
                        >
                          {/* A status this build has no sentence for renders as the server's
                              own word. A blank there would draw a replaced value exactly like
                              the current one, which is the confusion the whole chain exists to
                              prevent. */}
                          {stateKnown(observation.status)
                            ? t(`state.${observation.status}`)
                            : observation.status}
                        </span>
                      </div>

                      {(observation.note ?? '').trim() !== '' && (
                        // What the operator typed with the value — the cuff size, which arm,
                        // "patient could not stand". Often the explanation of the very thing
                        // somebody is about to flag.
                        <p className="app-corrections__version-note">{observation.note}</p>
                      )}

                      {raised.map((request: CorrectionRequest) => (
                        <CorrectionTrail
                          key={request.id}
                          request={request}
                          valueLabel={codeLabel}
                        />
                      ))}

                      {/* Not while the corrections are still arriving. Until they do, this
                          screen cannot tell an unflagged value from one somebody flagged a
                          minute ago, and a control offered on that ignorance is a control the
                          server answers with a 409 the moment it is pressed. */}
                      {blocker === null && !corrections.isPending && (
                        <FlagValue
                          patientId={patientId}
                          observationId={observation.id}
                          valueLabel={codeLabel}
                        />
                      )}

                      {blocker === 'your-own' && (
                        <p
                          className="app-corrections__hint"
                          data-testid={`chain-own-${observation.id}`}
                        >
                          {t('flag.yourOwn')}
                        </p>
                      )}
                    </li>
                  );
                })}
              </ol>
            </li>
          );
        })}
      </ol>
    </section>
  );
}
