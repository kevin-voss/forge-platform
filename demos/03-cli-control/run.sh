#!/usr/bin/env bash
# Demo 03: recreate the Control hierarchy using only the forge CLI.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# Pre-09 demos opt into the insecure auth bypass (Control default is enforce as of 09.06).
export FORGE_AUTH_MODE="${FORGE_AUTH_MODE:-dev}"
COMPOSE=(docker compose -f "${ROOT_DIR}/compose.yaml" --project-directory "${ROOT_DIR}")
CONTROL_URL="${FORGE_CONTROL_URL:-http://127.0.0.1:4001}"
CONTROL_SERVICE="forge-control"
POSTGRES_SERVICE="postgres"
CLI_DIR="${ROOT_DIR}/tools/forge-cli"
FORGE_BIN="${CLI_DIR}/forge"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-cli-demo.XXXXXX")"
CONFIG_HOME="${TMP_DIR}/xdg-config"
export XDG_CONFIG_HOME="${CONFIG_HOME}"
export CI=1
export FORGE_PROFILE="${FORGE_PROFILE:-demo}"
export FORGE_ENDPOINT="${FORGE_ENDPOINT:-${CONTROL_URL}}"
mkdir -p "${CONFIG_HOME}"

cleanup() {
  "${COMPOSE[@]}" stop "${CONTROL_SERVICE}" >/dev/null 2>&1 || true
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

fail() {
  echo "Demo 03 failed: $*" >&2
  echo "--- ${CONTROL_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=100 "${CONTROL_SERVICE}" >&2 || true
  exit 1
}

wait_ready() {
  local ready=0
  echo "Waiting for Control readiness at ${CONTROL_URL}/health/ready ..."
  for _ in $(seq 1 90); do
    if curl --fail --silent --show-error "${CONTROL_URL}/health/ready" >/dev/null; then
      ready=1
      break
    fi
    sleep 1
  done
  [[ "${ready}" -eq 1 ]] || fail "timed out waiting for Control readiness"
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

assert_contains_id() {
  local list_file="$1" expected_id="$2" label="$3"
  python3 - "$list_file" "$expected_id" "$label" <<'PY' || fail "list assertion failed for ${label}"
import json
import sys
import uuid

path, expected_id, label = sys.argv[1:]
uuid.UUID(expected_id)
items = json.load(open(path))
assert isinstance(items, list), f"{label} list is not a JSON array: {items!r}"
ids = [item.get("id") for item in items]
assert expected_id in ids, f"{label} id {expected_id} missing from {ids!r}"
PY
}

assert_deployment_status() {
  local status_file="$1" expected_id="$2" expected_service="$3" expected_env="$4"
  python3 - "$status_file" "$expected_id" "$expected_service" "$expected_env" <<'PY' || fail "deployment status assertion failed"
import json
import sys
import uuid

path, expected_id, expected_service, expected_env = sys.argv[1:]
for value in (expected_id, expected_service, expected_env):
    uuid.UUID(value)
payload = json.load(open(path))
assert payload["id"] == expected_id
assert payload["serviceId"] == expected_service
assert payload["environmentId"] == expected_env
assert payload["desiredReplicas"] == 1
assert payload["image"] == "localhost:5000/demo-go:latest"
assert payload["status"] == "pending"
PY
}

echo "== Demo 03: Forge CLI Control =="
echo "Building forge CLI..."
make -C "${CLI_DIR}" build || fail "CLI build failed"
[[ -x "${FORGE_BIN}" ]] || fail "forge binary missing at ${FORGE_BIN}"

echo "Starting PostgreSQL and Control..."
"${COMPOSE[@]}" up -d "${POSTGRES_SERVICE}"
"${COMPOSE[@]}" up -d --build --force-recreate "${CONTROL_SERVICE}"
wait_ready

echo "Configuring CLI profile ${FORGE_PROFILE} -> ${FORGE_ENDPOINT}..."
forge config set endpoint "${FORGE_ENDPOINT}" --profile "${FORGE_PROFILE}"
forge config use "${FORGE_PROFILE}"
configured="$(forge config get endpoint | tr -d '[:space:]')"
[[ "${configured}" == "${FORGE_ENDPOINT}" ]] ||
  fail "config get endpoint returned ${configured}, want ${FORGE_ENDPOINT}"

SUFFIX="$(date +%s)-$$"
PROJECT_NAME="demo-cli-${SUFFIX}"

echo "Creating control-plane hierarchy via forge..."
forge_json "${TMP_DIR}/project.json" project create --name "${PROJECT_NAME}"
PROJECT_ID="$(read_id "${TMP_DIR}/project.json")"

forge_json "${TMP_DIR}/environment.json" env create --project "${PROJECT_ID}" --name development
ENVIRONMENT_ID="$(read_id "${TMP_DIR}/environment.json")"

forge_json "${TMP_DIR}/application.json" app create --project "${PROJECT_ID}" --name web
APPLICATION_ID="$(read_id "${TMP_DIR}/application.json")"

forge_json "${TMP_DIR}/service.json" service create --app "${APPLICATION_ID}" --name api --port 8080
SERVICE_ID="$(read_id "${TMP_DIR}/service.json")"

forge_json "${TMP_DIR}/deployment.json" deployment create \
  --service "${SERVICE_ID}" \
  --image localhost:5000/demo-go:latest \
  --env "${ENVIRONMENT_ID}" \
  --replicas 1
DEPLOYMENT_ID="$(read_id "${TMP_DIR}/deployment.json")"

echo "Reading hierarchy back via list/status..."
forge_json "${TMP_DIR}/projects.json" project list
assert_contains_id "${TMP_DIR}/projects.json" "${PROJECT_ID}" "project"

forge_json "${TMP_DIR}/project-get.json" project get "${PROJECT_ID}"
python3 -c 'import json,sys; p=json.load(open(sys.argv[1])); assert p["id"]==sys.argv[2]' \
  "${TMP_DIR}/project-get.json" "${PROJECT_ID}" ||
  fail "project get did not return created project"

forge_json "${TMP_DIR}/environments.json" env list --project "${PROJECT_ID}"
assert_contains_id "${TMP_DIR}/environments.json" "${ENVIRONMENT_ID}" "environment"

forge_json "${TMP_DIR}/applications.json" app list --project "${PROJECT_ID}"
assert_contains_id "${TMP_DIR}/applications.json" "${APPLICATION_ID}" "application"

forge_json "${TMP_DIR}/services.json" service list --app "${APPLICATION_ID}"
assert_contains_id "${TMP_DIR}/services.json" "${SERVICE_ID}" "service"

forge_json "${TMP_DIR}/deployments.json" deployment list --service "${SERVICE_ID}"
assert_contains_id "${TMP_DIR}/deployments.json" "${DEPLOYMENT_ID}" "deployment"

forge_json "${TMP_DIR}/deployment-status.json" deployment status "${DEPLOYMENT_ID}"
assert_deployment_status \
  "${TMP_DIR}/deployment-status.json" \
  "${DEPLOYMENT_ID}" \
  "${SERVICE_ID}" \
  "${ENVIRONMENT_ID}"

echo "Checking table output remains human-readable..."
table_out="$(forge deployment status "${DEPLOYMENT_ID}")" || fail "table deployment status failed"
echo "${table_out}" | grep -Eq 'STATUS|pending' ||
  fail "table deployment status missing expected columns/values: ${table_out}"

echo "Negative case: unknown project id must exit 3..."
UNKNOWN_ID="00000000-0000-4000-8000-000000000000"
set +e
forge project get "${UNKNOWN_ID}" >"${TMP_DIR}/unknown.stdout" 2>"${TMP_DIR}/unknown.stderr"
unknown_rc=$?
set -e
[[ "${unknown_rc}" -eq 3 ]] ||
  fail "unknown project exit=${unknown_rc}, want 3; stderr=$(cat "${TMP_DIR}/unknown.stderr")"
grep -Eqi 'not found|requestid|request id' "${TMP_DIR}/unknown.stderr" ||
  fail "unknown project stderr lacked a useful error: $(cat "${TMP_DIR}/unknown.stderr")"

echo
echo "Demo 03 passed."
echo "  Project:     ${PROJECT_ID}"
echo "  Environment: ${ENVIRONMENT_ID}"
echo "  Application: ${APPLICATION_ID}"
echo "  Service:     ${SERVICE_ID}"
echo "  Deployment:  ${DEPLOYMENT_ID}"
