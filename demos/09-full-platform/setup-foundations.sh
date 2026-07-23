#!/usr/bin/env bash
# Capstone 19.03 foundations wiring: Identity roles, Secrets injection,
# managed Postgres attach, Storage bucket. Intended to be sourced by deploy.sh
# or invoked after the platform stack is healthy.
#
# Required env (set by deploy.sh): ROOT_DIR, DEMO_DIR, TMP_DIR, COMPOSE,
# CONTROL_URL, IDENTITY_URL, SECRETS_URL, STORAGE_URL, FORGE_BIN, ENV_NAME,
# APPLICATION_NAME, PROJECT_NAME, OWNER_EMAIL, OWNER_PASSWORD, SUFFIX
set -euo pipefail

: "${TMP_DIR:?TMP_DIR required}"
: "${CONTROL_URL:?}"
: "${IDENTITY_URL:?}"
: "${SECRETS_URL:?}"
: "${STORAGE_URL:?}"
: "${FORGE_BIN:?}"

ENV_NAME="${ENV_NAME:-development}"
APPLICATION_NAME="${APPLICATION_NAME:-incident}"
DB_NAME="${FORGE_CAPSTONE_DB_NAME:-incidents}"
STORAGE_BUCKET="${FORGE_STORAGE_BUCKET:-artifacts}"
APP_SHARED_SECRET="${APP_SHARED_SECRET:-capstone-shared-$(date +%s)}"
PRODUCT_MODE="${PRODUCT_MODE:-capstone}"
CONFIG_HOME="${XDG_CONFIG_HOME:-${TMP_DIR}/xdg-config}"
mkdir -p "${CONFIG_HOME}"

fail() {
  echo "setup-foundations failed: $*" >&2
  exit 1
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
  local method="$1" path="$2" token="$3" body="$4" output="$5"
  local status
  status="$(curl --silent --show-error --output "${output}" --write-out '%{http_code}' \
    --request "${method}" "${SECRETS_URL}${path}" \
    --header "Authorization: Bearer ${token}" \
    --header 'content-type: application/json' \
    --data "${body}")" || fail "Secrets ${method} ${path} did not complete"
  echo "${status}"
}

control_json() {
  local method="$1" path="$2" token="$3" body="$4" output="$5"
  local status
  status="$(curl --silent --show-error --output "${output}" --write-out '%{http_code}' \
    --request "${method}" "${CONTROL_URL}${path}" \
    --header "Authorization: Bearer ${token}" \
    --header 'content-type: application/json' \
    --data "${body}")" || fail "Control ${method} ${path} did not complete"
  echo "${status}"
}

setup_identity_owner() {
  echo "[foundations] register owner + login"
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
    "{\"name\":\"Capstone Org ${SUFFIX}\"}" \
    "${TMP_DIR}/org.json")"
  [[ "${status}" == "201" ]] || fail "create org returned HTTP ${status}: $(cat "${TMP_DIR}/org.json")"
  ORG_ID="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "${TMP_DIR}/org.json")"
  status="$(identity_json POST "/v1/orgs/${ORG_ID}/members" \
    "{\"user_id\":\"${OWNER_USER_ID}\",\"role\":\"organization-owner\"}" \
    "${TMP_DIR}/org-member.json")"
  [[ "${status}" == "201" || "${status}" == "200" ]] ||
    fail "add org member returned HTTP ${status}: $(cat "${TMP_DIR}/org-member.json")"
}

setup_control_project() {
  echo "[foundations] create Control project / env / app"
  forge_json "${TMP_DIR}/project.json" project create --name "${PROJECT_NAME}-${SUFFIX}"
  PROJECT_ID="$(read_id "${TMP_DIR}/project.json")"
  local status
  status="$(identity_json POST /v1/projects \
    "{\"id\":\"${PROJECT_ID}\",\"org_id\":\"${ORG_ID}\",\"name\":\"${PROJECT_NAME}-${SUFFIX}\"}" \
    "${TMP_DIR}/id-project.json")"
  [[ "${status}" == "201" ]] || fail "identity project register returned HTTP ${status}: $(cat "${TMP_DIR}/id-project.json")"
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
}

