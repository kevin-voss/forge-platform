#!/usr/bin/env bash
# Demo 18: managed PostgreSQL gate (epic 18 acceptance).
# Scenario: create → attach → deploy → migrate/write → backup → restore fixture.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/18-managed-database"
APP_DIR="${DEMO_DIR}/app"
COMPOSE=(
  docker compose
    -f "${ROOT_DIR}/compose.yaml"
    -f "${DEMO_DIR}/compose.yaml"
    --project-directory "${ROOT_DIR}"
)

export FORGE_AUTH_MODE=enforce
export FORGE_INTROSPECT_CACHE_TTL_S="${FORGE_INTROSPECT_CACHE_TTL_S:-2}"
export FORGE_AUTHZ_CACHE_TTL_S="${FORGE_AUTHZ_CACHE_TTL_S:-2}"
export FORGE_LIFECYCLE_OWNER="${FORGE_LIFECYCLE_OWNER:-control}"
export FORGE_RECONCILE_INTERVAL_MS="${FORGE_RECONCILE_INTERVAL_MS:-2000}"
export FORGE_INJECT_MASK_IN_LOGS="${FORGE_INJECT_MASK_IN_LOGS:-true}"
export FORGE_DB_PROVISIONER="${FORGE_DB_PROVISIONER:-local}"
export FORGE_DB_ENDPOINT_HOST="${FORGE_DB_ENDPOINT_HOST:-host.docker.internal}"
export FORGE_DB_MANAGED_NETWORK="${FORGE_DB_MANAGED_NETWORK:-forge-net}"
export DOCKER_GID="${DOCKER_GID:-$(stat -f '%g' /var/run/docker.sock 2>/dev/null || stat -c '%g' /var/run/docker.sock 2>/dev/null || echo 0)}"

if [[ -z "${FORGE_SECRETS_MASTER_KEY:-}" ]]; then
  FORGE_SECRETS_MASTER_KEY="$(python3 -c 'import base64,os; print(base64.b64encode(os.urandom(32)).decode())')"
fi
export FORGE_SECRETS_MASTER_KEY
export FORGE_SECRETS_MASTER_KEY_ID="${FORGE_SECRETS_MASTER_KEY_ID:-demo-m18}"

CONTROL_URL="${FORGE_CONTROL_URL:-http://127.0.0.1:4001}"
IDENTITY_URL="${FORGE_IDENTITY_HOST_URL:-http://127.0.0.1:4002}"
RUNTIME_URL="${FORGE_RUNTIME_HOST_URL:-http://127.0.0.1:4102}"
SECRETS_URL="${FORGE_SECRETS_HOST_URL:-http://127.0.0.1:4104}"
CONTROL_SERVICE="forge-control"
IDENTITY_SERVICE="forge-identity"
SECRETS_SERVICE="forge-secrets"
RUNTIME_SERVICE="forge-runtime"
POSTGRES_SERVICE="postgres"
REGISTRY_SERVICE="registry"
CLI_DIR="${ROOT_DIR}/tools/forge-cli"
FORGE_BIN="${CLI_DIR}/forge"
DEMO_IMAGE="${DEMO_IMAGE:-localhost:5000/demo-managed-db:18}"
SERVICE_SLUG="${SERVICE_SLUG:-backend}"
APPLICATION_NAME="${APPLICATION_NAME:-backend}"
ENV_NAME="${ENV_NAME:-development}"
DB_NAME="${DB_NAME:-main}"
FIXTURE_KEY="${FIXTURE_KEY:-demo18-fixture}"
FIXTURE_VALUE="${FIXTURE_VALUE:-managed-db-ok}"
PHASE="${1:-all}"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-mdb-demo.XXXXXX")"
CONFIG_HOME="${TMP_DIR}/xdg-config"
export XDG_CONFIG_HOME="${CONFIG_HOME}"
export CI=1
export FORGE_PROFILE="${FORGE_PROFILE:-demo}"
export FORGE_ENDPOINT="${FORGE_ENDPOINT:-${CONTROL_URL}}"
export FORGE_SECRETS_URL="${FORGE_SECRETS_URL:-${SECRETS_URL}}"
mkdir -p "${CONFIG_HOME}"

