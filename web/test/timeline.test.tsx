import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { clinicalReadingUnit, toClinicalReading } from '@dthcms/clinical-calc';

import type { Directory } from '@/features/attribution';

import { renderWithProviders } from './render';

/**
 * The longitudinal timeline (CP74, §8).
 *
 * The acceptance criteria are about a picture, and no test in jsdom can see one. What these
 * tests are for is the set of ways the picture would be quietly *wrong while looking right*,
 * which is this project's recurring failure and is worth listing:
 *
 *  - **A value with nobody's name against it.** Criterion 3 is *hovering any value shows its
 *    attribution*, and the two places it would be dropped are the two this file checks by
 *    rendering: a lane mark and a series point. Checking the JSON instead would prove the
 *    server sent a name, not that a physician can see one.
 *  - **A withheld series drawn as an empty chart.** "This patient has no HbA1c" and "you may
 *    not see their values" are opposite facts and the reassuring one is wrong.
 *  - **A bar that ends at its last refill.** That draws a patient as having stopped a drug
 *    they are still on. The evidence stretch and the inference stretch are different marks.
 *  - **A reduction that loses a spike.** Ten thousand points into a thousand pixels has to
 *    drop points, and dropping the wrong ones erases a hypoglycaemic episode. The rule is
 *    min-and-max per column, and there is an assertion here that a naive every-Nth
 *    implementation fails.
 *  - **A window search that is really a filter.** The frame rate depends on it being a
 *    binary search over the visible slice; a filter passes every rendering test and misses
 *    the criterion.
 *
 * The frame rate itself is measured in a real browser, against the running application, in
 * `e2e/timeline-performance.spec.ts`. A number produced in jsdom would be a number about
 * jsdom.
 */

const readSpans = vi.hoisted(() => vi.fn());

/*
 * Partial: the network call is stubbed and the rules are not. `markEnd`, `standsToday`,
 * `visibleLanes` and everything in `lib/plot` are what this feature *is*, and a test that
 * stubbed them would prove a component calls a function.
 */
vi.mock('@/features/timeline/api/spans', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/timeline/api/spans')>()),
  readSpans,
}));

const { PatientTimeline } = await import('@/features/timeline/components/PatientTimeline');
const { decimate, extentIn, nearestPoint, windowOf } = await import('@/features/timeline/lib/plot');
const { markEnd, markEvidenceEnd, standsToday, visibleLanes } =
  await import('@/features/timeline/api/spans');

const PATIENT = '0190a8f2-0000-7000-8000-0000000000a1';
const RINA = '0190a8f2-0000-7000-8000-0000000000c1';
const KABIR = '0190a8f2-0000-7000-8000-0000000000c2';

function directory(): Directory {
  return {
    staff: [
      { id: RINA, code: 'C001', name_en: 'Rina Akter', name_bn: 'রিনা আক্তার', status: 'active' },
      {
        id: KABIR,
        code: 'D004',
        name_en: 'Kabir Rahman',
        name_bn: 'কবির রহমান',
        status: 'active',
      },
    ],
    devices: [],
    stations: [
      { code: 'STN_LAB', name_en: 'Laboratory', name_bn: 'ল্যাবরেটরি', sequence: 6 },
      { code: 'STN_CONSULT', name_en: 'Consultation', name_bn: 'পরামর্শ', sequence: 9 },
    ],
    as_of: '2026-09-01T00:00:00Z',
  };
}

function mark(over: Record<string, unknown> = {}) {
  return {
    occurred_at: '2022-01-10T10:00:00Z',
    open_ended: true,
    last_seen_at: '2024-01-10T10:00:00Z',
    kind: 'medication.prescribed',
    label_en: 'Metformin 500mg',
    label_bn: 'মেটফরমিন ৫০০ মি.গ্রা.',
    flags: [] as string[],
    count: 24,
    event_id: '0190a8f2-0000-7000-8000-0000000000e1',
    actor_id: KABIR,
    actor_code: 'D004',
    actor_role: 'PHYSICIAN',
    actor_station: 'STN_CONSULT',
    source: 'STATION',
    recorded_at: '2022-01-10T10:20:00Z',
    ...over,
  };
}

