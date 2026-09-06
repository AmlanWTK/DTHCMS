import { Pressable, ScrollView, TextInput, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppButton } from '@/components/AppButton';
import { AppText } from '@/components/AppText';
import { EnteredBy, ofDietEntry } from '@/features/attribution';
import { theme, useTokens } from '@/lib/tokens';
import { usePreferences } from '@/stores/preferences';

import {
  MAX_RESULTS,
  QUANTITY_STEPS,
  REASON_PRESETS,
  adviceFor,
  hasEscapeHatch,
  wordingOf,
  searching,
  troubleKey,
  unusualDay,
  type DayRelation,
  type EntryDraft,
  type EntryLine,
  type FoodRow,
  type Meal,
  type MealCode,
  type MealGroup,
  type MeasureChoice,
  type Missing,
  type PickerState,
  type RecallDay,
  type TotalsReading,
  type Trouble,
  type WithdrawalDraft,
  type Wording,
} from './state';

/**
 * Station 7's 24-hour dietary recall (CP59, blueprint §5.2, §12.1, [R-01]).
 *
 * A nutritionist sits beside a patient and walks through what they ate yesterday, meal by meal,
 * with a queue waiting and four minutes for the whole recall. **A second assistant may be doing
 * the same thing on another tablet at the same time.** Everything below follows from those two
 * sentences.
 *
 * # Everything here is arrangement; every decision is in `state.ts`
 *
 * This component cannot be rendered outside a device, so anything it decided would be a decision
 * nobody checks. It computes no weight, no calorie and no total — every figure on it came off
 * `recall.totals` or off an entry the server wrote, and there is no arithmetic in this file at
 * all.
 *
 * # The tap count, because it is the requirement
 *
 * The first food of a meal, at a quantity of one, is **four taps and three letters**: the meal,
 * the search box, the food, and add. Every food after it in the same meal is **three taps and
 * three letters**, because the meal stays selected — a patient describes a meal at a time, and
 * charging a tap per item for a fact that did not change is how four minutes becomes six. A
 * quantity that is not one costs one more tap, on a button rather than a keyboard.
 *
 * The measure costs nothing in the common case: the food's first household portion is chosen
 * with the food, because the migration's ordering puts the measure a patient is likeliest to
 * have used first. Grams is always on the row beside it and is never the default — a screen that
 * opened on grams would be asking the operator to convert "two cups" in their head in front of a
 * patient, which is the thing this station was built not to do.
 *
 * # The day so far is above the form, and it is the whole duplicate-prevention mechanism
 *
 * Two assistants entering one recall cannot collide — each entry is its own row, written once
 * and never edited — so there is nothing to lock and nothing to merge. The one real collision is
 * the *duplicate*, and the only thing that prevents it is one operator seeing what the other has
 * already recorded. So the day's totals and the contributors line sit at the top where they are
 * read without scrolling, the entries sit directly under the form where the eye goes after each
 * add, and the whole day is replaced by the response to **every** write.
 *
 * `contributors` being more than one is drawn as a statement rather than a number in a corner.
 * A recall two people built is the thing [R-01] asked for, and a screen that could not show it
 * would make the feature invisible to the people using it.
 *
 * # Any entry may be taken back, by anybody, and the control says so
 *
 * "Take back" is on every standing row, including rows somebody else recorded, and a line above
 * the list says that out loud. Hiding the control on a colleague's rows would mean the duplicate
 * stays until they come back from the next patient — which is the whole reason the API allows
 * it. A withdrawn entry stays on the day, struck through, with its reason and both names.
 *
 * # The day being recalled is the largest thing on the screen
 *
 * A recall filed against the wrong day is wrong in every figure it produces, and it is wrong
 * silently. The date the server chose is drawn in full, with the word for what it is —
 * *yesterday*, and a warning when it is not — and it can be stepped. The server's default is
 * computed in UTC and the clinic runs six hours ahead of that, so "yesterday" is a claim worth
 * checking rather than one to trust.
 *
 * # Nobody has approved the food table, and the screen says so once, clearly
 *
 * Every seeded food is unapproved and every row names its source. The notice sits above the form
 * rather than behind a tap, because a caveat somebody has to open is a caveat nobody reads — and
 * a calorie figure that looks authoritative is exactly what a starter list must not be allowed
 * to look like. The chosen food's own source is drawn beside it, so the provenance of the
 * specific number about to be recorded is under the operator's eye.
 *
 * # No state is carried by colour alone
 *
 * The chosen food and the chosen measure say "chosen", a withdrawn entry says "taken back"
 * beside the strike-through, and every refusal says what to do next. Roughly one man in twelve
 * who will work here cannot rely on the colour, and direct sun through the clinic's windows
 * flattens it for everybody else.
 */
