import type { components } from '@dthcms/api-client';

/**
 * What a clinical value knows about how it came to exist (CP61, §4.2).
 *
 * # Why there is one shape rather than one per record
 *
 * §4.2's requirement is that a reviewer sees who entered a value **instantly, without
 * digging** — and it is a requirement about every value, on every screen. Five record types
 * spell attribution five slightly different ways (`recorded_by`, `asserted_by`, `raised_by`,
 * `by`), and a component that took each of them raw would be five components in a trench
 * coat, four of which somebody forgets to update. So the field names are translated once,
 * here, by the adapters at the bottom of this file, and the component knows one shape.
 *
 * # Every field is optional, and that is not laziness
 *
 * The records genuinely differ. An allergy carries no `source`; a growth percentile carries
 * no author at all; an observation carries a device server-side that the contract does not
 * expose. A shape that required them would force each adapter to invent a value, and an
 * invented "Station" against a value nobody knows the provenance of is worse than a blank —
 * it is a claim. Absent means absent, and the component says so in words.
 */

/** What kind of evidence a value is. The contract's enum, mirrored so a screen can switch. */
export const VALUE_SOURCES = ['STATION', 'OCR', 'FIELD', 'DEVICE', 'PATIENT'] as const;

export type ValueSource = (typeof VALUE_SOURCES)[number];

/**
 * Whether this build has a sentence for this source.
 *
 * A server that adds a sixth source tomorrow must render as its own code rather than
 * disappear: an unknown source drawn as nothing would make a value of unknown provenance
 * look like an ordinary station reading, which is the exact confusion criterion 3 exists to
 * prevent.
 */
export function isKnownSource(source: string): source is ValueSource {
  return (VALUE_SOURCES as readonly string[]).includes(source);
}

/**
 * Whether a value was typed by a person at a station, or arrived some other way.
 *
 * Criterion 3 names OCR because that is the one that bites — a number lifted off a
 * photograph of a paper chart by a scanner is evidence of a different kind from a number an
 * operator read off a calibrated scale — but the same is true of a patient-reported figure
 * and of a reading a device sent by itself. So the question the component asks is the
 * general one, and OCR gets its own sentence within it.
 */
export function isMachineRead(source: string | undefined): boolean {
  return source === 'OCR' || source === 'DEVICE';
}

/**
 * What kind of change happened to a value that is no longer the current one.
 *
 * `CORRECTED` and `SUPERSEDED` are the observation ledger's own words. `AMENDED` is a
 * history item whose detail was changed in place. `WITHDRAWN` is an allergy or an assertion
 * somebody took back — never a deletion, and the row stays. CP62 builds the full chain;
 * these are the four states a record can actually evidence today.
 */
export type CorrectionKind = 'CORRECTED' | 'SUPERSEDED' | 'AMENDED' | 'WITHDRAWN';

export interface ValueCorrection {
  kind: CorrectionKind;
  /** Who made the change, where the record names them. Observations do not. */
  by?: string;
  /** The role they were wearing, where the record carries it. */
  role?: string;
  at?: string;
  /** The value that replaced this one, where the record points at it. */
  replacedBy?: string;
}

export interface ValueAttribution {
  /** The uuid of whoever entered it. Resolved to a name through the directory. */
  recordedBy?: string;
  /** The role they were wearing at the time — a property of the value, not of the person. */
  recordedRole?: string;
  stationCode?: string;
  /**
   * The device it was entered on, resolved to a name through the directory.
   *
   * Absent is an honest answer and not a gap: a value typed on the web was not typed on a
   * tablet, and there is no device to name. Nothing is drawn for it — a line reading "device
   * not recorded" beside every value a physician enters at a desk would be noise standing in
   * for a fact that is already true.
   */
  deviceId?: string;
  /** When it was written down. */
  recordedAt?: string;
  /**
   * When the value was true, where the record distinguishes the two.
   *
   * A reading taken at 09:05 and entered at 09:20 has both, and a reviewer asking "was this
   * before or after the insulin" is asking about this one.
   */
  effectiveAt?: string;
  /**
   * What kind of evidence this is, in three states rather than two.
   *
   * `undefined` — the record has no source field at all. An allergy change line and a
   * critical alert are like this, and nothing is drawn: inventing a provenance for a record
   * whose schema has no room for one would be a claim nobody made.
   *
   * `null` — the record **has** the field and it is empty. Every row written before CP61's
   * migration is like this, and it is the state that matters: a blank drawn as nothing looks
   * exactly like a station entry, which is the confusion criterion 3 exists to prevent. So it
   * is drawn as "Source not recorded", in words.
   *
   * A string — the source, as the server spells it.
   *
   * The middle state is easy to mistake for tidying-up work. It is not: collapsing `null`
   * into `undefined` would silently draw an unknown provenance as an ordinary one on every
   * record written before the migration, which is most of the archive a reviewer looks at.
   */
  source?: string | null;
  /** Evidence that this value is not the original. Criterion 2. */
  correction?: ValueCorrection;
}

