import {
  LocalStoreFullError,
  LocalStoreLockedError,
  type Condition,
  type LocalStore,
  type Migration,
  type OpenOptions,
  type Query,
  type Row,
  type Transaction,
  type Value,
  type Write,
} from './driver';

/**
 * The local database, in memory (CP64).
 *
 * This is not a mock. It is the second implementation of the same contract, and it is the one
 * every correctness test in the offline system runs against — the outbox, the command handler,
 * batching, backoff, per-event outcomes, reconciliation, the whole §13.10 matrix. `op-sqlite.ts`
 * is the same contract on a device; `test/local-store.test.ts` runs one suite against both.
 *
 * Three things it deliberately reproduces rather than simplifying away, because a test against a
 * driver that is kinder than the real one is a test that passes on a laptop and fails in a clinic:
 *
 *   - **A key is required, and the wrong key is refused.** The `disk` remembers what it was
 *     written with. That is what SQLCipher does, and it is what makes "the database file is
 *     unreadable without the keystore key" assertable here at all.
 *   - **Storage can be full**, at a moment of the test's choosing, including in the middle of a
 *     transaction. §13.10 names it, and the property that matters — a half-written entry never
 *     exists — is only observable if the driver can fail.
 *   - **A transaction is all or nothing**, including one interrupted by the process going away.
 *     The committed state lives in a `MemoryDisk` that outlives the store object, so "kill the app
 *     with a full queue" is a new store over the same disk, and uncommitted work is simply not
 *     there.
 */

export interface MemoryDisk {
  /** Table name to rows. This is the durable part: it survives a store being thrown away. */
  tables: Record<string, Row[]>;
  /** Primary key columns per table, learned from the migrations. */
  keys: Record<string, string[]>;
  /** Applied schema versions. */
  versions: number[];
  /** What the database was written with. A different key cannot read it. */
  key: string | null;
}

/** A fresh, empty, unwritten device. */
export function createDisk(): MemoryDisk {
  return { tables: {}, keys: {}, versions: [], key: null };
}

/**
 * What can go wrong, on purpose.
 *
 * Mutable, so a test can fill the device up halfway through a clinic session and empty it again
 * afterwards — which is what actually happens, and is the interesting case: an operator who is
 * told "not saved" and then frees some space must be able to record the measurement.
 */
export interface MemoryFaults {
  /** Every write fails as though the disk were full. */
  full?: boolean;
  /** Writes fail once the total row count would exceed this. */
  rowLimit?: number;
  /** The next N commits fail *after* the work was done, leaving nothing behind. */
  failCommits?: number;
  /** Let this many commits through first, so a device can fill up part way through a sync. */
  afterCommits?: number;
}

export interface MemoryStore extends LocalStore {
  readonly disk: MemoryDisk;
  readonly faults: MemoryFaults;
  /** How many transactions have committed. A test arms `afterCommits` relative to this. */
  readonly commits: number;
}

export interface MemoryOptions {
  /** The device's storage. Pass an existing one to reopen a database across a "restart". */
  disk?: MemoryDisk;
  faults?: MemoryFaults;
}

export function createMemoryStore(options: MemoryOptions = {}): MemoryStore {
  const disk = options.disk ?? createDisk();
  const faults: MemoryFaults = options.faults ?? {};
  let opened = false;
  let inTransaction = false;
  let commits = 0;

  function guard(): void {
    if (!opened) throw new LocalStoreLockedError('the local database is not open');
  }

  function totalRows(tables: Record<string, Row[]>): number {
    return Object.values(tables).reduce((sum, rows) => sum + rows.length, 0);
  }

  function checkRoom(tables: Record<string, Row[]>, adding: number): void {
    if (faults.full) throw new LocalStoreFullError();
    if (faults.rowLimit !== undefined && totalRows(tables) + adding > faults.rowLimit) {
      throw new LocalStoreFullError();
    }
  }

  const store: MemoryStore = {
    disk,
    faults,

    get commits() {
      return commits;
    },

    get isOpen() {
      return opened;
    },

    async open(config: OpenOptions) {
      if (!config.key || config.key.trim() === '') {
        // The whole point of the checkpoint's first acceptance criterion. A driver that opened
        // an unencrypted database when the keystore was unavailable would produce a tablet full
        // of readable PHI that behaves exactly like a working one.
        throw new LocalStoreLockedError('the local database cannot be opened without its key');
      }
      if (disk.key !== null && disk.key !== config.key) {
        throw new LocalStoreLockedError('that is not the key this database was written with');
      }
      disk.key = config.key;
      opened = true;
    },

    async migrate(migrations: readonly Migration[]) {
      guard();
      let ran = 0;
      for (const migration of [...migrations].sort((a, b) => a.version - b.version)) {
        if (disk.versions.includes(migration.version)) continue;
        for (const change of migration.changes) {
          switch (change.kind) {
            case 'create table':
              disk.tables[change.table] ??= [];
              disk.keys[change.table] = [...change.primaryKey];
              break;
            case 'add column': {
              const rows = disk.tables[change.table];
              if (rows === undefined)
                throw new Error(`no table ${change.table} to add a column to`);
              for (const row of rows) row[change.column.name] = change.column.default ?? null;
              break;
            }
            case 'create index':
              // Indexes change how fast a read is, never what it answers. There is nothing for an
              // in-memory driver to do, and pretending otherwise would be theatre.
              break;
          }
        }
        disk.versions.push(migration.version);
        ran += 1;
      }
      return ran;
    },

    async all(query: Query) {
      guard();
      return read(disk.tables, query);
    },

    async run(write: Write | Write[]) {
      guard();
      // Outside a transaction is still inside one: several writes that must not half-happen are
      // the normal case here, not the exception.
      return store.transaction(async (tx) => tx.run(write));
    },

    async transaction<T>(fn: (tx: Transaction) => Promise<T>): Promise<T> {
      guard();
      if (inTransaction) {
        // Nesting would need savepoints to mean anything, and nothing in the offline system
        // wants one. Refused loudly rather than silently flattened, because a flattened
        // "transaction" that rolls back its caller's work is a bug nobody would look for.
        throw new Error('local transactions do not nest');
      }
      inTransaction = true;
      const working = clone(disk.tables);
      try {
        const result = await fn({
          all: async (query) => read(working, query),
          run: async (write) => apply(working, disk.keys, write, checkRoom),
        });
        commits += 1;
        if (
          faults.failCommits !== undefined &&
          faults.failCommits > 0 &&
          commits > (faults.afterCommits ?? 0)
        ) {
          faults.failCommits -= 1;
          throw new LocalStoreFullError('the commit failed');
        }
        // The commit: one assignment, so there is no state in which half of it happened.
        disk.tables = working;
        return result;
      } finally {
        inTransaction = false;
      }
    },

    async wipe() {
      guard();
      for (const table of Object.keys(disk.tables)) disk.tables[table] = [];
    },

    async close() {
      opened = false;
    },
  };

  return store;
}