export function NutritionStation({
  patientName,
  recallDate,
  relation,
  totals,
  unapprovedFoods,
  picker,
  rows,
  draft,
  meals,
  choices,
  ceiling,
  missing,
  chosenFood,
  groups,
  days,
  justAdded,
  withdrawal,
  loadingRecall,
  busy,
  trouble,
  onStepDay,
  onChooseDay,
  onRefresh,
  onTypeQuery,
  onRetrySearch,
  onChooseMeal,
  onChooseFood,
  onChooseMeasure,
  onTypeQuantity,
  onAdd,
  onOpenWithdrawal,
  onTypeReason,
  onWithdraw,
  onCancelWithdrawal,
}: {
  patientName: string;
  /** The day being recalled, as the server answered it. Never computed on this side. */
  recallDate: string;
  /** How that day sits against the clinic's calendar, or null while it is unknown. */
  relation: DayRelation | null;
  /** The server's own figures for the day. Null before the first read. */
  totals: TotalsReading | null;
  /** How many foods on offer nobody has approved. Today, all of them. */
  unapprovedFoods: number;
  picker: PickerState;
  rows: FoodRow[];
  draft: EntryDraft;
  /**
   * The day's meals, with their names, in the order the day happens.
   *
   * From `core.meal` through `GET /v1/foods/measures`. This component holds no meal name in
   * either language and no idea that lunch follows breakfast.
   */
  meals: readonly Meal[];
  /** The measures this food can be recorded in, and no others. */
  choices: MeasureChoice[];
  /** The most of the chosen measure one entry may carry, from the server. Null until it says. */
  ceiling: number | null;
  missing: Missing[];
  chosenFood: FoodRow | null;
  groups: MealGroup[];
  /** Which days this patient has a recall for, and how much is on each. Newest first. */
  days: readonly RecallDay[];
  /**
   * The entry this tablet's last write produced, derived from the event id it sent.
   *
   * Empty before the first write. The day is grouped by meal, so an added breakfast item lands
   * above where the operator is looking — and among two identical rows from two tablets there is
   * otherwise nothing to say which is yours.
   */
  justAdded: string;
  /** The entry being taken back, or null. One at a time. */
  withdrawal: WithdrawalDraft | null;
  loadingRecall?: boolean;
  busy?: boolean;
  trouble?: Trouble | null;
  onStepDay: (days: number) => void;
  onChooseDay: (date: string) => void;
  onRefresh: () => void;
  onTypeQuery: (text: string) => void;
  onRetrySearch: () => void;
  onChooseMeal: (meal: MealCode) => void;
  onChooseFood: (row: FoodRow) => void;
  onChooseMeasure: (code: string) => void;
  onTypeQuantity: (text: string) => void;
  onAdd: () => void;
  onOpenWithdrawal: (entryId: string) => void;
  onTypeReason: (text: string) => void;
  onWithdraw: () => void;
  onCancelWithdrawal: () => void;
}) {
  const t = useTranslations('nutrition');
  const { colors } = useTokens();

  return (
    <ScrollView
      contentContainerStyle={{ gap: theme.spacing['5'], paddingBottom: theme.spacing['12'] }}
      keyboardShouldPersistTaps="handled"
    >
      <View style={{ gap: theme.spacing['0.5'] }}>
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {t('station')}
        </AppText>
        <AppText size="lg" weight="semibold">
          {patientName}
        </AppText>
      </View>

      <RecallDay
        date={recallDate}
        relation={relation}
        days={days}
        busy={busy}
        onStepDay={onStepDay}
        onChooseDay={onChooseDay}
      />

      {trouble ? (
        <TroubleBanner trouble={trouble} onRefresh={onRefresh} onRetry={onRetrySearch} />
      ) : null}

      <Totals totals={totals} loading={loadingRecall} onRefresh={onRefresh} />

      {unapprovedFoods > 0 ? <TableNotice count={unapprovedFoods} /> : null}

      <EntryForm
        picker={picker}
        rows={rows}
        draft={draft}
        meals={meals}
        choices={choices}
        ceiling={ceiling}
        missing={missing}
        chosenFood={chosenFood}
        busy={busy}
        onTypeQuery={onTypeQuery}
        onRetrySearch={onRetrySearch}
        onChooseMeal={onChooseMeal}
        onChooseFood={onChooseFood}
        onChooseMeasure={onChooseMeasure}
        onTypeQuantity={onTypeQuantity}
        onAdd={onAdd}
      />

      <Day
        groups={groups}
        justAdded={justAdded}
        withdrawal={withdrawal}
        busy={busy}
        onOpenWithdrawal={onOpenWithdrawal}
        onTypeReason={onTypeReason}
        onWithdraw={onWithdraw}
        onCancelWithdrawal={onCancelWithdrawal}
      />
    </ScrollView>
  );
}

// --- which day is being recalled ---

/**
 * The day, in full, with the word for what it is.
 *
 * The largest non-clinical thing on the screen, because it is the one mistake that is wrong in
 * every figure and wrong silently: a recall taken on Tuesday about Monday, filed as Tuesday,
 * misdates every calorie the analysis reads.
 *
 * The date itself is drawn in the Latin face with tabular figures, like every other clinical
 * date in this system: digits in one order in both interfaces, because 03/04 meaning two
 * different days to two people looking at one record is an ambiguity a clinical date cannot
 * afford. The *word* beside it is translated; the digits are not.
 */
function RecallDay({
  date,
  relation,
  days,
  busy,
  onStepDay,
  onChooseDay,
}: {
  date: string;
  relation: DayRelation | null;
  days: readonly RecallDay[];
  busy?: boolean;
  onStepDay: (days: number) => void;
  onChooseDay: (date: string) => void;
}) {
  const t = useTranslations('nutrition');
  const { colors, status } = useTokens();
  const say = t as unknown as (key: string) => string;
  const odd = unusualDay(relation);

  return (
    <View
      testID="recall-day"
      style={{
        backgroundColor: odd ? status.borderline.surface : colors.surface.raised,
        borderRadius: theme.borderRadius.lg,
        borderWidth: odd ? theme.size.borderWidth.thick : theme.size.borderWidth.thin,
        borderColor: odd ? status.borderline.border : colors.border.subtle,
        padding: theme.spacing['4'],
        gap: theme.spacing['2'],
      }}
    >
      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {t('day.title')}
      </AppText>
      <AppText testID="recall-day-date" size="3xl" weight="bold" variant="clinicalValue">
        {date === '' ? t('day.unknown') : date}
      </AppText>
      {relation === null ? null : (
        <AppText testID="recall-day-relation" weight="semibold">
          {say(`day.relation.${relation}`)}
        </AppText>
      )}
      {/* A 24-hour recall is about a day that has finished. Said in words, in the tone the rest
          of the system uses for "look at this again", rather than left to the operator to
          notice that the date on screen is today's. */}
      {odd ? (
        <AppText testID="recall-day-warning" size="sm" style={{ color: status.borderline.text }}>
          {t('day.unusual')}
        </AppText>
      ) : null}

      <View style={{ flexDirection: 'row', gap: theme.spacing['2'] }}>
        <View style={{ flex: 1 }}>
          <AppButton
            testID="recall-day-back"
            variant="secondary"
            disabled={busy === true}
            label={t('day.back')}
            onPress={() => onStepDay(-1)}
          />
        </View>
        <View style={{ flex: 1 }}>
          <AppButton
            testID="recall-day-forward"
            variant="secondary"
            disabled={busy === true}
            label={t('day.forward')}
            onPress={() => onStepDay(1)}
          />
        </View>
      </View>

      {/* The days this patient already has a recall for, with how much is on each — because
          "3 September · 12 items" is a day worth opening and "3 September · 1 item" is one
          somebody abandoned, and stepping one day at a time to find either is a lot of taps.
          Drawn only when there is more than the day already on screen to reach. */}
      {days.length > 0 ? (
        <View testID="recall-days" style={{ gap: theme.spacing['1'] }}>
          <AppText size="xs" style={{ color: colors.text.muted }}>
            {t('day.otherDays')}
          </AppText>
          <View style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.spacing['2'] }}>
            {days.map((one) => {
              const selected = one.date === date;
              return (
                <Pressable
                  key={one.date}
                  testID={`recall-day-${one.date}`}
                  accessibilityRole="radio"
                  accessibilityState={{ selected }}
                  disabled={busy === true}
                  onPress={() => onChooseDay(one.date)}
                  style={{
                    minHeight: theme.size.touchTarget,
                    justifyContent: 'center',
                    paddingHorizontal: theme.spacing['3'],
                    borderRadius: theme.borderRadius.md,
                    borderWidth: selected
                      ? theme.size.borderWidth.thick
                      : theme.size.borderWidth.thin,
                    borderColor: selected ? colors.brand.border : colors.border.control,
                    backgroundColor: selected ? colors.brand.subtle : colors.surface.raised,
                  }}
                >
                  {/* The date in the Latin face like every other clinical date, and the count
                      in a sentence, so a Bangla reader gets Bengali numerals for the one and
                      one order of digits for the other. */}
                  <AppText size="sm" weight="semibold" variant="clinicalValue">
                    {one.date}
                  </AppText>
                  <AppText size="xs" style={{ color: colors.text.muted }}>
                    {t('day.dayEntries', { n: one.entries })}
                  </AppText>
                </Pressable>
              );
            })}
          </View>
        </View>
      ) : null}
    </View>
  );
}

