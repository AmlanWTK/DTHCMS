import { readProjection, readProjections, type Executor } from '@/lib/sync';
import { TABLES } from '@/lib/local-store';

import type { QueueRow, QueueStatus } from './state';

/**
 * The station's queue, from the local record (CP64 criterion 3).
 *
 * The queue is the first thing a station screen asks for and the last thing it should need a
 * network for: an operator standing in front of a patient with the cuff already on their arm is
 * the worst possible moment to discover the Wi-Fi has gone. The events behind it — entered,
 * called, left — are pullable (`internal/offline/pullable.go`), so a device that has synced this
 * morning can answer the question itself.
 *
 * **Stale by design, and §13.7 says so**: "local queue/traffic view (may be stale)". A queue is a
 * statement about a moment, and a device that has been in a corridor for ten minutes holds a
 * ten-minute-old one. What it must never be is *absent*, which is what a network read gives you
 * in the corridor.
 */

const STATUSES: Record<string, QueueStatus> = {
  waiting: 'waiting',
  called: 'called',
  in_service: 'in_service',
  done: 'done',
  skipped: 'skipped',
  rerouted: 'rerouted',
  left: 'done',
};

function text(document: Record<string, unknown>, field: string): string {
  const value = document[field];
  return typeof value === 'string' ? value : '';
}

function count(document: Record<string, unknown>, field: string): number {
  const value = document[field];
  return typeof value === 'number' ? value : Number(value ?? 0);
}

/**
 * Everybody this device knows to be in one station's queue, newest state per entry.
 *
 * `waited_seconds` is computed here rather than carried, because the number the operator reads
 * has to keep moving while the tablet is offline — a wait that froze at the moment of the last
 * sync would be a number that quietly lies for as long as the connection is down.
 */
export async function readStationQueue(
  store: Executor,
  station: string,
  now: number,
): Promise<QueueRow[]> {
  const rows = await store.all({
    table: TABLES.projections,
    where: [{ column: 'kind', op: '=', value: 'queue' }],
  });

  const out: QueueRow[] = [];
  for (const row of rows) {
    const document = JSON.parse(String(row.document)) as Record<string, unknown>;
    if (text(document, 'station_code') !== station) continue;
    const enteredAt = text(document, 'entered_at');
    const entered = Date.parse(enteredAt);
    out.push({
      id: text(document, 'id'),
      visit_id: text(document, 'visit_id'),
      patient_id: text(document, 'patient_id'),
      station_code: station,
      position: count(document, 'position'),
      status: STATUSES[text(document, 'status')] ?? 'waiting',
      priority: count(document, 'priority'),
      ...(text(document, 'priority_reason')
        ? { priority_reason: text(document, 'priority_reason') }
        : {}),
      entered_at: enteredAt,
      waited_seconds: Number.isFinite(entered)
        ? Math.max(0, Math.round((now - entered) / 1000))
        : 0,
    });
  }
  return out;
}

/** The person a station is working with, or null when this device has not been told about one. */
export async function inServicePatient(
  store: Executor,
  station: string,
  now: number,
): Promise<QueueRow | null> {
  const rows = await readStationQueue(store, station, now);
  return rows.find((row) => row.status === 'in_service') ?? null;
}

export interface LocalPatient {
  id: string;
  name: string;
  sex: 'male' | 'female' | 'other';
  ageYears: number;
}

/**
 * Who a patient is, from the local record.
 *
 * The name in Bangla when there is one, which is the same rule the station screens already
 * follow — and the age computed here from the birth date rather than carried, because an age
 * that was correct at the last sync is wrong on the patient's birthday and nobody would notice.
 */
export async function readLocalPatient(
  store: Executor,
  patientId: string,
  now: number,
): Promise<LocalPatient | null> {
  const projection = await readProjection(store, 'patient', patientId);
  if (!projection) return null;
  const document = projection.document;
  const nameBN = text(document, 'name_bn');
  const nameEN = text(document, 'name_en');
  const sex = text(document, 'sex');
  return {
    id: patientId,
    name: nameBN || nameEN,
    sex: sex === 'male' || sex === 'female' ? sex : 'other',
    ageYears: ageInYears(text(document, 'birth_date'), now),
  };
}

/** Whole years between a birth date and now. Zero when the record does not say. */
export function ageInYears(birthDate: string, now: number): number {
  const born = new Date(birthDate);
  if (Number.isNaN(born.getTime())) return 0;
  const today = new Date(now);
  let years = today.getUTCFullYear() - born.getUTCFullYear();
  const monthDiff = today.getUTCMonth() - born.getUTCMonth();
  if (monthDiff < 0 || (monthDiff === 0 && today.getUTCDate() < born.getUTCDate())) years -= 1;
  return Math.max(0, years);
}

/** Everything this device holds about one patient's values, for a station's comparison lines. */
export async function readLocalObservations(store: Executor, patientId: string) {
  return readProjections(store, patientId, 'observation');
}
