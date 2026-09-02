# Sourced helper (not executable on its own).
#
# Retries a pull-VM SSH command through the two OS Login failure modes that are
# caused by CI state rather than by anything wrong with the promotion:
#
#   1. "Login profile size exceeds 32 KiB" - every runner registers a throwaway
#      key in the impersonated SA's profile and nothing removes them, so the
#      profile fills up and blocks all access. Prune, then retry.
#   2. "Permission denied (publickey)" - a key registered seconds earlier has
#      not reached the VM yet. Wait, then retry.
#
# Usage: retry_pull_vm_ssh <log_file> <command> [args...]
# <command> must tee its combined output into <log_file> and return the real
# exit status of the ssh/scp call.
retry_pull_vm_ssh() {
  local log_file="$1"
  shift

  local script_dir max_attempts=4 attempt rc=0
  script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

  for attempt in $(seq 1 "$max_attempts"); do
    rc=0
    "$@" || rc=$?
    [ "$rc" -eq 0 ] && return 0

    if grep -q 'Login profile size exceeds' "$log_file"; then
      echo "⚠️ OS Login profile is full; pruning stale keys (attempt ${attempt}/${max_attempts})" >&2
      bash "$script_dir/oslogin-prune-keys.sh" >&2 || true
      continue
    fi

    if grep -q 'Permission denied (publickey)' "$log_file"; then
      echo "⚠️ OS Login key not propagated to the VM yet; retrying in 15s (attempt ${attempt}/${max_attempts})" >&2
      sleep 15
      continue
    fi

    return "$rc"
  done

  return "$rc"
}