// --- the day's totals, which are the server's ---

/**
 * The energy and the three macros, exactly as they came back.
 *
 * There is no branch here that produces a figure from anything but `totals`, and no arithmetic:
 * the sums are computed by the server over the entries that still stand, so a withdrawn entry
 * takes its calories with it the moment it is withdrawn. A client that added these up would be
 * a client that could show one number while the research extract held another.
 *
 * The contributors line is the part that matters at this station. It is a sentence rather than a
 * count in a corner, because the person reading it has to *act* on it — by scrolling down and
 * reading what their colleague already put in.
 */
function Totals({
  totals,
  loading,
  onRefresh,
}: {
  totals: TotalsReading | null;
  loading?: boolean;
  onRefresh: () => void;
}) {
  const t = useTranslations('nutrition');
  const { colors, status } = useTokens();

  return (
    <View
      testID="recall-totals"
      style={{
        backgroundColor: colors.surface.raised,
        borderRadius: theme.borderRadius.lg,
        borderWidth: theme.size.borderWidth.thin,
        borderColor: colors.border.subtle,
        padding: theme.spacing['4'],
        gap: theme.spacing['2'],
      }}
    >
      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {t('totals.title')}
      </AppText>

      {totals === null ? (
        <AppText size="sm" style={{ color: colors.text.muted }}>
          {loading === true ? t('totals.reading') : t('totals.notRead')}
        </AppText>
      ) : (
        <>
          <View style={{ flexDirection: 'row', alignItems: 'baseline', gap: theme.spacing['2'] }}>
            <AppText testID="recall-kcal" size="4xl" weight="bold" variant="clinicalValue">
              {String(totals.kcal)}
            </AppText>
            <AppText size="sm" style={{ color: colors.text.secondary }}>
              {t('totals.kcal')}
            </AppText>
          </View>

          <View style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.spacing['4'] }}>
            <Macro testID="recall-protein" label={t('totals.protein')} value={totals.protein} />
            <Macro testID="recall-carb" label={t('totals.carb')} value={totals.carb} />
            <Macro testID="recall-fat" label={t('totals.fat')} value={totals.fat} />
          </View>

          <AppText size="sm" style={{ color: colors.text.secondary }}>
            {t('totals.entries', { n: totals.entries })}
          </AppText>

          {/* [R-01], on the payload and therefore on the screen. Two assistants building one
              recall is the requirement, and this is the only place a person can see it
              happening.

              A day with nothing on it says so rather than claiming one contributor: the
              server counts distinct recorders over the entries that still stand, and on an
              empty day that count is nought. */}
          {totals.entries === 0 ? (
            <AppText testID="recall-none" size="xs" style={{ color: colors.text.muted }}>
              {t('totals.none')}
            </AppText>
          ) : totals.shared ? (
            <View
              testID="recall-shared"
              style={{
                backgroundColor: status.normal.surface,
                borderColor: status.normal.border,
                borderWidth: theme.size.borderWidth.thin,
                borderRadius: theme.borderRadius.md,
                padding: theme.spacing['3'],
                gap: theme.spacing['1'],
              }}
            >
              <AppText size="sm" weight="semibold" style={{ color: status.normal.text }}>
                {t('totals.shared', { n: totals.contributors })}
              </AppText>
              <AppText size="xs" style={{ color: status.normal.text }}>
                {t('totals.sharedWhy')}
              </AppText>
            </View>
          ) : (
            <AppText testID="recall-alone" size="xs" style={{ color: colors.text.muted }}>
              {t('totals.alone')}
            </AppText>
          )}
        </>
      )}

      {/* The other tablet's entries arrive with every write — and an operator who has not
          written anything for two minutes has been shown nothing for two minutes. The screen
          re-reads the day by itself; this is for the moment somebody wants to be sure. */}
      <AppButton
        testID="recall-refresh"
        variant="secondary"
        label={t('refresh')}
        onPress={onRefresh}
      />
    </View>
  );
}

function Macro({ testID, label, value }: { testID: string; label: string; value: number }) {
  const { colors } = useTokens();
  return (
    <View style={{ gap: theme.spacing['0.5'] }}>
      <AppText size="xs" style={{ color: colors.text.secondary }}>
        {label}
      </AppText>
      <AppText testID={testID} size="xl" weight="semibold" variant="clinicalValue">
        {String(value)}
      </AppText>
    </View>
  );
}

