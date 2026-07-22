#!/usr/bin/env bash
# Demo 06: fixture Git repo → Forge Build → registry → Control → Runtime → Gateway (epic gate).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
COMPOSE=(docker compose -f "${ROOT_DIR}/compose.yaml" --project-directory "${ROOT_DIR}")
GATEWAY_URL="${FORGE_GATEWAY_URL:-http://127.0.0.1:4000}"
CONTROL_URL="${FORGE_CONTROL_URL:-http://127.0.0.1:4001}"
RUNTIME_URL="${FORGE_RUNTIME_URL:-http://127.0.0.1:4102}"
BUILD_URL="${FORGE_BUILD_URL:-http://127.0.0.1:4103}"
REGISTRY="${FORGE_REGISTRY:-localhost:5000}"
GATEWAY_SERVICE="forge-gateway"
CONTROL_SERVICE="forge-control"
RUNTIME_SERVICE="forge-runtime"
BUILD_SERVICE="forge-build"
POSTGRES_SERVICE="postgres"
REGISTRY_SERVICE="registry"
CLI_DIR="${ROOT_DIR}/tools/forge-cli"
FORGE_BIN="${CLI_DIR}/forge"
FIXTURE_DIR="${ROOT_DIR}/demos/06-source-to-deployment/fixture"
FIXTURE_BROKEN_DIR="${ROOT_DIR}/demos/06-source-to-deployment/fixture-broken"
FIXTURE_REPO="file:///fixtures/demo"
FIXTURE_BROKEN_REPO="file:///fixtures/demo-broken"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-build-demo.XXXXXX")"
CONFIG_HOME="${TMP_DIR}/xdg-config"
export XDG_CONFIG_HOME="${CONFIG_HOME}"
export CI=1
export FORGE_PROFILE="${FORGE_PROFILE:-demo}"
export FORGE_ENDPOINT="${FORGE_ENDPOINT:-${CONTROL_URL}}"
export FORGE_RECONCILE_INTERVAL_SECONDS="${FORGE_RECONCILE_INTERVAL_SECONDS:-3}"
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
BUILT_IMAGES=()

