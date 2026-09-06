/**
 * The local database, as an interface (CP64, §13.3).
 *
 * # Why the storage driver is injected rather than imported
 *
 * The plan's own risk note for this checkpoint is *"SQLCipher build configuration on Expo —
 * validate early; it is a known integration friction point"*, and the honest consequence of that
 * sentence is what this file exists for. `@op-engineering/op-sqlite` is a native module: it needs
 * a prebuild, it runs in Hermes on a device, and nothing in it can be executed by a test runner.
 * If the outbox, the command handler, the sync engine and the reconciler were written against it
 * directly, then **every correctness-critical decision in the offline system would be reachable
 * only from a tablet** — and code that can only be exercised on hardware is code that is not
 * exercised. For CP66, whose stated failure mode is silent clinical data corruption, that is not a
 * trade-off worth making.
 *
 * So the whole system is written against `LocalStore`, and there are two implementations:
 * `op-sqlite.ts` for the device and `memory.ts` for the tests. Everything above this line —
 * migrations, the outbox, the command handler, batching, backoff, per-event outcomes,
 * reconciliation — is driver-agnostic and is tested against the in-memory one.
 *
 * # Why the interface is not `run(sql, params)`
 *
 * The obvious narrow interface is a SQL pipe, and it was rejected. An in-memory implementation of
 * `run(sql)` would have to *parse SQL*, so the tests would be exercising a toy SQL engine written
 * for the tests — which is the one thing worse than not testing at all, because a bug in the toy
 * makes a correct system look broken and a bug shared by both makes a broken one look correct.
 *
 * The interface is therefore a small set of **operations** — a filtered read, three kinds of
 * write, and a transaction. SQL exists in exactly one place, `sql.ts`, which compiles an operation
 * into a parameterised statement, and it is covered two ways: by unit tests on the compiler's
 * output, and by running the driver contract suite against real SQLite through `node:sqlite`
 * (`test/local-store-sql.test.ts`). What remains genuinely unexercised is the native binding and
 * SQLCipher itself, and that is stated where it lives rather than implied to be covered.
 *
 * # Values
 *
 * Text, integers, reals and null. No booleans (0/1), no dates (ISO 8601 strings for clinical
 * instants, epoch milliseconds for local bookkeeping), no objects (JSON text). SQLite would coerce
 * some of those silently and the in-memory driver would not, which is exactly the kind of
 * disagreement between the two implementations that would make the tests lie.
 */

/** What a column may hold. */
export type Value = string | number | null;

/** One row, as the driver moves it. */
export type Row = Record<string, Value>;

/** The comparisons a read may use. Deliberately few: this is a queue, not a query language. */
export type Comparison = '=' | '!=' | '<' | '<=' | '>' | '>=';

/** One condition. Several are ANDed; there is no OR, because nothing here has needed one. */
export type Condition =
  | { column: string; op: Comparison; value: Value }
  | { column: string; op: 'in'; values: Value[] }
  | { column: string; op: 'is null' | 'is not null' };

export interface Order {
  column: string;
  direction: 'asc' | 'desc';
}

/** A read. */
export interface Query {
  table: string;
  columns?: string[];
  where?: Condition[];
  order?: Order[];
  limit?: number;
}

/**
 * A write.
 *
 * `onConflict` is on the insert because the offline system leans on it in two places that mean
 * different things: `ignore` is how a pulled event that is already local becomes a no-op rather
 * than an error (reconciliation has to be idempotent), and `replace` is how a projection takes a
 * newer value. `fail` is the default, so a duplicate that nobody planned for is loud.
 */
export type Write =
  | { kind: 'insert'; table: string; row: Row; onConflict?: 'fail' | 'ignore' | 'replace' }
  | { kind: 'update'; table: string; set: Row; where?: Condition[] }
  | { kind: 'delete'; table: string; where?: Condition[] };

/** A column in a migration. */
export interface Column {
  name: string;
  type: 'text' | 'integer' | 'real';
  notNull?: boolean;
  default?: Value;
}

/** What one migration does. */
export type Change =
  | { kind: 'create table'; table: string; columns: Column[]; primaryKey: string[] }
  | { kind: 'add column'; table: string; column: Column }
  | { kind: 'create index'; name: string; table: string; columns: string[] };

/**
 * One step of the local schema.
 *
 * Numbered rather than hashed, and applied in order, with the applied versions kept in
 * `schema_migrations`. A device that has been in a drawer for two releases runs the ones it
 * missed and nothing else.
 */
export interface Migration {
  version: number;
  name: string;
  changes: Change[];
}

/** The transaction handle. Reads inside it see the transaction's own writes. */
export interface Transaction {
  all(query: Query): Promise<Row[]>;
  run(write: Write | Write[]): Promise<number>;
}

export interface OpenOptions {
  /**
   * The database key, hex, from the OS keystore.
   *
   * Required, and empty is refused rather than treated as "no encryption". A driver that
   * quietly opened an unencrypted database when the keystore was unavailable would produce a
   * tablet full of readable PHI that behaves exactly like a working one.
   */
  key: string;
  /** The file name. Ignored by the in-memory driver. */
  name?: string;
}

/**
 * The local database.
 *
 * Every method is async because the device's is. The in-memory one resolves immediately, which
 * makes the tests deterministic without making the interface a lie.
 */
export interface LocalStore {
  open(options: OpenOptions): Promise<void>;
  /** Applies the migrations this store has not applied. Returns how many it ran. */
  migrate(migrations: readonly Migration[]): Promise<number>;
  all(query: Query): Promise<Row[]>;
  /** A write, or several. Outside a transaction they are still committed together. */
  run(write: Write | Write[]): Promise<number>;
  /**
   * All of it, or none of it.
   *
   * This is what makes the command handler safe: the event, the projection and the outbox row
   * commit together, so the screen's state and the queue cannot disagree — which is the same
   * decision the server makes one layer up (`docs/sync.md`, "one transaction per event").
   */
  transaction<T>(fn: (tx: Transaction) => Promise<T>): Promise<T>;
  /** Deletes every row in every table. The key is not touched; see `key.ts` for why. */
  wipe(): Promise<void>;
  close(): Promise<void>;
  /** Whether this store is open. A closed store refuses every read and write. */
  readonly isOpen: boolean;
}

/** The base of everything this layer throws, so a caller can tell storage from logic. */
export class LocalStoreError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'LocalStoreError';
  }
}

/**
 * The database is not open, or was opened without a key.
 *
 * A separate class because the caller's answer is different: this is "nobody has signed in yet",
 * not "the write failed".
 */
export class LocalStoreLockedError extends LocalStoreError {
  constructor(message = 'the local database is locked') {
    super(message);
    this.name = 'LocalStoreLockedError';
  }
}

/**
 * There is no room on the device.
 *
 * Its own class because it is the one storage failure with a distinct clinical consequence: the
 * operator must be told *before* they walk away from the patient, and the entry must not be
 * half-written. §13.10 names it as a scenario for that reason.
 */
export class LocalStoreFullError extends LocalStoreError {
  constructor(message = 'there is no room left on this device') {
    super(message);
    this.name = 'LocalStoreFullError';
  }
}

/** Whether a failure is the device being out of room, wherever it was raised. */
export function isStorageFull(error: unknown): boolean {
  if (error instanceof LocalStoreFullError) return true;
  if (!(error instanceof Error)) return false;
  // SQLite's own words for it, in case the native layer raises before this one does.
  return /disk (is )?full|SQLITE_FULL|database or disk is full/i.test(error.message);
}
