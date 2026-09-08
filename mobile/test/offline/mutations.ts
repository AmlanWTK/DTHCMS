import { vi } from 'vitest';

import { META_KEYS, TABLES } from '../../src/lib/local-store/schema';
import { INTEGRITY_CLAIMS } from '../sync-integrity';
import { SCENARIO_CLAIMS } from './scenarios';

import type { Hooks } from './harness';

/**
 * The mutation harness (CP68 acceptance criterion 4).
 *
 * # Why this file is the checkpoint
 *
 * A suite nobody has watched fail proves nothing. Every green run of the §13.10 matrix is
 * consistent with two worlds — one where the sync engine is correct, and one where the checks are
 * asleep — and the only way to tell them apart is to break the engine on purpose and watch the
 * suite go red. That is what this file does: twelve deliberate bugs, each of a kind somebody could
 * plausibly introduce, each declaring in advance **which claim must catch it**.
 *
 * The declaration is the interesting half. "Something failed" is a weak result: a mutation that
 * makes forty checks fail for forty reasons tells you the suite is noisy, not that it is
 * watchful. Naming the claim in advance means the matrix is a statement about coverage — *this*
 * rule, and no other, is what stands between the record and *this* loss — and a mutation that is
 * caught by a different claim than the one predicted is a finding either way: either the rule is
 * not doing what it was written to do, or the prediction was wrong about the system.
 *
 * # Where a mutation is injected, and why not by editing a file
 *
 * Four seams, in order of preference:
 *
 *   - **`module`** — the engine's own decision functions, replaced by resetting the module
 *     registry and mocking one module before the graph is imported. The engine really calls the
 *     broken function; nothing is stubbed around it. This is as close to editing the source as it
 *     is possible to get without editing the source, and it is why `harness.ts` imports statically
 *     and the mutation test imports dynamically.
 *   - **`store`** — a write altered or swallowed underneath the engine. Some bugs are not
 *     decisions: "the outbox row was never written" is what a lost transaction looks like, and it
 *     has no function to replace.
 *   - **`ids`** — the id generator, for the two bugs that are about identity rather than about a
 *     decision or a write.
 *   - **`transport`** — the wire itself, for the one canary here that is not a client bug at all:
 *     a payload altered in transit, which exists to prove that the rule watching for it is
 *     connected to anything.
 *
 * A mutation applied by editing a file and reverting it would be untracked, unreviewable, and
 * impossible to run in CI — which would leave criterion 4 as a paragraph in a report rather than a
 * test that runs on every push. These run on every push.
 *
 * # A mutation that survives is a finding, not a nuisance
 *
 * The rule when one is not caught: the fix is a new check, never a quieter mutation. Two of the
 * rules in `sync-integrity.ts` exist because of this file, and neither was foreseen.
 *
 * `refusalDischarged` was written after `rejection-ignored` sailed through every rule the checker
 * had. CP67 added a legitimate way for a refused event to leave the queue — an operator corrects
 * it, and the correction replaces it — so rule 1 was widened to allow a refused event to be
 * absent, and that widening quietly also allowed an engine that dropped every rejection on its
 * own: a lost measurement per refusal, with no error anywhere.
 *
 * `noFalseAlarm` is the subtler one, because `duplicate-treated-as-a-failure` **repairs itself**.
 * The entry is in the ledger, so the next pull brings it down and clears the row, and a check that
 * looked at the end state would find a clean tablet. What it cost in between is an operator being
 * asked to restate a measurement the clinic already has — and if they do, the record gains a
 * second reading of that patient. So what is watched is the moment a row was put in front of a
 * person, which is the only version of that fact that survives.
 */

export interface Mutation {
  id: string;
  /** The bug, as a change somebody could plausibly make and defend in review. */
  bug: string;
  /** Why somebody would write it. A mutation nobody would ever introduce proves less. */
  plausibly: string;
  /** Which §13.10 scenario is run against it. */
  scenario: string;
  /** The claim that must fail. Named in advance; the test asserts this one specifically. */
  caughtBy: string;
  /** How it is injected. */
  seam: 'module' | 'store' | 'transport' | 'ids';
  /** Registers the module mock. Called after `vi.resetModules()` and before the graph is loaded. */
  install?(): void;
  /** The store and id seams, handed to the world. */
  hooks?: Hooks;
}

