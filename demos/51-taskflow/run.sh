#!/usr/bin/env bash
# Demo 51: TaskFlow product scaffold + baseline deploy (epic 51.01).
# Usage:
#   demos/51-taskflow/run.sh          # build → apply → Ready on app./api.taskflow.localhost
#   demos/51-taskflow/run.sh --down   # tear down product resources only
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/51-taskflow"
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
API_IMAGE="${DEMO_API_IMAGE:-${REGISTRY}/taskflow/taskflow-api:v1}"
WEB_IMAGE="${DEMO_WEB_IMAGE:-${REGISTRY}/taskflow/taskflow-web:v1}"
API_HOST="api.taskflow.localhost"
APP_HOST="app.taskflow.localhost"
ENV_NAME="local"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-demo-51.XXXXXX")"
CONFIG_HOME="${TMP_DIR}/xdg-config"
export XDG_CONFIG_HOME="${CONFIG_HOME}"
export CI="${CI:-1}"
export FORGE_PROFILE="${FORGE_PROFILE:-demo51}"
export FORGE_ENDPOINT="${FORGE_ENDPOINT:-${CONTROL_URL}}"
mkdir -p "${CONFIG_HOME}"

fail() {
  echo "Demo 51 failed: $*" >&2
  echo "--- ${GATEWAY_SERVICE} /admin/routes ---" >&2
  curl --silent --show-error "${GATEWAY_URL}/admin/routes" >&2 || true
  echo >&2
  echo "--- ${CONTROL_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=80 "${CONTROL_SERVICE}" >&2 || true
  echo "--- ${RUNTIME_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${RUNTIME_SERVICE}" >&2 || true
  echo "--- ${GATEWAY_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${GATEWAY_SERVICE}" >&2 || true
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
API_DEPLOYMENT_ID=${API_DEPLOYMENT_ID}
WEB_DEPLOYMENT_ID=${WEB_DEPLOYMENT_ID}
API_IMAGE=${API_IMAGE}
WEB_IMAGE=${WEB_IMAGE}
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
  echo "Tearing down demo 51 TaskFlow..."
  if read_state; then
    delete_deployment "${API_DEPLOYMENT_ID:-}"
    delete_deployment "${WEB_DEPLOYMENT_ID:-}"
    rm -f "${STATE_FILE}"
  else
    echo "  no .demo-state; best-effort cleanup of demo=51 containers"
    docker ps -aq --filter "label=forge.managed=true" --filter "label=demo=51" |
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
  local auth_mode pattern strategy
  auth_mode="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_AUTH_MODE 2>/dev/null || true)"
  pattern="$(docker exec "${GATEWAY_SERVICE}" printenv FORGE_HOST_PATTERN 2>/dev/null || true)"
  strategy="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_SCHEDULER_STRATEGY 2>/dev/null || true)"
  if [[ "${auth_mode}" != "dev" ]]; then
    need_recreate=1
  fi
  if [[ "${pattern}" != *'{service}.taskflow.localhost'* ]]; then
    need_recreate=1
  fi
  if [[ "${strategy}" != "single-node" ]]; then
    need_recreate=1
  fi

  if [[ "${need_recreate}" -eq 1 ]]; then
    echo "Recreating Control/Runtime/Gateway with demo 51 overlay..."
    "${COMPOSE[@]}" up -d --force-recreate \
      "${CONTROL_SERVICE}" "${RUNTIME_SERVICE}" "${GATEWAY_SERVICE}"
  else
    echo "Control/Gateway already configured for demo 51; ensuring they are up..."
    "${COMPOSE[@]}" up -d "${CONTROL_SERVICE}" "${RUNTIME_SERVICE}" "${GATEWAY_SERVICE}"
  fi
  "${COMPOSE[@]}" up -d "${BUILD_SERVICE}"

  wait_http "${CONTROL_URL}/health/ready" "Control"
  wait_http "${RUNTIME_URL}/health/ready" "Runtime"
  wait_http "${GATEWAY_URL}/health/ready" "Gateway"
  wait_http "${BUILD_URL}/health/ready" "Build" 60 || true

  pattern="$(docker exec "${GATEWAY_SERVICE}" printenv FORGE_HOST_PATTERN 2>/dev/null || true)"
  [[ "${pattern}" == *'{service}.taskflow.localhost'* ]] ||
    fail "gateway FORGE_HOST_PATTERN must be '{service}.taskflow.localhost' (got: ${pattern})"
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

deploy() {
  if [[ -f "${STATE_FILE}" ]]; then
    teardown
  fi

  ensure_platform
  ensure_cli
  ensure_images

  SUFFIX="$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')"
  PROJECT_NAME="TaskFlow ${SUFFIX}"
  PROJECT_SLUG="taskflow-${SUFFIX}"

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
      taskflow-api) API_DEPLOYMENT_ID="${dep_id}" ;;
      taskflow-web) WEB_DEPLOYMENT_ID="${dep_id}" ;;
    esac
  done < <(extract_deployment_ids)

  [[ -n "${API_DEPLOYMENT_ID}" ]] || fail "taskflow-api Deployment id missing from apply"
  [[ -n "${WEB_DEPLOYMENT_ID}" ]] || fail "taskflow-web Deployment id missing from apply"
  echo "Deployments api=${API_DEPLOYMENT_ID} web=${WEB_DEPLOYMENT_ID}"

  assert_applications_ready
  wait_route_host "${API_HOST}" 90
  wait_route_host "${APP_HOST}" 90
  wait_host_http "${API_HOST}" "/health/ready" 200 60
  wait_host_http "${APP_HOST}" "/" 200 60

  # Optional: forge wait Ready when CLI supports it.
  if "${FORGE_BIN}" wait --help >/dev/null 2>&1; then
    forge wait "application/taskflow-api" --for=condition=Ready --timeout=2m ||
      fail "forge wait taskflow-api failed"
    forge wait "application/taskflow-web" --for=condition=Ready --timeout=2m ||
      fail "forge wait taskflow-web failed"
  fi

  write_state
  echo
  echo "demo 51 deploy READY"
  echo "  App:          http://${APP_HOST}:4000/"
  echo "  API:          http://${API_HOST}:4000/health/ready"
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
