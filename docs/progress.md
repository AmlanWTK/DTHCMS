# Checkpoint progress

The running record of what has been built, so that any session — or any engineer joining
later — can see the state of the project without reading the whole implementation plan.

Full specifications: [`implementation-plan.md`](implementation-plan.md) §16.

| CP   | Name                                             | Status       | Notes                                                                                                                                                    |
| ---- | ------------------------------------------------ | ------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| CP01 | Repository, monorepo scaffolding & CI skeleton   | **Done**     | Repo, hooks, CI, blueprint custody hashes recorded                                                                                                       |
| CP02 | Architecture guardrails, ADRs & coding standards | **Done**     | `dthclint` arch + PHI checks; 7 ADRs; standards; Definition of Done                                                                                      |
| CP03 | Cloud project, environments & IaC baseline       | **Deferred** | Hosting decision postponed (D-01). Nothing before CP69 needs it                                                                                          |
| CP04 | Local development environment                    | **Done**     | Postgres, Redis, MinIO, mock AI/OCR, mail capture; one-command start                                                                                     |
| CP05 | Go backend skeleton & platform layer             | **Done**     | Four binaries, fail-fast config, PHI-safe logging, error model, health endpoints, graceful shutdown                                                      |
| CP06 | Database foundation & migration framework        | **Done**     | goose migrations embedded; six schemas; grants making `ledger` append-only at the database; sqlc                                                         |
| CP07 | Observability baseline                           | **Done**     | OTLP tracing and RED metrics; PHI redaction extended to spans and metric labels; 3 dashboards, 4 alerts                                                  |
| CP08 | Prototype assessment                             | **Closed**   | No prototype and no patient data exist (D-51). One-line decision record                                                                                  |
| CP09 | Design system foundation                         | **Done**     | Generated OKLCH ramps, contrast contract, 7 clinical statuses, 11 primitives, Storybook                                                                  |
| CP10 | Web application shell                            | **Done**     | Next.js 16 App Router; nine route groups; bilingual shell with an automated completeness check; CSP                                                      |
| CP11 | Mobile application shell                         | **Done**\*   | Expo SDK 57 shell: 5 route groups, bilingual, secure-storage allowlist, crash scrubbing. \*On-device acceptance waits on D-59                            |
| CP12 | API contract & generated clients                 | **Done**     | OpenAPI 3.1 contract of record; conformance test both ways; generated TS client used by web and mobile; conventions documented                           |
| CP13 | Test harness & synthetic data generator          | **Partial**  | Coverage floors (70/90) enforced; Go integration harness with per-test database isolation; E2E scaffolds; testing guide. Generator waits on the case-mix |
| CP14 | Phase 0 review & architecture sign-off           |              |                                                                                                                                                          |

## Decisions taken so far

| ID   | Decision                                                                                                                 | Recorded in                                                  |
| ---- | ------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------ |
| D-51 | No prototype, no existing patient data — greenfield                                                                      | Implementation plan §3.H                                     |
| D-52 | Built by Dr. Nahid and Claude; second engineer recommended by Phase 2                                                    | Implementation plan §3.H                                     |
| D-07 | Google Gemini as the AI provider; free tier for development only, paid or Vertex AI for anything touching a real patient | [ADR-0007](adr/0007-gemini-provider-and-tier.md)             |
| —    | Modular monolith rather than microservices                                                                               | [ADR-0002](adr/0002-modular-monolith.md)                     |
| —    | Event sourcing for clinical data only                                                                                    | [ADR-0003](adr/0003-event-sourcing-scope.md)                 |
| —    | Offline-first station app from Phase 1                                                                                   | [ADR-0004](adr/0004-offline-first-from-phase-1.md)           |
| —    | Staff authentication implemented in-house                                                                                | [ADR-0005](adr/0005-self-implemented-staff-auth.md)          |
| —    | PostgreSQL now, AlloyDB only when measured                                                                               | [ADR-0006](adr/0006-postgresql-first.md)                     |
| —    | The append-only ledger is enforced by database privilege, not application code                                           | [ADR-0008](adr/0008-database-enforced-append-only-ledger.md) |
| D-35 | Observability is OpenTelemetry/OTLP throughout; the backend stays a configuration choice                                 | [ADR-0009](adr/0009-vendor-neutral-observability.md)         |
| —    | Alerts go to Amlan alone until CP16; a physician paged about pool saturation cannot act on it                            | [`observability.md`](observability.md) §3                    |
| D-61 | `facility_id` on every facility-scoped table from CP06, enforced by an assertion                                         | [`database.md`](database.md) §3                              |
| —    | Colour ramps are generated in OKLCH from a hue and a chroma ceiling, so contrast survives a brand change                 | [`design-system.md`](design-system.md) §1                    |
| —    | Clinical status is never colour alone — colour, icon and bilingual label are all mandatory in the type                   | [`design-system.md`](design-system.md) §2                    |
| —    | Every type step carries a line height per script; `[lang='bn']` switches family and leading together                     | [`design-system.md`](design-system.md) §3                    |
| —    | `NumericInput` separates _impossible_ (rejected) from _implausible_ (warned, recorded); no `type="number"`               | [`design-system.md`](design-system.md) §4                    |
| —    | Primitives ship a token-driven stylesheet rather than Tailwind classes — a departure from the plan                       | [`design-system.md`](design-system.md) §6                    |
| —    | `Select` uses the native element rather than Radix — a departure from the plan                                           | [`design-system.md`](design-system.md) §6                    |
| —    | Next.js 16 rather than the plan's 15 — starting on a superseded major buys an upgrade in Phase 1                         | [`web-shell.md`](web-shell.md) §6                            |
| —    | The locale lives on the person, not in the URL; the public verification page takes `?lang=`                              | [`web-shell.md`](web-shell.md) §1                            |
| —    | Session credentials never touch `localStorage`; enforced by ESLint under `web/src`                                       | [ADR-0010](adr/0010-no-session-tokens-in-web-storage.md)     |
| —    | Every route group carries its own error boundary, and each shows a correlation ID                                        | [`web-shell.md`](web-shell.md) §4                            |
| —    | Fonts are self-hosted from npm (Fontsource), not fetched from Google at build time                                       | [`web-shell.md`](web-shell.md) §5                            |
| —    | Mobile i18n is `use-intl`, not the plan's `i18n-js` — one ICU dialect across both surfaces                               | [`mobile-shell.md`](mobile-shell.md) §6                      |
| —    | Keystore keys are an allowlist that throws on the undeclared; AsyncStorage is banned by lint                             | [`mobile-shell.md`](mobile-shell.md) §4                      |
| —    | Mobile crashes pass one scrubbed choke point before any vendor exists                                                    | [`mobile-shell.md`](mobile-shell.md) §5                      |
| —    | The OpenAPI document is the contract of record; a route and the document disagreeing fails the build                     | [`api-conventions.md`](api-conventions.md) §7                |
| —    | Cursor pagination everywhere, never offsets — a clinical list changes under the person reading it                        | [`api-conventions.md`](api-conventions.md) §3                |
| —    | `Idempotency-Key` is mandatory on every write, generated before the request is queued                                    | [`api-conventions.md`](api-conventions.md) §4                |
| —    | `/v1` is additive-only; anything breaking a client in the field needs `/v2`                                              | [`api-conventions.md`](api-conventions.md) §6                |
| —    | `openapi-typescript` rather than the plan's alternative `orval` — generated types, hand-written error layer              | [`api-conventions.md`](api-conventions.md) §8                |
| —    | Coverage floor is 70% overall, 90% on clinical calculation and safety-rule packages                                      | [`testing.md`](testing.md) §2                                |
| —    | A coverage exclusion must name the layer that covers the code instead, or it is an untested file                         | [`testing.md`](testing.md) §2                                |
| —    | Tests may import a feature's internals; production code may not — the boundary rule is relaxed under `test/`             | [`testing.md`](testing.md) §5                                |

