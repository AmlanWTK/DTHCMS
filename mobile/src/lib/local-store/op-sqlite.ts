import {
  LocalStoreError,
  LocalStoreFullError,
  LocalStoreLockedError,
  isStorageFull,
  type LocalStore,
  type Migration,
  type OpenOptions,
  type Query,
  type Row,
  type Transaction,
  type Value,
  type Write,
} from './driver';
import { TABLES } from './schema';
import { compileChange, compileQuery, compileWrite } from './sql';

/**
 * The local database on a device: SQLCipher through `@op-engineering/op-sqlite` (CP64).
 *
 * # What is proven here and what is not
 *
 * Everything above the driver is tested against `memory.ts`, and the SQL this file runs is tested
 * against real SQLite through `node:sqlite` (`test/local-store-sql.test.ts`) — the same contract
 * suite, the same statements, an actual query planner. **What is not exercised anywhere is the
 * native binding and SQLCipher itself**: the encryption, the key handling inside the native layer,
 * and the file on the device's disk. That needs an Expo prebuild and a tablet, and this
 * environment has neither.
 *
 * That gap is stated rather than papered over, and it is narrow by construction: this file has no
 * decisions in it. It opens a database with a key, compiles operations somebody else described,
 * and maps two failures onto the classes the rest of the app branches on. Everything that could be
 * wrong *about the offline system* is above it.
 *
 * The device work that remains, and which the plan already flags as this checkpoint's risk:
 *
 *   1. `"op-sqlite": { "sqlcipher": true }` in `mobile/package.json` — set, and it is what makes
 *      `encryptionKey` mean anything. Without it op-sqlite compiles plain SQLite and **accepts the
 *      key argument in silence**, which is the failure mode worth naming: an encrypted-looking
 *      database that is not encrypted.
 *   2. `expo prebuild` and a development build; the module cannot run in Expo Go.
 *   3. On the device, confirm criterion 1 by hand: pull the file and open it with `sqlite3`. It
 *      must refuse. `verifyEncrypted` below is the same check from inside the app, but a check
 *      that runs in the process holding the key proves less than one that does not.
 */

/** The narrow slice of op-sqlite this driver uses. Injectable, which is what makes it testable. */
export interface NativeDatabase {
  execute(sql: string, params?: Value[]): Promise<{ rows?: Row[]; rowsAffected?: number }>;
  close(): void | Promise<void>;
}

export interface NativeOpenOptions {
  name: string;
  location?: string;
  /** The SQLCipher key. Never logged, never defaulted, never optional. */
  encryptionKey: string;
}

export type NativeOpen = (options: NativeOpenOptions) => NativeDatabase | Promise<NativeDatabase>;

/** The file name. One database per device, not one per operator: the queue outlives a shift. */
export const DATABASE_NAME = 'dthcms.db';

async function openNative(options: NativeOpenOptions): Promise<NativeDatabase> {
  // Imported dynamically, and only on a device. A static import would put the whole React Native
  // module graph into every test run that touches the local store, which is what pushed the last
  // three checkpoints' logic out of their screens and into testable files in the first place.
  const module = await import('@op-engineering/op-sqlite');
  const db = module.open({ name: options.name, encryptionKey: options.encryptionKey });
  return {
    execute: async (sql, params) => {
      const result = await db.execute(sql, params ?? []);
      return { rows: result.rows as Row[] | undefined, rowsAffected: result.rowsAffected };
    },
    close: () => db.close(),
  };
}

export interface SQLiteStoreOptions {
  /** The opener. The app leaves this out; the tests pass one over `node:sqlite`. */
  open?: NativeOpen;
  name?: string;
}