/** The module the engine reads its decisions from. Mocked by id, so the alias resolves as it does. */
const STATE = '@/lib/sync/state';

type StateModule = typeof import('../../src/lib/sync/state');
type EventAction = import('../../src/lib/sync/state').EventAction;

/** Replace some of `state.ts` and leave the rest exactly as it is. */
function mutateState(replace: (actual: StateModule) => Partial<StateModule>): () => void {
  return () => {
    vi.doMock(STATE, async () => {
      const actual = await vi.importActual<StateModule>(STATE);
      return { ...actual, ...replace(actual) };
    });
  };
}

/**
 * Give one outcome the wrong action, by rewriting the plan rather than by replacing `actionFor`.
 *
 * The bug being modelled is `actionFor` returning the wrong thing, and mocking `actionFor` itself
 * does not model it: ESM replaces a module's *exports*, and `planFromReceipt` calls `actionFor` as
 * a local function in the same file, so the engine would go on using the real one and the mutation
 * would quietly test nothing. That is precisely the "green check that confirmed something existed"
 * failure this suite is here to avoid, met while building the suite — so it is recorded rather
 * than worked around silently.
 *
 * Rewriting the plan applies the same defect one level further out: what the engine acts on is
 * exactly what a wrong `actionFor` would have produced.
 */
function misreadOutcome(outcome: string, action: EventAction): () => void {
  return mutateState((actual) => ({
    planFromReceipt: (rows, receipt) => {
      const plan = actual.planFromReceipt(rows, receipt);
      return {
        ...plan,
        resolved: plan.resolved.map((one) => (one.outcome === outcome ? { ...one, action } : one)),
      };
    },
  }));
}

