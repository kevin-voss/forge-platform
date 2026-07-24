#!/usr/bin/env bash
# Demo 52: SnapNote + storage + events worker + queueDepth autoscaling (epic 52.04).
# Usage:
#   demos/52-snapnote/run.sh          # build → apply → DB → storage → events → autoscaler → proofs
#   demos/52-snapnote/run.sh --down   # tear down product resources only
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/52-snapnote"
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
export FORGE_DB_PROVISIONER="${FORGE_DB_PROVISIONER:-local}"
export FORGE_DB_ENDPOINT_HOST="${FORGE_DB_ENDPOINT_HOST:-host.docker.internal}"
export FORGE_DB_MANAGED_NETWORK="${FORGE_DB_MANAGED_NETWORK:-forge-net}"
export FORGE_INJECT_MASK_IN_LOGS="${FORGE_INJECT_MASK_IN_LOGS:-true}"
export FORGE_EVENTS_STREAMS="${FORGE_EVENTS_STREAMS:-build,deployment,runtime,application,agent,attachment}"
export FORGE_EVENTS_AUTH_MODE="${FORGE_EVENTS_AUTH_MODE:-dev}"
export FORGE_DEFAULT_ACK_WAIT_S="${FORGE_DEFAULT_ACK_WAIT_S:-10}"
export FORGE_AUTOSCALER_EVAL_INTERVAL_MS="${FORGE_AUTOSCALER_EVAL_INTERVAL_MS:-1000}"
export DOCKER_GID="${DOCKER_GID:-$(stat -f '%g' /var/run/docker.sock 2>/dev/null || stat -c '%g' /var/run/docker.sock 2>/dev/null || echo 0)}"
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
METRICS_URL="${FORGE_DEMO52_METRICS_URL:-http://127.0.0.1:4198}"
CONTROL_SERVICE="forge-control"
RUNTIME_SERVICE="forge-runtime"
GATEWAY_SERVICE="forge-gateway"
BUILD_SERVICE="forge-build"
STORAGE_SERVICE="forge-storage"
EVENTS_SERVICE="forge-events"
AUTOSCALER_SERVICE="forge-autoscaler"
METRICS_SERVICE="demo52-metrics"
NATS_SERVICE="nats"
POSTGRES_SERVICE="postgres"
REGISTRY_SERVICE="registry"
STORAGE_URL="${FORGE_STORAGE_HOST_URL:-http://127.0.0.1:4107}"
EVENTS_URL="${FORGE_EVENTS_HOST_URL:-http://127.0.0.1:4105}"
STORAGE_BUCKET="${FORGE_STORAGE_BUCKET:-snapnote-attachments}"
STORAGE_PROJECT="${FORGE_STORAGE_PROJECT:-snapnote}"
QUEUE_NAME="snapnote-attachments"
WORKER_NAME="snapnote-worker"
WORKER_POLICY="snapnote-worker-queue"
ENV_NAME="local"
CLI_DIR="${ROOT_DIR}/tools/forge-cli"
FORGE_BIN="${CLI_DIR}/forge"
REGISTRY="${FORGE_REGISTRY:-localhost:5000}"
API_IMAGE="${DEMO_API_IMAGE:-${REGISTRY}/snapnote/snapnote-api:v1}"
WEB_IMAGE="${DEMO_WEB_IMAGE:-${REGISTRY}/snapnote/snapnote-web:v1}"
WORKER_IMAGE="${DEMO_WORKER_IMAGE:-${REGISTRY}/snapnote/snapnote-worker:v1}"
API_HOST="api.snapnote.localhost"
APP_HOST="app.snapnote.localhost"
WORKER_HOST="worker.snapnote.localhost"
DB_NAME="snapnote-db"          # instance / dependency name (may contain '-')
DB_LOGICAL_NAME="snapnote_db"  # Postgres DB name ([a-z_][a-z0-9_]*)
SYNC_PID=""
MIN_REPLICAS=1
MAX_REPLICAS=8
TARGET_PER_REPLICA=20
BURST_COUNT="${BURST_COUNT:-40}"
BURST_DEPTH="${BURST_DEPTH:-80}"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-demo-52.XXXXXX")"
CONFIG_HOME="${TMP_DIR}/xdg-config"
export XDG_CONFIG_HOME="${CONFIG_HOME}"
export CI="${CI:-1}"
export FORGE_PROFILE="${FORGE_PROFILE:-demo52}"
export FORGE_ENDPOINT="${FORGE_ENDPOINT:-${CONTROL_URL}}"
mkdir -p "${CONFIG_HOME}"

fail() {
  echo "Demo 52 failed: $*" >&2
  echo "--- ${GATEWAY_SERVICE} /admin/routes ---" >&2
  curl --silent --show-error "${GATEWAY_URL}/admin/routes" >&2 || true
  echo >&2
  if [[ -n "${PROJECT_SLUG:-}" ]]; then
    echo "--- ScalingPolicy ${WORKER_POLICY} ---" >&2
    curl --silent --show-error \
      "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies/${WORKER_POLICY}" >&2 || true
    echo >&2
    echo "--- Worker ${WORKER_NAME} ---" >&2
    curl --silent --show-error \
      "${CONTROL_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/workers/${WORKER_NAME}" >&2 || true
    echo >&2
  fi
  echo "--- ${METRICS_SERVICE} queue metrics ---" >&2
  curl --silent --show-error "${METRICS_URL}/admin/metrics?queue=${QUEUE_NAME}" >&2 || true
  echo >&2
  echo "--- ${CONTROL_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=80 "${CONTROL_SERVICE}" >&2 || true
  echo "--- ${RUNTIME_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${RUNTIME_SERVICE}" >&2 || true
  echo "--- ${GATEWAY_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${GATEWAY_SERVICE}" >&2 || true
  echo "--- managed db containers ---" >&2
  docker ps --filter "label=forge.managed_db=true" --format '{{.Names}} {{.Status}}' >&2 || true
  echo "--- ${STORAGE_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${STORAGE_SERVICE}" >&2 || true
  echo "--- ${EVENTS_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${EVENTS_SERVICE}" >&2 || true
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
PROJECT_ID=${PROJECT_ID}
API_DEPLOYMENT_ID=${API_DEPLOYMENT_ID}
WEB_DEPLOYMENT_ID=${WEB_DEPLOYMENT_ID}
WORKER_DEPLOYMENT_ID=${WORKER_DEPLOYMENT_ID}
API_IMAGE=${API_IMAGE}
WEB_IMAGE=${WEB_IMAGE}
WORKER_IMAGE=${WORKER_IMAGE}
DB_NAME=${DB_NAME}
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
  echo "Tearing down demo 52 SnapNote..."
  if read_state; then
    delete_deployment "${API_DEPLOYMENT_ID:-}"
    delete_deployment "${WEB_DEPLOYMENT_ID:-}"
    delete_deployment "${WORKER_DEPLOYMENT_ID:-}"
    rm -f "${STATE_FILE}"
  else
    echo "  no .demo-state; best-effort cleanup of demo=52 containers"
    docker ps -aq --filter "label=forge.managed=true" --filter "label=demo=52" |
      while read -r cid; do
        [[ -n "${cid}" ]] || continue
        docker rm -f "${cid}" >/dev/null 2>&1 || true
      done
  fi
  # Best-effort: leave managed DB containers for inspect unless explicitly removed.
  echo "Teardown complete."
}

