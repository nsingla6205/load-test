#!/bin/sh
# sshuttle sidecar entrypoint for VCM internal-IP development.
#
# Waits for TUNNEL_ACTIVE=true in the environment (from vcm-tunnel-config
# ConfigMap), then starts sshuttle to forward the management CIDR through
# the jump host via the host-side IAP TCP tunnel.
#
# Required env (set by vcm-tunnel-config ConfigMap):
#   TUNNEL_ACTIVE    - "true" to start, anything else to wait
#   MGMT_CIDR        - management subnet CIDR (default 198.18.0.0/20)
#   IAP_LOCAL_PORT   - host-side IAP tunnel port (default 2222)
#   SSH_USER         - OS Login POSIX username (e.g. satya1_netapp_com)
#   SSH_HOST         - host reachable from inside the pod (default host.docker.internal)
#
# Required volume mount:
#   /etc/tunnel-ssh/key   - private SSH key (from vcm-tunnel-ssh-key Secret)
#
# The host observer opens an IAP TCP tunnel to the jump host's SSH port.
# The pod reaches it via host.docker.internal (Docker Desktop Kubernetes).
set -eu

POLL_INTERVAL=5
READY_FILE="/tmp/tunnel-ready"

rm -f "${READY_FILE}"

# Mark ready immediately so the pod passes readiness while idle.
# The sidecar is not blocking vcm-proxy traffic — it only adds routing
# once activated. Without this the rolling update never completes.
touch "${READY_FILE}"

echo "vcm-tunnel: waiting for activation (TUNNEL_ACTIVE=true in vcm-tunnel-config)..."

while true; do
  if [ "${TUNNEL_ACTIVE:-false}" = "true" ]; then
    break
  fi
  sleep "${POLL_INTERVAL}"
done

CIDR="${MGMT_CIDR:-198.18.0.0/20}"
PORT="${IAP_LOCAL_PORT:-2222}"
USER="${SSH_USER:-}"
HOST="${SSH_HOST:-host.docker.internal}"
KEY_FILE="/etc/tunnel-ssh/key"

if [ -z "${USER}" ]; then
  echo "vcm-tunnel: ERROR — SSH_USER is not set in vcm-tunnel-config." >&2
  exit 1
fi

if [ ! -f "${KEY_FILE}" ]; then
  echo "vcm-tunnel: ERROR — SSH key not found at ${KEY_FILE}. Is vcm-tunnel-ssh-key Secret mounted?" >&2
  exit 1
fi

echo "vcm-tunnel: activated. CIDR=${CIDR} SSH=${USER}@${HOST}:${PORT}"

# sshuttle inside a container with NET_ADMIN does not need sudo.
# --method nat uses iptables NAT rules (installed in the image).
# Connect to the jump host via the host-side IAP tunnel at host.docker.internal.
exec sshuttle \
  --method nat \
  --verbose \
  --ssh-cmd "ssh -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/tmp/known_hosts -i ${KEY_FILE} -p ${PORT}" \
  --remote="${USER}@${HOST}" \
  "${CIDR}"
