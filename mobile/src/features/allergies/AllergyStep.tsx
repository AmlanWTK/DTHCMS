import type { ReactNode } from 'react';
import { Pressable, TextInput, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppButton } from '@/components/AppButton';
import { AppText } from '@/components/AppText';
import { ConceptPicker, type Concept, type PickerState } from '@/features/terminology';
import { theme, useTokens } from '@/lib/tokens';
import { usePreferences } from '@/stores/preferences';

import {
  ANSWERS,
  CERTAINTIES,
  SEVERITIES,
  allergyRows,
  canAssert,
  canRecord,
  changeRows,
  isPristine,
  missingFrom,
  needsReason,
  problemsWith,
  reactionLabel,
  reactionsInOrder,
  readingOf,
  refusalOn,
  withdrawalRefused,
  type Allergy,
  type AllergyChange,
  type AllergyDraft,
  type AllergyReaction,
  type AllergyState,
  type Answer,
  type AssertionKind,
  type Certainty,
  type Severity,
} from './state';
import type { Trouble } from './api';

/**
 * Station 4's allergy hard stop, on the station screen (CP54, §3 step 4, [R-01]).
 *
 * # Everything here is arrangement; every decision is in `state.ts`
 *
 * The same split as every other station: this component cannot be rendered outside a device,
 * so anything it decided would be a decision nobody checks. What it holds is where things sit
 * — which matters here more than usual, because this question gets a few seconds while a queue
 * waits, and a control that is one scroll too far down is a control nobody uses.
 *
 * # It says the status in words before it draws a list
 *
 * The headline is the status, the sentence under it is what the status means, and the list
 * comes third. That order is the whole point: `NONE_RECORDED` and `NO_KNOWN_ALLERGY` both draw
 * an empty list, and a screen that led with the list would present the two as the same fact.
 * The sentence in the empty space is chosen by the status — never by the length of the list —
 * so "nobody has asked" and "asked, and there are none" can never come out as the same words.
 *
 * # The three answers are one row of three equal controls
 *
 * Record an allergy, no known allergies, could not be assessed. Same size, same place, none of
 * them styled as the easy one, and the countdown beside the first says exactly what it costs:
 * a substance, a reaction, how bad, how sure. That is the plan's risk note answered in layout —
 * if the assertion were a large button and recording were a form behind a link, operators would
 * learn the shape of the button, and the record would fill with claims instead of findings.
 *
 * # There is no fourth control
 *
 * No skip, no proceed, no "record later". The gate is a trigger on the queue table and this
 * screen cannot get past it; what it can do is say plainly, in the largest words in the
 * blocked banner, why the patient cannot be sent on and what would answer it. An operator who
 * needs to move an unconscious patient has an honest answer — *could not be assessed*, with a
 * reason — and it is on this screen, the same size as the other two.
 *
 * # No state is carried by colour alone
 *
 * Every state says what it is in words: blocked or answered, emergency or not, coded or
 * uncoded, withdrawn or standing. Roughly one man in twelve who will work here cannot rely on
 * the colour, and direct sun through the clinic's windows flattens it for everybody else.
 */
