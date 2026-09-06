/**
 * `observations` — a recorded value, drawn the one way this application draws one.
 *
 * `DualUnitValue` (CP44, [R-08]) is the clinical unit with the patient-familiar equivalent
 * beneath it. `ObservationValue` (CP62) is that nested inside CP61's attribution, decided
 * once here rather than at every call site: a value drawn without a name against it is the
 * failure §4.2 exists to end, and four call sites each remembering to nest one component
 * inside the other is three chances to forget.
 *
 * The API module is the reading half — the current values, one code's whole history with the
 * replaced rows in it, and the code registry that says what a value *is* and which units it
 * may be entered in. **Nothing in it edits a value.** Correcting is writing a new value that
 * replaces the old one, which is a different act with a different row, and it lives in the
 * corrections feature.
 */
export { DualUnitValue, unitLabel } from './components/DualUnitValue';
export { ObservationValue, type ObservationValueProps } from './components/ObservationValue';
export {
  OBSERVATION_CODES_KEY,
  chainsOf,
  codeEntry,
  computedFromCurrent,
  derivedFrom,
  enteredUnitOf,
  enteredValueOf,
  getObservation,
  inputSeen,
  isCurrent,
  isReplaced,
  latest,
  listObservationCodes,
  listObservationHistory,
  listPatientObservations,
  observationHistoryKey,
  observationKey,
  original,
  patientObservationsKey,
  unitsFor,
  type MeasurementUnit,
  type Observation,
  type ObservationCategory,
  type ObservationChain,
  type ObservationCode,
  type ObservationStatus,
} from './api/observations';
