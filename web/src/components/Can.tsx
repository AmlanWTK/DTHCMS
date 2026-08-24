'use client';

import type { ReactNode } from 'react';

import { usePermission } from '@/lib/use-permission';
import type { PermissionAction } from '@/lib/permissions';

export interface CanProps {
  action: PermissionAction;
  children: ReactNode;
  /**
   * Shown instead when the action is not permitted. Usually nothing.
   *
   * Give it a value only where the absence would be confusing — a panel that would
   * otherwise be an unexplained gap. A disabled control is not a good fallback: it
   * invites the operator to keep trying.
   */
  fallback?: ReactNode;
}

/**
 * Renders its children only if the operator may perform the action.
 *
 * This is presentation, not enforcement. See permissions.ts.
 */
export function Can({ action, children, fallback = null }: CanProps) {
  return usePermission(action) ? <>{children}</> : <>{fallback}</>;
}