## Still blocking, by the checkpoint they gate

| Decision                                        | Gates                          | Owner                     |
| ----------------------------------------------- | ------------------------------ | ------------------------- |
| D-01 hosting region and PDP Act posture         | CP03, and everything deployed  | Dr. Nahid + legal counsel |
| D-21 pediatric growth reference standard        | CP47, CP48                     | Dr. Nahid                 |
| D-27 critical-value table and escalation        | CP50                           | Dr. Nahid                 |
| D-22 drug knowledge base and interaction source | CP77, CP78                     | Dr. Nahid                 |
| D-24 terminology: ICD version, SNOMED licence   | CP52                           | Legal + clinical          |
| D-53 counseling template content                | CP55                           | Dr. Nahid                 |
| D-70 administrator account recovery             | Before CP16 reaches production | Dr. Nahid                 |
| DTHC brand colour — placeholder teal until then | Nothing; a ramp swap is a hue  | Dr. Nahid                 |
| Numerals in a Bengali interface, `7.8` or `৭.৮` | CP10 onward, cosmetically      | Dr. Nahid                 |
| Bengali clinical labels — my translations stand | CP52, with D-24                | Dr. Nahid                 |
| A checkpoint for the station desktop fallback   | Nothing; the screen says so    | Dr. Nahid + Amlan         |
| D-59: clinic device model and Android floor     | CP11 acceptance criteria 1–3   | Dr. Nahid                 |
| Expo account (EAS builds) and crash vendor      | CP11 criterion 5, dormant CI   | Amlan                     |
| Clinical case-mix for synthetic patients        | CP13 criterion 3, and CP93     | Dr. Nahid                 |

## Carried forward

| From | Item                                                                                                                                                                                                                                                   | Lands at                          |
| ---- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------- |
| CP06 | _Migrate-up on a restored snapshot._ Covered in part by `TestMigrationsRunOverExistingData`, but there is one wave of migrations and no production data to snapshot yet                                                                                | CP23                              |
| CP09 | _Visual regression snapshots._ Screenshot baselines taken on one machine fail on another — font rasterisation differs between Linux and Windows. They need a fixed environment, which means CI                                                         | CP03                              |
| CP09 | _Validation on a real low-end Android device._ Bengali conjunct rendering and the 48px targets are verified in a browser and in the stylesheet, not on the hardware                                                                                    | CP11                              |
| CP13 | _testcontainers as a provider._ The harness uses the CP04 compose stack, which CI already declares; testcontainers would add convenience rather than capability, and was not worth writing unverifiable against a dependency this sandbox cannot fetch | When `make up` becomes a nuisance |
| CP13 | _The clinical case-mix._ The generator is not built against guessed distributions — [`synthetic-data-profile.md`](synthetic-data-profile.md) waits on Dr. Nahid                                                                                        | pass two                          |

Clinical content authoring (implementation plan §17.4) can start at any time and does not
wait for software. It is the most common cause of slip in projects of this shape.
