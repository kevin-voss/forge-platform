#!/usr/bin/env bash
set -euo pipefail
log="${LIFECYCLE_ORDER_LOG:?LIFECYCLE_ORDER_LOG must be set}"
echo "deploy" >> "${log}"
echo "deploy ok"
