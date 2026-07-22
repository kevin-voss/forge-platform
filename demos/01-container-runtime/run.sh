#!/usr/bin/env bash
# Demo 01 (Go-only): build/start demo-go-api and validate the runtime contract.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${DEMO_DIR}"

COMPOSE=(docker compose -f "${DEMO_DIR}/compose.yaml")
CONTAINER="demo-go-api"
BASE_URL="http://127.0.0.1:4201"
LOG_FILE="$(mktemp "${TMPDIR:-/tmp}/demo-go-api-logs.XXXXXX.jsonl")"
VALIDATOR="${ROOT_DIR}/tools/contract-validator/run.sh"

cleanup() {
  "${COMPOSE[@]}" down --remove-orphans >/dev/null 2>&1 || true
  rm -f "${LOG_FILE}"
}
trap cleanup EXIT

echo "== Demo 01: Container runtime (Go) =="

chmod +x "${VALIDATOR}" "${ROOT_DIR}/tools/contract-validator/"*.py 2>/dev/null || true

echo "Building and starting ${CONTAINER} on host port 4201..."
"${COMPOSE[@]}" up -d --build --force-recreate demo-go-api

echo "Waiting for readiness at ${BASE_URL}/health/ready ..."
ready=0
for _ in $(seq 1 60); do
  if curl -sf "${BASE_URL}/health/ready" >/dev/null; then
    ready=1
    break
  fi
  sleep 1
done
if [[ "${ready}" -ne 1 ]]; then
  echo "Timed out waiting for ${CONTAINER} to become ready" >&2
  "${COMPOSE[@]}" logs demo-go-api >&2 || true
  exit 1
fi

echo "Smoke checks..."
curl -sf "${BASE_URL}/health/live" >/dev/null
curl -sf "${BASE_URL}/health/ready" >/dev/null
curl -sf "${BASE_URL}/" | tee /dev/stderr | grep -q '"language":"go"'

echo "Capturing structured logs..."
docker logs "${CONTAINER}" >"${LOG_FILE}" 2>&1

echo "Running contract validator (HTTP + logs + graceful shutdown)..."
"${VALIDATOR}" \
  --base-url "${BASE_URL}" \
  --expect-service demo-go-api \
  --expect-language go \
  --log-file "${LOG_FILE}" \
  --shutdown-container "${CONTAINER}" \
  --shutdown-timeout 10s

echo
echo "Demo 01 (Go) passed."
echo "  identity: ${BASE_URL}/ → demo-go-api / go"
echo "  host mapping: 4201:8080 (in-container PORT=8080)"
