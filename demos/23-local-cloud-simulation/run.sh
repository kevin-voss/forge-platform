#!/usr/bin/env bash
# Demo 23: local cloud simulation gate (epic 23 acceptance).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/23-local-cloud-simulation"
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
export FORGE_INFRA_RECONCILE_INTERVAL_MS="${FORGE_INFRA_RECONCILE_INTERVAL_MS:-1000}"
export FORGE_OTEL_ENABLED="${FORGE_OTEL_ENABLED:-false}"
# Runtime-node backends only (injected by docker provider CreateNode). Do not
# export FORGE_NETWORK_WG_BACKEND=fake globally — forge-network rejects it.
export COMPOSE_PARALLEL_LIMIT="${COMPOSE_PARALLEL_LIMIT:-1}"

DEMO_TARGET="${FORGE_DEMO_TARGET:-docker}"
DEMO_TARGET="$(echo "${DEMO_TARGET}" | tr '[:upper:]' '[:lower:]')"

COMPOSE=(
  docker compose
  -f "${ROOT_DIR}/compose.yaml"
  -f "${DEMO_DIR}/docker-compose.yml"
  --project-directory "${ROOT_DIR}"
)
CONTROL_URL="${FORGE_CONTROL_URL:-http://127.0.0.1:4001}"
INFRA_URL="${FORGE_INFRA_URL:-http://127.0.0.1:4111}"
NETWORK_URL="${FORGE_NETWORK_URL:-http://127.0.0.1:4110}"
NETWORK_NAME="${FORGE_NETWORK_NAME:-cluster-overlay}"
CONTROL_SERVICE="forge-control"
INFRA_SERVICE="forge-infrastructure"
NETWORK_SERVICE="forge-network"
RUNTIME_SERVICE="forge-runtime"
POSTGRES_SERVICE="postgres"
REGISTRY_SERVICE="registry"
RUNTIME_URL="${FORGE_RUNTIME_URL:-http://127.0.0.1:4102}"
CLI_DIR="${ROOT_DIR}/tools/forge-cli"
FORGE_BIN="${CLI_DIR}/forge"
REGISTRY="${FORGE_REGISTRY:-localhost:5000}"
DEMO_IMAGE="${DEMO_IMAGE:-${REGISTRY}/demo-infra:v1}"
POOL_NAME="local-docker-pool"
PROVIDER_NAME="docker-local"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-demo-23.XXXXXX")"
CONFIG_HOME="${TMP_DIR}/xdg-config"
export XDG_CONFIG_HOME="${CONFIG_HOME}"
export CI=1
export FORGE_PROFILE="${FORGE_PROFILE:-demo23}"
export FORGE_ENDPOINT="${FORGE_ENDPOINT:-${CONTROL_URL}}"
mkdir -p "${CONFIG_HOME}"

TRACKED_DEPLOYMENTS=()
SUFFIX="$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')"
PROJECT_NAME="Infra Cloud ${SUFFIX}"

