import type { LocalStore } from '@/lib/local-store';

import { issueWithin, type Command, type CommandDeps } from './commands';
import { appendSyncLog, dropFromOutbox, readOutboxRow, updateOutbox } from './outbox';
import { REASON_CODES, type OutboxRow } from './state';

/**
 * What a person does about an entry the clinic refused (CP67, §13.5, §13.6).
 *
 * # Why there is a file here at all
 *
 * CP66 left a `NEEDS_ATTENTION` row as something an operator could look at and nothing else, and
 * that is not a failure ladder — it is the bottom rung with no way off it. A rejected event is a
 * real clinical measurement, taken from a real patient, that the clinic has said it will not
 * accept as sent. Somebody has to do something, and there are exactly two somethings:
 *
 *   - **correct it** — the refusal is about the entry, the person who made the entry is holding
 *     the tablet, and they can restate it. A new event, a new id, the same moment;
 *   - **escalate it** — nothing on this tablet answers this refusal, and pretending otherwise
 *     would leave a measurement sitting under a button that cannot help.
 *
 * # Correcting writes a new event and never reuses the old one's id
 *
 * The most important line in this file is the one that calls `ids.newId()`. `event_id` is the
 * ledger's key, and a rejected event has no ledger row — so resending the refused id with a
 * different payload would put one key over two different clinical facts, one refused and one
 * accepted, in a system whose entire audit story is that the key identifies the fact. The
 * correction is a new event that *says* which one it replaces (`corrects_event_id`), which is a
 * claim the record can carry rather than a rewrite it cannot detect.
 *
 * # The corrected event is dated now, and the measurement is not
 *
 * The two timestamps come apart here, exactly as `vitalsCommands` says they can: the envelope's
 * `occurred_at` is when the entry was written down, and the payload's `effective_at` is when the
 * value was true. A correction typed at 09:40 about a blood pressure taken at 09:05 keeps 09:05 in
 * the payload — that is the clinical fact and nothing here may move it — and carries 09:40 on the
 * envelope, because that is when this statement was made.
 *
 * That is also the only thing that makes the correction *win*. §13.6 settles two values for one
 * patient and code by `occurred_at`, and breaks a tie on `event_id` — which is to say, by a coin
 * flip. A correction that copied the refused envelope's time would be in a tie with the value it
 * is correcting, and roughly half of them would leave the wrong number on the screen with nothing
 * anywhere reporting a problem. Dating the envelope now makes `replaces()` deterministic in the
 * one direction a correction can possibly mean.
 *
 * # Escalating does not send anything, and says so
 *
 * There is no route on the wire that carries a rejected event to a person — the quarantine holds
 * what the clinic *chose* to hold, and a rejection is the clinic declining to hold it. So
 * escalating moves nothing and tells nobody at the clinic. What it does is record that the
 * operator has read the refusal and handed it on, which stops the tablet asking them again for
 * something they have already done. The copy on the screen carries the other half, and it has to:
 * **the entry exists on this tablet and nowhere else**, so "escalate" means "find your supervisor
 * and show them this device", not "sent for review". A button that implied the second would be
 * this checkpoint's own headline failure — an indicator that lies — wearing a helpful face.
 */

/** The states that mean a person, rather than the engine, is next to act. */
export const ATTENTION_STATES = ['NEEDS_ATTENTION', 'ESCALATED', 'HELD'] as const;

/** The states an operator holding this tablet can actually do something about. */
export const OPERATOR_ACTIONABLE_STATES = ['NEEDS_ATTENTION', 'ESCALATED'] as const;

/**
 * A short, sayable name for one entry.
 *
 * An operator escalating an entry has to be able to tell a supervisor *which* one, out loud,
 * across a room, and `event_id` is a 36-character uuid. Eight hex digits in two groups is what a
 * person can read off a screen and what the supervisor can match against the same screen a minute
 * later; among the handful of refused entries one tablet ever holds it is unambiguous, and it is
 * deliberately not claimed to be unique beyond that.
 *
 * Opaque by construction: an id this device generated, carrying nothing about a patient, a value
 * or an operator. Nothing that goes on a screen for somebody else to read may carry more.
 */
export function referenceOf(eventId: string): string {
  const compact = eventId.replace(/-/g, '').toUpperCase();
  if (compact.length < 8) return compact;
  return `${compact.slice(0, 4)}-${compact.slice(4, 8)}`;
}

// --- correcting ---

