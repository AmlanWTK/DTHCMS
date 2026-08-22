.DEFAULT_GOAL := help
SHELL := /bin/bash

.PHONY: help bootstrap verify fmt lint test backend-verify web-verify custody clean

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

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

test: ## Run all tests
	cd backend && go test -race ./...
	pnpm run test

custody: ## Verify the ratified blueprint has not been altered
	python3 scripts/check_custody.py

clean: ## Remove build output and caches
	rm -rf node_modules web/node_modules mobile/node_modules backend/bin coverage
