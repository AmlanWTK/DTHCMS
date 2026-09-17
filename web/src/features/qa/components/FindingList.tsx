'use client';

import { useLocale, useTranslations } from 'next-intl';

import { Badge } from '@dthcms/ui';

import type { Locale } from '@/lib/i18n/config';

import type { QAFinding } from '../api/qa';

/**
 * The findings, as the officer reads them (CP83 §2).
 *
 * # Blocks above warnings, and they do not look the same
 *
 * The server orders them and this draws the order it was given. What it adds is the visual
 * difference, which matters more here than in most lists: the two severities mean *the patient
 * walks back up the corridor* and *the consultant may have had a reason*, and an officer reading
 * a flat list with a busy queue behind them will treat whatever is at the top as the important
 * one.
 *
 * # Every finding names its subject and its room
 *
 * "A renally-dosed drug with no eGFR" sends a physician looking through eight lines for the one
 * that matters. `subject_en` is what the server puts on it — the drug, the station, the
 * observation code — and the room's own name is drawn beside it rather than its code, because
 * nobody on the floor says STN_RX_EDUCATION.
 *
 * # There is no control in here
 *
 * Not a tick, not a dismiss, not a "mark as seen". The acknowledgement of a warning is part of
 * the clearance and lives on the form that records one; a per-finding control would be a second
 * way to acknowledge, which is a second thing to guard.
 */

export interface FindingListProps {
  findings: QAFinding[];
  /** Rendered under the list when there is nothing in it. */
  empty?: React.ReactNode;
}

export function FindingList({ findings, empty }: FindingListProps) {
  const t = useTranslations('qa');
  const locale = useLocale() as Locale;
  const bn = locale === 'bn';

  if (findings.length === 0) return <>{empty ?? null}</>;

  return (
    <ul className="app-stack" style={{ listStyle: 'none', margin: 0, padding: 0 }}>
      {findings.map((finding, index) => {
        const blocking = finding.severity === 'BLOCK';
        return (
          <li
            key={`${finding.rule_code}-${index}`}
            className="dthc-card dthc-card--flat dthc-card--compact"
            // The severity is carried on the element rather than only in the badge, so the
            // design system's own status colouring applies and a screen-reader user meets the
            // two groups as two groups.
            data-status={blocking ? 'critical' : 'high'}
            // The severity has to be visible before the words are read. An officer with a queue
            // behind them scans the list; two severities drawn the same way means whatever is at
            // the top gets treated as the important one.
            style={{
              padding: '0.75rem 1rem',
              borderLeft: `4px solid var(--color-status-${blocking ? 'critical' : 'high'}-border)`,
              background: `var(--color-status-${blocking ? 'critical' : 'high'}-surface)`,
            }}
          >
            <div
              style={{
                display: 'flex',
                gap: '0.75rem',
                alignItems: 'baseline',
                flexWrap: 'wrap',
              }}
            >
              <Badge tone={blocking ? 'brand' : 'neutral'}>
                {blocking ? t('severity.block') : t('severity.warn')}
              </Badge>
              <strong>{bn ? finding.title_bn : finding.title_en}</strong>
              <code style={{ opacity: 0.6, fontSize: '0.8em' }}>{finding.rule_code}</code>
            </div>

            {/* The sentence, and the codes behind it as a `title` rather than as text.
                Two checkpoints put internal codes on clinical screens because the code was the
                string to hand (ADR-0038); the server now sends the words. The codes are still
                here, because somebody debugging a rule needs them and throwing them away would
                trade one person's problem for another's — they are just not what is read. */}
            {(bn ? finding.subject_bn : finding.subject_en) && (
              <p
                style={{ margin: '0.35rem 0 0' }}
                title={finding.subject_codes?.length ? finding.subject_codes.join(', ') : undefined}
              >
                {bn ? finding.subject_bn : finding.subject_en}
              </p>
            )}

            {(bn ? finding.detail_bn : finding.detail_en) && (
              <p style={{ margin: '0.35rem 0 0', opacity: 0.75 }}>
                {bn ? finding.detail_bn : finding.detail_en}
              </p>
            )}

            {finding.bounce_station_code && (
              <p style={{ margin: '0.35rem 0 0', fontSize: '0.9em' }}>
                {t('finding.bouncesTo', {
                  station:
                    (bn ? finding.bounce_station_bn : finding.bounce_station_en) ||
                    finding.bounce_station_code,
                })}
              </p>
            )}
          </li>
        );
      })}
    </ul>
  );
}
