import { NetworkError } from '@dthcms/api-client';

import { itemsOf } from '../../src/features/sync/state';
import { TABLES } from '../../src/lib/local-store/schema';
import { escalate, resubmit } from '../../src/lib/sync/attention';
import { noteSessionLost, resumeAfterSignIn, syncOnce } from '../../src/lib/sync/engine';
import { readCounts, readMetrics, readOutbox } from '../../src/lib/sync/outbox';
import { BATCH_LIMIT, HALT_REASONS, REASON_CODES, statusOf } from '../../src/lib/sync/state';
import { INTEGRITY_CLAIMS, type IntegrityClaim, type IntegrityProblem } from '../sync-integrity';
import { FLAKY_NETWORK } from './chaos';
import { createWorld, mergeHooks, type Hooks, type World, type WorldOptions } from './harness';

import type { SyncTransport } from '../../src/lib/sync/transport';

/**
 * §13.10, enumerated (CP68 acceptance criterion 1).
 *
 * # The list is the deliverable
 *
 * The blueprint's §13.10 is one paragraph of ten clauses separated by middots, and "all of them"
 * is the checkpoint's first acceptance criterion. So each clause is transcribed here verbatim,
 * beside the scenario that runs it, and `offline-matrix.test.ts` asserts that the transcription
 * still matches the blueprint sentence word for word. A scenario cannot be quietly dropped, and
 * the paragraph cannot be quietly reinterpreted — which is the failure this suite exists to be an
 * answer to rather than an instance of.
 *
 * # Which of them a container can actually answer
 *
 * Every scenario here runs in CI against the in-memory store and `FakeClinic`. That covers the
 * whole decision layer — the batch, the receipt, the ladder, the outcomes, the cursor, the counts
 * — and covers **none** of the four things only hardware has: SQLCipher, a real radio, the OS's
 * own clock, and a filesystem that can genuinely fill up. `reach` says which is which for each
 * scenario and `device` names the Maestro flow that answers the other half, so that a green run
 * here is never mistaken for a device check. `mobile/maestro/offline/README.md` carries the same
 * table from the hardware side.
 *
 * # Why a scenario returns problems instead of asserting
 *
 * Every check names the claim it is checking, from one vocabulary shared with the integrity
 * checker, and a scenario collects rather than throws. That is what makes the mutation harness
 * possible: it can run the same scenario under a deliberately broken engine and ask *which claim*
 * noticed, rather than reading an assertion message and hoping. A scenario that threw on the first
 * problem would also hide the second, and the second is often the interesting one.
 */

export interface Scenario {
  id: string;
  /** The clause of §13.10, transcribed. Checked against the blueprint by the matrix test. */
  blueprint: string;
  /** What this scenario is really asking, in one sentence. */
  asks: string;
  reach: 'ci' | 'ci-and-device';
  /** The Maestro flow that answers the hardware half, or null when no flow can. */
  device: string | null;
  /** What only hardware can settle. Empty when nothing is outstanding. */
  awaits: string;
  /**
   * Run it.
   *
   * `extra` is the mutation harness's seam and nothing else ever passes it: a deliberate bug is
   * layered onto whatever seams the scenario already has, so the mutation matrix measures this
   * scenario rather than a mutable imitation of it.
   */
  run(extra?: Hooks): Promise<Outcome>;
}

export interface Outcome {
  world: World;
  problems: IntegrityProblem[];
  /** Anything worth printing next to a pass. Kept short; this is not a log. */
  notes: string[];
}

