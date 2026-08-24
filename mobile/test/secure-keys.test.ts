import { describe, expect, it } from 'vitest';

import { SECURE_KEYS, resolveSecureKey } from '../src/lib/secure-keys';

/**
 * The allowlist rule, tested as policy: acceptance criterion 4 says nothing sensitive
 * lives outside secure storage, and this wrapper enforces the inverse — nothing lives
 * in secure storage without being declared and classified.
 */

describe('the secure-storage allowlist', () => {
  it('resolves every declared key', () => {
    for (const name of Object.keys(SECURE_KEYS)) {
      expect(resolveSecureKey(name)).toBe(SECURE_KEYS[name as keyof typeof SECURE_KEYS]);
    }
  });

  it('throws on a key nobody declared', () => {
    // The failure this prevents: a key added in a hurry, holding something nobody
    // classified, discovered in an audit two years on.
    expect(() => resolveSecureKey('user-preferences')).toThrow(/not a declared/);
  });

  it('namespaces every stored key', () => {
    // Keystore entries are per-app but a recognisable prefix makes an audit readable.
    for (const key of Object.values(SECURE_KEYS)) {
      expect(key).toMatch(/^dthcms\./);
    }
  });
});
