'use client';

import { useQuery } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useEffect, useMemo, useState } from 'react';

import { Icon, Select } from '@dthcms/ui';

import { useRealtimeTopics } from '@/features/realtime';
import { formatDate } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';

import {
  omissionFor,
  readSpans,
  spansKey,
  visibleLanes,
  type TimelineOmission,
} from '../api/spans';

import { TimelineChart, type ChartHover } from './TimelineChart';
import { TimelineDetail } from './TimelineDetail';
import { TIMELINE_LANES, codeLabel, laneLabel } from './timelineText';

/**
 * §8's longitudinal timeline: one screen, one time axis, a decade at once (CP74).
 *
 * # What this component owns and what it does not
 *
 * It owns the *question* — which window, which overlays, which lanes — and the chart owns
 * the picture. That split is deliberate: the window and the overlay set are what change the
 * request, and keeping them here means every re-request is visible in one file rather than
 * hidden in a chart's effect.
 *
 * # Why the whole record is fetched once and the window is applied in the browser
 *
 * The obvious design re-requests on every zoom. It is wrong here for a reason that is
 * specific to this screen: zooming is the *interaction*, at pointer rate, and a request per
 * frame would make the acceptance criterion a property of the clinic's wifi. The response is
 * bounded by what can be drawn — a decade of this clinic's deepest patient is a few hundred
 * marks and a few thousand points — so it is fetched once and panned locally, and the range
 * control is what changes the request.
 *
 * The range control still exists, and not only as a convenience: `earliest` and `latest`
 * come back whatever window was asked for, so a physician who narrows to last year can still
 * be told the record starts in 2016 — which is the difference between "nothing happened" and
 * "nothing happened *in this window*".
 *
 * # Why the lane filter hides rather than re-requests
 *
 * A lane the reader turned off is a drawing decision, not a data one. Re-requesting would
 * mean the chart briefly had no lanes at all while it reloaded, and would make an interface
 * toggle cost a round trip on a shared connection.
 */

export interface PatientTimelineProps {
  patientId: string;
}

/** The overlays §8 names. Kept short: past four series on one axis nothing is legible. */
const OFFERED_SERIES = [
  'HBA1C',
  'BODY_WEIGHT',
  'BP_SYSTOLIC',
  'BP_DIASTOLIC',
  'GLUCOSE_FASTING',
  'BMI',
];

const DEFAULT_SERIES = ['HBA1C', 'BODY_WEIGHT', 'BP_SYSTOLIC', 'BP_DIASTOLIC'];

/**
 * Lanes that start turned off, and the argument for each.
 *
 * `observations` is every measurement as an event, and on this screen the measurements are
 * already the value plot underneath — five hundred dots in a 26-pixel row saying what the
 * lines below say better. It is offered in the filter and can be turned on, because the lane
 * also carries the measurements that are *not* numbers (an examination finding, a text
 * result) and a physician looking for those needs a way to see them.
 *
 * This is the only lane hidden by default. Hiding anything else would mean the chart's first
 * impression left out part of the record, which is the thing a physician is reading it for.
 */
const HIDDEN_BY_DEFAULT = ['observations'];

/** The windows the range control offers, in months. `null` is the whole record. */
const RANGES = [12, 36, 120, null] as const;

