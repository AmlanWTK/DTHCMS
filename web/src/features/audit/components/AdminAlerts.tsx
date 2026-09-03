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
          tone="critical"
          title={t(`alerts.kind.${alert.kind === 'break_glass' ? 'breakGlass' : 'chainBroken'}`)}
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
