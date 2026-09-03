import { describe, expect, it, vi } from 'vitest';

import {
  REQUESTED_WITH_HEADER,
  REQUESTED_WITH_VALUE,
  bearerAuthorizer,
  createApiClient,
  createRefreshingFetch,
  isCredentialEndpoint,
} from '../src';

const BASE = 'http://api.test';

/** A scripted fetch: each call takes the next response, and every request is recorded. */
function scripted(responses: Array<() => Response>) {
  const calls: Request[] = [];
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const request = new Request(input, init);
    calls.push(request);
    const next = responses.shift();
    if (!next) throw new Error(`unexpected request ${request.method} ${request.url}`);
    return next();
  }) as unknown as typeof globalThis.fetch;
  return { fetch, calls };
}

const ok = (body: unknown = {}) =>
  new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
const unauthenticated = () =>
  new Response(JSON.stringify({ error: { code: 'UNAUTHENTICATED' } }), { status: 401 });

describe('the forgery guard header', () => {
  it('is sent on every request the typed client makes', async () => {
    const { fetch, calls } = scripted([() => ok({ status: 'ok' })]);
    const api = createApiClient({ baseUrl: BASE, fetch });

    await api.GET('/healthz');

    expect(calls[0]?.headers.get(REQUESTED_WITH_HEADER)).toBe(REQUESTED_WITH_VALUE);
  });
});

describe('createRefreshingFetch', () => {
  it('passes a successful response straight through', async () => {
    const { fetch, calls } = scripted([() => ok({ id: 'u1' })]);
    const refreshing = createRefreshingFetch({ baseUrl: BASE, fetch });

    const response = await refreshing(`${BASE}/v1/auth/me`);

    expect(response.status).toBe(200);
    expect(calls).toHaveLength(1);
  });

  it('answers a 401 with one refresh and one retry', async () => {
    const { fetch, calls } = scripted([
      () => unauthenticated(), // the original request: token expired
      () => ok(), // the refresh
      () => ok({ id: 'u1' }), // the retry
    ]);
    const onSessionLost = vi.fn();
    const refreshing = createRefreshingFetch({ baseUrl: BASE, fetch, onSessionLost });

    const response = await refreshing(`${BASE}/v1/auth/me`);

    expect(response.status).toBe(200);
    expect(calls.map((c) => `${c.method} ${new URL(c.url).pathname}`)).toEqual([
      'GET /v1/auth/me',
      'POST /v1/auth/refresh',
      'GET /v1/auth/me',
    ]);
    expect(calls[1]?.headers.get(REQUESTED_WITH_HEADER)).toBe(REQUESTED_WITH_VALUE);
    expect(onSessionLost).not.toHaveBeenCalled();
  });

  it('retries with the original body intact', async () => {
    // A Request body can be read once. If the clone were taken after the first send, the
    // retry would post an empty body and the server would answer 400 — a bug that only
    // shows on the second attempt, fifteen minutes into a session.
    const { fetch, calls } = scripted([() => unauthenticated(), () => ok(), () => ok()]);
    const refreshing = createRefreshingFetch({ baseUrl: BASE, fetch });

    await refreshing(`${BASE}/v1/things`, {
      method: 'POST',
      body: JSON.stringify({ hba1c: 7.2 }),
      headers: { 'Content-Type': 'application/json' },
    });

    expect(await calls[2]?.text()).toBe(JSON.stringify({ hba1c: 7.2 }));
  });

  it('shares one refresh between concurrent failures', async () => {
    // The reuse detector treats a second exchange of the same refresh token as theft and
    // revokes the family. Three queries expiring together must therefore produce one
    // refresh, not three.
    let releaseRefresh!: () => void;
    const gate = new Promise<void>((resolve) => {
      releaseRefresh = resolve;
    });

    const calls: Request[] = [];
    const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const request = new Request(input, init);
      calls.push(request);
      const path = new URL(request.url).pathname;
      if (path === '/v1/auth/refresh') {
        await gate;
        return ok();
      }
      // First visit to a path is the expired one; the retry succeeds.
      const earlier = calls.filter((c) => new URL(c.url).pathname === path).length;
      return earlier === 1 ? unauthenticated() : ok();
    }) as unknown as typeof globalThis.fetch;

    const refreshing = createRefreshingFetch({ baseUrl: BASE, fetch });
    const results = Promise.all([
      refreshing(`${BASE}/v1/a`),
      refreshing(`${BASE}/v1/b`),
      refreshing(`${BASE}/v1/c`),
    ]);

    // Let the three originals fail and reach the refresh.
    await vi.waitFor(() => {
      expect(calls.filter((c) => new URL(c.url).pathname === '/v1/auth/refresh')).toHaveLength(1);
    });
    releaseRefresh();

    const responses = await results;
    expect(responses.map((r) => r.status)).toEqual([200, 200, 200]);
    expect(calls.filter((c) => new URL(c.url).pathname === '/v1/auth/refresh')).toHaveLength(1);
  });

  it('gives up, and says so, when the refresh is refused', async () => {
    const { fetch, calls } = scripted([() => unauthenticated(), () => unauthenticated()]);
    const onSessionLost = vi.fn();
    const refreshing = createRefreshingFetch({ baseUrl: BASE, fetch, onSessionLost });

    const response = await refreshing(`${BASE}/v1/auth/me`);

    expect(response.status).toBe(401);
    expect(calls).toHaveLength(2);
    expect(onSessionLost).toHaveBeenCalledTimes(1);
  });

  it('treats a refresh that cannot be reached as a lost session, not a crash', async () => {
    const fetch = vi.fn(async (input: RequestInfo | URL) => {
      const path = new URL(new Request(input).url).pathname;
      if (path === '/v1/auth/refresh') throw new TypeError('Failed to fetch');
      return unauthenticated();
    }) as unknown as typeof globalThis.fetch;
    const onSessionLost = vi.fn();
    const refreshing = createRefreshingFetch({ baseUrl: BASE, fetch, onSessionLost });

    const response = await refreshing(`${BASE}/v1/auth/me`);

    expect(response.status).toBe(401);
    expect(onSessionLost).toHaveBeenCalledTimes(1);
  });

  it('does not refresh around a refused sign-in or a refused refresh', async () => {
    // A wrong password is an answer. Refreshing on it would be wrong; refreshing on a
    // failed refresh would be a loop.
    for (const path of ['/v1/auth/login', '/v1/auth/refresh']) {
      const { fetch, calls } = scripted([() => unauthenticated()]);
      const onSessionLost = vi.fn();
      const refreshing = createRefreshingFetch({ baseUrl: BASE, fetch, onSessionLost });

      const response = await refreshing(`${BASE}${path}`, { method: 'POST' });

      expect(response.status).toBe(401);
      expect(calls).toHaveLength(1);
      expect(onSessionLost).not.toHaveBeenCalled();
    }
  });

  it('leaves other failures alone', async () => {
    const { fetch, calls } = scripted([() => new Response('', { status: 500 })]);
    const refreshing = createRefreshingFetch({ baseUrl: BASE, fetch });

    const response = await refreshing(`${BASE}/v1/auth/me`);

    expect(response.status).toBe(500);
    expect(calls).toHaveLength(1);
  });
});

