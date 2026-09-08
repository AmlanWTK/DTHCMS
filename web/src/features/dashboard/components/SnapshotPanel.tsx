'use client';

import Link from 'next/link';
import { useLocale, useTranslations } from 'next-intl';

import { Badge, Icon } from '@dthcms/ui';

import {
  ValueWithAttribution,
  alertAttribution,
  historyItemAttribution,
  observationAttribution,
} from '@/features/attribution';
import { PercentileCard } from '@/features/growth';
import { DualUnitValue } from '@/features/observations';
import { formatDateTime } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';
import { patientSubroutePath } from '@/lib/navigation';

import { omissionFor, type PhysicianDashboard } from '../api/dashboard';

import { Sparkline } from './Sparkline';
import { StatusWord } from './StatusWord';
import { bmiClassLabel, codeLabel } from './dashboardText';
import { WithheldNote } from './WithheldNote';

/**
 * §8's left panel: the patient snapshot (CP73).
 *
 * *"Demographics, current vitals, BMI, sparklines of the last 5 HbA1c values, active
 * diagnoses, critical alerts, paediatric percentile card where applicable, and the counseling
 * checklist status."* Seven cards, in that order, and the order is the argument.
 *
 * # Why the order is what it is
 *
 * A physician has about four seconds with this column before the patient starts talking.
 * What goes first is what changes what happens next:
 *
 *   1. **Critical alerts**, if there are any. §4.4 says a critical finding bypasses the queue
 *      straight to the consultant's dashboard, and a panel that put an unanswered saturation
 *      of 88% below the demographics would be the queue, in a different shape.
 *   2. **Current values with the BMI**, because that is what the consultation is about.
 *   3. **The trends**, because a number without its direction is half a fact.
 *   4. **The growth card**, when there is a child.
 *   5. **Active conditions.**
 *   6. **The counselling checkpoint**, last, because it is about the visit rather than about
 *      the patient — and because it is the one card that is a *procedural* fact.
 *
 * The allergy strip is not in this list because it is not in this panel: it belongs to the
 * patient header, above all three columns, where CP54 put it and where it stays visible while
 * this column scrolls.
 *
 * # Nothing here is drawn without attribution
 *
 * Every value in this panel goes through `ValueWithAttribution`, including the ones inside
 * the sparklines and the ones on the alert cards. That is criterion 5, and it is why the
 * payload carries whole observations rather than formatted numbers.
 *
 * # A card that was withheld says so
 *
 * `omitted` names the panels this caller was not given, and each of those draws a
 * `WithheldNote` rather than nothing. An absent card and a card reading "none" mean opposite
 * things, and the version that looks reassuring is the wrong one.
 */

export interface SnapshotPanelProps {
  view: PhysicianDashboard;
}

