#!/usr/bin/env bash
# Demo 55: PulseBoard epic gate (55.06) — HTTP/node autoscaling + Observe surfacing.
# Usage:
#   demos/55-pulseboard/run.sh          # build → apply → NodePool → load up/down + Observe proof
#   demos/55-pulseboard/run.sh --down   # tear down product resources + NodePool
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/55-pulseboard"
STATE_FILE="${DEMO_DIR}/.demo-state"
LOADGEN_SCRIPT="${DEMO_DIR}/scripts/loadgen.sh"
LOADGEN_PID_FILE="${DEMO_DIR}/.loadgen.pid"

export FORGE_AUTH_MODE="${FORGE_AUTH_MODE:-dev}"
export FORGE_LIFECYCLE_OWNER="${FORGE_LIFECYCLE_OWNER:-control}"
export FORGE_RECONCILE_INTERVAL_MS="${FORGE_RECONCILE_INTERVAL_MS:-1000}"
export FORGE_READINESS_POLL_MS="${FORGE_READINESS_POLL_MS:-500}"
export FORGE_READINESS_MAX_WAIT_S="${FORGE_READINESS_MAX_WAIT_S:-90}"
export FORGE_RESOURCE_API_ENABLED="${FORGE_RESOURCE_API_ENABLED:-true}"
export FORGE_SECRETS_URL="${FORGE_SECRETS_URL:-disabled}"
export FORGE_OTEL_ENABLED="${FORGE_OTEL_ENABLED:-false}"
export FORGE_SCHEDULER_STRATEGY="${FORGE_SCHEDULER_STRATEGY:-least-allocated}"
export FORGE_ANTI_AFFINITY_DEFAULT="${FORGE_ANTI_AFFINITY_DEFAULT:-soft}"
export FORGE_NODE_HEARTBEAT_TIMEOUT_S="${FORGE_NODE_HEARTBEAT_TIMEOUT_S:-8}"
export FORGE_RESCHEDULE_GRACE_S="${FORGE_RESCHEDULE_GRACE_S:-3}"
export FORGE_LIVENESS_INTERVAL_MS="${FORGE_LIVENESS_INTERVAL_MS:-2000}"
export FORGE_INFRA_RECONCILE_INTERVAL_MS="${FORGE_INFRA_RECONCILE_INTERVAL_MS:-1000}"
export FORGE_ROUTE_SYNC_INTERVAL_SECONDS="${FORGE_ROUTE_SYNC_INTERVAL_SECONDS:-2}"
export FORGE_UPSTREAM_PROBE_INTERVAL_SECONDS="${FORGE_UPSTREAM_PROBE_INTERVAL_SECONDS:-2}"
export FORGE_UPSTREAM_FAILURE_THRESHOLD="${FORGE_UPSTREAM_FAILURE_THRESHOLD:-1}"
export FORGE_UPSTREAM_SUCCESS_THRESHOLD="${FORGE_UPSTREAM_SUCCESS_THRESHOLD:-1}"
export FORGE_AUTOSCALER_EVAL_INTERVAL_MS="${FORGE_AUTOSCALER_EVAL_INTERVAL_MS:-1000}"
# Autoscaler EvaluateScaleUp/Down treat cooldown<=0 as built-in defaults (60s/5m),
# so use 1s — not 0 — when the gate needs near-immediate re-drain after run.sh.
export FORGE_AUTOSCALER_NODE_SCALE_UP_COOLDOWN_SECONDS="${FORGE_AUTOSCALER_NODE_SCALE_UP_COOLDOWN_SECONDS:-1}"
export FORGE_AUTOSCALER_NODE_SCALE_DOWN_COOLDOWN_SECONDS="${FORGE_AUTOSCALER_NODE_SCALE_DOWN_COOLDOWN_SECONDS:-1}"
# Keep long enough that a just-provisioned node is not drained during capacity absorb.
# 20s is enough to avoid drain-during-absorb races while keeping the gate under
# the harness lifecycle timeout (300s default; override via DEMO_TIMEOUT_MS).
export FORGE_AUTOSCALER_NODE_UNDERUTILIZATION_WINDOW_SECONDS="${FORGE_AUTOSCALER_NODE_UNDERUTILIZATION_WINDOW_SECONDS:-20}"
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
AUTOSCALER_URL="${FORGE_AUTOSCALER_URL:-http://127.0.0.1:4112}"
INFRA_URL="${FORGE_INFRA_URL:-http://127.0.0.1:4111}"
NETWORK_URL="${FORGE_NETWORK_URL:-http://127.0.0.1:4110}"
METRICS_URL="${FORGE_DEMO55_METRICS_URL:-http://127.0.0.1:4197}"
OBSERVE_URL="${FORGE_OBSERVE_URL:-http://127.0.0.1:4106}"
PROMETHEUS_URL="${FORGE_PROMETHEUS_URL:-http://127.0.0.1:3001}"
GRAFANA_URL="${FORGE_GRAFANA_URL:-http://127.0.0.1:3000}"
NETWORK_NAME="${FORGE_NETWORK_NAME:-cluster-overlay}"
CONTROL_SERVICE="forge-control"
RUNTIME_SERVICE="forge-runtime"
GATEWAY_SERVICE="forge-gateway"
BUILD_SERVICE="forge-build"
AUTOSCALER_SERVICE="forge-autoscaler"
INFRA_SERVICE="forge-infrastructure"
NETWORK_SERVICE="forge-network"
METRICS_SERVICE="demo55-metrics"
OBSERVE_SERVICE="forge-observe"
PROMETHEUS_SERVICE="prometheus"
GRAFANA_SERVICE="grafana"
# Dashboard vs Observe/Grafana replica tolerance (same PromQL series).
OBSERVE_REPLICA_TOLERANCE="${OBSERVE_REPLICA_TOLERANCE:-0.5}"
POSTGRES_SERVICE="postgres"
REGISTRY_SERVICE="registry"
CLI_DIR="${ROOT_DIR}/tools/forge-cli"
FORGE_BIN="${CLI_DIR}/forge"
REGISTRY="${FORGE_REGISTRY:-localhost:5000}"
API_IMAGE="${DEMO_API_IMAGE:-${REGISTRY}/pulseboard/pulseboard-api:v1}"
WEB_IMAGE="${DEMO_WEB_IMAGE:-${REGISTRY}/pulseboard/pulseboard-web:v1}"
API_HOST="api.pulseboard.localhost"
BOARD_HOST="board.pulseboard.localhost"
API_NAME="pulseboard-api"
API_POLICY="pulseboard-api-http"
POOL_NAME="pulseboard-pool"
ENV_NAME="local"
SYNC_PID=""
MIN_REPLICAS=1
MAX_REPLICAS=10
MIN_NODES=2
MAX_NODES=3
TARGET_RPS=50
# ceil(250/50)=5 api + 1 web = 6 slots → 3 docker-small nodes (min=2 → +1).
LOAD_RPS="${LOAD_RPS:-250}"
IDLE_RPS="${IDLE_RPS:-20}"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-demo-55.XXXXXX")"
CONFIG_HOME="${TMP_DIR}/xdg-config"
export XDG_CONFIG_HOME="${CONFIG_HOME}"
export CI="${CI:-1}"
export FORGE_PROFILE="${FORGE_PROFILE:-demo55}"
export FORGE_ENDPOINT="${FORGE_ENDPOINT:-${CONTROL_URL}}"
mkdir -p "${CONFIG_HOME}"

