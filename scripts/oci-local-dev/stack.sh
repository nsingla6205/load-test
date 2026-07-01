#!/usr/bin/env bash
#
# stack.sh — bring the OCI local stack up/down at a pinned release.
#
# Wraps compose with the release env (from release.sh) so the EXACT images
# "shared to OCI" are installed. The base overlay is docker-compose.local.yml
# (postgres + temporal + vlm-worker); --crud adds docker-compose.crud.yml
# (oci-proxy + vcp-worker + ontap-sim) needed for pool/SVM CRUD smoke.
#
# Subcommands:
#   up [--crud]              Start the stack (base, or base+crud with --crud)
#   down [--crud] [--wipe]   Stop; --wipe also removes volumes (fresh DB)
#   ps [--crud]              Show container status (--crud includes CRUD services)
#   logs <service> [-f]      Tail a service's logs
#
# Exit codes: 0 ok, 2 usage/setup error.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$SCRIPT_DIR/lib.sh"

BASE_FILE="${OCI_COMPOSE_FILE:-$WORKSPACE_ROOT/docker-compose.local.yml}"
CRUD_FILE="${OCI_CRUD_COMPOSE_FILE:-$WORKSPACE_ROOT/docker-compose.crud.yml}"
RELEASE_ENV_FILE="${OCI_RELEASE_ENV_FILE:-$WORKSPACE_ROOT/.oci-release.env}"

usage() { sed -n '2,22p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; }

# Load the pinned release into the environment so compose interpolates image refs.
load_release() {
  if [ -f "$RELEASE_ENV_FILE" ]; then
    # shellcheck disable=SC1090
    set -a; . "$RELEASE_ENV_FILE"; set +a
  fi
}

# compose_files — echo the -f flags for the requested overlay set.
compose_files() {
  local with_crud="$1"
  [ -f "$BASE_FILE" ] || die "compose file not found: $BASE_FILE"
  printf -- '-f\n%s\n' "$BASE_FILE"
  if [ "$with_crud" = 1 ]; then
    [ -f "$CRUD_FILE" ] || die "crud compose file not found: $CRUD_FILE"
    printf -- '-f\n%s\n' "$CRUD_FILE"
  fi
}

# run_compose <with_crud> <compose-args...>
run_compose() {
  local with_crud="$1"; shift
  local e; e="$(engine)"
  local -a files=(); mapfile -t files < <(compose_files "$with_crud")
  "$ENGINE_SH" ensure >/dev/null 2>&1 || true
  "$e" compose "${files[@]}" "$@"
}

cmd_up() {
  local crud=0
  [ "${1:-}" = "--crud" ] && crud=1
  load_release
  if [ "$crud" = 1 ] && [ ! -f "$RELEASE_ENV_FILE" ]; then
    die "crud overlay needs a pinned release. Run: release.sh apply <manifest.json>"
  fi
  info "[stack] starting${crud:+ base+crud} stack…"
  run_compose "$crud" up -d
  ok "[stack] up. Postgres :5432  Temporal-UI :8088${crud:+  oci-proxy :8080  ontap-sim :9444}"
}

cmd_down() {
  local crud=0 wipe=0
  while [ $# -gt 0 ]; do
    case "$1" in
      --crud) crud=1; shift ;;
      --wipe) wipe=1; shift ;;
      *) die "unknown arg '$1'" ;;
    esac
  done
  load_release
  local -a args=(down)
  [ "$wipe" = 1 ] && args+=(--volumes)
  run_compose "$crud" "${args[@]}"
  ok "[stack] down${wipe:+ (volumes removed)}."
}

cmd_ps() {
  local crud=0
  [ "${1:-}" = "--crud" ] && crud=1
  load_release
  run_compose "$crud" ps
}
cmd_logs() {
  local svc="${1:-}"; shift || true
  [ -n "$svc" ] || die "usage: stack.sh logs <service> [-f]"
  load_release
  # The crud services live in the crud overlay; include it so names resolve.
  run_compose 1 logs "$@" "$svc"
}

case "${1:-}" in
  up)   shift; cmd_up "$@" ;;
  down) shift; cmd_down "$@" ;;
  ps|status) shift; cmd_ps "$@" ;;
  logs) shift; cmd_logs "$@" ;;
  -h|--help|"") usage ;;
  *) die "unknown subcommand '$1' (use: up | down | ps | logs)" ;;
esac
