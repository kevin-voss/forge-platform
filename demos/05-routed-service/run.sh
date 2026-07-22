#!/usr/bin/env bash
# Demo 05: multi-language routing through Forge Gateway (epic gate).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# Pre-09 demos opt into the insecure auth bypass (Control default is enforce as of 09.06).
export FORGE_AUTH_MODE="${FORGE_AUTH_MODE:-dev}"
COMPOSE=(docker compose -f "${ROOT_DIR}/compose.yaml" --project-directory "${ROOT_DIR}")
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
GO_APP_DIR="${ROOT_DIR}/demos/01-container-runtime/apps/go"
RUST_APP_DIR="${ROOT_DIR}/demos/01-container-runtime/apps/rust"
PYTHON_APP_DIR="${ROOT_DIR}/demos/01-container-runtime/apps/python"
GO_IMAGE="${GO_IMAGE:-localhost:5000/demo-go:latest}"
RUST_IMAGE="${RUST_IMAGE:-localhost:5000/demo-rust:latest}"
PYTHON_IMAGE="${PYTHON_IMAGE:-localhost:5000/demo-python:latest}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-gateway-demo.XXXXXX")"
CONFIG_HOME="${TMP_DIR}/xdg-config"
ECHO_PID=""
export XDG_CONFIG_HOME="${CONFIG_HOME}"
export CI=1
export FORGE_PROFILE="${FORGE_PROFILE:-demo}"
export FORGE_ENDPOINT="${FORGE_ENDPOINT:-${CONTROL_URL}}"
# Faster converge / sync / unready detection for the acceptance gate.
export FORGE_RECONCILE_INTERVAL_SECONDS="${FORGE_RECONCILE_INTERVAL_SECONDS:-3}"
export FORGE_LIFECYCLE_OWNER="${FORGE_LIFECYCLE_OWNER:-runtime}"
# Avoid nested `${var:-{braces}}` — bash truncates the default at the inner `}`.
if [[ -z "${FORGE_HOST_PATTERN:-}" ]]; then
  FORGE_HOST_PATTERN='{service}.demo.localhost'
fi
export FORGE_HOST_PATTERN
export FORGE_ROUTE_SYNC_INTERVAL_SECONDS="${FORGE_ROUTE_SYNC_INTERVAL_SECONDS:-3}"
export FORGE_UPSTREAM_PROBE_INTERVAL_SECONDS="${FORGE_UPSTREAM_PROBE_INTERVAL_SECONDS:-2}"
export FORGE_UPSTREAM_FAILURE_THRESHOLD="${FORGE_UPSTREAM_FAILURE_THRESHOLD:-1}"
export FORGE_UPSTREAM_SUCCESS_THRESHOLD="${FORGE_UPSTREAM_SUCCESS_THRESHOLD:-1}"
mkdir -p "${CONFIG_HOME}"

TRACKED_DEPLOYMENTS=()

