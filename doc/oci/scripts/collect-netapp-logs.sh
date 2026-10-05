#!/usr/bin/env bash 
#
# collect-netapp-logs.sh 
# 
# Collect vlm-worker, vcp-worker, and oci-proxy logs for a single workflow id, 
# 
# Usage: 
#   ./collect-netapp-logs.sh <workflow_id> [namespace] 
# 
#   <workflow_id>   Required. The workflow / opc-work-request id to grep for. 
#   [namespace]     Optional. Kubernetes namespace to collect logs from. 
#                   Defaults to $NAMESPACE env var, or "vcp" if unset. 
# 
# Output (workflow id is embedded in the dir/zip name for later identification): 
#   <YYYYmmdd_HHMMSS>_<workflow_id>_netapp_logs/ 
#     ├── vlm-worker.log 
#     ├── vcp-worker.log 
#     └── oci-proxy.log 
#   <YYYYmmdd_HHMMSS>_<workflow_id>_netapp_logs.zip 
#

set -euo pipefail 
WORKFLOW_ID="${1:-}" 
# Namespace precedence: positional arg > NAMESPACE env var > "vcp" default. 
NAMESPACE="${2:-${NAMESPACE:-vcp}}" 
if [ -z "$WORKFLOW_ID" ]; then 
  echo "usage: $0 <workflow_id> [namespace]" >&2 
  exit 1 
fi

# Pod-name match patterns for each service (robust to label / RC differences). 
VLM_PATTERN="vlm-worker" 
VCP_PATTERN="vcp-customer-worker" 
OCI_PATTERN="oci-proxy" 
# Sanitize the workflow id for safe use in a filename (keep alnum, dash, underscore). 
SAFE_WORKFLOW_ID="$(printf '%s' "$WORKFLOW_ID" | tr -c 'A-Za-z0-9_-' '_')" 
TIMESTAMP="$(date +%Y%m%d_%H%M%S)" 
OUT_DIR="${TIMESTAMP}_${SAFE_WORKFLOW_ID}_netapp_logs" 
mkdir -p "$OUT_DIR" 
echo "Workflow ID : $WORKFLOW_ID" 
echo "Namespace   : $NAMESPACE" 
echo "Output dir  : $OUT_DIR" 
echo

# collect_service <pattern> <output_file> 
# Dumps all matching pods' logs and filters for the workflow id. 
collect_service() { 
  local pattern="$1" 
  local out_file="$2" 
  local pods 
  pods="$(kubectl get pods -n "$NAMESPACE" -o name | grep -E "$pattern" || true)" 
  if [ -z "$pods" ]; then 
    echo "  ! no pods matching '$pattern' in namespace '$NAMESPACE'" 
    : > "$out_file" 
    return 0 
  fi 
  # Grep is allowed to find nothing (exit 1) without aborting the script. 
  { 
    for p in $pods; do 
      kubectl logs -n "$NAMESPACE" "$p" --all-containers --prefix --timestamps --tail=-1 || true 
    done 
  } | grep -i "$WORKFLOW_ID" > "$out_file" || true 
  echo "  -> $out_file ($(wc -l < "$out_file" | tr -d ' ') lines)" 
} 

echo "Collecting vlm-worker logs..." 
collect_service "$VLM_PATTERN" "$OUT_DIR/vlm-worker.log" 

echo "Collecting vcp-worker logs..." 
collect_service "$VCP_PATTERN" "$OUT_DIR/vcp-worker.log" 

echo "Collecting oci-proxy logs..." 
collect_service "$OCI_PATTERN" "$OUT_DIR/oci-proxy.log" 

echo 
echo "Creating zip archive..." 
ZIP_FILE="${OUT_DIR}.zip" 
zip -r -q "$ZIP_FILE" "$OUT_DIR"

# Remove the working directory; the zip is the only artifact we keep. 
rm -rf "$OUT_DIR" 
echo 
echo "Done." 
echo "  Archive   : $ZIP_FILE" 