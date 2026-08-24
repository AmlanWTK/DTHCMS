'use client';

import { useSessionStore } from '@/stores/session';
import { roleCan, type PermissionAction } from '@/lib/permissions';

/**
 * Whether the current operator may do something.
 *
 * Read the note at the top of permissions.ts before relying on this for anything: it
 * decides what to render, and nothing else. The server decides what may happen.
 */
export function usePermission(action: PermissionAction): boolean {
  const activeRole = useSessionStore((state) => state.activeRole);
  return activeRole !== null && roleCan(activeRole, action);
}