function point(over: Record<string, unknown> = {}) {
  return {
    observation_id: '0190a8f2-0000-7000-8000-0000000000f1',
    at: '2022-03-01T09:05:00Z',
    value_num: 9.4,
    unit: '%',
    flags: [] as string[],
    actor_id: RINA,
    actor_code: '',
    actor_role: 'LAB_TECH',
    actor_station: 'STN_LAB',
    source: 'STATION',
    recorded_at: '2022-03-01T09:25:00Z',
    ...over,
  };
}

function view(over: Record<string, unknown> = {}) {
  return {
    lanes: [
      { key: 'medications', durative: true, marks: [mark()] },
      {
        key: 'investigations',
        durative: false,
        marks: [
          mark({
            kind: 'investigation.ordered',
            label_en: 'HbA1c',
            label_bn: 'এইচবিএ১সি',
            count: 1,
          }),
        ],
      },
    ],
    series: [
      {
        // The record's unit, which is IFCC. What a physician reads is NGSP %, and the chart
        // has to do that conversion — see the tests named for it.
        code: 'HBA1C',
        unit: 'mmol/mol',
        points: [point({ value_num: 79 }), point({ at: '2022-09-01T09:05:00Z', value_num: 62 })],
      },
    ],
    earliest: '2022-01-10T10:00:00Z',
    latest: '2024-06-01T10:00:00Z',
    omitted: [] as unknown[],
    ...over,
  };
}

/**
 * jsdom does not lay out, so every element measures zero and the chart — which deliberately
 * refuses to draw at a guessed width — would render nothing at all.
 *
 * Stubbing the measurement is honest here in a way that stubbing the component would not be:
 * what is faked is the browser's layout, and everything under test (the scales, the marks,
 * the hit-testing, the panel) is the real code running against a real width. A test that
 * stubbed `TimelineChart` would assert that `PatientTimeline` renders a component.
 */
const CHART_WIDTH = 900;

beforeEach(() => {
  readSpans.mockReset();
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (
    this: HTMLElement,
  ) {
    return {
      width: CHART_WIDTH,
      height: 400,
      top: 0,
      left: 0,
      right: CHART_WIDTH,
      bottom: 400,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    } as DOMRect;
  });
});

afterEach(() => {
  vi.restoreAllMocks();
});

// --- the arithmetic the frame rate depends on ------------------------------------------

describe('the window search', () => {
  const times = Array.from({ length: 1000 }, (_, index) => index * 1000);

  it('finds the slice by binary search and includes one point on each side', () => {
    // One on each side on purpose: the line has to leave the left edge and arrive at the
    // right one. Clipping to exactly the visible points draws lines that start and stop
    // inside the plot, which reads as missing data rather than as a window.
    const [first, last] = windowOf(times, 400_000, 500_000);
    expect(first).toBe(399);
    expect(last).toBe(501);
  });

  it('answers an empty slice for a window past the end of the record', () => {
    const [first, last] = windowOf(times, 5_000_000, 6_000_000);
    expect(last - first).toBeLessThanOrEqual(1);
  });

  it('is not a filter', () => {
    /*
     * The assertion that keeps the criterion honest. A `filter` over ten thousand points
     * gives the same answer and costs a hundred times as much per frame, and the difference
     * is invisible in every other test in this file — which is exactly how the frame-rate
     * criterion would stop holding with nothing going red.
     *
     * Counting comparisons is the only way to tell the two apart from the outside.
     */
    let comparisons = 0;
    const counted = new Proxy(times, {
      get(target, key) {
        if (typeof key === 'string' && /^\d+$/.test(key)) comparisons++;
        return Reflect.get(target, key);
      },
    }) as unknown as number[];

    windowOf(counted, 400_000, 500_000);
    // Two binary searches over 1,000 entries is about twenty reads. A filter is 1,000.
    expect(comparisons).toBeLessThan(60);
  });
});

