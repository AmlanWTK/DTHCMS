/**
 * The one door into this directory (CP68).
 *
 * The mutation harness imports **this module** after resetting the registry, and everything a run
 * needs is reachable from here through ordinary static imports. That is the whole reason the file
 * exists: one import means one module graph, and one module graph is the difference between a
 * mutation test that measures the engine and one that measures a mismatch between two copies of
 * the API client's error classes.
 */

export { CALM, FLAKY_NETWORK, createChaos, type ChaosProfile } from './chaos';
export {
  CLINIC_OPENS,
  DEVICE,
  PATIENT,
  createWorld,
  type Hooks,
  type World,
  type WorldOptions,
} from './harness';
export { MUTATIONS, mutationById, type Mutation } from './mutations';
export {
  ALL_CLAIMS,
  SCENARIOS,
  SCENARIO_CLAIMS,
  scenarioById,
  type Outcome,
  type Scenario,
} from './scenarios';
export {
  INTEGRITY_CLAIMS,
  integrityProblems,
  integrityReport,
  type IntegrityClaim,
  type IntegrityProblem,
} from '../sync-integrity';
export { FakeClinic, seeded } from '../fake-sync-server';
