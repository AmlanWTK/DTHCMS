import { TABLES } from '../src/lib/local-store/schema';

import type { LocalStore } from '../src/lib/local-store/driver';
import type { FakeClinic } from './fake-sync-server';

/**
 * The data-integrity check (CP68 acceptance criterion 2, brought forward).
 *
 * After a run — a clean one, a lossy one, a soak — the device and the clinic are compared **event
 * for event**, and every way they could disagree is a named problem rather than a vague count.
 * This is the check that would notice the failure this checkpoint is most afraid of: not an error,
 * not a crash, but two records that quietly differ.
 *
 * The six things it looks for:
 *
 *  1. **A measurement that is nowhere.** Every event this device recorded is in the ledger, in the
 *     clinic's quarantine, or still in this device's outbox. Zero data loss is this sentence.
 *  2. **A measurement recorded twice.** Two ledger rows with different ids but the same aggregate,
 *     type, moment and payload. That is what a client which regenerated `event_id` on retry
 *     produces, and nothing else in the system would ever notice it.
 *  3. **A value that changed on the way.** The payload and `occurred_at` on the device and in the
 *     ledger are identical, byte for byte.
 *  4. **A false confirmation.** Anything this device believes the clinic has, the clinic has.
 *  5. **A missed confirmation**, when the run is supposed to have finished: nothing still queued
 *     that the ledger already holds.
 *  6. **A projection built from an event nobody has.**
 */

export interface IntegrityOptions {
  /** Assert that the run finished: nothing left in flight, nothing queued that already landed. */
  quiescent?: boolean;
}

export async function integrityProblems(
  store: LocalStore,
  clinic: FakeClinic,
  options: IntegrityOptions = {},
): Promise<string[]> {
  const problems: string[] = [];

  const localEvents = await store.all({ table: TABLES.localEvents });
  const outbox = await store.all({ table: TABLES.outbox });
  const projections = await store.all({ table: TABLES.projections });

  const localById = new Map(localEvents.map((row) => [String(row.event_id), row]));
  const outboxById = new Map(outbox.map((row) => [String(row.event_id), row]));
  const heldById = new Set(clinic.held.map((row) => row.event_id));

  // 1. Nothing is nowhere.
  const mine = new Set<string>([
    ...localEvents
      .filter((row) => String(row.origin) === 'LOCAL')
      .map((row) => String(row.event_id)),
    ...outboxById.keys(),
  ]);
  for (const eventId of mine) {
    const somewhere =
      clinic.ledger.has(eventId) || heldById.has(eventId) || outboxById.has(eventId);
    if (!somewhere) problems.push(`${eventId}: recorded on the device and nowhere else`);
  }

  // 2. The same measurement, twice, under two ids.
  const byContent = new Map<string, string[]>();
  for (const row of clinic.ledger.values()) {
    const key = [
      row.aggregate_type,
      row.aggregate_id,
      row.event_type,
      row.occurred_at,
      JSON.stringify(row.payload),
    ].join('|');
    byContent.set(key, [...(byContent.get(key) ?? []), row.event_id]);
  }
  for (const [key, ids] of byContent) {
    if (ids.length > 1) {
      problems.push(
        `the ledger holds the same measurement ${ids.length} times (${key}): ${ids.join(', ')}`,
      );
    }
  }

  // 3. Nothing changed on the way.
  for (const row of clinic.ledger.values()) {
    const local = localById.get(row.event_id) ?? outboxById.get(row.event_id);
    if (!local) continue;
    if (String(local.occurred_at) !== row.occurred_at) {
      problems.push(
        `${row.event_id}: occurred_at is ${String(local.occurred_at)} here and ${row.occurred_at} at the clinic`,
      );
    }
    const localPayload = JSON.stringify(JSON.parse(String(local.payload)));
    if (localPayload !== JSON.stringify(row.payload)) {
      problems.push(`${row.event_id}: the payload differs between the device and the clinic`);
    }
  }

  // 4. Nothing is confirmed that is not there.
  for (const row of localEvents) {
    if (row.global_seq === null || row.global_seq === undefined) continue;
    const stored = clinic.ledger.get(String(row.event_id));
    if (!stored) {
      problems.push(
        `${String(row.event_id)}: the device says it synced and the clinic has never seen it`,
      );
      continue;
    }
    if (Number(row.global_seq) !== stored.global_seq) {
      problems.push(
        `${String(row.event_id)}: sequence ${String(row.global_seq)} here, ${stored.global_seq} there`,
      );
    }
  }

  // 5. When the run is finished, nothing queued has already landed.
  if (options.quiescent) {
    for (const row of outbox) {
      const state = String(row.state);
      const eventId = String(row.event_id);
      if (state === 'IN_FLIGHT') {
        problems.push(`${eventId}: still in flight after the run finished`);
      }
      if ((state === 'PENDING' || state === 'BLOCKED_LOCAL') && clinic.ledger.has(eventId)) {
        problems.push(`${eventId}: queued for sending and already in the ledger`);
      }
    }
  }

  // 6. Every projection names an event this device holds.
  for (const row of projections) {
    if (!localById.has(String(row.event_id)) && !outboxById.has(String(row.event_id))) {
      problems.push(
        `projection ${String(row.kind)}/${String(row.key)} was built from an event this device does not hold`,
      );
    }
  }

  return problems;
}
