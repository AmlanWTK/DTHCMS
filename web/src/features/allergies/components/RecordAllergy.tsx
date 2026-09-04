'use client';

import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useState, type FormEvent } from 'react';

import { ApiError, fieldMessages } from '@dthcms/api-client';
import { AlertBanner, Button, Card, Input, Select } from '@dthcms/ui';

import { ConceptPicker } from '@/features/terminology';
import type { Locale } from '@/lib/i18n/config';

import {
  CERTAINTIES,
  SEVERITIES,
  allergyChangesKey,
  allergyStateKey,
  emptyAllergyDraft,
  missingAllergyFields,
  reactionsInOrder,
  recordAllergy,
  recordRequestFrom,
  type AllergyDraft,
  type AllergyReaction,
  type RecordAllergyRequest,
} from '../api/allergies';

import { reactionLabel } from './allergyText';

/**
 * Recording one allergy (CP54).
 *
 * # The substance is coded, or it says it is not
 *
 * The escape hatch matters more here than anywhere else in the system. The clinic's
 * dictionary will not have a code for "the yellow tablet from the pharmacy near the
 * bridge", and refusing that would push it into a note field where nothing can find it —
 * so an allergy with no coding is allowed as long as the patient's own words carry it, and
 * the row is marked uncoded everywhere it is shown afterwards. The coding travels as three
 * fields or as none: `recordRequestFrom` is the only place they are written.
 *
 * # Severity and certainty have no default
 *
 * Both start empty and the form refuses without them. A pre-selected "moderate" is a
 * clinical claim nobody made, sitting in the one record a pharmacist reads before handing
 * over a medicine — and this whole checkpoint exists because a field that means something
 * by being empty is a field that eventually means it by accident.
 *
 * # The reaction comes from the vocabulary
 *
 * A free-text reaction is a row the header cannot draw, and an allergy that shows as a
 * blank line is worse than one nobody recorded, because the blank line reads as "checked,
 * nothing found". The emergency reactions are offered first and say so in words, since
 * `is_emergency` is a property of the reaction rather than of the severity beside it.
 */

export interface RecordAllergyProps {
  patientId: string;
  reactions: readonly AllergyReaction[];
  visitId?: string;
  onRecorded?: () => void;
  onCancel?: () => void;
}

/** The field names the server uses, so a refusal about one lands on the control that holds it. */
const NAMED_FIELDS = new Set([
  'code',
  'code_system',
  'code_version',
  'said',
  'reaction',
  'severity',
  'certainty',
  'note',
]);

