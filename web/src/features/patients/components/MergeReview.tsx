'use client';

import { useLocale, useTranslations } from 'next-intl';
import { useState } from 'react';

import { AlertBanner, Badge, Button, Card } from '@dthcms/ui';

import { StepUpCancelled, useStepUp } from '@/features/auth';

import {
  JUSTIFICATION_MIN,
  justificationAcceptable,
  mergePatients,
  type DuplicateCandidate,
  type MergeDecision,
  type Patient,
} from '../api/patients';
import { reasonText } from './reasonText';

/**
 * What the record says about how this decision was reached.
 *
 * `manual` — nobody's matcher suggested it; somebody went looking. That is a legitimate
 * and rarer path, and worth being able to find later.
 */
function decisionFor(candidate?: DuplicateCandidate): MergeDecision {
  if (!candidate) return 'manual';
  return candidate.deterministic ? 'blocked_match' : 'reviewed_match';
}

/**
 * Deciding whether two records are one person (CP30).
 *
 * Side by side, field by field, with the differences marked — because the decision is made
 * by reading two columns and asking "is this the same human being", and a screen that
 * summarises instead of showing makes that impossible.
 *
 * The asymmetry between the two actions is the point of the design. **Not merging** is a
 * single click with no reason and no confirmation: in this register two people genuinely
 * named Md Rahim, born in the same year, in the same upazila, are ordinary, and an
 * interface that makes "different people" tedious will produce wrong merges. **Merging**
 * asks which record survives, demands a justification a reviewer could act on, and then
 * asks for an authenticator code — because two clinical histories become one, and that is
 * irreversible in effect however well the decision is recorded.
 */
