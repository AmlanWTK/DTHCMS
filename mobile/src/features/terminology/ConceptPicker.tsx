import { Pressable, ScrollView, TextInput, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppButton } from '@/components/AppButton';
import { AppText } from '@/components/AppText';
import { theme, useTokens } from '@/lib/tokens';
import { usePreferences } from '@/stores/preferences';

import {
  atCap,
  busy,
  catalogueAbsent,
  conceptLabel,
  conceptRows,
  isSelected,
  MAX_RESULTS,
  type Concept,
  type ConceptRow,
  type PickerState,
} from './search';

/**
 * The coded terminology picker, at a station (CP52, §3 step 6, [R-01]).
 *
 * # Everything here is arrangement; every decision is in `search.ts`
 *
 * The same split as every other station: this component cannot be rendered outside a device,
 * so anything it decided would be a decision nobody checks. What it holds is where things sit
 * — which is worth getting right, and is judged by a clinician using it on the clinic's
 * tablet, not by a test.
 *
 * # It opens on the clinic's twenty
 *
 * Before anybody types, the list is the clinic's own ranked favourites. That is criterion 1's
 * cheap half: the diagnoses DTHC actually makes cost no keystrokes, and everything else costs
 * three. A picker that opened blank would teach staff that the search is where diagnoses come
 * from, and they would type three letters for "Type 2 diabetes mellitus" every time.
 *
 * # Every rank carries its reason, in words
 *
 * The server returns the tier on every row and this shows it. "Why is that third" is the
 * question every search gets asked, and the tier-4 answer — *this is a guess at a misspelling*
 * — is the one that has to arrive before the tap rather than after it.
 *
 * # The chip is the coding, all three parts of it
 *
 * System, code and version, together, in the largest chip on the screen. Criterion 2 is that a
 * coding is those three things, and a picker whose chip said only "E11.9" would be a picker
 * that taught everybody a code is enough.
 *
 * # Nothing here blocks the station
 *
 * A refused search shows the server's own sentence. An unreachable catalogue says so and says
 * plainly that the diagnosis may still be recorded in words — because the clinic's link drops
 * for minutes at a time (ADR-0004), and a picker that held the consultation hostage to a
 * catalogue would be switched off inside a week.
 *
 * # No state is carried by colour alone
 *
 * The selected row says "Selected", the failures say what failed, and every rank says why.
 * Roughly one man in twelve who will work here cannot rely on the colour, and direct sun
 * through the clinic's windows flattens it for everybody else.
 */
