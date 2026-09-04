import { afterEach, describe, expect, it, vi } from 'vitest';

import { ApiError, API_BASE_URL, NetworkError } from '@/lib/api';
import { readCurves, readGrowth, type Indicator } from '@/features/growth/api/growth';

/**
 * The two requests behind a growth chart (CP47, drawn by CP48).
 *
 * The split is the point. `readGrowth` is one child's data; `readCurves` is a published
 * table that is the same for every child in the world. Three things go wrong if the calls
 * stop behaving as two:
 *
 *  - **the reference line asked for the wrong child.** A boy plotted against the female
 *    curves is a percentile that is wrong by several points, on a screen where the number
 *    decides whether [R-06]'s obesity flag is raised. So the indicator and the sex actually
 *    reaching the query string are asserted.
 *  - **a patient id leaking into the reference request**, which would make a cacheable
 *    public table into a per-patient fetch, and eight hundred points of arithmetic would
 *    travel again on every chart open.
 *  - **the envelope handed on instead of its contents.** `readCurves` unwraps `.curves`;
 *    a caller given `{curves: …}` draws an empty chart with no error anywhere.
 *
 * The failure paths matter too: a refusal and an unreachable server are different sentences
 * on screen — "you may not see this" and "you are offline" — and the screen picks between
 * them by the class of the error thrown here.
 */

function server(routes: Record<string, (request: Request) => Response>): Request[] {
  const seen: Request[] = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const request = new Request(input, init);
    seen.push(request);
    const key = `${request.method} ${new URL(request.url).pathname}`;
    const handler = routes[key];
    if (!handler) throw new Error(`no route for ${key}`);
    return handler(request);
  });
  vi.stubGlobal('fetch', fetchMock);
  return seen;
}

function respond(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'req_growth' },
  });
}

const GROWTH = {
  growth: {
    patient_id: 'p-1',
    sex: 'male',
    age_days: 2600,
    applicable: true,
    current: {
      BFA: {
        indicator: 'BFA',
        code: 'BMI',
        value: 19.2,
        unit: 'kg/m2',
        age_days: 2600,
        age_months: 85.4,
        z: 1.8,
        percentile: 96.4,
        standard: 'CDC_2000',
        standard_version: '2000.1',
      },
    },
    history: {},
  },
  weight_status: {
    class: 'obese',
    percent_of_95th: 103,
    bmi_at_95th: 18.6,
    standard: 'CDC_2000',
  },
};

const CURVES = {
  indicator: 'BFA',
  sex: 'male',
  unit: 'kg/m2',
  standards: [
    {
      code: 'CDC_2000',
      version: '2000.1',
      min_age_months: 60,
      max_age_months: 240.5,
      name_en: 'CDC',
      name_bn: 'সিডিসি',
    },
  ],
  curves: [{ percentile: 95, points: [[60, 18.6]] }],
};

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("reading one child's growth", () => {
  it('asks the patient endpoint and hands back both the percentiles and the flag', async () => {
    // The weight status travels with the percentiles rather than being derived on the
    // client: [R-06]'s threshold is a clinical rule, and a second implementation of it in
    // TypeScript is a second answer waiting to disagree with the first.
    const seen = server({ 'GET /v1/patients/p-1/growth': () => respond(GROWTH) });

    const result = await readGrowth('p-1');

    expect(seen[0]!.url).toBe(`${API_BASE_URL}/v1/patients/p-1/growth`);
    expect(result.growth.sex).toBe('male');
    expect(result.weight_status?.class).toBe('obese');
  });

  it('passes an inapplicable answer through untouched', async () => {
    // A twenty-two-year-old has no growth reference, and the server says so with a note.
    // Turning that into an empty object here would give the card three dashes to render
    // instead of a sentence explaining why there is nothing.
    server({
      'GET /v1/patients/p-9/growth': () =>
        respond({
          growth: {
            patient_id: 'p-9',
            sex: 'female',
            age_days: 8200,
            applicable: false,
            note: 'too_old_for_a_growth_reference',
          },
        }),
    });

    const result = await readGrowth('p-9');

    expect(result.growth.applicable).toBe(false);
    expect(result.growth.note).toBe('too_old_for_a_growth_reference');
    expect(result.weight_status).toBeUndefined();
  });

  it('escapes the patient id rather than pasting it into the path', async () => {
    const seen = server({ 'GET /v1/patients/p%2F1/growth': () => respond(GROWTH) });
    await readGrowth('p/1');
    expect(seen).toHaveLength(1);
  });

  it('raises the shared ApiError when the caller may not read this child', async () => {
    server({
      'GET /v1/patients/p-1/growth': () =>
        respond(
          {
            error: {
              code: 'FORBIDDEN',
              kind: 'permission',
              message: 'Not permitted for this role.',
              message_bn: 'এই ভূমিকার জন্য অনুমোদিত নয়।',
            },
          },
          403,
        ),
    });

    const error = await readGrowth('p-1').catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).messageBN).toBe('এই ভূমিকার জন্য অনুমোদিত নয়।');
  });

  it('raises a NetworkError when the request never arrives', async () => {
    // A different sentence on the screen: "you are offline" rather than "the clinic server
    // refused this". The chart's failure state is chosen by which of these is thrown.
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')));
    await expect(readGrowth('p-1')).rejects.toBeInstanceOf(NetworkError);
  });
});

