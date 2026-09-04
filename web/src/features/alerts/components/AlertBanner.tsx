'use client';

import { useQuery } from '@tanstack/react-query';
import Link from 'next/link';
import { useLocale, useTranslations } from 'next-intl';
import { useEffect, useMemo, useState } from 'react';

import { queryKeys } from '@dthcms/api-client';
import { StatusPill } from '@dthcms/ui';

import { useRealtimeTopics } from '@/features/realtime';
import type { Locale } from '@/lib/i18n/config';

import { byUrgency, hasEscalated, listPatientAlerts, stillOpen } from '../api/alerts';

import {
  breachStatus,
  displayName,
  formatValue,
  limitKey,
  minutesSince,
  severityKey,
  unitText,
} from './alertText';

/**
 * The strip a patient screen carries when something about this patient is unanswered (CP50).
 *
 * # Why it is a strip and not a card
 *
 * Whoever opened this record came here to do something else — read a history, write a
 * prescription, explain a diet. The strip has to be impossible to walk past and small enough
 * that it does not push the work off the screen, because a banner that costs a scroll is a
 * banner people learn to collapse.
 *
 * # Why it disappears when there is nothing
 *
 * A permanent "no critical values" band on every patient screen is furniture, and furniture
 * is what the eye stops seeing. The one thing that must never be silent is *failure*: if
 * this patient's alerts could not be read, the strip says so, because an empty screen and an
 * unreadable one look identical and mean opposite things.
 *
 * # Why the socket is used here and not on the board
 *
 * This surface knows exactly one patient, so it can subscribe to that patient's topic and be
 * refreshed the instant `critical_value.raised` arrives. The query still owns the data —
 * the message invalidates, it never writes — so what lands on screen is what the endpoint
 * returned, through the same redaction as every other read.
 */

/** Recomputes "raised 4 min ago". Minutes, so a minute's granularity is plenty. */
const TICK_MS = 30_000;

export function AlertBanner({ patientId }: { patientId: string }) {
  const t = useTranslations('alerts');
  const locale = useLocale() as Locale;

  const alerts = useQuery({
    // Under the patient's own key, so a `critical_value.raised` on that patient's topic
    // invalidates this strip without anything here knowing about message kinds.
    queryKey: [...queryKeys.patient(patientId), 'alerts'],
    queryFn: () => listPatientAlerts(patientId),
  });

  useRealtimeTopics(useMemo(() => [`patient:${patientId}`], [patientId]));

  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), TICK_MS);
    return () => clearInterval(timer);
  }, []);

  if (alerts.isError) {
    // Deliberately not silent. "This patient has no critical values" and "this screen cannot
    // see whether they do" are different sentences, and only one of them is safe to imply.
    return (
      <p className="app-alert-strip__unavailable" role="status">
        {t('patientUnavailable')}
      </p>
    );
  }

  const open = byUrgency((alerts.data ?? []).filter(stillOpen));
  if (open.length === 0) return null;

  return (
    <section className="app-alert-strip" role="alert" aria-label={t('stripLabel')}>
      <p className="app-alert-strip__lede">{t('stripCount', { count: open.length })}</p>

      <ul className="app-alert-strip__list">
        {open.map((alert) => (
          <li
            key={alert.id}
            className="app-alert-strip__item"
            data-breached={alert.breached}
            data-escalated={hasEscalated(alert)}
            data-testid={`alert-strip-${alert.id}`}
          >
            {/* Word, icon, then hue — in that order, and never the hue alone. */}
            <span className="app-alert-strip__flag">{t(severityKey(alert))}</span>
            <StatusPill status={breachStatus(alert)} size="sm" />
            <span className="app-alert-strip__code">{displayName(alert, locale)}</span>
            <span className="app-alert-strip__value">
              {formatValue(alert.value, locale)}
              {unitText(alert.unit, locale) === '' ? '' : ` ${unitText(alert.unit, locale)}`}
            </span>
            <span className="app-alert-strip__limit">
              {t(limitKey(alert), {
                threshold: formatValue(alert.threshold, locale),
                unit: unitText(alert.unit, locale),
              })}
            </span>
            <span className="app-alert-strip__age">
              {t('raisedAgo', { minutes: minutesSince(alert.raised_at, now) })}
            </span>
            {hasEscalated(alert) && (
              <span className="app-alert-strip__escalated">
                {t('escalatedTo', { step: alert.escalation_step })}
              </span>
            )}
            {!alert.delivered && (
              <span className="app-alert-strip__undelivered">{t('notDelivered')}</span>
            )}
          </li>
        ))}
      </ul>

      {/* The strip shows; the board takes. Acknowledging is one act with one note, and
          offering it in two places is how the same alert gets two half-answers. */}
      <Link className="app-link" href="/alerts">
        {t('openBoard')}
      </Link>
    </section>
  );
}
