#!/usr/bin/env bash
# Demo 15 acceptance — falsifiable assertions against a running forge-agents.
# Intended to be invoked by run.sh after readiness. Exit 0 only when all pass.
set -euo pipefail

DEMO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${DEMO_DIR}/../.." && pwd)"

AGENTS_URL="${FORGE_AGENTS_URL:-http://127.0.0.1:4301}"
MODELS_URL="${FORGE_MODELS_URL:-http://127.0.0.1:4300}"
PROJECT_ID="${FORGE_AGENTS_PROJECT:-demo-15}"
DEPLOYMENT_ID="${FORGE_AGENTS_DEPLOYMENT:-dep-failing}"
POLL_ATTEMPTS="${FORGE_AGENTS_POLL_ATTEMPTS:-90}"
POLL_SLEEP_SECONDS="${FORGE_AGENTS_POLL_SLEEP_SECONDS:-0.25}"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-agents-accept.XXXXXX")"
trap 'rm -rf "${TMP_DIR}"' EXIT

PASS=0
FAIL=0

pass() {
  PASS=$((PASS + 1))
  echo "  PASS: $*"
}

fail() {
  FAIL=$((FAIL + 1))
  echo "  FAIL: $*" >&2
  echo "acceptance summary: ${PASS} passed, ${FAIL} failed" >&2
  exit 1
}

step() {
  echo
  echo "[$1] $2"
}

hdr_project() {
  printf 'X-Forge-Project: %s' "${PROJECT_ID}"
}

http_body() {
  local out="$1" method="$2" url="$3"
  shift 3
  curl --silent --show-error --output "${out}" --write-out '%{http_code}' \
    --request "${method}" "${url}" "$@"
}

assert_openapi_parses() {
  step "0" "OpenAPI contracts parse"
  if ! python3 - "${ROOT_DIR}/contracts/openapi/forge-agents.openapi.yaml" <<'PY'
import sys, yaml
path = sys.argv[1]
doc = yaml.safe_load(open(path))
assert doc.get("openapi"), "missing openapi version"
paths = doc.get("paths") or {}
for required in (
    "/v1/agents",
    "/v1/agents/{name}/runs",
    "/v1/runs/{run_id}",
    "/v1/tools",
    "/v1/approvals/{approval_id}/approve",
    "/v1/approvals/{approval_id}/deny",
):
    assert required in paths, f"missing path {required}"
print("agents openapi ok")
PY
  then
    fail "forge-agents.openapi.yaml did not parse or is missing required paths"
  fi
  pass "forge-agents OpenAPI parses with run/approval paths"

  if ! python3 - "${ROOT_DIR}/contracts/openapi/forge-models.openapi.yaml" <<'PY'
import sys, yaml
path = sys.argv[1]
doc = yaml.safe_load(open(path))
assert doc.get("openapi"), "missing openapi version"
assert "/v1/models" in (doc.get("paths") or {}), "missing /v1/models"
print("models openapi ok")
PY
  then
    fail "forge-models.openapi.yaml did not parse"
  fi
  pass "forge-models OpenAPI parses"
}

assert_models_ready() {
  step "1" "forge-models fake backend is reachable"
  local status body
  body="${TMP_DIR}/models-ready.json"
  status="$(http_body "${body}" GET "${MODELS_URL}/health/ready")"
  [[ "${status}" == "200" ]] || fail "models ready HTTP ${status}: $(cat "${body}")"
  pass "forge-models /health/ready"
}

assert_investigator_registered() {
  step "2" "deployment-investigator is registered"
  local status body
  body="${TMP_DIR}/agents.json"
  status="$(http_body "${body}" GET "${AGENTS_URL}/v1/agents")"
  [[ "${status}" == "200" ]] || fail "list agents HTTP ${status}: $(cat "${body}")"
  python3 - "${body}" <<'PY' || fail "deployment-investigator missing from registry"
import json, sys
body = json.load(open(sys.argv[1]))
names = {a.get("name") for a in body.get("agents") or []}
assert "deployment-investigator" in names, names
print("ok")
PY
  pass "deployment-investigator listed in GET /v1/agents"
}

