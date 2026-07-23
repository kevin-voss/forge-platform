#!/usr/bin/env bash
# Demo 09 full-platform (19.02): Build → registry → forge deployment create →
# Runtime → Gateway → Events (incident.created).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/09-full-platform"
PRODUCT_DIR="${DEMO_DIR}/product"
LIB_DIR="${DEMO_DIR}/lib"

# Temporary until 19.03 wires Identity.
export FORGE_AUTH_MODE="${FORGE_AUTH_MODE:-dev}"
export FORGE_EVENTS_AUTH_MODE="${FORGE_EVENTS_AUTH_MODE:-dev}"
export FORGE_EVENTS_STREAMS="${FORGE_EVENTS_STREAMS:-build,deployment,runtime,application,agent,incident}"
# Brace default must not sit inside ${VAR:-...} — bash truncates at the first '}'.
if [[ -z "${FORGE_HOST_PATTERN:-}" ]]; then
  FORGE_HOST_PATTERN='{service}.demo.localhost'
fi
export FORGE_HOST_PATTERN
export FORGE_RECONCILE_INTERVAL_SECONDS="${FORGE_RECONCILE_INTERVAL_SECONDS:-3}"
export FORGE_LIFECYCLE_OWNER="${FORGE_LIFECYCLE_OWNER:-runtime}"
export FORGE_ROUTE_SYNC_INTERVAL_SECONDS="${FORGE_ROUTE_SYNC_INTERVAL_SECONDS:-3}"
export FORGE_UPSTREAM_PROBE_INTERVAL_SECONDS="${FORGE_UPSTREAM_PROBE_INTERVAL_SECONDS:-2}"
export FORGE_UPSTREAM_FAILURE_THRESHOLD="${FORGE_UPSTREAM_FAILURE_THRESHOLD:-1}"
export FORGE_UPSTREAM_SUCCESS_THRESHOLD="${FORGE_UPSTREAM_SUCCESS_THRESHOLD:-1}"
export FORGE_BUILD_TIMEOUT_SECONDS="${FORGE_BUILD_TIMEOUT_SECONDS:-1200}"

COMPOSE=(
  docker compose
    -f "${ROOT_DIR}/compose.yaml"
    -f "${DEMO_DIR}/compose.yaml"
    --project-directory "${ROOT_DIR}"
)

GATEWAY_URL="${FORGE_GATEWAY_URL:-http://127.0.0.1:4000}"
CONTROL_URL="${FORGE_CONTROL_URL:-http://127.0.0.1:4001}"
RUNTIME_URL="${FORGE_RUNTIME_URL:-http://127.0.0.1:4102}"
BUILD_URL="${FORGE_BUILD_URL:-http://127.0.0.1:4103}"
EVENTS_URL="${FORGE_EVENTS_HOST_URL:-http://127.0.0.1:4105}"
REGISTRY="${FORGE_REGISTRY:-localhost:5000}"
PROJECT_NAME="${FORGE_CAPSTONE_PROJECT:-capstone}"

CLI_DIR="${ROOT_DIR}/tools/forge-cli"
FORGE_BIN="${CLI_DIR}/forge"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-capstone-deploy.XXXXXX")"
CONFIG_HOME="${TMP_DIR}/xdg-config"
export XDG_CONFIG_HOME="${CONFIG_HOME}"
export CI=1
export FORGE_PROFILE="${FORGE_PROFILE:-demo}"
export FORGE_ENDPOINT="${FORGE_ENDPOINT:-${CONTROL_URL}}"
mkdir -p "${CONFIG_HOME}"

TRACKED_DEPLOYMENTS=()

# control_name:fixture_dir:hostname
PRODUCT_SERVICES=(
  "api:api-go:api.demo.localhost"
  "admin:admin-kotlin:admin.demo.localhost"
  "logs:log-worker-rust:logs.demo.localhost"
  "classify:classify-python:classify.demo.localhost"
  "notify:notify-elixir:notify.demo.localhost"
)

