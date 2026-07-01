#!/usr/bin/env bash
#
# migration-gate.sh — Phase 0 pre-build gate for database migrations.
#
# Why this exists: production hit `Dirty database version 40. Fix and force
# version.` A migration that fails partway leaves golang-migrate's version
# table dirty and BLOCKS every later migration. The existing
# migration-validation.yml only checks that up/down files exist as pairs — it
# never *runs* the SQL, so a runtime failure (e.g. CREATE OR REPLACE VIEW
# raising SQLSTATE 42P16 against a drifted prod schema) slips through.
#
# This gate actually executes migrations against a (optionally prod-like)
# Postgres using the repo's own migrate CLI (tools/migrate), then:
#   1. runs migrate UP and fails on any error,
#   2. detects a dirty schema_migrations / schema_migrations_post row and maps
#      the dirty version back to the offending migration file,
#   3. (optional) runs an UP -> DOWN -> UP idempotency cycle to catch
#      non-idempotent migrations and broken down files.
#
# It drives the same Postgres container as docker-compose.local.yml, so it works
# locally and in CI alike.
#
# Usage:
#   migration-gate.sh [--db vcp] [--snapshot path/to/prod-schema.sql]
#                     [--fresh] [--no-idempotency]
#                     [--pre-steps N] [--post-steps N]
#
# Exit codes: 0 = pass, 1 = migration/dirty failure, 2 = setup/usage error.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$SCRIPT_DIR/lib.sh"

DB_NAME="vcp"
SNAPSHOT=""
FRESH=0
IDEMPOTENCY=1
PRE_STEPS=1
POST_STEPS=1

# migrate CLI invocation. Override MIGRATE_BIN to a prebuilt binary to skip the
# `go run` compile on every call (CI / MCP build it once).
MIGRATE_BIN="${MIGRATE_BIN:-}"

usage() { sed -n '2,30p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; }

while [ $# -gt 0 ]; do
  case "$1" in
    --db)             DB_NAME="$2"; shift 2 ;;
    --snapshot)       SNAPSHOT="$2"; shift 2 ;;
    --fresh)          FRESH=1; shift ;;
    --no-idempotency) IDEMPOTENCY=0; shift ;;
    --pre-steps)      PRE_STEPS="$2"; shift 2 ;;
    --post-steps)     POST_STEPS="$2"; shift 2 ;;
    -h|--help)        usage; exit 0 ;;
    *) die "unknown arg '$1' (see --help)" ;;
  esac
done

# --- migrate CLI wrapper -----------------------------------------------------
# All config is env-driven via common.LoadConfig(). LOCAL_REGION must be a key
# in utils.DefaultRegionNumberMap or LoadConfig() calls os.Exit(1).
run_migrate() {
  local action="$1"   # -migrate | -rollback
  local -a cmd
  if [ -n "$MIGRATE_BIN" ]; then
    # shellcheck disable=SC2206  # intentional word-split for a prebuilt command
    cmd=($MIGRATE_BIN)
  else
    cmd=(go run ./tools/migrate)
  fi
  (
    cd "$REPO_ROOT"
    env \
      DB_TYPE=postgres \
      DB_HOST="${OCI_PG_HOST:-localhost}" \
      DB_PORT="${OCI_PG_PORT:-5432}" \
      DB_USER="$PG_SUPERUSER" \
      DB_PASSWORD="$PG_PASSWORD" \
      DB_NAME="$DB_NAME" \
      DB_SSL_MODE=disable \
      DB_ADMIN_USER="$PG_SUPERUSER" \
      DB_ADMIN_PASSWORD="$PG_PASSWORD" \
      METRICS_ENABLED=false \
      LOCAL_REGION="${LOCAL_REGION:-us-east1}" \
      VCP_DB_PRE_ROLLBACK_STEPS="${VCP_DB_PRE_ROLLBACK_STEPS:-0}" \
      VCP_DB_POST_ROLLBACK_STEPS="${VCP_DB_POST_ROLLBACK_STEPS:-0}" \
      "${cmd[@]}" "$action"
  )
}

# dirty_report — print "<table>:<version>" for any dirty migration row, else
# nothing. Both phases are checked; post-migrations use schema_migrations_post.
dirty_report() {
  local out=""
  for tbl in schema_migrations schema_migrations_post; do
    # table may not exist yet on a brand-new DB; ignore those errors.
    local row
    row="$(psql_q "$DB_NAME" \
      "SELECT version FROM ${tbl} WHERE dirty = true LIMIT 1;" 2>/dev/null || true)"
    if [ -n "$row" ]; then out="${out}${tbl}:${row}"$'\n'; fi
  done
  printf '%s' "$out"
}

# map_version_to_file <phase> <version> — find the migration file for a version.
# phase is "pre" or "post"; version is an integer (zero-padding is matched).
map_version_to_file() {
  local phase="$1" version="$2"
  local dir="$REPO_ROOT/database/$DB_NAME/migrations/$phase"
  [ -d "$dir" ] || return 0
  local padded
  padded="$(printf '%04d' "$version" 2>/dev/null || echo "$version")"
  ls "$dir" 2>/dev/null | grep -E "^(${version}|${padded})_.*\.up\.sql$" | head -1
}