cleanup() {
  local dep img
  if ((${#TRACKED_DEPLOYMENTS[@]} > 0)); then
    for dep in "${TRACKED_DEPLOYMENTS[@]}"; do
      [[ -n "${dep}" ]] || continue
      curl --silent --show-error -X DELETE "${CONTROL_URL}/v1/deployments/${dep}" >/dev/null 2>&1 || true
      docker rm -f "forge-${dep}" >/dev/null 2>&1 || true
    done
  fi
  if ((${#BUILT_IMAGES[@]} > 0)); then
    for img in "${BUILT_IMAGES[@]}"; do
      [[ -n "${img}" ]] || continue
      docker rmi -f "${img}" >/dev/null 2>&1 || true
    done
  fi
  "${COMPOSE[@]}" stop \
    "${GATEWAY_SERVICE}" "${RUNTIME_SERVICE}" "${BUILD_SERVICE}" "${CONTROL_SERVICE}" \
    >/dev/null 2>&1 || true
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

dump_context() {
  local bid="${1:-}"
  if [[ -n "${bid}" ]]; then
    echo "--- build ${bid} ---" >&2
    curl --silent --show-error "${BUILD_URL}/v1/builds/${bid}" >&2 || true
    echo >&2
    echo "--- build ${bid} logs (tail) ---" >&2
    curl --silent --show-error "${BUILD_URL}/v1/builds/${bid}/logs" 2>/dev/null | tail -n 80 >&2 || true
    echo >&2
  fi
  echo "--- registry catalog ---" >&2
  curl --silent --show-error "http://${REGISTRY}/v2/_catalog" >&2 || true
  echo >&2
  echo "--- ${GATEWAY_SERVICE} /admin/routes ---" >&2
  curl --silent --show-error "${GATEWAY_URL}/admin/routes" >&2 || true
  echo >&2
  echo "--- ${BUILD_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=100 "${BUILD_SERVICE}" >&2 || true
  echo "--- ${GATEWAY_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=80 "${GATEWAY_SERVICE}" >&2 || true
  echo "--- ${RUNTIME_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=80 "${RUNTIME_SERVICE}" >&2 || true
  echo "--- ${CONTROL_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${CONTROL_SERVICE}" >&2 || true
  echo "--- docker ps -a (forge-*) ---" >&2
  docker ps -a --filter name=forge- --format 'table {{.Names}}\t{{.Status}}\t{{.Image}}' >&2 || true
}

fail() {
  echo "Demo 06 failed: $*" >&2
  dump_context "${LAST_BUILD_ID:-}"
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

prepare_fixture_git() {
  local dir="$1" label="$2"
  echo "Preparing fixture Git repo (${label}) at ${dir}..."
  [[ -f "${dir}/Dockerfile" && -f "${dir}/forge.yaml" ]] ||
    fail "fixture ${label} missing Dockerfile or forge.yaml"
  if [[ ! -d "${dir}/.git" ]]; then
    git -C "${dir}" init -b main >/dev/null
    git -C "${dir}" config user.email "forge@local"
    git -C "${dir}" config user.name "forge"
  fi
  git -C "${dir}" add -A
  if ! git -C "${dir}" diff --cached --quiet; then
    git -C "${dir}" commit -m "demo-06 ${label}" >/dev/null
  elif [[ -z "$(git -C "${dir}" rev-parse --verify HEAD 2>/dev/null || true)" ]]; then
    git -C "${dir}" commit --allow-empty -m "demo-06 ${label}" >/dev/null
  fi
  git -C "${dir}" rev-parse HEAD >/dev/null || fail "fixture ${label} has no HEAD commit"
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
}

create_hierarchy() {
  local suffix="$1"
  forge_json "${TMP_DIR}/project.json" project create --name "demo-build-${suffix}"
  PROJECT_ID="$(read_id "${TMP_DIR}/project.json")"
  forge_json "${TMP_DIR}/environment.json" env create --project "${PROJECT_ID}" --name development
  ENVIRONMENT_ID="$(read_id "${TMP_DIR}/environment.json")"
  forge_json "${TMP_DIR}/application.json" app create --project "${PROJECT_ID}" --name demos
  APPLICATION_ID="$(read_id "${TMP_DIR}/application.json")"
  forge_json "${TMP_DIR}/service.json" service create --app "${APPLICATION_ID}" --name api --port 8080
  SERVICE_ID="$(read_id "${TMP_DIR}/service.json")"
}

submit_build() {
  local repo="$1" auto_deploy="$2" out="$3"
  local payload
  payload="$(SERVICE_ID="${SERVICE_ID}" ENVIRONMENT_ID="${ENVIRONMENT_ID}" REPO="${repo}" AUTO_DEPLOY="${auto_deploy}" PROJECT_NAME="demo06" python3 - <<'PY'
import json, os
print(json.dumps({
    "repo": os.environ["REPO"],
    "ref": "main",
    "forgeYamlPath": "forge.yaml",
    "project": os.environ["PROJECT_NAME"],
    "serviceId": os.environ["SERVICE_ID"],
    "environmentId": os.environ["ENVIRONMENT_ID"],
    "autoDeploy": os.environ["AUTO_DEPLOY"].lower() == "true",
}))
PY
)"
  curl --fail --silent --show-error -X POST "${BUILD_URL}/v1/builds" \
    -H 'content-type: application/json' \
    -d "${payload}" >"${out}" || fail "POST /v1/builds failed"
  python3 -c 'import json,sys,uuid; v=json.load(open(sys.argv[1])); uuid.UUID(v["buildId"]); assert v["status"]=="queued"' \
    "${out}" || fail "unexpected build accept body: $(cat "${out}")"
}

read_build_id() {
  python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["buildId"])' "$1"
}

fetch_build() {
  local build_id="$1" out="$2"
  curl --fail --silent --show-error "${BUILD_URL}/v1/builds/${build_id}" >"${out}" ||
    fail "GET /v1/builds/${build_id} failed"
}

wait_build_status() {
  local build_id="$1" expected="$2" attempts="${3:-180}"
  local status=""
  echo "Waiting for build ${build_id} status=${expected} ..."
  for _ in $(seq 1 "${attempts}"); do
    fetch_build "${build_id}" "${TMP_DIR}/build.json"
    status="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["status"])' "${TMP_DIR}/build.json")"
    if [[ "${status}" == "${expected}" ]]; then
      echo "  status=${status}"
      return 0
    fi
    if [[ "${status}" == "failed" || "${status}" == "canceled" ]] && [[ "${expected}" != "${status}" ]]; then
      fail "build ${build_id} ended as ${status}, want ${expected}"
    fi
    sleep 2
  done
  fail "build ${build_id} status=${status:-unknown}, want ${expected}"
}

stream_build_logs() {
  local build_id="$1"
  echo "Streaming build logs for ${build_id} (follow)..."
  # Capture a sample of streamed logs; follow until the build finishes.
  curl --fail --silent --show-error -N \
    "${BUILD_URL}/v1/builds/${build_id}/logs?follow=true" \
    >"${TMP_DIR}/build-logs-${build_id}.txt" ||
    fail "log stream for ${build_id} failed"
  [[ -s "${TMP_DIR}/build-logs-${build_id}.txt" ]] ||
    fail "build ${build_id} produced empty streamed logs"
  # Logs must also remain available after the build completes.
  curl --fail --silent --show-error \
    "${BUILD_URL}/v1/builds/${build_id}/logs" \
    >"${TMP_DIR}/build-logs-after-${build_id}.txt" ||
    fail "post-build logs for ${build_id} failed"
  [[ -s "${TMP_DIR}/build-logs-after-${build_id}.txt" ]] ||
    fail "build ${build_id} post-build logs were empty"
  echo "  logs accessible during/after build ($(wc -l <"${TMP_DIR}/build-logs-after-${build_id}.txt" | tr -d ' ') lines)"
}

assert_workspace_cleaned() {
  local build_id="$1"
  echo "Asserting workspace cleaned for ${build_id}..."
  if docker exec "${BUILD_SERVICE}" test -d "/workspace/${build_id}"; then
    fail "workspace /workspace/${build_id} still present after terminal build"
  fi
  echo "  workspace removed"
}

assert_image_tag_encoding() {
  local image="$1" commit="$2" build_id="$3"
  IMAGE="${image}" COMMIT="${commit}" BUILD_ID="${build_id}" REGISTRY="${REGISTRY}" python3 - <<'PY' ||
import os, re, sys
image = os.environ["IMAGE"]
commit = os.environ["COMMIT"]
build_id = os.environ["BUILD_ID"]
registry = os.environ["REGISTRY"]
short_sha = commit[:7]
short_bid = build_id.split("-", 1)[0]
m = re.fullmatch(
    rf"{re.escape(registry)}/demo06-api:({re.escape(short_sha)})-({re.escape(short_bid)})",
    image,
)
assert m, f"image {image!r} does not encode commit+build id (want {registry}/demo06-api:{short_sha}-{short_bid})"
print(f"  tag encodes commit={m.group(1)} build={m.group(2)}")
PY
    fail "image tag encoding check failed for ${image}"
}

assert_registry_has_image() {
  local image="$1"
  echo "Verifying registry has ${image}..."
  docker pull "${image}" >/dev/null || fail "docker pull ${image} failed"
  BUILT_IMAGES+=("${image}")
  # Also confirm catalog/tags via registry HTTP API.
  local repo tag
  repo="$(python3 -c 'import sys; ref=sys.argv[1]; print(ref.split("/",1)[1].rsplit(":",1)[0])' "${image}")"
  tag="$(python3 -c 'import sys; print(sys.argv[1].rsplit(":",1)[1])' "${image}")"
  curl --fail --silent --show-error "http://${REGISTRY}/v2/${repo}/tags/list" \
    >"${TMP_DIR}/tags.json" || fail "registry tags list failed for ${repo}"
  TAG="${tag}" python3 -c '
import json, os, sys
tags = json.load(open(sys.argv[1])).get("tags") or []
want = os.environ["TAG"]
assert want in tags, (want, tags)
' "${TMP_DIR}/tags.json" || fail "registry missing tag ${tag} for ${repo}"
  echo "  registry has ${repo}:${tag}"
}

assert_control_image() {
  local service_id="$1" image="$2" commit="$3" build_id="$4"
  echo "Verifying Control service ${service_id} recorded image..."
  curl --fail --silent --show-error "${CONTROL_URL}/v1/services/${service_id}" \
    >"${TMP_DIR}/service.json" || fail "GET service failed"
  IMAGE="${image}" COMMIT="${commit}" BUILD_ID="${build_id}" python3 -c '
import json, os, sys
svc = json.load(open(sys.argv[1]))
assert svc.get("image") == os.environ["IMAGE"], svc
assert svc.get("imageCommit") == os.environ["COMMIT"], svc
assert svc.get("imageBuildId") == os.environ["BUILD_ID"], svc
assert svc.get("imageDigest"), svc
' "${TMP_DIR}/service.json" || fail "Control service image fields mismatch: $(cat "${TMP_DIR}/service.json")"
  echo "  Control image=${image}"
}

count_deployments() {
  local service_id="$1"
  curl --fail --silent --show-error "${CONTROL_URL}/v1/services/${service_id}/deployments" |
    python3 -c 'import json,sys; print(len(json.load(sys.stdin)))'
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
  fail "timed out waiting for route host=${host}"
}

assert_gateway_200() {
  local host="$1"
  local code body
  echo "Curling Host=${host} via Gateway..."
  code="$(curl --silent --show-error -o "${TMP_DIR}/gw-body.json" -w '%{http_code}' \
    -H "Host: ${host}" "${GATEWAY_URL}/")" || true
  [[ "${code}" == "200" ]] ||
    fail "Host ${host} returned HTTP ${code}, want 200; body=$(cat "${TMP_DIR}/gw-body.json")"
  body="$(cat "${TMP_DIR}/gw-body.json")"
  echo "${body}" | python3 -c '
import json, sys
body = json.load(sys.stdin)
assert body.get("ok") is True, body
assert body.get("service") == "source-to-deployment", body
' || fail "unexpected gateway body: ${body}"
  echo "  ${host} → HTTP 200 (source-to-deployment)"
}

echo "== Demo 06: source-to-deployment (Forge Build epic gate) =="
echo "Building forge CLI..."
make -C "${CLI_DIR}" build || fail "CLI build failed"
[[ -x "${FORGE_BIN}" ]] || fail "forge binary missing at ${FORGE_BIN}"

prepare_fixture_git "${FIXTURE_DIR}" "happy"
prepare_fixture_git "${FIXTURE_BROKEN_DIR}" "broken"
FIXTURE_COMMIT="$(git -C "${FIXTURE_DIR}" rev-parse HEAD)"

echo "Starting PostgreSQL, registry, Control, Runtime, Gateway, and Build..."
"${COMPOSE[@]}" up -d "${POSTGRES_SERVICE}" "${REGISTRY_SERVICE}"
"${COMPOSE[@]}" up -d --build --force-recreate "${CONTROL_SERVICE}"
wait_http "${CONTROL_URL}/health/ready" "Control"
"${COMPOSE[@]}" stop "${RUNTIME_SERVICE}" >/dev/null 2>&1 || true
purge_stale_deployments
"${COMPOSE[@]}" up -d --build --force-recreate "${RUNTIME_SERVICE}"
wait_http "${RUNTIME_URL}/health/ready" "Runtime"
"${COMPOSE[@]}" up -d --build --force-recreate "${GATEWAY_SERVICE}"
wait_http "${GATEWAY_URL}/health/ready" "Gateway"
"${COMPOSE[@]}" up -d --build --force-recreate "${BUILD_SERVICE}"
wait_http "${BUILD_URL}/health/ready" "Build"

docker exec "${GATEWAY_SERVICE}" printenv FORGE_HOST_PATTERN 2>/dev/null |
  grep -Fq '{service}' ||
  fail "gateway FORGE_HOST_PATTERN must contain '{service}' (got: $(docker exec "${GATEWAY_SERVICE}" printenv FORGE_HOST_PATTERN 2>/dev/null || true))"

echo "Configuring CLI profile ${FORGE_PROFILE} -> ${FORGE_ENDPOINT}..."
forge config set endpoint "${FORGE_ENDPOINT}" --profile "${FORGE_PROFILE}"
forge config use "${FORGE_PROFILE}"

SUFFIX="$(date +%s)-$$"
create_hierarchy "${SUFFIX}"
HOST="api.demo.localhost"

echo "Phase 1: happy-path build with autoDeploy..."
submit_build "${FIXTURE_REPO}" "true" "${TMP_DIR}/build-accept.json"
BUILD_ID="$(read_build_id "${TMP_DIR}/build-accept.json")"
LAST_BUILD_ID="${BUILD_ID}"
# Stream logs in background while polling status would race; follow until done then assert status.
stream_build_logs "${BUILD_ID}"
wait_build_status "${BUILD_ID}" "succeeded" 30

# Control recording/autoDeploy finishes before log follow ends; poll briefly for safety.
DEPLOYMENT_ID=""
BUILD_IMAGE=""
BUILD_COMMIT=""
for _ in $(seq 1 30); do
  fetch_build "${BUILD_ID}" "${TMP_DIR}/build.json"
  BUILD_IMAGE="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("image") or "")' "${TMP_DIR}/build.json")"
  BUILD_COMMIT="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("commit") or "")' "${TMP_DIR}/build.json")"
  DEPLOYMENT_ID="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("linkedDeploymentId") or "")' "${TMP_DIR}/build.json")"
  if IMAGE_OK="$(python3 -c 'import json,sys; print("yes" if json.load(open(sys.argv[1])).get("imageRecorded") is True else "no")' "${TMP_DIR}/build.json")" \
    && [[ "${IMAGE_OK}" == "yes" && -n "${DEPLOYMENT_ID}" && -n "${BUILD_IMAGE}" ]]; then
    break
  fi
  sleep 1
done

[[ -n "${BUILD_IMAGE}" ]] || fail "succeeded build missing image"
[[ "${BUILD_COMMIT}" == "${FIXTURE_COMMIT}" ]] ||
  fail "build commit ${BUILD_COMMIT} != fixture HEAD ${FIXTURE_COMMIT}"
[[ "${IMAGE_OK:-no}" == "yes" ]] ||
  fail "build imageRecorded not true: $(cat "${TMP_DIR}/build.json")"
[[ -n "${DEPLOYMENT_ID}" ]] || fail "autoDeploy did not link a deployment"
track_deployment "${DEPLOYMENT_ID}"

assert_image_tag_encoding "${BUILD_IMAGE}" "${BUILD_COMMIT}" "${BUILD_ID}"
assert_registry_has_image "${BUILD_IMAGE}"
assert_control_image "${SERVICE_ID}" "${BUILD_IMAGE}" "${BUILD_COMMIT}" "${BUILD_ID}"
assert_workspace_cleaned "${BUILD_ID}"

wait_deployment_status "${DEPLOYMENT_ID}" "active"
wait_route_host "${HOST}"
assert_gateway_200 "${HOST}"

echo "Phase 2: broken Dockerfile → failed, no deployment..."
BEFORE_DEP_COUNT="$(count_deployments "${SERVICE_ID}")"
submit_build "${FIXTURE_BROKEN_REPO}" "true" "${TMP_DIR}/bad-accept.json"
BAD_BUILD_ID="$(read_build_id "${TMP_DIR}/bad-accept.json")"
LAST_BUILD_ID="${BAD_BUILD_ID}"
stream_build_logs "${BAD_BUILD_ID}"
wait_build_status "${BAD_BUILD_ID}" "failed" 30
fetch_build "${BAD_BUILD_ID}" "${TMP_DIR}/bad-build.json"

python3 -c '
import json, sys
rec = json.load(open(sys.argv[1]))
assert rec["status"] == "failed", rec
assert not rec.get("image"), rec
assert not rec.get("linkedDeploymentId"), rec
' "${TMP_DIR}/bad-build.json" ||
  fail "failed build unexpectedly exposed image/deployment: $(cat "${TMP_DIR}/bad-build.json")"

AFTER_DEP_COUNT="$(count_deployments "${SERVICE_ID}")"
[[ "${AFTER_DEP_COUNT}" == "${BEFORE_DEP_COUNT}" ]] ||
  fail "failed build created deployments (${BEFORE_DEP_COUNT} → ${AFTER_DEP_COUNT})"
assert_workspace_cleaned "${BAD_BUILD_ID}"

# Happy-path route must still work after the negative case.
assert_gateway_200 "${HOST}"

echo
echo "Demo 06 passed."
echo "  Project:      ${PROJECT_ID}"
echo "  Environment:  ${ENVIRONMENT_ID}"
echo "  Application:  ${APPLICATION_ID}"
echo "  Service:      ${SERVICE_ID}"
echo "  Build:        ${BUILD_ID}"
echo "  Commit:       ${BUILD_COMMIT}"
echo "  Image:        ${BUILD_IMAGE}"
echo "  Deployment:   ${DEPLOYMENT_ID}"
echo "  Host:         ${HOST}"
echo "  Failed build: ${BAD_BUILD_ID} (no deployment)"
echo "  Gateway URL:  ${GATEWAY_URL}"
echo "  Build URL:    ${BUILD_URL}"
