#!/usr/bin/env bash
# rollback test — approval required; approve → rollback+report; deny → no rollback;
# mid-run resume does not repeat steps. Agent diagnosis references telemetry.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/09-full-platform"

export FORGE_SCENARIO_SKIP_COMPOSE="${FORGE_SCENARIO_SKIP_COMPOSE:-1}"
export FORGE_SCENARIO_KEEP="${FORGE_SCENARIO_KEEP:-1}"
export FORGE_MODELS_BACKEND="${FORGE_MODELS_BACKEND:-fake}"
export FORGE_AGENTS_TOOLS_MODE="${FORGE_AGENTS_TOOLS_MODE:-fake}"
export FORGE_WORKFLOWS_AGENTS_MODE="${FORGE_WORKFLOWS_AGENTS_MODE:-fake}"
export FORGE_WORKFLOWS_CONTROL_MODE="${FORGE_WORKFLOWS_CONTROL_MODE:-fake}"

echo "Running scenario/break-release.sh accept (recovery + rollback + deny + resume)..."
# `accept` phase skips unit (already in 01-smoke) and runs the full loop.
"${DEMO_DIR}/scenario/break-release.sh" accept

echo "Workflows OpenAPI present..."
[[ -f "${ROOT_DIR}/contracts/openapi/forge-workflows.openapi.yaml" ]]

echo "rollback / recovery loop ok"
