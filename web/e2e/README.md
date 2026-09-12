# `web/e2e/` — the browser suite

```bash
pnpm --filter @dthcms/web run e2e:install   # once: downloads Chromium
pnpm --filter @dthcms/web run e2e
```

Not part of `pnpm run verify`, deliberately. This suite needs a browser download and a
production build, and a verification step that cannot run on a fresh clone is a
verification step people learn to skip. CI runs it as its own job.

## What belongs here

Anything that needs a real navigation, a real stylesheet, or a real response header —
and nothing else. If a check can pass without a browser it belongs in `web/test/`, where
it runs in two seconds on every save rather than ninety in CI.

It earns its minutes: the Content Security Policy blocking every call to the API was a
CP10 defect that no test without a browser could see. `web/test/proxy.test.ts` now checks
the policy's _contents_; only this suite checks whether a browser accepts it.

## Fixtures

`fixtures/` provides `bangla` (the interface in Bangla, via the locale cookie) and
`signedIn` (inert until CP16, correct shape now). Import `test` and `expect` from there
rather than from `@playwright/test` in any spec that needs either.

## The CP81 measurement suite

`cp81-prescribe.spec.ts` is the exception to everything above: it does **not** mock the API.

The numbers CP81 is judged on — how long a four-item prescription takes, how quickly safety
findings appear — are meaningless against a mock. A completion time measured against a fixture is
a measurement of React; a latency measured against a fixture is a measurement of `setTimeout`. So
it signs in for real, against the Go service, against Postgres, with the real formulary and the
real forty-eight unapproved rules, and it is skipped without them:

```bash
# the stack: Postgres, Redis, migrations, dev-seed accounts, the API, the web build
# then, once, enrol the physician's second factor and save its seed to /tmp/doc01.totp
# then, because no browser session can perform a clinical write today (docs/prescription-editor.md §7):
node scratch/cp81/device-proxy.mjs        # :8081 → :8080, signing each request as one device

DTHCMS_E2E_LIVE=1 \
DTHCMS_E2E_PATIENT=<a patient id with an open visit> \
NEXT_PUBLIC_API_BASE_URL=http://127.0.0.1:8081 \
  pnpm --filter @dthcms/web run e2e -- e2e/cp81-prescribe.spec.ts
```

It prints its measurements as `CP81-MEASURE …` lines and **asserts none of them against a
target**: the plan's 90 seconds is a proposal pending Dr. Nahid's paper baseline, and an assertion
would turn an unvalidated number into a gate.

It is one test on one page after one navigation, which is a consequence of two real properties of
the system rather than a style: a TOTP code cannot be reused inside its thirty-second step, and a
full page navigation does not reliably keep the session. Both are written up in
`docs/prescription-editor.md` §7.
