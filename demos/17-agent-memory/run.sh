#!/usr/bin/env bash
# Demo 17: agent-memory gate (epic 17 acceptance).
# Scenario: seed historical incidents (Models embed) → NN query for similar
#           failure → agent memory.search cites incident → project isolation →
#           forge-memory restart durability.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/17-agent-memory"
COMPOSE=(
  docker compose
  -p forge-demo-17
  -f "${DEMO_DIR}/compose.yaml"
  --project-directory "${DEMO_DIR}"
)

export FORGE_MODELS_BACKEND="${FORGE_MODELS_BACKEND:-fake}"
export FORGE_AGENTS_TOOLS_MODE="${FORGE_AGENTS_TOOLS_MODE:-fake}"
export FORGE_MODELS_URL="${FORGE_MODELS_URL:-http://127.0.0.1:4300}"
export FORGE_AGENTS_URL="${FORGE_AGENTS_URL:-http://127.0.0.1:4301}"
export FORGE_MEMORY_URL="${FORGE_MEMORY_URL:-http://127.0.0.1:4303}"
export FORGE_MEMORY_PROJECT_A="${FORGE_MEMORY_PROJECT_A:-proj-a}"
export FORGE_MEMORY_PROJECT_B="${FORGE_MEMORY_PROJECT_B:-proj-b}"
export FORGE_DEMO17_COMPOSE_PROJECT="${FORGE_DEMO17_COMPOSE_PROJECT:-forge-demo-17}"
export FORGE_LOG_LEVEL="${FORGE_LOG_LEVEL:-info}"

MEMORY_SERVICE="forge-memory"
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
  echo "--- ${MEMORY_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=160 "${MEMORY_SERVICE}" >&2 || true
  echo "--- ${AGENTS_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=120 "${AGENTS_SERVICE}" >&2 || true
  echo "--- ${MODELS_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${MODELS_SERVICE}" >&2 || true
  echo "--- memory query dump (proj-a/incidents) ---" >&2
  curl --silent --show-error \
    -H "X-Forge-Project: ${FORGE_MEMORY_PROJECT_A}" \
    -H 'content-type: application/json' \
    -X POST "${FORGE_MEMORY_URL}/v1/collections/incidents/query" \
    -d '{"text":"Postgres connection pool exhausted during deploy rollout; readiness probes failing with connection refused","model":"local-embed-small","top_k":3}' \
    >&2 || true
  echo >&2
  echo "--- recent agent runs ---" >&2
  curl --silent --show-error \
    -H "X-Forge-Project: ${FORGE_MEMORY_PROJECT_A}" \
    "${FORGE_AGENTS_URL}/v1/runs" >&2 || true
  echo >&2
}

fail() {
  echo "Demo 17 failed: $*" >&2
  dump_context
  exit 1
}

wait_http() {
  local url="$1" label="$2" attempts="${3:-180}"
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
  for port in 4300 4301 4303; do
    cid="$(docker ps -q --filter "publish=${port}" 2>/dev/null || true)"
    if [[ -n "${cid}" ]]; then
      echo "Stopping container(s) publishing host port ${port} for a clean demo bind..."
      # shellcheck disable=SC2086
      docker stop ${cid} >/dev/null 2>&1 || true
    fi
  done
}

step_bootstrap() {
  echo "== Demo 17: Agent memory =="
  echo "Tools mode: FORGE_AGENTS_TOOLS_MODE=${FORGE_AGENTS_TOOLS_MODE}"
  echo "Models backend: FORGE_MODELS_BACKEND=${FORGE_MODELS_BACKEND}"
  echo "Memory URL: ${FORGE_MEMORY_URL}"
  echo "Agents URL: ${FORGE_AGENTS_URL}"
  echo "Models URL: ${FORGE_MODELS_URL}"
  echo "Project A: ${FORGE_MEMORY_PROJECT_A}"
  echo "Project B: ${FORGE_MEMORY_PROJECT_B}"

  chmod +x "${DEMO_DIR}/acceptance.sh" "${DEMO_DIR}/run.sh"
  free_host_ports

  echo "Bringing up forge-models (fake) + forge-memory + forge-agents (fake tools)..."
  "${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
  "${COMPOSE[@]}" up -d --build --force-recreate --remove-orphans \
    "${MODELS_SERVICE}" "${MEMORY_SERVICE}" "${AGENTS_SERVICE}"
  STARTED=1

  wait_http "${FORGE_MODELS_URL}/health/ready" "forge-models"
  wait_http "${FORGE_MEMORY_URL}/health/ready" "forge-memory" 240
  wait_http "${FORGE_AGENTS_URL}/health/ready" "forge-agents"
  curl --fail --silent --show-error "${FORGE_MEMORY_URL}/" >/dev/null ||
    fail "memory identity endpoint failed"
  curl --fail --silent --show-error "${FORGE_AGENTS_URL}/" >/dev/null ||
    fail "agents identity endpoint failed"
  echo "  forge-models + forge-memory + forge-agents ready"
}

step_acceptance() {
  echo "Running acceptance assertions..."
  bash "${DEMO_DIR}/acceptance.sh" || fail "acceptance.sh failed"
}

run_scenario() {
  step_bootstrap
  step_acceptance
  echo "demo 17 PASSED"
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
