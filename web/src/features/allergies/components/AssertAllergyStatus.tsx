'use client';

import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useState } from 'react';

import { ApiError, fieldMessages } from '@dthcms/api-client';
import { AlertBanner, Button, Card, Input } from '@dthcms/ui';

import {
  allergyChangesKey,
  allergyStateKey,
  assertAllergyStatus,
  assertionRequestFrom,
  missingAssertionFields,
  type AssertionKind,
  type AssertionRequest,
} from '../api/allergies';

/**
 * Saying, in your own name, what the allergy answer is (CP54, acceptance criterion 2).
 *
 * # Why this is two panels and not one control with a default
 *
 * "No known allergies" must never be a default or an empty field, and the only way to make
 * that structural rather than remembered is to require a positive act with a person behind
 * it. So each kind is opened by its own button, states in full what is about to be claimed,
 * and is confirmed deliberately. Neither is pre-selected anywhere, and there is no third
 * panel.
 *
 * # Why "unable to assess" is not an override
 *
 * The unconscious patient and the child brought in by a neighbour are real, and the usual
 * answer is a button that advances them anyway with a reason attached. That answer is wrong
 * here: a gate with a way past it is a gate people learn the shape of, and within a month
 * the override is the normal path. So the third state *is* allergy status — somebody
 * looked, somebody is named, the record says what was found — and it is emphatically not a
 * claim that there are none. Everything downstream treats the two differently because they
 * are different rows rather than one row and a missing one.
 *
 * # Why the reason is required here and refused there
 *
 * The point of the third state is that it is reviewable, and "we could not ask" with no
 * reason is a silent gap wearing a label. The button stays disabled without one, rather
 * than submitting and reporting what the form already knew. `NO_KNOWN_ALLERGY` carries no
 * reason at all — `assertionRequestFrom` drops it — because text nobody will read,
 * answering a question nobody asked, is how a required field becomes a ritual.
 */

export interface AssertAllergyStatusProps {
  patientId: string;
  kind: AssertionKind;
  visitId?: string;
  onAsserted?: () => void;
  onCancel?: () => void;
}

export function AssertAllergyStatus({
  patientId,
  kind,
  visitId,
  onAsserted,
  onCancel,
}: AssertAllergyStatusProps) {
  const t = useTranslations('allergies');
  const locale = useLocale();
  const client = useQueryClient();

  const [reason, setReason] = useState('');
  const [fields, setFields] = useState<Record<string, string>>({});
  const [refusal, setRefusal] = useState<string | null>(null);

  const assert = useMutation({
    mutationFn: (request: AssertionRequest) => assertAllergyStatus(patientId, request),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: allergyStateKey(patientId) });
      void client.invalidateQueries({ queryKey: allergyChangesKey(patientId) });
      onAsserted?.();
    },
    onError: (error: unknown) => {
      if (error instanceof ApiError) {
        const named = fieldMessages(error, locale);
        if (Object.keys(named).length > 0) {
          // The server names `reason` when it refuses one — required on the third state,
          // refused on the first. Either way the sentence belongs beside the box, and when
          // there is no box for it the banner below carries it rather than swallowing it.
          setFields(named);
          setRefusal(named.reason === undefined ? t('assertFailed') : null);
          return;
        }
      }
      setRefusal(t('assertFailed'));
    },
  });

  const needsReason = kind === 'UNABLE_TO_ASSESS';
  const missing = missingAssertionFields(kind, reason);
  const ready = missing.length === 0 && !assert.isPending;

  return (
    <Card elevation="raised" className="app-allergy-assert" compact>
      <div data-testid={`assert-${kind}`}>
        <h4>{t(`assert.${kind}.title`)}</h4>
        <p className="app-allergy-assert__body">{t(`assert.${kind}.body`)}</p>

        {refusal && <AlertBanner tone="critical" title={refusal} />}

        {needsReason && (
          <Input
            label={t('assert.reason')}
            description={t('assert.reasonHint')}
            placeholder={t('assert.reasonPlaceholder')}
            required
            value={reason}
            error={fields.reason}
            disabled={assert.isPending}
            onChange={(event) => setReason(event.target.value)}
          />
        )}

        <div className="app-allergy-assert__actions">
          <Button variant="quiet" disabled={assert.isPending} onClick={onCancel}>
            {t('cancel')}
          </Button>
          <Button
            variant="primary"
            loading={assert.isPending}
            disabled={!ready}
            onClick={() => assert.mutate(assertionRequestFrom(kind, reason, visitId))}
          >
            {t(`assert.${kind}.confirm`)}
          </Button>
        </div>
      </div>
    </Card>
  );
}
