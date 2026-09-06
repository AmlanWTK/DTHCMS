import { getRandomBytes } from 'expo-crypto';

import { bytesToHex } from '@/lib/device-signing';
import { deleteSecureItem, getSecureItem, setSecureItem } from '@/lib/secure-storage';

/**
 * The key to the local database (CP64, §13.8).
 *
 * Thirty-two bytes from the device's CSPRNG, hex, in the OS keystore behind the same allowlist
 * every other secret on this device goes through — never in AsyncStorage, never in a Zustand
 * persist, never in a log line. It is generated on the device and leaves it never; there is no
 * escrow, which is a decision with a consequence worth stating: **a tablet whose keystore is
 * cleared cannot read its own database, including any entries it had not yet synced.** That is the
 * same trade the refresh token makes (`docs/staff/devices.md`), and the alternative — a key
 * derivable from something the server holds — would make the encryption a formality.
 *
 * # "Released only after authentication"
 *
 * The plan's phrase, and this module is where it has to be true, because nothing below it can
 * enforce it: the key is read from the keystore only when somebody has signed in, and the store is
 * closed when the session ends. `expo-secure-store` is asked for the item with
 * `WHEN_UNLOCKED_THIS_DEVICE_ONLY`, so a locked or backed-up device cannot produce it at all.
 *
 * # Why sign-out does not delete it
 *
 * Deleting the key at sign-out would be the obvious reading of "wipe local PHI on logout", and it
 * would destroy any entry the tablet had not yet delivered — a morning's measurements, silently,
 * at the moment an operator does the most ordinary thing in the world. So sign-out **locks**: the
 * readable record is wiped (`features/sync`'s `wipeAfterSignOut`), the store is closed, the key
 * leaves memory, and the undelivered queue stays encrypted on disk until the next operator signs
 * in and it syncs. The key is deleted only when there is nothing left to lose, or when somebody
 * asks for the tablet to be wiped outright and is told what that costs.
 */

/** Thirty-two bytes, which is what SQLCipher's key derivation wants and what a CSPRNG gives. */
export const KEY_BYTES = 32;

/**
 * Held here between the moment somebody signs in and the moment the session ends.
 *
 * In memory rather than fetched per statement, because a database open per query would ask the
 * keystore hundreds of times a session and the answer cannot change while the store is open.
 */
let released: string | null = null;

export interface KeyOptions {
  /** Injected for tests; the app uses expo-crypto's. */
  randomBytes?: (n: number) => Uint8Array;
}

/**
 * The key for this device, made on first use.
 *
 * Racing callers get the same key: the read and the write are one `await` apart, and two sign-ins
 * cannot happen at once on a station tablet — but the in-memory copy is checked first anyway,
 * because generating a second key would not fail, it would produce a database nobody can read.
 */
export async function databaseKey(options: KeyOptions = {}): Promise<string> {
  if (released) return released;
  const stored = await getSecureItem('databaseKey');
  if (stored) {
    released = stored;
    return stored;
  }
  const random = options.randomBytes ?? getRandomBytes;
  const key = bytesToHex(random(KEY_BYTES));
  await setSecureItem('databaseKey', key);
  released = key;
  return key;
}

/** Whether the key is in memory — that is, whether somebody has signed in since the app started. */
export function keyIsReleased(): boolean {
  return released !== null;
}

/**
 * Drop the key from memory. The database on disk is unchanged and unreadable until the next
 * sign-in releases it again.
 */
export function lockDatabaseKey(): void {
  released = null;
}

/**
 * Delete the key.
 *
 * Everything the database holds becomes unrecoverable, including anything undelivered — so this is
 * the last step of a deliberate wipe and never a reflex on a failed request. See
 * `features/sync`'s `wipeAfterSignOut` for what is allowed to call it and when.
 */
export async function forgetDatabaseKey(): Promise<void> {
  released = null;
  try {
    await deleteSecureItem('databaseKey');
  } catch {
    // A keystore that refuses to delete is not a reason to keep the key in memory.
  }
}
