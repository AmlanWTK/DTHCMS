/**
 * Permissions — the server's, read here to decide what to show (CP20).
 *
 * Two rules:
 *
 *   1. The interface hides what the operator cannot do. Showing a control that returns
 *      "denied" teaches people that the software is unreliable, and in a clinic that
 *      costs attention nobody has spare.
 *   2. The server denies independently. Nothing here is a security boundary. Every
 *      check in this file is a courtesy to the person using the application; the route
 *      guard, the service and the serialiser refuse on their own (docs/access-model.md).
 *
 * What changed at CP20: the interface no longer invents a grant table. `/v1/auth/me`
 * reports the person's roles and, per role, the permissions the server's catalogue
 * confers. An interface *action* — "may I show the pharmacy area" — is answered by asking
 * whether any of the server permissions behind it is held by the role being worn.
 */

/** The server's role codes (`core.role.code`). The switcher lists the ones a person holds. */
export const ROLE_CODES = [
  'REGISTRATION',
  'ANTHROPOMETRY',
  'COUNSELOR',
  'HISTORY',
  'CLINICAL_ASSISTANT',
  'JUNIOR_DOCTOR',
  'RECORDS',
  'NUTRITIONIST',
  'EXERCISE',
  'PHYSICIAN',
  'QA',
  'RX_EDUCATOR',
  'PHARMACIST',
  'CRM',
  'RESEARCHER',
  'HR',
  'ADMIN',
  'FIELD_WORKER',
] as const;

export type RoleCode = (typeof ROLE_CODES)[number];

export function isKnownRole(code: string): code is RoleCode {
  return (ROLE_CODES as readonly string[]).includes(code);
}

/** What the interface asks about. Each maps to the server permissions that answer it. */
export const ACTIONS = [
  'clinical.view',
  'clinical.register',
  'clinical.prescribe',
  'alerts.view',
  'history.view',
  'history.write',
  'history.confirm',
  'allergies.view',
  'allergies.write',
  'stations.view',
  'board.view',
  'qa.view',
  'pharmacy.view',
  'crm.view',
  'research.view',
  'admin.view',
  'admin.users.manage',
  'admin.credentials.reset',
  'admin.devices.manage',
  'admin.audit.view',
  'clinical.break_glass',
  'exec.view',
  'account.view',
] as const;

export type PermissionAction = (typeof ACTIONS)[number];

/**
 * The server permissions behind each interface action: any one of them held means the
 * action is offered. `anyone` is an area every signed-in person has — their own account.
 */
const REQUIRES: Record<PermissionAction, readonly string[] | 'anyone'> = {
  'clinical.view': ['patient.read.demographics'],
  'clinical.register': ['patient.write.demographics'],
  'clinical.prescribe': ['prescription.draft', 'prescription.sign'],
  // The critical-value board (CP50). `alert.read` and not a general clinical permission:
  // the server grants it to the two roles that can act on a panic value at the moment it
  // is made, and an action asking for anything broader would put the board in the sidebar
  // of people who cannot acknowledge a single row on it.
  'alerts.view': ['alert.read'],
  // Medical history (CP53). Three actions rather than one, because the server grants three
  // and the difference between them is clinical rather than administrative. Reading a
  // history is reading clinical detail, and §4.4 blinds registration and the pharmacist to
  // it. Writing is station 4's job. **Confirming is neither**: it is answering "is this
  // still true", which at a follow-up is often done by the clinical assistant the patient
  // reaches without ever seeing the history officer — so somebody may confirm an item they
  // may not amend, and an interface that folded the two together would hide the button that
  // keeps a carried-forward list honest.
  'history.view': ['history.read'],
  'history.write': ['history.write'],
  'history.confirm': ['history.confirm'],
  // Allergies (CP54). Reading them is deliberately **not** blinded and is therefore its own
  // action rather than a fold into `history.view`: the server grants
  // `patient.read.allergies` to the pharmacist and the prescription educator, roles §4.4
  // blinds to diagnoses, because an allergy has to reach the person handing over the
  // medicine. Blinding them would mean the last person who could catch the mistake is the
  // one who cannot see the warning. Writing is separate again — the pharmacist reads the
  // warning and does not author it.
  'allergies.view': ['patient.read.allergies'],
  'allergies.write': ['allergy.write'],
  'stations.view': [
    'patient.write.demographics',
    'observation.write.anthro',
    'observation.write.vitals',
    'observation.write.lifestyle',
    'observation.write.history',
    'observation.write.nutrition',
    'observation.write.exercise',
    'counseling.tick',
    'records.upload',
    'education.record',
  ],
  // The traffic board (CP40). Its own server permission rather than a station one: the
  // wall display's account holds exactly `board.read`, and an interface action that asked
  // for a station permission would hide the board from the screen it was built for.
  'board.view': ['board.read'],
  'qa.view': ['qa.review'],
  'pharmacy.view': ['prescription.dispense', 'formulary.read'],
  'crm.view': ['crm.read'],
  'research.view': ['research.query'],
  'admin.view': [
    'user.read',
    'role.grant',
    'device.enroll',
    'device.revoke',
    'audit.read',
    'station.configure',
    'facility.configure',
  ],
  'admin.users.manage': ['user.invite', 'role.grant', 'user.suspend', 'user.deactivate'],
  'admin.credentials.reset': ['user.credential.reset'],
  'admin.devices.manage': ['device.enroll', 'device.revoke'],
  'admin.audit.view': ['audit.read'],
  'clinical.break_glass': ['patient.read.clinical', 'patient.read.demographics'],
  'exec.view': ['report.read.operational', 'report.read.financial'],
  'account.view': 'anyone',
};

/** Whether a set of held permissions offers an action. Pure, so it is testable without React. */
export function can(
  held: ReadonlySet<string> | readonly string[],
  action: PermissionAction,
): boolean {
  const needs = REQUIRES[action];
  if (needs === 'anyone') return true;
  const set = held instanceof Set ? held : new Set(held);
  return needs.some((permission) => set.has(permission));
}

/** The server permissions an action depends on, for the tests that check the mapping. */
export function requirementsOf(action: PermissionAction): readonly string[] {
  const needs = REQUIRES[action];
  return needs === 'anyone' ? [] : needs;
}
