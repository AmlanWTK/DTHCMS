import { useState } from 'react';
import { Pressable, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppText } from '@/components/AppText';
import { theme, useTokens } from '@/lib/tokens';
import { usePreferences } from '@/stores/preferences';
import { useSession } from '@/stores/session';

import { useDirectoryIndex } from './api';
import { clinicDay, readingFor, type Provenance, type Reading, type SourceTone } from './state';

/**
 * Who entered this value (CP61, §4.2, [R-03]).
 *
 * # Everything here is arrangement; every decision is in `state.ts`
 *
 * This component cannot be rendered outside a device, so anything it decided would be a
 * decision nobody checks — and what this component would be deciding is whose name appears
 * beside a number in somebody's clinical record. So it decides nothing. `readingFor` resolves
 * the person, the role, the station, the time, the source and the correction; what is left
 * here is where those sit and how far the reveal pushes the form.
 *
 * # One tap, and no navigation (criterion 1)
 *
 * §4.2 asks that a reviewer see who entered a value *instantly, without digging*. On a phone
 * there is no hover, so this is a disclosure: the row is a button, one press opens it, one
 * press closes it. There is no screen to go to and nothing to come back from — a reviewer who
 * has to navigate has lost the value they were looking at, and half of them will not bother
 * a second time.
 *
 * # Why the reveal expands downward and does not float
 *
 * The panel opens **below the summary line, inside the same block**. The summary line, the
 * clinical value above it and everything above that do not move; content below is pushed down
 * by the height of six short rows. That is the trade the alternatives lose:
 *
 *   - An overlay or modal moves nothing, and on Android it dismisses the keyboard. An operator
 *     mid-entry who taps to check whose last reading they are comparing against would lose
 *     their keyboard, their cursor and their place — worse than the scroll they were avoiding.
 *   - An absolutely positioned popover over the following rows covers the very values a
 *     reviewer is comparing this one against, and on a narrow screen it has nowhere to go.
 *   - A pushed layout that opened *upward* would move the value itself out from under the
 *     reader's thumb, which is the one thing that must never happen.
 *
 * So: downward, bounded, and never more than one block tall.
 *
 * # The compact variant is still an attribution, not a hint
 *
 * Dense lists — a history list, an allergy list, a checklist of counselling items — get
 * `compact`. It is smaller and it still **names the person on the always-visible line**,
 * because a dense list is exactly where a reviewer scans rather than taps. What compact drops
 * is padding, not information: the same tap opens the same panel.
 *
 * # Source is a word before it is a colour (criterion 3)
 *
 * The chip carries the word — *OCR*, *Station*, *Field*, *Device*, *Patient*, or *source not
 * recorded* — and the tone sits underneath it. Photocopied, screenshotted in greyscale, seen
 * in direct sun through the clinic's windows, or read by the roughly one man in twelve who
 * cannot rely on colour, the distinction survives, because it was never carried by the colour.
 *
 * # This component says who, and never who is at fault
 *
 * A staff name is not PHI, but it is a person. Nothing here is drawn in a warning tone because
 * of who entered it, no row is red, and the panel closes with the sentence saying so out loud.
 * A screen that made attribution look like blame is a screen where people stop entering values
 * under their own name.
 */
