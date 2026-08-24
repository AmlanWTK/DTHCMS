import { z } from 'zod';

import { API_BASE_URL } from '@/lib/env';

/**
 * The API client.
 *
 * Small on purpose: CP12 generates a typed client from the OpenAPI document, and this
 * file exists to establish the two things that generation will not give us — how an error
 * from §8.6 becomes something a person can act on, and where the correlation ID comes
 * from.
 */

export { API_BASE_URL };

/** Echoed by the backend on every response. An operator can quote it down the phone. */
export const REQUEST_ID_HEADER = 'X-Request-ID';

/** The one shape an error takes on the wire. Mirrors httpx.errorEnvelope. */
const errorEnvelopeSchema = z.object({
  error: z.object({
    code: z.string(),
    kind: z.string(),
    message: z.string(),
    message_bn: z.string(),
    fields: z.record(z.string(), z.string()).optional(),
    correlation_id: z.string().optional(),
  }),
});

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly kind: string;
  /** Both languages, because the interface may be in either when this is displayed. */
  readonly messageEN: string;
  readonly messageBN: string;
  readonly fields: Record<string, string>;
  readonly correlationID: string;

  constructor(init: {
    status: number;
    code: string;
    kind: string;
    messageEN: string;
    messageBN: string;
    fields?: Record<string, string>;
    correlationID: string;
  }) {
    super(`${init.code}: ${init.messageEN}`);
    this.name = 'ApiError';
    this.status = init.status;
    this.code = init.code;
    this.kind = init.kind;
    this.messageEN = init.messageEN;
    this.messageBN = init.messageBN;
    this.fields = init.fields ?? {};
    this.correlationID = init.correlationID;
  }

  /**
   * Whether retrying could plausibly succeed.
   *
   * A 4xx will not: the request is wrong, and sending it three more times only delays
   * telling the operator so. 408 and 429 are the exceptions — both are the server asking
   * for the same request later.
   */
  get retryable(): boolean {
    if (this.status === 408 || this.status === 429) return true;
    return this.status >= 500;
  }
}

/** Thrown when the request never reached the server. Distinct from an error it returned. */
export class NetworkError extends Error {
  readonly cause: unknown;

  constructor(cause: unknown) {
    super('The request did not reach the server.');
    this.name = 'NetworkError';
    this.cause = cause;
  }
}

export async function apiFetch<T>(
  path: string,
  init: RequestInit & { schema: z.ZodType<T> },
): Promise<T> {
  const { schema, ...requestInit } = init;

  let response: Response;
  try {
    response = await fetch(`${API_BASE_URL}${path}`, {
      ...requestInit,
      headers: { Accept: 'application/json', ...requestInit.headers },
      // Cookies carry the session from CP16. Never a token in a header read from storage
      // — see ADR-0010.
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

async function toApiError(response: Response, correlationID: string): Promise<ApiError> {
  const fallback = new ApiError({
    status: response.status,
    code: 'unknown',
    kind: 'internal',
    messageEN: 'Something went wrong.',
    messageBN: 'কিছু একটা সমস্যা হয়েছে।',
    correlationID,
  });

  let body: unknown;
  try {
    body = await response.json();
  } catch {
    return fallback;
  }

  const parsed = errorEnvelopeSchema.safeParse(body);
  if (!parsed.success) return fallback;

  const { error } = parsed.data;
  return new ApiError({
    status: response.status,
    code: error.code,
    kind: error.kind,
    messageEN: error.message,
    messageBN: error.message_bn,
    fields: error.fields,
    // The body's correlation ID is authoritative — it is the one written into the log.
    correlationID: error.correlation_id ?? correlationID,
  });
}
