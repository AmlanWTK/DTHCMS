import { describe, expect, it, vi } from 'vitest';

import { describeLocalStore, type Device } from './local-store-contract';

/**
 * The local database (CP64).
 *
 * The driver contract runs here against the in-memory implementation, which is the one every
 * correctness test in the offline system uses; `local-store-sql.test.ts` runs the same suite
 * against the SQL the device driver generates. What is here in addition is what only the
 * in-memory driver can be made to do — run out of room at a chosen moment — and the key
 * management, which is the acceptance criterion nothing else can assert.
 */

const keystore = new Map<string, string>();
vi.mock('expo-secure-store', () => ({
  setItemAsync: vi.fn(async (key: string, value: string) => {
    keystore.set(key, value);
  }),
  getItemAsync: vi.fn(async (key: string) => keystore.get(key) ?? null),
  deleteItemAsync: vi.fn(async (key: string) => {
    keystore.delete(key);
  }),
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'WHEN_UNLOCKED_THIS_DEVICE_ONLY',
}));

const {
  LocalStoreFullError,
  LocalStoreLockedError,
  MIGRATIONS,
  TABLES,
  createDisk,
  createMemoryStore,
  isStorageFull,
} = await import('../src/lib/local-store');
const { KEY_BYTES, databaseKey, forgetDatabaseKey, keyIsReleased, lockDatabaseKey } =
  await import('../src/lib/local-store/key');

const KEY = 'b8f1d0c3a2e4569788796a5b4c3d2e1f00112233445566778899aabbccddeeff';

async function memoryDevice(): Promise<Device> {
  const disk = createDisk();
  const store = createMemoryStore({ disk });
  await store.open({ key: KEY });
  return {
    store,
    key: KEY,
    reopen: async () => {
      await store.close();
      const next = createMemoryStore({ disk });
      await next.open({ key: KEY });
      return next;
    },
  };
}

describeLocalStore({ name: 'in memory', device: memoryDevice, enforcesKey: true });

describe('the schema this application actually uses', () => {
  it('creates every table the offline system needs', async () => {
    const store = createMemoryStore();
    await store.open({ key: KEY });
    expect(await store.migrate(MIGRATIONS)).toBe(MIGRATIONS.length);
    for (const table of Object.values(TABLES)) {
      if (table === TABLES.migrations) continue;
      await expect(store.all({ table })).resolves.toEqual([]);
    }
  });

  it('adds the sync log on the second migration rather than in the first', async () => {
    // The point of splitting it: a schema that has only ever been created from scratch has never
    // had its migration path run, and the first device to prove otherwise should not be a tablet
    // in a clinic.
    const store = createMemoryStore();
    await store.open({ key: KEY });
    const [one] = MIGRATIONS;
    if (!one) throw new Error('there is no first migration');
    await store.migrate([one]);
    await expect(store.all({ table: TABLES.syncLog })).rejects.toThrow(/no such local table/);
    expect(await store.migrate(MIGRATIONS)).toBe(MIGRATIONS.length - 1);
    await expect(store.all({ table: TABLES.syncLog })).resolves.toEqual([]);
  });
});

describe('a device that has run out of room', () => {
  it('writes nothing at all rather than half an entry', async () => {
    // §13.10's storage-full scenario, at the level where it must be survivable: the transaction
    // that would have written three rows writes none.
    const store = createMemoryStore();
    await store.open({ key: KEY });
    await store.migrate(MIGRATIONS);
    await store.run({
      kind: 'insert',
      table: TABLES.syncMeta,
      row: { key: 'before', value: '1' },
    });

    store.faults.full = true;
    await expect(
      store.transaction(async (tx) => {
        await tx.run({ kind: 'insert', table: TABLES.syncMeta, row: { key: 'a', value: '1' } });
        await tx.run({ kind: 'insert', table: TABLES.syncMeta, row: { key: 'b', value: '2' } });
      }),
    ).rejects.toBeInstanceOf(LocalStoreFullError);

    store.faults.full = false;
    const rows = await store.all({ table: TABLES.syncMeta });
    expect(rows.map((row) => row.key)).toEqual(['before']);
  });

  it('recovers as soon as there is room, without being restarted', async () => {
    // The operator deletes some photographs and tries again. Nothing about the store should have
    // to be reset for that to work.
    const store = createMemoryStore();
    await store.open({ key: KEY });
    await store.migrate(MIGRATIONS);
    store.faults.rowLimit = 1;
    await store.run({ kind: 'insert', table: TABLES.syncMeta, row: { key: 'a', value: '1' } });
    await expect(
      store.run({ kind: 'insert', table: TABLES.syncMeta, row: { key: 'b', value: '2' } }),
    ).rejects.toBeInstanceOf(LocalStoreFullError);
    store.faults.rowLimit = 10;
    await store.run({ kind: 'insert', table: TABLES.syncMeta, row: { key: 'b', value: '2' } });
    expect(await store.all({ table: TABLES.syncMeta })).toHaveLength(2);
  });

  it('loses a commit that fails, and nothing else', async () => {
    const store = createMemoryStore();
    await store.open({ key: KEY });
    await store.migrate(MIGRATIONS);
    store.faults.failCommits = 1;
    await expect(
      store.run({ kind: 'insert', table: TABLES.syncMeta, row: { key: 'a', value: '1' } }),
    ).rejects.toBeInstanceOf(LocalStoreFullError);
    expect(await store.all({ table: TABLES.syncMeta })).toEqual([]);
    await store.run({ kind: 'insert', table: TABLES.syncMeta, row: { key: 'a', value: '1' } });
    expect(await store.all({ table: TABLES.syncMeta })).toHaveLength(1);
  });

  it('recognises SQLite saying it in its own words', () => {
    // The native layer may raise before this one does, and the app branches on the class.
    expect(isStorageFull(new Error('database or disk is full'))).toBe(true);
    expect(isStorageFull(new Error('SQLITE_FULL: no room'))).toBe(true);
    expect(isStorageFull(new Error('no such table'))).toBe(false);
    expect(isStorageFull('not an error')).toBe(false);
  });
});