// --- the small relational engine ---

function clone(tables: Record<string, Row[]>): Record<string, Row[]> {
  const out: Record<string, Row[]> = {};
  for (const [name, rows] of Object.entries(tables)) out[name] = rows.map((row) => ({ ...row }));
  return out;
}

function rowsOf(tables: Record<string, Row[]>, table: string): Row[] {
  const rows = tables[table];
  if (rows === undefined) throw new Error(`no such local table: ${table}`);
  return rows;
}

function matches(row: Row, where: Condition[] | undefined): boolean {
  if (!where) return true;
  return where.every((condition) => {
    const value = row[condition.column] ?? null;
    switch (condition.op) {
      case 'is null':
        return value === null;
      case 'is not null':
        return value !== null;
      case 'in':
        return condition.values.includes(value);
      case '=':
        return value === condition.value;
      case '!=':
        return value !== condition.value;
      default: {
        // A comparison against null is never true, which is SQL's answer and the one the
        // op-sqlite driver will give.
        if (value === null || condition.value === null) return false;
        const order = compare(value, condition.value);
        if (condition.op === '<') return order < 0;
        if (condition.op === '<=') return order <= 0;
        if (condition.op === '>') return order > 0;
        return order >= 0;
      }
    }
  });
}

function compare(a: Value, b: Value): number {
  // Nulls first, as SQLite orders them ascending.
  if (a === null && b === null) return 0;
  if (a === null) return -1;
  if (b === null) return 1;
  if (typeof a === 'number' && typeof b === 'number') return a - b;
  return String(a) < String(b) ? -1 : String(a) > String(b) ? 1 : 0;
}

function read(tables: Record<string, Row[]>, query: Query): Row[] {
  let rows = rowsOf(tables, query.table).filter((row) => matches(row, query.where));
  for (const order of [...(query.order ?? [])].reverse()) {
    rows = [...rows].sort((a, b) => {
      const direction = order.direction === 'desc' ? -1 : 1;
      return compare(a[order.column] ?? null, b[order.column] ?? null) * direction;
    });
  }
  if (query.limit !== undefined) rows = rows.slice(0, query.limit);
  return rows.map((row) => (query.columns ? pick(row, query.columns) : { ...row }));
}

function pick(row: Row, columns: string[]): Row {
  const out: Row = {};
  for (const column of columns) out[column] = row[column] ?? null;
  return out;
}

function apply(
  tables: Record<string, Row[]>,
  keys: Record<string, string[]>,
  write: Write | Write[],
  checkRoom: (tables: Record<string, Row[]>, adding: number) => void,
): number {
  const writes = Array.isArray(write) ? write : [write];
  let changed = 0;
  for (const one of writes) {
    const rows = rowsOf(tables, one.table);
    switch (one.kind) {
      case 'insert': {
        const key = keys[one.table] ?? [];
        const existing = rows.findIndex((row) =>
          key.every((column) => (row[column] ?? null) === (one.row[column] ?? null)),
        );
        // Replacing a row that is already there needs no more room than it already occupies,
        // which is what SQLite does and what makes "the disk is full" testable at a row count.
        checkRoom(tables, key.length > 0 && existing >= 0 ? 0 : 1);
        if (key.length > 0 && existing >= 0) {
          const conflict = one.onConflict ?? 'fail';
          if (conflict === 'fail') {
            throw new Error(`a row with that key is already in ${one.table}`);
          }
          if (conflict === 'ignore') break;
          rows[existing] = { ...one.row };
          changed += 1;
          break;
        }
        rows.push({ ...one.row });
        changed += 1;
        break;
      }
      case 'update': {
        checkRoom(tables, 0);
        for (const row of rows) {
          if (!matches(row, one.where)) continue;
          Object.assign(row, one.set);
          changed += 1;
        }
        break;
      }
      case 'delete': {
        const kept = rows.filter((row) => !matches(row, one.where));
        changed += rows.length - kept.length;
        tables[one.table] = kept;
        break;
      }
    }
  }
  return changed;
}
