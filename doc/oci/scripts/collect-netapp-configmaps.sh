#!/usr/bin/env bash 
# 
# collect-netapp-configmaps.sh 
# 
# Collect all the ConfigMaps for vlm-worker, vcp-worker, and oci-proxy 
# 
# Usage: 
#   ./collect-netapp-configmaps.sh [namespace] 
# 
#   [namespace]   Optional. Kubernetes namespace to read from. Default: vcp. 
# 
# Output (timestamp is embedded in the dir/zip name for later identification): 
#   <YYYYmmdd_HHMMSS>_netapp_configmaps/ 
#     ├── vlm-worker/ 
#     │   └── <configmap-name>.yaml 
#     ├── vcp-worker/ 
#     │   └── <configmap-name>.yaml 
#     └── oci-proxy/ 
#         └── <configmap-name>.yaml 
#   <YYYYmmdd_HHMMSS>_netapp_configmaps.zip 
#

set -euo pipefail 
# Namespace precedence: positional arg > NAMESPACE env var > "vcp" default. 
NAMESPACE="${1:-${NAMESPACE:-vcp}}" 
# ConfigMap-name match patterns for each service (robust to version / RC differences). 
VLM_PATTERN="vlm-worker" 
VCP_PATTERN="vcp-customer-worker" 
OCI_PATTERN="oci-proxy" 
TIMESTAMP="$(date +%Y%m%d_%H%M%S)" 
OUT_DIR="${TIMESTAMP}_netapp_configmaps" 

mkdir -p "$OUT_DIR" 
echo "Context     : $(kubectl config current-context 2>/dev/null || echo unknown)" 
echo "Namespace   : $NAMESPACE" 
echo "Output dir  : $OUT_DIR" 
echo 

# collect_service <pattern> <service_name> 
# Dumps each matching ConfigMap as its own YAML file under OUT_DIR/<service_name>/. 
collect_service() { 
  local pattern="$1" 
  local service="$2" 
  local service_dir="$OUT_DIR/$service" 
  local cms 
  cms="$(kubectl get configmaps -n "$NAMESPACE" -o name 2>/dev/null | grep -E "$pattern" || true)" 
  if [ -z "$cms" ]; then 
    echo "  ! no configmaps matching '$pattern' in namespace '$NAMESPACE'" 
    return 0 
  fi 
  mkdir -p "$service_dir" 
  local count=0 
  for cm in $cms; do 
    # cm looks like 'configmap/<name>'; strip the resource-type prefix for the filename. 
    local name="${cm#configmap/}" 
    if kubectl get -n "$NAMESPACE" "$cm" -o yaml > "$service_dir/${name}.yaml" 2>/dev/null; then 
      count=$((count + 1)) 
      echo "  -> $service_dir/${name}.yaml" 
    else 
      echo "  ! failed to read $cm" 
      rm -f "$service_dir/${name}.yaml" 
    fi 
  done 
  echo "  ($count configmap(s) for $service)" 
} 

echo "Collecting vlm-worker configmaps..." 
collect_service "$VLM_PATTERN" "vlm-worker" 

echo "Collecting vcp-worker configmaps..." 
collect_service "$VCP_PATTERN" "vcp-worker" 

echo "Collecting oci-proxy configmaps..." 
collect_service "$OCI_PATTERN" "oci-proxy" 
echo 

echo "Creating zip archive..." 
ZIP_FILE="${OUT_DIR}.zip" 
zip -r -q "$ZIP_FILE" "$OUT_DIR" 

# Remove the working directory; the zip is the only artifact we keep. 
rm -rf "$OUT_DIR" 
echo 
echo "Done." 
echo "  Archive   : $ZIP_FILE" 