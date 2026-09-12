'use client';

import { useQuery } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';

import { Skeleton } from '@dthcms/ui';

import type { Locale } from '@/lib/i18n/config';
import { usePermission } from '@/lib/use-permission';

import { getRenalStatus, renalStatusKey, renalTone, type RenalStatus } from '../api/renal';

/**
 * The renal status a prescriber reads before deciding a dose (CP79, §7.2, §6.4).
 *
 * # Three states, and the empty one is the loudest
 *
 * `unknown` (no eGFR on file), `stale` (older than the clinic's window), `current`. The
 * ordering matters more than the colours: **"there is no eGFR" is not a milder version of
 * "the eGFR is old"**, and a prescriber who reads a blank indicator as "probably fine" is
 * the exact failure this checkpoint exists to prevent. So the absent case gets the loudest
 * tone, a heading of its own, and a sentence that says what has not been checked rather
 * than nothing at all.
 *
 * # Never colour alone
 *
 * Every state carries a word before any hue is involved — *No kidney function on file*,
 * *Out of date*, *Current* — for the reasons the allergy strip lists: a tablet held near a
 * window flattens hue, a clinic printer has none, and roughly one man in twelve cannot use
 * it. `data-tone` is on the element for tests, and the word is on the screen for people.
 *
 * # The clinical sentence comes from the server
 *
 * `summary_en` / `summary_bn` and the stage labels are written by the engine, and this
 * component renders whichever the current locale asks for. That is deliberate: the same
 * sentence is cited by the safety findings, and a second copy in a message catalogue is a
 * second copy that drifts. The catalogue here holds a heading and the word *provisional*.
 *
 * # The window says whose it is
 *
 * Six months is the plan's proposal and **nobody has approved it**. While `policy.approved`
 * is false the indicator says so in as many words, because "we are using six months and
 * nobody has agreed to it" is the true statement and a silent six months is not.
 *
 * # A stale eGFR is still shown
 *
 * With its age, loudly, and not hidden. The renal rules run against it — it is the best
 * information there is — and a component that blanked the number when it went stale would
 * leave the physician with less than he had.
 *
 * # Dialysis
 *
 * Not modelled anywhere in CP79. A patient on haemodialysis reads exactly as any other
 * patient here, which is a limitation to know about rather than a behaviour to rely on.
 */
export interface RenalIndicatorProps {
  patientId: string;
  /**
   * A status the caller already has. Given one, this component performs no I/O at all —
   * the same arrangement the allergy strip uses so that a screen with a one-request budget
   * can meet it structurally rather than by trusting a cache policy.
   */
  status?: RenalStatus;
}

export function RenalIndicator({ patientId, status: given }: RenalIndicatorProps) {
  const t = useTranslations('renal');
  const locale = useLocale() as Locale;
  // The same permission the safety check holds: the object is this patient's kidney
  // function either way.
  const mayRead = usePermission('medication.safety.check');

  const query = useQuery({
    queryKey: renalStatusKey(patientId),
    queryFn: () => getRenalStatus(patientId),
    enabled: mayRead && given === undefined,
  });

  if (!mayRead) return null;

  if (given !== undefined) return <RenalCard status={given} locale={locale} />;

  if (query.isError) {
    // Not an empty box. An indicator that failed silently reads as "checked, nothing
    // found", which is the opposite of what is true.
    return (
      <p className="app-renal__unavailable" role="status" data-testid="renal-unreadable">
        {t('unavailable')}
      </p>
    );
  }
  if (query.isPending || !query.data) return <Skeleton height="4rem" />;

  return <RenalCard status={query.data} locale={locale} />;
}

/** The card itself, given the status. Separated so the render is pure and testable. */
function RenalCard({ status, locale }: { status: RenalStatus; locale: Locale }) {
  const t = useTranslations('renal');
  const tone = renalTone(status);
  const summary = locale === 'bn' ? status.summary_bn : status.summary_en;
  const stage = locale === 'bn' ? status.stage_label_bn : status.stage_label_en;

  return (
    <section
      className="app-renal"
      // Interrupting is right when a prescriber is about to dose a renally-cleared drug
      // against nothing, and wrong for a current result.
      role={tone === 'unknown' ? 'alert' : 'status'}
      aria-live={tone === 'unknown' ? 'assertive' : 'polite'}
      aria-label={t('label')}
      data-testid="renal-indicator"
      data-tone={tone}
      data-stage={status.stage ?? ''}
      data-stale={status.stale}
      data-known={status.known}
    >
      {/* The word, before any colour. */}
      <p className="app-renal__state">{t(`state.${tone}`)}</p>

      {status.known && (
        <p className="app-renal__value">
          <span className="app-renal__number" data-testid="renal-egfr">
            {status.egfr}
          </span>{' '}
          <span className="app-renal__unit">mL/min/1.73m²</span>
          {stage ? <span className="app-renal__stage"> · {stage}</span> : null}
        </p>
      )}

      {/* Criterion 1: the value and its date, together, always. */}
      <p className="app-renal__summary" data-testid="renal-summary">
        {summary}
      </p>

      {status.known && (
        // A GFR category is not a diagnosis of chronic kidney disease. Said on the screen,
        // because a staging display read as a diagnosis is a diagnosis the software made up.
        <p className="app-renal__caveat">{t('stageIsNotADiagnosis')}</p>
      )}

      {!status.policy.approved && (
        <p className="app-renal__provisional" data-testid="renal-provisional">
          {t('provisional', { months: status.policy.recency_months })}
        </p>
      )}
    </section>
  );
}
