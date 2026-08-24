/**
 * Values that come from the environment, resolved once.
 *
 * Both the API client and the Content Security Policy need the API's origin, and they
 * have to agree: a policy that does not name the origin the client calls produces a
 * feature that is silently blocked in the browser and works perfectly in every test that
 * does not run one. That is what happened at CP10, and Lighthouse is what found it.
 */

/**
 * Where the backend is.
 *
 * Same-origin in a real deployment — CP03 puts the web application and the API behind one
 * hostname, which ADR-0010 requires so that the session cookie travels. In local
 * development the Go service is on its own port, which is why this is configurable and
 * why the policy below has to account for it.
 */
export const API_BASE_URL = process.env.NEXT_PUBLIC_API_BASE_URL ?? 'http://localhost:8080';

/** The API's origin, or null when it is same-origin or the value is not a URL. */
export function apiOrigin(): string | null {
  try {
    return new URL(API_BASE_URL).origin;
  } catch {
    return null;
  }
}