export const MUTATIONS: readonly Mutation[] = [
  {
    id: 'outbox-row-dropped',
    bug: 'The outbox row for one command is never written, though the event and the projection are.',
    plausibly:
      'Somebody splits the command handler’s single transaction into three writes "for clarity", ' +
      'and the third is skipped on a branch nobody exercises.',
    scenario: 'airplane-mode-mid-entry',
    caughtBy: INTEGRITY_CLAIMS.nothingNowhere.id,
    seam: 'store',
    hooks: {
      write(write) {
        // The third entry of the morning, and only that one: a mutation that dropped every row
        // would be caught by the count on the screen instead, which is a weaker result — the
        // interesting failure is the single measurement that goes missing in a queue of many.
        if (write.kind === 'insert' && write.table === TABLES.outbox && write.row.seq === 3) {
          return null;
        }
        return write;
      },
    },
  },

  {
    id: 'receipt-result-skipped',
    bug: 'Results that are not a plain acceptance are dropped from the plan the receipt produces.',
    plausibly:
      'A loop over results gains a `continue` for the outcomes somebody thought were ' +
      'uninteresting, and the rows they belonged to are neither resolved nor requeued — they stay ' +
      'in flight, invisibly, for the life of the tablet.',
    scenario: 'one-rejection-in-fifty',
    caughtBy: INTEGRITY_CLAIMS.nothingQueuedThatLanded.id,
    seam: 'module',
    install: mutateState((actual) => ({
      planFromReceipt: (rows, receipt) => {
        const plan = actual.planFromReceipt(rows, receipt);
        // Dropped rather than moved to `unanswered`, which is the difference between this bug and
        // the recovery path: an unanswered event is requeued and sent again, and this one is
        // simply forgotten.
        //
        // Aimed at the refusals rather than at the acceptances, and that is a finding in its own
        // right: **a skipped acceptance repairs itself.** The event is in the ledger, so the next
        // pull brings it back down and `applyPulled` drops it from the outbox — the second,
        // independent way an event leaves the queue, doing exactly the job CP66 says it is there
        // for. A skipped *refusal* has no such net: the clinic never took it, so nothing ever
        // comes down to clear it, and it sits in flight where no screen lists it.
        return { ...plan, resolved: plan.resolved.filter((one) => one.action === 'drop') };
      },
    })),
  },

  {
    id: 'event-id-regenerated-on-retry',
    bug: 'A retried event is sent with a fresh `event_id`.',
    plausibly:
      'The historical one. CP66 found it by mutation and found a second defect underneath it: an ' +
      'engine that regenerated the id only from the third attempt survived the whole suite, ' +
      'because no batch ever reached a third attempt.',
    // The batch that is cut in half and re-sent under its own id, rather than a run of weather
    // that may or may not produce a retry: a mutation whose exposure depends on a seed is a
    // mutation that will one day stop being tested by a seed somebody changed for another reason.
    scenario: 'duplicate-submission-of-a-batch',
    caughtBy: INTEGRITY_CLAIMS.noDoubleRecord.id,
    seam: 'module',
    install: mutateState((actual) => {
      let minted = 0;
      return {
        toWireEvent: (row) => {
          const event = actual.toWireEvent(row);
          if (row.attempts <= 1) return event;
          minted += 1;
          return { ...event, event_id: `${row.eventId}-retry-${minted}` };
        },
      };
    }),
  },

  {
    id: 'event-id-reused-between-measurements',
    bug: 'Two different measurements are issued under one `event_id`.',
    plausibly:
      'An id derived from something that looked unique — the patient and the observation code, ' +
      'say — instead of being generated per command.',
    scenario: 'airplane-mode-mid-entry',
    caughtBy: SCENARIO_CLAIMS.entryIsNeverSwallowed.id,
    seam: 'ids',
    hooks: {
      commandId(next) {
        // Every command from the third onwards gets the third one's id. The command handler is
        // idempotent by design, so it answers "already issued" and writes nothing — which is
        // exactly right for a genuine re-issue of the same measurement and catastrophic for a
        // different one.
        let issued = 0;
        let repeated: string | null = null;
        return () => {
          issued += 1;
          if (issued < 3) return next();
          repeated ??= next();
          return repeated;
        };
      },
    },
  },

  {
    id: 'rejection-ignored',
    bug: 'A `REJECTED` event is dropped from the queue as though the clinic had accepted it.',
    plausibly:
      'The most tempting bug in the file. `REJECTED` means the clinic will not take it, so a ' +
      'reasonable person reads that as "stop trying" and writes the same branch as `ACCEPTED` — ' +
      'and the queue empties, and the pill goes quiet, and the measurement is gone.',
    scenario: 'one-rejection-in-fifty',
    caughtBy: INTEGRITY_CLAIMS.refusalDischarged.id,
    seam: 'module',
    install: misreadOutcome('REJECTED', 'drop'),
  },

  {
    id: 'held-treated-as-delivered',
    bug: 'A `QUARANTINED` event is dropped from the queue as though it had been accepted.',
    plausibly:
      'It did reach the clinic, which makes "delivered" feel like the right word. It is not: a ' +
      'held event is waiting for a physician to decide about it, and a tablet that stopped ' +
      'counting it would tell the operator their morning was in the record when it is in a tray.',
    scenario: 'device-revoked-while-offline',
    caughtBy: SCENARIO_CLAIMS.refusalIsVisible.id,
    seam: 'module',
    install: misreadOutcome('QUARANTINED', 'drop'),
  },

  {
    id: 'duplicate-treated-as-a-failure',
    bug: 'A `DUPLICATE` result puts the entry in front of a person instead of clearing it.',
    plausibly:
      'It reads like an error. The contract says in as many words that it is not, because it is ' +
      'what a resent batch looks like — and a client that treats it as one never makes progress ' +
      'after a lost response.',
    scenario: 'duplicate-submission-of-a-batch',
    caughtBy: INTEGRITY_CLAIMS.noFalseAlarm.id,
    seam: 'module',
    install: misreadOutcome('DUPLICATE', 'attention'),
  },

  {
    id: 'escalated-row-resent',
    bug: 'An escalated entry is put back into the next batch.',
    plausibly:
      'CP67 added `ESCALATED` and `selectBatch` has a list of states that mean "a person is next ' +
      'to act". Leaving one out of that list is a one-word omission with no visible symptom: the ' +
      'entry is sent, refused again, and reset to "needs attention", undoing the operator’s work.',
    scenario: 'one-rejection-in-fifty',
    caughtBy: INTEGRITY_CLAIMS.noSecondDelivery.id,
    seam: 'module',
    install: mutateState((actual) => ({
      selectBatch: (rows, options) =>
        actual.selectBatch(
          rows.map((row) => (row.state === 'ESCALATED' ? { ...row, state: 'PENDING' } : row)),
          options,
        ),
    })),
  },

  {
    id: 'batch-ordered-by-the-clock',
    bug: 'The batch is ordered by `occurred_at` rather than by the local sequence.',
    plausibly:
      'The implementation plan’s own words are "ordered by `occurred_at`, preserving ' +
      'per-aggregate order", and the two come apart on precisely the device §13.10 asks about. ' +
      'Anybody implementing from the sentence writes this.',
    scenario: 'device-clock-three-hours-wrong',
    caughtBy: INTEGRITY_CLAIMS.batchOrder.id,
    seam: 'module',
    install: mutateState((actual) => ({
      selectBatch: (rows, options) =>
        actual
          .selectBatch(rows, options)
          .sort((left, right) => left.occurredAt.localeCompare(right.occurredAt)),
    })),
  },

  {
    id: 'cursor-lost',
    bug: 'The pull cursor is written back as zero once, part way through a run.',
    plausibly:
      'A cursor cleared on a path that meant to clear something else — a sign-out, a wipe, a ' +
      'reference cache refresh. Nothing is corrupted by it, because re-applying a pulled event is ' +
      'idempotent, so it produces no error and no wrong value: only a tablet that never finishes ' +
      'pulling, re-reading the same pages until the battery goes.',
    scenario: 'two-hundred-queued-events',
    caughtBy: INTEGRITY_CLAIMS.cursorMonotonic.id,
    seam: 'store',
    hooks: {
      write(write) {
        if (
          write.kind === 'insert' &&
          write.table === TABLES.syncMeta &&
          write.row.key === META_KEYS.cursor &&
          Number(write.row.value) > 60
        ) {
          return { ...write, row: { ...write.row, value: 0 } };
        }
        return write;
      },
    },
  },

  {
    id: 'cursor-runs-ahead',
    bug: 'The pull cursor advances to the end of the page rather than to the last event applied.',
    plausibly:
      'The obvious implementation. The server sends a cursor with every page; using it is one ' +
      'line shorter than tracking what actually applied, and it is right in every case except the ' +
      'one that matters.',
    scenario: 'two-hundred-queued-events',
    caughtBy: INTEGRITY_CLAIMS.cursorNotAhead.id,
    seam: 'store',
    hooks: {
      write(write) {
        if (
          write.kind === 'insert' &&
          write.table === TABLES.syncMeta &&
          write.row.key === META_KEYS.cursor
        ) {
          // Ahead by most of a page, which is what "advance to the page's cursor whatever
          // happened" looks like when the middle of a page fails to apply.
          return { ...write, row: { ...write.row, value: Number(write.row.value) + 40 } };
        }
        return write;
      },
    },
  },

  {
    id: 'payload-rewritten-in-transit',
    bug: 'One measurement’s value is altered between the outbox and the wire.',
    plausibly:
      'Not a bug anybody writes deliberately — a unit conversion applied on the way out, or a ' +
      'payload rebuilt from a projection instead of being sent as it was recorded. It is here ' +
      'because a rule that has never fired is a rule nobody knows is connected.',
    scenario: 'app-killed-with-a-full-queue',
    caughtBy: INTEGRITY_CLAIMS.unchangedInTransit.id,
    seam: 'transport',
    hooks: {
      transport: (base) => ({
        ...base,
        push: (body) =>
          base.push({
            ...body,
            events: body.events.map((event, index) =>
              index === 0 ? { ...event, payload: { ...event.payload, value: -1 } } : event,
            ),
          }),
      }),
    },
  },
];

export function mutationById(id: string): Mutation {
  const found = MUTATIONS.find((mutation) => mutation.id === id);
  if (!found) throw new Error(`no such mutation: ${id}`);
  return found;
}
