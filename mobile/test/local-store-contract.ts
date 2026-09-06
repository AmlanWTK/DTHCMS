import { describe, expect, it } from 'vitest';

import {
  LocalStoreLockedError,
  type LocalStore,
  type Migration,
} from '../src/lib/local-store/driver';

// Imported from the driver rather than from the barrel, and deliberately: the barrel reaches
// `key.ts`, which reaches `expo-secure-store`, whose published source is Flow and which the test
// runner cannot parse. A suite about SQL should not need a keystore mock to run.

/**
 * The driver contract, once, for both implementations (CP64).
 *
 * `memory.ts` and `op-sqlite.ts` are two implementations of one interface, and the whole offline
 * system is tested against the first — so the only thing that makes those tests mean anything on a
 * device is that the two behave identically. This file is that claim, written down: it runs
 * against the in-memory driver and, through `node:sqlite`, against the real SQL the device driver
 * generates.
 *
 * What it cannot reach is stated where it lives (`op-sqlite.ts`): the native binding and SQLCipher
 * itself need a prebuild and a tablet.
 */

export interface Device {
  store: LocalStore;
  /** The same device, a new store object — an app kill and a relaunch. */
  reopen(): Promise<LocalStore>;
  key: string;
}

export interface Harness {
  name: string;
  device(): Promise<Device>;
  /** True when a wrong key is refused. SQLCipher does; plain SQLite under `node:sqlite` cannot. */
  enforcesKey: boolean;
  cleanup?(): Promise<void>;
}

const first: Migration = {
  version: 1,
  name: 'readings',
  changes: [
    {
      kind: 'create table',
      table: 'reading',
      primaryKey: ['id'],
      columns: [
        { name: 'id', type: 'text', notNull: true },
        { name: 'patient', type: 'text' },
        { name: 'value', type: 'real' },
        { name: 'taken_at', type: 'text', notNull: true },
        { name: 'state', type: 'text', notNull: true, default: 'new' },
      ],
    },
    { kind: 'create index', name: 'reading_by_patient', table: 'reading', columns: ['patient'] },
  ],
};

const second: Migration = {
  version: 2,
  name: 'a note beside the reading',
  changes: [{ kind: 'add column', table: 'reading', column: { name: 'note', type: 'text' } }],
};

function reading(id: string, over: Record<string, string | number | null> = {}) {
  return { id, patient: 'p1', value: 70, taken_at: '2026-09-05T08:00:00Z', state: 'new', ...over };
}