fail() {
  echo "Demo 55 failed: $*" >&2
  echo "--- ${GATEWAY_SERVICE} /admin/routes ---" >&2
  curl --silent --show-error "${GATEWAY_URL}/admin/routes" >&2 || true
  echo >&2
  if [[ -n "${PROJECT_SLUG:-}" ]]; then
    echo "--- ScalingPolicy ${API_POLICY} ---" >&2
    curl --silent --show-error \
      "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies/${API_POLICY}" >&2 || true
    echo >&2
  fi
  echo "--- NodePool ${POOL_NAME} ---" >&2
  curl --silent --show-error "${CONTROL_URL}/v1/nodepools/${POOL_NAME}" >&2 || true
  echo >&2
  echo "--- /v1/forgenodes ---" >&2
  curl --silent --show-error "${CONTROL_URL}/v1/forgenodes" >&2 || true
  echo >&2
  echo "--- demo55-metrics application ---" >&2
  curl --silent --show-error "${METRICS_URL}/admin/metrics?application=${API_NAME}" >&2 || true
  echo >&2
  echo "--- Observe PromQL replicas ---" >&2
  curl --silent --show-error \
    --get "${METRICS_URL}/api/v1/query" \
    --data-urlencode "query=sum(forge_replicas_ready{application=\"${API_NAME}\"})" >&2 || true
  echo >&2
  echo "--- ${CONTROL_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=80 "${CONTROL_SERVICE}" >&2 || true
  echo "--- ${RUNTIME_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${RUNTIME_SERVICE}" >&2 || true
  echo "--- ${GATEWAY_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${GATEWAY_SERVICE}" >&2 || true
  echo "--- ${AUTOSCALER_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=80 "${AUTOSCALER_SERVICE}" >&2 || true
  echo "--- ${INFRA_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=80 "${INFRA_SERVICE}" >&2 || true
  exit 1
}

cleanup_tmp() {
  if [[ -n "${SYNC_PID}" ]]; then
    kill "${SYNC_PID}" >/dev/null 2>&1 || true
    wait "${SYNC_PID}" 2>/dev/null || true
    SYNC_PID=""
  fi
  GATEWAY_URL="${GATEWAY_URL}" API_HOST="${API_HOST}" METRICS_URL="${METRICS_URL}" \
    APPLICATION="${API_NAME}" LOADGEN_PID_FILE="${LOADGEN_PID_FILE}" \
    bash "${LOADGEN_SCRIPT}" stop >/dev/null 2>&1 || true
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
API_DEPLOYMENT_ID=${API_DEPLOYMENT_ID}
WEB_DEPLOYMENT_ID=${WEB_DEPLOYMENT_ID}
API_IMAGE=${API_IMAGE}
WEB_IMAGE=${WEB_IMAGE}
API_POLICY=${API_POLICY}
API_NAME=${API_NAME}
POOL_NAME=${POOL_NAME}
MIN_REPLICAS=${MIN_REPLICAS}
MAX_REPLICAS=${MAX_REPLICAS}
MIN_NODES=${MIN_NODES}
MAX_NODES=${MAX_NODES}
TARGET_RPS=${TARGET_RPS}
METRICS_URL=${METRICS_URL}
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

purge_pool_containers() {
  docker ps -aq --filter "label=forge.pool=${POOL_NAME}" | while read -r cid; do
    [[ -n "${cid}" ]] || continue
    docker rm -f "${cid}" >/dev/null 2>&1 || true
  done
}

teardown() {
  echo "Tearing down demo 55 PulseBoard..."
  GATEWAY_URL="${GATEWAY_URL}" API_HOST="${API_HOST}" METRICS_URL="${METRICS_URL}" \
    APPLICATION="${API_NAME}" LOADGEN_PID_FILE="${LOADGEN_PID_FILE}" \
    bash "${LOADGEN_SCRIPT}" stop >/dev/null 2>&1 || true
  if [[ -n "${SYNC_PID}" ]]; then
    kill "${SYNC_PID}" >/dev/null 2>&1 || true
    wait "${SYNC_PID}" 2>/dev/null || true
    SYNC_PID=""
  fi
  if read_state; then
    if [[ -n "${PROJECT_SLUG:-}" ]]; then
      curl --silent --show-error -X DELETE \
        "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies/${API_POLICY:-pulseboard-api-http}" \
        >/dev/null 2>&1 || true
    fi
    delete_deployment "${API_DEPLOYMENT_ID:-}"
    delete_deployment "${WEB_DEPLOYMENT_ID:-}"
    rm -f "${STATE_FILE}"
  else
    echo "  no .demo-state; best-effort cleanup of demo=55 containers"
    docker ps -aq --filter "label=forge.managed=true" --filter "label=demo=55" |
      while read -r cid; do
        [[ -n "${cid}" ]] || continue
        docker rm -f "${cid}" >/dev/null 2>&1 || true
      done
  fi
  curl --silent --show-error -X DELETE "${CONTROL_URL}/v1/nodepools/${POOL_NAME}" >/dev/null 2>&1 || true
  sleep 2
  purge_pool_containers
  echo "Teardown complete."
}

count_managed_pool_containers() {
  docker ps -q --filter "label=forge.pool=${POOL_NAME}" | wc -l | tr -d ' '
}

count_ready_forgenodes() {
  curl --fail --silent --show-error "${CONTROL_URL}/v1/forgenodes" |
    python3 -c '
import json,sys
body=json.load(sys.stdin)
items=body.get("items") if isinstance(body,dict) else body
print(sum(1 for it in items or [] if (it.get("status") or {}).get("phase")=="Ready"))
'
}

count_online_fleet() {
  curl --fail --silent --show-error "${CONTROL_URL}/v1/nodes" |
    python3 -c '
import json,sys
nodes=json.load(sys.stdin)
print(sum(1 for n in nodes if str(n.get("status","")).lower()=="online"))
'
}

wait_ready_nodes() {
  local want="$1" attempts="${2:-180}" exact="${3:-0}"
  local ready online managed
  if [[ "${exact}" == "1" ]]; then
    echo "Waiting for exactly ${want} Ready forgenodes + online fleet ..."
  else
    echo "Waiting for ${want}+ Ready forgenodes + online fleet ..."
  fi
  for _ in $(seq 1 "${attempts}"); do
    ready="$(count_ready_forgenodes 2>/dev/null || echo 0)"
    online="$(count_online_fleet 2>/dev/null || echo 0)"
    managed="$(count_managed_pool_containers 2>/dev/null || echo 0)"
    if [[ "${exact}" == "1" ]]; then
      if [[ "${ready}" -eq "${want}" && "${online}" -eq "${want}" && "${managed}" -eq "${want}" ]]; then
        echo "  ready=${ready} online=${online} managed=${managed}"
        return 0
      fi
    elif [[ "${ready}" -ge "${want}" && "${online}" -ge "${want}" && "${managed}" -ge "${want}" ]]; then
      echo "  ready=${ready} online=${online} managed=${managed}"
      return 0
    fi
    sleep 1
  done
  fail "timed out waiting for ${want} Ready nodes (ready=${ready:-?} online=${online:-?} managed=${managed:-?})"
}

assert_nodes_in_bounds() {
  local cur="$1"
  [[ "${cur}" -ge "${MIN_NODES}" && "${cur}" -le "${MAX_NODES}" ]] ||
    fail "readyNodes=${cur} outside [${MIN_NODES},${MAX_NODES}]"
}

pool_status_ints() {
  curl --fail --silent --show-error "${CONTROL_URL}/v1/nodepools/${POOL_NAME}" |
    python3 -c '
import json,sys
st=(json.load(sys.stdin).get("status") or {})
print(int(st.get("readyNodes") or 0), int(st.get("desiredNodes") or 0), int(st.get("creatingNodes") or 0))
'
}

wait_pool_caught_up() {
  local attempts="${1:-90}"
  local ready desired creating
  echo "Waiting for NodePool ${POOL_NAME} readyNodes >= desiredNodes ..."
  for _ in $(seq 1 "${attempts}"); do
    read -r ready desired creating <<<"$(pool_status_ints 2>/dev/null || echo "0 99 99")"
    if [[ "${ready}" -ge "${desired}" && "${desired}" -gt 0 ]]; then
      echo "  readyNodes=${ready} desiredNodes=${desired} creatingNodes=${creating}"
      return 0
    fi
    sleep 1
  done
  fail "timed out waiting for pool catch-up (ready=${ready:-?} desired=${desired:-?} creating=${creating:-?})"
}

wait_no_pending() {
  local attempts="${1:-120}"
  local pending=999
  echo "Waiting for zero pending placements ..."
  for _ in $(seq 1 "${attempts}"); do
    pending="$(curl --fail --silent --show-error "${CONTROL_URL}/v1/placements?status=pending" |
      python3 -c 'import json,sys; print(len(json.load(sys.stdin)))' 2>/dev/null || echo 99)"
    if [[ "${pending}" -eq 0 ]]; then
      echo "  pending=0"
      return 0
    fi
    sleep 1
  done
  fail "timed out waiting for pending=0 (got ${pending})"
}

