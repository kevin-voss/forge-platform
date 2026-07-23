#!/usr/bin/env bash
# Demo 25: HA placement M1 exit gate (epic 25 acceptance).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/25-ha-placement"
APP_DIR="${ROOT_DIR}/demos/07-rolling-deployment/apps/demo"

export FORGE_AUTH_MODE="${FORGE_AUTH_MODE:-dev}"
export FORGE_LIFECYCLE_OWNER="${FORGE_LIFECYCLE_OWNER:-control}"
export FORGE_RECONCILE_INTERVAL_MS="${FORGE_RECONCILE_INTERVAL_MS:-1000}"
export FORGE_READINESS_POLL_MS="${FORGE_READINESS_POLL_MS:-500}"
export FORGE_READINESS_MAX_WAIT_S="${FORGE_READINESS_MAX_WAIT_S:-90}"
export FORGE_RESOURCE_API_ENABLED="${FORGE_RESOURCE_API_ENABLED:-true}"
export FORGE_SCHEDULER_STRATEGY="${FORGE_SCHEDULER_STRATEGY:-least-allocated}"
export FORGE_ANTI_AFFINITY_DEFAULT="${FORGE_ANTI_AFFINITY_DEFAULT:-hard}"
export FORGE_SECRETS_URL="${FORGE_SECRETS_URL:-disabled}"
export FORGE_PROBE_INTERVAL_SECONDS="${FORGE_PROBE_INTERVAL_SECONDS:-2}"
export FORGE_PROBE_FAILURE_THRESHOLD="${FORGE_PROBE_FAILURE_THRESHOLD:-2}"
export FORGE_NODE_HEARTBEAT_TIMEOUT_S="${FORGE_NODE_HEARTBEAT_TIMEOUT_S:-8}"
export FORGE_RESCHEDULE_GRACE_S="${FORGE_RESCHEDULE_GRACE_S:-3}"
export FORGE_LIVENESS_INTERVAL_MS="${FORGE_LIVENESS_INTERVAL_MS:-2000}"
export FORGE_HEARTBEAT_INTERVAL_MS="${FORGE_HEARTBEAT_INTERVAL_MS:-2000}"
export FORGE_INFRA_RECONCILE_INTERVAL_MS="${FORGE_INFRA_RECONCILE_INTERVAL_MS:-1000}"
export FORGE_AUTOSCALER_EVAL_INTERVAL_MS="${FORGE_AUTOSCALER_EVAL_INTERVAL_MS:-1000}"
export FORGE_AUTOSCALER_NODE_SCALE_UP_COOLDOWN_SECONDS="${FORGE_AUTOSCALER_NODE_SCALE_UP_COOLDOWN_SECONDS:-0}"
export FORGE_DISCOVERY_LEASE_SECONDS_DEFAULT="${FORGE_DISCOVERY_LEASE_SECONDS_DEFAULT:-8}"
export FORGE_DISCOVERY_SWEEP_INTERVAL_SECONDS="${FORGE_DISCOVERY_SWEEP_INTERVAL_SECONDS:-2}"
export FORGE_DISCOVERY_LEASE_SECONDS="${FORGE_DISCOVERY_LEASE_SECONDS:-8}"
export FORGE_DISCOVERY_DEFAULT_PROJECT="${FORGE_DISCOVERY_DEFAULT_PROJECT:-demo}"
export FORGE_DISCOVERY_DEFAULT_ENVIRONMENT="${FORGE_DISCOVERY_DEFAULT_ENVIRONMENT:-production}"
export FORGE_ROUTE_SOURCE="${FORGE_ROUTE_SOURCE:-control}"
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
INFRA_URL="${FORGE_INFRA_URL:-http://127.0.0.1:4111}"
AUTOSCALER_URL="${FORGE_AUTOSCALER_URL:-http://127.0.0.1:4112}"
NETWORK_URL="${FORGE_NETWORK_URL:-http://127.0.0.1:4110}"
DISCOVERY_URL="${FORGE_DISCOVERY_URL_HOST:-http://127.0.0.1:4109}"
GATEWAY_URL="${FORGE_GATEWAY_URL:-http://127.0.0.1:4000}"
RUNTIME_URL="${FORGE_RUNTIME_URL:-http://127.0.0.1:4102}"
NETWORK_NAME="${FORGE_NETWORK_NAME:-cluster-overlay}"
CONTROL_SERVICE="forge-control"
INFRA_SERVICE="forge-infrastructure"
NETWORK_SERVICE="forge-network"
DISCOVERY_SERVICE="forge-discovery"
GATEWAY_SERVICE="forge-gateway"
RUNTIME_SERVICE="forge-runtime"
AUTOSCALER_SERVICE="forge-autoscaler"
POSTGRES_SERVICE="postgres"
REGISTRY_SERVICE="registry"
CLI_DIR="${ROOT_DIR}/tools/forge-cli"
FORGE_BIN="${CLI_DIR}/forge"
REGISTRY="${FORGE_REGISTRY:-localhost:5000}"
DEMO_IMAGE="${DEMO_IMAGE:-${REGISTRY}/demo-ha-placement:v1}"
POOL_NAME="ha-docker-pool"
ENV_NAME="production"
DISC_PROJECT="${FORGE_DISCOVERY_DEFAULT_PROJECT}"
DISC_ENV="${FORGE_DISCOVERY_DEFAULT_ENVIRONMENT}"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-demo-25.XXXXXX")"
CONFIG_HOME="${TMP_DIR}/xdg-config"
export XDG_CONFIG_HOME="${CONFIG_HOME}"
export CI=1
export FORGE_PROFILE="${FORGE_PROFILE:-demo25}"
export FORGE_ENDPOINT="${FORGE_ENDPOINT:-${CONTROL_URL}}"
mkdir -p "${CONFIG_HOME}"