export function describeLocalStore(harness: Harness): void {
  describe(`${harness.name}: opening`, () => {
    it('refuses to open without a key', async () => {
      const device = await harness.device();
      await device.store.close();
      // Acceptance criterion 1. A driver that fell back to an unencrypted database when the
      // keystore was unavailable would produce a tablet full of readable PHI that behaves exactly
      // like a working one.
      await expect(device.store.open({ key: '' })).rejects.toBeInstanceOf(LocalStoreLockedError);
      expect(device.store.isOpen).toBe(false);
    });

    it('refuses every read and write while closed', async () => {
      const device = await harness.device();
      await device.store.close();
      await expect(device.store.all({ table: 'reading' })).rejects.toBeInstanceOf(
        LocalStoreLockedError,
      );
    });

    if (harness.enforcesKey) {
      it('refuses the wrong key', async () => {
        const device = await harness.device();
        await device.store.close();
        await expect(device.store.open({ key: 'a-different-key' })).rejects.toBeInstanceOf(
          LocalStoreLockedError,
        );
      });
    }
  });

  describe(`${harness.name}: migrations`, () => {
    it('applies each version once, however often it is asked', async () => {
      const device = await harness.device();
      expect(await device.store.migrate([first])).toBe(1);
      expect(await device.store.migrate([first])).toBe(0);
      expect(await device.store.migrate([first, second])).toBe(1);
      expect(await device.store.migrate([first, second])).toBe(0);
    });

    it('brings a device that missed a release up to date in order', async () => {
      // The tablet that was in a drawer for two versions. It runs what it missed and nothing else.
      const device = await harness.device();
      await device.store.migrate([first]);
      await device.store.run({ kind: 'insert', table: 'reading', row: reading('r1') });
      expect(await device.store.migrate([first, second])).toBe(1);
      const rows = await device.store.all({ table: 'reading' });
      expect(rows[0]?.note ?? null).toBeNull();
    });
  });

  describe(`${harness.name}: reads and writes`, () => {
    it('filters, orders and limits', async () => {
      const device = await harness.device();
      await device.store.migrate([first]);
      await device.store.run([
        { kind: 'insert', table: 'reading', row: reading('a', { value: 70 }) },
        { kind: 'insert', table: 'reading', row: reading('b', { value: 90 }) },
        { kind: 'insert', table: 'reading', row: reading('c', { value: 80, patient: 'p2' }) },
      ]);

      const mine = await device.store.all({
        table: 'reading',
        where: [{ column: 'patient', op: '=', value: 'p1' }],
        order: [{ column: 'value', direction: 'desc' }],
      });
      expect(mine.map((row) => row.id)).toEqual(['b', 'a']);

      const one = await device.store.all({
        table: 'reading',
        order: [{ column: 'id', direction: 'asc' }],
        limit: 1,
      });
      expect(one).toHaveLength(1);

      const heavy = await device.store.all({
        table: 'reading',
        where: [{ column: 'value', op: '>=', value: 80 }],
        order: [{ column: 'id', direction: 'asc' }],
      });
      expect(heavy.map((row) => row.id)).toEqual(['b', 'c']);
    });

    it('answers `in` over the empty set with nothing rather than an error', async () => {
      // The queue asks this every time it is empty, and `IN ()` is a syntax error in SQLite.
      const device = await harness.device();
      await device.store.migrate([first]);
      await device.store.run({ kind: 'insert', table: 'reading', row: reading('a') });
      expect(
        await device.store.all({
          table: 'reading',
          where: [{ column: 'id', op: 'in', values: [] }],
        }),
      ).toEqual([]);
    });

    it('treats null the way SQL does', async () => {
      const device = await harness.device();
      await device.store.migrate([first]);
      await device.store.run([
        { kind: 'insert', table: 'reading', row: reading('a', { patient: null }) },
        { kind: 'insert', table: 'reading', row: reading('b') },
      ]);
      const missing = await device.store.all({
        table: 'reading',
        where: [{ column: 'patient', op: 'is null' }],
      });
      expect(missing.map((row) => row.id)).toEqual(['a']);
      const present = await device.store.all({
        table: 'reading',
        where: [{ column: 'patient', op: 'is not null' }],
      });
      expect(present.map((row) => row.id)).toEqual(['b']);
      // A comparison against null is never true, which is what both drivers must agree about.
      const compared = await device.store.all({
        table: 'reading',
        where: [{ column: 'patient', op: '>', value: 'a' }],
      });
      expect(compared.map((row) => row.id)).toEqual(['b']);
    });

    it('fails, ignores or replaces on a duplicate key, as asked', async () => {
      const device = await harness.device();
      await device.store.migrate([first]);
      await device.store.run({
        kind: 'insert',
        table: 'reading',
        row: reading('a', { value: 70 }),
      });

      await expect(
        device.store.run({ kind: 'insert', table: 'reading', row: reading('a', { value: 71 }) }),
      ).rejects.toBeTruthy();

      // `ignore` is what makes reconciliation idempotent: an event this device already has lands
      // again and changes nothing.
      await device.store.run({
        kind: 'insert',
        table: 'reading',
        row: reading('a', { value: 72 }),
        onConflict: 'ignore',
      });
      let rows = await device.store.all({ table: 'reading' });
      expect(rows[0]?.value).toBe(70);

      await device.store.run({
        kind: 'insert',
        table: 'reading',
        row: reading('a', { value: 73 }),
        onConflict: 'replace',
      });
      rows = await device.store.all({ table: 'reading' });
      expect(rows).toHaveLength(1);
      expect(rows[0]?.value).toBe(73);
    });

    it('updates and deletes only what the condition names', async () => {
      const device = await harness.device();
      await device.store.migrate([first]);
      await device.store.run([
        { kind: 'insert', table: 'reading', row: reading('a') },
        { kind: 'insert', table: 'reading', row: reading('b', { patient: 'p2' }) },
      ]);
      const changed = await device.store.run({
        kind: 'update',
        table: 'reading',
        set: { state: 'sent' },
        where: [{ column: 'patient', op: '=', value: 'p1' }],
      });
      expect(changed).toBe(1);
      const rows = await device.store.all({
        table: 'reading',
        order: [{ column: 'id', direction: 'asc' }],
      });
      expect(rows.map((row) => row.state)).toEqual(['sent', 'new']);

      await device.store.run({
        kind: 'delete',
        table: 'reading',
        where: [{ column: 'id', op: '=', value: 'a' }],
      });
      expect(await device.store.all({ table: 'reading' })).toHaveLength(1);
    });
  });

  describe(`${harness.name}: transactions`, () => {
    it('commits everything together', async () => {
      const device = await harness.device();
      await device.store.migrate([first]);
      await device.store.transaction(async (tx) => {
        await tx.run({ kind: 'insert', table: 'reading', row: reading('a') });
        await tx.run({ kind: 'insert', table: 'reading', row: reading('b') });
        // A read inside the transaction sees the transaction's own writes; otherwise the command
        // handler could not read the sequence number it just bumped.
        expect(await tx.all({ table: 'reading' })).toHaveLength(2);
      });
      expect(await device.store.all({ table: 'reading' })).toHaveLength(2);
    });

    it('leaves nothing behind when it fails half way', async () => {
      const device = await harness.device();
      await device.store.migrate([first]);
      await device.store.run({ kind: 'insert', table: 'reading', row: reading('kept') });
      await expect(
        device.store.transaction(async (tx) => {
          await tx.run({ kind: 'insert', table: 'reading', row: reading('a') });
          throw new Error('the disk went away');
        }),
      ).rejects.toThrow('the disk went away');
      const rows = await device.store.all({ table: 'reading' });
      expect(rows.map((row) => row.id)).toEqual(['kept']);
    });

    it('refuses to nest', async () => {
      const device = await harness.device();
      await device.store.migrate([first]);
      await expect(
        device.store.transaction(async () => {
          await device.store.transaction(async () => undefined);
        }),
      ).rejects.toThrow(/nest/);
    });
  });

  describe(`${harness.name}: durability`, () => {
    it('keeps committed rows across a restart and loses uncommitted ones', async () => {
      // §13.10: kill the app with a full queue and relaunch. The store object is thrown away and
      // a new one is opened over the same device.
      const device = await harness.device();
      await device.store.migrate([first]);
      await device.store.run({ kind: 'insert', table: 'reading', row: reading('committed') });
      await expect(
        device.store.transaction(async (tx) => {
          await tx.run({ kind: 'insert', table: 'reading', row: reading('never') });
          throw new Error('killed');
        }),
      ).rejects.toThrow();

      const reopened = await device.reopen();
      const rows = await reopened.all({ table: 'reading' });
      expect(rows.map((row) => row.id)).toEqual(['committed']);
      // The migrations are not re-run against a database that already has them.
      expect(await reopened.migrate([first])).toBe(0);
    });
  });

  describe(`${harness.name}: wiping`, () => {
    it('empties every table and keeps the schema', async () => {
      const device = await harness.device();
      await device.store.migrate([first]);
      await device.store.run({ kind: 'insert', table: 'reading', row: reading('a') });
      await device.store.wipe();
      expect(await device.store.all({ table: 'reading' })).toEqual([]);
      // Still usable afterwards: a wipe is not a reinstall.
      await device.store.run({ kind: 'insert', table: 'reading', row: reading('b') });
      expect(await device.store.all({ table: 'reading' })).toHaveLength(1);
    });
  });
}
