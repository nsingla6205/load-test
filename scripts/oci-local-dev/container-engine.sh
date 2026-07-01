#!/usr/bin/env bash
#
# container-engine.sh — resolve and drive Docker for the OCI local-dev stack.
#
# Why this exists: Docker Desktop on macOS sometimes installs without
# symlinking the `docker` CLI onto PATH (it only symlinks docker-compose and a
# couple of credential helpers). This script finds Docker Desktop's CLI
# wherever it lives and drives `compose`/`exec` against docker-compose.local.yml
# so the /setup-oci-local flow works the same by hand, in CI, or via the agent.
#
# Subcommands:
#   detect              Print the selected ENGINE / COMPOSE / COMPOSE_FILE. Read-only.
#   ensure              Verify Docker is usable. If the daemon is reachable it
#                       does nothing; if the CLI is present but the daemon is
#                       down it tells you to start Docker Desktop. Idempotent.
#   engine              Print just the engine binary name (docker).
#   compose [args...]   Run "docker compose -f <compose-file> [args...]".
#   exec    [args...]   Run "docker exec [args...]".
#
# The compose file defaults to <workspace-root>/docker-compose.local.yml, where
# <workspace-root> is the parent of the vsa-control-plane repo that holds this
# script. Override with OCI_COMPOSE_FILE.
#
# This script NEVER reads, prints, or logs secrets. It only selects and forwards
# to Docker.

set -euo pipefail

log()  { printf '%s\n' "$*" >&2; }
die()  { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"          # vsa-control-plane/
WORKSPACE_ROOT="$(cd "$REPO_ROOT/.." && pwd)"          # parent of the repo
COMPOSE_FILE="${OCI_COMPOSE_FILE:-$WORKSPACE_ROOT/docker-compose.local.yml}"

# Docker Desktop on macOS sometimes does not symlink its `docker` CLI onto PATH.
# Add it so `docker` works for the harness without a manual install step.
ensure_docker_on_path() {
  command -v docker >/dev/null 2>&1 && return 0
  local d
  for d in /usr/local/bin /opt/homebrew/bin \
           /Applications/Docker.app/Contents/Resources/bin; do
    if [ -x "$d/docker" ]; then PATH="$d:$PATH"; export PATH; return 0; fi
  done
  return 1
}
ensure_docker_on_path || true

docker_ok() { have docker && docker info >/dev/null 2>&1; }

# Resolve the engine binary name. Returns non-zero if Docker is not installed.
resolve_engine() {
  if have docker; then echo docker; return 0; fi
  return 1
}

# Resolve the compose command string ("docker compose" or "docker-compose").
resolve_compose() {
  if docker compose version >/dev/null 2>&1; then echo "docker compose"; return 0; fi
  if have docker-compose;                    then echo "docker-compose"; return 0; fi
  return 1
}

cmd_ensure() {
  if docker_ok; then
    log "Container engine: docker (daemon reachable) — nothing to do."
  elif have docker; then
    die "Docker CLI found but the daemon is not reachable. Start Docker Desktop
  (open -a Docker) and wait for it to report 'running', then re-run."
  else
    die "Docker is not installed. Install Docker Desktop (https://www.docker.com
  or 'brew install --cask docker'), start it, then re-run."
  fi

  local c
  c="$(resolve_compose)" || die "No compose provider found. Docker Desktop ships
  'docker compose'; ensure Docker Desktop is fully installed."
  log "OK: engine='docker' compose='$c' compose_file='$COMPOSE_FILE'"
}

cmd_detect() {
  resolve_engine >/dev/null || die "Docker not found. Run: $0 ensure"
  local c
  c="$(resolve_compose 2>/dev/null || true)"
  printf 'ENGINE=%s\n' "docker"
  printf 'COMPOSE=%s\n' "${c:-<none — run: $0 ensure>}"
  printf 'COMPOSE_FILE=%s\n' "$COMPOSE_FILE"
  if ! docker_ok; then
    log "WARNING: Docker daemon is NOT reachable. Start Docker Desktop (open -a Docker)."
  fi
}

cmd_engine() { resolve_engine || die "Docker not found. Run: $0 ensure"; }

cmd_compose() {
  resolve_engine >/dev/null || die "Docker not found. Run: $0 ensure"
  local c
  c="$(resolve_compose)" || die "No compose provider. Run: $0 ensure"
  [ -f "$COMPOSE_FILE" ] || die "Compose file not found: $COMPOSE_FILE"
  # shellcheck disable=SC2086  # $c is intentionally word-split ("docker compose").
  exec $c -f "$COMPOSE_FILE" "$@"
}

cmd_exec() {
  resolve_engine >/dev/null || die "Docker not found. Run: $0 ensure"
  exec docker exec "$@"
}

main() {
  local sub="${1:-detect}"
  [ "$#" -gt 0 ] && shift || true
  case "$sub" in
    detect)  cmd_detect "$@" ;;
    ensure)  cmd_ensure "$@" ;;
    engine)  cmd_engine "$@" ;;
    compose) cmd_compose "$@" ;;
    exec)    cmd_exec "$@" ;;
    -h|--help|help)
      sed -n '2,30p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
      ;;
    *) die "Unknown subcommand '$sub'. Use: detect | ensure | engine | compose | exec" ;;
  esac
}

main "$@"
