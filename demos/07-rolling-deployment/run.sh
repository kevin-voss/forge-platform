#!/usr/bin/env bash
# Demo 07: rolling update + automatic rollback (epic 07 acceptance gate).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/07-rolling-deployment"
APP_DIR="${DEMO_DIR}/apps/demo"
COMPOSE=(
  docker compose
  -f "${ROOT_DIR}/compose.yaml"
  -f "${DEMO_DIR}/docker-compose.yml"
  --project-directory "${ROOT_DIR}"
)
GATEWAY_URL="${FORGE_GATEWAY_URL:-http://127.0.0.1:4000}"
CONTROL_URL="${FORGE_CONTROL_URL:-http://127.0.0.1:4001}"
RUNTIME_URL="${FORGE_RUNTIME_URL:-http://127.0.0.1:4102}"
GATEWAY_SERVICE="forge-gateway"
CONTROL_SERVICE="forge-control"
RUNTIME_SERVICE="forge-runtime"
POSTGRES_SERVICE="postgres"
REGISTRY_SERVICE="registry"
CLI_DIR="${ROOT_DIR}/tools/forge-cli"
FORGE_BIN="${CLI_DIR}/forge"
REGISTRY="${FORGE_REGISTRY:-localhost:5000}"
V1_IMAGE="${V1_IMAGE:-${REGISTRY}/demo:v1}"
V2_IMAGE="${V2_IMAGE:-${REGISTRY}/demo:v2}"
V3_IMAGE="${V3_IMAGE:-${REGISTRY}/demo:v3-broken}"
HOST="${DEMO_HOST:-demo.localhost}"
SCENARIO="${1:-all}"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-rolling-demo.XXXXXX")"
CONFIG_HOME="${TMP_DIR}/xdg-config"
PROBE_PID=""
PROBE_FAIL_FILE="${TMP_DIR}/probe-fails"
export XDG_CONFIG_HOME="${CONFIG_HOME}"
export CI=1
export FORGE_PROFILE="${FORGE_PROFILE:-demo}"
export FORGE_ENDPOINT="${FORGE_ENDPOINT:-${CONTROL_URL}}"
# Control owns create/stop for epic 07 (do not set FORGE_LIFECYCLE_OWNER=runtime).
export FORGE_LIFECYCLE_OWNER="${FORGE_LIFECYCLE_OWNER:-control}"
# Demo-tuned values (force — do not inherit a too-low timeout from the parent shell).
export FORGE_RECONCILE_INTERVAL_MS=1000
export FORGE_ROLLOUT_TIMEOUT_S=90
export FORGE_ROLLOUT_BATCH_SIZE=1
export FORGE_READINESS_POLL_MS=500
export FORGE_READINESS_MAX_WAIT_S=45
# Runtime probes: keep failure threshold >1 so startup races do not mark Failed.
export FORGE_PROBE_INTERVAL_SECONDS=2
export FORGE_PROBE_FAILURE_THRESHOLD=2
export FORGE_RECONCILE_INTERVAL_SECONDS=3
if [[ -z "${FORGE_HOST_PATTERN:-}" ]]; then
  FORGE_HOST_PATTERN='{service}.localhost'
fi
export FORGE_HOST_PATTERN
export FORGE_ROUTE_SYNC_INTERVAL_SECONDS="${FORGE_ROUTE_SYNC_INTERVAL_SECONDS:-2}"
export FORGE_UPSTREAM_PROBE_INTERVAL_SECONDS="${FORGE_UPSTREAM_PROBE_INTERVAL_SECONDS:-1}"
export FORGE_UPSTREAM_FAILURE_THRESHOLD="${FORGE_UPSTREAM_FAILURE_THRESHOLD:-1}"
export FORGE_UPSTREAM_SUCCESS_THRESHOLD="${FORGE_UPSTREAM_SUCCESS_THRESHOLD:-1}"
mkdir -p "${CONFIG_HOME}"
: >"${PROBE_FAIL_FILE}"

TRACKED_DEPLOYMENTS=()

