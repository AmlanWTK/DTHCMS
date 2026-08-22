# DTHCMS — Digital Diabetes, Thyroid & Hormone Clinic Management System

Clinical operating system for DTHC (Diabetic & Thyroid Health Care), Faridpur.

**Status: CP01 complete — foundation only. No clinical functionality exists yet.**

| | |
|---|---|
| Specification | [`docs/blueprint-v2.0.md`](docs/blueprint-v2.0.md) — the single authoritative source |
| Delivery plan | [`docs/implementation-plan.md`](docs/implementation-plan.md) — 160 checkpoints, CP01–CP160 |
| Document custody | [`docs/CUSTODY.md`](docs/CUSTODY.md) — SHA-256 fingerprints of the ratified blueprint |
| Clinical authority | Dr. K. M. Nahid Ul Haque — every clinical decision, rule and content item is his |

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

| Tool | Version | Notes |
|---|---|---|
| Git | 2.40+ | Git for Windows includes Git Bash, which the hooks require |
| Go | 1.23+ | https://go.dev/dl/ |
| Node.js | 22 LTS | https://nodejs.org — see `.nvmrc` |
| pnpm | 10+ | `corepack enable && corepack prepare pnpm@latest --activate` |

Docker Desktop is **not** required until CP04.

## Getting started

```powershell
# Windows (PowerShell), from the repository root
.\scripts\bootstrap.ps1     # installs workspace dependencies and checks your toolchain
.\scripts\verify.ps1        # runs everything CI runs: format, lint, build, test
```

```bash
# macOS / Linux
make bootstrap
make verify
```

Both paths run the same checks. If `verify` passes locally, CI should pass too.

## What CP01 delivers

- The monorepo skeleton above, with workspace tooling for Go and TypeScript
- Formatting, linting and commit-message conventions, enforced by git hooks and CI
- A CI pipeline that fails on formatting, lint, build, test or secret-scan failure
- Secret scanning, both locally (pre-commit) and in CI (authoritative)
- The blueprint and implementation plan under version control, with SHA-256 custody records
- One trivial test per workspace, proving the test runners actually run

## What CP01 deliberately does **not** deliver

No application code, no database, no authentication, no deployment, no cloud infrastructure,
no Docker images. Those arrive at their own checkpoints, in dependency order.

## Licence and confidentiality

Proprietary and confidential. © DTHC (Diabetic & Thyroid Health Care), Faridpur.
This repository will contain systems that handle patient health data. Treat every branch,
issue and screenshot accordingly, and never commit real patient data — not even for testing.
Synthetic data arrives at CP13 for exactly that reason.
