import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { GrowthScreen } from '@/features/growth/components/GrowthScreen';

import { renderWithProviders } from './render';

/**
 * The growth screen as a clinician meets it (CP48, [R-06]).
 *
 * A child is sitting in front of the physician and the screen has four seconds to answer one
 * question: is this child's weight a problem? What is asserted here is the sequence of things
 * that decide whether it does.
 *
 * **It opens on BMI-for-age.** Height-for-age is the chart every paediatric textbook opens
 * with, and it is the wrong default here: obesity is the largest single presenting problem in
 * this clinic and [R-06]'s flag is read off BMI. A screen that opened on height would make a
 * physician click before seeing the number that matters, every single time.
 *
 * **The reference asked for matches the child.** A boy plotted against the female curves is
 * wrong by several percentile points, and nothing on screen would say so.
 *
 * **Nothing is ever silently empty.** Three ways a chart can have nothing to show — the
 * request failed, the data has not arrived, no reference applies at this age — must read as
 * three different sentences. An empty chart is a screen a physician has to interrogate; a
 * sentence is an answer.
 *
 * The network is a scripted fetch rather than a mocked api module, so what is exercised is
 * the request the screen actually makes, query string included.
 */

const CURVE_LINE = (offset: number): number[][] =>
  Array.from({ length: 40 }, (_, i) => [i * 6, 14 + offset + i * 0.06]);

function curveSet(indicator: string, sex: string) {
  return {
    indicator,
    sex,
    unit: indicator === 'HFA' ? 'cm' : 'kg/m2',
    standards: [
      {
        code: 'WHO_2006',
        version: '2006.1',
        min_age_months: 0,
        max_age_months: 60,
        name_en: 'WHO',
        name_bn: 'ডব্লিউএইচও',
      },
      {
        code: 'CDC_2000',
        version: '2000.1',
        min_age_months: 60,
        max_age_months: 240.5,
        name_en: 'CDC',
        name_bn: 'সিডিসি',
      },
    ],
    curves: [3, 15, 50, 85, 95, 97].map((percentile) => ({
      percentile,
      points: CURVE_LINE(percentile / 30),
    })),
  };
}

function percentile(indicator: string, value: number, months: number, p: number, z: number) {
  return {
    indicator,
    code: indicator === 'HFA' ? 'BODY_HEIGHT' : 'BMI',
    value,
    unit: indicator === 'HFA' ? 'cm' : 'kg/m2',
    age_days: Math.round(months * 30.44),
    age_months: months,
    z,
    percentile: p,
    standard: 'CDC_2000',
    standard_version: '2000.1',
    effective_at: `2026-0${Math.min(9, Math.max(1, Math.round(months / 24)))}-14T09:00:00Z`,
  };
}

/** A boy of seven, obese, with a history under both indicators. */
const GROWTH = {
  growth: {
    patient_id: 'p-1',
    sex: 'male',
    age_days: 2600,
    applicable: true,
    current: {
      HFA: percentile('HFA', 128, 85.4, 62.4, 0.32),
      WFA: percentile('WFA', 31.5, 85.4, 88.1, 1.18),
      BFA: percentile('BFA', 19.2, 85.4, 96.4, 1.8),
    },
    history: {
      // Deliberately different lengths, so a chart plotting the wrong indicator's points is
      // visible as a different number of marks rather than as a plausible-looking line.
      BFA: [24, 48, 72, 96].map((months, i) => percentile('BFA', 15.5 + i * 1.1, months, 60, 0.3)),
      HFA: [48, 96].map((months, i) => percentile('HFA', 105 + i * 20, months, 55, 0.1)),
    },
  },
  weight_status: {
    class: 'obese',
    percent_of_95th: 103,
    bmi_at_95th: 18.6,
    standard: 'CDC_2000',
  },
};

function respond(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'req_growth' },
  });
}

const REFUSED = {
  error: { code: 'INTERNAL', kind: 'internal', message: 'No.', message_bn: 'না।' },
};

