/**
 * `quality` — the operator correction record, on the web (CP63, §4.3, ADR-0029).
 *
 * CP62 routed a correction to the person who typed the value. This is what makes that worth
 * doing: §4.3's stated purpose is that *"recurring patterns per operator surface so targeted
 * retraining happens and the same mistake does not repeat."* Without these screens the
 * corrections are a pile of rows nobody reads.
 *
 * The plan also states the risk, in its own words, and it is a warning about a feature exactly
 * like this one: **"a metric that feels punitive damages data honesty — staff hide errors
 * instead of correcting them."** That sentence is not a caveat. It is the design constraint,
 * and it decided every surface below.
 *
 * # Two screens, and the smaller one is the important one
 *
 * **`MyQualityRecord`** is what an operator sees about themselves, behind no permission at
 * all. It is the screen that decides whether this feature produces honest data or careful
 * data: what somebody concludes from it in the first ten seconds is whether the clinic is
 * helping them or keeping a file on them. It reads as *here is your month* — the work, what
 * came back, what happened to it, where it clustered, and the rules that would raise a flag,
 * on the same page rather than on a help page nobody opens.
 *
 * **`SupervisorQuality`** is the other end: the raised patterns first, each with the suggested
 * first question, and the operator list second. That order is the argument — two of the three
 * seeded actions point away from the operator (*check the instrument*, *this is usually a rota
 * problem*), and a supervisor who reads them before the list arrives at it holding the right
 * frame.
 *
 * # Four rules, each enforced by construction rather than by care
 *
 * **A count cannot be drawn without its denominator.** `CorrectionRate` takes both as required
 * props and there is no other way to render either; the message that draws them interpolates
 * both or neither. Three corrections against four hundred entries and against forty are
 * different facts, and only one of them is a question.
 *
 * **A missing rate is words.** The server answers `rate: null` below its own floor of entries.
 * Nothing here substitutes a zero or a dash — zero is a claim about somebody's month and a
 * dash is a claim the answer went missing, and neither is true.
 *
 * **`rejected` sits beside `upheld` and is never folded into a total.** An operator who
 * defended a correct reading is doing the job, and a metric that punished it would teach
 * everybody to accept every flag without looking. There is no export here that adds the two
 * together, and a named test fails if one appears.
 *
 * **An unapproved threshold says so on the face of every flag.** Every threshold ships with
 * `approved_at` null; a flag drawn without that notice would be a statement of clinic policy
 * that no clinician has made, about a colleague, made by software.
 *
 * # The vocabulary discipline, extended from `corrections`
 *
 * No identifier, label or comment in this feature grades a person. No errors, no mistakes, no
 * score, no accuracy, no worst, no offender. A correction is a fact about a number and a flag
 * is a suggestion that two people have a conversation. `quality.test.tsx` walks this barrel
 * and fails on such a name, the way `corrections.test.tsx` already does for its own — because
 * the first place a feature like this goes wrong is the vocabulary, long before the
 * arithmetic does.
 *
 * # And two things that are deliberately absent
 *
 * There is no export here that ranks people, and no component that draws a position, a
 * comparison with a clinic average, or a trend arrow. A ranking can be added by accident in
 * one line of JSX, and it is the thing this feature must never become.
 *
 * There is also nothing that deletes a flag. Both answers — acknowledged, dismissed — are
 * recorded in place with the name of whoever gave them: a flag that could be made to
 * disappear is a flag a supervisor can be persuaded to make disappear.
 */
export { MyQualityRecord } from './components/MyQualityRecord';
export { SupervisorQuality } from './components/SupervisorQuality';
export { QualityRecordView, type QualityRecordViewProps } from './components/QualityRecordView';
export { QualityFlagCard, type QualityFlagCardProps } from './components/QualityFlagCard';
export { AnswerQualityFlag, type AnswerQualityFlagProps } from './components/AnswerQualityFlag';
export {
  OperatorQualityList,
  type OperatorQualityListProps,
} from './components/OperatorQualityList';
export { ThresholdList } from './components/ThresholdList';
export { WindowPicker, type WindowPickerProps } from './components/WindowPicker';
export {
  CorrectionRate,
  type CorrectionRateProps,
  type RateReading,
} from './components/CorrectionRate';
export { hourLabel, labelOr, qualityDate, qualityMoment } from './components/qualityText';
export {
  ALREADY_ANSWERED_CODE,
  DEFAULT_WINDOW_DAYS,
  MAX_WINDOW_DAYS,
  MIN_WINDOW_DAYS,
  QUALITY_KEY,
  QUALITY_THRESHOLDS_KEY,
  WINDOW_CHOICES,
  allThresholdsAreProposals,
  alreadyAnswered,
  answerFlag,
  entriesUntilARate,
  flagAnswerReady,
  flagsKey,
  getMyRecord,
  getOperatorRecord,
  isOpen,
  listFlags,
  listOperators,
  listThresholds,
  myRecordKey,
  operatorRecordKey,
  operatorsInOrder,
  operatorsKey,
  rateIsAvailable,
  thresholdsInOrder,
  windowIsAcceptable,
  type FlagAnswer,
  type OperatorOrder,
  type QualityCodeCount,
  type QualityEvidence,
  type QualityFlag,
  type QualityFlagStatus,
  type QualityHourCount,
  type QualityOperator,
  type QualityReasonCount,
  type QualityRecord,
  type QualityThreshold,
  type QualityWindow,
  type ThresholdWithDescription,
} from './api/quality';
