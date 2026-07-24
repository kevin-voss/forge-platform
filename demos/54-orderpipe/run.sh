#!/usr/bin/env bash
# Demo 54: OrderPipe multi-service + Discovery + NetworkPolicy (epic 54.03).
# Usage:
#   demos/54-orderpipe/run.sh          # build → apply → DB → Discovery → NetworkPolicy → proofs
#   demos/54-orderpipe/run.sh --down   # tear down product resources only
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/54-orderpipe"
STATE_FILE="${DEMO_DIR}/.demo-state"

export FORGE_AUTH_MODE="${FORGE_AUTH_MODE:-dev}"
export FORGE_LIFECYCLE_OWNER="${FORGE_LIFECYCLE_OWNER:-control}"
export FORGE_RECONCILE_INTERVAL_MS="${FORGE_RECONCILE_INTERVAL_MS:-1000}"
export FORGE_READINESS_POLL_MS="${FORGE_READINESS_POLL_MS:-500}"
export FORGE_READINESS_MAX_WAIT_S="${FORGE_READINESS_MAX_WAIT_S:-90}"
export FORGE_RESOURCE_API_ENABLED="${FORGE_RESOURCE_API_ENABLED:-true}"
export FORGE_SECRETS_URL="${FORGE_SECRETS_URL:-disabled}"
export FORGE_OTEL_ENABLED="${FORGE_OTEL_ENABLED:-false}"
export FORGE_SCHEDULER_STRATEGY="${FORGE_SCHEDULER_STRATEGY:-single-node}"
export FORGE_SCHEDULER_LOCAL_NODE_ID="${FORGE_SCHEDULER_LOCAL_NODE_ID:-node-local}"
export FORGE_ROUTE_SYNC_INTERVAL_SECONDS="${FORGE_ROUTE_SYNC_INTERVAL_SECONDS:-2}"
export FORGE_UPSTREAM_PROBE_INTERVAL_SECONDS="${FORGE_UPSTREAM_PROBE_INTERVAL_SECONDS:-2}"
export FORGE_UPSTREAM_FAILURE_THRESHOLD="${FORGE_UPSTREAM_FAILURE_THRESHOLD:-1}"
export FORGE_UPSTREAM_SUCCESS_THRESHOLD="${FORGE_UPSTREAM_SUCCESS_THRESHOLD:-1}"
export FORGE_DB_PROVISIONER="${FORGE_DB_PROVISIONER:-local}"
export FORGE_DB_ENDPOINT_HOST="${FORGE_DB_ENDPOINT_HOST:-host.docker.internal}"
export FORGE_DB_MANAGED_NETWORK="${FORGE_DB_MANAGED_NETWORK:-forge-net}"
export FORGE_INJECT_MASK_IN_LOGS="${FORGE_INJECT_MASK_IN_LOGS:-true}"
export FORGE_DISCOVERY_DEFAULT_PROJECT="${FORGE_DISCOVERY_DEFAULT_PROJECT:-orderpipe}"
export FORGE_DISCOVERY_DEFAULT_ENVIRONMENT="${FORGE_DISCOVERY_DEFAULT_ENVIRONMENT:-local}"
export FORGE_NETWORK_DNS_SEARCH="${FORGE_NETWORK_DNS_SEARCH:-local.orderpipe.svc.forge}"
export DOCKER_GID="${DOCKER_GID:-$(stat -f '%g' /var/run/docker.sock 2>/dev/null || stat -c '%g' /var/run/docker.sock 2>/dev/null || echo 0)}"
export COMPOSE_PARALLEL_LIMIT="${COMPOSE_PARALLEL_LIMIT:-1}"

COMPOSE=(
  docker compose
  -f "${ROOT_DIR}/compose.yaml"
  -f "${DEMO_DIR}/docker-compose.yml"
  --project-directory "${ROOT_DIR}"
)
CONTROL_URL="${FORGE_CONTROL_URL:-http://127.0.0.1:4001}"
RUNTIME_URL="${FORGE_RUNTIME_URL:-http://127.0.0.1:4102}"
GATEWAY_URL="${FORGE_GATEWAY_URL:-http://127.0.0.1:4000}"
BUILD_URL="${FORGE_BUILD_URL:-http://127.0.0.1:4103}"
DISCOVERY_URL="${FORGE_DISCOVERY_HOST_URL:-http://127.0.0.1:4109}"
NETWORK_URL="${FORGE_NETWORK_URL:-http://127.0.0.1:4110}"
CONTROL_SERVICE="forge-control"
RUNTIME_SERVICE="forge-runtime"
GATEWAY_SERVICE="forge-gateway"
BUILD_SERVICE="forge-build"
DISCOVERY_SERVICE="forge-discovery"
NETWORK_SERVICE="forge-network"
POSTGRES_SERVICE="postgres"
REGISTRY_SERVICE="registry"
DISC_PROJECT="${FORGE_DISCOVERY_DEFAULT_PROJECT}"
DISC_ENV="${FORGE_DISCOVERY_DEFAULT_ENVIRONMENT}"
DISC_NODE="${FORGE_SCHEDULER_LOCAL_NODE_ID}"
NETWORK_NAME="${FORGE_NETWORK_NAME:-cluster-overlay}"
NET_ORG="${FORGE_NETWORK_ORG:-default}"
CLI_DIR="${ROOT_DIR}/tools/forge-cli"
FORGE_BIN="${CLI_DIR}/forge"
REGISTRY="${FORGE_REGISTRY:-localhost:5000}"
API_IMAGE="${DEMO_API_IMAGE:-${REGISTRY}/orderpipe/orderpipe-api:v1}"
WEB_IMAGE="${DEMO_WEB_IMAGE:-${REGISTRY}/orderpipe/orderpipe-shop:v1}"
FULFILLMENT_IMAGE="${DEMO_FULFILLMENT_IMAGE:-${REGISTRY}/orderpipe/orderpipe-fulfillment:v1}"
NOTIFY_IMAGE="${DEMO_NOTIFY_IMAGE:-${REGISTRY}/orderpipe/orderpipe-notify:v1}"
API_HOST="api.orderpipe.localhost"
SHOP_HOST="shop.orderpipe.localhost"
FULFILLMENT_HOST="fulfillment.orderpipe.localhost"
NOTIFY_HOST="notify.orderpipe.localhost"
DB_NAME="orderpipe-db"
DB_LOGICAL_NAME="orderpipe_db"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-demo-54.XXXXXX")"
CONFIG_HOME="${TMP_DIR}/xdg-config"
export XDG_CONFIG_HOME="${CONFIG_HOME}"
export CI="${CI:-1}"
export FORGE_PROFILE="${FORGE_PROFILE:-demo54}"
export FORGE_ENDPOINT="${FORGE_ENDPOINT:-${CONTROL_URL}}"
mkdir -p "${CONFIG_HOME}"

