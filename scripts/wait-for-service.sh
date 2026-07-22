#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "Usage: $0 <url> [timeout_seconds]" >&2
  exit 1
}

if [[ $# -lt 1 ]]; then
  usage
fi

URL="$1"
TIMEOUT="${2:-60}"
START="$(date +%s)"

echo "Waiting for ${URL} (timeout ${TIMEOUT}s)..."

while true; do
  if curl -fsS --max-time 2 "${URL}" >/dev/null 2>&1; then
    echo "Ready: ${URL}"
    exit 0
  fi

  NOW="$(date +%s)"
  ELAPSED=$((NOW - START))
  if (( ELAPSED >= TIMEOUT )); then
    echo "Timed out waiting for ${URL}" >&2
    exit 1
  fi

  sleep 1
done
