'use client';

import { useQuery } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';

import { bilingual } from '@/lib/bilingual';
import { formatDateTime } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';
import { usePermission } from '@/lib/use-permission';
import { useSessionStore } from '@/stores/session';

import {
  CORRECTION_REASONS_KEY,
  answerRole,
  isOpen,
  listCorrectionReasons,
  reasonByCode,
  type CorrectionRequest,
  type CorrectionStatus,
} from '../api/corrections';

import { AnswerCorrection } from './AnswerCorrection';
import { PersonLine } from './PersonLine';

/**
 * One flag, and what happened to it (CP62, criterion 5).
 *
 * Criterion 5 is that the value history shows the complete chain **with both attributions**.
 * The observation rows carry one of them — who recorded each value — and only the request
 * carries the other: who said it was wrong, why, who was asked, and what they answered. This
 * is that half.
 *
 * # Why the four statuses are four sentences and never a badge alone
 *
 * `OPEN`, `APPLIED`, `REJECTED` and `OVERRIDDEN` mean four different things to the person
 * reading a record, and the difference between the last two is the one this checkpoint most
 * has to protect: **`OVERRIDDEN` means somebody other than the operator corrected the value.**
 * A screen that drew it the same way as `APPLIED` would let a supervisor's fix read as though
 * the operator had put it right themselves — which is a false statement about a colleague's
 * work, and at CP63 it is a false statement in a quality tally. So the override has its own
 * sentence, and the sentence names what actually happened rather than grading anybody.
 *
 * `REJECTED` is not a failure either. "The value stands, and here is why" is an answer, and a
 * screen that drew it as a problem would teach physicians that flagging something and being
 * told it was right is an outcome to avoid.
 *
 * # Why the reason code is shown in words and the note beside it
 *
 * `TRANSCRIPTION` on a physician's screen is a database identifier. The vocabulary carries
 * both languages and this reads the reader's own, falling back to the other and then to the
 * code — never to a blank, because a flag with no visible reason is exactly the thing a
 * required reason field exists to prevent.
 *
 * The note is drawn whenever there is one. It is the half a code cannot carry — "the tape was
 * against the wall, not the patient" — and it is what the person being asked to correct the
 * value actually reads.
 *
 * # Nothing here is a verdict
 *
 * Every sentence says who and what. There is no word for wrong about a person, no count of
 * anybody's mistakes, and no tone that marks an operator. A correction is a fact about a
 * number.
 */
export interface CorrectionTrailProps {
  request: CorrectionRequest;
  /**
   * Whether to offer the answer form here.
   *
   * The queue draws its own, above its own list, so it passes `false` — two answer forms for
   * one request on one screen is two buttons that do the same thing and disagree about which
   * one is busy.
   */
  allowAnswer?: boolean;
  /** What the flagged value is — "Height". Named in the answer form. */
  valueLabel?: string;
  onAnswered?: () => void;
}

/**
 * The four statuses this build has a sentence for.
 *
 * Listed rather than inferred so that a status the server adds tomorrow renders as its own
 * code instead of as a blank heading — and so that adding one server-side fails the type check
 * here rather than surfacing as a correction with no visible outcome.
 *
 * There is deliberately **no severity ordering** on this list and no tone that marks an
 * operator. The stylesheet distinguishes the four by shape and weight rather than by a red
 * mark: a supervisor answering for a colleague who has gone home is an ordinary and correct
 * thing to do, and a rejection means the value stood, which is an answer rather than a problem.
 */
const KNOWN_STATUSES: readonly CorrectionStatus[] = ['OPEN', 'APPLIED', 'REJECTED', 'OVERRIDDEN'];

function statusKnown(status: string): status is CorrectionStatus {
  return (KNOWN_STATUSES as readonly string[]).includes(status);
}

