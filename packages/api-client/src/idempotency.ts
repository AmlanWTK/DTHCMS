import { idempotencyKey, isUuidV7 } from '@dthcms/shared-schemas';

import { REQUESTED_WITH_HEADER, REQUESTED_WITH_VALUE } from './session';

/**
 * The client's side of `Idempotency-Key` (CP24).
 *
 * The server answers a retried mutating request from a stored response rather than running
 * the handler again — but only if the retry carries the *same* key as the attempt it is
 * retrying. That is the whole contract, and it is easy to get backwards: a fresh key on
 * each retry defeats the mechanism silently, which is the worst way for it to fail.
 *
 * Two helpers, because there are two situations:
 *
 *   `writing()`      one user gesture the screen will not repeat on its own — the console's
 *                    buttons. Mutations never retry automatically (`shouldRetryMutation`),
 *                    so a fresh key per call is right.
 *
 *   `beginAttempt()` a write that *will* be retried — an offline outbox draining. Persist
 *                    `key` beside the `event_id` and send it on every attempt until the
 *                    server answers.
 *
 * A *new* clinical action always gets a new key. Correcting a value the operator mistyped
 * is a new action, not a retry; sending the old key would be refused with
 * `IDEMPOTENCY_KEY_REUSED`, which is the mechanism doing its job.
 */

export const IDEMPOTENCY_HEADER = 'Idempotency-Key';

/** Set by the server on a response it replayed rather than recomputed. */
export const IDEMPOTENCY_REPLAYED_HEADER = 'Idempotency-Replayed';

/**
 * Header params for one mutating call: the forgery guard and a fresh key.
 *
 * Deliberately a different name from `guarded`, which is what a state-changing call needed
 * before this checkpoint: a call site that has not been updated fails to type-check rather
 * than failing at runtime with a 422.
 */
export function writing<E extends Record<string, string>>(extra?: E): WriteParams<E> {
  return headersFor(idempotencyKey(), extra);
}

/**
 * The header parameters a state-changing call needs, typed so that the generated client's
 * literal `'DTHCMS'` still matches — an object literal would widen it to `string` and the
 * call site would not compile.
 */
export type WriteParams<E extends Record<string, string> = Record<never, never>> = {
  header: {
    [REQUESTED_WITH_HEADER]: typeof REQUESTED_WITH_VALUE;
    [IDEMPOTENCY_HEADER]: string;
  } & E;
};

function headersFor<E extends Record<string, string>>(key: string, extra?: E): WriteParams<E> {
  return {
    header: {
      [REQUESTED_WITH_HEADER]: REQUESTED_WITH_VALUE,
      [IDEMPOTENCY_HEADER]: key,
      ...(extra ?? ({} as E)),
    },
  } as WriteParams<E>;
}

export interface Attempt {
  /** The key, stable for the life of this attempt. */
  readonly key: string;
  /** Header params carrying this attempt's key — the same one on every retry. */
  params<E extends Record<string, string>>(extra?: E): WriteParams<E>;
}

/**
 * Starts one attempt at a mutating request, or resumes one from a persisted key.
 *
 * Hold the returned object for as long as the operator might retry — across a network
 * failure, an app restart, a morning offline. A station app persists `key` in its outbox
 * row beside the `event_id`; both survive until the server has answered.
 */
export function beginAttempt(key: string = idempotencyKey()): Attempt {
  if (!isUuidV7(key)) {
    throw new Error(`an idempotency key must be a UUIDv7, got ${JSON.stringify(key)}`);
  }
  return {
    key,
    params<E extends Record<string, string>>(extra?: E) {
      return headersFor(key, extra);
    },
  };
}

/** Whether the server answered from its store rather than performing the write again. */
export function wasReplayed(response: Response): boolean {
  return response.headers.get(IDEMPOTENCY_REPLAYED_HEADER) === 'true';
}
