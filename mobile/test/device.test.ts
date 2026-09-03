import { randomBytes as nodeRandomBytes } from 'node:crypto';

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { DEVICE_HEADERS, publicKeyOf, verifyCanonical } from '../src/lib/device-signing';

/**
 * This device's identity: where the key lives, what enrolment sends, what a signed
 * request carries, and that a refused enrolment leaves nothing behind.
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

const { deviceAuthorizer, deviceIdentity, enrolDevice, forgetDevice, isEnrolled, signRequest } =
  await import('../src/lib/device');

const BASE = 'http://api.test';
const randomBytes = (n: number) => new Uint8Array(nodeRandomBytes(n));

const deviceView = {
  id: '5c1d2f9e-0a41-4b8c-9d3e-2f6a7b8c9d0e',
  name: 'Anthropometry tablet 1',
  kind: 'tablet' as const,
  status: 'active' as const,
  enrolled_at: '2026-09-03T09:00:00Z',
  model: 'Samsung',
  os_version: 'android 13',
  app_version: '1.2.0',
  last_seen_at: '2026-09-03T09:00:00Z',
  status_changed_at: '2026-09-03T09:00:00Z',
  status_reason: '',
  created_at: '2026-09-03T08:55:00Z',
};

beforeEach(async () => {
  keystore.clear();
  await forgetDevice();
});

afterEach(() => {
  vi.restoreAllMocks();
});

async function enrolOK() {
  let sentKey = '';
  const fetch = vi.fn(async (request: Request) => {
    const body = (await request.json()) as { code: string; public_key: string };
    sentKey = body.public_key;
    expect(body.code).toBe('K7Q2M-9XWRD');
    expect(request.headers.get('X-Requested-With')).toBe('DTHCMS');
    return Response.json({ device: deviceView, key_id: 'k1' });
  });
  const result = await enrolDevice({
    baseUrl: BASE,
    code: ' K7Q2M-9XWRD ',
    model: 'Samsung',
    osVersion: 'android 13',
    appVersion: '1.2.0',
    fetch: fetch as unknown as typeof globalThis.fetch,
    randomBytes,
  });
  return { result, sentKey };
}

describe('enrolment', () => {
  it('sends only the public key, and keeps the seed in the Keystore', async () => {
    const { result, sentKey } = await enrolOK();
    expect(result.kind).toBe('enrolled');
    expect(await isEnrolled()).toBe(true);
    expect(await deviceIdentity()).toEqual({ id: deviceView.id, name: deviceView.name });

    const stored = JSON.parse(keystore.get('dthcms.device-key')!) as { seed: string };
    expect(stored.seed).toMatch(/^[0-9a-f]{64}$/);
    // The key that left is the public half of the seed that stayed.
    const pub = publicKeyOf(Uint8Array.from(Buffer.from(stored.seed, 'hex')));
    expect(Buffer.from(pub).toString('base64')).toBe(sentKey);
    expect(keystore.size).toBe(1);
  });

  it('keeps nothing when the code is refused', async () => {
    const fetch = vi.fn(async () => new Response('{"error":{}}', { status: 401 }));
    const result = await enrolDevice({
      baseUrl: BASE,
      code: 'AAAAA-AAAAA',
      model: '',
      osVersion: '',
      appVersion: '',
      fetch: fetch as unknown as typeof globalThis.fetch,
      randomBytes,
    });
    expect(result).toEqual({ kind: 'refused', status: 401 });
    expect(await isEnrolled()).toBe(false);
    expect(keystore.size).toBe(0);
  });

  it('reports offline without touching the Keystore', async () => {
    const fetch = vi.fn(async () => {
      throw new TypeError('Network request failed');
    });
    const result = await enrolDevice({
      baseUrl: BASE,
      code: 'AAAAA-AAAAA',
      model: '',
      osVersion: '',
      appVersion: '',
      fetch: fetch as unknown as typeof globalThis.fetch,
      randomBytes,
    });
    expect(result).toEqual({ kind: 'offline' });
    expect(keystore.size).toBe(0);
  });

  it('can be forgotten', async () => {
    await enrolOK();
    await forgetDevice();
    expect(await isEnrolled()).toBe(false);
    expect(keystore.size).toBe(0);
  });
});

describe('signing requests', () => {
  it('adds nothing before enrolment', async () => {
    expect(await signRequest('GET', `${BASE}/v1/auth/me`, null, '1.0.0', randomBytes)).toEqual({});
    const authorize = deviceAuthorizer('1.0.0');
    const request = new Request(`${BASE}/v1/auth/me`);
    expect(await authorize(request)).toBe(request);
  });

  it('signs the method, path and body once enrolled', async () => {
    await enrolOK();
    const headers = await signRequest(
      'POST',
      `${BASE}/v1/observations?x=1`,
      '{"value":72}',
      '1.2.0',
      randomBytes,
    );
    expect(headers[DEVICE_HEADERS.id]).toBe(deviceView.id);
    expect(headers[DEVICE_HEADERS.appVersion]).toBe('1.2.0');

    const stored = JSON.parse(keystore.get('dthcms.device-key')!) as { seed: string };
    const pub = publicKeyOf(Uint8Array.from(Buffer.from(stored.seed, 'hex')));
    const sig = Uint8Array.from(Buffer.from(headers[DEVICE_HEADERS.signature]!, 'base64'));
    expect(
      verifyCanonical(pub, sig, {
        method: 'POST',
        path: '/v1/observations', // the query string is not part of the signature
        timestamp: Number(headers[DEVICE_HEADERS.timestamp]),
        nonce: headers[DEVICE_HEADERS.nonce]!,
        body: '{"value":72}',
        deviceId: deviceView.id,
      }),
    ).toBe(true);
  });

  it('the authorizer signs the request it is given and leaves the body readable', async () => {
    await enrolOK();
    const authorize = deviceAuthorizer('1.2.0');
    const original = new Request(`${BASE}/v1/observations`, {
      method: 'POST',
      headers: { Authorization: 'Bearer t', 'Content-Type': 'application/json' },
      body: '{"value":72}',
    });
    const signed = await authorize(original);
    expect(signed.headers.get('Authorization')).toBe('Bearer t');
    expect(signed.headers.get(DEVICE_HEADERS.id)).toBe(deviceView.id);
    expect(await signed.text()).toBe('{"value":72}');

    // Two authorizations of one request get two nonces — the retry after a refresh must
    // not replay the first.
    const again = await authorize(
      new Request(`${BASE}/v1/observations`, { method: 'POST', body: '{"value":72}' }),
    );
    expect(again.headers.get(DEVICE_HEADERS.nonce)).not.toBe(
      signed.headers.get(DEVICE_HEADERS.nonce),
    );
  });
});
