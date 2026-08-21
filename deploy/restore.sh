#!/usr/bin/env bash
#
# Restore a Stillhouse backup.
#
# Deliberately noisy and deliberately hard to fire by accident. This
# command destroys a database; the failure that matters is running it
# against the wrong one at 3am with a hangover.
#
# Usage:
#   deploy/restore.sh <dump-file> [--into <container>] [--force]
#
#   --into    postgres container to restore into (default $PG_CONTAINER)
#   --force   allow restoring over a database that already has tables

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$SCRIPT_DIR/lib.sh"

[ -f "$SCRIPT_DIR/.env" ] && { set -a; . "$SCRIPT_DIR/.env"; set +a; }

DUMP=""
TARGET="${PG_CONTAINER:-stillhouse-postgres}"
FORCE=0
PG_USER="${PG_USER:-stillhouse}"
PG_DB="${PG_DB:-stillhouse}"

while [ $# -gt 0 ]; do
    case "$1" in
        --into)  TARGET="$2"; shift 2 ;;
        --force) FORCE=1; shift ;;
        -*)      die "unknown option $1" ;;
        *)       DUMP="$1"; shift ;;
    esac
done
[ -n "$DUMP" ] || die "usage: restore.sh <dump-file> [--into <container>] [--force]"
[ -f "$DUMP" ] || die "no such file: $DUMP"
CLI="$(container_cli)"

# Integrity before anything else. Restoring a corrupted dump over a live
# database turns one problem into two.
if ! checksum_verify "$DUMP"; then
    [ "$FORCE" = "1" ] || die "checksum verification failed or missing; pass --force to restore anyway"
    warn "checksum not verified, continuing because --force was given"
fi

WORK=""
cleanup() { [ -n "$WORK" ] && rm -rf "$WORK"; }
trap cleanup EXIT

if [ "${DUMP##*.}" = "age" ]; then
    have_age || die "this backup is age-encrypted but age is not installed"
    [ -n "${BACKUP_AGE_IDENTITY:-}" ] || die "BACKUP_AGE_IDENTITY (path to the private key) is not set"
    WORK="$(mktemp -d)"
    say "decrypting…"
    age -d -i "$BACKUP_AGE_IDENTITY" -o "$WORK/restore.dump" "$DUMP"
    DUMP="$WORK/restore.dump"
fi


# Roles first. They are cluster-wide, so a pg_dump of one database carries
# the GRANTs that reference stillhouse_app but not the role itself —
# restoring without them fails on the first GRANT.
apply_globals() {
    local target="$1" globals="$2"
    [ -f "$globals" ] || return 0
    say "applying roles…"
    # Roles that already exist are not an error here: restoring onto a
    # cluster that already has them is the ordinary case.
    "$CLI" exec -i "$target" psql -U "$PG_USER" -d postgres -v ON_ERROR_STOP=0 \
        < "$globals" >/dev/null 2>&1 || true
}

GLOBALS="${DUMP%.dump}.globals.sql"
[ -f "$GLOBALS" ] || GLOBALS=""

EXISTING="$("$CLI" exec -i "$TARGET" psql -U "$PG_USER" -d "$PG_DB" -tAc \
    "SELECT count(*) FROM information_schema.tables WHERE table_schema='public'" 2>/dev/null || echo 0)"
if [ "${EXISTING:-0}" -gt 0 ] && [ "$FORCE" != "1" ]; then
    die "$PG_DB on $TARGET already has $EXISTING tables. Restoring would overwrite them. Pass --force if that is what you want."
fi

[ -n "$GLOBALS" ] && apply_globals "$TARGET" "$GLOBALS"

say "restoring into $PG_DB on $TARGET…"
# --clean --if-exists so a restore over an existing database replaces it
# rather than colliding. Single transaction: a restore that fails halfway
# leaves the database as it was, rather than half of each.
if ! "$CLI" exec -i "$TARGET" pg_restore -U "$PG_USER" -d "$PG_DB" \
        --clean --if-exists --no-owner --single-transaction < "$DUMP"; then
    die "pg_restore failed; the target database was rolled back to its previous state"
fi

say "restore complete."
say
say "Now check it before trusting it:"
say "  * log in and open the B266 page — do the last few periods look right?"
say "  * check a container balance you know the value of"
say "  * confirm the audit log's most recent entry is roughly when you expect"
