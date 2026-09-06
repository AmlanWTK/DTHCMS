import {
  LocalStoreError,
  type Change,
  type Column,
  type Condition,
  type Query,
  type Value,
  type Write,
} from './driver';

/**
 * The only SQL in the station application (CP64).
 *
 * Every statement the device driver runs is compiled here, from an operation the rest of the app
 * describes as data. Two things follow, and both are the reason the file exists:
 *
 *   - **A value is never in the statement.** Every one is a bound parameter, without exception,
 *     including the ones that "obviously" came from us. A patient's name is text somebody typed,
 *     an OCR payload is text from a photograph of paper the clinic did not write, and a device
 *     that pulls events pulls text other devices produced. There is no call site here that could
 *     concatenate one, because the compiler has no way to express it.
 *   - **An identifier is checked, not escaped.** Table and column names come from `schema.ts` and
 *     nowhere else, so the compiler refuses anything that is not a plain lower-case identifier
 *     rather than quoting it. Quoting would make a wrong name work; refusing makes it a test
 *     failure the first time it runs.
 *
 * This is also the part of the device driver that *is* testable off a device:
 * `test/local-store-sql.test.ts` asserts the compiled text and then runs the entire driver
 * contract suite through `node:sqlite`, so the SQL is executed by a real SQLite engine before it
 * ever reaches a tablet. What that still does not exercise is the native binding and SQLCipher
 * itself — see `op-sqlite.ts`, where that is said plainly rather than implied to be covered.
 */

/** A compiled statement: text, and the values to bind to it. */
export interface Statement {
  sql: string;
  params: Value[];
}

const IDENTIFIER = /^[a-z][a-z0-9_]*$/;

function identifier(name: string): string {
  if (!IDENTIFIER.test(name)) {
    throw new LocalStoreError(
      `"${name}" is not a local identifier. Tables and columns are declared in schema.ts and are ` +
        `lower-case words; a name that needs quoting is a name that is wrong.`,
    );
  }
  return name;
}

/** A DDL literal — only ever a default from `schema.ts`, and quoted anyway. */
function literal(value: Value): string {
  if (value === null) return 'NULL';
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) throw new LocalStoreError('a default must be a finite number');
    return String(value);
  }
  return `'${value.replace(/'/g, "''")}'`;
}

function column(spec: Column): string {
  const parts = [identifier(spec.name), spec.type.toUpperCase()];
  if (spec.notNull) parts.push('NOT NULL');
  if (spec.default !== undefined) parts.push(`DEFAULT ${literal(spec.default)}`);
  return parts.join(' ');
}

/** One migration step as DDL. */
export function compileChange(change: Change): Statement {
  switch (change.kind) {
    case 'create table': {
      const columns = change.columns.map(column);
      const key = change.primaryKey.map(identifier).join(', ');
      return {
        sql:
          `CREATE TABLE IF NOT EXISTS ${identifier(change.table)} (` +
          `${columns.join(', ')}, PRIMARY KEY (${key}))`,
        params: [],
      };
    }
    case 'add column':
      return {
        sql: `ALTER TABLE ${identifier(change.table)} ADD COLUMN ${column(change.column)}`,
        params: [],
      };
    case 'create index':
      return {
        sql:
          `CREATE INDEX IF NOT EXISTS ${identifier(change.name)} ` +
          `ON ${identifier(change.table)} (${change.columns.map(identifier).join(', ')})`,
        params: [],
      };
  }
}

function conditions(where: Condition[] | undefined, params: Value[]): string {
  if (!where || where.length === 0) return '';
  const clauses = where.map((condition) => {
    const name = identifier(condition.column);
    switch (condition.op) {
      case 'is null':
        return `${name} IS NULL`;
      case 'is not null':
        return `${name} IS NOT NULL`;
      case 'in': {
        if (condition.values.length === 0) {
          // `IN ()` is a syntax error in SQLite, and "in the empty set" is false. Said as `0`
          // rather than left to the caller to remember.
          return '0';
        }
        params.push(...condition.values);
        return `${name} IN (${condition.values.map(() => '?').join(', ')})`;
      }
      default:
        params.push(condition.value);
        return `${name} ${condition.op} ?`;
    }
  });
  return ` WHERE ${clauses.join(' AND ')}`;
}

/** A read. */
export function compileQuery(query: Query): Statement {
  const params: Value[] = [];
  const columns = query.columns ? query.columns.map(identifier).join(', ') : '*';
  let sql = `SELECT ${columns} FROM ${identifier(query.table)}`;
  sql += conditions(query.where, params);
  if (query.order && query.order.length > 0) {
    const order = query.order
      .map((one) => `${identifier(one.column)} ${one.direction === 'desc' ? 'DESC' : 'ASC'}`)
      .join(', ');
    sql += ` ORDER BY ${order}`;
  }
  if (query.limit !== undefined) {
    if (!Number.isInteger(query.limit) || query.limit < 0) {
      throw new LocalStoreError('a limit is a whole number of rows');
    }
    sql += ' LIMIT ?';
    params.push(query.limit);
  }
  return { sql, params };
}

/** A write. */
export function compileWrite(write: Write): Statement {
  const params: Value[] = [];
  switch (write.kind) {
    case 'insert': {
      const columns = Object.keys(write.row).map(identifier);
      const values = Object.values(write.row);
      params.push(...values);
      const conflict =
        write.onConflict === 'ignore'
          ? ' OR IGNORE'
          : write.onConflict === 'replace'
            ? ' OR REPLACE'
            : '';
      return {
        sql:
          `INSERT${conflict} INTO ${identifier(write.table)} (${columns.join(', ')}) ` +
          `VALUES (${values.map(() => '?').join(', ')})`,
        params,
      };
    }
    case 'update': {
      const assignments = Object.entries(write.set).map(([name, value]) => {
        params.push(value);
        return `${identifier(name)} = ?`;
      });
      if (assignments.length === 0) throw new LocalStoreError('an update sets something');
      let sql = `UPDATE ${identifier(write.table)} SET ${assignments.join(', ')}`;
      sql += conditions(write.where, params);
      return { sql, params };
    }
    case 'delete': {
      let sql = `DELETE FROM ${identifier(write.table)}`;
      sql += conditions(write.where, params);
      return { sql, params };
    }
  }
}
