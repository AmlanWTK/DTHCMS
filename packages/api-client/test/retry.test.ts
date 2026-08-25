import { describe, expect, it } from 'vitest';

import {
  ApiError,
  NetworkError,
  queryRetryDelay,
  shouldRetryMutation,
  shouldRetryQuery,
} from '../src/index';

/**
 * What is worth retrying.
 *
 * These rules live in this package so the station app and the web application cannot
 * quietly disagree about them, and they are clinical policy wearing the clothes of a
 * networking concern. Both surfaces tested them through their own query configuration;
 * neither tested them here, which meant the module that owns the rule had no test of the
 * rule. Coverage found that on the day the floor was switched on.
 */

function apiError(status: number) {
  return new ApiError({
    status,
    code: 'X',
    kind: 'technical',
    messageEN: '',
    messageBN: '',
    correlationID: '',
  });
}

describe('reads', () => {
  it.each([400, 403, 404, 409, 422])('does not retry a %i — it will fail the same way', (s) => {
    // Sending a wrong request three more times only delays telling the operator what is
    // wrong.
    expect(shouldRetryQuery(0, apiError(s))).toBe(false);
  });

  it.each([408, 429, 500, 503, 504])('retries a %i — the server asked for it later', (s) => {
    expect(shouldRetryQuery(0, apiError(s))).toBe(true);
  });

  it('gives up on a server error after two attempts', () => {
    expect(shouldRetryQuery(1, apiError(500))).toBe(true);
    expect(shouldRetryQuery(2, apiError(500))).toBe(false);
  });

  it('is more patient with a request that never arrived', () => {
    /*
     * The asymmetry is deliberate. A server that answered 500 has a problem retrying will
     * not fix. A request that never left the building is what a clinic's connection does
     * several times an hour, and the next attempt frequently works.
     */
    expect(shouldRetryQuery(2, new NetworkError(new Error('offline')))).toBe(true);
    expect(shouldRetryQuery(3, new NetworkError(new Error('offline')))).toBe(false);
  });

  it('does not retry something it cannot classify', () => {
    // An unknown failure is not assumed transient. Retrying a bug is just running it
    // again.
    expect(shouldRetryQuery(0, new Error('who knows'))).toBe(false);
    expect(shouldRetryQuery(0, 'a string')).toBe(false);
  });
});

describe('writes', () => {
  it('never retries, whatever happened', () => {
    /*
     * The rule that matters most. A mutation records a clinical observation, and an
     * automatic retry after an ambiguous failure is how one blood-pressure reading
     * becomes two rows in a ledger nobody can edit afterwards.
     *
     * Retrying a write is a decision for the screen that owns it, made explicitly, with
     * the Idempotency-Key the contract requires (docs/api-conventions.md §4).
     */
    expect(shouldRetryMutation()).toBe(false);
  });
});

describe('backoff', () => {
  it('grows exponentially', () => {
    expect(queryRetryDelay(0)).toBe(1000);
    expect(queryRetryDelay(1)).toBe(2000);
    expect(queryRetryDelay(2)).toBe(4000);
  });

  it('is capped, so a station never sits waiting longer than eight seconds', () => {
    // Past that, an operator has already decided the application is broken and is
    // reaching for the paper form.
    expect(queryRetryDelay(10)).toBe(8000);
    expect(queryRetryDelay(100)).toBe(8000);
  });
});
