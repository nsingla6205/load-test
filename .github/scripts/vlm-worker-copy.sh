#!/bin/bash
set -euo pipefail

SRC_GCS_PATH="$1"
DEST_FILE="$2"

if [ -z "$SRC_GCS_PATH" ] || [ -z "$DEST_FILE" ]; then
  echo "Usage: vlm-worker-copy.sh <src_gcs_path> <dest_file>"
  exit 1
fi

echo "📥 Copying VLM worker tar.gz from restricted bucket"
echo "Source: $SRC_GCS_PATH"
echo "Destination: $DEST_FILE"

rm -f "$DEST_FILE"
gcloud storage cp "$SRC_GCS_PATH" "$DEST_FILE"

test -s "$DEST_FILE"
echo "✅ VLM worker tar.gz copied successfully"