/** Claims a scenario can falsify that are not properties of the two records agreeing. */
export const SCENARIO_CLAIMS = {
  entryKeepsWorking: {
    id: 'entry-works-with-no-connection',
    claim:
      'A measurement recorded with no connection is stored and queued before the operator lets go ' +
      'of the tablet.',
    loss:
      'An operator who is told to write on paper. The station app’s whole justification is that ' +
      'they never have to.',
  },
  entryIsNeverSwallowed: {
    id: 'every-entry-the-operator-made-is-queued',
    claim: 'Every command the operator issued produced a queued event with an id of its own.',
    loss:
      'A measurement the tablet accepted and silently discarded as a repeat of another one. The ' +
      'operator saw it recorded; nobody else ever will.',
  },
  survivesRestart: {
    id: 'the-queue-survives-the-app-being-killed',
    claim:
      'Everything recorded before the app was killed is still in the queue when it comes back, ' +
      'with the same ids.',
    loss:
      'A morning of measurements gone with the process, on a tablet Android killed to reclaim ' +
      'memory while it was in a pocket.',
  },
  allDelivered: {
    id: 'everything-recorded-reaches-the-clinic-exactly-once',
    claim:
      'When the run finishes, every measurement the operator took is in the ledger exactly once ' +
      'and the queue is empty.',
    loss: 'Zero data loss and no duplication, which is what §13.10 is a list of ways to break.',
  },
  refusalIsVisible: {
    id: 'a-refusal-is-listed-for-a-person-and-never-counted-as-delivered',
    claim:
      'An event the clinic refused is on the sync screen with a reason and an act, and is still ' +
      'counted as undelivered.',
    loss:
      'A refusal that is neither delivered nor visible: the measurement is on the tablet, the ' +
      'indicator is quiet, and nobody knows to do anything.',
  },
  timestampsUntouched: {
    id: 'the-moment-a-measurement-was-taken-is-never-rewritten',
    claim:
      'What the clinic receives as `occurred_at` is what the device’s clock said at the moment of ' +
      'the measurement, however wrong that clock was.',
    loss:
      'A client forging the one field the server refuses to assign, and defeating the hold that ' +
      'exists so a person can say what time it actually was.',
  },
  nothingEmptiedToRecover: {
    id: 'nothing-leaves-the-queue-to-recover-from-a-failure',
    claim:
      'A failed attempt — no signal, a server error, a refusal about the request rather than the ' +
      'events — changes when an entry is next offered and never whether it still exists.',
    loss:
      'A queue that empties itself on a bad connection. The tablet looks healthy afterwards and ' +
      'the morning is gone, which is the failure that leaves no trace anywhere to find later.',
  },
  queueSurvivesSession: {
    id: 'a-lost-session-does-not-touch-the-queue',
    claim: 'A refused session halts sync and changes nothing about what is queued.',
    loss: 'A token expiry emptying a tablet. §13.10 names this one explicitly for that reason.',
  },
  backlogHandedOver: {
    id: 'a-revoked-tablet-hands-its-backlog-to-the-quarantine',
    claim:
      'A device the clinic has revoked delivers every queued event to the quarantine, has none of ' +
      'them accepted, and only then stops.',
    loss:
      'A tablet revoked at nine with a morning of real measurements on it, which then go in a ' +
      'drawer. Accepting them would defeat the revocation; dropping them loses the morning.',
  },
  nothingHalfWritten: {
    id: 'a-full-device-leaves-nothing-behind-rather-than-part-of-an-entry',
    claim:
      'A command that fails for want of space writes no event, no projection and no outbox row, ' +
      'and the operator is told.',
    loss:
      'A screen showing a weight the queue does not contain. The operator walks away; the value ' +
      'never reaches the clinic; nothing about the tablet looks wrong.',
  },
  batchesRespectTheLimit: {
    id: 'no-batch-is-larger-than-the-clinic-will-accept',
    claim: 'Every batch sent is within the client limit, which is well inside the server’s.',
    loss:
      'A backlog that cannot be delivered at all: every attempt refused for its size, retried at ' +
      'the same size, for ever.',
  },
} as const satisfies Record<string, IntegrityClaim>;

/** One vocabulary. The mutation matrix names claims from either half of it. */
export const ALL_CLAIMS = { ...INTEGRITY_CLAIMS, ...SCENARIO_CLAIMS };

// --- collecting ---

interface Checker {
  problems: IntegrityProblem[];
  notes: string[];
  that(claim: IntegrityClaim, ok: boolean, detail: string): void;
  note(line: string): void;
}

function checker(): Checker {
  const problems: IntegrityProblem[] = [];
  const notes: string[] = [];
  return {
    problems,
    notes,
    that(claim, ok, detail) {
      if (!ok) problems.push({ claim: claim.id, detail });
    },
    note(line) {
      notes.push(line);
    },
  };
}

async function finish(world: World, check: Checker): Promise<Outcome> {
  return {
    world,
    problems: [...check.problems, ...(await world.problems({ quiescent: true }))],
    notes: check.notes,
  };
}

/**
 * A radio with a switch.
 *
 * Airplane mode is not a lossy network and must not be modelled as one: nothing arrives, every
 * time, until somebody turns it back on. A rate would occasionally let a request through and the
 * scenario would be testing something else.
 */
