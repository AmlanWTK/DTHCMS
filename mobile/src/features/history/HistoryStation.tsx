import type { ReactNode } from 'react';
import { Pressable, ScrollView, TextInput, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppButton } from '@/components/AppButton';
import { AppText } from '@/components/AppText';
import { ConceptPicker, type Concept, type PickerState } from '@/features/terminology';
import { theme, useTokens } from '@/lib/tokens';
import { usePreferences } from '@/stores/preferences';

import {
  DURATION_PRESETS,
  ONSET_PRECISIONS,
  SEVERITIES,
  canRecord,
  fieldsFor,
  groupByKind,
  isPristine,
  itemCoding,
  itemLabel,
  ofUnknownKind,
  outstanding,
  problemsWith,
  refusalOn,
  relationsInOrder,
  removalRefused,
  uncodedCount,
  type CarriedItem,
  type FamilyRelation,
  type HistoryDraft,
  type HistoryField,
  type HistoryItem,
  type HistoryKind,
  type LifestyleRow,
  type OnsetPrecision,
  type Severity,
} from './form';
import type { Trouble } from './api';

/**
 * Station 4's medical history (CP53, §3 station 4).
 *
 * # Everything here is arrangement; every decision is in `form.ts`
 *
 * The same split as every other station: this component cannot be rendered outside a device,
 * so anything it decided would be a decision nobody checks. What it holds is where things
 * sit, which is judged by a history officer using it on the clinic's tablet.
 *
 * # The allergy checkpoint sits above all of it
 *
 * `gate` is CP54's step, and it is drawn first because it is not another kind of history: it
 * is the one thing on this screen that decides whether the patient may be sent to the next
 * station at all. An officer who had to scroll past six headings to reach it would meet the
 * refusal at the queue instead, with the patient already standing up.
 *
 * # It is laid out in the order the conversation goes
 *
 * The carried-forward list first, then what is new. That is not a preference about screens:
 * a returning patient's consultation begins with "you were on metformin and had pain in the
 * left foot — is that still right?", and an officer who has to scroll past an empty new-item
 * form to reach that question will stop asking it. The new item lives at the bottom, where
 * the conversation arrives at it.
 *
 * # There is no button that confirms the list
 *
 * Twenty carried-forward items is twenty presses, each its own request with a person behind
 * it. The screen says so out loud, in a sentence, because the absence of a convenience is
 * otherwise read as an oversight and somebody adds one. What it does instead is count: the
 * officer is told how many are still outstanding, which does the work of a progress bar
 * without making a single assertion on anybody's behalf.
 *
 * # Resolving and removing are two controls with two different words
 *
 * "No longer has this" is a clinical fact and the item stays on the list wearing the word
 * *Resolved*. "Should not have been recorded" opens a box for a reason and will not send
 * without one. They are never the same button, and neither is ever the only one showing.
 *
 * # An uncoded item says so
 *
 * The chip carries all three parts of a coding where there is one, and the word *Uncoded*
 * with the patient's own words where there is not. That is not an apology: the uncoded count
 * is the list of concepts the catalogue is missing, and hiding it would hide the one number
 * that gets the catalogue fixed.
 *
 * # No state is carried by colour alone
 *
 * Every row says what it is in words — active or resolved, confirmed this visit or waiting,
 * coded or not. Roughly one man in twelve who will work here cannot rely on the colour, and
 * direct sun through the clinic's windows flattens it for everybody else.
 */
