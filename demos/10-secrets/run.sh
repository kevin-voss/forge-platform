#!/usr/bin/env bash
# Demo 10: secrets lifecycle end-to-end (epic 10 acceptance gate).
# Scenario: set secret+config → bind → deploy → assert presence/length
#           → rotate → redeploy → assert new length → list metadata-only
#           → logs contain no plaintext. FORGE_AUTH_MODE=enforce throughout.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/10-secrets"
APP_DIR="${DEMO_DIR}/apps/demo"
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
export FORGE_RECONCILE_INTERVAL_MS="${FORGE_RECONCILE_INTERVAL_MS:-2000}"
export FORGE_INJECT_MASK_IN_LOGS="${FORGE_INJECT_MASK_IN_LOGS:-true}"

# Demo-only master key (32 bytes, base64). Generated per run unless provided.
if [[ -z "${FORGE_SECRETS_MASTER_KEY:-}" ]]; then
  FORGE_SECRETS_MASTER_KEY="$(python3 -c 'import base64,os; print(base64.b64encode(os.urandom(32)).decode())')"
fi
export FORGE_SECRETS_MASTER_KEY
export FORGE_SECRETS_MASTER_KEY_ID="${FORGE_SECRETS_MASTER_KEY_ID:-demo-m1}"

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
DEMO_IMAGE="${DEMO_IMAGE:-localhost:5000/demo-secrets:10}"
SERVICE_SLUG="${SERVICE_SLUG:-api}"
ENV_NAME="${ENV_NAME:-development}"
PHASE="${1:-all}"

SECRET_NAME="DATABASE_PASSWORD"
SECRET_V1="pw1"
SECRET_V2="pw-longer"
CONFIG_NAME="FEATURE_X"
CONFIG_VALUE="true"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-secrets-demo.XXXXXX")"
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
SERVICE_ID=""
SA_TOKEN=""
SESSION_TOKEN=""
DEPLOYMENT_ID=""
TRACKED_DEPLOYMENTS=()

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
  if [[ -x "${FORGE_BIN}" ]]; then
    "${FORGE_BIN}" logout >/dev/null 2>&1 || true
  fi
  "${COMPOSE[@]}" stop \
    "${RUNTIME_SERVICE}" "${CONTROL_SERVICE}" "${SECRETS_SERVICE}" "${IDENTITY_SERVICE}" \
    >/dev/null 2>&1 || true
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

dump_context() {
  echo "--- ${SECRETS_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=120 "${SECRETS_SERVICE}" >&2 || true
  echo "--- ${CONTROL_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=120 "${CONTROL_SERVICE}" >&2 || true
  echo "--- ${RUNTIME_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${RUNTIME_SERVICE}" >&2 || true
  if [[ -n "${PROJECT_ID}" && -n "${SESSION_TOKEN}" ]]; then
    echo "--- secrets audit (tail) ---" >&2
    curl --silent --show-error \
      -H "Authorization: Bearer ${SESSION_TOKEN}" \
      "${SECRETS_URL}/v1/projects/${PROJECT_ID}/audit" >&2 || true
    echo >&2
  fi
}

