import { describe, expect, it, vi } from 'vitest';

import { idempotencyKey, isUuidV7, uuidV7Timestamp, uuidv7 } from '../src/ids';

/**
 * The client's half of the write contract (CP24). What matters is not that these look
 * like UUIDs — it is that they are v7, that they sort by creation time, and that the
 * random half is actually random.
 */

const fixedRandom = (fill: number) => () => new Uint8Array(10).fill(fill);

describe('uuidv7', () => {
  it('has the version and variant RFC 9562 requires', () => {
    for (let i = 0; i < 200; i += 1) {
      const id = uuidv7();
      expect(isUuidV7(id), id).toBe(true);
      expect(id[14], `version nibble of ${id}`).toBe('7');
      expect('89ab', `variant nibble of ${id}`).toContain(id[19]);
    }
  });

  it('puts the millisecond in the first 48 bits', () => {
    const now = Date.UTC(2026, 8, 3, 4, 42, 0, 123);
    const id = uuidv7({ now, random: fixedRandom(0xab) });
    expect(uuidV7Timestamp(id)).toBe(now);
    expect(
      id.startsWith(
        now
          .toString(16)
          .padStart(12, '0')
          .replace(/^(.{8})(.{4})$/, '$1-$2'),
      ),
    ).toBe(true);
  });

  it('sorts in creation order, which is what makes an outbox replay correctly', () => {
    const made = [0, 1, 2, 5, 1000, 86_400_000].map((offset) =>
      uuidv7({ now: Date.UTC(2026, 8, 3) + offset, random: fixedRandom(0x11) }),
    );
    expect([...made].sort()).toEqual(made);
  });

  it('is unique across a burst in one millisecond', () => {
    const now = Date.UTC(2026, 8, 3, 4, 42);
    const ids = new Set(Array.from({ length: 5000 }, () => uuidv7({ now })));
    expect(ids.size).toBe(5000);
  });

  it('refuses to invent randomness when the platform has none', () => {
    const original = globalThis.crypto;
    // React Native before the polyfill is imported: the app must fail loudly, not fall
    // back to Math.random and mint colliding event ids.
    Object.defineProperty(globalThis, 'crypto', { value: undefined, configurable: true });
    try {
      expect(() => uuidv7()).toThrow(/react-native-get-random-values/);
    } finally {
      Object.defineProperty(globalThis, 'crypto', { value: original, configurable: true });
    }
  });

  it('uses the platform random source once per id', () => {
    const random = vi.fn(() => new Uint8Array(10).fill(3));
    uuidv7({ random });
    expect(random).toHaveBeenCalledTimes(1);
    expect(random).toHaveBeenCalledWith(10);
  });
});

describe('isUuidV7', () => {
  it('rejects a v4, which is what a client that ignored the contract would send', () => {
    expect(isUuidV7('f47ac10b-58cc-4372-a567-0e02b2c3d479')).toBe(false);
    expect(isUuidV7('0190a8f2-0000-7000-8000-0000000000e1')).toBe(true);
    expect(isUuidV7('0190A8F2-0000-7000-8000-0000000000E1')).toBe(false);
    expect(isUuidV7('not a uuid')).toBe(false);
    expect(uuidV7Timestamp('f47ac10b-58cc-4372-a567-0e02b2c3d479')).toBeNull();
  });
});

describe('idempotencyKey', () => {
  it('is a fresh v7 each time it is called', () => {
    const first = idempotencyKey();
    const second = idempotencyKey();
    expect(isUuidV7(first)).toBe(true);
    expect(first).not.toBe(second);
  });
});