cleanup() {
  local dep
  if [[ -n "${PROBE_PID}" ]]; then
    kill "${PROBE_PID}" >/dev/null 2>&1 || true
    wait "${PROBE_PID}" 2>/dev/null || true
    PROBE_PID=""
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
  # Also sweep demo replica containers by name prefix if labels are absent.
  docker ps -aq --filter "name=forge-demo-" | while read -r cid; do
    [[ -n "${cid}" ]] || continue
    docker rm -f "${cid}" >/dev/null 2>&1 || true
  done
  "${COMPOSE[@]}" stop "${GATEWAY_SERVICE}" "${RUNTIME_SERVICE}" "${CONTROL_SERVICE}" >/dev/null 2>&1 || true
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

dump_context() {
  local dep="${DEPLOYMENT_ID:-}"
  echo "--- controller / history ---" >&2
  if [[ -n "${dep}" ]]; then
    curl --silent --show-error "${CONTROL_URL}/v1/deployments/${dep}/reconcile" >&2 || true
    echo >&2
    curl --silent --show-error "${CONTROL_URL}/v1/deployments/${dep}/history" >&2 || true
    echo >&2
  fi
  echo "--- ${GATEWAY_SERVICE} /admin/routes ---" >&2
  curl --silent --show-error "${GATEWAY_URL}/admin/routes" >&2 || true
  echo >&2
  echo "--- ${CONTROL_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=120 "${CONTROL_SERVICE}" >&2 || true
  echo "--- ${RUNTIME_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=80 "${RUNTIME_SERVICE}" >&2 || true
  echo "--- ${GATEWAY_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${GATEWAY_SERVICE}" >&2 || true
  echo "--- docker ps -a (forge-*) ---" >&2
  docker ps -a --filter name=forge- --format 'table {{.Names}}\t{{.Status}}\t{{.Image}}' >&2 || true
}

fail() {
  echo "Demo 07 failed: $*" >&2
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
  local tag="$1" version="$2" ready_fail="$3"
  echo "Building and pushing ${tag} (VERSION=${version} READY_FAIL=${ready_fail})..."
  docker build \
    --build-arg "VERSION=${version}" \
    --build-arg "READY_FAIL=${ready_fail}" \
    -t "${tag}" \
    "${APP_DIR}" || fail "could not build ${tag}"
  docker push "${tag}" >/dev/null || fail "could not push ${tag}"
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
  docker ps -aq --filter "name=forge-demo-" | while read -r cid; do
    [[ -n "${cid}" ]] || continue
    docker rm -f "${cid}" >/dev/null 2>&1 || true
  done
}

create_hierarchy() {
  local suffix="$1"
  forge_json "${TMP_DIR}/project.json" project create --name "demo-rolling-${suffix}"
  PROJECT_ID="$(read_id "${TMP_DIR}/project.json")"
  forge_json "${TMP_DIR}/environment.json" env create --project "${PROJECT_ID}" --name development
  ENVIRONMENT_ID="$(read_id "${TMP_DIR}/environment.json")"
  forge_json "${TMP_DIR}/application.json" app create --project "${PROJECT_ID}" --name demos
  APPLICATION_ID="$(read_id "${TMP_DIR}/application.json")"
  forge_json "${TMP_DIR}/service.json" service create --app "${APPLICATION_ID}" --name demo --port 8080
  SERVICE_ID="$(read_id "${TMP_DIR}/service.json")"
}

patch_deployment_image() {
  local deployment_id="$1" image="$2"
  curl --fail --silent --show-error -X PATCH \
    "${CONTROL_URL}/v1/deployments/${deployment_id}" \
    -H 'content-type: application/json' \
    -d "{\"image\":\"${image}\"}" >"${TMP_DIR}/patch.json" ||
    fail "PATCH deployment image=${image} failed"
  python3 -c 'import json,sys; assert json.load(open(sys.argv[1]))["image"]==sys.argv[2]' \
    "${TMP_DIR}/patch.json" "${image}" ||
    fail "PATCH did not set image=${image}: $(cat "${TMP_DIR}/patch.json")"
}

wait_reconcile_status() {
  local deployment_id="$1" expected="$2" attempts="${3:-90}" image_substr="${4:-}"
  local status="" image=""
  echo "Waiting for deployment ${deployment_id} reconcile status=${expected}${image_substr:+ image~${image_substr}} ..."
  for _ in $(seq 1 "${attempts}"); do
    curl --fail --silent --show-error \
      "${CONTROL_URL}/v1/deployments/${deployment_id}/reconcile" \
      >"${TMP_DIR}/reconcile.json" || true
    if [[ -s "${TMP_DIR}/reconcile.json" ]]; then
      status="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("status") or "")' "${TMP_DIR}/reconcile.json")"
      image="$(python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print((d.get("desired") or {}).get("image") or d.get("currentImage") or "")' "${TMP_DIR}/reconcile.json")"
      if [[ "${status}" == "${expected}" ]]; then
        if [[ -z "${image_substr}" || "${image}" == *"${image_substr}"* ]]; then
          echo "  status=${status} image=${image}"
          return 0
        fi
      fi
    fi
    sleep 1
  done
  fail "deployment ${deployment_id} reconcile status=${status:-unknown} image=${image:-unknown}, want ${expected}${image_substr:+ image~${image_substr}}"
}

refresh_routes() {
  curl --fail --silent --show-error -X POST "${GATEWAY_URL}/admin/routes/refresh" \
    >"${TMP_DIR}/refresh.json" || fail "POST /admin/routes/refresh failed"
}

dump_routes() {
  curl --fail --silent --show-error "${GATEWAY_URL}/admin/routes" >"${TMP_DIR}/routes.json" ||
    fail "GET /admin/routes failed"
}

wait_route_host() {
  local host="$1" attempts="${2:-60}"
  echo "Waiting for gateway route host=${host} ..."
  for _ in $(seq 1 "${attempts}"); do
    refresh_routes
    dump_routes
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

curl_version() {
  local host="$1"
  local code body
  code="$(curl --silent --show-error -o "${TMP_DIR}/gw-body.json" -w '%{http_code}' \
    -H "Host: ${host}" "${GATEWAY_URL}/")" || true
  [[ "${code}" == "200" ]] ||
    fail "Host ${host} returned HTTP ${code}, want 200; body=$(cat "${TMP_DIR}/gw-body.json" 2>/dev/null || true)"
  body="$(cat "${TMP_DIR}/gw-body.json")"
  echo "${body}" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("version",""))'
}

# Soft read for retry loops — never exits the demo on transient errors.
probe_version() {
  local host="$1"
  local code
  code="$(curl --silent --show-error -o "${TMP_DIR}/gw-probe.json" -w '%{http_code}' \
    -H "Host: ${host}" "${GATEWAY_URL}/" 2>/dev/null || echo "000")"
  if [[ "${code}" != "200" ]]; then
    echo ""
    return 0
  fi
  python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("version",""))' \
    "${TMP_DIR}/gw-probe.json" 2>/dev/null || echo ""
}

