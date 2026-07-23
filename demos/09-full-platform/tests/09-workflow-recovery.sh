#!/usr/bin/env bash
# workflow recovery test — deployment.failed → diagnose → awaiting_approval
# (full approve/deny/resume covered together with 10-rollback via break-release)
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/09-full-platform"
LIB_DIR="${DEMO_DIR}/lib"
WORKFLOWS_URL="${FORGE_WORKFLOWS_URL:-http://127.0.0.1:4302}"

echo "Workflows ready + incident-response registered..."
curl --fail --silent --show-error "${WORKFLOWS_URL}/health/ready" >/dev/null
body="$(mktemp "${TMPDIR:-/tmp}/capstone-wfs.XXXXXX.json")"
code="$(curl --silent --show-error --output "${body}" --write-out '%{http_code}' \
  "${WORKFLOWS_URL}/v1/workflows")"
[[ "${code}" == "200" ]] || {
  echo "list workflows HTTP ${code}: $(cat "${body}")" >&2
  rm -f "${body}"
  exit 1
}
python3 - "${body}" <<'PY'
import json, sys
body = json.load(open(sys.argv[1]))
wfs = {w.get("name"): w for w in (body.get("workflows") or [])}
assert "incident-response" in wfs, sorted(wfs)
trig = (wfs["incident-response"].get("trigger") or {})
assert trig.get("event") == "deployment.failed", trig
print("incident-response ok")
PY
rm -f "${body}"

echo "Workflow definition on disk validates..."
DEMO_DIR="${DEMO_DIR}" PYTHONPATH="${LIB_DIR}" python3 - <<'PY'
import os
from pathlib import Path
from scenario_helpers import load_and_validate_workflow

demo = Path(os.environ["DEMO_DIR"])
validated = load_and_validate_workflow(demo / "scenario" / "incident-response.yaml")
assert validated["name"] == "incident-response"
assert "approve-rollback" in validated["step_ids"]
assert "do-rollback" in validated["step_ids"]
print("workflow yaml ok")
PY

echo "workflow-recovery prechecks ok (full loop in 10-rollback)"
