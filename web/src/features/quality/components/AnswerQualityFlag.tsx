'use client';

import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useState } from 'react';

import { ApiError, fieldMessages } from '@dthcms/api-client';
import { AlertBanner, Button } from '@dthcms/ui';

import type { Locale } from '@/lib/i18n/config';

import {
  QUALITY_KEY,
  alreadyAnswered,
  answerFlag,
  flagAnswerReady,
  type FlagAnswer,
  type QualityFlag,
} from '../api/quality';

/**
 * Answering a raised pattern: acknowledge it, or say it is not one (CP63, §4.3).
 *
 * # Two answers, both of which are answers
 *
 * Acknowledging is *"I have seen this and I am doing something about it"*. Dismissing is
 * *"this is not a problem"* — and on a mechanism whose thresholds no clinician has approved
 * yet, dismissing is frequently the correct answer. Neither is pre-selected here. A form
 * whose easier path is "acknowledge" would produce acknowledgements from supervisors who did
 * not agree with the flag; a form whose easier path is "dismiss" would empty the list without
 * anybody reading it.
 *
 * # Why a dismissal must say why, and why the form asks before the server does
 *
 * The database refuses a dismissal with an empty reason, the endpoint answers `422`, and this
 * form will not send one — the confirm button stays disabled until there is something in the
 * box. That is the same rule a rejected correction follows (CP62), for the same reason: "no"
 * with no reason teaches nobody anything.
 *
 * It matters more here than there. A dismissal is the *only* thing that takes a flag off a
 * colleague's record, and a record of unexplained dismissals is a record in which somebody
 * can be asked, next year, why one operator's flags kept disappearing. The reason is what
 * makes that question answerable.
 *
 * Refusing before the request rather than after it is not only politeness. The round trip is
 * over a clinic's connection, on a cheap tablet, usually with somebody waiting; a supervisor
 * who is told to fill in a box they have already looked at, after a wait, will fill it in with
 * a full stop.
 *
 * # Nothing here removes anything
 *
 * Both answers are recorded in place, with the name of whoever gave them, on the security
 * audit trail. There is no delete on this surface and no endpoint behind one: a flag that
 * could be made to disappear is a flag a supervisor can be persuaded to make disappear.
 */
export interface AnswerQualityFlagProps {
  flag: QualityFlag;
  onAnswered?: () => void;
}

type Mode = 'idle' | 'acknowledge' | 'dismiss';

