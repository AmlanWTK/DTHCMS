<#
.SYNOPSIS
  Write the build and CI files that cannot be transferred by the remote file bridge.
.DESCRIPTION
  The desktop bridge refuses to write Makefiles and anything under .github/ — a sensible
  guard, since those files control what runs on your machine and in CI. They are therefore
  embedded here as literal text and written locally by you.

  Safe to re-run. Existing files are overwritten only with -Force.
.EXAMPLE
  .\scripts\install-ci-files.ps1
  .\scripts\install-ci-files.ps1 -Force
#>
param([switch]$Force)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$utf8NoBom = New-Object System.Text.UTF8Encoding($false)
$written = 0
$skipped = 0

function Write-RepoFile {
    param([string]$RelativePath, [string]$Content)

    $full = Join-Path $repoRoot $RelativePath
    $dir = Split-Path -Parent $full
    if (-not (Test-Path $dir)) { New-Item -ItemType Directory -Path $dir -Force | Out-Null }

    if ((Test-Path $full) -and -not $Force) {
        Write-Host ("  skip   $RelativePath (already exists; use -Force to overwrite)") -ForegroundColor Yellow
        $script:skipped++
        return
    }

    # Normalise to LF. Makefile recipes and shell scripts in CI break on CRLF.
    $normalised = $Content -replace "`r`n", "`n"

    # A PowerShell here-string discards the newline immediately before its terminator,
    # so restore it. Prettier, POSIX and every diff tool expect a trailing newline.
    if (-not $normalised.EndsWith("`n")) { $normalised += "`n" }
    [System.IO.File]::WriteAllText($full, $normalised, $utf8NoBom)
    Write-Host ("  write  $RelativePath") -ForegroundColor Green
    $script:written++
}

Write-Host ''
Write-Host 'Installing build and CI files' -ForegroundColor Cyan
Write-Host '-----------------------------'

$content_Makefile = @'
.DEFAULT_GOAL := help
SHELL := /bin/bash

.PHONY: help bootstrap up down reset status logs psql redis verify fmt format lint test custody clean \
	migrate migrate-status migrate-verify migrate-down sqlc sqlc-check

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

up: ## Start the local stack and wait until healthy
	docker compose up -d --wait
	docker compose --profile init run --rm -T minio-init
	@echo ''
	@echo 'Postgres      127.0.0.1:$${POSTGRES_PORT:-5433}  (dthcms/dthcms_local_only, db dthcms)'
	@echo 'Redis         127.0.0.1:$${REDIS_PORT:-6380}'
	@echo 'MinIO console http://localhost:$${MINIO_CONSOLE_PORT:-9001}'
	@echo 'Mock AI + OCR http://localhost:$${MOCKAI_PORT:-8090}/healthz'
	@echo 'Mailpit       http://localhost:$${MAILPIT_UI_PORT:-8025}'
	@echo ''
	@echo 'Next: make migrate   (applies the schema and creates the restricted local roles)'

down: ## Stop the local stack, keeping data
	docker compose down

reset: ## Stop the stack, ERASE all local data, and start fresh
	docker compose down -v
	docker compose up -d --wait
	docker compose --profile init run --rm -T minio-init

status: ## Show what is running
	docker compose ps

logs: ## Follow logs (make logs SERVICE=postgres)
	docker compose logs -f $(SERVICE)

psql: ## Open a psql shell on the local database
	docker compose exec postgres psql -U dthcms -d dthcms

redis: ## Open a redis-cli shell
	docker compose exec redis redis-cli

migrate: ## Apply pending migrations, create local roles, then verify invariants
	cd backend && go run ./cmd/migrate up
	cd backend && go run ./cmd/migrate dev-roles

migrate-status: ## Show which migrations have been applied
	cd backend && go run ./cmd/migrate status

migrate-verify: ## Check migration checksums and database invariants; change nothing
	cd backend && go run ./cmd/migrate verify

migrate-down: ## Roll back one migration (refused in production)
	cd backend && go run ./cmd/migrate down

sqlc: ## Regenerate database code from the migrations and query files
	cd backend && sqlc generate

sqlc-check: ## Fail if the committed generated code is stale (what CI runs)
	cd backend && sqlc diff

