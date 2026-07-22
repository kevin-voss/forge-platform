#!/usr/bin/env bash
# Demo 04: CLI → Control → Runtime deploy of the Go demo image (epic gate).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
COMPOSE=(docker compose -f "${ROOT_DIR}/compose.yaml" --project-directory "${ROOT_DIR}")
CONTROL_URL="${FORGE_CONTROL_URL:-http://127.0.0.1:4001}"
RUNTIME_URL="${FORGE_RUNTIME_URL:-http://127.0.0.1:4102}"
CONTROL_SERVICE="forge-control"
RUNTIME_SERVICE="forge-runtime"
POSTGRES_SERVICE="postgres"
REGISTRY_SERVICE="registry"
CLI_DIR="${ROOT_DIR}/tools/forge-cli"
FORGE_BIN="${CLI_DIR}/forge"
GO_APP_DIR="${ROOT_DIR}/demos/01-container-runtime/apps/go"
DEMO_IMAGE="${DEMO_IMAGE:-localhost:5000/demo-go:latest}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-runtime-demo.XXXXXX")"
CONFIG_HOME="${TMP_DIR}/xdg-config"
export XDG_CONFIG_HOME="${CONFIG_HOME}"
export CI=1
export FORGE_PROFILE="${FORGE_PROFILE:-demo}"
export FORGE_ENDPOINT="${FORGE_ENDPOINT:-${CONTROL_URL}}"
# Faster converge for the acceptance gate (Compose interpolates this).
export FORGE_RECONCILE_INTERVAL_SECONDS="${FORGE_RECONCILE_INTERVAL_SECONDS:-3}"
mkdir -p "${CONFIG_HOME}"

TRACKED_DEPLOYMENTS=()

