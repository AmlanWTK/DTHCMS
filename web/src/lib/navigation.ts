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
        // Second, and not buried in the dashboard (CP50). This is the screen the escalation
        // chain tells a consultant to open by name; a surface somebody has to find inside
        // another surface is a surface found a minute late.
        href: '/alerts',
        labelKey: 'nav.alerts',
        icon: 'octagon-alert',
        permission: 'alerts.view',
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
        href: '/board',
        labelKey: 'nav.board',
        icon: 'trending-up',
        permission: 'board.view',
      },
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
 * One patient's screens, which are not sidebar entries and cannot be.
 *
 * Every path here needs a patient id, so none of them can appear in `ROUTE_GROUPS` — a
 * sidebar link to `/patients/{id}/consent` has no id to put in it. They were nonetheless
 * accumulating one screen at a time with nothing listing them, which is how CP53's history
 * screen came to be the fifth route reachable only by typing a URL.
 *
 * So this is the list, and it is checked the same way `ROUTE_GROUPS` is: `routes.test.ts`
 * asserts every segment here has a page file behind it, and the i18n test asserts every
 * label exists in both languages. The label keys are the ones each screen's own header
 * already uses, deliberately — a second name for one screen is how a breadcrumb and a tab
 * come to disagree about what the operator is looking at.
 *
 * The permission is the one that decides whether to *offer* the screen; each screen refuses
 * on its own, and so does the server (see permissions.ts).
 */
export interface PatientSubroute {
  /** The path segment under `/patients/{id}/`. */
  segment: string;
  labelKey: string;
  permission: PermissionAction;
}

export const PATIENT_SUBROUTES: readonly PatientSubroute[] = [
  { segment: 'edit', labelKey: 'patients.correct.pageTitle', permission: 'clinical.register' },
  { segment: 'duplicates', labelKey: 'patients.review.title', permission: 'clinical.register' },
  { segment: 'consent', labelKey: 'patients.consent.pageTitle', permission: 'clinical.view' },
  // Station 4 (CP53). Its own permission rather than a general clinical one: §4.4 blinds
  // registration and the pharmacist to a patient's history, and an entry offered on
  // `clinical.view` would put it in front of both.
  {
    segment: 'medical-history',
    labelKey: 'history.pageTitle',
    permission: 'history.view',
  },
  // The hard stop (CP54). Its own permission and not `history.view`: reading allergies is
  // deliberately *not* blinded, because `patient.read.allergies` reaches the pharmacist and
  // the prescription educator — the last people who could catch the mistake, and the ones
  // §4.4 blinds to everything else clinical.
  { segment: 'allergies', labelKey: 'allergies.pageTitle', permission: 'allergies.view' },
  { segment: 'growth', labelKey: 'growth.pageTitle', permission: 'clinical.view' },
];

/** Where one of those screens lives for a given patient. */
export function patientSubroutePath(patientId: string, segment: string): string {
  return `/patients/${patientId}/${segment}`;
}

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
