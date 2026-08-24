/**
 * The feature's public surface.
 *
 * Everything else in this folder is internal, and the ESLint boundary rule in the repo
 * root enforces it: another feature importing `system-status/model/status.js` is an
 * error, not a code review comment. Whatever a caller needs is exported here on purpose.
 */

export { SystemStatusCard } from './components/SystemStatusCard';
export { useSystemStatus, systemStatusKeys } from './api/useSystemStatus';
export type { SystemStatus, ServerState } from './model/status';
