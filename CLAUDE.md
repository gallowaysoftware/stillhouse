# Stillhouse — Claude Code project notes

Open-source distillery management for Canadian craft distillers. See
[`README.md`](README.md) for the user-facing overview and the stages
table tracking what's shipped.

## Layout

```
proto/                 .proto source of truth (Buf)
backend/
  cmd/server/          binary entrypoint
  cmd/seed/            bootstrap tenant + admin
  cmd/mcp-token/       recovery-path API-token issuer (UI is preferred)
  internal/audit/      audit-log writer
  internal/auth/       Argon2id password hashing
  internal/config/     env config
  internal/db/         pgx pool, migrations, sqlc queries + generated
  internal/distilling/ mass → sugar → ethanol → LAA projection math
  internal/excise/     Canadian excise duty rates
  internal/genpb/      generated Go from .proto (do not hand-edit)
  internal/mcp/        Model Context Protocol server (mounted at /mcp)
  internal/rpc/        ConnectRPC service implementations
  internal/server/     HTTP server, session middleware, static embed
  internal/tenantdb/   WithTenantTx — opens a tx, sets RLS GUC
web/                   React + TS + Vite frontend; generated TS client in src/gen/
deploy/                Dockerfile + compose
```

## Build / test / lint

Direct commands (the Makefile assumes `/usr/bin/env bash` resolves
cleanly; on some distros that breaks — fall back to plain invocations):

```sh
# Codegen
cd proto && buf generate --template buf.gen.yaml      # Go stubs
cd proto && buf generate --template buf.gen.web.yaml  # TS client
cd backend && sqlc generate                            # SQL → Go

# Backend
cd backend && go vet ./...
cd backend && go build ./...
cd backend && go test ./...

# Frontend
cd web && npm run lint
cd web && npm run build

# Dev DB (postgres in a container, bind-mounted to .data/postgres)
podman compose -f deploy/compose.yaml up -d postgres
migrate -path backend/internal/db/migrations \
  -database "postgres://stillhouse:stillhouse@localhost:5432/stillhouse?sslmode=disable" up
cd backend && DATABASE_URL=... go run ./cmd/seed

# Nuke + reset dev DB (compose down -v doesn't, bind mount survives)
podman compose -f deploy/compose.yaml down
podman unshare rm -rf .data/postgres
podman compose -f deploy/compose.yaml up -d postgres
```

## Container build + ship

Image registry is `registry.home.thegalloways.ca/stillhouse`. Tag with
the short git SHA + `:latest`. Push both. **Kyle pulls + restarts on
unraid himself** — don't try to automate that, ping him with the new
tag and stop.

```sh
SHA=$(git rev-parse --short HEAD)
podman build -f deploy/Dockerfile \
    -t registry.home.thegalloways.ca/stillhouse:$SHA \
    -t registry.home.thegalloways.ca/stillhouse:latest .
podman push registry.home.thegalloways.ca/stillhouse:$SHA
podman push registry.home.thegalloways.ca/stillhouse:latest
```

Production URL: `https://stillhouse.home.thegalloways.ca` (home
network, private).

## Conventions

- **Stage commits.** Every shipped feature is a single commit titled
  `stage N: <terse one-liner>`. Add a row to the README stages table
  in the same commit. Cross-reference related stages in the body
  (`see also stage 109`).
- **Web UI first.** Any operator-facing operation (token mgmt,
  invites, audit export, B266 generation, etc.) goes in the web UI,
  not a CLI. CLIs are reserved for bootstrap (`cmd/seed`,
  `cmd/server`), migrations, and recovery (`cmd/mcp-token`).
- **Canada-only framing.** LAA not proof gallons. B266 not 5110.40.
  Bulk / packaged not in-bond / tax-determined. Province-coded
  excise stamps as a first-class concept. Never reference TTB /
  DSP / 5110-series in shipped code, docs, or UI.
- **B266 scope.** B266 tracks alcohol (bulk + packaged + duty). Mash
  and fermentation are pre-alcohol — useful operational data, not
  on the B266 critical path.
- **Auth is dual-mode.** ConnectRPC interceptor accepts either an
  scs session cookie (browser) or `Authorization: Bearer sh_…`
  (MCP + scripts). Both resolve to a sqlcgen.User attached to the
  ctx via `rpc.WithUser`.
- **RLS enforcement.** Server runs as `stillhouse_app` (non-super).
  Migrations + seed run as `stillhouse` (super). `WithTenantTx` sets
  `app.current_tenant_id` before queries.
- **Proto generation drift.** Generated code in `internal/genpb/` and
  `web/src/gen/` is committed. After editing a `.proto`, regenerate
  both before committing. The Dockerfile regenerates the web client
  during the image build, so a generated-code drift between commit
  and Docker build will fail loudly.
- **Strength is a 20 °C quantity.** Never store or compare a strength
  without knowing its temperature. Gauging paths funnel through
  `rpc.resolveStrength`, which records whether a figure was determined
  from a hydrometer indication, corrected for volume only, or left
  uncorrected. The embedded table is regenerated with
  `go run gentable.go -src ALC_TAB.TXT -out alctab.bin` from the CRA ZIP;
  the 5.2 MB source is deliberately not committed, and its SHA-256 is
  pinned in the package test.
- **Cite the curriculum, or say you can't.** Domain constants in
  `internal/mashing` carry the CIBD passage they came from on the
  constant itself. Where the curriculum has no figure (oat
  gelatinisation, say), the code returns "unknown" and the UI says so —
  never interpolate a plausible-looking number into operator guidance.
- **Don't `gofmt -w` a whole directory.** Several files under
  `internal/rpc/` aren't gofmt-clean; formatting them sweeps unrelated
  churn into a stage commit. Format the specific files you edited.
- **Float display.** LAA / volume / duty values get full IEEE-754
  noise on the wire (`0.8399999999999999`). Not pretty — known issue
  (QA finding F17). Display rounding lives in `web/src/lib/format.ts`.

## MCP server

Stillhouse embeds an MCP server at `/mcp` (Streamable HTTP). Reuses
the same RPC service implementations as the web UI, so RLS, audit,
and role gating behave identically. See `backend/internal/mcp/` and
README's MCP section.

When `mcp__stillhouse__*` tools are loaded in a Claude Code session,
prefer them for QA + reads + supported writes. Tools intentionally
cover only the "operator with wet hands" surface; back-office writes
(production gauge, bottling, removal, B266 gen) stay in the web UI.

## QA pattern

Drive the live system end-to-end against real seeded data, not just
unit tests. Direct ConnectRPC via curl is in scope for seeding when
the MCP surface is too narrow. File findings as you go with severity
(P0–P3). Bundle fixes into a tight single-purpose stage. The bottling
LAA-conservation P0 (stage 109) was found this way — a unit test
wouldn't have surfaced it.

## What to read first

- `README.md` — user-facing overview + stages table
- `proto/stillhouse/v1/*.proto` — the API surface, well-commented
- `backend/internal/rpc/bottling.go` (LAA conservation), `barrel.go`
  (maturation + regauge), `b266.go` (period generation) — the
  load-bearing math
- `backend/internal/alcoholometry/` — CRA Canadian Alcoholometric
  Tables 1980; every strength/volume lands at 20 °C through here
- `backend/internal/mashing/` — cereal gelatinisation, amylase windows,
  strike temperature, conversion efficiency (CIBD-sourced, cited inline)
- `backend/internal/distilling/` — projection math (whisky spine)
- `backend/internal/mcp/` — MCP tool registrations
- `web/src/pages/RecipeDetailPage.tsx` — the most feature-dense page
  (gin recipe development, sensory bench, version compare)