// --- the content dependency, said out loud ---

/**
 * Nobody has approved this food table.
 *
 * The plan names the composition table as a content dependency needing a national institute's
 * table or an authored one; `approved_at` is null on every seeded row and the API reports it.
 * What ships is a starter list of what this clinic actually sees, and the nutritionist is
 * expected to correct it.
 *
 * Drawn above the form and not behind a tap. A screen that hid this would let a calorie figure
 * computed from an unapproved table look exactly like one computed from an agreed one, which is
 * the difference between a number somebody checks and a number somebody acts on.
 */
function TableNotice({ count }: { count: number }) {
  const t = useTranslations('nutrition');
  const { status } = useTokens();
  return (
    <View
      testID="food-table-notice"
      style={{
        backgroundColor: status.unknown.surface,
        borderColor: status.unknown.border,
        borderWidth: theme.size.borderWidth.thin,
        borderRadius: theme.borderRadius.md,
        padding: theme.spacing['3'],
        gap: theme.spacing['1'],
      }}
    >
      <AppText size="sm" weight="semibold" style={{ color: status.unknown.text }}>
        {t('table.title')}
      </AppText>
      <AppText size="xs" style={{ color: status.unknown.text }}>
        {t('table.body', { n: count })}
      </AppText>
    </View>
  );
}

// --- one entry, being recorded ---

function EntryForm({
  picker,
  rows,
  draft,
  meals,
  choices,
  ceiling,
  missing,
  chosenFood,
  busy,
  onTypeQuery,
  onRetrySearch,
  onChooseMeal,
  onChooseFood,
  onChooseMeasure,
  onTypeQuantity,
  onAdd,
}: {
  picker: PickerState;
  rows: FoodRow[];
  draft: EntryDraft;
  meals: readonly Meal[];
  choices: MeasureChoice[];
  ceiling: number | null;
  missing: Missing[];
  chosenFood: FoodRow | null;
  busy?: boolean;
  onTypeQuery: (text: string) => void;
  onRetrySearch: () => void;
  onChooseMeal: (meal: MealCode) => void;
  onChooseFood: (row: FoodRow) => void;
  onChooseMeasure: (code: string) => void;
  onTypeQuantity: (text: string) => void;
  onAdd: () => void;
}) {
  const t = useTranslations('nutrition');
  const { colors, status } = useTokens();
  const locale = usePreferences((state) => state.language);
  // `form.missing.*` is a key chosen at runtime, and use-intl's key type cannot follow one. The
  // test file asserts every part this code can name has a sentence in both languages.
  const say = t as unknown as (key: string, values?: Record<string, string | number>) => string;

  return (
    <View style={{ gap: theme.spacing['4'] }}>
      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {t('form.title')}
      </AppText>

      {/* The day's meals, from `core.meal`, in the order the day happens — **with the server's
          own names**. This screen holds no meal name in either language and no idea that lunch
          follows breakfast, so an eighth meal added to the clinic's day appears here with no
          change and reads correctly in Bangla the first time. */}
      <View style={{ gap: theme.spacing['1.5'] }}>
        <AppText size="sm" weight="semibold" style={{ color: colors.text.secondary }}>
          {t('form.meal')}
        </AppText>
        <View style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.spacing['2'] }}>
          {meals.map((meal) => {
            const selected = draft.meal === meal.code;
            const name = wordingOf(meal.name_en, meal.name_bn, locale);
            return (
              <Pressable
                key={meal.code}
                testID={`meal-${meal.code}`}
                accessibilityRole="radio"
                accessibilityState={{ selected }}
                accessibilityLabel={name.text}
                onPress={() => onChooseMeal(meal.code)}
                style={{
                  minHeight: theme.size.touchTarget,
                  justifyContent: 'center',
                  paddingHorizontal: theme.spacing['4'],
                  borderRadius: theme.borderRadius.md,
                  borderWidth: selected
                    ? theme.size.borderWidth.thick
                    : theme.size.borderWidth.thin,
                  borderColor: selected ? colors.brand.border : colors.border.control,
                  backgroundColor: selected ? colors.brand.subtle : colors.surface.raised,
                }}
              >
                <AppText
                  weight={selected ? 'semibold' : 'regular'}
                  style={{ color: selected ? colors.brand.text : colors.text.primary }}
                >
                  <ServerText wording={name} />
                </AppText>
              </Pressable>
            );
          })}
        </View>
        {/* No meals at all means the reference call has not answered, and nothing can be
            recorded until it does. Said, rather than left as an empty row somebody taps at. */}
        {meals.length === 0 ? (
          <AppText testID="no-meals" size="sm" style={{ color: status.unknown.text }}>
            {t('form.noMeals')}
          </AppText>
        ) : null}
      </View>

      <FoodPicker
        picker={picker}
        rows={rows}
        chosenCode={draft.foodCode}
        onTypeQuery={onTypeQuery}
        onRetrySearch={onRetrySearch}
        onChooseFood={onChooseFood}
      />

      {chosenFood === null ? null : (
        <>
          {/* Where this food's figures came from, beside the food itself. The standing notice
              says the table is unapproved; this says whose numbers the entry about to be
              recorded will be computed from. */}
          {chosenFood.source === '' ? null : (
            <AppText testID="food-source" size="xs" style={{ color: colors.text.muted }}>
              {t('table.source', { source: chosenFood.source })}
            </AppText>
          )}

          <View style={{ gap: theme.spacing['1.5'] }}>
            <AppText size="sm" weight="semibold" style={{ color: colors.text.secondary }}>
              {t('form.measure')}
            </AppText>
            {/* Only what the table can weigh. Said out loud, because an operator who cannot find
                "bowl" for a food needs to know it is missing from the portion table rather than
                from this screen — and that grams is the way on. */}
            <AppText size="xs" style={{ color: colors.text.muted }}>
              {t('form.measureHint')}
            </AppText>
            {choices.length === 0 ? (
              <AppText testID="no-measures" size="sm" style={{ color: status.unknown.text }}>
                {t('form.noMeasures')}
              </AppText>
            ) : null}
            {choices.length > 0 && !hasEscapeHatch(choices) ? (
              <AppText testID="no-grams" size="sm" style={{ color: status.unknown.text }}>
                {t('form.noGrams')}
              </AppText>
            ) : null}
            <View style={{ gap: theme.spacing['2'] }}>
              {choices.map((choice) => (
                <MeasureRow
                  key={choice.code}
                  choice={choice}
                  selected={draft.measureCode === choice.code}
                  onPress={() => onChooseMeasure(choice.code)}
                />
              ))}
            </View>
          </View>

          <View style={{ gap: theme.spacing['1.5'] }}>
            <AppText size="sm" weight="semibold" style={{ color: colors.text.secondary }}>
              {t('form.quantity')}
            </AppText>
            <View style={{ flexDirection: 'row', alignItems: 'stretch', gap: theme.spacing['2'] }}>
              <TextInput
                testID="quantity-field"
                value={draft.quantity}
                onChangeText={onTypeQuantity}
                keyboardType="decimal-pad"
                // Not `numeric`: on Android that keyboard has no decimal separator on several
                // OEM skins, and an operator who cannot type 1.5 types 15.
                inputMode="decimal"
                accessibilityLabel={t('form.quantity')}
                placeholder="—"
                placeholderTextColor={colors.text.muted}
                style={{
                  flex: 1,
                  minHeight: theme.size.touchTarget,
                  borderRadius: theme.borderRadius.md,
                  borderWidth: theme.size.borderWidth.thin,
                  borderColor: colors.border.control,
                  backgroundColor: colors.surface.raised,
                  color: colors.text.primary,
                  paddingHorizontal: theme.spacing['4'],
                  fontSize: theme.fontSize['3xl'],
                  fontFamily: 'Inter-SemiBold',
                }}
              />
              {/* The four counts that cover most of a recall, one tap each, beside the field
                  rather than instead of it. */}
              {QUANTITY_STEPS.map((step) => {
                const text = String(step);
                const selected = draft.quantity.trim() === text;
                return (
                  <Pressable
                    key={text}
                    testID={`quantity-${text}`}
                    accessibilityRole="radio"
                    accessibilityState={{ selected }}
                    onPress={() => onTypeQuantity(text)}
                    style={{
                      minWidth: theme.size.touchTarget,
                      minHeight: theme.size.touchTarget,
                      alignItems: 'center',
                      justifyContent: 'center',
                      paddingHorizontal: theme.spacing['2'],
                      borderRadius: theme.borderRadius.md,
                      borderWidth: selected
                        ? theme.size.borderWidth.thick
                        : theme.size.borderWidth.thin,
                      borderColor: selected ? colors.brand.border : colors.border.subtle,
                      backgroundColor: selected ? colors.brand.subtle : colors.surface.raised,
                    }}
                  >
                    <AppText weight={selected ? 'semibold' : 'regular'} variant="clinicalValue">
                      {text}
                    </AppText>
                  </Pressable>
                );
              })}
            </View>
          </View>
        </>
      )}

      {/* What is still wanted, named. A disabled button with no sentence beside it is a screen
          that has stopped talking to the person using it. */}
      {missing.length > 0 ? (
        <AppText testID="entry-missing" size="sm" style={{ color: colors.text.secondary }}>
          {/* The ceiling is the server's, off the reference payload, and it travels into the
              sentence rather than being written into it: a hundred cups is nobody's lunch, but
              which number that is belongs to the service and can be tuned there. */}
          {say(`form.missing.${missing[0] as string}`, { max: ceiling ?? 0 })}
        </AppText>
      ) : null}

      <AppButton
        testID="entry-add"
        label={t('form.add')}
        disabled={busy === true || missing.length > 0}
        onPress={onAdd}
      />
    </View>
  );
}