bootstrap: ## Install workspace dependencies
	corepack enable
	pnpm install
	cd backend && go mod download

verify: fmt lint test custody ## Everything CI runs

fmt: ## Check formatting (does not modify files)
	pnpm run format:check
	@cd backend && unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then echo "Not gofmt-formatted:"; echo "$$unformatted"; exit 1; fi

format: ## Fix formatting
	pnpm run format
	cd backend && gofmt -w .

lint: ## Run linters
	pnpm run lint
	cd backend && go vet ./...
	cd backend && go run ./tools/dthclint all

test: ## Run all tests
	@cd backend && DTHCMS_TEST_POSTGRES_URL=$${DTHCMS_TEST_POSTGRES_URL:-postgres://dthcms:dthcms_local_only@127.0.0.1:$${POSTGRES_PORT:-5433}/postgres?sslmode=disable} \
		go test -race ./...
	pnpm run test

custody: ## Verify the ratified blueprint has not been altered
	python3 scripts/check_custody.py

clean: ## Remove build output and caches
	rm -rf node_modules web/node_modules mobile/node_modules backend/bin coverage
'@

Write-RepoFile -RelativePath 'Makefile' -Content $content_Makefile

$content__github_workflows_ci_yml = @'
name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

permissions:
  contents: read

concurrency:
  group: ${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: true

jobs:
  commitlint:
    name: Commit messages
    runs-on: ubuntu-latest
    if: github.event_name == 'pull_request'
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: actions/setup-node@v4
        with:
          node-version: 22
      - uses: pnpm/action-setup@v4
      - run: pnpm install --frozen-lockfile
      - name: Validate commit messages
        run: >
          pnpm exec commitlint
          --from ${{ github.event.pull_request.base.sha }}
          --to ${{ github.event.pull_request.head.sha }}
          --verbose

  secrets:
    name: Secret scan
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - name: gitleaks
        uses: gitleaks/gitleaks-action@v2
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}

  backend:
    name: Backend (Go)
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: backend
    # A real PostgreSQL, because the guarantees CP06 makes about the event ledger are
    # properties of PostgreSQL's privilege system. A mock would only confirm that the
    # mock was written to agree with the test.
    services:
      postgres:
        image: postgres:16-alpine
        env:
          POSTGRES_USER: dthcms
          POSTGRES_PASSWORD: dthcms_local_only
          POSTGRES_DB: dthcms
        ports:
          - 5432:5432
        options: >-
          --health-cmd "pg_isready -U dthcms"
          --health-interval 5s
          --health-timeout 5s
          --health-retries 20
    env:
      DTHCMS_TEST_POSTGRES_URL: postgres://dthcms:dthcms_local_only@127.0.0.1:5432/postgres?sslmode=disable
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'
          cache-dependency-path: backend/go.sum
      - name: Verify formatting
        run: |
          unformatted=$(gofmt -l .)
          if [ -n "$unformatted" ]; then
            echo "These files are not gofmt-formatted:"
            echo "$unformatted"
            exit 1
          fi
      - name: Vet
        run: go vet ./...
      - name: Lint
        uses: golangci/golangci-lint-action@v6
        with:
          version: v1.62
          working-directory: backend
      - name: Build
        run: go build ./...
      - name: Architecture and PHI guardrails
        run: go run ./tools/dthclint all
      - name: Migrations apply to an empty database
        env:
          DTHCMS_ENV: test
          DTHCMS_POSTGRES_MIGRATION_URL: postgres://dthcms:dthcms_local_only@127.0.0.1:5432/dthcms?sslmode=disable
        run: |
          go run ./cmd/migrate up
          go run ./cmd/migrate verify
      - name: Test
        run: go test -race -coverprofile=coverage.out ./...

  sqlc:
    name: Generated database code
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: sqlc-dev/setup-sqlc@v4
        with:
          sqlc-version: '1.27.0'
      - name: Generated code is current
        working-directory: backend
        run: |
          if ! sqlc diff; then
            echo ''
            echo 'The committed sqlc output does not match the schema and queries.'
            echo 'Run `make sqlc` and commit the result.'
            exit 1
          fi

  frontend:
    name: Web, mobile and packages (TypeScript)
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: pnpm/action-setup@v4
      - uses: actions/setup-node@v4
        with:
          node-version: 22
          cache: pnpm
      - run: pnpm install --frozen-lockfile
      - name: Format check
        run: pnpm run format:check
      - name: Lint
        run: pnpm run lint
      - name: Typecheck
        run: pnpm run typecheck
      - name: Test
        run: pnpm run test

  custody:
    name: Blueprint custody
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-python@v5
        with:
          python-version: '3.12'
      - name: Verify the ratified blueprint has not been altered
        run: python scripts/check_custody.py
'@

Write-RepoFile -RelativePath '.github\workflows\ci.yml' -Content $content__github_workflows_ci_yml

$content__github_CODEOWNERS = @'
# Every change requires review. Ownership is deliberately broad while the team is small.
*                       @AmlanWTK

# Clinical content and specification changes need Dr. Nahid's review before merge.
# Replace with a GitHub team once one exists (e.g. @dthc/clinical).
/docs/blueprint-v2.0.md @AmlanWTK
/docs/CUSTODY.md        @AmlanWTK
'@

Write-RepoFile -RelativePath '.github\CODEOWNERS' -Content $content__github_CODEOWNERS

$content__github_dependabot_yml = @'
version: 2
updates:
  - package-ecosystem: gomod
    directory: /backend
    schedule:
      interval: weekly
    open-pull-requests-limit: 5
    commit-message:
      prefix: 'chore(deps)'

  - package-ecosystem: npm
    directory: /
    schedule:
      interval: weekly
    open-pull-requests-limit: 5
    commit-message:
      prefix: 'chore(deps)'
    groups:
      dev-dependencies:
        dependency-type: development

  - package-ecosystem: github-actions
    directory: /
    schedule:
      interval: monthly
    commit-message:
      prefix: 'chore(ci)'
'@

Write-RepoFile -RelativePath '.github\dependabot.yml' -Content $content__github_dependabot_yml

$content__github_pull_request_template_md = @'
## Checkpoint

<!-- e.g. CP01 — Repository, Monorepo Scaffolding & CI Skeleton -->

**CP:**
**What this delivers:**

## Scope discipline

- [ ] Everything in the checkpoint's SCOPE is implemented
- [ ] Nothing in its OUT OF SCOPE list is implemented
- [ ] Any new ambiguity found during implementation is raised as an open decision, not guessed

## Definition of Done

<!-- The full list, with the per-type additions: docs/definition-of-done.md -->

**Implementation**

- [ ] Follows the project coding standards; all linters pass
- [ ] No `TODO`s or commented-out code left behind (deferred work is a tracked issue)

**Testing**

- [ ] Unit tests cover this change, including failure paths
- [ ] Integration tests cover data and service interactions where applicable
- [ ] All tests pass in CI — not "pass locally", not "pass except one flaky test"

**Verification**

- [ ] The checkpoint's MANUAL VERIFICATION procedure has been performed, and the result recorded below
- [ ] Every ACCEPTANCE CRITERION is objectively satisfied
- [ ] Clinical behaviour (if any) has been verified by Dr. Nahid

**Security and data**

- [ ] No secrets in code, config, logs or fixtures
- [ ] No patient data of any kind — synthetic only
- [ ] New endpoints declare and enforce permissions
- [ ] Migrations included, reversible where feasible, and tested

**Interface**

- [ ] Loading, empty, error and offline states implemented
- [ ] Renders correctly in Bangla and English
- [ ] Clinical values use the attribution and dual-unit components

**Documentation**

- [ ] Architecture docs / ADRs updated if a decision was made
- [ ] The open-decision register is updated — decisions recorded, new ambiguities added

## Manual verification performed

<!-- What you actually did, and what you observed. Screenshots welcome. -->

## Open decisions raised or resolved

<!-- D-nn references, or "none" -->
'@

Write-RepoFile -RelativePath '.github\pull_request_template.md' -Content $content__github_pull_request_template_md

Write-Host ''
Write-Host ("Done: $written written, $skipped skipped.") -ForegroundColor Cyan
if ($skipped -gt 0) {
    Write-Host 'Re-run with -Force to overwrite the skipped files.'
}
Write-Host ''
