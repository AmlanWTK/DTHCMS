/**
 * The hat the operator is wearing, for the API client (CP41, [R-02]).
 *
 * A registry rather than a read of the session store, for the same reason the web has one:
 * the store imports the API client, so the client cannot import the store. The store writes
 * here whenever the active role changes; the client reads it when it builds a request and
 * sends it as `X-Active-Role`.
 *
 * That header is the mechanism, not a hint. The authorisation engine decides for that role
 * alone and refuses one the person does not hold, so a station app that forgot to send it
 * would get the union of every hat — which is exactly the over-grant §4.4 exists to stop.
 */

let current: string | null = null;

export function setCurrentActiveRole(role: string | null): void {
  current = role;
}

export function currentActiveRole(): string | null {
  return current;
}
