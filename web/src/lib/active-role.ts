/**
 * The role the interface is acting as, for the API client (CP20).
 *
 * A registry rather than a read of the session store: the store imports the API client,
 * so the client cannot import the store. The store writes here whenever the active role
 * changes; the client reads it when it builds a request and sends it as `X-Active-Role`,
 * so that what the server allows is what the interface is showing.
 */

let current: string | null = null;

export function setCurrentActiveRole(role: string | null): void {
  current = role;
}

export function currentActiveRole(): string | null {
  return current;
}