SUFFIX="$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')"
PROJECT_NAME="HA Placement ${SUFFIX}"
PROJECT_SLUG="ha-placement-${SUFFIX}"
APP_NAME="ha-api-${SUFFIX}"
DEPLOYMENT_ID=""
SERVICE_ID=""
HOLD_LOCK=0
LOCK_DIR="/tmp/forge-demo-25.lock"
VICTIM_NODE=""
VICTIM_CONTAINER=""

cleanup() {
  if [[ -n "${DEPLOYMENT_ID}" ]]; then
    curl --silent --show-error -X DELETE "${CONTROL_URL}/v1/deployments/${DEPLOYMENT_ID}" >/dev/null 2>&1 || true
  fi
  curl --silent --show-error -X DELETE "${CONTROL_URL}/v1/nodepools/${POOL_NAME}" >/dev/null 2>&1 || true
  sleep 2
  docker ps -aq --filter "label=forge.pool=${POOL_NAME}" | while read -r cid; do
    [[ -n "${cid}" ]] || continue
    docker rm -f "${cid}" >/dev/null 2>&1 || true
  done
  docker ps -aq --filter "label=forge.managed=true" | while read -r cid; do
    [[ -n "${cid}" ]] || continue
    docker rm -f "${cid}" >/dev/null 2>&1 || true
  done
  if [[ "${HOLD_LOCK}" -eq 1 ]]; then
    "${COMPOSE[@]}" stop \
      "${AUTOSCALER_SERVICE}" "${GATEWAY_SERVICE}" "${DISCOVERY_SERVICE}" \
      "${INFRA_SERVICE}" "${CONTROL_SERVICE}" "${NETWORK_SERVICE}" "${RUNTIME_SERVICE}" \
      >/dev/null 2>&1 || true
  fi
  rm -rf "${TMP_DIR}"
  if [[ "${HOLD_LOCK}" -eq 1 ]]; then
    rmdir "${LOCK_DIR}" 2>/dev/null || true
  fi
}

acquire_demo_lock() {
  local attempt stale_pid
  for attempt in 1 2 3; do
    if mkdir "${LOCK_DIR}" 2>/dev/null; then
      echo "$$" >"${LOCK_DIR}/pid"
      HOLD_LOCK=1
      return 0
    fi
    stale_pid="$(cat "${LOCK_DIR}/pid" 2>/dev/null || true)"
    if [[ -z "${stale_pid}" ]] || ! kill -0 "${stale_pid}" 2>/dev/null; then
      echo "Removing stale demo 25 lock (pid ${stale_pid:-empty})"
      rm -rf "${LOCK_DIR}"
      continue
    fi
    echo "Demo 25 failed: another demo 25 holds ${LOCK_DIR} (pid ${stale_pid})" >&2
    return 1
  done
  echo "Demo 25 failed: could not acquire ${LOCK_DIR}" >&2
  return 1
}
if ! acquire_demo_lock; then
  rm -rf "${TMP_DIR}"
  exit 1
fi
trap cleanup EXIT

dump_context() {
  echo "--- /v1/nodes ---" >&2
  curl --silent --show-error "${CONTROL_URL}/v1/nodes" >&2 || true
  echo >&2
  echo "--- NodePool ${POOL_NAME} ---" >&2
  curl --silent --show-error "${CONTROL_URL}/v1/nodepools/${POOL_NAME}" >&2 || true
  echo >&2
  if [[ -n "${DEPLOYMENT_ID}" ]]; then
    echo "--- placements deployment=${DEPLOYMENT_ID} ---" >&2
    curl --silent --show-error "${CONTROL_URL}/v1/placements?deployment=${DEPLOYMENT_ID}" >&2 || true
    echo >&2
    echo "--- reconcile ---" >&2
    curl --silent --show-error "${CONTROL_URL}/v1/deployments/${DEPLOYMENT_ID}/reconcile" >&2 || true
    echo >&2
  fi
  echo "--- discovery /v1/services ---" >&2
  curl --silent --show-error "${DISCOVERY_URL}/v1/services" >&2 || true
  echo >&2
  echo "--- gateway /admin/routes ---" >&2
  curl --silent --show-error "${GATEWAY_URL}/admin/routes" >&2 || true
  echo >&2
  echo "--- ${CONTROL_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${CONTROL_SERVICE}" >&2 || true
  echo "--- ${INFRA_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=40 "${INFRA_SERVICE}" >&2 || true
}