wait_run_status() {
  local run_id="$1"
  shift
  local want=("$@")
  local body status i
  body="${TMP_DIR}/run-${run_id}.json"
  for i in $(seq 1 "${POLL_ATTEMPTS}"); do
    status="$(http_body "${body}" GET "${AGENTS_URL}/v1/runs/${run_id}" \
      -H "$(hdr_project)")"
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
  fail "run ${run_id} did not reach status in: ${want[*]} (last=$(cat "${body}"))"
}

start_investigator_run() {
  # Writes response body to $1; prints run_id on stdout via python.
  local out="$1"
  local payload="$2"
  local status
  status="$(http_body "${out}" POST \
    "${AGENTS_URL}/v1/agents/deployment-investigator/runs" \
    -H "$(hdr_project)" \
    -H 'content-type: application/json' \
    -d "${payload}")"
  [[ "${status}" == "202" ]] || fail "start investigator HTTP ${status}: $(cat "${out}")"
  python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["run_id"])' "${out}"
}

step_investigator_diagnose_and_pause() {
  step "3" "investigator inspects failing deployment and requests restart (awaiting approval)"
  local start_body run_id run_body
  start_body="${TMP_DIR}/start-main.json"
  run_body="${TMP_DIR}/run-main.json"

  local payload
  payload="$(python3 - "${DEPLOYMENT_ID}" <<'PY'
import json, sys
dep = sys.argv[1]
plan = [
    {
        "kind": "tool_call",
        "tool": "deployment.read",
        "args": {"deployment_id": dep},
    },
    {
        "kind": "tool_call",
        "tool": "logs.search",
        "args": {"deployment": dep, "limit": 20},
    },
    {
        "kind": "tool_call",
        "tool": "metrics.query",
        "args": {"query": f'up{{deployment="{dep}"}}'},
    },
    {
        "kind": "tool_call",
        "tool": "runtime.restart",
        "args": {"deployment_id": dep},
    },
]
print(json.dumps({
    "input": f"Investigate failing deployment {dep} and recommend remediation",
    "context": {
        "dry_run": True,
        "deployment_id": dep,
        "plan": plan,
    },
}))
PY
)"

  run_id="$(start_investigator_run "${start_body}" "${payload}")"
  echo "  run_id=${run_id}"
  wait_run_status "${run_id}" awaiting_approval >"${run_body}"

  python3 - "${run_body}" "${DEPLOYMENT_ID}" <<'PY' || fail "investigator diagnosis / approval assertions failed"
import json, sys

body = json.load(open(sys.argv[1]))
dep = sys.argv[2]
assert body.get("status") == "awaiting_approval", body.get("status")
steps = body.get("steps") or []
assert steps, "expected auditable steps"

tool_steps = [s for s in steps if s.get("type") == "tool"]
by_tool = {s.get("tool"): s for s in tool_steps}

# Registered read tools executed successfully.
for name in ("deployment.read", "logs.search", "metrics.query"):
    assert name in by_tool, f"missing tool step {name}; have={list(by_tool)}"
    obs = by_tool[name].get("observation") or {}
    assert obs.get("ok") is True, f"{name} not ok: {obs}"

dep_obs = by_tool["deployment.read"]["observation"]
assert dep_obs.get("ready") is False, dep_obs
assert dep_obs.get("deployment_id") == dep, dep_obs

logs_obs = by_tool["logs.search"]["observation"]
entries = logs_obs.get("entries") or []
joined = " ".join(str(e.get("message") or "") for e in entries if isinstance(e, dict))
assert "readiness probe failed" in joined.lower(), logs_obs

metrics_obs = by_tool["metrics.query"]["observation"]
samples = metrics_obs.get("samples") or []
assert samples, metrics_obs
assert float(samples[0].get("value", 1)) == 0.0, samples

# Restart recommended via approval gate — not executed.
pending = body.get("pending_approval") or {}
assert pending.get("tool") == "runtime.restart", pending
assert pending.get("args", {}).get("deployment_id") == dep, pending
assert str(pending.get("status") or "pending").lower() == "pending", pending
# No runtime.restart tool observation yet (paused before execute).
assert "runtime.restart" not in by_tool, by_tool.get("runtime.restart")

