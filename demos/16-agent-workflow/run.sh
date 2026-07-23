#!/usr/bin/env bash
# Demo 16: agent-workflow gate (epic 16 acceptance).
# Scenario: deployment.failed → collect diagnostics (parallel) → investigator
#           agent → human approval → restart-resume → approve → Control rollback
#           + final report (rolled_back=true).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/16-agent-workflow"
COMPOSE=(
  docker compose
  -p forge-demo-16
  -f "${DEMO_DIR}/compose.yaml"
  --project-directory "${DEMO_DIR}"
)

export FORGE_MODELS_BACKEND="${FORGE_MODELS_BACKEND:-fake}"
export FORGE_AGENTS_TOOLS_MODE="${FORGE_AGENTS_TOOLS_MODE:-fake}"
export FORGE_WORKFLOWS_AGENTS_MODE="${FORGE_WORKFLOWS_AGENTS_MODE:-fake}"
export FORGE_WORKFLOWS_CONTROL_MODE="${FORGE_WORKFLOWS_CONTROL_MODE:-fake}"
export FORGE_MODELS_URL="${FORGE_MODELS_URL:-http://127.0.0.1:4300}"
export FORGE_AGENTS_URL="${FORGE_AGENTS_URL:-http://127.0.0.1:4301}"
export FORGE_WORKFLOWS_URL="${FORGE_WORKFLOWS_URL:-http://127.0.0.1:4302}"
export FORGE_WORKFLOWS_PROJECT="${FORGE_WORKFLOWS_PROJECT:-demo-16}"
export FORGE_WORKFLOWS_DEPLOYMENT="${FORGE_WORKFLOWS_DEPLOYMENT:-dep-failing}"
export FORGE_DEMO16_COMPOSE_PROJECT="${FORGE_DEMO16_COMPOSE_PROJECT:-forge-demo-16}"
export FORGE_LOG_LEVEL="${FORGE_LOG_LEVEL:-info}"

WORKFLOWS_SERVICE="forge-workflows"
AGENTS_SERVICE="forge-agents"
MODELS_SERVICE="forge-models"
POSTGRES_SERVICE="postgres"
PHASE="${1:-all}"
STARTED=0

cleanup() {
  if [[ "${STARTED}" -eq 1 ]]; then
    "${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

dump_context() {
  echo "--- ${WORKFLOWS_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=200 "${WORKFLOWS_SERVICE}" >&2 || true
  echo "--- ${AGENTS_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=80 "${AGENTS_SERVICE}" >&2 || true
  echo "--- ${MODELS_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=40 "${MODELS_SERVICE}" >&2 || true
  echo "--- recent workflow runs ---" >&2
  curl --silent --show-error \
    -H "X-Forge-Project: ${FORGE_WORKFLOWS_PROJECT}" \
    "${FORGE_WORKFLOWS_URL}/v1/runs" >&2 || true
  echo >&2
}

fail() {
  echo "Demo 16 failed: $*" >&2
  dump_context
  exit 1
}

wait_http() {
  local url="$1" label="$2" attempts="${3:-150}"
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
  for port in 4300 4301 4302; do
    cid="$(docker ps -q --filter "publish=${port}" 2>/dev/null || true)"
    if [[ -n "${cid}" ]]; then
      echo "Stopping container(s) publishing host port ${port} for a clean demo bind..."
      # shellcheck disable=SC2086
      docker stop ${cid} >/dev/null 2>&1 || true
    fi
  done
}

step_bootstrap() {
  echo "== Demo 16: Agent workflow =="
  echo "Agents mode (workflows client): FORGE_WORKFLOWS_AGENTS_MODE=${FORGE_WORKFLOWS_AGENTS_MODE}"
  echo "Control mode: FORGE_WORKFLOWS_CONTROL_MODE=${FORGE_WORKFLOWS_CONTROL_MODE}"
  echo "Tools mode: FORGE_AGENTS_TOOLS_MODE=${FORGE_AGENTS_TOOLS_MODE}"
  echo "Models backend: FORGE_MODELS_BACKEND=${FORGE_MODELS_BACKEND}"
  echo "Workflows URL: ${FORGE_WORKFLOWS_URL}"
  echo "Project: ${FORGE_WORKFLOWS_PROJECT}"
  echo "Deployment: ${FORGE_WORKFLOWS_DEPLOYMENT}"

  chmod +x "${DEMO_DIR}/acceptance.sh" "${DEMO_DIR}/run.sh" \
    "${DEMO_DIR}/fixtures/postgres-init.sh"
  free_host_ports

  echo "Bringing up postgres + models(fake) + agents(fake) + workflows..."
  "${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
  "${COMPOSE[@]}" up -d --build --force-recreate --remove-orphans \
    "${POSTGRES_SERVICE}" "${MODELS_SERVICE}" "${AGENTS_SERVICE}" "${WORKFLOWS_SERVICE}"
  STARTED=1

  wait_http "${FORGE_MODELS_URL}/health/ready" "forge-models"
  wait_http "${FORGE_AGENTS_URL}/health/ready" "forge-agents"
  wait_http "${FORGE_WORKFLOWS_URL}/health/ready" "forge-workflows" 180
  curl --fail --silent --show-error "${FORGE_WORKFLOWS_URL}/" >/dev/null ||
    fail "workflows identity endpoint failed"
  echo "  stack ready"
}

step_acceptance() {
  echo "Running acceptance assertions..."
  bash "${DEMO_DIR}/acceptance.sh" || fail "acceptance.sh failed"
}

run_scenario() {
  step_bootstrap
  step_acceptance
  echo "demo 16 PASSED"
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
