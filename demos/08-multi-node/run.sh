#!/usr/bin/env bash
# Demo 08: multi-node scheduler distribution + reschedule (epic 08 acceptance gate).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# Pre-09 demos opt into the insecure auth bypass (Control default is enforce as of 09.06).
export FORGE_AUTH_MODE="${FORGE_AUTH_MODE:-dev}"
DEMO_DIR="${ROOT_DIR}/demos/08-multi-node"
APP_DIR="${ROOT_DIR}/demos/07-rolling-deployment/apps/demo"
COMPOSE=(
  docker compose
    -f "${ROOT_DIR}/compose.yaml"
    -f "${DEMO_DIR}/docker-compose.yml"
    --project-directory "${ROOT_DIR}"
)
CONTROL_URL="${FORGE_CONTROL_URL:-http://127.0.0.1:4001}"
RUNTIME_A_URL="${FORGE_RUNTIME_A_URL:-http://127.0.0.1:4102}"
RUNTIME_B_URL="${FORGE_RUNTIME_B_URL:-http://127.0.0.1:4112}"
CONTROL_SERVICE="forge-control"
RUNTIME_A_SERVICE="forge-runtime"
RUNTIME_B_SERVICE="forge-runtime-b"
POSTGRES_SERVICE="postgres"
REGISTRY_SERVICE="registry"
CLI_DIR="${ROOT_DIR}/tools/forge-cli"
FORGE_BIN="${CLI_DIR}/forge"
REGISTRY="${FORGE_REGISTRY:-localhost:5000}"
DEMO_IMAGE="${DEMO_IMAGE:-${REGISTRY}/demo:multi-node}"
PHASE="${1:-all}"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-multi-node-demo.XXXXXX")"
CONFIG_HOME="${TMP_DIR}/xdg-config"
export XDG_CONFIG_HOME="${CONFIG_HOME}"
export CI=1
export FORGE_PROFILE="${FORGE_PROFILE:-demo}"
export FORGE_ENDPOINT="${FORGE_ENDPOINT:-${CONTROL_URL}}"
export FORGE_LIFECYCLE_OWNER="${FORGE_LIFECYCLE_OWNER:-control}"
export FORGE_RECONCILE_INTERVAL_MS=1000
export FORGE_NODE_HEARTBEAT_TIMEOUT_S=8
export FORGE_RESCHEDULE_GRACE_S=3
export FORGE_LIVENESS_INTERVAL_MS=2000
export FORGE_HEARTBEAT_INTERVAL_MS=2000
export FORGE_SCHEDULER_STRATEGY=least-allocated
export FORGE_ANTI_AFFINITY_DEFAULT=soft
export FORGE_PROBE_INTERVAL_SECONDS=2
export FORGE_PROBE_FAILURE_THRESHOLD=2
mkdir -p "${CONFIG_HOME}"

TRACKED_DEPLOYMENTS=()
DEPLOYMENT_ID=""

