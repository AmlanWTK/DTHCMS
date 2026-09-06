'use client';

import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useState } from 'react';

import { ApiError, fieldMessages } from '@dthcms/api-client';
import { AlertBanner, Button, Card, Input } from '@dthcms/ui';

import {
  counselingGateKey,
  overrideAlreadyStands,
  overrideCounselingGate,
  overrideReasonAcceptable,
} from '../api/gate';

/**
 * Sending one patient past the counselling checkpoint, in your own name (CP57, §5.5).
 *
 * # Why this exists at all, when CP54's gate has no override
 *
 * The allergy gate has three honest answers and each takes five seconds, so an override
 * there would simply become the fast one. This gate asks for seven conversations across
 * three rooms, and the reasons they cannot happen are ordinary and outside anybody's
 * control: the interpreter does not arrive, the insulin corner is closed, the patient's
 * daughter is waiting with the car. A rigid gate in that situation is not obeyed — it is
 * routed around, and a gate people route around is worse than one with a recorded valve,
 * because the routing-around is invisible and this is not.
 *
 * # Why the reason is refused when blank, three times over
 *
 * The server refuses an empty reason with `422` against `reason`, and a database constraint
 * refuses it again. This form refuses it a third time and does not send it — not because the
 * server cannot be trusted, but because the round trip costs a physician's attention with a
 * patient in the chair, and because the *hint* beside the box is the honest part: the reason
 * is read by whoever reviews how often this happens, and that reading is the only thing that
 * keeps the valve from becoming the normal path. A field whose purpose is not stated becomes
 * a field people type a full stop into.
 *
 * # What this write does not do
 *
 * It covers nothing. No item becomes ticked, no session becomes complete, and the panel
 * above continues to list every outstanding item afterwards — under a banner that says the
 * patient was sent past rather than that the checklist was finished. The card says so before
 * the button is pressed, because "override" is a word people read as "resolve".
 *
 * Only the gate's cache key is invalidated. The sessions are untouched by design: an
 * override that refreshed them would suggest something about them changed.
 */

export interface OverrideGateProps {
  visitId: string;
  onGranted?: () => void;
  onCancel?: () => void;
}

export function OverrideGate({ visitId, onGranted, onCancel }: OverrideGateProps) {
  const t = useTranslations('counseling');
  const locale = useLocale();
  const client = useQueryClient();

  const [reason, setReason] = useState('');
  const [fields, setFields] = useState<Record<string, string>>({});
  const [refusal, setRefusal] = useState<string | null>(null);
  const [conflict, setConflict] = useState(false);

  const grant = useMutation({
    mutationFn: (why: string) => overrideCounselingGate(visitId, why),
    onSuccess: () => {
      setRefusal(null);
      setFields({});
      void client.invalidateQueries({ queryKey: counselingGateKey(visitId) });
      onGranted?.();
    },
    onError: (error: unknown) => {
      setFields({});
      setConflict(false);
      if (overrideAlreadyStands(error)) {
        // Somebody else granted one between this screen loading and the button being pressed.
        // Not a failure to retry: one override stands per visit, and the useful next act is
        // reading whose it is and what reason they gave.
        //
        // Told by `COUNSELING_GATE_ALREADY_OVERRIDDEN` rather than by the 409, because the
        // counselling endpoints answer 409 for three different facts and only this one means
        // the patient has already been let through.
        setConflict(true);
        setRefusal(null);
        return;
      }
      if (error instanceof ApiError) {
        const named = fieldMessages(error, locale);
        if (named.reason !== undefined) {
          // The server names `reason` for both of its refusals — an empty reason, and an
          // override on a visit where nothing is outstanding. Its own sentence is more
          // accurate than a generic one, so it goes beside the box it belongs to.
          setFields(named);
          setRefusal(null);
          return;
        }
      }
      setRefusal(t('panel.override.failed'));
    },
  });

  const ready = overrideReasonAcceptable(reason) && !grant.isPending;

  return (
    <Card elevation="raised" className="app-counseling-panel__override-form" compact>
      <div data-testid="override-form">
        <h3 className="app-counseling-panel__heading">{t('panel.override.title')}</h3>
        <p className="app-page__description">{t('panel.override.body')}</p>

        {conflict && (
          <AlertBanner tone="borderline" title={t('panel.override.alreadyStands')}>
            {t('panel.override.alreadyStandsBody')}
          </AlertBanner>
        )}

        {refusal && (
          <AlertBanner tone="critical" title={refusal} onDismiss={() => setRefusal(null)}>
            {t('panel.override.nothingChanged')}
          </AlertBanner>
        )}

        <Input
          label={t('panel.override.reasonLabel')}
          description={t('panel.override.reasonHint')}
          placeholder={t('panel.override.reasonPlaceholder')}
          required
          value={reason}
          error={fields.reason}
          disabled={grant.isPending}
          data-testid="override-reason"
          onChange={(event) => setReason(event.target.value)}
        />

        <div className="app-counseling-panel__override-actions">
          <Button variant="quiet" disabled={grant.isPending} onClick={onCancel}>
            {t('cancel')}
          </Button>
          <Button
            variant="primary"
            loading={grant.isPending}
            disabled={!ready}
            data-testid="override-confirm"
            onClick={() => grant.mutate(reason)}
          >
            {t('panel.override.confirm')}
          </Button>
        </div>
      </div>
    </Card>
  );
}
