#!/usr/bin/env bash
# complete platform smoke test
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/09-full-platform"
LIB_DIR="${DEMO_DIR}/lib"
PRODUCT_DIR="${DEMO_DIR}/product"

echo "Helper unit tests (deploy/foundations/ai/scenario)..."
(
  cd "${DEMO_DIR}"
  PYTHONPATH="${LIB_DIR}" python3 -m unittest discover -s lib -p 'test_*.py' -v
)

echo "CAPSTONE_BREAK readiness smoke (api-go)..."
(
  cd "${PRODUCT_DIR}/api-go"
  go test ./... -count=1 -run 'TestCapstoneBreak|TestHealth'
)

echo "Capstone scripts exist and parse..."
for f in start.sh accept.sh deploy.sh setup-foundations.sh \
  ai/verify-diagnosis.sh ai/seed-memory.sh scenario/break-release.sh; do
  [[ -f "${DEMO_DIR}/${f}" ]] || { echo "missing ${f}" >&2; exit 1; }
  bash -n "${DEMO_DIR}/${f}"
done

# Distinct from Identity epic demo.
[[ -d "${ROOT_DIR}/demos/09-platform-identity" ]] || {
  echo "expected demos/09-platform-identity to remain present" >&2
  exit 1
}
[[ -f "${ROOT_DIR}/demos/09-platform-identity/run.sh" ]]
[[ "${DEMO_DIR}" == */demos/09-full-platform ]]

echo "smoke ok"