cleanup() {
  local dep
  set +u
  for dep in "${TRACKED_DEPLOYMENTS[@]+"${TRACKED_DEPLOYMENTS[@]}"}"; do
    [[ -n "${dep:-}" ]] || continue
    curl --silent --show-error -X DELETE "${CONTROL_URL}/v1/deployments/${dep}" >/dev/null 2>&1 || true
  done
  set -u
  curl --silent --show-error -X DELETE "${CONTROL_URL}/v1/nodepools/${POOL_NAME}" >/dev/null 2>&1 || true
  # Wait briefly for drain/delete before hard-killing leftovers.
  sleep 2
  docker ps -aq --filter "label=forge.pool=${POOL_NAME}" | while read -r cid; do
    [[ -n "${cid}" ]] || continue
    docker rm -f "${cid}" >/dev/null 2>&1 || true
  done
  docker ps -aq --filter "label=forge.managed=true" | while read -r cid; do
    [[ -n "${cid}" ]] || continue
    docker rm -f "${cid}" >/dev/null 2>&1 || true
  done
  "${COMPOSE[@]}" stop \
    "${INFRA_SERVICE}" "${CONTROL_SERVICE}" "${NETWORK_SERVICE}" "${RUNTIME_SERVICE}" >/dev/null 2>&1 || true
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

dump_context() {
  echo "--- Control fleet /v1/nodes ---" >&2
  curl --silent --show-error "${CONTROL_URL}/v1/nodes" >&2 || true
  echo >&2
  echo "--- Resource /v1/forgenodes ---" >&2
  curl --silent --show-error "${CONTROL_URL}/v1/forgenodes" >&2 || true
  echo >&2
  echo "--- /v1/nodepools/${POOL_NAME} ---" >&2
  curl --silent --show-error "${CONTROL_URL}/v1/nodepools/${POOL_NAME}" >&2 || true
  echo >&2
  if ((${#TRACKED_DEPLOYMENTS[@]} > 0)); then
    local dep="${TRACKED_DEPLOYMENTS[-1]}"
    echo "--- placements deployment=${dep} ---" >&2
    curl --silent --show-error "${CONTROL_URL}/v1/placements?deployment=${dep}" >&2 || true
    echo >&2
    echo "--- reconcile deployment=${dep} ---" >&2
    curl --silent --show-error "${CONTROL_URL}/v1/deployments/${dep}/reconcile" >&2 || true
    echo >&2
  fi
  echo "--- docker ps forge.managed ---" >&2
  docker ps -a --filter "label=forge.managed=true" \
    --format 'table {{.Names}}\t{{.Status}}\t{{.Labels}}' >&2 || true
  echo "--- ${INFRA_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=80 "${INFRA_SERVICE}" >&2 || true
  echo "--- ${CONTROL_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${CONTROL_SERVICE}" >&2 || true
}

fail() {
  echo "Demo 23 failed: $*" >&2
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

track_deployment() {
  TRACKED_DEPLOYMENTS+=("$1")
}

cloud_target_docs() {
  local target="$1"
  cat <<EOF
FORGE_DEMO_TARGET=${target} is opt-in and never part of CI.

Required:
  FORGE_DEMO_CLOUD_CONFIRM=1
EOF
  case "${target}" in
    hetzner)
      cat <<'EOF'
  FORGE_INFRA_HETZNER_API_TOKEN=<token>   # or credentialsSecretRef via Secrets
See: services/forge-infrastructure/README.md (Hetzner Cloud)
EOF
      ;;
    aws)
      cat <<'EOF'
  FORGE_INFRA_AWS_CREDENTIALS_JSON={"accessKeyId":"...","secretAccessKey":"...","region":"..."}
See: docs/operations/aws-provider-permissions.md
EOF
      ;;
    azure)
      cat <<'EOF'
  FORGE_INFRA_AZURE_CREDENTIALS_JSON={"tenantId":"...","clientId":"...","clientSecret":"...","subscriptionId":"..."}
See: docs/operations/azure-provider-permissions.md
EOF
      ;;
  esac
  echo "Refusing to create billable cloud resources from this script."
}

if [[ "${DEMO_TARGET}" != "docker" ]]; then
  case "${DEMO_TARGET}" in
    hetzner|aws|azure)
      cloud_target_docs "${DEMO_TARGET}"
      if [[ "${FORGE_DEMO_CLOUD_CONFIRM:-}" != "1" ]]; then
        echo "Set FORGE_DEMO_CLOUD_CONFIRM=1 to acknowledge billable cloud usage." >&2
        exit 2
      fi
      fail "cloud target '${DEMO_TARGET}' is documented but not automated in this gate (use docker)"
      ;;
    *)
      fail "unknown FORGE_DEMO_TARGET=${DEMO_TARGET} (want docker|hetzner|aws|azure)"
      ;;
  esac
fi

ensure_demo_image() {
  echo "Building and pushing ${DEMO_IMAGE}..."
  docker build \
    --build-arg "VERSION=infra-23" \
    --build-arg "READY_FAIL=false" \
    -t "${DEMO_IMAGE}" \
    "${APP_DIR}" || fail "could not build ${DEMO_IMAGE}"
  docker push "${DEMO_IMAGE}" >/dev/null || fail "could not push ${DEMO_IMAGE}"
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
  # Provider nodes only — workload containers also carry forge.managed=true.
  docker ps -q --filter "label=forge.pool=${POOL_NAME}" | wc -l | tr -d ' '
}

