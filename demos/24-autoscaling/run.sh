#!/usr/bin/env bash
# Demo 24: autoscaling gate (epic 24 acceptance).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/24-autoscaling"
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
export FORGE_AUTOSCALER_EVAL_INTERVAL_MS="${FORGE_AUTOSCALER_EVAL_INTERVAL_MS:-1000}"
export FORGE_AUTOSCALER_NODE_SCALE_UP_COOLDOWN_SECONDS="${FORGE_AUTOSCALER_NODE_SCALE_UP_COOLDOWN_SECONDS:-0}"
export FORGE_AUTOSCALER_NODE_SCALE_DOWN_COOLDOWN_SECONDS="${FORGE_AUTOSCALER_NODE_SCALE_DOWN_COOLDOWN_SECONDS:-0}"
# Keep long enough that a just-provisioned node is not drained during Phase 2
# capacity absorb (1s window caused drain_in_progress races with scale-up).
export FORGE_AUTOSCALER_NODE_UNDERUTILIZATION_WINDOW_SECONDS="${FORGE_AUTOSCALER_NODE_UNDERUTILIZATION_WINDOW_SECONDS:-45}"
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
METRICS_URL="${FORGE_DEMO24_METRICS_URL:-http://127.0.0.1:4199}"
NETWORK_NAME="${FORGE_NETWORK_NAME:-cluster-overlay}"
CONTROL_SERVICE="forge-control"
INFRA_SERVICE="forge-infrastructure"
NETWORK_SERVICE="forge-network"
RUNTIME_SERVICE="forge-runtime"
AUTOSCALER_SERVICE="forge-autoscaler"
METRICS_SERVICE="demo24-metrics"
POSTGRES_SERVICE="postgres"
REGISTRY_SERVICE="registry"
RUNTIME_URL="${FORGE_RUNTIME_URL:-http://127.0.0.1:4102}"
CLI_DIR="${ROOT_DIR}/tools/forge-cli"
FORGE_BIN="${CLI_DIR}/forge"
REGISTRY="${FORGE_REGISTRY:-localhost:5000}"
DEMO_IMAGE="${DEMO_IMAGE:-${REGISTRY}/demo-autoscaling:v1}"
POOL_NAME="docker-pool"
ENV_NAME="production"
# APP_NAME / WORKER_NAME / policies / QUEUE_NAME set after SUFFIX below.

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-demo-24.XXXXXX")"
CONFIG_HOME="${TMP_DIR}/xdg-config"
export XDG_CONFIG_HOME="${CONFIG_HOME}"
export CI=1
export FORGE_PROFILE="${FORGE_PROFILE:-demo24}"
export FORGE_ENDPOINT="${FORGE_ENDPOINT:-${CONTROL_URL}}"
mkdir -p "${CONFIG_HOME}"

SUFFIX="$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')"
PROJECT_NAME="Invoice Autoscaling ${SUFFIX}"
PROJECT_SLUG="invoice-autoscaling-${SUFFIX}"
# Unique per-run names so concurrent demos cannot clobber shared metrics keys
# or inflate NodePool reservation via leftover deployments.
APP_NAME="invoice-api-${SUFFIX}"
WORKER_NAME="invoice-worker-${SUFFIX}"
API_POLICY="invoice-api-scaling-${SUFFIX}"
WORKER_POLICY="invoice-worker-scaling-${SUFFIX}"
QUEUE_NAME="invoice-jobs-${SUFFIX}"
SYNC_PID=""
DEPLOYMENT_ID=""
# Fixed path (not $TMPDIR) so concurrent shells share one lock on macOS.
LOCK_DIR="/tmp/forge-demo-24.lock"
HOLD_LOCK=0

cleanup() {
  if [[ -n "${SYNC_PID}" ]]; then
    kill "${SYNC_PID}" >/dev/null 2>&1 || true
    wait "${SYNC_PID}" 2>/dev/null || true
    SYNC_PID=""
  fi
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
      "${AUTOSCALER_SERVICE}" "${METRICS_SERVICE}" \
      "${INFRA_SERVICE}" "${CONTROL_SERVICE}" "${NETWORK_SERVICE}" "${RUNTIME_SERVICE}" \
      >/dev/null 2>&1 || true
  fi
  rm -rf "${TMP_DIR}"
  if [[ "${HOLD_LOCK}" -eq 1 ]]; then
    rmdir "${LOCK_DIR}" 2>/dev/null || true
  fi
}

