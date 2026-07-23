#!/usr/bin/env bash
# Demo 09 full-platform (19.03–19.04): Identity/Secrets/Observe/Storage/DB plus
# Models + Agents + Memory diagnosis loop for the polyglot product.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/09-full-platform"
PRODUCT_DIR="${DEMO_DIR}/product"
LIB_DIR="${DEMO_DIR}/lib"

export FORGE_AUTH_MODE="${FORGE_AUTH_MODE:-enforce}"
export FORGE_EVENTS_AUTH_MODE="${FORGE_EVENTS_AUTH_MODE:-dev}"
export FORGE_OBSERVE_AUTH_MODE="${FORGE_OBSERVE_AUTH_MODE:-dev}"
export FORGE_STORAGE_AUTH_MODE="${FORGE_STORAGE_AUTH_MODE:-dev}"
export FORGE_EVENTS_STREAMS="${FORGE_EVENTS_STREAMS:-build,deployment,runtime,application,agent,incident}"
if [[ -z "${FORGE_HOST_PATTERN:-}" ]]; then
  FORGE_HOST_PATTERN='{service}.demo.localhost'
fi
export FORGE_HOST_PATTERN
export FORGE_RECONCILE_INTERVAL_SECONDS="${FORGE_RECONCILE_INTERVAL_SECONDS:-3}"
# Control owns lifecycle when managed-DB injection/reconcile is in play (demo 18).
export FORGE_LIFECYCLE_OWNER="${FORGE_LIFECYCLE_OWNER:-control}"
export FORGE_ROUTE_SYNC_INTERVAL_SECONDS="${FORGE_ROUTE_SYNC_INTERVAL_SECONDS:-3}"
export FORGE_UPSTREAM_PROBE_INTERVAL_SECONDS="${FORGE_UPSTREAM_PROBE_INTERVAL_SECONDS:-2}"
export FORGE_UPSTREAM_FAILURE_THRESHOLD="${FORGE_UPSTREAM_FAILURE_THRESHOLD:-1}"
export FORGE_UPSTREAM_SUCCESS_THRESHOLD="${FORGE_UPSTREAM_SUCCESS_THRESHOLD:-1}"
export FORGE_BUILD_TIMEOUT_SECONDS="${FORGE_BUILD_TIMEOUT_SECONDS:-1200}"
export FORGE_DB_PROVISIONER="${FORGE_DB_PROVISIONER:-local}"
export FORGE_DB_ENDPOINT_HOST="${FORGE_DB_ENDPOINT_HOST:-host.docker.internal}"
export FORGE_DB_MANAGED_NETWORK="${FORGE_DB_MANAGED_NETWORK:-forge-net}"
export FORGE_INJECT_MASK_IN_LOGS="${FORGE_INJECT_MASK_IN_LOGS:-true}"
export FORGE_INTROSPECT_CACHE_TTL_S="${FORGE_INTROSPECT_CACHE_TTL_S:-2}"
export FORGE_AUTHZ_CACHE_TTL_S="${FORGE_AUTHZ_CACHE_TTL_S:-2}"
export FORGE_MODELS_BACKEND="${FORGE_MODELS_BACKEND:-fake}"
export FORGE_AGENTS_TOOLS_MODE="${FORGE_AGENTS_TOOLS_MODE:-fake}"
export DOCKER_GID="${DOCKER_GID:-$(id -g)}"

if [[ -z "${FORGE_SECRETS_MASTER_KEY:-}" ]]; then
  FORGE_SECRETS_MASTER_KEY="$(python3 -c 'import base64,os; print(base64.b64encode(os.urandom(32)).decode())')"
fi
export FORGE_SECRETS_MASTER_KEY
export FORGE_SECRETS_MASTER_KEY_ID="${FORGE_SECRETS_MASTER_KEY_ID:-capstone-m1}"

COMPOSE=(
  docker compose
    -f "${ROOT_DIR}/compose.yaml"
    -f "${DEMO_DIR}/compose.yaml"
    --project-directory "${ROOT_DIR}"
)

