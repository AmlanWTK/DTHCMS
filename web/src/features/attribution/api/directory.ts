import type { components } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';

/**
 * The names behind the ids every clinical value carries (CP61, §4.2, `GET /v1/directory`).
 *
 * # One request per session, not a join on every read
 *
 * Every clinical read already carries the author's id, the role they were wearing, the
 * station and — where the record has one — the device. What it does not carry is a name, and
 * a uuid answers a different question from the one a physician is asking.
 *
 * The server deliberately did not join the staff record into every clinical query: a
 * patient's timeline is hundreds of values written by a dozen people, and joining two rows
 * onto each of them copies the same twelve names into every payload all day — worse, every
 * future clinical read would have to remember the join or quietly render a blank. So the
 * name is a *lookup*, which is what it actually is, and this module is the client half of
 * that decision. See `backend/internal/auth/directory.go` for the full reasoning.
 *
 * # Why it is cached for an hour and not for thirty seconds
 *
 * The application's query default is half a minute, tuned for a colleague's value appearing
 * on a screen that is already open. Nothing in this response moves at that speed: staff
 * names, staff codes, device names and station names change on the order of months. Re-
 * reading it every time a physician opens a patient would be a round trip on a shared clinic
 * connection to be told the same twelve names again.
 *
 * # Why deactivated staff and retired devices stay in it
 *
 * The server keeps them, and a client that filtered them out would undo the point. Most of
 * what a reviewer asks about is a value from weeks ago, entered by somebody who may since
 * have left; a directory of current staff only renders a blank for exactly the person the
 * question is about. `status` is on each entry so a screen can say "no longer at the clinic"
 * rather than sending somebody to go and ask a colleague who has gone.
 */

export type Directory = components['schemas']['Directory'];
export type DirectoryPerson = components['schemas']['DirectoryPerson'];
export type DirectoryDevice = components['schemas']['DirectoryDevice'];
export type DirectoryStation = components['schemas']['DirectoryStation'];

/**
 * The cache key, held beside the call.
 *
 * One key with nothing in it, because the response is scoped to the caller's facility by the
 * session and there is no second directory to ask for. A key that took an argument would
 * invite two spellings of the same request.
 */
export const DIRECTORY_KEY = ['directory'] as const;

/** An hour. See the note above: nothing in this response moves faster than that. */
export const DIRECTORY_STALE_MS = 60 * 60 * 1000;

/** The whole directory. Needs a session and nothing else; carries no patient data. */
export function readDirectory(): Promise<Directory> {
  return unwrap(api.GET('/v1/directory'));
}