export function PatientTimeline({ patientId }: PatientTimelineProps) {
  const t = useTranslations('timeline');
  const locale = useLocale() as Locale;

  const [months, setMonths] = useState<number | null>(null);
  const [chosen, setChosen] = useState<readonly string[]>(DEFAULT_SERIES);
  const [hiddenLanes, setHiddenLanes] = useState<ReadonlySet<string>>(
    () => new Set(HIDDEN_BY_DEFAULT),
  );
  const [focused, setFocused] = useState<string | null>(null);
  const [hover, setHover] = useState<ChartHover | null>(null);
  const [compact, setCompact] = useState(false);

  // A tablet held upright, or a phone. Measured rather than assumed from a user agent: the
  // same tablet is wide in one hand and narrow in the other, and this screen has to work in
  // both without a reload.
  useEffect(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return;
    const query = window.matchMedia('(max-width: 52rem)');
    const apply = () => setCompact(query.matches);
    apply();
    query.addEventListener('change', apply);
    return () => query.removeEventListener('change', apply);
  }, []);

  // A value typed at a station appears on the chart without a refresh. The key sits under
  // the patient's prefix, so this needs no new routing in the shared message router.
  useRealtimeTopics([`patient:${patientId}`]);

  const from = useMemo(() => {
    if (months === null) return undefined;
    const at = new Date();
    at.setMonth(at.getMonth() - months);
    return at.toISOString().slice(0, 10);
  }, [months]);

  const request = useMemo(
    () => ({ patientId, ...(from === undefined ? {} : { from }), series: chosen }),
    [patientId, from, chosen],
  );

  const { data, isPending, isError, refetch } = useQuery({
    queryKey: spansKey(request),
    queryFn: () => readSpans(request),
  });

  /*
   * The chart's props, memoised — and this is a frame-rate fix rather than tidiness.
   *
   * The hovered value is state, and it lives here because the panel that draws it is a
   * sibling of the chart. So **every pointer move re-renders this component**. The first
   * version computed `lanes` and the axis domain inline, which meant a fresh array on every
   * one of those renders; the chart's own `useMemo`s are keyed on them, so all of them
   * missed, and a scrub re-sliced and re-stringified four series of two thousand points at
   * pointer rate. Measured: 17fps. It also silently reset the zoom on every pointer move,
   * because the effect that resets the transform when the range changes is keyed on the same
   * array.
   *
   * These are computed before the early returns because they are hooks, which is why they
   * carry their own "no data yet" cases rather than sitting below the guards.
   */
  const lanes = useMemo(
    () => (data === undefined ? [] : visibleLanes(data, hiddenLanes)),
    [data, hiddenLanes],
  );

  const span = useMemo(() => {
    const earliest = data?.earliest === undefined ? null : Date.parse(data.earliest);
    const latest = data?.latest === undefined ? null : Date.parse(data.latest);
    return { earliest, latest };
  }, [data]);

  // The axis' outer limit: the record's own span, widened by a month at each end so the
  // first and last mark are not drawn flush against the frame, where they read as clipped.
  /*
   * Which series the value axis belongs to, and **it is never nothing**.
   *
   * The first version let this be `null` until the reader chose, so the control read
   * "Select…" while the axis was already drawing somebody's numbers. On a chart where every
   * series is scaled to its own extent that is not a cosmetic gap: a physician reads a
   * systolic pressure of 70 off an axis that belongs to HbA1c, and the first honest reading
   * of the screen is alarm.
   *
   * So the axis' owner is derived rather than stored: the reader's choice while it is still
   * on the chart, and otherwise the first overlay that has values. Unchecking the focused
   * series moves the axis rather than orphaning it.
   */
  const axisOwner = useMemo(() => {
    const drawable = chosen.filter((code) =>
      (data?.series ?? []).some((entry) => entry.code === code && entry.points.length > 0),
    );
    if (focused !== null && drawable.includes(focused)) return focused;
    return drawable[0] ?? chosen[0] ?? null;
  }, [chosen, data, focused]);

  const shown = useMemo((): [number, number] => {
    const { earliest, latest } = span;
    if (earliest === null || latest === null) return [Date.now() - 365 * DAY, Date.now()];
    const start = earliest - 30 * DAY;
    const end = Math.max(latest, Date.now()) + 30 * DAY;
    const windowStart = from === undefined ? start : Date.parse(from);
    return [Math.max(start, windowStart), end];
  }, [span, from]);

  if (isPending) {
    return (
      <p className="tl-state" data-testid="timeline-loading">
        {t('loading')}
      </p>
    );
  }

  if (isError || data === undefined) {
    return (
      <div className="tl-state tl-state--error" role="alert" data-testid="timeline-error">
        <p>{t('failed')}</p>
        <button type="button" className="tl-retry" onClick={() => void refetch()}>
          {t('retry')}
        </button>
      </div>
    );
  }

  const withheldSeries = omissionFor(data, 'series');
  const truncated = data.omitted.find((entry) => entry.truncated === true);
  const { earliest, latest } = span;
  const nothingOnRecord = earliest === null;

  return (
    <section className="tl" data-testid="timeline">
      <div className="tl__controls">
        <fieldset className="tl__range">
          <legend>{t('range.legend')}</legend>
          {RANGES.map((option) => (
            <button
              key={String(option)}
              type="button"
              className="tl__rangeButton"
              aria-pressed={months === option}
              data-testid={`timeline-range-${option ?? 'all'}`}
              onClick={() => setMonths(option)}
            >
              {option === null ? t('range.all') : t('range.months', { n: option })}
            </button>
          ))}
        </fieldset>

        {axisOwner !== null && (
          // Offered only when there is an axis to own. With every overlay turned off there is
          // no value plot to label, and a control naming a series nobody is drawing would be
          // a control that does nothing.
          <Select
            label={t('overlays')}
            value={axisOwner}
            options={chosen.map((code) => ({ value: code, label: codeLabel(code, t) }))}
            onChange={(event) => setFocused(event.target.value)}
            data-testid="timeline-axis-select"
          />
        )}
      </div>

      {/*
        Which numbers are drawn. Checkboxes rather than a multi-select, because this is
        read on a tablet with a thumb and a native multi-select on Android is a modal list
        with no visible state until it is opened.
      */}
      <fieldset className="tl__series" data-testid="timeline-series-filter">
        <legend>{t('seriesLegend')}</legend>
        {OFFERED_SERIES.map((code) => (
          <label key={code} className="tl__check">
            <input
              type="checkbox"
              checked={chosen.includes(code)}
              onChange={(event) =>
                setChosen((current) =>
                  event.target.checked
                    ? [...current, code]
                    : current.filter((entry) => entry !== code),
                )
              }
            />
            {codeLabel(code, t)}
          </label>
        ))}
      </fieldset>

      <fieldset className="tl__lanes" data-testid="timeline-lane-filter">
        <legend>{t('lanesLegend')}</legend>
        {TIMELINE_LANES.map((key) => {
          const present = data.lanes.some((lane) => lane.key === key && lane.marks.length > 0);
          return (
            <label key={key} className="tl__check" data-present={present}>
              <input
                type="checkbox"
                checked={!hiddenLanes.has(key)}
                // A lane this patient has nothing on is offered and disabled rather than
                // hidden. Hiding it would make the filter's contents depend on the patient,
                // so a physician who used it yesterday would find it gone today and have no
                // way to tell an empty lane from a lane that does not exist.
                disabled={!present}
                onChange={(event) =>
                  setHiddenLanes((current) => {
                    const next = new Set(current);
                    if (event.target.checked) next.delete(key);
                    else next.add(key);
                    return next;
                  })
                }
              />
              {laneLabel(key, t)}
            </label>
          );
        })}
      </fieldset>

      {withheldSeries !== undefined && <Withheld omission={withheldSeries} />}
      {truncated !== undefined && <Withheld omission={truncated} />}

      {nothingOnRecord ? (
        // "Nothing at all" and "nothing in this window" are different sentences, and the
        // response carries `earliest` whatever window was asked for precisely so this screen
        // can tell them apart.
        <p className="tl-state" data-testid="timeline-empty">
          {t('emptyRecord')}
        </p>
      ) : (
        <>
          <p className="tl__span" data-testid="timeline-span">
            {t('onRecord', {
              from: formatDate(earliest, locale),
              to: formatDate(latest ?? earliest, locale),
            })}
          </p>

          {lanes.length === 0 && (
            <p className="tl-state" data-testid="timeline-no-lanes">
              {t('noLanes')}
            </p>
          )}

          <div className="tl__plot">
            <TimelineChart
              lanes={lanes}
              series={data.series}
              domain={shown}
              focused={axisOwner}
              onFocus={setFocused}
              onHover={setHover}
              compact={compact}
            />

            {hover !== null && (
              <div
                className="tl__detail"
                // Anchored on a phone and floating on a desktop. A panel that followed the
                // finger on a touch screen would sit under the finger, which is the one
                // place it cannot be read.
                data-compact={compact}
                style={compact ? undefined : { left: hover.clientX, top: hover.clientY }}
              >
                <TimelineDetail hover={hover} />
              </div>
            )}
          </div>

          <p className="tl__hint">
            <Icon name="help-circle" aria-hidden /> {t('hint')}
          </p>
        </>
      )}
    </section>
  );
}

