/**
 * The secure-storage allowlist, separated from the native module so the rule itself is
 * testable in plain Node. See secure-storage.ts for why the allowlist exists.
 */

export const SECURE_KEYS = {
  /**
   * CP16: the refresh token — the one credential that outlives a restart. The access
   * token it buys lives fifteen minutes and is held in memory only, so it is never
   * written anywhere. Never in AsyncStorage, never in a Zustand persist.
   */
  refreshToken: 'dthcms.refresh-token',
  /** CP18: the device's enrolment private key reference. */
  deviceKey: 'dthcms.device-key',
  /**
   * CP33: a registration in progress.
   *
   * In the Keystore rather than AsyncStorage because a half-finished registration holds a
   * patient's name, telephone number and date of birth — the same data the finished record
   * holds, minus the record. AsyncStorage is plain files that a device backup carries off,
   * which is exactly the disclosure the finished record is protected from.
   *
   * One draft at a time, deliberately: a queue of half-registered people is a queue nobody
   * reconciles, and the resume prompt has to name one person to be answerable.
   */
  registrationDraft: 'dthcms.registration-draft',
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
