'use client';

import { useTranslations } from 'next-intl';

import { AlertBanner } from '@dthcms/ui';

import { useRealtime } from '../RealtimeProvider';

/**
 * The banner for a screen that is no longer live.
 *
 * The indicator in the top bar is for the operator who glances; this is for the one who is
 * about to act. It appears only at `offline` — after the client has stopped expecting the
 * connection back in a moment — because a banner that flashes on every wifi blip is a
 * banner people learn to scroll past.
 *
 * What it says is deliberately narrow: the screen may be behind. It does not say anything
 * about whether writes will succeed, because they may well: the API is a different
 * connection, and a write that reaches it is recorded whether or not this socket is up.
 */
export function StaleDataBanner() {
  const t = useTranslations('realtime');
  const { status } = useRealtime();

  if (status !== 'offline') return null;

  return (
    <AlertBanner tone="info" title={t('offline')}>
      {t('offlineDetail')}
    </AlertBanner>
  );
}
