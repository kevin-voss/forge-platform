#!/usr/bin/env bash
# contract-only communication verification
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/09-full-platform"
PRODUCT_DIR="${DEMO_DIR}/product"

echo "Lint all platform OpenAPI documents..."
python3 "${ROOT_DIR}/scripts/lint-openapi.py"

echo "All event schemas parse..."
ROOT_DIR="${ROOT_DIR}" python3 - <<'PY'
import json, os
from pathlib import Path

events = Path(os.environ["ROOT_DIR"]) / "contracts" / "events"
files = sorted(events.glob("*.schema.json"))
assert files, "no event schemas"
for path in files:
    json.load(path.open())
print(f"validated {len(files)} event schemas")
PY

echo "Product services talk via documented contracts only (no docker DNS peer hacks)..."
if grep -RInE 'http://incident-(api|admin|log-worker|classify|notify)(:|/)' \
  "${PRODUCT_DIR}" \
  --include='*.go' --include='*.kt' --include='*.rs' --include='*.py' --include='*.ex' \
  >/dev/null 2>&1; then
  echo "product uses hard-coded peer service DNS; must use Gateway/Events env URLs" >&2
  exit 1
fi

grep -n 'FORGE_EVENTS_URL' "${PRODUCT_DIR}/api-go/config.go" >/dev/null
grep -n 'incident.created' "${PRODUCT_DIR}/api-go/events.go" >/dev/null
grep -n 'FORGE_EVENTS' "${PRODUCT_DIR}/log-worker-rust/src/config.rs" >/dev/null
grep -nE 'Storage|FORGE_STORAGE|storage' "${PRODUCT_DIR}/api-go/storage.go" >/dev/null

echo "Capstone folder naming reconfirmed..."
[[ -d "${ROOT_DIR}/demos/09-full-platform" ]]
[[ -d "${ROOT_DIR}/demos/09-platform-identity" ]]
[[ "${ROOT_DIR}/demos/09-full-platform" != "${ROOT_DIR}/demos/09-platform-identity" ]]

echo "contracts checks ok"