export function EnteredBy({
  provenance,
  compact = false,
  testID,
}: {
  /** Built by one of `state.ts`'s `of*` extractors. Null while the payload is still loading. */
  provenance: Provenance | null | undefined;
  /** For dense lists: less padding, same information, same tap. */
  compact?: boolean;
  testID?: string;
}) {
  const t = useTranslations('attribution');
  const tRole = useTranslations('role');
  const { colors, status } = useTokens();
  const locale = usePreferences((state) => state.language);
  const me = useSession((state) => state.operator?.id ?? '');
  const index = useDirectoryIndex();
  const [open, setOpen] = useState(false);

  // A refusal, a source and a role each name their own key, so the key is a value rather than
  // a literal and `useTranslations` cannot type it — the same cast the examination and history
  // stations use, with the same guarantee behind it: `attribution.test.ts` asserts every key
  // this feature can produce exists in both languages.
  const say = t as unknown as (key: string, values?: Record<string, string>) => string;
  const sayRole = tRole as unknown as (key: string) => string;

  const reading: Reading = readingFor(provenance, index, {
    locale,
    me,
    // The clinic day the reader is having, which is what decides whether the collapsed line
    // shows a time or a date. Read here rather than in `state.ts` so the module stays pure.
    today: clinicDay(new Date().toISOString()),
  });

  // One id for the whole block, so a list of forty rows has forty distinguishable targets for
  // the device flow rather than forty called `entered-by`.
  const id = testID ?? 'entered-by';

  const who = reading.person.key === null ? reading.person.text : say(reading.person.key);
  const role = reading.role.key === null ? reading.role.code : sayRole(reading.role.key);
  const when = reading.when.key === null ? reading.when.text : say(reading.when.key);
  const headline = say(reading.headline.key, { person: who, role, when });

  return (
    <View
      testID={id}
      style={{
        gap: compact ? theme.spacing['1'] : theme.spacing['2'],
        borderTopWidth: theme.size.borderWidth.thin,
        borderTopColor: colors.border.subtle,
        paddingTop: compact ? theme.spacing['1.5'] : theme.spacing['2'],
      }}
    >
      <Pressable
        testID={`${id}-toggle`}
        accessibilityRole="button"
        accessibilityState={{ expanded: open }}
        accessibilityLabel={headline}
        accessibilityHint={t('revealHint')}
        onPress={() => setOpen((was) => !was)}
        style={{
          // The token. 48 is CP09's safety floor and the compact variant uses the compact
          // floor rather than inventing a smaller number: a dense list is where a mis-tap is
          // most likely, not least.
          minHeight: compact ? theme.size.touchTargetCompact : theme.size.touchTarget,
          justifyContent: 'center',
          gap: theme.spacing['1'],
        }}
      >
        <View
          style={{
            flexDirection: 'row',
            flexWrap: 'wrap',
            alignItems: 'center',
            gap: theme.spacing['2'],
          }}
        >
          <AppText
            testID={`${id}-headline`}
            size={compact ? 'xs' : 'sm'}
            style={{ color: colors.text.secondary, flexShrink: 1 }}
          >
            {headline}
          </AppText>

          <SourceChip reading={reading} say={say} testID={`${id}-source`} />

          {/* The affordance, as a word. An arrow glyph renders differently across the OEM
              fonts on the tablets this clinic buys, and a control whose only affordance is a
              character that sometimes draws as a box is a control people do not press. */}
          <AppText size="2xs" weight="semibold" style={{ color: colors.text.link }}>
            {open ? t('less') : t('more')}
          </AppText>
        </View>
      </Pressable>

      {open ? (
        <View
          testID={`${id}-detail`}
          style={{
            gap: theme.spacing['1.5'],
            padding: theme.spacing['3'],
            borderRadius: theme.borderRadius.md,
            backgroundColor: colors.surface.sunken,
          }}
        >
          <Line label={t('who')} value={who} testID={`${id}-who`} />

          {/* A colleague who has left is named **and** said to have left. The whole reason the
              directory lists deactivated staff is that most of what a reviewer asks about is
              somebody who has since gone; saying so is what stops them walking down a corridor
              after them. */}
          {reading.person.standingKey !== null ? (
            <AppText testID={`${id}-standing`} size="xs" style={{ color: colors.text.muted }}>
              {say(reading.person.standingKey)}
            </AppText>
          ) : null}

          {reading.role.code !== '' ? <Line label={t('role')} value={role} /> : null}

          {reading.station !== null ? (
            <Line label={t('station')} value={reading.station.text} testID={`${id}-station`} />
          ) : null}

          {reading.device !== null ? (
            <Line label={t('device')} value={reading.device.text} />
          ) : null}

          {/* Date and time together, always. The collapsed line shows one or the other; the
              reveal is where a reviewer settles which day they are looking at. */}
          <Line
            label={t('when')}
            value={
              reading.when.known
                ? t('whenExact', { date: reading.when.date, time: reading.when.time })
                : t('whenUnknown')
            }
            testID={`${id}-when`}
          />

          {reading.source.meaningKey !== null ? (
            <AppText
              testID={`${id}-source-meaning`}
              size="xs"
              style={{ color: colors.text.secondary }}
            >
              {say(reading.source.meaningKey)}
            </AppText>
          ) : null}

          {reading.correction !== null ? (
            <View
              testID={`${id}-correction`}
              style={{
                gap: theme.spacing['1'],
                paddingTop: theme.spacing['1.5'],
                borderTopWidth: theme.size.borderWidth.thin,
                borderTopColor: colors.border.subtle,
              }}
            >
              <AppText size="xs" weight="semibold" style={{ color: colors.text.secondary }}>
                {say(reading.correction.key)}
              </AppText>
              {/* Criterion 2: both people, where the payload carries both. Where it carries
                  only the fact of the change — an observation points at the row that replaced
                  it, not at whoever wrote that row — no second name is drawn, because a name
                  invented here would be the name of the person being corrected. */}
              {reading.correction.by !== null ? (
                <Line
                  label={t('correctedBy')}
                  value={
                    reading.correction.by.key === null
                      ? reading.correction.by.text
                      : say(reading.correction.by.key)
                  }
                  testID={`${id}-corrected-by`}
                />
              ) : null}
              {reading.correction.when.known ? (
                <Line
                  label={t('correctedWhen')}
                  value={t('whenExact', {
                    date: reading.correction.when.date,
                    time: reading.correction.when.time,
                  })}
                />
              ) : null}
              {reading.correction.replacedBy === '' ? null : (
                <AppText size="xs" style={{ color: colors.text.muted }}>
                  {t('replacementElsewhere')}
                </AppText>
              )}
            </View>
          ) : null}

          {/* Said when it is true, and not otherwise. A reader who is not told will read a
              missing name as "the record does not know", when what happened is that this
              tablet could not reach the directory — and those two send them to different
              people. */}
          {reading.directoryMissing ? (
            <AppText testID={`${id}-no-directory`} size="xs" style={{ color: status.unknown.text }}>
              {t('directoryMissing')}
            </AppText>
          ) : null}

          <AppText size="2xs" style={{ color: colors.text.muted }}>
            {t('notBlame')}
          </AppText>
        </View>
      ) : null}
    </View>
  );
}

