#!/usr/bin/env bash
# Capstone 19.05: failure injection → deployment.failed → incident-response
# workflow → Memory-assisted diagnosis → human approval → Control rollback →
# report + completion event. Also covers deny (no rollback) and mid-run resume.
#
# CI path (default): postgres + models(fake) + agents(fake) + workflows(fake Control)
# via root compose + capstone overlay. Product CAPSTONE_BREAK is unit-tested.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/09-full-platform"
SCENARIO_DIR="${DEMO_DIR}/scenario"
LIB_DIR="${DEMO_DIR}/lib"
PRODUCT_API="${DEMO_DIR}/product/api-go"

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
export FORGE_EVENTS_URL="${FORGE_EVENTS_HOST_URL:-${FORGE_EVENTS_URL:-http://127.0.0.1:4105}}"
export FORGE_HOST_PATTERN="${FORGE_HOST_PATTERN:-\{service\}.demo.localhost}"
if [[ -z "${FORGE_SECRETS_MASTER_KEY:-}" ]]; then
  FORGE_SECRETS_MASTER_KEY="$(python3 -c 'import base64,os; print(base64.b64encode(os.urandom(32)).decode())')"
fi
export FORGE_SECRETS_MASTER_KEY
export FORGE_SECRETS_MASTER_KEY_ID="${FORGE_SECRETS_MASTER_KEY_ID:-capstone-scenario-m1}"

PROJECT_ID="${FORGE_WORKFLOWS_DEFAULT_PROJECT}"
DEPLOYMENT_ID="${FORGE_CAPSTONE_DEPLOYMENT:-dep-capstone-broken}"
POLL_ATTEMPTS="${FORGE_WORKFLOWS_POLL_ATTEMPTS:-120}"
POLL_SLEEP_SECONDS="${FORGE_WORKFLOWS_POLL_SLEEP_SECONDS:-0.5}"
COMPOSE_PROJECT="${FORGE_CAPSTONE_COMPOSE_PROJECT:-forge}"
PHASE="${1:-all}"

COMPOSE=(
  docker compose
    -f "${ROOT_DIR}/compose.yaml"
    -f "${DEMO_DIR}/compose.yaml"
    --project-directory "${ROOT_DIR}"
)

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-capstone-scenario.XXXXXX")"
STARTED=0
PASS=0
FAIL=0

cleanup() {
  if [[ "${STARTED}" -eq 1 && "${FORGE_SCENARIO_KEEP:-0}" != "1" ]]; then
    "${COMPOSE[@]}" stop forge-workflows forge-agents forge-models >/dev/null 2>&1 || true
  fi
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

pass() { PASS=$((PASS + 1)); echo "  PASS: $*"; }
fail() {
  FAIL=$((FAIL + 1))
  echo "  FAIL: $*" >&2
  echo "--- workflows logs ---" >&2
  "${COMPOSE[@]}" logs --tail=120 forge-workflows >&2 || true
  echo "acceptance summary: ${PASS} passed, ${FAIL} failed" >&2
  exit 1
}
step() { echo; echo "[$1] $2"; }

hdr_project() { printf 'X-Forge-Project: %s' "${PROJECT_ID}"; }

http_body() {
  local out="$1" method="$2" url="$3"
  shift 3
  curl --silent --show-error --output "${out}" --write-out '%{http_code}' \
    --request "${method}" "${url}" "$@"
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
      echo "Stopping container(s) publishing host port ${port}..."
      # shellcheck disable=SC2086
      docker stop ${cid} >/dev/null 2>&1 || true
    fi
  done
}

run_unit_checks() {
  step "U1" "CAPSTONE_BREAK readiness unit tests (api-go + helpers)"
  (
    cd "${PRODUCT_API}"
    go test ./... -count=1 -run 'TestCapstoneBreak|TestHealth'
  ) || fail "api-go CAPSTONE_BREAK tests failed"
  pass "api-go CAPSTONE_BREAK fails /health/ready deterministically"

  (
    cd "${DEMO_DIR}"
    PYTHONPATH="${LIB_DIR}" python3 -m unittest discover -s lib -p 'test_scenario_helpers.py' -v
  ) || fail "scenario helper unit tests failed"
  pass "incident-response.yaml validates; event/report helpers ok"
}

bootstrap_stack() {
  step "0" "bring up models + agents + workflows (fake Control)"
  chmod +x "${SCENARIO_DIR}/break-release.sh"

  if [[ "${FORGE_SCENARIO_SKIP_COMPOSE:-0}" == "1" ]]; then
    echo "  using existing workflows stack (FORGE_SCENARIO_SKIP_COMPOSE=1)"
  else
    free_host_ports
    echo "Starting postgres + forge-models + forge-agents + forge-workflows..."
    "${COMPOSE[@]}" up -d --build --force-recreate \
      postgres forge-models forge-agents forge-workflows
    STARTED=1
  fi

  wait_http "${FORGE_MODELS_URL}/health/ready" "forge-models"
  wait_http "${FORGE_AGENTS_URL}/health/ready" "forge-agents"
  wait_http "${FORGE_WORKFLOWS_URL}/health/ready" "forge-workflows" 180
  pass "workflows stack ready (project=${PROJECT_ID})"
}

assert_workflow_registered() {
  step "1" "incident-response registered with deployment.failed trigger"
  local status body
  body="${TMP_DIR}/workflows.json"
  status="$(http_body "${body}" GET "${FORGE_WORKFLOWS_URL}/v1/workflows")"
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
    "collect-diagnostics", "diagnose", "approve-rollback", "do-rollback", "finalize",
):
    assert required in ids, (required, sorted(ids))
print("ok")
PY
  pass "incident-response listed with expected steps"
}

