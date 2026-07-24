#!/usr/bin/env bash
# Idempotent seed for SnapNote (epic 52.01): two starter notes.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/52-snapnote"
STATE_FILE="${DEMO_DIR}/.demo-state"
GATEWAY_URL="${FORGE_GATEWAY_URL:-http://127.0.0.1:4000}"
API_HOST="${API_HOST:-api.snapnote.localhost}"

fail() {
  echo "seed 52-snapnote failed: $*" >&2
  exit 1
}

read_state() {
  [[ -f "${STATE_FILE}" ]] || fail "missing ${STATE_FILE}; run demos/52-snapnote/run.sh first"
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
INSERT INTO notes (id, title, body)
VALUES
  ('note-seed-1', 'Welcome to SnapNote', 'Create notes now; attachments land in 52.02.'),
  ('note-seed-2', 'Trip photos', 'Placeholder for async thumbnail processing.')
ON CONFLICT (id) DO UPDATE
SET title = EXCLUDED.title,
    body = EXCLUDED.body,
    updated_at = NOW();
SQL
}

read_state
echo "seed 52-snapnote: resolving DATABASE_URL from API container..."
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

echo "seed 52-snapnote: applying idempotent SQL..."
run_sql "${URL}" "$(seed_sql)"

# Sanity: API lists at least the two seeded notes.
code="$(curl --silent --show-error -o /tmp/snapnote-seed-notes.json -w '%{http_code}' \
  -H "Host: ${API_HOST}" "${GATEWAY_URL}/notes" || echo "000")"
[[ "${code}" == "200" ]] || fail "GET /notes returned HTTP ${code}"
python3 - <<'PY'
import json
notes = json.load(open("/tmp/snapnote-seed-notes.json"))
ids = {n.get("id") for n in notes}
need = {"note-seed-1", "note-seed-2"}
missing = need - ids
if missing:
    raise SystemExit(f"seeded notes missing from API list: {sorted(missing)}")
print(f"seed OK: {len(notes)} note(s) visible via API (including seed rows)")
PY
