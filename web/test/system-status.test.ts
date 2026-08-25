import { describe, expect, it } from 'vitest';

import { toSystemStatus } from '@/features/system-status/model/status';
import { healthResponseSchema } from '@/features/system-status/schemas/status';

/**
 * Alive is not the same as ready.
 *
 * The distinction is the whole reason this model exists rather than a component reading
 * two booleans: a process that answers /healthz but fails /readyz is running and cannot
 * serve, which for an operator means "do not start entering a visit yet" — not "it is
 * broken", and not "everything is fine". Getting that wrong in either direction sends a
 * clinic the wrong instruction during an outage.
 */

const live = { status: 'ok', service: 'api', version: '0.1.0-dev' };

describe('reading the two endpoints together', () => {
  it('is unreachable when the process does not answer at all', () => {
    const status = toSystemStatus(null, null, 1000);
    expect(status.state).toBe('unreachable');
    expect(status.checks).toEqual({});
  });

  it('is unreachable even if a readiness response somehow arrived', () => {
    // Liveness is the cheaper, more fundamental probe. If it did not answer, the process
    // is not to be trusted whatever else came back.
    const status = toSystemStatus(null, { ...live, checks: { postgres: 'ok' } }, 1000);
    expect(status.state).toBe('unreachable');
  });

  it('is live-not-ready when the process answers but readiness does not', () => {
    expect(toSystemStatus(live, null, 1000).state).toBe('live-not-ready');
  });

  it('is live-not-ready when readiness answers unready', () => {
    const ready = { ...live, status: 'unready', checks: { postgres: 'unavailable' } };
    const status = toSystemStatus(live, ready, 1000);

    expect(status.state).toBe('live-not-ready');
    // The failing dependency survives to the screen. An operator being told "not ready"
    // without being told what is unhappy cannot tell anyone anything useful.
    expect(status.checks).toEqual({ postgres: 'unavailable' });
  });

  it('is ready only when readiness says ok', () => {
    const ready = { ...live, checks: { postgres: 'ok', redis: 'ok' } };
    const status = toSystemStatus(live, ready, 1000);

    expect(status.state).toBe('ready');
    expect(status.service).toBe('api');
    expect(status.version).toBe('0.1.0-dev');
  });

  it('takes identity from liveness, not readiness', () => {
    // Readiness may be absent; the service name and version must still show.
    const status = toSystemStatus(live, null, 1000);
    expect(status.service).toBe('api');
    expect(status.version).toBe('0.1.0-dev');
  });

  it('carries the time it was checked, so a stale panel can say so', () => {
    expect(toSystemStatus(live, live, 1_724_500_000_000).checkedAt).toBe(1_724_500_000_000);
  });
});

describe('the wire shape', () => {
  it('accepts a liveness response, which has no checks', () => {
    expect(healthResponseSchema.parse(live)).toEqual(live);
  });

  it('accepts a readiness response with dependency statuses', () => {
    const ready = { ...live, checks: { postgres: 'ok', blobstore: 'unavailable' } };
    expect(healthResponseSchema.safeParse(ready).success).toBe(true);
  });

  it('rejects a response missing the fields the panel renders', () => {
    // Parsed rather than cast: a backend that renames `service` should fail here, in one
    // place with a readable message, rather than as a blank cell on an operations panel.
    expect(healthResponseSchema.safeParse({ status: 'ok' }).success).toBe(false);
  });
});
