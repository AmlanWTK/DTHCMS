import type { HealthResponse } from '../schemas/status';

/**
 * What the two endpoints mean together.
 *
 * The distinction is operational and worth keeping in the model rather than in a
 * component: alive is not the same as ready. A process that answers /healthz but fails
 * /readyz is running and cannot serve — which for an operator means "do not start
 * entering a visit yet", not "it is broken".
 */

export type ServerState = 'unreachable' | 'live-not-ready' | 'ready';

export interface SystemStatus {
  state: ServerState;
  service: string;
  version: string;
  /** Dependency name to status word, from /readyz. */
  checks: Record<string, string>;
  checkedAt: number;
}

export function toSystemStatus(
  live: HealthResponse | null,
  ready: HealthResponse | null,
  checkedAt: number,
): SystemStatus {
  if (live === null) {
    return { state: 'unreachable', service: '', version: '', checks: {}, checkedAt };
  }

  const readyOk = ready !== null && ready.status === 'ok';

  return {
    state: readyOk ? 'ready' : 'live-not-ready',
    service: live.service,
    version: live.version,
    checks: ready?.checks ?? {},
    checkedAt,
  };
}