/**
 * A field the server may legitimately send as an empty string.
 *
 * The columns behind `station_code`, `source` and `device_id` were added to four tables by
 * CP61's migration and backfilled from the event envelope, so a row written before it keeps
 * an empty one. `''` and "absent" mean the same thing at every call site below, and a helper
 * is how they stay meaning the same thing when a fifth table is added.
 */
function stated(value: string | undefined | null): string | null {
  const trimmed = (value ?? '').trim();
  return trimmed === '' ? null : trimmed;
}

type Observation = components['schemas']['Observation'];
type HistoryItem = components['schemas']['HistoryItem'];
type Allergy = components['schemas']['Allergy'];
type AllergyAssertion = components['schemas']['AllergyAssertion'];
type AllergyChange = components['schemas']['AllergyChange'];
type CriticalAlert = components['schemas']['CriticalAlert'];

/**
 * An observation's attribution.
 *
 * The one record with a `source`, so it is the one where criterion 3 has anything to
 * distinguish. `status` is the correction evidence: `ACTIVE` is the current value,
 * `CORRECTED` and `SUPERSEDED` are values that have been replaced.
 *
 * **The corrector is not named, because the contract does not carry a name.** There is no
 * `corrected_by` and no `corrected_at` on this schema — only the status and, sometimes, the
 * id of the value that replaced this one. The component says so out loud rather than leaving
 * an empty line where a person's name should be; CP62 owns the chain that would fill it.
 */
export function observationAttribution(observation: Observation): ValueAttribution {
  const replaced = observation.status !== 'ACTIVE';
  const station = stated(observation.station_code);
  const device = stated(observation.device_id);
  return {
    recordedBy: observation.recorded_by,
    recordedRole: observation.recorded_role,
    ...(station === null ? {} : { stationCode: station }),
    ...(device === null ? {} : { deviceId: device }),
    recordedAt: observation.recorded_at,
    effectiveAt: observation.effective_at,
    source: stated(observation.source),
    ...(replaced
      ? {
          correction: {
            kind: observation.status as CorrectionKind,
            ...(observation.replaced_by === undefined
              ? {}
              : { replacedBy: observation.replaced_by }),
          },
        }
      : {}),
  };
}

/**
 * A history item's attribution.
 *
 * `amended_at` and `amended_by` are the correction, and they are the only place in the
 * contract today where **both** the original author and the person who changed the record
 * are named on the same row — which is criterion 2 in full.
 *
 * `status: RESOLVED` is deliberately not treated as one. "She had this and no longer does" is
 * a clinical fact about the patient, not a change to who said what; drawing it as a
 * correction would put a colleague's name against a revision nobody made.
 */
export function historyItemAttribution(item: HistoryItem): ValueAttribution {
  const station = stated(item.station_code);
  const device = stated(item.device_id);
  return {
    recordedBy: item.recorded_by,
    ...(item.recorded_role === undefined ? {} : { recordedRole: item.recorded_role }),
    ...(station === null ? {} : { stationCode: station }),
    ...(device === null ? {} : { deviceId: device }),
    recordedAt: item.recorded_at,
    // Always carried, `null` included: this schema has the field now, so a blank one is a
    // row from before the migration rather than a record with no provenance to speak of, and
    // the two are drawn differently.
    source: stated(item.source),
    ...(item.amended_at === undefined
      ? {}
      : {
          correction: {
            kind: 'AMENDED',
            at: item.amended_at,
            ...(item.amended_by === undefined ? {} : { by: item.amended_by }),
          },
        }),
  };
}

