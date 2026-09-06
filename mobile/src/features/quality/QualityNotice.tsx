import { usePathname, useRouter } from 'expo-router';
import { useMemo } from 'react';
import { Pressable, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppText } from '@/components/AppText';
import { useRealtimeTopics } from '@/lib/realtime';
import { theme, useTokens } from '@/lib/tokens';
import { useSession } from '@/stores/session';

import { useMyQualityRecord } from './api';
import { myTopic, noticeOf } from './state';

/** Where the record lives. One string, so the shell, the notice and the link cannot disagree. */
export const QUALITY_HREF = '/quality';

/**
 * "There is a note on your own record" (CP63, ADR-0029).
 *
 * # Why the operator is told, and not only the supervisor
 *
 * The bridge on the server publishes a raised note to the person it is about, on their own
 * topic, at the same moment it writes the audit row — and the comment beside it says why: *a
 * flag somebody first learns about from their supervisor is exactly the ambush that produces*
 * the dishonesty the plan warns about, *and telling them at the same moment costs one message
 * and is most of the defence.* This line is where that message surfaces. It sits in the
 * application shell, so it reaches an operator wherever they are rather than only if they
 * happen to open a screen they have no reason to open.
 *
 * # Two things it can say, and neither carries a number
 *
 * *Something has been written on your own record* while a note is open, and *somebody has
 * answered the note on your own record* for a day after a supervisor closes one. The second is
 * the other half of the same defence: a note that appeared here and then silently vanished
 * when somebody decided about it would teach an operator that things are decided about them
 * out of sight.
 *
 * A day, not thirty. Long enough that somebody who was off shift when their note was answered
 * still meets it; short enough that it is not a banner people learn to scroll past — and the
 * record keeps the answered note either way, so nothing is lost when the line goes. "Now" is
 * the record's own window end, which is the server's clock rather than this tablet's: a
 * station tablet with a wrong clock would otherwise nag forever or never speak, and neither
 * failure would look like a clock.
 *
 * There is no number on either sentence. Not because a number would be wrong, but because the
 * one place a count belongs is beside its denominator, and a shell banner is not the place to
 * put both. The record's own screen is one press away and every figure on it is denominated.
 *
 * # It does not shout
 *
 * No red, no icon that means danger, no modal, no sound. The same brand tone the correction
 * notice settled on, and for the stronger version of the same reason: this is a proposal for a
 * conversation, raised on numbers no clinician has yet approved. The one thing on this tablet
 * allowed to interrupt an operator mid-measurement is a critical value in a patient.
 *
 * # It is silent when there is nothing to say
 *
 * No open note, nobody signed in, the read refused, the link down: no line. Nearly always,
 * which is the point — a control that appears when nothing has happened is a control people
 * learn to ignore, and this one has to be believed on the day it means something.
 */
export function QualityNotice() {
  const t = useTranslations('quality');
  // Two sentences, keyed by which one applies. `quality.test.ts` asserts both exist in both
  // languages, which is what a value-keyed lookup costs and what makes it safe.
  const say = t as unknown as (key: string) => string;
  const { colors } = useTokens();
  const router = useRouter();
  const pathname = usePathname();
  const me = useSession((state) => state.operator?.id ?? '');

  /*
   * The operator's own channel, subscribed for as long as they are signed in.
   *
   * Here rather than only on the record's screen, because the whole point is that the note
   * reaches somebody who is not looking at their record. The shell is mounted for the session;
   * the screen is mounted once they have already been told.
   *
   * No message listener. `realtimeInvalidations` in `@dthcms/api-client` maps every
   * `quality.*` message onto `queryKeys.quality()`, and `gapInvalidations` does the same for a
   * `user:` topic after a dropped connection — which is the ordinary way an operator would
   * otherwise learn of a pattern from somebody else first. One rule, in one place, for both
   * surfaces.
   */
  useRealtimeTopics(useMemo(() => (myTopic(me) === '' ? [] : [myTopic(me)]), [me]));

  const { data } = useMyQualityRecord(me !== '');
  const notice = noticeOf(data);

  // Not on the record's own screen: a line saying "go and look at this" above the thing being
  // looked at is a control that cannot be obeyed.
  if (notice === 'none' || pathname === QUALITY_HREF) return null;

  return (
    <Pressable
      testID="quality-notice"
      accessibilityRole="button"
      accessibilityLabel={say(`notice.${notice}`)}
      accessibilityHint={t('noticeHint')}
      onPress={() => router.push(QUALITY_HREF)}
      style={({ pressed }) => ({
        minHeight: theme.size.touchTarget,
        justifyContent: 'center',
        paddingVertical: theme.spacing['2'],
        paddingHorizontal: theme.spacing['3'],
        borderRadius: theme.borderRadius.md,
        borderWidth: theme.size.borderWidth.thin,
        borderColor: colors.brand.border,
        backgroundColor: colors.brand.subtle,
        opacity: pressed ? 0.85 : 1,
      })}
    >
      <View
        style={{
          flexDirection: 'row',
          flexWrap: 'wrap',
          alignItems: 'center',
          gap: theme.spacing['2'],
        }}
      >
        <AppText size="sm" weight="semibold" style={{ color: colors.brand.text, flexShrink: 1 }}>
          {say(`notice.${notice}`)}
        </AppText>
        {/* The affordance as a word rather than a glyph, for the reason CP62 records: an arrow
            renders differently across the OEM fonts on the tablets this clinic buys. */}
        <AppText size="2xs" weight="semibold" style={{ color: colors.text.link }}>
          {t('noticeAction')}
        </AppText>
      </View>
    </Pressable>
  );
}

/**
 * The standing way in, with or without a note.
 *
 * The line above only appears when something has been written on the record, which is nearly
 * never — and a record an operator can only reach on the day somebody raises a note about them
 * is a record they will read as something that is kept from them until it is used against
 * them. ADR-0029's whole argument for asking no permission on `/v1/quality/me` is that
 * transparency is the defence; a door that is only unlocked from the outside is not
 * transparency.
 *
 * It sits on the correction queue because that is the adjacent question — *what have I been
 * asked to look at again* and *what does my own record say* are the same subject, and an
 * operator on that screen is already thinking about it. The station app has no menu, so an
 * entry point is a deliberate placement rather than an item somebody adds to a list.
 */
export function MyRecordLink() {
  const t = useTranslations('quality');
  const { colors } = useTokens();
  const router = useRouter();
  const pathname = usePathname();

  if (pathname === QUALITY_HREF) return null;

  return (
    <Pressable
      testID="quality-my-record-link"
      accessibilityRole="button"
      accessibilityHint={t('noticeHint')}
      onPress={() => router.push(QUALITY_HREF)}
      style={({ pressed }) => ({
        minHeight: theme.size.touchTarget,
        justifyContent: 'center',
        opacity: pressed ? 0.85 : 1,
      })}
    >
      <AppText size="sm" weight="semibold" style={{ color: colors.text.link }}>
        {t('myRecord')}
      </AppText>
    </Pressable>
  );
}
