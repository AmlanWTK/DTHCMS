import { afterEach, describe, expect, it, vi } from 'vitest';
import { z } from 'zod';

import { ApiError, NetworkError, api, apiFetch, API_BASE_URL } from '@/lib/api';

/**
 * The web binding, not the client's behaviour.
 *
 * How an error envelope becomes an ApiError, which failures are worth retrying, and where
 * the correlation ID comes from are all tested in `packages/api-client`, once, for both
 * surfaces. Duplicating them here would only create two places to update and one to
 * forget.
 *
 * What is worth asserting on this side is the part that is web-specific and easy to break
 * silently: that both entry points actually reach the configured API origin, and that the
 * error classes a screen catches are the same classes the shared package throws.
 */

function respond(body: unknown, init: { status?: number } = {}) {
  return new Response(JSON.stringify(body), {
    status: init.status ?? 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('the typed client', () => {
  it('is bound to the configured API origin', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(respond({ status: 'ok', service: 'api', version: '0.1.0-dev' }));
    vi.stubGlobal('fetch', fetchMock);

    await api.GET('/healthz');

    const request = fetchMock.mock.calls[0]?.[0] as Request;
    expect(request.url).toBe(`${API_BASE_URL}/healthz`);
  });
});

describe('the schema-validated path', () => {
  const schema = z.object({ status: z.string() });

  it('reaches the configured API origin too', async () => {
    // Two entry points, one origin. A bare path here previously meant a request to the
    // Next.js server rather than the Go one — which succeeds, returns HTML, and fails
    // somewhere else entirely.
    const fetchMock = vi.fn().mockResolvedValue(respond({ status: 'ok' }));
    vi.stubGlobal('fetch', fetchMock);

    await apiFetch('/healthz', { schema });

    expect(fetchMock.mock.calls[0]?.[0]).toBe(`${API_BASE_URL}/healthz`);
  });

  it('sends credentials, because the session is a cookie', async () => {
    // ADR-0010. If this ever stops being sent, every authenticated request silently
    // becomes anonymous.
    const fetchMock = vi.fn().mockResolvedValue(respond({ status: 'ok' }));
    vi.stubGlobal('fetch', fetchMock);

    await apiFetch('/healthz', { schema });

    expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({ credentials: 'include' });
  });

  it('throws the shared ApiError, which is what the screens catch', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        respond(
          {
            error: {
              code: 'patient.not_found',
              kind: 'not_found',
              message: 'No such patient.',
              message_bn: 'এমন কোনো রোগী নেই।',
            },
          },
          { status: 404 },
        ),
      ),
    );

    const error = await apiFetch('/patients/1', { schema }).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).messageBN).toBe('এমন কোনো রোগী নেই।');
  });

  it('throws the shared NetworkError when the request never arrives', async () => {
    // The distinction is what lets a screen say "you are offline" instead of "the clinic
    // server rejected this", which are different instructions to the person reading.
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')));
    await expect(apiFetch('/healthz', { schema })).rejects.toBeInstanceOf(NetworkError);
  });
});
