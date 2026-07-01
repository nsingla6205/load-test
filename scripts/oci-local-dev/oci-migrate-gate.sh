#!/usr/bin/env bash
#
# oci-migrate-gate.sh — Phase 0 migration gate for OCI, IMAGE-DRIVEN.
#
# OCI has NO separate db-migrate job/image. The migrator is the oci-proxy image
# itself: on start, when RUN_MIGRATION_ON_START=true, oci-proxy runs
# db.Migrate() (pre-migrations -> GORM AutoMigrate -> post-migrations ->
# post-fixes) BEFORE it touches Temporal (see oci-proxy/app.go main()). So this
# gate uses the exact release image as the migrator — not `go run`, not a
# db-migrate image — which is what actually runs in the OCI cluster.
#
# It runs an oci-proxy image as a one-shot migrator against a local Postgres and
# detects the production "Dirty database version N. Fix and force version."
# failure class.
#
# Two modes:
#
#   single   migrate one image against a (optionally prod-like) DB:
#               oci-migrate-gate.sh --image iad.ocir.io/.../oci-proxy:<tag>
#               oci-migrate-gate.sh                 # uses pinned OCI_PROXY_IMAGE
#
#   upgrade  the real OCI upgrade path — bring the schema to the OLD release,
#            then apply the NEW release's migrations on top and detect dirty:
#               oci-migrate-gate.sh \
#                 --from-image iad.ocir.io/.../oci-proxy:<old> \
#                 --to-image   iad.ocir.io/.../oci-proxy:<new>
#
# Options:
#   --image IMG          migrator image for single mode (default: $OCI_PROXY_IMAGE)
#   --from-image IMG     OLD release image (upgrade mode)
#   --to-image IMG       NEW release image (upgrade mode)
#   --snapshot FILE      load a prod-like schema SQL before migrating (single mode)
#   --db NAME            target database name (default: vcp)
#   --fresh              drop+recreate the DB first (default in upgrade mode)
#   --timeout SECONDS    per-image migration wait budget (default: 240)
#   --keep               keep the DB after the run (default: drop in upgrade mode)
#
# Exit codes: 0 = pass, 1 = migration/dirty failure, 2 = setup/usage error,
#             3 = emulation/runtime crash — inconclusive (e.g. amd64-on-arm
#                 QEMU/Rosetta). version-compat.sh classifies rc=3 as ENV?.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$SCRIPT_DIR/lib.sh"

RELEASE_ENV_FILE="${OCI_RELEASE_ENV_FILE:-$WORKSPACE_ROOT/.oci-release.env}"

IMAGE=""
FROM_IMAGE=""
TO_IMAGE=""
SNAPSHOT=""
DB_NAME="vcp"
FRESH=0
KEEP=0
TIMEOUT=240
# A valid REGION_NUMBER_MAP key — LoadConfig() calls os.Exit(1) otherwise. The
# region does not affect migration SQL; us-ashburn-1 is NOT in the default map.
MIGRATE_REGION="${OCI_MIGRATE_REGION:-us-east1}"
# Go async-preemption crashes amd64 binaries under QEMU/Rosetta on Apple Silicon;
# asyncpreemptoff=1 is the documented mitigation. Override via OCI_MIGRATE_GODEBUG
# (set to empty to disable) — see golang/go#68485, #42700.
MIGRATOR_NAME="${OCI_MIGRATOR_CONTAINER:-vcp-oci-migrator}"

usage() { sed -n '2,40p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; }

while [ $# -gt 0 ]; do
  case "$1" in
    --image)      IMAGE="$2"; shift 2 ;;
    --from-image) FROM_IMAGE="$2"; shift 2 ;;
    --to-image)   TO_IMAGE="$2"; shift 2 ;;
    --snapshot)   SNAPSHOT="$2"; shift 2 ;;
    --db)         DB_NAME="$2"; shift 2 ;;
    --fresh)      FRESH=1; shift ;;
    --keep)       KEEP=1; shift ;;
    --timeout)    TIMEOUT="$2"; shift 2 ;;
    -h|--help)    usage; exit 0 ;;
    *) die "unknown arg '$1' (see --help)" ;;
  esac
done

