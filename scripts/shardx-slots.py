#!/usr/bin/env python3
"""Allocate ProxyGate-Matrix slots for ShardX fingerprint browsers.

Examples:
  PROXYGATE_API=http://127.0.0.1:8080 PROXYGATE_API_KEY=secret \
    python3 scripts/shardx-slots.py --count 5 --country JP --mode sticky

  python3 scripts/shardx-slots.py --count 3 --country KR \
    --protocol openvpn_tcp --wait --format lines
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

DEFAULT_API = os.environ.get("PROXYGATE_API", "http://127.0.0.1:8080")


def request(api: str, key: str, method: str, path: str, payload: dict | None = None):
    body = None if payload is None else json.dumps(payload).encode()
    req = urllib.request.Request(
        api.rstrip("/") + path,
        data=body,
        method=method,
        headers={
            "Authorization": f"Bearer {key}",
            "Content-Type": "application/json",
            "Accept": "application/json",
        },
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as response:
            return json.load(response)
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode(errors="replace")
        raise SystemExit(f"{method} {path} -> HTTP {exc.code}: {detail}") from exc


def credentials(socks5: str) -> tuple[str, str, str, int]:
    parsed = urllib.parse.urlsplit(socks5)
    host = parsed.hostname or ""
    port = parsed.port or 0
    username = urllib.parse.unquote(parsed.username or "")
    password = urllib.parse.unquote(parsed.password or "")
    return username, password, host, port


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--api", default=DEFAULT_API, help="ProxyGate base URL")
    parser.add_argument("--key", default=os.environ.get("PROXYGATE_API_KEY", ""), help="pool API bearer key")
    parser.add_argument("--count", type=int, default=1, help="number of slots to allocate")
    parser.add_argument("--country", default="", help="ISO country code, empty for preferred")
    parser.add_argument("--protocol", default="", help="openvpn_tcp, openvpn_udp, softether_https, sstp, l2tp")
    parser.add_argument("--mode", default="sticky", choices=("sticky", "rotating"))
    parser.add_argument("--first-slot", type=int, default=1, help="first slot id to try")
    parser.add_argument("--wait", action="store_true", help="wait until every requested slot is ready")
    parser.add_argument("--wait-timeout", type=float, default=180.0, help="seconds to wait for ready")
    parser.add_argument("--format", choices=("json", "lines"), default="json")
    args = parser.parse_args()
    if not args.key:
        parser.error("set --key or PROXYGATE_API_KEY")

    state = request(args.api, args.key, "GET", "/api/v1/pool/slots")
    allocated = {slot["slotId"] for slot in state.get("slots", [])}
    wanted: list[int] = []
    slot_id = args.first_slot
    while len(wanted) < args.count and slot_id <= state.get("maxSlots", 35):
        if slot_id not in allocated:
            wanted.append(slot_id)
        slot_id += 1
    if len(wanted) < args.count:
        raise SystemExit(f"only {len(wanted)} free slots available")

    created: list[dict] = []
    for slot_id in wanted:
        payload = {
            "slot_id": slot_id,
            "country": args.country,
            "protocol": args.protocol,
            "mode": args.mode,
        }
        result = request(args.api, args.key, "POST", "/api/v1/pool/allocate", payload)
        created.append(result["slot"])
        print(f"allocated slot {slot_id}: {result['slot'].get('state')}", file=sys.stderr)

    if args.wait:
        deadline = time.time() + args.wait_timeout
        pending = {slot["slotId"] for slot in created}
        latest: dict[int, dict] = {slot["slotId"]: slot for slot in created}
        while pending and time.time() < deadline:
            time.sleep(3)
            state = request(args.api, args.key, "GET", "/api/v1/pool/slots")
            for slot in state.get("slots", []):
                if slot["slotId"] in pending and slot.get("state") == "ready":
                    pending.discard(slot["slotId"])
                if slot["slotId"] in latest:
                    latest[slot["slotId"]] = slot
        if pending:
            raise SystemExit(f"slots not ready before timeout: {sorted(pending)}")
        created = [latest[slot["slotId"]] for slot in created]

    output = []
    for slot in created:
        username, password, host, port = credentials(slot["socks5"])
        output.append(
            {
                "slotId": slot["slotId"],
                "name": f"proxygate-{slot['slotId']:02d}-{slot.get('countryShort') or slot.get('country') or 'auto'}",
                "host": host,
                "port": port,
                "username": username,
                "password": password,
                "protocol": "socks5",
                "mode": slot.get("mode"),
                "country": slot.get("countryShort") or slot.get("country"),
                "exitIp": slot.get("exitIp") or slot.get("activeIp"),
                "socks5": slot["socks5"],
            }
        )

    if args.format == "lines":
        for item in output:
            print(item["socks5"])
    else:
        json.dump({"proxies": output}, sys.stdout, indent=2)
        print()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
