.DEFAULT_GOAL := help
SHELL := /bin/bash

.PHONY: help bootstrap up down reset status logs psql redis verify fmt format lint test custody clean \
	migrate migrate-status migrate-verify migrate-down sqlc sqlc-check observability \
	spec spec-check spec-docs synth synth-summary synth-review \
	project project-status project-rebuild

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

up: ## Start the local stack and wait until healthy
	docker compose up -d --wait
	docker compose --profile init run --rm -T minio-init
	docker compose --profile init run --rm -T grafana-init
	@echo ''
	@echo 'Postgres      127.0.0.1:$${POSTGRES_PORT:-5433}  (dthcms/dthcms_local_only, db dthcms)'
	@echo 'Redis         127.0.0.1:$${REDIS_PORT:-6380}'
	@echo 'MinIO console http://localhost:$${MINIO_CONSOLE_PORT:-9001}'
	@echo 'Mock AI + OCR http://localhost:$${MOCKAI_PORT:-8090}/healthz'
	@echo 'Grafana       http://localhost:$${GRAFANA_PORT:-3001}  (DTHCMS folder)'
	@echo 'Mailpit       http://localhost:$${MAILPIT_UI_PORT:-8025}'
	@echo ''
	@echo 'Next: make migrate   (applies the schema and creates the restricted local roles)'

down: ## Stop the local stack, keeping data
	docker compose down

reset: ## Stop the stack, ERASE all local data, and start fresh
	docker compose down -v
	docker compose up -d --wait
	docker compose --profile init run --rm -T minio-init
	docker compose --profile init run --rm -T grafana-init

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

project: ## Follow the ledger and keep the read models up to date (Ctrl-C to stop)
	cd backend && go run ./cmd/projector run

project-status: ## Show each projection's checkpoint, lag and health
	cd backend && go run ./cmd/projector status

project-rebuild: ## Rebuild read models from event one. REASON is required; NAME optional
	@test -n "$${REASON}" || (echo "REASON is required: make project-rebuild REASON='why'" && exit 2)
	cd backend && go run ./cmd/projector -reason "$${REASON}" -operator "$${OPERATOR:-$$USER}" rebuild $${NAME}

observability: ## Re-provision dashboards and alert rules, and verify them
	docker compose --profile init run --rm -T grafana-init

# Run in the official container rather than from a locally installed binary. `go install`
# of sqlc v1.27.0 produces a binary that faults on start-up under Go 1.25: it embeds the
# Postgres parser as WebAssembly, and the wazero runtime vendored with that release predates
# the toolchain. The image carries a binary built with one that works, at the version CI
# installs — which matters, because a local sqlc a version ahead rewrites every generated
# file's header and makes `sqlc diff` in CI unreadable.
sqlc: ## Regenerate database code from the migrations and query files
	docker run --rm -v "$(CURDIR)/backend:/src" -w /src sqlc/sqlc:1.27.0 generate

sqlc-check: ## Fail if the committed generated code is stale (what CI runs)
	cd backend && sqlc diff

spec: ## Lint the API contract and regenerate the TypeScript client
	pnpm run spec:lint
	pnpm run api:generate

spec-check: ## Fail if the contract does not lint or the committed client is stale (what CI runs)
	pnpm run spec:lint
	pnpm run api:generate
	@if ! git diff --exit-code --stat -- packages/api-client/src/schema.ts; then \
		echo ""; \
		echo "The committed client does not match api/openapi.yaml."; \
		echo "Run \`make spec\` and commit the result."; \
		exit 1; \
	fi

spec-docs: ## Build the self-contained API documentation page at api/docs.html
	pnpm run spec:docs

bootstrap: ## Install workspace dependencies
	corepack enable
	pnpm install
	cd backend && go mod download

verify: fmt lint spec-check test custody ## Everything CI runs

fmt: ## Check formatting (does not modify files)
	pnpm run format:check
	@# go list ./... skips vendor/, so vendored third-party code is never formatted or checked.
	@cd backend && unformatted=$$(gofmt -l $$(go list -f '{{.Dir}}' ./...)); \
	if [ -n "$$unformatted" ]; then echo "Not gofmt-formatted:"; echo "$$unformatted"; exit 1; fi

format: ## Fix formatting
	pnpm run format
	cd backend && gofmt -w $$(go list -f '{{.Dir}}' ./...)

lint: ## Run linters
	pnpm run lint
	cd backend && go vet ./...
	cd backend && go run ./tools/dthclint all

test: ## Run all tests, with coverage floors enforced
	@cd backend && DTHCMS_TEST_POSTGRES_URL=$${DTHCMS_TEST_POSTGRES_URL:-postgres://dthcms:dthcms_local_only@127.0.0.1:$${POSTGRES_PORT:-5433}/postgres?sslmode=disable} \
		DTHCMS_TEST_REDIS_URL=$${DTHCMS_TEST_REDIS_URL:-redis://127.0.0.1:$${REDIS_PORT:-6380}} \
		go test -race ./...
	pnpm run test:coverage

synth: ## Generate a synthetic cohort as NDJSON (make synth N=5000 SEED=42 OUT=cohort.ndjson)
	cd backend && go run ./cmd/synthgen -n $${N:-1000} -seed $${SEED:-1} -out ../$${OUT:-cohort.ndjson}

synth-summary: ## Print the generated distributions beside the clinician's profile
	@cd backend && go run ./cmd/synthgen -n $${N:-20000} -seed $${SEED:-1} -summary

synth-review: ## Build the page a clinician reads to sign off the generator (CP13)
	cd backend && go run ./cmd/synthgen -review -with-cases \
		-n $${N:-30} -seed $${SEED:-7} -out ../$${OUT:-synthetic-review.html}

custody: ## Verify the ratified blueprint has not been altered
	python3 scripts/check_custody.py

clean: ## Remove build output and caches
	rm -rf node_modules web/node_modules mobile/node_modules backend/bin coverage
