import { useMemo } from 'react';
import { ScrollView, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppButton } from '@/components/AppButton';
import { AppText } from '@/components/AppText';
import { theme, useTokens } from '@/lib/tokens';

import { called, isPriority, nextUp, waitTone, waitedLabel, waiting, type QueueRow } from './state';

/**
 * My station's queue (CP39).
 *
 * The screen an operator lives in for a morning, so it is arranged around the one action they
 * take a hundred times: **call the next patient**. That button is at the top, is the full
 * width of the screen, and names the person it will call — because an operator who has to read
 * a list to find out who they just summoned will read it wrong once a day.
 *
 * Everything else is secondary and looks it. The list below is who is waiting, in the order
 * they will be called, with the wait beside each: a station operator's only real decision is
 * whether somebody has been sitting too long, and the number is what tells them.
 *
 * Priority is a chip *with the reason on it*. A patient who jumped the queue and no
 * explanation is what makes staff distrust the queue — and once they distrust it they work
 * around it.
 */
export function StationQueue({
  stationName,
  rows,
  busy,
  onCallNext,
  onStart,
}: {
  stationName: string;
  rows: QueueRow[];
  busy?: boolean;
  onCallNext: () => void;
  onStart: (row: QueueRow) => void;
}) {
  const t = useTranslations('queue');
  const { colors, status } = useTokens();

  const next = useMemo(() => nextUp(rows), [rows]);
  const mine = useMemo(() => called(rows), [rows]);
  const queue = useMemo(() => waiting(rows), [rows]);

  const toneColour = (seconds: number) => {
    const tone = waitTone(seconds);
    return tone === 'high'
      ? status.critical
      : tone === 'borderline'
        ? status.borderline
        : status.normal;
  };

  return (
    <ScrollView
      contentContainerStyle={{ gap: theme.spacing['4'], paddingBottom: theme.spacing['8'] }}
    >
      <View style={{ gap: theme.spacing['1'] }}>
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {stationName}
        </AppText>
        <AppText size="lg" weight="semibold">
          {t('waitingCount', { count: queue.length })}
        </AppText>
      </View>

      {/* The one action, named. "Call next" alone makes an operator look down a list to find
          out who they summoned; "Call Md Rahim Uddin" does not. */}
      <AppButton
        label={
          next
            ? t('callNamed', { name: next.person?.name_bn || next.person?.name_en || '' })
            : t('callNone')
        }
        disabled={busy === true || next === null}
        onPress={onCallNext}
        testID="call-next"
      />

      {mine.length > 0 ? (
        <View style={{ gap: theme.spacing['2'] }}>
          <AppText weight="semibold">{t('calledByYou')}</AppText>
          {mine.map((row) => (
            <View
              key={row.id}
              testID={`called-${row.id}`}
              style={{
                backgroundColor: colors.brand.subtle,
                borderRadius: theme.borderRadius.lg,
                borderWidth: 1,
                borderColor: colors.brand.border,
                padding: theme.spacing['4'],
                gap: theme.spacing['2'],
              }}
            >
              <Person row={row} />
              <AppButton
                label={t('startService')}
                variant="secondary"
                onPress={() => onStart(row)}
                testID={`start-${row.id}`}
              />
            </View>
          ))}
        </View>
      ) : null}

      <View style={{ gap: theme.spacing['2'] }}>
        <AppText weight="semibold">{t('waitingHeading')}</AppText>

        {queue.length === 0 ? (
          <View
            style={{
              backgroundColor: colors.surface.raised,
              borderRadius: theme.borderRadius.lg,
              borderWidth: 1,
              borderColor: colors.border.subtle,
              padding: theme.spacing['8'],
              alignItems: 'center',
              gap: theme.spacing['2'],
            }}
          >
            {/* "No patients waiting" is information, not absence. */}
            <AppText weight="semibold">{t('empty')}</AppText>
            <AppText size="sm" style={{ color: colors.text.secondary, textAlign: 'center' }}>
              {t('emptyDetail')}
            </AppText>
          </View>
        ) : (
          queue.map((row, index) => (
            <View
              key={row.id}
              testID={`queued-${row.id}`}
              style={{
                backgroundColor: colors.surface.raised,
                borderRadius: theme.borderRadius.lg,
                borderWidth: 1,
                borderColor: isPriority(row) ? status.high.border : colors.border.subtle,
                // The rail is the priority signal that survives a monochrome screen.
                borderLeftWidth: isPriority(row) ? 4 : 1,
                borderLeftColor: isPriority(row) ? status.high.solid : colors.border.subtle,
                padding: theme.spacing['4'],
                gap: theme.spacing['2'],
              }}
            >
              <View className="flex-row items-center" style={{ gap: theme.spacing['3'] }}>
                <AppText size="lg" weight="semibold" style={{ color: colors.text.secondary }}>
                  {index + 1}
                </AppText>
                <View style={{ flex: 1 }}>
                  <Person row={row} />
                </View>
                <View style={{ alignItems: 'flex-end' }}>
                  <AppText weight="semibold" style={{ color: toneColour(row.waited_seconds).text }}>
                    {waitedLabel(row.waited_seconds)}
                  </AppText>
                  <AppText size="sm" style={{ color: colors.text.secondary }}>
                    {t('waiting')}
                  </AppText>
                </View>
              </View>

              {/* The reason travels with the chip. A patient who jumped the queue with no
                  explanation is what makes staff distrust the queue — and once they distrust
                  it they work around it. */}
              {isPriority(row) ? (
                <View
                  testID={`priority-${row.id}`}
                  style={{
                    backgroundColor: status.high.surface,
                    borderRadius: theme.borderRadius.md,
                    padding: theme.spacing['2'],
                  }}
                >
                  <AppText size="sm" weight="semibold" style={{ color: status.high.text }}>
                    {t('priority')}
                  </AppText>
                  {row.priority_reason ? (
                    <AppText size="sm" style={{ color: status.high.text }}>
                      {row.priority_reason}
                    </AppText>
                  ) : null}
                </View>
              ) : null}
            </View>
          ))
        )}
      </View>
    </ScrollView>
  );
}

function Person({ row }: { row: QueueRow }) {
  const { colors } = useTokens();
  return (
    <View style={{ gap: 2 }}>
      <AppText weight="semibold">{row.person?.name_bn || row.person?.name_en || '—'}</AppText>
      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {[row.person?.clinical_id, row.person ? `${row.person.age}` : null]
          .filter(Boolean)
          .join(' · ')}
      </AppText>
    </View>
  );
}
