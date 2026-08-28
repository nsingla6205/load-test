#!/bin/bash
# Copy SRC to DEST unless DEST already has the same crc32c.
# Does not delete compute images. Overwrites a destination GCS object only
# when the source content differs (poisoned/stale artifact recovery).
#
# Usage: gcs-copy-if-different.sh <src_uri> <dest_uri>
# Writes status=identical|success to $GITHUB_OUTPUT when set
# (consumed as steps.<id>.outputs.status).

set -euo pipefail

SRC="${1:-}"
DEST="${2:-}"

if [ -z "$SRC" ] || [ -z "$DEST" ]; then
  echo "❌ Usage: gcs-copy-if-different.sh <src_uri> <dest_uri>"
  exit 1
fi

object_crc32c() {
  # gsutil ls -L prints "Hash (crc32c): <base64>"
  gsutil ls -L "$1" 2>/dev/null | awk '/Hash \(crc32c\):/ {print $3; exit}'
}

dest_exists=false
if gsutil -q stat "$DEST" 2>/dev/null; then
  dest_exists=true
fi

if [ "$dest_exists" = "true" ]; then
  SRC_CRC="$(object_crc32c "$SRC")"
  DEST_CRC="$(object_crc32c "$DEST")"
  echo "Source crc32c:      ${SRC_CRC:-<unknown>}"
  echo "Destination crc32c: ${DEST_CRC:-<unknown>}"
  if [ -n "$SRC_CRC" ] && [ "$SRC_CRC" = "$DEST_CRC" ]; then
    echo "✅ Destination already matches source; not overwriting $DEST"
    if [ -n "${GITHUB_OUTPUT:-}" ]; then
      echo "status=identical" >> "$GITHUB_OUTPUT"
    fi
    exit 0
  fi
  echo "⚠️ Destination exists but content differs; replacing $DEST from $SRC"
else
  echo "ℹ️ Destination missing; copying $SRC -> $DEST"
fi

gsutil cp "$SRC" "$DEST"
echo "✅ Copied $SRC -> $DEST"
if [ -n "${GITHUB_OUTPUT:-}" ]; then
  echo "status=success" >> "$GITHUB_OUTPUT"
fi
