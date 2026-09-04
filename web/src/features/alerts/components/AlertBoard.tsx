'use client';

import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useEffect, useState } from 'react';

import { AlertBanner, Button, Card, EmptyState, Input, Skeleton, StatusPill } from '@dthcms/ui';

import { formatDateTime } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';

import {
  acknowledgeAlert,
  byUrgency,
  hasEscalated,
  listOpenAlerts,
  noteAcceptable,
  type CriticalAlert,
} from '../api/alerts';

import {
  actionText,
  breachStatus,
  displayName,
  formatValue,
  limitKey,
  minutesSince,
  severityKey,
  unitText,
} from './alertText';

/**
 * The consultant's priority surface (CP50, §4.4).
 *
 * # Why it polls
 *
 * The realtime gateway publishes `critical_value.raised` on the *patient's* topic, and this
 * board is facility-wide: it does not know which patients to subscribe to until it has
 * already read them. So the socket cannot be this screen's only source, and the contract
 * says as much — the pull is the truth. A board that went quiet after a dropped connection
 * would look exactly like a clinic with nothing wrong in it, which is the single failure
 * this whole checkpoint exists to prevent.
 *
 * # Why nothing here is only a colour
 *
 * Every row states its severity as a word on the coloured ground, not as the ground alone.
 * Roughly one man in twelve cannot rely on hue; a tablet carried to a bedside by a window
 * flattens every one of them; and a photograph of this screen — which is how a night
 * registrar actually shows a consultant what is happening — keeps the words and loses the
 * rest. The tint is the fastest signal for the people it works for, and it is never the
 * only one.
 *
 * # Why an escalated alert looks different
 *
 * `escalation_step > 1` does not mean the value got worse. It means the chain moved on
 * because **nobody answered**, and that is a different instruction to the person reading:
 * the previous person has already been asked. A board that drew both the same way would
 * hide the only fact that distinguishes a new problem from a failing one.
 *
 * # Why `delivered: false` is on the row
 *
 * It is the difference between "somebody is on their way" and "nobody knows". The alert is
 * in the ledger either way — but if it reached no live screen, the instruction to walk down
 * the corridor and tell somebody is the honest one, and it has to be visible next to the
 * value rather than in a log.
 */

/** How often the board re-reads. Well inside the two seconds a socket would give, and true. */
export const REFRESH_MS = 15_000;

/** How often "raised 4 min ago" is recomputed. Minutes, so a minute's granularity will do. */
const TICK_MS = 30_000;

/** The board's cache key. Its own, because no realtime topic invalidates it — see above. */
export const OPEN_ALERTS_KEY = ['alerts', 'open'] as const;

