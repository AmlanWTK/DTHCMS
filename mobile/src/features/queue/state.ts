/**
 * My station's queue, as the screen holds it (CP39).
 *
 * The logic here is separated from the components for the same reason CP33's registration
 * was: it is the part worth testing, and Expo cannot run in the environment the tests do.
 */

export type QueueStatus = 'waiting' | 'called' | 'in_service' | 'done' | 'skipped' | 'rerouted';

export interface QueueEntry {
  id: string;
  visit_id: string;
  patient_id: string;
  station_code: string;
  position: number;
  status: QueueStatus;
  priority: number;
  priority_reason?: string;
  entered_at: string;
  called_at?: string;
  waited_seconds: number;
}

/** What the operator is shown about the person, alongside the queue entry. */
export interface QueuePerson {
  clinical_id: string;
  name_en: string;
  name_bn: string;
  age: number;
  sex: string;
}

export type QueueRow = QueueEntry & { person?: QueuePerson };

/**
 * The order the queue is called in: priority first, then arrival.
 *
 * Mirrored from the server's index rather than trusted from the response, because a station
 * screen that reorders on a stale render is a station screen that calls the wrong person —
 * and the operator has no way to tell.
 */
export function callOrder(rows: QueueRow[]): QueueRow[] {
  return [...rows].sort((a, b) => {
    if (a.priority !== b.priority) return b.priority - a.priority;
    if (a.entered_at !== b.entered_at) return a.entered_at < b.entered_at ? -1 : 1;
    return a.id < b.id ? -1 : 1;
  });
}

/** Whoever is next, or nothing. */
export function nextUp(rows: QueueRow[]): QueueRow | null {
  return callOrder(rows).find((row) => row.status === 'waiting') ?? null;
}

/** Who the operator has already called and not yet seen. */
export function called(rows: QueueRow[]): QueueRow[] {
  return callOrder(rows).filter((row) => row.status === 'called');
}

export function waiting(rows: QueueRow[]): QueueRow[] {
  return callOrder(rows).filter((row) => row.status === 'waiting');
}

/**
 * A waiting time as a person reads it at a glance.
 *
 * Minutes, not "2m 13s": nobody at a station acts on thirteen seconds, and the extra
 * precision is what makes the number hard to read across a room. Under a minute is "just
 * now" rather than "0 min", because "0" reads as a bug.
 */
export function waitedLabel(seconds: number): string {
  if (seconds < 60) return 'just now';
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} min`;
  const hours = Math.floor(minutes / 60);
  const rest = minutes % 60;
  return rest === 0 ? `${hours} hr` : `${hours} hr ${rest} min`;
}

/**
 * How alarming a wait is, for the colour of the chip.
 *
 * The thresholds are deliberately generous — twenty minutes is a normal wait at a busy
 * clinic — because a screen where everything is red is a screen nobody looks at.
 */
export function waitTone(seconds: number): 'normal' | 'borderline' | 'high' {
  if (seconds >= 60 * 60) return 'high';
  if (seconds >= 30 * 60) return 'borderline';
  return 'normal';
}

/** Whether this entry jumped the queue, and why. */
export function isPriority(row: QueueRow): boolean {
  return row.priority > 0;
}
