#!/usr/bin/env bash
# ProxyGate-Matrix public SOCKS relay.
#
# Run this ON a VPS with a public IPv4 address. It keeps one SSH session to the
# Instabox sandbox (ssh.instabox.sh:2222) and forwards every slot SOCKS port, so
# the per-slot links emitted by the share page work from the public internet:
#
#   socks5://SLOT_USER:SLOT_PASS@VPS_PUBLIC_IP:10801
#
# The VPS only pipes TCP; SOCKS5 authentication still happens inside the
# sandbox and every slot keeps its own exit IP.
#
# Required:
#   INSTABOX_USER   Instabox SSH login token username (ibssh_...)
#   INSTABOX_KEY    path to the private key registered with the Instabox account
#
# Optional:
#   INSTABOX_HOST   default ssh.instabox.sh
#   INSTABOX_PORT   default 2222
#   SLOT_PORT_START default 10801
#   SLOT_PORT_END   default 10900
#   PUBLIC_BIND     default 0.0.0.0
#   OPEN_FIREWALL   default 1 (adds a ufw rule when ufw is active)
#
# Usage:
#   sudo INSTABOX_USER=ibssh_xxx INSTABOX_KEY=/root/instabox_key \
#        bash vps-relay.sh
#
# After it starts, set PROXYGATE_MATRIX_PUBLIC_SOCKS_HOST to this VPS public
# address and restart ProxyGate-Matrix. Verify a slot with:
#   curl --socks5-hostname 'SLOT_USER:SLOT_PASS@VPS_PUBLIC_IP:10801' \
#        https://api.ipify.org
#
# If journalctl shows "administratively prohibited" for forwarded channels, the
# sandbox sshd refuses TCP forwarding and the tunnel must be initiated from the
# sandbox instead; check that before changing anything here.
#
# Uninstall:
#   systemctl disable --now proxygate-relay.service
#   rm -f /etc/systemd/system/proxygate-relay.service /etc/proxygate/relay.env
#   systemctl daemon-reload

set -euo pipefail

INSTABOX_HOST="${INSTABOX_HOST:-ssh.instabox.sh}"
INSTABOX_PORT="${INSTABOX_PORT:-2222}"
SLOT_PORT_START="${SLOT_PORT_START:-10801}"
SLOT_PORT_END="${SLOT_PORT_END:-10900}"
PUBLIC_BIND="${PUBLIC_BIND:-0.0.0.0}"
REMOTE_BIND="${REMOTE_BIND:-127.0.0.1}"
OPEN_FIREWALL="${OPEN_FIREWALL:-1}"

die() { echo "error: $*" >&2; exit 1; }

[[ "${EUID}" -eq 0 ]] || die "run as root: sudo -E bash $0"
[[ -n "${INSTABOX_USER:-}" ]] || die "set INSTABOX_USER to an Instabox SSH token username (ibssh_...)"
[[ -n "${INSTABOX_KEY:-}" ]] || die "set INSTABOX_KEY to the private key path"
[[ -f "${INSTABOX_KEY}" ]] || die "key not found: ${INSTABOX_KEY}"
[[ "${SLOT_PORT_START}" =~ ^[0-9]+$ && "${SLOT_PORT_END}" =~ ^[0-9]+$ ]] || die "slot ports must be numeric"
(( SLOT_PORT_START >= 1024 && SLOT_PORT_END <= 65535 && SLOT_PORT_START <= SLOT_PORT_END )) || die "invalid slot port range"
command -v ssh >/dev/null || die "openssh-client is required: apt-get install -y openssh-client"
command -v systemctl >/dev/null || die "systemd is required"

install -d -m 700 /etc/proxygate
key_dest=/etc/proxygate/instabox_key
if [[ "$(readlink -f "${INSTABOX_KEY}")" != "$(readlink -f "${key_dest}" 2>/dev/null || echo "")" ]]; then
  install -m 600 "${INSTABOX_KEY}" "${key_dest}"
else
  chmod 600 "${key_dest}"
fi
: > /etc/proxygate/known_hosts
chmod 600 /etc/proxygate/known_hosts

forwards=()
for ((port = SLOT_PORT_START; port <= SLOT_PORT_END; port++)); do
  forwards+=("-L" "${PUBLIC_BIND}:${port}:${REMOTE_BIND}:${port}")
done

cat > /etc/proxygate/relay.env <<EOF
INSTABOX_HOST=${INSTABOX_HOST}
INSTABOX_PORT=${INSTABOX_PORT}
SLOT_PORT_START=${SLOT_PORT_START}
SLOT_PORT_END=${SLOT_PORT_END}
EOF

unit=/etc/systemd/system/proxygate-relay.service
{
  cat <<EOF
[Unit]
Description=ProxyGate-Matrix public SOCKS relay (${SLOT_PORT_START}-${SLOT_PORT_END})
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=/etc/proxygate/relay.env
ExecStart=/usr/bin/ssh -N -T \\
  -o BatchMode=yes \\
  -o ExitOnForwardFailure=no \\
  -o ServerAliveInterval=15 \\
  -o ServerAliveCountMax=4 \\
  -o TCPKeepAlive=yes \\
  -o ConnectTimeout=15 \\
  -o Compression=no \\
  -o StrictHostKeyChecking=accept-new \\
  -o UserKnownHostsFile=/etc/proxygate/known_hosts \\
  -i /etc/proxygate/instabox_key \\
  -p \${INSTABOX_PORT} \\
EOF
  for ((i = 0; i < ${#forwards[@]}; i += 2)); do
    echo "  ${forwards[i]} ${forwards[i + 1]} \\"
  done
  cat <<EOF
  ${INSTABOX_USER}@\${INSTABOX_HOST}
Restart=always
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF
} > "${unit}"

systemctl daemon-reload
systemctl enable proxygate-relay.service >/dev/null
systemctl restart proxygate-relay.service

if [[ "${OPEN_FIREWALL}" == "1" ]] && command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q '^Status: active'; then
  ufw allow "${SLOT_PORT_START}:${SLOT_PORT_END}/tcp" >/dev/null
  echo "ufw: allowed ${SLOT_PORT_START}-${SLOT_PORT_END}/tcp"
fi

sleep 2
listening=$(ss -ltnH 2>/dev/null | awk -v s="${SLOT_PORT_START}" -v e="${SLOT_PORT_END}" \
  '{ p = $4; sub(/.*:/, "", p); if (p + 0 >= s && p + 0 <= e) count++ } END { print count + 0 }')
expected=$((SLOT_PORT_END - SLOT_PORT_START + 1))

echo
echo "listening slot ports: ${listening} / ${expected}"
echo "public SOCKS host:    $(curl -fsS --max-time 5 https://api.ipify.org || echo '<VPS_PUBLIC_IP>')"
echo
echo "next:"
echo "  1) set PROXYGATE_MATRIX_PUBLIC_SOCKS_HOST=<VPS_PUBLIC_IP> and restart ProxyGate-Matrix"
echo "  2) curl --socks5-hostname 'SLOT_USER:SLOT_PASS@<VPS_PUBLIC_IP>:${SLOT_PORT_START}' https://api.ipify.org"
echo
journalctl -u proxygate-relay.service -n 12 --no-pager 2>/dev/null || true
