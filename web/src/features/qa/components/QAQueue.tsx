'use client';

import { useQuery } from '@tanstack/react-query';
import Link from 'next/link';
import { useLocale, useTranslations } from 'next-intl';

import { AlertBanner, Badge, Card, EmptyState } from '@dthcms/ui';

import type { Locale } from '@/lib/i18n/config';

import { QA_QUEUE_KEY, readQAQueue } from '../api/qa';

/**
 * The line at station 10 (CP83).
 *
 * # Oldest first, and the bounce count is on the row
 *
 * A queue is a line. A screen that put the newest submission at the top would leave the patient
 * who has been waiting longest at the bottom of it, which is how a busy Thursday turns into
 * somebody sitting for an hour.
 *
 * `bounce_count` is drawn because a sheet on its third visit to this desk is a different
 * conversation from one on its first — and because a rising count across the morning is the
 * clinic telling the officer something about the consultation room rather than about the patient.
 */
export function QAQueue() {
  const t = useTranslations('qa');
  const locale = useLocale() as Locale;
  const bn = locale === 'bn';

  const queue = useQuery({ queryKey: QA_QUEUE_KEY, queryFn: readQAQueue });

  if (queue.isPending) return <p>{t('loading')}</p>;
  if (queue.isError) return <AlertBanner tone="critical" title={t('unavailable')} />;

  const entries = queue.data.queue ?? [];
  if (entries.length === 0) {
    return <EmptyState title={t('queue.empty')}>{t('queue.emptyWhy')}</EmptyState>;
  }

  return (
    <ul className="app-stack" style={{ listStyle: 'none', margin: 0, padding: 0 }}>
      {entries.map((entry) => (
        <Card as="li" key={entry.prescription_id} compact>
          <div
            style={{
              display: 'flex',
              gap: '0.75rem',
              alignItems: 'baseline',
              flexWrap: 'wrap',
            }}
          >
            <Link href={`/qa?prescription=${entry.prescription_id}`}>
              <strong>
                {(bn ? entry.patient_name_bn : entry.patient_name_en) || entry.clinical_id}
              </strong>
            </Link>
            <code style={{ opacity: 0.7 }}>{entry.clinical_id}</code>
            <span>{t('queue.items', { count: entry.item_count })}</span>
            {entry.bounce_count > 0 && (
              <Badge tone="brand">{t('queue.bounces', { count: entry.bounce_count })}</Badge>
            )}
          </div>
        </Card>
      ))}
    </ul>
  );
}
