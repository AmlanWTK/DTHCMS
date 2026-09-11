'use client';

import { scaleLinear, scaleTime } from 'd3-scale';
import { line as d3line } from 'd3-shape';
import { select } from 'd3-selection';
import { zoom as d3zoom, zoomIdentity, type D3ZoomEvent, type ZoomTransform } from 'd3-zoom';
import { useLocale, useTranslations } from 'next-intl';
import {
  memo,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
} from 'react';

import {
  clinicalReadingDecimals,
  clinicalReadingUnit,
  fromClinicalReading,
  toClinicalReading,
} from '@dthcms/clinical-calc';

import { unitLabel } from '@/features/observations';
import { formatDate } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';

import {
  markEnd,
  markEvidenceEnd,
  type TimelineLane,
  type TimelineMark,
  type TimelineSeries,
  type TimelineSeriesPoint,
} from '../api/spans';
import { decimate, extentIn, nearestPoint, scaleValue, type PlotPoint } from '../lib/plot';

import { codeLabel, laneLabel, seriesStyle } from './timelineText';
import { useChartSize } from './useChartSize';

/**
 * §8's continuous, scrubbable, stock-chart-style view (CP74).
 *
 * # The one idea the whole chart is built around
 *
 * Acceptance criterion 4: *the correlation between an intervention and a value change is
 * visually apparent.* Everything below is downstream of that sentence.
 *
 * An intervention is drawn as a **bar with a beginning**, directly above the value plot, on
 * the same time axis, with no gap between the two — so the eye travels a few pixels from
 * "metformin starts here" to "the line bends there" rather than across a legend. Medications
 * are the lane nearest the plot for exactly that reason; a lane order chosen alphabetically
 * would put admissions between them.
 *
 * The scrub is the second half. A vertical rule follows the pointer across both halves at
 * once, and every series reads out its value at that instant while every bar under the rule
 * lights up. That is what turns "these two things look near each other" into "on this day,
 * this drug was running and this number was 9.4".
 *
 * # Why every series is scaled to its own extent, and why there is still a y-axis
 *
 * HbA1c lives between 5 and 12, weight between 60 and 90, systolic pressure between 100 and
 * 180. On one linear axis the HbA1c series is a flat line at the bottom of the plot and its
 * whole clinical story is invisible. So each series is scaled to **its own** minimum and
 * maximum *within the visible window*, which is the same honest scaling CP73's sparkline
 * uses and the same one every multi-ticker stock chart uses.
 *
 * That makes the vertical position of one series meaningless against another — so the axis
 * belongs to **one** series at a time. Clicking a series in the legend focuses it; the left
 * axis then shows that series' real numbers in its real unit, and the legend says whose axis
 * it is. Drawing one axis and letting the reader assume it applies to all four would be a
 * chart that states something false, which is worse than a chart with no axis.
 *
 * # Why the frame rate is a design constraint rather than an optimisation
 *
 * Criterion 1 is *smooth interaction (≥30fps) with 10 years of data*, and the plan's test is
 * 10,000 points. Three things make that hold, and each one would be easy to undo:
 *
 *   1. **Zoom does not go through React state at pointer rate.** d3-zoom fires faster than a
 *      frame; the transform is stashed in a ref and one `requestAnimationFrame` per frame
 *      publishes it. Without this, a fast trackpad flick queues dozens of renders that all
 *      land after the gesture has finished.
 *   2. **Only the visible window is read** — a binary search, not a filter — so panning a
 *      one-month window across a decade costs the same as drawing a one-month record.
 *   3. **Points are reduced to the pixel columns that exist**, keeping each column's extreme
 *      values so that no spike is lost. Ten thousand points into a thousand-pixel plot is a
 *      few hundred drawn elements and an identical picture.
 *
 * The measured number is in `docs/timeline.md`.
 *
 * # Why the marks are real elements and the points are not
 *
 * A lane mark is a `<rect>` or a `<circle>` with a `tabIndex` and a pointer handler, because
 * criterion 3 — *hovering any value shows its attribution* — has to work for a keyboard and
 * for a screen reader, and because there are tens of them. A series has thousands of points,
 * and ten thousand focusable elements is a tab order nobody can escape. So the series is
 * interrogated through the scrub, which the arrow keys drive as well as the pointer: focus
 * the plot, press a key, and the crosshair walks point by point through the focused series
 * with its attribution in the panel. Same promise, one interaction, no tab trap.
 */

