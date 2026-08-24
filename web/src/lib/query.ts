import { QueryClient, type DefaultOptions } from '@tanstack/react-query';
import { queryRetryDelay, shouldRetryMutation, shouldRetryQuery } from '@dthcms/api-client';

/**
 * Query defaults.
 *
 * The retry rules moved into `@dthcms/api-client` at CP12 — they are clinical policy, and
 * the station app must not quietly disagree with the web about when a failed write may be
 * sent again. What stays here is the caching, which is a web-surface judgement.
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

    retry: shouldRetryQuery,
    retryDelay: queryRetryDelay,
  },

  mutations: {
    retry: shouldRetryMutation,
  },
};

export function createQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: queryDefaults });
}
