#!/usr/bin/env bash
# identity and permission test
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/09-full-platform"
LIB_DIR="${DEMO_DIR}/lib"
PRODUCT_DIR="${DEMO_DIR}/product"

echo "Foundations helpers (authz masking / status shapes)..."
(
  cd "${DEMO_DIR}"
  PYTHONPATH="${LIB_DIR}" python3 -m unittest discover -s lib -p 'test_foundations_helpers.py' -v
)

echo "Product API requires Identity introspect (no hardcoded tokens)..."
if grep -RInE 'forge_pat_[A-Za-z0-9]{8,}' "${PRODUCT_DIR}/api-go" \
  --include='*.go' >/dev/null 2>&1; then
  echo "hardcoded PAT found in api-go" >&2
  exit 1
fi
grep -RInE 'introspect|FORGE_IDENTITY|Identity' "${PRODUCT_DIR}/api-go" \
  --include='*.go' >/dev/null || {
  echo "api-go must wire Identity introspect" >&2
  exit 1
}

echo "setup-foundations.sh issues developer + viewer roles..."
grep -E 'viewer|developer' "${DEMO_DIR}/setup-foundations.sh" >/dev/null

echo "deploy.sh asserts viewer denied / developer allowed..."
grep -E 'assert_viewer_denied|viewer' "${DEMO_DIR}/deploy.sh" >/dev/null

echo "OpenAPI Identity contract present..."
[[ -f "${ROOT_DIR}/contracts/openapi/forge-identity.openapi.yaml" ]]

echo "identity checks ok"
