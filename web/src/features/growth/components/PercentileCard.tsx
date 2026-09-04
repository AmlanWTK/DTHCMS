'use client';

import { useLocale, useTranslations } from 'next-intl';

import { DualUnitValue } from '@/features/observations';

import type { Growth, GrowthPercentile, Indicator, WeightStatus } from '../api/growth';

/**
 * The paediatric percentile card (CP48, §8's snapshot panel, [R-06]).
 *
 * # What a clinician reads first
 *
 * The **flag**, if there is one. §8 puts the card in the snapshot panel, which is the strip a
 * physician reads in the four seconds before a child sits down — so the one thing that
 * changes what happens next goes at the top, in words, on a coloured ground. Never colour
 * alone: roughly one man in twelve who will work in this clinic cannot rely on it, and a
 * tablet in direct sun flattens every hue anyway.
 *
 * Then the three percentiles, each with its z-score beneath. Both, because they answer
 * different questions: a percentile is what a parent understands, and a z-score is what a
 * change over time is measured in — above about the 99th percentile the percentile stops
 * discriminating while the z-score keeps going, which is exactly the range where a child is
 * most unwell.
 *
 * # When there is nothing to show
 *
 * The card says so, in a sentence. An empty card with three dashes is a card a physician has
 * to interrogate; "no growth reference applies at this age" is an answer.
 */

const ORDER: Indicator[] = ['HFA', 'WFA', 'BFA'];

export function PercentileCard({
  growth,
  weightStatus,
  compact = false,
}: {
  growth: Growth;
  weightStatus?: WeightStatus;
  /** For the snapshot strip, where the card is one of six panels. */
  compact?: boolean;
}) {
  const t = useTranslations('growth');
  const locale = useLocale();

  if (!growth.applicable) {
    return (
      <section className="app-percentile" data-testid="percentile-card" data-empty="true">
        <h3>{t('title')}</h3>
        <p className="app-percentile__note">{t(`note.${growth.note ?? 'nothing_measured_yet'}`)}</p>
      </section>
    );
  }

  const current = growth.current ?? {};

  return (
    <section
      className="app-percentile"
      data-testid="percentile-card"
      data-density={compact ? 'compact' : 'full'}
    >
      <header>
        <h3>{t('title')}</h3>
        <p className="app-percentile__age">{ageText(growth.age_days, t)}</p>
      </header>

      {weightStatus !== undefined ? (
        <p
          className="app-percentile__flag"
          data-class={weightStatus.class}
          data-testid="weight-status"
        >
          <strong>{t(`class.${weightStatus.class}`)}</strong>
          {weightStatus.class === 'obese_class_2' || weightStatus.class === 'obese_class_3' ? (
            // CDC's severity convention, and the only thing that tells two very differently
            // unwell children apart once both are "above the 99th percentile".
            <span className="app-percentile__severity">
              {t('percentOf95th', { percent: weightStatus.percent_of_95th.toFixed(0) })}
            </span>
          ) : null}
        </p>
      ) : null}

      <dl className="app-percentile__values">
        {ORDER.map((indicator) => {
          const value = current[indicator] as GrowthPercentile | undefined;
          if (value === undefined) return null;
          return (
            <div key={indicator} data-testid={`percentile-${indicator}`}>
              <dt>{t(`indicator.${indicator}`)}</dt>
              <dd>
                <DualUnitValue value={value.value} unit={value.unit} code={value.code} />
                <span className="app-percentile__percentile">
                  {t('percentileLong', { p: formatPercentile(value.percentile, locale) })}
                </span>
                <span className="app-percentile__z">{t('zScore', { z: value.z.toFixed(2) })}</span>
              </dd>
            </div>
          );
        })}
      </dl>

      {/* Which reference produced these numbers. Not a footnote for tidiness: percentiles
          computed under WHO and under CDC are not comparable, and a card that did not say
          which would invite exactly that comparison (D-21). */}
      <p className="app-percentile__standard" data-testid="percentile-standard">
        {t('computedAgainst', { standard: standardName(current, t) })}
      </p>
    </section>
  );
}

function standardName(
  current: Record<string, GrowthPercentile | undefined>,
  t: (key: string) => string,
): string {
  for (const indicator of ORDER) {
    const value = current[indicator];
    if (value !== undefined) return t(`standard.${value.standard}`);
  }
  return '';
}

/**
 * A percentile, worded so the extremes stay readable.
 *
 * "99.97th" is arithmetically right and useless: nobody can tell it from "99.9th", and both
 * describe children who are very differently unwell. Past the edges the card says "above the
 * 99th" and lets the z-score carry the precision.
 */
function formatPercentile(p: number, locale: string): string {
  if (p >= 99) return '> 99';
  if (p <= 1) return '< 1';
  const rounded = p.toFixed(p < 10 || p > 90 ? 1 : 0);
  if (!locale.startsWith('en') || rounded.includes('.')) return rounded;
  // "3rd", not "3th". A card labelled the way no printed chart labels it is a card a
  // clinician stops trusting for reasons they would not bother to report.
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
