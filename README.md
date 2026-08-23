# DTHCMS — Digital Diabetes, Thyroid & Hormone Clinic Management System

Clinical operating system for DTHC (Diabetic & Thyroid Health Care), Faridpur.

**Status: CP07 complete — the backend runs against a real schema, and can be watched while it does. No clinical functionality exists yet.**

|                       |                                                                                                                                                                  |
| --------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Specification         | [`docs/blueprint-v2.0.md`](docs/blueprint-v2.0.md) — the single authoritative source                                                                             |
| Delivery plan         | [`docs/implementation-plan.md`](docs/implementation-plan.md) — 160 checkpoints, CP01–CP160                                                                       |
| Document custody      | [`docs/CUSTODY.md`](docs/CUSTODY.md) — SHA-256 fingerprints of the ratified blueprint                                                                            |
| Clinical authority    | Dr. K. M. Nahid Ul Haque — every clinical decision, rule and content item is his                                                                                 |
| Engineering standards | [`docs/engineering-standards.md`](docs/engineering-standards.md), [`docs/architecture-boundaries.md`](docs/architecture-boundaries.md), [`docs/adr/`](docs/adr/) |
| Database conventions  | [`docs/database.md`](docs/database.md) — schemas, grants, migration rules                                                                                        |
| Observability         | [`docs/observability.md`](docs/observability.md) — dashboards, alerts, on-call notes                                                                             |
| Definition of Done    | [`docs/definition-of-done.md`](docs/definition-of-done.md)                                                                                                       |

---

## How this project is built

One checkpoint at a time. A checkpoint is implemented, tested, demonstrated, reviewed by
Dr. Nahid, and explicitly approved before the next one starts. No checkpoint is implemented
early, and no architectural decision is made silently — unresolved questions are raised as
**open decisions** in the implementation plan rather than guessed at.

```
PLAN → IMPLEMENT CPnn → TEST → REVIEW → FIX → APPROVE → IMPLEMENT CPnn+1 → …
```

## Repository layout

```
dthcms/
├── docs/               blueprint, implementation plan, decision records, runbooks
├── api/                OpenAPI 3.1 contract (source of truth for all clients)   [CP12]
├── backend/            Go modular monolith: api · worker · realtime · migrate
├── web/                Next.js — physician, QA, admin, pharmacy, research        [CP10]
├── mobile/             React Native — the 12 clinical stations                   [CP11]
├── field/              React Native — community outreach tablets                 [CP144]
├── ml/                 Python — OCR, preprocessing, predictive models            [CP99]
├── packages/
│   ├── design-tokens/  one token source → web, mobile, print                     [CP09]
│   ├── api-client/     generated TypeScript client                               [CP12]
│   ├── shared-schemas/ Zod schemas shared by web and mobile                       [CP12]
│   └── clinical-calc/  BMI · BMR · eGFR · percentiles (Go ↔ TS parity-tested)    [CP43]
├── scripts/            bootstrap, verification, repository setup
└── .github/            CI, PR template, code owners, dependency updates
```

Bracketed checkpoints indicate where each area is actually built. Directories that exist now
contain a README explaining their purpose and nothing else — deliberately.

## Prerequisites

| Tool    | Version | Notes                                                        |
| ------- | ------- | ------------------------------------------------------------ |
| Git     | 2.40+   | Git for Windows includes Git Bash, which the hooks require   |
| Go      | 1.23+   | https://go.dev/dl/                                           |
| Node.js | 22 LTS  | https://nodejs.org — see `.nvmrc`                            |
| pnpm    | 10+     | `corepack enable && corepack prepare pnpm@latest --activate` |

Docker Desktop (WSL 2 backend on Windows) is required from CP04 onward — it runs the local Postgres, Redis, object storage and mock AI service.

## Getting started

```powershell
# Windows (PowerShell), from the repository root
.\scripts\bootstrap.ps1     # installs workspace dependencies and checks your toolchain
.\scripts\verify.ps1        # runs everything CI runs: format, lint, build, test
```

```bash
# macOS / Linux
make bootstrap
make up
make migrate       # applies the schema and creates the restricted local database roles
make verify
```

`make migrate` (or `.\scripts\dev.ps1 migrate`) is required before the API can connect:
its default connection is a role that may append to the event ledger and may not modify
it, and that role is created by the migration step. See [`docs/database.md`](docs/database.md).

Full detail, including how to force AI failure scenarios: [`docs/local-development.md`](docs/local-development.md).

Both paths run the same checks. If `verify` passes locally, CI should pass too.

## What exists so far

**CP01 — repository and CI skeleton**

