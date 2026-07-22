#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

echo "Stopping Compose stack and removing local volumes..."
docker compose down -v --remove-orphans

echo "Local platform state reset complete."