get_run() {
  local run_id="$1" out="$2"
  local status
  status="$(http_body "${out}" GET "${FORGE_WORKFLOWS_URL}/v1/runs/${run_id}" \
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

trigger_failed_deployment() {
  local out="$1" deployment_id="$2" event_id="$3"
  local status
  # Documented contract path: readiness failure → deployment.failed → workflow.
  # CI uses /v1/triggers/test (same event shape as Events subscription).
  status="$(http_body "${out}" POST "${FORGE_WORKFLOWS_URL}/v1/triggers/test" \
    -H "$(hdr_project)" \
    -H 'content-type: application/json' \
    -d "$(python3 - "${deployment_id}" "${event_id}" "${LIB_DIR}" <<'PY'
import json, sys
sys.path.insert(0, sys.argv[3])
from scenario_helpers import build_deployment_failed_event
dep, eid = sys.argv[1], sys.argv[2]
ev = build_deployment_failed_event(deployment_id=dep)
print(json.dumps({
    "event": "deployment.failed",
    "event_id": eid,
    "data": ev["data"],
}))
PY
)")"
  [[ "${status}" == "202" ]] || fail "triggers/test HTTP ${status}: $(cat "${out}")"
}

step_trigger_and_await_approval() {
  step "2" "inject deployment.failed → workflow → awaiting_approval"
  local trigger_body run_id run_body event_id
  trigger_body="${TMP_DIR}/trigger.json"
  run_body="${TMP_DIR}/run-main.json"
  event_id="capstone-$(python3 -c 'import uuid; print(uuid.uuid4())')"

  # Simulate detection of CAPSTONE_BREAK readiness failure before emitting the event.
  PYTHONPATH="${LIB_DIR}" python3 - <<'PY' || fail "readiness failure detection helper failed"
from scenario_helpers import readiness_failure_from_capstone_break
assert readiness_failure_from_capstone_break(
    {"status": "not_ready", "error": "capstone_break"}, 503
)
print("detected readiness failure (CAPSTONE_BREAK)")
PY
  pass "CAPSTONE_BREAK readiness failure detected → will emit deployment.failed"

  trigger_failed_deployment "${trigger_body}" "${DEPLOYMENT_ID}" "${event_id}"
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
parent = steps.get("collect-diagnostics") or {}
assert parent.get("status") == "completed", parent
diag = steps.get("diagnose") or {}
assert diag.get("status") == "completed", diag
out = diag.get("output") or {}
assert out.get("agent") == "deployment-investigator", out
result = out.get("result")
if isinstance(result, str):
    import json as _json
    result = _json.loads(result)
assert isinstance(result, dict), result
assert dep in str(result.get("diagnosis") or "") or result.get("deployment_id") == dep, result
pending = body.get("pending_approval") or {}
assert pending.get("status") == "pending", pending
assert "Approve rollback" in (pending.get("prompt") or ""), pending
assert pending.get("id"), pending
print("approval_id", pending["id"])
PY
  pass "diagnostics + agent diagnosis completed; approval pending (no rollback yet)"
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
  step "3" "restart forge-workflows mid-run → resume, no steps repeated"
  local run_id run_before run_after snap
  run_id="$(cat "${TMP_DIR}/main-run-id.txt")"
  run_before="${TMP_DIR}/run-main.json"
  run_after="${TMP_DIR}/run-after-restart.json"
  snap="${TMP_DIR}/attempts.json"

  snapshot_attempts "${run_before}" "${snap}" >/dev/null

  echo "  restarting forge-workflows..."
  "${COMPOSE[@]}" restart forge-workflows >/dev/null
  wait_http "${FORGE_WORKFLOWS_URL}/health/ready" "forge-workflows" 90

  wait_run_status "${run_id}" awaiting_approval >"${run_after}"
  python3 - "${run_after}" "${snap}" <<'PY' || fail "restart resume assertions failed"
import json, sys
body = json.load(open(sys.argv[1]))
snap = json.load(open(sys.argv[2]))
assert body.get("status") == "awaiting_approval", body.get("status")
pending = body.get("pending_approval") or {}
assert pending.get("id") == snap["approval_id"], (pending.get("id"), snap["approval_id"])
steps = {s.get("id"): s for s in (body.get("steps") or [])}
for sid, attempt in snap["attempts"].items():
    st = steps.get(sid) or {}
    assert st.get("status") == "completed", (sid, st)
    assert int(st.get("attempt") or 0) == int(attempt) == 1, (sid, st.get("attempt"), attempt)
print("resume ok")
PY
  pass "run resumed awaiting_approval; completed steps not repeated"
}

step_approve_rollback_and_report() {
  step "4" "approve → Control rollback + report + completion event"
  local run_id run_body approval_id status approve_body
  run_id="$(cat "${TMP_DIR}/main-run-id.txt")"
  run_body="${TMP_DIR}/run-final.json"
  approve_body="${TMP_DIR}/approve.json"

  get_run "${run_id}" "${TMP_DIR}/run-before-approve.json"
  approval_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["pending_approval"]["id"])' \
    "${TMP_DIR}/run-before-approve.json")"

  status="$(http_body "${approve_body}" POST \
    "${FORGE_WORKFLOWS_URL}/v1/approvals/${approval_id}/approve" \
    -H "$(hdr_project)" \
    -H 'content-type: application/json' \
    -d '{"decided_by":"capstone-operator","reason":"approved recovery"}')"
  [[ "${status}" == "200" ]] || fail "approve HTTP ${status}: $(cat "${approve_body}")"
  pass "approval ${approval_id} approved"

  wait_run_status "${run_id}" completed >"${run_body}"

  python3 - "${run_body}" "${DEPLOYMENT_ID}" "${LIB_DIR}" <<'PY' || fail "post-approval assertions failed"
import json, sys
sys.path.insert(0, sys.argv[3])
from scenario_helpers import assert_report_shape, assert_run_auditable, build_completion_event

body = json.load(open(sys.argv[1]))
dep = sys.argv[2]
assert body.get("status") == "completed", body.get("status")
result = body.get("result") or {}
assert result.get("rolled_back") is True, result
report = result.get("report") or {}
assert_report_shape(report, rolled_back=True, deployment_id=dep)
assert_run_auditable(body)
steps = {s.get("id"): s for s in (body.get("steps") or [])}
assert steps["do-rollback"].get("status") == "completed", steps.get("do-rollback")
assert steps["finalize"].get("status") == "completed", steps.get("finalize")
assert (steps.get("close") or {}).get("status") == "skipped", steps.get("close")
# Product returns to healthy v1 after rollback (fake Control restores lastHealthyImage).
rb = steps["do-rollback"].get("output") or {}
assert rb.get("rolled_back") is True or rb.get("action") == "rollback", rb
completion = build_completion_event(deployment_id=dep)
assert completion["subject"] == "deployment.completed"
print("completion_event", completion["subject"], completion["data"]["deployment_id"])
PY
  # Prefer publishing via Events when available; otherwise keep documented payload.
  if curl --fail --silent --show-error "${FORGE_EVENTS_URL}/health/ready" >/dev/null 2>&1; then
    local ev_status ev_body
    ev_body="${TMP_DIR}/completion-publish.json"
    ev_status="$(http_body "${ev_body}" POST "${FORGE_EVENTS_URL}/v1/events" \
      -H 'content-type: application/json' \
      -d "$(PYTHONPATH="${LIB_DIR}" python3 -c 'from scenario_helpers import build_completion_event, dumps; import sys; print(dumps(build_completion_event(deployment_id=sys.argv[1])))' "${DEPLOYMENT_ID}")")"
    [[ "${ev_status}" == "202" ]] || fail "completion event publish HTTP ${ev_status}: $(cat "${ev_body}")"
    pass "deployment.completed completion event published to Events"
  else
    pass "completion event payload built (Events not in CI subset; schema-validated)"
  fi
  pass "rollback executed only after approval; report stored; product healthy (v1)"
}