# Resolve the default single-mode image from the pinned release if not given.
load_pinned_proxy_image() {
  [ -f "$RELEASE_ENV_FILE" ] || return 1
  # shellcheck disable=SC1090
  set -a; . "$RELEASE_ENV_FILE"; set +a
  [ -n "${OCI_PROXY_IMAGE:-}" ] || return 1
  printf '%s' "$OCI_PROXY_IMAGE"
}

# --- dirty detection ---------------------------------------------------------
# dirty_report — print "<table>:<version>" lines for any dirty migration row.
dirty_report() {
  local out="" tbl row
  for tbl in schema_migrations schema_migrations_post; do
    row="$(psql_q "$DB_NAME" \
      "SELECT version FROM ${tbl} WHERE dirty = true LIMIT 1;" 2>/dev/null || true)"
    [ -n "$row" ] && out="${out}${tbl}:${row}"$'\n'
  done
  printf '%s' "$out"
}

map_version_to_file() {
  local phase="$1" version="$2"
  local dir="$REPO_ROOT/database/$DB_NAME/migrations/$phase"
  [ -d "$dir" ] || return 0
  local padded; padded="$(printf '%04d' "$version" 2>/dev/null || echo "$version")"
  ls "$dir" 2>/dev/null | grep -E "^(${version}|${padded})_.*\.up\.sql$" | head -1
}

report_dirty_and_fail() {
  local report="$1"
  err ""
  err "================ OCI MIGRATION GATE FAILED ================"
  err "An image left the schema DIRTY — the same failure mode as the production"
  err "'Dirty database version N. Fix and force version.' error."
  err ""
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    local tbl="${line%%:*}" ver="${line##*:}"
    local phase="pre"; [ "$tbl" = "schema_migrations_post" ] && phase="post"
    local file; file="$(map_version_to_file "$phase" "$ver")"
    err "  • table=${tbl} phase=${phase} dirty_version=${ver}"
    [ -n "$file" ] && err "    likely file: database/${DB_NAME}/migrations/${phase}/${file}"
  done <<< "$report"
  err ""
  err "Fix the migration SQL so it succeeds against this schema, rebuild the"
  err "image, then re-run the gate."
  err "=========================================================="
  exit 1
}