describe('the reduction to pixel columns', () => {
  const start = Date.parse('2016-01-01T00:00:00Z');
  const DAY = 86_400_000;

  // Ten thousand points across a decade, with one deliberate spike: a hypoglycaemic reading
  // in the middle of an otherwise flat series. This is the shape the whole reduction rule
  // exists for.
  const points = Array.from({ length: 10_000 }, (_, index) =>
    // Deliberately **not** on a pixel-column boundary. The first version of this test put
    // the spike at index 5000, which is exactly where a column starts on a 900px plot — so a
    // naive "keep the first point of each column" reduction kept it by luck and the test
    // stayed green while the rule it exists for was gone. Found by breaking the code on
    // purpose and watching nothing happen.
    point({
      at: new Date(start + index * DAY * 0.365).toISOString(),
      value_num: index === 5005 ? 2.1 : 7,
    }),
  );
  const times = points.map((entry) => Date.parse(entry.at));
  const from = times[0] as number;
  const to = times[times.length - 1] as number;
  const x = (at: number) => ((at - from) / (to - from)) * CHART_WIDTH;
  const y = (value: number) => 200 - value * 10;

  it('draws far fewer elements than there are points', () => {
    const drawn = decimate(points, times, from, to, x, y, CHART_WIDTH);
    expect(points.length).toBe(10_000);
    // Four per column at the very most, and in practice far fewer.
    expect(drawn.length).toBeLessThan(CHART_WIDTH * 4);
    expect(drawn.length).toBeGreaterThan(10);
  });

  it('keeps the spike an every-Nth reduction would lose', () => {
    /*
     * The one that matters clinically. A glucose of 2.1 in the middle of a decade is a
     * hypoglycaemic episode; a chart that drew a straight line through it is a chart that
     * erased the single most important reading on the series.
     *
     * The naive implementation — take every Nth point — is written out here so that the
     * assertion is against a real alternative rather than against a feeling.
     */
    const drawn = decimate(points, times, from, to, x, y, CHART_WIDTH);
    expect(drawn.some((entry) => entry.index === 5005)).toBe(true);
    // And stated as the property rather than as one index: the lowest value the reduction
    // draws is the lowest value the series reaches. That is what "the envelope is preserved"
    // means, and it cannot be satisfied by luck.
    const lowest = Math.min(
      ...drawn.map((entry) => (points[entry.index] as { value_num: number }).value_num),
    );
    expect(lowest).toBe(2.1);

    // 5005 = 5 × 7 × 11 × 13, so several tidy strides happen to land on it. Three does not.
    const everyNth = points.filter((_, index) => index % 3 === 0);
    expect(everyNth.some((entry) => entry.value_num === 2.1)).toBe(false);
  });

  it('draws a short series exactly, with nothing dropped', () => {
    const few = points.slice(0, 20);
    const fewTimes = times.slice(0, 20);
    const drawn = decimate(
      few,
      fewTimes,
      fewTimes[0] as number,
      (fewTimes[19] as number) + 1,
      x,
      y,
      CHART_WIDTH,
    );
    expect(drawn.length).toBe(20);
  });

  it('keeps the drawn points in time order', () => {
    // A path that goes backwards in x draws a hairline spike that looks exactly like a bad
    // reading, which is the artefact this kind of reduction is usually blamed for.
    const drawn = decimate(points, times, from, to, x, y, CHART_WIDTH);
    const xs = drawn.map((entry) => entry.x);
    expect([...xs].sort((a, b) => a - b)).toEqual(xs);
  });
});

describe('the nearest value under the pointer', () => {
  it('answers nothing when the nearest value is far away', () => {
    // A scrub at 2019 on a patient whose only HbA1c is from 2024 must show no HbA1c. A
    // tooltip that always finds something is a tooltip that regularly lies about when a
    // value was taken.
    const plotted = [{ x: 800, y: 10, index: 0, stands: true }];
    expect(nearestPoint(plotted, 100, 28)).toBeNull();
    expect(nearestPoint(plotted, 790, 28)?.index).toBe(0);
  });
});

