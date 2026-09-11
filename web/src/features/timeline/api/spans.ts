import type { components } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';

/**
 * The scrubbable timeline's read (CP74, §8).
 *
 * # Why this is not `GET /v1/patients/{id}/timeline`
 *
 * CP37's route answers *what happened, newest first, a page at a time*, capped at 500 rows.
 * Its own comment says why: "a decade of a diabetic patient's history is thousands of rows
 * and nothing renders them all at once."
 *
 * This screen is the one that must render a decade at once, so that justification stops
 * covering it — and the two obvious answers were both wrong. Raising the cap raises it for
 * every caller of a paging list. Paging twenty times puts twenty round trips in front of the
 * first pixel on a screen whose acceptance criterion is *smooth interaction*.
 *
 * `/timeline/spans` is bounded by **what can be drawn**. A medication a patient has been on
 * for two years is one bar with a beginning, not forty refill rows, and that fold happens on
 * the server so that two clients cannot do it two ways.
 *
 * # Why the key sits under the patient's prefix
 *
 * `['patient', id, 'timeline', …]`, so CP27's realtime invalidations reach it without this
 * feature teaching the shared message router about a new key — every message carrying a
 * `patient_id` invalidates `['patient', id]`, which is a prefix of this. A value typed at a
 * station appears on the chart without a refresh.
 */

export type TimelineSpans = components['schemas']['PatientTimelineSpans'];
export type TimelineLane = components['schemas']['TimelineLane'];
export type TimelineMark = components['schemas']['TimelineMark'];
export type TimelineSeries = components['schemas']['TimelineSeries'];
export type TimelineSeriesPoint = components['schemas']['TimelineSeriesPoint'];
export type TimelineOmission = components['schemas']['TimelineOmission'];

/** What the screen asks the server for. */
export interface SpansRequest {
  patientId: string;
  from?: string;
  to?: string;
  /**
   * The codes to overlay.
   *
   * An empty array is a real request — the reader turned every overlay off — and is sent as
   * `series=`, which the server does not round up to its defaults. `undefined` means "use
   * the defaults", which is what the first load asks for.
   */
  series?: readonly string[];
}

export function spansKey(request: SpansRequest) {
  return [
    'patient',
    request.patientId,
    'timeline',
    'spans',
    request.from ?? null,
    request.to ?? null,
    request.series === undefined ? null : [...request.series].join(','),
  ] as const;
}

export async function readSpans(request: SpansRequest): Promise<TimelineSpans> {
  const query: Record<string, string> = {};
  if (request.from !== undefined) query.from = request.from;
  if (request.to !== undefined) query.to = request.to;
  if (request.series !== undefined) query.series = request.series.join(',');

  return unwrap(
    api.GET('/v1/patients/{id}/timeline/spans', {
      params: { path: { id: request.patientId }, query },
    }),
  ) as Promise<TimelineSpans>;
}

/**
 * Whether a part of the answer was withheld, and why.
 *
 * A helper rather than a `find` at each call site, for the reason CP73's `omissionFor` is
 * one: the question decides which of two opposite sentences a panel draws — "this patient
 * has no measured values" against "you were not shown them" — and the reassuring one is the
 * wrong one.
 */
export function omissionFor(view: TimelineSpans, part: string): TimelineOmission | undefined {
  return view.omitted.find((entry) => entry.part === part);
}

/**
 * The lanes in the order §8 reads them, filtered to the ones the reader has turned on.
 *
 * The server already sends them in that order; this exists so that a lane the reader has
 * hidden disappears from the chart without disappearing from the filter, which is the
 * difference between a control and a bug.
 */
export function visibleLanes(view: TimelineSpans, hidden: ReadonlySet<string>): TimelineLane[] {
  return view.lanes.filter((lane) => !hidden.has(lane.key) && lane.marks.length > 0);
}

/**
 * When a mark stops, for drawing.
 *
 * Three cases, and collapsing any two of them would make the chart claim something the
 * record does not say:
 *
 *   - a **closed** bar ends where the record says it ended;
 *   - an **open-ended** bar runs to the right edge of what is on record, because "still on
 *     it" is what an unclosed prescription means — and the part beyond `last_seen_at` is
 *     drawn differently, because that stretch is inference rather than evidence;
 *   - a **point** mark has no end at all.
 */
export function markEnd(mark: TimelineMark, latest: number): number | null {
  if (mark.ended_at !== undefined) return Date.parse(mark.ended_at);
  if (mark.open_ended) return latest;
  return null;
}

/** Where the evidence for an open-ended bar stops. `null` when the bar is closed. */
export function markEvidenceEnd(mark: TimelineMark): number | null {
  if (!mark.open_ended) return null;
  return mark.last_seen_at === undefined ? null : Date.parse(mark.last_seen_at);
}

/**
 * The points a trend line may be drawn through.
 *
 * A value that was corrected or superseded is **not** dropped from the response — the record
 * is the record, and a chart that hid the reading somebody corrected would answer "there was
 * never a 14.2 here". It is dropped from the *line*, because a line through a value that has
 * been replaced is a trend the patient did not have. The point itself is still drawn, and
 * still carries its attribution.
 */
export function standsToday(point: TimelineSeriesPoint): boolean {
  return !point.flags.includes('corrected') && !point.flags.includes('superseded');
}
