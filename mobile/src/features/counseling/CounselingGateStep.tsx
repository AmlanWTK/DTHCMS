import { Pressable, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppText } from '@/components/AppText';
import { theme, useTokens } from '@/lib/tokens';
import { usePreferences } from '@/stores/preferences';

import {
  clockTime,
  gateReadingOf,
  grantedBy,
  mayTick as hatMayTick,
  type CounselingGate,
  type CounselingGateOverride,
  type Locale,
  type MissingChecklistGroup,
  type MissingRoomGroup,
} from './state';

/**
 * The counselling checkpoint, on the operator's phone (CP57, §5.5, [R-07]).
 *
 * # Everything here is arrangement; every decision is in `state.ts`
 *
 * The same split as every other station: this component cannot be rendered outside a device, so
 * anything it decided would be a decision nobody checks. What it holds is where things sit —
 * and here that is the whole deliverable, because the checkpoint itself is a trigger on the
 * queue table and nothing on this screen can move it.
 *
 * # It names the items, and that is what this checkpoint is for
 *
 * "This patient cannot go on" with no list is the failure CP57 exists to prevent: it sends an
 * operator back to a screen with no idea what to do next, and what they do next is find whoever
 * is quickest to ask rather than whoever is right. So the missing items are named — the item's
 * own words in the reader's language, the checklist they belong to, the room they are covered in
 * — above any of the sentences about what to do.
 *
 * # Two ways back, and they are drawn differently on purpose
 *
 * A checklist somebody left half-walked gets **resume**: the session exists and going back into
 * it is one tap. A checklist nobody opened gets **start**, and it says so in words — "nobody has
 * started this" — because "go back and finish it", told to somebody whose colleague never
 * started, is an instruction that cannot be followed, and following it sends half the patients
 * to the wrong room. The difference is the server's: it leaves `session_id` off the second kind.
 *
 * The control is one per checklist rather than one per item. A checklist nobody opened has every
 * mandatory item outstanding, and seven identical buttons that all open the same session is a
 * screen whose real choice is invisible.
 *
 * # There is no override control here, and there must never be one
 *
 * The valve exists — a physician records a reason and lets the patient through — and it needs
 * `counseling.gate.override`, which a station operator does not hold. A button for it would
 * answer 403 with a patient waiting, and a control that cannot work teaches an operator that the
 * screen lies. So the sentence names who *can* do it, and there is no call in `api.ts` a control
 * could be wired to.
 *
 * # It is not drawn as an error, and not as reassurance either
 *
 * Nothing was lost and nothing went wrong; the checkpoint did its job. So this uses the same
 * status tokens CP54's allergy gate uses rather than the treatment this app gives a crash or an
 * unreachable server — to an operator it is the same event as the other gate. And an
 * *overridden* visit is its own state with its own words: drawing it the way a clear one is
 * drawn would be telling a physician the counselling was done.
 *
 * # A checkpoint that has not answered is never silence
 *
 * With no answer yet, this says so. A screen that drew nothing while the read was in flight — or
 * after a 403, or after the link dropped — would read as "nothing is holding this patient",
 * which is the one wrong answer a checkpoint may not give by accident.
 */