/** The height of one event lane. Tuned so a tablet can hit a bar with a thumb. */
const LANE_HEIGHT = 26;
const LANE_BAR = 12;
/** How tall the value plot is. Generous, because it is the half a physician reads. */
const PLOT_HEIGHT = 240;
const PLOT_HEIGHT_COMPACT = 180;
const MARGIN = { top: 8, right: 12, bottom: 26, left: 52 };
/**
 * The gap between the last lane and the plot, where the axis writes whose numbers it is.
 *
 * A gap rather than a label floated over the lanes: the axis name sat on the last lane's own
 * name and the two overprinted, which on a chart whose whole point is knowing whose numbers
 * those are is the worst possible place for illegible text.
 */
const AXIS_LABEL_BAND = 16;
/** How near the pointer has to be, in pixels, before a value counts as the one under it. */
const HIT_RADIUS = 28;

export interface ChartHover {
  kind: 'mark' | 'point';
  mark?: TimelineMark;
  laneKey?: string;
  point?: TimelineSeriesPoint;
  code?: string;
  /**
   * Where to put the panel, in **viewport** coordinates.
   *
   * Viewport rather than container coordinates, and that is a frame-rate fix rather than a
   * preference. An absolutely-positioned panel inside the chart contributes to the
   * document's scrollable overflow, so a panel that follows the pointer grows and shrinks
   * the page under it — and on a window where the content is close to the viewport height,
   * the scrollbar appears and disappears on every frame of a scrub. That is a full-page
   * relayout per frame: measured at 22fps on a 1024×1366 tablet, against 59fps on a desktop
   * with the same data, and the only difference between the two was whether the page
   * happened to be scrolling already. A fixed panel is outside the flow and cannot do it.
   */
  clientX: number;
  clientY: number;
}

export interface TimelineChartProps {
  lanes: readonly TimelineLane[];
  series: readonly TimelineSeries[];
  /** The whole span on record — the outer limit of what zooming out shows. */
  domain: [number, number];
  /** The code whose numbers the left axis belongs to. */
  focused: string | null;
  onFocus: (code: string) => void;
  onHover: (hover: ChartHover | null) => void;
  /** Narrow layout: a phone, or a tablet held upright. */
  compact?: boolean;
}

/**
 * `memo`, and it is load-bearing.
 *
 * The hovered value is state in the parent, so every pointer move re-renders it. Without
 * this the chart re-rendered with it — four hundred lane marks reconciled and four series
 * re-drawn — at pointer rate, for a picture that had not changed. The crosshair itself is
 * state *inside* this component, so it still moves; what stops is everything else.
 */
