#!/usr/bin/env bash
# Remove SSH public keys from the OS Login profile used for pull-VM access.
#
# Every GitHub runner is fresh, so `gcloud compute ssh` generates a throwaway
# key and registers it against the impersonated service account's OS Login
# profile. Nothing removes them, so the profile eventually reaches its 32 KiB
# cap and every subsequent SSH fails with:
#   FAILED_PRECONDITION: Login profile size exceeds 32 KiB.
#
# Every key in this profile belongs to a completed runner, so all of them are
# safe to drop; gcloud re-registers a fresh key on the next SSH.
set -uo pipefail

IMPERSONATE_SA="${COT_IMPERSONATE_SA:-gcnv-vsa-image-sa@gcnv-vsa-prod.iam.gserviceaccount.com}"

# `ssh-keys list` returns the profile's key map, so the OS Login fingerprint is
# the entry's `key` field (the default table labels it FINGERPRINT). Asking for
# `fingerprint` silently yields one blank line per key.
mapfile -t FINGERPRINTS < <(
  gcloud compute os-login ssh-keys list \
    --impersonate-service-account="$IMPERSONATE_SA" \
    --format='value(key)' 2>/dev/null | sed '/^$/d'
)

if [ "${#FINGERPRINTS[@]}" -eq 0 ]; then
  echo "ℹ️ No OS Login keys listed for $IMPERSONATE_SA"
  exit 0
fi

echo "🧹 Pruning ${#FINGERPRINTS[@]} OS Login key(s) from $IMPERSONATE_SA"
REMOVED=0
FIRST_ERROR=""
for FP in "${FINGERPRINTS[@]}"; do
  if ERR=$(gcloud compute os-login ssh-keys remove \
      --key="$FP" \
      --impersonate-service-account="$IMPERSONATE_SA" 2>&1 >/dev/null); then
    REMOVED=$((REMOVED + 1))
  elif [ -z "$FIRST_ERROR" ]; then
    FIRST_ERROR="$ERR"
  fi
done

echo "✅ Removed $REMOVED of ${#FINGERPRINTS[@]} OS Login key(s)"
if [ "$REMOVED" -eq 0 ]; then
  # Silently removing nothing here just re-runs the SSH into the same 32 KiB
  # wall, so make the real reason visible.
  echo "❌ Could not remove any key; first error was:" >&2
  echo "$FIRST_ERROR" >&2
  exit 1
fi
