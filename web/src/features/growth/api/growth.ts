import type { components } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';

/**
 * Paediatric growth, typed against the contract (CP47, drawn by CP48).
 *
 * Two calls, not one, and the split is deliberate. `readGrowth` is patient data. `readCurves`
 * is the published reference — identical for every child in the world, fetched once and
 * cached for the session. Bundling them would re-send eight hundred points of somebody
 * else's arithmetic every time a chart opened.
 */

export type Growth = components['schemas']['Growth'];
export type GrowthPercentile = components['schemas']['GrowthPercentile'];
export type GrowthPoint = components['schemas']['GrowthPoint'];
export type WeightStatus = components['schemas']['WeightStatus'];
export type GrowthCurveSet = components['schemas']['GrowthCurveSet'];
export type Indicator = 'HFA' | 'WFA' | 'BFA';

export interface GrowthResponse {
  growth: Growth;
  weight_status?: WeightStatus;
}

/** This child's percentiles, trajectory and weight status. */
export async function readGrowth(patientID: string): Promise<GrowthResponse> {
  return unwrap(
    api.GET('/v1/patients/{id}/growth', { params: { path: { id: patientID } } }),
  ) as Promise<GrowthResponse>;
}

/** The reference lines behind the chart. */
export async function readCurves(
  indicator: Indicator,
  sex: 'male' | 'female',
): Promise<GrowthCurveSet> {
  const body = await unwrap(
    api.GET('/v1/observations/growth-curves', {
      params: { query: { indicator, sex } },
    }),
  );
  return (body as { curves: GrowthCurveSet }).curves;
}
