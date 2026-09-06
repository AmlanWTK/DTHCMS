'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useState } from 'react';

import { ApiError, fieldMessages } from '@dthcms/api-client';
import { AlertBanner, Button, Card, NumericInput, Select, Skeleton } from '@dthcms/ui';

import {
  OBSERVATION_CODES_KEY,
  ObservationValue,
  codeEntry,
  getObservation,
  listObservationCodes,
  observationHistoryKey,
  observationKey,
  patientObservationsKey,
  unitsFor,
} from '@/features/observations';
import { bilingual } from '@/lib/bilingual';
import type { Locale } from '@/lib/i18n/config';

import {
  alreadyAnswered,
  answerDefaults,
  applyCorrection,
  correctingFrom,
  correctionKey,
  correctionReady,
  myCorrectionsKey,
  notYoursToAnswer,
  patientCorrectionsKey,
  rejectCorrection,
  rejectReasonAcceptable,
  type AnswerDraft,
  type AnswerRole,
  type CorrectionRequest,
} from '../api/corrections';

/**
 * Answering a correction request: correct the value, or say it stands (CP62, §4.3).
 *
 * # Two answers, both of which are answers
 *
 * A request has two honest outcomes and this form gives them equal weight. Correcting writes a
 * new value that replaces the flagged one and recomputes everything derived from it. Saying
 * the value stands is not a refusal to co-operate — the operator was there with the tape and
 * the physician was not — and it **requires a reason**, because "no" with no reason is how a
 * flagging culture dies: the person who flagged it learns nothing, cannot tell a disagreement
 * from an oversight, and stops flagging.
 *
 * Neither is the default. There is no pre-selected button here, because a form whose easier
 * path is "correct it" would produce corrections from operators who did not agree, and a form
 * whose easier path is "it stands" would produce refusals nobody read.
 *
 * # Why the original value is on screen while it is being corrected
 *
 * The person answering typed this number some hours ago and is being asked about it by
 * somebody who was not there. Correcting from memory is how 150 becomes 130. So the flagged
 * value is drawn above the box — through the same attributed component the rest of the
 * application uses — and it stays drawn afterwards, because criterion 1 is that the original
 * is never altered and remains visible.
 *
 * # Why the box is pre-filled with the value as it was typed, in the unit it was typed in
 *
 * 154 lb is stored as 69.85 kg and read back as 154 lb. Pre-filling kilograms would ask an
 * operator to check a number they never wrote. The unit is a list rather than a text box —
 * "entered in the wrong unit" is one of the server's own reason codes, so changing it has to be
 * possible — and the list is the code registry's, so it cannot offer a unit the code does not
 * take.
 *
 * # Why there is no plausibility range on the input
 *
 * The registry's band is in the **canonical** unit and the box may be in another one, so a
 * range passed straight through would refuse a real reading typed in pounds. A range that
 * rejects a true value is worse than one that accepts a wrong one: the operator's only way
 * forward is to record something false. The server checks the band, in the right unit, and its
 * refusal arrives beside the field.
 *
 * # `OVERRIDDEN` is not `APPLIED`, and the person pressing the button is told so first
 *
 * Somebody who is not the value's author may answer only if they hold
 * `observation.correct.approve`, and the server records that as `OVERRIDDEN`. This form says
 * that **before** the press, in a banner, naming who the request was routed to — because a
 * supervisor who did not know their fix would be recorded separately from the operator's own
 * is a supervisor taking a decision about a colleague's record without being told they were
 * taking it. Nothing in the request body says which act it is; the server reads the caller's
 * own permissions, so a client cannot nominate itself as the author.
 */
export interface AnswerCorrectionProps {
  request: CorrectionRequest;
  /** Which act this would be. `nobody` never reaches here — the caller does not render it. */
  role: Exclude<AnswerRole, 'nobody'>;
  /** What the value is — "Height". Names the attribution control on the flagged value. */
  valueLabel?: string;
  onAnswered?: () => void;
}

