'use client';

import { useLocale, useTranslations } from 'next-intl';
import { Fragment, useMemo, useState } from 'react';

import { ApiError, fieldMessages } from '@dthcms/api-client';
import { normalisePhone, readDate, type DateParts } from '@dthcms/shared-schemas';
import { AlertBanner, Badge, Button, Card } from '@dthcms/ui';

import { StepUpCancelled, useStepUp } from '@/features/auth';

import {
  HIGH_IMPACT_FIELDS,
  REASON_MIN,
  correctPatient,
  isHighImpact,
  newEventId,
  reasonAcceptable,
  type CorrectableField,
  type CorrectionApplied,
  type CorrectionRequest,
  type Patient,
} from '../api/patients';
import { BirthDateField } from './BirthDateField';

/**
 * Correcting a patient's demographics (CP35, §4.3).
 *
 * The screen is built around one rule: **send only what changed**. Every field is rendered
 * with the value on file, and a field the operator did not touch is not in the request —
 * not sent as "the same value", not sent at all. A form that posts everything it rendered
 * makes "what did they actually alter" unanswerable, and that is the exact question the
 * history exists to answer. It also means a stale tab cannot quietly revert a colleague's
 * correction made two minutes ago.
 *
 * So the visible state of this form is the diff, not the record: changed fields are marked
 * as you type, the previous value is printed underneath, and the count above the button
 * names how many fields will change rather than only greying out.
 *
 * The **date of birth is CP32's field, not a date input.** A native date picker renders as
 * `06/14/1985` or `14/06/1985` depending on the browser's locale, and a correction screen
 * whose most dangerous field is ambiguous about which number is the month is the exact
 * hazard [R-06] describes. The three-field control with the age echoed in words is reused
 * whole, so a mistyped year is obvious here for the same reason it is obvious at
 * registration.
 *
 * The **reason** is not a formality and the copy says so. "Correction" is not a reason; "the
 * NID card reads 1985, the desk typed 1958" is one, and it is what somebody reading the
 * history in two years actually needs.
 *
 * A **high-impact** correction — the date of birth, its precision, the sex, the English name
 * — asks for an authenticator code *before* submitting rather than submitting, being refused
 * and asking afterwards, which on a tablet loses the typing. The server still decides; this
 * only decides what the browser offers first.
 */

/** The plain text and select fields, in the order a person reads a record. */
const TEXT_FIELDS = [
  'name_en',
  'name_bn',
  'sex',
  'phone_primary',
  'phone_secondary',
  'division',
  'district',
  'upazila',
  'address_line',
  'postcode',
] as const satisfies readonly CorrectableField[];

/** Grouped, because a screen that puts "date precision" next to "mobile number" reads as a
    list of database columns rather than as a record about a person. */
const SECTIONS = [
  { key: 'identity', fields: ['name_en', 'name_bn', 'sex'] },
  // The date of birth's own card is rendered between these two, because it is the field a
  // correction most often exists to fix and it belongs with the name, not after the postcode.
  { key: 'contact', fields: ['phone_primary', 'phone_secondary'] },
  { key: 'address', fields: ['division', 'district', 'upazila', 'address_line', 'postcode'] },
] as const;

type TextField = (typeof TEXT_FIELDS)[number];
type Draft = Record<TextField, string>;

const SEXES = ['female', 'male', 'other'] as const;

function currentOf(patient: Patient): Draft {
  return {
    name_en: patient.name_en ?? '',
    name_bn: patient.name_bn ?? '',
    sex: patient.sex,
    phone_primary: patient.phone_primary ?? '',
    phone_secondary: patient.phone_secondary ?? '',
    division: patient.address.division ?? '',
    district: patient.address.district ?? '',
    upazila: patient.address.upazila ?? '',
    address_line: patient.address.address_line ?? '',
    postcode: patient.address.postcode ?? '',
  };
}

/** The three date fields, filled from the record's ISO date and its precision. */
export function partsOf(patient: Patient): DateParts {
  const [year = '', month = '', day = ''] = patient.birth.date.split('-');
  const precision = patient.birth.precision;
  return {
    year,
    month: precision === 'year' ? '' : month,
    day: precision === 'day' ? day : '',
  };
}

/**
 * Which text fields actually differ.
 *
 * Telephone numbers are compared **normalised**, because `01712-345678` and `+8801712345678`
 * are the same number and the server will refuse a correction that changes nothing. Marking
 * a retyped number as a change here would produce a button that submits and then fails.
 */
export function changedFields(before: Draft, after: Draft): TextField[] {
  return TEXT_FIELDS.filter((field) => {
    if (field === 'phone_primary' || field === 'phone_secondary') {
      return normalisePhone(before[field]) !== normalisePhone(after[field]);
    }
    return before[field].trim() !== after[field].trim();
  });
}