export function AllergyStep({
  state,
  reactions,
  changes,
  answering,
  draft,
  picker,
  reason,
  withdrawing,
  withdrawReason,
  busy,
  justWrote,
  trouble,
  onChooseAnswer,
  onEditDraft,
  onPickerQuery,
  onPickerSelect,
  onPickerClear,
  onPickerRetry,
  onRecord,
  onChangeReason,
  onAssert,
  onStartWithdraw,
  onChangeWithdrawReason,
  onWithdraw,
  onRetry,
}: {
  /** The server's own answer, or null until it has arrived. Never assembled on this side. */
  state: AllergyState | null;
  reactions: readonly AllergyReaction[];
  /** Everything ever said, withdrawn entries included. Newest first, as the server sent it. */
  changes: readonly AllergyChange[];
  /** Which of the three answers is open, or null. There is no fourth value. */
  answering: Answer | null;
  draft: AllergyDraft;
  /** The coded-terminology picker (CP52), opened on the clinic's own `DTHC` dictionary. */
  picker: PickerState | null;
  /** The reason for an `UNABLE_TO_ASSESS`. Refused empty; refused at all on the other. */
  reason: string;
  /** What is being taken back, or null. Only ever one at a time. */
  withdrawing: WithdrawTarget | null;
  withdrawReason: string;
  busy?: boolean;
  /** True for a moment after a write lands, so the officer can see the press took. */
  justWrote?: boolean;
  trouble?: Trouble | null;
  onChooseAnswer: (answer: Answer | null) => void;
  onEditDraft: (patch: Partial<AllergyDraft>) => void;
  onPickerQuery: (text: string) => void;
  onPickerSelect: (concept: Concept) => void;
  onPickerClear: () => void;
  onPickerRetry: () => void;
  onRecord: () => void;
  onChangeReason: (text: string) => void;
  onAssert: (kind: AssertionKind) => void;
  onStartWithdraw: (target: WithdrawTarget | null) => void;
  onChangeWithdrawReason: (text: string) => void;
  onWithdraw: (target: WithdrawTarget) => void;
  onRetry: () => void;
}) {
  const t = useTranslations('allergies');
  const { colors, status } = useTokens();
  const locale = usePreferences((preference) => preference.language);

  // A status, a refusal and a field label each name their own sentence, so the key is a value
  // rather than a literal and `useTranslations` cannot type it — the same cast the other
  // stations use, with the same guarantee behind it: the test file asserts every key this code
  // can produce exists in both languages.
  const say = t as unknown as (key: string, values?: Record<string, string>) => string;

  if (state === null) {
    return (
      <View testID="allergy-step" style={{ gap: theme.spacing['2'] }}>
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {t('step')}
        </AppText>
        {/* Not an empty list. A screen that drew nothing here while the answer was in
            flight would read as "no allergies" for as long as the clinic's link takes. */}
        <AppText size="base">{t('loading')}</AppText>
      </View>
    );
  }

  const reading = readingOf(state, reactions);
  const tone = status[reading.tone];
  const rows = allergyRows(reading.allergies, reactions, locale);
  const missing = missingFrom(draft, reactions);
  // Nothing is corrected at an officer who has not started; a refusal that was on screen
  // before the first tap is furniture by the time it means something.
  const refusals = isPristine(draft) ? [] : problemsWith(draft, reactions);
  const assertion = reading.assertion;

  return (
    <View testID="allergy-step" style={{ gap: theme.spacing['4'] }}>
      {/* The status, in words, before anything else on the screen. */}
      <View
        testID="allergy-status"
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
          {t('step')}
        </AppText>
        <AppText testID="allergy-headline" size="xl" weight="bold" style={{ color: tone.text }}>
          {say(reading.headline)}
        </AppText>
        <AppText size="base">{say(reading.meaning)}</AppText>

        {reading.emergency ? (
          // A property of the reaction, not of the severity somebody ticked beside it.
          <AppText size="base" weight="semibold" style={{ color: status.critical.text }}>
            {t('emergency')}
          </AppText>
        ) : null}

        {/* The refusal, said out loud and said early. The officer should not meet it as a
            failure at the queue after the patient has stood up. */}
        <AppText
          testID="allergy-gate"
          size="base"
          weight="semibold"
          style={{ color: reading.blocked ? status.critical.text : colors.text.primary }}
        >
          {reading.blocked ? t('gate.blocked') : t('gate.open')}
        </AppText>
        <AppText size="xs" style={{ color: colors.text.muted }}>
          {t('gate.hint')}
        </AppText>
      </View>

      {/* The list, in the server's order: emergency reactions first, then by severity. It is
          never re-sorted here — see `allergyRows`. */}
      <Section title={t('recorded')} testID="allergy-list">
        {rows.length === 0 ? (
          // The sentence is chosen by the status. Four statuses, four sentences, and no path
          // that renders an empty list as "no allergies".
          <AppText testID="allergy-empty" size="base">
            {say(reading.empty)}
          </AppText>
        ) : (
          rows.map((row) => (
            <AllergyRowView
              key={row.allergy.id}
              row={row}
              say={say}
              busy={busy === true}
              withdrawing={withdrawing?.kind === 'allergy' && withdrawing.id === row.allergy.id}
              withdrawReason={withdrawReason}
              onStartWithdraw={onStartWithdraw}
              onChangeWithdrawReason={onChangeWithdrawReason}
              onWithdraw={() => onWithdraw({ kind: 'allergy', id: row.allergy.id })}
            />
          ))
        )}
      </Section>

      {assertion !== null ? (
        <Section title={t('standing')} testID="allergy-assertion">
          <AppText size="base" weight="semibold">
            {say(`status.${assertion.kind}`)}
          </AppText>
          {/* The reason is the whole worth of the third state: it is the thing that makes it
              reviewable rather than a silent gap wearing a label. */}
          {(assertion.reason ?? '').trim() !== '' ? (
            <AppText size="base">{assertion.reason}</AppText>
          ) : null}
          <AppText size="xs" style={{ color: colors.text.muted }}>
            {t('assertedAt', { when: assertion.asserted_at })}
            {(assertion.asserted_role ?? '').trim() === ''
              ? ''
              : ` · ${t('assertedBy', { role: assertion.asserted_role as string })}`}
          </AppText>

          <AppText size="xs" style={{ color: colors.text.muted }}>
            {t('withdrawClosesGate')}
          </AppText>
          <Withdrawal
            testID={`allergy-withdraw-assertion-${assertion.id}`}
            open={withdrawing?.kind === 'assertion' && withdrawing.id === assertion.id}
            reason={withdrawReason}
            busy={busy === true}
            onOpen={(open) =>
              onStartWithdraw(open ? { kind: 'assertion', id: assertion.id } : null)
            }
            onChangeReason={onChangeWithdrawReason}
            onWithdraw={() => onWithdraw({ kind: 'assertion', id: assertion.id })}
          />
        </Section>
      ) : null}

      <Section title={t('answer.title')} testID="allergy-answers">
        {/* Three ways to answer, and no fourth. Said out loud, because the absence of a
            "skip" is otherwise read as an oversight and somebody adds one. */}
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {t('answer.hint')}
        </AppText>
        <View
          accessibilityRole="radiogroup"
          style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.spacing['2'] }}
        >
          {ANSWERS.map((option) => (
            <Choice
              key={option}
              testID={`allergy-answer-${option}`}
              label={say(`answer.${option}`)}
              selected={answering === option}
              // Equal weight, equal size, same row. Whichever is cheapest to press is the one
              // the clinic's record fills up with.
              grow
              onPress={() => onChooseAnswer(answering === option ? null : option)}
            />
          ))}
        </View>
        <AppText size="xs" style={{ color: colors.text.muted }}>
          {t('inYourName')}
        </AppText>

        {answering === 'ALLERGY' ? (
          <NewAllergy
            draft={draft}
            reactions={reactions}
            picker={picker}
            missing={missing}
            refusals={refusals}
            locale={locale}
            say={say}
            busy={busy === true}
            justWrote={justWrote === true}
            onEditDraft={onEditDraft}
            onPickerQuery={onPickerQuery}
            onPickerSelect={onPickerSelect}
            onPickerClear={onPickerClear}
            onPickerRetry={onPickerRetry}
            onRecord={onRecord}
          />
        ) : null}

        {answering === 'NO_KNOWN_ALLERGY' || answering === 'UNABLE_TO_ASSESS' ? (
          <Assertion
            kind={answering}
            reason={reason}
            busy={busy === true}
            justWrote={justWrote === true}
            say={say}
            onChangeReason={onChangeReason}
            onAssert={() => onAssert(answering)}
          />
        ) : null}
      </Section>

      <Section title={t('changes')} testID="allergy-changes">
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {t('changesHint')}
        </AppText>
        {changes.length === 0 ? (
          <AppText size="base">{t('noChanges')}</AppText>
        ) : (
          changeRows(changes).map((row) => (
            <View key={`${row.kind}-${row.change.id}`} style={{ gap: theme.spacing['0.5'] }}>
              <AppText size="sm" weight="semibold">
                {say(`changeKind.${row.kind}`)}
                {row.label === '' ? '' : ` · ${row.label}`}
              </AppText>
              {row.detail === '' ? null : (
                <AppText size="sm" style={{ color: colors.text.secondary }}>
                  {row.detail}
                </AppText>
              )}
              {/* Both halves. Somebody believed it and somebody else disagreed, and the next
                  clinician needs to know that before writing a prescription. */}
              {row.withdrawn ? (
                <AppText size="xs" weight="semibold" style={{ color: status.borderline.text }}>
                  {t('withdrawn')}
                  {row.why === '' ? '' : ` · ${row.why}`}
                </AppText>
              ) : null}
              <AppText size="xs" style={{ color: colors.text.muted }}>
                {row.change.at}
              </AppText>
            </View>
          ))
        )}
      </Section>

      {trouble !== null && trouble !== undefined ? (
        <View
          testID="allergy-trouble"
          style={{
            gap: theme.spacing['2'],
            borderRadius: theme.borderRadius.lg,
            borderWidth: 1,
            borderColor: status.critical.border,
            backgroundColor: status.critical.surface,
            padding: theme.spacing['4'],
          }}
        >
          <AppText size="sm" weight="semibold" style={{ color: status.critical.text }}>
            {trouble.kind === 'refused'
              ? t('trouble.refused')
              : trouble.kind === 'unreachable'
                ? t('trouble.unreachable')
                : t('trouble.failed')}
          </AppText>
          {/* The server's own sentence where there is one. */}
          {trouble.kind !== 'unreachable' && trouble.message !== '' ? (
            <AppText size="base">{trouble.message}</AppText>
          ) : null}
          {/* What it costs, said plainly. There is no way round it offered here, because
              there is no way round it. */}
          <AppText size="sm">{t('troubleHint')}</AppText>
          <AppButton
            testID="allergy-retry"
            variant="secondary"
            label={t('retry')}
            onPress={onRetry}
          />
        </View>
      ) : null}
    </View>
  );
}

