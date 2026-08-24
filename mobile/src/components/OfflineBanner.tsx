import { View } from 'react-native';
import { useTranslations } from 'use-intl';

import { useConnectivity } from '@/lib/connectivity';
import { theme, useTokens } from '@/lib/tokens';
import { AppText } from '@/components/AppText';

/**
 * The offline banner. Same wording discipline as the web shell: a statement about the
 * device, not a diagnosis of the server, and it promises what the architecture will
 * actually deliver — nothing entered is lost.
 */
export function OfflineBanner() {
  const t = useTranslations('connection');
  const { online } = useConnectivity();
  const { colors } = useTokens();

  if (online) return null;

  return (
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
      <AppText weight="semibold">{t('offline')}</AppText>
      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {t('offlineDetail')}
      </AppText>
    </View>
  );
}
