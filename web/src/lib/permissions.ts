/**
 * Permissions — a stub with a real shape.
 *
 * Two rules survive past CP16, when this is replaced by something backed by the server:
 *
 *   1. The interface hides what the operator cannot do. Showing a control that returns
 *      "denied" teaches people that the software is unreliable, and in a clinic that
 *      costs attention nobody has spare.
 *   2. The server denies independently. Nothing here is a security boundary. Every
 *      check in this file is a courtesy to the person using the application, and an
 *      attacker skips all of them by calling the API directly.
 *
 * The grants table below is a placeholder. Roles and actions are real — they come from
 * the blueprint's audience list — but the mapping is invented and will be replaced by
 * CP20's admin console.
 */

export const ROLES = [
  'physician',
  'operator',
  'qa',
  'pharmacy',
  'crm',
  'researcher',
  'admin',
  'executive',
] as const;

export type Role = (typeof ROLES)[number];

export const ACTIONS = [
  'clinical.view',
  'clinical.prescribe',
  'stations.view',
  'qa.view',
  'pharmacy.view',
  'crm.view',
  'research.view',
  'admin.view',
  'admin.users.manage',
  'exec.view',
] as const;

export type PermissionAction = (typeof ACTIONS)[number];

/** Placeholder. Replaced at CP20 by role definitions the administrator can edit. */
const GRANTS: Record<Role, readonly PermissionAction[]> = {
  physician: ['clinical.view', 'clinical.prescribe', 'stations.view', 'qa.view', 'exec.view'],
  operator: ['stations.view'],
  qa: ['qa.view', 'clinical.view'],
  pharmacy: ['pharmacy.view'],
  crm: ['crm.view'],
  researcher: ['research.view'],
  admin: ['admin.view', 'admin.users.manage'],
  executive: ['exec.view'],
};

/** Whether a role may perform an action. Pure, so it can be tested without React. */
export function roleCan(role: Role, action: PermissionAction): boolean {
  return GRANTS[role].includes(action);
}
