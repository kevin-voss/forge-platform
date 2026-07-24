#!/usr/bin/env bash
# Idempotent seed for TaskFlow (epic 51.03): Identity users + local admin/member,
# shared project, two open tasks, identity_project_id setting.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/51-taskflow"
STATE_FILE="${DEMO_DIR}/.demo-state"
GATEWAY_URL="${FORGE_GATEWAY_URL:-http://127.0.0.1:4000}"
IDENTITY_URL="${FORGE_IDENTITY_HOST_URL:-http://127.0.0.1:4002}"
API_HOST="${API_HOST:-api.taskflow.localhost}"
ADMIN_EMAIL="${TASKFLOW_ADMIN_EMAIL:-admin@taskflow.local}"
ADMIN_PASSWORD="${TASKFLOW_ADMIN_PASSWORD:-AdminPass123!}"
MEMBER_EMAIL="${TASKFLOW_MEMBER_EMAIL:-member@taskflow.local}"
MEMBER_PASSWORD="${TASKFLOW_MEMBER_PASSWORD:-MemberPass123!}"

fail() {
  echo "seed 51-taskflow failed: $*" >&2
  exit 1
}

read_state() {
  [[ -f "${STATE_FILE}" ]] || fail "missing ${STATE_FILE}; run demos/51-taskflow/run.sh first"
  # shellcheck disable=SC1090
  source "${STATE_FILE}"
  [[ -n "${API_DEPLOYMENT_ID:-}" ]] || fail "API_DEPLOYMENT_ID missing from .demo-state"
  [[ -n "${PROJECT_ID:-}" ]] || fail "PROJECT_ID missing from .demo-state"
}

api_container() {
  local cid short
  cid="$(docker ps -q \
    --filter "label=forge.deployment_id=${API_DEPLOYMENT_ID}" \
    --filter "label=forge.managed=true" | head -n1)"
  if [[ -n "${cid}" ]]; then
    echo "${cid}"
    return 0
  fi
  short="$(python3 -c 'import sys; print(sys.argv[1].replace("-", "")[:8])' "${API_DEPLOYMENT_ID}")"
  docker ps -q --filter "label=forge.managed=true" --filter "name=forge-api-${short}-" | head -n1
}

database_url_from_container() {
  local cid="$1"
  docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "${cid}" 2>/dev/null |
    awk -F= '$1=="DATABASE_URL" { print substr($0, index($0, "=")+1); exit }'
}

run_sql() {
  local url="$1"
  local sql="$2"
  if command -v psql >/dev/null 2>&1; then
    PGPASSWORD="" psql "${url}" -v ON_ERROR_STOP=1 -c "${sql}" >/dev/null
    return 0
  fi
  docker run --rm --network host postgres:16-alpine \
    psql "${url}" -v ON_ERROR_STOP=1 -c "${sql}" >/dev/null
}

identity_json() {
  local method="$1" path="$2" body="${3:-}" output="$4"
  local status
  if [[ -n "${body}" ]]; then
    status="$(curl --silent --show-error --output "${output}" --write-out '%{http_code}' \
      --request "${method}" "${IDENTITY_URL}${path}" \
      --header 'content-type: application/json' \
      --data "${body}")" || fail "Identity ${method} ${path} failed"
  else
    status="$(curl --silent --show-error --output "${output}" --write-out '%{http_code}' \
      --request "${method}" "${IDENTITY_URL}${path}")" || fail "Identity ${method} ${path} failed"
  fi
  echo "${status}"
}

ensure_identity_user() {
  local email="$1" password="$2" display="$3" out="$4"
  local status
  status="$(identity_json POST /v1/auth/register \
    "{\"email\":\"${email}\",\"password\":\"${password}\",\"display_name\":\"${display}\"}" \
    "${out}")"
  if [[ "${status}" == "201" ]]; then
    python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["user_id"])' "${out}"
    return 0
  fi
  # Already registered — login + introspect for user id.
  status="$(identity_json POST /v1/auth/login \
    "{\"email\":\"${email}\",\"password\":\"${password}\"}" \
    "${out}.login")"
  [[ "${status}" == "200" ]] || fail "login ${email} returned HTTP ${status}: $(cat "${out}.login")"
  local session
  session="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["session_token"])' "${out}.login")"
  status="$(identity_json POST /v1/auth/introspect \
    "{\"token\":\"${session}\"}" \
    "${out}.intro")"
  [[ "${status}" == "200" ]] || fail "introspect ${email} returned HTTP ${status}"
  python3 -c 'import json,sys; b=json.load(open(sys.argv[1])); assert b.get("active"), b; print(b.get("user_id") or b.get("principal_id"))' \
    "${out}.intro"
}

