#!/usr/bin/env bash
# Product-only smoke: build/start five capstone services and validate runtime contract.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
PRODUCT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${PRODUCT_DIR}"

COMPOSE=(docker compose -f "${PRODUCT_DIR}/compose.yaml")
VALIDATOR="${ROOT_DIR}/tools/contract-validator/run.sh"

SERVICES=(
  "incident-api|api-go|http://127.0.0.1:4211|go|incident-api"
  "incident-admin|admin-kotlin|http://127.0.0.1:4212|kotlin|incident-admin"
  "incident-log-worker|log-worker-rust|http://127.0.0.1:4213|rust|incident-log-worker"
  "incident-classify|classify-python|http://127.0.0.1:4214|python|incident-classify"
  "incident-notify|notify-elixir|http://127.0.0.1:4215|elixir|incident-notify"
)

LOG_FILES=()

cleanup() {
  "${COMPOSE[@]}" down --remove-orphans >/dev/null 2>&1 || true
  for f in "${LOG_FILES[@]:-}"; do
    rm -f "${f}"
  done
}
trap cleanup EXIT

wait_ready() {
  local name="$1" url="$2"
  echo "Waiting for readiness at ${url}/health/ready ..."
  local ready=0
  for _ in $(seq 1 180); do
    if curl -sf "${url}/health/ready" >/dev/null; then
      ready=1
      break
    fi
    sleep 1
  done
  if [[ "${ready}" -ne 1 ]]; then
    echo "Timed out waiting for ${name} to become ready" >&2
    "${COMPOSE[@]}" logs >&2 || true
    exit 1
  fi
}

validate_service() {
  local container="$1" compose_svc="$2" url="$3" language="$4" service="$5"
  local log_file
  log_file="$(mktemp "${TMPDIR:-/tmp}/${container}-logs.XXXXXX.jsonl")"
  LOG_FILES+=("${log_file}")

  echo "Smoke checks (${compose_svc})..."
  curl -sf "${url}/health/live" >/dev/null
  curl -sf "${url}/health/ready" >/dev/null
  curl -sf "${url}/" | tee /dev/stderr | grep -q "\"language\":\"${language}\""

  echo "Capturing structured logs (${compose_svc})..."
  docker logs "${container}" >"${log_file}" 2>&1

  echo "Running contract validator for ${compose_svc}..."
  "${VALIDATOR}" \
    --base-url "${url}" \
    --expect-service "${service}" \
    --expect-language "${language}" \
    --log-file "${log_file}" \
    --shutdown-container "${container}" \
    --shutdown-timeout 10s
}

echo "== Capstone product (19.01): five polyglot services =="

chmod +x "${VALIDATOR}" "${ROOT_DIR}/tools/contract-validator/"*.py 2>/dev/null || true

echo "Building and starting product services (4211–4215)..."
"${COMPOSE[@]}" up -d --build --force-recreate \
  api-go admin-kotlin log-worker-rust classify-python notify-elixir

for entry in "${SERVICES[@]}"; do
  IFS='|' read -r container compose_svc url language service <<<"${entry}"
  wait_ready "${container}" "${url}"
done

for entry in "${SERVICES[@]}"; do
  IFS='|' read -r container compose_svc url language service <<<"${entry}"
  validate_service "${container}" "${compose_svc}" "${url}" "${language}" "${service}"
done

echo
echo "Product scaffold smoke passed."
echo "  api-go:           http://127.0.0.1:4211  (incident-api / go)"
echo "  admin-kotlin:     http://127.0.0.1:4212  (incident-admin / kotlin)"
echo "  log-worker-rust:  http://127.0.0.1:4213  (incident-log-worker / rust)"
echo "  classify-python:  http://127.0.0.1:4214  (incident-classify / python)"
echo "  notify-elixir:    http://127.0.0.1:4215  (incident-notify / elixir)"
