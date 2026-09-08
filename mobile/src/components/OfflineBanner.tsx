import { View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppText } from '@/components/AppText';
import { OFFLINE_FACTS } from '@/features/sync';
import { useConnectivity } from '@/lib/connectivity';
import { theme, useTokens } from '@/lib/tokens';

/**
 * No connection, and what that does and does not mean (CP67).
 *
 * # A banner that only says "offline" makes the operator guess
 *
 * And there are two guesses, both expensive. One is that nothing is being recorded, so they stop
 * and write the morning on paper — work that then has to be typed in by somebody who was not
 * there. The other is that everything is fine, which is true until they try to look up a patient
 * this tablet has never seen and get an empty screen with no explanation. The banner is where
 * that ambiguity is cheapest to remove, so it names the three things that still work and the one
 * that does not.
 *
 * # The order is the order the questions arrive in
 *
 * Reassurance first, because the operator is mid-measurement with a patient in front of them and
 * the question in their head is whether to keep going. Then the queue, because that is the fear.
 * Then reading, because that is what they will try next. The limitation last, stated as a fact
 * rather than as an apology: an operator who has been told plainly what will not work stops
 * pressing it, and stops reading the failure as the tablet being broken.
 *
 * # It is a statement about the device, not a diagnosis of the clinic
 *
 * The same discipline as the web shell's. This component knows that NetInfo says there is no
 * network; it does not know whether the clinic's server is up, and it does not say. The strong
 * signal — work that is not being delivered — is the sync pill, which is on the same screen and
 * is counting.
 */
export function OfflineBanner() {
  const t = useTranslations('connection');
  const { online } = useConnectivity();
  const { colors } = useTokens();

  if (online) return null;

  return (
    <View
      testID="offline-banner"
      accessibilityLiveRegion="polite"
      style={{
        backgroundColor: colors.surface.raised,
        borderRadius: theme.borderRadius.md,
        borderWidth: 1,
        borderColor: colors.border.default,
        padding: theme.spacing['4'],
        gap: theme.spacing['2'],
      }}
    >
      <AppText weight="semibold">{t('offline')}</AppText>
      {OFFLINE_FACTS.map((fact) => (
        <AppText key={fact} size="sm" style={{ color: colors.text.secondary }}>
          {t(fact as never)}
        </AppText>
      ))}
    </View>
  );
}