GATEWAY_URL="${FORGE_GATEWAY_URL:-http://127.0.0.1:4000}"
CONTROL_URL="${FORGE_CONTROL_URL:-http://127.0.0.1:4001}"
IDENTITY_URL="${FORGE_IDENTITY_HOST_URL:-http://127.0.0.1:4002}"
RUNTIME_URL="${FORGE_RUNTIME_URL:-http://127.0.0.1:4102}"
BUILD_URL="${FORGE_BUILD_URL:-http://127.0.0.1:4103}"
SECRETS_URL="${FORGE_SECRETS_HOST_URL:-http://127.0.0.1:4104}"
EVENTS_URL="${FORGE_EVENTS_HOST_URL:-http://127.0.0.1:4105}"
OBSERVE_URL="${FORGE_OBSERVE_URL:-http://127.0.0.1:4106}"
STORAGE_URL="${FORGE_STORAGE_URL:-http://127.0.0.1:4107}"
TEMPO_URL="${FORGE_TEMPO_URL:-http://127.0.0.1:3002}"
MODELS_URL="${FORGE_MODELS_URL:-http://127.0.0.1:4300}"
AGENTS_URL="${FORGE_AGENTS_URL:-http://127.0.0.1:4301}"
MEMORY_URL="${FORGE_MEMORY_URL:-http://127.0.0.1:4303}"
REGISTRY="${FORGE_REGISTRY:-localhost:5000}"
PROJECT_NAME="${FORGE_CAPSTONE_PROJECT:-capstone}"
ENV_NAME="${FORGE_CAPSTONE_ENV:-development}"
APPLICATION_NAME="${FORGE_CAPSTONE_APP:-incident}"

CLI_DIR="${ROOT_DIR}/tools/forge-cli"
FORGE_BIN="${CLI_DIR}/forge"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-capstone-deploy.XXXXXX")"
CONFIG_HOME="${TMP_DIR}/xdg-config"
export XDG_CONFIG_HOME="${CONFIG_HOME}"
export CI=1
export FORGE_PROFILE="${FORGE_PROFILE:-demo}"
export FORGE_ENDPOINT="${FORGE_ENDPOINT:-${CONTROL_URL}}"
export FORGE_IDENTITY_URL="${IDENTITY_URL}"
export FORGE_SECRETS_URL="${SECRETS_URL}"
mkdir -p "${CONFIG_HOME}"

SUFFIX="$(date +%s)-$$"
OWNER_EMAIL="owner-${SUFFIX}@example.com"
OWNER_PASSWORD="OwnerPass123!"

TRACKED_DEPLOYMENTS=()

# control_name:fixture_dir:hostname
PRODUCT_SERVICES=(
  "api:api-go:api.demo.localhost"
  "admin:admin-kotlin:admin.demo.localhost"
  "logs:log-worker-rust:logs.demo.localhost"
  "classify:classify-python:classify.demo.localhost"
  "notify:notify-elixir:notify.demo.localhost"
)

# shellcheck source=setup-foundations.sh
source "${DEMO_DIR}/setup-foundations.sh"

