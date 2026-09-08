import { pillFor } from '../src/features/sync/state';
import { TABLES } from '../src/lib/local-store/schema';
import { readMetrics } from '../src/lib/sync/outbox';

import type { LocalStore, Row } from '../src/lib/local-store/driver';
import type { FakeClinic } from './fake-sync-server';

/**
 * The data-integrity check (CP68 acceptance criterion 2; CP66 brought the first six rules forward).
 *
 * After a run — a clean one, a chaotic one, a soak — the device and the clinic are compared **event
 * for event**, and every way they could disagree is a named problem rather than a vague count.
 * This is the check that would notice the failure this checkpoint is most afraid of: not an error,
 * not a crash, but two records that quietly differ.
 *
 * # Every rule is a claim with a loss attached
 *
 * `INTEGRITY_CLAIMS` states each rule as a sentence about what must be true, beside the clinical
 * loss it would catch. That pairing is the point of the file rather than decoration. A rule with no
 * stated loss cannot be argued about — nobody can tell whether it is too strict, too loose, or
 * checking something that does not matter — and CP68's own risk is a suite people stop trusting.
 * A rule whose loss is written down can be deleted on purpose by somebody who decides the loss is
 * acceptable, which is the only safe way for a check like this to shrink.
 *
 * The claim ids are also the vocabulary the mutation harness speaks (`test/offline/mutations.ts`):
 * every deliberate bug names the claim that must catch it, and the matrix is a table of those
 * names. That is what makes "the suite catches a sync bug" a checkable statement rather than a
 * hope.
 *
 * # What each rule is for, in one line
 *
 *  1. **A measurement that is nowhere.** Zero data loss is this sentence.
 *
 *     The fourth arm — explicitly refused — arrived with CP67 and is worth stating rather than
 *     quietly allowing. Before it, a rejected event stayed in the outbox for ever and the first
 *     three covered everything; now an operator can correct one, and the corrected measurement
 *     goes in a **new** event while the refused one remains in `local_events` alone. Allowing that
 *     unconditionally would have blinded this rule to the failure it exists for, so it is allowed
 *     only for an id the clinic actually refused — and rule 10 then asks the harder question the
 *     fourth arm opened up, which is whether a *person* refused it or the engine simply dropped it.
 *  2. **A measurement recorded twice.** Two ledger rows with different ids but the same aggregate,
 *     type, moment and payload. That is what a client which regenerated `event_id` on retry
 *     produces, and nothing else in the system would ever notice it.
 *  3. **A value that changed on the way.**
 *  4. **A false confirmation.** Anything this device believes the clinic has, the clinic has.
 *  5. **A missed confirmation**, when the run is supposed to have finished.
 *  6. **A projection built from an event nobody has.**
 *  7. **Two entries about one record arriving in the wrong order** (CP68).
 *  8. **Work delivered twice** — an event the clinic has already answered for, sent again (CP68).
 *  9. **The cursor claiming more than was applied** (CP68).
 * 10. **A refusal that left the queue without a person answering it** (CP68).
 * 11. **An operator asked to deal with work the clinic already has** (CP68).
 * 12. **The number on the operator's screen disagreeing with the rows behind it** (CP68).
 *
 * # The two that cannot be checked afterwards
 *
 * `cursorMonotonic` is in the registry and is not checked here at all: a cursor that went backwards
 * and came forward again looks identical afterwards to one that never moved. `test/offline/harness.ts`
 * watches every write to it instead.
 *
 * `noFalseAlarm` is checked here **and** watched there, and the second is the one that matters. A
 * false alarm repairs itself — the event is in the ledger, so the next pull brings it down and the
 * row goes — and what it cost in the meantime is an operator being asked to restate a measurement
 * the clinic already has. This file catches the case where nothing repaired it; the harness catches
 * the case where something did.
 *
 * Both live in this list anyway, because there is one vocabulary of claims and splitting it would
 * mean the mutation matrix had two.
 */

export interface IntegrityClaim {
  /** What the mutation harness and the matrix name it by. */
  id: string;
  /** What must be true, as a sentence. */
  claim: string;
  /** What is lost, clinically, when it is not. */
  loss: string;
}