export function CorrectionTrail({
  request,
  allowAnswer = true,
  valueLabel,
  onAnswered,
}: CorrectionTrailProps) {
  const t = useTranslations('corrections');
  const locale = useLocale() as Locale;

  const viewerId = useSessionStore((state) => state.user?.id);
  const mayApprove = usePermission('corrections.approve');
  const role = answerRole(request, { viewerId, mayApprove });

  const reasons = useQuery({
    queryKey: CORRECTION_REASONS_KEY,
    queryFn: listCorrectionReasons,
    staleTime: 60 * 60 * 1000,
  });

  const reason = reasonByCode(reasons.data ?? [], request.reason_code);
  const reasonText =
    reason === undefined
      ? // A code the vocabulary has no row for renders as the code. It is a poor label and a
        // far better one than a blank, and it is a thing somebody can quote when they ask why
        // the vocabulary has changed under a request already in the record.
        request.reason_code
      : (bilingual(reason.display_en, reason.display_bn, locale)?.text ?? request.reason_code);

  const requestedAt = Date.parse(request.requested_at);
  const resolvedAt = request.resolved_at === undefined ? NaN : Date.parse(request.resolved_at);

  return (
    <article
      className="app-corrections__trail"
      data-testid={`correction-trail-${request.id}`}
      data-status={request.status}
    >
      <header className="app-corrections__trail-head">
        <span className="app-corrections__trail-status" data-testid="correction-status">
          {statusKnown(request.status) ? t(`status.${request.status}`) : request.status}
        </span>
      </header>

      <p className="app-corrections__trail-line" data-testid="correction-requested">
        {t('trail.flaggedAt', {
          when: Number.isFinite(requestedAt)
            ? formatDateTime(requestedAt, locale)
            : t('trail.timeUnreadable'),
        })}
      </p>

      <p className="app-corrections__trail-line">
        <span className="app-corrections__trail-label">{t('trail.flaggedBy')}</span>{' '}
        <PersonLine
          testId="correction-requester"
          nameEn={request.requested_by_name_en}
          nameBn={request.requested_by_name_bn}
          code={request.requested_by_code}
          role={request.requested_role}
          id={request.requested_by}
        />
      </p>

      <p className="app-corrections__trail-line" data-testid="correction-reason">
        <span className="app-corrections__trail-label">{t('trail.reason')}</span> {reasonText}
      </p>

      {(request.note ?? '').trim() !== '' && (
        <p className="app-corrections__trail-note" data-testid="correction-note">
          {request.note}
        </p>
      )}

      <p className="app-corrections__trail-line">
        <span className="app-corrections__trail-label">{t('trail.routedTo')}</span>{' '}
        <PersonLine
          testId="correction-assignee"
          nameEn={request.assigned_to_name_en}
          nameBn={request.assigned_to_name_bn}
          code={request.assigned_to_code}
          id={request.assigned_to}
        />
      </p>

      {isOpen(request) ? (
        <p className="app-corrections__trail-line" data-testid="correction-waiting">
          {t('trail.waiting')}
        </p>
      ) : (
        <>
          <p className="app-corrections__trail-line" data-testid="correction-answered">
            {t('trail.answeredAt', {
              when: Number.isFinite(resolvedAt)
                ? formatDateTime(resolvedAt, locale)
                : t('trail.timeUnknown'),
            })}
          </p>

          <p className="app-corrections__trail-line">
            <span className="app-corrections__trail-label">{t('trail.answeredBy')}</span>{' '}
            <PersonLine
              testId="correction-resolver"
              role={request.resolved_role}
              id={request.resolved_by}
            />
          </p>

          {/* The one sentence this whole status exists for. It is drawn from `OVERRIDDEN`
              itself rather than from comparing two ids, so it cannot quietly stop being true
              when a read stops joining a name. */}
          {request.status === 'OVERRIDDEN' && (
            <p className="app-corrections__trail-override" data-testid="correction-overridden">
              {t('trail.overridden')}
            </p>
          )}

          {(request.resolution_note ?? '').trim() !== '' && (
            <p className="app-corrections__trail-note" data-testid="correction-resolution-note">
              <span className="app-corrections__trail-label">
                {request.status === 'REJECTED' ? t('trail.standsBecause') : t('trail.answerNote')}
              </span>{' '}
              {request.resolution_note}
            </p>
          )}
        </>
      )}

      {allowAnswer && isOpen(request) && role !== 'nobody' && (
        <AnswerCorrection
          request={request}
          role={role}
          valueLabel={valueLabel}
          onAnswered={onAnswered}
        />
      )}
    </article>
  );
}
