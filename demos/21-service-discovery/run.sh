#!/usr/bin/env bash
# Demo 21: service discovery gate (epic 21 acceptance).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/21-service-discovery"
APP_DIR="${ROOT_DIR}/demos/07-rolling-deployment/apps/demo"
export FORGE_AUTH_MODE="${FORGE_AUTH_MODE:-dev}"
export FORGE_LIFECYCLE_OWNER="${FORGE_LIFECYCLE_OWNER:-control}"
export FORGE_RECONCILE_INTERVAL_MS="${FORGE_RECONCILE_INTERVAL_MS:-1000}"
export FORGE_READINESS_POLL_MS="${FORGE_READINESS_POLL_MS:-500}"
export FORGE_READINESS_MAX_WAIT_S="${FORGE_READINESS_MAX_WAIT_S:-90}"
export FORGE_RESOURCE_API_ENABLED="${FORGE_RESOURCE_API_ENABLED:-true}"
export FORGE_SCHEDULER_STRATEGY="${FORGE_SCHEDULER_STRATEGY:-least-allocated}"
export FORGE_ANTI_AFFINITY_DEFAULT="${FORGE_ANTI_AFFINITY_DEFAULT:-soft}"
export FORGE_SECRETS_URL="${FORGE_SECRETS_URL:-disabled}"
export FORGE_PROBE_INTERVAL_SECONDS="${FORGE_PROBE_INTERVAL_SECONDS:-2}"
export FORGE_PROBE_FAILURE_THRESHOLD="${FORGE_PROBE_FAILURE_THRESHOLD:-2}"
export FORGE_NODE_HEARTBEAT_TIMEOUT_S="${FORGE_NODE_HEARTBEAT_TIMEOUT_S:-8}"
export FORGE_RESCHEDULE_GRACE_S="${FORGE_RESCHEDULE_GRACE_S:-3}"
export FORGE_LIVENESS_INTERVAL_MS="${FORGE_LIVENESS_INTERVAL_MS:-2000}"
export FORGE_HEARTBEAT_INTERVAL_MS="${FORGE_HEARTBEAT_INTERVAL_MS:-2000}"
export FORGE_DISCOVERY_LEASE_SECONDS_DEFAULT="${FORGE_DISCOVERY_LEASE_SECONDS_DEFAULT:-8}"
export FORGE_DISCOVERY_SWEEP_INTERVAL_SECONDS="${FORGE_DISCOVERY_SWEEP_INTERVAL_SECONDS:-2}"
export FORGE_DISCOVERY_LEASE_SECONDS="${FORGE_DISCOVERY_LEASE_SECONDS:-8}"
export FORGE_DISCOVERY_DEFAULT_PROJECT="${FORGE_DISCOVERY_DEFAULT_PROJECT:-demo}"
export FORGE_DISCOVERY_DEFAULT_ENVIRONMENT="${FORGE_DISCOVERY_DEFAULT_ENVIRONMENT:-local}"
export FORGE_ROUTE_SOURCE="${FORGE_ROUTE_SOURCE:-discovery}"
export FORGE_ROUTE_SYNC_INTERVAL_SECONDS="${FORGE_ROUTE_SYNC_INTERVAL_SECONDS:-2}"
export FORGE_OTEL_ENABLED="${FORGE_OTEL_ENABLED:-false}"
export COMPOSE_PARALLEL_LIMIT="${COMPOSE_PARALLEL_LIMIT:-1}"

COMPOSE=(
  docker compose
  -f "${ROOT_DIR}/compose.yaml"
  -f "${DEMO_DIR}/docker-compose.yml"
  --project-directory "${ROOT_DIR}"
)
CONTROL_URL="${FORGE_CONTROL_URL:-http://127.0.0.1:4001}"
RUNTIME_A_URL="${FORGE_RUNTIME_A_URL:-http://127.0.0.1:4102}"
RUNTIME_B_URL="${FORGE_RUNTIME_B_URL:-http://127.0.0.1:4112}"
DISCOVERY_URL="${FORGE_DISCOVERY_URL_HOST:-http://127.0.0.1:4109}"
GATEWAY_URL="${FORGE_GATEWAY_URL:-http://127.0.0.1:4000}"
CONTROL_SERVICE="forge-control"
RUNTIME_A_SERVICE="forge-runtime"
RUNTIME_B_SERVICE="forge-runtime-b"
DISCOVERY_SERVICE="forge-discovery"
GATEWAY_SERVICE="forge-gateway"
POSTGRES_SERVICE="postgres"
REGISTRY_SERVICE="registry"
CLI_DIR="${ROOT_DIR}/tools/forge-cli"
FORGE_BIN="${CLI_DIR}/forge"
REGISTRY="${FORGE_REGISTRY:-localhost:5000}"
DEMO_IMAGE="${DEMO_IMAGE:-${REGISTRY}/demo-discovery:v1}"

