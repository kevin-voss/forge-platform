#!/usr/bin/env bash
# Demo 14: model-serving gate (epic 14 acceptance).
# Scenario: embed → classify → summarize against forge-models (fake backend),
#           then assert /v1/usage reflects the three calls.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/14-model-serving"
CLIENT_DIR="${DEMO_DIR}/client"
COMPOSE=(
  docker compose
  -f "${DEMO_DIR}/compose.yaml"
  --project-directory "${DEMO_DIR}"
)

export FORGE_MODELS_BACKEND="${FORGE_MODELS_BACKEND:-fake}"
export FORGE_MODELS_METRICS_ENABLED="${FORGE_MODELS_METRICS_ENABLED:-true}"
export FORGE_MODELS_URL="${FORGE_MODELS_URL:-http://127.0.0.1:4300}"
export FORGE_MODELS_EMBED_MODEL="${FORGE_MODELS_EMBED_MODEL:-local-embed-small}"
export FORGE_MODELS_GEN_MODEL="${FORGE_MODELS_GEN_MODEL:-local-general}"
export FORGE_LOG_LEVEL="${FORGE_LOG_LEVEL:-info}"

MODELS_SERVICE="forge-models"
PHASE="${1:-all}"
STARTED=0

cleanup() {
  if [[ "${STARTED}" -eq 1 ]]; then
    "${COMPOSE[@]}" --profile client down -v --remove-orphans >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

dump_context() {
  echo "--- ${MODELS_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=120 "${MODELS_SERVICE}" >&2 || true
  echo "--- /v1/usage ---" >&2
  curl --silent --show-error "${FORGE_MODELS_URL}/v1/usage" >&2 || true
  echo >&2
}

fail() {
  echo "Demo 14 failed: $*" >&2
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
  # Root compose may already bind 4300 as container `forge-models`.
  local cid
  cid="$(docker ps -q --filter "publish=4300" 2>/dev/null || true)"
  if [[ -n "${cid}" ]]; then
    echo "Stopping container(s) publishing host port 4300 for a clean demo bind..."
    # shellcheck disable=SC2086
    docker stop ${cid} >/dev/null 2>&1 || true
  fi
}

assert_openapi_parses() {
  echo "Validating contracts/openapi/forge-models.openapi.yaml parses..."
  if ! python3 - "${ROOT_DIR}/contracts/openapi/forge-models.openapi.yaml" <<'PY'
import sys
from pathlib import Path

try:
    import yaml
except ImportError:
    # Fallback: require PyYAML; demos usually have it via lint tooling.
    print("PyYAML required to parse OpenAPI", file=sys.stderr)
    sys.exit(1)

path = Path(sys.argv[1])
doc = yaml.safe_load(path.read_text())
assert doc.get("openapi"), "missing openapi version"
paths = doc.get("paths") or {}
for required in (
    "/v1/models/{model}/embed",
    "/v1/models/{model}/classify",
    "/v1/models/{model}/summarize",
    "/v1/usage",
):
    assert required in paths, f"missing path {required}"
print("openapi ok")
PY
  then
    fail "forge-models.openapi.yaml did not parse or is missing required paths"
  fi
}

run_unit_tests() {
  echo "Running Go client assertion unit tests..."
  (
    cd "${CLIENT_DIR}"
    go test ./...
  ) || fail "go test failed"
}

step_bootstrap() {
  echo "== Demo 14: Model serving =="
  echo "Backend: FORGE_MODELS_BACKEND=${FORGE_MODELS_BACKEND}"
  echo "Models URL: ${FORGE_MODELS_URL}"
  echo "Embed model: ${FORGE_MODELS_EMBED_MODEL}"
  echo "Gen model: ${FORGE_MODELS_GEN_MODEL}"

  chmod +x "${DEMO_DIR}/run.sh"
  assert_openapi_parses
  run_unit_tests
  free_host_port

  echo "Bringing up forge-models (fake backend)..."
  "${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
  "${COMPOSE[@]}" up -d --build --force-recreate --remove-orphans "${MODELS_SERVICE}"
  STARTED=1

  wait_http "${FORGE_MODELS_URL}/health/ready" "forge-models"
  curl --fail --silent --show-error "${FORGE_MODELS_URL}/" >/dev/null ||
    fail "identity endpoint failed"
  echo "  forge-models ready"
}

step_client() {
  echo "Running Go client (embed + classify + summarize)..."
  "${COMPOSE[@]}" --profile client run --rm --build go-client ||
    fail "Go client failed"
}

assert_usage() {
  echo "Checking /v1/usage reflects demo calls..."
  local usage
  usage="$(curl --fail --silent --show-error "${FORGE_MODELS_URL}/v1/usage")" ||
    fail "GET /v1/usage failed"

  python3 - "${usage}" "${FORGE_MODELS_EMBED_MODEL}" "${FORGE_MODELS_GEN_MODEL}" <<'PY' || fail "usage assertions failed"
import json, sys

usage = json.loads(sys.argv[1])
embed_model = sys.argv[2]
gen_model = sys.argv[3]
by_model = usage.get("by_model") or {}
if embed_model not in by_model:
    raise SystemExit(f"missing usage for {embed_model}: {by_model!r}")
if gen_model not in by_model:
    raise SystemExit(f"missing usage for {gen_model}: {by_model!r}")
embed_req = by_model[embed_model].get("requests", 0)
gen_req = by_model[gen_model].get("requests", 0)
if embed_req < 1:
    raise SystemExit(f"{embed_model} requests={embed_req}, want >= 1")
if gen_req < 2:
    raise SystemExit(f"{gen_model} requests={gen_req}, want >= 2 (classify+summarize)")
print(f"  PASS: {embed_model} requests={embed_req}, {gen_model} requests={gen_req}")
PY

  echo "Checking /metrics exposes inference counters..."
  local metrics
  metrics="$(curl --fail --silent --show-error "${FORGE_MODELS_URL}/metrics")" ||
    fail "GET /metrics failed"
  echo "${metrics}" | grep -q 'models_embed_requests_total' ||
    fail "/metrics missing models_embed_requests_total"
  echo "${metrics}" | grep -q 'models_classify_requests_total\|models_summarize_requests_total\|models_generate_requests_total' ||
    fail "/metrics missing classify/summarize/generate counters"
  echo "  PASS: /metrics contains inference counters"
}

run_scenario() {
  step_bootstrap
  step_client
  assert_usage
  echo "demo 14 PASSED"
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
