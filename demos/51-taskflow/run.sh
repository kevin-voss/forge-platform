#!/usr/bin/env bash
# Demo 51: TaskFlow + managed Postgres + Identity + Secrets (epic 51.04).
# Usage:
#   demos/51-taskflow/run.sh          # build → apply → secrets → DB → seed → persist → RBAC
#   demos/51-taskflow/run.sh --down   # tear down product resources only
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/51-taskflow"
STATE_FILE="${DEMO_DIR}/.demo-state"

# Demo-only master key (32 bytes, base64). Generated per run unless provided.
if [[ -z "${FORGE_SECRETS_MASTER_KEY:-}" ]]; then
  FORGE_SECRETS_MASTER_KEY="$(python3 -c 'import base64,os; print(base64.b64encode(os.urandom(32)).decode())')"
fi
export FORGE_SECRETS_MASTER_KEY
export FORGE_SECRETS_MASTER_KEY_ID="${FORGE_SECRETS_MASTER_KEY_ID:-demo-m51}"

export FORGE_AUTH_MODE="${FORGE_AUTH_MODE:-dev}"
export FORGE_LIFECYCLE_OWNER="${FORGE_LIFECYCLE_OWNER:-control}"
export FORGE_RECONCILE_INTERVAL_MS="${FORGE_RECONCILE_INTERVAL_MS:-1000}"
export FORGE_READINESS_POLL_MS="${FORGE_READINESS_POLL_MS:-500}"
export FORGE_READINESS_MAX_WAIT_S="${FORGE_READINESS_MAX_WAIT_S:-90}"
export FORGE_RESOURCE_API_ENABLED="${FORGE_RESOURCE_API_ENABLED:-true}"
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
IDENTITY_URL="${FORGE_IDENTITY_HOST_URL:-http://127.0.0.1:4002}"
SECRETS_URL="${FORGE_SECRETS_HOST_URL:-http://127.0.0.1:4104}"
# CLI talks to host-published Secrets; Control uses docker DNS (overlay).
export FORGE_SECRETS_URL="${FORGE_SECRETS_URL:-${SECRETS_URL}}"
CONTROL_SERVICE="forge-control"
RUNTIME_SERVICE="forge-runtime"
GATEWAY_SERVICE="forge-gateway"
BUILD_SERVICE="forge-build"
IDENTITY_SERVICE="forge-identity"
SECRETS_SERVICE="forge-secrets"
POSTGRES_SERVICE="postgres"
REGISTRY_SERVICE="registry"
CLI_DIR="${ROOT_DIR}/tools/forge-cli"
FORGE_BIN="${CLI_DIR}/forge"
REGISTRY="${FORGE_REGISTRY:-localhost:5000}"
API_IMAGE="${DEMO_API_IMAGE:-${REGISTRY}/taskflow/taskflow-api:v1}"
WEB_IMAGE="${DEMO_WEB_IMAGE:-${REGISTRY}/taskflow/taskflow-web:v1}"
API_HOST="api.taskflow.localhost"
APP_HOST="app.taskflow.localhost"
DB_NAME="taskflow-db"          # instance / dependency name (may contain '-')
DB_LOGICAL_NAME="taskflow_db"  # Postgres DB name ([a-z_][a-z0-9_]*)
ENV_NAME="local"
API_SERVICE_SLUG="api"
JWT_SECRET_NAME="JWT_SIGNING_KEY"
ADMIN_EMAIL="${TASKFLOW_ADMIN_EMAIL:-admin@taskflow.local}"
ADMIN_PASSWORD="${TASKFLOW_ADMIN_PASSWORD:-AdminPass123!}"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-demo-51.XXXXXX")"
CONFIG_HOME="${TMP_DIR}/xdg-config"
export XDG_CONFIG_HOME="${CONFIG_HOME}"
export CI="${CI:-1}"
export FORGE_PROFILE="${FORGE_PROFILE:-demo51}"
export FORGE_ENDPOINT="${FORGE_ENDPOINT:-${CONTROL_URL}}"
mkdir -p "${CONFIG_HOME}"

# Ephemeral secret values for leak assertions (never written to .demo-state).
JWT_SIGNING_KEY_VALUE=""
DATABASE_URL_VALUE=""
SESSION_TOKEN=""
SA_TOKEN=""
OWNER_USER_ID=""
DEV_TOKEN=""
VIEWER_TOKEN=""

fail() {
  echo "Demo 51 failed: $*" >&2
  echo "--- ${GATEWAY_SERVICE} /admin/routes ---" >&2
  curl --silent --show-error "${GATEWAY_URL}/admin/routes" >&2 || true
  echo >&2
  echo "--- ${SECRETS_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${SECRETS_SERVICE}" >&2 || true
  echo "--- ${CONTROL_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=80 "${CONTROL_SERVICE}" >&2 || true
  echo "--- ${RUNTIME_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${RUNTIME_SERVICE}" >&2 || true
  echo "--- ${GATEWAY_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${GATEWAY_SERVICE}" >&2 || true
  echo "--- managed db containers ---" >&2
  docker ps --filter "label=forge.managed_db=true" --format '{{.Names}} {{.Status}}' >&2 || true
  exit 1
}

