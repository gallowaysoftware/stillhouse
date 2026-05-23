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
  - `WEB_IPV4=172.16.3.210` (or any free IP on br0)
  - Leave `STILLHOUSE_IMAGE` alone unless you're pinning a specific SHA

```
chmod 600 .env
```

### 3. br0 macvlan

The app dual-homes onto Unraid's `br0` macvlan network so it appears on
the LAN with its own IP (default `172.16.3.210`). Postgres stays on the
internal `stillhouse` bridge only, never reachable from outside the host.

If `br0` doesn't exist yet on this Unraid host:
- Settings → Docker → enable macvlan/ipvlan mode.
- Once you have at least one Docker container bound to `br0`, the
  network shows up automatically.

If you need a different IP, change `WEB_IPV4` in `.env`. Pick one
outside your DHCP pool and not in use.

### 4. Nginx Proxy Manager

New Proxy Host:
- Domain: `stillhouse.thegalloways.ca` (or whatever)
- Scheme: `http`
- Forward Hostname/IP: `172.16.3.210` (whatever `WEB_IPV4` is set to)
- Forward Port: `8080`
- Block Common Exploits: on
- Websockets: on (Connect-RPC uses streaming responses for big lists)
- SSL tab: request a Let's Encrypt cert, force SSL

You can also hit `http://172.16.3.210:8080` directly from any LAN host
for debugging — no TLS, no NPM, just the app.

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

The schema is empty after first migration. Create the first tenant +
owner user:

```bash
docker exec -it stillhouse-app /app/stillhouse-seed
```

The seed binary reads `ADMIN_DATABASE_URL` directly (no shell needed —
the runtime is distroless and has no `/bin/sh`). This prints the
generated email + password — record them before you lose
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
  (check `docker ps`) but NPM can't reach `WEB_IPV4:8080`. Verify the
  app actually got the macvlan IP: `docker inspect stillhouse-app |
  grep IPAddress`. If NPM itself is in Docker, note that containers on
  the host network can't reach macvlan peers on the same host by
  default — NPM either needs to be on br0 too, or on a separate host.
- **macvlan IP collision** — `docker compose up` complains about
  `address already in use`. Some other device on the LAN grabbed
  `WEB_IPV4` (often DHCP handed it out before you reserved it). Pick a
  different IP outside the DHCP range and update `.env`.
