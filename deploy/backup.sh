#!/usr/bin/env bash
#
# Back up the Stillhouse database.
#
# A backup is not a file that was written. It is a file that has been READ
# BACK and found to be a restorable dump — so this script verifies every
# dump before it counts it, and exits non-zero if it cannot. A cron job
# that silently produces unreadable files for six months is worse than no
# cron job, because it buys confidence that is not there.
#
# The tenant CSV/ZIP export is data portability; it cannot reconstitute a
# running install. This can.
#
# Usage:
#   deploy/backup.sh
#
# Configuration, by environment or by a .env beside this script:
#   STILLHOUSE_BACKUP_DIR   where dumps go                      (required)
#   PG_CONTAINER            postgres container name             (default stillhouse-postgres)
#   PG_USER / PG_DB         role and database                   (default stillhouse/stillhouse)
#   BACKUP_AGE_RECIPIENT    age public key; encrypts the dump    (optional)
#   BACKUP_REQUIRE_ENCRYPTION  1 = refuse to write plaintext     (default 0)
#   BACKUP_RETAIN_DAYS      prune older than this               (default 30)
#   BACKUP_RETAIN_MIN       never prune below this many         (default 7)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$SCRIPT_DIR/lib.sh"

[ -f "$SCRIPT_DIR/.env" ] && { set -a; . "$SCRIPT_DIR/.env"; set +a; }

BACKUP_DIR="${STILLHOUSE_BACKUP_DIR:-}"
[ -n "$BACKUP_DIR" ] || die "STILLHOUSE_BACKUP_DIR is not set"
PG_CONTAINER="${PG_CONTAINER:-stillhouse-postgres}"
PG_USER="${PG_USER:-stillhouse}"
PG_DB="${PG_DB:-stillhouse}"
RETAIN_DAYS="${BACKUP_RETAIN_DAYS:-30}"
RETAIN_MIN="${BACKUP_RETAIN_MIN:-7}"
CLI="$(container_cli)"

mkdir -p "$BACKUP_DIR"

STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
DUMP="$BACKUP_DIR/stillhouse-$STAMP.dump"
INNER="/tmp/stillhouse-backup-$STAMP.dump"

# The dump is written, verified and hashed INSIDE the container, then
# copied out and hashed again. Two reasons. pg_restore --list needs a
# seekable file, so it cannot verify a stream; and hashing on both sides
# is what catches a copy that truncated — the failure that produces a
# plausible-looking file half the size of yesterday's.
cleanup_inner() { "$CLI" exec "$PG_CONTAINER" rm -f "$INNER" >/dev/null 2>&1 || true; }
trap cleanup_inner EXIT

say "dumping $PG_DB from $PG_CONTAINER…"
if ! "$CLI" exec "$PG_CONTAINER" sh -c "pg_dump -U '$PG_USER' -Fc '$PG_DB' > '$INNER'"; then
    die "pg_dump failed; no backup written"
fi

say "verifying…"
LISTING="$("$CLI" exec "$PG_CONTAINER" pg_restore --list "$INNER" 2>/dev/null)" \
    || die "the dump could not be read back by pg_restore; treating it as no backup at all"

# A dump that lists no table data restores to an empty database, quietly.
TABLE_COUNT="$(printf '%s\n' "$LISTING" | grep -c 'TABLE DATA' || true)"
if [ "${TABLE_COUNT:-0}" -lt 1 ]; then
    die "the dump contains no table data; treating it as no backup at all"
fi

INNER_SUM="$("$CLI" exec "$PG_CONTAINER" sha256sum "$INNER" | cut -d' ' -f1)"

if ! "$CLI" exec "$PG_CONTAINER" cat "$INNER" > "$DUMP"; then
    rm -f "$DUMP"
    die "copying the dump out of the container failed; no backup written"
fi
if [ ! -s "$DUMP" ]; then
    rm -f "$DUMP"
    die "the copied dump is empty; no backup written"
fi

