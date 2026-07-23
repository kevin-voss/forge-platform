# Shared helpers for capstone start/accept/tests (sourced).
# shellcheck shell=bash

: "${DEMO_DIR:?DEMO_DIR must be set before sourcing accept_common.sh}"
: "${ROOT_DIR:?ROOT_DIR must be set before sourcing accept_common.sh}"

PASS="${PASS:-0}"
FAIL="${FAIL:-0}"
STATE_FILE="${FORGE_CAPSTONE_STATE:-${DEMO_DIR}/.capstone-state}"

pass() {
  PASS=$((PASS + 1))
  echo "  PASS: $*"
}

fail() {
  FAIL=$((FAIL + 1))
  echo "  FAIL: $*" >&2
  return 1
}

step() {
  echo
  echo "[$1] $2"
}

dump_capstone_logs() {
  local compose=(
    docker compose
      -f "${ROOT_DIR}/compose.yaml"
      -f "${DEMO_DIR}/compose.yaml"
      --project-directory "${ROOT_DIR}"
  )
  echo "--- correlated service logs (tail) ---" >&2
  "${compose[@]}" logs --tail=80 \
    forge-workflows forge-agents forge-models forge-memory \
    forge-control forge-gateway forge-events 2>/dev/null || true
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
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
  [[ "${ready}" -eq 1 ]] || {
    echo "timed out waiting for ${label}" >&2
    return 1
  }
}

write_state() {
  local mode="$1"
  cat >"${STATE_FILE}" <<EOF
CI_SUBSET=${CI_SUBSET}
MODE=${mode}
FORGE_MODELS_URL=${FORGE_MODELS_URL:-http://127.0.0.1:4300}
FORGE_AGENTS_URL=${FORGE_AGENTS_URL:-http://127.0.0.1:4301}
FORGE_WORKFLOWS_URL=${FORGE_WORKFLOWS_URL:-http://127.0.0.1:4302}
FORGE_MEMORY_URL=${FORGE_MEMORY_URL:-http://127.0.0.1:4303}
FORGE_EVENTS_URL=${FORGE_EVENTS_HOST_URL:-${FORGE_EVENTS_URL:-http://127.0.0.1:4105}}
FORGE_CONTROL_URL=${FORGE_CONTROL_URL:-http://127.0.0.1:4001}
FORGE_GATEWAY_URL=${FORGE_GATEWAY_URL:-http://127.0.0.1:4000}
STARTED_AT=$(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF
}

load_state() {
  if [[ -f "${STATE_FILE}" ]]; then
    # shellcheck disable=SC1090
    set -a
    # shellcheck disable=SC1090
    source "${STATE_FILE}"
    set +a
  fi
}

summary_or_die() {
  echo
  echo "acceptance summary: ${PASS} passed, ${FAIL} failed"
  if [[ "${FAIL}" -ne 0 ]]; then
    exit 1
  fi
}
