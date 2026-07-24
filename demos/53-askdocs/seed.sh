#!/usr/bin/env bash
# Idempotent seed for AskDocs (epic 53.01): welcome chat turn in the default session.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/53-askdocs"
STATE_FILE="${DEMO_DIR}/.demo-state"
GATEWAY_URL="${FORGE_GATEWAY_URL:-http://127.0.0.1:4000}"
API_HOST="${API_HOST:-api.askdocs.localhost}"
SESSION_ID="${ASKDOCS_SEED_SESSION:-default}"

fail() {
  echo "seed 53-askdocs failed: $*" >&2
  exit 1
}

read_state() {
  [[ -f "${STATE_FILE}" ]] || fail "missing ${STATE_FILE}; run demos/53-askdocs/run.sh first"
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

seed_sql() {
  cat <<SQL
INSERT INTO messages (id, session_id, role, text, citations)
VALUES
  ('msg-seed-user', '${SESSION_ID}', 'user', 'Hello AskDocs', '[]'::jsonb),
  ('msg-seed-assistant', '${SESSION_ID}', 'assistant', 'Welcome to AskDocs — upload a handbook, then ask a question.', '[]'::jsonb)
ON CONFLICT (id) DO UPDATE
SET session_id = EXCLUDED.session_id,
    role = EXCLUDED.role,
    text = EXCLUDED.text,
    citations = EXCLUDED.citations;
SQL
}

read_state
echo "seed 53-askdocs: resolving DATABASE_URL from API container..."
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

echo "seed 53-askdocs: applying idempotent SQL..."
run_sql "${URL}" "$(seed_sql)"

code="$(curl --silent --show-error -o /tmp/askdocs-seed-messages.json -w '%{http_code}' \
  -H "Host: ${API_HOST}" "${GATEWAY_URL}/messages?sessionId=${SESSION_ID}" || echo "000")"
[[ "${code}" == "200" ]] || fail "GET /messages returned HTTP ${code}"
SESSION_ID="${SESSION_ID}" python3 - <<'PY'
import json, os
body = json.load(open("/tmp/askdocs-seed-messages.json"))
msgs = body.get("messages") or []
ids = {m.get("id") for m in msgs}
need = {"msg-seed-user", "msg-seed-assistant"}
missing = need - ids
if missing:
    raise SystemExit(f"seeded messages missing from API list: {sorted(missing)}")
print(f"seed OK: session={os.environ['SESSION_ID']} {len(msgs)} message(s) visible via API")
PY
