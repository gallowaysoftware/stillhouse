# Operating a Stillhouse install

What a person running Stillhouse has to know about keeping the data
alive. Written for a single-host install; a hosted operator running it for
someone else should publish their own answers to the same questions — see
[`TERMS.md`](../TERMS.md) §5 for the list a tenant is entitled to ask.

---

## Backups

### What is and is not a backup

The tenant CSV/ZIP export (Settings → Export) is **data portability**. It
gives a distillery its records in a form it can read anywhere, and it
exists so that a hosted tenant is never locked in. It is not a backup: it
cannot reconstitute a running install.

A backup is a `pg_dump` that has been **read back** and found restorable.
`deploy/backup.sh` writes one, verifies it before counting it, and exits
non-zero if it cannot. A cron job that silently produces unreadable files
for six months is worse than no cron job, because it buys confidence that
is not there.

### What a backup consists of

Each run produces two files plus their checksums:

| file | what it is |
|---|---|
| `stillhouse-<stamp>.dump` | the database, custom format |
| `stillhouse-<stamp>.globals.sql` | the cluster's roles |

**Both are required.** Roles are cluster-wide, so a `pg_dump` of one
database carries the `GRANT … TO stillhouse_app` statements but not the
role itself. Restoring the dump alone onto a fresh Postgres fails on the
first `GRANT`. This is not hypothetical: it is what the restore drill
found the first time it was run, and it is exactly the sort of thing
nobody discovers until an outage.

The globals are dumped `--no-role-passwords`. A backup should not be a
credential store, and `compose.prod.yaml` rotates `stillhouse_app`'s
password at boot from `STILLHOUSE_APP_PASSWORD` anyway.

### Running it

```sh
STILLHOUSE_BACKUP_DIR=/srv/stillhouse/backups deploy/backup.sh
```

Configuration, by environment or by a `.env` beside the script:

| variable | default | |
|---|---|---|
| `STILLHOUSE_BACKUP_DIR` | — | required |
| `PG_CONTAINER` | `stillhouse-postgres` | |
| `BACKUP_AGE_RECIPIENT` | — | age public key; encrypts both files |
| `BACKUP_REQUIRE_ENCRYPTION` | `0` | `1` refuses to leave plaintext |
| `BACKUP_RETAIN_DAYS` | `30` | |
| `BACKUP_RETAIN_MIN` | `7` | never prune below this many, whatever the clock says |

### Cadence

**Nightly, plus before every upgrade.** A migration is the most likely
thing to need one.

Schedule it however the host schedules things — cron, a systemd timer, an
Unraid User Script. Deliberately not baked into the compose stack:
scheduling is site-specific, and a container that quietly stops running
its cron is a backup arrangement that quietly stopped.

```cron
# nightly at 02:30, log where somebody will see it
30 2 * * *  STILLHOUSE_BACKUP_DIR=/srv/stillhouse/backups /srv/stillhouse/deploy/backup.sh >> /var/log/stillhouse-backup.log 2>&1
```

The script exits non-zero on any failure. Wire that to whatever tells you
things are broken. **An unmonitored backup job is a backup job you will
discover has been failing.**

### Offsite

A backup on the same disk as the database survives a `DROP TABLE` and
nothing else — not a failed disk, not a fire, not ransomware.

Copy the backup directory somewhere else, and **encrypt it** if that
somewhere is not yours:

```sh
age-keygen -o /root/stillhouse-backup.key           # keep this OFF the server
grep '^# public key:' /root/stillhouse-backup.key   # → BACKUP_AGE_RECIPIENT
```

Set `BACKUP_AGE_RECIPIENT` and `BACKUP_REQUIRE_ENCRYPTION=1`, then sync
with rclone, restic, rsync — whatever you already run.

**Keep the private key somewhere other than the server.** A key stored
beside the backups it decrypts protects against nothing.

---

## Restoring

```sh
deploy/restore.sh /srv/stillhouse/backups/stillhouse-20260821T192110Z.dump
```

It verifies the checksum, applies the roles, and refuses to restore over a
database that already has tables unless you pass `--force`.

