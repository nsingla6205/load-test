#!/bin/bash
# Read environment variables from the environment or prompt if not set
if [[ -z "$GCE_METADATA_HOST" ]]; then
  read -rp "Enter GCE_METADATA_HOST: " GCE_METADATA_HOST
fi
if [[ -z "$VSA_NODE_PASSWORD" ]]; then
  read -rsp "Enter VSA_NODE_PASSWORD: " VSA_NODE_PASSWORD; echo
fi
if [[ -z "$VSA_NODE_USERNAME" ]]; then
  read -rp "Enter VSA_NODE_USERNAME: " VSA_NODE_USERNAME
fi
if [[ -z "$LOCAL_REGION" ]]; then
  read -rp "Enter LOCAL_REGION: " LOCAL_REGION
fi

# Optional pool performance limit overrides (keep defaults aligned with LV unless explicitly changed)
MIN_TRUE_ZONAL_THROUGHPUT="${MIN_TRUE_ZONAL_THROUGHPUT:-64}"
MAX_TRUE_ZONAL_THROUGHPUT="${MAX_TRUE_ZONAL_THROUGHPUT:-60000}"
MIN_TRUE_ZONAL_CUSTOM_IOPS="${MIN_TRUE_ZONAL_CUSTOM_IOPS:-1024}"
MAX_TRUE_ZONAL_CUSTOM_IOPS="${MAX_TRUE_ZONAL_CUSTOM_IOPS:-960000}"

# If variables are still empty, exit with error
if [[ -z "$GCE_METADATA_HOST" || -z "$VSA_NODE_PASSWORD" || -z "$VSA_NODE_USERNAME" || -z "$LOCAL_REGION" ]]; then
  echo "One or more required environment variables are not set. Exiting."
  exit 1
fi

# Update environment variables in core deployment
kubectl set env deployment/core \
  MIN_TRUE_ZONAL_THROUGHPUT="$MIN_TRUE_ZONAL_THROUGHPUT" \
  MAX_TRUE_ZONAL_THROUGHPUT="$MAX_TRUE_ZONAL_THROUGHPUT" \
  MIN_TRUE_ZONAL_CUSTOM_IOPS="$MIN_TRUE_ZONAL_CUSTOM_IOPS" \
  MAX_TRUE_ZONAL_CUSTOM_IOPS="$MAX_TRUE_ZONAL_CUSTOM_IOPS" -n vcp

# Update environment variables in vcp-customer-worker deployment
kubectl set env deployment/vcp-customer-worker \
  GCE_METADATA_HOST="$GCE_METADATA_HOST" \
  LOCAL_REGION="$LOCAL_REGION" \
  VSA_NODE_PASSWORD="$VSA_NODE_PASSWORD" \
  VSA_NODE_USERNAME="$VSA_NODE_USERNAME" -n vcp

# Update environment variables in google-proxy deployment
kubectl set env deployment/google-proxy \
  GCE_METADATA_HOST="$GCE_METADATA_HOST" \
  LOCAL_REGION="$LOCAL_REGION" \
  VSA_NODE_PASSWORD="$VSA_NODE_PASSWORD" \
  VSA_NODE_USERNAME="$VSA_NODE_USERNAME" \
  MIN_TRUE_ZONAL_THROUGHPUT="$MIN_TRUE_ZONAL_THROUGHPUT" \
  MAX_TRUE_ZONAL_THROUGHPUT="$MAX_TRUE_ZONAL_THROUGHPUT" \
  MIN_TRUE_ZONAL_CUSTOM_IOPS="$MIN_TRUE_ZONAL_CUSTOM_IOPS" \
  MAX_TRUE_ZONAL_CUSTOM_IOPS="$MAX_TRUE_ZONAL_CUSTOM_IOPS" -n vcp


# Update environment variables in harvest-farm deployment
kubectl set env deployment/harvest-farm \
  GCE_METADATA_HOST="34.151.70.197:9090" \
  VSA_NODE_PASSWORD="$VSA_NODE_PASSWORD" \
  VSA_NODE_USERNAME="$VSA_NODE_USERNAME" -n vcp

# Wait for 15 seconds before updating deployments
echo "Waiting 15 seconds for deployments to be ready..."
sleep 15