export function AnswerQualityFlag({ flag, onAnswered }: AnswerQualityFlagProps) {
  const t = useTranslations('quality');
  const locale = useLocale() as Locale;
  const client = useQueryClient();

  const [mode, setMode] = useState<Mode>('idle');
  const [note, setNote] = useState('');
  const [reason, setReason] = useState('');
  const [fields, setFields] = useState<Record<string, string>>({});
  const [refusal, setRefusal] = useState<string | null>(null);
  const [taken, setTaken] = useState(false);

  const answer: FlagAnswer =
    mode === 'dismiss' ? { kind: 'dismissed', reason } : { kind: 'acknowledged', note };

  const send = useMutation({
    mutationFn: () => answerFlag(flag.id, answer),
    onSuccess: () => {
      /*
       * The whole feature's prefix, and not only the flag list. Answering moves the
       * supervisor's queue, the operator's own record — where the flag was drawn as open —
       * and that operator's detail if anybody has it open. Three reads in three files, which
       * is exactly the situation where two spellings of one key leave a flag on somebody's
       * screen that has already been answered.
       */
      void client.invalidateQueries({ queryKey: QUALITY_KEY });
      setMode('idle');
      onAnswered?.();
    },
    onError: (error: unknown) => {
      setFields({});
      if (alreadyAnswered(error)) {
        // A colleague got there first. Not a failure to retry: the useful next act is reading
        // what they decided, which arrives with the refreshed list.
        setTaken(true);
        setRefusal(null);
        void client.invalidateQueries({ queryKey: QUALITY_KEY });
        return;
      }
      if (error instanceof ApiError) {
        const named = fieldMessages(error, locale);
        if (Object.keys(named).length > 0) {
          setFields(named);
          setRefusal(null);
          return;
        }
      }
      setRefusal(t('answer.failed'));
    },
  });

  const ready = flagAnswerReady(answer) && !send.isPending;

  return (
    <div className="app-quality__answer" data-testid={`answer-flag-${flag.id}`}>
      {taken && (
        <AlertBanner tone="borderline" title={t('answer.alreadyAnswered')}>
          {t('answer.alreadyAnsweredBody')}
        </AlertBanner>
      )}

      {refusal !== null && (
        <AlertBanner tone="critical" title={refusal} onDismiss={() => setRefusal(null)}>
          {t('answer.nothingChanged')}
        </AlertBanner>
      )}

      {mode === 'idle' && (
        <div className="app-quality__actions">
          <Button
            variant="primary"
            data-testid={`acknowledge-${flag.id}`}
            onClick={() => {
              setMode('acknowledge');
              setRefusal(null);
            }}
          >
            {t('answer.acknowledgeAction')}
          </Button>
          <Button
            variant="secondary"
            data-testid={`dismiss-${flag.id}`}
            onClick={() => {
              setMode('dismiss');
              setRefusal(null);
            }}
          >
            {t('answer.dismissAction')}
          </Button>
        </div>
      )}

      {mode === 'acknowledge' && (
        <div data-testid={`acknowledge-form-${flag.id}`}>
          <p className="app-page__description">{t('answer.acknowledgeBody')}</p>

          <div className="app-quality__field">
            <label htmlFor={`quality-note-${flag.id}`}>{t('answer.noteLabel')}</label>
            <textarea
              id={`quality-note-${flag.id}`}
              data-testid={`acknowledge-note-${flag.id}`}
              className="app-quality__note"
              rows={3}
              maxLength={2000}
              value={note}
              disabled={send.isPending}
              aria-describedby={`quality-note-hint-${flag.id}`}
              onChange={(event) => setNote(event.target.value)}
            />
            <p id={`quality-note-hint-${flag.id}`} className="app-quality__hint">
              {t('answer.noteHint')}
            </p>
            {fields.resolution !== undefined && (
              <p className="app-quality__field-error">{fields.resolution}</p>
            )}
          </div>

          <div className="app-quality__actions">
            <Button variant="quiet" disabled={send.isPending} onClick={() => setMode('idle')}>
              {t('cancel')}
            </Button>
            <Button
              variant="primary"
              loading={send.isPending}
              disabled={!ready}
              data-testid={`acknowledge-confirm-${flag.id}`}
              onClick={() => send.mutate()}
            >
              {t('answer.acknowledgeConfirm')}
            </Button>
          </div>
        </div>
      )}

      {mode === 'dismiss' && (
        <div data-testid={`dismiss-form-${flag.id}`}>
          <p className="app-page__description">{t('answer.dismissBody')}</p>

          <div className="app-quality__field">
            <label htmlFor={`quality-reason-${flag.id}`}>{t('answer.reasonLabel')}</label>
            <textarea
              id={`quality-reason-${flag.id}`}
              data-testid={`dismiss-reason-${flag.id}`}
              className="app-quality__note"
              rows={3}
              maxLength={2000}
              value={reason}
              disabled={send.isPending}
              placeholder={t('answer.reasonPlaceholder')}
              aria-describedby={`quality-reason-hint-${flag.id}`}
              onChange={(event) => setReason(event.target.value)}
            />
            <p id={`quality-reason-hint-${flag.id}`} className="app-quality__hint">
              {t('answer.reasonHint')}
            </p>
            {fields.resolution !== undefined && (
              <p className="app-quality__field-error">{fields.resolution}</p>
            )}
          </div>

          <div className="app-quality__actions">
            <Button variant="quiet" disabled={send.isPending} onClick={() => setMode('idle')}>
              {t('cancel')}
            </Button>
            <Button
              variant="primary"
              loading={send.isPending}
              disabled={!ready}
              data-testid={`dismiss-confirm-${flag.id}`}
              onClick={() => send.mutate()}
            >
              {t('answer.dismissConfirm')}
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