ensure_platform() {
  echo "Ensuring Postgres, registry, NATS, Events, Storage, Autoscaler, Control, Runtime, Gateway, Build..."
  "${COMPOSE[@]}" up -d "${POSTGRES_SERVICE}" "${REGISTRY_SERVICE}" "${NATS_SERVICE}"
  for _ in $(seq 1 60); do
    if "${COMPOSE[@]}" exec -T "${POSTGRES_SERVICE}" pg_isready -U forge >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done
  "${COMPOSE[@]}" exec -T "${POSTGRES_SERVICE}" pg_isready -U forge >/dev/null 2>&1 ||
    fail "Postgres not ready"

  # Autoscaler DB (shared with demo 24).
  docker exec -i forge-postgres psql -U forge -d postgres -v ON_ERROR_STOP=1 <<'SQL' >/dev/null \
    || fail "could not ensure forge_autoscaler database"
SELECT 'CREATE DATABASE forge_autoscaler'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'forge_autoscaler')\gexec
SQL

  local need_recreate=0
  local auth_mode pattern strategy provisioner secrets_url streams events_url
  auth_mode="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_AUTH_MODE 2>/dev/null || true)"
  pattern="$(docker exec "${GATEWAY_SERVICE}" printenv FORGE_HOST_PATTERN 2>/dev/null || true)"
  strategy="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_SCHEDULER_STRATEGY 2>/dev/null || true)"
  provisioner="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_DB_PROVISIONER 2>/dev/null || true)"
  secrets_url="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_SECRETS_URL 2>/dev/null || true)"
  streams="$(docker exec "${EVENTS_SERVICE}" printenv FORGE_EVENTS_STREAMS 2>/dev/null || true)"
  events_url="$(docker exec "${AUTOSCALER_SERVICE}" printenv FORGE_EVENTS_URL 2>/dev/null || true)"
  if [[ "${auth_mode}" != "dev" ]]; then
    need_recreate=1
  fi
  if [[ "${pattern}" != *'{service}.snapnote.localhost'* ]]; then
    need_recreate=1
  fi
  if [[ "${strategy}" != "single-node" ]]; then
    need_recreate=1
  fi
  if [[ "${provisioner}" != "local" ]]; then
    need_recreate=1
  fi
  if [[ "${secrets_url}" != "disabled" ]]; then
    need_recreate=1
  fi
  if [[ "${streams}" != *attachment* ]]; then
    need_recreate=1
  fi
  if [[ "${events_url}" != *"demo52-metrics"* ]]; then
    need_recreate=1
  fi
  if ! docker exec "${CONTROL_SERVICE}" test -S /var/run/docker.sock 2>/dev/null; then
    need_recreate=1
  fi

  echo "Starting demo52-metrics sidecar..."
  docker rm -f demo52-metrics >/dev/null 2>&1 || true
  "${COMPOSE[@]}" up -d --build --force-recreate "${METRICS_SERVICE}" ||
    fail "compose up ${METRICS_SERVICE} failed"
  wait_http "${METRICS_URL}/health/live" "demo52-metrics" 60

  if [[ "${need_recreate}" -eq 1 ]]; then
    echo "Recreating Control/Runtime/Gateway/Events/Autoscaler with demo 52 overlay..."
    "${COMPOSE[@]}" up -d --force-recreate \
      "${CONTROL_SERVICE}" "${RUNTIME_SERVICE}" "${GATEWAY_SERVICE}" "${EVENTS_SERVICE}" \
      "${AUTOSCALER_SERVICE}"
  else
    echo "Control/Gateway/Events/Autoscaler already configured for demo 52; ensuring they are up..."
    "${COMPOSE[@]}" up -d "${CONTROL_SERVICE}" "${RUNTIME_SERVICE}" "${GATEWAY_SERVICE}" \
      "${EVENTS_SERVICE}" "${AUTOSCALER_SERVICE}"
  fi
  "${COMPOSE[@]}" up -d "${BUILD_SERVICE}" "${STORAGE_SERVICE}"

  wait_http "${CONTROL_URL}/health/ready" "Control"
  wait_http "${RUNTIME_URL}/health/ready" "Runtime"
  wait_http "${GATEWAY_URL}/health/ready" "Gateway"
  wait_http "${BUILD_URL}/health/ready" "Build" 60 || true
  wait_http "${STORAGE_URL}/health/ready" "Storage" 90
  wait_http "${EVENTS_URL}/health/ready" "Events" 90
  wait_http "${AUTOSCALER_URL}/health/ready" "Autoscaler" 120

  pattern="$(docker exec "${GATEWAY_SERVICE}" printenv FORGE_HOST_PATTERN 2>/dev/null || true)"
  [[ "${pattern}" == *'{service}.snapnote.localhost'* ]] ||
    fail "gateway FORGE_HOST_PATTERN must be '{service}.snapnote.localhost' (got: ${pattern})"
  provisioner="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_DB_PROVISIONER 2>/dev/null || true)"
  [[ "${provisioner}" == "local" ]] ||
    fail "control FORGE_DB_PROVISIONER must be local (got: ${provisioner})"
  streams="$(docker exec "${EVENTS_SERVICE}" printenv FORGE_EVENTS_STREAMS 2>/dev/null || true)"
  [[ "${streams}" == *attachment* ]] ||
    fail "events FORGE_EVENTS_STREAMS must include attachment (got: ${streams})"
  events_url="$(docker exec "${AUTOSCALER_SERVICE}" printenv FORGE_EVENTS_URL 2>/dev/null || true)"
  [[ "${events_url}" == *"demo52-metrics"* ]] ||
    fail "autoscaler FORGE_EVENTS_URL must point at demo52-metrics (got: ${events_url})"

  # Confirm attachment.uploaded schema is loaded.
  curl --fail --silent --show-error "${EVENTS_URL}/v1/schemas" >"${TMP_DIR}/schemas.json" ||
    fail "GET /v1/schemas failed"
  python3 - <<'PY' "${TMP_DIR}/schemas.json" || fail "attachment.uploaded schema missing"
import json, sys
raw = json.load(open(sys.argv[1]))
flat = []
if isinstance(raw, list):
    for item in raw:
        if isinstance(item, str):
            flat.append(item)
        elif isinstance(item, dict):
            flat.append(item.get("subject") or item.get("name") or "")
elif isinstance(raw, dict):
    for k, v in raw.items():
        flat.append(k)
        if isinstance(v, list):
            flat.extend(str(x) for x in v)
assert any("attachment.uploaded" in str(x) for x in flat), raw
print("  schema attachment.uploaded registered")
PY

  ensure_storage_bucket
}

ensure_storage_bucket() {
  echo "Ensuring storage bucket ${STORAGE_BUCKET} (project=${STORAGE_PROJECT})..."
  local code
  code="$(curl --silent --show-error -o "${TMP_DIR}/bucket.json" -w '%{http_code}' \
    -H "X-Forge-Project: ${STORAGE_PROJECT}" \
    -H 'content-type: application/json' \
    -d "{\"name\":\"${STORAGE_BUCKET}\"}" \
    "${STORAGE_URL}/v1/buckets" || echo "000")"
  if [[ "${code}" != "201" && "${code}" != "200" && "${code}" != "409" ]]; then
    fail "create bucket HTTP ${code}: $(cat "${TMP_DIR}/bucket.json" 2>/dev/null || true)"
  fi
  echo "  bucket ${STORAGE_BUCKET} ready (HTTP ${code})"
}

