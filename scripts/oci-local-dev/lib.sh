#!/usr/bin/env bash
#
# lib.sh — shared helpers for the OCI infra harness (migration-gate, crud-smoke).
#
# Source this from other scripts:
#   SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
#   . "$SCRIPT_DIR/lib.sh"
#
# Provides: REPO_ROOT / WORKSPACE_ROOT discovery, logging, an engine-agnostic
# `psql_q` that runs SQL inside the postgres container, and small wait helpers.
# Never reads, prints, or logs secrets.

# Guard against double-sourcing.
if [ -n "${__OCI_LIB_SOURCED:-}" ]; then return 0 2>/dev/null || true; fi
__OCI_LIB_SOURCED=1

LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$LIB_DIR/../.." && pwd)"            # vsa-control-plane/
WORKSPACE_ROOT="$(cd "$REPO_ROOT/.." && pwd)"         # parent of the repo
ENGINE_SH="$LIB_DIR/container-engine.sh"

# Docker-first: Docker Desktop on macOS sometimes does not symlink its `docker`
# CLI onto PATH. Add it so every harness script can call `docker` directly.
if ! command -v docker >/dev/null 2>&1; then
  for __d in /usr/local/bin /opt/homebrew/bin \
             /Applications/Docker.app/Contents/Resources/bin; do
    if [ -x "$__d/docker" ]; then PATH="$__d:$PATH"; export PATH; break; fi
  done
  unset __d
fi

# Postgres container name from docker-compose.local.yml.
PG_CONTAINER="${OCI_PG_CONTAINER:-vcp-postgres}"
PG_SUPERUSER="${OCI_PG_SUPERUSER:-postgres}"
PG_PASSWORD="${OCI_PG_PASSWORD:-postgres}"

# ANSI colours, disabled when stdout is not a TTY or NO_COLOR is set.
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  C_RED=$'\033[0;31m'; C_GREEN=$'\033[0;32m'; C_YELLOW=$'\033[1;33m'
  C_BLUE=$'\033[0;34m'; C_DIM=$'\033[2m'; C_RESET=$'\033[0m'
else
  C_RED=""; C_GREEN=""; C_YELLOW=""; C_BLUE=""; C_DIM=""; C_RESET=""
fi

log()  { printf '%s\n' "$*" >&2; }
info() { printf '%s%s%s\n' "$C_BLUE" "$*" "$C_RESET" >&2; }
ok()   { printf '%s%s%s\n' "$C_GREEN" "$*" "$C_RESET" >&2; }
warn() { printf '%s%s%s\n' "$C_YELLOW" "$*" "$C_RESET" >&2; }
err()  { printf '%s%s%s\n' "$C_RED" "$*" "$C_RESET" >&2; }
die()  { err "ERROR: $*"; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

# engine — print the resolved container engine binary (docker).
engine() { "$ENGINE_SH" engine; }

# compose [args...] — run "<engine> compose -f <compose-file> [args...]".
compose() { "$ENGINE_SH" compose "$@"; }

# engine_exec <container> [cmd...] — run a command inside a running container.
engine_exec() {
  local container="$1"; shift
  "$(engine)" exec -i "$container" "$@"
}

# psql_q <dbname> <sql> — run a single SQL statement inside the postgres
# container and print the result with no headers/alignment (-tA).
# Uses PGPASSWORD via the container env so no secret is echoed on the host.
psql_q() {
  local db="$1"; shift
  local sql="$1"; shift
  "$(engine)" exec -i \
    -e "PGPASSWORD=$PG_PASSWORD" \
    "$PG_CONTAINER" \
    psql -v ON_ERROR_STOP=1 -U "$PG_SUPERUSER" -d "$db" -tA -c "$sql"
}

# psql_file <dbname> — run SQL piped on stdin inside the postgres container.
psql_file() {
  local db="$1"; shift
  "$(engine)" exec -i \
    -e "PGPASSWORD=$PG_PASSWORD" \
    "$PG_CONTAINER" \
    psql -v ON_ERROR_STOP=1 -U "$PG_SUPERUSER" -d "$db"
}

# pg_ready — return 0 when the postgres container accepts connections.
pg_ready() {
  "$(engine)" exec -i "$PG_CONTAINER" pg_isready -U "$PG_SUPERUSER" >/dev/null 2>&1
}

# wait_for <seconds> <description> <command...> — poll command until it
# succeeds or the timeout elapses. Returns non-zero on timeout.
wait_for() {
  local timeout="$1"; shift
  local desc="$1"; shift
  local deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    if "$@" >/dev/null 2>&1; then return 0; fi
    sleep 2
  done
  err "timed out after ${timeout}s waiting for: $desc"
  return 1
}

# http_json <method> <url> <opc-request-id> [body-file] -- curl helper that
# prints "<http_status>\n<body>" so callers can parse both. Adds the OCI
# headers every endpoint requires. No auth token is needed for the local proxy.
http_json() {
  local method="$1" url="$2" opc="$3" body_file="${4:-}"
  local args=(-sS -o - -w $'\n%{http_code}' -X "$method"
    -H "opc-request-id: $opc" -H "Content-Type: application/json")
  if [ -n "$body_file" ]; then args+=(--data "@$body_file"); fi
  curl "${args[@]}" "$url"
}

# new_uuid — print a lowercase RFC-4122 UUID (needed for opc-request-id).
new_uuid() {
  if have uuidgen; then uuidgen | tr 'A-Z' 'a-z'; return; fi
  if [ -r /proc/sys/kernel/random/uuid ]; then cat /proc/sys/kernel/random/uuid; return; fi
  python3 - <<'PY'
import uuid; print(uuid.uuid4())
PY
}
