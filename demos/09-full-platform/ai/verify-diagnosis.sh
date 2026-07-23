#!/usr/bin/env bash
# Capstone 19.04 acceptance: seed Memory → NN query → investigator diagnosis
# cites Observe telemetry + Memory record; runtime.restart stays approval-gated.
#
# Brings up forge-models + forge-memory + forge-agents (fake tools for CI) via the
# capstone compose overlay unless FORGE_AI_SKIP_COMPOSE=1 (use an existing stack).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/09-full-platform"
AI_DIR="${DEMO_DIR}/ai"
LIB_DIR="${DEMO_DIR}/lib"
FIXTURES="${AI_DIR}/fixtures/historical-incidents.json"

export FORGE_MODELS_BACKEND="${FORGE_MODELS_BACKEND:-fake}"
export FORGE_AGENTS_TOOLS_MODE="${FORGE_AGENTS_TOOLS_MODE:-fake}"
export FORGE_MODELS_URL="${FORGE_MODELS_URL:-http://127.0.0.1:4300}"
export FORGE_AGENTS_URL="${FORGE_AGENTS_URL:-http://127.0.0.1:4301}"
export FORGE_MEMORY_URL="${FORGE_MEMORY_URL:-http://127.0.0.1:4303}"
export FORGE_MEMORY_PROJECT="${FORGE_MEMORY_PROJECT:-${FORGE_CAPSTONE_PROJECT:-capstone}}"
export FORGE_MEMORY_PROJECT_B="${FORGE_MEMORY_PROJECT_B:-capstone-b}"
export FORGE_AGENTS_DEPLOYMENT="${FORGE_AGENTS_DEPLOYMENT:-dep-capstone}"
export FORGE_CLASSIFY_LABEL="${FORGE_CLASSIFY_LABEL:-infra.readiness_failure}"
# Capstone overlay interpolates these even when AI-only services are started.
export FORGE_HOST_PATTERN="${FORGE_HOST_PATTERN:-\{service\}.demo.localhost}"
if [[ -z "${FORGE_SECRETS_MASTER_KEY:-}" ]]; then
  FORGE_SECRETS_MASTER_KEY="$(python3 -c 'import base64,os; print(base64.b64encode(os.urandom(32)).decode())')"
fi
export FORGE_SECRETS_MASTER_KEY
export FORGE_SECRETS_MASTER_KEY_ID="${FORGE_SECRETS_MASTER_KEY_ID:-capstone-ai-m1}"

COMPOSE=(
  docker compose
    -f "${ROOT_DIR}/compose.yaml"
    -f "${DEMO_DIR}/compose.yaml"
    --project-directory "${ROOT_DIR}"
)

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-capstone-ai.XXXXXX")"
STARTED=0
POLL_ATTEMPTS="${FORGE_AGENTS_POLL_ATTEMPTS:-90}"
POLL_SLEEP_SECONDS="${FORGE_AGENTS_POLL_SLEEP_SECONDS:-0.25}"