describe('the store the application holds', () => {
  it('opens once, stays open, and closes on the way out', async () => {
    const { localStore, lockLocalStore, unlockLocalStore } = await import('../src/lib/local-store');
    keystore.clear();
    await forgetDatabaseKey();
    expect(localStore()).toBeNull();

    const store = createMemoryStore();
    const opened = await unlockLocalStore({ store });
    expect(opened).toBe(store);
    expect(localStore()).toBe(store);
    // The schema is up to date, and a second call does not open a second connection.
    expect(await store.all({ table: TABLES.outbox })).toEqual([]);
    expect(await unlockLocalStore({ store: createMemoryStore() })).toBe(store);

    await lockLocalStore();
    expect(localStore()).toBeNull();
    expect(store.isOpen).toBe(false);
    // The key left memory with it; the database on disk is unchanged and unreadable until
    // somebody signs in again.
    expect(keyIsReleased()).toBe(false);
  });
});

describe('the key', () => {
  it('is generated once, kept in the keystore, and reused', async () => {
    keystore.clear();
    await forgetDatabaseKey();
    const bytes = new Uint8Array(KEY_BYTES).fill(7);
    const first = await databaseKey({ randomBytes: () => bytes });
    expect(first).toHaveLength(KEY_BYTES * 2);
    expect([...keystore.values()]).toContain(first);

    lockDatabaseKey();
    expect(keyIsReleased()).toBe(false);
    // A second generation would not fail — it would produce a database nobody can read.
    const again = await databaseKey({ randomBytes: () => new Uint8Array(KEY_BYTES).fill(9) });
    expect(again).toBe(first);
    expect(keyIsReleased()).toBe(true);
  });

  it('goes nowhere except the keystore', async () => {
    keystore.clear();
    await forgetDatabaseKey();
    await databaseKey({ randomBytes: () => new Uint8Array(KEY_BYTES).fill(3) });
    // The allowlist in lib/secure-keys is the inventory of secrets on this device; a key that
    // reached any other storage would not be in it.
    expect([...keystore.keys()]).toEqual(['dthcms.database-key']);
  });

  it('is forgotten on request, after which a new one is made', async () => {
    keystore.clear();
    await forgetDatabaseKey();
    const first = await databaseKey({ randomBytes: () => new Uint8Array(KEY_BYTES).fill(1) });
    await forgetDatabaseKey();
    expect(keystore.size).toBe(0);
    const second = await databaseKey({ randomBytes: () => new Uint8Array(KEY_BYTES).fill(2) });
    expect(second).not.toBe(first);
  });

  it('will not open a database written with a different key', async () => {
    // The in-memory stand-in for what SQLCipher does with the file. The real proof is the manual
    // one — pull the file off the tablet and try to open it — and it is recorded as such.
    const disk = createDisk();
    const store = createMemoryStore({ disk });
    await store.open({ key: KEY });
    await store.migrate(MIGRATIONS);
    await store.close();

    const impostor = createMemoryStore({ disk });
    await expect(impostor.open({ key: 'ffffffff' })).rejects.toBeInstanceOf(LocalStoreLockedError);
    expect(impostor.isOpen).toBe(false);
  });
});
