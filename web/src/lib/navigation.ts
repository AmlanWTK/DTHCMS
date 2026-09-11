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
        // `dashboard.view` and not `clinical.view` since CP73. The screen is the patient's
        // whole clinical picture, and the roles §4.4 blinds from that would otherwise be
        // offered a first sidebar item they can open and never read — a picker leading to a
        // refusal, which teaches people that the software is unreliable.
        permission: 'dashboard.view',
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
        // Counselling checklists (CP55, §5.1, [R-07]). In the clinical group and not the
        // administration one, though it is a configuration screen, because of who has to
        // reach it. Criterion 1 names a **physician** authoring a template, and the roles
        // holding `counseling.template.read` — the counsellor, the nutritionist, the
        // clinical assistant — hold nothing in `admin.view`'s list. Filed under
        // administration it would be behind a group heading most of its audience cannot
        // see, which is the same as not shipping it.
        href: '/counseling/templates',
        labelKey: 'nav.counselingTemplates',
        icon: 'stethoscope',
        permission: 'counseling.templates.view',
      },
      {
        // What I am being asked to fix (CP62, §4.3). Its own entry rather than a panel on the
        // dashboard: the request was routed to a person because an operator who never learns
        // they mistyped will mistype again, and a queue somebody has to remember to look
        // inside another screen for is a queue nobody looks at.
        //
        // `corrections.queue` and not `corrections.request`: the person with a queue is
        // whoever *recorded* values, and the person who may flag one is somebody else. An
        // entry offered on the flagging permission would put an empty inbox in front of the
        // physician and hide the full one from the operator.
        href: '/corrections',
        labelKey: 'nav.corrections',
        icon: 'inbox',
        permission: 'corrections.queue',
      },
      {
        // My own correction record (CP63, §4.3). Beside the queue, because the two are the
        // same conversation seen from two ends: what somebody is asking me to fix, and what
        // has been asked of me over the last month.
        //
        // `quality.mine`, which every signed-in person holds, and that is ADR-0029 §3 rather
        // than laziness. The endpoint needs a session and nothing else because it reads the
        // caller's own id from it; an operator who has to be granted something before they may
        // see their own correction count is an operator who will assume the count is being
        // kept from them. `corrections.queue` was the tempting choice and is wrong for the same
        // reason it was wrong at CP62: the community field worker records values, receives
        // corrections on them, and holds no read permission at all.
        //
        // The cost is real and accepted: an entry every account can see puts the clinical
        // heading in front of accounts that record nothing, the wall display among them. A
        // heading over one link nobody needs is a smaller failure than a record kept from the
        // person it is about.
        href: '/quality',
        labelKey: 'nav.myQuality',
        icon: 'scroll-text',
        permission: 'quality.mine',
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
    items: [
      { href: '/qa', labelKey: 'nav.qa', icon: 'shield-check', permission: 'qa.view' },
      {
        // The correction patterns across the floor (CP63, §4.3). Its own entry rather than a
        // panel inside `/qa`, because a supervisor comes here to do one thing and a surface
        // reached inside another surface is a surface found a week late.
        //
        // `quality.team` and not `qa.view`: the chief consultant holds `quality.read.team` and
        // does not hold `qa.review`, so an entry offered on the QA action would hide this from
        // the one person §4.3 describes doing the retraining. The group heading appears for
        // them with this single link under it, which is correct — it is the only thing in the
        // area they may read.
        href: '/qa/quality',
        labelKey: 'nav.qualityTeam',
        icon: 'users',
        permission: 'quality.team',
      },
    ],
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
      {
        // The background queue (CP69, ADR-0031). Its own entry rather than a panel inside
        // `/admin`, because it is opened at the moment somebody suspects work has stopped —
        // usually because a physician has just said the AI summary was not ready — and a
        // surface reached inside another surface is a surface found five minutes late.
        //
        // `admin.jobs.view`, which is `ops.jobs.read`, and not `admin.view`: the physician and
        // QA hold the queue read and hold none of the seven permissions behind `admin.view`
        // except `audit.read`. Offered on `admin.view` the entry would still appear for them
        // today by that one coincidence, and would vanish the day somebody narrowed the audit
        // grant — hiding the queue from exactly the two roles §7.1 makes a promise to.
        href: '/admin/jobs',
        labelKey: 'nav.jobs',
        icon: 'refresh-cw',
        permission: 'admin.jobs.view',
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
  // The counselling checkpoint and what was covered (CP57, §5.5). `counseling.sessions.view`
  // and not `counseling.templates.view`: reading what a counsellor covered for one patient is
  // clinical detail about that patient, and the server grants it under its own permission —
  // the same nine roles today, but the two questions are different and will not stay
  // together. It is also emphatically not the tick permission: this screen writes nothing to
  // a checklist, and an entry offered on `counseling.tick` would put a physician's panel
  // behind the right to write on somebody else's work.
  {
    segment: 'counseling',
    labelKey: 'counseling.panel.pageTitle',
    permission: 'counseling.sessions.view',
  },
  { segment: 'growth', labelKey: 'growth.pageTitle', permission: 'clinical.view' },
  // The whole record on one time axis (CP74, §8). `clinical.view` and not
  // `observations.view`: the chart's *lanes* are the timeline, which asks for
  // `patient.read.demographics` and which the registration desk holds — and the numeric
  // overlays are withheld inside the response, with a sentence saying so, rather than by
  // hiding the entry. Gating the entry on the narrower permission would take the record's
  // shape away from a reader who is allowed to see it.
  { segment: 'timeline', labelKey: 'timeline.pageTitle', permission: 'clinical.view' },
  // The value history and the correction chain (CP62, §4.3, criterion 5). `observations.view`
  // and not `clinical.view`: this screen is nothing but recorded clinical values, and
  // `clinical.view` asks for `patient.read.demographics`, which the registration desk holds
  // and which does not carry the right to read a measurement. Offered on that, the entry would
  // appear for the one role whose every request the screen makes would be refused.
  {
    segment: 'values',
    labelKey: 'corrections.history.pageTitle',
    permission: 'observations.view',
  },
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
