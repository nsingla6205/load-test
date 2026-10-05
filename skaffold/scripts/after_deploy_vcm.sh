#!/bin/bash
# Patch regional configuration into the shared vcm-env ConfigMap, write local
# credentials into the vcm-credentials Secret, and restart the VCM deployments.
#
# Required env (prompted if unset):
#   VCM_REGION                 - au-se1 | us-c1 | us-e4
#   VSA_NODE_PASSWORD          - ONTAP node password
#   VCM_LOCAL_ONTAP_USERNAME   - local ONTAP admin username
#   VCM_LOCAL_ONTAP_PASSWORD   - local ONTAP admin password
#
# ENV=local only checks that the local ONTAP variables are non-empty
# (utils/env.ValidateVCMEnvironmentVariables). Manifest placeholders such as
# <<VCM_LOCAL_ONTAP_USERNAME>> would otherwise be used as real credentials.

set -euo pipefail

NAMESPACE="vcm"
CONFIGMAP="vcm-env"
SECRET="vcm-credentials"
DEPLOYMENTS=(vcm-prepool vcm-proxy)

# --- Region matrix ------------------------------------------------------------
resolve_region() {
  case "${VCM_REGION:-}" in
    au-se1)
      # In au-se1 the metadata server's SA has correct access to VSA image pull.
      VCM_LOCAL_REGION="australia-southeast1"
      VCM_GCE_METADATA_HOST="34.116.66.254:9090"
      VCM_SECRET_MANAGER_PROJECT_ID="266893635349" # netapp-au-se1-autopush-sde-tst
      ;;
    us-c1)
      VSA_AUTH_TYPE=0
      VCM_LOCAL_REGION="us-central1"
      VCM_GCE_METADATA_HOST="34.116.66.254:9090" # AU-SE1, VSA image pull will work, but it can't manage secrets of us-c1's secret manager, therefore use VSA_AUTH_TYPE=0 always
      # VCM_GCE_METADATA_HOST="35.209.239.186:9090" #actual, but its not configured for VSA image pull.
      VCM_SECRET_MANAGER_PROJECT_ID="254387308849" # netapp-us-c1-autopush-sde-tst
      ;;
    us-e4)
      VSA_AUTH_TYPE=0
      VCM_LOCAL_REGION="us-east4"
      VCM_GCE_METADATA_HOST="34.116.66.254:9090" # AU-SE1, VSA image pull will work, but it can't manage secrets of us-e4's secret manager, therefore use VSA_AUTH_TYPE=0 always
      # VCM_GCE_METADATA_HOST="34.48.252.127:9090" #actual, but its not configured for VSA image pull.
      VCM_SECRET_MANAGER_PROJECT_ID="402007546452" # netapp-us-e4-autopush-sde-tst
      ;;
    "")
      echo "ERROR: VCM_REGION is not set."
      echo "Usage: make skaffold-dev-vcm VCM_REGION=<au-se1|us-c1|us-e4>"
      exit 1
      ;;
    *)
      echo "ERROR: Unsupported VCM_REGION=${VCM_REGION}. Supported: au-se1, us-c1, us-e4"
      exit 1
      ;;
  esac
}

resolve_region
echo "VCM region: ${VCM_REGION} (${VCM_LOCAL_REGION})"

# --- Credentials --------------------------------------------------------------
# Require every value. Skipping an empty local ONTAP variable leaves the
# non-empty <<VCM_LOCAL_ONTAP_*>> placeholder in the Secret, and a later run
# would also keep whatever was written last.
require_value() {
  local name="$1" secret="$2" value
  value="${!name-}"
  if [[ -z "$value" ]]; then
    if [[ "$secret" == "secret" ]]; then
      read -rsp "Enter ${name}: " value; echo
    else
      read -rp "Enter ${name}: " value
    fi
    printf -v "$name" '%s' "$value"
  fi
  value="${!name-}"
  if [[ -z "$value" ]]; then
    echo "${name} is not set. Exiting."
    exit 1
  fi
  if [[ "$value" == "<<${name}>>" ]]; then
    echo "${name} is still the manifest placeholder. Exiting."
    exit 1
  fi
}

require_value VSA_NODE_PASSWORD secret
require_value VCM_LOCAL_ONTAP_USERNAME text
require_value VCM_LOCAL_ONTAP_PASSWORD secret
export NAMESPACE SECRET VSA_NODE_PASSWORD VCM_LOCAL_ONTAP_USERNAME VCM_LOCAL_ONTAP_PASSWORD

# --- Build ConfigMap patch (non-secret regional config only) -----------------
json_escape() {
  local value="${1//\\/\\\\}"
  value="${value//\"/\\\"}"
  printf '%s' "$value"
}

entries=()
add_entry() {
  local key="$1" value="$2"
  [[ -z "$value" ]] && return 0
  entries+=("\"${key}\":\"$(json_escape "$value")\"")
}

add_entry LOCAL_REGION "$VCM_LOCAL_REGION"
add_entry GCE_METADATA_HOST "$VCM_GCE_METADATA_HOST"
add_entry SECRET_MANAGER_PROJECT_ID "$VCM_SECRET_MANAGER_PROJECT_ID"

patch="{\"data\":{$(IFS=,; echo "${entries[*]}")}}"

kubectl patch configmap "$CONFIGMAP" -n "$NAMESPACE" --type merge -p "$patch"

# Drop credential keys left in the ConfigMap by older installs. Explicit
# secretKeyRef env entries take precedence, but the keys should not remain
# readable via the ConfigMap.
for key in VSA_NODE_PASSWORD VCM_LOCAL_ONTAP_USERNAME VCM_LOCAL_ONTAP_PASSWORD; do
  kubectl patch configmap "$CONFIGMAP" -n "$NAMESPACE" --type json \
    -p "[{\"op\":\"remove\",\"path\":\"/data/${key}\"}]" >/dev/null 2>&1 || true
done

# stdin so the password is not a kubectl argument. Python reads the values
# from the environment rather than from argv.
python3 - <<'PY' | kubectl apply -f -
import json
import os

doc = {
    "apiVersion": "v1",
    "kind": "Secret",
    "metadata": {
        "name": os.environ["SECRET"],
        "namespace": os.environ["NAMESPACE"],
    },
    "type": "Opaque",
    "stringData": {
        "VSA_NODE_PASSWORD": os.environ["VSA_NODE_PASSWORD"],
        "VCM_LOCAL_ONTAP_USERNAME": os.environ["VCM_LOCAL_ONTAP_USERNAME"],
        "VCM_LOCAL_ONTAP_PASSWORD": os.environ["VCM_LOCAL_ONTAP_PASSWORD"],
    },
}
print(json.dumps(doc))
PY

for deployment in "${DEPLOYMENTS[@]}"; do
  kubectl rollout restart "deployment/${deployment}" -n "$NAMESPACE"
done

for deployment in "${DEPLOYMENTS[@]}"; do
  kubectl rollout status "deployment/${deployment}" -n "$NAMESPACE" --timeout=5m
done

echo "Updated ${CONFIGMAP} and ${SECRET} for ${VCM_REGION} and restarted: ${DEPLOYMENTS[*]}."
