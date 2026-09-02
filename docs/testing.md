# Testing

Established at CP13. The TypeScript half came first; the Go integration harness and the
synthetic data generator followed in a second pass, and §8 records why.

The short version: **every layer tests what only it can test.** A check that can pass
without a browser runs without one, because those run in two seconds on every save. A
check that needs a device waits for hardware rather than being faked in jsdom, because a
fake that passes where the real thing would fail is worse than no test.

---

## 1. The layers

| Layer                     | Where                                                                  | Runs                                | Gates                                                    |
| ------------------------- | ---------------------------------------------------------------------- | ----------------------------------- | -------------------------------------------------------- |
| Unit and integration (TS) | `*/test/`                                                              | `pnpm run verify`                   | Logic, schemas, message discipline, error translation    |
| Integration (Go)          | `backend/…/testsupport`                                                | `make test`                         | Real PostgreSQL and Redis, one private database per test |
| Contract                  | `backend/…/httpx/conformance_test.go`, `packages/shared-schemas/test/` | `go test`, `pnpm run verify`        | Router and OpenAPI document agreeing, in both directions |
| Compile (mobile)          | `bundle:check`                                                         | CI, every push                      | Every screen, font and token import actually resolving   |
| Browser                   | `web/e2e/`                                                             | `pnpm --filter @dthcms/web run e2e` | Real navigation, real stylesheet, real response header   |
| Device                    | `mobile/maestro/`                                                      | Waits on **D-59**                   | Install, cold start, Bangla at 200% font scale           |
| Load                      | `load/`                                                                | **CP93**                            | Scaffolding only today                                   |

## 2. Coverage, and what a floor means

**70% overall. 90% on clinical calculation and safety-rule packages.** Confirmed by
Dr. Nahid at CP13, enforced in `pnpm run verify` and in CI — the same gate in both, so a
developer never learns something from CI they could not have learned locally.

The 90% floor has no packages under it yet. `clinical-calc` arrives at CP43, and the
floor is declared now so that the checkpoint that creates it inherits the number rather
than negotiating it.

Branch coverage sits fifteen points below the statement floor, deliberately. Every
primitive carries branches for props no screen sets yet — a `Card` with five variants
used at two. Holding branches to the statement floor means writing tests for combinations
nobody renders, which produces a number rather than confidence.

### The denominator is the part that matters

`packages/ui` measured **1.18%** with 183 tests passing, because a built Storybook bundle
sat in the working directory and every minified asset in it counted as uncovered source.
The real figure was 90.6%. A number that moves 89 points depending on whether somebody
happened to run a build is not a quality signal.

So the exclusions live in one reviewable list, in `packages/test-config`, under one rule:

> **An exclusion must name the layer that covers the code instead.**

Anything excluded because it is hard to test is not an exclusion — it is an untested file,
and it belongs in the denominator where it can embarrass someone.

| Excluded                                                                 | Covered instead by                                       |
| ------------------------------------------------------------------------ | -------------------------------------------------------- |
| Build output, generated clients                                          | The regeneration diff in CI                              |
| `*.stories.tsx`                                                          | Storybook's accessibility suite, and by eye              |
| `web/src/app/**`                                                         | Playwright, in a real browser against a production build |
| `mobile/src/app/**`, `src/components/**`, the two React modules in `lib` | **Maestro — not yet live.** See below                    |

The mobile exclusion is the only one whose covering layer does not exist yet, because
Maestro cannot run until D-59 names the device. It is recorded here and in the config
rather than hidden, and it closes when D-59 does.

## 2a. The Go integration harness

`internal/platform/testsupport` gives a test its own database, created before it runs and
dropped after.

```go
db := testsupport.Postgres(t)   // fresh database, every migration applied
db.Seed(t, `INSERT INTO …`)
cache := testsupport.Redis(t)   // an isolated key prefix
```

Nothing is mocked. Everything worth asserting at this layer is a property of the real
thing — the privilege system that makes the ledger append-only, the constraints, the
transaction semantics — and a mock would only confirm that the mock agrees with the test.

Without `DTHCMS_TEST_POSTGRES_URL` and `DTHCMS_TEST_REDIS_URL` these tests **skip** rather
than fail. `make up` starts both; `make test` sets both. A suite that cannot run on a fresh
clone is one people learn to ignore, and a red build nobody can fix is worse than a skipped
one.

**Redis isolates by key prefix, not by database index.** Redis offers sixteen numbered
databases, which is a ceiling on parallel tests and an unpleasant one to hit: the
seventeenth test does not fail, it quietly shares state with the first. Cleanup deletes by
prefix — never `FLUSHDB`, which would take the other parallel tests and a developer's local
cache with it.

**testcontainers-go is deliberately not used yet**, though the plan names it. CP04's compose
stack already provides both services and CI already declares them, so testcontainers'
contribution here is convenience — `go test` without `make up` first — rather than
capability. Adding it is a change confined to this one package: provision a container when
the environment variables are absent. Worth doing the day somebody is annoyed enough by
typing `make up`, and not before.

