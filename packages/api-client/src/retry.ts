import { ApiError, NetworkError } from './errors';

/**
 * What is worth retrying, and what is not.
 *
 * These live here rather than in either application because they are clinical policy
 * wearing the clothes of a networking concern, and two surfaces quietly disagreeing about
 * them is exactly the drift this package exists to prevent.
 *
 * The functions take no dependency on TanStack Query — both surfaces pass them straight
 * into a `QueryClient`, but the rules are about clinics, not about a React library.
 */

/**
 * A query may be retried; a read that fails and succeeds is just a read.
 *
 * A 4xx will not succeed on the third attempt either, and retrying only delays telling
 * the operator what is wrong. A request that never reached the server is a different
 * matter: that is precisely what a clinic's connection does intermittently, and it is
 * worth more attempts than a server that answered.
 */
export function shouldRetryQuery(failureCount: number, error: unknown): boolean {
  if (error instanceof ApiError) return error.retryable && failureCount < 2;
  if (error instanceof NetworkError) return failureCount < 3;
  return false;
}

/** Exponential backoff, capped so a station never sits waiting longer than eight seconds. */
export function queryRetryDelay(attempt: number): number {
  return Math.min(1000 * 2 ** attempt, 8000);
}

/**
 * Never.
 *
 * A mutation in this application records a clinical observation, and an automatic retry
 * after an ambiguous failure is how one reading becomes two rows in the ledger — which,
 * the ledger being append-only, is not something anybody can quietly tidy up afterwards.
 *
 * Retrying a write is a decision for the screen that owns it, made explicitly, with the
 * `Idempotency-Key` the contract requires on every state-changing request. That header is
 * what makes a replay safe; without it, a retry is a duplicate.
 */
export function shouldRetryMutation(): boolean {
  return false;
}
