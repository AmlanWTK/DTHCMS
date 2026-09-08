import { View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppText } from '@/components/AppText';
import { theme, useTokens } from '@/lib/tokens';

/**
 * This value is on the tablet and not yet in the record (CP67, §13.9).
 *
 * # Why it goes on the value rather than in a corner
 *
 * The pill in the header answers "is my work safe" for the whole tablet. This answers it for the
 * number the operator is looking at, and the two questions are asked at different moments by the
 * same person. A clinical assistant comparing today's blood pressure with the last one needs to
 * know whether that last one is what the clinic has or what this tablet has — because if it is the
 * second, nobody else can see it, the physician downstairs is working without it, and a value that
 * looks like an established baseline is in fact something only this device knows.
 *
 * # A word, not a dot
 *
 * The pattern the attribution chip and the connection indicator already follow: colour and shape
 * carry nothing on their own here. "Not sent yet" is three words and it is the whole message; the
 * muted tone is decoration on top of a sentence that survives a greyscale screenshot and direct
 * sun on a tablet.
 *
 * # It says "not sent yet", never "not saved"
 *
 * The distinction is the entire offline design and getting it backwards would undo the thing this
 * app is built to promise. The measurement **is** saved — durably, transactionally, on this device
 * — and what has not happened is delivery. An operator who read this as "not saved" would retype
 * it, and a retyped measurement is a second event with a second id: two blood pressures in the
 * ledger, a minute apart, both true, neither a correction of the other.
 */
export function PendingChip({ testID }: { testID?: string }) {
  const t = useTranslations('sync');
  const { colors } = useTokens();

  return (
    <View
      testID={testID}
      accessibilityRole="text"
      accessibilityLabel={t('pendingDetail')}
      style={{
        alignSelf: 'flex-start',
        flexDirection: 'row',
        alignItems: 'center',
        gap: theme.spacing['1'],
        paddingHorizontal: theme.spacing['2'],
        paddingVertical: theme.spacing['0.5'],
        borderRadius: theme.borderRadius.full,
        borderWidth: 1,
        borderColor: colors.border.subtle,
        backgroundColor: colors.surface.raised,
      }}
    >
      <View
        style={{
          width: theme.spacing['1.5'],
          height: theme.spacing['1.5'],
          borderRadius: theme.borderRadius.full,
          borderWidth: 1,
          borderColor: colors.text.muted,
        }}
      />
      <AppText size="2xs" style={{ color: colors.text.muted }}>
        {t('pendingShort')}
      </AppText>
    </View>
  );
}
