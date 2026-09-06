'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useState } from 'react';

import { ApiError, fieldMessages } from '@dthcms/api-client';
import { AlertBanner, Button, Card, Select } from '@dthcms/ui';

import { bilingual } from '@/lib/bilingual';
import type { Locale } from '@/lib/i18n/config';

import {
  CORRECTION_REASONS_KEY,
  alreadyFlagged,
  flagObservation,
  flagReady,
  listCorrectionReasons,
  patientCorrectionsKey,
  reasonByCode,
  reasonsInOrder,
} from '../api/corrections';

/**
 * Saying that a value looks wrong (CP62, §4.3, criterion 2).
 *
 * # Why it takes two deliberate acts
 *
 * This control accuses a colleague of a mistake. Not in its wording — nothing here uses the
 * word wrong about a person — but in its effect: it puts a request on somebody's queue with
 * their name on it, and at CP63 the reason code is counted. A single press would put that one
 * mis-tap away from a physician scrolling a chain of values on a tablet.
 *
 * So the control is a quiet button that opens a form, and the form does not send until a
 * reason has been chosen. The same shape as CP57's override gate, for the same reason: an act
 * with a person's name on it is worth one more press than an act without.
 *
 * # Why the reason is a code *and* free text
 *
 * A code alone cannot say "the tape was against the wall, not the patient". Free text alone
 * cannot be counted, and counting is the whole of CP63's repeated-transcription detection. The
 * server sends the vocabulary rather than the interface inventing one, because the taxonomy is
 * a proposal the plan lists as needing clinical confirmation and changing it should be a
 * decision rather than a release.
 *
 * The note is not required, which matches the server. A physician who has chosen
 * `TRANSCRIPTION` has already said something countable, and demanding prose before they may
 * say it would slow down the one act this entire workflow depends on people doing. The screen
 * asks for it in words instead, beside the box, saying who reads it.
 *
 * # Why the screen says which reasons are counted
 *
 * Choosing a reason that CP63 counts has a consequence for a colleague, and a vocabulary whose
 * consequences are invisible is one people learn to answer strategically. So the form says so,
 * plainly, at the moment the choice is made — and says it as a fact about how the clinic finds
 * repeated problems, never as a threat about somebody's record.
 *
 * # What this control never does
 *
 * It does not change the value, hide it, or mark it as doubtful anywhere. The number a
 * physician disagrees with stays exactly as it is, on screen and in the record, until the
 * person who typed it answers — and it stays afterwards too. A flag is a question, and a
 * screen that dimmed the value the moment somebody asked one would have answered it.
 */
export interface FlagValueProps {
  patientId: string;
  observationId: string;
  /** What this value is — "Height". Named in the form, so the physician sees what they flagged. */
  valueLabel: string;
  onFlagged?: () => void;
}

