#!/usr/bin/env bash
# Demo 16 acceptance — falsifiable assertions against a running forge-workflows.
# Invoked by run.sh after readiness. Exit 0 only when all pass.
set -euo pipefail

DEMO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${DEMO_DIR}/../.." && pwd)"

WORKFLOWS_URL="${FORGE_WORKFLOWS_URL:-http://127.0.0.1:4302}"
MODELS_URL="${FORGE_MODELS_URL:-http://127.0.0.1:4300}"
AGENTS_URL="${FORGE_AGENTS_URL:-http://127.0.0.1:4301}"
PROJECT_ID="${FORGE_WORKFLOWS_PROJECT:-demo-16}"
DEPLOYMENT_ID="${FORGE_WORKFLOWS_DEPLOYMENT:-dep-failing}"
POLL_ATTEMPTS="${FORGE_WORKFLOWS_POLL_ATTEMPTS:-120}"
POLL_SLEEP_SECONDS="${FORGE_WORKFLOWS_POLL_SLEEP_SECONDS:-0.5}"
COMPOSE_PROJECT="${FORGE_DEMO16_COMPOSE_PROJECT:-forge-demo-16}"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-workflows-accept.XXXXXX")"
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
  if ! python3 - "${ROOT_DIR}/contracts/openapi/forge-workflows.openapi.yaml" <<'PY'
import sys, yaml
path = sys.argv[1]
doc = yaml.safe_load(open(path))
assert doc.get("openapi"), "missing openapi version"
paths = doc.get("paths") or {}
for required in (
    "/v1/workflows",
    "/v1/workflows/{name}/runs",
    "/v1/runs",
    "/v1/runs/{id}",
    "/v1/runs/{id}/approvals",
    "/v1/approvals/{id}/approve",
    "/v1/approvals/{id}/deny",
    "/v1/triggers/test",
):
    assert required in paths, f"missing path {required}"
print("workflows openapi ok")
PY
  then
    fail "forge-workflows.openapi.yaml did not parse or is missing required paths"
  fi
  pass "forge-workflows OpenAPI parses with run/approval/trigger paths"
}

assert_stack_ready() {
  step "1" "models + agents + workflows ready"
  local status body
  body="${TMP_DIR}/models-ready.json"
  status="$(http_body "${body}" GET "${MODELS_URL}/health/ready")"
  [[ "${status}" == "200" ]] || fail "models ready HTTP ${status}: $(cat "${body}")"
  pass "forge-models /health/ready"

  body="${TMP_DIR}/agents-ready.json"
  status="$(http_body "${body}" GET "${AGENTS_URL}/health/ready")"
  [[ "${status}" == "200" ]] || fail "agents ready HTTP ${status}: $(cat "${body}")"
  pass "forge-agents /health/ready"

  body="${TMP_DIR}/workflows-ready.json"
  status="$(http_body "${body}" GET "${WORKFLOWS_URL}/health/ready")"
  [[ "${status}" == "200" ]] || fail "workflows ready HTTP ${status}: $(cat "${body}")"
  pass "forge-workflows /health/ready"
}

assert_incident_workflow_registered() {
  step "2" "incident-response workflow registered with deployment.failed trigger"
  local status body
  body="${TMP_DIR}/workflows.json"
  status="$(http_body "${body}" GET "${WORKFLOWS_URL}/v1/workflows")"
  [[ "${status}" == "200" ]] || fail "list workflows HTTP ${status}: $(cat "${body}")"
  python3 - "${body}" <<'PY' || fail "incident-response missing or misconfigured"
import json, sys
body = json.load(open(sys.argv[1]))
wfs = {w.get("name"): w for w in (body.get("workflows") or [])}
assert "incident-response" in wfs, sorted(wfs)
wf = wfs["incident-response"]
trig = wf.get("trigger") or {}
assert trig.get("event") == "deployment.failed", trig
ids = {s.get("id") for s in (wf.get("steps") or [])}
for required in (
    "collect-diagnostics",
    "diagnose",
    "approve-rollback",
    "do-rollback",
    "finalize",
):
    assert required in ids, (required, sorted(ids))
print("ok")
PY
  pass "incident-response listed with event trigger and expected steps"
}

get_run() {
  local run_id="$1" out="$2"
  local status
  status="$(http_body "${out}" GET "${WORKFLOWS_URL}/v1/runs/${run_id}" \
    -H "$(hdr_project)")"
  [[ "${status}" == "200" ]] || fail "GET run ${run_id} HTTP ${status}: $(cat "${out}")"
}

