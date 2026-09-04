import { View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppText } from '@/components/AppText';
import { useRealtime } from '@/lib/realtime';
import { theme, useTokens } from '@/lib/tokens';

/**
 * Whether the screen is live (CP27 criterion 3), for the station app.
 *
 * The same three states as the web, sized for a tablet held at arm's length and read at a
 * glance between patients. `live` is the quietest — an operator working normally should
 * not be told so repeatedly — and `offline` is the only one that raises its voice, because
 * that is the one where what is on the screen may be behind.
 *
 * Colour never carries it alone: the word is beside the dot, and the dot changes shape
 * rather than only hue when the connection is gone.
 */
export function ConnectionIndicator() {
  const t = useTranslations();
  const { status: statusColors } = useTokens();
  const { status } = useRealtime();

  if (status === 'idle' || status === 'connecting') return null;

  const tone =
    status === 'live'
      ? statusColors.normal.text
      : status === 'reconnecting'
        ? statusColors.borderline.text
        : statusColors.high.text;

  return (
    <View
      className="flex-row items-center"
      style={{ gap: theme.spacing['2'] }}
      accessibilityLiveRegion="polite"
      accessibilityLabel={t(`realtime.${status}Detail` as never)}
    >
      <View
        style={{
          width: 8,
          height: 8,
          borderRadius: 4,
          backgroundColor: status === 'offline' ? 'transparent' : tone,
          borderWidth: status === 'offline' ? 2 : 0,
          borderColor: tone,
        }}
      />
      <AppText size="xs" weight={status === 'offline' ? 'bold' : 'regular'} style={{ color: tone }}>
        {t(`realtime.${status}` as never)}
      </AppText>
    </View>
  );
}