/** What is being taken back. Never a deletion, and never more than one at a time. */
export interface WithdrawTarget {
  kind: 'allergy' | 'assertion';
  id: string;
}

type Say = (key: string, values?: Record<string, string>) => string;

/**
 * One recorded allergy.
 *
 * The substance, then what it did, then how bad and how sure — the order a prescriber reads
 * it in. An emergency reaction says so in words above the severity, because the two are
 * different facts and the reaction's is the one that does not depend on anybody's judgement.
 */
function AllergyRowView({
  row,
  say,
  busy,
  withdrawing,
  withdrawReason,
  onStartWithdraw,
  onChangeWithdrawReason,
  onWithdraw,
}: {
  row: ReturnType<typeof allergyRows>[number];
  say: Say;
  busy: boolean;
  withdrawing: boolean;
  withdrawReason: string;
  onStartWithdraw: (target: WithdrawTarget | null) => void;
  onChangeWithdrawReason: (text: string) => void;
  onWithdraw: () => void;
}) {
  const t = useTranslations('allergies');
  const { colors, status } = useTokens();
  const allergy: Allergy = row.allergy;

  return (
    <View
      testID={`allergy-${allergy.id}`}
      style={{
        gap: theme.spacing['2'],
        padding: theme.spacing['4'],
        borderRadius: theme.borderRadius.lg,
        borderWidth: 1,
        borderColor: row.emergency ? status.critical.border : colors.border.subtle,
        backgroundColor: colors.surface.raised,
      }}
    >
      <AppText size="lg" weight="semibold">
        {row.label}
      </AppText>

      {row.coding !== null ? (
        // All three parts. A chip showing only the code would teach the clinic that a code is
        // a coding, which is the mistake the coding rule exists for.
        <AppText size="sm" variant="clinicalValue" style={{ color: colors.text.secondary }}>
          {t('coding', {
            system: row.coding.system,
            code: row.coding.code,
            version: row.coding.version,
          })}
        </AppText>
      ) : (
        <AppText size="xs" weight="semibold" style={{ color: status.borderline.text }}>
          {t('uncoded')}
        </AppText>
      )}

      {/* Kept beside a coded allergy too: "the yellow tablet from the pharmacy near the
          bridge" is sometimes the only thing that identifies what actually happened. */}
      {row.said !== '' && row.coding !== null ? (
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {t('said')}: {row.said}
        </AppText>
      ) : null}

      <View style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.spacing['3'] }}>
        <AppText size="base" weight="semibold">
          {row.reaction}
        </AppText>
        {row.emergency ? (
          <AppText size="base" weight="semibold" style={{ color: status.critical.text }}>
            {t('emergency')}
          </AppText>
        ) : null}
      </View>

      <View style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.spacing['3'] }}>
        <AppText size="sm" weight="semibold" style={{ color: colors.text.secondary }}>
          {say(`severity.${row.severity}`)}
        </AppText>
        {/* A suspected reaction thirty years ago and a confirmed anaphylaxis are both worth
            recording and they are not the same warning. */}
        <AppText size="sm" weight="semibold" style={{ color: colors.text.secondary }}>
          {say(`certainty.${row.certainty}`)}
        </AppText>
      </View>

      {(allergy.note ?? '').trim() === '' ? null : <AppText size="sm">{allergy.note}</AppText>}

      <Withdrawal
        testID={`allergy-withdraw-${allergy.id}`}
        open={withdrawing}
        reason={withdrawReason}
        busy={busy}
        onOpen={(open) => onStartWithdraw(open ? { kind: 'allergy', id: allergy.id } : null)}
        onChangeReason={onChangeWithdrawReason}
        onWithdraw={onWithdraw}
      />
    </View>
  );
}

