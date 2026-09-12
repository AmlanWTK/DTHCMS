'use client';

import Link from 'next/link';
import { useLocale, useTranslations } from 'next-intl';
import { useCallback, useEffect, useState } from 'react';

import { AlertBanner, Button } from '@dthcms/ui';

import { usePermission } from '@/lib/use-permission';

import { acknowledgeAlert, listAlerts, type AdminAlert } from '../api/audit';

/** How often an administrator's console asks. Half the criterion's minute. */
export const ALERT_POLL_MS = 30_000;

/**
 * The alarm (CP22 criterion 3): what an administrator sees, wherever they are in the
 * console, when somebody breaks the glass or the chain fails verification.
 *
 * Polled rather than pushed because the realtime gateway is CP26 and a break-glass
 * cannot wait for it. Thirty seconds is well inside the minute the criterion allows and
 * costs nothing at the clinic's scale. Shown to whoever is wearing a hat that holds
 * audit.read — the server decides for that hat, so the poll would be refused otherwise.
 */
export function AdminAlerts() {
  const t = useTranslations('audit');
  const locale = useLocale();
  const mayRead = usePermission('admin.audit.view');
  const [alerts, setAlerts] = useState<AdminAlert[]>([]);
  const [busy, setBusy] = useState<string | null>(null);

  const poll = useCallback(async () => {
    try {
      setAlerts(await listAlerts());
    } catch {
      // A failed poll is not an alert; the next one is thirty seconds away.
    }
  }, []);

  useEffect(() => {
    if (!mayRead) return;
    void poll();
    const timer = setInterval(() => void poll(), ALERT_POLL_MS);
    return () => clearInterval(timer);
  }, [mayRead, poll]);

  if (!mayRead || alerts.length === 0) return null;

  async function acknowledge(id: string) {
    setBusy(id);
    try {
      await acknowledgeAlert(id);
      setAlerts((prev) => prev.filter((a) => a.id !== id));
    } catch {
      await poll();
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="app-stack" role="region" aria-label={t('alerts.region')}>
      {alerts.map((alert) => (
        <AlertBanner
          key={alert.id}
          // The severity the server set, not a constant. CP75 added the first alert kind that is
          // routine — the monthly medicine price review is a nudge, not an emergency — and a red
          // banner for it would be an administrator interrupted mid-clinic by housekeeping. An
          // alert that is always the loudest colour is one people learn to close unread, which
          // is precisely what must not happen to the two kinds below it.
          tone={alert.severity === 'high' ? 'critical' : 'borderline'}
          title={alertTitle(t, alert.kind)}
        >
          <div className="app-stack">
            <p className="app-alert__message">
              {locale === 'bn' ? alert.message_bn : alert.message_en}
            </p>
            <div className="app-actions">
              <Button
                variant="primary"
                size="sm"
                onClick={() => void acknowledge(alert.id)}
                loading={busy === alert.id}
                disabled={busy !== null}
              >
                {t('alerts.acknowledge')}
              </Button>
              <Link className="app-link" href="/admin/audit">
                {t('alerts.open')}
              </Link>
            </div>
          </div>
        </AlertBanner>
      ))}
    </div>
  );
}

/**
 * The heading for one alert kind.
 *
 * A lookup with a **neutral fallback**, replacing a ternary whose else-branch said "the audit
 * chain failed verification" for every kind that was not break-glass. That was correct while
 * there were two kinds and became a lie the moment CP75 raised a third: the monthly price-review
 * reminder rendered under the most alarming heading in the system.
 *
 * The fallback is deliberately vague rather than clever. An alert kind this build has not seen is
 * a deployment mid-flight, and the honest thing to say about it is that somebody should read the
 * message underneath — which is there, in both languages, and is written by the code that raised
 * it.
 */
function alertTitle(t: ReturnType<typeof useTranslations>, kind: string): string {
  switch (kind) {
    case 'break_glass':
      return t('alerts.kind.breakGlass');
    // `chain_broken`, not `audit.chain_broken`: the second is the kind of the *audit event*
    // written into the chain, the first is the kind of the alert raised beside it
    // (`audit/http.go`). They differ by one prefix and the old ternary hid the difference by
    // treating everything that was not break-glass as a broken chain.
    case 'chain_broken':
      return t('alerts.kind.chainBroken');
    case 'formulary.price_review_due':
      return t('alerts.kind.priceReview');
    default:
      return t('alerts.kind.unknown');
  }
}
