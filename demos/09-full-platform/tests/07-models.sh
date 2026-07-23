#!/usr/bin/env bash
# model-serving test (uses live forge-models from start.sh)
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/09-full-platform"
MODELS_URL="${FORGE_MODELS_URL:-http://127.0.0.1:4300}"

echo "Models health..."
curl --fail --silent --show-error "${MODELS_URL}/health/ready" >/dev/null

echo "Deterministic embed via documented /v1/models/{model}/embed..."
body="$(mktemp "${TMPDIR:-/tmp}/capstone-embed.XXXXXX.json")"
code="$(curl --silent --show-error --output "${body}" --write-out '%{http_code}' \
  -X POST "${MODELS_URL}/v1/models/local-embed-small/embed" \
  -H 'content-type: application/json' \
  -d '{"input":"capstone readiness failure postgres pool exhausted"}')"
[[ "${code}" == "200" ]] || {
  echo "embed HTTP ${code}: $(cat "${body}")" >&2
  rm -f "${body}"
  exit 1
}
python3 - "${body}" <<'PY'
import json, sys
body = json.load(open(sys.argv[1]))
# Accept common shapes: {embedding:[...]} or {data:[{embedding:...}]}
emb = body.get("embedding")
if emb is None and isinstance(body.get("data"), list) and body["data"]:
    emb = body["data"][0].get("embedding")
if emb is None and isinstance(body.get("embeddings"), list) and body["embeddings"]:
    emb = body["embeddings"][0]
assert emb and len(emb) > 0, body
print("dims", len(emb))
PY
rm -f "${body}"

echo "Models OpenAPI present..."
[[ -f "${ROOT_DIR}/contracts/openapi/forge-models.openapi.yaml" ]]

echo "models checks ok"