# Exclusive gate — metrics sidecar + docker-pool are shared cluster resources.
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
      echo "Removing stale demo 24 lock (pid ${stale_pid:-empty})"
      rm -rf "${LOCK_DIR}"
      continue
    fi
    echo "Demo 24 failed: another demo 24 holds ${LOCK_DIR} (pid ${stale_pid})" >&2
    return 1
  done
  echo "Demo 24 failed: could not acquire ${LOCK_DIR}" >&2
  return 1
}
if ! acquire_demo_lock; then
  rm -rf "${TMP_DIR}"
  exit 1
fi
trap cleanup EXIT

dump_context() {
  echo "--- ScalingPolicy ${API_POLICY} ---" >&2
  curl --silent --show-error \
    "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies/${API_POLICY}" >&2 || true
  echo >&2
  echo "--- ScalingPolicy ${WORKER_POLICY} ---" >&2
  curl --silent --show-error \
    "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies/${WORKER_POLICY}" >&2 || true
  echo >&2
  echo "--- NodePool ${POOL_NAME} ---" >&2
  curl --silent --show-error "${CONTROL_URL}/v1/nodepools/${POOL_NAME}" >&2 || true
  echo >&2
  echo "--- /v1/nodes ---" >&2
  curl --silent --show-error "${CONTROL_URL}/v1/nodes" >&2 || true
  echo >&2
  if [[ -n "${DEPLOYMENT_ID}" ]]; then
    echo "--- placements deployment=${DEPLOYMENT_ID} ---" >&2
    curl --silent --show-error "${CONTROL_URL}/v1/placements?deployment=${DEPLOYMENT_ID}" >&2 || true
    echo >&2
    echo "--- reconcile ---" >&2
    curl --silent --show-error "${CONTROL_URL}/v1/deployments/${DEPLOYMENT_ID}/reconcile" >&2 || true
    echo >&2
  fi
  echo "--- demo24-metrics application ---" >&2
  curl --silent --show-error "${METRICS_URL}/admin/metrics?application=${APP_NAME}" >&2 || true
  echo >&2
  echo "--- ${AUTOSCALER_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=80 "${AUTOSCALER_SERVICE}" >&2 || true
  echo "--- ${INFRA_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${INFRA_SERVICE}" >&2 || true
  echo "--- ${CONTROL_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${CONTROL_SERVICE}" >&2 || true
}

