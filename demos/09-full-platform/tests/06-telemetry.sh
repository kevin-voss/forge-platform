#!/usr/bin/env bash
# telemetry test
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/09-full-platform"
LIB_DIR="${DEMO_DIR}/lib"
PRODUCT_DIR="${DEMO_DIR}/product"

echo "Tempo service-name extraction helper..."
PYTHONPATH="${LIB_DIR}" python3 - <<'PY'
from foundations_helpers import tempo_service_names

payload = {
    "batches": [
        {
            "resource": {
                "attributes": [
                    {"key": "service.name", "value": {"stringValue": "incident-api"}},
                ]
            },
            "scopeSpans": [{"spans": [{}]}],
        },
        {
            "resource": {
                "attributes": [
                    {"key": "service.name", "value": {"stringValue": "incident-admin"}},
                ]
            },
            "scopeSpans": [{"spans": [{}]}],
        },
        {
            "resource": {
                "attributes": [
                    {"key": "service.name", "value": {"stringValue": "incident-classify"}},
                ]
            },
            "scopeSpans": [{"spans": [{}]}],
        },
    ]
}
names = tempo_service_names(payload)
assert len(names) >= 3, names
assert "incident-api" in names
print("telemetry helper ok")
PY

echo "Product services export OTEL (api / admin / classify)..."
grep -RInE 'OTEL|otel|trace' "${PRODUCT_DIR}/api-go" --include='*.go' >/dev/null
grep -RInE 'OTEL|otel|trace' "${PRODUCT_DIR}/admin-kotlin/src" --include='*.kt' >/dev/null
grep -RInE 'OTEL|otel|trace' "${PRODUCT_DIR}/classify-python" --include='*.py' >/dev/null

echo "Observe OpenAPI present..."
[[ -f "${ROOT_DIR}/contracts/openapi/forge-observe.openapi.yaml" ]]

echo "Agent diagnosis fixtures reference real telemetry tool names..."
for f in logs.search.json metrics.query.json deployment.read.json; do
  [[ -f "${DEMO_DIR}/ai/fixtures/${f}" ]] || {
    echo "missing AI fixture ${f}" >&2
    exit 1
  }
done

echo "telemetry checks ok"