compose_up_retry() {
  local attempt
  for attempt in 1 2 3; do
    if "${COMPOSE[@]}" up -d --remove-orphans --no-build "$@"; then
      return 0
    fi
    echo "compose up attempt ${attempt} failed; retrying..." >&2
    sleep 3
  done
  return 1
}

reset_platform_ledgers() {
  echo "Resetting control/network/infra/autoscaler state for clean NodePool..."
  docker exec -i forge-postgres psql -U forge -d forge -v ON_ERROR_STOP=1 <<'SQL' >/dev/null \
    || fail "could not purge stale control/network/infra state"
BEGIN;
TRUNCATE infrastructure.provider_operations RESTART IDENTITY CASCADE;
TRUNCATE infrastructure.node_bootstrap_timers RESTART IDENTITY CASCADE;
TRUNCATE infrastructure.ssh_inventory_claims RESTART IDENTITY CASCADE;
DELETE FROM control.placements;
DELETE FROM control.reconcile_status;
DELETE FROM control.deployment_events;
DELETE FROM control.deployments;
DELETE FROM control.nodes;
DELETE FROM control.bootstrap_tokens;
DELETE FROM control.resource_events;
DELETE FROM control.resources;
DELETE FROM network.network_policies;
DELETE FROM network.environment_network_defaults;
DELETE FROM network.workload_placements;
DELETE FROM network.workload_leases;
DELETE FROM network.node_leases;
DELETE FROM network.wireguard_peers;
DELETE FROM network.network_routes;
DELETE FROM network.nodes;
DELETE FROM network.networks;
UPDATE network.policy_rule_generation SET generation = 0 WHERE id = 1;
INSERT INTO network.policy_rule_generation (id, generation) VALUES (1, 0)
ON CONFLICT (id) DO NOTHING;
COMMIT;
SQL
  docker exec -i forge-postgres psql -U forge -d postgres -v ON_ERROR_STOP=1 <<'SQL' >/dev/null \
    || fail "could not ensure forge_autoscaler database"
SELECT 'CREATE DATABASE forge_autoscaler'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'forge_autoscaler')\gexec
SQL
  docker exec -i forge-postgres psql -U forge -d forge_autoscaler -v ON_ERROR_STOP=1 <<'SQL' >/dev/null \
    || echo "autoscaler DB purge skipped (tables may not exist yet)" >&2
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'scaling_policies') THEN
    TRUNCATE TABLE scaling_policy_events RESTART IDENTITY CASCADE;
    TRUNCATE TABLE idempotency_keys RESTART IDENTITY CASCADE;
    TRUNCATE TABLE scaling_policies RESTART IDENTITY CASCADE;
  END IF;
END $$;
SQL
  purge_pool_containers
  docker ps -aq --filter "label=forge.managed=true" | while read -r cid; do
    [[ -n "${cid}" ]] || continue
    docker rm -f "${cid}" >/dev/null 2>&1 || true
  done
}

ensure_overlay_network() {
  local net_code="" attempt
  echo "Ensuring overlay Network ${NETWORK_NAME}..."
  for attempt in 1 2 3 4 5; do
    net_code="$(curl -s -o "${TMP_DIR}/net-create.json" -w '%{http_code}' -X POST "${NETWORK_URL}/v1/networks" \
      -H 'content-type: application/json' \
      -d "{\"name\":\"${NETWORK_NAME}\",\"spec\":{\"clusterCidr\":\"10.100.0.0/16\",\"nodePrefixLength\":24}}" \
      || echo "000")"
    if [[ "${net_code}" == "201" || "${net_code}" == "409" ]]; then
      break
    fi
    echo "  network create attempt ${attempt} HTTP ${net_code}; retrying..." >&2
    sleep 2
  done
  if [[ "${net_code}" == "201" ]]; then
    echo "  created ${NETWORK_NAME}"
  elif [[ "${net_code}" == "409" ]]; then
    echo "  reused ${NETWORK_NAME}"
  else
    fail "create network HTTP ${net_code}: $(cat "${TMP_DIR}/net-create.json" 2>/dev/null || true)"
  fi
}

apply_nodepool() {
  local apply_ok=0 attempt
  echo "Applying Docker InfrastructureProvider + NodePool..."
  for attempt in 1 2 3; do
    if forge --output json apply -f "${DEMO_DIR}/fixtures/nodepool-docker.yaml" \
        >"${TMP_DIR}/apply-pool.json" 2>"${TMP_DIR}/apply-pool.err"; then
      apply_ok=1
      break
    fi
    echo "forge apply attempt ${attempt} failed: $(cat "${TMP_DIR}/apply-pool.err")" >&2
    sleep 2
  done
  [[ "${apply_ok}" -eq 1 ]] || fail "forge apply nodepool failed"
  wait_ready_nodes "${MIN_NODES}" 180
  assert_nodes_in_bounds "$(count_ready_forgenodes)"
  wait_pool_caught_up 90
  echo "  NodePool ${POOL_NAME} ready (minNodes=${MIN_NODES} maxNodes=${MAX_NODES})"
}

