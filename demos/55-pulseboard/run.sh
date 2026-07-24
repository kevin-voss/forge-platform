#!/usr/bin/env bash
# Demo 55: PulseBoard + HTTP request-rate autoscaling (epic 55.02).
# Usage:
#   demos/55-pulseboard/run.sh          # build → apply → ScalingPolicy → load up/down proof
#   demos/55-pulseboard/run.sh --down   # tear down product resources only
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
export FORGE_SCHEDULER_STRATEGY="${FORGE_SCHEDULER_STRATEGY:-single-node}"
export FORGE_SCHEDULER_LOCAL_NODE_ID="${FORGE_SCHEDULER_LOCAL_NODE_ID:-node-local}"
export FORGE_ROUTE_SYNC_INTERVAL_SECONDS="${FORGE_ROUTE_SYNC_INTERVAL_SECONDS:-2}"
export FORGE_UPSTREAM_PROBE_INTERVAL_SECONDS="${FORGE_UPSTREAM_PROBE_INTERVAL_SECONDS:-2}"
export FORGE_UPSTREAM_FAILURE_THRESHOLD="${FORGE_UPSTREAM_FAILURE_THRESHOLD:-1}"
export FORGE_UPSTREAM_SUCCESS_THRESHOLD="${FORGE_UPSTREAM_SUCCESS_THRESHOLD:-1}"
export FORGE_AUTOSCALER_EVAL_INTERVAL_MS="${FORGE_AUTOSCALER_EVAL_INTERVAL_MS:-1000}"
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
METRICS_URL="${FORGE_DEMO55_METRICS_URL:-http://127.0.0.1:4197}"
CONTROL_SERVICE="forge-control"
RUNTIME_SERVICE="forge-runtime"
GATEWAY_SERVICE="forge-gateway"
BUILD_SERVICE="forge-build"
AUTOSCALER_SERVICE="forge-autoscaler"
METRICS_SERVICE="demo55-metrics"
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
ENV_NAME="local"
SYNC_PID=""
MIN_REPLICAS=1
MAX_REPLICAS=10
TARGET_RPS=50
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
  echo "--- demo55-metrics application ---" >&2
  curl --silent --show-error "${METRICS_URL}/admin/metrics?application=${API_NAME}" >&2 || true
  echo >&2
  echo "--- ${CONTROL_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=80 "${CONTROL_SERVICE}" >&2 || true
  echo "--- ${RUNTIME_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${RUNTIME_SERVICE}" >&2 || true
  echo "--- ${GATEWAY_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${GATEWAY_SERVICE}" >&2 || true
  echo "--- ${AUTOSCALER_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=80 "${AUTOSCALER_SERVICE}" >&2 || true
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
MIN_REPLICAS=${MIN_REPLICAS}
MAX_REPLICAS=${MAX_REPLICAS}
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
  echo "Teardown complete."
}

ensure_platform() {
  echo "Ensuring Postgres, registry, Autoscaler, Control, Runtime, Gateway, Build..."
  "${COMPOSE[@]}" up -d "${POSTGRES_SERVICE}" "${REGISTRY_SERVICE}"
  for _ in $(seq 1 60); do
    if "${COMPOSE[@]}" exec -T "${POSTGRES_SERVICE}" pg_isready -U forge >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done
  "${COMPOSE[@]}" exec -T "${POSTGRES_SERVICE}" pg_isready -U forge >/dev/null 2>&1 ||
    fail "Postgres not ready"

  # Autoscaler DB (shared with demos 24/52).
  docker exec -i forge-postgres psql -U forge -d postgres -v ON_ERROR_STOP=1 <<'SQL' >/dev/null \
    || fail "could not ensure forge_autoscaler database"
SELECT 'CREATE DATABASE forge_autoscaler'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'forge_autoscaler')\gexec
SQL

  local need_recreate=0
  local auth_mode pattern strategy gateway_admin
  auth_mode="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_AUTH_MODE 2>/dev/null || true)"
  pattern="$(docker exec "${GATEWAY_SERVICE}" printenv FORGE_HOST_PATTERN 2>/dev/null || true)"
  strategy="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_SCHEDULER_STRATEGY 2>/dev/null || true)"
  gateway_admin="$(docker exec "${AUTOSCALER_SERVICE}" printenv FORGE_GATEWAY_ADMIN_URL 2>/dev/null || true)"
  if [[ "${auth_mode}" != "dev" ]]; then
    need_recreate=1
  fi
  if [[ "${pattern}" != *'{service}.pulseboard.localhost'* ]]; then
    need_recreate=1
  fi
  if [[ "${strategy}" != "single-node" ]]; then
    need_recreate=1
  fi
  if [[ "${gateway_admin}" != *"demo55-metrics"* ]]; then
    need_recreate=1
  fi

  echo "Starting demo55-metrics sidecar..."
  docker rm -f demo55-metrics >/dev/null 2>&1 || true
  "${COMPOSE[@]}" up -d --build --force-recreate "${METRICS_SERVICE}" ||
    fail "compose up ${METRICS_SERVICE} failed"
  wait_http "${METRICS_URL}/health/live" "demo55-metrics" 60

  if [[ "${need_recreate}" -eq 1 ]]; then
    echo "Recreating Control/Runtime/Gateway/Autoscaler with demo 55 overlay..."
    "${COMPOSE[@]}" up -d --force-recreate \
      "${CONTROL_SERVICE}" "${RUNTIME_SERVICE}" "${GATEWAY_SERVICE}" "${AUTOSCALER_SERVICE}"
  else
    echo "Control/Gateway/Autoscaler already configured for demo 55; ensuring they are up..."
    "${COMPOSE[@]}" up -d "${CONTROL_SERVICE}" "${RUNTIME_SERVICE}" "${GATEWAY_SERVICE}" \
      "${AUTOSCALER_SERVICE}"
  fi
  "${COMPOSE[@]}" up -d "${BUILD_SERVICE}"

  wait_http "${CONTROL_URL}/health/ready" "Control"
  wait_http "${RUNTIME_URL}/health/ready" "Runtime"
  wait_http "${GATEWAY_URL}/health/ready" "Gateway"
  wait_http "${BUILD_URL}/health/ready" "Build" 60 || true
  wait_http "${AUTOSCALER_URL}/health/ready" "Autoscaler" 120

  pattern="$(docker exec "${GATEWAY_SERVICE}" printenv FORGE_HOST_PATTERN 2>/dev/null || true)"
  [[ "${pattern}" == *'{service}.pulseboard.localhost'* ]] ||
    fail "gateway FORGE_HOST_PATTERN must be '{service}.pulseboard.localhost' (got: ${pattern})"
  gateway_admin="$(docker exec "${AUTOSCALER_SERVICE}" printenv FORGE_GATEWAY_ADMIN_URL 2>/dev/null || true)"
  [[ "${gateway_admin}" == *"demo55-metrics"* ]] ||
    fail "autoscaler FORGE_GATEWAY_ADMIN_URL must point at demo55-metrics (got: ${gateway_admin})"
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

