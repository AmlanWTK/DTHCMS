import type { RealtimeMessage } from './realtime';

/**
 * Messages to query keys (CP27).
 *
 * **Invalidate, never mutate.** This module returns *keys*, and there is no function here
 * that returns data. That is the whole discipline the plan asks for, and the reason for
 * it is worth stating plainly: a realtime message carries a notification, not a record. If
 * a socket message were written into the cache, two paths would produce what the screen
 * shows — the endpoint and the socket — and on the day they disagree, a clinician reads a
 * value that no endpoint ever returned and that no log can explain.
 *
 * Invalidating costs a request. That request goes through the same authorisation, the same
 * serialiser and the same field-level redaction as every other read, which is exactly what
 * makes the result safe to put on a screen.
 *
 * It lives in the shared package because web and mobile must invalidate the same things.
 * Two lists would drift, and the symptom would be "the tablet updates and the dashboard
 * does not", which takes a day to diagnose.
 */

/** A TanStack Query key prefix. Anything under it is invalidated. */
export type QueryKey = readonly unknown[];

/**
 * The query keys this application uses, in one place so a message and a `useQuery` cannot
 * spell the same thing differently.
 */
export const queryKeys = {
  patient: (id: string) => ['patient', id] as const,
  patientTimeline: (id: string) => ['patient', id, 'timeline'] as const,
  patients: () => ['patients'] as const,
  visit: (id: string) => ['visit', id] as const,
  visitVitals: (id: string) => ['visit', id, 'vitals'] as const,
  queue: (facilityId: string) => ['queue', facilityId] as const,
  station: (id: string) => ['station', id] as const,
  devices: () => ['devices'] as const,
  auditAlerts: () => ['audit', 'alerts'] as const,
  users: () => ['users'] as const,
  /**
   * What an operator is being asked to fix (CP62). Its own key rather than a slice of the
   * patient's, because the screen that shows it is the operator's own queue: a station tablet
   * that is not looking at any patient still has to light up when a physician flags a number
   * that operator typed an hour ago.
   */
  corrections: () => ['corrections'] as const,
  /** Every flag ever raised on one patient's values — the chain a physician reads. */
  patientCorrections: (id: string) => ['patient', id, 'corrections'] as const,
  /**
   * The operator quality record (CP63). One prefix for the whole feature — the operator's
   * own record, the supervisor's list and the open flags all sit under it.
   *
   * One key rather than three because a raised flag moves all of them at once and the
   * message carries neither an operator nor a window: it names a flag and the threshold it
   * was raised on, and nothing else (see the note on the gateway bridge). A finer key would
   * have to be guessed from a payload that deliberately does not carry the answer.
   */
  quality: () => ['quality'] as const,
} as const;

/**
 * The keys one message invalidates.
 *
 * A message the client does not recognise invalidates nothing and is not an error: a newer
 * gateway may publish a kind this build has never heard of, and during a rolling deploy
 * that is the normal state of the world. The screen stays correct because it is still
 * fetching through the API; it is merely not refreshed *early*, which is the thing this
 * whole channel is for.
 */