const DAY = 24 * 60 * 60 * 1000;

/**
 * A part of the answer that is not here, drawn as an absence rather than as an emptiness.
 *
 * The same idea as CP73's `WithheldNote` and deliberately not the same component: that one
 * takes a dashboard panel, whose omission names a `panel` and a `permission`, and this one
 * takes a chart part, whose omission names a `part` and distinguishes *withheld* from
 * *truncated*. Sharing them would mean one component with two optional shapes and a branch
 * in the middle, which is how the distinction the two of them exist to carry gets lost.
 *
 * The sentence comes from the server, in both languages, because the server is the only
 * party that knows whether a part was refused or merely too long.
 */
function Withheld({ omission }: { omission: TimelineOmission }) {
  const t = useTranslations('timeline');
  const locale = useLocale() as Locale;

  return (
    <p
      className="tl-note"
      role="status"
      data-withheld={omission.withheld}
      data-testid={`timeline-omitted-${omission.part}`}
    >
      <Icon name={omission.withheld ? 'shield-check' : 'help-circle'} aria-hidden />{' '}
      {locale === 'bn' ? omission.reason_bn : omission.reason_en}
      {omission.needs !== undefined && omission.needs !== '' && (
        <span className="tl-note__permission">
          {t('needsPermission', { permission: omission.needs })}
        </span>
      )}
    </p>
  );
}
