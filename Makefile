SHELL := /usr/bin/env bash
.ONESHELL:
.DEFAULT_GOAL := help

# ----- Config -----------------------------------------------------------------

CONTAINER       ?= $(shell command -v podman 2>/dev/null || echo docker)
COMPOSE         ?= $(CONTAINER) compose -f deploy/compose.yaml

# ----- Image build / push -----------------------------------------------------
# Override REGISTRY / IMAGE_NAME to push elsewhere. Tag defaults to the short
# git SHA so every push is content-addressable; :latest is updated alongside.
REGISTRY        ?= registry.home.thegalloways.ca
IMAGE_NAME      ?= stillhouse
IMAGE_TAG       ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
IMAGE           := $(REGISTRY)/$(IMAGE_NAME)

# Two DSNs:
#   PG_ADMIN_DSN — superuser; runs migrations + seed. Bypasses RLS.
#   PG_APP_DSN   — non-superuser; the Go server connects with this so the
#                  tenant-isolation RLS policies actually enforce.
PG_ADMIN_DSN    ?= postgres://stillhouse:stillhouse@localhost:5432/stillhouse?sslmode=disable
PG_APP_DSN      ?= postgres://stillhouse_app:stillhouse_app@localhost:5432/stillhouse?sslmode=disable
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

migrate-up: ## Apply all pending migrations (runs as superuser).
	migrate -path $(MIGRATIONS_DIR) -database "$(PG_ADMIN_DSN)" up

migrate-down: ## Roll back the most recent migration.
	migrate -path $(MIGRATIONS_DIR) -database "$(PG_ADMIN_DSN)" down 1

migrate-new: ## Create a new migration. Usage: make migrate-new NAME=add_thing
	@test -n "$(NAME)" || (echo "NAME is required (e.g. make migrate-new NAME=add_thing)"; exit 1)
	migrate create -ext sql -dir $(MIGRATIONS_DIR) -seq $(NAME)

migrate-force: ## Force migration version (recovery). Usage: make migrate-force V=1
	@test -n "$(V)" || (echo "V is required"; exit 1)
	migrate -path $(MIGRATIONS_DIR) -database "$(PG_ADMIN_DSN)" force $(V)

# ----- Seed -------------------------------------------------------------------

seed: ## Seed a test tenant + admin user. Prints generated credentials.
	cd backend && DATABASE_URL="$(PG_ADMIN_DSN)" go run ./cmd/seed

mcp-token: ## Issue an MCP personal access token. Usage: make mcp-token EMAIL=foo@bar.com [NAME="phone"]
	@test -n "$(EMAIL)" || (echo "EMAIL is required (e.g. make mcp-token EMAIL=admin@example.com NAME=phone)"; exit 1)
	cd backend && ADMIN_DATABASE_URL="$(PG_ADMIN_DSN)" \
	    go run ./cmd/mcp-token --email "$(EMAIL)" --name "$(or $(NAME),mcp)"

# ----- Run --------------------------------------------------------------------

backend-dev: ## Run the Go backend (uses PG_APP_DSN so RLS enforces).
	cd backend && STILLHOUSE_DEV=1 DATABASE_URL="$(PG_APP_DSN)" go run ./cmd/server

web-dev: ## Run the Vite dev server.
	cd web && npm run dev

# ----- Build / test / lint ----------------------------------------------------

build: build-web build-backend ## Build everything for production.

build-web: ## Build the web frontend (output: web/dist).
	cd web && npm ci && npm run build

build-backend: ## Build the Go server (output: backend/bin/server).
	cd backend && go build -o bin/server ./cmd/server

test: ## Run all unit tests.
	(cd backend && go test ./...)
	(cd web && npm test --if-present)

test-integration: ## Run integration tests against the local Postgres (requires dev-up).
	cd backend && \
	    STILLHOUSE_INTEGRATION_TEST_DSN="$(PG_APP_DSN)" \
	    STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN="$(PG_ADMIN_DSN)" \
	    go test -v -tags integration ./...

lint: ## Run linters. Mirrors what CI enforces.
	@unformatted="$$(gofmt -l backend qa)"; \
	if [ -n "$$unformatted" ]; then \
	    echo "not gofmt-clean (run 'make fmt'):"; echo "$$unformatted"; exit 1; \
	fi
	(cd backend && go vet ./...)
	(cd backend && golangci-lint run ./...)
	(cd proto && buf lint)
	(cd web && npm run lint --if-present)

# gofmt rather than `go fmt`: the latter resolves packages, and the qa
# module replaces an out-of-tree sibling checkout that may not be present.
fmt: ## Format code.
	gofmt -w backend qa
	(cd proto && buf format -w)

# ----- Container image --------------------------------------------------------

image: ## Build the production container image. Tags :latest and :$(IMAGE_TAG).
	$(CONTAINER) build \
	    -f deploy/Dockerfile \
	    -t $(IMAGE):$(IMAGE_TAG) \
	    -t $(IMAGE):latest \
	    .

push: ## Push the production image to $(REGISTRY). Pushes both :latest and :$(IMAGE_TAG).
	$(CONTAINER) push $(IMAGE):$(IMAGE_TAG)
	$(CONTAINER) push $(IMAGE):latest

image-push: image push ## Build and push in one step.

image-info: ## Print the image coordinates that would be built / pushed.
	@echo "REGISTRY  = $(REGISTRY)"
	@echo "IMAGE     = $(IMAGE)"
	@echo "IMAGE_TAG = $(IMAGE_TAG)"

.PHONY: help tools generate buf-generate sqlc-generate dev-up dev-down dev-logs \
	migrate-up migrate-down migrate-new migrate-force seed mcp-token backend-dev web-dev \
	build build-web build-backend test lint fmt \
	image push image-push image-info