fail() {
  echo "Demo 54 failed: $*" >&2
  echo "--- ${GATEWAY_SERVICE} /admin/routes ---" >&2
  curl --silent --show-error "${GATEWAY_URL}/admin/routes" >&2 || true
  echo >&2
  echo "--- ${CONTROL_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=80 "${CONTROL_SERVICE}" >&2 || true
  echo "--- ${RUNTIME_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${RUNTIME_SERVICE}" >&2 || true
  echo "--- ${GATEWAY_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${GATEWAY_SERVICE}" >&2 || true
  echo "--- managed db containers ---" >&2
  docker ps --filter "label=forge.managed_db=true" --format '{{.Names}} {{.Status}}' >&2 || true
  exit 1
}

cleanup_tmp() {
  rm -rf "${TMP_DIR}"
}
trap cleanup_tmp EXIT

wait_http() {
  local url="$1" label="$2" attempts="${3:-90}"
  local ready=0
  echo "Waiting for ${label} at ${url} ..."
  for _ in $(seq 1 "${attempts}"); do
    if curl --fail --silent --show-error "${url}" >/dev/null 2>&1; then
      ready=1
      break
    fi
    sleep 1
  done
  [[ "${ready}" -eq 1 ]] || fail "timed out waiting for ${label}"
}

forge() {
  echo "+ forge $*" >&2
  "${FORGE_BIN}" "$@"
}

forge_json() {
  local output="$1"
  shift
  forge --output json "$@" >"${output}" || fail "forge $* failed"
  python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "${output}" ||
    fail "forge $* did not emit JSON: $(cat "${output}")"
}

write_state() {
  cat >"${STATE_FILE}" <<EOF
PROJECT_SLUG=${PROJECT_SLUG}
PROJECT_ID=${PROJECT_ID}
API_DEPLOYMENT_ID=${API_DEPLOYMENT_ID}
WEB_DEPLOYMENT_ID=${WEB_DEPLOYMENT_ID}
FULFILLMENT_DEPLOYMENT_ID=${FULFILLMENT_DEPLOYMENT_ID}
NOTIFY_DEPLOYMENT_ID=${NOTIFY_DEPLOYMENT_ID}
API_IMAGE=${API_IMAGE}
WEB_IMAGE=${WEB_IMAGE}
FULFILLMENT_IMAGE=${FULFILLMENT_IMAGE}
NOTIFY_IMAGE=${NOTIFY_IMAGE}
DB_NAME=${DB_NAME}
EOF
}

read_state() {
  [[ -f "${STATE_FILE}" ]] || return 1
  # shellcheck disable=SC1090
  source "${STATE_FILE}"
}

delete_deployment() {
  local dep_id="$1"
  [[ -n "${dep_id}" ]] || return 0
  curl --silent --show-error -X DELETE \
    "${CONTROL_URL}/v1/deployments/${dep_id}" >/dev/null 2>&1 || true
  docker ps -aq --filter "label=forge.deployment_id=${dep_id}" \
    --filter "label=forge.managed=true" |
    while read -r cid; do
      [[ -n "${cid}" ]] || continue
      docker rm -f "${cid}" >/dev/null 2>&1 || true
    done
}

teardown() {
  echo "Tearing down demo 54 OrderPipe..."
  if read_state; then
    delete_deployment "${API_DEPLOYMENT_ID:-}"
    delete_deployment "${WEB_DEPLOYMENT_ID:-}"
    delete_deployment "${FULFILLMENT_DEPLOYMENT_ID:-}"
    delete_deployment "${NOTIFY_DEPLOYMENT_ID:-}"
    rm -f "${STATE_FILE}"
  else
    echo "  no .demo-state; best-effort cleanup of demo=54 containers"
    docker ps -aq --filter "label=forge.managed=true" --filter "label=demo=54" |
      while read -r cid; do
        [[ -n "${cid}" ]] || continue
        docker rm -f "${cid}" >/dev/null 2>&1 || true
      done
  fi
  echo "Teardown complete."
}

ensure_platform() {
  echo "Ensuring Postgres, registry, Network, Discovery, Control, Runtime, Gateway, Build..."
  "${COMPOSE[@]}" up -d "${POSTGRES_SERVICE}" "${REGISTRY_SERVICE}"
  for _ in $(seq 1 60); do
    if "${COMPOSE[@]}" exec -T "${POSTGRES_SERVICE}" pg_isready -U forge >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done
  "${COMPOSE[@]}" exec -T "${POSTGRES_SERVICE}" pg_isready -U forge >/dev/null 2>&1 ||
    fail "Postgres not ready"

  local need_recreate=0
  local auth_mode pattern strategy provisioner secrets_url disc_project dns_search net_url policy_backend
  auth_mode="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_AUTH_MODE 2>/dev/null || true)"
  pattern="$(docker exec "${GATEWAY_SERVICE}" printenv FORGE_HOST_PATTERN 2>/dev/null || true)"
  strategy="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_SCHEDULER_STRATEGY 2>/dev/null || true)"
  provisioner="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_DB_PROVISIONER 2>/dev/null || true)"
  secrets_url="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_SECRETS_URL 2>/dev/null || true)"
  disc_project="$(docker exec "${RUNTIME_SERVICE}" printenv FORGE_DISCOVERY_DEFAULT_PROJECT 2>/dev/null || true)"
  dns_search="$(docker exec "${RUNTIME_SERVICE}" printenv FORGE_NETWORK_DNS_SEARCH 2>/dev/null || true)"
  net_url="$(docker exec "${RUNTIME_SERVICE}" printenv FORGE_NETWORK_URL 2>/dev/null || true)"
  policy_backend="$(docker exec "${RUNTIME_SERVICE}" printenv FORGE_NETWORK_POLICY_BACKEND 2>/dev/null || true)"
  if [[ "${auth_mode}" != "dev" ]]; then
    need_recreate=1
  fi
  if [[ "${pattern}" != *'{service}.orderpipe.localhost'* ]]; then
    need_recreate=1
  fi
  if [[ "${strategy}" != "single-node" ]]; then
    need_recreate=1
  fi
  if [[ "${provisioner}" != "local" ]]; then
    need_recreate=1
  fi
  if [[ "${secrets_url}" != "disabled" ]]; then
    need_recreate=1
  fi
  if [[ "${disc_project}" != "${DISC_PROJECT}" ]]; then
    need_recreate=1
  fi
  if [[ "${dns_search}" != *'orderpipe.svc.forge'* ]]; then
    need_recreate=1
  fi
  if [[ "${net_url}" != *"forge-network"* && "${net_url}" != *"4110"* ]]; then
    need_recreate=1
  fi
  if [[ "${policy_backend}" != "fake" && -n "${policy_backend}" ]]; then
    : # host/nft also fine; no recreate required
  fi
  if ! docker exec "${CONTROL_SERVICE}" test -S /var/run/docker.sock 2>/dev/null; then
    need_recreate=1
  fi

  "${COMPOSE[@]}" up -d "${NETWORK_SERVICE}" "${DISCOVERY_SERVICE}"
  if [[ "${need_recreate}" -eq 1 ]]; then
    echo "Recreating Control/Runtime/Gateway with demo 54 Discovery + Network + managed-DB overlay..."
    "${COMPOSE[@]}" up -d --force-recreate \
      "${CONTROL_SERVICE}" "${RUNTIME_SERVICE}" "${GATEWAY_SERVICE}"
  else
    echo "Control/Gateway already configured for demo 54; ensuring they are up..."
    "${COMPOSE[@]}" up -d "${CONTROL_SERVICE}" "${RUNTIME_SERVICE}" "${GATEWAY_SERVICE}"
  fi
  "${COMPOSE[@]}" up -d "${BUILD_SERVICE}"

  wait_http "${CONTROL_URL}/health/ready" "Control"
  wait_http "${NETWORK_URL}/health/ready" "Network"
  wait_http "${DISCOVERY_URL}/health/ready" "Discovery"
  wait_http "${RUNTIME_URL}/health/ready" "Runtime"
  wait_http "${GATEWAY_URL}/health/ready" "Gateway"
  wait_http "${BUILD_URL}/health/ready" "Build" 60 || true

  pattern="$(docker exec "${GATEWAY_SERVICE}" printenv FORGE_HOST_PATTERN 2>/dev/null || true)"
  [[ "${pattern}" == *'{service}.orderpipe.localhost'* ]] ||
    fail "gateway FORGE_HOST_PATTERN must be '{service}.orderpipe.localhost' (got: ${pattern})"
  provisioner="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_DB_PROVISIONER 2>/dev/null || true)"
  [[ "${provisioner}" == "local" ]] ||
    fail "control FORGE_DB_PROVISIONER must be local (got: ${provisioner})"
  disc_project="$(docker exec "${RUNTIME_SERVICE}" printenv FORGE_DISCOVERY_DEFAULT_PROJECT 2>/dev/null || true)"
  [[ "${disc_project}" == "${DISC_PROJECT}" ]] ||
    fail "runtime FORGE_DISCOVERY_DEFAULT_PROJECT must be ${DISC_PROJECT} (got: ${disc_project})"
}

