export { ConsentPanel } from './components/ConsentPanel';
export { SignaturePad } from './components/SignaturePad';
export {
  CONSENT_TYPES,
  NEEDS_EVIDENCE,
  NEEDS_WITNESS,
  consentHistory,
  consentTemplates,
  evidenceUploadURL,
  grantConsent,
  listConsents,
  revokeConsent,
  type CaptureMethod,
  type ConsentHistoryEntry,
  type ConsentTemplate,
  type ConsentType,
  type PatientConsent,
} from './api/consent';
export {
  MIN_POINTS,
  bounds,
  digestOf,
  draw,
  hasMark,
  type Point,
  type Stroke,
} from './lib/signature';
