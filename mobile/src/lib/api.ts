import {
  ApiError,
  NetworkError,
  REQUEST_ID_HEADER,
  apiFetch as baseApiFetch,
  bearerAuthorizer,
  createApiClient,
  createRefreshingFetch,
  unwrap,
} from '@dthcms/api-client';
import type { z } from 'zod';

import { currentActiveRole } from '@/lib/active-role';
import { APP_VERSION } from '@/lib/build';
import { getAccessToken, refreshCredentials } from '@/lib/credentials';
import { deviceAuthorizer } from '@/lib/device';

/**
 * The station application's API surface.
 *
 * Identical in substance to the web's, and deliberately so: both are thin bindings over
 * `@dthcms/api-client`, which is generated from `api/openapi.yaml`. What differs is only
 * where the backend is.
 */

/**
 * Where the backend is.
 *
 * `EXPO_PUBLIC_` is Expo's equivalent of Next's `NEXT_PUBLIC_` — inlined by Metro at
 * build time, so the value is compiled into the APK rather than read at runtime.
 *
 * The default is only useful in a simulator. **A physical station tablet cannot reach
 * `localhost`** — that address is the tablet itself, and a device build left on this
 * default fails every request with a connection error that looks exactly like the clinic
 * being offline. A development build points at the engineer's LAN address; the clinic's
 * build points at the clinic's server. Both are set through the environment, never
 * committed.
 */
export const API_BASE_URL: string = process.env.EXPO_PUBLIC_API_BASE_URL ?? 'http://localhost:8080';

export { ApiError, NetworkError, REQUEST_ID_HEADER, unwrap };

/**
 * The typed client, bound to this build's API origin.
 *
 * Paths are checked against the contract at compile time, and Metro's bundle check in CI
 * compiles this file on every push — so a path that does not exist fails the build rather
 * than a clinic session.
 */
export const api = createApiClient({
  baseUrl: API_BASE_URL,
  // The station app holds its own credentials (lib/credentials.ts) and lets no cookie jar
  // hold any for it. A jar that re-sent a refresh token the app had since rotated would
  // present a spent token and trip the reuse detector.
  credentials: 'omit',
  fetch: sessionFetch(),
});

/**
 * Listeners for the moment the session is found to be gone — the stored refresh token was
 * refused, or the session was ended from another device. The session store subscribes;
 * a registry rather than an import because the store imports this module.
 */
const sessionLostListeners = new Set<() => void>();

export function onSessionLost(listener: () => void): () => void {
  sessionLostListeners.add(listener);
  return () => sessionLostListeners.delete(listener);
}

/**
 * The active role on every request (CP41, [R-02]). Applied before the device signature so
 * the signature covers it.
 */
function withActiveRole(request: Request): Request {
  const role = currentActiveRole();
  if (!role) return request;
  const headers = new Headers(request.headers);
  headers.set('X-Active-Role', role);
  return new Request(request, { headers });
}

function sessionFetch(): typeof globalThis.fetch {
  const bearer = bearerAuthorizer(getAccessToken);
  const device = deviceAuthorizer(APP_VERSION);
  return createRefreshingFetch({
    baseUrl: API_BASE_URL,
    // The hat, then the token, then the device signature over the finished request. The
    // order matters: the signature (CP18) covers the finished request, so anything added
    // after it would not be signed — and the active role (CP41) is exactly the kind of
    // header a tampering proxy would want to change.
    authorize: async (request) => device(await bearer(withActiveRole(request))),
    refresh: () => refreshCredentials({ baseUrl: API_BASE_URL }),
    onSessionLost: () => {
      for (const listener of sessionLostListeners) listener();
    },
  });
}

/** A fetch validated by a Zod schema rather than by the contract's types. */
export function apiFetch<T>(path: string, init: RequestInit & { schema: z.ZodType<T> }) {
  return baseApiFetch(path, { ...init, baseUrl: API_BASE_URL });
}