ensure_images() {
  if "${FORGE_BIN}" build --help >/dev/null 2>&1; then
    echo "Building via forge build --source ..."
    (
      cd "${DEMO_DIR}"
      forge build --source . --forge-yaml api/forge.yaml --tag "${API_IMAGE}"
    ) || fail "forge build api failed"
    (
      cd "${DEMO_DIR}"
      forge build --source . --forge-yaml fulfillment/forge.yaml --tag "${FULFILLMENT_IMAGE}"
    ) || fail "forge build fulfillment failed"
    (
      cd "${DEMO_DIR}"
      forge build --source . --forge-yaml notify/forge.yaml --tag "${NOTIFY_IMAGE}"
    ) || fail "forge build notify failed"
    (
      cd "${DEMO_DIR}"
      forge build --source . --forge-yaml web.forge.yaml --tag "${WEB_IMAGE}"
    ) || fail "forge build shop failed"
    return 0
  fi

  echo "forge build CLI not available; building from source with docker build+push..."
  docker build -f "${DEMO_DIR}/api/Dockerfile" -t "${API_IMAGE}" "${DEMO_DIR}" ||
    fail "docker build api failed"
  docker push "${API_IMAGE}" || fail "docker push api failed"
  docker build -f "${DEMO_DIR}/fulfillment/Dockerfile" -t "${FULFILLMENT_IMAGE}" "${DEMO_DIR}" ||
    fail "docker build fulfillment failed"
  docker push "${FULFILLMENT_IMAGE}" || fail "docker push fulfillment failed"
  docker build -f "${DEMO_DIR}/notify/Dockerfile" -t "${NOTIFY_IMAGE}" "${DEMO_DIR}" ||
    fail "docker build notify failed"
  docker push "${NOTIFY_IMAGE}" || fail "docker push notify failed"
  docker build -f "${DEMO_DIR}/Dockerfile.web" -t "${WEB_IMAGE}" "${DEMO_DIR}" ||
    fail "docker build shop failed"
  docker push "${WEB_IMAGE}" || fail "docker push shop failed"
}

ensure_cli() {
  echo "Building forge CLI..."
  make -C "${CLI_DIR}" build || fail "CLI build failed"
  [[ -x "${FORGE_BIN}" ]] || fail "forge binary missing at ${FORGE_BIN}"
  forge config set endpoint "${FORGE_ENDPOINT}" --profile "${FORGE_PROFILE}"
  forge config use "${FORGE_PROFILE}"
}

purge_stale_workloads() {
  echo "Purging leftover Control deployments + managed containers..."
  "${COMPOSE[@]}" exec -T "${POSTGRES_SERVICE}" psql -U forge -d forge -v ON_ERROR_STOP=1 <<'SQL' >/dev/null \
    || fail "could not purge stale Control deployment state"
BEGIN;
DELETE FROM control.placements;
DELETE FROM control.reconcile_status;
DELETE FROM control.deployment_events;
DELETE FROM control.deployments;
COMMIT;
SQL
  docker ps -aq --filter "label=forge.managed=true" | while read -r cid; do
    [[ -n "${cid}" ]] || continue
    docker rm -f "${cid}" >/dev/null 2>&1 || true
  done
  echo "  purged Control desired-state + managed containers"
}

wait_deployment_status() {
  local dep_id="$1" want="$2" attempts="${3:-120}"
  local status="" image="" i
  for i in $(seq 1 "${attempts}"); do
    status="$(curl --fail --silent --show-error "${CONTROL_URL}/v1/deployments/${dep_id}" |
      python3 -c 'import json,sys; print(json.load(sys.stdin).get("status",""))')" || true
    image="$(curl --fail --silent --show-error "${CONTROL_URL}/v1/deployments/${dep_id}" |
      python3 -c 'import json,sys; print(json.load(sys.stdin).get("image",""))')" || true
    if [[ "${status}" == "rolled_back" || "${status}" == "failed" ]]; then
      fail "deployment ${dep_id} entered terminal status=${status} image=${image}"
    fi
    if [[ "${status}" == "${want}" || ( "${want}" == "deployed" && "${status}" == "active" ) ]]; then
      echo "Deployment ${dep_id} status=${status} image=${image}"
      return 0
    fi
    sleep 1
  done
  fail "deployment ${dep_id} status=${status:-unknown} image=${image}, want ${want}"
}

refresh_routes() {
  curl --fail --silent --show-error -X POST "${GATEWAY_URL}/admin/routes/refresh" \
    >"${TMP_DIR}/refresh.json" || fail "POST /admin/routes/refresh failed"
}