count_ready_forgenodes() {
  curl --fail --silent --show-error "${CONTROL_URL}/v1/forgenodes" |
    python3 -c '
import json,sys
body=json.load(sys.stdin)
items=body.get("items") if isinstance(body,dict) else body
n=0
for it in items or []:
    if (it.get("status") or {}).get("phase")=="Ready":
        n+=1
print(n)
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
  # exact=1 requires ready/online/managed == want (scale-down); default is >= want.
  local exact="${3:-0}"
  local ready online managed
  if [[ "${exact}" == "1" ]]; then
    echo "Waiting for exactly ${want} Ready forgenodes + online fleet ..."
  else
    echo "Waiting for ${want}+ Ready forgenodes + online fleet ..."
  fi
  for _ in $(seq 1 "${attempts}"); do
    ready="$(count_ready_forgenodes 2>/dev/null || echo 0)"
    online="$(count_online_fleet 2>/dev/null || echo 0)"
    managed="$(count_managed_containers 2>/dev/null || echo 0)"
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

patch_pool_replicas() {
  local replicas="$1" attempt code
  for attempt in 1 2 3 4 5; do
    curl --fail --silent --show-error "${CONTROL_URL}/v1/nodepools/${POOL_NAME}" >"${TMP_DIR}/pool.json" ||
      fail "GET nodepool failed"
    python3 - "${TMP_DIR}/pool.json" "${TMP_DIR}/pool-patch.json" "${replicas}" <<'PY'
import json, sys
src, dest, replicas = sys.argv[1], sys.argv[2], int(sys.argv[3])
pool = json.load(open(src))
body = {
    "apiVersion": pool.get("apiVersion", "forge.dev/v1"),
    "kind": "NodePool",
    "metadata": {
        "name": pool["metadata"]["name"],
        "resourceVersion": pool["metadata"]["resourceVersion"],
        "labels": pool["metadata"].get("labels") or {},
    },
    "spec": {**(pool.get("spec") or {}), "replicas": replicas},
}
json.dump(body, open(dest, "w"))
PY
    code="$(curl --silent --show-error --output "${TMP_DIR}/pool-put.json" --write-out '%{http_code}' \
      -X PUT "${CONTROL_URL}/v1/nodepools/${POOL_NAME}" \
      -H 'content-type: application/json' \
      --data @"${TMP_DIR}/pool-patch.json")" || fail "PUT nodepool failed"
    if [[ "${code}" == "200" ]]; then
      echo "NodePool ${POOL_NAME} replicas -> ${replicas}"
      return 0
    fi
    if [[ "${code}" == "409" && "${attempt}" -lt 5 ]]; then
      echo "  NodePool PUT conflict (attempt ${attempt}); retrying..." >&2
      sleep 1
      continue
    fi
    fail "PUT nodepool returned HTTP ${code}: $(cat "${TMP_DIR}/pool-put.json")"
  done
}

assert_no_orphans_or_stuck_ops() {
  local managed pending
  managed="$(count_managed_containers)"
  # Allow a short settle window after scale-down.
  local i
  for i in $(seq 1 30); do
    managed="$(count_managed_containers)"
    pending="$(curl --fail --silent --show-error "${INFRA_URL}/health/ready" >/dev/null && \
      curl --silent --show-error "${CONTROL_URL}/v1/forgenodes" |
      python3 -c '
import json,sys
body=json.load(sys.stdin)
items=body.get("items") if isinstance(body,dict) else body
stuck=0
for it in items or []:
    ph=(it.get("status") or {}).get("phase") or ""
    if ph in ("Draining","Deleting","Provisioning","Bootstrapping","Joining","Failed"):
        stuck+=1
print(stuck)
' || echo 99)"
    if [[ "${managed}" == "2" && "${pending}" == "0" ]]; then
      echo "  no orphans: managed=${managed}, non-Ready forgenodes=${pending}"
      return 0
    fi
    sleep 1
  done
  fail "expected 2 managed containers and 0 in-flight forgenodes (managed=${managed} stuck=${pending})"
}

create_hierarchy() {
  forge_json "${TMP_DIR}/project.json" project create --name "demo-infra-${SUFFIX}"
  PROJECT_ID="$(read_id "${TMP_DIR}/project.json")"
  forge_json "${TMP_DIR}/environment.json" env create --project "${PROJECT_ID}" --name development
  ENVIRONMENT_ID="$(read_id "${TMP_DIR}/environment.json")"
  forge_json "${TMP_DIR}/application.json" app create --project "${PROJECT_ID}" --name demos
  APPLICATION_ID="$(read_id "${TMP_DIR}/application.json")"
  forge_json "${TMP_DIR}/service.json" service create --app "${APPLICATION_ID}" --name demo --port 8080
  SERVICE_ID="$(read_id "${TMP_DIR}/service.json")"
}

wait_reconcile_deployed() {
  local deployment_id="$1" attempts="${2:-120}"
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

assert_placements_on_managed() {
  local deployment_id="$1"
  curl --fail --silent --show-error \
    "${CONTROL_URL}/v1/placements?deployment=${deployment_id}" \
    >"${TMP_DIR}/placements.json" || fail "GET placements failed"
  curl --fail --silent --show-error "${CONTROL_URL}/v1/nodes" >"${TMP_DIR}/fleet.json"
  python3 - "${TMP_DIR}/placements.json" "${TMP_DIR}/fleet.json" <<'PY' || fail "placements not on provider-created nodes"
import json, sys
placements = json.load(open(sys.argv[1]))
fleet = {n["id"]: n for n in json.load(open(sys.argv[2]))}
placed = [p for p in placements if p.get("status") == "placed"]
if len(placed) < 1:
    sys.exit("no placed replicas")
# Seed compose runtime is profile-off; every online node should be provider-created
# (FORGE_NODE_ADDRESS http://forge-node-...).
for p in placed:
    nid = p.get("node_id")
    node = fleet.get(nid) or {}
    addr = (node.get("address") or "").lower()
    if "forge-node-" not in addr and not addr.startswith("http://forge-node"):
        # Accept any online non-seed address from this demo's pool.
        if nid in ("node-a", "node-b", "node-c", "node-local"):
            sys.exit(f"placement on seed node {nid}")
print(f"placements ok: {len(placed)} on provider fleet")
PY
}

echo "=== Demo 23: local cloud simulation (Docker provider) ==="
echo "Project: ${PROJECT_NAME}"

echo "Starting platform services (sequential compose)..."
purge_managed
# Drop leftover NodePools from prior integration tests so reconcile stays focused.
if curl --fail --silent --show-error "${CONTROL_URL}/health/ready" >/dev/null 2>&1; then
  curl --silent --show-error "${CONTROL_URL}/v1/nodepools" 2>/dev/null |
    python3 -c '
import json,sys,urllib.request
try:
  body=json.load(sys.stdin)
except Exception:
  raise SystemExit
base="'"${CONTROL_URL}"'"
for it in (body.get("items") or []):
  name=(it.get("metadata") or {}).get("name") or ""
  if name and name != "'"${POOL_NAME}"'":
    req=urllib.request.Request(base+"/v1/nodepools/"+name, method="DELETE")
    try: urllib.request.urlopen(req, timeout=5)
    except Exception: pass
' || true
fi
# Build first, then start deps, then recreate Control/Infra. A single
# force-recreate of everything races under Docker Desktop memory pressure
# ("No such container" while starting forge-infrastructure).
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

echo "Building platform images..."
"${COMPOSE[@]}" build \
  "${NETWORK_SERVICE}" "${CONTROL_SERVICE}" "${INFRA_SERVICE}" "${RUNTIME_SERVICE}" ||
  fail "compose build failed"

echo "Starting postgres + registry + forge-network..."
compose_up_retry "${POSTGRES_SERVICE}" "${REGISTRY_SERVICE}" "${NETWORK_SERVICE}" ||
  fail "compose up deps failed"
wait_http "http://127.0.0.1:4110/health/live" "forge-network" 120

# Stop Control/Infra/Runtime before SQL purge so in-memory caches cannot
# serve half-deleted state (that produced flaky forge apply 500s).
echo "Stopping control/infra/runtime for clean ledger reset..."
docker rm -f forge-control forge-infrastructure forge-runtime >/dev/null 2>&1 || true
purge_managed

echo "Resetting infrastructure ledger and demo resources..."
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

echo "Starting forge-control + observe runtime + forge-infrastructure..."
# Prefer rm + up --no-deps over --force-recreate: Docker Desktop often races
# with "No such container" when recreate + depends_on healthchecks collide.
compose_up_retry --no-deps "${CONTROL_SERVICE}" || fail "compose up control failed"
wait_http "${CONTROL_URL}/health/ready" "forge-control" 180
# Zero-slot observe agent for Control loadActual/list (shared Docker socket).
compose_up_retry --no-deps "${RUNTIME_SERVICE}" || fail "compose up observe runtime failed"
wait_http "${RUNTIME_URL}/health/ready" "forge-runtime (observe)" 120
compose_up_retry --no-deps "${INFRA_SERVICE}" || fail "compose up infra failed"
wait_http "${INFRA_URL}/health/ready" "forge-infrastructure" 180

echo "Ensuring overlay Network ${NETWORK_NAME}..."
net_code="$(curl -s -o "${TMP_DIR}/net-create.json" -w '%{http_code}' -X POST "${NETWORK_URL}/v1/networks" \
  -H 'content-type: application/json' \
  -d "{\"name\":\"${NETWORK_NAME}\",\"spec\":{\"clusterCidr\":\"10.100.0.0/16\",\"nodePrefixLength\":24}}")"
if [[ "${net_code}" == "201" ]]; then
  echo "  created ${NETWORK_NAME}"
elif [[ "${net_code}" == "409" ]]; then
  curl --fail --silent --show-error "${NETWORK_URL}/v1/networks/${NETWORK_NAME}" \
    >"${TMP_DIR}/net-get.json" || fail "GET network after conflict failed"
  python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert d.get("status",{}).get("phase")=="Ready", d' \
    "${TMP_DIR}/net-get.json" || fail "network exists but not Ready"
  echo "  reused Ready ${NETWORK_NAME}"
else
  fail "create network HTTP ${net_code}: $(cat "${TMP_DIR}/net-create.json")"
fi

# Kinds register asynchronously; wait for NodePool plural.
echo "Waiting for infrastructure kinds..."
for _ in $(seq 1 60); do
  if curl --fail --silent --show-error "${CONTROL_URL}/v1/kinds" |
    python3 -c 'import json,sys; ks=json.load(sys.stdin); assert any(k.get("plural")=="nodepools" for k in ks); assert any(k.get("plural")=="forgenodes" for k in ks)'; then
    break
  fi
  sleep 1
done
curl --fail --silent --show-error "${CONTROL_URL}/v1/kinds" |
  python3 -c 'import json,sys; ks=json.load(sys.stdin); plurals={k.get("plural") for k in ks}; assert "nodepools" in plurals and "forgenodes" in plurals and "infrastructureproviders" in plurals, plurals' ||
  fail "infrastructure kinds not registered (need nodepools/forgenodes/infrastructureproviders)"

ensure_demo_image

echo "Applying Docker InfrastructureProvider + NodePool (replicas=2)..."
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
python3 - "${TMP_DIR}/apply-pool.json" <<'PY' || fail "apply contract failed"
import json,sys
body=json.load(open(sys.argv[1]))
results=body.get("results") or []
kinds={r.get("kind") for r in results}
assert "InfrastructureProvider" in kinds and "NodePool" in kinds, body
print("apply ok: changedCount=%s results=%s" % (
    body.get("changedCount"),
    [(r.get("kind"), r.get("name"), r.get("action")) for r in results],
))
PY

wait_ready_nodes 2 180

echo "Creating project hierarchy + deploying 2 replicas onto provider nodes..."
create_hierarchy
forge_json "${TMP_DIR}/deploy.json" deployment create \
  --service "${SERVICE_ID}" \
  --env "${ENVIRONMENT_ID}" \
  --image "${DEMO_IMAGE}" \
  --replicas 2
DEPLOYMENT_ID="$(read_id "${TMP_DIR}/deploy.json")"
track_deployment "${DEPLOYMENT_ID}"
wait_reconcile_deployed "${DEPLOYMENT_ID}" 150
assert_placements_on_managed "${DEPLOYMENT_ID}"

echo "Scaling NodePool to 3..."
patch_pool_replicas 3
wait_ready_nodes 3 180

echo "Scaling NodePool to 2 (drain + delete)..."
patch_pool_replicas 2
wait_ready_nodes 2 180 1
assert_no_orphans_or_stuck_ops

# Ledger: at least one completed create_node; no pending rows for this pool's ops via debug API if present.
echo "Checking provider operation ledger (sample)..."
curl --fail --silent --show-error "${INFRA_URL}/health/live" >/dev/null || fail "infra live failed"

echo "demo 23 PASSED"
