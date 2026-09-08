import { createDisk, createMemoryStore } from '../../src/lib/local-store/memory';
import { META_KEYS, MIGRATIONS, TABLES } from '../../src/lib/local-store/schema';
import { issueCommand } from '../../src/lib/sync/commands';
import { syncOnce } from '../../src/lib/sync/engine';
import { FakeClinic } from '../fake-sync-server';
import { INTEGRITY_CLAIMS, integrityReport, type IntegrityProblem } from '../sync-integrity';
import { createChaos, CALM, type Chaos, type ChaosProfile } from './chaos';
import { envSeed } from './env';

import type {
  LocalStore,
  MemoryFaults,
  Query,
  Row,
  Transaction,
  Write,
} from '../../src/lib/local-store';
import type { EngineDeps, SyncReport } from '../../src/lib/sync/engine';
import type { Command } from '../../src/lib/sync/commands';
import type { SyncTransport } from '../../src/lib/sync/transport';

/**
 * One tablet, one clinic, one clock (CP68).
 *
 * The §13.10 scenarios, the mutation harness and the soak all run against this, and that is the
 * point of it existing rather than each of the three building its own: a scenario written against
 * one world can be replayed under a deliberately broken engine without being rewritten, which is
 * what makes the mutation matrix a statement about *this* suite rather than about a suite written
 * to catch the mutations.
 *
 * Three things it owns that a plain `beforeEach` could not:
 *
 *  1. **The clock.** Nothing here sleeps. The device's clock, the clinic's clock and the latency
 *     the chaos harness charges for a slow answer are the same number, moved by hand. A test that
 *     needed a real timer would be a test whose result depends on how loaded the CI machine is,
 *     which is the flakiness CP68 names as its own risk.
 *  2. **The storage seam.** Every read and write passes through here, which is what lets the
 *     mutation harness drop an outbox row or corrupt a cursor *underneath* the engine — a
 *     deliberate bug injected where a real one would live, rather than a test asserting that a
 *     hand-written wrong value is wrong.
 *  3. **The two watches.** Some failures leave nothing behind. A cursor that goes backwards and
 *     comes forward again is one number either way; a row put in front of an operator while the
 *     clinic already had the event is cleared by the very next pull, having already asked somebody
 *     to restate a delivered measurement. Neither can be found afterwards, so both are recorded as
 *     they happen — every write to the cursor, and every row that ever entered a state a person is
 *     asked to act on — and checked against the clinic at the end.
 */

export interface Clock {
  /** What the tablet's clock says. Every `occurred_at` in the suite comes from here. */
  now(): number;
  advance(ms: number): void;
  /**
   * How far the tablet's clock is ahead of the clinic's.
   *
   * The one number the two are allowed to disagree about, and §13.10's fifth scenario is entirely
   * about what happens when it is large. Setting it moves the *clinic* rather than the device: the
   * measurements a device with a wrong clock has already taken keep the times it gave them, which
   * is the property the scenario is checking.
   */
  aheadOfClinic(): number;
  setAheadOfClinic(ms: number): void;
  /**
   * Somebody fixes the tablet's clock.
   *
   * The device's own time moves — that is what fixing a clock is — so entries taken after the
   * correction carry an **earlier** `occurred_at` than entries taken before it. That is not an
   * edge case dreamt up for a test; it is the ordinary consequence of correcting a clock in the
   * middle of a session, and it is exactly the case `selectBatch` orders by local sequence to
   * survive.
   */
  correct(): void;
}

/** A clinic day starts at eight. Fixed, because a suite whose data moves with the calendar drifts. */
export const CLINIC_OPENS = Date.parse('2026-09-07T02:00:00.000Z');

export interface Hooks {
  /**
   * Change a write on its way to the store, or swallow it.
   *
   * Returning `null` drops the write entirely, which is how "an event never reached the outbox"
   * is injected. Everything else returns the write, altered or not.
   */
  write?(write: Write): Write | null;
  /**
   * Replace the id the **command handler** is handed.
   *
   * Separate from the engine's `newId`, which also mints batch ids: a mutation about event
   * identity that quietly changed batch identity too would be two bugs in a trench coat, and the
   * matrix would not be able to say which one the suite caught.
   */
  commandId?(next: () => string): () => string;
  /** Wrap the transport, after the chaos wrapper. */
  transport?(base: SyncTransport): SyncTransport;
}

