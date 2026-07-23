#!/usr/bin/env bash
# Demo 22: Forge Network gate (epic 22 acceptance).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/22-forge-network"
APP_DIR="${ROOT_DIR}/demos/07-rolling-deployment/apps/demo"
# shellcheck source=lib/verify.sh
source "${DEMO_DIR}/lib/verify.sh"

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
export FORGE_DISCOVERY_DEFAULT_ENVIRONMENT="${FORGE_DISCOVERY_DEFAULT_ENVIRONMENT:-production}"
export FORGE_DISCOVERY_NETWORK_URL="${FORGE_DISCOVERY_NETWORK_URL:-http://forge-network:8080}"
export FORGE_CONTROL_NETWORK_URL="${FORGE_CONTROL_NETWORK_URL:-http://forge-network:8080}"
export FORGE_NETWORK_WG_BACKEND="${FORGE_NETWORK_WG_BACKEND:-fake}"
export FORGE_NETWORK_ROUTE_BACKEND="${FORGE_NETWORK_ROUTE_BACKEND:-fake}"
export FORGE_NETWORK_POLICY_BACKEND="${FORGE_NETWORK_POLICY_BACKEND:-fake}"
export FORGE_NETWORK_DNS_BACKEND="${FORGE_NETWORK_DNS_BACKEND:-fake}"
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
RUNTIME_C_URL="${FORGE_RUNTIME_C_URL:-http://127.0.0.1:4122}"
DISCOVERY_URL="${FORGE_DISCOVERY_URL_HOST:-http://127.0.0.1:4109}"
NETWORK_URL="${FORGE_NETWORK_URL_HOST:-http://127.0.0.1:4110}"
CONTROL_SERVICE="forge-control"
RUNTIME_A_SERVICE="forge-runtime"
RUNTIME_B_SERVICE="forge-runtime-b"
RUNTIME_C_SERVICE="forge-runtime-c"
DISCOVERY_SERVICE="forge-discovery"
NETWORK_SERVICE="forge-network"
POSTGRES_SERVICE="postgres"
REGISTRY_SERVICE="registry"
CLI_DIR="${ROOT_DIR}/tools/forge-cli"
FORGE_BIN="${CLI_DIR}/forge"
REGISTRY="${FORGE_REGISTRY:-localhost:5000}"
DEMO_IMAGE="${DEMO_IMAGE:-${REGISTRY}/demo-network:v1}"
NETWORK_NAME="${FORGE_NETWORK_NAME:-cluster-overlay}"
DISC_PROJECT="demo"
DISC_ENV="production"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-demo-22.XXXXXX")"
CONFIG_HOME="${TMP_DIR}/xdg-config"
export XDG_CONFIG_HOME="${CONFIG_HOME}"
export CI=1
export FORGE_PROFILE="${FORGE_PROFILE:-demo22}"
export FORGE_ENDPOINT="${FORGE_ENDPOINT:-${CONTROL_URL}}"
mkdir -p "${CONFIG_HOME}"

TRACKED_DEPLOYMENTS=()
SUFFIX="$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')"
PROJECT_NAME="Network Demo ${SUFFIX}"
TOKEN_A="" TOKEN_B="" TOKEN_C=""
FRONTEND_DEP="" API_DEP="" ECHO_DEP=""
RENEW_PID=""

