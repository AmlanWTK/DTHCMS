import { View } from 'react-native';
import { useTranslations } from 'use-intl';

import { dualUnit } from '@dthcms/clinical-calc';

import { AppText } from '@/components/AppText';
import { theme, useTokens } from '@/lib/tokens';
import { usePreferences } from '@/stores/preferences';

/**
 * Every clinical value, in both units (CP44, [R-08], blueprint §2).
 *
 * The station app's twin of the web component, and deliberately the same shape: primary
 * prominent, secondary beneath in muted type. A height that reads 168 cm / 5′6″ on a tablet
 * and 5.5 ft on a desktop is two screens a clinician has to read differently, which is
 * exactly what a shared component exists to prevent.
 *
 * The conversion itself is `@dthcms/clinical-calc`, the same module the web imports and the
 * same one CP89's print pipeline will. One copy of every factor, checked against the
 * database by a Go test — see `display_db_test.go`.
 */

const UNIT_LABELS: Record<string, { en: string; bn: string }> = {
  cm: { en: 'cm', bn: 'সেমি' },
  m: { en: 'm', bn: 'মিটার' },
  in: { en: 'in', bn: 'ইঞ্চি' },
  '[ft_i]': { en: 'ft', bn: 'ফুট' },
  kg: { en: 'kg', bn: 'কেজি' },
  g: { en: 'g', bn: 'গ্রাম' },
  '[lb_av]': { en: 'lb', bn: 'পাউন্ড' },
  'mm[Hg]': { en: 'mmHg', bn: 'মিমি পারদ' },
  kPa: { en: 'kPa', bn: 'কিপিএ' },
  Cel: { en: '°C', bn: '°সে' },
  '[degF]': { en: '°F', bn: '°ফা' },
  '/min': { en: '/min', bn: '/মিনিট' },
  '%': { en: '%', bn: '%' },
  'kcal/d': { en: 'kcal/day', bn: 'কিলোক্যালরি/দিন' },
  'kg/m2': { en: 'kg/m²', bn: 'কেজি/মি²' },
  m2: { en: 'm²', bn: 'মি²' },
  '1': { en: '', bn: '' },
  'mL/min/{1.73_m2}': { en: 'mL/min/1.73m²', bn: 'মিলি/মিনিট/১.৭৩মি²' },
  'mmol/L': { en: 'mmol/L', bn: 'মিলিমোল/লি' },
  'mg/dL': { en: 'mg/dL', bn: 'মিগ্রা/ডেসিলি' },
  'mmol/L#chol': { en: 'mmol/L', bn: 'মিলিমোল/লি' },
  'mg/dL#chol': { en: 'mg/dL', bn: 'মিগ্রা/ডেসিলি' },
  'mmol/L#trig': { en: 'mmol/L', bn: 'মিলিমোল/লি' },
  'mg/dL#trig': { en: 'mg/dL', bn: 'মিগ্রা/ডেসিলি' },
  'umol/L': { en: 'µmol/L', bn: 'মাইক্রোমোল/লি' },
  'mg/dL#cr': { en: 'mg/dL', bn: 'মিগ্রা/ডেসিলি' },
  'mmol/mol': { en: 'mmol/mol', bn: 'মিলিমোল/মোল' },
  '%#ngsp': { en: '%', bn: '%' },
};

/** An unknown code renders as itself: a bracket expression on screen is a bug somebody
 *  reports, and a blank is a bug nobody notices. */
export function unitLabel(code: string, language: 'en' | 'bn'): string {
  return UNIT_LABELS[code]?.[language] ?? code;
}

export function DualUnitValue({
  value,
  unit,
  code,
  label,
  large = false,
}: {
  value: number | null | undefined;
  unit: string;
  /**
   * The observation code. Matters for one thing: whether a centimetre value is spoken in
   * feet and inches (a height) or in plain inches (a waist).
   */
  code?: string;
  label?: string;
  /** For the one value a station screen is built around — the measurement just taken. */
  large?: boolean;
}) {
  const t = useTranslations('observations');
  const language = usePreferences((state) => state.language);
  const { colors } = useTokens();

  if (value === null || value === undefined || !Number.isFinite(value)) {
    // "Not recorded" and "zero" are different facts, and a dash that could be either is a
    // dash an operator has to go and check.
    return (
      <View testID="dual-unit" style={{ gap: theme.spacing['0.5'] }}>
        {label ? (
          <AppText size="xs" style={{ color: colors.text.muted }}>
            {label}
          </AppText>
        ) : null}
        <AppText size="sm" style={{ color: colors.text.muted }}>
          {t('notRecorded')}
        </AppText>
      </View>
    );
  }

  const pair = dualUnit(value, unit, code);

  return (
    <View testID="dual-unit" style={{ gap: theme.spacing['0.5'] }}>
      {label ? (
        <AppText size="xs" style={{ color: colors.text.muted }}>
          {label}
        </AppText>
      ) : null}
      <View
        style={{
          flexDirection: 'row',
          alignItems: 'baseline',
          gap: theme.spacing['1'],
        }}
      >
        <AppText size={large ? '2xl' : 'lg'} weight="semibold">
          {pair.primary.text}
        </AppText>
        <AppText size={large ? 'base' : 'sm'} style={{ color: colors.text.secondary }}>
          {unitLabel(pair.primary.unit, language)}
        </AppText>
      </View>
      {pair.secondary ? (
        // Beneath, and quieter. Never the same size as the clinical value: an equivalent
        // that competed with it would be a screen where somebody reads the wrong number.
        <AppText
          testID="dual-unit-secondary"
          size={large ? 'sm' : 'xs'}
          style={{ color: colors.text.muted }}
        >
          {pair.secondary.unit === 'in'
            ? pair.secondary.text
            : `${pair.secondary.text} ${unitLabel(pair.secondary.unit, language)}`}
        </AppText>
      ) : null}
    </View>
  );
}