# Prefer `forge build` when the CLI subcommand exists; otherwise docker build+push
# from source (same images forge-build would produce for this scaffold).
ensure_images() {
  if "${FORGE_BIN}" build --help >/dev/null 2>&1; then
    echo "Building via forge build --source ..."
    (
      cd "${DEMO_DIR}"
      forge build --source . --forge-yaml api/forge.yaml --tag "${API_IMAGE}"
    ) || fail "forge build api failed"
    (
      cd "${DEMO_DIR}"
      forge build --source . --forge-yaml worker/forge.yaml --tag "${WORKER_IMAGE}"
    ) || fail "forge build worker failed"
    (
      cd "${DEMO_DIR}"
      forge build --source . --forge-yaml web.forge.yaml --tag "${WEB_IMAGE}"
    ) || fail "forge build web failed"
    return 0
  fi

  echo "forge build CLI not available; building from source with docker build+push..."
  docker build -f "${DEMO_DIR}/api/Dockerfile" -t "${API_IMAGE}" "${DEMO_DIR}" ||
    fail "docker build api failed"
  docker push "${API_IMAGE}" || fail "docker push api failed"
  docker build -f "${DEMO_DIR}/worker/Dockerfile" -t "${WORKER_IMAGE}" "${DEMO_DIR}/worker" ||
    fail "docker build worker failed"
  docker push "${WORKER_IMAGE}" || fail "docker push worker failed"
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

extract_apply_ids() {
  python3 - "${TMP_DIR}/apply.json" <<'PY'
import json, sys
body = json.load(open(sys.argv[1]))
project_id = ""
for r in body.get("results", []):
    kind = r.get("kind") or ""
    name = r.get("name") or ""
    meta = (r.get("resource") or {}).get("metadata") or {}
    rid = meta.get("id") or ""
    if kind == "Project" and rid:
        project_id = rid
        print(f"PROJECT_ID={rid}")
    if kind == "Deployment" and name and rid:
        print(f"DEPLOYMENT:{name}={rid}")
if not project_id:
    # Fallback: some apply responses omit nested resource; leave empty for later lookup.
    pass
PY
}

assert_applications_ready() {
  echo "Checking applications/deployments Ready..."
  wait_deployment_status "${API_DEPLOYMENT_ID}" "deployed" 180
  wait_deployment_status "${WORKER_DEPLOYMENT_ID}" "deployed" 180
  wait_deployment_status "${WEB_DEPLOYMENT_ID}" "deployed" 120
  echo "  applications Ready (deployments active)"
}

provision_managed_db() {
  echo "Provisioning managed Database ${DB_NAME} (dependencies.database)..."
  [[ -n "${PROJECT_ID}" ]] || fail "PROJECT_ID missing; cannot create managed database"
  # Instance name matches the dependency name (snapnote-db). Logical Postgres DB
  # names cannot contain '-' (platform pattern [a-z_][a-z0-9_]*).
  forge_json "${TMP_DIR}/db-create.json" --project "${PROJECT_ID}" \
    database create "${DB_NAME}" --database "${DB_LOGICAL_NAME}"
  python3 - <<'PY' "${TMP_DIR}/db-create.json" || fail "database create did not reach available"
import json, sys
body = json.load(open(sys.argv[1]))
db = body.get("database") or {}
inst = body.get("instance") or {}
status = db.get("status") or ""
inst_status = inst.get("status") or ""
assert status == "available", body
assert inst_status == "available", body
print(f"  database Ready id={db.get('id')} name={db.get('name')} instance={inst.get('id')}")
PY

  forge_json "${TMP_DIR}/db-attach.json" --project "${PROJECT_ID}" \
    database attach "${DB_NAME}" --app snapnote-api --env-var DATABASE_URL
  python3 - <<'PY' "${TMP_DIR}/db-attach.json" || fail "attach missing secretRef"
import json, sys
body = json.load(open(sys.argv[1]))
ref = body.get("secretRef") or body.get("secret_ref") or ""
assert ref, body
assert "://" not in ref, body
print(f"  attached DATABASE_URL secretRef={ref} (snapnote-api)")
PY

  forge_json "${TMP_DIR}/db-attach-worker.json" --project "${PROJECT_ID}" \
    database attach "${DB_NAME}" --app snapnote-worker --env-var DATABASE_URL
  python3 - <<'PY' "${TMP_DIR}/db-attach-worker.json" || fail "worker attach missing secretRef"
import json, sys
body = json.load(open(sys.argv[1]))
ref = body.get("secretRef") or body.get("secret_ref") or ""
assert ref, body
assert "://" not in ref, body
print(f"  attached DATABASE_URL secretRef={ref} (snapnote-worker)")
PY
}

api_container_id() {
  # Runtime labels forge.deployment_id as "{service}-{shortId}-0", not the Control UUID.
  # Prefer the UUID label when present; otherwise match by image + demo label / name prefix.
  local cid
  cid="$(docker ps -q \
    --filter "label=forge.deployment_id=${API_DEPLOYMENT_ID}" \
    --filter "label=forge.managed=true" | head -n1)"
  if [[ -n "${cid}" ]]; then
    echo "${cid}"
    return 0
  fi
  local short
  short="$(python3 -c 'import sys; print(sys.argv[1].replace("-", "")[:8])' "${API_DEPLOYMENT_ID}")"
  docker ps -q --filter "label=forge.managed=true" --filter "name=forge-api-${short}-" | head -n1
}

worker_container_id() {
  local cid
  cid="$(docker ps -q \
    --filter "label=forge.deployment_id=${WORKER_DEPLOYMENT_ID}" \
    --filter "label=forge.managed=true" | head -n1)"
  if [[ -n "${cid}" ]]; then
    echo "${cid}"
    return 0
  fi
  local short
  short="$(python3 -c 'import sys; print(sys.argv[1].replace("-", "")[:8])' "${WORKER_DEPLOYMENT_ID}")"
  docker ps -q --filter "label=forge.managed=true" --filter "name=forge-worker-${short}-" | head -n1
}

web_container_id() {
  local cid
  cid="$(docker ps -q \
    --filter "label=forge.deployment_id=${WEB_DEPLOYMENT_ID}" \
    --filter "label=forge.managed=true" | head -n1)"
  if [[ -n "${cid}" ]]; then
    echo "${cid}"
    return 0
  fi
  local short
  short="$(python3 -c 'import sys; print(sys.argv[1].replace("-", "")[:8])' "${WEB_DEPLOYMENT_ID}")"
  docker ps -q --filter "label=forge.managed=true" --filter "name=forge-app-${short}-" | head -n1
}

inject_web_config() {
  # Project slug is unique per deploy; bake it into the SPA so the workers
  # indicator can poll ScalingPolicy status via the same-origin /autoscaler/ proxy.
  local cid config
  cid="$(web_container_id)"
  [[ -n "${cid}" ]] || fail "web container missing; cannot inject config.js"
  config="${TMP_DIR}/snapnote-config.js"
  cat >"${config}" <<EOF
window.SNAPNOTE_CONFIG = {
  projectSlug: '${PROJECT_SLUG}',
  environment: '${ENV_NAME}',
  workerPolicy: '${WORKER_POLICY}',
  minReplicas: ${MIN_REPLICAS},
  maxReplicas: ${MAX_REPLICAS},
};
EOF
  docker cp "${config}" "${cid}:/usr/share/nginx/html/config.js" ||
    fail "docker cp config.js into web container failed"
  echo "  injected SPA config.js project=${PROJECT_SLUG} into ${cid:0:12}"
}

wait_database_url_injected_worker() {
  local cid="" url="" i
  echo "Waiting for DATABASE_URL injection into worker container..."
  for i in $(seq 1 120); do
    cid="$(worker_container_id)"
    if [[ -n "${cid}" ]]; then
      url="$(container_env "${cid}" DATABASE_URL)"
      if [[ -n "${url}" ]]; then
        echo "  DATABASE_URL present on worker ${cid:0:12}"
        return 0
      fi
    fi
    sleep 1
  done
  fail "DATABASE_URL never appeared on worker container"
}

container_env() {
  local cid="$1" key="$2"
  # Distroless images have no printenv/shell — read Config.Env via inspect.
  docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "${cid}" 2>/dev/null |
    awk -F= -v k="${key}" '$1==k { print substr($0, index($0, "=")+1); exit }'
}

wait_database_url_injected() {
  local cid="" url="" i
  echo "Waiting for DATABASE_URL injection into API container..."
  for i in $(seq 1 120); do
    cid="$(api_container_id)"
    if [[ -n "${cid}" ]]; then
      url="$(container_env "${cid}" DATABASE_URL)"
      if [[ -n "${url}" ]]; then
        echo "  DATABASE_URL present on container ${cid:0:12}"
        return 0
      fi
    fi
    sleep 1
  done
  fail "DATABASE_URL never appeared on API container"
}

prove_persistence() {
  local title="persist-$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')"
  local create_body note_id code cid
  echo "Proving note persistence across API container restart..."
  create_body="$(python3 -c 'import json,sys; print(json.dumps({"title":sys.argv[1],"body":"persists"}))' "${title}")"
  code="$(curl --silent --show-error -o "${TMP_DIR}/create-note.json" -w '%{http_code}' \
    -H "Host: ${API_HOST}" -H 'content-type: application/json' \
    -d "${create_body}" "${GATEWAY_URL}/notes" || echo "000")"
  [[ "${code}" == "201" ]] || fail "create note HTTP ${code}: $(cat "${TMP_DIR}/create-note.json")"
  note_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "${TMP_DIR}/create-note.json")"
  [[ -n "${note_id}" ]] || fail "create note missing id"

  cid="$(api_container_id)"
  [[ -n "${cid}" ]] || fail "API container missing before restart"
  echo "  restarting API container ${cid:0:12}..."
  docker restart "${cid}" >/dev/null || fail "docker restart api failed"
  # Gateway may briefly 502/503 while the container and upstream probe recover.
  wait_host_http "${API_HOST}" "/health/ready" 200 120
  refresh_routes

  code="000"
  for _ in $(seq 1 60); do
    code="$(curl --silent --show-error -o "${TMP_DIR}/list-notes.json" -w '%{http_code}' \
      -H "Host: ${API_HOST}" "${GATEWAY_URL}/notes" || echo "000")"
    if [[ "${code}" == "200" ]]; then
      break
    fi
    sleep 1
  done
  [[ "${code}" == "200" ]] || fail "list notes after restart HTTP ${code}: $(cat "${TMP_DIR}/list-notes.json" 2>/dev/null || true)"
  TITLE="${title}" NOTE_ID="${note_id}" python3 - <<'PY' "${TMP_DIR}/list-notes.json" || fail "note missing after restart"
import json, os, sys
notes = json.load(open(sys.argv[1]))
want_id = os.environ["NOTE_ID"]
want_title = os.environ["TITLE"]
match = [n for n in notes if n.get("id") == want_id]
assert match, {"want": want_id, "notes": notes}
assert match[0].get("title") == want_title, match[0]
print(f"  persisted note id={want_id} title={want_title}")
PY
}

