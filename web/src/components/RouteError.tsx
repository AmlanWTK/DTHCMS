'use client';

import { useEffect, useMemo } from 'react';
import { useTranslations } from 'next-intl';

import { ErrorState } from '@dthcms/ui';

import { isClientMinted, newCorrelationID } from '@/lib/correlation';
import { ApiError } from '@/lib/api';

/**
 * What a route group shows when something below it throws.
 *
 * The correlation ID is the reason this component exists rather than a plain message.
 * An operator standing in front of a patient needs something to quote, and the three
 * cases are genuinely different:
 *
 *   - the error came from the API, and carries the id the server logged;
 *   - the error happened during server rendering, and Next gives us its `digest`, which
 *     is the token that appears in the server's own log;
 *   - the error happened in the browser and nothing recorded it anywhere, so the client
 *     mints an id and says plainly that it will not appear in the clinic's records.
 *
 * The third is the one usually left as "something went wrong". It is also the one where
 * the operator most needs to be told that reporting it is worthwhile.
 */
export function RouteError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  const t = useTranslations('error');

  const correlationId = useMemo(() => {
    if (error instanceof ApiError && error.correlationID) return error.correlationID;
    if (error.digest) return error.digest;
    return newCorrelationID();
  }, [error]);

  useEffect(() => {
    // Reported to the console for now. CP07's browser-side telemetry is not wired up
    // until there is an endpoint to send it to; when it is, this is the one place that
    // changes.
    // eslint-disable-next-line no-console
    console.error('[dthcms] route error', { correlationId, message: error.message });
  }, [correlationId, error]);

  return (
    <ErrorState
      title={t('title')}
      correlationId={correlationId}
      onRetry={reset}
      detail={error.message}
    >
      <p>{t('body')}</p>
      {isClientMinted(correlationId) && <p>{t('neverReachedServer')}</p>}
      <p>{t('referenceHelp')}</p>
    </ErrorState>
  );
}