OUTER_SUM="$(sha256sum "$DUMP" 2>/dev/null | cut -d' ' -f1 || shasum -a 256 "$DUMP" | cut -d' ' -f1)"
if [ "$INNER_SUM" != "$OUTER_SUM" ]; then
    rm -f "$DUMP"
    die "the dump changed on the way out of the container (truncated copy?); no backup written"
fi

checksum_write "$DUMP"

# Roles are CLUSTER-wide. pg_dump of one database carries the GRANTs that
# reference stillhouse_app but not the role itself, so restoring onto a
# fresh Postgres fails on the first GRANT — which is precisely what the
# restore drill found, and precisely the thing nobody would have learned
# until an outage. The globals travel with the backup so it is
# self-sufficient.
#
# --no-role-passwords deliberately: a backup should not be a credential
# store, and compose.prod.yaml rotates stillhouse_app's password at boot
# from STILLHOUSE_APP_PASSWORD anyway.
GLOBALS="${DUMP%.dump}.globals.sql"
say "dumping roles…"
if "$CLI" exec "$PG_CONTAINER" pg_dumpall -U "$PG_USER" --globals-only --no-role-passwords \
        > "$GLOBALS" && [ -s "$GLOBALS" ]; then
    checksum_write "$GLOBALS"
else
    rm -f "$DUMP" "$DUMP.sha256" "$GLOBALS"
    die "could not dump the cluster roles; a dump without them will not restore onto a fresh Postgres"
fi

if [ -n "${BACKUP_AGE_RECIPIENT:-}" ]; then
    have_age || die "BACKUP_AGE_RECIPIENT is set but age is not installed"
    say "encrypting…"
    age -r "$BACKUP_AGE_RECIPIENT" -o "$DUMP.age" "$DUMP"
    checksum_write "$DUMP.age"
    age -r "$BACKUP_AGE_RECIPIENT" -o "$GLOBALS.age" "$GLOBALS"
    checksum_write "$GLOBALS.age"
    # The plaintext goes only after the ciphertext exists and has its own
    # checksum. Deleting first and failing second loses the backup.
    rm -f "$DUMP" "$DUMP.sha256" "$GLOBALS" "$GLOBALS.sha256"
    DUMP="$DUMP.age"
elif [ "${BACKUP_REQUIRE_ENCRYPTION:-0}" = "1" ]; then
    rm -f "$DUMP" "$DUMP.sha256" "$GLOBALS" "$GLOBALS.sha256"
    die "BACKUP_REQUIRE_ENCRYPTION is set but no BACKUP_AGE_RECIPIENT was given; refusing to leave a plaintext dump"
fi

SIZE="$(du -h "$DUMP" | cut -f1)"
say "backup complete: $(basename "$DUMP") ($SIZE, $TABLE_COUNT tables with data)"

# Prune, but never below RETAIN_MIN. A retention policy that empties the
# directory because the clock was wrong is the worst kind of bug here.
if [ "$RETAIN_DAYS" -gt 0 ]; then
    mapfile -t ALL < <(find "$BACKUP_DIR" -maxdepth 1 -name 'stillhouse-*.dump*' ! -name '*.sha256' | sort)
    KEEP=$(( ${#ALL[@]} - RETAIN_MIN ))
    if [ "$KEEP" -gt 0 ]; then
        PRUNED=0
        for f in "${ALL[@]:0:$KEEP}"; do
            if [ -n "$(find "$f" -maxdepth 0 -mtime "+$RETAIN_DAYS" 2>/dev/null)" ]; then
                # The globals go with their dump: half a backup is not one.
                base="${f%.age}"; base="${base%.dump}"
                rm -f "$f" "$f.sha256" "$base".globals.sql "$base".globals.sql.sha256 \
                      "$base".globals.sql.age "$base".globals.sql.age.sha256
                PRUNED=$((PRUNED + 1))
            fi
        done
        [ "$PRUNED" -gt 0 ] && say "pruned $PRUNED backup(s) older than $RETAIN_DAYS days"
    fi
fi

say "$(find "$BACKUP_DIR" -maxdepth 1 -name 'stillhouse-*.dump*' ! -name '*.sha256' | wc -l | tr -d ' ') backup(s) on hand in $BACKUP_DIR"