export const INTEGRITY_CLAIMS = {
  nothingNowhere: {
    id: 'nothing-is-nowhere',
    claim:
      'Every event this device recorded is in the ledger, in the clinic’s quarantine, still in ' +
      'this device’s outbox, or explicitly refused by the clinic.',
    loss:
      'A measurement taken from a patient, shown on a screen as recorded, and delivered to ' +
      'nobody. The failure the whole offline system exists to prevent.',
  },
  noDoubleRecord: {
    id: 'no-measurement-recorded-twice',
    claim:
      'No two ledger rows describe the same measurement — same record, same type, same moment, ' +
      'same payload — under two different event ids.',
    loss:
      'One blood pressure counted as two facts about two moments. Nothing downstream can tell ' +
      'them apart, so a research cohort, a trend line and a physician all read a reading that ' +
      'never happened.',
  },
  unchangedInTransit: {
    id: 'nothing-changed-on-the-way',
    claim: 'The payload and `occurred_at` on the device and in the ledger are identical.',
    loss: 'A value silently different from the one the operator read off the instrument.',
  },
  noFalseConfirmation: {
    id: 'nothing-confirmed-that-is-not-there',
    claim:
      'Every event this device believes it synced is in the ledger, at the sequence it believes.',
    loss:
      'The indicator saying the work is safe over work nobody has. §13.9’s permanent loss of ' +
      'trust, arriving with no error anywhere.',
  },
  nothingQueuedThatLanded: {
    id: 'nothing-queued-that-already-landed',
    claim:
      'When the run has finished, nothing is still in flight and nothing waiting to be sent is ' +
      'already in the ledger.',
    loss:
      'A resend that the clinic answers `DUPLICATE` for ever, or — on a client whose idempotency ' +
      'was ever weakened — a second copy of a real measurement.',
  },
  projectionHasEvent: {
    id: 'every-projection-names-an-event',
    claim: 'Every projection was built from an event this device still holds.',
    loss: 'A number on a station screen with nothing behind it that anybody could audit.',
  },
  batchOrder: {
    id: 'per-record-order-is-preserved',
    claim:
      'Within one batch, two events about the same record are sent in the order the operator ' +
      'recorded them — by the local sequence, never by a clock that may be wrong.',
    loss:
      'The server applies a batch in the order it arrives, and §13.6 settles two values for one ' +
      'record by which is later. Invert two entries and the earlier reading wins: the value the ' +
      'operator corrected is the one left on the screen, with no error anywhere.',
  },
  noSecondDelivery: {
    id: 'nothing-answered-is-sent-again',
    claim:
      'An event the clinic has answered for terminally — accepted, duplicate, refused or held — ' +
      'is never sent again in a different batch.',
    loss:
      'At best a morning of pointless requests on a clinic’s connection. At worst a refusal ' +
      'somebody has already read and escalated being sent back, refused again, and reset to ' +
      '"needs attention" — the tablet undoing the one thing the operator did about it.',
  },
  cursorNotAhead: {
    id: 'the-cursor-never-runs-ahead-of-what-was-applied',
    claim: 'Every event in the ledger at or below this device’s pull cursor is on the device.',
    loss:
      'Another station’s work skipped for ever. The cursor says it has been seen, so it is never ' +
      'offered again, and the tablet shows a patient with a gap nobody can explain.',
  },
  cursorMonotonic: {
    id: 'the-cursor-never-goes-backwards',
    claim: 'The pull cursor only ever increases.',
    loss:
      'Re-pulling is idempotent, so nothing is corrupted — what is lost is the ability to finish. ' +
      'A cursor that slips back re-applies the same page every tick, for ever, on a battery.',
  },
  refusalDischarged: {
    id: 'a-refusal-leaves-the-queue-only-when-a-person-answers-it',
    claim:
      'An event the clinic refused is either still in the outbox for somebody to deal with, or ' +
      'answered by a correction that names it (`corrects_event_id`).',
    loss:
      'The engine quietly dropping what the clinic refused. Rule 1 permits a refused event to be ' +
      'absent — that is CP67’s correct-and-resubmit — so without this rule a client that treated ' +
      '`REJECTED` as success would pass every check in this file while losing a real measurement ' +
      'on every refusal.',
  },
  noFalseAlarm: {
    id: 'nothing-the-clinic-has-taken-is-put-in-front-of-a-person',
    claim:
      'No entry waiting for a person — refused, or escalated — is one the clinic already holds in ' +
      'its ledger.',
    loss:
      'The one alarm that manufactures the error it reports. An operator shown a delivered ' +
      'measurement as refused does the sensible thing and restates it, and the correction is a ' +
      'second, different-looking reading of the same patient a few minutes later. The record ends ' +
      'up worse than if nothing had been checked at all.',
  },
  countsMatchRows: {
    id: 'the-counts-on-screen-are-the-rows-in-the-store',
    claim:
      'What the operator is shown is counted from the rows: `undelivered` is the number of outbox ' +
      'rows, and the pill never says everything is with the clinic while there is one.',
    loss: '§13.9 in one sentence — an indicator that says "synced" when it is not.',
  },
} as const satisfies Record<string, IntegrityClaim>;

