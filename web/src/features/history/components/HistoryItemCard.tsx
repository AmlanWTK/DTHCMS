'use client';

import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useState, type FormEvent } from 'react';

import { ApiError, fieldMessages } from '@dthcms/api-client';
import { AlertBanner, Button, Card, Input, Select } from '@dthcms/ui';

import { ConceptChip } from '@/features/terminology';
import { formatDateTime } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';

import {
  ONSET_PRECISIONS,
  SEVERITIES,
  amendHistoryItem,
  confirmHistoryItem,
  historyItemsKey,
  isConfirmed,
  needsConfirmation,
  itemCoding,
  reasonAcceptable,
  removeHistoryItem,
  type AmendHistoryItemRequest,
  type FamilyRelation,
  type HistoryItem,
  type HistoryKind,
} from '../api/history';

import { itemName, onsetText, relationLabel } from './historyText';

/**
 * One thing the patient brought with them, and the four things that can be done to it
 * (CP53, §4.7).
 *
 * # Confirm, amend, resolve, remove — and why they are four
 *
 * **Confirm** says this is still true. It is one request about one item, and the button
 * exists on this card rather than on the panel because a person answered a question about
 * *this* item. Nothing on this screen confirms more than one.
 *
 * **Amend** changes a detail — the dose, the duration, what the patient said. It cannot
 * change what the item *is*: the kind and the coding are absent from the contract's
 * amendment schema, because changing those is removing one item and recording another, and
 * collapsing the two is how an audit trail stops answering "when did this become metformin".
 *
 * **Resolve** and **remove** are the pair that must never become one button, and they sit
 * beside each other here so that the difference is read rather than remembered. `status:
 * RESOLVED` says *she had this and no longer does* — a clinical fact worth keeping, and a
 * list that hid it would make every follow-up look like a first visit. Removing says *this
 * should never have been recorded*, which is a correction, and it takes a reason because
 * the disagreement is the interesting part. The words on the buttons say which is which; no
 * icon and no colour is asked to carry that distinction.
 *
 * # Why the unconfirmed state is a word
 *
 * An item nobody has confirmed carries the word on the card, a question above the button,
 * and a `data-confirmed` attribute for the stylesheet — in that order of importance. The
 * tint is the fastest signal for the people it works for and it is never the only one.
 */

export interface HistoryItemCardProps {
  item: HistoryItem;
  kind: HistoryKind;
  relations: readonly FamilyRelation[];
  patientId: string;
  visitId?: string;
  mayWrite: boolean;
  mayConfirm: boolean;
}

/** Which of the three forms is open, if any. One at a time; this is not a queue to work. */
type Panel = 'amend' | 'resolve' | 'remove' | null;

