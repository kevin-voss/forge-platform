#!/usr/bin/env bash
# Demo 09: platform identity end-to-end (epic 09 acceptance gate).
# Scenario: create user → org → project → developer token → authorized deploy
#           → viewer denied → revoke → access fails. Control runs in enforce mode.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/09-platform-identity"
GO_APP_DIR="${ROOT_DIR}/demos/01-container-runtime/apps/go"
COMPOSE=(
  docker compose
    -f "${ROOT_DIR}/compose.yaml"
    -f "${DEMO_DIR}/docker-compose.yml"
    --project-directory "${ROOT_DIR}"
)

# Enforce is the point of this demo — do not allow a silent bypass.
export FORGE_AUTH_MODE=enforce
export FORGE_INTROSPECT_CACHE_TTL_S="${FORGE_INTROSPECT_CACHE_TTL_S:-2}"
export FORGE_AUTHZ_CACHE_TTL_S="${FORGE_AUTHZ_CACHE_TTL_S:-2}"
export FORGE_LIFECYCLE_OWNER="${FORGE_LIFECYCLE_OWNER:-control}"

CONTROL_URL="${FORGE_CONTROL_URL:-http://127.0.0.1:4001}"
# Host-side Identity for curl / forge CLI. Compose Control uses forge-identity:8080.
IDENTITY_URL="${FORGE_IDENTITY_HOST_URL:-http://127.0.0.1:4002}"
RUNTIME_URL="${FORGE_RUNTIME_HOST_URL:-http://127.0.0.1:4102}"
CONTROL_SERVICE="forge-control"
IDENTITY_SERVICE="forge-identity"
RUNTIME_SERVICE="forge-runtime"
POSTGRES_SERVICE="postgres"
REGISTRY_SERVICE="registry"
CLI_DIR="${ROOT_DIR}/tools/forge-cli"
FORGE_BIN="${CLI_DIR}/forge"
DEMO_IMAGE="${DEMO_IMAGE:-localhost:5000/demo-go:identity}"
PHASE="${1:-all}"
CACHE_WAIT_S="${FORGE_DEMO_CACHE_WAIT_S:-3}"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-identity-demo.XXXXXX")"
CONFIG_HOME="${TMP_DIR}/xdg-config"
export XDG_CONFIG_HOME="${CONFIG_HOME}"
export CI=1
export FORGE_PROFILE="${FORGE_PROFILE:-demo}"
export FORGE_ENDPOINT="${FORGE_ENDPOINT:-${CONTROL_URL}}"
mkdir -p "${CONFIG_HOME}"

SUFFIX="$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')"
OWNER_EMAIL="owner-${SUFFIX}@example.com"
OWNER_PASSWORD="Demo-Passw0rd-${SUFFIX}!"
DEV_EMAIL="dev-${SUFFIX}@example.com"
VIEWER_EMAIL="viewer-${SUFFIX}@example.com"

OWNER_USER_ID=""
ORG_ID=""
PROJECT_ID=""
DEV_USER_ID=""
VIEWER_USER_ID=""
DEV_TOKEN=""
DEV_TOKEN_ID=""
VIEWER_TOKEN=""
SERVICE_ID=""
ENVIRONMENT_ID=""
TRACKED_DEPLOYMENTS=()