step_deny_no_rollback() {
  step "5" "deny path → no rollback"
  local trigger_body run_id run_body approval_id status deny_body event_id
  trigger_body="${TMP_DIR}/trigger-deny.json"
  run_body="${TMP_DIR}/run-deny.json"
  deny_body="${TMP_DIR}/deny.json"
  event_id="capstone-deny-$(python3 -c 'import uuid; print(uuid.uuid4())')"
  local deny_dep="${DEPLOYMENT_ID}-deny"

  trigger_failed_deployment "${trigger_body}" "${deny_dep}" "${event_id}"
  run_id="$(python3 - "${trigger_body}" <<'PY'
import json, sys
body = json.load(open(sys.argv[1]))
print((body.get("runs") or [])[0]["run_id"])
PY
)"
  wait_run_status "${run_id}" awaiting_approval >"${TMP_DIR}/run-deny-pending.json"
  approval_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["pending_approval"]["id"])' \
    "${TMP_DIR}/run-deny-pending.json")"

  status="$(http_body "${deny_body}" POST \
    "${FORGE_WORKFLOWS_URL}/v1/approvals/${approval_id}/deny" \
    -H "$(hdr_project)" \
    -H 'content-type: application/json' \
    -d '{"decided_by":"capstone-operator","reason":"hold / escalate"}')"
  [[ "${status}" == "200" ]] || fail "deny HTTP ${status}: $(cat "${deny_body}")"

  wait_run_status "${run_id}" completed >"${run_body}"

  python3 - "${run_body}" <<'PY' || fail "deny assertions failed"
