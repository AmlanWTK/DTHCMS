import { Link } from 'expo-router';
import { View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppText } from '@/components/AppText';
import { theme, useTokens } from '@/lib/tokens';

export default function NotFound() {
  const t = useTranslations('notFound');
  const { colors } = useTokens();

  return (
    <View className="flex-1 items-center justify-center" style={{ gap: theme.spacing['3'] }}>
      <AppText size="lg" weight="semibold">
        {t('title')}
      </AppText>
      <Link href="/queue">
        <AppText style={{ color: colors.text.link }}>{t('action')}</AppText>
      </Link>
    </View>
  );
}
