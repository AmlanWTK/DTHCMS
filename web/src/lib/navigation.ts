import type { IconName } from '@dthcms/ui';

import type { PermissionAction } from '@/lib/permissions';

/**
 * Every route group in the application, in one place.
 *
 * This is the single source, and it is load-bearing rather than convenient. The sidebar
 * renders from it, `routes.test.ts` asserts that each entry has a real page file on disk,
 * and the Playwright smoke test walks it. Acceptance criterion 1 — "all route groups
 * render with correct layout" — is therefore checked against the same list the
 * application navigates by, instead of a list somebody remembered to update.
 *
 * `directory` is the App Router group folder. It is here so the route test can look for
 * the layout that gives the group its shell, not only for the page.
 */

export interface NavItem {
  /** The path as the browser sees it. No locale segment — see next.config.ts. */
  href: string;
  /** Key under `nav.` in the message files. */
  labelKey: string;
  icon: IconName;
  permission: PermissionAction;
}

export interface RouteGroup {
  key: string;
  /** App Router route group directory, e.g. "(clinical)". */
  directory: string;
  labelKey: string;
  items: NavItem[];
}

export const ROUTE_GROUPS: readonly RouteGroup[] = [
  {
    key: 'clinical',
    directory: '(clinical)',
    labelKey: 'nav.groups.clinical',
    items: [
      {
        href: '/dashboard',
        labelKey: 'nav.dashboard',
        icon: 'house',
        permission: 'clinical.view',
      },
      {
        href: '/patients',
        labelKey: 'nav.patients',
        icon: 'user',
        permission: 'clinical.view',
      },
      {
        // Its own entry rather than a button inside the patients screen: the registration
        // desk does one thing all day, and it should be one click from anywhere (CP32).
        href: '/patients/new',
        labelKey: 'nav.register',
        icon: 'user',
        permission: 'clinical.register',
      },
      {
        href: '/break-glass',
        labelKey: 'nav.breakGlass',
        icon: 'siren',
        permission: 'clinical.break_glass',
      },
    ],
  },
  {
    key: 'stations',
    directory: '(stations)',
    labelKey: 'nav.groups.stations',
    items: [
      {
        href: '/stations',
        labelKey: 'nav.stations',
        icon: 'clipboard-list',
        permission: 'stations.view',
      },
    ],
  },
  {
    key: 'qa',
    directory: '(qa)',
    labelKey: 'nav.groups.qa',
    items: [{ href: '/qa', labelKey: 'nav.qa', icon: 'shield-check', permission: 'qa.view' }],
  },
  {
    key: 'pharmacy',
    directory: '(pharmacy)',
    labelKey: 'nav.groups.pharmacy',
    items: [
      { href: '/pharmacy', labelKey: 'nav.pharmacy', icon: 'pill', permission: 'pharmacy.view' },
    ],
  },
  {
    key: 'crm',
    directory: '(crm)',
    labelKey: 'nav.groups.crm',
    items: [
      { href: '/follow-up', labelKey: 'nav.followUp', icon: 'phone', permission: 'crm.view' },
    ],
  },
  {
    key: 'research',
    directory: '(research)',
    labelKey: 'nav.groups.research',
    items: [
      {
        href: '/research',
        labelKey: 'nav.research',
        icon: 'flask-conical',
        permission: 'research.view',
      },
    ],
  },
  {
    key: 'admin',
    directory: '(admin)',
    labelKey: 'nav.groups.admin',
    items: [
      {
        href: '/admin',
        labelKey: 'nav.admin',
        icon: 'sliders-horizontal',
        permission: 'admin.view',
      },
      {
        href: '/admin/users',
        labelKey: 'nav.users',
        icon: 'users',
        permission: 'admin.users.manage',
      },
      {
        href: '/admin/devices',
        labelKey: 'nav.devices',
        icon: 'tablet',
        permission: 'admin.devices.manage',
      },
      {
        href: '/admin/audit',
        labelKey: 'nav.audit',
        icon: 'scroll-text',
        permission: 'admin.audit.view',
      },
    ],
  },
  {
    key: 'exec',
    directory: '(exec)',
    labelKey: 'nav.groups.exec',
    items: [
      { href: '/overview', labelKey: 'nav.overview', icon: 'trending-up', permission: 'exec.view' },
    ],
  },
  {
    // The person's own account. Every role can reach it: everyone has a password, and
    // from CP17 everyone may have an authenticator.
    key: 'account',
    directory: '(account)',
    labelKey: 'nav.groups.account',
    items: [
      {
        href: '/account/security',
        labelKey: 'nav.security',
        icon: 'shield-check',
        permission: 'account.view',
      },
    ],
  },
];

/**
 * Route groups that are not in the sidebar, and why.
 *
 * `(auth)` has no shell — a person who is not signed in has no navigation to offer. The
 * verification page is public: a patient scanning the QR code on a printed prescription
 * has no account at all, which is the point of it.
 */
export const UNSHELLED_ROUTES = [
  { key: 'auth', directory: '(auth)', href: '/login' },
  { key: 'verify', directory: 'verify', href: '/verify/specimen-token' },
] as const;

/** Every navigable href, for the route test and the smoke test. */
export const ALL_NAV_HREFS: readonly string[] = ROUTE_GROUPS.flatMap((group) =>
  group.items.map((item) => item.href),
);

/** Finds the group owning a path, for breadcrumbs and for highlighting the sidebar. */
export function groupForPath(pathname: string): RouteGroup | undefined {
  return ROUTE_GROUPS.find((group) =>
    group.items.some((item) => pathname === item.href || pathname.startsWith(`${item.href}/`)),
  );
}

/**
 * Finds the nav item owning a path — the most specific one. `/admin/devices` belongs to
 * the devices item, not to `/admin`, even though both are prefixes of it.
 */
export function itemForPath(pathname: string): NavItem | undefined {
  return ROUTE_GROUPS.flatMap((group) => group.items)
    .filter((item) => pathname === item.href || pathname.startsWith(`${item.href}/`))
    .sort((a, b) => b.href.length - a.href.length)[0];
}