/** The corrected measurement, as the operator retyped it. */
export interface Correction {
  value: number;
  /** The unit, exactly as the station would have sent it. Never converted here. */
  unit: string;
}

export interface CorrectionIds {
  /** A fresh event id. Called exactly once, and never for a re-issue. */
  newId: () => string;
  now: () => number;
}

/**
 * The replacement event for a refused one, as a command.
 *
 * Pure, and separated from the write for the reason every decision in this subsystem is: what a
 * correction *is* — which fields move, which do not, and what the record is told about the
 * relationship — is a rule with consequences in a clinical ledger, and a rule inside a database
 * call is a rule nobody reads. `resubmit` writes whatever this returns.
 *
 * Three fields change and everything else is copied:
 *
 *   - `event_id` — new, always. See the header.
 *   - `value` and `unit` — what the operator retyped.
 *   - `occurred_at` — now, because that is when this statement was made.
 *
 * And three that a reader might expect to change and must not. `observation_id` is kept: this is
 * the same measurement restated, and a fresh one would present it to the clinic as an unrelated
 * second reading of the same patient a few minutes later — which is a finding, and this is not
 * one. `effective_at` is kept, for the same reason in the other direction. `patient_id`,
 * `facility_id` and the visit are kept because a correction that could move a measurement to a
 * different patient is not a correction.
 */
export function correctionFor(row: OutboxRow, correction: Correction, ids: CorrectionIds): Command {
  const payload = { ...(JSON.parse(row.payload) as Record<string, unknown>) };
  payload.value = correction.value;
  payload.unit = correction.unit;

  const metadata: Record<string, unknown> = {
    ...(row.metadata === null ? {} : (JSON.parse(row.metadata) as Record<string, unknown>)),
    // What this event is for, in the record rather than only in this tablet's memory. The clinic
    // can then see that a value it refused was answered rather than abandoned, which is the
    // question a physician reading a gap in a patient's vitals actually has.
    corrects_event_id: row.eventId,
    corrects_reason_code: row.reasonCode ?? REASON_CODES.refused,
  };

  return {
    eventId: ids.newId(),
    aggregateType: row.aggregateType,
    aggregateId: row.aggregateId,
    patientId: row.patientId,
    visitId: row.visitId,
    eventType: row.eventType,
    eventVersion: row.eventVersion,
    occurredAt: new Date(ids.now()).toISOString(),
    payload,
    metadata,
    // Never carried over, whatever the refused event declared. A correction is a fact about a
    // moment being restated; if it inherited an `expected_sequence` from an event the clinic has
    // already refused, it would come back `BLOCKED` behind the very row it is replacing — a
    // correction that cannot be sent because of the thing it corrects.
    expectedSequence: null,
  };
}

export type ResubmitOutcome =
  /** The correction is queued and the refused entry is accounted for. */
  | 'resubmitted'
  /** No such row. Somebody dealt with it on this device while the screen was open. */
  | 'gone'
  /** The row is not one an operator here may correct — it is the clinic's to decide about. */
  | 'not-yours';

export interface Resubmission {
  outcome: ResubmitOutcome;
  /** The new event's id, when one was written. */
  replacementEventId: string | null;
}

/**
 * Queue the correction and discharge the entry it replaces, together.
 *
 * **One transaction, and that is the whole of this function's difficulty.** The two halves are a
 * clinical write and a queue deletion, and either one alone is a bad state. The correction without
 * the discharge leaves the refusal on the screen, and the operator — who has just fixed it —
 * fixes it again, which puts the same blood pressure in the ledger twice under two event ids and
 * is the worst failure this subsystem has. The discharge without the correction loses the
 * measurement outright, which is the failure the subsystem exists for. So they commit together,
 * and an app killed halfway leaves the screen exactly as it was.
 *
 * # The refused row is deleted, and the refused event is not
 *
 * This is one of the two places in the application where an outbox row leaves the queue without
 * the clinic having taken it, and it is a person doing it deliberately, which is the exception
 * `lib/sync`'s index has always named. The alternative — parking the refused row in some resolved
 * state — sounds safer and is not: `undelivered` counts rows, so a refused entry that stayed would
 * be counted as undelivered work for ever, next to the correction that actually carries the
 * measurement. The operator would be told, honestly and permanently, that two things had not
 * reached the clinic when the number was one, and the count that must never overstate would then
 * never be right again.
 *
 * Nothing is lost by the deletion. The refused event stays in `local_events` with no `global_seq`
 * — this device recorded it, the clinic never took it, and both of those remain true and readable
 * — and the sync log gains a line saying a person corrected it.
 */