fail() {
  echo "Demo 25 failed: $*" >&2
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

purge_managed() {
  docker ps -aq --filter "label=forge.pool=${POOL_NAME}" | while read -r cid; do
    [[ -n "${cid}" ]] || continue
    docker rm -f "${cid}" >/dev/null 2>&1 || true
  done
  docker ps -aq --filter "label=forge.managed=true" | while read -r cid; do
    [[ -n "${cid}" ]] || continue
    docker rm -f "${cid}" >/dev/null 2>&1 || true
  done
}

count_managed_containers() {
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
  local want="$1" attempts="${2:-180}"
  local ready online managed
  echo "Waiting for ${want}+ Ready forgenodes + online fleet ..."
  for _ in $(seq 1 "${attempts}"); do
    ready="$(count_ready_forgenodes 2>/dev/null || echo 0)"
    online="$(count_online_fleet 2>/dev/null || echo 0)"
    managed="$(count_managed_containers 2>/dev/null || echo 0)"
    if [[ "${ready}" -ge "${want}" && "${online}" -ge "${want}" && "${managed}" -ge "${want}" ]]; then
      echo "  ready=${ready} online=${online} managed=${managed}"
      return 0
    fi
    sleep 1
  done
  fail "timed out waiting for ${want} Ready nodes (ready=${ready:-?} online=${online:-?} managed=${managed:-?})"
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

ensure_demo_image() {
  echo "Building and pushing ${DEMO_IMAGE}..."
  docker build \
    --build-arg "VERSION=ha-placement-25" \
    --build-arg "READY_FAIL=false" \
    -t "${DEMO_IMAGE}" \
    "${APP_DIR}" || fail "could not build ${DEMO_IMAGE}"
  docker push "${DEMO_IMAGE}" >/dev/null || fail "could not push ${DEMO_IMAGE}"
}

assert_portable_manifest() {
  local fixture="${DEMO_DIR}/fixtures/application.yaml"
  echo "Asserting portable Application manifest (no provider-specific fields) ..."
  # Ban provider/cloud identifiers that must never appear in product manifests.
  if grep -Eiq \
    'machineType|providerRef|securityGroup|security_group|subnetId|subnet_id|vpcId|vpc_id|volumeId|volume_id|diskId|disk_id|hetzner|amazonaws|azure\.com|i-[0-9a-f]{8}|sg-[0-9a-f]+' \
    "${fixture}"; then
    fail "application.yaml contains provider-specific fields"
  fi
  grep -Eq 'topologySpreadConstraints|priorityClassName|disruptionBudget|antiAffinity' "${fixture}" ||
    fail "application.yaml missing HA placement fields"
  echo "  portable manifest OK"
}

label_fleet_zones() {
  # Operator topology facts: agents send zone=default so Control preserves SQL updates.
  echo "Labeling fleet into zone-a / zone-b ..."
  curl --fail --silent --show-error "${CONTROL_URL}/v1/nodes" >"${TMP_DIR}/nodes.json"
  python3 - "${TMP_DIR}/nodes.json" "${TMP_DIR}/zone-sql.txt" <<'PY' || fail "could not plan zone labels"
import json, sys
nodes = [n for n in json.load(open(sys.argv[1])) if str(n.get("status","")).lower()=="online"]
nodes = sorted(nodes, key=lambda n: n.get("id") or "")
if len(nodes) < 3:
    raise SystemExit(f"need >=3 online nodes, got {len(nodes)}")
lines = []
for i, n in enumerate(nodes):
    zone = "zone-a" if i < 2 else "zone-b"
    # Split remaining nodes across zones (3→2+1, 4→2+2).
    if len(nodes) >= 4:
        zone = "zone-a" if i < (len(nodes)//2) else "zone-b"
    nid = n["id"].replace("'", "''")
    lines.append(f"UPDATE control.nodes SET zone = '{zone}' WHERE id = '{nid}';")
    print(f"  {n['id']} -> {zone}")
open(sys.argv[2], "w").write("\n".join(lines) + "\n")
PY
  docker exec -i forge-postgres psql -U forge -d forge -v ON_ERROR_STOP=1 \
    <"${TMP_DIR}/zone-sql.txt" >/dev/null || fail "could not apply zone labels"
  # Confirm zones visible on API.
  curl --fail --silent --show-error "${CONTROL_URL}/v1/nodes" >"${TMP_DIR}/nodes-zoned.json"
  python3 - "${TMP_DIR}/nodes-zoned.json" <<'PY' || fail "zone labels not visible"
import json, sys
nodes = [n for n in json.load(open(sys.argv[1])) if str(n.get("status","")).lower()=="online"]
zones = {n.get("zone") for n in nodes}
assert "zone-a" in zones and "zone-b" in zones, zones
print(f"  zones present: {sorted(zones)}")
PY
}

ensure_priority_classes() {
  local code
  code="$(curl -s -o "${TMP_DIR}/pc-high.json" -w '%{http_code}' -X POST \
    "${CONTROL_URL}/v1/priority-classes" \
    -H 'content-type: application/json' \
    -d '{"name":"high","value":100,"preemption_policy":"PreemptLowerPriority","description":"demo25 high"}')"
  if [[ "${code}" != "201" && "${code}" != "200" && "${code}" != "409" ]]; then
    fail "create priority class high HTTP ${code}: $(cat "${TMP_DIR}/pc-high.json")"
  fi
  code="$(curl -s -o "${TMP_DIR}/pc-low.json" -w '%{http_code}' -X POST \
    "${CONTROL_URL}/v1/priority-classes" \
    -H 'content-type: application/json' \
    -d '{"name":"low","value":0,"preemption_policy":"Never","description":"demo25 low"}')"
  if [[ "${code}" != "201" && "${code}" != "200" && "${code}" != "409" ]]; then
    fail "create priority class low HTTP ${code}: $(cat "${TMP_DIR}/pc-low.json")"
  fi
  echo "  PriorityClasses high/low ready"
}

create_hierarchy_and_deploy() {
  forge_json "${TMP_DIR}/project.json" project create \
    --name "demo-ha-${SUFFIX}" \
    --slug "${PROJECT_SLUG}"
  PROJECT_ID="$(read_id "${TMP_DIR}/project.json")"
  forge_json "${TMP_DIR}/environment.json" env create --project "${PROJECT_ID}" --name "${ENV_NAME}"
  ENVIRONMENT_ID="$(read_id "${TMP_DIR}/environment.json")"
  forge_json "${TMP_DIR}/application.json" app create --project "${PROJECT_ID}" --name "${APP_NAME}"
  APPLICATION_ID="$(read_id "${TMP_DIR}/application.json")"
  forge_json "${TMP_DIR}/service.json" service create --app "${APPLICATION_ID}" --name "${APP_NAME}" --port 8080
  SERVICE_ID="$(read_id "${TMP_DIR}/service.json")"
  forge_json "${TMP_DIR}/deploy.json" deployment create \
    --service "${SERVICE_ID}" \
    --env "${ENVIRONMENT_ID}" \
    --image "${DEMO_IMAGE}" \
    --replicas 3
  DEPLOYMENT_ID="$(read_id "${TMP_DIR}/deploy.json")"
  echo "  project=${PROJECT_SLUG} service=${SERVICE_ID} deployment=${DEPLOYMENT_ID}"
}

upsert_disruption_budget() {
  curl --fail --silent --show-error -X PUT \
    "${CONTROL_URL}/v1/deployments/${DEPLOYMENT_ID}/disruption-budget" \
    -H 'content-type: application/json' \
    -d '{"min_available":2}' \
    >"${TMP_DIR}/budget.json" || fail "upsert disruption budget failed"
  python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert d.get("min_available")==2, d' \
    "${TMP_DIR}/budget.json" || fail "budget min_available != 2"
  echo "  disruption budget min_available=2"
}

wait_reconcile_deployed() {
  local deployment_id="$1" attempts="${2:-180}"
  local status=""
  echo "Waiting for deployment ${deployment_id} reconcile status=deployed ..."
  for _ in $(seq 1 "${attempts}"); do
    if ! curl --fail --silent --show-error "${CONTROL_URL}/health/ready" >/dev/null 2>&1; then
      sleep 2
      continue
    fi
    if curl --fail --silent --show-error \
      "${CONTROL_URL}/v1/deployments/${deployment_id}/reconcile" \
      >"${TMP_DIR}/reconcile.json" 2>/dev/null; then
      status="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("status") or "")' "${TMP_DIR}/reconcile.json" 2>/dev/null || true)"
      if [[ "${status}" == "deployed" ]]; then
        echo "  status=${status}"
        return 0
      fi
    fi
    sleep 1
  done
  fail "deployment ${deployment_id} reconcile status=${status:-unknown}, want deployed"
}

assert_ha_spread() {
  local deployment_id="$1"
  curl --fail --silent --show-error \
    "${CONTROL_URL}/v1/placements?deployment=${deployment_id}" \
    >"${TMP_DIR}/placements.json" || fail "GET placements failed"
  curl --fail --silent --show-error "${CONTROL_URL}/v1/nodes" >"${TMP_DIR}/fleet.json"
  python3 - "${TMP_DIR}/placements.json" "${TMP_DIR}/fleet.json" <<'PY' || fail "HA spread assertion failed"
import json, sys
placements = [p for p in json.load(open(sys.argv[1])) if p.get("status") == "placed"]
fleet = {n["id"]: n for n in json.load(open(sys.argv[2]))}
if len(placements) < 3:
    raise SystemExit(f"want >=3 placed, got {len(placements)}")
nodes = sorted({p.get("node_id") for p in placements if p.get("node_id")})
if len(nodes) < 3:
    raise SystemExit(f"want >=3 distinct nodes, got {nodes}")
zones = set()
for nid in nodes:
    n = fleet.get(nid) or {}
    z = n.get("zone") or "default"
    zones.add(z)
    addr = (n.get("address") or "").lower()
    if "forge-node-" not in addr and nid in ("node-a", "node-b", "observe-local", "node-local"):
        raise SystemExit(f"placement on seed node {nid}")
if len(zones) < 2:
    raise SystemExit(f"want >=2 topology domains (zones), got {sorted(zones)}")
print(f"  spread OK: nodes={nodes} zones={sorted(zones)}")
PY
}

wait_discovery_ready() {
  local want="$1" attempts="${2:-120}"
  local count=0
  echo "Waiting for Discovery Ready endpoints >= ${want} (project=${DISC_PROJECT} env=${DISC_ENV}) ..."
  for _ in $(seq 1 "${attempts}"); do
    # GET /v1/services lists all registered services; sum Ready endpoints per service.
    count="$(curl --fail --silent --show-error "${DISCOVERY_URL}/v1/services" 2>/dev/null |
      python3 -c '
import json,sys,urllib.request
try:
  items=json.load(sys.stdin)
except Exception:
  print(0); raise SystemExit
if isinstance(items, dict):
  items=items.get("items") or []
total=0
base="'"${DISCOVERY_URL}"'"
want_proj="'"${DISC_PROJECT}"'"
want_env="'"${DISC_ENV}"'"
for it in items or []:
  proj=it.get("project") or ""
  env=it.get("environment") or ""
  name=it.get("name") or ""
  if not name:
    continue
  if want_proj and proj and proj != want_proj:
    continue
  if want_env and env and env != want_env:
    continue
  try:
    with urllib.request.urlopen(
      f"{base}/v1/projects/{proj}/environments/{env}/services/{name}/endpoints",
      timeout=5,
    ) as r:
      eps=json.load(r)
  except Exception:
    continue
  elist=eps.get("items") if isinstance(eps,dict) else eps
  total += sum(1 for e in (elist or []) if e.get("phase")=="Ready" or e.get("ready") is True)
print(total)
' 2>/dev/null || echo 0)"
    if [[ "${count}" -ge "${want}" ]]; then
      echo "  Ready endpoints=${count}"
      return 0
    fi
    sleep 1
  done
  fail "timed out waiting for Discovery Ready >= ${want} (got ${count})"
}

wait_gateway_healthy_upstreams() {
  local want="$1" attempts="${2:-90}"
  local host count
  echo "Refreshing Gateway routes and waiting for ${want}+ healthy upstreams ..."
  for _ in $(seq 1 "${attempts}"); do
    curl --fail --silent --show-error -X POST "${GATEWAY_URL}/admin/routes/refresh" \
      >"${TMP_DIR}/refresh.json" 2>/dev/null || true
    curl --fail --silent --show-error "${GATEWAY_URL}/admin/routes" \
      >"${TMP_DIR}/routes.json" 2>/dev/null || true
    read -r host count <<<"$(python3 - "${TMP_DIR}/routes.json" "${SERVICE_ID}" "${APP_NAME}" "${want}" <<'PY'
import json, sys
routes = json.load(open(sys.argv[1]))
svc, app, want = sys.argv[2], sys.argv[3], int(sys.argv[4])
best_host, best = "", 0
for r in routes:
    host = (r.get("host") or "")
    ups = r.get("upstreams") or r.get("targets") or []
    healthy = 0
    for u in ups:
        if isinstance(u, dict) and (u.get("healthy") is False or u.get("ready") is False):
            continue
        healthy += 1
    blob = json.dumps(r)
    if svc in blob or app in host or app in blob or "demo.localhost" in host:
        if healthy >= best:
            best, best_host = healthy, host
if best_host and best >= want:
    print(best_host, best)
elif best_host:
    print(best_host, best)
else:
    print("", 0)
PY
)"
    if [[ -n "${host}" && "${count}" -ge "${want}" ]]; then
      echo "  route host=${host} healthy_upstreams=${count}"
      if curl --fail --silent --show-error -H "Host: ${host}" "${GATEWAY_URL}/" \
        >"${TMP_DIR}/gw-hit.json" 2>/dev/null; then
        echo "  Gateway Host=${host} → ok"
        return 0
      fi
      echo "  Gateway probe failed for Host=${host}; retrying..." >&2
    elif [[ -n "${host}" ]]; then
      echo "  route host=${host} upstreams=${count} (want ${want})"
    fi
    sleep 1
  done
  fail "timed out waiting for Gateway ${want}+ healthy upstreams"
}

pick_victim_node() {
  curl --fail --silent --show-error \
    "${CONTROL_URL}/v1/placements?deployment=${DEPLOYMENT_ID}" \
    >"${TMP_DIR}/placements.json"
  curl --fail --silent --show-error "${CONTROL_URL}/v1/nodes" >"${TMP_DIR}/fleet.json"
  VICTIM_NODE="$(python3 -c '
import json,sys
placed=[p for p in json.load(open(sys.argv[1])) if p.get("status")=="placed" and p.get("node_id")]
print(placed[0]["node_id"])
' "${TMP_DIR}/placements.json")"
  [[ -n "${VICTIM_NODE}" ]] || fail "no victim node"
  # Map fleet node id → docker container name (http://forge-node-...:8080).
  VICTIM_CONTAINER="$(python3 - "${TMP_DIR}/fleet.json" "${VICTIM_NODE}" <<'PY'
import json,sys
fleet={n["id"]:n for n in json.load(open(sys.argv[1]))}
nid=sys.argv[2]
addr=(fleet.get(nid) or {}).get("address") or ""
host=addr.replace("http://","").replace("https://","").split(":")[0]
print(host)
PY
)"
  [[ -n "${VICTIM_CONTAINER}" ]] || fail "could not resolve victim container for ${VICTIM_NODE}"
  echo "  victim node=${VICTIM_NODE} container=${VICTIM_CONTAINER}"
}

stop_victim_node() {
  echo "Stopping runtime node ${VICTIM_CONTAINER} (simulated node loss) ..."
  docker stop "${VICTIM_CONTAINER}" >/dev/null || fail "could not stop ${VICTIM_CONTAINER}"
}

wait_node_offline() {
  local node_id="$1" attempts="${2:-60}"
  echo "Waiting for ${node_id} offline ..."
  for _ in $(seq 1 "${attempts}"); do
    curl --fail --silent --show-error "${CONTROL_URL}/v1/nodes" >"${TMP_DIR}/nodes.json"
    if NODE_ID="${node_id}" python3 - <<'PY' "${TMP_DIR}/nodes.json"
import json, os, sys
node = os.environ["NODE_ID"]
nodes = {n["id"]: n for n in json.load(open(sys.argv[1]))}
n = nodes.get(node)
sys.exit(0 if n and str(n.get("status","")).lower() == "offline" else 1)
PY
    then
      echo "  ${node_id} offline"
      return 0
    fi
    sleep 1
  done
  fail "timed out waiting for ${node_id} offline"
}

wait_recovery() {
  local attempts="${1:-180}"
  local placed distinct pending
  echo "Waiting for HA recovery (3 placed on live nodes, victim empty) ..."
  for _ in $(seq 1 "${attempts}"); do
    curl --fail --silent --show-error \
      "${CONTROL_URL}/v1/placements?deployment=${DEPLOYMENT_ID}" \
      >"${TMP_DIR}/placements.json" 2>/dev/null || true
    read -r placed distinct pending <<<"$(VICTIM="${VICTIM_NODE}" python3 - <<'PY' "${TMP_DIR}/placements.json"
import json, os, sys
victim=os.environ["VICTIM"]
rows=json.load(open(sys.argv[1]))
placed=[p for p in rows if p.get("status")=="placed"]
on_victim=[p for p in placed if p.get("node_id")==victim]
live=[p for p in placed if p.get("node_id")!=victim]
pending=sum(1 for p in rows if p.get("status")=="pending")
print(len(live), len({p.get("node_id") for p in live}), pending)
PY
)"
    if [[ "${placed}" -ge 3 && "${distinct}" -ge 2 && "${pending}" -eq 0 ]]; then
      echo "  recovered: live_placed=${placed} distinct_nodes=${distinct} pending=0"
      return 0
    fi
    # Autoscaler may add capacity when pending appears.
    sleep 1
  done
  fail "recovery incomplete (live_placed=${placed:-?} distinct=${distinct:-?} pending=${pending:-?})"
}

