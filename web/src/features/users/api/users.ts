import { guarded } from '@dthcms/api-client';
import type { components } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';
import { STEP_UP_HEADER } from '@/features/auth';

/**
 * The administration console's calls, typed against the contract (CP21).
 *
 * Every write carries a step-up token minted for its purpose: `user.manage` for the
 * account (invite, status, roles) and `credential.reset` for the credentials (sessions,
 * password, authenticator). The token is obtained by the component through `useStepUp`;
 * these functions only carry it.
 *
 * Which status moves are allowed, and which roles exist, are the server's rules. The
 * console mirrors the lifecycle here so it can show sensible buttons, and lets the server
 * have the last word.
 */

export type AdminUser = components['schemas']['AdminUser'];
export type AdminAccount = components['schemas']['AdminAccount'];
export type RoleDefinition = components['schemas']['RoleDefinition'];
export type GrantHistoryEntry = components['schemas']['GrantHistoryEntry'];
export type UserStatus = AdminUser['status'];
export type TargetStatus = components['schemas']['StatusChangeRequest']['status'];

export interface Invitation {
  employee_code: string;
  name_en: string;
  name_bn: string;
  phone?: string;
  email?: string;
  roles: string[];
  password: string;
}

function stepped(token: string) {
  return { header: { ...guarded.header, [STEP_UP_HEADER]: token } };
}

function steppedAt<P extends Record<string, string>>(token: string, path: P) {
  return { ...stepped(token), path };
}

export async function listUsers(status?: UserStatus): Promise<AdminUser[]> {
  const result = await unwrap(
    api.GET('/v1/admin/users', { params: { query: status ? { status } : {} } }),
  );
  return result.users;
}

export function getUser(id: string): Promise<AdminAccount> {
  return unwrap(api.GET('/v1/admin/users/{id}', { params: { path: { id } } }));
}

export async function listRoles(): Promise<RoleDefinition[]> {
  const result = await unwrap(api.GET('/v1/admin/roles'));
  return result.roles;
}

export function inviteUser(invitation: Invitation, token: string): Promise<AdminAccount> {
  return unwrap(api.POST('/v1/admin/users', { params: stepped(token), body: invitation }));
}

export function changeStatus(
  id: string,
  status: TargetStatus,
  reason: string,
  token: string,
): Promise<AdminAccount> {
  return unwrap(
    api.POST('/v1/admin/users/{id}/status', {
      params: steppedAt(token, { id }),
      body: reason ? { status, reason } : { status },
    }),
  );
}

export function grantRole(id: string, role: string, token: string): Promise<AdminAccount> {
  return unwrap(
    api.POST('/v1/admin/users/{id}/roles', { params: steppedAt(token, { id }), body: { role } }),
  );
}

export function revokeRole(
  id: string,
  role: string,
  reason: string,
  token: string,
): Promise<AdminAccount> {
  return unwrap(
    api.POST('/v1/admin/users/{id}/roles/{role}/revoke', {
      params: steppedAt(token, { id, role }),
      body: { reason },
    }),
  );
}

export async function endSessions(id: string, reason: string, token: string): Promise<number> {
  const result = await unwrap(
    api.POST('/v1/admin/users/{id}/sessions/end', {
      params: steppedAt(token, { id }),
      body: { reason },
    }),
  );
  return result.sessions_ended;
}

export function setPassword(
  id: string,
  password: string,
  reason: string,
  token: string,
): Promise<void> {
  return unwrap(
    api.POST('/v1/admin/users/{id}/password', {
      params: steppedAt(token, { id }),
      body: { password, reason },
    }),
  ).then(() => undefined);
}

export function resetSecondFactor(id: string, reason: string, token: string): Promise<void> {
  return unwrap(
    api.POST('/v1/admin/users/{id}/second-factor/reset', {
      params: steppedAt(token, { id }),
      body: { reason },
    }),
  ).then(() => undefined);
}

/**
 * The lifecycle, mirrored from the server (backend/internal/auth/lifecycle.go):
 *
 *   invited ──▶ active ──▶ suspended ──▶ active
 *      └──────────┴───────────┴──────▶ deactivated ──▶ active
 */
export function transitionsFor(status: UserStatus): TargetStatus[] {
  switch (status) {
    case 'invited':
      return ['active', 'deactivated'];
    case 'active':
      return ['suspended', 'deactivated'];
    case 'suspended':
      return ['active', 'deactivated'];
    case 'deactivated':
      return ['active'];
  }
}

/** Suspension must carry a reason; the server (and its CHECK) insists. */
export function reasonRequiredFor(status: TargetStatus): boolean {
  return status === 'suspended';
}

/** The password policy the server applies: 12 to 128 characters. Checked here for the hint. */
export const PASSWORD_MIN = 12;
export const PASSWORD_MAX = 128;

export function passwordAcceptable(password: string): boolean {
  const length = [...password].length;
  return length >= PASSWORD_MIN && length <= PASSWORD_MAX;
}

/** The employee code shape the server accepts: a capital, then up to fifteen capitals, digits or underscores. */
export const EMPLOYEE_CODE = /^[A-Z][A-Z0-9_]{1,15}$/;

/**
 * The permissions a set of roles confers together, from the catalogue — the preview
 * shown while an administrator picks roles, before anything is granted.
 */
export function permissionsOf(roles: readonly string[], catalogue: RoleDefinition[]): string[] {
  const held = new Set<string>();
  for (const role of catalogue) {
    if (roles.includes(role.code)) role.permissions.forEach((p) => held.add(p));
  }
  return [...held].sort();
}