report_dirty_and_fail() {
  local report="$1"
  err ""
  err "================ MIGRATION GATE FAILED ================"
  err "A migration left the schema DIRTY. This is the same failure mode as the"
  err "production 'Dirty database version N. Fix and force version.' error."
  err ""
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    local tbl="${line%%:*}" ver="${line##*:}"
    local phase="pre"; [ "$tbl" = "schema_migrations_post" ] && phase="post"
    local file; file="$(map_version_to_file "$phase" "$ver")"
    err "  • table=${tbl} phase=${phase} dirty_version=${ver}"
    if [ -n "$file" ]; then
      err "    offending file: database/${DB_NAME}/migrations/${phase}/${file}"
    fi
  done <<< "$report"
  err ""
  err "Fix the migration SQL so it succeeds against this schema, then re-run."
  err "======================================================"
  exit 1
}

# --- preconditions -----------------------------------------------------------
have curl || true
[ -x "$ENGINE_SH" ] || die "container-engine.sh not executable: $ENGINE_SH"

info "[gate] resolving container engine + Postgres…"
if ! pg_ready; then
  if [ -f "${OCI_COMPOSE_FILE:-$WORKSPACE_ROOT/docker-compose.local.yml}" ]; then
    info "[gate] Postgres not up — starting it via compose…"
    compose up -d postgres >/dev/null 2>&1 || die "could not start postgres via compose"
  else
    # CI / standalone: no compose file present, run a bare Postgres container.
    info "[gate] no compose file — starting a standalone Postgres container '$PG_CONTAINER'…"
    "$(engine)" rm -f "$PG_CONTAINER" >/dev/null 2>&1 || true
    # POSTGRES_HOST_AUTH_METHOD=trust: the migrate CLI connects over TCP from the
    # host (not the container socket), so it must pass pg_hba. This is a throwaway
    # CI/standalone DB, so trust auth avoids scram/password-mismatch flakiness
    # (the SQLSTATE 28P01 "password authentication failed" class) entirely.
    "$(engine)" run -d --name "$PG_CONTAINER" \
      -e "POSTGRES_USER=$PG_SUPERUSER" \
      -e "POSTGRES_PASSWORD=$PG_PASSWORD" \
      -e POSTGRES_HOST_AUTH_METHOD=trust \
      -e POSTGRES_DB=postgres \
      -p "${OCI_PG_PORT:-5432}:5432" \
      postgres:16-alpine >/dev/null \
      || die "could not start standalone postgres container"
  fi
  wait_for 60 "postgres ready" pg_ready || die "postgres did not become ready"
fi

# --- (re)create target DB ----------------------------------------------------
if [ "$FRESH" = 1 ]; then
  warn "[gate] --fresh: dropping and recreating database '$DB_NAME'…"
  psql_q postgres "DROP DATABASE IF EXISTS \"$DB_NAME\" WITH (FORCE);" >/dev/null
fi
# Create the DB if it is missing (idempotent).
if [ "$(psql_q postgres "SELECT 1 FROM pg_database WHERE datname='$DB_NAME';")" != "1" ]; then
  info "[gate] creating database '$DB_NAME'…"
  psql_q postgres "CREATE DATABASE \"$DB_NAME\";" >/dev/null
fi

# --- load prod-like snapshot -------------------------------------------------
if [ -n "$SNAPSHOT" ]; then
  [ -f "$SNAPSHOT" ] || die "snapshot file not found: $SNAPSHOT"
  info "[gate] loading prod-like snapshot into '$DB_NAME': $SNAPSHOT"
  if ! psql_file "$DB_NAME" < "$SNAPSHOT"; then
    die "failed to load snapshot $SNAPSHOT"
  fi
  ok "[gate] snapshot loaded."
fi

# --- 1) migrate UP -----------------------------------------------------------
info "[gate] running migrations UP (this builds tools/migrate on first run)…"
if ! run_migrate -migrate; then
  warn "[gate] migrate UP returned a non-zero exit code."
  report="$(dirty_report)"
  [ -n "$report" ] && report_dirty_and_fail "$report"
  die "migrate UP failed (no dirty row detected — see output above)"
fi

# Defensive: even a 'successful' run can leave a dirty row in edge cases.
report="$(dirty_report)"
[ -n "$report" ] && report_dirty_and_fail "$report"
ok "[gate] migrate UP succeeded; no dirty schema."

# --- 2) UP -> DOWN -> UP idempotency cycle -----------------------------------
if [ "$IDEMPOTENCY" = 1 ]; then
  info "[gate] idempotency check: rolling back (pre=$PRE_STEPS post=$POST_STEPS) then re-applying…"
  if ! VCP_DB_PRE_ROLLBACK_STEPS="$PRE_STEPS" VCP_DB_POST_ROLLBACK_STEPS="$POST_STEPS" \
        run_migrate -rollback; then
    die "rollback failed — a down migration is broken or missing"
  fi
  report="$(dirty_report)"
  [ -n "$report" ] && report_dirty_and_fail "$report"

  info "[gate] re-applying migrations after rollback…"
  if ! run_migrate -migrate; then
    report="$(dirty_report)"
    [ -n "$report" ] && report_dirty_and_fail "$report"
    die "re-apply after rollback failed — migration is not idempotent"
  fi
  report="$(dirty_report)"
  [ -n "$report" ] && report_dirty_and_fail "$report"
  ok "[gate] idempotency check passed (up → down → up clean)."
fi

ok ""
ok "================ MIGRATION GATE PASSED ================"
ok "DB '$DB_NAME' migrated cleanly${SNAPSHOT:+ (snapshot: $SNAPSHOT)}; no dirty versions."
ok "======================================================"
