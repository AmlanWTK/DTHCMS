'use client';

import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useState, type FormEvent } from 'react';

import { ApiError, fieldMessages } from '@dthcms/api-client';
import { AlertBanner, Button, Card, Input, Select } from '@dthcms/ui';

import { ConceptPicker } from '@/features/terminology';
import type { Locale } from '@/lib/i18n/config';

import {
  ONSET_PRECISIONS,
  SEVERITIES,
  emptyDraft,
  historyItemsKey,
  kindNamed,
  missingFields,
  recordHistoryItem,
  recordRequestFrom,
  type FamilyRelation,
  type HistoryDraft,
  type HistoryKind,
  type RecordHistoryItemRequest,
} from '../api/history';

import { kindLabel } from './historyText';

/**
 * Recording one item (CP53, §4.7).
 *
 * # Every field on this form is the server's decision
 *
 * Which controls appear comes from the chosen kind's rules — `requires_relation`,
 * `requires_duration`, `allows_severity`, `allows_onset`, `is_medication` — and never from a
 * switch on the kind's name. That is what `/v1/history/kinds` returns the rules *for*. A
 * form that remembered which kind needed what would ask for a relative on a complaint the
 * first time somebody reordered the list, and would silently stop asking for a duration if
 * a seventh kind arrived. The catalogue each kind draws on is the server's too: a complaint
 * is coded from the clinic's own dictionary and a comorbidity from ICD, and a picker that
 * chose for itself could make the record assert that a patient *presented with* type 2
 * diabetes.
 *
 * # An item may be uncoded, and that is not a failure
 *
 * The catalogue will not have a code for everything a history officer meets. Refusing those
 * would push them into a note field where nothing can find them, so an item with no coding
 * is allowed as long as `said` carries what the patient described — and that is exactly what
 * this form asks for when nothing has been picked. Those items are counted at
 * `/v1/history/uncoded`, and a growing count means the dictionary is wrong rather than the
 * officers.
 *
 * # Why the same rules are checked twice
 *
 * `missingFields` names the fields the server would refuse, using the server's own names, so
 * a complaint with no duration is caught before the request and the message lands on the
 * same control a 422 would land on. Nothing here is a validation *authority* — the server
 * enforces the identical rules and names the offending field — but a form that submits and
 * then reports what it already knew has spent a station operator's time to teach them
 * nothing.
 */

export interface AddHistoryItemProps {
  patientId: string;
  kinds: readonly HistoryKind[];
  relations: readonly FamilyRelation[];
  visitId?: string;
  onRecorded?: () => void;
  onCancel?: () => void;
}

/** The field names the server uses, so a refusal about one lands on the control that holds it. */
const NAMED_FIELDS = new Set([
  'kind',
  'code',
  'code_system',
  'code_version',
  'said',
  'relation',
  'duration_days',
  'severity',
  'onset_on',
  'onset_precision',
  'dose',
  'frequency',
]);