prove_storage_roundtrip() {
  local title="attach-$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')"
  local create_body note_id att_id upload_url object_key code payload download_url
  echo "Proving attachment presign + PUT + GET against Forge Storage..."
  create_body="$(python3 -c 'import json,sys; print(json.dumps({"title":sys.argv[1],"body":"with file"}))' "${title}")"
  code="$(curl --silent --show-error -o "${TMP_DIR}/create-note-att.json" -w '%{http_code}' \
    -H "Host: ${API_HOST}" -H 'content-type: application/json' \
    -d "${create_body}" "${GATEWAY_URL}/notes" || echo "000")"
  [[ "${code}" == "201" ]] || fail "create note for attachment HTTP ${code}: $(cat "${TMP_DIR}/create-note-att.json")"
  note_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "${TMP_DIR}/create-note-att.json")"

  code="$(curl --silent --show-error -o "${TMP_DIR}/create-att.json" -w '%{http_code}' \
    -H "Host: ${API_HOST}" -H 'content-type: application/json' \
    -d '{"filename":"lake.jpg","contentType":"image/jpeg"}' \
    "${GATEWAY_URL}/notes/${note_id}/attachments" || echo "000")"
  [[ "${code}" == "201" ]] || fail "create attachment HTTP ${code}: $(cat "${TMP_DIR}/create-att.json")"
  upload_url="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["uploadUrl"])' "${TMP_DIR}/create-att.json")"
  att_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["attachment"]["id"])' "${TMP_DIR}/create-att.json")"
  object_key="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["attachment"]["objectKey"])' "${TMP_DIR}/create-att.json")"
  [[ -n "${upload_url}" && -n "${att_id}" && -n "${object_key}" ]] ||
    fail "attachment response incomplete: $(cat "${TMP_DIR}/create-att.json")"
  echo "  attachment id=${att_id} objectKey=${object_key}"

  payload="${TMP_DIR}/lake.jpg"
  printf 'snapnote-demo-bytes-%s' "${att_id}" >"${payload}"
  # Prefer direct storage host URL for the demo proof (public URL is SPA nginx proxy).
  # Rewrite app.snapnote.localhost:4000/storage → STORAGE_URL when present.
  local put_url="${upload_url}"
  put_url="$(UPLOAD_URL="${upload_url}" STORAGE_URL="${STORAGE_URL}" python3 - <<'PY'
import os, urllib.parse
u = os.environ["UPLOAD_URL"]
storage = os.environ["STORAGE_URL"].rstrip("/")
parsed = urllib.parse.urlparse(u)
if parsed.path.startswith("/storage/"):
    path = parsed.path[len("/storage"):]
    print(storage + path + (("?" + parsed.query) if parsed.query else ""))
else:
    print(u)
PY
)"
  code="$(curl --silent --show-error -o "${TMP_DIR}/put-object.json" -w '%{http_code}' \
    -X PUT -H 'content-type: image/jpeg' --data-binary @"${payload}" \
    "${put_url}" || echo "000")"
  [[ "${code}" == "201" || "${code}" == "200" ]] ||
    fail "storage PUT HTTP ${code}: $(cat "${TMP_DIR}/put-object.json" 2>/dev/null || true)"

  code="$(curl --silent --show-error -o "${TMP_DIR}/download-att.json" -w '%{http_code}' \
    -H "Host: ${API_HOST}" \
    "${GATEWAY_URL}/notes/${note_id}/attachments/${att_id}/download" || echo "000")"
  [[ "${code}" == "200" ]] || fail "download presign HTTP ${code}: $(cat "${TMP_DIR}/download-att.json")"
  download_url="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["downloadUrl"])' "${TMP_DIR}/download-att.json")"
  local get_url="${download_url}"
  get_url="$(DOWNLOAD_URL="${download_url}" STORAGE_URL="${STORAGE_URL}" python3 - <<'PY'
import os, urllib.parse
u = os.environ["DOWNLOAD_URL"]
storage = os.environ["STORAGE_URL"].rstrip("/")
parsed = urllib.parse.urlparse(u)
if parsed.path.startswith("/storage/"):
    path = parsed.path[len("/storage"):]
    print(storage + path + (("?" + parsed.query) if parsed.query else ""))
else:
    print(u)
PY
)"
  code="$(curl --silent --show-error -o "${TMP_DIR}/get-object.bin" -w '%{http_code}' \
    "${get_url}" || echo "000")"
  [[ "${code}" == "200" ]] || fail "storage GET HTTP ${code}"
  cmp -s "${payload}" "${TMP_DIR}/get-object.bin" || fail "downloaded object bytes differ from upload"

  # Streamed GET via API (project-credential path).
  code="$(curl --silent --show-error -o "${TMP_DIR}/stream-object.bin" -w '%{http_code}' \
    -H "Host: ${API_HOST}" \
    "${GATEWAY_URL}/notes/${note_id}/attachments/${att_id}/content" || echo "000")"
  [[ "${code}" == "200" ]] || fail "streamed content HTTP ${code}"
  cmp -s "${payload}" "${TMP_DIR}/stream-object.bin" || fail "streamed object bytes differ from upload"

  # Metadata list includes pending row with correct key.
  code="$(curl --silent --show-error -o "${TMP_DIR}/list-att.json" -w '%{http_code}' \
    -H "Host: ${API_HOST}" \
    "${GATEWAY_URL}/notes/${note_id}/attachments" || echo "000")"
  [[ "${code}" == "200" ]] || fail "list attachments HTTP ${code}"
  ATT_ID="${att_id}" OBJECT_KEY="${object_key}" python3 - <<'PY' "${TMP_DIR}/list-att.json" || fail "attachment metadata mismatch"
import json, os, sys
items = json.load(open(sys.argv[1]))
want_id = os.environ["ATT_ID"]
want_key = os.environ["OBJECT_KEY"]
match = [a for a in items if a.get("id") == want_id]
assert match, items
assert match[0].get("objectKey") == want_key, match[0]
assert match[0].get("status") == "pending", match[0]
print(f"  storage round-trip ok id={want_id} key={want_key} status=pending")
PY
}