describe('the shape of a bar', () => {
  const latest = Date.parse('2024-06-01T10:00:00Z');

  it('runs an unclosed prescription to the edge of the record and not to its last refill', () => {
    // The failure this guards is the tempting one: ending the bar at the last refill draws a
    // patient as having stopped a drug they are still on.
    const running = mark();
    expect(markEnd(running, latest)).toBe(latest);
    expect(markEvidenceEnd(running)).toBe(Date.parse('2024-01-10T10:00:00Z'));
  });

  it('ends a closed bar where the record says it ended', () => {
    const stopped = mark({ open_ended: false, ended_at: '2023-05-01T10:00:00Z' });
    expect(markEnd(stopped, latest)).toBe(Date.parse('2023-05-01T10:00:00Z'));
    expect(markEvidenceEnd(stopped)).toBeNull();
  });

  it('gives a point mark no end at all', () => {
    expect(markEnd(mark({ open_ended: false, count: 1 }), latest)).toBeNull();
  });
});

describe('a value that no longer stands', () => {
  it('is kept on the chart and kept off the line', () => {
    // The record is the record. A chart that dropped a corrected reading would answer
    // "there was never a 14.2 here"; a line drawn through it would show a trend the patient
    // did not have. Both, which is why this is a predicate and not a filter at the source.
    expect(standsToday(point())).toBe(true);
    expect(standsToday(point({ flags: ['corrected'] }))).toBe(false);
    expect(standsToday(point({ flags: ['superseded'] }))).toBe(false);
  });
});

// --- the screen ------------------------------------------------------------------------

describe('the chart draws what came back', () => {
  it('renders a lane for each lane with marks, and none for a lane turned off', async () => {
    readSpans.mockResolvedValue(view());
    renderWithProviders(<PatientTimeline patientId={PATIENT} />, { directory: directory() });

    const svg = await screen.findByTestId('timeline-svg');
    expect(svg.querySelectorAll('[data-lane]')).toHaveLength(2);

    await userEvent.click(
      within(screen.getByTestId('timeline-lane-filter')).getByLabelText('Medicines'),
    );
    await waitFor(() =>
      expect(screen.getByTestId('timeline-svg').querySelectorAll('[data-lane]')).toHaveLength(1),
    );
  });

  it('offers a lane this patient has nothing on, disabled rather than missing', () => {
    /*
     * Hiding an empty lane would make the filter's contents depend on the patient, so a
     * physician who used it yesterday would find it gone today with nothing saying why —
     * and would have no way to tell "this lane is empty" from "this build has no such lane".
     */
    readSpans.mockResolvedValue(view());
    renderWithProviders(<PatientTimeline patientId={PATIENT} />, { directory: directory() });

    return waitFor(() => {
      const filter = within(screen.getByTestId('timeline-lane-filter'));
      expect(filter.getByLabelText('Admissions')).toBeDisabled();
      expect(filter.getByLabelText('Medicines')).toBeEnabled();
    });
  });

  it('draws the evidenced stretch of an open bar apart from the inferred one', async () => {
    // Everything past the last refill is the chart's inference, not the record's claim, and
    // drawing them identically would assert an end date nobody wrote down.
    readSpans.mockResolvedValue(view());
    renderWithProviders(<PatientTimeline patientId={PATIENT} />, { directory: directory() });

    const svg = await screen.findByTestId('timeline-svg');
    const bar = svg.querySelector('.tl-chart__bar[data-open="true"]');
    expect(bar).not.toBeNull();
    expect(bar?.querySelector('.tl-chart__barBody')).not.toBeNull();
    expect(bar?.querySelector('.tl-chart__barOpen')).not.toBeNull();
  });
});

