#!/usr/bin/env bash
# Capstone 19.04: seed historical incidents into Forge Memory (embedded via Models).
# Usage:
#   ./ai/seed-memory.sh
# Env:
#   FORGE_MEMORY_URL (default http://127.0.0.1:4303)
#   FORGE_MEMORY_PROJECT / FORGE_CAPSTONE_PROJECT (default capstone)
#   FORGE_MEMORY_PROJECT_B (optional isolation probe project; default capstone-b)
set -euo pipefail

AI_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FIXTURES="${AI_DIR}/fixtures/historical-incidents.json"
MEMORY_URL="${FORGE_MEMORY_URL:-http://127.0.0.1:4303}"
PROJECT="${FORGE_MEMORY_PROJECT:-${FORGE_CAPSTONE_PROJECT:-capstone}}"
PROJECT_B="${FORGE_MEMORY_PROJECT_B:-capstone-b}"

[[ -f "${FIXTURES}" ]] || {
  echo "seed-memory: missing fixtures ${FIXTURES}" >&2
  exit 1
}

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-capstone-seed.XXXXXX")"
trap 'rm -rf "${TMP_DIR}"' EXIT

http_body() {
  local out="$1" method="$2" url="$3"
  shift 3
  curl --silent --show-error --output "${out}" --write-out '%{http_code}' \
    --request "${method}" "${url}" "$@"
}

echo "Seeding Memory project=${PROJECT} via ${MEMORY_URL} ..."

# Wait for memory readiness (short).
ready=0
for _ in $(seq 1 60); do
  if curl --fail --silent --show-error "${MEMORY_URL}/health/ready" >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 1
done
[[ "${ready}" -eq 1 ]] || {
  echo "seed-memory: forge-memory not ready at ${MEMORY_URL}" >&2
  exit 1
}

create_payload="$(python3 - "${FIXTURES}" <<'PY'
import json, sys
fx = json.load(open(sys.argv[1]))
print(json.dumps({
    "name": fx["collection"],
    "dim": fx["dim"],
    "distance": fx.get("distance") or "cosine",
}))
PY
)"
status="$(http_body "${TMP_DIR}/create.json" POST "${MEMORY_URL}/v1/collections" \
  -H "X-Forge-Project: ${PROJECT}" \
  -H 'content-type: application/json' \
  -d "${create_payload}")"
[[ "${status}" == "201" || "${status}" == "200" ]] || {
  echo "seed-memory: create collection HTTP ${status}: $(cat "${TMP_DIR}/create.json")" >&2
  exit 1
}

upsert_payload="$(python3 - "${FIXTURES}" <<'PY'
import json, sys
fx = json.load(open(sys.argv[1]))
print(json.dumps({
    "model": fx["model"],
    "items": [
        {"id": i["id"], "text": i["text"], "metadata": i.get("metadata") or {}}
        for i in fx["incidents"]
    ],
}))
PY
)"
collection="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["collection"])' "${FIXTURES}")"
status="$(http_body "${TMP_DIR}/upsert.json" POST \
  "${MEMORY_URL}/v1/collections/${collection}/upsert" \
  -H "X-Forge-Project: ${PROJECT}" \
  -H 'content-type: application/json' \
  -d "${upsert_payload}")"
[[ "${status}" == "200" ]] || {
  echo "seed-memory: upsert HTTP ${status}: $(cat "${TMP_DIR}/upsert.json")" >&2
  exit 1
}

python3 - "${TMP_DIR}/upsert.json" "${FIXTURES}" <<'PY'
import json, sys
body = json.load(open(sys.argv[1]))
fx = json.load(open(sys.argv[2]))
want = len(fx["incidents"])
got = int(body.get("upserted") or 0)
assert got == want, (got, want, body)
print(f"upserted={got} collection={fx['collection']} project ok")
PY

# Optional: confirm project B cannot see the collection (isolation smoke).
if [[ "${FORGE_SEED_CHECK_ISOLATION:-1}" == "1" ]]; then
  status="$(http_body "${TMP_DIR}/iso.json" GET \
    "${MEMORY_URL}/v1/collections/${collection}" \
    -H "X-Forge-Project: ${PROJECT_B}")"
  [[ "${status}" == "404" ]] || {
    echo "seed-memory: isolation check expected 404 for ${PROJECT_B}, got ${status}" >&2
    exit 1
  }
  echo "isolation: project ${PROJECT_B} → 404 OK"
fi

echo "Memory seed complete (project=${PROJECT}, collection=${collection})."