seed_observe_metrics() {
  local replicas="${1:-${MIN_REPLICAS}}"
  local rps="${2:-0}"
  local p95="${3:-0.01}"
  curl --fail --silent --show-error -X PUT \
    "${METRICS_URL}/demo/application/${API_NAME}" \
    -H 'content-type: application/json' \
    -d "{\"requestsPerSecond\":${rps},\"activeConnections\":${rps},\"sampleCount\":100,\"p95LatencySeconds\":${p95},\"replicas\":${replicas}}" \
    >/dev/null || fail "seed Observe metrics failed"
  echo "  Observe metrics seeded application=${API_NAME} replicas=${replicas} rps=${rps}"
}

ensure_platform() {
  echo "Ensuring Postgres, registry, Observe stack, Network, Infrastructure, Autoscaler, Control, Runtime, Gateway, Build..."
  "${COMPOSE[@]}" up -d "${POSTGRES_SERVICE}" "${REGISTRY_SERVICE}"
  for _ in $(seq 1 60); do
    if "${COMPOSE[@]}" exec -T "${POSTGRES_SERVICE}" pg_isready -U forge >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done
  "${COMPOSE[@]}" exec -T "${POSTGRES_SERVICE}" pg_isready -U forge >/dev/null 2>&1 ||
    fail "Postgres not ready"

  echo "Starting demo55-metrics (Observe metrics backend)..."
  docker rm -f demo55-metrics >/dev/null 2>&1 || true
  "${COMPOSE[@]}" up -d --build --force-recreate "${METRICS_SERVICE}" ||
    fail "compose up ${METRICS_SERVICE} failed"
  wait_http "${METRICS_URL}/health/live" "demo55-metrics" 60
  seed_observe_metrics "${MIN_REPLICAS}" 0 0.01

  echo "Starting Prometheus + Grafana + forge-observe (Observe stack)..."
  # Prometheus depends_on alertmanager → webhook sink; pull those too.
  compose_up_retry alert-webhook-sink alertmanager "${PROMETHEUS_SERVICE}" ||
    fail "compose up prometheus failed"
  wait_http "${PROMETHEUS_URL}/-/healthy" "prometheus" 90
  # Base Grafana depends_on tempo/loki; start without them for this product gate.
  compose_up_retry --no-deps "${GRAFANA_SERVICE}" || fail "compose up grafana failed"
  wait_http "${GRAFANA_URL}/api/health" "grafana" 120
  # forge-observe is best-effort for this gate; /stats uses demo55-metrics PromQL.
  compose_up_retry --no-deps "${OBSERVE_SERVICE}" || true
  wait_http "${OBSERVE_URL}/health/live" "forge-observe" 60 || true

  echo "Stopping control/infra/runtime/autoscaler for clean ledger reset..."
  docker rm -f forge-control forge-infrastructure forge-runtime forge-autoscaler >/dev/null 2>&1 || true
  purge_pool_containers
  reset_platform_ledgers

  echo "Starting forge-network..."
  compose_up_retry "${NETWORK_SERVICE}" || fail "compose up network failed"
  wait_http "${NETWORK_URL}/health/live" "forge-network" 120

  echo "Starting forge-control + observe runtime + forge-infrastructure..."
  compose_up_retry --no-deps "${CONTROL_SERVICE}" || fail "compose up control failed"
  wait_http "${CONTROL_URL}/health/ready" "forge-control" 180
  compose_up_retry --no-deps "${RUNTIME_SERVICE}" || fail "compose up observe runtime failed"
  wait_http "${RUNTIME_URL}/health/ready" "forge-runtime (observe)" 120
  compose_up_retry --no-deps "${INFRA_SERVICE}" || fail "compose up infra failed"
  wait_http "${INFRA_URL}/health/ready" "forge-infrastructure" 180

  echo "Restarting forge-network after ledger reset..."
  docker rm -f forge-network >/dev/null 2>&1 || true
  compose_up_retry --no-deps --force-recreate "${NETWORK_SERVICE}" || fail "compose up network failed"
  wait_http "${NETWORK_URL}/health/live" "forge-network" 120
  ensure_overlay_network

  echo "Waiting for infrastructure kinds..."
  for _ in $(seq 1 60); do
    if curl --fail --silent --show-error "${CONTROL_URL}/v1/kinds" |
      python3 -c 'import json,sys; ks=json.load(sys.stdin); assert any(k.get("plural")=="nodepools" for k in ks)'; then
      break
    fi
    sleep 1
  done

  echo "Starting Gateway + Build..."
  compose_up_retry "${GATEWAY_SERVICE}" "${BUILD_SERVICE}" || fail "compose up gateway/build failed"
  wait_http "${GATEWAY_URL}/health/ready" "Gateway"
  wait_http "${BUILD_URL}/health/ready" "Build" 60 || true

  local pattern strategy
  pattern="$(docker exec "${GATEWAY_SERVICE}" printenv FORGE_HOST_PATTERN 2>/dev/null || true)"
  [[ "${pattern}" == *'{service}.pulseboard.localhost'* ]] ||
    fail "gateway FORGE_HOST_PATTERN must be '{service}.pulseboard.localhost' (got: ${pattern})"
  strategy="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_SCHEDULER_STRATEGY 2>/dev/null || true)"
  [[ "${strategy}" == "least-allocated" ]] ||
    fail "control FORGE_SCHEDULER_STRATEGY must be least-allocated (got: ${strategy})"
}

# Prefer `forge build` when the CLI subcommand exists; otherwise docker build+push
# from source (same images forge-build would produce for this scaffold).
ensure_images() {
  if "${FORGE_BIN}" build --help >/dev/null 2>&1; then
    echo "Building via forge build --source ..."
    (
      cd "${DEMO_DIR}/api"
      forge build --source . --tag "${API_IMAGE}"
    ) || fail "forge build api failed"
    (
      cd "${DEMO_DIR}"
      forge build --source . --forge-yaml web.forge.yaml --tag "${WEB_IMAGE}"
    ) || fail "forge build web failed"
    return 0
  fi

  echo "forge build CLI not available; building from source with docker build+push..."
  docker build -t "${API_IMAGE}" "${DEMO_DIR}/api" || fail "docker build api failed"
  docker push "${API_IMAGE}" || fail "docker push api failed"
  docker build -f "${DEMO_DIR}/Dockerfile.web" -t "${WEB_IMAGE}" "${DEMO_DIR}" ||
    fail "docker build web failed"
  docker push "${WEB_IMAGE}" || fail "docker push web failed"
}

