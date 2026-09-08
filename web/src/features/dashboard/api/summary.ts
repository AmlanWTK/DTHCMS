import { writing } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';

/**
 * Asking for a pre-consultation summary (CP71's route, CP73's button).
 *
 * Its own file and not part of the dashboard's aggregate, because it is the one call on this
 * screen that reaches an endpoint the dashboard does not own: `POST /v1/visits/{id}/synthesis`
 * belongs to the synthesis module and CP71 built it. Wrapping it here rather than importing a
 * synthesis feature that does not exist is the smaller of two wrongs; the day there is a
 * `features/synthesis`, this moves into it and the dashboard imports it.
 *
 * §7.1 is explicit that the physician *can* trigger the summary and by design never needs to:
 * the analysis takes time they do not have, so it runs while the patient is still in
 * counselling. The button exists for the morning the automatic trigger did not fire — which
 * is exactly the morning somebody needs it.
 *
 * The answer is 202 and carries a queued run, not a summary. Nothing here waits for it: the
 * dashboard invalidates and the next read shows `PENDING`, which is a state the centre panel
 * draws honestly rather than a spinner it has to keep alive.
 */
export async function requestSummary(visitId: string): Promise<void> {
  await unwrap(
    api.POST('/v1/visits/{id}/synthesis', {
      params: { ...writing(), path: { id: visitId } },
    }),
  );
}