export function HistoryStation({
  patientName,
  gate,
  kinds,
  relations,
  lifestyle,
  items,
  since,
  uncoded,
  draft,
  draftKind,
  picker,
  removingId,
  removeReason,
  busy,
  savedId,
  justRecorded,
  trouble,
  onChooseKind,
  onEditDraft,
  onSetOnset,
  onPickerQuery,
  onPickerSelect,
  onPickerClear,
  onPickerRetry,
  onRecord,
  onConfirm,
  onSetResolved,
  onStartRemoving,
  onChangeRemoveReason,
  onRemove,
  onRetry,
}: {
  patientName: string;
  /**
   * Station 4's allergy checkpoint (CP54), drawn above everything else on this screen.
   *
   * It is here rather than beside the history because it is not another kind of history: it
   * is the refusal that decides whether this patient may be sent to the next station at all,
   * and an officer who had to scroll past six headings to reach it would meet it at the queue
   * instead. One scroll, one screen, the checkpoint at the top of it.
   */
  gate?: ReactNode;
  /** The six kinds and their rules, fetched once. Every field below is derived from them. */
  kinds: readonly HistoryKind[];
  relations: readonly FamilyRelation[];
  /** What the lifestyle station has answered. Shown, never asked for again. */
  lifestyle: readonly LifestyleRow[];
  items: readonly HistoryItem[];
  /** When this visit began. What "confirmed this visit" is measured against. */
  since: string;
  /** Uncoded items at this clinic, by kind. The catalogue's to-do list, in the open. */
  uncoded: Record<string, number>;
  draft: HistoryDraft;
  /** The kind being recorded, or null before one is chosen. */
  draftKind: HistoryKind | null;
  /** The coded-terminology picker (CP52), opened on this kind's own catalogue. */
  picker: PickerState | null;
  /** The item whose removal is being written, or null. Only ever one at a time. */
  removingId: string | null;
  removeReason: string;
  busy?: boolean;
  /** The item just confirmed, so the officer can see that press landed. */
  savedId?: string | null;
  /** True for a moment after a new item is written, for the same reason. */
  justRecorded?: boolean;
  trouble?: Trouble | null;
  onChooseKind: (kind: HistoryKind) => void;
  onEditDraft: (patch: Partial<HistoryDraft>) => void;
  onSetOnset: (onsetOn: string, precision: OnsetPrecision | '') => void;
  onPickerQuery: (text: string) => void;
  onPickerSelect: (concept: Concept) => void;
  onPickerClear: () => void;
  onPickerRetry: () => void;
  onRecord: () => void;
  /** One item. There is no list-shaped counterpart to this anywhere in the feature. */
  onConfirm: (itemId: string) => void;
  onSetResolved: (itemId: string, resolved: boolean) => void;
  onStartRemoving: (itemId: string | null) => void;
  onChangeRemoveReason: (reason: string) => void;
  onRemove: (itemId: string) => void;
  onRetry: () => void;
}) {
  const t = useTranslations('history');
  const { colors, status } = useTokens();
  const locale = usePreferences((state) => state.language);

  // A refusal, a carry-forward reason and a field label each name their own sentence, so the
  // key is a value rather than a literal and `useTranslations` cannot type it — the same cast
  // the examination station uses for its prompts, with the same guarantee behind it: the test
  // file asserts every key this code can produce exists in both languages.
  const say = t as unknown as (key: string, values?: Record<string, string>) => string;

  const groups = groupByKind(items, kinds, since);
  const strays = ofUnknownKind(items, kinds, since);
  const progress = outstanding(items, since);
  // Nothing is corrected at an officer who has not started. A refusal that was on screen
  // before the first keystroke is furniture by the time it means something.
  const refusals = draftKind === null || isPristine(draft) ? [] : problemsWith(draft, draftKind);

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

      {/* The allergy checkpoint, before anything else. Nothing below it can be sent anywhere
          until it is answered, so nothing below it is drawn above it. */}
      {gate}

      {lifestyle.length > 0 ? (
        <Section title={t('lifestyle')} testID="history-lifestyle">
          {/* Shown and never asked. A second copy of "does she smoke" would be two answers
              to one question with no way to tell which one is current. */}
          <AppText size="sm" style={{ color: colors.text.secondary }}>
            {t('lifestyleHint')}
          </AppText>
          {lifestyle.map((row) => (
            <View
              key={row.code}
              testID={`lifestyle-${row.code}`}
              style={{
                flexDirection: 'row',
                alignItems: 'baseline',
                gap: theme.spacing['3'],
              }}
            >
              <AppText size="sm" weight="semibold" variant="clinicalValue" style={{ flex: 1 }}>
                {row.code}
              </AppText>
              <AppText
                size="sm"
                style={{ color: row.known ? colors.text.primary : colors.text.muted }}
              >
                {row.known ? row.valueCode : t('lifestyleUnknown')}
              </AppText>
            </View>
          ))}
        </Section>
      ) : null}

      <Section title={t('carried')} testID="history-carried">
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {t('carriedHint')}
        </AppText>

        {progress.total === 0 ? (
          <AppText size="base">{t('nothingCarried')}</AppText>
        ) : (
          <View style={{ gap: theme.spacing['1'] }}>
            {/* A count, not a control. It is the only aggregate on this screen, and there is
                nothing it can be pressed to clear. */}
            <AppText testID="history-outstanding" size="base" weight="semibold">
              {progress.done === progress.total
                ? t('allConfirmed')
                : t('outstanding', { done: String(progress.done), total: String(progress.total) })}
            </AppText>
            <AppText size="sm" style={{ color: colors.text.muted }}>
              {t('oneAtATime')}
            </AppText>
          </View>
        )}
      </Section>

      {groups.map((group) => (
        <Section
          key={group.kind.kind}
          title={locale === 'bn' ? group.kind.display_bn : group.kind.display_en}
          testID={`history-kind-${group.kind.kind}`}
          note={
            uncodedCount(uncoded, group.kind.kind) > 0
              ? t('uncodedInClinic', { n: String(uncodedCount(uncoded, group.kind.kind)) })
              : undefined
          }
        >
          {group.items.length === 0 ? (
            <AppText size="sm" style={{ color: colors.text.muted }}>
              {t('none')}
            </AppText>
          ) : (
            group.items.map((carried) => (
              <ItemRow
                key={carried.item.id}
                carried={carried}
                locale={locale}
                say={say}
                busy={busy === true}
                saved={savedId === carried.item.id}
                removing={removingId === carried.item.id}
                removeReason={removeReason}
                onConfirm={() => onConfirm(carried.item.id)}
                onSetResolved={(resolved) => onSetResolved(carried.item.id, resolved)}
                onStartRemoving={onStartRemoving}
                onChangeRemoveReason={onChangeRemoveReason}
                onRemove={() => onRemove(carried.item.id)}
              />
            ))
          )}
        </Section>
      ))}

      {strays.length > 0 ? (
        // A kind this build has never heard of. Surfaced rather than dropped: a tablet a
        // version behind the server would otherwise hide a whole kind of history from the
        // officer working through the list.
        <Section title={t('unknownKind')} testID="history-unknown-kind">
          {strays.map((carried) => (
            <ItemRow
              key={carried.item.id}
              carried={carried}
              locale={locale}
              say={say}
              busy={busy === true}
              saved={savedId === carried.item.id}
              removing={removingId === carried.item.id}
              removeReason={removeReason}
              onConfirm={() => onConfirm(carried.item.id)}
              onSetResolved={(resolved) => onSetResolved(carried.item.id, resolved)}
              onStartRemoving={onStartRemoving}
              onChangeRemoveReason={onChangeRemoveReason}
              onRemove={() => onRemove(carried.item.id)}
            />
          ))}
        </Section>
      ) : null}

      <Section title={t('add')} testID="history-add">
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {t('chooseKind')}
        </AppText>
        <View
          accessibilityRole="radiogroup"
          style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.spacing['2'] }}
        >
          {groups.map((group) => (
            <Choice
              key={group.kind.kind}
              testID={`kind-${group.kind.kind}`}
              label={locale === 'bn' ? group.kind.display_bn : group.kind.display_en}
              selected={draft.kind === group.kind.kind}
              onPress={() => onChooseKind(group.kind)}
            />
          ))}
        </View>

        {draftKind !== null ? (
          <NewItem
            kind={draftKind}
            relations={relations}
            locale={locale}
            draft={draft}
            picker={picker}
            refusals={refusals}
            say={say}
            onEditDraft={onEditDraft}
            onSetOnset={onSetOnset}
            onPickerQuery={onPickerQuery}
            onPickerSelect={onPickerSelect}
            onPickerClear={onPickerClear}
            onPickerRetry={onPickerRetry}
          />
        ) : null}

        {draftKind !== null ? (
          <AppButton
            testID="history-record"
            label={justRecorded === true ? t('recorded') : t('record')}
            disabled={busy === true || !canRecord(draft, draftKind)}
            onPress={onRecord}
          />
        ) : null}
      </Section>

      {trouble !== null && trouble !== undefined ? (
        <View
          testID="history-trouble"
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
            {/* Three troubles, three headings. A refusal and a dead link leave the officer in
                different places and send different people to look. */}
            {trouble.kind === 'refused'
              ? t('trouble.refused')
              : trouble.kind === 'unreachable'
                ? t('trouble.unreachable')
                : t('trouble.failed')}
          </AppText>
          {/* The server's own sentence where there is one. The per-kind rules are the
              database's, and a client that paraphrased them would be inventing a second,
              staler account of a clinical rule it does not own. */}
          {trouble.kind !== 'unreachable' && trouble.message !== '' ? (
            <AppText size="base">{trouble.message}</AppText>
          ) : null}
          <AppText size="sm">{t('troubleHint')}</AppText>
          <AppButton
            testID="history-retry"
            variant="secondary"
            label={t('retry')}
            onPress={onRetry}
          />
        </View>
      ) : null}
    </ScrollView>
  );
}