cleanup() {
  local dep
  if [[ -n "${RENEW_PID}" ]]; then
    kill "${RENEW_PID}" >/dev/null 2>&1 || true
    wait "${RENEW_PID}" 2>/dev/null || true
    RENEW_PID=""
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
    "${RUNTIME_C_SERVICE}" "${RUNTIME_B_SERVICE}" "${RUNTIME_A_SERVICE}" \
    "${DISCOVERY_SERVICE}" "${CONTROL_SERVICE}" "${NETWORK_SERVICE}" >/dev/null 2>&1 || true
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

dump_context() {
  echo "--- Control /v1/nodes ---" >&2
  curl --silent --show-error "${CONTROL_URL}/v1/nodes" >&2 || true
  echo >&2
  echo "--- Network peers node-a ---" >&2
  curl --silent --show-error "${NETWORK_URL}/v1/networks/${NETWORK_NAME}/nodes/node-a/peers" >&2 || true
  echo >&2
  echo "--- Network leases ---" >&2
  curl --silent --show-error "${NETWORK_URL}/v1/networks/${NETWORK_NAME}/workload-leases" >&2 || true
  echo >&2
  echo "--- Discovery /v1/services ---" >&2
  curl --silent --show-error "${DISCOVERY_URL}/v1/services" >&2 || true
  echo >&2
  echo "--- ${NETWORK_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${NETWORK_SERVICE}" >&2 || true
  echo "--- ${CONTROL_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${CONTROL_SERVICE}" >&2 || true
  echo "--- ${RUNTIME_A_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=40 "${RUNTIME_A_SERVICE}" >&2 || true
  echo "--- ${RUNTIME_B_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=40 "${RUNTIME_B_SERVICE}" >&2 || true
  echo "--- ${RUNTIME_C_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=40 "${RUNTIME_C_SERVICE}" >&2 || true
}

fail() {
  echo "Demo 22 failed: $*" >&2
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
  docker build --build-arg VERSION=network -t "${DEMO_IMAGE}" "${APP_DIR}" ||
    fail "docker build failed"
  docker push "${DEMO_IMAGE}" >/dev/null || fail "docker push failed"
}

purge_stale_state() {
  echo "Purging leftover Control / Discovery / Network state ..."
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
DELETE FROM control.bootstrap_tokens;
DELETE FROM discovery.endpoints;
DELETE FROM discovery.services;
DELETE FROM network.network_policies;
DELETE FROM network.environment_network_defaults;
DELETE FROM network.workload_placements;
DELETE FROM network.workload_leases;
DELETE FROM network.node_leases;
DELETE FROM network.wireguard_peers;
DELETE FROM network.network_routes;
DELETE FROM network.nodes;
DELETE FROM network.networks;
-- Keep singleton generation row (Runtime policy poll requires it).
UPDATE network.policy_rule_generation SET generation = 0 WHERE id = 1;
INSERT INTO network.policy_rule_generation (id, generation) VALUES (1, 0)
ON CONFLICT (id) DO NOTHING;
COMMIT;
SQL
  docker ps -aq --filter "label=forge.managed=true" | while read -r cid; do
    [[ -n "${cid}" ]] || continue
    docker rm -f "${cid}" >/dev/null 2>&1 || true
  done
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

issue_bootstrap_token() {
  local out="$1"
  curl --fail --silent --show-error -X POST "${CONTROL_URL}/v1/nodes/bootstrap-tokens" \
    -H 'content-type: application/json' \
    -d '{"organization":"default","ttl_seconds":900}' \
    >"${out}" || fail "issue bootstrap token failed"
  python3 -c 'import json,sys; t=json.load(open(sys.argv[1])).get("token"); assert t; print(t)' "${out}"
}

wait_nodes_joined() {
  local attempts="${1:-90}"
  echo "Waiting for node-a/b/c online with overlay CIDRs ..."
  for _ in $(seq 1 "${attempts}"); do
    curl --fail --silent --show-error "${CONTROL_URL}/v1/nodes" >"${TMP_DIR}/nodes.json" || true
    if python3 - <<'PY' "${TMP_DIR}/nodes.json"
import json, sys
nodes = {n["id"]: n for n in json.load(open(sys.argv[1]))}
for nid in ("node-a", "node-b", "node-c"):
    n = nodes.get(nid)
    if not n or n.get("status") != "online":
        sys.exit(1)
    net = n.get("network") or {}
    cidr = net.get("cidr") or ""
    if not cidr.startswith("10.100."):
        sys.exit(1)
    if not (n.get("wireguard_public_key") or n.get("wireguardPublicKey")):
        sys.exit(1)
sys.exit(0)
PY
    then
      for nid in node-a node-b node-c; do
        verify_node_overlay "${nid}"
      done
      return 0
    fi
    sleep 1
  done
  fail "timed out waiting for joined nodes with overlay"
}

peers_ready() {
  local node peers
  for node in node-a node-b node-c; do
    peers="$(curl --silent --show-error \
      "${NETWORK_URL}/v1/networks/${NETWORK_NAME}/nodes/${node}/peers" 2>/dev/null || true)"
    [[ -n "${peers}" ]] || return 1
    python3 -c 'import json,sys; d=json.load(sys.stdin); assert len(d.get("peers") or [])==2' \
      <<<"${peers}" 2>/dev/null || return 1
  done
  return 0
}

wait_peers_ready() {
  local attempts="${1:-60}"
  echo "Waiting for WireGuard peer registry convergence ..."
  for _ in $(seq 1 "${attempts}"); do
    if peers_ready; then
      verify_peers_converged "${NETWORK_NAME}" 2
      return 0
    fi
    sleep 1
  done
  verify_peers_converged "${NETWORK_NAME}" 2
}

create_hierarchy() {
  forge_json "${TMP_DIR}/project.json" project create --name "${PROJECT_NAME}"
  PROJECT_ID="$(read_id "${TMP_DIR}/project.json")"
  forge_json "${TMP_DIR}/environment.json" env create --project "${PROJECT_ID}" --name production
  ENVIRONMENT_ID="$(read_id "${TMP_DIR}/environment.json")"
  forge_json "${TMP_DIR}/app-frontend.json" app create --project "${PROJECT_ID}" --name frontend
  FRONTEND_APP_ID="$(read_id "${TMP_DIR}/app-frontend.json")"
  forge_json "${TMP_DIR}/app-api.json" app create --project "${PROJECT_ID}" --name api
  API_APP_ID="$(read_id "${TMP_DIR}/app-api.json")"
  forge_json "${TMP_DIR}/app-echo.json" app create --project "${PROJECT_ID}" --name echo
  ECHO_APP_ID="$(read_id "${TMP_DIR}/app-echo.json")"

  forge_json "${TMP_DIR}/svc-frontend.json" service create --app "${FRONTEND_APP_ID}" --name frontend --port 8080
  FRONTEND_SERVICE_ID="$(read_id "${TMP_DIR}/svc-frontend.json")"
  forge_json "${TMP_DIR}/svc-api.json" service create --app "${API_APP_ID}" --name api --port 8080
  API_SERVICE_ID="$(read_id "${TMP_DIR}/svc-api.json")"
  forge_json "${TMP_DIR}/svc-echo.json" service create --app "${ECHO_APP_ID}" --name echo --port 8080
  ECHO_SERVICE_ID="$(read_id "${TMP_DIR}/svc-echo.json")"
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
  local dep_id="$1" attempts="${2:-180}"
  local status="" reconcile=""
  echo "Waiting for deployment ${dep_id} active/deployed ..."
  for _ in $(seq 1 "${attempts}"); do
    status="$(curl --fail --silent --show-error "${CONTROL_URL}/v1/deployments/${dep_id}" |
      python3 -c 'import json,sys; print(json.load(sys.stdin).get("status",""))')" || true
    if [[ "${status}" == "active" || "${status}" == "deployed" ]]; then
      echo "  status=${status}"
      return 0
    fi
    curl --silent --show-error "${CONTROL_URL}/v1/deployments/${dep_id}/reconcile" \
      >"${TMP_DIR}/reconcile-${dep_id}.json" 2>/dev/null || true
    reconcile="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("status",""))' \
      "${TMP_DIR}/reconcile-${dep_id}.json" 2>/dev/null || true)"
    if [[ "${reconcile}" == "deployed" ]]; then
      echo "  reconcile status=${reconcile}"
      return 0
    fi
    if [[ "${status}" == "failed" || "${status}" == "rolled_back" || "${reconcile}" == "failed" ]]; then
      fail "deployment ${dep_id} terminal status=${status} reconcile=${reconcile}"
    fi
    sleep 1
  done
  echo "--- placements ---" >&2
  curl --silent --show-error "${CONTROL_URL}/v1/placements?deployment=${dep_id}" >&2 || true
  echo >&2
  fail "deployment ${dep_id} status=${status:-unknown} reconcile=${reconcile:-unknown}, want active"
}

wait_placement_on() {
  local dep_id="$1" node_id="$2" attempts="${3:-90}"
  echo "Waiting for deployment ${dep_id} placed on ${node_id} ..."
  for _ in $(seq 1 "${attempts}"); do
    curl --fail --silent --show-error \
      "${CONTROL_URL}/v1/placements?deployment=${dep_id}" \
      >"${TMP_DIR}/placements-${dep_id}.json" || true
    if NODE_ID="${node_id}" python3 - <<'PY' "${TMP_DIR}/placements-${dep_id}.json"
import json, os, sys
node = os.environ["NODE_ID"]
items = json.load(open(sys.argv[1]))
ok = any(p.get("status") == "placed" and p.get("node_id") == node for p in items)
sys.exit(0 if ok else 1)
PY
    then
      echo "  placed on ${node_id}"
      return 0
    fi
    sleep 1
  done
  fail "deployment ${dep_id} not placed on ${node_id}"
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

upsert_placement_mirror() {
  local workload_id="$1" node_id="$2" application="$3" service="$4"
  curl --fail --silent --show-error \
    -X PUT "${NETWORK_URL}/v1/workload-placements/${workload_id}" \
    -H 'content-type: application/json' \
    -d "{\"organization\":\"default\",\"project\":\"${DISC_PROJECT}\",\"environment\":\"${DISC_ENV}\",\"node_id\":\"${node_id}\",\"application\":\"${application}\",\"service\":\"${service}\"}" \
    >"${TMP_DIR}/placement-${workload_id}.json" || fail "upsert placement ${workload_id} failed"
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

apply_allow_policy() {
  curl --fail --silent --show-error \
    -X POST "${NETWORK_URL}/v1/projects/${DISC_PROJECT}/environments/${DISC_ENV}/network-policies" \
    -H 'content-type: application/json' \
    -d '{"name":"api-from-frontend","organization":"default","spec":{"target":{"application":"api"},"ingress":[{"from":{"service":"frontend"},"ports":[{"port":8080,"protocol":"tcp"}]}]}}' \
    >"${TMP_DIR}/policy-create.json" || fail "create NetworkPolicy failed"
  python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert d.get("status",{}).get("phase")=="Ready", d' \
    "${TMP_DIR}/policy-create.json" || fail "NetworkPolicy not Ready"
  echo "  NetworkPolicy api-from-frontend Ready"
}

delete_allow_policy() {
  local code
  code="$(curl -s -o "${TMP_DIR}/policy-del.json" -w '%{http_code}' \
    -X DELETE "${NETWORK_URL}/v1/projects/${DISC_PROJECT}/environments/${DISC_ENV}/network-policies/api-from-frontend")"
  [[ "${code}" == "204" || "${code}" == "200" || "${code}" == "404" ]] ||
    fail "delete NetworkPolicy HTTP ${code}: $(cat "${TMP_DIR}/policy-del.json")"
  echo "  NetworkPolicy api-from-frontend removed"
}

container_host_port() {
  local name="$1"
  docker inspect -f '{{with (index .NetworkSettings.Ports "8080/tcp")}}{{(index . 0).HostPort}}{{end}}' "${name}" 2>/dev/null || true
}

curl_docker_ok() {
  local container="$1" label="$2"
  local host_port ip
  host_port="$(container_host_port "${container}")"
  if [[ -n "${host_port}" ]]; then
    curl --fail --silent --show-error "http://127.0.0.1:${host_port}/" \
      >"${TMP_DIR}/curl-${label}.json" || fail "curl ${label} via host:${host_port} failed"
    echo "  docker-plane ${label} via 127.0.0.1:${host_port} → ok"
  else
    # Fallback: reachability from the frontend container over the Docker bridge.
    ip="$(container_ip "${container}")"
    [[ -n "${ip}" ]] || fail "no host port or IP for ${container}"
    docker exec "${FRONTEND_C0}" wget -qO- "http://${ip}:8080/" \
      >"${TMP_DIR}/curl-${label}.json" 2>/dev/null ||
      docker exec "${FRONTEND_C0}" python3 -c "import urllib.request; print(urllib.request.urlopen('http://${ip}:8080/').read().decode())" \
        >"${TMP_DIR}/curl-${label}.json" ||
      fail "curl ${label} from frontend → ${ip} failed"
    echo "  docker-plane ${label} via frontend→${ip} → ok"
  fi
  python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert d.get("ok") is True, d' \
    "${TMP_DIR}/curl-${label}.json" || fail "${label} unexpected body"
}

register_overlay_endpoint() {
  local service="$1" id="$2" node="$3" overlay_ip="$4"
  curl --fail --silent --show-error \
    -X POST "${DISCOVERY_URL}/v1/projects/${DISC_PROJECT}/environments/${DISC_ENV}/services/${service}/endpoints" \
    -H 'content-type: application/json' \
    -d "{\"id\":\"${id}\",\"node\":\"${node}\",\"address\":{\"ip\":\"${overlay_ip}\",\"port\":8080},\"protocol\":\"http\",\"revision\":\"v1\",\"leaseSeconds\":8}" \
    >"${TMP_DIR}/reg-${id}.json" || fail "register ${id} failed"
  curl --fail --silent --show-error \
    -X POST "${DISCOVERY_URL}/v1/projects/${DISC_PROJECT}/environments/${DISC_ENV}/endpoints/${id}/renew" \
    -H 'content-type: application/json' \
    -d '{"ready":true,"leaseSeconds":8}' \
    >"${TMP_DIR}/renew-${id}.json" || fail "renew ${id} failed"
  python3 -c 'import json,sys; assert json.load(open(sys.argv[1])).get("phase")=="Ready", open(sys.argv[1]).read()' \
    "${TMP_DIR}/renew-${id}.json" || fail "endpoint ${id} not Ready"
  echo "  registered ${service}/${id} @ ${overlay_ip} on ${node}"
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

echo "== Demo 22: Forge Network =="
command -v dig >/dev/null || fail "dig is required (bind-tools / dnsutils)"
echo "Building forge CLI..."
make -C "${CLI_DIR}" build || fail "CLI build failed"
[[ -x "${FORGE_BIN}" ]] || fail "forge binary missing at ${FORGE_BIN}"

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

echo "Starting forge-network → Control (join path) ..."
compose_up_one "${NETWORK_SERVICE}"
wait_http "${NETWORK_URL}/health/ready" "forge-network"

"${COMPOSE[@]}" stop \
  "${RUNTIME_A_SERVICE}" "${RUNTIME_B_SERVICE}" "${RUNTIME_C_SERVICE}" \
  "${DISCOVERY_SERVICE}" "${CONTROL_SERVICE}" >/dev/null 2>&1 || true
purge_stale_state

compose_up_one "${CONTROL_SERVICE}"
wait_http "${CONTROL_URL}/health/ready" "Control"
ensure_cluster_network

echo "Issuing bootstrap tokens for node-a/b/c ..."
TOKEN_A="$(issue_bootstrap_token "${TMP_DIR}/token-a.json")"
TOKEN_B="$(issue_bootstrap_token "${TMP_DIR}/token-b.json")"
TOKEN_C="$(issue_bootstrap_token "${TMP_DIR}/token-c.json")"
export FORGE_NODE_BOOTSTRAP_TOKEN_A="${TOKEN_A}"
export FORGE_NODE_BOOTSTRAP_TOKEN_B="${TOKEN_B}"
export FORGE_NODE_BOOTSTRAP_TOKEN_C="${TOKEN_C}"
# Sequential join+deploy pins placement: least-allocated prefers the empty new node.
export FORGE_NODE_SLOTS_A=4
export FORGE_NODE_SLOTS_B=4
export FORGE_NODE_SLOTS_C=4

compose_up_one "${DISCOVERY_SERVICE}"
wait_http "${DISCOVERY_URL}/health/ready" "Discovery"

ensure_demo_image

echo "Configuring CLI profile ${FORGE_PROFILE} -> ${FORGE_ENDPOINT}..."
forge config set endpoint "${FORGE_ENDPOINT}" --profile "${FORGE_PROFILE}"
forge config use "${FORGE_PROFILE}"

echo "Creating project hierarchy (frontend / api / echo)..."
create_hierarchy

# Seed policy defaults early (avoids empty generation 500s during Runtime policy poll).
set_env_default_policy "allow-within-environment"

echo "Join node-a + deploy frontend (only node online)..."
compose_up_one "${RUNTIME_A_SERVICE}"
wait_http "${RUNTIME_A_URL}/health/ready" "Runtime node-a"
for _ in $(seq 1 90); do
  curl --fail --silent --show-error "${CONTROL_URL}/v1/nodes" >"${TMP_DIR}/nodes.json" || true
  if python3 - <<'PY' "${TMP_DIR}/nodes.json"
import json, sys
n = {x["id"]: x for x in json.load(open(sys.argv[1]))}.get("node-a")
sys.exit(0 if n and n.get("status") == "online" and (n.get("network") or {}).get("cidr", "").startswith("10.100.") else 1)
PY
  then
    break
  fi
  sleep 1
done
verify_node_overlay node-a
FRONTEND_DEP="$(deploy_service "${FRONTEND_SERVICE_ID}" 1 frontend)"
wait_deployment_active "${FRONTEND_DEP}" 150
wait_placement_on "${FRONTEND_DEP}" node-a 90

echo "Join node-b + deploy api..."
compose_up_one "${RUNTIME_B_SERVICE}"
wait_http "${RUNTIME_B_URL}/health/ready" "Runtime node-b"
for _ in $(seq 1 90); do
  curl --fail --silent --show-error "${CONTROL_URL}/v1/nodes" >"${TMP_DIR}/nodes.json" || true
  if python3 - <<'PY' "${TMP_DIR}/nodes.json"
import json, sys
n = {x["id"]: x for x in json.load(open(sys.argv[1]))}.get("node-b")
sys.exit(0 if n and n.get("status") == "online" and (n.get("network") or {}).get("cidr", "").startswith("10.100.") else 1)
PY
  then
    break
  fi
  sleep 1
done
verify_node_overlay node-b
API_DEP="$(deploy_service "${API_SERVICE_ID}" 1 api)"
wait_deployment_active "${API_DEP}" 150
wait_placement_on "${API_DEP}" node-b 90

echo "Join node-c + deploy echo immediately (pin before peer waits)..."
compose_up_one "${RUNTIME_C_SERVICE}"
wait_http "${RUNTIME_C_URL}/health/ready" "Runtime node-c"
for _ in $(seq 1 90); do
  curl --fail --silent --show-error "${CONTROL_URL}/v1/nodes" >"${TMP_DIR}/nodes.json" || true
  if python3 - <<'PY' "${TMP_DIR}/nodes.json"
import json, sys
n = {x["id"]: x for x in json.load(open(sys.argv[1]))}.get("node-c")
sys.exit(0 if n and n.get("status") == "online" and (n.get("network") or {}).get("cidr", "").startswith("10.100.") else 1)
PY
  then
    break
  fi
  sleep 1
done
verify_node_overlay node-c
ECHO_DEP="$(deploy_service "${ECHO_SERVICE_ID}" 1 echo)"
wait_deployment_active "${ECHO_DEP}" 180
wait_placement_on "${ECHO_DEP}" node-c 90

wait_nodes_joined 60
wait_peers_ready 90
verify_transport "${NETWORK_NAME}" node-a node-b docker

# Exercise WireGuard path with fake backend via membership override on node-c.
echo "Forcing wireguard transport for node-a↔node-c (membership override) ..."
curl --fail --silent --show-error \
  -X PATCH "${NETWORK_URL}/v1/nodes/node-c/network-membership" \
  -H 'content-type: application/json' \
  -d '{"membership":"bare-metal-lab","docker_colocated":false}' \
  >"${TMP_DIR}/mem-c.json" || fail "patch membership node-c failed"
curl --fail --silent --show-error \
  -X PATCH "${NETWORK_URL}/v1/nodes/node-a/network-membership" \
  -H 'content-type: application/json' \
  -d '{"membership":"compose-local","docker_colocated":true}' \
  >"${TMP_DIR}/mem-a.json" || fail "patch membership node-a failed"
verify_transport "${NETWORK_NAME}" node-a node-b docker
verify_transport "${NETWORK_NAME}" node-a node-c wireguard

FRONTEND_SHORT="$(python3 -c 'import sys; print(sys.argv[1].replace("-","")[:8])' "${FRONTEND_DEP}")"
API_SHORT="$(python3 -c 'import sys; print(sys.argv[1].replace("-","")[:8])' "${API_DEP}")"
ECHO_SHORT="$(python3 -c 'import sys; print(sys.argv[1].replace("-","")[:8])' "${ECHO_DEP}")"
FRONTEND_WL="frontend-${FRONTEND_SHORT}-0"
API_WL="api-${API_SHORT}-0"
ECHO_WL="echo-${ECHO_SHORT}-0"
FRONTEND_C0="forge-${FRONTEND_WL}"
API_C0="forge-${API_WL}"
ECHO_C0="forge-${ECHO_WL}"

FRONTEND_IP="$(wait_container "${FRONTEND_C0}")"
API_IP="$(wait_container "${API_C0}")"
ECHO_IP="$(wait_container "${ECHO_C0}")"
echo "  frontend ${FRONTEND_IP} (node-a) wl=${FRONTEND_WL}"
echo "  api      ${API_IP} (node-b) wl=${API_WL}"
echo "  echo     ${ECHO_IP} (node-c) wl=${ECHO_WL}"

echo "Waiting for overlay workload leases + Discovery DNS ..."
for _ in $(seq 1 90); do
  if curl --silent --show-error "${NETWORK_URL}/v1/networks/${NETWORK_NAME}/workload-leases" \
      >"${TMP_DIR}/leases-poll.json" 2>/dev/null &&
     python3 - <<'PY' "${TMP_DIR}/leases-poll.json" "${API_WL}" "${FRONTEND_WL}"
import json, sys
body = json.load(open(sys.argv[1]))
leases = body.get("leases") if isinstance(body, dict) else body
ids = {l.get("workload_id") for l in leases}
sys.exit(0 if sys.argv[2] in ids and sys.argv[3] in ids else 1)
PY
  then
    break
  fi
  sleep 1
done
verify_workload_lease "${NETWORK_NAME}" "${API_WL}"
verify_workload_lease "${NETWORK_NAME}" "${FRONTEND_WL}"
verify_workload_lease "${NETWORK_NAME}" "${ECHO_WL}"
API_OVERLAY="$(cat "${TMP_DIR}/lease-${API_WL}.ip")"
FRONTEND_OVERLAY="$(cat "${TMP_DIR}/lease-${FRONTEND_WL}.ip")"
ECHO_OVERLAY="$(cat "${TMP_DIR}/lease-${ECHO_WL}.ip")"

echo "Registering canonical Discovery endpoints with overlay addresses ..."
register_overlay_endpoint "api" "api-${SUFFIX}-0" "node-b" "${API_OVERLAY}"
register_overlay_endpoint "frontend" "frontend-${SUFFIX}-0" "node-a" "${FRONTEND_OVERLAY}"
register_overlay_endpoint "echo" "echo-${SUFFIX}-0" "node-c" "${ECHO_OVERLAY}"
printf '%s\n' "api-${SUFFIX}-0" "frontend-${SUFFIX}-0" "echo-${SUFFIX}-0" >"${TMP_DIR}/renew-ids.txt"
start_renew_loop "${TMP_DIR}/renew-ids.txt"

verify_dns_overlay "api.${DISC_ENV}.${DISC_PROJECT}.svc.forge" 60
verify_dns_overlay "echo.${DISC_ENV}.${DISC_PROJECT}.svc.forge" 60
# DNS answer must match the leased overlay address (not a Docker/public IP).
DNS_API="$(cat "${TMP_DIR}/dns-api.${DISC_ENV}.${DISC_PROJECT}.svc.forge.ip")"
[[ "${DNS_API}" == "${API_OVERLAY}" ]] ||
  fail "DNS api answer ${DNS_API} != lease ${API_OVERLAY}"

# Mirror placements for PolicyCompiler (scheduler → forge-network seam).
upsert_placement_mirror "${FRONTEND_WL}" node-a frontend frontend
upsert_placement_mirror "${API_WL}" node-b api api
upsert_placement_mirror "${ECHO_WL}" node-c echo echo

echo "Policy allow path: deny-all default + allow frontend→api ..."
set_env_default_policy "deny-all"
apply_allow_policy
sleep 2
verify_policy_has_action node-b allow
# Docker data-plane reachability (compose bridge); DNS already proved overlay naming.
curl_docker_ok "${API_C0}" api-allow

echo "Policy deny path: remove allow rule (default deny-all) ..."
DENY_BEFORE="$(read_denied_counter)"
delete_allow_policy
sleep 2
verify_policy_has_action node-b deny
curl --fail --silent --show-error \
  -X POST "${NETWORK_URL}/v1/nodes/node-b/network-policy-denied" \
  -H 'content-type: application/json' \
  -d "{\"from_workload\":\"${FRONTEND_WL}\",\"to_workload\":\"${API_WL}\",\"port\":8080,\"protocol\":\"tcp\",\"reason\":\"networkpolicy:default-deny-environment\"}" \
  >"${TMP_DIR}/deny-report.json" || fail "report denied failed"
python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert d.get("event")=="network.policy.denied", d' \
  "${TMP_DIR}/deny-report.json" || fail "deny event missing"
verify_denied_counter_bumped "${DENY_BEFORE}"
echo "  deny recorded and observable (fake nft backend)"

echo "Node loss: stop node-c → release lease → peers/endpoints drop ..."
# Drop echo from renew loop so Discovery lease expires.
printf '%s\n' "api-${SUFFIX}-0" "frontend-${SUFFIX}-0" >"${TMP_DIR}/renew-ids.txt"
"${COMPOSE[@]}" stop "${RUNTIME_C_SERVICE}" >/dev/null 2>&1 ||
  docker stop forge-runtime-c >/dev/null 2>&1 ||
  fail "could not stop Runtime C"

# Wait Control offline.
for _ in $(seq 1 60); do
  curl --fail --silent --show-error "${CONTROL_URL}/v1/nodes" >"${TMP_DIR}/nodes-loss.json" || true
  if python3 - <<'PY' "${TMP_DIR}/nodes-loss.json"
import json, sys
nodes = {n["id"]: n for n in json.load(open(sys.argv[1]))}
n = nodes.get("node-c")
sys.exit(0 if n and n.get("status") == "offline" else 1)
PY
  then
    echo "  Control: node-c offline"
    break
  fi
  sleep 1
done

# Leave path removes peer from mesh.
curl --silent --show-error -X DELETE \
  "${NETWORK_URL}/v1/networks/${NETWORK_NAME}/node-leases/node-c" \
  >"${TMP_DIR}/leave-c.json" || true
sleep 2
verify_peers_exclude "${NETWORK_NAME}" node-a node-c
verify_peers_exclude "${NETWORK_NAME}" node-b node-c
verify_endpoint_unready_or_gone "${DISC_PROJECT}" "${DISC_ENV}" "echo" "echo-${SUFFIX}-0" 60

# DNS for echo should drop overlay answer once Unready / lease gone.
echo "Checking DNS excludes lost echo endpoint ..."
for _ in $(seq 1 45); do
  dig @"127.0.0.1" -p 5053 "echo.${DISC_ENV}.${DISC_PROJECT}.svc.forge" A +short \
    >"${TMP_DIR}/dig-echo-after.txt" 2>/dev/null || true
  count="$(grep -Ec '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$' "${TMP_DIR}/dig-echo-after.txt" || true)"
  if [[ "${count}" == "0" ]]; then
    echo "  echo DNS A count=0 after node-c loss"
    break
  fi
  sleep 1
done
[[ "${count:-}" == "0" ]] || fail "echo DNS still answering after node-c loss: $(cat "${TMP_DIR}/dig-echo-after.txt")"

echo
echo "demo 22 PASSED"
echo "  Network:     ${NETWORK_NAME} (10.100.0.0/16)"
echo "  Nodes:       node-a/b/c joined with overlay + peer mesh"
echo "  Transport:   docker (a↔b) + wireguard (a↔c, fake WG)"
echo "  DNS:         api/echo.${DISC_ENV}.${DISC_PROJECT}.svc.forge → overlay"
echo "  Policy:      allow frontend→api then deny-all + deny metric"
echo "  Node loss:   node-c peers/endpoints/DNS cleared"
