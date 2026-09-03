import {
  ApiError,
  NetworkError,
  REQUEST_ID_HEADER,
  apiFetch as baseApiFetch,
  createApiClient,
  createRefreshingFetch,
  unwrap,
} from '@dthcms/api-client';
import type { z } from 'zod';

import { currentActiveRole } from '@/lib/active-role';
import { API_BASE_URL } from '@/lib/env';

/**
 * The web application's API surface.
 *
 * Everything of substance moved into `@dthcms/api-client` at CP12, so that the station
 * app and the web application cannot drift on what an error means or what is worth
 * retrying. What is left here is the one thing that is genuinely web-specific: where the
 * backend is.
 */

export { API_BASE_URL, ApiError, NetworkError, REQUEST_ID_HEADER, unwrap };

/**
 * The typed client, bound to this deployment's API origin.
 *
 * Paths are checked against `api/openapi.yaml` at compile time — `api.GET('/helthz')` is
 * a type error rather than a 404 discovered by an operator.
 */
const fetchWithSession = sessionFetch();

export const api = createApiClient({ baseUrl: API_BASE_URL, fetch: fetchWithSession });

/**
 * The same fetch the typed client uses — bearer, refresh, active role — for the one kind
 * of response the contract's JSON types cannot describe: a file. The audit export (CP22)
 * is a PDF with its signature in headers.
 */
export function authenticatedFetch(path: string, init?: RequestInit): Promise<Response> {
  return fetchWithSession(`${API_BASE_URL}${path}`, init);
}

/**
 * Listeners for the moment the session is found to be gone — the refresh credential has
 * expired, or somebody ended the session from another device.
 *
 * A tiny registry rather than an import of the session store, because the store imports
 * this module to make its requests, and a cycle between the two is the kind of thing that
 * works until a bundler changes its evaluation order.
 */
const sessionLostListeners = new Set<() => void>();

export function onSessionLost(listener: () => void): () => void {
  sessionLostListeners.add(listener);
  return () => sessionLostListeners.delete(listener);
}

function sessionFetch(): typeof globalThis.fetch {
  return createRefreshingFetch({
    baseUrl: API_BASE_URL,
    // The hat being worn travels with every request (CP20, [R-02]). The server decides
    // for that role alone, which is also the role the sidebar is scoped to.
    authorize: (request) => {
      const role = currentActiveRole();
      if (!role) return request;
      const headers = new Headers(request.headers);
      headers.set('X-Active-Role', role);
      return new Request(request, { headers });
    },
    onSessionLost: () => {
      for (const listener of sessionLostListeners) listener();
    },
  });
}

/**
 * A fetch validated by a Zod schema rather than by the contract's types, bound to the
 * same origin.
 *
 * Types are erased at runtime; a schema is not. Where a wrong shape would be read by a
 * clinician as a number rather than caught as an error, this is the safer call.
 */
export function apiFetch<T>(path: string, init: RequestInit & { schema: z.ZodType<T> }) {
  return baseApiFetch(path, { ...init, baseUrl: API_BASE_URL });
}
