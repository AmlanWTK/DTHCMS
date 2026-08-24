/**
 * The secure-storage allowlist, separated from the native module so the rule itself is
 * testable in plain Node. See secure-storage.ts for why the allowlist exists.
 */

export const SECURE_KEYS = {
  /** CP16: the session credential. Never in AsyncStorage, never in a Zustand persist. */
  sessionToken: 'dthcms.session-token',
  /** CP15: the device's enrolment private key reference. */
  deviceKey: 'dthcms.device-key',
} as const;

export type SecureKeyName = keyof typeof SECURE_KEYS;

/** Pure, so the allowlist rule is testable without the native module. */
export function resolveSecureKey(name: string): string {
  const key = (SECURE_KEYS as Record<string, string>)[name];
  if (key === undefined) {
    throw new Error(
      `"${name}" is not a declared secure-storage key. Add it to SECURE_KEYS with a ` +
        `comment saying what it holds and why it is sensitive — or, if it is not ` +
        `sensitive, it does not belong in the Keystore at all.`,
    );
  }
  return key;
}
