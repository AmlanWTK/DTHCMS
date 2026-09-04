import type { ReactNode } from 'react';
import { Pressable, ScrollView, TextInput, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppButton } from '@/components/AppButton';
import { AppText } from '@/components/AppText';
import { theme, useTokens } from '@/lib/tokens';
import { usePreferences } from '@/stores/preferences';

import { FootDiagram } from './FootDiagram';
import {
  SIDES,
  answersFor,
  canSave,
  fieldsIn,
  interruptedFeet,
  scoreOutOfRange,
  stillBlank,
  type Answer,
  type ExamField,
  type ExamForm,
  type FootRisk,
  type MonofilamentSite,
  type Side,
} from './form';
import type { Prompt } from './prompts';

/**
 * Station 5's structured examination (CP51, §3 step 5, [R-01]).
 *
 * # Everything on this screen is a prop
 *
 * Not a style: this component cannot be rendered outside a device, so anything it decided
 * would be a decision nobody checks. What is left here is arrangement — which is worth
 * getting right, but is verified by a clinician doing an examination with it, not by a test.
 * The decisions are all in `form.ts` and `prompts.ts`, at 100%.
 *
 * # Criterion 1 is two minutes, and the layout is the budget
 *
 * One column, top to bottom, in the order the examination is performed: both feet, the
 * neuropathy score, the eyes, the chest. No horizontal scanning — an examiner whose eyes are
 * on a patient's foot cannot track a row of controls across a screen. Every answer is a
 * button at least a thumb across, because the other hand is holding a monofilament, and the
 * normal answer is drawn first in every row so the ordinary finding is the nearest tap.
 *
 * Nothing is pre-selected. The speed comes from where the buttons are, not from answers the
 * app supplied on the examiner's behalf.
 *
 * # The prompts are at the top
 *
 * Criterion 4's history-driven prompts sit above the first field, because the moment they
 * are useful is before the examiner has touched the patient — a prompt discovered at the
 * bottom of a form is a prompt discovered after the sock is back on.
 */