export function createSQLiteStore(options: SQLiteStoreOptions = {}): LocalStore {
  const openDatabase = options.open ?? openNative;
  const name = options.name ?? DATABASE_NAME;
  let db: NativeDatabase | null = null;
  let inTransaction = false;

  function connection(): NativeDatabase {
    if (!db) throw new LocalStoreLockedError('the local database is not open');
    return db;
  }

  async function execute(sql: string, params: Value[]): Promise<{ rows: Row[]; changes: number }> {
    try {
      const result = await connection().execute(sql, params);
      return { rows: result.rows ?? [], changes: result.rowsAffected ?? 0 };
    } catch (error) {
      // Two failures are worth their own class because the app answers them differently: no room
      // on the device is something the operator must be told at once, and everything else is a
      // fault. The rest keep their own message.
      if (isStorageFull(error)) throw new LocalStoreFullError();
      throw error;
    }
  }

  async function runOne(write: Write): Promise<number> {
    const statement = compileWrite(write);
    const result = await execute(statement.sql, statement.params);
    return result.changes;
  }

  const transaction: Transaction = {
    all: async (query: Query) => {
      const statement = compileQuery(query);
      return (await execute(statement.sql, statement.params)).rows;
    },
    run: async (write: Write | Write[]) => {
      let changed = 0;
      for (const one of Array.isArray(write) ? write : [write]) changed += await runOne(one);
      return changed;
    },
  };

  const store: LocalStore = {
    get isOpen() {
      return db !== null;
    },

    async open(config: OpenOptions) {
      if (!config.key || config.key.trim() === '') {
        throw new LocalStoreLockedError('the local database cannot be opened without its key');
      }
      db = await openDatabase({ name: config.name ?? name, encryptionKey: config.key });
      try {
        // SQLCipher does not check the key when the file is opened — it checks it on the first
        // read, and reports a wrong key as "file is not a database". So the first read happens
        // here, deliberately, rather than in whatever screen happened to ask first.
        await execute('SELECT count(*) AS tables FROM sqlite_master', []);
      } catch (error) {
        db = null;
        const message = error instanceof Error ? error.message : String(error);
        if (/not a database|file is encrypted|HMAC/i.test(message)) {
          throw new LocalStoreLockedError('that is not the key this database was written with');
        }
        throw error;
      }
    },

    async migrate(migrations: readonly Migration[]) {
      connection();
      await execute(
        `CREATE TABLE IF NOT EXISTS ${TABLES.migrations} ` +
          '(version INTEGER NOT NULL, name TEXT NOT NULL, applied_at INTEGER NOT NULL, ' +
          'PRIMARY KEY (version))',
        [],
      );
      const applied = new Set(
        (await execute(`SELECT version FROM ${TABLES.migrations}`, [])).rows.map((row) =>
          Number(row.version),
        ),
      );
      let ran = 0;
      for (const migration of [...migrations].sort((a, b) => a.version - b.version)) {
        if (applied.has(migration.version)) continue;
        // One transaction per migration. SQLite runs DDL transactionally, so a device that loses
        // power halfway through an upgrade comes back on the version it started from rather than
        // half way between two.
        await store.transaction(async (tx) => {
          for (const change of migration.changes) {
            const statement = compileChange(change);
            await execute(statement.sql, statement.params);
          }
          await tx.run({
            kind: 'insert',
            table: TABLES.migrations,
            row: { version: migration.version, name: migration.name, applied_at: Date.now() },
          });
        });
        ran += 1;
      }
      return ran;
    },

    async all(query: Query) {
      connection();
      return transaction.all(query);
    },

    async run(write: Write | Write[]) {
      connection();
      return store.transaction(async (tx) => tx.run(write));
    },

    async transaction<T>(fn: (tx: Transaction) => Promise<T>): Promise<T> {
      connection();
      if (inTransaction) throw new LocalStoreError('local transactions do not nest');
      inTransaction = true;
      // IMMEDIATE rather than DEFERRED: this transaction is going to write, and taking the write
      // lock at the start turns "somebody else is writing" into a wait rather than into a rollback
      // half way through a clinical entry.
      await execute('BEGIN IMMEDIATE', []);
      try {
        const result = await fn(transaction);
        await execute('COMMIT', []);
        return result;
      } catch (error) {
        try {
          await execute('ROLLBACK', []);
        } catch {
          // A rollback that fails means the transaction is already gone. The original failure is
          // what the caller needs to hear about.
        }
        throw error;
      } finally {
        inTransaction = false;
      }
    },

    async wipe() {
      connection();
      const tables = (
        await execute("SELECT name FROM sqlite_master WHERE type = 'table'", [])
      ).rows.map((row) => String(row.name));
      await store.transaction(async () => {
        for (const table of tables) {
          if (table.startsWith('sqlite_')) continue;
          if (table === TABLES.migrations) continue;
          await execute(`DELETE FROM ${table}`, []);
        }
      });
    },

    async close() {
      const open = db;
      db = null;
      if (open) await open.close();
    },
  };

  return store;
}

/**
 * A check that the database on disk is encrypted, from inside the app.
 *
 * Reads the first sixteen bytes through SQLite itself: a plain SQLite file starts with the string
 * `SQLite format 3`, and an SQLCipher one does not, because the header is encrypted along with
 * everything else. It is a weaker check than pulling the file off the device and trying to open
 * it — which is what the manual verification asks for, and which is the one that proves it to
 * somebody who does not already trust the process doing the checking.
 *
 * Returns false rather than throwing when the check cannot be made: this reports on encryption and
 * must never be the reason a station cannot record a measurement.
 */
export async function verifyEncrypted(db: NativeDatabase): Promise<boolean> {
  try {
    const result = await db.execute('PRAGMA cipher_version', []);
    const rows = result.rows ?? [];
    const version = rows[0] ? Object.values(rows[0])[0] : null;
    return typeof version === 'string' && version.length > 0;
  } catch {
    return false;
  }
}