cleanup() {
  local dep
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
  docker ps -aq --filter "name=forge-demo-" | while read -r cid; do
    [[ -n "${cid}" ]] || continue
    docker rm -f "${cid}" >/dev/null 2>&1 || true
  done
  "${COMPOSE[@]}" stop \
    "${RUNTIME_B_SERVICE}" "${RUNTIME_A_SERVICE}" "${CONTROL_SERVICE}" >/dev/null 2>&1 || true
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

dump_context() {
  echo "--- GET /v1/nodes ---" >&2
  curl --silent --show-error "${CONTROL_URL}/v1/nodes" >&2 || true
  echo >&2
  if [[ -n "${DEPLOYMENT_ID}" ]]; then
    echo "--- GET /v1/placements?deployment=${DEPLOYMENT_ID} ---" >&2
    curl --silent --show-error \
      "${CONTROL_URL}/v1/placements?deployment=${DEPLOYMENT_ID}" >&2 || true
    echo >&2
    echo "--- GET /v1/deployments/${DEPLOYMENT_ID}/reconcile ---" >&2
    curl --silent --show-error \
      "${CONTROL_URL}/v1/deployments/${DEPLOYMENT_ID}/reconcile" >&2 || true
    echo >&2
  fi
  echo "--- ${CONTROL_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=120 "${CONTROL_SERVICE}" >&2 || true
  echo "--- ${RUNTIME_A_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${RUNTIME_A_SERVICE}" >&2 || true
  echo "--- ${RUNTIME_B_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${RUNTIME_B_SERVICE}" >&2 || true
  echo "--- docker ps -a (forge-*) ---" >&2
  docker ps -a --filter name=forge- --format 'table {{.Names}}\t{{.Status}}\t{{.Image}}' >&2 || true
}

fail() {
  echo "Demo 08 failed: $*" >&2
  dump_context
  exit 1
}

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
  forge --output json "$@" >"${output}" || fail "forge $* failed (see stderr above)"
  python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "${output}" ||
    fail "forge $* did not emit stable JSON: $(cat "${output}")"
}

read_id() {
  python3 -c 'import json,sys,uuid; value=json.load(open(sys.argv[1]))["id"]; uuid.UUID(value); print(value)' "$1" ||
    fail "response did not contain a UUID id: $(cat "$1")"
}

track_deployment() {
  TRACKED_DEPLOYMENTS+=("$1")
}

ensure_demo_image() {
  echo "Building and pushing ${DEMO_IMAGE}..."
  docker build \
    --build-arg "VERSION=multi-node" \
    --build-arg "READY_FAIL=false" \
    -t "${DEMO_IMAGE}" \
    "${APP_DIR}" || fail "could not build ${DEMO_IMAGE}"
  docker push "${DEMO_IMAGE}" >/dev/null || fail "could not push ${DEMO_IMAGE}"
}

purge_stale_deployments() {
  echo "Purging leftover Control deployments (best effort)..."
  CONTROL_URL="${CONTROL_URL}" python3 - <<'PY' || true
import json
import urllib.error
import urllib.request

base = __import__("os").environ["CONTROL_URL"].rstrip("/")

def get(path):
    with urllib.request.urlopen(base + path, timeout=10) as resp:
        return json.load(resp)

def delete(path):
    req = urllib.request.Request(base + path, method="DELETE")
    try:
        urllib.request.urlopen(req, timeout=10).read()
    except urllib.error.HTTPError as exc:
        if exc.code not in (404, 204):
            raise

deleted = 0
projects = get("/v1/projects")
for project in projects:
    pid = project["id"]
    apps = get(f"/v1/projects/{pid}/applications")
    for app in apps:
        services = get(f"/v1/applications/{app['id']}/services")
        for svc in services:
            deps = get(f"/v1/services/{svc['id']}/deployments")
            for dep in deps:
                delete(f"/v1/deployments/{dep['id']}")
                deleted += 1
print(f"deleted {deleted} deployment(s)")
PY
  docker ps -aq --filter "label=forge.managed=true" | while read -r cid; do
    [[ -n "${cid}" ]] || continue
    docker rm -f "${cid}" >/dev/null 2>&1 || true
  done
}

create_hierarchy() {
  local suffix="$1"
  forge_json "${TMP_DIR}/project.json" project create --name "demo-multi-node-${suffix}"
  PROJECT_ID="$(read_id "${TMP_DIR}/project.json")"
  forge_json "${TMP_DIR}/environment.json" env create --project "${PROJECT_ID}" --name development
  ENVIRONMENT_ID="$(read_id "${TMP_DIR}/environment.json")"
  forge_json "${TMP_DIR}/application.json" app create --project "${PROJECT_ID}" --name demos
  APPLICATION_ID="$(read_id "${TMP_DIR}/application.json")"
  forge_json "${TMP_DIR}/service.json" service create --app "${APPLICATION_ID}" --name demo --port 8080
  SERVICE_ID="$(read_id "${TMP_DIR}/service.json")"
}