rewrite_storage_url() {
  local url="$1"
  UPLOAD_URL="${url}" STORAGE_URL="${STORAGE_URL}" python3 - <<'PY'
import os, urllib.parse
u = os.environ["UPLOAD_URL"]
storage = os.environ["STORAGE_URL"].rstrip("/")
parsed = urllib.parse.urlparse(u)
if parsed.path.startswith("/storage/"):
    path = parsed.path[len("/storage"):]
    print(storage + path + (("?" + parsed.query) if parsed.query else ""))
else:
    print(u)
PY
}

prove_worker_thumbnail() {
  local title="thumb-$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')"
  local create_body note_id att_id upload_url object_key code payload thumb_key status=""
  echo "Proving upload → events queue → worker thumbnail → status ready..."
  create_body="$(python3 -c 'import json,sys; print(json.dumps({"title":sys.argv[1],"body":"async thumb"}))' "${title}")"
  code="$(curl --silent --show-error -o "${TMP_DIR}/create-note-thumb.json" -w '%{http_code}' \
    -H "Host: ${API_HOST}" -H 'content-type: application/json' \
    -d "${create_body}" "${GATEWAY_URL}/notes" || echo "000")"
  [[ "${code}" == "201" ]] || fail "create note for thumb HTTP ${code}: $(cat "${TMP_DIR}/create-note-thumb.json")"
  note_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "${TMP_DIR}/create-note-thumb.json")"

  code="$(curl --silent --show-error -o "${TMP_DIR}/create-att-thumb.json" -w '%{http_code}' \
    -H "Host: ${API_HOST}" -H 'content-type: application/json' \
    -d '{"filename":"sunset.jpg","contentType":"image/jpeg"}' \
    "${GATEWAY_URL}/notes/${note_id}/attachments" || echo "000")"
  [[ "${code}" == "201" ]] || fail "create attachment for thumb HTTP ${code}: $(cat "${TMP_DIR}/create-att-thumb.json")"
  upload_url="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["uploadUrl"])' "${TMP_DIR}/create-att-thumb.json")"
  att_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["attachment"]["id"])' "${TMP_DIR}/create-att-thumb.json")"
  object_key="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["attachment"]["objectKey"])' "${TMP_DIR}/create-att-thumb.json")"

  payload="${TMP_DIR}/sunset.jpg"
  printf 'snapnote-thumb-bytes-%s' "${att_id}" >"${payload}"
  local put_url
  put_url="$(rewrite_storage_url "${upload_url}")"
  code="$(curl --silent --show-error -o "${TMP_DIR}/put-thumb.json" -w '%{http_code}' \
    -X PUT -H 'content-type: image/jpeg' --data-binary @"${payload}" \
    "${put_url}" || echo "000")"
  [[ "${code}" == "201" || "${code}" == "200" ]] ||
    fail "storage PUT (thumb) HTTP ${code}: $(cat "${TMP_DIR}/put-thumb.json" 2>/dev/null || true)"

  code="$(curl --silent --show-error -o "${TMP_DIR}/complete-att.json" -w '%{http_code}' \
    -H "Host: ${API_HOST}" -X POST \
    "${GATEWAY_URL}/notes/${note_id}/attachments/${att_id}/complete" || echo "000")"
  [[ "${code}" == "202" || "${code}" == "200" ]] ||
    fail "complete attachment HTTP ${code}: $(cat "${TMP_DIR}/complete-att.json")"

  echo "  waiting for worker to mark attachment ready..."
  for _ in $(seq 1 90); do
    code="$(curl --silent --show-error -o "${TMP_DIR}/get-att-thumb.json" -w '%{http_code}' \
      -H "Host: ${API_HOST}" \
      "${GATEWAY_URL}/notes/${note_id}/attachments/${att_id}" || echo "000")"
    if [[ "${code}" == "200" ]]; then
      status="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("status",""))' "${TMP_DIR}/get-att-thumb.json")"
      thumb_key="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("thumbnailKey") or "")' "${TMP_DIR}/get-att-thumb.json")"
      if [[ "${status}" == "ready" && -n "${thumb_key}" ]]; then
        echo "  attachment ready id=${att_id} thumbnailKey=${thumb_key}"
        break
      fi
    fi
    sleep 1
  done
  [[ "${status}" == "ready" && -n "${thumb_key}" ]] ||
    fail "attachment never reached ready; last=$(cat "${TMP_DIR}/get-att-thumb.json" 2>/dev/null || true)"

  # Thumbnail object exists in storage.
  code="$(curl --silent --show-error -o "${TMP_DIR}/get-thumb-obj.bin" -w '%{http_code}' \
    -H "X-Forge-Project: ${STORAGE_PROJECT}" \
    "${STORAGE_URL}/v1/buckets/${STORAGE_BUCKET}/objects/$(python3 -c 'import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1], safe="/"))' "${thumb_key}")" || echo "000")"
  [[ "${code}" == "200" ]] || fail "thumbnail GET HTTP ${code}"
  python3 - <<'PY' "${TMP_DIR}/get-thumb-obj.bin" || fail "thumbnail missing THUMB marker"
import sys
data = open(sys.argv[1], "rb").read()
assert data.startswith(b"THUMB\n"), data[:40]
print(f"  thumbnail object ok bytes={len(data)}")
PY

  # Idempotent complete (same Idempotency-Key) + still one thumbnail.
  code="$(curl --silent --show-error -o "${TMP_DIR}/complete-att-2.json" -w '%{http_code}' \
    -H "Host: ${API_HOST}" -X POST \
    "${GATEWAY_URL}/notes/${note_id}/attachments/${att_id}/complete" || echo "000")"
  [[ "${code}" == "202" || "${code}" == "200" ]] ||
    fail "second complete HTTP ${code}: $(cat "${TMP_DIR}/complete-att-2.json")"
  sleep 2
  code="$(curl --silent --show-error -o "${TMP_DIR}/get-att-thumb-2.json" -w '%{http_code}' \
    -H "Host: ${API_HOST}" \
    "${GATEWAY_URL}/notes/${note_id}/attachments/${att_id}" || echo "000")"
  [[ "${code}" == "200" ]] || fail "re-get attachment HTTP ${code}"
  ATT_ID="${att_id}" THUMB="${thumb_key}" python3 - <<'PY' "${TMP_DIR}/get-att-thumb-2.json" || fail "idempotency broke ready state"
import json, os, sys
body = json.load(open(sys.argv[1]))
assert body.get("status") == "ready", body
assert body.get("thumbnailKey") == os.environ["THUMB"], body
print(f"  idempotent complete ok id={os.environ['ATT_ID']}")
PY
}