type Say = (key: string, values?: Record<string, string>) => string;

/**
 * One carried-forward item, with the three things that can be done to it.
 *
 * The words come before the buttons: what it is, whether it is active or resolved, whether
 * anybody has confirmed it this visit and — when they have not — why not. An officer reading
 * a row aloud to a patient is reading the top of it.
 */
function ItemRow({
  carried,
  locale,
  say,
  busy,
  saved,
  removing,
  removeReason,
  onConfirm,
  onSetResolved,
  onStartRemoving,
  onChangeRemoveReason,
  onRemove,
}: {
  carried: CarriedItem;
  locale: 'en' | 'bn';
  say: Say;
  busy: boolean;
  saved: boolean;
  removing: boolean;
  removeReason: string;
  onConfirm: () => void;
  onSetResolved: (resolved: boolean) => void;
  onStartRemoving: (itemId: string | null) => void;
  onChangeRemoveReason: (reason: string) => void;
  onRemove: () => void;
}) {
  const t = useTranslations('history');
  const { colors, status } = useTokens();
  const item = carried.item;
  const coding = itemCoding(item);
  const tone = carried.needsConfirmation ? status.borderline : status.normal;

  return (
    <View
      testID={`history-item-${item.id}`}
      style={{
        gap: theme.spacing['2'],
        padding: theme.spacing['4'],
        borderRadius: theme.borderRadius.lg,
        borderWidth: 1,
        borderColor: colors.border.subtle,
        backgroundColor: colors.surface.raised,
      }}
    >
      <AppText size="base" weight="semibold">
        {itemLabel(item, locale)}
      </AppText>

      {coding !== null ? (
        // All three parts. A chip showing only the code would teach the clinic that a code is
        // a coding, which is the mistake the whole coding rule exists for.
        <AppText size="sm" variant="clinicalValue" style={{ color: colors.text.secondary }}>
          {t('coding', { system: coding.system, code: coding.code, version: coding.version })}
        </AppText>
      ) : (
        <View style={{ gap: theme.spacing['0.5'] }}>
          <AppText size="xs" weight="semibold" style={{ color: status.borderline.text }}>
            {t('uncoded')}
          </AppText>
          {carried.said !== '' ? <AppText size="sm">{carried.said}</AppText> : null}
        </View>
      )}

      {/* What the patient said, kept beside a coded item too: the catalogue says "Type 2
          diabetes mellitus without complications" and the patient said "sugar since the
          flood", and the second one is the clinical detail. */}
      {coding !== null && carried.said !== '' ? (
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {t('said')}: {carried.said}
        </AppText>
      ) : null}

      <View style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.spacing['3'] }}>
        <AppText size="xs" weight="semibold" style={{ color: colors.text.secondary }}>
          {say(`status.${item.status}`)}
        </AppText>
        {/* The word, never the colour. */}
        <AppText size="xs" weight="semibold" style={{ color: tone.text }}>
          {carried.reason === null ? t('confirmedToday') : say(`why.${carried.reason}`)}
        </AppText>
      </View>

      <View style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.spacing['2'] }}>
        {carried.needsConfirmation ? (
          // One item, one press, one request. There is deliberately no control above this
          // list that would do twenty of these at once.
          <AppButton
            testID={`confirm-${item.id}`}
            label={saved ? t('confirmedToday') : t('confirm')}
            disabled={busy}
            onPress={onConfirm}
          />
        ) : null}
        <AppButton
          testID={`resolve-${item.id}`}
          variant="secondary"
          label={carried.resolved ? t('reactivate') : t('resolve')}
          disabled={busy}
          onPress={() => onSetResolved(!carried.resolved)}
        />
        <AppButton
          testID={`remove-open-${item.id}`}
          variant="secondary"
          label={t('removeOpen')}
          disabled={busy}
          onPress={() => onStartRemoving(removing ? null : item.id)}
        />
      </View>

      <AppText size="xs" style={{ color: colors.text.muted }}>
        {t('resolveHint')}
      </AppText>

      {removing ? (
        <View
          testID={`remove-${item.id}`}
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
            {t('removeHint')}
          </AppText>
          <TextInput
            testID={`remove-reason-${item.id}`}
            value={removeReason}
            onChangeText={onChangeRemoveReason}
            accessibilityLabel={t('removeReason')}
            placeholder={t('removeReason')}
            placeholderTextColor={colors.text.muted}
            multiline
            style={{
              minHeight: theme.size.touchTarget * 1.5,
              borderWidth: 1,
              borderColor: colors.border.control,
              borderRadius: theme.borderRadius.md,
              paddingHorizontal: theme.spacing['3'],
              paddingVertical: theme.spacing['2'],
              backgroundColor: colors.surface.raised,
              color: colors.text.primary,
              fontSize: theme.fontSize.lg,
            }}
          />
          <View style={{ flexDirection: 'row', gap: theme.spacing['2'] }}>
            <AppButton
              testID={`remove-confirm-${item.id}`}
              label={t('remove')}
              // The reason is the point of the endpoint, so an empty one never leaves here.
              disabled={busy || removalRefused(removeReason)}
              onPress={onRemove}
            />
            <AppButton
              testID={`remove-cancel-${item.id}`}
              variant="secondary"
              label={t('removeCancel')}
              onPress={() => onStartRemoving(null)}
            />
          </View>
        </View>
      ) : null}
    </View>
  );
}

