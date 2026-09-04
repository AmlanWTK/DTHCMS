import { View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppText } from '@/components/AppText';
import { DualUnitValue } from '@/components/DualUnitValue';
import { theme, useTokens } from '@/lib/tokens';
import { usePreferences } from '@/stores/preferences';

/**
 * The paediatric percentile card, on the phone (CP48, [R-06]).
 *
 * # Why there is no curve here
 *
 * The web draws the full growth chart in SVG. This does not, and the reason is not effort:
 * CP48 is explicitly "no new dependencies", and drawing curves in React Native means adding
 * `react-native-svg` and rebuilding the APK — for a chart that, at 5 inches and arm's length,
 * a clinical assistant cannot read anyway.
 *
 * What the operator needs the instant they save a child's measurements is not a curve. It is
 * **which band this child is in, and whether it is flagged** — and that is what a position
 * strip says in one glance: the reference band drawn as a bar, the child's percentile as a
 * marker on it, and the 95th picked out because [R-06] flags obesity there.
 *
 * The physician's screen, where the full trajectory matters, is a desktop. That is where the
 * chart lives.
 */

const ORDER = ['HFA', 'WFA', 'BFA'] as const;
type Indicator = (typeof ORDER)[number];

export interface CardPercentile {
  indicator: string;
  code: string;
  value: number;
  unit: string;
  percentile: number;
  z: number;
  standard: string;
}

export interface CardWeightStatus {
  class: string;
  percent_of_95th: number;
}

export function PercentileCard({
  ageDays,
  applicable,
  note,
  current,
  weightStatus,
}: {
  ageDays: number;
  applicable: boolean;
  note?: string;
  current: Partial<Record<Indicator, CardPercentile>>;
  weightStatus?: CardWeightStatus;
}) {
  const t = useTranslations('growth');
  const { colors, status } = useTokens();
  const language = usePreferences((state) => state.language);

  const shell = {
    backgroundColor: colors.surface.raised,
    borderRadius: theme.borderRadius.lg,
    borderWidth: 1,
    borderColor: colors.border.subtle,
    padding: theme.spacing['4'],
    gap: theme.spacing['3'],
  } as const;

  if (!applicable) {
    return (
      <View testID="percentile-card" style={shell}>
        <AppText size="sm" weight="semibold" style={{ color: colors.text.secondary }}>
          {t('title')}
        </AppText>
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {t(`note.${note ?? 'nothing_measured_yet'}`)}
        </AppText>
      </View>
    );
  }

  const tone = flagTone(weightStatus?.class, status);

  return (
    <View testID="percentile-card" style={shell}>
      <View
        style={{ flexDirection: 'row', justifyContent: 'space-between', alignItems: 'baseline' }}
      >
        <AppText size="sm" weight="semibold" style={{ color: colors.text.secondary }}>
          {t('title')}
        </AppText>
        <AppText size="xs" style={{ color: colors.text.muted }}>
          {ageText(ageDays, t)}
        </AppText>
      </View>

      {weightStatus !== undefined ? (
        // The word first, on a coloured ground — never the colour alone.
        <View
          testID="weight-status"
          style={{
            alignSelf: 'flex-start',
            flexDirection: 'row',
            alignItems: 'baseline',
            gap: theme.spacing['2'],
            paddingHorizontal: theme.spacing['3'],
            paddingVertical: theme.spacing['1.5'],
            borderRadius: theme.borderRadius.md,
            borderWidth: 1,
            borderColor: tone.border,
            backgroundColor: tone.surface,
          }}
        >
          <AppText size="sm" weight="semibold" style={{ color: tone.text }}>
            {t(`class.${weightStatus.class}`)}
          </AppText>
          {weightStatus.class.startsWith('obese_class') ? (
            <AppText size="xs" style={{ color: tone.text }}>
              {t('percentOf95th', { percent: weightStatus.percent_of_95th.toFixed(0) })}
            </AppText>
          ) : null}
        </View>
      ) : null}

      {ORDER.map((indicator) => {
        const value = current[indicator];
        if (value === undefined) return null;
        return (
          <View key={indicator} testID={`percentile-${indicator}`} style={{ gap: theme.spacing['1'] }}>
            <View
              style={{
                flexDirection: 'row',
                alignItems: 'baseline',
                justifyContent: 'space-between',
              }}
            >
              <AppText size="xs" style={{ color: colors.text.muted }}>
                {t(`indicator.${indicator}`)}
              </AppText>
              <AppText size="sm" weight="semibold">
                {t('percentileLong', { p: formatPercentile(value.percentile, language) })}
              </AppText>
            </View>
            <DualUnitValue value={value.value} unit={value.unit} code={value.code} />
            <PositionStrip percentile={value.percentile} />
          </View>
        );
      })}

      <AppText size="xs" style={{ color: colors.text.muted }}>
        {t('computedAgainst', { standard: t(`standard.${standardOf(current)}`) })}
      </AppText>
    </View>
  );
}