export function MergeReview({
  left,
  right,
  candidate,
  onMerged,
  onDifferent,
}: {
  /** The record the officer arrived from. */
  left: Patient;
  /** The candidate being compared. */
  right: Patient;
  /** The match that brought them here, if it came from the matcher. */
  candidate?: DuplicateCandidate;
  onMerged?: (survivor: Patient) => void;
  onDifferent?: () => void;
}) {
  const t = useTranslations('patients.merge');
  const locale = useLocale();
  const requestStepUp = useStepUp();

  // Which record survives. Defaults to the older registration: it has the longer history,
  // so merging into it moves less.
  const [survivorId, setSurvivorId] = useState(
    left.registered_at <= right.registered_at ? left.id : right.id,
  );
  const [justification, setJustification] = useState('');
  const [busy, setBusy] = useState(false);
  const [refusal, setRefusal] = useState<string | null>(null);

  const survivor = survivorId === left.id ? left : right;
  const losing = survivorId === left.id ? right : left;
  const ready = justificationAcceptable(justification) && !busy;

  async function merge() {
    if (!ready) return;
    setBusy(true);
    setRefusal(null);
    try {
      const token = await requestStepUp(
        'patient_merge',
        t('stepUp', { survivor: survivor.clinical_id, merged: losing.clinical_id }),
      );
      const merged = await mergePatients(token, survivor.id, {
        merged_id: losing.id,
        score: candidate?.score ?? 0,
        decision: decisionFor(candidate),
        justification: justification.trim(),
      });
      onMerged?.(merged);
    } catch (error) {
      if (!(error instanceof StepUpCancelled)) {
        setRefusal(error instanceof Error ? error.message : t('failed'));
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="app-merge" aria-label={t('title')}>
      <header className="app-merge__head">
        <h2>{t('title')}</h2>
        <p>{t('lede')}</p>
      </header>

      {candidate ? (
        <ul className="app-merge__reasons">
          {candidate.reasons.map((reason) => (
            <li key={reason.code}>{reasonText(reason, locale)}</li>
          ))}
        </ul>
      ) : null}

      <Comparison left={left} right={right} survivorId={survivorId} onChoose={setSurvivorId} />

      <Card>
        {/* The grid is a child of the Card rather than the Card itself: the Card owns its
            own display, and putting a grid on it silently does nothing. */}
        <div className="app-merge__decide">
          {/* Deliberately first and deliberately primary. Most of these are two people. */}
          <div className="app-merge__different">
            <Button variant="primary" onClick={onDifferent} disabled={busy}>
              {t('different')}
            </Button>
            <p>{t('differentHint')}</p>
          </div>

          <div className="app-merge__same">
            <h3>{t('sameTitle')}</h3>
            <p className="app-merge__survivor">
              {t('survivorLine', { survivor: survivor.clinical_id, merged: losing.clinical_id })}
            </p>
            <label htmlFor="merge-justification">{t('justification')}</label>
            <textarea
              id="merge-justification"
              className="app-merge__justification"
              rows={3}
              value={justification}
              onChange={(event) => setJustification(event.target.value)}
              placeholder={t('justificationPlaceholder')}
              aria-describedby="merge-justification-hint"
            />
            <p id="merge-justification-hint" className="app-merge__hint">
              {t('justificationHint', { min: JUSTIFICATION_MIN })}
            </p>
            {refusal ? <AlertBanner tone="critical" title={refusal} /> : null}
            <Button variant="secondary" onClick={merge} disabled={!ready}>
              {busy ? t('merging') : t('merge')}
            </Button>
            <p className="app-merge__warning">{t('irreversible')}</p>
          </div>
        </div>
      </Card>
    </section>
  );
}

/** The fields worth comparing, in the order a person reads them. */
const FIELDS = [
  'clinical_id',
  'name_en',
  'name_bn',
  'sex',
  'birth_date',
  'phone_primary',
  'district',
  'upazila',
  'registered_at',
] as const;

function valueOf(
  patient: Patient,
  field: (typeof FIELDS)[number],
  t: (key: string) => string,
): string {
  switch (field) {
    case 'birth_date':
      // The precision travels with the date, in words. "1985-01-01 (year)" and
      // "1985-01-01 (day)" are the difference between a real birthday and a placeholder,
      // and that difference is often the whole argument for or against a merge.
      return `${patient.birth.date} · ${t(`precision.${patient.birth.precision}`)}`;
    case 'sex':
      return t(`sexes.${patient.sex}`);
    case 'district':
      return patient.address.district ?? '';
    case 'upazila':
      return patient.address.upazila ?? '';
    case 'registered_at':
      return patient.registered_at.slice(0, 10);
    default:
      return String(patient[field] ?? '');
  }
}

function Comparison({
  left,
  right,
  survivorId,
  onChoose,
}: {
  left: Patient;
  right: Patient;
  survivorId: string;
  onChoose: (id: string) => void;
}) {
  const t = useTranslations('patients.merge');

  return (
    <div className="app-merge__compare">
      <table>
        <caption className="app-merge__caption">{t('compareCaption')}</caption>
        <thead>
          <tr>
            <th scope="col">{t('field')}</th>
            {[left, right].map((patient) => (
              <th
                key={patient.id}
                scope="col"
                data-survivor={patient.id === survivorId || undefined}
              >
                <label>
                  <input
                    type="radio"
                    name="survivor"
                    value={patient.id}
                    checked={patient.id === survivorId}
                    onChange={() => onChoose(patient.id)}
                  />
                  {patient.clinical_id}
                </label>
                {patient.id === survivorId ? <Badge tone="brand">{t('keeps')}</Badge> : null}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {FIELDS.map((field) => {
            const a = valueOf(left, field, t);
            const b = valueOf(right, field, t);
            // Marked, not hidden. The differences are what the decision turns on, and a
            // screen that highlights only the matches would argue for merging.
            const differs = a !== b;
            return (
              <tr key={field} data-differs={differs || undefined}>
                <th scope="row">{t(`fields.${field}`)}</th>
                <td>{a || '—'}</td>
                <td>{b || '—'}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
