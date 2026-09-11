import type { TimelineSeriesPoint } from '../api/spans';

/**
 * The arithmetic behind the chart, kept out of the component (CP74).
 *
 * Two reasons, and the second is the one that matters. The first is ordinary: a pure
 * function can be tested without a layout engine, and jsdom has none.
 *
 * The second is acceptance criterion 1 — *smooth interaction (≥30fps) with 10 years of
 * data*. Every function here runs on **every frame of a scrub**, so each one is written to
 * be O(visible) rather than O(record), and the two places where that is not obvious are
 * commented. A criterion about frame rate is a criterion about these functions; putting them
 * inline in a component is how they quietly become O(n²) six months from now with nothing
 * saying so.
 */

/** A point reduced to what the drawing needs: when, how high, and which one it was. */
export interface PlotPoint {
  x: number;
  y: number;
  /** Index into the series' own points array, so the tooltip can find the whole record. */
  index: number;
  /**
   * Whether this value is still the one that stands.
   *
   * Decided here rather than in the component, and that is a frame-rate decision as much as
   * a tidiness one: the line is drawn through the standing points only, and computing that
   * with a `filter` in the render meant allocating a second array of thousands of objects on
   * every frame of a scrub.
   */
  stands: boolean;
}

/**
 * The slice of a sorted series that falls inside a time window.
 *
 * A binary search rather than a filter. That is the difference between reading 10,000 points
 * per frame and reading the two or three hundred that are on screen, and at 60 frames a
 * second over a decade-long record it is the difference between the criterion holding and
 * not.
 *
 * The bounds are widened by one on each side on purpose: the line has to leave the left edge
 * and arrive at the right one. Clipping to exactly the visible points draws a chart whose
 * lines start and stop inside the plot, which reads as missing data rather than as a window.
 */
export function windowOf(times: readonly number[], from: number, to: number): [number, number] {
  const first = Math.max(0, lowerBound(times, from) - 1);
  const last = Math.min(times.length, lowerBound(times, to) + 1);
  return [first, Math.max(first, last)];
}

/** The first index whose value is >= target. Plain binary search, no allocation. */
function lowerBound(values: readonly number[], target: number): number {
  let low = 0;
  let high = values.length;
  while (low < high) {
    const mid = (low + high) >> 1;
    if ((values[mid] as number) < target) low = mid + 1;
    else high = mid;
  }
  return low;
}

/**
 * The point nearest an x position, or `null` when there is nothing near enough.
 *
 * `null` rather than "whatever is closest" is the whole point of the `maxDistance` argument.
 * A scrub at 2019 on a patient whose only HbA1c is from 2024 must show **no** HbA1c, not the
 * 2024 one attached to a crosshair standing five years away from it. A tooltip that always
 * finds something is a tooltip that regularly lies about when a value was taken.
 */
export function nearestPoint(
  plotted: readonly PlotPoint[],
  x: number,
  maxDistance: number,
): PlotPoint | null {
  let best: PlotPoint | null = null;
  let bestDistance = Infinity;
  for (const point of plotted) {
    const distance = Math.abs(point.x - x);
    if (distance < bestDistance) {
      bestDistance = distance;
      best = point;
    }
  }
  return best !== null && bestDistance <= maxDistance ? best : null;
}

/**
 * The visible points of one series, reduced to what a screen of pixels can actually show.
 *
 * # Why this exists
 *
 * The plan's own test is *rendering with 10,000 timeline points*. A thousand-pixel-wide plot
 * has room for a thousand columns; drawing ten thousand SVG elements into it puts nine in
 * ten of them behind another one, costs ten times the layout, and looks identical.
 *
 * # Why it is min-and-max per column rather than "every tenth point"
 *
 * Subsampling loses spikes, and a spike is the single most clinically important shape on a
 * glucose or a blood-pressure series — a hypo the chart drew straight through is a hypo
 * nobody saw. Keeping the **extremes** of each pixel column preserves the envelope of the
 * data exactly: the drawn line touches every high and every low the full series reaches,
 * whatever the zoom.
 *
 * The first and last point of each column are kept as well so the line's direction within a
 * column is not inverted, which is what produces the sawtooth artefact this kind of
 * reduction is usually blamed for.
 */
