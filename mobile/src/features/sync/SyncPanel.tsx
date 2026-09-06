import { useCallback, useEffect, useState } from 'react';
import { View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppButton } from '@/components/AppButton';
import { AppText } from '@/components/AppText';
import { localStore } from '@/lib/local-store';
import { theme, useTokens } from '@/lib/tokens';

import {
  checkWithClinic,
  controlFor,
  readMetrics,
  readOutbox,
  reasonKey,
  statusOf,
  type OutboxRow,
  type SyncMetrics,
} from '@/lib/sync';

/**
 * What this tablet is holding, and whether the clinic has it (CP66's metrics, §13.9).
 *
 * CP67 owns the failure ladder and the polish. What is here is the part CP66 must not ship
 * without: the operator can see the true state of their work. Three rules, and the first is the
 * one §13.9 says is discovered exactly once if it is wrong.
 *
 *  1. **"Everything is with the clinic" is only ever shown over an empty queue.** `statusOf`
 *     decides it, in a tested function, because a status computed in a component is a status
 *     nobody checks.
 *  2. **Every number is undelivered work**, not activity. "Waiting", "being sent", "needs your
 *     attention", "waiting for room at the clinic" and "held at the clinic" add up to what has not
 *     reached the record.
 *  3. **A reason is a sentence in the operator's language**, from the reason code — never the
 *     server's English prose, which is written for whoever reads the log.
 */

export interface SyncPanelProps {
  /**
   * Ask for a sync now. Wired by `SyncProvider`; absent when there is no engine yet.
   *
   * Returns the attempt, so the panel can repaint when it is over rather than at whatever point
   * the five-second poll next comes round. A button whose effect appears three seconds later is
   * a button an operator presses again.
   */
  onRetry?: () => void | Promise<unknown>;
}

