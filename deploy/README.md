# Stillhouse deploy

End-to-end recipe for pushing a build from a dev machine and standing it up
on an Unraid host behind Nginx Proxy Manager. Single-tenant single-host;
multi-host is a future-day problem.

## One-time setup

### 1. Registry trust on Unraid

You're pulling from `registry.home.thegalloways.ca` (NPM-fronted, HTTPS).
Nothing to configure on the Docker daemon — it trusts public CAs out of
the box, and NPM is presenting a real cert. If you ever switch to plain
HTTP, add this to `/boot/config/plugins/dockerMan/template-defaults.cfg`
or the daemon config:

```
{
  "insecure-registries": ["172.16.3.207:5000"]
}
```

### 2. Stack directory on Unraid

```
mkdir -p /mnt/user/appdata/stillhouse/{postgres,compose}
cd /mnt/user/appdata/stillhouse/compose
```

Drop in:
- `compose.yaml` — copy of `deploy/compose.prod.yaml`
- `.env` — copy of `deploy/.env.example`, then edit:
  - `PG_ADMIN_PASSWORD` — `openssl rand -base64 32`
  - `PG_APP_PASSWORD` — different `openssl rand -base64 32`
  - `STILLHOUSE_DATA_DIR=/mnt/user/appdata/stillhouse`
  - `STILLHOUSE_PORT=8080` (or another free port)
  - Leave `STILLHOUSE_IMAGE` alone unless you're pinning a specific SHA

```
chmod 600 .env
```

### 3. Nginx Proxy Manager

New Proxy Host:
- Domain: `stillhouse.thegalloways.ca` (or whatever)
- Scheme: `http`
- Forward Hostname/IP: `<unraid host or container IP>`
- Forward Port: `8080` (or your `STILLHOUSE_PORT`)
- Block Common Exploits: on
- Websockets: on (Connect-RPC uses streaming responses for big lists)
- SSL tab: request a Let's Encrypt cert, force SSL

## Push a new build (dev machine)

```bash
# From repo root, on a clean working tree:
make image-push
```

That builds the multi-stage image (web frontend + Go backend + embedded
migrations) and pushes two tags:
- `registry.home.thegalloways.ca/stillhouse:latest`
- `registry.home.thegalloways.ca/stillhouse:<git-short-sha>`

To deploy a specific SHA in production instead of tracking `:latest`:

```bash
# In /mnt/user/appdata/stillhouse/compose/.env on Unraid:
STILLHOUSE_IMAGE=registry.home.thegalloways.ca/stillhouse:abc1234
```

## Pull + restart (Unraid)

```bash
cd /mnt/user/appdata/stillhouse/compose
docker compose pull app
docker compose up -d
```

The app container's entrypoint will:
1. Apply any pending migrations against the superuser DSN.
2. Rotate the `stillhouse_app` role password to whatever's in `PG_APP_PASSWORD`.
3. Switch to the app DSN and start serving on `:8080`.

If you'd rather drive it from Unraid's Docker Compose Manager plugin,
import the same `compose.yaml` / `.env` pair and toggle from the UI.

## First-run bootstrap

The schema is empty after first migration. To create the first tenant +
owner user:

```bash
# Get the admin DSN that the running container is using:
docker exec stillhouse-app printenv ADMIN_DATABASE_URL
# Run the seed binary against it (the binary is in the same image):
docker exec -it stillhouse-app \
  sh -c 'DATABASE_URL="$ADMIN_DATABASE_URL" /app/stillhouse-seed'
```

This prints the generated email + password — record them before you lose
the terminal. After login, change the password via Settings → Change my
password.

## Backups

The `postgres` data dir is the only thing you care about. Stick this in
Unraid's user-script schedule:

```bash
docker exec stillhouse-postgres \
  pg_dump -U stillhouse -Fc stillhouse \
  > /mnt/user/backups/stillhouse/$(date +%Y-%m-%d).dump
```

Or use the in-app owner-only `/export/tenant.zip` for a CSV bundle (good
for offsite copies, easier to inspect than a binary pg_dump).

## Rollback

Roll back by changing `STILLHOUSE_IMAGE` to the previous SHA and
`docker compose up -d`. Migrations are not auto-rolled-back — if the
new build added a migration that the old build doesn't know about,
either:
- Don't roll back across schema changes; fix forward.
- Or `make migrate-down N=1` on the old image's source tree against the
  same DB before downgrading the image.

## Troubleshooting

- **App container exits with `migrate: ...`** — superuser DSN is wrong or
  the postgres container hasn't finished initialising. Check `docker logs
  stillhouse-postgres`.
- **`DATABASE_URL is required`** — `.env` not loaded. Make sure the file
  sits next to `compose.yaml` in the same directory you `docker compose
  up` from.
- **NPM returns 502 / WebSocket disconnect** — the app container is up
  (check `docker ps`) but NPM can't reach it. Re-check the proxy host's
  Forward IP and Forward Port; if running NPM in Docker too, both
  containers need to be on the same Docker network.
