#!/usr/bin/env bash
# Demo 15: agent-runtime gate (epic 15 acceptance).
# Scenario: failing-deployment fixtures → deployment-investigator
#           diagnoses via registered tools → requests restart →
#           awaiting_approval (not executed) → hallucinated tool rejected.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/15-agent-runtime"
COMPOSE=(
  docker compose
  -f "${DEMO_DIR}/compose.yaml"
  --project-directory "${DEMO_DIR}"
)

export FORGE_MODELS_BACKEND="${FORGE_MODELS_BACKEND:-fake}"
export FORGE_AGENTS_TOOLS_MODE="${FORGE_AGENTS_TOOLS_MODE:-fake}"
export FORGE_MODELS_URL="${FORGE_MODELS_URL:-http://127.0.0.1:4300}"
export FORGE_AGENTS_URL="${FORGE_AGENTS_URL:-http://127.0.0.1:4301}"
export FORGE_AGENTS_PROJECT="${FORGE_AGENTS_PROJECT:-demo-15}"
export FORGE_AGENTS_DEPLOYMENT="${FORGE_AGENTS_DEPLOYMENT:-dep-failing}"
export FORGE_LOG_LEVEL="${FORGE_LOG_LEVEL:-info}"

AGENTS_SERVICE="forge-agents"
MODELS_SERVICE="forge-models"
PHASE="${1:-all}"
STARTED=0

cleanup() {
  if [[ "${STARTED}" -eq 1 ]]; then
    "${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

dump_context() {
  echo "--- ${AGENTS_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=160 "${AGENTS_SERVICE}" >&2 || true
  echo "--- ${MODELS_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=80 "${MODELS_SERVICE}" >&2 || true
  echo "--- recent runs ---" >&2
  curl --silent --show-error \
    -H "X-Forge-Project: ${FORGE_AGENTS_PROJECT}" \
    "${FORGE_AGENTS_URL}/v1/runs" >&2 || true
  echo >&2
}

fail() {
  echo "Demo 15 failed: $*" >&2
  dump_context
  exit 1
}

wait_http() {
  local url="$1" label="$2" attempts="${3:-120}"
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

free_host_ports() {
  local port cid
  for port in 4300 4301; do
    cid="$(docker ps -q --filter "publish=${port}" 2>/dev/null || true)"
    if [[ -n "${cid}" ]]; then
      echo "Stopping container(s) publishing host port ${port} for a clean demo bind..."
      # shellcheck disable=SC2086
      docker stop ${cid} >/dev/null 2>&1 || true
    fi
  done
}

step_bootstrap() {
  echo "== Demo 15: Agent runtime =="
  echo "Tools mode: FORGE_AGENTS_TOOLS_MODE=${FORGE_AGENTS_TOOLS_MODE}"
  echo "Models backend: FORGE_MODELS_BACKEND=${FORGE_MODELS_BACKEND}"
  echo "Agents URL: ${FORGE_AGENTS_URL}"
  echo "Models URL: ${FORGE_MODELS_URL}"
  echo "Project: ${FORGE_AGENTS_PROJECT}"
  echo "Deployment fixture: ${FORGE_AGENTS_DEPLOYMENT}"

  chmod +x "${DEMO_DIR}/acceptance.sh" "${DEMO_DIR}/run.sh"
  free_host_ports

  echo "Bringing up forge-models (fake) + forge-agents (fake tools + failing fixtures)..."
  "${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
  "${COMPOSE[@]}" up -d --build --force-recreate --remove-orphans \
    "${MODELS_SERVICE}" "${AGENTS_SERVICE}"
  STARTED=1

  wait_http "${FORGE_MODELS_URL}/health/ready" "forge-models"
  wait_http "${FORGE_AGENTS_URL}/health/ready" "forge-agents"
  curl --fail --silent --show-error "${FORGE_AGENTS_URL}/" >/dev/null ||
    fail "agents identity endpoint failed"
  echo "  forge-models + forge-agents ready"
}

step_acceptance() {
  echo "Running acceptance assertions..."
  bash "${DEMO_DIR}/acceptance.sh" || fail "acceptance.sh failed"
}

run_scenario() {
  step_bootstrap
  step_acceptance
  echo "demo 15 PASSED"
}

case "${PHASE}" in
  all|--phase=all|"")
    run_scenario
    ;;
  --phase=up)
    step_bootstrap
    echo "phase up PASSED (stack left running until EXIT trap)"
    trap - EXIT
    STARTED=0
    ;;
  *)
    echo "Unknown phase: ${PHASE}" >&2
    echo "Usage: $0 [all|--phase=up]" >&2
    exit 2
    ;;
esac