describe('the bearer transport', () => {
  it('attaches the held token, refreshes through the client, and retries with the new one', async () => {
    let held: string | null = 'old';
    const { fetch, calls } = scripted([() => unauthenticated(), () => ok({ id: 'u1' })]);
    const refresh = vi.fn(async () => {
      held = 'new';
      return true;
    });
    const refreshing = createRefreshingFetch({
      baseUrl: BASE,
      fetch,
      authorize: bearerAuthorizer(() => held),
      refresh,
    });

    const response = await refreshing(`${BASE}/v1/auth/me`);

    expect(response.status).toBe(200);
    expect(refresh).toHaveBeenCalledTimes(1);
    // No cookie-style refresh POST: the client's own refresh ran instead.
    expect(calls.map((c) => c.headers.get('Authorization'))).toEqual(['Bearer old', 'Bearer new']);
  });

  it('sends no Authorization header when no token is held', async () => {
    const { fetch, calls } = scripted([() => ok()]);
    const refreshing = createRefreshingFetch({
      baseUrl: BASE,
      fetch,
      authorize: bearerAuthorizer(() => null),
    });
    await refreshing(`${BASE}/healthz`);
    expect(calls[0]?.headers.has('Authorization')).toBe(false);
  });

  it('treats a refresh that throws as a lost session', async () => {
    const { fetch } = scripted([() => unauthenticated()]);
    const onSessionLost = vi.fn();
    const refreshing = createRefreshingFetch({
      baseUrl: BASE,
      fetch,
      refresh: async () => {
        throw new Error('keystore unavailable');
      },
      onSessionLost,
    });
    const response = await refreshing(`${BASE}/v1/auth/me`);
    expect(response.status).toBe(401);
    expect(onSessionLost).toHaveBeenCalledTimes(1);
  });

  it('lets the station app opt out of cookies', async () => {
    const { fetch, calls } = scripted([() => ok({ status: 'ok' })]);
    const api = createApiClient({ baseUrl: BASE, fetch, credentials: 'omit' });
    await api.GET('/healthz');
    expect(calls[0]?.credentials).toBe('omit');
  });
});

describe('isCredentialEndpoint', () => {
  it('recognises sign-in and refresh, absolute or relative, and nothing else', () => {
    expect(isCredentialEndpoint('http://api.test/v1/auth/login')).toBe(true);
    expect(isCredentialEndpoint('/v1/auth/refresh')).toBe(true);
    expect(isCredentialEndpoint('http://api.test/v1/auth/refresh?x=1')).toBe(true);
    expect(isCredentialEndpoint('http://api.test/v1/auth/me')).toBe(false);
    expect(isCredentialEndpoint('http://api.test/v1/auth/logout')).toBe(false);
    expect(isCredentialEndpoint('http://api.test/v1/patients')).toBe(false);
  });
});
