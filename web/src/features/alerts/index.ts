/**
 * `alerts` — critical values and the chain that chases them (CP50).
 *
 * Two surfaces over one set of calls. `AlertBoard` is the consultant's: everything in the
 * facility that nobody has answered, with the acknowledgement that stops the escalation.
 * `AlertBanner` is the patient's: the same alerts for one person, compact enough to sit
 * above whatever the clinician actually opened the record to do.
 *
 * Acknowledging lives on the board alone. One alert, one note, one place — offering the act
 * twice is how the same finding collects two half-answers.
 */
// `OPEN_ALERTS_KEY` is public because any screen that acknowledges or raises something has
// to be able to invalidate the board; the poll interval is the board's own business.
export { AlertBoard, OPEN_ALERTS_KEY } from './components/AlertBoard';
export { AlertBanner } from './components/AlertBanner';
export {
  acknowledgeAlert,
  byUrgency,
  hasEscalated,
  listEscalation,
  listOpenAlerts,
  listPatientAlerts,
  listRules,
  noteAcceptable,
  readAlert,
  stillOpen,
  NOTE_MIN,
  type AcknowledgeResult,
  type CriticalAlert,
  type CriticalValueRule,
  type EscalationStep,
} from './api/alerts';
