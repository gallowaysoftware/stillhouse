# Stillhouse

Open-source distillery management for Canadian craft distillers.

Stillhouse helps a CRA-licensed spirits producer plan recipes, track
production from grain through bottle, manage province-coded excise stamps, and
generate the values that go on CRA Form B266 each month — all in one ledger that
keeps the operational reality and the compliance reality in sync.

**Status:** pre-v1, in active development. The v1 goal is to file one real B266
from Stillhouse for a production month.

## Architecture

- **Backend** — Go ([backend/](backend/)). Single binary that serves both a
  ConnectRPC API and the embedded web frontend.
- **Frontend** — React + TypeScript + Vite + Tailwind ([web/](web/)). Talks
  to the backend over ConnectRPC.
- **Schemas** — Protocol Buffers ([proto/](proto/)), compiled with Buf into
  typed Go server stubs and typed TS clients.
- **Database** — PostgreSQL with row-level security for multi-tenant isolation.
  One tenant = one CRA spirits licence.
- **License** — AGPL-3.0. Free to self-host. Managed hosting will be a paid
  offering once the project is ready.

## Repo layout

```
proto/                Protocol Buffer definitions
backend/              Go server
  cmd/server/         binary entrypoint
  internal/auth/      password hashing + sessions
  internal/config/    env config
  internal/db/        pgx pool, migrations, sqlc-generated queries
  internal/genpb/     generated Go from .proto
  internal/rpc/       ConnectRPC service implementations
  internal/server/    HTTP server + middleware + static asset embedding
web/                  React frontend
  src/gen/            generated TS Connect client from .proto
deploy/               Dockerfile + dev compose
.github/workflows/    CI
```

## First-time setup

```sh
# Install required tools (Go binaries: buf, sqlc, golang-migrate, protoc plugins)
make tools

# Start a local Postgres in podman/docker
make dev-up

# Apply migrations
make migrate-up

# Seed a test tenant + admin user
make seed

# In two terminals:
make backend-dev      # Go server on :8080
make web-dev          # Vite dev server on :5173 (proxies API to :8080)
```

Then open http://localhost:5173 and log in with the seeded credentials printed by `make seed`.

## License

Stillhouse is licensed under the [GNU Affero General Public License v3.0](LICENSE).
