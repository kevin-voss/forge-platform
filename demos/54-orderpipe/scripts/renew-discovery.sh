#!/usr/bin/env bash
# Renew OrderPipe Discovery Ready endpoints (longer lease for E2E). Used by
# tests/e2e/projects/04-orderpipe and optional manual re-wire after lease expiry.
set -euo pipefail

DEMO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STATE="${DEMO_DIR}/.demo-state"
[[ -f "${STATE}" ]] || { echo "missing ${STATE}" >&2; exit 1; }
# shellcheck disable=SC1090
source "${STATE}"

DISCOVERY_URL="${FORGE_DISCOVERY_HOST_URL:-http://127.0.0.1:4109}"
DISC_PROJECT="${FORGE_DISCOVERY_DEFAULT_PROJECT:-orderpipe}"
DISC_ENV="${FORGE_DISCOVERY_DEFAULT_ENVIRONMENT:-local}"
DISC_NODE="${FORGE_SCHEDULER_LOCAL_NODE_ID:-}"
LEASE="${ORDERPIPE_DISCOVERY_LEASE_SECONDS:-300}"
POSTGRES_CONTAINER="${FORGE_POSTGRES_CONTAINER:-forge-postgres}"

# forge-runtime exposes FORGE_NODE_ID; older stacks used FORGE_SCHEDULER_LOCAL_NODE_ID.
if [[ -z "${DISC_NODE}" ]]; then
  DISC_NODE="$(docker exec forge-runtime printenv FORGE_SCHEDULER_LOCAL_NODE_ID 2>/dev/null || true)"
fi
if [[ -z "${DISC_NODE}" ]]; then
  DISC_NODE="$(docker exec forge-runtime printenv FORGE_NODE_ID 2>/dev/null || true)"
fi
DISC_NODE="${DISC_NODE:-node-local}"
[[ -n "${DISC_NODE}" ]] || { echo "discovery node id unset" >&2; exit 1; }
[[ -n "${PROJECT_SLUG:-}" ]] || { echo "PROJECT_SLUG missing from .demo-state" >&2; exit 1; }
[[ -n "${API_DEPLOYMENT_ID:-}" && -n "${FULFILLMENT_DEPLOYMENT_ID:-}" && -n "${NOTIFY_DEPLOYMENT_ID:-}" ]] ||
  { echo "deployment ids missing from .demo-state" >&2; exit 1; }

# Match demos/54-orderpipe/run.sh: container name uses first 8 hex of deployment id.
deployment_container_id() {
  local dep_id="$1" slug="$2" short
  short="$(python3 -c 'import sys; print(sys.argv[1].replace("-", "")[:8])' "${dep_id}")"
  docker ps -q --filter "label=forge.managed=true" --filter "name=forge-${slug}-${short}-" | head -n1
}

container_ip() {
  docker inspect -f '{{range.NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$1"
}

api_cid="$(deployment_container_id "${API_DEPLOYMENT_ID}" "api")"
ff_cid="$(deployment_container_id "${FULFILLMENT_DEPLOYMENT_ID}" "fulfillment")"
nt_cid="$(deployment_container_id "${NOTIFY_DEPLOYMENT_ID}" "notify")"
[[ -n "${api_cid}" && -n "${ff_cid}" && -n "${nt_cid}" ]] ||
  { echo "missing managed containers api=${api_cid:-?} fulfillment=${ff_cid:-?} notify=${nt_cid:-?}" >&2; exit 1; }

api_ip="$(container_ip "${api_cid}")"
ff_ip="$(container_ip "${ff_cid}")"
nt_ip="$(container_ip "${nt_cid}")"
[[ -n "${api_ip}" && -n "${ff_ip}" && -n "${nt_ip}" ]] ||
  { echo "missing container IPs" >&2; exit 1; }

echo "Renewing Discovery peers (project=${DISC_PROJECT} env=${DISC_ENV} lease=${LEASE}s)..."
docker exec "${POSTGRES_CONTAINER}" psql -U forge -d forge -v ON_ERROR_STOP=1 -c \
  'DELETE FROM discovery.endpoints; DELETE FROM discovery.services;' >/dev/null

register() {
  local service="$1" id="$2" ip="$3"
  curl --fail --silent --show-error \
    -X POST "${DISCOVERY_URL}/v1/projects/${DISC_PROJECT}/environments/${DISC_ENV}/services/${service}/endpoints" \
    -H 'content-type: application/json' \
    -d "{\"id\":\"${id}\",\"node\":\"${DISC_NODE}\",\"address\":{\"ip\":\"${ip}\",\"port\":8080},\"protocol\":\"http\",\"revision\":\"v1\",\"leaseSeconds\":${LEASE}}" \
    >/dev/null
  local renew
  renew="$(curl --fail --silent --show-error \
    -X POST "${DISCOVERY_URL}/v1/projects/${DISC_PROJECT}/environments/${DISC_ENV}/endpoints/${id}/renew" \
    -H 'content-type: application/json' \
    -d "{\"ready\":true,\"leaseSeconds\":${LEASE}}")"
  python3 -c 'import json,sys; d=json.loads(sys.argv[1]); assert d.get("phase")=="Ready", d' \
    "${renew}"
  echo "  ${service} id=${id} ip=${ip} phase=Ready"
}

register "api" "api-${PROJECT_SLUG}-0" "${api_ip}"
register "fulfillment" "fulfillment-${PROJECT_SLUG}-0" "${ff_ip}"
register "notify" "notify-${PROJECT_SLUG}-0" "${nt_ip}"

for service in api fulfillment notify; do
  curl --fail --silent --show-error \
    "${DISCOVERY_URL}/v1/projects/${DISC_PROJECT}/environments/${DISC_ENV}/services/${service}/endpoints" |
    python3 -c 'import json,sys; items=json.load(sys.stdin); assert items and all(i.get("phase")=="Ready" for i in items), items; print(f"  {sys.argv[1]}: {len(items)} Ready")' \
      "${service}"
done
echo "discovery renew ok"
