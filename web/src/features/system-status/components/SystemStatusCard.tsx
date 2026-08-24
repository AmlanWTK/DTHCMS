'use client';

import { useLocale, useTranslations } from 'next-intl';

import { AlertBanner, Badge, Button, Card, ErrorState, Skeleton } from '@dthcms/ui';

import { formatDateTime } from '@/lib/formatters';
import { NetworkError, ApiError } from '@/lib/api';
import type { Locale } from '@/lib/i18n/config';
import { useSystemStatus } from '../api/useSystemStatus';

/**
 * The one screen in CP10 that talks to the backend.
 *
 * It exists to make the feature convention concrete — api/, model/, schemas/,
 * components/, index.ts — against something real, and to exercise the three states every
 * screen in this application will need: loading, loaded, and could-not-load with a
 * correlation ID the operator can quote.
 */
export function SystemStatusCard() {
  const t = useTranslations('systemStatus');
  const locale = useLocale() as Locale;
  const query = useSystemStatus();

  return (
    <Card
      as="section"
      header={
        <div>
          <strong>{t('title')}</strong>
          <p className="app-page__description">{t('description')}</p>
        </div>
      }
    >
      {query.isPending && <Skeleton lines={2} label={t('checking')} />}

      {query.isError && (
        <ErrorState
          title={t('unreachable')}
          correlationId={query.error instanceof ApiError ? query.error.correlationID : undefined}
          variant={query.error instanceof NetworkError ? 'offline' : 'error'}
          onRetry={() => void query.refetch()}
          retrying={query.isFetching}
        >
          {t('unreachableDetail')}
        </ErrorState>
      )}

      {query.data && (
        <div className="app-stack">
          {/*
           * Badges and a banner, not a StatusPill.
           *
           * StatusPill renders one of the seven clinical statuses, and its label comes
           * from the token that defines it. Reaching for `normal` to mean "the server is
           * up" would put a clinical vocabulary on an operational fact — and would give
           * the operator a green pill reading "Normal" next to a value it says nothing
           * about.
           */}
          <div className="app-status-row">
            <Badge tone={query.data.state === 'ready' ? 'brand' : 'neutral'}>
              {query.data.state === 'ready' ? t('ready') : t('notReady')}
            </Badge>
            <Badge tone="neutral">{query.data.service}</Badge>
            <Badge tone="neutral">{query.data.version}</Badge>
          </div>

          {query.data.state === 'live-not-ready' && (
            <AlertBanner tone="info" title={t('notReady')} />
          )}

          {Object.entries(query.data.checks).length > 0 && (
            <ul className="app-check-list">
              {Object.entries(query.data.checks).map(([name, state]) => (
                <li key={name}>{t('dependency', { name, state })}</li>
              ))}
            </ul>
          )}

          <div className="app-status-row">
            <span className="app-page__description">
              {t('lastChecked', { time: formatDateTime(query.data.checkedAt, locale) })}
            </span>
            <Button
              size="sm"
              variant="quiet"
              iconStart="refresh-cw"
              loading={query.isFetching}
              onClick={() => void query.refetch()}
            >
              {t('refresh')}
            </Button>
          </div>
        </div>
      )}
    </Card>
  );
}