- The monorepo skeleton above, with workspace tooling for Go and TypeScript
- Formatting, linting and commit-message conventions, enforced by git hooks and CI
- A CI pipeline that fails on formatting, lint, build, test or secret-scan failure
- Secret scanning, both locally (pre-commit) and in CI (authoritative)
- The blueprint and implementation plan under version control, with SHA-256 custody records
- One trivial test per workspace, proving the test runners actually run

**CP02 — architecture guardrails and standards**

- Seven architecture decision records covering the decisions that shape everything else
- `docs/engineering-standards.md` and `docs/architecture-boundaries.md`
- `docs/definition-of-done.md`, referenced by the pull request template
- **`backend/tools/dthclint`** — two checks that fail the build:
  - `arch` — module dependency boundaries, declared in `backend/architecture.json`
  - `phi` — patient identifiers or credentials used as logging keys
- Feature-boundary rules for TypeScript in the ESLint configuration

**CP04 — local development environment**

- `docker compose` stack: Postgres 16 with the production extension set, Redis 7, MinIO
  standing in for Cloud Storage, and mail capture
- **`backend/tools/mockai`** — a deterministic local stand-in for Gemini and the OCR
  service, so development never calls a real model (ADR-0007), with forced failure
  scenarios for timeout, rate-limit, malformed-response and refusal handling
- One command to bring it all up, on Windows or Unix, and one to erase it and start again

**CP05 — Go backend skeleton and platform layer**

- Four binaries sharing one bootstrap: `api`, `worker`, `realtime`, `migrate`
- Typed configuration that **refuses to start when it is wrong**, reporting every problem
  at once — including production rules that make the free AI tier, a plaintext database
  connection or a leftover development password impossible to deploy
- Structured logging that **cannot carry patient identity**: the redaction handler shares
  its key list with `dthclint`, so the same rule is enforced at build time and at run time
- One error model: stable machine code, bilingual user message, internal detail logged
  and never returned
- PostgreSQL and Redis pools that are verified at start-up, `/healthz`, `/readyz` with
  per-dependency status, `/version`, and graceful shutdown that drains in-flight requests
- The middleware chain in its final order, with authentication, device verification,
  authorisation and rate limiting as explicit placeholders

**CP06 — database foundation and migration framework**

- Migrations in `backend/migrations/`, embedded into the binary, applied by `cmd/migrate`
  as an explicit step and never at application start-up
- Six schemas — `core`, `ledger`, `read`, `ops`, `docs`, `research` — and three roles
- **The event ledger is append-only because the application's database role has no
  `UPDATE`, `DELETE` or `TRUNCATE` on it**, and no write access to the derived read
  models at all. Not a convention: a privilege ([ADR-0008](docs/adr/0008-database-enforced-append-only-ledger.md))
- The same restriction applies on a developer's machine, so a forbidden write fails where
  it is written rather than in staging
- A SHA-256 of every migration is recorded when it is applied and checked on every later
  run, so editing an applied migration is an error rather than a silent divergence
- `core.assert_invariants()` re-checks the whole model after every run — including that
  every table carries `facility_id` or a written exemption (D-61)
- `core.facility` with DTHC Faridpur seeded under a fixed identifier
- `sqlc` generating typed query code from the migrations themselves; CI fails if the
  committed output is stale
- Twelve tests against a real PostgreSQL that connect **as the application role** and try
  to rewrite the ledger

**CP07 — observability baseline**

- OpenTelemetry tracing across HTTP, PostgreSQL and Redis: every request produces a trace,
  and every query it made is a span inside that trace
- RED metrics per endpoint, labelled by **route template** — never the raw path, which
  would create one time series per patient and carry whatever an operator typed
- `trace_id` and `span_id` on every log line written inside a span, so a log line and a
  trace are two views of one event rather than two records to correlate by timestamp
- **PHI redaction extended from logs to span attributes and metric labels**, over the same
  key list, enforced at build time by `dthclint` and at run time by a redacting span
  exporter — which also strips URL query strings and SQL literals
- One local container (`grafana/otel-lgtm`) with three dashboards and four alert rules,
  committed as JSON and installed by a script that verifies its own work
- Alert email delivered to Mailpit, so an alert firing is something you can watch happen
- Telemetry **fails open**: an unreachable collector never stops a request being served
  ([ADR-0009](docs/adr/0009-vendor-neutral-observability.md))

## What deliberately does **not** exist yet

No domain tables, no event store, no authentication, no deployment, no cloud
infrastructure, no Docker images. Those arrive at their own checkpoints, in dependency
order.

## Licence and confidentiality

Proprietary and confidential. © DTHC (Diabetic & Thyroid Health Care), Faridpur.
This repository will contain systems that handle patient health data. Treat every branch,
issue and screenshot accordingly, and never commit real patient data — not even for testing.
Synthetic data arrives at CP13 for exactly that reason.