# Discovery directory scope for .svc.forge (epic success demo naming).
DISC_PROJECT="demo"
DISC_ENV="local"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-demo-21.XXXXXX")"
CONFIG_HOME="${TMP_DIR}/xdg-config"
WATCH_PID=""
RENEW_PID=""
WATCH_LOG="${TMP_DIR}/watch.sse"
export XDG_CONFIG_HOME="${CONFIG_HOME}"
export CI=1
export FORGE_PROFILE="${FORGE_PROFILE:-demo21}"
export FORGE_ENDPOINT="${FORGE_ENDPOINT:-${CONTROL_URL}}"
mkdir -p "${CONFIG_HOME}"

TRACKED_DEPLOYMENTS=()
SUFFIX="$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')"
PROJECT_NAME="Discovery Shop ${SUFFIX}"
PROJECT_SLUG="shop-${SUFFIX}"

cleanup() {
  local dep
  if [[ -n "${RENEW_PID}" ]]; then
    kill "${RENEW_PID}" >/dev/null 2>&1 || true
    wait "${RENEW_PID}" 2>/dev/null || true
    RENEW_PID=""
  fi
  if [[ -n "${WATCH_PID}" ]]; then
    kill "${WATCH_PID}" >/dev/null 2>&1 || true
    wait "${WATCH_PID}" 2>/dev/null || true
    WATCH_PID=""
  fi
  if ((${#TRACKED_DEPLOYMENTS[@]} > 0)); then
    for dep in "${TRACKED_DEPLOYMENTS[@]}"; do
      [[ -n "${dep}" ]] || continue
      curl --silent --show-error -X DELETE "${CONTROL_URL}/v1/deployments/${dep}" >/dev/null 2>&1 || true
    done
  fi
  docker ps -aq --filter "label=forge.managed=true" | while read -r cid; do
    [[ -n "${cid}" ]] || continue
    docker rm -f "${cid}" >/dev/null 2>&1 || true
  done
  "${COMPOSE[@]}" stop \
    "${GATEWAY_SERVICE}" "${RUNTIME_B_SERVICE}" "${RUNTIME_A_SERVICE}" \
    "${DISCOVERY_SERVICE}" "${CONTROL_SERVICE}" >/dev/null 2>&1 || true
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

dump_context() {
  echo "--- Discovery /v1/services ---" >&2
  curl --silent --show-error "${DISCOVERY_URL}/v1/services" >&2 || true
  echo >&2
  echo "--- Gateway /admin/routes ---" >&2
  curl --silent --show-error "${GATEWAY_URL}/admin/routes" >&2 || true
  echo >&2
  echo "--- ${DISCOVERY_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=80 "${DISCOVERY_SERVICE}" >&2 || true
  echo "--- ${GATEWAY_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${GATEWAY_SERVICE}" >&2 || true
  echo "--- ${RUNTIME_A_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=40 "${RUNTIME_A_SERVICE}" >&2 || true
  echo "--- ${RUNTIME_B_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=40 "${RUNTIME_B_SERVICE}" >&2 || true
}

fail() {
  echo "Demo 21 failed: $*" >&2
  dump_context
  exit 1
}

wait_http() {
  local url="$1" label="$2" attempts="${3:-120}"
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

compose_up_one() {
  local svc="$1"
  if ! "${COMPOSE[@]}" up -d --build --force-recreate "${svc}"; then
    echo "${svc} build/up failed once; retrying sequentially..." >&2
    COMPOSE_PARALLEL_LIMIT=1 "${COMPOSE[@]}" build "${svc}" || fail "${svc} rebuild failed"
    "${COMPOSE[@]}" up -d --force-recreate "${svc}" || fail "${svc} up failed"
  fi
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

read_id() {
  python3 -c 'import json,sys,uuid; value=json.load(open(sys.argv[1]))["id"]; uuid.UUID(value); print(value)' "$1" ||
    fail "response missing UUID id: $(cat "$1")"
}

track_deployment() {
  TRACKED_DEPLOYMENTS+=("$1")
}

ensure_demo_image() {
  echo "Building and pushing ${DEMO_IMAGE} ..."
  docker build --build-arg VERSION=discovery -t "${DEMO_IMAGE}" "${APP_DIR}" ||
    fail "docker build failed"
  docker push "${DEMO_IMAGE}" >/dev/null || fail "docker push failed"
}

purge_stale_state() {
  echo "Purging leftover Control + Discovery state (local Postgres)..."
  "${COMPOSE[@]}" exec -T "${POSTGRES_SERVICE}" psql -U forge -d forge -v ON_ERROR_STOP=1 <<'SQL' >/dev/null \
    || fail "could not purge stale state"
BEGIN;
DELETE FROM control.placements;
DELETE FROM control.reconcile_status;
DELETE FROM control.deployment_events;
DELETE FROM control.deployments;
DELETE FROM control.db_attachment;
DELETE FROM control.db_backup;
DELETE FROM control.db_credential;
DELETE FROM control.db_database;
DELETE FROM control.db_instance;
DELETE FROM control.services;
DELETE FROM control.applications;
DELETE FROM control.environments;
DELETE FROM control.projects;
DELETE FROM control.resource_events;
DELETE FROM control.resources;
DELETE FROM control.nodes;
DELETE FROM discovery.endpoints;
DELETE FROM discovery.services;
COMMIT;
SQL
  docker ps -aq --filter "label=forge.managed=true" | while read -r cid; do
    [[ -n "${cid}" ]] || continue
    docker rm -f "${cid}" >/dev/null 2>&1 || true
  done
}

create_hierarchy() {
  forge_json "${TMP_DIR}/project.json" project create --name "${PROJECT_NAME}"
  PROJECT_ID="$(read_id "${TMP_DIR}/project.json")"
  # Prefer slug from create response when present; else keep generated slug.
  PROJECT_SLUG="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("slug") or sys.argv[2])' \
    "${TMP_DIR}/project.json" "${PROJECT_SLUG}")"
  forge_json "${TMP_DIR}/environment.json" env create --project "${PROJECT_ID}" --name local
  ENVIRONMENT_ID="$(read_id "${TMP_DIR}/environment.json")"
  forge_json "${TMP_DIR}/application.json" app create --project "${PROJECT_ID}" --name shop
  APPLICATION_ID="$(read_id "${TMP_DIR}/application.json")"

  forge_json "${TMP_DIR}/svc-echo.json" service create --app "${APPLICATION_ID}" --name demo-echo --port 8080
  ECHO_SERVICE_ID="$(read_id "${TMP_DIR}/svc-echo.json")"
  forge_json "${TMP_DIR}/svc-users.json" service create --app "${APPLICATION_ID}" --name users-api --port 8080
  USERS_SERVICE_ID="$(read_id "${TMP_DIR}/svc-users.json")"
  forge_json "${TMP_DIR}/svc-orders.json" service create --app "${APPLICATION_ID}" --name orders-api --port 8080
  ORDERS_SERVICE_ID="$(read_id "${TMP_DIR}/svc-orders.json")"
}

deploy_service() {
  local service_id="$1" replicas="$2" label="$3"
  forge_json "${TMP_DIR}/dep-${label}.json" deployment create \
    --service "${service_id}" \
    --image "${DEMO_IMAGE}" \
    --env "${ENVIRONMENT_ID}" \
    --replicas "${replicas}"
  local dep_id
  dep_id="$(read_id "${TMP_DIR}/dep-${label}.json")"
  track_deployment "${dep_id}"
  echo "${dep_id}"
}

wait_deployment_active() {
  local dep_id="$1" attempts="${2:-120}"
  local status=""
  echo "Waiting for deployment ${dep_id} active ..."
  for _ in $(seq 1 "${attempts}"); do
    status="$(curl --fail --silent --show-error "${CONTROL_URL}/v1/deployments/${dep_id}" |
      python3 -c 'import json,sys; print(json.load(sys.stdin).get("status",""))')" || true
    if [[ "${status}" == "active" || "${status}" == "deployed" ]]; then
      echo "  status=${status}"
      return 0
    fi
    if [[ "${status}" == "failed" || "${status}" == "rolled_back" ]]; then
      fail "deployment ${dep_id} terminal status=${status}"
    fi
    sleep 1
  done
  fail "deployment ${dep_id} status=${status:-unknown}, want active"
}

container_ip() {
  local name="$1"
  docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "${name}" 2>/dev/null |
    awk 'NF{print; exit}' || true
}

wait_container() {
  local name="$1" attempts="${2:-90}"
  local ip=""
  for _ in $(seq 1 "${attempts}"); do
    if docker inspect "${name}" >/dev/null 2>&1; then
      ip="$(container_ip "${name}")"
      if [[ -n "${ip}" ]]; then
        echo "${ip}"
        return 0
      fi
    fi
    sleep 1
  done
  fail "container ${name} not ready with IP"
}

register_endpoint() {
  local service="$1" id="$2" node="$3" ip="$4" lease="${5:-8}"
  curl --fail --silent --show-error \
    -X POST "${DISCOVERY_URL}/v1/projects/${DISC_PROJECT}/environments/${DISC_ENV}/services/${service}/endpoints" \
    -H 'content-type: application/json' \
    -d "{\"id\":\"${id}\",\"node\":\"${node}\",\"address\":{\"ip\":\"${ip}\",\"port\":8080},\"protocol\":\"http\",\"revision\":\"v1\",\"leaseSeconds\":${lease}}" \
    >"${TMP_DIR}/reg-${id}.json" || fail "register ${id} failed"
  curl --fail --silent --show-error \
    -X POST "${DISCOVERY_URL}/v1/projects/${DISC_PROJECT}/environments/${DISC_ENV}/endpoints/${id}/renew" \
    -H 'content-type: application/json' \
    -d "{\"ready\":true,\"leaseSeconds\":${lease}}" \
    >"${TMP_DIR}/renew-${id}.json" || fail "renew ${id} failed"
  python3 -c 'import json,sys; assert json.load(open(sys.argv[1])).get("phase")=="Ready", open(sys.argv[1]).read()' \
    "${TMP_DIR}/renew-${id}.json" || fail "endpoint ${id} not Ready after renew"
}

set_aliases() {
  local service="$1"
  shift
  local aliases_json
  aliases_json="$(python3 -c 'import json,sys; print(json.dumps(sys.argv[1:]))' "$@")"
  "${COMPOSE[@]}" exec -T "${POSTGRES_SERVICE}" psql -U forge -d forge -v ON_ERROR_STOP=1 \
    -c "UPDATE discovery.services SET aliases = '${aliases_json}'::jsonb, updated_at = now() WHERE project='${DISC_PROJECT}' AND environment='${DISC_ENV}' AND name='${service}';" \
    >/dev/null || fail "set aliases for ${service} failed"
}

list_ready() {
  local service="$1" out="$2"
  curl --fail --silent --show-error \
    "${DISCOVERY_URL}/v1/projects/${DISC_PROJECT}/environments/${DISC_ENV}/services/${service}/endpoints" \
    >"${out}" || fail "list Ready ${service} failed"
}

list_all() {
  local service="$1" out="$2"
  curl --fail --silent --show-error \
    "${DISCOVERY_URL}/v1/projects/${DISC_PROJECT}/environments/${DISC_ENV}/services/${service}/endpoints?ready=false" \
    >"${out}" || fail "list all ${service} failed"
}

assert_ready_count() {
  local service="$1" want="$2"
  list_ready "${service}" "${TMP_DIR}/ready-${service}.json"
  python3 - "${TMP_DIR}/ready-${service}.json" "${want}" "${service}" <<'PY' || fail "Ready count mismatch for ${service}"
import json, sys
items, want, svc = json.load(open(sys.argv[1])), int(sys.argv[2]), sys.argv[3]
assert isinstance(items, list), items
assert len(items) == want, f"{svc}: ready={len(items)} want={want} body={items}"
for it in items:
    assert it.get("phase") == "Ready" and it.get("ready") is True, it
print(f"{svc}: {want} Ready endpoint(s)")
PY
}

start_renew_loop() {
  local ids_file="$1"
  (
    while true; do
      while read -r id; do
        [[ -n "${id}" ]] || continue
        curl --silent --show-error \
          -X POST "${DISCOVERY_URL}/v1/projects/${DISC_PROJECT}/environments/${DISC_ENV}/endpoints/${id}/renew" \
          -H 'content-type: application/json' \
          -d '{"ready":true,"leaseSeconds":8}' >/dev/null 2>&1 || true
      done <"${ids_file}"
      sleep 3
    done
  ) &
  RENEW_PID=$!
}

stop_renew_loop() {
  if [[ -n "${RENEW_PID}" ]]; then
    kill "${RENEW_PID}" >/dev/null 2>&1 || true
    wait "${RENEW_PID}" 2>/dev/null || true
    RENEW_PID=""
  fi
}

start_watch() {
  : >"${WATCH_LOG}"
  curl --silent --show-error --no-buffer \
    "${DISCOVERY_URL}/v1/projects/${DISC_PROJECT}/environments/${DISC_ENV}/services/demo-echo/endpoints/watch?since=0" \
    >"${WATCH_LOG}" 2>"${TMP_DIR}/watch.err" &
  WATCH_PID=$!
  sleep 1
  if ! kill -0 "${WATCH_PID}" >/dev/null 2>&1; then
    fail "watch stream exited early: $(cat "${WATCH_LOG}" "${TMP_DIR}/watch.err" 2>/dev/null || true)"
  fi
}

assert_watch_event() {
  local typ="$1" attempts="${2:-40}"
  local found=0
  for _ in $(seq 1 "${attempts}"); do
    # Discovery SSE puts the type in `event:` (payload JSON has no type field).
    if grep -Eq "^event:[[:space:]]*${typ}$" "${WATCH_LOG}" 2>/dev/null; then
      found=1
      break
    fi
    sleep 1
  done
  [[ "${found}" -eq 1 ]] || fail "watch did not emit event=${typ} (see ${WATCH_LOG})"
}

assert_dns_a_count() {
  local name="$1" want="$2" attempts="${3:-30}"
  local count=0
  echo "DNS A ${name} expect ${want} ..."
  for _ in $(seq 1 "${attempts}"); do
    dig @"127.0.0.1" -p 5053 "${name}" A +short >"${TMP_DIR}/dig.txt" 2>/dev/null || true
    count="$(grep -Ec '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$' "${TMP_DIR}/dig.txt" || true)"
    if [[ "${count}" == "${want}" ]]; then
      echo "  ${name} → ${count} A record(s): $(tr '\n' ' ' <"${TMP_DIR}/dig.txt")"
      return 0
    fi
    sleep 1
  done
  fail "DNS ${name} A count=${count}, want ${want}; answers=$(cat "${TMP_DIR}/dig.txt")"
}

refresh_routes() {
  curl --fail --silent --show-error -X POST "${GATEWAY_URL}/admin/routes/refresh" \
    >"${TMP_DIR}/refresh.json" || fail "POST /admin/routes/refresh failed"
}

assert_route_source() {
  local want="$1"
  refresh_routes
  # control mode names itself "control+fallback" when the Runtime interim is wired.
  python3 - "${TMP_DIR}/refresh.json" "${want}" <<'PY' || fail "route source mismatch"
import json, sys
body, want = json.load(open(sys.argv[1])), sys.argv[2]
got = (body.get("source") or "").strip()
ok = got == want or (want == "control" and got in ("control", "control+fallback"))
assert ok, body
print("route source=%s" % got)
PY
}

wait_route_host() {
  local host="$1" attempts="${2:-45}"
  echo "Waiting for gateway route host=${host} ..."
  for _ in $(seq 1 "${attempts}"); do
    refresh_routes
    curl --fail --silent --show-error "${GATEWAY_URL}/admin/routes" >"${TMP_DIR}/routes.json" || true
    if HOST="${host}" python3 -c '
import json, os, sys
host = os.environ["HOST"].lower()
routes = json.load(open(sys.argv[1]))
sys.exit(0 if any((r.get("host") or "").lower() == host for r in routes) else 1)
' "${TMP_DIR}/routes.json"; then
      echo "  route present: ${host}"
      return 0
    fi
    sleep 1
  done
  fail "timed out waiting for route host=${host}"
}

curl_host_ok() {
  local host="$1"
  curl --fail --silent --show-error -H "Host: ${host}" "${GATEWAY_URL}/" \
    >"${TMP_DIR}/curl-${host}.json" || fail "curl Host=${host} failed"
  python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert d.get("ok") is True, d' \
    "${TMP_DIR}/curl-${host}.json" || fail "Host ${host} unexpected body"
  echo "  Host ${host} → ok"
}

flip_gateway_source() {
  local source="$1"
  echo "Flipping Gateway FORGE_ROUTE_SOURCE=${source} ..."
  FORGE_ROUTE_SOURCE="${source}" "${COMPOSE[@]}" up -d --force-recreate --no-deps "${GATEWAY_SERVICE}" \
    || fail "gateway recreate with source=${source} failed"
  wait_http "${GATEWAY_URL}/health/ready" "Gateway (${source})" 60
  assert_route_source "${source}"
}

echo "== Demo 21: Forge Discovery service discovery =="
echo "Building forge CLI..."
make -C "${CLI_DIR}" build || fail "CLI build failed"
[[ -x "${FORGE_BIN}" ]] || fail "forge binary missing at ${FORGE_BIN}"

render_fixture() {
  PROJECT_SLUG="${DISC_PROJECT}" envsubst '${PROJECT_SLUG}' \
    <"${DEMO_DIR}/fixtures/services.yaml" >"${TMP_DIR}/services.yaml"
}
render_fixture

echo "Starting PostgreSQL + registry (reuse if already up)..."
"${COMPOSE[@]}" up -d "${POSTGRES_SERVICE}" "${REGISTRY_SERVICE}"
for _ in $(seq 1 60); do
  if "${COMPOSE[@]}" exec -T "${POSTGRES_SERVICE}" pg_isready -U forge >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
"${COMPOSE[@]}" exec -T "${POSTGRES_SERVICE}" pg_isready -U forge >/dev/null 2>&1 ||
  fail "Postgres not ready"

echo "Starting Control → Discovery → Runtime A/B → Gateway (sequential)..."
compose_up_one "${CONTROL_SERVICE}"
wait_http "${CONTROL_URL}/health/ready" "Control"

"${COMPOSE[@]}" stop "${RUNTIME_A_SERVICE}" "${RUNTIME_B_SERVICE}" "${GATEWAY_SERVICE}" >/dev/null 2>&1 || true
purge_stale_state

compose_up_one "${DISCOVERY_SERVICE}"
wait_http "${DISCOVERY_URL}/health/ready" "Discovery"
# Ensure 21.05 service list route is present (rebuild if stale image).
curl --fail --silent --show-error "${DISCOVERY_URL}/v1/services" >"${TMP_DIR}/services-list.json" ||
  fail "GET /v1/services failed (rebuild forge-discovery?)"

compose_up_one "${RUNTIME_A_SERVICE}"
wait_http "${RUNTIME_A_URL}/health/ready" "Runtime node-a"
compose_up_one "${RUNTIME_B_SERVICE}"
wait_http "${RUNTIME_B_URL}/health/ready" "Runtime node-b"

compose_up_one "${GATEWAY_SERVICE}"
wait_http "${GATEWAY_URL}/health/ready" "Gateway"
assert_route_source "discovery"

ensure_demo_image

echo "Configuring CLI profile ${FORGE_PROFILE} -> ${FORGE_ENDPOINT}..."
forge config set endpoint "${FORGE_ENDPOINT}" --profile "${FORGE_PROFILE}"
forge config use "${FORGE_PROFILE}"

echo "Creating project hierarchy + three services..."
create_hierarchy

echo "Deploying demo-echo (2 replicas), users-api, orders-api..."
ECHO_DEP="$(deploy_service "${ECHO_SERVICE_ID}" 2 echo)"
USERS_DEP="$(deploy_service "${USERS_SERVICE_ID}" 1 users)"
ORDERS_DEP="$(deploy_service "${ORDERS_SERVICE_ID}" 1 orders)"
wait_deployment_active "${ECHO_DEP}" 150
wait_deployment_active "${USERS_DEP}" 120
wait_deployment_active "${ORDERS_DEP}" 120

# Resolve managed container names (Control → Runtime naming: forge-<slug>-<short>-<idx>).
ECHO_SHORT="$(python3 -c 'import sys; print(sys.argv[1].replace("-","")[:8])' "${ECHO_DEP}")"
USERS_SHORT="$(python3 -c 'import sys; print(sys.argv[1].replace("-","")[:8])' "${USERS_DEP}")"
ORDERS_SHORT="$(python3 -c 'import sys; print(sys.argv[1].replace("-","")[:8])' "${ORDERS_DEP}")"
ECHO_C0="forge-demo-echo-${ECHO_SHORT}-0"
ECHO_C1="forge-demo-echo-${ECHO_SHORT}-1"
USERS_C0="forge-users-api-${USERS_SHORT}-0"
ORDERS_C0="forge-orders-api-${ORDERS_SHORT}-0"

ECHO_IP0="$(wait_container "${ECHO_C0}")"
ECHO_IP1="$(wait_container "${ECHO_C1}")"
USERS_IP="$(wait_container "${USERS_C0}")"
ORDERS_IP="$(wait_container "${ORDERS_C0}")"
echo "  demo-echo-0 ${ECHO_IP0}"
echo "  demo-echo-1 ${ECHO_IP1}"
echo "  users-api   ${USERS_IP}"
echo "  orders-api  ${ORDERS_IP}"

# Prove Runtime also registered leases (service name falls back to replica deployment id).
echo "Checking Runtime→Discovery auto-registration..."
for _ in $(seq 1 60); do
  curl --fail --silent --show-error "${DISCOVERY_URL}/v1/services" >"${TMP_DIR}/runtime-svcs.json" || true
  if python3 - "${TMP_DIR}/runtime-svcs.json" <<'PY'
import json, sys
items = json.load(open(sys.argv[1]))
assert isinstance(items, list) and len(items) >= 1, items
sys.exit(0)
PY
  then
    break
  fi
  sleep 1
done
python3 - "${TMP_DIR}/runtime-svcs.json" <<'PY' || fail "Runtime did not register any Discovery services"
import json, sys
items = json.load(open(sys.argv[1]))
assert isinstance(items, list) and len(items) >= 1, items
print("Runtime-registered services: %d" % len(items))
PY

echo "Registering canonical Discovery endpoints from fixtures (demo/local)..."
register_endpoint "demo-echo" "demo-echo-${SUFFIX}-0" "node-a" "${ECHO_IP0}"
register_endpoint "demo-echo" "demo-echo-${SUFFIX}-1" "node-b" "${ECHO_IP1}"
register_endpoint "users-api" "users-api-${SUFFIX}-0" "node-a" "${USERS_IP}"
register_endpoint "orders-api" "orders-api-${SUFFIX}-0" "node-a" "${ORDERS_IP}"
set_aliases "demo-echo" "echo"

printf '%s\n' \
  "demo-echo-${SUFFIX}-0" \
  "demo-echo-${SUFFIX}-1" \
  "users-api-${SUFFIX}-0" \
  "orders-api-${SUFFIX}-0" >"${TMP_DIR}/renew-ids.txt"
start_renew_loop "${TMP_DIR}/renew-ids.txt"

assert_ready_count "demo-echo" 2
assert_ready_count "users-api" 1
assert_ready_count "orders-api" 1

curl --fail --silent --show-error "${DISCOVERY_URL}/v1/services" >"${TMP_DIR}/all-services.json"
python3 - "${TMP_DIR}/all-services.json" <<'PY' || fail "service registry missing fixture services"
import json, sys
items = json.load(open(sys.argv[1]))
names = {(i.get("project"), i.get("environment"), i.get("name")) for i in items}
for want in [("demo", "local", "demo-echo"), ("demo", "local", "users-api"), ("demo", "local", "orders-api")]:
    assert want in names, (want, names)
echo = next(i for i in items if i.get("name") == "demo-echo" and i.get("project") == "demo")
assert "echo" in (echo.get("aliases") or []), echo
print("service registry ok (%d services)" % len(items))
PY

echo "Starting demo-echo endpoint watch..."
start_watch

assert_dns_a_count "demo-echo.local.demo.svc.forge" 2
assert_dns_a_count "echo.local.demo.svc.forge" 2
assert_dns_a_count "users-api.local.demo.svc.forge" 1
assert_dns_a_count "orders-api.local.demo.svc.forge" 1

wait_route_host "demo-echo.demo.localhost"
wait_route_host "echo.demo.localhost"
wait_route_host "users-api.demo.localhost"
wait_route_host "orders-api.demo.localhost"
curl_host_ok "demo-echo.demo.localhost"
curl_host_ok "echo.demo.localhost"
curl_host_ok "users-api.demo.localhost"
curl_host_ok "orders-api.demo.localhost"

echo "Failure path: stop renewing node-b endpoint + stop Runtime B (lease/node loss)..."
# Drop node-b endpoint from renew loop.
printf '%s\n' \
  "demo-echo-${SUFFIX}-0" \
  "users-api-${SUFFIX}-0" \
  "orders-api-${SUFFIX}-0" >"${TMP_DIR}/renew-ids.txt"
# Stop Runtime B so its heartbeat expires (node offline) and leases stop renewing.
"${COMPOSE[@]}" stop "${RUNTIME_B_SERVICE}" >/dev/null 2>&1 ||
  docker stop forge-runtime-b >/dev/null 2>&1 ||
  fail "could not stop Runtime B"

echo "Waiting for demo-echo Ready count → 1 (expired/unready excluded)..."
for _ in $(seq 1 60); do
  list_ready "demo-echo" "${TMP_DIR}/ready-echo.json"
  count="$(python3 -c 'import json,sys; print(len(json.load(open(sys.argv[1]))))' "${TMP_DIR}/ready-echo.json")"
  if [[ "${count}" == "1" ]]; then
    echo "  Ready count=1"
    break
  fi
  sleep 1
done
[[ "${count:-}" == "1" ]] || fail "demo-echo Ready count=${count:-unknown}, want 1 after lease/node loss"

list_all "demo-echo" "${TMP_DIR}/all-echo.json"
python3 - "${TMP_DIR}/all-echo.json" "demo-echo-${SUFFIX}-1" <<'PY' || fail "expired endpoint not Unready"
import json, sys
items, victim = json.load(open(sys.argv[1])), sys.argv[2]
by_id = {i["id"]: i for i in items}
assert victim in by_id, items
row = by_id[victim]
assert row.get("phase") == "Unready" or row.get("ready") is False, row
print("unready ok: id=%s phase=%s reason=%s" % (victim, row.get("phase"), row.get("unreadyReason")))
PY
assert_watch_event "updated" 30
assert_dns_a_count "demo-echo.local.demo.svc.forge" 1 45

# Gateway must stop routing to the dead upstream (still serve the Ready replica).
refresh_routes
curl_host_ok "demo-echo.demo.localhost"

echo "Replacement: register new Ready endpoint on node-a..."
"${COMPOSE[@]}" start "${RUNTIME_B_SERVICE}" >/dev/null 2>&1 ||
  "${COMPOSE[@]}" up -d "${RUNTIME_B_SERVICE}" ||
  fail "could not restart Runtime B"
wait_http "${RUNTIME_B_URL}/health/ready" "Runtime node-b (restart)" 60

# Replacement uses a distinct Ready address (orders-api container; same demo image contract).
register_endpoint "demo-echo" "demo-echo-${SUFFIX}-2" "node-a" "${ORDERS_IP}"
printf '%s\n' \
  "demo-echo-${SUFFIX}-0" \
  "demo-echo-${SUFFIX}-2" \
  "users-api-${SUFFIX}-0" \
  "orders-api-${SUFFIX}-0" >"${TMP_DIR}/renew-ids.txt"

assert_ready_count "demo-echo" 2
assert_dns_a_count "demo-echo.local.demo.svc.forge" 2 45
wait_route_host "demo-echo.demo.localhost" 30
curl_host_ok "demo-echo.demo.localhost"

echo "Gateway route-source flip: discovery → control → discovery (no data loss)..."
# Capture Discovery-sourced routes before flip.
curl --fail --silent --show-error "${GATEWAY_URL}/admin/routes" >"${TMP_DIR}/routes-before.json"
before_count="$(python3 -c 'import json,sys; print(len(json.load(open(sys.argv[1]))))' "${TMP_DIR}/routes-before.json")"
[[ "${before_count}" -ge 3 ]] || fail "expected >=3 discovery routes before flip, got ${before_count}"

flip_gateway_source "control"
# Control path may or may not have /v1/endpoints; require source flip + health, keep Discovery data intact.
curl --fail --silent --show-error "${DISCOVERY_URL}/v1/projects/${DISC_PROJECT}/environments/${DISC_ENV}/services/demo-echo/endpoints" \
  >"${TMP_DIR}/ready-after-control.json" || fail "Discovery list failed during control flip"
python3 - "${TMP_DIR}/ready-after-control.json" <<'PY' || fail "Discovery data lost during control flip"
import json, sys
items = json.load(open(sys.argv[1]))
assert len(items) >= 2, items
print("Discovery Ready endpoints retained: %d" % len(items))
PY

flip_gateway_source "discovery"
wait_route_host "demo-echo.demo.localhost" 45
wait_route_host "users-api.demo.localhost" 30
curl_host_ok "demo-echo.demo.localhost"
curl_host_ok "users-api.demo.localhost"
assert_ready_count "demo-echo" 2

stop_renew_loop
if [[ -n "${WATCH_PID}" ]]; then
  kill "${WATCH_PID}" >/dev/null 2>&1 || true
  wait "${WATCH_PID}" 2>/dev/null || true
  WATCH_PID=""
fi

echo
echo "demo 21 PASSED"
echo "  Discovery project/env: ${DISC_PROJECT}/${DISC_ENV}"
echo "  Control project:       ${PROJECT_SLUG} (${PROJECT_ID})"
echo "  Services:              demo-echo (2+replacement), users-api, orders-api"
echo "  DNS:                   *.local.demo.svc.forge"
echo "  Gateway:               FORGE_ROUTE_SOURCE flip discovery↔control ok"
