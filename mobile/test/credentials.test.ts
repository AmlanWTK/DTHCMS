import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

/**
 * Where each credential lives, and what happens to it when the server says no.
 *
 * The properties here are the ones CP11's acceptance criterion 4 and ADR-0010 rest on for
 * the station app: the refresh token touches the Keystore and nothing else; the access
 * token touches nothing at all; a refused refresh token is discarded; an unreachable server
 * discards nothing.
 */

const keystore = new Map<string, string>();
vi.mock('expo-secure-store', () => ({
  setItemAsync: vi.fn(async (key: string, value: string) => {
    keystore.set(key, value);
  }),
  getItemAsync: vi.fn(async (key: string) => keystore.get(key) ?? null),
  deleteItemAsync: vi.fn(async (key: string) => {
    keystore.delete(key);
  }),
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'WHEN_UNLOCKED_THIS_DEVICE_ONLY',
}));

const {
  forgetCredentials,
  getAccessToken,
  hasStoredRefreshToken,
  keepCredentials,
  refreshCredentials,
} = await import('../src/lib/credentials');

const BASE = 'http://api.test';
const issued = (access: string, refresh?: string) => ({
  access_token: access,
  expires_at: '2026-09-03T09:15:00Z',
  user: {
    id: 'u1',
    employee_code: 'E001',
    name_en: 'Dr Test',
    name_bn: 'ডা. পরীক্ষা',
    status: 'active' as const,
    roles: ['PHYSICIAN'],
    permissions: [],
    grants: [],
    second_factor: { required: true, enrolled: false, pending: false, recovery_codes_left: 0 },
  },
  ...(refresh ? { refresh_token: refresh, refresh_expires_at: '2026-09-17T09:00:00Z' } : {}),
});

beforeEach(async () => {
  keystore.clear();
  await forgetCredentials();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('keeping credentials', () => {
  it('puts the refresh token in the Keystore and the access token nowhere', async () => {
    await keepCredentials(issued('access-1', 'refresh-1'));

    expect(getAccessToken()).toBe('access-1');
    expect([...keystore.entries()]).toEqual([['dthcms.refresh-token', 'refresh-1']]);
    // Not in the keystore under any name, and not in any other store this module has.
    expect([...keystore.values()]).not.toContain('access-1');
  });

  it('keeps an existing refresh token when a response carries none', async () => {
    await keepCredentials(issued('a', 'r'));
    await keepCredentials(issued('b'));
    expect(getAccessToken()).toBe('b');
    expect(keystore.get('dthcms.refresh-token')).toBe('r');
  });

  it('forgets both', async () => {
    await keepCredentials(issued('a', 'r'));
    await forgetCredentials();
    expect(getAccessToken()).toBeNull();
    expect(await hasStoredRefreshToken()).toBe(false);
  });
});

describe('refreshing', () => {
  it('exchanges the stored token by body, with the guard header and no cookies', async () => {
    await keepCredentials(issued('old-access', 'old-refresh'));
    const fetch = vi.fn(
      async () =>
        new Response(JSON.stringify(issued('new-access', 'new-refresh')), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
    );

    expect(await refreshCredentials({ baseUrl: BASE, fetch })).toBe(true);

    const request = (fetch.mock.calls as unknown as [Request][])[0]![0];
    expect(request.url).toBe(`${BASE}/v1/auth/refresh`);
    expect(request.method).toBe('POST');
    expect(request.credentials).toBe('omit');
    expect(request.headers.get('X-Requested-With')).toBe('DTHCMS');
    expect(await request.json()).toEqual({ refresh_token: 'old-refresh' });

    expect(getAccessToken()).toBe('new-access');
    expect(keystore.get('dthcms.refresh-token')).toBe('new-refresh');
  });

  it('does nothing without a stored token', async () => {
    const fetch = vi.fn();
    expect(await refreshCredentials({ baseUrl: BASE, fetch })).toBe(false);
    expect(fetch).not.toHaveBeenCalled();
  });

  it('discards a refused token — presenting it again would look like a replay', async () => {
    await keepCredentials(issued('a', 'spent'));
    const fetch = vi.fn(async () => new Response('{}', { status: 401 }));

    expect(await refreshCredentials({ baseUrl: BASE, fetch })).toBe(false);

    expect(await hasStoredRefreshToken()).toBe(false);
    expect(getAccessToken()).toBeNull();
  });

  it('keeps the token when the server is merely unreachable', async () => {
    // A tablet in the corridor without signal is not signed out.
    await keepCredentials(issued('a', 'good'));
    const fetch = vi.fn(async () => {
      throw new TypeError('Network request failed');
    });

    expect(await refreshCredentials({ baseUrl: BASE, fetch })).toBe(false);

    expect(keystore.get('dthcms.refresh-token')).toBe('good');
    expect(getAccessToken()).toBe('a');
  });

  it('keeps the token on a server error, which is not a refusal', async () => {
    await keepCredentials(issued('a', 'good'));
    const fetch = vi.fn(async () => new Response('', { status: 503 }));
    expect(await refreshCredentials({ baseUrl: BASE, fetch })).toBe(false);
    expect(keystore.get('dthcms.refresh-token')).toBe('good');
  });
});