cleanup_tmp() {
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
API_IMAGE=${API_IMAGE}
WEB_IMAGE=${WEB_IMAGE}
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
  echo "Tearing down demo 51 TaskFlow..."
  if read_state; then
    delete_deployment "${API_DEPLOYMENT_ID:-}"
    delete_deployment "${WEB_DEPLOYMENT_ID:-}"
    rm -f "${STATE_FILE}"
  else
    echo "  no .demo-state; best-effort cleanup of demo=51 containers"
    docker ps -aq --filter "label=forge.managed=true" --filter "label=demo=51" |
      while read -r cid; do
        [[ -n "${cid}" ]] || continue
        docker rm -f "${cid}" >/dev/null 2>&1 || true
      done
  fi
  # Best-effort: leave managed DB containers for inspect unless explicitly removed.
  echo "Teardown complete."
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

secrets_json() {
  local method="$1" path="$2" token="$3" body="${4:-}" output="$5"
  local status
  local -a args=(
    --silent --show-error --output "${output}" --write-out '%{http_code}'
    --request "${method}" "${SECRETS_URL}${path}"
    --header "Authorization: Bearer ${token}"
  )
  if [[ -n "${body}" ]]; then
    args+=(--header 'content-type: application/json' --data "${body}")
  fi
  status="$(curl "${args[@]}")" || fail "Secrets ${method} ${path} did not complete"
  echo "${status}"
}

ensure_platform() {
  echo "Ensuring Postgres, registry, Identity, Secrets, Control (LocalProvisioner), Runtime, Gateway, Build..."
  "${COMPOSE[@]}" up -d "${POSTGRES_SERVICE}" "${REGISTRY_SERVICE}"
  for _ in $(seq 1 60); do
    if "${COMPOSE[@]}" exec -T "${POSTGRES_SERVICE}" pg_isready -U forge >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done
  "${COMPOSE[@]}" exec -T "${POSTGRES_SERVICE}" pg_isready -U forge >/dev/null 2>&1 ||
    fail "Postgres not ready"

  "${COMPOSE[@]}" up -d --build "${IDENTITY_SERVICE}"
  wait_http "${IDENTITY_URL}/health/ready" "Identity"
  "${COMPOSE[@]}" up -d --build --force-recreate --no-deps "${SECRETS_SERVICE}"
  wait_http "${SECRETS_URL}/health/ready" "Secrets"

  local need_recreate=0
  local auth_mode pattern strategy provisioner secrets_url
  auth_mode="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_AUTH_MODE 2>/dev/null || true)"
  pattern="$(docker exec "${GATEWAY_SERVICE}" printenv FORGE_HOST_PATTERN 2>/dev/null || true)"
  strategy="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_SCHEDULER_STRATEGY 2>/dev/null || true)"
  provisioner="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_DB_PROVISIONER 2>/dev/null || true)"
  secrets_url="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_SECRETS_URL 2>/dev/null || true)"
  # Main product path uses Control auth=dev; RBAC proof switches enforce later.
  if [[ "${auth_mode}" != "dev" ]]; then
    need_recreate=1
  fi
  if [[ "${pattern}" != *'{service}.taskflow.localhost'* ]]; then
    need_recreate=1
  fi
  if [[ "${strategy}" != "single-node" ]]; then
    need_recreate=1
  fi
  if [[ "${provisioner}" != "local" ]]; then
    need_recreate=1
  fi
  if [[ "${secrets_url}" != "http://forge-secrets:8080" ]]; then
    need_recreate=1
  fi
  if ! docker exec "${CONTROL_SERVICE}" test -S /var/run/docker.sock 2>/dev/null; then
    need_recreate=1
  fi

  if [[ "${need_recreate}" -eq 1 ]]; then
    echo "Recreating Control/Runtime/Gateway with demo 51 Secrets + managed-DB overlay..."
    FORGE_AUTH_MODE=dev "${COMPOSE[@]}" up -d --force-recreate \
      "${CONTROL_SERVICE}" "${RUNTIME_SERVICE}" "${GATEWAY_SERVICE}"
  else
    echo "Control/Gateway already configured for demo 51; ensuring they are up..."
    FORGE_AUTH_MODE=dev "${COMPOSE[@]}" up -d "${CONTROL_SERVICE}" "${RUNTIME_SERVICE}" "${GATEWAY_SERVICE}"
  fi
  "${COMPOSE[@]}" up -d "${BUILD_SERVICE}"

  wait_http "${CONTROL_URL}/health/ready" "Control"
  wait_http "${RUNTIME_URL}/health/ready" "Runtime"
  wait_http "${GATEWAY_URL}/health/ready" "Gateway"
  wait_http "${BUILD_URL}/health/ready" "Build" 60 || true

  pattern="$(docker exec "${GATEWAY_SERVICE}" printenv FORGE_HOST_PATTERN 2>/dev/null || true)"
  [[ "${pattern}" == *'{service}.taskflow.localhost'* ]] ||
    fail "gateway FORGE_HOST_PATTERN must be '{service}.taskflow.localhost' (got: ${pattern})"
  provisioner="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_DB_PROVISIONER 2>/dev/null || true)"
  [[ "${provisioner}" == "local" ]] ||
    fail "control FORGE_DB_PROVISIONER must be local (got: ${provisioner})"
  secrets_url="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_SECRETS_URL 2>/dev/null || true)"
  [[ "${secrets_url}" == "http://forge-secrets:8080" ]] ||
    fail "control FORGE_SECRETS_URL must be http://forge-secrets:8080 (got: ${secrets_url})"
}

bootstrap_identity_project() {
  echo "Bootstrapping Identity org/project for Control project ${PROJECT_ID}..."
  [[ -n "${PROJECT_ID}" ]] || fail "PROJECT_ID required for Identity bootstrap"
  local suffix status owner_email owner_password org_id
  suffix="$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')"
  owner_email="taskflow-owner-${suffix}@example.com"
  owner_password="OwnerPass123!"

  status="$(identity_json POST /v1/auth/register \
    "{\"email\":\"${owner_email}\",\"password\":\"${owner_password}\",\"display_name\":\"TaskFlow Owner\"}" \
    "${TMP_DIR}/id-register.json")"
  [[ "${status}" == "201" ]] || fail "identity register HTTP ${status}: $(cat "${TMP_DIR}/id-register.json")"
  OWNER_USER_ID="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["user_id"])' "${TMP_DIR}/id-register.json")"

  status="$(identity_json POST /v1/auth/login \
    "{\"email\":\"${owner_email}\",\"password\":\"${owner_password}\"}" \
    "${TMP_DIR}/id-login.json")"
  [[ "${status}" == "200" ]] || fail "identity login HTTP ${status}: $(cat "${TMP_DIR}/id-login.json")"
  SESSION_TOKEN="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["session_token"])' "${TMP_DIR}/id-login.json")"
  forge login --token "${SESSION_TOKEN}" || fail "forge login with session failed"

  status="$(identity_json POST /v1/orgs \
    "{\"name\":\"TaskFlow Org ${suffix}\"}" \
    "${TMP_DIR}/id-org.json")"
  [[ "${status}" == "201" ]] || fail "create org HTTP ${status}: $(cat "${TMP_DIR}/id-org.json")"
  org_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "${TMP_DIR}/id-org.json")"
  status="$(identity_json POST "/v1/orgs/${org_id}/members" \
    "{\"user_id\":\"${OWNER_USER_ID}\",\"role\":\"organization-owner\"}" \
    "${TMP_DIR}/id-org-member.json")"
  [[ "${status}" == "201" || "${status}" == "200" ]] ||
    fail "org member HTTP ${status}: $(cat "${TMP_DIR}/id-org-member.json")"

  status="$(identity_json POST /v1/projects \
    "{\"id\":\"${PROJECT_ID}\",\"org_id\":\"${org_id}\",\"name\":\"taskflow-${suffix}\"}" \
    "${TMP_DIR}/id-project.json")"
  if [[ "${status}" != "201" && "${status}" != "409" ]]; then
    fail "identity project register HTTP ${status}: $(cat "${TMP_DIR}/id-project.json")"
  fi
  status="$(identity_json POST "/v1/projects/${PROJECT_ID}/members" \
    "{\"user_id\":\"${OWNER_USER_ID}\",\"role\":\"project-admin\"}" \
    "${TMP_DIR}/id-proj-member.json")"
  [[ "${status}" == "201" || "${status}" == "200" || "${status}" == "409" ]] ||
    fail "project-admin member HTTP ${status}: $(cat "${TMP_DIR}/id-proj-member.json")"

  # Developer + viewer principals for deploy RBAC proof.
  status="$(identity_json POST /v1/users \
    "{\"email\":\"dev-${suffix}@example.com\",\"display_name\":\"Developer\"}" \
    "${TMP_DIR}/dev-user.json")"
  [[ "${status}" == "201" ]] || fail "create developer user HTTP ${status}: $(cat "${TMP_DIR}/dev-user.json")"
  DEV_USER_ID="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "${TMP_DIR}/dev-user.json")"
  status="$(identity_json POST /v1/users \
    "{\"email\":\"viewer-${suffix}@example.com\",\"display_name\":\"Viewer\"}" \
    "${TMP_DIR}/viewer-user.json")"
  [[ "${status}" == "201" ]] || fail "create viewer user HTTP ${status}: $(cat "${TMP_DIR}/viewer-user.json")"
  VIEWER_USER_ID="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "${TMP_DIR}/viewer-user.json")"

  status="$(identity_json POST "/v1/projects/${PROJECT_ID}/members" \
    "{\"user_id\":\"${DEV_USER_ID}\",\"role\":\"developer\"}" \
    "${TMP_DIR}/dev-member.json")"
  [[ "${status}" == "201" || "${status}" == "200" || "${status}" == "409" ]] ||
    fail "developer member HTTP ${status}: $(cat "${TMP_DIR}/dev-member.json")"
  status="$(identity_json POST "/v1/projects/${PROJECT_ID}/members" \
    "{\"user_id\":\"${VIEWER_USER_ID}\",\"role\":\"viewer\"}" \
    "${TMP_DIR}/viewer-member.json")"
  [[ "${status}" == "201" || "${status}" == "200" || "${status}" == "409" ]] ||
    fail "viewer member HTTP ${status}: $(cat "${TMP_DIR}/viewer-member.json")"

  status="$(identity_json POST /v1/tokens \
    "{\"owner\":{\"type\":\"user\",\"id\":\"${DEV_USER_ID}\"},\"project_id\":\"${PROJECT_ID}\",\"role\":\"developer\"}" \
    "${TMP_DIR}/dev-token.json")"
  [[ "${status}" == "201" ]] || fail "developer PAT HTTP ${status}: $(cat "${TMP_DIR}/dev-token.json")"
  DEV_TOKEN="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["token"])' "${TMP_DIR}/dev-token.json")"
  status="$(identity_json POST /v1/tokens \
    "{\"owner\":{\"type\":\"user\",\"id\":\"${VIEWER_USER_ID}\"},\"project_id\":\"${PROJECT_ID}\",\"role\":\"viewer\"}" \
    "${TMP_DIR}/viewer-token.json")"
  [[ "${status}" == "201" ]] || fail "viewer PAT HTTP ${status}: $(cat "${TMP_DIR}/viewer-token.json")"
  VIEWER_TOKEN="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["token"])' "${TMP_DIR}/viewer-token.json")"
  [[ "${DEV_TOKEN}" == forge_pat_* ]] || fail "developer token missing forge_pat_ prefix"
  [[ "${VIEWER_TOKEN}" == forge_pat_* ]] || fail "viewer token missing forge_pat_ prefix"
  echo "  Identity project=${PROJECT_ID} developer/viewer PATs issued"
}