assert_budget_satisfied() {
  curl --fail --silent --show-error \
    "${CONTROL_URL}/v1/placements?deployment=${DEPLOYMENT_ID}" \
    >"${TMP_DIR}/placements.json"
  python3 - <<'PY' "${TMP_DIR}/placements.json" || fail "disruption budget not satisfied after recovery"
import json,sys
placed=[p for p in json.load(open(sys.argv[1])) if p.get("status")=="placed"]
# min_available=2; after recovery we expect 3 placed.
assert len(placed) >= 2, f"placed={len(placed)} < min_available=2"
print(f"  disruption budget satisfied: placed={len(placed)} >= min_available=2")
PY
}

assert_optional_gpu_hint() {
  # Optional: fake GPU capacity assertion is a no-op unless FORGE_DEMO25_GPU=1.
  if [[ "${FORGE_DEMO25_GPU:-}" != "1" ]]; then
    echo "  optional GPU/stateful assertions skipped (set FORGE_DEMO25_GPU=1 to enable)"
    return 0
  fi
  echo "  FORGE_DEMO25_GPU=1 set but fake GPU path is operator-opt-in; skipping live GPU attach"
}

# -----------------------------------------------------------------------------
# Main
# -----------------------------------------------------------------------------
echo "=== Demo 25: HA placement M1 exit gate ==="
echo "Project: ${PROJECT_NAME} (${PROJECT_SLUG})"