/**
 * The food picker.
 *
 * The list stays on screen with the chosen row marked, rather than collapsing to a chip with a
 * "change" button. Two reasons, and both are taps: picking a different food after a mis-tap is
 * one tap instead of two, and the query stays in the box so a patient who says "no, the fried
 * one" does not cost three letters again.
 *
 * Nothing here re-ranks. The server sorts prefix matches first and breaks ties on trigram
 * similarity over the names *and* the synonyms — which is what makes "roti", "ruti" and "রুটি"
 * one food — and a screen that re-sorted would sink the exact match somebody typed.
 */
function FoodPicker({
  picker,
  rows,
  chosenCode,
  onTypeQuery,
  onRetrySearch,
  onChooseFood,
}: {
  picker: PickerState;
  rows: FoodRow[];
  chosenCode: string;
  onTypeQuery: (text: string) => void;
  onRetrySearch: () => void;
  onChooseFood: (row: FoodRow) => void;
}) {
  const t = useTranslations('nutrition');
  const { colors, status } = useTokens();
  const busy = searching(picker);

  return (
    <View testID="food-picker" style={{ gap: theme.spacing['2'] }}>
      <AppText size="sm" weight="semibold" style={{ color: colors.text.secondary }}>
        {t('form.food')}
      </AppText>
      <TextInput
        testID="food-query"
        value={picker.query}
        onChangeText={onTypeQuery}
        autoCorrect={false}
        autoCapitalize="none"
        accessibilityLabel={t('form.searchLabel')}
        placeholder={t('form.placeholder')}
        placeholderTextColor={colors.text.muted}
        style={{
          // Half again the touch-target floor, as station 4's picker uses. The staff here type
          // standing up beside a patient, and a 48-point box is one they miss.
          minHeight: theme.size.touchTarget * 1.5,
          borderWidth: theme.size.borderWidth.thin,
          borderColor: colors.border.control,
          borderRadius: theme.borderRadius.md,
          paddingHorizontal: theme.spacing['4'],
          backgroundColor: colors.surface.raised,
          color: colors.text.primary,
          fontSize: theme.fontSize.xl,
        }}
      />
      {/* The search matches what people say as well as what the table calls it. Worth one line:
          an operator who does not know that types the formal name and gives up when it is
          spelled differently. */}
      <AppText size="xs" style={{ color: colors.text.muted }}>
        {t('form.searchHint')}
      </AppText>

      {picker.trouble !== null ? (
        <View
          testID="food-picker-trouble"
          style={{
            backgroundColor: status.borderline.surface,
            borderColor: status.borderline.border,
            borderWidth: theme.size.borderWidth.thin,
            borderRadius: theme.borderRadius.md,
            padding: theme.spacing['3'],
            gap: theme.spacing['2'],
          }}
        >
          <AppText size="sm" weight="semibold" style={{ color: status.borderline.text }}>
            {t('form.searchFailed')}
          </AppText>
          {picker.trouble.message === '' ? null : (
            <AppText size="sm" style={{ color: status.borderline.text }}>
              {picker.trouble.message}
            </AppText>
          )}
          <AppButton
            testID="food-retry"
            variant="secondary"
            label={t('trouble.retry')}
            onPress={onRetrySearch}
          />
        </View>
      ) : null}

      {busy ? (
        // A word rather than a spinner alone: a spinner beside a stale list says nothing about
        // which of the two the operator is looking at.
        <AppText testID="food-searching" size="sm" style={{ color: colors.text.muted }}>
          {t('form.searching')}
        </AppText>
      ) : null}

      <ScrollView
        testID="food-results"
        style={{ maxHeight: theme.size.touchTarget * 6 }}
        keyboardShouldPersistTaps="handled"
        contentContainerStyle={{ gap: theme.spacing['2'] }}
        nestedScrollEnabled
      >
        {rows.map((row) => (
          <FoodResult
            key={row.code}
            row={row}
            selected={row.code === chosenCode}
            onPress={() => onChooseFood(row)}
          />
        ))}

        {rows.length === 0 && !busy && picker.trouble === null ? (
          <View testID="food-empty" style={{ gap: theme.spacing['1'] }}>
            <AppText size="base">{t('form.noResults')}</AppText>
            {/* The honest instruction. The table is twenty-four foods and it is meant to grow;
                recording something close enough is how a starter list becomes wrong data. */}
            <AppText size="sm" style={{ color: colors.text.secondary }}>
              {t('form.askForIt')}
            </AppText>
          </View>
        ) : null}

        {rows.length >= MAX_RESULTS ? (
          <AppText testID="food-cap" size="xs" style={{ color: colors.text.muted }}>
            {t('form.atCap', { n: MAX_RESULTS })}
          </AppText>
        ) : null}
      </ScrollView>
    </View>
  );
}

