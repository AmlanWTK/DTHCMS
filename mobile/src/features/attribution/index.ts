/**
 * "Entered by" (CP61, §4.2, [R-03]).
 *
 * One component and one lookup, adopted by every screen that renders a clinical value read
 * back from the record. The public surface is deliberately narrow: the `of*` extractors, the
 * component, and the pure pieces the tests and the audit need. Nothing here lets a screen
 * assemble a provenance out of loose fields, because a value attributed to whoever *confirmed*
 * it rather than whoever entered it is worse than one with no attribution at all.
 */

export { EnteredBy } from './EnteredBy';

export { DIRECTORY_QUERY_KEY, DIRECTORY_STALE_MS, getDirectory, useDirectoryIndex } from './api';

export {
  CLINIC_UTC_OFFSET_MINUTES,
  KNOWN_ROLE_CODES,
  NO_DIRECTORY,
  NO_PROVENANCE,
  SOURCES,
  clinicDay,
  clockTime,
  deviceOf,
  indexDirectory,
  ofAllergy,
  ofAllergyAssertion,
  ofCounselingTick,
  ofCriticalAlert,
  ofDietEntry,
  ofExerciseAssessment,
  ofExercisePlan,
  ofHistoryItem,
  ofInstrumentResponse,
  observationFor,
  ofObservation,
  personOf,
  readingFor,
  readingOf,
  roleOf,
  sourceKnown,
  sourceReading,
  stationOf,
  whenOf,
  type AlertProvenance,
  type AllergyProvenance,
  type AssertionProvenance,
  type CorrectionKind,
  type CorrectionReading,
  type DietEntryProvenance,
  type Directory,
  type DirectoryIndex,
  type DirectoryPerson,
  type ExerciseAssessmentProvenance,
  type ExercisePlanProvenance,
  type HistoryItemProvenance,
  type InstrumentResponseProvenance,
  type Locale,
  type Named,
  type ObservationProvenance,
  type PersonKind,
  type PersonReading,
  type PlaceReading,
  type Provenance,
  type RawCorrection,
  type Reader,
  type Reading,
  type RoleReading,
  type SourceCode,
  type SourceReading,
  type SourceTone,
  type TickProvenance,
  type WhenReading,
} from './state';
