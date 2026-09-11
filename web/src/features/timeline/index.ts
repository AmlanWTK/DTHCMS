/**
 * `timeline` — §8's continuous, scrubbable, stock-chart-style view of a whole record (CP74).
 *
 * # The one sentence this feature is built around
 *
 * Acceptance criterion 4: *the correlation between an intervention and a value change is
 * visually apparent.* Every decision here is downstream of it.
 *
 * A drug is a **bar with a beginning**, drawn directly above the value plot on the same time
 * axis, with a tick dropping from its start into the plot. A physician's eye travels a few
 * pixels from "this started here" to "the line bent there". Forty refill dots would carry
 * the same data and none of the meaning, which is why the collapsing into spans happens on
 * the server rather than being left to whichever client draws it next.
 *
 * # The four properties worth knowing before changing anything here
 *
 * **The frame rate is a design constraint, not an optimisation.** Criterion 1 is ≥30fps with
 * ten years of data. Three things hold it: zoom is published to React once per animation
 * frame rather than per pointer event; only the visible window is read, by binary search;
 * and points are reduced to the pixel columns that actually exist, keeping each column's
 * extremes so no spike is lost. Undoing any one of them breaks the criterion silently — the
 * picture stays correct and the scrub starts to stutter. `web/test/timeline.test.tsx` holds
 * the assertions; the measured number is in `docs/timeline.md`.
 *
 * **Each series is scaled to its own extent, so the y-axis belongs to one of them.** HbA1c
 * between 5 and 12 and systolic pressure between 100 and 180 on one linear axis makes the
 * HbA1c a flat line at the bottom of the plot. The legend is therefore a control: pressing a
 * series moves the axis to it and says so. One axis over four differently-scaled series
 * would be a chart stating something false.
 *
 * **Attribution is one interaction, not two.** The panel uses CP61's component in its
 * `compact` variant, so the name is on screen the moment the panel is. A `disclosure`
 * variant inside a hover panel would make §4.2's promise cost two interactions on the one
 * surface whose whole purpose is that it costs one.
 *
 * **An open-ended bar is not a bar that ended at its last refill.** Everything past
 * `last_seen_at` on an unclosed prescription is hatched rather than solid, because that
 * stretch is the chart's inference and not the record's claim.
 */
export { PatientTimeline, type PatientTimelineProps } from './components/PatientTimeline';
export {
  TimelineChart,
  type ChartHover,
  type TimelineChartProps,
} from './components/TimelineChart';
export { TimelineDetail, type TimelineDetailProps } from './components/TimelineDetail';
export {
  SERIES_DASHES,
  SERIES_SHAPES,
  TIMELINE_LANES,
  codeLabel,
  flagLabel,
  laneLabel,
  seriesStyle,
  type SeriesShape,
  type TimelineLaneKey,
} from './components/timelineText';
export { useChartSize } from './components/useChartSize';
export { decimate, extentIn, nearestPoint, scaleValue, windowOf, type PlotPoint } from './lib/plot';
export {
  markEnd,
  markEvidenceEnd,
  omissionFor,
  readSpans,
  spansKey,
  standsToday,
  visibleLanes,
  type SpansRequest,
  type TimelineLane,
  type TimelineMark,
  type TimelineOmission,
  type TimelineSeries,
  type TimelineSeriesPoint,
  type TimelineSpans,
} from './api/spans';
