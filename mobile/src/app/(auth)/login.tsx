import { TextInput, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppButton } from '@/components/AppButton';
import { AppText } from '@/components/AppText';
import { ScreenShell } from '@/components/ScreenShell';
import { theme, useTokens } from '@/lib/tokens';

/**
 * The sign-in screen — a placeholder, and deliberately an inert one, exactly like the
 * web's. No handler, no state, disabled controls: a login form that looks functional
 * and silently does nothing is worse than one that says it is not built, because
 * somebody will type a real password into it.
 */
export default function LoginScreen() {
  const t = useTranslations('login');
  const tScreen = useTranslations('screen');
  const { colors } = useTokens();

  const field = {
    minHeight: theme.size.touchTarget,
    borderRadius: theme.borderRadius.md,
    borderWidth: 1,
    borderColor: colors.border.control,
    backgroundColor: colors.surface.raised,
    paddingHorizontal: theme.spacing['4'],
    color: colors.text.primary,
  };

  return (
    <ScreenShell titleKey="screen.login">
      <View style={{ gap: theme.spacing['4'] }}>
        <AppText style={{ color: colors.text.secondary }}>{t('subtitle')}</AppText>

        <View
          style={{
            backgroundColor: colors.surface.raised,
            borderRadius: theme.borderRadius.md,
            borderWidth: 1,
            borderColor: colors.border.subtle,
            padding: theme.spacing['4'],
          }}
        >
          <AppText size="sm" style={{ color: colors.text.secondary }}>
            {t('placeholderNotice')}
          </AppText>
        </View>

        <View style={{ gap: theme.spacing['1'] }}>
          <AppText size="sm" weight="medium">
            {t('username')}
          </AppText>
          <TextInput editable={false} style={field} accessibilityLabel={t('username')} />
        </View>

        <View style={{ gap: theme.spacing['1'] }}>
          <AppText size="sm" weight="medium">
            {t('password')}
          </AppText>
          <TextInput
            editable={false}
            secureTextEntry
            style={field}
            accessibilityLabel={t('password')}
          />
        </View>

        <AppButton label={tScreen('login')} disabled />
      </View>
    </ScreenShell>
  );
}
