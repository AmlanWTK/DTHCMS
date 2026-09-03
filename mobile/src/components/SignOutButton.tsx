import { useRouter } from 'expo-router';
import { Pressable } from 'react-native';
import { useTranslations } from 'use-intl';

import { theme, useTokens } from '@/lib/tokens';
import { AppText } from '@/components/AppText';
import { useSession } from '@/stores/session';

/**
 * Sign out, from every station screen. Renders nothing for a visitor.
 *
 * Quiet rather than prominent: on a shared tablet the common action is handing over, not
 * signing out, and CP18's device binding is where that gets its own flow.
 */
export function SignOutButton() {
  const t = useTranslations('session');
  const operator = useSession((state) => state.operator);
  const signOut = useSession((state) => state.signOut);
  const router = useRouter();
  const { colors } = useTokens();

  if (!operator) return null;

  async function handlePress() {
    await signOut();
    router.replace('/login');
  }

  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={t('signOut')}
      onPress={() => void handlePress()}
      style={{
        minHeight: theme.size.touchTarget,
        paddingHorizontal: theme.spacing['3'],
        justifyContent: 'center',
      }}
    >
      <AppText size="sm" weight="medium" style={{ color: colors.text.link }}>
        {t('signOut')}
      </AppText>
    </Pressable>
  );
}