cleanup() {
  local dep
  if [[ -n "${ECHO_PID}" ]]; then
    kill "${ECHO_PID}" >/dev/null 2>&1 || true
  fi
  if ((${#TRACKED_DEPLOYMENTS[@]} > 0)); then
    for dep in "${TRACKED_DEPLOYMENTS[@]}"; do
      [[ -n "${dep}" ]] || continue
      curl --silent --show-error -X DELETE "${CONTROL_URL}/v1/deployments/${dep}" >/dev/null 2>&1 || true
      docker rm -f "forge-${dep}" >/dev/null 2>&1 || true
    done
  fi
  "${COMPOSE[@]}" stop "${GATEWAY_SERVICE}" "${RUNTIME_SERVICE}" "${CONTROL_SERVICE}" >/dev/null 2>&1 || true
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

dump_context() {
  echo "--- ${GATEWAY_SERVICE} /admin/routes ---" >&2
  curl --silent --show-error "${GATEWAY_URL}/admin/routes" >&2 || true
  echo >&2
  echo "--- ${GATEWAY_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=120 "${GATEWAY_SERVICE}" >&2 || true
  echo "--- ${RUNTIME_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=80 "${RUNTIME_SERVICE}" >&2 || true
  echo "--- ${CONTROL_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${CONTROL_SERVICE}" >&2 || true
  echo "--- docker ps -a (forge-*) ---" >&2
  docker ps -a --filter name=forge- --format 'table {{.Names}}\t{{.Status}}\t{{.Image}}' >&2 || true
}

fail() {
  echo "Demo 05 failed: $*" >&2
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
  local local_tag="$1" app_dir="$2" registry_ref="$3"
  echo "Ensuring ${registry_ref} is in the local registry..."
  docker image inspect "${local_tag}" >/dev/null 2>&1 ||
    docker build -t "${local_tag}" "${app_dir}" ||
    fail "could not build ${local_tag}"
  docker tag "${local_tag}" "${registry_ref}" || fail "could not tag ${registry_ref}"
  docker push "${registry_ref}" >/dev/null || fail "could not push ${registry_ref}"
}

# Host pattern `{service}.demo.localhost` collapses every service name globally.
# Purge leftover Control deployments so go/rust/python each have one upstream.
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
}

create_hierarchy() {
  local suffix="$1"
  forge_json "${TMP_DIR}/project.json" project create --name "demo-gateway-${suffix}"
  PROJECT_ID="$(read_id "${TMP_DIR}/project.json")"
  forge_json "${TMP_DIR}/environment.json" env create --project "${PROJECT_ID}" --name development
  ENVIRONMENT_ID="$(read_id "${TMP_DIR}/environment.json")"
  forge_json "${TMP_DIR}/application.json" app create --project "${PROJECT_ID}" --name demos
  APPLICATION_ID="$(read_id "${TMP_DIR}/application.json")"

  forge_json "${TMP_DIR}/svc-go.json" service create --app "${APPLICATION_ID}" --name go --port 8080
  GO_SERVICE_ID="$(read_id "${TMP_DIR}/svc-go.json")"
  forge_json "${TMP_DIR}/svc-rust.json" service create --app "${APPLICATION_ID}" --name rust --port 8080
  RUST_SERVICE_ID="$(read_id "${TMP_DIR}/svc-rust.json")"
  forge_json "${TMP_DIR}/svc-python.json" service create --app "${APPLICATION_ID}" --name python --port 8080
  PYTHON_SERVICE_ID="$(read_id "${TMP_DIR}/svc-python.json")"
}

deploy_service() {
  local service_id="$1" image="$2" label="$3"
  forge_json "${TMP_DIR}/dep-${label}.json" deployment create \
    --service "${service_id}" \
    --image "${image}" \
    --env "${ENVIRONMENT_ID}" \
    --replicas 1
  local dep_id
  dep_id="$(read_id "${TMP_DIR}/dep-${label}.json")"
  track_deployment "${dep_id}"
  echo "${dep_id}"
}

wait_deployment_status() {
  local deployment_id="$1" expected="$2" attempts="${3:-90}"
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

gateway_started_at() {
  docker inspect -f '{{.State.StartedAt}}' "${GATEWAY_SERVICE}" 2>/dev/null ||
    fail "could not read gateway StartedAt"
}

assert_gateway_not_restarted() {
  local before="$1" label="$2"
  local after
  after="$(gateway_started_at)"
  [[ "${after}" == "${before}" ]] ||
    fail "gateway restarted during ${label} (before=${before}, after=${after})"
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
    sleep 2
  done
  fail "timed out waiting for route host=${host} (see /admin/routes dump above on failure)"
}

curl_host() {
  local host="$1"
  shift
  curl --silent --show-error -H "Host: ${host}" "$@" "${GATEWAY_URL}/"
}

assert_language() {
  local host="$1" language="$2"
  local body
  body="$(curl_host "${host}" --fail)" || fail "curl Host=${host} failed"
  echo "${body}" | LANGUAGE="${language}" python3 -c '
import json, os, sys
body = json.load(sys.stdin)
want = os.environ["LANGUAGE"]
got = body.get("language")
assert got == want, f"Host language={got!r}, want {want!r}; body={body!r}"
' || fail "Host ${host} did not return language=${language}: ${body}"
  echo "  ${host} → language=${language}"
}

assert_http_code() {
  local host="$1" want_code="$2"
  local code
  code="$(curl --silent --show-error -o "${TMP_DIR}/code-body.json" -w '%{http_code}' \
    -H "Host: ${host}" "${GATEWAY_URL}/")" || true
  [[ "${code}" == "${want_code}" ]] ||
    fail "Host ${host} returned HTTP ${code}, want ${want_code}; body=$(cat "${TMP_DIR}/code-body.json")"
  if [[ "${want_code}" == "503" ]]; then
    python3 -c '
import json, sys
body = json.load(open(sys.argv[1]))
code = body.get("error", {}).get("code")
assert code == "no_healthy_upstream", body
' "${TMP_DIR}/code-body.json" ||
      fail "503 body missing no_healthy_upstream: $(cat "${TMP_DIR}/code-body.json")"
  fi
  echo "  ${host} → HTTP ${want_code}"
}

upstream_url_for_host() {
  local host="$1"
  dump_routes
  HOST="${host}" python3 -c '
import json, os, sys
host = os.environ["HOST"].lower()
routes = json.load(open(sys.argv[1]))
for r in routes:
    if r.get("host", "").lower() == host:
        ups = r.get("upstreams") or []
        if ups and ups[0].get("url"):
            print(ups[0]["url"])
            sys.exit(0)
sys.exit("no upstream for " + host)
' "${TMP_DIR}/routes.json" || fail "could not find upstream for ${host}"
}

start_echo_upstream() {
  local port="$1"
  python3 - "$port" <<'PY' &
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

port = int(sys.argv[1])

class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        return

    def do_GET(self):
        if self.path in ("/health/live", "/health/ready"):
            body = b'{"status":"ok"}\n'
        else:
            rid = self.headers.get("X-Request-Id", "")
            body = (json.dumps({"echo_request_id": rid, "path": self.path}) + "\n").encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

ThreadingHTTPServer(("0.0.0.0", port), Handler).serve_forever()
PY
  ECHO_PID=$!
  for _ in $(seq 1 30); do
    if curl --fail --silent --show-error "http://127.0.0.1:${port}/health/ready" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.2
  done
  fail "echo upstream on :${port} did not become ready"
}

assert_request_id_propagation() {
  local echo_port=18905
  local req_id="demo-05-req-$$"
  echo "Asserting X-Request-Id propagation via echo upstream..."
  start_echo_upstream "${echo_port}"

  dump_routes
  ECHO_PORT="${echo_port}" python3 -c '
import json, os, sys
routes = json.load(open(sys.argv[1]))
routes.append({
    "host": "echo.demo.localhost",
    "pathPrefix": "/",
    "upstreams": [{"url": "http://host.docker.internal:%s" % os.environ["ECHO_PORT"]}],
    "strategy": "round_robin",
})
json.dump(routes, open(sys.argv[2], "w"))
' "${TMP_DIR}/routes.json" "${TMP_DIR}/routes-with-echo.json" || fail "could not build echo route table"

  curl --fail --silent --show-error -X PUT "${GATEWAY_URL}/admin/routes" \
    -H 'content-type: application/json' \
    --data-binary @"${TMP_DIR}/routes-with-echo.json" >/dev/null ||
    fail "PUT /admin/routes (echo) failed"

  local headers body echoed
  headers="$(mktemp "${TMP_DIR}/hdrs.XXXXXX")"
  body="$(curl --fail --silent --show-error -D "${headers}" \
    -H 'Host: echo.demo.localhost' \
    -H "X-Request-Id: ${req_id}" \
    "${GATEWAY_URL}/")" || fail "echo upstream curl failed"

  grep -qi "^X-Request-Id: ${req_id}" "${headers}" ||
    fail "gateway did not echo X-Request-Id on response; headers=$(cat "${headers}")"

  echoed="$(echo "${body}" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("echo_request_id",""))')" ||
    fail "echo upstream body was not JSON: ${body}"
  [[ "${echoed}" == "${req_id}" ]] ||
    fail "upstream did not receive X-Request-Id=${req_id}, got '${echoed}'; body=${body}"

  echo "  X-Request-Id ${req_id} echoed by client response and upstream"

  # Restore Control/Runtime-derived routes (no gateway restart).
  refresh_routes
  if [[ -n "${ECHO_PID}" ]]; then
    kill "${ECHO_PID}" >/dev/null 2>&1 || true
    wait "${ECHO_PID}" 2>/dev/null || true
    ECHO_PID=""
  fi
}

echo "== Demo 05: Forge Gateway routed service =="
echo "Building forge CLI..."
make -C "${CLI_DIR}" build || fail "CLI build failed"
[[ -x "${FORGE_BIN}" ]] || fail "forge binary missing at ${FORGE_BIN}"

echo "Starting PostgreSQL, registry, Control, Runtime, and Gateway..."
"${COMPOSE[@]}" up -d "${POSTGRES_SERVICE}" "${REGISTRY_SERVICE}"
"${COMPOSE[@]}" up -d --build --force-recreate "${CONTROL_SERVICE}"
wait_http "${CONTROL_URL}/health/ready" "Control"
# Stop Runtime while purging so it cannot recreate deleted desired state mid-cleanup.
"${COMPOSE[@]}" stop "${RUNTIME_SERVICE}" >/dev/null 2>&1 || true
purge_stale_deployments
"${COMPOSE[@]}" up -d --build --force-recreate "${RUNTIME_SERVICE}"
wait_http "${RUNTIME_URL}/health/ready" "Runtime"
"${COMPOSE[@]}" up -d --build --force-recreate "${GATEWAY_SERVICE}"
wait_http "${GATEWAY_URL}/health/ready" "Gateway"
# Confirm host pattern survived Compose/YAML (braces must remain substitutable tokens).
docker exec "${GATEWAY_SERVICE}" printenv FORGE_HOST_PATTERN 2>/dev/null |
  grep -Fq '{service}' ||
  fail "gateway FORGE_HOST_PATTERN must contain '{service}' (got: $(docker exec "${GATEWAY_SERVICE}" printenv FORGE_HOST_PATTERN 2>/dev/null || true))"

ensure_demo_image "forge/demo-go-api:local" "${GO_APP_DIR}" "${GO_IMAGE}"
ensure_demo_image "forge/demo-rust-api:local" "${RUST_APP_DIR}" "${RUST_IMAGE}"
ensure_demo_image "forge/demo-python-api:local" "${PYTHON_APP_DIR}" "${PYTHON_IMAGE}"

echo "Configuring CLI profile ${FORGE_PROFILE} -> ${FORGE_ENDPOINT}..."
forge config set endpoint "${FORGE_ENDPOINT}" --profile "${FORGE_PROFILE}"
forge config use "${FORGE_PROFILE}"

SUFFIX="$(date +%s)-$$"
create_hierarchy "${SUFFIX}"

echo "Deploying Go / Rust / Python demo workloads..."
GO_DEPLOYMENT_ID="$(deploy_service "${GO_SERVICE_ID}" "${GO_IMAGE}" go)"
RUST_DEPLOYMENT_ID="$(deploy_service "${RUST_SERVICE_ID}" "${RUST_IMAGE}" rust)"
PYTHON_DEPLOYMENT_ID="$(deploy_service "${PYTHON_SERVICE_ID}" "${PYTHON_IMAGE}" python)"

wait_deployment_status "${GO_DEPLOYMENT_ID}" "active"
wait_deployment_status "${RUST_DEPLOYMENT_ID}" "active"
wait_deployment_status "${PYTHON_DEPLOYMENT_ID}" "active"

wait_route_host "go.demo.localhost"
wait_route_host "rust.demo.localhost"
wait_route_host "python.demo.localhost"

GATEWAY_STARTED="$(gateway_started_at)"
echo "Gateway StartedAt=${GATEWAY_STARTED}"

echo "Curling each language through the gateway by Host header..."
assert_language "go.demo.localhost" "go"
assert_language "rust.demo.localhost" "rust"
assert_language "python.demo.localhost" "python"
assert_gateway_not_restarted "${GATEWAY_STARTED}" "hostname routing"

assert_request_id_propagation
assert_gateway_not_restarted "${GATEWAY_STARTED}" "request-id check"
# Ensure language routes still present after echo restore.
wait_route_host "go.demo.localhost" 30
assert_language "go.demo.localhost" "go"

echo "Stopping rust workload; expect 503 without gateway restart..."
# Pause Runtime reconcile so it cannot immediately restart the stopped container.
"${COMPOSE[@]}" stop "${RUNTIME_SERVICE}" >/dev/null ||
  fail "could not stop ${RUNTIME_SERVICE} for the unready window"
docker stop "forge-${RUST_DEPLOYMENT_ID}" >/dev/null ||
  fail "could not stop forge-${RUST_DEPLOYMENT_ID}"
# Active probes mark the upstream unready (route table retained; no gateway restart).
UNREADY=0
for _ in $(seq 1 45); do
  code="$(curl --silent --show-error -o "${TMP_DIR}/rust-stop.json" -w '%{http_code}' \
    -H 'Host: rust.demo.localhost' "${GATEWAY_URL}/")" || true
  if [[ "${code}" == "503" ]]; then
    UNREADY=1
    break
  fi
  sleep 1
done
[[ "${UNREADY}" -eq 1 ]] ||
  fail "rust host did not become 503 after stop; last=$(cat "${TMP_DIR}/rust-stop.json" 2>/dev/null || true)"
assert_http_code "rust.demo.localhost" "503"
# Healthy siblings must still work.
assert_language "go.demo.localhost" "go"
assert_language "python.demo.localhost" "python"
assert_gateway_not_restarted "${GATEWAY_STARTED}" "stop rust workload"

echo "Redeploying rust (start container + Runtime) without gateway restart..."
docker start "forge-${RUST_DEPLOYMENT_ID}" >/dev/null ||
  fail "could not start forge-${RUST_DEPLOYMENT_ID}"
"${COMPOSE[@]}" start "${RUNTIME_SERVICE}" >/dev/null ||
  fail "could not restart ${RUNTIME_SERVICE}"
wait_http "${RUNTIME_URL}/health/ready" "Runtime" 60
wait_deployment_status "${RUST_DEPLOYMENT_ID}" "active" 60
wait_route_host "rust.demo.localhost" 45
assert_language "rust.demo.localhost" "rust"
assert_gateway_not_restarted "${GATEWAY_STARTED}" "redeploy rust"

echo "Changing go route to python upstream without gateway restart..."
PYTHON_UPSTREAM="$(upstream_url_for_host "python.demo.localhost")"
GO_UPSTREAM="$(upstream_url_for_host "go.demo.localhost")"
python3 -c '
import json, sys
go_up, py_up = sys.argv[1], sys.argv[2]
routes = json.load(open(sys.argv[3]))
out = []
for r in routes:
    host = (r.get("host") or "").lower()
    if host == "go.demo.localhost":
        r = dict(r)
        r["upstreams"] = [{"url": py_up}]
    out.append(r)
json.dump(out, open(sys.argv[4], "w"))
' "${GO_UPSTREAM}" "${PYTHON_UPSTREAM}" "${TMP_DIR}/routes.json" "${TMP_DIR}/routes-swapped.json" ||
  fail "could not build swapped route table"

curl --fail --silent --show-error -X PUT "${GATEWAY_URL}/admin/routes" \
  -H 'content-type: application/json' \
  --data-binary @"${TMP_DIR}/routes-swapped.json" >/dev/null ||
  fail "PUT /admin/routes (swap) failed"

assert_language "go.demo.localhost" "python"
assert_gateway_not_restarted "${GATEWAY_STARTED}" "admin route change"

echo "Refreshing from Control/Runtime restores go without gateway restart..."
refresh_routes
assert_language "go.demo.localhost" "go"
assert_language "rust.demo.localhost" "rust"
assert_language "python.demo.localhost" "python"
assert_gateway_not_restarted "${GATEWAY_STARTED}" "route refresh restore"

echo
echo "Demo 05 passed."
echo "  Project:      ${PROJECT_ID}"
echo "  Environment:  ${ENVIRONMENT_ID}"
echo "  Application:  ${APPLICATION_ID}"
echo "  Deployments:  go=${GO_DEPLOYMENT_ID} rust=${RUST_DEPLOYMENT_ID} python=${PYTHON_DEPLOYMENT_ID}"
echo "  Hosts:        go.demo.localhost rust.demo.localhost python.demo.localhost"
echo "  Gateway URL:  ${GATEWAY_URL}"
echo "  Images:       ${GO_IMAGE} ${RUST_IMAGE} ${PYTHON_IMAGE}"
