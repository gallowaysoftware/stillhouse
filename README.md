# Stillhouse

Open-source distillery management for Canadian craft distillers.

Stillhouse helps a CRA-licensed spirits producer plan recipes, track
production from grain through bottle, manage province-coded excise stamps,
and generate the values that go on CRA Form B266 each month — all in one
ledger that keeps the operational reality and the compliance reality in
sync.

## What's implemented

Each stage below has its own commit with a verified end-to-end smoke test.

| Stage | Feature |
|------:|---------|
| 1 | Auth + multi-tenant foundation (one tenant = one CRA spirits licence) |
| 2 | Materials + versioned recipes + projected-LAA math |
| 3 | Mash + fermentation operational capture |
| 4 | Distillation + production gauge → bulk alcohol ledger (the bridge) |
| 5 | Barrels + maturation clock + Canadian Whisky eligibility |
| 6 | Products + bottling + province-coded excise stamp lifecycle |
| 7 | Packaging removals + CRA Form B266 generation |
| 8 | Audit log for production gauge / bottling / removal / B266 submit |
| 9 | Live dashboard with LAA + duty rollups |
| 10 | Non-superuser app role so RLS actually enforces |
| 11 | Integration test that verifies tenant isolation |
| 12 | Unit tests for the load-bearing alcohol-math functions |
| 13 | Audit log extended to barrel fill / dump / regauge |
| 105 | MCP server — operate Stillhouse from Claude (phone/desktop) over Streamable HTTP |
| 106 | API token management moves into the web UI |
| 107 | MCP polish from first-pass QA: filter barrels out of bulk lists, FK→NotFound on ferment/mash log, emit empty arrays + zero numerics, prefix Connect code in MCP errors |
| 108 | MCP polish round 2: `get_bulk_container` rejects barrel ids, regauge can't fully drain a non-empty barrel, fermentation reading keeps real `0` measurements, slim write-tool responses |
| 109 | Bottling conserves LAA — debits source by `bottleLAA` rather than physical volume × source ABV, handles implicit dilution at bottling time, rejects bottling stronger than the source |
| 110 | Gin recipe backend — botanical roles, per-version sensory scores (10 axes), NGS input + maceration + distillation method, gin-aware LAA projection |

**v1 milestone:** *file one real B266 from Stillhouse for a production
month.* Achieved at Stage 7.

## Architecture

- **Backend** — Go ([backend/](backend/)). Single binary that serves a
  ConnectRPC API and the embedded web frontend.
- **Frontend** — React + TypeScript + Vite + Tailwind ([web/](web/)).
  Talks to the backend over ConnectRPC (JSON over HTTP).
- **Schemas** — Protocol Buffers ([proto/](proto/)), compiled with Buf into
  typed Go server stubs and typed TS clients.
- **Database** — PostgreSQL with row-level security keyed off the
  per-request `app.current_tenant_id` GUC. Migrations run as a superuser;
  the application connects as a separate non-super role (`stillhouse_app`)
  so the RLS policies actually enforce. One tenant = one CRA spirits licence.
- **License** — AGPL-3.0. Free to self-host. Managed hosting will be a paid
  offering once the project is ready.

## Repo layout

```
proto/                Protocol Buffer definitions
backend/              Go server
  cmd/server/         binary entrypoint
  cmd/seed/           bootstrap a starter tenant + admin user
  internal/audit/     audit log writer
  internal/auth/      Argon2id password hashing
  internal/config/    env config
  internal/db/        pgx pool, migrations, sqlc-generated queries
  internal/distilling/ projection math (mass → sugar → ethanol → LAA)
  internal/excise/    Canadian excise duty rates + Owed() helper
  internal/genpb/     generated Go from .proto
  internal/rpc/       ConnectRPC service implementations
  internal/server/    HTTP server + session middleware + static embedding
  internal/tenantdb/  WithTenantTx — opens a tx, sets the tenant GUC,
                      runs your callback with a tenant-scoped Queries
web/                  React frontend
  src/gen/            generated TS Connect client from .proto
  src/pages/          per-route views (Materials, Recipes, Mashes, …,
                      Bulk, Barrels, Products, Stamps, Bottling,
                      Removals, B266 returns, Audit log)
deploy/               Dockerfile + dev compose
.github/workflows/    CI
```

## First-time setup

```sh
# Install required tools (Go binaries: buf, sqlc, golang-migrate, protoc plugins).
make tools

# Start a local Postgres in podman/docker.
make dev-up

# Apply migrations (creates the schema + the stillhouse_app role).
make migrate-up

# Seed a tenant + admin user. Prints the random password — capture it.
make seed

# In two terminals:
make backend-dev    # Go server on :8080, connected as stillhouse_app
make web-dev        # Vite dev server on :5173 (proxies API to :8080)
```

Then open http://localhost:5173 and log in with the seeded credentials.

### Running the integration test

The RLS isolation test exercises two real tenants through both the
admin pool (to insert fixtures) and the app pool (to verify each
tenant sees only its own rows). Requires `make dev-up`.

```sh
make test-integration
```

## MCP server

Stillhouse exposes a [Model Context Protocol](https://modelcontextprotocol.io)
server at `/mcp`, so an LLM (e.g. Claude on your phone) can read the
ledger and capture activity while you have wet hands at the still.

It reuses the same RPC service implementations as the web UI, so RLS
tenant isolation, audit-log writes, and role gating all behave
identically — an MCP-driven barrel fill leaves the same trail as a
web-driven one.

### Issue a token

Tokens are per-user. The plaintext value is printed once; only its
SHA-256 hash is stored.

```sh
make mcp-token EMAIL=you@example.com NAME="phone"
```

### Connect from a client

Configure a remote MCP server pointing at your Stillhouse install:

- **URL:** `https://stillhouse.example.com/mcp`
- **Header:** `Authorization: Bearer sh_…` (the token printed above)

### Tools

- **Read** — `get_dashboard`, `list_bulk_containers`, `get_bulk_container`,
  `list_barrels`, `get_barrel`, `list_recent_bulk_movements`,
  `list_recipes`, `list_products`, `list_b266_periods`
- **Capture** — `fill_barrel`, `regauge_barrel`, `dump_barrel`,
  `add_fermentation_reading`, `add_mash_reading`

Back-office work (distillation runs with cuts, bottling, removals,
B266 generation) stays in the web UI — those flows have multi-row
inputs that don't translate cleanly to a chat interface.

## License

Stillhouse is licensed under the [GNU Affero General Public License v3.0](LICENSE).
