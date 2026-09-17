/**
 * `signing` — the signature on a prescription, and the page a stranger checks it on (CP84, CP85).
 *
 * Its own feature rather than a file inside `prescriptions`, because the two have different
 * readers: everything in `prescriptions` is written for a physician at a desk, and half of this is
 * written for somebody with no account holding a piece of paper.
 */
export { SignaturePanel, type SignaturePanelProps } from './components/SignaturePanel';
export { SignatureImageNote } from './components/SignatureImageNote';
export { VerificationCode } from './components/VerificationCode';
export { PublicVerificationPanel } from './components/PublicVerificationPanel';
export { useSignCapability, type SignCapability } from './api/capability';
export {
  readSignature,
  signPrescription,
  signatureKey,
  verifyPublicly,
  type PrescriptionSignature,
  type PublicVerification,
  type SignatureImageCaveat,
  type SignatureVerification,
  type SignatureView,
  type SigningReadiness,
  type SigningResult,
} from './api/signing';