export function HistoryItemCard({
  item,
  kind,
  relations,
  patientId,
  visitId,
  mayWrite,
  mayConfirm,
}: HistoryItemCardProps) {
  const t = useTranslations('history');
  const locale = useLocale() as Locale;
  const client = useQueryClient();

  const [panel, setPanel] = useState<Panel>(null);
  const [failure, setFailure] = useState<string | null>(null);

  const invalidate = () => client.invalidateQueries({ queryKey: historyItemsKey(patientId) });

  const confirmed = isConfirmed(item);
  // Whether to ask, which is not the same as whether it has been confirmed. A resolved item
  // is the record already answering — "she had this and no longer does" — and asking about it
  // every visit for the rest of her life would be asking a clinician to re-confirm the past.
  const asking = needsConfirmation(item);
  const coding = itemCoding(item);
  const name = itemName(item, locale);

  const confirm = useMutation({
    // One id. There is no list overload and there must not be one — see MedicalHistory.
    mutationFn: () => confirmHistoryItem(item.id, visitId),
    onSuccess: () => {
      setFailure(null);
      void invalidate();
    },
    onError: () => setFailure(t('confirmFailed')),
  });

  const resolve = useMutation({
    mutationFn: () =>
      amendHistoryItem(item.id, {
        status: 'RESOLVED',
        ...(visitId === undefined ? {} : { visit_id: visitId }),
      }),
    onSuccess: () => {
      setPanel(null);
      setFailure(null);
      void invalidate();
    },
    onError: () => setFailure(t('resolveFailed')),
  });

  const remove = useMutation({
    mutationFn: (reason: string) => removeHistoryItem(item.id, reason),
    onSuccess: () => {
      setPanel(null);
      setFailure(null);
      void invalidate();
    },
    onError: () => setFailure(t('removeFailed')),
  });

  const busy = confirm.isPending || resolve.isPending || remove.isPending;

  return (
    // A plain element rather than `Card`: the state of this row is carried on data
    // attributes the stylesheet reads, and the primitive takes no arbitrary props. The same
    // choice the alert row made, for the same reason.
    <article
      className="app-history-item"
      data-testid={`history-item-${item.id}`}
      data-confirmed={!asking}
      data-coded={coding !== null}
      data-status={item.status}
    >
      <div className="app-history-item__head">
        {/* The word first. An unconfirmed item is one nobody has vouched for since it was
            written down, and that has to survive a photograph of this screen. */}
        {asking && <span className="app-history-item__flag">{t('flag.unconfirmed')}</span>}

        {coding ? (
          <ConceptChip concept={coding} />
        ) : (
          // Not hidden and not dressed up as a coding. An uncoded item is legitimate — the
          // catalogue has nothing for what this patient described — and the honest display
          // is the patient's own words with a label saying no code stands behind them.
          <span className="app-history-item__uncoded" data-testid="uncoded-flag">
            {t('flag.uncoded')}
          </span>
        )}

        {/* A word, not a pill. The design system's status labels are clinical states —
            normal, borderline, critical — and "resolved" is none of them: it says the
            patient had this and no longer does, which is a fact about the item's life
            rather than about how worrying it is. */}
        {item.status === 'RESOLVED' && (
          <span className="app-history-item__status">{t('status.RESOLVED')}</span>
        )}
      </div>

      {item.said && <p className="app-history-item__said">{t('said', { said: item.said })}</p>}

      <Details item={item} kind={kind} relations={relations} />

      <p className="app-history-item__attribution">
        {t('recordedBy', {
          at: formatDateTime(Date.parse(item.recorded_at), locale),
          who: item.recorded_by,
        })}
      </p>

      {/* Said only where it is an outstanding question. On a resolved item "nobody has
          confirmed this" is true and useless — it reads as a task with no button, because
          the record has already answered and nobody is going to be asked again. */}
      {(confirmed || asking) && (
        <p className="app-history-item__confirmation">
          {confirmed
            ? t('confirmedAt', {
                at: formatDateTime(Date.parse(item.confirmed_at as string), locale),
              })
            : t('neverConfirmed')}
        </p>
      )}

      {item.amended_at && (
        <p className="app-history-item__amended">
          {t('amendedAt', { at: formatDateTime(Date.parse(item.amended_at), locale) })}
        </p>
      )}

      {failure && (
        <AlertBanner tone="critical" title={failure} onDismiss={() => setFailure(null)}>
          {t('nothingChanged')}
        </AlertBanner>
      )}

      {asking && mayConfirm && (
        <div className="app-history-item__confirm">
          <p className="app-history-item__question">{t('stillTrue')}</p>
          <Button
            variant="primary"
            loading={confirm.isPending}
            disabled={busy}
            // Named with the item, because a list of six otherwise offers a screen reader
            // six controls all called "Yes, still true".
            aria-label={t('confirmNamed', { what: name })}
            onClick={() => confirm.mutate()}
          >
            {t('confirm')}
          </Button>
        </div>
      )}

      {mayWrite && panel === null && (
        <div className="app-history-item__actions">
          <Button variant="quiet" disabled={busy} onClick={() => setPanel('amend')}>
            {t('amend')}
          </Button>

          {/* Two controls, two sentences, side by side on purpose. */}
          {item.status === 'ACTIVE' && (
            <Button
              variant="quiet"
              disabled={busy}
              aria-label={t('resolveNamed', { what: name })}
              onClick={() => setPanel('resolve')}
            >
              {t('resolve')}
            </Button>
          )}

          <Button
            variant="quiet"
            disabled={busy}
            aria-label={t('removeNamed', { what: name })}
            onClick={() => setPanel('remove')}
          >
            {t('remove')}
          </Button>
        </div>
      )}

      {panel === 'amend' && (
        <AmendForm
          item={item}
          kind={kind}
          patientId={patientId}
          visitId={visitId}
          onDone={() => setPanel(null)}
          onCancel={() => setPanel(null)}
        />
      )}

      {panel === 'resolve' && (
        <Card elevation="raised" className="app-history-item__panel" compact>
          <h4>{t('resolveTitle', { what: name })}</h4>
          <p>{t('resolveBody')}</p>
          <div className="app-history-item__panel-actions">
            <Button variant="quiet" disabled={busy} onClick={() => setPanel(null)}>
              {t('cancel')}
            </Button>
            <Button
              variant="primary"
              loading={resolve.isPending}
              disabled={busy}
              onClick={() => resolve.mutate()}
            >
              {t('resolveConfirm')}
            </Button>
          </div>
        </Card>
      )}

      {panel === 'remove' && (
        <RemoveForm
          name={name}
          busy={remove.isPending}
          onCancel={() => setPanel(null)}
          onRemove={(reason) => remove.mutate(reason)}
        />
      )}
    </article>
  );
}