export type ClaimName = keyof typeof INTEGRITY_CLAIMS;

export interface IntegrityProblem {
  /** The claim that failed, by its id. */
  claim: string;
  /** What was found, named concretely enough to debug from. */
  detail: string;
}

export interface IntegrityOptions {
  /** Assert that the run finished: nothing left in flight, nothing queued that already landed. */
  quiescent?: boolean;
}

/**
 * The whole check, as a list of failed claims.
 *
 * Empty is the only acceptable answer after any scenario in this suite.
 */
export async function integrityReport(
  store: LocalStore,
  clinic: FakeClinic,
  options: IntegrityOptions = {},
): Promise<IntegrityProblem[]> {
  const problems: IntegrityProblem[] = [];
  const found = (claim: IntegrityClaim, detail: string) =>
    problems.push({ claim: claim.id, detail });

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
      clinic.ledger.has(eventId) ||
      heldById.has(eventId) ||
      outboxById.has(eventId) ||
      clinic.refusals.has(eventId);
    if (!somewhere) {
      found(INTEGRITY_CLAIMS.nothingNowhere, `${eventId}: recorded on the device and nowhere else`);
    }
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
      found(
        INTEGRITY_CLAIMS.noDoubleRecord,
        `the ledger holds the same measurement ${ids.length} times (${key}): ${ids.join(', ')}`,
      );
    }
  }

  // 3. Nothing changed on the way.
  for (const row of clinic.ledger.values()) {
    const local = localById.get(row.event_id) ?? outboxById.get(row.event_id);
    if (!local) continue;
    if (String(local.occurred_at) !== row.occurred_at) {
      found(
        INTEGRITY_CLAIMS.unchangedInTransit,
        `${row.event_id}: occurred_at is ${String(local.occurred_at)} here and ${row.occurred_at} at the clinic`,
      );
    }
    const localPayload = JSON.stringify(JSON.parse(String(local.payload)));
    if (localPayload !== JSON.stringify(row.payload)) {
      found(
        INTEGRITY_CLAIMS.unchangedInTransit,
        `${row.event_id}: the payload differs between the device and the clinic`,
      );
    }
  }

  // 4. Nothing is confirmed that is not there.
  for (const row of localEvents) {
    if (row.global_seq === null || row.global_seq === undefined) continue;
    const stored = clinic.ledger.get(String(row.event_id));
    if (!stored) {
      found(
        INTEGRITY_CLAIMS.noFalseConfirmation,
        `${String(row.event_id)}: the device says it synced and the clinic has never seen it`,
      );
      continue;
    }
    if (Number(row.global_seq) !== stored.global_seq) {
      found(
        INTEGRITY_CLAIMS.noFalseConfirmation,
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
        found(
          INTEGRITY_CLAIMS.nothingQueuedThatLanded,
          `${eventId}: still in flight after the run finished`,
        );
      }
      if ((state === 'PENDING' || state === 'BLOCKED_LOCAL') && clinic.ledger.has(eventId)) {
        found(
          INTEGRITY_CLAIMS.nothingQueuedThatLanded,
          `${eventId}: queued for sending and already in the ledger`,
        );
      }
    }
  }

  // 6. Every projection names an event this device holds.
  for (const row of projections) {
    if (!localById.has(String(row.event_id)) && !outboxById.has(String(row.event_id))) {
      found(
        INTEGRITY_CLAIMS.projectionHasEvent,
        `projection ${String(row.kind)}/${String(row.key)} was built from an event this device does not hold`,
      );
    }
  }

  // 7. Two events about one record went in the order the operator worked in.
  //
  // Checked over what was **sent** rather than over the ledger, and the difference is not
  // pedantry. Across batches an independent later event legitimately overtakes an earlier one the
  // clinic has refused or had no room for — the engine does that deliberately, because holding a
  // morning's work behind one entry somebody else must deal with is the failure `docs/sync.md`
  // argues against at length. Within one batch there is no such case: `selectBatch` never puts a
  // record's second event in a batch its first is not in, so anything out of order here is the
  // ordering rule itself failing.
  const seqOf = new Map<string, number>();
  for (const row of localEvents) seqOf.set(String(row.event_id), Number(row.seq));
  for (const row of outbox) seqOf.set(String(row.event_id), Number(row.seq));
  for (const body of clinic.pushes) {
    const last = new Map<string, number>();
    for (const event of body.events) {
      const key = `${event.aggregate_type}/${event.aggregate_id}`;
      const seq = seqOf.get(event.event_id);
      if (seq === undefined) continue;
      const previous = last.get(key);
      if (previous !== undefined && seq < previous) {
        found(
          INTEGRITY_CLAIMS.batchOrder,
          `batch ${body.batch_id}: ${event.event_id} (recorded ${seq}) was sent after ${previous} on ${key}`,
        );
      }
      last.set(key, seq);
    }
  }

  // 8. Nothing the clinic has answered for is sent again.
  //
  // "In a different batch" is the whole precision of this rule. Re-sending a batch **under its own
  // id** is not a second delivery, it is the receipt doing its job: the clinic answers a closed
  // batch from its record without touching the ledger, and re-sends an open one because that is
  // the only way to learn what happened to a batch a server was interrupted half way through. A
  // rule that forbade both would forbid the recovery path this protocol is built on.
  //
  // `BLOCKED` is deliberately not terminal. It means the event was not attempted, and sending it
  // again is what the client is supposed to do.
  const answeredIn = new Map<string, string>();
  for (const body of clinic.pushes) {
    for (const event of body.events) {
      const previous = answeredIn.get(event.event_id);
      if (previous !== undefined && previous !== body.batch_id) {
        found(
          INTEGRITY_CLAIMS.noSecondDelivery,
          `${event.event_id}: answered in batch ${previous} and sent again in ${body.batch_id}`,
        );
      }
    }
    for (const result of clinic.batches.get(body.batch_id)?.results ?? []) {
      if (result.outcome !== 'BLOCKED') answeredIn.set(result.event_id, body.batch_id);
    }
  }

  // 9. The cursor never claims more than was applied.
  const cursorRows = await store.all({
    table: TABLES.syncMeta,
    where: [{ column: 'key', op: '=', value: 'pull_cursor' }],
    limit: 1,
  });
  const cursor = Number(cursorRows[0]?.value ?? 0);
  for (const row of clinic.ledger.values()) {
    if (row.global_seq > cursor) continue;
    if (!localById.has(row.event_id)) {
      found(
        INTEGRITY_CLAIMS.cursorNotAhead,
        `${row.event_id}: at sequence ${row.global_seq}, below a cursor of ${cursor}, and not on the device`,
      );
    }
  }

  // 10. A refusal left the queue only because a person answered it.
  //
  // The evidence is the correction's own metadata, which is the copy that travels: a correction is
  // a new event whose `corrects_event_id` names the refusal it replaces, and it says so on the
  // wire as well as on the tablet. Looking for it in three places covers the correction wherever
  // it has got to — still queued, delivered, or held at the clinic — because a refusal is answered
  // the moment somebody writes the replacement, not when the clinic finally takes it.
  const answersFor = new Set<string>();
  const noteAnswer = (metadata: unknown) => {
    if (metadata === null || metadata === undefined) return;
    const parsed = (typeof metadata === 'string' ? JSON.parse(metadata) : metadata) as Record<
      string,
      unknown
    >;
    const corrects = parsed.corrects_event_id;
    if (typeof corrects === 'string') answersFor.add(corrects);
  };
  for (const row of outbox) noteAnswer(row.metadata);
  for (const row of clinic.ledger.values()) noteAnswer(row.metadata);
  for (const row of clinic.held) noteAnswer(row.event.metadata);
  for (const refusedId of clinic.refusals.keys()) {
    if (outboxById.has(refusedId) || answersFor.has(refusedId)) continue;
    found(
      INTEGRITY_CLAIMS.refusalDischarged,
      `${refusedId}: the clinic refused it, it is not in the queue, and no correction names it`,
    );
  }

  // 11. Nothing the clinic already has is waiting for a person.
  for (const problem of falseAlarms(outbox, clinic)) problems.push(problem);

  // 12. The counts on the screen are the rows in the store.
  const metrics = await readMetrics(store);
  if (metrics.undelivered !== outbox.length) {
    found(
      INTEGRITY_CLAIMS.countsMatchRows,
      `the screen says ${metrics.undelivered} undelivered and the outbox holds ${outbox.length}`,
    );
  }
  const named =
    metrics.queued +
    metrics.inFlight +
    metrics.needsAttention +
    metrics.escalated +
    metrics.held +
    metrics.blocked +
    metrics.awaitingTriage;
  if (named > metrics.total) {
    // Deliberately one-sided. A row in a state nobody has written a case for counts in `total` and
    // in none of the named states, so `named` below `total` is the honest direction and is allowed
    // — the label is wrong and the headline is right. `named` *above* `total` would mean a row
    // counted twice, which makes the headline wrong.
    found(
      INTEGRITY_CLAIMS.countsMatchRows,
      `the named states add up to ${named} over ${metrics.total} rows`,
    );
  }
  for (const online of [true, false]) {
    const pill = pillFor(metrics, { online });
    if (pill.status === 'synced' && outbox.length > 0) {
      found(
        INTEGRITY_CLAIMS.countsMatchRows,
        `the pill says everything is with the clinic over ${outbox.length} undelivered row(s)`,
      );
    }
  }

  return problems;
}