assert_serving_version() {
  local want="$1" attempts="${2:-60}"
  local got=""
  echo "Asserting gateway Host=${HOST} serves version=${want} ..."
  for _ in $(seq 1 "${attempts}"); do
    refresh_routes >/dev/null 2>&1 || true
    got="$(probe_version "${HOST}")"
    if [[ "${got}" == "${want}" ]]; then
      echo "  serving version: ${got}"
      return 0
    fi
    sleep 1
  done
  fail "gateway still serving version=${got:-unknown}, want ${want}; body=$(cat "${TMP_DIR}/gw-probe.json" 2>/dev/null || true)"
}

count_v3_containers() {
  docker ps -a --format '{{.Image}} {{.Names}}' |
    grep -F "${V3_IMAGE}" |
    grep -c . || true
}

start_probe() {
  : >"${PROBE_FAIL_FILE}"
  (
    local code
    while true; do
      code="$(curl --silent --show-error -o /dev/null -w '%{http_code}' \
        -H "Host: ${HOST}" "${GATEWAY_URL}/" || echo "000")"
      if [[ "${code}" != "200" ]]; then
        echo "${code}" >>"${PROBE_FAIL_FILE}"
      fi
      sleep 0.15
    done
  ) &
  PROBE_PID=$!
}

stop_probe() {
  if [[ -n "${PROBE_PID}" ]]; then
    kill "${PROBE_PID}" >/dev/null 2>&1 || true
    wait "${PROBE_PID}" 2>/dev/null || true
    PROBE_PID=""
  fi
}

