#!/usr/bin/env bash
# multi-language interoperability test
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/09-full-platform"
PRODUCT_DIR="${DEMO_DIR}/product"

echo "Five-language product unit tests..."

echo "  Go..."
( cd "${PRODUCT_DIR}/api-go" && go test ./... -count=1 )

echo "  Python..."
( cd "${PRODUCT_DIR}/classify-python" && python3 -m unittest discover -s . -p 'test_*.py' -v )

echo "  Kotlin..."
( cd "${PRODUCT_DIR}/admin-kotlin" && ./gradlew --no-daemon test -q )

echo "  Rust..."
( cd "${PRODUCT_DIR}/log-worker-rust" && cargo test --quiet )

echo "  Elixir..."
if command -v mix >/dev/null 2>&1; then
  ( cd "${PRODUCT_DIR}/notify-elixir" && mix test )
else
  # Host may not have Elixir; Dockerfile runs mix test during build.
  echo "  mix not on PATH — building notify-elixir image (runs MIX_ENV=test mix test)..."
  docker build -t forge/incident-notify:accept-test "${PRODUCT_DIR}/notify-elixir"
fi

echo "Runtime contract OpenAPI present..."
[[ -f "${ROOT_DIR}/contracts/openapi/runtime-contract.openapi.yaml" ]]

echo "Languages covered: go, kotlin, rust, python, elixir"
echo "interop checks ok"