cleanup() {
  if [[ "${STARTED}" -eq 1 && "${FORGE_AI_KEEP:-0}" != "1" ]]; then
    "${COMPOSE[@]}" stop forge-agents forge-memory forge-models >/dev/null 2>&1 || true
  fi
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

pass() { echo "  PASS: $*"; }
fail() {
  echo "  FAIL: $*" >&2
  echo "--- agents logs ---" >&2
  "${COMPOSE[@]}" logs --tail=80 forge-agents >&2 || true
  echo "--- memory logs ---" >&2
  "${COMPOSE[@]}" logs --tail=80 forge-memory >&2 || true
  exit 1
}
step() { echo; echo "[$1] $2"; }

http_body() {
  local out="$1" method="$2" url="$3"
  shift 3
  curl --silent --show-error --output "${out}" --write-out '%{http_code}' \
    --request "${method}" "${url}" "$@"
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

free_ai_ports() {
  local port cid
  for port in 4300 4301 4303; do
    cid="$(docker ps -q --filter "publish=${port}" 2>/dev/null || true)"
    if [[ -n "${cid}" ]]; then
      echo "Stopping container(s) on host port ${port} for AI bind..."
      # shellcheck disable=SC2086
      docker stop ${cid} >/dev/null 2>&1 || true
    fi
  done
}

wait_run_status() {
  local run_id="$1"
  shift
  local want=("$@")
  local body status i
  body="${TMP_DIR}/run-${run_id}.json"
  for i in $(seq 1 "${POLL_ATTEMPTS}"); do
    status="$(http_body "${body}" GET "${FORGE_AGENTS_URL}/v1/runs/${run_id}" \
      -H "X-Forge-Project: ${FORGE_MEMORY_PROJECT}")"
    [[ "${status}" == "200" ]] || fail "GET run ${run_id} HTTP ${status}: $(cat "${body}")"
    if python3 - "${body}" "${want[@]}" <<'PY'
import json, sys
body = json.load(open(sys.argv[1]))
want = set(sys.argv[2:])
sys.exit(0 if body.get("status") in want else 1)
PY
    then
      cat "${body}"
      return 0
    fi
    sleep "${POLL_SLEEP_SECONDS}"
  done
  fail "run ${run_id} did not reach: ${want[*]} (last=$(cat "${body}"))"
}

echo "== Capstone 19.04: Models + Agents + Memory diagnosis =="
echo "Tools mode: ${FORGE_AGENTS_TOOLS_MODE}"
echo "Models backend: ${FORGE_MODELS_BACKEND}"

step "0" "unit tests (fixtures + agent config)"
python3 -m unittest discover -s "${LIB_DIR}" -p 'test_ai_helpers.py' -v \
  || fail "ai helper unit tests failed"
pass "ai helper unit tests"

if [[ "${FORGE_AI_SKIP_COMPOSE:-0}" != "1" ]]; then
  step "1" "start forge-models + forge-memory + forge-agents"
  free_ai_ports
  for name in forge-agents forge-memory forge-models; do
    docker rm -f "${name}" >/dev/null 2>&1 || true
  done
  "${COMPOSE[@]}" up -d --build --force-recreate \
    forge-models forge-memory forge-agents
  STARTED=1
else
  step "1" "using existing AI stack (FORGE_AI_SKIP_COMPOSE=1)"
fi

wait_http "${FORGE_MODELS_URL}/health/ready" "Models"
wait_http "${FORGE_MEMORY_URL}/health/ready" "Memory"
wait_http "${FORGE_AGENTS_URL}/health/ready" "Agents"
pass "AI services ready"

step "2" "seed historical incidents"
FORGE_MEMORY_URL="${FORGE_MEMORY_URL}" \
FORGE_MEMORY_PROJECT="${FORGE_MEMORY_PROJECT}" \
FORGE_MEMORY_PROJECT_B="${FORGE_MEMORY_PROJECT_B}" \
  "${AI_DIR}/seed-memory.sh" || fail "seed-memory.sh failed"
pass "Memory seeded for project ${FORGE_MEMORY_PROJECT}"

step "3" "NN query returns expected historical incident"
expected="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["expected_id"])' "${FIXTURES}")"
query_text="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["new_failure_text"])' "${FIXTURES}")"
collection="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["collection"])' "${FIXTURES}")"
status="$(http_body "${TMP_DIR}/nn.json" POST \
  "${FORGE_MEMORY_URL}/v1/collections/${collection}/query" \
  -H "X-Forge-Project: ${FORGE_MEMORY_PROJECT}" \
  -H 'content-type: application/json' \
  -d "$(python3 -c 'import json,sys; print(json.dumps({"text":sys.argv[1],"model":"local-embed-small","top_k":3}))' "${query_text}")")"
[[ "${status}" == "200" ]] || fail "NN query HTTP ${status}: $(cat "${TMP_DIR}/nn.json")"
PYTHONPATH="${LIB_DIR}" python3 - "${TMP_DIR}/nn.json" "${expected}" <<'PY' || fail "NN ordering failed"
import sys
from ai_helpers import nn_top_id
body = open(sys.argv[1]).read()
assert nn_top_id(body) == sys.argv[2], body
print("top", sys.argv[2])
PY
pass "NN top-1 = ${expected}"

# Isolation
status="$(http_body "${TMP_DIR}/iso.json" GET \
  "${FORGE_MEMORY_URL}/v1/collections/${collection}" \
  -H "X-Forge-Project: ${FORGE_MEMORY_PROJECT_B}")"
[[ "${status}" == "404" ]] || fail "project isolation expected 404, got ${status}"
pass "Memory retrieval project-isolated (${FORGE_MEMORY_PROJECT_B} → 404)"

step "4" "deployment-investigator registered with memory.search"
status="$(http_body "${TMP_DIR}/agents.json" GET "${FORGE_AGENTS_URL}/v1/agents")"
[[ "${status}" == "200" ]] || fail "list agents HTTP ${status}"
python3 - "${TMP_DIR}/agents.json" <<'PY' || fail "investigator tools mismatch"
import json, sys
body = json.load(open(sys.argv[1]))
agents = {a["name"]: a for a in body.get("agents") or []}
inv = agents.get("deployment-investigator")
assert inv, sorted(agents)
tools = set(inv.get("tools") or [])
for t in ("deployment.read", "logs.search", "metrics.query", "memory.search", "runtime.restart"):
    assert t in tools, (t, tools)
perms = set(inv.get("permissions") or [])
assert "memory:read" in perms, perms
print("tools ok", sorted(tools))
PY
pass "capstone investigator tools/permissions registered"

# Registered tools contract
status="$(http_body "${TMP_DIR}/tools.json" GET "${FORGE_AGENTS_URL}/v1/tools")"
[[ "${status}" == "200" ]] || fail "list tools HTTP ${status}"
python3 - "${TMP_DIR}/tools.json" <<'PY' || fail "tool registry missing required tools"
import json, sys
body = json.load(open(sys.argv[1]))
names = {t.get("name") for t in body.get("tools") or []}
for t in ("deployment.read", "logs.search", "metrics.query", "memory.search", "runtime.restart"):
    assert t in names, (t, sorted(names))
restart = next(t for t in body["tools"] if t["name"] == "runtime.restart")
assert restart.get("destructive") is True, restart
print("registered tools ok")
PY
pass "tool set matches registered platform tools"

step "5" "investigator diagnoses with telemetry + Memory citation"
payload="$(PYTHONPATH="${LIB_DIR}" python3 - <<PY
import json
from ai_helpers import build_investigator_plan, load_json
fx = load_json("${FIXTURES}")
dep = "${FORGE_AGENTS_DEPLOYMENT}"
label = "${FORGE_CLASSIFY_LABEL}"
plan = build_investigator_plan(
    deployment_id=dep,
    collection=fx["collection"],
    query=fx["new_failure_text"],
    expected_memory_id=fx["expected_id"],
    classification={"label": label, "source": "incident-classify"},
)
print(json.dumps({
    "input": f"Investigate failing deployment {dep}; product classify={label}",
    "context": {
        "dry_run": True,
        "deployment_id": dep,
        "classification": {"label": label, "source": "incident-classify"},
        "plan": plan,
    },
}))
PY
)"
status="$(http_body "${TMP_DIR}/start.json" POST \
  "${FORGE_AGENTS_URL}/v1/agents/deployment-investigator/runs" \
  -H "X-Forge-Project: ${FORGE_MEMORY_PROJECT}" \
  -H 'content-type: application/json' \
  -d "${payload}")"
[[ "${status}" == "202" ]] || fail "start investigator HTTP ${status}: $(cat "${TMP_DIR}/start.json")"
run_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["run_id"])' "${TMP_DIR}/start.json")"
echo "  run_id=${run_id}"
wait_run_status "${run_id}" awaiting_approval >"${TMP_DIR}/run-main.json"

PYTHONPATH="${LIB_DIR}" python3 - "${TMP_DIR}/run-main.json" "${expected}" \
  "${FORGE_AGENTS_DEPLOYMENT}" "${FORGE_CLASSIFY_LABEL}" <<'PY' || fail "diagnosis assertions failed"
import sys
from ai_helpers import diagnosis_cites_telemetry_and_memory
text = diagnosis_cites_telemetry_and_memory(
    open(sys.argv[1]).read(),
    expected_memory_id=sys.argv[2],
    deployment_id=sys.argv[3],
    classification_label=sys.argv[4],
)
print(text)
PY
pass "diagnosis cites Observe telemetry + Memory ${expected}; restart awaiting_approval"

echo
echo "Capstone 19.04 AI diagnosis loop ready."
echo "  Project:    ${FORGE_MEMORY_PROJECT}"
echo "  Memory id:  ${expected}"
echo "  Deployment: ${FORGE_AGENTS_DEPLOYMENT}"
echo "  Run:        ${run_id}"
echo "  Agents:     ${FORGE_AGENTS_URL}"
echo "  Memory:     ${FORGE_MEMORY_URL}"
echo "  Models:     ${FORGE_MODELS_URL}"