setup_role_tokens() {
  echo "[foundations] create developer + viewer principals"
  local status
  DEV_EMAIL="dev-${SUFFIX}@example.com"
  VIEWER_EMAIL="viewer-${SUFFIX}@example.com"
  status="$(identity_json POST /v1/users \
    "{\"email\":\"${DEV_EMAIL}\",\"password\":\"DevPass123!\",\"display_name\":\"Developer\"}" \
    "${TMP_DIR}/dev-user.json")"
  [[ "${status}" == "201" ]] || fail "create developer user returned HTTP ${status}: $(cat "${TMP_DIR}/dev-user.json")"
  DEV_USER_ID="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "${TMP_DIR}/dev-user.json")"

  status="$(identity_json POST /v1/users \
    "{\"email\":\"${VIEWER_EMAIL}\",\"password\":\"ViewerPass123!\",\"display_name\":\"Viewer\"}" \
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

  status="$(identity_json POST /v1/tokens \
    "{\"owner\":{\"type\":\"user\",\"id\":\"${DEV_USER_ID}\"},\"project_id\":\"${PROJECT_ID}\",\"role\":\"developer\"}" \
    "${TMP_DIR}/dev-token.json")"
  [[ "${status}" == "201" ]] || fail "create developer token returned HTTP ${status}: $(cat "${TMP_DIR}/dev-token.json")"
  DEV_TOKEN="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["token"])' "${TMP_DIR}/dev-token.json")"

  status="$(identity_json POST /v1/tokens \
    "{\"owner\":{\"type\":\"user\",\"id\":\"${VIEWER_USER_ID}\"},\"project_id\":\"${PROJECT_ID}\",\"role\":\"viewer\"}" \
    "${TMP_DIR}/viewer-token.json")"
  [[ "${status}" == "201" ]] || fail "create viewer token returned HTTP ${status}: $(cat "${TMP_DIR}/viewer-token.json")"
  VIEWER_TOKEN="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["token"])' "${TMP_DIR}/viewer-token.json")"

  forge login --token "${DEV_TOKEN}" || fail "forge login --token developer failed"
}

issue_secrets_service_account() {
  echo "[foundations] issue Control secrets-resolve token"
  local status
  status="$(identity_json POST /v1/tokens \
    "{\"owner\":{\"type\":\"user\",\"id\":\"${OWNER_USER_ID}\"},\"project_id\":\"${PROJECT_ID}\",\"role\":\"developer\"}" \
    "${TMP_DIR}/sa-token.json")"
  [[ "${status}" == "201" ]] || fail "create resolve token returned HTTP ${status}: $(cat "${TMP_DIR}/sa-token.json")"
  SA_TOKEN="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["token"])' "${TMP_DIR}/sa-token.json")"
  [[ "${SA_TOKEN}" == forge_pat_* ]] || fail "resolve token missing forge_pat_ prefix"
  export FORGE_SECRETS_SERVICE_ACCOUNT="${SA_TOKEN}"
}

setup_secrets_and_bindings() {
  local service_slug="$1"
  echo "[foundations] set secrets/config + bindings for ${service_slug}"
  printf '%s' "${APP_SHARED_SECRET}" | forge --project "${PROJECT_ID}" --env "${ENV_NAME}" \
    secret set APP_SHARED_SECRET --from-stdin || fail "forge secret set APP_SHARED_SECRET failed"
  forge --project "${PROJECT_ID}" --env "${ENV_NAME}" \
    config set "PRODUCT_MODE=${PRODUCT_MODE}" || fail "forge config set PRODUCT_MODE failed"
  forge --project "${PROJECT_ID}" --env "${ENV_NAME}" \
    config set "FORGE_STORAGE_BUCKET=${STORAGE_BUCKET}" || fail "forge config set bucket failed"
  forge --project "${PROJECT_ID}" --env "${ENV_NAME}" \
    config set "FORGE_STORAGE_PROJECT=${PROJECT_ID}" || fail "forge config set storage project failed"
  forge --project "${PROJECT_ID}" --env "${ENV_NAME}" \
    config set "FORGE_PRODUCT_AUTH=enforce" || fail "forge config set product auth failed"
  forge --project "${PROJECT_ID}" --env "${ENV_NAME}" \
    config set "FORGE_PROJECT=${PROJECT_ID}" || fail "forge config set FORGE_PROJECT failed"
  forge --project "${PROJECT_ID}" --env "${ENV_NAME}" \
    config set "FORGE_OTEL_ENABLED=true" || fail "forge config set otel failed"
  forge --project "${PROJECT_ID}" --env "${ENV_NAME}" \
    config set "FORGE_OTEL_EXPORTER_ENDPOINT=http://host.docker.internal:4317" || true
  forge --project "${PROJECT_ID}" --env "${ENV_NAME}" \
    config set "FORGE_STORAGE_URL=http://host.docker.internal:4107" || true
  forge --project "${PROJECT_ID}" --env "${ENV_NAME}" \
    config set "FORGE_IDENTITY_URL=http://host.docker.internal:4002" || true

  local body status
  body="$(python3 - <<'PY'
import json
print(json.dumps({
  "secrets": ["APP_SHARED_SECRET"],
  "config": [
    "PRODUCT_MODE",
    "FORGE_STORAGE_BUCKET",
    "FORGE_STORAGE_PROJECT",
    "FORGE_PRODUCT_AUTH",
    "FORGE_PROJECT",
    "FORGE_OTEL_ENABLED",
    "FORGE_OTEL_EXPORTER_ENDPOINT",
    "FORGE_STORAGE_URL",
    "FORGE_IDENTITY_URL",
  ],
}))
PY
)"
  status="$(secrets_json PUT \
    "/v1/projects/${PROJECT_ID}/envs/${ENV_NAME}/services/${service_slug}/bindings" \
    "${SESSION_TOKEN}" "${body}" "${TMP_DIR}/bindings-${service_slug}.json")"
  [[ "${status}" == "200" ]] || fail "put bindings for ${service_slug} returned HTTP ${status}: $(cat "${TMP_DIR}/bindings-${service_slug}.json")"
}