diagnosis = (
    f"Diagnosis: deployment {dep} ready=false status={dep_obs.get('status')}; "
    "readiness probe failed; metrics up=0; recommend runtime.restart "
    "(awaiting human approval)."
)
print(diagnosis)
print("AUDIT_STEPS", len(steps))
PY
  echo "  (diagnosis + audit assertions above)"
  pass "diagnosis produced from registered tools; restart awaiting_approval (not executed)"
  pass "run history is auditable (model + tool steps persisted)"

  # Leave run paused — do not approve (proves no unauthorized restart).
  echo "${run_id}" >"${TMP_DIR}/main-run-id.txt"
}

step_hallucinated_tool_rejected() {
  step "4" "hallucinated / unregistered tool call is rejected"
  local start_body run_body run_id
  start_body="${TMP_DIR}/start-halluc.json"
  run_body="${TMP_DIR}/run-halluc.json"

  local payload
  payload="$(python3 <<'PY'
import json
print(json.dumps({
    "input": "try a hallucinated tool",
    "context": {
        "dry_run": True,
        "plan": [
            {
                "kind": "tool_call",
                "tool": "shell.exec",
                "args": {"command": "rm -rf /"},
            },
        ],
    },
}))
PY
)"

  run_id="$(start_investigator_run "${start_body}" "${payload}")"
  echo "  run_id=${run_id}"
  wait_run_status "${run_id}" failed stopped cancelled >"${run_body}"

  python3 - "${run_body}" <<'PY' || fail "hallucinated tool was not rejected"
import json, sys
body = json.load(open(sys.argv[1]))
assert body.get("status") == "failed", body.get("status")
steps = body.get("steps") or []
tool_steps = [s for s in steps if s.get("type") == "tool"]
assert tool_steps, steps
step = tool_steps[0]
assert step.get("tool") == "shell.exec", step
obs = step.get("observation") or {}
assert obs.get("ok") is False, obs
reason = str(obs.get("reason") or body.get("error") or "")
assert "unknown_tool" in reason or reason == "unknown_tool", (reason, obs)
# Must never show a successful destructive side effect.
assert obs.get("restarted") is not True, obs
print("rejected", reason)
PY
  pass "unregistered tool shell.exec rejected (unknown_tool); no side effects"
}

step_limits_respected() {
  step "5" "execution limits are respected (max_steps)"
  # deployment-investigator max_steps=10; force a bounded stop via a short agent
  # is already covered by unit tests — here assert the seed advertises limits and
  # the main run did not exceed them.
  local agent_body status
  agent_body="${TMP_DIR}/investigator.json"
  status="$(http_body "${agent_body}" GET \
    "${AGENTS_URL}/v1/agents/deployment-investigator")"
  [[ "${status}" == "200" ]] || fail "get agent HTTP ${status}: $(cat "${agent_body}")"

  local main_run
  main_run="${TMP_DIR}/run-main.json"
  [[ -f "${main_run}" ]] || fail "main run body missing (step 3)"

  python3 - "${agent_body}" "${main_run}" <<'PY' || fail "limits assertion failed"
import json, sys
agent = json.load(open(sys.argv[1]))
run = json.load(open(sys.argv[2]))
limits = agent.get("limits") or {}
max_steps = int(limits.get("max_steps") or 0)
assert max_steps >= 1, limits
model_steps = [s for s in (run.get("steps") or []) if s.get("type") == "model"]
assert len(model_steps) <= max_steps, (len(model_steps), max_steps)
assert int(limits.get("timeout_seconds") or 0) >= 1, limits
print(f"model_steps={len(model_steps)} max_steps={max_steps}")
PY
  pass "investigator limits declared; main run stayed within max_steps"
}

assert_openapi_parses
assert_models_ready
assert_investigator_registered
step_investigator_diagnose_and_pause
step_hallucinated_tool_rejected
step_limits_respected

echo
echo "acceptance summary: ${PASS} passed, ${FAIL} failed"
[[ "${FAIL}" -eq 0 ]]