/**
 * The new item, drawn from the kind's own rules.
 *
 * Every field below is present because `fieldsFor` put it there, and `fieldsFor` is the
 * server's booleans read in order. There is no `switch` on the kind anywhere on this screen:
 * a clinic that changes which kinds carry a severity changes this form by changing a row.
 */
function NewItem({
  kind,
  relations,
  locale,
  draft,
  picker,
  refusals,
  say,
  onEditDraft,
  onSetOnset,
  onPickerQuery,
  onPickerSelect,
  onPickerClear,
  onPickerRetry,
}: {
  kind: HistoryKind;
  relations: readonly FamilyRelation[];
  locale: 'en' | 'bn';
  draft: HistoryDraft;
  picker: PickerState | null;
  refusals: ReturnType<typeof problemsWith>;
  say: Say;
  onEditDraft: (patch: Partial<HistoryDraft>) => void;
  onSetOnset: (onsetOn: string, precision: OnsetPrecision | '') => void;
  onPickerQuery: (text: string) => void;
  onPickerSelect: (concept: Concept) => void;
  onPickerClear: () => void;
  onPickerRetry: () => void;
}) {
  const t = useTranslations('history');
  const { colors, status } = useTokens();

  const problem = (where: HistoryField | 'coding') => {
    const found = refusalOn(refusals, where);
    if (found === null) return null;
    return (
      <AppText testID={`problem-${where}`} size="sm" style={{ color: status.critical.text }}>
        {say(`problem.${found}`)}
      </AppText>
    );
  };

  function control(field: HistoryField, label: string): ReactNode {
    switch (field) {
      case 'relation':
        return (
          <View
            accessibilityRole="radiogroup"
            style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.spacing['2'] }}
          >
            {relationsInOrder(relations).map((relation) => (
              <Choice
                key={relation.relation}
                testID={`relation-${relation.relation}`}
                label={locale === 'bn' ? relation.display_bn : relation.display_en}
                selected={draft.relation === relation.relation}
                onPress={() => onEditDraft({ relation: relation.relation })}
              />
            ))}
          </View>
        );

      case 'duration':
        return (
          <View style={{ gap: theme.spacing['2'] }}>
            {/* The six durations a complaint usually turns out to be. "Two weeks" on a
                number pad is four taps and a conversion in somebody's head; here it is one,
                and the box stays for everything that is not one of six numbers. */}
            <View style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.spacing['2'] }}>
              {DURATION_PRESETS.map((preset) => (
                <Choice
                  key={preset.key}
                  testID={`duration-${preset.key}`}
                  label={say(`preset.${preset.key}`)}
                  selected={draft.duration === String(preset.days)}
                  onPress={() => onEditDraft({ duration: String(preset.days) })}
                />
              ))}
            </View>
            <TextInput
              testID="draft-duration"
              value={draft.duration}
              onChangeText={(text) => onEditDraft({ duration: text })}
              keyboardType="number-pad"
              inputMode="numeric"
              accessibilityLabel={label}
              placeholder={t('durationDays')}
              placeholderTextColor={colors.text.muted}
              style={box(colors)}
            />
          </View>
        );

      case 'onset':
        return (
          <View style={{ gap: theme.spacing['2'] }}>
            <TextInput
              testID="draft-onset"
              value={draft.onsetOn}
              onChangeText={(text) => onSetOnset(text, draft.onsetPrecision)}
              accessibilityLabel={t('onsetOn')}
              placeholder={t('onsetOn')}
              placeholderTextColor={colors.text.muted}
              style={box(colors)}
            />
            {/* How exact the date is, beside the date rather than under it. A patient who
                says "about two years ago" has given a real answer, and storing it to the day
                makes a guess look like a measurement. */}
            <View
              accessibilityRole="radiogroup"
              style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.spacing['2'] }}
            >
              {ONSET_PRECISIONS.map((precision: OnsetPrecision) => (
                <Choice
                  key={precision}
                  testID={`onset-${precision}`}
                  label={say(`onsetPrecision.${precision}`)}
                  selected={draft.onsetPrecision === precision}
                  onPress={() =>
                    onSetOnset(draft.onsetOn, draft.onsetPrecision === precision ? '' : precision)
                  }
                />
              ))}
            </View>
          </View>
        );

      case 'severity':
        return (
          <View
            accessibilityRole="radiogroup"
            style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.spacing['2'] }}
          >
            {SEVERITIES.map((severity: Severity) => (
              <Choice
                key={severity}
                testID={`severity-${severity}`}
                label={say(`severity.${severity}`)}
                selected={draft.severity === severity}
                // Nothing is pre-selected, and a second press clears it: a severity nobody
                // chose is a finding the form invented on the officer's behalf.
                onPress={() =>
                  onEditDraft({ severity: draft.severity === severity ? '' : severity })
                }
              />
            ))}
          </View>
        );

      case 'dose':
      case 'frequency':
        return (
          <TextInput
            testID={`draft-${field}`}
            value={field === 'dose' ? draft.dose : draft.frequency}
            onChangeText={(text) =>
              onEditDraft(field === 'dose' ? { dose: text } : { frequency: text })
            }
            accessibilityLabel={label}
            placeholderTextColor={colors.text.muted}
            style={box(colors)}
          />
        );

      default:
        return (
          <TextInput
            testID="draft-said"
            value={draft.said}
            onChangeText={(text) => onEditDraft({ said: text })}
            accessibilityLabel={label}
            placeholder={t('said')}
            placeholderTextColor={colors.text.muted}
            multiline
            style={box(colors)}
          />
        );
    }
  }

  return (
    <View style={{ gap: theme.spacing['4'] }}>
      {/* The picker, on this kind's own catalogue. A complaint comes from the clinic's own
          dictionary and a comorbidity from ICD, and the screen never chooses between them. */}
      {picker !== null ? (
        <View style={{ gap: theme.spacing['1'] }}>
          <ConceptPicker
            state={picker}
            onChangeQuery={onPickerQuery}
            onSelect={onPickerSelect}
            onClearSelection={onPickerClear}
            onRetry={onPickerRetry}
          />
          {problem('coding')}
          {/* The escape hatch, said out loud rather than discovered. The catalogue will not
              have a code for everything a history officer meets, and an item that could not
              be coded is worth more in words than lost to a blank form. */}
          <AppText size="xs" style={{ color: colors.text.muted }}>
            {t('uncodedHint')}
          </AppText>
        </View>
      ) : null}

      {/* Every control below is here because `fieldsFor` put it here, in the order it put it
          — which is the server's own booleans read in order. There is no `switch (kind)`
          anywhere on this screen: the switch is on the *field*, which is the only thing a
          control can honestly be chosen by. Change the rules and this form changes. */}
      {fieldsFor(kind).map((ask) => {
        const label = say(`field.${ask.field}`);
        return (
          <Field key={ask.field} label={label} required={ask.required}>
            {control(ask.field, label)}
            {problem(ask.field)}
          </Field>
        );
      })}
    </View>
  );
}