export function AlertBoard() {
  const t = useTranslations('alerts');
  // Station names belong to the traffic board's namespace and are the same names on every
  // screen in the building. A second copy here would drift, and two names for one room is
  // how a patient gets sent to the wrong door.
  const stationName = useTranslations('board');
  const locale = useLocale() as Locale;
  const client = useQueryClient();

  const alerts = useQuery({
    queryKey: OPEN_ALERTS_KEY,
    queryFn: () => listOpenAlerts(),
    refetchInterval: REFRESH_MS,
  });

  // The clock the ages are measured against. Held in state rather than read at render,
  // because a poll that returns an unchanged list does not re-render — and a frozen "raised
  // 1 min ago" on an alert that has been open for twenty is worse than no age at all.
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), TICK_MS);
    return () => clearInterval(timer);
  }, []);

  /** The alert whose acknowledgement form is open. One at a time; this is not a queue to work. */
  const [taking, setTaking] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
  /**
   * Somebody else got there first. Not an error — see the notice this renders.
   *
   * Wrapped rather than held as the alert itself, because the server is allowed to answer a
   * 409 with no alert attached, and a bare null would then be indistinguishable from "no
   * conflict happened" — which is how the one notice that matters goes missing on exactly
   * the path where something already went differently than expected.
   */
  const [taken, setTaken] = useState<{ alert: CriticalAlert | null } | null>(null);

  async function acknowledge(id: string, note: string) {
    setBusy(true);
    setFailed(false);
    try {
      const result = await acknowledgeAlert(id, note);
      setTaking(null);
      if (result.outcome === 'taken') setTaken({ alert: result.alert });
      await client.invalidateQueries({ queryKey: OPEN_ALERTS_KEY });
    } catch {
      // A refusal or an unreachable server. The alert has not been taken, and saying so is
      // the whole message: the clinician must either try again or go and find somebody.
      setFailed(true);
    } finally {
      setBusy(false);
    }
  }

  if (alerts.isPending) return <Skeleton height="12rem" />;
  if (alerts.isError || !alerts.data) {
    return (
      <AlertBanner tone="critical" title={t('unavailable')}>
        {t('unavailableBody')}
      </AlertBanner>
    );
  }

  const ordered = byUrgency(alerts.data);

  return (
    <section className="app-alerts" aria-label={t('title')}>
      {taken && (
        <AlertBanner
          tone="info"
          title={t('alreadyTaken')}
          onDismiss={() => setTaken(null)}
          className="app-alerts__taken"
        >
          <TakenBy alert={taken.alert} locale={locale} />
        </AlertBanner>
      )}

      {failed && (
        <AlertBanner
          tone="critical"
          title={t('acknowledgeFailed')}
          onDismiss={() => setFailed(false)}
        >
          {t('acknowledgeFailedBody')}
        </AlertBanner>
      )}

      {ordered.length === 0 ? (
        <EmptyState icon="check" title={t('empty.title')}>
          {t('empty.body')}
        </EmptyState>
      ) : (
        <ol className="app-alerts__list">
          {ordered.map((alert) => (
            <li
              key={alert.id}
              className="app-alert-row"
              data-breached={alert.breached}
              data-escalated={hasEscalated(alert)}
              data-delivered={alert.delivered}
              data-testid={`alert-${alert.id}`}
            >
              <div className="app-alert-row__head">
                {/* The word first, on the coloured ground rather than instead of it. */}
                <span className="app-alert-row__flag">{t(severityKey(alert))}</span>
                <StatusPill status={breachStatus(alert)} size="sm" />
                <h3 className="app-alert-row__code">{displayName(alert, locale)}</h3>
              </div>

              <p className="app-alert-row__value">
                <span className="app-alert-row__number">{formatValue(alert.value, locale)}</span>
                {unitText(alert.unit, locale) && (
                  <span className="app-alert-row__unit">{unitText(alert.unit, locale)}</span>
                )}
                <span className="app-alert-row__limit">
                  {t(limitKey(alert), {
                    threshold: formatValue(alert.threshold, locale),
                    unit: unitText(alert.unit, locale),
                  })}
                </span>
              </p>

              {actionText(alert, locale) && (
                <p className="app-alert-row__action">{actionText(alert, locale)}</p>
              )}

              <p className="app-alert-row__where">
                {alert.station_code && <span>{stationName(`station.${alert.station_code}`)}</span>}
                <span>{t('raisedAgo', { minutes: minutesSince(alert.raised_at, now) })}</span>
              </p>

              {hasEscalated(alert) && (
                <p className="app-alert-row__escalated">
                  {t('escalatedTo', { step: alert.escalation_step })}
                </p>
              )}

              {/* Not a warning about the software. A statement about the room: no screen
                  received this, so nobody has been told by the system. */}
              {!alert.delivered && (
                <p className="app-alert-row__undelivered">{t('notDelivered')}</p>
              )}

              {taking === alert.id ? (
                <AcknowledgeForm
                  busy={busy}
                  onCancel={() => setTaking(null)}
                  onConfirm={(note) => void acknowledge(alert.id, note)}
                />
              ) : (
                <div className="app-alert-row__actions">
                  <Button
                    variant="primary"
                    disabled={busy}
                    // Named, because a board with four rows has four buttons reading
                    // "Acknowledge" and a screen reader hears them as four identical
                    // controls. The visible word is kept inside the spoken name rather
                    // than replaced by it.
                    aria-label={t('acknowledgeNamed', { name: displayName(alert, locale) })}
                    onClick={() => {
                      setTaken(null);
                      setFailed(false);
                      setTaking(alert.id);
                    }}
                  >
                    {t('acknowledge')}
                  </Button>
                </div>
              )}
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}

/**
 * Who has it, from the alert the server attached to its 409.
 *
 * The contract carries an account id rather than a name, so the useful half of this notice
 * is what the other clinician *said they were doing* — which is exactly why the note is
 * mandatory. The id is shown anyway, quietly, because it is the thing an operator can quote
 * down a corridor or into a phone.
 */
function TakenBy({ alert, locale }: { alert: CriticalAlert | null; locale: Locale }) {
  const t = useTranslations('alerts');

  if (!alert) return <p>{t('alreadyTakenUnknown')}</p>;

  return (
    <div className="app-alerts__taken-detail">
      {alert.acknowledged_at && (
        <p>{t('takenAt', { at: formatDateTime(Date.parse(alert.acknowledged_at), locale) })}</p>
      )}
      {alert.acknowledgement && (
        <p className="app-alerts__taken-note">{t('theySaid', { note: alert.acknowledgement })}</p>
      )}
      {alert.acknowledged_by && (
        <p className="app-alerts__taken-who">{t('takenBy', { who: alert.acknowledged_by })}</p>
      )}
    </div>
  );
}

/**
 * The acknowledgement, which is a sentence and not a button.
 *
 * "Seen" is not an acknowledgement; "giving oral glucose, rechecking in 15" is. The next
 * person to open this record reads this instead of asking, and they read it at the moment
 * when nobody has time to say it twice — so the field's description says so, rather than
 * leaving the operator to guess how much to write.
 *
 * The form opens under the alert it belongs to. A modal would hide the value the note is
 * about, and the number is the thing the sentence has to answer.
 */
function AcknowledgeForm({
  busy,
  onCancel,
  onConfirm,
}: {
  busy: boolean;
  onCancel: () => void;
  onConfirm: (note: string) => void;
}) {
  const t = useTranslations('alerts');
  const [note, setNote] = useState('');
  const ready = noteAcceptable(note) && !busy;

  return (
    <Card elevation="raised" className="app-alert-row__form" compact>
      <Input
        label={t('note')}
        name="note"
        value={note}
        onChange={(event) => setNote(event.target.value)}
        description={t('noteDescription')}
        placeholder={t('notePlaceholder')}
        disabled={busy}
        autoFocus
        required
      />
      <div className="app-alert-row__form-actions">
        <Button variant="quiet" onClick={onCancel} disabled={busy}>
          {t('cancel')}
        </Button>
        <Button
          variant="primary"
          loading={busy}
          disabled={!ready}
          onClick={() => onConfirm(note.trim())}
        >
          {t('confirmAcknowledge')}
        </Button>
      </div>
    </Card>
  );
}
