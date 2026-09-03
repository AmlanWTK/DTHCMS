import createClient from 'openapi-fetch';
import type { z } from 'zod';

import { ApiError, NetworkError, REQUEST_ID_HEADER, apiErrorFromBody, toApiError } from './errors';
import type { paths } from './schema';
import { REQUESTED_WITH_HEADER, REQUESTED_WITH_VALUE } from './session';

/**
 * The client.
 *
 * `src/schema.ts` is generated from api/openapi.yaml and describes every path, parameter
 * and response the contract allows. This file is the runtime around it, and it exists to
 * do three things generation does not:
 *
 *   - carry the credential the surface uses — the browser's cookie, or the station app's
 *     bearer token, attached by the refreshing fetch (ADR-0010);
 *   - turn a failure into an ApiError or a NetworkError, which are different instructions
 *     to the person reading the screen;
 *   - carry the correlation ID from the response to wherever the error is displayed.
 */

export interface ApiClientOptions {
  /** Where the backend is. Same-origin in a deployment; its own port locally. */
  baseUrl: string;
  /** Injectable for tests and for React Native, whose fetch is not the DOM's. */
  fetch?: typeof globalThis.fetch;
  /**
   * `include` for the browser, whose credential is a cookie. `omit` for the station app,
   * which carries its own bearer token and must not let a cookie jar it does not control
   * re-send a refresh token the app has since rotated.
   */
  credentials?: RequestCredentials;
}

export type ApiClient = ReturnType<typeof createApiClient>;

export function createApiClient(options: ApiClientOptions) {
  return createClient<paths>({
    baseUrl: options.baseUrl,
    // The forgery guard goes on every request. The server only checks it on requests that
    // change state, but sending it unconditionally means no call site can forget it.
    headers: { Accept: 'application/json', [REQUESTED_WITH_HEADER]: REQUESTED_WITH_VALUE },
    // Cookies carry the session in the browser. Never a token in a header read from
    // storage — see ADR-0010. The station app opts out; see ApiClientOptions.
    credentials: options.credentials ?? 'include',
    /*
     * Resolved per call, not captured at module load.
     *
     * The client is created at module scope on both surfaces, and openapi-fetch would
     * otherwise close over whatever `globalThis.fetch` happened to be at import time. On
     * React Native that can precede a polyfill; under a test runner it silently defeats
     * every attempt to stub the network, which is how this was found — a unit test opened
     * a real socket to localhost:8080.
     */
    fetch: options.fetch ?? ((request: Request) => globalThis.fetch(request)),
  });
}

/**
 * openapi-fetch returns `{ data, error, response }`; TanStack Query wants a value or a
 * throw. This is the adapter, and it is where the two error classes are decided.
 */
export async function unwrap<T>(
  call: Promise<{ data?: T; error?: unknown; response: Response }>,
): Promise<T> {
  let result: { data?: T; error?: unknown; response: Response };

  try {
    result = await call;
  } catch (cause) {
    // The request never arrived. That is a different sentence on screen — "you are
    // offline" rather than "the clinic server rejected this" — and a different retry rule.
    throw new NetworkError(cause);
  }

  const { data, error, response } = result;

  if (!response.ok || error !== undefined) {
    throw apiErrorFromBody(error, response);
  }

  return data as T;
}

/**
 * The un-generated escape hatch: a fetch validated by a Zod schema rather than by the
 * contract's types.
 *
 * Types are erased at runtime, so a backend that renames a field ships happily past `tsc`
 * and surfaces as `undefined` in a table cell three screens from the cause. Where that
 * matters more than the convenience of generated paths — and it does for anything a
 * clinician reads a number off — this parses instead.
 */
export async function apiFetch<T>(
  path: string,
  init: RequestInit & { schema: z.ZodType<T>; baseUrl?: string },
): Promise<T> {
  const { schema, baseUrl = '', ...requestInit } = init;

  let response: Response;
  try {
    response = await fetch(`${baseUrl}${path}`, {
      ...requestInit,
      headers: {
        Accept: 'application/json',
        [REQUESTED_WITH_HEADER]: REQUESTED_WITH_VALUE,
        ...requestInit.headers,
      },
      credentials: 'include',
    });
  } catch (cause) {
    throw new NetworkError(cause);
  }

  const correlationID = response.headers.get(REQUEST_ID_HEADER) ?? '';

  if (!response.ok) {
    throw await toApiError(response, correlationID);
  }

  const body: unknown = await response.json();
  // Parsed rather than cast. A backend that changes a field name should fail here, in one
  // place with a readable message, rather than as `undefined` somewhere in a table cell.
  return schema.parse(body);
}

export { ApiError, NetworkError, REQUEST_ID_HEADER };
