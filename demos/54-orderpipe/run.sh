#!/usr/bin/env bash
# Demo 54: OrderPipe multi-service + managed Postgres (epic 54.01).
# Usage:
#   demos/54-orderpipe/run.sh          # build → apply → DB → Ready → seed → place-order proof
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
CONTROL_SERVICE="forge-control"
RUNTIME_SERVICE="forge-runtime"
GATEWAY_SERVICE="forge-gateway"
BUILD_SERVICE="forge-build"
POSTGRES_SERVICE="postgres"
REGISTRY_SERVICE="registry"
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
  echo "Ensuring Postgres, registry, Control, Runtime, Gateway, Build..."
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
  local auth_mode pattern strategy provisioner secrets_url
  auth_mode="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_AUTH_MODE 2>/dev/null || true)"
  pattern="$(docker exec "${GATEWAY_SERVICE}" printenv FORGE_HOST_PATTERN 2>/dev/null || true)"
  strategy="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_SCHEDULER_STRATEGY 2>/dev/null || true)"
  provisioner="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_DB_PROVISIONER 2>/dev/null || true)"
  secrets_url="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_SECRETS_URL 2>/dev/null || true)"
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
  if ! docker exec "${CONTROL_SERVICE}" test -S /var/run/docker.sock 2>/dev/null; then
    need_recreate=1
  fi

  if [[ "${need_recreate}" -eq 1 ]]; then
    echo "Recreating Control/Runtime/Gateway with demo 54 managed-DB overlay..."
    "${COMPOSE[@]}" up -d --force-recreate \
      "${CONTROL_SERVICE}" "${RUNTIME_SERVICE}" "${GATEWAY_SERVICE}"
  else
    echo "Control/Gateway already configured for demo 54; ensuring they are up..."
    "${COMPOSE[@]}" up -d "${CONTROL_SERVICE}" "${RUNTIME_SERVICE}" "${GATEWAY_SERVICE}"
  fi
  "${COMPOSE[@]}" up -d "${BUILD_SERVICE}"

  wait_http "${CONTROL_URL}/health/ready" "Control"
  wait_http "${RUNTIME_URL}/health/ready" "Runtime"
  wait_http "${GATEWAY_URL}/health/ready" "Gateway"
  wait_http "${BUILD_URL}/health/ready" "Build" 60 || true

  pattern="$(docker exec "${GATEWAY_SERVICE}" printenv FORGE_HOST_PATTERN 2>/dev/null || true)"
  [[ "${pattern}" == *'{service}.orderpipe.localhost'* ]] ||
    fail "gateway FORGE_HOST_PATTERN must be '{service}.orderpipe.localhost' (got: ${pattern})"
  provisioner="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_DB_PROVISIONER 2>/dev/null || true)"
  [[ "${provisioner}" == "local" ]] ||
    fail "control FORGE_DB_PROVISIONER must be local (got: ${provisioner})"
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

api_container_id() {
  local cid
  cid="$(docker ps -q \
    --filter "label=forge.deployment_id=${API_DEPLOYMENT_ID}" \
    --filter "label=forge.managed=true" | head -n1)"
  if [[ -n "${cid}" ]]; then
    echo "${cid}"
    return 0
  fi
  local short
  short="$(python3 -c 'import sys; print(sys.argv[1].replace("-", "")[:8])' "${API_DEPLOYMENT_ID}")"
  docker ps -q --filter "label=forge.managed=true" --filter "name=forge-api-${short}-" | head -n1
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

prove_place_order() {
  local email="buyer-$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')@example.com"
  local code order_id cid
  echo "Proving place-order creates a persisted orders row..."
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

  cid="$(api_container_id)"
  [[ -n "${cid}" ]] || fail "API container missing before restart"
  echo "  restarting API container ${cid:0:12}..."
  docker restart "${cid}" >/dev/null || fail "docker restart api failed"
  wait_host_http "${API_HOST}" "/health/ready" 200 120
  refresh_routes

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
  prove_place_order

  echo
  echo "demo 54 deploy READY (OrderPipe multi-service + Postgres scaffold)"
  echo "  Shop:         http://${SHOP_HOST}:4000/"
  echo "  API:          http://${API_HOST}:4000/health/ready"
  echo "  Fulfillment:  http://${FULFILLMENT_HOST}:4000/health/ready"
  echo "  Notify:       http://${NOTIFY_HOST}:4000/health/ready"
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
