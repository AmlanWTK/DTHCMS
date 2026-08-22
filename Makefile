.DEFAULT_GOAL := help
SHELL := /bin/bash

.PHONY: help bootstrap up down reset status logs psql redis verify fmt format lint test custody clean

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
	cd backend && go test -race ./...
	pnpm run test

custody: ## Verify the ratified blueprint has not been altered
	python3 scripts/check_custody.py

clean: ## Remove build output and caches
	rm -rf node_modules web/node_modules mobile/node_modules backend/bin coverage