assert_probe_clean() {
  local fails
  fails="$(wc -l <"${PROBE_FAIL_FILE}" | tr -d ' ')"
  [[ "${fails}" == "0" ]] ||
    fail "no-downtime probe recorded ${fails} failed request(s); sample=$(head -n 5 "${PROBE_FAIL_FILE}" | tr '\n' ' ')"
  echo "  0 failed requests OK"
}

fetch_history() {
  local deployment_id="$1"
  curl --fail --silent --show-error \
    "${CONTROL_URL}/v1/deployments/${deployment_id}/history" \
    >"${TMP_DIR}/history.json" || fail "GET /history failed"
}

assert_history_has() {
  local to_status="$1" image_substr="${2:-}"
  TO="${to_status}" IMG="${image_substr}" python3 - "${TMP_DIR}/history.json" <<'PY' ||
import json, os, sys
to = os.environ["TO"]
img = os.environ.get("IMG") or ""
events = json.load(open(sys.argv[1])).get("events") or []
for ev in events:
    if ev.get("to") != to:
        continue
    if img and img not in (ev.get("image") or ""):
        continue
    sys.exit(0)
sys.exit(1)
PY
    fail "history missing to=${to_status} image~${image_substr}: $(cat "${TMP_DIR}/history.json")"
}

assert_history_rollout_and_rollback() {
  echo "Asserting deployment history trail..."
  fetch_history "${DEPLOYMENT_ID}"
  # Successful v2 rollout
  assert_history_has "deployed" "demo:v2"
  # Broken v3 attempt then rollback to v2
  assert_history_has "deploying" "demo:v3-broken"
  assert_history_has "rolling_back" ""
  assert_history_has "rolled_back" "demo:v2"
  echo "  history: v2 deployed + v3 rolled_back to v2 OK"
}

scenario_a() {
  echo "[A] deploying v1 replicas=2 ..."
  forge_json "${TMP_DIR}/dep.json" deployment create \
    --service "${SERVICE_ID}" \
    --image "${V1_IMAGE}" \
    --env "${ENVIRONMENT_ID}" \
    --replicas 2
  DEPLOYMENT_ID="$(read_id "${TMP_DIR}/dep.json")"
  track_deployment "${DEPLOYMENT_ID}"

  wait_reconcile_status "${DEPLOYMENT_ID}" "deployed" 90 "demo:v1"
  wait_route_host "${HOST}"
  assert_serving_version "v1"

  echo "[A] rolling update v1 -> v2 (no-downtime probe) ..."
  start_probe
  sleep 0.5
  patch_deployment_image "${DEPLOYMENT_ID}" "${V2_IMAGE}"
  # Require deployed + desired image v2 so we do not race the prior v1 deployed status.
  wait_reconcile_status "${DEPLOYMENT_ID}" "deployed" 180 "demo:v2"
  stop_probe
  assert_probe_clean
  assert_serving_version "v2"
  echo "[A] serving version: v2"
  echo "[A] rolling update v1 -> v2 ... 0 failed requests OK"
}

scenario_b() {
  [[ -n "${DEPLOYMENT_ID:-}" ]] || fail "scenario B requires a deployment from scenario A"
  echo "[B] deploying broken v3 ... waiting for rollback"
  patch_deployment_image "${DEPLOYMENT_ID}" "${V3_IMAGE}"
  # Enter deploying for v3, then wait for automatic rollback to last healthy (v2).
  wait_reconcile_status "${DEPLOYMENT_ID}" "deploying" 60 "demo:v3-broken" || true
  wait_reconcile_status "${DEPLOYMENT_ID}" "rolled_back" 180 "demo:v2"
  assert_serving_version "v2" 60
  local v3_count
  v3_count="$(count_v3_containers)"
  [[ "${v3_count}" == "0" ]] ||
    fail "expected 0 v3 containers after rollback, found ${v3_count}"
  echo "[B] rolled_back to v2 OK ; serving version: v2 ; v3 containers: 0"
}