/** What the date fields and the source amount to, as a correction — or nothing. */
export function changedBirth(
  patient: Patient,
  parts: DateParts,
  source: string,
): Partial<Pick<CorrectionRequest, 'birth_date' | 'dob_precision' | 'dob_source'>> {
  const read = readDate(parts);
  const out: Record<string, string> = {};
  if (read) {
    if (read.iso !== patient.birth.date) out.birth_date = read.iso;
    if (read.precision !== patient.birth.precision) out.dob_precision = read.precision;
  }
  if (source && source !== patient.birth.source) out.dob_source = source;
  return out;
}

export function CorrectionForm({
  patient,
  today = new Date(),
  onCorrected,
}: {
  patient: Patient;
  /** Injected so the age echo is testable and so the clinic's calendar decides it. */
  today?: Date;
  onCorrected?: (applied: CorrectionApplied) => void;
}) {
  const t = useTranslations('patients.correct');
  const locale = useLocale();
  const requestStepUp = useStepUp();

  const before = useMemo(() => currentOf(patient), [patient]);
  const [draft, setDraft] = useState<Draft>(before);
  const [parts, setParts] = useState<DateParts>(() => partsOf(patient));
  const [source, setSource] = useState<string>(patient.birth.source);
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [refusal, setRefusal] = useState<string | null>(null);
  const [problems, setProblems] = useState<Record<string, string>>({});
  const [applied, setApplied] = useState<CorrectionApplied | null>(null);

  const changedText = changedFields(before, draft);
  const changedDate = changedBirth(patient, parts, source);
  const changed = [...changedText, ...Object.keys(changedDate)];
  const high = isHighImpact(changed);
  const ready = changed.length > 0 && reasonAcceptable(reason) && !busy;

  function set(field: TextField, value: string) {
    setDraft((current) => ({ ...current, [field]: value }));
  }

  async function submit() {
    if (!ready) return;
    setBusy(true);
    setRefusal(null);
    setProblems({});
    try {
      const body = {
        event_id: newEventId(),
        reason: reason.trim(),
        ...changedDate,
      } as CorrectionRequest;
      for (const field of changedText) {
        (body as Record<string, string>)[field] = draft[field].trim();
      }
      const token = high
        ? await requestStepUp(
            'patient_correct_identity',
            t('stepUp', { fields: changed.map((f) => t(`fields.${f}`)).join(', ') }),
          )
        : undefined;
      const result = await correctPatient(patient.id, body, token);
      setApplied(result);
      setReason('');
      onCorrected?.(result);
    } catch (error) {
      if (error instanceof StepUpCancelled) return;
      if (error instanceof ApiError) {
        const named = fieldMessages(error, locale);
        if (Object.keys(named).length > 0) {
          setProblems(named);
          return;
        }
      }
      setRefusal(error instanceof Error ? error.message : t('failed'));
    } finally {
      setBusy(false);
    }
  }

  if (applied) {
    return <Applied applied={applied} onAgain={() => setApplied(null)} />;
  }

  // CP32's control, whole. Three fields and the age in words, because this is the field a
  // correction most often exists to fix and the one a wrong fix does most damage to.
  const birthCard = (
    <Card>
      <h3 className="app-correct__section">
        {t('sections.birth')}
        <Badge tone="info">{t('highImpact')}</Badge>
      </h3>
      <div
        className="app-correct__birth"
        data-changed={Object.keys(changedDate).length > 0 || undefined}
        data-testid="correct-birth"
      >
        <BirthDateField
          value={parts}
          onChange={setParts}
          source={source}
          onSourceChange={setSource}
          today={today}
          serverError={problems.birth_date}
        />
        {Object.keys(changedDate).length > 0 ? (
          <p className="app-correct__was" data-testid="correct-birth-was">
            {t('was')}{' '}
            <span>
              {patient.birth.date} · {t(`precision.${patient.birth.precision}`)}
            </span>
          </p>
        ) : null}
      </div>
    </Card>
  );

  return (
    <section className="app-correct" aria-label={t('title')} data-testid="correction-form">
      <header className="app-correct__head">
        <h2>{t('title')}</h2>
        <p>{t('lede', { clinicalId: patient.clinical_id })}</p>
      </header>

      {SECTIONS.map((section) => (
        <Fragment key={section.key}>
          <Card>
            <h3 className="app-correct__section">{t(`sections.${section.key}`)}</h3>
            <div className="app-correct__grid">
              {section.fields.map((field) => (
                <Field
                  key={field}
                  field={field}
                  label={t(`fields.${field}`)}
                  before={before[field]}
                  value={draft[field]}
                  problem={problems[field]}
                  changed={changedText.includes(field)}
                  highImpact={(HIGH_IMPACT_FIELDS as readonly string[]).includes(field)}
                  highLabel={t('highImpact')}
                  wasLabel={t('was')}
                  onChange={(value) => set(field, value)}
                  sexLabel={(value: string) => t(`sexes.${value}`)}
                />
              ))}
            </div>
          </Card>
          {section.key === 'identity' ? birthCard : null}
        </Fragment>
      ))}

      <Card>
        <div className="app-correct__why">
          <label htmlFor="correction-reason">{t('reason')}</label>
          <textarea
            id="correction-reason"
            data-testid="correction-reason"
            className="app-correct__reason"
            rows={3}
            value={reason}
            onChange={(event) => setReason(event.target.value)}
            placeholder={t('reasonPlaceholder')}
            aria-describedby="correction-reason-hint"
          />
          <p id="correction-reason-hint" className="app-correct__hint">
            {t('reasonHint', { min: REASON_MIN })}
          </p>

          {high ? (
            <AlertBanner tone="borderline" title={t('highImpactTitle')}>
              {t('highImpactBody')}
            </AlertBanner>
          ) : null}

          {refusal ? <AlertBanner tone="critical" title={refusal} /> : null}

          <p className="app-correct__count" data-testid="correction-count">
            {changed.length === 0
              ? t('nothingChanged')
              : t('willChange', { count: changed.length })}
          </p>

          <Button
            variant="primary"
            onClick={submit}
            disabled={!ready}
            data-testid="correction-save"
          >
            {busy ? t('saving') : t('save')}
          </Button>
          <p className="app-correct__hint">{t('neverOverwritten')}</p>
        </div>
      </Card>
    </section>
  );
}

