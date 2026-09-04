/**
 * Station 4's medical history (CP53).
 *
 * A station reaches this feature through here and nowhere else. What it gets is the list of
 * everything currently believed about a patient, presented for **confirmation** — because
 * carrying a history forward is not the same act as somebody saying it is still true, and a
 * record that confused the two would eventually assert, in a signed clinical document, that a
 * patient is on a drug they stopped in March, with nobody able to say who claimed it.
 *
 * The public surface is in three parts: `HistoryStation`, the screen; `form.ts`, where every
 * decision this station makes actually lives — which fields a kind asks for, read off the
 * server's own rules; what the server would refuse, refused here first so the officer hears
 * it while the patient is still talking; and what carry-forward means, item by item; and the
 * calls in `api.ts`.
 *
 * Three things are deliberately absent from this list, and their absence is the design:
 *
 *   - **There is no confirm-all.** `confirmItem` takes one item id, there is no batch
 *     endpoint behind it, and nothing here loops over one. One action by a person must not
 *     become twenty assertions in a clinical record.
 *   - **There is no coding constructor that can produce two of the three fields.**
 *     `codingFrom` returns a whole coding or nothing, and `partialCoding` is what the screen
 *     refuses on.
 *   - **There is no single control that resolves and removes.** `toResolution` records a
 *     clinical fact and keeps the item; `toRemoval` is a correction and will not build a body
 *     without a reason.
 */

export { HistoryStation } from './HistoryStation';
export {
  amendItem,
  confirmItem,
  countUncoded,
  getItem,
  listKinds,
  listMedicalHistory,
  recordItem,
  removeItem,
  troubleOf,
  type HistoryKinds,
  type Trouble,
} from './api';
export {
  CARRY_REASONS,
  DURATION_PRESETS,
  HISTORY_FIELDS,
  ONSET_PRECISIONS,
  PROBLEMS,
  SEVERITIES,
  asks,
  canRecord,
  carryForward,
  carryReasonFor,
  carriedItem,
  coded,
  codingFrom,
  edit,
  emptyDraft,
  fieldsFor,
  fromKindsCatalogue,
  groupByKind,
  isPristine,
  itemCoding,
  itemLabel,
  kindNamed,
  kindsInOrder,
  lifestyleRows,
  needingConfirmation,
  needsConfirmation,
  ofUnknownKind,
  outstanding,
  parsedDuration,
  partialCoding,
  problemsWith,
  refusalOn,
  relationsInOrder,
  removalRefused,
  setCoding,
  setKind,
  setOnset,
  toReactivation,
  toRecording,
  toRemoval,
  toResolution,
  uncodedCount,
  type AmendHistoryItemRequest,
  type CarriedItem,
  type CarryReason,
  type CodingParts,
  type FamilyRelation,
  type FieldAsk,
  type HistoryDraft,
  type HistoryField,
  type HistoryItem,
  type HistoryKind,
  type ItemStatus,
  type KindGroup,
  type KindName,
  type LifestyleRow,
  type Locale,
  type ObservationRow,
  type OnsetPrecision,
  type Problem,
  type RecordHistoryItemRequest,
  type Refusal,
  type Removal,
  type Severity,
} from './form';