purge_stale_workloads() {
  # Leftover desired-state from prior demos leaves multiple Gateway upstreams.
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
  echo "Checking /stats baseline replicas=1 ..."
  curl --fail --silent --show-error -H "Host: ${API_HOST}" "${GATEWAY_URL}/stats" \
    >"${TMP_DIR}/stats.json" || fail "GET /stats failed"
  python3 - "${TMP_DIR}/stats.json" <<'PY' || fail "/stats baseline assertion failed"
import json, sys
stats = json.load(open(sys.argv[1]))
assert stats.get("replicas") == 1, stats
assert "counter" in stats, stats
print(f"  /stats replicas={stats['replicas']} counter={stats.get('counter')}")
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
  curl --fail --silent --show-error -X PUT \
    "${METRICS_URL}/demo/application/${API_NAME}" \
    -H 'content-type: application/json' \
    -d "{\"requestsPerSecond\":${rps},\"activeConnections\":${rps},\"sampleCount\":2000}" \
    >/dev/null || fail "set idle metrics failed"
  echo "  metrics: application=${API_NAME} rps=${rps}"
}

sync_application_to_deployment() {
  # Bridge Application.spec.scaling.desiredReplicas → Deployment desiredReplicas
  # (autoscaler actuates Application; reconciler reads Deployment).
  [[ -n "${API_DEPLOYMENT_ID}" ]] || fail "API_DEPLOYMENT_ID required before sync loop"
  python3 - "${CONTROL_URL}" "${PROJECT_SLUG}" "${ENV_NAME}" "${API_NAME}" "${API_DEPLOYMENT_ID}" <<'PY' &
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
                print(f"sync: api deployment {dep_id} desiredReplicas {cur} -> {desired}", flush=True)
            last = desired
    except Exception as exc:
        print(f"sync: {exc}", flush=True)
    time.sleep(1)
PY
  SYNC_PID=$!
  echo "  started Application→Deployment sync pid=${SYNC_PID}"
}

prove_http_autoscaling() {
  local up_desired down_desired metric_type metric_value peak_min
  echo "Proving API httpRequests autoscaling (load → scale-up → stop → scale-down)..."
  python3 "${DEMO_DIR}/scripts/test_http_scaling.py" ||
    fail "http scaling unit tests failed"

  ensure_application_resource
  apply_api_scaling_policy
  set_idle_metrics "${IDLE_RPS}"
  sync_application_to_deployment

  wait_policy_desired_eq "${MIN_REPLICAS}" 60
  assert_replicas_in_bounds "$(policy_desired)"

  # ceil(LOAD_RPS / TARGET_RPS); clamp to max. Default 250/50 → 5.
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

  echo "  scale-up ok desiredReplicas=${up_desired} metricType=${metric_type} metricValue=${metric_value}"

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
  echo "  scale-down ok desiredReplicas=${down_desired}"
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

  prove_http_autoscaling

  write_state
  echo
  echo "demo 55 deploy READY"
  echo "  Board:        http://${BOARD_HOST}:4000/"
  echo "  API:          http://${API_HOST}:4000/health/ready"
  echo "  Stats:        http://${API_HOST}:4000/stats"
  echo "  Autoscaler:   ${AUTOSCALER_URL} policy=${API_POLICY} bounds=[${MIN_REPLICAS},${MAX_REPLICAS}] targetRPS=${TARGET_RPS}"
  echo "  Loadgen:      ${LOADGEN_SCRIPT} start|stop (against ${API_HOST})"
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