issue_secrets_service_account() {
  echo "Issuing Control secrets-resolve token (developer PAT)..."
  [[ -n "${OWNER_USER_ID}" && -n "${PROJECT_ID}" ]] || fail "OWNER_USER_ID/PROJECT_ID required"
  local status
  status="$(identity_json POST /v1/tokens \
    "{\"owner\":{\"type\":\"user\",\"id\":\"${OWNER_USER_ID}\"},\"project_id\":\"${PROJECT_ID}\",\"role\":\"developer\"}" \
    "${TMP_DIR}/sa-token.json")"
  [[ "${status}" == "201" ]] || fail "create resolve token HTTP ${status}: $(cat "${TMP_DIR}/sa-token.json")"
  SA_TOKEN="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["token"])' "${TMP_DIR}/sa-token.json")"
  [[ "${SA_TOKEN}" == forge_pat_* ]] || fail "resolve token missing forge_pat_ prefix"
  export FORGE_SECRETS_SERVICE_ACCOUNT="${SA_TOKEN}"

  echo "Recreating Control with FORGE_SECRETS_SERVICE_ACCOUNT for resolve/attach..."
  FORGE_AUTH_MODE=dev "${COMPOSE[@]}" up -d --force-recreate --no-deps "${CONTROL_SERVICE}"
  wait_http "${CONTROL_URL}/health/ready" "Control (secrets SA)"
  local sa_len
  sa_len="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_SECRETS_SERVICE_ACCOUNT 2>/dev/null | wc -c | tr -d ' ')"
  [[ "${sa_len}" -gt 20 ]] ||
    fail "Control missing FORGE_SECRETS_SERVICE_ACCOUNT after recreate (len=${sa_len})"
  echo "  Control has secrets resolve token (len=${sa_len})"
}

