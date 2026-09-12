import { Pressable, ScrollView, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppButton } from '@/components/AppButton';
import { AppText } from '@/components/AppText';
import { unitLabel } from '@/components/DualUnitValue';
import { MeasurementField } from '@/components/MeasurementField';
import { PendingChip } from '@/components/PendingChip';
import { EnteredBy, ofObservation } from '@/features/attribution';
import { theme, useTokens } from '@/lib/tokens';
import { usePreferences } from '@/stores/preferences';

import {
  BP_ARMS,
  BP_CUFFS,
  BP_POSITIONS,
  VITAL_FIELDS,
  canonicalVital,
  halfABloodPressure,
  isBlank,
  type OutOfRange,
  type PreviousVitalSources,
  type Reading,
  type VitalKey,
} from './form';

/**
 * Station 5's vitals (CP49, [R-01], §15.2).
 *
 * # Built for a person who is not looking at the screen
 *
 * §15.2's heads-up requirement. The operator's eyes are on the cuff, the oximeter and the
 * patient; the screen is something they glance at. So: one column, no horizontal scanning,
 * the number in each field big enough to confirm from arm's length, and every control at
 * least a thumb wide. Nothing here needs two hands.
 *
 * # The second reading is a first-class thing
 *
 * A blood pressure measured once is a blood pressure measured badly. "Add another reading"
 * is a button, not a workaround, and both readings are stored — the second is not a
 * correction of the first, and a record that treated it as one would lose the fact that they
 * differed, which is often the finding.
 *
 * # The comparison line says whether the clinic has the value it is comparing to (CP67)
 *
 * "Last recorded: 148 mm[Hg]" reads as an established fact about the patient. On a tablet that
 * has been offline since eight it may instead be something this device alone knows — recorded
 * here twenty minutes ago, in no record anybody downstairs can see. Those are different pieces of
 * evidence for the operator deciding whether a thirty-point difference is a finding or a
 * different cuff, and the difference is one chip wide.
 */