fetch_nodes() {
  curl --fail --silent --show-error "${CONTROL_URL}/v1/nodes" >"${TMP_DIR}/nodes.json" ||
    fail "GET /v1/nodes failed"
}

fetch_placements() {
  local deployment_id="$1"
  curl --fail --silent --show-error \
    "${CONTROL_URL}/v1/placements?deployment=${deployment_id}" \
    >"${TMP_DIR}/placements.json" || fail "GET /v1/placements failed"
}

wait_nodes_online() {
  local attempts="${1:-60}"
  echo "Waiting for node-a and node-b online (4 slots each) ..."
  for _ in $(seq 1 "${attempts}"); do
    fetch_nodes
    if python3 - <<'PY' "${TMP_DIR}/nodes.json"
import json, sys
nodes = {n["id"]: n for n in json.load(open(sys.argv[1]))}
for nid in ("node-a", "node-b"):
    n = nodes.get(nid)
    if not n or n.get("status") != "online":
        sys.exit(1)
    cap = (n.get("capacity") or {}).get("slots")
    if cap != 4:
        sys.exit(1)
sys.exit(0)
PY
    then
      echo "  node-a and node-b online OK"
      return 0
    fi
    sleep 1
  done
  fail "timed out waiting for node-a/node-b online"
}

count_placed() {
  local node_id="$1"
  NODE_ID="${node_id}" python3 - <<'PY' "${TMP_DIR}/placements.json"
import json, os, sys
node = os.environ["NODE_ID"]
placed = [
    p for p in json.load(open(sys.argv[1]))
    if p.get("status") == "placed" and p.get("node_id") == node
]
print(len(placed))
PY
}

wait_distribution() {
  local deployment_id="$1" attempts="${2:-90}"
  local a_count b_count
  echo "[distribute] waiting for placements 2+2 ..."
  for _ in $(seq 1 "${attempts}"); do
    fetch_placements "${deployment_id}"
    a_count="$(count_placed node-a)"
    b_count="$(count_placed node-b)"
    if [[ "${a_count}" == "2" && "${b_count}" == "2" ]]; then
      echo "[distribute] node-a=${a_count} node-b=${b_count} OK"
      return 0
    fi
    sleep 1
  done
  fail "distribution not 2+2 (node-a=${a_count:-?} node-b=${b_count:-?})"
}

wait_reconcile_deployed() {
  local deployment_id="$1" attempts="${2:-90}"
  local status=""
  echo "Waiting for deployment ${deployment_id} reconcile status=deployed ..."
  for _ in $(seq 1 "${attempts}"); do
    curl --fail --silent --show-error \
      "${CONTROL_URL}/v1/deployments/${deployment_id}/reconcile" \
      >"${TMP_DIR}/reconcile.json" || true
    if [[ -s "${TMP_DIR}/reconcile.json" ]]; then
      status="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("status") or "")' "${TMP_DIR}/reconcile.json")"
      if [[ "${status}" == "deployed" ]]; then
        echo "  status=${status}"
        return 0
      fi
    fi
    sleep 1
  done
  fail "deployment ${deployment_id} reconcile status=${status:-unknown}, want deployed"
}

wait_node_offline() {
  local node_id="$1" attempts="${2:-60}"
  echo "[reschedule] waiting for ${node_id} offline ..."
  for _ in $(seq 1 "${attempts}"); do
    fetch_nodes
    if NODE_ID="${node_id}" python3 - <<'PY' "${TMP_DIR}/nodes.json"
import json, os, sys
node = os.environ["NODE_ID"]
nodes = {n["id"]: n for n in json.load(open(sys.argv[1]))}
n = nodes.get(node)
sys.exit(0 if n and n.get("status") == "offline" else 1)
PY
    then
      return 0
    fi
    sleep 1
  done
  fail "timed out waiting for ${node_id} offline"
}