type Mode = 'idle' | 'correct' | 'stands';

export function AnswerCorrection({ request, role, valueLabel, onAnswered }: AnswerCorrectionProps) {
  const t = useTranslations('corrections');
  const locale = useLocale() as Locale;
  const client = useQueryClient();

  const [mode, setMode] = useState<Mode>('idle');
  const [value, setValue] = useState<string | null>(null);
  const [unit, setUnit] = useState<string | null>(null);
  const [text, setText] = useState<string | null>(null);
  const [note, setNote] = useState('');
  const [reason, setReason] = useState('');
  const [fields, setFields] = useState<Record<string, string>>({});
  const [refusal, setRefusal] = useState<string | null>(null);
  const [conflict, setConflict] = useState<'answered' | 'not-yours' | null>(null);

  const observation = useQuery({
    queryKey: observationKey(request.observation_id),
    queryFn: () => getObservation(request.observation_id),
  });

  const codes = useQuery({
    queryKey: OBSERVATION_CODES_KEY,
    queryFn: listObservationCodes,
    staleTime: 60 * 60 * 1000,
  });

  /*
   * Everything the answer moves, invalidated together.
   *
   * A correction writes a new observation, retires the flagged one and recomputes every live
   * derived value in the same transaction. Refreshing the request without refreshing the
   * observation history would leave a chain view showing 150 under a request that says it is
   * now 140 — two numbers on one screen that disagree, both looking equally official, which is
   * the exact failure the server takes a transaction to avoid.
   *
   * **Every** code's history for this patient, not just the flagged one. The client does not
   * know which derivations read this code — that table is the server's, and a copy here would be
   * a second opinion that goes stale the day a formula changes — so the honest key is the
   * patient's whole history subtree. It deliberately stops short of `['observations']`, which
   * would also throw away the code registry: reference data with no patient in it, fetched once
   * an hour, and re-reading it on every correction is a round trip on a shared clinic connection
   * to be told the same hundred and forty codes again.
   */
  function settled() {
    void client.invalidateQueries({ queryKey: correctionKey(request.id) });
    void client.invalidateQueries({ queryKey: myCorrectionsKey(false) });
    void client.invalidateQueries({ queryKey: myCorrectionsKey(true) });
    void client.invalidateQueries({ queryKey: patientCorrectionsKey(request.patient_id) });
    void client.invalidateQueries({ queryKey: patientObservationsKey(request.patient_id) });
    void client.invalidateQueries({ queryKey: observationHistoryKey(request.patient_id) });
    // The flagged row itself: its status has just moved from ACTIVE to CORRECTED, and this
    // form is drawing it.
    void client.invalidateQueries({ queryKey: observationKey(request.observation_id) });
    setMode('idle');
    onAnswered?.();
  }

  function failed(error: unknown) {
    setFields({});
    setConflict(null);
    if (alreadyAnswered(error)) {
      // Somebody answered between this screen loading and the button being pressed. Not a
      // failure to retry: the useful next act is reading what they decided.
      setConflict('answered');
      setRefusal(null);
      return;
    }
    if (notYoursToAnswer(error)) {
      // The server names whose it is. An operator staring at a refused button needs to know
      // who to go and find, rather than what to retype.
      setConflict('not-yours');
      setRefusal(null);
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
  }

  const draft: AnswerDraft = { value, unit, text, note };

  const correct = useMutation({
    /*
     * The body is built by the same function that filled the boxes. The first version of this
     * read the raw state instead, and sent `0` for a number the operator had not retyped and
     * dropped the unit entirely — a divergence no test of the form's *rendering* could catch,
     * because the boxes looked right.
     */
    mutationFn: () => {
      const original = observation.data;
      if (original === undefined) throw new Error('no observation to correct');
      return applyCorrection(request.id, correctingFrom(original, draft));
    },
    onSuccess: settled,
    onError: failed,
  });

  const stands = useMutation({
    mutationFn: () => rejectCorrection(request.id, reason),
    onSuccess: settled,
    onError: failed,
  });

  if (observation.isPending || codes.isPending) return <Skeleton height="8rem" />;

  if (observation.isError || observation.data === undefined) {
    // Answering a value nobody can read is answering blind, and this form's whole reason for
    // showing the original is that correcting from memory is how 150 becomes 130.
    return (
      <AlertBanner tone="critical" title={t('answer.valueUnavailable')}>
        {t('answer.valueUnavailableBody')}
      </AlertBanner>
    );
  }

  const original = observation.data;
  const entry = codeEntry(codes.data ?? [], original.code);
  const units = unitsFor(entry);
  const shape = original.value_type;

  const started = answerDefaults(original);
  const startedValue = value ?? started.value;
  const startedUnit = unit ?? started.unit;
  const startedText = text ?? started.text;

  const correctable = shape !== 'structured';
  const ready = !correct.isPending && correctionReady(original, draft);

  return (
    <Card elevation="raised" className="app-corrections__answer" compact>
      <div data-testid={`answer-${request.id}`} data-role={role}>
        <h4 className="app-corrections__heading">{t('answer.title')}</h4>

        {role === 'supervisor' && (
          // Said before the press, not reported after it. See the note at the top of the file.
          <AlertBanner tone="borderline" title={t('answer.supervisorTitle')}>
            {t('answer.supervisorBody')}
          </AlertBanner>
        )}

        <div className="app-corrections__answer-value">
          <span className="app-corrections__trail-label">{t('answer.flaggedValue')}</span>{' '}
          <ObservationValue
            observation={original}
            label={valueLabel ?? original.code}
            testId="answer-original"
          />
        </div>

        {conflict === 'answered' && (
          <AlertBanner tone="borderline" title={t('answer.alreadyAnswered')}>
            {t('answer.alreadyAnsweredBody')}
          </AlertBanner>
        )}

        {conflict === 'not-yours' && (
          <AlertBanner tone="borderline" title={t('answer.notYours')}>
            {t('answer.notYoursBody')}
          </AlertBanner>
        )}

        {refusal !== null && (
          <AlertBanner tone="critical" title={refusal} onDismiss={() => setRefusal(null)}>
            {t('answer.nothingChanged')}
          </AlertBanner>
        )}

        {mode === 'idle' && (
          <div className="app-corrections__actions">
            {correctable ? (
              <Button
                variant="primary"
                data-testid="answer-correct"
                onClick={() => {
                  setMode('correct');
                  setRefusal(null);
                }}
              >
                {t('answer.correctAction')}
              </Button>
            ) : (
              // A shape this form cannot edit. Said out loud rather than drawn as a disabled
              // button: a disabled control teaches "not right now" and invites hunting for the
              // state in which it would work, and there is none on this screen.
              <p className="app-corrections__hint" data-testid="answer-not-correctable">
                {t('answer.notCorrectable')}
              </p>
            )}
            <Button
              variant="secondary"
              data-testid="answer-stands"
              onClick={() => {
                setMode('stands');
                setRefusal(null);
              }}
            >
              {t('answer.standsAction')}
            </Button>
          </div>
        )}

        {mode === 'correct' && (
          <div data-testid="answer-correct-form">
            <p className="app-page__description">{t('answer.correctBody')}</p>

            {shape === 'numeric' ? (
              <div className="app-corrections__answer-fields">
                <NumericInput
                  label={t('answer.valueLabel')}
                  description={t('answer.valueHint')}
                  required
                  value={startedValue}
                  error={fields.value}
                  disabled={correct.isPending}
                  data-testid="answer-value"
                  onValueChange={setValue}
                />
                {units.length > 0 && (
                  <Select
                    label={t('answer.unitLabel')}
                    description={t('answer.unitHint')}
                    value={startedUnit}
                    error={fields.unit}
                    disabled={correct.isPending}
                    data-testid="answer-unit"
                    options={units.map((measure) => ({
                      value: measure.code,
                      label:
                        bilingual(measure.display_en, measure.display_bn, locale)?.text ??
                        measure.code,
                    }))}
                    onChange={(event) => setUnit(event.target.value)}
                  />
                )}
              </div>
            ) : shape === 'boolean' ? (
              <Select
                label={t('answer.valueLabel')}
                required
                value={startedText}
                error={fields.value_bool}
                disabled={correct.isPending}
                data-testid="answer-value"
                options={[
                  { value: 'true', label: t('yes') },
                  { value: 'false', label: t('no') },
                ]}
                onChange={(event) => setText(event.target.value)}
              />
            ) : (
              <div className="app-corrections__field">
                <label htmlFor={`answer-text-${request.id}`}>{t('answer.valueLabel')}</label>
                <textarea
                  id={`answer-text-${request.id}`}
                  data-testid="answer-value"
                  className="app-corrections__note"
                  rows={2}
                  value={startedText}
                  disabled={correct.isPending}
                  onChange={(event) => setText(event.target.value)}
                />
              </div>
            )}

            <div className="app-corrections__field">
              <label htmlFor={`answer-note-${request.id}`}>{t('answer.noteLabel')}</label>
              <textarea
                id={`answer-note-${request.id}`}
                data-testid="answer-note"
                className="app-corrections__note"
                rows={2}
                maxLength={500}
                value={note}
                disabled={correct.isPending}
                aria-describedby={`answer-note-hint-${request.id}`}
                onChange={(event) => setNote(event.target.value)}
              />
              <p id={`answer-note-hint-${request.id}`} className="app-corrections__hint">
                {t('answer.noteHint')}
              </p>
            </div>

            <p className="app-corrections__hint">{t('answer.keepsOriginal')}</p>

            <div className="app-corrections__actions">
              <Button variant="quiet" disabled={correct.isPending} onClick={() => setMode('idle')}>
                {t('cancel')}
              </Button>
              <Button
                variant="primary"
                loading={correct.isPending}
                disabled={!ready}
                data-testid="answer-correct-confirm"
                onClick={() => correct.mutate()}
              >
                {role === 'supervisor'
                  ? t('answer.correctConfirmSupervisor')
                  : t('answer.correctConfirm')}
              </Button>
            </div>
          </div>
        )}

        {mode === 'stands' && (
          <div data-testid="answer-stands-form">
            <p className="app-page__description">{t('answer.standsBody')}</p>

            <div className="app-corrections__field">
              <label htmlFor={`answer-reason-${request.id}`}>{t('answer.reasonLabel')}</label>
              <textarea
                id={`answer-reason-${request.id}`}
                data-testid="answer-reason"
                className="app-corrections__note"
                rows={3}
                maxLength={500}
                value={reason}
                disabled={stands.isPending}
                placeholder={t('answer.reasonPlaceholder')}
                aria-describedby={`answer-reason-hint-${request.id}`}
                onChange={(event) => setReason(event.target.value)}
              />
              <p id={`answer-reason-hint-${request.id}`} className="app-corrections__hint">
                {t('answer.reasonHint')}
              </p>
              {fields.reason !== undefined && (
                <p className="app-corrections__field-error">{fields.reason}</p>
              )}
            </div>

            <div className="app-corrections__actions">
              <Button variant="quiet" disabled={stands.isPending} onClick={() => setMode('idle')}>
                {t('cancel')}
              </Button>
              <Button
                variant="primary"
                loading={stands.isPending}
                disabled={!rejectReasonAcceptable(reason) || stands.isPending}
                data-testid="answer-stands-confirm"
                onClick={() => stands.mutate()}
              >
                {t('answer.standsConfirm')}
              </Button>
            </div>
          </div>
        )}
      </div>
    </Card>
  );
}