fail() {
  echo "Demo 24 failed: $*" >&2
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

ensure_demo_image() {
  echo "Building and pushing ${DEMO_IMAGE}..."
  docker build \
    --build-arg "VERSION=autoscaling-24" \
    --build-arg "READY_FAIL=false" \
    -t "${DEMO_IMAGE}" \
    "${APP_DIR}" || fail "could not build ${DEMO_IMAGE}"
  docker push "${DEMO_IMAGE}" >/dev/null || fail "could not push ${DEMO_IMAGE}"
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

set_http_load() {
  local rps="$1" got=""
  curl --fail --silent --show-error -X PUT "${METRICS_URL}/demo/application/${APP_NAME}" \
    -H 'content-type: application/json' \
    -d "{\"requestsPerSecond\":${rps},\"activeConnections\":${rps},\"sampleCount\":2000}" \
    >/dev/null || fail "set HTTP load failed"
  got="$(curl --fail --silent --show-error "${METRICS_URL}/admin/metrics?application=${APP_NAME}" |
    python3 -c 'import json,sys; print(int(float(json.load(sys.stdin).get("requestsPerSecond") or 0)))')"
  [[ "${got}" -eq "${rps}" ]] || fail "metrics sidecar rps=${got}, want ${rps}"
  echo "  traffic generator: application=${APP_NAME} rps=${rps}"
}

clear_http_load() {
  curl --fail --silent --show-error -X DELETE "${METRICS_URL}/demo/application/${APP_NAME}" >/dev/null || true
  echo "  traffic generator: cleared application=${APP_NAME}"
}

publish_queue() {
  local depth="$1"
  curl --fail --silent --show-error -X PUT "${METRICS_URL}/demo/queue/${QUEUE_NAME}" \
    -H 'content-type: application/json' \
    -d "{\"depth\":${depth},\"oldestAgeSeconds\":30,\"consumerLag\":${depth},\"retryRate\":0}" \
    >/dev/null || fail "publish queue failed"
  echo "  queue publisher: queue=${QUEUE_NAME} depth=${depth}"
}

clear_queue() {
  curl --fail --silent --show-error -X DELETE "${METRICS_URL}/demo/queue/${QUEUE_NAME}" >/dev/null || true
  echo "  queue publisher: cleared queue=${QUEUE_NAME}"
}

policy_desired() {
  local name="$1"
  curl --fail --silent --show-error \
    "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies/${name}" |
    python3 -c 'import json,sys; print(int(json.load(sys.stdin).get("status",{}).get("desiredReplicas") or 0))'
}

policy_field() {
  local name="$1" expr="$2"
  curl --fail --silent --show-error \
    "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies/${name}" |
    python3 -c "import json,sys; d=json.load(sys.stdin); print(${expr})"
}

wait_policy_desired_ge() {
  local name="$1" min="$2" attempts="${3:-90}"
  local cur=0
  echo "Waiting for ScalingPolicy ${name} desiredReplicas >= ${min} ..."
  for _ in $(seq 1 "${attempts}"); do
    if ! curl --fail --silent --show-error \
      "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies/${name}" \
      >"${TMP_DIR}/pol-wait.json" 2>/dev/null; then
      cur=0
      sleep 1
      continue
    fi
    cur="$(python3 -c 'import json,sys; print(int(json.load(open(sys.argv[1])).get("status",{}).get("desiredReplicas") or 0))' "${TMP_DIR}/pol-wait.json")"
    if [[ "${cur}" -ge "${min}" ]]; then
      echo "  ${name} desiredReplicas=${cur}"
      return 0
    fi
    sleep 1
  done
  fail "timed out waiting for ${name} desiredReplicas >= ${min} (got ${cur})"
}

wait_policy_desired_eq() {
  local name="$1" want="$2" attempts="${3:-90}"
  local cur=0
  echo "Waiting for ScalingPolicy ${name} desiredReplicas == ${want} ..."
  for _ in $(seq 1 "${attempts}"); do
    cur="$(policy_desired "${name}" 2>/dev/null || echo 0)"
    if [[ "${cur}" -eq "${want}" ]]; then
      echo "  ${name} desiredReplicas=${cur}"
      return 0
    fi
    sleep 1
  done
  fail "timed out waiting for ${name} desiredReplicas == ${want} (got ${cur})"
}

wait_policy_desired_le() {
  local name="$1" max="$2" attempts="${3:-90}"
  local cur=999
  echo "Waiting for ScalingPolicy ${name} desiredReplicas <= ${max} ..."
  for _ in $(seq 1 "${attempts}"); do
    cur="$(policy_desired "${name}" 2>/dev/null || echo 999)"
    if [[ "${cur}" -le "${max}" ]]; then
      echo "  ${name} desiredReplicas=${cur}"
      return 0
    fi
    sleep 1
  done
  fail "timed out waiting for ${name} desiredReplicas <= ${max} (got ${cur})"
}

sync_application_to_deployment() {
  # Bridge Application.spec.scaling.desiredReplicas → Deployment desiredReplicas
  # (autoscaler actuates Application; reconciler reads Deployment).
  [[ -n "${DEPLOYMENT_ID}" ]] || fail "DEPLOYMENT_ID required before sync loop"
  python3 - "${CONTROL_URL}" "${PROJECT_SLUG}" "${ENV_NAME}" "${APP_NAME}" "${DEPLOYMENT_ID}" <<'PY' &
import json, time, urllib.request, sys
base, project, env, app, dep_id = sys.argv[1:6]
app_url = f"{base}/v1/projects/{project}/environments/{env}/applications/{app}"
dep_url = f"{base}/v1/deployments/{dep_id}"

def get(url):
    with urllib.request.urlopen(url, timeout=5) as r:
        return json.load(r)

def patch(url, body):
    data = json.dumps(body).encode()
    req = urllib.request.Request(url, data=data, method="PATCH",
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
                print(f"sync: deployment {dep_id} desiredReplicas {cur} -> {desired}", flush=True)
            last = desired
    except Exception as exc:
        print(f"sync: {exc}", flush=True)
    time.sleep(1)
PY
  SYNC_PID=$!
}

wait_reconcile_deployed() {
  local deployment_id="$1" attempts="${2:-180}"
  local status=""
  echo "Waiting for deployment ${deployment_id} reconcile status=deployed ..."
  for _ in $(seq 1 "${attempts}"); do
    if ! curl --fail --silent --show-error "${CONTROL_URL}/health/ready" >/dev/null 2>&1; then
      echo "  control not ready; waiting..." >&2
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

ensure_application_resource() {
  # Autoscaler patches Application.spec.scaling.desiredReplicas; ensure the
  # environment-scoped Application envelope exists alongside the CLI hierarchy.
  local code
  code="$(curl -s -o "${TMP_DIR}/app-res.json" -w '%{http_code}' -X POST \
    "${CONTROL_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/applications" \
    -H 'content-type: application/json' \
    -d "{\"metadata\":{\"name\":\"${APP_NAME}\"},\"spec\":{\"image\":\"${DEMO_IMAGE}\",\"scaling\":{\"desiredReplicas\":2,\"minReplicas\":2,\"maxReplicas\":6}}}")"
  if [[ "${code}" == "201" || "${code}" == "200" ]]; then
    echo "  Application resource ${APP_NAME} created"
    return 0
  fi
  if [[ "${code}" == "409" ]]; then
    echo "  Application resource ${APP_NAME} already exists"
    return 0
  fi
  # Companion may already exist from CLI app create — PATCH desiredReplicas.
  code="$(curl -s -o "${TMP_DIR}/app-patch.json" -w '%{http_code}' -X PATCH \
    "${CONTROL_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/applications/${APP_NAME}" \
    -H 'content-type: application/json' \
    -d "{\"spec\":{\"scaling\":{\"desiredReplicas\":2,\"minReplicas\":2,\"maxReplicas\":6}}}")"
  if [[ "${code}" == "200" ]]; then
    echo "  Application resource ${APP_NAME} patched"
    return 0
  fi
  fail "ensure Application resource HTTP ${code}: $(cat "${TMP_DIR}/app-res.json" "${TMP_DIR}/app-patch.json" 2>/dev/null || true)"
}

create_hierarchy_and_deploy() {
  forge_json "${TMP_DIR}/project.json" project create \
    --name "demo-autoscaling-${SUFFIX}" \
    --slug "${PROJECT_SLUG}"
  PROJECT_ID="$(read_id "${TMP_DIR}/project.json")"
  forge_json "${TMP_DIR}/environment.json" env create --project "${PROJECT_ID}" --name "${ENV_NAME}"
  ENVIRONMENT_ID="$(read_id "${TMP_DIR}/environment.json")"
  forge_json "${TMP_DIR}/application.json" app create --project "${PROJECT_ID}" --name "${APP_NAME}"
  APPLICATION_ID="$(read_id "${TMP_DIR}/application.json")"
  forge_json "${TMP_DIR}/service.json" service create --app "${APPLICATION_ID}" --name "${APP_NAME}" --port 8080
  SERVICE_ID="$(read_id "${TMP_DIR}/service.json")"
  ensure_application_resource
  forge_json "${TMP_DIR}/deploy.json" deployment create \
    --service "${SERVICE_ID}" \
    --env "${ENVIRONMENT_ID}" \
    --image "${DEMO_IMAGE}" \
    --replicas 2
  DEPLOYMENT_ID="$(read_id "${TMP_DIR}/deploy.json")"
  echo "  project=${PROJECT_SLUG} deployment=${DEPLOYMENT_ID}"
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

wait_pending_gt() {
  local min="$1" attempts="${2:-90}"
  local pending=0
  echo "Waiting for pending placements > ${min} ..."
  for _ in $(seq 1 "${attempts}"); do
    pending="$(curl --fail --silent --show-error "${CONTROL_URL}/v1/placements?status=pending" |
      python3 -c 'import json,sys; print(len(json.load(sys.stdin)))' 2>/dev/null || echo 0)"
    if [[ "${pending}" -gt "${min}" ]]; then
      echo "  pending=${pending}"
      return 0
    fi
    sleep 1
  done
  fail "timed out waiting for pending > ${min} (got ${pending})"
}

resolve_deployment_id() {
  if [[ -f "${TMP_DIR}/apply-app.json" ]]; then
    if python3 - "${TMP_DIR}/apply-app.json" <<'PY'
import json,sys
body=json.load(open(sys.argv[1]))
for r in body.get("results") or []:
    if r.get("kind") != "Deployment":
        continue
    if r.get("id"):
        print(r["id"]); raise SystemExit
    res = r.get("resource") or {}
    meta = res.get("metadata") or {}
    rid = meta.get("id") or meta.get("uid") or res.get("id")
    if rid:
        print(rid); raise SystemExit
raise SystemExit(1)
PY
    then
      return 0
    fi
  fi
  curl --fail --silent --show-error "${CONTROL_URL}/v1/deployments" |
    python3 -c '
import json,sys
body=json.load(sys.stdin)
items=body if isinstance(body,list) else (body.get("items") or body.get("deployments") or [])
for d in items:
    if "demo-autoscaling" in str(d.get("image","")):
        print(d["id"]); raise SystemExit
raise SystemExit("deployment not found")
' || fail "could not resolve deployment id"
}

ensure_worker_kind() {
  local code
  code="$(curl -s -o "${TMP_DIR}/kind.json" -w '%{http_code}' -X POST "${CONTROL_URL}/v1/kinds" \
    -H 'content-type: application/json' \
    -d '{"kind":"Worker","plural":"workers","scope":"environment","controller":"worker-controller","idPrefix":"wrk"}')"
  if [[ "${code}" == "201" || "${code}" == "200" || "${code}" == "409" ]]; then
    echo "  Worker kind ready (HTTP ${code})"
    return 0
  fi
  fail "register Worker kind HTTP ${code}: $(cat "${TMP_DIR}/kind.json")"
}

create_worker() {
  local code
  code="$(curl -s -o "${TMP_DIR}/worker.json" -w '%{http_code}' -X POST \
    "${CONTROL_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/workers" \
    -H 'content-type: application/json' \
    -d "{\"metadata\":{\"name\":\"${WORKER_NAME}\"},\"spec\":{\"queue\":\"${QUEUE_NAME}\",\"scaling\":{\"desiredReplicas\":1,\"minReplicas\":1,\"maxReplicas\":20}}}")"
  if [[ "${code}" == "201" || "${code}" == "200" ]]; then
    echo "  Worker ${WORKER_NAME} created"
    return 0
  fi
  if [[ "${code}" == "409" ]]; then
    echo "  Worker ${WORKER_NAME} already exists"
    return 0
  fi
  fail "create Worker HTTP ${code}: $(cat "${TMP_DIR}/worker.json")"
}

apply_scaling_policies_json() {
  local api_spec worker_spec
  api_spec="$(cat <<EOF
{
  "metadata": {"name": "${API_POLICY}"},
  "spec": {
    "targetRef": {"kind": "Application", "name": "${APP_NAME}"},
    "minReplicas": 2,
    "maxReplicas": 6,
    "metrics": [{"type": "httpRequests", "targetValue": 150}],
    "behavior": {
      "scaleUp": {"stabilizationWindowSeconds": 0, "maxReplicasPerMinute": 10},
      "scaleDown": {"stabilizationWindowSeconds": 0, "maxReplicasPerMinute": 10}
    },
    "metricOutageFallback": {"mode": "hold"}
  }
}
EOF
)"
  worker_spec="$(cat <<EOF
{
  "metadata": {"name": "${WORKER_POLICY}"},
  "spec": {
    "targetRef": {"kind": "Worker", "name": "${WORKER_NAME}"},
    "minReplicas": 1,
    "maxReplicas": 20,
    "metrics": [{"type": "queueDepth", "targetValue": 500, "queue": "${QUEUE_NAME}"}],
    "behavior": {
      "scaleUp": {"stabilizationWindowSeconds": 0, "maxReplicasPerMinute": 20},
      "scaleDown": {"stabilizationWindowSeconds": 0, "maxReplicasPerMinute": 20}
    },
    "metricOutageFallback": {"mode": "hold"}
  }
}
EOF
)"
  local code
  code="$(curl -s -o "${TMP_DIR}/sp-api.json" -w '%{http_code}' -X POST \
    "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies" \
    -H 'content-type: application/json' -H "Idempotency-Key: demo24-${SUFFIX}-${API_POLICY}" \
    -d "${api_spec}")"
  [[ "${code}" == "201" || "${code}" == "200" ]] ||
    fail "create ${API_POLICY} HTTP ${code}: $(cat "${TMP_DIR}/sp-api.json")"
  code="$(curl -s -o "${TMP_DIR}/sp-worker.json" -w '%{http_code}' -X POST \
    "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies" \
    -H 'content-type: application/json' -H "Idempotency-Key: demo24-${SUFFIX}-${WORKER_POLICY}" \
    -d "${worker_spec}")"
  [[ "${code}" == "201" || "${code}" == "200" ]] ||
    fail "create ${WORKER_POLICY} HTTP ${code}: $(cat "${TMP_DIR}/sp-worker.json")"
  # Prove policies are readable before phases start.
  curl --fail --silent --show-error \
    "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies/${API_POLICY}" \
    >"${TMP_DIR}/sp-api-get.json" || fail "GET ${API_POLICY} failed after create"
  echo "  ScalingPolicies applied (${API_POLICY}, ${WORKER_POLICY}) project=${PROJECT_SLUG}"
}

set_override() {
  local replicas="$1"
  curl --fail --silent --show-error \
    "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies/${API_POLICY}" \
    >"${TMP_DIR}/pol.json"
  local rv
  rv="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["metadata"]["resourceVersion"])' "${TMP_DIR}/pol.json")"
  curl --fail --silent --show-error -X PUT \
    "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies/${API_POLICY}/override" \
    -H 'content-type: application/json' \
    -d "{\"metadata\":{\"resourceVersion\":\"${rv}\"},\"replicas\":${replicas},\"reason\":\"demo24 override\",\"ttlSeconds\":120,\"createdBy\":\"demo24\"}" \
    >"${TMP_DIR}/override.json" || fail "set override failed"
  echo "  manual override replicas=${replicas}"
}

clear_override() {
  curl --fail --silent --show-error \
    "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies/${API_POLICY}" \
    >"${TMP_DIR}/pol.json"
  local rv
  rv="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["metadata"]["resourceVersion"])' "${TMP_DIR}/pol.json")"
  curl --fail --silent --show-error -X DELETE \
    "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies/${API_POLICY}/override" \
    -H 'content-type: application/json' \
    -d "{\"metadata\":{\"resourceVersion\":\"${rv}\"},\"reason\":\"demo24 clear\"}" \
    >/dev/null || true
  echo "  manual override cleared"
}

echo "=== Demo 24: autoscaling gate ==="
echo "Project: ${PROJECT_NAME} (${PROJECT_SLUG})"

echo "Starting platform services (sequential compose)..."
purge_managed

echo "Building platform images..."
"${COMPOSE[@]}" build \
  "${METRICS_SERVICE}" "${NETWORK_SERVICE}" "${CONTROL_SERVICE}" \
  "${INFRA_SERVICE}" "${RUNTIME_SERVICE}" "${AUTOSCALER_SERVICE}" ||
  fail "compose build failed"

echo "Starting postgres + registry + forge-network + demo24-metrics..."
# Force-recreate metrics so in-memory load/queue state cannot leak across runs.
docker rm -f demo24-metrics >/dev/null 2>&1 || true
compose_up_retry "${POSTGRES_SERVICE}" "${REGISTRY_SERVICE}" "${NETWORK_SERVICE}" ||
  fail "compose up deps failed"
compose_up_retry --force-recreate "${METRICS_SERVICE}" || fail "compose up metrics failed"
wait_http "http://127.0.0.1:4110/health/live" "forge-network" 120
wait_http "${METRICS_URL}/health/live" "demo24-metrics" 60

echo "Stopping control/infra/runtime/autoscaler for clean ledger reset..."
docker rm -f forge-control forge-infrastructure forge-runtime forge-autoscaler >/dev/null 2>&1 || true
purge_managed

echo "Resetting control/network/infra/autoscaler state..."
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

echo "Starting forge-control + observe runtime + forge-infrastructure..."
# Start autoscaler only after the initial Application is deployed so reservation
# scale-up / orphan races cannot strand the first reconcile.
compose_up_retry --no-deps "${CONTROL_SERVICE}" || fail "compose up control failed"
wait_http "${CONTROL_URL}/health/ready" "forge-control" 180
compose_up_retry --no-deps "${RUNTIME_SERVICE}" || fail "compose up observe runtime failed"
wait_http "${RUNTIME_URL}/health/ready" "forge-runtime (observe)" 120
compose_up_retry --no-deps "${INFRA_SERVICE}" || fail "compose up infra failed"
wait_http "${INFRA_URL}/health/ready" "forge-infrastructure" 180

# DB purge wiped network rows while forge-network stayed up — bounce it so the
# process does not serve from a torn connection / empty in-memory cache.
echo "Restarting forge-network after ledger reset..."
docker rm -f forge-network >/dev/null 2>&1 || true
compose_up_retry --no-deps --force-recreate "${NETWORK_SERVICE}" || fail "compose up network failed"
wait_http "http://127.0.0.1:4110/health/live" "forge-network" 120

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

echo "Waiting for infrastructure kinds..."
for _ in $(seq 1 60); do
  if curl --fail --silent --show-error "${CONTROL_URL}/v1/kinds" |
    python3 -c 'import json,sys; ks=json.load(sys.stdin); assert any(k.get("plural")=="nodepools" for k in ks)'; then
    break
  fi
  sleep 1
done

ensure_demo_image

echo "Applying Docker InfrastructureProvider + NodePool..."
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
wait_ready_nodes 2 180

echo "Creating project hierarchy + deploying 2 replicas..."
# Ensure no leftover autoscaler from a prior interrupted run.
docker rm -f forge-autoscaler >/dev/null 2>&1 || true
create_hierarchy_and_deploy
wait_reconcile_deployed "${DEPLOYMENT_ID}" 180

ensure_worker_kind
create_worker

echo "Starting forge-autoscaler after baseline deploy..."
docker rm -f forge-autoscaler >/dev/null 2>&1 || true
compose_up_retry --no-deps --force-recreate "${AUTOSCALER_SERVICE}" || fail "compose up autoscaler failed"
wait_http "${AUTOSCALER_URL}/health/ready" "forge-autoscaler" 120

apply_scaling_policies_json

# Seed baseline metrics so policies do not enter outage before load phases.
set_http_load 50
publish_queue 0
sync_application_to_deployment

# -----------------------------------------------------------------------------
# Phase 1 — HTTP workload scale-up / scale-down
# -----------------------------------------------------------------------------
echo "=== Phase 1: HTTP workload scale-up ==="
# Traffic math is ceil(rps / targetValue); 450/150 → 3.
set_http_load 450
wait_policy_desired_ge "${API_POLICY}" 3 90
PHASE1_UP="$(policy_desired "${API_POLICY}")"
[[ "${PHASE1_UP}" -ge 3 ]] || fail "expected HTTP scale-up to >=3, got ${PHASE1_UP}"
echo "  HTTP scale-up ok desiredReplicas=${PHASE1_UP}"

echo "=== Phase 1b: HTTP workload scale-down ==="
set_http_load 50
wait_policy_desired_eq "${API_POLICY}" 2 90
echo "  HTTP scale-down ok desiredReplicas=2"

# -----------------------------------------------------------------------------
# Phase 2 — Node scale-up from unschedulable demand
# -----------------------------------------------------------------------------
echo "=== Phase 2: node scale-up from capacity exhaustion ==="
# 2 nodes × 2 slots = 4 capacity. ceil(900/150)=6 → pending → +1 node.
set_http_load 900
wait_policy_desired_eq "${API_POLICY}" 6 90
# Wait until sync raises Deployment desiredReplicas and either pending appears
# or the node autoscaler already grew the pool.
for _ in $(seq 1 60); do
  ready="$(count_ready_forgenodes 2>/dev/null || echo 0)"
  pending="$(curl --fail --silent --show-error "${CONTROL_URL}/v1/placements?status=pending" |
    python3 -c 'import json,sys; print(len(json.load(sys.stdin)))' 2>/dev/null || echo 0)"
  dep_desired="$(curl --fail --silent --show-error "${CONTROL_URL}/v1/deployments/${DEPLOYMENT_ID}" |
    python3 -c 'import json,sys; print(int(json.load(sys.stdin).get("desiredReplicas") or 0))' 2>/dev/null || echo 0)"
  echo "  wait capacity: dep_desired=${dep_desired} pending=${pending} ready=${ready}"
  if [[ "${ready}" -ge 3 ]]; then
    break
  fi
  if [[ "${dep_desired}" -gt 4 && "${pending}" -gt 0 ]]; then
    break
  fi
  sleep 1
done
wait_ready_nodes 3 180
wait_no_pending 180
wait_reconcile_deployed "${DEPLOYMENT_ID}" 150
wait_pool_caught_up 90
echo "  node scale-up ok (3+ Ready nodes, no pending)"

# -----------------------------------------------------------------------------
# Phase 3 — Worker queue backlog
# -----------------------------------------------------------------------------
echo "=== Phase 3: worker scale-up from queue backlog ==="
publish_queue 20000
wait_policy_desired_eq "${WORKER_POLICY}" 20 90
WORKER_DESIRED="$(curl --fail --silent --show-error \
  "${CONTROL_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/workers/${WORKER_NAME}" |
  python3 -c 'import json,sys; d=json.load(sys.stdin); print(int(((d.get("spec") or {}).get("scaling") or {}).get("desiredReplicas") or 0))')"
[[ "${WORKER_DESIRED}" -eq 20 ]] || fail "Worker desiredReplicas=${WORKER_DESIRED}, want 20"
echo "  worker scale-up ok desiredReplicas=20"

publish_queue 0
wait_policy_desired_eq "${WORKER_POLICY}" 1 90
echo "  worker scale-down ok desiredReplicas=1"

# -----------------------------------------------------------------------------
# Phase 4 — Scale-down nodes after drain
# -----------------------------------------------------------------------------
echo "=== Phase 4: workload + node scale-down ==="
set_http_load 40
wait_policy_desired_eq "${API_POLICY}" 2 90
# Let reconcile drop replicas before the node autoscaler scores underutilized nodes.
for _ in $(seq 1 60); do
  actual="$(curl --fail --silent --show-error \
    "${CONTROL_URL}/v1/deployments/${DEPLOYMENT_ID}/reconcile" |
    python3 -c 'import json,sys; d=json.load(sys.stdin); print(int(d.get("actualReplicas") or d.get("updatedReplicas") or 0))' 2>/dev/null || echo 99)"
  if [[ "${actual}" -le 2 ]]; then
    echo "  deployment actualReplicas=${actual}"
    break
  fi
  sleep 1
done
wait_reconcile_deployed "${DEPLOYMENT_ID}" 150
wait_ready_nodes 2 240 1
echo "  node scale-down ok (exactly 2 Ready nodes)"

# -----------------------------------------------------------------------------
# Safety — manual override + metric-outage fallback
# -----------------------------------------------------------------------------
echo "=== Safety: manual override + metric-outage fallback ==="
set_override 5
wait_policy_desired_eq "${API_POLICY}" 5 60
override_visible="$(policy_field "${API_POLICY}" 'd.get("status",{}).get("manualOverride") is not None')"
[[ "${override_visible}" == "True" ]] || fail "manualOverride not visible in status"
echo "  manual override visible desiredReplicas=5"
clear_override

# Hold current desired under outage (mode=hold).
set_http_load 40
wait_policy_desired_eq "${API_POLICY}" 2 60
clear_http_load
# Wait for metricOutageMode=hold
outage=""
for _ in $(seq 1 60); do
  outage="$(policy_field "${API_POLICY}" 'd.get("status",{}).get("metricOutageMode") or ""')"
  desired="$(policy_desired "${API_POLICY}")"
  if [[ "${outage}" == "hold" && "${desired}" -eq 2 ]]; then
    echo "  metric-outage fallback visible mode=${outage} desiredReplicas=${desired}"
    break
  fi
  sleep 1
done
[[ "${outage}" == "hold" ]] || fail "expected metricOutageMode=hold, got '${outage}'"

# Restore baseline metrics for clean shutdown.
set_http_load 40
publish_queue 0

echo "demo 24 PASSED"