export function ExaminationStation({
  patientName,
  form,
  answers,
  prompts,
  risk,
  busy,
  saved,
  onTapSite,
  onMarkAllFelt,
  onSetNotTested,
  onChangeReason,
  onChangeCoded,
  onChangeFlag,
  onChangeScore,
  onSave,
}: {
  patientName: string;
  form: ExamForm;
  /** The whole vocabulary, fetched once. Rendered in the server's order, never re-sorted. */
  answers: readonly Answer[];
  /** What this patient's record asks for today (CP51 criterion 4). */
  prompts: readonly Prompt[];
  /** The risk category the server derived, once the findings have landed. */
  risk?: Partial<Record<Side, FootRisk>>;
  busy?: boolean;
  saved?: boolean;
  onTapSite: (side: Side, site: MonofilamentSite) => void;
  onMarkAllFelt: (side: Side) => void;
  onSetNotTested: (side: Side, notTested: boolean) => void;
  onChangeReason: (side: Side, reason: string) => void;
  onChangeCoded: (code: string, valueCode: string) => void;
  onChangeFlag: (code: string, value: boolean) => void;
  onChangeScore: (text: string) => void;
  onSave: () => void;
}) {
  const t = useTranslations('examination');
  const { colors, status } = useTokens();

  const blank = stillBlank(form);
  const interrupted = interruptedFeet(form);
  const outOfRange = scoreOutOfRange(form);

  // A prompt names its own sentence, so the key is a value here rather than a literal and
  // `useTranslations` cannot type it. The cast is what buys that: the rules stay the only
  // place that decides what is said, and the test file checks every key they can produce
  // exists in both languages — which is the guarantee the type would have given.
  const say = t as unknown as (key: string, values?: Record<string, string>) => string;

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

      {prompts.length > 0 ? (
        <View testID="exam-prompts" style={{ gap: theme.spacing['2'] }}>
          {prompts.map((prompt) => (
            <View
              key={`${prompt.id}-${prompt.side ?? 'both'}`}
              testID={`prompt-${prompt.id}`}
              style={{
                borderRadius: theme.borderRadius.lg,
                borderWidth: 1,
                borderColor: status.borderline.border,
                backgroundColor: status.borderline.surface,
                padding: theme.spacing['3'],
              }}
            >
              <AppText size="sm" weight="semibold" style={{ color: status.borderline.text }}>
                {say(prompt.messageKey, {
                  side: prompt.side === undefined ? '' : t(`sideName.${prompt.side}`),
                })}
              </AppText>
            </View>
          ))}
        </View>
      ) : null}

      {SIDES.map((side) => (
        <Section key={side} title={t(`footTitle.${side}`)} testID={`section-foot-${side}`}>
          <AppText size="sm" style={{ color: colors.text.secondary }}>
            {t('finding.MONOFILAMENT')}
          </AppText>
          <FootDiagram
            side={side}
            state={form.monofilament[side]}
            onTapSite={(site) => onTapSite(side, site)}
            onMarkAllFelt={() => onMarkAllFelt(side)}
            onSetNotTested={(notTested) => onSetNotTested(side, notTested)}
            onChangeReason={(reason) => onChangeReason(side, reason)}
          />

          {fieldsIn('foot', side)
            .filter((field) => field.kind !== 'monofilament')
            .map((field) => (
              <Finding
                key={field.code}
                field={field}
                form={form}
                answers={answers}
                onChangeCoded={onChangeCoded}
                onChangeFlag={onChangeFlag}
              />
            ))}

          {risk?.[side] !== undefined ? (
            <View
              testID={`risk-${side}`}
              style={{
                alignSelf: 'flex-start',
                borderRadius: theme.borderRadius.md,
                borderWidth: 1,
                borderColor: riskTone(risk[side], status).border,
                backgroundColor: riskTone(risk[side], status).surface,
                paddingHorizontal: theme.spacing['3'],
                paddingVertical: theme.spacing['1.5'],
              }}
            >
              <AppText
                size="sm"
                weight="semibold"
                style={{ color: riskTone(risk[side], status).text }}
              >
                {t('riskCategory', { category: t(`risk.${risk[side]}`) })}
              </AppText>
            </View>
          ) : null}
        </Section>
      ))}

      <Section title={t('section.neuropathy')} testID="section-neuropathy">
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {t('finding.NEUROPATHY_SYMPTOM_SCORE')}
        </AppText>
        <TextInput
          testID="neuropathy-score"
          value={form.score}
          onChangeText={onChangeScore}
          keyboardType="number-pad"
          inputMode="numeric"
          accessibilityLabel={t('finding.NEUROPATHY_SYMPTOM_SCORE')}
          placeholder="—"
          placeholderTextColor={colors.text.muted}
          style={{
            minHeight: theme.size.touchTarget,
            borderWidth: 1,
            borderColor: outOfRange ? status.critical.border : colors.border.control,
            borderRadius: theme.borderRadius.md,
            paddingHorizontal: theme.spacing['3'],
            backgroundColor: colors.surface.raised,
            color: colors.text.primary,
            // Large enough to confirm at arm's length, like every other number in the app.
            fontSize: theme.fontSize['2xl'],
          }}
        />
        {outOfRange ? (
          <AppText size="sm" style={{ color: status.critical.text }}>
            {t('scoreOutOfRange')}
          </AppText>
        ) : null}
      </Section>

      <Section title={t('section.retinopathy')} testID="section-retinopathy">
        {fieldsIn('retinopathy').map((field) => (
          <Finding
            key={field.code}
            field={field}
            form={form}
            answers={answers}
            onChangeCoded={onChangeCoded}
            onChangeFlag={onChangeFlag}
          />
        ))}
      </Section>

      <Section title={t('section.cardiovascular')} testID="section-cardiovascular">
        {fieldsIn('cardiovascular').map((field) => (
          <Finding
            key={field.code}
            field={field}
            form={form}
            answers={answers}
            onChangeCoded={onChangeCoded}
            onChangeFlag={onChangeFlag}
          />
        ))}
      </Section>

      {interrupted.length > 0 ? (
        // The one refusal that sends the examiner back to the patient rather than to the
        // screen: nine sites is not a monofilament test, and there is nowhere but the foot
        // the tenth answer can come from.
        <AppText testID="exam-interrupted" size="sm" style={{ color: status.critical.text }}>
          {t('interrupted', {
            feet: interrupted.map((side) => t(`sideName.${side}`)).join(', '),
          })}
        </AppText>
      ) : null}

      {blank.length > 0 ? (
        // A count, not a barrier. A screening clinic examines what it can reach, and a form
        // that demanded every field is a form somebody fills with whatever clears it.
        <AppText size="sm" style={{ color: colors.text.muted }}>
          {t('stillBlank', { n: String(blank.length) })}
        </AppText>
      ) : null}

      <AppButton
        testID="exam-save"
        label={saved === true ? t('saved') : t('save')}
        disabled={busy === true || !canSave(form)}
        onPress={onSave}
      />
    </ScrollView>
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
        gap: theme.spacing['4'],
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

/**
 * One finding: a label and a row of answers.
 *
 * A coded finding's words come from the server's vocabulary and a yes/no finding's from the
 * message file, but they are drawn identically — an examiner working down twenty of these
 * should never have to work out which kind of control they are looking at.
 */
function Finding({
  field,
  form,
  answers,
  onChangeCoded,
  onChangeFlag,
}: {
  field: ExamField;
  form: ExamForm;
  answers: readonly Answer[];
  onChangeCoded: (code: string, valueCode: string) => void;
  onChangeFlag: (code: string, value: boolean) => void;
}) {
  const t = useTranslations('examination');
  const { colors } = useTokens();
  const language = usePreferences((state) => state.language);

  const label =
    field.side === undefined || field.section === 'foot'
      ? t(`finding.${field.label}`)
      : t('findingSide', {
          finding: t(`finding.${field.label}`),
          side: t(`sideName.${field.side}`),
        });

  const options =
    field.kind === 'boolean'
      ? // No before yes: on every boolean finding in this examination the normal answer is
        // no, and the normal answer is always the nearest tap.
        [
          { value: 'false', text: t('no') },
          { value: 'true', text: t('yes') },
        ]
      : answersFor(answers, field.code).map((answer) => ({
          value: answer.value_code,
          text: language === 'bn' ? answer.display_bn : answer.display_en,
        }));

  const selected =
    field.kind === 'boolean'
      ? form.flags[field.code] === undefined
        ? ''
        : String(form.flags[field.code])
      : (form.coded[field.code] ?? '');

  return (
    <View style={{ gap: theme.spacing['1.5'] }}>
      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {label}
      </AppText>
      <View
        accessibilityRole="radiogroup"
        style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.spacing['2'] }}
      >
        {options.map((option) => {
          const isSelected = option.value === selected;
          return (
            <Pressable
              key={option.value}
              testID={`${field.code}-${option.value}`}
              accessibilityRole="radio"
              accessibilityState={{ selected: isSelected }}
              onPress={() =>
                field.kind === 'boolean'
                  ? onChangeFlag(field.code, option.value === 'true')
                  : onChangeCoded(field.code, option.value)
              }
              style={{
                minHeight: theme.size.touchTarget,
                justifyContent: 'center',
                paddingHorizontal: theme.spacing['4'],
                borderRadius: theme.borderRadius.md,
                borderWidth: 1,
                borderColor: isSelected ? colors.brand.border : colors.border.subtle,
                backgroundColor: isSelected ? colors.brand.subtle : colors.surface.raised,
              }}
            >
              <AppText
                size="sm"
                weight={isSelected ? 'semibold' : 'regular'}
                style={{ color: isSelected ? colors.brand.text : colors.text.secondary }}
              >
                {option.text}
              </AppText>
            </Pressable>
          );
        })}
      </View>
    </View>
  );
}

type StatusTones = ReturnType<typeof useTokens>['status'];

/**
 * Which tone a risk category wears.
 *
 * The category is always shown as a word on the coloured ground, never as colour alone —
 * the same rule the BMI class follows, and for the same reason: roughly one man in twelve
 * who will work here cannot rely on the colour, and direct sun flattens it for everybody.
 *
 * A high-risk foot wears the strongest tone the palette has and still makes no sound. It is
 * an appointment, not an emergency, and the audible alarm belongs to CP50's critical values —
 * a screen that shouted at every high-risk foot in a diabetes clinic is a screen nobody hears
 * when a potassium comes back at 7.
 */
function riskTone(category: FootRisk | undefined, status: StatusTones) {
  switch (category) {
    case 'very_low':
      return status.normal;
    case 'low':
      return status.borderline;
    case 'moderate':
      return status.high;
    default:
      return status.critical;
  }
}
