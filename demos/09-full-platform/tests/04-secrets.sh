#!/usr/bin/env bash
# secret-injection test
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/09-full-platform"
LIB_DIR="${DEMO_DIR}/lib"
PRODUCT_DIR="${DEMO_DIR}/product"

echo "Secret masking + status helpers..."
PYTHONPATH="${LIB_DIR}" python3 - <<'PY'
from foundations_helpers import mask_secrets_in_text, secret_status_ok, assert_no_plaintext

masked = mask_secrets_in_text(
    "DATABASE_URL=postgresql://u:p@h/db APP_SHARED_SECRET=supersecret",
    ["supersecret"],
)
assert "postgresql://" not in masked
assert "supersecret" not in masked
assert secret_status_ok(
    {"APP_SHARED_SECRET_present": True, "PRODUCT_MODE_present": True, "length": 12}
)
assert_no_plaintext('{"ok":true}', ["supersecret"])
print("masking ok")
PY

echo "Product reports presence only (never echoes secrets)..."
grep -n 'DATABASE_URL_present\|APP_SHARED_SECRET_present\|secret-status\|db-status' \
  "${PRODUCT_DIR}/api-go/server.go" >/dev/null

echo "No checked-in Secrets master key in compose overlay..."
if grep -nE 'FORGE_SECRETS_MASTER_KEY:[[:space:]]*["'"'"'][A-Za-z0-9+/=]{20,}' \
  "${DEMO_DIR}/compose.yaml" >/dev/null 2>&1; then
  echo "checked-in secrets master key detected" >&2
  exit 1
fi

echo "Secrets OpenAPI present..."
[[ -f "${ROOT_DIR}/contracts/openapi/forge-secrets.openapi.yaml" ]]

echo "secrets checks ok"
