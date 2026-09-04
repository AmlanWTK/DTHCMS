import { useMemo } from 'react';
import { ScrollView, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppButton } from '@/components/AppButton';
import { AppText } from '@/components/AppText';
import { DualUnitValue, unitLabel } from '@/components/DualUnitValue';
import { MeasurementField } from '@/components/MeasurementField';
import { theme, useTokens } from '@/lib/tokens';
import { usePreferences } from '@/stores/preferences';

import {
  ANTHRO_FIELDS,
  canonicalValue,
  deltaOf,
  isEmpty,
  panelOf,
  type FieldKey,
  type FieldWarning,
  type FormState,
  type PatientFacts,
  type PreviousValues,
} from './form';

/**
 * Station 2 (CP45, blueprint §3 step 2, §5.2).
 *
 * The first station that is a whole vertical slice: a phone, six fields, four values the
 * server will compute, and a physician's dashboard that updates without anybody refreshing.
 *
 * # The panel is above the fields, not below them
 *
 * P-4 asks for derived values "as the operator types", and where they sit decides whether
 * that is useful. Below the form they are a result — something you scroll to after
 * finishing. Above it they are a *readout*: the operator types a weight, glances up, sees
 * the BMI move, and knows they typed what they meant. That glance is the quality control.
 *
 * # Nothing here computes a stored value
 *
 * The panel is arithmetic on what is on screen, for the operator's eyes. Saving posts the
 * numbers **as typed** with the units **as selected**, and the server computes the derived
 * values from what it has just written (CP43). The two agree because they are the same
 * equations held together by shared fixtures — not because this screen sent its answer.
 */
export function AnthropometryStation({
  patientName,
  patient,
  form,
  previous,
  busy,
  saved,
  warnings,
  onChangeValue,
  onChangeUnit,
  onConfirm,
  onSave,
}: {
  patientName: string;
  patient: PatientFacts;
  form: FormState;
  previous: PreviousValues;
  busy?: boolean;
  /** Set once the batch has landed, so the operator knows the record has it. */
  saved?: boolean;
  /** Plausibility feedback, keyed by field (CP46). Facts; this screen writes the words. */
  warnings?: Partial<Record<FieldKey, FieldWarning>>;
  onChangeValue: (key: FieldKey, text: string) => void;
  onChangeUnit: (key: FieldKey, unit: string) => void;
  onConfirm?: (key: FieldKey) => void;
  onSave: () => void;
}) {
  const t = useTranslations('anthropometry');
  const { colors, status } = useTokens();
  const language = usePreferences((state) => state.language);

  // Recomputed on every keystroke. It is pure arithmetic on four numbers — criterion (1)
  // allows 200ms and this uses a few microseconds.
  const panel = useMemo(() => panelOf(form, patient), [form, patient]);

  const blocked = Object.values(warnings ?? {}).some((w) => w?.severity === 'stop');

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

      {/* The readout. */}
      <View
        testID="anthro-panel"
        style={{
          backgroundColor: colors.surface.raised,
          borderRadius: theme.borderRadius.lg,
          borderWidth: 1,
          borderColor: colors.border.subtle,
          padding: theme.spacing['4'],
          gap: theme.spacing['3'],
        }}
      >
        <View style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.spacing['5'] }}>
          <DualUnitValue
            value={panel.bmi?.value ?? null}
            unit="kg/m2"
            label={t('bmi')}
            large
          />
          <DualUnitValue
            value={panel.bmr?.value ?? null}
            unit="kcal/d"
            label={t('bmr')}
          />
          <DualUnitValue
            value={panel.ideal_body_weight?.value ?? null}
            unit="kg"
            label={t('idealWeight')}
          />
          <DualUnitValue value={panel.whr?.value ?? null} unit="1" label={t('whr')} />
        </View>

        {panel.obesity_class ? (
          // The class as a word on a coloured ground, never colour alone: roughly one man
          // in twelve who will work in this clinic cannot rely on the colour, and a tablet
          // in direct sun flattens all of them anyway.
          <View
            testID="obesity-class"
            style={{
              alignSelf: 'flex-start',
              backgroundColor: classTone(panel.obesity_class, status).surface,
              borderColor: classTone(panel.obesity_class, status).border,
              borderWidth: 1,
              borderRadius: theme.borderRadius.md,
              paddingHorizontal: theme.spacing['3'],
              paddingVertical: theme.spacing['1.5'],
            }}
          >
            <AppText
              size="sm"
              weight="semibold"
              style={{ color: classTone(panel.obesity_class, status).text }}
            >
              {t(`class.${panel.obesity_class}`)}
            </AppText>
          </View>
        ) : null}

        {panel.needs !== undefined ? (
          // What is still missing, as a sentence composed here — the library sends the
          // facts and the screen writes the words, because the reader may be reading Bangla
          // (the same rule as CP40's board).
          <AppText testID="anthro-needs" size="xs" style={{ color: colors.text.muted }}>
            {t('waitingFor', { fields: missingList(panel.needs, t) })}
          </AppText>
        ) : null}
      </View>

      {/* The form. */}
      <View style={{ gap: theme.spacing['4'] }}>
        {ANTHRO_FIELDS.map((field) => {
          const state = form[field.key];
          const delta = deltaOf(canonicalValue(state), previous[field.key]);
          return (
            <MeasurementField
              key={field.key}
              testID={`field-${field.key}`}
              label={t(`field.${field.key}`)}
              value={state.text}
              unit={state.unit}
              units={field.units}
              onChangeValue={(text) => onChangeValue(field.key, text)}
              onChangeUnit={(unit) => onChangeUnit(field.key, unit)}
              warning={warningFor(warnings?.[field.key], field.key, t, language)}
              onConfirm={onConfirm === undefined ? undefined : () => onConfirm(field.key)}
              confirmLabel={t('confirmValue')}
              delta={
                delta === null
                  ? null
                  : delta.direction === 'same'
                    ? t('deltaSame')
                    : t(delta.direction === 'up' ? 'deltaUp' : 'deltaDown', {
                        amount: formatDelta(delta.change),
                        unit: unitLabel(canonicalUnitOf(field.key), language),
                      })
              }
            />
          );
        })}
      </View>

      <AppButton
        testID="anthro-save"
        label={saved === true ? t('saved') : t('save')}
        disabled={busy === true || blocked || isEmpty(form)}
        onPress={onSave}
      />

      {blocked ? (
        <AppText size="sm" style={{ color: status.critical.text, textAlign: 'center' }}>
          {t('blocked')}
        </AppText>
      ) : null}
    </ScrollView>
  );
}

