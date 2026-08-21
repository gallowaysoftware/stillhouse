#!/usr/bin/env bash
#
# Restore drill: prove the backups actually restore.
#
# PLAN H2 asked for a *tested* restore, and the only honest way to have one
# is a command anybody can run. This takes the most recent backup, restores
# it into a throwaway Postgres that touches nothing, and checks that what
# comes out the other end is a working Stillhouse database rather than an
# empty one that restored without complaint.
#
# Run it after any change to the backup arrangement, and on a schedule. A
# backup nobody has restored is a hypothesis.
#
# Usage:
#   deploy/restore-drill.sh [dump-file] [--source <container>]
#
# With no argument it takes the newest file in STILLHOUSE_BACKUP_DIR.
#
# --source names the live Postgres the backup came from. Given one, the
# drill compares row counts table by table instead of merely checking that
# something restored — which is the difference between "the mechanism
# works" and "your data survives". Without one (restoring an offsite copy
# on a different machine, which is the case that matters most) it falls
# back to checking the tables are not empty, and says so.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$SCRIPT_DIR/lib.sh"

[ -f "$SCRIPT_DIR/.env" ] && { set -a; . "$SCRIPT_DIR/.env"; set +a; }

CLI="$(container_cli)"
PG_USER="${PG_USER:-stillhouse}"
PG_DB="${PG_DB:-stillhouse}"
PG_IMAGE="${PG_IMAGE:-docker.io/library/postgres:16}"
DRILL_CONTAINER="stillhouse-restore-drill-$$"

DUMP=""
SOURCE="${PG_CONTAINER:-}"
while [ $# -gt 0 ]; do
    case "$1" in
        --source) SOURCE="$2"; shift 2 ;;
        -*)       die "unknown option $1" ;;
        *)        DUMP="$1"; shift ;;
    esac
done
if [ -z "$DUMP" ]; then
    [ -n "${STILLHOUSE_BACKUP_DIR:-}" ] || die "give a dump file, or set STILLHOUSE_BACKUP_DIR"
    DUMP="$(find "$STILLHOUSE_BACKUP_DIR" -maxdepth 1 -name 'stillhouse-*.dump*' ! -name '*.sha256' \
            | sort | tail -1)"
    [ -n "$DUMP" ] || die "no backups found in $STILLHOUSE_BACKUP_DIR"
fi
[ -f "$DUMP" ] || die "no such file: $DUMP"
ORIGINAL="$DUMP"

say "drill: restoring $(basename "$DUMP") into a throwaway Postgres"
say

FAILED=0
check() {
    local label="$1" got="$2" want="$3"
    if [ "$got" = "$want" ] || { [ "$want" = ">0" ] && [ "${got:-0}" -gt 0 ]; }; then
        printf '  ok    %s (%s)\n' "$label" "$got"
    else
        printf '  FAIL  %s: got %s, want %s\n' "$label" "$got" "$want"
        FAILED=1
    fi
}

cleanup() {
    "$CLI" rm -f "$DRILL_CONTAINER" >/dev/null 2>&1 || true
    [ -n "${WORK:-}" ] && rm -rf "$WORK"
}
trap cleanup EXIT

if ! checksum_verify "$DUMP"; then
    printf '  FAIL  checksum\n'
    FAILED=1
else
    printf '  ok    checksum\n'
fi

WORK="$(mktemp -d)"
if [ "${DUMP##*.}" = "age" ]; then
    have_age || die "this backup is age-encrypted but age is not installed"
    [ -n "${BACKUP_AGE_IDENTITY:-}" ] || die "BACKUP_AGE_IDENTITY is not set; the drill cannot decrypt"
    WORK="$(mktemp -d)"
    age -d -i "$BACKUP_AGE_IDENTITY" -o "$WORK/drill.dump" "$DUMP"
    DUMP="$WORK/drill.dump"
    printf '  ok    decrypt\n'
fi

# No published port: the drill talks to it through the runtime, so it
# cannot collide with anything and cannot be reached from the network.
"$CLI" run -d --name "$DRILL_CONTAINER" \
    -e POSTGRES_DB="$PG_DB" -e POSTGRES_USER="$PG_USER" -e POSTGRES_PASSWORD=drill \
    "$PG_IMAGE" >/dev/null

for _ in $(seq 1 60); do
    if "$CLI" exec "$DRILL_CONTAINER" pg_isready -U "$PG_USER" -d "$PG_DB" >/dev/null 2>&1; then
        break
    fi
    sleep 1
done
"$CLI" exec "$DRILL_CONTAINER" pg_isready -U "$PG_USER" -d "$PG_DB" >/dev/null 2>&1 \
    || die "the throwaway Postgres never came up"