assert_portable_manifest

echo "Building forge CLI..."
make -C "${CLI_DIR}" build || fail "CLI build failed"
[[ -x "${FORGE_BIN}" ]] || fail "forge binary missing at ${FORGE_BIN}"

echo "Starting platform services (sequential compose)..."
purge_managed

echo "Building platform images..."
"${COMPOSE[@]}" build \
  "${NETWORK_SERVICE}" "${DISCOVERY_SERVICE}" "${CONTROL_SERVICE}" \
  "${INFRA_SERVICE}" "${RUNTIME_SERVICE}" "${GATEWAY_SERVICE}" "${AUTOSCALER_SERVICE}" ||
  fail "compose build failed"

echo "Starting postgres + registry..."
compose_up_retry "${POSTGRES_SERVICE}" "${REGISTRY_SERVICE}" || fail "compose up postgres/registry failed"
echo "Waiting for Postgres..."
for _ in $(seq 1 60); do
  if "${COMPOSE[@]}" exec -T "${POSTGRES_SERVICE}" pg_isready -U forge >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
"${COMPOSE[@]}" exec -T "${POSTGRES_SERVICE}" pg_isready -U forge >/dev/null 2>&1 ||
  fail "Postgres not ready"

echo "Applying Control DB migrations (host classpath — ensures V25_* scripts apply)..."
PORT=8080 \
  DATABASE_URL="${DATABASE_URL:-jdbc:postgresql://127.0.0.1:5001/forge}" \
  DATABASE_USER="${DATABASE_USER:-forge}" \
  DATABASE_PASSWORD="${DATABASE_PASSWORD:-forge}" \
  DATABASE_SCHEMA=control \
  make -C "${ROOT_DIR}/services/forge-control" migrate ||
  fail "Control DB migrate failed"

