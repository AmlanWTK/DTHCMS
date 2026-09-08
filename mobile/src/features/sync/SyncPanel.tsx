import { useRouter } from 'expo-router';
import { useCallback, useEffect, useState } from 'react';
import { View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppButton } from '@/components/AppButton';
import { AppText } from '@/components/AppText';
import { localStore } from '@/lib/local-store';
import { theme, useTokens } from '@/lib/tokens';
import { useSession } from '@/stores/session';

import { useSyncMetrics } from './SyncProvider';
import { itemsOf } from './state';

import {
  ATTENTION_STATES,
  checkWithClinic,
  controlFor,
  readOutbox,
  statusOf,
  type OutboxRow,
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
 *
 * CP67 has since taken the ladder itself to `/sync-items`, and this stayed the summary. The two
 * are one question asked twice — *is my work safe*, then *which entries and what do I do* — and
 * splitting them is what keeps a normal morning's queue from scrolling past forty rows of nothing
 * wrong. What CP67 changed here is small and deliberate: the metrics come from the shared reader
 * the header pill uses, so the two cannot disagree; the escalated count has a line of its own; and
 * the refusal list is now a link to the screen that can actually do something about them, because
 * a list an operator can only read is where CP66 stopped.
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
  const router = useRouter();
  const { colors } = useTokens();
  const permissions = useSession((state) => state.operator?.permissions);
  // The same numbers the pill in the header is drawing, from the same read. Two components polling
  // the same tables on two timers would show two different counts for a second or two after every
  // sync, on the same screen, which is how an operator learns the indicator is unreliable.
  const { metrics, refresh } = useSyncMetrics();
  const [attention, setAttention] = useState<OutboxRow[]>([]);
  const store = localStore();

  const read = useCallback(async () => {
    if (!store) return;
    await refresh();
    setAttention(await readOutbox(store, [...ATTENTION_STATES]));
  }, [store, refresh]);

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
              : status === 'escalated'
                ? t('statusEscalated', { count: metrics.escalated })
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
        <Count label={t('escalatedLabel')} value={metrics.escalated} />
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
          {/*
            A summary of the refusals, not the workflow. Three lines say what happened; the button
            below goes where something can be done about them. Putting the correction fields here
            would put a clinical value editor on the screen an operator opens to reassure
            themselves, which is the one screen it must not be on.

            The reason arrives through `itemsOf`, which routes it via `reasonFor` — the function
            that decides whether this reader may also see the clinic's own prose. On a station
            tablet the answer is almost always no, and the local sentence is the whole answer.
          */}
          {itemsOf(attention, { permissions }).needsYou.map((item) => (
            <View key={item.eventId} style={{ gap: theme.spacing['1'] }}>
              <AppText size="sm">{t(item.entryKey as never)}</AppText>
              <AppText size="sm" style={{ color: colors.text.secondary }}>
                {item.reason === null ? '' : t(item.reason.key as never)}
              </AppText>
              <AppText size="xs" style={{ color: colors.text.muted }}>
                {new Date(item.occurredAt).toLocaleString()}
              </AppText>
            </View>
          ))}
        </View>
      ) : null}

      {/*
        Always offered, not only when something is wrong.

        An operator who has never opened this list on a good morning will not find it on a bad
        one, and the honest answer to "is my work safe" for somebody who wants to check is a list of
        what is on the tablet — including, on most days, nothing. It is the secondary control:
        below the one thing the screen is asking them to do, above nothing.
      */}
      <AppButton
        testID="open-sync-items"
        label={t('openItems')}
        variant="secondary"
        onPress={() => router.push('/sync-items')}
      />

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
