import { describe, expect, it } from 'vitest';

import { queryDefaults } from '@/lib/query';
import { ApiError, NetworkError } from '@/lib/api';

/**
 * The query defaults, tested as policy rather than configuration.
 *
 * Two of these are clinical decisions, and both are the kind that get "tidied up" by
 * somebody adding a sensible-looking retry a year from now.
 */

const retry = queryDefaults.queries?.retry;

function apiError(status: number) {
  return new ApiError({
    status,
    code: 'x',
    kind: 'y',
    messageEN: '',
    messageBN: '',
    correlationID: '',
  });
}

describe('queries', () => {
  it('does not retry a rejected request', () => {
    // A 422 will not succeed on the third attempt. Retrying only delays telling the
    // operator what is wrong with what they entered.
    expect(typeof retry).toBe('function');
    if (typeof retry !== 'function') return;
    expect(retry(0, apiError(422))).toBe(false);
  });

  it('retries a server error, a few times', () => {
    if (typeof retry !== 'function') return;
    expect(retry(0, apiError(503))).toBe(true);
    expect(retry(5, apiError(503))).toBe(false);
  });

  it('retries a request that never arrived, which is what a clinic connection does', () => {
    if (typeof retry !== 'function') return;
    expect(retry(0, new NetworkError(new Error('offline')))).toBe(true);
  });

  it('refetches when the tab regains focus', () => {
    // A physician tabbing back after a phone call is about to act on what is on screen.
    expect(queryDefaults.queries?.refetchOnWindowFocus).toBe(true);
  });
});

describe('mutations', () => {
  it('never retries', () => {
    /*
     * The one that matters. A mutation here records a clinical observation, and an
     * automatic retry after an ambiguous failure is how one reading becomes two rows in
     * an append-only ledger — which nobody can quietly tidy up afterwards.
     *
     * Retrying a write is a decision for the screen that owns it, with an idempotency
     * key, once CP12 defines one.
     */
    expect(queryDefaults.mutations?.retry).toBe(false);
  });
});
