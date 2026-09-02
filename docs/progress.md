# Checkpoint progress

The running record of what has been built, so that any session — or any engineer joining
later — can see the state of the project without reading the whole implementation plan.

Full specifications: [`implementation-plan.md`](implementation-plan.md) §16.

| CP   | Name                                             | Status       | Notes                                                                                                                                                                                                                                                |
| ---- | ------------------------------------------------ | ------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| CP01 | Repository, monorepo scaffolding & CI skeleton   | **Done**     | Repo, hooks, CI, blueprint custody hashes recorded                                                                                                                                                                                                   |
| CP02 | Architecture guardrails, ADRs & coding standards | **Done**     | `dthclint` arch + PHI checks; 7 ADRs; standards; Definition of Done                                                                                                                                                                                  |
| CP03 | Cloud project, environments & IaC baseline       | **Deferred** | Hosting decision postponed (D-01). Nothing before CP69 needs it                                                                                                                                                                                      |
| CP04 | Local development environment                    | **Done**     | Postgres, Redis, MinIO, mock AI/OCR, mail capture; one-command start                                                                                                                                                                                 |
| CP05 | Go backend skeleton & platform layer             | **Done**     | Four binaries, fail-fast config, PHI-safe logging, error model, health endpoints, graceful shutdown                                                                                                                                                  |
| CP06 | Database foundation & migration framework        | **Done**     | goose migrations embedded; six schemas; grants making `ledger` append-only at the database; sqlc                                                                                                                                                     |
| CP07 | Observability baseline                           | **Done**     | OTLP tracing and RED metrics; PHI redaction extended to spans and metric labels; 3 dashboards, 4 alerts                                                                                                                                              |
| CP08 | Prototype assessment                             | **Closed**   | No prototype and no patient data exist (D-51). One-line decision record                                                                                                                                                                              |
| CP09 | Design system foundation                         | **Done**     | Generated OKLCH ramps, contrast contract, 7 clinical statuses, 11 primitives, Storybook                                                                                                                                                              |
| CP10 | Web application shell                            | **Done**     | Next.js 16 App Router; nine route groups; bilingual shell with an automated completeness check; CSP                                                                                                                                                  |
| CP11 | Mobile application shell                         | **Done**\*   | Expo SDK 57 shell: 5 route groups, bilingual, secure-storage allowlist, crash scrubbing. \*On-device acceptance waits on D-59                                                                                                                        |
| CP12 | API contract & generated clients                 | **Done**     | OpenAPI 3.1 contract of record; conformance test both ways; generated TS client used by web and mobile; conventions documented                                                                                                                       |
| CP13 | Test harness & synthetic data generator          | **Done**\*   | Coverage floors (70/90); Go integration harness with per-test database isolation; E2E scaffolds; `synthgen` generating a coherent population from the clinician's case-mix, 19 tests asserting it. \*Clinical read-through of the cohort outstanding |
| CP14 | Phase 0 review & architecture sign-off           | **Done**     | Held 1 Sep 2026. 14 red decisions resolved, 8 deferred with owner and date; counsel letter drafted. Criterion 2 met; §6 of [`phase-0-review.md`](phase-0-review.md) lists what remains                                                               |

## Phase 1

| CP   | Name                               | Status     | Notes                                                                                                                                                                                                                                               |
| ---- | ---------------------------------- | ---------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| CP15 | User, role & permission data model | **Done**\* | 18 roles, 12 stations, 62 permissions; grants keep their history; users cannot be hard-deleted; blueprint §4.4's access rules enforced as database invariants. \*Role seed is aspirational until DTHC's staffing is known — stations ship unstaffed |

## Decisions taken so far

