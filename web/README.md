# `@dthcms/web`

The web application: physician, quality control, pharmacy, patient contact, research,
administration and management. Station capture is the mobile app's job (P-1) — this one
has a desktop fallback for it and nothing more.

Built at CP10 as a shell. There are no clinical screens yet, and every route says which
checkpoint fills it.

```bash
pnpm --filter @dthcms/web dev        # http://localhost:3100
pnpm --filter @dthcms/web test       # 148 assertions, no browser
pnpm --filter @dthcms/web e2e:install # once: downloads Chromium
pnpm --filter @dthcms/web e2e        # 27 assertions, in a browser
```

---

## Where things go

```
src/
├── app/            App Router. Route groups by audience, one folder each.
├── features/       Feature-first, mirroring the backend modules.
├── components/     The shell itself. No domain knowledge.
├── lib/            api, i18n, permissions, formatters, navigation.
├── stores/         Zustand: session, ui.
└── styles/         globals.css — layout, keyed to design tokens.
```

A feature is imported through its `index.ts` and never by reaching inside it. That is an
ESLint rule, not a convention: `import { useSystemStatus } from '@/features/system-status/api/...'`
fails the build.

## Decisions worth knowing about

### The locale is not in the URL

next-intl's documented default is `/bn/patients/123`. This application uses its
"without i18n routing" mode instead, and language lives on the person.

There is no SEO to win here, and a physician sending a colleague a link to a patient
would otherwise impose their own interface language on whoever opens it. The one
exception is the public verification page: a patient scanning a QR code on a printed
prescription has no account, no session and no stored preference, so the printed URL
carries `?lang=bn` and the page opens in the language the prescription was printed in.

The language is set by a server action writing a cookie, not by client state. It has to
be known on the server before the first byte of HTML, or the shell renders in English and
then flips — which on a slow tablet is a visible and unpleasant flash.

### Nothing is kept in web storage

[ADR-0010](../docs/adr/0010-no-session-tokens-in-web-storage.md). Sessions will be
`httpOnly` cookies; `localStorage`, `sessionStorage` and `document.cookie` fail the lint
under `web/src`, and an end-to-end test asserts that nothing arrives there by way of a
dependency either.

### Route groups have their own error boundaries

Nine groups, nine `error.tsx` files. A failure in the research area should not blank the
clinical screen a physician is reading.

Every boundary shows a **reference** the operator can quote. Three cases, and they are
genuinely different: an error from the API carries the correlation ID the server logged;
an error during server rendering carries Next's `digest`, which appears in the server
log; an error in the browser has no record anywhere, so the client mints one and the page
says plainly that it will not appear in the clinic's records. The third is the one usually
left as "something went wrong", and the one where reporting it matters most.

`global-error.tsx` replaces the root layout, so it has no providers and no way to know
which language the reader uses. It shows both at once, with hard-coded text and inline
styles, because whatever failed may be the message loader or the stylesheet.

### The Content Security Policy is nonce-based

`src/proxy.ts` (Next 16's name for what used to be middleware) builds a fresh nonce per
response and a `script-src` with `'strict-dynamic'`. A policy that lists origins is only
as strong as the weakest thing on the list; with `strict-dynamic`, an injected `<script>`
in a patient name — the actual attack — does not execute.

`connect-src` names the API origin when it is not same-origin. That line is not
decoration: without it the browser refuses every call to the Go service in local
development, and no test that does not run a browser can see it. Ours did not, and
Lighthouse found it.

### Permissions decide what is rendered, and nothing else

`usePermission()` and `<Can action="…">` hide what the operator cannot do, because a
control that returns "denied" teaches people the software is unreliable. The server
denies independently. Everything in `lib/permissions.ts` is a stub with a real shape,
replaced at CP16 and CP20.

The role switch in the top bar is scaffolding and says so. Without it, five of the nine
route groups would be unreachable before authentication exists, and CP10's manual
verification step is "navigate all route groups".

## Testing

Two suites, deliberately separated.

`pnpm test` needs nothing but Node. It covers the message files against the code that
uses them, the navigation definition against the files on disk, the retry policy, the
error envelope, the formatters and the stylesheet.

`pnpm e2e` needs a browser and a production build, which is why it is not part of
`pnpm run verify` — a verification step that cannot run on a fresh clone is one people
learn to skip. It covers what only a browser can see: response headers, the real
stylesheet, a real navigation, and the error boundary firing.

The dividing line is not "unit versus integration". It is whether a browser is required.

## Measured at CP10

Lighthouse, desktop preset, against the production build of `/dashboard`:

| Category       | Score | Note                                                                 |
| -------------- | ----- | -------------------------------------------------------------------- |
| Performance    | 100   | FCP 0.5s, LCP 0.8s, TBT 10ms, CLS 0.013                              |
| Accessibility  | 100   |                                                                      |
| Best practices | 93    | Console errors from the Go API being unreachable in the sandbox      |
| SEO            | 60    | `robots: noindex` on purpose. A clinical record must not be indexed. |

Acceptance criterion 4 asks for performance ≥ 90.