bootstrap() {
  echo "== Demo 07: rolling deployment (reconcile epic gate) =="
  echo "Building forge CLI..."
  make -C "${CLI_DIR}" build || fail "CLI build failed"
  [[ -x "${FORGE_BIN}" ]] || fail "forge binary missing at ${FORGE_BIN}"

  echo "Starting PostgreSQL, registry, Control, Runtime, and Gateway..."
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
  # Host-side Flyway: apply from source so new versioned scripts are never missed.
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
  "${COMPOSE[@]}" stop "${RUNTIME_SERVICE}" >/dev/null 2>&1 || true
  purge_stale_deployments
  "${COMPOSE[@]}" up -d --build --force-recreate "${RUNTIME_SERVICE}"
  wait_http "${RUNTIME_URL}/health/ready" "Runtime"
  "${COMPOSE[@]}" up -d --build --force-recreate "${GATEWAY_SERVICE}"
  wait_http "${GATEWAY_URL}/health/ready" "Gateway"

  ctrl_timeout="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_ROLLOUT_TIMEOUT_S 2>/dev/null || true)"
  echo "  control FORGE_ROLLOUT_TIMEOUT_S=${ctrl_timeout}"
  [[ "${ctrl_timeout}" == "90" ]] ||
    fail "Control FORGE_ROLLOUT_TIMEOUT_S must be 90 (got: ${ctrl_timeout})"

  gw_pattern="$(docker exec "${GATEWAY_SERVICE}" printenv FORGE_HOST_PATTERN 2>/dev/null || true)"
  echo "  gateway FORGE_HOST_PATTERN=${gw_pattern}"
  [[ "${gw_pattern}" == *'{service}.localhost'* ]] ||
    fail "gateway FORGE_HOST_PATTERN must be '{service}.localhost' (got: ${gw_pattern})"

  ensure_demo_image "${V1_IMAGE}" "v1" "false"
  ensure_demo_image "${V2_IMAGE}" "v2" "false"
  ensure_demo_image "${V3_IMAGE}" "v3" "true"

  echo "Configuring CLI profile ${FORGE_PROFILE} -> ${FORGE_ENDPOINT}..."
  forge config set endpoint "${FORGE_ENDPOINT}" --profile "${FORGE_PROFILE}"
  forge config use "${FORGE_PROFILE}"

  SUFFIX="$(date +%s)-$$"
  create_hierarchy "${SUFFIX}"
}

case "${SCENARIO}" in
  --scenario)
    shift || true
    SCENARIO="${1:-all}"
    ;;
esac

case "${SCENARIO}" in
  A|a|--scenario-A)
    bootstrap
    scenario_a
    fetch_history "${DEPLOYMENT_ID}"
    assert_history_has "deployed" "demo:v2"
    echo
    echo "demo 07 Scenario A PASSED"
    ;;
  B|b|--scenario-B)
    # Full bootstrap + A then B so B can be run in isolation for CI.
    bootstrap
    scenario_a
    scenario_b
    assert_history_rollout_and_rollback
    echo
    echo "demo 07 Scenario B PASSED"
    ;;
  all|ALL|"")
    bootstrap
    scenario_a
    scenario_b
    assert_history_rollout_and_rollback
    echo
    echo "demo 07 PASSED"
    echo "  Project:      ${PROJECT_ID}"
    echo "  Environment:  ${ENVIRONMENT_ID}"
    echo "  Application:  ${APPLICATION_ID}"
    echo "  Service:      ${SERVICE_ID}"
    echo "  Deployment:   ${DEPLOYMENT_ID}"
    echo "  Host:         ${HOST}"
    echo "  Images:       ${V1_IMAGE} → ${V2_IMAGE} → ${V3_IMAGE} (rolled back)"
    echo "  Gateway URL:  ${GATEWAY_URL}"
    echo "  Rollout timeout: ${FORGE_ROLLOUT_TIMEOUT_S}s"
    ;;
  *)
    echo "Usage: $0 [all|A|B|--scenario A|--scenario B]" >&2
    exit 2
    ;;
esac