function Field({
  field,
  label,
  before,
  value,
  problem,
  changed,
  highImpact,
  highLabel,
  wasLabel,
  onChange,
  sexLabel,
}: {
  field: TextField;
  label: string;
  before: string;
  value: string;
  problem?: string;
  changed: boolean;
  highImpact: boolean;
  highLabel: string;
  wasLabel: string;
  onChange: (value: string) => void;
  sexLabel: (value: string) => string;
}) {
  const id = `correct-${field}`;
  return (
    <div className="app-correct__field" data-changed={changed || undefined}>
      <label htmlFor={id}>
        {label}
        {highImpact ? <Badge tone="info">{highLabel}</Badge> : null}
      </label>

      {field === 'sex' ? (
        <select id={id} data-testid={id} value={value} onChange={(e) => onChange(e.target.value)}>
          {SEXES.map((option) => (
            <option key={option} value={option}>
              {sexLabel(option)}
            </option>
          ))}
        </select>
      ) : (
        <input
          id={id}
          data-testid={id}
          type="text"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          aria-invalid={problem ? true : undefined}
        />
      )}

      {/* The value on file stays on screen next to the box. A correction screen that
          replaces the old value with an editable copy of it gives the operator nothing to
          check their typing against. */}
      {changed ? (
        <p className="app-correct__was" data-testid={`${id}-was`}>
          {wasLabel} <span>{before || '—'}</span>
        </p>
      ) : null}
      {problem ? <p className="app-correct__problem">{problem}</p> : null}
    </div>
  );
}

function Applied({ applied, onAgain }: { applied: CorrectionApplied; onAgain: () => void }) {
  const t = useTranslations('patients.correct');
  return (
    <section className="app-correct" aria-label={t('doneTitle')} data-testid="correction-done">
      <AlertBanner tone="normal" title={t('doneTitle')}>
        {t('doneBody', { count: applied.changes.length })}
      </AlertBanner>
      <Card>
        <ul className="app-correct__changes">
          {applied.changes.map((change) => (
            <li key={change.field}>
              <strong>{t(`fields.${change.field}`)}</strong>: {change.previous || '—'} →{' '}
              {change.current || '—'}
            </li>
          ))}
        </ul>
        {applied.invalidated.length > 0 ? (
          <>
            {/* Named, not summarised. "Some derived values were updated" is not something a
                clinician can check; "the anonymised research row was rebuilt" is. */}
            <h3 className="app-correct__section">{t('invalidatedTitle')}</h3>
            <ul className="app-correct__invalidated" data-testid="correction-invalidated">
              {applied.invalidated.map((item) => (
                <li key={`${item.derived_name}:${item.depends_on}`}>
                  <Badge tone={item.action === 'review' ? 'info' : 'neutral'}>
                    {t(`actions.${item.action}`)}
                  </Badge>{' '}
                  <code>{item.derived_name}</code> — {item.description}
                </li>
              ))}
            </ul>
          </>
        ) : null}
        <Button variant="secondary" onClick={onAgain}>
          {t('again')}
        </Button>
      </Card>
    </section>
  );
}