/**
 * Two sets of hooks over one world.
 *
 * A scenario often needs a seam of its own — a radio with a switch, a push whose answer is lost
 * once — and the mutation harness needs to add its deliberate bug on top without the scenario
 * knowing. Composing rather than replacing is what lets the mutation matrix run the *same*
 * scenario the matrix runs, rather than a simplified one written to be mutable.
 */
export function mergeHooks(own: Hooks, extra: Hooks): Hooks {
  const merged: Hooks = {};
  if (own.write || extra.write) {
    merged.write = (write) => {
      const first = own.write ? own.write(write) : write;
      if (first === null) return null;
      return extra.write ? extra.write(first) : first;
    };
  }
  if (own.commandId || extra.commandId) {
    merged.commandId = (next) => {
      const first = own.commandId ? own.commandId(next) : next;
      return extra.commandId ? extra.commandId(first) : first;
    };
  }
  if (own.transport || extra.transport) {
    merged.transport = (base) => {
      const first = own.transport ? own.transport(base) : base;
      return extra.transport ? extra.transport(first) : first;
    };
  }
  return merged;
}

export interface WorldOptions {
  seed?: number;
  profile?: Partial<ChaosProfile>;
  /** How many events go in one batch. Small numbers make batch behaviour visible in few events. */
  batchLimit?: number;
  pullPage?: number;
  /** How many of this device's events the clinic will hold at once. */
  quarantineCap?: number;
  faults?: MemoryFaults;
  hooks?: Hooks;
}

export interface Entry {
  eventId: string;
  patientId: string;
  /** True when the command handler said it had already seen this id. Always a bug if it is. */
  duplicate: boolean;
}

export interface World {
  readonly clock: Clock;
  readonly clinic: FakeClinic;
  readonly chaos: Chaos;
  /** The live store. Reassigned by `restart`, so read it through this rather than holding it. */
  readonly store: LocalStore;
  readonly deps: EngineDeps;
  /** Every write to the pull cursor, in the order it happened. */
  readonly cursorWrites: number[];
  /** The entries the operator made, in order. */
  readonly entries: Entry[];
  /** Take one measurement at a station. */
  record(over?: Partial<Command> & { patientId?: string }): Promise<Entry>;
  /** One synchronisation attempt. */
  sync(): Promise<SyncReport>;
  /** Kill the app and start it again over the same storage. */
  restart(): Promise<void>;
  /** Sync until the queue stops moving *and* one whole attempt has gone through cleanly. */
  drain(options?: { ticks?: number; everyMs?: number }): Promise<SyncReport[]>;
  /** The claims that failed, integrity and cursor together. */
  problems(options?: { quiescent?: boolean }): Promise<IntegrityProblem[]>;
}

/**
 * Build a world.
 *
 * Everything it needs is imported statically, and that is deliberate rather than incidental. The
 * mutation harness works by resetting the module registry, mocking one module and importing this
 * directory's entry point again — so a static import here resolves to the **mutated** copy, and
 * every object in one run comes from one module graph. That second half matters as much as the
 * first: an `ApiError` thrown by one copy of the API client is not an `instanceof` the other
 * copy's, and a client that classified a refusal as a network failure because two graphs were
 * mixed would be a very slow afternoon of debugging a bug that is not there.
 */
