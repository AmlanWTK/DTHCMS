'use client';

import { useLocale, useTranslations } from 'next-intl';

import type { GrowthCurveSet, GrowthPoint, Indicator } from '../api/growth';

/**
 * The growth curve (CP48, [R-06]).
 *
 * # Why it is hand-drawn SVG
 *
 * No charting library. Not asceticism — a growth chart is not a generic line chart, and the
 * three things that make it readable are the three things a library makes hard: the 95th
 * percentile has to be visually distinct because [R-06] flags obesity at it; the join
 * between two reference standards has to be *drawn*, because D-21 says a percentile computed
 * under WHO and one computed under CDC are not the same measurement; and the whole thing has
 * to print, on a page a clinician hands to a parent.
 *
 * Fifty lines of SVG does all three and adds nothing to the bundle.
 *
 * # What the reader is meant to see, in order
 *
 * 1. **Where this child is** — the trajectory, drawn heaviest and last, so it sits on top.
 * 2. **Which band they are in** — the 3rd/50th/97th as the frame, the 95th picked out.
 * 3. **That the reference changed at five** — a rule and a label, not a silent join.
 *
 * Everything else is quiet on purpose. A chart where the gridlines compete with the patient
 * is a chart somebody reads wrongly at a glance.
 */

const WIDTH = 720;
const HEIGHT = 460;
const PAD = { top: 16, right: 76, bottom: 44, left: 52 };

/** The lines that get a label and a heavier stroke. The rest are context. */
const NAMED = new Set([3, 50, 97]);

export interface GrowthChartProps {
  curves: GrowthCurveSet;
  points: GrowthPoint[];
  indicator: Indicator;
  /** Narrow the age window to what this child actually spans, plus a margin. */
  focus?: boolean;
  caption?: string;
}

export function GrowthChart({ curves, points, indicator, focus = true, caption }: GrowthChartProps) {
  const t = useTranslations('growth');
  const locale = useLocale();

  const plotted = points.filter((point) => Number.isFinite(point.age_months));
  const domain = ageDomain(curves, plotted, focus);
  const range = valueRange(curves, plotted, domain);

  const x = (months: number) =>
    PAD.left + ((months - domain[0]) / (domain[1] - domain[0])) * (WIDTH - PAD.left - PAD.right);
  const y = (value: number) =>
    HEIGHT - PAD.bottom - ((value - range[0]) / (range[1] - range[0])) * (HEIGHT - PAD.top - PAD.bottom);

  const path = (pairs: [number, number][]) =>
    pairs
      .filter(([months]) => months >= domain[0] && months <= domain[1])
      .map(([months, value], index) => `${index === 0 ? 'M' : 'L'}${x(months).toFixed(2)},${y(value).toFixed(2)}`)
      .join(' ');

  // Where the reference changes. Drawn, because D-21 says it must be visible: a chart with a
  // silent join invites exactly the comparison across it that the decision forbids.
  const boundaries = curves.standards
    .map((standard) => standard.min_age_months)
    .filter((months) => months > domain[0] && months < domain[1]);

  return (
    <figure className="app-growth" data-indicator={indicator}>
      <svg
        viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
        role="img"
        aria-label={t(`chartLabel.${indicator}`)}
        className="app-growth__svg"
        data-testid="growth-chart"
      >
        {/* The frame. Quiet: it is a ruler, not information. */}
        <g className="app-growth__axes">
          {yTicks(range).map((value) => (
            <g key={value}>
              <line x1={PAD.left} x2={WIDTH - PAD.right} y1={y(value)} y2={y(value)} />
              <text x={PAD.left - 8} y={y(value)} textAnchor="end" dominantBaseline="middle">
                {value}
              </text>
            </g>
          ))}
          {xTicks(domain).map((months) => (
            <g key={months}>
              <line x1={x(months)} x2={x(months)} y1={PAD.top} y2={HEIGHT - PAD.bottom} />
              <text x={x(months)} y={HEIGHT - PAD.bottom + 18} textAnchor="middle">
                {ageTickLabel(months, t)}
              </text>
            </g>
          ))}
          <text
            x={PAD.left}
            y={HEIGHT - 8}
            className="app-growth__axis-title"
            textAnchor="start"
          >
            {t('axisAge')}
          </text>
        </g>

        {/* The reference lines. */}
        {curves.curves.map((curve) => (
          <path
            key={curve.percentile}
            d={path(curve.points as [number, number][])}
            className="app-growth__curve"
            data-percentile={curve.percentile}
            data-named={NAMED.has(curve.percentile) ? 'true' : undefined}
            data-threshold={curve.percentile === 95 ? 'true' : undefined}
            fill="none"
          />
        ))}

        {/* Their labels, at the right edge where the eye leaves the line. */}
        {curves.curves
          .filter((curve) => NAMED.has(curve.percentile) || curve.percentile === 95)
          .map((curve) => {
            const last = (curve.points as [number, number][])
              .filter(([months]) => months <= domain[1])
              .at(-1);
            if (last === undefined) return null;
            return (
              <text
                key={`label-${curve.percentile}`}
                x={WIDTH - PAD.right + 6}
                y={y(last[1])}
                dominantBaseline="middle"
                className="app-growth__curve-label"
                data-threshold={curve.percentile === 95 ? 'true' : undefined}
              >
                {t('percentileShort', { p: ordinal(curve.percentile, locale) })}
              </text>
            );
          })}

        {/* Where the reference changes. */}
        {boundaries.map((months) => (
          <g key={`boundary-${months}`} className="app-growth__boundary" data-testid="standard-change">
            <line x1={x(months)} x2={x(months)} y1={PAD.top} y2={HEIGHT - PAD.bottom} />
            <text x={x(months) + 4} y={PAD.top + 12}>
              {t('standardChanges')}
            </text>
          </g>
        ))}

        {/* This child, on top and heaviest. */}
        {plotted.length > 1 ? (
          <path
            d={path(plotted.map((point) => [point.age_months, point.value]) as [number, number][])}
            className="app-growth__patient"
            fill="none"
            data-testid="growth-trajectory"
          />
        ) : null}
        {plotted.map((point) => (
          <circle
            key={`${point.effective_at}-${point.value}`}
            cx={x(point.age_months)}
            cy={y(point.value)}
            r={plotted.length > 12 ? 3 : 4.5}
            className="app-growth__point"
            data-standard-changed={point.standard_changed === true ? 'true' : undefined}
          />
        ))}
      </svg>
      {caption !== undefined ? <figcaption>{caption}</figcaption> : null}
    </figure>
  );
}