**Entity builders arrive with entities.** A builder for a patient with a visit and three
observations cannot be written before those tables exist; `Seed` is the piece that is useful
until CP29.

## 3. Running things

```bash
pnpm run verify                              # format, lint, spec lint, typecheck, tests + floors
pnpm run test:coverage                       # the floors alone
pnpm --filter @dthcms/web run e2e            # browser suite (needs e2e:install once)
pnpm --filter @dthcms/mobile run bundle:check # Metro compiles every screen
cd backend && go test ./...                  # Go, including the contract test
make verify                                  # everything CI runs
.\scripts\verify.ps1                         # the same, on Windows, where make is not installed
```

## 4. What CP13 found by turning the gate on

Worth recording, because it is the argument for gates generally.

- **`mobile/src/lib/secure-storage.ts` had no test at all** — the Keystore wrapper that
  CP11 acceptance criterion 4 rests on. The cause was mechanical: mobile's Vitest config
  never had the `@/` alias, so the module could not be imported and the allowlist got
  tested in isolation instead. A missing alias had been quietly deciding which modules
  were testable.
- **`web/src/proxy.ts` had no test** — the Content Security Policy and per-response nonce,
  which is where CP10's worst defect lived and was caught by Lighthouse rather than by the
  suite.
- **`packages/api-client/src/retry.ts` was at 0% functions** — the module holding "never
  retry a mutation", tested through two surfaces' query configs but never in the package
  that owns the rule.

None of those were visible before there was a floor.

## 5. Writing a test here

**Say why, not what.** `it('never retries', …)` is a fact; the comment explaining that a
retried write becomes two rows in an append-only ledger is why anybody would keep it.

**Test the failure path.** An error envelope missing its Bangla message, an undeclared
Keystore key, a page shorter than `limit` that is not the end of a list. The happy path
tends to get exercised by hand; the failure path does not.

**Prefer a real module to a mock.** Mocks are for the boundary — the native Keystore, the
device locale, `fetch`. Everything inside the boundary should be the real thing, or the
test measures the mock.

**Tests may reach into a feature's internals.** The ESLint boundary rule is relaxed under
`test/` on purpose: a unit test for `model/status.ts` importing `model/status.ts` is the
job. Forcing tests through `index.ts` would make features export things for testing rather
than because a caller needs them.

## 6. Bilingual testing

Message completeness is checked automatically for both surfaces — every key present in
both files, no orphans. What that cannot check is whether a screen _renders_ both
correctly, so anything with text gets a Bangla case. `placeholder.test.tsx` shows the
pattern, and the thing it asserts is not the translation but that the checkpoint reference
survives it: the one token in the sentence that must not be translated.

Beware a test written against a message key that does not exist. next-intl only _warns_ on
an unresolved key, so such a test passes while asserting nothing.

## 7. No patient data. Ever.

Synthetic only, from the generator (§8). Not in fixtures, not in a `.env`, not
"temporarily" in a test file. `CONTRIBUTING.md` states the rule and the pre-commit hook
gives the first warning; neither is a substitute for not doing it.

## 8. Carried forward

| Item                                       | Blocked by                     | Lands at                          |
| ------------------------------------------ | ------------------------------ | --------------------------------- |
| testcontainers as an optional provider     | Nobody is annoyed enough yet   | When `make up` becomes a nuisance |
| Loading generated patients into a database | No patient tables exist yet    | CP29                              |
| Maestro flows running                      | **D-59**                       | Device confirmed                  |
| 90% floor having packages under it         | `clinical-calc`                | CP43                              |
| Load scenarios                             | Generator, and a real workload | CP93                              |
| Visual regression snapshots                | A fixed environment            | CP03                              |

### The synthetic data generator

`backend/cmd/synthgen` produces a coherent fictional population from the clinician-authored
case-mix; `docs/synthetic-data-profile.md` §8 documents it. Three things about it belong here:

- **It is tested against the profile, not against itself.** Every share the clinician stated
  is asserted within tolerance over a 20,000-patient cohort, with a fixed seed, so the tests
  are exact rather than statistical and cannot flake.
- **The coherence tests matter more than the distribution tests.** A share that is a point
  off is a fidelity question; a pregnant man is the record that makes a clinician stop
  trusting every other record in the file, and there is no partial credit for that.
- **Its manual verification is a clinician reading it.** `synthgen -review` renders a cohort
  as a page for exactly that, and rendering it found three defects that no assertion over
  the same data could have caught — the third being a rule that was dead code, right in
  every line and wrong in its ordering.

The Go half is a second pass because it cannot be verified where it is written: this
sandbox has no Docker daemon and no route to `proxy.golang.org`. Writing a testcontainers
harness blind and handing it over to be debugged by paste is slower than doing the
verifiable half first — which is the sequencing Dr. Nahid chose at CP13.