export function RecordAllergy({
  patientId,
  reactions,
  visitId,
  onRecorded,
  onCancel,
}: RecordAllergyProps) {
  const t = useTranslations('allergies');
  const locale = useLocale() as Locale;
  const client = useQueryClient();

  const [draft, setDraft] = useState<AllergyDraft>(emptyAllergyDraft);
  const [fields, setFields] = useState<Record<string, string>>({});
  const [refusal, setRefusal] = useState<string | null>(null);

  const ordered = reactionsInOrder(reactions);

  const record = useMutation({
    mutationFn: (request: RecordAllergyRequest) => recordAllergy(patientId, request),
    onSuccess: () => {
      // Both, and the state first: recording an allergy moves the gate, and the header on
      // every screen reads that key.
      void client.invalidateQueries({ queryKey: allergyStateKey(patientId) });
      void client.invalidateQueries({ queryKey: allergyChangesKey(patientId) });
      onRecorded?.();
    },
    onError: (error: unknown) => {
      if (error instanceof ApiError) {
        const named = fieldMessages(error, locale);
        if (Object.keys(named).length > 0) {
          setFields(named);
          setRefusal(null);
          return;
        }
      }
      setRefusal(t('recordFailed'));
    },
  });

  if (ordered.length === 0) {
    // No vocabulary means no reaction can be chosen, and a reaction is what makes this an
    // allergy rather than a note. Saying so beats a submit button that always refuses.
    return <AlertBanner tone="unknown" title={t('noReactions')} />;
  }

  const set = <K extends keyof AllergyDraft>(key: K, value: AllergyDraft[K]) =>
    setDraft((current) => ({ ...current, [key]: value }));

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (record.isPending) return;

    const missing = missingAllergyFields(draft);
    if (missing.length > 0) {
      setFields(Object.fromEntries(missing.map((field) => [field, t(`required.${field}`)])));
      setRefusal(null);
      return;
    }

    setFields({});
    setRefusal(null);
    record.mutate(recordRequestFrom(draft, visitId));
  }

  const unmatched = Object.entries(fields)
    .filter(([field]) => !NAMED_FIELDS.has(field))
    .map(([, message]) => message);

  return (
    <Card elevation="raised" className="app-allergy-form">
      <form onSubmit={submit} noValidate aria-label={t('recordTitle')}>
        <h3>{t('recordTitle')}</h3>

        {refusal && <AlertBanner tone="critical" title={refusal} />}
        {unmatched.length > 0 && <AlertBanner tone="critical" title={unmatched.join(' ')} />}

        {/* The clinic's own dictionary. Allergens are not an ICD question. */}
        <ConceptPicker
          system="DTHC"
          value={draft.concept}
          label={t('form.substance')}
          description={t('form.substanceHint')}
          onSelect={(concept) => set('concept', concept)}
          onClear={() => set('concept', null)}
        />
        {fields.code && (
          <p className="app-allergy-form__error" role="alert">
            {fields.code}
          </p>
        )}

        <Input
          label={t('form.said')}
          description={draft.concept === null ? t('form.saidRequired') : t('form.saidHint')}
          placeholder={t('form.saidPlaceholder')}
          required={draft.concept === null}
          value={draft.said}
          error={fields.said}
          onChange={(event) => set('said', event.target.value)}
        />

        <Select
          label={t('form.reaction')}
          description={t('form.reactionHint')}
          placeholder={t('form.choose')}
          required
          value={draft.reaction}
          error={fields.reaction}
          options={ordered.map((one) => ({
            value: one.reaction,
            // The emergency ones say so in the option itself. A dropdown that separated
            // them only by position tells nobody anything once it is open.
            label: one.is_emergency
              ? t('reactionEmergency', { reaction: reactionLabel(one, locale) })
              : reactionLabel(one, locale),
          }))}
          onChange={(event) => set('reaction', event.target.value)}
        />

        <Select
          label={t('form.severity')}
          description={t('form.severityHint')}
          // No default. See the note at the top: a pre-selected severity is a claim
          // nobody made, in the record a pharmacist reads.
          placeholder={t('form.choose')}
          required
          value={draft.severity}
          error={fields.severity}
          options={SEVERITIES.map((one) => ({ value: one, label: t(`severity.${one}`) }))}
          onChange={(event) => set('severity', event.target.value)}
        />

        <Select
          label={t('form.certainty')}
          description={t('form.certaintyHint')}
          placeholder={t('form.choose')}
          required
          value={draft.certainty}
          error={fields.certainty}
          options={CERTAINTIES.map((one) => ({ value: one, label: t(`certainty.${one}`) }))}
          onChange={(event) => set('certainty', event.target.value)}
        />

        <Input
          label={t('form.note')}
          description={t('form.noteHint')}
          value={draft.note}
          error={fields.note}
          onChange={(event) => set('note', event.target.value)}
        />

        <div className="app-allergy-form__actions">
          {onCancel && (
            <Button variant="quiet" type="button" disabled={record.isPending} onClick={onCancel}>
              {t('cancel')}
            </Button>
          )}
          <Button variant="primary" type="submit" loading={record.isPending}>
            {t('form.record')}
          </Button>
        </div>
      </form>
    </Card>
  );
}
