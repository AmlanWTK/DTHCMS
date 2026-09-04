/**
 * `terminology` — the coded picker, and the calls behind it (CP52).
 *
 * One component that matters and one that keeps it honest. `ConceptPicker` is how a
 * clinician turns "diabetes" into a coding; `ConceptChip` is how a coding is shown anywhere
 * afterwards, and it is exported rather than kept private because the diagnosis field (CP53)
 * and everything downstream of it show the same three facts. A second chip written locally
 * is how one of them ends up rendering `E11.9` with no version beside it.
 *
 * The pure helpers are public for the same reason the components share them: a screen that
 * needs a concept's Bangla label, or the reason a row ranked where it did, must get the same
 * answer this picker gives — not a second implementation that falls back differently.
 *
 * Nothing here writes, and nothing here is audited. There is no patient in a terminology
 * search.
 */
export {
  ConceptPicker,
  SEARCH_DEBOUNCE_MS,
  conceptQueryKey,
  type ConceptPickerProps,
} from './components/ConceptPicker';
export { ConceptChip, type ConceptChipProps } from './components/ConceptChip';
export {
  MAX_RESULTS,
  codingOf,
  conceptHeading,
  conceptLabel,
  listCodeSystems,
  listFavourites,
  readConcept,
  refusalText,
  searchConcepts,
  selectionOf,
  tierReason,
  type CodeSystem,
  type Coding,
  type Concept,
  type ConceptList,
  type ConceptMapping,
  type ConceptSelection,
  type SearchRequest,
  type TierReasonKey,
} from './api/terminology';
