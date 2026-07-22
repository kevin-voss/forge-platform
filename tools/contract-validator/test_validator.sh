#!/usr/bin/env bash
# Unit/integration tests for tools/contract-validator (step 01.02).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TOOL_DIR="${ROOT_DIR}/tools/contract-validator"
VALIDATOR="${TOOL_DIR}/run.sh"
FIXTURE="${TOOL_DIR}/fixture_server.py"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-contract-XXXXXX")"
PASS=0
FAIL=0

cleanup() {
  if [[ -n "${FIXTURE_PID:-}" ]] && kill -0 "${FIXTURE_PID}" 2>/dev/null; then
    kill -KILL "${FIXTURE_PID}" 2>/dev/null || true
    wait "${FIXTURE_PID}" 2>/dev/null || true
  fi
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

start_fixture() {
  local mode="$1"
  local port="$2"
  python3 "${FIXTURE}" --host 127.0.0.1 --port "${port}" --mode "${mode}" \
    --service fixture --language go \
    >"${TMP_DIR}/fixture-${mode}.log" 2>&1 &
  FIXTURE_PID=$!

  local i
  for i in $(seq 1 50); do
    if curl -fsS --max-time 1 "http://127.0.0.1:${port}/health/live" >/dev/null 2>&1; then
      return 0
    fi
    if ! kill -0 "${FIXTURE_PID}" 2>/dev/null; then
      echo "Fixture exited early (mode=${mode}). Log:" >&2
      cat "${TMP_DIR}/fixture-${mode}.log" >&2 || true
      return 1
    fi
    sleep 0.1
  done
  echo "Timed out starting fixture mode=${mode} on port ${port}" >&2
  return 1
}

stop_fixture() {
  if [[ -n "${FIXTURE_PID:-}" ]] && kill -0 "${FIXTURE_PID}" 2>/dev/null; then
    kill -KILL "${FIXTURE_PID}" 2>/dev/null || true
    wait "${FIXTURE_PID}" 2>/dev/null || true
  fi
  FIXTURE_PID=""
}

expect_exit() {
  local name="$1"
  local want="$2"
  shift 2
  set +e
  "$@" >"${TMP_DIR}/${name}.out" 2>"${TMP_DIR}/${name}.err"
  local got=$?
  set -e
  if [[ "${got}" -eq "${want}" ]]; then
    echo "  PASS ${name} (exit ${got})"
    PASS=$((PASS + 1))
  else
    echo "  FAIL ${name}: expected exit ${want}, got ${got}" >&2
    echo "  --- stdout ---" >&2
    cat "${TMP_DIR}/${name}.out" >&2 || true
    echo "  --- stderr ---" >&2
    cat "${TMP_DIR}/${name}.err" >&2 || true
    FAIL=$((FAIL + 1))
  fi
}

echo "== contract-validator help =="
expect_exit help 0 "${VALIDATOR}" --help

echo "== fixture: compliant → validator exit 0 =="
start_fixture compliant 18099
# Valid log sample for schema check
cat >"${TMP_DIR}/good.jsonl" <<'EOF'
{"timestamp":"2026-07-22T14:30:00Z","level":"info","service":"fixture","message":"listening"}
EOF
expect_exit compliant_pass 0 \
  "${VALIDATOR}" \
  --base-url "http://127.0.0.1:18099" \
  --expect-service fixture \
  --expect-language go \
  --log-file "${TMP_DIR}/good.jsonl"
stop_fixture

echo "== fixture: missing /health/ready (503) → non-zero =="
start_fixture no_ready 18100
expect_exit no_ready_fail 1 \
  "${VALIDATOR}" \
  --base-url "http://127.0.0.1:18100" \
  --expect-service fixture \
  --expect-language go
stop_fixture

echo "== fixture: identity missing language → non-zero =="
start_fixture missing_language 18101
expect_exit missing_language_fail 1 \
  "${VALIDATOR}" \
  --base-url "http://127.0.0.1:18101" \
  --expect-service fixture \
  --expect-language go
stop_fixture

echo "== log line missing timestamp → non-zero =="
cat >"${TMP_DIR}/bad.jsonl" <<'EOF'
{"level":"info","service":"fixture","message":"listening"}
EOF
# Use skip-http with a dummy base-url; still need base-url because required.
# Start a compliant server so we can isolate the log failure, or use --skip-http.
expect_exit bad_log_fail 1 \
  "${VALIDATOR}" \
  --base-url "http://127.0.0.1:9" \
  --skip-http \
  --log-file "${TMP_DIR}/bad.jsonl"

echo "== connection refused → non-zero =="
expect_exit not_listening 1 \
  "${VALIDATOR}" \
  --base-url "http://127.0.0.1:9" \
  --expect-service fixture \
  --expect-language go

echo "== shutdown ignores SIGTERM beyond timeout → non-zero =="
start_fixture ignore_sigterm 18102
expect_exit shutdown_timeout_fail 1 \
  "${VALIDATOR}" \
  --base-url "http://127.0.0.1:18102" \
  --expect-service fixture \
  --expect-language go \
  --shutdown-pid "${FIXTURE_PID}" \
  --shutdown-timeout 1s
# Fixture may still be alive; force kill in stop_fixture
stop_fixture

echo "== graceful shutdown within grace → exit 0 =="
start_fixture compliant 18103
expect_exit shutdown_ok 0 \
  "${VALIDATOR}" \
  --base-url "http://127.0.0.1:18103" \
  --expect-service fixture \
  --expect-language go \
  --shutdown-pid "${FIXTURE_PID}" \
  --shutdown-timeout 5s
FIXTURE_PID=""  # already terminated by validator

echo
echo "Results: ${PASS} passed, ${FAIL} failed"
if [[ "${FAIL}" -ne 0 ]]; then
  exit 1
fi
echo "contract-validator tests passed."
