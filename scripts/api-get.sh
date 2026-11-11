#!/bin/sh
# Fetch a ProxyGate API path with the pool bearer key from the deployment .env.
# Usage: sh scripts/api-get.sh /api/v1/pool/slots
set -eu
cd "$(dirname "$0")/.."
set -a
. ./.env
set +a
curl -sS -H "Authorization: Bearer ${PROXYGATE_API_KEY}" "http://127.0.0.1:8080${1:-/api/v1/pool/slots}"
