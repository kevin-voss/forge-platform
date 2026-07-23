#!/usr/bin/env bash
# full deployment test (CI subset: forge.yaml + deploy helpers; full: state from deploy.sh)
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/09-full-platform"
PRODUCT_DIR="${DEMO_DIR}/product"
LIB_DIR="${DEMO_DIR}/lib"

echo "Deploy helper unit tests..."
(
  cd "${DEMO_DIR}"
  PYTHONPATH="${LIB_DIR}" python3 -m unittest discover -s lib -p 'test_deploy_helpers.py' -v
)

echo "Product forge.yaml + Dockerfile present for all five services..."
for svc in api-go admin-kotlin log-worker-rust classify-python notify-elixir; do
  [[ -f "${PRODUCT_DIR}/${svc}/forge.yaml" ]] || {
    echo "missing forge.yaml for ${svc}" >&2
    exit 1
  }
  [[ -f "${PRODUCT_DIR}/${svc}/Dockerfile" ]] || {
    echo "missing Dockerfile for ${svc}" >&2
    exit 1
  }
done

echo "Gateway hostnames documented..."
grep -q 'api.demo.localhost' "${DEMO_DIR}/routes.md"
grep -q 'notify.demo.localhost' "${DEMO_DIR}/routes.md"

if [[ "${CI_SUBSET:-true}" == "false" || "${CI_SUBSET:-true}" == "0" ]]; then
  echo "Full mode: requiring prior deploy state..."
  [[ -f "${DEMO_DIR}/.capstone-state" ]] || {
    echo "run ./start.sh with CI_SUBSET=false first" >&2
    exit 1
  }
  # shellcheck disable=SC1091
  source "${DEMO_DIR}/.capstone-state"
  [[ "${MODE:-}" == "full" ]] || {
    echo "full deploy state required (MODE=full)" >&2
    exit 1
  }
  curl --fail --silent --show-error \
    "${FORGE_GATEWAY_URL:-http://127.0.0.1:4000}/health/ready" >/dev/null
  curl --fail --silent --show-error \
    "${FORGE_CONTROL_URL:-http://127.0.0.1:4001}/health/ready" >/dev/null
fi

echo "deploy checks ok"
