'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { useTranslations } from 'next-intl';

import { Icon } from '@dthcms/ui';

import { ROUTE_GROUPS, type NavItem, type RouteGroup } from '@/lib/navigation';
import { roleCan, type Role } from '@/lib/permissions';
import { useSessionStore } from '@/stores/session';
import { useUiStore } from '@/stores/ui';

/**
 * The sidebar, rendered from ROUTE_GROUPS.
 *
 * Nothing here is hand-listed. The same array the route test reads and the smoke test
 * walks is the array this renders, so a group that exists on disk but not in navigation —
 * or the reverse — is a test failure rather than a screen nobody can reach.
 *
 * Permission is read once, as a role, and applied with the pure `roleCan`. The
 * `usePermission` hook is for a single known action at a call site; using it inside a
 * filter would mean calling a hook a variable number of times per render.
 */
export function Sidebar() {
  const t = useTranslations();
  const open = useUiStore((state) => state.sidebarOpen);
  const setOpen = useUiStore((state) => state.setSidebarOpen);
  const activeRole = useSessionStore((state) => state.activeRole);

  return (
    <>
      {open && (
        <button
          type="button"
          className="app-sidebar__scrim"
          aria-label={t('nav.close')}
          onClick={() => setOpen(false)}
        />
      )}

      <nav className="app-sidebar" aria-label={t('nav.primary')} data-open={open}>
        {ROUTE_GROUPS.map((group) => (
          <SidebarGroup
            key={group.key}
            group={group}
            activeRole={activeRole}
            onNavigate={() => setOpen(false)}
          />
        ))}
      </nav>
    </>
  );
}

function SidebarGroup({
  group,
  activeRole,
  onNavigate,
}: {
  group: RouteGroup;
  activeRole: Role | null;
  onNavigate: () => void;
}) {
  const t = useTranslations();
  const pathname = usePathname();

  const visible: NavItem[] =
    activeRole === null ? [] : group.items.filter((item) => roleCan(activeRole, item.permission));

  // A heading over nothing is worse than no heading: it tells the operator a section
  // exists and then does not say where it went.
  if (visible.length === 0) return null;

  return (
    <div className="app-nav-group">
      <p className="app-nav-group__label">{t(group.labelKey)}</p>
      <ul className="app-nav-group__list">
        {visible.map((item) => {
          const current = pathname === item.href || pathname.startsWith(`${item.href}/`);
          return (
            <li key={item.href}>
              <Link
                href={item.href}
                className="app-nav-link"
                aria-current={current ? 'page' : undefined}
                onClick={onNavigate}
              >
                <Icon name={item.icon} size={20} aria-hidden />
                <span>{t(item.labelKey)}</span>
              </Link>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
