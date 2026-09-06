import { useQueryClient } from '@tanstack/react-query';
import { usePathname, useRouter } from 'expo-router';
import { useCallback, useMemo } from 'react';
import { Pressable, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppText } from '@/components/AppText';
import { theme, useTokens } from '@/lib/tokens';
import { useRealtimeMessages, useRealtimeTopics } from '@/lib/realtime';
import { useSession } from '@/stores/session';

import { CORRECTIONS_QUERY_PREFIX, useMyCorrections } from './api';
import { myTopic, notifiesMe, queueOf } from './state';

/** Where the queue lives. One string, so the shell and the notice cannot disagree. */
export const CORRECTIONS_HREF = '/corrections';

/**
 * "Somebody has asked you to look at a value again" (CP62, §4.3, criterion 4).
 *
 * # This is the notification, and it is why the whole thing works
 *
 * Criterion 4 is that the author is told **on their device**. The server routes a flag to
 * whoever typed the value and publishes it on that person's own topic; this line is where it
 * surfaces. It sits in the application shell, so an operator meets it wherever they are — the
 * queue, their station, the middle of a set of vitals — rather than only if they happen to
 * open a screen they have no reason to open.
 *
 * # One line, a count, and no clinical value
 *
 * Nothing here names a patient, a measurement or a number, and that is deliberate twice over.
 * A banner that said "your height of 150 cm is wrong" would put a clinical value on every
 * screen in the application, including whatever is on the tablet when a colleague walks past —
 * and it would say it in the tone of an accusation. So: how many, and a way to go and look.
 *
 * # It does not shout
 *
 * No red, no icon that means danger, no modal. This is the same treatment the shell gives its
 * other quiet facts, in the brand tone rather than a status one, because a colleague asking a
 * question about a number is not an alarm — and the plan's own risk note is that a mechanism
 * which feels punitive makes staff hide errors rather than correct them. The one thing on this
 * tablet that is allowed to interrupt an operator mid-measurement is a critical value.
 *
 * # It is silent when there is nothing to say
 *
 * Nothing open, nobody signed in, the read refused, the link down: no line. An empty banner
 * that appeared and disappeared would be a control people learn to ignore. The queue's own
 * screen is where absences and failures get explained, because that is where somebody is
 * looking for an answer rather than being handed one.
 */
export function CorrectionNotice() {
  const t = useTranslations('corrections');
  const { colors } = useTokens();
  const router = useRouter();
  const pathname = usePathname();
  const queryClient = useQueryClient();
  const me = useSession((state) => state.operator?.id ?? '');

  /*
   * The operator's own channel.
   *
   * Subscribed here rather than on the corrections screen, because the point of criterion 4 is
   * that the flag reaches somebody who is not looking at their correction queue. The shell is
   * mounted for as long as they are signed in; the screen is mounted when they have already
   * been told.
   */
  useRealtimeTopics(useMemo(() => (myTopic(me) === '' ? [] : [myTopic(me)]), [me]));

  /*
   * A flag arriving means "read the queue again", and nothing more.
   *
   * The gateway's summary carries the request id, the observation id, the code and the reason
   * code — deliberately not the value — so there is nothing here to write into the cache even
   * if this application were willing to, which it is not. The shared invalidation map in
   * `@dthcms/api-client` has no entry for this kind yet: a `user:` topic turns into the
   * audit-alerts key, so without this the queue would sit stale on the one device the request
   * was routed to.
   */
  useRealtimeMessages(
    useCallback(
      (message: { kind?: string; topic?: string }) => {
        if (!notifiesMe(message, me)) return;
        void queryClient.invalidateQueries({ queryKey: CORRECTIONS_QUERY_PREFIX });
      },
      [me, queryClient],
    ),
  );

  const { data } = useMyCorrections(me !== '', false);
  const waiting = queueOf(data).open.length;

  // Not on the queue's own screen: a banner saying "go and look at this" above the thing being
  // looked at is a control that cannot be obeyed.
  if (waiting === 0 || pathname === CORRECTIONS_HREF) return null;

  return (
    <Pressable
      testID="correction-notice"
      accessibilityRole="button"
      accessibilityLabel={t('notice', { count: waiting })}
      accessibilityHint={t('noticeHint')}
      onPress={() => router.push(CORRECTIONS_HREF)}
      style={({ pressed }) => ({
        minHeight: theme.size.touchTargetCompact,
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
          {t('notice', { count: waiting })}
        </AppText>
        {/* The affordance as a word rather than a glyph: an arrow renders differently across
            the OEM fonts on the tablets this clinic buys, and a control whose only affordance
            is a character that sometimes draws as a box is a control people do not press. */}
        <AppText size="2xs" weight="semibold" style={{ color: colors.text.link }}>
          {t('noticeAction')}
        </AppText>
      </View>
    </Pressable>
  );
}
