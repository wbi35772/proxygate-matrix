#!/bin/sh
# Probe every ready matrix slot through its SOCKS5 listener and print:
#   <slotId> <port> <exitIp|DEAD>
# Safe to run on the sandbox host: it only talks to published slot ports.
set -eu
cd "$(dirname "$0")/.."
set -a
. ./.env
set +a

TMP_SLOTS=$(mktemp)
trap 'rm -f "$TMP_SLOTS"' EXIT

curl -sS -H "Authorization: Bearer ${PROXYGATE_API_KEY}" "http://127.0.0.1:8080/api/v1/pool/slots" \
  | python3 -c '
import json, sys
state = json.load(sys.stdin)
for slot in state.get("slots", []):
    if slot.get("state") != "ready":
        continue
    url = slot.get("socks5", "")
    if "://" not in url or "@" not in url:
        continue
    auth = url.split("://", 1)[1].rsplit("@", 1)[0]
    print(slot["slotId"], slot["port"], auth)
' > "$TMP_SLOTS"

while read -r id port auth; do
  (
    ip=""
    for target in \
      "https://checkip.amazonaws.com" \
      "https://ifconfig.me/ip" \
      "http://ip-api.com/line/?fields=query"; do
      ip=$(curl -sS --max-time 8 --socks5-hostname "$auth@127.0.0.1:$port" "$target" 2>/dev/null \
        | head -n 1 | tr -d '\r\n ') || true
      if [ -n "$ip" ]; then
        break
      fi
    done
    printf '%s %s %s\n' "$id" "$port" "${ip:-DEAD}"
  ) &
done < "$TMP_SLOTS"
wait
rm -f "$TMP_SLOTS"
