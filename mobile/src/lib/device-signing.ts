import * as ed from '@noble/ed25519';
import { sha256 } from '@noble/hashes/sha256';
import { sha512 } from '@noble/hashes/sha512';
import { bytesToHex } from '@noble/hashes/utils';

/**
 * How the station app proves a request is its own (CP18, D-46).
 *
 * The mirror of `backend/internal/auth/devicesig`. The device holds a 32-byte Ed25519 seed
 * that never leaves the Keystore; every request carries four headers, and the signature
 * is over
 *
 *     METHOD "\n" path "\n" timestamp "\n" nonce "\n" hex(sha256(body)) "\n" device-id
 *
 * This module is pure: bytes in, headers out. No storage, no fetch, no Expo. The vector in
 * `test/device-signing.test.ts` is the same one the Go test asserts; if the two ever
 * disagree, one side has drifted and every signed request would be refused.
 *
 * `@noble/ed25519` is audited, dependency-free, and runs in Hermes without a native
 * module. It needs a SHA-512 to derive keys, which `@noble/hashes` supplies.
 */

ed.etc.sha512Sync = (...messages) => sha512(ed.etc.concatBytes(...messages));

export const DEVICE_HEADERS = {
  id: 'X-Device-Id',
  timestamp: 'X-Device-Timestamp',
  nonce: 'X-Device-Nonce',
  signature: 'X-Device-Signature',
  appVersion: 'X-Device-App-Version',
} as const;

/** The number of random bytes in a nonce. The server refuses anything shorter. */
export const NONCE_LENGTH = 16;

export interface SigningInput {
  method: string;
  /** Path only — no origin, no query string. */
  path: string;
  /** Seconds since the epoch. */
  timestamp: number;
  nonce: string;
  /** The exact bytes that will be sent, or nothing. */
  body: Uint8Array | string | null | undefined;
  deviceId: string;
}

const encoder = new TextEncoder();

function bodyBytes(body: SigningInput['body']): Uint8Array {
  if (body == null) return new Uint8Array();
  return typeof body === 'string' ? encoder.encode(body) : body;
}

/** The string that is signed. Exported for the vector test. */
export function canonical(input: SigningInput): string {
  const digest = bytesToHex(sha256(bodyBytes(input.body)));
  return [
    input.method.toUpperCase(),
    input.path,
    String(input.timestamp),
    input.nonce,
    digest,
    input.deviceId,
  ].join('\n');
}

/** The public key for a seed, 32 bytes. */
export function publicKeyOf(seed: Uint8Array): Uint8Array {
  return ed.getPublicKey(seed);
}

/** Sign the canonical string with the seed. 64 bytes. */
export function signCanonical(seed: Uint8Array, input: SigningInput): Uint8Array {
  return ed.sign(encoder.encode(canonical(input)), seed);
}

/** Verify — used by the tests to prove the round trip, never by the app. */
export function verifyCanonical(pub: Uint8Array, sig: Uint8Array, input: SigningInput): boolean {
  return ed.verify(sig, encoder.encode(canonical(input)), pub);
}

/**
 * The headers for one request. `randomBytes` is injected so this stays pure — the app
 * passes `expo-crypto`'s, the tests pass Node's.
 */
export function signedHeaders(
  seed: Uint8Array,
  input: Omit<SigningInput, 'nonce' | 'timestamp'>,
  options: { now?: () => number; randomBytes: (n: number) => Uint8Array; appVersion?: string },
): Record<string, string> {
  const timestamp = Math.floor((options.now ?? Date.now)() / 1000);
  const nonce = base64url(options.randomBytes(NONCE_LENGTH));
  const signature = signCanonical(seed, { ...input, timestamp, nonce });
  const headers: Record<string, string> = {
    [DEVICE_HEADERS.id]: input.deviceId,
    [DEVICE_HEADERS.timestamp]: String(timestamp),
    [DEVICE_HEADERS.nonce]: nonce,
    [DEVICE_HEADERS.signature]: base64(signature),
  };
  if (options.appVersion) headers[DEVICE_HEADERS.appVersion] = options.appVersion;
  return headers;
}

// --- encodings ---
//
// Hand-rolled rather than `btoa`, which Hermes has but which takes a "binary string" —
// an easy place to corrupt bytes above 0x7f without noticing.

const ALPHABET = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';

export function base64(bytes: Uint8Array): string {
  let out = '';
  for (let i = 0; i < bytes.length; i += 3) {
    const a = bytes[i]!;
    const b = i + 1 < bytes.length ? bytes[i + 1]! : 0;
    const c = i + 2 < bytes.length ? bytes[i + 2]! : 0;
    const triple = (a << 16) | (b << 8) | c;
    out += ALPHABET[(triple >> 18) & 63];
    out += ALPHABET[(triple >> 12) & 63];
    out += i + 1 < bytes.length ? ALPHABET[(triple >> 6) & 63] : '=';
    out += i + 2 < bytes.length ? ALPHABET[triple & 63] : '=';
  }
  return out;
}

/** base64url without padding — the nonce encoding. */
export function base64url(bytes: Uint8Array): string {
  return base64(bytes).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

export function hexToBytes(hex: string): Uint8Array {
  if (hex.length % 2 !== 0) throw new Error('hex string has odd length');
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) {
    const byte = Number.parseInt(hex.slice(i * 2, i * 2 + 2), 16);
    if (Number.isNaN(byte)) throw new Error('hex string has a non-hex character');
    out[i] = byte;
  }
  return out;
}

export { bytesToHex };