gateway_json() {
  local method="$1" path="$2" token="${3:-}" body="${4:-}" output="$5"
  local -a args=(
    --silent --show-error --output "${output}" --write-out '%{http_code}'
    --request "${method}" -H "Host: ${API_HOST}" "${GATEWAY_URL}${path}"
  )
  if [[ -n "${token}" ]]; then
    args+=(-H "Authorization: Bearer ${token}")
  fi
  if [[ -n "${body}" ]]; then
    args+=(-H 'content-type: application/json' --data "${body}")
  fi
  curl "${args[@]}" || echo "000"
}

read_state
TMP="$(mktemp -d "${TMPDIR:-/tmp}/taskflow-seed.XXXXXX")"
trap 'rm -rf "${TMP}"' EXIT

echo "seed 51-taskflow: waiting for Identity at ${IDENTITY_URL} ..."
for _ in $(seq 1 60); do
  if curl --fail --silent --show-error "${IDENTITY_URL}/health/ready" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl --fail --silent --show-error "${IDENTITY_URL}/health/ready" >/dev/null ||
  fail "Identity not ready at ${IDENTITY_URL}"

echo "seed 51-taskflow: ensuring Identity users..."
ADMIN_ID="$(ensure_identity_user "${ADMIN_EMAIL}" "${ADMIN_PASSWORD}" "TaskFlow Admin" "${TMP}/admin.json")"
MEMBER_ID="$(ensure_identity_user "${MEMBER_EMAIL}" "${MEMBER_PASSWORD}" "TaskFlow Member" "${TMP}/member.json")"
[[ -n "${ADMIN_ID}" && -n "${MEMBER_ID}" ]] || fail "missing identity user ids"

# Ensure users are project members so login can mint PATs.
status="$(identity_json POST "/v1/projects/${PROJECT_ID}/members" \
  "{\"user_id\":\"${ADMIN_ID}\",\"role\":\"developer\"}" \
  "${TMP}/admin-member.json")"
[[ "${status}" == "201" || "${status}" == "200" || "${status}" == "409" ]] ||
  fail "add admin member HTTP ${status}: $(cat "${TMP}/admin-member.json")"
status="$(identity_json POST "/v1/projects/${PROJECT_ID}/members" \
  "{\"user_id\":\"${MEMBER_ID}\",\"role\":\"developer\"}" \
  "${TMP}/member-member.json")"
[[ "${status}" == "201" || "${status}" == "200" || "${status}" == "409" ]] ||
  fail "add member member HTTP ${status}: $(cat "${TMP}/member-member.json")"

echo "seed 51-taskflow: resolving DATABASE_URL from API container..."
CID="$(api_container)"
[[ -n "${CID}" ]] || fail "no running API container for deployment ${API_DEPLOYMENT_ID}"

URL=""
for _ in $(seq 1 60); do
  URL="$(database_url_from_container "${CID}")"
  if [[ -n "${URL}" ]]; then
    break
  fi
  sleep 1
  CID="$(api_container)"
  [[ -n "${CID}" ]] || fail "API container disappeared while waiting for DATABASE_URL"
done
[[ -n "${URL}" ]] || fail "DATABASE_URL not present on API container (managed DB attach missing?)"

echo "seed 51-taskflow: applying idempotent SQL (roles + project + tasks)..."
run_sql "${URL}" "
INSERT INTO app_settings (key, value)
VALUES ('identity_project_id', '${PROJECT_ID}')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

DELETE FROM tasks WHERE id IN ('task-seed-1', 'task-seed-2');
DELETE FROM projects WHERE id = 'project-shared';
DELETE FROM users WHERE email IN ('${ADMIN_EMAIL}', '${MEMBER_EMAIL}');

