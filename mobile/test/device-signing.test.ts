import { randomBytes as nodeRandomBytes } from 'node:crypto';

import { describe, expect, it } from 'vitest';

import {
  DEVICE_HEADERS,
  base64,
  base64url,
  bytesToHex,
  canonical,
  hexToBytes,
  publicKeyOf,
  signCanonical,
  signedHeaders,
  verifyCanonical,
} from '../src/lib/device-signing';

/**
 * The signing scheme, and the vector shared with the server.
 *
 * `backend/internal/auth/devicesig/devicesig_test.go` asserts the same canonical string
 * for the same inputs. If either side changes the format, the other's test fails — which
 * is the point: a station tablet whose signatures the server cannot verify is a tablet
 * that cannot record a single value.
 */

const randomBytes = (n: number) => new Uint8Array(nodeRandomBytes(n));

describe('the canonical string', () => {
  it('matches the server-side vector', () => {
    const got = canonical({
      method: 'post',
      path: '/v1/x',
      timestamp: 1_700_000_000,
      nonce: 'AAAAAAAAAAAAAAAAAAAAAA',
      body: '{"a":1}',
      deviceId: 'dev-1',
    });
    expect(got).toBe(
      'POST\n/v1/x\n1700000000\nAAAAAAAAAAAAAAAAAAAAAA\n' +
        '015abd7f5cc57a2dd94b7590f04ad8084273905ee33ec5cebeae62276a97f862\ndev-1',
    );
  });

  it('digests an empty body as SHA-256 of nothing', () => {
    const line = canonical({
      method: 'GET',
      path: '/v1/devices/self',
      timestamp: 1,
      nonce: 'n',
      body: null,
      deviceId: 'd',
    }).split('\n')[4];
    expect(line).toBe('e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855');
  });
});

describe('signing', () => {
  const seed = randomBytes(32);
  const pub = publicKeyOf(seed);
  const input = {
    method: 'POST',
    path: '/v1/observations',
    timestamp: 1_756_890_000,
    nonce: base64url(randomBytes(16)),
    body: '{"value":72}',
    deviceId: '5c1d2f9e-0a41-4b8c-9d3e-2f6a7b8c9d0e',
  };

  it('round-trips under the matching public key', () => {
    const sig = signCanonical(seed, input);
    expect(sig).toHaveLength(64);
    expect(verifyCanonical(pub, sig, input)).toBe(true);
  });

  it('is bound to every field', () => {
    const sig = signCanonical(seed, input);
    for (const change of [
      { method: 'PUT' },
      { path: '/v1/other' },
      { timestamp: input.timestamp + 1 },
      { nonce: 'BBBBBBBBBBBBBBBBBBBBBB' },
      { body: '{"value":73}' },
      { deviceId: '7c1d2f9e-0a41-4b8c-9d3e-2f6a7b8c9d0e' },
    ]) {
      expect(verifyCanonical(pub, sig, { ...input, ...change })).toBe(false);
    }
  });

  it('does not verify under another key', () => {
    const other = publicKeyOf(randomBytes(32));
    expect(verifyCanonical(other, signCanonical(seed, input), input)).toBe(false);
  });

  it('produces the four headers, with a fresh nonce each time', () => {
    const now = () => 1_756_890_000_000;
    const a = signedHeaders(seed, input, { now, randomBytes, appVersion: '1.2.0' });
    const b = signedHeaders(seed, input, { now, randomBytes, appVersion: '1.2.0' });

    expect(a[DEVICE_HEADERS.id]).toBe(input.deviceId);
    expect(a[DEVICE_HEADERS.timestamp]).toBe('1756890000');
    expect(a[DEVICE_HEADERS.appVersion]).toBe('1.2.0');
    expect(a[DEVICE_HEADERS.nonce]).toMatch(/^[A-Za-z0-9_-]{22}$/);
    expect(a[DEVICE_HEADERS.nonce]).not.toBe(b[DEVICE_HEADERS.nonce]);
    expect(a[DEVICE_HEADERS.signature]).toMatch(/^[A-Za-z0-9+/]{86}==$/);

    // What the server will compute from those headers verifies.
    const sig = Uint8Array.from(Buffer.from(a[DEVICE_HEADERS.signature]!, 'base64'));
    expect(
      verifyCanonical(pub, sig, {
        ...input,
        timestamp: 1_756_890_000,
        nonce: a[DEVICE_HEADERS.nonce]!,
      }),
    ).toBe(true);
  });
});

describe('encodings', () => {
  it('base64 agrees with Node for every length mod 3', () => {
    for (const n of [0, 1, 2, 3, 4, 31, 32, 33, 64]) {
      const bytes = randomBytes(n);
      expect(base64(bytes)).toBe(Buffer.from(bytes).toString('base64'));
    }
    expect(base64(new Uint8Array([0xff, 0xfe, 0xfd]))).toBe('//79');
    expect(base64url(new Uint8Array([0xff, 0xfe, 0xfd, 0x01]))).toBe('__79AQ');
  });

  it('hex round-trips and refuses garbage', () => {
    const bytes = randomBytes(32);
    expect(hexToBytes(bytesToHex(bytes))).toEqual(bytes);
    expect(() => hexToBytes('abc')).toThrow();
    expect(() => hexToBytes('zz')).toThrow();
  });
});