| ID   | Decision                                                                                                                                 | Recorded in                                                   |
| ---- | ---------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------- |
| D-51 | No prototype, no existing patient data — greenfield                                                                                      | Implementation plan §3.H                                      |
| D-52 | Built by Dr. Nahid and Claude; second engineer recommended by Phase 2                                                                    | Implementation plan §3.H                                      |
| D-07 | Google Gemini as the AI provider; free tier for development only, paid or Vertex AI for anything touching a real patient                 | [ADR-0007](adr/0007-gemini-provider-and-tier.md)              |
| —    | Modular monolith rather than microservices                                                                                               | [ADR-0002](adr/0002-modular-monolith.md)                      |
| —    | Event sourcing for clinical data only                                                                                                    | [ADR-0003](adr/0003-event-sourcing-scope.md)                  |
| —    | Offline-first station app from Phase 1                                                                                                   | [ADR-0004](adr/0004-offline-first-from-phase-1.md)            |
| —    | Staff authentication implemented in-house                                                                                                | [ADR-0005](adr/0005-self-implemented-staff-auth.md)           |
| —    | PostgreSQL now, AlloyDB only when measured                                                                                               | [ADR-0006](adr/0006-postgresql-first.md)                      |
| —    | The append-only ledger is enforced by database privilege, not application code                                                           | [ADR-0008](adr/0008-database-enforced-append-only-ledger.md)  |
| D-35 | Observability is OpenTelemetry/OTLP throughout; the backend stays a configuration choice                                                 | [ADR-0009](adr/0009-vendor-neutral-observability.md)          |
| —    | Alerts go to Amlan alone until CP16; a physician paged about pool saturation cannot act on it                                            | [`observability.md`](observability.md) §3                     |
| D-61 | `facility_id` on every facility-scoped table from CP06, enforced by an assertion                                                         | [`database.md`](database.md) §3                               |
| —    | Colour ramps are generated in OKLCH from a hue and a chroma ceiling, so contrast survives a brand change                                 | [`design-system.md`](design-system.md) §1                     |
| —    | Clinical status is never colour alone — colour, icon and bilingual label are all mandatory in the type                                   | [`design-system.md`](design-system.md) §2                     |
| —    | Every type step carries a line height per script; `[lang='bn']` switches family and leading together                                     | [`design-system.md`](design-system.md) §3                     |
| —    | `NumericInput` separates _impossible_ (rejected) from _implausible_ (warned, recorded); no `type="number"`                               | [`design-system.md`](design-system.md) §4                     |
| —    | Primitives ship a token-driven stylesheet rather than Tailwind classes — a departure from the plan                                       | [`design-system.md`](design-system.md) §6                     |
| —    | `Select` uses the native element rather than Radix — a departure from the plan                                                           | [`design-system.md`](design-system.md) §6                     |
| —    | Next.js 16 rather than the plan's 15 — starting on a superseded major buys an upgrade in Phase 1                                         | [`web-shell.md`](web-shell.md) §6                             |
| —    | The locale lives on the person, not in the URL; the public verification page takes `?lang=`                                              | [`web-shell.md`](web-shell.md) §1                             |
| —    | Session credentials never touch `localStorage`; enforced by ESLint under `web/src`                                                       | [ADR-0010](adr/0010-no-session-tokens-in-web-storage.md)      |
| —    | Every route group carries its own error boundary, and each shows a correlation ID                                                        | [`web-shell.md`](web-shell.md) §4                             |
| —    | Fonts are self-hosted from npm (Fontsource), not fetched from Google at build time                                                       | [`web-shell.md`](web-shell.md) §5                             |
| —    | Mobile i18n is `use-intl`, not the plan's `i18n-js` — one ICU dialect across both surfaces                                               | [`mobile-shell.md`](mobile-shell.md) §6                       |
| —    | Keystore keys are an allowlist that throws on the undeclared; AsyncStorage is banned by lint                                             | [`mobile-shell.md`](mobile-shell.md) §4                       |
| —    | Mobile crashes pass one scrubbed choke point before any vendor exists                                                                    | [`mobile-shell.md`](mobile-shell.md) §5                       |
| —    | The OpenAPI document is the contract of record; a route and the document disagreeing fails the build                                     | [`api-conventions.md`](api-conventions.md) §7                 |
| —    | Cursor pagination everywhere, never offsets — a clinical list changes under the person reading it                                        | [`api-conventions.md`](api-conventions.md) §3                 |
| —    | `Idempotency-Key` is mandatory on every write, generated before the request is queued                                                    | [`api-conventions.md`](api-conventions.md) §4                 |
| —    | `/v1` is additive-only; anything breaking a client in the field needs `/v2`                                                              | [`api-conventions.md`](api-conventions.md) §6                 |
| —    | `openapi-typescript` rather than the plan's alternative `orval` — generated types, hand-written error layer                              | [`api-conventions.md`](api-conventions.md) §8                 |
| —    | Coverage floor is 70% overall, 90% on clinical calculation and safety-rule packages                                                      | [`testing.md`](testing.md) §2                                 |
| —    | A coverage exclusion must name the layer that covers the code instead, or it is an untested file                                         | [`testing.md`](testing.md) §2                                 |
| —    | Tests may import a feature's internals; production code may not — the boundary rule is relaxed under `test/`                             | [`testing.md`](testing.md) §5                                 |
| —    | Blueprint §4.4's three access rules are database assertions, not prose — a migration that breaks one fails                               | [`identity.md`](identity.md) §1                               |
| —    | Users are never deleted: `dthcms_app` holds no DELETE on `core.app_user`, checked after every migration                                  | [`identity.md`](identity.md) §1                               |
| —    | Suspension resolves a user to zero permissions without touching a grant, so it applies and reverses in one statement                     | [`identity.md`](identity.md) §2                               |
| —    | Roles and permissions are system-wide, not facility-scoped; a grant is what carries the facility                                         | [`identity.md`](identity.md) §3                               |
| —    | `patient.read.allergies` is held apart from diagnoses so the pharmacist can dispense safely while staying blinded — **needs confirming** | [`identity.md`](identity.md) §3                               |
| —    | An administrator may not revoke their own ADMIN role, nor suspend their own account (D-70 in miniature)                                  | [`identity.md`](identity.md) §5                               |
| —    | The generator draws the presenting problem first and the person second, so sex-specific categories keep their stated share               | [`synthetic-data-profile.md`](synthetic-data-profile.md) §8.1 |
| —    | Whole-caseload figures (type 1 share, insulin, obesity) are solved for, not sampled per patient                                          | [`synthetic-data-profile.md`](synthetic-data-profile.md) §8.1 |
| —    | The profile decoder accepts `$comment` beside the numbers it explains — the file serves the clinician, not the parser                    | [`synthetic-data-profile.md`](synthetic-data-profile.md) §8   |
| D-59 | Device floor: Android 12 (API 31), 4 GB RAM, 8–10in; one unit for CP11 acceptance                                                        | [`phase-0-review.md`](phase-0-review.md) §2.3                 |
| D-70 | Two administrators from day one plus sealed break-glass — **a gate before CP16 reaches production**                                      | [`phase-0-review.md`](phase-0-review.md) §2.3                 |
| D-56 | The clinic pharmacist owns formulary content and the monthly price review                                                                | [`phase-0-review.md`](phase-0-review.md) §2.3                 |
| D-21 | Growth reference: WHO below 5.0y, CDC 2000 from 5.0y — keeps [R-06]'s ≥95th percentile rule intact                                       | Implementation plan §3.D                                      |
| D-22 | Drug knowledge base: physician-curated rules are the only runtime authority; public sources aid authoring only                           | Implementation plan §3.D                                      |
| D-44 | Short-lived access token; opaque, rotating, revocable refresh token bound to the device                                                  | [`phase-0-review.md`](phase-0-review.md) §2.2                 |
| D-45 | TOTP for privileged roles; device-trusted sessions for floor staff; step-up to sign a prescription                                       | [`phase-0-review.md`](phase-0-review.md) §2.2                 |
| D-46 | Admin-enrolled devices, server-issued keypair in Android Keystore, every request device-bound                                            | [`phase-0-review.md`](phase-0-review.md) §2.2                 |
| D-30 | Cloud Run for API and workers — **conditional on D-01**                                                                                  | [`phase-0-review.md`](phase-0-review.md) §2.2                 |
| D-34 | Google Secret Manager with workload identity — **conditional on D-01**                                                                   | [`phase-0-review.md`](phase-0-review.md) §2.2                 |
| D-37 | RPO ≤5 min / RTO ≤4 h, cross-region backups, rehearsed restore drills — **conditional on D-01**                                          | [`phase-0-review.md`](phase-0-review.md) §2.2                 |
| D-08 | Default deny to the model; pseudonym plus age-in-months, sex and clinical parameters only                                                | [`phase-0-review.md`](phase-0-review.md) §2.2                 |
| D-15 | AI fails visible, never silent, never invented                                                                                           | [`phase-0-review.md`](phase-0-review.md) §2.2                 |
| D-43 | Staff authentication self-implemented in the Go monolith — confirmed at CP14                                                             | [ADR-0005](adr/0005-self-implemented-staff-auth.md)           |