export function FlagValue({ patientId, observationId, valueLabel, onFlagged }: FlagValueProps) {
  const t = useTranslations('corrections');
  const locale = useLocale() as Locale;
  const client = useQueryClient();

  const [open, setOpen] = useState(false);
  const [reasonCode, setReasonCode] = useState('');
  const [note, setNote] = useState('');
  const [fields, setFields] = useState<Record<string, string>>({});
  const [refusal, setRefusal] = useState<string | null>(null);
  const [conflict, setConflict] = useState(false);

  const reasons = useQuery({
    queryKey: CORRECTION_REASONS_KEY,
    queryFn: listCorrectionReasons,
    // Reference data with no patient in it. It changes when somebody decides the taxonomy is
    // wrong, which is a decision, not an event — so it is not re-read every thirty seconds on
    // a shared clinic connection.
    staleTime: 60 * 60 * 1000,
  });

  const raise = useMutation({
    mutationFn: () => flagObservation(observationId, { reasonCode, note }),
    onSuccess: () => {
      setOpen(false);
      setReasonCode('');
      setNote('');
      setFields({});
      setRefusal(null);
      setConflict(false);
      // Only the corrections list. The value itself has not changed — a flag is a question
      // about a number, not a change to it — and re-reading the observation history here would
      // suggest to the next reader that something about the value had moved.
      void client.invalidateQueries({ queryKey: patientCorrectionsKey(patientId) });
      onFlagged?.();
    },
    onError: (error: unknown) => {
      setFields({});
      setConflict(false);
      if (alreadyFlagged(error)) {
        // Somebody flagged it between this screen loading and the button being pressed. Not a
        // failure to retry: a second flag is the same conversation, and the useful next act is
        // reading whose request is already open and what they said.
        //
        // Told by `CORRECTION_ALREADY_OPEN` rather than by the 409, because this endpoint
        // answers 409 for two different facts — already flagged, and already replaced — and a
        // screen that read the number would say the wrong one.
        setConflict(true);
        setRefusal(null);
        return;
      }
      if (error instanceof ApiError) {
        const named = fieldMessages(error, locale);
        // The server names `reason_code` for a code that is not in the vocabulary and
        // `observation_id` for your own value. Its own sentences are more accurate than a
        // generic one, so they go beside the field they belong to.
        if (named.reason_code !== undefined || named.observation_id !== undefined) {
          setFields(named);
          setRefusal(null);
          return;
        }
      }
      setRefusal(t('flag.failed'));
    },
  });

  if (!open) {
    return (
      <Button
        variant="quiet"
        size="sm"
        data-testid="flag-open"
        onClick={() => {
          setOpen(true);
          setConflict(false);
          setRefusal(null);
        }}
      >
        {t('flag.action')}
      </Button>
    );
  }

  const vocabulary = reasonsInOrder(reasons.data ?? []);
  const chosen = reasonByCode(vocabulary, reasonCode);
  const ready = flagReady(reasonCode) && !raise.isPending && vocabulary.length > 0;

  return (
    <Card elevation="raised" className="app-corrections__flag-form" compact>
      <div data-testid="flag-form">
        <h4 className="app-corrections__heading">{t('flag.title', { what: valueLabel })}</h4>
        <p className="app-page__description">{t('flag.body')}</p>

        {conflict && (
          <AlertBanner tone="borderline" title={t('flag.alreadyOpen')}>
            {t('flag.alreadyOpenBody')}
          </AlertBanner>
        )}

        {refusal !== null && (
          <AlertBanner tone="critical" title={refusal} onDismiss={() => setRefusal(null)}>
            {t('flag.nothingChanged')}
          </AlertBanner>
        )}

        {fields.observation_id !== undefined && (
          <AlertBanner tone="borderline" title={fields.observation_id} />
        )}

        {reasons.isError && (
          // Without the vocabulary there is nothing honest to send: a free-text-only flag
          // cannot be counted, and inventing a code here would put a word in the record that
          // the clinic never agreed on.
          <AlertBanner tone="critical" title={t('flag.reasonsUnavailable')}>
            {t('flag.reasonsUnavailableBody')}
          </AlertBanner>
        )}

        <Select
          label={t('flag.reasonLabel')}
          description={t('flag.reasonHint')}
          placeholder={t('flag.reasonPlaceholder')}
          required
          value={reasonCode}
          error={fields.reason_code}
          disabled={raise.isPending || reasons.isPending}
          data-testid="flag-reason"
          options={vocabulary.map((reason) => ({
            value: reason.code,
            // The reader's language where the vocabulary has it, the other where it does not.
            // A blank option is an option nobody can choose on purpose.
            label: bilingual(reason.display_en, reason.display_bn, locale)?.text ?? reason.code,
          }))}
          onChange={(event) => setReasonCode(event.target.value)}
        />

        {chosen?.transcription === true && (
          <p className="app-corrections__counted" data-testid="flag-counted">
            {t('flag.counted')}
          </p>
        )}

        <div className="app-corrections__field">
          <label htmlFor={`flag-note-${observationId}`}>{t('flag.noteLabel')}</label>
          <textarea
            id={`flag-note-${observationId}`}
            data-testid="flag-note"
            className="app-corrections__note"
            rows={3}
            value={note}
            maxLength={500}
            disabled={raise.isPending}
            placeholder={t('flag.notePlaceholder')}
            aria-describedby={`flag-note-hint-${observationId}`}
            onChange={(event) => setNote(event.target.value)}
          />
          <p id={`flag-note-hint-${observationId}`} className="app-corrections__hint">
            {t('flag.noteHint')}
          </p>
        </div>

        <div className="app-corrections__actions">
          <Button variant="quiet" disabled={raise.isPending} onClick={() => setOpen(false)}>
            {t('cancel')}
          </Button>
          <Button
            variant="primary"
            loading={raise.isPending}
            disabled={!ready}
            data-testid="flag-confirm"
            onClick={() => raise.mutate()}
          >
            {t('flag.confirm')}
          </Button>
        </div>
      </div>
    </Card>
  );
}