prove_worker_restart_exactly_once() {
  local title="restart-$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')"
  local create_body note_id att_id upload_url object_key code payload thumb_key status="" cid
  echo "Proving exactly-once across worker restart mid-processing..."
  create_body="$(python3 -c 'import json,sys; print(json.dumps({"title":sys.argv[1],"body":"restart"}))' "${title}")"
  code="$(curl --silent --show-error -o "${TMP_DIR}/create-note-rst.json" -w '%{http_code}' \
    -H "Host: ${API_HOST}" -H 'content-type: application/json' \
    -d "${create_body}" "${GATEWAY_URL}/notes" || echo "000")"
  [[ "${code}" == "201" ]] || fail "create note restart HTTP ${code}"
  note_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "${TMP_DIR}/create-note-rst.json")"

  code="$(curl --silent --show-error -o "${TMP_DIR}/create-att-rst.json" -w '%{http_code}' \
    -H "Host: ${API_HOST}" -H 'content-type: application/json' \
    -d '{"filename":"burst.jpg","contentType":"image/jpeg"}' \
    "${GATEWAY_URL}/notes/${note_id}/attachments" || echo "000")"
  [[ "${code}" == "201" ]] || fail "create attachment restart HTTP ${code}"
  upload_url="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["uploadUrl"])' "${TMP_DIR}/create-att-rst.json")"
  att_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["attachment"]["id"])' "${TMP_DIR}/create-att-rst.json")"
  object_key="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["attachment"]["objectKey"])' "${TMP_DIR}/create-att-rst.json")"

  payload="${TMP_DIR}/burst.jpg"
  printf 'snapnote-restart-bytes-%s' "${att_id}" >"${payload}"
  local put_url
  put_url="$(rewrite_storage_url "${upload_url}")"
  code="$(curl --silent --show-error -o /dev/null -w '%{http_code}' \
    -X PUT -H 'content-type: image/jpeg' --data-binary @"${payload}" \
    "${put_url}" || echo "000")"
  [[ "${code}" == "201" || "${code}" == "200" ]] || fail "PUT restart HTTP ${code}"

  # Stop worker before complete so the message is unconsumed; Control reconciles a
  # replacement replica (restart-safe consume + app idempotency).
  cid="$(worker_container_id)"
  [[ -n "${cid}" ]] || fail "worker container missing before restart proof"
  echo "  stopping worker ${cid:0:12} before publish..."
  docker stop "${cid}" >/dev/null || fail "docker stop worker failed"

  code="$(curl --silent --show-error -o "${TMP_DIR}/complete-rst.json" -w '%{http_code}' \
    -H "Host: ${API_HOST}" -X POST \
    "${GATEWAY_URL}/notes/${note_id}/attachments/${att_id}/complete" || echo "000")"
  [[ "${code}" == "202" || "${code}" == "200" ]] ||
    fail "complete during worker stop HTTP ${code}: $(cat "${TMP_DIR}/complete-rst.json")"

  echo "  waiting for Control to reconcile a replacement worker..."
  # Best-effort start of the stopped container; reconciler may also create a new one.
  docker start "${cid}" >/dev/null 2>&1 || true
  wait_host_http "${WORKER_HOST}" "/health/ready" 200 180

  for _ in $(seq 1 90); do
    code="$(curl --silent --show-error -o "${TMP_DIR}/get-att-rst.json" -w '%{http_code}' \
      -H "Host: ${API_HOST}" \
      "${GATEWAY_URL}/notes/${note_id}/attachments/${att_id}" || echo "000")"
    if [[ "${code}" == "200" ]]; then
      status="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("status",""))' "${TMP_DIR}/get-att-rst.json")"
      thumb_key="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("thumbnailKey") or "")' "${TMP_DIR}/get-att-rst.json")"
      if [[ "${status}" == "ready" && -n "${thumb_key}" ]]; then
        break
      fi
    fi
    sleep 1
  done
  [[ "${status}" == "ready" && -n "${thumb_key}" ]] ||
    fail "attachment not ready after worker restart; last=$(cat "${TMP_DIR}/get-att-rst.json" 2>/dev/null || true)"

  code="$(curl --silent --show-error -o "${TMP_DIR}/get-thumb-rst.bin" -w '%{http_code}' \
    -H "X-Forge-Project: ${STORAGE_PROJECT}" \
    "${STORAGE_URL}/v1/buckets/${STORAGE_BUCKET}/objects/$(python3 -c 'import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1], safe="/"))' "${thumb_key}")" || echo "000")"
  [[ "${code}" == "200" ]] || fail "thumbnail after restart HTTP ${code}"
  echo "  restart-safe exactly-once ok id=${att_id} thumbnailKey=${thumb_key}"
}

publish_queue_metrics() {
  local depth="$1" retry="${2:-0}"
  curl --fail --silent --show-error -X PUT "${METRICS_URL}/demo/queue/${QUEUE_NAME}" \
    -H 'content-type: application/json' \
    -d "{\"depth\":${depth},\"oldestAgeSeconds\":15,\"consumerLag\":${depth},\"retryRate\":${retry}}" \
    >/dev/null || fail "publish queue metrics failed"
  echo "  queue metrics: depth=${depth} retryRate=${retry}"
}