wait_route_host() {
  local host="$1" attempts="${2:-90}"
  echo "Waiting for gateway route host=${host} ..."
  for _ in $(seq 1 "${attempts}"); do
    refresh_routes
    curl --fail --silent --show-error "${GATEWAY_URL}/admin/routes" \
      >"${TMP_DIR}/routes.json" || fail "GET /admin/routes failed"
    if HOST="${host}" python3 -c '
import json, os, sys
host = os.environ["HOST"].lower()
routes = json.load(open(sys.argv[1]))
sys.exit(0 if any(r.get("host", "").lower() == host for r in routes) else 1)
' "${TMP_DIR}/routes.json"; then
      echo "  route present: ${host}"
      return 0
    fi
    sleep 1
  done
  fail "timed out waiting for route host=${host}"
}

wait_host_http() {
  local host="$1" path="${2:-/}" expect="${3:-200}" attempts="${4:-60}"
  local code
  echo "Waiting for Host=${host}${path} → ${expect} ..."
  for _ in $(seq 1 "${attempts}"); do
    code="$(curl --silent --show-error -o "${TMP_DIR}/host-body" -w '%{http_code}' \
      -H "Host: ${host}" "${GATEWAY_URL}${path}" || echo "000")"
    if [[ "${code}" == "${expect}" ]]; then
      echo "  Host ${host}${path} → ${code}"
      return 0
    fi
    sleep 1
  done
  fail "Host ${host}${path} returned HTTP ${code:-000}, want ${expect}; body=$(cat "${TMP_DIR}/host-body" 2>/dev/null || true)"
}

extract_apply_ids() {
  python3 - "${TMP_DIR}/apply.json" <<'PY'
import json, sys
body = json.load(open(sys.argv[1]))
project_id = ""
for r in body.get("results", []):
    kind = r.get("kind") or ""
    name = r.get("name") or ""
    meta = (r.get("resource") or {}).get("metadata") or {}
    rid = meta.get("id") or ""
    if kind == "Project" and rid:
        project_id = rid
        print(f"PROJECT_ID={rid}")
    if kind == "Deployment" and name and rid:
        print(f"DEPLOYMENT:{name}={rid}")
if not project_id:
    pass
PY
}

assert_applications_ready() {
  echo "Checking applications/deployments Ready..."
  wait_deployment_status "${API_DEPLOYMENT_ID}" "deployed" 180
  wait_deployment_status "${FULFILLMENT_DEPLOYMENT_ID}" "deployed" 120
  wait_deployment_status "${NOTIFY_DEPLOYMENT_ID}" "deployed" 180
  wait_deployment_status "${WEB_DEPLOYMENT_ID}" "deployed" 120
  echo "  applications Ready (deployments active)"
}

provision_managed_db() {
  echo "Provisioning managed Database ${DB_NAME} (dependencies.database)..."
  [[ -n "${PROJECT_ID}" ]] || fail "PROJECT_ID missing; cannot create managed database"
  forge_json "${TMP_DIR}/db-create.json" --project "${PROJECT_ID}" \
    database create "${DB_NAME}" --database "${DB_LOGICAL_NAME}"
  python3 - <<'PY' "${TMP_DIR}/db-create.json" || fail "database create did not reach available"
import json, sys
body = json.load(open(sys.argv[1]))
db = body.get("database") or {}
inst = body.get("instance") or {}
status = db.get("status") or ""
inst_status = inst.get("status") or ""
assert status == "available", body
assert inst_status == "available", body
print(f"  database Ready id={db.get('id')} name={db.get('name')} instance={inst.get('id')}")
PY

  forge_json "${TMP_DIR}/db-attach.json" --project "${PROJECT_ID}" \
    database attach "${DB_NAME}" --app orderpipe-api --env-var DATABASE_URL
  python3 - <<'PY' "${TMP_DIR}/db-attach.json" || fail "attach missing secretRef"
import json, sys
body = json.load(open(sys.argv[1]))
ref = body.get("secretRef") or body.get("secret_ref") or ""
assert ref, body
assert "://" not in ref, body
print(f"  attached DATABASE_URL secretRef={ref}")
PY
}

deployment_container_id() {
  local dep_id="$1" slug="$2"
  local cid
  cid="$(docker ps -q \
    --filter "label=forge.deployment_id=${dep_id}" \
    --filter "label=forge.managed=true" | head -n1)"
  if [[ -n "${cid}" ]]; then
    echo "${cid}"
    return 0
  fi
  local short
  short="$(python3 -c 'import sys; print(sys.argv[1].replace("-", "")[:8])' "${dep_id}")"
  docker ps -q --filter "label=forge.managed=true" --filter "name=forge-${slug}-${short}-" | head -n1
}

api_container_id() {
  deployment_container_id "${API_DEPLOYMENT_ID}" "api"
}

fulfillment_container_id() {
  deployment_container_id "${FULFILLMENT_DEPLOYMENT_ID}" "fulfillment"
}

notify_container_id() {
  deployment_container_id "${NOTIFY_DEPLOYMENT_ID}" "notify"
}

container_ip() {
  local cid="$1"
  docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "${cid}" 2>/dev/null |
    awk 'NF{print; exit}' || true
}

reset_discovery_ledger() {
  echo "Resetting Discovery endpoint ledger for demo 54..."
  "${COMPOSE[@]}" exec -T "${POSTGRES_SERVICE}" psql -U forge -d forge -v ON_ERROR_STOP=1 <<'SQL' >/dev/null \
    || fail "could not reset discovery ledger"
DELETE FROM discovery.endpoints;
DELETE FROM discovery.services;
SQL
}

register_discovery_endpoint() {
  local service="$1" id="$2" ip="$3" lease="${4:-30}"
  [[ -n "${ip}" ]] || fail "register ${service}: empty IP"
  curl --fail --silent --show-error \
    -X POST "${DISCOVERY_URL}/v1/projects/${DISC_PROJECT}/environments/${DISC_ENV}/services/${service}/endpoints" \
    -H 'content-type: application/json' \
    -d "{\"id\":\"${id}\",\"node\":\"${DISC_NODE}\",\"address\":{\"ip\":\"${ip}\",\"port\":8080},\"protocol\":\"http\",\"revision\":\"v1\",\"leaseSeconds\":${lease}}" \
    >"${TMP_DIR}/reg-${id}.json" || fail "register ${id} failed"
  curl --fail --silent --show-error \
    -X POST "${DISCOVERY_URL}/v1/projects/${DISC_PROJECT}/environments/${DISC_ENV}/endpoints/${id}/renew" \
    -H 'content-type: application/json' \
    -d "{\"ready\":true,\"leaseSeconds\":${lease}}" \
    >"${TMP_DIR}/renew-${id}.json" || fail "renew ${id} failed"
  python3 -c 'import json,sys; assert json.load(open(sys.argv[1])).get("phase")=="Ready", open(sys.argv[1]).read()' \
    "${TMP_DIR}/renew-${id}.json" || fail "endpoint ${id} not Ready after renew"
  echo "  registered ${service} id=${id} ip=${ip} phase=Ready"
}

