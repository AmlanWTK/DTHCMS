# Testing

Established at CP13. The TypeScript half came first; the Go integration harness and the
synthetic data generator followed in a second pass, and §8 records why.

The short version: **every layer tests what only it can test.** A check that can pass
without a browser runs without one, because those run in two seconds on every save. A
check that needs a device waits for hardware rather than being faked in jsdom, because a
fake that passes where the real thing would fail is worse than no test.

---

## 1. The layers

| Layer                     | Where                                                                                                      | Runs                                | Gates                                                                |
| ------------------------- | ---------------------------------------------------------------------------------------------------------- | ----------------------------------- | -------------------------------------------------------------------- |
| Unit and integration (TS) | `*/test/`                                                                                                  | `pnpm run verify`                   | Logic, schemas, message discipline, error translation                |
| Integration (Go)          | `backend/…/testsupport`                                                                                    | `make test`                         | Real PostgreSQL and Redis, one private database per test             |
| Contract                  | `backend/cmd/api/contract_test.go`, `backend/…/httpx/conformance_test.go`, `packages/shared-schemas/test/` | `go test`, `pnpm run verify`        | Router and OpenAPI document agreeing, in both directions             |
| Compile (mobile)          | `bundle:check`                                                                                             | CI, every push                      | Every screen, font and token import actually resolving               |
| Browser                   | `web/e2e/`                                                                                                 | `pnpm --filter @dthcms/web run e2e` | Real navigation, real stylesheet, real response header               |
| Offline (§13.10)          | `mobile/test/offline/`                                                                                     | CI, every push                      | All ten §13.10 scenarios, the integrity checker, the mutation matrix |
| Soak                      | `mobile/test/soak/`                                                                                        | Nightly                             | A simulated clinic day on a new seed each night                      |
| Device                    | `mobile/maestro/`                                                                                          | Waits on **D-59**                   | Install, cold start, Bangla at 200% font scale, §13.10 on hardware   |
| Load                      | `load/`                                                                                                    | **CP93**                            | Scaffolding only today                                               |

### Where the contract test lives, and why it moved

The contract test is in two halves, because the two things it checks are visible from
different places.

The **route** half is in `backend/cmd/api/contract_test.go`. It has to be: the full surface
only exists once the authentication endpoints are mounted beside the operational ones, and
`internal/platform/httpx` may not import a module — `architecture.json` says `platform`
imports nothing, and the check is enforced by `dthclint`. The composition root is the only
place the whole surface is assembled, so it is the only place the whole surface can
honestly be checked. `main.go` builds the router through a small `surface` type for exactly
this reason: the test and the process read the same route table, rather than two lists that
are meant to match.

CP16 is what made this necessary. Six `/v1/auth/*` routes were added to the contract and
served by the binary, while the test built its own router without them — and reported the
six as documented-but-unimplemented. The test was right that the two disagreed and wrong
about which one was incomplete, which is the failure mode of a test that constructs a
substitute for the thing it is checking.

The **schema** half stays in `backend/internal/platform/httpx/conformance_test.go`, because
it reflects over types that are private to that package.

Both read the document through `internal/platform/apispec`, a small mapping-key scanner
rather than a YAML library: the tests need the keys and nothing else, and the backend module
has no YAML dependency to reuse. It refuses to report success on an empty result, so a
scanner that has quietly stopped understanding the document fails the build instead of
agreeing that everything conforms.

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

| Excluded                                                                                      | Covered instead by                                       |
| --------------------------------------------------------------------------------------------- | -------------------------------------------------------- |
| Build output, generated clients                                                               | The regeneration diff in CI                              |
| `*.stories.tsx`                                                                               | Storybook's accessibility suite, and by eye              |
| `web/src/app/**`                                                                              | Playwright, in a real browser against a production build |
| `mobile/src/app/**`, `src/components/**`, `src/features/**/*.tsx`, the React modules in `lib` | **Maestro — not yet live.** See below                    |

The mobile exclusion is the only one whose covering layer does not exist yet, because
Maestro cannot run until D-59 names the device. It is recorded here and in the config
rather than hidden, and it closes when D-59 does.

It grew at CP45–CP49, and the shape of that growth is the thing to watch. Every station
screen is a React Native component and none of them can be rendered here, so each one
arrives as several hundred uncovered lines. What keeps that from being a hole is where the
station's decisions live: the field order, the plausibility warnings, the batch body, the
queue's call order and the vitals ranges are all in a `form.ts` or `state.ts` beside the
screen, and those files are at 100%. The screen is left holding layout — which is what
Maestro, and a clinical assistant with the real device, will judge anyway.