function radio(base: SyncTransport): { transport: SyncTransport; on(state: boolean): void } {
  let online = true;
  const guard = <T>(call: () => Promise<T>): Promise<T> =>
    online ? call() : Promise.reject(new NetworkError(new Error('airplane mode')));
  return {
    on(state) {
      online = state;
    },
    transport: {
      push: (body) => guard(() => base.push(body)),
      receipt: (id) => guard(() => base.receipt(id)),
      pull: (since, limit) => guard(() => base.pull(since, limit)),
      reference: () => guard(() => base.reference()),
      state: () => guard(() => base.state()),
    },
  };
}

/** How many of the operator's own entries the clinic holds in its ledger. */
function delivered(world: World): number {
  return world.entries.filter((entry) => world.clinic.ledger.has(entry.eventId)).length;
}

async function outboxSize(world: World): Promise<number> {
  return (await world.store.all({ table: TABLES.outbox })).length;
}

/** One world, with the scenario's own seams and the mutation harness's composed over them. */
function makeWorld(extra: Hooks, options: WorldOptions = {}): Promise<World> {
  return createWorld({ ...options, hooks: mergeHooks(options.hooks ?? {}, extra) });
}

// --- the ten ---

export const SCENARIOS: readonly Scenario[] = [
  {
    id: 'airplane-mode-mid-entry',
    blueprint: 'Airplane mode mid-entry',
    asks: 'Does the tablet keep taking measurements with no signal, and deliver them all after?',
    reach: 'ci-and-device',
    device: 'offline/airplane-mid-entry.yaml',
    awaits:
      'That the radio really is off and that NetInfo reports it. In CI the transport refuses; on ' +
      'a tablet the operating system does.',
    async run(extra = {}) {
      const check = checker();
      let link: (state: boolean) => void = () => {};
      const world = await makeWorld(extra, {
        hooks: {
          transport: (base) => {
            const switchable = radio(base);
            link = switchable.on;
            return switchable.transport;
          },
        },
      });

      // The signal goes half way through the operator's work, which is the scenario's whole point:
      // the first entry was taken with a connection and the next three were not.
      await world.record();
      await world.sync();
      link(false);
      for (let index = 0; index < 3; index += 1) await world.record();

      const queued = await readOutbox(world.store);
      check.that(
        SCENARIO_CLAIMS.entryKeepsWorking,
        queued.length === 3,
        `three entries were taken with no signal and ${queued.length} reached the queue`,
      );
      check.that(
        SCENARIO_CLAIMS.entryIsNeverSwallowed,
        world.entries.every((entry) => !entry.duplicate),
        'the command handler treated one of the operator’s entries as a repeat',
      );

      const offline = await world.sync();
      check.that(
        SCENARIO_CLAIMS.nothingEmptiedToRecover,
        offline.failure === 'offline' && (await outboxSize(world)) === 3,
        `a failed attempt with no signal left ${await outboxSize(world)} of 3 entries queued`,
      );

      link(true);
      await world.drain();
      check.that(
        SCENARIO_CLAIMS.allDelivered,
        delivered(world) === 4 && (await outboxSize(world)) === 0,
        `${delivered(world)} of 4 reached the clinic, ${await outboxSize(world)} still queued`,
      );
      check.note(`${world.clinic.pushes.length} batches`);
      return finish(world, check);
    },
  },

  {
    id: 'app-killed-with-a-full-queue',
    blueprint: 'kill the app with a full queue and relaunch',
    asks: 'Does an app killed with a batch in flight come back and find out what happened to it?',
    reach: 'ci-and-device',
    device: 'offline/killed-with-a-full-queue.yaml',
    awaits:
      'That SQLCipher’s committed state is what survives, and that Expo’s cold start reopens it ' +
      'with the key from the Keystore.',
    async run(extra = {}) {
      const check = checker();
      // The interesting kill is not "the app stopped": it is "the app stopped after the clinic
      // had taken the batch and before it could be told". Anything else is a queue sitting still.
      let killAfterPush = true;
      const world = await makeWorld(extra, {
        batchLimit: 20,
        hooks: {
          transport: (base) => ({
            ...base,
            push: async (body) => {
              const receipt = await base.push(body);
              if (killAfterPush) {
                killAfterPush = false;
                throw new NetworkError(new Error('the app went away before the answer arrived'));
              }
              return receipt;
            },
          }),
        },
      });

      for (let index = 0; index < 40; index += 1) await world.record();
      const before = await outboxSize(world);
      await world.sync();
      await world.restart();

      const after = await readOutbox(world.store);
      check.that(
        SCENARIO_CLAIMS.survivesRestart,
        after.length === before,
        `${before} entries were queued before the kill and ${after.length} after it`,
      );
      check.that(
        SCENARIO_CLAIMS.survivesRestart,
        after.some((row) => row.state === 'IN_FLIGHT' && row.batchId !== null),
        'the relaunched app does not know which batch it was sending',
      );

      await world.drain();
      check.that(
        SCENARIO_CLAIMS.allDelivered,
        delivered(world) === 40 && (await outboxSize(world)) === 0,
        `${delivered(world)} of 40 reached the clinic after the relaunch`,
      );
      check.note(`ledger ${world.clinic.ledger.size} for 40 entries`);
      return finish(world, check);
    },
  },

  {
    id: 'two-hundred-queued-events',
    blueprint: 'sync 200 queued events at once',
    asks: 'Does a morning’s backlog go in batches the clinic will take, in the order it was taken?',
    reach: 'ci',
    device: null,
    awaits:
      'Nothing about correctness. What a tablet adds is how long 200 events take over a clinic’s ' +
      'Wi-Fi and what it costs the battery, which is CP93’s question and not a correctness one.',
    async run(extra = {}) {
      const check = checker();
      const world = await makeWorld(extra, { pullPage: 50 });

      // Four patients, because a single aggregate would make the per-record ordering rule
      // vacuous — everything on one record in one order is trivially in order.
      for (let index = 0; index < 200; index += 1) {
        await world.record({ patientId: `patient-${(index % 4) + 1}` });
      }
      await world.drain();

      // And then the rest of the clinic's morning arrives — sixty events this device has never
      // seen, over two pages. Appended *after* the push rather than before it, deliberately: a
      // cursor that jumps past its own writes skips nothing, because this device already holds
      // them, so a pull that only ever covers this tablet's own events cannot tell a correct
      // cursor from one that runs ahead.
      for (let index = 0; index < 60; index += 1) {
        world.clinic.append({
          event_id: `web-${index}`,
          aggregate_type: 'patient',
          aggregate_id: `patient-${9 + (index % 3)}`,
          patient_id: `patient-${9 + (index % 3)}`,
          event_type: 'OBSERVATION_RECORDED',
          event_version: 1,
          occurred_at: new Date(world.clock.now() - 60_000 + index).toISOString(),
          payload: { code: 'BODY_HEIGHT', value: 160 + index, unit: 'cm' },
        });
      }
      await world.drain();

      check.that(
        SCENARIO_CLAIMS.allDelivered,
        delivered(world) === 200 && (await outboxSize(world)) === 0,
        `${delivered(world)} of 200 reached the clinic`,
      );
      const oversized = world.clinic.pushes.filter((body) => body.events.length > BATCH_LIMIT);
      check.that(
        SCENARIO_CLAIMS.batchesRespectTheLimit,
        oversized.length === 0,
        `${oversized.length} batches were larger than the client limit of ${BATCH_LIMIT}`,
      );
      const metrics = await readMetrics(world.store);
      const pulled = await world.store.all({
        table: TABLES.localEvents,
        where: [{ column: 'origin', op: '=', value: 'SERVER' }],
      });
      check.that(
        INTEGRITY_CLAIMS.cursorNotAhead,
        pulled.length === 60,
        `${pulled.length} of the other station's 60 events reached this tablet`,
      );
      check.note(`${world.clinic.pushes.length} batches, cursor ${metrics.cursor}`);
      return finish(world, check);
    },
  },

  {
    id: 'one-rejection-in-fifty',
    blueprint: 'server rejects one event in a batch of fifty',
    asks: 'Do the other forty-nine land, and does the refusal become a person’s problem rather than a loss?',
    reach: 'ci',
    device: null,
    awaits:
      'That the sentence on the screen is one a clinical assistant can act on, in Bangla, which ' +
      'is a person reading it rather than a test.',
    async run(extra = {}) {
      const check = checker();
      const world = await makeWorld(extra, { batchLimit: 50 });
      for (let index = 0; index < 50; index += 1) await world.record();

      // Two refusals rather than §13.10's one, because CP67 gave an operator two acts and the
      // scenario has to reach both. One is a value they can look at and restate; the other is one
      // nothing on this tablet answers, and the honest thing to do with it is tell somebody.
      const corrected = world.entries[16];
      const escalated = world.entries[31];
      if (!corrected || !escalated) throw new Error('the scenario needs fifty entries');
      const refusals = new Set([corrected.eventId, escalated.eventId]);
      world.clinic.rejectWhen = (event) =>
        refusals.has(event.event_id)
          ? { code: REASON_CODES.invalidPayload, reason: 'value 999 out of range for BODY_WEIGHT' }
          : null;

      await world.drain();

      check.that(
        SCENARIO_CLAIMS.allDelivered,
        delivered(world) === 48,
        `${delivered(world)} of the other 48 reached the clinic`,
      );
      const rows = await readOutbox(world.store);
      const items = itemsOf(rows);
      check.that(
        SCENARIO_CLAIMS.refusalIsVisible,
        items.needsYou.length === 2 && items.needsYou.every((item) => refusals.has(item.eventId)),
        `${items.needsYou.length} entries are on the operator's list, and 2 were refused`,
      );
      check.that(
        SCENARIO_CLAIMS.refusalIsVisible,
        (await readCounts(world.store)).total === 2,
        'the refusals are not counted as undelivered',
      );

      // The first act: restate the measurement. A new event with a new id that says which one it
      // replaces, and the refused row is discharged in the same transaction.
      const correction = await resubmit(
        world.store,
        corrected.eventId,
        { value: 79, unit: 'kg' },
        { newId: () => `${corrected.eventId}-corrected`, now: world.clock.now },
      );
      check.that(
        SCENARIO_CLAIMS.refusalIsVisible,
        correction.outcome === 'resubmitted',
        `correcting the refused entry answered ${correction.outcome}`,
      );

      // The second act: hand it on. This sends nothing and must send nothing — there is no route
      // on the wire that carries a refused event to a person — so the entry stays exactly where it
      // is, and the one thing that must never happen is the tablet quietly offering it again.
      const handedOn = await escalate(world.store, escalated.eventId, { now: world.clock.now });
      check.that(
        SCENARIO_CLAIMS.refusalIsVisible,
        handedOn === 'escalated',
        `escalating the second refusal answered ${handedOn}`,
      );

      const sentBefore = world.clinic.pushes.length;
      await world.drain();
      const carriedAgain = world.clinic.pushes
        .slice(sentBefore)
        .some((body) => body.events.some((event) => event.event_id === escalated.eventId));
      check.that(
        INTEGRITY_CLAIMS.noSecondDelivery,
        !carriedAgain,
        'an entry a person had escalated was sent to the clinic again',
      );
      check.that(
        SCENARIO_CLAIMS.allDelivered,
        world.clinic.ledger.size === 49,
        `the clinic holds ${world.clinic.ledger.size} events: 48 accepted and 1 correction`,
      );
      check.that(
        SCENARIO_CLAIMS.refusalIsVisible,
        (await readCounts(world.store)).escalated === 1,
        'the escalated entry stopped being counted as undelivered',
      );
      check.note('2 refused, 1 corrected, 1 escalated, 49 in the ledger');
      return finish(world, check);
    },
  },

  {
    id: 'device-clock-three-hours-wrong',
    blueprint: 'device clock set 3 hours wrong',
    asks: 'Does a wrong clock get reported rather than silently corrected, and does order survive fixing it?',
    reach: 'ci-and-device',
    device: null,
    awaits:
      'Setting the tablet’s own clock, which no Maestro flow can do. `adb shell date` and a run ' +
      'of the airplane flow; the checklist in mobile/maestro/offline/README.md has the command.',
    async run(extra = {}) {
      const check = checker();
      const world = await makeWorld(extra);

      // Part one: three hours ahead. The clinic holds anything dated that far in the future,
      // because the alternative is a timeline nobody can trust, and the client must not "help".
      world.clock.setAheadOfClinic(3 * 60 * 60_000);
      const early = await world.record();
      await world.record();
      await world.drain();

      const heldOne = world.clinic.held.find((row) => row.event_id === early.eventId);
      check.that(
        SCENARIO_CLAIMS.timestampsUntouched,
        heldOne?.reason_code === REASON_CODES.clockImplausible,
        `the clinic answered ${heldOne?.reason_code ?? 'nothing'} for an entry three hours ahead`,
      );
      const localEarly = (
        await world.store.all({
          table: TABLES.localEvents,
          where: [{ column: 'event_id', op: '=', value: early.eventId }],
        })
      )[0];
      check.that(
        SCENARIO_CLAIMS.timestampsUntouched,
        heldOne?.event.occurred_at === String(localEarly?.occurred_at),
        'the time the clinic received is not the time the device recorded',
      );
      const skewed = await readMetrics(world.store);
      check.that(
        SCENARIO_CLAIMS.timestampsUntouched,
        skewed.skew.level === 'wrong' && skewed.skew.entriesWouldBeHeld,
        `the tablet reports its clock as ${skewed.skew.level} while three hours out`,
      );

      // Part two: an operator corrects the clock in the middle of the session. The correction is
      // small enough that both entries are accepted — which is what makes the ordering question
      // real, because two events on one record now have `occurred_at` in the opposite order to
      // the order the operator worked in.
      world.clock.setAheadOfClinic(4 * 60_000);
      const beforeCorrection = await world.record();
      world.clock.correct();
      const afterCorrection = await world.record();
      await world.drain();

      const first = world.clinic.ledger.get(beforeCorrection.eventId);
      const second = world.clinic.ledger.get(afterCorrection.eventId);
      check.that(
        INTEGRITY_CLAIMS.batchOrder,
        first !== undefined && second !== undefined && first.occurred_at > second.occurred_at,
        'the scenario failed to invert the two timestamps, so it is proving nothing',
      );
      check.that(
        INTEGRITY_CLAIMS.batchOrder,
        first !== undefined && second !== undefined && first.global_seq < second.global_seq,
        'a corrected clock put the later entry into the ledger first',
      );
      check.note('2 held for the clock, 2 accepted after it was corrected');
      return finish(world, check);
    },
  },

  {
    id: 'token-expires-while-offline',
    blueprint:
      'token expires while offline (must refresh cleanly on reconnect, not lose the queue)',
    asks: 'Does an expired session stop sync without touching a single queued measurement?',
    reach: 'ci',
    device: null,
    awaits:
      'The refresh itself, which is the API client’s and happens below this engine. On a tablet ' +
      'it is a real token, a real clock and a real 401.',
    async run(extra = {}) {
      const check = checker();
      const world = await makeWorld(extra);
      for (let index = 0; index < 12; index += 1) await world.record();

      // Offline all morning; the refresh token expires meanwhile. The client learns this the
      // moment it reconnects, and the only correct thing to do with the queue is nothing.
      await noteSessionLost(world.store);
      const halted = await world.sync();
      check.that(
        SCENARIO_CLAIMS.queueSurvivesSession,
        halted.halted === HALT_REASONS.sessionLost && (await outboxSize(world)) === 12,
        `a lost session left ${await outboxSize(world)} of 12 entries queued`,
      );
      check.that(
        SCENARIO_CLAIMS.queueSurvivesSession,
        statusOf(await readMetrics(world.store)) === 'halted',
        'the operator is not told that sync has stopped',
      );
      check.that(
        SCENARIO_CLAIMS.allDelivered,
        world.clinic.pushes.length === 0,
        'the tablet went on sending against a session the clinic has refused',
      );

      // Somebody signs in. That clears this halt and only this one.
      const resumed = await resumeAfterSignIn(world.store);
      check.that(
        SCENARIO_CLAIMS.queueSurvivesSession,
        resumed,
        'signing in again did not clear a halt that signing in fixes',
      );
      await world.drain();
      check.that(
        SCENARIO_CLAIMS.allDelivered,
        delivered(world) === 12 && (await outboxSize(world)) === 0,
        `${delivered(world)} of 12 reached the clinic after the operator signed in again`,
      );
      check.note('12 queued across a session expiry, 12 delivered');
      return finish(world, check);
    },
  },

  {
    id: 'device-revoked-while-offline',
    blueprint: 'device revoked while offline',
    asks: 'Does a revoked tablet hand its morning to the quarantine instead of keeping it?',
    reach: 'ci',
    device: null,
    awaits:
      'An administrator revoking a real enrolment while a real tablet is out of range, which is ' +
      'an end-to-end rehearsal rather than a test.',
    async run(extra = {}) {
      const check = checker();
      const world = await makeWorld(extra, { batchLimit: 5 });
      for (let index = 0; index < 10; index += 1) await world.record();

      // Revoked at nine, offline since eight. The tablet does not know yet.
      world.clinic.deviceStatus = 'revoked';
      const reports = await world.drain({ ticks: 8 });

      check.that(
        SCENARIO_CLAIMS.backlogHandedOver,
        world.clinic.held.length === 10,
        `${world.clinic.held.length} of 10 entries reached the clinic's quarantine`,
      );
      check.that(
        SCENARIO_CLAIMS.backlogHandedOver,
        world.clinic.ledger.size === 0,
        `${world.clinic.ledger.size} events from a revoked tablet were accepted into the ledger`,
      );
      check.that(
        SCENARIO_CLAIMS.backlogHandedOver,
        reports[reports.length - 1]?.halted === HALT_REASONS.deviceRefused,
        'the tablet did not stop once it had handed everything over',
      );
      check.that(
        SCENARIO_CLAIMS.refusalIsVisible,
        (await readCounts(world.store)).held === 10,
        'entries held at the clinic are not shown as still undelivered',
      );
      check.note('10 handed to the quarantine, 0 accepted, then halted');
      return finish(world, check);
    },
  },

  {
    id: 'duplicate-submission-of-a-batch',
    blueprint: 'duplicate submission of an entire batch',
    asks: 'Does the same batch arriving twice produce one set of measurements, and an empty queue?',
    reach: 'ci',
    device: null,
    awaits: '',
    async run(extra = {}) {
      const check = checker();
      const world = await makeWorld(extra, { batchLimit: 8 });
      for (let index = 0; index < 8; index += 1) await world.record();

      // The connection is cut half way through the batch. The clinic has opened the batch row,
      // written four events into the ledger and closed nothing, so its receipt says `closed:
      // false` — which is the client's instruction to send the batch again **under the same id**
      // rather than start a fresh one. The whole batch therefore arrives twice, and the four that
      // landed the first time must come back `DUPLICATE` rather than as four new measurements.
      world.clinic.crashAfter = 4;
      await world.drain();

      const ids = world.clinic.pushes.map((body) => body.batch_id);
      check.that(
        INTEGRITY_CLAIMS.noSecondDelivery,
        ids.length >= 2 && ids[0] === ids[1],
        `the batch was re-sent under ${ids[1] ?? 'no id'} rather than under ${ids[0] ?? 'its own'}`,
      );
      const second = world.clinic.pushes[1];
      check.that(
        SCENARIO_CLAIMS.allDelivered,
        second?.events.length === 8,
        `the second submission carried ${second?.events.length ?? 0} of the 8 events`,
      );
      check.that(
        SCENARIO_CLAIMS.allDelivered,
        world.clinic.ledger.size === 8 && (await outboxSize(world)) === 0,
        `the clinic holds ${world.clinic.ledger.size} events after a batch of 8 arrived twice`,
      );
      const answers = world.clinic.batches.get(ids[0] ?? '')?.results ?? [];
      check.that(
        SCENARIO_CLAIMS.allDelivered,
        answers.filter((one) => one.outcome === 'DUPLICATE').length === 4,
        `the second submission produced ${answers.filter((one) => one.outcome === 'DUPLICATE').length} duplicates, not 4`,
      );
      check.note(`${ids.length} pushes, ${world.clinic.ledger.size} events, 4 duplicates`);
      return finish(world, check);
    },
  },

  {
    id: 'flaky-network',
    blueprint: 'flaky network (10% packet loss, 3s latency)',
    asks: 'Does a bad connection cost time and nothing else?',
    reach: 'ci-and-device',
    device: 'offline/flaky-network.yaml',
    awaits:
      'Real loss on a real radio. The flow runs the entry half; shaping the network is the ' +
      'emulator’s or the access point’s job, and the README carries both commands.',
    async run(extra = {}) {
      const check = checker();
      // One seed, three streams, no wall clock: a failure here is repeatable from the number in
      // the message and from nothing else. See `chaos.ts`.
      const world = await makeWorld(extra, {
        seed: 20_260_907,
        profile: FLAKY_NETWORK,
        batchLimit: 15,
      });

      for (let index = 0; index < 60; index += 1) {
        await world.record({ patientId: `patient-${(index % 3) + 1}` });
      }
      for (let index = 0; index < 8; index += 1) {
        world.clinic.append({
          event_id: `web-flaky-${index}`,
          aggregate_type: 'patient',
          aggregate_id: 'patient-9',
          patient_id: 'patient-9',
          event_type: 'OBSERVATION_RECORDED',
          event_version: 1,
          occurred_at: new Date(world.clock.now() - 30_000).toISOString(),
          payload: { code: 'BODY_HEIGHT', value: 150 + index, unit: 'cm' },
        });
      }

      await world.drain({ ticks: 40 });

      check.that(
        SCENARIO_CLAIMS.allDelivered,
        delivered(world) === 60 && (await outboxSize(world)) === 0,
        `${delivered(world)} of 60 got through a bad connection — ${world.chaos.replay()}`,
      );
      const metrics = await readMetrics(world.store);
      check.that(
        INTEGRITY_CLAIMS.cursorNotAhead,
        metrics.cursor >= 60,
        `the cursor stopped at ${metrics.cursor} — ${world.chaos.replay()}`,
      );
      check.note(`${world.clinic.pushes.length} pushes for 60 entries · ${world.chaos.replay()}`);
      return finish(world, check);
    },
  },

  {
    id: 'storage-full',
    blueprint: 'storage full',
    asks: 'Does a full tablet refuse the entry outright rather than half-recording it?',
    reach: 'ci-and-device',
    device: null,
    awaits:
      'A device that is genuinely out of space, with SQLCipher raising SQLITE_FULL rather than ' +
      'this driver raising its own error. Fill the tablet, take a measurement, read the screen.',
    async run(extra = {}) {
      const check = checker();
      // Shared with the store, and mutable on purpose: the clinic day that matters is the one
      // where the disk fills up and is then freed — the operator is told, deletes some photographs,
      // and the measurement they were told was not saved has to be recordable afterwards.
      const faults: { full?: boolean } = {};
      const world = await makeWorld(extra, { faults });

      for (let index = 0; index < 5; index += 1) await world.record();
      await world.drain();
      const settled = await world.store.all({ table: TABLES.localEvents });

      faults.full = true;
      let refused = false;
      try {
        await world.record();
      } catch {
        refused = true;
      }
      check.that(
        SCENARIO_CLAIMS.nothingHalfWritten,
        refused,
        'a command on a full device resolved as though it had been saved',
      );
      const events = await world.store.all({ table: TABLES.localEvents });
      const outbox = await world.store.all({ table: TABLES.outbox });
      const projections = await world.store.all({ table: TABLES.projections });
      check.that(
        SCENARIO_CLAIMS.nothingHalfWritten,
        events.length === settled.length && outbox.length === 0,
        `a refused command left ${events.length - settled.length} events and ${outbox.length} outbox rows behind`,
      );
      check.that(
        SCENARIO_CLAIMS.nothingHalfWritten,
        projections.every((row) => events.some((event) => event.event_id === row.event_id)),
        'a refused command left a projection with no event behind it',
      );

      // Space is freed, two more measurements are taken, and the device fills up again before
      // they can be sent. A sync now cannot write down the clinic's answer, so it must not ask
      // the question — and it must not lose the two entries in the process.
      faults.full = false;
      const late = await world.record();
      check.that(
        SCENARIO_CLAIMS.entryKeepsWorking,
        !late.duplicate,
        'the measurement refused for want of space could not be recorded once there was space',
      );
      await world.record();

      faults.full = true;
      const stopped = await syncOnce(world.deps);
      check.that(
        SCENARIO_CLAIMS.nothingHalfWritten,
        stopped.failure === 'client' && stopped.accepted === 0,
        `a sync on a full device reported ${stopped.failure ?? 'no failure'} and accepted ${stopped.accepted}`,
      );
      check.that(
        SCENARIO_CLAIMS.nothingEmptiedToRecover,
        (await outboxSize(world)) === 2,
        `a sync that could not write left ${await outboxSize(world)} of 2 entries queued`,
      );

      faults.full = false;
      await world.drain();
      check.that(
        SCENARIO_CLAIMS.allDelivered,
        (await outboxSize(world)) === 0 && delivered(world) === 7,
        `${delivered(world)} of 7 reached the clinic after the device had room again`,
      );
      check.note('5 delivered, 1 refused for space, 2 recorded and delivered after');
      return finish(world, check);
    },
  },
];

export function scenarioById(id: string): Scenario {
  const found = SCENARIOS.find((scenario) => scenario.id === id);
  if (!found) throw new Error(`no such scenario: ${id}`);
  return found;
}