export const TimelineChart = memo(function TimelineChart({
  lanes,
  series,
  domain,
  focused,
  onFocus,
  onHover,
  compact = false,
}: TimelineChartProps) {
  const t = useTranslations('timeline');
  const locale = useLocale() as Locale;
  const [box, width] = useChartSize();
  const svgRef = useRef<SVGSVGElement | null>(null);

  const [transform, setTransform] = useState<ZoomTransform>(zoomIdentity);
  // The transform d3 last produced, published to React once per frame. See the note above:
  // d3-zoom fires faster than the display refreshes, and a setState per event turns one
  // gesture into a backlog of renders that arrive after the finger has left the glass.
  const pending = useRef<ZoomTransform | null>(null);
  const frame = useRef<number | null>(null);
  /** Which value the panel is currently showing, so it is not told the same thing twice. */
  const shownPoint = useRef<string | null>(null);

  /*
   * Where each lane sits, and **how many rows it needs**.
   *
   * Two medications running at once are two bars on one lane, and drawn on one row the later
   * one covers the earlier: the chart then says a patient is on one drug when they are on
   * two, and adding a second agent — which is the commonest intervention there is — is
   * invisible. That is criterion 4 failing on the exact case it exists for.
   *
   * So a durative lane is packed like a calendar: bars are laid out in start order and each
   * goes on the first row whose previous bar has ended. Overlaps become rows; a lane with no
   * overlaps is one row and looks exactly as it did.
   *
   * Greedy is the right algorithm here and not a shortcut — for intervals sorted by start it
   * uses the minimum number of rows, which is what keeps a lane as short as it can honestly
   * be.
   */
  const layout = useMemo(() => {
    let top = MARGIN.top;
    const out = lanes.map((lane) => {
      const rows: number[] = [];
      const rowOf = lane.marks.map((mark) => {
        if (!lane.durative) return 0;
        const start = Date.parse(mark.occurred_at);
        const end = markEnd(mark, domain[1]) ?? start;
        const free = rows.findIndex((busyUntil) => busyUntil <= start);
        const row = free === -1 ? rows.length : free;
        rows[row] = end;
        return row;
      });
      const height = Math.max(1, rows.length) * LANE_HEIGHT;
      const placed = { lane, top, height, rowOf };
      top += height;
      return placed;
    });
    return { lanes: out, height: top - MARGIN.top };
  }, [lanes, domain]);

  const plotHeight = compact ? PLOT_HEIGHT_COMPACT : PLOT_HEIGHT;
  const lanesHeight = layout.height;
  const height = MARGIN.top + lanesHeight + AXIS_LABEL_BAND + plotHeight + MARGIN.bottom;
  const innerWidth = Math.max(0, width - MARGIN.left - MARGIN.right);
  const plotTop = MARGIN.top + lanesHeight + AXIS_LABEL_BAND;
  const plotBottom = plotTop + plotHeight;

  const xFull = useMemo(
    () => scaleTime().domain([domain[0], domain[1]]).range([0, innerWidth]),
    [domain, innerWidth],
  );
  const x = useMemo(() => transform.rescaleX(xFull), [transform, xFull]);
  const [from, to] = useMemo(() => {
    const [a, b] = x.domain() as [Date, Date];
    return [a.getTime(), b.getTime()];
  }, [x]);

  // The times of each series, as a plain sorted number array. Extracted once per payload
  // rather than per frame: `Date.parse` on ten thousand strings is a whole frame by itself,
  // and it is the same answer every time.
  const times = useMemo(() => {
    const out = new Map<string, number[]>();
    for (const entry of series) {
      out.set(
        entry.code,
        entry.points.map((point) => Date.parse(point.at)),
      );
    }
    return out;
  }, [series]);

  const columns = Math.max(1, Math.floor(innerWidth));

  /** Each series reduced to what is on screen, scaled to its own extent in this window. */
  const drawn = useMemo(() => {
    return series.map((entry, index) => {
      const at = times.get(entry.code) ?? [];
      const extent = extentIn(entry.points, at, from, to);
      const style = seriesStyle(index);
      if (extent === null) {
        return { series: entry, style, plotted: [] as PlotPoint[], extent: null };
      }
      const [low, high] = extent;
      // A little headroom so a maximum is not drawn flush against the top rule, where it
      // reads as clipped rather than as the highest value.
      const pad = high === low ? 1 : (high - low) * 0.12;
      const y = (value: number) =>
        scaleValue(value, low - pad, high + pad, plotTop + 6, plotBottom - 6);
      const plotted = decimate(
        entry.points,
        at,
        from,
        to,
        (instant) => x(new Date(instant)),
        y,
        columns,
      );
      return { series: entry, style, plotted, extent: [low - pad, high + pad] as [number, number] };
    });
  }, [series, times, from, to, x, columns, plotTop, plotBottom]);

  const focusedDrawn = drawn.find((entry) => entry.series.code === focused) ?? drawn[0];

  const path = useMemo(
    () =>
      d3line<PlotPoint>()
        .x((point) => point.x)
        .y((point) => point.y),
    [],
  );

  /*
   * The `d` strings, memoised on the geometry rather than rebuilt in the render.
   *
   * This is a frame-rate decision, and it is the one that mattered most. A scrub changes the
   * crosshair and nothing else, but it is a state change, so the component re-renders — and
   * building four path strings of a couple of thousand points each, on every pointer move,
   * is tens of milliseconds of string concatenation for a picture that has not changed.
   * Keyed on `drawn`, which only changes when the window does.
   */
  const paths = useMemo(
    () =>
      drawn.map((entry) => {
        const standing = entry.plotted.filter((point) => point.stands);
        return standing.length > 1 ? (path(standing) ?? '') : '';
      }),
    [drawn, path],
  );

  // --- the scrub ---

  const [scrub, setScrub] = useState<number | null>(null);
  const scrubPending = useRef<number | null>(null);

  const publishScrub = useCallback(() => {
    frame.current = null;
    if (pending.current !== null) {
      setTransform(pending.current);
      pending.current = null;
    }
    if (scrubPending.current !== null) {
      setScrub(scrubPending.current);
      scrubPending.current = null;
    }
  }, []);

  const schedule = useCallback(() => {
    if (frame.current !== null) return;
    frame.current = requestAnimationFrame(publishScrub);
  }, [publishScrub]);

  useEffect(() => {
    return () => {
      if (frame.current !== null) cancelAnimationFrame(frame.current);
    };
  }, []);

  // --- zoom and pan ---

  useEffect(() => {
    const element = svgRef.current;
    if (element === null || innerWidth <= 0) return;

    const behaviour = d3zoom<SVGSVGElement, unknown>()
      // The lower bound is 1 because the record is the record: zooming out past the whole
      // span would show empty years and invite somebody to read the gap as data. The upper
      // bound is what puts a single day across the plot on a decade-long record.
      .scaleExtent([1, 5000])
      .translateExtent([
        [0, 0],
        [innerWidth, 0],
      ])
      .extent([
        [0, 0],
        [innerWidth, 1],
      ])
      .on('zoom', (event: D3ZoomEvent<SVGSVGElement, unknown>) => {
        pending.current = event.transform;
        schedule();
      });

    const selection = select(element);
    selection.call(behaviour);
    // The double-click reset is removed on purpose: a physician double-clicking a
    // medication bar to read it would otherwise have the whole window jump underneath them,
    // and the range controls above the chart are where resetting belongs.
    selection.on('dblclick.zoom', null);
    return () => {
      selection.on('.zoom', null);
    };
  }, [innerWidth, schedule]);

  // A change of range resets the zoom, because the transform is relative to the domain and
  // keeping it would silently apply last month's magnification to a decade.
  useEffect(() => {
    pending.current = null;
    setTransform(zoomIdentity);
  }, [domain]);

  // --- pointer ---

  const pointerAt = useCallback((event: ReactPointerEvent<SVGSVGElement>) => {
    const rect = event.currentTarget.getBoundingClientRect();
    return {
      x: event.clientX - rect.left - MARGIN.left,
      y: event.clientY - rect.top,
      clientX: event.clientX,
      top: rect.top,
    };
  }, []);

  const onPointerMove = useCallback(
    (event: ReactPointerEvent<SVGSVGElement>) => {
      const at = pointerAt(event);
      if (at.x < 0 || at.x > innerWidth) {
        scrubPending.current = null;
        setScrub(null);
        shownPoint.current = null;
        onHover(null);
        return;
      }
      scrubPending.current = at.x;
      schedule();

      if (at.y < plotTop) {
        // In the lanes. The marks handle their own hover — they are real elements — so
        // nothing is decided here beyond clearing a stale value tooltip.
        return;
      }
      const focusedSeries = focusedDrawn;
      if (focusedSeries === undefined) return;
      const near = nearestPoint(focusedSeries.plotted, at.x, HIT_RADIUS);
      if (near === null) {
        if (shownPoint.current !== null) {
          shownPoint.current = null;
          onHover(null);
        }
        return;
      }
      const point = focusedSeries.series.points[near.index];
      if (point === undefined) return;
      // Only when the value under the pointer actually changes. A scrub crosses tens of
      // pixels per value on a decade-wide chart, and telling the parent about the same point
      // forty times re-renders the panel forty times for a panel that says the same thing.
      const identity = `${focusedSeries.series.code}:${near.index}`;
      if (identity === shownPoint.current) return;
      shownPoint.current = identity;
      onHover({
        kind: 'point',
        point,
        code: focusedSeries.series.code,
        clientX: at.clientX,
        clientY: at.top + near.y,
      });
    },
    [pointerAt, innerWidth, schedule, plotTop, focusedDrawn, onHover],
  );

  const onPointerLeave = useCallback(() => {
    scrubPending.current = null;
    setScrub(null);
    shownPoint.current = null;
    onHover(null);
  }, [onHover]);

  // --- the keyboard ---

  const [cursor, setCursor] = useState<number | null>(null);

  const step = useCallback(
    (direction: 1 | -1) => {
      const entry = focusedDrawn;
      if (entry === undefined || entry.series.points.length === 0) return;
      const next =
        cursor === null
          ? direction === 1
            ? 0
            : entry.series.points.length - 1
          : Math.min(entry.series.points.length - 1, Math.max(0, cursor + direction));
      setCursor(next);
      const point = entry.series.points[next];
      if (point === undefined) return;
      const at = Date.parse(point.at);
      const px = x(new Date(at));
      setScrub(px);
      const rect = svgRef.current?.getBoundingClientRect();
      onHover({
        kind: 'point',
        point,
        code: entry.series.code,
        clientX: (rect?.left ?? 0) + px + MARGIN.left,
        clientY: (rect?.top ?? 0) + plotTop,
      });
    },
    [focusedDrawn, cursor, x, onHover, plotTop],
  );

  const ticks = useMemo(() => x.ticks(compact ? 4 : 7), [x, compact]);
  /*
   * The axis' ticks, **chosen in the unit a clinician reads and placed in the unit the record
   * stores**.
   *
   * That order matters. Choosing them in the stored unit and converting the labels gives
   * gridlines at 7.6 % and 8.5 %; choosing them in the reading unit gives 7.5 % and 8.0 %,
   * which is what a person expects an axis to be marked in. Both directions come from CP44's
   * one table (`toClinicalReading` / `fromClinicalReading`), so the chart invents no
   * arithmetic of its own beside the record's.
   *
   * For every analyte whose clinical unit *is* its stored unit — which is all but HbA1c —
   * both conversions are the identity and this is the ordinary path.
   */
  const valueTicks = useMemo(() => {
    if (focusedDrawn === undefined || focusedDrawn.extent === null) return [];
    const [low, high] = focusedDrawn.extent;
    const unit = focusedDrawn.series.unit ?? '';
    return scaleLinear()
      .domain([toClinicalReading(low, unit), toClinicalReading(high, unit)])
      .ticks(4)
      .map((reading) => ({
        reading,
        y: scaleValue(fromClinicalReading(reading, unit), low, high, plotTop + 6, plotBottom - 6),
      }));
  }, [focusedDrawn, plotTop, plotBottom]);

  const axisDecimals = clinicalReadingDecimals(focusedDrawn?.series.unit ?? '');

  const scrubAt = scrub === null ? null : x.invert(scrub).getTime();

  return (
    <div className="tl-chart" ref={box} data-testid="timeline-chart">
      {width > 0 && (
        <svg
          ref={svgRef}
          className="tl-chart__svg"
          width={width}
          height={height}
          viewBox={`0 0 ${width} ${height}`}
          role="application"
          tabIndex={0}
          aria-label={t('chartLabel')}
          data-testid="timeline-svg"
          onPointerMove={onPointerMove}
          onPointerLeave={onPointerLeave}
          onKeyDown={(event) => {
            if (event.key === 'ArrowRight') {
              event.preventDefault();
              step(1);
            } else if (event.key === 'ArrowLeft') {
              event.preventDefault();
              step(-1);
            } else if (event.key === 'Escape') {
              setScrub(null);
              onHover(null);
            }
          }}
        >
          {/* The lanes' alternating grounds. Drawn first so everything sits on them, and
              kept very low contrast: they are a reading aid for which row is which, not a
              second thing to look at. */}
          {layout.lanes.map((placed, index) => (
            <rect
              key={placed.lane.key}
              className="tl-chart__laneGround"
              data-odd={index % 2 === 1}
              x={0}
              y={placed.top}
              width={width}
              height={placed.height}
            />
          ))}

          {/* The time axis. Above the plot's bottom rule rather than below it, because the
              lanes and the plot share it and a reader should not have to look past the
              plot to date a medication bar. */}
          <g className="tl-chart__axis" transform={`translate(${MARGIN.left},0)`}>
            {ticks.map((tick) => (
              <g key={tick.getTime()} transform={`translate(${x(tick)},0)`}>
                <line className="tl-chart__gridline" y1={MARGIN.top} y2={plotBottom} />
                <text className="tl-chart__tick" y={plotBottom + 16} textAnchor="middle">
                  {formatDate(tick, locale)}
                </text>
              </g>
            ))}
          </g>

          {/* The value axis, and it belongs to one series. See the note at the top: a single
              axis over four differently-scaled series would be a chart stating something
              false. */}
          {/*
            The value axis, and **it belongs to one series and says so**.

            Each series is scaled to its own extent — HbA1c between 5 and 12 and systolic
            pressure between 100 and 180 on one linear axis makes the HbA1c a flat line — so a
            bare column of numbers beside four lines is a column a reader will attach to the
            wrong one. On a chart where systolic sits between the 60 and 80 gridlines, the
            first honest reading is that this patient is dying.

            So the ticks, the rule and the name all carry the owning series' identity: its
            tint, its dash pattern in the legend, and its name in words at the top of the
            axis. A reader who takes a number off it has been told four times whose it is, and
            the one that survives a photograph and a monochrome printout is the name.
          */}
          <g className="tl-chart__axis tl-chart__axis--value" data-slot={focusedDrawn?.style.slot}>
            <line
              className="tl-chart__axisRule"
              data-slot={focusedDrawn?.style.slot}
              x1={MARGIN.left}
              x2={MARGIN.left}
              y1={plotTop}
              y2={plotBottom}
            />
            {focusedDrawn !== undefined && (
              <text
                className="tl-chart__axisName"
                data-slot={focusedDrawn.style.slot}
                x={2}
                y={plotTop - 5}
              >
                {codeLabel(focusedDrawn.series.code, t)}
                {axisUnit(focusedDrawn.series.unit, locale) === ''
                  ? ''
                  : ` (${axisUnit(focusedDrawn.series.unit, locale)})`}
              </text>
            )}
            {valueTicks.map((tick) => (
              <text
                key={tick.reading}
                className="tl-chart__tick tl-chart__tick--value"
                data-slot={focusedDrawn?.style.slot}
                x={MARGIN.left - 8}
                y={tick.y + 4}
                textAnchor="end"
              >
                {/* One decimal count for the whole axis, from CP44's table rather than from
                    each number's magnitude — an axis reading 12.0, 10.0, 8.00, 6.00 makes a
                    reader look for a precision that changes halfway down it. */}
                {tick.reading.toFixed(axisDecimals)}
              </text>
            ))}
          </g>

          <g transform={`translate(${MARGIN.left},0)`}>
            {/* --- the lanes --- */}
            {layout.lanes.map((placed) => {
              const lane = placed.lane;
              return (
                <g key={lane.key} data-lane={lane.key}>
                  {lane.marks.map((mark, markIndex) => {
                    // Each bar on the row the packing gave it, so two drugs running at once
                    // are two visible bars rather than one drawn over the other.
                    const centre =
                      placed.top + (placed.rowOf[markIndex] ?? 0) * LANE_HEIGHT + LANE_HEIGHT / 2;
                    const start = Date.parse(mark.occurred_at);
                    const end = markEnd(mark, domain[1]);
                    const evidence = markEvidenceEnd(mark);
                    const x0 = x(new Date(start));
                    const active =
                      scrubAt !== null &&
                      scrubAt >= start &&
                      (end === null ? scrubAt <= start + DAY : scrubAt <= end);
                    const key = `${mark.event_id}-${mark.item ?? ''}-${mark.occurred_at}`;

                    if (!lane.durative || end === null) {
                      return (
                        <circle
                          key={key}
                          className="tl-chart__point"
                          data-active={active}
                          cx={x0}
                          cy={centre}
                          r={4.5}
                          tabIndex={0}
                          role="button"
                          aria-label={`${laneLabel(lane.key, t)} · ${mark.label_en}`}
                          onPointerEnter={(event) =>
                            onHover({
                              kind: 'mark',
                              mark,
                              laneKey: lane.key,
                              ...anchorOf(event.currentTarget),
                            })
                          }
                          onFocus={(event) =>
                            onHover({
                              kind: 'mark',
                              mark,
                              laneKey: lane.key,
                              ...anchorOf(event.currentTarget),
                            })
                          }
                          onBlur={() => onHover(null)}
                        />
                      );
                    }

                    const x1 = x(new Date(end));
                    const xe = evidence === null ? x1 : x(new Date(evidence));
                    return (
                      <g
                        key={key}
                        className="tl-chart__bar"
                        data-active={active}
                        data-open={mark.open_ended}
                        tabIndex={0}
                        role="button"
                        aria-label={`${laneLabel(lane.key, t)} · ${mark.label_en}`}
                        onPointerEnter={(event) =>
                          onHover({
                            kind: 'mark',
                            mark,
                            laneKey: lane.key,
                            ...anchorOf(event.currentTarget),
                          })
                        }
                        onFocus={(event) =>
                          onHover({
                            kind: 'mark',
                            mark,
                            laneKey: lane.key,
                            ...anchorOf(event.currentTarget),
                          })
                        }
                        onBlur={() => onHover(null)}
                      >
                        {/* The evidenced stretch: solid, because the record says so. */}
                        <rect
                          className="tl-chart__barBody"
                          x={x0}
                          y={centre - LANE_BAR / 2}
                          width={Math.max(2, xe - x0)}
                          height={LANE_BAR}
                          rx={3}
                        />
                        {/* Past the last refill on an unclosed prescription. Hatched rather
                            than solid: this stretch is inference — nobody wrote it down —
                            and drawing it like the rest would make the chart assert an end
                            date the record does not have. */}
                        {mark.open_ended && x1 > xe + 1 && (
                          <rect
                            className="tl-chart__barOpen"
                            x={xe}
                            y={centre - LANE_BAR / 2}
                            width={x1 - xe}
                            height={LANE_BAR}
                            rx={3}
                          />
                        )}
                        {/* The beginning, marked. This tick is what criterion 4 is about:
                            it is the thing a physician's eye lines up with the bend in the
                            line below. */}
                        <line
                          className="tl-chart__barStart"
                          x1={x0}
                          x2={x0}
                          y1={centre - LANE_HEIGHT / 2 + 2}
                          y2={plotBottom}
                          data-active={active}
                        />
                      </g>
                    );
                  })}
                </g>
              );
            })}

            {/* --- the value plot --- */}
            <rect
              className="tl-chart__plot"
              x={0}
              y={plotTop}
              width={innerWidth}
              height={plotHeight}
            />

            {drawn.map((entry, index) => (
              <g key={entry.series.code} data-series={entry.series.code}>
                {paths[index] !== '' && (
                  <path
                    className="tl-chart__line"
                    data-slot={entry.style.slot}
                    data-focused={entry.series.code === focusedDrawn?.series.code}
                    d={paths[index]}
                    fill="none"
                    strokeDasharray={entry.style.dash === 'none' ? undefined : entry.style.dash}
                  />
                )}
                {/* Points are drawn only when the window is sparse enough that each one is
                    its own dot. Past that they are noise on top of the line they describe,
                    and the scrub is how an individual value is read anyway. */}
                {entry.plotted.length <= 120 &&
                  entry.plotted.map((point) => (
                    <circle
                      key={`${entry.series.code}-${point.index}`}
                      className="tl-chart__dot"
                      data-slot={entry.style.slot}
                      data-replaced={!point.stands}
                      cx={point.x}
                      cy={point.y}
                      r={entry.series.code === focusedDrawn?.series.code ? 3 : 2.2}
                    />
                  ))}
              </g>
            ))}

            {/* --- the scrub --- */}
            {scrub !== null && (
              <g className="tl-chart__scrub" data-testid="timeline-scrub">
                <line x1={scrub} x2={scrub} y1={MARGIN.top} y2={plotBottom} />
                {drawn.map((entry) => {
                  const near = nearestPoint(entry.plotted, scrub, HIT_RADIUS);
                  if (near === null) return null;
                  return (
                    <circle
                      key={entry.series.code}
                      className="tl-chart__scrubDot"
                      data-slot={entry.style.slot}
                      cx={near.x}
                      cy={near.y}
                      r={4}
                    />
                  );
                })}
              </g>
            )}
          </g>

          {/* The lane names, over everything, on their own ground so a bar behind one does
              not make it unreadable. Left of the plot, in the axis gutter. */}
          {layout.lanes.map((placed) => (
            <text
              key={placed.lane.key}
              className="tl-chart__laneName"
              x={4}
              // Beside the lane's **first** row, so a lane that grew to three rows still reads
              // as one named thing rather than as three anonymous ones.
              y={placed.top + LANE_HEIGHT / 2 + 4}
            >
              {laneLabel(placed.lane.key, t)}
            </text>
          ))}
        </svg>
      )}

      {/*
        The chart's text alternative, and the reason it is not `aria-hidden`.

        A polyline read aloud is nothing, and a screen reader user needs the same two facts a
        sighted reader gets in a glance: what is on the chart, and over what span. The
        per-value answer is the scrub, which the arrow keys drive — so this says how to use
        it rather than repeating ten thousand numbers into a live region.
      */}
      <p className="tl-chart__alt" data-testid="timeline-alt">
        {t('altText', {
          lanes: lanes.length,
          series: series.filter((entry) => entry.points.length > 0).length,
          from: formatDate(from, locale),
          to: formatDate(to, locale),
        })}
      </p>

      {/* The legend is a control, not a caption: it says whose axis is on the left, and
          pressing one moves the axis. Every entry carries a shape and a dash as well as a
          tint, because a tablet near a window has no reliable hue and neither does a
          photograph of this screen sent to a consultant. */}
      <ul className="tl-chart__legend" data-testid="timeline-legend">
        {drawn.map((entry) => (
          <li key={entry.series.code}>
            <button
              type="button"
              className="tl-chart__legendItem"
              data-slot={entry.style.slot}
              data-focused={entry.series.code === focusedDrawn?.series.code}
              aria-pressed={entry.series.code === focusedDrawn?.series.code}
              onClick={() => onFocus(entry.series.code)}
            >
              <svg className="tl-chart__swatch" width={26} height={12} aria-hidden="true">
                <line
                  x1={1}
                  x2={25}
                  y1={6}
                  y2={6}
                  data-slot={entry.style.slot}
                  strokeDasharray={entry.style.dash === 'none' ? undefined : entry.style.dash}
                />
              </svg>
              <span>{codeLabel(entry.series.code, t)}</span>
              {axisUnit(entry.series.unit, locale) !== '' && (
                // The unit a clinician **reads**, not the unit the record stores. An HbA1c
                // legend reading `mmol/mol` beside an axis marked in NGSP % would be the two
                // halves of the same chart disagreeing.
                <span className="tl-chart__legendUnit">{axisUnit(entry.series.unit, locale)}</span>
              )}
              {entry.series.points.length === 0 && (
                // Said in words rather than left off the legend. A code the reader asked for
                // and this patient has none of is a clinical fact — "no HbA1c on record" —
                // and a series that silently vanished would look like one nobody asked for.
                <span className="tl-chart__legendEmpty">{t('noValues')}</span>
              )}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
});

const DAY = 24 * 60 * 60 * 1000;

/**
 * Where a panel about this element should sit, in viewport coordinates.
 *
 * Taken from the element rather than from the pointer, because half the ways into this panel
 * are not a pointer: a keyboard focus has no coordinates, and a tap's coordinates are under
 * the finger. The element's own top edge is right for all of them.
 */
function anchorOf(target: Element): { clientX: number; clientY: number } {
  // The bar's own body, where there is one. A duration bar's group also contains the tick
  // that drops from its start into the plot, so the group's box is the height of the whole
  // chart and its centre is nowhere near the bar — which put the panel over the filters
  // instead of over the thing it describes.
  const body = target.querySelector('.tl-chart__barBody') ?? target;
  const rect = body.getBoundingClientRect();
  return { clientX: rect.left + rect.width / 2, clientY: rect.top };
}

/**
 * The unit an axis or a legend prints, in the reader's language.
 *
 * CP44's `clinicalReadingUnit` decides *which* unit — the clinician's, not the record's —
 * and CP44's `unitLabel` decides how it is spelled, so `%#ngsp` reaches a physician as `%`
 * and never as a disambiguating suffix nobody outside the codebase has seen.
 */
function axisUnit(unit: string | undefined, locale: Locale): string {
  const canonical = unit ?? '';
  if (canonical === '') return '';
  return unitLabel(clinicalReadingUnit(canonical), locale === 'bn' ? 'bn' : 'en');
}