wait_run_status() {
  local run_id="$1"
  shift
  local want=("$@")
  local body i
  body="${TMP_DIR}/run-wait-${run_id}.json"
  for i in $(seq 1 "${POLL_ATTEMPTS}"); do
    get_run "${run_id}" "${body}"
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

step_trigger_and_await_approval() {
  step "3" "inject deployment.failed → workflow starts → awaiting_approval"
  local trigger_body run_id run_body
  trigger_body="${TMP_DIR}/trigger.json"
  run_body="${TMP_DIR}/run-main.json"

  local event_id status
  event_id="demo-16-$(python3 -c 'import uuid; print(uuid.uuid4())')"
  status="$(http_body "${trigger_body}" POST "${WORKFLOWS_URL}/v1/triggers/test" \
    -H "$(hdr_project)" \
    -H 'content-type: application/json' \
    -d "$(python3 - "${DEPLOYMENT_ID}" "${event_id}" <<'PY'
import json, sys
print(json.dumps({
    "event": "deployment.failed",
    "event_id": sys.argv[2],
    "data": {"deployment_id": sys.argv[1]},
}))
PY
)")"
  [[ "${status}" == "202" ]] || fail "triggers/test HTTP ${status}: $(cat "${trigger_body}")"

  run_id="$(python3 - "${trigger_body}" <<'PY'
import json, sys
body = json.load(open(sys.argv[1]))
assert body.get("status") == "started", body
runs = body.get("runs") or []
assert runs, body
print(runs[0]["run_id"])
PY
)" || fail "trigger did not start a run: $(cat "${trigger_body}")"
  echo "  run_id=${run_id}"
  echo "${run_id}" >"${TMP_DIR}/main-run-id.txt"

  wait_run_status "${run_id}" awaiting_approval >"${run_body}"

  python3 - "${run_body}" "${DEPLOYMENT_ID}" <<'PY' || fail "pre-approval assertions failed"
import json, sys
body = json.load(open(sys.argv[1]))
dep = sys.argv[2]
assert body.get("status") == "awaiting_approval", body.get("status")
assert body.get("workflow") == "incident-response", body.get("workflow")
steps = {s.get("id"): s for s in (body.get("steps") or [])}

# Parallel diagnostics completed (parent + branches).
parent = steps.get("collect-diagnostics") or {}
assert parent.get("status") == "completed", parent
branches = (parent.get("output") or {}).get("branches") or {}
assert "collect-logs" in branches and branches["collect-logs"].get("status") == "completed", branches
assert "collect-metrics" in branches and branches["collect-metrics"].get("status") == "completed", branches
for bid in ("collect-logs", "collect-metrics"):
    child = steps.get(bid) or {}
    assert child.get("status") == "completed", (bid, child)
    assert int(child.get("attempt") or 0) == 1, (bid, child)

# Agent analysis produced.
diag = steps.get("diagnose") or {}
assert diag.get("status") == "completed", diag
assert int(diag.get("attempt") or 0) == 1, diag
out = diag.get("output") or {}
assert out.get("agent") == "deployment-investigator", out
result = out.get("result")
if isinstance(result, str):
    result = json.loads(result)
assert isinstance(result, dict), result
assert dep in str(result.get("diagnosis") or ""), result
assert result.get("deployment_id") == dep or dep in str(result), result

prep = steps.get("prepare-rollback") or {}
assert prep.get("status") == "completed", prep
assert int(prep.get("attempt") or 0) == 1, prep

pending = body.get("pending_approval") or {}
assert pending.get("status") == "pending", pending
assert "Approve rollback" in (pending.get("prompt") or ""), pending
assert dep in (pending.get("prompt") or ""), pending
assert pending.get("id"), pending
print("approval_id", pending["id"])
print("AUDIT_STEPS", len(steps))
PY
  pass "diagnostics (parallel) + agent analysis completed"
  pass "approval requested; run awaiting_approval"
}

snapshot_attempts() {
  local run_body="$1" out="$2"
  python3 - "${run_body}" "${out}" <<'PY'
import json, sys
body = json.load(open(sys.argv[1]))
snap = {
    "approval_id": (body.get("pending_approval") or {}).get("id"),
    "attempts": {
        s["id"]: int(s.get("attempt") or 0)
        for s in (body.get("steps") or [])
        if s.get("status") == "completed"
    },
}
json.dump(snap, open(sys.argv[2], "w"))
print(json.dumps(snap))
PY
}

