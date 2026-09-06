import { bilingual } from '@/lib/bilingual';
import type { Locale } from '@/lib/i18n/config';

import type { Directory } from '../api/directory';

/**
 * The directory, turned into the three questions a screen actually asks of it (CP61).
 *
 * Pure, and separated from the hook on purpose: what a screen renders when a name cannot be
 * resolved is the interesting half of this checkpoint, and a rule that can only be exercised
 * by mocking a network call is a rule nobody exercises. Everything below is testable with a
 * literal.
 *
 * # Three states, and why "unavailable" is not "empty"
 *
 * A directory that has not arrived yet, a directory that failed to arrive, and a directory
 * that arrived are three different situations, and only the third one licenses a screen to
 * say anything about who somebody is. Collapsing the first two into "no name" would draw a
 * still-loading screen exactly like a broken one; collapsing either into "unknown person"
 * would put a sentence about a colleague on screen on the strength of a failed request.
 *
 * The state is therefore carried, and **a failure never removes a value from a screen**. The
 * component falls back to what the clinical record itself carries — the role, the station,
 * the time — which is enough for a reviewer to walk to the right room, and it says that the
 * names could not be read rather than implying nobody was there.
 *
 * # Why an unresolved id is `null` rather than the id
 *
 * A uuid is not an answer to "who entered this". Resolution failing is a fact about this
 * lookup, and the component turns it into a sentence; handing the uuid back here would let a
 * caller print it as if it were a name.
 */

export type DirectoryState = 'loading' | 'ready' | 'unavailable';

export interface ResolvedPerson {
  id: string;
  /** The name in the reader's language where there is one, otherwise the other. */
  name: string | null;
  /** The staff code — the identifier that does not move when somebody's name does. */
  code: string | null;
  /**
   * `active`, `suspended`, `deactivated`, or whatever the server adds next.
   *
   * Kept as the server's own string rather than narrowed to a union: a status this build has
   * never heard of must render as itself, not disappear into an `else` branch that says the
   * person is working here when nobody knows whether they are.
   */
  status: string;
}

export interface ResolvedDevice {
  id: string;
  name: string;
  kind: string;
  status: string;
}

export interface DirectoryLookup {
  state: DirectoryState;
  /** When the directory was read, so a screen can say how old its answer is. */
  asOf: string | null;
  person(id: string | null | undefined): ResolvedPerson | null;
  device(id: string | null | undefined): ResolvedDevice | null;
  /** The station's name in the reader's language, or `null` when the code is not listed. */
  station(code: string | null | undefined): string | null;
}

/**
 * A lookup over one directory response.
 *
 * The indexes are built once per response rather than per question: a patient's timeline
 * asks the same twelve ids hundreds of times, and a linear scan per value is the kind of
 * thing that is invisible on a developer's machine and noticeable on the clinic's tablets.
 */
export function buildLookup(
  directory: Directory | null,
  locale: Locale,
  state: DirectoryState,
): DirectoryLookup {
  const staff = new Map<string, ResolvedPerson>();
  const devices = new Map<string, ResolvedDevice>();
  const stations = new Map<string, string>();

  if (directory !== null) {
    for (const person of directory.staff) {
      staff.set(person.id, {
        id: person.id,
        name: bilingual(person.name_en, person.name_bn, locale)?.text ?? null,
        // An empty code is the same fact as no code, and the difference between `''` and
        // `null` is one every caller would otherwise have to remember.
        code: person.code.trim() === '' ? null : person.code,
        status: person.status,
      });
    }
    for (const device of directory.devices) {
      devices.set(device.id, {
        id: device.id,
        name: device.name,
        kind: device.kind,
        status: device.status,
      });
    }
    for (const station of directory.stations) {
      const name = bilingual(station.name_en, station.name_bn, locale)?.text;
      // A station with no name in either language keeps its code, which is what the queue,
      // the board and the assignment rules all call it anyway.
      stations.set(station.code, name ?? station.code);
    }
  }

  return {
    state,
    asOf: directory?.as_of ?? null,
    person: (id) => (id ? (staff.get(id) ?? null) : null),
    device: (id) => (id ? (devices.get(id) ?? null) : null),
    station: (code) => (code ? (stations.get(code) ?? null) : null),
  };
}

/** Whether this person is somebody a reviewer could still walk down the corridor and ask. */
export function isPresentStaff(person: ResolvedPerson): boolean {
  return person.status === 'active';
}
