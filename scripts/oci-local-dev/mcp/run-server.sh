#!/usr/bin/env bash
#
# run-server.sh — portable launcher for the oci-infra MCP server.
#
# Resolves its OWN location (not the Cursor ${workspaceFolder}), so it works no
# matter where the repo is cloned or which folder is opened as the workspace.
# On first run it creates a local .venv and installs requirements.txt; on later
# runs it just execs the server. All bootstrap logs go to stderr so they never
# corrupt the stdio JSON-RPC stream on stdout.
#
# Wire it into Cursor with a relative-to-workspace path, e.g.:
#   "command": "${workspaceFolder}/scripts/oci-local-dev/mcp/run-server.sh"

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VENV="$SCRIPT_DIR/.venv"
REQ="$SCRIPT_DIR/requirements.txt"
PYBIN="$VENV/bin/python"

log() { printf '[mcp] %s\n' "$*" >&2; }

pick_python() {
  local p
  for p in python3 python3.13 python3.12 python3.11 python3.10 python; do
    if command -v "$p" >/dev/null 2>&1; then echo "$p"; return 0; fi
  done
  return 1
}

needs_bootstrap=0
if [ ! -x "$PYBIN" ]; then
  needs_bootstrap=1
elif ! "$PYBIN" -c "import mcp" >/dev/null 2>&1; then
  needs_bootstrap=1
fi

if [ "$needs_bootstrap" = 1 ]; then
  if [ ! -x "$PYBIN" ]; then
    base="$(pick_python)" || {
      log "ERROR: no python3 found. Install Python 3.10+ and retry."; exit 1; }
    log "creating venv at $VENV (first run)…"
    "$base" -m venv "$VENV"
  fi
  log "installing dependencies from requirements.txt…"
  "$PYBIN" -m pip install --quiet --upgrade pip >&2 || true
  "$PYBIN" -m pip install --quiet -r "$REQ" >&2
fi

exec "$PYBIN" "$SCRIPT_DIR/server.py" "$@"