fail() {
  echo "Demo 10 failed: $*" >&2
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

ensure_demo_image() {
  echo "Ensuring ${DEMO_IMAGE} is in the local registry..."
  docker build -t forge/demo-secrets:local "${APP_DIR}" ||
    fail "could not build forge/demo-secrets:local"
  docker tag forge/demo-secrets:local "${DEMO_IMAGE}" || fail "could not tag ${DEMO_IMAGE}"
  docker push "${DEMO_IMAGE}" >/dev/null || fail "could not push ${DEMO_IMAGE}"
}

deployment_short() {
  # WorkloadNamer.deploymentShort: first 8 hex chars of UUID without dashes.
  python3 -c 'import sys; print(sys.argv[1].replace("-", "")[:8])' "$1"
}

wait_deployment_status() {
  local deployment_id="$1" expected="$2" attempts="${3:-60}"
  local status=""
  echo "Waiting for deployment ${deployment_id} status=${expected} ..."
  for _ in $(seq 1 "${attempts}"); do
    forge_json "${TMP_DIR}/dep-status.json" deployment status "${deployment_id}"
    status="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["status"])' "${TMP_DIR}/dep-status.json")"
    # Control wire form is "deployed"; "active" is accepted as an alias.
    if [[ "${status}" == "${expected}" ]] ||
      { [[ "${expected}" == "active" || "${expected}" == "deployed" ]] &&
        [[ "${status}" == "active" || "${status}" == "deployed" ]]; }; then
      echo "  status=${status}"
      return 0
    fi
    sleep 2
  done
  fail "deployment ${deployment_id} status=${status:-unknown}, want ${expected}"
}

wait_container() {
  local deployment_id="$1" attempts="${2:-45}"
  local short
  short="$(deployment_short "${deployment_id}")"
  echo "Waiting for managed container matching ${SERVICE_SLUG}-${short}-* ..."
  for _ in $(seq 1 "${attempts}"); do
    if docker ps --filter "label=forge.managed=true" \
      --filter "name=forge-${SERVICE_SLUG}-${short}-" -q | grep -q .; then
      return 0
    fi
    sleep 2
  done
  fail "container for deployment ${deployment_id} (short=${short}) did not appear"
}

host_port_for() {
  local deployment_id="$1"
  local short
  short="$(deployment_short "${deployment_id}")"
  curl --fail --silent --show-error "${RUNTIME_URL}/v1/node/state" |
    DEPLOYMENT_SHORT="${short}" python3 -c '
import json, os, sys
state = json.load(sys.stdin)
short = os.environ["DEPLOYMENT_SHORT"]
for w in state.get("workloads", []):
    rid = w.get("deploymentId") or ""
    if short in rid and w.get("hostPort"):
        print(w["hostPort"])
        sys.exit(0)
sys.exit("hostPort not found for short id " + short)
' || fail "could not read hostPort for ${deployment_id} from Runtime node state"
}

purge_stale_deployments() {
  # Leftover desired-state / placements from prior demos (other tenants) starve
  # node slots and hold StartReplica on secrets resolve. Clear via local Postgres
  # — API purge cannot see cross-tenant rows under FORGE_AUTH_MODE=enforce.
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

wait_secret_status() {
  local host_port="$1" want_present="$2" want_len="$3" attempts="${4:-45}"
  local body present length
  echo "Waiting for /secret-status present=${want_present} len=${want_len} on :${host_port} ..."
  for _ in $(seq 1 "${attempts}"); do
    if body="$(curl --fail --silent --show-error "http://127.0.0.1:${host_port}/secret-status" 2>/dev/null)"; then
      # Reject any response that echoes known plaintext values.
      if echo "${body}" | grep -Fq "${SECRET_V1}" || echo "${body}" | grep -Fq "${SECRET_V2}"; then
        fail "workload /secret-status leaked plaintext secret: ${body}"
      fi
      present="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["DATABASE_PASSWORD_present"])' <<<"${body}")"
      length="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["value_length"])' <<<"${body}")"
      if [[ "${present}" == "${want_present}" && "${length}" == "${want_len}" ]]; then
        echo "  secret present: ${present} (len ${length})"
        return 0
      fi
    fi
    sleep 2
  done
  fail "/secret-status did not converge (last=${body:-none})"
}

assert_list_metadata_only() {
  forge --project "${PROJECT_ID}" --env "${ENV_NAME}" --output json secret list \
    >"${TMP_DIR}/secret-list.json" || fail "forge secret list failed"
  python3 - "${TMP_DIR}/secret-list.json" "${SECRET_NAME}" "${SECRET_V1}" "${SECRET_V2}" <<'PY' || fail "secret list contained plaintext or missing metadata"
import json, sys
path, name, v1, v2 = sys.argv[1:5]
items = json.load(open(path))
assert isinstance(items, list), items
match = [i for i in items if i.get("name") == name]
assert match, f"secret {name} missing from list: {items}"
item = match[0]
assert "value" not in item, f"list item leaked value: {item}"
assert "version" in item and int(item["version"]) >= 1, item
blob = json.dumps(items)
assert v1 not in blob and v2 not in blob, "plaintext secret in list JSON"
print(f"list metadata-only OK (name={name} version={item['version']})")
PY
}

assert_no_plaintext_in_logs() {
  local logs_file="${TMP_DIR}/service-logs.txt"
  {
    "${COMPOSE[@]}" logs --no-color \
      "${SECRETS_SERVICE}" "${CONTROL_SERVICE}" "${RUNTIME_SERVICE}" 2>/dev/null || true
  } >"${logs_file}"

  python3 - "$logs_file" "${SECRET_V1}" "${SECRET_V2}" "${OWNER_PASSWORD}" "${SA_TOKEN}" <<'PY' || fail "plaintext secret found in service logs"
import sys
path, v1, v2, password, sa_token = sys.argv[1:6]
text = open(path, errors="replace").read()
leaks = []
for label, value in (
    ("secret v1", v1),
    ("secret v2", v2),
    ("owner password", password),
    ("service-account token", sa_token),
):
    if value and value in text:
        leaks.append(label)
if leaks:
    raise SystemExit("leaked: " + ", ".join(leaks))
print("logs contain plaintext secret: no")
PY
}

step_bootstrap_stack() {
  echo "== Demo 10: Secrets =="
  echo "Auth mode: FORGE_AUTH_MODE=${FORGE_AUTH_MODE} (must be enforce)"
  [[ "${FORGE_AUTH_MODE}" == "enforce" ]] || fail "FORGE_AUTH_MODE must be enforce for demo 10"
  echo "Master key id: ${FORGE_SECRETS_MASTER_KEY_ID} (demo-only, generated per run)"

  echo "Building forge CLI..."
  make -C "${CLI_DIR}" build || fail "CLI build failed"
  [[ -x "${FORGE_BIN}" ]] || fail "forge binary missing at ${FORGE_BIN}"

  echo "Starting PostgreSQL, registry, Identity, Secrets, and Control..."
  "${COMPOSE[@]}" up -d --remove-orphans "${POSTGRES_SERVICE}" "${REGISTRY_SERVICE}"
  "${COMPOSE[@]}" up -d --build --force-recreate --no-deps "${IDENTITY_SERVICE}"
  wait_http "${IDENTITY_URL}/health/ready" "Identity"
  "${COMPOSE[@]}" up -d --build --force-recreate --no-deps "${SECRETS_SERVICE}"
  wait_http "${SECRETS_URL}/health/ready" "Secrets"
  # Control starts without SA token first so project/hierarchy can be created;
  # step_issue_service_account recreates it with FORGE_SECRETS_SERVICE_ACCOUNT.
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

  forge_json "${TMP_DIR}/project.json" project create --name "demo-secrets-${SUFFIX}"
  PROJECT_ID="$(read_id "${TMP_DIR}/project.json")"
  status="$(identity_json POST /v1/projects \
    "{\"id\":\"${PROJECT_ID}\",\"org_id\":\"${ORG_ID}\",\"name\":\"demo-secrets-${SUFFIX}\"}" \
    "${TMP_DIR}/id-project.json")"
  [[ "${status}" == "201" ]] || fail "identity project register returned HTTP ${status}: $(cat "${TMP_DIR}/id-project.json")"
  export FORGE_PROJECT="${PROJECT_ID}"

  forge_json "${TMP_DIR}/environment.json" env create --project "${PROJECT_ID}" --name "${ENV_NAME}"
  ENVIRONMENT_ID="$(read_id "${TMP_DIR}/environment.json")"
  forge_json "${TMP_DIR}/application.json" app create --project "${PROJECT_ID}" --name web
  local application_id
  application_id="$(read_id "${TMP_DIR}/application.json")"
  forge_json "${TMP_DIR}/service.json" service create --app "${application_id}" --name "${SERVICE_SLUG}" --port 8080
  SERVICE_ID="$(read_id "${TMP_DIR}/service.json")"
  echo "  project_id=${PROJECT_ID} env=${ENV_NAME} service=${SERVICE_SLUG}"
}

step_issue_service_account() {
  # Control's FORGE_SECRETS_SERVICE_ACCOUNT bearer must pass Secrets authz
  # (secret.read). Identity RoleResolver evaluates project membership for user
  # principals; a project-scoped developer PAT is the supported resolve credential.
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

  echo "Recreating Control with secrets resolve token; starting Runtime..."
  "${COMPOSE[@]}" up -d --force-recreate --no-deps "${CONTROL_SERVICE}"
  wait_http "${CONTROL_URL}/health/ready" "Control"
  "${COMPOSE[@]}" up -d --build --force-recreate --no-deps "${RUNTIME_SERVICE}"
  wait_http "${RUNTIME_URL}/health/ready" "Runtime"
  purge_stale_deployments
}

step_set_and_bind() {
  echo "[set] ${SECRET_NAME} + ${CONFIG_NAME} set"
  printf '%s' "${SECRET_V1}" | forge --project "${PROJECT_ID}" --env "${ENV_NAME}" \
    secret set "${SECRET_NAME}" --from-stdin || fail "forge secret set failed"
  forge --project "${PROJECT_ID}" --env "${ENV_NAME}" \
    config set "${CONFIG_NAME}=${CONFIG_VALUE}" || fail "forge config set failed"

  local status body
  body="$(python3 -c 'import json,sys; print(json.dumps({"secrets":[sys.argv[1]],"config":[sys.argv[2]]}))' \
    "${SECRET_NAME}" "${CONFIG_NAME}")"
  status="$(secrets_json PUT \
    "/v1/projects/${PROJECT_ID}/envs/${ENV_NAME}/services/${SERVICE_SLUG}/bindings" \
    "${SESSION_TOKEN}" "${body}" "${TMP_DIR}/bindings.json")"
  [[ "${status}" == "200" ]] || fail "put bindings returned HTTP ${status}: $(cat "${TMP_DIR}/bindings.json")"
  echo "  bindings set for service ${SERVICE_SLUG}"
}

step_deploy_and_assert() {
  echo "[deploy] creating deployment with injected secrets"
  forge_json "${TMP_DIR}/deployment.json" deployment create \
    --service "${SERVICE_ID}" \
    --image "${DEMO_IMAGE}" \
    --env "${ENVIRONMENT_ID}" \
    --replicas 1
  DEPLOYMENT_ID="$(read_id "${TMP_DIR}/deployment.json")"
  TRACKED_DEPLOYMENTS+=("${DEPLOYMENT_ID}")

  wait_container "${DEPLOYMENT_ID}"
  wait_deployment_status "${DEPLOYMENT_ID}" "active"
  local host_port
  host_port="$(host_port_for "${DEPLOYMENT_ID}")"
  wait_secret_status "${host_port}" "True" "3"
  echo "[deploy] secret present: true (len 3)"
}

step_rotate_and_redeploy() {
  echo "[rotate] rotating ${SECRET_NAME}"
  printf '%s' "${SECRET_V2}" | forge --project "${PROJECT_ID}" --env "${ENV_NAME}" \
    secret rotate "${SECRET_NAME}" --from-stdin || fail "forge secret rotate failed"

  # Fingerprint drift triggers reconciler recreate; also create a fresh deploy for clarity.
  forge_json "${TMP_DIR}/deployment-v2.json" deployment create \
    --service "${SERVICE_ID}" \
    --image "${DEMO_IMAGE}" \
    --env "${ENVIRONMENT_ID}" \
    --replicas 1
  local new_id
  new_id="$(read_id "${TMP_DIR}/deployment-v2.json")"
  TRACKED_DEPLOYMENTS+=("${new_id}")
  # Scale previous desired state out of the way by deleting the old deployment.
  curl --silent --show-error \
    -H "Authorization: Bearer ${SESSION_TOKEN}" \
    -X DELETE "${CONTROL_URL}/v1/deployments/${DEPLOYMENT_ID}" >/dev/null 2>&1 || true
  DEPLOYMENT_ID="${new_id}"

  wait_container "${DEPLOYMENT_ID}"
  wait_deployment_status "${DEPLOYMENT_ID}" "active"
  local host_port
  host_port="$(host_port_for "${DEPLOYMENT_ID}")"
  wait_secret_status "${host_port}" "True" "9"
  echo "[rotate+redeploy] secret present: true (len 9) OK"
}

step_list_and_logs() {
  echo "[list] asserting metadata only"
  assert_list_metadata_only
  echo "[list] no plaintext values OK"

  echo "[logs] asserting masking (no plaintext secrets)"
  assert_no_plaintext_in_logs
  echo "[logs] no plaintext secret OK"
}

run_scenario() {
  step_bootstrap_stack
  step_create_user_org_project
  step_issue_service_account
  step_set_and_bind
  step_deploy_and_assert
  step_rotate_and_redeploy
  step_list_and_logs
  echo "demo 10 PASSED"
}

case "${PHASE}" in
  all|--phase=all|"")
    run_scenario
    ;;
  --phase=set)
    step_bootstrap_stack
    step_create_user_org_project
    step_issue_service_account
    step_set_and_bind
    step_deploy_and_assert
    echo "phase set PASSED"
    ;;
  --phase=rotate)
    step_bootstrap_stack
    step_create_user_org_project
    step_issue_service_account
    step_set_and_bind
    step_deploy_and_assert
    step_rotate_and_redeploy
    step_list_and_logs
    echo "phase rotate PASSED"
    echo "demo 10 PASSED"
    ;;
  *)
    echo "Unknown phase: ${PHASE}" >&2
    echo "Usage: $0 [all|--phase=set|--phase=rotate]" >&2
    exit 2
    ;;
esac