export function CounselingGateStep({
  gate,
  permissions,
  loading,
  starting,
  onResume,
  onStart,
}: {
  /**
   * The server's own answer, or null until it has arrived. Never assembled on this side.
   *
   * One read draws the whole panel: each missing item carries the room's own words and the
   * checklist's own title beside their codes, so nothing here waits on a second request to say
   * what `INSULIN_CORNER` reads as.
   */
  gate: CounselingGate | null;
  /** The hat's permissions — a courtesy for choosing a sentence, never a control. */
  permissions: readonly string[];
  /** True while the answer is still in flight. False and no gate is a different fact. */
  loading: boolean;
  /** The template being opened, or null. One checklist at a time. */
  starting: string | null;
  onResume: (sessionId: string) => void;
  onStart: (templateId: string) => void;
}) {
  const t = useTranslations('counseling');
  const { colors, status } = useTokens();
  const locale = usePreferences((preference) => preference.language);

  // A headline, a meaning and a remedy each name their own sentence, so the key is a value
  // rather than a literal and `useTranslations` cannot type it. The same cast the other stations
  // use, with the same guarantee behind it: the test file asserts every key this code can
  // produce exists in both languages.
  const say = t as unknown as (key: string, values?: Record<string, string>) => string;

  if (gate === null) {
    return (
      <View testID="counseling-gate" style={{ gap: theme.spacing['1'] }}>
        <AppText size="sm" weight="semibold" style={{ color: colors.text.secondary }}>
          {t('gate.step')}
        </AppText>
        {/* Two different silences, and neither of them is "the patient may go on". */}
        <AppText size="base">{loading ? t('gate.loading') : t('gate.unknown')}</AppText>
      </View>
    );
  }

  const reading = gateReadingOf(gate, locale as Locale);
  const tone = status[reading.tone];
  const allowed = hatMayTick(permissions);

  return (
    <View testID="counseling-gate" style={{ gap: theme.spacing['4'] }}>
      {/* What the queue will do, in words, before anything else on the panel. */}
      <View
        style={{
          gap: theme.spacing['2'],
          padding: theme.spacing['4'],
          borderRadius: theme.borderRadius.lg,
          borderWidth: 2,
          borderColor: tone.border,
          backgroundColor: tone.surface,
        }}
      >
        <AppText size="sm" weight="semibold" style={{ color: colors.text.secondary }}>
          {t('gate.step')}
        </AppText>
        <AppText
          testID="counseling-gate-headline"
          size="xl"
          weight="bold"
          style={{ color: tone.text }}
        >
          {say(reading.headline)}
        </AppText>
        <AppText size="base">{say(reading.meaning)}</AppText>
        {reading.missing > 0 ? (
          <AppText testID="counseling-gate-count" size="base" weight="semibold">
            {t('gate.stillMissing', { n: String(reading.missing) })}
          </AppText>
        ) : null}
      </View>

      {reading.override !== null ? <Override override={reading.override} /> : null}

      {/* By name, grouped by room, in the order the corridor is walked. A count on its own is
          what sends an operator hunting for somebody to ask. */}
      {reading.rooms.map((group) => (
        <Room
          key={group.room}
          group={group}
          say={say}
          allowed={allowed}
          starting={starting}
          onResume={onResume}
          onStart={onStart}
        />
      ))}

      {reading.blocked ? (
        // The valve, named rather than offered. It is a physician's, it needs a reason, and it
        // is recorded — and a control here would be a 403 in front of a waiting patient.
        <AppText
          testID="counseling-gate-no-override"
          size="sm"
          style={{ color: colors.text.secondary }}
        >
          {t('gate.noOverrideHere')}
        </AppText>
      ) : null}
    </View>
  );
}

type Say = (key: string, values?: Record<string, string>) => string;

/**
 * One room's worth of what is missing.
 *
 * A heading, exactly as on the checklist itself, because §5.2's rooms are rooms in a corridor
 * and the grouping is the route back. There is no "your room" marking here as there is on the
 * checklist: that comes from `room_station`, which a session's items carry and the gate's do not
 * — and fetching the room catalogue for that one boolean is the second request CP56 removed.
 * Every room is drawn to everybody either way, which was always the half that mattered.
 */
function Room({
  group,
  say,
  allowed,
  starting,
  onResume,
  onStart,
}: {
  group: MissingRoomGroup;
  say: Say;
  allowed: boolean;
  starting: string | null;
  onResume: (sessionId: string) => void;
  onStart: (templateId: string) => void;
}) {
  return (
    <View testID={`counseling-gate-room-${group.room}`} style={{ gap: theme.spacing['2'] }}>
      <AppText size="lg" weight="semibold">
        {group.heading.text}
      </AppText>
      {group.lists.map((list) => (
        <Checklist
          key={`${list.templateId}:${list.sessionId}`}
          list={list}
          say={say}
          allowed={allowed}
          // Only a start is ever in flight from here. Resuming is a choice on this screen and
          // writes nothing, so it must not go quiet because a checklist is being opened.
          busy={list.remedy === 'start' && starting === list.templateId}
          onResume={() => onResume(list.sessionId)}
          onStart={() => onStart(list.templateId)}
        />
      ))}
    </View>
  );
}

/**
 * One checklist's missing items, and the one tap that leads back to them.
 *
 * The items come first and by name, in the reader's language: "two items missing" and "insulin
 * technique and glucometer use are missing" are the difference between a screen somebody acts on
 * and one they go and ask about. The checklist's own name is above them, because a patient with
 * two conditions gets two checklists and an item with no list beside it is an item in an unknown
 * room.
 *
 * Then one sentence saying which of the two situations this is, and then one control. The
 * sentence is not decoration: the verb on a button is read after the press as often as before
 * it, and "resume" pressed by somebody whose colleague never started is a tap that does nothing
 * they expected.
 */