The rule that keeps this honest is the one above: if a decision ever moves _into_ a `.tsx`
file, it has moved out of the denominator, and the fix is to move it back out rather than
to widen the exclusion.

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

### The migrations run once, not once per test

`Postgres(t)` used to do the obvious thing: create a database, apply all fifty migrations,
hand it over. On Linux against a local server that is about a second and a half, and for a
year nobody noticed. Then the same suite was run on Windows with Docker Desktop, where every
statement crosses a port proxy into a virtual machine, and the same run costs twenty to
thirty seconds. A package with forty database tests therefore spent twenty minutes applying
the same migrations forty times — past Go's **ten-minute per-package timeout**, which is
reported as `panic: test timed out` naming whichever test happened to be running.

Two things are worth separating there. The suite was slow, which is annoying. The suite was
**unrunnable on a developer's actual machine**, which is the failure this document keeps
warning about from the other direction: a suite people stop running is a suite that stops
being true, and it does not matter whether they stop because it skips silently or because it
never finishes.

PostgreSQL can copy a database at the file level — `CREATE DATABASE x TEMPLATE y` — for a few
hundred milliseconds regardless of how many migrations built `y`. So the migrations now run
**once**, into a template, and every test gets a copy. On Linux that took the heaviest
packages from 231s to 115s and from 101s to 27s; where the migration run is the expensive
part, the saving is most of the suite.

Three details carry the safety of it:

- **The template is named after the migrations' contents**, not fixed. A fixed name would
  serve a stale schema the moment somebody edited a migration — the template exists, so it is
  reused, so the tests pass against last week's database. That is a green run that checked
  nothing, which is the same failure as a silent skip. The name carries a hash of every
  migration file's name and bytes, so an edit produces a name nothing has built yet.
- **It is built under a provisional name and renamed on success.** A process killed part way
  through migrating would otherwise leave a half-built database under the final name, which
  every later test would copy and trust.
- **`TestACopiedTemplateIsTheSameDatabaseTheMigrationsWouldHaveBuilt`** compares a copied
  database against a migrated one — columns, constraints, indexes, triggers, whole function
  bodies, table privileges, extensions and the seeded catalogue counts. Not just table names:
  most of what this repository proves in SQL is proved with privileges and triggers, and a
  shallower comparison would pass while the interesting half of the schema was missing.

`DTHCMS_TEST_NO_TEMPLATE=1` goes back to migrating each test's own database, for when the
harness itself is under suspicion. `make test-templates-drop` clears the cached templates,
which accumulate one per schema version the machine has tested.

**The local Postgres runs with `fsync=off`.** It holds nothing that must survive a crash —
test databases created and dropped inside one run, and a development database rebuilt by
`make reset` and `make synth` — and applying the migrations is several hundred small DDL
transactions whose commits were the cost being multiplied. Never on a deployed server: an
ungraceful power-off there leaves the data directory unrecoverable rather than merely stale.

**`make test` passes `-timeout 30m`.** Go's default is ten minutes per package, and a slow
server plus `-race` can exceed it on a machine where nothing is actually wrong.

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

## 2b. The offline suite (CP68)

Offline correctness is the one area of this system where a defect is **silent by
construction**: nothing throws, no screen goes red, and the first person to notice is a
physician wondering why a patient has no vitals. Every other layer in this document exists
to catch something that announces itself. This one exists to catch something that does not.

Four pieces, in `mobile/test/`:

| Piece                 | File                   | What it is                                                            |
| --------------------- | ---------------------- | --------------------------------------------------------------------- |
| The §13.10 matrix     | `offline/scenarios.ts` | The blueprint's ten scenarios, as runnable functions                  |
| The integrity checker | `sync-integrity.ts`    | Thirteen claims comparing the device and the clinic, event for event  |
| The chaos harness     | `offline/chaos.ts`     | A clinic that answers badly, reproducibly, from one integer           |
| The mutation harness  | `offline/mutations.ts` | Twelve deliberate sync bugs, each naming the claim that must catch it |

`offline-matrix.test.ts` runs the first two on every push; `sync-mutation.test.ts` runs the
fourth; `soak/clinic-day.soak.ts` runs a clinic day nightly.

### §13.10 is transcribed, not summarised

The blueprint's §13.10 is one paragraph of ten clauses. Each is copied verbatim into the
scenario that runs it, and `offline-matrix.test.ts` asserts the ten transcriptions still
equal the paragraph. That looks like bookkeeping and is the one check here that cannot be
satisfied by writing more code: "all ten scenarios run" is exactly the shape of claim that
decays into "all nine remaining scenarios run" the first time somebody deletes an awkward
one, and this repository has been caught three times by a green check that confirmed
something _existed_ rather than that it _worked_.