setup_managed_db() {
  echo "[foundations] forge database create/attach ${DB_NAME}"
  forge_json "${TMP_DIR}/db-create.json" --project "${PROJECT_ID}" database create "${DB_NAME}"
  forge_json "${TMP_DIR}/db-attach.json" --project "${PROJECT_ID}" \
    database attach "${DB_NAME}" --app "${APPLICATION_NAME}" --env-var DATABASE_URL
  python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert d.get("secretRef") or d.get("secret_ref"), d' \
    "${TMP_DIR}/db-attach.json" || fail "attach response missing secretRef"
  echo "  DATABASE_URL attached via Secrets (no plaintext in attach response)"
}

setup_storage_bucket() {
  echo "[foundations] create Storage bucket ${STORAGE_BUCKET}"
  local status
  status="$(curl --silent --show-error --output "${TMP_DIR}/bucket.json" --write-out '%{http_code}' \
    -X POST "${STORAGE_URL}/v1/buckets" \
    -H "X-Forge-Project: ${PROJECT_ID}" \
    -H 'content-type: application/json' \
    -d "{\"name\":\"${STORAGE_BUCKET}\"}")" || fail "create bucket request failed"
  [[ "${status}" == "201" || "${status}" == "200" || "${status}" == "409" ]] ||
    fail "create bucket HTTP ${status}: $(cat "${TMP_DIR}/bucket.json")"
  echo "  bucket ready (HTTP ${status})"
}

assert_viewer_denied_deploy() {
  local service_id="$1"
  echo "[foundations] assert viewer cannot deploy"
  local body status
  body="$(python3 -c 'import json,sys; print(json.dumps({"image":"localhost:5000/forge/capstone-probe:deny","environmentId":sys.argv[1],"replicas":1}))' "${ENVIRONMENT_ID}")"
  status="$(control_json POST "/v1/services/${service_id}/deployments" "${VIEWER_TOKEN}" "${body}" \
    "${TMP_DIR}/deploy-viewer.json")"
  [[ "${status}" == "403" ]] || fail "viewer deploy expected 403, got ${status}: $(cat "${TMP_DIR}/deploy-viewer.json")"
  python3 -c 'import json,sys; b=json.load(open(sys.argv[1])); assert "forbidden" in json.dumps(b).lower(), b' \
    "${TMP_DIR}/deploy-viewer.json" || fail "viewer denial body missing forbidden"
  echo "  viewer deploy → 403 forbidden OK"
}

write_foundations_state() {
  cat >"${TMP_DIR}/foundations.env" <<EOF
PROJECT_ID=${PROJECT_ID}
ENVIRONMENT_ID=${ENVIRONMENT_ID}
APPLICATION_ID=${APPLICATION_ID}
ORG_ID=${ORG_ID}
OWNER_USER_ID=${OWNER_USER_ID}
SESSION_TOKEN=${SESSION_TOKEN}
DEV_TOKEN=${DEV_TOKEN}
VIEWER_TOKEN=${VIEWER_TOKEN}
SA_TOKEN=${SA_TOKEN}
APP_SHARED_SECRET=${APP_SHARED_SECRET}
PRODUCT_MODE=${PRODUCT_MODE}
STORAGE_BUCKET=${STORAGE_BUCKET}
DB_NAME=${DB_NAME}
EOF
  echo "[foundations] state written to ${TMP_DIR}/foundations.env"
}

# When executed directly (not sourced), run the full sequence expecting
# PROJECT services already created by the caller is NOT required — this script
# creates project/env/app. Service create + deploy remain in deploy.sh.
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  echo "setup-foundations.sh is designed to be sourced by deploy.sh" >&2
  echo "Source it after exporting required URLs and forge binary path." >&2
  exit 2
fi