export function decimate(
  points: readonly TimelineSeriesPoint[],
  times: readonly number[],
  from: number,
  to: number,
  x: (at: number) => number,
  y: (value: number) => number,
  pixels: number,
): PlotPoint[] {
  const [start, end] = windowOf(times, from, to);
  const count = end - start;
  if (count <= 0) return [];

  // At one point per pixel or fewer there is nothing to gain and a real cost: the reduction
  // itself is work, and a short series must be drawn exactly. Above it, the reduction is
  // what keeps the path short enough to re-stringify inside a frame.
  if (count <= pixels) {
    const out: PlotPoint[] = [];
    for (let index = start; index < end; index++) {
      const point = points[index] as TimelineSeriesPoint;
      out.push({
        x: x(times[index] as number),
        y: y(point.value_num),
        index,
        stands: stands(point),
      });
    }
    return out;
  }

  const out: PlotPoint[] = [];
  const columnWidth = (to - from) / Math.max(1, pixels);
  let column = Math.floor(((times[start] as number) - from) / columnWidth);
  let first = start;
  let last = start;
  let lowest = start;
  let highest = start;

  const flush = () => {
    // In time order, and de-duplicated, so the path is monotonic in x. A path that goes
    // backwards draws a hairline spike that looks exactly like a bad reading.
    const chosen = [first, lowest, highest, last]
      .sort((a, b) => a - b)
      .filter((index, position, all) => position === 0 || index !== all[position - 1]);
    for (const index of chosen) {
      const point = points[index] as TimelineSeriesPoint;
      out.push({
        x: x(times[index] as number),
        y: y(point.value_num),
        index,
        stands: stands(point),
      });
    }
  };

  for (let index = start; index < end; index++) {
    const at = times[index] as number;
    const which = Math.floor((at - from) / columnWidth);
    if (which !== column) {
      flush();
      column = which;
      first = lowest = highest = index;
    }
    last = index;
    const value = (points[index] as TimelineSeriesPoint).value_num;
    if (value < (points[lowest] as TimelineSeriesPoint).value_num) lowest = index;
    if (value > (points[highest] as TimelineSeriesPoint).value_num) highest = index;
  }
  flush();
  return out;
}

/**
 * The extent of a series inside a window, for scaling it to its own shape.
 *
 * `null` when the window holds nothing, which the caller draws as an absent line rather than
 * as a flat one at zero.
 */
export function extentIn(
  points: readonly TimelineSeriesPoint[],
  times: readonly number[],
  from: number,
  to: number,
): [number, number] | null {
  const [start, end] = windowOf(times, from, to);
  if (end <= start) return null;
  let low = Infinity;
  let high = -Infinity;
  for (let index = start; index < end; index++) {
    const value = (points[index] as TimelineSeriesPoint).value_num;
    if (value < low) low = value;
    if (value > high) high = value;
  }
  return Number.isFinite(low) ? [low, high] : null;
}

/**
 * A value's vertical position between two bounds.
 *
 * A flat series — a well-controlled patient, which is the outcome everybody is working
 * towards — has a zero range, and dividing by it would put every point at NaN and draw
 * nothing at all. It is centred instead, which is the honest picture: nothing moved.
 */
export function scaleValue(value: number, low: number, high: number, top: number, bottom: number) {
  if (high === low) return (top + bottom) / 2;
  return bottom - ((value - low) / (high - low)) * (bottom - top);
}

/**
 * Whether a value is still the one that stands.
 *
 * Duplicated from `standsToday` in the API module rather than imported, because this file is
 * the hot path and must not depend on the wire types beyond the two fields it reads. The two
 * are held together by the test named *a value that no longer stands*.
 */
function stands(point: TimelineSeriesPoint): boolean {
  const flags = point.flags;
  for (const flag of flags) {
    if (flag === 'corrected' || flag === 'superseded') return false;
  }
  return true;
}
