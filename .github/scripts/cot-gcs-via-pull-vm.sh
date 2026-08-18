#!/usr/bin/env bash
# Read/copy objects in gs://cot-releases-public/ via the pull VM + SA impersonation.
# GitHub-hosted runners cannot reach this bucket directly (org policy / VPC-SC); see doc/design-doc/image-copy-pipeline-improvements.md
set -euo pipefail

CMD="${1:?usage: $0 stat|cp|cat|fetch <args>}"
IMPERSONATE_SA="${COT_IMPERSONATE_SA:-gcnv-vsa-image-sa@gcnv-vsa-prod.iam.gserviceaccount.com}"
: "${PULL_VM_NAME:?PULL_VM_NAME}"
: "${GCP_ZONE:?GCP_ZONE}"
: "${PULL_VM_PROJECT:?PULL_VM_PROJECT}"

vm_ssh() {
  local remote_cmd="$1"
  gcloud compute ssh "$PULL_VM_NAME" \
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
    remote=$(printf 'gsutil -i %q -q stat %q' "$IMPERSONATE_SA" "$uri")
    #remote=$(printf 'gcloud config set auth/impersonate_service_account %q >/dev/null 2>&1 && gsutil -q stat %q' "$IMPERSONATE_SA" "$uri")
    vm_ssh "$remote"
    ;;
  cp)
    src="${2:?gs src}"; dst="${3:?gs dst}"
    #remote=$(printf 'gcloud config set auth/impersonate_service_account %q >/dev/null 2>&1 && gsutil cp %q %q' "$IMPERSONATE_SA" "$src" "$dst")
    remote=$(printf 'gsutil -i %q cp %q %q' "$IMPERSONATE_SA" "$src" "$dst")
    vm_ssh "$remote"
    ;;
  cat)
    uri="${2:?gs uri}"
    #remote=$(printf 'gcloud config set auth/impersonate_service_account %q >/dev/null 2>&1 && gsutil cat %q' "$IMPERSONATE_SA" "$uri")
    remote=$(printf 'gsutil -i %q cat %q' "$IMPERSONATE_SA" "$uri")
    vm_ssh "$remote"
    ;;
  fetch)
    uri="${2:?gs uri}"; local_dest="${3:?local path on runner}"
    vm_tmp="/tmp/cot-fetch-${GITHUB_RUN_ID:-$$}-${RANDOM}"
    #r1=$(printf 'gcloud config set auth/impersonate_service_account %q >/dev/null 2>&1 && gsutil cp %q %q' "$IMPERSONATE_SA" "$uri" "$vm_tmp")
    r1=$(printf 'gsutil -i %q cp %q %q' "$IMPERSONATE_SA" "$uri" "$vm_tmp")
    vm_ssh "$r1"
    gcloud compute scp \
      --zone="$GCP_ZONE" \
      --project="$PULL_VM_PROJECT" \
      --impersonate-service-account="${COT_IMPERSONATE_SA}" \
      --quiet \
      "${PULL_VM_NAME}:${vm_tmp}" \
      "$local_dest"
    r2=$(printf 'rm -f %q' "$vm_tmp")
    vm_ssh "$r2" || true
    ;;
  *)
    echo "Usage: $0 stat <gs_uri> | cp <gs_src> <gs_dst> | cat <gs_uri> | fetch <gs_uri> <local_runner_path>" >&2
    exit 1
    ;;
esac