export function realtimeInvalidations(message: RealtimeMessage): QueryKey[] {
  const keys: QueryKey[] = [];
  const [topicKind, topicId] = splitTopic(message.topic);

  // The topic decides the broad target: what the subscriber is watching.
  switch (topicKind) {
    case 'patient':
      if (topicId) keys.push(queryKeys.patient(topicId));
      break;
    case 'visit':
      if (topicId) keys.push(queryKeys.visit(topicId));
      break;
    case 'station':
      if (topicId) keys.push(queryKeys.station(topicId));
      break;
    case 'queue':
      if (topicId) keys.push(queryKeys.queue(topicId));
      break;
    case 'user':
      // Messages addressed to a person: an alert, an assignment. Nothing patient-shaped.
      keys.push(queryKeys.auditAlerts());
      break;
  }

  // A correction is addressed to a person and read on two screens: the operator's own queue,
  // and the chain under the patient's value. Both are invalidated wherever the message was
  // published, because the physician who flagged the value is watching the second one while
  // the operator who typed it is watching the first.
  if (message.kind.startsWith('correction.')) {
    keys.push(queryKeys.corrections());
    if (message.patient_id) keys.push(queryKeys.patientCorrections(message.patient_id));
  }

  // A retraining flag is published on the topic of the person it is *about*, not the
  // supervisor's, and that is deliberate: a flag somebody first learns about from their
  // supervisor is a flag that felt like an ambush. So the operator's own record refreshes on
  // their own screen, and the supervisor's list refreshes under the same prefix wherever they
  // happen to be watching from.
  if (message.kind.startsWith('quality.')) {
    keys.push(queryKeys.quality());
  }

  // The kind narrows it, and adds the reads that are not on the topic. A measurement on a
  // patient's topic also changes that visit's vitals strip, which a different screen is
  // showing.
  if (message.patient_id) {
    keys.push(queryKeys.patient(message.patient_id));
    if (isTimelineKind(message.kind)) {
      keys.push(queryKeys.patientTimeline(message.patient_id));
    }
  }
  if (message.visit_id) {
    keys.push(queryKeys.visit(message.visit_id));
    if (message.kind.startsWith('measurement.') || message.kind.startsWith('vital.')) {
      keys.push(queryKeys.visitVitals(message.visit_id));
    }
  }
  if (message.kind.startsWith('queue.') || message.kind.startsWith('visit.')) {
    // The board changes shape when a visit opens, closes or moves station, wherever the
    // message was published.
    const facility = asString(message.summary?.facility_id);
    if (facility) keys.push(queryKeys.queue(facility));
  }
  if (message.kind.startsWith('device.')) keys.push(queryKeys.devices());
  if (message.kind.startsWith('alert.') || message.kind.startsWith('break_glass.')) {
    keys.push(queryKeys.auditAlerts());
  }
  if (message.kind.startsWith('user.') || message.kind.startsWith('role.')) {
    keys.push(queryKeys.users());
  }

  return dedupe(keys);
}

/**
 * A patient's timeline is the expensive read, so it is refreshed only for the kinds that
 * actually add a row to it — not for every message that names the patient.
 */
function isTimelineKind(kind: string): boolean {
  for (const prefix of [
    'measurement.',
    'vital.',
    'diagnosis.',
    'prescription.',
    'lab.',
    'counseling.',
    'visit.',
    'patient.',
  ]) {
    if (kind.startsWith(prefix)) return true;
  }
  return false;
}

function splitTopic(topic: string): [string, string | undefined] {
  const at = topic.indexOf(':');
  if (at < 0) return [topic, undefined];
  return [topic.slice(0, at), topic.slice(at + 1) || undefined];
}

function asString(value: unknown): string | undefined {
  return typeof value === 'string' && value.length > 0 ? value : undefined;
}

function dedupe(keys: QueryKey[]): QueryKey[] {
  const seen = new Set<string>();
  const out: QueryKey[] = [];
  for (const key of keys) {
    const id = JSON.stringify(key);
    if (seen.has(id)) continue;
    seen.add(id);
    out.push(key);
  }
  return out;
}

/**
 * What to invalidate after a gap — a reconnect, or a connection the gateway reported as
 * too slow.
 *
 * Everything the client is watching, and deliberately not "everything": invalidating the
 * whole cache on every wifi blip would make a clinic's morning a re-fetch storm. The
 * topics a client holds are the screens people are looking at, which is exactly the set
 * that has to be right.
 */
export function gapInvalidations(topics: readonly string[]): QueryKey[] {
  const keys: QueryKey[] = [];
  for (const topic of topics) {
    const [kind, id] = splitTopic(topic);
    if (!id) continue;
    switch (kind) {
      case 'patient':
        keys.push(queryKeys.patient(id));
        break;
      case 'visit':
        keys.push(queryKeys.visit(id));
        break;
      case 'station':
        keys.push(queryKeys.station(id));
        break;
      case 'queue':
        keys.push(queryKeys.queue(id));
        break;
      case 'user':
        keys.push(queryKeys.auditAlerts());
        // A correction request raised while the tablet was offline is exactly the message a
        // gap swallows, and the operator would otherwise learn of it at the end of the day.
        keys.push(queryKeys.corrections());
        // And the retraining flag that may have been raised on the back of it. The one thing
        // this feature cannot afford is an operator hearing about a pattern from somebody else
        // first, and a dropped connection is the ordinary way that happens.
        keys.push(queryKeys.quality());
        break;
    }
  }
  return dedupe(keys);
}
