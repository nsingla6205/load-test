# Minimal sshuttle sidecar for VCM internal-IP development.
# Runs inside the vcm-proxy pod network namespace with NET_ADMIN.
# Starts idle; the host observer patches vcm-tunnel-config to activate.
FROM python:3.12-alpine

RUN apk add --no-cache \
    openssh-client \
    iptables \
    ip6tables \
    && pip install --no-cache-dir sshuttle==1.1.2

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
