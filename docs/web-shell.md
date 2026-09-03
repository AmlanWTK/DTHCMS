# The web application shell

Established at CP10. The frame every web screen will sit in: routing, layout, language,
permissions and what happens when something breaks.

Detail lives beside the code — [`web/README.md`](../web/README.md). This page is the
decisions.

---

## 1. Language belongs to the person, not the URL

next-intl's documented default puts the locale in the path: `/bn/patients/123`. This
application does not, and the reason is not tidiness.

There is no SEO to win — every authenticated route is `noindex`, because a clinical record
must never be indexed. What there is instead is a physician sending a colleague a link to
a patient. With a locale in the path, that link imposes the sender's interface language on
whoever opens it. With language on the person, everybody reads their own.

The exception is the public prescription-verification page. A patient scanning a QR code
on a printed sheet has no account, no session and no stored preference, so the printed URL
carries `?lang=bn`. The paper decides, which is correct: the paper is what they have.

The language is set by a server action writing a cookie, not by client state, because it
has to be known before the first byte of HTML. Held in the browser only, the shell renders
in English and then flips — on a slow tablet, a visible flash on every load.

## 2. Both the definition and the disk have to agree

`lib/navigation.ts` is the only list of route groups. The sidebar renders from it, a test
asserts that every entry has a real `page.tsx` behind it, another asserts that no route
group exists on disk without an entry, and the browser suite walks it.

The failures this prevents are quiet ones. A link to a path with no page is a 404 nobody
finds until a reviewer clicks that item. A folder full of finished screens with no sidebar
entry is an audience who cannot reach their area, in an application that looks complete.

The same technique holds for permissions: a test asserts no navigation item exists that
no role can see. A dead entry renders for nobody, so no screen ever looks wrong.

## 3. The bilingual guarantee is automated, or it is not a guarantee

Acceptance criterion 2 is that language switching is complete, "verified by an automated
check". That is the load-bearing half. A bilingual interface does not fail loudly — it
fails as one English word in a Bangla screen, in a place the person who reads Bangla
notices and the person who wrote it never looks.

Three different failures, checked separately:

| Failure                                   | How it shows up                   |
| ----------------------------------------- | --------------------------------- |
| A key in one file and not the other       | The string renders as its own key |
| English text copied into the Bangla file  | Invisible to a key-set comparison |
| A key used in code and present in neither | The screen renders the key        |

Values that are legitimately identical — a product name, a format string of two
placeholders — are exempt one at a time, each with its reason written in the test. Same
idea as the design system's contrast contract: an exemption is allowed, and it has to be
argued for.

## 4. An error has to leave the operator something to say

Every route group has its own boundary, so a failure in the research area does not blank
the clinical screen a physician is reading.

Each one shows a reference. Three cases, and they are genuinely different:

- an error from the API carries the correlation ID the server wrote into its log;
- an error during server rendering carries Next's `digest`, which is in the server log;
- an error in the browser is recorded nowhere, so the client mints an ID and the page says
  plainly that it will not appear in the clinic's records.

The third is the one usually left as "something went wrong". It is also the one where the
operator — standing in front of a patient — most needs to be told that reporting it is
worth doing.

`global-error.tsx` replaces the root layout, so it has no providers and cannot know which
language the reader uses. It shows both at once, in hard-coded text with inline styles,
because whatever failed may be the message loader or the stylesheet.

## 5. Impossible to verify is impossible to trust

Two defects in this checkpoint were invisible to every test that does not run a browser.

`connect-src 'self'` looked obviously correct and blocked every call to the API in local
development, where the Go service runs on its own port. The one feature CP10 has would
have failed on a developer's machine and passed the whole suite. Lighthouse found it.

Before that, eight rules in `@dthcms/ui` referenced `var(--space-0.5)` — a custom property
whose name contains a dot, which every reference has to escape. An unescaped one does not
error; the declaration is silently dropped and the gap becomes zero. It had shipped in
CP09 and passed 182 tests. Turbopack's stricter CSS parser is what surfaced it, and the
fix was to rename the tokens so the trap cannot be stepped in again.

Both are why the browser suite is now part of CI rather than something run by hand.

## 5a. Permissions come from the server (CP20)

CP10's invented grant table is gone. `/v1/auth/me` reports the person's roles and, per
role, the permissions the catalogue confers; the role switcher lists the roles held, in
catalogue order, labelled from `role.*`; the sidebar and `usePermission` decide from the
active role's permission set through `lib/permissions.ts`, whose interface actions each
name the server permissions behind them. The chosen role travels as `X-Active-Role` on
every request (`lib/active-role.ts`, read by the API client), so the server decides for
the same hat the interface shows. None of it is a control; every route is guarded
server-side (`docs/access-model.md` §7).

## 6. Open decisions

| Decision                                       | Default taken                                      | Needs             |
| ---------------------------------------------- | -------------------------------------------------- | ----------------- |
| Next.js major version                          | 16, against the plan's 15                          | Recorded, agreed  |
| Where the locale lives                         | On the person; `?lang=` on the public page         | Recorded, agreed  |
| A checkpoint for the `(stations)` desktop area | None. The screen says so rather than inventing one | Dr. Nahid + Amlan |

The plan lists `(stations)` in §14.9 as "desktop fallbacks for station work", and P-1 says
web is not for floor capture. Those are consistent — a fallback is not the normal path —
but nothing schedules it. The route group exists and its screen says it has no checkpoint,
which is the honest state. It should either get one or be dropped.

## 7. Known gaps

| Gap                                     | Why                                                                                                                                                                                                                        | Lands at |
| --------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------- |
| Real authentication                     | **Landed at CP16.** `SessionGate` holds every shelled screen until `/v1/auth/me` answers; the sign-in page is `features/auth`; the role switch remains for accounts that hold more than one role (`docs/identity.md` §7.9) | CP16     |
| A typed API client                      | `lib/api.ts` is hand-written and small on purpose; the generated client replaces it                                                                                                                                        | CP12     |
| Browser-side telemetry                  | Errors go to the console. CP07 wired the backend; there is no endpoint for the browser yet                                                                                                                                 | CP27     |
| Visual regression                       | Screenshot baselines need a fixed environment. The browser suite now runs in CI, which is where they will live                                                                                                             | CP13     |
| `ValueWithAttribution`, `DualUnitValue` | §14.9 requires every clinical value to render through them. There are no clinical values yet                                                                                                                               | CP42     |