SUFFIX="$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')"
OWNER_EMAIL="owner-${SUFFIX}@example.com"
OWNER_PASSWORD="Demo-Passw0rd-${SUFFIX}!"

OWNER_USER_ID=""
ORG_ID=""
PROJECT_ID=""
ENVIRONMENT_ID=""
APPLICATION_ID=""
SERVICE_ID=""
SA_TOKEN=""
SESSION_TOKEN=""
DEPLOYMENT_ID=""
TRACKED_DEPLOYMENTS=()
LOCK_DIR=""

cleanup() {
  local dep
  if ((${#TRACKED_DEPLOYMENTS[@]} > 0)); then
    for dep in "${TRACKED_DEPLOYMENTS[@]}"; do
      [[ -n "${dep}" ]] || continue
      curl --silent --show-error \
        -H "Authorization: Bearer ${SESSION_TOKEN:-${SA_TOKEN:-}}" \
        -X DELETE "${CONTROL_URL}/v1/deployments/${dep}" >/dev/null 2>&1 || true
    done
  fi
  docker ps -aq --filter "label=forge.managed=true" | while read -r cid; do
    [[ -n "${cid}" ]] || continue
    docker rm -f "${cid}" >/dev/null 2>&1 || true
  done
  docker ps -aq --filter "label=forge.managed_db=true" | while read -r cid; do
    [[ -n "${cid}" ]] || continue
    docker rm -f "${cid}" >/dev/null 2>&1 || true
  done
  if [[ -x "${FORGE_BIN}" ]]; then
    "${FORGE_BIN}" logout >/dev/null 2>&1 || true
  fi
  "${COMPOSE[@]}" stop \
    "${RUNTIME_SERVICE}" "${CONTROL_SERVICE}" "${SECRETS_SERVICE}" "${IDENTITY_SERVICE}" \
    >/dev/null 2>&1 || true
  rm -rf "${TMP_DIR}"
  if [[ -n "${LOCK_DIR}" ]]; then
    rm -rf "${LOCK_DIR}" >/dev/null 2>&1 || true
    LOCK_DIR=""
  fi
}
trap cleanup EXIT
# Ignore SIGTERM so a stray local `pkill` from a parallel demo attempt cannot
# abort the gate mid-run. SIGINT/SIGKILL still work; EXIT still cleans up.
trap '' TERM

acquire_demo_lock() {
  # Portable exclusive lock (macOS has no util-linux flock).
  LOCK_DIR="${TMPDIR:-/tmp}/forge-demo-18.lock"
  if ! mkdir "${LOCK_DIR}" 2>/dev/null; then
    local holder
    holder="$(cat "${LOCK_DIR}/pid" 2>/dev/null || true)"
    if [[ -n "${holder}" ]] && kill -0 "${holder}" 2>/dev/null; then
      echo "Demo 18 failed: another demos/18-managed-database/run.sh is running (pid ${holder})" >&2
      exit 1
    fi
    # Stale lock from a killed run.
    rm -rf "${LOCK_DIR}" >/dev/null 2>&1 || true
    mkdir "${LOCK_DIR}" || {
      echo "Demo 18 failed: could not acquire ${LOCK_DIR}" >&2
      exit 1
    }
  fi
  echo "$$" >"${LOCK_DIR}/pid"
}

dump_context() {
  echo "--- ${CONTROL_SERVICE} reconcile/secrets signals (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=300 "${CONTROL_SERVICE}" 2>&1 |
    grep -E 'secrets_resolve|StartReplica|runtime unreachable|managed db|reconcile tick|controller_healthy' |
    sed -E 's#postgresql://[^[:space:]]+#postgresql://***#g' |
    tail -n 80 >&2 || true
  echo "--- ${CONTROL_SERVICE} logs (tail, URL-masked) ---" >&2
  "${COMPOSE[@]}" logs --tail=80 "${CONTROL_SERVICE}" 2>&1 |
    sed -E 's#postgresql://[^[:space:]]+#postgresql://***#g' >&2 || true
  echo "--- ${RUNTIME_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${RUNTIME_SERVICE}" 2>&1 |
    sed -E 's#postgresql://[^[:space:]]+#postgresql://***#g' >&2 || true
  echo "--- ${SECRETS_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=40 "${SECRETS_SERVICE}" >&2 || true
  echo "--- managed containers ---" >&2
  docker ps --filter "label=forge.managed=true" --format '{{.Names}} {{.Status}}' >&2 || true
  docker ps --filter "label=forge.managed_db=true" --format '{{.Names}} {{.Status}}' >&2 || true
}

fail() {
  echo "Demo 18 failed: $*" >&2
  dump_context
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
  if [[ "${1:-}" == "login" ]]; then
    echo "+ forge login …" >&2
  else
    echo "+ forge $*" >&2
  fi
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

ensure_demo_image() {
  echo "Ensuring ${DEMO_IMAGE} is in the local registry..."
  docker build -t forge/demo-managed-db:local "${APP_DIR}" ||
    fail "could not build forge/demo-managed-db:local"
  docker tag forge/demo-managed-db:local "${DEMO_IMAGE}" || fail "could not tag ${DEMO_IMAGE}"
  docker push "${DEMO_IMAGE}" >/dev/null || fail "could not push ${DEMO_IMAGE}"
}

purge_stale_deployments() {
  echo "Purging leftover Control deployments/placements (local Postgres)..."
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

step_bootstrap_stack() {
  echo "== Demo 18: Managed PostgreSQL =="
  echo "Auth mode: FORGE_AUTH_MODE=${FORGE_AUTH_MODE}"
  echo "Provisioner: FORGE_DB_PROVISIONER=${FORGE_DB_PROVISIONER}"
  echo "Endpoint host: FORGE_DB_ENDPOINT_HOST=${FORGE_DB_ENDPOINT_HOST}"
  [[ "${FORGE_AUTH_MODE}" == "enforce" ]] || fail "FORGE_AUTH_MODE must be enforce for demo 18"
  [[ "${FORGE_DB_PROVISIONER}" == "local" ]] || fail "FORGE_DB_PROVISIONER must be local for demo 18"

  chmod +x "${DEMO_DIR}/acceptance.sh" "${DEMO_DIR}/run.sh"

  echo "Building forge CLI..."
  make -C "${CLI_DIR}" build || fail "CLI build failed"
  [[ -x "${FORGE_BIN}" ]] || fail "forge binary missing at ${FORGE_BIN}"

  echo "Starting PostgreSQL, registry, Identity, Secrets, Control, Runtime..."
  # Clear name conflicts from other compose projects / prior demos.
  for name in forge-identity forge-secrets forge-control forge-runtime; do
    docker rm -f "${name}" >/dev/null 2>&1 || true
  done
  "${COMPOSE[@]}" up -d --remove-orphans "${POSTGRES_SERVICE}" "${REGISTRY_SERVICE}"
  "${COMPOSE[@]}" up -d --build --force-recreate --no-deps "${IDENTITY_SERVICE}"
  wait_http "${IDENTITY_URL}/health/ready" "Identity"
  "${COMPOSE[@]}" up -d --build --force-recreate --no-deps "${SECRETS_SERVICE}"
  wait_http "${SECRETS_URL}/health/ready" "Secrets"
  docker rm -f "${CONTROL_SERVICE}" >/dev/null 2>&1 || true
  "${COMPOSE[@]}" up -d --build --force-recreate --no-deps "${CONTROL_SERVICE}"
  wait_http "${CONTROL_URL}/health/ready" "Control"

  ensure_demo_image

  export FORGE_IDENTITY_URL="${IDENTITY_URL}"

  echo "Configuring CLI profile ${FORGE_PROFILE} -> ${FORGE_ENDPOINT}..."
  forge config set endpoint "${FORGE_ENDPOINT}" --profile "${FORGE_PROFILE}"
  forge config use "${FORGE_PROFILE}"
}

step_create_user_org_project() {
  echo "[identity] create user / org / project"
  local status
  status="$(identity_json POST /v1/auth/register \
    "{\"email\":\"${OWNER_EMAIL}\",\"password\":\"${OWNER_PASSWORD}\",\"display_name\":\"Owner ${SUFFIX}\"}" \
    "${TMP_DIR}/register.json")"
  [[ "${status}" == "201" ]] || fail "register returned HTTP ${status}: $(cat "${TMP_DIR}/register.json")"
  OWNER_USER_ID="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["user_id"])' "${TMP_DIR}/register.json")"

  status="$(identity_json POST /v1/auth/login \
    "{\"email\":\"${OWNER_EMAIL}\",\"password\":\"${OWNER_PASSWORD}\"}" \
    "${TMP_DIR}/login.json")"
  [[ "${status}" == "200" ]] || fail "login returned HTTP ${status}: $(cat "${TMP_DIR}/login.json")"
  SESSION_TOKEN="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["session_token"])' "${TMP_DIR}/login.json")"
  forge login --token "${SESSION_TOKEN}" || fail "forge login with session failed"
  forge whoami >/dev/null || fail "forge whoami failed after login"

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

  forge_json "${TMP_DIR}/project.json" project create --name "demo-mdb-${SUFFIX}"
  PROJECT_ID="$(read_id "${TMP_DIR}/project.json")"
  status="$(identity_json POST /v1/projects \
    "{\"id\":\"${PROJECT_ID}\",\"org_id\":\"${ORG_ID}\",\"name\":\"demo-mdb-${SUFFIX}\"}" \
    "${TMP_DIR}/id-project.json")"
  [[ "${status}" == "201" ]] || fail "identity project register returned HTTP ${status}: $(cat "${TMP_DIR}/id-project.json")"
  # Explicit project membership so Control authz resolves even if org-owner
  # inheritance is unavailable (matches demo 09 belt-and-suspenders).
  status="$(identity_json POST "/v1/projects/${PROJECT_ID}/members" \
    "{\"user_id\":\"${OWNER_USER_ID}\",\"role\":\"project-admin\"}" \
    "${TMP_DIR}/project-member.json")"
  [[ "${status}" == "201" || "${status}" == "200" ]] ||
    fail "add project member returned HTTP ${status}: $(cat "${TMP_DIR}/project-member.json")"
  export FORGE_PROJECT="${PROJECT_ID}"

  forge_json "${TMP_DIR}/environment.json" env create --project "${PROJECT_ID}" --name "${ENV_NAME}"
  ENVIRONMENT_ID="$(read_id "${TMP_DIR}/environment.json")"
  forge_json "${TMP_DIR}/application.json" app create --project "${PROJECT_ID}" --name "${APPLICATION_NAME}"
  APPLICATION_ID="$(read_id "${TMP_DIR}/application.json")"
  forge_json "${TMP_DIR}/service.json" service create --app "${APPLICATION_ID}" --name "${SERVICE_SLUG}" --port 8080
  SERVICE_ID="$(read_id "${TMP_DIR}/service.json")"
  echo "  project_id=${PROJECT_ID} app=${APPLICATION_NAME} service=${SERVICE_SLUG}"
}

step_issue_service_account() {
  echo "[identity] issue Control secrets-resolve token (developer PAT)"
  local status
  status="$(identity_json POST /v1/tokens \
    "{\"owner\":{\"type\":\"user\",\"id\":\"${OWNER_USER_ID}\"},\"project_id\":\"${PROJECT_ID}\",\"role\":\"developer\"}" \
    "${TMP_DIR}/sa-token.json")"
  [[ "${status}" == "201" ]] || fail "create resolve token returned HTTP ${status}: $(cat "${TMP_DIR}/sa-token.json")"
  SA_TOKEN="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["token"])' "${TMP_DIR}/sa-token.json")"
  [[ "${SA_TOKEN}" == forge_pat_* ]] || fail "resolve token missing forge_pat_ prefix"
  export FORGE_SECRETS_SERVICE_ACCOUNT="${SA_TOKEN}"
  echo "  developer PAT issued for Control → Secrets resolve"

  echo "Starting Runtime, then recreating Control with secrets resolve token..."
  # Runtime must be reachable before Control's first reconcile tick so JDK DNS
  # and HttpClient do not observe a missing forge-runtime during startup GC.
  docker ps -aq --filter "name=forge-runtime" | while read -r cid; do
    [[ -n "${cid}" ]] || continue
    docker rm -f "${cid}" >/dev/null 2>&1 || true
  done
  docker ps -aq --filter "name=forge-control" | while read -r cid; do
    [[ -n "${cid}" ]] || continue
    docker rm -f "${cid}" >/dev/null 2>&1 || true
  done
  "${COMPOSE[@]}" up -d --build --force-recreate --no-deps "${RUNTIME_SERVICE}"
  wait_http "${RUNTIME_URL}/health/ready" "Runtime"
  "${COMPOSE[@]}" up -d --force-recreate --no-deps "${CONTROL_SERVICE}"
  wait_http "${CONTROL_URL}/health/ready" "Control"

  local sa_len
  sa_len="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_SECRETS_SERVICE_ACCOUNT 2>/dev/null | wc -c | tr -d ' ')"
  [[ "${sa_len}" -gt 20 ]] ||
    fail "Control missing FORGE_SECRETS_SERVICE_ACCOUNT after recreate (len=${sa_len})"
  echo "  Control has secrets resolve token (len=${sa_len})"

  # Prove Control → Runtime observe path before acceptance (curl; same network).
  local ready=0
  echo "Waiting for Control→Runtime node/state ..."
  for _ in $(seq 1 60); do
    if docker exec "${CONTROL_SERVICE}" \
      curl --fail --silent --show-error \
      http://forge-runtime:8080/v1/node/state >/dev/null 2>&1; then
      ready=1
      break
    fi
    sleep 1
  done
  [[ "${ready}" -eq 1 ]] || fail "Control cannot reach Runtime /v1/node/state"
  echo "  Control→Runtime OK"

  purge_stale_deployments
}

step_acceptance() {
  echo "Running acceptance assertions..."
  export FORGE_BIN CONTROL_URL PROJECT_ID APPLICATION_ID SERVICE_ID ENVIRONMENT_ID
  export DEMO_IMAGE TMP_DIR SESSION_TOKEN RUNTIME_URL APP_DIR
  export SERVICE_SLUG APPLICATION_NAME DB_NAME FIXTURE_KEY FIXTURE_VALUE
  bash "${DEMO_DIR}/acceptance.sh" || fail "acceptance.sh failed"
  # Track deployment created inside acceptance for cleanup.
  if [[ -n "${DEPLOYMENT_ID:-}" ]]; then
    TRACKED_DEPLOYMENTS+=("${DEPLOYMENT_ID}")
  elif [[ -f "${TMP_DIR}/deployment.json" ]]; then
    TRACKED_DEPLOYMENTS+=("$(read_id "${TMP_DIR}/deployment.json")")
  fi
}

run_scenario() {
  step_bootstrap_stack
  step_create_user_org_project
  step_issue_service_account
  step_acceptance
  echo "demo 18 PASSED"
}

case "${PHASE}" in
  all|--phase=all|"")
    acquire_demo_lock
    run_scenario
    ;;
  *)
    echo "Unknown phase: ${PHASE}" >&2
    echo "Usage: $0 [all]" >&2
    exit 2
    ;;
esac