/**
 * A percentile as a reader says it: "3rd", not "3th".
 *
 * Small, and worth the code. A chart labelled "3th" beside a printed reference chart labelled
 * "3rd" is a chart a clinician stops trusting for reasons they would not bother to report.
 * Bangla takes one suffix for every number, so the message file carries it and this only has
 * to handle English.
 */
function ordinal(p: number, locale: string): string {
  if (!locale.startsWith('en')) return String(p);
  const suffixes: Record<string, string> = { one: 'st', two: 'nd', few: 'rd', other: 'th' };
  const rule = new Intl.PluralRules('en', { type: 'ordinal' }).select(p);
  return `${p}${suffixes[rule] ?? 'th'}`;
}

/** The age window: what the child spans plus a margin, or the whole reference. */
function ageDomain(curves: GrowthCurveSet, points: GrowthPoint[], focus: boolean): [number, number] {
  const all = curves.curves.flatMap((curve) =>
    (curve.points as [number, number][]).map(([months]) => months),
  );
  const full: [number, number] = [Math.min(...all), Math.max(...all)];
  if (!focus || points.length === 0) return full;
  const ages = points.map((point) => point.age_months);
  // Twelve months of margin either side: a chart cropped to the data alone makes every
  // trajectory look like it fills the page, which flattens the one that is actually steep.
  const lo = Math.max(full[0], Math.min(...ages) - 12);
  const hi = Math.min(full[1], Math.max(...ages) + 12);
  return hi - lo < 6 ? full : [lo, hi];
}

/** The value window: the reference band over the visible ages, widened to include the child. */
function valueRange(
  curves: GrowthCurveSet,
  points: GrowthPoint[],
  domain: [number, number],
): [number, number] {
  const visible = curves.curves.flatMap((curve) =>
    (curve.points as [number, number][])
      .filter(([months]) => months >= domain[0] && months <= domain[1])
      .map(([, value]) => value),
  );
  const values = [...visible, ...points.map((point) => point.value)];
  if (values.length === 0) return [0, 1];
  const lo = Math.min(...values);
  const hi = Math.max(...values);
  const margin = (hi - lo) * 0.06 || 1;
  return [lo - margin, hi + margin];
}

function niceStep(span: number): number {
  const raw = span / 6;
  const magnitude = Math.pow(10, Math.floor(Math.log10(raw)));
  for (const factor of [1, 2, 2.5, 5, 10]) {
    if (raw <= magnitude * factor) return magnitude * factor;
  }
  return magnitude * 10;
}

function yTicks(range: [number, number]): number[] {
  const step = niceStep(range[1] - range[0]);
  const out: number[] = [];
  for (let value = Math.ceil(range[0] / step) * step; value <= range[1]; value += step) {
    out.push(Number(value.toFixed(2)));
  }
  return out;
}

function xTicks(domain: [number, number]): number[] {
  const span = domain[1] - domain[0];
  // Months while the window is short, years once it is long. A chart labelled "72 months"
  // is a chart a parent has to do arithmetic on.
  const step = span <= 24 ? 6 : span <= 72 ? 12 : 24;
  const out: number[] = [];
  for (let months = Math.ceil(domain[0] / step) * step; months <= domain[1]; months += step) {
    out.push(months);
  }
  return out;
}

function ageTickLabel(
  months: number,
  t: (key: string, values?: Record<string, string | number | Date>) => string,
): string {
  if (months < 24) return t('ageMonths', { months });
  return t('ageYears', { years: Math.round(months / 12) });
}
