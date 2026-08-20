# Deploying Stillhouse

Stillhouse is a single Go binary with the web UI embedded, plus a
Postgres. That's the whole system — there is no queue, cache, or object
store to stand up.

Single-tenant, single-host. Multi-host is a future-day problem.

## What you need

- A container runtime (Docker or Podman) with `compose`.
- A host with a real disk for the Postgres volume.
- A reverse proxy for TLS, unless you're happy on plain HTTP inside a
  trusted network.

## 1. Build and push an image

There are no published images — build your own so you know what's in it.
Put your registry in a gitignored `local.mk` at the repo root:

```make
REGISTRY := registry.example.com
```

Then:

```sh
make image-push     # builds, tags <short-sha> + :latest, pushes both
```

The Dockerfile regenerates the web client during the build, so a
generated-code drift between what's committed and what the proto produces
fails the build loudly rather than shipping a stale UI.

## 2. Configure the stack

```sh
mkdir -p /srv/stillhouse/{postgres,compose}
cd /srv/stillhouse/compose
cp <repo>/deploy/compose.prod.yaml compose.yaml
cp <repo>/deploy/.env.example .env
chmod 600 .env
```

Fill in `.env` — every value is documented in the file. The two passwords
want `openssl rand -base64 32` each, and they must differ:

- `PG_ADMIN_PASSWORD` — the Postgres superuser. Runs migrations and
  bootstraps the app role at boot.
- `PG_APP_PASSWORD` — the `stillhouse_app` non-superuser the running
  server connects as, so the row-level security policies actually
  enforce. Must not contain `$$`.

## 3. Networking

The compose file gives the app its own LAN IP via macvlan, so it appears
as a real host and a reverse proxy can point straight at
`WEB_IPV4:8080`. Set `COMPOSE_NETWORK_EXTERNAL` to your existing external
network (`br0` on Unraid) and `WEB_IPV4` to a free address on it.

Prefer an ordinary published port? Delete the `macvlan` network and the
app's `networks:` block from `compose.yaml` and add:

```yaml
    ports: ["8080:8080"]
```

## 4. Start it

```sh
docker compose up -d
docker logs -f stillhouse-app
```

On boot the app applies migrations with the admin DSN, rotates the app
role's password to `PG_APP_PASSWORD`, then switches to the app DSN and
serves. Watch the log for the migration lines.

## 5. Seed the first tenant

```sh
docker exec -it stillhouse-app /app/seed
```

It prints a generated admin password once. Capture it.

## Upgrading

Push a new image, then pull and restart on the host. Migrations run at
boot, so there's no separate step — but read the release notes: a stage
that adds a migration will say so.

Pin `STILLHOUSE_IMAGE` to a specific SHA in production rather than
tracking `:latest`, so a restart can't silently move you to a new
version.

## Backups

Everything that matters is in Postgres. `pg_dump` the `stillhouse`
database on whatever schedule your record-keeping obligations demand —
for a CRA-licensed producer that should be at least as long as you're
required to retain excise records.

The app also exposes a per-tenant export (Settings → Export) that writes
a zip of every table as CSV, which is the right thing to keep if you ever
want your data outside this system.