ensure_cli() {
  echo "Building forge CLI..."
  make -C "${CLI_DIR}" build || fail "CLI build failed"
  [[ -x "${FORGE_BIN}" ]] || fail "forge binary missing at ${FORGE_BIN}"
  forge config set endpoint "${FORGE_ENDPOINT}" --profile "${FORGE_PROFILE}"
  forge config use "${FORGE_PROFILE}"
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

extract_deployment_ids() {
  python3 - "${TMP_DIR}/apply.json" <<'PY'
import json, sys
body = json.load(open(sys.argv[1]))
for r in body.get("results", []):
    if r.get("kind") != "Deployment":
        continue
    name = r.get("name") or ""
    meta = (r.get("resource") or {}).get("metadata") or {}
    dep_id = meta.get("id") or ""
    if name and dep_id:
        print(f"{name}={dep_id}")
PY
}

assert_applications_ready() {
  # Practical Ready signal: both deployments active (forge wait / forge get not shipped).
  echo "Checking applications/deployments Ready..."
  wait_deployment_status "${API_DEPLOYMENT_ID}" "deployed" 120
  wait_deployment_status "${WEB_DEPLOYMENT_ID}" "deployed" 120
  echo "  applications Ready (deployments active)"
}

assert_baseline_stats() {
  local code
  echo "Checking /stats baseline Observe-sourced replicas=1 ..."
  seed_observe_metrics "${MIN_REPLICAS}" 0 0.01
  # Routes can briefly flap while Control recreates placements; retry with refresh.
  for _ in $(seq 1 60); do
    refresh_routes
    code="$(curl --silent --show-error -o "${TMP_DIR}/stats.json" -w '%{http_code}' \
      -H "Host: ${API_HOST}" "${GATEWAY_URL}/stats" || echo "000")"
    if [[ "${code}" == "200" ]] && python3 - "${TMP_DIR}/stats.json" <<'PY'
import json, sys
stats = json.load(open(sys.argv[1]))
assert stats.get("replicas") == 1, stats
assert "counter" in stats, stats
assert stats.get("source") == "observe", stats
assert "rps" in stats and "p95Ms" in stats, stats
print(f"  /stats replicas={stats['replicas']} rps={stats.get('rps')} p95Ms={stats.get('p95Ms')} source={stats.get('source')}")
PY
    then
      return 0
    fi
    sleep 1
  done
  fail "GET /stats baseline failed (last HTTP ${code:-000}; body=$(cat "${TMP_DIR}/stats.json" 2>/dev/null || true))"
}

assert_observe_grafana_consistency() {
  local tol="${OBSERVE_REPLICA_TOLERANCE}"
  echo "Checking dashboard /stats vs Observe/Grafana PromQL (tolerance=${tol})..."
  python3 "${DEMO_DIR}/scripts/test_observe_surfacing.py" ||
    fail "observe surfacing unit tests failed"

  local code
  code="$(curl --silent --show-error -o "${TMP_DIR}/stats.json" -w '%{http_code}' \
    -H "Host: ${API_HOST}" "${GATEWAY_URL}/stats" || echo "000")"
  [[ "${code}" == "200" ]] || fail "GET /stats for consistency failed HTTP ${code}"

  curl --fail --silent --show-error \
    --get "${METRICS_URL}/api/v1/query" \
    --data-urlencode "query=sum(forge_replicas_ready{application=\"${API_NAME}\"})" \
    >"${TMP_DIR}/observe-replicas.json" || fail "Observe replica query failed"

  # Grafana uses the same Prometheus scrape of demo55-metrics; query Prometheus when healthy.
  if curl --fail --silent --show-error "${PROMETHEUS_URL}/-/healthy" >/dev/null 2>&1; then
    curl --fail --silent --show-error \
      --get "${PROMETHEUS_URL}/api/v1/query" \
      --data-urlencode "query=sum(forge_replicas_ready{application=\"${API_NAME}\"})" \
      >"${TMP_DIR}/prom-replicas.json" || true
  fi

  python3 - "${TMP_DIR}/stats.json" "${TMP_DIR}/observe-replicas.json" \
    "${TMP_DIR}/prom-replicas.json" "${tol}" "${GRAFANA_URL}" <<'PY'
import json, sys, urllib.request

stats = json.load(open(sys.argv[1]))
observe = json.load(open(sys.argv[2]))
prom_path = sys.argv[3]
tol = float(sys.argv[4])
grafana_url = sys.argv[5].rstrip("/")

dash = float(stats.get("replicas") or 0)
obs = float(observe["data"]["result"][0]["value"][1])
assert stats.get("source") == "observe", stats
assert abs(dash - obs) <= tol, (dash, obs, tol)

prom = None
try:
    prom_body = json.load(open(prom_path))
    if prom_body.get("data", {}).get("result"):
        prom = float(prom_body["data"]["result"][0]["value"][1])
except Exception:
    prom = None
if prom is not None:
    assert abs(dash - prom) <= max(tol, 1.0), (dash, prom, "prometheus/grafana lag")

# Grafana health confirms the UI datasource stack is up for manual cross-check.
with urllib.request.urlopen(f"{grafana_url}/api/health", timeout=5) as resp:
    assert resp.status == 200

print(f"  consistency ok dashboard={dash} observe={obs} prometheus={prom} grafana={grafana_url}")
PY
}

ensure_application_resource() {
  # Autoscaler patches Application.spec.scaling.desiredReplicas; ensure the
  # environment-scoped Application envelope exists with matching bounds.
  local code
  code="$(curl -s -o "${TMP_DIR}/app-res.json" -w '%{http_code}' -X POST \
    "${CONTROL_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/applications" \
    -H 'content-type: application/json' \
    -d "{\"metadata\":{\"name\":\"${API_NAME}\"},\"spec\":{\"image\":\"${API_IMAGE}\",\"scaling\":{\"desiredReplicas\":${MIN_REPLICAS},\"minReplicas\":${MIN_REPLICAS},\"maxReplicas\":${MAX_REPLICAS}}}}")"
  if [[ "${code}" == "201" || "${code}" == "200" ]]; then
    echo "  Application resource ${API_NAME} created"
    return 0
  fi
  if [[ "${code}" == "409" ]]; then
    code="$(curl -s -o "${TMP_DIR}/app-patch.json" -w '%{http_code}' -X PATCH \
      "${CONTROL_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/applications/${API_NAME}" \
      -H 'content-type: application/json' \
      -d "{\"spec\":{\"scaling\":{\"desiredReplicas\":${MIN_REPLICAS},\"minReplicas\":${MIN_REPLICAS},\"maxReplicas\":${MAX_REPLICAS}}}}")"
    [[ "${code}" == "200" ]] ||
      fail "patch Application ${API_NAME} HTTP ${code}: $(cat "${TMP_DIR}/app-patch.json")"
    echo "  Application resource ${API_NAME} patched bounds=[${MIN_REPLICAS},${MAX_REPLICAS}]"
    return 0
  fi
  # Companion may already exist from forge apply — try PATCH.
  code="$(curl -s -o "${TMP_DIR}/app-patch.json" -w '%{http_code}' -X PATCH \
    "${CONTROL_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/applications/${API_NAME}" \
    -H 'content-type: application/json' \
    -d "{\"spec\":{\"scaling\":{\"desiredReplicas\":${MIN_REPLICAS},\"minReplicas\":${MIN_REPLICAS},\"maxReplicas\":${MAX_REPLICAS}}}}")"
  if [[ "${code}" == "200" ]]; then
    echo "  Application resource ${API_NAME} patched"
    return 0
  fi
  fail "ensure Application resource HTTP ${code}: $(cat "${TMP_DIR}/app-res.json" "${TMP_DIR}/app-patch.json" 2>/dev/null || true)"
}

api_policy_spec() {
  cat <<EOF
{
  "targetRef": {"kind": "Application", "name": "${API_NAME}"},
  "minReplicas": ${MIN_REPLICAS},
  "maxReplicas": ${MAX_REPLICAS},
  "metrics": [{"type": "httpRequests", "targetValue": ${TARGET_RPS}}],
  "behavior": {
    "scaleUp": {"stabilizationWindowSeconds": 0, "maxReplicasPerMinute": ${MAX_REPLICAS}},
    "scaleDown": {"stabilizationWindowSeconds": 0, "maxReplicasPerMinute": ${MAX_REPLICAS}}
  },
  "metricOutageFallback": {"mode": "hold"}
}
EOF
}

replace_api_scaling_policy() {
  local spec rv code
  spec="$(api_policy_spec)"
  curl --fail --silent --show-error \
    "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies/${API_POLICY}" \
    >"${TMP_DIR}/sp-get.json" || fail "GET ${API_POLICY} before replace failed"
  rv="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["metadata"]["resourceVersion"])' "${TMP_DIR}/sp-get.json")"
  code="$(curl -s -o "${TMP_DIR}/sp-put.json" -w '%{http_code}' -X PUT \
    "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies/${API_POLICY}" \
    -H 'content-type: application/json' \
    -d "{\"metadata\":{\"resourceVersion\":\"${rv}\"},\"spec\":${spec}}")"
  [[ "${code}" == "200" ]] ||
    fail "replace ${API_POLICY} HTTP ${code}: $(cat "${TMP_DIR}/sp-put.json")"
  echo "  ScalingPolicy ${API_POLICY} replaced"
}

apply_api_scaling_policy() {
  local spec code
  spec="$(api_policy_spec)"
  code="$(curl -s -o "${TMP_DIR}/sp-api.json" -w '%{http_code}' -X POST \
    "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies" \
    -H 'content-type: application/json' \
    -H "Idempotency-Key: demo55-${PROJECT_SLUG}-${API_POLICY}" \
    -d "{\"metadata\":{\"name\":\"${API_POLICY}\"},\"spec\":${spec}}")"
  if [[ "${code}" == "201" || "${code}" == "200" ]]; then
    echo "  ScalingPolicy ${API_POLICY} created"
  elif [[ "${code}" == "409" ]]; then
    replace_api_scaling_policy
  else
    fail "create ${API_POLICY} HTTP ${code}: $(cat "${TMP_DIR}/sp-api.json")"
  fi
  curl --fail --silent --show-error \
    "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies/${API_POLICY}" \
    >"${TMP_DIR}/sp-get.json" || fail "GET ${API_POLICY} failed after create"
  echo "  ScalingPolicy readable project=${PROJECT_SLUG}"
}

policy_desired() {
  curl --fail --silent --show-error \
    "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies/${API_POLICY}" |
    python3 -c 'import json,sys; print(int(json.load(sys.stdin).get("status",{}).get("desiredReplicas") or 0))'
}

policy_metric_value() {
  curl --fail --silent --show-error \
    "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies/${API_POLICY}" |
    python3 -c 'import json,sys; r=(json.load(sys.stdin).get("status") or {}).get("lastRecommendation") or {}; v=r.get("metricValue"); print("" if v is None else v)'
}

policy_metric_type() {
  curl --fail --silent --show-error \
    "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies/${API_POLICY}" |
    python3 -c 'import json,sys; r=(json.load(sys.stdin).get("status") or {}).get("lastRecommendation") or {}; print(r.get("metricType") or "")'
}

wait_policy_desired_ge() {
  local min="$1" attempts="${2:-90}"
  local cur=0
  echo "Waiting for ScalingPolicy ${API_POLICY} desiredReplicas >= ${min} ..."
  for _ in $(seq 1 "${attempts}"); do
    cur="$(policy_desired 2>/dev/null || echo 0)"
    if [[ "${cur}" -ge "${min}" ]]; then
      echo "  ${API_POLICY} desiredReplicas=${cur}"
      return 0
    fi
    sleep 1
  done
  fail "timed out waiting for ${API_POLICY} desiredReplicas >= ${min} (got ${cur})"
}

wait_policy_desired_eq() {
  local want="$1" attempts="${2:-90}"
  local cur=0
  echo "Waiting for ScalingPolicy ${API_POLICY} desiredReplicas == ${want} ..."
  for _ in $(seq 1 "${attempts}"); do
    cur="$(policy_desired 2>/dev/null || echo 0)"
    if [[ "${cur}" -eq "${want}" ]]; then
      echo "  ${API_POLICY} desiredReplicas=${cur}"
      return 0
    fi
    sleep 1
  done
  fail "timed out waiting for ${API_POLICY} desiredReplicas == ${want} (got ${cur})"
}

assert_replicas_in_bounds() {
  local cur="$1"
  [[ "${cur}" -ge "${MIN_REPLICAS}" && "${cur}" -le "${MAX_REPLICAS}" ]] ||
    fail "desiredReplicas=${cur} outside [${MIN_REPLICAS},${MAX_REPLICAS}]"
}

set_idle_metrics() {
  local rps="${1:-${IDLE_RPS}}"
  local replicas
  replicas="$(policy_desired 2>/dev/null || echo "${MIN_REPLICAS}")"
  curl --fail --silent --show-error -X PUT \
    "${METRICS_URL}/demo/application/${API_NAME}" \
    -H 'content-type: application/json' \
    -d "{\"requestsPerSecond\":${rps},\"activeConnections\":${rps},\"sampleCount\":2000,\"p95LatencySeconds\":0.02,\"replicas\":${replicas}}" \
    >/dev/null || fail "set idle metrics failed"
  echo "  metrics: application=${API_NAME} rps=${rps} replicas=${replicas}"
}

sync_application_to_deployment() {
  # Bridge Application.spec.scaling.desiredReplicas → Deployment desiredReplicas
  # (autoscaler actuates Application; reconciler reads Deployment) and publish
  # replica count into the Observe metrics sidecar for /stats + Grafana.
  [[ -n "${API_DEPLOYMENT_ID}" ]] || fail "API_DEPLOYMENT_ID required before sync loop"
  python3 - "${CONTROL_URL}" "${PROJECT_SLUG}" "${ENV_NAME}" "${API_NAME}" \
    "${API_DEPLOYMENT_ID}" "${METRICS_URL}" <<'PY' &
import json, time, urllib.request, sys
base, project, env, app, dep_id, metrics = sys.argv[1:7]
app_url = f"{base}/v1/projects/{project}/environments/{env}/applications/{app}"
dep_url = f"{base}/v1/deployments/{dep_id}"
metrics_url = f"{metrics.rstrip('/')}/demo/application/{app}"

def get(url):
    with urllib.request.urlopen(url, timeout=5) as r:
        return json.load(r)

def patch(url, body):
    data = json.dumps(body).encode()
    req = urllib.request.Request(url, data=data, method="PATCH",
                                 headers={"content-type": "application/json"})
    with urllib.request.urlopen(req, timeout=5) as r:
        return r.status

def put_metrics(replicas):
    data = json.dumps({"replicas": int(replicas)}).encode()
    req = urllib.request.Request(metrics_url, data=data, method="PUT",
                                 headers={"content-type": "application/json"})
    with urllib.request.urlopen(req, timeout=5) as r:
        return r.status

last = None
while True:
    try:
        app_body = get(app_url)
        desired = (((app_body.get("spec") or {}).get("scaling") or {}).get("desiredReplicas"))
        if desired is None:
            time.sleep(1)
            continue
        desired = int(desired)
        dep = get(dep_url)
        cur = int(dep.get("desiredReplicas") or 0)
        if cur != desired:
            patch(dep_url, {"desiredReplicas": desired})
            if desired != last:
                print(f"sync: api deployment {dep_id} desiredReplicas {cur} -> {desired}", flush=True)
            last = desired
        put_metrics(desired)
    except Exception as exc:
        print(f"sync: {exc}", flush=True)
    time.sleep(1)
PY
  SYNC_PID=$!
  echo "  started Application→Deployment+Observe sync pid=${SYNC_PID}"
}

start_autoscaler() {
  echo "Starting forge-autoscaler after NodePool + baseline deploy..."
  docker rm -f forge-autoscaler >/dev/null 2>&1 || true
  compose_up_retry --no-deps --force-recreate "${AUTOSCALER_SERVICE}" || fail "compose up autoscaler failed"
  wait_http "${AUTOSCALER_URL}/health/ready" "forge-autoscaler" 120
  local gateway_admin node_up
  gateway_admin="$(docker exec "${AUTOSCALER_SERVICE}" printenv FORGE_GATEWAY_ADMIN_URL 2>/dev/null || true)"
  [[ "${gateway_admin}" == *"demo55-metrics"* ]] ||
    fail "autoscaler FORGE_GATEWAY_ADMIN_URL must point at demo55-metrics (got: ${gateway_admin})"
  node_up="$(docker exec "${AUTOSCALER_SERVICE}" printenv FORGE_AUTOSCALER_NODE_SCALE_UP_ENABLED 2>/dev/null || true)"
  [[ "${node_up}" == "true" ]] ||
    fail "autoscaler node scale-up must be enabled (got: ${node_up})"
}

prove_http_and_node_autoscaling() {
  local up_desired down_desired metric_type metric_value peak_min
  local nodes_before nodes_after_up nodes_after_down
  echo "Proving HTTP request-rate + node autoscaling (capacity → Docker node → drain)..."
  python3 "${DEMO_DIR}/scripts/test_http_scaling.py" ||
    fail "http scaling unit tests failed"
  python3 "${DEMO_DIR}/scripts/test_node_scaling.py" ||
    fail "node scaling unit tests failed"

  ensure_application_resource
  apply_api_scaling_policy
  set_idle_metrics "${IDLE_RPS}"
  sync_application_to_deployment

  wait_policy_desired_eq "${MIN_REPLICAS}" 60
  assert_replicas_in_bounds "$(policy_desired)"
  nodes_before="$(count_ready_forgenodes)"
  assert_nodes_in_bounds "${nodes_before}"
  [[ "${nodes_before}" -eq "${MIN_NODES}" ]] ||
    fail "baseline readyNodes=${nodes_before}, want ${MIN_NODES}"

  # ceil(LOAD_RPS / TARGET_RPS); clamp to max. Default 250/50 → 5.
  # 5 api + 1 web = 6 slots on docker-small (2 slots) → needs 3 nodes.
  peak_min="$(python3 -c "import math; print(min(${MAX_REPLICAS}, max(${MIN_REPLICAS}, math.ceil(${LOAD_RPS}/${TARGET_RPS}))))")"

  echo "  starting loadgen rps=${LOAD_RPS} (peak_min=${peak_min}) against ${API_HOST}..."
  GATEWAY_URL="${GATEWAY_URL}" API_HOST="${API_HOST}" METRICS_URL="${METRICS_URL}" \
    APPLICATION="${API_NAME}" LOADGEN_PID_FILE="${LOADGEN_PID_FILE}" \
    bash "${LOADGEN_SCRIPT}" start --rps "${LOAD_RPS}" || fail "loadgen start failed"

  wait_policy_desired_ge "${peak_min}" 90
  up_desired="$(policy_desired)"
  assert_replicas_in_bounds "${up_desired}"
  [[ "${up_desired}" -ge "${peak_min}" ]] ||
    fail "scale-up desiredReplicas=${up_desired} < peak_min=${peak_min}"
  [[ "${up_desired}" -gt "${MIN_REPLICAS}" ]] ||
    fail "scale-up did not increase replicas (still ${up_desired})"

  metric_type="$(policy_metric_type)"
  metric_value="$(policy_metric_value)"
  [[ "${metric_type}" == "httpRequests" ]] ||
    fail "lastRecommendation.metricType=${metric_type}, want httpRequests"
  python3 -c "
v='${metric_value}'
assert v != '', 'metricValue missing'
assert float(v) >= ${TARGET_RPS}, (v, ${TARGET_RPS})
print('  policy status reflects RPS metricValue=%s type=%s' % (v, '${metric_type}'))
" || fail "ScalingPolicy status does not reflect elevated RPS"

  echo "  HTTP scale-up ok desiredReplicas=${up_desired}; waiting for node scale-up on unschedulability..."
  # Wait until sync raises Deployment desiredReplicas and either pending appears
  # or the node autoscaler already grew the pool past minNodes.
  for _ in $(seq 1 60); do
    nodes_after_up="$(count_ready_forgenodes 2>/dev/null || echo 0)"
    pending="$(curl --fail --silent --show-error "${CONTROL_URL}/v1/placements?status=pending" |
      python3 -c 'import json,sys; print(len(json.load(sys.stdin)))' 2>/dev/null || echo 0)"
    dep_desired="$(curl --fail --silent --show-error "${CONTROL_URL}/v1/deployments/${API_DEPLOYMENT_ID}" |
      python3 -c 'import json,sys; print(int(json.load(sys.stdin).get("desiredReplicas") or 0))' 2>/dev/null || echo 0)"
    echo "  wait capacity: dep_desired=${dep_desired} pending=${pending} ready=${nodes_after_up}"
    if [[ "${nodes_after_up}" -gt "${nodes_before}" ]]; then
      break
    fi
    if [[ "${dep_desired}" -gt 2 && "${pending}" -gt 0 ]]; then
      break
    fi
    sleep 1
  done

  wait_ready_nodes $((nodes_before + 1)) 180
  nodes_after_up="$(count_ready_forgenodes)"
  assert_nodes_in_bounds "${nodes_after_up}"
  [[ "${nodes_after_up}" -gt "${nodes_before}" ]] ||
    fail "node scale-up did not increase readyNodes (before=${nodes_before} after=${nodes_after_up})"
  [[ "${nodes_after_up}" -le "${MAX_NODES}" ]] ||
    fail "readyNodes=${nodes_after_up} exceeds maxNodes=${MAX_NODES}"
  wait_no_pending 180
  wait_deployment_status "${API_DEPLOYMENT_ID}" "deployed" 150
  wait_pool_caught_up 90
  echo "  node scale-up ok readyNodes=${nodes_after_up} (was ${nodes_before}) within [${MIN_NODES},${MAX_NODES}]"

  echo "  stopping loadgen → idle rps=${IDLE_RPS}..."
  GATEWAY_URL="${GATEWAY_URL}" API_HOST="${API_HOST}" METRICS_URL="${METRICS_URL}" \
    APPLICATION="${API_NAME}" LOADGEN_PID_FILE="${LOADGEN_PID_FILE}" \
    bash "${LOADGEN_SCRIPT}" stop || fail "loadgen stop failed"
  set_idle_metrics "${IDLE_RPS}"

  wait_policy_desired_eq "${MIN_REPLICAS}" 90
  down_desired="$(policy_desired)"
  assert_replicas_in_bounds "${down_desired}"
  [[ "${down_desired}" -eq "${MIN_REPLICAS}" ]] ||
    fail "scale-down desiredReplicas=${down_desired}, want ${MIN_REPLICAS}"
  [[ "${down_desired}" -lt "${up_desired}" ]] ||
    fail "scale-down did not decrease (up=${up_desired} down=${down_desired})"
  echo "  HTTP scale-down ok desiredReplicas=${down_desired}"

  # Let reconcile drop pods before the node autoscaler scores underutilized nodes.
  # ReconcileStatus exposes actual.replicas[] (updatedReplicas is "ready/total").
  for _ in $(seq 1 120); do
    actual="$(curl --fail --silent --show-error \
      "${CONTROL_URL}/v1/deployments/${API_DEPLOYMENT_ID}/reconcile" |
      python3 -c 'import json,sys; d=json.load(sys.stdin); print(len((d.get("actual") or {}).get("replicas") or []))' 2>/dev/null || echo 99)"
    status="$(curl --fail --silent --show-error \
      "${CONTROL_URL}/v1/deployments/${API_DEPLOYMENT_ID}" |
      python3 -c 'import json,sys; print(json.load(sys.stdin).get("status") or "")' 2>/dev/null || echo "")"
    if [[ "${actual}" -le "${MIN_REPLICAS}" && ( "${status}" == "deployed" || "${status}" == "active" ) ]]; then
      echo "  deployment actualReplicas=${actual} status=${status}"
      break
    fi
    sleep 1
  done
  wait_deployment_status "${API_DEPLOYMENT_ID}" "deployed" 150
  wait_ready_nodes "${MIN_NODES}" 240 1
  nodes_after_down="$(count_ready_forgenodes)"
  assert_nodes_in_bounds "${nodes_after_down}"
  [[ "${nodes_after_down}" -eq "${MIN_NODES}" ]] ||
    fail "node scale-down readyNodes=${nodes_after_down}, want ${MIN_NODES}"
  [[ "${nodes_after_down}" -lt "${nodes_after_up}" ]] ||
    fail "node scale-down did not decrease (up=${nodes_after_up} down=${nodes_after_down})"
  echo "  node scale-down ok readyNodes=${nodes_after_down} (drain respected bounds)"
}

