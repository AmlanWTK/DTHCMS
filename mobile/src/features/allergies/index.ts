/**
 * The allergy hard stop, at station 4 (CP54, §3 step 4, [R-01]).
 *
 * A station reaches this feature through here and nowhere else. What it gets is the answer to
 * one question — *what is this patient's allergy status* — stated in words, and the three ways
 * an officer can answer it in the seconds the question actually gets.
 *
 * The gate itself is not here and cannot be. **No patient advances past the history station
 * without allergy status**, and that is enforced by a trigger on the queue table, so the
 * support script meets it, the second client meets it, and so does the one written after
 * everybody who read the plan has left. What this feature does is let a station satisfy it
 * quickly, and say plainly why the patient cannot be sent on yet.
 *
 * The public surface is in three parts: `AllergyStep`, the screen; `state.ts`, where every
 * decision lives — what each of the four statuses means, what a draft still needs, and what
 * the server would refuse; and the calls in `api.ts`.
 *
 * Four things in this list are deliberate, and three of them are absences:
 *
 *   - **An empty list is never rendered as "no allergies".** `NONE_RECORDED` and
 *     `NO_KNOWN_ALLERGY` both come back with nothing on them and they are opposite facts.
 *     `readingOf` chooses the sentence for an empty list from the **status**, and there is no
 *     function here that reads a list and decides what it means.
 *   - **There is no override, and nothing here can be made into one.** `mayAdvance` returns
 *     the server's own `satisfied` untouched; nothing in this feature constructs an
 *     `AllergyState`; and `ANSWERS` has exactly three entries — record an allergy, assert no
 *     known allergies, assert unable to assess. A fourth would have to be added here, next to
 *     the sentence saying why there is not one.
 *   - **`UNABLE_TO_ASSESS` is not reassurance.** It satisfies the gate, it requires a reason,
 *     and `reassuring` is true for exactly one status, which is not this one.
 *   - **Recording is as cheap as asserting.** `missingFrom` counts what the honest answer
 *     still costs — four taps — and the screen draws the three answers as one row of equal
 *     controls, because whichever is cheapest to press is what the record fills up with.
 */

export { AllergyStep, type WithdrawTarget } from './AllergyStep';
export {
  assertAllergyStatus,
  getAllergyState,
  listAllergyChanges,
  listReactions,
  recordAllergy,
  troubleOf,
  withdrawAllergy,
  withdrawAllergyAssertion,
  type Trouble,
} from './api';
export {
  ALLERGEN_SYSTEM,
  ALLERGY_FIELDS,
  ALLERGY_STATUSES,
  ANSWERS,
  ASSERTION_KINDS,
  CERTAINTIES,
  PROBLEMS,
  SEVERITIES,
  allergyLabel,
  allergyRows,
  anyEmergency,
  asked,
  assertionProblem,
  canAssert,
  canRecord,
  changeRows,
  coded,
  codingFrom,
  edit,
  emergencyOf,
  emptyDraft,
  isPristine,
  knownReaction,
  mayAdvance,
  missingFrom,
  needsReason,
  partialCoding,
  problemsWith,
  reactionLabel,
  reactionNamed,
  reactionTextOf,
  reactionsInOrder,
  readingOf,
  refusalOn,
  setCoding,
  statusKnown,
  toAssertion,
  toRecording,
  toWithdrawal,
  toneFor,
  wholeCoding,
  withdrawalRefused,
  type Allergy,
  type AllergyAssertion,
  type AllergyAssertionRequest,
  type AllergyChange,
  type AllergyDraft,
  type AllergyField,
  type AllergyReaction,
  type AllergyRecording,
  type AllergyRow,
  type AllergyState,
  type AllergyStatus,
  type AllergyWithdrawal,
  type Answer,
  type AssertionKind,
  type Certainty,
  type ChangeRow,
  type CodingParts,
  type Locale,
  type Problem,
  type ReactionName,
  type Reading,
  type RecordAllergyRequest,
  type Refusal,
  type Severity,
  type Tone,
  type WriteIDs,
} from './state';
