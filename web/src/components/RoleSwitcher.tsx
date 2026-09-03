'use client';

import { useTranslations } from 'next-intl';

import { Select } from '@dthcms/ui';

import { ROLE_CODES, isKnownRole } from '@/lib/permissions';
import { useSessionStore } from '@/stores/session';

/**
 * A switch between the roles the account holds [R-02].
 *
 * The sidebar shows one role's areas at a time, and every request carries the role
 * chosen here as `X-Active-Role` — so the server decides for the same hat the interface
 * is showing. Most staff hold one role, and for them this renders nothing.
 */
export function RoleSwitcher() {
  const t = useTranslations();
  const user = useSessionStore((state) => state.user);
  const activeRole = useSessionStore((state) => state.activeRole);
  const setActiveRole = useSessionStore((state) => state.setActiveRole);

  if (!user || activeRole === null) return null;

  // Catalogue order, filtered to what this account holds, so the list is stable rather
  // than following whatever order the account happened to store.
  const held = ROLE_CODES.filter((code) => user.roles.includes(code));
  if (held.length < 2) return null;

  return (
    <Select
      label={t('shell.activeRole')}
      labelHidden
      value={activeRole}
      onChange={(event) => setActiveRole(event.target.value)}
      options={held.map((code) => ({ value: code, label: roleLabel(t, code) }))}
    />
  );
}

/** A role's label in the interface language; the raw code for one the messages lack. */
export function roleLabel(t: ReturnType<typeof useTranslations>, code: string): string {
  return isKnownRole(code) ? t(`role.${code}`) : code;
}
