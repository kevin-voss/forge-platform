#!/usr/bin/env bash
# Wrapper so contract-validator tests are discoverable under tests/.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
exec "${ROOT_DIR}/tools/contract-validator/test_validator.sh"
