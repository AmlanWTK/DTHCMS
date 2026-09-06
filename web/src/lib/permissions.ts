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
  'counseling.templates.view',
  'counseling.templates.write',
  'counseling.templates.publish',
  'counseling.sessions.view',
  'counseling.gate.override',
  'counseling.overrides.review',
  'observations.view',
  'corrections.request',
  'corrections.queue',
  'corrections.approve',
  'quality.mine',
  'quality.team',
  'quality.flags.resolve',
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
  'admin.jobs.view',
  'admin.jobs.manage',
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
  // Counselling checklists (CP55). Three actions rather than one, because the server grants
  // three and the difference between them is the checkpoint. Reading reaches nearly every
  // clinical role — a counsellor about to work through a checklist, and a nutritionist about
  // to be handed the patient, both need to see what it asks. Writing is the physician's.
  // **Publishing is separate again**: it puts a checklist on every phone on the floor within
  // seconds, freezes that version forever and retires the current one, and it carries its own
  // step-up purpose. An interface that folded publishing into writing would offer the button
  // to somebody the server would refuse, which is the one thing this file exists to avoid.
  'counseling.templates.view': ['counseling.template.read'],
  'counseling.templates.write': ['counseling.template.write'],
  'counseling.templates.publish': ['counseling.template.publish'],
  // The counselling checkpoint (CP57). Three more actions, and the split is the checkpoint
  // rather than tidiness.
  //
  // Reading a session is `counseling.session.read`, which the server grants to nine roles —
  // the counsellor and the nutritionist who walk the checklist, and the physician, the
  // junior doctor, the clinical assistant and QA who read what was covered. Deliberately not
  // `counseling.tick`: a physician's panel built on the tick permission would be a screen
  // carrying the right to write on somebody else's checklist, which is the failure §5.4's
  // spot-questioning exists to catch.
  //
  // **Overriding is its own permission and reaches two roles.** It sends a patient past a
  // checkpoint with the counselling unfinished, it is marked sensitive in the server's
  // catalogue, and it is the one act on this surface with a person's name on it. Folding it
  // into the read action would put the button in front of every counsellor on the floor.
  //
  // The override *rate* is quality's, not the physician's: the answer to a rising rate is a
  // person asking why, and that person is not the one granting them. It is named as its own
  // action rather than reusing `qa.view` so that moving it later is one row here.
  'counseling.sessions.view': ['counseling.session.read'],
  'counseling.gate.override': ['counseling.gate.override'],
  'counseling.overrides.review': ['qa.review'],
  // Recorded values and the chain a corrected one leaves behind (CP62). Its own action rather
  // than `clinical.view`, which asks for `patient.read.demographics`: the registration desk
  // holds that and does not hold `observation.read.values`, so an entry offered on it would put
  // a screen of clinical measurements in front of the one role §4.4 blinds to them — and every
  // request the screen made would be refused.
  'observations.view': ['observation.read.values'],
  // Saying a value looks wrong (CP62, §4.3). Deliberately **not** satisfied by
  // `observation.correct.approve`: the server's flag route admits `observation.correct.request`
  // and nothing else, so an action that accepted the approver's permission would draw a control
  // for exactly the people the server refuses — the failure this whole file exists to avoid.
  //
  // Worth knowing when reading this: the seeded catalogue grants the request permission to
  // registration, anthropometry, the counsellor, the history officer, the clinical assistant
  // and the junior doctor — and **not** to `PHYSICIAN`, though §4.3's canonical scenario is a
  // physician flagging a height. That is a grant on the server, not a mapping here, and adding
  // the approver's permission to this row to paper over it would produce a control that answers
  // 403.
  'corrections.request': ['observation.correct.request'],
  // The operator's own queue. `observation.read.values` is what the endpoint asks for, and it
  // is the right question: everybody who can record a value can be asked about one, and a queue
  // gated on the approver's permission would hide the request from the person it was routed to.
  'corrections.queue': ['observation.read.values'],
  // Answering a request that was routed to somebody else. Its own action because the *result*
  // is different: the server records it as `OVERRIDDEN` rather than `APPLIED`, so that an
  // operator's quality record does not read a supervisor's fix as though they had put it right
  // themselves. The person it was routed to needs no permission at all for their own work —
  // asking somebody to hold a permission to fix their own mistake is how mistakes stay — so
  // there is deliberately no action here for that.
  'corrections.approve': ['observation.correct.approve'],
  // The operator quality record (CP63, ADR-0029). Three actions, and the first of them is the
  // checkpoint's central decision rather than an oversight.
  //
  // **Your own record is everyone's.** `GET /v1/quality/me` requires a session and nothing
  // else: it reads the caller's own id from the session, so there is no version of it that
  // returns somebody else's work. Gating the entry would repeat the mistake CP62 made and had
  // to undo — the community field worker records values all day, receives corrections on them,
  // and holds no read permission at all, so any operator-shaped permission put here would hide
  // their own record from exactly the person it is for. And an operator who has to be granted
  // something before they may see their own correction count is an operator who will assume
  // the count is being kept from them, which is the failure the whole checkpoint is arranged
  // to avoid.
  //
  // **Somebody else's is `quality.read.team`**, and emphatically not `hr.performance.read`,
  // which already exists in the catalogue and which HR holds. The plan puts performance-linked
  // pay and discipline out of scope; a permission that hands an operator's correction history
  // to the department that sets pay puts it back in, whatever anybody intends by it. Mapping
  // this action onto the HR permission would do exactly that from the client side, which is
  // why the mapping is written down here rather than left to whoever adds the next screen.
  //
  // **Answering a flag is separate again.** It is the only act on the surface that changes
  // anything, both its answers are recorded with a name against them, and whether QA holds it
  // as well as the read is still an open question for Dr. Nahid — one row on the server, and
  // nothing here has to change either way.
  'quality.mine': 'anyone',
  'quality.team': ['quality.read.team'],
  'quality.flags.resolve': ['quality.flag.resolve'],
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
  // The background queue (CP69, ADR-0031). Two actions, and the split is the checkpoint
  // rather than tidiness: `ops.jobs.read` is a graph with no patient in it by construction
  // (invariant 90), and the server grants it to the physician, QA and the administrator so
  // that whoever is on the floor when the queue goes red can see that it has. `ops.jobs.manage`
  // is touching it, and the administrator holds it alone.
  //
  // Folding the two together is the tempting simplification and it is the one thing this file
  // exists to prevent. Retrying a dead-lettered job runs code against a patient's record, and
  // pausing a kind stops the synthesis §7.1 promises will be ready before the consultation —
  // so the floor supervisor who needs to know whether the queue is healthy would have been
  // handed the ability to turn it off, and a physician would be shown two buttons the server
  // answers 403 to.
  'admin.jobs.view': ['ops.jobs.read'],
  'admin.jobs.manage': ['ops.jobs.manage'],
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
