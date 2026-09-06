import { databaseKey, lockDatabaseKey } from './key';
import { createSQLiteStore } from './op-sqlite';
import { MIGRATIONS } from './schema';

import type { LocalStore } from './driver';

/**
 * The local database layer (CP64).
 *
 * The store itself is a singleton, because there is one database file and opening it twice would
 * produce two connections that disagree about a transaction. It is deliberately **not** created at
 * import time: the key is released only after somebody signs in, so before that there is nothing
 * to open and a module that opened one anyway would either fail or open an unencrypted one.
 */

export {
  LocalStoreError,
  LocalStoreFullError,
  LocalStoreLockedError,
  isStorageFull,
  type Change,
  type Column,
  type Condition,
  type LocalStore,
  type Migration,
  type OpenOptions,
  type Order,
  type Query,
  type Row,
  type Transaction,
  type Value,
  type Write,
} from './driver';

export {
  EVENT_ORIGINS,
  META_KEYS,
  MIGRATIONS,
  OUTBOX_STATES,
  SYNC_LOG_LIMIT,
  TABLES,
  type EventOrigin,
  type MetaKey,
  type OutboxState,
} from './schema';

export {
  createDisk,
  createMemoryStore,
  type MemoryDisk,
  type MemoryFaults,
  type MemoryStore,
} from './memory';
export {
  DATABASE_NAME,
  createSQLiteStore,
  verifyEncrypted,
  type NativeDatabase,
  type NativeOpen,
} from './op-sqlite';
export { KEY_BYTES, databaseKey, forgetDatabaseKey, keyIsReleased, lockDatabaseKey } from './key';
export { compileChange, compileQuery, compileWrite, type Statement } from './sql';

let current: LocalStore | null = null;

export interface UnlockOptions {
  /** Injected by the tests; the app opens SQLCipher through op-sqlite. */
  store?: LocalStore;
}

/**
 * Open the local database and bring the schema up to date.
 *
 * Called once, after authentication. Safe to call again — a second call while a store is open
 * returns the same one rather than opening a second connection to the same file.
 */
export async function unlockLocalStore(options: UnlockOptions = {}): Promise<LocalStore> {
  if (current?.isOpen) return current;
  const store = options.store ?? createSQLiteStore();
  await store.open({ key: await databaseKey() });
  await store.migrate(MIGRATIONS);
  current = store;
  return store;
}

/** The open store, or null when nobody has signed in. Screens read through this. */
export function localStore(): LocalStore | null {
  return current?.isOpen ? current : null;
}

/**
 * Close the database and drop the key from memory.
 *
 * What stays on disk is whatever was committed, encrypted. That is the point: an undelivered
 * measurement survives a sign-out, a restart and a flat battery, and is unreadable until somebody
 * signs in again.
 */
export async function lockLocalStore(): Promise<void> {
  const store = current;
  current = null;
  lockDatabaseKey();
  if (store) await store.close();
}