export async function createWorld(options: WorldOptions = {}): Promise<World> {
  const hooks = options.hooks ?? {};
  let at = CLINIC_OPENS;
  let ahead = 0;
  const clock: Clock = {
    now: () => at,
    advance: (ms) => {
      at += ms;
    },
    aheadOfClinic: () => ahead,
    setAheadOfClinic: (ms) => {
      ahead = ms;
    },
    correct: () => {
      at -= ahead;
      ahead = 0;
    },
  };

  const chaos = createChaos({
    // The scenario's own seed, unless somebody is debugging. `DTHCMS_CHAOS_SEED` is what the
    // sentence on a failed assertion tells them to set, and a message that named a variable
    // nothing reads would be a small lie in the one place a person is already having a bad
    // morning. Set it and every world in the run takes it, which is what "see that run again"
    // means when the interesting one was three scenarios in.
    seed: envSeed('DTHCMS_CHAOS_SEED', options.seed ?? 1),
    profile: options.profile ?? CALM,
    clock,
  });

  const disk = createDisk();
  const faults = options.faults ?? {};
  const cursorWrites: number[] = [];
  const alarmed: string[] = [];

  let raw = createMemoryStore({ disk, faults });
  await raw.open({ key: KEY });
  await raw.migrate(MIGRATIONS);
  let live = instrument(raw, { cursorWrites, alarmed }, hooks);

  const clinic = new FakeClinic({
    // The clinic's own clock, which is the true one. A device three hours ahead is three hours
    // ahead *of this*, and the difference is what the receipt reports back as the skew.
    now: () => at - ahead,
    signedBy: DEVICE,
    ...(options.quarantineCap === undefined ? {} : { quarantineCap: options.quarantineCap }),
  });

  let ids = 0;
  const plainId = () => `ev-${(ids += 1).toString().padStart(5, '0')}`;
  const commandId = hooks.commandId ? hooks.commandId(plainId) : plainId;

  const chaotic = chaos.attach(clinic);
  const transport = hooks.transport ? hooks.transport(chaotic) : chaotic;

  const entries: Entry[] = [];
  const world: World = {
    clock,
    clinic,
    chaos,
    cursorWrites,
    entries,

    get store() {
      return live;
    },

    deps: {
      get store() {
        return live;
      },
      transport,
      deviceId: DEVICE,
      now: () => at,
      // From the seed, like everything else: a run reproduced from its seed has to include the
      // backoff jitter, or two runs of "the same" seed take different numbers of ticks to drain.
      random: chaos.random,
      newId: plainId,
      ...(options.batchLimit === undefined ? {} : { batchLimit: options.batchLimit }),
      ...(options.pullPage === undefined ? {} : { pullPage: options.pullPage }),
    },

    async record(over = {}) {
      const patientId = over.patientId ?? PATIENT;
      const issued = await issueCommand(
        live,
        {
          aggregateType: 'patient',
          aggregateId: patientId,
          patientId,
          visitId: `visit-${patientId}`,
          eventType: 'OBSERVATION_RECORDED',
          eventVersion: 1,
          occurredAt: new Date(at).toISOString(),
          payload: {
            observation_id: plainId(),
            facility_id: 'facility-1',
            patient_id: patientId,
            code: 'BODY_WEIGHT',
            effective_at: new Date(at).toISOString(),
            source: 'STATION',
            value: 70 + entries.length,
            unit: 'kg',
          },
          ...over,
        },
        { now: () => at, newId: commandId },
      );
      // A second between entries, because there is one in a clinic. Two measurements at the same
      // millisecond with the same payload is what a duplicate looks like to the integrity check,
      // and manufacturing one here would make rule 2 unusable.
      at += 1_000;
      const entry = { eventId: issued.eventId, patientId, duplicate: issued.duplicate };
      entries.push(entry);
      return entry;
    },

    async sync() {
      return syncOnce(world.deps);
    },

    async restart() {
      await live.close();
      raw = createMemoryStore({ disk, faults });
      await raw.open({ key: KEY });
      await raw.migrate(MIGRATIONS);
      live = instrument(raw, { cursorWrites, alarmed }, hooks);
    },

    async drain(drainOptions = {}) {
      const ticks = drainOptions.ticks ?? 12;
      const everyMs = drainOptions.everyMs ?? 5 * 60_000;
      const reports: SyncReport[] = [];
      for (let tick = 0; tick < ticks; tick += 1) {
        const report = await world.sync();
        reports.push(report);
        const rows = await live.all({ table: TABLES.outbox });
        // Nothing left, or nothing left that any number of further attempts would move: a refusal
        // waits for a person and a held event waits for the clinic, so a drain loop that kept
        // going would just burn ticks proving the engine is right not to retry them.
        const movable = rows.filter((row) => {
          const state = String(row.state);
          return state !== 'NEEDS_ATTENTION' && state !== 'ESCALATED' && state !== 'HELD';
        });
        // An empty queue is not the end of an attempt. A tick that pushed the last batch may still
        // have lost its pull to the weather, and stopping there would leave the tablet holding a
        // cursor it never advanced — which a scenario would then report as a defect in the engine
        // rather than as a drain loop that stopped one tick early.
        if (movable.length === 0 && report.failure === null) break;
        at += everyMs;
      }
      return reports;
    },

    async problems(problemOptions = {}) {
      const found = await integrityReport(live, clinic, problemOptions);
      // A false alarm does not survive to be found at the end: the event is in the ledger, so the
      // very same attempt pulls it back down and `applyPulled` drops the row. Between those two
      // moments an operator holding the tablet is being asked to restate a measurement the clinic
      // already has — and if they do, the record gains a second reading of that patient. So what
      // is watched is the *moment a row was put in front of a person*, recorded as it happened,
      // and checked against the ledger afterwards.
      for (const eventId of alarmed) {
        if (!clinic.ledger.has(eventId)) continue;
        found.push({
          claim: INTEGRITY_CLAIMS.noFalseAlarm.id,
          detail: `${eventId}: the clinic has it and the operator was asked to deal with it`,
        });
      }
      for (let index = 1; index < cursorWrites.length; index += 1) {
        const previous = cursorWrites[index - 1] ?? 0;
        const current = cursorWrites[index] ?? 0;
        if (current < previous) {
          found.push({
            claim: INTEGRITY_CLAIMS.cursorMonotonic.id,
            detail: `the cursor went from ${previous} back to ${current}`,
          });
        }
      }
      return found;
    },
  };

  return world;
}

