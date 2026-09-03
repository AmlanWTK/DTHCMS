/**
 * UUIDv7 — the client's half of the write contract (CP24).
 *
 * Two identifiers are the client's to generate, and both must be v7:
 *
 *   - `event_id` on every clinical event. It is the identity of the fact, and the ledger
 *     stores an event once however many times it arrives (blueprint §7.5). A station app
 *     that queued a measurement offline keeps the id it generated when the operator
 *     pressed save, and sends that id on every retry for the rest of the week if need be.
 *   - `Idempotency-Key` on a mutating request. One key per *attempt*, held across the
 *     retries of that attempt, so a timeout is answered with the original response rather
 *     than performing the write again.
 *
 * Why v7 rather than v4: the first 48 bits are the Unix millisecond, so ids sort in
 * creation order. That makes them a usable primary key (a B-tree insert lands at the right
 * edge instead of scattering), and it makes an outbox that sorts by id replay in the order
 * the operator worked — which is the order the events must reach the ledger in.
 *
 * RFC 9562 §5.7 layout:
 *
 *   0                   1                   2                   3
 *   |             unix_ts_ms (48 bits)              | ver |  rand_a |
 *   | var |                   rand_b (62 bits)                      |
 */

/** A source of cryptographically strong random bytes. */
export type RandomBytes = (length: number) => Uint8Array;

/**
 * The platform's random source.
 *
 * `globalThis.crypto` exists in browsers, in Node 19+, and in React Native once
 * `react-native-get-random-values` (or Expo's crypto polyfill) has been imported. When it
 * is missing, this throws rather than falling back to `Math.random`: a weak `event_id` is
 * a collision in a clinical ledger, and a silent downgrade is how that happens.
 */
export const platformRandomBytes: RandomBytes = (length) => {
  const source = globalThis.crypto;
  if (!source?.getRandomValues) {
    throw new Error(
      'No cryptographic random source. On React Native, import "react-native-get-random-values" ' +
        'once at the top of the entry file; the app must not generate identifiers without one.',
    );
  }
  return source.getRandomValues(new Uint8Array(length));
};

export interface UuidV7Options {
  /** Milliseconds since the epoch. Defaults to now; injected by tests. */
  now?: number;
  /** Random source. Defaults to the platform's; injected by tests. */
  random?: RandomBytes;
}

const HEX = Array.from({ length: 256 }, (_, byte) => byte.toString(16).padStart(2, '0'));

/**
 * Generates a UUIDv7.
 *
 * The timestamp is truncated to milliseconds, so two ids made in the same millisecond
 * differ only in their 74 random bits. That is enough — a station app generates a handful
 * of ids a minute — and it keeps the function pure and cheap. Monotonicity within a
 * millisecond (RFC 9562 §6.2 method 1) is deliberately not implemented: it would need
 * state, and nothing here depends on ordering finer than a millisecond.
 */
export function uuidv7(options: UuidV7Options = {}): string {
  const now = options.now ?? Date.now();
  const random = (options.random ?? platformRandomBytes)(10);
  if (random.length < 10) {
    throw new Error('the random source returned too few bytes');
  }

  const bytes = new Uint8Array(16);
  // 48 bits of Unix milliseconds, big-endian. Number is exact to 2^53, so a millisecond
  // timestamp is safe until the year 10889 — well past the 48-bit field's own limit.
  const timestamp = Math.floor(now);
  bytes[0] = (timestamp / 2 ** 40) & 0xff;
  bytes[1] = (timestamp / 2 ** 32) & 0xff;
  bytes[2] = (timestamp / 2 ** 24) & 0xff;
  bytes[3] = (timestamp / 2 ** 16) & 0xff;
  bytes[4] = (timestamp / 2 ** 8) & 0xff;
  bytes[5] = timestamp & 0xff;

  bytes.set(random, 6);
  // Version 7 in the high nibble of byte 6; variant 10 in the top bits of byte 8.
  bytes[6] = (bytes[6]! & 0x0f) | 0x70;
  bytes[8] = (bytes[8]! & 0x3f) | 0x80;

  const hex = Array.from(bytes, (byte) => HEX[byte]!);
  return (
    hex.slice(0, 4).join('') +
    '-' +
    hex.slice(4, 6).join('') +
    '-' +
    hex.slice(6, 8).join('') +
    '-' +
    hex.slice(8, 10).join('') +
    '-' +
    hex.slice(10, 16).join('')
  );
}

const UUID_V7 = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

/** Whether a string is a well-formed UUIDv7. The server checks the same thing. */
export function isUuidV7(value: string): boolean {
  return UUID_V7.test(value);
}

/**
 * The millisecond a UUIDv7 was created, or null if it is not one.
 *
 * The outbox uses this to show an operator how long a queued event has been waiting
 * without keeping a second timestamp beside it.
 */
export function uuidV7Timestamp(value: string): number | null {
  if (!isUuidV7(value)) return null;
  return Number.parseInt(value.slice(0, 8) + value.slice(9, 13), 16);
}

/**
 * A key for one attempt at a mutating request.
 *
 * Named apart from `uuidv7` because the two have different lifetimes and mixing them up is
 * the mistake this contract exists to prevent: an `event_id` is generated once and kept
 * for the life of the fact, while a key is generated once per attempt and re-sent only for
 * the retries of *that* attempt. Sending a fresh key on a retry defeats the middleware;
 * re-using one across different requests is refused with 409.
 */
export function idempotencyKey(options: UuidV7Options = {}): string {
  return uuidv7(options);
}