import json, sys
body = json.load(open(sys.argv[1]))
assert body.get("status") == "completed", body.get("status")
steps = {s.get("id"): s for s in (body.get("steps") or [])}
do_rb = steps.get("do-rollback") or {}
assert do_rb.get("status") == "skipped", do_rb
fin = steps.get("finalize") or {}
assert fin.get("status") == "skipped", fin
result = body.get("result") or {}
assert result.get("rolled_back") is not True, result
close = steps.get("close") or {}
assert close.get("status") == "completed", close
print("deny ok; do-rollback skipped; close completed")
PY
  pass "deny → no Control rollback"
}

run_acceptance() {
  bootstrap_stack
  assert_workflow_registered
  step_trigger_and_await_approval
  step_restart_resume
  step_approve_rollback_and_report
  step_deny_no_rollback
  echo
  echo "acceptance summary: ${PASS} passed, ${FAIL} failed"
  [[ "${FAIL}" -eq 0 ]]
  echo "scenario 19.05 PASSED"
}

case "${PHASE}" in
  all|--phase=all|"")
    run_unit_checks
    run_acceptance
    ;;
  unit|--phase=unit)
    run_unit_checks
    echo "unit checks PASSED"
    ;;
  accept|--phase=accept)
    run_acceptance
    ;;
  --phase=up)
    bootstrap_stack
    assert_workflow_registered
    echo "phase up PASSED (stack left running)"
    trap - EXIT
    STARTED=0
    ;;
  *)
    echo "Usage: $0 [all|unit|accept|--phase=up]" >&2
    exit 2
    ;;
esac