## Still blocking, by the checkpoint they gate

Every 🔴 decision now has an owner and a date — see
[`phase-0-review.md`](phase-0-review.md) §3. What remains:

| Decision                                  | Gates                          | Owner                     | Target      |
| ----------------------------------------- | ------------------------------ | ------------------------- | ----------- |
| D-01 PDP Act posture and hosting region   | CP03, and everything deployed  | Dr. Nahid + legal counsel | 8 Sep 2026  |
| D-38 data residency                       | Final infrastructure sign-off  | Dr. Nahid + legal counsel | 8 Sep 2026  |
| D-02 consent model, scope, revocation     | CP29, CP121, all communication | Dr. Nahid + legal counsel | 8 Sep 2026  |
| D-04 medico-legal status of the signature | CP84                           | Dr. Nahid + legal counsel | 8 Sep 2026  |
| D-24 SNOMED CT licensing                  | CP52                           | SNOMED International      | 8 Sep 2026  |
| D-27 critical-value table and escalation  | CP50                           | Dr. Nahid                 | 30 Sep 2026 |
| D-53 counselling template content         | CP55 content                   | Dr. Nahid                 | 30 Nov 2026 |
| D-54 drug warning library, both languages | CP86 content                   | Dr. Nahid                 | 30 Nov 2026 |

