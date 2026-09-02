#!/usr/bin/env bash
# Read/copy objects in gs://cot-releases-public/ via the pull VM + SA impersonation.
# GitHub-hosted runners cannot reach this bucket directly (org policy / VPC-SC); see doc/design-doc/image-copy-pipeline-improvements.md
set -euo pipefail

CMD="${1:?usage: $0 stat|cp|cat|fetch <args>}"
IMPERSONATE_SA="${COT_IMPERSONATE_SA:-gcnv-vsa-image-sa@gcnv-vsa-prod.iam.gserviceaccount.com}"
: "${PULL_VM_NAME:?PULL_VM_NAME}"
: "${GCP_ZONE:?GCP_ZONE}"
: "${PULL_VM_PROJECT:?PULL_VM_PROJECT}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# shellcheck source=/dev/null
source "$SCRIPT_DIR/oslogin-ssh-retry.sh"

# Only stderr is captured, so `cat` output reaches callers unpolluted.
_pull_vm_attempt() {
  local rc=0
  "$@" 2>"$PULL_VM_ERRFILE" || rc=$?
  cat "$PULL_VM_ERRFILE" >&2
  return "$rc"
}

run_with_oslogin_recovery() {
  local rc=0
  PULL_VM_ERRFILE=$(mktemp)
  retry_pull_vm_ssh "$PULL_VM_ERRFILE" _pull_vm_attempt "$@" || rc=$?
  rm -f "$PULL_VM_ERRFILE"
  return "$rc"
}

vm_ssh() {
  local remote_cmd="$1"
  run_with_oslogin_recovery gcloud compute ssh "$PULL_VM_NAME" \
    --zone="$GCP_ZONE" \
    --project="$PULL_VM_PROJECT" \
    --impersonate-service-account="${COT_IMPERSONATE_SA}" \
    --tunnel-through-iap \
    --quiet \
    --command="$remote_cmd"
}

case "$CMD" in
  stat)
    uri="${2:?gs uri}"
    # gsutil -i writes impersonation state under the executing user's config
    # directory. The pull VM restricts that path for the SSH user, which caused
    # an opaque "OSError: Permission denied." Run it in the same elevated
    # context as the proven debug-mode COT reads.
    remote=$(printf 'sudo gsutil -i %q -q stat %q' "$IMPERSONATE_SA" "$uri")
    vm_ssh "$remote"
    ;;
  cp)
    src="${2:?gs src}"; dst="${3:?gs dst}"
    remote=$(printf 'sudo gsutil -i %q cp %q %q' "$IMPERSONATE_SA" "$src" "$dst")
    vm_ssh "$remote"
    ;;
  cat)
    uri="${2:?gs uri}"
    remote=$(printf 'sudo gsutil -i %q cat %q' "$IMPERSONATE_SA" "$uri")
    vm_ssh "$remote"
    ;;
  fetch)
    uri="${2:?gs uri}"; local_dest="${3:?local path on runner}"
    vm_tmp="/tmp/cot-fetch-${GITHUB_RUN_ID:-$$}-${RANDOM}"
    # chmod is required because sudo creates the temporary file as root and the
    # subsequent gcloud compute scp runs as the SSH user.
    r1=$(printf 'sudo gsutil -i %q cp %q %q && sudo chmod 0644 %q' "$IMPERSONATE_SA" "$uri" "$vm_tmp" "$vm_tmp")
    vm_ssh "$r1"
    run_with_oslogin_recovery gcloud compute scp \
      --zone="$GCP_ZONE" \
      --project="$PULL_VM_PROJECT" \
      --impersonate-service-account="${COT_IMPERSONATE_SA}" \
      --quiet \
      "${PULL_VM_NAME}:${vm_tmp}" \
      "$local_dest"
    r2=$(printf 'sudo rm -f %q' "$vm_tmp")
    vm_ssh "$r2" || true
    ;;
  *)
    echo "Usage: $0 stat <gs_uri> | cp <gs_src> <gs_dst> | cat <gs_uri> | fetch <gs_uri> <local_runner_path>" >&2
    exit 1
    ;;
esac
