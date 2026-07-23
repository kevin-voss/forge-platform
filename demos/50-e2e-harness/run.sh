#!/usr/bin/env bash
# Demo 50: harness self-test product (epic 50.07 gate).
# Usage:
#   demos/50-e2e-harness/run.sh          # deploy hello → Ready on hello.localhost
#   demos/50-e2e-harness/run.sh --down   # tear down product resources only
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/50-e2e-harness"
STATE_FILE="${DEMO_DIR}/.demo-state"

export FORGE_AUTH_MODE="${FORGE_AUTH_MODE:-dev}"
export FORGE_LIFECYCLE_OWNER="${FORGE_LIFECYCLE_OWNER:-control}"
export FORGE_RECONCILE_INTERVAL_MS="${FORGE_RECONCILE_INTERVAL_MS:-1000}"
export FORGE_READINESS_POLL_MS="${FORGE_READINESS_POLL_MS:-500}"
export FORGE_READINESS_MAX_WAIT_S="${FORGE_READINESS_MAX_WAIT_S:-90}"
export FORGE_RESOURCE_API_ENABLED="${FORGE_RESOURCE_API_ENABLED:-true}"
export FORGE_SECRETS_URL="${FORGE_SECRETS_URL:-disabled}"
export FORGE_OTEL_ENABLED="${FORGE_OTEL_ENABLED:-false}"
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
CONTROL_SERVICE="forge-control"
RUNTIME_SERVICE="forge-runtime"
GATEWAY_SERVICE="forge-gateway"
POSTGRES_SERVICE="postgres"
REGISTRY_SERVICE="registry"
CLI_DIR="${ROOT_DIR}/tools/forge-cli"
FORGE_BIN="${CLI_DIR}/forge"
REGISTRY="${FORGE_REGISTRY:-localhost:5000}"
IMAGE="${DEMO_IMAGE:-${REGISTRY}/demo-50-hello:v1}"
HOST="hello.localhost"
ENV_NAME="local"
APP_NAME="hello"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-demo-50.XXXXXX")"
CONFIG_HOME="${TMP_DIR}/xdg-config"
export XDG_CONFIG_HOME="${CONFIG_HOME}"
export CI="${CI:-1}"
export FORGE_PROFILE="${FORGE_PROFILE:-demo50}"
export FORGE_ENDPOINT="${FORGE_ENDPOINT:-${CONTROL_URL}}"
mkdir -p "${CONFIG_HOME}"

fail() {
  echo "Demo 50 failed: $*" >&2
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
DEPLOYMENT_ID=${DEPLOYMENT_ID}
IMAGE=${IMAGE}
EOF
}

read_state() {
  [[ -f "${STATE_FILE}" ]] || return 1
  # shellcheck disable=SC1090
  source "${STATE_FILE}"
}

teardown() {
  echo "Tearing down demo 50 harness self-test..."
  if read_state; then
    if [[ -n "${DEPLOYMENT_ID:-}" ]]; then
      curl --silent --show-error -X DELETE \
        "${CONTROL_URL}/v1/deployments/${DEPLOYMENT_ID}" >/dev/null 2>&1 || true
      docker ps -aq --filter "label=forge.deployment_id=${DEPLOYMENT_ID}" \
        --filter "label=forge.managed=true" |
        while read -r cid; do
          [[ -n "${cid}" ]] || continue
          docker rm -f "${cid}" >/dev/null 2>&1 || true
        done
    fi
    rm -f "${STATE_FILE}"
  else
    echo "  no .demo-state; best-effort cleanup of demo=50 containers"
    docker ps -aq --filter "label=forge.managed=true" --filter "label=demo=50" |
      while read -r cid; do
        [[ -n "${cid}" ]] || continue
        docker rm -f "${cid}" >/dev/null 2>&1 || true
      done
  fi
  echo "Teardown complete."
}