Not decisions, but still open:

| Item                                                 | Gates                                           | Owner             |
| ---------------------------------------------------- | ----------------------------------------------- | ----------------- |
| Buy one tablet meeting the D-59 floor                | CP11 criteria 1–3                               | Dr. Nahid         |
| DTHC's actual staffing — who works there, doing what | CP15's seed being real rather than aspirational | Dr. Nahid         |
| Read the CP13 synthetic cohort                       | CP13 sign-off                                   | Dr. Nahid         |
| Repository write access for pull requests            | CP12 review, and every review                   | Amlan             |
| Expo account (EAS builds) and crash vendor           | CP11 criterion 5, dormant CI                    | Amlan             |
| DTHC brand colour — placeholder teal until then      | Nothing; a ramp swap is a hue                   | Dr. Nahid         |
| Numerals in a Bengali interface, `7.8` or `৭.৮`      | CP10 onward, cosmetically                       | Dr. Nahid         |
| Bengali clinical labels — my translations stand      | CP52, with D-24                                 | Dr. Nahid         |
| A checkpoint for the station desktop fallback        | Nothing; the screen says so                     | Dr. Nahid + Amlan |

## Carried forward

| From | Item                                                                                                                                                                                                                                                   | Lands at                          |
| ---- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------- |
| CP06 | _Migrate-up on a restored snapshot._ Covered in part by `TestMigrationsRunOverExistingData`, but there is one wave of migrations and no production data to snapshot yet                                                                                | CP23                              |
| CP09 | _Visual regression snapshots._ Screenshot baselines taken on one machine fail on another — font rasterisation differs between Linux and Windows. They need a fixed environment, which means CI                                                         | CP03                              |
| CP09 | _Validation on a real low-end Android device._ Bengali conjunct rendering and the 48px targets are verified in a browser and in the stylesheet, not on the hardware                                                                                    | CP11                              |
| CP13 | _testcontainers as a provider._ The harness uses the CP04 compose stack, which CI already declares; testcontainers would add convenience rather than capability, and was not worth writing unverifiable against a dependency this sandbox cannot fetch | When `make up` becomes a nuisance |
| CP13 | _Loading generated patients into a database._ The generator emits records; there are no patient tables to write them into yet                                                                                                                          | CP29                              |

Clinical content authoring (implementation plan §17.4) can start at any time and does not
wait for software. It is the most common cause of slip in projects of this shape.