export function SyncPanel({ onRetry }: SyncPanelProps) {
  const t = useTranslations('sync');
  const { colors } = useTokens();
  const [metrics, setMetrics] = useState<SyncMetrics | null>(null);
  const [attention, setAttention] = useState<OutboxRow[]>([]);
  const store = localStore();

  const read = useCallback(async () => {
    if (!store) return;
    setMetrics(await readMetrics(store));
    setAttention(await readOutbox(store, ['NEEDS_ATTENTION', 'HELD']));
  }, [store]);

  /**
   * The operator has telephoned the clinic, been told the list is clear, and would like to see
   * the work go. This offers the ceilinged entries again — and then repaints, which is the half
   * that makes it worth having: the answer, whichever way it goes, is on the screen when they
   * look up. If the clinic still has no room they get a newer "last checked" time and the same
   * sentence, which is a true thing to have learned; a button that left the screen exactly as it
   * was would teach them it does nothing.
   */
  const checkAgain = useCallback(async () => {
    if (store) await checkWithClinic(store);
    await onRetry?.();
    await read();
  }, [store, onRetry, read]);

  useEffect(() => {
    void read();
    // Polled rather than subscribed: the local database has no change feed, and a station screen
    // that refreshed only when something told it to would show a stale count after a background
    // sync — which is the one thing this screen must never do.
    const timer = setInterval(() => void read(), 5_000);
    return () => clearInterval(timer);
  }, [read]);

  if (!store || !metrics) {
    return (
      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {t('locked')}
      </AppText>
    );
  }

  const status = statusOf(metrics);
  const headline =
    status === 'synced'
      ? t('statusSynced')
      : status === 'syncing'
        ? t('statusSyncing', { count: metrics.inFlight })
        : status === 'attention'
          ? t('statusAttention', { count: metrics.needsAttention + metrics.held })
          : status === 'halted'
            ? t('statusHalted')
            : status === 'stalled'
              ? t('statusStalled', { count: metrics.awaitingTriage })
              : t('statusQueued', { count: metrics.queued + metrics.blocked });

  return (
    <View style={{ gap: theme.spacing['4'] }}>
      <View
        accessibilityLiveRegion="polite"
        style={{
          backgroundColor: colors.surface.raised,
          borderRadius: theme.borderRadius.md,
          borderWidth: 1,
          borderColor: colors.border.default,
          padding: theme.spacing['4'],
          gap: theme.spacing['1'],
        }}
      >
        <AppText weight="semibold">{headline}</AppText>
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {metrics.lastSuccessAt === null
            ? t('never')
            : t('lastSuccess', { when: new Date(metrics.lastSuccessAt).toLocaleString() })}
        </AppText>
      </View>

      <View style={{ gap: theme.spacing['1'] }}>
        <Count label={t('queuedLabel')} value={metrics.queued} />
        <Count label={t('inFlightLabel')} value={metrics.inFlight} />
        <Count label={t('blockedLabel')} value={metrics.blocked} />
        <Count label={t('awaitingTriageLabel')} value={metrics.awaitingTriage} />
        <Count label={t('attentionLabel')} value={metrics.needsAttention} />
        <Count label={t('heldLabel')} value={metrics.held} />
      </View>

      {metrics.skew.level !== 'fine' && metrics.skew.level !== 'unknown' ? (
        <View style={{ gap: theme.spacing['1'] }}>
          <AppText size="sm">
            {metrics.skew.ahead
              ? t('clockAhead', { minutes: metrics.skew.minutes })
              : t('clockBehind', { minutes: metrics.skew.minutes })}
          </AppText>
          {metrics.skew.entriesWouldBeHeld ? (
            <AppText size="sm" style={{ color: colors.text.secondary }}>
              {t('clockHeld')}
            </AppText>
          ) : null}
        </View>
      ) : null}

      {/*
        What a person can actually do about this, and where. Everything else on this screen is
        about the tablet; this one is the only state whose remedy is in another room, so the
        sentence names it. It is shown ahead of `delivering` and instead of it, because "handing
        its entries to the clinic" is exactly what this tablet has been stopped from doing — true
        of the intent and false about what is happening, which is the kind of sentence an operator
        believes once.
      */}
      {metrics.awaitingTriage > 0 ? (
        <View style={{ gap: theme.spacing['1'] }}>
          <AppText size="sm">{t('triageFull')}</AppText>
          {metrics.noRoomAt === null ? null : (
            <AppText size="sm" style={{ color: colors.text.secondary }}>
              {t('lastChecked', { when: new Date(metrics.noRoomAt).toLocaleString() })}
            </AppText>
          )}
        </View>
      ) : metrics.delivering ? (
        <AppText size="sm">{t('delivering', { count: metrics.queued + metrics.inFlight })}</AppText>
      ) : null}

      {metrics.halted !== '' ? (
        <AppText size="sm">
          {metrics.halted === 'SESSION_LOST'
            ? t('haltSessionLost')
            : metrics.halted === 'STORAGE_FULL'
              ? t('haltStorageFull')
              : metrics.halted === 'DEVICE_MISMATCH'
                ? t('haltDeviceMismatch')
                : t('haltDeviceRefused', { count: metrics.undelivered })}
        </AppText>
      ) : null}

      {attention.length > 0 ? (
        <View style={{ gap: theme.spacing['2'] }}>
          <AppText weight="semibold">{t('needsYou')}</AppText>
          {attention.map((row) => (
            <View key={row.eventId} style={{ gap: theme.spacing['1'] }}>
              <AppText size="sm">{t(reasonKey(row.reasonCode ?? '') as never)}</AppText>
              <AppText size="xs" style={{ color: colors.text.muted }}>
                {new Date(row.occurredAt).toLocaleString()}
              </AppText>
            </View>
          ))}
        </View>
      ) : null}

      {/*
        One button, and the more specific one wins. "Check with the clinic again" does everything
        "try again now" does and then some, so showing both would be two controls for one action
        with the vaguer of the two on top — and the operator picking the vague one would watch it
        leave the stalled entries exactly where they were.
      */}
      {onRetry ? (
        controlFor(metrics) === 'check-with-clinic' ? (
          <AppButton label={t('checkAgain')} onPress={() => void checkAgain()} />
        ) : (
          <AppButton label={t('retry')} onPress={() => void onRetry()} />
        )
      ) : null}
    </View>
  );
}

function Count({ label, value }: { label: string; value: number }) {
  const { colors } = useTokens();
  return (
    <View className="flex-row items-center justify-between">
      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {label}
      </AppText>
      <AppText size="sm" weight="semibold">
        {value}
      </AppText>
    </View>
  );
}
