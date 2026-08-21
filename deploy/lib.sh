#!/usr/bin/env bash
# Shared helpers for the backup, restore and drill scripts.
#
# Sourced, not executed. Everything here is deliberately boring: these
# scripts run unattended at 3am and the failure mode that matters is the
# one that exits 0 having done nothing.

set -euo pipefail

# container_cli picks whichever runtime is installed. Podman first because
# that is what the reference deployment uses.
container_cli() {
    if [ -n "${CONTAINER_CLI:-}" ]; then echo "$CONTAINER_CLI"; return; fi
    if command -v podman >/dev/null 2>&1; then echo podman; return; fi
    if command -v docker >/dev/null 2>&1; then echo docker; return; fi
    die "no container runtime found (looked for podman, docker); set CONTAINER_CLI"
}

die()  { printf 'stillhouse-backup: %s\n' "$*" >&2; exit 1; }
warn() { printf 'stillhouse-backup: %s\n' "$*" >&2; }
say()  { printf '%s\n' "$*"; }

require() {
    command -v "$1" >/dev/null 2>&1 || die "$1 is required but not installed${2:+ ($2)}"
}

# checksum_file writes <file>.sha256 next to a file, and reads back the
# stored value. sha256sum on Linux, shasum on macOS.
checksum_write() {
    local f="$1"
    if command -v sha256sum >/dev/null 2>&1; then
        (cd "$(dirname "$f")" && sha256sum "$(basename "$f")" > "$(basename "$f").sha256")
    else
        (cd "$(dirname "$f")" && shasum -a 256 "$(basename "$f")" > "$(basename "$f").sha256")
    fi
}

checksum_verify() {
    local f="$1"
    [ -f "$f.sha256" ] || { warn "no checksum beside $(basename "$f") — cannot verify integrity"; return 1; }
    if command -v sha256sum >/dev/null 2>&1; then
        (cd "$(dirname "$f")" && sha256sum -c "$(basename "$f").sha256" >/dev/null)
    else
        (cd "$(dirname "$f")" && shasum -a 256 -c "$(basename "$f").sha256" >/dev/null)
    fi
}

# encrypt_to / decrypt_from wrap age, falling back to gpg. Both are
# symmetric-or-recipient depending on what is configured; see
# docs/operations.md.
have_age() { command -v age >/dev/null 2>&1; }
have_gpg() { command -v gpg >/dev/null 2>&1; }
