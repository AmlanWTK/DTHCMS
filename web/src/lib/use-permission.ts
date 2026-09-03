'use client';

import { can, type PermissionAction } from '@/lib/permissions';
import { activePermissions, useSessionStore } from '@/stores/session';

/**
 * Whether the current operator may do something, wearing the role they chose.
 *
 * Read the note at the top of permissions.ts before relying on this for anything: it
 * decides what to render, and nothing else. The server decides what may happen — for
 * the same role, which the client sends with every request.
 */
export function usePermission(action: PermissionAction): boolean {
  const user = useSessionStore((state) => state.user);
  const activeRole = useSessionStore((state) => state.activeRole);
  return user !== null && can(activePermissions(user, activeRole), action);
}
