import { afterEach, describe, expect, it, vi } from 'vitest';
import { z } from 'zod';

/*
 * The binding now reaches the Keystore through lib/credentials, and the native module
 * cannot load under Node. Mocked the same way secure-storage.test.ts does; nothing here
 * exercises it.
 */
vi.mock('expo-secure-store', () => ({
  setItemAsync: vi.fn(async () => undefined),
  getItemAsync: vi.fn(async () => null),
  deleteItemAsync: vi.fn(async () => undefined),
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'WHEN_UNLOCKED_THIS_DEVICE_ONLY',
}));

const { ApiError, NetworkError, api, apiFetch, API_BASE_URL } = await import('../src/lib/api');
type ApiErrorInstance = InstanceType<typeof ApiError>;
const { queryDefaults } = await import('../src/lib/query');

/**
 * The station binding, not the client's behaviour — that is tested once, in
 * `packages/api-client`, for both surfaces.
 *
 * What is worth asserting here is the station-specific half: that the generated client is
 * reachable from mobile code at all (criterion 3), and that the query defaults keep the
 * one rule a clinic cannot afford to lose.
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

describe('the typed client on the station', () => {
  it('is bound to the API origin this build was compiled with', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(respond({ status: 'ok', service: 'api', version: '0.1.0-dev' }));
    vi.stubGlobal('fetch', fetchMock);

    await api.GET('/healthz');

    const request = fetchMock.mock.calls[0]?.[0] as Request;
    expect(request.url).toBe(`${API_BASE_URL}/healthz`);
  });

  it('throws the shared error classes, so a screen can tell the two apart', async () => {
    // An ApiError means the clinic server answered and refused; a NetworkError means the
    // request never left the tablet. On a station those are different sentences on screen
    // and different things for the operator to do.
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Network request failed')));
    await expect(apiFetch('/healthz', { schema: z.object({}) })).rejects.toBeInstanceOf(
      NetworkError,
    );

    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        respond(
          {
            error: {
              code: 'NOT_FOUND',
              kind: 'not_found',
              message: 'Not found.',
              message_bn: 'পাওয়া যায়নি।',
            },
          },
          { status: 404 },
        ),
      ),
    );
    const error = await apiFetch('/patients/1', { schema: z.object({}) }).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiErrorInstance).messageBN).toBe('পাওয়া যায়নি।');
  });
});

describe('the station query defaults', () => {
  it('never retries a mutation', () => {
    // The rule that matters most offline. A retried write is how one recorded
    // observation becomes two rows in an append-only ledger, and the ledger is
    // append-only — nobody can quietly tidy that up afterwards.
    const retry = queryDefaults.mutations?.retry;
    expect(typeof retry === 'function' ? retry(0, new Error('x')) : retry).toBe(false);
  });

  it('holds cached reads far longer than the web does', () => {
    // A station tablet is offline for stretches of a session by design (ADR-0004).
    // Re-fetching on every screen change spends a connection that may not be there.
    expect(queryDefaults.queries?.staleTime).toBe(5 * 60_000);
  });

  it('refetches on reconnect but not on focus', () => {
    // Reconnecting is the moment a station has been waiting for. Focus is not: on
    // Android, returning from the camera or a permission dialog fires it.
    expect(queryDefaults.queries?.refetchOnReconnect).toBe(true);
    expect(queryDefaults.queries?.refetchOnWindowFocus).toBe(false);
  });
});