export function ConceptPicker({
  state,
  systemTitle,
  onChangeQuery,
  onSelect,
  onClearSelection,
  onRetry,
}: {
  state: PickerState;
  /** The terminology's own title, when the caller has fetched `/v1/terminology/systems`. */
  systemTitle?: string;
  onChangeQuery: (text: string) => void;
  onSelect: (concept: Concept) => void;
  onClearSelection: () => void;
  onRetry: () => void;
}) {
  const t = useTranslations('terminology');
  const { colors, status } = useTokens();
  const locale = usePreferences((s) => s.language);

  // A reason names its own sentence, so the key is a value rather than a literal and
  // `useTranslations` cannot type it — the same cast the examination station uses for its
  // prompts, and the same guarantee behind it: the test file asserts every reason this code
  // can produce exists in both languages.
  const say = t as unknown as (key: string, values?: Record<string, string>) => string;

  const rows = conceptRows(state.concepts, locale);
  const searching = busy(state);
  const showingFavourites = state.query.trim() === '';

  return (
    <View testID="concept-picker" style={{ gap: theme.spacing['4'] }}>
      <View style={{ gap: theme.spacing['0.5'] }}>
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {t('title')}
        </AppText>
        {/* Which catalogue, and which version of it, in plain sight. A clinician who cannot
            see what they are searching cannot tell a thin result from the wrong system. */}
        <AppText size="sm" style={{ color: colors.text.muted }}>
          {t('searchingIn', {
            system: systemTitle ?? state.system,
            version: state.version === '' ? t('versionPending') : state.version,
          })}
        </AppText>
      </View>

      {state.selected !== null ? (
        <View
          testID="concept-selected"
          style={{
            flexDirection: 'row',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: theme.spacing['3'],
            borderRadius: theme.borderRadius.lg,
            borderWidth: 2,
            borderColor: status.normal.border,
            backgroundColor: status.normal.surface,
            padding: theme.spacing['4'],
          }}
        >
          <View style={{ flex: 1, gap: theme.spacing['0.5'] }}>
            {/* The word, not the colour. */}
            <AppText size="xs" weight="semibold" style={{ color: status.normal.text }}>
              {t('selected')}
            </AppText>
            {/* All three parts of the coding. A chip showing only the code would teach the
                clinic that a code is a coding, which is the mistake criterion 2 exists for. */}
            <AppText size="lg" weight="semibold" variant="clinicalValue">
              {t('coding', {
                system: state.selected.system,
                code: state.selected.code,
                version: state.selected.version,
              })}
            </AppText>
          </View>
          <AppButton
            testID="concept-clear"
            variant="secondary"
            label={t('clear')}
            onPress={onClearSelection}
          />
        </View>
      ) : null}

      <View style={{ gap: theme.spacing['1.5'] }}>
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {t('searchLabel')}
        </AppText>
        <TextInput
          testID="concept-query"
          value={state.query}
          onChangeText={onChangeQuery}
          autoCorrect={false}
          autoCapitalize="none"
          accessibilityLabel={t('searchLabel')}
          placeholder={t('placeholder')}
          placeholderTextColor={colors.text.muted}
          style={{
            // Half again the touch-target floor. The station's staff use these standing up
            // and often in gloves, and a 48-point box is a box they miss once a morning.
            minHeight: theme.size.touchTarget * 1.5,
            borderWidth: 1,
            borderColor: colors.border.control,
            borderRadius: theme.borderRadius.md,
            paddingHorizontal: theme.spacing['4'],
            backgroundColor: colors.surface.raised,
            color: colors.text.primary,
            fontSize: theme.fontSize.xl,
          }}
        />
      </View>

      {state.trouble !== null ? (
        <View
          testID="concept-trouble"
          style={{
            gap: theme.spacing['2'],
            borderRadius: theme.borderRadius.lg,
            borderWidth: 1,
            borderColor: status.borderline.border,
            backgroundColor: status.borderline.surface,
            padding: theme.spacing['4'],
          }}
        >
          <AppText size="sm" weight="semibold" style={{ color: status.borderline.text }}>
            {/* Three troubles, three headings. "Cannot be reached" and "could not answer"
                leave the operator in the same place but are not the same fact, and a picker
                that reported a 503 as a dead network would send the wrong person to look. */}
            {state.trouble.kind === 'refused'
              ? t('refused')
              : state.trouble.kind === 'unreachable'
                ? t('unreachable')
                : t('failed')}
          </AppText>
          {/* The server's own sentence where there is one. A client that paraphrased a
              licensing refusal would be inventing a second account of somebody else's rules. */}
          {state.trouble.kind !== 'unreachable' && state.trouble.message !== '' ? (
            <AppText size="base">{state.trouble.message}</AppText>
          ) : null}
          {catalogueAbsent(state.trouble) ? (
            <>
              <AppText size="base" weight="semibold">
                {t('freeText')}
              </AppText>
              <AppButton
                testID="concept-retry"
                variant="secondary"
                label={t('retry')}
                onPress={onRetry}
              />
            </>
          ) : null}
        </View>
      ) : null}

      <View style={{ gap: theme.spacing['1'] }}>
        <AppText size="sm" weight="semibold" style={{ color: colors.text.secondary }}>
          {showingFavourites ? t('favourites') : t('results')}
        </AppText>
        {searching ? (
          // A word rather than a spinner alone: a spinner beside a stale list says nothing
          // about which of the two the clinician is looking at.
          <AppText testID="concept-searching" size="sm" style={{ color: colors.text.muted }}>
            {t('searching')}
          </AppText>
        ) : null}
      </View>

      <ScrollView
        testID="concept-results"
        style={{ maxHeight: theme.size.touchTarget * 9 }}
        keyboardShouldPersistTaps="handled"
        contentContainerStyle={{ gap: theme.spacing['2'] }}
      >
        {rows.map((row) => (
          <Row
            key={`${row.concept.system}|${row.concept.version}|${row.concept.code}`}
            row={row}
            locale={locale}
            selected={isSelected(state, row.concept)}
            reasonText={reasonTextOf(row, say)}
            onPress={() => onSelect(row.concept)}
          />
        ))}

        {rows.length === 0 && !searching && state.trouble === null ? (
          <View testID="concept-empty" style={{ gap: theme.spacing['2'] }}>
            <AppText size="base">
              {showingFavourites ? t('noFavourites') : t('nothingFound')}
            </AppText>
            <AppText size="sm" style={{ color: colors.text.secondary }}>
              {t('freeText')}
            </AppText>
          </View>
        ) : null}

        {atCap(state.concepts) ? (
          // Honest about the ceiling. The bottom of this list is not the bottom of the
          // catalogue, and only a clinician who is told so can write a better query.
          <AppText testID="concept-cap" size="sm" style={{ color: colors.text.muted }}>
            {t('atCap', { n: String(MAX_RESULTS) })}
          </AppText>
        ) : null}
      </ScrollView>
    </View>
  );
}

