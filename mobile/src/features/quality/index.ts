/**
 * An operator's own quality record, on their own device (CP63, §4.3, ADR-0029).
 *
 * The public surface is deliberately narrow, and what it leaves out is the point. There is no
 * exported way to read anybody else's record — no function in this feature takes an operator
 * id — and no exported way to answer a note. Both need permissions a station operator does not
 * hold (`quality.read.team`, `quality.flag.resolve`), and both belong on a desk with the
 * correction chain and the instrument log open rather than on a phone between patients. The
 * supervisor's view is web-only.
 *
 * What a screen can reach for is the operator's own panel, the one line that tells them
 * something has been written on their record or answered on it, the standing way in that
 * exists whether either has happened or not, and the pure pieces the tests need.
 */

export { MyQualityRecord } from './MyQualityRecord';
export { MyRecordLink, QUALITY_HREF, QualityNotice } from './QualityNotice';

export {
  QUALITY_QUERY_PREFIX,
  QUALITY_THRESHOLDS_QUERY_KEY,
  THRESHOLDS_STALE_MS,
  getMyQualityRecord,
  listQualityThresholds,
  myQualityQueryKey,
  troubleOf,
  useMyQualityRecord,
  useQualityThresholds,
} from './api';

export {
  ADVICE,
  BASES,
  CLINIC_UTC_OFFSET_MINUTES,
  MAX_WINDOW_DAYS,
  MIN_WINDOW_DAYS,
  NOTE_STATES,
  NOTICES,
  OUTCOMES,
  PROBLEMS,
  QUALITY_FLAG_RAISED,
  QUALITY_FLAG_RESOLVED,
  QUALITY_KINDS,
  RECENT_DECISION_MS,
  SECTIONS,
  WINDOW_DAYS,
  adviceFor,
  answeredOf,
  byCodeOf,
  byHourOf,
  byReasonOf,
  clinicDay,
  clockHour,
  counted,
  flagReadingOf,
  hasOpenNote,
  myTopic,
  noteStateOf,
  notesOf,
  noticeOf,
  questionsOf,
  rateOf,
  retentionOf,
  thresholdReadingsOf,
  windowDaysFor,
  windowOf,
  workDone,
  type Advice,
  type Answered,
  type Basis,
  type Counted,
  type EvidenceReading,
  type FlagReading,
  type Locale,
  type Notice,
  type NoteState,
  type Outcome,
  type PatternRow,
  type Problem,
  type QualityCodeCount,
  type QualityEvidence,
  type QualityFlag,
  type QualityHourCount,
  type QualityReasonCount,
  type QualityRecord,
  type QualityRule,
  type QualityThreshold,
  type QualityWindow,
  type Rate,
  type Section,
  type ThresholdReading,
  type Trouble,
  type WindowReading,
} from './state';
