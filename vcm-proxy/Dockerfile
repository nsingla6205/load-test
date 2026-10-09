ARG BASE
FROM $BASE

COPY artifacts/vcm-proxy /app
COPY common/vsa_config /common/vsa_config
COPY config/vmrs_gcp.yaml  /config/vmrs_gcp.yaml
COPY config/vmrs_gcp_v2.yaml  /config/vmrs_gcp_v2.yaml
COPY config/vmrs_gcp_tz_12node.yaml  /config/vmrs_gcp_tz_12node.yaml
COPY config/vmrs_gcp_tz_24node.yaml  /config/vmrs_gcp_tz_24node.yaml

# Set ownership and permissions
USER nonroot:nonroot