provision_taskflow_secrets() {
  echo "Provisioning Forge Secrets (JWT_SIGNING_KEY + bindings for service ${API_SERVICE_SLUG})..."
  [[ -n "${PROJECT_ID}" && -n "${SESSION_TOKEN}" ]] || fail "PROJECT_ID/SESSION_TOKEN required for secrets"
  JWT_SIGNING_KEY_VALUE="$(python3 -c 'import secrets; print(secrets.token_urlsafe(32))')"
  [[ -n "${JWT_SIGNING_KEY_VALUE}" ]] || fail "failed to generate JWT signing key"

  printf '%s' "${JWT_SIGNING_KEY_VALUE}" | forge --project "${PROJECT_ID}" --env "${ENV_NAME}" \
    secret set "${JWT_SECRET_NAME}" --from-stdin || fail "forge secret set ${JWT_SECRET_NAME} failed"

  # Metadata-only list must not echo the value.
  forge --project "${PROJECT_ID}" --env "${ENV_NAME}" --output json secret list \
    >"${TMP_DIR}/secret-list.json" || fail "forge secret list failed"
  JWT_SECRET_NAME="${JWT_SECRET_NAME}" JWT_SIGNING_KEY_VALUE="${JWT_SIGNING_KEY_VALUE}" \
    python3 - "${TMP_DIR}/secret-list.json" <<'PY' || fail "secret list leaked plaintext or missing JWT secret"
import json, os, sys
items = json.load(open(sys.argv[1]))
name = os.environ["JWT_SECRET_NAME"]
value = os.environ["JWT_SIGNING_KEY_VALUE"]
match = [i for i in items if i.get("name") == name]
assert match, items
assert "value" not in match[0], match[0]
blob = json.dumps(items)
assert value not in blob, "plaintext JWT key in secret list"
print(f"  secret {name} metadata-only OK (version={match[0].get('version')})")
PY

  local body status
  body="$(python3 -c 'import json,sys; print(json.dumps({"secrets":[sys.argv[1]],"config":[]}))' \
    "${JWT_SECRET_NAME}")"
  status="$(secrets_json PUT \
    "/v1/projects/${PROJECT_ID}/envs/${ENV_NAME}/services/${API_SERVICE_SLUG}/bindings" \
    "${SESSION_TOKEN}" "${body}" "${TMP_DIR}/bindings.json")"
  [[ "${status}" == "200" ]] ||
    fail "put bindings HTTP ${status}: $(cat "${TMP_DIR}/bindings.json")"
  echo "  bindings set for service ${API_SERVICE_SLUG} (secret ${JWT_SECRET_NAME})"
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
      forge build --source . --forge-yaml web.forge.yaml --tag "${WEB_IMAGE}"
    ) || fail "forge build web failed"
    return 0
  fi

  echo "forge build CLI not available; building from source with docker build+push..."
  docker build -f "${DEMO_DIR}/api/Dockerfile" -t "${API_IMAGE}" "${DEMO_DIR}" ||
    fail "docker build api failed"
  docker push "${API_IMAGE}" || fail "docker push api failed"
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
  # Leftover desired-state from prior TaskFlow / other demos leaves multiple
  # Gateway upstreams; seed then round-robins into a container without our DB.
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
  wait_deployment_status "${WEB_DEPLOYMENT_ID}" "deployed" 120
  echo "  applications Ready (deployments active)"
}