step_restart_resume() {
  step "4" "restart forge-workflows while awaiting → resume, no steps repeated"
  local run_id run_before run_after snap
  run_id="$(cat "${TMP_DIR}/main-run-id.txt")"
  run_before="${TMP_DIR}/run-main.json"
  run_after="${TMP_DIR}/run-after-restart.json"
  snap="${TMP_DIR}/attempts.json"

  snapshot_attempts "${run_before}" "${snap}" >/dev/null

  echo "  restarting forge-workflows container..."
  docker compose -p "${COMPOSE_PROJECT}" \
    -f "${DEMO_DIR}/compose.yaml" \
    --project-directory "${DEMO_DIR}" \
    restart forge-workflows >/dev/null

  local ready=0
  for _ in $(seq 1 90); do
    if curl --fail --silent --show-error "${WORKFLOWS_URL}/health/ready" >/dev/null 2>&1; then
      ready=1
      break
    fi
    sleep 1
  done
  [[ "${ready}" -eq 1 ]] || fail "workflows not ready after restart"

  # Stay awaiting; completed steps must keep attempt=1.
  wait_run_status "${run_id}" awaiting_approval >"${run_after}"

  python3 - "${run_after}" "${snap}" <<'PY' || fail "restart resume assertions failed"
import json, sys
body = json.load(open(sys.argv[1]))
snap = json.load(open(sys.argv[2]))
assert body.get("status") == "awaiting_approval", body.get("status")
pending = body.get("pending_approval") or {}
assert pending.get("status") == "pending", pending
assert pending.get("id") == snap["approval_id"], (pending.get("id"), snap["approval_id"])
steps = {s.get("id"): s for s in (body.get("steps") or [])}
for sid, attempt in snap["attempts"].items():
    st = steps.get(sid) or {}
    assert st.get("status") == "completed", (sid, st)
    assert int(st.get("attempt") or 0) == int(attempt), (sid, st.get("attempt"), attempt)
    assert int(st.get("attempt") or 0) == 1, (sid, st)
print("resume ok; approval", pending["id"])
PY
  pass "run still awaiting_approval after restart; approval state preserved"
  pass "completed steps not repeated (attempt remains 1)"
}

step_approve_rollback_and_report() {
  step "5" "approve → rollback (fake Control) + final report stored"
  local run_id run_body approval_id status approve_body
  run_id="$(cat "${TMP_DIR}/main-run-id.txt")"
  run_body="${TMP_DIR}/run-final.json"
  approve_body="${TMP_DIR}/approve.json"

  get_run "${run_id}" "${TMP_DIR}/run-before-approve.json"
  approval_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["pending_approval"]["id"])' \
    "${TMP_DIR}/run-before-approve.json")"

  status="$(http_body "${approve_body}" POST \
    "${WORKFLOWS_URL}/v1/approvals/${approval_id}/approve" \
    -H "$(hdr_project)" \
    -H 'content-type: application/json' \
    -d '{"decided_by":"demo-16","reason":"approved for demo gate"}')"
  [[ "${status}" == "200" ]] || fail "approve HTTP ${status}: $(cat "${approve_body}")"
  pass "approval ${approval_id} approved"

  wait_run_status "${run_id}" completed >"${run_body}"

  python3 - "${run_body}" "${DEPLOYMENT_ID}" <<'PY' || fail "post-approval rollback/report assertions failed"
import json, sys
body = json.load(open(sys.argv[1]))
dep = sys.argv[2]
assert body.get("status") == "completed", body.get("status")
result = body.get("result") or {}
assert result.get("rolled_back") is True, result
assert result.get("report_ref") or (result.get("report") or {}).get("report_ref"), result
report = result.get("report") or {}
if report:
    assert report.get("rolled_back") is True, report
    assert report.get("deployment_id") == dep, report
    assert report.get("report_ref"), report

steps = {s.get("id"): s for s in (body.get("steps") or [])}
for sid in ("do-rollback", "finalize"):
    st = steps.get(sid) or {}
    assert st.get("status") == "completed", (sid, st)
rb = steps["do-rollback"].get("output") or {}
assert rb.get("rolled_back") is True or rb.get("action") == "rollback", rb
fin = steps["finalize"].get("output") or {}
assert fin.get("report_ref"), fin
assert fin.get("rolled_back") is True, fin

# Deny-path close step skipped after approve.
close = steps.get("close") or {}
assert close.get("status") == "skipped", close

# History fully auditable.
assert len(steps) >= 7, sorted(steps)
for sid in (
    "collect-diagnostics",
    "collect-logs",
    "collect-metrics",
    "diagnose",
    "prepare-rollback",
    "approve-rollback",
    "do-rollback",
    "finalize",
):
    assert sid in steps, sid
print("rolled_back", result.get("rolled_back"), "report_ref", result.get("report_ref") or fin.get("report_ref"))
PY
  pass "rollback executed only after approval (rolled_back=true)"
  pass "final report stored with report_ref"
  pass "run history fully auditable"
}

assert_openapi_parses
assert_stack_ready
assert_incident_workflow_registered
step_trigger_and_await_approval
step_restart_resume
step_approve_rollback_and_report

echo
echo "acceptance summary: ${PASS} passed, ${FAIL} failed"
[[ "${FAIL}" -eq 0 ]]