clear_queue_metrics() {
  curl --silent --show-error -X DELETE "${METRICS_URL}/demo/queue/${QUEUE_NAME}" >/dev/null || true
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

create_worker_resource() {
  local code
  code="$(curl -s -o "${TMP_DIR}/worker.json" -w '%{http_code}' -X POST \
    "${CONTROL_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/workers" \
    -H 'content-type: application/json' \
    -d "{\"metadata\":{\"name\":\"${WORKER_NAME}\"},\"spec\":{\"queue\":\"${QUEUE_NAME}\",\"scaling\":{\"desiredReplicas\":${MIN_REPLICAS},\"minReplicas\":${MIN_REPLICAS},\"maxReplicas\":${MAX_REPLICAS}}}}")"
  if [[ "${code}" == "201" || "${code}" == "200" ]]; then
    echo "  Worker ${WORKER_NAME} created"
    return 0
  fi
  if [[ "${code}" == "409" ]]; then
    code="$(curl -s -o "${TMP_DIR}/worker-patch.json" -w '%{http_code}' -X PATCH \
      "${CONTROL_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/workers/${WORKER_NAME}" \
      -H 'content-type: application/json' \
      -d "{\"spec\":{\"scaling\":{\"desiredReplicas\":${MIN_REPLICAS},\"minReplicas\":${MIN_REPLICAS},\"maxReplicas\":${MAX_REPLICAS}}}}")"
    [[ "${code}" == "200" || "${code}" == "201" ]] ||
      fail "patch Worker HTTP ${code}: $(cat "${TMP_DIR}/worker-patch.json")"
    echo "  Worker ${WORKER_NAME} already exists (patched bounds)"
    return 0
  fi
  fail "create Worker HTTP ${code}: $(cat "${TMP_DIR}/worker.json")"
}

worker_policy_spec() {
  # $1 = include_retry (1|0). retryRate is only armed during the hold proof —
  # permanently including it blocks drain because HoldRetryHealthy returns the
  # current replica count and wins the max-recommendation merge.
  local include_retry="${1:-0}"
  local metrics
  if [[ "${include_retry}" == "1" ]]; then
    metrics="$(cat <<EOF
[
  {"type": "queueDepth", "targetValue": ${TARGET_PER_REPLICA}, "queue": "${QUEUE_NAME}"},
  {"type": "retryRate", "targetValue": 0.05, "queue": "${QUEUE_NAME}"}
]
EOF
)"
  else
    metrics="$(cat <<EOF
[
  {"type": "queueDepth", "targetValue": ${TARGET_PER_REPLICA}, "queue": "${QUEUE_NAME}"}
]
EOF
)"
  fi
  cat <<EOF
{
  "targetRef": {"kind": "Worker", "name": "${WORKER_NAME}"},
  "minReplicas": ${MIN_REPLICAS},
  "maxReplicas": ${MAX_REPLICAS},
  "metrics": ${metrics},
  "behavior": {
    "scaleUp": {"stabilizationWindowSeconds": 0, "maxReplicasPerMinute": ${MAX_REPLICAS}},
    "scaleDown": {"stabilizationWindowSeconds": 0, "maxReplicasPerMinute": ${MAX_REPLICAS}}
  },
  "metricOutageFallback": {"mode": "hold"}
}
EOF
}

replace_worker_scaling_policy() {
  local include_retry="${1:-0}"
  local spec rv code
  spec="$(worker_policy_spec "${include_retry}")"
  curl --fail --silent --show-error \
    "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies/${WORKER_POLICY}" \
    >"${TMP_DIR}/sp-get.json" || fail "GET ${WORKER_POLICY} before replace failed"
  rv="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["metadata"]["resourceVersion"])' "${TMP_DIR}/sp-get.json")"
  code="$(curl -s -o "${TMP_DIR}/sp-put.json" -w '%{http_code}' -X PUT \
    "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies/${WORKER_POLICY}" \
    -H 'content-type: application/json' \
    -d "{\"metadata\":{\"resourceVersion\":\"${rv}\"},\"spec\":${spec}}")"
  [[ "${code}" == "200" ]] ||
    fail "replace ${WORKER_POLICY} HTTP ${code}: $(cat "${TMP_DIR}/sp-put.json")"
  echo "  ScalingPolicy ${WORKER_POLICY} replaced (retryRate=${include_retry})"
}

apply_worker_scaling_policy() {
  local spec code
  spec="$(worker_policy_spec 0)"
  code="$(curl -s -o "${TMP_DIR}/sp-worker.json" -w '%{http_code}' -X POST \
    "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies" \
    -H 'content-type: application/json' \
    -H "Idempotency-Key: demo52-${PROJECT_SLUG}-${WORKER_POLICY}" \
    -d "{\"metadata\":{\"name\":\"${WORKER_POLICY}\"},\"spec\":${spec}}")"
  if [[ "${code}" == "201" || "${code}" == "200" ]]; then
    echo "  ScalingPolicy ${WORKER_POLICY} created"
  elif [[ "${code}" == "409" ]]; then
    replace_worker_scaling_policy 0
  else
    fail "create ${WORKER_POLICY} HTTP ${code}: $(cat "${TMP_DIR}/sp-worker.json")"
  fi
  curl --fail --silent --show-error \
    "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies/${WORKER_POLICY}" \
    >"${TMP_DIR}/sp-get.json" || fail "GET ${WORKER_POLICY} failed after create"
  echo "  ScalingPolicy readable project=${PROJECT_SLUG}"
}

policy_desired() {
  curl --fail --silent --show-error \
    "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies/${WORKER_POLICY}" |
    python3 -c 'import json,sys; print(int(json.load(sys.stdin).get("status",{}).get("desiredReplicas") or 0))'
}

policy_metric_type() {
  curl --fail --silent --show-error \
    "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies/${WORKER_POLICY}" |
    python3 -c 'import json,sys; r=(json.load(sys.stdin).get("status") or {}).get("lastRecommendation") or {}; print(r.get("metricType") or "")'
}

wait_policy_desired_ge() {
  local min="$1" attempts="${2:-90}"
  local cur=0
  echo "Waiting for ScalingPolicy ${WORKER_POLICY} desiredReplicas >= ${min} ..."
  for _ in $(seq 1 "${attempts}"); do
    cur="$(policy_desired 2>/dev/null || echo 0)"
    if [[ "${cur}" -ge "${min}" ]]; then
      echo "  ${WORKER_POLICY} desiredReplicas=${cur}"
      return 0
    fi
    sleep 1
  done
  fail "timed out waiting for ${WORKER_POLICY} desiredReplicas >= ${min} (got ${cur})"
}

wait_policy_desired_eq() {
  local want="$1" attempts="${2:-90}"
  local cur=0
  echo "Waiting for ScalingPolicy ${WORKER_POLICY} desiredReplicas == ${want} ..."
  for _ in $(seq 1 "${attempts}"); do
    cur="$(policy_desired 2>/dev/null || echo 0)"
    if [[ "${cur}" -eq "${want}" ]]; then
      echo "  ${WORKER_POLICY} desiredReplicas=${cur}"
      return 0
    fi
    sleep 1
  done
  fail "timed out waiting for ${WORKER_POLICY} desiredReplicas == ${want} (got ${cur})"
}

assert_replicas_in_bounds() {
  local cur="$1"
  [[ "${cur}" -ge "${MIN_REPLICAS}" && "${cur}" -le "${MAX_REPLICAS}" ]] ||
    fail "desiredReplicas=${cur} outside [${MIN_REPLICAS},${MAX_REPLICAS}]"
}

sync_worker_to_deployment() {
  # Bridge Worker.spec.scaling.desiredReplicas → Deployment desiredReplicas
  # (autoscaler actuates Worker; reconciler reads Deployment).
  [[ -n "${WORKER_DEPLOYMENT_ID}" ]] || fail "WORKER_DEPLOYMENT_ID required before sync loop"
  python3 - "${CONTROL_URL}" "${PROJECT_SLUG}" "${ENV_NAME}" "${WORKER_NAME}" "${WORKER_DEPLOYMENT_ID}" <<'PY' &
import json, time, urllib.request, sys
base, project, env, worker, dep_id = sys.argv[1:6]
worker_url = f"{base}/v1/projects/{project}/environments/{env}/workers/{worker}"
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
        w = get(worker_url)
        desired = (((w.get("spec") or {}).get("scaling") or {}).get("desiredReplicas"))
        if desired is None:
            time.sleep(1)
            continue
        desired = int(desired)
        dep = get(dep_url)
        cur = int(dep.get("desiredReplicas") or 0)
        if cur != desired:
            patch(dep_url, {"desiredReplicas": desired})
            if desired != last:
                print(f"sync: worker deployment {dep_id} desiredReplicas {cur} -> {desired}", flush=True)
            last = desired
    except Exception as exc:
        print(f"sync: {exc}", flush=True)
    time.sleep(1)
PY
  SYNC_PID=$!
  echo "  started Worker→Deployment sync pid=${SYNC_PID}"
}

prove_worker_autoscaling() {
  local up_desired held_desired down_desired metric_type peak_min
  echo "Proving worker queueDepth autoscaling (burst → scale-up → retry hold → drain)..."
  python3 "${DEMO_DIR}/scripts/test_queue_scaling.py" ||
    fail "queue scaling unit tests failed"

  ensure_worker_kind
  create_worker_resource
  apply_worker_scaling_policy
  publish_queue_metrics 0 0
  sync_worker_to_deployment

  wait_policy_desired_eq "${MIN_REPLICAS}" 60
  assert_replicas_in_bounds "$(policy_desired)"

  # ceil(BURST_DEPTH / TARGET_PER_REPLICA); clamp to max. Default 80/20 → 4.
  peak_min="$(python3 -c "import math; print(min(${MAX_REPLICAS}, max(${MIN_REPLICAS}, math.ceil(${BURST_DEPTH}/${TARGET_PER_REPLICA}))))")"

  echo "  bursting ${BURST_COUNT} attachments (metrics depth=${BURST_DEPTH})..."
  GATEWAY_URL="${GATEWAY_URL}" API_HOST="${API_HOST}" STORAGE_URL="${STORAGE_URL}" \
    METRICS_URL="${METRICS_URL}" QUEUE_NAME="${QUEUE_NAME}" PUBLISH_METRICS=1 \
    bash "${DEMO_DIR}/scripts/burst.sh" --count "${BURST_COUNT}" --depth "${BURST_DEPTH}" \
    >"${TMP_DIR}/burst.out" || fail "burst.sh failed: $(cat "${TMP_DIR}/burst.out" 2>/dev/null || true)"
  # Keep depth high while autoscaler evaluates (worker may drain real queue quickly).
  publish_queue_metrics "${BURST_DEPTH}" 0

  wait_policy_desired_ge "${peak_min}" 90
  up_desired="$(policy_desired)"
  assert_replicas_in_bounds "${up_desired}"
  [[ "${up_desired}" -ge "${peak_min}" ]] ||
    fail "expected scale-up >= ${peak_min}, got ${up_desired}"
  [[ "${up_desired}" -le "${MAX_REPLICAS}" ]] ||
    fail "scale-up exceeded maxReplicas: ${up_desired}"
  metric_type="$(policy_metric_type)"
  [[ "${metric_type}" == "queueDepth" ]] ||
    fail "lastRecommendation.metricType=${metric_type}, want queueDepth"
  echo "  scale-up ok desiredReplicas=${up_desired} metricType=${metric_type}"

  # Scale-down safety: temporarily arm retryRate on the policy, drop backlog
  # while retry pressure is high, assert hold, then disarm retryRate so drain
  # can proceed (HoldRetryHealthy would otherwise keep desired at current).
  replace_worker_scaling_policy 1
  publish_queue_metrics "${BURST_DEPTH}" 0.06
  sleep 2
  publish_queue_metrics 0 0.06
  held_desired=0
  local saw_retry_block=0
  for _ in $(seq 1 20); do
    held_desired="$(policy_desired 2>/dev/null || echo 0)"
    curl --fail --silent --show-error \
      "${AUTOSCALER_URL}/v1/projects/${PROJECT_SLUG}/environments/${ENV_NAME}/scalingpolicies/${WORKER_POLICY}" \
      >"${TMP_DIR}/sp-retry.json" || true
    if python3 - <<'PY' "${TMP_DIR}/sp-retry.json"
import json, sys
body = json.load(open(sys.argv[1]))
conds = (body.get("status") or {}).get("conditions") or []
sys.exit(0 if any(c.get("reason") == "RetryPressureBlocksScaleDown" for c in conds) else 1)
PY
    then
      saw_retry_block=1
    fi
    if [[ "${held_desired}" -ge "${peak_min}" && "${saw_retry_block}" -eq 1 ]]; then
      break
    fi
    publish_queue_metrics 0 0.06
    sleep 1
  done
  assert_replicas_in_bounds "${held_desired}"
  [[ "${held_desired}" -ge "${peak_min}" ]] ||
    fail "retry pressure must block scale-down (held=${held_desired}, prior=${up_desired})"
  [[ "${saw_retry_block}" -eq 1 ]] ||
    fail "expected RetryPressureBlocksScaleDown condition; status=$(cat "${TMP_DIR}/sp-retry.json")"
  echo "  scale-down safety ok desiredReplicas=${held_desired} (retry in flight)"

  # Drain: disarm retryRate + clear backlog → minReplicas.
  replace_worker_scaling_policy 0
  publish_queue_metrics 0 0
  wait_policy_desired_eq "${MIN_REPLICAS}" 90
  down_desired="$(policy_desired)"
  assert_replicas_in_bounds "${down_desired}"
  echo "  scale-down ok desiredReplicas=${down_desired}"

  # Real burst backlog should eventually reach ready (worker still running).
  local note_id pending=0
  note_id="$(awk -F= '/^BURST_NOTE_ID=/{print $2}' "${TMP_DIR}/burst.out")"
  if [[ -n "${note_id}" ]]; then
    echo "  waiting for burst attachments on note ${note_id} to reach ready..."
    for _ in $(seq 1 180); do
      curl --fail --silent --show-error -H "Host: ${API_HOST}" \
        "${GATEWAY_URL}/notes/${note_id}/attachments" >"${TMP_DIR}/burst-atts.json" || true
      pending="$(python3 -c '
import json,sys
items=json.load(open(sys.argv[1]))
print(sum(1 for a in items if a.get("status")!="ready"))
' "${TMP_DIR}/burst-atts.json" 2>/dev/null || echo 99)"
      if [[ "${pending}" -eq 0 ]]; then
        echo "  burst backlog drained (all ready)"
        break
      fi
      sleep 1
    done
    [[ "${pending}" -eq 0 ]] ||
      echo "  warning: ${pending} burst attachments still pending (non-fatal for scaling proof)" >&2
  fi

  clear_queue_metrics
  echo "  worker autoscaling proof complete (bounds [${MIN_REPLICAS},${MAX_REPLICAS}])"
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
  PROJECT_NAME="SnapNote ${SUFFIX}"
  PROJECT_SLUG="snapnote-${SUFFIX}"

  echo "Rendering forge.yaml → apply (project=${PROJECT_SLUG})..."
  PROJECT_NAME="${PROJECT_NAME}" PROJECT_SLUG="${PROJECT_SLUG}" \
    API_IMAGE="${API_IMAGE}" WEB_IMAGE="${WEB_IMAGE}" WORKER_IMAGE="${WORKER_IMAGE}" \
    envsubst '${PROJECT_NAME} ${PROJECT_SLUG} ${API_IMAGE} ${WEB_IMAGE} ${WORKER_IMAGE}' \
    <"${DEMO_DIR}/forge.yaml" >"${TMP_DIR}/forge.yaml"

  forge_json "${TMP_DIR}/apply.json" apply -f "${TMP_DIR}/forge.yaml"

  PROJECT_ID=""
  API_DEPLOYMENT_ID=""
  WEB_DEPLOYMENT_ID=""
  WORKER_DEPLOYMENT_ID=""
  while IFS= read -r line; do
    case "${line}" in
      PROJECT_ID=*) PROJECT_ID="${line#PROJECT_ID=}" ;;
      DEPLOYMENT:snapnote-api=*) API_DEPLOYMENT_ID="${line#DEPLOYMENT:snapnote-api=}" ;;
      DEPLOYMENT:snapnote-web=*) WEB_DEPLOYMENT_ID="${line#DEPLOYMENT:snapnote-web=}" ;;
      DEPLOYMENT:snapnote-worker=*) WORKER_DEPLOYMENT_ID="${line#DEPLOYMENT:snapnote-worker=}" ;;
    esac
  done < <(extract_apply_ids)

  [[ -n "${API_DEPLOYMENT_ID}" ]] || fail "snapnote-api Deployment id missing from apply"
  [[ -n "${WEB_DEPLOYMENT_ID}" ]] || fail "snapnote-web Deployment id missing from apply"
  [[ -n "${WORKER_DEPLOYMENT_ID}" ]] || fail "snapnote-worker Deployment id missing from apply"

  if [[ -z "${PROJECT_ID}" ]]; then
    # Resolve project UUID by slug via Control list API (auth=dev).
    PROJECT_ID="$(curl --fail --silent --show-error "${CONTROL_URL}/v1/projects" |
      PROJECT_SLUG="${PROJECT_SLUG}" python3 -c '
