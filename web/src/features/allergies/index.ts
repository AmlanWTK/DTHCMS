/**
 * `allergies` — §3 step 4's checkpoint, and the refusal behind it (CP54, [R-01]).
 *
 * **No patient advances past the history station without allergy status.** The gate is a
 * trigger on the queue table, so no client can get past it and nothing in this feature
 * enforces it. What lives here is the other half: letting a station satisfy it in five
 * seconds, and letting every screen say why the next step is not available yet.
 *
 * Two surfaces over one set of calls. `AllergyBanner` is the strip the patient header
 * carries on **every** patient screen — acceptance criterion 3 — and `AllergyPanel` is the
 * station's, where the three answers are given and taken back.
 *
 * **There is no override in this module and there must not be one.** Three acts satisfy the
 * gate: one or more allergies recorded, "no known allergies" asserted, or "unable to
 * assess" with its reason. Nothing exported here clears it without one of those, and no
 * component renders a control that would. The third state exists precisely so there is no
 * fourth: the unconscious patient and the child with no attendant are real, and the usual
 * answer is a button that advances them anyway — but a gate with a way past it is a gate
 * people learn the shape of, and the plan names the risk it produces, which is operators
 * asserting NKA reflexively to clear the gate. Both test files have a named test that fails
 * if such an export or such a button ever appears.
 *
 * **`status` is the answer and the list is not.** `NONE_RECORDED` and `NO_KNOWN_ALLERGY`
 * both carry an empty list and mean opposite things — nobody asked, against somebody asked
 * and there are none. `statusOf` and `isReassuring` are exported so that every screen gets
 * the same answer to "may this be drawn as reassurance", rather than a second
 * implementation somewhere that reads `allergies.length` and rounds the dangerous one up to
 * the safe one.
 *
 * The pure helpers are public for the same reason terminology's and history's are: a screen
 * that needs to know whether an allergy is an emergency, or uncoded, or what its substance
 * is called in Bangla, must get the same answer this panel gives.
 */
export { AllergyBanner, type AllergyBannerProps } from './components/AllergyBanner';
export { AllergyPanel, type AllergyPanelProps } from './components/AllergyPanel';
export { AllergyCard, type AllergyCardProps } from './components/AllergyCard';
export { AllergyChanges, type AllergyChangesProps } from './components/AllergyChanges';
export {
  AssertAllergyStatus,
  type AssertAllergyStatusProps,
} from './components/AssertAllergyStatus';
export { RecordAllergy, type RecordAllergyProps } from './components/RecordAllergy';
export {
  changeSubject,
  reactionLabel,
  reactionName,
  substanceName,
} from './components/allergyText';
export {
  ALLERGY_REACTIONS_KEY,
  ASSERTION_KINDS,
  CERTAINTIES,
  REASON_MIN,
  SEVERITIES,
  allergyChangesKey,
  allergyCoding,
  allergyStateKey,
  allergyAssertionRates,
  assertAllergyStatus,
  assertionRequestFrom,
  emptyAllergyDraft,
  gateSatisfied,
  getAllergyState,
  hasEmergency,
  isEmergency,
  isReassuring,
  isUncoded,
  isWithdrawn,
  listAllergyChanges,
  listAllergyReactions,
  missingAllergyFields,
  missingAssertionFields,
  newEventId,
  reactionsInOrder,
  reasonAcceptable,
  recordAllergy,
  recordRequestFrom,
  statusOf,
  withdrawAllergy,
  withdrawAllergyAssertion,
  type Allergy,
  type AllergyAssertion,
  type AllergyAssertionRate,
  type AllergyCertainty,
  type AllergyChange,
  type AllergyDraft,
  type AllergyReaction,
  type AllergySeverity,
  type AllergyState,
  type AllergyStatus,
  type AssertionKind,
  type AssertionRequest,
  type RecordAllergyRequest,
} from './api/allergies';
