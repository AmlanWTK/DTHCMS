import {
  ApiError,
  NetworkError,
  REQUEST_ID_HEADER,
  apiFetch as baseApiFetch,
  createApiClient,
  unwrap,
} from '@dthcms/api-client';
import type { z } from 'zod';

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
export const api = createApiClient({ baseUrl: API_BASE_URL });

/** A fetch validated by a Zod schema rather than by the contract's types. */
export function apiFetch<T>(path: string, init: RequestInit & { schema: z.ZodType<T> }) {
  return baseApiFetch(path, { ...init, baseUrl: API_BASE_URL });
}
