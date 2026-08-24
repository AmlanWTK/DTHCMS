import { QueryClient, type DefaultOptions } from '@tanstack/react-query';

import { ApiError, NetworkError } from '@/lib/api';

/**
 * Query defaults.
 *
 * Three of these are clinical decisions rather than performance tuning.
 */
export const queryDefaults: DefaultOptions = {
  queries: {
    /*
     * Half a minute. Long enough that moving between screens does not re-fetch the same
     * patient repeatedly on a clinic's shared connection; short enough that a value a
     * colleague recorded two minutes ago is not still hidden behind a cache.
     */
    staleTime: 30_000,

    /*
     * A physician who tabs back to the application after taking a call is, at that
     * moment, about to act on what is on the screen. Refetching is the cheap half of
     * that trade.
     */
    refetchOnWindowFocus: true,
    refetchOnReconnect: true,

    retry: (failureCount, error) => {
      // A 4xx will not succeed on the third attempt either, and retrying only delays
      // telling the operator what is wrong.
      if (error instanceof ApiError) return error.retryable && failureCount < 2;
      // A request that never reached the server is exactly what a clinic's connection
      // does intermittently. This one is worth retrying.
      if (error instanceof NetworkError) return failureCount < 3;
      return false;
    },
    retryDelay: (attempt) => Math.min(1000 * 2 ** attempt, 8000),
  },

  mutations: {
    /*
     * Never. A mutation in this application records a clinical observation, and an
     * automatic retry after an ambiguous failure is how one reading becomes two rows in
     * the ledger — which, the ledger being append-only, is not something anybody can
     * quietly tidy up afterwards.
     *
     * Retrying a write is a decision for the screen that owns it, with an idempotency
     * key, once CP12 defines one.
     */
    retry: false,
  },
};

export function createQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: queryDefaults });
}
