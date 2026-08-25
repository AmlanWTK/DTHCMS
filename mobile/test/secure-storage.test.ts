import { beforeEach, describe, expect, it, vi } from 'vitest';

/**
 * The Keystore wrapper.
 *
 * `secure-keys.test.ts` proves the allowlist rule in isolation. This proves the wrapper
 * actually applies it — that the path from a screen calling `setSecureItem` to the native
 * module carries the declared key name and the right accessibility class, and that an
 * undeclared name never reaches the Keystore at all.
 *
 * Worth its own file because this is the code CP11 acceptance criterion 4 rests on, and
 * until now it was the one module in `lib` with no test of any kind.
 */

const setItemAsync = vi.fn(async () => undefined);
const getItemAsync = vi.fn(async () => null as string | null);
const deleteItemAsync = vi.fn(async () => undefined);

vi.mock('expo-secure-store', () => ({
  setItemAsync,
  getItemAsync,
  deleteItemAsync,
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'WHEN_UNLOCKED_THIS_DEVICE_ONLY',
}));

const { deleteSecureItem, getSecureItem, setSecureItem } =
  await import('../src/lib/secure-storage');

beforeEach(() => {
  vi.clearAllMocks();
});

describe('writing', () => {
  it('stores under the declared key, not the name the caller used', () => {
    // The indirection is the point: a screen names an intention, the allowlist decides
    // what string touches the Keystore.
    void setSecureItem('sessionToken', 'abc');
    expect(setItemAsync).toHaveBeenCalledWith('dthcms.session-token', 'abc', expect.anything());
  });

  it('pins the accessibility class to this device, while unlocked', () => {
    /*
     * Not a detail. This is what makes a clinic phone that loses its screen lock also
     * lose its session, and what keeps the token out of an iCloud or Google backup. A
     * silent downgrade to the default class would never fail a test that only checked
     * the value round-trips.
     */
    void setSecureItem('deviceKey', 'key-material');
    expect(setItemAsync).toHaveBeenCalledWith(
      'dthcms.device-key',
      'key-material',
      expect.objectContaining({ keychainAccessible: 'WHEN_UNLOCKED_THIS_DEVICE_ONLY' }),
    );
  });
});

describe('reading and deleting', () => {
  it('reads through the same allowlist', async () => {
    getItemAsync.mockResolvedValueOnce('stored');
    await expect(getSecureItem('sessionToken')).resolves.toBe('stored');
    expect(getItemAsync).toHaveBeenCalledWith('dthcms.session-token');
  });

  it('returns null when nothing is stored, rather than throwing', async () => {
    getItemAsync.mockResolvedValueOnce(null);
    await expect(getSecureItem('sessionToken')).resolves.toBeNull();
  });

  it('deletes through the same allowlist', async () => {
    await deleteSecureItem('deviceKey');
    expect(deleteItemAsync).toHaveBeenCalledWith('dthcms.device-key');
  });
});

describe('an undeclared key', () => {
  it('never reaches the Keystore, on any operation', async () => {
    /*
     * The failure mode this prevents is quiet growth: a key added in a hurry, holding
     * something nobody classified, found in an audit two years on.
     *
     * Note the shape of the failure. These are async functions, so the allowlist rejects
     * rather than throwing synchronously — a caller that does `void setSecureItem(...)`
     * without awaiting gets an unhandled rejection instead of a stack trace at the call
     * site. Every current caller awaits, so this is a sharp edge rather than a defect,
     * but it is the reason these assertions read `rejects` and not `toThrow`.
     */
    const undeclared = 'patientNotes' as never;

    await expect(setSecureItem(undeclared, 'x')).rejects.toThrow(
      /not a declared secure-storage key/,
    );
    await expect(getSecureItem(undeclared)).rejects.toThrow(/not a declared secure-storage key/);
    await expect(deleteSecureItem(undeclared)).rejects.toThrow(/not a declared secure-storage key/);

    expect(setItemAsync).not.toHaveBeenCalled();
    expect(getItemAsync).not.toHaveBeenCalled();
    expect(deleteItemAsync).not.toHaveBeenCalled();
  });

  it('says what to do about it, not just that it failed', async () => {
    // An error a developer can act on at 6pm without reading the source.
    await expect(setSecureItem('somethingNew' as never, 'x')).rejects.toThrow(
      /Add it to SECURE_KEYS/,
    );
  });
});