function FoodResult({
  row,
  selected,
  onPress,
}: {
  row: FoodRow;
  selected: boolean;
  onPress: () => void;
}) {
  const t = useTranslations('nutrition');
  const { colors } = useTokens();
  const say = t as unknown as (key: string) => string;

  return (
    <Pressable
      testID={`food-${row.code}`}
      accessibilityRole="radio"
      accessibilityState={{ selected }}
      accessibilityLabel={row.name.text}
      onPress={onPress}
      style={{
        minHeight: theme.size.touchTarget,
        justifyContent: 'center',
        gap: theme.spacing['0.5'],
        paddingHorizontal: theme.spacing['4'],
        paddingVertical: theme.spacing['2'],
        borderRadius: theme.borderRadius.md,
        borderWidth: selected ? theme.size.borderWidth.thick : theme.size.borderWidth.thin,
        borderColor: selected ? colors.brand.border : colors.border.subtle,
        backgroundColor: selected ? colors.brand.subtle : colors.surface.raised,
      }}
    >
      <AppText
        weight={selected ? 'semibold' : 'regular'}
        style={{ color: selected ? colors.brand.text : colors.text.primary }}
      >
        <ServerText wording={row.name} />
        {selected ? ` · ${t('form.chosen')}` : ''}
      </AppText>
      <AppText size="xs" style={{ color: colors.text.muted }}>
        {say(`group.${row.group}`)}
      </AppText>
    </Pressable>
  );
}

/**
 * One measure, with what it weighs and how big it is.
 *
 * The note is the whole reason a household measure is usable at all: "a teacup, packed" and "one
 * medium ruti" are the difference between two operators meaning the same thing by "one cup" and
 * two operators meaning different things. A measure without a size is a measure two people use
 * differently.
 */
function MeasureRow({
  choice,
  selected,
  onPress,
}: {
  choice: MeasureChoice;
  selected: boolean;
  onPress: () => void;
}) {
  const t = useTranslations('nutrition');
  const { colors } = useTokens();

  return (
    <Pressable
      testID={`measure-${choice.code}`}
      accessibilityRole="radio"
      accessibilityState={{ selected }}
      accessibilityLabel={choice.name.text}
      onPress={onPress}
      style={{
        minHeight: theme.size.touchTarget,
        justifyContent: 'center',
        gap: theme.spacing['0.5'],
        paddingHorizontal: theme.spacing['4'],
        paddingVertical: theme.spacing['2'],
        borderRadius: theme.borderRadius.md,
        borderWidth: selected ? theme.size.borderWidth.thick : theme.size.borderWidth.thin,
        borderColor: selected ? colors.brand.border : colors.border.control,
        backgroundColor: selected ? colors.brand.subtle : colors.surface.raised,
      }}
    >
      <AppText
        weight={selected ? 'semibold' : 'regular'}
        style={{ color: selected ? colors.brand.text : colors.text.primary }}
      >
        <ServerText wording={choice.name} />
        {selected ? ` · ${t('form.chosen')}` : ''}
      </AppText>
      {choice.grams === null ? (
        // Grams, which means the same for every food. Named as the escape hatch rather than
        // left as one measure among nine: it is the answer whenever the table cannot weigh
        // what the patient described.
        <AppText size="xs" style={{ color: colors.text.muted }}>
          {t('form.universal')}
        </AppText>
      ) : (
        <AppText size="xs" style={{ color: colors.text.muted }}>
          {t('form.portion', { grams: choice.grams })}
          {choice.note.text === '' ? '' : ` · ${choice.note.text}`}
        </AppText>
      )}
    </Pressable>
  );
}

// --- the day so far ---