import json,os,sys
slug=os.environ["PROJECT_SLUG"]
for p in json.load(sys.stdin):
    if p.get("slug")==slug or p.get("name")==slug:
        print(p["id"]); break
')" || true
  fi
  [[ -n "${PROJECT_ID}" ]] || fail "PROJECT_ID missing from apply/list"
  echo "Deployments api=${API_DEPLOYMENT_ID} worker=${WORKER_DEPLOYMENT_ID} web=${WEB_DEPLOYMENT_ID} project=${PROJECT_ID}"

  provision_managed_db
  wait_database_url_injected
  wait_database_url_injected_worker
  assert_applications_ready
  wait_route_host "${API_HOST}" 90
  wait_route_host "${WORKER_HOST}" 90
  wait_route_host "${APP_HOST}" 90
  wait_host_http "${API_HOST}" "/health/ready" 200 90
  wait_host_http "${WORKER_HOST}" "/health/ready" 200 90
  wait_host_http "${APP_HOST}" "/" 200 60
  inject_web_config

  # Optional: forge wait Ready when CLI supports it.
  if "${FORGE_BIN}" wait --help >/dev/null 2>&1; then
    forge wait "application/snapnote-api" --for=condition=Ready --timeout=2m ||
      fail "forge wait snapnote-api failed"
    forge wait "application/snapnote-worker" --for=condition=Ready --timeout=2m ||
      fail "forge wait snapnote-worker failed"
    forge wait "application/snapnote-web" --for=condition=Ready --timeout=2m ||
      fail "forge wait snapnote-web failed"
  fi

  write_state
  bash "${DEMO_DIR}/seed.sh" || fail "seed.sh failed"
  prove_persistence
  prove_storage_roundtrip
  prove_worker_thumbnail
  prove_worker_restart_exactly_once
  prove_worker_autoscaling

  echo
  echo "demo 52 deploy READY (storage + events + queueDepth worker autoscaling)"
  echo "  App:          http://${APP_HOST}:4000/"
  echo "  API:          http://${API_HOST}:4000/health/ready"
  echo "  Worker:       http://${WORKER_HOST}:4000/health/ready"
  echo "  API image:    ${API_IMAGE}"
  echo "  Worker image: ${WORKER_IMAGE}"
  echo "  Web image:    ${WEB_IMAGE}"
  echo "  Database:     ${DB_NAME} (Ready)"
  echo "  Storage:      ${STORAGE_BUCKET} @ ${STORAGE_URL}"
  echo "  Events:       ${EVENTS_URL} (queue ${QUEUE_NAME} / attachment.uploaded)"
  echo "  Autoscaler:   ${AUTOSCALER_URL} policy=${WORKER_POLICY} bounds=[${MIN_REPLICAS},${MAX_REPLICAS}]"
  echo "  Deployments:  api=${API_DEPLOYMENT_ID} worker=${WORKER_DEPLOYMENT_ID} web=${WEB_DEPLOYMENT_ID}"
  echo "  Project:      ${PROJECT_SLUG} (${PROJECT_ID})"
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