export function SnapshotPanel({ view }: SnapshotPanelProps) {
  const t = useTranslations('dashboard');
  const tTrend = useTranslations('dashboard.trend');
  const locale = useLocale() as Locale;

  const conditionsWithheld = omissionFor(view, 'active_conditions');
  const vitalsWithheld = omissionFor(view, 'vitals');
  const alertsWithheld = omissionFor(view, 'critical_alerts');
  const counselingWithheld = omissionFor(view, 'counseling');

  /*
   * Every other omission, drawn at the foot of the column.
   *
   * This exists because the first version of this panel did not have it, and a test caught
   * what that costs: the server names a withheld *trend* as `trends.bp_systolic`, and a panel
   * that only knew about the five omissions it was written for **dropped it silently**. The
   * whole argument for the `omitted` list is that an absence is said out loud, and a client
   * that says it only for the panels it recognises has kept the shape and lost the property.
   *
   * The centre and right panels claim `summary` for themselves, so it is excluded here rather
   * than drawn twice.
   */
  const claimed = new Set([
    'active_conditions',
    'vitals',
    'critical_alerts',
    'counseling',
    'summary',
  ]);
  const otherOmissions = view.omitted.filter((entry) => !claimed.has(entry.panel));

  const openAlerts = view.critical_alerts.filter((alert) => alert.status !== 'ACKNOWLEDGED');

  return (
    <div className="dash-panel dash-panel--snapshot" data-testid="snapshot-panel">
      <h2 className="dash-panel__title" id="dash-snapshot-heading">
        {t('snapshot.title')}
      </h2>

      {/* 1. Critical values. */}
      {alertsWithheld ? (
        <WithheldNote omission={alertsWithheld} />
      ) : (
        openAlerts.length > 0 && (
          <section
            className="dash-card dash-card--critical"
            data-testid="snapshot-alerts"
            // Assertive, and the only assertive region in this column. A screen that
            // interrupted for every card would be a screen whose interruptions mean nothing.
            role="alert"
          >
            <h3 className="dash-card__title">
              <Icon name="siren" aria-hidden /> {t('snapshot.alerts', { count: openAlerts.length })}
            </h3>
            <ul className="dash-card__list">
              {openAlerts.map((alert) => (
                <li key={alert.id}>
                  <ValueWithAttribution
                    attribution={alertAttribution(alert)}
                    label={alert.display_en ?? alert.code}
                    variant="compact"
                    testId={`snapshot-alert-${alert.id}`}
                  >
                    <span className="dash-alert">
                      <strong>
                        {locale === 'bn'
                          ? (alert.display_bn ?? alert.code)
                          : (alert.display_en ?? alert.code)}
                      </strong>
                      <DualUnitValue
                        value={alert.value}
                        unit={alert.unit ?? ''}
                        code={alert.code}
                        size="sm"
                      />
                      <span className="dash-alert__breach">
                        {t(`snapshot.breach.${alert.breached}`, { threshold: alert.threshold })}
                      </span>
                    </span>
                  </ValueWithAttribution>
                </li>
              ))}
            </ul>
            <Link className="app-link" href="/alerts">
              {t('snapshot.openAlerts')}
            </Link>
          </section>
        )
      )}

      {/* 2. Current values and the BMI. */}
      {vitalsWithheld ? (
        <WithheldNote omission={vitalsWithheld} />
      ) : (
        <section className="dash-card" data-testid="snapshot-vitals">
          <h3 className="dash-card__title">{t('snapshot.vitals')}</h3>
          {view.vitals.length === 0 ? (
            <p className="dash-card__empty">{t('snapshot.noVitals')}</p>
          ) : (
            <ul className="dash-values">
              {/*
                Every code, not the first ten. The payload is already one row per code — the
                server collapses a six-year record to the newest value of each — so the length
                of this list is a property of the registry rather than of the patient, and a
                slice here would drop a measurement without saying so. Silently showing four
                fewer values than exist is the failure this whole screen is arranged against.

                The BMI is excluded because it has its own card below, with its band and its
                scale. Drawing it twice is clutter on the densest column in the application.
              */}
              {view.vitals
                .filter((observation) => observation.code !== 'BMI' || !view.body_mass)
                .map((observation) => (
                  <li key={observation.id}>
                    <ValueWithAttribution
                      attribution={observationAttribution(observation)}
                      label={codeLabel(observation.code, locale, tTrend)}
                      testId={`snapshot-value-${observation.code}`}
                    >
                      <DualUnitValue
                        value={observation.value ?? null}
                        unit={observation.unit ?? ''}
                        code={observation.code}
                        label={codeLabel(observation.code, locale, tTrend)}
                        size="sm"
                      />
                    </ValueWithAttribution>
                  </li>
                ))}
            </ul>
          )}

          {view.body_mass && (
            <div className="dash-bmi" data-testid="snapshot-bmi" data-class={view.body_mass.class}>
              <ValueWithAttribution
                attribution={observationAttribution(view.body_mass.observation)}
                label={t('snapshot.bmi')}
                testId="snapshot-bmi-attribution"
              >
                <DualUnitValue
                  value={view.body_mass.observation.value ?? null}
                  unit={view.body_mass.observation.unit ?? ''}
                  code="BMI"
                  label={t('snapshot.bmi')}
                  size="md"
                />
              </ValueWithAttribution>
              {/* The band as a word, and the scale beside it. A class with no scale is a word
                  two people read differently: 24 is "normal" internationally and "overweight"
                  on the Asian cut-offs this clinic uses, and that difference decides whether
                  a patient enters a screening pathway. */}
              <StatusWord status={bmiStatus(view.body_mass.class)} testId="snapshot-bmi-class">
                {bmiClassLabel(view.body_mass.class ?? undefined, t) ?? t('bmi.unbanded')}
              </StatusWord>
              <span className="dash-bmi__scale">{t(`bmi.scale.${view.body_mass.scale}`)}</span>
            </div>
          )}
        </section>
      )}

      {/* 3. The trends. */}
      {view.trends.length > 0 && (
        <section className="dash-card" data-testid="snapshot-trends">
          <h3 className="dash-card__title">{t('snapshot.trends')}</h3>
          <div className="dash-sparks">
            {view.trends.map((trend) => (
              <Sparkline key={trend.code} trend={trend} />
            ))}
          </div>
        </section>
      )}

      {/* 4. The child. `applicable` is false for an adult, and the card says why rather than
             disappearing — a physician who expected a percentile and found nothing would go
             looking for a fault. */}
      {view.growth && view.growth.applicable && (
        <section className="dash-card" data-testid="snapshot-growth">
          <PercentileCard
            growth={view.growth}
            weightStatus={view.weight_status ?? undefined}
            compact
          />
          <Link className="app-link" href={patientSubroutePath(view.patient.id, 'growth')}>
            {t('snapshot.openGrowth')}
          </Link>
        </section>
      )}

      {/* 5. Active conditions. */}
      {conditionsWithheld ? (
        <WithheldNote omission={conditionsWithheld} />
      ) : (
        <section className="dash-card" data-testid="snapshot-conditions">
          <h3 className="dash-card__title">{t('snapshot.conditions')}</h3>
          {(view.active_conditions ?? []).length === 0 ? (
            // "Nothing coded" and "nothing wrong" are different sentences, and this is the
            // first. The coded history is what station 4 recorded; a patient with an
            // uncoded complaint has an active problem and no row here.
            <p className="dash-card__empty">{t('snapshot.noConditions')}</p>
          ) : (
            <ul className="dash-card__list">
              {(view.active_conditions ?? []).map((item) => (
                <li key={item.id}>
                  <ValueWithAttribution
                    attribution={historyItemAttribution(item)}
                    label={item.display_en || item.said || item.code || ''}
                    variant="compact"
                    testId={`snapshot-condition-${item.id}`}
                  >
                    <span className="dash-condition">
                      <strong>
                        {locale === 'bn'
                          ? item.display_bn || item.said || item.code
                          : item.display_en || item.said || item.code}
                      </strong>
                      {item.code ? (
                        <Badge tone="neutral">{item.code}</Badge>
                      ) : (
                        // Marked rather than hidden or dressed up. "Sugar since the flood" is
                        // a real problem and not a coded one, and only one of those two facts
                        // is safe to drop. `StatusPill` rather than `Badge` because an
                        // uncoded condition is a clinical fact about the record and the
                        // design system deliberately gives badges no clinical tones.
                        <StatusWord status="unknown">{t('snapshot.uncoded')}</StatusWord>
                      )}
                      {item.said && item.display_en && (
                        <span className="dash-condition__said">{item.said}</span>
                      )}
                    </span>
                  </ValueWithAttribution>
                </li>
              ))}
            </ul>
          )}
          <Link className="app-link" href={patientSubroutePath(view.patient.id, 'medical-history')}>
            {t('snapshot.openHistory')}
          </Link>
        </section>
      )}

      {/* 6. The counselling checkpoint (§5.4). */}
      {counselingWithheld ? (
        <WithheldNote omission={counselingWithheld} />
      ) : (
        view.counseling && (
          <section className="dash-card" data-testid="snapshot-counseling">
            <h3 className="dash-card__title">{t('snapshot.counseling')}</h3>
            {/* `blocked` is what the queue will do and `overridden` is why it will not, and a
                card that reduced the pair to "is the patient held" would tell a physician the
                counselling was done. CP57's own argument, on the panel that repeats it. */}
            <StatusWord
              status={counselingStatus(view.counseling)}
              testId="snapshot-counseling-status"
            >
              {t(
                view.counseling.overridden
                  ? 'snapshot.counselingOverridden'
                  : view.counseling.blocked
                    ? 'snapshot.counselingBlocked'
                    : 'snapshot.counselingClear',
                { count: view.counseling.missing.length },
              )}
            </StatusWord>
            {view.counseling.missing.length > 0 && (
              <ul className="dash-card__list dash-card__list--compact">
                {view.counseling.missing.slice(0, 4).map((item) => (
                  <li key={`${item.template_code}-${item.item_code}`}>
                    {locale === 'bn' ? item.text_bn : item.text_en}
                    <span className="dash-room">
                      {locale === 'bn' ? (item.room_bn ?? item.room) : (item.room_en ?? item.room)}
                    </span>
                  </li>
                ))}
              </ul>
            )}
            {view.counseling.override && (
              <p className="dash-card__note">
                {t('snapshot.counselingOverrideNote', {
                  at: formatDateTime(Date.parse(view.counseling.override.granted_at), locale),
                })}
              </p>
            )}
            <Link className="app-link" href={patientSubroutePath(view.patient.id, 'counseling')}>
              {t('snapshot.openCounseling')}
            </Link>
          </section>
        )
      )}

      {otherOmissions.map((omission) => (
        <WithheldNote key={omission.panel} omission={omission} />
      ))}
    </div>
  );
}

/**
 * The BMI band as a status token.
 *
 * Mapped rather than passed through, because the design system's status vocabulary is about
 * *how alarmed to be* and the band is about which side of a cut-off somebody sits. The two
 * happen to line up for four of the six bands and not for `normal`, which is `normal` here and
 * `healthy` in the growth card's own vocabulary — two words for one idea, in two modules that
 * were written a checkpoint apart, and worth one function rather than one silent mismatch.
 */
function bmiStatus(className: string | null | undefined) {
  switch (className) {
    case 'underweight':
      return 'low' as const;
    case 'normal':
      return 'normal' as const;
    case 'overweight':
      return 'borderline' as const;
    case 'obese_i':
    case 'obese_ii':
    case 'obese_iii':
      return 'high' as const;
    default:
      return 'unknown' as const;
  }
}

function counselingStatus(gate: { blocked: boolean; overridden: boolean }) {
  if (gate.overridden) return 'borderline' as const;
  return gate.blocked ? ('high' as const) : ('normal' as const);
}
