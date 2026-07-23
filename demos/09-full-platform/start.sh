#!/usr/bin/env bash
# Capstone one-command start (19.06).
# Brings the demo to healthy so accept.sh can run.
#
# CI_SUBSET=true  (default) — documented CI gate stack:
#   postgres + forge-models + forge-agents + forge-memory + forge-workflows
#   (fake Models/Agents/Control for deterministic recovery loop)
# CI_SUBSET=false — full platform + product via deploy.sh (FORGE_CAPSTONE_KEEP=1)
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/09-full-platform"
# shellcheck source=lib/accept_common.sh
source "${DEMO_DIR}/lib/accept_common.sh"

export CI_SUBSET="${CI_SUBSET:-true}"
export FORGE_MODELS_BACKEND="${FORGE_MODELS_BACKEND:-fake}"
export FORGE_AGENTS_TOOLS_MODE="${FORGE_AGENTS_TOOLS_MODE:-fake}"
export FORGE_WORKFLOWS_AGENTS_MODE="${FORGE_WORKFLOWS_AGENTS_MODE:-fake}"
export FORGE_WORKFLOWS_CONTROL_MODE="${FORGE_WORKFLOWS_CONTROL_MODE:-fake}"
export FORGE_WORKFLOWS_EVENTS_ENABLED="${FORGE_WORKFLOWS_EVENTS_ENABLED:-false}"
export FORGE_WORKFLOWS_REPORT_BUCKET="${FORGE_WORKFLOWS_REPORT_BUCKET:-wf-reports}"
export FORGE_WORKFLOWS_DEFAULT_PROJECT="${FORGE_WORKFLOWS_DEFAULT_PROJECT:-${FORGE_CAPSTONE_PROJECT:-capstone}}"
export FORGE_MODELS_URL="${FORGE_MODELS_URL:-http://127.0.0.1:4300}"
export FORGE_AGENTS_URL="${FORGE_AGENTS_URL:-http://127.0.0.1:4301}"
export FORGE_WORKFLOWS_URL="${FORGE_WORKFLOWS_URL:-http://127.0.0.1:4302}"
export FORGE_MEMORY_URL="${FORGE_MEMORY_URL:-http://127.0.0.1:4303}"
export FORGE_HOST_PATTERN="${FORGE_HOST_PATTERN:-\{service\}.demo.localhost}"
if [[ -z "${FORGE_SECRETS_MASTER_KEY:-}" ]]; then
  FORGE_SECRETS_MASTER_KEY="$(python3 -c 'import base64,os; print(base64.b64encode(os.urandom(32)).decode())')"
fi
export FORGE_SECRETS_MASTER_KEY
export FORGE_SECRETS_MASTER_KEY_ID="${FORGE_SECRETS_MASTER_KEY_ID:-capstone-start-m1}"

COMPOSE=(
  docker compose
    -f "${ROOT_DIR}/compose.yaml"
    -f "${DEMO_DIR}/compose.yaml"
    --project-directory "${ROOT_DIR}"
)

require_cmd docker
require_cmd curl
require_cmd python3

free_ports() {
  local port cid
  for port in "$@"; do
    cid="$(docker ps -q --filter "publish=${port}" 2>/dev/null || true)"
    if [[ -n "${cid}" ]]; then
      echo "Stopping container(s) publishing host port ${port}..."
      # shellcheck disable=SC2086
      docker stop ${cid} >/dev/null 2>&1 || true
    fi
  done
}

start_ci_subset() {
  echo "== Capstone start (CI subset) =="
  echo "Stack: postgres + models + agents + memory + workflows"
  echo "FORGE_MODELS_BACKEND=${FORGE_MODELS_BACKEND} FORGE_AGENTS_TOOLS_MODE=${FORGE_AGENTS_TOOLS_MODE}"
  free_ports 4300 4301 4302 4303

  for name in forge-workflows forge-agents forge-memory forge-models; do
    docker rm -f "${name}" >/dev/null 2>&1 || true
  done

  "${COMPOSE[@]}" up -d --build --force-recreate \
    postgres forge-models forge-agents forge-memory forge-workflows

  wait_http "${FORGE_MODELS_URL}/health/ready" "forge-models" 180
  wait_http "${FORGE_AGENTS_URL}/health/ready" "forge-agents" 180
  wait_http "${FORGE_MEMORY_URL}/health/ready" "forge-memory" 180
  wait_http "${FORGE_WORKFLOWS_URL}/health/ready" "forge-workflows" 180

  write_state "ci-subset"
  echo
  echo "Capstone CI subset is healthy."
  echo "  Models:    ${FORGE_MODELS_URL}"
  echo "  Agents:    ${FORGE_AGENTS_URL}"
  echo "  Workflows: ${FORGE_WORKFLOWS_URL}"
  echo "  Memory:    ${FORGE_MEMORY_URL}"
  echo "  State:     ${STATE_FILE}"
  echo
  echo "Next: ./accept.sh   (or: make demo-accept DEMO=09-full-platform)"
}

start_full() {
  echo "== Capstone start (full platform + product) =="
  echo "Delegating to deploy.sh with FORGE_CAPSTONE_KEEP=1 ..."
  FORGE_CAPSTONE_KEEP=1 \
  FORGE_MODELS_BACKEND="${FORGE_MODELS_BACKEND}" \
  FORGE_AGENTS_TOOLS_MODE="${FORGE_AGENTS_TOOLS_MODE}" \
    "${DEMO_DIR}/deploy.sh"

  # Ensure workflows (recovery loop) is up alongside the deployed product.
  "${COMPOSE[@]}" up -d --build --force-recreate forge-workflows
  wait_http "${FORGE_WORKFLOWS_URL}/health/ready" "forge-workflows" 180

  write_state "full"
  echo
  echo "Capstone full stack + product is healthy (kept running)."
  echo "  State: ${STATE_FILE}"
  echo "Next: ./accept.sh"
}

case "${CI_SUBSET}" in
  true|1|yes|YES)
    start_ci_subset
    ;;
  false|0|no|NO)
    start_full
    ;;
  *)
    echo "CI_SUBSET must be true|false (got ${CI_SUBSET})" >&2
    exit 2
    ;;
esac
