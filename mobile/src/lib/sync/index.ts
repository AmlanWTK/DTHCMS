/**
 * The offline write path and the sync engine (CP64, CP66, §13).
 *
 * # Why this is a library and not a feature
 *
 * It was a feature until the first station was converted, and the conversion is what proved it
 * wrong: `features/vitals` needed the command handler, the house rule says a feature is imported
 * through its `index.ts`, and that index re-exported a React Native screen. So one station's
 * *logic* could not be tested without dragging a component — and, through it, the whole React
 * Native import graph — into a test runner that deliberately never renders one.
 *
 * The rule was not the problem; the classification was. This is the application's data layer, the
 * plan's own words for it: *"the command handler that is the only write path in the app"*, beside
 * `lib/local-store`. Every station will depend on it, exactly as every station depends on the API
 * client. What stayed in `features/sync` is what is genuinely a feature: the screen that shows an
 * operator what has and has not reached the clinic.
 *
 * What is deliberately **not** exported, and must never be:
 *
 *   - anything that writes a projection directly. A screen that could would be a second write
 *     path, and the queue and the screen would be able to disagree about what was recorded;
 *   - anything that deletes an outbox row. The only thing that removes an event from the queue is
 *     the clinic saying it has it (or a person, deliberately, through `wipeEverything`);
 *   - a read that goes to the network. The station app reads from the local record, which is what
 *     makes it work in a corridor.
 */

export { issueCommand, type Command, type CommandDeps, type Issued } from './commands';
export {
  CHECK_INTERVAL_MS,
  DEFAULT_INTERVAL_MS,
  applyPulled,
  cacheCatalogue,
  checkWithClinic,
  createSyncEngine,
  halt,
  noteSessionLost,
  resume,
  resumeAfterSignIn,
  syncOnce,
  undeliveredCount,
  type CeilingCheck,
  type EngineDeps,
  type SchedulerDeps,
  type SyncEngine,
  type SyncReason,
  type SyncReport,
} from './engine';
export {
  CACHE_TTL_DAYS,
  DESTROYS_UNDELIVERED_WORK,
  lifecycleFor,
  purgeExpired,
  wipeAfterSignOut,
  wipeEverything,
  type LifecycleAction,
  type SessionPhase,
  type WipeReport,
} from './lifecycle';
export {
  appendSyncLog,
  eventsForPatient,
  readCatalogue,
  readCounts,
  readMetrics,
  readOutbox,
  readOutboxRow,
  readProjection,
  readProjections,
  readSyncLog,
  type Executor,
  type ProjectionRow,
  type SyncLogEntry,
} from './outbox';
export { PROJECTORS, projectionsFor, type AppliedEvent, type ProjectionWrite } from './projections';
export {
  BACKOFF_LADDER_MS,
  BATCH_LIMIT,
  CLOCK_NOTICE_MS,
  CLOCK_TOLERANCE_MS,
  HALT_REASONS,
  MAX_AUTH_FAILURES,
  MAX_BATCHES_PER_TICK,
  MAX_RETRY_AFTER_MS,
  PULL_PAGE,
  REASON_CODES,
  SERVER_MAX_BATCH,
  SYNC_QUARANTINE_FULL,
  TRIAGE_RETRY_MS,
  actionFor,
  backoffDelay,
  classifyFailure,
  controlFor,
  outboxRowOf,
  planFromReceipt,
  reasonKey,
  replaces,
  retryAfterDelay,
  retryAfterOf,
  retryable,
  selectBatch,
  skewReading,
  statusOf,
  toWireEvent,
  triageDelay,
  type EventAction,
  type FailureKind,
  type HaltReason,
  type Outcome,
  type OutboxRow,
  type PulledEvent,
  type ReceiptPlan,
  type ReferenceVersion,
  type SkewReading,
  type SyncControl,
  type SyncCounts,
  type SyncEvent,
  type SyncMetrics,
  type SyncPage,
  type SyncReceipt,
  type SyncResult,
  type SyncStatus,
} from './state';
export {
  httpTransport,
  type PushBody,
  type ReferenceAnswer,
  type SyncTransport,
} from './transport';