assert_discovery_ready() {
  local service="$1"
  curl --fail --silent --show-error \
    "${DISCOVERY_URL}/v1/projects/${DISC_PROJECT}/environments/${DISC_ENV}/services/${service}/endpoints" \
    >"${TMP_DIR}/ready-${service}.json" || fail "list Ready ${service} failed"
  python3 - <<'PY' "${TMP_DIR}/ready-${service}.json" "${service}" || fail "no Ready endpoints for ${service}"
import json, sys
items, svc = json.load(open(sys.argv[1])), sys.argv[2]
assert isinstance(items, list) and items, items
for it in items:
    assert it.get("phase") == "Ready" and it.get("ready") is True, it
    assert (it.get("address") or {}).get("ip"), it
print(f"  {svc}: {len(items)} Ready endpoint(s)")
PY
}

assert_dns_a() {
  local name="$1" attempts="${2:-45}"
  local out=""
  echo "Waiting for DNS A ${name} ..."
  for _ in $(seq 1 "${attempts}"); do
    out="$(dig @"127.0.0.1" -p 5053 "${name}" A +short 2>/dev/null | head -n1 || true)"
    if [[ -n "${out}" ]]; then
      echo "  ${name} → ${out}"
      return 0
    fi
    sleep 1
  done
  fail "DNS A ${name} timed out (no answer on 127.0.0.1:5053)"
}

wire_discovery_peers() {
  local api_cid ff_cid nt_cid api_ip ff_ip nt_ip
  echo "Wiring Discovery peers (project=${DISC_PROJECT} env=${DISC_ENV})..."
  reset_discovery_ledger

  api_cid="$(api_container_id)"
  ff_cid="$(fulfillment_container_id)"
  nt_cid="$(notify_container_id)"
  [[ -n "${api_cid}" ]] || fail "api container missing for discovery register"
  [[ -n "${ff_cid}" ]] || fail "fulfillment container missing for discovery register"
  [[ -n "${nt_cid}" ]] || fail "notify container missing for discovery register"

  api_ip="$(container_ip "${api_cid}")"
  ff_ip="$(container_ip "${ff_cid}")"
  nt_ip="$(container_ip "${nt_cid}")"
  [[ -n "${api_ip}" && -n "${ff_ip}" && -n "${nt_ip}" ]] ||
    fail "missing container IPs api=${api_ip} fulfillment=${ff_ip} notify=${nt_ip}"

  register_discovery_endpoint "api" "api-${PROJECT_SLUG}-0" "${api_ip}"
  register_discovery_endpoint "fulfillment" "fulfillment-${PROJECT_SLUG}-0" "${ff_ip}"
  register_discovery_endpoint "notify" "notify-${PROJECT_SLUG}-0" "${nt_ip}"

  assert_discovery_ready "api"
  assert_discovery_ready "fulfillment"
  assert_discovery_ready "notify"

  assert_dns_a "fulfillment.${DISC_ENV}.${DISC_PROJECT}.svc.forge"
  assert_dns_a "notify.${DISC_ENV}.${DISC_PROJECT}.svc.forge"
  assert_dns_a "api.${DISC_ENV}.${DISC_PROJECT}.svc.forge"
}

container_env() {
  local cid="$1" key="$2"
  docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "${cid}" 2>/dev/null |
    awk -F= -v k="${key}" '$1==k { print substr($0, index($0, "=")+1); exit }'
}

wait_database_url_injected() {
  local cid="" url="" i
  echo "Waiting for DATABASE_URL injection into API container..."
  for i in $(seq 1 120); do
    cid="$(api_container_id)"
    if [[ -n "${cid}" ]]; then
      url="$(container_env "${cid}" DATABASE_URL)"
      if [[ -n "${url}" ]]; then
        echo "  DATABASE_URL present on container ${cid:0:12}"
        return 0
      fi
    fi
    sleep 1
  done
  fail "DATABASE_URL never appeared on API container"
}

ensure_cluster_network() {
  echo "Ensuring Network ${NETWORK_NAME} ..."
  local code
  code="$(curl -s -o "${TMP_DIR}/net-create.json" -w '%{http_code}' -X POST "${NETWORK_URL}/v1/networks" \
    -H 'content-type: application/json' \
    -d "{\"name\":\"${NETWORK_NAME}\",\"spec\":{\"clusterCidr\":\"10.100.0.0/16\",\"nodePrefixLength\":24}}")"
  if [[ "${code}" == "201" ]]; then
    echo "  created ${NETWORK_NAME}"
    return 0
  fi
  if [[ "${code}" == "409" ]]; then
    curl --fail --silent --show-error "${NETWORK_URL}/v1/networks/${NETWORK_NAME}" \
      >"${TMP_DIR}/net-get.json" || fail "GET network after conflict failed"
    python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert d.get("status",{}).get("phase")=="Ready", d' \
      "${TMP_DIR}/net-get.json" || fail "network exists but not Ready"
    echo "  reused existing Ready ${NETWORK_NAME}"
    return 0
  fi
  fail "create network HTTP ${code}: $(cat "${TMP_DIR}/net-create.json")"
}

ensure_node_lease() {
  echo "Allocating node lease for ${DISC_NODE} on ${NETWORK_NAME}..."
  curl --fail --silent --show-error \
    -X POST "${NETWORK_URL}/v1/networks/${NETWORK_NAME}/node-leases" \
    -H 'content-type: application/json' \
    -d "{\"node_id\":\"${DISC_NODE}\"}" \
    >"${TMP_DIR}/node-lease.json" || fail "allocate node lease failed"
  python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert d.get("cidr") or d.get("node_id"), d; print("  node lease cidr=%s node=%s" % (d.get("cidr"), d.get("node_id")))' \
    "${TMP_DIR}/node-lease.json" || fail "node lease response invalid"
}

allocate_workload_lease() {
  local workload_id="$1"
  curl --fail --silent --show-error \
    -X POST "${NETWORK_URL}/v1/networks/${NETWORK_NAME}/workload-leases" \
    -H 'content-type: application/json' \
    -d "{\"node_id\":\"${DISC_NODE}\",\"workload_id\":\"${workload_id}\"}" \
    >"${TMP_DIR}/wl-${workload_id}.json" || fail "allocate workload lease ${workload_id} failed"
  python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert d.get("address"), d; print(d["address"])' \
    "${TMP_DIR}/wl-${workload_id}.json" || fail "workload lease ${workload_id} missing address"
}