Then check it before trusting it:

- log in and open the B266 page — do the last few periods look right?
- check a container balance you know the value of;
- confirm the audit log's most recent entry is roughly when you expect.

### Recovery targets

These are what the arrangement above actually delivers on a single host.
They are not promises to anyone else; a hosted operator should state their
own.

| | |
|---|---|
| **RPO** (data you can lose) | up to 24 hours — the gap since the last nightly backup |
| **RTO** (time to running again) | under an hour on a host that already has the image, dominated by pulling the backup back from offsite rather than by the restore itself |

The restore itself is seconds to a couple of minutes for a distillery's
data volume. Postgres is not the slow part; finding the backup and getting
a host is.

If 24 hours of loss is too much, the answer is not more frequent
`pg_dump`s — it is WAL archiving (`pgbackrest`, `wal-g`), which is a
different arrangement and not one Stillhouse ships.

---

## The restore drill

**A backup nobody has restored is a hypothesis.**

```sh
STILLHOUSE_BACKUP_DIR=/srv/stillhouse/backups deploy/restore-drill.sh --source stillhouse-postgres
```

It takes the newest backup, restores it into a throwaway Postgres that
touches nothing, and compares row counts table by table against the live
database. Given `--source` it proves *your data survives*; without one —
which is the case that matters most, an offsite copy on a different
machine — it checks the tables are populated and says which check it ran.

Run it **after any change to the backup arrangement, and quarterly
otherwise.** Write the date down. A drill nobody recorded did not happen.

### Drill log

| date | backup tested | result | notes |
|---|---|---|---|
| 2026-08-21 | `stillhouse-20260821T192110Z` | **pass** | First drill. Found that a dump restored onto a fresh Postgres fails on `role "stillhouse_app" does not exist` — roles are cluster-wide and were not in the backup. Fixed by dumping globals alongside; re-run passed with row counts matching the source across all eight spine tables. Restore took under a second on a 372 KB dump. |

---

## Where the data lives

**Answer this for your own install.** It is a fair question under PIPEDA,
and under Quebec's Law 25 for a Quebec licensee, and a tenant is entitled
to ask it.

Stillhouse itself makes no outbound connection on your behalf. There is no
telemetry, no analytics, no vendor. The data is wherever you put the
Postgres volume and wherever you copy the backups — nowhere else.

For the reference deployment that means:

- **Primary:** the `STILLHOUSE_DATA_DIR/postgres` bind mount on the host
  running the compose stack.
- **Backups:** `STILLHOUSE_BACKUP_DIR` on the same host, plus wherever you
  sync them.
- **Nothing else.** No managed service, no object store, no third party,
  unless you configured one.

If your offsite copy goes to a provider, **that provider's region is part
of the answer** and belongs in what you tell your tenants.

---

## Retention

Subsection 206(1) of the Excise Act, 2001 requires records sufficient to
determine compliance. **Six years** is the working window.

That obligation is on the licensee, not on the software and not on whoever
hosts it. Two consequences:

1. `BACKUP_RETAIN_DAYS` is about *operational recovery*, not about
   records retention. Thirty days of nightly dumps does not satisfy a
   six-year obligation.
2. For the six-year window, keep periodic **tenant exports** (Settings →
   Export) somewhere durable — they are readable without Stillhouse,
   without Postgres, and without this repository still existing. A yearly
   export archived off-site is the version of this that still works in
   2032.

Filed B266 periods carry a frozen snapshot and a recorded acknowledgement
naming who confirmed the figures (stage 149), so an exported period is
self-describing rather than a set of numbers whose basis has to be
reconstructed.

---

## Upgrading

1. **Back up first.** `deploy/backup.sh`, and check it exited 0.
2. Pull the new image and restart the stack. Migrations run at boot.
3. If it goes wrong: restore the backup, then pin the previous image tag.

Migrations are forward-only in practice. Every one ships a `.down.sql` and
they are tested both ways, but rolling a schema backwards over data
written by the newer version is not something to attempt during an
incident. **Restore the backup instead.** That is what it is for, and it
is why step 1 is step 1.