/**
 * Taking something back, with a reason that is not optional.
 *
 * One component for both halves, because they are the same act on two rows and a second copy
 * is a second place the reason could be forgotten. Nothing is deleted: the row stays, the
 * reason is attached, and both halves show in the change history.
 */
function Withdrawal({
  testID,
  open,
  reason,
  busy,
  onOpen,
  onChangeReason,
  onWithdraw,
}: {
  testID: string;
  open: boolean;
  reason: string;
  busy: boolean;
  onOpen: (open: boolean) => void;
  onChangeReason: (text: string) => void;
  onWithdraw: () => void;
}) {
  const t = useTranslations('allergies');
  const { colors, status } = useTokens();

  return (
    <View style={{ gap: theme.spacing['2'] }}>
      <AppButton
        testID={`${testID}-open`}
        variant="secondary"
        label={t('withdrawOpen')}
        disabled={busy}
        onPress={() => onOpen(!open)}
      />
      {open ? (
        <View
          testID={testID}
          style={{
            gap: theme.spacing['2'],
            padding: theme.spacing['3'],
            borderRadius: theme.borderRadius.md,
            borderWidth: 1,
            borderColor: status.critical.border,
            backgroundColor: status.critical.surface,
          }}
        >
          <AppText size="sm" weight="semibold" style={{ color: status.critical.text }}>
            {t('withdrawHint')}
          </AppText>
          <TextInput
            testID={`${testID}-reason`}
            value={reason}
            onChangeText={onChangeReason}
            accessibilityLabel={t('withdrawReason')}
            placeholder={t('withdrawReason')}
            placeholderTextColor={colors.text.muted}
            multiline
            style={box(colors)}
          />
          <View style={{ flexDirection: 'row', gap: theme.spacing['2'] }}>
            <AppButton
              testID={`${testID}-confirm`}
              label={t('withdraw')}
              // The reason is the point of the endpoint, so an empty one never leaves here.
              disabled={busy || withdrawalRefused(reason)}
              onPress={onWithdraw}
            />
            <AppButton
              testID={`${testID}-cancel`}
              variant="secondary"
              label={t('withdrawCancel')}
              onPress={() => onOpen(false)}
            />
          </View>
        </View>
      ) : null}
    </View>
  );
}

