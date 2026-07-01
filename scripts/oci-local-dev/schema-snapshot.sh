#!/usr/bin/env bash
#
# schema-snapshot.sh — capture a "prod-like" schema snapshot for the migration
# gate to replay against.
#
# The dirty-version-40 class of bug only reproduces when migrations run against
# a schema that already drifted (e.g. an existing pool_views with a different
# column shape). A fresh empty DB will NOT reproduce it. Feed the resulting
# snapshot to migration-gate.sh via `--snapshot`.
#
# Two modes:
#
#   from-db   pg_dump --schema-only against a reachable database (a sanitized
#             prod/staging replica). NEVER point this at prod write traffic; use
#             a read replica or restored backup. Schema-only means no row data.
#
#               schema-snapshot.sh from-db \
#                 --dsn 'postgresql://user@replica-host:5432/vcp' \
#                 --out scripts/oci-local-dev/snapshots/vcp-prodlike.sql
#
#   from-release  Build the snapshot by replaying the migration chain that is
#                 already deployed in prod (the *previous* release), so the gate
#                 then applies only the NEW migrations on top — exactly the prod
#                 upgrade path. Requires a clean local Postgres.
#
#               schema-snapshot.sh from-release \
#                 --ref origin/release-9.19 \
#                 --out scripts/oci-local-dev/snapshots/vcp-prodlike.sql
#
# Exit codes: 0 ok, 2 usage/setup error.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$SCRIPT_DIR/lib.sh"

usage() { sed -n '2,40p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; }

cmd_from_db() {
  local dsn="" out=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --dsn) dsn="$2"; shift 2 ;;
      --out) out="$2"; shift 2 ;;
      *) die "unknown arg '$1'" ;;
    esac
  done
  [ -n "$dsn" ] || die "--dsn is required"
  [ -n "$out" ] || die "--out is required"
  mkdir -p "$(dirname "$out")"

  info "[snapshot] dumping schema-only from the target DB…"
  # Prefer a host pg_dump; otherwise run it inside the postgres container image.
  if have pg_dump; then
    pg_dump --schema-only --no-owner --no-privileges "$dsn" > "$out"
  else
    warn "[snapshot] host pg_dump not found; using the postgres container image"
    "$(engine)" run --rm -i postgres:16-alpine \
      pg_dump --schema-only --no-owner --no-privileges "$dsn" > "$out"
  fi
  ok "[snapshot] wrote $out ($(wc -l < "$out" | tr -d ' ') lines)"
  warn "[snapshot] review the file and strip anything sensitive before committing."
}

cmd_from_release() {
  local ref="" out="" db="vcp_snapshot_build"
  while [ $# -gt 0 ]; do
    case "$1" in
      --ref) ref="$2"; shift 2 ;;
      --out) out="$2"; shift 2 ;;
      --db)  db="$2"; shift 2 ;;
      *) die "unknown arg '$1'" ;;
    esac
  done
  [ -n "$ref" ] || die "--ref is required (the already-deployed release git ref)"
  [ -n "$out" ] || die "--out is required"
  mkdir -p "$(dirname "$out")"

  command -v git >/dev/null || die "git is required for from-release"
  pg_ready || die "local Postgres is not up; run the bringup first"

  local worktree; worktree="$(mktemp -d)"
  info "[snapshot] checking out '$ref' into a temp worktree…"
  ( cd "$REPO_ROOT" && git worktree add --detach "$worktree" "$ref" >/dev/null )
  trap '( cd "$REPO_ROOT" && git worktree remove --force "$worktree" >/dev/null 2>&1 || true )' RETURN

  info "[snapshot] (re)creating build DB '$db'…"
  psql_q postgres "DROP DATABASE IF EXISTS \"$db\" WITH (FORCE);" >/dev/null
  psql_q postgres "CREATE DATABASE \"$db\";" >/dev/null

  info "[snapshot] applying the released migration chain…"
  (
    cd "$worktree"
    env DB_TYPE=postgres DB_HOST="${OCI_PG_HOST:-localhost}" DB_PORT="${OCI_PG_PORT:-5432}" \
        DB_USER="$PG_SUPERUSER" DB_PASSWORD="$PG_PASSWORD" DB_NAME="$db" DB_SSL_MODE=disable \
        DB_ADMIN_USER="$PG_SUPERUSER" DB_ADMIN_PASSWORD="$PG_PASSWORD" \
        METRICS_ENABLED=false LOCAL_REGION="${LOCAL_REGION:-us-east1}" \
        go run ./tools/migrate -migrate
  ) || die "released migration chain failed to apply"

  info "[snapshot] dumping the resulting schema…"
  "$(engine)" exec -i -e "PGPASSWORD=$PG_PASSWORD" "$PG_CONTAINER" \
    pg_dump --schema-only --no-owner --no-privileges -U "$PG_SUPERUSER" -d "$db" > "$out"
  psql_q postgres "DROP DATABASE IF EXISTS \"$db\" WITH (FORCE);" >/dev/null
  ok "[snapshot] wrote $out from release '$ref'"
}

case "${1:-}" in
  from-db)      shift; cmd_from_db "$@" ;;
  from-release) shift; cmd_from_release "$@" ;;
  -h|--help|"") usage ;;
  *) die "unknown subcommand '$1' (use: from-db | from-release)" ;;
esac