/**
 * The one rule that has to be checked **while** a run happens rather than after it.
 *
 * A false alarm is transient by nature: the event is in the ledger, so the next pull brings it
 * back down and `applyPulled` drops the row — the second, independent way an event leaves the
 * queue, doing exactly what CP66 built it for. Afterwards there is nothing left to find, and the
 * check would report a clean device that spent ten minutes asking an operator to fix work that
 * was already in the record.
 *
 * So `harness.ts` runs this after every attempt, and `integrityReport` runs it once more at the
 * end for the case where nothing repaired it.
 */
export function falseAlarms(outbox: Row[], clinic: FakeClinic): IntegrityProblem[] {
  const problems: IntegrityProblem[] = [];
  for (const row of outbox) {
    const state = String(row.state);
    if (state !== 'NEEDS_ATTENTION' && state !== 'ESCALATED') continue;
    if (!clinic.ledger.has(String(row.event_id))) continue;
    problems.push({
      claim: INTEGRITY_CLAIMS.noFalseAlarm.id,
      detail: `${String(row.event_id)}: the clinic has it and the operator is being asked to fix it`,
    });
  }
  return problems;
}

/**
 * The same check, as sentences.
 *
 * Kept because every scenario written before CP68 asserts on this shape, and because a failure
 * message a person reads should be a sentence rather than a claim id.
 */
export async function integrityProblems(
  store: LocalStore,
  clinic: FakeClinic,
  options: IntegrityOptions = {},
): Promise<string[]> {
  return (await integrityReport(store, clinic, options)).map((problem) => problem.detail);
}