function Day({
  groups,
  justAdded,
  withdrawal,
  busy,
  onOpenWithdrawal,
  onTypeReason,
  onWithdraw,
  onCancelWithdrawal,
}: {
  groups: MealGroup[];
  justAdded: string;
  withdrawal: WithdrawalDraft | null;
  busy?: boolean;
  onOpenWithdrawal: (entryId: string) => void;
  onTypeReason: (text: string) => void;
  onWithdraw: () => void;
  onCancelWithdrawal: () => void;
}) {
  const t = useTranslations('nutrition');
  const { colors } = useTokens();

  return (
    <View testID="recall-day-list" style={{ gap: theme.spacing['4'] }}>
      <View style={{ gap: theme.spacing['1'] }}>
        <AppText size="sm" weight="semibold" style={{ color: colors.text.secondary }}>
          {t('day.list')}
        </AppText>
        {/* Said once, above the list, because the control on every row is the surprising part.
            An operator who assumed they could only remove their own would leave a colleague's
            duplicate standing. */}
        <AppText size="xs" style={{ color: colors.text.muted }}>
          {t('day.anyoneMayTakeBack')}
        </AppText>
      </View>

      {groups.length === 0 ? (
        <AppText testID="day-empty" size="sm" style={{ color: colors.text.muted }}>
          {t('day.empty')}
        </AppText>
      ) : null}

      {groups.map((group) => (
        <View key={group.meal} style={{ gap: theme.spacing['2'] }}>
          {/* The server's name for the meal, decided in `mealGroups` and only drawn here. */}
          <AppText size="sm" weight="semibold">
            <ServerText wording={group.name} />
          </AppText>
          {/* A meal the reference payload did not name — in practice, a tablet whose measures
              call failed. The entries are still here under their code, because a patient's
              dinner disappearing because a lookup failed is worse than a heading somebody has
              to squint at. */}
          {group.known ? null : (
            <AppText
              testID={`meal-${group.meal}-unnamed`}
              size="xs"
              style={{ color: colors.text.muted }}
            >
              {t('day.mealNotNamed')}
            </AppText>
          )}
          {group.lines.map((line) => (
            <EntryRow
              key={line.id}
              line={line}
              mine={justAdded !== '' && line.id === justAdded}
              withdrawal={withdrawal?.entryId === line.id ? withdrawal : null}
              busy={busy}
              onOpenWithdrawal={onOpenWithdrawal}
              onTypeReason={onTypeReason}
              onWithdraw={onWithdraw}
              onCancelWithdrawal={onCancelWithdrawal}
            />
          ))}
        </View>
      ))}
    </View>
  );
}

/**
 * One thing the patient said they ate, and who wrote it down.
 *
 * A withdrawn entry **stays on the day**, struck through, with its reason and both names. It is
 * not tidied away: at a station where two people are entering one recall, a row that vanished
 * would leave the other operator wondering whether they imagined recording it — and the record
 * of what was said is a record of what was said, corrections included.
 *
 * The strike-through is never the only signal. The row says "taken back" in words and carries
 * the reason underneath, for the reader who cannot see a thin line through small text on a
 * tablet in the sun.
 */
