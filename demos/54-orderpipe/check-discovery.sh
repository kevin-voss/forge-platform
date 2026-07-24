#!/usr/bin/env bash
# Contract check: OrderPipe product source must address peers via Discovery
# (*.svc.forge), never hard-coded compose DNS / peer IPs (mirrors capstone 12-contracts.sh).
set -euo pipefail

DEMO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
API_DIR="${DEMO_DIR}/api"

echo "OrderPipe discovery contract: no hard-coded peer DNS..."

# Compose-style peer base URLs (without .svc.forge) are forbidden in product source.
if grep -RInE 'https?://(fulfillment|notify|orderpipe-fulfillment|orderpipe-notify)(:[0-9]+)?(/|"|'\'')' \
  "${API_DIR}" \
  --include='*.go' 2>/dev/null |
  grep -vE '\.svc\.forge' >/dev/null; then
  echo "product uses hard-coded peer service DNS; must use *.svc.forge Discovery names" >&2
  grep -RInE 'https?://(fulfillment|notify|orderpipe-fulfillment|orderpipe-notify)(:[0-9]+)?(/|"|'\'')' \
    "${API_DIR}" --include='*.go' 2>/dev/null | grep -vE '\.svc\.forge' >&2 || true
  exit 1
fi

grep -n 'fulfillment.svc.forge' "${API_DIR}/config.go" >/dev/null
grep -n 'notify.svc.forge' "${API_DIR}/config.go" >/dev/null
grep -n 'FORGE_DISCOVERY_URL' "${API_DIR}/config.go" >/dev/null
grep -n 'serviceFromDiscoveryHost' "${API_DIR}/peers.go" >/dev/null
grep -n 'resolveReady' "${API_DIR}/peers.go" >/dev/null

echo "discovery contract checks ok"
