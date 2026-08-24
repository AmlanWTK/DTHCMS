import * as SecureStore from 'expo-secure-store';

/**
 * Secure storage, allowlisted.
 *
 * CP11 acceptance criterion 4 is "nothing sensitive is stored outside secure storage".
 * A wrapper cannot stop somebody importing AsyncStorage — a lint rule does that — but it
 * can enforce the inverse discipline: every key that touches the Keystore is declared
 * here, with what it holds and why it is sensitive, so a reviewer can read the complete
 * inventory of secrets in one screenful.
 *
 * An unknown key throws. The failure mode this prevents is quiet growth: a key added in
 * a hurry, holding something nobody classified, discovered in an audit two years on.
 */

import { resolveSecureKey, type SecureKeyName } from '@/lib/secure-keys';

export { SECURE_KEYS, resolveSecureKey, type SecureKeyName } from '@/lib/secure-keys';

export async function getSecureItem(name: SecureKeyName): Promise<string | null> {
  return SecureStore.getItemAsync(resolveSecureKey(name));
}

export async function setSecureItem(name: SecureKeyName, value: string): Promise<void> {
  await SecureStore.setItemAsync(resolveSecureKey(name), value, {
    // Never synced, never backed up, gone if the device's lock is removed. A clinic
    // phone that loses its screen lock should also lose its session.
    keychainAccessible: SecureStore.WHEN_UNLOCKED_THIS_DEVICE_ONLY,
  });
}

export async function deleteSecureItem(name: SecureKeyName): Promise<void> {
  await SecureStore.deleteItemAsync(resolveSecureKey(name));
}
