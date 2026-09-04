'use client';

import { useLocale, useTranslations } from 'next-intl';
import { useEffect, useState } from 'react';

import { AlertBanner, Badge, Card, Skeleton } from '@dthcms/ui';

import { listCorrections, type PatientCorrection } from '../api/patients';

/**
 * What has been corrected on this record, and why (CP35 criterion 4).
 *
 * One row per **field**, not per correction: an operator who fixed a name and a telephone
 * number in one gesture made two changes to the record, and somebody auditing a date of
 * birth should not have to open a correction about a postcode to find out.
 *
 * The reason is a column rather than a tooltip. It is the only part of this table that
 * explains anything, and a reason nobody reads is a reason nobody writes carefully.
 */
export function CorrectionHistory({ patientId }: { patientId: string }) {
  const t = useTranslations('patients.history');
  const locale = useLocale();
  const [rows, setRows] = useState<PatientCorrection[] | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let live = true;
    setRows(null);
    setFailed(false);
    listCorrections(patientId)
      .then((result) => {
        if (live) setRows(result);
      })
      .catch(() => {
        if (live) setFailed(true);
      });
    return () => {
      live = false;
    };
  }, [patientId]);

  if (failed) return <AlertBanner tone="critical" title={t('failed')} />;
  if (rows === null) return <Skeleton lines={5} />;

  if (rows.length === 0) {
    return (
      <Card>
        <p data-testid="history-empty">{t('none')}</p>
      </Card>
    );
  }

  return (
    <div className="app-history" data-testid="correction-history">
      <div className="app-history__scroll">
        <table>
          <caption className="app-history__caption">{t('caption', { count: rows.length })}</caption>
          <thead>
            <tr>
              <th scope="col">{t('when')}</th>
              <th scope="col">{t('field')}</th>
              <th scope="col">{t('was')}</th>
              <th scope="col">{t('now')}</th>
              <th scope="col">{t('why')}</th>
              <th scope="col">{t('who')}</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr
                key={`${row.event_id}:${row.field}`}
                data-high-impact={row.high_impact || undefined}
              >
                <td>{new Date(row.corrected_at).toLocaleString(locale)}</td>
                <td>
                  {t(`fields.${row.field}`)}
                  {row.high_impact ? <Badge tone="info">{t('highImpact')}</Badge> : null}
                </td>
                {/* The previous value stays legible. Striking it through would say it was
                    wrong; often it was right and the new one is a different kind of right —
                    a year-precision date replaced by a certificate. */}
                <td className="app-history__was">{row.previous || '—'}</td>
                <td className="app-history__now">{row.current || '—'}</td>
                <td>{row.reason}</td>
                <td>{row.corrected_by_code}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="app-history__note">{t('note')}</p>
    </div>
  );
}
