import { writing } from '@dthcms/api-client';
import type { components } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';

/**
 * The clinic traffic control board, typed against the contract (CP40).
 *
 * Two things about this payload are worth knowing before using it.
 *
 * **`label` is already redacted.** The facility decides whether the wall shows a visit code,
 * a visit code with initials, or a visit code with the clinical id, and the server resolves
 * that before serialising. There is no name in the response to render by accident, and no
 * client-side redaction to get wrong.
 *
 * **There is no patient id.** The only handle a board row offers is a queue entry. That is
 * deliberate: a screen in a public waiting area should not be holding join keys to the rest
 * of the record. If a screen ever needs the patient behind a row, it has to ask for them
 * under a permission the wall display does not hold — which is the point.
 */

export type TrafficBoard = components['schemas']['TrafficBoard'];
export type BoardStation = components['schemas']['BoardStation'];
export type BoardEntry = components['schemas']['BoardEntry'];
export type BoardSuggestion = components['schemas']['BoardSuggestion'];
export type BoardSettings = components['schemas']['BoardSettings'];
export type Heat = BoardStation['heat'];

/** Read the board. `day` is a clinic day in Asia/Dhaka; omitted means today. */
export async function readBoard(day?: string): Promise<TrafficBoard> {
  return unwrap(
    api.GET('/v1/board', {
      params: { query: day ? { day } : {} },
    }),
  );
}

/**
 * Move a waiting patient to another station.
 *
 * A `409` means the patient is no longer waiting — usually because somebody else moved them
 * while both supervisors were looking at the same board. That is not an error to retry; it
 * is a board that needs refreshing, which is what the caller does with it.
 */
export async function reroute(entryId: string, to: string, reason: string): Promise<void> {
  await unwrap(
    api.POST('/v1/board/reroute/{entryId}', {
      params: { ...writing(), path: { entryId } },
      body: { event_id: crypto.randomUUID(), to, reason },
    }),
  );
}