The scenarios also carry `reach` and `awaits`, and the matrix test fails a scenario that
claims to need hardware without saying what for. Every green tick in this layer is therefore
accompanied, in the same file, by a sentence naming what it does **not** cover.

### The integrity checker states claims, and each names a loss

Every rule is a sentence about what must be true beside the clinical loss it detects, and
the claim ids are the vocabulary the mutation matrix speaks. A rule with no stated loss
cannot be argued with, and a rule nobody can argue with does not get deleted when it becomes
wrong — it gets weakened, quietly, by somebody in a hurry.

CP66 shipped six of the thirteen. CP68 added seven, and the shape of what was missing is
worth recording: **five of the seven are about time rather than about state.** Per-record
ordering within a batch, work sent twice, a cursor that runs ahead of what was applied, a
cursor that goes backwards, and an entry put in front of a person while the clinic already
had it. A checker that only compares two records after a run cannot see any of them, because
each is a fact about what happened _during_ one — so two of them are watched as they happen,
in `offline/harness.ts`, rather than looked for afterwards.

The seventh is `refusalDischarged`, and it is there because a mutation walked past every
other rule. CP67 gave an operator a legitimate way to make a refused event leave the queue —
correct it, and the correction replaces it — so rule 1 was widened to allow a refused event
to be absent. That widening also quietly permitted an engine that dropped every rejection on
its own, which is a lost measurement per refusal with no error anywhere. The rule now asks
the harder question: a refusal may be gone only if a correction **names** it.

### The chaos harness is seeded, and that is the first requirement

Random failure injection that cannot be replayed produces a red build with a stack trace
nobody can reach twice, and the honest response to it is to re-run until it goes green —
after which the suite has taught everybody that red means "try again". CP68's own risk note
is flaky tests eroding trust; this is that erosion arriving through the server.

So everything random in a chaotic run comes from one integer, through three separate streams
(the network, the clinic's decisions, the engine's backoff jitter) so that changing one rate
does not shift the others and make a recorded failure unreachable. Nothing consults
`Math.random` and nothing consults the wall clock. A failing assertion carries the seed and
the last few things the weather did.

**Latency is charged to the injected clock, never to a real timer.** Three seconds of
latency implemented with `setTimeout` would put ten real minutes into the matrix and make
every timing assertion a race against CI's load. If a test in this directory ever needs a
real delay, something has stopped taking its clock as a dependency, and that is the bug.

One rule the soak taught, worth more than the bug that produced it: **weather may be random,
decisions may not.** The first version of the soak drew the clinic's accept/refuse decision
fresh each time it saw an event, and within a dozen seeds it produced a failure that looked
exactly like a client defect — an entry refused on a first pass and accepted when the batch
was re-sent. Whether a request arrives is a property of the morning; whether a value is
acceptable is a property of the value.

### The mutation matrix is the checkpoint

A suite nobody has watched fail proves nothing. Every green run of the matrix is consistent
with two worlds — one where the engine is correct and one where the checks are asleep — and
the only way to tell them apart is to break the engine on purpose.

Twelve bugs, each of a kind somebody could introduce and defend in review, injected through
four seams: the engine's own decision functions (by resetting the module registry and mocking
`lib/sync/state` before the graph is imported, so the engine really calls the broken
function), a write altered or swallowed underneath the store, the id generator, and the wire.
Each names **in advance** the claim that must catch it, and the test asserts that claim
specifically — "something failed" is a weak result, and a suite could pass it by being
uniformly noisy.

Two rules govern this file. A mutation that survives is a finding, and the fix is a new
check, never a quieter mutation. And a mutation is only meaningful if the scenario it is
aimed at passes **unmutated**, which is asserted once over the whole set.

Two things it taught that the design did not predict:

- **A skipped acceptance repairs itself.** An event dropped from a receipt plan is in the
  ledger, so the next pull brings it back down and `applyPulled` clears it — the second,
  independent route out of the queue, doing exactly the job CP66 built it for. A skipped
  _refusal_ has no such net, so the mutation is aimed there.
- **ESM mocking cannot replace an intra-module call.** `planFromReceipt` calls `actionFor`
  as a local function, so mocking the export changes nothing the engine does. Three mutations
  are therefore applied to the plan rather than to `actionFor`, which is the same defect one
  level out. Worth knowing before writing the next mutation, and worth stating: it is exactly
  the failure this suite exists to avoid, met while building the suite.

