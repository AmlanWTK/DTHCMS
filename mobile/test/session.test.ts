import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

/**
 * The station session, against a scripted server.
 *
 * What is asserted is the shape of every request the store makes — bearer header, no
 * cookies, the forgery guard, the transport it asks for — and what ends up where after
 * each answer. None of it is visible on the screen, and all of it is what makes the
 * bearer transport safe.
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
vi.mock('expo-localization', () => ({ getLocales: () => [{ languageCode: 'en' }] }));

const { API_BASE_URL } = await import('../src/lib/api');
const { forgetCredentials, getAccessToken } = await import('../src/lib/credentials');
const { displayName, operatorFromServer, useSession } = await import('../src/stores/session');

const currentUser = {
  id: 'u1',
  employee_code: 'E001',
  name_en: 'Dr Test Physician',
  name_bn: 'ডা. পরীক্ষা চিকিৎসক',
  status: 'active' as const,
  roles: ['PHYSICIAN'],
  permissions: ['patient.read'],
  grants: [{ role: 'PHYSICIAN', permissions: ['patient.read'] }],
  second_factor: { required: true, enrolled: false, pending: false, recovery_codes_left: 0 },
};

const unauthenticated = {
  error: {
    code: 'UNAUTHENTICATED',
    kind: 'auth',
    message: 'Please sign in again.',
    message_bn: 'অনুগ্রহ করে আবার সাইন ইন করুন।',
    correlation_id: 'req_1',
  },
};

function respond(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'req_1' },
  });
}

/** Routes by "METHOD /path" and records every request. Unlisted routes are test bugs. */
function server(routes: Record<string, (request: Request) => Response | Promise<Response>>) {
  const calls: Request[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const request = new Request(input, init);
      calls.push(request);
      const key = `${request.method} ${new URL(request.url).pathname}`;
      const handler = routes[key];
      if (!handler) throw new Error(`no route for ${key}`);
      return handler(request);
    }),
  );
  return calls;
}

const initial = useSession.getInitialState();

beforeEach(async () => {
  keystore.clear();
  await forgetCredentials();
  useSession.setState(initial, true);
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('signing in', () => {
  it('asks for the bearer transport, stores the pair correctly, and sends no cookies', async () => {
    const calls = server({
      'POST /v1/auth/login': () =>
        respond({
          access_token: 'access-1',
          expires_at: '2026-09-03T09:15:00Z',
          refresh_token: 'refresh-1',
          refresh_expires_at: '2026-09-17T09:00:00Z',
          user: currentUser,
        }),
    });

    await useSession.getState().signIn('E001', 'correct horse battery');

    const state = useSession.getState();
    expect(state.status).toBe('authenticated');
    expect(state.operator?.employeeCode).toBe('E001');

    const request = calls[0]!;
    expect(request.url).toBe(`${API_BASE_URL}/v1/auth/login`);
    expect(request.credentials).toBe('omit');
    expect(request.headers.get('X-Requested-With')).toBe('DTHCMS');
    expect(await request.json()).toEqual({
      employee_code: 'E001',
      password: 'correct horse battery',
      transport: 'bearer',
    });

    expect(getAccessToken()).toBe('access-1');
    expect(keystore.get('dthcms.refresh-token')).toBe('refresh-1');
    // Neither token is in the store, which is the thing that could be persisted by mistake.
    expect(JSON.stringify(state)).not.toMatch(/access-1|refresh-1/);
  });

  it('throws the refusal and stores nothing', async () => {
    server({ 'POST /v1/auth/login': () => respond(unauthenticated, 401) });

    await expect(useSession.getState().signIn('E001', 'wrong')).rejects.toMatchObject({
      status: 401,
    });

    expect(useSession.getState().status).toBe('unknown');
    expect(getAccessToken()).toBeNull();
    expect(keystore.size).toBe(0);
  });
});

describe('recovering after a restart', () => {
  it('is anonymous, without a request, when nothing is stored', async () => {
    const calls = server({});
    await useSession.getState().hydrate();
    expect(useSession.getState().status).toBe('anonymous');
    expect(calls).toHaveLength(0);
  });

  it('refreshes with the stored token, then carries the new one', async () => {
    keystore.set('dthcms.refresh-token', 'stored');
    let meCalls = 0;
    const calls = server({
      'GET /v1/auth/me': () =>
        meCalls++ === 0 ? respond(unauthenticated, 401) : respond(currentUser),
      'POST /v1/auth/refresh': () =>
        respond({
          access_token: 'fresh-access',
          expires_at: '',
          refresh_token: 'fresh-refresh',
          refresh_expires_at: '',
          user: currentUser,
        }),
    });

    await useSession.getState().hydrate();

    expect(useSession.getState().status).toBe('authenticated');
    expect(calls.map((c) => `${c.method} ${new URL(c.url).pathname}`)).toEqual([
      'GET /v1/auth/me',
      'POST /v1/auth/refresh',
      'GET /v1/auth/me',
    ]);
    // First call had no token to attach; the refresh posted the stored one by body; the
    // retry carried the fresh one.
    expect(calls[0]?.headers.get('Authorization')).toBeNull();
    expect(await calls[1]?.json()).toEqual({ refresh_token: 'stored' });
    expect(calls[2]?.headers.get('Authorization')).toBe('Bearer fresh-access');
    expect(keystore.get('dthcms.refresh-token')).toBe('fresh-refresh');
  });

  it('forgets a refresh token the server refuses', async () => {
    keystore.set('dthcms.refresh-token', 'expired');
    server({
      'GET /v1/auth/me': () => respond(unauthenticated, 401),
      'POST /v1/auth/refresh': () => respond(unauthenticated, 401),
    });

    await useSession.getState().hydrate();

    expect(useSession.getState().status).toBe('anonymous');
    expect(keystore.size).toBe(0);
  });

  it('keeps the refresh token when the server is unreachable', async () => {
    keystore.set('dthcms.refresh-token', 'good');
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new TypeError('Network request failed');
      }),
    );

    await useSession.getState().hydrate();

    expect(useSession.getState().status).toBe('anonymous');
    expect(keystore.get('dthcms.refresh-token')).toBe('good');
  });
});