interface Clinic {
  /** Every request the screen made, in order. */
  requests: Request[];
  /** Just the reference-curve requests, as `${indicator}/${sex}`. */
  curveCalls: () => string[];
}

function clinic(
  options: { growth?: unknown; growthStatus?: number; curvesStatus?: number } = {},
): Clinic {
  const requests: Request[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const request = new Request(input, init);
      requests.push(request);
      const url = new URL(request.url);
      if (url.pathname.endsWith('/growth')) {
        return options.growthStatus !== undefined
          ? respond(REFUSED, options.growthStatus)
          : respond(options.growth ?? GROWTH);
      }
      if (url.pathname === '/v1/observations/growth-curves') {
        if (options.curvesStatus !== undefined) return respond(REFUSED, options.curvesStatus);
        const indicator = url.searchParams.get('indicator') ?? '';
        const sex = url.searchParams.get('sex') ?? '';
        return respond({ curves: curveSet(indicator, sex) });
      }
      throw new Error(`no route for ${url.pathname}`);
    }),
  );
  return {
    requests,
    curveCalls: () =>
      requests
        .filter((request) => new URL(request.url).pathname === '/v1/observations/growth-curves')
        .map((request) => {
          const query = new URL(request.url).searchParams;
          return `${query.get('indicator')}/${query.get('sex')}`;
        }),
  };
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('before the answer arrives', () => {
  it('says it is loading rather than showing an empty card', () => {
    // Nothing on screen and nothing said is a screen a physician taps twice.
    clinic();
    renderWithProviders(<GrowthScreen patientId="p-1" />);
    expect(screen.getByText('Loading…')).toBeInTheDocument();
  });

  it('says growth could not be loaded when the request fails', async () => {
    // Distinct from "no reference applies": one is the clinic's server having a bad day and
    // the other is a fact about the patient. Showing the second for the first would have a
    // physician conclude something about the child.
    clinic({ growthStatus: 500 });
    renderWithProviders(<GrowthScreen patientId="p-1" />);

    expect(await screen.findByText('Growth could not be loaded.')).toBeInTheDocument();
    expect(screen.queryByTestId('percentile-card')).toBeNull();
    expect(screen.queryByTestId('growth-tab-BFA')).toBeNull();
  });

  it('asks the reference for nothing until it knows whose chart it is', async () => {
    // The sex is in the patient's response. Guessing it would be a request for the wrong
    // table, answered and cached, and then drawn.
    const seen = clinic({ growthStatus: 500 });
    renderWithProviders(<GrowthScreen patientId="p-1" />);

    await screen.findByText('Growth could not be loaded.');
    expect(seen.curveCalls()).toEqual([]);
  });
});