describe('attribution reaches the panel', () => {
  it('names who prescribed a medication when its bar is hovered', async () => {
    /*
     * Criterion 3, on a lane mark, proved by rendering. Inspecting the JSON would prove the
     * server sent an id; what a physician needs is a name on screen after one interaction,
     * and the failure worth catching is the one where the panel renders a uuid or a blank.
     */
    readSpans.mockResolvedValue(view());
    renderWithProviders(<PatientTimeline patientId={PATIENT} />, { directory: directory() });

    const svg = await screen.findByTestId('timeline-svg');
    const bar = svg.querySelector('.tl-chart__bar') as SVGGElement;
    // `pointerOver` rather than `userEvent.hover`: React synthesises `onPointerEnter` from
    // the native `pointerover`, and this is the event a real pointer entering the bar
    // actually produces.
    fireEvent.pointerOver(bar);

    const panel = await screen.findByTestId('timeline-detail');
    expect(within(panel).getByText('Metformin 500mg')).toBeInTheDocument();
    // The name, not the id, and on screen without a second interaction — `compact`.
    expect(within(panel).getByTestId('attribution-summary')).toHaveTextContent('Kabir Rahman');
    // And the rest of the attribution, in the panel underneath it.
    expect(within(panel).getByTestId('attribution-station')).toHaveTextContent('Consultation');
    expect(within(panel).getByTestId('attribution-evidence')).toHaveTextContent(
      'Typed by an operator at a station.',
    );
  });

  it('names who measured a value when the chart is walked with the keyboard', async () => {
    /*
     * Criterion 3, on a series point. Driven from the keyboard rather than the pointer
     * because that is the path that would be forgotten: a chart with ten thousand focusable
     * points is a tab trap, so the arrow keys walk the scrub instead — and if that stopped
     * working, a mouse test would still pass.
     */
    readSpans.mockResolvedValue(view());
    renderWithProviders(<PatientTimeline patientId={PATIENT} />, { directory: directory() });

    const svg = await screen.findByTestId('timeline-svg');
    svg.focus();
    await userEvent.keyboard('{ArrowRight}');

    const panel = await screen.findByTestId('timeline-detail');
    // The number is drawn by CP44's component, not formatted here — [R-08], and the
    // repository's own audit is what caught this panel rendering it raw. How many decimals a
    // unit shows is that component's table, not this screen's, which is why the assertion is
    // about the component being there rather than about the digits.
    expect(within(panel).getByTestId('dual-unit')).toBeInTheDocument();
    expect(within(panel).getByTestId('attribution-summary')).toHaveTextContent('Rina Akter');
    expect(within(panel).getByTestId('attribution-station')).toHaveTextContent('Laboratory');
  });

  it('says in words that a charted value has been corrected', async () => {
    // A number read off a chart with no mark on it is taken as the value that stands. This
    // is the one thing a trend line is most likely to hide.
    readSpans.mockResolvedValue(
      view({
        series: [{ code: 'HBA1C', unit: '%', points: [point({ flags: ['corrected'] })] }],
      }),
    );
    renderWithProviders(<PatientTimeline patientId={PATIENT} />, { directory: directory() });

    const svg = await screen.findByTestId('timeline-svg');
    svg.focus();
    await userEvent.keyboard('{ArrowRight}');

    const panel = await screen.findByTestId('timeline-detail');
    expect(within(panel).getByText(/not the value that stands today/)).toBeInTheDocument();
  });
});

