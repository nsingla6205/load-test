ARG BASE
FROM $BASE

COPY artifacts/vcm-proxy /app
COPY common/vsa_config /common/vsa_config

# Set ownership and permissions
USER nonroot:nonroot