cleanup() {
  local dep
  if ((${#TRACKED_DEPLOYMENTS[@]} > 0)); then
    for dep in "${TRACKED_DEPLOYMENTS[@]}"; do
      [[ -n "${dep}" ]] || continue
      curl --silent --show-error -X DELETE "${CONTROL_URL}/v1/deployments/${dep}" >/dev/null 2>&1 || true
      docker rm -f "forge-${dep}" >/dev/null 2>&1 || true
    done
  fi
  "${COMPOSE[@]}" stop "${RUNTIME_SERVICE}" "${CONTROL_SERVICE}" >/dev/null 2>&1 || true
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

fail() {
  echo "Demo 04 failed: $*" >&2
  echo "--- ${RUNTIME_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=120 "${RUNTIME_SERVICE}" >&2 || true
  echo "--- ${CONTROL_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=80 "${CONTROL_SERVICE}" >&2 || true
  echo "--- docker ps -a (forge-*) ---" >&2
  docker ps -a --filter name=forge- --format 'table {{.Names}}\t{{.Status}}\t{{.Image}}' >&2 || true
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
  echo "Ensuring ${DEMO_IMAGE} is in the local registry..."
  docker image inspect forge/demo-go-api:local >/dev/null 2>&1 ||
    docker build -t forge/demo-go-api:local "${GO_APP_DIR}" ||
    fail "could not build forge/demo-go-api:local"
  docker tag forge/demo-go-api:local "${DEMO_IMAGE}" || fail "could not tag ${DEMO_IMAGE}"
  docker push "${DEMO_IMAGE}" >/dev/null || fail "could not push ${DEMO_IMAGE}"
}

create_hierarchy() {
  local suffix="$1"
  forge_json "${TMP_DIR}/project.json" project create --name "demo-runtime-${suffix}"
  PROJECT_ID="$(read_id "${TMP_DIR}/project.json")"
  forge_json "${TMP_DIR}/environment.json" env create --project "${PROJECT_ID}" --name development
  ENVIRONMENT_ID="$(read_id "${TMP_DIR}/environment.json")"
  forge_json "${TMP_DIR}/application.json" app create --project "${PROJECT_ID}" --name web
  APPLICATION_ID="$(read_id "${TMP_DIR}/application.json")"
  forge_json "${TMP_DIR}/service.json" service create --app "${APPLICATION_ID}" --name api --port 8080
  SERVICE_ID="$(read_id "${TMP_DIR}/service.json")"
}

wait_deployment_status() {
  local deployment_id="$1" expected="$2" attempts="${3:-60}"
  local status=""
  echo "Waiting for deployment ${deployment_id} status=${expected} ..."
  for _ in $(seq 1 "${attempts}"); do
    forge_json "${TMP_DIR}/dep-status.json" deployment status "${deployment_id}"
    status="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["status"])' "${TMP_DIR}/dep-status.json")"
    if [[ "${status}" == "${expected}" ]]; then
      echo "  status=${status}"
      return 0
    fi
    sleep 2
  done
  fail "deployment ${deployment_id} status=${status:-unknown}, want ${expected}"
}

wait_container() {
  local deployment_id="$1" attempts="${2:-45}"
  echo "Waiting for managed container forge-${deployment_id} ..."
  for _ in $(seq 1 "${attempts}"); do
    if docker ps --filter "label=forge.deployment_id=${deployment_id}" \
      --filter "label=forge.managed=true" -q | grep -q .; then
      return 0
    fi
    sleep 2
  done
  fail "container for deployment ${deployment_id} did not appear"
}

assert_no_container() {
  local deployment_id="$1" attempts="${2:-45}"
  echo "Waiting for container removal for ${deployment_id} ..."
  for _ in $(seq 1 "${attempts}"); do
    if ! docker ps -aq --filter "label=forge.deployment_id=${deployment_id}" \
      --filter "label=forge.managed=true" | grep -q .; then
      return 0
    fi
    sleep 2
  done
  fail "container for deployment ${deployment_id} still present"
}

host_port_for() {
  local deployment_id="$1"
  curl --fail --silent --show-error "${RUNTIME_URL}/v1/node/state" |
    DEPLOYMENT_ID="${deployment_id}" python3 -c '
import json, os, sys
state = json.load(sys.stdin)
did = os.environ["DEPLOYMENT_ID"]
for w in state.get("workloads", []):
    if w.get("deploymentId") == did and w.get("hostPort"):
        print(w["hostPort"])
        sys.exit(0)
sys.exit("hostPort not found for " + did)
' || fail "could not read hostPort for ${deployment_id} from Runtime node state"
}

assert_labels() {
  local deployment_id="$1" node_id="$2"
  docker inspect "forge-${deployment_id}" --format '{{json .Config.Labels}}' |
    DEPLOYMENT_ID="${deployment_id}" NODE_ID="${node_id}" python3 -c '
import json, os, sys
labels = json.load(sys.stdin)
assert labels.get("forge.deployment_id") == os.environ["DEPLOYMENT_ID"], labels
assert labels.get("forge.managed") == "true", labels
assert labels.get("forge.node_id") == os.environ["NODE_ID"], labels
' || fail "container labels were not deterministic for ${deployment_id}"
}

echo "== Demo 04: Forge Runtime =="
echo "Building forge CLI..."
make -C "${CLI_DIR}" build || fail "CLI build failed"
[[ -x "${FORGE_BIN}" ]] || fail "forge binary missing at ${FORGE_BIN}"

echo "Starting PostgreSQL, registry, Control, and Runtime..."
"${COMPOSE[@]}" up -d "${POSTGRES_SERVICE}" "${REGISTRY_SERVICE}"
"${COMPOSE[@]}" up -d --build --force-recreate "${CONTROL_SERVICE}"
wait_http "${CONTROL_URL}/health/ready" "Control"
"${COMPOSE[@]}" up -d --build --force-recreate "${RUNTIME_SERVICE}"
wait_http "${RUNTIME_URL}/health/ready" "Runtime"

ensure_demo_image

echo "Configuring CLI profile ${FORGE_PROFILE} -> ${FORGE_ENDPOINT}..."
forge config set endpoint "${FORGE_ENDPOINT}" --profile "${FORGE_PROFILE}"
forge config use "${FORGE_PROFILE}"

SUFFIX="$(date +%s)-$$"
create_hierarchy "${SUFFIX}"

echo "Creating happy-path deployment via forge..."
IDEM_KEY="demo-04-${SUFFIX}-happy"
forge_json "${TMP_DIR}/deployment.json" deployment create \
  --service "${SERVICE_ID}" \
  --image "${DEMO_IMAGE}" \
  --env "${ENVIRONMENT_ID}" \
  --replicas 1 \
  --idempotency-key "${IDEM_KEY}"
DEPLOYMENT_ID="$(read_id "${TMP_DIR}/deployment.json")"
track_deployment "${DEPLOYMENT_ID}"

echo "Idempotency: recreate with the same key must return the same deployment..."
forge_json "${TMP_DIR}/deployment-again.json" deployment create \
  --service "${SERVICE_ID}" \
  --image "${DEMO_IMAGE}" \
  --env "${ENVIRONMENT_ID}" \
  --replicas 1 \
  --idempotency-key "${IDEM_KEY}"
AGAIN_ID="$(read_id "${TMP_DIR}/deployment-again.json")"
[[ "${AGAIN_ID}" == "${DEPLOYMENT_ID}" ]] ||
  fail "idempotent create returned ${AGAIN_ID}, want ${DEPLOYMENT_ID}"

wait_container "${DEPLOYMENT_ID}"
wait_deployment_status "${DEPLOYMENT_ID}" "active"

NODE_ID="$(curl --fail --silent --show-error "${RUNTIME_URL}/v1/node" |
  python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')" ||
  fail "could not read Runtime node id"
assert_labels "${DEPLOYMENT_ID}" "${NODE_ID}"

CONTAINER_COUNT="$(docker ps -aq --filter "label=forge.deployment_id=${DEPLOYMENT_ID}" \
  --filter "label=forge.managed=true" | wc -l | tr -d ' ')"
[[ "${CONTAINER_COUNT}" == "1" ]] ||
  fail "expected exactly one container for ${DEPLOYMENT_ID}, found ${CONTAINER_COUNT}"

HOST_PORT="$(host_port_for "${DEPLOYMENT_ID}")"
echo "Curling app on host port ${HOST_PORT}..."
curl --fail --silent --show-error "http://127.0.0.1:${HOST_PORT}/health/live" | grep -Eqi 'ok|live' ||
  fail "workload /health/live failed on port ${HOST_PORT}"
curl --fail --silent --show-error "http://127.0.0.1:${HOST_PORT}/" >/dev/null ||
  fail "workload root endpoint failed on port ${HOST_PORT}"

echo "Reading logs via Runtime..."
LOGS="$(curl --fail --silent --show-error \
  "${RUNTIME_URL}/v1/workloads/${DEPLOYMENT_ID}/logs?tail=20")" ||
  fail "Runtime logs fetch failed"
echo "${LOGS}" | grep -Eq '\[(stdout|stderr)\]' ||
  fail "Runtime logs missing stream markers: ${LOGS}"

echo "Deleting deployment ${DEPLOYMENT_ID} via Control API..."
DEL_CODE="$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  -X DELETE "${CONTROL_URL}/v1/deployments/${DEPLOYMENT_ID}")" ||
  fail "DELETE deployment did not complete"
[[ "${DEL_CODE}" == "204" ]] || fail "DELETE deployment returned HTTP ${DEL_CODE}"
assert_no_container "${DEPLOYMENT_ID}"

echo "Negative case: bad image must converge to failed..."
forge_json "${TMP_DIR}/bad-deployment.json" deployment create \
  --service "${SERVICE_ID}" \
  --image "localhost:5000/definitely-missing-demo-04:latest" \
  --env "${ENVIRONMENT_ID}" \
  --replicas 1
BAD_ID="$(read_id "${TMP_DIR}/bad-deployment.json")"
track_deployment "${BAD_ID}"
wait_deployment_status "${BAD_ID}" "failed" 45
! docker ps -aq --filter "label=forge.deployment_id=${BAD_ID}" \
  --filter "label=forge.managed=true" | grep -q . ||
  fail "failed bad-image deploy left a managed container"
curl --silent --show-error -X DELETE "${CONTROL_URL}/v1/deployments/${BAD_ID}" >/dev/null || true

echo
echo "Demo 04 passed."
echo "  Project:     ${PROJECT_ID}"
echo "  Environment: ${ENVIRONMENT_ID}"
echo "  Application: ${APPLICATION_ID}"
echo "  Service:     ${SERVICE_ID}"
echo "  Deployment:  ${DEPLOYMENT_ID} (deleted after active path)"
echo "  Node:        ${NODE_ID}"
echo "  Image:       ${DEMO_IMAGE}"