describe('what is not here is said, not left blank', () => {
  it('draws a withheld series as a refusal rather than as an empty chart', async () => {
    readSpans.mockResolvedValue(
      view({
        series: [{ code: 'HBA1C', points: [] }],
        omitted: [
          {
            part: 'series',
            needs: 'observation.read.values',
            reason_en: 'You were not shown the measured values on this record.',
            reason_bn: 'এই রেকর্ডের পরিমাপ করা মানগুলো আপনাকে দেখানো হয়নি।',
            withheld: true,
          },
        ],
      }),
    );
    renderWithProviders(<PatientTimeline patientId={PATIENT} />, { directory: directory() });

    const note = await screen.findByTestId('timeline-omitted-series');
    expect(note).toHaveTextContent('You were not shown the measured values');
    // The permission is named, because the remedy is a grant and whoever can make one has
    // to be told which.
    expect(note).toHaveTextContent('observation.read.values');
  });

  it('tells a patient with nothing on record apart from a window with nothing in it', async () => {
    readSpans.mockResolvedValue(
      view({ lanes: [], series: [], earliest: undefined, latest: undefined }),
    );
    renderWithProviders(<PatientTimeline patientId={PATIENT} />, { directory: directory() });

    expect(await screen.findByTestId('timeline-empty')).toBeInTheDocument();
  });

  it('says how long the record is, whatever window is being shown', async () => {
    readSpans.mockResolvedValue(view());
    renderWithProviders(<PatientTimeline patientId={PATIENT} />, { directory: directory() });

    const span = await screen.findByTestId('timeline-span');
    expect(span).toHaveTextContent('2022');
    expect(span).toHaveTextContent('2024');
  });
});

describe('it reads in Bangla', () => {
  it('names the lanes, the controls and a mark in Bengali', async () => {
    readSpans.mockResolvedValue(view());
    renderWithProviders(<PatientTimeline patientId={PATIENT} />, {
      locale: 'bn',
      directory: directory(),
    });

    const filter = within(await screen.findByTestId('timeline-lane-filter'));
    expect(filter.getByLabelText('ওষুধ')).toBeInTheDocument();

    const svg = screen.getByTestId('timeline-svg');
    fireEvent.pointerOver(svg.querySelector('.tl-chart__bar') as SVGGElement);

    const panel = await screen.findByTestId('timeline-detail');
    // The mark's own Bengali label, from the row rather than from a message file: a drug's
    // name is data and the projection carries both languages for exactly this.
    expect(within(panel).getByText('মেটফরমিন ৫০০ মি.গ্রা.')).toBeInTheDocument();
    expect(within(panel).getByTestId('attribution-summary')).toHaveTextContent('কবির রহমান');
  });
});

describe('lanes the reader has turned off', () => {
  it('are hidden without being re-requested', () => {
    const spans = view();
    expect(visibleLanes(spans as never, new Set(['medications']))).toHaveLength(1);
    expect(visibleLanes(spans as never, new Set())).toHaveLength(2);
    // A lane with no marks never draws, whatever the filter says: an empty row of a chart is
    // a row a reader spends a second deciding is empty.
    expect(
      visibleLanes(
        { ...spans, lanes: [{ key: 'visits', durative: false, marks: [] }] } as never,
        new Set(),
      ),
    ).toHaveLength(0);
  });
});

describe('the extent used to scale a series', () => {
  it('is the extent inside the window, not the extent of the record', () => {
    // Scaling to the whole record would flatten a zoomed-in window into a horizontal line —
    // which is the exact case a physician zooms in to look at.
    const points = [
      point({ at: '2018-01-01T00:00:00Z', value_num: 4 }),
      point({ at: '2020-01-01T00:00:00Z', value_num: 5 }),
      point({ at: '2022-01-01T00:00:00Z', value_num: 9 }),
      point({ at: '2022-06-01T00:00:00Z', value_num: 9.4 }),
      point({ at: '2024-01-01T00:00:00Z', value_num: 14 }),
      point({ at: '2026-01-01T00:00:00Z', value_num: 20 }),
    ];
    const times = points.map((entry) => Date.parse(entry.at));
    const inside = extentIn(
      points,
      times,
      Date.parse('2021-12-01T00:00:00Z'),
      Date.parse('2022-12-01T00:00:00Z'),
    );
    // Exactly one point either side of the window is included, deliberately: the line has to
    // arrive from somewhere and leave for somewhere, so the scale has to know where those
    // two neighbours are. Everything beyond them is not — the 4 from 2018 and the 20 from
    // 2026 are what a record-wide extent would have flattened this window against.
    expect(inside).toEqual([5, 14]);
  });
});

// --- the units a clinician reads ---------------------------------------------------------

