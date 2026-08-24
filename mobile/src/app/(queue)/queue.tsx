import { View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppText } from '@/components/AppText';
import { ScreenShell } from '@/components/ScreenShell';
import { theme, useTokens } from '@/lib/tokens';

/**
 * The queue — the screen a station operator lives in. Real content arrives when
 * registration exists (CP29/CP33); until then it shows its empty state, which is itself
 * worth getting right: "no patients waiting" is information, not absence.
 */
export default function QueueScreen() {
  const t = useTranslations('queue');
  const { colors } = useTokens();

  return (
    <ScreenShell titleKey="screen.queue">
      <View style={{ gap: theme.spacing['2'] }}>
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {t('subtitle')}
        </AppText>

        <View
          className="items-center justify-center"
          style={{
            backgroundColor: colors.surface.raised,
            borderRadius: theme.borderRadius.lg,
            borderWidth: 1,
            borderColor: colors.border.subtle,
            padding: theme.spacing['8'],
            gap: theme.spacing['2'],
          }}
        >
          <AppText weight="semibold">{t('empty')}</AppText>
          <AppText size="sm" style={{ color: colors.text.secondary, textAlign: 'center' }}>
            {t('emptyDetail')}
          </AppText>
        </View>
      </View>
    </ScreenShell>
  );
}
