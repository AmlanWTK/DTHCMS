import type { ReactNode } from 'react';
import { View } from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { useTranslations } from 'use-intl';

import { theme, useTokens } from '@/lib/tokens';
import { AppText } from '@/components/AppText';
import { ConnectionIndicator } from '@/components/ConnectionIndicator';
import { LanguageToggle } from '@/components/LanguageToggle';
import { OfflineBanner } from '@/components/OfflineBanner';
import { RoleSwitcher } from '@/components/RoleSwitcher';
import { SignOutButton } from '@/components/SignOutButton';

/**
 * The frame every station screen sits in: safe area, title, the language switch and the
 * connection banner. The mobile analogue of the web AppShell, sized for a hand.
 */
export function ScreenShell({ titleKey, children }: { titleKey: string; children: ReactNode }) {
  const t = useTranslations();
  const { colors } = useTokens();

  return (
    <SafeAreaView className="flex-1" style={{ backgroundColor: colors.surface.sunken }}>
      <View className="flex-1" style={{ padding: theme.spacing['4'], gap: theme.spacing['4'] }}>
        <View className="flex-row items-center justify-between">
          <View>
            <AppText size="xs" style={{ color: colors.text.muted }}>
              {t('app.name')}
            </AppText>
            <AppText size="xl" weight="bold">
              {t(titleKey as never)}
            </AppText>
          </View>
          <View className="flex-row items-center" style={{ gap: theme.spacing['2'] }}>
            <ConnectionIndicator />
            <LanguageToggle />
            <SignOutButton />
          </View>
        </View>

        {/* Always on screen, because criterion 3 asks that the active role be
            unmistakable — and it is also the only way to change hats, so an operator
            cannot be wearing one they cannot see (CP41). */}
        <RoleSwitcher />

        <OfflineBanner />

        <View className="flex-1">{children}</View>
      </View>
    </SafeAreaView>
  );
}