export function VitalsStation({
  patientName,
  readings,
  flags,
  previous,
  previousSources,
  busy,
  saved,
  onChangeValue,
  onChangeUnit,
  onChangeContext,
  onAddReading,
  onSave,
}: {
  patientName: string;
  readings: Reading[];
  /** Out-of-range flags, per reading index then per field (CP49 criterion 3). */
  flags: Partial<Record<VitalKey, NonNullable<OutOfRange>>>[];
  /** The patient's last recorded value per code, canonical, for the comparison line. */
  previous: Partial<Record<VitalKey, number>>;
  /**
   * The rows those values came off, for CP61's attribution.
   *
   * Optional, and its absence is honest rather than convenient: a screen given the numbers and
   * not the rows draws the comparison and says the record does not name an author.
   */
  previousSources?: PreviousVitalSources;
  busy?: boolean;
  saved?: boolean;
  onChangeValue: (reading: number, key: VitalKey, text: string) => void;
  onChangeUnit: (reading: number, key: VitalKey, unit: string) => void;
  onChangeContext: (reading: number, field: 'arm' | 'position' | 'cuff', value: string) => void;
  onAddReading: () => void;
  onSave: () => void;
}) {
  const t = useTranslations('vitals');
  const { colors, status } = useTokens();
  const language = usePreferences((state) => state.language);

  const incomplete = readings.some(halfABloodPressure);
  const empty = readings.every(isBlank);

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

      {readings.map((reading, index) => (
        <View
          key={index}
          testID={`reading-${index}`}
          style={{
            gap: theme.spacing['4'],
            padding: index === 0 ? 0 : theme.spacing['4'],
            borderRadius: theme.borderRadius.lg,
            borderWidth: index === 0 ? 0 : 1,
            borderColor: colors.border.subtle,
            backgroundColor: index === 0 ? 'transparent' : colors.surface.raised,
          }}
        >
          {readings.length > 1 ? (
            <AppText size="sm" weight="semibold" style={{ color: colors.text.secondary }}>
              {t('readingNumber', { n: index + 1 })}
            </AppText>
          ) : null}

          {VITAL_FIELDS.map((field) => {
            const flag = flags[index]?.[field.key];
            const canonical = canonicalVital(reading.values[field.key]);
            const earlier = previous[field.key];
            // The comparison line is only drawn on the first reading, and only once this
            // operator has typed something. CP61's attribution follows it exactly: it belongs
            // to the *stored* value in that line, not to the number being typed above it,
            // which has no author until it is saved.
            const comparing = canonical !== null && earlier !== undefined && index === 0;
            return (
              <View key={field.key} style={{ gap: theme.spacing['1'] }}>
                <MeasurementField
                  testID={`vital-${index}-${field.key}`}
                  label={t(`field.${field.key}`)}
                  value={reading.values[field.key].text}
                  unit={reading.values[field.key].unit}
                  units={field.units}
                  onChangeValue={(text) => onChangeValue(index, field.key, text)}
                  onChangeUnit={(unit) => onChangeUnit(index, field.key, unit)}
                  warning={
                    flag === undefined
                      ? null
                      : {
                          // "Outside normal", not "critical". A screen that shouted at every
                          // second patient is a screen nobody hears when it matters (CP50).
                          text: t(flag.direction === 'low' ? 'belowNormal' : 'aboveNormal', {
                            limit: String(flag.limit),
                            unit: unitLabel(canonicalUnitOf(field.key), language),
                          }),
                          severity: 'warn',
                        }
                  }
                  delta={
                    comparing
                      ? t('lastRecorded', {
                          value: String(earlier),
                          unit: unitLabel(canonicalUnitOf(field.key), language),
                        })
                      : null
                  }
                />
                {comparing ? (
                  <View
                    style={{
                      flexDirection: 'row',
                      alignItems: 'center',
                      flexWrap: 'wrap',
                      gap: theme.spacing['2'],
                    }}
                  >
                    <EnteredBy
                      compact
                      testID={`vital-${index}-${field.key}-entered-by`}
                      provenance={ofObservation(previousSources?.[field.key])}
                    />
                    {/* Beside the author rather than beside the number, because it is the same
                        kind of statement: both are about the *stored* value in the comparison
                        line and neither is about the number being typed above it. */}
                    {previousSources?.[field.key]?.pending === true ? (
                      <PendingChip testID={`vital-${index}-${field.key}-pending`} />
                    ) : null}
                  </View>
                ) : null}
              </View>
            );
          })}

          {/* The context a blood pressure needs. Below the numbers, because it is chosen
              once and rarely changed — and above the save button, because a reading saved
              without it is a reading nobody can compare next time. */}
          <View style={{ gap: theme.spacing['3'] }}>
            <ChoiceRow
              label={t('arm')}
              options={BP_ARMS}
              selected={reading.arm}
              onSelect={(value) => onChangeContext(index, 'arm', value)}
              translate={(value) => t(`armOption.${value}`)}
              testID={`arm-${index}`}
            />
            <ChoiceRow
              label={t('position')}
              options={BP_POSITIONS}
              selected={reading.position}
              onSelect={(value) => onChangeContext(index, 'position', value)}
              translate={(value) => t(`positionOption.${value}`)}
              testID={`position-${index}`}
            />
            <ChoiceRow
              label={t('cuff')}
              options={BP_CUFFS}
              selected={reading.cuff}
              onSelect={(value) => onChangeContext(index, 'cuff', value)}
              translate={(value) => t(`cuffOption.${value}`)}
              testID={`cuff-${index}`}
            />
          </View>
        </View>
      ))}

      <AppButton
        testID="add-reading"
        label={t('addReading')}
        variant="secondary"
        disabled={busy === true || empty}
        onPress={onAddReading}
      />

      {incomplete ? (
        <AppText size="sm" style={{ color: status.borderline.text }}>
          {t('halfABloodPressure')}
        </AppText>
      ) : null}

      <AppButton
        testID="vitals-save"
        label={saved === true ? t('saved') : t('save')}
        disabled={busy === true || empty || incomplete}
        onPress={onSave}
      />
    </ScrollView>
  );
}

/**
 * A row of choices, sized for a thumb.
 *
 * Not a dropdown: a dropdown is two taps and a modal, and it hides the current choice behind
 * a chevron. Three or four options fit on one row, and the selected one is then impossible to
 * misread — which matters, because "left arm" recorded as "right arm" makes the next visit's
 * comparison meaningless.
 */
function ChoiceRow<T extends string>({
  label,
  options,
  selected,
  onSelect,
  translate,
  testID,
}: {
  label: string;
  options: readonly T[];
  selected: T;
  onSelect: (value: T) => void;
  translate: (value: T) => string;
  testID: string;
}) {
  const { colors } = useTokens();
  return (
    <View style={{ gap: theme.spacing['1.5'] }}>
      <AppText size="xs" style={{ color: colors.text.muted }}>
        {label}
      </AppText>
      <View
        accessibilityRole="radiogroup"
        style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.spacing['2'] }}
      >
        {options.map((option) => {
          const isSelected = option === selected;
          return (
            <Pressable
              key={option}
              testID={`${testID}-${option}`}
              accessibilityRole="radio"
              accessibilityState={{ selected: isSelected }}
              onPress={() => onSelect(option)}
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
                {translate(option)}
              </AppText>
            </Pressable>
          );
        })}
      </View>
    </View>
  );
}

function canonicalUnitOf(key: VitalKey): string {
  switch (key) {
    case 'systolic':
    case 'diastolic':
      return 'mm[Hg]';
    case 'temperature':
      return 'Cel';
    case 'spo2':
      return '%';
    default:
      return '/min';
  }
}