/**
 * An allergy's attribution.
 *
 * `station_code`, `source` and `device_id` arrived on this schema at CP61, which is what puts
 * criterion 3's distinction on the one record a prescriber reads before writing: an allergy
 * a scanner lifted off a photograph of a paper card the patient brought in is different
 * evidence from one an officer typed at station 4, and until now the screen had no way to
 * say so.
 *
 * Still no correction fields, and none is invented. An allergy somebody disagreed with is
 * withdrawn rather than corrected, and the withdrawal is a row in the change history.
 */
export function allergyAttribution(allergy: Allergy): ValueAttribution {
  const station = stated(allergy.station_code);
  const device = stated(allergy.device_id);
  return {
    recordedBy: allergy.recorded_by,
    ...(allergy.recorded_role === undefined ? {} : { recordedRole: allergy.recorded_role }),
    ...(station === null ? {} : { stationCode: station }),
    ...(device === null ? {} : { deviceId: device }),
    recordedAt: allergy.recorded_at,
    source: stated(allergy.source),
  };
}

/**
 * One line of the allergy change history, with both of its people.
 *
 * This is the one record in the contract that names an author and the person who disagreed
 * with them on the same row, which makes it criterion 2's clearest case: somebody recorded
 * an allergy, somebody else took it back, and a screen that showed only one of them has
 * destroyed the half a prescriber most needs.
 *
 * A withdrawal is not a deletion — nothing in this feature deletes — so it is carried as a
 * correction rather than as a reason to hide the row.
 */
export function allergyChangeAttribution(change: AllergyChange): ValueAttribution {
  return {
    ...(change.by === undefined ? {} : { recordedBy: change.by }),
    ...(change.by_role === undefined ? {} : { recordedRole: change.by_role }),
    recordedAt: change.at,
    ...(change.undone_at === undefined
      ? {}
      : {
          correction: {
            kind: 'WITHDRAWN',
            at: change.undone_at,
            ...(change.undone_by === undefined ? {} : { by: change.undone_by }),
          },
        }),
  };
}

/** An assertion's attribution. "No known allergies" is a claim, and it has an author. */
export function assertionAttribution(assertion: AllergyAssertion): ValueAttribution {
  const station = stated(assertion.station_code);
  const device = stated(assertion.device_id);
  return {
    recordedBy: assertion.asserted_by,
    ...(assertion.asserted_role === undefined ? {} : { recordedRole: assertion.asserted_role }),
    ...(station === null ? {} : { stationCode: station }),
    ...(device === null ? {} : { deviceId: device }),
    recordedAt: assertion.asserted_at,
    source: stated(assertion.source),
  };
}

/**
 * A critical alert's attribution.
 *
 * The value on an alert is a **copy** taken at the moment it was raised, so the attribution
 * is the attribution of that copy: whoever recorded the observation that breached, the role
 * they were wearing and the station they were at.
 *
 * `acknowledged_by` is deliberately not a correction. Answering an alert is not changing a
 * value, and a panel that drew the consultant who took it as somebody who altered the reading
 * would put a colleague's name against an act they did not perform.
 *
 * No `source`, and none is asked for. An alert's number is a copy of an observation, and the
 * honest way to say where that number came from is a link to the observation rather than a
 * second copy of its provenance that can disagree with the first. `observation_id` is already
 * on the schema; nothing on this screen can follow it yet.
 */
export function alertAttribution(alert: CriticalAlert): ValueAttribution {
  return {
    recordedBy: alert.raised_by,
    ...(alert.raised_role === undefined ? {} : { recordedRole: alert.raised_role }),
    ...(alert.station_code === undefined ? {} : { stationCode: alert.station_code }),
    recordedAt: alert.raised_at,
  };
}