type Palette = ReturnType<typeof useTokens>['colors'];

/** The one text box shape this station uses, sized for a gloved thumb at arm's length. */
function box(colors: Palette) {
  return {
    minHeight: theme.size.touchTarget * 1.5,
    borderWidth: 1,
    borderColor: colors.border.control,
    borderRadius: theme.borderRadius.md,
    paddingHorizontal: theme.spacing['4'],
    backgroundColor: colors.surface.raised,
    color: colors.text.primary,
    fontSize: theme.fontSize.lg,
  };
}

function Field({
  label,
  required,
  children,
}: {
  label: string;
  required?: boolean;
  children: ReactNode;
}) {
  const t = useTranslations('history');
  const { colors } = useTokens();
  return (
    <View style={{ gap: theme.spacing['1.5'] }}>
      <View style={{ flexDirection: 'row', gap: theme.spacing['2'], alignItems: 'baseline' }}>
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {label}
        </AppText>
        {required === true ? (
          // The word, because the kind's rule is a refusal the officer will otherwise meet
          // after the patient has finished speaking.
          <AppText size="xs" weight="semibold" style={{ color: colors.text.muted }}>
            {t('required')}
          </AppText>
        ) : null}
      </View>
      {children}
    </View>
  );
}

function Choice({
  testID,
  label,
  selected,
  onPress,
}: {
  testID: string;
  label: string;
  selected: boolean;
  onPress: () => void;
}) {
  const { colors } = useTokens();
  return (
    <Pressable
      testID={testID}
      accessibilityRole="radio"
      accessibilityState={{ selected }}
      onPress={onPress}
      style={{
        minHeight: theme.size.touchTarget,
        justifyContent: 'center',
        paddingHorizontal: theme.spacing['4'],
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
    </Pressable>
  );
}

function Section({
  title,
  note,
  testID,
  children,
}: {
  title: string;
  note?: string;
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
      <View style={{ gap: theme.spacing['0.5'] }}>
        <AppText size="base" weight="semibold">
          {title}
        </AppText>
        {note !== undefined ? (
          // The uncoded count, in the open beside the heading. If it grows the catalogue is
          // wrong rather than the officers, and it is only actionable if somebody sees it.
          <AppText size="xs" style={{ color: colors.text.muted }}>
            {note}
          </AppText>
        ) : null}
      </View>
      {children}
    </View>
  );
}
