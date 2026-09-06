import { META_KEYS, TABLES, forgetDatabaseKey, type LocalStore } from '@/lib/local-store';

import { readCounts, readOutbox, writeMeta } from './outbox';

/**
 * What sign-in, sign-out and revocation do to the local record (CP64, §13.8).
 *
 * # The rule §13.8 states, and the one thing it must not be read to mean
 *
 * *"Immediate local wipe on logout, device revocation, or N consecutive auth failures."* Read
 * literally, that instruction deletes undelivered clinical measurements — and the situations it
 * names are exactly the ones in which undelivered measurements are most likely to exist. A tablet
 * that has been offline all morning is revoked at nine; an operator signs out at the end of a
 * shift while the queue is still draining. Deleting the queue there is the silent loss the whole
 * offline design exists to prevent, arriving through the security control rather than through a
 * bug.
 *
 * So a wipe here means: **everything readable goes, and undelivered work stays, encrypted.**
 *
 *   - projections, cached events, the reference cache and the sync log are deleted, so nothing
 *     about any patient can be read on this device by whoever picks it up next;
 *   - the outbox is kept, because it is the only copy of work the clinic has never received;
 *   - the key is *not* deleted, because deleting it would make the outbox unrecoverable, which is
 *     the same loss by another route. It leaves memory instead, so nothing can be read until
 *     somebody authenticates again.
 *
 * `wipeEverything` is the other case, and it is deliberately a separate function with a count in
 * its answer: a person who genuinely wants this tablet emptied is told how many entries that
 * destroys, and CP67's screen is where they are asked.
 *
 * # The cursor is reset, and that is not free
 *
 * A wipe deletes the pulled events, so the pull cursor no longer describes what this device holds.
 * Keeping it would leave the device permanently missing everything it had already downloaded —
 * patients whose names would simply not appear, with no error anywhere. Resetting it means the
 * next sync re-downloads, which is slow on a device that has been in service a long time. Slow is
 * the right side to be wrong on; the size of that download is a real open question, and
 * `GET /v1/sync/state` exists on the server for the opposite case (a device that was wiped and
 * wants to resume) — see the report accompanying this checkpoint.
 */

export type SessionPhase = 'unknown' | 'anonymous' | 'authenticated';
export type LifecycleAction = 'unlock' | 'lock' | 'nothing';

/**
 * What a change of session means for the database.
 *
 * The key is released only on the transition *into* an authenticated session, which is the plan's
 * "released only after authentication" as a function rather than as an intention.
 */
export function lifecycleFor(previous: SessionPhase, next: SessionPhase): LifecycleAction {
  if (next === 'authenticated') return previous === 'authenticated' ? 'nothing' : 'unlock';
  if (next === 'anonymous' && previous === 'authenticated') return 'lock';
  return 'nothing';
}

export interface WipeReport {
  /** How many entries had not reached the clinic when the wipe ran. */
  undelivered: number;
  /** Whether those entries were kept. False only for a deliberate, total wipe. */
  keptQueue: boolean;
  keyForgotten: boolean;
}

/** The tables that hold something a person could read about a patient. */
const READABLE = [TABLES.projections, TABLES.localEvents, TABLES.referenceCache, TABLES.syncLog];

/**
 * Sign-out, revocation, and the failed-authentication case.
 *
 * Leaves nothing readable and loses nothing undelivered.
 */
export async function wipeAfterSignOut(store: LocalStore): Promise<WipeReport> {
  const undelivered = (await readCounts(store)).total;

  await store.transaction(async (tx) => {
    for (const table of READABLE) await tx.run({ kind: 'delete', table });
    // The events behind undelivered rows are not lost with `local_events`: the outbox row carries
    // the whole event, which is what gets sent. What is lost is the local *history* view, which is
    // a cache of the clinic's record and is re-pulled.
    await writeMeta(tx, META_KEYS.cursor, 0);
  });

  return { undelivered, keptQueue: true, keyForgotten: false };
}

/**
 * The word that has to be said out loud to destroy undelivered clinical work.
 *
 * A required argument rather than a boolean flag, and spelled as a sentence, so that no call site
 * reaches this function by autocomplete, by copying the line above it, or in a hurry. This is the
 * only function in the offline system that can lose a measurement.
 */
export const DESTROYS_UNDELIVERED_WORK = 'destroys-undelivered-work' as const;

/**
 * Empty the tablet, including anything it has not delivered, and destroy the key.
 *
 * Irreversible, and **the only function here that can lose a clinical measurement** — which is why
 * it is a separate function with a separate name and a required confirmation, rather than an option
 * on `wipeAfterSignOut`. A flag would eventually be passed by something that meant "tidy up".
 *
 * It exists because "this tablet was stolen and has been recovered" and "this tablet is being
 * handed to another clinic" are real, and because the alternative — nobody being able to do it —
 * leads to somebody doing it with a factory reset and no record of what was destroyed. The count
 * comes back so whoever asked can be told what it cost, and CP67's screen is where they are asked.
 */
export async function wipeEverything(
  store: LocalStore,
  confirm: typeof DESTROYS_UNDELIVERED_WORK,
): Promise<WipeReport> {
  if (confirm !== DESTROYS_UNDELIVERED_WORK) {
    throw new Error('wipeEverything destroys undelivered clinical work and must be confirmed');
  }
  return wipeEverythingConfirmed(store);
}

async function wipeEverythingConfirmed(store: LocalStore): Promise<WipeReport> {
  const undelivered = (await readCounts(store)).total;
  await store.wipe();
  await forgetDatabaseKey();
  return { undelivered, keptQueue: false, keyForgotten: true };
}

/**
 * The cached-patient TTL (§13.8).
 *
 * Seven days beyond the day itself, which is the plan's default and is **unapproved** — it is an
 * operational question about how far back a station is asked to look, and the answer belongs to
 * Dr. Nahid rather than to this file.
 */
export const CACHE_TTL_DAYS = 7;

/**
 * Purge what is no longer needed.
 *
 * Two rules, and the second is the one that matters: nothing is purged if it is still undelivered.
 * A patient whose visit was ten days ago and whose weight is still in this device's outbox keeps
 * every trace of that weight until the clinic has it.
 */
export async function purgeExpired(
  store: LocalStore,
  options: { now: number; ttlDays?: number },
): Promise<number> {
  const ttl = (options.ttlDays ?? CACHE_TTL_DAYS) * 24 * 60 * 60 * 1000;
  const cutoff = new Date(options.now - ttl).toISOString();
  const queued = new Set((await readOutbox(store)).map((row) => row.eventId));

  const stale = await store.all({
    table: TABLES.localEvents,
    where: [{ column: 'occurred_at', op: '<', value: cutoff }],
  });
  const removable = stale
    .map((row) => String(row.event_id))
    .filter((eventId) => !queued.has(eventId));
  if (removable.length === 0) return 0;

  await store.transaction(async (tx) => {
    await tx.run({
      kind: 'delete',
      table: TABLES.localEvents,
      where: [{ column: 'event_id', op: 'in', values: removable }],
    });
    await tx.run({
      kind: 'delete',
      table: TABLES.projections,
      where: [
        { column: 'occurred_at', op: '<', value: cutoff },
        { column: 'confirmed', op: '=', value: 1 },
      ],
    });
  });
  return removable.length;
}