function EntryRow({
  line,
  mine,
  withdrawal,
  busy,
  onOpenWithdrawal,
  onTypeReason,
  onWithdraw,
  onCancelWithdrawal,
}: {
  line: EntryLine;
  /** The row this tablet's last write produced. Marked, because the day is grouped by meal. */
  mine: boolean;
  withdrawal: WithdrawalDraft | null;
  busy?: boolean;
  onOpenWithdrawal: (entryId: string) => void;
  onTypeReason: (text: string) => void;
  onWithdraw: () => void;
  onCancelWithdrawal: () => void;
}) {
  const t = useTranslations('nutrition');
  const { colors, status } = useTokens();

  return (
    <View
      testID={`entry-${line.id}`}
      style={{
        backgroundColor: line.withdrawn ? colors.surface.sunken : colors.surface.raised,
        borderRadius: theme.borderRadius.lg,
        borderWidth:
          mine && !line.withdrawn ? theme.size.borderWidth.thick : theme.size.borderWidth.thin,
        borderColor: mine && !line.withdrawn ? colors.brand.border : colors.border.subtle,
        padding: theme.spacing['4'],
        gap: theme.spacing['2'],
      }}
    >
      {/* Said in a word as well as drawn in a border. The day is grouped by meal, so the food
          just added can land three groups above where the operator is looking — and among two
          identical rows from two tablets, nothing else says which is theirs. */}
      {mine && !line.withdrawn ? (
        <AppText
          testID={`entry-${line.id}-mine`}
          size="xs"
          weight="semibold"
          style={{ color: colors.brand.text }}
        >
          {t('entry.justAdded')}
        </AppText>
      ) : null}
      <AppText
        weight="semibold"
        style={{
          color: line.withdrawn ? colors.text.muted : colors.text.primary,
          textDecorationLine: line.withdrawn ? 'line-through' : 'none',
        }}
      >
        <ServerText wording={line.food} />
      </AppText>

      {/* The answer as the patient gave it, and the weight the table says that is. Both,
          because the count and the measure are the evidence and the grams are an
          interpretation of them — and the energy is the server's arithmetic on the grams. */}
      <AppText
        size="sm"
        style={{
          color: colors.text.secondary,
          textDecorationLine: line.withdrawn ? 'line-through' : 'none',
        }}
      >
        {t('entry.line', {
          quantity: line.entry.quantity,
          measure: line.measure.text,
          grams: line.entry.grams,
          kcal: line.entry.kcal,
        })}
      </AppText>

      {line.withdrawn ? (
        <View
          testID={`entry-${line.id}-withdrawn`}
          style={{
            backgroundColor: status.unknown.surface,
            borderColor: status.unknown.border,
            borderWidth: theme.size.borderWidth.thin,
            borderRadius: theme.borderRadius.md,
            padding: theme.spacing['3'],
            gap: theme.spacing['1'],
          }}
        >
          {/* The word, not the line through the text. */}
          <AppText size="sm" weight="semibold" style={{ color: status.unknown.text }}>
            {t('entry.withdrawn')}
          </AppText>
          {line.reason === '' ? null : (
            <AppText size="sm" style={{ color: status.unknown.text }}>
              {t('entry.reason', { reason: line.reason })}
            </AppText>
          )}
        </View>
      ) : null}

      {/* CP61. Both names: whoever recorded it, and — through the correction on the provenance
          — whoever took it back. At this station those are routinely two different people, and
          "which of us wrote this" is the first question anybody asks of a row they did not
          write. */}
      <EnteredBy
        compact
        testID={`entry-${line.id}-entered-by`}
        provenance={ofDietEntry(line.entry)}
      />

      {line.mayWithdraw && withdrawal === null ? (
        <AppButton
          testID={`entry-${line.id}-withdraw`}
          variant="secondary"
          disabled={busy === true}
          label={t('withdraw.open')}
          onPress={() => onOpenWithdrawal(line.id)}
        />
      ) : null}

      {withdrawal === null ? null : (
        <View testID={`entry-${line.id}-withdraw-form`} style={{ gap: theme.spacing['2'] }}>
          <AppText size="sm" weight="semibold">
            {t('withdraw.title')}
          </AppText>
          {/* Required, and required for a reason: an entry that vanishes with no explanation is
              a gap the *other* operator has to account for, and at this station there is always
              another operator. */}
          <AppText size="xs" style={{ color: colors.text.muted }}>
            {t('withdraw.why')}
          </AppText>

          <View style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.spacing['2'] }}>
            {REASON_PRESETS.map((preset) => (
              <Pressable
                key={preset}
                testID={`withdraw-preset-${preset}`}
                accessibilityRole="button"
                // The preset fills the box; it does not send. There is exactly one path by
                // which a reason reaches the server, and the operator can edit it first.
                onPress={() => onTypeReason(t(`reason.${preset}`))}
                style={{
                  minHeight: theme.size.touchTarget,
                  justifyContent: 'center',
                  paddingHorizontal: theme.spacing['4'],
                  borderRadius: theme.borderRadius.md,
                  borderWidth: theme.size.borderWidth.thin,
                  borderColor: colors.border.control,
                  backgroundColor: colors.surface.raised,
                }}
              >
                <AppText size="sm">{t(`reason.${preset}`)}</AppText>
              </Pressable>
            ))}
          </View>

          <TextInput
            testID={`entry-${line.id}-reason`}
            value={withdrawal.reason}
            onChangeText={onTypeReason}
            accessibilityLabel={t('withdraw.reasonLabel')}
            placeholder={t('withdraw.placeholder')}
            placeholderTextColor={colors.text.muted}
            multiline
            style={{
              minHeight: theme.size.touchTarget,
              borderWidth: theme.size.borderWidth.thin,
              borderColor: colors.border.control,
              borderRadius: theme.borderRadius.md,
              paddingHorizontal: theme.spacing['4'],
              paddingVertical: theme.spacing['2'],
              backgroundColor: colors.surface.raised,
              color: colors.text.primary,
              fontSize: theme.fontSize.base,
            }}
          />

          <AppButton
            testID={`entry-${line.id}-withdraw-confirm`}
            label={t('withdraw.confirm')}
            disabled={busy === true || withdrawal.reason.trim() === ''}
            onPress={onWithdraw}
          />
          <AppButton
            testID={`entry-${line.id}-withdraw-cancel`}
            variant="secondary"
            label={t('withdraw.cancel')}
            onPress={onCancelWithdrawal}
          />
        </View>
      )}
    </View>
  );
}

// --- what went wrong ---

function TroubleBanner({
  trouble,
  onRefresh,
  onRetry,
}: {
  trouble: Trouble;
  onRefresh: () => void;
  onRetry: () => void;
}) {
  const t = useTranslations('nutrition');
  // The same cast the counselling and lifestyle screens use: `troubleKey` returns a key this
  // component does not enumerate, and use-intl's key type cannot follow a value chosen at
  // runtime. The test file asserts every key this code can produce exists in both languages.
  const say = t as unknown as (key: string, values?: Record<string, string>) => string;
  const { status } = useTokens();
  const advice = adviceFor(trouble);

  return (
    <View
      testID="nutrition-trouble"
      style={{
        backgroundColor: status.borderline.surface,
        borderColor: status.borderline.border,
        borderWidth: theme.size.borderWidth.thin,
        borderRadius: theme.borderRadius.md,
        padding: theme.spacing['3'],
        gap: theme.spacing['2'],
      }}
    >
      <AppText size="sm" weight="semibold" style={{ color: status.borderline.text }}>
        {say(troubleKey(trouble))}
      </AppText>
      {trouble.message === '' ? null : (
        <AppText size="sm" style={{ color: status.borderline.text }}>
          {trouble.message}
        </AppText>
      )}
      {/* The table cannot weigh that measure, so the way on is the one measure that always
          works. Said rather than left for the operator to work out from a field name. */}
      {advice === 'grams' ? (
        <AppText testID="nutrition-use-grams" size="sm" style={{ color: status.borderline.text }}>
          {t('trouble.useGrams')}
        </AppText>
      ) : null}
      {advice === 'refresh' ? (
        <AppButton
          testID="nutrition-trouble-refresh"
          variant="secondary"
          label={t('refresh')}
          onPress={onRefresh}
        />
      ) : null}
      {advice === 'retry' ? (
        <AppButton
          testID="nutrition-trouble-retry"
          variant="secondary"
          label={t('trouble.retry')}
          onPress={onRetry}
        />
      ) : null}
    </View>
  );
}

// --- shared pieces ---

/**
 * Text the server wrote, with a line when it is not in the reader's language.
 *
 * The fallback happens **and says so**. A Bangla-reading nutritionist handed an English food
 * name with no explanation is one who thinks the app switched languages on them, and who may
 * read it aloud wrongly rather than admit they could not read it.
 */
function ServerText({ wording }: { wording: Wording }) {
  const t = useTranslations('nutrition');
  if (wording.text === '') return <>{t('noWording')}</>;
  if (wording.ownLanguage) return <>{wording.text}</>;
  return <>{`${wording.text} · ${t('inOtherLanguage')}`}</>;
}