describe('what the screen opens on', () => {
  it('opens on BMI-for-age, because that is where the obesity flag is read', async () => {
    // Not height, which is what a paediatric textbook opens with. [R-06] flags obesity off
    // BMI, and obesity is the largest single presenting problem in this clinic.
    clinic();
    renderWithProviders(<GrowthScreen patientId="p-1" />);

    const bfa = await screen.findByTestId('growth-tab-BFA');
    expect(bfa).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByTestId('growth-tab-HFA')).toHaveAttribute('aria-selected', 'false');
    expect(screen.getByTestId('growth-tab-WFA')).toHaveAttribute('aria-selected', 'false');
  });

  it('puts the answer above the evidence', async () => {
    // The card first, the chart beneath. A screen that opened with a chart would make a
    // physician read a line before reading a number, with a child in front of them.
    clinic();
    const { container } = renderWithProviders(<GrowthScreen patientId="p-1" />);
    await screen.findByTestId('growth-chart');

    const card = screen.getByTestId('percentile-card');
    const chart = screen.getByTestId('growth-chart');
    expect(container.contains(card)).toBe(true);
    expect(card.compareDocumentPosition(chart) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it('shows the weight status the server computed, in words', async () => {
    clinic();
    renderWithProviders(<GrowthScreen patientId="p-1" />);
    expect(await screen.findByTestId('weight-status')).toHaveTextContent('Obese');
  });

  it('asks for the BMI curves for this child’s sex', async () => {
    // A boy plotted against the female curves is wrong by several percentile points, and
    // there is nothing on the chart that would say so.
    const seen = clinic();
    renderWithProviders(<GrowthScreen patientId="p-1" />);
    await screen.findByTestId('growth-chart');
    expect(seen.curveCalls()).toEqual(['BFA/male']);
  });

  it('asks for the female curves for a girl', async () => {
    const seen = clinic({
      growth: { growth: { ...GROWTH.growth, sex: 'female' }, weight_status: GROWTH.weight_status },
    });
    renderWithProviders(<GrowthScreen patientId="p-1" />);
    await screen.findByTestId('growth-chart');
    expect(seen.curveCalls()).toEqual(['BFA/female']);
  });
});

describe('moving between the three indicators', () => {
  it('draws the chart with the standards named beneath it', async () => {
    // D-21: a percentile computed under WHO and one under CDC are not the same measurement,
    // so the chart says which references it is drawn from.
    clinic();
    renderWithProviders(<GrowthScreen patientId="p-1" />);

    expect(await screen.findByTestId('growth-chart')).toBeInTheDocument();
    expect(screen.getByRole('img', { name: /BMI-for-age/i })).toBeInTheDocument();
    expect(screen.getByText(/WHO Child Growth Standards/)).toHaveTextContent(
      /CDC 2000 Growth Charts/,
    );
  });

  it('fetches the height reference when the clinician asks for height', async () => {
    const user = userEvent.setup();
    const seen = clinic();
    renderWithProviders(<GrowthScreen patientId="p-1" />);
    await screen.findByTestId('growth-chart');

    await user.click(screen.getByTestId('growth-tab-HFA'));

    await waitFor(() =>
      expect(screen.getByRole('img', { name: /Height-for-age/i })).toBeInTheDocument(),
    );
    expect(seen.curveCalls()).toEqual(['BFA/male', 'HFA/male']);
    expect(screen.getByTestId('growth-tab-HFA')).toHaveAttribute('aria-selected', 'true');
  });

  it('plots this child’s own points for the indicator on screen', async () => {
    // The history is per indicator. Plotting BMI's points on the height chart would be a
    // trajectory that looks entirely plausible and is somebody else's arithmetic.
    const user = userEvent.setup();
    clinic();
    const { container } = renderWithProviders(<GrowthScreen patientId="p-1" />);
    await screen.findByTestId('growth-chart');

    expect(container.querySelectorAll('circle.app-growth__point')).toHaveLength(4);

    await user.click(screen.getByTestId('growth-tab-HFA'));
    await waitFor(() =>
      expect(container.querySelectorAll('circle.app-growth__point')).toHaveLength(2),
    );
  });

  it('does not fetch the reference again for a tab already seen', async () => {
    // The curves are a published table, identical for every child in the world. Refetching
    // them on every tab click is eight hundred points of arithmetic per glance.
    const user = userEvent.setup();
    const seen = clinic();
    renderWithProviders(<GrowthScreen patientId="p-1" />);
    await screen.findByTestId('growth-chart');

    await user.click(screen.getByTestId('growth-tab-HFA'));
    await waitFor(() => expect(seen.curveCalls()).toEqual(['BFA/male', 'HFA/male']));
    await user.click(screen.getByTestId('growth-tab-BFA'));

    await waitFor(() =>
      expect(screen.getByRole('img', { name: /BMI-for-age/i })).toBeInTheDocument(),
    );
    expect(seen.curveCalls()).toEqual(['BFA/male', 'HFA/male']);
  });

  it('says it is loading while a newly chosen reference is on its way', async () => {
    const user = userEvent.setup();
    clinic();
    renderWithProviders(<GrowthScreen patientId="p-1" />);
    await screen.findByTestId('growth-chart');

    await user.click(screen.getByTestId('growth-tab-WFA'));

    // The card is the answer and stays; only the evidence is missing for a moment.
    expect(screen.getByTestId('percentile-card')).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole('img', { name: /Weight-for-age/i })).toBeInTheDocument(),
    );
  });

  it('keeps the card standing when only the reference lines fail', async () => {
    // The percentiles are the answer; the curves are the picture. Losing the picture must
    // not take the number away with it.
    clinic({ curvesStatus: 500 });
    renderWithProviders(<GrowthScreen patientId="p-1" />);

    expect(await screen.findByTestId('percentile-card')).toBeInTheDocument();
    expect(screen.getByTestId('weight-status')).toHaveTextContent('Obese');
    expect(screen.queryByTestId('growth-chart')).toBeNull();
    // And not the failure sentence: growth loaded perfectly well.
    expect(screen.queryByText('Growth could not be loaded.')).toBeNull();
  });
});

