#!/usr/bin/env bash
# Entrypoint for the shared Forge runtime contract validator.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec python3 "${ROOT_DIR}/validate.py" "$@"
