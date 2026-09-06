/**
 * `corrections` — the 140/150 case, on the web (CP62, §4.3, [R-04]).
 *
 * An operator records a height of 150 cm. The physician is sure it is 140 and flags it. The
 * request goes to **the operator who typed it**. They correct it; a new value replaces the old
 * one and the old one keeps its row; everything derived from it recomputes; and both numbers
 * stay in the record with both names against them.
 *
 * Four surfaces, and each exists because one of §4.3's acceptance criteria is otherwise
 * unverifiable from a screen:
 *
 * **`FlagValue`** is where a physician says a value looks wrong. Two deliberate acts, a reason
 * **code** from the server's own vocabulary, and free text beside it — because a code alone
 * cannot say "the tape was against the wall, not the patient" and free text alone cannot be
 * counted, and counting is the whole of CP63's repeated-transcription detection.
 *
 * **`ValueHistory`** and **`ValueChain`** are criterion 5: the complete chain for one code,
 * replaced rows included, each with its own attribution, its status, and — where somebody
 * flagged it — who flagged it, why, who was asked, and what they answered. Criterion 1 says
 * the original is never altered and *remains visible*, and this is the screen where it is
 * visible: every version is drawn the same way, at the same size, through the same attributed
 * component.
 *
 * **`DerivedValues`** is criterion 4 read from the record rather than assumed. A corrected
 * height supersedes the BMI computed from it, and this says whether the BMI standing today was
 * computed from the height standing today — by comparing what the derived row says it *saw*
 * with what the code now says, which is two facts in the record rather than a guess about a
 * transaction. Where either is missing it says the record does not say, in words: rounding
 * "we cannot tell" up to "it is fine" is the one answer a physician must not be given.
 *
 * **`CorrectionQueue`** is the other end of the routing: what I am being asked to fix, with
 * both honest answers. Correcting writes a new value; saying the value stands **requires a
 * reason**, because "no" with no reason is how a flagging culture dies.
 *
 * # Two rules this feature is built to keep
 *
 * **`OVERRIDDEN` is not `APPLIED`.** A supervisor's fix is a different status, drawn with its
 * own sentence, and the person about to press the button is told so beforehand. An operator's
 * quality record must not read a supervisor's fix as though they had put it right themselves.
 *
 * **Nothing here reads as blame.** A correction is a fact about a number, not a verdict on a
 * person. There is no word for wrong about anybody in this feature, no count of somebody's
 * mistakes on the screen they open every morning, and no tone that marks an operator. The
 * plan's own risk note says a metric that feels punitive makes staff hide errors instead of
 * correcting them, and the first place that goes wrong is the vocabulary.
 *
 * # And one thing that is not here
 *
 * There is no function in this feature that edits a value in place, and there must never be
 * one. Correcting is writing a new value that replaces the old one; an edit would destroy the
 * chain the whole checkpoint exists to show, in the one place a reviewer would never look.
 */
export { FlagValue, type FlagValueProps } from './components/FlagValue';
export { CorrectionTrail, type CorrectionTrailProps } from './components/CorrectionTrail';
export { ValueChain, type ValueChainProps } from './components/ValueChain';
export { ValueHistory, type ValueHistoryProps } from './components/ValueHistory';
export { DerivedValues, type DerivedValuesProps } from './components/DerivedValues';
export { AnswerCorrection, type AnswerCorrectionProps } from './components/AnswerCorrection';
export { CorrectionQueue } from './components/CorrectionQueue';
export { PersonLine, type PersonLineProps } from './components/PersonLine';
export {
  ALREADY_ANSWERED_CODE,
  ALREADY_FLAGGED_CODE,
  CORRECTION_REASONS_KEY,
  REJECT_REASON_MIN,
  alreadyAnswered,
  alreadyFlagged,
  answerDefaults,
  answerRole,
  applyCorrection,
  correctingFrom,
  correctionKey,
  correctionReady,
  flagObservation,
  flagReady,
  getCorrectionRequest,
  isOpen,
  isTranscription,
  listCorrectionReasons,
  listCorrectionsForPatient,
  listMyCorrections,
  myCorrectionsKey,
  newEventId,
  notYoursToAnswer,
  openRequestOn,
  patientCorrectionsKey,
  reasonByCode,
  reasonsInOrder,
  rejectCorrection,
  rejectReasonAcceptable,
  requestsOn,
  wasOverridden,
  whyNotFlaggable,
  type AnswerDraft,
  type AnswerRole,
  type Correcting,
  type CorrectionReason,
  type CorrectionRequest,
  type CorrectionStatus,
  type FlagBlocker,
  type Flagging,
} from './api/corrections';