# --- one-shot image migrator -------------------------------------------------
# run_image_migrate <image> <label> — run the image as a migrator, wait for the
# migration phase to finish, then classify. Returns 0 on clean migrate, 1 on a
# migration/dirty failure. Never leaves the migrator container behind.
run_image_migrate() {
  local img="$1" label="$2"
  info "[gate] (${label}) pulling migrator image: $img"
  "$(engine)" pull --platform linux/amd64 "$img" >/dev/null 2>&1 \
    || warn "[gate] pull failed/aborted; relying on a local copy if present"

  "$(engine)" rm -f "$MIGRATOR_NAME" >/dev/null 2>&1 || true
  info "[gate] (${label}) running migrations via the image (RUN_MIGRATION_ON_START=true)…"
  # Share the Postgres container's network namespace so DB_HOST=127.0.0.1 works
  # on either engine without resolving a compose network name. Temporal is
  # pointed at a dead port so the proxy exits quickly AFTER it has migrated.
  "$(engine)" run -d --name "$MIGRATOR_NAME" --platform linux/amd64 \
    --network "container:$PG_CONTAINER" \
    -e DB_TYPE=postgres \
    -e DB_HOST=127.0.0.1 \
    -e DB_PORT=5432 \
    -e DB_USER="$PG_SUPERUSER" \
    -e DB_PASSWORD="$PG_PASSWORD" \
    -e DB_NAME="$DB_NAME" \
    -e DB_SSL_MODE=disable \
    -e DB_ADMIN_USER="$PG_SUPERUSER" \
    -e DB_ADMIN_PASSWORD="$PG_PASSWORD" \
    -e RUN_MIGRATION_ON_START=true \
    -e HYPERSCALER=oci \
    -e LOCAL_REGION="$MIGRATE_REGION" \
    -e TEMPORAL_ADDRESS=127.0.0.1:1 \
    -e GODEBUG="${OCI_MIGRATE_GODEBUG:-asyncpreemptoff=1}" \
    "$img" >/dev/null \
    || die "could not start migrator container from $img"

  # Wait for a terminal migration marker in the logs (stable Go log strings from
  # database/drivers/postgres/migrate.go and oci-proxy/app.go).
  local deadline=$(( $(date +%s) + TIMEOUT )) logs="" outcome="timeout"
  local ok_re='Successfully executed post-migrations'
  local fail_re='Failed to initialize database|failed to run (pre|post)-migrations|migration SQL files failed|automigrate failed|Dirty database version'
  # amd64 Go binaries can crash in the runtime under emulation (QEMU) or Rosetta
  # on Apple Silicon — this is an ENVIRONMENT problem, not a migration defect.
  # (GODEBUG=asyncpreemptoff=1 is injected above to mitigate the Rosetta/QEMU
  # async-preemption crash; these patterns catch the cases it can't.)
  local emu_re='lfstack\.push|invalid packing|taggedPointerPack|fatal error: (runtime|lfstack)|registerUniqueMapCleanup|illegal instruction|EXC_BAD_INSTRUCTION|code fragment does not contain'
  while [ "$(date +%s)" -lt "$deadline" ]; do
    logs="$("$(engine)" logs "$MIGRATOR_NAME" 2>&1 || true)"
    if printf '%s' "$logs" | grep -Eq "$emu_re";  then outcome="emucrash"; break; fi
    if printf '%s' "$logs" | grep -Eq "$fail_re"; then outcome="fail"; break; fi
    if printf '%s' "$logs" | grep -Eq "$ok_re";   then outcome="ok";   break; fi
    # Container exited before any marker (e.g. crash) — stop waiting.
    if [ "$("$(engine)" inspect -f '{{.State.Running}}' "$MIGRATOR_NAME" 2>/dev/null || echo false)" != "true" ]; then
      outcome="exited"; break
    fi
    sleep 3
  done

  # Snapshot logs + tear the migrator down (its DB writes persist in Postgres).
  logs="$("$(engine)" logs "$MIGRATOR_NAME" 2>&1 || true)"
  "$(engine)" rm -f "$MIGRATOR_NAME" >/dev/null 2>&1 || true

  # A dirty row is authoritative regardless of what the logs said.
  local report; report="$(dirty_report)"
  if [ -n "$report" ]; then
    printf '%s\n' "$logs" | tail -n 25 >&2
    report_dirty_and_fail "$report"
  fi

  # Catch an emulation crash that surfaced only in the final log snapshot.
  if [ "$outcome" = exited ] && printf '%s' "$logs" | grep -Eq "$emu_re"; then
    outcome="emucrash"
  fi

  case "$outcome" in
    ok)
      ok "[gate] (${label}) migrations applied cleanly; no dirty schema."
      return 0 ;;
    emucrash)
      err ""
      err "[gate] (${label}) the image CRASHED IN THE Go RUNTIME under emulation —"
      err "        this is an ENVIRONMENT issue (amd64 image on a non-amd64 host via"
      err "        QEMU), NOT a migration defect. The migration never ran."
      err "        Run the gate on a native linux/amd64 host (CI runner / OKE amd64"
      err "        node / amd64 VM). Last log lines:"
      printf '%s\n' "$logs" | tail -n 8 >&2
      return 3 ;;
    fail|exited)
      err "[gate] (${label}) migration FAILED — last log lines:"
      printf '%s\n' "$logs" | tail -n 30 >&2
      return 1 ;;
    timeout)
      err "[gate] (${label}) timed out after ${TIMEOUT}s with no migration result."
      err "        (increase --timeout, or the image may be hanging before migrate)"
      printf '%s\n' "$logs" | tail -n 30 >&2
      return 1 ;;
  esac
}

# --- preconditions -----------------------------------------------------------
[ -x "$ENGINE_SH" ] || die "container-engine.sh not executable: $ENGINE_SH"

# Decide mode.
MODE="single"
if [ -n "$FROM_IMAGE" ] || [ -n "$TO_IMAGE" ]; then
  MODE="upgrade"
  [ -n "$FROM_IMAGE" ] || die "upgrade mode needs --from-image"
  [ -n "$TO_IMAGE" ]   || die "upgrade mode needs --to-image"
  FRESH=1   # upgrade always starts from a clean DB
fi
if [ "$MODE" = single ] && [ -z "$IMAGE" ]; then
  IMAGE="$(load_pinned_proxy_image)" \
    || die "no --image given and no pinned release (run: release.sh apply <manifest.json>)"
  info "[gate] using pinned OCI_PROXY_IMAGE: $IMAGE"
