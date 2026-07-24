#!/usr/bin/env bash
# Idempotent seed for TaskFlow (epic 51.02): admin + member, shared project, two open tasks.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/51-taskflow"
STATE_FILE="${DEMO_DIR}/.demo-state"
GATEWAY_URL="${FORGE_GATEWAY_URL:-http://127.0.0.1:4000}"
API_HOST="${API_HOST:-api.taskflow.localhost}"

fail() {
  echo "seed 51-taskflow failed: $*" >&2
  exit 1
}

read_state() {
  [[ -f "${STATE_FILE}" ]] || fail "missing ${STATE_FILE}; run demos/51-taskflow/run.sh first"
  # shellcheck disable=SC1090
  source "${STATE_FILE}"
  [[ -n "${API_DEPLOYMENT_ID:-}" ]] || fail "API_DEPLOYMENT_ID missing from .demo-state"
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
  # Distroless images have no printenv — read Config.Env via inspect.
  docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "${cid}" 2>/dev/null |
    awk -F= '$1=="DATABASE_URL" { print substr($0, index($0, "=")+1); exit }'
}

run_sql() {
  local url="$1"
  local sql="$2"
  # Prefer psql on the host; fall back to a one-shot postgres client container.
  if command -v psql >/dev/null 2>&1; then
    PGPASSWORD="" psql "${url}" -v ON_ERROR_STOP=1 -c "${sql}" >/dev/null
    return 0
  fi
  docker run --rm --network host postgres:16-alpine \
    psql "${url}" -v ON_ERROR_STOP=1 -c "${sql}" >/dev/null
}

seed_sql() {
  cat <<'SQL'
INSERT INTO users (id, email, password_hash, role)
VALUES
  ('user-admin', 'admin@taskflow.local', 'seed-placeholder-hash', 'admin'),
  ('user-member', 'member@taskflow.local', 'seed-placeholder-hash', 'member')
ON CONFLICT (email) DO UPDATE
SET role = EXCLUDED.role,
    password_hash = EXCLUDED.password_hash;

INSERT INTO projects (id, name, owner_id)
VALUES ('project-shared', 'Shared', 'user-admin')
ON CONFLICT (id) DO UPDATE
SET name = EXCLUDED.name,
    owner_id = EXCLUDED.owner_id;

INSERT INTO tasks (id, project_id, title, done)
VALUES
  ('task-seed-1', 'project-shared', 'Welcome to TaskFlow', false),
  ('task-seed-2', 'project-shared', 'Try completing a task', false)
ON CONFLICT (id) DO UPDATE
SET title = EXCLUDED.title,
    done = EXCLUDED.done,
    project_id = EXCLUDED.project_id,
    updated_at = NOW();
SQL
}

read_state
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

echo "seed 51-taskflow: applying idempotent SQL..."
run_sql "${URL}" "$(seed_sql)"

# Sanity: API lists at least the two seeded tasks (plus any others).
code="$(curl --silent --show-error -o /tmp/taskflow-seed-tasks.json -w '%{http_code}' \
  -H "Host: ${API_HOST}" "${GATEWAY_URL}/tasks" || echo "000")"
[[ "${code}" == "200" ]] || fail "GET /tasks returned HTTP ${code}"
python3 - <<'PY'
import json
tasks = json.load(open("/tmp/taskflow-seed-tasks.json"))
ids = {t.get("id") for t in tasks}
need = {"task-seed-1", "task-seed-2"}
missing = need - ids
if missing:
    raise SystemExit(f"seeded tasks missing from API list: {sorted(missing)}")
print(f"seed OK: {len(tasks)} task(s) visible via API (including seed rows)")
PY