provision_managed_db() {
  echo "Provisioning managed Database ${DB_NAME} (dependencies.database)..."
  [[ -n "${PROJECT_ID}" ]] || fail "PROJECT_ID missing; cannot create managed database"
  # Instance name matches the dependency name (taskflow-db). Logical Postgres DB
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
    database attach "${DB_NAME}" --app taskflow-api --env-var DATABASE_URL
  python3 - <<'PY' "${TMP_DIR}/db-attach.json" || fail "attach missing secretRef"
import json, sys
body = json.load(open(sys.argv[1]))
ref = body.get("secretRef") or body.get("secret_ref") or ""
assert ref, body
assert "://" not in ref, body
print(f"  attached DATABASE_URL secretRef={ref}")
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

container_env() {
  local cid="$1" key="$2"
  # Distroless images have no printenv/shell — read Config.Env via inspect.
  docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "${cid}" 2>/dev/null |
    awk -F= -v k="${key}" '$1==k { print substr($0, index($0, "=")+1); exit }'
}

wait_secrets_injected() {
  local cid="" url="" jwt="" i
  echo "Waiting for DATABASE_URL + JWT_SIGNING_KEY injection into API container..."
  for i in $(seq 1 180); do
    cid="$(api_container_id)"
    if [[ -n "${cid}" ]]; then
      url="$(container_env "${cid}" DATABASE_URL)"
      jwt="$(container_env "${cid}" JWT_SIGNING_KEY)"
      if [[ -n "${url}" && -n "${jwt}" ]]; then
        DATABASE_URL_VALUE="${url}"
        if [[ -n "${JWT_SIGNING_KEY_VALUE}" && "${jwt}" != "${JWT_SIGNING_KEY_VALUE}" ]]; then
          fail "container JWT_SIGNING_KEY does not match provisioned secret"
        fi
        echo "  DATABASE_URL + JWT_SIGNING_KEY present on container ${cid:0:12}"
        return 0
      fi
    fi
    sleep 1
  done
  fail "DATABASE_URL / JWT_SIGNING_KEY never appeared on API container"
}

assert_no_plaintext_secrets() {
  local rendered="${TMP_DIR}/forge.yaml"
  local logs_file="${TMP_DIR}/platform-logs.txt"
  echo "Asserting no plaintext secrets in rendered manifest + platform/API logs..."
  [[ -f "${rendered}" ]] || fail "rendered forge.yaml missing at ${rendered}"
  [[ -n "${JWT_SIGNING_KEY_VALUE}" ]] || fail "JWT_SIGNING_KEY_VALUE unset"
  [[ -n "${DATABASE_URL_VALUE}" ]] || fail "DATABASE_URL_VALUE unset"

  # Manifest: secret refs only; never the JWT value or a postgres URL.
  JWT_SIGNING_KEY_VALUE="${JWT_SIGNING_KEY_VALUE}" DATABASE_URL_VALUE="${DATABASE_URL_VALUE}" \
    python3 - "${rendered}" <<'PY' || fail "rendered manifest contained plaintext secrets"
import os, sys, re
path = sys.argv[1]
text = open(path, errors="replace").read()
jwt = os.environ["JWT_SIGNING_KEY_VALUE"]
db = os.environ["DATABASE_URL_VALUE"]
assert jwt not in text, "JWT signing key leaked into forge.yaml"
assert db not in text, "DATABASE_URL leaked into forge.yaml"
assert "postgres://" not in text.lower() and "postgresql://" not in text.lower(), \
    "plaintext postgres URL in forge.yaml"
assert "taskflow-dev-jwt-key" not in text, "legacy plaintext JWT default in forge.yaml"
assert "valueFrom" in text and "JWT_SIGNING_KEY" in text and "DATABASE_URL" in text, \
    "forge.yaml missing valueFrom secret refs"
# No plaintext value: for the secret env entries.
assert not re.search(r"name:\s*JWT_SIGNING_KEY\s*\n\s*value:\s*\S+", text), \
    "JWT_SIGNING_KEY has plaintext value: in forge.yaml"
assert not re.search(r"name:\s*DATABASE_URL\s*\n\s*value:\s*\S+", text), \
    "DATABASE_URL has plaintext value: in forge.yaml"
print("  manifest: secret refs only OK")
PY

  local cid
  cid="$(api_container_id)"
  {
    "${COMPOSE[@]}" logs --no-color \
      "${SECRETS_SERVICE}" "${CONTROL_SERVICE}" "${RUNTIME_SERVICE}" 2>/dev/null || true
    if [[ -n "${cid}" ]]; then
      docker logs "${cid}" 2>&1 || true
    fi
  } >"${logs_file}"

  JWT_SIGNING_KEY_VALUE="${JWT_SIGNING_KEY_VALUE}" DATABASE_URL_VALUE="${DATABASE_URL_VALUE}" \
    SA_TOKEN="${SA_TOKEN}" python3 - "${logs_file}" <<'PY' || fail "plaintext secret found in logs"
import os, sys
path = sys.argv[1]
text = open(path, errors="replace").read()
for label, needle in (
    ("JWT_SIGNING_KEY", os.environ["JWT_SIGNING_KEY_VALUE"]),
    ("DATABASE_URL", os.environ["DATABASE_URL_VALUE"]),
    ("SA_TOKEN", os.environ.get("SA_TOKEN", "")),
):
    if needle and needle in text:
        raise SystemExit(f"plaintext {label} found in platform/API logs")
print("  logs: no plaintext secret values OK")
PY
}

login_admin_token() {
  local code
  code="$(curl --silent --show-error -o "${TMP_DIR}/admin-login.json" -w '%{http_code}' \
    -H "Host: ${API_HOST}" -H 'content-type: application/json' \
    -d "{\"email\":\"${ADMIN_EMAIL}\",\"password\":\"${ADMIN_PASSWORD}\"}" \
    "${GATEWAY_URL}/auth/login" || echo "000")"
  [[ "${code}" == "200" ]] || fail "admin login HTTP ${code}: $(cat "${TMP_DIR}/admin-login.json")"
  python3 -c 'import json,sys; b=json.load(open(sys.argv[1])); t=b.get("token") or b.get("pat") or ""; assert t, b; print(t)' \
    "${TMP_DIR}/admin-login.json"
}

prove_persistence() {
  local title="persist-$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')"
  local create_body task_id code cid token
  echo "Proving task persistence across API container restart..."
  token="$(login_admin_token)"
  create_body="$(python3 -c 'import json,sys; print(json.dumps({"title":sys.argv[1]}))' "${title}")"
  code="$(curl --silent --show-error -o "${TMP_DIR}/create-task.json" -w '%{http_code}' \
    -H "Host: ${API_HOST}" -H 'content-type: application/json' \
    -H "Authorization: Bearer ${token}" \
    -d "${create_body}" "${GATEWAY_URL}/tasks" || echo "000")"
  [[ "${code}" == "201" ]] || fail "create task HTTP ${code}: $(cat "${TMP_DIR}/create-task.json")"
  task_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "${TMP_DIR}/create-task.json")"
  [[ -n "${task_id}" ]] || fail "create task missing id"

  cid="$(api_container_id)"
  [[ -n "${cid}" ]] || fail "API container missing before restart"
  echo "  restarting API container ${cid:0:12}..."
  docker restart "${cid}" >/dev/null || fail "docker restart api failed"
  # Gateway may briefly 502/503 while the container and upstream probe recover.
  wait_host_http "${API_HOST}" "/health/ready" 200 120
  refresh_routes
  token="$(login_admin_token)"

  code="000"
  for _ in $(seq 1 60); do
    code="$(curl --silent --show-error -o "${TMP_DIR}/list-tasks.json" -w '%{http_code}' \
      -H "Host: ${API_HOST}" -H "Authorization: Bearer ${token}" \
      "${GATEWAY_URL}/tasks" || echo "000")"
    if [[ "${code}" == "200" ]]; then
      break
    fi
    sleep 1
  done
  [[ "${code}" == "200" ]] || fail "list tasks after restart HTTP ${code}: $(cat "${TMP_DIR}/list-tasks.json" 2>/dev/null || true)"
  TITLE="${title}" TASK_ID="${task_id}" python3 - <<'PY' "${TMP_DIR}/list-tasks.json" || fail "task missing after restart"
import json, os, sys
tasks = json.load(open(sys.argv[1]))
want_id = os.environ["TASK_ID"]
want_title = os.environ["TITLE"]
match = [t for t in tasks if t.get("id") == want_id]
assert match, {"want": want_id, "tasks": tasks}
assert match[0].get("title") == want_title, match[0]
print(f"  persisted task id={want_id} title={want_title}")
PY
}