cleanup() {
  local dep
  if ((${#TRACKED_DEPLOYMENTS[@]} > 0)); then
    for dep in "${TRACKED_DEPLOYMENTS[@]}"; do
      [[ -n "${dep}" ]] || continue
      curl --silent --show-error -H "Authorization: Bearer ${DEV_TOKEN:-}" \
        -X DELETE "${CONTROL_URL}/v1/deployments/${dep}" >/dev/null 2>&1 || true
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
  "${COMPOSE[@]}" logs --tail=80 forge-control forge-secrets forge-identity forge-build forge-runtime forge-gateway >&2 || true
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
  CONTROL_URL="${CONTROL_URL}" TOKEN="${SESSION_TOKEN:-${DEV_TOKEN:-}}" python3 - <<'PY' || true
import json, urllib.error, urllib.request, os
base = os.environ["CONTROL_URL"].rstrip("/")
token = os.environ.get("TOKEN") or ""

def req(method, path):
    headers = {}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    r = urllib.request.Request(base + path, method=method, headers=headers)
    try:
        with urllib.request.urlopen(r, timeout=10) as resp:
            if method == "GET":
                return json.load(resp)
            return None
    except urllib.error.HTTPError as exc:
        if exc.code in (401, 403, 404, 204):
            return [] if method == "GET" else None
        raise

deleted = 0
for project in req("GET", "/v1/projects") or []:
    for app in req("GET", f"/v1/projects/{project['id']}/applications") or []:
        for svc in req("GET", f"/v1/applications/{app['id']}/services") or []:
            for dep in req("GET", f"/v1/services/{svc['id']}/deployments") or []:
                req("DELETE", f"/v1/deployments/{dep['id']}")
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
  payload="$(SERVICE_ID="${service_id}" ENVIRONMENT_ID="${environment_id}" REPO="${repo}" PROJECT_NAME="${PROJECT_NAME}" TOKEN="${DEV_TOKEN}" python3 - <<'PY'
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
    -H "Authorization: Bearer ${DEV_TOKEN}" \
    -d "${payload}" >"${out}" || fail "POST /v1/builds failed for ${repo}"
}

wait_build_succeeded() {
  local build_id="$1" attempts="${2:-300}"
  local status="" image=""
  echo "Waiting for build ${build_id} succeeded ..." >&2
  for _ in $(seq 1 "${attempts}"); do
    curl --fail --silent --show-error \
      -H "Authorization: Bearer ${DEV_TOKEN}" \
      "${BUILD_URL}/v1/builds/${build_id}" >"${TMP_DIR}/build.json" ||
      fail "GET build ${build_id} failed"
    status="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["status"])' "${TMP_DIR}/build.json")"
    image="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("image") or "")' "${TMP_DIR}/build.json")"
    if [[ "${status}" == "succeeded" && -n "${image}" ]]; then
      echo "  status=${status} image=${image}" >&2
      printf '%s\n' "${image}"
      return 0
    fi
    if [[ "${status}" == "failed" || "${status}" == "canceled" ]]; then
      curl --silent --show-error -H "Authorization: Bearer ${DEV_TOKEN}" \
        "${BUILD_URL}/v1/builds/${build_id}/logs" | tail -n 80 >&2 || true
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
  # Control enforce blocks Gateway's unauthenticated /v1/endpoints sync.
  # Seed routes from Runtime node state + Control metadata (developer PAT).
  seed_gateway_routes_from_runtime ||
    curl --silent --show-error -X POST "${GATEWAY_URL}/admin/routes/refresh" >/dev/null || true
}

seed_gateway_routes_from_runtime() {
  # Runtime is auth=dev. Workload ids look like "<service>-<short8>-<index>".
  # Under Control enforce, Gateway cannot sync /v1/endpoints itself — seed admin routes.
  RUNTIME_URL="${RUNTIME_URL}" GATEWAY_URL="${GATEWAY_URL}" \
  HOST_PATTERN="${FORGE_HOST_PATTERN}" \
  KNOWN_SERVICES="api,admin,logs,classify,notify" \
  OUT="${TMP_DIR}/routes-seed.json" python3 - <<'PY'
import json, os, urllib.error, urllib.request

runtime = os.environ["RUNTIME_URL"].rstrip("/")
gateway = os.environ["GATEWAY_URL"].rstrip("/")
pattern = os.environ.get("HOST_PATTERN") or "{service}.demo.localhost"
known = {s.strip() for s in os.environ.get("KNOWN_SERVICES", "").split(",") if s.strip()}
out = os.environ["OUT"]

def get(url):
    req = urllib.request.Request(url, headers={"Accept": "application/json"})
    with urllib.request.urlopen(req, timeout=10) as resp:
        return json.load(resp)

def put(url, body):
    data = json.dumps(body).encode()
    req = urllib.request.Request(
        url, data=data, method="PUT",
        headers={"Content-Type": "application/json", "Accept": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=10) as resp:
        return resp.read()

try:
    state = get(f"{runtime}/v1/node/state")
except Exception as exc:
    print(f"runtime state unavailable: {exc}", flush=True)
    raise SystemExit(1)

routes_by_host = {}
for w in state.get("workloads") or []:
    if str(w.get("status") or "").lower() != "ready":
        continue
    port = int(w.get("hostPort") or 0)
    if port < 1:
        continue
    rid = str(w.get("deploymentId") or "")
    service = rid.split("-", 1)[0] if rid else ""
    if service not in known:
        # Fallback: longest known prefix match.
        service = next((s for s in sorted(known, key=len, reverse=True) if rid.startswith(s + "-")), "")
    if not service:
        continue
    host = pattern.replace("{service}", service).lower()
    url = f"http://host.docker.internal:{port}"
    entry = routes_by_host.setdefault(host, {"host": host, "upstreams": []})
    if not any(u.get("url") == url for u in entry["upstreams"]):
        entry["upstreams"].append({"url": url})

routes = [routes_by_host[h] for h in sorted(routes_by_host)]
open(out, "w").write(json.dumps(routes))
if not routes:
    print("no ready workloads to seed", flush=True)
    raise SystemExit(1)
try:
    put(f"{gateway}/admin/routes", routes)
except urllib.error.HTTPError as exc:
    print(f"PUT /admin/routes failed: {exc}", flush=True)
    raise SystemExit(1)
print(f"seeded {len(routes)} gateway route(s)", flush=True)
PY
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

gw_auth() {
  # usage: gw_auth HOST PATH [curl args...] → writes body to TMP_DIR/gw.json, prints status
  local host="$1" path="$2"
  shift 2
  curl --silent --show-error -o "${TMP_DIR}/gw.json" -w '%{http_code}' \
    -H "Host: ${host}" \
    -H "Authorization: Bearer ${DEV_TOKEN}" \
    "$@" \
    "${GATEWAY_URL}${path}"
}

assert_product_foundations() {
  echo "Asserting product foundations via Gateway..."

  local code
  code="$(gw_auth api.demo.localhost /db-status)"
  [[ "${code}" == "200" ]] || fail "db-status HTTP ${code}: $(cat "${TMP_DIR}/gw.json")"
  PYTHONPATH="${LIB_DIR}" python3 -c '
import json,sys
from foundations_helpers import db_status_ok
assert db_status_ok(open(sys.argv[1]).read()), open(sys.argv[1]).read()
print("db-status OK (postgres, no plaintext URL)")
' "${TMP_DIR}/gw.json" || fail "db-status assertion failed"

  code="$(gw_auth api.demo.localhost /secret-status)"
  [[ "${code}" == "200" ]] || fail "secret-status HTTP ${code}: $(cat "${TMP_DIR}/gw.json")"
  PYTHONPATH="${LIB_DIR}" APP_SHARED_SECRET="${APP_SHARED_SECRET}" python3 -c '
import json,os,sys
from foundations_helpers import secret_status_ok
body=open(sys.argv[1]).read()
assert secret_status_ok(body, [os.environ["APP_SHARED_SECRET"]]), body
print("secret-status OK (present, no plaintext)")
' "${TMP_DIR}/gw.json" || fail "secret-status assertion failed"

  # Unauthorized product request
  code="$(curl --silent --show-error -o "${TMP_DIR}/gw.json" -w '%{http_code}' \
    -H "Host: api.demo.localhost" "${GATEWAY_URL}/incidents")" || true
  [[ "${code}" == "401" ]] || fail "anonymous /incidents expected 401, got ${code}"

  # Persist incident in managed DB
  code="$(gw_auth api.demo.localhost /incidents -X POST -H 'content-type: application/json' \
    -d '{"title":"Foundations incident","description":"19.03 DB","severity":"high"}')"
  [[ "${code}" == "201" ]] || fail "create incident HTTP ${code}: $(cat "${TMP_DIR}/gw.json")"
  INCIDENT_ID="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "${TMP_DIR}/gw.json")"
  code="$(gw_auth api.demo.localhost "/incidents/${INCIDENT_ID}")"
  [[ "${code}" == "200" ]] || fail "get incident HTTP ${code}"
  echo "  incident ${INCIDENT_ID} persisted"

  # Storage artifact round-trip via product API
  code="$(gw_auth api.demo.localhost '/artifacts?key=capstone-bundle.txt' -X POST \
    -H 'content-type: application/octet-stream' \
    --data-binary $'capstone log bundle\nline-2\n')"
  [[ "${code}" == "201" ]] || fail "upload artifact HTTP ${code}: $(cat "${TMP_DIR}/gw.json")"
  ARTIFACT_KEY="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["key"])' "${TMP_DIR}/gw.json")"
  code="$(gw_auth api.demo.localhost "/artifacts/${ARTIFACT_KEY}" -o "${TMP_DIR}/artifact.bin")"
  # gw_auth always writes to gw.json; re-fetch raw body
  curl --fail --silent --show-error -o "${TMP_DIR}/artifact.bin" \
    -H "Host: api.demo.localhost" \
    -H "Authorization: Bearer ${DEV_TOKEN}" \
    "${GATEWAY_URL}/artifacts/${ARTIFACT_KEY}" || fail "download artifact failed"
  grep -q 'capstone log bundle' "${TMP_DIR}/artifact.bin" || fail "artifact contents mismatch"
  echo "  storage artifact ${ARTIFACT_KEY} round-trip OK"

  # Events path still works
  code="$(curl --silent --show-error -o "${TMP_DIR}/ev-status.json" -w '%{http_code}' \
    -H "Host: logs.demo.localhost" "${GATEWAY_URL}/events/status")" || true
  echo "  logs events/status HTTP ${code}"

  # Masking: Control/Runtime logs must not contain APP_SHARED_SECRET plaintext
  if docker logs --since 10m forge-control 2>&1 | grep -Fq "${APP_SHARED_SECRET}"; then
    fail "APP_SHARED_SECRET plaintext found in forge-control logs"
  fi
  if docker logs --since 10m forge-runtime 2>&1 | grep -Fq "${APP_SHARED_SECRET}"; then
    fail "APP_SHARED_SECRET plaintext found in forge-runtime logs"
  fi
  echo "  secret masking in platform logs OK"
}

assert_distributed_trace() {
  echo "Asserting distributed product trace in Tempo..."
  local tp_out traceparent trace_id
  tp_out="$(python3 - <<'PY'
import secrets
trace_id = secrets.token_hex(16)
span_id = secrets.token_hex(8)
print(f"00-{trace_id}-{span_id}-01")
print(trace_id)
PY
)"
  TRACEPARENT="$(echo "${tp_out}" | sed -n '1p')"
  TRACE_ID="$(echo "${tp_out}" | sed -n '2p')"
  echo "  traceparent=${TRACEPARENT}"

  for host_path in \
    "api.demo.localhost:/" \
    "admin.demo.localhost:/" \
    "classify.demo.localhost:/"; do
    host="${host_path%%:*}"
    path="${host_path#*:}"
    curl --silent --show-error -o /dev/null \
      -H "Host: ${host}" \
      -H "traceparent: ${TRACEPARENT}" \
      -H "Authorization: Bearer ${DEV_TOKEN}" \
      "${GATEWAY_URL}${path}" || true
  done
  # Also hit classify POST for a richer span.
  curl --silent --show-error -o /dev/null \
    -H "Host: classify.demo.localhost" \
    -H "traceparent: ${TRACEPARENT}" \
    -H 'content-type: application/json' \
    -d '{"text":"database timeout in api"}' \
    "${GATEWAY_URL}/classify" || true

  local found="" ok=0
  for _ in $(seq 1 60); do
    curl --silent --show-error -o "${TMP_DIR}/tempo.json" \
      -H 'Accept: application/json' \
      "${TEMPO_URL}/api/traces/${TRACE_ID}" || true
    found="$(PYTHONPATH="${LIB_DIR}" python3 -c '
import sys
from foundations_helpers import tempo_service_names
names=tempo_service_names(open(sys.argv[1]).read())
print(",".join(sorted(names)))
' "${TMP_DIR}/tempo.json" 2>/dev/null || true)"
    if FOUND="${found}" python3 - <<'PY'
import os,sys
found={p.strip() for p in os.environ.get("FOUND","").split(",") if p.strip()}
want={"incident-api","incident-admin","incident-classify"}
# accept short names too
norm={x.removeprefix("forge-") for x in found}
sys.exit(0 if len(want & norm) >= 3 else 1)
PY
    then
      ok=1
      break
    fi
    sleep 2
  done
  [[ "${ok}" -eq 1 ]] || fail "Tempo trace ${TRACE_ID} missing product services (found=${found})"
  echo "  Tempo services: ${found}"
}

echo "== Capstone 19.03: Identity / Secrets / Observe / Storage / managed DB =="
echo "Auth: FORGE_AUTH_MODE=${FORGE_AUTH_MODE}"
[[ "${FORGE_AUTH_MODE}" == "enforce" ]] || fail "FORGE_AUTH_MODE must be enforce for 19.03"

echo "Unit-testing helpers..."
python3 -m unittest discover -s "${LIB_DIR}" -p 'test_*.py' -v || fail "helper unit tests failed"

echo "Building forge CLI..."
make -C "${CLI_DIR}" build || fail "CLI build failed"
[[ -x "${FORGE_BIN}" ]] || fail "forge binary missing"

for entry in "${PRODUCT_SERVICES[@]}"; do
  IFS=':' read -r _control fixture _host <<<"${entry}"
  prepare_fixture_git "${PRODUCT_DIR}/${fixture}" "${fixture}"
done

wipe_nats_volume

echo "Starting infra (postgres, registry, nats, OTEL stack)..."
"${COMPOSE[@]}" up -d postgres registry nats otel-collector tempo loki prometheus
wait_http "http://127.0.0.1:5003/healthz" "NATS monitoring" 60
wait_http "${TEMPO_URL}/ready" "Tempo" 90

echo "Starting Identity + Secrets..."
for name in forge-identity forge-secrets; do
  docker rm -f "${name}" >/dev/null 2>&1 || true
done
"${COMPOSE[@]}" up -d --build --force-recreate forge-identity
wait_http "${IDENTITY_URL}/health/ready" "Identity"
"${COMPOSE[@]}" up -d --build --force-recreate forge-secrets
wait_http "${SECRETS_URL}/health/ready" "Secrets"

echo "Starting Control (enforce + LocalProvisioner)..."
docker rm -f forge-control >/dev/null 2>&1 || true
"${COMPOSE[@]}" up -d --build --force-recreate forge-control
wait_http "${CONTROL_URL}/health/ready" "Control"

echo "Configuring CLI profile ${FORGE_PROFILE} -> ${FORGE_ENDPOINT}..."
forge config set endpoint "${FORGE_ENDPOINT}" --profile "${FORGE_PROFILE}"
forge config use "${FORGE_PROFILE}"

setup_identity_owner
setup_control_project
setup_role_tokens
issue_secrets_service_account

echo "Recreating Control with secrets resolve token..."
"${COMPOSE[@]}" up -d --force-recreate --no-deps forge-control
wait_http "${CONTROL_URL}/health/ready" "Control"

echo "Starting Runtime, Gateway, Build, Events, Observe, Storage, Models, Memory, Agents..."
"${COMPOSE[@]}" stop forge-runtime >/dev/null 2>&1 || true
purge_stale_deployments
"${COMPOSE[@]}" up -d --build --force-recreate \
  forge-runtime forge-gateway forge-build forge-events forge-observe forge-storage \
  forge-models forge-memory forge-agents
wait_http "${RUNTIME_URL}/health/ready" "Runtime"
wait_http "${GATEWAY_URL}/health/ready" "Gateway"
wait_http "${BUILD_URL}/health/ready" "Build"
wait_http "${EVENTS_URL}/health/ready" "Events"
wait_http "${OBSERVE_URL}/health/ready" "Observe"
wait_http "${STORAGE_URL}/health/ready" "Storage"
wait_http "${MODELS_URL}/health/ready" "Models"
wait_http "${MEMORY_URL}/health/ready" "Memory"
wait_http "${AGENTS_URL}/health/ready" "Agents"

docker exec forge-gateway printenv FORGE_HOST_PATTERN 2>/dev/null |
  grep -Fq '{service}' ||
  fail "gateway FORGE_HOST_PATTERN must contain '{service}'"

# Confirm incident.created schema is registered.
curl --fail --silent --show-error "${EVENTS_URL}/v1/schemas" >"${TMP_DIR}/schemas.json"
python3 -c '
import json,sys
schemas=json.load(open(sys.argv[1]))
flat=json.dumps(schemas)
assert "incident.created" in flat, schemas
print("schema incident.created registered")
' "${TMP_DIR}/schemas.json" || fail "incident.created schema missing"

# Create api service early so viewer denial can target a real service id.
forge_json "${TMP_DIR}/service-api.json" \
  service create --app "${APPLICATION_ID}" --name api --port 8080
API_SERVICE_ID="$(read_id "${TMP_DIR}/service-api.json")"
echo "${API_SERVICE_ID}" >"${TMP_DIR}/service-id-api.txt"
assert_viewer_denied_deploy "${API_SERVICE_ID}"

setup_secrets_and_bindings "api"
setup_managed_db
setup_storage_bucket
# Light OTEL/config bindings for other instrumented services (no secret rotation).
for slug in admin classify; do
  forge --project "${PROJECT_ID}" --env "${ENV_NAME}" config set "FORGE_OTEL_ENABLED=true" >/dev/null || true
  body="$(python3 - <<'PY'
import json
print(json.dumps({"secrets": [], "config": ["FORGE_OTEL_ENABLED", "FORGE_OTEL_EXPORTER_ENDPOINT"]}))
PY
)"
  secrets_json PUT \
    "/v1/projects/${PROJECT_ID}/envs/${ENV_NAME}/services/${slug}/bindings" \
    "${SESSION_TOKEN}" "${body}" "${TMP_DIR}/bindings-${slug}.json" >/dev/null || true
done
write_foundations_state

# Re-login as developer for CLI deploy path.
forge login --token "${DEV_TOKEN}" || fail "forge login developer failed"

SUMMARY_LINES=()

for entry in "${PRODUCT_SERVICES[@]}"; do
  IFS=':' read -r control_name fixture host <<<"${entry}"
  echo "--- service ${control_name} (${fixture}) → ${host} ---"
  if [[ "${control_name}" == "api" ]]; then
    service_id="${API_SERVICE_ID}"
  else
    forge_json "${TMP_DIR}/service-${control_name}.json" \
      service create --app "${APPLICATION_ID}" --name "${control_name}" --port 8080
    service_id="$(read_id "${TMP_DIR}/service-${control_name}.json")"
  fi
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

assert_product_foundations
assert_distributed_trace

echo "Running AI diagnosis loop (19.04) against live stack..."
FORGE_AI_SKIP_COMPOSE=1 \
FORGE_AI_KEEP=1 \
FORGE_MODELS_URL="${MODELS_URL}" \
FORGE_AGENTS_URL="${AGENTS_URL}" \
FORGE_MEMORY_URL="${MEMORY_URL}" \
FORGE_MEMORY_PROJECT="${PROJECT_NAME}" \
FORGE_MEMORY_PROJECT_B="${PROJECT_NAME}-b" \
FORGE_AGENTS_TOOLS_MODE="${FORGE_AGENTS_TOOLS_MODE}" \
FORGE_MODELS_BACKEND="${FORGE_MODELS_BACKEND}" \
  "${DEMO_DIR}/ai/verify-diagnosis.sh" || fail "19.04 AI diagnosis verification failed"

# Events consumer catch-up (best effort after authenticated create).
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
    if [[ "${processed}" -ge 1 && ( -z "${INCIDENT_ID:-}" || "${last}" == "${INCIDENT_ID}" ) ]]; then
      echo "  processed_count=${processed} last_incident_id=${last}"
      break
    fi
  fi
  sleep 1
done
[[ "${processed}" -ge 1 ]] || echo "  WARN: logs consumer lag (non-fatal for 19.03 foundations); status=$(cat "${TMP_DIR}/ev-status.json" 2>/dev/null || true)"

echo
echo "Capstone 19.03–19.04 foundations + AI diagnosis passed."
echo "  Project:     ${PROJECT_ID}"
echo "  Environment: ${ENVIRONMENT_ID}"
echo "  Application: ${APPLICATION_ID}"
for line in "${SUMMARY_LINES[@]}"; do
  echo "${line}"
done
echo "  Incident:    ${INCIDENT_ID:-}"
echo "  Trace:       ${TRACE_ID:-}"
echo "  Gateway:     ${GATEWAY_URL}"
echo "  Identity:    ${IDENTITY_URL}"
echo "  Secrets:     ${SECRETS_URL}"
echo "  Storage:     ${STORAGE_URL}"
echo "  Observe:     ${OBSERVE_URL}"
echo "  Models:      ${MODELS_URL}"
echo "  Agents:      ${AGENTS_URL}"
echo "  Memory:      ${MEMORY_URL}"
echo
echo "Manual checks:"
echo "  # viewer cannot deploy (already asserted)"
echo "  curl -fsS -H 'Host: api.demo.localhost' -H \"Authorization: Bearer \$DEV_TOKEN\" ${GATEWAY_URL}/db-status"
echo "  curl -fsS -H 'Host: api.demo.localhost' -H \"Authorization: Bearer \$DEV_TOKEN\" ${GATEWAY_URL}/secret-status"
echo "  ./ai/seed-memory.sh"
echo "  forge agent run deployment-investigator --project ${PROJECT_NAME} --deployment dep-capstone --dry-run"
