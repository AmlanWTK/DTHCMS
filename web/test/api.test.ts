import { afterEach, describe, expect, it, vi } from 'vitest';
import { z } from 'zod';

import { ApiError, NetworkError, apiFetch, REQUEST_ID_HEADER } from '@/lib/api';

/**
 * The API client, and specifically the two things CP12's generated client will not do:
 * turn the §8.6 error envelope into something a person can act on, and carry the
 * correlation ID all the way to the screen.
 */

const schema = z.object({ status: z.string() });

function respond(body: unknown, init: { status?: number; headers?: Record<string, string> } = {}) {
  return new Response(JSON.stringify(body), {
    status: init.status ?? 200,
    headers: { 'Content-Type': 'application/json', ...init.headers },
  });
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('a successful response', () => {
  it('parses the body rather than trusting it', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(respond({ status: 'ok' })));
    await expect(apiFetch('/healthz', { schema })).resolves.toEqual({ status: 'ok' });
  });

  it('rejects a body that does not match, instead of returning undefined fields', async () => {
    // The alternative is a renamed field arriving as `undefined` in a table cell three
    // screens away, where nobody can tell it from missing data.
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(respond({ state: 'ok' })));
    await expect(apiFetch('/healthz', { schema })).rejects.toThrow();
  });

  it('sends credentials, because the session is a cookie', async () => {
    // ADR-0010. If this ever stops being sent, every authenticated request silently
    // becomes anonymous.
    const fetchMock = vi.fn().mockResolvedValue(respond({ status: 'ok' }));
    vi.stubGlobal('fetch', fetchMock);
    await apiFetch('/healthz', { schema });
    expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({ credentials: 'include' });
  });
});

describe('an error response', () => {
  const envelope = {
    error: {
      code: 'patient.not_found',
      kind: 'not_found',
      message: 'No such patient.',
      message_bn: 'এমন কোনো রোগী নেই।',
      correlation_id: 'abc-123',
    },
  };

  it('carries both languages, because the interface may be in either', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(respond(envelope, { status: 404 })));

    const error = await apiFetch('/patients/1', { schema }).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).messageEN).toBe('No such patient.');
    expect((error as ApiError).messageBN).toBe('এমন কোনো রোগী নেই।');
    expect((error as ApiError).code).toBe('patient.not_found');
  });

  it('prefers the correlation ID in the body over the one in the header', async () => {
    // The body's is the one written into the log line. The header is a fallback for a
    // response that never reached the handler — a proxy timeout, say.
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValue(
          respond(envelope, { status: 404, headers: { [REQUEST_ID_HEADER]: 'header-999' } }),
        ),
    );

    const error = (await apiFetch('/patients/1', { schema }).catch((e: unknown) => e)) as ApiError;
    expect(error.correlationID).toBe('abc-123');
  });

  it('falls back to the header when the body is not an envelope', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response('<html>502</html>', {
          status: 502,
          headers: { [REQUEST_ID_HEADER]: 'header-999' },
        }),
      ),
    );

    const error = (await apiFetch('/patients/1', { schema }).catch((e: unknown) => e)) as ApiError;
    expect(error.correlationID).toBe('header-999');
    expect(error.code).toBe('unknown');
    // Still bilingual. A gateway error is exactly when the operator is least likely to be
    // reading the language the proxy happens to speak.
    expect(error.messageBN).not.toBe(error.messageEN);
  });
});

describe('what is worth retrying', () => {
  it.each([
    [400, false],
    [403, false],
    [404, false],
    [422, false],
    [408, true],
    [429, true],
    [500, true],
    [503, true],
  ])('status %i retryable: %s', (status, retryable) => {
    const error = new ApiError({
      status,
      code: 'x',
      kind: 'y',
      messageEN: '',
      messageBN: '',
      correlationID: '',
    });
    expect(error.retryable).toBe(retryable);
  });
});

describe('a request that never arrived', () => {
  it('is a NetworkError, distinct from an error the server returned', async () => {
    // The distinction is what lets a screen say "you are offline" instead of "the clinic
    // server rejected this", which are different instructions to the person reading.
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')));
    await expect(apiFetch('/healthz', { schema })).rejects.toBeInstanceOf(NetworkError);
  });
});
