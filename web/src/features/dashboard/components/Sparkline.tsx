'use client';

import { useLocale, useTranslations } from 'next-intl';

import { ValueWithAttribution, observationAttribution } from '@/features/attribution';
import { DualUnitValue } from '@/features/observations';
import { formatDate } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';

import type { DashboardTrend } from '../api/dashboard';

import { codeLabel } from './dashboardText';

/**
 * §8's sparkline: the last five values of one code, and what happened across them.
 *
 * # Why the picture is not the answer
 *
 * A sparkline five points wide, an inch across, on a tablet in a clinic, is a *gesture* — it
 * says up or down and nothing more precise. Read as a chart it is a lie, because five points
 * over two years and five points over five months draw identically. So the chart is
 * accompanied, always, by:
 *
 *   - the current value in both units, drawn by the same component every other value on this
 *     screen is drawn by;
 *   - the change, in words, with the interval it happened over — *"−1.8 over 14 months"* —
 *     computed on the server so that two screens cannot compute it two ways;
 *   - the same five numbers in a table, which is what the SVG's `<title>` and the expandable
 *     list carry, so that a screen reader and a keyboard user get the series rather than the
 *     word "chart".
 *
 * # Why every point is a button
 *
 * Criterion 5: attribution one interaction away on **every** value. A step in an HbA1c series
 * is the most likely thing on this screen to make a physician ask "who typed that", and a
 * sparkline whose points cannot be interrogated is the one place the promise would quietly
 * not hold. The points are therefore rendered twice — as a polyline for the eye, and as a
 * list of `ValueWithAttribution` for the hand and the keyboard — and the list is not hidden
 * from assistive technology.
 *
 * # Why there is no y-axis and no zero line
 *
 * A five-point sparkline with an axis is a chart pretending to be readable at this size, and
 * a zero baseline would flatten every clinically interesting HbA1c series into a straight
 * line near the top. The line is drawn between its own minimum and maximum, which is the only
 * honest scaling for a shape-only chart — and the numbers beneath are what a physician
 * actually reads. CP74's timeline is the scrubable chart with a real axis.
 */

export interface SparklineProps {
  trend: DashboardTrend;
}

/** The drawing box. Small, because this sits in a column beside five other cards. */
const WIDTH = 108;
const HEIGHT = 28;
const PADDING = 3;