fi

# start_standalone_pg — run a bare Postgres container (no compose needed).
start_standalone_pg() {
  info "[gate] starting standalone Postgres '$PG_CONTAINER'…"
  "$(engine)" rm -f "$PG_CONTAINER" >/dev/null 2>&1 || true
  "$(engine)" run -d --name "$PG_CONTAINER" \
    -e "POSTGRES_USER=$PG_SUPERUSER" \
    -e "POSTGRES_PASSWORD=$PG_PASSWORD" \
    -e POSTGRES_DB=postgres \
    -p "${OCI_PG_PORT:-5432}:5432" \
    postgres:16-alpine >/dev/null \
    || die "could not start standalone postgres container"
}

info "[gate] resolving container engine + Postgres…"
if ! pg_ready; then
  started=0
  if [ -f "${OCI_COMPOSE_FILE:-$WORKSPACE_ROOT/docker-compose.local.yml}" ]; then
    info "[gate] Postgres not up — trying compose…"
    if compose up -d postgres >/dev/null 2>&1; then
      started=1
    else
      warn "[gate] compose unavailable (no provider?) — falling back to a standalone Postgres"
    fi
  fi
  [ "$started" = 1 ] || start_standalone_pg
  wait_for 60 "postgres ready" pg_ready || die "postgres did not become ready"
fi

# --- (re)create target DB ----------------------------------------------------
if [ "$FRESH" = 1 ]; then
  warn "[gate] --fresh: dropping and recreating database '$DB_NAME'…"
  psql_q postgres "DROP DATABASE IF EXISTS \"$DB_NAME\" WITH (FORCE);" >/dev/null
fi
if [ "$(psql_q postgres "SELECT 1 FROM pg_database WHERE datname='$DB_NAME';")" != "1" ]; then
  info "[gate] creating database '$DB_NAME'…"
  psql_q postgres "CREATE DATABASE \"$DB_NAME\";" >/dev/null
fi

# --- run ---------------------------------------------------------------------
# run_or_exit <image> <label> <fail-message> — run a migrator and translate its
# return code: 0 ok, 3 emulation crash (pass through as-is), else the given msg.
run_or_exit() {
  local img="$1" label="$2" msg="$3" rc=0
  run_image_migrate "$img" "$label" || rc=$?
  case "$rc" in
    0) return 0 ;;
    3) exit 3 ;;   # emulation crash already explained; do not relabel
    *) err ""; err "$msg"; exit 1 ;;
  esac
}

if [ "$MODE" = upgrade ]; then
  info "[gate] UPGRADE: $FROM_IMAGE  →  $TO_IMAGE"
  run_or_exit "$FROM_IMAGE" "baseline/old" \
    "the BASELINE (old) release failed to migrate a fresh DB — fix the baseline first; the upgrade test needs a clean starting point"
  run_or_exit "$TO_IMAGE" "candidate/new" \
    "The CANDIDATE (new) release failed to migrate on top of the old release's schema. This is the upgrade that would run in OCI — block the build."
else
  if [ -n "$SNAPSHOT" ]; then
    [ -f "$SNAPSHOT" ] || die "snapshot file not found: $SNAPSHOT"
    info "[gate] loading prod-like snapshot into '$DB_NAME': $SNAPSHOT"
    psql_file "$DB_NAME" < "$SNAPSHOT" || die "failed to load snapshot $SNAPSHOT"
    ok "[gate] snapshot loaded."
  fi
  run_or_exit "$IMAGE" "single" "migration failed"
fi

# --- cleanup -----------------------------------------------------------------
if [ "$MODE" = upgrade ] && [ "$KEEP" != 1 ]; then
  psql_q postgres "DROP DATABASE IF EXISTS \"$DB_NAME\" WITH (FORCE);" >/dev/null 2>&1 || true
fi

ok ""
ok "================ OCI MIGRATION GATE PASSED ================"
if [ "$MODE" = upgrade ]; then
  ok "Upgrade clean: old → new applied with no dirty versions."
else
  ok "DB '$DB_NAME' migrated cleanly by the release image; no dirty versions."
fi
ok "=========================================================="