upsert_placement() {
  local workload_id="$1" application="$2" service="$3"
  curl --fail --silent --show-error \
    -X PUT "${NETWORK_URL}/v1/workload-placements/${workload_id}" \
    -H 'content-type: application/json' \
    -d "{\"organization\":\"${NET_ORG}\",\"project\":\"${DISC_PROJECT}\",\"environment\":\"${DISC_ENV}\",\"node_id\":\"${DISC_NODE}\",\"application\":\"${application}\",\"service\":\"${service}\"}" \
    >"${TMP_DIR}/placement-${workload_id}.json" || fail "upsert placement ${workload_id} failed"
  echo "  placement ${workload_id} app=${application} service=${service}"
}

set_env_default_policy() {
  local policy="$1"
  curl --fail --silent --show-error \
    -X PATCH "${NETWORK_URL}/v1/projects/${DISC_PROJECT}/environments/${DISC_ENV}/network-defaults" \
    -H 'content-type: application/json' \
    -d "{\"defaultPolicy\":\"${policy}\"}" \
    >"${TMP_DIR}/defaults.json" || fail "patch network-defaults failed"
  echo "  environment defaultPolicy=${policy}"
}

delete_network_policy() {
  local name="$1"
  local code
  code="$(curl -s -o "${TMP_DIR}/policy-del-${name}.json" -w '%{http_code}' \
    -X DELETE "${NETWORK_URL}/v1/projects/${DISC_PROJECT}/environments/${DISC_ENV}/network-policies/${name}")"
  [[ "${code}" == "204" || "${code}" == "200" || "${code}" == "404" ]] ||
    fail "delete NetworkPolicy ${name} HTTP ${code}: $(cat "${TMP_DIR}/policy-del-${name}.json")"
}

create_network_policy() {
  local name="$1" target_app="$2" from_service="$3"
  curl --fail --silent --show-error \
    -X POST "${NETWORK_URL}/v1/projects/${DISC_PROJECT}/environments/${DISC_ENV}/network-policies" \
    -H 'content-type: application/json' \
    -d "{\"name\":\"${name}\",\"organization\":\"${NET_ORG}\",\"spec\":{\"target\":{\"application\":\"${target_app}\"},\"ingress\":[{\"from\":{\"service\":\"${from_service}\"},\"ports\":[{\"port\":8080,\"protocol\":\"tcp\"}]}]}}" \
    >"${TMP_DIR}/policy-${name}.json" || fail "create NetworkPolicy ${name} failed"
  python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert d.get("status",{}).get("phase")=="Ready", d' \
    "${TMP_DIR}/policy-${name}.json" || fail "NetworkPolicy ${name} not Ready"
  echo "  NetworkPolicy ${name} Ready (target=${target_app} from=${from_service})"
}

read_denied_counter() {
  curl --fail --silent --show-error "${NETWORK_URL}/metrics" >"${TMP_DIR}/metrics.txt" ||
    fail "GET /metrics failed"
  python3 - "${TMP_DIR}/metrics.txt" <<'PY'
import re, sys
text = open(sys.argv[1]).read()
m = re.search(r'^forge_network_policy_denied_total\s+(\d+(?:\.\d+)?)', text, re.M)
print(m.group(1) if m else "0")
PY
}

verify_denied_counter_bumped() {
  local before="$1"
  curl --fail --silent --show-error "${NETWORK_URL}/metrics" >"${TMP_DIR}/metrics.txt" ||
    fail "GET /metrics failed"
  if ! BEFORE="${before}" python3 - "${TMP_DIR}/metrics.txt" <<'PY'
import os, re, sys
text = open(sys.argv[1]).read()
m = re.search(r'^forge_network_policy_denied_total\s+(\d+(?:\.\d+)?)', text, re.M)
assert m, text
got = float(m.group(1))
before = float(os.environ["BEFORE"])
assert got > before, f"denied_total={got} before={before}"
print(f"  forge_network_policy_denied_total {before} → {got}")
PY
  then
    fail "forge_network_policy_denied_total did not increase"
  fi
}

verify_policy_rules() {
  curl --fail --silent --show-error \
    "${NETWORK_URL}/v1/nodes/${DISC_NODE}/network-policy-rules" \
    >"${TMP_DIR}/policy-rules.json" || fail "GET network-policy-rules failed"
  python3 - "${TMP_DIR}/policy-rules.json" <<'PY' || fail "compiled NetworkPolicy rules missing allow/deny"
import json, sys
rs = json.load(open(sys.argv[1]))
rules = rs.get("rules") or []
assert rules, rs
actions = {r.get("action") for r in rules}
assert "allow" in actions, rs
assert "deny" in actions, rs
# Allow must cover order-api → fulfillment / notify (explicit-policy).
allows = [r for r in rules if r.get("action") == "allow" and r.get("direction") == "ingress"]
assert allows, rs
print(f"  node={rs.get('node_id')} generation={rs.get('generation')} rules={len(rules)} allow={len(allows)} deny={sum(1 for r in rules if r.get('action')=='deny')}")
PY
}

wire_network_policy() {
  local api_wl ff_wl nt_wl
  echo "Wiring NetworkPolicy orderpipe-mesh (project=${DISC_PROJECT} env=${DISC_ENV})..."
  ensure_cluster_network
  ensure_node_lease

  api_wl="${API_DEPLOYMENT_ID}"
  ff_wl="${FULFILLMENT_DEPLOYMENT_ID}"
  nt_wl="${NOTIFY_DEPLOYMENT_ID}"
  [[ -n "${api_wl}" && -n "${ff_wl}" && -n "${nt_wl}" ]] ||
    fail "deployment ids required for NetworkPolicy placements"

  echo "  allocating overlay workload leases..."
  allocate_workload_lease "${api_wl}" >/dev/null
  allocate_workload_lease "${ff_wl}" >/dev/null
  allocate_workload_lease "${nt_wl}" >/dev/null
  echo "  leases ready for api/fulfillment/notify"

  upsert_placement "${api_wl}" "orderpipe-api" "api"
  upsert_placement "${ff_wl}" "orderpipe-fulfillment" "fulfillment"
  upsert_placement "${nt_wl}" "orderpipe-notify" "notify"

  set_env_default_policy "deny-all"
  delete_network_policy "orderpipe-mesh"
  delete_network_policy "orderpipe-mesh-notify"
  create_network_policy "orderpipe-mesh" "orderpipe-fulfillment" "api"
  create_network_policy "orderpipe-mesh-notify" "orderpipe-notify" "api"
  sleep 1
  verify_policy_rules
}

