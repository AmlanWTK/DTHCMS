'use client';

import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useState } from 'react';

import { AlertBanner, Button, Card, Input } from '@dthcms/ui';

import { ConceptChip } from '@/features/terminology';
import { formatDateTime } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';

import {
  allergyChangesKey,
  allergyCoding,
  allergyStateKey,
  isEmergency,
  reasonAcceptable,
  withdrawAllergy,
  type Allergy,
} from '../api/allergies';

import { reactionName, substanceName } from './allergyText';

/**
 * One recorded allergy, and the one thing that can be done to it (CP54).
 *
 * # Withdrawing is never a deletion
 *
 * An allergy somebody withdrew is one somebody disagreed with, and the next clinician
 * reading the record needs to know that a colleague once believed it — so the row stays,
 * the reason is attached, and both halves show in the change history. The word on the
 * button is "withdraw" rather than "delete" for exactly that reason, and the reason box is
 * mandatory because the disagreement is the interesting part.
 *
 * Withdrawing the last allergy can **re-close the gate**: the patient drops back to
 * whatever assertion stands behind it, or to nothing at all. The response carries the
 * resulting status and this card invalidates the state the header reads, so the strip on
 * every screen changes in the same breath rather than going stale in the safe-looking
 * direction.
 *
 * # Why the danger is a word
 *
 * `is_emergency` is a property of the reaction — anaphylaxis is an emergency whatever
 * severity somebody ticked — and `life_threatening` is a property of what happened. Either
 * puts a word on the card before any tint. A colour is the fastest signal for the people it
 * works for and it is never the only one here.
 */

export interface AllergyCardProps {
  allergy: Allergy;
  patientId: string;
  mayWrite: boolean;
}

export function AllergyCard({ allergy, patientId, mayWrite }: AllergyCardProps) {
  const t = useTranslations('allergies');
  const locale = useLocale() as Locale;
  const client = useQueryClient();

  const [withdrawing, setWithdrawing] = useState(false);
  const [reason, setReason] = useState('');
  const [failure, setFailure] = useState<string | null>(null);

  const coding = allergyCoding(allergy);
  const urgent = isEmergency(allergy);
  const name = substanceName(allergy, locale);

  const withdraw = useMutation({
    mutationFn: (why: string) => withdrawAllergy(allergy.id, why),
    onSuccess: () => {
      setWithdrawing(false);
      setFailure(null);
      void client.invalidateQueries({ queryKey: allergyStateKey(patientId) });
      void client.invalidateQueries({ queryKey: allergyChangesKey(patientId) });
    },
    onError: () => setFailure(t('withdrawFailed')),
  });

  return (
    <article
      className="app-allergy-item"
      data-testid={`allergy-${allergy.id}`}
      data-emergency={urgent}
      data-coded={coding !== null}
      data-severity={allergy.severity}
    >
      <div className="app-allergy-item__head">
        {/* The word first, and never the hue alone. */}
        {urgent && <span className="app-allergy-item__flag">{t('flag.emergency')}</span>}

        {coding ? (
          <ConceptChip concept={coding} />
        ) : (
          <span className="app-allergy-item__uncoded" data-testid="uncoded-flag">
            {t('flag.uncoded')}
          </span>
        )}

        {/* Only where the chip is not already saying it. `ConceptChip` renders the
            substance's own display, so printing `name` beside a coded allergy put
            "Penicillin group" on the card twice — which reads as two allergies at a glance,
            on the one screen where a miscount is expensive. On an uncoded allergy the chip
            says only "no code", and the name is the whole of what there is to show. */}
        {!coding && <span className="app-allergy-item__substance">{name}</span>}
      </div>

      {/* Her words, kept beside the coding and never instead of it — sometimes the only
          thing that identifies what actually happened. */}
      {allergy.said && (
        <p className="app-allergy-item__said">{t('said', { said: allergy.said })}</p>
      )}

      <dl className="app-allergy-item__facts">
        <div className="app-allergy-item__fact" data-fact="reaction">
          <dt>{t('field.reaction')}</dt>
          <dd>{reactionName(allergy, locale)}</dd>
        </div>
        <div className="app-allergy-item__fact" data-fact="severity">
          <dt>{t('field.severity')}</dt>
          <dd>{t(`severity.${allergy.severity}`)}</dd>
        </div>
        <div className="app-allergy-item__fact" data-fact="certainty">
          <dt>{t('field.certainty')}</dt>
          <dd>{t(`certainty.${allergy.certainty}`)}</dd>
        </div>
      </dl>

      {allergy.note && <p className="app-allergy-item__note">{allergy.note}</p>}

      <p className="app-allergy-item__attribution">
        {t('recordedBy', {
          at: formatDateTime(Date.parse(allergy.recorded_at), locale),
          who: allergy.recorded_by,
        })}
      </p>

      {failure && (
        <AlertBanner tone="critical" title={failure} onDismiss={() => setFailure(null)}>
          {t('nothingChanged')}
        </AlertBanner>
      )}

      {mayWrite && !withdrawing && (
        <div className="app-allergy-item__actions">
          <Button
            variant="quiet"
            // Named with the substance, because a list of four otherwise offers a screen
            // reader four controls all called "Withdraw".
            aria-label={t('withdrawNamed', { what: name })}
            onClick={() => setWithdrawing(true)}
          >
            {t('withdraw')}
          </Button>
        </div>
      )}

      {withdrawing && (
        <Card elevation="raised" className="app-allergy-item__panel" compact>
          <h4>{t('withdrawTitle', { what: name })}</h4>
          <p>{t('withdrawBody')}</p>
          <Input
            label={t('withdrawReason')}
            description={t('withdrawReasonHint')}
            placeholder={t('withdrawReasonPlaceholder')}
            required
            value={reason}
            disabled={withdraw.isPending}
            onChange={(event) => setReason(event.target.value)}
          />
          <div className="app-allergy-item__panel-actions">
            <Button
              variant="quiet"
              disabled={withdraw.isPending}
              onClick={() => setWithdrawing(false)}
            >
              {t('cancel')}
            </Button>
            <Button
              variant="primary"
              loading={withdraw.isPending}
              // Disabled without a reason rather than submitting and then reporting what
              // the form already knew.
              disabled={!reasonAcceptable(reason) || withdraw.isPending}
              onClick={() => withdraw.mutate(reason.trim())}
            >
              {t('withdrawConfirm')}
            </Button>
          </div>
        </Card>
      )}
    </article>
  );
}
