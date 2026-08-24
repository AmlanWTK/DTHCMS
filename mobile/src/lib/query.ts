import { QueryClient, type DefaultOptions } from '@tanstack/react-query';
import { queryRetryDelay, shouldRetryMutation, shouldRetryQuery } from '@dthcms/api-client';

/**
 * Query defaults for the station application.
 *
 * The retry rules are the shared ones — a write is never retried automatically on either
 * surface, because a duplicated clinical observation in an append-only ledger is not
 * something anybody can tidy up afterwards.
 *
 * The caching differs from the web's, and that difference is the point of having two
 * files rather than one.
 */
export const queryDefaults: DefaultOptions = {
  queries: {
    /*
     * Five minutes rather than the web's thirty seconds.
     *
     * A station tablet is offline for stretches of a clinic session by design (ADR-0004),
     * and re-fetching on every screen change spends a connection that may not be there.
     * The queue is the exception and will set its own, shorter, staleness when it has
     * real data at CP33 — a stale queue is a patient standing in the wrong place.
     */
    staleTime: 5 * 60_000,

    /*
     * Reconnecting is the moment a station has been waiting for; that one is worth
     * taking. Focus is not — on Android, returning from the camera or a permission
     * dialog fires it, and a burst of re-fetches on a shared clinic connection is the
     * opposite of helpful.
     */
    refetchOnReconnect: true,
    refetchOnWindowFocus: false,

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