prove_network_policy() {
  local code deny_before
  echo "Proving NetworkPolicy allow + deny (orderpipe-mesh)..."

  # Allowed pair: order-api → fulfillment already exercised by place-order; re-check fulfill path.
  code="$(curl --silent --show-error -o "${TMP_DIR}/allow-fulfill.json" -w '%{http_code}' \
    -H "Host: ${FULFILLMENT_HOST}" -H 'content-type: application/json' \
    -d '{"orderId":"policy-allow-probe"}' \
    "${GATEWAY_URL}/fulfill" || echo "000")"
  [[ "${code}" == "202" ]] || fail "allowed order-api→fulfillment HTTP ${code}: $(cat "${TMP_DIR}/allow-fulfill.json")"
  echo "  allowed pair order-api→fulfillment → HTTP 202"

  deny_before="$(read_denied_counter)"
  code="$(curl --silent --show-error -o "${TMP_DIR}/denied-call.json" -w '%{http_code}' \
    -H "Host: ${FULFILLMENT_HOST}" -H 'content-type: application/json' \
    -d "{\"fromWorkload\":\"${FULFILLMENT_DEPLOYMENT_ID}\",\"toWorkload\":\"${NOTIFY_DEPLOYMENT_ID}\",\"reason\":\"networkpolicy:policy-default-deny\"}" \
    "${GATEWAY_URL}/debug/denied-call" || echo "000")"
  [[ "${code}" == "403" ]] || fail "denied-call HTTP ${code}: $(cat "${TMP_DIR}/denied-call.json")"
  python3 - <<'PY' "${TMP_DIR}/denied-call.json" || fail "denied-call response invalid"
import json, sys
body = json.load(open(sys.argv[1]))
assert body.get("blocked") is True, body
assert body.get("event") == "network.policy.denied", body
assert body.get("pair") == "fulfillment→notify", body
assert body.get("notifyAttempted") is False, body
print("  denied pair fulfillment→notify → blocked + network.policy.denied")
PY
  verify_denied_counter_bumped "${deny_before}"
}

prove_place_order() {
  local email="buyer-$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')@example.com"
  local code order_id cid
  echo "Proving place-order reaches peers via Discovery (*.svc.forge)..."
  code="$(curl --silent --show-error -o "${TMP_DIR}/place-order.json" -w '%{http_code}' \
    -H "Host: ${API_HOST}" -H 'content-type: application/json' \
    -d "{\"customerEmail\":\"${email}\",\"items\":[{\"sku\":\"mug\",\"qty\":1}]}" \
    "${GATEWAY_URL}/orders" || echo "000")"
  [[ "${code}" == "201" ]] || fail "place order HTTP ${code}: $(cat "${TMP_DIR}/place-order.json")"
  order_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "${TMP_DIR}/place-order.json")"
  [[ -n "${order_id}" ]] || fail "place order missing id"
  EMAIL="${email}" ORDER_ID="${order_id}" python3 - <<'PY' "${TMP_DIR}/place-order.json" || fail "place order response invalid"
import json, os, sys
body = json.load(open(sys.argv[1]))
assert body.get("status") == "placed", body
assert body.get("customerEmail") == os.environ["EMAIL"], body
assert body.get("totalCents") == 1800, body
assert body.get("items"), body
print(f"  created order id={os.environ['ORDER_ID']} status=placed")
PY

  echo "  verifying fulfillment accepted order via Discovery peer call..."
  code="000"
  for _ in $(seq 1 30); do
    code="$(curl --silent --show-error -o "${TMP_DIR}/fulfillments.json" -w '%{http_code}' \
      -H "Host: ${FULFILLMENT_HOST}" "${GATEWAY_URL}/fulfillments" || echo "000")"
    if [[ "${code}" == "200" ]] && ORDER_ID="${order_id}" python3 - <<'PY' "${TMP_DIR}/fulfillments.json"
import json, os, sys
body = json.load(open(sys.argv[1]))
items = body.get("items") or []
sys.exit(0 if any(i.get("orderId") == os.environ["ORDER_ID"] for i in items) else 1)
PY
    then
      echo "  fulfillment recorded orderId=${order_id}"
      break
    fi
    sleep 1
  done
  [[ "${code}" == "200" ]] || fail "fulfillments list HTTP ${code}"
  ORDER_ID="${order_id}" python3 - <<'PY' "${TMP_DIR}/fulfillments.json" || fail "fulfillment missing order after Discovery peer call"
import json, os, sys
body = json.load(open(sys.argv[1]))
items = body.get("items") or []
assert any(i.get("orderId") == os.environ["ORDER_ID"] for i in items), body
PY

  echo "  verifying notify queued order via Discovery peer call..."
  code="000"
  for _ in $(seq 1 30); do
    code="$(curl --silent --show-error -o "${TMP_DIR}/notifications.json" -w '%{http_code}' \
      -H "Host: ${NOTIFY_HOST}" "${GATEWAY_URL}/notifications" || echo "000")"
    if [[ "${code}" == "200" ]] && ORDER_ID="${order_id}" python3 - <<'PY' "${TMP_DIR}/notifications.json"
import json, os, sys
body = json.load(open(sys.argv[1]))
items = body.get("items") or []
sys.exit(0 if any(i.get("orderId") == os.environ["ORDER_ID"] for i in items) else 1)
PY
    then
      echo "  notify recorded orderId=${order_id}"
      break
    fi
    sleep 1
  done
  [[ "${code}" == "200" ]] || fail "notifications list HTTP ${code}"
  ORDER_ID="${order_id}" python3 - <<'PY' "${TMP_DIR}/notifications.json" || fail "notify missing order after Discovery peer call"
import json, os, sys
body = json.load(open(sys.argv[1]))
items = body.get("items") or []
assert any(i.get("orderId") == os.environ["ORDER_ID"] for i in items), body
PY

  cid="$(api_container_id)"
  [[ -n "${cid}" ]] || fail "API container missing before restart"
  echo "  restarting API container ${cid:0:12}..."
  docker restart "${cid}" >/dev/null || fail "docker restart api failed"
  wait_host_http "${API_HOST}" "/health/ready" 200 120
  refresh_routes
  # Re-register api endpoint after restart (container IP may change).
  api_ip="$(container_ip "$(api_container_id)")"
  register_discovery_endpoint "api" "api-${PROJECT_SLUG}-0" "${api_ip}"

  code="000"
  for _ in $(seq 1 60); do
    code="$(curl --silent --show-error -o "${TMP_DIR}/get-order.json" -w '%{http_code}' \
      -H "Host: ${API_HOST}" \
      "${GATEWAY_URL}/orders/${order_id}" || echo "000")"
    if [[ "${code}" == "200" ]]; then
      break
    fi
    sleep 1
  done
  [[ "${code}" == "200" ]] || fail "get order after restart HTTP ${code}: $(cat "${TMP_DIR}/get-order.json" 2>/dev/null || true)"
  ORDER_ID="${order_id}" EMAIL="${email}" python3 - <<'PY' "${TMP_DIR}/get-order.json" || fail "order missing after restart"
import json, os, sys
body = json.load(open(sys.argv[1]))
assert body.get("id") == os.environ["ORDER_ID"], body
assert body.get("customerEmail") == os.environ["EMAIL"], body
assert body.get("status") == "placed", body
print(f"  persisted order id={os.environ['ORDER_ID']}")
PY
}