/**
 * Where this child sits between the 3rd and the 97th, as a bar.
 *
 * The chart a phone can actually show. The band is the reference; the tick at 95 is
 * [R-06]'s threshold, drawn so a child approaching it is visible before they cross it; the
 * marker is the patient.
 */
function PositionStrip({ percentile }: { percentile: number }) {
  const { colors, status } = useTokens();
  const clamped = Math.max(0, Math.min(100, percentile));

  return (
    <View
      testID="position-strip"
      accessibilityRole="progressbar"
      accessibilityValue={{ min: 0, max: 100, now: Math.round(clamped) }}
      style={{
        height: 10,
        borderRadius: theme.borderRadius.full,
        backgroundColor: colors.surface.sunken,
        borderWidth: 1,
        borderColor: colors.border.subtle,
        justifyContent: 'center',
      }}
    >
      {/* The 95th, as a fixed tick. */}
      <View
        style={{
          position: 'absolute',
          left: '95%',
          width: 2,
          top: -2,
          bottom: -2,
          backgroundColor: status.high.solid,
        }}
      />
      {/* The child. */}
      <View
        testID="position-marker"
        style={{
          position: 'absolute',
          left: `${clamped}%`,
          marginLeft: -5,
          width: 10,
          height: 10,
          borderRadius: theme.borderRadius.full,
          backgroundColor: colors.brand.solid,
          borderWidth: 1,
          borderColor: colors.surface.raised,
        }}
      />
    </View>
  );
}

type StatusTones = ReturnType<typeof useTokens>['status'];

function flagTone(name: string | undefined, status: StatusTones) {
  switch (name) {
    case 'healthy':
      return status.normal;
    case 'overweight':
      return status.borderline;
    case 'underweight':
      return status.low;
    case 'obese_class_2':
    case 'obese_class_3':
      return status.critical;
    default:
      return status.high;
  }
}

function standardOf(current: Partial<Record<Indicator, CardPercentile>>): string {
  for (const indicator of ORDER) {
    const value = current[indicator];
    if (value !== undefined) return value.standard;
  }
  return 'WHO_2006';
}

/**
 * "99.97th" is arithmetically right and useless; the z-score carries the precision.
 *
 * The message file supplies the ordinal suffix — "{p}তম" in Bangla, "{p} percentile" in
 * English — so this only rounds.
 */
function formatPercentile(p: number, language: 'en' | 'bn'): string {
  if (p >= 99) return '> 99';
  if (p <= 1) return '< 1';
  const rounded = p.toFixed(p < 10 || p > 90 ? 1 : 0);
  if (language !== 'en' || rounded.includes('.')) return rounded;
  // "3rd", not "3th". A card labelled the way no printed chart labels it is a card a
  // clinician stops trusting for reasons they would not bother to report. Bangla takes one
  // suffix for every number, so the message file carries it and this only handles English.
  const suffixes: Record<string, string> = { one: 'st', two: 'nd', few: 'rd', other: 'th' };
  const rule = new Intl.PluralRules('en', { type: 'ordinal' }).select(Number(rounded));
  return `${rounded}${suffixes[rule] ?? 'th'}`;
}

function ageText(
  days: number,
  t: (key: string, values?: Record<string, string | number | Date>) => string,
): string {
  if (days < 60) return t('ageInDays', { days });
  const months = Math.floor(days / 30.4375);
  if (months < 24) return t('ageInMonths', { months });
  return t('ageInYearsMonths', { years: Math.floor(months / 12), months: months % 12 });
}
