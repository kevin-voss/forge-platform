#!/usr/bin/env bash
set -euo pipefail
log="${LIFECYCLE_ORDER_LOG:?LIFECYCLE_ORDER_LOG must be set}"
echo "teardown" >> "${log}"
echo "teardown ok"