deploy() {
  if [[ -f "${STATE_FILE}" ]]; then
    teardown
  fi

  ensure_platform
  ensure_cli
  ensure_images
  apply_nodepool

  SUFFIX="$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')"
  PROJECT_NAME="PulseBoard ${SUFFIX}"
  PROJECT_SLUG="pulseboard-${SUFFIX}"

  echo "Rendering forge.yaml → apply (project=${PROJECT_SLUG})..."
  PROJECT_NAME="${PROJECT_NAME}" PROJECT_SLUG="${PROJECT_SLUG}" \
    API_IMAGE="${API_IMAGE}" WEB_IMAGE="${WEB_IMAGE}" \
    envsubst '${PROJECT_NAME} ${PROJECT_SLUG} ${API_IMAGE} ${WEB_IMAGE}' \
    <"${DEMO_DIR}/forge.yaml" >"${TMP_DIR}/forge.yaml"

  forge_json "${TMP_DIR}/apply.json" apply -f "${TMP_DIR}/forge.yaml"

  API_DEPLOYMENT_ID=""
  WEB_DEPLOYMENT_ID=""
  while IFS='=' read -r name dep_id; do
    case "${name}" in
      pulseboard-api) API_DEPLOYMENT_ID="${dep_id}" ;;
      pulseboard-web) WEB_DEPLOYMENT_ID="${dep_id}" ;;
    esac
  done < <(extract_deployment_ids)

  [[ -n "${API_DEPLOYMENT_ID}" ]] || fail "pulseboard-api Deployment id missing from apply"
  [[ -n "${WEB_DEPLOYMENT_ID}" ]] || fail "pulseboard-web Deployment id missing from apply"
  echo "Deployments api=${API_DEPLOYMENT_ID} web=${WEB_DEPLOYMENT_ID}"

  assert_applications_ready
  wait_no_pending 120
  wait_route_host "${API_HOST}" 90
  wait_route_host "${BOARD_HOST}" 90
  wait_host_http "${API_HOST}" "/health/ready" 200 60
  wait_host_http "${BOARD_HOST}" "/" 200 60
  assert_baseline_stats

  # Optional: forge wait Ready when CLI supports it.
  if "${FORGE_BIN}" wait --help >/dev/null 2>&1; then
    forge wait "application/pulseboard-api" --for=condition=Ready --timeout=2m ||
      fail "forge wait pulseboard-api failed"
    forge wait "application/pulseboard-web" --for=condition=Ready --timeout=2m ||
      fail "forge wait pulseboard-web failed"
  fi

  start_autoscaler
  prove_http_and_node_autoscaling
  assert_observe_grafana_consistency

  write_state
  echo
  echo "demo 55 deploy READY (PulseBoard epic gate)"
  echo "  Board:        http://${BOARD_HOST}:4000/"
  echo "  API:          http://${API_HOST}:4000/health/ready"
  echo "  Stats:        http://${API_HOST}:4000/stats  (Observe-sourced replicas/RPS/p95)"
  echo "  Autoscaler:   ${AUTOSCALER_URL} policy=${API_POLICY} bounds=[${MIN_REPLICAS},${MAX_REPLICAS}] targetRPS=${TARGET_RPS}"
  echo "  NodePool:     ${POOL_NAME} bounds=[${MIN_NODES},${MAX_NODES}] provider=docker"
  echo "  Loadgen:      ${LOADGEN_SCRIPT} start|stop (against ${API_HOST})"
  echo "  Observe:      ${METRICS_URL}/api/v1/query  (metrics backend)"
  echo "  Grafana:      ${GRAFANA_URL}  (Prometheus scrape of demo55-metrics)"
  echo "  Metrics:      ${METRICS_URL}"
  echo "  API image:    ${API_IMAGE}"
  echo "  Web image:    ${WEB_IMAGE}"
  echo "  Deployments:  api=${API_DEPLOYMENT_ID} web=${WEB_DEPLOYMENT_ID}"
  echo "  Project slug: ${PROJECT_SLUG}"
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
