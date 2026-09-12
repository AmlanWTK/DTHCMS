/**
 * `prescriptions` — the renal half of it, so far (CP79).
 *
 * CP80 built the prescription aggregate and its API; **CP81 builds the editor**, and this
 * folder holds the one piece of interface CP79 owns in the meantime: the renal status
 * indicator a prescriber reads before deciding a dose.
 *
 * It is exported rather than inlined into a screen because the screen it belongs on does not
 * exist yet. When CP81's editor lands, this mounts beside the item list — and until then it
 * is a component with its own tests rather than a promise in a document.
 */
export { RenalIndicator, type RenalIndicatorProps } from './components/RenalIndicator';
export {
  getRenalStatus,
  isCurrentRenalFunction,
  renalStatusKey,
  renalTone,
  type RenalPolicy,
  type RenalStatus,
  type RenalTone,
} from './api/renal';