function Checklist({
  list,
  say,
  allowed,
  busy,
  onResume,
  onStart,
}: {
  list: MissingChecklistGroup;
  say: Say;
  allowed: boolean;
  busy: boolean;
  onResume: () => void;
  onStart: () => void;
}) {
  const t = useTranslations('counseling');
  const { colors } = useTokens();
  const resume = list.remedy === 'resume';

  return (
    <View
      testID={`counseling-gate-list-${list.templateId}`}
      style={{
        gap: theme.spacing['2'],
        padding: theme.spacing['4'],
        borderRadius: theme.borderRadius.lg,
        borderWidth: 1,
        borderColor: colors.border.subtle,
        backgroundColor: colors.surface.raised,
      }}
    >
      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {t('gate.onChecklist', { checklist: list.checklist })}
      </AppText>

      {list.rows.map((row) => (
        <View key={row.code} testID={`counseling-gate-missing-${row.code}`}>
          {row.text.text === '' ? (
            // Never a blank line where the question belongs. An item with no words in either
            // language is still an item the patient is being held for.
            <AppText size="base" style={{ color: colors.text.secondary }}>
              {t('noText')}
            </AppText>
          ) : (
            <>
              <AppText size="base" weight="semibold">
                {row.text.text}
              </AppText>
              {!row.text.ownLanguage ? (
                <AppText size="xs" style={{ color: colors.text.muted }}>
                  {say(`inLanguage.${row.text.language ?? 'en'}`)}
                </AppText>
              ) : null}
            </>
          )}
        </View>
      ))}

      {/* Which of the two situations this is, said before the control rather than left to the
          verb on it. They are a different room and a different person. */}
      <AppText size="sm">{say(`gate.remedyHint.${list.remedy}`)}</AppText>

      {resume || allowed ? (
        <Pressable
          testID={
            resume
              ? `counseling-gate-resume-${list.templateId}`
              : `counseling-gate-start-${list.templateId}`
          }
          accessibilityRole="button"
          accessibilityState={{ disabled: busy }}
          disabled={busy}
          onPress={resume ? onResume : onStart}
          style={({ pressed }) => ({
            // The token, and then some — the same sizing the tick control gets, and for the same
            // reason: read at arm's length and tapped in a hurry by somebody who is also talking
            // to a patient.
            minHeight: theme.size.touchTarget,
            paddingVertical: theme.spacing['3'],
            justifyContent: 'center',
            paddingHorizontal: theme.spacing['4'],
            borderRadius: theme.borderRadius.md,
            borderWidth: 2,
            borderColor: busy ? colors.state.disabledBorder : colors.border.control,
            backgroundColor: busy ? colors.state.disabledSurface : colors.surface.base,
            opacity: pressed ? 0.85 : 1,
          })}
        >
          <AppText size="lg" weight="semibold">
            {busy ? t('startingChecklist') : say(`gate.remedy.${list.remedy}`)}
          </AppText>
        </Pressable>
      ) : (
        // A hat that may read counselling without ticking it — a reviewer's, a physician's.
        // Not a greyed-out button: a control that looks disabled invites the press that
        // produces the 403, and "nobody has opened this" is a fact about the visit rather than
        // something waiting on the reader.
        <AppText testID={`counseling-gate-unwalked-${list.templateId}`} size="base">
          {t('unwalked')}
        </AppText>
      )}
    </View>
  );
}

/**
 * The override standing on this visit: who, when, why, and what it covered.
 *
 * `missing_at_grant` is what was outstanding **at the moment it was granted**, not what is
 * outstanding now — items covered afterwards would otherwise make the record read as an override
 * for nothing. It is shown as the server sent it, by item code, because that is the list the
 * person reviewing override rates will be reading.
 */
function Override({ override }: { override: CounselingGateOverride }) {
  const t = useTranslations('counseling');
  const { colors } = useTokens();
  const locale = usePreferences((preference) => preference.language);
  const when = clockTime(override.granted_at);
  // The person, where the record names one, and the role behind them where it does not. A
  // sentence reading "let through at 11:02 by  " would look like a bug and be read past; "by
  // somebody else" is less information and is still true.
  const named = grantedBy(override, locale as Locale);
  const who = named === '' ? t('unknownRole') : named;

  return (
    <View testID="counseling-gate-override" style={{ gap: theme.spacing['1'] }}>
      <AppText size="base" weight="semibold">
        {when === ''
          ? t('gate.overrideGranted', { who })
          : t('gate.overrideGrantedAt', { when, who })}
      </AppText>
      {/* The reason is the whole worth of the valve: what keeps an override honest is a person
          reading them, and a reason nobody shows is a reason nobody reads. */}
      <AppText size="base">{t('gate.overrideReason', { reason: override.reason })}</AppText>
      {override.missing_at_grant.length > 0 ? (
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {t('gate.overrideCovered', { items: override.missing_at_grant.join(', ') })}
        </AppText>
      ) : null}
    </View>
  );
}