/** The canonical unit each field's delta is expressed in. */
function canonicalUnitOf(key: FieldKey): string {
  switch (key) {
    case 'weight':
    case 'muscle':
      return 'kg';
    case 'bodyFat':
      return '%';
    default:
      return 'cm';
  }
}

/** One decimal, signed by the wording rather than by a minus sign. */
function formatDelta(change: number): string {
  return Math.abs(change).toFixed(1);
}

function missingList(
  needs: Record<string, string[]>,
  t: (key: string) => string,
): string {
  const names = new Set<string>();
  for (const list of Object.values(needs)) for (const name of list) names.add(name);
  return [...names].map((name) => t(`measurement.${name}`)).join(', ');
}

/**
 * A rule's verdict, as a sentence.
 *
 * Composed here rather than sent by the server, for the same reason CP40's board composes
 * its own: the operator may be reading Bangla, and a message assembled in English on a
 * server is a message half the staff cannot act on. The rule sends the numbers; the screen
 * writes the words.
 */
function warningFor(
  verdict: FieldWarning | undefined,
  key: FieldKey,
  t: ReturnType<typeof useTranslations<'anthropometry'>>,
  language: 'en' | 'bn',
): { text: string; severity: 'warn' | 'stop' } | null {
  if (verdict === undefined) return null;
  const unit = unitLabel(canonicalUnitOf(key), language);
  const limit = formatDelta(verdict.limit);
  const note = language === 'bn' ? verdict.note_bn : verdict.note_en;

  let text: string;
  switch (verdict.kind) {
    case 'low':
      text = t('warnLow', { limit, unit });
      break;
    case 'high':
      text = t('warnHigh', { limit, unit });
      break;
    case 'rose':
      text = t('warnRose', { limit, unit });
      break;
    default:
      text = t('warnFell', { limit, unit });
  }
  if (note !== undefined && note !== '') text = `${text} ${note}`;
  return { text, severity: verdict.severity };
}

type StatusTones = ReturnType<typeof useTokens>['status'];

/**
 * Which tone a BMI class wears.
 *
 * Underweight and obese are both departures from normal and both get a warning tone;
 * "critical" is reserved for CP50's actual critical values, because a screen that shouts at
 * every second patient is a screen nobody hears when it matters.
 */
function classTone(name: string, status: StatusTones) {
  switch (name) {
    case 'normal':
      return status.normal;
    case 'overweight':
      return status.borderline;
    case 'underweight':
      return status.low;
    default:
      return status.high;
  }
}
