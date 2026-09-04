/**
 * The coded terminology picker (CP52).
 *
 * A station reaches this feature through here and nowhere else. What it gets is a picker
 * whose whole job is to hand back a **coding** — a system, a version and a code, together —
 * because that is what a diagnosis is in the record and a bare code is a string somebody
 * cannot read back in four years.
 *
 * The public surface is in three parts: `ConceptPicker`, the screen; the state machine in
 * `search.ts`, which is where every decision the picker makes actually lives and where the
 * out-of-order-answer rule is enforced structurally; and the four calls in `api.ts`, of which
 * `runSearch` is the one a picker uses — it chooses the favourites endpoint for an empty
 * query and returns failures rather than throwing them, so that a stale failure can be aged
 * out exactly like a stale result.
 */

export { ConceptPicker } from './ConceptPicker';
export {
  getConcept,
  listFavourites,
  listSystems,
  runSearch,
  searchConcepts,
  troubleOf,
} from './api';
export {
  DEBOUNCE_MS,
  MAX_RESULTS,
  REASONS,
  apply,
  atCap,
  busy,
  catalogueAbsent,
  clearSelection,
  codingOf,
  conceptHeading,
  conceptLabel,
  conceptRows,
  due,
  isSelected,
  issue,
  openPicker,
  reasonFor,
  requestVersion,
  retry,
  sameCoding,
  select,
  selectable,
  tierReason,
  typed,
  visible,
  type CodeSystem,
  type Coding,
  type Concept,
  type ConceptMapping,
  type ConceptRow,
  type Locale,
  type PickerState,
  type Reason,
  type SearchAnswer,
  type SearchRequest,
  type Tier,
  type Trouble,
} from './search';
