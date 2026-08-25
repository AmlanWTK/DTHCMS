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