wait_reschedule_to_a() {
  local deployment_id="$1" attempts="${2:-90}"
  local a_count b_count pending
  echo "[reschedule] waiting for replicas on node-a=4 ..."
  for _ in $(seq 1 "${attempts}"); do
    fetch_placements "${deployment_id}"
    a_count="$(count_placed node-a)"
    b_count="$(count_placed node-b)"
    pending="$(python3 -c 'import json,sys; print(sum(1 for p in json.load(open(sys.argv[1])) if p.get("status")=="pending"))' "${TMP_DIR}/placements.json")"
    # Accept fully placed on node-a, or pending that then drains to node-a.
    if [[ "${a_count}" == "4" && "${b_count}" == "0" ]]; then
      echo "[reschedule] node-b offline ; replicas on node-a=${a_count} OK"
      return 0
    fi
    # Capacity-bound path: pending is temporarily OK while draining onto survivor.
    if [[ "${a_count}" -ge 2 && "${pending}" -gt 0 ]]; then
      sleep 1
      continue
    fi
    sleep 1
  done
  fail "reschedule incomplete (node-a=${a_count:-?} node-b=${b_count:-?} pending=${pending:-?})"
}

phase_distribute() {
  echo "[distribute] deploying replicas=4 ..."
  forge_json "${TMP_DIR}/dep.json" deployment create \
    --service "${SERVICE_ID}" \
    --image "${DEMO_IMAGE}" \
    --env "${ENVIRONMENT_ID}" \
    --replicas 4
  DEPLOYMENT_ID="$(read_id "${TMP_DIR}/dep.json")"
  track_deployment "${DEPLOYMENT_ID}"
  wait_reconcile_deployed "${DEPLOYMENT_ID}" 120
  wait_distribution "${DEPLOYMENT_ID}" 90
}

phase_reschedule() {
  [[ -n "${DEPLOYMENT_ID}" ]] || fail "reschedule phase requires a deployment from distribute"
  echo "[reschedule] stopping node-b ..."
  "${COMPOSE[@]}" stop "${RUNTIME_B_SERVICE}" >/dev/null ||
    docker stop forge-runtime-b >/dev/null ||
    fail "could not stop ${RUNTIME_B_SERVICE}"
  wait_node_offline "node-b" 60
  # Past heartbeat timeout + grace (8+3); poll placements for replacement.
  wait_reschedule_to_a "${DEPLOYMENT_ID}" 90
}