describe('reading the published reference lines', () => {
  it.each([
    { indicator: 'BFA' as Indicator, sex: 'male' as const },
    { indicator: 'HFA' as Indicator, sex: 'female' as const },
    { indicator: 'WFA' as Indicator, sex: 'male' as const },
  ])('asks for the $indicator lines for a $sex child', async ({ indicator, sex }) => {
    // The two parameters that decide which table is drawn. A boy plotted against the female
    // curves is wrong by several percentile points, silently, on the chart a physician reads.
    const seen = server({
      'GET /v1/observations/growth-curves': () =>
        respond({ curves: { ...CURVES, indicator, sex } }),
    });

    await readCurves(indicator, sex);

    const query = new URL(seen[0]!.url).searchParams;
    expect(query.get('indicator')).toBe(indicator);
    expect(query.get('sex')).toBe(sex);
  });

  it('hands back the curve set, not the envelope it arrived in', async () => {
    server({ 'GET /v1/observations/growth-curves': () => respond({ curves: CURVES }) });

    const set = await readCurves('BFA', 'male');

    expect(set.indicator).toBe('BFA');
    expect(set.standards[0]?.code).toBe('CDC_2000');
    expect(set.curves[0]?.percentile).toBe(95);
  });

  it('names no patient, because the reference is nobody in particular', async () => {
    // The whole reason this is its own endpoint. A patient id here would make a table that
    // is identical for every child in the world into a per-patient fetch and a cache miss
    // on every chart that opens.
    const seen = server({
      'GET /v1/observations/growth-curves': () => respond({ curves: CURVES }),
    });

    await readCurves('BFA', 'male');

    const url = new URL(seen[0]!.url);
    expect(url.pathname).toBe('/v1/observations/growth-curves');
    expect(url.search).not.toContain('p-1');
    expect(url.searchParams.has('id')).toBe(false);
  });

  it('raises the shared ApiError when the reference cannot be served', async () => {
    // The curves failing is not the child's data failing; the card stays and the chart is
    // what goes missing. That only works if this throws rather than resolving to nothing.
    server({
      'GET /v1/observations/growth-curves': () =>
        respond(
          {
            error: {
              code: 'INTERNAL',
              kind: 'internal',
              message: 'Something went wrong.',
              message_bn: 'কিছু একটা সমস্যা হয়েছে।',
            },
          },
          500,
        ),
    });

    await expect(readCurves('BFA', 'male')).rejects.toBeInstanceOf(ApiError);
  });
});