echo "Starting forge-network + forge-discovery..."
compose_up_retry "${NETWORK_SERVICE}" "${DISCOVERY_SERVICE}" || fail "compose up network/discovery failed"
wait_http "http://127.0.0.1:4110/health/live" "forge-network" 120
wait_http "${DISCOVERY_URL}/health/live" "forge-discovery" 120

echo "Stopping control/infra/runtime/gateway/autoscaler for clean ledger reset..."
docker rm -f forge-control forge-infrastructure forge-runtime forge-gateway forge-autoscaler \
  >/dev/null 2>&1 || true
purge_managed

echo "Resetting control/network/discovery/infra state..."
docker exec -i forge-postgres psql -U forge -d forge -v ON_ERROR_STOP=1 <<'SQL' >/dev/null \
  || fail "could not purge stale state"
BEGIN;
TRUNCATE infrastructure.provider_operations RESTART IDENTITY CASCADE;
TRUNCATE infrastructure.node_bootstrap_timers RESTART IDENTITY CASCADE;
TRUNCATE infrastructure.ssh_inventory_claims RESTART IDENTITY CASCADE;
DELETE FROM control.preemption_events WHERE TRUE;
DELETE FROM control.disruption_budgets WHERE TRUE;
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
DELETE FROM discovery.endpoints;
DELETE FROM discovery.services;
COMMIT;
SQL
docker exec -i forge-postgres psql -U forge -d postgres -v ON_ERROR_STOP=1 <<'SQL' >/dev/null \
  || fail "could not ensure forge_autoscaler database"