export async function resubmit(
  store: LocalStore,
  refusedEventId: string,
  correction: Correction,
  ids: CorrectionIds,
  deps: CommandDeps = {},
): Promise<Resubmission> {
  const result = await store.transaction<Resubmission>(async (tx) => {
    const row = await readOutboxRow(tx, refusedEventId);
    if (row === null) return { outcome: 'gone', replacementEventId: null };
    if (!isOperatorActionable(row)) return { outcome: 'not-yours', replacementEventId: null };

    // Issued **before** the refused row goes, and the order matters for something no test would
    // notice from the outside: the correction's projection has to overwrite the refused value on
    // the screen, and `applyProjections` only overwrites what is already there. Dropping first
    // would work too; doing it in this order means the screen is never, at any instant inside the
    // transaction, showing neither number.
    const issued = await issueWithin(tx, correctionFor(row, correction, ids), deps);
    await dropFromOutbox(tx, refusedEventId);
    return { outcome: 'resubmitted', replacementEventId: issued.eventId };
  });

  if (result.outcome === 'resubmitted') {
    await noteAttention(store, 'CORRECTED', refusedEventId, ids.now());
  }
  return result;
}

// --- escalating ---

export type EscalationOutcome =
  /** Recorded. The operator has done what this tablet can do. */
  | 'escalated'
  /** It was already escalated — two people pressed it, or the screen was stale. */
  | 'already'
  /** No such row. */
  | 'gone'
  /** Held at the clinic: a physician has it, and there is nothing here to escalate. */
  | 'not-yours';

/**
 * Record that a person has read this refusal and taken it to somebody who can act.
 *
 * It sends nothing, and the name is chosen to be honest about that at the call site as well as on
 * the screen: this writes down that the escalation *happened*, in the same way a paper log records
 * that somebody was telephoned. The telephoning is the operator's, and the screen tells them so.
 *
 * `HELD` is refused rather than escalated, which is worth stating because it looks like an
 * omission. A held entry is already in front of a person — it is at the clinic, in the quarantine,
 * waiting for a physician — and offering an operator a button that escalates it would invite them
 * to spend a supervisor's time on the one category of entry where the process is already running.
 */
export async function escalate(
  store: LocalStore,
  eventId: string,
  deps: { now?: () => number } = {},
): Promise<EscalationOutcome> {
  const now = (deps.now ?? Date.now)();
  const outcome = await store.transaction<EscalationOutcome>(async (tx) => {
    const row = await readOutboxRow(tx, eventId);
    if (row === null) return 'gone';
    if (row.state === 'ESCALATED') return 'already';
    if (row.state !== 'NEEDS_ATTENTION') return 'not-yours';
    // The state, and nothing else. Not the reason code, not the payload, not the attempt count —
    // an escalated entry has to stay byte for byte what the clinic refused, because the next
    // person to look at it is deciding what actually happened.
    await updateOutbox(tx, eventId, { state: 'ESCALATED' });
    return 'escalated';
  });

  if (outcome === 'escalated') await noteAttention(store, 'ESCALATED', eventId, now);
  return outcome;
}

/**
 * One line in the local sync log, for a thing a person did rather than a thing the network did.
 *
 * Best effort on purpose. Losing a log line is a support inconvenience; failing a correction
 * because the log could not be appended would be losing a clinical measurement to bookkeeping, and
 * by the time this runs the correction has already committed.
 *
 * The entry's id goes in `detail` and nothing else does. That column has held codes and batch ids
 * since CP66 and must go on holding only those: support logs are copied, pasted and emailed, and a
 * reason sentence from the clinic in one would be the one place on this device where a clinical
 * value escapes into a channel nobody thinks of as a record.
 */
async function noteAttention(
  store: LocalStore,
  outcome: 'CORRECTED' | 'ESCALATED',
  eventId: string,
  at: number,
): Promise<void> {
  try {
    await appendSyncLog(store, {
      id: globalThis.crypto.randomUUID(),
      at,
      phase: 'attention',
      outcome,
      detail: referenceOf(eventId),
    });
  } catch {
    // Deliberately swallowed. See above.
  }
}

function isOperatorActionable(row: OutboxRow): boolean {
  return (OPERATOR_ACTIONABLE_STATES as readonly string[]).includes(row.state);
}