deploy() {
  if [[ -f "${STATE_FILE}" ]]; then
    teardown
  fi

  ensure_platform
  ensure_cli
  ensure_images
  purge_stale_workloads

  SUFFIX="$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')"
  PROJECT_NAME="OrderPipe ${SUFFIX}"
  PROJECT_SLUG="orderpipe-${SUFFIX}"

  echo "Rendering forge.yaml → apply (project=${PROJECT_SLUG})..."
  PROJECT_NAME="${PROJECT_NAME}" PROJECT_SLUG="${PROJECT_SLUG}" \
    API_IMAGE="${API_IMAGE}" WEB_IMAGE="${WEB_IMAGE}" \
    FULFILLMENT_IMAGE="${FULFILLMENT_IMAGE}" NOTIFY_IMAGE="${NOTIFY_IMAGE}" \
    envsubst '${PROJECT_NAME} ${PROJECT_SLUG} ${API_IMAGE} ${WEB_IMAGE} ${FULFILLMENT_IMAGE} ${NOTIFY_IMAGE}' \
    <"${DEMO_DIR}/forge.yaml" >"${TMP_DIR}/forge.yaml"

  forge_json "${TMP_DIR}/apply.json" apply -f "${TMP_DIR}/forge.yaml"

  PROJECT_ID=""
  API_DEPLOYMENT_ID=""
  WEB_DEPLOYMENT_ID=""
  FULFILLMENT_DEPLOYMENT_ID=""
  NOTIFY_DEPLOYMENT_ID=""
  while IFS= read -r line; do
    case "${line}" in
      PROJECT_ID=*) PROJECT_ID="${line#PROJECT_ID=}" ;;
      DEPLOYMENT:orderpipe-api=*) API_DEPLOYMENT_ID="${line#DEPLOYMENT:orderpipe-api=}" ;;
      DEPLOYMENT:orderpipe-shop=*) WEB_DEPLOYMENT_ID="${line#DEPLOYMENT:orderpipe-shop=}" ;;
      DEPLOYMENT:orderpipe-fulfillment=*) FULFILLMENT_DEPLOYMENT_ID="${line#DEPLOYMENT:orderpipe-fulfillment=}" ;;
      DEPLOYMENT:orderpipe-notify=*) NOTIFY_DEPLOYMENT_ID="${line#DEPLOYMENT:orderpipe-notify=}" ;;
    esac
  done < <(extract_apply_ids)

  [[ -n "${API_DEPLOYMENT_ID}" ]] || fail "orderpipe-api Deployment id missing from apply"
  [[ -n "${WEB_DEPLOYMENT_ID}" ]] || fail "orderpipe-shop Deployment id missing from apply"
  [[ -n "${FULFILLMENT_DEPLOYMENT_ID}" ]] || fail "orderpipe-fulfillment Deployment id missing from apply"
  [[ -n "${NOTIFY_DEPLOYMENT_ID}" ]] || fail "orderpipe-notify Deployment id missing from apply"

  if [[ -z "${PROJECT_ID}" ]]; then
    PROJECT_ID="$(curl --fail --silent --show-error "${CONTROL_URL}/v1/projects" |
      PROJECT_SLUG="${PROJECT_SLUG}" python3 -c '
import json,os,sys
slug=os.environ["PROJECT_SLUG"]
for p in json.load(sys.stdin):
    if p.get("slug")==slug or p.get("name")==slug:
        print(p["id"]); break
')" || true
  fi
  [[ -n "${PROJECT_ID}" ]] || fail "PROJECT_ID missing from apply/list"
  echo "Deployments api=${API_DEPLOYMENT_ID} fulfillment=${FULFILLMENT_DEPLOYMENT_ID} notify=${NOTIFY_DEPLOYMENT_ID} shop=${WEB_DEPLOYMENT_ID} project=${PROJECT_ID}"

  provision_managed_db
  wait_database_url_injected
  assert_applications_ready
  wait_route_host "${API_HOST}" 90
  wait_route_host "${SHOP_HOST}" 90
  wait_route_host "${FULFILLMENT_HOST}" 90
  wait_route_host "${NOTIFY_HOST}" 90
  wait_host_http "${API_HOST}" "/health/ready" 200 90
  wait_host_http "${FULFILLMENT_HOST}" "/health/ready" 200 90
  wait_host_http "${NOTIFY_HOST}" "/health/ready" 200 90
  wait_host_http "${SHOP_HOST}" "/" 200 60

  if "${FORGE_BIN}" wait --help >/dev/null 2>&1; then
    forge wait "application/orderpipe-api" --for=condition=Ready --timeout=2m ||
      fail "forge wait orderpipe-api failed"
    forge wait "application/orderpipe-fulfillment" --for=condition=Ready --timeout=2m ||
      fail "forge wait orderpipe-fulfillment failed"
    forge wait "application/orderpipe-notify" --for=condition=Ready --timeout=2m ||
      fail "forge wait orderpipe-notify failed"
    forge wait "application/orderpipe-shop" --for=condition=Ready --timeout=2m ||
      fail "forge wait orderpipe-shop failed"
  fi

  write_state
  bash "${DEMO_DIR}/seed.sh" || fail "seed.sh failed"
  wire_discovery_peers
  bash "${DEMO_DIR}/check-discovery.sh" || fail "discovery contract check failed"
  wire_network_policy
  prove_place_order
  prove_network_policy

  echo
  echo "demo 54 deploy READY (OrderPipe Discovery + NetworkPolicy)"
  echo "  Shop:         http://${SHOP_HOST}:4000/"
  echo "  API:          http://${API_HOST}:4000/health/ready"
  echo "  Fulfillment:  http://${FULFILLMENT_HOST}:4000/health/ready"
  echo "  Notify:       http://${NOTIFY_HOST}:4000/health/ready"
  echo "  Discovery:    ${DISC_PROJECT}/${DISC_ENV} (*.svc.forge)"
  echo "  DNS:          fulfillment/notify/api.${DISC_ENV}.${DISC_PROJECT}.svc.forge"
  echo "  Network:      orderpipe-mesh (allow api→fulfillment/notify; deny fulfillment↔notify)"
  echo "  API image:    ${API_IMAGE}"
  echo "  Shop image:   ${WEB_IMAGE}"
  echo "  Fulfillment:  ${FULFILLMENT_IMAGE}"
  echo "  Notify:       ${NOTIFY_IMAGE}"
  echo "  Database:     ${DB_NAME} (Ready)"
  echo "  Deployments:  api=${API_DEPLOYMENT_ID} fulfillment=${FULFILLMENT_DEPLOYMENT_ID} notify=${NOTIFY_DEPLOYMENT_ID} shop=${WEB_DEPLOYMENT_ID}"
  echo "  Project:      ${PROJECT_SLUG} (${PROJECT_ID})"
}

case "${1:-}" in
  --down|down|teardown)
    teardown
    ;;
  ""|up|deploy)
    deploy
    ;;
  *)
    echo "Usage: $0 [--down]" >&2
    exit 2
    ;;
esac