cleanup() {
  local dep
  if ((${#TRACKED_DEPLOYMENTS[@]} > 0)); then
    for dep in "${TRACKED_DEPLOYMENTS[@]}"; do
      [[ -n "${dep}" ]] || continue
      # Best-effort delete; token may already be revoked.
      curl --silent --show-error \
        -H "Authorization: Bearer ${VIEWER_TOKEN:-}" \
        -X DELETE "${CONTROL_URL}/v1/deployments/${dep}" >/dev/null 2>&1 || true
      curl --silent --show-error \
        -X DELETE "${CONTROL_URL}/v1/deployments/${dep}" >/dev/null 2>&1 || true
    done
  fi
  docker ps -aq --filter "label=forge.managed=true" | while read -r cid; do
    [[ -n "${cid}" ]] || continue
    docker rm -f "${cid}" >/dev/null 2>&1 || true
  done
  if [[ -x "${FORGE_BIN}" ]]; then
    "${FORGE_BIN}" logout >/dev/null 2>&1 || true
  fi
  "${COMPOSE[@]}" stop \
    "${RUNTIME_SERVICE}" "${CONTROL_SERVICE}" "${IDENTITY_SERVICE}" >/dev/null 2>&1 || true
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

dump_context() {
  echo "--- ${IDENTITY_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=120 "${IDENTITY_SERVICE}" >&2 || true
  echo "--- ${CONTROL_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=120 "${CONTROL_SERVICE}" >&2 || true
  echo "--- ${RUNTIME_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${RUNTIME_SERVICE}" >&2 || true
}

fail() {
  echo "Demo 09 failed: $*" >&2
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

identity_json() {
  local method="$1" path="$2" body="${3:-}" output="$4"
  local status
  if [[ -n "${body}" ]]; then
    status="$(curl --silent --show-error --output "${output}" --write-out '%{http_code}' \
      --request "${method}" "${IDENTITY_URL}${path}" \
      --header 'content-type: application/json' \
      --data "${body}")" || fail "Identity ${method} ${path} did not complete"
  else
    status="$(curl --silent --show-error --output "${output}" --write-out '%{http_code}' \
      --request "${method}" "${IDENTITY_URL}${path}")" ||
      fail "Identity ${method} ${path} did not complete"
  fi
  echo "${status}"
}

control_json() {
  local method="$1" path="$2" token="$3" body="${4:-}" output="$5"
  local status
  local -a args=(
    --silent --show-error --output "${output}" --write-out '%{http_code}'
    --request "${method}" "${CONTROL_URL}${path}"
    --header "Authorization: Bearer ${token}"
  )
  if [[ -n "${body}" ]]; then
    args+=(--header 'content-type: application/json' --data "${body}")
  fi
  status="$(curl "${args[@]}")" || fail "Control ${method} ${path} did not complete"
  echo "${status}"
}

ensure_demo_image() {
  echo "Ensuring ${DEMO_IMAGE} is in the local registry..."
  docker image inspect forge/demo-go-api:local >/dev/null 2>&1 ||
    docker build -t forge/demo-go-api:local "${GO_APP_DIR}" ||
    fail "could not build forge/demo-go-api:local"
  docker tag forge/demo-go-api:local "${DEMO_IMAGE}" || fail "could not tag ${DEMO_IMAGE}"
  docker push "${DEMO_IMAGE}" >/dev/null || fail "could not push ${DEMO_IMAGE}"
}

assert_no_plaintext_secrets() {
  local logs_file="${TMP_DIR}/service-logs.txt"
  {
    "${COMPOSE[@]}" logs --no-color "${IDENTITY_SERVICE}" "${CONTROL_SERVICE}" 2>/dev/null || true
  } >"${logs_file}"

  python3 - "$logs_file" "${OWNER_PASSWORD}" "${DEV_TOKEN}" "${VIEWER_TOKEN}" <<'PY' || fail "plaintext secret found in service logs"
import sys
path, password, dev_token, viewer_token = sys.argv[1:5]
text = open(path, errors="replace").read()
leaks = []
if password and password in text:
    leaks.append("owner password")
if dev_token and dev_token in text:
    leaks.append("developer token")
if viewer_token and viewer_token in text:
    leaks.append("viewer token")
# Session tokens from login responses should not appear either if present in env.
if leaks:
    raise SystemExit("leaked: " + ", ".join(leaks))
print("logs contain plaintext secret: no")
PY
}

step_bootstrap_stack() {
  echo "== Demo 09: Platform Identity =="
  echo "Auth mode: FORGE_AUTH_MODE=${FORGE_AUTH_MODE} (must be enforce)"
  [[ "${FORGE_AUTH_MODE}" == "enforce" ]] || fail "FORGE_AUTH_MODE must be enforce for demo 09"

  echo "Building forge CLI..."
  make -C "${CLI_DIR}" build || fail "CLI build failed"
  [[ -x "${FORGE_BIN}" ]] || fail "forge binary missing at ${FORGE_BIN}"

  echo "Starting PostgreSQL, registry, Identity, Control (enforce), and Runtime..."
  "${COMPOSE[@]}" up -d --remove-orphans "${POSTGRES_SERVICE}" "${REGISTRY_SERVICE}"
  "${COMPOSE[@]}" up -d --build --force-recreate --no-deps "${IDENTITY_SERVICE}"
  wait_http "${IDENTITY_URL}/health/ready" "Identity"
  # --no-deps avoids racing a second Identity recreate via Control depends_on.
  "${COMPOSE[@]}" up -d --build --force-recreate --no-deps "${CONTROL_SERVICE}"
  wait_http "${CONTROL_URL}/health/ready" "Control"
  "${COMPOSE[@]}" up -d --build --force-recreate --no-deps "${RUNTIME_SERVICE}"
  wait_http "${RUNTIME_URL}/health/ready" "Runtime"

  ensure_demo_image

  # Host Identity URL for forge login/whoami (after compose is up).
  export FORGE_IDENTITY_URL="${IDENTITY_URL}"

  echo "Configuring CLI profile ${FORGE_PROFILE} -> ${FORGE_ENDPOINT}..."
  forge config set endpoint "${FORGE_ENDPOINT}" --profile "${FORGE_PROFILE}"
  forge config use "${FORGE_PROFILE}"
}

step_create_user_org_project() {
  echo "[1] create user"
  local status
  status="$(identity_json POST /v1/auth/register \
    "{\"email\":\"${OWNER_EMAIL}\",\"password\":\"${OWNER_PASSWORD}\",\"display_name\":\"Owner ${SUFFIX}\"}" \
    "${TMP_DIR}/register.json")"
  [[ "${status}" == "201" ]] || fail "register returned HTTP ${status}: $(cat "${TMP_DIR}/register.json")"
  OWNER_USER_ID="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["user_id"])' "${TMP_DIR}/register.json")"
  echo "  user_id=${OWNER_USER_ID}"

  status="$(identity_json POST /v1/auth/login \
    "{\"email\":\"${OWNER_EMAIL}\",\"password\":\"${OWNER_PASSWORD}\"}" \
    "${TMP_DIR}/login.json")"
  [[ "${status}" == "200" ]] || fail "login returned HTTP ${status}: $(cat "${TMP_DIR}/login.json")"
  local session_token
  session_token="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["session_token"])' "${TMP_DIR}/login.json")"
  forge login --token "${session_token}" || fail "forge login with session failed"
  forge whoami >/dev/null || fail "forge whoami failed after login"

  echo "[2] create org"
  status="$(identity_json POST /v1/orgs \
    "{\"name\":\"Demo Org ${SUFFIX}\"}" \
    "${TMP_DIR}/org.json")"
  [[ "${status}" == "201" ]] || fail "create org returned HTTP ${status}: $(cat "${TMP_DIR}/org.json")"
  ORG_ID="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "${TMP_DIR}/org.json")"
  status="$(identity_json POST "/v1/orgs/${ORG_ID}/members" \
    "{\"user_id\":\"${OWNER_USER_ID}\",\"role\":\"organization-owner\"}" \
    "${TMP_DIR}/org-member.json")"
  [[ "${status}" == "201" || "${status}" == "200" ]] ||
    fail "add org member returned HTTP ${status}: $(cat "${TMP_DIR}/org-member.json")"
  echo "  org_id=${ORG_ID}"

  echo "[3] create project"
  forge_json "${TMP_DIR}/project.json" project create --name "demo-identity-${SUFFIX}"
  PROJECT_ID="$(read_id "${TMP_DIR}/project.json")"
  status="$(identity_json POST /v1/projects \
    "{\"id\":\"${PROJECT_ID}\",\"org_id\":\"${ORG_ID}\",\"name\":\"demo-identity-${SUFFIX}\"}" \
    "${TMP_DIR}/id-project.json")"
  [[ "${status}" == "201" ]] || fail "identity project register returned HTTP ${status}: $(cat "${TMP_DIR}/id-project.json")"
  echo "  project_id=${PROJECT_ID}"

  # Developer + viewer principals for role tokens.
  status="$(identity_json POST /v1/users \
    "{\"email\":\"${DEV_EMAIL}\",\"display_name\":\"Developer ${SUFFIX}\"}" \
    "${TMP_DIR}/dev-user.json")"
  [[ "${status}" == "201" ]] || fail "create developer user returned HTTP ${status}: $(cat "${TMP_DIR}/dev-user.json")"
  DEV_USER_ID="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "${TMP_DIR}/dev-user.json")"

  status="$(identity_json POST /v1/users \
    "{\"email\":\"${VIEWER_EMAIL}\",\"display_name\":\"Viewer ${SUFFIX}\"}" \
    "${TMP_DIR}/viewer-user.json")"
  [[ "${status}" == "201" ]] || fail "create viewer user returned HTTP ${status}: $(cat "${TMP_DIR}/viewer-user.json")"
  VIEWER_USER_ID="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "${TMP_DIR}/viewer-user.json")"

  status="$(identity_json POST "/v1/projects/${PROJECT_ID}/members" \
    "{\"user_id\":\"${DEV_USER_ID}\",\"role\":\"developer\"}" \
    "${TMP_DIR}/dev-member.json")"
  [[ "${status}" == "201" || "${status}" == "200" ]] ||
    fail "add developer member returned HTTP ${status}: $(cat "${TMP_DIR}/dev-member.json")"

  status="$(identity_json POST "/v1/projects/${PROJECT_ID}/members" \
    "{\"user_id\":\"${VIEWER_USER_ID}\",\"role\":\"viewer\"}" \
    "${TMP_DIR}/viewer-member.json")"
  [[ "${status}" == "201" || "${status}" == "200" ]] ||
    fail "add viewer member returned HTTP ${status}: $(cat "${TMP_DIR}/viewer-member.json")"
}

step_issue_tokens() {
  echo "[4] issue developer token"
  local status
  status="$(identity_json POST /v1/tokens \
    "{\"owner\":{\"type\":\"user\",\"id\":\"${DEV_USER_ID}\"},\"project_id\":\"${PROJECT_ID}\",\"role\":\"developer\"}" \
    "${TMP_DIR}/dev-token.json")"
  [[ "${status}" == "201" ]] || fail "create developer token returned HTTP ${status}: $(cat "${TMP_DIR}/dev-token.json")"
  DEV_TOKEN="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["token"])' "${TMP_DIR}/dev-token.json")"
  DEV_TOKEN_ID="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["token_id"])' "${TMP_DIR}/dev-token.json")"
  [[ "${DEV_TOKEN}" == forge_pat_* ]] || fail "developer token missing forge_pat_ prefix"

  status="$(identity_json POST /v1/tokens \
    "{\"owner\":{\"type\":\"user\",\"id\":\"${VIEWER_USER_ID}\"},\"project_id\":\"${PROJECT_ID}\",\"role\":\"viewer\"}" \
    "${TMP_DIR}/viewer-token.json")"
  [[ "${status}" == "201" ]] || fail "create viewer token returned HTTP ${status}: $(cat "${TMP_DIR}/viewer-token.json")"
  VIEWER_TOKEN="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["token"])' "${TMP_DIR}/viewer-token.json")"
  echo "[4] developer token issued"

  forge login --token "${DEV_TOKEN}" || fail "forge login --token developer failed"

  forge_json "${TMP_DIR}/environment.json" env create --project "${PROJECT_ID}" --name development
  ENVIRONMENT_ID="$(read_id "${TMP_DIR}/environment.json")"
  forge_json "${TMP_DIR}/application.json" app create --project "${PROJECT_ID}" --name web
  local application_id
  application_id="$(read_id "${TMP_DIR}/application.json")"
  forge_json "${TMP_DIR}/service.json" service create --app "${application_id}" --name api --port 8080
  SERVICE_ID="$(read_id "${TMP_DIR}/service.json")"
}

step_deploy_developer() {
  echo "[5] deploy as developer"
  local status body
  body="$(python3 -c 'import json,sys; print(json.dumps({"image":sys.argv[1],"desiredReplicas":1,"environmentId":sys.argv[2]}))' \
    "${DEMO_IMAGE}" "${ENVIRONMENT_ID}")"
  status="$(control_json POST "/v1/services/${SERVICE_ID}/deployments" "${DEV_TOKEN}" "${body}" \
    "${TMP_DIR}/deploy-dev.json")"
  [[ "${status}" == "201" ]] || fail "developer deploy returned HTTP ${status}: $(cat "${TMP_DIR}/deploy-dev.json")"
  local deployment_id
  deployment_id="$(read_id "${TMP_DIR}/deploy-dev.json")"
  TRACKED_DEPLOYMENTS+=("${deployment_id}")
  echo "[5] deploy as developer -> ${status} OK"
}

step_deploy_viewer_denied() {
  echo "[6] deploy as viewer"
  local status body
  body="$(python3 -c 'import json,sys; print(json.dumps({"image":sys.argv[1],"desiredReplicas":1,"environmentId":sys.argv[2]}))' \
    "${DEMO_IMAGE}" "${ENVIRONMENT_ID}")"
  status="$(control_json POST "/v1/services/${SERVICE_ID}/deployments" "${VIEWER_TOKEN}" "${body}" \
    "${TMP_DIR}/deploy-viewer.json")"
  [[ "${status}" == "403" ]] || fail "viewer deploy expected 403, got ${status}: $(cat "${TMP_DIR}/deploy-viewer.json")"
  python3 -c 'import json,sys; e=json.load(open(sys.argv[1])); assert e.get("error",{}).get("code")=="forbidden", e' \
    "${TMP_DIR}/deploy-viewer.json" || fail "viewer deploy error envelope missing forbidden"
  echo "[6] deploy as viewer -> ${status} OK"
}

step_revoke_and_fail() {
  echo "[7] revoke developer token"
  local status
  status="$(identity_json POST "/v1/tokens/${DEV_TOKEN_ID}/revoke" "" "${TMP_DIR}/revoke.json")"
  [[ "${status}" == "204" || "${status}" == "200" ]] ||
    fail "revoke returned HTTP ${status}: $(cat "${TMP_DIR}/revoke.json")"
  echo "[7] revoked developer token"

  echo "Waiting ${CACHE_WAIT_S}s for introspection cache TTL (${FORGE_INTROSPECT_CACHE_TTL_S}s) ..."
  sleep "${CACHE_WAIT_S}"

  echo "[8] deploy with revoked token"
  local body
  body="$(python3 -c 'import json,sys; print(json.dumps({"image":sys.argv[1],"desiredReplicas":1,"environmentId":sys.argv[2]}))' \
    "${DEMO_IMAGE}" "${ENVIRONMENT_ID}")"
  status="$(control_json POST "/v1/services/${SERVICE_ID}/deployments" "${DEV_TOKEN}" "${body}" \
    "${TMP_DIR}/deploy-revoked.json")"
  [[ "${status}" == "401" ]] || fail "revoked deploy expected 401, got ${status}: $(cat "${TMP_DIR}/deploy-revoked.json")"
  python3 -c 'import json,sys; e=json.load(open(sys.argv[1])); assert e.get("error",{}).get("code")=="unauthenticated", e' \
    "${TMP_DIR}/deploy-revoked.json" || fail "revoked deploy error envelope missing unauthenticated"
  echo "[8] deploy with revoked token -> ${status} OK"
}

step_unauthenticated() {
  local status body
  body="$(python3 -c 'import json,sys; print(json.dumps({"image":sys.argv[1],"desiredReplicas":1,"environmentId":sys.argv[2]}))' \
    "${DEMO_IMAGE}" "${ENVIRONMENT_ID}")"
  status="$(curl --silent --show-error --output "${TMP_DIR}/deploy-anon.json" --write-out '%{http_code}' \
    --request POST "${CONTROL_URL}/v1/services/${SERVICE_ID}/deployments" \
    --header 'content-type: application/json' \
    --data "${body}")" || fail "anonymous deploy request failed"
  [[ "${status}" == "401" ]] || fail "anonymous deploy expected 401, got ${status}"
}

run_scenario() {
  step_bootstrap_stack
  step_create_user_org_project
  step_issue_tokens
  step_deploy_developer
  step_deploy_viewer_denied
  step_revoke_and_fail
  step_unauthenticated
  assert_no_plaintext_secrets
  echo "demo 09 PASSED"
}

case "${PHASE}" in
  all|--phase=all|"")
    run_scenario
    ;;
  --phase=bootstrap)
    step_bootstrap_stack
    echo "phase bootstrap PASSED"
    ;;
  --phase=identity)
    step_bootstrap_stack
    step_create_user_org_project
    step_issue_tokens
    echo "phase identity PASSED"
    ;;
  --phase=authz)
    step_bootstrap_stack
    step_create_user_org_project
    step_issue_tokens
    step_deploy_developer
    step_deploy_viewer_denied
    step_revoke_and_fail
    step_unauthenticated
    assert_no_plaintext_secrets
    echo "phase authz PASSED"
    echo "demo 09 PASSED"
    ;;
  *)
    echo "Unknown phase: ${PHASE}" >&2
    echo "Usage: $0 [all|--phase=bootstrap|--phase=identity|--phase=authz]" >&2
    exit 2
    ;;
esac