INSERT INTO users (id, email, password_hash, role)
VALUES
  ('${ADMIN_ID}', '${ADMIN_EMAIL}', 'identity-managed', 'admin'),
  ('${MEMBER_ID}', '${MEMBER_EMAIL}', 'identity-managed', 'member');

INSERT INTO projects (id, name, owner_id)
VALUES ('project-shared', 'Shared', '${ADMIN_ID}');

INSERT INTO tasks (id, project_id, title, done)
VALUES
  ('task-seed-1', 'project-shared', 'Welcome to TaskFlow', false),
  ('task-seed-2', 'project-shared', 'Try completing a task', false);
"

echo "seed 51-taskflow: login as admin via product API..."
code="$(gateway_json POST /auth/login "" \
  "{\"email\":\"${ADMIN_EMAIL}\",\"password\":\"${ADMIN_PASSWORD}\"}" \
  "${TMP}/login.json")"
[[ "${code}" == "200" ]] || fail "POST /auth/login HTTP ${code}: $(cat "${TMP}/login.json")"
ADMIN_TOKEN="$(python3 -c 'import json,sys; b=json.load(open(sys.argv[1])); print(b.get("token") or b.get("pat") or "")' "${TMP}/login.json")"
[[ "${ADMIN_TOKEN}" == forge_pat_* || "${ADMIN_TOKEN}" == *.* ]] ||
  fail "login missing token: $(cat "${TMP}/login.json")"

code="$(gateway_json GET /tasks "${ADMIN_TOKEN}" "" "${TMP}/tasks.json")"
[[ "${code}" == "200" ]] || fail "GET /tasks returned HTTP ${code}: $(cat "${TMP}/tasks.json")"
python3 - <<'PY' "${TMP}/tasks.json"
import json, sys
tasks = json.load(open(sys.argv[1]))
ids = {t.get("id") for t in tasks}
need = {"task-seed-1", "task-seed-2"}
missing = need - ids
if missing:
    raise SystemExit(f"seeded tasks missing from API list: {sorted(missing)}")
print(f"seed OK: {len(tasks)} task(s) visible via authenticated API (including seed rows)")
PY

# Role gate smoke: member cannot delete project; admin can (re-seed restores project).
code="$(gateway_json POST /auth/login "" \
  "{\"email\":\"${MEMBER_EMAIL}\",\"password\":\"${MEMBER_PASSWORD}\"}" \
  "${TMP}/member-login.json")"
[[ "${code}" == "200" ]] || fail "member login HTTP ${code}: $(cat "${TMP}/member-login.json")"
MEMBER_TOKEN="$(python3 -c 'import json,sys; b=json.load(open(sys.argv[1])); print(b.get("token") or b.get("pat") or "")' "${TMP}/member-login.json")"
code="$(gateway_json DELETE /projects/project-shared "${MEMBER_TOKEN}" "" "${TMP}/member-del.json")"
[[ "${code}" == "403" ]] || fail "member delete expected 403, got ${code}: $(cat "${TMP}/member-del.json")"
echo "seed OK: member delete project → 403"

code="$(gateway_json DELETE /projects/project-shared "${ADMIN_TOKEN}" "" "${TMP}/admin-del.json")"
[[ "${code}" == "204" ]] || fail "admin delete expected 204, got ${code}: $(cat "${TMP}/admin-del.json")"
echo "seed OK: admin delete project → 204"

# Restore shared project + seed tasks after admin delete proof.
run_sql "${URL}" "
INSERT INTO projects (id, name, owner_id)
VALUES ('project-shared', 'Shared', '${ADMIN_ID}')
ON CONFLICT (id) DO UPDATE SET owner_id = EXCLUDED.owner_id, name = EXCLUDED.name;
INSERT INTO tasks (id, project_id, title, done)
VALUES
  ('task-seed-1', 'project-shared', 'Welcome to TaskFlow', false),
  ('task-seed-2', 'project-shared', 'Try completing a task', false)
ON CONFLICT (id) DO UPDATE
SET title = EXCLUDED.title, done = EXCLUDED.done, project_id = EXCLUDED.project_id, updated_at = NOW();
"
echo "seed 51-taskflow complete (Identity auth + role gating)"
