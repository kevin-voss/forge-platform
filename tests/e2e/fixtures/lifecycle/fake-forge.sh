#!/usr/bin/env bash
set -euo pipefail
log="${FORGE_CALL_LOG:?FORGE_CALL_LOG must be set}"
# Record the subcommand + args for assertions.
printf '%s\n' "$*" >> "${log}"
if [[ "${1:-}" == "fail" ]]; then
  echo "fake forge deliberate failure" >&2
  exit 3
fi
echo "fake-forge ok: $*"
exit 0