describe('signing out', () => {
  it('tells the server with the bearer token, then forgets everything', async () => {
    keystore.set('dthcms.refresh-token', 'r');
    server({
      'POST /v1/auth/login': () =>
        respond({ access_token: 'a', expires_at: '', refresh_token: 'r', user: currentUser }),
      'POST /v1/auth/logout': () => new Response(null, { status: 204 }),
    });
    await useSession.getState().signIn('E001', 'x');
    const calls = server({ 'POST /v1/auth/logout': () => new Response(null, { status: 204 }) });

    await useSession.getState().signOut();

    expect(calls[0]?.headers.get('Authorization')).toBe('Bearer a');
    expect(useSession.getState().status).toBe('anonymous');
    expect(getAccessToken()).toBeNull();
    expect(keystore.size).toBe(0);
  });

  it('forgets everything even when the server cannot be told', async () => {
    keystore.set('dthcms.refresh-token', 'r');
    useSession.setState({ status: 'authenticated', operator: operatorFromServer(currentUser) });
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new TypeError('Network request failed');
      }),
    );

    await useSession.getState().signOut();

    expect(useSession.getState().status).toBe('anonymous');
    expect(keystore.size).toBe(0);
  });
});

describe('the operator', () => {
  it('is named in the interface language, with a fallback', () => {
    const operator = operatorFromServer(currentUser);
    expect(displayName(operator, 'en')).toBe('Dr Test Physician');
    expect(displayName(operator, 'bn')).toBe('ডা. পরীক্ষা চিকিৎসক');
    expect(displayName({ ...operator, nameBN: '' }, 'bn')).toBe('Dr Test Physician');
  });
});

describe('the second step', () => {
  it('returns a challenge for an enrolled account, then completes it with a code', async () => {
    const enrolledUser = {
      ...currentUser,
      second_factor: { required: true, enrolled: true, pending: false, recovery_codes_left: 9 },
    };
    const calls = server({
      'POST /v1/auth/login': () =>
        respond({ challenge: 'ch-1', expires_at: '2026-09-03T09:05:00Z' }, 202),
      'POST /v1/auth/login/second-factor': () =>
        respond({
          access_token: 'a',
          expires_at: '',
          refresh_token: 'r',
          refresh_expires_at: '',
          user: enrolledUser,
        }),
    });

    const result = await useSession.getState().signIn('E001', 'pw');
    expect(result).toEqual({ kind: 'second-factor', challenge: 'ch-1' });
    // No credentials yet — the password alone bought nothing storable.
    expect(getAccessToken()).toBeNull();
    expect(keystore.size).toBe(0);
    expect(useSession.getState().status).toBe('unknown');

    await useSession.getState().completeSecondFactor('ch-1', { code: '123456' });

    expect(useSession.getState().status).toBe('authenticated');
    expect(getAccessToken()).toBe('a');
    expect(keystore.get('dthcms.refresh-token')).toBe('r');
    const completion = calls[1]!;
    expect(completion.credentials).toBe('omit');
    expect(await completion.json()).toEqual({
      challenge: 'ch-1',
      transport: 'bearer',
      code: '123456',
    });
  });

  it('sends a recovery code when that is what was offered', async () => {
    const calls = server({
      'POST /v1/auth/login/second-factor': () =>
        respond({ access_token: 'a', expires_at: '', refresh_token: 'r', user: currentUser }),
    });
    await useSession
      .getState()
      .completeSecondFactor('ch-1', { recoveryCode: 'AAAA-BBBB-CCCC-DDDD' });
    expect(await calls[0]!.json()).toEqual({
      challenge: 'ch-1',
      transport: 'bearer',
      recovery_code: 'AAAA-BBBB-CCCC-DDDD',
    });
  });

  it('throws the refusal and stores nothing on a wrong code', async () => {
    server({ 'POST /v1/auth/login/second-factor': () => respond(unauthenticated, 401) });
    await expect(
      useSession.getState().completeSecondFactor('ch-1', { code: '000000' }),
    ).rejects.toMatchObject({ status: 401 });
    expect(getAccessToken()).toBeNull();
    expect(keystore.size).toBe(0);
  });
});
