'use client';

import { useQuery, type UseQueryResult } from '@tanstack/react-query';

import { apiFetch } from '@/lib/api';
import { healthResponseSchema } from '../schemas/status';
import { toSystemStatus, type SystemStatus } from '../model/status';

/**
 * Query keys are exported so that CP27's WebSocket layer can invalidate them by name
 * rather than by guessing at the string.
 */
export const systemStatusKeys = {
  all: ['system-status'] as const,
};

/**
 * Both health endpoints, as one answer.
 *
 * /readyz is allowed to fail without failing the query: an unready server is a state to
 * display, not an error to hide the panel behind. Only an unreachable server — where
 * /healthz itself did not answer — is a failure.
 */
export function useSystemStatus(): UseQueryResult<SystemStatus> {
  return useQuery({
    queryKey: systemStatusKeys.all,
    queryFn: async (): Promise<SystemStatus> => {
      const live = await apiFetch('/healthz', { schema: healthResponseSchema });

      const ready = await apiFetch('/readyz', { schema: healthResponseSchema }).catch(() => null);

      return toSystemStatus(live, ready, Date.now());
    },
    // Shorter than the default. This panel exists to answer "is the server up right now?",
    // and a cached answer from half a minute ago is not that.
    staleTime: 5_000,
    refetchInterval: 30_000,
  });
}
