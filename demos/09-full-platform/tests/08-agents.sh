#!/usr/bin/env bash
# agent-tool test — Memory-assisted investigator diagnosis cites telemetry
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/09-full-platform"

export FORGE_AI_SKIP_COMPOSE="${FORGE_AI_SKIP_COMPOSE:-1}"
export FORGE_AI_KEEP="${FORGE_AI_KEEP:-1}"
export FORGE_MODELS_BACKEND="${FORGE_MODELS_BACKEND:-fake}"
export FORGE_AGENTS_TOOLS_MODE="${FORGE_AGENTS_TOOLS_MODE:-fake}"

echo "Running ai/verify-diagnosis.sh (NN + investigator + approval-gated restart)..."
"${DEMO_DIR}/ai/verify-diagnosis.sh"

echo "Agents OpenAPI + Memory OpenAPI present..."
[[ -f "${ROOT_DIR}/contracts/openapi/forge-agents.openapi.yaml" ]]
[[ -f "${ROOT_DIR}/contracts/openapi/forge-memory.openapi.yaml" ]]

echo "agents checks ok"