/** The device this suite is. One tablet, one enrolment, one key. */
export const DEVICE = 'device-1';
export const PATIENT = 'patient-1';
const KEY = 'ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554433221100';

/**
 * The store, with every write passing through the hooks and the cursor watched.
 *
 * Deliberately a wrapper rather than a subclass or a fork of `memory.ts`: the driver under test has
 * to be the same one every other test in this repository runs against, or a mutation caught here
 * would say nothing about the code that ships. What this adds is a seam, and the seam is only ever
 * used by a mutation that is announcing itself.
 */
interface Watches {
  /** Every value the pull cursor was actually set to, in order. */
  cursorWrites: number[];
  /** Every event id that was ever put in front of a person, whatever became of the row after. */
  alarmed: string[];
}

function instrument(base: LocalStore, watches: Watches, hooks: Hooks): LocalStore {
  function seen(write: Write): Write | null {
    // The hook first, then the record. What is watched has to be what actually lands in the store:
    // a mutation that corrupts the cursor on its way in would otherwise be invisible to the very
    // watch written to catch it, which is a check that agrees with itself and nothing else.
    const written = hooks.write ? hooks.write(write) : write;
    if (
      written !== null &&
      written.kind === 'insert' &&
      written.table === TABLES.syncMeta &&
      written.row.key === META_KEYS.cursor
    ) {
      watches.cursorWrites.push(Number(written.row.value ?? 0));
    }
    if (
      written !== null &&
      written.kind === 'update' &&
      written.table === TABLES.outbox &&
      (written.set.state === 'NEEDS_ATTENTION' || written.set.state === 'ESCALATED')
    ) {
      const named = written.where?.find((one) => one.column === 'event_id');
      if (named && 'value' in named && typeof named.value === 'string') {
        watches.alarmed.push(named.value);
      }
    }
    return written;
  }

  function filtered(write: Write | Write[]): Write[] {
    const writes = Array.isArray(write) ? write : [write];
    return writes.map(seen).filter((one): one is Write => one !== null);
  }

  return {
    get isOpen() {
      return base.isOpen;
    },
    open: (opened) => base.open(opened),
    migrate: (migrations) => base.migrate(migrations),
    all: (query: Query): Promise<Row[]> => base.all(query),
    run: async (write) => {
      const writes = filtered(write);
      return writes.length === 0 ? 0 : base.run(writes);
    },
    transaction: <T>(fn: (tx: Transaction) => Promise<T>) =>
      base.transaction((tx) =>
        fn({
          all: (query) => tx.all(query),
          run: async (write) => {
            const writes = filtered(write);
            return writes.length === 0 ? 0 : tx.run(writes);
          },
        }),
      ),
    wipe: () => base.wipe(),
    close: () => base.close(),
  };
}