/**
 * The per-kind detail, drawn from the kind's rules rather than from its name.
 *
 * A complaint shows a duration and a severity; a family history shows the relative; a
 * medicine shows the dose, the frequency and whether anybody has matched it to the
 * formulary. Which of those appear is `requires_duration`, `allows_severity`,
 * `requires_relation`, `allows_onset` and `is_medication` — the server's own answer — so a
 * seventh kind added tomorrow renders correctly here without this file changing.
 */
function Details({
  item,
  kind,
  relations,
}: {
  item: HistoryItem;
  kind: HistoryKind;
  relations: readonly FamilyRelation[];
}) {
  const t = useTranslations('history');
  const locale = useLocale() as Locale;

  const onset = onsetText(item, locale);

  const facts: { key: string; label: string; value: string }[] = [];

  if (kind.requires_relation && item.relation) {
    facts.push({
      key: 'relation',
      label: t('field.relation'),
      value: relationLabel(relations, item.relation, locale),
    });
  }

  if (kind.requires_duration && item.duration_days !== undefined) {
    facts.push({
      key: 'duration',
      label: t('field.duration'),
      // A duration a patient reported is a count in running text — "twenty-one days" — and
      // not a measurement transcribed onto a chart, so its numerals follow the language.
      // The plural is ICU's, which formats `#` with the locale's own digits: Bengali reads
      // ২১, and a duration is not the kind of number anybody transcribes onto a lab form.
      value: t('durationDays', { days: item.duration_days }),
    });
  }

  if (kind.allows_severity && item.severity) {
    facts.push({
      key: 'severity',
      label: t('field.severity'),
      value: t(`severity.${item.severity}`),
    });
  }

  if (kind.allows_onset && onset) {
    facts.push({ key: 'onset', label: t('field.onset'), value: onset });
  }

  if (kind.is_medication) {
    if (item.dose) facts.push({ key: 'dose', label: t('field.dose'), value: item.dose });
    if (item.frequency) {
      facts.push({ key: 'frequency', label: t('field.frequency'), value: item.frequency });
    }
    facts.push({
      key: 'reconciliation',
      label: t('field.reconciliation'),
      // `NOT_STOCKED` is a finding, not a failure: somebody looked, and this clinic does not
      // carry it, which is worth knowing before a prescription is written.
      value: t(`reconciliation.${item.reconciliation ?? 'UNRECONCILED'}`),
    });
  }

  if (facts.length === 0) return null;

  return (
    <dl className="app-history-item__facts">
      {facts.map((fact) => (
        <div key={fact.key} className="app-history-item__fact" data-fact={fact.key}>
          <dt>{fact.label}</dt>
          <dd>{fact.value}</dd>
        </div>
      ))}
    </dl>
  );
}

/**
 * Correcting a detail.
 *
 * Only the fields the kind actually has, and only the ones that changed are sent: an absent
 * field means unchanged and an empty string clears it, which are two different requests the
 * contract distinguishes and this form has to be able to make both of.
 *
 * The duration is the exception and it is named here rather than left to be discovered: the
 * contract types it as a number, so an emptied box cannot express "clear this". It is left
 * alone instead, which is the safe half of the wrong answer — a stale duration is visible
 * and correctable, a silently dropped one is not.
 */
