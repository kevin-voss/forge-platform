#!/usr/bin/env bash
# Demo 13: object-storage gate (epic 13 acceptance).
# Scenario: create bucket → stream 50 MiB upload/download → checksum → range
#           → expired token → delete → restart durability.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/13-object-storage"
COMPOSE=(
  docker compose
    -f "${DEMO_DIR}/compose.yaml"
    --project-directory "${DEMO_DIR}"
)

# Demo-only placeholders (see .env.example). Not real credentials.
export FORGE_AUTH_MODE="${FORGE_AUTH_MODE:-dev}"
export FORGE_STORAGE_SIGNING_KEY="${FORGE_STORAGE_SIGNING_KEY:-demo-13-signing-key-not-a-secret}"
export FORGE_STORAGE_CLOCK_SKEW_SECONDS="${FORGE_STORAGE_CLOCK_SKEW_SECONDS:-0}"
export FORGE_STORAGE_MAX_TTL_SECONDS="${FORGE_STORAGE_MAX_TTL_SECONDS:-3600}"
export FORGE_STORAGE_DEFAULT_QUOTA_BYTES="${FORGE_STORAGE_DEFAULT_QUOTA_BYTES:-1073741824}"
export FORGE_STORAGE_URL="${FORGE_STORAGE_URL:-http://127.0.0.1:4107}"
export FORGE_STORAGE_PROJECT="${FORGE_STORAGE_PROJECT:-demo-13}"
export FORGE_LOG_LEVEL="${FORGE_LOG_LEVEL:-info}"

STORAGE_SERVICE="forge-storage"
PHASE="${1:-all}"
STARTED=0

cleanup() {
  if [[ "${STARTED}" -eq 1 ]]; then
    "${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

dump_context() {
  echo "--- ${STORAGE_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=120 "${STORAGE_SERVICE}" >&2 || true
}

fail() {
  echo "Demo 13 failed: $*" >&2
  dump_context
  exit 1
}

wait_http() {
  local url="$1" label="$2" attempts="${3:-90}"
  local ready=0
  echo "Waiting for ${label} at ${url} ..."
  for _ in $(seq 1 "${attempts}"); do
    if curl --fail --silent --show-error "${url}" >/dev/null 2>&1; then
      ready=1
      break
    fi
    sleep 1
  done
  [[ "${ready}" -eq 1 ]] || fail "timed out waiting for ${label}"
}

free_host_port() {
  # Root compose may already bind 4107 as container `forge-storage`.
  local cid
  cid="$(docker ps -q --filter "publish=4107" 2>/dev/null || true)"
  if [[ -n "${cid}" ]]; then
    echo "Stopping container(s) publishing host port 4107 for a clean demo bind..."
    # shellcheck disable=SC2086
    docker stop ${cid} >/dev/null 2>&1 || true
  fi
}

step_bootstrap() {
  echo "== Demo 13: Object storage =="
  echo "Auth mode: FORGE_AUTH_MODE=${FORGE_AUTH_MODE} (dev header X-Forge-Project)"
  echo "Signing key: demo placeholder (FORGE_STORAGE_SIGNING_KEY set)"
  echo "Clock skew: ${FORGE_STORAGE_CLOCK_SKEW_SECONDS}s (0 so ttl=1s expiry is observable)"
  echo "Storage URL: ${FORGE_STORAGE_URL}"

  chmod +x "${DEMO_DIR}/acceptance.sh" "${DEMO_DIR}/run.sh"

  free_host_port

  echo "Bringing up forge-storage with a fresh volume..."
  "${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
  "${COMPOSE[@]}" up -d --build --force-recreate --remove-orphans \
    "${STORAGE_SERVICE}" runner
  STARTED=1

  wait_http "${FORGE_STORAGE_URL}/health/ready" "forge-storage"
  curl --fail --silent --show-error "${FORGE_STORAGE_URL}/" >/dev/null ||
    fail "identity endpoint failed"
  echo "  forge-storage ready"
}

step_acceptance() {
  echo "Running acceptance assertions..."
  bash "${DEMO_DIR}/acceptance.sh" || fail "acceptance.sh failed"
}

run_scenario() {
  step_bootstrap
  step_acceptance
  echo "demo 13 PASSED"
}

case "${PHASE}" in
  all|--phase=all|"")
    run_scenario
    ;;
  --phase=up)
    step_bootstrap
    echo "phase up PASSED (stack left running until EXIT trap)"
    # Keep stack up for interactive debugging: disable teardown.
    trap - EXIT
    STARTED=0
    ;;
  *)
    echo "Unknown phase: ${PHASE}" >&2
    echo "Usage: $0 [all|--phase=up]" >&2
    exit 2
    ;;
esac