describe('HbA1c is drawn in NGSP %, not in the unit the record stores', () => {
  /*
   * The clinic reads and prescribes in NGSP %. The record stores IFCC mmol/mol, correctly —
   * it is the interoperable unit and the one the database converts into — and the two are a
   * factor and an offset apart, not a rename.
   *
   * A chart whose legend says `mmol/mol` and whose axis is marked 60, 70, 80 makes a
   * physician convert in their head on the one analyte this clinic exists for. 66 mmol/mol
   * is 8.2 %; a reader scanning for "is this patient controlled" sees 66 and has to stop.
   *
   * The conversion is CP44's, from CP44's table, so the next analyte whose clinical unit is
   * not its storage unit is one line there and nothing here.
   */
  it('marks the axis in per cent and names the unit as %', async () => {
    readSpans.mockResolvedValue(view());
    renderWithProviders(<PatientTimeline patientId={PATIENT} />, { directory: directory() });

    const svg = await screen.findByTestId('timeline-svg');
    const name = svg.querySelector('.tl-chart__axisName');
    expect(name?.textContent).toContain('HbA1c');
    expect(name?.textContent).toContain('%');
    expect(name?.textContent).not.toContain('mmol/mol');

    // The ticks are NGSP numbers. The series runs 62–79 mmol/mol, which is 7.8–9.4 %; a
    // tick above 20 could only be an IFCC number that nobody converted.
    const ticks = [...svg.querySelectorAll('.tl-chart__tick--value')].map((node) =>
      Number(node.textContent),
    );
    expect(ticks.length).toBeGreaterThan(1);
    for (const tick of ticks) expect(tick).toBeLessThan(20);
  });

  it('names the reading unit in the legend too', async () => {
    // The legend and the axis are two halves of one chart. A legend reading `mmol/mol` beside
    // an axis marked in per cent is worse than either alone: it tells a reader the axis is
    // IFCC.
    readSpans.mockResolvedValue(view());
    renderWithProviders(<PatientTimeline patientId={PATIENT} />, { directory: directory() });

    const legend = await screen.findByTestId('timeline-legend');
    expect(legend.textContent).toContain('%');
    expect(legend.textContent).not.toContain('mmol/mol');
  });

  it('leaves a unit with no second reading exactly as the record spells it', () => {
    // One analyte wide. A weight that started reading in pounds on the axis would be a worse
    // bug than the one this fixed.
    expect(clinicalReadingUnit('kg')).toBe('kg');
    expect(toClinicalReading(69.9, 'kg')).toBe(69.9);
  });
});

describe('the value axis always belongs to a named series', () => {
  /*
   * Every series is scaled to its own extent, because a shared linear axis flattens HbA1c
   * into a line at the bottom of the plot. That is the right call and it has a cost: a column
   * of numbers beside four lines is a column a reader attaches to the wrong one.
   *
   * The failure is not cosmetic. On the corpus that produced this checkpoint's screenshots,
   * HbA1c, weight and both pressures all sat between the 60 and 80 gridlines — so a physician
   * reads a systolic of 70 off the axis, which is not a survivable blood pressure. The first
   * honest reaction to that screen is alarm and the second is distrust.
   */
  it('never draws an axis nobody owns', async () => {
    readSpans.mockResolvedValue(view());
    renderWithProviders(<PatientTimeline patientId={PATIENT} />, { directory: directory() });

    // The control is on a real series from the first frame — no "Select…" state.
    const select = (await screen.findByTestId('timeline-axis-select')) as HTMLSelectElement;
    expect(select.value).toBe('HBA1C');
    // And the axis says whose it is, in words rather than only in a tint.
    const svg = screen.getByTestId('timeline-svg');
    expect(svg.querySelector('.tl-chart__axisName')?.textContent).toContain('HbA1c');
  });

  it('moves the axis when its series is turned off rather than orphaning it', async () => {
    /*
     * The state that would otherwise reintroduce the defect from the other direction: the
     * reader turns off the series the axis belongs to, and the numbers stay while their owner
     * leaves the chart.
     */
    readSpans.mockResolvedValue(
      view({
        series: [
          { code: 'HBA1C', unit: 'mmol/mol', points: [point({ value_num: 79 })] },
          { code: 'BODY_WEIGHT', unit: 'kg', points: [point({ value_num: 71.2 })] },
        ],
      }),
    );
    renderWithProviders(<PatientTimeline patientId={PATIENT} />, { directory: directory() });

    await screen.findByTestId('timeline-svg');
    /*
     * Chosen explicitly first, and that is the whole point of this test rather than a
     * flourish. Without it `focused` is still null, the derivation falls back for a different
     * reason, and the assertion passes against a build that keeps a dead choice — which is
     * exactly what happened when this was broken on purpose to check.
     */
    await userEvent.selectOptions(screen.getByTestId('timeline-axis-select'), 'HBA1C');
    expect(
      screen.getByTestId('timeline-svg').querySelector('.tl-chart__axisName')?.textContent,
    ).toContain('HbA1c');

    await userEvent.click(
      within(screen.getByTestId('timeline-series-filter')).getByLabelText('HbA1c'),
    );

    await waitFor(() => {
      const name = screen.getByTestId('timeline-svg').querySelector('.tl-chart__axisName');
      expect(name?.textContent).toContain('Weight');
    });
  });
});