bootstrap() {
  echo "== Demo 08: multi-node scheduler (epic gate) =="
  echo "Building forge CLI..."
  make -C "${CLI_DIR}" build || fail "CLI build failed"
  [[ -x "${FORGE_BIN}" ]] || fail "forge binary missing at ${FORGE_BIN}"

  echo "Starting PostgreSQL, registry, Control, Runtime A/B..."
  "${COMPOSE[@]}" up -d "${POSTGRES_SERVICE}" "${REGISTRY_SERVICE}"
  echo "Waiting for Postgres..."
  for _ in $(seq 1 60); do
    if "${COMPOSE[@]}" exec -T "${POSTGRES_SERVICE}" pg_isready -U forge >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done
  "${COMPOSE[@]}" exec -T "${POSTGRES_SERVICE}" pg_isready -U forge >/dev/null 2>&1 ||
    fail "Postgres not ready"

  echo "Applying Control DB migrations..."
  PORT=8080 \
    DATABASE_URL="${DATABASE_URL:-jdbc:postgresql://127.0.0.1:5001/forge}" \
    DATABASE_USER="${DATABASE_USER:-forge}" \
    DATABASE_PASSWORD="${DATABASE_PASSWORD:-forge}" \
    DATABASE_SCHEMA=control \
    make -C "${ROOT_DIR}/services/forge-control" migrate ||
    fail "Control DB migrate failed"

  "${COMPOSE[@]}" up -d --build --force-recreate "${CONTROL_SERVICE}"
  wait_http "${CONTROL_URL}/health/ready" "Control"
  "${COMPOSE[@]}" stop "${RUNTIME_A_SERVICE}" "${RUNTIME_B_SERVICE}" >/dev/null 2>&1 || true
  purge_stale_deployments
  "${COMPOSE[@]}" up -d --build --force-recreate "${RUNTIME_A_SERVICE}" "${RUNTIME_B_SERVICE}"
  wait_http "${RUNTIME_A_URL}/health/ready" "Runtime node-a"
  wait_http "${RUNTIME_B_URL}/health/ready" "Runtime node-b"

  ctrl_timeout="$(docker exec forge-control printenv FORGE_NODE_HEARTBEAT_TIMEOUT_S 2>/dev/null || true)"
  ctrl_grace="$(docker exec forge-control printenv FORGE_RESCHEDULE_GRACE_S 2>/dev/null || true)"
  echo "  control FORGE_NODE_HEARTBEAT_TIMEOUT_S=${ctrl_timeout}"
  echo "  control FORGE_RESCHEDULE_GRACE_S=${ctrl_grace}"
  [[ "${ctrl_timeout}" == "8" ]] ||
    fail "Control FORGE_NODE_HEARTBEAT_TIMEOUT_S must be 8 (got: ${ctrl_timeout})"
  [[ "${ctrl_grace}" == "3" ]] ||
    fail "Control FORGE_RESCHEDULE_GRACE_S must be 3 (got: ${ctrl_grace})"

  node_a_id="$(docker exec forge-runtime-a printenv FORGE_NODE_ID 2>/dev/null || true)"
  node_b_id="$(docker exec forge-runtime-b printenv FORGE_NODE_ID 2>/dev/null || true)"
  [[ "${node_a_id}" == "node-a" ]] || fail "runtime-a FORGE_NODE_ID must be node-a (got: ${node_a_id})"
  [[ "${node_b_id}" == "node-b" ]] || fail "runtime-b FORGE_NODE_ID must be node-b (got: ${node_b_id})"

  wait_nodes_online 60
  ensure_demo_image

  echo "Configuring CLI profile ${FORGE_PROFILE} -> ${FORGE_ENDPOINT}..."
  forge config set endpoint "${FORGE_ENDPOINT}" --profile "${FORGE_PROFILE}"
  forge config use "${FORGE_PROFILE}"

  SUFFIX="$(date +%s)-$$"
  create_hierarchy "${SUFFIX}"
}

case "${PHASE}" in
  --phase)
    shift || true
    PHASE="${1:-all}"
    ;;
esac

case "${PHASE}" in
  distribute|--phase-distribute)
    bootstrap
    phase_distribute
    echo
    echo "demo 08 distribute PASSED"
    ;;
  reschedule|--phase-reschedule)
    bootstrap
    phase_distribute
    phase_reschedule
    echo
    echo "demo 08 reschedule PASSED"
    ;;
  all|ALL|"")
    bootstrap
    phase_distribute
    phase_reschedule
    echo
    echo "demo 08 PASSED"
    echo "  Project:      ${PROJECT_ID}"
    echo "  Environment:  ${ENVIRONMENT_ID}"
    echo "  Application:  ${APPLICATION_ID}"
    echo "  Service:      ${SERVICE_ID}"
    echo "  Deployment:   ${DEPLOYMENT_ID}"
    echo "  Image:        ${DEMO_IMAGE}"
    echo "  Nodes:        node-a (4 slots), node-b (4 slots → stopped)"
    echo "  Control URL:  ${CONTROL_URL}"
    ;;
  *)
    echo "Usage: $0 [all|distribute|reschedule|--phase distribute|--phase reschedule]" >&2
    exit 2
    ;;
esac
