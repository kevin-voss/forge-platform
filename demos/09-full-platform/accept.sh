#!/usr/bin/env bash
# Capstone one-command acceptance suite (19.06 / specs.md Step 19 test list).
# Expects start.sh to have brought the stack healthy (or starts CI subset itself).
#
# Exit 0 only when every Step 19 acceptance item passes.
# On failure: non-zero exit + correlated log dump. Teardown always runs unless
# FORGE_ACCEPT_KEEP=1.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/09-full-platform"
TESTS_DIR="${DEMO_DIR}/tests"
LIB_DIR="${DEMO_DIR}/lib"

# shellcheck source=lib/accept_common.sh
source "${LIB_DIR}/accept_common.sh"

export CI_SUBSET="${CI_SUBSET:-true}"
export FORGE_MODELS_BACKEND="${FORGE_MODELS_BACKEND:-fake}"
export FORGE_AGENTS_TOOLS_MODE="${FORGE_AGENTS_TOOLS_MODE:-fake}"
export FORGE_WORKFLOWS_AGENTS_MODE="${FORGE_WORKFLOWS_AGENTS_MODE:-fake}"
export FORGE_WORKFLOWS_CONTROL_MODE="${FORGE_WORKFLOWS_CONTROL_MODE:-fake}"
export FORGE_ACCEPT_KEEP="${FORGE_ACCEPT_KEEP:-0}"

load_state

COMPOSE=(
  docker compose
    -f "${ROOT_DIR}/compose.yaml"
    -f "${DEMO_DIR}/compose.yaml"
    --project-directory "${ROOT_DIR}"
)

STARTED_BY_ACCEPT=0

cleanup() {
  local ec=$?
  if [[ "${STARTED_BY_ACCEPT}" -eq 1 && "${FORGE_ACCEPT_KEEP}" != "1" ]]; then
    echo "Teardown: stopping CI subset services..."
    "${COMPOSE[@]}" stop \
      forge-workflows forge-agents forge-memory forge-models >/dev/null 2>&1 || true
  fi
  if [[ "${ec}" -ne 0 ]]; then
    dump_capstone_logs
  fi
  exit "${ec}"
}
trap cleanup EXIT

require_cmd docker
require_cmd curl
require_cmd python3

ensure_stack() {
  if curl --fail --silent --show-error \
    "${FORGE_WORKFLOWS_URL:-http://127.0.0.1:4302}/health/ready" >/dev/null 2>&1 &&
    curl --fail --silent --show-error \
      "${FORGE_AGENTS_URL:-http://127.0.0.1:4301}/health/ready" >/dev/null 2>&1 &&
    curl --fail --silent --show-error \
      "${FORGE_MODELS_URL:-http://127.0.0.1:4300}/health/ready" >/dev/null 2>&1 &&
    curl --fail --silent --show-error \
      "${FORGE_MEMORY_URL:-http://127.0.0.1:4303}/health/ready" >/dev/null 2>&1; then
    echo "Stack already healthy; reusing."
    return 0
  fi
  echo "Stack not ready — invoking start.sh (CI_SUBSET=${CI_SUBSET})..."
  CI_SUBSET="${CI_SUBSET}" "${DEMO_DIR}/start.sh"
  STARTED_BY_ACCEPT=1
  load_state
}

run_test() {
  local script="$1" name
  name="$(basename "${script}" .sh)"
  echo
  echo "======== ${name} ========"
  if bash "${script}"; then
    pass "${name}"
  else
    fail "${name}" || true
  fi
}

echo "== Capstone acceptance suite (19.06) =="
echo "CI_SUBSET=${CI_SUBSET}  MODELS_BACKEND=${FORGE_MODELS_BACKEND}"
echo "Folder: demos/09-full-platform (distinct from demos/09-platform-identity)"

# Offline / self-contained tests first (no stack required).
OFFLINE_TESTS=(
  "${TESTS_DIR}/01-smoke.sh"
  "${TESTS_DIR}/02-deploy.sh"
  "${TESTS_DIR}/03-identity.sh"
  "${TESTS_DIR}/04-secrets.sh"
  "${TESTS_DIR}/05-events.sh"
  "${TESTS_DIR}/06-telemetry.sh"
  "${TESTS_DIR}/11-interop.sh"
  "${TESTS_DIR}/12-contracts.sh"
)

for t in "${OFFLINE_TESTS[@]}"; do
  [[ -x "${t}" ]] || chmod +x "${t}"
  run_test "${t}"
done

# Stack-backed AI + recovery loop.
ensure_stack
export FORGE_AI_SKIP_COMPOSE=1
export FORGE_AI_KEEP=1
export FORGE_SCENARIO_SKIP_COMPOSE=1
export FORGE_SCENARIO_KEEP=1

STACK_TESTS=(
  "${TESTS_DIR}/07-models.sh"
  "${TESTS_DIR}/08-agents.sh"
  "${TESTS_DIR}/09-workflow-recovery.sh"
  "${TESTS_DIR}/10-rollback.sh"
)

for t in "${STACK_TESTS[@]}"; do
  [[ -x "${t}" ]] || chmod +x "${t}"
  run_test "${t}"
done

summary_or_die
echo
echo "NORTH-STAR GATE PASSED (demos/09-full-platform)"
echo "  Recovery: broken release → detect → diagnose → approve → rollback → report"
echo "  Contracts: OpenAPI + event schemas validated; product uses documented APIs only"
