/**
 * `history` — what the patient brought with them, and the question asked of it (CP53).
 *
 * Station 4's surface. `MedicalHistory` is the panel: six kinds in the order the station
 * asks, each item showing its coding, the patient's own words, the detail the kind carries,
 * who recorded it, and whether anybody has said it is still true. `AddHistoryItem` is the
 * form, and every field on it is drawn from the kind's rules rather than from its name —
 * that is what `/v1/history/kinds` returns them for.
 *
 * **Confirming is exported one item at a time and there is nothing here that takes a list.**
 * That is acceptance criterion 3 in the shape of a module boundary: a helper that confirmed
 * a whole history would let one click put twenty assertions into the record with a person's
 * name on them and no person behind them, which is the auto-acceptance the criterion
 * forbids. The contract offers no batch endpoint; this feature does not build one.
 *
 * **Resolving and removing are two exports because they are two acts.** `amendHistoryItem`
 * with `status: 'RESOLVED'` says the patient had this and no longer does; `removeHistoryItem`
 * says it should never have been recorded, and takes the reason the server requires. A
 * caller that could reach only one of them would be forced to lie with it.
 *
 * The pure helpers are public for the same reason terminology's are: a screen that needs to
 * know whether an item is confirmed, or uncoded, or which kind it belongs under, must get
 * the same answer this panel gives rather than a second implementation that decides
 * differently about a half-filled coding.
 */
export { MedicalHistory, type MedicalHistoryProps } from './components/MedicalHistory';
export { AddHistoryItem, type AddHistoryItemProps } from './components/AddHistoryItem';
export { HistoryItemCard, type HistoryItemCardProps } from './components/HistoryItemCard';
export { itemName, kindLabel, onsetText, relationLabel } from './components/historyText';
export {
  HISTORY_KINDS_KEY,
  ONSET_PRECISIONS,
  REASON_MIN,
  SEVERITIES,
  amendHistoryItem,
  confirmHistoryItem,
  countUncoded,
  emptyDraft,
  groupByKind,
  historyItemsKey,
  isConfirmed,
  isUncoded,
  itemCoding,
  kindNamed,
  listHistoryKinds,
  listMedicalHistory,
  missingFields,
  needsConfirmation,
  newEventId,
  readHistoryItem,
  reasonAcceptable,
  recordHistoryItem,
  recordRequestFrom,
  removeHistoryItem,
  unconfirmedItems,
  type AmendHistoryItemRequest,
  type FamilyRelation,
  type HistoryDraft,
  type HistoryGroup,
  type HistoryItem,
  type HistoryKind,
  type HistoryReference,
  type KindName,
  type OnsetPrecision,
  type RecordHistoryItemRequest,
  type Severity,
} from './api/history';
