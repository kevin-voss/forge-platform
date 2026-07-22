#!/usr/bin/env bash
# Demo 02: exercise the Forge Control desired-state API end to end.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
COMPOSE=(docker compose -f "${ROOT_DIR}/compose.yaml" --project-directory "${ROOT_DIR}")
CONTROL_URL="${FORGE_CONTROL_URL:-http://127.0.0.1:4001}"
CONTROL_SERVICE="forge-control"
POSTGRES_SERVICE="postgres"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-control-demo.XXXXXX")"

cleanup() {
  "${COMPOSE[@]}" stop "${CONTROL_SERVICE}" >/dev/null 2>&1 || true
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

fail() {
  echo "Demo 02 failed: $*" >&2
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

post_json() {
  local path="$1" body="$2" output="$3"
  local status
  status="$(curl --silent --show-error --output "${output}" --write-out '%{http_code}' \
    --request POST "${CONTROL_URL}${path}" \
    --header 'content-type: application/json' \
    --header "Idempotency-Key: demo-02-${SUFFIX}-${path//[^A-Za-z0-9]/-}" \
    --data "${body}")" || fail "POST ${path} did not complete"
  [[ "${status}" == "201" ]] || fail "POST ${path} returned HTTP ${status}: $(cat "${output}")"
}

read_id() {
  python3 -c 'import json, sys, uuid; value = json.load(open(sys.argv[1]))["id"]; uuid.UUID(value); print(value)' "$1" ||
    fail "response did not contain a UUID id: $(cat "$1")"
}

assert_tree() {
  local response="$1"
  python3 - "$response" "$PROJECT_ID" "$ENVIRONMENT_ID" "$APPLICATION_ID" "$SERVICE_ID" "$DEPLOYMENT_ID" <<'PY'
import json
import sys
import uuid

path, project_id, environment_id, application_id, service_id, deployment_id = sys.argv[1:]
tree = json.load(open(path))

def assert_uuid(value, label):
    try:
        uuid.UUID(value)
    except (TypeError, ValueError) as error:
        raise AssertionError(f"{label} is not a UUID: {value!r}") from error

for value, label in (
    (project_id, "project id"),
    (environment_id, "environment id"),
    (application_id, "application id"),
    (service_id, "service id"),
    (deployment_id, "deployment id"),
):
    assert_uuid(value, label)

assert tree["project"]["id"] == project_id
assert len(tree["environments"]) == 1
assert tree["environments"][0]["id"] == environment_id
assert tree["environments"][0]["projectId"] == project_id
assert len(tree["applications"]) == 1
application = tree["applications"][0]
assert application["id"] == application_id
assert application["projectId"] == project_id
assert len(application["services"]) == 1
service = application["services"][0]
assert service["id"] == service_id
assert service["applicationId"] == application_id
assert service["port"] == 8080
assert len(service["deployments"]) == 1
deployment = service["deployments"][0]
assert deployment["id"] == deployment_id
assert deployment["serviceId"] == service_id
assert deployment["environmentId"] == environment_id
assert deployment["desiredReplicas"] == 1
assert deployment["status"] == "pending"
PY
}

assert_error_envelope() {
  local response="$1"
  python3 - "$response" <<'PY'
import json
import sys

payload = json.load(open(sys.argv[1]))
error = payload.get("error")
assert isinstance(error, dict), "missing error object"
assert error.get("code") == "not_found", error
assert isinstance(error.get("message"), str) and error["message"], error
assert isinstance(error.get("requestId"), str) and error["requestId"], error
PY
}

echo "== Demo 02: Forge Control =="
echo "Starting PostgreSQL and Control (migrations run on Control startup)..."
"${COMPOSE[@]}" up -d "${POSTGRES_SERVICE}"
"${COMPOSE[@]}" up -d --build --force-recreate "${CONTROL_SERVICE}"
wait_ready

echo "Checking the migrated Control schema..."
"${COMPOSE[@]}" exec -T "${POSTGRES_SERVICE}" \
  psql --username forge --dbname forge --tuples-only --no-align \
  --command "SELECT to_regclass('control.flyway_schema_history') IS NOT NULL AND to_regclass('control.deployments') IS NOT NULL;" \
  | tr -d '[:space:]' | grep -qx 't' || fail "Control migrations are not present"

SUFFIX="$(date +%s)-$$"
post_json "/v1/projects" "{\"name\":\"demo-control-${SUFFIX}\"}" "${TMP_DIR}/project.json"
PROJECT_ID="$(read_id "${TMP_DIR}/project.json")"

post_json "/v1/projects/${PROJECT_ID}/environments" '{"name":"development"}' "${TMP_DIR}/environment.json"
ENVIRONMENT_ID="$(read_id "${TMP_DIR}/environment.json")"

post_json "/v1/projects/${PROJECT_ID}/applications" '{"name":"web"}' "${TMP_DIR}/application.json"
APPLICATION_ID="$(read_id "${TMP_DIR}/application.json")"

post_json "/v1/applications/${APPLICATION_ID}/services" '{"name":"api","port":8080}' "${TMP_DIR}/service.json"
SERVICE_ID="$(read_id "${TMP_DIR}/service.json")"

post_json "/v1/services/${SERVICE_ID}/deployments" \
  "{\"image\":\"localhost:5000/demo-go:latest\",\"desiredReplicas\":1,\"environmentId\":\"${ENVIRONMENT_ID}\"}" \
  "${TMP_DIR}/deployment.json"
DEPLOYMENT_ID="$(read_id "${TMP_DIR}/deployment.json")"

echo "Reading the complete project tree..."
curl --fail --silent --show-error "${CONTROL_URL}/v1/projects/${PROJECT_ID}?expand=tree" >"${TMP_DIR}/tree-before.json" ||
  fail "could not read the project tree"
assert_tree "${TMP_DIR}/tree-before.json" || fail "project tree did not match the created hierarchy"

echo "Checking the canonical 404 error envelope..."
UNKNOWN_ID="00000000-0000-4000-8000-000000000000"
UNKNOWN_STATUS="$(curl --silent --show-error --output "${TMP_DIR}/unknown.json" --write-out '%{http_code}' \
  "${CONTROL_URL}/v1/projects/${UNKNOWN_ID}")" || fail "unknown-project request did not complete"
[[ "${UNKNOWN_STATUS}" == "404" ]] || fail "unknown project returned HTTP ${UNKNOWN_STATUS}: $(cat "${TMP_DIR}/unknown.json")"
assert_error_envelope "${TMP_DIR}/unknown.json" || fail "unknown project did not return the canonical error envelope"

echo "Restarting Control to verify durable desired state..."
"${COMPOSE[@]}" restart "${CONTROL_SERVICE}" >/dev/null
wait_ready
curl --fail --silent --show-error "${CONTROL_URL}/v1/projects/${PROJECT_ID}?expand=tree" >"${TMP_DIR}/tree-after.json" ||
  fail "could not read the project tree after restart"
assert_tree "${TMP_DIR}/tree-after.json" || fail "project tree changed after Control restart"
cmp --silent "${TMP_DIR}/tree-before.json" "${TMP_DIR}/tree-after.json" ||
  fail "project tree response changed after Control restart"

echo
echo "Demo 02 passed."
echo "  Project:     ${PROJECT_ID}"
echo "  Environment: ${ENVIRONMENT_ID}"
echo "  Application: ${APPLICATION_ID}"
echo "  Service:     ${SERVICE_ID}"
echo "  Deployment:  ${DEPLOYMENT_ID}"