cleanup() {
  local dep
  if ((${#TRACKED_DEPLOYMENTS[@]} > 0)); then
    for dep in "${TRACKED_DEPLOYMENTS[@]}"; do
      [[ -n "${dep}" ]] || continue
      curl --silent --show-error -X DELETE "${CONTROL_URL}/v1/deployments/${dep}" >/dev/null 2>&1 || true
      docker rm -f "forge-${dep}" >/dev/null 2>&1 || true
    done
  fi
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

fail() {
  echo "Capstone deploy failed: $*" >&2
  echo "--- gateway routes ---" >&2
  curl --silent --show-error "${GATEWAY_URL}/admin/routes" >&2 || true
  echo >&2
  echo "--- events health ---" >&2
  curl --silent --show-error "${EVENTS_URL}/health/ready" >&2 || true
  echo >&2
  "${COMPOSE[@]}" logs --tail=80 forge-build forge-runtime forge-gateway forge-events >&2 || true
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
  python3 -c 'import json,sys,uuid; v=json.load(open(sys.argv[1]))["id"]; uuid.UUID(v); print(v)' "$1" ||
    fail "response missing UUID id: $(cat "$1")"
}

track_deployment() {
  TRACKED_DEPLOYMENTS+=("$1")
}

prepare_fixture_git() {
  local dir="$1" label="$2"
  echo "Preparing product Git repo (${label}) at ${dir}..."
  [[ -f "${dir}/Dockerfile" && -f "${dir}/forge.yaml" ]] ||
    fail "product ${label} missing Dockerfile or forge.yaml"
  if [[ ! -d "${dir}/.git" ]]; then
    git -C "${dir}" init -b main >/dev/null
    git -C "${dir}" config user.email "forge@local"
    git -C "${dir}" config user.name "forge"
  fi
  git -C "${dir}" add -A
  if ! git -C "${dir}" diff --cached --quiet; then
    git -C "${dir}" commit -m "capstone ${label}" >/dev/null
  elif [[ -z "$(git -C "${dir}" rev-parse --verify HEAD 2>/dev/null || true)" ]]; then
    git -C "${dir}" commit --allow-empty -m "capstone ${label}" >/dev/null
  fi
}

purge_stale_deployments() {
  echo "Purging leftover Control deployments (best effort)..."
  CONTROL_URL="${CONTROL_URL}" python3 - <<'PY' || true
import json, urllib.error, urllib.request, os
base = os.environ["CONTROL_URL"].rstrip("/")

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
for project in get("/v1/projects"):
    for app in get(f"/v1/projects/{project['id']}/applications"):
        for svc in get(f"/v1/applications/{app['id']}/services"):
            for dep in get(f"/v1/services/{svc['id']}/deployments"):
                delete(f"/v1/deployments/{dep['id']}")
                deleted += 1
print(f"deleted {deleted} deployment(s)")
PY
  docker ps -aq --filter "label=forge.managed=true" | while read -r cid; do
    [[ -n "${cid}" ]] || continue
    docker rm -f "${cid}" >/dev/null 2>&1 || true
  done
}

wipe_nats_volume() {
  echo "Resetting NATS JetStream so the incident stream family is clean..."
  "${COMPOSE[@]}" stop forge-events nats >/dev/null 2>&1 || true
  "${COMPOSE[@]}" rm -f forge-events nats >/dev/null 2>&1 || true
  local vol
  vol="$(docker volume ls -q --filter name=forge-nats-data | head -n 1 || true)"
  if [[ -n "${vol}" ]]; then
    docker volume rm -f "${vol}" >/dev/null 2>&1 || true
    echo "  removed volume ${vol}"
  fi
}

submit_build() {
  local repo="$1" service_id="$2" environment_id="$3" out="$4"
  local payload
  payload="$(SERVICE_ID="${service_id}" ENVIRONMENT_ID="${environment_id}" REPO="${repo}" PROJECT_NAME="${PROJECT_NAME}" python3 - <<'PY'
import json, os
print(json.dumps({
    "repo": os.environ["REPO"],
    "ref": "main",
    "forgeYamlPath": "forge.yaml",
    "project": os.environ["PROJECT_NAME"],
    "serviceId": os.environ["SERVICE_ID"],
    "environmentId": os.environ["ENVIRONMENT_ID"],
    "autoDeploy": False,
}))
PY
)"
  curl --fail --silent --show-error -X POST "${BUILD_URL}/v1/builds" \
    -H 'content-type: application/json' \
    -d "${payload}" >"${out}" || fail "POST /v1/builds failed for ${repo}"
}

wait_build_succeeded() {
  local build_id="$1" attempts="${2:-300}"
  local status="" image=""
  # Progress on stderr; stdout is ONLY the image ref (captured by callers).
  echo "Waiting for build ${build_id} succeeded ..." >&2
  for _ in $(seq 1 "${attempts}"); do
    curl --fail --silent --show-error "${BUILD_URL}/v1/builds/${build_id}" >"${TMP_DIR}/build.json" ||
      fail "GET build ${build_id} failed"
    status="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["status"])' "${TMP_DIR}/build.json")"
    image="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("image") or "")' "${TMP_DIR}/build.json")"
    if [[ "${status}" == "succeeded" && -n "${image}" ]]; then
      echo "  status=${status} image=${image}" >&2
      printf '%s\n' "${image}"
      return 0
    fi
    if [[ "${status}" == "failed" || "${status}" == "canceled" ]]; then
      curl --silent --show-error "${BUILD_URL}/v1/builds/${build_id}/logs" | tail -n 80 >&2 || true
      fail "build ${build_id} ended as ${status}"
    fi
    sleep 2
  done
  fail "build ${build_id} timed out (status=${status:-unknown})"
}

wait_deployment_status() {
  local deployment_id="$1" expected="$2" attempts="${3:-120}"
  local status=""
  echo "Waiting for deployment ${deployment_id} status=${expected} ..."
  for _ in $(seq 1 "${attempts}"); do
    forge_json "${TMP_DIR}/dep-status.json" deployment status "${deployment_id}"
    status="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["status"])' "${TMP_DIR}/dep-status.json")"
    if [[ "${status}" == "${expected}" ]] ||
      { [[ "${expected}" == "active" || "${expected}" == "deployed" ]] &&
        [[ "${status}" == "active" || "${status}" == "deployed" ]]; }; then
      echo "  status=${status}"
      return 0
    fi
    if [[ "${status}" == "failed" && "${expected}" != "failed" ]]; then
      fail "deployment ${deployment_id} failed (want ${expected})"
    fi
    sleep 2
  done
  fail "deployment ${deployment_id} status=${status:-unknown}, want ${expected}"
}

refresh_routes() {
  curl --fail --silent --show-error -X POST "${GATEWAY_URL}/admin/routes/refresh" >/dev/null ||
    fail "POST /admin/routes/refresh failed"
}

wait_route_host() {
  local host="$1" attempts="${2:-90}"
  echo "Waiting for gateway route host=${host} ..."
  for _ in $(seq 1 "${attempts}"); do
    refresh_routes
    curl --fail --silent --show-error "${GATEWAY_URL}/admin/routes" >"${TMP_DIR}/routes.json" ||
      fail "GET /admin/routes failed"
    if HOST="${host}" python3 -c '
import json, os, sys
host = os.environ["HOST"].lower()
routes = json.load(open(sys.argv[1]))
sys.exit(0 if any(str(r.get("host","")).lower()==host for r in routes) else 1)
' "${TMP_DIR}/routes.json"; then
      echo "  route present: ${host}"
      return 0
    fi
    sleep 2
  done
  fail "timed out waiting for route host=${host}"
}

assert_gateway_ready() {
  local host="$1"
  local code
  echo "Curling Host=${host} /health/ready via Gateway..."
  code="$(curl --silent --show-error -o "${TMP_DIR}/gw-body.json" -w '%{http_code}' \
    -H "Host: ${host}" "${GATEWAY_URL}/health/ready")" || true
  [[ "${code}" == "200" ]] ||
    fail "Host ${host} returned HTTP ${code}; body=$(cat "${TMP_DIR}/gw-body.json")"
  python3 -c 'import json,sys; b=json.load(open(sys.argv[1])); assert b.get("status")=="ok", b' \
    "${TMP_DIR}/gw-body.json" || fail "unexpected ready body for ${host}"
  echo "  ${host} → ready"
}

echo "== Capstone 19.02: Build → Runtime → Gateway → Events =="
echo "Auth: FORGE_AUTH_MODE=${FORGE_AUTH_MODE} (dev until 19.03 Identity)"

echo "Unit-testing deploy helpers..."
python3 -m unittest discover -s "${LIB_DIR}" -p 'test_*.py' -v || fail "deploy helper unit tests failed"

echo "Building forge CLI..."
make -C "${CLI_DIR}" build || fail "CLI build failed"
[[ -x "${FORGE_BIN}" ]] || fail "forge binary missing"

for entry in "${PRODUCT_SERVICES[@]}"; do
  IFS=':' read -r _control fixture _host <<<"${entry}"
  prepare_fixture_git "${PRODUCT_DIR}/${fixture}" "${fixture}"
done

wipe_nats_volume

echo "Starting postgres, registry, nats..."
"${COMPOSE[@]}" up -d postgres registry nats
wait_http "http://127.0.0.1:5003/healthz" "NATS monitoring" 60

echo "Starting Control, Runtime, Gateway, Build, Events..."
"${COMPOSE[@]}" up -d --build --force-recreate forge-control
wait_http "${CONTROL_URL}/health/ready" "Control"
"${COMPOSE[@]}" stop forge-runtime >/dev/null 2>&1 || true
purge_stale_deployments
"${COMPOSE[@]}" up -d --build --force-recreate forge-runtime forge-gateway forge-build forge-events
wait_http "${RUNTIME_URL}/health/ready" "Runtime"
wait_http "${GATEWAY_URL}/health/ready" "Gateway"
wait_http "${BUILD_URL}/health/ready" "Build"
wait_http "${EVENTS_URL}/health/ready" "Events"

docker exec forge-gateway printenv FORGE_HOST_PATTERN 2>/dev/null |
  grep -Fq '{service}' ||
  fail "gateway FORGE_HOST_PATTERN must contain '{service}' (got: $(docker exec forge-gateway printenv FORGE_HOST_PATTERN 2>/dev/null || true))"
echo "Gateway host pattern: $(docker exec forge-gateway printenv FORGE_HOST_PATTERN)"

# Confirm incident.created schema is registered.
curl --fail --silent --show-error "${EVENTS_URL}/v1/schemas" >"${TMP_DIR}/schemas.json"
python3 -c '
import json,sys
schemas=json.load(open(sys.argv[1]))
subjects=schemas if isinstance(schemas, list) else schemas.get("subjects") or list(schemas.keys())
flat=json.dumps(schemas)
assert "incident.created" in flat, schemas
print("schema incident.created registered")
' "${TMP_DIR}/schemas.json" || fail "incident.created schema missing"

echo "Configuring CLI profile ${FORGE_PROFILE} -> ${FORGE_ENDPOINT}..."
forge config set endpoint "${FORGE_ENDPOINT}" --profile "${FORGE_PROFILE}"
forge config use "${FORGE_PROFILE}"

SUFFIX="$(date +%s)-$$"
forge_json "${TMP_DIR}/project.json" project create --name "${PROJECT_NAME}-${SUFFIX}"
PROJECT_ID="$(read_id "${TMP_DIR}/project.json")"
forge_json "${TMP_DIR}/environment.json" env create --project "${PROJECT_ID}" --name development
ENVIRONMENT_ID="$(read_id "${TMP_DIR}/environment.json")"
forge_json "${TMP_DIR}/application.json" app create --project "${PROJECT_ID}" --name incident
APPLICATION_ID="$(read_id "${TMP_DIR}/application.json")"

# Persist ids to files (macOS /bin/bash 3.2 has no associative arrays).
SUMMARY_LINES=()

for entry in "${PRODUCT_SERVICES[@]}"; do
  IFS=':' read -r control_name fixture host <<<"${entry}"
  echo "--- service ${control_name} (${fixture}) → ${host} ---"
  forge_json "${TMP_DIR}/service-${control_name}.json" \
    service create --app "${APPLICATION_ID}" --name "${control_name}" --port 8080
  service_id="$(read_id "${TMP_DIR}/service-${control_name}.json")"
  echo "${service_id}" >"${TMP_DIR}/service-id-${control_name}.txt"

  submit_build \
    "file:///fixtures/product/${fixture}" \
    "${service_id}" \
    "${ENVIRONMENT_ID}" \
    "${TMP_DIR}/build-accept-${control_name}.json"
  BUILD_ID="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["buildId"])' \
    "${TMP_DIR}/build-accept-${control_name}.json")"
  image="$(wait_build_succeeded "${BUILD_ID}")"
  echo "${image}" >"${TMP_DIR}/image-${control_name}.txt"

  echo "forge deployment create for ${control_name}..."
  forge_json "${TMP_DIR}/deployment-${control_name}.json" deployment create \
    --service "${service_id}" \
    --image "${image}" \
    --env "${ENVIRONMENT_ID}" \
    --replicas 1
  deployment_id="$(read_id "${TMP_DIR}/deployment-${control_name}.json")"
  echo "${deployment_id}" >"${TMP_DIR}/deployment-id-${control_name}.txt"
  track_deployment "${deployment_id}"
  wait_deployment_status "${deployment_id}" "active"
  wait_route_host "${host}"
  assert_gateway_ready "${host}"
  SUMMARY_LINES+=("  ${control_name}: service=${service_id} deployment=${deployment_id}")
  SUMMARY_LINES+=("             image=${image}")
  SUMMARY_LINES+=("             host=${host}")
done

echo "Exercising product event path (api → Events → logs consumer)..."
curl --fail --silent --show-error -X POST \
  -H "Host: api.demo.localhost" \
  -H 'content-type: application/json' \
  -d '{"title":"Capstone deploy path","description":"19.02 smoke","severity":"high"}' \
  "${GATEWAY_URL}/incidents" >"${TMP_DIR}/incident.json"
INCIDENT_ID="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "${TMP_DIR}/incident.json")"
[[ -n "${INCIDENT_ID}" ]] || fail "create incident missing id"
echo "  created incident ${INCIDENT_ID}"

# Poll logs service events status via Gateway until processed.
echo "Waiting for logs consumer to process incident.created ..."
processed=0
for _ in $(seq 1 90); do
  code="$(curl --silent --show-error -o "${TMP_DIR}/ev-status.json" -w '%{http_code}' \
    -H "Host: logs.demo.localhost" "${GATEWAY_URL}/events/status")" || true
  if [[ "${code}" == "200" ]]; then
    processed="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("processed_count") or 0)' \
      "${TMP_DIR}/ev-status.json")"
    last="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("last_incident_id") or "")' \
      "${TMP_DIR}/ev-status.json")"
    if [[ "${processed}" -ge 1 && "${last}" == "${INCIDENT_ID}" ]]; then
      echo "  processed_count=${processed} last_incident_id=${last}"
      break
    fi
  fi
  sleep 1
done
[[ "${processed}" -ge 1 ]] || fail "logs consumer did not process incident.created (last status=$(cat "${TMP_DIR}/ev-status.json" 2>/dev/null || true))"

echo
echo "Capstone 19.02 deploy path passed."
echo "  Project:     ${PROJECT_ID}"
echo "  Environment: ${ENVIRONMENT_ID}"
echo "  Application: ${APPLICATION_ID}"
for line in "${SUMMARY_LINES[@]}"; do
  echo "${line}"
done
echo "  Incident:    ${INCIDENT_ID}"
echo "  Gateway:     ${GATEWAY_URL}"
echo "  Events:      ${EVENTS_URL}"
echo
echo "Manual check:"
echo "  curl -fsS -H 'Host: api.demo.localhost' ${GATEWAY_URL}/health/ready"
