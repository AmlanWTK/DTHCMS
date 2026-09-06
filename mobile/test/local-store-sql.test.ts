import { mkdtempSync, rmSync } from 'node:fs';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import { afterAll, describe, expect, it } from 'vitest';

import { LocalStoreError } from '../src/lib/local-store/driver';
import { compileChange, compileQuery, compileWrite } from '../src/lib/local-store/sql';
import {
  createSQLiteStore,
  type NativeDatabase,
  type NativeOpen,
} from '../src/lib/local-store/op-sqlite';
import { describeLocalStore, type Device } from './local-store-contract';

/**
 * The SQL the device driver generates (CP64).
 *
 * Two halves, and the second is the one that matters. The first asserts what the compiler writes.
 * The second **runs the entire driver contract suite through real SQLite**, using Node's own
 * `node:sqlite` as the native database — the same statements the tablet will run, executed by a
 * real engine with a real query planner, on a real file for the durability test.
 *
 * That is as close to the device as this environment reaches. What it still does not cover, and
 * what the checkpoint's manual verification exists for, is the native binding and SQLCipher: the
 * encryption itself, the key handling inside op-sqlite, and the file on the tablet's disk.
 */

describe('the compiler writes SQL nobody has to read', () => {
  it('creates a table with its keys and defaults', () => {
    const statement = compileChange({
      kind: 'create table',
      table: 'outbox',
      primaryKey: ['event_id'],
      columns: [
        { name: 'event_id', type: 'text', notNull: true },
        { name: 'attempts', type: 'integer', notNull: true, default: 0 },
        { name: 'state', type: 'text', notNull: true, default: 'PENDING' },
      ],
    });
    expect(statement.sql).toBe(
      'CREATE TABLE IF NOT EXISTS outbox (event_id TEXT NOT NULL, ' +
        "attempts INTEGER NOT NULL DEFAULT 0, state TEXT NOT NULL DEFAULT 'PENDING', " +
        'PRIMARY KEY (event_id))',
    );
    expect(statement.params).toEqual([]);
  });

  it('binds every value, including ones that came from us', () => {
    // A payload is text somebody typed, or text an OCR read off a photograph of paper the clinic
    // did not write. There is no call site here that could put one in a statement, because the
    // compiler has no way to express it.
    const nasty = "'); DROP TABLE outbox; --";
    const statement = compileWrite({
      kind: 'insert',
      table: 'outbox',
      row: { event_id: nasty, attempts: 1 },
    });
    expect(statement.sql).toBe('INSERT INTO outbox (event_id, attempts) VALUES (?, ?)');
    expect(statement.sql).not.toContain('DROP');
    expect(statement.params).toEqual([nasty, 1]);
  });

  it('says which conflict rule it was given', () => {
    expect(
      compileWrite({ kind: 'insert', table: 'outbox', row: { a: 1 }, onConflict: 'ignore' }).sql,
    ).toContain('INSERT OR IGNORE');
    expect(
      compileWrite({ kind: 'insert', table: 'outbox', row: { a: 1 }, onConflict: 'replace' }).sql,
    ).toContain('INSERT OR REPLACE');
    expect(compileWrite({ kind: 'insert', table: 'outbox', row: { a: 1 } }).sql).toBe(
      'INSERT INTO outbox (a) VALUES (?)',
    );
  });

  it('compiles a read with every clause the queue uses', () => {
    const statement = compileQuery({
      table: 'outbox',
      columns: ['event_id', 'seq'],
      where: [
        { column: 'state', op: 'in', values: ['PENDING', 'BLOCKED_LOCAL'] },
        { column: 'next_attempt_at', op: '<=', value: 1000 },
        { column: 'batch_id', op: 'is null' },
      ],
      order: [{ column: 'seq', direction: 'asc' }],
      limit: 100,
    });
    expect(statement.sql).toBe(
      'SELECT event_id, seq FROM outbox WHERE state IN (?, ?) AND next_attempt_at <= ? ' +
        'AND batch_id IS NULL ORDER BY seq ASC LIMIT ?',
    );
    expect(statement.params).toEqual(['PENDING', 'BLOCKED_LOCAL', 1000, 100]);
  });

  it('answers an empty `in` with false rather than a syntax error', () => {
    const statement = compileQuery({
      table: 'outbox',
      where: [{ column: 'event_id', op: 'in', values: [] }],
    });
    expect(statement.sql).toBe('SELECT * FROM outbox WHERE 0');
  });

  it('refuses an identifier that is not one', () => {
    // Refused rather than quoted. Quoting makes a wrong name work; refusing makes it a failure the
    // first time it runs.
    expect(() => compileQuery({ table: 'outbox; DROP TABLE outbox' })).toThrow(LocalStoreError);
    expect(() => compileQuery({ table: 'outbox', columns: ['event_id"'] })).toThrow(
      LocalStoreError,
    );
    expect(() => compileWrite({ kind: 'update', table: 'outbox', set: { 'a = 1, b': 2 } })).toThrow(
      LocalStoreError,
    );
    expect(() => compileQuery({ table: 'outbox', limit: -1 })).toThrow(LocalStoreError);
    expect(() => compileWrite({ kind: 'update', table: 'outbox', set: {} })).toThrow(
      LocalStoreError,
    );
  });
});

