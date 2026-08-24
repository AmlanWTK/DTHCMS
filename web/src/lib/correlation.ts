/**
 * Correlation IDs on the client.
 *
 * Every backend response carries one, and an error that came from the API should quote
 * that one — it is the value written into the log line the engineer will search for.
 *
 * An error that never reached the server has no such ID, and this is where it would be
 * tempting to show nothing. That is the wrong answer: the operator is standing in front
 * of a patient, and "something went wrong" with no handle is a problem they cannot
 * report. So the client mints one. It matches nothing server-side, which is itself the
 * useful signal — an ID that appears in a report and in no log means the request never
 * arrived.
 */

const CLIENT_PREFIX = 'web-';

export function newCorrelationID(): string {
  const uuid =
    typeof crypto !== 'undefined' && 'randomUUID' in crypto
      ? crypto.randomUUID()
      : Math.random().toString(36).slice(2);
  return `${CLIENT_PREFIX}${uuid}`;
}

/** True for an ID this client invented, meaning the request never reached the server. */
export function isClientMinted(id: string): boolean {
  return id.startsWith(CLIENT_PREFIX);
}