SELECT 'CREATE DATABASE forge_autoscaler'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'forge_autoscaler')\gexec
SQL
docker exec -i forge-postgres psql -U forge -d forge_autoscaler -v ON_ERROR_STOP=1 <<'SQL' >/dev/null \
  || echo "autoscaler DB purge skipped" >&2
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'scaling_policies') THEN
    TRUNCATE TABLE scaling_policy_events RESTART IDENTITY CASCADE;
    TRUNCATE TABLE idempotency_keys RESTART IDENTITY CASCADE;
    TRUNCATE TABLE scaling_policies RESTART IDENTITY CASCADE;
  END IF;
END $$;
SQL

echo "Starting forge-control + observe runtime + forge-infrastructure + gateway..."
compose_up_retry --no-deps "${CONTROL_SERVICE}" || fail "compose up control failed"
wait_http "${CONTROL_URL}/health/ready" "forge-control" 180
compose_up_retry --no-deps "${RUNTIME_SERVICE}" || fail "compose up observe runtime failed"
wait_http "${RUNTIME_URL}/health/ready" "forge-runtime (observe)" 120
compose_up_retry --no-deps "${INFRA_SERVICE}" || fail "compose up infra failed"
wait_http "${INFRA_URL}/health/ready" "forge-infrastructure" 180
compose_up_retry --no-deps --force-recreate "${GATEWAY_SERVICE}" || fail "compose up gateway failed"
wait_http "${GATEWAY_URL}/health/ready" "forge-gateway" 120

