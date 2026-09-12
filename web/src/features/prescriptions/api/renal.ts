import type { components } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';

/**
 * The renal status a prescriber reads before deciding a dose (CP79).
 *
 * # Every sentence on the screen comes from the server, in both languages
 *
 * `summary_en` / `summary_bn` and `stage_label_en` / `stage_label_bn` are written by the
 * engine, not assembled here. That is deliberate and it is not laziness: the clinical
 * sentence — *"eGFR 41.4 mL/min/1.73m², G3b, taken on 31 July 2026 (43 days ago). Within
 * the clinic's 6-month window."* — is the same sentence the safety findings cite, and a
 * second copy of it in a message catalogue is a second copy that drifts. The catalogue here
 * holds the chrome (a heading, the word *provisional*) and nothing clinical.
 *
 * # `stale` is a fact about the date, not about the number
 *
 * A stale eGFR is still used: the renal rules run against it, because it is the best
 * information there is. What changes is that the screen says how old it is. A component that
 * hid the value when it was stale would leave the physician with nothing at all, which is
 * worse than an old number he can see the age of.
 *
 * # `policy.approved` is false today and the indicator must say so
 *
 * Six months is the plan's proposal; nobody has approved it. The window applies either way
 * — there is no inert value for a staleness window — so the honest rendering is "we are
 * using six months and nobody has agreed to it", never a silent six months.
 */

export type RenalStatus = components['schemas']['RenalStatus'];
export type RenalPolicy = components['schemas']['RenalPolicy'];

/** The query key, so a screen that already has the answer can prime it. */
export function renalStatusKey(patientId: string) {
  return ['renal-status', patientId] as const;
}

export async function getRenalStatus(patientId: string): Promise<RenalStatus> {
  return unwrap(api.GET('/v1/patients/{id}/renal-status', { params: { path: { id: patientId } } }));
}

/**
 * How loudly to draw it. Three tones and no fourth.
 *
 * `unknown` outranks `stale`, which outranks `current`, and the ordering is the whole point:
 * **"there is no eGFR" is not a milder version of "the eGFR is old".** A prescriber who reads
 * an empty indicator as "probably fine" is the failure this checkpoint exists to prevent, so
 * the absent case gets the loudest tone rather than the quietest.
 */
export type RenalTone = 'unknown' | 'stale' | 'current';

export function renalTone(status: RenalStatus): RenalTone {
  if (!status.known) return 'unknown';
  if (status.stale) return 'stale';
  return 'current';
}

/**
 * Whether this indicator may be drawn as reassurance.
 *
 * True for exactly one case: a known eGFR inside the window. Exported so that no screen
 * derives it a second way — the way a second derivation goes wrong is by treating `!stale`
 * as sufficient, which is true for a patient with no eGFR at all.
 */
export function isCurrentRenalFunction(status: RenalStatus): boolean {
  return status.known === true && status.stale === false;
}