/**
 * Recording one allergy: a substance, a reaction, how bad, how sure.
 *
 * Four answers and every one of them a tap. The picker opens on the clinic's own favourites,
 * so the common allergens cost no keystrokes; the reaction vocabulary is short on purpose;
 * severity and certainty are chip rows. The countdown at the top is what makes the cost
 * visible before the officer starts, and what makes the promise checkable: recording is four
 * taps, which is the same order of effort as the answer that claims there is nothing to record.
 *
 * Nothing is pre-selected. A severity nobody chose is a finding this form invented, and on
 * this screen the form's inventions reach a prescriber.
 */
function NewAllergy({
  draft,
  reactions,
  picker,
  missing,
  refusals,
  locale,
  say,
  busy,
  justWrote,
  onEditDraft,
  onPickerQuery,
  onPickerSelect,
  onPickerClear,
  onPickerRetry,
  onRecord,
}: {
  draft: AllergyDraft;
  reactions: readonly AllergyReaction[];
  picker: PickerState | null;
  missing: ReturnType<typeof missingFrom>;
  refusals: ReturnType<typeof problemsWith>;
  locale: 'en' | 'bn';
  say: Say;
  busy: boolean;
  justWrote: boolean;
  onEditDraft: (patch: Partial<AllergyDraft>) => void;
  onPickerQuery: (text: string) => void;
  onPickerSelect: (concept: Concept) => void;
  onPickerClear: () => void;
  onPickerRetry: () => void;
  onRecord: () => void;
}) {
  const t = useTranslations('allergies');
  const { colors, status } = useTokens();

  const problem = (where: Parameters<typeof refusalOn>[1]) => {
    const found = refusalOn(refusals, where);
    if (found === null) return null;
    return (
      <AppText
        testID={`allergy-problem-${where}`}
        size="sm"
        style={{ color: status.critical.text }}
      >
        {say(`problem.${found}`)}
      </AppText>
    );
  };

  return (
    <View style={{ gap: theme.spacing['4'] }}>
      {/* What this answer still costs, before the officer starts paying it. */}
      <View style={{ gap: theme.spacing['1'] }}>
        <AppText testID="allergy-missing" size="sm" weight="semibold">
          {missing.length === 0
            ? t('nothingMissing')
            : t('stillNeeded', { n: String(missing.length) })}
        </AppText>
        <View style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.spacing['2'] }}>
          {missing.map((field) => (
            <AppText key={field} size="xs" style={{ color: colors.text.muted }}>
              {say(`field.${field}`)}
            </AppText>
          ))}
        </View>
      </View>

      <Field label={say('field.substance')}>
        {picker !== null ? (
          <ConceptPicker
            state={picker}
            onChangeQuery={onPickerQuery}
            onSelect={onPickerSelect}
            onClearSelection={onPickerClear}
            onRetry={onPickerRetry}
          />
        ) : null}
        {problem('substance')}
        <TextInput
          testID="allergy-said"
          value={draft.said}
          onChangeText={(text) => onEditDraft({ said: text })}
          accessibilityLabel={t('said')}
          placeholder={t('said')}
          placeholderTextColor={colors.text.muted}
          multiline
          style={box(colors)}
        />
        {/* The escape hatch, said out loud rather than discovered. An allergy nobody could
            code is far more dangerous in a note field than it is here, marked and visible. */}
        <AppText size="xs" style={{ color: colors.text.muted }}>
          {t('uncodedHint')}
        </AppText>
      </Field>

      <Field label={say('field.reaction')}>
        <View
          accessibilityRole="radiogroup"
          style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.spacing['2'] }}
        >
          {reactionsInOrder(reactions).map((reaction: AllergyReaction) => (
            <Choice
              key={reaction.reaction}
              testID={`allergy-reaction-${reaction.reaction}`}
              label={reactionLabel(reaction, locale)}
              selected={draft.reaction === reaction.reaction}
              // The emergency ones say so on the chip, in words: it is a property of the
              // reaction, and the officer chooses it before any severity is ticked.
              note={reaction.is_emergency ? t('emergency') : undefined}
              onPress={() =>
                onEditDraft({
                  reaction: draft.reaction === reaction.reaction ? '' : reaction.reaction,
                })
              }
            />
          ))}
        </View>
        {problem('reaction')}
      </Field>

      <Field label={say('field.severity')}>
        <View
          accessibilityRole="radiogroup"
          style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.spacing['2'] }}
        >
          {SEVERITIES.map((severity: Severity) => (
            <Choice
              key={severity}
              testID={`allergy-severity-${severity}`}
              label={say(`severity.${severity}`)}
              selected={draft.severity === severity}
              onPress={() => onEditDraft({ severity: draft.severity === severity ? '' : severity })}
            />
          ))}
        </View>
        {problem('severity')}
      </Field>

      <Field label={say('field.certainty')}>
        <View
          accessibilityRole="radiogroup"
          style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.spacing['2'] }}
        >
          {CERTAINTIES.map((certainty: Certainty) => (
            <Choice
              key={certainty}
              testID={`allergy-certainty-${certainty}`}
              label={say(`certainty.${certainty}`)}
              selected={draft.certainty === certainty}
              onPress={() =>
                onEditDraft({ certainty: draft.certainty === certainty ? '' : certainty })
              }
            />
          ))}
        </View>
        {problem('certainty')}
      </Field>

      <Field label={say('field.note')}>
        <TextInput
          testID="allergy-note"
          value={draft.note}
          onChangeText={(text) => onEditDraft({ note: text })}
          accessibilityLabel={say('field.note')}
          placeholderTextColor={colors.text.muted}
          multiline
          style={box(colors)}
        />
      </Field>

      <AppButton
        testID="allergy-record"
        label={justWrote ? t('recordedOne') : t('record')}
        disabled={busy || !canRecord(draft, reactions)}
        onPress={onRecord}
      />
    </View>
  );
}