export function Sparkline({ trend }: SparklineProps) {
  const t = useTranslations('dashboard.trend');
  const locale = useLocale() as Locale;

  // A point with no numeric value is not drawable and is not an error: a code that stores
  // text or a boolean has a history and no line. Filtering rather than refusing means a
  // patient whose HbA1c series contains one unmeasurable row still gets the other four.
  const points = trend.points.filter(
    (point): point is (typeof trend.points)[number] & { value: number } =>
      typeof point.value === 'number',
  );
  const newest = points.at(-1);
  if (newest === undefined) return null;

  const values = points.map((point) => point.value);

  return (
    <section className="dash-spark" data-testid={`sparkline-${trend.code}`}>
      <header className="dash-spark__head">
        <h4 className="dash-spark__label">{codeLabel(trend.code, locale, t)}</h4>
        {trend.change ? (
          <p className="dash-spark__change" data-direction={direction(trend.change.delta)}>
            {/* The arithmetic is the server's; this only says it. A delta computed here as
                well would be a second implementation of one subtraction, and the two would
                disagree on the day a unit conversion changed. */}
            {t('change', {
              delta: signed(trend.change.delta),
              days: Math.max(trend.change.over_days, 0),
            })}
          </p>
        ) : (
          // One value is a series of one, and it is drawn. "One reading, no trend yet" is a
          // clinical fact a physician wants: it means this is a baseline.
          <p className="dash-spark__change" data-direction="none">
            {t('single')}
          </p>
        )}
      </header>

      <div className="dash-spark__row">
        <svg
          className="dash-spark__chart"
          viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
          width={WIDTH}
          height={HEIGHT}
          // Decorative *as a picture*: everything it conveys is in the list below it, and a
          // screen reader reading a polyline's coordinates would be reading nothing. The
          // values themselves are not hidden — they are the list.
          aria-hidden="true"
          focusable="false"
        >
          <polyline
            className="dash-spark__line"
            points={geometry(values)}
            fill="none"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
          {/* The newest point marked, because "where is the patient now" is the question the
              chart is asked, and the eye should not have to work out which end is today. */}
          <circle
            className="dash-spark__now"
            cx={xAt(values.length - 1, values.length)}
            cy={yAt(newest.value, values)}
            r={2.4}
          />
        </svg>

        <ValueWithAttribution
          attribution={observationAttribution(newest)}
          label={codeLabel(trend.code, locale, t)}
          testId={`sparkline-now-${trend.code}`}
        >
          <DualUnitValue
            value={newest.value}
            unit={newest.unit ?? ''}
            code={newest.code}
            size="sm"
          />
        </ValueWithAttribution>
      </div>

      {/*
        The series, as text. Not a fallback and not hidden: it is how a keyboard user and a
        screen reader read the trend, and it is where criterion 5 is satisfied for the four
        older points — each one is a control that reveals who entered it.

        `<ol>` rather than a table because it is one dimension, and the reading order is the
        clinical one: oldest to newest.
      */}
      <ol
        className="dash-spark__series"
        aria-label={t('seriesLabel', { code: codeLabel(trend.code, locale, t) })}
      >
        {points.map((point) => (
          <li key={point.id}>
            {/*
              The disclosure variant, not the compact one, and this is the only place on the
              screen where that choice is about density rather than about safety. `compact`
              puts the operator's name on screen without any interaction, which is right for
              the allergy strip and wrong for twenty sparkline points: five points × four
              series is twenty names, and a column of names is a column a physician reads past
              rather than reads. The promise is *one interaction away*, and a disclosure keeps
              it — hover, focus or tap, exactly as everywhere else.
            */}
            <ValueWithAttribution
              attribution={observationAttribution(point)}
              label={`${codeLabel(trend.code, locale, t)} · ${formatDate(Date.parse(point.effective_at), locale)}`}
              testId={`sparkline-point-${point.id}`}
            >
              <span className="dash-spark__point">
                <span className="dash-spark__point-date">
                  {formatDate(Date.parse(point.effective_at), locale)}
                </span>
                <DualUnitValue
                  value={point.value}
                  unit={point.unit ?? ''}
                  code={point.code}
                  size="sm"
                />
              </span>
            </ValueWithAttribution>
          </li>
        ))}
      </ol>
    </section>
  );
}

/** The polyline's points, scaled between the series' own minimum and maximum. */
function geometry(values: readonly number[]): string {
  return values
    .map((value, index) => `${xAt(index, values.length)},${yAt(value, values)}`)
    .join(' ');
}

function xAt(index: number, count: number): number {
  if (count <= 1) return WIDTH / 2;
  return PADDING + (index * (WIDTH - PADDING * 2)) / (count - 1);
}

/**
 * A value's vertical position, between the series' own extremes.
 *
 * A flat series — five identical values, which is what a well-controlled patient looks like —
 * has a zero range, and dividing by it would put every point at NaN and draw nothing. It is
 * centred instead, which is the honest picture: nothing moved.
 */
function yAt(value: number, values: readonly number[]): number {
  const low = Math.min(...values);
  const high = Math.max(...values);
  const span = high - low;
  if (span === 0) return HEIGHT / 2;
  const usable = HEIGHT - PADDING * 2;
  return PADDING + usable - ((value - low) / span) * usable;
}

function direction(delta: number): 'up' | 'down' | 'none' {
  if (delta > 0) return 'up';
  if (delta < 0) return 'down';
  return 'none';
}

/**
 * The delta with its sign, always — including the plus.
 *
 * A bare "1.8" beside "over 14 months" is ambiguous in exactly the direction that matters:
 * an HbA1c that rose 1.8 and one that fell 1.8 are opposite clinical stories, and the reader
 * is scanning six of these cards in four seconds.
 */
function signed(delta: number): string {
  const rounded = Math.round(delta * 100) / 100;
  return rounded > 0 ? `+${rounded}` : `${rounded}`;
}
