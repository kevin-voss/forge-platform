#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=/dev/null
source "${ROOT_DIR}/scripts/lib/ports.sh"

FAIL=0

check_http() {
  local name="$1"
  local url="$2"
  if curl -fsS --max-time 5 "${url}" >/dev/null 2>&1; then
    echo "OK  ${name}: ${url}"
  else
    echo "FAIL ${name}: ${url}" >&2
    FAIL=1
  fi
}

check_tcp() {
  local name="$1"
  local host="$2"
  local port="$3"
  if (echo >/dev/tcp/"${host}"/"${port}") >/dev/null 2>&1; then
    echo "OK  ${name}: ${host}:${port}"
  elif command -v nc >/dev/null 2>&1 && nc -z "${host}" "${port}" >/dev/null 2>&1; then
    echo "OK  ${name}: ${host}:${port}"
  else
    # Fallback: use bash /dev/tcp only; if unavailable, try curl for HTTP services.
    echo "FAIL ${name}: ${host}:${port}" >&2
    FAIL=1
  fi
}

echo "== Forge Platform infrastructure smoke test =="

check_tcp "PostgreSQL" "127.0.0.1" "${FORGE_PORT_POSTGRES}"
check_http "NATS monitoring" "http://127.0.0.1:${FORGE_PORT_NATS_MONITOR}/healthz"
check_http "OCI registry" "http://127.0.0.1:${FORGE_PORT_REGISTRY}/v2/"
check_http "OTEL collector health" "http://127.0.0.1:${FORGE_PORT_OTEL_HEALTH}/"
check_http "Prometheus" "http://127.0.0.1:${FORGE_PORT_PROMETHEUS}/-/healthy"
check_http "Tempo" "http://127.0.0.1:${FORGE_PORT_TEMPO}/ready"
check_http "Loki" "http://127.0.0.1:${FORGE_PORT_LOKI}/ready"
check_http "Grafana" "http://127.0.0.1:${FORGE_PORT_GRAFANA}/api/health"

if [[ "${FAIL}" -ne 0 ]]; then
  echo "Smoke test failed." >&2
  exit 1
fi

echo "All infrastructure checks passed."