# Roles are cluster-wide and not in a single-database dump. This drill
# found that the hard way — the first restore attempt failed on
# "role stillhouse_app does not exist" — which is exactly the sort of
# thing a drill is for, and exactly what nobody would have discovered
# until an outage.
GLOBALS="${ORIGINAL%.dump}.globals.sql"
[ "${ORIGINAL##*.}" = "age" ] && GLOBALS="${ORIGINAL%.dump.age}.globals.sql.age"
if [ -f "$GLOBALS" ]; then
    G="$GLOBALS"
    if [ "${G##*.}" = "age" ]; then
        age -d -i "$BACKUP_AGE_IDENTITY" -o "$WORK/globals.sql" "$G"
        G="$WORK/globals.sql"
    fi
    if "$CLI" exec -i "$DRILL_CONTAINER" psql -U "$PG_USER" -d postgres -v ON_ERROR_STOP=0 \
            < "$G" >/dev/null 2>&1; then
        printf '  ok    roles\n'
    else
        printf '  FAIL  roles\n'
        FAILED=1
    fi
else
    printf '  FAIL  roles: no .globals.sql beside this dump; it will not restore onto a fresh Postgres\n'
    FAILED=1
fi

# pg_restore needs a seekable file for the custom format, so the dump goes
# into the container rather than down a pipe.
"$CLI" cp "$DUMP" "$DRILL_CONTAINER:/tmp/drill.dump"

START="$(date +%s)"
if "$CLI" exec "$DRILL_CONTAINER" pg_restore -U "$PG_USER" -d "$PG_DB" \
        --no-owner --single-transaction /tmp/drill.dump >/dev/null 2>&1; then
    printf '  ok    pg_restore\n'
else
    printf '  FAIL  pg_restore\n'
    FAILED=1
fi
ELAPSED=$(( $(date +%s) - START ))

q() { "$CLI" exec -i "$DRILL_CONTAINER" psql -U "$PG_USER" -d "$PG_DB" -tAc "$1" 2>/dev/null | tr -d ' '; }

# Restoring "successfully" into an empty database is the failure this
# drill exists to catch, so every check below asks for something to be
# there rather than for the command to have exited 0.
check "schema_migrations at a version" "$(q "SELECT version FROM schema_migrations LIMIT 1")" ">0"
check "no dirty migration"             "$(q "SELECT dirty FROM schema_migrations LIMIT 1")" "f"
check "row-level security still forced" \
      "$(q "SELECT count(*) FROM pg_class WHERE relname='bulk_movements' AND relrowsecurity AND relforcerowsecurity")" "1"

# The tables worth proving survived. Not an exhaustive list — an
# exhaustive list would drift — but the spine: who the tenant is, who can
# log in, where the alcohol went, what was filed, and the trail.
SPINE="tenants users bulk_containers bulk_movements bottling_runs packaging_removals b266_periods audit_events"

SOURCE_REACHABLE=0
if [ -n "$SOURCE" ] && "$CLI" exec "$SOURCE" pg_isready -U "$PG_USER" -d "$PG_DB" >/dev/null 2>&1; then
    SOURCE_REACHABLE=1
fi

if [ "$SOURCE_REACHABLE" = "1" ]; then
    src() { "$CLI" exec -i "$SOURCE" psql -U "$PG_USER" -d "$PG_DB" -tAc "$1" 2>/dev/null | tr -d ' '; }
    TOTAL=0
    for tbl in $SPINE; do
        WANT="$(src "SELECT count(*) FROM $tbl")"
        GOT="$(q "SELECT count(*) FROM $tbl")"
        TOTAL=$(( TOTAL + ${WANT:-0} ))
        if [ "$GOT" = "$WANT" ]; then
            printf '  ok    %-20s %s rows match the source\n' "$tbl" "$GOT"
        else
            printf '  FAIL  %-20s restored %s rows, source has %s\n' "$tbl" "$GOT" "$WANT"
            FAILED=1
        fi
    done
    if [ "$TOTAL" = "0" ]; then
        say
        say "  NOTE  the source database is empty, so this drill proved the mechanism"
        say "        works but not that your data survives. Run it against a backup of"
        say "        a database with something in it before relying on the result."
    fi
else
    [ -n "$SOURCE" ] && printf '  note  source %s not reachable; checking the tables are populated instead\n' "$SOURCE"
    for tbl in $SPINE; do
        check "$tbl populated" "$(q "SELECT count(*) FROM $tbl")" ">0"
    done
fi

say
if [ "$FAILED" = "0" ]; then
    say "DRILL PASSED — restored in ${ELAPSED}s."
    say "Record the date in docs/operations.md. A drill nobody wrote down did not happen."
else
    say "DRILL FAILED — do not rely on these backups until this is understood."
fi
exit "$FAILED"
