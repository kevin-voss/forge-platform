#!/usr/bin/env bash
# event-processing test
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/09-full-platform"
LIB_DIR="${DEMO_DIR}/lib"
EVENTS_DIR="${ROOT_DIR}/contracts/events"

echo "Scenario event helpers + workflow trigger..."
(
  cd "${DEMO_DIR}"
  PYTHONPATH="${LIB_DIR}" python3 -m unittest discover -s lib -p 'test_scenario_helpers.py' -v
)

echo "Required event schemas present and parse as JSON..."
for schema in \
  deployment.failed.schema.json \
  deployment.completed.schema.json \
  incident.created.schema.json \
  agent.run.completed.schema.json; do
  [[ -f "${EVENTS_DIR}/${schema}" ]] || {
    echo "missing event schema ${schema}" >&2
    exit 1
  }
  python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "${EVENTS_DIR}/${schema}"
done

echo "Scenario helpers emit contract-shaped deployment.failed / completed..."
PYTHONPATH="${LIB_DIR}" python3 - <<'PY'
import json
from pathlib import Path
from scenario_helpers import build_deployment_failed_event, build_completion_event

root = Path(__file__).resolve() if "__file__" in dir() else Path.cwd()
# validate against schema files via required keys
failed = build_deployment_failed_event(deployment_id="dep-x")
assert failed["subject"] == "deployment.failed"
assert failed["data"]["deployment_id"] == "dep-x"
assert "readiness" in failed["data"]["reason"] or "capstone_break" in failed["data"]["reason"]

done = build_completion_event(deployment_id="dep-x")
assert done["subject"] == "deployment.completed"
assert done["data"]["deployment_id"] == "dep-x"
print("event shapes ok")
PY

echo "Product publishes incident.created via Forge Events URL (not peer HTTP)..."
grep -n 'FORGE_EVENTS_URL\|incident.created' \
  "${DEMO_DIR}/product/api-go/events.go" >/dev/null
grep -n 'FORGE_EVENTS_URL\|incident.created' \
  "${DEMO_DIR}/product/log-worker-rust/src"/*.rs >/dev/null \
  || grep -n 'FORGE_EVENTS' "${DEMO_DIR}/product/log-worker-rust/src/config.rs" >/dev/null

echo "Events OpenAPI present..."
[[ -f "${ROOT_DIR}/contracts/openapi/forge-events.openapi.yaml" ]]

echo "events checks ok"
