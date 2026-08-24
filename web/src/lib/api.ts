import {
  ApiError,
  NetworkError,
  REQUEST_ID_HEADER,
  apiFetch as baseApiFetch,
  createApiClient,
  unwrap,
} from '@dthcms/api-client';
import type { z } from 'zod';

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
export const api = createApiClient({ baseUrl: API_BASE_URL });

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