/**
 * One of the two assertions.
 *
 * They are mirror images and the screen says so. `UNABLE_TO_ASSESS` opens a box and will not
 * send without it, because the third state is worth having only while it is reviewable.
 * `NO_KNOWN_ALLERGY` has no box at all — text nobody will read, answering a question nobody
 * asked — and instead carries the sentence that says what the tap actually claims.
 */
function Assertion({
  kind,
  reason,
  busy,
  justWrote,
  say,
  onChangeReason,
  onAssert,
}: {
  kind: AssertionKind;
  reason: string;
  busy: boolean;
  justWrote: boolean;
  say: Say;
  onChangeReason: (text: string) => void;
  onAssert: () => void;
}) {
  const t = useTranslations('allergies');
  const { colors, status } = useTokens();

  return (
    <View
      testID={`allergy-assert-${kind}`}
      style={{
        gap: theme.spacing['3'],
        padding: theme.spacing['4'],
        borderRadius: theme.borderRadius.md,
        borderWidth: 1,
        borderColor: colors.border.subtle,
        backgroundColor: colors.surface.raised,
      }}
    >
      {/* What this tap claims, in the officer's own name, before they make it. */}
      <AppText size="base">{say(`assertHint.${kind}`)}</AppText>

      {needsReason(kind) ? (
        <View style={{ gap: theme.spacing['1.5'] }}>
          <AppText size="sm" style={{ color: colors.text.secondary }}>
            {t('reasonLabel')}
          </AppText>
          <TextInput
            testID="allergy-reason"
            value={reason}
            onChangeText={onChangeReason}
            accessibilityLabel={t('reasonLabel')}
            placeholder={t('reasonPlaceholder')}
            placeholderTextColor={colors.text.muted}
            multiline
            style={box(colors)}
          />
          {reason.trim() === '' ? (
            <AppText size="sm" style={{ color: status.critical.text }}>
              {say('problem.needsReason')}
            </AppText>
          ) : null}
        </View>
      ) : null}

      <AppButton
        testID={`allergy-assert-${kind}-confirm`}
        label={justWrote ? t('recordedOne') : t('assert')}
        disabled={busy || !canAssert(kind, reason)}
        onPress={onAssert}
      />
    </View>
  );
}