describe('two treatments running at once are two visible bars', () => {
  /*
   * The commonest intervention in this clinic is *adding* a second agent, and on one row the
   * later bar covers the earlier one — so the chart says a patient is on one drug when they
   * are on two, and the change a physician came to the screen to see is invisible. That is
   * criterion 4 failing on the exact case it exists for.
   *
   * Overlapping bars are packed into rows, calendar-style. A lane with no overlaps stays one
   * row, which is the assertion in the second half: growing every lane would cost the screen
   * a third of its height for nothing.
   */
  it('puts overlapping bars on different rows', async () => {
    readSpans.mockResolvedValue(
      view({
        lanes: [
          {
            key: 'medications',
            durative: true,
            marks: [
              mark({ label_en: 'Metformin 500mg', occurred_at: '2020-02-05T10:00:00Z' }),
              mark({
                label_en: 'Empagliflozin 10mg',
                occurred_at: '2021-10-17T10:00:00Z',
                event_id: '0190a8f2-0000-7000-8000-0000000000e2',
              }),
            ],
          },
        ],
      }),
    );
    renderWithProviders(<PatientTimeline patientId={PATIENT} />, { directory: directory() });

    const svg = await screen.findByTestId('timeline-svg');
    const bars = [...svg.querySelectorAll('.tl-chart__bar .tl-chart__barBody')];
    expect(bars).toHaveLength(2);
    const ys = bars.map((bar) => bar.getAttribute('y'));
    expect(new Set(ys).size, 'two drugs running at once were drawn on one row').toBe(2);
  });

  it('leaves a lane whose bars never overlap on one row', async () => {
    readSpans.mockResolvedValue(
      view({
        lanes: [
          {
            key: 'medications',
            durative: true,
            marks: [
              mark({
                label_en: 'Gliclazide 80mg',
                occurred_at: '2020-02-05T10:00:00Z',
                ended_at: '2021-01-01T10:00:00Z',
                open_ended: false,
                last_seen_at: undefined,
              }),
              mark({
                label_en: 'Empagliflozin 10mg',
                occurred_at: '2021-10-17T10:00:00Z',
                event_id: '0190a8f2-0000-7000-8000-0000000000e2',
              }),
            ],
          },
        ],
      }),
    );
    renderWithProviders(<PatientTimeline patientId={PATIENT} />, { directory: directory() });

    const svg = await screen.findByTestId('timeline-svg');
    const bars = [...svg.querySelectorAll('.tl-chart__bar .tl-chart__barBody')];
    expect(bars).toHaveLength(2);
    expect(new Set(bars.map((bar) => bar.getAttribute('y'))).size).toBe(1);
  });
});
