'use client';

import { useTranslations } from 'next-intl';

import { Select } from '@dthcms/ui';

import { ROLES, type Role } from '@/lib/permissions';
import { useSessionStore } from '@/stores/session';

/**
 * A switch between the roles the placeholder session holds.
 *
 * This is scaffolding, and it says so: at CP16 a person has the roles their account was
 * given, and switching between them is either a real feature with an audit trail or it
 * does not exist.
 *
 * It is here because the alternative is worse. The sidebar shows only what the active
 * role can reach — which is the correct behaviour and the thing worth reviewing — so with
 * a single hard-coded role, five of the nine route groups would have no way to be reached
 * at all, and CP10's manual verification step is "navigate all route groups". A reviewer
 * would have to type URLs.
 *
 * It disappears with the placeholder session it belongs to.
 */
export function RoleSwitcher() {
  const t = useTranslations();
  const user = useSessionStore((state) => state.user);
  const activeRole = useSessionStore((state) => state.activeRole);
  const setActiveRole = useSessionStore((state) => state.setActiveRole);

  if (!user || activeRole === null) return null;

  // The order the roles are declared in, filtered to the ones this session holds, so the
  // list is stable rather than following whatever order the account happened to store.
  const held = ROLES.filter((role) => user.roles.includes(role));

  return (
    <Select
      label={t('shell.activeRole')}
      labelHidden
      value={activeRole}
      onChange={(event) => setActiveRole(event.target.value as Role)}
      options={held.map((role) => ({ value: role, label: t(`shell.role.${role}`) }))}
    />
  );
}
