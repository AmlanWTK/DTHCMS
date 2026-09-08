import { useRouter } from 'expo-router';
import { Pressable, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppText } from '@/components/AppText';
import { pillFor, useSyncMetrics } from '@/features/sync';
import { useConnectivity } from '@/lib/connectivity';
import { theme, useTokens } from '@/lib/tokens';

/**
 * Is my work safe? — answered on every screen (CP67, §13.9).
 *
 * # Why it is in the shell and not on the sync screen
 *
 * An operator at a station does not go looking for a sync screen; they look up between patients.
 * A truthful indicator that has to be navigated to is one consulted at the end of the morning, by
 * which time a tablet that stopped delivering at nine has a morning of measurements on it and a
 * queue of people who have gone home. So it sits beside the connection indicator, in the frame
 * every screen shares, and it is a `Pressable` because the honest answer to "3 need you" is a list
 * of the three.
 *
 * # It is quiet, on purpose
 *
 * CP67's named risk is alarm fatigue, and this component is where it would happen. The tone comes
 * from `toneOf` — a tested function, not a colour chosen here — and the calm tone is drawn in the
 * ordinary secondary text of the interface with a small dot: a queue draining normally is the
 * system working, and an operator who sees an amber badge every time they save a blood pressure
 * stops seeing badges. Only `alert` gets a filled background, and only two statuses are `alert`.
 *
 * # Colour never carries it
 *
 * The same rule the connection indicator follows, for the same person: roughly one man in twelve
 * cannot rely on hue, and a station tablet is read in direct sun. The word is always there, the
 * count is always in the word, and the dot changes shape — hollow when something is wrong — as
 * well as colour.
 *
 * # What it says when there is nothing to say
 *
 * It still says it. An indicator that disappears when everything is fine cannot be distinguished
 * from one that has crashed, and "I could not see the sync thing" is not a state an operator can
 * reason about. `pillFor` always returns a sentence.
 */
export function SyncPill() {
  const t = useTranslations('sync');
  const router = useRouter();
  const { colors, status: statusColors } = useTokens();
  const { metrics } = useSyncMetrics();
  const { online } = useConnectivity();

  /*
   * `use-intl` infers a message's arguments from the *literal* key, so a key chosen at run time
   * types its values as `undefined` and the count cannot be passed. The keys here come from
   * `pillFor`, which is a closed set, and `sync-attention.test.ts` asserts every one of them
   * exists in both message files with the same ICU arguments — which is the guarantee the
   * inference was providing, obtained from a test that can also check the Bangla.
   */
  const say = t as unknown as (key: string, values?: Record<string, number>) => string;

  // Nobody is signed in, or the database has not been opened yet. There is no honest thing to say
  // about a queue this component cannot read, and a hopeful "synced" would be the worst of the
  // available lies — so it draws nothing at all rather than guessing.
  if (metrics === null) return null;

  const pill = pillFor(metrics, { online });
  const tone =
    pill.tone === 'alert'
      ? statusColors.critical
      : pill.tone === 'notice'
        ? statusColors.borderline
        : statusColors.normal;

  const alert = pill.tone === 'alert';
  const calm = pill.tone === 'calm';

  return (
    <Pressable
      testID="sync-pill"
      accessibilityRole="button"
      accessibilityHint={t('pillOpen')}
      // Read out as one sentence, and the total is in it whatever the headline says: a headline
      // about three refusals must not leave somebody unable to learn that eleven other entries are
      // also still on the tablet.
      accessibilityLabel={`${say(pill.key, { count: pill.count })}. ${
        pill.undelivered === 0 ? t('pillAllClear') : t('pillAll', { count: pill.undelivered })
      }`}
      accessibilityLiveRegion="polite"
      onPress={() => router.push('/sync')}
      style={{
        flexDirection: 'row',
        alignItems: 'center',
        gap: theme.spacing['1.5'],
        minHeight: theme.size.touchTargetCompact,
        paddingHorizontal: theme.spacing['2'],
        borderRadius: theme.borderRadius.full,
        // Calm states carry no fill and no border. They are information, not a notification, and
        // the interface behind them is the thing the operator is actually using.
        backgroundColor: calm ? 'transparent' : tone.surface,
        borderWidth: calm ? 0 : 1,
        borderColor: tone.border,
      }}
    >
      <View
        style={{
          width: theme.spacing['2'],
          height: theme.spacing['2'],
          borderRadius: theme.borderRadius.full,
          backgroundColor: alert ? 'transparent' : tone.icon,
          borderWidth: alert ? 2 : 0,
          borderColor: tone.icon,
        }}
      />
      <AppText
        size="xs"
        weight={calm ? 'regular' : 'semibold'}
        style={{ color: calm ? colors.text.secondary : tone.text }}
      >
        {say(pill.key, { count: pill.count })}
      </AppText>
    </Pressable>
  );
}
