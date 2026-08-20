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
| 111 | Gin recipe UI — form branches on spirit kind, botanical-role pickers, tasting notes, sensory scoring bench (0–10 on 10 axes), version-compare with quantity & score diff highlighting |
| 112 | MCP gin-bench tools — get_recipe, list_recipe_versions, save_recipe_version_sensory; lets an LLM read the recipe + iteration history and score a fresh tasting from the still floor |
| 113 | Gin-bench QA fixes — list_recipe_versions joins sensory (compare workflow works end-to-end on MCP + web), sensory upsert is partial-update so single-axis tweaks don't null the other 9 |
| 114 | Close P3 backlog — LAA/ABV/duty/CAD rounded at display, sensory bench gated to gin recipes, FillBarrel rejects overfill, write tools emit full schema (no more `_set: true` with no value) |
| 115 | Sensory gin-only gate now surfaces as failed_precondition (was getting swallowed as internal error by the handler's catch-all) |
| 116 | Whisky tasting bench — SWRI Flavour Wheel axes (cereal / estery / floral / peaty / feinty / sulphury / woody / winey) + body / finish / overall; backend + MCP `save_recipe_version_whisky_sensory` + web UI for whisky-family recipes |
| 117 | Strength at 20 °C — gauges resolve through the CRA Canadian Alcoholometric Tables 1980; hydrometer indication + temperature determine both strength and volume, the as-observed reading is kept for audit, and every reading records which determination path produced it |
| 118 | Mash bench — cereal species on materials drives gelatinisation guidance off the real grain bill; flags when maize or rice force a separate cereal cook, checks mash temp / pH / thickness against the amylase bands, computes conversion efficiency from OG, and calculates strike temperature. MCP `get_mash` + `plan_strike` |
| 119 | Reduction calculator — proofing down by volume *or by weight*, with the ethanol/water volume contraction computed from the CRA density column instead of estimated. Weighing is exact (mass is additive); the volume figure carries the correction. On tank pages, the bottling page, and MCP `plan_reduction` |
| 120 | Sensory radar — flavour profiles drawn as shapes on both tasting benches, and two versions overlaid on one plot in version-compare. Hand-rolled SVG, no chart dependency; series colours validated for colour-blind separation against both themes |
| 121 | Angel's share — measured annual loss against the band a cool, humid warehouse should hold to, with the shelf height deciding which way the strength ought to drift. Flags a probable leak, and flags strength moving against its position as a likely gauge error. Dashboard alert + barrel panel |
| 122 | Money is not a quantity — `formatCAD` renders CAD at two decimals with grouping everywhere (duty payable was showing four); plus a real bug where the stagnant-inventory alert compared against the wrong enum value and silently skipped every "Other" container, and the last of the hardcoded palette names |
| 123 | Tests for the arithmetic that had none — pricing, password hashing, and the deposit/blend maths. Caught a rounding bug: the `int(x+0.5)` idiom in the rpc helpers rounds negatives the wrong way, and negatives reach it |
| 124 | Adopt existing stock — bring a working distillery's casks into the ledger from a scale reading and a hydrometer, with no batch history behind them. CRA's Mass/Density Procedure; keeps the cask's real age so adopted whisky doesn't lose its CW eligibility; books as opening inventory, never as production |
| 125 | Cut profile — where a run's alcohol went, as an emphasis chart with hearts against the rest, plus the mass balance (you cannot collect more than you charged) and a strength-falls-through-the-run check. Also stops a repeated fermenter charge reporting as a 500 |
| 126 | Yield sanity — a recipe's projection expressed as L/tonne against what the grain can actually give, so efficiencies left at 1.0 stop producing a confident impossible number. Separates "physically impossible" from "ahead of industry" |
| 127 | Fermentation curve — gravity and temperature over time as two plots sharing an x-axis (never a dual axis), plus phase inference and findings for a stuck ferment, thermal stress, and the pH crash that signals contamination |
| 128 | Vatting planner — what actually comes out when parcels are blended, since a blend is neither the sum of its volumes nor the weighted mean of its strengths, then optionally reduced to bottling strength. Web + MCP `plan_blend` |
| 129 | Pricing by channel — wholesale, on-site retail and export priced separately, with every rate carrying its provenance. Ontario and PEI now REFUSE to price the board channel rather than guessing, because neither publishes a spirits mark-up |
| 130 | Operator-supplied alcoholometric tables — the CRA tables are no longer shipped in the binary. Each install downloads its own copy (Crown material: non-commercial reproduction only), the server reads the ZIP as downloaded, and a missing copy degrades to uncorrected readings with a Settings panel explaining the one-time fix rather than taking anything down |
| 131 | Ledger integrity — QA drove the live system as a sole operator and as a team and found alcohol being created: concurrent withdrawals clobbered each other (8 fills moved 800 L while the tank fell 100), a barrel fill credited at the gauged strength but debited at the tank's, a dump silently deleted what the cask kept, and barrel/blend writes landed in already-filed B266 periods |
| 132 | Say which field and why — the day-one adoption path gets the overfill guard the fill path already had, `extract_pct` stops accepting 78 where 0.78 belongs (it was projecting a 1077% ABV wash), the yield check gains an absolute anchor so it can catch an extract its own ceilings are derived from, and validation errors stop arriving as `internal` |

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
- **Mash guidance** — gelatinisation ranges, amylase activity windows, pH
  and thickness bands come from the IBD/CIBD distilling curriculum and are
  cited on the constant that carries them
  ([backend/internal/mashing](backend/internal/mashing/)). Where the
  curriculum gives no figure for a cereal, Stillhouse reports it as unknown
  rather than interpolating.
- **Alcoholometry** — strength and volume are resolved to 20 °C against the
  CRA [Canadian Alcoholometric Tables 1980](https://www.canada.ca/en/revenue-agency/services/tax/technical-information/excise-duty/tables-alcoholometry/canadian-alcoholometric-tables-1980.html),
  computed from the OIML general formula (International Recommendation
  No. 22, 1972). The tables are Crown material and are **not** shipped —
  you download them from CRA once and point the server at the file (see
  [deploy/README.md](deploy/README.md)); until you do, readings record
  uncorrected and nothing else changes. With a copy present the package
  replays all 117,137 published rows back through the lookup, and CRA's
  own worked examples, as tests.
- **License** — AGPL-3.0. Free to self-host, forever. See
  [LICENSE](LICENSE), [NOTICE](NOTICE) for third-party material, and
  [CONTRIBUTING.md](CONTRIBUTING.md) before opening a PR.

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
- **Bench** — `get_mash` (grain bill + readings + mash guidance),
  `plan_strike` (strike temperature for a target rest),
  `plan_reduction` (proofing down, by volume or by weight),
  `plan_blend` (vatting parcels together, optionally reduced)

Barrel fill / regauge / dump and the production gauge accept a hydrometer
indication (`density_kg_m3`) plus `temperature_c`; supply both and the
strength and volume are resolved to 20 °C through the published CRA tables
rather than taken as typed.

Back-office work (distillation runs with cuts, bottling, removals,
B266 generation) stays in the web UI — those flows have multi-row
inputs that don't translate cleanly to a chat interface.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). The short version: cite your
sources for anything that produces a number a distiller relies on, don't
invent domain constants, and keep alcohol conserved.

First pull request? A bot will ask you to sign the [CLA](CLA.md) — one
comment, once. Stillhouse stays AGPL-3.0 regardless; the CLA exists so
the project can also be offered commercially, and it doesn't affect your
copyright in your own work.

Security issues: [SECURITY.md](SECURITY.md) — please don't open a public
issue.

## License

Stillhouse is licensed under the
[GNU Affero General Public License v3.0](LICENSE).

You can run it, read it, modify it and self-host it for free, forever.
The AGPL's network clause means that if you modify Stillhouse and offer
it to others over a network, you have to offer them the source of your
modified version too.

[NOTICE](NOTICE) records third-party material. Note that the CRA's
Canadian Alcoholometric Tables are deliberately *not* included — each
operator supplies their own copy — which keeps commercial redistribution
of Stillhouse itself clear of the Government of Canada's non-commercial
reproduction terms.
