import { randomBytes as nodeRandomBytes } from 'node:crypto';

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { DEVICE_HEADERS } from '../src/lib/device-signing';

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

const { enrolDevice, forgetDevice } = await import('../src/lib/device');
const { connectionAction, handshakeHeaders, realtimeUrl } =
  await import('../src/lib/realtime-handshake');

const randomBytes = (n: number) => new Uint8Array(nodeRandomBytes(n));

async function enrolThisTablet() {
  const fetch = vi.fn(async () =>
    Response.json({
      device: {
        id: '0190a8f2-0000-7000-8000-000000000002',
        name: 'Tablet 7',
        kind: 'tablet',
        status: 'active',
        enrolled_at: '2026-09-03T09:00:00Z',
        model: 'Samsung',
        os_version: 'android 13',
        app_version: '1.2.0',
        last_seen_at: '2026-09-03T09:00:00Z',
        status_changed_at: '2026-09-03T09:00:00Z',
        status_reason: '',
        created_at: '2026-09-03T08:55:00Z',
      },
      key_id: 'k1',
    }),
  );
  await enrolDevice({
    baseUrl: 'http://api.test',
    code: 'K7Q2M-9XWRD',
    model: 'Samsung',
    osVersion: 'android 13',
    appVersion: '1.2.0',
    fetch: fetch as unknown as typeof globalThis.fetch,
    randomBytes,
  });
}

/**
 * The station app's realtime handshake (CP27).
 *
 * What is asserted is the part that is a decision rather than plumbing: which credential
 * goes on the wire, and what the app does when it is put down and picked up again. The
 * provider that wires these into React is not tested here for the reason
 * `vitest.config.mts` records — rendering React Native in a Node environment proves
 * nothing a device would not disprove.
 */

describe('realtimeUrl', () => {
  it('follows the API, and upgrades the scheme with it', () => {
    expect(realtimeUrl('http://192.168.1.10:8080')).toBe('ws://192.168.1.10:8080/v1/realtime');
    expect(realtimeUrl('https://clinic.example')).toBe('wss://clinic.example/v1/realtime');
    expect(realtimeUrl('https://clinic.example/')).toBe('wss://clinic.example/v1/realtime');
  });
});

describe('handshakeHeaders', () => {
  beforeEach(async () => {
    keystore.clear();
    await forgetDevice();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('carries the bearer token, because React Native can put it in a header', async () => {
    const headers = await handshakeHeaders('ws://clinic.test/v1/realtime', 'tok_abc');
    expect(headers.Authorization).toBe('Bearer tok_abc');
  });

  it('carries no credential at all when there is no session', async () => {
    const headers = await handshakeHeaders('ws://clinic.test/v1/realtime', null);
    expect(headers.Authorization).toBeUndefined();
  });

  it('signs the handshake with the device key, as it signs every other request', async () => {
    await enrolThisTablet();

    const headers = await handshakeHeaders('ws://clinic.test/v1/realtime', 'tok_abc');
    expect(headers[DEVICE_HEADERS.id]).toBe('0190a8f2-0000-7000-8000-000000000002');
    expect(headers[DEVICE_HEADERS.signature]).toBeTruthy();
    expect(headers[DEVICE_HEADERS.nonce]).toBeTruthy();
    expect(headers[DEVICE_HEADERS.timestamp]).toMatch(/^\d+$/);
  });

  it('mints a fresh nonce each time, so a reconnect is not refused as a replay', async () => {
    await enrolThisTablet();

    const first = await handshakeHeaders('ws://clinic.test/v1/realtime', 'tok_abc');
    const second = await handshakeHeaders('ws://clinic.test/v1/realtime', 'tok_abc');
    expect(first[DEVICE_HEADERS.nonce]).not.toBe(second[DEVICE_HEADERS.nonce]);
    expect(first[DEVICE_HEADERS.signature]).not.toBe(second[DEVICE_HEADERS.signature]);
  });

  it('never puts a credential in the URL', async () => {
    const url = realtimeUrl('https://clinic.example');
    expect(url).not.toMatch(/token|Bearer|access/i);
    expect(url).not.toContain('?');
  });
});

describe('the background/foreground policy', () => {
  // A tablet in a drawer should hold nothing open; one taken back out should be live
  // before the operator has finished unlocking it.
  it('closes the socket when the app goes to the background', () => {
    expect(connectionAction('active', 'background')).toBe('disconnect');
    expect(connectionAction('inactive', 'background')).toBe('disconnect');
  });

  it('reconnects at once when the app comes back', () => {
    expect(connectionAction('background', 'active')).toBe('resume');
  });

  // On iOS `inactive` is a notification banner, an incoming call, the app switcher.
  // Tearing the connection down for a banner would mean reconnecting several times a
  // minute on a busy phone.
  it('does nothing for the transient inactive state', () => {
    expect(connectionAction('active', 'inactive')).toBe('none');
    expect(connectionAction('inactive', 'active')).toBe('none');
  });

  it('does nothing when nothing changed', () => {
    expect(connectionAction('active', 'active')).toBe('none');
    expect(connectionAction('background', 'background')).toBe('none');
  });
});

describe('the client is the shared one', () => {
  it('uses @dthcms/api-client rather than a second implementation', async () => {
    // Two realtime clients would drift, and the symptom is "the tablet updates and the
    // dashboard does not" — a day to diagnose.
    // The path is built rather than passed as a URL: React Native's type surface narrows
    // the DOM URL enough that `readFileSync` no longer accepts one.
    const { readFileSync } = await import('node:fs');
    const { dirname, join } = await import('node:path');
    const here = dirname(import.meta.url.replace('file://', ''));
    const source = readFileSync(join(here, '..', 'src', 'lib', 'realtime.tsx'), 'utf8');
    expect(source).toContain("from '@dthcms/api-client'");
    expect(source).toContain('createRealtimeClient');
    // And it invalidates rather than writing into the cache.
    expect(source).toContain('invalidateQueries');
    expect(source).not.toContain('setQueryData');
  });
});