export function AddHistoryItem({
  patientId,
  kinds,
  relations,
  visitId,
  onRecorded,
  onCancel,
}: AddHistoryItemProps) {
  const t = useTranslations('history');
  const locale = useLocale() as Locale;
  const client = useQueryClient();

  // The kinds in the server's order, so the form opens on the one station 4 asks first.
  const ordered = [...kinds].sort((a, b) => a.ordering - b.ordering);
  const first = ordered[0];

  const [draft, setDraft] = useState<HistoryDraft>(() => emptyDraft(first?.kind ?? ''));
  const [fields, setFields] = useState<Record<string, string>>({});
  const [refusal, setRefusal] = useState<string | null>(null);

  const kind = kindNamed(ordered, draft.kind) ?? first;

  const record = useMutation({
    mutationFn: (request: RecordHistoryItemRequest) => recordHistoryItem(patientId, request),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: historyItemsKey(patientId) });
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

  if (!kind) {
    // The server returned no kinds at all. Not a crash and not an empty form: a form with
    // no kind cannot record anything, and saying so is more use than a submit button that
    // always refuses.
    return <AlertBanner tone="unknown" title={t('noKinds')} />;
  }

  const set = <K extends keyof HistoryDraft>(key: K, value: HistoryDraft[K]) =>
    setDraft((current) => ({ ...current, [key]: value }));

  function changeKind(name: string) {
    // The whole draft, not only the kind. Each kind draws on its own catalogue, so a concept
    // picked from the clinic's dictionary is not a legal coding for a comorbidity — carrying
    // it across would send a coding the server is right to refuse.
    setDraft(emptyDraft(name));
    setFields({});
    setRefusal(null);
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!kind || record.isPending) return;

    const missing = missingFields(kind, draft);
    if (missing.length > 0) {
      setFields(Object.fromEntries(missing.map((field) => [field, t(`required.${field}`)])));
      setRefusal(null);
      return;
    }

    setFields({});
    setRefusal(null);
    record.mutate({
      ...recordRequestFrom(kind, draft),
      ...(visitId === undefined ? {} : { visit_id: visitId }),
    });
  }

  const unmatched = Object.entries(fields)
    .filter(([field]) => !NAMED_FIELDS.has(field))
    .map(([, message]) => message);

  return (
    <Card elevation="raised" className="app-history-form">
      <form onSubmit={submit} noValidate aria-label={t('addTitle')}>
        <h3>{t('addTitle')}</h3>

        {refusal && <AlertBanner tone="critical" title={refusal} />}
        {unmatched.length > 0 && <AlertBanner tone="critical" title={unmatched.join(' ')} />}

        <Select
          label={t('form.kind')}
          description={t('form.kindHint')}
          value={draft.kind}
          error={fields.kind}
          options={ordered.map((one) => ({ value: one.kind, label: kindLabel(one, locale) }))}
          onChange={(event) => changeKind(event.target.value)}
        />

        {/* The kind's own catalogue, named by the server. Never chosen here. */}
        <ConceptPicker
          system={kind.code_system}
          value={draft.concept}
          label={t('form.concept', { kind: kindLabel(kind, locale) })}
          description={t('form.conceptHint')}
          onSelect={(concept) => set('concept', concept)}
          onClear={() => set('concept', null)}
        />
        {fields.code && (
          <p className="app-history-form__error" role="alert">
            {fields.code}
          </p>
        )}

        <Input
          label={t('form.said')}
          // Required in the one case the server requires it, and welcome in every other:
          // the catalogue says "Type 2 diabetes mellitus without complications" and the
          // patient said "sugar since the flood", and the second one is the clinical detail.
          description={draft.concept === null ? t('form.saidRequired') : t('form.saidHint')}
          placeholder={t('form.saidPlaceholder')}
          required={draft.concept === null}
          value={draft.said}
          error={fields.said}
          onChange={(event) => set('said', event.target.value)}
        />

        {kind.requires_relation && (
          <Select
            label={t('form.relation')}
            description={t('form.relationHint')}
            value={draft.relation}
            error={fields.relation}
            required
            options={[...relations]
              .sort((a, b) => a.ordering - b.ordering)
              .map((relation) => ({
                value: relation.relation,
                label:
                  locale === 'bn' && relation.display_bn
                    ? relation.display_bn
                    : relation.display_en,
              }))}
            onChange={(event) => set('relation', event.target.value)}
          />
        )}

        {kind.requires_duration && (
          <Input
            label={t('form.duration')}
            description={t('form.durationHint')}
            inputMode="numeric"
            required
            value={draft.durationDays}
            error={fields.duration_days}
            onChange={(event) => set('durationDays', event.target.value)}
          />
        )}

        {kind.allows_severity && (
          <Select
            label={t('form.severity')}
            description={t('form.severityHint')}
            placeholder={t('form.severityNone')}
            value={draft.severity}
            error={fields.severity}
            options={SEVERITIES.map((one) => ({ value: one, label: t(`severity.${one}`) }))}
            onChange={(event) => set('severity', event.target.value)}
          />
        )}

        {kind.allows_onset && (
          <>
            <Input
              label={t('form.onset')}
              description={t('form.onsetHint')}
              type="date"
              value={draft.onsetOn}
              error={fields.onset_on}
              onChange={(event) => set('onsetOn', event.target.value)}
            />
            {/* The precision is asked for, not inferred. "About two years ago" is a real
                answer, and storing it as an exact day makes a guess look like a measurement. */}
            <Select
              label={t('form.precision')}
              description={t('form.precisionHint')}
              value={draft.onsetPrecision}
              error={fields.onset_precision}
              options={ONSET_PRECISIONS.map((one) => ({
                value: one,
                label: t(`precision.${one}`),
              }))}
              onChange={(event) => set('onsetPrecision', event.target.value)}
            />
          </>
        )}

        {kind.is_medication && (
          <>
            <Input
              label={t('form.dose')}
              description={t('form.doseHint')}
              placeholder={t('form.dosePlaceholder')}
              value={draft.dose}
              error={fields.dose}
              onChange={(event) => set('dose', event.target.value)}
            />
            <Input
              label={t('form.frequency')}
              description={t('form.frequencyHint')}
              placeholder={t('form.frequencyPlaceholder')}
              value={draft.frequency}
              error={fields.frequency}
              onChange={(event) => set('frequency', event.target.value)}
            />
          </>
        )}

        <div className="app-history-form__actions">
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
