SHELL := /usr/bin/env bash
.ONESHELL:
.DEFAULT_GOAL := help

# ----- Config -----------------------------------------------------------------

CONTAINER       ?= $(shell command -v podman 2>/dev/null || echo docker)
COMPOSE         ?= $(CONTAINER) compose -f deploy/compose.yaml
PG_DSN          ?= postgres://stillhouse:stillhouse@localhost:5432/stillhouse?sslmode=disable
MIGRATIONS_DIR  := backend/internal/db/migrations

# ----- Help -------------------------------------------------------------------

help: ## Show this help.
	@awk 'BEGIN {FS = ":.*##"; printf "Usage: make \033[36m<target>\033[0m\n\nTargets:\n"} \
		/^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

# ----- One-time setup ---------------------------------------------------------

tools: ## Install required Go-based dev tools (buf, sqlc, migrate, protoc plugins).
	go install github.com/bufbuild/buf/cmd/buf@latest
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
	go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install connectrpc.com/connect/cmd/protoc-gen-connect-go@latest

# ----- Code generation --------------------------------------------------------

generate: buf-generate-go buf-generate-web sqlc-generate ## Run all code generators.

buf-generate-go: ## Generate Go code from .proto definitions.
	cd proto && buf generate --template buf.gen.yaml

buf-generate-web: ## Generate TS code from .proto definitions. Requires `cd web && npm install` first.
	cd proto && buf generate --template buf.gen.web.yaml

sqlc-generate: ## Generate type-safe Go query code from SQL.
	cd backend && sqlc generate

# ----- Dev environment --------------------------------------------------------

dev-up: ## Start local dev Postgres.
	$(COMPOSE) up -d postgres

dev-down: ## Stop local dev Postgres.
	$(COMPOSE) down

dev-logs: ## Tail dev Postgres logs.
	$(COMPOSE) logs -f postgres

# ----- Migrations -------------------------------------------------------------

migrate-up: ## Apply all pending migrations.
	migrate -path $(MIGRATIONS_DIR) -database "$(PG_DSN)" up

migrate-down: ## Roll back the most recent migration.
	migrate -path $(MIGRATIONS_DIR) -database "$(PG_DSN)" down 1

migrate-new: ## Create a new migration. Usage: make migrate-new NAME=add_thing
	@test -n "$(NAME)" || (echo "NAME is required (e.g. make migrate-new NAME=add_thing)"; exit 1)
	migrate create -ext sql -dir $(MIGRATIONS_DIR) -seq $(NAME)

migrate-force: ## Force migration version (recovery). Usage: make migrate-force V=1
	@test -n "$(V)" || (echo "V is required"; exit 1)
	migrate -path $(MIGRATIONS_DIR) -database "$(PG_DSN)" force $(V)

# ----- Seed -------------------------------------------------------------------

seed: ## Seed a test tenant + admin user. Prints generated credentials.
	cd backend && go run ./cmd/seed

# ----- Run --------------------------------------------------------------------

backend-dev: ## Run the Go backend with live reload (uses STILLHOUSE_DEV=1).
	cd backend && STILLHOUSE_DEV=1 DATABASE_URL="$(PG_DSN)" go run ./cmd/server

web-dev: ## Run the Vite dev server.
	cd web && npm run dev

# ----- Build / test / lint ----------------------------------------------------

build: build-web build-backend ## Build everything for production.

build-web: ## Build the web frontend (output: web/dist).
	cd web && npm ci && npm run build

build-backend: ## Build the Go server (output: backend/bin/server).
	cd backend && go build -o bin/server ./cmd/server

test: ## Run all tests.
	cd backend && go test ./...
	cd web && npm test --if-present

lint: ## Run linters.
	cd backend && go vet ./...
	cd proto && buf lint
	cd web && npm run lint --if-present

fmt: ## Format code.
	cd backend && go fmt ./...
	cd proto && buf format -w

.PHONY: help tools generate buf-generate sqlc-generate dev-up dev-down dev-logs \
	migrate-up migrate-down migrate-new migrate-force seed backend-dev web-dev \
	build build-web build-backend test lint fmt