### The soak runs nightly, on a different seed every night

The matrix visits ten states in the same order every time, which makes it a good regression
net and a poor explorer. The soak is eight simulated hours, ~580 measurements, a connection
that drops for minutes at a time, an app killed mid-morning, a clock corrected at lunchtime.
Its assertion is an accounting rather than a delivery count: every measurement ends the day
delivered, held at the clinic, still queued, or refused and on the operator's screen, and the
four add up to the number taken. A count that only checked deliveries would call a tablet
that dropped its refusals a success.

It is **not** in `pnpm test`, and not behind a skip flag either. A skipped test reports
success, which is the reassuring non-answer this whole checkpoint exists to stop producing;
a separate configuration means the soak either runs or is not in the run. The cost is that
`pnpm test` does not compile it, which `pnpm -r typecheck` covers from the other side.

To repeat a failed night, take the seed from the test's own name:

```bash
DTHCMS_SOAK_SEED=20260907 pnpm --filter @dthcms/mobile run test:soak
```

or dispatch the `Nightly soak` workflow with that seed in the input box.

### What none of this reaches, and where that is written down

Nothing here runs on a tablet. Four things are therefore unproven by every green run above:
SQLCipher on a real filesystem, a real radio, the operating system's own clock, and a device
that can genuinely fill up. `mobile/maestro/offline/` holds the flows for the three of those
a flow can drive and a written checklist for the four that need a person with a tablet — the
clock, a real token expiry, a real revocation, and a full disk. `scripts/check_maestro_flows.py`
runs in CI and checks what is checkable with no device: that every flow names this
application, that every command in one is a command Maestro has, and that every flow the
scenario registry names is really on disk.

**A device flow that fails is quarantined, not re-run.** A flow re-run until it passes has
been switched off without anybody saying so, and on this subsystem an intermittent failure is
a report of intermittent data loss until somebody proves otherwise.

## 3. Running things

```bash
pnpm run verify                              # format, lint, spec lint, typecheck, tests + floors
pnpm run test:coverage                       # the floors alone
pnpm --filter @dthcms/web run e2e            # browser suite (needs e2e:install once)
pnpm --filter @dthcms/mobile run bundle:check # Metro compiles every screen
pnpm --filter @dthcms/mobile run test:soak    # the clinic-day soak, on today's seed
python scripts/check_maestro_flows.py        # the device flows, without a device
cd backend && go test ./...                  # Go, including the contract test
cd backend && go run ./tools/aieval          # the frozen AI evaluation set (CP72) — no model, no database
make verify                                  # everything CI runs
.\scripts\verify.ps1                         # the same, on Windows, where make is not installed
```

## 3a. The AI evaluation set (CP72)

A fourth kind of test, and it does not look like the other three. `internal/ai/evalset` holds
twenty-seven **frozen cases** — twenty model answers believed correct and seven deliberately
corrupted — with a manifest of per-file hashes, and `go run ./tools/aieval` re-checks all of them
against the _current_ validator, the _current_ output schema and the _current_ prompt in about a
second, with no model and no database.

It is a regression gate rather than a unit test, and it fails on four things: a missed
hallucination, a new false positive, a frozen answer that no longer satisfies the agent's schema,
and a prompt whose content hash is not the one the committed baseline was blessed against. The last
is §10.5's _"every change to a prompt or model runs the frozen evaluation set"_ — enforced by a
hash in the baseline rather than by a `paths:` filter, because a filter silently stops applying the
day somebody moves the prompts directory and the build stays green either way.

Changing a case is a two-file change with a hash in the diff. That is not a barrier to anybody
honest; it is a barrier to the easiest way of making a regression disappear, which is to change the
case that caught it. `docs/ai-grounding.md` §6 has the transcripts of the gate refusing.

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

| Item                                           | Blocked by                     | Lands at                          |
| ---------------------------------------------- | ------------------------------ | --------------------------------- |
| testcontainers as an optional provider         | Nobody is annoyed enough yet   | When `make up` becomes a nuisance |
| Loading generated patients into a database     | No patient tables exist yet    | CP29                              |
| Maestro flows running                          | **D-59**                       | Device confirmed                  |
| §13.10 on hardware, and its four manual checks | **D-59**                       | Device confirmed                  |
| 90% floor having packages under it             | `clinical-calc`                | CP43                              |
| Load scenarios                                 | Generator, and a real workload | CP93                              |
| Visual regression snapshots                    | A fixed environment            | CP03                              |

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