type Palette = ReturnType<typeof useTokens>['colors'];

/** The one text box shape this step uses, sized for a gloved thumb at arm's length. */
function box(colors: Palette) {
  return {
    minHeight: theme.size.touchTarget * 1.5,
    borderWidth: 1,
    borderColor: colors.border.control,
    borderRadius: theme.borderRadius.md,
    paddingHorizontal: theme.spacing['4'],
    paddingVertical: theme.spacing['2'],
    backgroundColor: colors.surface.raised,
    color: colors.text.primary,
    fontSize: theme.fontSize.lg,
  };
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  const { colors } = useTokens();
  return (
    <View style={{ gap: theme.spacing['1.5'] }}>
      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {label}
      </AppText>
      {children}
    </View>
  );
}

function Choice({
  testID,
  label,
  note,
  selected,
  grow,
  onPress,
}: {
  testID: string;
  label: string;
  note?: string;
  selected: boolean;
  grow?: boolean;
  onPress: () => void;
}) {
  const { colors, status } = useTokens();
  return (
    <Pressable
      testID={testID}
      accessibilityRole="radio"
      accessibilityState={{ selected }}
      accessibilityLabel={note === undefined ? label : `${label}, ${note}`}
      onPress={onPress}
      style={{
        minHeight: theme.size.touchTarget,
        // The three answers share a row and share its width. None of them is the cheap one.
        flexGrow: grow === true ? 1 : 0,
        flexBasis: grow === true ? theme.size.touchTarget * 2 : 'auto',
        justifyContent: 'center',
        paddingHorizontal: theme.spacing['4'],
        paddingVertical: theme.spacing['2'],
        borderRadius: theme.borderRadius.md,
        borderWidth: selected ? 2 : 1,
        borderColor: selected ? colors.brand.border : colors.border.subtle,
        backgroundColor: selected ? colors.brand.subtle : colors.surface.raised,
      }}
    >
      <AppText
        size="sm"
        weight={selected ? 'semibold' : 'regular'}
        style={{ color: selected ? colors.brand.text : colors.text.secondary }}
      >
        {label}
      </AppText>
      {note === undefined ? null : (
        <AppText size="xs" weight="semibold" style={{ color: status.critical.text }}>
          {note}
        </AppText>
      )}
    </Pressable>
  );
}

function Section({
  title,
  testID,
  children,
}: {
  title: string;
  testID: string;
  children: ReactNode;
}) {
  const { colors } = useTokens();
  return (
    <View
      testID={testID}
      style={{
        gap: theme.spacing['3'],
        padding: theme.spacing['4'],
        borderRadius: theme.borderRadius.lg,
        borderWidth: 1,
        borderColor: colors.border.subtle,
        backgroundColor: colors.surface.base,
      }}
    >
      <AppText size="base" weight="semibold">
        {title}
      </AppText>
      {children}
    </View>
  );
}