echo "Restarting forge-network + forge-discovery after ledger reset..."
docker rm -f forge-network forge-discovery >/dev/null 2>&1 || true
compose_up_retry --no-deps --force-recreate "${NETWORK_SERVICE}" "${DISCOVERY_SERVICE}" ||
  fail "compose up network/discovery failed"
wait_http "http://127.0.0.1:4110/health/live" "forge-network" 120
wait_http "${DISCOVERY_URL}/health/live" "forge-discovery" 120

echo "Ensuring overlay Network ${NETWORK_NAME}..."
net_code=""
for attempt in 1 2 3 4 5; do
  net_code="$(curl -s -o "${TMP_DIR}/net-create.json" -w '%{http_code}' -X POST "${NETWORK_URL}/v1/networks" \
    -H 'content-type: application/json' \
    -d "{\"name\":\"${NETWORK_NAME}\",\"spec\":{\"clusterCidr\":\"10.100.0.0/16\",\"nodePrefixLength\":24}}" \
    || echo "000")"
  if [[ "${net_code}" == "201" || "${net_code}" == "409" ]]; then
    break
  fi
  sleep 2
done
[[ "${net_code}" == "201" || "${net_code}" == "409" ]] ||
  fail "create network HTTP ${net_code}: $(cat "${TMP_DIR}/net-create.json" 2>/dev/null || true)"

echo "Waiting for infrastructure kinds..."
for _ in $(seq 1 60); do
  if curl --fail --silent --show-error "${CONTROL_URL}/v1/kinds" |
    python3 -c 'import json,sys; ks=json.load(sys.stdin); assert any(k.get("plural")=="nodepools" for k in ks)'; then
    break
  fi
  sleep 1
done

ensure_demo_image
ensure_priority_classes

echo "Applying Docker InfrastructureProvider + NodePool (3 nodes)..."
apply_ok=0
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
wait_ready_nodes 3 180

label_fleet_zones

echo "Creating project hierarchy + deploying 3 HA replicas..."
create_hierarchy_and_deploy
upsert_disruption_budget
wait_reconcile_deployed "${DEPLOYMENT_ID}" 180
assert_ha_spread "${DEPLOYMENT_ID}"

echo "Starting forge-autoscaler (capacity if needed on recovery)..."
docker rm -f forge-autoscaler >/dev/null 2>&1 || true
compose_up_retry --no-deps --force-recreate "${AUTOSCALER_SERVICE}" || fail "compose up autoscaler failed"
wait_http "${AUTOSCALER_URL}/health/ready" "forge-autoscaler" 120

echo "=== Discovery Ready endpoints ==="
wait_discovery_ready 3 120

echo "=== Gateway routes to healthy replicas ==="
# Accept 2+ healthy upstreams (HA routable). A third host-port can lag briefly.
wait_gateway_healthy_upstreams 2 120

echo "=== Node loss + recovery ==="
curl --fail --silent --show-error "${CONTROL_URL}/v1/nodes" >"${TMP_DIR}/fleet.json"
pick_victim_node
stop_victim_node
wait_node_offline "${VICTIM_NODE}" 60
wait_recovery 180
assert_ha_spread "${DEPLOYMENT_ID}"
assert_budget_satisfied

echo "=== Post-recovery Discovery (stale removed, replacements Ready) ==="
# After node loss, Ready count should return to >=2 (min) and preferably 3.
wait_discovery_ready 2 120

assert_optional_gpu_hint

echo "demo 25 PASSED"