ensure_platform() {
  echo "Ensuring Postgres, registry, Control, Runtime, Gateway..."
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
  local auth_mode pattern
  auth_mode="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_AUTH_MODE 2>/dev/null || true)"
  pattern="$(docker exec "${GATEWAY_SERVICE}" printenv FORGE_HOST_PATTERN 2>/dev/null || true)"
  if [[ "${auth_mode}" != "dev" ]]; then
    need_recreate=1
  fi
  if [[ "${pattern}" != *'{service}.localhost'* ]]; then
    need_recreate=1
  fi

  if [[ "${need_recreate}" -eq 1 ]]; then
    echo "Recreating Control/Runtime/Gateway with demo 50 overlay (auth=dev, host={service}.localhost)..."
    "${COMPOSE[@]}" up -d --force-recreate \
      "${CONTROL_SERVICE}" "${RUNTIME_SERVICE}" "${GATEWAY_SERVICE}"
  else
    echo "Control/Gateway already configured for demo 50; ensuring they are up..."
    "${COMPOSE[@]}" up -d "${CONTROL_SERVICE}" "${RUNTIME_SERVICE}" "${GATEWAY_SERVICE}"
  fi

  wait_http "${CONTROL_URL}/health/ready" "Control"
  wait_http "${RUNTIME_URL}/health/ready" "Runtime"
  wait_http "${GATEWAY_URL}/health/ready" "Gateway"

  pattern="$(docker exec "${GATEWAY_SERVICE}" printenv FORGE_HOST_PATTERN 2>/dev/null || true)"
  [[ "${pattern}" == *'{service}.localhost'* ]] ||
    fail "gateway FORGE_HOST_PATTERN must be '{service}.localhost' (got: ${pattern})"
}

ensure_image() {
  echo "Building and pushing ${IMAGE} ..."
  docker build -t "${IMAGE}" "${DEMO_DIR}" || fail "docker build failed"
  docker push "${IMAGE}" || fail "docker push failed"
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

deploy() {
  # Clean prior run if state left behind (idempotent re-deploy).
  if [[ -f "${STATE_FILE}" ]]; then
    teardown
  fi

  ensure_platform
  ensure_cli
  ensure_image

  SUFFIX="$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')"
  PROJECT_NAME="Harness Self-Test ${SUFFIX}"
  PROJECT_SLUG="harness-${SUFFIX}"

  echo "Rendering forge.yaml → apply (project=${PROJECT_SLUG})..."
  PROJECT_NAME="${PROJECT_NAME}" PROJECT_SLUG="${PROJECT_SLUG}" IMAGE="${IMAGE}" \
    envsubst '${PROJECT_NAME} ${PROJECT_SLUG} ${IMAGE}' \
    <"${DEMO_DIR}/forge.yaml" >"${TMP_DIR}/forge.yaml"

  forge_json "${TMP_DIR}/apply.json" apply -f "${TMP_DIR}/forge.yaml"
  DEPLOYMENT_ID="$(python3 - "${TMP_DIR}/apply.json" <<'PY'
import json, sys
body = json.load(open(sys.argv[1]))
for r in body.get("results", []):
    if r.get("kind") == "Deployment" and r.get("resource"):
        meta = r["resource"].get("metadata") or {}
        print(meta.get("id") or "")
        break
else:
    sys.exit("Deployment id missing from apply response")
PY
)" || fail "could not read Deployment id from apply"
  [[ -n "${DEPLOYMENT_ID}" ]] || fail "empty Deployment id from apply"
  echo "Deployment id=${DEPLOYMENT_ID}"

  wait_deployment_status "${DEPLOYMENT_ID}" "deployed" 120
  wait_route_host "${HOST}" 90
  wait_host_http "${HOST}" "/" 200 60

  write_state
  echo
  echo "demo 50 deploy READY"
  echo "  Host:         http://${HOST}:4000/"
  echo "  Image:        ${IMAGE}"
  echo "  Deployment:   ${DEPLOYMENT_ID}"
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