describe('when there is nothing to plot', () => {
  const TOO_OLD = {
    growth: {
      patient_id: 'p-9',
      sex: 'female',
      age_days: 8200,
      applicable: false,
      note: 'too_old_for_a_growth_reference',
    },
  };

  it('says why in a sentence rather than drawing an empty chart', async () => {
    clinic({ growth: TOO_OLD });
    renderWithProviders(<GrowthScreen patientId="p-9" />);

    const card = await screen.findByTestId('percentile-card');
    expect(card).toHaveAttribute('data-empty', 'true');
    expect(card).toHaveTextContent('20 years');
  });

  it('offers no tabs and no chart for a patient no reference covers', async () => {
    /*
     * Tabs over an empty chart are three ways of being told nothing.
     *
     * Not asserted, and worth knowing: the screen still *fetches* the reference table for
     * this patient. The effect that loads the curves is gated on the child's sex and not on
     * `applicable`, so every adult who opens this tab costs one pointless eight-hundred-point
     * request. Nothing is drawn from it. That is a defect in GrowthScreen.tsx rather than
     * something to freeze into an expectation here.
     */
    const seen = clinic({ growth: TOO_OLD });
    renderWithProviders(<GrowthScreen patientId="p-9" />);

    await screen.findByTestId('percentile-card');
    await waitFor(() => expect(seen.requests.length).toBeGreaterThan(0));
    expect(screen.queryByRole('tablist')).toBeNull();
    expect(screen.queryByTestId('growth-chart')).toBeNull();
    expect(screen.queryByRole('img', { name: /growth chart/i })).toBeNull();
  });

  it('asks for no curves when the child’s sex is not one the reference has', async () => {
    // Every published growth reference is sexed. A patient recorded as `other` — or with the
    // field never filled in — has no table, and asking for one would be a 422 per render.
    const seen = clinic({
      growth: {
        growth: { ...GROWTH.growth, sex: 'other', history: {} },
        weight_status: GROWTH.weight_status,
      },
    });
    renderWithProviders(<GrowthScreen patientId="p-1" />);

    await screen.findByTestId('percentile-card');
    expect(screen.getByTestId('growth-tab-BFA')).toBeInTheDocument();
    expect(screen.getByText('Loading…')).toBeInTheDocument();
    expect(seen.curveCalls()).toEqual([]);
  });
});

describe('in Bangla', () => {
  it('names the indicators and the standards in Bangla', async () => {
    // The clinic works in Bangla, and this is the screen a physician reads with a parent
    // beside them.
    clinic();
    renderWithProviders(<GrowthScreen patientId="p-1" />, { locale: 'bn' });

    expect(await screen.findByTestId('growth-tab-BFA')).toHaveTextContent('বয়স অনুযায়ী বিএমআই');
    expect(screen.getByTestId('weight-status')).toHaveTextContent('স্থূল');
    expect(await screen.findByTestId('growth-chart')).toBeInTheDocument();
  });

  it('says loading and unavailable in Bangla too', async () => {
    clinic({ growthStatus: 500 });
    renderWithProviders(<GrowthScreen patientId="p-1" />, { locale: 'bn' });
    expect(await screen.findByText('বৃদ্ধির তথ্য আনা যায়নি।')).toBeInTheDocument();
  });
});