// --- the same driver contract, against real SQLite ---

interface SqliteModule {
  DatabaseSync: new (path: string) => {
    prepare(sql: string): {
      all(...params: unknown[]): unknown[];
      run(...params: unknown[]): { changes: number | bigint };
    };
    close(): void;
  };
}

let sqlite: SqliteModule | null = null;
try {
  // `require` rather than `import`: Vite resolves a dynamic `import('node:sqlite')` by stripping
  // the prefix and looking for a package called `sqlite`, which is not what was asked for.
  sqlite = createRequire(import.meta.url)('node:sqlite') as SqliteModule;
} catch {
  // Node before 22.5 has no `node:sqlite`. The suite below is skipped rather than failing the
  // build: it is a second opinion about the device driver, not the thing being shipped.
  sqlite = null;
}

const directories: string[] = [];

afterAll(() => {
  for (const directory of directories) rmSync(directory, { recursive: true, force: true });
});

function nativeOpenOver(path: string): NativeOpen {
  return ({ name }): NativeDatabase => {
    if (!sqlite) throw new Error('node:sqlite is not available');
    const db = new sqlite.DatabaseSync(join(path, name));
    return {
      execute: async (sql, params = []) => {
        const statement = db.prepare(sql);
        if (/^\s*(select|pragma)/i.test(sql)) {
          return { rows: statement.all(...params) as Record<string, never>[], rowsAffected: 0 };
        }
        const result = statement.run(...params);
        return { rows: [], rowsAffected: Number(result.changes) };
      },
      close: () => db.close(),
    };
  };
}

const KEY = 'a-key-plain-sqlite-will-ignore';

async function sqliteDevice(): Promise<Device> {
  const directory = mkdtempSync(join(tmpdir(), 'dthcms-local-store-'));
  directories.push(directory);
  const open = nativeOpenOver(directory);
  const store = createSQLiteStore({ open, name: 'device.db' });
  await store.open({ key: KEY });
  return {
    store,
    key: KEY,
    reopen: async () => {
      await store.close();
      const next = createSQLiteStore({ open, name: 'device.db' });
      await next.open({ key: KEY });
      return next;
    },
  };
}

if (sqlite) {
  describeLocalStore({
    name: 'op-sqlite (through node:sqlite)',
    device: sqliteDevice,
    // Plain SQLite ignores the key; only SQLCipher enforces it, and that is the part of this
    // driver no test in this repository reaches.
    enforcesKey: false,
  });
} else {
  describe.skip('op-sqlite (through node:sqlite)', () => {
    it('needs a Node with node:sqlite', () => undefined);
  });
}
