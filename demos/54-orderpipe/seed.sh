#!/usr/bin/env bash
# Idempotent catalog seed for OrderPipe (epic 54.01).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/54-orderpipe"
STATE_FILE="${DEMO_DIR}/.demo-state"
GATEWAY_URL="${FORGE_GATEWAY_URL:-http://127.0.0.1:4000}"
API_HOST="${API_HOST:-api.orderpipe.localhost}"

fail() {
  echo "seed 54-orderpipe failed: $*" >&2
  exit 1
}

read_state() {
  [[ -f "${STATE_FILE}" ]] || fail "missing ${STATE_FILE}; run demos/54-orderpipe/run.sh first"
  # shellcheck disable=SC1090
  source "${STATE_FILE}"
  [[ -n "${API_DEPLOYMENT_ID:-}" ]] || fail "API_DEPLOYMENT_ID missing from .demo-state"
}

api_container() {
  local cid short
  cid="$(docker ps -q \
    --filter "label=forge.deployment_id=${API_DEPLOYMENT_ID}" \
    --filter "label=forge.managed=true" | head -n1)"
  if [[ -n "${cid}" ]]; then
    echo "${cid}"
    return 0
  fi
  short="$(python3 -c 'import sys; print(sys.argv[1].replace("-", "")[:8])' "${API_DEPLOYMENT_ID}")"
  docker ps -q --filter "label=forge.managed=true" --filter "name=forge-api-${short}-" | head -n1
}

database_url_from_container() {
  local cid="$1"
  docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "${cid}" 2>/dev/null |
    awk -F= '$1=="DATABASE_URL" { print substr($0, index($0, "=")+1); exit }'
}

run_sql() {
  local url="$1"
  local sql="$2"
  if command -v psql >/dev/null 2>&1; then
    PGPASSWORD="" psql "${url}" -v ON_ERROR_STOP=1 -c "${sql}" >/dev/null
    return 0
  fi
  docker run --rm --network host postgres:16-alpine \
    psql "${url}" -v ON_ERROR_STOP=1 -c "${sql}" >/dev/null
}

seed_sql() {
  cat <<'SQL'
INSERT INTO catalog_items (sku, name, unit_cents)
VALUES
  ('mug', 'Forge Mug', 1800),
  ('shirt', 'Forge Tee', 2800),
  ('sticker', 'Forge Sticker Pack', 600)
ON CONFLICT (sku) DO UPDATE
SET name = EXCLUDED.name,
    unit_cents = EXCLUDED.unit_cents;
SQL
}

read_state
echo "seed 54-orderpipe: resolving DATABASE_URL from API container..."
CID="$(api_container)"
[[ -n "${CID}" ]] || fail "no running API container for deployment ${API_DEPLOYMENT_ID}"

URL=""
for _ in $(seq 1 60); do
  URL="$(database_url_from_container "${CID}")"
  if [[ -n "${URL}" ]]; then
    break
  fi
  sleep 1
  CID="$(api_container)"
  [[ -n "${CID}" ]] || fail "API container disappeared while waiting for DATABASE_URL"
done
[[ -n "${URL}" ]] || fail "DATABASE_URL not present on API container (managed DB attach missing?)"

echo "seed 54-orderpipe: applying idempotent catalog SQL..."
run_sql "${URL}" "$(seed_sql)"

code="$(curl --silent --show-error -o /tmp/orderpipe-seed-catalog.json -w '%{http_code}' \
  -H "Host: ${API_HOST}" "${GATEWAY_URL}/catalog" || echo "000")"
[[ "${code}" == "200" ]] || fail "GET /catalog returned HTTP ${code}"
python3 - <<'PY'
import json
body = json.load(open("/tmp/orderpipe-seed-catalog.json"))
items = body.get("items") or []
skus = {i.get("sku") for i in items}
need = {"mug", "shirt", "sticker"}
missing = need - skus
if missing:
    raise SystemExit(f"seeded catalog missing SKUs: {sorted(missing)}")
print(f"seed OK: catalog {len(items)} item(s) visible via API")
PY