type Say = (key: string, values?: Record<string, string>) => string;

/**
 * The sentence behind one row's rank. The clinic list names its number; the tiers do not.
 *
 * The rank is stringified rather than localised into Bengali numerals, which is a deliberate
 * divergence from the web picker: the station app has no number formatter anywhere, and Hermes
 * ships without the ICU data `Intl.NumberFormat` needs for `bn`. Adding it for one ordinal
 * would be a bundle cost for a digit. Worth revisiting the day this app formats its first
 * measurement — at which point it should be one helper, not two.
 */
function reasonTextOf(row: ConceptRow, say: Say): string {
  if (row.reason === null) return '';
  if (row.reason === 'clinicList') {
    return say('reason.clinicList', { rank: String(row.concept.favourite_rank ?? '') });
  }
  return say(`reason.${row.reason}`);
}

/**
 * One result.
 *
 * The code sits beside the display rather than under it, in the Latin face with tabular
 * figures, because a clinician checking a coding reads the code — and a code whose digits
 * change shape with the interface language is a code somebody transcribes wrongly onto paper.
 */
function Row({
  row,
  locale,
  selected,
  reasonText,
  onPress,
}: {
  row: ConceptRow;
  locale: 'en' | 'bn';
  selected: boolean;
  reasonText: string;
  onPress: () => void;
}) {
  const t = useTranslations('terminology');
  const { colors, status } = useTokens();
  const label = conceptLabel(row.concept, locale);

  return (
    <View style={{ gap: theme.spacing['1'] }}>
      {row.heading !== '' ? (
        // Captioned where the chapter changes, walking the server's order. Collecting the
        // chapters together would re-rank the results, which is the server's job and not this
        // screen's.
        <AppText size="xs" weight="semibold" style={{ color: colors.text.muted }}>
          {row.heading}
        </AppText>
      ) : null}

      <Pressable
        testID={`concept-${row.concept.code}`}
        accessibilityRole="radio"
        accessibilityState={{ selected, disabled: !row.selectable }}
        accessibilityLabel={`${label}, ${row.concept.code}${reasonText === '' ? '' : `, ${reasonText}`}`}
        disabled={!row.selectable}
        onPress={onPress}
        style={{
          minHeight: theme.size.touchTarget * 1.5,
          justifyContent: 'center',
          gap: theme.spacing['1'],
          padding: theme.spacing['4'],
          borderRadius: theme.borderRadius.md,
          borderWidth: selected ? 2 : 1,
          borderColor: selected ? colors.brand.border : colors.border.subtle,
          backgroundColor: selected ? colors.brand.subtle : colors.surface.raised,
        }}
      >
        <View
          style={{
            flexDirection: 'row',
            alignItems: 'baseline',
            gap: theme.spacing['3'],
          }}
        >
          <AppText size="base" weight="semibold" variant="clinicalValue">
            {row.concept.code}
          </AppText>
          <AppText size="base" weight={selected ? 'semibold' : 'regular'} style={{ flex: 1 }}>
            {label}
          </AppText>
        </View>

        <View style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.spacing['2'] }}>
          {selected ? (
            <AppText size="xs" weight="semibold" style={{ color: colors.brand.text }}>
              {t('selected')}
            </AppText>
          ) : null}
          {reasonText !== '' ? (
            <AppText size="xs" style={{ color: colors.text.muted }}>
              {reasonText}
            </AppText>
          ) : null}
          {!row.selectable ? (
            // A row the server sent without a version. It cannot become a coding, and saying
            // so on the row is the only honest place to say it.
            <AppText size="xs" weight="semibold" style={{ color: status.critical.text }}>
              {t('noVersion')}
            </AppText>
          ) : null}
        </View>
      </Pressable>
    </View>
  );
}
