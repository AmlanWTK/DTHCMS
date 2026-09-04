import { Pressable, TextInput, View } from 'react-native';

import { AppText } from '@/components/AppText';
import { unitLabel } from '@/components/DualUnitValue';
import { theme, useTokens } from '@/lib/tokens';
import { usePreferences } from '@/stores/preferences';

/**
 * One measurement, entered on a phone (CP45, §15.2).
 *
 * # Why the number is so large
 *
 * §15.2's heads-up requirement: the operator is looking at the patient, not at the screen.
 * A number they can read from arm's length in one glance is a number they can confirm
 * without stopping — and confirming is the whole of quality control at a measuring station.
 *
 * # Why the unit selector is a row of buttons and not a dropdown
 *
 * A dropdown is two taps and a modal, and it hides the current unit behind a chevron. The
 * units a station offers are two or three; showing them all costs one row and makes the
 * selected one impossible to misread. A weight recorded in the wrong unit is wrong by a
 * factor of 2.2, which is exactly the error that reaches a dose.
 *
 * When there is only one unit — per cent for body fat, /min for a pulse — no selector is
 * drawn at all. A control with one option is a control that teaches people to tap without
 * reading.
 */
export function MeasurementField({
  label,
  value,
  unit,
  units,
  onChangeValue,
  onChangeUnit,
  delta,
  warning,
  onConfirm,
  confirmLabel,
  testID,
}: {
  label: string;
  value: string;
  unit: string;
  units: readonly string[];
  onChangeValue: (text: string) => void;
  onChangeUnit: (unit: string) => void;
  /** What changed since the last visit, already worded. */
  delta?: string | null;
  /** A plausibility warning the operator has to see while they can still re-measure. */
  warning?: { text: string; severity: 'warn' | 'stop' } | null;
  /** Offered only for a warning a confirmation can pass. */
  onConfirm?: () => void;
  confirmLabel?: string;
  testID?: string;
}) {
  const { colors, status } = useTokens();
  const language = usePreferences((state) => state.language);

  const tone = warning?.severity === 'stop' ? status.critical : status.borderline;

  return (
    <View style={{ gap: theme.spacing['1.5'] }}>
      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {label}
      </AppText>

      <View style={{ flexDirection: 'row', alignItems: 'stretch', gap: theme.spacing['2'] }}>
        <TextInput
          testID={testID}
          value={value}
          onChangeText={onChangeValue}
          keyboardType="decimal-pad"
          // Not `numeric`: on Android that keyboard has no decimal separator on several
          // OEM skins, and an operator who cannot type 72.5 types 725.
          inputMode="decimal"
          accessibilityLabel={label}
          placeholder="—"
          placeholderTextColor={colors.text.muted}
          style={{
            flex: 1,
            minHeight: theme.size.touchTarget,
            borderRadius: theme.borderRadius.md,
            borderWidth: warning ? theme.size.borderWidth.thick : theme.size.borderWidth.thin,
            borderColor: warning ? tone.border : colors.border.control,
            backgroundColor: warning ? tone.surface : colors.surface.raised,
            color: colors.text.primary,
            paddingHorizontal: theme.spacing['4'],
            // The measurement itself, at display size. Everything else on this screen is
            // interface; this is the clinical value.
            fontSize: theme.fontSize['3xl'],
            fontFamily: 'Inter-SemiBold',
          }}
        />

        {units.length > 1 ? (
          <View
            accessibilityRole="radiogroup"
            style={{ flexDirection: 'row', gap: theme.spacing['1'] }}
          >
            {units.map((option) => {
              const selected = option === unit;
              return (
                <Pressable
                  key={option}
                  testID={testID ? `${testID}-unit-${option}` : undefined}
                  accessibilityRole="radio"
                  accessibilityState={{ selected }}
                  accessibilityLabel={unitLabel(option, language)}
                  onPress={() => onChangeUnit(option)}
                  style={{
                    minWidth: theme.size.touchTarget,
                    minHeight: theme.size.touchTarget,
                    alignItems: 'center',
                    justifyContent: 'center',
                    paddingHorizontal: theme.spacing['2'],
                    borderRadius: theme.borderRadius.md,
                    borderWidth: theme.size.borderWidth.thin,
                    borderColor: selected ? colors.brand.border : colors.border.subtle,
                    backgroundColor: selected ? colors.brand.subtle : colors.surface.raised,
                  }}
                >
                  <AppText
                    size="sm"
                    weight={selected ? 'semibold' : 'regular'}
                    style={{ color: selected ? colors.brand.text : colors.text.secondary }}
                  >
                    {unitLabel(option, language)}
                  </AppText>
                </Pressable>
              );
            })}
          </View>
        ) : (
          <View style={{ justifyContent: 'center', paddingHorizontal: theme.spacing['2'] }}>
            <AppText size="lg" style={{ color: colors.text.secondary }}>
              {unitLabel(unit, language)}
            </AppText>
          </View>
        )}
      </View>

      {warning ? (
        <View style={{ gap: theme.spacing['2'] }}>
          <AppText
            testID={testID ? `${testID}-warning` : undefined}
            size="sm"
            weight="medium"
            style={{ color: tone.text }}
          >
            {warning.text}
          </AppText>
          {/* Offered only when a confirmation would actually store the value. An impossible
              value that showed a confirm button would send the operator round a loop. */}
          {warning.severity === 'warn' && onConfirm !== undefined && confirmLabel !== undefined ? (
            <Pressable
              testID={testID ? `${testID}-confirm` : undefined}
              accessibilityRole="button"
              onPress={onConfirm}
              style={{
                alignSelf: 'flex-start',
                minHeight: theme.size.touchTarget,
                justifyContent: 'center',
                paddingHorizontal: theme.spacing['4'],
                borderRadius: theme.borderRadius.md,
                borderWidth: theme.size.borderWidth.thin,
                borderColor: tone.border,
                backgroundColor: colors.surface.raised,
              }}
            >
              <AppText size="sm" weight="semibold" style={{ color: tone.text }}>
                {confirmLabel}
              </AppText>
            </Pressable>
          ) : null}
        </View>
      ) : delta ? (
        // The comparison with last visit, quiet. It is context for the number above, not a
        // second number to read — but it is here, beside the field, because the moment it
        // is useful is the moment the operator can still re-measure.
        <AppText
          testID={testID ? `${testID}-delta` : undefined}
          size="xs"
          style={{ color: colors.text.muted }}
        >
          {delta}
        </AppText>
      ) : null}
    </View>
  );
}