prove_deploy_rbac() {
  echo "Proving deploy RBAC (viewer PAT → 403, developer PAT → 201)..."
  [[ -n "${DEV_TOKEN:-}" && -n "${VIEWER_TOKEN:-}" ]] || fail "DEV_TOKEN/VIEWER_TOKEN missing; bootstrap Identity first"
  [[ -n "${API_DEPLOYMENT_ID}" ]] || fail "API_DEPLOYMENT_ID missing"

  curl --fail --silent --show-error "${CONTROL_URL}/v1/deployments/${API_DEPLOYMENT_ID}" \
    >"${TMP_DIR}/dep.json" || fail "GET deployment failed"
  local service_id environment_id image
  service_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("serviceId") or "")' "${TMP_DIR}/dep.json")"
  environment_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("environmentId") or "")' "${TMP_DIR}/dep.json")"
  image="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("image") or "")' "${TMP_DIR}/dep.json")"
  [[ -n "${service_id}" && -n "${environment_id}" && -n "${image}" ]] ||
    fail "deployment missing serviceId/environmentId/image: $(cat "${TMP_DIR}/dep.json")"

  echo "  switching Control to FORGE_AUTH_MODE=enforce for RBAC proof..."
  FORGE_AUTH_MODE=enforce "${COMPOSE[@]}" up -d --force-recreate --no-deps "${CONTROL_SERVICE}"
  wait_http "${CONTROL_URL}/health/ready" "Control (enforce)"
  local mode
  mode="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_AUTH_MODE 2>/dev/null || true)"
  [[ "${mode}" == "enforce" ]] || fail "Control FORGE_AUTH_MODE want enforce, got ${mode}"

  local body status
  body="$(python3 -c 'import json,sys; print(json.dumps({"image":sys.argv[1],"desiredReplicas":1,"environmentId":sys.argv[2]}))' \
    "${image}" "${environment_id}")"

  status="$(control_json POST "/v1/services/${service_id}/deployments" "${VIEWER_TOKEN}" "${body}" \
    "${TMP_DIR}/deploy-viewer.json")"
  [[ "${status}" == "403" ]] ||
    fail "viewer deploy expected 403, got ${status}: $(cat "${TMP_DIR}/deploy-viewer.json")"
  python3 -c 'import json,sys; e=json.load(open(sys.argv[1])); assert e.get("error",{}).get("code")=="forbidden", e' \
    "${TMP_DIR}/deploy-viewer.json" || fail "viewer deploy missing forbidden envelope"
  echo "  viewer PAT deploy → 403 OK"

  status="$(control_json POST "/v1/services/${service_id}/deployments" "${DEV_TOKEN}" "${body}" \
    "${TMP_DIR}/deploy-dev.json")"
  [[ "${status}" == "201" ]] ||
    fail "developer deploy expected 201, got ${status}: $(cat "${TMP_DIR}/deploy-dev.json")"
  local extra_dep
  extra_dep="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("id",""))' "${TMP_DIR}/deploy-dev.json")"
  echo "  developer PAT deploy → 201 OK (deployment ${extra_dep})"

  # Best-effort cleanup of the proof deployment, then restore Control to auth=dev.
  if [[ -n "${extra_dep}" ]]; then
    curl --silent --show-error -X DELETE \
      -H "Authorization: Bearer ${DEV_TOKEN}" \
      "${CONTROL_URL}/v1/deployments/${extra_dep}" >/dev/null 2>&1 || true
  fi
  echo "  restoring Control FORGE_AUTH_MODE=dev..."
  FORGE_AUTH_MODE=dev "${COMPOSE[@]}" up -d --force-recreate --no-deps "${CONTROL_SERVICE}"
  wait_http "${CONTROL_URL}/health/ready" "Control (dev)"
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
  PROJECT_NAME="TaskFlow ${SUFFIX}"
  PROJECT_SLUG="taskflow-${SUFFIX}"

  echo "Rendering forge.yaml → apply (project=${PROJECT_SLUG})..."
  PROJECT_NAME="${PROJECT_NAME}" PROJECT_SLUG="${PROJECT_SLUG}" \
    API_IMAGE="${API_IMAGE}" WEB_IMAGE="${WEB_IMAGE}" \
    envsubst '${PROJECT_NAME} ${PROJECT_SLUG} ${API_IMAGE} ${WEB_IMAGE}' \
    <"${DEMO_DIR}/forge.yaml" >"${TMP_DIR}/forge.yaml"

  forge_json "${TMP_DIR}/apply.json" apply -f "${TMP_DIR}/forge.yaml"

  PROJECT_ID=""
  API_DEPLOYMENT_ID=""
  WEB_DEPLOYMENT_ID=""
  while IFS= read -r line; do
    case "${line}" in
      PROJECT_ID=*) PROJECT_ID="${line#PROJECT_ID=}" ;;
      DEPLOYMENT:taskflow-api=*) API_DEPLOYMENT_ID="${line#DEPLOYMENT:taskflow-api=}" ;;
      DEPLOYMENT:taskflow-web=*) WEB_DEPLOYMENT_ID="${line#DEPLOYMENT:taskflow-web=}" ;;
    esac
  done < <(extract_apply_ids)

  [[ -n "${API_DEPLOYMENT_ID}" ]] || fail "taskflow-api Deployment id missing from apply"
  [[ -n "${WEB_DEPLOYMENT_ID}" ]] || fail "taskflow-web Deployment id missing from apply"

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
  echo "Deployments api=${API_DEPLOYMENT_ID} web=${WEB_DEPLOYMENT_ID} project=${PROJECT_ID}"

  # Identity + Secrets SA must exist before managed-db attach / JWT bindings.
  bootstrap_identity_project
  issue_secrets_service_account
  provision_taskflow_secrets
  provision_managed_db
  wait_secrets_injected
  assert_no_plaintext_secrets
  assert_applications_ready
  wait_route_host "${API_HOST}" 90
  wait_route_host "${APP_HOST}" 90
  wait_host_http "${API_HOST}" "/health/ready" 200 90
  wait_host_http "${APP_HOST}" "/" 200 60

  # Optional: forge wait Ready when CLI supports it.
  if "${FORGE_BIN}" wait --help >/dev/null 2>&1; then
    forge wait "application/taskflow-api" --for=condition=Ready --timeout=2m ||
      fail "forge wait taskflow-api failed"
    forge wait "application/taskflow-web" --for=condition=Ready --timeout=2m ||
      fail "forge wait taskflow-web failed"
  fi

  write_state
  bash "${DEMO_DIR}/seed.sh" || fail "seed.sh failed"
  prove_persistence
  prove_deploy_rbac

  echo
  echo "demo 51 deploy READY (managed Postgres + Identity + Secrets injection + deploy RBAC)"
  echo "  App:          http://${APP_HOST}:4000/"
  echo "  API:          http://${API_HOST}:4000/health/ready"
  echo "  API image:    ${API_IMAGE}"
  echo "  Web image:    ${WEB_IMAGE}"
  echo "  Database:     ${DB_NAME} (Ready; DATABASE_URL via Secrets)"
  echo "  Secrets:      ${JWT_SECRET_NAME} bound to ${API_SERVICE_SLUG}"
  echo "  Identity:     ${IDENTITY_URL}"
  echo "  Deployments:  api=${API_DEPLOYMENT_ID} web=${WEB_DEPLOYMENT_ID}"
  echo "  Project:      ${PROJECT_SLUG} (${PROJECT_ID})"
  echo "  Seed logins:  ${ADMIN_EMAIL} / ${ADMIN_PASSWORD}"
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
