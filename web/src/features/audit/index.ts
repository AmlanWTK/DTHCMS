/**
 * `audit` — the security audit trail (CP22).
 *
 * The viewer and the export for administrators, the alarm every administrator's console
 * shows, and the emergency door a clinician opens with a typed justification.
 */
export { AuditViewer } from './components/AuditViewer';
export { AdminAlerts, ALERT_POLL_MS } from './components/AdminAlerts';
export { BreakGlassConsole } from './components/BreakGlassConsole';
export {
  listAuditEvents,
  listAuditKinds,
  verifyChain,
  exportTrail,
  listAlerts,
  acknowledgeAlert,
  openBreakGlass,
  myBreakGlass,
  endBreakGlass,
  justificationAcceptable,
  MIN_JUSTIFICATION,
  type AuditEvent,
  type AuditFilter,
  type AdminAlert,
  type BreakGlassAccess,
  type ChainVerification,
} from './api/audit';
