#!/usr/bin/env bash
# Demo 01 (Go + Kotlin + Rust + Python): build/start contract apps and validate the runtime contract.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${DEMO_DIR}"

COMPOSE=(docker compose -f "${DEMO_DIR}/compose.yaml")
VALIDATOR="${ROOT_DIR}/tools/contract-validator/run.sh"
GO_CONTAINER="demo-go-api"
KT_CONTAINER="demo-kotlin-api"
RS_CONTAINER="demo-rust-api"
PY_CONTAINER="demo-python-api"
GO_URL="http://127.0.0.1:4201"
KT_URL="http://127.0.0.1:4202"
RS_URL="http://127.0.0.1:4203"
PY_URL="http://127.0.0.1:4204"
GO_LOG="$(mktemp "${TMPDIR:-/tmp}/demo-go-api-logs.XXXXXX.jsonl")"
KT_LOG="$(mktemp "${TMPDIR:-/tmp}/demo-kotlin-api-logs.XXXXXX.jsonl")"
RS_LOG="$(mktemp "${TMPDIR:-/tmp}/demo-rust-api-logs.XXXXXX.jsonl")"
PY_LOG="$(mktemp "${TMPDIR:-/tmp}/demo-python-api-logs.XXXXXX.jsonl")"

cleanup() {
  "${COMPOSE[@]}" down --remove-orphans >/dev/null 2>&1 || true
  rm -f "${GO_LOG}" "${KT_LOG}" "${RS_LOG}" "${PY_LOG}"
}
trap cleanup EXIT

wait_ready() {
  local name="$1" url="$2"
  echo "Waiting for readiness at ${url}/health/ready ..."
  local ready=0
  for _ in $(seq 1 90); do
    if curl -sf "${url}/health/ready" >/dev/null; then
      ready=1
      break
    fi
    sleep 1
  done
  if [[ "${ready}" -ne 1 ]]; then
    echo "Timed out waiting for ${name} to become ready" >&2
    "${COMPOSE[@]}" logs "${name}" >&2 || true
    exit 1
  fi
}

validate_service() {
  local name="$1" url="$2" language="$3" service="$4" log_file="$5"
  echo "Smoke checks (${name})..."
  curl -sf "${url}/health/live" >/dev/null
  curl -sf "${url}/health/ready" >/dev/null
  curl -sf "${url}/" | tee /dev/stderr | grep -q "\"language\":\"${language}\""

  echo "Capturing structured logs (${name})..."
  docker logs "${name}" >"${log_file}" 2>&1

  echo "Running contract validator for ${name}..."
  "${VALIDATOR}" \
    --base-url "${url}" \
    --expect-service "${service}" \
    --expect-language "${language}" \
    --log-file "${log_file}" \
    --shutdown-container "${name}" \
    --shutdown-timeout 10s
}

echo "== Demo 01: Container runtime (Go + Kotlin + Rust + Python) =="

chmod +x "${VALIDATOR}" "${ROOT_DIR}/tools/contract-validator/"*.py 2>/dev/null || true

echo "Building and starting ${GO_CONTAINER} (4201), ${KT_CONTAINER} (4202), ${RS_CONTAINER} (4203), ${PY_CONTAINER} (4204)..."
"${COMPOSE[@]}" up -d --build --force-recreate demo-go-api demo-kotlin-api demo-rust-api demo-python-api

wait_ready "${GO_CONTAINER}" "${GO_URL}"
wait_ready "${KT_CONTAINER}" "${KT_URL}"
wait_ready "${RS_CONTAINER}" "${RS_URL}"
wait_ready "${PY_CONTAINER}" "${PY_URL}"

validate_service "${GO_CONTAINER}" "${GO_URL}" "go" "demo-go-api" "${GO_LOG}"
validate_service "${KT_CONTAINER}" "${KT_URL}" "kotlin" "demo-kotlin-api" "${KT_LOG}"
validate_service "${RS_CONTAINER}" "${RS_URL}" "rust" "demo-rust-api" "${RS_LOG}"
validate_service "${PY_CONTAINER}" "${PY_URL}" "python" "demo-python-api" "${PY_LOG}"

echo
echo "Demo 01 (Go + Kotlin + Rust + Python) passed."
echo "  Go:     ${GO_URL}/ → demo-go-api / go         (4201:8080)"
echo "  Kotlin: ${KT_URL}/ → demo-kotlin-api / kotlin (4202:8080)"
echo "  Rust:   ${RS_URL}/ → demo-rust-api / rust     (4203:8080)"
echo "  Python: ${PY_URL}/ → demo-python-api / python (4204:8080)"