function AmendForm({
  item,
  kind,
  patientId,
  visitId,
  onDone,
  onCancel,
}: {
  item: HistoryItem;
  kind: HistoryKind;
  patientId: string;
  visitId?: string;
  onDone: () => void;
  onCancel: () => void;
}) {
  const t = useTranslations('history');
  const locale = useLocale();
  const client = useQueryClient();

  const [said, setSaid] = useState(item.said ?? '');
  const [severity, setSeverity] = useState<string>(item.severity ?? '');
  const [duration, setDuration] = useState(
    item.duration_days === undefined ? '' : String(item.duration_days),
  );
  const [onsetOn, setOnsetOn] = useState(item.onset_on ?? '');
  const [precision, setPrecision] = useState<string>(item.onset_precision ?? 'day');
  const [dose, setDose] = useState(item.dose ?? '');
  const [frequency, setFrequency] = useState(item.frequency ?? '');

  const [fields, setFields] = useState<Record<string, string>>({});
  const [refusal, setRefusal] = useState<string | null>(null);

  const amend = useMutation({
    mutationFn: (changes: AmendHistoryItemRequest) => amendHistoryItem(item.id, changes),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: historyItemsKey(patientId) });
      onDone();
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
      setRefusal(t('amendFailed'));
    },
  });

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (amend.isPending) return;
    setFields({});
    setRefusal(null);

    const changes: AmendHistoryItemRequest = {
      ...(visitId === undefined ? {} : { visit_id: visitId }),
      ...(said !== (item.said ?? '') ? { said } : {}),
      ...(kind.allows_severity && severity !== (item.severity ?? '') ? { severity } : {}),
      ...(kind.requires_duration &&
      duration.trim() !== '' &&
      Number(duration) !== item.duration_days &&
      Number.isFinite(Number(duration))
        ? { duration_days: Number(duration) }
        : {}),
      ...(kind.allows_onset && onsetOn !== (item.onset_on ?? '')
        ? { onset_on: onsetOn, onset_precision: precision }
        : {}),
      ...(kind.is_medication && dose !== (item.dose ?? '') ? { dose } : {}),
      ...(kind.is_medication && frequency !== (item.frequency ?? '') ? { frequency } : {}),
    };

    amend.mutate(changes);
  }

  return (
    <Card elevation="raised" className="app-history-item__panel" compact>
      <form onSubmit={submit} noValidate aria-label={t('amendTitle')}>
        <h4>{t('amendTitle')}</h4>
        {/* Stated, because it surprises people: an amendment is a fresh assertion about the
            item, so the server stamps it confirmed as it changes. */}
        <p className="app-history-item__note">{t('amendNote')}</p>

        {refusal && <AlertBanner tone="critical" title={refusal} />}

        <Input
          label={t('form.said')}
          description={t('form.saidHint')}
          value={said}
          error={fields.said}
          onChange={(event) => setSaid(event.target.value)}
        />

        {kind.requires_duration && (
          <Input
            label={t('form.duration')}
            description={t('form.durationHint')}
            inputMode="numeric"
            value={duration}
            error={fields.duration_days}
            onChange={(event) => setDuration(event.target.value)}
          />
        )}

        {kind.allows_severity && (
          <Select
            label={t('form.severity')}
            value={severity}
            error={fields.severity}
            placeholder={t('form.severityNone')}
            options={SEVERITIES.map((one) => ({ value: one, label: t(`severity.${one}`) }))}
            onChange={(event) => setSeverity(event.target.value)}
          />
        )}

        {kind.allows_onset && (
          <>
            <Input
              label={t('form.onset')}
              description={t('form.onsetHint')}
              type="date"
              value={onsetOn}
              error={fields.onset_on}
              onChange={(event) => setOnsetOn(event.target.value)}
            />
            <Select
              label={t('form.precision')}
              value={precision}
              error={fields.onset_precision}
              options={ONSET_PRECISIONS.map((one) => ({
                value: one,
                label: t(`precision.${one}`),
              }))}
              onChange={(event) => setPrecision(event.target.value)}
            />
          </>
        )}

        {kind.is_medication && (
          <>
            <Input
              label={t('form.dose')}
              description={t('form.doseHint')}
              value={dose}
              error={fields.dose}
              onChange={(event) => setDose(event.target.value)}
            />
            <Input
              label={t('form.frequency')}
              description={t('form.frequencyHint')}
              value={frequency}
              error={fields.frequency}
              onChange={(event) => setFrequency(event.target.value)}
            />
          </>
        )}

        <div className="app-history-item__panel-actions">
          <Button variant="quiet" type="button" disabled={amend.isPending} onClick={onCancel}>
            {t('cancel')}
          </Button>
          <Button variant="primary" type="submit" loading={amend.isPending}>
            {t('amendSave')}
          </Button>
        </div>
      </form>
    </Card>
  );
}

/**
 * Saying an item should never have been recorded.
 *
 * The reason is mandatory on the server and the button stays disabled without one, because
 * a form that submits and then reports "reason is required" has spent the operator's time
 * teaching them something it already knew. The wording is deliberately not "delete": nothing
 * is deleted, the row stays, and the ledger keeps both the recording and the removal.
 */
function RemoveForm({
  name,
  busy,
  onCancel,
  onRemove,
}: {
  name: string;
  busy: boolean;
  onCancel: () => void;
  onRemove: (reason: string) => void;
}) {
  const t = useTranslations('history');
  const [reason, setReason] = useState('');
  const ready = reasonAcceptable(reason) && !busy;

  return (
    <Card elevation="raised" className="app-history-item__panel" compact>
      <h4>{t('removeTitle', { what: name })}</h4>
      <p>{t('removeBody')}</p>
      <Input
        label={t('removeReason')}
        description={t('removeReasonHint')}
        placeholder={t('removeReasonPlaceholder')}
        value={reason}
        required
        disabled={busy}
        onChange={(event) => setReason(event.target.value)}
      />
      <div className="app-history-item__panel-actions">
        <Button variant="quiet" disabled={busy} onClick={onCancel}>
          {t('cancel')}
        </Button>
        <Button
          variant="primary"
          loading={busy}
          disabled={!ready}
          onClick={() => onRemove(reason.trim())}
        >
          {t('removeConfirm')}
        </Button>
      </div>
    </Card>
  );
}
