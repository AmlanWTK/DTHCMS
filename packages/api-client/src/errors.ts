import { errorEnvelopeSchema } from '@dthcms/shared-schemas';

/**
 * What an API failure becomes by the time a screen sees it.
 *
 * Generated types stop at the boundary: they describe the shape of a 404 body, not what
 * an interface should do about one. These two classes are that missing half, and they are
 * hand-written on purpose — the distinctions they draw are clinical and operational
 * rather than mechanical, and no generator knows about them.
 */

/** Echoed by the backend on every response. An operator can quote it down the phone. */
export const REQUEST_ID_HEADER = 'X-Request-ID';

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly kind: string;
  /** Both languages, because the interface may be in either when this is displayed. */
  readonly messageEN: string;
  readonly messageBN: string;
  readonly fields: Record<string, string>;
  /**
   * The same per-field messages in Bangla (CP29).
   *
   * Kept beside `fields` rather than replacing it, so a build that reads only `fields`
   * keeps working. `fieldMessage` below is the one both surfaces should call: it falls back
   * to English rather than showing nothing, because an English sentence is better than a
   * blank space at the moment somebody is trying to fix a form.
   */
  readonly fieldsBN: Record<string, string>;
  readonly correlationID: string;

  constructor(init: {
    status: number;
    code: string;
    kind: string;
    messageEN: string;
    messageBN: string;
    fields?: Record<string, string>;
    fieldsBN?: Record<string, string>;
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
    this.fieldsBN = init.fieldsBN ?? {};
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

/**
 * The last resort, for a failure that never reached a DTHCMS handler — a proxy timeout, a
 * load balancer's HTML error page, a truncated response.
 *
 * Still bilingual. A gateway error is exactly when the operator is least likely to be
 * reading whatever language the proxy happens to speak.
 */
function unknownError(status: number, correlationID: string): ApiError {
  return new ApiError({
    status,
    code: 'unknown',
    kind: 'internal',
    messageEN: 'Something went wrong.',
    messageBN: 'কিছু একটা সমস্যা হয়েছে।',
    correlationID,
  });
}

/**
 * Turns an already-parsed error body into an ApiError.
 *
 * Used on the generated-client path, where the fetch layer has read the body already.
 */
export function apiErrorFromBody(body: unknown, response: Response): ApiError {
  const headerID = response.headers.get(REQUEST_ID_HEADER) ?? '';

  const parsed = errorEnvelopeSchema.safeParse(body);
  if (!parsed.success) return unknownError(response.status, headerID);

  const { error } = parsed.data;
  return new ApiError({
    status: response.status,
    code: error.code,
    kind: error.kind,
    messageEN: error.message,
    messageBN: error.message_bn,
    fields: error.fields,
    fieldsBN: error.fields_bn,
    // The body's correlation ID is authoritative — it is the one written into the log.
    correlationID: error.correlation_id ?? headerID,
  });
}

/** Reads the body off a failed Response and turns it into an ApiError. */
export async function toApiError(response: Response, correlationID: string): Promise<ApiError> {
  let body: unknown;
  try {
    body = await response.json();
  } catch {
    return unknownError(response.status, correlationID);
  }

  const parsed = errorEnvelopeSchema.safeParse(body);
  if (!parsed.success) return unknownError(response.status, correlationID);

  const { error } = parsed.data;
  return new ApiError({
    status: response.status,
    code: error.code,
    kind: error.kind,
    messageEN: error.message,
    messageBN: error.message_bn,
    fields: error.fields,
    fieldsBN: error.fields_bn,
    correlationID: error.correlation_id ?? correlationID,
  });
}

/**
 * One field's message, in the reader's language, falling back to English.
 *
 * The fallback is the point. A build that meets a field the server has a message for in only
 * one language must show that one rather than a blank space — somebody is standing at a desk
 * trying to fix a form, and silence is the worst of the three outcomes.
 */
export function fieldMessage(error: ApiError, field: string, locale: string): string | undefined {
  if (locale === 'bn' && error.fieldsBN[field]) return error.fieldsBN[field];
  return error.fields[field];
}

/** Every field the server named, in the reader's language. */
export function fieldMessages(error: ApiError, locale: string): Record<string, string> {
  const out: Record<string, string> = { ...error.fields };
  if (locale === 'bn') {
    for (const [field, message] of Object.entries(error.fieldsBN)) out[field] = message;
  }
  return out;
}
