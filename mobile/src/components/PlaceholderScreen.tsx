import { View } from 'react-native';
import { useTranslations } from 'use-intl';

import { theme, useTokens } from '@/lib/tokens';
import { AppText } from '@/components/AppText';

/**
 * A screen that does not exist yet, said out loud — naming the checkpoint that fills
 * it, so the placeholder is a status report rather than a "coming soon".
 */
export function PlaceholderScreen({
  labelKey,
  checkpoint,
}: {
  labelKey: string;
  checkpoint: string;
}) {
  const t = useTranslations();
  const { colors } = useTokens();

  const name = t(labelKey.replace(/^screen\./, 'screen.') as never);

  return (
    <View className="flex-1 items-center justify-center" style={{ gap: theme.spacing['2'] }}>
      <AppText size="lg" weight="semibold">
        {t('placeholder.title', { screen: name })}
      </AppText>
      <AppText
        size="sm"
        style={{
          color: colors.text.secondary,
          textAlign: 'center',
          paddingHorizontal: theme.spacing['6'],
        }}
      >
        {t('placeholder.body', { checkpoint })}
      </AppText>
    </View>
  );
}