/**
 * The source, as a word on a ground.
 *
 * The word is the carrier and the tone is decoration — see the note at the top of this file.
 * A code this build has never heard of is drawn as itself rather than hidden, in the tone
 * reserved for "we cannot vouch for this", because a future source silently rendering as a
 * plain station entry is the failure criterion 3 exists to prevent.
 */
function SourceChip({
  reading,
  say,
  testID,
}: {
  reading: Reading;
  say: (key: string) => string;
  testID: string;
}) {
  const { colors, status } = useTokens();
  const tone = toneColours(reading.source.tone, colors, status);
  const word = reading.source.key === null ? reading.source.code : say(reading.source.key);

  return (
    <View
      testID={testID}
      style={{
        paddingHorizontal: theme.spacing['1.5'],
        paddingVertical: theme.spacing['0.5'],
        borderRadius: theme.borderRadius.sm,
        borderWidth: theme.size.borderWidth.thin,
        borderColor: tone.border,
        backgroundColor: tone.surface,
      }}
    >
      <AppText size="2xs" weight="semibold" style={{ color: tone.text }}>
        {word}
      </AppText>
    </View>
  );
}

type Palette = ReturnType<typeof useTokens>['colors'];
type StatusTones = ReturnType<typeof useTokens>['status'];

function toneColours(
  tone: SourceTone,
  colors: Palette,
  status: StatusTones,
): { surface: string; border: string; text: string } {
  switch (tone) {
    case 'transcribed':
      // The tone the design system keeps for a value that came from somewhere else, which is
      // exactly what an OCR read, a field visit or an instrument feed is.
      return status.stale;
    case 'unrecorded':
      return status.unknown;
    default:
      // A station entry is the ordinary case and wears no status colour at all. Tinting the
      // common case makes the uncommon one harder to spot, which is the opposite of the point.
      return {
        surface: colors.surface.sunken,
        border: colors.border.subtle,
        text: colors.text.secondary,
      };
  }
}

/** One labelled fact. The label is quiet; the fact is not. */
function Line({ label, value, testID }: { label: string; value: string; testID?: string }) {
  const { colors } = useTokens();
  return (
    <View
      testID={testID}
      style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.spacing['2'] }}
    >
      <AppText size="xs" style={{ color: colors.text.muted }}>
        {label}
      </AppText>
      <AppText size="xs" weight="semibold" style={{ color: colors.text.primary, flexShrink: 1 }}>
        {value}
      </AppText>
    </View>
  );
}
