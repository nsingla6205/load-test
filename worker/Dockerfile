ARG BASE
FROM $BASE

COPY artifacts/vcp-worker /app
COPY common/vsa_config/vlm-config.json /common/vsa_config/vlm-config.json
COPY config/vmrs_gcp.yaml  /config/vmrs_gcp.yaml
COPY config/vmrs_gcp_v2.yaml  /config/vmrs_gcp_v2.yaml

# Set ownership and permissions
USER nonroot:nonroot
