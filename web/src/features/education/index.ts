/**
 * `education` — station 11, and the one number the patient gives about themselves (CP88, CP92).
 *
 * Two surfaces over one set of calls. `EducationStation` is the officer's: the checklists the
 * prescription selected, the compliance question with its preamble, and the improvement score,
 * in that order and in that order for a reason. `ImprovementScoreSelector` is exported on its own
 * because the dashboard draws the same faces and bands beside the measurements, and two
 * renderings of one scale is how a patient comes to point at a different face on two screens.
 *
 * There is deliberately no export that records a score by itself, and no helper that turns the
 * checklist into a percentage. Both would be easy and both are the things the two specs forbid.
 */
export { EducationStation } from './components/EducationStation';
export { ImprovementScoreSelector } from './components/ImprovementScoreSelector';
export { ScoreFace } from './components/ScoreFace';
export { TechniqueChecklist } from './components/TechniqueChecklist';
export { ComplianceQuestion } from './components/ComplianceQuestion';
export { ReeducationNotice } from './components/ReeducationNotice';
export {
  anchorFor,
  educationSessionKey,
  notApplicableReasonOf,
  readEducationReference,
  readEducationSession,
  recordAssessment,
  scaleValues,
  scoreOf,
  tally,
  EDUCATION_REFERENCE_KEY,
  type EducationAssessmentRequest,
  type EducationAssessmentResult,
  type EducationChecklist,
  type EducationChecklistItem,
  type EducationCompetency,
  type EducationReference,
  type EducationSession,
  type EducationState,
  type ImprovementAnswer,
  type ImprovementScale,
  type ImprovementScaleAnchor,
} from './api/education';
